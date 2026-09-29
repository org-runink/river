---
title: ZFS & encryption
weight: 3
---
<!--
SPDX-FileCopyrightText: 2026 The RIVER Authors
SPDX-License-Identifier: MIT
-->

Runink River installs onto a **ZFS root** with **native encryption**. The rule is simple:
every file on the disk is encrypted, and every secret file on the system is treated like
`/etc/shadow`. The reference is {{< repo "docs/ENCRYPTION.md" >}}; the code is
{{< repo "installer/lib/10-disk-zfs.sh" >}}.

## The layout

```text
nvme0n1p1  EFI partition, FAT32, 1 GiB  -> /boot   unencrypted: kernel, initramfs, GRUB
nvme0n1p2  zriver    encryption=aes-256-gcm  keyformat=passphrase  keylocation=prompt
           ├─ zriver/ROOT            (no mountpoint)   holds the boot environments
           │  └─ zriver/ROOT/<be>    /                 the system (canmount=noauto)
           └─ zriver/home            /home             your files
```

The graphical installer names the boot environment `runink` (`zriver/ROOT/runink`); the
text installer asks, and offers `runink-<version>`. The pool is created with these
properties, which every dataset inherits:

| Property | Value | Why |
|---|---|---|
| `encryption` | `aes-256-gcm` | set on the pool root, so the pool root is the **encryption root** |
| `keyformat`, `keylocation` | `passphrase`, `prompt` | the key is typed at the console at boot |
| `compression` | `zstd` | |
| `atime` | `off` | no write for every read |
| `xattr`, `acltype` | `sa`, `posixacl` | extended attributes and POSIX ACLs stored efficiently |
| `ashift` | `12` | 4 KiB sectors |
| `autotrim` | `on` | TRIM for SSDs |
| `mountpoint` | `none` on the pool root | only the datasets below it mount |

Because the pool root is the encryption root, every dataset created later, by the installer,
by you or by an update, inherits aes-256-gcm, and nothing can opt out below it: ZFS children
inherit `encryption`, and it cannot be turned off later. The installer checks right after
`zpool create` that the pool really is encrypted and aborts if it is not. Swap is zram, in
RAM, so there is no unencrypted swap device. With more than one disk the pool becomes a mirror
or RAID-Z ([the planner]({{< relref "installer#how-the-disks-are-laid-out" >}})).

The EFI partition, mounted at `/boot` with `fmask=0077,dmask=0077` (root only), is the
**only unencrypted filesystem**. It holds code and public configuration (the kernel, the
initramfs with the hostid, GRUB and its menu), never your data or a key. It exists because
GRUB cannot read a pool that uses ZFS encryption. Its `/etc/fstab` line carries `nofail`, so a
missing EFI partition never stops the boot.

### Check it

```bash
zfs get -r -o name,property,value encryption,encryptionroot,keystatus zriver
zfs list -o name,used,avail,mountpoint -r zriver
zpool get ashift,autotrim,bootfs zriver
```

Every dataset should show `aes-256-gcm`, encryption root `zriver` and key status
`available` while the system runs.

## The key

- **Generated in memory** by the installer: 32 random bytes from `/dev/urandom`, as 64
  hexadecimal characters, held in one process only and piped to `zpool create` on its standard
  input. It is never written to any file, not even in RAM-backed `/tmp`.
- **Shown once** as the recovery key: on screen and as a QR code by the graphical installer,
  in eight groups of eight characters by the text installer. Type it **without the spaces**.
- **The same secret as the boot passphrase.** ZFS has exactly one wrapping key per encryption
  root (there are no LUKS-style key slots), so the recovery key is what the machine asks for at
  every boot, unless you chose your own passphrase in the text installer.
- **Asked for at every boot** by the initramfs's `zfs` hook, which retries until the right
  key is typed.

### Change it

```bash
sudo zfs change-key zriver       # prompts for the new passphrase, twice
```

`zfs change-key` re-wraps the pool's master key under the new passphrase without rewriting
any data. Because there is one wrapping key, **the new passphrase replaces the recovery key**:
the old key stops working at once. Record the new one before you reboot.

{{< callout type="error" >}}
Lose the passphrase and the recovery key, and the data is gone. There is no back door.
{{< /callout >}}

### Unattended unlock

Unlocking with a TPM 2.0 chip is **planned**, not built. The initramfs already has the
extension point: every file in `/etc/zfs/initramfs-tools-load-key.d/` (root only, 0600 in a
0700 directory) is copied into the initramfs and sourced **before** the console prompt. Such
a key provider only re-delivers the same passphrase, and must fail quickly so the prompt
follows. The TPM tool that will fill it is planned for Runink River's own base, without the
network libraries the usual TPM tool stack pulls in. Until then, every boot is attended. The
provider contract and the threat model are in {{< repo "docs/ENCRYPTION.md" >}}, "Key providers".

## Memory: the ZFS cache and zram

OpenZFS's own default lets its cache (the ARC) take most of the RAM, which suits a file
server and not a desktop. The installer caps it at one sixteenth of the RAM, at least 1 GiB
and at most 16 GiB, in `/etc/modprobe.d/zfs.conf`:

```text
options zfs zfs_arc_max=<bytes>
```

The `zfs` initramfs hook copies this file into the initramfs, so the limit applies from the
moment the pool is imported; after editing it, run `sudo mkinitcpio -P`. The running value is
in `/sys/module/zfs/parameters/zfs_arc_max`.

Swap is a zstd-compressed **zram** device sized by the install plan (half the RAM below
32 GiB, a quarter above, at most 16 GiB; recorded in `/etc/runink/zram.conf`), set up by `runink-zram.sh` from `rc.local` at every boot, with
`vm.swappiness=150` and `vm.page-cluster=0` (`/etc/sysctl.d/99-runink-workstation.conf`),
the settings that suit RAM-backed swap.

## Boot environments

The system lives in a **boot environment**, `zriver/ROOT/<be>`, separate from `/home`.
Snapshot it before an update and you can return to the exact previous system with one
`zfs rollback`, without touching your files.
See [Boot environments & rollback]({{< relref "/docs/configuration/boot-environments" >}}).

## Back up, still encrypted

A **raw** send (`zfs send -w`) copies the blocks exactly as they are on disk, still encrypted.
The receiving side stores them without ever holding the key, so a backup disk or host that is
lost or compromised leaks only ciphertext:

```bash
sudo zfs snapshot -r zriver/home@backup-1
sudo zfs send -w -R zriver/home@backup-1 | sudo zfs receive -u backup/home   # a pool on another disk
```

A raw-received dataset is its own encryption root, keyed with the passphrase that was current
when it was sent; load it with `zfs load-key -L prompt <dataset>` to read it.

## Shadow-grade files

Every secret or machine-state file is owned by the one account that reads it, mode 0600, in a
0700 directory. The `river-perms` oneshot re-asserts this table **on every boot** and logs any
drift to `/var/log/river-perms.log` (0600); `sudo river-perms --check` reports without
changing anything and exits 1 on drift.

| Path | Owner | Mode |
|---|---|---|
| `/etc/runink/` (the install plan, `fw-open`, `fw-forward`, `zram.conf`) | root | dirs 0700, files 0600 |
| `/var/lib/runink/` (machine state) | root | 0700 / 0600 |
| `/etc/zfs/initramfs-tools-load-key.d/`, `/etc/zfs/keys/` | root | 0700 / 0600 |
| `~runink/.ssh`, `~runink/.ssh/authorized_keys` | `runink` | 0700, 0600 |
| `/etc/shadow`, `/etc/gshadow`, `/etc/ssh/ssh_host_*_key` | root | 0600 |

A new secret path belongs in the table in `/usr/local/bin/river-perms`; that is a rule for
contributors ({{< repo "AGENTS.md" >}}, invariant 4).

## Status

An encrypted install was booted and checked in QEMU on 2026-09-25: a fresh encrypted pool, the
recovery key printed once, the passphrase prompt answered, every dataset encrypted and mounted,
`river-perms` run by s6 at boot. `build/qemu-gui-test.sh` repeats the workstation install and
unlock end to end. **Still untested:** re-installing into an existing encrypted pool, the raw
send/receive round trip, a TPM key provider, and the GRUB-on-ESP layout on real hardware.
