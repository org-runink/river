# Building the Runink River ISO

## Flow

```bash
make repo         # pin the iso-profiles fork base
make components   # build/build-all.sh: optional payload, installer binaries, models manifest, makepkg
make localrepo    # repo-add every built package into localrepo/ (the [runink] repo)
make lint         # shellcheck, closure-lint, installer/branding sync, k0s pin, no-python
make test         # gofmt + go vet + go test of installer/ (river-hwprobe, river-plan)
make iso          # buildiso -p river (Artix only; gated on the k0s pin and no-python lints)
make vmtest       # boot the ISO in qemu, install to a virtual disk, run the asserts
```

`make help` lists every target. The one in-tree profile is `iso-profiles/river` (Runink
River); a downstream profile builds with `build/local-iso.sh` and `RIVER_PROFILE_DIR`.

## Prerequisites

- **`make components`**: `sh`, Go (for `build/20-installer-binaries.sh`, which builds
  `river-hwprobe` and `river-plan` from `installer/` with `GOAMD64=v1`) and, for packaging,
  `makepkg` (Arch or Artix). Without
  `makepkg` the artifacts are staged in `build/artifacts/` and packaging is skipped. A
  downstream payload brings its own toolchain requirements.
- **`make localrepo`**: `repo-add` (pacman).
- **`make iso`**: Artix with `artools` (`artools-iso`, `artools-pkg`), or an Artix
  container. `buildiso` needs root, loop devices, and an overlayfs upper directory that is
  not on ZFS.
- **`make vmtest`**: qemu with KVM and at least 8 GiB of RAM for the guest.

## Building on any podman host

`builder/Containerfile` pins an Artix build environment (artools, Go, a C toolchain). It
installs the Flutter SDK only with `BUILD_FLUTTER=1`, for payloads that need it.

```bash
make builder                     # build the runink-os-builder image
make iso-in-builder              # components + localrepo + iso inside it
RIVER_PAYLOAD_DIR=/path/to/payload make iso-in-builder
```

The container runs with `--privileged` (buildiso needs mount and loop devices) and
`--userns=keep-id`, so files written into the bind-mounted checkout keep your uid. When
`RIVER_PAYLOAD_DIR` is set, that directory is bind-mounted at `/payload` and the inner
build sees `RIVER_PAYLOAD_DIR=/payload`.

A nested or sandboxed podman that cannot allocate loop devices cannot build the ISO. Build
on real Artix hardware or a rootful privileged container instead.
`scripts/build-iso-box.sh` is the script used for that path: it backs `/var/lib/artools`
with tmpfs (overlayfs cannot use a ZFS upper directory) and injects `[runink]` into the
artools pacman configuration.

## Building on a workstation, with root only for buildiso

`build/local-iso.sh` is the owner-facing path on any podman host (Arch, Artix or other). It
runs every stage that does not need root as the calling user, rebuilds every component this
project builds from pinned sources (only pinned third-party inputs are cached: the builder
image, the k0s airgap images, the model weights), and ends by printing the one command that
needs root:

```bash
df -h ~/.cache                                       # a build wants about 40 GB free
build/local-iso.sh                                   # PROFILE=river (the default), PUBLIC
sudo sh build/iso-root-stage.sh ~/.cache/river-build/local-iso/root-stage-river.env
build/qemu-gui-test.sh ~/.cache/river-build/iso-out/runink-river-<date>-x86_64.iso
build/qemu-test.sh ~/.cache/river-build/iso-out/runink-river-<date>-x86_64.iso
```

The image is named `runink-river-<date>-x86_64.iso`, volume label `RIVER` (its EFI
partition `RIVER_EFI`); the build renames buildiso's `artix-<profile>-s6-...`
(`scripts/patch-artools.go`). It is **public**: the OS, the desktop and the installer, no
models and no other payload, and the build refuses to attach one.

A downstream server profile (`RIVER_PROFILE_DIR`, below) may build a **private** image
(`RIVER_PUBLIC=0`, implied by `RIVER_PAYLOAD_DIR`) that carries encrypted payloads; it is
named `<ISO_NAME>-<RIVER_ISO_VARIANT>-<date>-x86_64.iso`, and its repository, work directory
and stage file carry the variant (`root-stage-<profile>-<variant>.env`), so the public and
the private image can be prepared side by side. See [PAYLOADS.md](PAYLOADS.md).

| Stage | As | What |
|---|---|---|
| 0 | user | an external profile (`RIVER_PROFILE_DIR`) or edition branding (`RIVER_BRANDING_DIR`): the profile copied under the state directory |
| 1 | user | the Artix builder image (`make builder`) |
| 2 | user | `linux-runink` + `runink-zfs` from the in-tree PKGBUILDs in rootless podman (`build/build-kernel-zfs.sh`, about an hour at `-j8`), **rebuilt from source on every run** and then checked by `build/verify-kernel-zfs.sh`: the pinned kernelrelease, every line of `config.require` against the built `.config`, and `spl.ko`, `zfs.ko` and an in-tree module all signed by the same per-build key (`runink_signing.pem`, kept 0600 next to the packages, never in the image) |
| 3 | user | the downstream payload, `"$RIVER_PAYLOAD_DIR/build.sh" build/artifacts` on the host (a server profile only), and the k0s airgap bundle (`build/k0s-airgap.sh`: the images `k0s airgap list-images` names for the profile's own `/etc/k0s/k0s.yaml` or the reference `build/k0s/k0s.yaml`, pulled rootless by digest from `build/k0s-images.lock`, one verified docker-archive in `build/artifacts/k0s-airgap/`, reused when it verifies) |
| 4 | user | `build/build-all.sh` in the builder, rootless, with `RIVER_PAYLOAD_PREBUILT=1` when stage 3 ran, and `RIVER_GUIDE_MODEL=1` (fetch the river-guide model) when the profile ships the guide |
| 5 | user | the `[runink]` repository for the profile (`~/.cache/river-build/local-iso/localrepo-<profile>`) |
| 6 | user | private server builds only: the encrypted model payload ([MODEL-PAYLOAD.md](MODEL-PAYLOAD.md)) and every downstream payload of `RIVER_PAYLOAD_STAGE`, sealed with the same passphrase ([PAYLOADS.md](PAYLOADS.md)) |
| 7 | user | Tier 1, the installer and guide Go tests, the k0s pin, and the closure lint of the in-tree profile (and a staged one) against that repository |
| 8 | user | the builder image saved for root's podman, and the root-stage file |
| root | root | `build/iso-root-stage.sh`: `buildiso -i s6` in a privileged container (work directory on disk, not tmpfs), the payloads (private) added under `/river-models` and `/river-<kind>/` with `xorriso` (boot records replayed) and the image renamed for its variant, or (public) checked to carry none, label check, `.sha256`, ISO handed back to the user |

Runink River takes no downstream payload and no models. Its image needs only the kernel and
ZFS packages from `[runink]`; every component package (k0s, its airgap bundle, NAT64, the
guide) is built and linted anyway, for the downstream distributions.
Environment: `PROFILE`, `RIVER_PROFILE_DIR`, `RIVER_BRANDING_DIR`, `RIVER_PUBLIC`,
`RIVER_ISO_VARIANT`, `RIVER_PAYLOAD_DIR`, `RIVER_PAYLOAD_STAGE`, `MODELS_DIR`,
`MODEL_PAYLOAD` (`auto`/`yes`/`no`), `RIVER_MODELS_PASSPHRASE_FILE`, `OUT_DIR`,
`KERNEL_OUT`, `STATE` (see the script's header). Everything large lives under
`~/.cache/river-build`, not `/tmp`.

`build/qemu-gui-test.sh` drives the graphical installer end to end under QEMU/KVM with OVMF,
unattended, as the user (prerequisite: `edk2-ovmf`): the live desktop opens the installer,
the install runs through every screen, the installed machine unlocks, `/home` is mounted,
the firewall is loaded, the admin logs in at SDDM and Plasma starts, and the network is up
(`plasma-login`, `net-ready`; screenshots of every screen). Against an ISO built before a
change it overlays this tree's installer and root overlay on the live system first, and
lists every file it copied. A profile's own installed-system checks
(`tests/installed.d/`, [below](#installed-system-checks)) run last and join its verdict.
`build/qemu-test.sh` checks a live medium without installing:
the GRUB entry, SDDM, the live user logged into Plasma (screenshots `desktop-early.png` and
`desktop.png`), NetworkManager, SDDM, Bluetooth and CUPS up in s6-rc, and the network step.
Its `--profile server` mode (with `--lab`, `--offline`, `--expect-payloads`, `--public`) checks
a downstream server distribution's ISO. See the scripts' headers.

## Downstream payloads

Runink River builds a complete base image without any payload. A downstream platform that wants to
ship on Runink River keeps its payload **outside this repository** and points `RIVER_PAYLOAD_DIR`
at it:

```bash
RIVER_PAYLOAD_DIR=/path/to/payload make components
```

### Build-time contract

`build/build-all.sh` runs `"$RIVER_PAYLOAD_DIR/build.sh" "$OUT_DIR"` (`OUT_DIR` is
`build/artifacts/`). The script must be executable and must stage:

| Path under `$OUT_DIR` | Packaged by | Installed at |
|---|---|---|
| `bin/*` | `runink-runtime` | `/usr/local/bin/` (mode 0755) |
| `core-tree/` | `runink-core` | `/usr/local/share/runink/core/` |
| `models.tiers`, `models.lock` (optional) | `runink-installer` | `/usr/local/share/runink/` (read by `river-plan`) |

A private build may also stage plaintext payloads for the medium outside `build/artifacts`, in
`RIVER_PAYLOAD_STAGE/<kind>/<group>/` (files plus a generic `LOCK`); `build/local-iso.sh`
packs each into `/river-<kind>/<group>/` ([PAYLOADS.md](PAYLOADS.md)).

Inside `core-tree/`:

| Entry | Required | Used by |
|---|---|---|
| `deploy` | yes, executable | First-boot enrollment on a controller or single node, after k0s is ready. It runs with the working directory set to the tree, `KUBECONFIG=/var/lib/k0s/pki/admin.conf`, and `k0s` plus the tree's `bin/` on `PATH`. |
| `enroll.d/*.sh` | no | Sourced (not executed) by first-boot enrollment, in lexical order, with every enrollment variable in scope and `RUNINK_USER` set, **before** the enrollment file is shredded. Hooks run under `umask 077`, must not call `exit`, and must write secrets 0600 inside 0700 directories. Any new secret path belongs in `river-perms`' table. |
| `smoke.sh` | no | Run last by `tests/smoke-k0s.sh` when executable. |
| `bin/` | no | Added to `PATH` for `deploy`. |
| `firstboot.d/*` | no | Installed to `/usr/local/lib/runink/firstboot.d/` and run by `river-firstboot-hooks` after the k0s API is up, with or without enrollment, each until it has succeeded once ([PAYLOADS.md](PAYLOADS.md#first-boot-hand-off)). Every entry must be an executable file. |

Payload-specific secrets are the payload's business: its `enroll.d` hooks read them from
the enrollment file. Runink River does not define or document them.

Container images can be baked by placing OCI or docker-archive tarballs in
`/usr/local/share/runink/images/`; enrollment imports every `*.tar` there into k0s's
containerd before running `deploy`.

### Failure rules

- A payload `build.sh` that fails fails the build.
- `RIVER_PAYLOAD_PREBUILT=1` tells `build-all.sh` the caller already ran the payload's
  `build.sh` into `$OUT_DIR` (`build/local-iso.sh` does, on the host where the payload's
  sources live); it still refuses a tree without an executable `core-tree/deploy`.
- `runink-core` refuses to package a payload tree without an executable `deploy`.
- `runink-runtime` refuses to package when a payload build staged no host binaries, or
  staged an empty file.
- `runink-installer` refuses to package without both `river-hwprobe` and `river-plan`.
- `RUNINK_ALLOW_PARTIAL=1` downgrades the first and third to warnings, for iterating on
  other packages. An image built that way must not be shipped.

### Without a payload

When `RIVER_PAYLOAD_DIR` is unset, `build-all.sh` exports `RIVER_PAYLOAD_NONE=1`.
`runink-runtime` then packages no host binaries, and `runink-core` ships an empty tree
with a `PAYLOAD-NONE` marker. At first boot enrollment finds the marker and logs "base
image (no downstream payload), node is a k0s host with nothing deployed, by design". The
marker lets enrollment tell a base image from a payload build that shipped hollow. That
second case is recorded in `/var/lib/runink/DEPLOY-INCOMPLETE` instead of being reported
as success.

## Downstream distributions

A payload adds software to a Runink River image. A **downstream distribution** goes further:
it ships its own images, with its own name, volume label, branding, edition descriptors and
package set, built from Runink River's build tooling but from a profile that lives **outside
this repository**. The distribution keeps its profiles and branding in its own repository,
checks out a pinned Runink River commit, and points the build at them:

```bash
# in a detached worktree of a pinned Runink River commit
RIVER_PROFILE_DIR=/path/to/distro/profiles/example-server \
RIVER_BRANDING_DIR=/path/to/distro/branding/example-server \
RIVER_PAYLOAD_DIR=/path/to/payload \
    build/local-iso.sh
sudo sh build/iso-root-stage.sh ~/.cache/river-build/local-iso/root-stage-example-server-private.env
```

`PROFILE` defaults to the basename of `RIVER_PROFILE_DIR`. The in-tree profile builds exactly
as before when neither variable is set. A server distribution needs no profile in this
repository: since Runink River became the workstation (2026-09-26) the server profile lives
downstream, and what it builds on stays here: the packaging (`build/pkgbuilds/*`: k0s, its
airgap bundle, NAT64, the payload packages, river-guide), the payload and first-boot contract
([PAYLOADS.md](PAYLOADS.md)), and the installer steps a server uses (`installer/lib/`).

### The profile

An external profile has the same shape as `iso-profiles/<profile>/` (`profile.yaml`,
`Packages-Root`, `Packages-Live`, `forbidden.*`, `root-overlay/`, `live-overlay/`, `grub/`,
and `common.yaml` for a server), plus a `river-profile.env`, read (never sourced) by
`build/profile-lib.sh`:

| Key | Meaning |
|---|---|
| `KIND` | `server` or `workstation`. A server takes the model and downstream payloads and has a private variant (`RIVER_PUBLIC=0`); a workstation takes neither. The in-tree profile is known by name (`river`, Runink River, a workstation). |
| `ISO_LABEL` | The ISO volume label, which is also the live `root=LABEL` kernel argument. `[A-Z0-9_]`, at most 32 characters, and unique per image: two images with one label can mount each other's root filesystem. |
| `ISO_NAME` | The ISO file name prefix: `<ISO_NAME>-<date>-x86_64.iso` (a private variant is `<ISO_NAME>-<variant>-<date>-x86_64.iso`). |
| `GRUB_TITLE` | The live menu title, for a profile that keeps Artix's live menu. A profile with `grub/grub.cfg` writes its own entries. Letters, digits, spaces and `. _ + -`. |
| `EDITION` | Optional. The `id` of the image's own edition descriptor, for a medium that carries several editions under ONE `ISO_LABEL` (see "One medium, several editions" below). `[a-z][a-z0-9-]*`, at most 32 characters. |

```ini
KIND=server
ISO_LABEL=EXAMPLE_SERVER
ISO_NAME=example-server
GRUB_TITLE=Example Server
```

**Edition descriptors.** The graphical installer reads
`/usr/share/river/installer/editions/*.json` from the live medium
(schema: `Edition` in `installer/internal/wizard/editions.go`), so a profile supplies its own in
`live-overlay/usr/share/river/installer/editions/`. The installer decodes them strictly: a
descriptor with a field it does not know is skipped, so a descriptor carries only the fields
that schema lists (the title and description, not an icon or colours). Exactly one of them
must carry the image's own `ISO_LABEL` as `medium_label`; `build/local-iso.sh` refuses the build
otherwise, because the installer would not know which edition it is installing. A descriptor
for another edition is offered when a volume with its label is present (a stick with both images).

**One medium, several editions.** A distribution may put several editions on one medium: it
builds each edition as its own image, all with the same `ISO_LABEL`, and composes them into
one ISO, each edition's live system in its own directory (the live initramfs takes the
directory as `root=<dir>`, default `LiveOS`) and booted from its own menu entry. Every edition
then sets `EDITION=<its descriptor id>`, and every entry in its `grub/kernels.cfg` passes
`river.edition=<id>`: the installer takes the running edition from that argument among the
descriptors that carry the medium's label, and treats the others as present on the same
medium (choosing one tells the operator to restart into its menu entry, as for a stick with
several images). `build/local-iso.sh` refuses such an edition unless exactly one descriptor
carries both the label and the id and every `linux` line of its `grub/kernels.cfg` passes
`river.edition=<id>`. The account screen offers the medium passphrase only to an edition whose
steps unpack a payload, so the payloads on the medium are for the editions that take them.
Composing the ISO is the distribution's own step; Runink River builds each edition.

**Installer steps and the guide.** The profile carries copies of the installer steps and of
`runink-install` like the in-tree profile; they must match `installer/lib/` byte for byte
(`river lint installer-sync <profile-dir>`). A profile that ships river-guide carries a
`guide-model.lock` equal to `guide/model.lock`, which the `river-guide` package and the staged
model come from, and the guide's s6 service and getty override in its `live-overlay/`;
`build/local-iso.sh` then fetches the model and stages it into the profile copy. (Runink
River's own medium does not carry the guide: the graphical installer is its front end, and
the 1.1 GB model would ride along unused.)

**k0s.** A server profile's own `root-overlay/etc/k0s/k0s.yaml`, when it has one, is what the
k0s airgap bundle is listed from (stage 3); its image list must match `build/k0s-images.lock`.
The reference config is `build/k0s/k0s.yaml`.

### Branding

`RIVER_BRANDING_DIR` holds an edition's branding, kept apart from the profile so that the
profile can stay a plain copy of an upstream one:

| Path | Effect |
|---|---|
| `overlay/` | Copied over the profile, file by file (same tree: `root-overlay/`, `live-overlay/`, `grub/`, `river-profile.env`). Replace `os-release`, `issue`, `motd`, the GRUB theme the installer copies (`root-overlay/usr/share/runink/branding/grub/river/`), Plymouth and SDDM themes, wallpapers, icons and the edition descriptors here. |
| `overlay/grub/theme/` | The **live** menu's GRUB theme (`theme.txt` and its images). Without it the live menu uses `branding/grub/<theme>` from this repository. |
| `remove` | Optional. One profile-relative path per line, deleted from the copy (for example an upstream wallpaper set the edition does not ship). An entry that names nothing is an error. |

A branding directory may also be used with an in-tree profile.

### What the build does with them

`build/local-iso.sh` copies the profile to `$STATE/profiles/<flavor>/<profile>/`, applies the
branding, links the river-guide model in, and runs the profile checks on the copy
(`lint-profile-manifest`, `lint-installer-sync`, the closure lint, the edition descriptor
check). The source directories are never written to. `build/iso-root-stage.sh` mounts the copy
into the build container at `/profile/<profile>` and passes `RIVER_PROFILE_DIR`;
`scripts/build-iso-box.sh` links it into the artools workspace and hands the label, ISO name,
title and profile directory to `scripts/patch-artools.go`. The ISO's label is read back off
the finished image and compared with `ISO_LABEL`, as for the in-tree profiles.
`river test external-profile` (Tier 1) exercises the description, staging and lint steps.

The distribution owns everything in its profile, including keeping it in step with the
Runink River commit it pins: installer steps, package lists and s6 services change here, and
a profile copied from an older commit fails the checks above until it is re-synced.

### Installed-system checks

A profile may carry its own checks for `build/qemu-gui-test.sh` in `tests/installed.d/`: every
**executable** `*.sh` there runs as root on the installed VM after its first boot and the
harness's own checks (`golden` on a server), in name order, as `sh HOOK` with its output on the
serial log (`build/qemu-hooks.sh` holds the contract; `river test installed-hooks` exercises it in
Tier 1):

```sh
#!/bin/sh
# RIVERTEST-CHECKS: example-api example-later
# RIVERTEST-TIMEOUT: 900
echo "RIVERTEST OK example-api"                       # or: RIVERTEST FAIL example-api (<why>)
echo "RIVERTEST SKIP example-later (enable with RIVER_HOOK_EXAMPLE_LATER=1)"
```

- The `RIVERTEST-CHECKS` header is required and declares the check names (`[a-z0-9-]`); the
  harness reads it on the host, so every declared check is required by the verdict even if the
  VM never gets as far as running the hook. A hook without one is refused before the VM starts.
- `RIVERTEST OK` passes, `RIVERTEST FAIL` or no line fails, and `RIVERTEST SKIP` is listed as
  SKIP without failing (a check whose prerequisite has not landed yet).
- The harness adds `hook-<name>` per hook: it fails when the hook exits non-zero, runs past its
  `RIVERTEST-TIMEOUT` (default 1200 s; the installed phase's budget grows by the sum), reports a
  declared check twice or not at all, or reports a check it did not declare.
- Every `RIVER_HOOK_*` variable in the harness's environment is exported to the hooks
  (`/run/rt/config`), which is how a profile switches its own optional checks on.
- Staging keeps the directory: `build/local-iso.sh` copies the whole profile, so the staged copy
  the harness is pointed at (`RIVER_PROFILE_DIR`) carries it.

## Models manifest

`build/50-models-manifest.sh` writes `models.manifest`, the sha256 list of the model set
pinned in `models.lock`, which first-boot enrollment verifies after syncing the weights and
the installer verifies after unpacking a model payload. It checks the cache filled by
`build/models-fetch.sh` (`MODELS_DIR`, default `~/.cache/river-build/models`) against the
lock first. On a payload build it fails if the cache does not match (`RUNINK_ALLOW_PARTIAL=1`
writes an empty manifest instead, which turns on-node verification into a no-op). On a base
image with no cache it writes no manifest at all, and enrollment skips verification because
the file is absent.

Models are never part of the image. A server medium may carry them NEXT to the image as the
encrypted model payload (`/river-models`), which the installer unpacks into `<pool>/models`;
see [MODEL-PAYLOAD.md](MODEL-PAYLOAD.md).

## Kernel and ZFS

Both the live and the installed image run exactly one kernel, `linux-runink`: a fork of
zen-kernel on the 7.2.x stable series (`build/pkgbuilds/runink-kernel/`), built for generic
x86-64-v3. It is paired with `runink-zfs`, a prebuilt OpenZFS stable module for that exact
kernel release, and with `runink-zfs-utils`, the matching userland and mkinitcpio `zfs`
hook. The pair comes from one split PKGBUILD built from the signed OpenZFS release tarball.
`runink-zfs` depends on `runink-zfs-utils` of the same version, and `prepare()` refuses a
kernel outside the range the OpenZFS release declares in its `META` file.

- `scripts/patch-artools.go` swaps the kernel in artools' `common.yaml`. It also fixes
  buildiso's initcpio step, which assumes the mainline kernel's version file, and adds a
  GRUB default and timeout to the live menu so a headless machine boots unattended.
- The kernel and ZFS packages are built by two `workflow_dispatch` workflows on
  GitHub-hosted runners: `kernel-build.yml`, then `zfs-build.yml`, which compiles against
  the kernel run's headers. Download their artifacts into `build/artifacts/` before
  `make localrepo`, or build the PKGBUILDs locally with `makepkg`.
- `[runink]` is injected before the distribution repos so its kernel and ZFS win version
  resolution.

The kernel pin (`_major`, `_minor`, `_zenrel` in the kernel PKGBUILD) and the OpenZFS pin
move together: on every zen stable release, and onto the next series before the current
one reaches end of life. See `build/pkgbuilds/runink-kernel/README.md` for the sources,
signature keys and config policy.

## Pinning and reproducibility

- `build/config.env` pins upstream versions (`K0S_VERSION`, ...). `river lint k0s-pin`
  checks that value, the `runink-k0s` PKGBUILD and the package actually in `localrepo/`
  against each other, because buildiso bakes whatever sits in `localrepo/`. `make iso`
  runs it, so a stale package cannot slip into an image.
- `pacman/mirrorlist.pin` freezes a distribution mirror snapshot; `pacman/pacman.conf.in`
  declares the `[runink]` file repo.
- `make lock` resolves `Packages-Root` against the pinned repos and records the full
  package set in `Pkglist.lock`; it is expected to be byte-identical across two runs.
- `runink-k0s` and `runink-tayga` are built with integrity checking. `runink-installer`,
  `runink-runtime` and `runink-core` package local trees and skip it.

### Reproducibility status

"Reproducible" here means the reproducible-builds.org definition: the same source, build
environment and instructions give bit-for-bit identical output. Status on 2026-09-24:

| Artifact | Status | Evidence |
| --- | --- | --- |
| Go binaries (`river-hwprobe`, `river-plan`, `river-guide`) | **Repeatable on one host**: two builds of the same commit with the release flags (`CGO_ENABLED=0 GOAMD64=v1 go build -trimpath`) gave identical sha256. Not yet compared across hosts or Go patch releases. | `build/20-installer-binaries.sh`, `build/25-river-guide.sh` |
| Own-base recipes (s6, skalibs, execline) | **Reproducible** in the pinned `--network=none` builder: `SOURCE_DATE_EPOCH`, fixed locale and time zone, clamped mtimes. | [OWN-BASE.md](OWN-BASE.md) §5.3, `base/river-build` |
| `Pkglist.lock` (the resolved package set) | **Repeatable**: `make lock` is expected to be byte-identical across runs against the pinned mirror snapshot. | `scripts/gen-pkglist-lock.sh` |
| `linux-runink`, `runink-zfs` packages | **Not verified.** Sources are pinned by sha256 and signature, and the kernel PKGBUILD derives `KBUILD_BUILD_TIMESTAMP` from `SOURCE_DATE_EPOCH` when it is set, but no package has been rebuilt and compared. | `build/pkgbuilds/runink-kernel/`, `build/pkgbuilds/runink-zfs/` |
| The Runink River ISO | **Not reproducible.** The ISO build path (`make iso`, artools/buildiso) does not set `SOURCE_DATE_EPOCH` or clamp timestamps, and no two ISO builds have been compared. Reproducible images are an exit criterion of the own base (phase 2). | [OWN-BASE.md](OWN-BASE.md), [ROADMAP.md](../ROADMAP.md) |

Until the ISO is reproducible, a release maintainer checks the published artifacts against
their own build before signing ([RELEASE.md](../RELEASE.md)), and the provenance that
`release-attest.yml` records proves which digests were published, not how they were built
(SLSA Build L1; see [governance/OPENSSF-BEST-PRACTICES.md](governance/OPENSSF-BEST-PRACTICES.md)).

## artools notes

- artools 0.39+ reads `profile.yaml`; `profile.conf`, `Packages-Root` and `Packages-Live`
  are not consulted by buildiso.
- The `[runink]` repo must be present in artools' ISO pacman configuration
  (`/usr/share/artools/pacman.conf.d/iso-x86_64.conf`) or the `runink-*` packages do not
  resolve.
- On Artix, `basestrap` comes from `artools-base`, and live boot needs `artix-grub-live`.
- The default init for buildiso is openrc; pass `-i s6` when invoking it by hand.

## CI

`.github/workflows/ci.yml` runs `tier1` (`scripts/ci-tier1.sh`: shellcheck, the
installer/overlay sync check, and the hermetic lints: branding sync, the no-python guard,
the k0s pin, the one-engine check), `installer-go` (gofmt, vet, the planner's table-driven
tests, a `GOAMD64=v1` build), `river-guide` and `go-security` (gosec, govulncheck). This
public repository runs only on GitHub-hosted runners, never on a self-hosted one; a fork's
pull request runs the same jobs once a maintainer approves the run. `kernel-build.yml` and
`zfs-build.yml` are manual dispatch on standard runners (the kernel job first removes the
runner's unused preinstalled toolchains to get ~30 GB of disk). `images.yml` builds the
public ISO on a hosted `ubuntu-24.04` runner the same way (`build/local-iso.sh` with
`MODEL_PAYLOAD=no`, then the root stage) on a manual dispatch or a pushed `runink-os-*` tag,
never on a pull request; its publish job stays disabled. The Tier 2 VM boot does not fit a
standard runner and is run locally. Maintainers also build the ISO locally with
`build/local-iso.sh`, `make iso` on Artix or `make iso-in-builder` on a podman host. The CI
plan, including KVM boot tests, is in
[governance/CI.md](governance/CI.md). Release signing is described in
[RELEASE-SIGNING.md](RELEASE-SIGNING.md).

## Status

- The zen 7.2.x kernel and its ZFS pairing have not yet been boot-tested end to end. An
  earlier image (kernel.org LTS kernel) passed the qemu/KVM boot test: GRUB, the live s6
  session and `zpool create` on a real disk in the live environment
  (`docs/boot-test/`).
- The profile `root-overlay` lands in the live rootfs as well as on the target; a cleaner
  live/target split is an open refinement.
- Boot tests (`make vmtest`, `tests/boot-test-onbox.sh`) need KVM and are run by hand.
