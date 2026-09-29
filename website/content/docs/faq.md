---
title: FAQ & troubleshooting
linkTitle: FAQ
weight: 15
---
<!--
SPDX-FileCopyrightText: 2026 The RIVER Authors
SPDX-License-Identifier: MIT
-->

## About the project

### What is Runink River?

An optimized developer workstation on s6: a KDE Plasma desktop with s6 init, the zen-based
`linux-runink` kernel, an encrypted ZFS root with boot environments and a graphical installer,
for data, analytics and AI development on hardware you own. See the
[overview]({{< relref "/docs" >}}).

### Why the full name, "Runink River"?

"River" alone is the name of an unrelated Wayland compositor. Package and command names
(`runink-*`, `river-*`) are identifiers and keep their spelling.

### Is there a release I can download?

Not yet. The release-signing key is published (fingerprint in `KEYS`), but no release signed with it has been cut, and
nothing is published until it can be verified. You can
[build the image yourself]({{< relref "/docs/build" >}}). See
[Download & verify]({{< relref "/docs/getting-started/download" >}}).

### Why s6?

- **One small, auditable mechanism.** s6, s6-rc and s6-linux-init are small programs with a
  narrow job: be PID 1, supervise services, bring a dependency graph up and down. Less init
  code is less to review and less to go wrong, which is the "economy of mechanism" rule in
  {{< repo "docs/SECURITY-ASSURANCE.md" >}}.
- **Services you can read.** A service is a directory with a short `run` or `up` script and
  its dependencies, compiled into a database when the image is built.
- **Reproducible.** In Runink River's own-base scaffold, s6, skalibs and execline already
  build bit-for-bit reproducibly and link only the C library
  ({{< repo "docs/OWN-BASE.md" >}}, section 5.5).
- **Permissively licensed** (ISC), which suits a project whose own code is MIT.

### Why not systemd?

Runink River chose a different design: a small init and supervisor with a
separate, minimal set of services around it, rather than one project that also provides
logging, networking, DNS, time sync, home directories and more. "s6, never systemd" is
**invariant 1** of the project ({{< repo "AGENTS.md" >}}): no `.service` units, no `systemctl`,
and a change to it needs a two-thirds vote of the technical steering committee.

In practice: software that only ships systemd units needs an s6 service definition instead,
and tools that require a running systemd (`systemctl`, `systemd --user`) will not work. The
Plasma desktop runs on elogind and dbus, the same way other non-systemd distributions run it.

### Is Runink River an official Arch Linux distribution, or Arch-based?

**No.** Runink River is not made, reviewed or endorsed by Arch Linux. Today its image is
assembled with a fork of **Artix Linux's** ISO tooling and Artix packages, as a
**transitional** builder; the project is moving to its own from-source base
({{< repo "docs/OWN-BASE.md" >}}). An interim Arch Linux base was evaluated and deferred
({{< repo "docs/ARCH-BASE.md" >}}). It uses the pacman package format.

### Is the linux-runink AUR package official?

No. `linux-runink` and `zfs-linux-runink` are **community packages in the AUR**, not official
Arch Linux packages, and Arch Linux does not endorse them. Their publication is also still
pending. See [Kernel on Arch (AUR)]({{< relref "/docs/kernel-on-arch" >}}).

### Is there a server edition?

Not in Runink River. The project ships the workstation, its packaging and its installer. A
downstream distribution builds a server on the same base, outside this repository.

### Is there telemetry?

No. There is no telemetry, no phone-home behaviour and no third-party hosted AI service in the
image, by design.

### What is the licence?

MIT for Runink River's own code and documentation, GPL-2.0-only for the kernel packaging tree,
and CDDL-1.0 for the OpenZFS packaging tree, which is built as a separate module package and
never merged into the kernel. Third-party files keep their licences. The Runink name and marks
are trademarks and are not licensed. Details: {{< repo "docs/LICENSING.md" >}}.

## Installing

### Which hardware does it support?

UEFI firmware, an x86-64-v3 CPU (Intel Haswell or AMD Excavator and newer), 2 physical
cores, 8 GB of RAM and one internal disk of 64 GiB or more. The installer checks and tells
you what is missing. See [System requirements]({{< relref "/docs/getting-started#system-requirements" >}}).

### The installer says my computer is not supported.

It names the minimum that was missed. On a virtual machine or a test rig, "Try anyway (lab)"
(`--lab` for the text installer) waives the minimums; things may not work, and the plan
records that it was waived.

### Can I dual boot?

Not on the same disk: Runink River installs to **whole disks** and erases every disk you
confirm. Disks you do not confirm are not touched, so another system on a separate disk
stays as it is; pick it in your firmware's boot menu.

### My USB stick does not boot.

Check that you picked the stick's **UEFI** entry in the boot menu, that Secure Boot is **off**,
and that you wrote the image to the whole device (`/dev/sdX`), not a partition. Verify the
image's checksum before writing it.

### Which graphics drivers are included?

The image ships the Mesa drivers for AMD and Intel GPUs (with Vulkan and VA-API). NVIDIA's
proprietary driver is not included.

## After the install

### I forgot my disk passphrase.

Use the **recovery key** the installer showed you; it unlocks the disk at the same prompt.
Then set a new passphrase with `sudo zfs change-key zriver`. Without either, the data cannot be
recovered: there is no back door.

### Why does it ask for a passphrase at every boot?

Because the key is never stored on the machine. Unlocking with a TPM is
[planned]({{< relref "/docs/features/zfs-encryption#unattended-unlock" >}}).

### An update broke something.

Roll the boot environment back to the snapshot you took before the update:
[Boot environments & rollback]({{< relref "/docs/configuration/boot-environments" >}}).

### After a kernel update the old kernel still boots.

The EFI partition was probably not mounted at `/boot` during the update, so the new kernel
landed inside the encrypted boot environment where GRUB cannot see it. Check with
`findmnt /boot` and see [Updates]({{< relref "/docs/configuration/updates" >}}).

### Where is `curl`?

The installer removes the `curl` and `wget` programs from the installed system (step
`30-target-config`; their libraries stay, because NetworkManager needs them). `git`, `pacman`
and Firefox work as usual.

### Where are the logs? There is no `journalctl`.

There is no systemd journal. s6 sends service output to its catch-all logger, the kernel log
is read with `sudo dmesg` (the firewall logs dropped packets there), and Runink River's own
tools name their log files, for example `/var/log/river-perms.log`. See
[Services (s6)]({{< relref "/docs/configuration/services#logs" >}}).

### How do I let SSH in?

`sshd` runs, but the firewall closes port 22 until you open it: add `tcp 22` to
`/etc/runink/fw-open` and run `sudo runink-fw apply`
([Firewall]({{< relref "/docs/features/firewall#managing-it" >}})).

### Something else went wrong.

See [Troubleshooting]({{< relref "/docs/troubleshooting" >}}), which lists the error messages
by the text you see.

### Where do I report a bug?

In a [GitHub issue](https://github.com/org-runink/river/issues), with the version from
`/etc/runink-os-version`, the hardware, what you expected and what happened. Remove hostnames,
addresses and credentials from logs first. Security problems go through the
[private process]({{< relref "/docs/security/reporting" >}}).
