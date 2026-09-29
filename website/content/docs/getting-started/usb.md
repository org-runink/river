---
title: Create a USB stick
weight: 2
---
<!--
SPDX-FileCopyrightText: 2026 The RIVER Authors
SPDX-License-Identifier: MIT
-->

The ISO is a hybrid image: write it to the whole stick, not to a partition. A stick of
8 GB or more is enough. **Everything on the stick is erased.** Write only an image you have
verified ([Download & verify]({{< relref "download" >}})).

## With install.sh (Linux)

`install.sh` downloads and verifies the image first, then writes it:

```bash
curl -fsSL https://raw.githubusercontent.com/org-runink/river/main/install.sh | sh -s -- --write /dev/sdX
```

It refuses a device that is not a whole disk, is neither USB nor removable, is mounted, or
reports no serial number. Before it writes, it shows the stick's model, size and serial and
asks you to type the serial, so a typo in `/dev/sdX` cannot erase the wrong disk. For a
scripted run, pass the serial you checked with `--yes-i-have-checked-serial=SERIAL`.

The script never runs `sudo`. Run as a normal user, it verifies the image and the stick, then
prints the one command that needs root, for you to run:

```text
sudo dd if=./runink-river-<YYYYMMDD>-x86_64.iso of=/dev/sdX bs=4M conv=fsync oflag=direct status=progress
```

Add `--dry-run` to see what it would write without writing anything, and `--out DIR` when the
image is in, or should go to, another directory. Every flag is described on
[Download & verify]({{< relref "download#flags" >}}).

## By hand (Linux)

Find the stick, check its size, model and serial, unmount anything mounted from it, then
write the verified image:

```bash
lsblk -o NAME,SIZE,MODEL,SERIAL,TRAN,MOUNTPOINTS   # the stick shows TRAN=usb
sudo umount /dev/sdX1                              # for each mounted partition of the stick
sudo dd if=runink-river-<YYYYMMDD>-x86_64.iso of=/dev/sdX bs=4M conv=fsync oflag=direct status=progress
sync
```

`/dev/sdX` is the whole device (`/dev/sdb`), never a partition (`/dev/sdb1`).

## From Windows or macOS

Any tool that writes a raw disk image byte for byte works. Pick "write an image as-is"
(dd mode) if the tool asks, and verify the image before you write it.

## Boot it

Plug the stick in, open your firmware's boot menu (often F12, F11, F8 or Esc at power-on)
and pick the USB stick's **UEFI** entry. Turn Secure Boot off first: the image does not boot
with Secure Boot on yet ([Secure Boot]({{< relref "/docs/security/secure-boot" >}})).

The stick's live system then starts Plasma and opens the installer by itself:
[Install]({{< relref "install" >}}). It does not start?
[Troubleshooting]({{< relref "/docs/troubleshooting#the-usb-stick" >}}).

## Reuse the stick afterwards

The installer never offers the stick as a target and never saves the recovery key to it. To
use the stick for files again, recreate a partition table and a filesystem on it with
KDE Partition Manager (on the installed system) or any partitioning tool.
