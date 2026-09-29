---
title: Verify a release
weight: 2
description: "Check a Runink River ISO before you write it: the OpenPGP signature on SHA256SUMS, the checksum, the build provenance, the Sigstore bundle and the evidence the release gate attaches."
---
<!--
SPDX-FileCopyrightText: 2026 The RIVER Authors
SPDX-License-Identifier: MIT
-->

A Runink River release is only published after the release gate has checked the exact ISO
you download ({{< repo "docs/VERIFY.md" >}}). Verifying it yourself takes a minute, and the
first check, the signature, is the one that matters: everything else is additional evidence.

{{< callout type="info" >}}
The release-signing key is published and pinned; **no Runink River release signed with it has
been published yet** ([Releases]({{< relref "/docs/releases" >}})). The commands below are
what you will run on the first one.
{{< /callout >}}

## The key

| | |
|---|---|
| Fingerprint | `95C0A7B97D547413E42660DDB06FE75626F15BF3` |
| Algorithm | NIST P-384 (`nistp384`) |
| Public key | `https://runink.org/.well-known/gpg-key.txt` |
| Pinned in | {{< repo "KEYS" >}} and `KEY_FPR_PINNED` in {{< repo "install.sh" >}}, set in the same commit |
| Signs | each release's `SHA256SUMS`, as the detached signature `SHA256SUMS.asc` |

Always compare the **full fingerprint**, never a short key ID or a name: the user IDs on the
key may change (a "Runink River Release Engineering" user ID may be added to it), the
fingerprint does not.

## The quick way: install.sh

On any Linux machine:

```bash
curl -fsSL https://raw.githubusercontent.com/org-runink/river/main/install.sh | sh -s -- --dry-run
```

`install.sh` downloads the public key, checks that the key which made the signature is the
pinned fingerprint, verifies `SHA256SUMS.asc` over `SHA256SUMS`, and checks the ISO's sha256.
It **fails closed**: no fingerprint, no signature or a wrong checksum means no image.

| Option | Does |
|---|---|
| `--dry-run` | verify the release metadata, print what would be done, change nothing |
| `--out DIR` | put the ISO in `DIR` (default: the current directory) |
| `--write DEV` | after verifying, write the ISO to the USB stick `DEV`, after you type its serial |
| `--yes-i-have-checked-serial=S` | confirm the stick's serial without a prompt, for scripts |

It never runs `sudo` itself; a step that needs root prints the command instead. Its only
requests are the release files and the public key. `RIVER_KEY_FPR` overrides the pinned
fingerprint, which means you are choosing what to trust; leave it unset.

## By hand

### 1. The signature and the checksum

```bash
curl -fsSLO https://runink.org/.well-known/gpg-key.txt
gpg --import gpg-key.txt
gpg --fingerprint 95C0A7B97D547413E42660DDB06FE75626F15BF3   # must match KEYS
gpg --verify SHA256SUMS.asc SHA256SUMS                     # must say "Good signature"
sha256sum -c --ignore-missing SHA256SUMS                   # the ISO must say "OK"
```

`gpg --verify` also prints a warning that the key is not certified with a trusted signature
unless you have signed it yourself; that is expected. What matters is "Good signature" and a
primary key fingerprint equal to the one above.

### 2. An ISO published in parts

An ISO larger than 2 GiB (GitHub's limit for one release file) is also published as numbered
parts, each listed in the signed `SHA256SUMS`. `install.sh` joins them for you; by hand:

```bash
cat runink-river-<date>-x86_64.iso.part-* > runink-river-<date>-x86_64.iso
sha256sum -c --ignore-missing SHA256SUMS
```

### 3. Provenance: which workflow published it, from which commit

```bash
gh attestation verify runink-river-<date>-x86_64.iso --repo org-runink/river
```

### 4. The Sigstore signature

A keyless signature, additional to the OpenPGP one, never a replacement for it:

```bash
cosign verify-blob runink-river-<date>-x86_64.iso \
  --bundle runink-river-<date>-x86_64.iso.sigstore.json \
  --certificate-identity-regexp '^https://github.com/org-runink/river/.github/workflows/release-gate.yml@' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
```

The evidence files carry their own bundles and are listed, with their checksums, in
`EVIDENCE.SHA256SUMS`.

### 5. The signed tag

```bash
git clone https://github.com/org-runink/river.git && cd river
git verify-tag runink-os-YYYY.MM
```

## What the evidence tells you

| File | Tells you |
|---|---|
| `<iso>.spdx.json`, `<iso>.cdx.json` | every package and file in the image (SPDX and CycloneDX SBOMs) |
| `river-source.*` | the SBOM of the source tree |
| `<iso>.ownfiles.txt` | every file Runink River added or changed relative to the packages it comes from: the part of the image that is the project's own |
| `<iso>.archaudit.json`, `.txt` | distribution packages checked against the Arch Linux security tracker; a High or Critical advisory with a fix available blocks the release |
| `<iso>.govulncheck.txt` | the image's own Go programs checked with govulncheck |
| `<iso>.gitleaks.json` | the secret scan of the project's own files |

Known, accepted advisories are waivers in `.github/release-gate/waivers.txt`, each with an
owner and an expiry date; an expired waiver blocks the next release.

## Honest limits

- The ISO is **not reproducible yet**, so the provenance proves which digests were published,
  not how they were built (SLSA Build Level 1). A release maintainer compares the artifacts
  with their own build before signing.
- Secure Boot keys are a separate X.509 chain, and Secure Boot is not supported yet:
  [Secure Boot]({{< relref "secure-boot" >}}).
