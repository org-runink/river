<!--
SPDX-FileCopyrightText: 2026 The RIVER Authors
SPDX-License-Identifier: MIT
-->

## What and why

<!-- What does this change, and why? Link the issue. -->

## How it was tested

<!-- Every feature and bug fix needs a check that fails without it (CONTRIBUTING.md#tests).
     Name it. For boot, installer, ZFS or kernel changes, say how you ran the VM test. -->

## Checklist

- [ ] Every commit is signed off (`git commit -s`, DCO).
- [ ] A test, lint rule or assertion covers the change, or the PR explains why none is possible.
- [ ] `sh scripts/ci-tier1.sh` passes locally; `make test` / `make test-guide` if Go changed.
- [ ] No AGENTS.md invariant is broken (or a TSC vote is requested).
- [ ] `CHANGELOG.md` has a line under `## [Unreleased]` if users would notice the change.
- [ ] No secret, real address, internal hostname or personal data is added.
