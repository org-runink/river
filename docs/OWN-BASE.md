# Runink River's own base: architecture and migration plan

Status: **PLAN**, dated 2026-09-23. Nothing described here ships yet. The ISO is still built
by the Artix tooling (`artools`/`buildiso`, `iso-profiles/`, `scripts/patch-artools.go`),
and that stays the builder until Phase 3 below exits. The only code that exists is the Phase 0
scaffold in [`base/`](../base/README.md).

An Arch Linux interim base (Arch packages plus `[runink]`, s6 kept) was measured on
2026-09-25 and deferred; see [ARCH-BASE.md](ARCH-BASE.md).

## Contents

1. [Decision and scope](#1-decision-and-scope)
2. [Recommendations at a glance](#2-recommendations-at-a-glance)
3. [Toolchain bootstrap and the libc choice](#3-toolchain-bootstrap-and-the-libc-choice)
4. [Package format and manager](#4-package-format-and-manager)
5. [The build system](#5-the-build-system)
6. [Image assembly and the installer](#6-image-assembly-and-the-installer)
7. [Package set: what we build and what we drop](#7-package-set-what-we-build-and-what-we-drop)
8. [Init, session and network](#8-init-session-and-network)
9. [KDE Plasma 6 workstation](#9-kde-plasma-6-workstation)
10. [Security](#10-security)
11. [Licensing](#11-licensing)
12. [Migration phases, exit criteria and estimates](#12-migration-phases-exit-criteria-and-estimates)
13. [Open questions for the owner](#13-open-questions-for-the-owner)

---

## 1. Decision and scope

Runink River stops being an Artix derivative and becomes its own Linux base, built from source. The
new base is built around the zen-kernel 7.2.x fork, which lands in a separate PR. The main
branch today ships `linux-runink` = kernel.org 6.18 LTS. The kernel recipe in the own base is
ported from whichever kernel PKGBUILD is on `main` when Phase 2 starts, and the kernel-bump
rule still holds: OpenZFS 2.4.4 declares support for Linux 4.18 to 7.2, so the zen 7.2.x fork is
inside its range, but every kernel bump must still be checked against that range.

The goals are fixed by the owner:

| Goal | What it means for the design |
|---|---|
| Sovereign | No runtime fetch. Every upstream is pinned by sha256, and by signature where upstream signs. Builds are reproducible. |
| s6, not systemd | s6 + s6-rc + s6-linux-init from skarnet sources. No systemd components in the server image. |
| ZFS root with native encryption | Every dataset sits under one encryption root, so every file is encrypted at rest. |
| Dual-stack host, IPv6-only k0s cluster | Unchanged from today. |
| Minimal closure | Attack surface is the metric. The server drops dbus, elogind, NetworkManager, GRUB, pacman and gnupg. |
| Every file encrypted and shadow-moded | ZFS native encryption at rest, plus default-deny file modes enforced at build time (§10.6). |
| Open source, heading toward a Linux Foundation project | License clarity, DCO (`git commit -s`), SPDX SBOMs. Dropping artools removes a GPL-3.0 fork that our own code patches. |
| Two profiles | The server first. The KDE Plasma 6 workstation is a separate, later phase (§9). |

Out of scope for this PR: wiring anything into CI, `make`, or the ISO; any change to what
installed nodes run.

## 2. Recommendations at a glance

| Question | Recommendation | Why, in one line |
|---|---|---|
| libc | **glibc** (hardened build) | The GitHub Actions runner and Flutter both effectively need it. musl buys little because k0s and Go are static anyway. |
| Package format | **Keep the pacman format** (`.pkg.tar.zst` + `.PKGINFO`/`.BUILDINFO`/`.MTREE`) | The `runink-*` PKGBUILDs and the `[runink]` repo already use it, and installed nodes can read it. |
| Package manager on the node | **Server: none** (image-based ZFS boot-environment updates). **Workstation: pacman.** | Removes pacman, libcurl, gpgme and gnupg from the server closure. Developers still need to install software on a workstation. |
| Build system | `base/river-build`: lockfile-only sources, a digest-pinned hermetic container with `--network=none`, `SOURCE_DATE_EPOCH`, and an SPDX SBOM per package | Proven in the scaffold: s6, skalibs and execline build **bit-for-bit reproducibly** (§5.5). |
| Image assembly | Our own `river-compose` (rootfs, squashfs, ISO), replacing buildiso | buildiso is the reason we patch artools at runtime. |
| Boot | **Signed EFI-stub kernel image (UKI) on the ESP. GRUB is dropped.** | GRUB cannot read natively encrypted ZFS, and GRUB is GPL-3.0. |
| Network (server) | **dhcpcd (dual-stack; IPv6-only on request) + iproute2 + nftables.** NetworkManager and wpa_supplicant are dropped. | Takes about 20 packages out of the closure, including libcurl. dhcpcd hooks replace the NetworkManager dispatcher. |
| Session (server) | **No elogind, no dbus** | Nothing on a headless k0s node needs logind. The stack already runs in init context. |
| Session (workstation) | elogind + dbus + polkit | KWin, SDDM and PowerDevil talk to the logind D-Bus API. |
| Trust root | **A dedicated "Runink River Release Engineering" OpenPGP key** (Arch-packager model). The signing subkey lives on a hardware token and the primary key stays offline. | Decided by the owner. The key never enters CI (§10.1, [RELEASE-SIGNING.md](RELEASE-SIGNING.md)). |
| Secure Boot | **Our own keys enrolled in firmware** (PK/KEK/db), keeping the Microsoft UEFI CA for option ROMs | We control the fleet. shim review is a later option for a public ISO. |
| TPM2 unseal | A small static tool on **google/go-tpm**, or on tpm2-tss ESYS built `--disable-fapi` | Either way there is no curl and no json-c. go-tpm also drops tpm2-tss and OpenSSL from the initramfs. |
| Initial server size | **About 65 runtime packages + about 45 build-only**, against about 206 in today's closure | Details in §7. |

## 3. Toolchain bootstrap and the libc choice

### 3.1 Bootstrap stages

Each stage is a set of ordinary recipes in `base/recipes/`, built by `base/river-build` from
`base/sources.lock`. The difference between stages is which toolchain image they build *in*.

| Stage | Built in | Produces | Notes |
|---|---|---|---|
| **0: seed** | nothing (third party) | `buildpack-deps:trixie` pinned by digest in `base/seed.lock` (gcc 14.2, glibc 2.41) | The one explicit hole in "built from source". It is named as the build tool in every SBOM. The scaffold uses it today. |
| **1a: cross** | seed | `x86_64-river-linux-gnu` cross binutils → gcc pass 1 (C only, no libc) → linux-api-headers → glibc → gcc pass 2 (C, C++) | LFS "cross toolchain" chapter. The distinct target triplet stops the seed's headers or libraries from leaking in. |
| **1b: temporary tools** | seed + cross | Minimal userland (make, bash, coreutils, sed, grep, gawk, tar, xz, zstd, patch, findutils, diffutils, m4, bison, perl) cross-compiled into a clean sysroot | This is the chroot that stage 2 runs in. The seed is no longer used after this point. |
| **2: native** | stage 1b chroot, as a container image | Native binutils, gcc, glibc and the build userland, rebuilt by themselves | Assembled into `river-builder:<n>`, which replaces the seed in `seed.lock`. |
| **3: fixed point** | `river-builder:<n>` | The same toolchain again | Exit check: the stage-3 toolchain packages are **bit-identical** to stage 2. That proves the seed no longer influences the output. |

Stage 3 does not make Runink River *bootstrappable* in the stage0/live-bootstrap sense: stage 0 is
still a trusted binary. Full bootstrappability (building from a few hundred bytes of hex via
[live-bootstrap](https://github.com/fosslinux/live-bootstrap)) is a Phase 4 option. It
removes the last trusted binary and is a strong story for a Linux Foundation project, but it
costs weeks and is not needed for the server to ship.

Go is its own bootstrap problem: Go 1.24+ needs Go ≥ 1.22 to build. The plan is the upstream
bootstrap chain from a pinned, sha256-verified official Go release in `sources.lock`. Go has
built its toolchain reproducibly since 1.21, so the result can be checked against upstream's
published hashes (`golang.org/x/build/cmd/gorebuild`). Rust is not needed on the server.

### 3.2 glibc vs musl

| Component | glibc | musl | Weight |
|---|---|---|---|
| k0s (+ bundled containerd, runc, kube-router, kubelet) | ✓ | ✓ static Go, libc-agnostic | none |
| Go toolchain and our Go binaries | ✓ | ✓ (`cmd/go` is pure Go; cgo works) | none |
| OpenZFS userland | ✓ primary target | ✓ (Alpine ships it) | low |
| GitHub Actions runner (on the node, `runner-keepalive.sh`) | ✓ | ✗ upstream ships glibc-only .NET builds. The bundled Node for JS actions is glibc too. | **high**: the reference deployment runs its CI runner on the node |
| Flutter (developers build Flutter applications on the workstation) | ✓ | ✗ the prebuilt engine and desktop toolchain are glibc. On musl you build the engine from source. | **high** for the workstation |
| KDE Plasma 6 / Qt 6 | ✓ | ~ works (postmarketOS, Alpine), with occasional patches | medium |
| App containers | n/a | n/a (they carry their own libc) | none |
| Attack surface | larger (NSS, iconv/gconv, locales) | smaller, simpler | medium |

**Recommendation: glibc.** Two workloads of the reference deployment, the on-node Actions runner and
Flutter, have no supported musl path, and the components where musl would be free (k0s, Go)
are static anyway. glibc's extra attack surface is handled by how we build and ship it:

- `--enable-stack-protector=strong --enable-bind-now --enable-fortify-source`, and CET where the
  toolchain supports it;
- no nscd; `nsswitch.conf` limited to `files` and `dns`;
- only the C.UTF-8 locale;
- **gconv modules pruned to the UTF family.** The iconv exploit class (for example
  CVE-2024-2961 in ISO-2022-CN-EXT) lives in modules that the server has no reason to carry.

Revisit this only if the runner leaves the node. The runner is the dominant constraint.

## 4. Package format and manager

**Keep the pacman package format and makepkg's shape, and remove the package manager from the
server.**

What we keep:

- **The archive format:** `.pkg.tar.zst` with `.PKGINFO`, `.BUILDINFO` and `.MTREE`. The
  scaffold emits exactly this, and host `pacman -Qip` reads it. It is simple (a tar, a
  key/value header, an mtree), it is documented, and it is what `[runink]`, the six existing
  `build/pkgbuilds/runink-*` PKGBUILDs and `installer/lib/35-pacman-keyring.sh` already assume.
- **The recipe shape:** the PKGBUILD variables and `build()`/`package()`, so porting a PKGBUILD
  is mechanical (see [`base/README.md`](../base/README.md) for the differences: POSIX sh, no
  arrays, no URLs).
- **pacman as a build-host tool:** `river-compose` uses `pacman --root` in the builder
  container to resolve and unpack a profile's closure (conflict and file-ownership checking
  for free). It is never copied into the server image.

What we do *not* keep on the server:

- **pacman itself, and its closure:** libalpm, libarchive, **libcurl**, gpgme, gnupg,
  libgcrypt, libassuan, libksba, npth and pinentry. On a node that "does not fetch", a package
  manager that links an HTTP client is the wrong shape. The server is updated as a whole
  signed image received into a new ZFS boot environment (§6.4), which is also what makes
  rollback atomic.
- **Install scriptlets.** Recipes may not ship `.INSTALL` scripts. Users, directories and
  services are declared in the profile and applied once, deterministically, by `river-compose`.
  This rules out a whole class of "the package ran code as root at install time".

The workstation keeps pacman, because developers install software. Its repo is the same
signed `river` repo, trusted through the owner key in the `river-keyring` package (§10).

**Alternative considered: a simpler format of our own** (a tar plus a TOML manifest). It would
save almost nothing. The server runs no package manager in either design, so the format only
matters at compose time, where the pacman format and tools are already known and already in use.
It would cost a rewrite of every existing PKGBUILD, a new tool for workstation users, and lost
familiarity for contributors. Rejected.

## 5. The build system

The scaffold implements the core of this (Phase 0). See [`base/README.md`](../base/README.md).

### 5.1 Pinned sources

`base/sources.lock` has one line per upstream file: `name version sha256 url sig_url sig_key`.

- The **sha256 is the pin.** `RIVER_BASE_MIRROR` can point the fetch at our own mirror, which
  can only ever serve the same bytes.
- **Signatures are verified when upstream signs.** The signature must chain to the pinned
  **primary** fingerprint. Upstream keys live in `base/keys/pgp/<FPR>.asc` and are verified
  with `gpgv` against a private, single-key keyring, never the user's keyring. This was tested
  against OpenZFS 2.4.4's real `.asc`: a valid signature passes and a one-byte tamper fails.
- **An unsigned upstream is written down, not accepted silently.** skarnet publishes no
  signatures, so each such source needs a `# nosig: <name> <reason>` line, and `river-build
  lint` fails without it.
- Recipes contain **no URLs** (lint-enforced), so a recipe cannot fetch anything the lock does
  not pin.
- **The source mirror.** Before Phase 3 every locked file is mirrored on infrastructure we
  control, keyed by sha256. That is the sovereignty answer to upstreams disappearing (tayga's
  upstream has been dormant since 2011), and it is also the GPL "corresponding source" offer (§11).

### 5.2 The hermetic builder

- The fetch is the **only** networked step, and it runs on the build host.
- Every package builds in a **fresh container** from the digest-pinned builder image with
  `--network=none --pull=never`. Build inputs (the extracted sources plus the recipe
  dependencies' packages) are bind-mounted read-only or staged, so nothing else is visible.
- `river-build` refuses a builder image that is not pinned by digest.
- Phase 1 replaces the third-party seed with our own `river-builder` image (§3.1).

### 5.3 Reproducibility

These are fixed per build: `SOURCE_DATE_EPOCH` (the last commit touching the recipe),
`TZ=UTC`, `LC_ALL=C`, `umask 022`, the build path `/build` with `-ffile-prefix-map=/build=.`,
`-j` (verified not to matter), archive members in sorted order with uid/gid 0 and mtime clamped
to the epoch, and zstd `-19 -T1`.

Standard hardening flags apply to every recipe: `-O2 -march=x86-64-v3 -fstack-protector-strong
-fstack-clash-protection -fcf-protection -D_FORTIFY_SOURCE=3 -fno-plt`, with `-z relro -z now
-z noexecstack`. The x86-64-v3 target matches the fleet baseline in AGENTS.md: AVX2, no AVX-512.

### 5.4 SBOM per package

Each package gets an SPDX 2.3 tag-value document with:

- the built package (sha256, license);
- each upstream source (URL, sha256, and whether it was OpenPGP-verified);
- the builder image (by digest);
- `DEPENDS_ON` and `BUILD_DEPENDENCY_OF` links to the dependencies' own SBOMs through
  `ExternalDocumentRef`.

`river-compose` will aggregate these into one image SBOM, shipped on the node at
`/usr/share/river/sbom/` so the node can list its own inventory without a package manager.

### 5.5 What the scaffold has proven (2026-09-23)

- The three starter tarballs were downloaded fresh, and their hashes matched both the lock and
  upstream's `.sha256` files.
- `river-build build s6` builds skalibs, then execline, then s6 in the seed container. It
  produces pacman-format packages that `pacman -Qip` reads, SPDX SBOMs, and a SHA256SUMS.
- The s6 binaries link only `libc.so.6` (skalibs and libexecline are linked in statically) and
  are PIE with full RELRO/BIND_NOW and a non-executable stack.
- **A second build in a different work directory, at a different `-j`, produced an identical
  SHA256SUMS.**
- `river-sign` was tested with a throwaway key that has a signing subkey. It signs, verifies
  the signatures back to the primary key through the exported public key alone, refuses to run
  under `CI=true`, refuses the placeholder fingerprint, and refuses to sign after a one-byte
  tamper.

### 5.6 Still to add (tracked, not done)

- Split packages (`-dev` for headers and static libraries). Today `execline` carries
  `libexecline.a` because s6 links it.
- Kernel-style signatures over the *decompressed* tarball (kernel.org signs `.tar`, not
  `.tar.xz`). This needs a `sig_over` column.
- Non-root file ownership inside packages. Everything is root:root today; `river-compose`
  will apply ownership from the profile.
- The kernel and zfs.ko must build in **one** job so the ephemeral module-signing key never
  persists (§10.4).
- A rebuilder: CI rebuilds every package on a second host and diffs the result.

## 6. Image assembly and the installer

### 6.1 `river-compose` replaces buildiso

Inputs:

- a profile: `base/profiles/<name>/packages`, the successor of `Packages-Root`, and still an
  allow-list with a per-profile `forbidden.closure`;
- the profile's `root-overlay/` (moved as-is from `iso-profiles/<name>/`);
- a declarative users/groups/directories file;
- the s6-rc source tree.

Steps:

1. `pacman --root "$rootfs" -S` resolves the profile's closure from the local `river` repo in
   the builder container. Signatures are required (§10), there are no scriptlets, and hooks are
   disabled.
2. The closure lint runs. Today's `river lint closure` needs `pactree` and the live Artix
   repos, and so is not in CI. Against a local repo it can run in CI.
3. Apply `root-overlay/`, then create users and groups from the declarative file. This is not
   `useradd` at install time.
4. Run the deterministic post-steps: `ldconfig`, `depmod`, `s6-rc-compile`, the initramfs, the
   UKI (§10.3), the aggregated SBOM, and the file-mode audit (§10.6).
5. Clamp every mtime to `SOURCE_DATE_EPOCH`.

Outputs (all unsigned, with a SHA256SUMS, signed later by the owner with `river-sign`):

- `river-<ver>-rootfs.tar.zst`: the update payload for existing nodes (§6.4);
- `river-<ver>.sfs`: the live squashfs;
- `river-<ver>.iso`: a UEFI-only ISO. xorriso builds it with an El Torito EFI system-partition
  image holding the signed live UKI. The volume label stays per-profile (`RIVER`,
  `RUNINK_WORKSTATION`). That was the one non-cosmetic job `patch-artools.go` did, and
  `river-compose` does it natively.

Live boot uses a small initramfs hook of our own. It finds the medium by label, mounts the
squashfs under a tmpfs overlay, and runs `switch_root`, replacing artools' live hooks.
BIOS boot is dropped: `00-preflight.sh` already requires UEFI.

This removes from the repo (Phase 3): `scripts/patch-artools.go`, `scripts/build-iso-box.sh`,
`scripts/fetch-iso-profiles.sh`, the Artix `builder/` container, `pacman/mirrorlist.pin`, the
Artix repos in `pacman.conf.in`, `profile.yaml` and `profile.conf`.

### 6.2 The installer stays

`installer/lib/NN-*.sh` is already our own code. It changes where the base changes:

| Step | Change |
|---|---|
| `00-preflight` | Check for a TPM2 (`/dev/tpmrm0`) and Secure Boot setup mode. Drop the basestrap/pacstrap probes. |
| `10-disk-zfs` | Create the pool with **one encryption root** (§10.5). State datasets (`state`, `containers`, `home`) move under it. There is still no boot pool: `/boot` lives on the ESP as a UKI. |
| `20-clone-rootfs` | Unchanged in principle (install-from-live). Exclusions are renamed from `/run/archiso` and `/var/lib/artools`. |
| `30-target-config` | The `curl` stripping disappears, because curl is no longer in the closure (§8.3). |
| `35-pacman-keyring` | Server: removed (no pacman). Workstation: populate the `river` keyring (the owner key) instead of the `artix`/`archlinux` keyrings. |
| `40-boot-grub-zfs` → `40-boot-uki` | Enroll Secure Boot keys if the firmware is in setup mode. Install the signed UKI as `EFI/BOOT/BOOTX64.EFI`, keeping the removable-path invariant, with the previous one as `EFI/RIVER/previous.efi`. Seal the pool key to the TPM (§10.5). hostid handling is unchanged. |
| `50`–`80` | Unchanged apart from the rootless-podman leftovers in `50-runink-user.sh`. That file still writes `containers.conf`, which k0s does not need (cleanup, not base work). |

### 6.3 One kernel, one module

The kernel recipe comes from the zen 7.2.x PR. It builds the kernel image, the module tree
and **zfs.ko in the same job** (§10.4), plus `cpupower` from the same kernel tarball. There is
still exactly one kernel.

### 6.4 How existing installs upgrade

Existing nodes are Artix-based. Their pools are unencrypted, GRUB boots them, and on nodes
installed before 2026-09-07 the state datasets are *inside* the boot environment. There is no
safe in-place package upgrade from Artix to the own base, and there should not be one. The
path is a new boot environment:

1. **Verify.** `river-update` (new, ours) checks the owner signature on `SHA256SUMS`, then the
   rootfs payload's hash.
2. **Encrypt.** If the node has no encryption root yet, create `zriver/enc` (encrypted, raw key
   sealed to the TPM plus a recovery passphrase), and `zfs send | zfs receive` the state
   datasets into it. This needs free space equal to the live state; the big caches can be
   dropped and regenerated instead. A node with no room is **reinstalled from the ISO**, with
   `/var/lib/core` and `/var/lib/k0s` rebuilt as they are after a normal install.
3. **Stage.** Receive the rootfs into a new BE under the encryption root. Copy node identity
   into it: `/etc/hostid`, SSH host keys, `/etc/runink/*`, k0s PKI and state, the runner's
   `.runner`/`.credentials`.
4. **Switch.** Install the signed UKI to the ESP. The old GRUB entry stays as
   `EFI/RIVER/artix-grub.efi` for rollback, with Secure Boot left off until step 5.
5. **Confirm, then close the door.** Reboot and run the health check (`tests/assert-golden.sh`
   on the node). The node only enrolls Secure Boot keys, deletes the fallback, and (after a grace
   period) destroys the old BE once the health check passes. If it fails, the node boots the old
   BE.

After migration, every later update is steps 1, 3 and 5: a new BE per release, rollback by
selecting the previous UKI. Existing pool names are kept as they are.

## 7. Package set: what we build and what we drop

The Artix `base` meta-package is replaced by an explicit list. Today's server closure is about
206 packages (measured in the `ci.yml` header: 33 explicit, about 206 resolved).

### 7.1 Server (`iso-profiles/river/Packages-Root`)

| Today | Own base | Notes |
|---|---|---|
| `base` (meta) | **build** ~33: filesystem (ours), glibc, gcc-libs, libxcrypt, tzdata, iana-etc, ca-certificates, bash, readline, ncurses, coreutils, findutils, grep, sed, gawk, diffutils, tar, gzip, xz, zstd, less, procps-ng, util-linux, shadow, kmod, eudev, iproute2, iputils, acl, attr, libcap, zlib, openssl | **drop** from base: pacman, gettext (build with `--disable-nls`), file, psmisc, which, pciutils/usbutils (evaluate), texinfo, and every Artix-specific helper (esysusers, etmpfiles, artix-* keyrings). gcc-libs stays for libstdc++, which the Actions runner needs. |
| `linux-runink` | **build** | From the zen 7.2.x PR. |
| `mkinitcpio` | **build** (Phase 2), **replace** (Phase 4) | Replaced by a small initramfs generator of our own with the vendored zfs hook. |
| `linux-firmware-intel` | **build** (repackage) | From a pinned linux-firmware tag: only the Intel files the 155H needs. The blobs are binary (§11). |
| `grub`, `efibootmgr` | **drop** | Signed UKI on the removable path. There is no NVRAM entry to manage. |
| `runink-zfs`, `runink-zfs-utils` | **build** (port the PKGBUILD) | +libtirpc (the zfs userland needs XDR). |
| `s6`, `s6-rc`, `s6-linux-init` | **build** from skarnet | +execline, +skalibs (build-only). s6 is already a recipe. |
| `s6-scripts` | **replace** | Our own s6-rc source tree. The Artix service scripts go away. |
| `elogind-s6`, `dbus-s6` | **drop** | §8.2 |
| `networkmanager-s6`, `networkmanager`, `wpa_supplicant` | **drop** → `dhcpcd` | §8.3 |
| `openssh-s6`, `openssh` | **build** openssh; the s6 service is ours | Built without PAM. |
| `iptables` | **build**, then evaluate dropping it | k0s ships its own iptables binaries for kube-router/kube-proxy. If nothing on the host calls the system one, drop it in Phase 4. |
| `nftables` | **build** minimal | +libnftnl, +libmnl. Built without the interactive CLI, JSON support or system gmp (exact configure flags confirmed when the recipe lands), so there is no readline, gmp or jansson. |
| `rsync` | **build** | With bundled popt. No openssl: xxhash and zstd only. |
| `runink-tayga` | **build** (port) | Move to the maintained apalrd/tayga fork. Upstream 0.9.2 is from 2011. |
| `sudo` | **replace** → `opendoas` | A small fraction of sudo's code, ISC-licensed, no PAM. Needs the audit in open question 6. |
| `vim` | **build** small | `--with-features=small`, no interpreters, no GUI, no X. |
| `cpupower` | **build** | From the kernel tree. |
| `bubblewrap` | **build** | libcap only. |
| `go` | **build** | §3.1. This is still the scoped exception AGENTS.md records. |
| `runink-runtime`, `runink-core`, `runink-k0s` | **build** (port the existing PKGBUILDs) | Already ours. k0s stays a pinned upstream release binary checked by sha256; building k0s from source is a Phase 4 option. |
| *(new)* | **build**: dhcpcd, opendoas, river-unseal (ours), river-update (ours), river-s6-services (ours), gnupg's `gpgv` (Phase 2 update verifier; §10.1) | |

**Server count: about 63 runtime packages** (base ~33, init 5, boot/ZFS ~7, network ~8,
security ~4, ops 4, Runink 3) **plus about 45 build-only recipes**: the stage 1/2 toolchain
(binutils, gcc, gmp, mpfr, mpc, isl, linux-api-headers, make, m4, bison, flex, perl, pkgconf,
autoconf, automake, libtool, bc, cpio, elfutils, pahole, cmake, meson, ninja, go (no Python:
the kernel's one Python step runs as build/tools/bpfdoc), patch, squashfs-tools, xorriso,
mtools, dosfstools and so on). That is
**about 110 recipes in total, against about 206 packages in today's closure**, with no dbus,
NetworkManager, glib, libcurl, gnupg, pacman, GRUB or Python in the image.

### 7.2 Server live medium (`Packages-Live`, `profile.yaml`)

| Today | Own base |
|---|---|
| `dialog`, `parted`, `gptfdisk`, `dosfstools`, `util-linux`, `rsync`, `runink-zfs(-utils)` | **build** (live-only extras on top of the server set) |
| `arch-install-scripts`, `artools-base` | **drop** (install-from-live needs none of them) |
| `mkinitcpio-nfs-utils` | **already dropped** (2026-10-01, no initramfs here uses the `net` hook) |
| `artix-grub-live` | **already ours**: `runink-grub-live` (`build/pkgbuilds/runink-grub-live`) since 2026-10-01. Keeps the live medium's GRUB scaffolding with no `artix-*` package; this phase replaces `buildiso` itself, which is what still reads those paths. |
| `git` | **drop**. The live medium does not need it, and AGENTS.md's no-fetch posture applies. |
| `squashfs-tools` | **build-only** (`river-compose`) |

### 7.3 Workstation (`iso-profiles/runink-workstation/Packages-Root`)

This is Phase 5 (§9). Mapping:

| Today | Own base |
|---|---|
| base, kernel, mkinitcpio, zfs, s6 set, openssh, nftables | Same as the server |
| `grub`, `efibootmgr` | **drop** (UKI) |
| `linux-firmware-amdgpu` + `-intel` | **build** (repackage, both vendors) |
| `elogind-s6`, `dbus-s6` | **build** elogind + dbus (with our own s6 services) + polkit (the duktape backend, not mozjs) |
| `networkmanager(-s6)`, `wpa_supplicant` | **build** NetworkManager with **iwd** instead of wpa_supplicant (drop wpa_supplicant) |
| `iptables` | Same evaluation as the server |
| `plasma-meta`, `sddm(-s6)`, `konsole`, `dolphin`, `kate` | **build** Qt 6 + KF6 + Plasma 6 + these apps (§9). We pick the Plasma components explicitly; there is no meta-package. |
| `plymouth` | **build** |
| `mesa`, `vulkan-icd-loader`, `vulkan-radeon`, `vulkan-intel`, `libva-mesa-driver`, `mesa-utils` | **build** (+LLVM for radeonsi/llvmpipe, +libdrm, wayland, wayland-protocols) |
| `pipewire(-pulse, -alsa)`, `wireplumber` | **build** (+alsa-lib, lua for wireplumber) |
| fonts ×4 | **build** (data-only repackaging) |
| `base-devel`, `git`, `curl`, `go`, `rsync`, `vim`, `sudo`, `zstd`, `util-linux`, `cpupower`, `bubblewrap` | **build**. The workstation keeps its toolchain on purpose (see its `forbidden.explicit`). It keeps `sudo`, since developer tooling expects it. |

Workstation estimate: **about 450 to 600 recipes** on top of the server set. Qt 6 alone is
about 25 modules, KF6 is about 70 frameworks, and Plasma is about 60 components.

## 8. Init, session and network

### 8.1 s6 from skarnet sources

The packages are skalibs (static, build-only), execline, s6, s6-rc and s6-linux-init, all ISC.
The first three are recipes today. Next come s6-rc and s6-linux-init, then **our own s6-rc
source tree** (`river-s6-services`): the longruns and oneshots that live in
`iso-profiles/river/root-overlay/etc/s6/` today, plus the base services Artix's `s6-scripts`
provided (udevd, the hostname and mount oneshots, getty, sshd, dhcpcd, nftables). These are
compiled at image-build time by `s6-rc-compile`, so the node boots a precompiled database.

mdevd (skarnet's uevent daemon) is a Phase 4 candidate to replace eudev. It is much smaller,
but it does not execute udev rules, and the zfs hook and by-id links rely on them today. Phase 2
ships eudev.

### 8.2 elogind: none on the server

Nothing on a headless k0s node needs logind or D-Bus:

- sshd runs without PAM or logind;
- kubelet uses the cgroupfs driver;
- the Runink stack already launches from `rc.local` in init context;
- the only reason `KillUserProcesses=no` exists is that elogind is present.

Dropping both takes out elogind, dbus, libelogind and their s6 services. The rc.local
init-context rule in AGENTS.md becomes simpler: there is no session reaper left.

The workstation needs a logind (KWin, SDDM and PowerDevil use the `org.freedesktop.login1` API),
so it gets elogind, LGPL-2.1. seatd alone does not satisfy Plasma.

### 8.3 Network: dhcpcd + iproute2 + nftables on the server

NetworkManager brings in glib2, libnm, **libcurl** (today stripped to `/usr/bin/curl` by
`30-target-config.sh`, with the library kept), nghttp2, libpsl, dbus, wpa_supplicant and
polkit hooks. That is roughly 20 packages of closure for a node with one wired uplink.

The replacement:

- **The kernel does SLAAC.** `accept_ra=2` on uplinks when forwarding is on, which it is for
  k0s. `sysctl.d/99-runink.conf` already sets forwarding.
- **dhcpcd** (BSD-2, privilege-separated, libc only) runs as an s6 longrun with `noipv4`,
  `ipv6rs` and `ia_na` only if the network requires DHCPv6. It supplies RDNSS/DNSSL into
  `resolv.conf`, which the kernel will not do.
- **MAC spoofing**, replacing `cloned-mac-address=stable`: an s6 oneshot derives a stable
  locally-administered MAC per interface (an HMAC of the interface name keyed by a per-node
  secret) and applies it with `ip link set address` before dhcpcd starts. The SLAAC identity
  stays stable, as it is today.
- **The firewall reapply**, replacing NetworkManager `dispatcher.d/50-runink-fw` and
  `61-runink-nat64`: dhcpcd hook scripts (`/usr/lib/dhcpcd/dhcpcd-hooks/`). The logic moves
  over unchanged.
- **Wi-Fi on the server: dropped.** If a site ever needs it, add iwd (no wpa_supplicant) as an
  opt-in.

**Why not write our own IPv6 client now?** RA parsing and RDNSS are small, but dhcpcd is
mature, audited and privilege-separated. A Go `river-netd` stays possible later if dhcpcd ever
becomes the largest network component. It is not a Phase 2 item.

The workstation keeps NetworkManager (with iwd) for the Plasma applet and roaming.

## 9. KDE Plasma 6 workstation

This is a separate, later phase (Phase 5). The server must not wait for it, and the
workstation keeps building on the current Artix tooling until then. That is the one place
where the transitional builder outlives Phase 3.

What it takes, honestly:

- **Stack:**
  - Qt 6 (~25 modules, and QtWebEngine if any app needs it: a Chromium build of several hours
    that we should avoid by choosing apps without it);
  - KF6 (~70);
  - Plasma 6 (~60);
  - KDE apps (konsole, dolphin, kate + deps);
  - Mesa + LLVM + libdrm + Wayland + wayland-protocols + libinput + xkbcommon + libei;
  - PipeWire + WirePlumber; SDDM; elogind + dbus + polkit;
  - NetworkManager + iwd; fonts; ICU, harfbuzz, freetype, fontconfig, cairo and pango (for
    Plymouth and GTK-less theming); Xwayland for legacy apps.
- **Size:** about 450 to 600 recipes, several of them heavy (LLVM, Qt base, Mesa).
- **Cadence:** KF6 releases monthly, Plasma has regular point releases, Mesa and Qt ship
  frequent security fixes. Keeping current is continuous work.
- **Effort:** **6 to 12 engineer-months** to a daily-drivable Plasma session at parity with
  today's workstation profile, then **about 0.5 FTE ongoing**.
- **The s6 wrinkle:** Plasma assumes systemd user units in places (for example the
  `plasma-*.service` session startup). Plasma still supports the non-systemd `startplasma`
  path that Artix uses today, so it is solvable, but every Plasma release can regress it.

Recommendation: start Phase 5 only after the server has run on the own base in production for
at least one release cycle. Revisit then whether the workstation needs to be Runink River-built at
all, or whether "Runink River Server + any developer distro with equivalent guardrails" is
enough.

## 10. Security

### 10.1 Signing and the key hierarchy (owner decision)

> **Superseded in part (2026-09-24).** Releases, packages and tags are now signed with a
> **dedicated "Runink River Release Engineering" OpenPGP key** (Ed25519 or RSA 4096), not a
> personal key. Its public key is published at <https://runink.org/.well-known/gpg-key.txt>
> and the procedure is in [RELEASE-SIGNING.md](RELEASE-SIGNING.md), which is authoritative.
> The hierarchy below (offline primary, token-held signing subkey, never in CI) still
> applies; read "owner key" as "the release-engineering key".

**Runink River is signed with a dedicated release-engineering OpenPGP key** ("the owner key" below), which is the Arch-packager model.
The owner key is the root of trust for packages, repository databases and release artifacts.

| Key | Where it lives | Signs | Notes |
|---|---|---|---|
| **Owner primary** (`95C0A7B97D547413E42660DDB06FE75626F15BF3`, in `base/keys/owner/fingerprint` and [KEYS](../KEYS); see [RELEASE-SIGNING.md](RELEASE-SIGNING.md)) | **Offline** (an air-gapped machine or paper backup) | Only certifies subkeys and signs revocations | This fingerprint is what every verifier pins. A subkey change never changes trust on nodes. |
| **Owner signing subkey** | **Hardware token** (an OpenPGP card, e.g. a YubiKey), with a backup subkey on a second token | Every `*.pkg.tar.zst`, repo `*.db`/`*.files`, `SHA256SUMS`, ISO, rootfs payload and SBOM, as detached binary `.sig` files | Expires after 1 to 2 years and is renewed by extending the expiry in an offline ceremony. |
| Upstream source keys | `base/keys/pgp/<FPR>.asc` | Nothing of ours | They vouch for upstream bytes only. **A separate keyring, never merged with the owner's.** |
| Secure Boot db key (X.509) | Owner, offline; optionally the token's PIV applet | UKIs (Authenticode) | UEFI requires X.509, not OpenPGP, so this is necessarily a second key (§10.3). |
| Kernel module signing key | **Ephemeral**, generated and destroyed inside the kernel build | The kernel's own modules and zfs.ko | §10.4 |

**The private key never enters CI.** CI and `base/river-build` produce unsigned artifacts plus
`SHA256SUMS`. The owner then runs **`base/river-sign DIR`** locally. It:

1. checks every file against `SHA256SUMS`;
2. refuses anything the checksums do not cover;
3. runs `gpg --detach-sign` on each artifact, repo db and `SHA256SUMS`;
4. mirrors repo-add's db symlinks for the signatures;
5. **verifies every signature back to the owner's primary key using only the exported public
   key** (`base/keys/owner/<FPR>.asc`), the same one the image ships.

It refuses to run when `CI` or `GITHUB_ACTIONS` is set. That is a tripwire; the real control is
that no CI secret holds the key. Because builds are reproducible, the owner can rebuild locally
(or check an independent rebuilder's hashes) before signing, so signing asserts "I rebuilt
this", not "CI said so".

**Where the owner's public key ships:**

- Every image carries it at `/usr/share/river/keys/owner.asc`.
- The server's `river-update` verifies with `gpgv` against a binary keyring made from it.
  Phase 4 may replace `gpgv` with a small static Go OpenPGP verifier to drop libgcrypt and
  libgpg-error.
- The workstation carries it in the `river-keyring` package: `pacman-key --populate river`
  replaces today's `--populate artix` in `35-pacman-keyring.sh`. Its `pacman.conf` has
  `SigLevel = Required` for `[river]`, replacing today's `[runink] SigLevel = Optional TrustAll`.
- Upstream source keys (`base/keys/pgp/`) are build-time only and never in the image's trusted
  keyring.

**Rotation:**

- **Subkey:** a routine rotation. Add the new subkey in the offline ceremony, publish the
  updated public key (same primary fingerprint), ship it in the next `river-keyring` or image,
  and let the old subkey expire.
- **Primary:** rare. The new key is cross-certified by the old one. A keyring update signed by
  the *old* key carries both keys for one release cycle, then drops the old one. Nodes pin
  fingerprints, so this is always a two-release transition.

**Revocation:**

- Generate a revocation certificate for the primary key at creation and store it offline.
- If a subkey or token is lost, revoke the subkey with the primary and ship the updated public
  key (carrying the revocation) in the next release, signed by the replacement subkey.
- `river-update` refuses signatures by a revoked subkey.
- **Honest limit:** nodes do not fetch, so an offline node learns of a revocation only when it
  receives its next signed update. That comes with the no-runtime-fetch design, and it is why
  the subkey lives on a token (theft of the key material is hard) with a short expiry.

**Sigstore/cosign:** optional and additive at most. For example, CI can attach keyless
provenance attestations for public consumers. It is **never** required for verification:
checking Rekor on a node would be a runtime fetch, and GPG is the primary signature.

### 10.2 Signed repositories

There is one repository, `river`, replacing `[runink]`. It holds packages and a repo db, each
with an owner `.sig`. `river-compose` resolves with `SigLevel = Required` against the owner
keyring, so an unsigned or foreign package cannot enter an image, even from the local build
directory. The TrustAll `file://` repo that `patch-artools.go` injects today goes away.

### 10.3 Secure Boot

The current plan is in [SECURE-BOOT.md](SECURE-BOOT.md); where the two differ, that file
wins.

**Recommended: our own keys, enrolled at install.**

- `40-boot-uki` enrolls the owner's PK, KEK and db when the firmware is in setup mode.
- The db **keeps the Microsoft UEFI CA** so GPU and NIC option ROMs still load. Dropping it can
  brick a box with a discrete GPU. Owner decision; see open question 3.
- The node boots one signed UKI: an EFI-stub kernel with the initramfs and command line
  embedded, signed by the owner's db key. The command line cannot be edited at boot.
- Kernel lockdown (`lockdown=integrity`) is on when Secure Boot is on.
- Signing uses `sbsign` (build-time, owner-local, PKCS#11 for a token-held key). In the build,
  the UKI is produced unsigned and the owner signs it, the same flow as `river-sign`.

**Later option: shim.** A Microsoft-signed shim with our key in MOK makes a public ISO boot on
arbitrary Secure-Boot-on hardware. It requires passing the shim-review process, which is weeks
to months and plausible for a Linux Foundation project. It is not needed for the fleet.

**Boot-environment selection:** `BOOTX64.EFI` is the current BE's UKI and
`EFI/RIVER/previous.efi` the previous one. Rollback is a menu choice in the firmware's boot
menu, or `river-update --rollback`, which swaps the two. There is no NVRAM dependency and the
removable-path invariant holds. A console selector in the initramfs (kexec, as ZFSBootMenu
does) is a Phase 4 option.

### 10.4 Kernel module signing

The kernel builds with `CONFIG_MODULE_SIG_FORCE` and a per-build ephemeral key that is
destroyed at the end of the job. Only modules built in that same job load, and **that includes
zfs.ko**, so the kernel and OpenZFS recipes become one build unit. The scaffold's
one-recipe-one-container model needs a "build group" for this (§5.6).

### 10.5 ZFS native encryption and TPM2 unseal

**Layout:** one encryption root (`zriver/enc`: `encryption=aes-256-gcm`, `keyformat=raw`).
ROOT/BEs, home, state and containers all inherit from it, so every file on the pool is
encrypted. **Honest limit:** ZFS native encryption does not hide dataset names, sizes,
snapshot names or properties. It encrypts file contents and file metadata, not the pool's
structure.

**The key:** 32 random bytes, generated at install. It is:

- **sealed to the TPM2** under a policy of PCR 7, which captures the Secure Boot state and
  which db certificates are enrolled. Kernel updates therefore do not break unsealing, but
  turning Secure Boot off or changing keys does. An optional PIN can be added (`PolicyAuthValue`);
- **escrowed as a recovery passphrase.** ZFS has one key per encryption root, unlike LUKS
  keyslots, so the recovery path is the same raw key, shown once at enrollment as a
  base32-encoded passphrase.

**The initramfs** runs `river-unseal`, which reads the sealed blob from the ESP, unseals it via
`/dev/tpmrm0` and pipes the key into `zfs load-key`. If unsealing fails, it prompts for the
recovery passphrase on the console.

**The unseal tool:**

- Preferred: **google/go-tpm** (Apache-2.0, pure Go, static, talks to the TPM resource manager
  directly). This removes tpm2-tss and OpenSSL from the initramfs.
- Alternative: **tpm2-tss ESYS** (BSD-2) built `--disable-fapi`, which removes its libcurl and
  json-c dependencies, plus a small C tool.
- Neither needs curl, and tpm2-tools is not required.

### 10.6 File modes ("shadow-moded")

This plan reads "shadow-moded" as "every file carries the tightest mode its job allows, the way
`/etc/shadow` does". See open question 7.

**Enforced at build time today:** the scaffold refuses packages with setuid or setgid files
(unless a recipe lists them in `setuid_allow`) and world-writable paths other than sticky
directories.

**Planned in `river-compose`:**

- an image-wide audit with the same rules;
- `/etc/runink`, the enrollment secrets, the TPM blob and the SSH host keys at 0700/0600;
- `umask 077` for services and users;
- the ESP mounted with `fmask=0177,dmask=0077`;
- `/proc` mounted `hidepid=invisible` with a `proc` group exemption for k0s and the monitoring
  paths (needs validation against kubelet);
- `/home/*` at 0700;
- no setuid binaries in the server image at all. `doas` is the one exception to decide on:
  it needs setuid, and the alternative is key-only SSH as root with no escalation tool.

## 11. Licensing

What we distribute is an aggregate. Each component keeps its own license, and "mere
aggregation" does not spread GPL terms across components.

| Class | Examples | License | Obligations and risk |
|---|---|---|---|
| **Runink River's own code** | installer, `base/` tooling, recipes, s6 service tree, river-update, river-unseal, `runtime/`, docs | **MIT** (decided; root `LICENSE`) with SPDX headers + DCO. See [LICENSING.md](LICENSING.md). | OSI-approved, accepted by Linux Foundation projects. |
| Kernel + zen fork patches | `linux-runink` (`build/pkgbuilds/runink-kernel/**`) | GPL-2.0-only | Publish the exact source + patches + config. The source mirror covers this. |
| **OpenZFS** | `runink-zfs(-utils)` | CDDL-1.0, a separate out-of-tree prebuilt module package (`conflicts=zfs-dkms`) | **The known risk.** Distributing a prebuilt zfs.ko alongside a GPL kernel is the practice Ubuntu follows and the SFC disputes. A Linux Foundation project will ask. The alternative (building on the node with DKMS) breaks the no-compiler invariant. See [governance/ZFS-LICENSING.md](governance/ZFS-LICENSING.md) and open question 2. |
| Firmware | linux-firmware subset | Per-file redistributable, often non-free (WHENCE) | Ship WHENCE and license texts. A Linux Foundation project may want firmware in a separate, clearly labelled package set. |
| skarnet, dhcpcd, opendoas, go-tpm, tpm2-tss, OpenSSH | init, network, security | ISC, BSD-2, ISC, Apache-2.0, BSD-2, BSD-style | Notices only |
| glibc, gcc-libs, util-linux libs, elogind (workstation) | runtime libraries | LGPL-2.1+, GPL-3.0 with the runtime-library exception, mixed | Relinking is possible (dynamic), so this is fine |
| GNU userland | bash, coreutils, grep, sed, gawk, tar, findutils, diffutils, readline, gzip, rsync | **GPL-3.0-or-later** | Removing artools removes the GPL-3.0 *fork we patch* (`patch-artools.go` edits artools files in place, which makes a derivative). **It does not remove GPL-3.0 from the image**, because the GNU userland is GPL-3.0. GPLv3's "installation information" clause applies to *User Products* (consumer devices). A server appliance is generally not one, but Secure Boot with owner-only keys on a device sold to consumers would trigger it. A toybox (0BSD) + mksh userland could replace most GNU tools later, at the cost of script compatibility. Not recommended for Phase 2. |
| Linux-kernel-adjacent GPL-2.0 | iproute2, nftables, kmod, iptables, eudev | GPL-2.0 | Source offer |
| Build-only tools | xorriso, sbsigntools, squashfs-tools, gcc, binutils | GPL-3.0 / GPL-2.0 | Not distributed in the image. No obligation beyond their own use. |
| k0s | runink-k0s | Apache-2.0 (bundles containerd and runc, Apache-2.0) | Notices |
| Data | tzdata, ca-certificates, fonts | public domain, MPL-2.0, OFL / various | Notices |
| Workstation stack | Qt 6, KF6, Plasma, Mesa | LGPL-3.0 / GPL-2.0+ / GPL-3.0 / MIT | Qt's LGPL-3.0 also carries the installation-information clause for User Products |

Every package's SBOM records its declared license. `river-compose` will fail on a missing
license and emit a notices bundle (`/usr/share/licenses/`) for the image.

## 12. Migration phases, exit criteria and estimates

Estimates are for one experienced engineer with agent assistance. Ranges are honest: the low
end assumes upstreams cooperate, the high end assumes the usual surprises.

| Phase | Scope | Exit criteria | Effort | Risk |
|---|---|---|---|---|
| **0: plan + scaffold** (this PR) | `docs/OWN-BASE.md`; `base/` with the recipe format, `sources.lock`, `seed.lock`, `river-build`, `river-sign`, and s6/skalibs/execline recipes | The plan is reviewed. The 3 recipes build **reproducibly** in the pinned container and emit packages, SBOMs and SHA256SUMS; river-sign signs and verifies. *Met on 2026-09-23, except review.* | done | low |
| **1: toolchain** | Stage 1a/1b/2/3 (§3.1); `river-builder` image; source mirror; rebuilder job | A stage-3 toolchain is bit-identical to stage 2. `seed.lock` points at our own image. Two hosts produce identical toolchain packages. | **4 to 8 weeks** | medium: well-trodden (LFS), but gcc bootstrap reproducibility takes patience |
| **2: server base** | About 65 runtime recipes; the kernel (zen 7.2.x) + zfs build group; own s6-rc tree; dhcpcd networking; eudev; UKI + own-key Secure Boot; encryption + TPM2 unseal in the installer; `river-compose` (rootfs, sfs, ISO); `river-update` | The own-base ISO boots in qemu (OVMF + swtpm) **and** on the 155H box. It installs to an encrypted pool, unseals by TPM, and brings up k0s single-node. `tests/assert-golden.sh` passes. The closure lint runs **in CI** against the local repo. Two independent image builds are bit-identical. A real IPv6 network is validated (SLIRP cannot do it). | **3 to 5 months** | **high**: the boot chain, encryption, udev and network replacement all fail in ways that only show on hardware |
| **3: fleet migration + artools removal** | Signed `river` repo and release flow live; `river-update` migrates existing nodes (§6.4); CI builds unsigned artifacts and the owner signs | Every server node runs the own base, has rolled back at least once in a drill, and has an encrypted pool. `patch-artools.go`, the Artix `builder/`, `mirrorlist.pin` and the artools paths are **deleted**. AGENTS.md's "Artix" wording is removed. | **1 to 2 months** | **medium-high**: nodes installed before 2026-09-07 keep their state inside the BE and need space or a reinstall; a remote node nobody can reach physically must never be half-migrated |
| **4: minimize + harden** | Own initramfs generator (drop mkinitcpio); mdevd evaluation; drop host iptables if possible; Go OpenPGP verifier (drop gnupg's gpgv); CVE/VEX feed against the aggregated SBOM; optional live-bootstrap seed; optional k0s from source | Measurable closure reduction from the Phase 2 baseline; a CVE report per release | **2 to 3 months**, then ongoing | low-medium |
| **5: workstation on the own base** | §9 | A Plasma 6 Wayland session at parity with today's workstation profile, built entirely from recipes | **6 to 12 months**, then about 0.5 FTE | **high** |

**Ongoing, from Phase 2:** security updates for about 110 server recipes (CVE watch, bumps,
rebuilds, owner signing). That is **about 0.25 to 0.5 FTE**, and more once Phase 5 lands. This
is the real cost of the decision. Artix does this work for us today.

**Until Phase 3 exits,** the Artix tooling remains the only builder that ships. Fixes to
installed nodes continue through it, and the two build paths share `installer/` and the
root-overlays so that work is not duplicated.

## 13. Open questions for the owner

1. ~~**The license for Runink River's own code.**~~ **Resolved 2026-09-24:** MIT for userspace and
   docs, GPL-2.0-only for the kernel tree, CDDL-1.0 for the OpenZFS package
   ([LICENSING.md](LICENSING.md)).
2. **OpenZFS under a Linux Foundation project.** Is shipping a prebuilt CDDL zfs.ko acceptable,
   or does the Linux Foundation path need a different answer (a separately-hosted ZFS package
   set, or on-node DKMS, which breaks the no-compiler rule)?
3. **Secure Boot db contents.** Keep the Microsoft UEFI CA for option ROMs (safer on hardware
   with a discrete GPU), or owner keys only (stricter, can brick some boards)?
4. **Recovery key custody.** Where does each node's recovery passphrase go? Printed at the
   console once, stored by enrollment in the owner's vault, or both?
5. **Existing nodes.** Is a reinstall acceptable for nodes that lack free space for the
   encrypt-by-send/receive migration, and which nodes are physically unreachable (and so must
   never be at risk of a half-done migration)?
6. **`sudo` → `doas` on the server.** Downstream payload workflows and runner jobs may call `sudo`. An audit
   is needed before the swap, or the server keeps `sudo` built without PAM.
7. **What "shadow-moded" means.** Is it the reading in §10.6 (tightest mode per file, no
   setuid, `hidepid`), or something stricter, such as per-file encryption beyond ZFS or
   integrity measurement (IMA/EVM)?
8. **The workstation.** Is it definitely Runink River-built (Phase 5, 6 to 12 months), or is "any
   distro + equivalent developer guardrails" acceptable long-term?
9. **The Actions runner on the node.** It pins glibc, pulls libstdc++ and self-updates from
   GitHub, which is itself a runtime fetch. Is it staying on the server long-term? If it moves
   to a pod or off-node, the libc and closure calculus changes.
10. **k0s from source.** Is a pinned, sha256-verified upstream k0s release acceptable in the
    trust story, or must k0s (and its bundled containerd/runc) be Runink River-built too? That would
    add weeks and a Go-module vendoring pipeline.
