<!--
SPDX-FileCopyrightText: 2026 The RIVER Authors
SPDX-License-Identifier: MIT
-->

# Runink River roadmap

What the project intends to do over the next year, and what it deliberately will not do.
Milestones are listed in dependency order, not by date: with one maintainer today, dates
would be guesses. Each milestone links to the document that holds its plan. Status is one
of **done**, **in review** (code exists on a branch, not on `main`), **in progress** or
**planned** (no code yet).

## Milestones

| # | Milestone | Status | Plan |
| --- | --- | --- | --- |
| 1 | **Public repository and community basics.** Publish from one squashed commit; enable private vulnerability reporting, branch protection and 2FA; register the OpenSSF Best Practices badge and reach *passing*. | in progress (the repository side is done; the owner steps remain) | [docs/governance/LF-AIDATA.md](docs/governance/LF-AIDATA.md), [docs/governance/OPENSSF-BEST-PRACTICES.md](docs/governance/OPENSSF-BEST-PRACTICES.md) |
| 2 | **LF AI & Data Sandbox application.** Sponsor, TAC presentation, trademark transfer on acceptance. | planned | [docs/governance/lfaidata-proposal.md](docs/governance/lfaidata-proposal.md) |
| 3 | **First signed public release.** Generate the release-engineering key, publish its fingerprint, cut `runink-os-YYYY.MM` with signed checksums, packages and tag. | planned (blocked on the key) | [RELEASE.md](RELEASE.md), [docs/RELEASE-SIGNING.md](docs/RELEASE-SIGNING.md) |
| 4 | **Analytics kernel profile and benchmark harness**, then published, reproducible benchmark results. No results exist yet. | in review | `build/pkgbuilds/runink-kernel/README.md` |
| 5 | **Tier 2 CI.** A dedicated KVM runner; an unattended install to a virtual encrypted ZFS disk; the on-target asserts in CI. | in progress (workflow scaffold, no runner) | [docs/governance/CI.md](docs/governance/CI.md) |
| 6 | **RIVER runtime, step 1:** the Go library that parses and validates the pipeline formats (`.dsl`, `.contract`, `.herd`, `@step`, golden tests). | planned | [runtime/README.md](runtime/README.md) |
| 7 | **RIVER runtime, step 2:** `riverd`, placing and running pipelines on the cluster, each step in `river-sandbox` and a cgroup v2 leaf. | planned | [runtime/README.md](runtime/README.md) |
| 8 | **Own from-source base, phases 1 and 2:** a reproducible toolchain, then the server base built from recipes. | in progress (phase 0 scaffold done) | [docs/OWN-BASE.md](docs/OWN-BASE.md) |
| 9 | **Reproducible ISO:** two independent builds of the same commit produce identical images. | planned (depends on 8) | [docs/BUILD.md](docs/BUILD.md#reproducibility-status) |
| 10 | **Secure Boot:** signed modules with `MODULE_SIG_FORCE`, the organisation CA / MOK path, then a shim through rhboot/shim-review. | planned | [docs/SECURE-BOOT.md](docs/SECURE-BOOT.md) |
| 11 | **Incubation prerequisites:** a second maintainer, then maintainers from three organisations, a TSC with a chair, OpenSSF *silver*. | planned | [docs/governance/LF-AIDATA.md](docs/governance/LF-AIDATA.md) |
| 12 | **SLSA Build L3 release provenance.** Build the ISO on an isolated, ephemeral builder that generates its own non-forgeable provenance, instead of attesting digests after a maintainer build (Build L1 is the most the current `release-attest.yml` can reach, [docs/governance/OPENSSF-BEST-PRACTICES.md](docs/governance/OPENSSF-BEST-PRACTICES.md#honest-slsa-level)). | planned (depends on 5 and 9) | [docs/governance/CI.md](docs/governance/CI.md) |

## Not planned

These are out of scope by design ([AGENTS.md](AGENTS.md) invariants); a change to any of them
needs a TSC vote.

- systemd, or a second init system.
- More than one kernel, or ZFS built into the kernel.
- A desktop stack, runtime fetch tools (`curl`, `wget`, `git`) or a C/C++ toolchain on the
  server image.
- Application logic, a specific downstream product, or its manifests in this repository.
- Telemetry, phone-home behaviour or third-party hosted AI APIs in the image. Model
  inference stays local.
- IPv4 inside the k0s cluster network (the host is dual-stack; pods and services stay
  IPv6-only).
