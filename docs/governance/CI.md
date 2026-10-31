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
| Triggers | every pull request from a branch of this repository, every push to `main` |
| Runners | `[self-hosted, public-ci]`, owner-only. See "Runners" below |
| Secrets | none |

The closure lint (`river lint closure`) is not in Tier 1: it needs the pinned
Artix/Arch sync databases plus the locally built `[runink]` repository, so it runs in
`make lint` and inside the ISO build.

The installer's Go code (`installer/`: `river-hwprobe`, `river-plan`) is checked by a second
Tier 1 job, `installer-go` in the same workflow: gofmt, `go vet`, the planner's table-driven
tests on fixture probes, and a `GOAMD64=v1` build that runs the probe and a plan on the
runner. It needs the network to install the Go toolchain, so it does not run inside the
`--network=none` container. It runs on the same `public-ci` runners as `tier1`, with no
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
`reuse.yml` (a plain Go process with no container, on `public-ci`), so the README badge
reflects compliance on `main`.

## Tier 2 — boot, ZFS and appfs tests in short-lived microVMs

| | |
| --- | --- |
| Workflow | `.github/workflows/tier2-vm.yml` |
| What | Build the ISO in the ephemeral builder container (`make iso-in-builder`), then boot it in a throwaway QEMU/KVM microVM with no network (`tests/tier2-boot-smoke.sh`). Planned next: unattended install to a virtual ZFS disk, reboot, `tests/assert-golden.sh`, pool/encryption and appfs mount tests |
| Runners | `[self-hosted, public-ci]`, which has no `/dev/kvm`, no sudo and too little disk: **it does not run there.** One job compiles every component from source and builds the ISO (~40 GB of disk, more than 6 h, more than 16 GB of RAM). See the workflow header |
| Triggers | `workflow_dispatch` only; never a pull request |
| Status | Not run in CI. The maintainers run Tier 2 locally: `build/local-iso.sh` ([BUILD.md](../BUILD.md)), then `tests/tier2-boot-smoke.sh <iso>` or `build/qemu-gui-test.sh` |

## Runners

**Every job runs on the project's own runners, and only for the owner** (since
2026-10-31; before that this repository used GitHub-hosted runners, which it no longer
uses). `river lint ci-runners` (Tier 1) keeps two rules true for every job of every
workflow:

- `runs-on: [self-hosted, public-ci]`, exactly. No GitHub-hosted label (`ubuntu-*`,
  `windows-*`, `macos-*`), no `runs-on` expression, no other pool.
- `if: github.event_name != 'pull_request' || github.event.pull_request.head.repo.full_name == github.repository`,
  alone or ANDed after the job's own condition as `(<own>) && (<owner-only>)`. A push to a
  branch of this repository, a schedule or a dispatch runs; a pull request runs only when
  its head branch is in this repository. A fork's pull request starts no job: a maintainer
  who wants CI on it pushes the reviewed commits to a branch here.

A `public-ci` runner serves one job and is then replaced; it holds no secret and no
credential beyond the job's own `GITHUB_TOKEN`, has internet egress only, and serves no
private repository. The `if:` is read from the pull request's own workflow files, so it is
not the boundary on its own: a fork that edits it still waits for a maintainer's approval
and, once approved, still reaches only these runners.

What protects the project from a malicious pull request:

- **Owner-only jobs.** The `if:` above, on every job.
- **Maintainer approval.** Repository setting: **require approval for all external
  contributors** ("Approval for running fork pull request workflows from contributors").
  No workflow runs for an outside contributor's pull request until a maintainer has read
  the diff and approved the run.
- **No secrets, read-only token.** GitHub withholds secrets from fork runs and gives them a
  read-only `GITHUB_TOKEN`; every checkout uses `persist-credentials: false`, and every
  workflow defaults to `permissions: contents: read`.
- **No write access from pull requests.** The jobs that write (the Pages deploy with
  `pages: write` and `id-token: write`, the release gate and attest jobs with
  `contents: write`) run only on a push to `main`, a schedule or a maintainer's dispatch,
  never on `pull_request`. No workflow uses `pull_request_target`.
- **Tier 1 isolation.** The lint runs in a `--network=none`, read-only, capability-free
  rootless container, so pull-request code it executes cannot reach the network or change
  the checkout.

### The heavy jobs

A `public-ci` runner has no sudo, no `/dev/kvm` and a few GB of scratch disk. The jobs
below were sized for a GitHub-hosted VM (4 vCPU, 16 GB of RAM, passwordless sudo, docker
and podman, ~38 GB free after removing its preinstalled toolchains) and **fail on
`public-ci`** until a public runner with those capabilities exists. Build them locally
meanwhile (`build/local-iso.sh`, [BUILD.md](../BUILD.md)).

| Workflow | Needs |
| --- | --- |
| `kernel-build.yml` | sudo, docker, ~30 GB, ~4 h |
| `zfs-build.yml` | docker, ~10 GB |
| `images.yml` | sudo, podman, 20-30 GB |
| `release-gate.yml`, `release-attest.yml` | sudo, apt, docker, ~20 GB |
| `tier2-vm.yml` | `/dev/kvm`, sudo, ~40 GB; see Tier 2 above |

## Other workflows

| Workflow | Runs on | Purpose |
| --- | --- | --- |
| `dco.yml` | `public-ci` | DCO sign-off check on every same-repository PR (fork PRs: the maintainer checks sign-off before pushing them here) |
| `reuse.yml` | `public-ci` | `river lint reuse` (REUSE 3.3, the Go port of `reuse lint`) on every PR and push to `main`, a plain process with no container |
| `docs.yml`, `docs-deploy.yml` | `public-ci` | Build the website on every PR that touches it; deploy it to GitHub Pages on `main` (the deploy job alone holds `pages: write`). `docs-deploy.yml` is off unless `RIVER_DOCS_COMMIT` is `true` |
| `gatekeeper.yml` | reusable org workflow | Secret scan, workflow security lint, credential checks, dependency review |
| `scorecard.yml` | `public-ci` | OpenSSF Scorecard, weekly and on push once public; SARIF to code scanning. Results are not published: the Scorecard API accepts only GitHub-hosted runs, so the badge no longer updates |
| `kernel-build.yml`, `zfs-build.yml` | `public-ci`, manual dispatch (fails there today; see "The heavy jobs") | Build `linux-runink` and `runink-zfs` in a throwaway Artix container |
| `release-attest.yml`, `release-gate.yml` | `public-ci`, manual dispatch (fails there today) | SBOMs, checksums and provenance for a release; the gate verifies, scans, attests and publishes; never signs |
| `images.yml` | `public-ci`, manual dispatch or a `runink-os-*` tag (fails there today) | The public, payload-free ISO; never a downstream payload; publishing disabled ([CLOUD-IMAGES.md](../CLOUD-IMAGES.md#building-in-github-actions)) |
| `tier2-vm.yml` | `public-ci`, manual dispatch | Does not run there; see Tier 2 |
