# Getting started

<!--
SPDX-FileCopyrightText: 2026 The RIVER Authors
SPDX-License-Identifier: MIT
-->

Installing Runink River takes four steps, and a fifth page shows the desktop you land on.
Nothing in the install needs a network: the installer copies the running live system onto
your disk.

1. <a name="download-and-verify"></a>**Download and verify**

   Get `runink-river-<date>-x86_64.iso` and check its signature and checksum, with the one-line
   `install.sh` or by hand with `gpg` and `sha256sum`.
   [Download & verify](download/README.md)

2. <a name="write-a-usb-stick"></a>**Write a USB stick**

   Write the image to a USB stick of at least 8 GB. Everything on the stick is erased.
   [Create a USB stick](usb/README.md)

3. <a name="install"></a>**Install**

   Boot the stick in UEFI mode and follow the graphical installer.
   [Install with the graphical installer](install/README.md)

4. <a name="first-boot"></a>**First boot**

   Unlock the disk with your passphrase and sign in to DEsk.
   [First boot](first-boot/README.md)

5. <a name="use-the-desktop"></a>**Use the desktop**

   The programs, memory sticks, Bluetooth, printers and the battery.
   [Using the desktop](desktop/README.md)

After the install, the system updates with `pacman -Syu`, including Runink River's own
packages from the signed `[runink]` repository:
[Updates](../configuration/updates/README.md) and
[The [runink] package repository](../configuration/package-repository/README.md).

## System requirements

The installer measures the machine before it asks anything, and it **refuses** a machine
below these minimums, saying which one it missed.

| What | Minimum |
|---|---|
| Firmware | UEFI. Legacy BIOS boot is not supported. Secure Boot must be **off** for now ([why](../security/secure-boot/README.md)). |
| CPU | x86-64-v3 (AVX2, FMA, F16C, BMI1/2, MOVBE): Intel Haswell, AMD Excavator or newer. |
| Physical cores | 2 |
| RAM | 8 GB installed (7168 MiB reported by the kernel) |
| Disk | one internal disk of 64 GiB or more. USB, removable and read-only disks, disks in use and the boot stick itself are never offered as targets. |

Several eligible disks become one pool with redundancy (a mirror for two disks, RAID-Z for
more); the rules are in [the hardware planner](../features/installer/README.md).
These are the workstation minimums of `river-plan --profile workstation`, documented in
[`docs/INSTALLER-HARDWARE.md`](https://github.com/org-runink/river/blob/main/docs/INSTALLER-HARDWARE.md).
