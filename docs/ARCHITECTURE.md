# Runink River architecture

> **Server-era document.** Since 2026-09-26 Runink River is a developer workstation
> (AGENTS.md, "What this is") and the server profile lives in downstream distributions. This
> page still describes the server node that such a distribution builds from this tooling;
> the build, payload and first-boot contracts it links to are current.

Runink River Server is a headless Linux distribution for running
Kubernetes workloads on hardware you own: s6 init, a ZFS root with native encryption, a
dual-stack host with an IPv6-only cluster network, and a single-binary k0s cluster. This repository builds the
bootable installer ISO, the installer that puts the image on a disk, and the first-boot
enrollment that turns an installed disk into a working node.

Runink River builds a complete, bootable **base image** on its own. A downstream platform that
wants to ship on Runink River supplies an optional **payload** at build time (host binaries plus a
deploy entrypoint); Runink River packages it, and first-boot enrollment runs it. The contract is in
[BUILD.md, "Downstream payloads"](BUILD.md#downstream-payloads). Nothing in this repository
depends on any particular payload.

The ISO is currently assembled with Artix Linux's `artools`/`buildiso`, which is the
transitional builder. Runink River is replacing that userland with its own from-source base; the
plan is in [OWN-BASE.md](OWN-BASE.md).

## Layers

### 1. Profiles (`iso-profiles/`)

One `artools` profile, `initsys=s6`: `iso-profiles/river/`, Runink River, the developer
workstation. (Until 2026-09-26 this repository also held a server profile; a server image is
now built by a downstream distribution from its own profile, docs/BUILD.md "Downstream
distributions". Parts of this document still describe that server and say so.)

Inside a profile:

| File | Role |
|---|---|
| `profile.yaml` | Authoritative for artools 0.39+: the live-session settings and the package set baked into the live rootfs. |
| `Packages-Root` | The curated allow-list checked by `river lint closure` and `scripts/gen-pkglist-lock.sh`. The installer does not resolve it on the target. |
| `forbidden.explicit`, `forbidden.closure` | Packages that must not appear in the manifest or anywhere in its dependency closure (desktop stack, codecs, a second kernel, podman, curl/wget, ...). |
| `profile.conf` | Legacy pre-0.39 format, kept for reference; artools ignores it. |
| `root-overlay/` | A filesystem tree merged into the rootfs: the s6 launcher, sysctl, k0s config, NetworkManager and sshd drop-ins, the installer steps, the enrollment script and the keepalive loops. |
| `live-overlay/` | Merged into the live session only. |

### 2. Packages (`build/pkgbuilds/`)

Everything Runink River adds to the base distribution enters the ISO as a pacman package from a
pinned local `[runink]` repository, which is injected ahead of the distribution repos:

| Package | Contents |
|---|---|
| `linux-runink` (`runink-kernel/`) | The only kernel: a fork of zen-kernel on the 7.2.x stable series, built from signature-verified sources for generic x86-64-v3. |
| `runink-zfs`, `runink-zfs-utils` | OpenZFS stable, built as a prebuilt module for exactly that kernel, plus the matching userland and the mkinitcpio `zfs` hook. Split from one PKGBUILD so module and userland cannot skew. |
| `runink-k0s` | The pinned k0s release binary (bundles containerd, runc, kubelet and the CNI), integrity-checked against upstream checksums. |
| `runink-tayga` | The TAYGA NAT64 translator, built from a sha256-pinned upstream tarball. |
| `runink-grub-live` | The live medium's GRUB scaffolding: exactly the `/usr/share/grub` paths `buildiso` copies off the livefs layer onto the ISO. It replaced Artix's `artix-grub-live`; the boot menu itself is the profile's own `grub/`. Live medium only — the install removes it. |
| `runink-installer` | `river-hwprobe` and `river-plan`, the installer's hardware probe and planner, and `river-netsetup` + `river-netcheck`, the network-first step ([INSTALL.md](INSTALL.md#network-first)), built from `installer/` (Go standard library, `GOAMD64=v1`), plus `models.tiers` when one is supplied. On every image. |
| `runink-runtime` | Host binaries staged by a downstream payload (none on a base image) plus `models.manifest`. |
| `runink-core` | The downstream platform tree at `/usr/local/share/runink/core`, or a `PAYLOAD-NONE` marker on a base image. |

### 3. Installer (`installer/`)

Before it asks anything, `runink-install` (a `dialog` TUI in the live session) probes the
hardware with `river-hwprobe`, makes an install plan with `river-plan` (RAM budget,
threads, the ZFS layout across the eligible disks), shows it, stops on a machine below the
documented minimums and has the operator confirm every target disk by typing its serial;
the boot medium is never eligible. It then runs the ordered `NN-*.sh` steps from an
**explicit, hard-coded list**: preflight, plan re-check (`05-hwplan-verify` re-probes and
re-resolves the plan by serial), ZFS pool and boot environment, clone, target config, boot,
user, artifacts, enrollment staging, plan record (`75-install-plan`), s6 enable, export.
The directory is never globbed, so a script in the step directory that is not named in the
list never runs. The probe, the plan schema and the decision table are in
[INSTALLER-HARDWARE.md](INSTALLER-HARDWARE.md). The steps exist in two copies,
`installer/lib/` and the copy baked into each profile's root overlay
(`root-overlay/usr/local/lib/runink-install/`); any step present in both must be
byte-identical, which `river lint installer-sync` enforces.

The target is populated by **install-from-live**: the live rootfs already is the finished
system, so `20-clone-rootfs` copies it onto the ZFS boot environment with `rsync`. No
package repository, network access or version resolution happens at install time.
`runink-autoinstall` runs the same list non-interactively, without the two enrollment
staging steps. It plans the same way, reading a saved plan from `--plan-file` or planning
itself, and takes each disk confirmation as `--yes-i-have-checked-serial=<serial>`.
The step-by-step table is in [INSTALL.md](INSTALL.md).

`installer/golden-snapshot` is an alternative for a machine that already runs this stack:
it captures that machine's boot environment as a `zfs send` stream with node-specific state
stripped out.

## Boot to running node

1. **GRUB** (on the FAT ESP) loads `linux-runink` and an initramfs whose `zfs` hook imports
   the pool by its baked hostid and loads the encryption key (a key provider, or a console
   passphrase prompt). See [ENCRYPTION.md](ENCRYPTION.md).
2. **s6-linux-init** brings up the default bundle: udev, dbus, elogind, NetworkManager,
   sshd, the `river-perms` oneshot (re-asserts 0600/0700 modes on every secret and state
   file and logs any drift) and `rc-local`.
3. **`/etc/s6/rc.local`** runs in init context (so elogind cannot reap what it starts). In
   the live installer session (`overlay=livefs` on the kernel command line) it exits at
   once. On an installed node it runs, in order:
   - `runink-firstboot.sh` once, until enrollment has completed (wired in by installer
     step `70-secrets-models`);
   - tuning: `sysctl --system`, the `performance` CPU governor, zram;
   - `runink-fw apply`: re-applies the host firewall the s6 oneshot `runink-fw` loaded at
     boot (base posture, or the private one once enrollment armed it);
   - `he-tunnel-keepalive.sh`: an optional Hurricane Electric 6in4 tunnel for uplinks
     without native IPv6 (a no-op unless enrolled);
   - `lan-hosts.sh`: maps the node's own public service names to its on-link address in
     `/etc/hosts`, so local clients do not need to hairpin through a router's NAT;
   - `k0s-keepalive.sh`: supervises `k0s controller` or `k0s worker` in a respawn loop,
     in the role written by enrollment (`single` by default);
   - `runner-keepalive.sh`: an optional GitHub Actions runner (a no-op unless registered);
   - `runink-backup.sh`: periodic recursive ZFS snapshots, with an optional raw
     (still-encrypted) incremental `zfs send` to a remote host.
4. **Workloads** run as k0s pods. On a base image the node is an empty single-node
   cluster; with a payload, first-boot enrollment runs the payload's `deploy` entrypoint
   against the local API.

## Networking

- **Dual-stack host, IPv6-only cluster.** The host takes IPv4 (DHCP) and IPv6
  (SLAAC/DHCPv6) on its LAN by default (NetworkManager `conf.d/10-dual-stack.conf`), and
  sshd listens on both families behind the default-deny firewall. IPv6-only sites set
  `NET_MODE=ipv6` at enrollment (or `river.net=dhcp,v6only` on the live medium). The k0s
  cluster is single-stack IPv6 with ULA ranges (pods `fd00:10:244::/56`, services
  `fd00:10:96::/108`) either way. Because k0s would otherwise advertise the host's IPv4,
  `k0s-keepalive` and `runink-firstboot` pin kubelet's `--node-ip` and `api.address` to the
  host's IPv6 address (`runink-node-ip6`) whenever the host has IPv4.
- **NAT64.** TAYGA translates the RFC 6052 well-known prefix `64:ff9b::/96` using a dynamic
  pool in `192.168.255.0/24`, so IPv6-only pods can reach IPv4-only destinations. The
  NetworkManager dispatcher `61-runink-nat64` brings it up; `60-runink-k8s-route` adds a
  route for the service CIDR, which the kernel needs because the node may receive no IPv6
  default route.
- **Cluster DNS.** k0s computes an invalid CoreDNS service IP on an IPv6-only cluster, so the
  image disables k0s's CoreDNS and ships its own manifest
  (`/var/lib/k0s/manifests/runink-coredns/`) at `fd00:10:96::a`. Its upstreams are written at
  enrollment and reach IPv4 resolvers through NAT64.
- **CNI.** kube-router, pinned to a build that uses the same nftables backend as kube-proxy.
  The reasons for each non-default setting are recorded inline in `/etc/k0s/k0s.yaml`.
- **Firewall.** `runink-fw` manages an `inet runink_fw` nftables table for both families,
  loaded on every node from first boot by the s6 oneshot `runink-fw`, before NetworkManager
  and sshd. The **base** posture (enrolled or not) is default-deny: loopback, established,
  ICMPv6 and ICMPv4 essentials, DHCP client replies, the cluster, SSH on port 22, and ports
  opened on purpose (`RUNINK_FW_OPEN`, `runink-fw open`). A **private** node swaps in a
  whitelist at enrollment (`RUNINK_FW_WHITELIST`, with ephemeral, auto-expiring `allow`
  entries) instead of open SSH. In both, **forward is default-drop** except the IPv6 cluster
  (pod and service CIDRs) and the NAT64 path (tayga's pool on `nat64`), so the host routes
  nothing for its LAN. `net.ipv4.ip_forward=1` stays only because NAT64 is IPv4 forwarding.
  On the live medium only, the LAN-install pairing ports (UDP 47653, TCP 47654 and 47655)
  are accepted from IPv6 link-local sources (`pair_tcp`/`pair_udp`, filled by `runink-fw`
  from the live overlay's `river-pair/fw-pair`); installed nodes keep those sets empty.
  `river lint firewall` checks the rules against `k0s.yaml` and `tayga.conf`.
- **Image registry.** containerd trusts a node-local registry at `localhost:5000` (plain
  HTTP, never exposed off the node) for images built inside the cluster.

## Hardening invariants

- One kernel (`linux-runink`), Intel firmware only, no display or audio stack.
- s6 only: no systemd anywhere in the image.
- No runtime fetch tools on the node: `curl` and `wget` binaries are removed at install
  (the shared libraries stay). rsync over SSH is the only transfer path.
- Nothing secret is baked into the ISO. Node identity and credentials arrive through the
  enrollment file, which is shredded after use.
- Every secret or state file is owned by its one reader and is 0600 in a 0700 directory
  (`river-perms`), and every ZFS dataset is encrypted.
- Unprivileged user namespaces stay enabled for `river-sandbox` (bubblewrap), which confines
  untrusted build or pipeline steps: no capabilities, no network unless requested, no
  nested user namespaces, and no view of `/etc/runink`.

`tests/assert-golden.sh` checks these on a running node; [GOLDEN-IMAGE.md](GOLDEN-IMAGE.md)
is the full contract.

## The RIVER runtime (`runtime/`, planned, not built)

The RIVER runtime (*Raft-Integrated Validated Event Runtime*) is a planned raft-based
pipeline runtime: a Go library for the pipeline formats (TOML `.dsl`, Go `.contract`, TOML
`.herd`, `@step` `io.Reader`/`io.Writer` steps, golden tests) plus a `riverd` service that
places and runs pipelines on the cluster, confining each step with `river-sandbox` and a
cgroup v2 leaf. Nothing under `runtime/` is compiled into the image today. The plan is in
[`runtime/README.md`](../runtime/README.md).
