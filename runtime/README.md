# runtime/ — the RIVER runtime (plan, not built yet)

**RIVER** (*Raft-Integrated Validated Event Runtime*) is Runink River's raft-based pipeline
runtime, run by Runink River Server. This directory is its future home. **Nothing here is built yet**
and nothing under `runtime/` is compiled into the server image today; this file records
the plan so the first code lands in the agreed shape.

## What it will be

1. **A Go library** that parses and validates the RIVER pipeline formats:
   - TOML `.dsl` — pipeline definitions;
   - Go `.contract` — typed data contracts between steps;
   - TOML `.herd` — groupings of pipelines/steps scheduled together;
   - `@step` functions — steps are plain `io.Reader` → `io.Writer` transforms;
   - golden tests — recorded input/expected-output pairs that gate a pipeline.
2. **`riverd`** — the service that places and runs those pipelines on the cluster.

## Building blocks

`riverd` needs three things it should not re-implement: a **raft log** for placement and
state (a job-placement entry kind), **cgroup v2 confinement** for per-step resources, and
a **lineage record** for every step input and output. Which open-source libraries provide
them is an open decision, tracked in `docs/governance/LF-AIDATA.md`: every dependency
of `riverd` must be available under an OSI-approved licence.

## Step confinement: namespaces + cgroups

Each pipeline step runs confined on two axes:

- **Namespaces — `river-sandbox`** (`/usr/local/bin/river-sandbox`, bubblewrap, already in
  the image). A step is `river-sandbox [--net] [--rw DIR]... -- <step binary>`: user, pid,
  ipc, uts, cgroup and net namespaces unshared, no capabilities, nested user namespaces
  disabled, cleared environment, read-only `/usr` and a minimal `/etc`, fresh tmpfs `/tmp`
  and HOME. Network is off unless the step's `.dsl` declares it (`--net`); the only writable
  paths are the step's own work dir (`--rw`). Its `io.Reader`/`io.Writer` contract maps to
  the sandboxed process's stdin/stdout, so a step never needs a shared filesystem to talk to
  its neighbours. The wrapper refuses binds that would expose `/etc/runink`, runner
  credentials or k0s state — riverd must not work around that by calling `bwrap` directly.
- **Resources — cgroup v2.** riverd places the sandboxed process in a per-step
  cgroup v2 leaf (CPU, memory, pids limits); bwrap itself does not set limits.
  (bwrap's `--unshare-cgroup` only hides the host cgroup tree from the step,
  it does not confine it.)

`riverd` does not grow its own sandbox code: if a step needs a mount or namespace option
the wrapper lacks, the change goes into `river-sandbox`, where the hardening review is.

## How it is served

An **internal** service, not a public endpoint: workloads on the cluster submit pipelines
to it over mutually authenticated (mTLS) connections.

## Persistence

`riverd` gets no bespoke database. Job records, placement and run history go into an
append-only record log on the cluster's object storage; SQL (with streaming replication)
only where it is genuinely needed. Records are encrypted before they reach any store, and
everything sits on ZFS datasets that inherit the pool's aes-256-gcm encryption root
(`../docs/ENCRYPTION.md`), so there are two layers: record encryption wherever a record
travels, and pool encryption for every byte on disk. Host-side config or state riverd
writes follows the shadow-grade rule: root-only 0600/0700, listed in `river-perms`.

## Boundaries

- It is a runtime. **Application logic never lives here** — pipelines, contracts and
  business rules belong to the workloads that submit them.
- The server image invariants in `../AGENTS.md` still hold: s6 (not systemd), ZFS root,
  an IPv6-only cluster network on a dual-stack host, no runtime fetch tooling. When `riverd` ships it is built off-node
  and delivered as a prebuilt artifact, like every other component; how it is packaged
  (host package vs k0s workload) is not decided yet.
