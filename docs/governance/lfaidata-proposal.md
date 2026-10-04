<!--
SPDX-FileCopyrightText: 2026 The RIVER Authors
SPDX-License-Identifier: MIT
-->

# LF AI & Data Sandbox project proposal: Runink River

> **Status: DRAFT, not submitted.** This is the text of Runink River's application to the
> LF AI & Data Foundation as a **Sandbox** project. It follows the
> [`lfai/proposing-projects` template](https://github.com/lfai/proposing-projects/blob/master/proposal-template.adoc)
> field by field. Items marked **OWNER-TODO** need the project owner before submission;
> the full list, with the lifecycle checklist, is in [LF-AIDATA.md](LF-AIDATA.md), and
> every foundation criterion is on one page in [FOUNDATION-READINESS.md](FOUNDATION-READINESS.md).
>
> **How to read it.** Every capability is marked **EXISTS** (on `main`, with the file
> paths a reviewer can open) or **ROADMAP** (no code yet). Nothing here claims adopters,
> outside contributors, stars or benchmark results: there are none yet.

## Name of project

**Runink River** (always in full; a bare "River" is an unrelated Wayland compositor).
**RIVER** (*Raft-Integrated Validated Event Runtime*) names only the planned pipeline
runtime, `riverd`. The name is unique among LF AI & Data projects. Trademark clearance is
pending (see "Trademark").

## Requested project maturity level

**Sandbox.**

## Project description

**Runink River — a minimal, auditable developer workstation for data, analytics and AI
work on hardware you own.**

Runink River is a Linux distribution for developers who build data, analytics and AI
software on their own machines and need to know exactly what runs there. It installs KDE
Plasma on Wayland on **s6** (never systemd), with one pinned kernel, an **encrypted ZFS
root** with boot environments, a **default-deny firewall** and a **sandbox** for code the
machine does not trust. There is no app store, no telemetry, and nothing in the image
calls a hosted AI service. Every upstream is pinned by version and checksum, the rules
every change must keep are written down as invariants and checked in CI, and releases are
signed offline.

It is also a base: other distributions build their own images from a pinned commit of this
repository through documented, public interfaces (external profiles, payloads and a
first-boot contract), without forking it.

### What exists today

| Capability | Status | Where to look |
| --- | --- | --- |
| **Zen-based kernel with an enforced config floor.** `linux-runink` is a pinned fork of the zen kernel 7.2.x stable series (7.2.7-zen1), kernel.org tarball plus zen patch, sha256-pinned and signature-verified. The build refuses any other kernel release or a config below `config.require`, which enforces the cgroup v2 controllers, the eBPF floor (BPF JIT, BTF, BPF LSM), `sched_ext` and kernel hardening. An analytics profile tunes it for data work; full preemption is selected at boot for a responsive desktop. | EXISTS | `build/pkgbuilds/runink-kernel/` (`PKGBUILD`, `config.require`, `config.delta`, `README.md`), [docs/KERNEL.md](../KERNEL.md) |
| **Benchmark harness for the kernel.** `riverbench` compares kernels on the same machine with analytics-shaped loads (DuckDB TPC-H from a pinned CLI release, fio on ZFS and ext4, memory bandwidth, sort and hash-join), with warm-up, repetitions and the full kernel configuration recorded. **No benchmark results are published yet**, and none are claimed. | EXISTS (harness only) | `bench/analytics/`, [docs/KERNEL.md](../KERNEL.md#benchmarking) |
| **Encrypted ZFS root.** Every pool is an aes-256-gcm encryption root that every dataset inherits; the key is generated in memory, never written to a file, and shown once as the recovery key; `/home` is its own dataset; boot environments for rollback. OpenZFS 2.4.4 ships as a separate out-of-tree module package. | EXISTS | `installer/lib/10-disk-zfs.sh`, `build/pkgbuilds/runink-zfs/`, [docs/ENCRYPTION.md](../ENCRYPTION.md), [ZFS-LICENSING.md](ZFS-LICENSING.md) |
| **A graphical installer that measures before it erases.** `river-hwprobe` inventories CPU level, RAM, GPUs, disks, NICs, TPM and firmware; `river-plan` is a pure function from that inventory to an install plan (RAM budget, ZFS layout) or a REFUSED verdict below the documented minimums. A graphical, click-Next installer drives it and erases a disk only after the operator confirms its serial. Probe and planner are Go standard library only, with decisions pinned by table-driven tests on fixture probes. | EXISTS | `installer/` (`hwprobe`, `plan`, `internal/`, `gui/`), [docs/INSTALLER-HARDWARE.md](../INSTALLER-HARDWARE.md), [docs/INSTALL.md](../INSTALL.md) |
| **Confinement and least exposure.** `river-sandbox` (bubblewrap) runs code the machine does not trust with every namespace unshared, no capabilities, no network unless asked and a deny-list of secret paths; a default-deny nftables firewall for IPv4 and IPv6 from first boot; shadow-grade file modes re-asserted on every boot. | EXISTS | `iso-profiles/river/root-overlay/usr/local/bin/river-sandbox`, `runink-fw`, `river-perms`, [docs/SECURITY-ASSURANCE.md](../SECURITY-ASSURANCE.md) |
| **An offline install-guide agent.** `river-guide` answers install questions from the guide alone, with a local model on loopback and citations; any command in an answer must appear verbatim in the guide or the answer is withheld, and destructive steps are never executed from any channel. It is built with every image; a profile chooses whether its medium carries it (the Runink River medium does not, because its model is 1.1 GB). | EXISTS | `guide/`, `guide/model.lock`, [docs/INSTALL-GUIDE-AGENT.md](../INSTALL-GUIDE-AGENT.md) |
| **Signed releases and supply chain.** Every upstream pinned by version and checksum (and signature where upstream signs); `install.sh` verifies an OpenPGP signature over `SHA256SUMS`, then the ISO, and fails closed; SBOMs (SPDX, CycloneDX) and build provenance per release; signing happens offline, never in CI. The release key is published; **no release has been cut yet**. | EXISTS, first release pending | `install.sh`, `KEYS`, `base/river-sign`, `.github/workflows/release-gate.yml`, [docs/RELEASE-SIGNING.md](../RELEASE-SIGNING.md) |
| **A base for other distributions.** External profiles (`RIVER_PROFILE_DIR`), branding (`RIVER_BRANDING_DIR`) and payloads (`RIVER_PAYLOAD_DIR`) let a downstream build its own image from a pinned commit without forking, with installed-system checks it can add to the project's QEMU harness. | EXISTS | [docs/BUILD.md](../BUILD.md), "Downstream distributions"; [docs/PAYLOADS.md](../PAYLOADS.md) |

### Roadmap (no code on `main` yet)

The maintained list is [ROADMAP.md](../../ROADMAP.md).

| Item | Status | Plan |
| --- | --- | --- |
| **RIVER pipeline runtime (`riverd`)**: a Go library for the pipeline formats (TOML `.dsl` definitions, Go `.contract` typed data contracts, TOML `.herd` groupings, `@step` `io.Reader`→`io.Writer` steps, golden tests), then `riverd`, which runs pipelines with each step in `river-sandbox` and a cgroup v2 leaf, with a lineage record for every step input and output. | ROADMAP (plan only) | [runtime/README.md](../../runtime/README.md) |
| **Own from-source base** replacing the transitional Artix-derived image tooling: a reproducible toolchain, then the base built from recipes, then in-place upgrades as new boot environments. | ROADMAP (phase 0 scaffold exists: three recipes build reproducibly) | [docs/OWN-BASE.md](../OWN-BASE.md), `base/` |
| **Published kernel benchmarks** from `riverbench`, with the hardware, so they can be reproduced. | ROADMAP | [ROADMAP.md](../../ROADMAP.md) |
| **Secure Boot**: forced module signing, the project's own CA with MOK enrollment, then a shim through rhboot/shim-review. Secure Boot is off today. | ROADMAP | [docs/SECURE-BOOT.md](../SECURE-BOOT.md) |
| Reproducible ISO, unattended VM install tests in CI, TPM 2.0 unattended unlock. | ROADMAP | [docs/BUILD.md](../BUILD.md#reproducibility-status), [CI.md](CI.md), [docs/ENCRYPTION.md](../ENCRYPTION.md) |

### Why it is valuable

Developers who work with sensitive data, models and pipelines increasingly need to do that
work on machines they own and can account for: which kernel runs, what is installed, where
the data is encrypted, what may reach the network, and how untrusted code is contained.
Assembling that by hand on a general-purpose distribution is slow and leaves no record.
Runink River makes those decisions once, writes them down as invariants, checks them in CI,
and ships them as one verifiable image that other distributions can build on.

### Origin, history, ongoing development

Started in July 2026 by Runink, and built in the open-source shape from the start:
per-path licensing, DCO, public contributor rules ([AGENTS.md](../../AGENTS.md)), and
anything vendor-specific kept out of the tree as optional, out-of-tree profiles and
payloads. The kernel moved from a kernel.org LTS build to the zen stable fork in September
2026; the installer planner, the graphical installer and the guide agent landed the same
month, and on 2026-09-26 the project's scope became the developer workstation. The image is
still assembled with transitional Artix tooling while the own base is built. Development is
active; the repository is public at <https://github.com/org-runink/river>.

## Statement on alignment with LF AI & Data's mission

LF AI & Data supports open-source innovation in AI and data. Runink River's contribution is
the **operating-system layer under that work** for developers who keep it on their own
hardware:

- **A trustworthy place to do data and AI work.** Encrypted storage by default, a
  default-deny network posture, no telemetry and no hosted AI calls in the image, and every
  upstream pinned and verifiable.
- **Containment for code you did not write.** `river-sandbox` gives model-written code,
  checkouts and, later, pipeline steps a confined place to run.
- **Agents that are safe by construction.** The install guide agent shows the pattern the
  project applies: a local model, grounded answers, the system (not the model) deciding
  what may run, destructive actions never executed.
- **Data pipelines with contracts and lineage** (roadmap): the RIVER runtime's typed
  contracts, golden tests and per-step lineage target the data-quality and provenance
  problems LF AI & Data projects work on.
- **Measured performance for analytics**: a kernel profile and a reproducible benchmark
  harness built around analytics workloads.

**Honest scope note.** An operating system is an unusual LF AI & Data project; the
foundation's current portfolio is frameworks, model tooling and data systems. The closest
Linux Foundation precedent for hosting an operating system is **EVE-OS**, which is hosted
by **LF Edge**, not LF AI & Data. Much of Runink River is general OS engineering (init,
storage, packaging, installation) that could also fit another LF umbrella or LF Projects
directly. The case for LF AI & Data rests on the data and AI focus above and on the roadmap
runtime; the TAC may reasonably judge that another home is better, and the project would
take that feedback.

## Collaboration opportunities with current LF AI & Data projects

None has started; these are candidates the project would pursue:

- **OpenLineage / Marquez**: the natural format and store for the lineage record `riverd`
  must emit for every step (roadmap).
- **ONNX**: a portable model format for local development and inference on the workstation.
- **Data and ML tools developers run locally** (for example Milvus for vector search, or
  pipeline tools such as Flyte and Kedro): candidates for packaging, documentation and
  reference set-ups, and a comparison point for the RIVER pipeline formats.

## License name, version, and URL to license text

Per path, recorded in [REUSE.toml](../../REUSE.toml) (REUSE 3.3 compliant, checked in CI) and
explained in [docs/LICENSING.md](../LICENSING.md):

| Scope | Licence | Text |
| --- | --- | --- |
| Everything the project authors by default: installer, scripts, build tooling, configuration, Go code, documentation | **MIT** | [LICENSE](../../LICENSE) |
| Kernel packaging tree `build/pkgbuilds/runink-kernel/` | **GPL-2.0-only** | [LICENSES/GPL-2.0-only.txt](../../LICENSES/GPL-2.0-only.txt) |
| OpenZFS packaging tree `build/pkgbuilds/runink-zfs/` | **CDDL-1.0** | [LICENSES/CDDL-1.0.txt](../../LICENSES/CDDL-1.0.txt) |
| Project artwork (not code) | **CC-BY-4.0** | [LICENSES/CC-BY-4.0.txt](../../LICENSES/CC-BY-4.0.txt) |
| Model validation suite `validation/` (build-host tool, not shipped) | Apache-2.0 | [LICENSES/Apache-2.0.txt](../../LICENSES/Apache-2.0.txt) |
| Third-party files | Their upstream licences (0BSD, BSD-2-Clause, GPL-2.0-or-later, GPL-3.0-only, MIT) | [LICENSES/](../../LICENSES), [NOTICE](../../NOTICE) |
| The project mark and the Runink name | **Trademark, not licensed** (`LicenseRef-Runink-Trademark`) | [TRADEMARKS.md](../../TRADEMARKS.md), [NOTICE](../../NOTICE) |

**ZFS legal note.** GPL-2.0 and CDDL-1.0 are generally considered incompatible for a
combined work. Runink River keeps them apart as the distributions that ship ZFS do: OpenZFS
is never merged into or built with the kernel, ships as its own prebuilt module package
compiled against the kernel headers, and is loaded at runtime; both sources are pinned and
published. The boundary is documented in [ZFS-LICENSING.md](ZFS-LICENSING.md). A written
opinion from counsel has **not** been obtained yet (OWNER-TODO), and LF AI & Data's default
licence is Apache-2.0, so the non-default licences need the foundation's approval.

## Source control

Git, hosted on **GitHub**: <https://github.com/org-runink/river>, public. `main` is
protected: six CI checks (Tier 1, the Go test jobs, Go security analysis, REUSE, DCO) are
required for every merge, administrators included.

## Does the project sit in its own GitHub organization?

**Not yet.** It lives in the founding company's organisation. Moving it to a project-owned
organisation is OWNER-TODO (lifecycle task T2 in [LF-AIDATA.md](LF-AIDATA.md)).

## Do you have the GitHub DCO app active in the repos?

**Not yet.** The DCO is adopted ([CONTRIBUTING.md](../../CONTRIBUTING.md)) and every pull
request is checked by a DCO workflow (`.github/workflows/dco.yml`), which is a required
status check. Installing the GitHub DCO app is an organisation setting (OWNER-TODO).

## Issue tracker

**GitHub Issues** in the project repository (bugs, agreed features), **GitHub Discussions**
(questions and proposals), and GitHub private vulnerability reporting for security issues
([SECURITY.md](../../SECURITY.md)).

## Collaboration tools

Today: GitHub issues, pull requests and Discussions. Requested from LF AI & Data on
acceptance: a project mailing list, a security mailing list (to replace
`security@runink.org`), and a chat channel. OWNER-TODO: confirm.

## External dependencies

Build and image dependencies, with licences. Distribution package versions are fixed by the
pinned mirror snapshot (`pacman/mirrorlist.pin`); the rest are pinned by version and sha256
in the files named.

| Dependency | Version | Licence | Pinned in |
| --- | --- | --- | --- |
| Linux + zen patch | 7.2.7-zen1 | GPL-2.0-only | `build/pkgbuilds/runink-kernel/PKGBUILD` |
| OpenZFS | 2.4.4 | CDDL-1.0 | `build/pkgbuilds/runink-zfs/` |
| s6, s6-rc, s6-linux-init, skalibs, execline | distribution snapshot | ISC | mirror snapshot; own-base recipes in `base/recipes/` |
| KDE Plasma 6, PipeWire, SDDM | distribution snapshot | GPL / LGPL (upstream) | `iso-profiles/river/Packages-Root` (an explicit list, no meta-package) |
| bubblewrap | distribution snapshot | LGPL-2.0-or-later | mirror snapshot |
| nftables, OpenSSH, elogind, GRUB | distribution snapshot | GPL-2.0 / BSD / LGPL-2.1 / GPL-3.0 (upstream) | mirror snapshot |
| mistral.rs (guide model server) | v0.9.4 | MIT | `guide/model.lock` |
| Guide model: Qwen2.5-1.5B-Instruct GGUF | pinned revision | Apache-2.0 | `guide/model.lock` |
| DuckDB CLI (benchmark harness only) | 1.5.5 | MIT | `bench/analytics/duckdb.lock` |
| Go toolchain and standard library; the `river` CLI vendors cobra, pflag and viper and their dependencies | current stable | BSD-3-Clause, Apache-2.0, MIT | `*/go.mod`, `cli/vendor/` |
| Artix artools / buildiso (transitional ISO builder, build host only) | pinned fork base | GPL-3.0 | `scripts/fetch-iso-profiles.sh`, `scripts/patch-artools.go` |

## Initial committers

| Name | Email | Organisation | On the project since |
| --- | --- | --- | --- |
| Daniel Paes (@paesdan) | paes@runink.org | Runink | July 2026 (start) |

Commits are written with AI coding assistance under the maintainer's DCO sign-off
([CONTRIBUTING.md](../../CONTRIBUTING.md), "AI-assisted contributions").

## Have the project defined the roles of contributor, committer, maintainer?

**Yes.** Users, contributors, maintainers (committers) and the TSC are defined in
[GOVERNANCE.md](../../GOVERNANCE.md), with the path from contributor to maintainer; the
current maintainers are in [MAINTAINERS.md](../../MAINTAINERS.md), and
[.github/CODEOWNERS](../../.github/CODEOWNERS) mirrors it. **There is one maintainer; a
second is needed** (bus factor 1).

## Total number of contributors, with affiliations

**One** human contributor (Runink). No outside contributors yet.

## Release methodology

Documented in [RELEASE.md](../../RELEASE.md): calendar-versioned dated image sets
(`runink-os-YYYY.MM`), release criteria (CI, local lint, ISO build and VM install, a
dependency advisory check, a changelog with a **Security** section), offline signing, one
publication path (`release-gate.yml`), and [CHANGELOG.md](../../CHANGELOG.md) in Keep a
Changelog format. No fixed cadence yet (OWNER-TODO). No public release exists yet.

## Code of conduct

**Yes**: [CODE_OF_CONDUCT.md](../../CODE_OF_CONDUCT.md), Contributor Covenant 2.1. Its
contact (`conduct@runink.org`) is a placeholder until the mailbox is confirmed. OWNER-TODO:
keep it (as an approved alternate) or adopt the LF Projects Code of Conduct.

## OpenSSF (CII) Best Practices badge

**Not yet registered** (OWNER-TODO). The criterion-by-criterion self-assessment for
*passing* and *silver*, with evidence, is
[OPENSSF-BEST-PRACTICES.md](OPENSSF-BEST-PRACTICES.md): every *passing* MUST criterion is
met in the repository. Sandbox requires *passing*; Incubation requires *silver*.

## Infrastructure requests

- Hosting for release artefacts and the package repository (ISOs are several GiB).
- A dedicated, isolated KVM runner for boot and install tests (Tier 2 CI), per the
  isolation rules in [CI.md](CI.md).
- Foundation-held release-signing identity and security mailbox, once the project is
  vendor-neutral.

OWNER-TODO: confirm the list with LF AI & Data staff.

## Project website

Documentation: <https://docs.runink.org/river/>, built from `website/` in the repository.
There is no project-owned domain yet. OWNER-TODO: reserve a project domain and decide
whether LF AI & Data should create the website.

## Project governance

[GOVERNANCE.md](../../GOVERNANCE.md): lazy consensus with a public TSC vote as the fallback;
TSC votes (2/3) for invariant, licensing, trademark, charter and maintainer changes; every
decision recorded in the repository; neutrality and conflict-of-interest rules; a
published path from single-vendor to vendor-neutral governance with a seat cap per
employer. [CHARTER.md](../../CHARTER.md) is a draft technical charter on the LF Projects
template, to be finalised with LF staff.

## Social media accounts

None for the project. OWNER-TODO.

## Existing sponsorship

Development to date is funded by **Runink**, the founding company (maintainer time and
hardware). No other organisation has provided funding or support.

**LF AI & Data sponsor: OWNER-TODO.** Sandbox requires a sponsor who is an existing LF AI
& Data member (or a new member joining to sponsor); none is identified yet.

## Trademark

"Runink River" is **unregistered**; clearance searches (USPTO, EUIPO; Classes 9 and 42) are
pending (OWNER-TODO). On acceptance the project mark and logo would transfer to LF
Projects, LLC; whether the name "Runink" transfers or stays with Runink under a licence
back is an owner decision. Current policy: [TRADEMARKS.md](../../TRADEMARKS.md),
[NOTICE](../../NOTICE); plan: [CHARTER.md](../../CHARTER.md) §5.

## Milestones

In dependency order; the maintained version is [ROADMAP.md](../../ROADMAP.md).

| # | Milestone | Status | Needed for |
| --- | --- | --- | --- |
| 1 | Project GitHub organisation, 2FA, DCO app (public repository, private vulnerability reporting and branch protection are done) | owner-side | Sandbox |
| 2 | OpenSSF Best Practices *passing* | repository side done; registration owner-side | Sandbox |
| 3 | Sponsor, TAC presentation and vote, charter and contribution agreement | owner-side | Sandbox |
| 4 | First signed public release (the key is published) | in progress | Sandbox credibility |
| 5 | First reproducible kernel benchmark results published | roadmap (harness exists) | Positioning |
| 6 | Tier 2 CI: unattended encrypted install test in a VM | in progress | Silver |
| 7 | RIVER runtime: format library, then `riverd` with lineage | roadmap | Positioning |
| 8 | Own from-source base, reproducible ISO | in progress (phase 0) | Gold |
| 9 | Secure Boot (module signing, MOK, shim-review) | roadmap | Public installs |
| 10 | Second maintainer; three contributing organisations; TSC with a chair; OpenSSF *silver*; 500 stars | not started | Incubation |
