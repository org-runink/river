---
title: First boot
weight: 4
---
<!--
SPDX-FileCopyrightText: 2026 The RIVER Authors
SPDX-License-Identifier: MIT
-->

## What you see

{{% steps %}}

### The boot menu

The Runink River GRUB menu is shown for five seconds, then starts the default entry. The
kernel, the initramfs and the menu live on the unencrypted EFI partition; everything else is
on the encrypted pool.

### Unlock the disk

The boot splash appears and the initramfs asks for the passphrase of the pool (`zriver`,
unless you named it differently in the text installer). Type the **recovery key** the
installer showed you, the 64 characters **without the spaces**, or your own passphrase if you
chose one in the text installer. A wrong entry simply asks again.

Every boot asks, because the key is never stored on the machine. Unattended unlock with a
TPM is [planned]({{< relref "/docs/features/zfs-encryption#unattended-unlock" >}}), not
built.

### Sign in

The Runink River login screen (SDDM) appears. Sign in with the account you created in the
installer. You get a KDE Plasma session on Wayland with Konsole, Dolphin, Kate and Firefox,
plus Okular, Gwenview, Spectacle, Ark, KCalc, Elisa, Dragon Player and KDE Partition Manager.
The network the installer set up is already connected.

{{% /steps %}}

Konsole opens fish, your login shell, which greets you with the Runink River mark and a
summary of the machine (fastfetch). `set -Ux RUNINK_GREETING static` keeps the summary
without the animation; `set -Ux RUNINK_GREETING off` turns the greeting off.

## What is installed for development

`base-devel` (the C and C++ toolchain, `make`), `go`, `git`, `openssh`, `rsync`, `vim` and
`zstd` come with the image, and `pacman` installs more from the distribution's repositories
([Updates]({{< relref "/docs/configuration/updates" >}})). There is no container runtime,
Flatpak or app store in the base image, by design. The installer removes the `curl` and
`wget` programs from the installed system; `git` works as usual.

Use [`river-sandbox`]({{< relref "/docs/features/sandbox" >}}) to build or run code you do
not trust.

## Check the system

A few commands confirm that the machine came up as designed:

```bash
cat /etc/runink-os-version                            # the installed version, runink-os-YYYY.MM
uname -r                                              # the kernel, 7.2.7-zen1-1-runink
findmnt -no SOURCE /                                  # the boot environment, zriver/ROOT/runink
zfs get -r -o name,value encryption zriver            # aes-256-gcm on every dataset
zpool status zriver                                   # pool health and layout
findmnt /boot                                         # the EFI partition (vfat) at /boot
s6-rc -a list                                         # the services s6 is running
sudo runink-fw list                                   # the firewall table, inet runink_fw
swapon --show                                         # the zram swap device
cat /sys/module/zfs/parameters/zfs_arc_max            # the ZFS cache limit from the plan
sudo river-perms --check                              # secret file modes; exit 0 = no drift
```

`/etc/runink/install-plan.json` (root only) records what the installer measured and
decided, including the ZFS cache limit (`zfs.arc_max_bytes`, one sixteenth of RAM, between
1 GiB and 16 GiB) and the zram size.

## Next steps

- **Store the recovery key safely.** It is the only way into the data if you forget your
  passphrase.
- **Choose your own passphrase**, if you like, with `sudo zfs change-key zriver`. ZFS keeps
  exactly one passphrase per encryption root, so the new passphrase **replaces** the recovery
  key: from then on, the passphrase you chose is the only key. Store it as carefully.
- **Snapshot before you change anything big**:
  [Boot environments & rollback]({{< relref "/docs/configuration/boot-environments" >}}).
- **Open SSH** if you need it: add `tcp 22` to `/etc/runink/fw-open` and run
  `sudo runink-fw apply` ([Firewall]({{< relref "/docs/features/firewall" >}})).
- **Keep it current**: [Updates]({{< relref "/docs/configuration/updates" >}}).

Something did not come up as described? See [Troubleshooting]({{< relref "/docs/troubleshooting#the-first-boot" >}}).
