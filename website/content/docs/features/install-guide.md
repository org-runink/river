---
title: Offline install guide
weight: 7
---
<!--
SPDX-FileCopyrightText: 2026 The RIVER Authors
SPDX-License-Identifier: MIT
-->

`river-guide` is an install guide that runs **on the live medium, offline**. It walks you
through the install steps, answers questions about them **from the guide itself, with
citations**, and runs the read-only checks the guide names. It never runs a destructive
command.

{{< callout type="info" >}}
**Status.** `river-guide` is in the tree (`guide/`), but the Runink River workstation medium
does **not** ship it: its local model is about 1.1 GB and it has only a console front end,
next to a graphical installer that already walks you through every step. An image that
ships it starts it on a text console of the live medium, with its model pinned in
`guide/model.lock`.
{{< /callout >}}

## Grounded answers, or none

1. **Retrieval.** A keyword index (BM25) over the guide picks the four most relevant
   sections. It needs no model, so it works on any machine.
2. **Model mode.** A small local model, served on the machine itself, sees only those
   excerpts and is told to answer from them, cite them, or say `NOT IN GUIDE`.
3. **Validation.** `river-guide` checks the answer itself instead of trusting the model:
   - every command in the answer must appear in a retrieved excerpt; a single invented
     command and the whole answer is withheld and replaced by the excerpts;
   - at least 70 % of the answer's content words must come from the excerpts or the
     question;
   - citations of sections that were not retrieved are dropped.
4. **Degraded mode.** With no model (or too little RAM for it), the answer is the matching
   guide sections, quoted.

Model output is text for a person to read. No code path executes it.

## Steps it can and cannot run

| Kind | What river-guide does |
|---|---|
| `info` | shows it; you mark it done |
| `check`, `action` | runs the step's command after you approve it: an argument list, never a shell |
| `destructive` | **never runs it**: shows the command, which you run yourself on another console |

The built-in steps start with the network (a read-only report of what the live session set
up), then firmware, hardware probe, plan, recovery key, install (destructive), reboot, first
boot and verify.

## Optional: follow an install remotely

With your consent at the console, `river-guide` can mirror the install to one GitHub issue,
so someone at another site can follow and answer questions by commenting `@river_install`.
It signs in with the OAuth device flow (no credential on the medium; the token lives in
memory only) and polls over outbound HTTPS, with no listening port. It is off unless you
turn it on.

The guide is removed from the installed system. Design and threat model:
{{< repo "docs/INSTALL-GUIDE-AGENT.md" >}}.
