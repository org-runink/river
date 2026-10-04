<!--
SPDX-FileCopyrightText: 2026 The RIVER Authors
SPDX-License-Identifier: MIT
-->

# Runink River roadmap

Where Runink River is going over the next year, what it will deliberately not do, and how
this plan changes. Milestones are listed in dependency order, not by date: with one
maintainer today, dates would be guesses, and this roadmap does not guess. Each milestone
links to the document that holds its plan.

## Where we are going

Five outcomes define success for the coming year. Every milestone below serves at least
one of them.

1. **A release anyone can verify.** Signed, dated releases that install, unlock and reach a
   working desktop on real hardware, with SBOMs and provenance, and an image that two
   independent builds reproduce bit for bit.
2. **Every change proven before it merges.** CI that builds the image and installs it in a
   VM, so a regression in boot, the installer or ZFS fails a pull request instead of a
   user's machine.
3. **A base the project fully controls.** The transitional ISO tooling replaced by a
   from-source base, then Secure Boot with the project's own keys.
4. **Measured, not claimed.** Published, reproducible benchmarks for the kernel's analytics
   profile, and a validated pipeline runtime (`riverd`) whose steps run confined.
5. **A project no single company controls.** More maintainers from more organisations, an
   elected TSC, and a home at a vendor-neutral foundation.

## Milestones

Status is one of **done**, **in review** (code exists on a branch, not on `main`),
**in progress** or **planned** (no code yet). Milestone numbers are stable references and
do not change when the order does.

| # | Milestone | Outcome | Status | Plan |
| --- | --- | --- | --- | --- |
| 1 | **Public repository and community basics.** Publish; enable private vulnerability reporting and branch protection with required checks; register the OpenSSF Best Practices badge and reach *passing*. | 5 | in progress: public, private reporting and branch protection are on; the badge is not registered yet | [docs/governance/OPENSSF-BEST-PRACTICES.md](docs/governance/OPENSSF-BEST-PRACTICES.md), [docs/governance/FOUNDATION-READINESS.md](docs/governance/FOUNDATION-READINESS.md) |
| 2 | **Foundation application (LF AI & Data Sandbox).** A sponsor, the proposal, a TAC presentation; trademark transfer on acceptance. | 5 | planned: the proposal is a draft and has **not** been submitted | [docs/governance/LF-AIDATA.md](docs/governance/LF-AIDATA.md), [docs/governance/lfaidata-proposal.md](docs/governance/lfaidata-proposal.md) |
| 3 | **First signed public release.** `runink-os-YYYY.MM` with signed checksums, packages and tag, published only through `release-gate.yml`, after an install, reboot, disk unlock and desktop login pass on real hardware. | 1 | in progress: the release key exists and is pinned in [KEYS](KEYS); the real-hardware gate is open; no release yet | [RELEASE.md](RELEASE.md), [docs/RELEASE-SIGNING.md](docs/RELEASE-SIGNING.md) |
| 4 | **Kernel benchmarks.** Published, reproducible results for the analytics kernel profile against the stock kernels, with the hardware and the harness. | 4 | in progress: the profile and the `bench/analytics` harness are on `main`; no results yet | [docs/KERNEL.md](docs/KERNEL.md), `bench/analytics/` |
| 5 | **Tier 2 CI.** An unattended graphical install to a virtual encrypted ZFS disk, checked in CI on every relevant change. | 2 | in progress: the harness runs on a maintainer's machine; the workflow is a scaffold without a runner | [docs/governance/CI.md](docs/governance/CI.md) |
| 6 | **RIVER runtime, step 1:** the Go library that parses and validates the pipeline formats (`.dsl`, `.contract`, `.herd`, `@step`, golden tests). | 4 | planned | [runtime/README.md](runtime/README.md) |
| 7 | **RIVER runtime, step 2:** `riverd`, which runs those pipelines with each step in `river-sandbox` and a cgroup v2 leaf, and records lineage for every step. | 4 | planned | [runtime/README.md](runtime/README.md) |
| 8 | **Own from-source base, phases 1 and 2:** a reproducible toolchain, then the base built from recipes. | 3 | in progress: the phase 0 scaffold is done | [docs/OWN-BASE.md](docs/OWN-BASE.md) |
| 9 | **Reproducible ISO:** two independent builds of the same commit produce identical images. | 1 | planned (depends on 8) | [docs/BUILD.md](docs/BUILD.md#reproducibility-status) |
| 10 | **Secure Boot:** signed modules with `MODULE_SIG_FORCE`, the project's own CA and MOK path, then a shim through rhboot/shim-review. | 3 | planned | [docs/SECURE-BOOT.md](docs/SECURE-BOOT.md) |
| 11 | **Incubation prerequisites:** a second maintainer, then maintainers from three organisations, an elected TSC with a chair, OpenSSF *silver*. | 5 | planned: one maintainer today | [GOVERNANCE.md](GOVERNANCE.md#path-to-vendor-neutral-governance), [docs/governance/LF-AIDATA.md](docs/governance/LF-AIDATA.md) |
| 12 | **SLSA Build L3 release provenance.** Build the ISO on an isolated, ephemeral builder that generates its own non-forgeable provenance, instead of attesting digests after a maintainer build (Build L1 is the most the current `release-attest.yml` can reach, [docs/governance/OPENSSF-BEST-PRACTICES.md](docs/governance/OPENSSF-BEST-PRACTICES.md#honest-slsa-level)). | 1 | planned (depends on 5 and 9) | [docs/governance/CI.md](docs/governance/CI.md) |
| 13 | **In-place upgrades.** A new release installed as a new boot environment, keeping the pool and `/home`, with rollback; today the documented path is a reinstall. | 1 | planned (with the own base) | [docs/OWN-BASE.md](docs/OWN-BASE.md) |
| 14 | **TPM 2.0 unattended disk unlock**, beside the recovery key. | 3 | planned (with the own base) | [docs/ENCRYPTION.md](docs/ENCRYPTION.md) |
| 15 | **Kernel on the AUR.** Publish `linux-runink` and `zfs-linux-runink` for existing Arch Linux systems. | 4 | in progress: the packaging is ready, publication is pending | [packaging/aur/PUBLISHING.md](packaging/aur/PUBLISHING.md) |

## Not planned

These are out of scope by design ([AGENTS.md](AGENTS.md) invariants); a change to any of them
needs a TSC vote ([GOVERNANCE.md](GOVERNANCE.md#decision-making)).

- systemd, or a second init system.
- More than one kernel, or ZFS built into the kernel.
- A second software supply chain in the base image: no app store, no Flatpak, no container
  runtime.
- Application logic, a specific downstream product, or its manifests in this repository.
- Telemetry, phone-home behaviour or third-party hosted AI APIs in the image.

## How this roadmap changes

Anyone can propose a change to the roadmap: open a
[Discussion](https://github.com/org-runink/river/discussions) or an issue describing the
outcome you want and why, then a pull request against this file. Adding or reordering a
milestone follows the "new feature" row of [GOVERNANCE.md](GOVERNANCE.md#decision-making)
(one approval, no objection within 3 working days); removing an item from *Not planned*
changes an invariant and needs a TSC vote. A milestone's status is updated in the pull
request that changes it. The documentation site mirrors this file at
<https://docs.runink.org/river/docs/roadmap/>.
