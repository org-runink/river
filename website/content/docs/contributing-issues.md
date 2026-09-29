---
title: Bug reports & feature requests
linkTitle: Bugs & features
weight: 14
description: "How to report a Runink River bug that can be acted on, how to propose a feature, and what will be declined because it breaks an invariant."
---
<!--
SPDX-FileCopyrightText: 2026 The RIVER Authors
SPDX-License-Identifier: MIT
-->

Runink River tracks bugs and feature requests as
[GitHub issues](https://github.com/org-runink/river/issues). Two templates are offered when
you open one: **Bug Report** and **Feature Request**.

{{< callout type="error" >}}
**Security problems never go in an issue.** Report them privately, as described in
[Report a vulnerability]({{< relref "/docs/security/reporting" >}}).
{{< /callout >}}

## Report a bug

[Open a bug report](https://github.com/org-runink/river/issues/new?template=bug_report.md).
A report that can be acted on has:

- **the version**, from `cat /etc/runink-os-version` on the machine (or the commit, for a
  build from source);
- **the hardware**: CPU, RAM, disk type (NVMe, SATA SSD, hard disk) and the number of disks;
- **what you did, what you expected and what happened**, step by step;
- **the messages**, copied as text rather than photographed where you can.

Useful output, depending on the problem:

| Problem | Collect |
|---|---|
| The installer refused the machine or a disk | `river-hwprobe --json` and the plan (`river-plan --probe probe.json --no-models --profile workstation`) from the live session |
| An install step failed | the failing step's name and its output: **Show details** in the graphical installer, or the terminal output of `sudo runink-install` |
| The machine does not boot | the last lines on the console, and whether the passphrase prompt appeared |
| A service is down | `s6-rc -a list`, and `s6-svstat /run/service/<service>` for a longrun |
| Networking | `river-netsetup --check` (live medium) or `nmcli device status` |
| The firewall | `sudo runink-fw list` and `sudo dmesg \| grep runink-fw` |
| Disks, ZFS | `zpool status`, `zfs list`, `zfs get -r encryption,keystatus <pool>` |

Runink River uses s6, not systemd: there is no `journalctl` and no `systemctl status`.

**Remove private data before you paste.** Hostnames, IP and MAC addresses, disk serial
numbers, user names and SSH keys all appear in the output above. Never paste the recovery key
or a passphrase.

Before you open a new issue, search the [open issues](https://github.com/org-runink/river/issues)
and the [troubleshooting page]({{< relref "/docs/troubleshooting" >}}).

## Propose a feature

[Open a feature request](https://github.com/org-runink/river/issues/new?template=feature_request.md).
The template asks for a summary, the motivation, a proposed solution, the alternatives you
considered and the **architectural impact**: which part of the system it touches and whether
it affects an invariant. For anything larger than a bug fix, agree on the design in the issue
before you write code ({{< repo "CONTRIBUTING.md" >}}).

## What will be declined

Requests that break one of the project's invariants ({{< repo "AGENTS.md" >}}) are closed
with an explanation, however well they are made. Changing an invariant needs a two-thirds
vote of the technical steering committee
([Governance]({{< relref "/docs/contributing/governance" >}})), not a normal pull request.
Among them:

- systemd, a second init system, or software that works only under systemd;
- a second kernel, or ZFS built into the kernel;
- an unencrypted dataset, or a key written to a file;
- an app store, Flatpak or a container runtime in the base image;
- telemetry, phone-home behaviour or a third-party hosted AI service;
- application logic, or a specific downstream product, in this repository.

Software that ships only systemd units is not automatically out: it needs an s6 service
definition instead ([s6 services]({{< relref "/docs/configuration/services" >}})).

## After you open an issue

A maintainer triages it: asks for missing information, confirms it, or explains why it will
not be changed. Once it is confirmed, anyone may send a fix: see
[Contributing]({{< relref "/docs/contributing" >}}) for the DCO sign-off and the checks to
run. Changes users notice are recorded in {{< repo "CHANGELOG.md" >}}.
