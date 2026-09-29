<!--
SPDX-FileCopyrightText: 2026 The RIVER Authors
SPDX-License-Identifier: MIT
-->

# Scope of Runink River's own base

> **Status: adopted scope for the planned own base** ([OWN-BASE.md](OWN-BASE.md) has the
> build plan and phases). Today's images are still assembled with the transitional Artix
> tooling.

Runink River's base is a small, **immutable** system image. It contains only what every node
needs, and it is versioned and released on its own. Everything else is a workload.

## In the immutable base

| Component | Role |
| --- | --- |
| Zen kernel (`linux-runink`, pinned 7.2.x stable) | The one kernel |
| OpenZFS (pinned stable, separate out-of-tree module package) | Root filesystem, boot environments, snapshots, replication |
| ZFS native encryption | Every dataset under one aes-256-gcm encryption root |
| `river-sandbox` | bubblewrap confinement for any code run on behalf of others |
| Core userland utilities | The minimal GNU/Linux userland the node itself needs (s6 init, networking, rsync/SSH, nftables) |
| `objectd` | The node's object-storage service |
| appfs mount handlers | Mount and unmount each workload's isolated, encrypted appfs root |

The init system is **s6** (see [AGENTS.md](../AGENTS.md)); systemd is not part of the base.

## Outside the base: workloads

- Workloads run **isolated**, each on its own **appfs root** (a separate encrypted ZFS
  filesystem with its own mount and credentials), under k0s and `river-sandbox`.
- Workloads are **release-decoupled** from the base: a workload ships, upgrades and rolls
  back on its own schedule, and a base upgrade (kernel, ZFS) does not rebuild workloads.
  The interface between them is the appfs mount, the k0s API and the documented enrollment
  hooks ([BUILD.md](BUILD.md#downstream-payloads)).
- Runink River names and ships no specific workload. Downstream platforms deliver theirs as
  payloads outside this repository.

## Rules that follow

- A change that adds a component to the base must argue why every node needs it; the
  default answer is "make it a workload".
- The base never depends on a workload being present to boot, unlock its pool, or reach
  the network.
