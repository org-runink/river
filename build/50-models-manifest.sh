#!/bin/sh
# 50-models-manifest.sh — verify MODELS_DIR against models.lock, then write models.manifest.
#
# The model set is PINNED in models.lock (repo root): upstream Hugging Face repo, exact
# commit revision, and size + sha256 per file. build/models-fetch.sh fills a build-host
# cache from that lock at ISO BUILD time; the installed node never fetches a model.
#
# This step no longer hashes "whatever sits in a reference directory". It used to snapshot
# the server's /var/lib/core/models/shared, so the manifest described what one box happened
# to hold (including an unused GGUF, and not two files the inference tiers named, which were
# missing). Now the lock is the list: `models-fetch.sh --check` proves every locked file is
# present in MODELS_DIR with the pinned size and sha256, and the manifest is written from
# the lock rows. Files in MODELS_DIR that the lock does not name are ignored.
#
# Output: $OUT_DIR/models.manifest — `sha256  ./<dest>` lines, which runink-firstboot.sh
#         checks with `sha256sum -c` from /var/lib/core/models/shared (staged into the image
#         at /usr/local/share/runink/models.manifest by the runink-runtime package).
#
# Usage: 50-models-manifest.sh [MODELS_DIR]
#        (default: $MODELS_DIR, else ${XDG_CACHE_HOME:-$HOME/.cache}/river-build/models)
set -eu

HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck disable=SC1091
. "$HERE/config.env"

MODELS_DIR="${1:-${MODELS_DIR:-${XDG_CACHE_HOME:-$HOME/.cache}/river-build/models}}"
LOCK="${MODELS_LOCK:-$HERE/../models.lock}"
DEST="$OUT_DIR/models.manifest"
mkdir -p "$OUT_DIR"
export MODELS_DIR MODELS_LOCK="$LOCK"

if [ ! -d "$MODELS_DIR" ] && { [ "${RIVER_PAYLOAD_NONE:-0}" = 1 ] || [ "${MODEL_PAYLOAD:-auto}" = no ]; }; then
	# A base image has no inference workloads to verify weights for, and a no-model image
	# (MODEL_PAYLOAD=no: the model set is decided later) carries no weights: either way ship
	# no manifest at all (firstboot only verifies when the file exists), not an empty one.
	echo "50-models-manifest: no model payload (base image or MODEL_PAYLOAD=no) — no models manifest."
	rm -f "$DEST"
	exit 0
fi

if ! "$HERE/models-fetch.sh" --check; then
	echo "50-models-manifest: $MODELS_DIR does not match $LOCK." >&2
	echo "  Fill it first: MODELS_DIR=$MODELS_DIR build/models-fetch.sh" >&2
	# An empty or partial manifest is a silent failure: firstboot's `sha256sum -c` on an
	# all-comment file trivially "passes", so a node with missing or corrupt weights
	# verified clean (shipped that way 2026-08-13). Fail instead, with the same explicit
	# opt-out build-all.sh uses.
	if [ "${RUNINK_ALLOW_PARTIAL:-0}" = 1 ]; then
		echo "50-models-manifest: RUNINK_ALLOW_PARTIAL=1 — writing an EMPTY manifest." >&2
		echo "  Model verification on the node will be a no-op. Do not ship this image." >&2
		: > "$DEST"
		exit 0
	fi
	exit 1
fi

# Columns: role repo revision file size sha256 license dest
# (dest `-` means the file's basename at the MODELS_DIR root, as in models-fetch.sh.)
awk '!/^[[:space:]]*(#|$)/ {
	d = $8; if (d == "-") { d = $4; sub(/.*\//, "", d) }
	printf "%s  ./%s\n", $6, d
}' "$LOCK" > "$DEST"
echo "50-models-manifest: wrote $DEST ($(wc -l < "$DEST") file(s), verified in $MODELS_DIR)"
cat "$DEST"
