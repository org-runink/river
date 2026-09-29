---
title: Kernel on Arch (AUR)
linkTitle: Kernel on Arch (AUR)
weight: 9
---
<!--
SPDX-FileCopyrightText: 2026 The RIVER Authors
SPDX-License-Identifier: MIT
-->

`linux-runink`, the Runink River kernel, is also packaged **for Arch Linux, in the AUR**, as
a community package. You can run it on an existing Arch system next to your current kernel.

{{< callout type="warning" >}}
**Not an official Arch Linux package.** `linux-runink` and `zfs-linux-runink` are community
packages in the AUR. Arch Linux did not make, review or endorse them.

**Publication pending.** The packaging is in this repository
({{< repo "packaging/aur/" >}}), and the AUR packages have not been published yet. Until
they are, the commands below will not find them.
{{< /callout >}}

## What you get

The zen kernel 7.2.x stable series with the data-work configuration described in
[The linux-runink kernel]({{< relref "/docs/features/kernel" >}}): 250 Hz tick, lazy
preemption, transparent huge pages on `madvise`, BBR with `fq`, zen's interactivity bundle
off, CPU vulnerability mitigations and hardening on. It is built from the signed kernel.org
tarball and the signed zen patch; the configuration files are fetched from a tag of this
repository and checked against sha256 pins.

## Before you install

- The kernel is compiled for **x86-64-v3** (AVX2, BMI2, FMA, MOVBE: Intel Haswell, AMD
  Excavator and newer). The PKGBUILD refuses to build on a CPU without it; build with
  `RUNINK_MARCH=x86-64` or `x86-64-v2` for an older CPU.
- **Hibernation, kexec/kdump, `/dev/mem` and `/proc/kcore` are disabled.** Keep another
  kernel if you need them.
- It is a full distribution kernel: building takes a long time.
- It does **not** carry Runink River's runtime settings (`preempt=full` on a desktop, zram
  tuning); those belong to the Runink River image. On a desktop, consider adding
  `preempt=full` to your kernel command line.

## Build and install

With an AUR helper:

```bash
paru -S linux-runink linux-runink-headers
```

By hand:

```bash
git clone https://aur.archlinux.org/linux-runink.git
cd linux-runink
gpg --import keys/pgp/*.asc        # the kernel.org and zen signing keys, shipped with the package
makepkg -si                        # verifies sha256 and signatures, then builds
```

mkinitcpio's pacman hook creates `/boot/vmlinuz-linux-runink` and
`/boot/initramfs-linux-runink.img` as for any Arch kernel. Then add a boot entry:

- **GRUB:** `sudo grub-mkconfig -o /boot/grub/grub.cfg`
- **systemd-boot:** add `/boot/loader/entries/linux-runink.conf`, adapting `options` from
  your existing entry:

  ```text
  title   Arch Linux (linux-runink)
  linux   /vmlinuz-linux-runink
  initrd  /initramfs-linux-runink.img
  options root=UUID=<your-root-uuid> rw
  ```

## ZFS

Pick one:

- **`zfs-dkms`** (AUR) builds OpenZFS for every installed kernel, linux-runink included, and
  follows kernel upgrades by itself.
- **`zfs-linux-runink`** (AUR) is OpenZFS 2.4.4 prebuilt for exactly this linux-runink
  release. It depends on the AUR's `zfs-utils` for the userland and conflicts with
  `zfs-dkms`.

With a ZFS root, rebuild the initramfs after installing the modules: `sudo mkinitcpio -P`.

## Remove it

```bash
sudo pacman -Rns linux-runink linux-runink-headers   # and zfs-linux-runink, if installed
```

Then regenerate your GRUB menu or delete the systemd-boot entry.

## Measure it yourself

Whether the profile is faster than `linux-zen` or `linux-lts` on your workload is **to be
measured**: no comparison has been published. {{< repo "bench/analytics/" >}} is the harness
(TPC-H on DuckDB, fio, memory bandwidth, sort and join; five measured repetitions per suite),
and {{< repo "docs/KERNEL.md" >}} describes a fair comparison. Maintainers publish the packages
following {{< repo "packaging/aur/PUBLISHING.md" >}}.
