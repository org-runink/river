#!/bin/sh
# SPDX-FileCopyrightText: 2026 The RIVER Authors
# SPDX-License-Identifier: MIT
#
# 25-river-guide.sh — stage river-guide (the install guide agent) for a LIVE medium.
#
#   1. Build guide/cmd/river-guide (Go, standard library only, CGO off, GOAMD64=v1 so it
#      runs on — and can explain — the machines the installer refuses).
#   2. Stage the public Runink River guide markdown it embeds, for read-only paging on the medium.
#   3. With RIVER_GUIDE_MODEL=1, fetch the ONE guide model named in guide/model.lock, verify
#      its sha256, and stage it under $OUT_DIR/river-guide/model (gitignored). The default is 0:
#      Runink River's own image does not carry the guide; build/local-iso.sh sets 1 for a
#      profile that ships it (it has a guide-model.lock, docs/BUILD.md "Downstream
#      distributions") and copies the model into that profile's live overlay.
#
# The model server binary is NOT fetched here: build/pkgbuilds/river-guide pulls the pinned
# mistral.rs release through makepkg's own sha256 check.
#
# Output: $OUT_DIR/river-guide/{river-guide,bundles/river/*.md}
#         $OUT_DIR/river-guide/model/<file>.gguf   (RIVER_GUIDE_MODEL=1 only)
set -eu

HERE="$(cd "$(dirname "$0")" && pwd)"
ROOT="$(cd "$HERE/.." && pwd)"
# shellcheck disable=SC1091
. "$HERE/config.env"

DEST="$OUT_DIR/river-guide"
LOCK="$ROOT/guide/model.lock"
MODEL_DIR="$DEST/model"

command -v go >/dev/null 2>&1 || { echo "25-river-guide: go toolchain required (builder only)" >&2; exit 1; }
mkdir -p "$DEST/bundles/river"

echo "  building river-guide (GOAMD64=v1, CGO off)"
( cd "$ROOT/guide" && CGO_ENABLED=0 GOAMD64=v1 go build -trimpath \
	-ldflags="-s -w -X main.version=$(cat "$ROOT/VERSION" 2>/dev/null || echo dev)" \
	-o "$DEST/river-guide" ./cmd/river-guide )
cp "$ROOT"/guide/bundles/river/*.md "$DEST/bundles/river/"

if [ "${RIVER_GUIDE_MODEL:-0}" != 1 ]; then
	echo "  RIVER_GUIDE_MODEL=${RIVER_GUIDE_MODEL:-0}: guide model not fetched (a staged one, if any, is kept for reuse)"
	exit 0
fi

# The lock's model row: role repo revision file size sha256 license dest
row="$(awk '$1 == "guide" { print; exit }' "$LOCK")"
[ -n "$row" ] || { echo "25-river-guide: no guide row in $LOCK" >&2; exit 1; }
# shellcheck disable=SC2086
set -- $row
repo="$2"; rev="$3"; file="$4"; size="$5"; sum="$6"

mkdir -p "$MODEL_DIR"
target="$MODEL_DIR/$file"
if [ -f "$target" ] && echo "$sum  $target" | sha256sum -c --status; then
	echo "  guide model already staged and verified: $file"
else
	command -v curl >/dev/null 2>&1 || { echo "25-river-guide: curl required on the builder to fetch the model" >&2; exit 1; }
	echo "  fetching $repo@$rev/$file ($size bytes)"
	# Remove stale weights first: exactly one model may be on the medium.
	find "$MODEL_DIR" -maxdepth 1 -name '*.gguf' -delete
	curl -fL --retry 3 -o "$target.part" "https://huggingface.co/$repo/resolve/$rev/$file"
	echo "$sum  $target.part" | sha256sum -c --status || {
		echo "25-river-guide: sha256 mismatch for $file — refusing to stage it" >&2
		rm -f "$target.part"; exit 1; }
	mv "$target.part" "$target"
fi
chmod 0644 "$target"
echo "25-river-guide: staged $DEST and $target"
