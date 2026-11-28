<!--
SPDX-FileCopyrightText: 2026 The RIVER Authors
SPDX-License-Identifier: MIT
-->

# Moving off Artix: Arch Linux packages plus our own s6 layer

Status: **IN PROGRESS** (decided 2026-11-28; first measured 2026-09-25, Appendix A).
Runink River stops using Artix. The userland comes from **official Arch Linux packages**,
pinned to a dated snapshot of the Arch Linux Archive, and everything Artix adds on top of Arch
(the s6 init and service packages, the live-medium hooks, the ISO builder) is replaced by
packages and code of our own, built from pinned sources into `[runink]`. Init stays **s6,
never systemd** ([AGENTS.md](../AGENTS.md) invariant 1).

**Until the Arch-based ISO boots, the shipping ISO stays Artix-built.** The two build paths
exist side by side; nothing on the Artix path is removed until the Arch path has passed the
QEMU harnesses and a hardware install (§5, step D4). No PR in this sequence adds an Artix
package or dependency.

The long-term direction in [OWN-BASE.md](OWN-BASE.md) (a base built entirely from source) is
unchanged. Everything this migration builds — the skarnet recipes, our s6-rc service tree,
the initramfs hooks, the ISO builder — is work the own base needs too, so none of it is
throwaway.

## 1. Why now, and what changes

The 2026-09-25 evaluation (Appendix A) found the Arch path feasible and deferred it because it
trades one outside distribution for another. That trade is now accepted: Arch is the larger,
better-staffed upstream, its archive gives exact dated snapshots (Artix has none), and it
removes a layer — Artix rebuilds Arch with systemd stripped, and we already rebuild or replace
most of what that layer adds. The cost is a recurring coupling check on every snapshot bump
(§4), which Artix did for us.

## 2. Ground rules for every PR in the sequence

- **s6 only.** No `systemd` package, no `.service` unit, no `systemctl`, on the image or the
  live medium.
- **No Artix binaries, no Artix packages, no Artix repositories** in anything new. The Artix
  path keeps working, untouched, until D4 deletes it.
- **Pinned.** Arch through `https://archive.archlinux.org/repos/YYYY/MM/DD/` (one date for
  `[core]` and `[extra]`), container bases by digest, sources by sha256 and signature where
  upstream signs (invariant 9).
- **Allow-list.** `Packages-Root` stays curated; every Arch name that replaces an Artix one is
  checked against the pinned snapshot's database, not against memory.
- **Go, not shell** for new logic ([GO-CLI.md](GO-CLI.md)): the ISO builder and any helper
  are `river` subcommands. `river lint shell-ratchet` still forbids new shell scripts.
- **Honest test claims.** CI on the project's runners runs Tier 1, the Go tests and the
  container builds. It does not boot an ISO. No PR claims an image boots until the QEMU
  harnesses (`build/qemu-test.sh`, `build/qemu-gui-test.sh`) have passed against an image
  built from that tree, and a hardware install has been done.

## 3. Every Artix dependency and its replacement

`grep -rIil artix .` lists 98 tracked files. They reduce to these dependencies:

| # | Artix dependency today | Where | Replacement | PR |
|---|---|---|---|---|
| 1 | Repositories `[system]`, `[world]`, `[galaxy]`; Artix mirrors (no dated snapshots) | `pacman/pacman.conf.in`, `pacman/mirrorlist.pin`, `build/kernel-builder.Containerfile` | Arch `[core]` + `[extra]` from `archive.archlinux.org` at one `PIN_DATE`; `[runink]` unchanged | C2 |
| 2 | `artix-keyring` | `builder/Containerfile`, `35-pacman-keyring` | `archlinux-keyring` (the step already populates `archlinux` when present); `[runink]` and `[blackarch]` keyrings ship in the profile | C2, B |
| 3 | Artix `base` meta-package | `Packages-Root` | Arch `base` hard-depends on `systemd`, so its members are listed explicitly (Appendix A.3) | C2 |
| 4 | Builder images `artixlinux/artixlinux:latest` (unpinned) | `builder/Containerfile`, `build/kernel-builder.Containerfile`, `.github/workflows/kernel-build.yml`, `zfs-build.yml` | `docker.io/library/archlinux:base-devel` pinned by digest | C2 (builder), C5 (kernel/ZFS) |
| 5 | skarnet stack as Artix binaries: `s6`, `s6-rc`, `s6-linux-init`, and the `s6` command (s6-frontend) | `Packages-Root`, `80-enable-s6` | Our recipes in `base/recipes` (skalibs, execline, s6, s6-rc, s6-linux-init, s6-portable-utils, s6-frontend), one coherent upstream release set, built into `[runink]` | C1 |
| 6 | `s6-scripts` / `s6-base`: Artix's PID 1 glue (`rc.init`, `rc.shutdown`, the `/etc/s6/adminsv` tree: mounts, udevd, ttys, hostname, sysctl, modules, `rc-local`) | `Packages-Root`, `root-overlay/etc/s6/adminsv` | `runink-s6-base`: our own s6-linux-init skeleton and s6-rc base tree, same store paths (`/etc/s6/sv`, `/etc/s6/adminsv`, `/etc/s6/repo`, boot database `/etc/s6/rc/compiled`) so `80-enable-s6` keeps its commands | C3 |
| 7 | `*-s6` service packages: `elogind-s6`, `dbus-s6`, `networkmanager-s6`, `openssh-s6`, `chrony-s6`, `bluez-s6`, `cups-s6` | `Packages-Root`, `profile.yaml` `packages-init` | `runink-s6-services`: our service definitions for the same bundle names (`NetworkManager`, `sshd`, `chrony`, `bluetoothd`, `cupsd`, `dbus`, `elogind`); `runink-greetd-s6` (already ours) loses its Artix dependencies | C3 |
| 8 | udev built by Artix from the systemd tarball | implicit in `base` | `runink-udev`: `systemd-udevd`, `udevadm`, rules and hwdb built from the systemd source of the **same version** as the snapshot's `systemd-libs` (policy §4) | C4 |
| 9 | `esysusers`, `etmpfiles` (standalone sysusers/tmpfiles) | implicit in `base` | `runink-sysusers-tmpfiles`: systemd's standalone `systemd-sysusers`/`systemd-tmpfiles` binaries from the same source, plus our alpm hooks (Arch's hooks live in the `systemd` package) | C4 |
| 10 | `elogind`, `libelogind` (not in Arch) | `Packages-Root`, `builder/Containerfile`, `runink-wlroots0.18` | `runink-elogind` from the pinned upstream elogind release; `polkit` rebuilt against it | C4 |
| 11 | Arch packages with a hard `systemd` dependency that this image installs: `mkinitcpio`, `pacman`, `polkit`, `cups`, `xdg-desktop-portal` (re-measured on the DEsk list in C4) | `Packages-Root` | rebuilt in `[runink]` without the `systemd` dependency (Appendix A.4, A.5) | C4 |
| 12 | `artools-iso` / `buildiso`, `scripts/patch-artools.go`, `scripts/build-iso.sh`, `scripts/build-iso-box.sh`, `scripts/fetch-iso-profiles.sh`, artools' `common.yaml` | `build/local-iso.sh`, `build/iso-root-stage.sh`, `Makefile` | `river iso build`: our own builder, mkarchiso-style (pacstrap into a rootfs, the same two squashfs layers `rootfs.img` + `livefs.img`, GRUB for EFI and BIOS from `iso-profiles/river/grub`, xorriso), reading `profile.yaml` | D2 |
| 13 | `runink-grub-live` (exists only to feed buildiso's `prepare_grub()`) | `Packages-Live` | drops out: the builder takes the menu from the profile | D2 |
| 14 | `artix_*` initcpio hooks (`artix`, `artix_shutdown`, `artix_loop_mnt`, `artix_kms`) and `/run/artix/bootmnt` | live initramfs, `patch-artools.go`, `72/73/74` installer steps, `installer/internal/wizard`, `installer/pairannounce` | `runink-live-initcpio`: our hooks (`river_live`, `river_loop_mnt`, `river_shutdown`; upstream `kms`), medium mounted at `/run/river/bootmnt`; the installer's medium search lists the new path first | D1 |
| 15 | `artools-base` (live: `basestrap`, `artix-chroot`, `fstabgen`) | `Packages-Root`, `profile.yaml`, installer comments | `arch-install-scripts` (`pacstrap`, `arch-chroot`, `genfstab`) | D3 |
| 16 | `artix-live-*` names in cleanup lists (`20-clone-rootfs`, `80-enable-s6`) | installer | removed once no Artix medium is supported (installs clone the live system they booted from) | D4 |
| 17 | Branding and comments naming Artix (`branding/grub/river/theme.txt`, `grub/grub.cfg`, `palette.md`, Packages-* comments) | profile, branding | reworded; no functional change | D4 |
| 18 | Provenance text pointing at `gitea.artixlinux.org` | `85-cloud-finalize` | points at the Arch package sources (`gitlab.archlinux.org/archlinux/packaging/packages/<pkgbase>`) and our recipes | D3 |
| 19 | Lints and Go code that know artools (`cli/internal/lint/closure.go`, `profilemanifest.go`, `repocmd/installtest.go`, `testcmd/modelsrequired.go`) | `cli/` | taught the Arch path in C2/D2, artools branches deleted in D4 | C2, D2, D4 |
| 20 | Docs and website pages naming Artix (`book/`, `website/`, `docs/`, `README.md`, `AGENTS.md`) | docs | rewritten when the default flips | D4 |

Not affected: the kernel (`linux-runink`) and OpenZFS (`runink-zfs`, `runink-zfs-utils`) are
built from upstream sources in `[runink]`; only their build container changes (C5).
`runink-zfs-utils` declares `depends=libudev`, an Artix package name; it becomes the
`libudev.so` soname, which both bases provide.

## 4. The systemd-dependency policy (in force from C2)

- **Forbidden in any closure:** `systemd`, `systemd-sysvcompat`, `dbus-units`,
  `dbus-broker-units`, and any package that provides a systemd daemon. `forbidden.closure`
  lists them, and `river lint closure` runs against the pinned Arch databases.
- **Allowed:** `systemd-libs` (`libsystemd.so`, `libudev.so`) as a library. No systemd daemon
  runs; rebuilding the eight or more base packages that link it buys nothing at run time.
- **Allowed, built by us from the systemd source tarball:** the udev daemon and the standalone
  sysusers/tmpfiles tools (§3 rows 8–9), at exactly the version of the snapshot's
  `systemd-libs`. This is what Artix does today. mkinitcpio's `udev` hook already runs
  `systemd-udevd` with s6 as PID 1.
- **Every snapshot bump** repeats the Appendix A.2 scan. A new hard `systemd` dependent is
  either rebuilt in `[runink]` or dropped from the image, in the bump PR itself.

## 5. Pull request sequence

Each PR is reviewable and mergeable on its own and leaves the Artix build working.

| Step | PR | What it changes | Can CI show it works? |
|---|---|---|---|
| A | this plan (#103) | this document | n/a |
| B | `[blackarch]` repository | the BlackArch repository and its keyring configured on installed machines, **no BlackArch package installed** (`docs/BLACKARCH.md`) | Tier 1 and Go tests: the keyring is the pinned one, the stanza is written, no package list names a BlackArch package. Not an install. |
| C1 | skarnet stack | one coherent skarnet release set in `base/`: skalibs, execline, s6 bumped; s6-rc, s6-linux-init, s6-portable-utils, s6-frontend added | `river-build lint`. Building needs the seed container (a CI job is part of this step). |
| C2 | Arch package configuration (#104, draft: package source and builder images) | `pacman/arch/` (pacman.conf, the archive pin), `builder/Containerfile.arch` on a digest-pinned `archlinux:base-devel`, `Packages-Root` names for Arch behind a base switch, the systemd policy in `forbidden.closure`, `35-pacman-keyring` populating `archlinux` | Container build and the closure lint against the pinned Arch databases. |
| C3 | our s6 layer | `runink-s6-base` (PID 1 glue, base tree) and `runink-s6-services` (rows 6–7) | Package builds; a container check that `s6-rc-compile` accepts the full tree. Boot: no. |
| C4 | systemd-coupled rebuilds | `runink-udev`, `runink-sysusers-tmpfiles`, `runink-elogind`, and the rebuilt `mkinitcpio`, `pacman`, `polkit`, `cups`, `xdg-desktop-portal` | Package builds and the closure lint (no forbidden name). |
| C5 | kernel and ZFS in the Arch container | `kernel-build.yml`, `zfs-build.yml`, `build/kernel-builder.Containerfile` | Yes: those workflows build the packages. |
| D1 | live initramfs | `runink-live-initcpio` (row 14) | Package build. Boot: no. |
| D2 | the ISO builder | `river iso build` replaces buildiso, `patch-artools.go`, `runink-grub-live`; `RIVER_BASE=arch` in `build/local-iso.sh` | It can build an ISO in CI (`images.yml` dispatch with the new base). It cannot boot it. |
| D3 | installer on Arch | pacstrap/arch-chroot names, `/run/river/bootmnt`, `80-enable-s6` against our tree, provenance text | Go tests and Tier 1. |
| D4 | flip and delete | QEMU harnesses green on the Arch ISO, a hardware install, then the default becomes Arch and the Artix path, its lint branches and its docs are deleted | Only after manual boot evidence is attached to the PR. |

The live medium keeps everything it has today on the Arch path: the River GRUB entries and
theme, the volume label `RIVER` and `RIVER_EFI`, the live overlay, the graphical-session →
kiosk → text-installer fallback chain, the try-or-install session, and the text installer.
D2 and D4 each list these in their PR as checks.

### 5.1 Work streams, file ownership and dependency order

The steps above run as five parallel streams with disjoint files, so their PRs do not
conflict. A file not listed is unowned: whoever needs it says so in their PR first.

| Stream | Steps | Owns | Depends on |
|---|---|---|---|
| **Base** (package source) | A, C2 (package source part), C5 (builder images) | `docs/ARCH-BASE.md`; `pacman/pacman.conf.in` (except the `[blackarch]` block), `pacman/mirrorlist.pin` and the Arch archive pin; `builder/` (the Arch builder image); `build/kernel-builder.Containerfile` | nothing |
| **BlackArch** | B | the `[blackarch]` block at the marked spot at the **end** of `pacman/pacman.conf.in`; `pacman/blackarch-*`; its docs | nothing (the block is last, so `[core]`/`[extra]` win any name clash) |
| **s6 layer** | C1, C3, the elogind/udev/sysusers parts of C4 | `base/`; `build/pkgbuilds/*-s6`, `runink-greetd-s6`, seatd, elogind, `dbus`; the package lists in `build/build-all.sh` | Base (the Arch builder image and pin) for package builds; C1 recipes need nothing |
| **ISO builder** | D1, D2 | the new ISO builder replacing artools/buildiso; our initcpio hooks; `iso-profiles/river/profile.yaml`, `iso-profiles/river/grub/`; `build/local-iso.sh` | Base and s6 layer (it installs their packages); it can be written against them before they land |
| **Cleanup** | D4 (wording and CI part) | `.github/workflows/`; Artix wording in `README.md`, `book/`, `website/`, `docs/` (not this file), `NOTICE`, `branding/`; Artix names in `iso-profiles/river/Packages-*` | each rename lands only once its replacement exists, or it breaks the shipping build |

Order: **Base → s6 layer → ISO builder → boot evidence → Cleanup**, with BlackArch independent.
Base lands first because everything else resolves packages against its pin. No stream removes
anything the Artix path still reads until D4.

Not yet owned, and needed before D4: the installer steps (`35-pacman-keyring`,
`80-enable-s6`, `/run/artix` in `installer/`, the Go medium search) and their profile copies;
the rebuilt hard `systemd` dependents (`mkinitcpio`, `pacman`, `polkit`, `cups`,
`xdg-desktop-portal`); the lints in `cli/internal/lint/` and `cli/internal/repocmd/installtest.go`;
`forbidden.closure`; `scripts/build-iso*.sh` and `scripts/patch-artools.go` (deleted at D4).

Two constraints every stream inherits:

- **No new shell scripts.** `river lint shell-ratchet` fails on any new one
  ([GO-CLI.md](GO-CLI.md)). The ISO builder and the initramfs glue that is not a mkinitcpio
  hook (hooks are shell by mkinitcpio's design) are `river` subcommands in Go.
- **The shipping path stays intact.** `build/local-iso.sh` → `build/iso-root-stage.sh` →
  `scripts/build-iso-box.sh` reads `builder/Containerfile` (Artix, with artools) and
  `ARTIX_MIRRORS` from `pacman/mirrorlist.pin`. Those stay until D4; the Arch path uses new
  files beside them.

**For the BlackArch stream, checked 2026-11-28** (re-check before pinning): keyring tag
`20251011` of `github.com/BlackArch/blackarch-keyring`; its `blackarch.gpg`,
`blackarch-trusted` and `blackarch-revoked` are byte-identical in that tag, in the release
tarball on `blackarch.org/keyring/` (detached signature valid, primary key
`CBA3C7D4798912702DCF568E67D8BDF42AD93F4E`) and in the repository's signed
`blackarch-keyring-20251011-2` package. Trusted primary keys:
`8F9A9793CB8591147C2EC70566E0CDBD1E01F333`, `A0917C4147A37007CB54C1CFD295AA940EFDDF62`,
`4345771566D76038C7FEB43863EC0ADBEA87E4E3`, `F9A6E68A711354D84A9B91637533BAFE69A25079` (the same
four as `KEYRING_SIGNERS` in BlackArch's `strap.sh`); revoked:
`5E210889BBB5C48500E0C4F9C75E985FF8B993B4`. The live `blackarch.db` was signed by
`F9A6E68A711354D84A9B91637533BAFE69A25079` on 2026-11-27. The keyring package ships
`/etc/pacman.d/hooks/blackarch-key.hook` and a `pacman-key --populate blackarch` scriptlet, so
an image that ships the keyring as plain files must tell users to install the package with
`--overwrite '/usr/share/pacman/keyrings/blackarch*'`, as `strap.sh` does.

## 6. Estimate

Server-equivalent work was measured at 2 to 4 weeks (Appendix A, table A.6). The image is now a
workstation (DEsk on Wayland), which adds elogind, the polkit/cups/portal rebuilds and their
s6 services: about 1 to 2 weeks more. **Total: about 3 to 6 weeks** of focused work to a
boot-tested Arch ISO, then the per-bump coupling check (§4) for as long as Arch is the base.

## 7. Risks

- **Arch is systemd-first by design.** Coupling can appear on any snapshot bump (pacman
  gained its `systemd` dependency in 7.0); §4 makes the check part of every bump.
- **The live medium.** Booting s6-linux-init as PID 1 from our own live hooks is unproven
  until D4. archiso's own airootfs conventions assume systemd; we take only its approach
  (squashfs layers, xorriso layout), not its hooks.
- **udev from the systemd source.** `runink-udev` must track the exact `systemd-libs`
  version; a skew breaks `libudev` consumers (ZFS by-id links, the `zfs` hook). C4 adds a
  check that the two versions are equal.
- **elogind is not an Arch package.** We carry it and its polkit pairing ourselves; seat and
  session behaviour on real hardware shows only at D4.
- **Two build paths for a while.** Every profile change during the migration must work on
  both; C2 adds the base switch to the profile lint so a change that breaks one fails Tier 1.

## Appendix A: the 2026-09-25 measurement

Kept as measured. Package counts are from the Arch Linux Archive snapshot of 2026-09-24 and the
server profile of that date; C2 and C4 re-measure against the new pin and the DEsk list. The
original question was whether to move the userland from Artix to upstream Arch packages plus
`[runink]`, with s6 kept; the effort table that follows A.5 is the one §6 builds on.

### A.2 Method

- Pinned snapshot: Arch Linux Archive `2026/09/24` (`core`: 299 packages, `extra`: 14 983),
  resolved in rootless podman from `docker.io/library/archlinux@sha256:f3691b4dde62ba4c4b6f0ae2c1fbf28e8c0c8c4b9a35c7e06dc1f70e21aa29f6`.
- Resolver: `pacman --dbpath <empty> -Sp --print-format %n <list>`. The empty local database
  matters: against the image's own database pacman reports only what is missing, which
  undercounts the closure by a factor of three.
- systemd-free closure: the same resolve with `--assume-installed systemd=261.3`, then a
  scan of every `Depends On` in the closure for `systemd`, `systemd-sysvcompat`,
  `dbus-units` and `udev` (hard coupling) and for `systemd-libs`, `libsystemd.so`,
  `libudev.so` (library coupling).
- The server list is `iso-profiles/river/Packages-Root` with Artix names mapped to Arch
  (A.3), plus the declared dependencies of the `[runink]` packages. The live extras are the
  install tooling from `profile.yaml`.

### A.3 Package mapping (server)

| Artix (today) | Arch equivalent | Note |
|---|---|---|
| `base` | **not usable**: Arch `base` 3-3 hard-depends on `systemd` and `systemd-sysvcompat` | Replaced by its members listed explicitly: filesystem gcc-libs glibc bash coreutils file findutils gawk grep procps-ng sed tar gettext pciutils psmisc shadow util-linux bzip2 gzip xz licenses iputils iproute2 pacman archlinux-keyring |
| `mkinitcpio` | `mkinitcpio` 42-1 | **hard-depends on `systemd`**, so it needs a rebuild |
| `grub`, `efibootmgr`, `linux-firmware-intel` | same names | fine |
| `s6`, `s6-rc`, `s6-linux-init` | **not in Arch** | build: `base/recipes` has s6, skalibs and execline; s6-rc, s6-linux-init and s6-portable-utils are new recipes |
| `s6-scripts`, `*-s6` (elogind, dbus, networkmanager, openssh) | **none** | write our own s6-rc tree (udevd, mounts, ttys, hostname, sysctl, modules, zfs import/mount, sshd, network, rc-local, river-perms, river-guide-model) |
| `elogind` | **not in Arch** (AUR only) | dropped on the server anyway ([OWN-BASE.md](OWN-BASE.md) §8.2) |
| udev (Artix builds it from the systemd tarball) | **only inside the `systemd` package** | repackage `systemd-udevd`, `udevadm`, `libsystemd-shared`, rules and hwdb from the pinned `systemd` package, or build eudev (not in Arch either) |
| esysusers / etmpfiles | **only inside `systemd`** | Arch packages ship `sysusers.d`/`tmpfiles.d` and rely on systemd's alpm hooks; we would need a standalone sysusers/tmpfiles package plus the hooks |
| `networkmanager`, `wpa_supplicant`, `dbus` | available (library coupling only) | the server plan already replaces them with `dhcpcd` 10.5.2 (in Arch; `systemd-libs` only) |
| `openssh` 10.5p1, `nftables`, `iptables` 1.8.13, `rsync`, `sudo`, `vim`, `cpupower` 7.2.7, `bubblewrap` | same names | fine |
| `runink-tayga` | ours (`tayga` is not in Arch) | unchanged |
| `artools-base` (live) | `arch-install-scripts`, `mkinitcpio-archiso` 73-1, `archiso` 90 (build host) | fine; mkarchiso replaces buildiso |
| `runink-grub-live` (live; ours since 2026-10-01, replacing `artix-grub-live`) | n/a — it exists only to feed `buildiso`'s `prepare_grub()` | **drops out entirely** under mkarchiso, which takes the live menu straight from the profile |
| `runink-zfs-utils` | ours | declares `depends=libudev` (an Artix name); Arch provides `libudev.so` from `systemd-libs`, so the PKGBUILD dependency must change or a shim is needed |

### A.4 Measurements

| | Packages |
|---|---|
| Server closure, Arch packages, naive (systemd allowed in) | **156** |
| Server closure, Arch packages, systemd assumed away | **150** |
| Pulled only by `systemd` | 6: systemd, dbus-units, dbus-broker, dbus-broker-units, cryptsetup, kbd |
| Live medium extras | +6: arch-install-scripts, dialog, dosfstools, gptfdisk, mkinitcpio-archiso, parted |
| `[runink]` on top (kernel, headers, ZFS ×2, k0s, core, runtime, installer, tayga, river-guide) | +10 |
| New packages we would own (s6 stack ×6, s6-rc service tree, udev repack, sysusers/tmpfiles repack, init glue) | about +10 |
| **Server image total** | **about 170**, against about 206 in today's Artix closure |

**Hard `systemd` dependents in the server and live closure: 2 real packages**, plus the
meta-package:

| Package | Why | Fix |
|---|---|---|
| `base` | meta-package | list its members explicitly |
| `mkinitcpio` 42-1 | the `systemd` hook and udev binaries | rebuild without the dependency (shell scripts; minutes) |
| `pacman` 7.1.0 | the `alpm` download-sandbox user comes from `systemd-sysusers` | rebuild with the dependency pointed at our sysusers package, or drop pacman from the server as OWN-BASE §4 plans |

**Library-only coupling (`systemd-libs` / `libudev.so`), fine as-is under the policy below:**
dbus (pulled by libpcap via iptables), device-mapper, dhcpcd, libusb, pam, pciutils,
procps-ng, util-linux.

Also pulled in, and worth knowing: `pacman` brings `curl`, `gnupg`, `gpgme`; `gnupg` brings
`tpm2-tss` and `libusb`; `pinentry`/`libsecret` bring `glib2`. None run as services, but they
are closure the own base drops (OWN-BASE §4).

#### Policy as written in 2026-09 (now in force, see §4)

- **Forbidden in the closure:** `systemd`, `systemd-sysvcompat`, `dbus-units`,
  `dbus-broker-units`, and any package providing a systemd daemon.
- **Allowed:** `systemd-libs` (`libsystemd.so`, `libudev.so`) as a library, because no systemd
  daemon runs and the alternative is rebuilding eight base packages for no runtime gain.
- **Allowed with a note:** the udev daemon and the standalone sysusers/tmpfiles tools,
  repackaged byte-for-byte from the pinned `systemd` package. This is what Artix does today
  (its udev comes from the systemd tarball), and mkinitcpio's own `udev` hook already runs
  `systemd-udevd` without systemd as PID 1.

### A.5 Workstation (Plasma 6)

Measured the same way, from `iso-profiles/runink-workstation/Packages-Root` with `base`
expanded and the `*-s6`/s6 entries removed:

| | Packages |
|---|---|
| Workstation closure, Arch packages | **838** (833 with systemd assumed away) |
| Hard `systemd` dependents | **9**: accountsservice, bolt, cups, mdadm, media-player-info, mkinitcpio, pacman, polkit, xdg-desktop-portal (plus `systemd` itself) |
| Library-only (`systemd-libs`/`libudev.so`) users | 56 |

On top of the server work, a systemd-free Plasma 6 needs: those seven extra rebuilds (polkit is
the important one: KDE's authentication agent, udisks2, NetworkManager and PowerDevil need it);
elogind and a logind-compatible stack, which Arch does not carry at all (elogind,
`polkit`-on-elogind, sddm checked against elogind); s6 services for dbus, elogind,
NetworkManager, bluez, cups and sddm; and PipeWire/WirePlumber started as user services
without systemd user units (s6 per-user supervision or the session's autostart). Plasma's
non-systemd `startplasma` path works today on Artix, but every Plasma release can regress it.

Estimate: **3 to 6 weeks** beyond the server, then continuing work on each Plasma and
snapshot bump. Not measured: whether any of the 56 library users call logind or systemd's
D-Bus APIs at run time and degrade without them.

### A.6 Effort estimate (2026-09-25)

| Work | Estimate |
|---|---|
| s6-rc, s6-linux-init, s6-portable-utils recipes (next to the existing three) | 1 day |
| Own s6-rc service tree and PID 1 glue replacing `s6-scripts` and every `*-s6` package, including the live medium's autologin and tty1 river-guide hand-off | 3 to 5 days |
| mkinitcpio and pacman rebuilds; udev and sysusers/tmpfiles repackaging with alpm hooks | 1 to 2 days |
| archiso profile with s6 as PID 1 in the airootfs (archiso's live environment assumes systemd), River GRUB theme, volume label, model payload and multiboot USB compatibility | 3 to 5 days |
| Installer: basestrap/artix-chroot → pacstrap/arch-chroot, `/run/artix` → `/run/archiso`, `80-enable-s6` against our tree, keyring step, NetworkManager dispatcher scripts ported to dhcpcd hooks | 2 to 3 days |
| Closure lint on the Arch snapshot, Tier 1, QEMU boot test, then hardware | 2 to 4 days |
| **Server total** | **about 2 to 4 weeks** to a boot-tested ISO |
| Workstation on top | see A.5 |
| Ongoing | tracking Arch's rolling snapshot, re-checking systemd coupling on each bump |

