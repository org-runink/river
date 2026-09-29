package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	osexec "os/exec"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	"github.com/org-runink/river/bench/analytics/internal/compress"
	"github.com/org-runink/river/bench/analytics/internal/fio"
	"github.com/org-runink/river/bench/analytics/internal/otel"
	"github.com/org-runink/river/bench/analytics/internal/report"
	"github.com/org-runink/river/bench/analytics/internal/sysinfo"
	"github.com/org-runink/river/bench/analytics/internal/tpch"
)

var compressSmoke = map[string]string{
	"sample":        strconv.Itoa(4 << 20),
	"bench-seconds": "0",
	"reps":          "1",
	"tpch-sf":       "0.1",
	"io-bytes":      strconv.Itoa(128 << 20),
	"scan-rows":     strconv.Itoa(4 << 20),
	"zram-pages":    "1024",
	"delta-commits": "200",
	"modules":       "40",
	"stream-cap":    strconv.Itoa(16 << 20),
}

// telemetryFlags are the export options shared by compress and export. Every one is off
// unless the operator passes it.
type telemetryFlags struct {
	endpoint, format, headers, promFile string
}

func (t *telemetryFlags) register(fs *flag.FlagSet) {
	fs.StringVar(&t.endpoint, "otlp-endpoint", "", "OTLP/HTTP metrics URL to push to (default: none; nothing is sent unless this is set)")
	fs.StringVar(&t.format, "otlp-format", "protobuf", "OTLP encoding: protobuf or json")
	fs.StringVar(&t.headers, "otlp-header", "", "extra request headers, k=v[,k=v] (for an authenticating collector)")
	fs.StringVar(&t.promFile, "prom-file", "", "also write the metrics in Prometheus text format to this file")
}

func (t *telemetryFlags) emit(b *otel.Batch) error {
	if t.promFile != "" {
		f, err := os.Create(t.promFile)
		if err != nil {
			return err
		}
		if err := otel.EncodePrometheus(f, b); err != nil {
			f.Close()
			return err
		}
		if err := f.Close(); err != nil {
			return err
		}
		progress("wrote %s", t.promFile)
	}
	if t.endpoint == "" {
		return nil
	}
	hdr := map[string]string{}
	for _, kv := range strings.Split(t.headers, ",") {
		if k, v, ok := strings.Cut(kv, "="); ok {
			hdr[strings.TrimSpace(k)] = strings.TrimSpace(v)
		}
	}
	if err := otel.Push(context.Background(), t.endpoint, t.format, hdr, b); err != nil {
		return err
	}
	progress("pushed %d metrics to %s (%s)", len(b.Metrics), t.endpoint, t.format)
	return nil
}

func benchCommit() string {
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return "unknown"
	}
	rev, dirty := "", false
	for _, s := range bi.Settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.modified":
			dirty = s.Value == "true"
		}
	}
	if rev == "" {
		return "unknown"
	}
	if dirty {
		rev += "-dirty"
	}
	return rev
}

func osName() string {
	b, err := os.ReadFile("/etc/os-release")
	if err != nil {
		return "unknown"
	}
	for _, l := range strings.Split(string(b), "\n") {
		if v, ok := strings.CutPrefix(l, "PRETTY_NAME="); ok {
			return strings.Trim(v, `"`)
		}
	}
	return "unknown"
}

func loadAvg() string {
	b, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return ""
	}
	f := strings.Fields(string(b))
	if len(f) < 3 {
		return ""
	}
	return strings.Join(f[:3], " ")
}

func provenance(work, hostKind string) compress.Provenance {
	id := make([]byte, 8)
	_, _ = rand.Read(id)
	p := compress.Provenance{RunID: hex.EncodeToString(id), OSName: osName(), BenchCommit: benchCommit(),
		RootFS: fio.FSType("/"), WorkFS: fio.FSType(work), LoadStart: loadAvg()}
	if _, err := os.Stat("/etc/runink-os-version"); err == nil {
		p.IsRunink = true
	}
	if b, err := os.ReadFile("/sys/module/zfs/version"); err == nil {
		p.ZFSVersion = strings.TrimSpace(string(b))
	}
	switch {
	case hostKind != "auto":
		p.HostKind = hostKind
	case !p.IsRunink:
		p.HostKind = "other"
	default:
		if _, err := os.Stat("/var/lib/k0s"); err == nil {
			p.HostKind = "server"
		} else {
			p.HostKind = "workstation"
		}
	}
	return p
}

func cmdCompress(args []string) error {
	cache := defaultCache()
	fs := flag.NewFlagSet("compress", flag.ExitOnError)
	out := fs.String("out", "", "output directory for compress.json and compress.md (required)")
	label := fs.String("label", "", "run label (default: kernel release)")
	smoke := fs.Bool("smoke", false, "small samples to validate the harness (not a result)")
	layers := fs.String("layers", "zfs,zram,kernel,parquet,io,scan", "layers to run; add zfs-pool and zram-pressure on a Runink River machine (root)")
	shapeList := fs.String("shapes", "", "comma-separated data shapes to keep (default: all)")
	sample := fs.Int("sample", 64<<20, "bytes per data shape")
	benchSec := fs.Int("bench-seconds", 1, "minimum seconds per codec benchmark direction (zstd/lz4 -i)")
	reps := fs.Int("reps", 3, "repetitions of whole-stream and file-access timings (median kept)")
	duckdb := fs.String("duckdb", "", "pinned duckdb CLI (run.sh fetches it)")
	sf := fs.Float64("tpch-sf", 1, "TPC-H scale factor for the table shapes")
	tpchDir := fs.String("tpch-dir", filepath.Join(cache, "tpch"), "directory for the generated TPC-H database")
	duckExt := fs.String("duckdb-ext", filepath.Join(cache, "duckdb-extensions"), "DuckDB extension directory")
	work := fs.String("work", filepath.Join(cache, "compress-work"), "scratch directory (needs a few GiB; keep it off tmpfs)")
	kpkg := fs.String("kernel-pkg", "", "linux-runink package (.pkg.tar.zst) for the kernel layer")
	zpkg := fs.String("zfs-pkg", "", "runink-zfs package (adds zfs.ko/spl.ko to the initramfs)")
	zupkg := fs.String("zfs-utils-pkg", "", "runink-zfs-utils package (adds zpool/zfs and libraries to the initramfs)")
	modCount := fs.Int("modules", 400, "kernel: modules sampled for the module-compression comparison")
	streamCap := fs.Int("stream-cap", 0, "kernel: keep only this many bytes of the kernel image and initramfs (0: all)")
	zparent := fs.String("zfs-parent", "", "zfs-pool: existing dataset to create the test datasets under (root)")
	zcodecs := fs.String("zram-codecs", "lz4,lzo-rle,zstd", "zram-pressure: codecs (root)")
	frac := fs.Float64("pressure-frac", 0.5, "zram-pressure: memory limit as a fraction of the workload's unconstrained peak")
	ioBytes := fs.Int64("io-bytes", 2<<30, "io: size of the Parquet file read by the file-access tests")
	scanRows := fs.Int("scan-rows", 64<<20, "scan: rows in the THP columnar scan")
	zramPages := fs.Int("zram-pages", 16384, "zram: 4 KiB pages sampled per process")
	deltaCommits := fs.Int("delta-commits", 2000, "delta-log: commit files generated")
	hostKind := fs.String("host-kind", "auto", "workstation, server or other (auto: detected)")
	var tel telemetryFlags
	tel.register(fs)
	_ = fs.Parse(args)
	if *out == "" {
		return fmt.Errorf("compress: -out is required")
	}
	if *smoke {
		set := map[string]bool{}
		fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
		for k, v := range compressSmoke {
			if !set[k] {
				_ = fs.Set(k, v)
			}
		}
	}
	on := map[string]bool{}
	for _, l := range strings.Split(*layers, ",") {
		on[strings.TrimSpace(l)] = true
	}
	if err := os.MkdirAll(*work, 0o755); err != nil {
		return err
	}
	self, _ := os.Executable()
	rep := &compress.Report{Started: time.Now().UTC(), Smoke: *smoke, Host: sysinfo.Collect(), Params: map[string]string{},
		Provenance: provenance(*work, *hostKind)}
	rep.Label = *label
	if rep.Label == "" {
		rep.Label = rep.Host.KernelRelease
	}
	fs.VisitAll(func(f *flag.Flag) {
		if f.Name != "out" && f.Name != "label" && !strings.HasPrefix(f.Name, "otlp") && f.Name != "prom-file" {
			rep.Params[f.Name] = f.Value.String()
		}
	})
	o := compress.Options{Work: *work, SampleBytes: *sample, BenchSeconds: *benchSec, Reps: *reps, Tools: compress.FindTools(),
		Self: self, ZFSSettings: compress.DefaultZFSSettings, RecordSizes: []int{128 << 10, 1 << 20},
		ZramSettings: compress.DefaultZramSettings, ZramPages: *zramPages, KernelPkg: *kpkg, ZFSPkg: *zpkg,
		ZFSUtilsPkg: *zupkg, ModuleCount: *modCount, StreamCap: *streamCap, DeltaCommits: *deltaCommits, Progress: progress}
	if *shapeList != "" {
		o.Shapes = map[string]bool{}
		for _, s := range strings.Split(*shapeList, ",") {
			o.Shapes[strings.TrimSpace(s)] = true
		}
	}
	rep.Tools = o.Tools.Versions
	if o.Tools.Zstd == "" || o.Tools.LZ4 == "" {
		return fmt.Errorf("compress: zstd and lz4 are required (pacman -S zstd lz4)")
	}
	if *duckdb != "" {
		topt := tpch.Options{DuckDB: *duckdb, DataDir: *tpchDir, ExtensionDir: *duckExt, ScaleFactor: *sf}
		progress("tpch: preparing sf=%v in %s", *sf, topt.DBPath())
		if _, err := tpch.Generate(topt); err != nil {
			return err
		}
		o.Duck = compress.Duck{Bin: *duckdb, ExtensionDir: *duckExt, DB: topt.DBPath()}
		if b, err := osexec.Command(*duckdb, "-version").Output(); err == nil {
			rep.Tools["duckdb"] = strings.TrimSpace(string(b))
		}
	}

	var shapes []compress.Shape
	if on["zfs"] || on["zfs-pool"] || on["zram"] {
		var err error
		if shapes, err = compress.Corpus(o, rep); err != nil {
			return err
		}
	}
	if on["zram"] {
		csv := ""
		if o.Duck.Bin != "" {
			csv = filepath.Join(*work, "lineitem.csv")
		}
		if err := compress.RunZram(o, compress.MemorySources(o, rep, csv), rep); err != nil {
			return err
		}
		compress.ZramLive(rep)
	}
	if on["zfs"] {
		if err := compress.RunZFS(o, shapes, rep); err != nil {
			return err
		}
	}
	if on["zfs-pool"] {
		if *zparent == "" {
			rep.Skip("zfs-pool", "no -zfs-parent dataset")
		} else if err := compress.RunZFSPool(o, *zparent, shapes, rep); err != nil {
			return err
		}
	}
	shapes = nil
	runtime.GC()
	if on["kernel"] {
		in, err := compress.PrepareKernel(o)
		if err != nil {
			rep.Skip("kernel", err.Error())
		} else if err := compress.RunKernel(o, in, rep); err != nil {
			return err
		}
		_ = os.RemoveAll(filepath.Join(*work, "kernel"))
		_ = os.RemoveAll(filepath.Join(*work, "zfs-utils"))
	}
	if on["parquet"] || on["io"] || on["scan"] {
		if o.Duck.Bin == "" {
			rep.Skip("parquet, io, duckdb THP", "no DuckDB binary")
		} else if err := compress.RunParquet(o, rep); err != nil {
			return err
		}
	}
	pqFile := filepath.Join(*work, "pq-zstd-3.parquet")
	if on["io"] && o.Duck.Bin != "" {
		big := filepath.Join(*work, "io-test.parquet")
		if err := growFile(pqFile, big, *ioBytes); err != nil {
			rep.Skip("io", err.Error())
		} else {
			if err := compress.RunIO(o, big, fio.FSType(*work), rep); err != nil {
				return err
			}
			_ = os.Remove(big)
		}
	}
	if on["scan"] {
		if err := compress.RunScan(o, *scanRows, rep); err != nil {
			return err
		}
		if o.Duck.Bin != "" {
			compress.RunDuckTHP(o, pqFile, int64(*sf*6_000_000), rep)
		}
	}
	if on["zram-pressure"] {
		if err := compress.RunZramPressure(o, strings.Split(*zcodecs, ","), *frac, rep); err != nil {
			return err
		}
	}
	compress.Recommend(rep, compress.DefaultRules)
	rep.Finished = time.Now().UTC()
	rep.Provenance.LoadEnd = loadAvg()
	if err := writeCompress(rep, *out); err != nil {
		return err
	}
	return tel.emit(rep.Batch())
}

func writeCompress(rep *compress.Report, out string) error {
	if err := os.MkdirAll(out, 0o755); err != nil {
		return err
	}
	jf, err := os.Create(filepath.Join(out, "compress.json"))
	if err != nil {
		return err
	}
	if err := rep.WriteJSON(jf); err != nil {
		jf.Close()
		return err
	}
	if err := jf.Close(); err != nil {
		return err
	}
	mf, err := os.Create(filepath.Join(out, "compress.md"))
	if err != nil {
		return err
	}
	rep.WriteMarkdown(mf)
	if err := mf.Close(); err != nil {
		return err
	}
	rep.WriteMarkdown(os.Stdout)
	return nil
}

// growFile writes dst as src repeated up to n bytes (the file-access tests want a file
// larger than the drive's cache, and the bytes only need to be Parquet-shaped).
func growFile(src, dst string, n int64) error {
	b, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	f, err := os.Create(dst)
	if err != nil {
		return err
	}
	w := bufio.NewWriterSize(f, 4<<20)
	for written := int64(0); written < n; written += int64(len(b)) {
		if _, err := w.Write(b); err != nil {
			f.Close()
			return err
		}
	}
	if err := w.Flush(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// cmdMemhold is the memory-snapshot target for the zram corpus: a Go process holding
// parsed lineitem rows, typed columns and a hash index until its stdin closes.
func cmdMemhold(args []string) error {
	fs := flag.NewFlagSet("memhold", flag.ExitOnError)
	csv := fs.String("csv", "", "lineitem CSV")
	ready := fs.String("ready", "", "file to create once the data is in memory")
	_ = fs.Parse(args)
	data, err := os.ReadFile(*csv)
	if err != nil {
		return err
	}
	_, rows, err := compress.ReadCSV(data)
	if err != nil {
		return err
	}
	data = nil
	type col struct {
		keys   []int64
		prices []float64
		flags  []string
	}
	var c col
	index := make(map[int64][]int, len(rows))
	for i, r := range rows {
		k, _ := strconv.ParseInt(r[0], 10, 64)
		p, _ := strconv.ParseFloat(r[5], 64)
		c.keys, c.prices, c.flags = append(c.keys, k), append(c.prices, p), append(c.flags, r[8]+r[9])
		index[k] = append(index[k], i)
	}
	runtime.GC()
	if err := os.WriteFile(*ready, []byte("1"), 0o644); err != nil {
		return err
	}
	_, _ = io.Copy(io.Discard, os.Stdin)
	runtime.KeepAlive(data)
	runtime.KeepAlive(rows)
	runtime.KeepAlive(c)
	runtime.KeepAlive(index)
	return nil
}

// cmdExport re-emits a finished run (compress.json or a kernel results.json) as
// OpenTelemetry metrics, and can re-render a compress report with the current rules.
func cmdExport(args []string) error {
	fs := flag.NewFlagSet("export", flag.ExitOnError)
	in := fs.String("in", "", "compress.json or results.json")
	jsonOut := fs.Bool("otlp-json-stdout", false, "print the OTLP JSON request to stdout")
	rerank := fs.String("rerank", "", "compress.json only: re-apply the current recommendation rules and write the report to this directory")
	merge := fs.String("merge", "", "compress.json only: comma-separated reports whose rows are added (a re-run of some shapes) before -rerank")
	var tel telemetryFlags
	tel.register(fs)
	_ = fs.Parse(args)
	b, err := os.ReadFile(*in)
	if err != nil {
		return err
	}
	var head struct {
		Schema string `json:"schema"`
	}
	if err := json.Unmarshal(b, &head); err != nil {
		return err
	}
	var batch *otel.Batch
	switch head.Schema {
	case compress.Schema:
		r, err := compress.Load(*in)
		if err != nil {
			return err
		}
		for _, p := range strings.Split(*merge, ",") {
			if p == "" {
				continue
			}
			m, err := compress.Load(p)
			if err != nil {
				return err
			}
			r.Rows = append(r.Rows, m.Rows...)
			r.Skipped = append(r.Skipped, compress.Skipped{What: "merged", Reason: "rows from run " + m.Provenance.RunID})
		}
		if *rerank != "" {
			for i, row := range r.Rows { // reports before the file-bytes basis
				if row.Layer == "zfs" && row.Method == "model" && row.AllocBytes > 0 {
					r.Rows[i].AllocRatio = float64(row.OrigBytes) / float64(row.AllocBytes)
				}
			}
			r.Recommendations = nil
			kept := r.Skipped[:0]
			for _, s := range r.Skipped {
				if !strings.HasPrefix(s.What, "recommendation ") {
					kept = append(kept, s)
				}
			}
			r.Skipped = kept
			compress.Recommend(r, compress.DefaultRules)
			if err := writeCompress(r, *rerank); err != nil {
				return err
			}
		}
		batch = r.Batch()
	case report.Schema:
		r, err := report.Load(*in)
		if err != nil {
			return err
		}
		batch = compress.KernelBatch(r)
	default:
		return fmt.Errorf("export: %s: unknown schema %q", *in, head.Schema)
	}
	if *jsonOut {
		if err := otel.EncodeJSON(os.Stdout, batch); err != nil {
			return err
		}
	}
	return tel.emit(batch)
}

// cmdCatalogue prints the metric catalogue as the README's markdown table.
func cmdCatalogue() error {
	_, err := io.WriteString(os.Stdout, compress.CatalogueMarkdown())
	return err
}
