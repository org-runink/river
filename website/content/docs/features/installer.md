---
title: Installer & hardware planner
linkTitle: Installer & planner
weight: 6
---
<!--
SPDX-FileCopyrightText: 2026 The RIVER Authors
SPDX-License-Identifier: MIT
-->

Runink River is installed on hardware nobody has seen in advance. So before it asks a single
question, the installer **measures the machine and decides what it can do**, and it asks for
exactly one dangerous confirmation: which disks to erase.

## Zero knowledge required

The graphical installer, `river-installer`, is written for someone who has never installed
an operating system. It asks about language, keyboard, network, a name and a password, and
shows the recovery key; every technical decision (partitioning, pool layout, encryption,
boot loader) is made by the plan. Walk-through: [Install]({{< relref "/docs/getting-started/install" >}}).

- One program for the whole flow, with a small local web interface (plain HTML, CSS and
  JavaScript, keyboard navigation, English, Spanish, French and Portuguese). It listens on
  the loopback address only and is shown full-screen.
- Every action runs the same tools as the text installer: `river-netsetup`, `river-hwprobe`,
  `river-plan` and the numbered install steps.
- Secrets stay in memory, are redacted from logs, and are served only to root and the kiosk
  user. The password is hashed inside the installer.
- If the interface restarts, it reconnects and picks up where the install is.

## Measure: river-hwprobe

`river-hwprobe` reads `/proc`, `/sys` and the udev database and writes a JSON inventory:
CPU level and topology, RAM, GPUs, disks, network interfaces, TPM and firmware. It is
read-only, never opens a block device and needs no root.

## Plan: river-plan

`river-plan` is a pure function of that inventory. For a workstation
(`--profile workstation`) it checks the [minimums]({{< relref "/docs/getting-started#system-requirements" >}}),
budgets RAM for the system and the ZFS cache, and lays out the disks. A machine below a
minimum gets a **refused** plan with the reason; `--lab` waives the minimums for virtual
machines and test rigs and records that it did.

### How the disks are laid out

Only eligible disks count: 64 GiB or more, not USB, not removable, not read-only, not in
use, and never the boot medium.

| Eligible disks | Layout |
|---|---|
| 1 | single disk (no redundancy; the plan warns) |
| 2 | mirror |
| 3 | RAID-Z1 |
| 4–5 SSD/NVMe | RAID-Z1 |
| 4–5 HDD | RAID-Z2 (hard-disk rebuilds are long) |
| 6–12 | RAID-Z2 |
| HDDs plus two or more SSDs | data on the HDDs, the two best SSDs as a mirrored special vdev |
| mixed sizes | the largest group of disks within 10 % of each other |

Every disk that will be erased is listed with its serial; every disk left alone is listed
with the reason.

## Confirm: by serial

The graphical installer shows the exact disks and their serials on one screen and asks you
to type a word to confirm; the text installer asks you to **type each disk's serial**. Right
before anything is written, `05-hwplan-verify` probes the machine again and refuses if any
target disk changed. The plan is saved on the installed system as
`/etc/runink/install-plan.json` (root only).

## Code and tests

`installer/hwprobe`, `installer/plan` and `installer/internal/` are Go, standard library
only, MIT-licensed, built for the baseline `x86-64` level so they can run on (and refuse)
CPUs below x86-64-v3. Planner decisions are pinned by table-driven tests on fixture machines
(`make test`, CI job `installer-go`). The JSON output is a documented contract:
{{< repo "docs/INSTALLER-HARDWARE.md" >}}.
