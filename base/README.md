# base/: Runink River's own from-source base (Phase 0 scaffold)

**Status: scaffold.** This directory is the first piece of the own base described in
[`docs/OWN-BASE.md`](../docs/OWN-BASE.md). It is **not** wired into CI, `make`, or the ISO. The
image is still built by the Artix tooling, and nothing built here ships anywhere yet.

```
base/
  river-build          hermetic build driver (POSIX sh)
  river-sign           owner-local signing (POSIX sh); refuses to run in CI
  sources.lock         every upstream file: name version sha256 url sig_url sig_key
  seed.lock            the digest-pinned Phase-0 build container
  recipes/<pkg>/recipe one recipe per package (the format is below)
  keys/pgp/            upstream source-signing keys, <FPR>.asc
  keys/owner/          the owner's fingerprint (root of trust) + public key
```

## Quick start

```sh
podman pull "$(awk '$1=="image"{print $2}' base/seed.lock)"   # once; the driver never pulls
base/river-build lint
base/river-build fetch            # the ONLY networked step; sha256 (+ OpenPGP) checked
base/river-build build s6         # builds skalibs -> execline -> s6 in the seed, offline
ls base/out                       # *.pkg.tar.zst, *.spdx, SHA256SUMS (all unsigned)
base/river-sign base/out          # OWNER ONLY, locally, with the signing subkey
```

The outputs (`base/.cache/`, `base/.work/`, `base/out/`) are gitignored.

## Recipe format

A recipe is `base/recipes/<pkgname>/recipe`. It keeps the **shape of a PKGBUILD**, so a
maintainer who knows makepkg can read one and existing `build/pkgbuilds/runink-*` port
mechanically. The differences are deliberate:

| | PKGBUILD | recipe |
|---|---|---|
| Language | bash | **POSIX sh**, sourced by `/bin/sh` inside the build container |
| Lists | bash arrays `depends=(a b)` | **space-separated strings** `depends="a b"` |
| Sources | `source=(URL…)` + `sha256sums=(…)` in the file | **`sources="name…"`**: keys into `base/sources.lock`. **No URLs in recipes** (lint-enforced). |
| Checksums and signatures | per PKGBUILD, `SKIP` allowed | **only** in `sources.lock`, never skippable; `validpgpkeys` becomes the lock's `sig_key` |
| `prepare()` | separate | fold it into `build()` |
| `.install` scriptlets | allowed | **not allowed.** Users, directories and services are declared by the image profile. |
| setuid/setgid, world-writable | allowed | **refused** unless the path is listed in `setuid_allow` |
| License | `license=(…)` | `license="…"`, an SPDX expression |

### Variables

| Variable | Required | Meaning |
|---|---|---|
| `pkgname` | yes | Must equal the directory name. |
| `pkgver` | yes | Upstream version. It must equal the lock's version when the source has the same name. |
| `pkgrel` | yes | Our release number. Bump it for a recipe-only change. |
| `pkgdesc` | yes | One line. |
| `url` | yes | Upstream home page, for humans. It is the only URL a recipe may contain. |
| `license` | yes | SPDX license expression. It goes into `.PKGINFO` and the SBOM. |
| `sources` | yes | Lock entry names, each extracted into `$srcdir`. |
| `depends` | no | Runtime dependencies. Each must be a recipe or listed in `seed.lock`'s `provides`. |
| `makedepends` | no | Build-only dependencies, with the same rule. |
| `setuid_allow` | no | Absolute paths that may carry setuid/setgid bits. |

### Functions

Both are required. The working directory is `$srcdir` for each.

- `build()`: configure and compile. `CFLAGS`, `CXXFLAGS` and `LDFLAGS` arrive with the
  hardening and reproducibility defaults (see `river-build`), and `MAKEFLAGS` carries `-j`.
- `package()`: install into `$pkgdir`, for example `make DESTDIR="$pkgdir" install`.

### The build environment

Each package builds in a **fresh container** from `seed.lock`'s image with `--network=none`
and `--pull=never`:

- `/build/src` holds the extracted sources and `/build/pkg` is `$pkgdir`;
- the recipe's dependency packages (plus their runtime closure) are unpacked into `/` first;
- `SOURCE_DATE_EPOCH` is the last commit touching the recipe, `TZ=UTC`, `LC_ALL=C` and
  `umask 022`.

### The output

For each recipe, `river-build` writes:

- `<pkgname>-<pkgver>-<pkgrel>-x86_64.pkg.tar.zst`: pacman format (`.PKGINFO`, `.BUILDINFO`,
  `.MTREE`, sorted members, uid/gid 0, clamped mtimes);
- `<same>.spdx`: an SPDX 2.3 tag-value SBOM covering the package, its sources, the seed image
  and links to its dependencies' SBOMs.

It also writes one `SHA256SUMS` over everything in the output directory.

## Trust

- **Sources.** The sha256 in `sources.lock` is the pin. Where upstream signs, the signature
  must chain to the pinned primary fingerprint, and the key is `keys/pgp/<FPR>.asc`. Upstreams
  that do not sign need a `# nosig:` line saying so. skarnet (all three starter sources) does
  not sign; its hashes were checked against a fresh download on 2026-09-23.
- **What we ship.** It is signed by the **owner's personal OpenPGP key**
  (`keys/owner/fingerprint`, the same key as [`KEYS`](../KEYS)), using `river-sign` on
  the owner's machine. CI and `river-build` never hold a private key. The full key hierarchy,
  rotation and revocation are in [`docs/OWN-BASE.md` §10.1](../docs/OWN-BASE.md#101-signing-and-the-key-hierarchy-owner-decision).

## The s6 layer of the Arch-based image (2026-11-28)

The transitional image moves from Artix to official Arch Linux plus `[runink]`. Every init and
service package then comes from this repository, built by `build/build-all.sh` with
`RIVER_BASE=arch`; nothing is taken from another distribution's binaries or service packages.

| Package(s) | Source | Notes |
|---|---|---|
| `skalibs`, `execline`, `s6`, `s6-rc`, `s6-linux-init`, `s6-portable-utils`, `s6-frontend` | skarnet.org release tarballs, sha256-pinned (`build/pkgbuilds/<name>`) | skalibs 2.16.0.0, execline 2.10.0.0, s6 2.15.1.1, s6-rc 0.8.0.2, s6-linux-init 1.2.0.2, s6-portable-utils 2.3.1.2, s6-frontend 0.2.0.0. The recipes in this directory are still at the older skalibs/execline/s6 versions; bumping them is separate. |
| `runink-s6-base` | ours (`build/pkgbuilds/runink-s6-services`) | `/etc/s6.conf`, the s6-linux-init skeleton, the base services in `/etc/s6/adminsv`, gettys, `/usr/bin/init` and friends, alpm hooks that regenerate `/etc/s6/current` and sync the s6-rc repository |
| `runink-s6-services` | ours (same PKGBUILD) | s6-rc pipelines for dbus, NetworkManager, sshd, chrony, bluetoothd, cupsd and tailscaled |
| `runink-greetd-s6` | ours | greetd and seatd services |
| `runink-udev`, `runink-sysusers-tmpfiles` | repackaged from the pinned Arch `systemd` package | see below |
| `greetd` | upstream release, rebuilt without Arch's `systemd` dependency | |
| `dbus` | upstream release (existing PKGBUILD) | with `RIVER_BASE=arch`: no elogind patch, no libelogind link |

**udev.** Arch has udev only inside the `systemd` package, which the closure forbids. eudev is
not in Arch and its `libudev.so.1` collides with `systemd-libs`'; mdevd writes no udev database,
which libinput, wlroots and udisks2 need; building udev from systemd's sources needs Python. So
`runink-udev` repackages `udevadm`/`systemd-udevd`, `systemd-hwdb`, the rules and helpers, and
`runink-sysusers-tmpfiles` repackages `systemd-sysusers`, `systemd-tmpfiles` and the
`libsystemd-shared` they link, byte for byte, from the systemd package of the same Arch snapshot
(262.2-1 in the 2026/11/25 snapshot). This is the library-only policy of
[`docs/ARCH-BASE.md`](../docs/ARCH-BASE.md) section 4: no systemd daemon, no unit. The build
refuses a `systemd-libs` of another version.

**Seats and logind.** seatd (in Arch) is the seat provider on the live medium and on installed
machines. elogind is not in Arch, and its meson build runs Python generators (jinja2 templates,
gperf, syscall and filesystem lists), so it cannot be built here yet. **Not done:** a
`runink-elogind` package needs a no-Python build patch, the way `dbus`'s 0003 patch removed
Python from dbus's build. Until it exists the Arch-based image has no logind: DEsk's logind
power actions, polkit's session checks (polkit is also one of the packages Arch builds against
systemd, below) and `pam_elogind`'s `XDG_RUNTIME_DIR` are missing.

**Still to rebuild for the Arch-based image** (they hard-depend on Arch's `systemd`): `cups`,
`polkit`, `xdg-desktop-portal`, `mkinitcpio` and `pacman`. The `cupsd` service is written; the
package under it is not.
