<!--
SPDX-FileCopyrightText: 2026 The RIVER Authors
SPDX-License-Identifier: MIT
-->

# Compression and I/O defaults for Runink River

What each layer of a Runink River machine should use to compress and read analytics data,
chosen from measurements with [`riverbench compress`](../bench/analytics/README.md) and
the declared rules in `bench/analytics/internal/compress/recommend.go`. It covers the
workstation image (`iso-profiles/river`) and a downstream server built from it that runs
a Kubernetes node (the installer's `state` and `containers` datasets).

> **Where these numbers come from.** Everything below was measured on ONE host that is
> **not** a Runink River install: AMD Ryzen 7 8840U (8 cores, 16 threads), 25 GiB RAM,
> NVMe, CachyOS with kernel 6.18 LTS, btrfs (`compress=zstd:3`) on dm-crypt. Codec ratios
> and single-core speeds are properties of the codec and the data and carry over to other
> x86-64 machines; the ZFS and zram columns are a **model** of the kernel's allocation
> rules fed with those measurements. Rows marked **pending** need `riverbench compress
> -layers zfs-pool,zram-pressure` as root on a Runink River machine before they can be
> quoted as on-pool or in-kernel results. The full run is in
> [performance/2026-09-26-host-compress.md](performance/2026-09-26-host-compress.md):
> the zfs and zram layers ran at load average 0.3 to 1.6, the kernel, Parquet, file-access
> and scan layers at 2.2 to 7.1 (the tail is the pressure run itself). An earlier run on
> the same machine at load average 20 to 40 is not used for any speed quoted here.

| Layer | Measured here (host) | Pending on a Runink River machine |
|---|---|---|
| ZFS | codec ratio and single-core speed per record, OpenZFS allocation model | on-pool `logicalused/used`, write/read MB/s and CPU with encryption (`zfs-pool`) |
| zram | codec on 4 KiB pages of real DuckDB and Go process memory; one pressure run on the host's zstd zram | lz4 vs lzo-rle vs zstd under the same cgroup limit, in the kernel (`zram-pressure`) |
| Kernel, initramfs, modules | size and user-space decompression time from the built `linux-runink` 7.2.7 and `runink-zfs` 2.4.4 packages | in-kernel decompression time at boot |
| Parquet | DuckDB 1.5.5 writer codecs, file size, write and cold/warm scan | the same on ZFS |
| File access, THP | on btrfs + dm-crypt | on ZFS (ARC instead of the page cache) |

## ZFS: codec per dataset, and the double-compression question

Allocated ratio = file bytes / bytes ZFS allocates (4 KiB sectors, `ashift=12`), after
OpenZFS's rules: a record is kept compressed only if that saves 1/8 and a sector; the tail
record of a multi-record file is a full zero-padded record; a file smaller than the
recordsize is one block of its own length; zstd levels >= 3 on records >= 128 KiB try LZ4
and then zstd-1 first and store the record raw when both fail (early abort). Throughput is
per core, with early abort included.

| Shape | rs | off | lz4 | zstd-1 | zstd-3 | zstd-9 | zstd-1 compress / decompress MB/s/core | records stored raw (zstd-1) |
|---|---|---:|---:|---:|---:|---:|---:|---:|
| Parquet, zstd pages | 1M | 1.000 | 1.000 | 1.000 | 1.000 | 1.000 | 1996 / no decompression | 100% |
| Parquet, zstd pages | 128K | 1.000 | 1.005 | 1.004 | 1.003 | 1.004 | 2138 / >100000 | 97% |
| Parquet, snappy pages | 1M | 1.000 | 1.000 | 1.100 | 1.103 | 1.102 | 860 / 3109 | 56% |
| Parquet, snappy pages | 128K | 1.000 | 1.023 | 1.091 | 1.093 | 1.104 | 900 / 3773 | 65% |
| Delta Lake `_delta_log` (2000 commits, 1.6 KiB mean) | any | 0.401 | 0.401 | 0.401 | 0.401 | 0.401 | 430 / none | 100% |
| Delta Lake checkpoint (snappy Parquet) | 1M | 0.999 | 0.999 | 1.269 | 1.335 | 1.359 | 561 / 1425 | 0% |
| NDJSON | 1M | 1.000 | 3.877 | 6.919 | 7.111 | 8.258 | 546 / 1562 | 0% |
| CSV | 1M | 1.000 | 1.967 | 3.053 | 3.238 | 3.655 | 290 / 1083 | 0% |
| Avro (uncompressed blocks) | 1M | 1.000 | 1.866 | 2.123 | 2.486 | 2.782 | 311 / 1055 | 0% |
| Model weights (GGUF-shaped, synthetic) | 1M | 1.000 | 1.000 | 1.077 | 1.077 | 1.077 | 965 / 3768 | 67% |
| JPEG images | 128K | 0.915 | 0.994 | 0.997 | 0.996 | 0.996 | 955 / 46725 | 67% |
| OS files (`/usr/bin`, `/usr/lib`) | 128K | 0.948 | 1.784 | 2.228 | 2.334 | 2.481 | 258 / 650 | 35% |
| Source tree (Go toolchain) | 128K | 0.776 | 1.518 | 1.811 | 1.839 | 1.893 | 285 / 811 | 65% |
| Container layer blob (tar.gz) | 1M | 0.983 | 1.000 | 1.000 | 1.000 | 1.000 | 2697 / >100000 | 97% |

LZ4 costs 6.5 to 10.7 GB/s per core on incompressible data; zstd-3 with early abort 1.6
to 2.1 GB/s per core.

**Double compression, answered with the numbers above.** The expectation was "lz4 (early
abort) or off for already-compressed Parquet, zstd for the logs and JSON". Measured:

- **Parquet with zstd pages: confirmed.** No ZFS codec gains anything (1.000 to 1.005);
  96 to 100% of records are stored raw. The cheapest setting is `lz4`, but the inherited
  `zstd` family costs little more because of early abort (zstd-1: 2.0 GB/s/core). `off` is
  never better: it saves no space and loses the zero-padded tail records, which cost
  8.5% on the JPEG shape at 128K and 1.7% on the layer blobs at 1M.
- **Parquet with snappy pages: refuted.** zstd-1 still takes 9 to 10% off (1.091 at 128K,
  1.100 at 1M) where lz4 takes 0 to 2%. Snappy leaves redundancy that zstd finds.
- **The Delta Lake JSON log: refuted, compression cannot help at all.** Each commit is a
  small file, one block rounded to a 4 KiB sector, so 2000 commits (3.1 MiB) allocate
  7.8 MiB with any codec (0.401). The log's cost is the sector size, not the codec; it is
  small next to the data files and needs no dataset of its own.
- **Bulk JSON and CSV (logs, exports, landing zones): confirmed.** zstd-1 stores NDJSON at
  6.9x and CSV at 3.1x against lz4's 3.9x and 2.0x.
- **Checkpoints:** zstd-1 1.27 vs lz4 1.00 at 1M.

**Per dataset** (declared mixes in `recommend.go`; rule: the highest allocated ratio among
settings that compress and decompress at >= 250 MB/s per core, i.e. 2 GB/s on 8 cores,
a current NVMe's sequential speed; within 1% the faster wins):

| Dataset | Image | recordsize | off | lz4 | zstd-1 | zstd-3 (today's pool default) | Pick | Why |
|---|---|---|---:|---:|---:|---:|---|---|
| `ROOT/<be>` (/) | both | 128K | 0.948 | 1.784 | 2.228 @ 258 MB/s | 2.334 @ 155 MB/s | **zstd-1** | zstd-3 is 4.5% smaller but compresses at 155 MB/s/core, under the floor |
| `home` | workstation | 128K | 0.889 | 1.343 | 1.508 @ 449 | 1.515 @ 297 | **zstd-1** | within 0.5% of zstd-3, 51% faster |
| a data-lake directory | workstation | 1M | 0.984 | 1.152 | 1.215 @ 1054 | 1.218 @ 601 | **zstd-1** | Parquet-dominated: within 0.3% of zstd-3 |
| `containers` (/var/lib/k0s) | server | 128K | 0.973 | 1.282 | 1.380 @ 476 | 1.400 @ 288 | **zstd-3** | unpacked snapshots reward zstd-3 by 1.4% and it clears the floor |
| `state` (/var/lib/core) | server | 1M | 0.992 | 1.080 | 1.129 @ 1277 | 1.130 @ 815 | **zstd-1** | registry blobs and weights barely compress; 128K would give 1.128 |
| model files | both | 1M | 1.000 | 1.000 | 1.077 @ 965 | 1.077 @ 632 | **zstd-1** | the bf16 part compresses 1.08x; lz4 finds nothing |

Recordsize is chosen from the access pattern, not by this rule: 1M for datasets of large
files read sequentially (1M gives 0 to 9% more ratio than 128K on every shape above except
the small-file ones), 128K where files are small or read randomly.

## zram

4 KiB pages of real process memory (DuckDB holding TPC-H SF1 lineitem, a sorted copy and a
group-by; a Go process holding parsed rows and a hash index), 16384 pages each. Same-filled
pages cost nothing; pages that compress to >= 3264 bytes are stored whole.

| Memory | lz4 | zstd-1 | zstd-3 (zram's zstd default) | lz4 / zstd-3 decompress MB/s/core |
|---|---:|---:|---:|---:|
| DuckDB (18% same-filled) | 2.153 | 3.469 | 3.487 | 2799 / 707 (1.5 / 5.8 us per page) |
| Go (2% same-filled) | 2.385 | 4.368 | 4.347 | 2382 / 764 (1.7 / 5.4 us per page) |

zstd holds 1.6x (DuckDB) to 1.8x (Go) more data in the same RAM than lz4. A zstd page
fault is ~6 us, still an order of magnitude under the ~100 us NVMe read it replaces.
zstd-1 buys nothing over zstd-3 here (same ratio, no faster to decompress).
**lzo-rle is pending:** there is no user-space LZO-RLE tool, so only the in-kernel
`zram-pressure` run measures it. On the host, one pressure run (DuckDB sort and self-join,
peak 3.5 GiB, cgroup limit half of that, the host's own zstd zram) took 6.29 s against
1.42 s unconstrained (4.4x), with 651k pages swapped out and 201k in. That is one sample
on a shared device and is not a codec comparison. The host's live zram holds 9.1 GiB at
1.80x.

## Kernel image, initramfs, modules

From the built `linux-runink` 7.2.7 package; user-space decompression (zstd 1.5.7, xz 5.8.3,
lz4 1.10.0, gzip 1.14) as the proxy for the kernel's. The rule is the lowest boot cost,
decompress time + size / read rate, at a declared 100 MB/s firmware read of the ESP (not
measured: it is firmware-specific), and the report states over which read rates the pick
holds.

| Artifact | zstd-3 | zstd-19 | zstd-22 (image: today) | xz-6 | lz4-9 | Pick |
|---|---|---|---|---|---|---|
| vmlinux (71.4 MiB) | 22.4 MiB, 65 ms | 17.1 MiB, 89 ms | 17.0 MiB, 90 ms | 15.3 MiB, 624 ms | 26.7 MiB, 28 ms | zstd-19/22 (tie); holds from 59 to 164 MB/s |
| initramfs, modules kept `.ko.zst` inside (54.2 MiB) | 42.7 MiB, 33 ms (mkinitcpio today) | 41.2 MiB, 41 ms | - | 40.7 MiB, 604 ms | 43.8 MiB, 35 ms | zstd-19, by 8 ms of boot cost |
| initramfs, modules decompressed inside (177 MiB) | 44.0 MiB, 171 ms | 33.9 MiB, 199 ms | - | 29.6 MiB, 1250 ms | 65.8 MiB, 65 ms | zstd-19 |
| 400 modules, each on its own (40.2 MiB) | 10.2 MiB, 44 ms | 8.4 MiB, 46 ms | as built: 8.7 MiB | 7.5 MiB, 255 ms | - | zstd-19 |

Keeping modules compressed inside the initramfs (mkinitcpio's default) beats decompressing
them first: 42.7 MiB + 33 ms against 33.9 MiB + 199 ms, i.e. 481 ms against 554 ms of boot
cost at 100 MB/s. The kernel image is already at the pick.

## Parquet writer codecs (DuckDB 1.5.5, TPC-H SF1 lineitem, 374 MiB uncompressed)

| Codec | Size | Ratio | Write | Cold full scan | Warm full scan |
|---|---:|---:|---:|---:|---:|
| uncompressed | 373.9 MiB | 1.00 | 0.85 s | 153 ms | 89 ms |
| snappy | 197.5 MiB | 1.89 | 1.76 s | 151 ms | 110 ms |
| lz4_raw | 200.2 MiB | 1.87 | 0.98 s | 142 ms | 100 ms |
| zstd-1 | 145.9 MiB | 2.56 | 1.36 s | 174 ms | 140 ms |
| zstd-3 | 142.4 MiB | 2.63 | 1.91 s | 166 ms | 132 ms |
| zstd-9 | 135.4 MiB | 2.76 | 2.45 s | 160 ms | 126 ms |
| gzip | 142.2 MiB | 2.63 | 3.98 s | 261 ms | 200 ms |

The declared rule (smallest file whose cold scan is within 10% of the fastest) picks
snappy; zstd-3 is 28% smaller at +17% scan time on this NVMe. Together with the ZFS
table: snappy Parquet on a zstd-1 dataset allocates ~180 MiB, zstd-3 Parquet ~142 MiB on
any dataset. For storage- or network-bound tables write zstd; for scan-CPU-bound ones on
fast local disks snappy or lz4_raw. This is workload guidance, not an image default.

## What the OS can do beyond codecs (host: btrfs on dm-crypt, cold page cache)

| Read pattern | 1 thread, page cache | fadvise hint | O_DIRECT, 1 | O_DIRECT, 8 threads | io_uring QD8 | io_uring QD32 |
|---|---:|---:|---:|---:|---:|---:|
| full scan, 1 MiB reads (2 GiB file) | 2162 MB/s | SEQUENTIAL: 1988 | 2045 | 5004 | 4975 | 5014 |
| projection, 256 KiB of every 1 MiB | 706 MB/s | RANDOM: 712 | 1005 | 4577 | 4583 | 4825 |

Warm (page cache) full scan: 7519 MB/s. Queue depth is what matters: 2.3x on the full scan
and 6.5x on the projection; io_uring at QD32 equals eight threads doing `pread`; readahead
hints change nothing measurable. These are properties of the engine (how many reads it
keeps in flight), not an OS default to change. On ZFS the ARC replaces the page cache
(pending).

**Transparent huge pages** (host THP `always`, defrag `defer+madvise`): a TPC-H Q6 scan
over 1.8 GiB of mmap'd columns is 5% faster with `MADV_HUGEPAGE` on one thread (140 vs 133
Mrows/s) and 4% on 16 (1601 vs 1541), but building the columns takes 2.4x longer (0.505 s
vs 0.211 s). DuckDB's Q1 over Parquet runs the same 42 ms with THP on or off for the
process. This supports the image's `madvise` default: engines that gain opt in.

## Proposed image defaults

For the maintainers of the lead-owned files; each change is tied to a number above. None
of them has been booted: they need the Tier 2 VM test (and the on-box `zfs-pool` run to
replace the model with on-pool numbers) before they land.

1. **`installer/lib/10-disk-zfs.sh`** and its byte-identical copy
   `iso-profiles/river/root-overlay/usr/local/lib/runink-install/10-disk-zfs.sh`: pool
   default zstd-3 -> zstd-1 (BE: 2.228 vs 2.334, compress 258 vs 155 MB/s/core; `home`
   1.508 vs 1.515 at 449 vs 297), `state` at 1M, `containers` keeps zstd-3. Every dataset
   still inherits the pool's encryption.

   ```diff
   -		-O compression=zstd \
   +		-O compression=zstd-1 \
   ```
   ```diff
   -[ "${RUNINK_PLAN_PROFILE:-server}" = workstation ] || siblings="state:/var/lib/core containers:/var/lib/k0s"
   +[ "${RUNINK_PLAN_PROFILE:-server}" = workstation ] || siblings="state:/var/lib/core:recordsize=1M containers:/var/lib/k0s:compression=zstd-3"
    for spec in $siblings; do
   -	ds="${spec%%:*}"
   -	mp="${spec#*:}"
   +	ds="${spec%%:*}"; rest="${spec#*:}"
   +	mp="${rest%%:*}"; prop="${rest#*:}"
    	if ! zfs list "$POOL/$ds" >/dev/null 2>&1; then
   -		zfs create -o mountpoint="$mp" -o com.sun:auto-snapshot=false "$POOL/$ds"
   +		zfs create -o mountpoint="$mp" -o "$prop" -o com.sun:auto-snapshot=false "$POOL/$ds"
    	fi
    done
   ```

   Do not add `compression=off` anywhere: off never won (it loses the padded tail records,
   up to 8.5%). A data-lake or model directory the user creates later wants
   `zfs create -o recordsize=1M <pool>/home/<user>/data` (1M: +0 to 9% ratio on large
   files); that belongs in the user documentation, not the installer.

2. **zram / zswap** (`iso-profiles/river/root-overlay/usr/local/bin/runink-zram.sh`,
   `etc/s6/rc.local`): **no change.** zstd is the pick on both memory samples (3.49x and
   4.35x vs lz4's 2.15x and 2.39x, at ~6 us per page fault); zstd-1 via
   `algorithm_params` gains nothing measured; zswap stays off (compressing in front of zram
   compresses twice). The size is now the install plan's (`/etc/runink/zram.conf`,
   [INSTALLER-HARDWARE.md](INSTALLER-HARDWARE.md)); `ram` stays the default without a plan.
   lzo-rle is pending the in-kernel run.

3. **sysctl** (`etc/sysctl.d/99-runink-workstation.conf`): **no change.** Nothing here
   measured `vm.swappiness`, `vm.page-cluster` or `vm.watermark_boost_factor`; the
   pressure run on a Runink River machine is where they should be tested
   (`riverbench compress -layers zram-pressure` with and without them).

4. **mkinitcpio** (`etc/mkinitcpio.conf.d/zfs.conf`): optional, small.

   ```diff
    COMPRESSION="zstd"
   +COMPRESSION_OPTIONS=(-19)
   ```

   41.2 MiB vs 42.7 MiB (-3.5%) for +7 ms decompression (boot cost 473 vs 481 ms at
   100 MB/s), but 14.5 s instead of 0.14 s per image rebuild. Keep `MODULES_DECOMPRESS`
   at its default (`no`): decompressing modules first costs 73 ms more boot at 100 MB/s.
   Never go above 19 for the initramfs: higher levels need a 128 MiB window in the
   kernel's streaming decompressor (`scripts/Makefile.lib`).

5. **Kernel `CONFIG_*`** (`build/pkgbuilds/runink-kernel/config.*`): **no change.**
   `KERNEL_ZSTD` (zstd -22) ties zstd-19 for the pick (17.0 MiB, 90 ms);
   `MODULE_COMPRESS_ZSTD` ships 8.7 MiB for the 400-module sample, between zstd-9 (9.3)
   and zstd-19 (8.4), 46 ms either way; `ZRAM_DEF_COMP_ZSTD` and
   `ZSWAP_COMPRESSOR_DEFAULT_ZSTD` match the zram result. XZ would save 10 to 20% more
   space at 5 to 7x the decompression time.

6. **THP**: **no change** (`madvise`); see above.

## Reproducing and exporting

```bash
bench/analytics/run.sh compress --out results/compress-$(uname -r)       # rootless layers
sudo riverbench compress -layers zfs-pool,zram-pressure -zfs-parent <pool>/bench \
     -duckdb "$DUCKDB" -out results/onbox                                 # Runink River, root
bench/analytics/run.sh compare base/compress.json new/compress.json      # JSON diff
```

Results are OpenTelemetry metrics (OTLP/HTTP protobuf or JSON, or a Prometheus text file),
off unless a destination is passed; names, units and attributes are in the
[metric catalogue](../bench/analytics/README.md#metric-catalogue).
