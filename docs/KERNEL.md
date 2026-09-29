<!--
SPDX-FileCopyrightText: 2026 The RIVER Authors
SPDX-License-Identifier: MIT
-->

# The Runink River kernel (`linux-runink`)

`linux-runink` is the one kernel of Runink River Server and Runink River Workstation
([AGENTS.md](../AGENTS.md), invariant 2). It is also published for Arch Linux in the AUR as a
community package. It is **not an official Arch Linux package** and is not endorsed by Arch
Linux.

It is a kernel for **data pipelines and analytics workloads**: long-running, throughput-bound
jobs such as columnar scans, joins and aggregations, bulk ingest and ETL, on servers where no
one is waiting at a desktop for a frame to render. This page explains what it is built from,
what its configuration changes and why, how to install it on Arch Linux, and how to measure it.

> **No performance claims yet.** Every change below is chosen for throughput-bound work,
> with the upstream documentation that motivates it. Whether it helps on a given machine,
> and by how much, is **to be measured** with the harness in
> [`bench/analytics/`](../bench/analytics/) ([Benchmarking](#benchmarking)). No measured
> comparison with linux-zen or linux-lts exists yet, and this page will not quote a number
> until one does.

## What it is built from

| Part | Pin | Verified by |
|---|---|---|
| Kernel base | kernel.org `linux-7.2.7.tar.xz` | `.tar.sign`, Greg Kroah-Hartman (or Linus Torvalds) |
| zen patch | `linux-v7.2.7-zen1.patch.zst` (the zen-kernel `v7.2.7-zen1` release asset) | `.sig`, Jan Alexander Steffens (heftig), zen's maintainer |
| Configuration | `config.base` + `config.delta`, floor `config.require` | sha256 pins in both PKGBUILDs; `prepare()` fails below the floor |
| OpenZFS (separate package) | `zfs-2.4.4.tar.gz` | `.asc`, Tony Hutter |

The build is the one Arch's `linux-zen` uses: the signed kernel.org stable tarball plus the
signed zen patch, so the kernel.org signature covers the whole base and the zen delta has its
maintainer's signature. The same kernel is built by two PKGBUILDs:

- `build/pkgbuilds/runink-kernel/` for the Runink River images (see its
  [README](../build/pkgbuilds/runink-kernel/README.md) for pins, guards and the staged
  module-signing rollout);
- `packaging/aur/linux-runink/` for the AUR, which fetches the three config files from a
  pinned tag of this repository ([Publishing to the AUR](../packaging/aur/PUBLISHING.md)).

`river lint aur-sync` (Tier 1 CI) fails if the two ever pin different sources.

### Why zen as the base

- **zen tracks the current stable series** (7.2.x), with a signed patch per release. Runink
  River chose the current stable series over an LTS for hardware support and current
  subsystems (sched_ext, io_uring, MGLRU, BPF); the cost is a rebase every stable series
  ([ZFS pacing](#zfs-pacing-policy)).
- **zen is packaged and reviewed in Arch** (`linux-zen`), so the build recipe, the signing
  keys and the base configuration have an upstream that other people also exercise.
- zen carries features Runink River uses: `USER_NS_UNPRIVILEGED` (the switch bubblewrap's
  unprivileged sandbox depends on), and BBRv3 as a module.

What zen is *for* is the problem: its configuration is tuned for **desktop latency**
(1000 Hz tick, full preemption, THP always, and the `ZEN_INTERACTIVE` bundle). That is the
wrong trade-off for throughput-bound pipelines, so the analytics profile below reverses it.

## Analytics profile

### Decision: one kernel, the analytics profile is the default

The options were (a) make the analytics profile the Runink River default, or (b) ship two
flavours (a latency kernel and a throughput kernel). **The profile is the default (a).**

- Runink River Server is a server OS for data pipelines; throughput is its job.
- Invariant 2 of [AGENTS.md](../AGENTS.md) is *exactly one kernel*. Two flavours would double
  the build, the signing, the OpenZFS module matrix and the boot testing, and would need a
  TSC vote.
- The latency case does not need a second kernel. The profile keeps `PREEMPT_DYNAMIC`, so the
  preemption model is a boot parameter: `preempt=full` restores full preemption on the same
  binary. Runink River does exactly that
  (`iso-profiles/river/root-overlay/etc/default/grub.d/20-runink-preempt.cfg`).
  The other runtime-switchable knobs (THP mode, congestion control, qdisc, autogroup,
  power-efficient workqueues) are listed with their switches below.

### What changes, and why

These are the only differences between the resolved configuration before and after the
profile (`config.base` + `config.delta`, after `make olddefconfig`); `config.require` asserts
each one, and the build fails if a rebase loses it.

| Area | zen (before) | linux-runink | Why | Source | Runtime switch |
|---|---|---|---|---|---|
| Interactivity bundle | `ZEN_INTERACTIVE=y` | `n` | zen's bundle "tunes the kernel for responsiveness at the cost of throughput and power usage". Off restores upstream defaults: block elevator `mq-deadline` (single-queue) / `none` (multi-queue NVMe) instead of `bfq` / `kyber`; EEVDF base slice 0.7 ms instead of 0.4 ms and migration cost 0.5 ms instead of 0.3 ms (fewer preemptions and migrations); `nr_migrate` 32 instead of 8; THP defrag `madvise` instead of kswapd background reclaim; unevictable compaction on; swap-in readahead back to upstream; **split-lock mitigation back on** (zen turns it off) | `ZEN_INTERACTIVE` Kconfig help in the zen patch (`init/Kconfig`), which lists exactly these values | elevator per disk in `/sys/block/*/queue/scheduler`; scheduler knobs in `/sys/kernel/debug/sched/` |
| Preemption model | `PREEMPT=y` (full) | `PREEMPT_LAZY=y`, `PREEMPT_DYNAMIC=y` kept | On x86 in 7.2, `PREEMPT_NONE` and `PREEMPT_VOLUNTARY` are no longer selectable (they depend on `!ARCH_HAS_PREEMPT_LAZY`); `PREEMPT_LAZY` is upstream's default and is described as "less eager to preempt SCHED_NORMAL tasks in an attempt to reduce lock holder preemption and recover some of the performance gains seen from using Voluntary preemption" | `kernel/Kconfig.preempt`; `preempt=` in `Documentation/admin-guide/kernel-parameters.txt` | `preempt=full` (or `lazy`) on the command line |
| Timer tick | `HZ=1000` | `HZ=250` | "1000 Hz is the preferred choice for desktop systems"; 250 Hz is upstream's default and "a good compromise choice allowing server performance". Fewer timer interrupts per CPU; with lazy preemption, a `SCHED_NORMAL` task is preempted at the next tick at the latest | `kernel/Kconfig.hz` | none (compile time); `nohz_full=` still available (`NO_HZ_FULL=y` kept) |
| Transparent huge pages | `always` | `madvise` | `always` "can increase the memory footprint of applications without a guaranteed benefit"; `madvise` "will only provide a performance improvement benefit to the applications using madvise(MADV_HUGEPAGE)". Engines that want THP ask for it (JVM `-XX:+UseTransparentHugePages`, jemalloc `thp:always`, allocators in analytics engines); everything else avoids compaction stalls and RSS growth. `bench/analytics` measures both paths (Go heap vs `MADV_HUGEPAGE`) | `mm/Kconfig`; `Documentation/admin-guide/mm/transhuge.rst` | `/sys/kernel/mm/transparent_hugepage/enabled` |
| TCP congestion control | `cubic` (BBR as module) | `bbr` built in and default | BBR "aims to maximize network utilization and minimize queues" and "tolerates packet loss and delay unrelated to congestion", which suits bulk transfers between pipeline stages and object stores. A compiled-in default also applies in every container network namespace. zen's out-of-tree BBRv3 stays a module | `net/ipv4/Kconfig` (`TCP_CONG_BBR`); header of `net/ipv4/tcp_bbr.c` | `net.ipv4.tcp_congestion_control` |
| Default qdisc | `fq_codel` | `fq` | `tcp_bbr.c`: "BBR might be used with the fq qdisc with pacing enabled, otherwise TCP stack falls back to an internal pacing using one high resolution timer per TCP socket and may use more resources" | `net/ipv4/tcp_bbr.c`; `net/sched/Kconfig` | `net.core.default_qdisc`, `tc qdisc` |
| Autogroup | `SCHED_AUTOGROUP=y` | `n` | Autogroup "optimizes the scheduler for common desktop workloads" by grouping tasks per session. On a server it only makes `nice` ineffective between sessions; k0s/kubelet already group workloads with cgroup v2 | `init/Kconfig` | `kernel.sched_autogroup_enabled` (needs the option built in; now it is not) |
| Workqueues | `WQ_POWER_EFFICIENT_DEFAULT=y` | `n` | "Per-cpu workqueues are generally preferred because they show better performance thanks to cache locality"; the power-efficient mode saves power "at the cost of small performance overhead" | `kernel/power/Kconfig` | `workqueue.power_efficient=1` on the command line |

The resolved `.config` delta is exactly these symbols: `ZEN_INTERACTIVE y→n`,
`COMPACT_UNEVICTABLE_DEFAULT 0→1` (follows `ZEN_INTERACTIVE`), `PREEMPT y→n`,
`PREEMPT_LAZY n→y`, `HZ_1000 y→n`, `HZ_250 n→y`, `HZ 1000→250`,
`TRANSPARENT_HUGEPAGE_ALWAYS y→n`, `TRANSPARENT_HUGEPAGE_MADVISE n→y`, `TCP_CONG_BBR m→y`,
`DEFAULT_CUBIC y→n`, `+DEFAULT_BBR y`, `DEFAULT_TCP_CONG "cubic"→"bbr"`, `NET_SCH_FQ m→y`,
`DEFAULT_FQ_CODEL y→n`, `DEFAULT_FQ n→y`, `DEFAULT_NET_SCH "fq_codel"→"fq"`,
`SCHED_AUTOGROUP y→n`, `WQ_POWER_EFFICIENT_DEFAULT y→n`.

### Evaluated and kept as they are

| Feature | Setting | Why it stays | Source |
|---|---|---|---|
| NUMA balancing | `NUMA_BALANCING=y`, default on | Moves memory to the node that uses it; on a single-node machine it has nothing to balance. Pin-heavy deployments (cpusets, `numactl`) can turn it off at runtime | `Documentation/admin-guide/sysctl/kernel.rst` (`numa_balancing`) |
| MGLRU | `LRU_GEN=y`, `LRU_GEN_ENABLED=y` | The multi-generational LRU replaces the classic active/inactive lists for page reclaim, which "directly impacts the kswapd CPU usage and RAM efficiency"; memory-heavy scans and joins are exactly where reclaim runs | `Documentation/admin-guide/mm/multigen_lru.rst` |
| zswap | `ZSWAP=y`, default on | "trades CPU cycles for potentially reduced swap I/O", useful for Arch users with disk swap. On Runink River the only swap is zram, so `runink-zram.sh` switches zswap off at boot (compressing twice gains nothing) | `Documentation/admin-guide/mm/zswap.rst` |
| sched_ext | `SCHED_CLASS_EXT=y` | BPF-loadable schedulers, for workloads that want a different policy without a different kernel. The `scx-scheds` package (Arch) provides them, e.g. `scx_rusty`, `scx_layered`, `scx_bpfland`, `scx_lavd`; none is enabled by default | `Documentation/scheduler/sched-ext.rst` |
| io_uring | `IO_URING=y` | The asynchronous I/O interface modern storage engines use; `fio` in the harness uses it. Operators who do not want it can set `kernel.io_uring_disabled=2` | `Documentation/admin-guide/sysctl/kernel.rst` (`io_uring_disabled`) |
| Huge pages | `HUGETLBFS=y`, `HUGETLB_PAGE=y`, `CGROUP_HUGETLB=y` | Explicit huge pages for engines that reserve them | `Documentation/admin-guide/mm/hugetlbpage.rst` |
| PSI | `PSI=y`, not default-disabled | Pressure stall information for CPU, memory and I/O, used by schedulers, autoscalers and OOM daemons | `Documentation/accounting/psi.rst` |
| BPF / eBPF | the whole eBPF floor in `config.require` | Runink River needs it (BPF LSM, cgroup BPF, XDP, sched_ext, BTF) | `config.require` |

### Runtime knobs on Runink River (not in the AUR package)

Settings that depend on the node rather than on the kernel build are drop-ins in the server
profile, checked by `tests/assert-golden.sh`:

| File | Setting | Why |
|---|---|---|
| `etc/sysctl.d/50-runink-analytics.conf` | `vm.max_map_count=1048576` | Columnar engines mmap many segments; the default 65530 is the first limit they hit. Arch Linux ships the same value by default ([Arch news](https://archlinux.org/news/increasing-the-default-vmmax_map_count-value/)). `Documentation/admin-guide/sysctl/vm.rst` |
| `etc/sysctl.d/50-runink-analytics.conf` | `vm.page-cluster=0` | Swap is zram only; swap-in readahead is a disk-seek optimisation and only decompresses unrequested pages there (`vm.rst`, `page-cluster`). Keeps the value zen compiled in |
| `etc/udev/rules.d/60-runink-readahead.rules` | `read_ahead_kb=1024` on whole NVMe/SATA/virtio disks | Larger readahead for sequential scans through the page cache (ext4, xfs). Does not affect OpenZFS, which prefetches on its own. Effect to be measured (`fio` seqread on ext4). `Documentation/ABI/stable/sysfs-block` |
| `usr/local/bin/runink-zram.sh` | zswap off | See zswap above |
| Workstation `etc/default/grub.d/20-runink-preempt.cfg` | `preempt=full` | The desktop keeps full preemption on the same kernel |

The AUR package ships only the kernel. On Arch, `vm.max_map_count` is already 1048576
(`filesystem` package) and everything else is left to the administrator.

### Security defaults kept

The profile trades latency for throughput, never security for throughput:

- **CPU vulnerability mitigations stay compiled in and on** (`CPU_MITIGATIONS=y` and each
  `MITIGATION_*` asserted in `config.require`). Nothing here, and no Runink River image,
  passes `mitigations=off`; `tests/assert-golden.sh` fails if the command line has it.
  Turning `ZEN_INTERACTIVE` off also turns split-lock mitigation back on.
- **Memory hardening stays** (`config.delta`): `INIT_ON_ALLOC_DEFAULT_ON`,
  `INIT_ON_FREE_DEFAULT_ON`, `SLAB_MERGE_DEFAULT=n`, `HARDENED_USERCOPY`, `FORTIFY_SOURCE`,
  `STRICT_KERNEL_RWX`/`STRICT_MODULE_RWX`, KASLR, stack protector, `RANDOMIZE_KSTACK_OFFSET`.
  `init_on_free` has a real cost ("the performance impact varies by workload, but is more
  expensive than init_on_alloc", `security/Kconfig.hardening`); it is kept deliberately, and
  the benchmark measures the kernel with it.
- **Attack surface stays trimmed**: no `/dev/mem`, no `/proc/kcore`, no kexec, no
  hibernation, lockdown LSM available (`lsm=landlock,lockdown,yama,integrity,bpf`), all
  modules signed with a per-build key.

## Installing on Arch Linux (AUR)

`linux-runink` and `linux-runink-headers` build from the `linux-runink` AUR package;
`zfs-linux-runink` builds the matching OpenZFS modules. They install next to your existing
kernel; keep that kernel as a fallback.

**Requirements and differences from Arch's kernels:**

- The kernel is compiled for **x86-64-v3** (AVX2, BMI2, FMA, MOVBE; Intel Haswell / AMD
  Excavator and newer). The PKGBUILD refuses to build it on a CPU without v3. On older CPUs
  build with `RUNINK_MARCH=x86-64` (or `x86-64-v2`).
- **Hibernation, kexec/kdump, `/dev/mem` and `/proc/kcore` are disabled** (hardening). Use
  another kernel if you need them.
- Building takes a long time: it is a full distribution-configuration kernel.

**Build and install.** With an AUR helper, `paru -S linux-runink linux-runink-headers`. By
hand:

```bash
git clone https://aur.archlinux.org/linux-runink.git
cd linux-runink
gpg --import keys/pgp/*.asc        # kernel.org and zen signing keys, shipped with the package
makepkg -si                        # verifies sha256 + signatures, then builds
```

The kernel package installs `/usr/lib/modules/7.2.7-zen1-1-runink/`; mkinitcpio's pacman
hook then creates `/etc/mkinitcpio.d/linux-runink.preset`, `/boot/vmlinuz-linux-runink` and
`/boot/initramfs-linux-runink.img`, as for any Arch kernel. Add a boot entry:

- **GRUB:** `grub-mkconfig -o /boot/grub/grub.cfg`.
- **systemd-boot:** add `/boot/loader/entries/linux-runink.conf` (adapt `options` from your
  existing entry):

  ```
  title   Arch Linux (linux-runink)
  linux   /vmlinuz-linux-runink
  initrd  /initramfs-linux-runink.img
  options root=UUID=<your-root-uuid> rw
  ```

**ZFS.** Pick one of:

- `zfs-dkms` (AUR): builds OpenZFS for every installed kernel, including linux-runink, on your
  machine, and follows kernel upgrades by itself.
- `zfs-linux-runink` (AUR): OpenZFS 2.4.4 modules built for exactly this linux-runink
  release, archzfs-style. It depends on the AUR's `zfs-utils=2.4.4`, which provides the
  userland and the mkinitcpio `zfs` hook (this package does not duplicate it). It conflicts
  with `zfs-dkms`, because both install `zfs.ko`.

With a ZFS root, rebuild the initramfs after installing the modules (`mkinitcpio -P`). The
OpenZFS modules are not signed by the kernel's per-build key; they load because
`MODULE_SIG_FORCE` is off, as on Arch's own kernels.

**Remove:** `pacman -Rns linux-runink linux-runink-headers` (and `zfs-linux-runink`), then
regenerate your boot menu or delete the systemd-boot entry.

## Benchmarking

[`bench/analytics/`](../bench/analytics/) compares kernels on **the same machine**. One run
measures the running kernel; reboot into each kernel under test and run it again with the
same arguments.

| Suite | What it measures | Tool |
|---|---|---|
| `tpch` | the 22 TPC-H queries, SF10 by default, per-query and total wall time | DuckDB CLI release binary, pinned by version and sha256 (`bench/analytics/duckdb.lock`) |
| `fio` | sequential read (1 MiB, QD32) and random read (4 KiB, 4 jobs x QD64), bandwidth, IOPS, p99 latency, on each target directory (ZFS and ext4); O_DIRECT, io_uring | `fio` |
| `stream` | STREAM-like Copy/Scale/Add/Triad bandwidth, with Go-heap arrays and with `MADV_HUGEPAGE` arrays | Go stdlib |
| `sortjoin` | parallel sort and radix-partitioned parallel hash join throughput, with checksums that must match across reps | Go stdlib |

Each suite runs one unmeasured warm-up and **5 measured repetitions**; the report gives the
median, min, max and spread. Every results file records the kernel release and version, the
command line (disk UUIDs redacted), the sha256 of the kernel configuration
(`/proc/config.gz`) and its profile-relevant symbols, the runtime policy (THP, congestion
control, qdisc, NUMA balancing, MGLRU, zswap, governor, CPU vulnerability status), the CPU
model, core counts, NUMA nodes and RAM. The TPC-H numbers are TPC-H-derived measurements,
not audited TPC-H results.

**On the target machine (Arch Linux):**

```bash
pacman -S --needed go fio linux-zen linux-zen-headers linux-lts linux-lts-headers
# plus linux-runink from the AUR (above); add all three to the boot menu.

# Prepare the storage targets once: an empty directory on the ZFS dataset under test and one
# on an ext4 filesystem, each with at least 2x --fio-size free, and the TPC-H database on ZFS.
# For each kernel: boot it, keep the machine otherwise idle, then
bench/analytics/run.sh --out results/$(uname -r) \
    --fio-dirs zfs=/tank/bench,ext4=/mnt/ext4/bench --tpch-dir /tank/bench/tpch
# After all three:
bench/analytics/run.sh compare results/*runink*/results.json \
    results/*zen*/results.json results/*lts*/results.json
```

For a fair comparison: the same BIOS settings, the same command line apart from the kernel,
the same CPU frequency governor (the results record it), no other load (`run.sh` prints the
load average), and the same datasets. The comparison marks a difference smaller than the
larger spread of the two runs with `~` (within noise) and refuses to hide a missing suite. A
node of Runink River Server has no `curl` or `go`: build `riverbench` and fetch DuckDB on
another machine and copy them with `rsync`, then pass `DUCKDB=/path/to/duckdb`.

**Status.** The harness has been validated with a smoke run (`--smoke`: SF 0.1, small
arrays, 256 MiB fio files) on a development machine running a different kernel. That run
proves the harness works; it compares nothing. The linux-runink vs linux-zen vs linux-lts
comparison is **to be measured** on the target hardware.

## ZFS pacing policy

OpenZFS support **gates** every kernel change:

1. `linux-runink` is only bumped (within 7.2.x) or rebased (7.2 → 7.3) when the pinned
   OpenZFS release's `META` declares the new series (`Linux-Maximum`). OpenZFS 2.4.4 declares
   `Linux-Maximum: 7.2`.
2. Both ZFS packages enforce it: `runink-zfs` and `zfs-linux-runink` fail in `prepare()` when
   the kernel is outside that range.
3. The kernel and its ZFS modules are bumped **together** (`river lint aur-sync` checks
   that the AUR packages pin the same versions as the tree), and on Runink River nodes they
   upgrade in one transaction after a boot-environment snapshot
   ([runink-kernel README](../build/pkgbuilds/runink-kernel/README.md#upgrade-and-rollback)).
4. If the 7.2 series approaches its kernel.org end of life before an OpenZFS stable covers
   7.3, the rebase waits for OpenZFS: a kernel without its root filesystem driver is not a
   release.

## Upstream references

All paths are in the Linux 7.2.7 source tree with the zen patch applied.

- `init/Kconfig`: `ZEN_INTERACTIVE` (zen), `SCHED_AUTOGROUP`
- `kernel/Kconfig.preempt`, `kernel/Kconfig.hz`, `kernel/power/Kconfig`
- `mm/Kconfig`, `Documentation/admin-guide/mm/transhuge.rst`,
  `Documentation/admin-guide/mm/multigen_lru.rst`, `Documentation/admin-guide/mm/zswap.rst`,
  `Documentation/admin-guide/mm/hugetlbpage.rst`
- `net/ipv4/Kconfig`, `net/ipv4/tcp_bbr.c`, `net/sched/Kconfig`
- `Documentation/admin-guide/sysctl/kernel.rst`, `Documentation/admin-guide/sysctl/vm.rst`,
  `Documentation/admin-guide/kernel-parameters.txt`, `Documentation/ABI/stable/sysfs-block`
- `Documentation/scheduler/sched-ext.rst`, `Documentation/accounting/psi.rst`,
  `security/Kconfig.hardening`
