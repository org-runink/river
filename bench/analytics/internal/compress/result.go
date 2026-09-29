package compress

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/org-runink/river/bench/analytics/internal/otel"
	"github.com/org-runink/river/bench/analytics/internal/sysinfo"
)

// Schema identifies the compress report format.
const Schema = "riverbench-compress/v1"

// Row is one measured (or modelled) codec result.
type Row struct {
	Layer     string `json:"layer"` // zfs, zfs-codec, zram, kernel, modules, initramfs, parquet, zfs-pool, zram-pressure, zram-live
	Shape     string `json:"shape"` // data shape
	Setting   string `json:"setting"`
	BlockSize int    `json:"block_size,omitempty"` // record / page / chunk size; 0 = whole stream
	// Method says how the numbers were obtained: codec-bench (the tool's in-memory
	// benchmark), model (per-block sizes + the kernel's allocation rules), stream (whole-file
	// CLI runs), duckdb, on-pool (a real ZFS dataset), in-kernel (zram device counters).
	Method string `json:"method"`

	Files      int     `json:"files,omitempty"`
	OrigBytes  int64   `json:"orig_bytes,omitempty"`
	CompBytes  int64   `json:"comp_bytes,omitempty"`
	AllocBytes int64   `json:"alloc_bytes,omitempty"`
	Ratio      float64 `json:"ratio,omitempty"`
	AllocRatio float64 `json:"alloc_ratio,omitempty"`

	CompressBps   float64 `json:"compress_bps,omitempty"`   // one core
	DecompressBps float64 `json:"decompress_bps,omitempty"` // one core

	CompressS      float64    `json:"compress_s,omitempty"`   // wall, median
	DecompressS    float64    `json:"decompress_s,omitempty"` // wall, median
	CompressCPU    [2]float64 `json:"compress_cpu_s"`         // user, system
	DecompressCPU  [2]float64 `json:"decompress_cpu_s"`
	ColdDecompress bool       `json:"cold_decompress,omitempty"`

	Fractions map[string]float64 `json:"fractions,omitempty"` // stored_raw, early_abort, same_filled, huge
	Extra     map[string]float64 `json:"extra,omitempty"`     // layer-specific numbers
	Notes     string             `json:"notes,omitempty"`
}

// IORow is one file-access measurement.
type IORow struct {
	Test       string  `json:"test"`   // seq, rowgroup
	Method     string  `json:"method"` // pread, odirect, io_uring
	BlockSize  int     `json:"block_size"`
	QueueDepth int     `json:"queue_depth"`
	Fadvise    string  `json:"fadvise,omitempty"`
	Cache      string  `json:"cache"` // cold, warm
	Filesystem string  `json:"filesystem"`
	Bps        float64 `json:"bps"`
	Notes      string  `json:"notes,omitempty"`
}

// ScanRow is one columnar-scan measurement.
type ScanRow struct {
	Memory  string  `json:"memory"` // go-mmap, duckdb-parquet
	Phase   string  `json:"phase"`  // populate, scan, query
	THP     string  `json:"thp"`
	Threads int     `json:"threads"`
	RowsPS  float64 `json:"rows_per_s"`
	Seconds float64 `json:"seconds"`
	Notes   string  `json:"notes,omitempty"`
}

// Recommendation is the setting a declared rule picks.
type Recommendation struct {
	Layer   string `json:"layer"`
	Scope   string `json:"scope"`
	Setting string `json:"setting"`
	Rule    string `json:"rule"`
	Reason  string `json:"reason"`
}

// Skipped is a layer or case that did not run, and why.
type Skipped struct {
	What   string `json:"what"`
	Reason string `json:"reason"`
}

// Provenance is what an agent needs to know about where a run came from.
type Provenance struct {
	RunID        string            `json:"run_id"`
	HostKind     string            `json:"host_kind"` // workstation, server, other
	IsRunink     bool              `json:"is_runink_river"`
	OSName       string            `json:"os_name"`
	BenchCommit  string            `json:"bench_commit"`
	ZFSVersion   string            `json:"zfs_version,omitempty"`
	RootFS       string            `json:"root_fs"`
	WorkFS       string            `json:"work_fs"`
	DatasetProps map[string]string `json:"dataset_props,omitempty"`
	// Load average at the start and end: throughput measured on a busy machine is not a
	// result, and an agent comparing runs needs to know.
	LoadStart string `json:"load_start"`
	LoadEnd   string `json:"load_end"`
}

// Report is one `riverbench compress` run.
type Report struct {
	Schema           string            `json:"schema"`
	CatalogueVersion string            `json:"catalogue_version"`
	Label            string            `json:"label"`
	Started          time.Time         `json:"started"`
	Finished         time.Time         `json:"finished"`
	Smoke            bool              `json:"smoke"`
	Provenance       Provenance        `json:"provenance"`
	Host             sysinfo.Info      `json:"host"`
	Tools            map[string]string `json:"tools"`
	Params           map[string]string `json:"params"`
	Rows             []Row             `json:"rows"`
	IO               []IORow           `json:"io,omitempty"`
	Scan             []ScanRow         `json:"scan,omitempty"`
	Recommendations  []Recommendation  `json:"recommendations,omitempty"`
	Skipped          []Skipped         `json:"skipped,omitempty"`
	// Metrics is the same data as the OTLP export, flattened, one entry per point, sorted
	// by name and attributes: the diffable form.
	Metrics []FlatMetric `json:"metrics"`
}

// FlatMetric is one metric point in the JSON report.
type FlatMetric struct {
	Name       string            `json:"name"`
	Unit       string            `json:"unit"`
	Attributes map[string]string `json:"attributes"`
	Value      float64           `json:"value"`
}

// Skip records something that did not run.
func (r *Report) Skip(what, reason string) {
	r.Skipped = append(r.Skipped, Skipped{What: what, Reason: reason})
}

// attribute keys
const (
	aLayer = "river.bench.layer"
	aShape = "river.bench.shape"
	aSet   = "river.bench.setting"
	aBlock = "river.bench.block_size"
	aMeth  = "river.bench.method"
	aDir   = "river.bench.direction"
)

func (row Row) attrs(extra ...otel.Attr) []otel.Attr {
	a := []otel.Attr{otel.S(aLayer, row.Layer), otel.S(aShape, row.Shape), otel.S(aSet, row.Setting),
		otel.I(aBlock, int64(row.BlockSize)), otel.S(aMeth, row.Method)}
	if s, err := ParseSetting(row.Setting); err == nil {
		a = append(a, otel.S("river.bench.codec", s.Codec), otel.I("river.bench.codec.level", int64(s.Level)))
	}
	return append(a, extra...)
}

func add(b *otel.Batch, d Def, v float64, attrs ...otel.Attr) {
	if v == 0 && d.Name != MRecommend.Name {
		return // not measured
	}
	b.Add(d.Name, d.Unit, d.Description, d.Kind, v, attrs...)
}

// Resource is the OpenTelemetry resource of a report: service, host and run provenance.
func (r *Report) Resource() []otel.Attr {
	p := r.Provenance
	a := []otel.Attr{
		otel.S("service.name", "riverbench"),
		otel.S("service.version", CatalogueVersion),
		otel.S("river.bench.schema.version", Schema+"+catalogue."+CatalogueVersion),
		otel.S("river.bench.run.id", p.RunID),
		otel.S("river.bench.run.label", r.Label),
		otel.B("river.bench.smoke", r.Smoke),
		otel.S("river.bench.host.kind", p.HostKind),
		otel.B("river.bench.host.is_runink_river", p.IsRunink),
		otel.S("river.bench.fs.root", p.RootFS),
		otel.S("river.bench.fs.work", p.WorkFS),
		otel.S("vcs.ref.head.revision", p.BenchCommit),
		otel.S("os.type", "linux"),
		otel.S("os.name", p.OSName),
		otel.S("os.version", r.Host.KernelRelease),
		otel.S("host.arch", "amd64"),
		otel.S("host.cpu.model.name", r.Host.CPUModel),
		otel.I("system.memory.limit", r.Host.MemTotalKiB*1024),
	}
	if p.ZFSVersion != "" {
		a = append(a, otel.S("river.bench.zfs.version", p.ZFSVersion))
	}
	for _, k := range sortedKeys(p.DatasetProps) {
		a = append(a, otel.S("river.bench.zfs.dataset."+k, p.DatasetProps[k]))
	}
	return a
}

// Batch converts the report into OpenTelemetry metrics.
func (r *Report) Batch() *otel.Batch {
	b := &otel.Batch{Resource: r.Resource(), ScopeName: "riverbench/compress", ScopeVersion: CatalogueVersion,
		Start: r.Started, Time: r.Finished}
	for _, row := range r.Rows {
		add(b, MRatio, row.Ratio, row.attrs()...)
		add(b, MAllocRatio, row.AllocRatio, row.attrs()...)
		add(b, MThroughput, row.CompressBps, row.attrs(otel.S(aDir, "compress"))...)
		add(b, MThroughput, row.DecompressBps, row.attrs(otel.S(aDir, "decompress"))...)
		add(b, MSize, float64(row.OrigBytes), row.attrs(otel.S("river.bench.size.kind", "original"))...)
		add(b, MSize, float64(row.CompBytes), row.attrs(otel.S("river.bench.size.kind", "compressed"))...)
		add(b, MSize, float64(row.AllocBytes), row.attrs(otel.S("river.bench.size.kind", "allocated"))...)
		cache := "warm"
		if row.ColdDecompress {
			cache = "cold"
		}
		add(b, MDuration, row.CompressS, row.attrs(otel.S(aDir, "compress"))...)
		add(b, MDuration, row.DecompressS, row.attrs(otel.S(aDir, "decompress"), otel.S("river.bench.cache", cache))...)
		for i, mode := range []string{"user", "system"} {
			add(b, MCPU, row.CompressCPU[i], row.attrs(otel.S(aDir, "compress"), otel.S("cpu.mode", mode))...)
			add(b, MCPU, row.DecompressCPU[i], row.attrs(otel.S(aDir, "decompress"), otel.S("cpu.mode", mode))...)
		}
		for _, k := range sortedKeys(row.Fractions) {
			if row.Fractions[k] > 0 {
				add(b, MFraction, row.Fractions[k], row.attrs(otel.S("river.bench.block.class", k))...)
			}
		}
		if v, ok := row.Extra["slowdown"]; ok {
			add(b, MSlowdown, v, otel.S(aLayer, row.Layer), otel.S(aSet, row.Setting))
		}
		for _, dir := range []string{"in", "out"} {
			if v, ok := row.Extra["pswp"+dir]; ok {
				add(b, MPaging, v, otel.S(aLayer, row.Layer), otel.S(aSet, row.Setting), otel.S("system.paging.direction", dir))
			}
		}
	}
	for _, io := range r.IO {
		add(b, MIOThroughput, io.Bps, otel.S("river.bench.io.test", io.Test), otel.S("river.bench.io.method", io.Method),
			otel.I("river.bench.io.block_size", int64(io.BlockSize)), otel.I("river.bench.io.queue_depth", int64(io.QueueDepth)),
			otel.S("river.bench.fadvise", io.Fadvise), otel.S("river.bench.cache", io.Cache), otel.S("river.bench.filesystem", io.Filesystem))
	}
	for _, s := range r.Scan {
		add(b, MScan, s.RowsPS, otel.S("river.bench.scan.memory", s.Memory), otel.S("river.bench.scan.phase", s.Phase),
			otel.S("river.bench.thp", s.THP), otel.I("river.bench.threads", int64(s.Threads)))
	}
	for _, rec := range r.Recommendations {
		add(b, MRecommend, 1, otel.S(aLayer, rec.Layer), otel.S("river.bench.scope", rec.Scope),
			otel.S(aSet, rec.Setting), otel.S("river.bench.rule", rec.Rule))
	}
	return b
}

func sortedKeys[V any](m map[string]V) []string {
	k := make([]string, 0, len(m))
	for x := range m {
		k = append(k, x)
	}
	sort.Strings(k)
	return k
}

// Flatten turns a batch into the sorted, diffable metric list of the JSON report.
func Flatten(b *otel.Batch) []FlatMetric {
	var out []FlatMetric
	for _, m := range b.Metrics {
		for _, p := range m.Points {
			f := FlatMetric{Name: m.Name, Unit: m.Unit, Value: p.Value, Attributes: map[string]string{}}
			for _, a := range p.Attrs {
				switch a.Type {
				case otel.TString:
					f.Attributes[a.Key] = a.Str
				case otel.TInt:
					f.Attributes[a.Key] = fmt.Sprint(a.Int)
				case otel.TDouble:
					f.Attributes[a.Key] = fmt.Sprint(a.Dbl)
				case otel.TBool:
					f.Attributes[a.Key] = fmt.Sprint(a.Bool)
				}
			}
			out = append(out, f)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Key() < out[j].Key() })
	return out
}

// Key identifies a metric point across runs: its name and every attribute.
func (f FlatMetric) Key() string {
	var sb strings.Builder
	sb.WriteString(f.Name)
	for _, k := range sortedKeys(f.Attributes) {
		sb.WriteString("|" + k + "=" + f.Attributes[k])
	}
	return sb.String()
}

// WriteJSON writes the report (with Metrics filled from its batch).
func (r *Report) WriteJSON(w io.Writer) error {
	r.Metrics = Flatten(r.Batch())
	r.Schema, r.CatalogueVersion = Schema, CatalogueVersion
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(r)
}

// Load reads a compress report.
func Load(path string) (*Report, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var r Report
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if r.Schema != Schema {
		return nil, fmt.Errorf("%s: schema %q, want %q", path, r.Schema, Schema)
	}
	return &r, nil
}

func mbps(v float64) string {
	if v == 0 {
		return "-"
	}
	if v > 1e11 {
		return ">100000"
	}
	return fmt.Sprintf("%.0f", v/1e6)
}

func ratio(v float64) string {
	if v == 0 {
		return "-"
	}
	return fmt.Sprintf("%.3f", v)
}

func secs(v float64) string {
	if v == 0 {
		return "-"
	}
	if v < 1 {
		return fmt.Sprintf("%.1f ms", v*1000)
	}
	return fmt.Sprintf("%.2f s", v)
}

func sizeStr(v int64) string {
	switch {
	case v == 0:
		return "-"
	case v >= 1<<30:
		return fmt.Sprintf("%.2f GiB", float64(v)/(1<<30))
	case v >= 1<<20:
		return fmt.Sprintf("%.1f MiB", float64(v)/(1<<20))
	}
	return fmt.Sprintf("%.1f KiB", float64(v)/(1<<10))
}

func bsStr(n int) string {
	switch {
	case n == 0:
		return "stream"
	case n%(1<<20) == 0:
		return fmt.Sprintf("%dM", n>>20)
	case n%(1<<10) == 0:
		return fmt.Sprintf("%dK", n>>10)
	}
	return fmt.Sprint(n)
}

// WriteMarkdown renders the report for people.
func (r *Report) WriteMarkdown(w io.Writer) {
	fmt.Fprintf(w, "# riverbench compress: %s\n\n", r.Label)
	if r.Smoke {
		fmt.Fprintf(w, "> **Smoke run.** Small samples to validate the harness; do not quote these numbers.\n\n")
	}
	p := r.Provenance
	where := "this host is NOT a Runink River install; numbers that depend on the filesystem or kernel configuration must be re-run on one"
	if p.IsRunink {
		where = "Runink River (" + p.HostKind + ")"
	}
	fmt.Fprintf(w, "| | |\n|---|---|\n| Run | `%s` (commit `%s`) |\n| Host | %s |\n| OS | %s, kernel `%s` |\n| CPU | %s, %d threads |\n| RAM | %.1f GiB |\n| Root / work filesystem | %s / %s |\n| Load average start / end | %s / %s |\n",
		p.RunID, short(p.BenchCommit), where, p.OSName, r.Host.KernelRelease, r.Host.CPUModel, r.Host.LogicalCPUs,
		float64(r.Host.MemTotalKiB)/(1<<20), p.RootFS, p.WorkFS, p.LoadStart, p.LoadEnd)
	for _, k := range sortedKeys(r.Tools) {
		fmt.Fprintf(w, "| %s | %s |\n", k, r.Tools[k])
	}
	var layers []string
	seen := map[string]bool{}
	for _, row := range r.Rows {
		if !seen[row.Layer] {
			seen[row.Layer] = true
			layers = append(layers, row.Layer)
		}
	}
	for _, l := range layers {
		if l == "zfs-codec" {
			fmt.Fprintf(w, "\n(The raw codec benchmarks behind the zfs table, layer `zfs-codec`, are in compress.json.)\n")
			continue
		}
		fmt.Fprintf(w, "\n## %s\n\n| Shape | Setting | Block | Method | Ratio | Alloc ratio | Compress MB/s/core | Decompress MB/s/core | Compress | Decompress | Orig | Stored | Notes |\n|---|---|---|---|---:|---:|---:|---:|---:|---:|---:|---:|---|\n", l)
		for _, row := range r.Rows {
			if row.Layer != l {
				continue
			}
			stored := row.AllocBytes
			if stored == 0 {
				stored = row.CompBytes
			}
			notes := row.Notes
			for _, k := range sortedKeys(row.Fractions) {
				if row.Fractions[k] > 0 {
					notes = strings.TrimSpace(fmt.Sprintf("%s %s=%.0f%%", notes, k, row.Fractions[k]*100))
				}
			}
			for _, k := range sortedKeys(row.Extra) {
				notes = strings.TrimSpace(fmt.Sprintf("%s %s=%.4g", notes, k, row.Extra[k]))
			}
			fmt.Fprintf(w, "| %s | %s | %s | %s | %s | %s | %s | %s | %s | %s | %s | %s | %s |\n", row.Shape, row.Setting, bsStr(row.BlockSize),
				row.Method, ratio(row.Ratio), ratio(row.AllocRatio), mbps(row.CompressBps), mbps(row.DecompressBps),
				secs(row.CompressS), secs(row.DecompressS), sizeStr(row.OrigBytes), sizeStr(stored), notes)
		}
	}
	if len(r.IO) > 0 {
		fmt.Fprintf(w, "\n## File access (Parquet)\n\n| Test | Method | Block | QD | fadvise | Cache | FS | MB/s | Notes |\n|---|---|---|---:|---|---|---|---:|---|\n")
		for _, io := range r.IO {
			fmt.Fprintf(w, "| %s | %s | %s | %d | %s | %s | %s | %s | %s |\n", io.Test, io.Method, bsStr(io.BlockSize), io.QueueDepth,
				io.Fadvise, io.Cache, io.Filesystem, mbps(io.Bps), io.Notes)
		}
	}
	if len(r.Scan) > 0 {
		fmt.Fprintf(w, "\n## Columnar scan and transparent huge pages\n\n| Memory | Phase | THP | Threads | Mrows/s | Seconds | Notes |\n|---|---|---|---:|---:|---:|---|\n")
		for _, s := range r.Scan {
			fmt.Fprintf(w, "| %s | %s | %s | %d | %.1f | %.3f | %s |\n", s.Memory, s.Phase, s.THP, s.Threads, s.RowsPS/1e6, s.Seconds, s.Notes)
		}
	}
	if len(r.Recommendations) > 0 {
		fmt.Fprintf(w, "\n## What the declared rules pick\n\n| Layer | Scope | Setting | Rule | Why |\n|---|---|---|---|---|\n")
		for _, rec := range r.Recommendations {
			fmt.Fprintf(w, "| %s | %s | **%s** | %s | %s |\n", rec.Layer, rec.Scope, rec.Setting, rec.Rule, rec.Reason)
		}
	}
	if len(r.Skipped) > 0 {
		fmt.Fprintf(w, "\n**Not run:**\n\n")
		for _, s := range r.Skipped {
			fmt.Fprintf(w, "- `%s`: %s\n", s.What, s.Reason)
		}
	}
}

func short(s string) string {
	if len(s) > 12 {
		return s[:12]
	}
	return s
}

// ---- compare: regressions and improvements between two runs, as JSON for agents.

// Change is one metric point that moved beyond the threshold.
type Change struct {
	Name       string            `json:"name"`
	Unit       string            `json:"unit"`
	Attributes map[string]string `json:"attributes"`
	Base       float64           `json:"base"`
	New        float64           `json:"new"`
	DeltaPct   float64           `json:"delta_pct"`
	Better     string            `json:"better"` // "higher" or "lower"
}

// Comparison is the result of comparing two reports.
type Comparison struct {
	Schema           string   `json:"schema"`
	BaseRun          string   `json:"base_run"`
	NewRun           string   `json:"new_run"`
	ThresholdPct     float64  `json:"threshold_pct"`
	Comparable       bool     `json:"comparable"`
	Warnings         []string `json:"warnings,omitempty"`
	Regressions      []Change `json:"regressions"`
	Improvements     []Change `json:"improvements"`
	Unchanged        int      `json:"unchanged"`
	NotDirectional   int      `json:"not_directional"`
	OnlyInBase       []string `json:"only_in_base,omitempty"`
	OnlyInNew        []string `json:"only_in_new,omitempty"`
	RecommendChanged []string `json:"recommendation_changes,omitempty"`
}

// Compare diffs two reports metric by metric. A change is a regression or an improvement
// when it moves by more than thresholdPct in the catalogue's worse or better direction.
func Compare(a, b *Report, thresholdPct float64) Comparison {
	c := Comparison{Schema: "riverbench-compress-compare/v1", BaseRun: a.Provenance.RunID, NewRun: b.Provenance.RunID,
		ThresholdPct: thresholdPct, Comparable: true, Regressions: []Change{}, Improvements: []Change{}}
	if a.CatalogueVersion != b.CatalogueVersion {
		c.Comparable = false
		c.Warnings = append(c.Warnings, fmt.Sprintf("catalogue versions differ (%s vs %s): names may not mean the same thing", a.CatalogueVersion, b.CatalogueVersion))
	}
	if a.Host.CPUModel != b.Host.CPUModel || a.Host.MemTotalKiB != b.Host.MemTotalKiB {
		c.Warnings = append(c.Warnings, fmt.Sprintf("different hardware (%q vs %q): throughput changes are not a like-for-like comparison", a.Host.CPUModel, b.Host.CPUModel))
	}
	if a.Smoke || b.Smoke {
		c.Warnings = append(c.Warnings, "a smoke run is involved")
	}
	idx := map[string]FlatMetric{}
	for _, m := range a.Metrics {
		idx[m.Key()] = m
	}
	seen := map[string]bool{}
	for _, m := range b.Metrics {
		k := m.Key()
		seen[k] = true
		base, ok := idx[k]
		if !ok {
			c.OnlyInNew = append(c.OnlyInNew, k)
			continue
		}
		if m.Name == MRecommend.Name {
			continue
		}
		d, ok := DefByName(m.Name)
		if !ok || d.Name == MFraction.Name || d.Name == MSize.Name && m.Attributes["river.bench.size.kind"] == "original" || base.Value == 0 {
			c.NotDirectional++
			continue
		}
		delta := (m.Value - base.Value) / math.Abs(base.Value) * 100
		ch := Change{Name: m.Name, Unit: m.Unit, Attributes: m.Attributes, Base: base.Value, New: m.Value,
			DeltaPct: math.Round(delta*100) / 100, Better: "lower"}
		if d.HigherIsBetter {
			ch.Better = "higher"
		}
		switch {
		case math.Abs(delta) <= thresholdPct:
			c.Unchanged++
		case (delta > 0) == d.HigherIsBetter:
			c.Improvements = append(c.Improvements, ch)
		default:
			c.Regressions = append(c.Regressions, ch)
		}
	}
	for _, m := range a.Metrics {
		if !seen[m.Key()] {
			c.OnlyInBase = append(c.OnlyInBase, m.Key())
		}
	}
	recs := func(r *Report) map[string]string {
		out := map[string]string{}
		for _, x := range r.Recommendations {
			out[x.Layer+" "+x.Scope] = x.Setting
		}
		return out
	}
	ra, rb := recs(a), recs(b)
	for _, k := range sortedKeys(rb) {
		if ra[k] != "" && ra[k] != rb[k] {
			c.RecommendChanged = append(c.RecommendChanged, fmt.Sprintf("%s: %s -> %s", k, ra[k], rb[k]))
		}
	}
	sort.Strings(c.OnlyInBase)
	sort.Strings(c.OnlyInNew)
	byMag := func(x []Change) {
		sort.SliceStable(x, func(i, j int) bool { return math.Abs(x[i].DeltaPct) > math.Abs(x[j].DeltaPct) })
	}
	byMag(c.Regressions)
	byMag(c.Improvements)
	return c
}
