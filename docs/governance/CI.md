<!--
SPDX-FileCopyrightText: 2026 The RIVER Authors
SPDX-License-Identifier: MIT
-->

# Runink River CI structure

Runink River's CI has two tiers. Both are designed so that code from a pull request, including
one from a fork, cannot reach a machine that holds anything of value.

## Tier 1 — lint and unit checks (ephemeral rootless podman)

| | |
| --- | --- |
| Workflow | `.github/workflows/ci.yml` (job `tier1`) |
| Entry point | `sh scripts/ci-tier1.sh` (the same command runs locally) |
| Isolation | A fresh **rootless** podman container per run, built from `ci/tier1.Containerfile` (Alpine pinned by digest): `--network=none`, source mounted **read-only**, tmpfs `/tmp`, all capabilities dropped, `no-new-privileges`, removed on exit. The same for every event, fork pull requests included. `RIVER_TIER1_NO_CONTAINER=1` runs the same checks on the host (for a machine without podman; it needs shellcheck and Go) |
| Checks | shellcheck (warning and up) on every script; installer/overlay sync; branding sync and size budget; no-python guard; k0s pin cross-check |
| Triggers | every pull request, every push to `main` |
| Runners | GitHub-hosted `ubuntu-latest`, always. See "Runners" below |
| Secrets | none |

The closure lint (`river lint closure`) is not in Tier 1: it needs the pinned
Artix/Arch sync databases plus the locally built `[runink]` repository, so it runs in
`make lint` and inside the ISO build.

The installer's Go code (`installer/`: `river-hwprobe`, `river-plan`) is checked by a second
Tier 1 job, `installer-go` in the same workflow: gofmt, `go vet`, the planner's table-driven
tests on fixture probes, and a `GOAMD64=v1` build that runs the probe and a plan on the
runner. It needs the network to install the Go toolchain, so it does not run inside the
`--network=none` container. It runs on the same GitHub-hosted runners as `tier1`, with no
secrets and `persist-credentials: false`. `make test` runs the same checks locally.
`install.sh` is covered by the shellcheck step above.

The `river-guide` job does the same for `guide/`, with the race detector (`go test -race`); `installer-go`
runs its tests under the race detector too. A fourth job, `go-security`, on the same runners, runs two
analysers pinned by version: **gosec** (static analysis for common weaknesses, CWE-mapped) over the Go
that ships in the image (`installer/`, `guide/`), and **govulncheck** (known vulnerabilities in the Go
standard library and modules) over every Go module. Rule G304 is excluded because both tools are
operator CLIs that read the paths the operator names; every other finding is fixed or carries a
`#nosec <rule> -- <reason>` annotation at the line.

The org-wide `gatekeeper` workflow adds a secret scan (gitleaks), workflow linting (actionlint,
zizmor), SHA-pinning and self-hosted-runner checks, credential checks and dependency review, as one
status check. The REUSE lint (`river lint reuse`, also run by Tier 1) runs in its own workflow,
`reuse.yml` (a plain Go process with no container, GitHub-hosted), so the README badge
reflects compliance on `main`.

## Tier 2 — boot, ZFS and appfs tests in short-lived microVMs

| | |
| --- | --- |
| Workflow | `.github/workflows/tier2-vm.yml` |
| What | Build the ISO in the ephemeral builder container (`make iso-in-builder`), then boot it in a throwaway QEMU/KVM microVM with no network (`tests/tier2-boot-smoke.sh`). Planned next: unattended install to a virtual ZFS disk, reboot, `tests/assert-golden.sh`, pool/encryption and appfs mount tests |
| Runners | GitHub-hosted `ubuntu-latest` (it has `/dev/kvm`; the workflow's udev rule makes it usable). **It does not fit there:** one job compiles every component from source and builds the ISO, which needs ~40 GB of disk, more than 6 h and more than 16 GB of RAM. See the workflow header |
| Triggers | `workflow_dispatch` only; never a pull request |
| Status | Not run in CI. The maintainers run Tier 2 locally: `build/local-iso.sh` ([BUILD.md](../BUILD.md)), then `tests/tier2-boot-smoke.sh <iso>` or `build/qemu-gui-test.sh` |

## Runners

**This public repository runs only on GitHub-hosted runners.** No job here ever runs on a
self-hosted runner, and no workflow may name a self-hosted label (`runs-on` is
`ubuntu-latest`, or `ubuntu-24.04` where a job depends on that release's packages). No
runner registered anywhere else is ever used for this one.

A GitHub-hosted runner is a fresh VM per job, destroyed afterwards, with no route to
anything of the project's. That makes every job equally safe for pull requests from forks,
so there are no fork-only twin jobs and no same-repository guards: each check is one job
that runs for every event.

What still protects the project from a malicious pull request:

- **Maintainer approval.** Repository setting: **require approval for all external
  contributors** ("Approval for running fork pull request workflows from contributors").
  No workflow runs for an outside contributor's pull request until a maintainer has read
  the diff and approved the run.
- **No secrets, read-only token.** GitHub withholds secrets from fork runs and gives them a
  read-only `GITHUB_TOKEN`; every checkout uses `persist-credentials: false`, and every
  workflow defaults to `permissions: contents: read`.
- **No write access from pull requests.** The jobs that write (the Pages deploy with
  `pages: write` and `id-token: write`, Scorecard's `id-token: write`, the release gate
  and attest jobs with `contents: write`) run only on a push to `main`, a schedule or a
  maintainer's dispatch, never on `pull_request`. No workflow uses `pull_request_target`.
- **Tier 1 isolation.** The lint runs in a `--network=none`, read-only, capability-free
  rootless container, so pull-request code it executes cannot reach the network or change
  the checkout.

### Fitting the heavy jobs on a standard runner

A standard hosted runner (public repository) has 4 vCPU, 16 GB of RAM, ~14 GB free on `/`,
`/dev/kvm`, passwordless sudo, docker and podman, and a 6 h job limit. `/mnt` is on the same
root disk. The heavy jobs make room by removing the preinstalled toolchains they never use
(.NET, the Android SDK, GHC, Swift, PowerShell, Boost, the hosted tool cache, browsers,
preinstalled docker images); on 2026-09-05 that took a runner from 14 GB to 38 GB free.
Each job then checks it has the room it needs and stops at once if not.

| Workflow | Fits? | How |
| --- | --- | --- |
| `kernel-build.yml` | yes | reclaim, build under `/mnt/os-build` (~30 GB); built in ~4 h 10 min on 2026-09-05 |
| `zfs-build.yml` | yes | ~10 GB, ~15-30 min; no reclaim needed |
| `images.yml` | yes, not yet run | `ubuntu-24.04`; `build/ci-runner-prep.sh` reclaims and checks 30 GB (kernel job) / 20 GB (ISO job) |
| `release-gate.yml`, `release-attest.yml` | yes, not yet run | reclaim step, then a 20 GB check (ISO, its unpacked root, SBOMs) |
| `tier2-vm.yml` | **no** | dispatch only; see Tier 2 above |

## Other workflows

| Workflow | Runs on | Purpose |
| --- | --- | --- |
| `dco.yml` | `ubuntu-latest` | DCO sign-off check on every PR |
| `reuse.yml` | `ubuntu-latest` | `river lint reuse` (REUSE 3.3, the Go port of `reuse lint`) on every PR and push to `main`, a plain process with no container |
| `docs.yml`, `docs-deploy.yml` | `ubuntu-latest` | Build the website on every PR that touches it; deploy it to GitHub Pages on `main` (the deploy job alone holds `pages: write`). `docs-deploy.yml` is off unless `RIVER_DOCS_COMMIT` is `true` |
| `gatekeeper.yml` | reusable org workflow | Secret scan, workflow security lint, credential checks, dependency review |
| `scorecard.yml` | `ubuntu-latest` | OpenSSF Scorecard, weekly and on push once public; SARIF to code scanning, results published to the Scorecard API (which accepts only GitHub-hosted runs) |
| `kernel-build.yml`, `zfs-build.yml` | `ubuntu-latest`, manual dispatch | Build `linux-runink` and `runink-zfs` in a throwaway Artix container |
| `release-attest.yml`, `release-gate.yml` | `ubuntu-latest`, manual dispatch | SBOMs, checksums and provenance for a release; the gate verifies, scans, attests and publishes; never signs |
| `images.yml` | `ubuntu-24.04`, manual dispatch or a `runink-os-*` tag | The public, payload-free ISO; never a downstream payload; publishing disabled ([CLOUD-IMAGES.md](../CLOUD-IMAGES.md#building-in-github-actions)) |
| `tier2-vm.yml` | `ubuntu-latest`, manual dispatch | Does not fit a standard runner; see Tier 2 |
