<!--
SPDX-FileCopyrightText: 2026 The RIVER Authors
SPDX-License-Identifier: MIT
-->

# Runink River release signing

Runink River releases are signed with **one OpenPGP key, held offline**: today the lead
maintainer's key (the Arch-packager model: one accountable person signs), which may gain a
`Runink River Release Engineering` UID or be succeeded by a dedicated project key.

| | |
| --- | --- |
| Key (UID) | the lead maintainer's key (see [KEYS](../KEYS)); a `Runink River Release Engineering` UID may be added to it |
| Algorithm | nistp384 |
| Fingerprint | `95C0A7B97D547413E42660DDB06FE75626F15BF3` (published in [KEYS](../KEYS), river#144); the same value is in `base/keys/owner/fingerprint` (what `base/river-sign` pins) and in `KEY_FPR_PINNED` in `install.sh` |
| Public key | `https://runink.org/.well-known/gpg-key.txt`, and the public keyservers |
| Where the private key lives | Offline, with the release maintainers. Never in CI, never in a repository secret. |

## What is signed

- **Tags:** every release tag is signed, `git tag -s <tag>`. Verify with
  `git verify-tag <tag>`.
- **Artifacts:** each release publishes `SHA256SUMS` and a detached signature
  `SHA256SUMS.asc` over it; the ISO and packages are covered by the checksums. Packages
  in the `[runink]` repository carry detached `.sig` files (`base/river-sign`), and so does its
  database; installed systems require both ([REPOSITORY.md](REPOSITORY.md)).
- **Supply-chain metadata** (optional, additive): `release-attest.yml` produces SBOMs and
  GitHub build provenance, and can add Sigstore keyless bundles. These never replace the
  OpenPGP signature.

## Verifying a release

```bash
curl -fsSLO https://runink.org/.well-known/gpg-key.txt
gpg --import gpg-key.txt
gpg --fingerprint 95C0A7B97D547413E42660DDB06FE75626F15BF3   # compare with KEYS
gpg --verify SHA256SUMS.asc SHA256SUMS
sha256sum -c SHA256SUMS --ignore-missing
```

## Process

1. CI and the builders only ever produce **unsigned** artifacts plus `SHA256SUMS`.
2. A release maintainer downloads them, checks them against a local build or the CI run,
   and signs offline with `base/river-sign` (which refuses to run in CI).
3. The maintainer uploads `SHA256SUMS.asc` (and package `.sig` files) and pushes the
   signed tag.

## Key lifecycle (owner-side, tracked in docs/governance/LF-AIDATA.md)

- Generate the key on an offline machine; keep the primary offline and use a signing
  subkey (ideally on a hardware token).
- Publish the public key at the URL above and on keyservers; record the fingerprint here,
  in `SECURITY.md` and in `base/keys/owner/fingerprint`.
- Create a revocation certificate and store it separately.
- Rotate the signing subkey on a schedule; announce any rotation in the release notes.

Secure Boot uses X.509 keys, which are a separate chain: see
[SECURE-BOOT.md](SECURE-BOOT.md).
