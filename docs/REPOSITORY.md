<!--
SPDX-FileCopyrightText: 2026 The RIVER Authors
SPDX-License-Identifier: MIT
-->

# The `[runink]` package repository

Runink River builds its own packages: the kernel (`linux-runink`), OpenZFS (`runink-zfs`,
`runink-zfs-utils`), k0s, the installer and the rest of `build/pkgbuilds/`. Until now they
reached a machine only inside the image. `[runink]` is the public, signed pacman repository
that lets an installed Runink River system update them with `pacman -Syu`, like every other
package.

## How an installed system updates

The installer (step `35-pacman-keyring`) sets the target up from files the image ships:

| File on the image | What it does |
|---|---|
| `/usr/share/pacman/keyrings/runink.gpg` | the release public key (the same bytes as `base/keys/owner/<fingerprint>.asc`) |
| `/usr/share/pacman/keyrings/runink-trusted` | tells `pacman-key --populate runink` to trust that key |
| `/etc/pacman.d/mirrorlist-runink` | where the repository is served (below) |

The step runs `pacman-key --populate runink`, checks the key is valid in the target's keyring,
and adds this to `/etc/pacman.conf`, ahead of the distribution's repositories as at build
time:

```ini
[runink]
SigLevel = Required DatabaseRequired
Include = /etc/pacman.d/mirrorlist-runink
```

`Required DatabaseRequired` means pacman refuses a package or a database without a valid
signature by a trusted key. Only the release key is trusted for it (its fingerprint is in
[KEYS](../KEYS)). A tampered or unsigned file fails the update; nothing falls back to trusting
it. The step fails the install if the image ships only part of this, and if the target's
`pacman.conf` names any `file://` repository (the build's local repository, below, must never
reach a machine).

The image **build** has its own `[runink]`: a `file://` directory of the packages just built,
`SigLevel = Optional TrustAll` (`pacman/pacman.conf.in`, `scripts/build-iso-box.sh`). It lives
in the build's pacman configuration only. The image's `/etc/pacman.conf` is the distribution's
own (buildiso does not copy its configuration into the root filesystem), and the installer
adds the public stanza above to it.

A system installed before this change has no `[runink]`. To add it by hand, as root:

```sh
curl -fsSLo /tmp/runink.asc https://runink.org/.well-known/gpg-key.txt
gpg --show-keys --with-fingerprint /tmp/runink.asc      # must match KEYS
pacman-key --add /tmp/runink.asc
pacman-key --lsign-key 95C0A7B97D547413E42660DDB06FE75626F15BF3
# then add the stanza above ahead of [system] in /etc/pacman.conf, and the two Server
# lines below to /etc/pacman.d/mirrorlist-runink
```

A node upgrades a package only when its version grows, so every change that should reach
installed systems bumps the PKGBUILD's `pkgver` or `pkgrel`.

## Where it is served

```
Server = https://github.com/org-runink/river/releases/download/repo-x86_64
Server = https://runink.org/river/repo/x86_64
```

1. **The GitHub release `repo-x86_64`** (primary, complete). A rolling release on this
   repository whose assets are the repository: every package, its `.sig`, `runink.db`,
   `runink.files` and their signatures. It is a **prerelease that is never "latest"**, so
   `install.sh`, which downloads the image from the latest release, never sees it.
   `river repo publish` refuses to upload to it if someone made it a full release.
2. **runink.org** (a PARTIAL mirror). The website's GitHub Pages serves
   `static/river/repo/x86_64/` of the site repository. Git hosting refuses files over
   100 MiB, so the mirror holds the database, the file list and every package up to 100 MiB,
   each with its signature, and **not** the packages over it: today `linux-runink` (about
   173 MB), `runink-k0s` (about 186 MB) and `runink-k0s-airgap` (about 125 MB). For those it
   answers 404.

pacman tries the `Server` lines in order and moves to the next on any failure, a 404
included. **GitHub is listed first** because it is the complete copy and the first place a
publish lands: the release is updated by `river repo publish` directly, while the mirror
changes only when its files are committed to the site repository and the site deploys. With
the mirror first, a node would read the mirror's database as long as it answered, so a publish
would reach nodes only after the site deployed, and a mirror that fell behind would hide
updates with no error. With GitHub first, the mirror is what it should be: the fallback when
GitHub is unreachable. During such an outage the database and the small packages still come
from the mirror, and the three large packages cannot be updated until GitHub is back.

Every file is signed, so a mirror is trusted for availability only, never for content: a
mirror that serves a wrong file fails pacman's signature check.

The mirror has two more limits worth knowing: every publish commits its packages to the site
repository's history, which only grows (up to about 130 MB each time today), and GitHub Pages
publishes sites up to 1 GB. If either becomes a problem, the mirror can be reduced to the
database alone, which still lets pacman see updates while GitHub serves the files.

### Adding a mirror

A mirror is any HTTPS server that serves the files of the GitHub release (or the mirror's
subset of them) under one directory. Copy them there, add its `Server` line to
`iso-profiles/river/root-overlay/etc/pacman.d/mirrorlist-runink` (after GitHub, unless it is
complete and updated at the same moment), and keep the signatures with the files. Nothing
about a mirror needs to be trusted beyond serving files.

## Publishing (release maintainers)

The repository is assembled from a **public** build, signed **offline** by the owner, then
verified and published. CI and agents never hold the private key: they can assemble, verify
and publish, but they cannot sign, and publish refuses anything unsigned.

```sh
# 1. Build the public image and its packages (no downstream payload; docs/BUILD.md).
build/local-iso.sh                              # packages land in ~/.cache/river-build/local-iso/localrepo-river

# 2. Assemble the repository: River's public packages only, repo-add, SHA256SUMS. Unsigned.
river repo assemble --from ~/.cache/river-build/local-iso/localrepo-river --out ~/.cache/river-repo/x86_64

# 3. The owner signs every package, the database and SHA256SUMS, offline, with the release key.
base/river-sign ~/.cache/river-repo/x86_64

# 4. Check every signature against the release public key in this tree, and nothing else.
river repo verify --dir ~/.cache/river-repo/x86_64

# 5. Publish: the GitHub release, and the Pages mirror subset into a site checkout.
river repo publish --dir ~/.cache/river-repo/x86_64 --github \
    --pages-out <site checkout>/static/river/repo/x86_64
#    then commit that directory in the site repository through its normal review.
```

(`river` is the command line built from `cli/`: `cd cli && go build -o ~/.local/bin/river ./cmd/river`.)

What each step guarantees:

- **`river repo assemble`** refuses the whole set, and writes nothing, if any package is not
  public (next section). It runs `repo-add` (never with `--sign`), writes `runink.db` and
  `runink.files`, and a `SHA256SUMS` over the packages and both archives, which is what
  `base/river-sign` checks before signing. Its output directory must be new or empty.
- **`base/river-sign`** checks `SHA256SUMS`, signs each listed file and `SHA256SUMS` itself
  with a detached `.sig`, links `runink.db.sig` and `runink.files.sig` to the archives'
  signatures, and verifies all of it against `base/keys/owner/<fingerprint>.asc`. It refuses
  to run in CI.
- **`river repo verify`** fails closed: every package, `runink.db`, `runink.files`, both
  archives and `SHA256SUMS` must have a `.sig` that `gpgv` accepts using **only** the release
  public key from this tree (`--key-file`, default `base/keys/owner/<fingerprint>.asc`), whose
  primary fingerprint is the one given (`--key-fpr`, default `base/keys/owner/fingerprint`).
  It also checks every file against `SHA256SUMS`, that the database names exactly the packages
  present, and that the directory holds nothing else. A missing tool (`gpgv`, `zstd`) is a
  failure, not a skip.
- **`river repo publish`** runs `verify` and the public-package guard again, and publishes
  only if both pass. It never signs. With `--github` it creates the `repo-x86_64` release if
  it is missing (a prerelease, `--latest=false`), uploads the packages first and the database
  last with `gh release upload --clobber` (so the database never names a package the release
  lacks), then deletes assets the new database no longer has (`--prune=false` keeps them).
  With `--pages-out` it writes the mirror subset as regular files into that directory,
  removes files a previous publish left there that the repository no longer has, reports each
  package it left out for size, and refuses a directory holding anything that is not a
  repository file.

## What is never published, and why

`[runink]` is public: anyone can download it, and every installed Runink River system trusts
it. It carries only what this repository builds and publishes as source.

- **Only River's own packages.** `river repo assemble` and `publish` accept a package only if
  its `.PKGINFO` name is on an explicit allow-list (`PublicPackages` in
  `cli/internal/repocmd/policy.go`), which a test holds equal to the packages
  `build/pkgbuilds/*/PKGBUILD` produce. A package built by anyone else is refused, whatever it
  contains.
- **No downstream payload.** `runink-core` and `runink-runtime` are the two packages whose
  contents come from a downstream payload (`RIVER_PAYLOAD_DIR`, [PAYLOADS.md](PAYLOADS.md))
  when a build has one. Only a build **without** one is public: `runink-core` must hold exactly
  its `PAYLOAD-NONE` marker and nothing else, and `runink-runtime` nothing beyond the models
  manifest. No other package may install anything under the payload paths
  (`/usr/local/share/runink/core/`, `/usr/local/lib/runink/firstboot.d/`,
  `/usr/local/share/runink/images/`).
- **Nothing from a private build.** A source directory, or a package, whose path contains
  `private`, or the downstream server profile's name, is refused: those are the names private
  and server builds give
  their state directories.

A downstream distribution that ships its own software keeps it out of `[runink]`. It may add
its own authenticated repository to its images, with its own key and access control; nothing
here publishes, mirrors or trusts it.
