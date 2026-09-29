---
title: Architecture
weight: 6
description: "How a Runink River machine is put together: the image's two layers, the boot chain from GRUB to Plasma, the s6 service graph, and the packages Runink River builds itself."
---
<!--
SPDX-FileCopyrightText: 2026 The RIVER Authors
SPDX-License-Identifier: MIT
-->

This page describes how the workstation image is built and how an installed machine comes
up. Every name on it (packages, services, files) is taken from the source tree; follow the
links to read the code.

## The layers

| Layer | What it is | Source |
|---|---|---|
| Kernel | `linux-runink` 7.2.7-zen1: the zen kernel's 7.2.x stable series, the only kernel on the image | {{< repo "build/pkgbuilds/runink-kernel/" >}} |
| Root filesystem | OpenZFS 2.4.4 as a separate module package (`runink-zfs`) plus its userland and initramfs hook (`runink-zfs-utils`) | {{< repo "build/pkgbuilds/runink-zfs/" >}} |
| Init | s6, s6-rc and s6-linux-init, driven by the `s6` front end | {{< repo "iso-profiles/river/root-overlay/etc/s6/" >}} |
| Desktop | KDE Plasma 6 on Wayland, SDDM, PipeWire, NetworkManager, Bluetooth and printing, each with its s6 service package | {{< repo "iso-profiles/river/profile.yaml" >}} |
| Installer | `river-installer` (graphical), `runink-install` (text), `river-hwprobe`, `river-plan`, `river-netsetup` and the numbered install steps | {{< repo "installer/" >}} |
| Host policy | the `runink-fw` firewall, `river-perms`, `river-sandbox`, sysctl and zram tuning | {{< repo "iso-profiles/river/root-overlay/usr/local/bin/" >}} |

Everything else on the image (Plasma, Firefox, the libraries and tools) is a distribution
package from pinned mirrors. Today those come from Artix Linux, through Artix's ISO tooling:
that is the **transitional** builder, which Runink River is replacing with its own
from-source base ([below](#the-transitional-builder-and-the-own-base)).

## The image: two layers, one golden system

The ISO holds two squashfs layers ({{< repo "iso-profiles/river/profile.yaml" >}}):

- **`rootfs`** is the finished system: every package the installed machine has, plus the
  profile's `root-overlay/`. An installed machine is a copy of it.
- **`livefs`** is for the live medium only and sits on top of `rootfs`: the live user's
  autologin (Plasma and a text console on Ctrl+Alt+F2), its passwordless `sudo`, the live
  hostname, the graphical installer's s6 service, and the live GRUB menu package
  (`artix-grub-live`, the whole of `Packages-Live`).

The installer does not download or resolve packages. Step `20-clone-rootfs` copies the
running live system onto the new ZFS boot environment with `rsync`, and then removes what
belongs only to the live medium (the autologin drop-ins, the live `sudo` rule, the installer
itself and its service, the machine-id and the SSH host keys). This is why an install works
with no network at all.

The package set is an allow-list. `Packages-Root` is curated by hand, never by package
group; `river lint profile-manifest` checks that every package `profile.yaml` installs
is on it, and `river lint closure` checks the whole dependency closure against
`forbidden.closure`. Nothing on this list may enter the image, directly or as a dependency:

| Kept out | Why |
|---|---|
| `podman`, `docker`, `docker-compose`, `podman-compose` | no container runtime in the base image |
| `flatpak`, `flatpak-kcm`, `discover`, `packagekit`, `packagekit-qt6` | no second software supply chain beside the pinned pacman repositories |
| `aws-cli`, `aws-cli-v2`, `google-cloud-cli`, `google-cloud-sdk`, `azure-cli` | no cloud vendor tooling in the base image |
| `linux` | exactly one kernel, `linux-runink` |

## From power-on to the desktop

{{% steps %}}

### Firmware and GRUB

UEFI firmware loads GRUB from the EFI system partition's removable path
(`EFI/BOOT/BOOTX64.EFI`, installed with `grub-install --removable`), so the machine boots
even without a firmware boot entry. The EFI partition is mounted at `/boot` and holds the
kernel, the initramfs and `grub.cfg`: GRUB cannot read a natively encrypted ZFS pool, so
nothing it needs lives on the pool.

### The initramfs

The kernel command line names the root dataset, `root=ZFS=<pool>/ROOT/<be>`
(`/etc/default/grub.d/10-runink-zfs.cfg`, written by `40-boot-grub-zfs`). The initramfs
is built by mkinitcpio with these hooks
({{< repo "iso-profiles/river/root-overlay/etc/mkinitcpio.conf.d/zfs.conf" >}}):

```text
HOOKS=(base udev autodetect microcode modconf kms plymouth keyboard keymap block zfs filesystems)
```

The `zfs` hook imports the pool (matching the hostid baked into the image), runs any key
provider in `/etc/zfs/initramfs-tools-load-key.d/`, and otherwise asks on the console for
the pool's passphrase. It then mounts the boot environment at `/`. The Plymouth splash
covers the import.

### s6-linux-init and s6-rc

`s6-linux-init` becomes PID 1 and starts the supervision tree; `s6-rc` brings up the default
bundle compiled into `/etc/s6/rc/compiled` at install time (step `80-enable-s6`). The
services the project adds, and the order they impose:

| Service | Type | Depends on | Job |
|---|---|---|---|
| `runink-fw` | oneshot | `modules` | loads the default-deny firewall |
| `zfs-mount` | oneshot | `modules`, `mount-tmpfs`, `udevadm` | `zfs mount -a`: mounts `/home` and the pool's other datasets |
| `mount-filesystems` | oneshot (s6 base) | `zfs-mount` (added) | the rest of `/etc/fstab`, including the EFI partition at `/boot` |
| `river-perms` | oneshot | `mount-filesystems` | re-asserts 0600/0700 modes on secret files |
| `NetworkManager-srv` | longrun | `runink-fw` (added) | the network |
| `sshd-srv` | longrun | `runink-fw` (added) | SSH, still closed by the firewall until you open port 22 |
| `plymouth-quit` | oneshot | | ends the boot splash |
| `sddm-srv` | longrun | `plymouth-quit`, `zfs-mount` (added) | the login screen |
| `rc-local` | oneshot | `mount-filesystems`, `runink-fw` | runs `/etc/s6/rc.local` |

Bluetooth (`bluetoothd`) and printing (`cupsd`) come from the `bluez-s6` and `cups-s6`
packages, and dbus and elogind are pulled in as dependencies. "(added)" marks a dependency
Runink River adds to a packaged service, as a file in its `dependencies.d/` directory.

### rc.local

`/etc/s6/rc.local` runs last, in init context rather than in a login session, so elogind
never reaps what it starts. On the live medium (`overlay=livefs` on the kernel command line)
it exits at once. On an installed machine it runs `sysctl --system`, sets up zram swap
(`runink-zram.sh`), and runs any first-boot hooks a downstream image carries (none on Runink
River).

### Login

SDDM shows the Runink River greeter; the session is Plasma on Wayland (KWin), with PipeWire
and WirePlumber as user services.

{{% /steps %}}

## Packages Runink River builds

The `[runink]` repository is built from the PKGBUILDs under {{< repo "build/pkgbuilds/" >}}
and placed ahead of the distribution repositories, so its packages win. On the workstation
image:

| Package | Contents |
|---|---|
| `linux-runink`, `linux-runink-headers` | the kernel, built from the signed kernel.org tarball and the signed zen patch |
| `runink-zfs` | OpenZFS kernel modules prebuilt for exactly that kernel release |
| `runink-zfs-utils` | the matching OpenZFS userland and the mkinitcpio `zfs` hook, from the same PKGBUILD, so module and userland cannot drift apart |
| `runink-installer` | `river-installer`, `river-hwprobe`, `river-plan`, `river-netsetup` and `river-netcheck`, from {{< repo "installer/" >}} (Go standard library only) |

The tree builds further packages for downstream distributions; they are not on the Runink
River image. An installed system gets updates of these packages from the public, signed
`[runink]` repository: [The [runink] package repository]({{< relref "/docs/configuration/package-repository" >}}).

## The transitional builder and the own base

Today `make iso` runs Artix's `buildiso` with the profile under `iso-profiles/river/`, and
`scripts/patch-artools.go` adjusts it (the kernel swap, the image name and volume label,
the live menu). That tooling is transitional. The plan in {{< repo "docs/OWN-BASE.md" >}}
replaces it with Runink River's own base, built from source:

| Phase | Scope | State |
|---|---|---|
| 0 | plan and scaffold: the recipe format, `base/river-build`, `base/river-sign`, recipes for s6, skalibs and execline | done: the three recipes build bit-for-bit reproducibly in a pinned, network-less container |
| 1 | a reproducible toolchain | planned |
| 2 | the base built from recipes, its own image assembly (`river-compose`) and `river-update` | planned |
| 3 | migration of existing installs, removal of the Artix tooling | planned |
| 5 | the Plasma 6 workstation built from recipes | planned |

Planned changes that users will notice: the kernel booted as a signed unified kernel image
instead of through GRUB, TPM 2.0 unlock of the pool, and updates installed as a new boot
environment per release. None of these exists yet.

## Where to read more

- {{< repo "docs/ARCHITECTURE.md" >}}: the architecture document. Parts of it still describe
  the server image that left this repository and say so.
- {{< repo "docs/ENCRYPTION.md" >}}: the pool, the key and the ESP layout.
- {{< repo "docs/INSTALL.md" >}}: every install step.
- {{< repo "docs/OWN-BASE.md" >}}: the own-base plan.
