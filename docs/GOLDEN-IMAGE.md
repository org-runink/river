# The Runink River Server image contract

> **Downstream server node.** Since 2026-09-26 the server profile lives in downstream
> distributions, not in this repository; this contract (and `tests/assert-golden.sh`) is what
> such a server node must satisfy. The Runink River workstation is checked by
> `build/qemu-gui-test.sh` instead.

This is what an installed, booted Runink River Server node must satisfy. `tests/assert-golden.sh`
checks the OS half and `tests/smoke-k0s.sh` checks the cluster half; both run on the node
(see [`tests/README.md`](../tests/README.md)).

## OS

| Property | Value |
|---|---|
| Init | s6, s6-rc and s6-linux-init, plus elogind (`KillUserProcesses=no`). No systemd. |
| Kernel | `linux-runink` only (zen-kernel 7.2.x stable fork), with the matching prebuilt `runink-zfs` module under `/usr/lib/modules/*-runink/extra`. No `linux` or `linux-lts`. |
| Root filesystem | ZFS boot environment `<pool>/ROOT/<be>`, `canmount=noauto mountpoint=/`. |
| Encryption | Every dataset encrypted (aes-256-gcm, one encryption root at the pool root) with its key loaded. See [ENCRYPTION.md](ENCRYPTION.md). |
| ESP | vfat, mounted at `/boot` with `fmask=0077,dmask=0077` and an `/etc/fstab` entry; holds the kernel, initramfs and GRUB. |
| hostid | `/etc/hostid` matches the pool's hostid and is baked into the initramfs. |
| Firmware | `linux-firmware-intel` only. |
| CPU baseline | x86-64-v3 (AVX2, FMA, F16C). The reference machine is an Intel Core Ultra 7 155H (Meteor Lake, AVX2, no AVX-512). |
| CPU governor | `performance` on all cores. |
| Swap | zram only, as an OOM backstop; no disk swap on the pool. |
| Absent | X11, Mesa, Vulkan, audio, codecs, fonts and the other packages in the profile's `forbidden.*` lists; podman; the `curl` and `wget` binaries; host-side inference binaries. |
| File modes | Every node secret or state file is 0600 in a 0700 directory, owned by its one reader. `/etc/runink` is 0700. `river-perms --check` reports no drift. |

### sysctl

| Key | Value | Why |
|---|---|---|
| `net.ipv6.conf.all.forwarding` | 1 | Pod and Service traffic on the IPv6-only cluster. |
| `net.bridge.bridge-nf-call-ip6tables`, `...-iptables` | 1 | kube-proxy and NetworkPolicy see bridged pod traffic. |
| `net.netfilter.nf_conntrack_tcp_be_liberal` | 1 | Stops out-of-window TCP on long-lived flows being marked INVALID and silently dropped by kube-router's per-pod rule. This does not relax access control. |
| `kernel.unprivileged_userns_clone`, `user.max_user_namespaces` | 1, > 0 | Required by `river-sandbox` (unprivileged bubblewrap) and rootless in-cluster image builds. |
| `vm.swappiness` | 10 | Keep memory-resident model weights out of swap. |
| `net.ipv4.ip_unprivileged_port_start` | 80 | The edge binds 80/443 without a capability. Set in `99-runink.conf` (it was asserted but set nowhere until 2026-09-25). |
| `net.core.default_qdisc` | fq | BBR pacing (docs/KERNEL.md). Restated in `99-runink.conf` because `/usr/lib/sysctl.d/50-default.conf` resets it to fq_codel. |

Kernel hardening sysctls (`kptr_restrict`, `dmesg_restrict`, `kexec_load_disabled`, Yama
ptrace scope, BPF JIT hardening, protected links and FIFOs, no redirects or source routing)
live in `/etc/sysctl.d/10-runink-hardening.conf`.

## Users

- `runink`, uid 1000, with `subuid`/`subgid` `runink:100000:65536` and `/run/user/1000`
  created at boot.
- The `runink` user can open a `river-sandbox`, and the sandbox cannot see `/etc/runink` or
  the runink home directory. `/usr/bin/bwrap` is not setuid.
- SSH: key authentication only, no root password login. Operator keys arrive through
  enrollment (`RUNINK_SSH_AUTHORIZED_KEYS`); the image ships none.

## Network

| Property | Value |
|---|---|
| Host addressing | Dual-stack: IPv4 DHCP + IPv6 SLAAC/DHCPv6 on NetworkManager-managed interfaces (`conf.d/10-dual-stack.conf`); sshd listens on both families. `NET_MODE=ipv6` at enrollment turns IPv4 off. The k0s node address is always IPv6 (`runink-node-ip6`). |
| Cluster | k0s, IPv6 single-stack: pods `fd00:10:244::/56`, services `fd00:10:96::/108`, per-node mask /64. |
| Cluster DNS | The image's own CoreDNS at `fd00:10:96::a` (k0s's CoreDNS is disabled). |
| NAT64 | TAYGA on `64:ff9b::/96`, dynamic pool `192.168.255.0/24`. |
| Firewall | `inet runink_fw` nftables table, loaded from first boot on every node (s6 oneshot `runink-fw`, before NetworkManager and sshd): base posture default-deny with SSH open, private posture (whitelist) once enrollment arms it; forward default-drop except the IPv6 cluster and NAT64. |
| Registry | node-local `localhost:5000`, plain HTTP, not reachable off the node. |

## Processes

Launched from `/etc/s6/rc.local` in init context, each a respawn loop or a oneshot:

| Process | Runs when |
|---|---|
| `k0s controller --single` (or `controller` / `worker` per `K0S_ROLE`) | Always on an installed node; a worker waits for its join token. |
| `he-tunnel-keepalive.sh` | `/etc/runink/he-tunnel.env` exists. |
| `runner-keepalive.sh` | A GitHub Actions runner was registered at enrollment. |
| `runink-backup.sh` | `zfs` is present; off-box send only if `/etc/runink/backup.env` sets `BACKUP_TARGET`. |

None of them run in the live installer session.

## Cluster

| Check (`smoke-k0s.sh`) | Expectation |
|---|---|
| k0s API | `/readyz` answers. |
| `kube-system/coredns` | At least one available replica. |
| `SMOKE_DEPLOYMENTS` | Each listed `namespace/name` has at least one available replica (default: none). |
| `SMOKE_HOSTS` or `/etc/runink/app-hosts` | Each name answers HTTPS on `127.0.0.1` with a 2xx or 3xx. |
| Payload smoke test | `/usr/local/share/runink/core/smoke.sh` succeeds, when present. |

A base image passes with only the first two rows. What a payload deploys, and how it
terminates TLS or authenticates users, is defined by that payload, not by this contract.

## Data locations

| Path | Contents |
|---|---|
| `/var/lib/core/models/shared` | GGUF model weights synced at enrollment and verified against `/usr/local/share/runink/models.manifest`. It is a real directory, world-readable, because inference workloads mount it as a `hostPath` of type `Directory`. |
| `/var/lib/core` | Workload state. Snapshotted with the boot environment. |
| `/var/lib/k0s` | k0s state (kine/SQLite cluster store, PKI). |
| `/usr/local/share/runink/core` | The payload tree, or `PAYLOAD-NONE`. |
| `/etc/runink` | Node identity and enrollment output (0700). |
| `/var/lib/runink/.enrolled` | Enrollment sentinel. |
