---
title: Report a vulnerability
weight: 1
---
<!--
SPDX-FileCopyrightText: 2026 The RIVER Authors
SPDX-License-Identifier: MIT
-->

**Please do not open a public issue, discussion or pull request for a security problem.**
Report it privately through either channel:

1. **Encrypted email** to `security@runink.org`, encrypted to the release key published at
   `https://runink.org/.well-known/gpg-key.txt`. Check its fingerprint first:
   `95C0A7B97D547413E42660DDB06FE75626F15BF3`, as pinned in {{< repo "KEYS" >}}.
2. **GitHub private vulnerability reporting**: the *Report a vulnerability* button on the
   repository's [Security tab](https://github.com/org-runink/river/security).

{{< callout type="warning" >}}
The key was published on 2026-09-27; {{< repo "SECURITY.md" >}} still describes it as
pending. If you cannot encrypt to it, use GitHub's private reporting. Until a second
maintainer joins, one person handles every report.
{{< /callout >}}

## What to include

- the affected component (kernel or ZFS packaging, installer, firewall, `river-sandbox`,
  release artifacts, ...) with the version (`/etc/runink-os-version`) or commit;
- the impact and, if you can, a proof of concept or steps to reproduce;
- whether you think it is already known or exploited.

## What happens next

| Step | Target |
|---|---|
| Acknowledge receipt | within 3 working days |
| First assessment | within 10 working days |
| Fix, critical | within 14 days of the assessment |
| Fix, high | within 30 days of the assessment |
| Fix, medium or higher already public | within 60 days of it becoming public |
| Fix, anything else | within 90 days of the report |

Runink River follows **coordinated disclosure**: an embargo agreed with you, credit in the
advisory unless you prefer otherwise, and a CVE through GitHub's CNA where warranted.

## Especially wanted

- escapes from `river-sandbox` or its deny-list;
- ways around the default-deny firewall;
- secrets reaching the ISO, the installed system, logs or the process list;
- supply-chain problems: an unpinned or unverified upstream, a workflow an untrusted pull
  request can steer, unsigned release artifacts.

Known and documented, no report needed: the **live** medium has a default `runink`/`runink`
account with autologin (never boot it on an untrusted network; the installed system does not
carry it). Vulnerabilities in upstream projects packaged unchanged (Linux, OpenZFS, KDE, ...)
go to those projects. The full policy: {{< repo "SECURITY.md" >}}.
