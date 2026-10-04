---
title: Governance & community
linkTitle: Governance & community
weight: 1
description: "How Runink River is governed, how decisions are made, how to become a maintainer, where the community talks, and the documents that set it all down."
---
<!--
SPDX-FileCopyrightText: 2026 The RIVER Authors
SPDX-License-Identifier: MIT
-->

Runink River is an open-source project with written, open governance. Today it is a
**single-vendor project** stewarded by Runink, with one maintainer, and a written path to
vendor-neutral governance under a foundation. Incubation at a foundation is **planned;
nothing has been submitted**. The documents below are the authoritative text; this page
summarises them.

## The documents

| Document | What it covers |
|---|---|
| {{< repo path="GOVERNANCE.md" text="GOVERNANCE.md" >}} | roles, how decisions are made, how to become a maintainer, neutrality, the path to a foundation |
| {{< repo path="CHARTER.md" text="CHARTER.md" >}} | the draft technical charter for the foundation era (not in force) |
| {{< repo path="MAINTAINERS.md" text="MAINTAINERS.md" >}} | who the maintainers are, their affiliation and signing key |
| {{< repo path="CONTRIBUTING.md" text="CONTRIBUTING.md" >}} | how to send a change, the DCO, the test policy |
| {{< repo path="CODE_OF_CONDUCT.md" text="CODE_OF_CONDUCT.md" >}} | Contributor Covenant 2.1 |
| {{< repo path="SECURITY.md" text="SECURITY.md" >}} | private reporting, response targets, coordinated disclosure |
| {{< repo path="ROADMAP.md" text="ROADMAP.md" >}} | outcomes and milestones ([summary]({{< relref "/docs/roadmap" >}})) |
| {{< repo path="RELEASE.md" text="RELEASE.md" >}} | how a release is cut, checked and signed ([summary]({{< relref "/docs/releases" >}})) |
| {{< repo path="TRADEMARKS.md" text="TRADEMARKS.md" >}} | the project name and marks, and the project's neutrality |
| {{< repo path="ADOPTERS.md" text="ADOPTERS.md" >}} | who uses Runink River (none listed yet) and how to add yourself |
| {{< repo path="SUPPORT.md" text="SUPPORT.md" >}} | where to get help |
| {{< repo path="docs/governance/FOUNDATION-READINESS.md" text="Foundation readiness" >}} | the foundation, OpenSSF and Apache Way criteria, with the evidence and the gaps |
| {{< repo path="docs/governance/OPENSSF-BEST-PRACTICES.md" text="OpenSSF self-assessment" >}} | the OpenSSF Best Practices criteria, one by one (not registered yet; no badge awarded) |

## Working in the open

If a decision is not recorded in the repository, it has not been made. Proposals, reviews,
votes and their results happen on GitHub issues, pull requests and Discussions, where anyone
can read and take part. Only security reports and Code of Conduct reports are handled in
private, and both end in a public record.

## Roles

- **Users** run Runink River. **Contributors** send issues, reviews, documentation, tests on
  real hardware or code.
- **Maintainers** ({{< repo "MAINTAINERS.md" >}}; *committers*, in Apache terms) review and
  merge, triage issues and security reports, cut and sign releases, and enforce the
  invariants and the Code of Conduct.
- The **Technical Steering Committee (TSC)**: while there are fewer than five maintainers,
  all of them. From five, an elected TSC of three to seven with a chair, and no employer
  holding more than a third of the seats.

## How decisions are made

Lazy consensus: a proposal is accepted if no maintainer objects within its review window.
When consensus fails, any maintainer may call a TSC vote (+1, 0, -1) on the issue or pull
request, so there is a public record.

| Change | Needs |
|---|---|
| Bug fix, docs, a routine pin bump | 1 maintainer approval (not the author) |
| New feature, new package, new workflow | 1 approval and no objection within 3 working days |
| An invariant (s6, one kernel, encrypted ZFS root, secrets like `/etc/shadow`, the default-deny firewall, the sandbox deny-list, pinned upstreams, ...) | TSC vote, 2/3 majority, 7 days |
| Licensing, trademark, charter, governance, maintainers | TSC vote, 2/3 majority, 7 days |

While there is one maintainer, the "not the author" approval cannot be met: the maintainer
merges their own pull requests once the required checks pass and the review window has run.
That gap closes with the second maintainer.

## Becoming a maintainer

About three months of sustained contribution of any kind (code, reviews, documentation,
testing, packaging, helping users), a nomination by a maintainer, and a TSC vote. Your
employer does not matter. **The project needs a second maintainer**, so this path is open
now: start with [Contributing]({{< relref "/docs/contributing" >}}).

## Neutrality

Runink River ships **no proprietary components** and names no vendor's product. Any vendor,
Runink included, builds on it outside the repository, through the same public interfaces
(external profiles, payloads, the first-boot contract). No vendor holds a veto outside the
TSC, and maintainers disclose a conflict of interest before they vote. The project's marks
are held by Runink today, with the stated intent to transfer them to a foundation
({{< repo "TRADEMARKS.md" >}}).

## Where to talk

| Channel | Use it for |
|---|---|
| [GitHub Discussions](https://github.com/org-runink/river/discussions) | questions, ideas and proposals, announcements |
| [GitHub Issues](https://github.com/org-runink/river/issues) | bugs and agreed feature work |
| [Pull requests](https://github.com/org-runink/river/pulls) | changes, reviews and every governance decision |
| [Private vulnerability reporting](https://github.com/org-runink/river/security/advisories/new) or `security@runink.org` | security problems only ([how to report]({{< relref "/docs/security/reporting" >}})) |

There is no mailing list, chat channel or community meeting yet. When one is created it is
announced in Discussions and added to {{< repo "README.md" >}}.

## Path to a foundation

| Phase | State |
|---|---|
| 0: private preparation | done |
| 1: public, single-vendor | **now**: the repository is public, governance is written down, one maintainer |
| 2: foundation application | planned: the LF AI & Data Sandbox proposal is a draft and has **not** been submitted ({{< repo "docs/governance/LF-AIDATA.md" >}}) |
| 3: vendor-neutral | marks and domain assigned to the foundation, charter adopted, maintainers from two or more organisations |
