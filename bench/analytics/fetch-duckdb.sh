#!/bin/sh
# fetch-duckdb.sh: download the DuckDB CLI release pinned in duckdb.lock into the benchmark
# cache, verify its sha256, and print the path of the binary. An existing verified copy is
# reused; a download that does not match the pin is deleted and the script fails.
#
# Server nodes (a downstream distribution) carry no curl: fetch on a workstation and
# rsync the binary over, then pass it to run.sh with DUCKDB=/path.
set -eu

here="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=bench/analytics/duckdb.lock
. "$here/duckdb.lock"
cache="${RIVERBENCH_CACHE:-${XDG_CACHE_HOME:-$HOME/.cache}/river-bench}"
dir="$cache/duckdb-v$DUCKDB_VERSION"
bin="$dir/duckdb"
url="https://github.com/duckdb/duckdb/releases/download/v$DUCKDB_VERSION/$DUCKDB_ASSET"

if [ -x "$bin" ] && [ -f "$dir/.verified" ]; then
	echo "$bin"
	exit 0
fi
command -v curl >/dev/null 2>&1 || { echo "fetch-duckdb: curl not found" >&2; exit 1; }
mkdir -p "$dir"
tmp="$dir/$DUCKDB_ASSET.part"
curl -fsSL --retry 3 -o "$tmp" "$url"
got="$(sha256sum "$tmp" | cut -d' ' -f1)"
if [ "$got" != "$DUCKDB_SHA256" ]; then
	rm -f "$tmp"
	echo "fetch-duckdb: sha256 mismatch for $DUCKDB_ASSET: got $got, pinned $DUCKDB_SHA256" >&2
	exit 1
fi
gzip -dc "$tmp" > "$bin.part"
chmod 0755 "$bin.part"
mv "$bin.part" "$bin"
rm -f "$tmp"
"$bin" -version >&2
echo "$DUCKDB_SHA256  $DUCKDB_ASSET" > "$dir/.verified"
echo "$bin"
