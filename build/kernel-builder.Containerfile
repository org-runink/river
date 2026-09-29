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
