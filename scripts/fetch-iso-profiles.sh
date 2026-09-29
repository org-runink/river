#!/bin/sh
# Pin the upstream Artix iso-profiles base we forked from, for a reviewable diff.
# We VENDOR our profile under iso-profiles/river/; this fetches the canonical
# upstream tree into iso-profiles/.upstream/ (gitignored) so `git diff --no-index` shows
# exactly what we changed relative to Artix.
set -eu

UPSTREAM_URL="https://gitea.artixlinux.org/artix/iso-profiles.git"
# Pin to a commit for reproducibility; update deliberately.
UPSTREAM_REF="${ISO_PROFILES_REF:-master}"
DEST="iso-profiles/.upstream"

if ! command -v git >/dev/null 2>&1; then
	echo "fetch-iso-profiles: git required" >&2
	exit 1
fi

if [ -d "$DEST/.git" ]; then
	echo "fetch-iso-profiles: updating $DEST"
	git -C "$DEST" fetch --depth 1 origin "$UPSTREAM_REF"
	git -C "$DEST" checkout -q FETCH_HEAD
else
	echo "fetch-iso-profiles: cloning $UPSTREAM_URL@$UPSTREAM_REF"
	git clone --depth 1 --branch "$UPSTREAM_REF" "$UPSTREAM_URL" "$DEST" 2>/dev/null \
		|| git clone --depth 1 "$UPSTREAM_URL" "$DEST"
fi

echo "fetch-iso-profiles: upstream base at $DEST ($(git -C "$DEST" rev-parse --short HEAD))"
echo "  compare with:  git diff --no-index $DEST/base iso-profiles/river"
