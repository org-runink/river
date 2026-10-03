#!/bin/sh
# SPDX-FileCopyrightText: 2026 The RIVER Authors
# SPDX-License-Identifier: MIT
#
# build-kernel-zfs.sh — build the image kernel and its ZFS pair WITHOUT root.
#
# Builds, in one rootless podman container derived from the Artix builder image:
#   1. linux-runink + linux-runink-headers from build/pkgbuilds/runink-kernel (the in-tree
#      PKGBUILD: signed sources, config.require floor, kernelrelease guard). Its prepare()
#      generates a fresh per-build module-signing key and build() exports it as
#      runink_signing.pem next to the PKGBUILD.
#   2. runink-zfs + runink-zfs-utils from build/pkgbuilds/runink-zfs, against the headers just
#      built, signing spl.ko/zfs.ko with that SAME key (the PKGBUILD reads
#      ../runink-kernel/runink_signing.pem).
# Then it verifies the result on the host (build/verify-kernel-zfs.sh) and copies the
# packages, SHA256SUMS and the signing key (0600) into $KERNEL_OUT/.
#
# The kernel build is long (about an hour at -j8 on a 16-thread host) and runs under
# `nice -n 19`. Nothing here needs root on the host: the container runs with
# --userns=keep-id as the builder user, whose uid is the caller's.
#
# Usage: build/build-kernel-zfs.sh [--verify-only]
# Environment:
#   KERNEL_OUT     output directory (default ${XDG_CACHE_HOME:-$HOME/.cache}/river-build/kernel)
#   BUILDER_IMAGE  Artix builder image (default runink-os-builder, see `make builder`)
#   KERNEL_JOBS    make -j value (default: half the host's threads, at least 1)
#   SRCDEST        optional makepkg source cache (pinned by sha256 + signature, so a cache
#                  can only ever supply the pinned bytes)
set -eu

HERE="$(cd "$(dirname "$0")" && pwd)"
CACHE="${XDG_CACHE_HOME:-$HOME/.cache}"
KERNEL_OUT="${KERNEL_OUT:-$CACHE/river-build/kernel}"
BUILDER_IMAGE="${BUILDER_IMAGE:-runink-os-builder}"
KBUILDER_IMAGE="${KBUILDER_IMAGE:-runink-kernel-builder}"
_threads="$(nproc 2>/dev/null || echo 2)"
KERNEL_JOBS="${KERNEL_JOBS:-$(( _threads / 2 > 0 ? _threads / 2 : 1 ))}"
WORK="$KERNEL_OUT/work"

log() { echo "build-kernel-zfs: $*" >&2; }
die() { log "$*"; exit 1; }

if [ "${1:-}" = "--verify-only" ]; then
	exec "$HERE/verify-kernel-zfs.sh" "$KERNEL_OUT"
fi
[ $# -eq 0 ] || die "usage: $0 [--verify-only]"

command -v podman >/dev/null 2>&1 || die "podman not found"
podman image exists "$BUILDER_IMAGE" \
	|| die "builder image '$BUILDER_IMAGE' not found; build it with: make builder"

# The builder image carries artools and base-devel but not every kernel/ZFS makedepend, and it
# carries a Python interpreter the build must not have. build/kernel-builder.Containerfile
# derives the build image: the makedepends plus `go` (the PKGBUILD compiles build/tools/bpfdoc,
# the Go port of the kernel's scripts/bpf_doc.py), and the interpreter removed and asserted
# absent. It is rebuilt only when that file or the builder image changes.
log "ensuring $KBUILDER_IMAGE (kernel + OpenZFS makedepends on $BUILDER_IMAGE, no interpreter)"
podman build -q -t "$KBUILDER_IMAGE" --build-arg BUILDER_IMAGE="$BUILDER_IMAGE" \
	-f "$HERE/kernel-builder.Containerfile" "$HERE/pkgbuilds/runink-kernel/keys" >/dev/null

mkdir -p "$KERNEL_OUT"
rm -rf "$WORK"
mkdir -p "$WORK/pkgbuilds" "$WORK/tmp" "$WORK/srcdest"
# Fresh copies: never build inside the checkout (src/ trees are ~30 GB).
for p in runink-kernel runink-zfs; do
	cp -aL "$HERE/pkgbuilds/$p" "$WORK/pkgbuilds/$p"   # -L: bpfdoc.go is a link into build/tools
	rm -rf "$WORK/pkgbuilds/$p/src" "$WORK/pkgbuilds/$p/pkg" "$WORK/pkgbuilds/$p"/*.pkg.tar.zst \
	       "$WORK/pkgbuilds/$p/runink_signing.pem"
done
SRC_MOUNT="$WORK/srcdest"
if [ -n "${SRCDEST:-}" ]; then
	[ -d "$SRCDEST" ] || die "SRCDEST=$SRCDEST is not a directory"
	SRC_MOUNT="$SRCDEST"
fi

log "building in $WORK (make -j$KERNEL_JOBS, nice 19); log: $KERNEL_OUT/build.log"
start=$(date +%s)
podman run --rm --userns=keep-id --user builder \
	-v "$WORK:/w" -v "$SRC_MOUNT:/srcdest" \
	-e TMPDIR=/w/tmp -e SRCDEST=/srcdest -e MAKEFLAGS="-j$KERNEL_JOBS" \
	-w /w/pkgbuilds "$KBUILDER_IMAGE" bash -euo pipefail -c '
		gpg --batch --quiet --import runink-kernel/keys/pgp/*.asc runink-zfs/keys/pgp/*.asc
		echo "== linux-runink (in-tree PKGBUILD) =="
		( cd runink-kernel && nice -n 19 makepkg -f --nodeps --noconfirm )
		[ -s runink-kernel/runink_signing.pem ] || { echo "no runink_signing.pem exported" >&2; exit 1; }
		echo "== install the headers + kernel for the OpenZFS build (container only) =="
		# --assume-installed initramfs: linux-runink depends on an initramfs GENERATOR, which a
		# container that never boots has no use for. Without this, pacman resolves that dep from
		# the baked database and downloads mkinitcpio -- the ONLY repo fetch in this step, and
		# the one that broke the build on 2026-10-03: the db was 44 h old, both pinned mirrors
		# had moved past mkinitcpio-42.1-1, and the transaction died 404 AFTER the kernel had
		# already compiled for an hour. Satisfying the dep instead of fetching it takes the
		# repos off the critical path here; kernel and OpenZFS SOURCES stay pinned by sha256 and
		# signature, so reproducibility is unchanged. Nothing in this container runs an
		# initramfs: linux-runink has no .install hook, and runink-zfs is only built, never
		# installed (its initcpio hook is a FILE it ships, which mkinitcpio reads on a real
		# installed system). NO APOSTROPHES in this block: it lives inside a single-quoted
		# container script.
		sudo pacman -U --noconfirm --assume-installed initramfs \
			runink-kernel/linux-runink-[0-9]*.pkg.tar.zst \
			runink-kernel/linux-runink-headers-*.pkg.tar.zst
		echo "== runink-zfs + runink-zfs-utils against it =="
		( cd runink-zfs && nice -n 19 makepkg -f --nodeps --noconfirm )
	' >"$KERNEL_OUT/build.log" 2>&1 || die "container build failed; tail of $KERNEL_OUT/build.log:
$(tail -30 "$KERNEL_OUT/build.log")"
log "container build finished in $(( ($(date +%s) - start) / 60 )) min"

mkdir -p "$KERNEL_OUT/pkgs"
rm -f "$KERNEL_OUT/pkgs"/*.pkg.tar.zst
cp "$WORK/pkgbuilds/runink-kernel"/*.pkg.tar.zst "$WORK/pkgbuilds/runink-zfs"/*.pkg.tar.zst "$KERNEL_OUT/pkgs/"
install -m 0600 "$WORK/pkgbuilds/runink-kernel/runink_signing.pem" "$KERNEL_OUT/runink_signing.pem"
( cd "$KERNEL_OUT/pkgs" && sha256sum ./*.pkg.tar.zst > SHA256SUMS )

"$HERE/verify-kernel-zfs.sh" "$KERNEL_OUT"
# The source trees are ~30 GB; the packages and the key are what the image needs.
rm -rf "$WORK"
log "done: $KERNEL_OUT/pkgs"
