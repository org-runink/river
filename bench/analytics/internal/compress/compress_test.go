package compress

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/org-runink/river/bench/analytics/internal/sysinfo"
)

func TestSettingNames(t *testing.T) {
	for _, s := range []string{"off", "lz4", "lz4-9", "zstd-3", "zstd-fast-1", "xz-6", "gzip-9", "zstd-19"} {
		st, err := ParseSetting(s)
		if err != nil {
			t.Fatal(err)
		}
		if st.Name() != s {
			t.Errorf("round trip %q -> %q", s, st.Name())
		}
	}
	if _, err := ParseSetting("brotli-5"); err == nil {
		t.Error("unknown codec accepted")
	}
}

func TestZFSModel(t *testing.T) {
	m := ZFSModel{Ashift: 12, EarlyAbort: true}
	const rec = 128 << 10
	z3 := Setting{"zstd", 3}
	// Compressible: stored compressed, rounded up to the 4 KiB sector.
	if r := m.Record(z3, rec, 30000, 50000, 35000); !r.Compressed || r.PSize != 32768 {
		t.Errorf("compressible record: %+v", r)
	}
	// Saves less than 1/8: stored raw.
	if r := m.Record(Setting{"lz4", 1}, rec, rec-rec/16, 0, 0); r.Compressed || r.PSize != rec {
		t.Errorf("1/8 rule: %+v", r)
	}
	// Early abort: LZ4 and zstd-1 both fail, the zstd-3 size is never consulted.
	if r := m.Record(z3, rec, 1000, rec, rec); !r.Aborted || r.Compressed {
		t.Errorf("early abort: %+v", r)
	}
	// LZ4 fails but zstd-1 fits: zstd-3 runs.
	if r := m.Record(z3, rec, 90000, rec, 100000); r.Aborted || !r.Compressed {
		t.Errorf("zstd-1 pass: %+v", r)
	}
	// Below the abort size, no early abort.
	if r := m.Record(z3, 64<<10, 1000, 64<<10, 64<<10); r.Aborted {
		t.Errorf("early abort below 128 KiB: %+v", r)
	}
	// A 1.5 KiB file: a 1536-byte block; compressing it to 700 bytes still costs a 4 KiB
	// sector, which is not smaller, so it is stored raw (and still takes 4 KiB).
	l := ZFSLogicalSize(1500, rec)
	if l != 1536 {
		t.Errorf("small file logical size %d", l)
	}
	if r := m.Record(z3, l, 700, 700, 700); r.Compressed || r.PSize != 4096 {
		t.Errorf("small file: %+v", r)
	}
	if r := m.Record(Setting{"off", 0}, rec, 0, 0, 0); r.PSize != rec || r.Compressed {
		t.Errorf("off: %+v", r)
	}
}

func TestZfsRecordsPadTail(t *testing.T) {
	sh := Shape{Files: []File{{Data: make([]byte, 300<<10)}, {Data: make([]byte, 1000)}}}
	recs := zfsRecords(sh, 128<<10)
	if len(recs) != 4 || len(recs[2]) != 128<<10 || len(recs[3]) != 1024 {
		t.Fatalf("records: %d, sizes %d %d", len(recs), len(recs[2]), len(recs[3]))
	}
}

func TestZram(t *testing.T) {
	z := DefaultZram
	if !SameFilled(make([]byte, 4096)) {
		t.Error("zero page is same-filled")
	}
	p := bytes.Repeat([]byte{1, 2, 3, 4, 5, 6, 7, 8}, 512)
	if !SameFilled(p) {
		t.Error("repeated word is same-filled")
	}
	p[4095] = 0
	if SameFilled(p) {
		t.Error("page with a different word is not same-filled")
	}
	if z.Stored(1000) != 1008 || z.Stored(3264) != 4096 || z.Stored(3263) != 3264 {
		t.Errorf("stored sizes: %d %d %d", z.Stored(1000), z.Stored(3264), z.Stored(3263))
	}
}

func TestParseBench(t *testing.T) {
	zstdOut := []byte("Loading s.bin...       \r\r |-s.bin : 2366880 -> \r |-s.bin : 2366880 ->   1164884 (x2.032),  214.7 MB/s \r |-s.bin             :   2366880 ->   1164884 (x2.032),  214.7 MB/s,  906.7 MB/s\r 3#\n")
	sp, err := ParseBench(zstdOut)
	if err != nil {
		t.Fatal(err)
	}
	if sp.CompressBps != 214.7e6 || sp.DecompressBps != 906.7e6 || sp.Ratio < 2.03 || sp.Ratio > 2.04 {
		t.Errorf("zstd: %+v", sp)
	}
	lz4Out := []byte("using blocks of size 128 KB \n\r |-s.bin : 2366880 ->   1538228 (1.539), 557.9 MB/s\r =-s.bin : 2366880 ->   1538228 (1.539), 557.9 MB/s, 3452.9 MB/s\r |-s.bin : 2366880 ->   1538228 (1.539), 560.1 MB/s, 3506.6 MB/s\r 1#\n")
	sp, err = ParseBench(lz4Out)
	if err != nil {
		t.Fatal(err)
	}
	if sp.CompressBps != 560.1e6 || sp.DecompressBps != 3506.6e6 {
		t.Errorf("lz4 must take the last line: %+v", sp)
	}
	if _, err := ParseBench([]byte("garbage")); err == nil {
		t.Error("no result must be an error")
	}
}

func TestParseTimings(t *testing.T) {
	out := []byte("Run Time (s): real 0.163 user 0.603163 sys 0.123110\nx\nRun Time (s): real 1.5 user 2 sys 0.5\n")
	tm := ParseTimings(out)
	if len(tm) != 2 || tm[1] != (Timing{1.5, 2, 0.5}) {
		t.Errorf("%+v", tm)
	}
}

func readVarint(b []byte) (int64, []byte) {
	u, n := binary.Uvarint(b)
	return int64(u>>1) ^ -int64(u&1), b[n:]
}

func TestAvroContainer(t *testing.T) {
	cols := []AvroCol{{"k", "long"}, {"p", "double"}, {"s", "string"}, {"d", "date"}}
	rows := [][]string{{"-3", "1.5", "hi", "1970-01-11"}, {"7", "2", "", "1969-12-31"}}
	var buf bytes.Buffer
	var sync [16]byte
	copy(sync[:], "0123456789abcdef")
	if err := EncodeAvro(&buf, "r", cols, rows, 10, sync); err != nil {
		t.Fatal(err)
	}
	b := buf.Bytes()
	if !bytes.HasPrefix(b, []byte("Obj\x01")) {
		t.Fatal("magic")
	}
	b = b[4:]
	n, b := readVarint(b)
	if n != 2 {
		t.Fatalf("metadata entries %d", n)
	}
	meta := map[string]string{}
	for i := 0; i < 2; i++ {
		var l int64
		l, b = readVarint(b)
		k := string(b[:l])
		b = b[l:]
		l, b = readVarint(b)
		meta[k] = string(b[:l])
		b = b[l:]
	}
	if meta["avro.codec"] != "null" || !strings.Contains(meta["avro.schema"], `"logicalType":"date"`) {
		t.Errorf("metadata %v", meta)
	}
	var sch map[string]any
	if err := json.Unmarshal([]byte(meta["avro.schema"]), &sch); err != nil {
		t.Errorf("schema is not JSON: %v", err)
	}
	var end int64
	end, b = readVarint(b)
	if end != 0 || !bytes.Equal(b[:16], sync[:]) {
		t.Fatal("header end / sync")
	}
	b = b[16:]
	count, b := readVarint(b)
	size, b := readVarint(b)
	if count != 2 || int(size) != len(b)-16 {
		t.Fatalf("block count %d size %d (have %d)", count, size, len(b)-16)
	}
	k, b := readVarint(b)
	if k != -3 {
		t.Errorf("long %d", k)
	}
	b = b[8:] // double
	sl, b := readVarint(b)
	if string(b[:sl]) != "hi" {
		t.Errorf("string")
	}
	b = b[sl:]
	d, _ := readVarint(b)
	if d != 10 {
		t.Errorf("date %d", d)
	}
}

func TestWeightsDeterministic(t *testing.T) {
	a, b := Weights(1<<16, 1), Weights(1<<16, 1)
	if len(a) != 1<<16 || !bytes.Equal(a, b) || !bytes.HasPrefix(a, []byte("GGUF")) {
		t.Error("weights must be deterministic, sized and GGUF-shaped")
	}
	if bytes.Equal(a, Weights(1<<16, 2)) {
		t.Error("seed ignored")
	}
}

func TestDeltaLogIsJSONLines(t *testing.T) {
	for _, f := range DeltaLog(5, 1) {
		for _, line := range strings.Split(strings.TrimSpace(string(f.Data)), "\n") {
			var v map[string]any
			if err := json.Unmarshal([]byte(line), &v); err != nil {
				t.Fatalf("%s: %v: %s", f.Name, err, line)
			}
		}
		if len(f.Name) != len("00000000000000000000.json") {
			t.Errorf("commit file name %q", f.Name)
		}
	}
}

func TestNewc(t *testing.T) {
	var b bytes.Buffer
	if err := WriteNewc(&b, []File{{Name: "/usr/lib/x.ko", Data: []byte("abc")}}); err != nil {
		t.Fatal(err)
	}
	s := b.String()
	if !strings.HasPrefix(s, "070701") || !strings.Contains(s, "usr/lib/x.ko\x00") || !strings.Contains(s, "TRAILER!!!") {
		t.Errorf("newc: %q", s)
	}
	if b.Len()%4 != 0 {
		t.Errorf("newc length %d not 4-aligned", b.Len())
	}
}

func TestMaps(t *testing.T) {
	maps := `55d0c0000000-55d0c0021000 rw-p 00000000 00:00 0                          [heap]
7f0000000000-7f0000100000 rw-p 00000000 00:00 0
7f0000100000-7f0000200000 r--p 00000000 fd:01 123                        /usr/lib/libc.so.6
7f0000200000-7f0000201000 rw-p 00000000 fd:01 123                        /usr/lib/libc.so.6
7ffc00000000-7ffc00021000 rw-p 00000000 00:00 0                          [stack]
`
	regs, err := ParseMaps(strings.NewReader(maps))
	if err != nil || len(regs) != 5 {
		t.Fatalf("%v %d", err, len(regs))
	}
	want := []bool{true, true, false, false, false}
	for i, r := range regs {
		if r.Anonymous() != want[i] {
			t.Errorf("region %d (%s %q) anonymous=%v", i, r.Perms, r.Path, r.Anonymous())
		}
	}
	if !Resident(1<<63) || !Resident(1<<62) || Resident(12345) {
		t.Error("pagemap flags")
	}
}

func TestSnapshotSelf(t *testing.T) {
	keep := bytes.Repeat([]byte("riverbench"), 1<<18) // 2.5 MiB resident
	pages, err := Snapshot(os.Getpid(), 64)
	if err != nil {
		t.Skipf("no /proc access here: %v", err)
	}
	if len(pages) == 0 || len(pages[0]) != 4096 {
		t.Errorf("pages %d", len(pages))
	}
	_ = keep[0]
}

func fixtureReport() *Report {
	r := &Report{Label: "t", Started: time.Unix(1, 0).UTC(), Finished: time.Unix(2, 0).UTC(),
		Host:       sysinfo.Info{KernelRelease: "k", CPUModel: "cpu", MemTotalKiB: 1},
		Provenance: Provenance{RunID: "r1", HostKind: "other"}}
	r.Rows = []Row{
		{Layer: "zfs", Shape: "csv", Setting: "zstd-3", BlockSize: 131072, Method: "model", AllocRatio: 3, CompressBps: 2e8, DecompressBps: 9e8,
			Fractions: map[string]float64{"stored_raw": 0.1}},
		{Layer: "kernel", Shape: "vmlinux", Setting: "zstd-19", Method: "stream", Ratio: 4, DecompressS: 0.1, CompressCPU: [2]float64{1, 0.5}},
	}
	return r
}

func TestBatchUsesOnlyCatalogueNames(t *testing.T) {
	b := fixtureReport().Batch()
	if len(b.Metrics) == 0 {
		t.Fatal("no metrics")
	}
	for _, m := range b.Metrics {
		d, ok := DefByName(m.Name)
		if !ok {
			t.Errorf("metric %s is not in the catalogue", m.Name)
			continue
		}
		if d.Unit != m.Unit || d.Kind != m.Kind {
			t.Errorf("metric %s: unit/kind differ from the catalogue", m.Name)
		}
	}
	var sawVersion bool
	for _, a := range b.Resource {
		if a.Key == "river.bench.schema.version" && strings.HasSuffix(a.Str, "catalogue."+CatalogueVersion) {
			sawVersion = true
		}
	}
	if !sawVersion {
		t.Error("resource must carry river.bench.schema.version")
	}
}

func TestJSONIsDeterministic(t *testing.T) {
	var a, b bytes.Buffer
	if err := fixtureReport().WriteJSON(&a); err != nil {
		t.Fatal(err)
	}
	if err := fixtureReport().WriteJSON(&b); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a.Bytes(), b.Bytes()) {
		t.Error("two identical reports serialise differently")
	}
	var r Report
	if err := json.Unmarshal(a.Bytes(), &r); err != nil {
		t.Fatal(err)
	}
	for i := 1; i < len(r.Metrics); i++ {
		if r.Metrics[i-1].Key() > r.Metrics[i].Key() {
			t.Fatal("metrics are not sorted")
		}
	}
}

func TestCompare(t *testing.T) {
	a, b := fixtureReport(), fixtureReport()
	var buf bytes.Buffer
	_ = a.WriteJSON(&buf)
	b.Rows[0].AllocRatio = 2.5  // ratio down 16.7%: regression
	b.Rows[0].CompressBps = 3e8 // throughput up 50%: improvement
	b.Rows[1].DecompressS = 0.102
	_ = b.WriteJSON(&buf)
	c := Compare(a, b, 5)
	if len(c.Regressions) != 1 || c.Regressions[0].Name != MAllocRatio.Name || c.Regressions[0].DeltaPct != -16.67 {
		t.Errorf("regressions: %+v", c.Regressions)
	}
	if len(c.Improvements) != 1 || c.Improvements[0].Name != MThroughput.Name {
		t.Errorf("improvements: %+v", c.Improvements)
	}
	if c.Unchanged == 0 || !c.Comparable {
		t.Errorf("unchanged %d comparable %v", c.Unchanged, c.Comparable)
	}
	// Lower-is-better: a duration that grows is a regression.
	b.Rows[1].DecompressS = 0.2
	_ = b.WriteJSON(&buf)
	c = Compare(a, b, 5)
	found := false
	for _, x := range c.Regressions {
		if x.Name == MDuration.Name && x.Better == "lower" {
			found = true
		}
	}
	if !found {
		t.Errorf("slower decompression must be a regression: %+v", c.Regressions)
	}
}

func TestBootReadRange(t *testing.T) {
	rows := []Row{
		{Setting: "lz4", CompBytes: 27e6, DecompressS: 0.045},
		{Setting: "zstd-19", CompBytes: 17e6, DecompressS: 0.12},
		{Setting: "xz", CompBytes: 15e6, DecompressS: 0.9},
	}
	lo, hi := BootReadRange(&rows[1], rows)
	// xz wins below 2e6/0.78 = 2.6 MB/s; lz4 wins above 10e6/0.075 = 133 MB/s.
	if lo < 2.5e6 || lo > 2.7e6 || hi < 130e6 || hi > 136e6 {
		t.Errorf("range %.0f..%.0f", lo, hi)
	}
}

func TestRecommendPicksByDeclaredRule(t *testing.T) {
	r := &Report{}
	for _, x := range []struct {
		s    string
		a, c float64
	}{{"off", 1, 0}, {"lz4", 2.0, 600e6}, {"zstd-3", 2.6, 300e6}, {"zstd-19", 3.0, 5e6}} {
		r.Rows = append(r.Rows, Row{Layer: "zfs", Method: "model", Shape: "os-files", Setting: x.s, BlockSize: 128 << 10,
			AllocRatio: x.a, CompressBps: x.c, DecompressBps: 2e9})
	}
	Recommend(r, DefaultRules)
	var got string
	for _, rec := range r.Recommendations {
		if rec.Layer == "zfs" && strings.HasPrefix(rec.Scope, "<pool>/ROOT") {
			got = rec.Setting
		}
	}
	// zstd-19 has the best ratio but is under the CPU floor; zstd-3 beats lz4 on ratio.
	if got != "zstd-3" {
		t.Errorf("boot environment pick %q, want zstd-3 (%+v)", got, r.Recommendations)
	}
}

// The README's metric catalogue is generated by `riverbench catalogue`; keep them equal.
func TestReadmeCatalogueInSync(t *testing.T) {
	b, err := os.ReadFile("../../README.md")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), CatalogueMarkdown()) {
		t.Error("bench/analytics/README.md metric catalogue is stale: paste the output of `riverbench catalogue`")
	}
}
