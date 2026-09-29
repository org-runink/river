---
title: Roadmap
weight: 16
---
<!--
SPDX-FileCopyrightText: 2026 The RIVER Authors
SPDX-License-Identifier: MIT
-->

What Runink River intends to do, and what it deliberately will not do. Milestones are listed
in dependency order, not by date: with one maintainer today, dates would be guesses. The
authoritative list is {{< repo "ROADMAP.md" >}}.

**Status** is one of **done**, **in review** (code on a branch, not on `main`),
**in progress** or **planned** (no code yet).

## Milestones

| Milestone | Status |
|---|---|
| **Public repository and community basics**: publish from one squashed commit, private vulnerability reporting, branch protection, the OpenSSF Best Practices badge at *passing* | in progress (the repository side is done; owner steps remain) |
| **This documentation website** | done (published from `main`); being deepened |
| **Graphical, zero-knowledge installer** (`river-installer`) | on `main`, not yet in a release |
| **First signed public release**: generate the release key, publish its fingerprint, cut `runink-os-YYYY.MM` with signed checksums | in progress: the key is published (fingerprint in `KEYS`, 2026-09-27); no release cut yet |
| **Kernel on the AUR**: publish `linux-runink` and `zfs-linux-runink` | packaging ready, publication pending |
| **Kernel benchmarks**: published, reproducible results from the `bench/analytics` harness | harness on `main`, smoke run only; no results yet |
| **LF AI & Data Sandbox application** | planned: the proposal is a draft, not submitted |
| **Tier 2 CI**: an unattended install to a virtual encrypted ZFS disk, checked in CI | in progress (workflow scaffold, no runner) |
| **Own from-source base**: a reproducible toolchain, then the base built from recipes, then `river-update` (one boot environment per release) | in progress (phase 0 scaffold done) |
| **TPM 2.0 unattended disk unlock** | planned, with the own base |
| **Reproducible ISO**: two independent builds of one commit produce identical images | planned (depends on the own base) |
| **Secure Boot**: enforced module signing, own keys, then a reviewed shim | planned |
| **The workstation built on the own base** (Plasma 6 from recipes) | planned, after the base itself |
| **RIVER runtime**: a validated pipeline runtime whose steps run under `river-sandbox` | planned; no code yet |
| **SLSA Build L3 release provenance** | planned (depends on Tier 2 CI and the reproducible ISO) |
| **Incubation prerequisites**: more maintainers from more organisations, an elected TSC | planned |

## Not planned

Out of scope by design. Changing any of these needs a vote of the technical steering
committee ([Governance]({{< relref "/docs/contributing/governance" >}})).

- systemd, or a second init system.
- More than one kernel, or ZFS built into the kernel.
- Telemetry, phone-home behaviour or third-party hosted AI services in the image.
- Application logic or a specific downstream product in this repository.
