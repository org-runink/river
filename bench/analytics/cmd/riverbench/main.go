// Command riverbench is the Runink River analytics benchmark harness. It measures one
// kernel per run, on the running system, and writes results.json plus results.md; the
// compare subcommand puts several runs (one per kernel, same machine) side by side.
//
//	riverbench run -out DIR [-smoke] [-reps 5] [-suites stream,sortjoin,fio,tpch] ...
//	riverbench compare runink/results.json zen/results.json lts/results.json
//	riverbench compress -out DIR [-smoke] [-layers zfs,zram,kernel,parquet,io,scan] ...
//	riverbench compare [-threshold 5] base/compress.json new/compress.json   (JSON for agents)
//	riverbench export -in compress.json -prom-file m.prom [-otlp-endpoint URL]
//	riverbench catalogue
//	riverbench sysinfo
//
// run.sh in this directory is the usual entry point: it fetches the pinned DuckDB CLI and
// builds this command first. See docs/KERNEL.md ("Benchmarking") for the procedure.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/org-runink/river/bench/analytics/internal/compress"
	"github.com/org-runink/river/bench/analytics/internal/fio"
	"github.com/org-runink/river/bench/analytics/internal/membw"
	"github.com/org-runink/river/bench/analytics/internal/report"
	"github.com/org-runink/river/bench/analytics/internal/sortjoin"
	"github.com/org-runink/river/bench/analytics/internal/sysinfo"
	"github.com/org-runink/river/bench/analytics/internal/tpch"
)

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	var err error
	switch os.Args[1] {
	case "run":
		err = cmdRun(os.Args[2:])
	case "compare":
		err = cmdCompare(os.Args[2:])
	case "compress":
		err = cmdCompress(os.Args[2:])
	case "memhold":
		err = cmdMemhold(os.Args[2:])
	case "export":
		err = cmdExport(os.Args[2:])
	case "catalogue":
		err = cmdCatalogue()
	case "sysinfo":
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		err = enc.Encode(sysinfo.Collect())
	default:
		usage()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "riverbench:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: riverbench run -out DIR [flags] | compress -out DIR [flags] | compare [-threshold PCT] A.json B.json [...] | export -in FILE [flags] | catalogue | sysinfo")
	os.Exit(2)
}

type runFlags struct {
	out, label, suites               string
	reps, warmup                     int
	smoke                            bool
	streamElems                      int
	sortRows, buildRows, probeRows   int
	fioBin, fioDirs, fioSize, fioEng string
	fioRuntime                       int
	fioDirect                        bool
	duckdb, tpchDir, duckExt         string
	tpchSF                           float64
	tpchThreads                      int
}

// Full-size defaults (the real comparison) and smoke-size overrides (harness validation).
var smokeDefaults = map[string]string{
	"stream-elements": strconv.Itoa(1 << 22),
	"sort-rows":       "2000000",
	"build-rows":      "1000000",
	"probe-rows":      "4000000",
	"fio-size":        "256M",
	"fio-runtime":     "5",
	"tpch-sf":         "0.1",
}

func cmdRun(args []string) error {
	var f runFlags
	cache := defaultCache()
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	fs.StringVar(&f.out, "out", "", "output directory for results.json and results.md (required)")
	fs.StringVar(&f.label, "label", "", "run label (default: kernel release)")
	fs.StringVar(&f.suites, "suites", "stream,sortjoin,fio,tpch", "comma-separated suites")
	fs.IntVar(&f.reps, "reps", 5, "measured repetitions per suite")
	fs.IntVar(&f.warmup, "warmup", 1, "unmeasured warm-up repetitions per suite")
	fs.BoolVar(&f.smoke, "smoke", false, "small sizes for validating the harness (results are not a comparison)")
	fs.IntVar(&f.streamElems, "stream-elements", 1<<26, "STREAM-like: float64 elements per array (3 arrays)")
	fs.IntVar(&f.sortRows, "sort-rows", 100_000_000, "sort: uint64 keys")
	fs.IntVar(&f.buildRows, "build-rows", 20_000_000, "hash join: build rows")
	fs.IntVar(&f.probeRows, "probe-rows", 100_000_000, "hash join: probe rows")
	fs.StringVar(&f.fioBin, "fio", "fio", "fio binary")
	fs.StringVar(&f.fioDirs, "fio-dirs", "", "label=dir[,label=dir] targets, e.g. zfs=/tank/bench,ext4=/mnt/bench")
	fs.StringVar(&f.fioSize, "fio-size", "8G", "fio file size per job (make it larger than RAM for buffered runs)")
	fs.IntVar(&f.fioRuntime, "fio-runtime", 30, "fio seconds per job")
	fs.BoolVar(&f.fioDirect, "fio-direct", true, "fio O_DIRECT")
	fs.StringVar(&f.fioEng, "fio-engine", "io_uring", "fio ioengine")
	fs.StringVar(&f.duckdb, "duckdb", "", "pinned duckdb CLI (run.sh fetches it)")
	fs.Float64Var(&f.tpchSF, "tpch-sf", 10, "TPC-H scale factor")
	fs.StringVar(&f.tpchDir, "tpch-dir", filepath.Join(cache, "tpch"), "directory for the generated TPC-H database")
	fs.StringVar(&f.duckExt, "duckdb-ext", filepath.Join(cache, "duckdb-extensions"), "DuckDB extension directory")
	fs.IntVar(&f.tpchThreads, "tpch-threads", 0, "DuckDB threads (0 = all)")
	_ = fs.Parse(args)
	if f.out == "" {
		return fmt.Errorf("run: -out is required")
	}
	if f.reps < 1 {
		return fmt.Errorf("run: -reps must be >= 1")
	}
	if f.smoke {
		set := map[string]bool{}
		fs.Visit(func(fl *flag.Flag) { set[fl.Name] = true })
		for k, v := range smokeDefaults {
			if !set[k] {
				_ = fs.Set(k, v)
			}
		}
	}

	res := &report.Results{
		Schema:  report.Schema,
		Started: time.Now().UTC(),
		Smoke:   f.smoke,
		Reps:    f.reps,
		Host:    sysinfo.Collect(),
		Params:  map[string]string{},
	}
	res.Label = f.label
	if res.Label == "" {
		res.Label = res.Host.KernelRelease
	}
	fs.VisitAll(func(fl *flag.Flag) {
		if fl.Name != "out" && fl.Name != "label" {
			res.Params[fl.Name] = fl.Value.String()
		}
	})

	for _, s := range strings.Split(f.suites, ",") {
		s = strings.TrimSpace(s)
		var err error
		switch s {
		case "":
			continue
		case "stream":
			err = suiteStream(res, f)
		case "sortjoin":
			err = suiteSortJoin(res, f)
		case "fio":
			err = suiteFio(res, f)
		case "tpch":
			err = suiteTPCH(res, f)
		default:
			return fmt.Errorf("run: unknown suite %q", s)
		}
		if err != nil {
			return fmt.Errorf("suite %s: %w", s, err)
		}
	}
	res.Finished = time.Now().UTC()
	return writeResults(res, f.out)
}

func writeResults(res *report.Results, out string) error {
	if err := os.MkdirAll(out, 0o755); err != nil {
		return err
	}
	jf, err := os.Create(filepath.Join(out, "results.json"))
	if err != nil {
		return err
	}
	if err := res.WriteJSON(jf); err != nil {
		jf.Close()
		return err
	}
	if err := jf.Close(); err != nil {
		return err
	}
	mf, err := os.Create(filepath.Join(out, "results.md"))
	if err != nil {
		return err
	}
	res.WriteMarkdown(mf)
	if err := mf.Close(); err != nil {
		return err
	}
	res.WriteMarkdown(os.Stdout)
	return nil
}

func progress(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "riverbench: "+format+"\n", a...)
}

func suiteStream(res *report.Results, f runFlags) error {
	for _, madv := range []bool{false, true} {
		variant := "heap"
		if madv {
			variant = "madv-hugepage"
		}
		o := membw.Options{Elements: f.streamElems, Madvise: madv}
		ar, err := membw.Alloc(o)
		if err != nil {
			return err
		}
		var cp, sc, ad, tr []float64
		for i := 0; i < f.warmup+f.reps; i++ {
			r := membw.Run(o, ar)
			if i < f.warmup {
				continue
			}
			progress("stream %s rep %d: triad %.2f GB/s", variant, i-f.warmup+1, r.Triad/1e9)
			cp, sc = append(cp, r.Copy/1e9), append(sc, r.Scale/1e9)
			ad, tr = append(ad, r.Add/1e9), append(tr, r.Triad/1e9)
		}
		ar.Free()
		note := fmt.Sprintf("%d float64 x 3 arrays = %.2f GiB", f.streamElems, float64(3*8*f.streamElems)/(1<<30))
		for _, m := range []struct {
			n string
			v []float64
		}{{"copy", cp}, {"scale", sc}, {"add", ad}, {"triad", tr}} {
			res.Add(report.Metric{Suite: "stream", Name: m.n + " (" + variant + ")", Unit: "GB/s",
				HigherIsBetter: true, Samples: m.v, Notes: note})
		}
	}
	return nil
}

func suiteSortJoin(res *report.Results, f runFlags) error {
	o := sortjoin.Options{SortRows: f.sortRows, BuildRows: f.buildRows, ProbeRows: f.probeRows}
	var sortS, joinS []float64
	var first *sortjoin.Result
	for i := 0; i < f.warmup+f.reps; i++ {
		r, err := sortjoin.Run(o)
		if err != nil {
			return err
		}
		if first == nil {
			first = &r
		} else if r.SortChecksum != first.SortChecksum || r.JoinMatches != first.JoinMatches ||
			r.JoinChecksum != first.JoinChecksum {
			return fmt.Errorf("checksums differ between reps: the benchmark is not deterministic")
		}
		runtime.GC()
		if i < f.warmup {
			continue
		}
		progress("sortjoin rep %d: sort %.1f Mrows/s, join %.1f Mrows/s", i-f.warmup+1, r.SortRowsPerSec/1e6, r.JoinRowsPerSec/1e6)
		sortS = append(sortS, r.SortRowsPerSec/1e6)
		joinS = append(joinS, r.JoinRowsPerSec/1e6)
	}
	res.Add(report.Metric{Suite: "sortjoin", Name: "parallel sort", Unit: "Mrows/s", HigherIsBetter: true,
		Samples: sortS, Notes: fmt.Sprintf("%d uint64 keys", f.sortRows)})
	res.Add(report.Metric{Suite: "sortjoin", Name: "hash join", Unit: "Mrows/s", HigherIsBetter: true,
		Samples: joinS, Notes: fmt.Sprintf("build %d, probe %d, %d matches", f.buildRows, f.probeRows, first.JoinMatches)})
	return nil
}

func suiteFio(res *report.Results, f runFlags) error {
	bin, err := exec.LookPath(f.fioBin)
	if err != nil {
		res.Skip("fio", "fio not found ("+f.fioBin+"); install it (pacman -S fio) or pass -fio")
		return nil
	}
	if f.fioDirs == "" {
		res.Skip("fio", "no -fio-dirs given (label=dir on the filesystem under test)")
		return nil
	}
	for _, t := range strings.Split(f.fioDirs, ",") {
		label, dir, ok := strings.Cut(t, "=")
		if !ok {
			return fmt.Errorf("-fio-dirs entry %q is not label=dir", t)
		}
		if st, err := os.Stat(dir); err != nil || !st.IsDir() {
			res.Skip("fio "+label, dir+" is not a directory")
			continue
		}
		fsType := fio.FSType(dir)
		o := fio.Options{Binary: bin, Dir: dir, Size: f.fioSize, Runtime: f.fioRuntime,
			Direct: f.fioDirect, IOEngine: f.fioEng}
		for _, j := range fio.Jobs {
			var bw, iops, p99 []float64
			for i := 0; i < f.warmup+f.reps; i++ {
				r, err := fio.Run(o, j)
				if err != nil {
					fio.Cleanup(dir)
					return err
				}
				if i < f.warmup {
					continue
				}
				progress("fio %s %s rep %d: %.0f MiB/s, %.0f IOPS", label, j.Name, i-f.warmup+1, r.BandwidthBytes/(1<<20), r.IOPS)
				bw, iops, p99 = append(bw, r.BandwidthBytes/(1<<20)), append(iops, r.IOPS), append(p99, r.P99LatUsec)
			}
			name := fmt.Sprintf("%s %s [%s]", label, j.Name, fsType)
			note := fmt.Sprintf("bs=%s iodepth=%d numjobs=%d engine=%s direct=%v size=%s", j.BS, j.IODepth, j.NumJobs, f.fioEng, f.fioDirect, f.fioSize)
			if j.RW == "read" {
				res.Add(report.Metric{Suite: "fio", Name: name, Unit: "MiB/s", HigherIsBetter: true, Samples: bw, Notes: note})
			} else {
				res.Add(report.Metric{Suite: "fio", Name: name, Unit: "IOPS", HigherIsBetter: true, Samples: iops, Notes: note})
				res.Add(report.Metric{Suite: "fio", Name: name + " p99", Unit: "us", HigherIsBetter: false, Samples: p99, Notes: note})
			}
		}
		fio.Cleanup(dir)
	}
	return nil
}

func suiteTPCH(res *report.Results, f runFlags) error {
	if f.duckdb == "" {
		res.Skip("tpch", "no -duckdb binary (run.sh fetches the pinned release)")
		return nil
	}
	o := tpch.Options{DuckDB: f.duckdb, DataDir: f.tpchDir, ExtensionDir: f.duckExt,
		ScaleFactor: f.tpchSF, Threads: f.tpchThreads}
	progress("tpch: preparing sf=%v in %s", f.tpchSF, o.DBPath())
	created, err := tpch.Generate(o)
	if err != nil {
		return err
	}
	if created {
		progress("tpch: generated %s", o.DBPath())
	}
	var total []float64
	perQuery := make([][]float64, tpch.Queries)
	for i := 0; i < f.warmup+f.reps; i++ {
		times, err := tpch.Run(o)
		if err != nil {
			return err
		}
		if i < f.warmup {
			continue
		}
		sum := 0.0
		for q, t := range times {
			sum += t
			perQuery[q] = append(perQuery[q], t)
		}
		progress("tpch rep %d: 22 queries in %.3f s", i-f.warmup+1, sum)
		total = append(total, sum)
	}
	fsType := fio.FSType(f.tpchDir)
	res.Add(report.Metric{Suite: "tpch", Name: fmt.Sprintf("SF%v total, 22 queries [%s]", f.tpchSF, fsType),
		Unit: "s", HigherIsBetter: false, Samples: total, Notes: "sum of per-query wall times, one DuckDB process per rep"})
	for q := range perQuery {
		res.Add(report.Metric{Suite: "tpch", Name: fmt.Sprintf("Q%02d", q+1), Unit: "s",
			HigherIsBetter: false, Samples: perQuery[q]})
	}
	return nil
}

func cmdCompare(args []string) error {
	fs := flag.NewFlagSet("compare", flag.ExitOnError)
	threshold := fs.Float64("threshold", 5, "compress reports: percent change that counts as a regression or improvement")
	_ = fs.Parse(args)
	args = fs.Args()
	if len(args) == 2 {
		if a, err := compress.Load(args[0]); err == nil {
			b, err := compress.Load(args[1])
			if err != nil {
				return err
			}
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(compress.Compare(a, b, *threshold))
		}
	}
	var runs []*report.Results
	for _, p := range args {
		r, err := report.Load(p)
		if err != nil {
			return err
		}
		runs = append(runs, r)
	}
	return report.Compare(os.Stdout, runs)
}

func defaultCache() string {
	if d := os.Getenv("RIVERBENCH_CACHE"); d != "" {
		return d
	}
	if d, err := os.UserCacheDir(); err == nil {
		return filepath.Join(d, "river-bench")
	}
	return filepath.Join(os.TempDir(), "river-bench")
}
