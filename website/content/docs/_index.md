---
title: Documentation
linkTitle: Documentation
weight: 1
next: /docs/getting-started
---
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

{{< callout type="warning" >}}
**Status: pre-release.** There is **no signed public release yet**: the release-signing key
is published, but no release has been cut with it. The first release ships only after it
installs, reboots, unlocks its disk and reaches a working desktop on real hardware; that
gate is still open. The ISO is still assembled with a fork of Artix Linux's ISO tooling
and Artix packages. Runink River is migrating from Artix to official Arch Linux packages plus Runink River's own s6 layer (in progress, `docs/ARCH-BASE.md`); its own from-source base comes after that. See
[the roadmap]({{< relref "/docs/roadmap" >}}).
{{< /callout >}}

## By role

| You want to | Read |
|---|---|
| **Use** it: install, the desktop, everyday tasks | [Getting started]({{< relref "getting-started" >}}), [Using the desktop]({{< relref "getting-started/desktop" >}}), [Features]({{< relref "features" >}}), [Troubleshooting]({{< relref "troubleshooting" >}}), [FAQ]({{< relref "faq" >}}) |
| **Administer** it: configure and operate a machine | [Configuration]({{< relref "configuration" >}}) (boot environments, s6 services, updates, the package repository, keyboard and language, networking), [Firewall]({{< relref "features/firewall" >}}), [Hardware support]({{< relref "hardware" >}}), [Verify a release]({{< relref "security/verify" >}}), [Kernel on Arch]({{< relref "kernel-on-arch" >}}) |
| **Develop** it: build, contribute, extend | [Architecture]({{< relref "architecture" >}}), [Design principles]({{< relref "concepts" >}}), [Build from source]({{< relref "build" >}}) (including your own distribution on Runink River), [Contributing]({{< relref "contributing" >}}), [Releases]({{< relref "releases" >}}), [Licensing]({{< relref "governance" >}}), [Roadmap]({{< relref "roadmap" >}}) |

## Where to start

{{< cards >}}
  {{< card link="getting-started" title="Getting started" icon="download" subtitle="Download and verify the ISO, write a USB stick, install, first boot." >}}
  {{< card link="getting-started/desktop" title="Using the desktop" icon="desktop-computer" subtitle="DEsk: the programs, memory sticks, Bluetooth, printers, the battery." >}}
  {{< card link="features" title="Features" icon="lightning-bolt" subtitle="s6, the kernel, ZFS and encryption, the firewall, the sandbox, the installer." >}}
  {{< card link="getting-started/download" title="Download & verify" icon="shield-check" subtitle="The one-line install.sh, its flags, and verifying the ISO by hand against the release key." >}}
  {{< card link="configuration" title="Configuration" icon="cog" subtitle="Boot environments and rollback, s6 services, updates, keyboard and language, networking." >}}
  {{< card link="configuration/package-repository" title="Package repository" icon="archive" subtitle="The signed [runink] pacman repository: key, mirrors, adding it to an older install." >}}
  {{< card link="troubleshooting" title="Troubleshooting" icon="support" subtitle="Error messages, what they mean, and a rescue from the live medium." >}}
  {{< card link="hardware" title="Hardware support" icon="chip" subtitle="Minimums, which disks are used, graphics and firmware." >}}
  {{< card link="architecture" title="Architecture" icon="template" subtitle="The image's layers, the boot chain and the s6 service graph." >}}
  {{< card link="build" title="Build from source" icon="terminal" subtitle="Build and test the ISO yourself." >}}
  {{< card link="kernel-on-arch" title="Kernel on Arch (AUR)" icon="archive" subtitle="Run linux-runink on an existing Arch Linux system." >}}
  {{< card link="security" title="Security" icon="shield-check" subtitle="Report a vulnerability, verify a release, Secure Boot plans." >}}
  {{< card link="contributing" title="Contributing" icon="users" subtitle="DCO sign-off, the invariants, your first change." >}}
  {{< card link="contributing/governance" title="Governance & community" icon="user-group" subtitle="How decisions are made, becoming a maintainer, where to talk." >}}
  {{< card link="faq" title="FAQ" icon="question-mark-circle" subtitle="Why s6, why not systemd, is it Arch, and more." >}}
  {{< card link="roadmap" title="Roadmap" icon="map" subtitle="What is done, what is planned, what is out of scope." >}}
{{< /cards >}}

## Scope of the project

Runink River ships the operating system, its packaging and its installer. Applications and
platforms are not part of it: a downstream distribution can build on Runink River, and does
so outside this repository. The source, including every document these pages summarise, is
at [github.com/org-runink/river](https://github.com/org-runink/river).
