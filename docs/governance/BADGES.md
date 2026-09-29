<!--
SPDX-FileCopyrightText: 2026 The RIVER Authors
SPDX-License-Identifier: MIT
-->

# README badges: Runink River

Every badge at the top of [README.md](../../README.md): what it proves, where its image
comes from, whether it is live, and, for a pending one, the exact action that unlocks it.

**Rule: a badge never claims a status the project does not have.** A badge whose status
does not exist yet (a registration not made, a release not cut, a foundation decision not
taken) is not shown. It sits in the README as an HTML comment,
`<!-- badge:pending <id> ... -->`, with its final markup and its unlock step, so the person
who unlocks it only has to uncomment it and fill in any placeholder.

Order follows the usual CNCF / LF layout: build, security, compliance, community,
distribution. The foundation badge, once earned, goes first.

## Live badges

"Live" means the markup is in the README. They render only while a public repository exists
at the URL in the markup: GitHub serves no workflow badge for a private repository (HTTP 404),
the Scorecard API answers "invalid repo path", and shields.io answers "repo not found".

| # | Badge | What it proves | Image source | Links to | State |
| --- | --- | --- | --- | --- | --- |
| 1 | **CI** | The latest run of `.github/workflows/ci.yml` on `main` passed: Tier 1 (shellcheck, installer/overlay sync, branding, no-Python, pins, AUR sync, in rootless podman), the installer and river-guide Go tests (gofmt, `go vet`, race-enabled `go test`), the analytics harness tests and `go-security` (gosec, govulncheck). | `https://github.com/org-runink/river/actions/workflows/ci.yml/badge.svg?branch=main` | the workflow's runs on `main` | live on publication |
| 2 | **OpenSSF Scorecard** | The score from the latest published Scorecard run (`.github/workflows/scorecard.yml`). | `https://api.scorecard.dev/projects/github.com/org-runink/river/badge` | `https://scorecard.dev/viewer/?uri=github.com/org-runink/river` | live after the first run on the public repository (on push to `main`, or the Monday schedule) |
| 3 | **REUSE** | Every file declares its copyright and licence, and every licence named is in `LICENSES/` (`river lint reuse`, REUSE 3.3). It is the status of `.github/workflows/reuse.yml` on `main`, so it goes red the moment `main` stops being compliant. | `https://github.com/org-runink/river/actions/workflows/reuse.yml/badge.svg?branch=main` | the workflow's runs on `main` | live on publication |
| 4 | **License** | The default licence (root `LICENSE`) is MIT. The alt text and the link carry the rest: the kernel packaging tree is GPL-2.0-only, the OpenZFS packaging tree CDDL-1.0, `validation/` Apache-2.0, artwork CC-BY-4.0. | `https://img.shields.io/badge/license-MIT%20%28default%29-blue` (static) | [docs/LICENSING.md](../LICENSING.md) | live now (static) |
| 5 | **DCO** | The project requires a DCO sign-off on every commit ([CONTRIBUTING.md](../../CONTRIBUTING.md)), checked on every pull request by `.github/workflows/dco.yml`. | `https://img.shields.io/badge/DCO-sign--off%20required-blue` (static) | CONTRIBUTING.md, DCO section | live now (static) |
| 6 | **GitHub stars** | Community interest; LF AI & Data uses stars as an Incubation (500) and Graduation (1000) metric ([LF-AIDATA.md](LF-AIDATA.md)). | `https://img.shields.io/github/stars/org-runink/river?style=social` | the stargazers page | live on publication |

Why these two are static:

- **License.** A licence is a fact about the tree, not a check result; the static badge
  says only what `LICENSE` says. The dynamic `github/license` badge would say the same
  (GitHub detects MIT) with no way to point at the mixed-licence explanation.
- **DCO.** `dco.yml` runs on `pull_request` only. GitHub's badge for a workflow with no run
  on `main` shows the most recent run on *any* branch, so a workflow badge would show the
  last contributor's pull request, going red whenever a newcomer forgets `-s`. That says
  nothing about `main`. The static badge states the policy, which is true; the check that
  enforces it is the workflow. When the owner makes DCO a required status check
  (owner action 2 in [LF-AIDATA.md](LF-AIDATA.md)), no unsigned commit can reach `main`.

## Pending badges

Each is a `<!-- badge:pending <id> -->` block right under the badge row in README.md.

| Id | Badge | Image source | What unlocks it (owner action) | Where it goes |
| --- | --- | --- | --- | --- |
| `lfaidata-sandbox` | **LF AI & Data Sandbox** | `https://img.shields.io/badge/LF%20AI%20%26%20Data-Sandbox-0072C6` (static) | The LF AI & Data TAC votes to accept the project ([LF-AIDATA.md](LF-AIDATA.md) owner actions 1 to 9). Change the message at Incubation / Graduation. | first |
| `openssf-best-practices` | **OpenSSF Best Practices** (formerly CII) | `https://www.bestpractices.dev/projects/PROJECT_ID/badge` | Publish the repository, create the bestpractices.dev account, register the project with the public URL, enter the answers in [OPENSSF-BEST-PRACTICES.md](OPENSSF-BEST-PRACTICES.md) and reach *passing* (owner action 7). Replace `PROJECT_ID` with the number bestpractices.dev assigns; add it within 48 hours of the award (`documentation_achievements`). | after Scorecard |
| (same badge) | **OpenSSF Best Practices silver** | the same image | No second badge: the same image shows "silver" (then "gold") once that level is awarded. Silver is the LF AI & Data Incubation requirement; the unmet silver items are in [OPENSSF-BEST-PRACTICES.md](OPENSSF-BEST-PRACTICES.md). | unchanged |
| `reuse-api` | **REUSE (api.reuse.software)** | `https://api.reuse.software/badge/github.com/org-runink/river` | Register the public repository at <https://api.reuse.software/register> (a maintainer confirms by mail). Optional: it then replaces the workflow-based REUSE badge, which already proves the same from our own CI. | in place of badge 3 |
| `aur-linux-runink` | **AUR linux-runink** | `https://img.shields.io/aur/version/linux-runink` | A maintainer publishes `packaging/aur/linux-runink` to the AUR from their own AUR account ([packaging/aur/PUBLISHING.md](../../packaging/aur/PUBLISHING.md)). Today shields answers "package not found". | after stars |
| `release` | **Latest release** | `https://img.shields.io/github/v/release/org-runink/river?display_name=tag` | The first signed release: the release-engineering key exists and its fingerprint is in `KEYS` (owner action 15), then a `runink-os-YYYY.MM` tag with `SHA256SUMS.asc` ([RELEASE.md](../../RELEASE.md)). Until then the badge would say "no releases". | last |
| `slsa` | **SLSA provenance level** | `https://slsa.dev/images/gh-badge-level1.svg` | A published release attested by `.github/workflows/release-attest.yml` (public repository only). That workflow attests digests of an ISO a maintainer built, which is **SLSA Build L1** with signed provenance, never more ([OPENSSF-BEST-PRACTICES.md](OPENSSF-BEST-PRACTICES.md#honest-slsa-level)). Switch to `gh-badge-level3.svg` only after [ROADMAP](../../ROADMAP.md) milestone 12 (ISO built on an isolated ephemeral builder that emits its own provenance). | after release |

## Considered and not used

- **Go Report Card.** The Go code is four separate modules (`installer/`, `guide/`,
  `validation/`, `bench/analytics/`) with no module at the repository root, which
  goreportcard.com does not grade as one project, and the grade covers `gofmt`, `go vet`,
  `ineffassign` and spelling, which CI already enforces. The CI badge carries that signal.
- **A separate `go vet` badge.** `go vet` runs inside the CI workflow's Go jobs; a second
  badge for part of the same workflow would add nothing.
- **Artifact Hub.** It indexes Helm charts, OCI artefacts, operators and similar; Runink
  River ships ISO images and pacman packages, which it does not list.

## Moving the repository

The badge markup names the repository `org-runink/river`. If the project is published or
moved elsewhere (for example to an organisation of its own, lifecycle task T2 in
[LF-AIDATA.md](LF-AIDATA.md)), repoint every badge with one command, from the repository
root:

```sh
sed -i 's#org-runink/river\b#NEW-ORG/NEW-REPO#g' README.md
```

That also repoints the README's install URL, which must follow the move anyway. Other files
name the slug too (the Go module paths of `guide/` and `bench/analytics/`, `install.sh`,
`SECURITY.md`, the PKGBUILDs); list them with `git grep -l 'org-runink/river'` and move
them in the same change, since a module path is an API. The Scorecard badge starts empty at
a new URL until its first run there; the OpenSSF Best Practices entry keeps its project id
but its repository URL must be edited on bestpractices.dev; a REUSE API registration has to
be made again for the new URL.
