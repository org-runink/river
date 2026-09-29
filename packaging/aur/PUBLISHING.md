<!--
SPDX-FileCopyrightText: 2026 The RIVER Authors
SPDX-License-Identifier: MIT
-->

# Publishing the Runink River kernel to the AUR

The steps the **maintainer** runs to publish `linux-runink` and `zfs-linux-runink` to the Arch
User Repository (AUR) from this directory. Nothing here is automated, and no CI job or agent
holds AUR credentials: the AUR is written only by a person, from their own AUR account.

These are **community packages in the AUR**. They are not official Arch Linux packages,
Arch Linux does not endorse them, and nothing we publish may say or suggest otherwise (see
[Rules](#rules-to-respect)).

| AUR package base | Directory | Builds |
|---|---|---|
| `linux-runink` | `packaging/aur/linux-runink/` | `linux-runink`, `linux-runink-headers` |
| `zfs-linux-runink` | `packaging/aur/zfs-linux-runink/` | `zfs-linux-runink` (modules only; userland is the AUR's `zfs-utils`) |

Both names were free on the AUR when this was written (checked with the AUR RPC); check again
before the first push.

## 1. One-time account setup

1. Log in at <https://aur.archlinux.org> with the maintainer's Arch account.
2. Create an SSH key used only for the AUR and add its public half under **My Account → SSH
   Public Key**:

   ```bash
   ssh-keygen -t ed25519 -f ~/.ssh/aur -C "aur"
   cat >> ~/.ssh/config <<'EOF'
   Host aur.archlinux.org
     IdentityFile ~/.ssh/aur
     User aur
   EOF
   ssh aur@aur.archlinux.org help     # must answer with the AUR command list
   ```

3. Optional but recommended: sign commits and the Runink River tag with the maintainer's GPG
   key (`git config user.signingkey <fingerprint>`), and publish that key's fingerprint in
   `MAINTAINERS.md`.

## 2. Tag the configuration the AUR package fetches

`linux-runink` does not copy the kernel configuration into the AUR repository; it downloads
`config.base`, `config.delta` and `config.require` from **a tag of the public Runink River
repository** and checks each against a sha256 in the PKGBUILD. The tag name is
`v${pkgver}-${pkgrel}`, so for this release: **`v7.2.7.zen1-1`** (a placeholder until you
create it; the PKGBUILD cannot be built from the AUR until the tag exists).

1. Merge the change that carries the configuration to `main` in `org-runink/river`.
2. Tag the merge commit and push the tag:

   ```bash
   git fetch origin && git switch --detach origin/main
   (cd cli && go run ./cmd/river lint aur-sync --repo ..)   # pins, .SRCINFO and keys agree
   git tag -s v7.2.7.zen1-1 -m "linux-runink 7.2.7.zen1-1 (AUR)"
   git push origin v7.2.7.zen1-1
   ```

3. Check that the tag serves the pinned bytes:

   ```bash
   cd packaging/aur/linux-runink && makepkg --verifysource   # downloads, checks sha256 + signatures
   ```

A tag is immutable by policy: never move or delete one that an AUR release points at. A
configuration change is a new `pkgrel` (or `pkgver`), a new tag, new sha256 pins.

## 3. Build and check in a clean chroot

Build in a clean chroot so a missing `makedepends` shows up (the `devtools` package):

```bash
cd packaging/aur/linux-runink
gpg --import keys/pgp/*.asc
extra-x86_64-build                                  # or: makepkg -s in a throwaway container
namcap PKGBUILD *.pkg.tar.zst
```

Then `zfs-linux-runink`, which needs the kernel headers just built. It also needs the AUR's
`zfs-utils` at the same OpenZFS version (2.4.4), so build and install that first:

```bash
cd ../zfs-linux-runink
gpg --import keys/pgp/*.asc
extra-x86_64-build -- -I ../linux-runink/linux-runink-7.2.7.zen1-1-x86_64.pkg.tar.zst \
                   -I ../linux-runink/linux-runink-headers-7.2.7.zen1-1-x86_64.pkg.tar.zst \
                   -I /path/to/zfs-utils-2.4.4-*.pkg.tar.zst
namcap PKGBUILD *.pkg.tar.zst
```

Boot the kernel on real hardware (or at least a VM) before the first publication.

## 4. First publication

For each package base (shown for `linux-runink`; repeat with `zfs-linux-runink`):

```bash
git clone ssh://aur@aur.archlinux.org/linux-runink.git aur-linux-runink   # empty repo: expected
cd aur-linux-runink
git switch -c master                   # the AUR only accepts pushes to master
cp -r ../river/packaging/aur/linux-runink/{PKGBUILD,.SRCINFO,.nvchecker.toml,keys} .
makepkg --printsrcinfo | diff - .SRCINFO   # must print nothing
git add PKGBUILD .SRCINFO .nvchecker.toml keys
git commit -s -m "linux-runink 7.2.7.zen1-1: initial upload"
git push origin master
```

The package page appears at `https://aur.archlinux.org/packages/linux-runink`. On first push
the account that pushed becomes the maintainer.

Only these files go to the AUR repository: `PKGBUILD`, `.SRCINFO`, `.nvchecker.toml` and
`keys/pgp/*.asc`. Never commit sources, built packages, logs or the `src/` and `pkg/`
directories.

## 5. Updates

For a new zen stable release in the series (`nvchecker` reports it; `.nvchecker.toml` is in
each directory):

1. In this repository, bump both kernel PKGBUILDs together (`build/pkgbuilds/runink-kernel`
   and `packaging/aur/linux-runink`: `_minor` / `_zenrel`, the kernel and zen sha256), check
   the new kernel is inside OpenZFS's `META` range, and bump `zfs-linux-runink` (`_kernver`,
   `_kernrel`; its `pkgver` follows). Regenerate both `.SRCINFO` files with
   `makepkg --printsrcinfo > .SRCINFO` and run `river lint aur-sync` (`cd cli && go run ./cmd/river lint aur-sync --repo ..`).
2. Merge, tag `v<pkgver>-<pkgrel>` (step 2), build and check (step 3).
3. Push both AUR repositories: kernel first, then `zfs-linux-runink`, whose exact
   `linux-runink=` dependency would otherwise point at a version the AUR does not have yet.

A series rebase (7.2 → 7.3) waits for an OpenZFS stable that declares the new series
([docs/KERNEL.md](../../docs/KERNEL.md#zfs-pacing-policy)).

When someone flags the package out of date, answer on the package page; if the bump is
blocked by OpenZFS, say so there.

## 6. Maintainer and co-maintainers

- The account that first pushes is the **maintainer**. Use the project lead's Arch account
  (listed in `MAINTAINERS.md`).
- Add at least one **co-maintainer** as soon as a second Runink River maintainer exists:
  package page → **Manage Co-Maintainers**, one AUR username per line. Co-maintainers can
  push, but cannot remove the maintainer.
- Every co-maintainer registers their own SSH key; keys and accounts are never shared.
- If the maintainer steps down, transfer the package (make the successor a co-maintainer,
  then disown; the first co-maintainer becomes maintainer) rather than letting it be
  orphaned.
- Record the AUR maintainers in `MAINTAINERS.md` so the project and the AUR agree.

## Rules to respect

From the AUR submission guidelines and the Arch Linux trademark policy
(<https://wiki.archlinux.org/title/AUR_submission_guidelines>,
<https://terms.archlinux.org/docs/trademark-policy/>); re-read both before the first push.

- **Build from source.** Both packages compile from signed upstream tarballs; no binaries,
  no prebuilt modules. Keep it that way.
- **Keep `.SRCINFO` current.** Regenerate it on every change to the PKGBUILD; the AUR reads
  metadata only from `.SRCINFO`. `river lint aur-sync` fails when it is stale.
- **Do not duplicate packages.** Official repositories already have `linux-zen`; these
  packages differ by their configuration (the analytics profile) and must say so. The AUR's
  `zfs-utils` already provides the OpenZFS userland; `zfs-linux-runink` depends on it and must
  not ship its own.
- **Accurate metadata.** Valid SPDX identifiers (`GPL-2.0-only` for the kernel, `CDDL-1.0`
  for OpenZFS), correct `depends`/`makedepends`, no `replaces` (nothing is being renamed).
- **Trademarks.** Say "for Arch Linux" and "in the AUR". Never "official", never an Arch
  logo, never wording that suggests Arch Linux made, reviewed or endorses these packages.
  The same applies to the Runink River docs, site and announcements.
- **Licensing of the packaging.** In this repository the packaging files are MIT
  (`REUSE.toml`); the linux-runink PKGBUILD also carries Arch's linux-zen packaging, which is
  0BSD. Arch's own package sources are 0BSD (RFC 0040); check the current AUR guidelines for
  any licensing recommendation for AUR repositories before the first push.
- **Security.** Never commit credentials. Never loosen the sha256 pins or `validpgpkeys`, and
  never replace a pin with `SKIP` except for detached signatures.
