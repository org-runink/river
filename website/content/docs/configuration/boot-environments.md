---
title: Boot environments & rollback
linkTitle: Boot environments
weight: 1
---
<!--
SPDX-FileCopyrightText: 2026 The RIVER Authors
SPDX-License-Identifier: MIT
-->

The whole system (everything except `/home`) lives in one ZFS dataset, the **boot
environment** (BE): `zriver/ROOT/<be>`, created by the installer with `canmount=noauto
mountpoint=/`. A ZFS snapshot of it is instant and costs nothing until files change, so taking
one before any risky change is cheap insurance.

{{< callout type="info" >}}
**Status.** There is no boot-environment manager command yet: the steps below use `zfs` and
GRUB directly. A `river-update` tool that installs each release into a **new** BE, and rolls
back by booting the previous one, is **planned** for Runink River's own base
({{< repo "docs/OWN-BASE.md" >}}, section 6.4).
{{< /callout >}}

## Find your boot environment

```bash
zfs list -o name,used,mountpoint -r zriver/ROOT
findmnt -no SOURCE /          # the BE you are running
grep -o 'root=ZFS=[^ ]*' /proc/cmdline
```

The graphical installer names it `runink`, so the BE is `zriver/ROOT/runink`; the text
installer offers `runink-<version>` (for example `zriver/ROOT/runink-2026.07`). The examples
below use `zriver/ROOT/runink`; replace it with yours.

Your files in `/home` are a separate dataset, `zriver/home`, beside `zriver/ROOT`, and are
**not** affected by anything on this page. Snapshot them separately if you want.

## Snapshot before a change

```bash
sudo zfs snapshot zriver/ROOT/runink@pre-update
zfs list -t snapshot -o name,used,creation -r zriver     # every snapshot, with its size
```

A snapshot's `used` grows as the files it still references change or are deleted in the live
system. Old snapshots keep that space allocated, so remove the ones you no longer need:

```bash
sudo zfs destroy zriver/ROOT/runink@pre-update
zfs list -o space -r zriver                              # where the space goes, snapshots included
```

To look inside a snapshot without rolling back, read it from the hidden `.zfs` directory:
for the BE, `/.zfs/snapshot/pre-update/` (copy a single file back from there); for your files,
`/home/.zfs/snapshot/<name>/`.

## Roll back

A rollback returns the BE to the snapshot and discards every change made to it since. Do it
from the live USB stick, so the system you roll back is not the one running:

```bash
# in the live session (Konsole)
sudo zpool import -N zriver
sudo zfs load-key zriver                            # your passphrase or recovery key
sudo zfs rollback -r zriver/ROOT/runink@pre-update
sudo zpool export zriver                            # always export before rebooting
```

`-r` also destroys any snapshots taken after `pre-update`. Without it, `zfs rollback` refuses
when newer snapshots exist. The `zpool export` is required: see the warning at the end of
[the rescue procedure]({{< relref "/docs/troubleshooting#rescue-from-the-live-medium" >}}).

## Keep the old system bootable instead

For a bigger change (a kernel series upgrade, say), keep the current BE untouched and change
a **clone** of it:

```bash
sudo zfs snapshot zriver/ROOT/runink@base
sudo zfs clone -o canmount=noauto -o mountpoint=/ \
    zriver/ROOT/runink@base zriver/ROOT/runink-next
```

The boot entry names its BE on the kernel command line, `root=ZFS=zriver/ROOT/<be>`, which the
installer wrote to `/etc/default/grub.d/10-runink-zfs.cfg`.

**Boot the clone once.** At the GRUB menu, press `e` on the Runink River entry, change
`root=ZFS=zriver/ROOT/runink` to `root=ZFS=zriver/ROOT/runink-next`, and boot with Ctrl+X.
Nothing is saved: the next boot uses the old BE again.

**Make the clone the default.** Boot into it, then change the dataset name in
`10-runink-zfs.cfg` and regenerate the menu, with the EFI partition mounted at `/boot`:

```bash
sudoedit /etc/default/grub.d/10-runink-zfs.cfg        # root=ZFS=zriver/ROOT/runink-next
sudo grub-mkconfig -o /boot/grub/grub.cfg
```

Each BE has its own copy of that file, so a BE you boot keeps pointing at itself only if its
own copy says so.

**Retire the old BE** once the clone has proved itself. A clone depends on the snapshot it was
made from; `zfs promote` reverses that, so the old BE can be destroyed:

```bash
sudo zfs promote zriver/ROOT/runink-next
sudo zfs destroy -r zriver/ROOT/runink
```

{{< callout type="warning" >}}
**The kernel is shared.** The kernel and initramfs live on the EFI partition, not in the BE,
so a clone that upgrades the kernel also changes what the old BE boots with. The old BE's
modules in `/usr/lib/modules/` no longer match the new kernel. For a kernel upgrade, keep the
previous kernel packages at hand for a downgrade (`pacman -U`), as described in
[Updates]({{< relref "updates" >}}).
{{< /callout >}}
