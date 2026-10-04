<!--
SPDX-FileCopyrightText: 2026 The RIVER Authors
SPDX-License-Identifier: MIT
-->

# Runink River governance

This document says who decides what in the Runink River project, how they decide, and how
anyone can come to share in those decisions. Runink River starts as a **single-vendor
project** stewarded by Runink, with one maintainer. The last section sets out the concrete
path to vendor-neutral governance under a foundation; [CHARTER.md](CHARTER.md) is the draft
of the charter for that era. Until a charter is adopted, this document governs the project.

## Principles

- **Open participation.** Anyone may use, study, change and redistribute Runink River, and
  anyone may contribute, whoever employs them, subject only to the
  [Code of Conduct](CODE_OF_CONDUCT.md) and the [DCO](CONTRIBUTING.md#developer-certificate-of-origin-dco).
- **Merit, earned in public.** Responsibility (review, merge, release) goes to people who
  have shown sustained, good-quality work in this repository. Employer, title and
  commercial interest earn nothing here.
- **Consensus first.** Most decisions are made by lazy consensus. Votes are a fallback, not
  the normal way of working.
- **Working in the open.** See the next section.
- **Neutrality.** The project serves every user and every downstream on the same terms,
  including competitors of its current steward. See
  [Neutrality and the steward](#neutrality-and-the-steward).

## Working in the open

**If a decision is not recorded in this repository, it has not been made.** Proposals,
discussions, reviews, votes and their results happen on GitHub issues, pull requests and
[Discussions](https://github.com/org-runink/river/discussions) in `org-runink/river`, where
anyone can read and comment. A conversation that happens elsewhere (a call, a chat, a
meeting at a conference) is an input, not a decision: its outcome is written up on the
relevant issue or pull request, and the usual review window then applies.

Only two kinds of work happen in private, and both end in a public record:

- **security reports**, handled under embargo as [SECURITY.md](SECURITY.md) describes and
  published as an advisory when the fix ships;
- **Code of Conduct reports**, where the privacy of the people involved comes first; an
  outcome is published only as far as it does not identify a reporter.

The communication channels in use are listed in
[README.md](README.md#community-and-communication). A new channel (a mailing list, a chat
room, a community meeting) is added by a pull request to that list, and it does not change
this rule.

## Roles

| Role | Who | Can | How you get there |
| --- | --- | --- | --- |
| **User** | anyone who runs Runink River | use it, ask questions, report bugs | install it |
| **Contributor** | anyone who opens an issue, reviews a change, writes docs or code, tests on hardware, or answers a question | everything a user can, plus propose changes; reviews from contributors count and are welcome | take part, under the Code of Conduct and the DCO |
| **Maintainer** (a *committer*, in Apache terms) | the people in [MAINTAINERS.md](MAINTAINERS.md) | review and merge pull requests; triage issues and security reports ([SECURITY.md](SECURITY.md)); cut and sign releases; enforce the invariants in [AGENTS.md](AGENTS.md) and the Code of Conduct | [Becoming a maintainer](#becoming-a-maintainer) |
| **Technical Steering Committee (TSC)** | all maintainers while there are fewer than five; then three to seven elected members | decide what lazy consensus could not; approve invariant, licence, trademark, charter and governance changes; add and remove maintainers | elected by the maintainers (below) |
| **Emeritus maintainer** | former maintainers | return by the same process as any contributor | step down, or inactivity (below) |

Once there are five or more maintainers, the maintainers elect a TSC of three to seven
members for one-year terms, and the TSC elects a chair from among its members. **No single
employer (counting related companies as one) may hold more than one third of the TSC seats,
rounded up.** If a change of employment breaks the cap, the members from the
over-represented employer agree among themselves who steps down within 30 days; otherwise
the most recently elected of them does. Until there are five maintainers the cap cannot
apply: that is the single-vendor phase described below.

## Decision making

Runink River uses **lazy consensus**: a proposal (a pull request, or an issue for a design)
is accepted if no maintainer objects within its review window. Silence is assent. An
objection must give a reason and, where it can, a way forward.

| Change | Needs | Window |
| --- | --- | --- |
| Bug fix, docs, a routine package or pin bump | 1 maintainer approval (not the author) | none |
| New feature, new package in `Packages-Root`, a new workflow | 1 approval + no maintainer objection | 3 working days |
| Change to an **invariant** (the numbered list in AGENTS.md: s6, one kernel, encrypted ZFS root, secrets like `/etc/shadow`, pinned signed supply, the manifest allow-list, the default-deny firewall, the sandbox deny-list, pinned upstreams) | TSC vote, 2/3 majority | 7 days |
| Licensing, trademark, charter or governance change; adding or removing a maintainer | TSC vote, 2/3 majority | 7 days |
| Security fix under embargo | 1 maintainer approval, reviewed in private | none |

**Voting.** When consensus fails, any maintainer may call a TSC vote on the issue or pull
request. Members vote **+1** (yes), **0** (abstain) or **-1** (no, with a reason). A vote is
open for 7 days, or until every TSC member has voted. It passes by a simple majority of the
votes cast unless the table says otherwise, and only if at least half of the TSC voted. The
call, every vote and the result stay on the issue or pull request, so there is a public
record.

**Interim rule while there is one maintainer.** The "not the author" approval above cannot
be met while [MAINTAINERS.md](MAINTAINERS.md) lists one person. Until a second maintainer
joins, the maintainer merges their own pull requests once the required CI checks pass and
the review window has run, so that anyone can comment first. This is a known gap, not the
intended steady state; the second maintainer ends it.

## Becoming a maintainer

A contributor becomes a maintainer when:

1. they have a sustained record of good contributions over roughly three months: code,
   reviews, documentation, release or packaging work and helping users all count;
2. an existing maintainer nominates them in a pull request against `MAINTAINERS.md` and
   `.github/CODEOWNERS`, describing that record, and the nominee accepts on the pull
   request;
3. the TSC approves by the vote in the table above.

New maintainers get merge rights first. Release-signing rights follow once they have taken
part in a release, with their own OpenPGP key listed in [MAINTAINERS.md](MAINTAINERS.md).

A maintainer may step down at any time and becomes emeritus. A maintainer who has been
inactive for six months may be moved to emeritus by a TSC vote, after they have been asked.
A maintainer may be removed for a serious or repeated breach of the Code of Conduct or of
this document, by a TSC vote in which the maintainer concerned does not vote. Emeritus
maintainers can return by the same process as any contributor.

## Neutrality and the steward

Runink River ships **no proprietary components**. Runink, like any other vendor, may build
commercial products on it. Those products are *downstream*: optional, out-of-tree profiles
and payloads passed to the image build (`RIVER_PROFILE_DIR`, `RIVER_PAYLOAD_DIR`), never
part of this repository. The rules that keep the line clean:

- Nothing in this repository requires a proprietary or source-available component to
  build, boot or test the **base image**. An image built without a payload is complete.
  See [docs/LICENSING.md](docs/LICENSING.md).
- This repository does not name, document or carry code for any vendor's product. The
  extension points a vendor uses (profiles, payloads, the first-boot contract) are public,
  documented and open to every vendor on the same terms.
- Runink employees contribute under the same process as everyone else. No vendor holds a
  veto outside the TSC, and no vendor's product plan is the project's roadmap: the
  [roadmap](ROADMAP.md) is decided here, in public.
- **Conflicts of interest.** A maintainer whose employer has a direct commercial stake in a
  decision says so on the issue or pull request before voting. They may still vote; the
  disclosure lets everyone weigh their arguments.
- The project's name and marks are held by Runink today, with the stated intent to transfer
  them to a foundation ([TRADEMARKS.md](TRADEMARKS.md)).

## Code of Conduct enforcement

Reports go to the contact in [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md). They are handled by
maintainers who are not involved in the incident, following the Contributor Covenant
enforcement guidelines. While there is one maintainer, a report about that maintainer
cannot be handled independently inside the project; this is part of the bus-factor gap in
[MAINTAINERS.md](MAINTAINERS.md). After the move to a foundation, the foundation's Code of
Conduct committee is the escalation path.

## Changing this document

This document, and [CHARTER.md](CHARTER.md) while it is a draft, change by pull request
under the governance row of the decision table: a TSC vote, 2/3 majority, 7 days.

## Path to vendor-neutral governance

| Phase | State | Exit criteria |
| ----- | ----- | ------------- |
| **0: private preparation** (done) | Runink-only, private repository | License decided (MIT; GPL-2.0-only kernel tree; CDDL-1.0 OpenZFS package), public history started from a single squashed commit, community files in place |
| **1: public, single-vendor** (now) | Public repo, Runink maintainers, this document in force | 3+ maintainers; at least 1 maintainer and 2 regular contributors from outside Runink; a public release cadence; OpenSSF Best Practices *passing*; trademark filings started |
| **2: foundation application** | LF AI & Data Sandbox application: planned, **not submitted** ([docs/governance/LF-AIDATA.md](docs/governance/LF-AIDATA.md), [the draft proposal](docs/governance/lfaidata-proposal.md), [the readiness map](docs/governance/FOUNDATION-READINESS.md)) | A sponsor, a TAC presentation and a majority TAC vote |
| **3: vendor-neutral** | Trademark and domain assigned to the foundation, [CHARTER.md](CHARTER.md) adopted, TSC seat cap enforced | Maintainers from 2+ organizations; foundation-hosted infrastructure (CI, release signing identity, security mailbox) |

When the project enters a foundation, [CHARTER.md](CHARTER.md) replaces this document
wherever the two conflict.
