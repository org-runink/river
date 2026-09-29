<!--
SPDX-FileCopyrightText: 2026 The RIVER Authors
SPDX-License-Identifier: MIT
-->

# riverbench: analytics benchmark for kernel comparisons

Compares kernels (linux-runink, linux-zen, linux-lts) on the same machine with
analytics-shaped loads: DuckDB TPC-H (pinned CLI release, `duckdb.lock`), fio sequential and
random reads on ZFS and ext4, a STREAM-like memory bandwidth test and a parallel sort /
hash-join microbenchmark. Bash and Go standard library only. `riverbench compress` (below)
measures compression, file access and huge pages at every layer of the OS.

```bash
./run.sh --smoke --out /tmp/rb-smoke                      # validate the harness (small sizes)
./run.sh --out results/$(uname -r) --fio-dirs zfs=/tank/bench,ext4=/mnt/ext4/bench
./run.sh compare results/*/results.json                   # after one run per kernel
```

One run measures the running kernel: 1 warm-up and 5 measured repetitions per suite, reported
as median, min, max and spread, with the kernel release, command line, config hash, runtime
policy, CPU and RAM recorded in `results.json` (rendered to `results.md`). Smoke results are
labelled as such and are never a comparison.

Procedure, fairness checklist and the meaning of each suite: [docs/KERNEL.md](../../docs/KERNEL.md#benchmarking).
Tests: `make test-bench`.

## Compression, file access and huge pages: `riverbench compress`

Measures what a Runink River machine does with analytics data at every layer that
compresses or moves it, and says which setting a declared rule picks for each:

```bash
./run.sh compress --smoke --out /tmp/rb-compress-smoke        # validate the harness (minutes)
./run.sh compress --out results/compress-$(uname -r)          # the real run (about an hour)
./run.sh compare results/compress-A/compress.json results/compress-B/compress.json   # JSON diff
```

| Layer | What is measured | How | Needs |
|---|---|---|---|
| `zfs` | `off`, `lz4`, `zstd-fast-1`, `zstd-1,3,6,9,19` x recordsize 128K and 1M on each data shape | each record compressed on its own by the reference `zstd`/`lz4` tool (frame overhead removed), then OpenZFS's rules applied: keep only if it saves 1/8, round to the 4 KiB sector (ashift=12), zero-padded tail records, small files as one block, and zstd early abort (OpenZFS >= 2.2: LZ4 then zstd-1 are tried first on records >= 128 KiB for levels >= 3). Throughput is the tool's own single-core benchmark (`zstd -b`/`lz4 -b`, in memory) combined with the same early-abort path | rootless |
| `zfs-pool` | the same settings on a real dataset: `logicalused`/`used`, write (incl. `zpool sync`) and read time with `primarycache=metadata`, machine-wide CPU | child datasets of `-zfs-parent` (they inherit its encryption), destroyed afterwards | root, ZFS: a Runink River machine |
| `zram` | `lz4`, `zstd-1`, `zstd-3` on 4 KiB pages of REAL process memory: DuckDB holding TPC-H lineitem with a sorted copy and a group-by, and a Go process holding parsed rows | resident anonymous pages read from `/proc/<child>/mem` (pagemap-filtered), then zram's rules: same-filled pages cost nothing, pages compressing to >= 3264 bytes are stored whole, 16-byte size classes | rootless (child processes only) |
| `zram-live` | the host's active zram device: in-kernel ratio of whatever it has swapped | `/sys/block/zram*/mm_stat` | rootless |
| `zram-pressure` | `lz4`, `lzo-rle`, `zstd` in the kernel: the DuckDB sort/join under a cgroup memory limit of half its peak, a dedicated zram device per codec; slowdown, swap-ins/outs, peak ratio, PSI | cgroup v2 + zram hot_add | root: a Runink River machine. Without root it runs once on the host's own zram through `systemd-run --user --scope` where that exists |
| `kernel`, `initramfs`, `modules` | the kernel image, an initramfs (modules uncompressed or `.ko.zst` inside) and a module sample under `zstd-1..19` (and 22 for the image), `xz-6`, `lz4-9`, `gzip-9`: size, compress and decompress time | the built `linux-runink` / `runink-zfs` / `runink-zfs-utils` packages (`RIVER_ARTIFACTS`), read-only; user-space decompression as the proxy for the kernel's | rootless |
| `parquet` | DuckDB Parquet writer codecs `uncompressed`, `snappy`, `lz4_raw`, `zstd-1/3/9`, `gzip` on TPC-H lineitem: file size, write time and CPU, cold and warm full scans | pinned DuckDB; cold = the file evicted with `posix_fadvise(DONTNEED)` | rootless |
| `io` | reading a Parquet file: page cache under each readahead hint, `O_DIRECT` at 1 and 8 threads, `io_uring` at queue depth 8 and 32, for a full scan (1 MiB reads) and a projection (256 KiB of every 1 MiB) | raw syscalls (`io_uring` included), cold unless stated | rootless |
| `scan` | transparent huge pages for a columnar scan: TPC-H Q6 predicate over mmap'd columns with `MADV_HUGEPAGE` vs `MADV_NOHUGEPAGE`, and DuckDB's Q1 over Parquet with THP on vs off for the process (`PR_SET_THP_DISABLE`) | rootless | rootless |

Data shapes (`-sample` bytes each, 64 MiB by default): TPC-H lineitem as Parquet (snappy and
zstd pages), CSV, NDJSON and Avro (uncompressed object container); a Delta Lake
`_delta_log` (many small JSON commits) and its Parquet checkpoint; GGUF-shaped synthetic
model weights (bf16, Q8_0, Q4_0 of N(0, 0.02): they bound, not predict, real weights);
photo-like JPEGs; the host's `/usr/bin` + `/usr/lib` files; the same as a tar.gz container
layer; the Go toolchain's source tree. Everything generated is seeded; the host-file shapes
say which host they came from.

**Where the numbers come from.** The codec numbers (ratio, single-core MB/s) are properties
of the codec and the data and hold on any x86-64 machine. The ZFS and zram columns are a
model of what the kernel allocates, with each rule cited in `internal/compress/model.go`;
`zfs-pool` and `zram-pressure` are the measurements that confirm or refute it, and they need
a Runink River machine. File-access numbers depend on the filesystem and drive and are only
meaningful on the machine they describe: the report says which filesystem it ran on.

**Recommendations** come from rules declared in `internal/compress/recommend.go` before any
run, with their thresholds (`DefaultRules`), and are applied the same way to every report,
so they can be recomputed and diffed. Each dataset's content is a declared mix of shapes
(`Mixes`): the boot environment, `/home`, a data-lake directory, a model store, and on a
server that runs a Kubernetes node, the container image store and node state.

### Telemetry: OpenTelemetry metrics, Prometheus

Every run writes `compress.json` (rows, provenance, recommendations and a flattened,
sorted `metrics` list with the same names and attributes as the export) and
`compress.md`. Nothing leaves the machine unless the operator asks:

```bash
riverbench compress ... -prom-file /var/lib/node_exporter/riverbench.prom   # text exposition 0.0.4
riverbench compress ... -otlp-endpoint http://collector.example.org:4318/v1/metrics
riverbench compress ... -otlp-endpoint http://prometheus.example.org:9090/api/v1/otlp/v1/metrics \
                        -otlp-format protobuf        # Prometheus 3 with --web.enable-otlp-receiver
riverbench export -in results.json -prom-file kernel.prom   # a kernel comparison run, too
```

There is no default endpoint: `-otlp-endpoint` empty means no network traffic, and the
image ships no collector. OTLP/HTTP is sent as protobuf (default) or JSON
(`-otlp-format json`); `-otlp-header k=v` adds headers for an authenticating collector.
The Prometheus names in the table are exactly what Prometheus 3's OTLP receiver produces
from the OTLP names under its default translation (`UnderscoreEscapingWithSuffixes`), so a
query works the same whichever path the data took; the resource becomes `target_info`.

**For agents.** The resource of every export carries `river.bench.schema.version`
(`riverbench-compress/v1+catalogue.N`; a rename bumps N), `river.bench.run.id`,
`river.bench.run.label`, `river.bench.host.kind` (workstation, server, other),
`river.bench.host.is_runink_river`, `vcs.ref.head.revision` (the bench's commit),
`os.name`, `os.version` (kernel release), `host.cpu.model.name`, `system.memory.limit`,
`river.bench.fs.root` / `river.bench.fs.work`, and on ZFS `river.bench.zfs.version` and
the parent dataset's properties (`river.bench.zfs.dataset.*`). The JSON report is
deterministic apart from the run id and the start and end times.
`riverbench compare [-threshold 5] base.json new.json` prints JSON: `regressions` and
`improvements` (beyond the threshold, in each metric's better direction), `unchanged`,
points present in only one run, changed recommendations, and warnings when the hardware,
catalogue version or smoke status differ.

### Metric catalogue

Attribute keys are `river.bench.<name>` except the OpenTelemetry semantic-convention ones
(`cpu.mode`, `system.paging.direction`). `river.bench.method` is `codec-bench`, `model`,
`stream`, `duckdb`, `on-pool` or `in-kernel`. Generated by `riverbench catalogue`; a test
keeps this table and the code identical.

| Metric (OTLP name) | Prometheus name | Unit | Type | Better | Attributes | Meaning |
|---|---|---|---|---|---|---|
| `river.bench.compression.ratio` | `river_bench_compression_ratio` | `1` | gauge | higher | layer, shape, setting, codec, codec.level, block_size, method | Uncompressed bytes / compressed bytes, as the codec produced them (no allocation rounding). |
| `river.bench.compression.allocated_ratio` | `river_bench_compression_allocated_ratio` | `1` | gauge | higher | layer, shape, setting, block_size, method | Data bytes / bytes the consumer allocates: ZFS (vs the file bytes) after the 1/8 rule, early abort and ashift rounding; zram after same-filled and huge pages and size classes; on-pool, logicalused/used. |
| `river.bench.compression.throughput` | `river_bench_compression_throughput_bytes_per_second` | `By/s` | gauge | higher | layer, shape, setting, block_size, direction, method | Uncompressed bytes per second through the codec, ONE core (compress: input rate; decompress: output rate). With method=model it includes OpenZFS early abort. |
| `river.bench.compression.size` | `river_bench_compression_size_bytes` | `By` | gauge | lower | layer, shape, setting, block_size, size.kind | Bytes of a measured object: original, compressed or allocated (size.kind). |
| `river.bench.compression.duration` | `river_bench_compression_duration_seconds` | `s` | gauge | lower | layer, shape, setting, direction, cache | Wall time of a whole-stream compress or decompress (kernel, initramfs, Parquet write/read, on-pool write/read), median of the repetitions. |
| `process.cpu.time` | `process_cpu_time_seconds_total` | `s` | counter (cumulative sum) | lower | layer, shape, setting, direction, cpu.mode | OpenTelemetry semantic convention: CPU time of the measured process (the codec tool or DuckDB), split by cpu.mode. |
| `river.bench.compression.block_fraction` | `river_bench_compression_block_fraction_ratio` | `1` | gauge | not ranked | layer, shape, setting, block_size, block.class | Fraction of blocks (records or pages) in a class: stored_raw, early_abort, same_filled, huge. Describes the data; compare does not rank it. |
| `system.paging.operations` | `system_paging_operations_total` | `{operation}` | counter (cumulative sum) | lower | layer, setting, system.paging.direction | OpenTelemetry semantic convention: pages swapped during the memory-pressure run (/proc/vmstat pswpin/pswpout deltas). |
| `river.bench.zram.slowdown` | `river_bench_zram_slowdown_ratio` | `1` | gauge | lower | layer, setting | Wall time of the workload under the memory limit / wall time unconstrained. |
| `river.bench.io.throughput` | `river_bench_io_throughput_bytes_per_second` | `By/s` | gauge | higher | io.test, io.method, io.block_size, io.queue_depth, fadvise, cache, filesystem | Read throughput of a file-access method on a Parquet file (cold page cache unless cache=warm). |
| `river.bench.scan.throughput` | `river_bench_scan_throughput_per_second` | `{row}/s` | gauge | higher | scan.memory, scan.phase, thp, threads | Rows per second of a columnar filter-and-aggregate scan (TPC-H Q6 shape), or of DuckDB's Q1 over Parquet. |
| `river.bench.recommendation` | `river_bench_recommendation_ratio` | `1` | gauge | not ranked | layer, scope, setting, rule | 1 for the setting the run's declared rule picks for a layer and scope (rule attribute says which rule). |
| `river.bench.kernel.throughput` | `river_bench_kernel_throughput_bytes_per_second` | `By/s` | gauge | higher | suite, metric, stat, run.label | Kernel comparison: throughput (STREAM, fio bandwidth). |
| `river.bench.kernel.row_rate` | `river_bench_kernel_row_rate_per_second` | `{row}/s` | gauge | higher | suite, metric, stat, run.label | Kernel comparison: rows per second (parallel sort, hash join). |
| `river.bench.kernel.iops` | `river_bench_kernel_iops_per_second` | `{operation}/s` | gauge | higher | suite, metric, stat, run.label | Kernel comparison: I/O operations per second (fio random reads). |
| `river.bench.kernel.latency` | `river_bench_kernel_latency_seconds` | `s` | gauge | lower | suite, metric, stat, run.label | Kernel comparison: latency (fio p99). |
| `river.bench.kernel.duration` | `river_bench_kernel_duration_seconds` | `s` | gauge | lower | suite, metric, stat, run.label | Kernel comparison: wall time (TPC-H queries). |
| `river.bench.kernel.spread` | `river_bench_kernel_spread_percent` | `%` | gauge | lower | suite, metric, run.label | Kernel comparison: (max - min) / median of the repetitions. |
