#!/bin/sh
# SPDX-FileCopyrightText: 2026 The RIVER Authors
# SPDX-License-Identifier: MIT
#
# release-assets.sh — prepare a release's assets for GitHub, whose release assets are limited
# to 2 GiB each: an ISO that fits is copied as is; a larger one is split into numbered parts
# (<iso>.part-00, -01, … of 1900 MiB). SHA256SUMS lists every ISO AND every part, so the one
# offline signature covers both, install.sh verifies each part and the joined ISO, and the
# release gate (.github/workflows/release-gate.yml) joins and verifies them the same way.
#
#   build/release-assets.sh OUT_DIR ISO [ISO...]
#
# Then, as the release maintainer (the key never leaves your machine):
#   gpg --local-user <the KEYS fingerprint> --armor --detach-sign OUT_DIR/SHA256SUMS
#   gh release create <tag> --draft --title <tag> OUT_DIR/*
#   gh workflow run release-gate.yml -f tag=<tag> -f dry_run=false
set -eu

LIMIT=2147483648              # GitHub's per-asset limit (2 GiB)
PART=1992294400               # 1900 MiB per part, safely under it

[ $# -ge 2 ] || { sed -n '4,17p' "$0" | sed 's/^# \{0,1\}//'; exit 2; }
out="$1"; shift
mkdir -p "$out"
[ -z "$(ls -A "$out")" ] || { echo "release-assets: $out is not empty" >&2; exit 1; }

for iso in "$@"; do
	[ -f "$iso" ] || { echo "release-assets: no such file: $iso" >&2; exit 1; }
	case "$iso" in *.iso) ;; *) echo "release-assets: not an .iso: $iso" >&2; exit 1 ;; esac
	base="$(basename "$iso")"
	size="$(stat -c %s "$iso")"
	if [ "$size" -le "$LIMIT" ]; then
		cp "$iso" "$out/$base"
		echo "release-assets: $base ($size bytes) fits in one asset"
	else
		split -b "$PART" -d -a 2 "$iso" "$out/$base.part-"
		n="$(ls "$out/$base".part-* | wc -l)"
		echo "release-assets: $base ($size bytes) split into $n parts"
		# The whole ISO's sum is computed from the ISO itself, never from the parts.
		( cd "$(dirname "$iso")" && sha256sum "$base" ) >> "$out/SHA256SUMS.whole"
	fi
done

(
	cd "$out"
	find . -maxdepth 1 -type f ! -name 'SHA256SUMS*' -printf '%P\0' | sort -z | xargs -0 sha256sum -- > SHA256SUMS.parts
	cat SHA256SUMS.whole SHA256SUMS.parts 2>/dev/null | sort -k2 > SHA256SUMS
	rm -f SHA256SUMS.whole SHA256SUMS.parts
	sha256sum -c --ignore-missing SHA256SUMS >/dev/null
)
echo "release-assets: $out/SHA256SUMS lists $(wc -l < "$out/SHA256SUMS") files. Sign it:"
echo "  gpg --local-user <the KEYS fingerprint> --armor --detach-sign $out/SHA256SUMS"
