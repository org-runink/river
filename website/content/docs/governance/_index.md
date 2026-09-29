---
title: Licensing
weight: 13
description: "Which licence covers which part of Runink River: MIT userspace and docs, a GPL-2.0-only kernel tree, CDDL-1.0 OpenZFS as a separate module package, CC-BY-4.0 artwork and the reserved marks."
---
<!--
SPDX-FileCopyrightText: 2026 The RIVER Authors
SPDX-License-Identifier: MIT
-->

Runink River is open source. Its own code and documentation are MIT; two packaging trees are
deliberately not, because of what they package. The authoritative per-path mapping is
{{< repo "REUSE.toml" >}} with the licence texts in {{< repo "LICENSES/" >}}, checked in CI
with `river lint reuse`; the reasoning is in {{< repo "docs/LICENSING.md" >}}. Where a summary and
`REUSE.toml` disagree, `REUSE.toml` wins.

## The licence map

| What | Licence |
|---|---|
| Runink River's own userspace: the installer, the scripts and build tooling, the configuration and s6 service tree, the workflows, and this documentation | **MIT** |
| The kernel packaging tree, `build/pkgbuilds/runink-kernel/` (PKGBUILD, configuration, patches) | **GPL-2.0-only** |
| The OpenZFS packaging tree, `build/pkgbuilds/runink-zfs/` | **CDDL-1.0**, built as a **separate, out-of-tree module package**, never merged into the kernel |
| Runink River artwork files, as `REUSE.toml` maps them | **CC-BY-4.0** |
| The Runink River community mark (the mascot and the logos, icons, splash and wallpaper rendered from it) and the Runink name | **`LicenseRef-Runink-Trademark`**: all rights reserved |
| Third-party files (for example the CachyOS Emerald wallpapers, GPL-3.0-only) | their upstream licence |

MIT is compatible with shipping the GPL-2.0 kernel and the CDDL-1.0 ZFS modules as
**separate packages** in the same image. The ZFS boundary (a prebuilt module package, never
`CONFIG_ZFS=y`) is explained in {{< repo "docs/governance/ZFS-LICENSING.md" >}}.

## No proprietary components

Every file in the repository is under an open licence, except the marks, which are reserved
as a trademark and can be swapped out. Products may be built **on** Runink River outside
this repository, as a downstream distribution or an out-of-tree payload
(`RIVER_PAYLOAD_DIR`); their licences travel with them. An image built without a payload,
which the public Runink River image always is, is complete and fully open.

## Using the marks

The marks identify unchanged Runink River releases. A modified or community build replaces
them; see [Brand assets]({{< relref "/docs/brand" >}}) for what is covered and
{{< repo "LICENSES/LicenseRef-Runink-Trademark.txt" >}} for the terms.

## New files

Every new file declares its licence in an SPDX header, or in `REUSE.toml` if it cannot carry
a comment:

```sh
# SPDX-FileCopyrightText: 2026 The RIVER Authors
# SPDX-License-Identifier: MIT
```

"The RIVER Authors" are the copyright holders recorded in the git history. Contributions are
made under the [DCO]({{< relref "/docs/contributing#sign-off-every-commit-dco" >}}), not a
CLA. Who decides what, and how, is on [Governance]({{< relref "/docs/contributing/governance" >}}).
