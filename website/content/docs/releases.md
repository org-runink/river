---
title: Releases & versioning
linkTitle: Releases
weight: 11
description: "How Runink River versions its releases, what a release contains, the checks a release must pass, and which versions are supported."
---
<!--
SPDX-FileCopyrightText: 2026 The RIVER Authors
SPDX-License-Identifier: MIT
-->

{{< callout type="warning" >}}
**No signed Runink River release has been published yet.** The release-signing key now exists
and its fingerprint is pinned in {{< repo "KEYS" >}} and in `install.sh`; the first release
signed with it is the next milestone on the [roadmap]({{< relref "/docs/roadmap" >}}). Until
then, [build the ISO yourself]({{< relref "/docs/build" >}}).
{{< /callout >}}

## Version numbers

Runink River uses **calendar versions**, not semantic versions
({{< repo "RELEASE.md" >}}):

| Form | Example | Used for |
|---|---|---|
| `runink-os-YYYY.MM` | `runink-os-2026.10` | a release, its signed git tag and its version file |
| `runink-os-YYYY.MM.N` | `runink-os-2026.10.1` | a second release in the same month, such as a security release |

The version is written to `VERSION` in the repository and lands on an installed machine as
`/etc/runink-os-version`:

```bash
cat /etc/runink-os-version
```

`main` is always the next release. There are no long-lived release branches: an older
release is superseded by the next one.

## What a release contains

- The ISO, `runink-river-<date>-x86_64.iso`. An ISO larger than GitHub's 2 GiB asset limit is
  also published as numbered parts, `<iso>.part-00`, `-01`, and so on.
- `SHA256SUMS`, listing the ISO and every part, and its detached OpenPGP signature,
  `SHA256SUMS.asc`.
- The evidence of the release gate (below): SBOMs, vulnerability and secret-scan reports,
  build provenance and Sigstore bundles.
- A signed git tag, verifiable with `git verify-tag runink-os-YYYY.MM`.

How to check all of it: [Verify a release]({{< relref "/docs/security/verify" >}}).

## Cadence

There is **no fixed cadence yet**; setting one is an open decision. Until then a release is
cut when `main` has changes users will notice and they pass the criteria below. Security
fixes are released as soon as they are ready, within the targets in
{{< repo "SECURITY.md" >}}; upstream kernel and OpenZFS security releases are picked up the
same way.

## Release criteria

A tag is cut only when all of these hold on the commit being tagged:

1. CI is green: `tier1`, `installer-go`, `river-guide`, `go-security`, REUSE and the DCO
   check.
2. `make lint` passes locally, including the closure lint that CI cannot run.
3. The ISO builds from that commit and passes a VM install and boot, with
   `tests/assert-golden.sh` on the installed system (run by hand until the Tier 2 CI runner
   exists).
4. Every pinned upstream is checked against its advisories: the kernel and the zen patch,
   OpenZFS, the distribution mirror snapshot and the Go modules (`govulncheck`). An
   exploitable known vulnerability blocks the release until it is fixed or shown not to be
   exploitable, and the finding is written in the release notes.
5. `CHANGELOG.md` has the release's section, with **Security** filled in, or "None.".

## The release gate

A release stays a **draft** until `.github/workflows/release-gate.yml` has checked the exact
ISO that will be downloaded; the gate is the only thing that publishes it
({{< repo "docs/VERIFY.md" >}}):

| Check | Evidence attached to the release |
|---|---|
| `SHA256SUMS` lists and verifies every ISO | `SHA256SUMS` |
| `SHA256SUMS` is signed offline by the key pinned in `KEYS` | `SHA256SUMS.asc` |
| Distribution packages have no High or Critical advisory with a fix missing (Arch Linux security tracker) | `<iso>.archaudit.json`, `.txt`, `arch-security-tracker.json` |
| The image's own Go programs have no known vulnerability (govulncheck) | `<iso>.govulncheck.txt` |
| No secret in any file the image's authors added or changed (gitleaks) | `<iso>.gitleaks.json`, `<iso>.ownfiles.txt` |
| SBOMs of the image contents and of the source (SPDX, CycloneDX) | `<iso>.spdx.json`, `.cdx.json`, `river-source.*` |
| Build provenance (SLSA) and Sigstore signatures | a GitHub attestation, `*.sigstore.json` |

Accepted advisories are listed as waivers in `.github/release-gate/waivers.txt`, each with an
owner and an expiry date; an expired waiver blocks the next release.

## How a release is made

1. A pull request sets `VERSION`, renames `## [Unreleased]` in `CHANGELOG.md` to the new
   version and date, and opens a fresh `## [Unreleased]`.
2. The ISO and the packages are built from the merged commit on a maintainer's builder.
3. The release criteria are run.
4. A GitHub release is created with the changelog section as its notes and the unsigned
   artifacts attached. CI never holds a signing key.
5. A release maintainer verifies the artifacts against their own build, signs `SHA256SUMS`
   and the packages **offline** (`base/river-sign`, which refuses to run in CI), uploads the
   signatures and pushes the signed tag (`git tag -s`).

## Release notes

Every change a user would notice adds a line under `## [Unreleased]` in
{{< repo "CHANGELOG.md" >}}, which follows [Keep a Changelog 1.1](https://keepachangelog.com/en/1.1.0/).
Sections come in this order: **Security**, **Added**, **Changed**, **Deprecated**,
**Removed**, **Fixed**. **Security** lists every fixed vulnerability with its CVE or GitHub
advisory ID and the reporter's credit, and upstream security bumps with their advisory IDs.
A release without vulnerability fixes says "None." there, so "none" cannot be confused with
"not recorded". The GitHub release carries the same text.

## Supported versions

| Version | Supported |
|---|---|
| the latest `runink-os-*` release | yes |
| `main` | yes (fixes land here first) |
| any older release | no: upgrade to the latest |

Once a stable cadence exists, the policy will cover the latest two releases.
