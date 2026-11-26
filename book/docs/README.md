# Documentation

<!--
SPDX-FileCopyrightText: 2026 The RIVER Authors
SPDX-License-Identifier: MIT
-->

**Runink River is an optimized developer workstation on s6.** It is a DEsk desktop
for data, analytics and AI development on hardware you own, built on:

- **s6** init (s6, s6-rc, s6-linux-init). Never systemd.
- **`linux-runink`**, one kernel: the zen kernel 7.2.x stable series, built from
  signature-verified sources. The kernel and OpenZFS packages build for x86_64 and
  aarch64; the installer image and the `[runink]` repository are x86_64 only today.
- An **encrypted ZFS root** (aes-256-gcm on every dataset) with **boot environments**.
- A **default-deny firewall** and **`river-sandbox`** for code you do not trust.
- A **graphical, zero-knowledge installer** that measures the machine, plans the disks and
  shows you the recovery key once.

The name is always written in full, "Runink River". A bare "River" is the name of an
unrelated Wayland compositor. Package, path and binary names (`runink-*`, `river-*`,
`/etc/runink`) are identifiers and keep their spelling.

> **Warning:** **Status: pre-release.** There is **no signed public release yet**: the release-signing key
> is published, but no release has been cut with it. The first release ships only after it
> installs, reboots, unlocks its disk and reaches a working desktop on real hardware; that
> gate is still open. The ISO is still assembled with a fork of Artix Linux's ISO tooling
> while Runink River moves to its own from-source base. See
> [the roadmap](roadmap/README.md).

## By role

| You want to | Read |
|---|---|
| **Use** it: install, the desktop, everyday tasks | [Getting started](getting-started/README.md), [Using the desktop](getting-started/desktop/README.md), [Features](features/README.md), [Troubleshooting](troubleshooting/README.md), [FAQ](faq/README.md) |
| **Administer** it: configure and operate a machine | [Configuration](configuration/README.md) (boot environments, s6 services, updates, the package repository, keyboard and language, networking), [Firewall](features/firewall/README.md), [Hardware support](hardware/README.md), [Verify a release](security/verify/README.md), [Kernel on Arch](kernel-on-arch/README.md) |
| **Develop** it: build, contribute, extend | [Architecture](architecture/README.md), [Design principles](concepts/README.md), [Build from source](build/README.md) (including your own distribution on Runink River), [Contributing](contributing/README.md), [Releases](releases/README.md), [Licensing](governance/README.md), [Roadmap](roadmap/README.md) |

## Where to start

- [Getting started](getting-started/README.md): Download and verify the ISO, write a USB stick, install, first boot.
- [Using the desktop](getting-started/desktop/README.md): DEsk: the programs, memory sticks, Bluetooth, printers, the battery.
- [Features](features/README.md): s6, the kernel, ZFS and encryption, the firewall, the sandbox, the installer.
- [Download & verify](getting-started/download/README.md): The one-line install.sh, its flags, and verifying the ISO by hand against the release key.
- [Configuration](configuration/README.md): Boot environments and rollback, s6 services, updates, keyboard and language, networking.
- [Package repository](configuration/package-repository/README.md): The signed [runink] pacman repository: key, mirrors, adding it to an older install.
- [Troubleshooting](troubleshooting/README.md): Error messages, what they mean, and a rescue from the live medium.
- [Hardware support](hardware/README.md): Minimums, which disks are used, graphics and firmware.
- [Architecture](architecture/README.md): The image's layers, the boot chain and the s6 service graph.
- [Build from source](build/README.md): Build and test the ISO yourself.
- [Kernel on Arch (AUR)](kernel-on-arch/README.md): Run linux-runink on an existing Arch Linux system.
- [Security](security/README.md): Report a vulnerability, verify a release, Secure Boot plans.
- [Contributing](contributing/README.md): DCO sign-off, the invariants, your first change.
- [Governance & community](contributing/governance/README.md): How decisions are made, becoming a maintainer, where to talk.
- [FAQ](faq/README.md): Why s6, why not systemd, is it Arch, and more.
- [Roadmap](roadmap/README.md): What is done, what is planned, what is out of scope.

## Scope of the project

Runink River ships the operating system, its packaging and its installer. Applications and
platforms are not part of it: a downstream distribution can build on Runink River, and does
so outside this repository. The source, including every document these pages summarise, is
at [github.com/org-runink/river](https://github.com/org-runink/river).
