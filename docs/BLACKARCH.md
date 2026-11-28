<!--
SPDX-FileCopyrightText: 2026 The RIVER Authors
SPDX-License-Identifier: MIT
-->

# The [blackarch] repository

Runink River configures the [BlackArch](https://blackarch.org) package repository on every
installed machine and **installs nothing from it**. A user who wants a security tool runs
`pacman -S <tool>`; the image carries no BlackArch tool, and no package list may name one.
User-facing instructions: [book/docs/configuration/blackarch](../book/docs/configuration/blackarch/README.md).
This page is for maintainers: what ships, where the key came from, and how BlackArch's rolling
repository interacts with the pinned distribution snapshot.

Status: in the tree from step B of [ARCH-BASE.md](ARCH-BASE.md) §5. It works the same on the
current Artix-based image and on the Arch-based one (the stanza, the keyring and the mirrorlist
do not depend on the base), but BlackArch builds against current Arch Linux, so the snapshot
section below applies to both, and more sharply to Artix.

## What ships

| File | Purpose |
|---|---|
| `iso-profiles/river/root-overlay/usr/share/pacman/keyrings/blackarch.gpg` | BlackArch's public keys (armored), byte for byte from its signed keyring package |
| `.../keyrings/blackarch-trusted` | the four fingerprints `pacman-key --populate blackarch` trusts |
| `.../keyrings/blackarch-revoked` | the one key it disables |
| `.../keyrings/blackarch*.license` | REUSE sidecars (public keys, `LicenseRef-Public-Key`) |
| `iso-profiles/river/root-overlay/etc/pacman.d/blackarch-mirrorlist` | seven HTTPS mirrors |
| `pacman/blackarch-keyring.pin` | the pin: upstream package and sha256, the repository signer, the trusted list, the sha256 of each vendored file, and the verification record |
| `pacman/pacman.conf.in` | the build-time `[blackarch]` stanza, last, `SigLevel = Required`, `Usage = Sync Search` |
| `installer/lib/35-pacman-keyring.sh` (and its overlay copy) | populates the keyring on the target, checks every trusted key is valid, appends the node stanza |

**No upstream bootstrap script runs.** BlackArch's documented way to add the repository to an
Arch system is `strap.sh` (download and run as root). Runink River never runs it, at build
time or on a node: its only use here was as the published list of signing fingerprints
(read, not executed). The keyring is vendored instead, which matches how the image already ships
the `[runink]` release key (`keyrings/runink.gpg` + `runink-trusted`, populated by the same
installer step). A keyring *package* built in `build/pkgbuilds/` was considered and not used:
it would own the same three files as BlackArch's own `blackarch-keyring` and add a package to
`Packages-Root` and the build for no gain over vendored files.

### The stanza

On an installed machine, `35-pacman-keyring` appends, at the **end** of `/etc/pacman.conf`:

```ini
[blackarch]
SigLevel = Required
Include = /etc/pacman.d/blackarch-mirrorlist
```

- **Last**: pacman takes a package from the first repository that carries it, so a BlackArch
  build of a name the distribution or `[runink]` also has is never chosen.
- **`SigLevel = Required`** (no `Package`/`Database` prefix) applies to both: the database and
  every package must be signed by a trusted key. Measured 2026-11-28 with pacman 7.1.0: with
  only the vendored files trusted, the live `blackarch.db` (signed 2026-11-27) verified; with an
  empty keyring pacman refused it (`key "F9A6E68A…" is unknown`); with no signature it refused
  it (`missing required signature`).
- The build-time copy in `pacman.conf.in` adds **`Usage = Sync Search`**: pacman reads the
  database but refuses to install from it, so even a package list that named a BlackArch tool
  could not put it in the image. Measured the same day: `pacman -Sp nmap-parse-output` answered
  `target not found` with `Usage = Sync Search` and resolved the package with the default usage.
  The build's rendered file names a `file://` repository and never reaches a node (the
  installer refuses a target whose `pacman.conf` does).

### Checks

`river lint blackarch` (Tier 1, `make lint`, tests in `cli/internal/lint/blackarch_test.go`)
fails when: a vendored file's sha256 differs from the pin; `blackarch-trusted` is not exactly
the pinned list; a trusted fingerprint (or the repository signer) is not a primary key of
`blackarch.gpg`, computed from the key packets; a key is both trusted and revoked; `[blackarch]`
is not the last section of `pacman.conf.in` or lacks `SigLevel = Required`, `Usage = Sync
Search` or the mirrorlist `Include`; the mirrorlist has fewer than three `Server` lines or any
that is not `https://`; the installer does not populate the keyring or write the stanza; or any
`iso-profiles/*/Packages-*` names a `blackarch*` package.

Not checked offline: whether a Packages-* name happens to be a BlackArch tool with a generic
name (the `Usage = Sync Search` line is the guard for that), and mirror reachability.

## Where the key came from (verified 2026-11-28)

1. `blackarch.db` and `.sig` from `https://www.blackarch.org/blackarch/blackarch/os/x86_64/`.
   The database lists `blackarch-keyring 20251011-2`, sha256
   `d40c1301f84195164a2843f2447f5b3833bd9ea30e5cfdaf4271ad9c9771ee07`.
2. The package and its `.sig` from the same directory; sha256 equal to the database entry.
3. The four signer keys fetched separately from `keyserver.ubuntu.com` (IPv4). `gpg --verify`
   of the package: `VALIDSIG F9A6E68A711354D84A9B91637533BAFE69A25079` (signed 2025-10-12).
   The current database is signed by the same key.
4. **The fingerprint as published by the BlackArch project**: `https://blackarch.org/strap.sh`,
   retrieved 2026-11-28, lists in `KEYRING_SIGNERS` ("keep in sync with blackarch-trusted"):
   `8F9A9793CB8591147C2EC70566E0CDBD1E01F333`, `A0917C4147A37007CB54C1CFD295AA940EFDDF62`,
   `4345771566D76038C7FEB43863EC0ADBEA87E4E3`, `F9A6E68A711354D84A9B91637533BAFE69A25079`.
   `https://blackarch.org/downloads.html` publishes that script's SHA1,
   `d338a4bb95d9e09f97508da68ac9e17d963b85f6`, and the copy fetched matched it. blackarch.org's
   HTML pages do not print the repository fingerprint themselves (its front page links team
   members' personal keys, which are different keys); `strap.sh` is the project's own
   published statement of it.
5. `blackarch.gpg` is identical to the file in `github.com/BlackArch/blackarch-keyring`
   (git blob `c9eede6e…`, commit `e5bb520`, 2025-10-11). That repository's `blackarch-trusted`
   also lists the master key `CBA3C7D4798912702DCF568E67D8BDF42AD93F4E`, which the signed
   package's copy (and `strap.sh`) does not; the image ships the signed package's list.
6. The fingerprints computed from `blackarch.gpg` by `river lint blackarch` contain all four.

**Bumping the keyring**: when BlackArch publishes a new `blackarch-keyring` (a new signer, a
revocation), repeat 1–6 against it, replace the three files and update every value in
`pacman/blackarch-keyring.pin` in one commit. Between bumps a node keeps the vendored keys:
pacman does not update them, because the image does not install the `blackarch-keyring`
package. If BlackArch signs with a key the vendored keyring lacks, `pacman -Syu` fails on the
`[blackarch]` database (fails closed) until the bump ships, or until the user installs the
upstream keyring package as the user guide describes.

## Mirrors

From the project's list (`https://blackarch.org/blackarch-mirrorlist`, 2026-11-28), HTTPS only:
RWTH Aachen (DE), dotsrc (DK), mirrorservice.org (UK), OSU OSL (US), OCF Berkeley (US),
Team Cymru (US), and `www.blackarch.org` itself last. All seven answered over IPv4 on
2026-11-28 with the same database (`Last-Modified: Fri, 27 Nov 2026 21:33:07 GMT`); the first
six also publish AAAA records, `www.blackarch.org` has none. IPv6 reachability was not measured
(the host used had no IPv6 route). Signatures make every mirror untrusted for content; a mirror
can still serve a stale database, which pacman accepts while its signature is valid.

**Availability coupling**: with the stanza present, `pacman -Syu` fails with `failed to
synchronize all databases` when every BlackArch mirror is unreachable, even though nothing
from BlackArch is installed. Seven mirrors in four countries make that unlikely; the user guide
says how to comment the stanza out.

## BlackArch is rolling; Runink River is pinned

BlackArch publishes **no dated archive**: its database is always the current one, and old
package versions are removed from the mirrors. Runink River resolves its distribution
packages from a dated snapshot (`pacman/mirrorlist.pin`; on the Arch base, one `PIN_DATE` of
`archive.archlinux.org`). The two are built for different points in time, and the gap grows
until the next snapshot bump.

What that means for a tool installed from `[blackarch]`:

- **BlackArch builds against current Arch.** A tool built after `PIN_DATE` may need a newer
  library than the snapshot has. When the dependency is *versioned* (`openssl>=3.6`), pacman
  refuses the transaction (`unable to satisfy dependency`): safe, and the fix is to wait for the
  next snapshot bump. When it is *unversioned* and the library changed its soname or ABI in
  between, pacman installs it and the tool fails at run time (`error while loading shared
  libraries: libfoo.so.N`). Interpreted tools have the same problem with the interpreter's
  minor version: modules installed for a newer interpreter are not found by an older one.
- **This is a partial upgrade.** Arch supports only a system where every package comes from
  the same moment; a pinned base plus a rolling repository is not that. Runink River does not
  claim BlackArch tools work on every snapshot. It claims the repository is configured,
  signed and verified, and that pacman refuses what it can detect.
- **The risk is smallest right after a snapshot bump** and grows with the snapshot's age.
  With the Artix base it is larger, because Artix's versions of some libraries differ from
  Arch's at any given moment.
- **`replaces`**: a BlackArch package may declare that it replaces a distribution package.
  pacman asks before such a replacement during `-Syu`, even from the last repository, and
  the default answer is yes: read the prompt and answer no unless you mean it.
- **Reproducibility**: the image is unaffected (it installs nothing from `[blackarch]`). A
  tool a user installs is not reproducible: the same `pacman -S` a week later may fetch a
  different build, and the previous one may no longer exist on any mirror.

A snapshot bump PR does not test BlackArch tools. If a pinned snapshot ever needs to match
BlackArch more closely, the options are a newer `PIN_DATE` or a mirror of the BlackArch
repository kept at a chosen date; neither is done today.
