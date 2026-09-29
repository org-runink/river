<!--
SPDX-FileCopyrightText: 2026 The RIVER Authors
SPDX-License-Identifier: MIT
-->

# Maintainers

The people below are the Runink River maintainers as defined in [GOVERNANCE.md](GOVERNANCE.md).
They review and merge changes, cut releases and handle security reports.

| Name        | GitHub                                 | Contact         | Affiliation | Areas      | OpenPGP (commits, tags, AUR)                        |
| ----------- | -------------------------------------- | --------------- | ----------- | ---------- | --------------------------------------------------- |
| Daniel Paes | [@paesdan](https://github.com/paesdan) | paes@runink.org | Runink      | All (lead) | `95C0 A7B9 7D54 7413 E426  60DD B06F E756 26F1 5BF3` |

> **A second maintainer is needed.** With one maintainer the project's bus factor is 1:
> nobody else can merge, release or answer a security report if the lead is unavailable.
> Linux Foundation projects expect more than one maintainer, and ideally maintainers from
> more than one employer. Until a second maintainer joins, every review in
> [GOVERNANCE.md](GOVERNANCE.md) that says "not the author" cannot be met, and releases
> depend on one person. If you want to help, start with the issues and see
> [Becoming a maintainer](GOVERNANCE.md#becoming-a-maintainer). The path to more than one
> employer is in [GOVERNANCE.md](GOVERNANCE.md#path-to-vendor-neutral-governance).

The OpenPGP key above is the maintainer's key. It signs commits, the `v<pkgver>-<pkgrel>`
tags the AUR packages fetch ([packaging/aur/PUBLISHING.md](packaging/aur/PUBLISHING.md)) and
the AUR uploads, and since 2026-09-27 it is also the Runink River release-signing key: it signs
each release's `SHA256SUMS` and the packages ([KEYS](KEYS), [SECURITY.md](SECURITY.md)). A
second person able to sign releases, or a written key-recovery plan, is still needed.

## Emeritus maintainers

None yet.

## Becoming a maintainer

See [GOVERNANCE.md](GOVERNANCE.md#becoming-a-maintainer).
