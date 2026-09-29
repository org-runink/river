---
title: Governance
weight: 1
---
<!--
SPDX-FileCopyrightText: 2026 The RIVER Authors
SPDX-License-Identifier: MIT
-->

Runink River starts as a **single-vendor project** stewarded by Runink, with a written path to
vendor-neutral governance under a foundation. The authoritative text is
{{< repo "GOVERNANCE.md" >}}; the draft foundation-era charter is {{< repo "CHARTER.md" >}}.

## Roles

- **Users** run Runink River. **Contributors** send issues, reviews, docs or code.
- **Maintainers** ({{< repo "MAINTAINERS.md" >}}) review and merge, triage issues and security
  reports, cut and sign releases, and enforce the invariants and the Code of Conduct.
- The **Technical Steering Committee (TSC)**: while there are fewer than five maintainers, all
  of them. From five, an elected TSC of three to seven, with no employer holding more than a
  third of the seats.

## How decisions are made

Lazy consensus: a proposal is accepted if no maintainer objects within the review window.

| Change | Needs |
|---|---|
| Bug fix, docs, a routine pin bump | 1 maintainer approval (not the author) |
| New feature, new package, new workflow | 1 approval and no objection within 3 working days |
| An invariant (s6, one kernel, ZFS root, no runtime fetch, no secrets in the image, the sandbox deny-list, ...) | TSC vote, 2/3 majority, 7 days |
| Licensing, trademark, charter, governance, maintainers | TSC vote, 2/3 majority, 7 days |

Votes happen on the issue or pull request, so there is a public record.

## Commercial products

Runink River ships **no proprietary components**. Any vendor, Runink included, may build
products on it, outside this repository. Nothing here requires a proprietary component to
build, boot or test the image, and no vendor holds a veto outside the TSC.

## Path to a foundation

| Phase | State |
|---|---|
| 0: private preparation | done |
| 1: public, single-vendor | the current phase: the repository is public |
| 2: foundation application | planned: the LF AI & Data Sandbox proposal is a draft, not yet submitted ({{< repo "docs/governance/lfaidata-proposal.md" >}}) |
| 3: vendor-neutral | trademark and domain assigned to the foundation, charter adopted, maintainers from two or more organisations |
