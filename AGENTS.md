# AGENTS.md — rules for contributors and coding agents (Runink River)

This file is for everyone who changes this repository: people, and AI coding agents working
on their behalf. It says what the repository is, how to build and test it, the invariants a
change must keep, and the traps that catch newcomers. The same rules apply whoever (or
whatever) writes the patch, and a human contributor signs off every commit
([CONTRIBUTING.md](CONTRIBUTING.md), DCO). `CLAUDE.md` only points here.

## 1. What this is

**Runink River is an optimized developer workstation on s6**: one OS image (KDE Plasma on
Wayland, an encrypted ZFS root, one pinned kernel), its packaging, its graphical installer
and (planned) the RIVER runtime, nothing else. RIVER (*Raft-Integrated Validated Event
Runtime*) names only that pipeline runtime. The one in-tree profile is `iso-profiles/river`;
it builds `runink-river-<date>-x86_64.iso` (volume label `RIVER`, EFI partition `RIVER_EFI`).
It is a public, open-source project (MIT userspace, GPL-2.0-only kernel, CDDL-1.0 ZFS;
[docs/LICENSING.md](docs/LICENSING.md)). It must never carry application logic, a model, a
secret, or anything that names a specific downstream product.

- **Naming.** In prose, the project and its image are "Runink River". Never a bare "River"
  as a name (it collides with an unrelated Wayland compositor). Package, path, binary and
  variable names (`runink-*`, `river-*`, `RIVER_*`, `/etc/runink`) are identifiers; do not
  rename them for branding.
- Installed-machine identifiers are part of the product and must not be renamed without a
  migration: the `runink-*` / `linux-runink` packages and the `[runink]` pacman repo,
  `/etc/runink/*`, `/etc/runink-os-version`, `ID=runink`, the `runink-os-*` release tags,
  the `runink` user / hostname, `/usr/local/share/runink`, `/var/lib/core`.

## 2. Place in the ecosystem

- **Upstreams** (all pinned, see invariant 9): the zen kernel (7.2.x stable) and kernel.org,
  OpenZFS, k0s and its images, a distribution mirror snapshot (`pacman/mirrorlist.pin`), and
  Artix `artools`/`buildiso`, which is the **transitional** ISO builder while the own
  from-source base ([docs/OWN-BASE.md](docs/OWN-BASE.md), `base/`) replaces it. Runink River is
  not an Artix fork.
- **Downstream distributions** build their own images from a pinned commit of this repository
  with an out-of-tree profile (`RIVER_PROFILE_DIR`, [docs/BUILD.md](docs/BUILD.md),
  "Downstream distributions") and, for a private image, out-of-tree payloads
  (`RIVER_PAYLOAD_DIR`, [docs/PAYLOADS.md](docs/PAYLOADS.md)). The packaging they need stays
  here and is built with every image (`build/pkgbuilds/*`, including k0s, its airgap bundle
  and NAT64), as do the payload and first-boot contract and the external-profile support.
  Never add a downstream profile, payload, its binaries, its manifests or its secrets to this
  tree, and never name or document a specific downstream product here. A change to a
  contract they rely on (installer steps, `river-profile.env` keys, the payload layout) breaks
  them at their next pin bump: say so in the PR and in `CHANGELOG.md`.
- Application code never lives here. `runtime/` holds the plan for `riverd`; it executes
  pipelines that workloads submit, and the pipelines, contracts and business rules belong to
  those workloads.

## 3. Map

| Path | What |
|---|---|
| `iso-profiles/river/` | the one in-tree profile: `profile.yaml`, `Packages-Root`/`Packages-Live` (allow-lists), `forbidden.*`, `root-overlay/` (s6 services, installer copies, firewall), `live-overlay/`, `grub/` |
| `build/` | the image build: `local-iso.sh` (entry point), `iso-root-stage.sh` (the root step), `build-all.sh`, `build-kernel-zfs.sh` + `verify-kernel-zfs.sh`, `k0s-airgap.sh`, `models-fetch.sh`, `profile-lib.sh`, `config.env` (pins), `pkgbuilds/*` (every `[runink]` package), the QEMU harnesses (`qemu-gui-test.sh` with its profile-hook contract `qemu-hooks.sh`, `qemu-test.sh`, `qemu-lan-test.sh`), `cloud-image.sh`, `release-assets.sh`, `tools/` (Go: `archaudit`, `bpfdoc`) |
| `builder/` | `Containerfile` of the pinned Artix build environment (`make builder`, image `runink-os-builder`) |
| `installer/` | Go module: `river-hwprobe`, `river-plan` (stdlib only, `GOAMD64=v1`), the graphical installer (`gui/`, `internal/wizard`), `modelpack`/`payloadpack`, `lib/` (the install steps, mirrored into the profile) |
| `guide/` | Go module: river-guide, the install guide agent for a live medium ([docs/INSTALL-GUIDE-AGENT.md](docs/INSTALL-GUIDE-AGENT.md)); `model.lock` pins its model |
| `base/` | the own from-source base: recipes, `river-build`, `river-sign` (offline signing), `keys/`, `sources.lock` |
| `branding/` | artwork sources and `render.sh`; `palette.md` is the only colour source |
| `scripts/` | lints (`lint-*.sh`), `ci-tier1.sh`, `build-iso.sh`, `build-iso-box.sh`, `patch-artools.go`, `gen-pkglist-lock.sh` |
| `tests/` | on-target asserts (`assert-golden.sh`, `smoke-k0s.sh`) and Tier 1 unit scripts ([tests/README.md](tests/README.md)) |
| `validation/` | Go module: `rivervalidate`, the inference validation suite; `SELECTION.md` records model evidence |
| `bench/analytics/` | Go module: `riverbench`, the analytics benchmark harness |
| `website/` | the Hugo (Hextra, vendored) documentation site, published to GitHub Pages |
| `install.sh` | the signature-verifying one-line installer (downloads, verifies, writes a stick) |
| `packaging/aur/` | AUR packaging of the kernel (`river lint aur-sync`) |
| `pacman/` | `mirrorlist.pin` (the mirror snapshot) and `pacman.conf.in` (the `[runink]` repo) |
| `models.lock`, `models.tiers`, `models.evidence.json` | the pinned model set and its evidence (models travel only as an encrypted payload beside a downstream image, never in this one) |
| `runtime/` | plan only for `riverd`; nothing is built |
| `KEYS` | the release-signing key's fingerprint |

## 4. Build, test, lint

### Checks every PR runs (verified 2026-09-27 on a dev host)

```bash
RIVER_TIER1_NO_CONTAINER=1 sh scripts/ci-tier1.sh   # Tier 1 on the host (needs shellcheck, go)
sh scripts/ci-tier1.sh      # the same inside an ephemeral rootless podman container (fork PRs in CI)
make test                   # gofmt + go vet + go test of installer/
make test-guide             # the same for guide/ (river-guide)
make test-bench             # bench/analytics;  make test-bpfdoc: build/tools/bpfdoc
make docs                   # the website, into ~/.cache/river-docs-build/public (CI pins Hugo 0.147.3)
make help                   # every target
```

Verified here: the host-mode Tier 1, `make test`, `make test-guide`, `make docs`, `make help`.
`make lint` adds the closure lint and the k0s pin against `localrepo/`; it needs the pinned
sync databases and a built `localrepo/` (not verified here).

### Building the ISO (not verified here: needs podman, about 40 GB, and root once)

`build/local-iso.sh` is the path for people and agents. It runs every stage that does not
need root as the calling user and ends by printing **the one command that needs root**:

```bash
df -h ~/.cache                          # about 40 GB free; everything lands under ~/.cache/river-build
build/local-iso.sh --help               # stages and environment
build/local-iso.sh                      # PROFILE=river (default), PUBLIC: no payload, no models
sudo sh build/iso-root-stage.sh ~/.cache/river-build/local-iso/root-stage-river.env   # the ONE sudo line
ls ~/.cache/river-build/iso-out/        # runink-river-<date>-x86_64.iso + .sha256
```

Stages 0–8 (profile copy, builder image, kernel + ZFS, payload + k0s airgap bundle,
component packages, `[runink]` repo, encrypted payloads for a private downstream build only,
pre-flight lints, root-stage file) are in the script header and
[docs/BUILD.md](docs/BUILD.md). **Nothing this project builds is reused between images**:
the kernel and ZFS (about an hour), every component package and every downstream payload are
rebuilt from pinned sources on every run. Only pinned third-party inputs are cached: the
builder image, the k0s airgap images (reused while they verify), the model weights, and a
model payload packed from the same `models.lock`. `MODEL_PAYLOAD=no` builds without models
(the public image always does; `images.yml` sets it). `--no-lint` skips stage 7.

A downstream image: `RIVER_PROFILE_DIR=<profile> RIVER_BRANDING_DIR=<branding>
RIVER_PAYLOAD_DIR=<payload> build/local-iso.sh`, then the printed `sudo` line
(`root-stage-<profile>-<variant>.env`). Alternatives: `make iso` (Artix with artools) and
`make iso-in-builder` (any podman host, privileged).

### Testing an image (not verified here: needs KVM, OVMF and a built ISO)

```bash
build/qemu-gui-test.sh ~/.cache/river-build/iso-out/runink-river-<date>-x86_64.iso   # full graphical install
build/qemu-test.sh     ~/.cache/river-build/iso-out/runink-river-<date>-x86_64.iso   # live medium only
```

`qemu-gui-test.sh` installs through every installer screen unattended, reboots, unlocks,
logs in at SDDM and prints `RIVERTEST OK|FAIL <check>` per check (the full list is the
`want=` line near the end of the script; screenshots under
`~/.cache/river-build/qemu-gui-test/<time>/`). For a downstream **server** image:
`RIVER_PROFILE_DIR=<the staged branded profile> build/qemu-gui-test.sh --profile server --offline <iso>`
(see Traps). A profile may add its own installed-system checks: every executable
`<profile>/tests/installed.d/*.sh` runs on the installed VM after the harness's checks, declares
its check names in a `# RIVERTEST-CHECKS: a b` header (required; the verdict requires each one
plus `hook-<name>`), and prints `RIVERTEST OK|FAIL|SKIP <check>`; `RIVER_HOOK_*` variables reach
it ([docs/BUILD.md](docs/BUILD.md), "Installed-system checks"; contract in
`build/qemu-hooks.sh`, Tier 1 test `river test installed-hooks`). Downstream checks live in the
downstream profile, never here.

## 5. Invariants (a change that breaks one needs a TSC vote, see GOVERNANCE.md)

1. **s6, never systemd.** No `.service` units, no `systemctl`. Services are s6-rc
   oneshots/longruns under `iso-profiles/*/root-overlay/etc/s6/`; the desktop's services
   (SDDM, Bluetooth, printing) come from their `-s6` packages. Long-running services that
   are not the desktop start from `rc.local` in init context, so elogind never reaps them.
2. **Exactly one kernel**, `linux-runink` (`build/pkgbuilds/runink-kernel`): one pinned
   zen-kernel stable tag, kernel.org tarball + zen patch, both sha256-pinned and
   signature-verified, a build that refuses any other kernelrelease or a config below
   `config.require`. OpenZFS (`build/pkgbuilds/runink-zfs`) is bumped together with it and
   stays a **separate out-of-tree module package**
   ([docs/governance/ZFS-LICENSING.md](docs/governance/ZFS-LICENSING.md)).
3. **ZFS root, encrypted.** New pools are an aes-256-gcm encryption root; every dataset
   inherits it. Never create a dataset with `encryption=off`. The key exists in memory
   and is printed once as the recovery key; it is never written to a file. The ESP is
   mounted at `/boot` and is the only unencrypted filesystem. `/etc/hostid` is generated
   per machine and baked into the initramfs. The pool's datasets beside the boot environment
   (`/home`) are mounted at boot by the `zfs-mount` oneshot, which the display manager
   depends on. See [docs/ENCRYPTION.md](docs/ENCRYPTION.md).
4. **Secrets are handled like `/etc/shadow`.** 0600 files in 0700 directories, owned by
   their one reader. Every secret or machine-state path is listed in
   `usr/local/bin/river-perms` (run at every boot by its s6 oneshot); add yours there. No
   secret, key, token or model is ever baked into an image. The one way models or
   downstream payloads travel with a medium is encrypted NEXT to the image
   ([docs/MODEL-PAYLOAD.md](docs/MODEL-PAYLOAD.md), [docs/PAYLOADS.md](docs/PAYLOADS.md)),
   whose passphrase is never on the medium. The public Runink River image carries no payload.
5. **Pinned, signed software supply.** Everything on the image comes from the pinned
   repositories (and the `[runink]` packages built here from pinned sources). A developer
   workstation carries its tools (`git`, `curl`, `base-devel`, `go`), but no second software
   supply chain: no app store or Flatpak, no container runtime in the base image
   (`forbidden.closure`, `forbidden.explicit`), no telemetry.
6. **The manifest is an allow-list.** `Packages-Root` is curated; never add a package
   *group*. `river lint closure` and the profile's `forbidden.closure` check its
   closure, and `river lint profile-manifest` checks that every package `profile.yaml`
   installs is on it (and, for a profile with its own `common.yaml`, that the rootfs is
   exactly `Packages-Root`); the live-only layer is exactly `Packages-Live`.
7. **Default-deny firewall, on every machine from first boot.** `inet runink_fw`
   (`runink-fw`, an s6 oneshot) is loaded before NetworkManager and sshd, on the live medium
   too: input lets in only loopback, established/related, ICMP and ICMPv6 essentials, DHCP
   and DHCPv6 replies, mDNS and the ports opened on purpose (`/etc/runink/fw-open`; SSH is
   closed until it is listed there); output is open, so a laptop keeps its LAN; forward is
   default-drop except the interfaces listed in `/etc/runink/fw-forward`. It never flushes
   the ruleset (`river lint firewall`).
8. **Untrusted code runs under `river-sandbox`.** Anything the machine compiles or executes
   on behalf of someone it does not trust (a checkout's build, model-written code), and
   every future `riverd` step, runs through `/usr/local/bin/river-sandbox`. Never widen its
   deny-list (`/etc/runink`, runner credentials, the `runink` user's secret dirs, k0s
   state), and never call `bwrap` directly to get around it.
9. **Pin every upstream** by version *and* checksum (and signature where the upstream
   signs): mirror snapshots, kernel, OpenZFS, k0s, container base images, GitHub Actions
   (by full commit SHA).

These invariants were rewritten for the workstation on 2026-09-26, when the server profile
left this repository: the former "IPv6-only cluster" invariant moved to the downstream
distributions that run a cluster, and the firewall and fetch-tooling invariants were
restated for a desktop. Changing them again needs the TSC's sign-off (GOVERNANCE.md).

### Conventions every change keeps

- **Go, not shell, for internals** (decided 2026-09-28; [docs/GO-CLI.md](docs/GO-CLI.md)). New
  logic is Go: the plain pipeline pattern in `pkg/pipe` (standard library: `os/exec`, `io`,
  channels) instead of `a | b | c`, and `os`/`io/fs` instead of `cp`/`mv`/`install`. Every
  command line is a subcommand of the one `river` CLI (`cli/`, cobra + viper: each flag also
  reads `RIVER_<FLAG>` and the config file). Shell stays only where a tool demands it, and only
  as a one-line call into `river`: PKGBUILDs, s6 `run` files, CI `run:` lines, and the one
  `sudo` line. `river lint shell-ratchet` fails on any NEW shell script, and on a ported one
  still listed in `scripts/shell-ratchet.txt`; the list only shrinks.
- Shell that still exists (until ported): `#!/bin/sh` POSIX where possible, `bash` only when
  needed, `set -eu`, clean under `shellcheck --severity=warning`.
- **No Python** anywhere in the build path or the image (`river lint python-purge`).
- **mistral.rs is the only inference engine** the tree names (`river lint one-engine`
  rejects any other engine's name outside its allow-list).
- The installer steps exist twice (`installer/lib/` and the profile's
  `root-overlay/usr/local/lib/runink-install/`): change both, byte-identical
  (`river lint installer-sync`). A step runs only if a step list names it (the edition
  descriptor in `live-overlay/usr/share/river/installer/editions/` and `runink-install`):
  add a new step to both, in order (`river lint edition-steps`; a step only a downstream or
  cloud image runs goes in `scripts/edition-steps.allow` with its reason).
- The installer's hardware probe and planner (`installer/{hwprobe,plan,internal}`,
  `river-hwprobe` / `river-plan`) are Go **standard library only** and build with
  `GOAMD64=v1`, so they run on the CPUs they refuse. Their CLI and JSON output are a
  contract ([docs/INSTALLER-HARDWARE.md](docs/INSTALLER-HARDWARE.md)); planner decisions
  are pinned by the table-driven tests on fixture probes, and CI runs them (`installer-go`).
- The installer never erases a disk the operator has not confirmed by serial, and the boot
  medium is never a target. `install.sh` verifies the release signature and fails closed
  while `KEYS` has no fingerprint; keep both properties.
- `guide/` is **river-guide**, the install guide agent for a LIVE medium (stdlib Go,
  `GOAMD64=v1`): grounded Q&A over guide bundles (the public Runink River guide is built
  in; a downstream may add a private, ed25519-signed bundle fetched after device-flow
  sign-in, held in RAM only), the install step machine (it never executes a destructive
  step), and the optional `@river_install` issue channel. It is built with every image
  (`build/pkgbuilds/river-guide`), but the Runink River medium does not carry it: its
  model is 1.1 GB and its console front end has no place beside the graphical installer.
  A profile ships it by carrying a `guide-model.lock` equal to `guide/model.lock` (the
  pinned model) and the `river-guide-model` s6 longrun and getty override in its
  `live-overlay/`; `build/local-iso.sh` then stages the model into it. `20-clone-rootfs`
  strips it from installed machines.
- Branding: edit the sources under `branding/` and re-render; never hand-edit an overlay
  copy (`river lint branding-sync`). Colours come from `branding/palette.md` only
  (dark-first, one sage accent; the old warm palette is retired and the lint rejects it).
  Text in artwork is outlined by `branding/render.sh` in outline mode, never rendered live.
  The Runink River community mark (the M2 mascot `branding/mascot/river-mascot.svg`, its one
  source; the marks generated from it, `branding/logo/river-mark*.svg`, `river-lockup*.svg`;
  `runink-tagline.svg`, `branding/ascii/`) and every render of it are Runink brand
  assets (`LicenseRef-Runink-Trademark`, all rights reserved); a community build swaps them
  ([docs/LICENSING.md](docs/LICENSING.md) §5).
- Every file carries an SPDX header or a `REUSE.toml` entry (`river lint reuse`, in Tier 1 and `reuse.yml`).

### Tests

Every behaviour change comes with a check that would fail without it: a lint rule, an
assertion in `tests/assert-golden.sh` (runs on a booted target), a smoke check in
`tests/smoke-k0s.sh`, a `RIVERTEST` check in a QEMU harness, or a Tier 2 VM test. A guard
that can pass having examined nothing (an empty file list, a missing tool) must fail
instead; several of the existing linters assert their own inputs for exactly that reason.

### Security guardrails

- Never commit credentials, private keys, tokens, enrollment files, real IP addresses,
  internal hostnames or personal data. Use documentation ranges (`192.0.2.0/24`,
  `203.0.113.0/24`, `2001:db8::/32`) and `example.org` in examples.
- Workflows: least-privilege `permissions:`, actions pinned by SHA,
  `persist-credentials: false`, no `pull_request_target`. This public repository runs only on
  GitHub-hosted runners (`ubuntu-latest`, or `ubuntu-24.04` where a job needs that
  release); no job ever runs on a self-hosted runner, and no workflow names a self-hosted
  label. Pull requests from forks run the same jobs once a maintainer approves the run
  ("Require approval for all external contributors"), with a read-only token and no
  secrets. Jobs that write (Pages deploy, releases) never run on `pull_request`
  ([docs/governance/CI.md](docs/governance/CI.md), "Runners").
- Do not add third-party AI SDKs, telemetry or phone-home behaviour to the image.
- Report vulnerabilities privately ([SECURITY.md](SECURITY.md)), never in a public issue.

## 6. How changes ship

1. Branch (or worktree) from `main`; sign off every commit: `git commit -s` (`dco.yml`
   rejects a pull request with an unsigned commit).
2. Open a pull request. CI runs on GitHub-hosted runners (a fork's pull request once a
   maintainer approves the run): `ci.yml` (`tier1`, `installer-go`,
   `river-guide`, `go-security`), `reuse.yml`, `dco.yml`, and `docs.yml` for website changes.
   Changes that touch boot, the installer, ZFS or the kernel also need a VM boot, which
   does not fit a GitHub-hosted runner: build with `build/local-iso.sh` and run the QEMU
   harnesses (`tests/tier2-boot-smoke.sh`, `build/qemu-gui-test.sh`) yourself. Say in the PR
   how you tested.
3. A maintainer merges (squash). Agents never merge, push to `main`, force-push shared
   branches, change repository settings or publish releases.
4. Pushes to `main` publish the website to GitHub Pages.
5. Kernel/ZFS packages built in CI: dispatch `kernel-build.yml`, then `zfs-build.yml` with
   its run id.
6. Images and releases: `images.yml` builds the public ISO (`MODEL_PAYLOAD=no`) on a dispatch
   or a pushed `runink-os-*` tag. A release follows [RELEASE.md](RELEASE.md): a DRAFT release
   carrying the ISO, `SHA256SUMS` and the offline signature `SHA256SUMS.asc`, then
   `release-gate.yml`, the ONLY path to a public release (verify, scan, attest, publish; run
   it with `dry_run: true` first).
   The `[runink]` package repository is published separately, from a public build, after the
   owner signs it offline: `river repo assemble`, `base/river-sign`, `river repo verify`,
   `river repo publish` ([docs/REPOSITORY.md](docs/REPOSITORY.md)). Agents never sign or publish it.
7. Downstream distributions pick a change up by moving their pinned Runink River commit.

## 7. Traps

- **`/tmp` is often a tmpfs.** Filling it kills processes with no disk-full error (exit
  144, "Page crashed"). Every build path defaults to `~/.cache`; keep it that way, and run
  `df -h` before a build.
- **One heavy job at a time.** A running ISO build holds podman, the builder image and root's
  podman storage: do not start a second `local-iso.sh`/`iso-root-stage.sh`, and do not start
  two VMs at the same moment (a fresh VM once failed in OVMF with "USB HARDDRIVE … Not Found"
  while another started; a rerun booted). Stop processes by PID or with a bracketed pattern
  (`pkill -f '[q]emu-system'`): an unbracketed `pkill -f` matches your own shell and kills it.
- **`qemu-gui-test.sh --profile server`** refuses to run without `RIVER_PROFILE_DIR` (point
  it at the staged, branded copy under the downstream's state directory, not the source
  profile) and, without `--offline`, gives the guest no NIC at all, so k0s never starts and
  `fb-k0s` fails by design. `fb-finish` can report "kiosk user left" while `golden` (run
  later) shows the kiosk gone: a timing race, not a defect.
- **An old ISO under a new tree.** Against an ISO built before a change, `qemu-gui-test.sh`
  copies this tree's installer and overlay onto the live system and lists every file on the
  serial log: that result is not the ISO's own. Rebuild before claiming an image passes.
- **`tests/assert-golden.sh` and [docs/GOLDEN-IMAGE.md](docs/GOLDEN-IMAGE.md) describe a
  downstream server node**, not the workstation; the workstation is checked by
  `qemu-gui-test.sh`'s workstation checks. `make vmtest` is a manual scaffold.
- **`make iso-in-builder PROFILE=<x>`**: the inner `make` re-reads the Makefile, so the
  target passes `PROFILE` explicitly. Keep it that way.
- **`river lint k0s-pin` reads `localrepo/`**: a stale package there once shipped an old k0s.
  `make iso` runs it; don't bypass it.
- **Edit branding and installer steps at their source**, never the overlay copy; the sync
  lints fail otherwise.
- **Model measurements taken on a small development host are not sizing evidence**; rows in
  `validation/SELECTION.md` measured on another engine are marked NOT EVIDENCE. The model set is
  pending a benchmark on the target hardware.
- **Squash merges of stacked PRs lose work.** After a merge wave, diff every merged branch
  against `main` (`git rebase origin/main` saying "patch contents already upstream" is the
  reliable check; `git merge-base --is-ancestor` calls every squashed branch unmerged).
- **Public repository.** No downstream product names, internal addresses, runner labels or
  hostnames in code, docs, commit messages or PR text. `river lint public-leak` (Tier 1)
  checks the tree; its names are SHA-256 digests, and a false positive goes in
  `scripts/public-leak.allow` with a reason. `--history` checks every commit, message and
  identity ([docs/PUBLICATION.md](docs/PUBLICATION.md)).
- **`river lint one-engine` scans docs too**: naming a second inference engine in any tracked
  file (outside its allow-list) fails Tier 1, even in a sentence saying it is not used.
- **Never `git stash`** in a clone with several worktrees: the stash is shared by all of them,
  and other agents' work gets swapped.

## 8. Deeper docs

| Doc | Read for | Status |
|---|---|---|
| [docs/BUILD.md](docs/BUILD.md) | the build stages, downstream profiles and branding, pins, reproducibility | current |
| [docs/PAYLOADS.md](docs/PAYLOADS.md), [docs/MODEL-PAYLOAD.md](docs/MODEL-PAYLOAD.md) | the payload and first-boot contract, encrypted payloads | current; they describe what downstream server media carry |
| [docs/INSTALL.md](docs/INSTALL.md), [docs/INSTALLER-HARDWARE.md](docs/INSTALLER-HARDWARE.md) | installing; the probe/planner contract | current |
| [docs/ENCRYPTION.md](docs/ENCRYPTION.md), [docs/SECURE-BOOT.md](docs/SECURE-BOOT.md) | ZFS encryption, Secure Boot | current |
| [docs/KERNEL.md](docs/KERNEL.md), `build/pkgbuilds/runink-kernel/README.md` | the kernel pin and config policy | current |
| [docs/OWN-BASE.md](docs/OWN-BASE.md), [docs/OWN-BASE-SCOPE.md](docs/OWN-BASE-SCOPE.md), [docs/ARCH-BASE.md](docs/ARCH-BASE.md) | the from-source base plan | plan; their `iso-profiles/runink-workstation` is today's `iso-profiles/river` |
| [docs/INSTALL-GUIDE-AGENT.md](docs/INSTALL-GUIDE-AGENT.md) | river-guide design and threat model | current |
| [RELEASE.md](RELEASE.md), [docs/RELEASE-SIGNING.md](docs/RELEASE-SIGNING.md), [docs/VERIFY.md](docs/VERIFY.md) | cutting, signing, verifying a release | current |
| [docs/REPOSITORY.md](docs/REPOSITORY.md) | the public signed `[runink]` repository: mirrors, `river repo assemble/verify/publish`, what is never published | current |
| [docs/governance/CI.md](docs/governance/CI.md) | CI tiers and runner safety | current |
| [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md), [docs/GOLDEN-IMAGE.md](docs/GOLDEN-IMAGE.md), [docs/CLOUD-IMAGES.md](docs/CLOUD-IMAGES.md) | the server-era architecture, node contract, cloud images | **server-era**: they describe a downstream server, not this workstation |
| [CHARTER.md](CHARTER.md) | the project charter | §1 still says "server distribution"; changing it is a TSC decision |
| [docs/PERFORMANCE.md](docs/PERFORMANCE.md), `bench/` | tuning and benchmark method | current |
| [CHANGELOG.md](CHANGELOG.md), [ROADMAP.md](ROADMAP.md) | what changed, what is next | current |
