# Verifying a Runink River release

Every Runink River release is published by the **release gate**
(`.github/workflows/release-gate.yml`), and only after these checks passed on the exact ISO
you download:

| Check | Evidence attached to the release |
|---|---|
| `SHA256SUMS` lists and verifies every ISO | `SHA256SUMS` |
| `SHA256SUMS` is signed offline by the release key pinned in [KEYS](../KEYS) | `SHA256SUMS.asc` |
| Distribution packages have no High/Critical advisory whose fix is missing (Arch Linux security tracker) | `<iso>.archaudit.json`, `.txt`, `arch-security-tracker.json` |
| The image's own Go programs have no known vulnerability (govulncheck) | `<iso>.govulncheck.txt` |
| No secret in any file the image's authors added or changed (gitleaks) | `<iso>.gitleaks.json`, `<iso>.ownfiles.txt` |
| SBOM of the image contents and of the source (SPDX, CycloneDX) | `<iso>.spdx.json`, `.cdx.json`, `river-source.*` |
| Build provenance (SLSA) and Sigstore signatures | GitHub attestation, `*.sigstore.json` |

A release that failed any check is never published: it stays a draft.

An ISO larger than 2 GiB (GitHub's per-asset limit) is published as numbered parts,
`<iso>.part-00`, `-01`, …, each listed in the signed `SHA256SUMS` next to the whole ISO.
`install.sh` downloads, verifies and joins them for you; by hand:
`cat <iso>.part-* > <iso>` and then `sha256sum -c --ignore-missing SHA256SUMS`.

## 1. The signature (the root of trust)

```sh
curl -fsSLO https://runink.org/.well-known/gpg-key.txt
gpg --import gpg-key.txt
gpg --fingerprint <the fingerprint in KEYS>     # must match KEYS in this repository
gpg --verify SHA256SUMS.asc SHA256SUMS
sha256sum -c --ignore-missing SHA256SUMS
```

## 2. Provenance (which workflow published this ISO, from which commit)

```sh
gh attestation verify runink-river-<date>-x86_64.iso --repo org-runink/river
```

## 3. Sigstore signature (keyless, additive to the OpenPGP one)

```sh
cosign verify-blob runink-river-<date>-x86_64.iso \
  --bundle runink-river-<date>-x86_64.iso.sigstore.json \
  --certificate-identity-regexp '^https://github.com/org-runink/river/.github/workflows/release-gate.yml@' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
```

The evidence files carry their own bundles and are listed, with their checksums, in
`EVIDENCE.SHA256SUMS`.

## 4. What is inside

The SBOMs list every package and file in the image. `<iso>.ownfiles.txt` lists every file
the image's authors added or changed relative to the packages they come from, which is the
part of the image that is Runink River's own.

Waivers (known, accepted advisories) are in `.github/release-gate/waivers.txt`; each one has
an owner and an expiry date, and an expired waiver blocks the next release.
