<!--
SPDX-FileCopyrightText: 2026 The RIVER Authors
SPDX-License-Identifier: MIT
-->

# Contributing to Runink River

Thanks for your interest in Runink River, the developer workstation on s6. This file covers
how to propose a change, what we check before merging, and the one legal requirement: the
**Developer Certificate of Origin (DCO)**.

Only the maintainers in [MAINTAINERS.md](MAINTAINERS.md) can merge.

## Ground rules

- Be kind. Everyone taking part must follow the [Code of Conduct](CODE_OF_CONDUCT.md).
- **Do not report security problems in public issues or PRs.** Use the private process in
  [SECURITY.md](SECURITY.md).
- Read [AGENTS.md](AGENTS.md) before you change anything. Its **Invariants** section lists
  rules a PR must not break. Some of them: s6 and never systemd, exactly one kernel, an
  encrypted ZFS root, a default-deny firewall, pinned upstreams, and no secrets or models in
  the image. A PR that breaks one of these is closed, however good the code is. AGENTS.md also
  has the exact build and test commands and the known traps.
- This repository ships OS + packaging + installer + the RIVER runtime. It does not ship
  application logic. Applications built on Runink River live outside the repository as optional
  downstream payloads (`RIVER_PAYLOAD_DIR`); see [docs/LICENSING.md](docs/LICENSING.md).

## Developer Certificate of Origin (DCO)

Runink River uses the [Developer Certificate of Origin 1.1](https://developercertificate.org/)
and not a CLA. The DCO is the Linux kernel's model and the Linux Foundation default. When
you sign off a commit, you certify that you wrote the change, or that you otherwise have
the right to submit it under the project's license:

```
Developer Certificate of Origin
Version 1.1

Copyright (C) 2004, 2006 The Linux Foundation and its contributors.

Everyone is permitted to copy and distribute verbatim copies of this
license document, but changing it is not allowed.

Developer's Certificate of Origin 1.1

By making a contribution to this project, I certify that:

(a) The contribution was created in whole or in part by me and I
    have the right to submit it under the open source license
    indicated in the file; or

(b) The contribution is based upon previous work that, to the best
    of my knowledge, is covered under an appropriate open source
    license and I have the right under that license to submit that
    work with modifications, whether created in whole or in part
    by me, under the same open source license (unless I am
    permitted to submit under a different license), as indicated
    in the file; or

(c) The contribution was provided directly to me by some other
    person who certified (a), (b) or (c) and I have not modified
    it.

(d) I understand and agree that this project and the contribution
    are public and that a record of the contribution (including all
    personal information I submit with it, including my sign-off) is
    maintained indefinitely and may be redistributed consistent with
    this project or the open source license(s) involved.
```

**How to sign off.** Add a `Signed-off-by` trailer with your real name and an email you
can be reached at. `git commit -s` adds it for you:

```
Signed-off-by: Jane Doe <jane@example.org>
```

**Every commit in a PR must be signed off.** The `DCO` workflow
(`.github/workflows/dco.yml`) checks this on each pull request. To fix a PR that is
missing sign-offs:

```bash
git rebase --signoff origin/main      # sign off every commit on the branch
git push --force-with-lease
```

**AI-assisted contributions.** You may use AI coding tools, but you are the one who signs
off. Your sign-off certifies the DCO for the whole change, including any generated parts.
Review it as if you had written it. Record material assistance with a `Co-Authored-By:`
or `Assisted-by:` trailer. Never paste code whose license you cannot state.

## Licensing of contributions

Each file declares its license in an SPDX header, or in `REUSE.toml` for files that cannot
carry a comment. New files **must** start with an SPDX header:

```sh
# SPDX-FileCopyrightText: 2026 The RIVER Authors
# SPDX-License-Identifier: MIT
```

The project license is **MIT** (root [`LICENSE`](LICENSE)), for code and docs alike, with
two exceptions:

- files under `build/pkgbuilds/runink-kernel/` (the kernel PKGBUILD, config and kernel
  patches) are **GPL-2.0-only**, and a kernel patch carries the SPDX identifier of the file
  it modifies;
- files under `build/pkgbuilds/runink-zfs/` package upstream OpenZFS and follow
  **CDDL-1.0**. OpenZFS is shipped as a separate out-of-tree module package and must never
  be merged into the kernel source; see
  [docs/governance/ZFS-LICENSING.md](docs/governance/ZFS-LICENSING.md).

The per-path mapping is in `REUSE.toml`; see [docs/LICENSING.md](docs/LICENSING.md). Do not
add third-party code without recording where it came from, its license and the upstream
commit. The `archzfs` initcpio hook in `build/pkgbuilds/runink-zfs/` is the model to
follow.

## Making a change

1. Open an issue first for anything larger than a bug fix, so the design can be agreed
   before you spend time on it.
2. Branch from `main`. Keep each PR to one topic.
3. Run the local checks before you push:
   ```bash
   make lint           # shellcheck + closure-lint + sync linters (needs Arch tooling)
   ```
   CI runs `shellcheck`, `river lint installer-sync` and the hermetic lints
   (branding sync, no-python guard, k0s pin cross-check) on every PR, on GitHub-hosted runners only (a fork's PR runs once a maintainer approves
   it; see `.github/workflows/ci.yml` and
   [docs/governance/CI.md](docs/governance/CI.md)). The closure-lint is **not** run in
   CI, so run it yourself.
4. Shell: POSIX `#!/bin/sh` where possible, `set -eu`, and it must pass `shellcheck`.
5. Pin every upstream by checksum and, where one exists, by signature. New GitHub Actions
   steps must pin the action **by full commit SHA**, with the tag in a comment.
6. Write a commit message that says *why*, not only *what*. Reference the issue.
7. Add the tests the change needs (next section) and say in the PR how you tested it.
8. If users would notice the change, add a line under `## [Unreleased]` in
   [CHANGELOG.md](CHANGELOG.md), in the right section (**Security** for a vulnerability
   fix, with its advisory or CVE ID once public).

## Tests

**Policy: every new feature and every bug fix comes with an automated check that fails
without the change.** A reviewer asks for it, and a PR without one is not merged unless it
explains why no check is possible (a pure documentation change, for example). Pick the
cheapest check that would have caught the problem:

| What changed | Where the check goes | Runs in |
| --- | --- | --- |
| Go code (`installer/`, `guide/`, `validation/`) | a `_test.go` table-driven test next to the code; planner decisions get a fixture probe under `installer/internal/planner/testdata/` | CI jobs `installer-go`, `river-guide` (`go test -race`); `make test`, `make test-guide` |
| A shell script, pin or synced copy | a rule in the matching `scripts/lint-*.sh`, or a new lint wired into `scripts/ci-tier1.sh` | CI job `tier1` |
| The state of an installed node (file modes, services, sysctl, mounts) | an assertion in `tests/assert-golden.sh` | on a booted target, `make vmtest` |
| The k0s cluster on a node | a check in `tests/smoke-k0s.sh` | on a booted target |
| Boot, installer, ZFS or kernel | a Tier 2 VM test, or the manual `make vmtest` run described in the PR | [docs/governance/CI.md](docs/governance/CI.md) |

A check that can pass having examined nothing (an empty file list, a missing tool) must
fail instead. Security fixes follow the same rule; see [SECURITY.md](SECURITY.md).

## Review and merge

- Every PR needs approval from at least one maintainer who did not write it. See
  [GOVERNANCE.md](GOVERNANCE.md) for changes that need more than that: invariants,
  licensing, releases and governance.
- CI must be green and every commit signed off.
- Maintainers squash or rebase-merge. The DCO sign-offs of the original commits are kept
  in the squashed message.
- Maintainers commit and sign off as themselves, with the address they chose to make
  public (a GitHub no-reply address keeps a personal mailbox out of the history). A bot
  identity never authors or signs off a commit, including commits an automated tool
  prepared: the maintainer who publishes it signs it off.

## Reporting bugs

Open a GitHub issue with the Runink River version (`/etc/runink-os-version`), the hardware, what
you expected and what happened. Remove hostnames, IP addresses and any credentials from
logs before you paste them.
