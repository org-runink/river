# Features

<!--
SPDX-FileCopyrightText: 2026 The RIVER Authors
SPDX-License-Identifier: MIT
-->

What makes Runink River different from a general-purpose desktop distribution, one page per
feature. Each page says what exists today and marks what is still planned.

- [s6 init](s6/README.md): s6, s6-rc and s6-linux-init. Never systemd.
- [The linux-runink kernel](kernel/README.md): zen 7.2.x stable, verified sources, a data-work profile.
- [ZFS & encryption](zfs-encryption/README.md): Encrypted ZFS root, boot environments, shadow-grade file modes.
- [Firewall](firewall/README.md): Default-deny nftables for IPv4 and IPv6.
- [river-sandbox](sandbox/README.md): bubblewrap confinement for code you do not trust.
- [Installer & hardware planner](installer/README.md): Measure, plan, confirm by serial, install.
- [Offline install guide](install-guide/README.md): river-guide: grounded answers, no network needed.

## The design rules behind them

Every change to Runink River keeps a short list of invariants, written down in
[`AGENTS.md`](https://github.com/org-runink/river/blob/main/AGENTS.md). Changing one needs a vote of the technical steering committee
([Governance](../contributing/governance/README.md)). The ones a user notices:

1. **s6, never systemd.**
2. **Exactly one kernel**, `linux-runink`, with OpenZFS as a separate module package.
3. **ZFS root, encrypted.** Never a dataset with `encryption=off`.
4. **Secrets are handled like `/etc/shadow`**: 0600 files in 0700 directories, and no
   secret or key ever baked into an image.
5. **Untrusted code runs under `river-sandbox`.**
6. **Every upstream is pinned** by version and checksum, and by signature where the upstream
   signs.
