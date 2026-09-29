---
title: The linux-runink kernel
linkTitle: Kernel
weight: 2
---
<!--
SPDX-FileCopyrightText: 2026 The RIVER Authors
SPDX-License-Identifier: MIT
-->

Runink River has **exactly one kernel**, `linux-runink`: the zen kernel on the current
**7.2.x stable** series (not an LTS), built from signature-verified sources and configured
for data work. OpenZFS is built for it as a **separate module package**, never merged into
the kernel.

## What it is built from

| Part | Pin | Verified by |
|---|---|---|
| Kernel base | kernel.org `linux-7.2.7.tar.xz` | its `.tar.sign` (kernel.org stable signing keys) |
| zen patch | `linux-v7.2.7-zen1.patch.zst` | its `.sig`, signed by zen's maintainer |
| Configuration | `config.base` + `config.delta`, with a floor in `config.require` | sha256 pins; the build fails below the floor |
| OpenZFS (separate package) | `zfs-2.4.4.tar.gz` | its `.asc` |

This is the same recipe Arch Linux uses for `linux-zen`: the signed stable tarball plus the
signed zen patch. The kernel is compiled for **x86-64-v3** CPUs.

## The profile

zen's own configuration is tuned for desktop latency. `linux-runink` reverses the parts of
that trade-off that cost throughput in long-running data jobs (scans, joins, aggregations,
bulk ingest), and keeps the switches to get latency back at boot:

| Area | zen | linux-runink |
|---|---|---|
| Interactivity bundle | `ZEN_INTERACTIVE=y` | off (upstream I/O schedulers and scheduler defaults; split-lock mitigation back on) |
| Preemption | full | lazy, with `PREEMPT_DYNAMIC` kept |
| Timer tick | 1000 Hz | 250 Hz |
| Transparent huge pages | always | `madvise` |
| TCP congestion control | cubic | BBR built in, with the `fq` qdisc |
| Autogroup, power-efficient workqueues | on | off |

**On the workstation, the desktop keeps full preemption.** Because `PREEMPT_DYNAMIC` is
kept, the preemption model is a boot parameter: Runink River sets `preempt=full` on the
kernel command line (`/etc/default/grub.d/20-runink-preempt.cfg`), so the same kernel binary
stays responsive under Plasma.

The workstation also sets up zstd-compressed zram swap, sized by the install plan (half the
RAM below 32 GiB, a quarter above, at most 16 GiB), and sets `vm.swappiness=150`,
`vm.page-cluster=0` and `vm.watermark_boost_factor=0` for it
(`/etc/sysctl.d/99-runink-workstation.conf`).

## Security settings are not traded away

- CPU vulnerability mitigations stay compiled in and on. No Runink River image passes
  `mitigations=off`.
- Memory hardening stays on: `init_on_alloc`, `init_on_free`, hardened usercopy,
  `FORTIFY_SOURCE`, strict kernel and module RWX, KASLR, stack protector.
- Hibernation, kexec, `/dev/mem` and `/proc/kcore` are disabled; modules are signed with a
  per-build key.

## On your machine

```bash
uname -r            # 7.2.7-zen1-1-runink: the one kernel release this PKGBUILD may produce
cat /proc/cmdline   # the parameters below
```

The kernel command line of an installed workstation is assembled from two GRUB drop-ins:

| Parameter | From | Why |
|---|---|---|
| `root=ZFS=zriver/ROOT/<be> rw` | `10-runink-zfs.cfg` (the installer) | the boot environment to mount at `/` |
| `slab_nomerge init_on_alloc=1 init_on_free=1 randomize_kstack_offset=1` | `10-runink-zfs.cfg` | memory hardening that works with out-of-tree modules |
| `zfs_force=1` | `10-runink-zfs.cfg` | import the pool even after an unclean shutdown left it marked in use |
| `quiet splash` | `10-runink-zfs.cfg`, when the Plymouth theme is present | the boot splash |
| `preempt=full` | `20-runink-preempt.cfg` (the image) | full preemption for the desktop |

`lockdown=integrity` and `module.sig_enforce=1` are **not** set yet: the ZFS modules load from
the initramfs, and enforcing signatures before module signing is complete would stop the pool
from importing ([Secure Boot]({{< relref "/docs/security/secure-boot" >}})). Every module is
signed with a per-build key (`MODULE_SIG_ALL`, SHA-512) that never enters the image.

To change the command line, edit a drop-in in `/etc/default/grub.d/` and regenerate the menu
with `sudo grub-mkconfig -o /boot/grub/grub.cfg` (with the EFI partition mounted at `/boot`).

## No benchmark claims yet

Every setting has a documented upstream reason, but **no measured comparison** with
linux-zen or linux-lts has been published. The harness to do it,
{{< repo "bench/analytics/" >}} (TPC-H on DuckDB, fio, memory bandwidth, sort and join), exists
and has only had a smoke run. Numbers will be published when they are measured on real
hardware, not before.

## Kernel and ZFS move together

A kernel update is only released when the pinned OpenZFS release declares support for that
kernel series, and the kernel and its ZFS modules are bumped in the same change. A kernel
without its root filesystem driver is not a release.

The full rationale, with upstream sources for each setting: {{< repo "docs/KERNEL.md" >}}.
To run this kernel on Arch Linux, see [Kernel on Arch (AUR)]({{< relref "/docs/kernel-on-arch" >}}).
