<!--
SPDX-FileCopyrightText: 2026 The RIVER Authors
SPDX-License-Identifier: MIT
-->

# LF AI & Data lifecycle checklist: Runink River

Where Runink River stands against the **LF AI & Data Foundation** project lifecycle, stage by
stage. The requirements are quoted from the
[LF AI & Data Project Lifecycle Document](https://github.com/lfai/foundation/blob/main/LF%20AI%20&%20Data%20Project%20Lifecycle%20Document.md)
(approved and in effect as of 2023-06-01; check the current version before applying). The
proposal itself is [lfaidata-proposal.md](lfaidata-proposal.md); the OpenSSF badge
self-assessment is [OPENSSF-BEST-PRACTICES.md](OPENSSF-BEST-PRACTICES.md); every criterion on one page is [FOUNDATION-READINESS.md](FOUNDATION-READINESS.md). The README badges, what
each proves and the owner action that unlocks the pending ones, are in [BADGES.md](BADGES.md).

**Status:** LF AI & Data Sandbox application (planned). Nothing has been submitted.

Legend:

- **done**: met in this repository; the evidence column points at it.
- **missing**: not met, and a maintainer can fix it with an ordinary pull request.
- **owner-side**: needs the copyright and trademark holder, a GitHub organisation admin,
  or the foundation. Listed again, numbered, in [Owner-side actions](#owner-side-actions).

## Positioning

Runink River applies as **"Runink River — a minimal, auditable developer workstation for
data, analytics and AI work on hardware you own"**: a Linux distribution (KDE Plasma on s6,
one pinned kernel, an encrypted ZFS root, a default-deny firewall, a sandbox for untrusted
code) whose every upstream is pinned and whose rules are written down and checked in CI,
plus a planned validated pipeline runtime (`riverd`). The proposal separates what exists
today (with file paths) from the roadmap, and explains the scope fit, including the
precedent of an operating system hosted by an LF umbrella (LF Edge's EVE-OS). An earlier
draft of this checklist targeted CNCF Sandbox first; the owner chose LF AI & Data on
2026-09-24.

## Stage 1: Sandbox

### Requirements

| # | Requirement | State | Evidence / what is needed |
| --- | --- | --- | --- |
| S1 | Fit the scope and mission of LF AI & Data | done (argued; the TAC decides) | [lfaidata-proposal.md](lfaidata-proposal.md), "Alignment with the LF AI & Data mission" |
| S2 | A sponsor who is an existing LF AI & Data member (or a new member joining to sponsor) | owner-side | OWNER-TODO: no sponsor identified. Owner action 1. |
| S3 | Open and documented technical governance | done | [GOVERNANCE.md](../../GOVERNANCE.md), [MAINTAINERS.md](../../MAINTAINERS.md), [CHARTER.md](../../CHARTER.md) (draft) |
| S4 | An OSI-approved licence | done | MIT by default ([LICENSE](../../LICENSE)); GPL-2.0-only kernel tree; CDDL-1.0 OpenZFS tree; per path in [REUSE.toml](../../REUSE.toml), REUSE-compliant. Artwork is CC-BY-4.0 (not code); brand assets are a trademark, not a licence ([docs/LICENSING.md](../LICENSING.md)). The ZFS/CDDL legal note is owner action 6. |
| S5 | Open governance in a Technical Charter, and the Project Contribution Agreement transferring the project's assets to the Linux Foundation | owner-side | [CHARTER.md](../../CHARTER.md) is a draft on the LF Projects template; its open brackets (which marks transfer, the domain) are owner decisions. Executing the agreement is owner action 8. |
| S6 | TAC presentation and a majority TAC vote | owner-side | Owner action 9, after S2 and the tasks below. |
| S7 | Trademark transfer once accepted | owner-side | "Runink River" is unregistered and clearance is pending (owner actions 4 and 5). |

### Tasks for the applying team

| # | Task | State | Evidence / what is needed |
| --- | --- | --- | --- |
| T1 | Submit the project proposal as a pull request to `lfai/proposing-projects` (`proposals/`) | owner-side | The text is ready: [lfaidata-proposal.md](lfaidata-proposal.md). Owner action 9. |
| T2 | The code in its own GitHub organisation, not the founder's | owner-side | The repository is in the company's organisation, and public since before 2026-09-28 (made public with its full history; the remedy is [docs/PUBLICATION.md](../PUBLICATION.md)). Owner actions 2 and 3. |
| T3 | Two-factor authentication for every member of the project's GitHub organisation | owner-side | Owner action 3. |
| T4 | The GitHub DCO app on every repository | owner-side | DCO is adopted and checked by a workflow ([CONTRIBUTING.md](../../CONTRIBUTING.md), `.github/workflows/dco.yml`); installing the app is an organisation setting. Owner action 3. |
| T5 | `@thelinuxfoundation` as a co-owner of the GitHub organisation | owner-side | At acceptance. Owner action 10. |
| T6 | OpenSSF Best Practices badge, **passing** | missing (repository side done; registration is owner-side) | Self-assessment: [OPENSSF-BEST-PRACTICES.md](OPENSSF-BEST-PRACTICES.md). Registration needs the public URL: owner action 7. |
| T7 | Identify who handles security issues | done (one person) | [SECURITY.md](../../SECURITY.md) (response process), [MAINTAINERS.md](../../MAINTAINERS.md). A second handler is owner action 11. |
| T8 | A security mailing list, set up by LF AI & Data | owner-side | At acceptance; until then `security@runink.org` (owner action 12). |
| T9 | `LICENSE` at the root, with third-party licence information | done | [LICENSE](../../LICENSE) (named `LICENSE`, not `LICENSE.md`), [NOTICE](../../NOTICE), [LICENSES/](../../LICENSES), [docs/LICENSING.md](../LICENSING.md) |
| T9 | `README.md` | done | [README.md](../../README.md) |
| T9 | `CONTRIBUTING.md` | done | [CONTRIBUTING.md](../../CONTRIBUTING.md), including the test policy |
| T9 | `CODEOWNERS` | done | [.github/CODEOWNERS](../../.github/CODEOWNERS), mirrors MAINTAINERS.md (emeritus list there) |
| T9 | `CODE_OF_CONDUCT.md` (LF Projects Code of Conduct by default, unless an alternate is approved) | owner-side | [CODE_OF_CONDUCT.md](../../CODE_OF_CONDUCT.md) is Contributor Covenant 2.1 with a placeholder contact. Keep it (needs approval) or adopt the LF Projects code: owner action 13. |
| T9 | `RELEASE.md` (methodology, cadence, criteria) | done (cadence open) | [RELEASE.md](../../RELEASE.md), [CHANGELOG.md](../../CHANGELOG.md). A fixed cadence is owner action 14. |
| T9 | `GOVERNANCE.md` | done | [GOVERNANCE.md](../../GOVERNANCE.md) |
| T9 | `SUPPORT.md` | done | [SUPPORT.md](../../SUPPORT.md) |
| T9 | `SECURITY.md` | done | [SECURITY.md](../../SECURITY.md) |
| T9 | SPDX identifiers in file headers (recommended) | done | Every file has an SPDX header or a [REUSE.toml](../../REUSE.toml) entry; the REUSE lint runs in CI. |

## Stage 2: Incubation

All of Sandbox, plus:

| # | Requirement | State | Evidence / what is needed |
| --- | --- | --- | --- |
| I1 | At least three organisations actively contributing | missing | One organisation (Runink) and one person today. [GOVERNANCE.md](../../GOVERNANCE.md), "Path to vendor-neutral governance". |
| I2 | A Technical Steering Committee with a chair, communicating openly | missing | Until there are five maintainers, all maintainers form the TSC ([GOVERNANCE.md](../../GOVERNANCE.md)); no chair is defined. Needs I1 and a governance change. |
| I3 | At least 500 GitHub stars | missing | 0 stars on 2026-10-04. |
| I4 | OpenSSF Best Practices badge, **silver** | missing | See [OPENSSF-BEST-PRACTICES.md](OPENSSF-BEST-PRACTICES.md), silver section: the unmet items are listed with their fixes. |
| I5 | A majority TAC vote | owner-side | After I1 to I4. |

## Stage 3: Graduation

All of Incubation, plus:

| # | Requirement | State | Evidence / what is needed |
| --- | --- | --- | --- |
| G1 | A healthy number of code contributions from at least five organisations | missing | |
| G2 | At least 1000 GitHub stars | missing | |
| G3 | OpenSSF Best Practices badge, **gold** | missing | Gold adds two unassociated significant contributors, review of most changes by someone other than the author, reproducible builds and a recent security review; none is met ([OPENSSF-BEST-PRACTICES.md](OPENSSF-BEST-PRACTICES.md)). |
| G4 | A substantial ongoing flow of commits and merged contributions for the past 12 months | missing | Active development since mid-2026; public history starts at publication. |
| G5 | At least one collaboration with another LF AI & Data hosted project | missing | Candidates are named in the proposal ("Collaboration opportunities"); none has started. |
| G6 | Affirmative votes of the TAC **and** the Governing Board | owner-side | |

## Owner-side actions

Numbered so other documents can refer to them. None can be done by a pull request.

| # | Action | State |
| --- | --- | --- |
| 1 | **Find a sponsor**: an existing LF AI & Data member organisation (or a new member) willing to sponsor the Sandbox application. | Open (OWNER-TODO) |
| 2 | **Publish the repository** from a reviewed clean snapshot (never flip a private one: GitHub keeps every `refs/pull/*`). The step-by-step, and the remedy for `org-runink/river` having been made public with its full history, is [docs/PUBLICATION.md](../PUBLICATION.md). Then enable private vulnerability reporting, branch protection on `main` requiring the DCO, `tier1`, `REUSE lint`, `installer-go`, `river-guide` and `go-security` checks, signed tags, and "require approval for all outside collaborators". | Partly done (verified 2026-10-04): the repository is public (with its full history, see PUBLICATION.md); private vulnerability reporting is on; `main` is protected with the six required checks above, admins included. Open: signed tags with the first release, a required approving review (needs a second maintainer), confirming the outside-collaborator approval setting |
| 3 | **Project GitHub organisation**: move the repository to an organisation of its own (lifecycle task T2), enforce 2FA for every member, install the GitHub DCO app. Make the org-wide `gatekeeper` workflow's source public or vendor it into this repository, so reviewers can read what it checks. | Open |
| 4 | **Trademark clearance**: USPTO and EUIPO searches for "Runink River", Class 9 and Class 42. The project never uses a bare "River" as a mark because of the unrelated *river* Wayland compositor. | Open |
| 5 | **Trademark transfer plan**: which marks go to LF Projects, LLC on acceptance (normally "Runink River" and its logo) and which stay with Runink under a licence back; what happens to the installed identifiers (`runink-*` packages, `/etc/runink`, `ID=runink`). Fill in the brackets in [CHARTER.md](../../CHARTER.md) §5. | Open |
| 6 | **ZFS/CDDL legal note**: a written opinion from counsel on distributing the prebuilt CDDL `runink-zfs` module package beside the GPL-2.0 kernel ([ZFS-LICENSING.md](ZFS-LICENSING.md)). Also confirm with LF AI & Data that the non-default licences (MIT; GPL-2.0-only and CDDL-1.0 by necessity; Apache-2.0 in `validation/`) are acceptable, and decide whether `validation/` stays Apache-2.0 or moves to the MIT default. | Open |
| 7 | **OpenSSF Best Practices**: create the bestpractices.dev account, register the project with the public repository URL, enter the self-assessment, reach *passing*. | Open (needs 2) |
| 8 | **Project Contribution Agreement** and technical charter: finalise [CHARTER.md](../../CHARTER.md) with LF staff and execute the agreement. | At acceptance |
| 9 | **Submit and present**: open the proposal PR on `lfai/proposing-projects`, present to the TAC, answer its questions. | After 1–7 |
| 10 | Add `@thelinuxfoundation` as a co-owner of the project's GitHub organisation. | At acceptance |
| 11 | **Second maintainer** (bus factor is 1), then maintainers from other employers. Also: a second person able to handle security reports and to sign releases (or a documented key-recovery plan). | Open |
| 12 | **Mailboxes** `security@runink.org` and `conduct@runink.org` exist and are read by at least two people, until the foundation's lists replace them. | Open |
| 13 | **Code of Conduct**: keep Contributor Covenant 2.1 (needs approval as an alternate) or adopt the LF Projects Code of Conduct. | Open |
| 14 | **Release cadence** for [RELEASE.md](../../RELEASE.md). | Open |
| 15 | **Release key**: publish it at `https://runink.org/.well-known/gpg-key.txt` and on keyservers, and set the fingerprint in `KEYS`, `install.sh`, [RELEASE-SIGNING.md](../RELEASE-SIGNING.md), [SECURITY.md](../../SECURITY.md) and `base/keys/owner/fingerprint` in one commit. | Done 2026-09-27: the lead maintainer's key `95C0A7B97D547413E42660DDB06FE75626F15BF3`, published at that URL and pinned in all five places. A dedicated "Release Engineering" uid and a second signer (action 11) remain open. |
| 16 | **Secure Boot**: the organisation Root CA and signing certificate (HSM decision) for MOK enrollment; later a Runink River shim through rhboot/shim-review ([SECURE-BOOT.md](../SECURE-BOOT.md)). | Open (roadmap) |
| 17 | **CI runners**: this public repository runs only on GitHub-hosted runners and never on a self-hosted one; fork pull requests need a maintainer's approval ("Require approval for all external contributors", a repository setting to confirm) ([CI.md](CI.md)). Tier 2 (ISO build + VM boot) does not fit a standard hosted runner and runs locally until a larger runner is chosen. | Open (setting to confirm) |
| 18 | **Project website and social accounts** for the proposal's website field (none exist for the project itself). | Open (OWNER-TODO) |

## Maintainer follow-ups (ordinary pull requests)

| Item | Notes |
| --- | --- |
| Close the remaining OpenSSF *passing* and *silver* gaps a PR can close | Listed with their fixes in [OPENSSF-BEST-PRACTICES.md](OPENSSF-BEST-PRACTICES.md) |
| Tier 2: register a KVM runner, make the install test unattended, add ZFS/encryption tests | `tests/vm-boot-test.sh` is still interactive |
| Wire the closure lint into the ISO build job | Needs the post-`localrepo` closure |
| Two or three written use cases | Only once real adopters exist. [ADOPTERS.md](../../ADOPTERS.md) exists as an empty list with instructions; none are claimed |
| `riverd` dependency plan: every dependency under an OSI licence | [runtime/README.md](../../runtime/README.md) |
| Own from-source base replaces the Artix-derived tooling (removes the GPL-3.0 artools patching and the BSD-2-Clause profile files) | [OWN-BASE.md](../OWN-BASE.md) |
