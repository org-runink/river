# Runink River: encryption at rest and shadow-grade files

The owner's requirement: *"All files must be encrypted, handled as /etc/shadow."* On a
Runink River node that means three things:

1. **Every file sits on encrypted disk.** The kernel's filesystem layer (OpenZFS native
   encryption) enforces it: one aes-256-gcm encryption root, inherited by every dataset.
2. **Every secret or state file is root-only.** It gets `/etc/shadow`-style modes: 0600 in
   0700 directories, owned by the one principal that reads it. The `river-perms` oneshot
   re-asserts this on every boot and logs any drift.
3. **Backups leave the box still encrypted.** They are raw `zfs send -w` streams, never
   decrypted to be sent.

> **Status: booted in QEMU, not on hardware.** A pool was created, unlocked at the initramfs
> prompt and checked by `assert-golden.sh` on 2026-09-25; the import path, send/receive and
> key providers are still untested. See [Tested, and untested](#tested-and-untested).

## Existing installs cannot be encrypted in place

ZFS encrypts a dataset only when it is **created** encrypted. There is no `zfs set
encryption=on` for existing data. Every node installed before this change stays plaintext
until it is migrated with send/recv into a new encrypted root ([Migrating an existing pool](#migrating-an-existing-pool)). The installer
enforces this: it **refuses** to import an unencrypted pool unless
`RUNINK_ALLOW_PLAINTEXT_POOL=1` is set. `runink-backup.sh` warns on every start when the BE
is plaintext, and `tests/assert-golden.sh` fails on any unencrypted dataset.

## Pool layout (`installer/lib/10-disk-zfs.sh`)

```
nvme0n1p1  ESP, FAT32, 1 GiB        -> /boot      UNENCRYPTED: kernel, initramfs, GRUB, grub.cfg
nvme0n1p2  zriver                   encryption=aes-256-gcm  keyformat=passphrase  keylocation=prompt
           ├─ zriver/ROOT/<be>      /             (inherits: encrypted)
           ├─ zriver/home           /home         (inherits)
           ├─ zriver/state          /var/lib/core (inherits) registry, models, payload data, Litestream dirs
           └─ zriver/containers     /var/lib/k0s  (inherits) containerd + kine
```

The **pool root is the encryption root**, so every dataset created later, by the
installer, by an operator or by a future migration, inherits encryption, and nothing can
opt out below it. There is no swap zvol: swap is zram, which is RAM.

### Why the ESP moved to `/boot`

GRUB's ZFS reader refuses a pool with the `encryption` feature active. That is a pool-wide
property, so an unencrypted `/boot` dataset in the same pool cannot help. The kernel and
initramfs therefore have to live outside the encrypted pool. There were two options:

- **ESP at `/boot` (chosen).** The existing 1 GiB FAT partition holds the kernel, a default
  and a fallback initramfs, and GRUB with its theme. GRUB no longer needs its `zfs` module
  at all. This is the stock Arch layout, so mkinitcpio and the kernel package's pacman hook
  write to `/boot` with no preset changes.
- **A separate unencrypted `bpool`** with GRUB's restricted feature set. This costs a third
  partition, a second pool to import and keep feature-compatible, and a second hostid
  consideration, and it buys nothing the ESP does not already provide.

What stays unencrypted is code and public configuration: the kernel, the initramfs (hooks,
the hostid, key-provider scripts), GRUB and `grub.cfg` (which names the pool/BE). No node
state and no key material. The ESP is mounted `fmask=0077,dmask=0077`.

## Key handling

- **Generated in memory.** `10-disk-zfs` reads 32 bytes from `/dev/urandom` as 64 hex
  characters into a shell variable of that process only. The variable is not exported and
  not passed to another step, and the key is never written to any file, not even tmpfs.
  It is piped into `zpool create` on stdin (`keylocation=prompt` reads stdin when it is not
  a TTY), then `unset`.
- **`keyformat=passphrase` (working default).** The 64-hex string *is* the passphrase, so
  it can be typed at a console. `RUNINK_ZFS_KEY=own` asks for an operator passphrase
  instead: typed twice with echo off, minimum 12 characters.
- **Recovery key printed once.** The 64 hex characters are shown in 8 groups on stderr
  during step 10 (the TUI waits for Enter). Nothing stores them. ZFS has exactly **one**
  wrapping key per encryption root, with no LUKS-style key slots, so the recovery key and
  the boot-time key are the same secret. Losing it with no provider installed means losing
  the node's data. (The generated key is only as private as the console it is shown on:
  `tests/vm-boot-test.sh` runs `-nographic`, so the key lands in that serial log.)
- **Boot unlock today.** The vendored `zfs` initcpio hook prompts on the console for the
  passphrase for `<pool>`. That makes the box attended until a provider exists: a headless
  node waits at the prompt (IPMI/serial-over-LAN works; there is no in-initramfs SSH).
- **Rotation.** `zfs change-key zriver` rewraps the master key under a new passphrase
  without rewriting any data. Old raw backups stay readable with the key that was current
  when they were sent.

### Key providers

The runink-zfs-utils `zfs` hook (`build/pkgbuilds/runink-zfs/zfs-utils.initcpio.{install,hook}`,
vendored from archzfs) already has the extension point. **Every regular file** in
`/etc/zfs/initramfs-tools-load-key.d/` is copied into the initramfs and **sourced in a
subshell before the console prompt**. This is the interface for unattended unlock. The
template, which is not active, is `/usr/share/runink/zfs/key-provider.example`.

**Contract**

| | |
|---|---|
| Location | `/etc/zfs/initramfs-tools-load-key.d/<name>`, root:root 0600, dir 0700. Nothing else may live there, because every file in it is executed at boot. |
| Shell | busybox ash (the initramfs), POSIX only |
| Inputs | `DATASET`, `ENCRYPTIONROOT` (on Runink River, the pool root), `KEYLOCATION` (`prompt`), `BOOT_DATASET`, `ZFS`, `ZPOOL` |
| Success | pipe the passphrase to `"$ZFS" load-key -L prompt "$ENCRYPTIONROOT"` and return 0 only if the key is now loaded |
| Failure | return non-zero, quietly and quickly. The hook tries the next provider, then prompts on the console. Never block unboundedly. |
| Key handling | the passphrase lives only in a pipe or a variable, never in a file, not even inside the initramfs |
| Payload | extra binaries or blobs come in through a mkinitcpio install hook listed in `HOOKS` before `zfs` (`add_binary`/`add_file`). A blob in the image lands on the unencrypted ESP, so it must be useless off this machine (TPM-sealed), never the passphrase itself. |
| Rebuild | `mkinitcpio -P` after adding, changing or removing a provider |

A provider only **re-delivers** the recovery-key passphrase. It never introduces a second
key.

### Follow-up: TPM2 unattended unlock (for the own userland base)

Not in this change. No TPM tooling was added: Runink River is moving off Artix to the owner's own
userland base, and Arch/Artix `tpm2-tools` pulls the `curl` package (and `tpm2-tss` links
libcurl) into the node. The own base has to build:

1. **`tpm2-tss`**: `libtss2-esys`, `-mu`, `-rc`, `-sys`, `-tctildr`, `-tcti-device`,
   configured **without** libcurl (`--disable-fapi`: FAPI is the only libcurl consumer,
   used for EK-certificate download, which a sovereign node never does) and without
   json-c.
2. **A minimal unseal tool** (`river-tpm-unseal`), not `tpm2-tools`. It should do three
   things only: create the owner-hierarchy primary, load the sealed object, and unseal it
   under a PCR policy session, writing the secret to stdout. That can be a small C program
   against `libtss2-esys` plus `tcti-device`, or a static Go binary over `/dev/tpmrm0`
   (go-tpm, pure Go, no tss at all), built off-node like every other component. No curl,
   no network.
3. **A sealing counterpart** for install and reseal (the same tool with a `seal` verb). It
   seals the passphrase from stdin under a PCR policy (proposed default: PCRs 0, 2, 7) to
   `/etc/zfs/keys/zfs-key.sealed` (root:root 0600, on the encrypted pool; `river-perms`
   already covers `/etc/zfs/keys`).
4. **A mkinitcpio install hook** (`river-tpm2`, before `zfs`) that adds the tool, the tpm
   kernel modules the kernel builds as modules, and the sealed blob. Plus the provider
   itself, which is the skeleton at the bottom of the template.

Threat-model note to carry into that work: PCRs 0, 2 and 7 protect a stolen or RMA'd disk
(the blob only unseals on this TPM with this firmware state). They do **not** stop an
attacker who holds the whole box and edits the kernel command line on the unencrypted ESP.
Binding PCRs 8 and 9 (GRUB command line and loaded files) closes that gap, but then every
kernel or GRUB update needs a reseal.

## Shadow-grade files (`river-perms`)

`iso-profiles/river/root-overlay/usr/local/bin/river-perms` holds the table. It runs as
the s6-rc oneshot `etc/s6/sv/river-perms`, enabled with `s6 set enable` (a
live-session service in `profile.yaml`, re-asserted on the target by `80-enable-s6`) and
dependent on `mount-filesystems`. `rc.local`
calls it again as belt-and-braces. It **fixes and logs** drift (stdout and
`/var/log/river-perms.log`, 0600) and never fails the boot. `river-perms --check` only
reports, and exits 1 on drift; `assert-golden.sh` uses that mode.

| Path | Owner | Mode |
|---|---|---|
| `/etc/runink/` recursively: enrollment.env, k0s.env, net.env, he-tunnel.env, backup.env, fw-whitelist, fw-enabled, app-hosts, any payload-supplied CA bundle, backup SSH keys, any Litestream config placed there | root:root | dirs 0700, files 0600 |
| `/var/lib/runink/` (enrollment sentinel, backup-last-sent, DEPLOY-INCOMPLETE) | root:root | 0700 / 0600 |
| `/etc/k0s`, `/etc/k0s/join-token`, `/var/lib/k0s/pki/admin.conf` | root:root | 0700, 0600, 0600 |
| `/etc/zfs/initramfs-tools-load-key.d/`, `/etc/zfs/keys/` (key providers, sealed blobs) | root:root | 0700 / 0600 |
| `/etc/litestream.yml` (if a node has one host-side) | root:root | 0600 |
| `~runink/.ssh`, `authorized_keys` | runink | 0700, 0600 |
| `~runink/.runink/` (secrets delivered at enrollment), `~runink/edge/` (oidc.env) | runink | 0700 / 0600 |
| `~runink/actions-runner/` and its `.credentials`, `.credentials_rsaparams`, `.runner`, `.env` | runink | 0700, 0600 |
| `/etc/shadow`, `/etc/gshadow`, `/etc/ssh/ssh_host_*_key` | root:root | 0600 |

The runner and the runink secret dirs are owned by `runink` because that user **is** the
reader: the runner runs as runink. Every other reader is a root process: the keepalives
from rc.local, the NetworkManager dispatcher, runink-fw and k0s. No entry needs a
group-readable 0640 today. If a service group ever has to read one, it gets root:<group>
0640 in the table, never 0644.

Writers were hardened to match. `runink-firstboot.sh` writes its secrets under `umask 077`
and no longer runs `install -d -m 755 /etc/runink`. That line used to widen the directory
on every enrollment that set a firewall whitelist or the HE tunnel. `30-target-config`
sets the table's `/etc/runink` and `/var/lib/runink` modes at install, because
`runink-autoinstall` skips step 70. `50-runink-user` creates `~runink/edge` as 0700.

Litestream: there is no host-side Litestream config in this repo. Workloads that use
Litestream run it in pods, with config from their own k8s manifests and Secrets (supplied
by a downstream payload, not by Runink River). Its working directories live on `zriver/state`,
which is encrypted like everything else.

## Backups (`runink-backup.sh`)

Off-box sends are now `zfs send -w -R` (full) and `zfs send -w -RI` (incremental), piped to
`ssh $BACKUP_TARGET zfs receive -u -F <dataset>`. The blocks leave the box exactly as they
sit on disk, still encrypted with the dataset key. The receiver stores them with
`keystatus=unavailable` and never holds the key. A compromised backup host leaks
ciphertext only.

**Transport stays rsync/SSH.** An in-cluster HTTP object store is *not* the target: it has
no SSH ingest, and reaching it would need an HTTP client on the node, which the hardening
invariant forbids. To land copies in such a store's storage, run the `zfs receive` on the
host that backs it; the stream stays raw either way.

**Restore:** `ssh backup zfs send -w -R backup/runink@<snap> | zfs receive -u <pool>/restore`,
then `zfs load-key -L prompt <pool>/restore` (a raw-received dataset becomes its own
encryption root, keyed with the passphrase that was current at send time), then mount it
or promote it.

**Scope (unchanged):** the backup snapshots the BE. `zriver/state` (`/var/lib/core`) and
`zriver/containers` (`/var/lib/k0s`) are siblings outside it by design (see the long
comment in `10-disk-zfs.sh`), so payload data and Litestream working copies are not in
these streams. They are encrypted at rest, but backing them up is a separate decision.

## Migrating an existing pool

For a node whose pool predates this change (`encryption=off` on the pool root). **Take an
off-box backup first**, because a mistake here destroys data. Either route runs from the
Runink River live ISO, so the pool is not in use.

**A. Reinstall onto a new or wiped disk, then pull the data across (recommended).**

```sh
# 1. fresh encrypted install onto the NEW disk (runink-install); record the recovery key
# 2. from the live ISO, with both pools visible:
zpool import -N -f zold
zpool import -N -f zriver && zfs load-key zriver
zfs snapshot -r zold/home@migrate
zfs send -R zold/home@migrate | zfs receive -u zriver/home-migrated
#    NOT -w: a plain stream received under an encrypted parent is encrypted by
#    inheritance. Check it before trusting it:
zfs get -r encryption,encryptionroot zriver/home-migrated   # aes-256-gcm / zriver
# 3. swap mountpoints (zfs rename / set mountpoint), repeat for any other dataset worth
#    keeping, export both pools, then wipe the old disk (zpool labelclear + blkdiscard)
```

**B. Same disk, in place.** This needs free space at least equal to the used space.

```sh
zpool import -N -f zold
zfs create -o encryption=aes-256-gcm -o keyformat=passphrase -o keylocation=prompt zold/enc
zfs snapshot -r zold/ROOT@migrate zold/home@migrate           # every top-level dataset
zfs send -R zold/ROOT@migrate | zfs receive -u zold/enc/ROOT
zfs send -R zold/home@migrate | zfs receive -u zold/enc/home
zfs get -r encryption zold/enc                               # all aes-256-gcm
zfs destroy -r zold/ROOT && zfs destroy -r zold/home         # only after verifying
zfs set mountpoint=/home zold/enc/home
zpool set bootfs=zold/enc/ROOT/<be> zold
```

Here the encryption root is `zold/enc`, not the pool root, so only datasets under it are
encrypted. Create everything new under `zold/enc`. Route A gives the clean layout.

**Both routes must also move the boot layout before the first reboot.** Once any dataset
in the pool is encrypted, GRUB can no longer read `/boot` on that pool. Mount the ESP at
`/boot`, copy `/usr/lib/modules/<rel>/vmlinuz` to `/boot/vmlinuz-<pkgbase>`, run
`mkinitcpio -P` and `grub-install --efi-directory=/boot --removable`, set `root=ZFS=` to
the new BE, run `grub-mkconfig -o /boot/grub/grub.cfg`, and replace the fstab `/boot/efi`
line with the `/boot` one from [INSTALL.md](INSTALL.md). Skipping this leaves a node that
does not boot.

The old plaintext blocks stay on the disk until they are overwritten. Route A's wipe, or a
`zpool trim` on SSDs with autotrim, is what actually retires them.

## Tested, and untested

Booted in QEMU (UEFI, virtio disk) on 2026-09-25 with `build/qemu-test.sh --lab`, on the
first server ISO with the installer fixes of that date overlaid (`--kit-overlay`): a fresh
encrypted pool (`zpool create` with the key piped on stdin), the recovery key printed once,
the GRUB path on the ESP (`grub-install --efi-directory=/boot`, `grub-mkconfig`), the
initramfs passphrase prompt answered with the recovery key, every dataset encrypted and
mounted, `river-perms` run by s6 at boot, `assert-golden.sh` including the encryption block,
and the model payload unpacked into `<pool>/models`.

- **Untested:** `zfs load-key -r` on the import path (reinstall into an existing pool),
  the raw send/receive round trip, a TPM key provider, and real hardware.

