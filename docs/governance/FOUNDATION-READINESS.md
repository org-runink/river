<!--
SPDX-FileCopyrightText: 2026 The RIVER Authors
SPDX-License-Identifier: MIT
-->

# Foundation readiness: Runink River

One page for anyone evaluating Runink River as a foundation project: each criterion that
Linux Foundation project intake, the OpenSSF and the Apache Way commonly ask about, where
the evidence is in this repository, and what is still missing. It summarises; the detail is
in the linked documents.

**Status, stated once:** Runink River is a public, single-vendor project with one
maintainer. Incubation at a vendor-neutral foundation (LF AI & Data, as a Sandbox project)
is **planned; nothing has been submitted**, and no foundation has reviewed, accepted or
endorsed the project. No OpenSSF Best Practices badge has been awarded. Last checked:
2026-10-04.

Legend:

- **met**: in place; the evidence column points at it.
- **partly met**: in place on paper or in part; the gap is named.
- **open**: not met yet; the gap and the plan are named.

## Linux Foundation project intake

| Criterion | State | Evidence and gap |
| --- | --- | --- |
| OSI-approved licence, clear per file | met | MIT by default ([LICENSE](../../LICENSE)); GPL-2.0-only kernel packaging and CDDL-1.0 OpenZFS packaging, where upstream requires it. Every file is mapped in [REUSE.toml](../../REUSE.toml) and CI checks REUSE 3.3 (`reuse.yml`). Reasoning: [docs/LICENSING.md](../LICENSING.md), [ZFS-LICENSING.md](ZFS-LICENSING.md). |
| IP policy consistent with the licence | met | Inbound = outbound licence per path, no copyright assignment, DCO sign-off ([CHARTER.md](../../CHARTER.md) §7, [CONTRIBUTING.md](../../CONTRIBUTING.md)). Third-party provenance is in [NOTICE](../../NOTICE) and [LICENSES/](../../LICENSES/). |
| DCO, not a CLA | met | [CONTRIBUTING.md](../../CONTRIBUTING.md#developer-certificate-of-origin-dco); `.github/workflows/dco.yml` is a required status check on `main`. |
| Code of Conduct | partly met | Contributor Covenant 2.1 ([CODE_OF_CONDUCT.md](../../CODE_OF_CONDUCT.md)), enforcement in [GOVERNANCE.md](../../GOVERNANCE.md#code-of-conduct-enforcement). Gap: the reporting mailbox is marked as a placeholder, and with one maintainer a report about that maintainer cannot be handled independently. |
| Open, written technical governance | met | [GOVERNANCE.md](../../GOVERNANCE.md): roles, lazy consensus with a public TSC vote as fallback, an interim rule for the single-maintainer phase, how governance itself changes. |
| Technical charter | partly met | [CHARTER.md](../../CHARTER.md) follows the LF Projects template and is a **draft, not in force**; its bracketed items (marks, domain, licence confirmation) are open. |
| Clear decision process | met | [GOVERNANCE.md](../../GOVERNANCE.md#decision-making): what needs one approval, what needs a 3-day window, what needs a 2/3 TSC vote; votes and results stay on the issue or pull request. |
| Path from contributor to maintainer | met | [GOVERNANCE.md](../../GOVERNANCE.md#becoming-a-maintainer), [CONTRIBUTING.md](../../CONTRIBUTING.md#from-contributor-to-maintainer): about three months of sustained contribution of any kind, a nomination, a TSC vote; emeritus and removal rules. |
| Vendor neutrality | partly met | Rules in [GOVERNANCE.md](../../GOVERNANCE.md#neutrality-and-the-steward) and [TRADEMARKS.md](../../TRADEMARKS.md): no proprietary component, no vendor product in the tree, public extension points, conflict-of-interest disclosure, a one-third TSC seat cap per employer. Gap: today every maintainer and contributor is from one organisation, so the cap cannot yet bite. |
| Trademark and neutrality statement | partly met | [TRADEMARKS.md](../../TRADEMARKS.md), [NOTICE](../../NOTICE). Gap: "Runink River" is unregistered, clearance is pending, and which marks transfer to a foundation is open ([CHARTER.md](../../CHARTER.md) §5). |
| More than one maintainer, more than one organisation | open | One maintainer, one organisation ([MAINTAINERS.md](../../MAINTAINERS.md)). This is the largest gap. The plan is [GOVERNANCE.md](../../GOVERNANCE.md#path-to-vendor-neutral-governance), phase 1 exit criteria. |
| Roadmap | met | [ROADMAP.md](../../ROADMAP.md): outcomes, milestones in dependency order with honest status, what is out of scope, and how the roadmap changes. |
| Release process | met | [RELEASE.md](../../RELEASE.md): calendar versions, release criteria, offline signing, one publication path (`release-gate.yml`). No release has been cut yet. |
| Security response | met | [SECURITY.md](../../SECURITY.md): private reporting (GitHub private vulnerability reporting, enabled; encrypted mail), response targets, a written handling process, coordinated disclosure with a 90-day embargo ceiling, reporter credit. Gap: one handler until a second maintainer joins. |
| Communication channels | partly met | GitHub Issues, pull requests and Discussions, listed in [README.md](../../README.md#community-and-communication). No mailing list, chat or community meeting exists yet; the rule for adding one is in [GOVERNANCE.md](../../GOVERNANCE.md#working-in-the-open). |
| Adopters | open | [ADOPTERS.md](../../ADOPTERS.md) is an empty list with instructions; no adopter is claimed. |
| Repository hygiene (README, CONTRIBUTING, CODEOWNERS, SUPPORT, SECURITY, RELEASE, GOVERNANCE, LICENSE) | met | All at the repository root; [.github/CODEOWNERS](../../.github/CODEOWNERS) mirrors MAINTAINERS.md. The LF AI & Data checklist, item by item: [LF-AIDATA.md](LF-AIDATA.md). |
| Project in its own GitHub organisation; 2FA; LF as co-owner | open | The repository lives in its steward's organisation. Moving it, enforcing 2FA and adding the LF happen at application or acceptance ([LF-AIDATA.md](LF-AIDATA.md), owner actions 3 and 10). |

## OpenSSF Best Practices (passing)

The full criterion-by-criterion self-assessment, with the evidence for every answer, is
[OPENSSF-BEST-PRACTICES.md](OPENSSF-BEST-PRACTICES.md). Summary as of 2026-10-04: every
*passing* MUST criterion is met in the repository; the project has **not registered** on
bestpractices.dev, so no badge has been awarded. For *silver*, five MUST and three SHOULD
criteria are unmet, plus items that need a second maintainer.

## OpenSSF Scorecard

`.github/workflows/scorecard.yml` publishes a Scorecard result on every push to `main` and
weekly; the README badge shows the current score. What each check looks at, and the
evidence:

| Check | Evidence in this repository | State |
| --- | --- | --- |
| Branch-Protection | `main` requires the six CI checks (Tier 1, both Go test jobs, Go security analysis, REUSE, DCO), applies to admins, forbids force pushes and deletion | partly met: no required approving review and no required signed commits yet; both need a second maintainer to be practical |
| Code-Review | every change goes through a pull request | open: with one maintainer, changes are not reviewed by a second person |
| Contributors | | open: one organisation |
| Maintained | active development | young repository; Scorecard counts activity over 90 days |
| Security-Policy | [SECURITY.md](../../SECURITY.md) | met |
| License | [LICENSE](../../LICENSE), [LICENSES/](../../LICENSES/) | met |
| CI-Tests | `.github/workflows/ci.yml` on every pull request | met |
| Dependency-Update-Tool | `.github/dependabot.yml` (GitHub Actions) | met; Go modules are not covered by Dependabot, `govulncheck` covers them in CI |
| Pinned-Dependencies | every action pinned by full commit SHA; container images by digest; upstream sources by checksum ([AGENTS.md](../../AGENTS.md) invariant 9) | met |
| Token-Permissions | every workflow sets a top-level read-only `permissions:` and grants writes per job | met |
| Dangerous-Workflow | no `pull_request_target`; inputs reach scripts through `env` | met |
| Binary-Artifacts | no binaries in the tree | met |
| Signed-Releases | process in [RELEASE.md](../../RELEASE.md) and [docs/RELEASE-SIGNING.md](../RELEASE-SIGNING.md); key in [KEYS](../../KEYS) | open until the first release |
| SAST | `gosec` and `govulncheck` run in CI (`go-security`) | open as Scorecard scores it: Scorecard recognises a fixed set of SAST tools (CodeQL among them), and none of them runs yet |
| Fuzzing | | open: no fuzz targets yet |
| CII-Best-Practices | [OPENSSF-BEST-PRACTICES.md](OPENSSF-BEST-PRACTICES.md) | open until the project registers |
| Packaging | releases are published by `release-gate.yml` | not applicable until the first release |

## The Apache Way

The project's planned foundation home is LF AI & Data ([GOVERNANCE.md](../../GOVERNANCE.md)).
This section maps the Apache Way because its principles are a widely used benchmark for
open, community-led governance; it is not a statement that the project has applied, or
will apply, to the Apache Software Foundation.

| Principle | State | Evidence and gap |
| --- | --- | --- |
| Community over code | partly met | The governance documents, the contributor path and the roadmap are written to grow a community; the community today is one maintainer. |
| Meritocracy: earned, individual responsibility | met | Maintainership is earned by contribution, held as an individual, and independent of employer ([GOVERNANCE.md](../../GOVERNANCE.md#principles)). |
| Consensus decision making | met | Lazy consensus by default, votes (+1/0/-1) only when consensus fails ([GOVERNANCE.md](../../GOVERNANCE.md#decision-making)). |
| Open communication: "if it didn't happen on the list, it didn't happen" | met | The equivalent rule, with the repository's issues, pull requests and Discussions as the record: [GOVERNANCE.md](../../GOVERNANCE.md#working-in-the-open). There is no mailing list yet. |
| Independence from any single company | open | Written rules exist (neutrality, conflict-of-interest disclosure, seat cap); in practice the project has one organisation. |
| Responsible oversight (a PMC, or a PPMC with mentors in the Incubator) | partly met | The TSC plays the oversight role ([GOVERNANCE.md](../../GOVERNANCE.md#roles)); it has one member. The project has no foundation mentors or champion. |
| Licensing and third-party dependencies | partly met | Every licence in the tree is open and recorded per file. ASF releases are under the Apache License 2.0, while this project's default is MIT and its kernel and OpenZFS packaging trees are GPL-2.0-only and CDDL-1.0 by upstream necessity ([docs/LICENSING.md](../LICENSING.md)). An ASF evaluation would have to look at both. |

## What closes the gaps

In order of impact, none of which a document can do on its own:

1. A second maintainer, then maintainers from other organisations
   ([GOVERNANCE.md](../../GOVERNANCE.md#becoming-a-maintainer)).
2. The first signed release ([ROADMAP.md](../../ROADMAP.md), milestone 3).
3. Registering on bestpractices.dev and entering the self-assessment
   ([OPENSSF-BEST-PRACTICES.md](OPENSSF-BEST-PRACTICES.md#owner-actions-for-the-badge)).
4. A confirmed Code of Conduct mailbox, and further community channels when there are
   people to use them.
5. Trademark clearance and the transfer plan ([TRADEMARKS.md](../../TRADEMARKS.md)), then a
   sponsor and the application itself ([LF-AIDATA.md](LF-AIDATA.md)).
