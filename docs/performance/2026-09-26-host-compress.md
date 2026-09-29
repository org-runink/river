<!--
SPDX-FileCopyrightText: 2026 The RIVER Authors
SPDX-License-Identifier: MIT
-->

# riverbench compress: 6.18.52-1-cachyos-lts

| | |
|---|---|
| Run | `71ecb4c11e520a9e` (commit `8ae7c2ea31eb`) |
| Host | this host is NOT a Runink River install; numbers that depend on the filesystem or kernel configuration must be re-run on one |
| OS | CachyOS, kernel `6.18.52-1-cachyos-lts` |
| CPU | AMD Ryzen 7 8840U w/ Radeon 780M Graphics, 16 threads |
| RAM | 25.2 GiB |
| Root / work filesystem | btrfs / btrfs |
| Load average start / end | 2.19 6.86 12.95 / 7.11 4.01 5.79 |
| duckdb | v1.5.5 (Variegata) d8cdaa33fd |
| gzip | gzip 1.14-modified |
| lz4 | lz4 v1.10.0 64-bit multithread, by Yann Collet |
| xz | xz (XZ Utils) 5.8.3 |
| zstd | Zstandard CLI (64-bit) v1.5.7, by Yann Collet |

## kernel

| Shape | Setting | Block | Method | Ratio | Alloc ratio | Compress MB/s/core | Decompress MB/s/core | Compress | Decompress | Orig | Stored | Notes |
|---|---|---|---|---:|---:|---:|---:|---:|---:|---:|---:|---|
| vmlinux 7.2.7-zen1-1-runink | zstd-1 | stream | stream | 2.905 | - | 471 | 1316 | 158.9 ms | 56.9 ms | 71.4 MiB | 24.6 MiB |  |
| vmlinux 7.2.7-zen1-1-runink | zstd-3 | stream | stream | 3.187 | - | 253 | 1145 | 296.3 ms | 65.4 ms | 71.4 MiB | 22.4 MiB |  |
| vmlinux 7.2.7-zen1-1-runink | zstd-9 | stream | stream | 3.419 | - | 66 | 1013 | 1.13 s | 73.9 ms | 71.4 MiB | 20.9 MiB |  |
| vmlinux 7.2.7-zen1-1-runink | zstd-19 | stream | stream | 4.186 | - | 3 | 841 | 27.38 s | 89.0 ms | 71.4 MiB | 17.1 MiB |  |
| vmlinux 7.2.7-zen1-1-runink | xz-6 | stream | stream | 4.674 | - | 4 | 120 | 19.39 s | 624.2 ms | 71.4 MiB | 15.3 MiB |  |
| vmlinux 7.2.7-zen1-1-runink | lz4-9 | stream | stream | 2.676 | - | 289 | 2724 | 259.0 ms | 27.5 ms | 71.4 MiB | 26.7 MiB |  |
| vmlinux 7.2.7-zen1-1-runink | gzip-9 | stream | stream | 3.556 | - | 11 | 286 | 6.85 s | 262.0 ms | 71.4 MiB | 20.1 MiB |  |
| vmlinux 7.2.7-zen1-1-runink | zstd-22 | stream | stream | 4.204 | - | 4 | 829 | 19.98 s | 90.3 ms | 71.4 MiB | 17.0 MiB |  |

## initramfs

| Shape | Setting | Block | Method | Ratio | Alloc ratio | Compress MB/s/core | Decompress MB/s/core | Compress | Decompress | Orig | Stored | Notes |
|---|---|---|---|---:|---:|---:|---:|---:|---:|---:|---:|---|
| modules-uncompressed | zstd-1 | stream | stream | 3.584 | - | 435 | 1172 | 427.2 ms | 158.5 ms | 177.1 MiB | 49.4 MiB |  |
| modules-uncompressed | zstd-3 | stream | stream | 4.026 | - | 299 | 1084 | 622.0 ms | 171.3 ms | 177.1 MiB | 44.0 MiB |  |
| modules-uncompressed | zstd-9 | stream | stream | 4.638 | - | 71 | 1157 | 2.63 s | 160.5 ms | 177.1 MiB | 38.2 MiB |  |
| modules-uncompressed | zstd-19 | stream | stream | 5.229 | - | 3 | 932 | 65.91 s | 199.3 ms | 177.1 MiB | 33.9 MiB |  |
| modules-uncompressed | xz-6 | stream | stream | 5.977 | - | 3 | 149 | 62.31 s | 1.25 s | 177.1 MiB | 29.6 MiB |  |
| modules-uncompressed | lz4-9 | stream | stream | 2.691 | - | 304 | 2875 | 610.1 ms | 64.6 ms | 177.1 MiB | 65.8 MiB |  |
| modules-uncompressed | gzip-9 | stream | stream | 3.407 | - | 4 | 331 | 43.64 s | 561.5 ms | 177.1 MiB | 52.0 MiB |  |
| modules-ko.zst | zstd-1 | stream | stream | 1.240 | - | 650 | 1914 | 87.4 ms | 29.7 ms | 54.2 MiB | 43.7 MiB |  |
| modules-ko.zst | zstd-3 | stream | stream | 1.269 | - | 413 | 1710 | 137.6 ms | 33.2 ms | 54.2 MiB | 42.7 MiB |  |
| modules-ko.zst | zstd-9 | stream | stream | 1.287 | - | 120 | 1702 | 474.4 ms | 33.4 ms | 54.2 MiB | 42.1 MiB |  |
| modules-ko.zst | zstd-19 | stream | stream | 1.315 | - | 4 | 1402 | 14.49 s | 40.5 ms | 54.2 MiB | 41.2 MiB |  |
| modules-ko.zst | xz-6 | stream | stream | 1.332 | - | 3 | 94 | 19.90 s | 604.2 ms | 54.2 MiB | 40.7 MiB |  |
| modules-ko.zst | lz4-9 | stream | stream | 1.236 | - | 263 | 1632 | 216.3 ms | 34.8 ms | 54.2 MiB | 43.8 MiB |  |
| modules-ko.zst | gzip-9 | stream | stream | 1.273 | - | 15 | 304 | 3.79 s | 186.6 ms | 54.2 MiB | 42.5 MiB |  |

## modules

| Shape | Setting | Block | Method | Ratio | Alloc ratio | Compress MB/s/core | Decompress MB/s/core | Compress | Decompress | Orig | Stored | Notes |
|---|---|---|---|---:|---:|---:|---:|---:|---:|---:|---:|---|
| 400 modules | as-built | stream | package | 4.625 | - | - | - | - | - | 40.2 MiB | 8.7 MiB | the .ko.zst files exactly as the linux-runink package ships them |
| 400 modules | zstd-3 | stream | per-file+stream | 3.939 | - | - | 957 | 144.8 ms | 44.1 ms | 40.2 MiB | 10.2 MiB | sizes: each module compressed alone; times: the sample as one stream |
| 400 modules | zstd-9 | stream | per-file+stream | 4.347 | - | - | 1054 | 619.9 ms | 40.0 ms | 40.2 MiB | 9.3 MiB | sizes: each module compressed alone; times: the sample as one stream |
| 400 modules | zstd-19 | stream | per-file+stream | 4.786 | - | - | 909 | 14.08 s | 46.4 ms | 40.2 MiB | 8.4 MiB | sizes: each module compressed alone; times: the sample as one stream |
| 400 modules | xz-6 | stream | per-file+stream | 5.387 | - | - | 165 | 14.38 s | 255.3 ms | 40.2 MiB | 7.5 MiB | sizes: each module compressed alone; times: the sample as one stream |
| 400 modules | gzip-9 | stream | per-file+stream | 3.866 | - | - | 337 | 16.56 s | 125.1 ms | 40.2 MiB | 10.4 MiB | sizes: each module compressed alone; times: the sample as one stream |

## parquet

| Shape | Setting | Block | Method | Ratio | Alloc ratio | Compress MB/s/core | Decompress MB/s/core | Compress | Decompress | Orig | Stored | Notes |
|---|---|---|---|---:|---:|---:|---:|---:|---:|---:|---:|---|
| tpch-lineitem | uncompressed | stream | duckdb | 1.000 | - | - | - | 852.0 ms | 153.0 ms | 373.9 MiB | 373.9 MiB | ratio vs the uncompressed Parquet file warm_read_s=0.089 |
| tpch-lineitem | snappy | stream | duckdb | 1.893 | - | - | - | 1.76 s | 151.0 ms | 373.9 MiB | 197.5 MiB | ratio vs the uncompressed Parquet file warm_read_s=0.11 |
| tpch-lineitem | lz4_raw | stream | duckdb | 1.868 | - | - | - | 976.0 ms | 142.0 ms | 373.9 MiB | 200.2 MiB | ratio vs the uncompressed Parquet file warm_read_s=0.1 |
| tpch-lineitem | zstd-1 | stream | duckdb | 2.562 | - | - | - | 1.36 s | 174.0 ms | 373.9 MiB | 145.9 MiB | ratio vs the uncompressed Parquet file warm_read_s=0.14 |
| tpch-lineitem | zstd-3 | stream | duckdb | 2.625 | - | - | - | 1.91 s | 166.0 ms | 373.9 MiB | 142.4 MiB | ratio vs the uncompressed Parquet file warm_read_s=0.132 |
| tpch-lineitem | zstd-9 | stream | duckdb | 2.761 | - | - | - | 2.45 s | 160.0 ms | 373.9 MiB | 135.4 MiB | ratio vs the uncompressed Parquet file warm_read_s=0.126 |
| tpch-lineitem | gzip | stream | duckdb | 2.629 | - | - | - | 3.98 s | 261.0 ms | 373.9 MiB | 142.2 MiB | ratio vs the uncompressed Parquet file warm_read_s=0.2 |

## zram-pressure

| Shape | Setting | Block | Method | Ratio | Alloc ratio | Compress MB/s/core | Decompress MB/s/core | Compress | Decompress | Orig | Stored | Notes |
|---|---|---|---|---:|---:|---:|---:|---:|---:|---:|---:|---|
| duckdb-sort-join | zstd-3 | 4K | in-kernel | - | - | - | - | 6.29 s | - | - | - | host zram device shared with the rest of the machine; codec as configured, not chosen baseline_s=1.419 limit_bytes=1.844e+09 peak_rss_bytes=3.689e+09 pswpin=2.009e+05 pswpout=6.505e+05 slowdown=4.428 |

## zram

| Shape | Setting | Block | Method | Ratio | Alloc ratio | Compress MB/s/core | Decompress MB/s/core | Compress | Decompress | Orig | Stored | Notes |
|---|---|---|---|---:|---:|---:|---:|---:|---:|---:|---:|---|
| duckdb-process | lz4 | 4K | model | 1.791 | 2.153 | 746 | 2799 | - | - | 64.0 MiB | 29.7 MiB | huge=17% same_filled=18% |
| duckdb-process | zstd-1 | 4K | model | 2.894 | 3.469 | 280 | 671 | - | - | 64.0 MiB | 18.5 MiB | huge=5% same_filled=18% |
| duckdb-process | zstd-3 | 4K | model | 2.916 | 3.487 | 238 | 707 | - | - | 64.0 MiB | 18.4 MiB | huge=5% same_filled=18% |
| go-process | lz4 | 4K | model | 2.356 | 2.385 | 736 | 2382 | - | - | 64.0 MiB | 26.8 MiB | huge=0% same_filled=2% |
| go-process | zstd-1 | 4K | model | 4.334 | 4.368 | 290 | 713 | - | - | 64.0 MiB | 14.7 MiB | same_filled=2% |
| go-process | zstd-3 | 4K | model | 4.311 | 4.347 | 256 | 764 | - | - | 64.0 MiB | 14.7 MiB | same_filled=2% |

## zram-live

| Shape | Setting | Block | Method | Ratio | Alloc ratio | Compress MB/s/core | Decompress MB/s/core | Compress | Decompress | Orig | Stored | Notes |
|---|---|---|---|---:|---:|---:|---:|---:|---:|---:|---:|---|
| this-host-swap | zstd-3 | 4K | in-kernel | 1.819 | 1.804 | - | - | - | - | 9.12 GiB | 5.05 GiB | zram0: whatever this machine happened to swap; not a controlled workload |

## zfs

| Shape | Setting | Block | Method | Ratio | Alloc ratio | Compress MB/s/core | Decompress MB/s/core | Compress | Decompress | Orig | Stored | Notes |
|---|---|---|---|---:|---:|---:|---:|---:|---:|---:|---:|---|
| parquet-snappy | off | 128K | model | 1.000 | 1.000 | - | - | - | - | 64.0 MiB | 64.0 MiB |  |
| parquet-snappy | lz4 | 128K | model | 1.025 | 1.023 | 3865 | >100000 | - | - | 64.0 MiB | 62.6 MiB | stored_raw=94% |
| parquet-snappy | zstd-fast-1 | 128K | model | 1.029 | 1.027 | 2955 | >100000 | - | - | 64.0 MiB | 62.3 MiB | stored_raw=94% |
| parquet-snappy | zstd-1 | 128K | model | 1.146 | 1.091 | 900 | 3773 | - | - | 64.0 MiB | 58.6 MiB | stored_raw=65% |
| parquet-snappy | zstd-3 | 128K | model | 1.148 | 1.093 | 583 | 3850 | - | - | 64.0 MiB | 58.6 MiB | early_abort=65% stored_raw=65% |
| parquet-snappy | zstd-6 | 128K | model | 1.160 | 1.103 | 251 | 2979 | - | - | 64.0 MiB | 58.0 MiB | early_abort=65% stored_raw=65% |
| parquet-snappy | zstd-9 | 128K | model | 1.162 | 1.104 | 216 | 3032 | - | - | 64.0 MiB | 57.9 MiB | early_abort=65% stored_raw=65% |
| parquet-snappy | zstd-19 | 128K | model | 1.178 | 1.102 | 25 | 2327 | - | - | 64.0 MiB | 58.1 MiB | early_abort=65% stored_raw=65% |
| parquet-snappy | off | 1M | model | 1.000 | 1.000 | - | - | - | - | 64.0 MiB | 64.0 MiB |  |
| parquet-snappy | lz4 | 1M | model | 1.025 | 1.000 | 4061 | - | - | - | 64.0 MiB | 64.0 MiB | every record stored raw: reads decompress nothing stored_raw=100% |
| parquet-snappy | zstd-fast-1 | 1M | model | 1.030 | 1.000 | 2692 | - | - | - | 64.0 MiB | 64.0 MiB | every record stored raw: reads decompress nothing stored_raw=100% |
| parquet-snappy | zstd-1 | 1M | model | 1.150 | 1.100 | 860 | 3109 | - | - | 64.0 MiB | 58.2 MiB | stored_raw=56% |
| parquet-snappy | zstd-3 | 1M | model | 1.157 | 1.103 | 424 | 3066 | - | - | 64.0 MiB | 58.0 MiB | early_abort=56% stored_raw=56% |
| parquet-snappy | zstd-6 | 1M | model | 1.159 | 1.089 | 287 | 4130 | - | - | 64.0 MiB | 58.8 MiB | early_abort=56% stored_raw=67% |
| parquet-snappy | zstd-9 | 1M | model | 1.161 | 1.102 | 267 | 3366 | - | - | 64.0 MiB | 58.1 MiB | early_abort=56% stored_raw=59% |
| parquet-snappy | zstd-19 | 1M | model | 1.194 | 1.126 | 27 | 1755 | - | - | 64.0 MiB | 56.9 MiB | early_abort=56% stored_raw=56% |
| parquet-zstd | off | 128K | model | 1.000 | 1.000 | - | - | - | - | 64.0 MiB | 64.0 MiB |  |
| parquet-zstd | lz4 | 128K | model | 1.006 | 1.005 | 6771 | >100000 | - | - | 64.0 MiB | 63.7 MiB | stored_raw=96% |
| parquet-zstd | zstd-fast-1 | 128K | model | 1.006 | 1.004 | 6100 | >100000 | - | - | 64.0 MiB | 63.8 MiB | stored_raw=97% |
| parquet-zstd | zstd-1 | 128K | model | 1.006 | 1.004 | 2138 | >100000 | - | - | 64.0 MiB | 63.8 MiB | stored_raw=97% |
| parquet-zstd | zstd-3 | 128K | model | 1.005 | 1.003 | 1622 | >100000 | - | - | 64.0 MiB | 63.8 MiB | early_abort=96% stored_raw=98% |
| parquet-zstd | zstd-6 | 128K | model | 1.006 | 1.004 | 1603 | >100000 | - | - | 64.0 MiB | 63.8 MiB | early_abort=96% stored_raw=97% |
| parquet-zstd | zstd-9 | 128K | model | 1.006 | 1.004 | 1600 | >100000 | - | - | 64.0 MiB | 63.8 MiB | early_abort=96% stored_raw=97% |
| parquet-zstd | zstd-19 | 128K | model | 1.009 | 1.006 | 308 | >100000 | - | - | 64.0 MiB | 63.6 MiB | early_abort=96% stored_raw=96% |
| parquet-zstd | off | 1M | model | 1.000 | 1.000 | - | - | - | - | 64.0 MiB | 64.0 MiB |  |
| parquet-zstd | lz4 | 1M | model | 1.005 | 1.000 | 8638 | - | - | - | 64.0 MiB | 64.0 MiB | every record stored raw: reads decompress nothing stored_raw=100% |
| parquet-zstd | zstd-fast-1 | 1M | model | 1.006 | 1.000 | 4801 | - | - | - | 64.0 MiB | 64.0 MiB | every record stored raw: reads decompress nothing stored_raw=100% |
| parquet-zstd | zstd-1 | 1M | model | 1.006 | 1.000 | 1996 | - | - | - | 64.0 MiB | 64.0 MiB | every record stored raw: reads decompress nothing stored_raw=100% |
| parquet-zstd | zstd-3 | 1M | model | 1.005 | 1.000 | 1622 | - | - | - | 64.0 MiB | 64.0 MiB | every record stored raw: reads decompress nothing early_abort=100% stored_raw=100% |
| parquet-zstd | zstd-6 | 1M | model | 1.006 | 1.000 | 1622 | - | - | - | 64.0 MiB | 64.0 MiB | every record stored raw: reads decompress nothing early_abort=100% stored_raw=100% |
| parquet-zstd | zstd-9 | 1M | model | 1.006 | 1.000 | 1622 | - | - | - | 64.0 MiB | 64.0 MiB | every record stored raw: reads decompress nothing early_abort=100% stored_raw=100% |
| parquet-zstd | zstd-19 | 1M | model | 1.009 | 1.000 | 1622 | - | - | - | 64.0 MiB | 64.0 MiB | every record stored raw: reads decompress nothing early_abort=100% stored_raw=100% |
| csv | off | 128K | model | 1.000 | 1.000 | - | - | - | - | 64.0 MiB | 64.0 MiB |  |
| csv | lz4 | 128K | model | 1.953 | 1.882 | 440 | 3227 | - | - | 64.0 MiB | 34.0 MiB |  |
| csv | zstd-fast-1 | 128K | model | 2.364 | 2.286 | 294 | 969 | - | - | 64.0 MiB | 28.0 MiB |  |
| csv | zstd-1 | 128K | model | 3.005 | 2.909 | 290 | 976 | - | - | 64.0 MiB | 22.0 MiB |  |
| csv | zstd-3 | 128K | model | 3.155 | 2.909 | 151 | 937 | - | - | 64.0 MiB | 22.0 MiB |  |
| csv | zstd-6 | 128K | model | 3.314 | 3.200 | 62 | 925 | - | - | 64.0 MiB | 20.0 MiB |  |
| csv | zstd-9 | 128K | model | 3.453 | 3.200 | 37 | 1056 | - | - | 64.0 MiB | 20.0 MiB |  |
| csv | zstd-19 | 128K | model | 3.786 | 3.556 | 3 | 978 | - | - | 64.0 MiB | 18.0 MiB |  |
| csv | off | 1M | model | 1.000 | 1.000 | - | - | - | - | 64.0 MiB | 64.0 MiB |  |
| csv | lz4 | 1M | model | 1.975 | 1.967 | 430 | 3241 | - | - | 64.0 MiB | 32.5 MiB |  |
| csv | zstd-fast-1 | 1M | model | 2.417 | 2.410 | 299 | 1094 | - | - | 64.0 MiB | 26.6 MiB |  |
| csv | zstd-1 | 1M | model | 3.078 | 3.053 | 290 | 1083 | - | - | 64.0 MiB | 21.0 MiB |  |
| csv | zstd-3 | 1M | model | 3.256 | 3.238 | 140 | 939 | - | - | 64.0 MiB | 19.8 MiB |  |
| csv | zstd-6 | 1M | model | 3.523 | 3.501 | 64 | 1062 | - | - | 64.0 MiB | 18.3 MiB |  |
| csv | zstd-9 | 1M | model | 3.681 | 3.655 | 46 | 1165 | - | - | 64.0 MiB | 17.5 MiB |  |
| csv | zstd-19 | 1M | model | 4.174 | 4.128 | 4 | 1212 | - | - | 64.0 MiB | 15.5 MiB |  |
| ndjson | off | 128K | model | 1.000 | 1.000 | - | - | - | - | 64.0 MiB | 64.0 MiB |  |
| ndjson | lz4 | 128K | model | 3.810 | 3.556 | 768 | 3318 | - | - | 64.0 MiB | 18.0 MiB |  |
| ndjson | zstd-fast-1 | 128K | model | 5.490 | 5.333 | 585 | 1531 | - | - | 64.0 MiB | 12.0 MiB |  |
| ndjson | zstd-1 | 128K | model | 6.643 | 6.400 | 564 | 1449 | - | - | 64.0 MiB | 10.0 MiB |  |
| ndjson | zstd-3 | 128K | model | 6.771 | 6.400 | 299 | 1533 | - | - | 64.0 MiB | 10.0 MiB |  |
| ndjson | zstd-6 | 128K | model | 7.314 | 6.400 | 110 | 1532 | - | - | 64.0 MiB | 10.0 MiB |  |
| ndjson | zstd-9 | 128K | model | 7.834 | 6.400 | 65 | 1844 | - | - | 64.0 MiB | 10.0 MiB |  |
| ndjson | zstd-19 | 128K | model | 8.717 | 8.000 | 2 | 1898 | - | - | 64.0 MiB | 8.0 MiB |  |
| ndjson | off | 1M | model | 1.000 | 1.000 | - | - | - | - | 64.0 MiB | 64.0 MiB |  |
| ndjson | lz4 | 1M | model | 3.895 | 3.877 | 739 | 3318 | - | - | 64.0 MiB | 16.5 MiB |  |
| ndjson | zstd-fast-1 | 1M | model | 5.879 | 5.818 | 563 | 1554 | - | - | 64.0 MiB | 11.0 MiB |  |
| ndjson | zstd-1 | 1M | model | 7.077 | 6.919 | 546 | 1562 | - | - | 64.0 MiB | 9.2 MiB |  |
| ndjson | zstd-3 | 1M | model | 7.178 | 7.111 | 288 | 1592 | - | - | 64.0 MiB | 9.0 MiB |  |
| ndjson | zstd-6 | 1M | model | 7.964 | 7.758 | 108 | 1784 | - | - | 64.0 MiB | 8.2 MiB |  |
| ndjson | zstd-9 | 1M | model | 8.456 | 8.258 | 78 | 2040 | - | - | 64.0 MiB | 7.8 MiB |  |
| ndjson | zstd-19 | 1M | model | 10.070 | 9.846 | 3 | 2277 | - | - | 64.0 MiB | 6.5 MiB |  |
| avro | off | 128K | model | 1.000 | 1.000 | - | - | - | - | 50.0 MiB | 50.0 MiB |  |
| avro | lz4 | 128K | model | 1.857 | 1.778 | 497 | 3964 | - | - | 50.0 MiB | 28.1 MiB |  |
| avro | zstd-fast-1 | 128K | model | 2.117 | 2.002 | 328 | 1188 | - | - | 50.0 MiB | 25.0 MiB |  |
| avro | zstd-1 | 128K | model | 2.165 | 2.133 | 311 | 1000 | - | - | 50.0 MiB | 23.4 MiB |  |
| avro | zstd-3 | 128K | model | 2.377 | 2.286 | 162 | 1005 | - | - | 50.0 MiB | 21.9 MiB |  |
| avro | zstd-6 | 128K | model | 2.580 | 2.462 | 67 | 1035 | - | - | 50.0 MiB | 20.3 MiB |  |
| avro | zstd-9 | 128K | model | 2.670 | 2.583 | 42 | 1114 | - | - | 50.0 MiB | 19.4 MiB |  |
| avro | zstd-19 | 128K | model | 2.882 | 2.667 | 3 | 997 | - | - | 50.0 MiB | 18.7 MiB |  |
| avro | off | 1M | model | 1.000 | 1.000 | - | - | - | - | 50.0 MiB | 50.0 MiB |  |
| avro | lz4 | 1M | model | 1.871 | 1.866 | 490 | 3972 | - | - | 50.0 MiB | 26.8 MiB |  |
| avro | zstd-fast-1 | 1M | model | 2.078 | 2.067 | 332 | 1309 | - | - | 50.0 MiB | 24.2 MiB |  |
| avro | zstd-1 | 1M | model | 2.134 | 2.123 | 311 | 1055 | - | - | 50.0 MiB | 23.5 MiB |  |
| avro | zstd-3 | 1M | model | 2.500 | 2.486 | 147 | 993 | - | - | 50.0 MiB | 20.1 MiB |  |
| avro | zstd-6 | 1M | model | 2.727 | 2.718 | 68 | 1081 | - | - | 50.0 MiB | 18.4 MiB |  |
| avro | zstd-9 | 1M | model | 2.792 | 2.782 | 50 | 1127 | - | - | 50.0 MiB | 18.0 MiB |  |
| avro | zstd-19 | 1M | model | 3.176 | 3.160 | 4 | 1166 | - | - | 50.0 MiB | 15.8 MiB |  |
| delta-log | off | 128K | model | 1.000 | 0.401 | - | - | - | - | 3.1 MiB | 7.8 MiB |  |
| delta-log | lz4 | 128K | model | 1.971 | 0.401 | 1155 | - | - | - | 3.1 MiB | 7.8 MiB | every record stored raw: reads decompress nothing stored_raw=100% |
| delta-log | zstd-fast-1 | 128K | model | 2.078 | 0.401 | 620 | - | - | - | 3.1 MiB | 7.8 MiB | every record stored raw: reads decompress nothing stored_raw=100% |
| delta-log | zstd-1 | 128K | model | 2.664 | 0.401 | 430 | - | - | - | 3.1 MiB | 7.8 MiB | every record stored raw: reads decompress nothing stored_raw=100% |
| delta-log | zstd-3 | 128K | model | 2.694 | 0.401 | 345 | - | - | - | 3.1 MiB | 7.8 MiB | every record stored raw: reads decompress nothing stored_raw=100% |
| delta-log | zstd-6 | 128K | model | 2.762 | 0.401 | 137 | - | - | - | 3.1 MiB | 7.8 MiB | every record stored raw: reads decompress nothing stored_raw=100% |
| delta-log | zstd-9 | 128K | model | 2.762 | 0.401 | 80 | - | - | - | 3.1 MiB | 7.8 MiB | every record stored raw: reads decompress nothing stored_raw=100% |
| delta-log | zstd-19 | 128K | model | 2.756 | 0.401 | 4 | - | - | - | 3.1 MiB | 7.8 MiB | every record stored raw: reads decompress nothing stored_raw=100% |
| delta-log | off | 1M | model | 1.000 | 0.401 | - | - | - | - | 3.1 MiB | 7.8 MiB |  |
| delta-log | lz4 | 1M | model | 1.971 | 0.401 | 1160 | - | - | - | 3.1 MiB | 7.8 MiB | every record stored raw: reads decompress nothing stored_raw=100% |
| delta-log | zstd-fast-1 | 1M | model | 2.078 | 0.401 | 621 | - | - | - | 3.1 MiB | 7.8 MiB | every record stored raw: reads decompress nothing stored_raw=100% |
| delta-log | zstd-1 | 1M | model | 2.664 | 0.401 | 426 | - | - | - | 3.1 MiB | 7.8 MiB | every record stored raw: reads decompress nothing stored_raw=100% |
| delta-log | zstd-3 | 1M | model | 2.694 | 0.401 | 346 | - | - | - | 3.1 MiB | 7.8 MiB | every record stored raw: reads decompress nothing stored_raw=100% |
| delta-log | zstd-6 | 1M | model | 2.762 | 0.401 | 136 | - | - | - | 3.1 MiB | 7.8 MiB | every record stored raw: reads decompress nothing stored_raw=100% |
| delta-log | zstd-9 | 1M | model | 2.762 | 0.401 | 80 | - | - | - | 3.1 MiB | 7.8 MiB | every record stored raw: reads decompress nothing stored_raw=100% |
| delta-log | zstd-19 | 1M | model | 2.756 | 0.401 | 4 | - | - | - | 3.1 MiB | 7.8 MiB | every record stored raw: reads decompress nothing stored_raw=100% |
| delta-checkpoint | off | 128K | model | 1.000 | 0.968 | - | - | - | - | 619.5 KiB | 640.0 KiB |  |
| delta-checkpoint | lz4 | 128K | model | 1.107 | 1.046 | 1451 | 45184 | - | - | 619.5 KiB | 592.0 KiB | stored_raw=60% |
| delta-checkpoint | zstd-fast-1 | 128K | model | 1.107 | 1.054 | 1239 | 23104 | - | - | 619.5 KiB | 588.0 KiB | stored_raw=60% |
| delta-checkpoint | zstd-1 | 128K | model | 1.306 | 1.239 | 614 | 1387 | - | - | 619.5 KiB | 500.0 KiB |  |
| delta-checkpoint | zstd-3 | 128K | model | 1.320 | 1.269 | 243 | 1326 | - | - | 619.5 KiB | 488.0 KiB |  |
| delta-checkpoint | zstd-6 | 128K | model | 1.397 | 1.335 | 74 | 945 | - | - | 619.5 KiB | 464.0 KiB |  |
| delta-checkpoint | zstd-9 | 128K | model | 1.399 | 1.335 | 60 | 951 | - | - | 619.5 KiB | 464.0 KiB |  |
| delta-checkpoint | zstd-19 | 128K | model | 1.464 | 1.383 | 7 | 716 | - | - | 619.5 KiB | 448.0 KiB |  |
| delta-checkpoint | off | 1M | model | 1.000 | 0.999 | - | - | - | - | 619.5 KiB | 620.0 KiB |  |
| delta-checkpoint | lz4 | 1M | model | 1.073 | 0.999 | 1448 | - | - | - | 619.5 KiB | 620.0 KiB | every record stored raw: reads decompress nothing stored_raw=100% |
| delta-checkpoint | zstd-fast-1 | 1M | model | 1.089 | 0.999 | 1062 | - | - | - | 619.5 KiB | 620.0 KiB | every record stored raw: reads decompress nothing stored_raw=100% |
| delta-checkpoint | zstd-1 | 1M | model | 1.278 | 1.269 | 561 | 1425 | - | - | 619.5 KiB | 488.0 KiB |  |
| delta-checkpoint | zstd-3 | 1M | model | 1.339 | 1.335 | 154 | 1281 | - | - | 619.5 KiB | 464.0 KiB |  |
| delta-checkpoint | zstd-6 | 1M | model | 1.364 | 1.359 | 72 | 1214 | - | - | 619.5 KiB | 456.0 KiB |  |
| delta-checkpoint | zstd-9 | 1M | model | 1.365 | 1.359 | 72 | 1218 | - | - | 619.5 KiB | 456.0 KiB |  |
| delta-checkpoint | zstd-19 | 1M | model | 1.444 | 1.434 | 9 | 609 | - | - | 619.5 KiB | 432.0 KiB |  |
| model-weights | off | 128K | model | 1.000 | 1.000 | - | - | - | - | 64.0 MiB | 64.0 MiB |  |
| model-weights | lz4 | 128K | model | 1.000 | 1.000 | 7781 | - | - | - | 64.0 MiB | 64.0 MiB | every record stored raw: reads decompress nothing stored_raw=100% |
| model-weights | zstd-fast-1 | 128K | model | 1.000 | 1.000 | 6391 | - | - | - | 64.0 MiB | 64.0 MiB | every record stored raw: reads decompress nothing stored_raw=100% |
| model-weights | zstd-1 | 128K | model | 1.112 | 1.078 | 991 | 3760 | - | - | 64.0 MiB | 59.4 MiB | stored_raw=67% |
| model-weights | zstd-3 | 128K | model | 1.112 | 1.078 | 673 | 3750 | - | - | 64.0 MiB | 59.4 MiB | early_abort=67% stored_raw=67% |
| model-weights | zstd-6 | 128K | model | 1.112 | 1.078 | 503 | 3632 | - | - | 64.0 MiB | 59.4 MiB | early_abort=67% stored_raw=67% |
| model-weights | zstd-9 | 128K | model | 1.112 | 1.078 | 484 | 3624 | - | - | 64.0 MiB | 59.4 MiB | early_abort=67% stored_raw=67% |
| model-weights | zstd-19 | 128K | model | 1.111 | 1.074 | 34 | 3048 | - | - | 64.0 MiB | 59.6 MiB | early_abort=67% stored_raw=67% |
| model-weights | off | 1M | model | 1.000 | 1.000 | - | - | - | - | 64.0 MiB | 64.0 MiB |  |
| model-weights | lz4 | 1M | model | 1.000 | 1.000 | 10740 | - | - | - | 64.0 MiB | 64.0 MiB | every record stored raw: reads decompress nothing stored_raw=100% |
| model-weights | zstd-fast-1 | 1M | model | 1.000 | 1.000 | 5197 | - | - | - | 64.0 MiB | 64.0 MiB | every record stored raw: reads decompress nothing stored_raw=100% |
| model-weights | zstd-1 | 1M | model | 1.112 | 1.077 | 965 | 3768 | - | - | 64.0 MiB | 59.4 MiB | stored_raw=67% |
| model-weights | zstd-3 | 1M | model | 1.112 | 1.077 | 632 | 3678 | - | - | 64.0 MiB | 59.4 MiB | early_abort=67% stored_raw=67% |
| model-weights | zstd-6 | 1M | model | 1.112 | 1.077 | 623 | 3808 | - | - | 64.0 MiB | 59.4 MiB | early_abort=67% stored_raw=67% |
| model-weights | zstd-9 | 1M | model | 1.112 | 1.077 | 592 | 3772 | - | - | 64.0 MiB | 59.4 MiB | early_abort=67% stored_raw=67% |
| model-weights | zstd-19 | 1M | model | 1.113 | 1.078 | 48 | 3035 | - | - | 64.0 MiB | 59.4 MiB | early_abort=67% stored_raw=67% |
| images-jpeg | off | 128K | model | 1.000 | 0.915 | - | - | - | - | 64.1 MiB | 70.1 MiB |  |
| images-jpeg | lz4 | 128K | model | 1.092 | 0.994 | 7061 | 46624 | - | - | 64.1 MiB | 64.5 MiB | stored_raw=67% |
| images-jpeg | zstd-fast-1 | 128K | model | 1.093 | 0.997 | 6520 | 46781 | - | - | 64.1 MiB | 64.3 MiB | stored_raw=67% |
| images-jpeg | zstd-1 | 128K | model | 1.093 | 0.997 | 955 | 46725 | - | - | 64.1 MiB | 64.3 MiB | stored_raw=67% |
| images-jpeg | zstd-3 | 128K | model | 1.093 | 0.996 | 831 | 46837 | - | - | 64.1 MiB | 64.4 MiB | early_abort=67% stored_raw=67% |
| images-jpeg | zstd-6 | 128K | model | 1.093 | 0.996 | 779 | 47082 | - | - | 64.1 MiB | 64.4 MiB | early_abort=67% stored_raw=67% |
| images-jpeg | zstd-9 | 128K | model | 1.093 | 0.996 | 771 | 46633 | - | - | 64.1 MiB | 64.4 MiB | early_abort=67% stored_raw=67% |
| images-jpeg | zstd-19 | 128K | model | 1.106 | 0.998 | 38 | 2981 | - | - | 64.1 MiB | 64.3 MiB | early_abort=67% stored_raw=67% |
| images-jpeg | off | 1M | model | 1.000 | 0.997 | - | - | - | - | 64.1 MiB | 64.3 MiB |  |
| images-jpeg | lz4 | 1M | model | 1.000 | 0.997 | 9191 | - | - | - | 64.1 MiB | 64.3 MiB | every record stored raw: reads decompress nothing stored_raw=100% |
| images-jpeg | zstd-fast-1 | 1M | model | 1.000 | 0.997 | 5345 | - | - | - | 64.1 MiB | 64.3 MiB | every record stored raw: reads decompress nothing stored_raw=100% |
| images-jpeg | zstd-1 | 1M | model | 1.000 | 0.997 | 941 | - | - | - | 64.1 MiB | 64.3 MiB | every record stored raw: reads decompress nothing stored_raw=100% |
| images-jpeg | zstd-3 | 1M | model | 1.000 | 0.997 | 854 | - | - | - | 64.1 MiB | 64.3 MiB | every record stored raw: reads decompress nothing early_abort=100% stored_raw=100% |
| images-jpeg | zstd-6 | 1M | model | 1.000 | 0.997 | 854 | - | - | - | 64.1 MiB | 64.3 MiB | every record stored raw: reads decompress nothing early_abort=100% stored_raw=100% |
| images-jpeg | zstd-9 | 1M | model | 1.000 | 0.997 | 854 | - | - | - | 64.1 MiB | 64.3 MiB | every record stored raw: reads decompress nothing early_abort=100% stored_raw=100% |
| images-jpeg | zstd-19 | 1M | model | 1.013 | 0.997 | 854 | - | - | - | 64.1 MiB | 64.3 MiB | every record stored raw: reads decompress nothing early_abort=100% stored_raw=100% |
| os-files | off | 128K | model | 1.000 | 0.948 | - | - | - | - | 100.3 MiB | 105.9 MiB |  |
| os-files | lz4 | 128K | model | 1.994 | 1.784 | 600 | 3056 | - | - | 100.3 MiB | 56.2 MiB | stored_raw=37% |
| os-files | zstd-fast-1 | 128K | model | 2.303 | 2.035 | 376 | 1112 | - | - | 100.3 MiB | 49.3 MiB | stored_raw=36% |
| os-files | zstd-1 | 128K | model | 2.551 | 2.228 | 258 | 650 | - | - | 100.3 MiB | 45.0 MiB | stored_raw=35% |
| os-files | zstd-3 | 128K | model | 2.691 | 2.334 | 155 | 630 | - | - | 100.3 MiB | 43.0 MiB | early_abort=1% stored_raw=35% |
| os-files | zstd-6 | 128K | model | 2.854 | 2.461 | 68 | 652 | - | - | 100.3 MiB | 40.8 MiB | early_abort=1% stored_raw=35% |
| os-files | zstd-9 | 128K | model | 2.881 | 2.481 | 40 | 650 | - | - | 100.3 MiB | 40.4 MiB | early_abort=1% stored_raw=35% |
| os-files | zstd-19 | 128K | model | 3.102 | 2.652 | 6 | 563 | - | - | 100.3 MiB | 37.8 MiB | early_abort=1% stored_raw=35% |
| os-files | off | 1M | model | 1.000 | 0.908 | - | - | - | - | 100.3 MiB | 110.5 MiB |  |
| os-files | lz4 | 1M | model | 2.096 | 1.834 | 600 | 3017 | - | - | 100.3 MiB | 54.7 MiB | stored_raw=54% |
| os-files | zstd-fast-1 | 1M | model | 2.403 | 2.083 | 375 | 1099 | - | - | 100.3 MiB | 48.1 MiB | stored_raw=53% |
| os-files | zstd-1 | 1M | model | 2.712 | 2.328 | 258 | 643 | - | - | 100.3 MiB | 43.1 MiB | stored_raw=52% |
| os-files | zstd-3 | 1M | model | 2.988 | 2.544 | 155 | 624 | - | - | 100.3 MiB | 39.4 MiB | early_abort=0% stored_raw=52% |
| os-files | zstd-6 | 1M | model | 3.150 | 2.668 | 69 | 647 | - | - | 100.3 MiB | 37.6 MiB | early_abort=0% stored_raw=52% |
| os-files | zstd-9 | 1M | model | 3.182 | 2.692 | 40 | 641 | - | - | 100.3 MiB | 37.3 MiB | early_abort=0% stored_raw=52% |
| os-files | zstd-19 | 1M | model | 3.488 | 2.928 | 6 | 559 | - | - | 100.3 MiB | 34.3 MiB | early_abort=0% stored_raw=52% |
| container-layers | off | 128K | model | 1.000 | 0.999 | - | - | - | - | 38.4 MiB | 38.4 MiB |  |
| container-layers | lz4 | 128K | model | 1.002 | 1.000 | 6531 | >100000 | - | - | 38.4 MiB | 38.4 MiB | stored_raw=100% |
| container-layers | zstd-fast-1 | 128K | model | 1.001 | 1.000 | 6091 | >100000 | - | - | 38.4 MiB | 38.4 MiB | stored_raw=100% |
| container-layers | zstd-1 | 128K | model | 1.002 | 1.000 | 3033 | >100000 | - | - | 38.4 MiB | 38.4 MiB | stored_raw=100% |
| container-layers | zstd-3 | 128K | model | 1.002 | 1.000 | 2070 | >100000 | - | - | 38.4 MiB | 38.4 MiB | early_abort=100% stored_raw=100% |
| container-layers | zstd-6 | 128K | model | 1.002 | 1.000 | 2066 | >100000 | - | - | 38.4 MiB | 38.4 MiB | early_abort=100% stored_raw=100% |
| container-layers | zstd-9 | 128K | model | 1.002 | 1.000 | 2065 | >100000 | - | - | 38.4 MiB | 38.4 MiB | early_abort=100% stored_raw=100% |
| container-layers | zstd-19 | 128K | model | 1.005 | 1.000 | 1422 | >100000 | - | - | 38.4 MiB | 38.4 MiB | early_abort=100% stored_raw=100% |
| container-layers | off | 1M | model | 1.000 | 0.983 | - | - | - | - | 38.4 MiB | 39.0 MiB |  |
| container-layers | lz4 | 1M | model | 1.018 | 1.000 | 8301 | >100000 | - | - | 38.4 MiB | 38.4 MiB | stored_raw=97% |
| container-layers | zstd-fast-1 | 1M | model | 1.018 | 1.000 | 4806 | >100000 | - | - | 38.4 MiB | 38.4 MiB | stored_raw=97% |
| container-layers | zstd-1 | 1M | model | 1.018 | 1.000 | 2697 | >100000 | - | - | 38.4 MiB | 38.4 MiB | stored_raw=97% |
| container-layers | zstd-3 | 1M | model | 1.018 | 1.000 | 2027 | >100000 | - | - | 38.4 MiB | 38.3 MiB | early_abort=97% stored_raw=97% |
| container-layers | zstd-6 | 1M | model | 1.019 | 1.000 | 1996 | >100000 | - | - | 38.4 MiB | 38.3 MiB | early_abort=97% stored_raw=97% |
| container-layers | zstd-9 | 1M | model | 1.019 | 1.000 | 1976 | >100000 | - | - | 38.4 MiB | 38.3 MiB | early_abort=97% stored_raw=97% |
| container-layers | zstd-19 | 1M | model | 1.022 | 1.000 | 630 | >100000 | - | - | 38.4 MiB | 38.3 MiB | early_abort=97% stored_raw=97% |
| source-code | off | 128K | model | 1.000 | 0.776 | - | - | - | - | 64.0 MiB | 82.5 MiB |  |
| source-code | lz4 | 128K | model | 2.705 | 1.518 | 622 | 3298 | - | - | 64.0 MiB | 42.2 MiB | stored_raw=67% |
| source-code | zstd-fast-1 | 128K | model | 3.224 | 1.672 | 375 | 1212 | - | - | 64.0 MiB | 38.3 MiB | stored_raw=66% |
| source-code | zstd-1 | 128K | model | 3.837 | 1.811 | 285 | 811 | - | - | 64.0 MiB | 35.3 MiB | stored_raw=65% |
| source-code | zstd-3 | 128K | model | 3.969 | 1.839 | 202 | 790 | - | - | 64.0 MiB | 34.8 MiB | early_abort=0% stored_raw=65% |
| source-code | zstd-6 | 128K | model | 4.206 | 1.884 | 82 | 839 | - | - | 64.0 MiB | 34.0 MiB | early_abort=0% stored_raw=65% |
| source-code | zstd-9 | 128K | model | 4.256 | 1.893 | 43 | 846 | - | - | 64.0 MiB | 33.8 MiB | early_abort=0% stored_raw=65% |
| source-code | zstd-19 | 128K | model | 4.445 | 1.932 | 5 | 761 | - | - | 64.0 MiB | 33.1 MiB | early_abort=0% stored_raw=65% |
| source-code | off | 1M | model | 1.000 | 0.783 | - | - | - | - | 64.0 MiB | 81.8 MiB |  |
| source-code | lz4 | 1M | model | 2.681 | 1.532 | 623 | 3293 | - | - | 64.0 MiB | 41.8 MiB | stored_raw=68% |
| source-code | zstd-fast-1 | 1M | model | 3.201 | 1.692 | 374 | 1207 | - | - | 64.0 MiB | 37.9 MiB | stored_raw=67% |
| source-code | zstd-1 | 1M | model | 3.823 | 1.836 | 285 | 813 | - | - | 64.0 MiB | 34.9 MiB | stored_raw=67% |
| source-code | zstd-3 | 1M | model | 3.968 | 1.867 | 202 | 793 | - | - | 64.0 MiB | 34.3 MiB | early_abort=0% stored_raw=67% |
| source-code | zstd-6 | 1M | model | 4.204 | 1.913 | 82 | 843 | - | - | 64.0 MiB | 33.5 MiB | early_abort=0% stored_raw=67% |
| source-code | zstd-9 | 1M | model | 4.253 | 1.923 | 43 | 848 | - | - | 64.0 MiB | 33.3 MiB | early_abort=0% stored_raw=67% |
| source-code | zstd-19 | 1M | model | 4.455 | 1.965 | 5 | 763 | - | - | 64.0 MiB | 32.6 MiB | early_abort=0% stored_raw=67% |

(The raw codec benchmarks behind the zfs table, layer `zfs-codec`, are in compress.json.)

## File access (Parquet)

| Test | Method | Block | QD | fadvise | Cache | FS | MB/s | Notes |
|---|---|---|---:|---|---|---|---:|---|
| seq | pread | 1M | 1 | normal | cold | btrfs | 2162 |  |
| seq | pread | 1M | 1 | sequential | cold | btrfs | 1988 |  |
| seq | pread | 1M | 1 | normal | warm | btrfs | 7519 |  |
| seq | odirect | 1M | 1 |  | cold | btrfs | 2045 |  |
| seq | odirect | 1M | 8 |  | cold | btrfs | 5004 | 8 threads, one pread each |
| seq | io_uring | 1M | 8 |  | cold | btrfs | 4975 |  |
| seq | io_uring | 1M | 32 |  | cold | btrfs | 5014 |  |
| rowgroup | pread | 256K | 1 | normal | cold | btrfs | 706 |  |
| rowgroup | pread | 256K | 1 | random | cold | btrfs | 712 |  |
| rowgroup | odirect | 256K | 1 |  | cold | btrfs | 1005 |  |
| rowgroup | odirect | 256K | 8 |  | cold | btrfs | 4577 | 8 threads, one pread each |
| rowgroup | io_uring | 256K | 8 |  | cold | btrfs | 4583 |  |
| rowgroup | io_uring | 256K | 32 |  | cold | btrfs | 4825 |  |

## Columnar scan and transparent huge pages

| Memory | Phase | THP | Threads | Mrows/s | Seconds | Notes |
|---|---|---|---:|---:|---:|---|
| go-mmap | populate | madvise-hugepage | 16 | 133.0 | 0.505 | 67108864 rows, 1.8 GiB; process AnonHugePages 1826 MiB |
| go-mmap | scan | madvise-hugepage | 1 | 140.4 | 0.478 | TPC-H Q6 predicate, best of reps |
| go-mmap | scan | madvise-hugepage | 16 | 1600.9 | 0.042 | TPC-H Q6 predicate, best of reps |
| go-mmap | populate | madvise-nohugepage | 16 | 317.7 | 0.211 | 67108864 rows, 1.8 GiB; process AnonHugePages 82 MiB |
| go-mmap | scan | madvise-nohugepage | 1 | 133.2 | 0.504 | TPC-H Q6 predicate, best of reps |
| go-mmap | scan | madvise-nohugepage | 16 | 1541.0 | 0.044 | TPC-H Q6 predicate, best of reps |
| duckdb-parquet | query | system-default | 0 | 142.9 | 0.042 | TPC-H Q1 shape, warm, median of 4; system THP: [always] madvise never |
| duckdb-parquet | query | disabled-for-process | 0 | 142.9 | 0.042 | TPC-H Q1 shape, warm, median of 4; system THP: [always] madvise never |

## What the declared rules pick

| Layer | Scope | Setting | Rule | Why |
|---|---|---|---|---|
| zfs | <pool>/ROOT/<be> (/) [workstation+server] recordsize=128K | **zstd-1** | max-alloc-ratio-at-cpu-floor | allocated ratio 2.228 (off 0.948), 258 MB/s/core compress, 650 MB/s/core decompress; the boot environment: binaries, libraries, package files; small random reads |
| zfs | <pool>/home (/home) [workstation] recordsize=128K | **zstd-1** | max-alloc-ratio-at-cpu-floor | allocated ratio 1.508 (off 0.889), 449 MB/s/core compress, 1441 MB/s/core decompress; a developer's home: checkouts, notebooks' data, a few models and pictures; mixed access |
| zfs | <pool>/home/<user>/data (data lake) [workstation] recordsize=1M | **zstd-1** | max-alloc-ratio-at-cpu-floor | allocated ratio 1.215 (off 0.984), 1054 MB/s/core compress, 4823 MB/s/core decompress; open-table-format data: large Parquet files read sequentially, plus the JSON log |
| zfs | <pool>/containers (/var/lib/k0s) [server] recordsize=128K | **zstd-3** | max-alloc-ratio-at-cpu-floor | allocated ratio 1.400 (off 0.973), 288 MB/s/core compress, 1260 MB/s/core decompress; compressed layer blobs in the content store plus their unpacked snapshots |
| zfs | <pool>/state (/var/lib/core, node state) [server] recordsize=1M | **zstd-1** | max-alloc-ratio-at-cpu-floor | allocated ratio 1.129 (off 0.992), 1277 MB/s/core compress, 5847 MB/s/core decompress; a node's local image registry blobs and model weights, plus logs; large sequential files |
| zfs | <pool>/models (model files) [workstation+server] recordsize=1M | **zstd-1** | max-alloc-ratio-at-cpu-floor | allocated ratio 1.077 (off 1.000), 965 MB/s/core compress, 3768 MB/s/core decompress; large model files, written once, read sequentially |
| zfs-shape | parquet-snappy recordsize=128K | **zstd-6** | max-alloc-ratio-at-cpu-floor | allocated ratio 1.103 (off 1.000), 251 MB/s/core compress, 2979 MB/s/core decompress, 65% of records stored raw |
| zfs-shape | parquet-snappy recordsize=1M | **zstd-1** | max-alloc-ratio-at-cpu-floor | allocated ratio 1.100 (off 1.000), 860 MB/s/core compress, 3109 MB/s/core decompress, 56% of records stored raw |
| zfs-shape | parquet-zstd recordsize=128K | **lz4** | max-alloc-ratio-at-cpu-floor | allocated ratio 1.005 (off 1.000), 6771 MB/s/core compress, >100000 MB/s/core (almost every record stored raw) decompress, 96% of records stored raw |
| zfs-shape | parquet-zstd recordsize=1M | **lz4** | max-alloc-ratio-at-cpu-floor | allocated ratio 1.000 (off 1.000), 8638 MB/s/core compress, no decompression (records stored raw) decompress, 100% of records stored raw |
| zfs-shape | csv recordsize=128K | **zstd-1** | max-alloc-ratio-at-cpu-floor | allocated ratio 2.909 (off 1.000), 290 MB/s/core compress, 976 MB/s/core decompress, 0% of records stored raw |
| zfs-shape | csv recordsize=1M | **zstd-1** | max-alloc-ratio-at-cpu-floor | allocated ratio 3.053 (off 1.000), 290 MB/s/core compress, 1083 MB/s/core decompress, 0% of records stored raw |
| zfs-shape | ndjson recordsize=128K | **zstd-1** | max-alloc-ratio-at-cpu-floor | allocated ratio 6.400 (off 1.000), 564 MB/s/core compress, 1449 MB/s/core decompress, 0% of records stored raw |
| zfs-shape | ndjson recordsize=1M | **zstd-3** | max-alloc-ratio-at-cpu-floor | allocated ratio 7.111 (off 1.000), 288 MB/s/core compress, 1592 MB/s/core decompress, 0% of records stored raw |
| zfs-shape | avro recordsize=128K | **zstd-1** | max-alloc-ratio-at-cpu-floor | allocated ratio 2.133 (off 1.000), 311 MB/s/core compress, 1000 MB/s/core decompress, 0% of records stored raw |
| zfs-shape | avro recordsize=1M | **zstd-1** | max-alloc-ratio-at-cpu-floor | allocated ratio 2.123 (off 1.000), 311 MB/s/core compress, 1055 MB/s/core decompress, 0% of records stored raw |
| zfs-shape | delta-log recordsize=128K | **lz4** | max-alloc-ratio-at-cpu-floor | allocated ratio 0.401 (off 0.401), 1155 MB/s/core compress, no decompression (records stored raw) decompress, 100% of records stored raw |
| zfs-shape | delta-log recordsize=1M | **lz4** | max-alloc-ratio-at-cpu-floor | allocated ratio 0.401 (off 0.401), 1160 MB/s/core compress, no decompression (records stored raw) decompress, 100% of records stored raw |
| zfs-shape | delta-checkpoint recordsize=128K | **zstd-1** | max-alloc-ratio-at-cpu-floor | allocated ratio 1.239 (off 0.968), 614 MB/s/core compress, 1387 MB/s/core decompress, 0% of records stored raw |
| zfs-shape | delta-checkpoint recordsize=1M | **zstd-1** | max-alloc-ratio-at-cpu-floor | allocated ratio 1.269 (off 0.999), 561 MB/s/core compress, 1425 MB/s/core decompress, 0% of records stored raw |
| zfs-shape | model-weights recordsize=128K | **zstd-1** | max-alloc-ratio-at-cpu-floor | allocated ratio 1.078 (off 1.000), 991 MB/s/core compress, 3760 MB/s/core decompress, 67% of records stored raw |
| zfs-shape | model-weights recordsize=1M | **zstd-1** | max-alloc-ratio-at-cpu-floor | allocated ratio 1.077 (off 1.000), 965 MB/s/core compress, 3768 MB/s/core decompress, 67% of records stored raw |
| zfs-shape | images-jpeg recordsize=128K | **lz4** | max-alloc-ratio-at-cpu-floor | allocated ratio 0.994 (off 0.915), 7061 MB/s/core compress, 46624 MB/s/core decompress, 67% of records stored raw |
| zfs-shape | images-jpeg recordsize=1M | **lz4** | max-alloc-ratio-at-cpu-floor | allocated ratio 0.997 (off 0.997), 9191 MB/s/core compress, no decompression (records stored raw) decompress, 100% of records stored raw |
| zfs-shape | os-files recordsize=128K | **zstd-1** | max-alloc-ratio-at-cpu-floor | allocated ratio 2.228 (off 0.948), 258 MB/s/core compress, 650 MB/s/core decompress, 35% of records stored raw |
| zfs-shape | os-files recordsize=1M | **zstd-1** | max-alloc-ratio-at-cpu-floor | allocated ratio 2.328 (off 0.908), 258 MB/s/core compress, 643 MB/s/core decompress, 52% of records stored raw |
| zfs-shape | container-layers recordsize=128K | **lz4** | max-alloc-ratio-at-cpu-floor | allocated ratio 1.000 (off 0.999), 6531 MB/s/core compress, >100000 MB/s/core (almost every record stored raw) decompress, 100% of records stored raw |
| zfs-shape | container-layers recordsize=1M | **lz4** | max-alloc-ratio-at-cpu-floor | allocated ratio 1.000 (off 0.983), 8301 MB/s/core compress, >100000 MB/s/core (almost every record stored raw) decompress, 97% of records stored raw |
| zfs-shape | source-code recordsize=128K | **zstd-1** | max-alloc-ratio-at-cpu-floor | allocated ratio 1.811 (off 0.776), 285 MB/s/core compress, 811 MB/s/core decompress, 65% of records stored raw |
| zfs-shape | source-code recordsize=1M | **zstd-1** | max-alloc-ratio-at-cpu-floor | allocated ratio 1.836 (off 0.783), 285 MB/s/core compress, 813 MB/s/core decompress, 67% of records stored raw |
| zram | duckdb-process | **zstd-3** | max-alloc-ratio-at-fault-latency | allocated ratio 3.487, 707 MB/s/core decompress (5.8 us per 4 KiB page) |
| zram | go-process | **zstd-3** | max-alloc-ratio-at-fault-latency | allocated ratio 4.347, 764 MB/s/core decompress (5.4 us per 4 KiB page) |
| kernel | vmlinux 7.2.7-zen1-1-runink | **zstd-19** | min-boot-cost | 17.1 MiB, 89.0 ms to decompress, boot cost 268 ms at 100 MB/s read; stays the pick for read rates 59 to 164 MB/s |
| initramfs | modules-uncompressed | **zstd-19** | min-boot-cost | 33.9 MiB, 199.3 ms to decompress, boot cost 554 ms at 100 MB/s read; stays the pick for read rates 4 to 116 MB/s |
| initramfs | modules-ko.zst | **zstd-19** | min-boot-cost | 41.2 MiB, 40.5 ms to decompress, boot cost 473 ms at 100 MB/s read; stays the pick for read rates 1 to 132 MB/s |
| modules | 400 modules | **zstd-19** | min-boot-cost | 8.4 MiB, 46.4 ms to decompress, boot cost 135 ms at 100 MB/s read; stays the pick for read rates 5 to 139 MB/s |
| parquet | tpch-lineitem | **snappy** | smallest-within-read-budget | 197.5 MiB (ratio 1.89 vs uncompressed), cold scan 151 ms (fastest 142 ms), write 1764 ms |

**Not run:**

- `merged`: rows from run b00bb83ffcda8c8d
