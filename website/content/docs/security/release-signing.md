---
title: Release signing
weight: 3
---
<!--
SPDX-FileCopyrightText: 2026 The RIVER Authors
SPDX-License-Identifier: MIT
-->

Runink River releases are signed with a **dedicated release-signing key**, published on
2026-09-27 and pinned in {{< repo "KEYS" >}}.

| | |
|---|---|
| Fingerprint | `95C0A7B97D547413E42660DDB06FE75626F15BF3` |
| Algorithm | NIST P-384 (`nistp384`) |
| User ID | the lead maintainer's today; a `Runink River Release Engineering` user ID may be added. Identify the key by its fingerprint, never by a user ID. |
| Public key | `https://runink.org/.well-known/gpg-key.txt` |
| Pinned in | `KEYS`, `KEY_FPR_PINNED` in `install.sh` and `base/keys/owner/fingerprint`, changed together in one commit |
| Private key | offline, with the release maintainers; never in CI, never a repository secret |

The same key is the package-signing root of trust of the planned own base
(`base/keys/owner/`). It is separate from the X.509 keys of [Secure Boot]({{< relref "secure-boot" >}}).
No release signed with it has been published yet.

## What is signed

- **Tags**: every release tag, `runink-os-YYYY.MM`, is a signed tag
  (`git verify-tag runink-os-YYYY.MM`).
- **Artifacts**: each release publishes `SHA256SUMS` and a detached signature over it,
  `SHA256SUMS.asc`. The ISO and the packages are covered by the checksums. Packages carry
  detached `.sig` files.
- **Supply-chain metadata** (additive): SBOMs and GitHub build provenance, optionally
  Sigstore bundles. These add evidence; they never replace the OpenPGP signature.

## How a release is made

1. CI and the builders produce only **unsigned** artifacts plus `SHA256SUMS`.
2. A release maintainer checks them against a local build or the CI run and signs offline
   with `base/river-sign`, which refuses to run in CI.
3. The maintainer uploads `SHA256SUMS.asc` and pushes the signed tag.

A release stays a draft until the release gate has checked it and published it with its
evidence ([Releases]({{< relref "/docs/releases#the-release-gate" >}})). To verify a
download, see [Verify a release]({{< relref "verify" >}}).

## Honest provenance level

The current release workflow can reach **SLSA Build Level 1**: it attests digests after a
maintainer build. Level 3 needs the ISO built on an isolated, ephemeral builder that emits its
own provenance, and that is on the [roadmap]({{< relref "/docs/roadmap" >}}), as is a
reproducible ISO.

Secure Boot uses a separate chain of X.509 keys: [Secure Boot]({{< relref "secure-boot" >}}).
Sources: {{< repo "KEYS" >}}, {{< repo "docs/RELEASE-SIGNING.md" >}} (whose key table
predates the key's publication).
