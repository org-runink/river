---
title: river-sandbox
weight: 5
---
<!--
SPDX-FileCopyrightText: 2026 The RIVER Authors
SPDX-License-Identifier: MIT
-->

`river-sandbox` runs a command confined by [bubblewrap](https://github.com/containers/bubblewrap)
(`bwrap`). Use it for code you did not write and do not fully trust: a build script from a
pull request, a tool a coding agent produced, a dependency's install hook. It is invariant 8
of the project: anything the machine runs on behalf of someone it does not trust goes through
it. The script is {{< repo "iso-profiles/river/root-overlay/usr/local/bin/river-sandbox" >}}.

## Usage

```bash
river-sandbox [--net] [--rw DIR]... -- cmd [args...]
```

- `--rw DIR` binds `DIR` read-write at the same path. Repeat it for more directories.
  Symbolic links are resolved first, so a link cannot smuggle a protected tree past the checks.
- `--net` keeps the network (and makes DNS resolution available). **Without it there is no
  network at all.**

## Examples

```bash
cd ~/src/some-project

river-sandbox --rw "$PWD" -- go build ./...          # build the checkout, no network
river-sandbox --rw "$PWD" -- make test               # run its tests, no network
river-sandbox --net --rw "$PWD" -- go mod download   # fetch dependencies, with network
river-sandbox --rw "$PWD" --rw ~/.cache/go-build -- go test ./...   # keep a build cache
```

A good pattern is two steps: fetch with `--net` first, then build and test **without** it, so
the code under test cannot reach the network at all.

Inside, `HOME` is a fresh, empty directory (`/home/sandbox`), so your shell configuration,
Git credentials and caches are not there unless you bind them. Bind a **project directory**,
never your whole home: whatever you bind, the confined code can read and change. (For the
`runink` account, the installer's default administrator, a `--rw` that contains its `~/.ssh`
is refused outright.)

## What the command gets

- Every namespace unshared (user, IPC, PID, network, UTS, cgroup), with the hostname
  `river-sandbox`, **no capabilities** (`--cap-drop ALL`) and **no nested user namespaces**
  (`--disable-userns`), so the confined code cannot build its own escape hatch.
- It dies with its parent and runs in a new session, so it cannot type into your terminal.
- An empty environment apart from `PATH=/usr/local/bin:/usr/bin`, `HOME`, `LANG=C.UTF-8` and
  `TMPDIR=/tmp`.
- `/usr` read-only (and the `/bin`, `/sbin`, `/lib`, `/lib64` links into it), a fresh
  `/tmp` and `HOME`, a new `/proc` and a minimal `/dev`.
- A read-only allow-list of `/etc`: `passwd`, `group`, `nsswitch.conf`, `hosts`, `host.conf`,
  `ld.so.cache`, `ld.so.conf`, `localtime`, `ssl`, `ca-certificates`, and, with `--net`,
  `resolv.conf`. Nothing else under `/etc` is visible: no `shadow`, no `sudoers`, no SSH host
  keys, no NetworkManager connections.

## What it never gets

These are never reachable from inside, **even when you ask for them with `--rw`**: a `--rw`
that is inside, equal to or above one of them is refused, not trimmed.

- `/etc/runink`, the machine's configuration and secrets;
- `/root`;
- the `runink` user's `~/.ssh` and its secret directories;
- the state directories a downstream image may use for a cluster or a CI runner.

```text
$ river-sandbox --rw /etc -- ls /etc
river-sandbox: --rw /etc: overlaps protected /etc/runink
```

A refused call exits with status 2 and runs nothing.

The deny-list is an invariant of the project; a change that widens it needs a vote of the
technical steering committee. The other messages are listed on
[Troubleshooting]({{< relref "/docs/troubleshooting#river-sandbox" >}}).

## What it is not

`river-sandbox` confines a process; it is not a virtual machine. The kernel is shared, and
unprivileged user namespaces stay enabled on the host because bubblewrap needs them, which the
project's assurance case lists as a known widening of the kernel's attack surface for local
users. For code you consider hostile, use a VM.

Found a way out? That is a security issue: please [report it privately]({{< relref "/docs/security/reporting" >}}).
