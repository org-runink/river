<!--
SPDX-FileCopyrightText: 2026 The RIVER Authors
SPDX-License-Identifier: MIT
-->

# Trademarks and neutrality

Runink River's code and documentation are open source ([LICENSE](LICENSE),
[docs/LICENSING.md](docs/LICENSING.md)). Its name and marks are not: they tell people that
what they are running is the project's own release. This page says who holds the marks
today, what you may do with them, how that is meant to change, and what keeps the project
neutral in the meantime. The binding text is in [NOTICE](NOTICE) and
[LICENSES/LicenseRef-Runink-Trademark.txt](LICENSES/LicenseRef-Runink-Trademark.txt).

## Who holds the marks

"Runink", the Runink logo, the name "Runink River" and the Runink River community mark (the
mascot and the logos, icons, splash and wallpaper rendered from it, marked
`LicenseRef-Runink-Trademark` in [REUSE.toml](REUSE.toml)) are held by Runink. No licence in
this repository grants any right to use them. "Runink River" is not a registered trademark;
clearance searches for it are pending.

## What you may do

- Say truthfully that your software is based on, compatible with, or derived from Runink
  River, and refer to the project by name.
- Redistribute unmodified official Runink River releases, including their artwork.
- Build and ship your own image from this repository. A modified or community build must
  **not** use the Runink name or marks as its own brand, and replaces the files marked
  `LicenseRef-Runink-Trademark` with its own artwork ([docs/LICENSING.md](docs/LICENSING.md)
  says which files and how). The build supports this directly: `RIVER_BRANDING_DIR` points
  it at your artwork.
- Do not use the marks in a way that suggests the project, or Runink, endorses or is
  affiliated with your product.

The project is always named in full, "Runink River". A bare "River" is not used as a mark:
it is the name of an unrelated Wayland compositor.

## Where the marks are meant to go

Runink's stated intent is to transfer the project marks ("Runink River" and its community
mark) to a vendor-neutral foundation if the project is accepted by one, as phase 3 of
[GOVERNANCE.md](GOVERNANCE.md#path-to-vendor-neutral-governance) describes. The foundation's
trademark policy then replaces this one. Which marks transfer, and what happens to the
`runink-*` identifiers on installed machines, are open decisions, recorded as brackets in
[CHARTER.md](CHARTER.md) §5. No application has been submitted, and nothing has been
transferred.

## Neutrality

The marks are the steward's; the project is everyone's. Holding the marks gives Runink no
say over technical decisions outside the governance process:

- Decisions are made in public by the maintainers and the TSC under
  [GOVERNANCE.md](GOVERNANCE.md), where no vendor holds a veto.
- This repository ships no proprietary component and names no vendor's product. Every
  vendor, Runink included, builds on Runink River through the same public interfaces
  (external profiles, payloads, the first-boot contract), outside this repository.
- The TSC seat cap (no employer above one third of the seats) applies as soon as there are
  enough maintainers to fill a TSC.

Questions about the marks go to the maintainers in [MAINTAINERS.md](MAINTAINERS.md).

Linux® is the registered trademark of Linus Torvalds in the U.S. and other countries.
