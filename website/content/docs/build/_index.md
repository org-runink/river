---
title: Build from source
weight: 8
description: "Build the Runink River ISO yourself: prerequisites, the local build with one root step, the make targets, VM tests, and what is and is not reproducible today."
---
<!--
SPDX-FileCopyrightText: 2026 The RIVER Authors
SPDX-License-Identifier: MIT
-->

Anyone can build the Runink River ISO from this repository. The result is
`runink-river-<date>-x86_64.iso`, volume label `RIVER`, with its EFI partition labelled
`RIVER_EFI`. The reference is {{< repo "docs/BUILD.md" >}}; this page is the short path.

{{< callout type="info" >}}
The ISO is still assembled with Artix Linux's `buildiso`, the **transitional** builder. The
own from-source base that replaces it is planned in {{< repo "docs/OWN-BASE.md" >}}
([Architecture]({{< relref "/docs/architecture#the-transitional-builder-and-the-own-base" >}})).
{{< /callout >}}

## What you need

- A Linux host with **rootless podman**. Arch, Artix or another distribution all work: the
  Artix build environment runs in a container (`builder/Containerfile`).
- **About 40 GB free** under `~/.cache`. Everything large goes to `~/.cache/river-build`,
  never to `/tmp`.
- **Time**: the kernel and ZFS are compiled from source on every build (about an hour at
  `-j8` for the kernel).
- **Root once**, for the last step: `buildiso` needs mounts and loop devices.
- For the VM tests: QEMU with KVM, and `edk2-ovmf`.

## Build on your workstation

`build/local-iso.sh` runs every stage that does not need root as your user, then prints the
one command that does:

```bash
df -h ~/.cache                     # check the free space first
build/local-iso.sh                 # the Runink River profile, a public image
sudo sh build/iso-root-stage.sh ~/.cache/river-build/local-iso/root-stage-river.env
```

What the stages do:

| Stage | Runs as | Does |
|---|---|---|
| 1 | you | builds the Artix builder image (`make builder`) |
| 2 | you | builds `linux-runink` and `runink-zfs` from the in-tree PKGBUILDs in rootless podman (`build/build-kernel-zfs.sh`), then checks them with `build/verify-kernel-zfs.sh`: the pinned kernel release, every line of `config.require` in the built `.config`, and `spl.ko`, `zfs.ko` and an in-tree module signed by the same per-build key |
| 4 | you | builds the component packages in the builder (`build/build-all.sh`) |
| 5 | you | creates the `[runink]` repository for the profile, under `~/.cache/river-build/local-iso/` |
| 7 | you | the pre-flight checks: Tier 1, the Go tests of the installer and the guide, the closure lint of the profile against that repository |
| 8 | you | saves the builder image for root's podman and writes the root-stage file |
| root | root | `build/iso-root-stage.sh`: runs `buildiso -i s6` in a privileged container, checks the volume label, writes the `.sha256` and hands the ISO back to you |

Stages 0 and 6 exist only for downstream profiles and private images. Stage 3, and part of
stage 4, build packaging that downstream distributions need: it is built and linted with every
image, but the Runink River image does not carry it. The kernel signing key, `runink_signing.pem`, is kept 0600 next to the
built packages and never enters the image.

## Test the ISO in a VM

```bash
build/qemu-gui-test.sh ~/.cache/river-build/iso-out/runink-river-<date>-x86_64.iso
build/qemu-test.sh     ~/.cache/river-build/iso-out/runink-river-<date>-x86_64.iso
```

- **`qemu-gui-test.sh`** installs with no human, under QEMU/KVM with OVMF: it drives the
  graphical installer's API with the same calls the screens make, photographs every screen,
  reboots into the installed system, unlocks the pool, checks that `/home` is mounted, the
  firewall is loaded and the machine-id is set, logs in at SDDM, waits for Plasma, and checks
  the network.
- **`qemu-test.sh`** checks the live medium without installing: the GRUB entry, SDDM, the
  live user's Plasma session, and NetworkManager, SDDM, Bluetooth and CUPS up in s6-rc.

Against an ISO built before your change, both can overlay this tree's installer and root
overlay onto the live system first; see the scripts' headers.

## The make targets

On an Artix machine you can also drive the stages one by one:

```bash
make help            # every target
make components      # build/build-all.sh: installer binaries, packages (makepkg)
make localrepo       # repo-add the built packages into localrepo/ (the [runink] repo)
make lint            # shellcheck, closure lint, installer and branding sync, pin checks
make test            # gofmt, go vet and go test of the installer's probe and planner
make iso             # buildiso -p river (Artix and artools only)
make iso-in-builder  # the same inside the builder container, on any podman host
make vmtest          # boot the ISO in qemu, install to a virtual disk, run the asserts
make lock            # resolve Packages-Root against the pinned repos into Pkglist.lock
```

`make iso` needs `artools` and an overlayfs upper directory that is **not on ZFS**;
`scripts/build-iso-box.sh` backs `/var/lib/artools` with tmpfs for that reason. A nested or
sandboxed podman that cannot allocate loop devices cannot build the ISO.

## Pins and reproducibility

- `pacman/mirrorlist.pin` freezes the distribution mirror snapshot; `pacman/pacman.conf.in`
  declares the `[runink]` file repository ahead of it.
- The kernel, the zen patch and OpenZFS are pinned by sha256 **and** checked against their
  upstream signatures ([The kernel]({{< relref "/docs/features/kernel" >}})).
- `make lock` records the full resolved package set in `Pkglist.lock`.

"Reproducible" below means the reproducible-builds.org definition: the same source,
environment and instructions give bit-for-bit identical output.

| Artifact | Status |
|---|---|
| The installer's Go binaries | repeatable on one host (two builds, identical sha256); not yet compared across hosts |
| Own-base recipes (s6, skalibs, execline) | reproducible in the pinned, network-less builder |
| `Pkglist.lock` | repeatable against the pinned mirror snapshot |
| `linux-runink`, `runink-zfs` | not verified: sources are pinned and verified, but no rebuild has been compared |
| **The ISO** | **not reproducible**: the buildiso path sets no `SOURCE_DATE_EPOCH`, and no two ISO builds have been compared |

Until the ISO is reproducible, a release maintainer checks the published artifacts against
their own build before signing ([Releases]({{< relref "/docs/releases" >}})).

## Building your own distribution on Runink River

A downstream distribution keeps its profile and branding **outside this repository** and
points the build at them, from a pinned Runink River commit:

```bash
RIVER_PROFILE_DIR=/path/to/profiles/example \
RIVER_BRANDING_DIR=/path/to/branding/example \
    build/local-iso.sh
```

The profile has the same shape as `iso-profiles/river/`, plus a `river-profile.env` that sets
`KIND`, `ISO_LABEL`, `ISO_NAME` and `GRUB_TITLE`. Its installer steps must match
`installer/lib/` byte for byte (`river lint installer-sync <profile-dir>`). A private
image may add out-of-tree payloads with `RIVER_PAYLOAD_DIR`, which travel encrypted next to
the image ({{< repo "docs/PAYLOADS.md" >}}). The public Runink River image carries none, and
`build/local-iso.sh` and `build/iso-root-stage.sh` refuse to attach one to it. The full
contract is in {{< repo "docs/BUILD.md" >}}, "Downstream distributions".

## Build only the kernel

To run `linux-runink` on an existing Arch system without building the ISO, see
[Kernel on Arch (AUR)]({{< relref "/docs/kernel-on-arch" >}}).
