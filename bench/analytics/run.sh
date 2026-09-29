#!/bin/bash
# run.sh: run the Runink River analytics benchmark on the RUNNING kernel.
#
#   bench/analytics/run.sh --smoke --out /path/results/smoke
#   bench/analytics/run.sh --out /path/results/$(uname -r) \
#       --fio-dirs zfs=/tank/bench,ext4=/mnt/bench --tpch-dir /tank/bench/tpch
#
# One invocation measures one kernel. Reboot into each kernel under test (linux-runink,
# linux-zen, linux-lts) on the SAME machine, run this with the same arguments, then:
#
#   bench/analytics/run.sh compare results/*/results.json
#
# Compression, file access and THP (see README.md; rootless unless you add the on-box layers):
#
#   bench/analytics/run.sh compress --out /path/results/compress [--smoke]
#   bench/analytics/run.sh compare base/compress.json new/compress.json   # JSON diff
#
# RIVER_ARTIFACTS points at the built packages for the kernel layer (default build/artifacts).
#
# Needs: go (to build riverbench), curl + gzip (first DuckDB fetch only), fio (storage suite);
# compress also needs zstd, lz4, xz and tar.
# Every other argument is passed to `riverbench run`; see `riverbench run -h`.
set -euo pipefail

here="$(cd "$(dirname "$0")" && pwd)"
cache="${RIVERBENCH_CACHE:-${XDG_CACHE_HOME:-$HOME/.cache}/river-bench}"
export RIVERBENCH_CACHE="$cache"
mkdir -p "$cache/bin"

build() {
	command -v go >/dev/null 2>&1 || { echo "run.sh: go not found (pacman -S go)" >&2; exit 1; }
	(cd "$here" && CGO_ENABLED=0 go build -trimpath -o "$cache/bin/riverbench" ./cmd/riverbench)
}

if [ "${1:-}" = compare ] || [ "${1:-}" = export ] || [ "${1:-}" = catalogue ]; then
	sub="$1"
	shift
	build
	exec "$cache/bin/riverbench" "$sub" "$@"
fi

mode="run"
if [ "${1:-}" = compress ]; then
	mode="compress"
	shift
fi

args=()
smoke=0
while [ $# -gt 0 ]; do
	case "$1" in
		--smoke) smoke=1; args+=(-smoke) ;;
		--*) args+=("-${1#--}") ;;
		*) args+=("$1") ;;
	esac
	shift
done

build
duckdb="${DUCKDB:-}"
if [ -z "$duckdb" ]; then
	duckdb="$("$here/fetch-duckdb.sh")" || { echo "run.sh: DuckDB fetch failed; tpch will be skipped" >&2; duckdb=""; }
fi

# Record what else was running: a benchmark on a busy machine is not a result.
echo "run.sh: load average $(cut -d' ' -f1-3 /proc/loadavg); kernel $(uname -r); smoke=$smoke" >&2
if [ "$mode" = compress ]; then
	# The kernel layer reads the built packages (never the running system's /boot).
	art="${RIVER_ARTIFACTS:-$here/../../build/artifacts}"
	pkg() { for f in "$art"/$1; do [ -f "$f" ] && { echo "$f"; return; }; done; }
	exec "$cache/bin/riverbench" compress -duckdb "$duckdb" \
		-kernel-pkg "$(pkg 'linux-runink-[0-9]*.pkg.tar.zst')" \
		-zfs-pkg "$(pkg 'runink-zfs-[0-9]*.pkg.tar.zst')" \
		-zfs-utils-pkg "$(pkg 'runink-zfs-utils-[0-9]*.pkg.tar.zst')" "${args[@]}"
fi
exec "$cache/bin/riverbench" run -duckdb "$duckdb" "${args[@]}"
