<!--
SPDX-FileCopyrightText: 2026 The RIVER Authors
SPDX-License-Identifier: CC-BY-4.0
-->

# Runink River Server: hardware-adaptive installer

The Runink River Server ISO is installed on hardware nobody has seen in advance. So before it asks a
single question, the installer measures the machine and decides what it can run:

1. **`river-hwprobe`** reads `/proc`, `/sys` and the udev database and writes a JSON inventory:
   CPU level and topology, RAM, GPUs, disks, NICs, TPM and firmware. It is read-only. It never
   opens a block device and needs no root.
2. **`river-plan`** is a pure function of that inventory and a models manifest. It returns the
   **install plan**: which model tiers run, and at what context length, KV budget and thread
   count; the RAM budget; the ZFS pool layout; the swap policy. A machine below the minimums
   gets a **REFUSED** plan, with the reason.
3. The installer **shows the plan**. You confirm every disk it will erase **by typing its
   serial**. The disks are re-checked against a fresh probe right before anything is written.
   The plan is then saved on the node as **`/etc/runink/install-plan.json`** (root, 0600).

A downstream platform reads `install-plan.json` to size its inference deployment on each node.
The plan describes capability tiers. It does not name any application.

Code: `installer/hwprobe`, `installer/plan` and `installer/internal/{hw,planner}`. It is Go,
standard library only, MIT-licensed, and ships in the `runink-installer` package as
`/usr/local/bin/river-hwprobe` and `/usr/local/bin/river-plan`. Both binaries are built for
`GOAMD64=v1`, so they run on the CPUs they refuse. Tests: `make test`, and the `installer-go`
CI job.

## Minimums

A plan is **refused** below any of these. `--lab` waives them for VMs and test rigs. The waiver
is recorded in the plan (`"lab": true`).

| What | Minimum | Why |
|---|---|---|
| Firmware | UEFI | The ESP is `/boot`, and GRUB installs to the removable EFI path. |
| CPU | x86-64-v3 (AVX2, FMA, F16C, BMI1/2, MOVBE) | The image and the CPU inference runtime are built for v3. |
| Physical cores | 4 | 2 are reserved for the node and k0s. |
| RAM (MemTotal) | 15360 MiB ("16 GB" installed) | A hard floor. The effective floor is higher: the mandatory tiers must also fit (below). |
| Mandatory tiers | embedding, stt and tts must all fit at their `ctx_min` (a tier marked `pending` in the manifest is skipped) | Every node serves them. Indexes depend on the embedding tier. |
| Target disk | at least one eligible disk of 64 GiB or more | Eligible means not the boot medium, not USB, not removable, not read-only, and not in use (mounted, swap, or held by dm/md). |
| Pool capacity | about 200 GiB usable | Room for the system, the baked models, the image store and state. |

With the fixture manifest in `installer/internal/planner/testdata/models.tiers`, the
mandatory tiers alone need about 17 GiB of MemTotal. About 25 GiB is needed before a fallback
`general` model fits.

## Profiles

`river-plan --profile server|workstation` picks the minimums. `server` is the default and
is everything above. `workstation` is the Runink River Workstation, a desktop that runs no
workloads. Its `runink-install` passes `--profile workstation --no-models` (through
`RUNINK_PLAN_PROFILE=workstation` in `hwplan.sh`), so the server minimums never refuse a
desktop.

| What | Server | Workstation |
|---|---|---|
| Firmware | UEFI | UEFI |
| CPU | x86-64-v3 | x86-64-v3 |
| Physical cores | 4 | 2 |
| RAM (MemTotal) | 15360 MiB ("16 GB") | 7168 MiB ("8 GB") |
| Model tiers | planned; the mandatory ones must fit | not planned (`--manifest` is refused; a manifest passed to the library is ignored) |
| RAM reserves | os, k0s, platform, ZFS ARC | os and ZFS ARC only (no k0s, no platform) |
| Target disk | one eligible disk of 64 GiB or more | the same |
| Pool capacity | about 200 GiB usable | no minimum beyond the disk (a single-disk pool is fine) |

The layout rules (below) are the same for both profiles, and every disk the plan erases is
confirmed by serial either way. A workstation plan has no tiers and no warning about them,
so its verdict is `ok` unless a minimum is missed; it records `"profile": {"iso":
"river"}` (a server plan records `"server"`). With `workstation-25g` it is `ok` (single NVMe); `edge-16g`, which
the server refuses, is also `ok` as a workstation. `TestWorkstationProfile` pins both and
each desktop minimum.

## Decision table

### CPU

| Input | Decision |
|---|---|
| physical cores `P` | reserved = `clamp(P/8, 2, 8)`; inference threads `I = P - reserved` |
| general, coder, vision | `RAYON_NUM_THREADS = I` |
| stt | `clamp(I/2, 2, 8)` |
| embedding | `clamp(I/2, 1, 4)` |
| tts | `clamp(I, 1, 2)` |
| a manifest `threads_max` | caps the tier |
| AVX-512, AMX | noted, not used: the shipped inference build targets v3 |
| more than one socket | noted: this plan does not NUMA-pin threads |

### RAM budget

```
total (MemTotal)
  - os         1024 MiB   kernel, s6, udev, sshd, NetworkManager, ZFS userland
  - k0s        2048 MiB   controller + worker, containerd, CNI, cluster DNS
  - platform   6144 MiB   the downstream platform's non-inference services
  - zfs-arc    clamp(total/16, 1 GiB, 16 GiB)   recommended zfs_arc_max
  - headroom   max(1 GiB, 10% of total)          never allocated
  = available for model tiers
```

A manifest may override the three fixed reserves (`reserve=os|k0s|platform mib=N`). Each
model tier costs `resident_mib + kv_mib_per_1k × context / 1024`. The plan's
`memory.budget` table lists every line. The planner guarantees that reserves + tiers +
headroom ≤ total, and the tests check it.

### Model tiers

Tiers are generic capability roles: `embedding`, `stt`, `tts` (mandatory) and `general`,
`coder`, `vision` (optional).

1. **Placement at minimum context.** Mandatory tiers are placed first, then the optional tiers
   in the order general → coder → vision. Each tier takes the best-ranked variant that fits at
   its `ctx_min`. If rank 1 does not fit, the next rank is tried. **A fallback is reported**:
   the tier is `degraded`, and its `reasons` list the variant that did not fit, the MiB it
   needed, the MiB that was left, and the variant chosen instead.
2. **Context growth.** In the same order, each placed tier grows from `ctx_min` toward
   `ctx_max` in 4096-token steps while RAM remains. A tier left below `ctx_max` is `degraded`.
3. **Verdict.**
   - A mandatory tier that does not fit **refuses** the install.
   - An optional tier that does not fit is `disabled`, with its reasons.
   - A tier the manifest marks `pending=<tier>` is `pending`: its model is being re-selected,
     so nothing is placed or budgeted for it and it never refuses the install, even when it is
     mandatory. The plan carries a warning naming it.
   - The verdict is `ok` when every tier is `enabled` at rank 1 and `ctx_max`. Otherwise it is
     `degraded`.

### ZFS layout

The pool is always one pool. Its root is the aes-256-gcm encryption root, and every dataset
inherits it (docs/ENCRYPTION.md).

| Eligible disks (≥ 64 GiB) | Layout |
|---|---|
| 1 | `single`, with a warning (no redundancy) |
| 2 | `mirror` |
| 3 | `raidz1` |
| 4-5 flash | `raidz1` |
| 4-5 HDD | `raidz2` (HDD rebuilds are long) |
| 6-12 | `raidz2` |
| more than 12 | equal-width `raidz2` vdevs, `ceil(n/12)` of them; leftover disks are unused |
| ≥ 2 HDD **and** ≥ 2 flash | data on the HDDs; the two best flash devices (NVMe first, then the largest) form a **mirrored special vdev**; extra flash is unused |
| HDDs + 1 flash | data on the HDDs; the single flash device is unused (a lone special vdev is a single point of failure for the pool) |
| more flash than HDD | data on the flash; the HDDs are unused (a vdev runs at the speed of its slowest disk) |
| mixed sizes | the largest group of disks within 10% of each other; the rest are unused |

The boot disk is the first data disk by name. Every data disk gets the same GPT: a 1 GiB ESP
and a ZFS partition. Only the boot disk's ESP is mounted at `/boot` and receives GRUB. The
others are formatted `RIVER_ESP<n>`. Special-vdev disks are given to ZFS whole. Every disk the
plan will erase is listed in `storage.disks` with its `confirm_id`. Every disk it leaves alone
is listed in `storage.unused`, with the reason.

### Swap, ARC, GPU, TPM, network

| Item | Decision |
|---|---|
| Swap | Never on disk or on ZFS. A zstd zram device: RAM/2 below 32 GiB, else RAM/4, capped at 16 GiB. Applied: `30-target-config` writes it to `/etc/runink/zram.conf`, which `runink-zram.sh` reads at every boot. |
| ZFS ARC | `zfs_arc_max = clamp(RAM/16, 1 GiB, 16 GiB)`, counted in the RAM budget. Applied: `30-target-config` writes `options zfs zfs_arc_max=<bytes>` to `/etc/modprobe.d/zfs.conf`, before `40-boot-grub-zfs` builds the initramfs that loads the module. |
| GPU | The inference build is CPU-only (`accelerator.mode = cpu`). Discrete GPUs are reported, and tiers that would fit in 90% of a known VRAM are listed as `gpu_offload_candidates`. VRAM is readable from sysfs only on amdgpu. |
| TPM 2.0 | Noted as eligible for a future unattended-unlock key provider. None ships yet. |
| Network | Warns when no physical NIC has link, or none has a global IPv6 address. The host may be dual-stack, but the k0s cluster network is IPv6-only and takes its node address from the host's IPv6. |

#### Where the ARC and zram sizes are applied

`river-plan --env` prints `RUNINK_ZFS_ARC_MAX` (bytes) and `RUNINK_ZRAM_SIZE` (`<zram_mib>M`).
Both drivers pass them to the steps: `runink-install` exports them, and the graphical
installer passes every `RUNINK_*` variable of the resolved plan. Step `30-target-config`
applies them through `installer/lib/memtune.sh`:

| Value | Written to | Read by |
|---|---|---|
| `zfs.arc_max_bytes` | `/etc/modprobe.d/zfs.conf`: `options zfs zfs_arc_max=<bytes>`. Other `options zfs` settings and other lines in the file are kept; only an earlier `zfs_arc_max` is replaced. | The `zfs` module. On a ZFS root it loads in the initramfs, and the `zfs` mkinitcpio hook (`runink-zfs`) copies this file into it. Step 30 runs before `40-boot-grub-zfs` builds the initramfs, and every later `mkinitcpio` (kernel or ZFS upgrade) copies it again. |
| `swap.zram_mib` | `/etc/runink/zram.conf`: `RUNINK_ZRAM_SIZE=<mib>M`, root 0600 like the rest of `/etc/runink`. | `runink-zram.sh`, at every boot from `rc.local`. Its order: `RUNINK_ZRAM_SIZE` in the environment (an operator's override), then `zram.conf`, then the image default `RUNINK_ZRAM_DEFAULT` (`ram` on Runink River, which also covers the live medium), then `16G`. |

Without a plan (a driver that resolves none), the ARC falls back to the same rule,
`clamp(MemTotal/16, 1 GiB, 16 GiB)`, computed from `/proc/meminfo` of the machine being
installed. OpenZFS's own default takes most of RAM, which suits a file server and not a
desktop. The zram size has no fallback: no `zram.conf` is written, and `runink-zram.sh` keeps
the image default. A cloud image (`RUNINK_CLOUD`) is built in a VM that is not the instance,
so step 30 sizes neither. `tests/assert-golden.sh` checks the running value of `zfs_arc_max`
and `zram.conf` against `/etc/runink/install-plan.json`.

## Fixture outcomes

These are the table-driven tests in `installer/internal/planner/plan_test.go`. They use the
fixture manifest.

| Probe fixture | Verdict | Tiers | Layout |
|---|---|---|---|
| `workstation-25g` (8 cores, 25 GiB, 1 NVMe, v4) | degraded | general falls back to its rank-2 variant at 16k context; coder and vision disabled | single |
| `server-64g` (16 cores, 64 GB, 2 NVMe) | degraded | general and coder rank 1 at 20k of 32k context; vision falls back to rank 2 | mirror |
| `server-256g-avx512` (2 sockets, 64 cores, AMX, 8 HDD + 2 NVMe + 1 SSD) | ok | every tier at rank 1 and full context | raidz2 of 8 HDD + special mirror of 2 NVMe; the SATA SSD is unused |
| `edge-16g` (4 cores, 16 GB, 1 SSD) | **refused** | stt (mandatory) does not fit | single |
| `gpu-128g` (24 cores, 128 GB, NVIDIA + AMD discrete, 3 SSD) | ok | every tier at rank 1; accelerator stays `cpu` | raidz1 |

With `--profile workstation`, `workstation-25g` and `edge-16g` are both `ok` (no tiers, single).

## The models manifest: `models.tiers`

The planner does not choose models. It reads their sizes from `models.tiers`. The image ships
that file at `/usr/local/share/runink/models.tiers`, packaged by the `runink-installer`
PKGBUILD next to `models.lock` when that exists. A downstream payload that serves models
stages both in `$OUT_DIR` (it knows its model set); otherwise they are taken from the
repository root when present. One record per line,
whitespace-separated `key=value` fields, `#` comments. Unknown keys are an error.

```
reserve=<os|k0s|platform> mib=<n>

tier=<general|coder|vision|stt|embedding|tts> variant=<id> rank=<n>
     resident_mib=<n> kv_mib_per_1k=<n> ctx_min=<n> ctx_max=<n>
     [threads_max=<n>] [lock_role=<role>] [lock_repo=<org/name>]

pending=<general|coder|vision|stt|embedding|tts>
```

| Field | Meaning |
|---|---|
| `rank` | 1 is preferred. Higher ranks are the fallbacks, tried in order. |
| `resident_mib` | Resident memory while serving, **excluding** the KV cache: weights as loaded (after any load-time quantization) plus the runtime's own working memory. |
| `kv_mib_per_1k` | KV-cache MiB per 1024 tokens of context at the serving dtype. For a standard transformer: `2 × layers × kv_heads × head_dim × bytes / 1024`. |
| `ctx_min`, `ctx_max` | The smallest useful context and the most worth reserving. The tier is never planned below `ctx_min`. |
| `lock_repo`, `lock_role` | Cross-reference to a `models.lock` row (the 8-column file: role, repo, revision, file, size, sha256, license, dest). With `--lock`, every reference must exist in the lock. A variant that omits `resident_mib` gets the sum of its locked file sizes + 10%. That is a floor, not a measurement: a runtime that quantizes at load time is smaller, and one with a large vision or audio encoder is bigger. The lock's roles `voice` and `embed` map to the tiers `stt` and `embedding`. |

`pending=<tier>` says that no model serves the tier yet (its model is being re-selected). A
pending tier must not also have a `tier=` row, and a mandatory tier needs one or the other.

If the medium has no `models.tiers`, the installer plans hardware and storage only. The plan
then says `"no models manifest"`, its verdict is `degraded`, and `tiers` is empty.

## Operator flows

**Interactive (`sudo runink-install`):** probe, then plan, then the plan screen. A refused
plan stops here. Next you type the serial of each disk to be erased (three misses abort), then
answer the pool, hostname, BE, enrollment and key questions, then type `YES`. Step
`05-hwplan-verify` then re-probes and re-resolves the plan by serial, step `10-disk-zfs`
builds the layout, and step `75-install-plan` writes `/etc/runink/install-plan.json`.

**Scripted (datacenter):**

```sh
river-hwprobe --json > probe.json
river-plan --probe probe.json --manifest /usr/local/share/runink/models.tiers --json > plan.json
river-plan --plan-file plan.json --list-disks          # review what will be erased
sudo runink-autoinstall --plan-file plan.json \
     --yes-i-have-checked-serial=<serial> [--yes-i-have-checked-serial=<serial> ...] \
     [<disk>] [pool] [be] [hostname]
```

Every disk the plan erases must be confirmed, and nothing else may be. Disks are matched by
serial, so a plan made before a reboot still finds the right disks under new kernel names.
The probe reads the serial where each bus keeps it: udev's `ID_SERIAL_SHORT` (ATA, SCSI,
NVMe, USB), `/sys/block/<disk>/serial` (virtio-blk, as on cloud and VM servers),
`device/serial` (the NVMe controller), SCSI VPD page 0x80, and last udev's `ID_SERIAL` for
virtio-blk only. A disk with neither a serial nor a WWN (a VM disk given no serial) is
confirmed with the `NOSERIAL-<name>-<size>G` id that the probe prints. `--lab` exists for VMs. The default pool
name is `zriver`.

**One line from anywhere:**
`curl -fsSL https://raw.githubusercontent.com/org-runink/river/main/install.sh | sh`. On a
Linux host it downloads and verifies the ISO. Inside the live environment it runs the probe
and the plan, then hands over to `runink-install`. See the header of `install.sh`, and `KEYS`.

## Contract

Other tools on the install medium call these binaries. The CLI, the exit codes and the two
JSON schemas are stable. Fields may be **added** within a schema version. Renaming a field,
retyping one or changing its meaning bumps the version.

```
river-hwprobe --json                 -> river.hwprobe/v1 on stdout
river-hwprobe                        -> human summary
river-hwprobe --root DIR [--json]    -> read /proc, /sys, /run/udev, /dev/disk under DIR

river-plan --probe P (--manifest M [--lock L] | --no-models) [--lab] [--profile server|workstation] --json
                                     -> river.install-plan/v1 on stdout
river-plan --probe P (...)           -> the same plan, human-readable
river-plan --plan-file F [--json]    -> re-render a saved plan
river-plan --plan-file F --list-disks
                                     -> one line per disk to erase, boot disk first:
                                        name TAB confirm_id TAB GiB TAB kind TAB role(boot|data|special) TAB model
river-plan (--plan-file F | --manifest M ...) --probe FRESH --env --confirm ID [--confirm ID ...]
                                     -> sh assignments: RUNINK_PLAN_VERDICT RUNINK_DISK
                                        RUNINK_POOL_TOPOLOGY RUNINK_POOL_DISKS
                                        RUNINK_POOL_VDEV_WIDTH RUNINK_SPECIAL_DISKS
                                        RUNINK_ZFS_ARC_MAX RUNINK_ZRAM_SIZE
                                        RUNINK_INFERENCE_THREADS
```

`river-plan` exit codes:

| Code | Meaning |
|---|---|
| 0 | Plan made (`ok` or `degraded`), or resolved. |
| 1 | Error: unreadable input or wrong schema. |
| 2 | Usage error. |
| 3 | Verdict `refused`. The plan is still printed. |
| 4 | `--env` could not resolve: a disk is missing, ineligible, the boot medium or matched by more than one disk; or a confirmation is missing or extra. |

`river-hwprobe` exits 0 (ok), 1 (`/proc/cpuinfo` or `/proc/meminfo` unreadable) or 2 (usage).

The load-bearing fields:

- **Probe:** `cpu.psabi_level`, `cpu.physical_cores`, `memory.total_bytes`,
  `disks[].{name, confirm_id, eligible, boot_media, ineligible_reasons, size_bytes, kind}`,
  `firmware.uefi`, `tpm.present`.
- **Plan:** `verdict`, `refusals[]`, `tiers[].{tier, status, variant, context_tokens,
  kv_budget_mib, threads, env}`, `memory.budget[]`, `storage.{layout, disks[], data_vdevs[],
  special_vdev, unused[]}`, `zfs.arc_max_bytes`, `swap.zram_mib`.

Both schemas are the Go types in `installer/internal/hw/types.go` and
`installer/internal/planner/plan.go`.

## Not done yet

- **Secondary ESPs are not kept in sync.** They are formatted but empty. If the boot disk of a
  mirror or raidz dies, the pool survives, but the node needs its ESP recreated before it can
  boot.
- **No GPU offload.** The inference build is CPU-only, and NVIDIA VRAM is not readable without
  the vendor tool.
- **The shipping `models.tiers` covers only the pinned tiers.** `models.tiers` at the repository
  root lists stt, tts, coder and vision, measured against `models.lock`, and marks embedding
  `pending`: mistral.rs is the only inference engine, and the embedding model is being
  re-selected for one it serves (768 dimensions, to match the vector indexes). Until then no
  node budgets RAM for embeddings. general is the coder process.
- **`10-disk-zfs.sh`'s multi-disk path has not run on real hardware or in a VM.** It is
  shellchecked, and the drivers were exercised end to end with stub steps.
