---
title: The [runink] package repository
linkTitle: Package repository
weight: 4
description: "The signed [runink] pacman repository: what it carries, the pacman.conf stanza and mirrorlist the image installs, the release key, the mirrors, adding it by hand, verifying a package, updating and rolling back."
---
<!--
SPDX-FileCopyrightText: 2026 The RIVER Authors
SPDX-License-Identifier: MIT
-->

`[runink]` is Runink River's own pacman repository. It lets an installed system update the
packages Runink River builds (the kernel, OpenZFS, the installer) with `pacman -Syu`, like
every other package, instead of only with a new release image.

{{< callout type="warning" >}}
**The repository goes live with the first signed Runink River release.** The configuration
below is in the image today, and nothing has been published to it yet: both mirrors answer
404 for `runink.db`. On a system that already has the `[runink]` stanza, `pacman -Syu` stops
with `error: failed to synchronize all databases` until then. To update the rest of the
system in the meantime, comment out the three `[runink]` lines in `/etc/pacman.conf` and put
them back after the first release.
{{< /callout >}}

## What it carries

Only packages this repository builds from its own
{{< repo path="build/pkgbuilds/" text="build/pkgbuilds/" >}}, from source that is public. The
publishing tool accepts a package only if its name is on an explicit allow-list, which a test
holds equal to what those PKGBUILDs produce:

| Package | What it is |
|---|---|
| `linux-runink`, `linux-runink-headers` | the kernel, the zen 7.2.x stable series ([Kernel]({{< relref "/docs/features/kernel" >}})) |
| `runink-zfs`, `runink-zfs-utils` | the OpenZFS kernel modules built for exactly that kernel, and the matching userland |
| `runink-installer` | the hardware probe, the planner, the network set-up and the graphical installer |
| `river-guide` | the offline install guide of the live medium |
| `runink-grub-live` | the live medium's GRUB scaffolding; the install removes it, so it is never on a running machine |
| `runink-k0s`, `runink-k0s-airgap`, `runink-tayga` | packaging that images built downstream from Runink River use; not on the workstation image |
| `runink-core`, `runink-runtime` | placeholders filled only by a downstream build; the published ones hold a marker file and a models manifest at most |

Nothing built by anyone else is published, and nothing a downstream distribution adds to its
own images. The distribution's everyday packages (Plasma, Firefox, libraries, tools) keep
coming from the distribution's repositories, as before ([Updates]({{< relref "updates" >}})).

## How the image configures it

The image ships three files, and the installer's `35-pacman-keyring` step uses them to set up
the installed system:

| File | What it is |
|---|---|
| `/usr/share/pacman/keyrings/runink.gpg` | the release public key, the same bytes as `https://runink.org/.well-known/gpg-key.txt` |
| `/usr/share/pacman/keyrings/runink-trusted` | tells `pacman-key --populate runink` to trust that key: `95C0A7B97D547413E42660DDB06FE75626F15BF3:4:` |
| `/etc/pacman.d/mirrorlist-runink` | where the repository is served (below) |

The step runs `pacman-key --populate runink`, checks that the key is valid in the new system's
keyring, and adds this stanza to `/etc/pacman.conf`, directly ahead of `[system]`, so a
package in `[runink]` wins over one with the same name further down:

```ini
[runink]
SigLevel = Required DatabaseRequired
Include = /etc/pacman.d/mirrorlist-runink
```

The step fails the install, rather than leaving a half-configured repository, if the image
ships only some of the three files or if the target's `pacman.conf` names a `file://`
repository. Its source is {{< repo "installer/lib/35-pacman-keyring.sh" >}}.

### The signature policy

`SigLevel = Required DatabaseRequired` means pacman refuses a package **and** a database that
does not carry a valid signature by a trusted key. The only key trusted for it is the release
key, fingerprint `95C0A7B97D547413E42660DDB06FE75626F15BF3`, the same key that signs the ISO's
`SHA256SUMS` ({{< repo "KEYS" >}}, [Release signing]({{< relref "/docs/security/release-signing" >}})).
A tampered or unsigned file fails the update; nothing falls back to trusting it.

To see the key in pacman's keyring:

```bash
pacman-key --list-keys 95C0A7B97D547413E42660DDB06FE75626F15BF3
```

## The mirrors

`/etc/pacman.d/mirrorlist-runink`, as the image installs it
({{< repo "iso-profiles/river/root-overlay/etc/pacman.d/mirrorlist-runink" >}}):

```ini
Server = https://github.com/org-runink/river/releases/download/repo-x86_64
Server = https://runink.org/river/repo/x86_64
```

1. **The GitHub release `repo-x86_64`** is the complete repository and the first place a
   publish lands: every package, its `.sig`, `runink.db`, `runink.files` and their signatures.
   It is a prerelease that is never marked "latest", so `install.sh`, which downloads the ISO
   from the latest release, never picks it up.
2. **runink.org** is a **partial** mirror. Its hosting refuses files over 100 MiB, so it
   carries the database, the file list and every package up to that size, with their
   signatures, but not the larger ones: today the kernel (`linux-runink`), `runink-k0s` and
   `runink-k0s-airgap`. For those it answers 404.

pacman tries the `Server` lines in order and moves to the next on any failure, a 404
included. GitHub comes first because it is complete and always current; the mirror is the
fallback when GitHub is unreachable. During such an outage the database and the smaller
packages still update from the mirror, and the kernel waits until GitHub is back.

## Add it to a system installed before it existed

A system installed from an image older than the repository has no `[runink]`. The installed
system has no `curl` (the installer removes it), so take the files from a clone of the
repository, where they are the same files the image ships. As your administrator user:

{{% steps %}}

### Get the files

```bash
git clone --depth 1 https://github.com/org-runink/river.git ~/river-src
cd ~/river-src/iso-profiles/river/root-overlay
```

### Check the key's fingerprint

```bash
gpg --show-keys --with-fingerprint usr/share/pacman/keyrings/runink.gpg
```

It must show `95C0 A7B9 7D54 7413 E426  60DD B06F E756 26F1 5BF3`, the fingerprint on this
page and in {{< repo "KEYS" >}}. Anything else: stop.

### Install the key and the mirrorlist

```bash
sudo install -m 0644 usr/share/pacman/keyrings/runink.gpg usr/share/pacman/keyrings/runink-trusted /usr/share/pacman/keyrings/
sudo install -m 0644 etc/pacman.d/mirrorlist-runink /etc/pacman.d/
sudo pacman-key --populate runink
pacman-key --list-keys 95C0A7B97D547413E42660DDB06FE75626F15BF3   # must be listed
```

This is what the installer does. The equivalent by hand, with the key file you checked, is
`sudo pacman-key --add usr/share/pacman/keyrings/runink.gpg` followed by
`sudo pacman-key --lsign-key 95C0A7B97D547413E42660DDB06FE75626F15BF3`.

### Add the stanza

Edit `/etc/pacman.conf` as root (`sudoedit /etc/pacman.conf`) and add, directly **above** the
`[system]` line:

```ini
[runink]
SigLevel = Required DatabaseRequired
Include = /etc/pacman.d/mirrorlist-runink
```

### Sync

```bash
sudo pacman -Sy
```

`runink` must be listed among the databases it synchronizes. Then remove `~/river-src` if you
like.

{{% /steps %}}

## Verify a package by hand

pacman checks every signature by itself. To check one yourself, download the package and its
`.sig` from the [`repo-x86_64` release](https://github.com/org-runink/river/releases/tag/repo-x86_64)
and verify the signature against the key in pacman's keyring (no root needed):

```bash
pacman-key --verify runink-zfs-utils-<version>-x86_64.pkg.tar.zst.sig runink-zfs-utils-<version>-x86_64.pkg.tar.zst
```

A package pacman has already installed is in `/var/cache/pacman/pkg/`, with the `.sig` it
downloaded next to it; `pacman-key --verify /var/cache/pacman/pkg/<package>.pkg.tar.zst.sig`
checks it, finding the package by dropping `.sig` from the name.

On a machine without pacman, with the release key imported into gpg
([Download & verify]({{< relref "/docs/getting-started/download#2-get-the-key-and-check-its-fingerprint" >}})):

```bash
gpg --verify runink-zfs-utils-<version>-x86_64.pkg.tar.zst.sig runink-zfs-utils-<version>-x86_64.pkg.tar.zst
```

It must say **Good signature**, with the primary key fingerprint above. The database is
checked the same way (`runink.db.sig` over `runink.db`). The repository also carries a
`SHA256SUMS` over every package and both archives, with its own `SHA256SUMS.sig`: verify that
signature first, then `sha256sum -c --ignore-missing SHA256SUMS` checks every file you have.

## Update, and roll back

`[runink]` changes nothing about how you update: snapshot the boot environment, then run
`pacman -Syu`.

```bash
findmnt /boot                                                      # the EFI partition must be mounted
sudo zfs snapshot "$(findmnt -no SOURCE /)@pre-update-$(date +%Y%m%d)"
sudo pacman -Syu
```

An update from `[runink]` arrives only when a package's version grows, so what you already
have is never replaced by the same version rebuilt. The kernel and its ZFS modules are one
unit: upgrade them together, in one transaction, and keep the previous packages so you can go
back offline ([Kernel and ZFS updates]({{< relref "updates#kernel-and-zfs-updates" >}})).

If an update breaks the system, [roll back]({{< relref "boot-environments#roll-back" >}}) to
the snapshot from the live USB stick, or, for a bigger change such as a kernel series, upgrade
a [clone of the boot environment]({{< relref "boot-environments#keep-the-old-system-bootable-instead" >}})
and keep the old one bootable. Your files in `/home` are not part of a rollback.

## For mirror operators

A mirror is any HTTPS server that serves the files of the `repo-x86_64` GitHub release under
one directory: every `*.pkg.tar.zst` with its `.sig`, `runink.db` and `runink.files` with
their signatures (and the `runink.db.tar.zst` and `runink.files.tar.zst` archives they point at, with theirs), and `SHA256SUMS`
with `SHA256SUMS.sig`. Copy the files as they are, keep every signature next to its file, and
refresh the database last, so it never names a package the mirror does not have yet. A mirror
that holds only some packages is valid too: pacman moves to the next server for what it lacks.

Every file is signed with the release key, so a mirror is trusted for availability only,
never for content: a mirror that serves a wrong or altered file fails pacman's signature
check, and nothing is installed from it. A user adds a mirror with one more `Server` line in
`/etc/pacman.d/mirrorlist-runink`, for example
`Server = https://mirror.example.org/runink-river/x86_64`, after the GitHub line unless the
mirror is complete and updated at the same moment. How releases are assembled, signed and
published is in {{< repo "docs/REPOSITORY.md" >}}.
