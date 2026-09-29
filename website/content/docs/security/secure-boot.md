---
title: Secure Boot
weight: 4
---
<!--
SPDX-FileCopyrightText: 2026 The RIVER Authors
SPDX-License-Identifier: MIT
-->

{{< callout type="warning" >}}
**Today Runink River boots with Secure Boot off.** GRUB boots from the UEFI removable path
and is not signed. Turn Secure Boot off in your firmware before you boot the USB stick.
Everything below is a plan unless it says otherwise.
{{< /callout >}}

## Two paths, in order

**1. Your own keys.** For machines you control: an organisation root CA (kept offline) issues
a Secure Boot signing certificate that signs the kernel, the out-of-tree ZFS modules and the
initramfs (as one unified kernel image once Runink River's own base drops GRUB). The CA is
enrolled on each machine as a Machine Owner Key (`mokutil --import`), or as your own
PK/KEK/db where the firmware allows it.

**2. A shim for public installs.** A public image must boot on machines that trust only the
Microsoft UEFI CA. The standard route is a Runink River shim signed by Microsoft after the
community review at [rhboot/shim-review](https://github.com/rhboot/shim-review), which checks
a reproducible shim build, SBAT metadata, key protection in an HSM, kernel lockdown and a
signed-module policy. That is why it comes after the own base, enforced module signing and
the release-key process.

## Module signing, staged

| Stage | State |
|---|---|
| v1: boot the kernel | done |
| v2: sign `spl.ko` and `zfs.ko` with the kernel's per-build key | in place, best effort |
| v3: `MODULE_SIG_FORCE` and `lockdown=integrity` | planned; Secure Boot enforcement follows it |

Secure Boot keys are X.509 and separate from the OpenPGP release key; neither stands in for
the other, and private keys never enter CI. Source: {{< repo "docs/SECURE-BOOT.md" >}}.
