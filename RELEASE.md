<!--
SPDX-FileCopyrightText: 2026 The RIVER Authors
SPDX-License-Identifier: MIT
-->

# Runink River release process

How a Runink River release is cut, what it must pass, and how it is signed. The rules for
*who* may release are in [GOVERNANCE.md](GOVERNANCE.md); the signing key and its handling
are in [docs/RELEASE-SIGNING.md](docs/RELEASE-SIGNING.md).

## What a release is

A release is a **dated image set**: the Runink River ISO (the workstation;
downstream distributions release their own images), the `runink-*` / `linux-runink` packages
built for it, `SHA256SUMS`, its detached signature `SHA256SUMS.asc`, SBOMs and build
provenance. It is identified by a calendar version and a signed git tag:

- `runink-os-YYYY.MM`, and `runink-os-YYYY.MM.N` for a second release in the same month
  (a security release, for example);
- the version is written to `VERSION` and lands on the node as `/etc/runink-os-version`.

`main` is always the next release. There are no long-lived release branches: an older
release is superseded by the next one ([SECURITY.md](SECURITY.md), "Supported versions").

## Cadence

- **Scheduled releases:** no fixed cadence yet. Setting one is an owner decision, tracked
  in [docs/governance/LF-AIDATA.md](docs/governance/LF-AIDATA.md). Until then a release is
  cut when `main` has user-visible changes that passed the checks below.
- **Security releases:** as soon as a fix is ready, within the targets in
  [SECURITY.md](SECURITY.md). An upstream kernel, OpenZFS or k0s security release is picked
  up the same way.

## Release criteria

A tag is cut only when all of these hold on the commit being tagged:

1. CI is green: `tier1`, `installer-go`, `river-guide`, `go-security`, REUSE and the DCO
   check ([docs/governance/CI.md](docs/governance/CI.md)).
2. `make lint` passes locally, including the closure lint that CI cannot run.
3. The ISO builds from that commit (`build/local-iso.sh` and its root stage, or `images.yml`),
   and passes the unattended graphical install and boot (`build/qemu-gui-test.sh`, every
   `RIVERTEST` check OK). The Tier 2 CI job replaces this manual step once its runner exists.
4. **Dependency check.** Every pinned upstream is checked against its advisories: the
   kernel (kernel.org stable and the zen patch), OpenZFS, k0s, the mistral.rs build pinned
   in `guide/model.lock`, the distribution mirror snapshot, and the Go
   modules (`govulncheck`, in CI). An exploitable known vulnerability blocks the release
   until it is fixed or shown not to be exploitable, and the finding is written down in the
   release notes.
5. `CHANGELOG.md` has the release section, with **Security** filled in (or "None.").

## Steps

1. Open a PR that sets `VERSION`, renames `## [Unreleased]` in `CHANGELOG.md` to the new
   version and date, and adds a fresh `## [Unreleased]`. Merge it under the normal review
   rules.
2. Build the ISO and the packages from the merged commit, on a maintainer's builder.
3. Run the release criteria above.
4. `build/release-assets.sh OUT_DIR ISO` prepares the assets: an ISO over GitHub's 2 GiB
   asset limit is split into numbered parts, and `SHA256SUMS` lists every ISO and part.
5. A release maintainer verifies the artifacts against the local build and signs
   `SHA256SUMS` offline with the key whose fingerprint is in [KEYS](KEYS)
   (`SHA256SUMS.asc`), and the packages with `base/river-sign`. CI never holds a signing key.
6. Push the signed tag (`git tag -s`). Create a DRAFT GitHub release for it with the
   changelog section as its notes and every asset plus `SHA256SUMS.asc`, then dispatch `release-gate.yml` (first with
   `dry_run: true`). The gate verifies the signature and checksums, scans the image, attests
   it and publishes the draft; it is the only path to a public release. `release-attest.yml`
   only re-attests an existing release.

`base/river-sign` checks every signature back to the fingerprint in
`base/keys/owner/fingerprint` (the same key as `KEYS`); see [docs/RELEASE-SIGNING.md](docs/RELEASE-SIGNING.md).

## The package repository

The signed `[runink]` pacman repository, which installed systems update from, is published
from the same public build, apart from the image release: `river repo assemble` (River's public
packages only), the owner signs offline with `base/river-sign`, then `river repo verify` and
`river repo publish` (the rolling `repo-x86_64` GitHub prerelease and the runink.org mirror).
It is never marked "latest", so it does not change what `install.sh` downloads. The steps and
what each one guarantees are in [docs/REPOSITORY.md](docs/REPOSITORY.md).

## Reproducibility

Where the build is reproducible today, and where it is not, is recorded in
[docs/BUILD.md](docs/BUILD.md#reproducibility-status). The ISO is not yet reproducible
bit for bit.
