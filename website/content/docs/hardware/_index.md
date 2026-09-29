---
title: Hardware support
weight: 5
description: "What a machine needs to run Runink River, how the installer decides, which disks it will and will not use, and which graphics and firmware the image carries."
---
<!--
SPDX-FileCopyrightText: 2026 The RIVER Authors
SPDX-License-Identifier: MIT
-->

The installer does not ask you whether your machine is suitable. It measures it with
`river-hwprobe`, makes a plan with `river-plan --profile workstation`, and **refuses** a
machine below a minimum, naming the one it missed. The rules on this page are the planner's
own, from {{< repo "installer/internal/planner/plan.go" >}} and
{{< repo "docs/INSTALLER-HARDWARE.md" >}}.

## Minimums

| What | Minimum | The refusal you see |
|---|---|---|
| Firmware | UEFI | `firmware booted in legacy BIOS mode: RIVER requires UEFI (...)` |
| CPU | x86-64-v3: AVX2, FMA, F16C, BMI1/2, MOVBE (Intel Haswell, AMD Excavator or newer) | `CPU is x86-64-v2 (missing ...): ... built for x86-64-v3 (AVX2/FMA/F16C/BMI2)` |
| Physical cores | 2 | `1 physical cores: the minimum is 2` |
| RAM | 7168 MiB as the kernel reports it (`MemTotal`), which is what 8 GB installed shows | `6144 MiB RAM (MemTotal): the minimum is 7168 MiB (8 GB installed)` |
| Disk | one eligible disk of 64 GiB or more | `no eligible target disk (need a non-removable, non-USB, unused disk of at least 64 GiB)` |

The numbers in the example messages are illustrations; the planner prints yours. A
workstation has no minimum pool size beyond the disk: a single-disk pool is fine.

### Trying anyway

On a virtual machine or a test rig, the graphical installer's **Details** link on the
"Your computer" screen offers **Try anyway (lab)**; the text installer takes `--lab`. The
plan then records every minimum it waived (`LAB OVERRIDE: ... refusal(s) waived`). It is
meant for testing: a machine below the minimums may not work.

## Which disks can be used

`river-hwprobe` marks a disk **ineligible**, with the reason, when it is any of:

| Reason in the plan | Meaning |
|---|---|
| `boot medium of this installer` | the USB stick you booted from; it is never a target |
| `... is mounted at ...`, `... is active swap`, `... is held by ...` | the disk or one of its partitions is in use |
| `USB-attached` | any disk on USB, including external SSDs |
| `removable media` | card readers and similar |
| `read-only` | write-protected devices |
| `smaller than 8 GiB` | too small to consider |

An eligible disk smaller than 64 GiB is listed as unused. Every disk the plan will erase is
shown with its serial number, and every disk it leaves alone is shown with the reason. The
probe only reads `/proc`, `/sys` and the udev database; it never opens a block device and
needs no root.

### More than one disk

Several eligible disks become one pool with redundancy. The layout rules, with the special
vdev for hard disks plus SSDs and the size-group rule for mixed disks, are on
[Installer & hardware planner]({{< relref "/docs/features/installer#how-the-disks-are-laid-out" >}}).

{{< callout type="warning" >}}
**Multi-disk installs are the least tested path.** `10-disk-zfs.sh`'s multi-disk layouts
have not yet run on real hardware or in a VM; the drivers were exercised end to end with stub
steps. And only the boot disk's EFI partition is kept up to date: the other disks get one,
formatted but empty, so if the boot disk of a mirror dies the pool survives, but the machine
needs its EFI partition recreated before it boots again.
{{< /callout >}}

## Graphics, media and firmware

| Area | On the image |
|---|---|
| GPU drivers | Mesa for AMD and Intel: `mesa`, `vulkan-radeon`, `vulkan-intel`, `vulkan-icd-loader`, `libva-mesa-driver` |
| Hardware video | `intel-media-driver` (VA-API for recent Intel graphics) and `libva-utils` (`vainfo`) |
| Codecs | `gst-plugins-base`, `-good`, `-bad`, `-ugly`, `gst-libav`, `ffmpeg` |
| Firmware | `linux-firmware-amdgpu` and `linux-firmware-intel` only |
| Audio | PipeWire (`pipewire`, `pipewire-pulse`, `pipewire-alsa`, `pipewire-jack`, `wireplumber`) |
| Bluetooth, printing | `bluez`, `bluez-utils`, `cups`, with the Plasma front ends |

NVIDIA's proprietary driver is not included. Devices that need firmware from other vendors'
firmware packages (some Wi-Fi and Bluetooth chips, for example) may not work, because only the
AMD GPU and Intel firmware packages are on the image.

## Check a machine before you install

In the live session (Konsole), the probe and the planner run without changing anything:

```bash
river-hwprobe                                     # a human-readable summary
river-hwprobe --json > probe.json                 # the full inventory (schema river.hwprobe/v1)
river-plan --probe probe.json --no-models --profile workstation   # the plan the installer would make
```

`river-plan` exits 0 when the plan is usable and **3 when it is refused**, printing the plan
either way. The JSON schemas are a stable contract, documented in
{{< repo "docs/INSTALLER-HARDWARE.md" >}}.
