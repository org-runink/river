---
title: Features
weight: 2
sidebar:
  open: true
---
<!--
SPDX-FileCopyrightText: 2026 The RIVER Authors
SPDX-License-Identifier: MIT
-->

What makes Runink River different from a general-purpose desktop distribution, one page per
feature. Each page says what exists today and marks what is still planned.

{{< cards >}}
  {{< card link="s6" title="s6 init" icon="lightning-bolt" subtitle="s6, s6-rc and s6-linux-init. Never systemd." >}}
  {{< card link="kernel" title="The linux-runink kernel" icon="chip" subtitle="zen 7.2.x stable, verified sources, a data-work profile." >}}
  {{< card link="zfs-encryption" title="ZFS & encryption" icon="lock-closed" subtitle="Encrypted ZFS root, boot environments, shadow-grade file modes." >}}
  {{< card link="firewall" title="Firewall" icon="shield-check" subtitle="Default-deny nftables for IPv4 and IPv6." >}}
  {{< card link="sandbox" title="river-sandbox" icon="cube-transparent" subtitle="bubblewrap confinement for code you do not trust." >}}
  {{< card link="installer" title="Installer & hardware planner" icon="cursor-click" subtitle="Measure, plan, confirm by serial, install." >}}
  {{< card link="install-guide" title="Offline install guide" icon="chat-alt-2" subtitle="river-guide: grounded answers, no network needed." >}}
{{< /cards >}}

## The design rules behind them

Every change to Runink River keeps a short list of invariants, written down in
{{< repo "AGENTS.md" >}}. Changing one needs a vote of the technical steering committee
([Governance]({{< relref "/docs/contributing/governance" >}})). The ones a user notices:

1. **s6, never systemd.**
2. **Exactly one kernel**, `linux-runink`, with OpenZFS as a separate module package.
3. **ZFS root, encrypted.** Never a dataset with `encryption=off`.
4. **Secrets are handled like `/etc/shadow`**: 0600 files in 0700 directories, and no
   secret or key ever baked into an image.
5. **Untrusted code runs under `river-sandbox`.**
6. **Every upstream is pinned** by version and checksum, and by signature where the upstream
   signs.
