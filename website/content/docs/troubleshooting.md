---
title: Troubleshooting
weight: 4
description: "What Runink River's error messages mean and what to do about them: the USB stick, the installer, the passphrase prompt, a missing desktop, kernel updates, the firewall and the sandbox, plus a rescue procedure from the live medium."
---
<!--
SPDX-FileCopyrightText: 2026 The RIVER Authors
SPDX-License-Identifier: MIT
-->

The messages quoted on this page are copied from the source, so you can search for the text
you see. If your problem is not here, see
[Bug reports & feature requests]({{< relref "/docs/contributing-issues" >}}) for what to
collect before you open an issue.

## The USB stick

**The machine does not boot the stick, or boots its old system.**

- Pick the stick's **UEFI** entry in the firmware's boot menu (often F12, F11, F8 or Esc at
  power-on). Legacy BIOS boot is not supported.
- Turn **Secure Boot off**. GRUB on the image is not signed yet
  ([Secure Boot]({{< relref "/docs/security/secure-boot" >}})).
- Write the image to the **whole device** (`/dev/sdX`), not to a partition (`/dev/sdX1`),
  and verify it before you write it ([Verify a release]({{< relref "/docs/security/verify" >}})).

**`preflight: FAIL — not booted in UEFI mode (EFI required)`**: the stick was started from a
legacy (CSM) boot entry. Restart and pick its UEFI entry.

## The installer

**"This computer is not supported"**, or a plan with the verdict `refused`: the machine is
below a minimum, and the message names which one, for example
`6144 MiB RAM (MemTotal): the minimum is 7168 MiB (8 GB installed)`. The full list, and what **Try anyway (lab)** does, is on
[Hardware support]({{< relref "/docs/hardware" >}}).

**`no eligible target disk (need a non-removable, non-USB, unused disk of at least 64 GiB)`**:
no internal disk qualifies. The plan lists every disk it did not use with the reason
(`USB-attached`, `removable media`, `... is mounted at ...`, and so on). An external USB disk
is never a target, by design.

**"The disks changed since you confirmed them. Nothing was written."** (text installer:
`hwplan-verify: REFUSING — the disks changed since they were confirmed`): right before
writing, the installer probes the machine again and compares every target disk, by serial,
with what you confirmed. A disk was plugged in, removed or renumbered in between. Nothing was
erased; go back to "Your computer" and confirm the new plan.

**The text installer imported an old pool instead of erasing the disk.** `sudo runink-install`
looks for an importable ZFS pool first and, if it finds one, offers its name as the default:
pressing Enter imports it (a reinstall) rather than creating a fresh `zriver`. To start from
an empty disk, use the graphical installer's **Erase and install**, which always creates a new
pool.

**`disk-zfs: REFUSING — pool <name> is NOT encrypted`**: the text installer found an existing
pool created without encryption and will not install into it, because ZFS cannot encrypt
data in place. Install onto an erased disk instead, or migrate the data first
({{< repo "docs/ENCRYPTION.md" >}}, "Migrating an existing pool").

**A step failed.** The graphical installer shows which one, with **Show details** (the log)
and **Retry**. The live session's text console (Ctrl+Alt+F2, logged in automatically) is
available for looking around. Every step is a shell script under
`/usr/local/lib/runink-install/` on the live medium, named in the log, and the text installer
prints `installer: step <name> FAILED` and stops at the first failure.

**Messages on the account and Wi-Fi screens:**

| Message | Meaning |
|---|---|
| `Use at least 8 characters.` | the administrator password is too short |
| `That password contains characters that cannot be typed at start-up.` | pick a password made of characters the console keyboard can type |
| `Use lowercase letters, numbers, - and _, starting with a letter.` | the user name is invalid |
| `That name is used by the system. Choose another.` | the user name is reserved |
| `A Wi-Fi password has 8 to 63 characters.` | a WPA passphrase outside that length |

## The first boot

**The console asks for a passphrase and does not accept it.** It asks again after every wrong
attempt. Check, in order:

1. You are typing the **recovery key** exactly as shown, **without the spaces**: the
   installer prints it in eight groups of eight characters for readability, and the key is the
   64 characters run together. Or type your own passphrase, if you chose one.
2. The **keyboard layout**: the prompt uses the console keymap from `/etc/vconsole.conf`,
   which the installer set from your choice on the first screen. On a layout where digits
   need Shift (French AZERTY, for example), the digits of the recovery key need it too.
3. **Caps Lock**: the recovery key is lowercase hexadecimal (`0`-`9`, `a`-`f`).

There is no other way in: without the passphrase or the recovery key the data cannot be
read.

**`ZFS: Unable to import pool zriver.`** in the initramfs: the pool could not be imported
at all, for example because a disk of a mirror or RAID-Z set is missing or failing. Boot the
live medium and run `sudo zpool import` to see the pool's state and which devices it is
missing ([rescue below](#rescue-from-the-live-medium)). The boot entry already passes
`zfs_force=1`, so a pool that was not exported cleanly after a crash still imports.

**The desktop login screen appears, but signing in returns to it, or Plasma does not
start.** Usually `/home` is not mounted. The `zfs-mount` service reports
`zfs-mount: some ZFS datasets did not mount (zfs mount -a failed)` and lets the boot continue.
From a text console (Ctrl+Alt+F2 or F3):

```bash
zfs get -r mounted,mountpoint zriver     # zriver/home should be mounted at /home
sudo zfs mount -a                         # try again, and read the error it prints
s6-rc -a list | grep zfs-mount            # the service ran
```

## After an update

**The machine still boots the old kernel.** The EFI partition was not mounted at `/boot`
during the update, so the new kernel and initramfs were written into the `/boot` directory
of the encrypted boot environment, where GRUB cannot see them.

```bash
findmnt /boot                # must show the vfat EFI partition
grep ' /boot ' /etc/fstab    # the entry the installer wrote (vfat, nofail)
```

The fstab entry carries `nofail` on purpose, so a missing EFI partition never stops the boot;
it only means the partition is not there. Mount it (`sudo mount /boot`), then put the running
kernel back where GRUB looks, as install step `40-boot-grub-zfs` does:

```bash
ls /usr/lib/modules                                            # the installed kernel releases
rel=7.2.7-zen1-1-runink                                        # the one you want to boot
sudo cp /usr/lib/modules/"$rel"/vmlinuz /boot/vmlinuz-"$(cat /usr/lib/modules/"$rel"/pkgbase)"
sudo mkinitcpio -P
sudo grub-mkconfig -o /boot/grub/grub.cfg
```

**An update broke something else.** Roll the boot environment back to the snapshot you took
before it: [Boot environments & rollback]({{< relref "/docs/configuration/boot-environments" >}}).

## Networking and the firewall

**A service on this machine cannot be reached from another one.** Inbound connections are
dropped unless you opened the port. Dropped packets are logged to the kernel log, at most five
a minute:

```bash
sudo dmesg | grep 'runink-fw drop'      # IN=, SRC=, DPT= show what was refused
sudo runink-fw open tcp 8080            # open it until the next apply
echo 'tcp 8080' | sudo tee -a /etc/runink/fw-open && sudo runink-fw apply   # keep it open
```

**SSH is refused.** `sshd` runs, but the firewall closes port 22 until `/etc/runink/fw-open`
has a `tcp 22` line. Your account also needs a public key in `~/.ssh/authorized_keys`, which
the installer writes if you added one.

**A VM or container on this machine has no network.** Forwarding is dropped unless the
bridge's interface name is listed in `/etc/runink/fw-forward`. A listed interface is also
trusted for input to this machine, so list only bridges you control.

See [Firewall]({{< relref "/docs/features/firewall" >}}) and
[Networking]({{< relref "/docs/configuration/networking" >}}).

## river-sandbox

| Message | Meaning |
|---|---|
| `bwrap not found (package bubblewrap)` | bubblewrap is missing; it is on the image, so the system was changed |
| `--rw DIR: not a directory` | `--rw` takes an existing directory |
| `--rw /: refusing to bind the whole filesystem` | binding `/` read-write is never allowed |
| `--rw DIR: would expose FILE` | `DIR` contains a protected file (a secret); bind a narrower directory |
| `--rw DIR: overlaps protected TREE` | `DIR` is inside, equal to or above a protected tree such as `/etc/runink` |

The deny-list cannot be switched off, and a `--rw` that would expose it is refused rather than
trimmed. Details: [river-sandbox]({{< relref "/docs/features/sandbox" >}}).

## File modes changed at boot

`river-perms` corrects the owner and mode of every secret file at every boot and logs each
correction. A line in `/var/log/river-perms.log` means something wrote a secret with the wrong
mode; the line says which file and from which mode. To check without changing anything:

```bash
sudo river-perms --check     # exit status 1 if anything has drifted
```

## Rescue from the live medium

When the installed system does not boot, start the Runink River USB stick and open Konsole in
the live session (the live user has `sudo` without a password). This is the same sequence the
installer uses:

```bash
sudo zpool import                                  # list the pools the live system can see
sudo zpool import -N -f -R /mnt zriver             # import without mounting, rooted at /mnt
sudo zfs load-key zriver                           # your passphrase or recovery key
zfs list -r zriver/ROOT                            # find the boot environment's name
sudo zfs mount zriver/ROOT/<be>                    # the system, at /mnt
sudo zfs mount -a                                  # /home and the rest, under /mnt
lsblk -o NAME,PARTTYPE,FSTYPE,MOUNTPOINT          # the EFI partition: PARTTYPE c12a7328-...
sudo mount /dev/<efi-partition> /mnt/boot
for fs in dev proc sys; do sudo mount --rbind /$fs /mnt/$fs; done
sudo chroot /mnt /bin/sh                           # repair: mkinitcpio -P, grub-mkconfig, pacman ...
```

When you are done, leave the chroot and **always export the pool** before you reboot:

```bash
exit
for fs in sys proc dev; do sudo umount -R /mnt/$fs; done
sudo umount /mnt/boot
sudo zfs umount -a
sudo zpool export zriver
```

{{< callout type="error" >}}
**Do not skip `zpool export`.** A pool left imported keeps the `/mnt` root it was imported
with; the next boot then mounts the system in the wrong place and the kernel panics
("Attempted to kill init"). Exporting clears it, and also unloads the key.
{{< /callout >}}
