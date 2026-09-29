---
title: Design principles
weight: 7
description: "The invariants every change to Runink River keeps, what each one means on your machine, and where it is enforced."
---
<!--
SPDX-FileCopyrightText: 2026 The RIVER Authors
SPDX-License-Identifier: MIT
-->

Runink River's design is a short list of **invariants**, written down in
{{< repo "AGENTS.md" >}}. They apply to every change, whoever writes it, and changing one
needs a vote of the technical steering committee
([Governance]({{< relref "/docs/contributing/governance" >}})). Most are checked by a lint
or a test, so a change that breaks one fails CI instead of relying on review alone.

## The invariants

| # | Invariant | What it means on your machine | Enforced by |
|---|---|---|---|
| 1 | **s6, never systemd** | No `.service` units, no `systemctl`. Services are s6-rc oneshots and longruns; the desktop's services come from their `-s6` packages. | the service tree under `/etc/s6/`, `80-enable-s6` |
| 2 | **Exactly one kernel**, `linux-runink` | One pinned zen-kernel stable tag, verified sources, and a build that refuses any other kernel release or a configuration below `config.require`. OpenZFS stays a separate module package. | the kernel PKGBUILD, `build/verify-kernel-zfs.sh` |
| 3 | **ZFS root, encrypted** | Every dataset inherits aes-256-gcm from the pool root. The key is printed once as the recovery key and never written to a file. The EFI partition at `/boot` is the only unencrypted filesystem. | `10-disk-zfs` (refuses to continue if the pool came out unencrypted), `tests/assert-golden.sh` |
| 4 | **Secrets are handled like `/etc/shadow`** | 0600 files in 0700 directories, owned by their one reader, re-asserted at every boot. No secret, key or token is ever baked into an image. | `river-perms` |
| 5 | **Pinned, signed software supply** | Software comes from the pinned repositories and the `[runink]` packages. There is no app store, no Flatpak, no container runtime in the base image and no telemetry. | `forbidden.explicit`, `forbidden.closure`, `river lint closure` |
| 6 | **The manifest is an allow-list** | `Packages-Root` is curated package by package, never by group. | `river lint profile-manifest`, `river lint closure` |
| 7 | **Default-deny firewall from first boot** | Inbound traffic is dropped unless it is one of a short list or a port you opened; outbound is open; forwarding is dropped. SSH is closed until you open it. | `runink-fw`, `river lint firewall` |
| 8 | **Untrusted code runs under `river-sandbox`** | Code you do not trust gets no network (unless asked), no capabilities and no view of the machine's secrets. The deny-list is never widened. | `river-sandbox` |
| 9 | **Pin every upstream** by version and checksum, and by signature where it signs | Mirror snapshots, the kernel, OpenZFS, container base images and every GitHub Action (by full commit SHA). | the PKGBUILDs, `pacman/mirrorlist.pin`, the workflow files |

Details of each mechanism: [s6]({{< relref "/docs/features/s6" >}}),
[the kernel]({{< relref "/docs/features/kernel" >}}),
[ZFS & encryption]({{< relref "/docs/features/zfs-encryption" >}}),
[firewall]({{< relref "/docs/features/firewall" >}}),
[river-sandbox]({{< relref "/docs/features/sandbox" >}}).

## What the project promises

The security assurance case, {{< repo "docs/SECURITY-ASSURANCE.md" >}}, states the
requirements the mechanisms above serve. Those that apply to the workstation:

- **Data at rest is unreadable without the pool key** (ZFS native encryption).
- **No secret exists in any image**, and a machine's own secrets are readable only by their
  one reader (`river-perms`).
- **Code run on behalf of others cannot reach the machine's secrets** (`river-sandbox`).
- **What you install is what the project built from reviewed source**: pinned and verified
  upstreams, signed release checksums, and an `install.sh` that fails closed.
- **The installer never destroys data you did not choose to destroy**: disks are confirmed
  by serial, the boot medium is never a target, and the machine is probed again right before
  anything is written.

The same document names the gaps, among them:

- Secure Boot is off, so firmware does not verify the boot chain;
- the pool passphrase is typed at every boot until TPM unlock exists;
- the live medium has a default account with autologin (never boot it on an untrusted
  network; the installed system does not carry it);
- unprivileged user namespaces stay enabled because `river-sandbox` needs them;
- the boot, ZFS and encryption tests do not run in CI yet and are run by hand;
- the ISO is not reproducible yet;
- there is one maintainer, and no external security review has been done.

## Scope

Runink River is the operating system, its packaging and its installer. It holds no
application code and names no specific downstream product. A downstream distribution builds
its own images from this repository with an out-of-tree profile (`RIVER_PROFILE_DIR`) and,
for a private image, out-of-tree payloads (`RIVER_PAYLOAD_DIR`); the public Runink River image
carries no payload, and its build refuses to attach one
([Build from source]({{< relref "/docs/build" >}})).

## What is deliberately left out

- systemd, or a second init system;
- more than one kernel, or ZFS built into the kernel;
- telemetry, phone-home behaviour or third-party hosted AI services in the image;
- application logic, or a specific downstream product, in this repository.
