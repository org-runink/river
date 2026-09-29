<!--
SPDX-FileCopyrightText: 2026 The RIVER Authors
SPDX-License-Identifier: MIT
-->

# Runink River governance

This document says who decides what in the Runink River project and how. Runink River starts as a
**single-vendor project** stewarded by Runink. The last section sets out the concrete path
to vendor-neutral governance under a foundation. [CHARTER.md](CHARTER.md) is the draft of
the foundation-era charter.

## Roles

**Users** run Runink River. **Contributors** send issues, reviews, docs or code. Anyone can be
either, subject to the [Code of Conduct](CODE_OF_CONDUCT.md) and the DCO
([CONTRIBUTING.md](CONTRIBUTING.md)).

**Maintainers** (listed in [MAINTAINERS.md](MAINTAINERS.md)):

- review and merge pull requests;
- triage issues and security reports ([SECURITY.md](SECURITY.md));
- cut and sign releases;
- enforce the invariants in [AGENTS.md](AGENTS.md) and the Code of Conduct.

**Technical Steering Committee (TSC).** While there are fewer than five maintainers, all
maintainers together act as the TSC. Once there are five or more, the maintainers elect a
TSC of three to seven members for one-year terms. No single employer may hold more than
one third of the seats, rounded up. Until then this limit does not apply. That is the
single-vendor phase described below.

## Decision making

Runink River uses **lazy consensus**. A proposal (a PR or an issue) is accepted if no maintainer
objects within the review window. Most changes need nothing more than that.

| Change                                                                 | Needs                                                              | Window |
| ---------------------------------------------------------------------- | ------------------------------------------------------------------ | ------ |
| Bug fix, docs, a routine package or pin bump                           | 1 maintainer approval (not the author)                             | none   |
| New feature, new package in `Packages-Root`, a new workflow            | 1 approval + no maintainer objection                               | 3 working days |
| Change to an **invariant** (the numbered list in AGENTS.md: s6, one kernel, encrypted ZFS root, secrets like `/etc/shadow`, pinned signed supply, the manifest allow-list, the default-deny firewall, the sandbox deny-list, pinned upstreams) | TSC vote, 2/3 majority | 7 days |
| Licensing, trademark, charter or governance change; adding or removing a maintainer | TSC vote, 2/3 majority                              | 7 days |
| Security fix under embargo                                             | 1 maintainer approval, reviewed in private                         | none   |

When consensus fails, any maintainer may call a TSC vote. A vote is decided by a simple
majority of the TSC unless the table says otherwise, with at least half of the TSC taking
part. Votes are held on the issue or PR, so there is a public record.

## Becoming a maintainer

A contributor becomes a maintainer when:

1. they have a sustained record of good contributions, such as reviews, fixes or releases,
   over roughly three months;
2. an existing maintainer nominates them in a PR against `MAINTAINERS.md`;
3. the TSC approves by the vote in the table above.

A maintainer who has been inactive for six months may be moved to emeritus by a TSC vote,
after they have been asked. They can return by the same process.

## Code of Conduct enforcement

Reports go to the address in [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md). They are handled by
maintainers who are not involved in the incident, following the Contributor Covenant
enforcement guidelines. After the move to a foundation, the foundation's Code of Conduct
committee is the escalation path.

## Relationship with Runink (commercial products)

Runink River ships **no proprietary components**. Runink, like any other vendor, may build
commercial products on it. Those products are *downstream payloads*: optional,
out-of-tree directories passed to the image build (`RIVER_PAYLOAD_DIR`), never part of this
repository. Rules that keep the line clean:

- Nothing in this repository requires a proprietary or source-available component to
  build, boot or test the **base image**. A base image built without a payload is
  complete. See [docs/LICENSING.md](docs/LICENSING.md).
- Runink employees contribute under the same process as everyone else. No vendor holds
  a veto outside the TSC.
- The roadmap is public: issues and milestones in this repository.

## Path to vendor-neutral governance

| Phase | State | Exit criteria |
| ----- | ----- | ------------- |
| **0: private preparation** (done) | Runink-only, private repository | License decided (MIT; GPL-2.0-only kernel tree; CDDL-1.0 OpenZFS package), public history started from a single squashed commit, community files in place |
| **1: public, single-vendor** (now) | Public repo, Runink maintainers, this document in force | 3+ maintainers; at least 1 maintainer and 2 regular contributors from outside Runink; a public release cadence; OpenSSF Best Practices *passing*; trademark filings started |
| **2: foundation application** | LF AI & Data Sandbox application (planned; see [docs/governance/LF-AIDATA.md](docs/governance/LF-AIDATA.md) and [the proposal](docs/governance/lfaidata-proposal.md)) | A sponsor, a TAC presentation and a majority TAC vote |
| **3: vendor-neutral** | Trademark and domain assigned to the LF, [CHARTER.md](CHARTER.md) adopted, TSC seat cap enforced | Maintainers from 2+ organizations; foundation-hosted infrastructure (CI, release signing identity, security mailbox) |

When the project enters a foundation, [CHARTER.md](CHARTER.md) replaces this document
wherever the two conflict.
