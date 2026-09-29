---
title: Getting started
weight: 1
sidebar:
  open: true
---
<!--
SPDX-FileCopyrightText: 2026 The RIVER Authors
SPDX-License-Identifier: MIT
-->

Installing Runink River takes four steps. Nothing in the install needs a network: the
installer copies the running live system onto your disk.

{{% steps %}}

### Download and verify

Get `runink-river-<date>-x86_64.iso` and check its signature and checksum, with the one-line
`install.sh` or by hand with `gpg` and `sha256sum`.
[Download & verify]({{< relref "download" >}})

### Write a USB stick

Write the image to a USB stick of at least 8 GB. Everything on the stick is erased.
[Create a USB stick]({{< relref "usb" >}})

### Install

Boot the stick in UEFI mode and follow the graphical installer.
[Install with the graphical installer]({{< relref "install" >}})

### First boot

Unlock the disk with your passphrase and sign in to Plasma.
[First boot]({{< relref "first-boot" >}})

{{% /steps %}}

After the install, the system updates with `pacman -Syu`, including Runink River's own
packages from the signed `[runink]` repository:
[Updates]({{< relref "/docs/configuration/updates" >}}) and
[The [runink] package repository]({{< relref "/docs/configuration/package-repository" >}}).

## System requirements

The installer measures the machine before it asks anything, and it **refuses** a machine
below these minimums, saying which one it missed.

| What | Minimum |
|---|---|
| Firmware | UEFI. Legacy BIOS boot is not supported. Secure Boot must be **off** for now ([why]({{< relref "/docs/security/secure-boot" >}})). |
| CPU | x86-64-v3 (AVX2, FMA, F16C, BMI1/2, MOVBE): Intel Haswell, AMD Excavator or newer. |
| Physical cores | 2 |
| RAM | 8 GB installed (7168 MiB reported by the kernel) |
| Disk | one internal disk of 64 GiB or more. USB, removable and read-only disks, disks in use and the boot stick itself are never offered as targets. |

Several eligible disks become one pool with redundancy (a mirror for two disks, RAID-Z for
more); the rules are in [the hardware planner]({{< relref "/docs/features/installer" >}}).
These are the workstation minimums of `river-plan --profile workstation`, documented in
{{< repo "docs/INSTALLER-HARDWARE.md" >}}.
