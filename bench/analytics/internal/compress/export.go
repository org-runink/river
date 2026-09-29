package compress

import (
	"fmt"
	"strings"

	"github.com/org-runink/river/bench/analytics/internal/otel"
	"github.com/org-runink/river/bench/analytics/internal/report"
)

// Kernel-comparison results (riverbench run, schema riverbench/v1) as OpenTelemetry
// metrics: each suite metric's median, min and max, converted to base units, under a
// metric name per quantity so one Prometheus series never mixes units.
var (
	MKThroughput = Def{"river.bench.kernel.throughput", "By/s", otel.Gauge, true,
		"Kernel comparison: throughput (STREAM, fio bandwidth).", "suite, metric, stat, run.label"}
	MKRowRate = Def{"river.bench.kernel.row_rate", "{row}/s", otel.Gauge, true,
		"Kernel comparison: rows per second (parallel sort, hash join).", "suite, metric, stat, run.label"}
	MKIOPS = Def{"river.bench.kernel.iops", "{operation}/s", otel.Gauge, true,
		"Kernel comparison: I/O operations per second (fio random reads).", "suite, metric, stat, run.label"}
	MKLatency = Def{"river.bench.kernel.latency", "s", otel.Gauge, false,
		"Kernel comparison: latency (fio p99).", "suite, metric, stat, run.label"}
	MKDuration = Def{"river.bench.kernel.duration", "s", otel.Gauge, false,
		"Kernel comparison: wall time (TPC-H queries).", "suite, metric, stat, run.label"}
	MKSpread = Def{"river.bench.kernel.spread", "%", otel.Gauge, false,
		"Kernel comparison: (max - min) / median of the repetitions.", "suite, metric, run.label"}
)

func init() {
	Catalogue = append(Catalogue, MKThroughput, MKRowRate, MKIOPS, MKLatency, MKDuration, MKSpread)
}

// kernelUnit maps a riverbench/v1 unit to a catalogue entry and a factor to base units.
func kernelUnit(u string) (Def, float64, bool) {
	switch u {
	case "GB/s":
		return MKThroughput, 1e9, true
	case "MiB/s":
		return MKThroughput, 1 << 20, true
	case "Mrows/s":
		return MKRowRate, 1e6, true
	case "IOPS":
		return MKIOPS, 1, true
	case "us":
		return MKLatency, 1e-6, true
	case "s":
		return MKDuration, 1, true
	}
	return Def{}, 0, false
}

// KernelBatch converts a kernel-comparison results file.
func KernelBatch(r *report.Results) *otel.Batch {
	b := &otel.Batch{ScopeName: "riverbench/run", ScopeVersion: CatalogueVersion, Start: r.Started, Time: r.Finished,
		Resource: []otel.Attr{
			otel.S("service.name", "riverbench"), otel.S("service.version", CatalogueVersion),
			otel.S("river.bench.schema.version", report.Schema+"+catalogue."+CatalogueVersion),
			otel.S("river.bench.run.label", r.Label), otel.B("river.bench.smoke", r.Smoke),
			otel.S("os.type", "linux"), otel.S("os.version", r.Host.KernelRelease), otel.S("host.arch", "amd64"),
			otel.S("host.cpu.model.name", r.Host.CPUModel), otel.I("system.memory.limit", r.Host.MemTotalKiB*1024),
			otel.S("river.bench.kernel.config_sha256", r.Host.ConfigSHA256),
		}}
	for _, m := range r.Metrics {
		d, f, ok := kernelUnit(m.Unit)
		if !ok || m.Summary.N == 0 {
			continue
		}
		base := []otel.Attr{otel.S("river.bench.suite", m.Suite), otel.S("river.bench.metric", m.Name), otel.S("river.bench.run.label", r.Label)}
		for _, s := range []struct {
			k string
			v float64
		}{{"median", m.Summary.Median}, {"min", m.Summary.Min}, {"max", m.Summary.Max}} {
			add(b, d, s.v*f, append(append([]otel.Attr{}, base...), otel.S("river.bench.stat", s.k))...)
		}
		add(b, MKSpread, m.Summary.SpreadPct, base...)
	}
	return b
}

// CatalogueMarkdown renders the catalogue for bench/analytics/README.md.
func CatalogueMarkdown() string {
	var sb strings.Builder
	sb.WriteString("| Metric (OTLP name) | Prometheus name | Unit | Type | Better | Attributes | Meaning |\n|---|---|---|---|---|---|---|\n")
	for _, d := range Catalogue {
		kind, better := "gauge", "lower"
		if d.Kind == otel.Counter {
			kind = "counter (cumulative sum)"
		}
		if d.HigherIsBetter {
			better = "higher"
		}
		if d.Name == MFraction.Name || d.Name == MRecommend.Name {
			better = "not ranked"
		}
		prom := otel.PromName(otel.Metric{Name: d.Name, Unit: d.Unit, Kind: d.Kind})
		fmt.Fprintf(&sb, "| `%s` | `%s` | `%s` | %s | %s | %s | %s |\n", d.Name, prom, d.Unit, kind, better, d.Attributes, d.Description)
	}
	return sb.String()
}
