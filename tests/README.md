# Tests

Golden-image verification. The `assert-*` / `smoke-*` scripts run **on a booted target**
(directly or over ssh) and exit non-zero on failure.

The host-side contract tests that used to live here as `*.sh` are now subcommands of the
`river` CLI (`river test <name> --repo .`, package `cli/internal/testcmd`, [docs/GO-CLI.md](../docs/GO-CLI.md)):
`scripts/ci-tier1.sh` runs them with the binary it builds. They still drive the shell code they
test (a sourced library, the first-boot runner) as programs, so that code is unchanged.

| Script | Gate | Where |
|---|---|---|
| `assert-golden.sh` | **a downstream server node** (not the workstation, which has a desktop): one kernel, no desktop stack, s6 (no systemd), ZFS root + hostid, encryption on every dataset, shadow-grade modes (`river-perms --check`), governor=performance, sysctl, zram, the plan's `zfs_arc_max` and zram size, subuid, no host-side llama.cpp leftovers | booted target |
| `river test memtune` | the install plan's ZFS ARC (`/etc/modprobe.d/zfs.conf`) and zram size (`/etc/runink/zram.conf`, `runink-zram.sh`) in a scratch root; no root needed | Tier 1 (`scripts/ci-tier1.sh`) |
| `smoke-k0s.sh` | k0s API ready, the shipped CoreDNS up, plus any node-defined checks (`SMOKE_DEPLOYMENTS`, `SMOKE_HOSTS` / `/etc/runink/app-hosts`, and a payload's own `smoke.sh`) | booted target |
| `../build/qemu-gui-test.sh` | the graphical installer end to end, unattended, as the user: every screen, reboot, unlock, `/home` mounted, firewall, SDDM login into Plasma, network up; `RIVERTEST OK/FAIL <check>` on the serial log. `--profile server --offline` with `RIVER_PROFILE_DIR` for a downstream server image | dev box with KVM + OVMF |
| `river test installed-hooks` | the profile-hook contract of `qemu-gui-test.sh` (`build/qemu-hooks.sh`): only executable `tests/installed.d/*.sh` run, the `RIVERTEST-CHECKS` header is required and its checks join the verdict, `hook-<name>` fails on a bad exit, a timeout, a missing, doubled or undeclared check; SKIP is listed, not failed; `RIVER_HOOK_*` reach the hooks. Fake hooks in a scratch directory, no VM | Tier 1 (`scripts/ci-tier1.sh`) |
| `river test firstboot-hooks` | the first-boot hook contract of `river-firstboot-hooks` (docs/PAYLOADS.md): done, registered (a page), deferred (exit 75), failed, headless, answers shredded once every hook is done, `RIVER_ROLE` passed through; in a scratch root (`RIVER_FIRSTBOOT_ROOT`), no root needed | Tier 1 (`scripts/ci-tier1.sh`) |
| `river test external-profile` | a profile outside this repository (`build/profile-lib.sh`): `river-profile.env` read and bad values refused, staging with a branding overlay and remove list, one medium with several editions, and the profile lints on the staged copy; scratch directory, no root, no network | Tier 1 (`scripts/ci-tier1.sh`) |
| `../build/qemu-test.sh` | a live medium without installing, as the user: GRUB entry, SDDM, the live user in Plasma, NetworkManager, Bluetooth and CUPS in s6-rc, the network step. `--profile server` (with `--lab`, `--offline`, `--expect-payloads`, `--public`) checks a downstream server ISO, including a `runink-autoinstall` and `assert-golden.sh` | dev box with KVM |
| `vm-boot-test.sh` | qemu harness: boot the ISO, install, then run the asserts | dev box with KVM |
| `boot-test-onbox.sh` | the same boot test driven on a build host | dev box with KVM |
| `river test sddm-theme <out-dir>` | the SDDM greeter theme `runink-river` and the Plasma splash rendered offscreen through `sddm/Harness.qml` (a stand-in for the greeter): one PNG per state (three animation phases, 1024x768, 4K, focused password, failed sign-in, keyboard focus, no user list, second screen, hand-off, splash); fails on any QML warning or if the mark does not move | dev box with qt6-declarative + qt6-svg |

## Full gate

For the Runink River workstation image:

```sh
build/local-iso.sh            # then the one sudo line it prints (AGENTS.md, "Build, test, lint")
build/qemu-gui-test.sh ~/.cache/river-build/iso-out/runink-river-<date>-x86_64.iso
```

The older manual server-node gate (`vm-boot-test.sh` is a scaffold):

```sh
make iso                      # build the ISO on Artix (or: make iso-in-builder)
tests/vm-boot-test.sh *.iso   # boot it in qemu
# inside the guest:
sudo runink-install           # install to the virtual disk
# after reboot, over ssh (hostfwd :2222):
ssh -p 2222 runink@localhost 'sh -s' < tests/assert-golden.sh
ssh -p 2222 runink@localhost 'sh -s' < tests/smoke-k0s.sh
```

None of these run in CI on every pull request: they need KVM and a built ISO.
`tier2-vm.yml` builds and boots an ISO, on manual dispatch only; it does not fit a standard
GitHub-hosted runner, so maintainers run these locally ([docs/governance/CI.md](../docs/governance/CI.md)).
This repository never uses a self-hosted runner.
