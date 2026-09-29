---
title: Updates
weight: 3
---
<!--
SPDX-FileCopyrightText: 2026 The RIVER Authors
SPDX-License-Identifier: MIT
-->

A workstation needs to install and update software, so Runink River keeps **pacman** on the
workstation (unlike a locked-down appliance, which would be updated only as a whole image).

Two kinds of repository feed `pacman -Syu`:

- **The distribution's repositories.** Today the image is assembled from Artix Linux
  packages, so everyday packages (Plasma, Firefox, libraries, tools) update from those
  repositories.
- **`[runink]`**, Runink River's own signed repository, for the packages it builds
  (`linux-runink`, `runink-zfs`, `runink-zfs-utils`, `runink-installer`, ...). The image
  configures it, ahead of the distribution's repositories, and trusts only the release key for
  it: [The [runink] package repository]({{< relref "package-repository" >}}).

{{< callout type="warning" >}}
**`[runink]` goes live with the first signed Runink River release.** Until then nothing is
published to it, and on a system whose `/etc/pacman.conf` has the `[runink]` stanza,
`pacman -Syu` stops with `error: failed to synchronize all databases`. Comment out the three
`[runink]` lines until the first release
([details]({{< relref "package-repository" >}})). `river-update`, which installs each
release into a new boot environment, is **planned** ([Roadmap]({{< relref "/docs/roadmap" >}})).
{{< /callout >}}

## Update the system

Snapshot the boot environment first, then update:

```bash
findmnt /boot                                                      # the EFI partition must be mounted
sudo zfs snapshot "$(findmnt -no SOURCE /)@pre-update-$(date +%Y%m%d)"
sudo pacman -Syu
```

## Install software

```bash
pacman -Ss <word>              # search the repositories
sudo pacman -S <package>       # install
sudo pacman -Rns <package>     # remove, with the dependencies nothing else needs
```

A package that ships only a systemd unit for its daemon needs an s6 service definition
before it can run as a service ([Services (s6)]({{< relref "services" >}})). The image carries
no container runtime, Flatpak or app store; installing one is your choice, outside what
Runink River supports and tests. Run code you do not trust under
[`river-sandbox`]({{< relref "/docs/features/sandbox" >}}).

If the update breaks something, [roll back]({{< relref "boot-environments#roll-back" >}}) to
the snapshot. Your files in `/home` are not part of the rollback.

## Kernel and ZFS updates

The kernel and its ZFS modules are **one unit**: the ZFS module must match the kernel
release exactly, and a kernel is only released when the pinned OpenZFS release supports its
series. When they change:

1. **Snapshot the boot environment** (above), or better, upgrade a
   [clone]({{< relref "boot-environments#keep-the-old-system-bootable-instead" >}}).
2. **Keep the previous packages** (`linux-runink`, `linux-runink-headers`, `runink-zfs`,
   `runink-zfs-utils`) so you can go back with `pacman -U`, offline.
3. **Upgrade kernel and ZFS in one transaction**, so `mkinitcpio -P` sees both.

The kernel and initramfs are written to `/boot`, the EFI partition. It must be mounted when
you update, or the new kernel lands inside the encrypted boot environment, where GRUB cannot
see it, and the machine keeps booting the old one. `findmnt /boot` shows it.

## Release notes

Changes that users notice are listed in {{< repo "CHANGELOG.md" >}}. Security fixes are
listed under **Security**, with their advisory or CVE ID once public.
