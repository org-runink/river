<!--
SPDX-FileCopyrightText: 2026 The RIVER Authors
SPDX-License-Identifier: MIT
-->

# Maintainers

The people below are the Runink River maintainers as defined in [GOVERNANCE.md](GOVERNANCE.md).
They review and merge changes, cut and sign releases, and handle security reports. While
there are fewer than five maintainers, they are also the Technical Steering Committee.

| Name        | GitHub                                 | Contact         | Affiliation | Areas      | OpenPGP (commits, tags, AUR)                        |
| ----------- | -------------------------------------- | --------------- | ----------- | ---------- | --------------------------------------------------- |
| Daniel Paes | [@paesdan](https://github.com/paesdan) | paes@runink.org | Runink      | All (lead) | `95C0 A7B9 7D54 7413 E426  60DD B06F E756 26F1 5BF3` |

**Organisations represented: 1** (Runink). **Maintainers: 1.**

> **A second maintainer is needed.** With one maintainer the project's bus factor is 1:
> nobody else can merge, release or answer a security report if the lead is unavailable.
> Linux Foundation projects expect more than one maintainer, and ideally maintainers from
> more than one employer. Until a second maintainer joins, every review in
> [GOVERNANCE.md](GOVERNANCE.md) that says "not the author" cannot be met (the interim rule
> is in [GOVERNANCE.md](GOVERNANCE.md#decision-making)), and releases depend on one person.
> If you want to help, start with the issues and see
> [Becoming a maintainer](GOVERNANCE.md#becoming-a-maintainer). The path to more than one
> employer is in [GOVERNANCE.md](GOVERNANCE.md#path-to-vendor-neutral-governance).

## What a maintainer does

- Reviews pull requests, including from first-time contributors, and gives a reason for
  every request for changes.
- Merges only changes that pass the required CI checks and keep the
  [invariants](AGENTS.md); asks for a TSC vote where [GOVERNANCE.md](GOVERNANCE.md) requires
  one.
- Triages issues and answers questions in
  [Discussions](https://github.com/org-runink/river/discussions).
- Takes part in the security response ([SECURITY.md](SECURITY.md)) and keeps embargoed
  information private.
- Cuts and signs releases ([RELEASE.md](RELEASE.md)) once they hold release-signing rights.
- Discloses a conflict of interest before voting
  ([GOVERNANCE.md](GOVERNANCE.md#neutrality-and-the-steward)).

## Release signing

The OpenPGP key above is the maintainer's key. It signs commits, the `v<pkgver>-<pkgrel>`
tags the AUR packages fetch ([packaging/aur/PUBLISHING.md](packaging/aur/PUBLISHING.md)) and
the AUR uploads, and since 2026-09-27 it is also the Runink River release-signing key: it signs
each release's `SHA256SUMS` and the packages ([KEYS](KEYS), [SECURITY.md](SECURITY.md)). A
second person able to sign releases, or a written key-recovery plan, is still needed.

## Emeritus maintainers

None yet.

## Becoming a maintainer

See [GOVERNANCE.md](GOVERNANCE.md#becoming-a-maintainer). When a maintainer joins or becomes
emeritus, this file and [.github/CODEOWNERS](.github/CODEOWNERS) change in the same pull
request.
