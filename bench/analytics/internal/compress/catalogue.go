package compress

import "github.com/org-runink/river/bench/analytics/internal/otel"

// CatalogueVersion versions the metric names, units and attributes below. It is on every
// export's resource (river.bench.schema.version) so an agent that reads two runs can tell
// whether their names are comparable. Renaming or re-uniting a metric bumps it; adding one
// does not.
const CatalogueVersion = "1"

// Def is one catalogue entry.
type Def struct {
	Name           string
	Unit           string
	Kind           otel.Kind
	HigherIsBetter bool
	Description    string
	Attributes     string // the attributes its points carry, for the README
}

// The catalogue. bench/analytics/README.md ("Metric catalogue") is generated from this
// table by `riverbench catalogue`, and a test keeps the two identical.
var (
	MRatio = Def{"river.bench.compression.ratio", "1", otel.Gauge, true,
		"Uncompressed bytes / compressed bytes, as the codec produced them (no allocation rounding).",
		"layer, shape, setting, codec, codec.level, block_size, method"}
	MAllocRatio = Def{"river.bench.compression.allocated_ratio", "1", otel.Gauge, true,
		"Data bytes / bytes the consumer allocates: ZFS (vs the file bytes) after the 1/8 rule, early abort and ashift rounding; zram after same-filled and huge pages and size classes; on-pool, logicalused/used.",
		"layer, shape, setting, block_size, method"}
	MThroughput = Def{"river.bench.compression.throughput", "By/s", otel.Gauge, true,
		"Uncompressed bytes per second through the codec, ONE core (compress: input rate; decompress: output rate). With method=model it includes OpenZFS early abort.",
		"layer, shape, setting, block_size, direction, method"}
	MSize = Def{"river.bench.compression.size", "By", otel.Gauge, false,
		"Bytes of a measured object: original, compressed or allocated (size.kind).",
		"layer, shape, setting, block_size, size.kind"}
	MDuration = Def{"river.bench.compression.duration", "s", otel.Gauge, false,
		"Wall time of a whole-stream compress or decompress (kernel, initramfs, Parquet write/read, on-pool write/read), median of the repetitions.",
		"layer, shape, setting, direction, cache"}
	MCPU = Def{"process.cpu.time", "s", otel.Counter, false,
		"OpenTelemetry semantic convention: CPU time of the measured process (the codec tool or DuckDB), split by cpu.mode.",
		"layer, shape, setting, direction, cpu.mode"}
	MFraction = Def{"river.bench.compression.block_fraction", "1", otel.Gauge, false,
		"Fraction of blocks (records or pages) in a class: stored_raw, early_abort, same_filled, huge. Describes the data; compare does not rank it.",
		"layer, shape, setting, block_size, block.class"}
	MPaging = Def{"system.paging.operations", "{operation}", otel.Counter, false,
		"OpenTelemetry semantic convention: pages swapped during the memory-pressure run (/proc/vmstat pswpin/pswpout deltas).",
		"layer, setting, system.paging.direction"}
	MSlowdown = Def{"river.bench.zram.slowdown", "1", otel.Gauge, false,
		"Wall time of the workload under the memory limit / wall time unconstrained.",
		"layer, setting"}
	MIOThroughput = Def{"river.bench.io.throughput", "By/s", otel.Gauge, true,
		"Read throughput of a file-access method on a Parquet file (cold page cache unless cache=warm).",
		"io.test, io.method, io.block_size, io.queue_depth, fadvise, cache, filesystem"}
	MScan = Def{"river.bench.scan.throughput", "{row}/s", otel.Gauge, true,
		"Rows per second of a columnar filter-and-aggregate scan (TPC-H Q6 shape), or of DuckDB's Q1 over Parquet.",
		"scan.memory, scan.phase, thp, threads"}
	MRecommend = Def{"river.bench.recommendation", "1", otel.Gauge, true,
		"1 for the setting the run's declared rule picks for a layer and scope (rule attribute says which rule).",
		"layer, scope, setting, rule"}
)

// Catalogue lists every metric riverbench can emit (export.go appends the kernel-run ones).
var Catalogue = []Def{MRatio, MAllocRatio, MThroughput, MSize, MDuration, MCPU, MFraction,
	MPaging, MSlowdown, MIOThroughput, MScan, MRecommend}

// DefByName finds a catalogue entry.
func DefByName(name string) (Def, bool) {
	for _, d := range Catalogue {
		if d.Name == name {
			return d, true
		}
	}
	return Def{}, false
}
