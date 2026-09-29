package compress

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

// Options configures a compress run.
type Options struct {
	Work         string // scratch directory (large: keep it off a tmpfs)
	SampleBytes  int    // bytes per data shape
	BenchSeconds int    // minimum seconds per codec benchmark direction
	Reps         int    // repetitions for whole-stream timings
	Duck         Duck   // Bin empty: DuckDB-based shapes and layers are skipped
	Tools        Tools
	Self         string // this executable (for the memory-hold child)

	ZFSSettings  []Setting
	RecordSizes  []int
	ZramSettings []Setting
	ZramPages    int

	KernelPkg, ZFSPkg, ZFSUtilsPkg string
	ModuleCount                    int // modules sampled for the module-compression comparison
	StreamCap                      int // bytes kept of the kernel image and initramfs (0: all; smoke)
	DeltaCommits                   int
	Shapes                         map[string]bool // restrict the corpus to these shapes (nil: all)

	Progress func(format string, a ...any)
}

func (o Options) progress(f string, a ...any) {
	if o.Progress != nil {
		o.Progress(f, a...)
	}
}

// DefaultZFSSettings are the OpenZFS compression values compared.
var DefaultZFSSettings = []Setting{{"off", 0}, {"lz4", 1}, {"zstd-fast", 1}, {"zstd", 1}, {"zstd", 3},
	{"zstd", 6}, {"zstd", 9}, {"zstd", 19}}

// DefaultZramSettings are the zram codecs measurable in user space (lzo-rle has no
// command-line tool; the on-box pressure run measures it in the kernel).
var DefaultZramSettings = []Setting{{"lz4", 1}, {"zstd", 1}, {"zstd", 3}}

// ---- corpus

func truncate(b []byte, n int) []byte {
	if len(b) > n {
		return b[:n]
	}
	return b
}

// readTree reads regular files under roots in sorted path order until n bytes, skipping
// files larger than maxFile and anything unreadable. Deterministic for a given tree.
func readTree(roots []string, n, maxFile int, keep func(string) bool) []File {
	var paths []string
	for _, root := range roots {
		_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.Type().IsRegular() && (keep == nil || keep(p)) {
				paths = append(paths, p)
			}
			return nil
		})
	}
	sort.Strings(paths)
	// Spread the sample over the whole tree rather than its alphabetical head.
	var out []File
	total := 0
	stride := max(1, len(paths)/max(1, n/(64<<10)))
	for pass := 0; pass < stride && total < n; pass++ {
		for i := pass; i < len(paths) && total < n; i += stride {
			st, err := os.Stat(paths[i])
			if err != nil || st.Size() == 0 || st.Size() > int64(maxFile) {
				continue
			}
			b, err := os.ReadFile(paths[i])
			if err != nil {
				continue
			}
			out = append(out, File{Name: paths[i], Data: b})
			total += len(b)
		}
	}
	return out
}

// Corpus builds every data shape it can and records the ones it cannot.
func Corpus(o Options, r *Report) ([]Shape, error) {
	var shapes []Shape
	n := o.SampleBytes
	if err := os.MkdirAll(o.Work, 0o755); err != nil {
		return nil, err
	}
	if o.Duck.Bin != "" {
		o.progress("corpus: TPC-H lineitem shapes from %s", o.Duck.DB)
		csvPath := filepath.Join(o.Work, "lineitem.csv")
		jsonPath := filepath.Join(o.Work, "lineitem.ndjson")
		pqSnappy := filepath.Join(o.Work, "lineitem-snappy.parquet")
		pqZstd := filepath.Join(o.Work, "lineitem-zstd.parquet")
		sql := fmt.Sprintf(`COPY (SELECT * FROM lineitem LIMIT %d) TO %s (FORMAT csv, HEADER);
COPY (SELECT * FROM lineitem LIMIT %d) TO %s (FORMAT json);
COPY lineitem TO %s (FORMAT parquet, COMPRESSION snappy);
COPY lineitem TO %s (FORMAT parquet, COMPRESSION zstd);
`, n/100+1000, sqlString(csvPath), n/250+1000, sqlString(jsonPath), sqlString(pqSnappy), sqlString(pqZstd))
		if _, _, err := o.Duck.Exec(sql, nil); err != nil {
			return nil, err
		}
		for _, s := range []struct{ name, path, desc string }{
			{"parquet-snappy", pqSnappy, "TPC-H lineitem, Parquet, snappy pages (DuckDB defaults)"},
			{"parquet-zstd", pqZstd, "TPC-H lineitem, Parquet, zstd pages"},
			{"csv", csvPath, "TPC-H lineitem, CSV with header"},
			{"ndjson", jsonPath, "TPC-H lineitem, newline-delimited JSON"},
		} {
			b, err := os.ReadFile(s.path)
			if err != nil {
				return nil, err
			}
			shapes = append(shapes, Shape{Name: s.name, Desc: s.desc, Files: []File{{Name: filepath.Base(s.path), Data: truncate(b, n)}}})
		}
		hdr, rows, err := ReadCSV(shapes[2].Files[0].Data)
		if err != nil {
			return nil, err
		}
		if len(hdr) != len(LineitemAvroCols) {
			return nil, fmt.Errorf("lineitem CSV has %d columns, want %d", len(hdr), len(LineitemAvroCols))
		}
		var av bytes.Buffer
		if err := EncodeAvro(&av, "lineitem", LineitemAvroCols, rows, 4000, [16]byte{'r', 'i', 'v', 'e', 'r', 'b', 'e', 'n', 'c', 'h'}); err != nil {
			return nil, err
		}
		shapes = append(shapes, Shape{Name: "avro", Desc: "TPC-H lineitem, Avro object container, uncompressed blocks",
			Files: []File{{Name: "lineitem.avro", Data: av.Bytes()}}})

		// Delta Lake: the JSON commit log, and a Parquet checkpoint of it.
		logDir := filepath.Join(o.Work, "_delta_log")
		_ = os.RemoveAll(logDir)
		if err := os.MkdirAll(logDir, 0o755); err != nil {
			return nil, err
		}
		commits := DeltaLog(o.DeltaCommits, 7)
		for _, c := range commits {
			if err := os.WriteFile(filepath.Join(logDir, c.Name), c.Data, 0o644); err != nil {
				return nil, err
			}
		}
		shapes = append(shapes, Shape{Name: "delta-log", Desc: fmt.Sprintf("Delta Lake _delta_log: %d small JSON commit files", len(commits)), Files: commits})
		ckpt := filepath.Join(o.Work, "checkpoint.parquet")
		if _, _, err := o.Duck.Exec(fmt.Sprintf("COPY (SELECT * FROM read_json_auto(%s, union_by_name=true)) TO %s (FORMAT parquet, COMPRESSION snappy);\n",
			sqlString(filepath.Join(logDir, "*.json")), sqlString(ckpt)), nil); err != nil {
			return nil, err
		}
		cb, err := os.ReadFile(ckpt)
		if err != nil {
			return nil, err
		}
		shapes = append(shapes, Shape{Name: "delta-checkpoint", Desc: "Delta Lake checkpoint: the log's actions as one snappy Parquet file", Files: []File{{Name: "checkpoint.parquet", Data: cb}}})
	} else {
		r.Skip("corpus: parquet, csv, ndjson, avro, delta-log", "no DuckDB binary (run.sh fetches the pinned release)")
	}

	o.progress("corpus: model weights, images, OS files, source code, container layers")
	shapes = append(shapes, Shape{Name: "model-weights", Desc: "GGUF-shaped synthetic weights: bf16 + Q8_0 + Q4_0 of N(0, 0.02)",
		Files: []File{{Name: "weights.gguf", Data: Weights(n, 1)}}})
	imgs, err := Images(n, 11)
	if err != nil {
		return nil, err
	}
	shapes = append(shapes, Shape{Name: "images-jpeg", Desc: "1920x1080 photo-like JPEGs, quality 90", Files: imgs})

	osFiles := readTree([]string{"/usr/bin", "/usr/lib"}, n, 64<<20, func(p string) bool {
		return !strings.HasSuffix(p, ".zst") && !strings.HasSuffix(p, ".gz") && !strings.HasSuffix(p, ".xz")
	})
	if len(osFiles) > 0 {
		shapes = append(shapes, Shape{Name: "os-files", Desc: "files of this host's /usr/bin and /usr/lib (binaries, libraries, data)", Files: osFiles})
		var tb bytes.Buffer
		gz, _ := gzip.NewWriterLevel(&tb, gzip.DefaultCompression)
		tw := tar.NewWriter(gz)
		for _, f := range osFiles {
			_ = tw.WriteHeader(&tar.Header{Name: strings.TrimPrefix(f.Name, "/"), Mode: 0o755, Size: int64(len(f.Data)), ModTime: time.Unix(0, 0), Format: tar.FormatPAX})
			_, _ = tw.Write(f.Data)
		}
		_ = tw.Close()
		_ = gz.Close()
		shapes = append(shapes, Shape{Name: "container-layers", Desc: "the os-files sample as one tar.gz layer blob (an OCI image layer as a registry or content store keeps it)",
			Files: []File{{Name: "layer.tar.gz", Data: tb.Bytes()}}})
	} else {
		r.Skip("corpus: os-files, container-layers", "/usr/bin and /usr/lib unreadable")
	}
	goroot := runtime.GOROOT() // empty in a -trimpath build: ask the toolchain
	if goroot == "" {
		if out, err := exec.Command("go", "env", "GOROOT").Output(); err == nil {
			goroot = strings.TrimSpace(string(out))
		}
	}
	var src []File
	if goroot != "" {
		src = readTree([]string{filepath.Join(goroot, "src")}, n, 4<<20, nil)
	}
	if len(src) > 0 {
		shapes = append(shapes, Shape{Name: "source-code", Desc: "the Go toolchain's source tree: many small text files", Files: src})
	} else {
		r.Skip("corpus: source-code", "no Go toolchain source tree found")
	}
	if o.Shapes != nil {
		var keep []Shape
		for _, s := range shapes {
			if o.Shapes[s.Name] {
				keep = append(keep, s)
			}
		}
		shapes = keep
	}
	return shapes, nil
}

// ---- ZFS (codec measured, allocation modelled)

// zfsRecords cuts a shape's files into records the way OpenZFS stores them.
func zfsRecords(s Shape, recordsize int) [][]byte {
	var recs [][]byte
	for _, f := range s.Files {
		if len(f.Data) < recordsize {
			b := make([]byte, ZFSLogicalSize(len(f.Data), recordsize))
			copy(b, f.Data)
			recs = append(recs, b)
			continue
		}
		for off := 0; off < len(f.Data); off += recordsize {
			end := min(off+recordsize, len(f.Data))
			if end-off == recordsize {
				recs = append(recs, f.Data[off:end])
			} else { // the tail block of a multi-block file is a full, zero-padded record
				b := make([]byte, recordsize)
				copy(b, f.Data[off:end])
				recs = append(recs, b)
			}
		}
	}
	return recs
}

func medianFileSize(s Shape) int {
	sz := make([]int, len(s.Files))
	for i, f := range s.Files {
		sz[i] = len(f.Data)
	}
	sort.Ints(sz)
	return sz[len(sz)/2]
}

// RunZFS measures every ZFS setting on every shape at every recordsize.
func RunZFS(o Options, shapes []Shape, r *Report) error {
	model := ZFSModel{Ashift: ZFSAshift, EarlyAbort: true}
	for _, sh := range shapes {
		concat := filepath.Join(o.Work, "concat-"+sh.Name)
		if err := os.WriteFile(concat, sh.Concat(), 0o600); err != nil {
			return err
		}
		for _, rs := range o.RecordSizes {
			recs := zfsRecords(sh, rs)
			var logical int64
			for _, b := range recs {
				logical += int64(len(b))
			}
			benchBlock := rs
			if len(sh.Files) > 1 && medianFileSize(sh) < rs {
				benchBlock = max(4096, medianFileSize(sh))
			}
			sizes := map[string][]int{}
			speeds := map[string]Speed{}
			need := append([]Setting{{"lz4", 1}, {"zstd", 1}}, o.ZFSSettings...)
			for _, st := range need {
				if st.Codec == "off" || sizes[st.Name()] != nil {
					continue
				}
				o.progress("zfs %s rs=%s %s", sh.Name, bsStr(rs), st.Name())
				bs, err := BlockSizes(o.Tools, st, recs, o.Work)
				if err != nil {
					r.Skip(fmt.Sprintf("zfs %s %s", sh.Name, st.Name()), err.Error())
					continue
				}
				for i := range bs {
					bs[i] -= FrameOverhead("zfs", st, len(recs[i]))
				}
				sizes[st.Name()] = bs
				sp, err := BenchSpeed(o.Tools, st, concat, benchBlock, o.BenchSeconds)
				if err != nil {
					r.Skip(fmt.Sprintf("zfs speed %s %s", sh.Name, st.Name()), err.Error())
					continue
				}
				speeds[st.Name()] = sp
			}
			lz4s, z1s := sizes["lz4"], sizes["zstd-1"]
			lz4sp, z1sp := speeds["lz4"], speeds["zstd-1"]
			for _, st := range o.ZFSSettings {
				row := Row{Layer: "zfs", Shape: sh.Name, Setting: st.Name(), BlockSize: rs, Method: "model",
					Files: len(sh.Files), OrigBytes: sh.Bytes(), Fractions: map[string]float64{}}
				if st.Codec == "off" {
					var alloc int64
					for _, b := range recs {
						alloc += int64(roundUp(len(b), 1<<ZFSAshift))
					}
					row.AllocBytes, row.AllocRatio, row.Ratio = alloc, float64(sh.Bytes())/float64(alloc), 1
					r.Rows = append(r.Rows, row)
					continue
				}
				cs, ok := sizes[st.Name()]
				sp, ok2 := speeds[st.Name()]
				if !ok || !ok2 || lz4s == nil || z1s == nil {
					continue
				}
				r.Rows = append(r.Rows, Row{Layer: "zfs-codec", Shape: sh.Name, Setting: st.Name(), BlockSize: benchBlock,
					Method: "codec-bench", Ratio: sp.Ratio, CompressBps: sp.CompressBps, DecompressBps: sp.DecompressBps})
				var comp, alloc int64
				var ctime, dtime float64
				var raw, aborted int
				for i, b := range recs {
					l := len(b)
					comp += int64(cs[i])
					rec := model.Record(st, l, cs[i], lz4s[i], z1s[i])
					alloc += int64(rec.PSize)
					earlyPath := st.Codec == "zstd" && st.Level >= earlyAbortMinLevel && l >= earlyAbortMinSize
					switch {
					case rec.Aborted:
						aborted++
						ctime += float64(l)/lz4sp.CompressBps + float64(l)/z1sp.CompressBps
					case earlyPath && !model.fits(lz4s[i], l):
						ctime += float64(l)/lz4sp.CompressBps + float64(l)/z1sp.CompressBps + float64(l)/sp.CompressBps
					case earlyPath:
						ctime += float64(l)/lz4sp.CompressBps + float64(l)/sp.CompressBps
					default:
						ctime += float64(l) / sp.CompressBps
					}
					if rec.Compressed {
						dtime += float64(l) / sp.DecompressBps
					} else {
						raw++
					}
				}
				row.CompBytes, row.AllocBytes = comp, alloc
				row.Ratio = float64(logical) / float64(comp)
				// Against the file bytes, not the padded records: a zero-padded tail record
				// that compression removes is not a compression gain.
				row.AllocRatio = float64(sh.Bytes()) / float64(alloc)
				row.CompressBps = float64(logical) / ctime
				if dtime > 0 {
					row.DecompressBps = float64(logical) / dtime
				} else {
					row.Notes = "every record stored raw: reads decompress nothing"
				}
				row.Fractions["stored_raw"] = float64(raw) / float64(len(recs))
				row.Fractions["early_abort"] = float64(aborted) / float64(len(recs))
				r.Rows = append(r.Rows, row)
			}
		}
		_ = os.Remove(concat)
	}
	return nil
}

// ---- zram (real process memory, codec measured, allocation modelled)

// holdMemory starts a child that fills its memory with analytics state and waits for
// its stdin to close; it returns the child's pid and a function that ends it.
func holdMemory(name string, cmd *exec.Cmd, input, ready string) (int, func(), error) {
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return 0, nil, err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return 0, nil, err
	}
	// The input goes in without closing stdin: the child holds its memory until stop().
	go func() { _, _ = io.WriteString(stdin, input) }()
	stop := func() {
		stdin.Close()
		done := make(chan struct{})
		go func() { _ = cmd.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(30 * time.Second):
			_ = cmd.Process.Kill()
			<-done
		}
	}
	deadline := time.Now().Add(20 * time.Minute)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(ready); err == nil {
			time.Sleep(500 * time.Millisecond) // let the allocator settle
			return cmd.Process.Pid, stop, nil
		}
		if exited(cmd.Process.Pid) {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	stop()
	return 0, nil, fmt.Errorf("%s: never became ready: %s", name, strings.TrimSpace(stderr.String()))
}

// exited reports whether pid is gone or a zombie (it died before becoming ready).
func exited(pid int) bool {
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return true
	}
	i := bytes.LastIndexByte(b, ')')
	return i < 0 || i+2 >= len(b) || b[i+2] == 'Z' || b[i+2] == 'X'
}

// MemorySources snapshots the memory of analytics processes: DuckDB holding lineitem and
// its sorted and aggregated copies, and a Go process holding parsed CSV rows.
func MemorySources(o Options, r *Report, csvPath string) []Shape {
	var out []Shape
	snap := func(what, name, desc string, cmd *exec.Cmd, input, ready string) {
		_ = os.Remove(ready)
		pid, stop, err := holdMemory(what, cmd, input, ready)
		if err != nil {
			r.Skip("zram memory: "+what, err.Error())
			return
		}
		pages, err := Snapshot(pid, o.ZramPages)
		stop()
		if err != nil {
			r.Skip("zram memory: "+what, err.Error())
			return
		}
		out = append(out, Shape{Name: name, Desc: desc, Files: []File{{Name: name + ".pages", Data: joinPages(pages)}}})
	}
	if o.Duck.Bin != "" {
		ready := filepath.Join(o.Work, "duck.ready")
		sql := fmt.Sprintf(`SET extension_directory=%s;
SET preserve_insertion_order=false;
ATTACH %s AS t (READ_ONLY);
CREATE TABLE m AS SELECT * FROM t.lineitem;
CREATE TABLE s AS SELECT * FROM m ORDER BY l_shipdate, l_comment;
CREATE TABLE g AS SELECT l_suppkey, l_partkey, count(*) AS n, sum(l_extendedprice) AS rev, list(l_comment) AS comments FROM m GROUP BY ALL;
COPY (SELECT 1) TO %s (FORMAT csv);
`, sqlString(o.Duck.ExtensionDir), sqlString(o.Duck.DB), sqlString(ready))
		snap("duckdb", "duckdb-process", "resident anonymous pages of DuckDB holding TPC-H lineitem, a sorted copy and a group-by",
			exec.Command(o.Duck.Bin, "-batch", "-bail", ":memory:"), sql, ready)
	}
	if o.Self != "" && csvPath != "" {
		ready := filepath.Join(o.Work, "go.ready")
		snap("go", "go-process", "resident anonymous pages of a Go process holding parsed lineitem rows, typed columns and a hash index",
			exec.Command(o.Self, "memhold", "-csv", csvPath, "-ready", ready), "", ready)
	}
	return out
}

// RunZram measures zram codecs on page samples.
func RunZram(o Options, mem []Shape, r *Report) error {
	z := DefaultZram
	for _, sh := range mem {
		pages := Split(sh.Files[0].Data, z.PageSize)
		same := PageStats(pages)
		var nonSame [][]byte
		for _, p := range pages {
			if !SameFilled(p) {
				nonSame = append(nonSame, p)
			}
		}
		concat := filepath.Join(o.Work, "pages-"+sh.Name)
		if err := os.WriteFile(concat, joinPages(nonSame), 0o600); err != nil {
			return err
		}
		for _, st := range o.ZramSettings {
			o.progress("zram %s %s (%d pages, %d same-filled)", sh.Name, st.Name(), len(pages), same)
			cs, err := BlockSizes(o.Tools, st, nonSame, o.Work)
			if err != nil {
				r.Skip("zram "+sh.Name+" "+st.Name(), err.Error())
				continue
			}
			sp, err := BenchSpeed(o.Tools, st, concat, z.PageSize, o.BenchSeconds)
			if err != nil {
				r.Skip("zram speed "+sh.Name+" "+st.Name(), err.Error())
				continue
			}
			var comp, stored int64
			huge := 0
			for i := range cs {
				c := cs[i] - FrameOverhead("zram", st, z.PageSize)
				comp += int64(c)
				s := z.Stored(c)
				if s == z.PageSize {
					huge++
				}
				stored += int64(s)
			}
			logical := int64(len(pages) * z.PageSize)
			r.Rows = append(r.Rows, Row{Layer: "zram", Shape: sh.Name, Setting: st.Name(), BlockSize: z.PageSize, Method: "model",
				OrigBytes: logical, CompBytes: comp, AllocBytes: stored,
				Ratio: float64(len(nonSame)*z.PageSize) / float64(comp), AllocRatio: float64(logical) / float64(max(stored, 1)),
				CompressBps: sp.CompressBps, DecompressBps: sp.DecompressBps,
				Fractions: map[string]float64{"same_filled": float64(same) / float64(len(pages)), "huge": float64(huge) / float64(len(pages))}})
		}
		_ = os.Remove(concat)
	}
	r.Skip("zram lzo-rle (user space)", "no command-line LZO-RLE tool; the on-box pressure run (zram-pressure, root) measures it in the kernel")
	return nil
}

// ZramLive reads the host's active zram devices: the in-kernel ratio on whatever the
// machine has swapped so far. Rootless (the counters are world-readable).
func ZramLive(r *Report) {
	devs, _ := filepath.Glob("/sys/block/zram*")
	for _, d := range devs {
		p := readMMStat(filepath.Base(d))
		if p.orig == 0 || p.compr == 0 {
			continue
		}
		algo := "unknown"
		if b, err := os.ReadFile(filepath.Join(d, "comp_algorithm")); err == nil {
			for _, w := range strings.Fields(string(b)) {
				if strings.HasPrefix(w, "[") {
					algo = strings.Trim(w, "[]")
				}
			}
		}
		setting := algo
		if algo == "zstd" {
			setting = "zstd-3" // zram's zstd backend default level
		}
		r.Rows = append(r.Rows, Row{Layer: "zram-live", Shape: "this-host-swap", Setting: setting, BlockSize: 4096, Method: "in-kernel",
			OrigBytes: int64(p.orig), CompBytes: int64(p.compr), AllocBytes: int64(p.used), Ratio: p.orig / p.compr, AllocRatio: p.orig / p.used,
			Notes: filepath.Base(d) + ": whatever this machine happened to swap; not a controlled workload"})
	}
}
