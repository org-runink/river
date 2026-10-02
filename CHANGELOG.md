<!--
SPDX-FileCopyrightText: 2026 The RIVER Authors
SPDX-License-Identifier: MIT
-->

# Changelog

All notable changes to Runink River are recorded here: the server and workstation images,
their packages, the installer and (once it has code) the RIVER runtime.

## Convention

- The format follows [Keep a Changelog 1.1](https://keepachangelog.com/en/1.1.0/).
- Releases use calendar versions, `runink-os-YYYY.MM`, the tag and image name
  ([RELEASE.md](RELEASE.md)). A second release in the same month adds a micro level,
  `runink-os-YYYY.MM.N`.
- Every pull request that changes something a user or operator would notice adds a line
  under `## [Unreleased]` ([CONTRIBUTING.md](CONTRIBUTING.md)). At release time the
  maintainer renames that heading to the release tag and date and opens a new empty one.
- Sections, in this order: **Security**, **Added**, **Changed**, **Deprecated**,
  **Removed**, **Fixed**. **Security** lists every fixed vulnerability with its CVE or
  GitHub advisory ID and the reporter's credit ([SECURITY.md](SECURITY.md)). It also lists
  upstream security bumps (kernel, OpenZFS, k0s) with the upstream advisory IDs. A release
  with no vulnerability fixes says "None." under **Security**, so readers can tell "none"
  from "not recorded".
- The GitHub release for a tag carries the same text.

## [Unreleased]

### Security

None.

## [runink-os-2026.10] - 2026-10-02

### Security

None. No vulnerability was fixed in this release, and no upstream security bump is
carried beyond what `runink-os-2026.09` already recorded.

### Fixed

- **An installed machine no longer boots to a black screen.** `runink-plymouth-quit`
  passed `--retain-splash`, which tells plymouthd to leave its last frame up and
  therefore NOT hand the VT back. tty1 stayed in graphics mode, SDDM's Xorg blocked
  forever in `VT_WAITACTIVE` on a switch the kernel could never complete, and the
  machine sat idle with nothing on the graphical console *or* any text console, the
  pending switch swallowing Ctrl+Alt+Fn. The flag works under systemd, where
  `plymouth-quit.service` is sequenced against the display manager and logind performs
  the hand-over; nothing under s6 does that. Proven A/B/A on one disk with the same
  cmdline. The cost of dropping it is a brief text console between splash and greeter —
  the exact thing the flag existed to avoid, and worth paying. It hid for so long
  because `console=ttyS0` masks it completely: plymouth then takes no VT, and every
  successful boot of an installed machine had been a serial one.
- **Time is synchronised.** Nothing on either image synchronised the clock: no
  `chrony`/`ntp`/`timesyncd` package, no service. The clock was set from the RTC at boot
  and drifted freely for the life of the node. An unsynchronised clock makes TLS
  certificates look not-yet-valid or expired, breaks k0s and Kubernetes client
  certificates and tokens the same way, and gets GitHub App JWTs refused with
  `"'Expiration time' claim ('exp') is too far in the future"`. Measured on a node built
  from the server profile: ~39 seconds ahead of GitHub, which failed every private-repo
  CI job. Now `chrony` + `chrony-s6`, with two NTS-authenticated sources and the public
  pool as an unauthenticated fallback, `makestep 1.0 3` so a badly wrong RTC is corrected
  in seconds while a running node never jumps under a database, and `rtcsync` so the next
  boot starts close before the network is up. Enabled in the **installed node's** boot
  bundle by `80-enable-s6`, which now fails the install if `chrony-srv` is not in the
  compiled `default` bundle — an installed-but-never-started time daemon is
  indistinguishable from none at all, and that is exactly how this was missed on a running
  node. The live medium is ephemeral and does not run it: the clock matters where TLS and
  Kubernetes certificates are checked over a machine's lifetime, not for the minutes an
  installer is on screen.
- **The graphical installer makes you prove you copied the recovery key.** It accepted a
  tickbox — "I have written it down" — and started the install. The key is shown exactly
  once, for a disk it is the only way to decrypt, so a mistranscribed character was
  discovered at the next boot, when nothing could be done about it. It now asks for three
  randomly chosen groups of the eight to be typed back and checks them against the key it
  generated, matching what the text installer has done since the live-fallback work.
  Spaces and letter case are ignored; a wrong answer changes nothing and leaves the key on
  screen; there is deliberately no attempt limit, because locking someone out of the one
  screen that shows the key would brick the disk being installed.

### Added

- **A boot entry an unbootable machine can be diagnosed from.** Every kernel now also gets
  a *"Runink (<kernel>) — verbose, serial console"* entry: same kernel, same initramfs, no
  splash, `loglevel=7` and `console=tty1 console=ttyS0,115200n8`. The black screen above
  could only be *characterised*, not diagnosed, because the installed system had no way to
  say anything — the splash hid the log and there was no serial console to watch. Generated
  by `/etc/grub.d/11_runink_debug`, which re-resolves the ESP and re-globs the kernels on
  every `grub-mkconfig` so it survives a kernel upgrade; numbered after `10_linux`, so the
  normal entry stays first and `GRUB_DEFAULT=0` is unchanged. No serial getty is started
  with it: the boot log needs no login, and an `agetty` respawning against a port that does
  not exist would be noise on every machine without one.

- `runink-grub-live` (`build/pkgbuilds/runink-grub-live`), Runink River's own live-medium
  GRUB scaffolding, replacing Artix's `artix-grub-live` in `Packages-Live`. It provides
  exactly the three paths artools' `prepare_grub()` reads out of the livefs layer —
  `usr/share/grub/cfg/*.cfg` plus the `locales/` and `tz/` directories — with no `artix-*`
  dependency. The live boot menu is unchanged: it was already the profile's own
  `iso-profiles/river/grub/grub.cfg` + `kernels.cfg`, which `scripts/patch-artools.go`
  copies over the top. `loopback.cfg` (multiboot sticks) is ours now too; Artix's
  interactive clock/locale/keymap/timezone menu, its `defaults.cfg` and its `variable.cfg`
  were already being overwritten or deleted on every build, and are gone.

### Changed

- **The login shell is bash, and the terminal greeting no longer dances.** The admin the
  installer creates and the live user both get `/bin/bash`; `fish` is dropped from the image
  entirely. The greeting moved from a fish function to
  `/etc/profile.d/runink-greeting.sh`, so it belongs to the login rather than to one shell,
  and it is now a **static** one-shot `fastfetch` summary — the six-frame arrival animation
  and its rendered frames are gone. `RUNINK_GREETING=off` still turns it off completely.
  It prints **only in an interactive shell**: `/etc/profile.d` is also sourced by the login
  shells `scp`, `sftp` and `ssh host command` start, and a banner on those streams corrupts
  the transfer, so the greeting checks `$-` before writing anything. The installed-system
  test now asserts both halves — that an interactive shell prints the summary, and that a
  non-interactive one prints nothing at all.

### Removed

- **`plasma-workspace-wallpapers`** (255 MiB): the image ships its own wallpapers — the
  RIVER default, the `runink-river` SDDM theme and fourteen vendored Emerald JPEGs. Nothing
  in the closure depended on it. KDE's stock wallpapers no longer appear in the picker.
- **`noto-fonts-extra`** (331 MiB): all 84 of its font families are already in `noto-fonts`;
  it adds only extra widths and weights of them, so no script loses coverage.
- **`kdeplasma-addons`** (52 MiB over 5 packages): extra plasmoids and wallpaper plugins the
  default desktop layout never loads. It was the only thing putting `qt6-quick3d`, `openxr`,
  `qt6-quicktimeline` and `jsoncpp` into the closure. Those widgets are no longer offered in
  "Add Widgets".
- **`mkinitcpio-nfs-utils`**: NFS-root tooling for mkinitcpio's `net` hook, which no
  initramfs this image builds names.

## [runink-os-2026.09] - 2026-09-29

### Security

- The old fetch script under `install/` is removed. It downloaded the latest release ISO with no
  signature check (a sha256 file from the same release at most) and was meant to run under
  `sudo bash`; `install.sh` is the one entry point and fails closed without a valid
  release signature.

### Added

- **The signed `[runink]` package repository** ([docs/REPOSITORY.md](docs/REPOSITORY.md)).
  Installed systems now get `[runink]` in `/etc/pacman.conf` with
  `SigLevel = Required DatabaseRequired` and `/etc/pacman.d/mirrorlist-runink`: the rolling
  `repo-x86_64` GitHub release first (complete), runink.org second (a partial mirror without
  the packages over 100 MiB). The image ships the release key as a pacman keyring
  (`runink.gpg`, `runink-trusted`) and the installer trusts it with
  `pacman-key --populate runink`; the build-time `file://` repository never reaches a node.
  New commands: `river repo assemble` (River's own packages only; refuses a payload-carrying
  `runink-core` or `runink-runtime`, any other package, and private build directories),
  `river repo verify` (every package and the database signed by the release key, fail
  closed) and `river repo publish` (GitHub release and Pages mirror; refuses unless verify
  passes; never signs). The release public key is now in the tree,
  `base/keys/owner/95C0A7B97D547413E42660DDB06FE75626F15BF3.asc`, so `base/river-sign`
  verifies against it alone.
- **`river lint public-leak`** (Tier 1): no downstream product name, private repository,
  private-range address, runner label, target machine or personal mailbox in the public
  tree. Names are matched by SHA-256 digest, so the lint does not publish them; exceptions
  live in `scripts/public-leak.allow`, each with a reason, and a stale one fails.
  `--history` runs the same rules over every commit, message and identity.
- [docs/PUBLICATION.md](docs/PUBLICATION.md): the owner's step-by-step to publish, given that
  the repository is already public with its full history.
- **The `river` command line, and Go instead of shell for River's internals**
  ([docs/GO-CLI.md](docs/GO-CLI.md)). `pkg/pipe` (standard library only) is the Go pipeline
  pattern that replaces shell pipelines and loops: command pipelines that fail like
  `set -o pipefail`, and channel stages that stop on the first error. `cli/` is one `river`
  binary (cobra + viper, vendored): every flag also reads `RIVER_<FLAG>` and the config file.
  First port: `scripts/lint-one-engine.sh` is now `river lint one-engine`. New lint:
  `river lint shell-ratchet` fails on any new shell script, and `scripts/shell-ratchet.txt`
  (119 scripts today) only shrinks as ports land.
- **`river lint reuse`, a Go port of `reuse lint`** (REUSE 3.3; standard library only). It
  reads SPDX headers, `.license` sidecars and `REUSE.toml` annotations (globs, nested files,
  closest/aggregate/override precedence), parses every SPDX expression and checks `LICENSES/`
  for missing, unused, unknown, deprecated and extensionless licences, against the embedded
  SPDX License List 3.29.0. It lints the tracked files, prints a `reuse lint`-style report and
  exits non-zero on any problem. `reuse.yml` and Tier 1 run it as a plain process, so the check
  no longer needs a container (or Python) and runs on GitHub-hosted `ubuntu-latest`. It
  agrees with `reuse lint` on this repository's history (971/971 files at 17a391d, 418/418
  at e8f46f7).
- Ported to `river lint`: `lint-shell.sh` (`river lint shell`; `--strict --severity=warning`
  is Tier 1's shellcheck step), `lint-installer-sync.sh`, `lint-aur-sync.sh`,
  `lint-firewall.sh` and `lint-docs-vendor.sh`, with the same checks and messages.
- **Profile installed-system checks in `qemu-gui-test.sh`.** A profile's executable
  `tests/installed.d/*.sh` run on the installed VM after the first boot and the harness's own
  checks; each declares its checks in a `# RIVERTEST-CHECKS:` header, and the verdict requires
  them (plus `hook-<name>` per hook). A new result, `RIVERTEST SKIP`, is listed without failing;
  `RIVER_HOOK_*` variables reach the hooks (`build/qemu-hooks.sh`, `docs/BUILD.md`). A
  downstream profile's `tests/` directory is now part of the harness contract.
- **The release gate** (`.github/workflows/release-gate.yml`): a release is a draft until it
  passes, and the gate is the only thing that publishes it. It checks the checksums and the
  offline signature (failing closed while `KEYS` has no fingerprint), SBOMs the image contents
  and the source, checks the distribution packages against the Arch Linux security tracker
  (`build/tools/archaudit`: fixable High/Critical advisories block, waivers expire) and the
  image's Go programs with govulncheck, scans every file the image's authors added or changed
  for secrets (gitleaks), then attests (SLSA provenance, Sigstore) and publishes with the
  evidence attached. `docs/VERIFY.md` shows users how to check a release. On the current ISO:
  981 packages, 0 blocking advisories; 9 Go programs clean; 2,165 own files, no secret.
- A documentation website (`website/`, Hugo with the vendored Hextra theme): getting
  started, features, configuration, the kernel on Arch, security, FAQ and roadmap. `make docs`
  builds it offline; `docs.yml` publishes it to GitHub Pages from `main`.
- **A terminal greeting with the Runink River mark and the machine summary** (like CachyOS's,
  in the Runink brand). fish is the login shell of the live user and of the admin the installer
  creates (`installer/lib/50-runink-user.sh`, `scripts/patch-artools.go`); every interactive
  fish prints fastfetch's summary (`/etc/xdg/fastfetch/config.jsonc`: OS, kernel, init, packages,
  desktop, CPU, GPU, memory, disks, local IP, battery, locale) beside the community mark drawn
  in truecolor half blocks. The first shell of a terminal plays the mark's arrival first: six
  frames, about 0.2 s, the raft settling on the water; nested
  shells, pipes and `RUNINK_GREETING=static` skip it, `RUNINK_GREETING=off` drops the greeting.
  Every module reads this machine only (the lint rejects a network module: invariant 5).
  `branding/render.sh` renders the logo and the frames (`branding/fastfetch/`);
  `lint-branding-sync` checks them; `qemu-gui-test` adds `greeting` and `no-base-name`.
- An animated Runink River sign-in screen: the SDDM greeter theme `runink-river` (Qt 6)
  replaces Breeze. The community mark comes alive: the raft, with the dog on it, bobs gently
  on the water and the wave lines drift, in a slow loop that pauses after a minute without
  input, during a failed sign-in and when `motion=false` is set in its `theme.conf`. The theme
  has user tiles, or a user name field when SDDM lists no users, a password field, a session
  picker (default Plasma (Wayland)), a keyboard-layout indicator, a Caps Lock warning, a
  failed sign-in message, suspend, restart and shut down, and a clock. It can be used entirely
  from the keyboard, with a 3 px sage focus ring, and scales from 1024x768 to 4K. The primary
  screen shows the form, and every other screen shows the mark. After a sign-in the form fades
  out and the mark keeps bobbing as it glides to where the Plasma start-up splash draws it.
  The splash now shows the same animated mark, so the hand-off into the session is continuous.
  The mark's layers are generated from `branding/logo/river-mark.svg` by `branding/render.sh`.
  `tests/sddm-theme.sh` renders every state offscreen.
- `riverbench compress` (`bench/analytics`): compression, file access and huge pages
  measured at every layer (ZFS records, zram pages of real process memory, kernel image,
  initramfs and modules, Parquet files, `O_DIRECT`/`io_uring` reads, THP for columnar
  scans) on analytics data shapes, with declared rules that pick a default per layer and
  dataset, root-only on-box runs (`zfs-pool`, `zram-pressure`) that confirm them, and
  results as OpenTelemetry metrics: OTLP/HTTP (protobuf or JSON) and a Prometheus text
  file, both off unless the operator passes a destination, plus a JSON report and a JSON
  `compare` for automated review ([docs/PERFORMANCE.md](docs/PERFORMANCE.md)).
- Hardware-adaptive install: `river-hwprobe` inventories the machine and `river-plan`
  turns the inventory into an install plan (RAM budget, model tiers, ZFS layout), and
  refuses machines below the documented minimums ([docs/INSTALLER-HARDWARE.md](docs/INSTALLER-HARDWARE.md)).
- `install.sh`: a one-line download, signature and checksum check and USB write. It fails
  closed until the release key's fingerprint is published.
- `river-guide`, the offline install guide agent on the live medium
  ([docs/INSTALL-GUIDE-AGENT.md](docs/INSTALL-GUIDE-AGENT.md)).
- Opt-in LAN installs: one operator machine (`river-pair`) installs other machines on the
  same link, but only live installers whose owner chose "Let another machine install this
  one" (`river-pair-announce`): the operator types the one-time pairing code shown on
  that screen, and its owner answers `y` there. Link-local announcements, an HMAC pairing
  keyed by the code, a per-session SSH host key and a restricted sshd with a fixed command
  grammar; secrets are streamed, never written to disk. `runink-install` gains the menu
  ([docs/INSTALL.md](docs/INSTALL.md#lan-installs)).
- `models.lock` pins the AI models a node may serve, by upstream revision, size and
  sha256, with model-card evidence (`models.evidence.json`). A validation suite
  (`validation/`) measures candidate models against per-tier gates
  ([validation/SELECTION.md](validation/SELECTION.md)).
- `river-sandbox`: bubblewrap confinement for code a node compiles or runs on behalf of
  others.
- Native aes-256-gcm encryption for every ZFS dataset, the ESP at `/boot`, shadow-grade
  file modes re-asserted on every boot (`river-perms`) ([docs/ENCRYPTION.md](docs/ENCRYPTION.md)).
- The Runink River Workstation profile (KDE Plasma) on the same base.
- On Runink River: a default-deny host firewall (`runink-fw`: replies, DHCP, ICMP and mDNS in,
  SSH closed until listed in `/etc/runink/fw-open`, forward only for `/etc/runink/fw-forward`),
  `river-sandbox`, `river-perms` and kernel hardening sysctls, on the installed machine and the
  live medium.
- Go security analysis in CI (gosec, govulncheck) and the race detector for the installer's
  Go tests.
- An optional encrypted model payload on the server medium: the `models.lock` set, zstd
  compressed and AES-256-GCM encrypted under a build-time passphrase that never goes on the
  medium, unpacked by the installer into a natively encrypted `<pool>/models` and checked
  file by file against the lock (`river-modelpack`, step `72-models-payload`,
  [docs/MODEL-PAYLOAD.md](docs/MODEL-PAYLOAD.md)).
- Air-gapped installs: the server needs nothing from any network. `runink-k0s-airgap`
  carries k0s's own system images (pinned by digest in `build/k0s-images.lock`), imported
  and pinned by k0s before kubelet starts; `k0s.yaml` pulls `IfNotPresent`.
- Public and private server images: the public `runink-river-server-<date>` ISO carries no
  payload (the build refuses one); a private `runink-river-server-<variant>-<date>` ISO may
  carry generic encrypted downstream payloads (`river-payloadpack`, `/river-<kind>/<group>/`),
  which step `73-downstream-payloads` copies onto the node still encrypted, and a
  `/usr/local/lib/runink/firstboot.d/` hand-off (`river-firstboot-hooks`)
  ([docs/PAYLOADS.md](docs/PAYLOADS.md)).
- `build/qemu-test.sh --offline`, `--expect-payloads`, `--public`, `--setup-answers`.
- `runink-autoinstall --setup-answers FILE` (step `76-setup-answers`): a downstream's setup
  answers for its first-boot hooks, 0600, shredded once they have all succeeded.
- `models.lock` pins the vision model (Qwen3-VL-2B-Instruct Q4_K_M, its mmproj and its
  tokenizer/config assets) and five piper voices (en_GB, es_ES, fr_FR, pt_BR, pt_PT).
- `build/local-iso.sh`: a workstation ISO build that runs every stage except `buildiso` as
  the user (kernel and ZFS in rootless podman, verified by `build/verify-kernel-zfs.sh`),
  then prints the one `sudo` command (`build/iso-root-stage.sh`).
- `build/qemu-test.sh`: an unattended UEFI boot, install and golden test of an ISO under
  QEMU/KVM, as the user.
- `build/make-multiboot-usb.sh`: one stick with both ISOs, a GRUB menu and an exFAT data
  partition, guarded by the stick's serial and size ([docs/USB-MULTIBOOT.md](docs/USB-MULTIBOOT.md)).
- Network first: `river-netsetup` (with `river-netcheck`, in `runink-installer`) is the first
  action of every live session and step 0 of `runink-install` on both images. It sets up
  wired, Wi-Fi or static IPv4/IPv6 networking (or `river.net=` / a config file headless),
  checks gateway, DNS and IPv4/IPv6 internet reachability, and records online, lan-only or
  offline in `/run/river/net-state.json` ([docs/INSTALL.md](docs/INSTALL.md#network-first)).
  `build/qemu-test.sh --net user` checks it on a QEMU user-mode NIC.
- **Incremental model payload packing** ([docs/MODEL-PAYLOAD.md](docs/MODEL-PAYLOAD.md),
  "Packing a large set incrementally"): `river-modelpack pack --append` packs the lock rows
  present in `--models-dir` that the payload does not hold yet, and `--consume` deletes each
  source file once its ciphertext is written, fsynced, re-verified and recorded, so a set close
  to the build host's free space fits in one payload. `river-modelpack pending` lists what is
  left. The finished payload is format `river-modelpack 1` and opens with the same `unpack`; an
  unfinished one carries an `incomplete` journal that `check`, `unpack`, `build/local-iso.sh`
  and `build/iso-root-stage.sh` all refuse. Interrupted calls resume.
- `build/models-fetch.sh`: `HF_TOKEN_FILE` (an upstream read token, passed to curl as a header
  file in a private directory, never to the mirror) and `MODELS_ONLY` (fetch a subset of the
  lock by dest path).
- `build/iso-root-stage.sh`: `ISO_OUTDEV=/dev/disk/by-id/usb-…` with `ISO_OUTDEV_CONFIRM` writes a
  private image with payloads straight onto a removable USB disk (guarded: whole disk,
  removable, unmounted, large enough, confirmed), checks it with `xorriso -check_media` and
  writes the sha256 of the image's length to `OUT_DIR/<name>.sha256`.
- Installer step `72-models-payload`: required mode, `RUNINK_MODELS_REQUIRED=1` or an edition
  descriptor's `"models_required": true`. No payload, a blank passphrase, an unattended install
  without one, or a skip then FAILS the install instead of deferring the models. The step also
  checks that the pool has room for the set before it reads the medium.
  `river test models-required` and `river test models-fetch` (Tier 1) pin both scripts'
  behaviour.
- The install planner accepts **extra model tiers** (for example `imagegen`, `rerank`, `guard`):
  a tier that is not built in is valid when `models.lock` pins a role of that name, and is
  placed as an optional tier after the built-in ones
  ([docs/INSTALLER-HARDWARE.md](docs/INSTALLER-HARDWARE.md), "Model tiers").

### Changed

- **The host-side tests are `river test` subcommands** ([docs/GO-CLI.md](docs/GO-CLI.md)).
  `tests/firstboot-hooks.sh`, `tests/memtune.sh`, `tests/external-profile.sh`,
  `tests/installed-hooks.sh` and `tests/sddm-theme.sh` are now `river test firstboot-hooks`,
  `memtune`, `external-profile`, `installed-hooks` and `sddm-theme <out-dir>`, with the same
  checks and messages. Tier 1 runs the first four with the `river` binary it builds. The
  scripts that run on a booted target or in a VM stay shell for now.

- **CI runs only on GitHub-hosted runners.** This public repository never runs a job on a
  self-hosted runner; the self-hosted runner plan from #160 is withdrawn. Tier 1 is back in its
  rootless, `--network=none` container for every event; the fork-only twin jobs are merged
  back into one job each (fork runs wait for a maintainer's approval, with a read-only token
  and no secrets); Scorecard publishes its results again. The heavy workflows fit a standard
  runner by removing unused preinstalled toolchains first and checking the disk they need
  (`build/ci-runner-prep.sh NEED_GB`). Tier 2 (`tier2-vm.yml`) does not fit and is
  `workflow_dispatch` only; maintainers run the VM boot locally. `.github/actionlint.yaml`
  is gone ([docs/governance/CI.md](docs/governance/CI.md), "Runners").
- **Five more lints are `river` subcommands** ([docs/GO-CLI.md](docs/GO-CLI.md)):
  `river lint k0s-pin` (still reads `LOCALREPO`), `river lint profile-manifest`,
  `river lint python-purge`, `river lint branding-sync` and `river lint closure` (still reads
  `LINT_CLOSURE_MODE`, or `--mode`) replace `scripts/lint-{k0s-pin,profile-manifest,python-purge,branding-sync,closure}.sh`,
  with the same checks, messages and exit status. Tier 1, the Makefile, `build/local-iso.sh`
  and `branding/render.sh --check` call them.
- **The installer applies the hardware plan's ZFS ARC and zram sizes.** Step `30-target-config`
  (`installer/lib/memtune.sh`) writes the plan's `zfs.arc_max_bytes` as `options zfs zfs_arc_max=`
  to `/etc/modprobe.d/zfs.conf`, before the initramfs that loads the module is built, and the
  plan's `swap.zram_mib` to `/etc/runink/zram.conf`, which `runink-zram.sh` now reads at boot.
  Without a plan the ARC falls back to the planner's rule, `clamp(RAM/16, 1 GiB, 16 GiB)`, and zram
  keeps the image default (`ram`, now passed by `rc.local` as `RUNINK_ZRAM_DEFAULT`; an
  explicit `RUNINK_ZRAM_SIZE` still overrides everything). Until now both values were only
  recorded in the plan. `tests/memtune.sh` (Tier 1) and `tests/assert-golden.sh` check them.

- **The M2 mascot is the Runink River mark on every surface**: the brown-and-white puppy
  grinning over the front of a three-log raft on the water replaces the round badge with the
  dog-head silhouette. `branding/mascot/river-mascot.svg` is the one source; `render.sh`
  generates `branding/logo/river-mark.svg` (the whole drawing on the 512-unit square) and
  `river-mark-small.svg` (the same art cropped to the head, for the 16-24 px icons and the
  16 px favicon) from it, and from those the GRUB background, the Plymouth logo, the Plasma
  splash, the SDDM greeter, both wallpapers, the lockups, the hicolor icons, the favicons,
  the fastfetch logo and its arrival frames; `/etc/issue` and the text-console greeting get a
  new ASCII puppy. The greeter and splash animate the mascot's own parts (the raft with the
  puppy bobs and rocks, the head nods, the waves drift). `scripts/lint-branding-sync.sh`
  checks that every mark file carries the current mascot's sha256.

- **The kernel builds without Python** (owner decision 2026-09-26). The kernel's one Python
  step for this configuration, libbpf's `bpf_helper_defs.h` (`scripts/bpf_doc.py`, reached
  through `resolve_btfids` and bpftool's bootstrap), now runs `build/tools/bpfdoc`, a
  standard-library Go port whose output is byte-identical (tested against the header upstream
  `bpf_doc.py` generated for 7.2.7). Both kernel PKGBUILDs (in-tree and AUR) build it in
  `prepare()` and drop the `python` makedepend for `go`. The build containers
  (`builder/Containerfile`, the new `build/kernel-builder.Containerfile`) and the
  kernel/OpenZFS workflows remove the interpreter base-devel pulls in and fail while it
  remains; `scripts/lint-python-purge.sh` gains a check for all of it. `linux-runink-headers`
  now carries the bpfdoc binary as `scripts/bpf_doc.py`.

- The image and the graphical installer take the new Runink palette: dark-first (ground
  `#212121`), one sage accent (`#C0CC7C`) for what a person acts on, danger kept a distinct
  red. GRUB, Plymouth, the Plasma splash, SDDM and both wallpapers show the community mark
  beside a light "Runink River" wordmark and the meaning of the name, "RIVER ·
  Raft-Integrated Validated Event Runtime" (branding/palette.md). The installer names the
  running edition in its header and window title ("Install <edition>") instead of a fixed
  "Runink River".
- **Runink River is the developer workstation** (owner decision 2026-09-26): the repository
  ships one image, the KDE Plasma workstation (`iso-profiles/river`, formerly
  `iso-profiles/runink-workstation`), as `runink-river-<date>-x86_64.iso`, volume label
  `RIVER` (EFI partition `RIVER_EFI`), live menu "Runink River — Install / Live",
  os-release `Runink River`, one installer edition (`river`). Every visual surface carries
  the community mark: GRUB (live and installed, baseline JPEG), Plymouth, a new Plasma
  start-up splash, SDDM, the wallpaper (one 3840x2160 JPEG per variant), the Kickoff button,
  the `runink-river` icon, favicons, `/etc/issue`. The AGENTS.md invariants were rewritten
  for a workstation (pending TSC sign-off).
- `build/make-multiboot-usb.sh write` takes `--iso ISO [--title T]` up to four times instead
  of `--server` / `--workstation`. `build/qemu-gui-test.sh` and `build/qemu-test.sh` default
  to the workstation; their server modes check a downstream server ISO. `build/cloud-image.sh`
  takes the cloud installer from a downstream server profile (`RIVER_PROFILE_DIR`).
- river-guide's model pin moved to `guide/model.lock`; the model is fetched only for a
  profile that ships the guide (`RIVER_GUIDE_MODEL=1`, set by `build/local-iso.sh`). The k0s
  airgap bundle is listed from `build/k0s/k0s.yaml` or the profile's own `k0s.yaml`.
- The planner records `"iso": "river"` for a workstation plan and `"server"` for a server one.
- The workstation installer makes an install plan and confirms every disk it erases by
  serial, instead of asking for a disk path. `river-plan --profile workstation` plans it
  against desktop minimums (x86-64-v3, 2 cores, 8 GB, one 64 GiB disk, UEFI) with no model
  tiers and no k0s/platform reserve ([docs/INSTALLER-HARDWARE.md](docs/INSTALLER-HARDWARE.md#profiles)).
- river-guide's first step is `network`; the IPv6 address check is now `ipv6-address`.
- **Dual-stack host, IPv6-only cluster** (owner decision): the server host takes IPv4 (DHCP)
  and IPv6 by default (NetworkManager `conf.d/10-dual-stack.conf` replaces
  `10-ipv6-only.conf`; sshd listens on both families). The k0s pod and service networks
  stay IPv6-only, and `runink-node-ip6` pins the k0s node address (kubelet `--node-ip`,
  `api.address`) to the host's IPv6 whenever it has IPv4. `NET_MODE` now defaults to
  `dual`; `NET_MODE=ipv6` and `river.net=dhcp,v6only` are for IPv6-only sites.
- The host firewall is on from first boot on every server node, enrolled or not (s6 oneshot
  `runink-fw`, before NetworkManager and sshd): a base default-deny posture for both families
  (SSH, ICMP essentials, DHCP replies, the cluster, `RUNINK_FW_OPEN` ports). A whitelist at
  enrollment narrows it as before. Forward is default-drop except the IPv6 cluster and NAT64,
  so the host no longer routes a LAN peer's traffic. **Public nodes** that serve host ports
  must now list them in `RUNINK_FW_OPEN`.
- On the live medium the same default-deny firewall accepts the LAN-install pairing ports
  (UDP 47653, TCP 47654 and 47655) from IPv6 link-local sources only
  (`/usr/local/lib/river-pair/fw-pair`, live overlay); an installed node keeps them closed.
- The kernel is `linux-runink`, a pinned fork of the zen kernel 7.2.x stable series, with
  OpenZFS 2.4.4 rebuilt for it as a separate out-of-tree module package.
- The project is named Runink River in full; the pipeline runtime keeps the name RIVER.
- The server ISO installs exactly `Packages-Root` (rootfs) and `Packages-Live` (a live-only
  layer): its own `common.yaml` replaces artools' desktop package lists. 321 packages
  became 204; ModemManager, avahi, bluez, lvm2, mdadm, dmraid, cryptsetup, five other
  filesystems' tools, zsh, vim, os-prober, memtest86+, the non-Intel firmware and the kernel
  headers are gone; bubblewrap, cpupower and runink-tayga, which the allow-list always named,
  are installed. `scripts/lint-profile-manifest.sh` keeps the two in step.
- The live GRUB menu shows only the two Runink River Server entries (no clock, tz,
  keytable or lang entries, no `efi_uga` error), and the badge keeps its shape on 4:3 screens.
- ISO files are named `runink-river-server-<date>-x86_64.iso` and
  `runink-river-workstation-<date>-x86_64.iso`.
- OpenZFS is built `--with-python=no`: runink-zfs-utils no longer ships four scripts for an
  interpreter the image lacks.
- For downstream distributions: the edition descriptor gains the optional `models_required`
  key; `72-models-payload` reads `RUNINK_MODELS_REQUIRED` and `RUNINK_MODELS_RUNDIR`; a model
  payload directory may now hold an unfinished journal, which every tool refuses. With none of
  them set, behaviour is unchanged. The Tier 1 image adds `curl`.

### Removed

- The server profile (`iso-profiles/river` as it was: k0s, NAT64, the tunnel, the kiosk
  medium, the cloud installer overlay) left this repository for a downstream distribution.
  Its packaging (`build/pkgbuilds/*`), the payload and first-boot contract and the installer
  steps stay. The unused Runink-logo assets, the server console palette and pixmaps went
  with it.
- **llama.cpp: mistral.rs is the only inference engine.** The unused `LLAMA_CPP_*` pins, the
  builder's cmake and ninja, the `runink-llama-*` packages from the own-base plan and the
  llama.cpp candidates in `validation/run.sh` are gone. Past llama.cpp measurements stay as
  history marked NOT EVIDENCE (`not_evidence` in `validation/results.json`, which
  `rivervalidate merge` sets and its tests enforce). `scripts/lint-one-engine.sh` keeps the
  name out of the tree, and `tests/assert-golden.sh` still fails an image that carries a
  llama.cpp binary.

### Fixed

- **An image built before `[runink]` is published no longer breaks `pacman -Syu`.** Installer
  step `35-pacman-keyring` enabled `[runink]` on every image that carried its mirrorlist, while
  the repository did not exist yet, so every update would fail with "failed to synchronize all
  databases". The step now enables it only on an image that carries
  `/etc/pacman.d/runink-published`, which only `river repo check-published --mark` writes, at
  build time (`RIVER_REPO_PUBLISHED=yes` in `build/local-iso.sh`), after it fetched
  `runink.db` and its signature from the primary mirror and verified them with the release
  key. Any other image gets the stanza commented out, with the same
  `SigLevel = Required DatabaseRequired` and a one-line note on enabling it; the release key
  is still trusted. The release gate (step 3b) refuses an image that enables `[runink]` until
  the repository is published and reachable. Downstream profiles: an image of yours enables
  `[runink]` only if it is built with `RIVER_REPO_PUBLISHED=yes` (`river test pacman-keyring`,
  [docs/REPOSITORY.md](docs/REPOSITORY.md), "Release order").
- **`install.sh` in the live environment planned with the server minimums** (4 cores, 16 GB, a
  200 GiB pool) and the image's model tiers, so it refused machines the workstation installs
  on. It now plans with `--profile workstation --no-models`, as `runink-install`, which it
  hands over to, does (`river test install-live`).
- docs/REPOSITORY.md: adding `[runink]` to an older installation used `curl`, which an
  installed system does not have (`30-target-config` removes it); the steps now use `git` and
  `pacman-key`.

- **Installed systems had no pacman keyring**, so `pacman` could verify nothing, and the
  `[runink]` stanza and release key described under Added never reached one. Installer step
  `35-pacman-keyring` (added in #45) was in no step list: not the edition descriptor, not
  `runink-install`. It now runs after `32-locale-keyboard` and before `40-boot-grub-zfs`, and
  unmounts what it mounts and stops the gpg-agent `pacman-key` leaves in the target. New
  Tier 1 lint `river lint edition-steps`: every step an image ships is in one of its step
  lists, every listed step is shipped, and steps only a downstream or cloud image runs are in
  `scripts/edition-steps.allow` with a reason.
- **The mouse could not be used in the server medium's graphical installer**: the kiosk (WPE
  WebKit straight on KMS/DRM) received moves and clicks but drew no pointer on the legacy KMS
  path it uses, on displays without a cursor plane or without a cursor theme, so nobody could
  see where the mouse was. The UI now draws the pointer in the kiosk (`kiosk-pointer.js`,
  enabled by the `#kiosk` fragment `river-kiosk` adds) and opens drop-down lists on a click (the
  web view shows no pop-up for a `<select>`), first-boot setup pages can load the
  same script, and `build/qemu-gui-test.sh` checks a real click (`gui-pointer`). The kiosk's
  wait for the installer also passed a bare `120` as a duration and always failed; it waits
  `120s` now.
- **The ISO could never be downloaded from a release**: GitHub caps a release asset at 2 GiB and
  the ISO is over 3 GB, so `install.sh` (and a manual download) had nothing to fetch. A larger
  ISO is now published as 1900 MiB parts listed in the signed `SHA256SUMS` next to the whole
  ISO (`build/release-assets.sh`); `install.sh` verifies each part, joins them and verifies the
  ISO, and the release gate does the same. Git LFS was not used: same 2 GiB limit on this plan,
  and its bandwidth is metered per download.
- **The release-signing key is published**: fingerprint
  `95C0A7B97D547413E42660DDB06FE75626F15BF3` in `KEYS` and `install.sh` (which no longer fails
  closed for want of one); the public key at https://runink.org/.well-known/gpg-key.txt. The
  release gate checks the primary key in GnuPG's VALIDSIG, as install.sh does.
- The Linux console showed the greeting's truecolor badge as grey-and-teal stripes (a text VT
  has only a few colours): it now shows the ASCII River mark, the one /etc/issue shows.
- Services ran in the C locale (the image has no system LANG), so a Qt program printed
  "Detected locale C … Qt depends on a UTF-8 locale" onto VT1: the supervision tree now
  starts with LANG=C.UTF-8 (/etc/s6/current/env/LANG).
- The installed workstation was named after the live medium (`runink-live`): the installer's
  default hostname took the network's name even when that was the live system's own. It is
  now the edition's name, or one the network assigned (river#136); `qemu-gui-test` checks it.
- On the Linux console the terminal greeting skips fastfetch's Display and GPU modules, which
  open /dev/dri; the live medium's VT2 ran them at boot while the compositor took the display,
  the suspected cause of a black first Ctrl+Alt+F2 (river#134, to be confirmed on the next ISO).
- `docs/INSTALL.md` said 8 GiB of RAM; the hardware plan refuses below 16 GB (15360 MiB).
- `/etc/lsb-release` named Artix Linux (`lsb_release`, system-info tools); it names Runink River.
  os-release loses its stale `BUILD_ID=runink-os-2026.07` (the version is `/etc/runink-os-version`).
- `tests/assert-golden.sh` failed every downstream server whose `k0s.yaml` enables
  `spec.network.dualStack`: it asserted an IPv6-only cluster and rejected the IPv4 cluster rules
  in the forward chain. A dual-stack profile is now checked as dual-stack with IPv6 as the
  primary family (IPv6 ranges, kube-router routing both families, an IPv6 node address first);
  without `dualStack` the IPv6-only checks are unchanged.
- The installed workstation never reached the desktop: the pool's `/home` dataset was not
  mounted at boot (only the server profile shipped the `zfs-mount` oneshot), so SDDM's helper
  could not enter `/home/runink` and Plasma never started. The profile now ships `zfs-mount`,
  `sddm-srv` depends on it, `~/.local` is created owned by the user, and every install gets
  its own `/etc/machine-id`.
- The cloud-image install path (`runink-autoinstall --cloud`), `river-cloud-init` in
  `runink-installer`, and the cloud checks of `tests/assert-golden.sh`, all dropped when the
  air-gapped payloads change was merged on top of the Compute Engine image change. Cloud
  images now also carry the downstream payloads, and a public one never carries models.
- The server live medium now carries its live overlay (river-guide on tty1, its model and
  s6 service, tty2 autologin): the profile had no `livefs` layer, so artools never applied it.
- The live s6 boot database was never committed (a bundle without a `type` file broke
  `s6 repository sync`), so the live system and every installed node booted without
  NetworkManager and sshd. `80-enable-s6` now builds the node's database with s6-frontend and
  fails the install when it cannot.
- Installed nodes mount `<pool>/home`, `/var/lib/core`, `/var/lib/k0s` and the models
  dataset at boot (`zfs-mount`), and `rc.local` waits for the filesystems.
- `river-hwprobe` reads virtio-blk serials (`/sys/block/vdX/serial`), so cloud and VM disks
  can be confirmed by serial.
- `runink-autoinstall` no longer waits for a key press after printing the recovery key, and
  its RAM check defers to the install plan.
- `net.ipv4.ip_unprivileged_port_start=80` and `net.core.default_qdisc=fq` are set, as the
  golden image asserts.
- Runink River Workstation ISO: the build failed on `sddm-srv` depending on `artix-live`, a
  service artools adds for the display manager although the image does not install it, and
  no live service reached the boot database. artools now adds that dependency only when
  artix-live is installed. The workstation live medium gains a `livefs` layer (its live
  overlay was never applied), its own GRUB menu (with a safe-graphics entry), SDDM autologin
  into Plasma and a tty2 console for the live user, passwordless sudo on the live medium,
  and `sudo` with the admin's password on installed machines (root is locked there).
  Installed machines keep SDDM, Bluetooth and CUPS enabled and boot to the SDDM greeter.
  `build/qemu-test.sh --profile workstation` checks the live desktop.

## runink-os-2026.07 (2026-07-31)

The first dated image, cut before this changelog existed. No release notes were kept for
it, and it predates the public history of this repository.
