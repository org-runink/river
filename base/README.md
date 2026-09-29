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
