<!--
SPDX-FileCopyrightText: 2026 The RIVER Authors
SPDX-License-Identifier: MIT
-->

# Runink River: OpenSSF Best Practices self-assessment

The answers Runink River will enter for the [OpenSSF Best Practices badge](https://www.bestpractices.dev/),
criterion by criterion, for the **passing** and **silver** levels. The criteria IDs and
categories are those of the badge's
[criteria.yml](https://github.com/coreinfrastructure/best-practices-badge/blob/main/criteria/criteria.yml)
as of 2026-09-24 (retired and future criteria left out). **Not submitted:** registration
needs the public repository URL (owner-side). LF AI & Data requires *passing* for Sandbox
and *silver* for Incubation ([LF-AIDATA.md](LF-AIDATA.md)).

Columns:

- **Req.**: MUST, SHOULD or SUGG(ested), as the badge defines them.
- **Before** / **After**: the state before and after the pull request that added this
  document (the fixes it made are in the **Fix** column, marked *fixed*).
- **Status**: **Met**, **Unmet**, **N/A** (the badge allows N/A and the justification is
  given), **Owner** (only the owner can meet it: an account, a repository setting, a key or
  a person).

## Summary

| Level | Criteria | Before: Met / Unmet / N/A / Owner | After: Met / Unmet / N/A / Owner |
| --- | --- | --- | --- |
| Passing | 67 | 54 / 7 / 2 / 4 | **59 / 2 / 2 / 4** |
| Silver | 55 | 35 / 12 / 3 / 5 | **40 / 7 / 3 / 5** |

After this change every **passing** MUST that a pull request can meet is met; the two
unmet passing items are SUGGESTED. Passing is reached once the four owner items are done
(publication, the public tracker and archive, private reporting). For **silver**, five
MUST criteria remain unmet and need engineering (listed under "Silver: what is left"), plus
the five owner items.

## Passing level

### Basics

| Criterion | Req. | Before | After | Evidence | Fix / note |
| --- | --- | --- | --- | --- | --- |
| `description_good` | MUST | Met | Met | `README.md` first paragraph | |
| `interact` | MUST | Met | Met | `README.md` (install, contributing), `SUPPORT.md`, `SECURITY.md` | `SUPPORT.md` added |
| `contribution` | MUST | Met | Met | `CONTRIBUTING.md` | |
| `contribution_requirements` | SHOULD | Met | Met | `CONTRIBUTING.md` (style, tests, DCO), `AGENTS.md` | |
| `floss_license` | MUST | Met | Met | `LICENSE` (MIT), `REUSE.toml`, `docs/LICENSING.md` | |
| `floss_license_osi` | SUGG | Met | Met | MIT, GPL-2.0-only, CDDL-1.0, Apache-2.0 and the third-party licences are OSI-approved; artwork is CC-BY-4.0 and brand assets are a trademark, neither is software | |
| `license_location` | MUST | Met | Met | `LICENSE`, `LICENSES/` | Added the missing `LICENSES/Apache-2.0.txt` (REUSE lint was failing) |
| `documentation_basics` | MUST | Met | Met | `README.md`, `docs/INSTALL.md`, `docs/BUILD.md`, `docs/ARCHITECTURE.md` | |
| `documentation_interface` | MUST | Met | Met | `docs/INSTALLER-HARDWARE.md` (CLI and JSON contract of `river-hwprobe`/`river-plan`), `docs/INSTALL-GUIDE-AGENT.md`, `docs/ENCRYPTION.md` (key-provider contract), `docs/BUILD.md` (payload contract) | |
| `sites_https` | MUST | Met | Met | GitHub; `https://runink.org` | |
| `discussion` | MUST | Owner | Owner | GitHub Issues | Searchable and open to newcomers only once the repository is public |
| `english` | SHOULD | Met | Met | All documentation | |
| `maintained` | MUST | Met | Met | Active development since July 2026 | |

### Change control

| Criterion | Req. | Before | After | Evidence | Fix / note |
| --- | --- | --- | --- | --- | --- |
| `repo_public` | MUST | Owner | Owner | | `org-runink/river` is public (2026-09-28) with its full history; [docs/PUBLICATION.md](../PUBLICATION.md) is the owner's step-by-step, including republishing from a clean snapshot |
| `repo_track` | MUST | Met | Met | git | |
| `repo_interim` | MUST | Met | Met | `main` between releases | |
| `repo_distributed` | SUGG | Met | Met | git | |
| `version_unique` | MUST | Met | Met | `runink-os-YYYY.MM` tags, `VERSION`, `/etc/runink-os-version` | |
| `version_semver` | SUGG | Met | Met | Calendar versioning (CalVer) | *fixed*: a micro level (`YYYY.MM.N`) for a second release in a month, `RELEASE.md` |
| `version_tags` | SUGG | Met | Met | release tags | |
| `release_notes` | MUST | Unmet | Met | `CHANGELOG.md` | *fixed*: `CHANGELOG.md` (Keep a Changelog), release steps in `RELEASE.md`, the PR checklist and `CONTRIBUTING.md` require an entry |
| `release_notes_vulns` | MUST | Unmet | Met | `CHANGELOG.md` "Convention" | *fixed*: a mandatory **Security** section listing every fixed vulnerability with its CVE/advisory ID ("None." when there are none) |

### Reporting

| Criterion | Req. | Before | After | Evidence | Fix / note |
| --- | --- | --- | --- | --- | --- |
| `report_process` | MUST | Met | Met | `CONTRIBUTING.md` "Reporting bugs", `SUPPORT.md` | |
| `report_tracker` | SHOULD | Met | Met | GitHub Issues | |
| `report_responses` | MUST | Met | Met | No external bug reports in the window | Re-assess after publication |
| `enhancement_responses` | SHOULD | Met | Met | No external requests in the window | Re-assess after publication |
| `report_archive` | MUST | Owner | Owner | GitHub Issues | Public archive only once the repository is public |
| `vulnerability_report_process` | MUST | Met | Met | `SECURITY.md` | |
| `vulnerability_report_private` | MUST | Owner | Owner | `SECURITY.md` (encrypted mail, GitHub private reporting) | Owner: enable private vulnerability reporting, confirm `security@runink.org`, publish the release key used to encrypt reports |
| `vulnerability_report_response` | MUST | Met | Met | `SECURITY.md`: acknowledge within 3 working days | No reports in the last 6 months |

### Quality

| Criterion | Req. | Before | After | Evidence | Fix / note |
| --- | --- | --- | --- | --- | --- |
| `build` | MUST | Met | Met | `Makefile` (`make components`, `make iso`, `make iso-in-builder`) | |
| `build_common_tools` | SUGG | Met | Met | make, makepkg, go | |
| `build_floss_tools` | SHOULD | Met | Met | All build tools are FLOSS | |
| `test` | MUST | Met | Met | `make test`, `make test-guide`, `scripts/ci-tier1.sh`, `tests/` | |
| `test_invocation` | SHOULD | Met | Met | `make test`, `make test-guide`, `sh scripts/ci-tier1.sh` | |
| `test_most` | SUGG | Unmet | Unmet | Go code: installer 84.7 %, guide 76.5 % statement coverage (measured 2026-09-24); lints over every shell script | Boot, installer-on-disk, ZFS and encryption paths are tested by hand (`make vmtest`) until Tier 2 CI has a runner (`docs/governance/CI.md`) |
| `test_continuous_integration` | SUGG | Met | Met | `.github/workflows/ci.yml` on every PR and push | |
| `test_policy` | MUST | Met | Met | `AGENTS.md` "Tests"; now also `CONTRIBUTING.md` "Tests" | *fixed*: the policy is in `CONTRIBUTING.md` with a table of where each kind of test goes |
| `tests_are_added` | MUST | Met | Met | Recent major changes came with tests: the planner's fixture tests, `guide/` tests, the validation suite | |
| `tests_documented_added` | SUGG | Unmet | Met | `CONTRIBUTING.md` "Making a change" step 7, `.github/pull_request_template.md` | *fixed* |
| `warnings` | MUST | Met | Met | shellcheck, `go vet`, gofmt in CI | |
| `warnings_fixed` | MUST | Met | Met | CI fails on any finding | |
| `warnings_strict` | SUGG | Unmet | Unmet | shellcheck runs at `--severity=warning` with four exclusions; Go uses `go vet` defaults | Fix: raise shellcheck to `style` (or justify each exclusion inline) and add staticcheck |

### Security

| Criterion | Req. | Before | After | Evidence | Fix / note |
| --- | --- | --- | --- | --- | --- |
| `know_secure_design` | MUST | Met | Met | `AGENTS.md` invariants, `docs/SECURITY-ASSURANCE.md` §4 | The owner attests this on the form |
| `know_common_errors` | MUST | Met | Met | `docs/SECURITY-ASSURANCE.md` §5 | The owner attests this on the form |
| `crypto_published` | MUST | Met | Met | aes-256-gcm (ZFS), OpenPGP Ed25519/RSA-4096, Ed25519 bundle signatures, SHA-256 | |
| `crypto_call` | SHOULD | Met | Met | No project-written cryptography: OpenZFS, GnuPG, Go `crypto/*` | |
| `crypto_floss` | MUST | Met | Met | All FLOSS | |
| `crypto_keylength` | MUST | Met | Met | AES-256, Ed25519, RSA-4096 | |
| `crypto_working` | MUST | Met | Met | No MD5/SHA-1/DES for security; `MODULE_SIG_SHA1` disabled | The SPDX 2.x SBOM document reference carries a SHA-1 checksum because that format requires it; it is not a security control |
| `crypto_weaknesses` | SHOULD | Met | Met | as above | |
| `crypto_pfs` | SHOULD | Met | Met | TLS 1.2+ (ECDHE) and SSH key exchange | |
| `crypto_password_storage` | MUST | N/A | N/A | The project stores no user passwords; node accounts use the system's shadow file | |
| `crypto_random` | MUST | Met | Met | Pool key from `/dev/urandom` (`installer/lib/10-disk-zfs.sh`); Go `crypto/rand` | |
| `delivery_mitm` | MUST | Met | Met | HTTPS only (`install.sh`: `--proto '=https'`, `--tlsv1.2`) plus a signed `SHA256SUMS` | |
| `delivery_unsigned` | MUST | Met | Met | `install.sh` verifies the signature before trusting the checksums, and fails closed without a published key | |
| `vulnerabilities_fixed_60_days` | MUST | Met | Met | No known unpatched medium-or-higher vulnerability | *fixed*: `SECURITY.md` previously allowed 90 days; it now commits to 60 days for publicly known medium-or-higher issues |
| `vulnerabilities_critical_fixed` | SHOULD | Unmet | Met | `SECURITY.md` targets | *fixed*: critical 14 days, high 30 days (was 90 days for both) |
| `no_leaked_credentials` | MUST | Met | Met | gitleaks over every change and the full tree (gatekeeper); pre-publication tree scan | |

### Analysis

| Criterion | Req. | Before | After | Evidence | Fix / note |
| --- | --- | --- | --- | --- | --- |
| `static_analysis` | MUST | Met | Met | shellcheck (every script), `go vet`, actionlint and zizmor (workflows), gitleaks | |
| `static_analysis_common_vulnerabilities` | SUGG | Unmet | Met | CI job `go-security`: **gosec** (CWE-mapped rules) over `installer/` and `guide/`; **govulncheck** over all Go modules | *fixed*: job added; findings triaged with `#nosec <rule> -- <reason>` at the line, G304 excluded with the reason in `ci.yml` |
| `static_analysis_fixed` | MUST | Met | Met | CI blocks on findings | |
| `static_analysis_often` | SUGG | Met | Met | Every PR and push | |
| `dynamic_analysis` | SUGG | Met | Met | `go test -race` for `guide/` | *fixed*: `installer-go` also runs under the race detector |
| `dynamic_analysis_unsafe` | SUGG | N/A | N/A | Project code is Go and POSIX shell (memory-safe); no C/C++ is authored here | |
| `dynamic_analysis_enable_assertions` | SUGG | Met | Met | The race detector's runtime checks are on in CI test runs, off in release builds | |
| `dynamic_analysis_fixed` | MUST | Met | Met | No dynamic-analysis finding is open | |

## Silver level

| Criterion | Req. | Before | After | Evidence | Fix / note |
| --- | --- | --- | --- | --- | --- |
| `achieve_passing` | MUST | Owner | Owner | | After the passing owner items |
| `contribution_requirements` | MUST | Met | Met | `CONTRIBUTING.md` | |
| `dco` | SHOULD | Met | Met | `CONTRIBUTING.md`, `.github/workflows/dco.yml` | Owner: make it a required check |
| `governance` | MUST | Met | Met | `GOVERNANCE.md`, `CHARTER.md` (draft) | |
| `code_of_conduct` | MUST | Met | Met | `CODE_OF_CONDUCT.md` (Contributor Covenant 2.1) | Contact mailbox is a placeholder (owner) |
| `roles_responsibilities` | MUST | Met | Met | `GOVERNANCE.md`, `MAINTAINERS.md`, `.github/CODEOWNERS` | `CODEOWNERS` added |
| `access_continuity` | MUST | Owner | Owner | | One person holds admin, merge and (future) signing rights. Owner: a second admin/maintainer and a key-recovery plan |
| `bus_factor` | SHOULD | Owner | Owner | `MAINTAINERS.md` | Bus factor 1 |
| `documentation_roadmap` | MUST | Unmet | Met | `ROADMAP.md` | *fixed*: milestones for the next year, and what is not planned |
| `documentation_architecture` | MUST | Met | Met | `docs/ARCHITECTURE.md` | |
| `documentation_security` | MUST | Met | Met | `SECURITY.md` (known weaknesses), `docs/ENCRYPTION.md`, `docs/SECURITY-ASSURANCE.md` §6 | |
| `documentation_quick_start` | MUST | Met | Met | `README.md` "Install" | |
| `documentation_current` | MUST | Met | Met | | *fixed*: a stale "models.tiers is not written yet" note in `docs/INSTALLER-HARDWARE.md` |
| `documentation_achievements` | MUST | Met | Met | `README.md` "Project status" | *fixed*: the section links the badge once awarded; nothing is claimed before |
| `accessibility_best_practices` | SHOULD | N/A | N/A | No project-authored GUI; the installer and guide are text consoles | |
| `internationalization` | SHOULD | Unmet | Unmet | Installer and guide messages are English only | Fix: message catalogues for the installer TUI and `river-guide` |
| `sites_password_security` | MUST | N/A | N/A | Project sites store no user passwords (GitHub) | |
| `maintenance_or_update` | MUST | Unmet | Unmet | Only the latest release is supported (`SECURITY.md`) | Fix: document the in-place upgrade path (install a new boot environment from a new release, keep the pool and enrollment); today the documented path is a reinstall |
| `report_tracker` | MUST | Met | Met | GitHub Issues | |
| `vulnerability_report_credit` | MUST | Met | Met | `SECURITY.md` (credit in the advisory and release notes) | No vulnerabilities resolved in the last 12 months |
| `vulnerability_response_process` | MUST | Met | Met | `SECURITY.md` "How maintainers handle a report" | *fixed*: the handling process (triage with CVSS, private fix with a regression check, coordination, release, follow-up) is now written down, not only the timelines |
| `coding_standards` | MUST | Met | Met | `AGENTS.md` (POSIX sh, `set -eu`, shellcheck), gofmt | |
| `coding_standards_enforced` | MUST | Met | Met | shellcheck and gofmt in CI | |
| `build_standard_variables` | MUST | Met | Met | Go is built with `CGO_ENABLED=0` and honours `GOFLAGS`; C packages go through makepkg, which applies `CFLAGS`/`LDFLAGS` | |
| `build_preserve_debug` | SHOULD | Unmet | Unmet | Go release binaries use `-ldflags "-s -w"` unconditionally | Fix: let `build/20-installer-binaries.sh` and `build/25-river-guide.sh` keep symbols on request |
| `build_non_recursive` | MUST | Met | Met | One `Makefile`; component builds run as ordered scripts, no recursive make | |
| `build_repeatable` | MUST | Unmet | Unmet | `docs/BUILD.md` "Reproducibility status" | *documented*: Go binaries repeat bit for bit on one host; three own-base recipes are reproducible; the ISO is not. Fix: the own base (`docs/OWN-BASE.md`) |
| `installation_common` | MUST | Met | Met | `install.sh`, the ISO installer, pacman packages | |
| `installation_standard_variables` | MUST | Met | Met | Packages install into makepkg's `$pkgdir` (the `DESTDIR` convention) | |
| `installation_development_quick` | MUST | Met | Met | `AGENTS.md` "Before you push": `sh scripts/ci-tier1.sh`, `make test` | |
| `external_dependencies` | MUST | Met | Met | `Packages-Root`, PKGBUILDs, `go.mod` files, `models.lock`, `guide/model.lock`, `build/config.env`; summarised in `lfaidata-proposal.md` | |
| `dependency_monitoring` | MUST | Unmet | Met | Dependabot (Actions), govulncheck (Go), the per-release dependency check in `RELEASE.md` | *fixed*: govulncheck in CI and a mandatory advisory check of every pinned upstream before each release |
| `updateable_reused_components` | MUST | Met | Met | Every upstream pinned in one place (`build/config.env`, PKGBUILDs, lock files) | |
| `interfaces_current` | SHOULD | Met | Met | | |
| `automated_integration_testing` | MUST | Met | Met | CI on every PR and push | |
| `regression_tests_added50` | MUST | Unmet | Unmet | Of 56 `fix` commits in the last six months, 9 touched a test, lint or assertion | The policy now requires one (`CONTRIBUTING.md`); re-measure in six months |
| `test_statement_coverage80` | MUST | Unmet | Unmet | installer 84.7 %, guide 76.5 % (`go test -coverpkg=./...`, 2026-09-24); shell has no coverage tool in use | Fix: raise `guide/` (the GitHub client is at 36.5 %), and measure shell with kcov or document the N/A case |
| `test_policy_mandated` | MUST | Met | Met | `AGENTS.md` "Tests", `CONTRIBUTING.md` "Tests" | |
| `tests_documented_added` | MUST | Unmet | Met | `CONTRIBUTING.md`, PR template | *fixed* |
| `warnings_strict` | MUST | Unmet | Unmet | see passing | Fix: as above |
| `implement_secure_design` | MUST | Met | Met | `docs/SECURITY-ASSURANCE.md` §4 | |
| `crypto_weaknesses` | MUST | Met | Met | see passing | |
| `crypto_algorithm_agility` | SHOULD | Met | Met | Release key Ed25519 or RSA; ZFS encryption algorithm is a pool property; bundle trust anchors are algorithm-tagged (`ed25519:`) | |
| `crypto_credential_agility` | MUST | Met | Met | Credentials and keys arrive in the enrollment file and live in separate 0600 files (`river-perms`); the release key is referenced by fingerprint in `KEYS` | |
| `crypto_used_network` | SHOULD | Met | Met | SSH, HTTPS | |
| `crypto_tls12` | SHOULD | Met | Met | `install.sh --tlsv1.2`; Go clients default to TLS 1.2+ and no code lowers it | |
| `crypto_certificate_verification` | MUST | Met | Met | No `InsecureSkipVerify`, no `curl -k` anywhere in the tree | |
| `crypto_verification_private` | MUST | Met | Met | Certificates are verified before the guide sends its token | |
| `signed_releases` | MUST | Owner | Owner | Process: `docs/RELEASE-SIGNING.md`, `RELEASE.md` | The release key does not exist yet |
| `version_tags_signed` | SUGG | Owner | Owner | `git tag -s` in `RELEASE.md` | Same key |
| `input_validation` | MUST | Met | Met | Planner rejects unparsable probes and manifests; bundles verified against an ed25519 anchor and syntax-restricted; sandbox `--rw` paths canonicalised and checked | |
| `hardening` | SHOULD | Met | Met | Kernel hardening floor (`config.require`), sysctl hardening, default-deny firewall, `river-sandbox` | |
| `assurance_case` | MUST | Unmet | Met | `docs/SECURITY-ASSURANCE.md` | *fixed*: requirements, threat model, trust boundaries, design principles, CWE countermeasures, known gaps |
| `static_analysis_common_vulnerabilities` | MUST | Unmet | Met | CI job `go-security` (gosec, govulncheck) | *fixed* |
| `dynamic_analysis_unsafe` | MUST | N/A | N/A | Memory-safe languages only | |

### Silver: what is left

MUST criteria a pull request can close, in rough order of effort:

1. `warnings_strict`: shellcheck at `style` severity, staticcheck for Go.
2. `test_statement_coverage80`: more `guide/` tests.
3. `maintenance_or_update`: document (and test) the in-place upgrade path.
4. `regression_tests_added50`: follow the new policy for six months, then re-measure.
5. `build_repeatable`: the own from-source base; the largest item.

SHOULD items: `internationalization`, `build_preserve_debug`.

Owner items: `achieve_passing`, `access_continuity`, `bus_factor`, `signed_releases`,
`version_tags_signed`.

## Owner actions for the badge

1. Publish the repository (this unblocks `repo_public`, `discussion`, `report_archive`).
2. Create the bestpractices.dev account, register the project with the public URL, and enter
   the answers above.
3. Enable GitHub private vulnerability reporting; confirm `security@runink.org` is read by
   at least two people (`vulnerability_report_private`).
4. Enforce 2FA for every member of the GitHub organisation (the badge form asks; LF AI &
   Data requires it).
5. Add a second maintainer with admin rights (`access_continuity`, `bus_factor`).
6. Generate the release-engineering key and publish its fingerprint (`signed_releases`,
   `version_tags_signed`); keep a revocation certificate offline and document recovery.
7. When the badge is awarded, add it to `README.md` "Project status" within 48 hours
   (`documentation_achievements`).

## Gold level (headline gaps)

- **Two unassociated significant contributors**, and **most changes reviewed by someone
  other than the author**. Today everything comes from one person.
- **Reproducible builds** of the shipped images (see `build_repeatable`).
- **A security review** within the last five years: none yet.
- **90 % statement and 80 % branch coverage**, and the hardening and signing items above.

## Supply-chain controls

| Control | Where | State |
| --- | --- | --- |
| CI | `.github/workflows/ci.yml` | Tier 1: every PR and push to `main`, in rootless podman, on GitHub-hosted runners (never self-hosted; fork PRs after a maintainer's approval); no secrets. |
| Tier 2 | `.github/workflows/tier2-vm.yml` | ISO build + QEMU/KVM boot smoke; `workflow_dispatch` only, never on a pull request. Does not fit a standard GitHub-hosted runner (disk, time, RAM), so maintainers run it locally. |
| OpenSSF Scorecard | `.github/workflows/scorecard.yml` | Weekly, on push to `main` and on branch-protection changes once public; uploads SARIF and publishes results to the Scorecard API (GitHub-hosted runner, as the API requires). |
| DCO check | `.github/workflows/dco.yml` | Runs on every PR. To be made a required check. |
| Go security analysis | `.github/workflows/ci.yml` (job `go-security`) | gosec and govulncheck, pinned by version, on every PR and push. |
| Org security gate | `.github/workflows/gatekeeper.yml` | gitleaks, actionlint, zizmor, SHA-pin and self-hosted-runner checks, credential checks, dependency review; one required status check. Its source is in the organisation's private `.github` repository (owner action: make it readable). |
| Action pinning updates | `.github/dependabot.yml` | Weekly, grouped. `ci:` prefix. |
| SBOM (SPDX + CycloneDX) | `.github/workflows/release-attest.yml` | Built with syft v1.51.1, pinned by digest. Covers the source tree and each ISO's unpacked rootfs (pacman DB). Kept as a run artifact, optionally uploaded to the release. |
| SLSA provenance | same | `actions/attest-build-provenance` over `SHA256SUMS`. |
| cosign keyless | same | **Optional and additive** (`cosign: true`). |

### Signed releases (`signed_releases`)

**Decision (owner, 2026-09-24):** Runink River releases, packages and tags are signed with a
**dedicated "Runink River Release Engineering" OpenPGP key** (Ed25519 or RSA 4096). The public
key is published at <https://runink.org/.well-known/gpg-key.txt>; its fingerprint is
**pending** and will be recorded in `docs/RELEASE-SIGNING.md` and `SECURITY.md`.

- The release workflow produces **unsigned** artifacts: `SHA256SUMS`, SBOMs and
  provenance. **It never holds a signing key.** No GPG or cosign private key is ever
  stored as an Actions secret. This satisfies the criterion's rule that "the private key
  must not be on site(s) used to directly distribute the software".
- A release engineer signs **locally** (`river-sign`, added in the own-base PR), which
  produces a detached `SHA256SUMS.asc` and signs the `runink-*` packages, then uploads the
  signatures to the release. Tags are signed with `git tag -s`.
- The verification procedure is in [RELEASE-SIGNING.md](../RELEASE-SIGNING.md) and
  `SECURITY.md` ("Verifying releases"): check the fingerprint, then
  `gpg --verify SHA256SUMS.asc SHA256SUMS`, then `sha256sum -c SHA256SUMS`.
- cosign keyless bundles and GitHub attestations may be added **alongside**. They are never
  the primary signature.

**Owner actions:** generate the key, publish the fingerprint in `SECURITY.md`,
`docs/RELEASE-SIGNING.md` and on keys.openpgp.org. Keep a revocation certificate offline.
Document key rotation and recovery (this closes the bus-factor gap for signing).

### Honest SLSA level

The ISO is built by a maintainer with `make iso` (on an Artix host) or
`make iso-in-builder` (in the privileged builder container, any podman host); see
`docs/BUILD.md`. The assets are attached to a GitHub Release and attested *afterwards* by
`release-attest.yml`. The provenance therefore proves which digests the workflow saw, not
how they were built. **That is SLSA Build L1 with signed provenance.** Reaching **L3**
means running the image build on an isolated, ephemeral builder that generates the
provenance itself (for example an ephemeral VM, with `slsa-github-generator`-style
non-forgeable provenance). The CI plan (`docs/governance/CI.md`) and the own-base build
plan should design for this from the start.
