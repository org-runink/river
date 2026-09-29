---
title: Contributing
weight: 12
sidebar:
  open: true
---
<!--
SPDX-FileCopyrightText: 2026 The RIVER Authors
SPDX-License-Identifier: MIT
-->

Contributions are welcome: issues, reviews, documentation and code. Everyone taking part
follows the [Code of Conduct](https://github.com/org-runink/river/blob/main/CODE_OF_CONDUCT.md).
Security problems go through the [private process]({{< relref "/docs/security/reporting" >}}),
never a public issue.

## Before you start

- **Read {{< repo "AGENTS.md" >}}.** It lists the invariants every change must keep (s6 and
  never systemd, exactly one kernel, an encrypted ZFS root, no secrets in the image, every
  upstream pinned, ...). It applies equally to people and to AI coding agents working for
  them. A change that breaks an invariant is closed, however good the code.
- **Open an issue first** for anything larger than a bug fix, so the design is agreed before
  you spend time on it.
- Keep each pull request to one topic.

## Sign off every commit (DCO)

Runink River uses the [Developer Certificate of Origin 1.1](https://developercertificate.org/),
not a CLA. Signing off certifies that you wrote the change or otherwise have the right to
submit it under the project's licence:

```bash
git commit -s        # adds: Signed-off-by: Your Name <you@example.org>
```

The DCO check fails a pull request with any unsigned commit. To fix one:

```bash
git rebase --signoff origin/main
git push --force-with-lease
```

You may use AI coding tools, but **you** sign off, and your sign-off covers the whole change.
Record material assistance with a `Co-Authored-By:` or `Assisted-by:` trailer.

## Licensing of new files

Every file declares its licence in an SPDX header, or in `REUSE.toml` if it cannot carry a
comment:

```sh
# SPDX-FileCopyrightText: 2026 The RIVER Authors
# SPDX-License-Identifier: MIT
```

MIT is the default. The kernel packaging tree is GPL-2.0-only and the OpenZFS packaging tree
CDDL-1.0. The Runink River mark and logos are trademarks and are not licensed; see
{{< repo "docs/LICENSING.md" >}}.

## Checks to run

```bash
sh scripts/ci-tier1.sh   # shellcheck and the hermetic lints, in a rootless container
make lint                # adds the package-closure lint
make test                # Go tests of the hardware probe and planner
make test-guide          # Go tests of river-guide
make docs                # build this website (Hugo)
```

Shell scripts are POSIX `sh` where possible, `set -eu`, and clean under `shellcheck`. There is
**no Python** anywhere in the build path or the image. New GitHub Actions steps pin the action
by full commit SHA.

**Every behaviour change comes with a check that fails without it**: a Go test, a lint rule,
an assertion in `tests/assert-golden.sh` on a booted system, or a VM test. Say in the pull
request how you tested. The full guide: {{< repo "CONTRIBUTING.md" >}}.

## This website

The pages you are reading are in {{< repo "website/" >}}: Hugo, with the Hextra theme vendored
so the build needs no network. Every page has an "Edit this page on GitHub" link at the bottom.
Preview locally with `make docs-serve`.
