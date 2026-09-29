<!--
SPDX-FileCopyrightText: 2026 The RIVER Authors
SPDX-License-Identifier: MIT
-->

# Security policy

> **Pending owner actions.**
> - `security@runink.org` must be read by at least two people.
> - **Private vulnerability reporting** must be enabled under Settings → Code security.
> - Until a second maintainer joins ([MAINTAINERS.md](MAINTAINERS.md)), one person handles
>   every report, so the independent review in step 3 below cannot happen yet.

## Supported versions

Runink River is released as dated workstation images (`runink-os-YYYY.MM`) plus the packages built for
them.

| Version                           | Supported                                   |
| --------------------------------- | ------------------------------------------- |
| latest `runink-os-*` release      | yes                                         |
| `main`                            | yes (fixes land here first)                 |
| any older release                 | no, reinstall or upgrade to the latest      |

After a stable release cadence exists, this policy will cover the latest two releases.

## Reporting a vulnerability

**Please do not open a public issue, discussion or pull request for a security problem.**

Report privately through either channel:

1. **Encrypted email** (preferred): encrypt your report to the **Runink River release-signing**
   key and send it to `security@runink.org`. The public key is published at
   <https://runink.org/.well-known/gpg-key.txt>. Fetch and check it first:
   ```bash
   curl -fsSLO https://runink.org/.well-known/gpg-key.txt
   gpg --import gpg-key.txt
   gpg --fingerprint 95C0A7B97D547413E42660DDB06FE75626F15BF3   # must match the fingerprint published in this file
   gpg --encrypt --armor --recipient 95C0A7B97D547413E42660DDB06FE75626F15BF3 report.txt
   ```
2. **GitHub private vulnerability reporting**: the *Report a vulnerability* button on the
   repository's **Security** tab.

Please include:

- the affected component (kernel/ZFS packaging, installer, firewall, `river-sandbox`, k0s
  configuration, release artifacts, the RIVER runtime, ...), with the version or commit;
- the impact and, if you can, a proof of concept or reproduction steps;
- whether you think the issue is already known or being exploited.

## What happens next

| Step                                  | Target                         |
| ------------------------------------- | ------------------------------ |
| Acknowledge receipt                   | within 3 working days          |
| First assessment (severity, scope)    | within 10 working days         |
| Fix or mitigation, **critical**       | within 14 days of the assessment |
| Fix or mitigation, **high**           | within 30 days of the assessment |
| Fix or mitigation, **medium or higher that is already public** | within 60 days of it becoming public |
| Fix or mitigation, any other          | within 90 days of the report   |
| Public advisory (GHSA, CVE if needed) | when the fix ships             |

We follow **coordinated disclosure**. We agree an embargo with the reporter, credit you in
the advisory unless you ask us not to, and request a CVE through GitHub's CNA where one is
warranted. If a fix cannot land within these targets, we will explain why and agree a new
date with you.

## How maintainers handle a report

This is the vulnerability response process. It applies to every report, whichever channel
it came in on, and to vulnerabilities a maintainer finds on their own.

1. **Receive and acknowledge.** The maintainer on duty (today: every maintainer in
   [MAINTAINERS.md](MAINTAINERS.md)) acknowledges the report and opens a **private GitHub
   security advisory** draft for it. All discussion from then on happens in that draft or
   in encrypted mail, never in a public issue, pull request, commit message or chat.
2. **Triage.** Reproduce the problem on the latest release and on `main`. Decide whether it
   is in Runink River's own code or packaging, or upstream (see "Scope notes"); an
   upstream-only issue is reported upstream, and we track the fixed version. Score it with
   **CVSS v4.0** (v3.1 if the CNA requires it) and record the affected versions and
   components in the draft.
3. **Fix in private.** Develop the fix on the advisory's private fork. It gets a
   regression check that fails without it (a test, a lint rule or an assertion, per
   [CONTRIBUTING.md](CONTRIBUTING.md#tests)) and is reviewed by a maintainer who did not
   write it, as [GOVERNANCE.md](GOVERNANCE.md) requires for an embargoed fix. Pinned
   upstream bumps follow the usual checksum and signature rules.
4. **Coordinate.** Agree the disclosure date with the reporter. When the issue also
   affects an upstream or another distribution, notify them before the date. The embargo
   is as short as the fix allows and never longer than 90 days.
5. **Release.** Merge the fix, build and sign the release ([RELEASE.md](RELEASE.md)), and
   publish the advisory with the CVE, the affected and fixed versions, the workaround if
   there is one and the reporter's credit. The release notes in
   [CHANGELOG.md](CHANGELOG.md) list the fix under **Security** with the CVE or advisory ID.
6. **Follow up.** If the class of bug could recur, add a check that finds it (a lint, a
   test or a CI rule), and record anything the process got wrong in the advisory.

Reporters are credited in the advisory and in the release notes, by the name they choose,
unless they ask not to be.

## Scope notes

These are in scope and especially wanted:

- escapes from `river-sandbox` (bubblewrap confinement) or its `--rw` deny-list;
- ways around the default-deny nftables firewall (`runink-fw`), its base or private posture, or its default-drop forward chain;
- secrets reaching the ISO, the installed image, logs or the process list (the
  invariant: keys, OIDC client IDs and runner tokens arrive at first-boot enrollment only);
- supply-chain problems: an unpinned or unverified upstream in a PKGBUILD, a CI workflow
  that can be steered by an untrusted PR, or unsigned release artifacts.

**Known, documented weaknesses** (already tracked, so no report is needed):

- The **live installer ISO** has a default `runink`/`runink` account with autologin and
  passwordless sudo, and starts `sshd` (`iso-profiles/river/profile.yaml`); the host
  firewall keeps port 22 closed on the live medium, but do not open it there, and do not
  boot the live ISO on an untrusted network. The installed system carries neither the live
  password nor the passwordless sudo.
- `sshd` runs on an installed machine with the distribution's default configuration
  (password logins allowed); the host firewall keeps port 22 closed until the owner lists it
  in `/etc/runink/fw-open`.

Out of scope: vulnerabilities in upstream projects we package unchanged (Linux, OpenZFS,
k0s, ...). Report those upstream. Do tell us if our packaging makes them worse or keeps a
fixed version from reaching users. Also out of scope: applications or platforms that
third parties ship on Runink River as downstream payloads; report those to their vendor.

## Verifying releases

Runink River releases, packages and tags are **signed with the Runink River release-signing
OpenPGP key** (today the lead maintainer's key, [KEYS](KEYS)), fingerprint **`95C0A7B97D547413E42660DDB06FE75626F15BF3`**. Signing happens
locally, never in CI; the full procedure is in
[docs/RELEASE-SIGNING.md](docs/RELEASE-SIGNING.md). The release workflow
(`.github/workflows/release-attest.yml`) produces unsigned artifacts, `SHA256SUMS`, SBOMs
and SLSA provenance. A release engineer then signs `SHA256SUMS`, and the detached signature
`SHA256SUMS.asc` is published next to it.

```bash
# 1. Get the key and check its fingerprint against the one published here and in
#    docs/RELEASE-SIGNING.md. Do not trust a key by its ID alone.
curl -fsSLO https://runink.org/.well-known/gpg-key.txt
gpg --import gpg-key.txt
gpg --fingerprint 95C0A7B97D547413E42660DDB06FE75626F15BF3

# 2. Verify the signature over the checksum file.
gpg --verify SHA256SUMS.asc SHA256SUMS
#    Must say "Good signature" with primary key fingerprint 95C0A7B97D547413E42660DDB06FE75626F15BF3.

# 3. Verify the ISO (and anything else you downloaded) against the signed checksums.
sha256sum -c --ignore-missing SHA256SUMS

# 4. Verify a release tag in a clone of the repository.
git verify-tag runink-os-YYYY.MM
```

`runink-*` and `linux-runink` pacman packages are signed with the same key. Import it into
pacman's keyring with `pacman-key --add gpg-key.txt && pacman-key --lsign-key
95C0A7B97D547413E42660DDB06FE75626F15BF3`, only after you have checked the fingerprint as in step 1.

**Optional, additive:** a release may also carry Sigstore `*.sigstore.json` bundles
(cosign keyless) and a GitHub build-provenance attestation
(`gh attestation verify <file> --repo org-runink/river`). These add evidence. They do
**not** replace the release-engineering GPG signature, which is the authoritative one.
