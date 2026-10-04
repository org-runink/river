<!--
SPDX-FileCopyrightText: 2026 The RIVER Authors
SPDX-License-Identifier: MIT
-->

# Runink River licensing

> **Status: DECIDED (owner, 2026-09-24).** The root [`LICENSE`](../LICENSE) is **MIT**.
> Two trees are deliberately *not* MIT: the kernel packaging tree is **GPL-2.0-only**, and
> the OpenZFS package tree carries **CDDL-1.0** upstream code. `REUSE.toml` and `LICENSES/`
> hold the authoritative per-path mapping, checked with `river lint reuse` (§7). Where this file
> and `REUSE.toml` disagree, `REUSE.toml` wins and this file is the bug.
>
> "The RIVER Authors" means the copyright holders recorded in git history. At publication
> that is Runink / Daniel Paes, including AI-assisted commits made by the same person.
> There were no outside contributors, so choosing the license needed no one else's
> consent. The public repository starts from a single squashed commit; the earlier
> private history is kept as an archive by Runink.

## 1. The license map

| What | License | Why |
| --- | --- | --- |
| **All userspace**: Runink River's own scripts, installer, build tooling, configuration, s6 service tree, workflows, the future `runtime/` Go code, **and the documentation** | **MIT** (root `LICENSE`) | Short, OSI-approved, permissive and universally understood. It lets anyone, including commercial vendors, build on Runink River, and it is compatible with shipping GPL-2.0 and CDDL-1.0 components as **separate packages** in the same image. |
| **Kernel tree**: `build/pkgbuilds/runink-kernel/**` (PKGBUILD, config, Runink River's kernel patches) | **GPL-2.0-only** | Patches against kernel files must follow the file they modify. Keeping the whole kernel packaging tree GPL-2.0-only removes any doubt at the boundary. The Arch-derived parts were 0BSD upstream, which permits this. |
| **OpenZFS package tree**: `build/pkgbuilds/runink-zfs/**` | **CDDL-1.0** (upstream OpenZFS code), packaged as a **separate, out-of-tree, prebuilt module package** (`conflicts=zfs-dkms`) | Never merged into the kernel source, never `CONFIG_ZFS=y`. The boundary is explained in [governance/ZFS-LICENSING.md](governance/ZFS-LICENSING.md). The two archzfs files vendored byte-for-byte keep their **MIT** notice (§2). |
| Runink brand artwork (§5) | **`LicenseRef-Runink-Trademark`** (all rights reserved) | A trademark, not a code license. Swappable. |
| CachyOS Emerald wallpapers | **GPL-3.0-only** | Upstream's license; shipped as separate works. |
| Documentation site theme: `website/_vendor/github.com/imfing/hextra/**` (Hextra, vendored unmodified) | **MIT** (upstream) | Its LICENSE stays beside it, and the built site publishes it at `/third-party/hextra-LICENSE.txt` and credits the theme in the footer, because the compiled theme CSS/JS ships with every page. |
| Model validation suite: `validation/**` (a build-host tool; not shipped in any image) | **Apache-2.0**, as its SPDX headers declare | Declared by its author when it was added. It differs from the MIT default; whether to keep it or relicense it to MIT is an open owner decision ([governance/LF-AIDATA.md](governance/LF-AIDATA.md)). |
| Other third-party files | **Keep upstream's license** (§3) | They cannot be relicensed. |

`LICENSES/` holds the text of every license used: MIT, GPL-2.0-only and CDDL-1.0, plus the
Apache-2.0 for `validation/`, and the existing third-party ones (0BSD, BSD-2-Clause,
GPL-2.0-or-later, GPL-3.0-only,
CC-BY-4.0 for the Runink River artwork and the Code of Conduct, `LicenseRef-Public-Key`, `LicenseRef-Runink-Trademark`),
and the `Linux-syscall-note` exception for the kernel uapi header in bpfdoc's test fixtures.

**Why not GPL for the userspace?** No Runink River userspace file is GPL-derived (§2). The one
GPL-3.0 entanglement is a *build-time* tool, `artools`, and the owner has decided to
remove it (§4). The kernel stays GPL-2.0 in its own tree. Nothing forces copyleft on the
rest, and MIT keeps Runink River usable as a base by any vendor, Runink included. That
neutrality is what a foundation wants to see.

**Runink River ships no proprietary components.** Every file in this repository is under an
open license, with one exception: the Runink brand artwork in §5, which is reserved as a
trademark and is swappable. Commercial products, Runink's or anyone else's, may be built
**on** Runink River as a *downstream payload*: an optional, out-of-tree directory passed to the
build with `RIVER_PAYLOAD_DIR` (see `build/config.env`). The payload is not part of this
repository, and its own license travels with it. A base image built without a payload is
complete and fully open.

## 2. Per-path inventory

| Path | Origin | License | Confidence / flag |
| --- | --- | --- | --- |
| `installer/**`, `scripts/**`, `tests/**`, `build/*.sh`, `build/config.env`, `install/**`, `Makefile`, `builder/Containerfile`, `pacman/**` | Runink original | MIT | High. Written in-repo. |
| `iso-profiles/*/root-overlay/**`, `iso-profiles/*/live-overlay/**`, `forbidden.*` | Runink original | MIT | High. Runink's own overlay trees. |
| `iso-profiles/*/profile.yaml`, `profile.conf`, `Packages-Root`, `Packages-Live` | Adapted from **Artix `iso-profiles`** (`gitea.artixlinux.org/artix/iso-profiles`) | **MIT AND BSD-2-Clause** (© 2017 Cromnix GNU/Linux) | Medium. The upstream `LICENSE` is **BSD-2-Clause, not GPL** (checked 2026-09-23). How much upstream text survives is unclear, so the upstream notice is kept to be safe. |
| `scripts/patch-artools.go` | Runink original. It *patches* an installed **artools** (GPL-3.0-or-later) at build time | MIT | Medium. It embeds a few one-line artools strings as match markers (`iso_label="ARTIX_$(date +%Y%m)"`, `k=$(<"$mnt"/usr/src/linux/version)`, ...). These are de minimis, and no artools file is vendored. The *modified artools* runs only on the builder and is never distributed. **It is deleted when artools goes (§4).** |
| `build/pkgbuilds/runink-kernel/**` (PKGBUILD, `config.base`, `config.delta`, `config.require`, patches, `README.md`) | Runink, "based on Arch's linux-lts PKGBUILD (heftig, A. Radke) and AUR linux-xanmod-lts (Joan Figueras)"; `config.base` is `make olddefconfig` output of an **Arch linux-zen** config | **GPL-2.0-only** | Medium. Arch packaging sources have been **0BSD** since Arch RFC 0040 (2024), which allows relicensing. **Flag:** check that the AUR `linux-xanmod-lts` portion is also 0BSD; the AUR's 0BSD default applies only to submissions after the policy change. |
| `build/tools/bpfdoc/**` (and the link `build/pkgbuilds/runink-kernel/bpfdoc.go`) | A Go **port** of the Linux kernel's `scripts/bpf_doc.py` (© Netronome, Isovalent), which the kernel PKGBUILD builds in place of that script so the kernel builds without Python | **GPL-2.0-only**, as the script it ports | High. Only the `--header` mode is ported. Built and run on the build host; the binary is installed into the kernel build tree (and so ships in `linux-runink-headers` as `scripts/bpf_doc.py`, where upstream's Python script would have been). |
| `build/tools/bpfdoc/testdata/*.gz` | Linux 7.2.7 `tools/include/uapi/linux/bpf.h` and the `bpf_helper_defs.h` upstream `bpf_doc.py` generated from it (test fixtures, `testdata/README`) | **GPL-2.0-only WITH Linux-syscall-note**, the header's own licence | High. Verbatim kernel uapi header and its generated derivative; not shipped in any image. |
| `build/pkgbuilds/runink-zfs/PKGBUILD`, `runink-zfs.install` | Runink. The package function "mirrors archzfs's package_zfs-utils" | **CDDL-1.0**, with the archzfs **MIT** notice kept for the mirrored part | Medium. Same archzfs caveat as below. |
| `build/pkgbuilds/runink-zfs/zfs-utils.initcpio.hook`, `.install` | **Vendored byte-for-byte from archzfs** @ `d85933f5c001901511d730420fa78858590b1fb8` (sha256-pinned in the PKGBUILD) | **MIT** (© archzfs contributors) | Medium. The archzfs README states "the license of the Arch Linux package sources is MIT", but the repository has **no LICENSE file and names no copyright holder**. **Flag:** ask archzfs to add a LICENSE, or at least confirm in an issue. **These files must not get an in-file SPDX header**: that would break the byte-for-byte sha256 pin. REUSE.toml carries their license instead. |
| `build/pkgbuilds/runink-*/PKGBUILD` other than the kernel and OpenZFS trees | Runink original | MIT | High. `runink-installer` packages `river-hwprobe`/`river-plan` built from `installer/` (MIT). Two payload-wrapper packages package whatever a downstream payload stages and are empty on a base image (§1); a payload's license travels with the payload. `runink-k0s` builds Apache-2.0 k0s; `runink-tayga` builds GPL-2.0 tayga. `runink-grub-live` ships one file written here (`loopback.cfg`, a single `source` line) plus two empty directories, and copies nothing from the GPL `artix-grub-live` it replaced. |
| `build/pkgbuilds/*/keys/pgp/*.asc` | Public OpenPGP keys of Linus Torvalds, Greg Kroah-Hartman and the OpenZFS release signer | `LicenseRef-Public-Key` | High. Redistributed only for signature verification. No copyright is claimed. See `LICENSES/LicenseRef-Public-Key.txt`. |
| `branding/**` + overlay copies (`usr/share/runink/branding/**`, `usr/share/plymouth/themes/river/**`, `usr/share/wallpapers/River/**`) | Runink original artwork | CC-BY-4.0 for the files (as `REUSE.toml` maps them), trademarks reserved | Medium. Depends on the trademark plan (§5). `branding/render.sh` and `branding/**/*.md` are MIT. |
| `branding/mascot/**`, `branding/logo/river-{mark,mark-small,lockup,lockup-dark}.svg`, `branding/icons/**`, `branding/ascii/**`, `branding/plymouth/river/logo.png`, `branding/splash/**`, `branding/grub/river/background.jpg`, the Runink River wallpaper (`branding/wallpapers/River/**`) + their overlay copies, and the profile's `/etc/issue` | The **Runink River community mark** (the M2 mascot, `branding/mascot/**`: mark, small mark, lockups, ASCII) and everything rendered from it (the Runink River edition's icons, favicons, splash images, GRUB background, wallpaper and terminal banner) | **`LicenseRef-Runink-Trademark`** (all rights reserved) | High. Owner's instruction (2026-09-24). See §5: a community image may need to swap them out. |
| `branding/wallpapers/emerald/*/**` + overlay copies `usr/share/wallpapers/{Abstract,…,Spectrum}/**` | **CachyOS Emerald KDE** wallpapers by Dharam Dhurandhar, JPEG derivatives of upstream `CachyOS/CachyOS-Emerald-KDE@d61d1dd0` | **GPL-3.0-only** | Medium. Upstream has no LICENSE/README; each `metadata.desktop` says `GPLv3` (no "or later") and the CachyOS package says `GPL3`. The transform and per-file upstream and derived sha256 are in `branding/wallpapers/emerald/PROVENANCE`. Shipping GPL-3.0 images in the image is fine; they are separate works. `derive.sh` and the sha files are MIT. |
| `iso-profiles/river/.../plasma/look-and-feel/org.runink.river.desktop/**` | Runink original | **GPL-2.0-or-later** (the splash image: `LicenseRef-Runink-Trademark`) | **Flag:** its own `metadata.json` declares `"License": "GPLv2+"` (probably copied from a Breeze template). It is mapped as declared. Change the field to `MIT` if the owner agrees. It is a small config package (defaults, layout script, splash QML) with no Breeze code. |
| `docs/**`, `*.md` | Runink original | MIT | High. `docs/boot-test/*.png` are QEMU screenshots of Runink River's own boot output. |
| `CODE_OF_CONDUCT.md` | Contributor Covenant 2.1 | CC-BY-4.0 (© Contributor Covenant) | High. Upstream's license; not relicensed. |

**What is *not* in the tree.** No artools source, no kernel source (except one uapi
header, verbatim, as a bpfdoc test fixture, and bpfdoc's port of one kernel script), no
OpenZFS source and no binaries (`localrepo/`, `build/artifacts/` and ISOs are gitignored). The PKGBUILDs
download upstream sources at build time and verify them by checksum and signature.

**Nothing incompatible was found in the tree itself.** The combinations that need care
arise in the **built image** (§4, the CDDL / GPL question).

## 3. Upstream licenses checked

| Upstream | License | Checked from |
| --- | --- | --- |
| Artix `artools` | **GPL-3.0** (LICENSE is the GPLv3 text) | `gitea.artixlinux.org/api/v1/repos/artix/artools/contents/LICENSE`, 2026-09-23 |
| Artix `iso-profiles` | **BSD-2-Clause**, © 2017 Cromnix GNU/Linux | same API, `artix/iso-profiles` |
| archzfs | "Arch Linux package sources: MIT" (README only, no LICENSE file) | `github.com/archzfs/archzfs` README |
| Linux / zen-kernel | GPL-2.0-only with the syscall note. Some files are dual-licensed (e.g. GPL-2.0 OR BSD, GPL-2.0 OR MIT), as recorded in each file's SPDX tag | kernel `COPYING`, `LICENSES/` |
| OpenZFS | CDDL-1.0 (SPL parts historically GPL-2.0-or-later) | `openzfs/zfs` `LICENSE`, `META` |
| Arch packaging (linux-lts, linux-zen config) | 0BSD (Arch RFC 0040) | `rfc.archlinux.page/0040-license-package-sources/` |

## 4. Target state (owner decision, 2026-09-23)

The owner has decided that Runink River becomes **its own sovereign distribution**:

- **Kernel:** the **zen kernel** forked from `github.com/zen-kernel` on the **7.2.x stable**
  series. It is **GPL-2.0-only** with dual-licensed files. (HEAD currently packages
  kernel.org 6.18 LTS, and `runink-kernel/README.md` records that 6.18 *replaced* zen 7.1.
  The two need reconciling in the kernel PR. OpenZFS 2.4.4 declares `Linux-Maximum: 7.2`,
  so the match still holds, with no headroom.)
- **Userland:** the owner's **own from-scratch base**, built from source. It replaces
  Artix, `artools` and `buildiso`. See [OWN-BASE.md](OWN-BASE.md).

**Removing artools removes the GPL-3.0 entanglement entirely.** Today it is a build-time
dependency only (no file is vendored), but `patch-artools.go` exists to modify it, and the
image build cannot run without it. After the move, the tree licensing is:

| Target component | License | Note |
| --- | --- | --- |
| Kernel packaging, config and Runink River's kernel patches (`build/pkgbuilds/runink-kernel/**`) | **GPL-2.0-only** | Every patch carries an SPDX header naming GPL-2.0-only, or the dual license of the file it touches. Publish corresponding source (tarball + patches + config) with every image release. |
| OpenZFS modules and userland (`runink-zfs`, `runink-zfs-utils`) | **CDDL-1.0**, shipped as a **separate module package**, as `runink-zfs` already does | See below and [governance/ZFS-LICENSING.md](governance/ZFS-LICENSING.md). |
| archzfs initcpio hook | **MIT** | Or replace it with a from-scratch hook written for Runink River's own initramfs, which removes the last archzfs dependency. |
| Runink River's own code (base-build tooling, installer, firewall, sandbox, `riverd`) | **MIT** | |
| Docs | **MIT** | |
| Brand art | `LicenseRef-Runink-Trademark` | §5 |

**Files to rewrite or delete to get off Artix/artools:**

- **Delete:** `scripts/patch-artools.go` (it patches artools `buildiso`, `common.yaml`,
  `initcpio.sh` and `grub.sh`), `scripts/fetch-iso-profiles.sh` (it clones Artix
  iso-profiles), and `iso-profiles/*/profile.conf` (legacy artools format, already marked
  SUPERSEDED).
- **Rewrite** for the new base's image builder: `iso-profiles/*/profile.yaml`,
  `Packages-Root` and `Packages-Live` (the BSD-2-derived files). Once rewritten from
  scratch, the BSD-2-Clause notice can go. Also `scripts/build-iso.sh`,
  `scripts/build-iso-box.sh` (they call `buildiso` and import the Artix keyring),
  `builder/Containerfile` (an Artix builder image), `pacman/mirrorlist.pin` and
  `pacman/pacman.conf.in` (Artix `[system]/[world]/[galaxy]` mirrors), and
  `installer/lib/35-pacman-keyring.sh` and its two overlay copies (the Artix keyring).
- **Review** `river lint closure`, `scripts/gen-pkglist-lock.sh`, `Makefile`,
  `docs/BUILD.md`, `docs/ARCHITECTURE.md`, `README.md` and `AGENTS.md` for Artix/artools
  assumptions (`buildiso -p river`, "hard fork of Artix", the s6 packages from Artix
  `[system]`).

### The CDDL / GPL question (OpenZFS + Linux)

The full position is in [governance/ZFS-LICENSING.md](governance/ZFS-LICENSING.md). In
short: linking CDDL-1.0 OpenZFS into GPL-2.0 Linux and **distributing the resulting binary
modules** is legally contested. The FSF and the Software Freedom Conservancy consider it
infringing. Canonical has shipped prebuilt `zfs.ko` in Ubuntu since 2016 on counsel's
advice that it is permitted. Distributions handle it in three ways:

1. **Source only, built on the user's machine** (DKMS). Debian (`zfs-dkms` in `contrib`),
   Arch and archzfs (`zfs-dkms`), Gentoo. The distribution never ships a combined binary.
2. **A separate prebuilt module package.** Ubuntu (`zfs.ko` inside `linux-modules-*`),
   archzfs (`zfs-linux*`), NixOS. The modules are a separate package and never built into
   `vmlinuz`.
3. **Not shipped.** Fedora and RHEL (third-party repos only).

**Runink River uses model 2:** `runink-zfs` is a separate, out-of-tree package holding only
`spl.ko`/`zfs.ko`, built against `linux-runink-headers`, declaring `conflicts=zfs-dkms`.
It is **never** compiled into the kernel image or merged into the kernel source tree, and
its userland ships as `runink-zfs-utils` (CDDL-1.0). That is the Ubuntu and archzfs model
and the least-bad option for an image that has no compiler (Runink River's invariant rules out
DKMS on the node). **A foundation's counsel will ask about it.** The rules Runink River keeps:

- ZFS stays strictly modular: never `CONFIG_ZFS=y`, never in-tree, always its own package
  with its own `license=(CDDL-1.0)`, and its corresponding source ships alongside every
  release;
- a written legal opinion is obtained before a foundation application (LF counsel reviews
  this during intake);
- a DKMS-style build in the (compiler-equipped) builder, for users who want model 1, would
  strengthen the case.

## 5. Trademarks and artwork

Copyright licenses do not grant trademark rights. **"Runink", "Runink River" and the Runink River logo
are unregistered today** (no filing has been made). LF practice is that the *project*
mark ("Runink River") is **assigned to LF Projects, LLC** when the project joins. The *vendor* mark
(Runink) usually stays with the company, and the project stops using it in package and
path names over time. `runink-*` package names, `/etc/runink` and `ID=runink` are all live
on installed nodes (AGENTS.md, "Deliberately NOT renamed"), so this needs a migration
plan. The name clash with the unrelated *river* Wayland compositor, the choice of
foundation and the trademark transfer are covered in
[governance/LF-AIDATA.md](governance/LF-AIDATA.md).

**The image carries Runink brand assets that are NOT open.** The Runink River community mark
(the M2 mascot `branding/mascot/river-mascot.svg`, the marks generated from it,
`branding/logo/river-mark.svg`, `river-mark-small.svg`, `river-lockup*.svg`, and the ASCII
rendition `branding/ascii/river-mark-small.txt`) and everything rendered from it (the GRUB
background, the Plymouth and Plasma splash logo, the Runink River wallpaper, the
`runink-river` hicolor icons: Kickoff button, `LOGO=` in os-release, SDDM, the installer's
edition icon; `branding/icons/favicon/*`; `/etc/issue`) are mapped to
`LicenseRef-Runink-Trademark`: all rights reserved, trademark of Runink. A public or
community Runink River image that is not published by Runink **may need to swap them out**. Replace
those files (and the icon names if desired), re-run `branding/render.sh`, and the rest of
the tree is unaffected.

Linux® is the registered trademark of Linus Torvalds in the U.S. and other countries.

## 6. Open flags

1. **archzfs**: confirm the MIT claim upstream, or replace the hook with Runink River's own.
2. **AUR `linux-xanmod-lts`** derivation in the kernel PKGBUILD: confirm the license,
   or drop the XanMod switch in the zen move.
3. **The plasma LnF `metadata.json`** says GPLv2+. Keep it or change it to MIT.
4. **`runtime/` dependencies.** `riverd` may link only code available under an
   OSI-approved license compatible with MIT. Anything that is not must be re-implemented
   in this repository.
5. **AI-assisted history.** Many commits were made with AI coding tools. The copyright
   position of AI-generated portions is unsettled. The LF's generative-AI policy asks
   contributors to make sure the output can be licensed under the project license. The
   project's position is in [CONTRIBUTING.md](../CONTRIBUTING.md).
6. **The ZFS legal opinion** (§4) before any foundation application.

## 7. How to check

```bash
# REUSE 3.3 compliance (reads SPDX headers, .license sidecars, REUSE.toml and LICENSES/),
# the Go port of `reuse lint` in the river CLI, run from the repository root:
(cd cli && go run ./cmd/river lint reuse --repo ..)
```
