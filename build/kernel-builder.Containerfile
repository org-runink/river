# SPDX-FileCopyrightText: 2026 The RIVER Authors
# SPDX-License-Identifier: MIT
#
# The kernel + OpenZFS build container (build/build-kernel-zfs.sh): the Artix builder image
# (builder/Containerfile) plus the linux-runink and runink-zfs makedepends, and NO Python.
# Rebuilt by build-kernel-zfs.sh on every run; podman's layer cache makes that a no-op until
# this file or the builder image changes. Packages come from the same Artix repositories as
# the builder image's (neither pins a mirror snapshot today; the kernel and OpenZFS SOURCES are
# what the sha256 and signature pins cover).
#
# Python: nothing in the kernel or OpenZFS build runs it. The kernel's one Python step for
# this config, libbpf's bpf_helper_defs.h (scripts/bpf_doc.py), is replaced by
# build/tools/bpfdoc in the PKGBUILD's prepare(), so `go` is listed; OpenZFS is configured
# --with-python=no. A builder image built before builder/Containerfile purged the interpreter
# still carries it, as a dependency of tools the build never runs (debugedit -> gdb, and
# artools-pkg -> bzr, mercurial). The second RUN removes it, every python-* package and the
# packages that require them, without their dependents (-Rdd: debugedit and artools-pkg stay,
# and neither is used here), then fails the image build if an interpreter is still on PATH.
# That assertion is what a later base-image change would trip, rather than a kernel build
# quietly succeeding with Python present.
ARG BUILDER_IMAGE=runink-os-builder
FROM ${BUILDER_IMAGE}
USER root

# Pin the mirrors, for the same reason scripts/build-iso-box.sh does (read its "skip mirrors
# measured as degraded" note first — it records what NOT to do here).
#
# pacman does NOT fail over to the next mirror on a stalled transfer: it aborts the whole
# transaction. The base image ships an ORDERED list of 12 whose #1, mirrors.dotsrc.org, has
# repeatedly degraded to "less than 1 bytes/sec" and taken builds down with it — three kernel
# builds on 2026-10-01 alone, each on a different package.
#
# This is an ALLOWLIST of the two mirrors pacman/mirrorlist.pin pins, NOT a deletion from the
# ordered list. That distinction is load-bearing: an earlier attempt deleted the bad entry and
# silently promoted a year-stale mirror at #2, which built a rootfs from 2025 packages and
# failed much later and much more confusingly. Keeping only known-current mirrors cannot do
# that. Adding one here means checking it is CURRENT, not merely reachable.
RUN printf '%s\n' \
      'Server = https://mirror1.artixlinux.org/repos/$repo/os/$arch' \
      'Server = https://mirror.pascalpuffke.de/artix-linux/$repo/os/$arch' \
      > /etc/pacman.d/mirrorlist

RUN pacman -Syu --noconfirm --needed \
      bc cpio go libelf pahole perl tar xz zstd kmod openssl xxhash zlib bison flex \
      diffutils inetutils gnupg \
      libaio libtirpc ncurses libudev pam util-linux-libs \
    && pacman -Scc --noconfirm
RUN py="$(pacman -Qq | grep -E '^python(-|$)' || true)" \
    && if [ -n "$py" ]; then \
         pacman -Rdd --noconfirm $py $(pacman -Qi $py | sed -n 's/^Required By *: //p' \
           | tr ' ' '\n' | grep -vxE 'None|python(-.*)?' | sort -u); \
       fi \
    && ! command -v python3 && ! command -v python \
    && ! ls /usr/bin/python* >/dev/null 2>&1
USER builder
