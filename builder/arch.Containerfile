# SPDX-FileCopyrightText: 2026 The RIVER Authors
# SPDX-License-Identifier: MIT
#
# The Arch Linux build environment for the Arch-based image (docs/ARCH-BASE.md, step C).
#
# It replaces builder/Containerfile (Artix, artools) once the Arch-based ISO boots; until then
# both exist and the shipping ISO is still built from builder/Containerfile. Nothing in this
# image comes from Artix.
#
# Pinned twice over (invariant 9):
#   - the base image by its linux/amd64 manifest digest: archlinux:base-devel-20261122.0.629543,
#     index digest sha256:949ecb1661609ab7e8f6854ba03efd62792e7a796313738e94b040c095c20d91,
#     resolved from Docker Hub 2026-11-28;
#   - every package by the Arch Linux Archive snapshot ARCH_PIN_DATE. The default below must
#     equal ARCH_PIN_DATE in pacman/mirrorlist.pin (TestArchPin fails Tier 1 otherwise); this
#     build context is builder/, which cannot read that file.
#
# This is a BUILD HOST, not the image: pacman here is Arch's, which depends on systemd, so the
# container carries systemd's files as the stock base image does. No systemd process runs in a
# container build, and the image's own closure is checked separately (docs/ARCH-BASE.md §4).
#
# Build:   podman build -t runink-os-builder-arch -f builder/arch.Containerfile builder
FROM docker.io/library/archlinux@sha256:a1d4b509c51fc68dcc7ee1f88342fe26b1f65e6775c3aff26f196141e6c76a6f

ARG ARCH_PIN_DATE=2026-11-25

# The archive snapshot is the WHOLE mirrorlist: no live mirror can hand this build a newer
# package. /etc/pacman.d/mirrorlist-arch is the name pacman/pacman.conf.in includes.
# -Syyuu: refresh, upgrade, and allow downgrades, so the result is exactly the snapshot whatever
# date the base image was built on.
RUN set -eu; \
    d="$(printf '%s' "$ARCH_PIN_DATE" | tr - /)"; \
    printf 'Server = https://archive.archlinux.org/repos/%s/$repo/os/$arch\n' "$d" > /etc/pacman.d/mirrorlist; \
    cp /etc/pacman.d/mirrorlist /etc/pacman.d/mirrorlist-arch; \
    pacman-key --init; \
    pacman-key --populate archlinux; \
    pacman -Syyuu --noconfirm --needed \
        archlinux-keyring \
        base-devel git rsync curl \
        go gcc \
        squashfs-tools dosfstools mtools libisoburn libarchive arch-install-scripts grub \
        wayland wayland-protocols libxkbcommon libinput pixman libdrm mesa libglvnd \
        libdisplay-info seatd hwdata libxcb xcb-util-renderutil xcb-util-wm \
        xorg-xwayland cairo fontconfig pam libpulse gtk3 json-c greetd \
    ; \
    pacman -Scc --noconfirm; \
    grep -qx "Server = https://archive.archlinux.org/repos/$d/\$repo/os/\$arch" /etc/pacman.d/mirrorlist-arch

# Not here, on purpose:
#   - libelogind. Arch has no elogind; the desktop packages that linked it on Artix
#     (runink-wlroots0.18 and its dependents) are ported by the s6-layer change, which adds
#     elogind to [runink] (docs/ARCH-BASE.md §3 row 10) and its build dependency here.
#   - artools, archiso. The ISO builder that replaces buildiso is our own (docs/ARCH-BASE.md
#     step D2); the tools it needs (squashfs-tools, libisoburn, mtools, grub, dosfstools,
#     arch-install-scripts) are above.
#   - the optional Flutter SDK of builder/Containerfile: it clones an unpinned branch, and no
#     package in this repository needs it.

# No Python in the build environment, exactly as builder/Containerfile: remove the interpreter,
# every python-* package and the packages that require them, without their dependents, then
# fail the image build if an interpreter is still on PATH.
RUN py="$(pacman -Qq | grep -E '^python(-|$)' || true)" \
    && if [ -n "$py" ]; then \
         pacman -Rdd --noconfirm $py $(pacman -Qi $py | sed -n 's/^Required By *: //p' \
           | tr ' ' '\n' | grep -vxE 'None|python(-.*)?' | sort -u); \
       fi \
    && ! command -v python3 && ! command -v python \
    && ! ls /usr/bin/python* >/dev/null 2>&1

# muon, the C implementation of meson, for the meson projects in build/pkgbuilds (same pin as
# builder/Containerfile).
ARG MUON_VERSION=0.5.0
ARG MUON_SHA256=565c1b6e1e58f7e90d8813fda0e2102df69fb493ddab4cf6a84ce3647466bee5
RUN cd /tmp \
    && curl -fsSLo muon.tar.gz "https://github.com/muon-build/muon/archive/refs/tags/${MUON_VERSION}.tar.gz" \
    && echo "${MUON_SHA256}  muon.tar.gz" | sha256sum -c - \
    && mkdir muon-src && tar -xzf muon.tar.gz -C muon-src --strip-components=1 && cd muon-src \
    && ./bootstrap.sh build \
    && build/muon-bootstrap setup -Dprefix=/usr/local -Dbuildtype=release -Dlibpkgconf=disabled build-final \
    && build/muon-bootstrap -C build-final samu \
    && build/muon-bootstrap -C build-final install \
    && cd / && rm -rf /tmp/muon.tar.gz /tmp/muon-src \
    && muon version

# The non-root build user, uid 1000, for the same reason as builder/Containerfile:
# build/build-kernel-zfs.sh and build/local-iso.sh map the caller onto that uid.
RUN useradd -m -u 1000 -U -G wheel builder && \
    printf 'builder ALL=(ALL) NOPASSWD: ALL\n' > /etc/sudoers.d/builder
USER builder
WORKDIR /os
CMD ["/bin/bash"]
