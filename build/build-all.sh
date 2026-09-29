#!/bin/sh
# build-all.sh — orchestrate the from-source component builds, then package them.
#
# Ordered: downstream payload (optional) -> installer binaries -> river-guide -> models manifest -> makepkg each PKGBUILD
# (which consumes $OUT_DIR). The resulting *.pkg.tar.zst are picked up by
# scripts/make-localrepo.sh.
set -eu

HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck disable=SC1091
. "$HERE/config.env"

# A failing component step MUST fail the build. Earlier versions used `|| true` here, and
# a build whose payload step had failed still packaged, installed cleanly, and produced a
# node that came up as a bare substrate while reporting success. Partial builds are still
# legitimate (e.g. iterating on the k0s package), but they must be asked for.
step() {
	if "$@"; then return 0; fi
	if [ "${RUNINK_ALLOW_PARTIAL:-0}" = 1 ]; then
		echo "build-all: WARNING: '$*' FAILED — RUNINK_ALLOW_PARTIAL=1, continuing." >&2
		echo "build-all: the resulting packages will be INCOMPLETE. Do not ship this image." >&2
		return 0
	fi
	echo "build-all: '$*' FAILED." >&2
	echo "build-all: refusing to package an incomplete image." >&2
	echo "build-all: set RUNINK_ALLOW_PARTIAL=1 if an incomplete build is what you want." >&2
	exit 1
}

echo "== build-all: components from source =="
if [ "${RIVER_PAYLOAD_PREBUILT:-0}" = 1 ]; then
	# The caller already ran "$RIVER_PAYLOAD_DIR/build.sh" "$OUT_DIR" (build/local-iso.sh
	# does, on the host, where a payload's toolchain and source live) and this step runs in
	# the builder container without them. The packaging rules below still refuse a hollow
	# payload: runink-core needs an executable deploy, runink-runtime a non-empty bin/.
	[ -x "$OUT_DIR/core-tree/deploy" ] || {
		echo "build-all: RIVER_PAYLOAD_PREBUILT=1 but $OUT_DIR/core-tree/deploy is missing" >&2; exit 1; }
	echo "  downstream payload: prebuilt into $OUT_DIR by the caller"
	RIVER_PAYLOAD_NONE=0
elif [ -n "${RIVER_PAYLOAD_DIR:-}" ]; then
	[ -x "$RIVER_PAYLOAD_DIR/build.sh" ] || {
		echo "build-all: RIVER_PAYLOAD_DIR=$RIVER_PAYLOAD_DIR has no executable build.sh" >&2; exit 1; }
	echo "  downstream payload: $RIVER_PAYLOAD_DIR"
	step "$RIVER_PAYLOAD_DIR/build.sh" "$OUT_DIR"
	RIVER_PAYLOAD_NONE=0
else
	# A BASE image: RIVER alone, no downstream platform. runink-core and runink-runtime
	# are still built (the manifest lists them) but carry no payload, and first-boot
	# enrollment knows the node is a bare k0s host by design rather than by accident.
	echo "  no RIVER_PAYLOAD_DIR — building the BASE image (no downstream payload)"
	RIVER_PAYLOAD_NONE=1
fi
export RIVER_PAYLOAD_NONE
# The installer's probe + planner come from this repository and ship on every image.
step "$HERE/20-installer-binaries.sh"
# river-guide (live medium only): the install guide agent + its pinned guide model.
step "$HERE/25-river-guide.sh"
# MODELS_DIR: the models.lock cache filled by build/models-fetch.sh (default
# ~/.cache/river-build/models). 50-models-manifest.sh verifies it against the lock.
step "$HERE/50-models-manifest.sh"

echo "== build-all: packaging (makepkg) =="
if command -v makepkg >/dev/null 2>&1; then
	# runink-installer, runink-runtime, runink-core and runink-k0s-airgap package LOCAL trees
	# ($OUT_DIR/installer-bin, $OUT_DIR/bin, $OUT_DIR/core-tree, $OUT_DIR/k0s-airgap) with no
	# upstream checksum to verify here (k0s-airgap.sh pins and verifies every image by digest)
	# — hence --skipinteg for those four only.
	for p in runink-installer runink-runtime runink-core runink-k0s-airgap; do
		echo "  makepkg $p"
		( cd "$HERE/pkgbuilds/$p" && \
		  RUNINK_OUT="$OUT_DIR" makepkg -f --nodeps --skipinteg --noconfirm ) \
		  || { echo "  makepkg $p FAILED" >&2; exit 1; }
	done

	# runink-k0s WITH integrity checking: it fetches a release binary over the network, and an
	# unverified download baked into a sovereign image is precisely what the pin exists to stop.
	echo "  makepkg runink-k0s (integrity-checked)"
	( cd "$HERE/pkgbuilds/runink-k0s" && \
	  RUNINK_OUT="$OUT_DIR" makepkg -f --nodeps --noconfirm ) \
	  || { echo "  makepkg runink-k0s FAILED (sha256 mismatch? re-pin from k0s's sha256sums.txt)" >&2; exit 1; }

	# river-guide WITH integrity checking: it downloads the pinned mistral.rs release.
	echo "  makepkg river-guide (integrity-checked)"
	( cd "$HERE/pkgbuilds/river-guide" && \
	  RUNINK_OUT="$OUT_DIR" makepkg -f --nodeps --noconfirm ) \
	  || { echo "  makepkg river-guide FAILED (sha256 mismatch? see guide/model.lock)" >&2; exit 1; }

	# runink-tayga is built WITH integrity checking. Its source is a plain upstream tarball
	# from a dormant project (last release 2011) whose ONLY guarantee is the sha256 pinned in
	# its PKGBUILD. No --nodeps either: it compiles C, so its build deps must be present.
	echo "  makepkg runink-tayga (integrity-checked)"
	( cd "$HERE/pkgbuilds/runink-tayga" && \
	  RUNINK_OUT="$OUT_DIR" makepkg -f --noconfirm ) \
	  || { echo "  makepkg runink-tayga FAILED" >&2; exit 1; }
else
	echo "build-all: makepkg not found (not on Arch/Artix) — artifacts staged in $OUT_DIR,"
	echo "           run packaging on an Arch/Artix builder to produce *.pkg.tar.zst"
fi

echo "== build-all: done =="
