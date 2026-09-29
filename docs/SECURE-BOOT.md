<!--
SPDX-FileCopyrightText: 2026 The RIVER Authors
SPDX-License-Identifier: MIT
-->

# Runink River and Secure Boot

> **Status: roadmap.** Today Runink River boots with GRUB via the UEFI removable path and Secure
> Boot **off**. Module signing is staged (below). Nothing on this page is enforced yet
> unless it says so.

Runink River plans two Secure Boot paths, one for machines its operators control and one for
public installs on arbitrary hardware.

## 1. Operator-controlled fleets: an organisation Root CA + MOK

- An **organisation Root CA** (X.509, kept offline) issues a Secure Boot signing
  certificate.
- That certificate signs the **kernel** (`linux-runink`), the **out-of-tree ZFS modules**
  (`spl.ko`, `zfs.ko` from `runink-zfs`) and the **initramfs** (as part of a unified kernel
  image once Runink River's own base drops GRUB, see [OWN-BASE.md](OWN-BASE.md)).
- The CA certificate is enrolled on each machine as a **Machine Owner Key (MOK)** with
  `mokutil --import`, confirmed once at the console on the next boot. Machines whose
  firmware is in setup mode can instead enroll the organisation's own PK/KEK/db.
- Kernel module signing is staged in `build/pkgbuilds/runink-kernel/README.md`:
  v1 boots the kernel; v2 signs `spl.ko`/`zfs.ko` with the kernel's build key (in place
  today, best effort); v3 turns on `MODULE_SIG_FORCE` and `lockdown=integrity`. Secure
  Boot enforcement follows v3.

## 2. Public installs: shim through shim-review

A public Runink River ISO must boot on machines whose firmware only trusts the Microsoft UEFI CA.
The standard route is a Runink River-specific **shim** signed by Microsoft after passing the
community review at **rhboot/shim-review**, with Runink River's own certificate embedded in the
shim (the "vendor certificate") signing the next stage. Requirements that review checks
include: a reproducible shim build, SBAT metadata, a documented key-protection process
(HSM), kernel lockdown, and a signed-module policy. That is why this path waits for the
own base, v3 module signing and the release-key process
([RELEASE-SIGNING.md](RELEASE-SIGNING.md)).

## Keys

Secure Boot keys are X.509 and form a separate chain from the OpenPGP release key; neither
can stand in for the other. Private keys never enter CI.

## Open items (owner-side; see docs/governance/LF-AIDATA.md)

- Create the organisation Root CA and the Secure Boot signing certificate; decide HSM
  storage.
- Prepare and submit the shim-review application.
