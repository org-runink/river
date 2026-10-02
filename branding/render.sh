#!/bin/sh
# branding/render.sh — regenerate EVERY Runink River render from its source, then copy each
# asset to where the profile (iso-profiles/river) expects it.
#
# The SVGs are the source of truth; the PNGs and JPEGs are build products that are committed
# only because buildiso copies overlays verbatim and has no render step (and must not grow
# one: the ISO build container is not where artwork should be made). Edit a source, run this,
# commit both.
#
#   branding/render.sh            render + copy everything
#   branding/render.sh --check    only verify that every shipped copy matches its
#                                 branding/ original (same as river lint branding-sync)
#   branding/render.sh --outline  re-outline the lockup and the tagline (branding/src/logo/,
#                                 live text) into branding/logo/*.svg paths. Needs inkscape,
#                                 woff2_decompress and RIVER_FONT_DIR, a directory holding
#                                 Figtree-var.woff2 and PlayfairDisplay-Italic-var.woff2
#                                 (both OFL-1.1, Google Fonts). Run it only when a lockup
#                                 source changes; the plain render never needs a font.
#
# Renderer: rsvg-convert if present, else inkscape, else ImageMagick (magick/convert).
# Optimiser (optional): oxipng, else optipng; otherwise PNGs are written as rendered.
# Every PNG must stay under MAX_PNG_BYTES (default 1.5 MB) or this script fails.
# ImageMagick 7 (`magick`) is REQUIRED for the JPEG outputs (the GRUB background, the
# wallpaper) and the icons; the size budgets themselves are enforced by
# river lint branding-sync.
#
# EVERY visual surface carries the Runink River community mark, the M2 mascot: the
# brown-and-white puppy grinning over the front of a three-log raft on the water. Its one
# source is branding/mascot/river-mascot.svg; this script generates the mark files from it
# (branding/logo/river-mark.svg, river-mark-small.svg) and everything else from those, set on
# the dark palette (branding/palette.md); on dark grounds beside the light wordmark of
# logo/river-lockup-dark.svg: GRUB (live and installed), Plymouth, the Plasma start-up
# splash and the SDDM greeter theme `runink-river` (both with the mark ANIMATED: its layers,
# generated below from river-mark.svg, and src/qml/RiverLockup.qml), the wallpaper, the
# Kickoff button, the hicolor icon `runink-river` (os-release LOGO=, the installer's edition
# icon), the favicons, the terminal greeting (fastfetch) and, in ASCII
# (ascii/river-mark-small.txt), /etc/issue.
set -eu

cd "$(dirname "$0")/.."
ROOT="$(pwd)"
B="$ROOT/branding"
WS="$ROOT/iso-profiles/river/root-overlay"
MAX_PNG_BYTES="${MAX_PNG_BYTES:-1572864}"

if [ "${1:-}" = "--check" ]; then
	cd "$ROOT/cli" && exec go run ./cmd/river lint branding-sync --repo "$ROOT"
fi

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT INT TERM

# art <in.svg> <out.svg> — expand each placeholder
#   <!-- RIVER-ART src=logo/X.svg x=N y=N height=N [anchor=start|middle|end] [recolor=#A:#B,#C:#D] -->
# into a <g transform> holding the body of branding/<src>, scaled so its viewBox is `height`
# tall, placed with its left edge (anchor=start, the default), centre or right edge at x,
# with each colour A replaced by B. A <g>, never a nested <svg>: Qt's SVG renderer (SDDM, the
# icon theme) is SVG Tiny 1.2, which has no nested <svg>. So every surface draws the logo
# from its one source file.
art() {
	awk -v B="$B" '
	function slurp(f,   l, s) { s = ""; while ((getline l < f) > 0) s = s l "\n"; close(f); return s }
	/<!-- RIVER-ART / {
		delete v
		n = split($0, f, /[ ]+/)
		for (i = 1; i <= n; i++) if (index(f[i], "=")) { k = substr(f[i], 1, index(f[i], "=") - 1); v[k] = substr(f[i], index(f[i], "=") + 1) }
		src = B "/" v["src"]
		s = slurp(src)
		if (s == "") { print "render: art: cannot read " src > "/dev/stderr"; exit 1 }
		o = index(s, "<svg"); s = substr(s, o)
		e = index(s, ">"); tag = substr(s, 1, e); body = substr(s, e + 1)
		c = 0; for (p = length(body); p > 0; p--) if (substr(body, p, 6) == "</svg>") { c = p; break }
		body = substr(body, 1, c - 1)
		vb = tag; sub(/.*viewBox="/, "", vb); sub(/".*/, "", vb)
		split(vb, m, /[ ,]+/)
		sc = v["height"] / m[4]; w = m[3] * sc
		x = v["x"]; if (v["anchor"] == "middle") x -= w / 2; else if (v["anchor"] == "end") x -= w
		if (v["recolor"] != "") {
			nr = split(v["recolor"], pairs, ",")
			for (i = 1; i <= nr; i++) {
				split(pairs[i], ab, ":")
				gsub(tolower(ab[1]), ab[2], body); gsub(toupper(ab[1]), ab[2], body)
			}
		}
		printf "<g transform=\"translate(%.3f %.3f) scale(%.6f)\">%s</g>\n", x - m[1] * sc, v["y"] - m[2] * sc, sc, body
		next
	}
	{ print }
	' "$1" > "$2"
	if grep -q '<!-- RIVER-ART ' "$2"; then echo "render: art: unexpanded placeholder in $1" >&2; exit 1; fi
}

# ── the community mark: the M2 mascot (branding/mascot/river-mascot.svg) ─────────────────
# The mascot is the ONE source of the mark: the brown-and-white puppy grinning over the front of
# a three-log raft on the water. Both mark files are GENERATED from it, before anything else
# (the outline mode draws the mark into the dark lockup too):
#   logo/river-mark.svg        the mascot's art, one top-level element per line, on the
#                              512-unit square MARK_VIEWBOX: the whole drawing, ears and
#                              water included (the mascot file's own viewBox is a tighter
#                              bust crop that clips an ear tip and the water). 32 px and up.
#   logo/river-mark-small.svg  the same art, cropped to the head (SMALL_VIEWBOX), for the
#                              16-24 px icons and favicons, where the whole puppy on its
#                              raft closes up into a smudge. Every element is still drawn;
#                              the crop only frames the face.
# Its ids are prefixed river-m2- (the lockups and wallpapers embed the mark in bigger SVGs).
# Each generated file records the mascot's sha256, which river lint branding-sync checks.
MASCOT="$B/mascot/river-mascot.svg"
MARK_VIEWBOX="0 0 512 512"
SMALL_VIEWBOX="95 20 280 280"
# mark_elements <mascot.svg> <out> — the mascot's drawing elements, one top-level element
# per line (after its <title>), ids prefixed
mark_elements() {
	LC_ALL=C awk '
	{ s = s $0 "\n" }
	END {
		t = index(s, "</title>"); if (!t) { print "render: mark: no <title> in the mascot" > "/dev/stderr"; exit 1 }
		s = substr(s, t + 8)
		c = 0; for (p = length(s); p > 0; p--) if (substr(s, p, 6) == "</svg>") { c = p; break }
		if (!c) { print "render: mark: no </svg> in the mascot" > "/dev/stderr"; exit 1 }
		s = substr(s, 1, c - 1); gsub(/\n/, " ", s)
		# ids -> river-m2-<id>, and every reference to them
		r = s; n = 0
		while (match(r, /id="[^"]*"/)) { ids[++n] = substr(r, RSTART + 4, RLENGTH - 5); r = substr(r, RSTART + RLENGTH) }
		for (i = 1; i <= n; i++) {
			gsub("id=\"" ids[i] "\"", "id=\"river-m2-" ids[i] "\"", s)
			gsub("url\\(#" ids[i] "\\)", "url(#river-m2-" ids[i] ")", s)
		}
		d = 0; cur = ""
		while (match(s, /<[^>]*>/)) {
			pre = substr(s, 1, RSTART - 1); tag = substr(s, RSTART, RLENGTH); s = substr(s, RSTART + RLENGTH)
			if (d == 0 && pre ~ /[^ ]/) { print "render: mark: text outside an element: " pre > "/dev/stderr"; exit 1 }
			cur = cur (d > 0 ? pre : "") tag
			if (tag ~ /^<!--/) { if (d == 0) cur = ""; continue }
			if (tag ~ /^<\//) d--; else if (tag !~ /\/>$/) d++
			if (d < 0) { print "render: mark: unbalanced mascot markup" > "/dev/stderr"; exit 1 }
			if (d == 0) { print " " cur; cur = "" }
		}
		if (d != 0) { print "render: mark: unbalanced mascot markup" > "/dev/stderr"; exit 1 }
	}' "$1" > "$2"
	[ "$(wc -l < "$2")" -gt 10 ] || { echo "render: mark: $1 split into too few elements" >&2; exit 1; }
}
# mark_file <viewBox> <what> <elements> <out>
mark_file() {
	{
		printf '<svg viewBox="%s" xmlns="http://www.w3.org/2000/svg">\n' "$1"
		printf ' <!-- SPDX-FileCopyrightText: 2026 Runink\n      SPDX-License-Identifier: LicenseRef-Runink-Trademark\n'
		printf '      Runink River community mark, %s: the M2 mascot, a brown-and-white puppy\n' "$2"
		printf '      grinning over the front of a three-log raft on the water. GENERATED by\n'
		printf '      branding/render.sh from branding/mascot/river-mascot.svg, whose sha256 is\n'
		printf '      mascot-sha256 %s\n' "$(sha256sum "$MASCOT" | cut -d' ' -f1)"
		printf '      Edit the mascot and re-run; never edit this file. -->\n'
		printf ' <title>Runink River</title>\n'
		cat "$3"
		printf '</svg>\n'
	} > "$4"
}
mkdir -p "$TMP/m2"
mark_elements "$MASCOT" "$TMP/m2/elements"
mark_file "$MARK_VIEWBOX" "primary (32 px and up)" "$TMP/m2/elements" "$B/logo/river-mark.svg"
mark_file "$SMALL_VIEWBOX" "small (the head, for 16-24 px)" "$TMP/m2/elements" "$B/logo/river-mark-small.svg"

# ── --outline: live-text lockup sources -> outlined branding/logo/*.svg ─────────────────────
if [ "${1:-}" = "--outline" ]; then
	for t in inkscape woff2_decompress fc-match; do
		command -v "$t" >/dev/null 2>&1 || { echo "render: --outline needs $t" >&2; exit 1; }
	done
	: "${RIVER_FONT_DIR:?render: --outline needs RIVER_FONT_DIR (Figtree-var.woff2, PlayfairDisplay-Italic-var.woff2)}"
	mkdir -p "$TMP/fonts"
	for f in Figtree-var.woff2 PlayfairDisplay-Italic-var.woff2; do
		[ -f "$RIVER_FONT_DIR/$f" ] || { echo "render: --outline: $RIVER_FONT_DIR/$f missing" >&2; exit 1; }
		cp "$RIVER_FONT_DIR/$f" "$TMP/fonts/"
		# Pango does not read WOFF2 through fontconfig; the decompressed TTF it does.
		(cd "$TMP/fonts" && woff2_decompress "$f" >/dev/null && rm -f "$f")
	done
	cat > "$TMP/fonts.conf" <<EOF
<?xml version="1.0"?>
<!DOCTYPE fontconfig SYSTEM "fonts.dtd">
<fontconfig><include ignore_missing="yes">/etc/fonts/fonts.conf</include><dir>$TMP/fonts</dir><cachedir>$TMP/fc-cache</cachedir></fontconfig>
EOF
	export FONTCONFIG_FILE="$TMP/fonts.conf"
	for q in 'Figtree:bold' 'Playfair Display:italic'; do
		case "$(fc-match -f '%{file}' "$q")" in
		"$TMP"/*) ;;
		*) echo "render: --outline: fontconfig does not resolve $q to RIVER_FONT_DIR" >&2; exit 1 ;;
		esac
	done
	for n in river-lockup-dark runink-tagline; do
		art "$B/src/logo/$n.svg" "$TMP/$n.svg"
		inkscape "$TMP/$n.svg" --export-text-to-path --export-plain-svg --export-area-drawing \
			--export-filename="$TMP/$n.out.svg" 2>/dev/null
		if grep -q '<text' "$TMP/$n.out.svg"; then echo "render: --outline: text left in $n" >&2; exit 1; fi
		# a generated header, then Inkscape's SVG without its XML declaration, the copied
		# source comment and its ids
		{
			printf '<!-- SPDX-FileCopyrightText: 2026 Runink\n     SPDX-License-Identifier: LicenseRef-Runink-Trademark\n'
			printf '     GENERATED by the outline mode of branding/render.sh from branding/src/logo/%s.svg\n' "$n"
			printf '     (text outlined to paths). Edit the source and re-run; never edit this file. -->\n'
			sed -n '/^<svg/,$p' "$TMP/$n.out.svg" | sed -e '/^ *id="river-/!s/^\( *\)id="[^"]*"$/\1/' -e '/^ *$/d'
		} > "$B/logo/$n.svg"
		printf 'render: %-78s %5s KiB\n' "branding/logo/$n.svg" "$(($(wc -c < "$B/logo/$n.svg") / 1024))"
	done
	echo "render: outlined — now run branding/render.sh to re-render every surface"
	exit 0
fi

command -v magick >/dev/null 2>&1 || { echo "render: need ImageMagick 7 (magick)" >&2; exit 1; }

# ── tools ──────────────────────────────────────────────────────────────────────────────
if command -v rsvg-convert >/dev/null 2>&1; then
	RENDERER=rsvg
elif command -v inkscape >/dev/null 2>&1; then
	RENDERER=inkscape
elif command -v magick >/dev/null 2>&1; then
	RENDERER=magick
elif command -v convert >/dev/null 2>&1; then
	RENDERER=convert
else
	echo "render: need rsvg-convert, inkscape or ImageMagick" >&2
	exit 1
fi
echo "render: using $RENDERER"

# pngopt <png> — lossless optimise in place, when an optimiser is installed.
pngopt() {
	if command -v oxipng >/dev/null 2>&1; then
		oxipng -q -o 4 --strip safe "$1"
	elif command -v optipng >/dev/null 2>&1; then
		optipng -quiet -o2 "$1"
	fi
}

# svg2png <in.svg> <out.png> <width> <height>
svg2png() {
	mkdir -p "$(dirname "$2")"
	case "$RENDERER" in
	rsvg) rsvg-convert -w "$3" -h "$4" "$1" -o "$2" ;;
	inkscape) inkscape "$1" --export-type=png --export-filename="$2" -w "$3" -h "$4" >/dev/null 2>&1 ;;
	magick) magick -background none -density 192 "$1" -resize "$3x$4!" "$2" ;;
	convert) convert -background none -density 192 "$1" -resize "$3x$4!" "$2" ;;
	esac
	pngopt "$2"
	bytes=$(wc -c < "$2")
	if [ "$bytes" -gt "$MAX_PNG_BYTES" ]; then
		echo "render: $2 is $bytes bytes (> $MAX_PNG_BYTES)" >&2
		exit 1
	fi
	printf 'render: %-78s %5s KiB\n' "${2#"$ROOT"/}" "$((bytes / 1024))"
}

# svg2jpg <in.svg> <out.jpg> <width> <height> <quality> — render through a temp PNG, then
# a stripped progressive JPEG. 4:4:4 chroma: the art is thin strokes on a dark gradient,
# and 4:2:0 smears the one sage strand.
svg2jpg() {
	svg2png "$1" "$TMP/svg2jpg.png" "$3" "$4" >/dev/null
	mkdir -p "$(dirname "$2")"
	magick "$TMP/svg2jpg.png" -strip -sampling-factor 4:4:4 -interlace JPEG -quality "$5" "$2"
	printf 'render: %-78s %5s KiB\n' "${2#"$ROOT"/}" "$(($(wc -c < "$2") / 1024))"
}

# ship <src-dir> <dest-dir> — replace dest with an exact copy of src.
ship() {
	rm -rf "$2"
	mkdir -p "$(dirname "$2")"
	cp -R "$1" "$2"
}

# ── the light lockup (documents): the mark beside the ink wordmark, already outlined ─────
art "$B/src/logo/river-lockup.svg" "$B/logo/river-lockup.svg"
printf 'render: %-78s %5s KiB\n' "branding/logo/river-lockup.svg" "$(($(wc -c < "$B/logo/river-lockup.svg") / 1024))"

# ── GRUB theme (the live ISO menu and the installed system) ────────────────────────────
# One theme, at the path the installer step reads (usr/share/runink/branding/grub/river,
# installer/lib/40-boot-grub-zfs.sh) and the one scripts/patch-artools.go copies onto the live
# ISO. The background is BASELINE JPEG, 4:2:0 (GRUB's jpeg reader reads neither progressive
# JPEG nor every sampling layout).
G="$B/grub/river"
art "$B/src/grub/background.svg" "$TMP/grub-bg.svg"
svg2png "$TMP/grub-bg.svg" "$TMP/grub-bg.png" 1920 1080 >/dev/null
magick "$TMP/grub-bg.png" -strip -interlace none -sampling-factor 4:2:0 -quality 88 "$G/background.jpg"
printf 'render: %-78s %5s KiB\n' "branding/grub/river/background.jpg" "$(($(wc -c < "$G/background.jpg") / 1024))"
for s in select_w:10:36 select_c:4:36 select_e:10:36 \
	progress_bar_w:3:6 progress_bar_c:4:6 progress_bar_e:3:6 \
	progress_highlight_w:3:6 progress_highlight_c:4:6 progress_highlight_e:3:6; do
	n=${s%%:*}
	wh=${s#*:}
	svg2png "$B/src/grub/$n.svg" "$G/$n.png" "${wh%:*}" "${wh#*:}"
done
ship "$G" "$WS/usr/share/runink/branding/grub/river"

# ── Plymouth theme ───────────────────────────────────────────────────────────────────────
# logo.png: the dark-ground lockup (logo/river-lockup-dark.svg, via src/plymouth/logo.svg);
# river.script scales it to at most 42% of the screen width.
P="$B/plymouth/river"
art "$B/src/plymouth/logo.svg" "$TMP/splash-logo.svg"
svg2png "$TMP/splash-logo.svg" "$P/logo.png" 1260 280
for i in 0 1 2; do
	svg2png "$B/src/plymouth/stream$i.svg" "$P/stream$i.png" 1600 80
done
ship "$P" "$WS/usr/share/plymouth/themes/river"

# ── the animated community mark (the SDDM greeter and the Plasma splash) ─────────────────
# The mark is split into five LAYERS, generated here from branding/logo/river-mark.svg (never
# hand-drawn), in the mark's own drawing order: back (the raft's back logs and the puppy's
# body, legs and chest), head (the puppy's head), front (the log ends and the front paws on
# them), water (the water band) and waves (the wave lines and the two leaves on the water).
# back, head and front ride the raft together (the head nods a little on top of that), the
# water stays put and the waves drift. The split is by position, keyed on the two elements
# that start a layer: the head is the mark's one top-level <g transform="matrix(...)">, and the
# water is its one fill="#9ed8d2" element. The gradient <defs> go into every layer. The script
# checks that the layers stacked in order ARE the mark's elements, byte for byte; each layer
# keeps the mark's viewBox (MARK_VIEWBOX, 512 units), so the QML stacks them at the same size
# with no offsets. The wordmark is branding/logo/river-lockup-dark.svg without its mark (same
# viewBox), and LockupGeometry.qml records where the mark sits in the lockup, read from the
# same file. branding/src/qml/RiverLockup.qml animates the layers (QtQuick only; phase from
# the wall clock, so the greeter and the splash bob in step).
# RiverLockup.qml places and animates the layers in 512 mark units from the origin.
[ "$MARK_VIEWBOX" = "0 0 512 512" ] || { echo "render: mark layers: RiverLockup.qml assumes MARK_VIEWBOX 0 0 512 512" >&2; exit 1; }
MARK_SRC="$B/logo/river-mark.svg"
LOCKUP_SRC="$B/logo/river-lockup-dark.svg"
ML="$TMP/mark"
LAYERS="back head front water waves"
mkdir -p "$ML"
# layer <name> — the generated layer file for <name>, from the element lines in $ML/<name>.body
layer_svg() {
	{
		printf '<svg xmlns="http://www.w3.org/2000/svg" viewBox="%s">\n' "$MARK_VIEWBOX"
		printf ' <!-- SPDX-FileCopyrightText: 2026 Runink\n      SPDX-License-Identifier: LicenseRef-Runink-Trademark\n'
		printf '      GENERATED by branding/render.sh: the %s layer of branding/logo/river-mark.svg\n' "$1"
		printf '      (the animated mark, branding/src/qml/RiverLockup.qml). Never edit this file. -->\n'
		cat "$ML/defs.body" "$ML/$1.body"
		printf '</svg>\n'
	} > "$ML/mark-$1.svg"
}
: > "$ML/defs.body"
awk -v D="$ML" '
	function put(n) { print > (D "/" n ".body"); print > (D "/all.body") }
	BEGIN { layer = "back" }
	/^ *<svg/ || /^ *<\/svg>/ || /^ *<\?xml/ || /^ *<title>/ { next }
	/^ *<!--/ { inc = !index($0, "-->"); next }
	inc { if (index($0, "-->")) inc = 0; next }
	/^ *<defs>/ { print > (D "/defs.body"); next }
	/^ *<g transform="matrix\(/ { if (layer != "back") bad = 1; put("head"); layer = "front"; heads++; next }
	/fill="#9ed8d2"/ { if (layer != "front") bad = 1; put("water"); layer = "waves"; waters++; next }
	/^ *<[a-z]/ { put(layer); next }
	/[^ ]/ { print "render: mark layers: unclassified line in river-mark.svg: " $0 > "/dev/stderr"; bad = 1 }
	END {
		if (heads != 1 || waters != 1) { print "render: mark layers: want one head group and one water element, got " heads + 0 " and " waters + 0 > "/dev/stderr"; bad = 1 }
		exit bad
	}
' "$MARK_SRC" || { echo "render: mark layers: river-mark.svg no longer has the shape the animation splits" >&2; exit 1; }
stack=""
for n in $LAYERS; do
	[ -s "$ML/$n.body" ] || { echo "render: mark layers: layer $n is empty (river-mark.svg changed?)" >&2; exit 1; }
	stack="$stack $ML/$n.body"
done
# the layers, stacked in order, are exactly the mark's drawing elements
# shellcheck disable=SC2086
cat $stack > "$ML/stack.body"
cmp -s "$ML/stack.body" "$ML/all.body" || {
	echo "render: mark layers: the stacked layers do not reproduce river-mark.svg (element order changed?)" >&2; exit 1; }
for n in $LAYERS; do layer_svg "$n"; done

# The wordmark: the lockup without its mark (the first top-level <g>), same viewBox.
awk '
	!done && /^  <g$/ { skip = 1; next }
	skip { if ($0 ~ /^  <\/g>$/) { skip = 0; done = 1 } ; next }
	{ print }
	END { if (!done) exit 1 }
' "$LOCKUP_SRC" > "$ML/wordmark-dark.svg" || { echo "render: wordmark: no mark group in $LOCKUP_SRC" >&2; exit 1; }
if grep -qi -e '#fdf5e6' -e '#1d2b3a' "$ML/wordmark-dark.svg" || ! grep -q 'aria-label="Runink River"' "$ML/wordmark-dark.svg"; then
	echo "render: wordmark: $LOCKUP_SRC did not split into mark + wordmark" >&2; exit 1
fi
sed -i 's/GENERATED by the outline mode of branding\/render.sh from branding\/src\/logo\/river-lockup-dark.svg/GENERATED by branding\/render.sh: branding\/logo\/river-lockup-dark.svg without its mark/' "$ML/wordmark-dark.svg"
# where the mark sits: the lockup's viewBox and the mark group's matrix(s,0,0,s,x,y)
vb="$(sed -n 's/^ *viewBox="0 0 \([0-9.]*\) \([0-9.]*\)".*/\1 \2/p' "$LOCKUP_SRC" | head -1)"
mx="$(sed -n 's/^ *transform="matrix(\([-0-9.]*\),0,0,[-0-9.]*,\([-0-9.]*\),\([-0-9.]*\))"$/\1 \2 \3/p' "$LOCKUP_SRC" | head -1)"
[ -n "$vb" ] && [ -n "$mx" ] || { echo "render: lockup geometry: cannot read viewBox/matrix of $LOCKUP_SRC" >&2; exit 1; }
# shellcheck disable=SC2086
set -- $vb $mx
cat > "$ML/LockupGeometry.qml" <<QML
// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT
//
// GENERATED by branding/render.sh from branding/logo/river-lockup-dark.svg: its viewBox, and
// where the community mark (its MARK_VIEWBOX, 512 units) sits in it. Never edit this file.
import QtQml

QtObject {
    readonly property real viewWidth: $1
    readonly property real viewHeight: $2
    readonly property real markScale: $3
    readonly property real markX: $4
    readonly property real markY: $5
}
QML
set --

# mark_into <dir> — the animated lockup (component + images) into a QML surface directory
mark_into() {
	mkdir -p "$1/images"
	cp "$B/src/qml/RiverLockup.qml" "$ML/LockupGeometry.qml" "$1/"
	cp "$ML"/mark-*.svg "$ML/wordmark-dark.svg" "$1/images/"
}

# ── Plasma start-up splash (ksplash) ─────────────────────────────────────────────────────
# The look-and-feel package's contents/splash/: Splash.qml (hand-written,
# branding/src/splash/Splash.qml) draws the animated lockup where the SDDM greeter leaves it
# after a login, so the hand-off from the greeter to the session is continuous.
KS="$B/splash"
rm -rf "$KS"
mkdir -p "$KS"
cp "$B/src/splash/Splash.qml" "$KS/"
mark_into "$KS"
ship "$KS" "$WS/usr/share/plasma/look-and-feel/org.runink.river.desktop/contents/splash"

# ── SDDM greeter theme `runink-river` (etc/sddm.conf.d/10-river.conf) ─────────────────────
# The hand-written theme (branding/src/sddm/runink-river/: Main.qml, its components,
# metadata.desktop, theme.conf), the animated lockup, and background.jpg: the dark
# wallpaper's river lines without its lockup and tagline (the greeter draws the lockup
# itself), one 2560x1440 JPEG, scaled to any screen.
SD="$B/sddm/runink-river"
rm -rf "$SD"
mkdir -p "$SD"
cp -R "$B/src/sddm/runink-river/." "$SD/"
mark_into "$SD"
grep -v 'RIVER-ART ' "$B/src/wallpaper/river-dark.svg" > "$TMP/sddm-bg.svg"
svg2jpg "$TMP/sddm-bg.svg" "$SD/background.jpg" 2560 1440 88
ship "$SD" "$WS/usr/share/sddm/themes/runink-river"

# ── wallpaper ──────────────────────────────────────────────────────────────────────────────
# One KDE wallpaper package, two variants: Plasma picks contents/images_dark/ when the colour
# scheme is dark and contents/images/ otherwise. ONE resolution per variant, 3840x2160 (Plasma
# scales it to any screen; the art is vector and survives the downscale). JPEG q88 4:4:4.
# Runink River stays the default wallpaper through the look-and-feel package's
# contents/defaults.
WPB="$B/wallpapers/River"
WP="$WPB/contents"
rm -rf "$WPB"
mkdir -p "$WP"
art "$B/src/wallpaper/river-light.svg" "$TMP/wp-light.svg"
art "$B/src/wallpaper/river-dark.svg" "$TMP/wp-dark.svg"
svg2jpg "$TMP/wp-light.svg" "$WP/images/3840x2160.jpg" 3840 2160 88
svg2jpg "$TMP/wp-dark.svg" "$WP/images_dark/3840x2160.jpg" 3840 2160 88
# The preview stays PNG: Plasma's wallpaper package structure only looks for screenshot.png.
svg2png "$TMP/wp-dark.svg" "$WP/screenshot.png" 400 225
cat > "$WPB/metadata.json" <<'JSON'
{
    "KPlugin": {
        "Authors": [
            {
                "Name": "Runink"
            }
        ],
        "Id": "River",
        "License": "CC-BY-4.0",
        "Name": "Runink River",
        "Description": "Runink River: river lines and the community mark, on the dark and light grounds"
    }
}
JSON
ship "$WPB" "$WS/usr/share/wallpapers/River"

# CachyOS Emerald (GPL-3.0, third party). The JPEGs under branding/wallpapers/emerald are
# derived from the pinned upstream by branding/wallpapers/emerald/derive.sh (which needs
# the upstream tree, so it is NOT run here); this only verifies them and ships them.
EM="$B/wallpapers/emerald"
(cd "$EM" && sha256sum --quiet --strict -c SHA256SUMS) || {
	echo "render: branding/wallpapers/emerald does not match its SHA256SUMS — re-run derive.sh" >&2
	exit 1
}
for d in "$EM"/*/; do
	d="${d%/}"
	ship "$d" "$WS/usr/share/wallpapers/$(basename "$d")"
done

# icon <name> <svg> <px> <outdir> — square transparent PNG, rendered at 4x and downscaled
# (sharper small sizes).
icon() {
	out="$4/${3}x${3}/apps/$1.png"
	mkdir -p "$(dirname "$out")"
	svg2png "$2" "$TMP/icon-big.png" $(($3 * 4)) $(($3 * 4)) >/dev/null
	magick "$TMP/icon-big.png" -resize "${3}x${3}" -strip "$out"
	pngopt "$out"
	printf 'render: %-78s %5s B\n' "${out#"$ROOT"/}" "$(wc -c < "$out")"
}
MARK="$B/logo/river-mark.svg"
SMALL="$B/logo/river-mark-small.svg"

# ── hicolor icon `runink-river` (os-release LOGO=, Info Center, the Kickoff button,
#    the installer's edition icon) ──────────────────────────────────────────────────────────
# scalable = the mark (river-mark.svg: the whole puppy on its raft, transparent; its dark
# outline and cream and brown fills read on dark panels and light ones). The small mark
# (river-mark-small.svg: the same art cropped to the head) at 24 px and below, where the
# whole puppy closes up into a smudge; the whole mark from 32 px.
IC="$B/icons/hicolor"
rm -rf "$IC"
mkdir -p "$IC/scalable/apps"
cp "$MARK" "$IC/scalable/apps/runink-river.svg"
for px in 16 22 24; do icon runink-river "$SMALL" "$px" "$IC"; done
for px in 32 48 64 128 256; do icon runink-river "$MARK" "$px" "$IC"; done
ship "$IC" "$WS/usr/share/icons/hicolor"

# ── Favicons (not shipped into an overlay from here) ─────────────────────────────────────
# For any web page the image serves, first of all the graphical installer's wizard UI
# (installer/internal/wizard/web/): copy branding/icons/favicon/* next to its index.html and
# link them there. 16 px (and the SVG, which browsers draw at 16) are the small mark, the
# puppy's head; 32 and 48 px the whole mark.
FV="$B/icons/favicon"
rm -rf "$FV"
mkdir -p "$FV"
for px in 16 32 48; do
	if [ "$px" -le 24 ]; then src="$SMALL"; else src="$MARK"; fi
	svg2png "$src" "$TMP/fav-big.png" $((px * 4)) $((px * 4)) >/dev/null
	magick "$TMP/fav-big.png" -resize "${px}x${px}" -strip "$FV/favicon-$px.png"
	pngopt "$FV/favicon-$px.png"
done
magick "$FV/favicon-16.png" "$FV/favicon-32.png" "$FV/favicon-48.png" "$FV/favicon.ico"
cp "$SMALL" "$FV/favicon.svg"
for f in "$FV"/*; do printf 'render: %-78s %5s B\n' "${f#"$ROOT"/}" "$(wc -c < "$f")"; done

# ── /etc/issue: the small mark in 7-bit ASCII (ascii/river-mark-small.txt) ───────────────
# In white, with the title block to the right of art lines 3-5, two columns past the art's
# widest line (measured without escapes), so it follows the art when the art changes.
# agetty reads backslash escapes in /etc/issue, so the art's backslashes are doubled there.
# banner <art> <out> <title> <line2> <line3>
banner() {
	T1="$3" T2="$4" T3="$5" LC_ALL=C awk '
	# titles via ENVIRON, not -v: -v would expand the \r \l \n agetty escapes.
	BEGIN { t1 = ENVIRON["T1"]; t2 = ENVIRON["T2"]; t3 = ENVIRON["T3"] }
	function c(name) { return "\\e{" name "}" }
	{ art[NR] = $0; w = length($0); if (w > maxw) maxw = w }
	END {
		print ""
		for (i = 1; i <= NR; i++) {
			line = art[i]
			gsub(/\\/, "\\\\", line)
			txt = ""
			if (i == 3) txt = c("white") t1 c("reset")
			if (i == 4) txt = c("lightgray") t2 c("reset")
			if (i == 5) txt = c("darkgray") t3 c("reset")
			if (txt != "") line = line sprintf("%" (maxw + 2 - length(art[i])) "s", "")
			print "  " c("white") line c("reset") txt
		}
		print ""
	}' "$1" > "$2"
}
banner "$B/ascii/river-mark-small.txt" "$WS/etc/issue" "R U N I N K   R I V E R" "developer workstation" '\r (\l)  \n'

# ── fastfetch: the mark in truecolor half blocks (fastfetch/) ──────────────────────────────
# The terminal greeting (/etc/profile.d/runink-greeting.sh; fastfetch's config is
# /etc/xdg/fastfetch/config.jsonc). The mark rendered at 44x44 px; each terminal cell is
# two pixels, upper = foreground of "▀", lower = background, so the logo is 44 columns x 22
# rows. A transparent pixel (alpha < 128) is the terminal's own background. A colour escape is
# written only when the colour changes, which keeps each file small.
#   river-mark.ansi   the logo fastfetch prints beside the machine summary
# The six-frame arrival animation (anim/1..6.ansi) was removed on 2026-10-02: the greeting is
# static (owner: "Can we make it less dancy"). lint-branding-sync fails if the frames return.
# halfblock <png> <out.ansi>
halfblock() {
	magick "$1" -depth 8 txt:- | LC_ALL=C.UTF-8 awk '
	BEGIN { FS = "[ ,:()]+" }
	/^#/ { next }
	{
		x = $1; y = $2; if (x + 1 > w) w = x + 1; if (y + 1 > h) h = y + 1
		on[y, x] = ($6 >= 128); col[y, x] = int($3) ";" int($4) ";" int($5)
	}
	END {
		for (r = 0; r < h; r += 2) {
			last = -1
			for (x = 0; x < w; x++) if (on[r, x] || on[r + 1, x]) last = x
			line = ""; fg = ""; bg = ""
			for (x = 0; x <= last; x++) {
				t = on[r, x]; u = on[r + 1, x]
				nf = t ? col[r, x] : u ? col[r + 1, x] : fg
				nb = t && u ? col[r + 1, x] : ""
				if (nb != bg) { line = line (nb == "" ? "\033[49m" : "\033[48;2;" nb "m"); bg = nb }
				if (nf != fg) { line = line "\033[38;2;" nf "m"; fg = nf }
				line = line (t ? "▀" : u ? "▄" : " ")
			}
			print line "\033[0m"
		}
	}' > "$2"
}
# frame <dy> <dx> <out.svg> — the mark with the raft (the puppy aboard: layers back, head,
# front) moved down dy and the waves layer moved right dx (clipped to the water band), in the
# mark's 512 units (at 44 px one pixel is about 11.6 units). Composed from the same layers
# the SDDM greeter animates, so it fails loudly with them if the mark changes shape.
frame() {
	wd="$(sed -n 's/^ *<path d="\([^"]*\)".*/\1/p' "$ML/water.body")"
	[ -n "$wd" ] || { echo "render: frame: the water layer is not one <path d=...> (river-mark.svg changed?)" >&2; exit 1; }
	{
		printf '<svg viewBox="%s" xmlns="http://www.w3.org/2000/svg">\n' "$MARK_VIEWBOX"
		printf '<defs><clipPath id="river-water"><path d="%s"/></clipPath></defs>\n' "$wd"
		cat "$ML/defs.body"
		printf '<g transform="translate(0 %s)">\n' "$1"
		cat "$ML/back.body" "$ML/head.body" "$ML/front.body"
		printf '</g>\n'
		cat "$ML/water.body"
		printf '<g clip-path="url(#river-water)"><g transform="translate(%s 0)">\n' "$2"
		cat "$ML/waves.body"
		printf '</g></g>\n</svg>\n'
	} > "$3"
}
rm -rf "$B/fastfetch"
mkdir -p "$B/fastfetch"
svg2png "$B/logo/river-mark.svg" "$TMP/ff.png" 44 44 >/dev/null
halfblock "$TMP/ff.png" "$B/fastfetch/river-mark.ansi"
for f in "$B/fastfetch/river-mark.ansi"; do
	[ "$(wc -l < "$f")" -eq 22 ] || { echo "render: $f is not 22 rows" >&2; exit 1; }
	printf 'render: %-78s %5s B\n' "${f#"$ROOT"/}" "$(wc -c < "$f")"
done
# The Linux console cannot draw the truecolor mark (it degrades to a few VGA colours): the
# greeting shows the ASCII mark there, the one /etc/issue shows.
cp "$B/ascii/river-mark-small.txt" "$B/fastfetch/river-mark.txt"
rm -rf "$WS/usr/share/runink/fastfetch"
mkdir -p "$WS/usr/share/runink"
cp -r "$B/fastfetch" "$WS/usr/share/runink/fastfetch"

echo "render: done — commit the source changes together with every render listed above"
