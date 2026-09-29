package otel

import (
	"bytes"
	"context"
	"encoding/binary"
	"flag"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

var update = flag.Bool("update", false, "rewrite the golden files")

func fixture() *Batch {
	b := &Batch{
		Resource:     []Attr{S("service.name", "riverbench"), S("os.type", "linux"), B("river.bench.smoke", true)},
		ScopeName:    "riverbench/compress",
		ScopeVersion: "1",
		Start:        time.Unix(1700000000, 0).UTC(),
		Time:         time.Unix(1700000060, 500).UTC(),
	}
	base := []Attr{S("river.bench.layer", "zfs"), S("river.bench.setting", "zstd-3"), I("river.bench.block_size", 131072)}
	b.Add("river.bench.compression.ratio", "1", "Uncompressed bytes / compressed bytes", Gauge, 3.25, base...)
	b.Add("river.bench.compression.throughput", "By/s", "Single-core codec throughput", Gauge, 5.125e8,
		append(append([]Attr{}, base...), S("river.bench.direction", "compress"))...)
	b.Add("process.cpu.time", "s", "CPU time of the measured process", Counter, 1.5,
		S("cpu.mode", "user"), F("river.bench.weight", 0.5), S("river.bench.note", `a "quoted"\value`))
	b.Add("system.paging.operations", "{operation}", "Pages swapped", Counter, 42, S("system.paging.direction", "in"))
	b.Add("river.bench.compression.ratio", "1", "", Gauge, math.NaN(), base...) // never recorded
	return b
}

func golden(t *testing.T, name string, got []byte) {
	t.Helper()
	p := filepath.Join("testdata", name)
	if *update {
		if err := os.WriteFile(p, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("%v (run go test -update once)", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("%s differs from golden:\n--- got\n%s\n--- want\n%s", name, got, want)
	}
}

func TestOTLPJSONGolden(t *testing.T) {
	var buf bytes.Buffer
	if err := EncodeJSON(&buf, fixture()); err != nil {
		t.Fatal(err)
	}
	golden(t, "otlp.json", buf.Bytes())
}

func TestPrometheusGolden(t *testing.T) {
	var buf bytes.Buffer
	if err := EncodePrometheus(&buf, fixture()); err != nil {
		t.Fatal(err)
	}
	golden(t, "metrics.prom", buf.Bytes())
}

func TestPromNames(t *testing.T) {
	for _, c := range []struct {
		m    Metric
		want string
	}{
		{Metric{Name: "river.bench.compression.ratio", Unit: "1"}, "river_bench_compression_ratio"},
		{Metric{Name: "river.bench.compression.throughput", Unit: "By/s"}, "river_bench_compression_throughput_bytes_per_second"},
		{Metric{Name: "river.bench.compression.size", Unit: "By"}, "river_bench_compression_size_bytes"},
		{Metric{Name: "process.cpu.time", Unit: "s", Kind: Counter}, "process_cpu_time_seconds_total"},
		{Metric{Name: "system.paging.operations", Unit: "{operation}", Kind: Counter}, "system_paging_operations_total"},
		{Metric{Name: "river.bench.zram.slowdown", Unit: "1"}, "river_bench_zram_slowdown_ratio"},
		{Metric{Name: "river.bench.scan.throughput", Unit: "{row}/s"}, "river_bench_scan_throughput_per_second"},
		{Metric{Name: "river.bench.kernel.spread", Unit: "%"}, "river_bench_kernel_spread_percent"},
	} {
		if got := PromName(c.m); got != c.want {
			t.Errorf("PromName(%s %s) = %s, want %s", c.m.Name, c.m.Unit, got, c.want)
		}
	}
}

// ---- a minimal protobuf reader, enough to check the wire structure field by field.

type field struct {
	num int
	wt  int
	v   uint64
	b   []byte
}

func parse(t *testing.T, b []byte) []field {
	t.Helper()
	var out []field
	for len(b) > 0 {
		tag, n := binary.Uvarint(b)
		if n <= 0 {
			t.Fatalf("bad tag")
		}
		b = b[n:]
		f := field{num: int(tag >> 3), wt: int(tag & 7)}
		switch f.wt {
		case wtVarint:
			f.v, n = binary.Uvarint(b)
			b = b[n:]
		case wtFixed64:
			f.v = binary.LittleEndian.Uint64(b)
			b = b[8:]
		case wtLen:
			l, n := binary.Uvarint(b)
			b = b[n:]
			f.b = b[:l]
			b = b[l:]
		default:
			t.Fatalf("unexpected wire type %d", f.wt)
		}
		out = append(out, f)
	}
	return out
}

func only(fs []field, num int) []field {
	var out []field
	for _, f := range fs {
		if f.num == num {
			out = append(out, f)
		}
	}
	return out
}

func TestProtoStructure(t *testing.T) {
	b := fixture()
	top := parse(t, EncodeProto(b))
	if len(top) != 1 || top[0].num != 1 {
		t.Fatalf("want one resource_metrics, got %+v", top)
	}
	rm := parse(t, top[0].b)
	res := parse(t, only(rm, 1)[0].b)
	if len(only(res, 1)) != 3 {
		t.Errorf("resource attributes: got %d, want 3", len(only(res, 1)))
	}
	sm := parse(t, only(rm, 2)[0].b)
	scope := parse(t, only(sm, 1)[0].b)
	if string(only(scope, 1)[0].b) != "riverbench/compress" {
		t.Errorf("scope name wrong")
	}
	metrics := only(sm, 2)
	if len(metrics) != 4 {
		t.Fatalf("metrics: got %d, want 4", len(metrics))
	}
	// Metric 0: gauge, 1 point (the NaN was never recorded), value 3.25 at the batch time.
	m0 := parse(t, metrics[0].b)
	if string(only(m0, 1)[0].b) != "river.bench.compression.ratio" || string(only(m0, 3)[0].b) != "1" {
		t.Errorf("metric 0 name/unit wrong")
	}
	g := parse(t, only(m0, 5)[0].b)
	pts := only(g, 1)
	if len(pts) != 1 {
		t.Fatalf("gauge points: got %d, want 1", len(pts))
	}
	dp := parse(t, pts[0].b)
	if math.Float64frombits(only(dp, 4)[0].v) != 3.25 {
		t.Errorf("as_double wrong")
	}
	if only(dp, 3)[0].v != uint64(b.Time.UnixNano()) {
		t.Errorf("time_unix_nano wrong")
	}
	if len(only(dp, 2)) != 0 {
		t.Errorf("a gauge point must not carry start_time_unix_nano")
	}
	// An int attribute is AnyValue.int_value (field 3, varint).
	var sawInt bool
	for _, kv := range only(dp, 7) {
		f := parse(t, kv.b)
		if string(only(f, 1)[0].b) == "river.bench.block_size" {
			av := parse(t, only(f, 2)[0].b)
			sawInt = av[0].num == 3 && av[0].v == 131072
		}
	}
	if !sawInt {
		t.Errorf("int attribute not encoded as int_value")
	}
	// Metric 2: monotonic cumulative sum with a start time.
	m2 := parse(t, metrics[2].b)
	sum := parse(t, only(m2, 7)[0].b)
	if only(sum, 2)[0].v != 2 || only(sum, 3)[0].v != 1 {
		t.Errorf("sum must be CUMULATIVE and monotonic")
	}
	sdp := parse(t, only(sum, 1)[0].b)
	if only(sdp, 2)[0].v != uint64(b.Start.UnixNano()) {
		t.Errorf("sum start_time_unix_nano wrong")
	}
}

func TestPushIsOffWithoutEndpoint(t *testing.T) {
	if err := Push(context.Background(), "", "protobuf", nil, fixture()); err == nil {
		t.Fatal("Push with no endpoint must fail, not pick a default")
	}
	if err := Push(context.Background(), "collector:4318", "protobuf", nil, fixture()); err == nil {
		t.Fatal("Push must refuse a non-URL endpoint")
	}
}

func TestPushSendsOTLP(t *testing.T) {
	for _, format := range []string{"protobuf", "json"} {
		var gotType string
		var gotBody []byte
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotType = r.Header.Get("Content-Type")
			gotBody, _ = io.ReadAll(r.Body)
		}))
		err := Push(context.Background(), srv.URL+"/v1/metrics", format, map[string]string{"X-Test": "1"}, fixture())
		srv.Close()
		if err != nil {
			t.Fatalf("%s: %v", format, err)
		}
		want := map[string]string{"protobuf": "application/x-protobuf", "json": "application/json"}[format]
		if gotType != want || len(gotBody) == 0 {
			t.Errorf("%s: content-type %q, %d bytes", format, gotType, len(gotBody))
		}
	}
}
