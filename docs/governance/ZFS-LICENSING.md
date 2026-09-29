<!--
SPDX-FileCopyrightText: 2026 The RIVER Authors
SPDX-License-Identifier: MIT
-->

# Runink River, ZFS and the kernel: the licence boundary

Runink River ships two kernel-side components under different licences:

| Component | Package | Licence | Source in this repo |
| --- | --- | --- | --- |
| Linux (zen fork) | `linux-runink`, `linux-runink-headers` | GPL-2.0-only | `build/pkgbuilds/runink-kernel/` |
| OpenZFS modules (`spl.ko`, `zfs.ko`) | `runink-zfs` | CDDL-1.0 | `build/pkgbuilds/runink-zfs/` |
| OpenZFS userland + initcpio hook | `runink-zfs-utils` | CDDL-1.0 (hook: MIT, from archzfs) | `build/pkgbuilds/runink-zfs/` |

The GPL-2.0 and the CDDL-1.0 are both free-software licences, but they are generally
considered incompatible for a *combined* work. Runink River therefore keeps them apart the way
the established distributions that ship ZFS do, and this document records exactly where
the line is so that no future change crosses it by accident.

## The boundary

1. **Never merged into the kernel source.** OpenZFS is not applied to, vendored into, or
   built as part of the kernel tree. `runink-kernel`'s `PKGBUILD` builds the kernel.org
   tarball plus the zen patch and nothing else; `config.delta` / `config.require` contain
   no ZFS symbols. There is no "built-in ZFS" configuration.
2. **A separate, out-of-tree module package.** `runink-zfs` is compiled *against* the
   installed `linux-runink-headers` (the kernel's public build interface at
   `/usr/lib/modules/<release>/build`), from the pinned, signature-verified OpenZFS
   release tarball, in its own build, and shipped as its own package with its own
   `license=(CDDL-1.0)`. It `conflicts=(zfs-dkms)`: there is one source of the modules.
3. **Same release, userland and module.** `runink-zfs-utils` comes out of the same
   configure/make of the same tarball, so userland and module can never be mismatched.
4. **Loaded at runtime, like any module.** The kernel loads `zfs.ko` from the package's
   files at boot (mkinitcpio `zfs` hook). Nothing links the two at build time.
5. **Sources are available.** The OpenZFS source is the upstream release tarball pinned by
   sha256 and signature in the `PKGBUILD`; Runink River carries no private patches to it. The
   kernel source is the kernel.org tarball plus the published zen patch, also pinned.
   Anyone can rebuild both from this repository.

## What contributors must not do

- Add ZFS code, patches or symbols to the kernel packaging tree, or build ZFS into the
  kernel image.
- Copy code between the two trees, in either direction.
- Replace the separate package with a combined "kernel + ZFS" package or image.

## Status

This is the structure Runink River uses today, and it is the arrangement relied on by other
distributions that ship prebuilt ZFS modules. It is **not legal advice**: a written
opinion from counsel on distributing the prebuilt `runink-zfs` module beside the GPL
kernel is an open owner-side item in [LF-AIDATA.md](LF-AIDATA.md) (owner action 6), and a foundation
may require it (and may require an exception from its governing board for non-Apache
licences).
