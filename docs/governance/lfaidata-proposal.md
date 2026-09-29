<!--
SPDX-FileCopyrightText: 2026 The RIVER Authors
SPDX-License-Identifier: MIT
-->

# LF AI & Data Sandbox project proposal: Runink River

> **Status: DRAFT, not submitted.** This is the text of Runink River's application to the
> LF AI & Data Foundation as a **Sandbox** project. It follows the
> [`lfai/proposing-projects` template](https://github.com/lfai/proposing-projects/blob/master/proposal-template.adoc)
> field by field. Items marked **OWNER-TODO** need the project owner before submission;
> the full list, with the lifecycle checklist, is in [LF-AIDATA.md](LF-AIDATA.md).
>
> **How to read it.** Every capability is marked **EXISTS** (on `main`, with the file
> paths a reviewer can open), **IN REVIEW** (code written, not yet on `main`) or
> **ROADMAP** (no code yet). Nothing here claims adopters, outside contributors, stars or
> benchmark results: there are none yet.

## Name of project

**Runink River** (always in full; a bare "River" is an unrelated Wayland compositor). The
images are **Runink River Server** and **Runink River Workstation**. **RIVER**
(*Raft-Integrated Validated Event Runtime*) names only the planned pipeline runtime,
`riverd`. The name is unique among LF AI & Data projects. Trademark clearance is pending
(see "Trademark").

## Requested project maturity level

**Sandbox.**

## Project description

**Runink River — an analytics-intensive data and agentic operating system.**

Runink River is an appliance-grade Linux distribution for running data pipelines, local
model inference and AI agents on hardware the operator owns, including air-gapped sites. It
installs identically on every node: s6 init (never systemd), one pinned kernel, an
encrypted ZFS root, a dual-stack host with an IPv6-only cluster network behind a
default-deny firewall, and a k0s Kubernetes cluster for the workloads. Nothing in the image fetches from the internet at
runtime, and no secret or model is baked into it.

What makes it a *data and agent* operating system rather than a generic server
distribution is where the engineering has gone: sizing a machine for local model tiers
before installing, pinning and validating the models a node may serve, an install agent
that runs a local model offline and cannot execute what the model says, confinement for
code that agents and pipelines submit, and a planned pipeline runtime with validated data
contracts and lineage.

### What exists today

| Capability | Status | Where to look |
| --- | --- | --- |
| **Zen-based kernel with an enforced config floor.** `linux-runink` is a pinned fork of the zen kernel 7.2.x stable series (7.2.7-zen1), kernel.org tarball plus zen patch, sha256-pinned and signature-verified. The build refuses any other kernel release or a config below `config.require`, which enforces the cgroup v2 controllers, the eBPF floor (BPF JIT always on, BTF, BPF LSM, cgroup BPF, XDP sockets), `sched_ext` (`SCHED_CLASS_EXT`) and kernel hardening (init-on-alloc/free, lockdown LSM, SHA-512 module signing). | EXISTS | `build/pkgbuilds/runink-kernel/` (`PKGBUILD`, `config.require`, `config.delta`, `README.md`) |
| **Analytics-tuned kernel profile and benchmark harness.** An analytics profile for the kernel config and a benchmark harness to measure it. | IN REVIEW (not on `main`) | Lands in `build/pkgbuilds/runink-kernel/` when merged. **No benchmark results exist**, and none are claimed. |
| **Encrypted ZFS root.** Every pool is an aes-256-gcm encryption root that every dataset inherits; the key is generated in memory, never written to a file, and printed once as the recovery key; boot environments; shadow-grade file modes re-asserted on every boot; raw (still-encrypted) backups. OpenZFS 2.4.4 ships as a separate out-of-tree module package. | EXISTS | `installer/lib/10-disk-zfs.sh`, `build/pkgbuilds/runink-zfs/`, [docs/ENCRYPTION.md](../ENCRYPTION.md), [ZFS-LICENSING.md](ZFS-LICENSING.md) |
| **Hardware-adaptive install planner.** `river-hwprobe` inventories CPU level and topology, RAM, GPUs, disks, NICs, TPM and firmware; `river-plan` is a pure function from that inventory and a models manifest to an install plan: which model tiers fit at what context length, KV-cache budget and thread count, the RAM budget, the ZFS layout across the disks found, and a REFUSED verdict below the minimums. Go standard library only; decisions pinned by table-driven tests on fixture probes. | EXISTS | `installer/hwprobe`, `installer/plan`, `installer/internal/{hw,planner}`, [docs/INSTALLER-HARDWARE.md](../INSTALLER-HARDWARE.md) |
| **Local model tiers: pinned, budgeted, validated.** `models.lock` pins every model a node may serve by upstream repository, exact revision, size and sha256, with model-card evidence per row; models are fetched and verified at build time only, never by a node. `models.tiers` gives the planner each tier's measured resident memory and KV cost. A validation suite runs each candidate model behind its serving engine against hard per-tier gates (context retrieval, output format, languages, speed, memory) and records the evidence. | EXISTS, with limits (next column) | `models.lock`, `models.evidence.json`, `models.tiers`, `build/models-fetch.sh`, `build/50-models-manifest.sh`, `validation/` ([SELECTION.md](../../validation/SELECTION.md), `results.json`). **Limits:** the tier servers themselves run as a workload on the cluster, not as a base-image service; measurements come from one CPU-only workstation; the vision and speech-to-text picks each fail one hard gate and text-to-speech has no passing model, as `SELECTION.md` records; the general and vision tiers are not pinned yet. |
| **Sovereign install-guide agent.** `river-guide` runs on the install medium, fully offline: a local model (pinned in `guide-model.lock`, served by a pinned mistral.rs build on loopback as `nobody`) answers questions only from retrieved guide sections, with citations. The agent enforces grounding itself: any command in an answer must appear verbatim in the guide, or the answer is withheld; destructive steps are never executed from any channel. An optional GitHub issue channel lets a remote colleague follow the install, with redaction. Go standard library only; a threat model and an evaluation set are in the tree. | EXISTS | `guide/`, `guide/model.lock`, [docs/INSTALL-GUIDE-AGENT.md](../INSTALL-GUIDE-AGENT.md) |
| **Workload substrate and confinement.** s6/s6-rc service tree; k0s (single binary, its own containerd), IPv6-only pod and service networks with NAT64/DNS64; eBPF-capable kernel (the floor above; Runink River ships no eBPF programs of its own yet); `river-sandbox`, a bubblewrap wrapper that confines code a node compiles or runs for others: all namespaces unshared, no capabilities, no network unless asked, and a deny-list of secret paths it refuses to bind. | EXISTS | `iso-profiles/river/root-overlay/etc/s6/`, `build/pkgbuilds/runink-k0s/`, `iso-profiles/river/root-overlay/usr/local/bin/river-sandbox`, `runink-fw`, [docs/ARCHITECTURE.md](../ARCHITECTURE.md) |
| **Signed releases, supply chain.** Every upstream pinned by version and checksum (and signature where upstream signs); `install.sh` verifies an OpenPGP signature over `SHA256SUMS`, then the ISO, and fails closed; SBOMs (SPDX, CycloneDX) and build provenance per release; signing happens offline, never in CI. **The release key does not exist yet**, so no release can be verified today. | EXISTS, key pending | `install.sh`, `KEYS`, `base/river-sign`, `.github/workflows/release-attest.yml`, [docs/RELEASE-SIGNING.md](../RELEASE-SIGNING.md) |

### Roadmap (no code on `main` yet)

| Item | Status | Plan |
| --- | --- | --- |
| **RIVER pipeline runtime (`riverd`)**: a Go library for the pipeline formats (TOML `.dsl` definitions, Go `.contract` typed data contracts, TOML `.herd` groupings, `@step` `io.Reader`→`io.Writer` steps, golden tests), then `riverd`, which places and runs pipelines on the cluster over mTLS, each step in `river-sandbox` and a cgroup v2 leaf, with a lineage record for every step input and output. | ROADMAP (plan only) | [runtime/README.md](../../runtime/README.md) |
| **Own from-source base** replacing the transitional Artix-derived image tooling: reproducible toolchain, then the server base built from recipes. | ROADMAP (phase 0 scaffold exists: three recipes build reproducibly) | [docs/OWN-BASE.md](../OWN-BASE.md), `base/` |
| **Benchmark results** for the analytics kernel profile, published with the harness and hardware so they can be reproduced. | ROADMAP | [ROADMAP.md](../../ROADMAP.md) |
| **Secure Boot**: forced module signing, an organisation CA with MOK enrollment, then a Runink River shim through rhboot/shim-review. Secure Boot is off today. | ROADMAP | [docs/SECURE-BOOT.md](../SECURE-BOOT.md) |
| Reproducible ISO, unattended VM install tests in CI, TPM2 unattended unlock. | ROADMAP | [docs/BUILD.md](../BUILD.md#reproducibility-status), [CI.md](CI.md), [docs/ENCRYPTION.md](../ENCRYPTION.md) |

### Why it is valuable

Organisations that must keep data and models on their own hardware (regulated industries,
public sector, research sites, air-gapped plants) assemble the same stack by hand: a
hardened OS, encrypted storage, a small Kubernetes, a local inference server, a way to
decide which models fit which machine, and a way to confine the code agents write.
Runink River makes that stack one reproducible, verifiable image with those decisions written
down as invariants and checked in CI.

### Origin, history, ongoing development

Started in July 2026 by Runink as the base for its own self-hosted infrastructure, and
built in the open-source shape from the start: per-path licensing, DCO, public contributor
rules ([AGENTS.md](../../AGENTS.md)), and downstream products kept out of the tree as
optional build-time payloads. The kernel moved from a kernel.org LTS build to the zen
stable fork in September 2026; the installer planner, the guide agent, the pinned model set
and the validation suite landed the same month. The distribution boots and installs; the
image is still assembled with transitional Artix tooling while the own base is built.
Development is active; the public history starts at publication (the earlier private
history is kept as an archive).

## Statement on alignment with LF AI & Data's mission

LF AI & Data supports open-source innovation in AI and data. Runink River's contribution is
the **operating-system layer for sovereign AI and data workloads**: the place where
models, pipelines and agents actually run when they must run on-premises.

- **Local inference is a first-class OS concern.** Model tiers are sized by the installer
  from measured memory and KV-cache costs, models are pinned by hash and verified at build
  time, and a validation suite decides which model a tier gets. That is AI engineering
  done at the OS level, not an application bolted on top.
- **Agents that are safe by construction.** The install guide agent shows the pattern
  Runink River applies to agent workloads: local model, grounded answers, the system (not
  the model) decides what may run, destructive actions never executed, confinement for
  anything an agent submits.
- **Data pipelines with contracts and lineage** (roadmap): the RIVER runtime's typed
  contracts, golden tests and per-step lineage target the data-quality and provenance
  problems LF AI & Data projects work on.
- **Sovereignty**: no third-party hosted AI API, no telemetry, no runtime fetch, encrypted
  storage by default.

**Honest scope note.** An operating system is an unusual LF AI & Data project; the
foundation's current portfolio is frameworks, model tooling and data systems. The closest
Linux Foundation precedent for hosting an operating system is **EVE-OS**, which is hosted
by **LF Edge**, not LF AI & Data. Much of Runink River is general OS engineering (init,
storage, networking, packaging) that would also fit LF Edge. The case for LF AI & Data rests
on the data and agent layer described above and on the roadmap runtime; the TAC may reasonably
judge that LF Edge is the better home, and the project would take that feedback.

## Collaboration opportunities with current LF AI & Data projects

None has started; these are candidates the project would pursue:

- **ONNX**: a portable model format for the local model tiers, alongside the GGUF and
  safetensors files pinned today.
- **OpenLineage / Marquez**: the natural format and store for the lineage record `riverd`
  must emit for every step (roadmap).
- **Milvus**: a vector database as a reference workload on the cluster for retrieval
  over local data.
- **OPEA**: its on-premises enterprise AI reference architectures could target Runink River
  as a base OS.
- **Flyte, Kedro**: pipeline tools that could run on the k0s cluster, and a comparison
  point for the RIVER pipeline formats.

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
| Runink brand assets and the project mark | **Trademark, not licensed** (`LicenseRef-Runink-Trademark`) | [NOTICE](../../NOTICE) |

**ZFS legal note.** GPL-2.0 and CDDL-1.0 are generally considered incompatible for a
combined work. Runink River keeps them apart as the distributions that ship ZFS do: OpenZFS
is never merged into or built with the kernel, ships as its own prebuilt module package
compiled against the kernel headers, and is loaded at runtime; both sources are pinned and
published. The boundary is documented in [ZFS-LICENSING.md](ZFS-LICENSING.md). A written
opinion from counsel has **not** been obtained yet (OWNER-TODO), and LF AI & Data's default
licence is Apache-2.0, so the non-default licences need the foundation's approval.

## Source control

Git, hosted on **GitHub**: <https://github.com/org-runink/river>. It is public today with its
development history. The recommended remedy, an owner decision, is to republish it from a
single reviewed commit and keep the earlier history as a private archive
([docs/PUBLICATION.md](../PUBLICATION.md)).

## Does the project sit in its own GitHub organization?

**Not yet.** It lives in the founding company's organisation. Moving it to a project-owned
organisation is OWNER-TODO (lifecycle task T2 in [LF-AIDATA.md](LF-AIDATA.md)).

## Do you have the GitHub DCO app active in the repos?

**Not yet.** The DCO is adopted ([CONTRIBUTING.md](../../CONTRIBUTING.md)) and every pull
request is checked by a DCO workflow (`.github/workflows/dco.yml`). Installing the GitHub
DCO app is an organisation setting (OWNER-TODO).

## Issue tracker

**GitHub Issues** in the project repository (bugs, features, roadmap), GitHub private
vulnerability reporting for security issues ([SECURITY.md](/SECURITY.md)).

## Collaboration tools

Today: GitHub issues and pull requests only. Requested from LF AI & Data on acceptance: a
project mailing list, a security mailing list (to replace `security@runink.org`), and a
Slack channel. OWNER-TODO: confirm.

## External dependencies

Runtime and build dependencies, with licences. Distribution package versions are fixed by
the pinned mirror snapshot (`pacman/mirrorlist.pin`); the rest are pinned by version and
sha256 in the files named.

| Dependency | Version | Licence | Pinned in |
| --- | --- | --- | --- |
| Linux + zen patch | 7.2.7-zen1 | GPL-2.0-only | `build/pkgbuilds/runink-kernel/PKGBUILD` |
| OpenZFS | 2.4.4 | CDDL-1.0 | `build/pkgbuilds/runink-zfs/` |
| k0s | v1.31.2+k0s.0 | Apache-2.0 | `build/config.env`, `build/pkgbuilds/runink-k0s/` |
| s6, s6-rc, s6-linux-init, skalibs, execline | distribution snapshot | ISC | mirror snapshot; own-base recipes in `base/recipes/` |
| bubblewrap | distribution snapshot | LGPL-2.0-or-later | mirror snapshot |
| nftables, OpenSSH, rsync, elogind, GRUB | distribution snapshot | GPL-2.0 / BSD / GPL-3.0 / LGPL-2.1 / GPL-3.0 (upstream) | mirror snapshot |
| tayga (NAT64) | see PKGBUILD | GPL-2.0 | `build/pkgbuilds/runink-tayga/` |
| mistral.rs (guide model server) | v0.9.4 | MIT | `guide/model.lock` |
| Guide model: Qwen2.5-1.5B-Instruct GGUF | pinned revision | Apache-2.0 | `guide/model.lock` |
| Tier models: Qwen3-14B GGUF, Voxtral-Mini-4B-Realtime (embedding: pending re-selection) | pinned revisions | Apache-2.0 | `models.lock` |
| Go toolchain and standard library (the Go code has no third-party modules) | current stable | BSD-3-Clause | `installer/go.mod`, `guide/go.mod`, `validation/go.mod` |
| Artix artools / buildiso (transitional ISO builder, build host only) | pinned fork base | GPL-3.0 | `scripts/fetch-iso-profiles.sh`, `scripts/patch-artools.go` |

## Initial committers

| Name | Email | Organisation | On the project since |
| --- | --- | --- | --- |
| Daniel Paes (@paesdan) | paes@runink.org | Runink | July 2026 (start) |

Commits are written with AI coding assistance under the maintainer's DCO sign-off
([CONTRIBUTING.md](../../CONTRIBUTING.md), "AI-assisted contributions").

## Have the project defined the roles of contributor, committer, maintainer?

**Yes.** Users, contributors, maintainers and the TSC are defined in
[GOVERNANCE.md](../../GOVERNANCE.md); the current maintainers are in
[MAINTAINERS.md](../../MAINTAINERS.md), and [.github/CODEOWNERS](../../.github/CODEOWNERS)
mirrors it. **There is one maintainer; a second is needed** (bus factor 1).

## Total number of contributors, with affiliations

**One** human contributor (Runink). No outside contributors yet.

## Release methodology

Documented in [RELEASE.md](../../RELEASE.md): calendar-versioned dated image sets
(`runink-os-YYYY.MM`), release criteria (CI, local lint, ISO build and VM install, a
dependency advisory check, a changelog with a **Security** section), offline signing, and
[CHANGELOG.md](../../CHANGELOG.md) in Keep a Changelog format. No fixed cadence yet
(OWNER-TODO). One dated image has been cut privately; no public release exists.

## Code of conduct

**Yes**: [CODE_OF_CONDUCT.md](../../CODE_OF_CONDUCT.md), Contributor Covenant 2.1. Its
contact (`conduct@runink.org`) is a placeholder until the mailbox is confirmed. OWNER-TODO:
keep it (as an approved alternate) or adopt the LF Projects Code of Conduct.

## OpenSSF (CII) Best Practices badge

**Not yet registered.** Registration needs the public repository URL (OWNER-TODO). The
criterion-by-criterion self-assessment for *passing* and *silver*, with evidence and the
fixes already made, is [OPENSSF-BEST-PRACTICES.md](OPENSSF-BEST-PRACTICES.md). Sandbox
requires *passing*; Incubation requires *silver*.

## Infrastructure requests

- Hosting for release artefacts and the package repository (ISOs are several GiB).
- A dedicated, isolated KVM runner for boot and install tests (Tier 2 CI), per the
  isolation rules in [CI.md](CI.md).
- Foundation-held release-signing identity and security mailbox, once the project is
  vendor-neutral.

OWNER-TODO: confirm the list with LF AI & Data staff.

## Project website

None for the project itself yet; `runink.org` is the company's site. OWNER-TODO: reserve a
project domain and decide whether LF AI & Data should create the website.

## Project governance

[GOVERNANCE.md](../../GOVERNANCE.md): lazy consensus; TSC votes (2/3) for invariant,
licensing, trademark, charter and maintainer changes; a published path from single-vendor
to vendor-neutral governance with a seat cap per employer. [CHARTER.md](../../CHARTER.md) is a
draft technical charter on the LF Projects template, to be finalised with LF staff.

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
Projects, LLC; whether the vendor mark "Runink" transfers or stays with Runink under a
licence back is an owner decision. Current policy: [NOTICE](../../NOTICE); plan:
[docs/LICENSING.md](../LICENSING.md) §5 and [CHARTER.md](../../CHARTER.md) §5.

## Milestones

In dependency order; the maintained version is [ROADMAP.md](../../ROADMAP.md).

| # | Milestone | Status | Needed for |
| --- | --- | --- | --- |
| 1 | Public repository, 2FA, DCO app, private vulnerability reporting, branch protection | owner-side | Sandbox |
| 2 | OpenSSF Best Practices *passing* | repository side done; registration owner-side | Sandbox |
| 3 | Sponsor, TAC presentation and vote, charter and contribution agreement | owner-side | Sandbox |
| 4 | Release key and first signed public release | owner-side (key) | Sandbox credibility |
| 5 | Analytics kernel profile and benchmark harness merged; first reproducible benchmark results published | in review / roadmap | Positioning |
| 6 | Tier 2 CI: KVM runner, unattended encrypted install test | in progress | Silver |
| 7 | RIVER runtime: format library, then `riverd` with lineage | roadmap | Positioning |
| 8 | Own from-source base, reproducible ISO | in progress (phase 0) | Gold |
| 9 | Secure Boot (module signing, MOK, shim-review) | roadmap | Public installs |
| 10 | Second maintainer; three contributing organisations; TSC with a chair; OpenSSF *silver*; 500 stars | not started | Incubation |
