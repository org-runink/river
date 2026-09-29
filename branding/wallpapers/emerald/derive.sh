#!/bin/sh
# derive.sh — regenerate the vendored CachyOS Emerald wallpapers from the upstream source.
#
#   branding/wallpapers/emerald/derive.sh <upstream-wallpapers-dir>
#
# <upstream-wallpapers-dir> is the `wallpapers/` directory of CachyOS-Emerald-KDE at the
# pinned commit below, e.g. from
#   https://github.com/CachyOS/CachyOS-Emerald-KDE/archive/d61d1dd01c1b186f9732c586fe238b8e25da02c2.tar.gz
# That commit is no longer on any upstream branch: upstream deleted the wallpapers in
# cbe7396 (2022-12-29) and rewrote history, but GitHub still serves the commit by SHA. It
# is the exact revision CachyOS packaged as cachyos-emerald-kde-theme-git r25.d61d1dd.
#
# This script does not fetch anything. It refuses to run unless every upstream file matches
# UPSTREAM.sha256, then writes, per wallpaper, ONE image (the largest upstream raster,
# downscaled only, to at most 3840x2160) as a stripped progressive JPEG, plus a Plasma
# metadata.json, and regenerates SHA256SUMS. The transform is lossy on purpose (the
# upstream PNGs are 41 MB; the budget for the set is 8 MB). JPEG output is not bit-for-bit
# reproducible across ImageMagick/libjpeg versions, so SHA256SUMS records what was
# committed, and UPSTREAM.sha256 is what makes the provenance verifiable.
#
# License: GPL-3.0 (every upstream metadata.desktop says X-KDE-PluginInfo-License=GPLv3;
# the Arch/CachyOS package declares GPL3). The derived JPEGs are a modified version of that
# work and stay GPL-3.0; see REUSE.toml and LICENSES/GPL-3.0-only.txt.
set -eu

HERE="$(cd "$(dirname "$0")" && pwd)"
UP="${1:?usage: derive.sh <upstream-wallpapers-dir>}"
QUALITY_DEFAULT=85

command -v magick >/dev/null 2>&1 || { echo "derive: need ImageMagick 7 (magick)" >&2; exit 1; }

# Verify the pin before touching anything.
(cd "$UP" && sha256sum --quiet --strict -c "$HERE/UPSTREAM.sha256") || {
	echo "derive: upstream files do not match UPSTREAM.sha256 — wrong commit or tampered tree" >&2
	exit 1
}

# id|display name|upstream source raster|jpeg quality|max geometry (downscale only)
# GreenFeathers and OrangeFeathers never had a full-size image upstream: their only raster
# is the 960x540 preview (GreenFeathers' is misspelt "screenhot.png"), so that is what
# ships. Several upstream files are also named for a resolution they do not have (e.g.
# Abstract's "3840x2160.png" is 960x540); the derived file is named for its REAL size,
# because Plasma picks an image by the size in its file name.
#
# Quality/size exceptions to the q85 / 3840x2160 default, each forced by a budget
# (river lint branding-sync: no asset over 1 MiB, the workstation set under 10 MiB):
#   Dimensions, Spectrum  q90 — smooth full-screen gradients; q85 showed faint blocking.
#   Skyscraper            q78 — fine detail; q85 at 4K was 1.08 MiB.
#   Lines                 2560x1440 q82 — 56k-colour line art; even q72 at 4K was 1.39 MiB.
#   Liquid                2560x1440 q88 — smooth marbling, so resolution was cut, not quality.
TABLE='Abstract|Abstract|Abstract/contents/images/3840x2160.png|85|3840x2160
BlueFeathers|Blue Feathers|BlueFeathers/contents/images/3840x2160.png|85|3840x2160
DarkStreaks|Dark Streaks|DarkStreaks/contents/images/3840x2160.png|85|3840x2160
Dimensions|Dimensions|Dimensions/contents/images/3840x2160.png|90|3840x2160
GreenFeathers|Green Feathers|GreenFeathers/contents/screenhot.png|85|3840x2160
Lines|Lines|Lines/contents/images/3840x2160.png|82|2560x1440
Liquid|Liquid|Liquid/contents/images/3840x2160.png|88|2560x1440
Metal|Metal|Metal/contents/images/3840x2160.png|85|3840x2160
OrangeFeathers|Orange Feathers|OrangeFeathers/contents/screenshot.png|85|3840x2160
paper|Paper|paper/contents/images/3840x2160.png|85|3840x2160
PurpleFeathers|Purple Feathers|PurpleFeathers/contents/images/feathers7680x4320.png|85|3840x2160
Skyscraper|Skyscrapers|Skyscraper/contents/images/3840x2160.png|78|3840x2160
Spectrum|Spectrum|Spectrum/contents/images/3840x2160.png|90|3840x2160'

printf '%s\n' "$TABLE" | while IFS='|' read -r id name src q geom; do
	[ -n "$id" ] || continue
	q="${q:-$QUALITY_DEFAULT}"
	out="$HERE/$id"
	rm -rf "$out"
	mkdir -p "$out/contents/images"
	# Flatten: a few upstream edge pixels are not fully opaque (mean alpha > 0.9997).
	magick "$UP/$src" -background black -alpha remove -alpha off -strip \
		-resize "${geom:-3840x2160}>" -sampling-factor 4:2:0 -interlace JPEG -quality "$q" \
		"$out/contents/images/tmp.jpg"
	wh="$(magick identify -format '%wx%h' "$out/contents/images/tmp.jpg")"
	mv "$out/contents/images/tmp.jpg" "$out/contents/images/$wh.jpg"
	cat > "$out/metadata.json" <<-JSON
		{
		    "KPlugin": {
		        "Authors": [
		            {
		                "Email": "rkstrdee@gmail.com",
		                "Name": "Dharam Dhurandhar"
		            }
		        ],
		        "Id": "$id",
		        "License": "GPL-3.0",
		        "Name": "$name",
		        "Description": "CachyOS Emerald wallpaper (GPL-3.0), JPEG derivative vendored by RIVER"
		    }
		}
	JSON
	printf 'derive: %-15s <- %-52s q%s %s %6s KiB\n' "$id" "$src" "$q" "$wh" \
		"$(($(wc -c < "$out/contents/images/$wh.jpg") / 1024))"
done

# PROVENANCE: one line per wallpaper, upstream raster -> derived file, both sha256s.
{
	echo "# CachyOS Emerald KDE wallpapers, vendored into RIVER as JPEG derivatives. Written by derive.sh."
	echo "# upstream: https://github.com/CachyOS/CachyOS-Emerald-KDE @ d61d1dd01c1b186f9732c586fe238b8e25da02c2"
	echo "#   (tree path wallpapers/; commit off-branch since upstream cbe7396, still served by SHA;"
	echo "#    == CachyOS package cachyos-emerald-kde-theme-git r25.d61d1dd, byte-identical)"
	echo "# author: Dharam Dhurandhar <rkstrdee@gmail.com>; license: GPL-3.0 (metadata.desktop: GPLv3)"
	echo "# transform: alpha flattened onto black, metadata stripped, downscale-only to the listed"
	echo "#   geometry, JPEG 4:2:0 progressive at the listed quality; metadata.desktop -> metadata.json;"
	echo "#   upstream screenshots dropped (Plasma renders its own preview)."
	echo "# id | upstream path | upstream sha256 | quality/geometry | derived path | derived sha256"
	printf '%s\n' "$TABLE" | while IFS='|' read -r id _ src q geom; do
		d="$(cd "$HERE" && ls "$id"/contents/images/*.jpg)"
		printf '%s | %s | %s | q%s/%s | %s | %s\n' "$id" "wallpapers/$src" \
			"$(sha256sum "$UP/$src" | cut -d' ' -f1)" "$q" "$geom" "$d" \
			"$(sha256sum "$HERE/$d" | cut -d' ' -f1)"
	done
} > "$HERE/PROVENANCE"

(cd "$HERE" && find . -mindepth 2 -type f \( -name '*.jpg' -o -name metadata.json \) | sed 's|^\./||' | LC_ALL=C sort | xargs sha256sum > SHA256SUMS)
echo "derive: total $(du -cb "$HERE"/*/contents/images/*.jpg | tail -1 | cut -f1) bytes; SHA256SUMS rewritten"
