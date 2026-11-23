<!--
SPDX-FileCopyrightText: 2026 The RIVER Authors
SPDX-License-Identifier: MIT
-->

# Runink River on Google Cloud (Compute Engine)

> **Status: design, with phase 1 in code.** This page says how Runink River becomes an image
> anyone can start on Compute Engine. It is free, like Fedora CoreOS: no paid tier and no
> licence gate. It lists what Google requires, how Runink River meets each item, which paths
> exist, what they cost, and what "a secure base image for agentic development" means in
> config lines. Every claim either points at a file in this repository or is labelled
> **to build**. Google's pages were fetched on 2026-11-23. Quotes are short and linked.

Related: [CLOUD-IMAGES.md](../CLOUD-IMAGES.md) is the existing cloud pipeline (server-era,
cluster node profile). This page adds the **base** profile and the publishing plan. It reuses
that pipeline rather than forking it.

## 1. Decision in one paragraph

Publish Runink River as a **free public image project**, the way Fedora CoreOS, AlmaLinux and
the community Arch Linux image do: `--image-project=<ours> --image-family=runink-river`. This
is self-service: no Google programme to join and no partner agreement. It costs roughly
**$0.15 per image version per month** (section 5). A **free Google Cloud Marketplace listing**
can follow. It needs a Partner Network membership and Google's written acceptance of an image
not built on a Google base image. That is an open question, so the listing is optional and
second.

## 2. What Google requires of an imported Linux image, and how Runink River meets it

### 2.1 Image format

[Import a boot disk image manually](https://docs.cloud.google.com/compute/docs/import/import-existing-image):

- "The disk image filename must be `disk.raw`."
- "The RAW image file must have a size in an increment of 1 GB."
- "must be a `.tar.gz` file that uses gzip compression", made with "the `--format=oldgnu`
  option for the `tar` utility".
- "Add `console=ttyS0,38400n8d` to the kernel command-line arguments".

How Runink River meets it:

- `build/cloud-image.sh` writes exactly `disk.raw` and checks it is a whole number of GiB.
- It archives the disk with `tar --format=oldgnu -Sczf <name>.tar.gz disk.raw` and checks
  the archive holds only that file.
- Installer step `78-cloud-target` (`installer/lib/78-cloud-target.sh`, `cloud_gce`) sets
  `console=tty0 console=ttyS0,38400n8d` and removes `quiet`. It fails the image if
  `grub.cfg` lacks the console or still has `quiet`.

The same page notes that image import is moving to Migrate to Virtual Machines.
`gcloud compute images create --source-uri` stays the documented manual route.

### 2.2 GPT, UEFI and Shielded VM

[Creating shielded images](https://docs.cloud.google.com/compute/shielded-vm/docs/creating-shielded-images):
a Shielded VM image "requires a GUID Partition Table (GPT) scheme". Runink River is
UEFI-only. Its disk is GPT with a FAT32 EFI system partition (`/boot`) and one ZFS partition,
and GRUB is on the removable path `EFI/BOOT/BOOTX64.EFI`.

The image is created with the `UEFI_COMPATIBLE` guest OS feature. The
[custom image page](https://docs.cloud.google.com/compute/docs/images/create-custom) lists
UEFI_COMPATIBLE with "Secure Boot : disabled by default", and vTPM and integrity monitoring
on.

**Secure Boot, today: off.** "Google Cloud provides a default value for `PK`, `KEK`, `db`,
and `dbx`". The default `db` holds Microsoft certificates. Runink River's GRUB and kernel are
signed by neither ([SECURE-BOOT.md](../SECURE-BOOT.md): "Secure Boot **off**"). Shielded VM
vTPM and integrity monitoring work without Secure Boot.

**Secure Boot, to build: our own keys in the image.** A custom image can carry its own UEFI
variables. The flags are `--platform-key-file`, `--key-exchange-key-file`,
`--signature-database-file` and `--forbidden-database-file`, with "DER-encoded X.509"
certificates. Google warns that "adding your own certificates will overwrite the default ones
rather than merging". That is what we want: an instance from the image then boots only what
our key signed. The steps:

1. An offline Runink River Secure Boot CA. The organisation-root model in
   [SECURE-BOOT.md](../SECURE-BOOT.md) §1, kept like the release key.
2. A signed GRUB (or, with the own base, a signed unified kernel image) and a signed
   `linux-runink`.
3. `MODULE_SIG_FORCE` and `lockdown=integrity`. Both are staged today; see
   `build/pkgbuilds/runink-kernel/config.delta` ("staged — enabled only AFTER a boot test").
   The ZFS modules are signed with the per-build key.
4. Pass the CA certificate as `--signature-database-file`.

We do not need a Microsoft-signed shim for the cloud image, because the image brings its own
`db`. A shim is only needed for arbitrary bare-metal firmware. Google also notes that the
Microsoft UEFI CA 2011, which signs shims, expired on 2026-06-27
([certificate expiration](https://docs.cloud.google.com/compute/docs/security/ms-secure-boot-certificates-expiration)).
That is one more reason to use our own `db` and not chase a 2011-chained shim.

### 2.3 Kernel drivers: virtio-scsi, NVMe, gVNIC

[Building custom operating systems](https://docs.cloud.google.com/compute/docs/images/building-custom-os)
says "You must build the operating system kernel with the following options". They are
`CONFIG_KVM_GUEST`, `CONFIG_KVM_CLOCK`, `CONFIG_VIRTIO_PCI`, `CONFIG_SCSI_VIRTIO`,
`CONFIG_VIRTIO_NET` and `CONFIG_PCI_MSI`. The same page also says:

- NVMe: "all third generation and later machine series, you must use an NVMe storage
  controller".
- gVNIC: "gVNIC is required for second generation machine series that use per VM Tier_1"
  networking, and for third generation and later.

`linux-runink` 7.2.7 (`build/pkgbuilds/runink-kernel/config.base`; verified in the built
package, section 8) has:

| Option | Value | Notes |
|---|---|---|
| `KVM_GUEST`, `PARAVIRT_CLOCK` | `y` | `KVM_CLOCK` no longer exists as a symbol in 7.x; the KVM clock is `PARAVIRT_CLOCK` under `KVM_GUEST` |
| `VIRTIO_PCI`, `SCSI_VIRTIO`, `VIRTIO_BLK`, `PCI_MSI` | `y` | |
| `VIRTIO_NET` | `m` | forced into the initramfs |
| `BLK_DEV_NVME` | `m` | forced into the initramfs |
| `GVE` (gVNIC) | `m` | forced into the initramfs |
| `IDPF` | `m` | bare metal and Cloud RDMA only; not declared |
| `SERIAL_8250_CONSOLE`, `TCG_TPM`, `TCG_CRB`, `TCG_TIS` | `y` | serial console; Shielded VM vTPM |

The kernel needs no config change. Step `78-cloud-target` adds `nvme virtio_net gve` to the
initramfs and fails the image if `lsinitcpio` does not list them. With this change it also
fails the image if the running kernel (the target's own `linux-runink`) lacks any of
`KVM_GUEST VIRTIO_PCI SCSI_VIRTIO PCI_MSI SERIAL_8250_CONSOLE` as `=y`.

Guest OS features to declare: `UEFI_COMPATIBLE`, `GVNIC` and `VIRTIO_SCSI_MULTIQUEUE`.
`SEV_CAPABLE`, `SEV_SNP_CAPABLE` and `TDX_CAPABLE` are not declared. The kernel has the
options, but no Confidential VM boot has been tried.

### 2.4 Serial console

Kernel and GRUB output go to `ttyS0` at 38400 baud. A getty runs there: the s6 `ttyS`
service with `/etc/s6/config/ttyS.conf`, both written by `78-cloud-target`.
`gcloud compute instances get-serial-port-output` shows the boot.

The first-boot code prints a single machine-readable line to the console:
`RUNINK-HEALTH ready (...)` or `RUNINK-HEALTH failed (...)`. A deployment check can wait for
it.

### 2.5 Time: NTP from the metadata server

[Configuring imported images](https://docs.cloud.google.com/compute/docs/images/configuring-imported-images):
"include only the `server metadata.google.internal iburst` Google NTP server". Runink River
ships chrony (`iso-profiles/river/Packages-Root`). For a GCE image, step `78-cloud-target`
now does three things:

- writes `server metadata.google.internal iburst` as the only source;
- comments out the image's public NTS servers and pool;
- fails the image unless exactly that one source is left.

Instances without an external address reach no public NTP server, so this is a fix, not a
preference.

### 2.6 The guest environment, under s6 and not systemd

Google: "You must install the guest environment before you can use key features". The guest
environment is Apache-2.0 Go code
([guest-agent](https://github.com/GoogleCloudPlatform/guest-agent) and its successor
[google-guest-agent](https://github.com/GoogleCloudPlatform/google-guest-agent), read at
their 2026-11-20 heads). We checked whether it can run under s6.

| Part of the agent | systemd dependency | Evidence |
|---|---|---|
| service lifecycle | **hard** | `internal/daemon/daemon_linux.go`: the daemon client is a `systemdClient`, and every start, stop, restart, enable and is-active shells out to `systemctl`. There is no other backend |
| readiness | soft | `internal/service/service_linux.go`: `systemd-notify` only if `NOTIFY_SOCKET` is set |
| OS Login plugin | **hard** | `cmd/core_plugin/oslogin/oslogin_linux.go`: `systemctl start --no-block google-oslogin-cache.service`, and restarts sshd and nscd through the daemon client. It edits `/etc/ssh/sshd_config` and `/etc/pam.d/sshd` |
| hostname | soft | `syscall.Sethostname`; `hostnamectl` only if found |
| units | systemd only | `google-guest-agent.service`, `-manager.service` and `-compat-manager.service` are `Type=notify` and `Restart=always` |
| OS Login itself | separate C++ code | [guest-oslogin](https://github.com/GoogleCloudPlatform/guest-oslogin): NSS module, PAM modules and `google_authorized_keys`, linked against libcurl and json-c, with systemd timer units |

**Decision: do not ship the Google guest agent now.** It cannot run under s6 without either
a patched daemon client or a fake `systemctl`. Invariant 1 rules out the fake, and we do not
fork an agent we would then have to track.

Runink River instead ships `river-cloud-init` (`installer/cloudinit`, MIT). It is one
standard-library Go binary run by s6 services in `installer/cloud/s6/`. Phase 1 covers:

| Guest environment feature | Runink River | Status |
|---|---|---|
| SSH keys from `ssh-keys` (instance, and project unless `block-project-ssh-keys`) | `river-cloud-init boot`, then the `river-cloud-watch` longrun | **done**; keys follow metadata changes without a reboot (this change) |
| Expired `gcloud compute ssh` keys (`google-ssh {"expireOn":…}`) skipped | `cloud.ParseSSHKeys` | done (existing) |
| hostname from `instance/hostname` | `river-cloud-init boot` | done (existing) |
| disk growth to the instance's disk | `river-cloud-init boot` (`grow`: GPT moved, partition and ZFS pool grown) | done (existing), ZFS root only |
| per-instance machine-id, SSH host keys, ZFS hostid, pool GUID | `identity` and `late` | done (existing) |
| NTP from metadata | chrony, set by `78-cloud-target` | **done** (this change) |
| OS Login (`enable-oslogin`) | none | **to build**: an NSS + `AuthorizedKeysCommand` client in Go, or OS Login's C++ components packaged without their systemd units |
| startup and shutdown scripts from metadata | none | **to build, or deliberately not**: running arbitrary metadata as root is exactly what an agent sandbox host should not do by default |
| guest attributes, Windows password reset, workload certificates | none | not planned |

The `watch` subcommand uses the documented long-poll ([querying
metadata](https://docs.cloud.google.com/compute/docs/metadata/querying-metadata): "append the
`?wait_for_change=true` query parameter"). It polls the recursive tree with `last_etag` and
re-applies the keys whenever the ETag moves. The ETag only advances after a successful apply,
so a failed write is retried and never skipped.

### 2.7 Accounts, SSH and passwords

There is no default password and login is SSH-key only. `78-cloud-target` writes
`/etc/ssh/sshd_config.d/05-river-cloud.conf` with:

```
PermitRootLogin no
PasswordAuthentication no
KbdInteractiveAuthentication no
AuthorizedKeysFile .ssh/authorized_keys /etc/ssh/authorized_keys.d/%u
ClientAliveInterval 420
```

These are the values Google lists. `85-cloud-finalize` locks the root and `runink`
passwords and asserts that no `authorized_keys`, host key or machine-id is left in the image.
Every metadata key is granted to the one admin account, `runink`. Per-user accounts from the
key's username are **to build** together with the agent account (section 4).

### 2.8 Base profile: an image with nothing to unlock

The existing cloud image is a **node** image. Its data datasets are encrypted under a
per-instance key that a key provider (Secret Manager) wraps. Without that secret the boot
stops before the workload starts (fail closed). That is right for a node and wrong for a
general-purpose base image that anyone starts with nothing but an SSH key.

This change adds `RIVER_CLOUD_PROFILE=base` in `/etc/river-cloud/cloud.env`, written by
`78-cloud-target` from `RUNINK_CLOUD_PROFILE`:

- `river-cloud-init boot` does identity, hostname, SSH keys and disk growth, and prints
  `RUNINK-HEALTH ready`.
- It creates no data datasets, stages no enrollment and sets no cluster address.
- `late` only reguids the pool and rebuilds the initramfs for the instance's hostid.
- An unknown profile value is an error, never a guess.

**Honest gap:** a base image's root is not encrypted. That departs from invariant 3, as the
node cloud image already does ([CLOUD-IMAGES.md](../CLOUD-IMAGES.md), "Encryption"), and
needs the same TSC vote. Compute Engine encrypts persistent disks at rest with Google- or
customer-managed keys. An encrypted base root needs the vTPM key provider, which is still a
stub (**to build**).

## 3. What builds the image

Reuse, not a fork:

1. **Disk, packaging and test.** `build/cloud-image.sh` builds the disk and
   `build/cloud-image-test.sh` boots it under OVMF, NVMe and the Go metadata stub. These are
   the same scripts a downstream server distribution's cloud target calls.
2. **What this change adds:**
   - the base profile;
   - `river-cloud-watch`;
   - the s6 definitions in `installer/cloud/s6/` (identity, init, late, watch);
   - NTP and the kernel-option check in `78-cloud-target`;
   - `85-cloud-finalize` enabling `river-cloud-watch` when present;
   - `wait_for_change` support in the metadata client and the fake server.
3. **To build before the first public image:**
   - `cloud-image.sh` builds from a downstream server profile today
     (`RIVER_PROFILE_DIR`), because Runink River has had no cloud installer since the
     server profile left this tree. A Runink River image needs an in-tree **headless cloud
     edition**: the workstation's package set minus the desktop, plus the
     `installer/cloud/s6` services. It also needs an unattended install driver, as a
     `river` subcommand in Go (shell ratchet), and `--profile base` on `cloud-image.sh`.
   - The base image should be small. `cloud-image.sh` refuses disks under 64 GiB because
     that is the node installer's minimum. A base image should be about 10 GiB, since the
     image size is the smallest boot disk a customer can create. That needs a cloud-base
     minimum in the planner.
   - `AGENTS.md` §1 describes Runink River as a workstation with one in-tree profile. A
     headless cloud edition changes that scope and is a TSC decision (GOVERNANCE.md).

## 4. "A secure base image for agentic development", in config lines

### 4.1 What Runink River has today

Each line is in this repository today.

| Property | Where | Note |
|---|---|---|
| kexec disabled | `config.delta`: `--disable KEXEC`, `KEXEC_FILE`, `KEXEC_HANDOVER`; `config.require`: `KEXEC=n`; sysctl `kernel.kexec_load_disabled=1` | no kernel replacement at runtime |
| memory zeroed on alloc and free | `config.delta`: `INIT_ON_ALLOC_DEFAULT_ON`, `INIT_ON_FREE_DEFAULT_ON`; `config.require`: `INIT_ON_FREE_DEFAULT_ON=y` | |
| kernel self-protection | `config.delta`: `RANDOMIZE_KSTACK_OFFSET_DEFAULT`, `HARDENED_USERCOPY`, `STRICT_KERNEL_RWX`, `STRICT_MODULE_RWX`, `--disable DEVMEM`, `--disable PROC_KCORE`, `SECURITY_DMESG_RESTRICT`, `--disable SLAB_MERGE_DEFAULT` | |
| LSM stack | `config.require`: `LSM="landlock,lockdown,yama,integrity,bpf"` | **lockdown is loaded but not enforcing**: no `lockdown=` on the command line, and `MODULE_SIG_FORCE` is off (`docs/KERNEL.md`). Landlock is available to processes |
| module signing | `config.delta`: `MODULE_SIG`, `MODULE_SIG_ALL`, `MODULE_SIG_SHA512`, per-build key | signed, not forced |
| sysctl hardening | `iso-profiles/river/root-overlay/etc/sysctl.d/10-runink-hardening.conf`: `kernel.kptr_restrict=2`, `kernel.yama.ptrace_scope=1`, `net.core.bpf_jit_harden=2`, `kernel.perf_event_paranoid=2`, `fs.protected_*`, no redirects or source routing | |
| sandbox for untrusted code | `/usr/local/bin/river-sandbox` (bubblewrap, unprivileged), invariant 8 | an agent's tool calls can run under it today |
| minimal init | s6 and s6-rc; the cloud services are four small definitions in `installer/cloud/s6/` | no systemd |
| ZFS boot environments | root is `<pool>/ROOT/runink` (`12-cloud-disk`) | `zfs snapshot` and rollback work. A pre-update snapshot and rollback command is **to build** |
| signed package updates | `[runink]`: `SigLevel = Required DatabaseRequired`, release key in `KEYS` ([REPOSITORY.md](../REPOSITORY.md)); distribution repositories `SigLevel = Required` (`pacman/pacman.conf.in`) | see section 6 |
| key-only SSH, no root login, no default password | section 2.7 | |
| no telemetry, no Python | AGENTS.md §5 (`river lint python-purge`) | |

### 4.2 To build, each its own change with a test

- **Agent sandboxes:**
  - Landlock and seccomp policies for agent processes. `river-sandbox` gives namespaces and
    a deny-list; it applies no Landlock ruleset and no seccomp filter.
  - A per-agent cgroup with memory, CPU and PID limits.
- **An agent user** with no sudo by default. The admin keeps `runink`; agents get an
  unprivileged account whose home is its own ZFS dataset, snapshotted before each task.
- **Rootless podman.** Invariant 5 and `forbidden.explicit` forbid a container runtime in the
  base image today (`podman docker docker-compose podman-compose`). Shipping it in the cloud
  edition changes an invariant and needs a TSC vote. The alternative is `river-sandbox` alone.
- **Audit trail:**
  - the kernel has `CONFIG_AUDIT=y` and `CONFIG_AUDITSYSCALL=y`, but no audit daemon or rules
    ship;
  - an append-only log of every agent command (exec and network) shipped off the instance.
- **Enforcing lockdown and signed boot:** `lockdown=integrity`, `MODULE_SIG_FORCE` and our
  own Secure Boot `db` (section 2.2).
- **Reproducible, attested images:**
  - The ISO release already has SBOMs, provenance and a signed `SHA256SUMS` through
    `release-gate.yml` ([RELEASE.md](../../RELEASE.md)), but is not yet reproducible
    (`docs/BUILD.md`, "Reproducibility status").
  - The cloud image needs the same: an SBOM of `disk.raw`'s package database, a provenance
    attestation, and a signature over the `.tar.gz` checksum.
- **A CIS-style benchmark:** a published list of checks with an `assert-golden.sh`-style
  runner on a booted instance, and its results with each image.

### 4.3 Compared with Amazon Linux 2023 and Container-Optimized OS

| | Amazon Linux 2023 | Container-Optimized OS | Runink River (today) |
|---|---|---|---|
| Mandatory access control | SELinux; "SELinux is enabled and set to permissive mode" ([AWS](https://docs.aws.amazon.com/linux/al2023/ug/selinux-modes.html)) | Chromium OS LSMs ([COS security](https://docs.cloud.google.com/container-optimized-os/docs/concepts/security)) | Landlock, Yama and BPF LSM available; no MAC policy enforced |
| Root filesystem | read-write | "root filesystem is always mounted as read-only"; verified at boot ("checksum … verified by the kernel on each boot") | read-write ZFS boot environment; snapshots, not verity |
| Secure Boot | supported from 2023.1: "accept only kernel level code… signed by Amazon" ([AWS](https://docs.aws.amazon.com/linux/al2023/ug/uefi-secure-boot.html)) | "UEFI Secure Boot is not enabled on Container-Optimized OS VMs by default" ([COS](https://docs.cloud.google.com/container-optimized-os/docs/concepts/auto-update)) | off; own `db` to build |
| Package manager | dnf, versioned repositories "turned on by default" ([AWS](https://docs.aws.amazon.com/linux/al2023/ug/deterministic-upgrades.html)) | "does not include a package manager" | pacman, signed; pinned snapshot at build |
| Updates | in place, deterministic | image-based; automatic updates "disabled by default" since M117 | in place (`pacman -Syu`) plus a new image per release |
| Kernel live patching | yes ([AWS](https://docs.aws.amazon.com/linux/al2023/ug/live-patching.html)) | no | no (kexec and livepatch off by design) |
| FIPS | opt-in mode; certification status not covered by that page | no | no |
| Metadata service | IMDSv2-only by default | Google guest agent | `river-cloud-init`; header-checked, never through a proxy |
| Init | systemd | systemd | s6 |
| Kernel hardening by config | distribution defaults | IMA, audit, KPTI | kexec off, init-on-free, lockdown LSM loaded, signed modules (section 4.1) |
| Support term | "supported until June 30, 2029" ([AWS](https://docs.aws.amazon.com/linux/al2023/ug/release-cadence.html)) | per milestone | community; no published support term yet |

**What they have that Runink River lacks:**

- an enforced MAC policy (AL2023 ships it permissive; COS enforces its LSMs);
- a read-only, verified root (COS);
- Secure Boot with a vendor key (AL2023);
- live patching and FIPS mode (AL2023);
- a published support lifecycle;
- the Google guest agent with OS Login (COS).

**What Runink River has that they do not, by config:**

- kexec removed from the kernel;
- init-on-free and other hardening options in a kernel built from a pinned source;
- an unprivileged sandbox for untrusted code as an invariant;
- ZFS boot environments and snapshots;
- no systemd, no Python and a small first-boot agent you can read in one sitting.

None of this is a certification. Claims stay at "config line X is set".

## 5. Paths to "available in GCP", effort and cost

### 5.1 The paths

**(a) Public image project.** This is how community images are published:

- Fedora CoreOS: "FCOS images are published under the `fedora-coreos-cloud` project"
  ([provisioning on GCP](https://github.com/coreos/fedora-coreos-docs/blob/main/modules/ROOT/pages/provisioning-gcp.adoc)).
- AlmaLinux: "Images are published by the AlmaLinux OS Foundation under the almalinux-cloud
  project" ([wiki](https://wiki.almalinux.org/cloud/Google.html)).
- The Arch Linux community image is `--image-project=arch-linux-gce --image-family=arch`, and
  Google "does not provide any support"
  ([builder](https://github.com/GoogleCloudPlatform/compute-archlinux-image-builder)).

Mechanics, from [managing access to custom
images](https://docs.cloud.google.com/compute/docs/images/managing-access-custom-images):

- Grant `roles/compute.imageUser` to `allAuthenticatedUsers` **per image**. "On images, you
  can't grant roles to the special member type" `allUsers`.
- A project-level grant "lets users delete all images", so it must never be used.
- Keep images in "a single project dedicated to host these images".
- "Publicly shared images do not appear in the images list for users"; users name the
  project and family.
- "An image family always points to the latest version of an OS image that is not
  deprecated" ([images](https://docs.cloud.google.com/compute/docs/images)).
- Rollback is deprecating the newest image; "Deprecating an image is irreversible"
  ([deprecate](https://docs.cloud.google.com/compute/docs/images/deprecate-custom)).

Google's own list of community-supported images is Fedora Cloud, FreeBSD and openSUSE. We
found no documented programme to be added to that list. Being listed would be a conversation
with Google, not a form.

**(b) Free Marketplace VM listing.**

- Pricing "Free" is allowed: customers "only pay for the Google Cloud resources that they
  use" ([pricing models](https://docs.cloud.google.com/marketplace/docs/partners/vm/select-pricing)).
- Prerequisites ([get started](https://docs.cloud.google.com/marketplace/docs/partners/get-started)):
  - "Your organization must join and maintain good standing in Google Cloud Partner
    Network";
  - a production-ready product with support;
  - Google verifies the product is hosted primarily on Google Cloud.
- The VM image guide ([build your VM image](https://docs.cloud.google.com/marketplace/docs/partners/vm/build-vm-image))
  conflicts with Runink River in two places:
  - "Use one of Google's supported base public images to create a VM";
  - "Verify that Python 2.6 or greater is installed".
- A listing of an OS that is not built on a Google base image needs Google's **written**
  acceptance. That is the open question. Without it, (b) is blocked however good the image
  is.

**(c) Both.** Do (a) first, then (b) once Google answers. (a)'s images, test record and
monthly cadence are what (b)'s review asks for.

| | (a) public image project | (b) Marketplace, free | (c) both |
|---|---|---|---|
| Owner prerequisites | a GCP project with billing; a bucket; the IAM binding per image | (a) plus a Partner Network membership, a Producer Portal listing, Google's written answer on a non-Google base OS, a support channel | all of both |
| Engineering effort | section 3's to-build list, then a publish runbook (section 7) | (a) plus listing assets, the licence string on each image (`--licenses`), a deployment test, monthly re-publish ("re-publish images once every month") | (a) then (b) |
| Gate outside our control | none | Google's acceptance | Google's acceptance, for (b) only |
| Recommendation | **do now** | after Google answers | the end state |

### 5.2 Cost

[Disk and image pricing](https://cloud.google.com/compute/disks-image-pricing) lists custom
image storage at "$0.000068493 / 1 gibibyte hour" (Iowa). That is about **$0.050 per
GiB-month** (× 730 h). "The cost of these custom images depends on the location where you
store the image." Images are not in the [free
tier](https://docs.cloud.google.com/free/docs/free-cloud-features), which covers "30
GB-months standard persistent disk", not images.

| Image versions kept | Billed on a ~3 GB archive | If billed on a 10 GiB disk | If billed on a 64 GiB disk |
|---|---|---|---|
| 1 | $0.15 / month | $0.50 | $3.20 |
| 3 | $0.45 / month | $1.50 | $9.60 |
| 6 | $0.90 / month | $3.00 | $19.20 |
| 12 (a year, monthly) | $1.80 / month | $6.00 | $38.40 |

- Whether Google bills on the archive size (`archiveSizeBytes`) or the disk size is **not
  stated** on the pricing page. A community answer says archive size. Check the first
  month's invoice and correct this table.
- The 64 GiB column is why the base image should be about 10 GiB (section 3).
- **Upload bucket:** about 3 GB for minutes. Cloud Storage Standard is about $0.020 per
  GiB-month, and the free tier includes "5 GB-months of regional storage (US regions only)".
  Delete the object after `images create`. Cost: **$0**.
- **Customer usage:** "There are no network transfer fees for creating images, nor for
  creating disks from images." The publisher pays only for storage. Customers pay for their
  own instances and disks.
- **Marketplace:** a free listing has no listing fee from Google that we found. Its cost is
  the Partner Network membership terms, which are not priced on the pages read, so ask
  Google.

**Bottom line:** keep three versions of a ~3 GB image for **about $0.45 a month**. That is
not zero, because images are outside the free tier. It stays under $1 a month as long as
images are billed on their archive size and old versions are deprecated and deleted.

## 6. The update channel

A Runink River instance on Compute Engine gets updates two ways:

1. **In place:** `pacman -Syu` against signed repositories.
   - `[runink]` (kernel, ZFS, Runink River packages) uses `SigLevel = Required
     DatabaseRequired` and is signed offline with the release key in `KEYS`
     ([REPOSITORY.md](../REPOSITORY.md)).
   - The distribution repositories use `SigLevel = Required`.
   - A tampered or unsigned package fails the update.
   - **To build:** a ZFS snapshot of the boot environment before each upgrade, and
     `river rollback` to boot the previous one. The boot environment layout already allows
     it.
2. **New image:** each signed `runink-os-*` release publishes a new image into the family
   and deprecates the oldest beyond N.
   - New instances get the new image.
   - Existing instances are not reimaged, as on every public image.
   - The image name carries the release and date: `runink-river-v<yyyymmdd>-x86-64`.
   - The family is `runink-river` (and `runink-river-arm64` once an aarch64 image exists;
     the kernel already builds for aarch64).

There is no automatic, unattended upgrade on the instance. That is deliberate for a host
whose operator decides when a kernel changes. An opt-in s6 timer is **to build**.

## 7. Publishing runbook (owner; nothing here is run by an agent)

Values in `<>` are the owner's. The image name and family are proposals.

```sh
IMG=runink-river-v20261201-x86-64          # one name per build, never reused
PROJECT=<image-project>                    # dedicated: images only, nothing else
BUCKET=<upload-bucket>                     # in a US region (free tier), object deleted after import
sha256sum -c "$IMG.tar.gz.sha256"
gcloud storage cp "$IMG.tar.gz" "gs://$BUCKET/$IMG.tar.gz"
gcloud compute images create "$IMG" --project="$PROJECT" \
  --source-uri="gs://$BUCKET/$IMG.tar.gz" \
  --family=runink-river --architecture=X86_64 \
  --guest-os-features=UEFI_COMPATIBLE,GVNIC,VIRTIO_SCSI_MULTIQUEUE \
  --description="Runink River $IMG (UEFI, ZFS, s6). Free; community supported."
gcloud storage rm "gs://$BUCKET/$IMG.tar.gz"
# test before sharing: a Shielded VM (vTPM, integrity monitoring), on gVNIC and on an NVMe series
gcloud compute instances create river-check-1 --project="$PROJECT" --zone=<zone> \
  --machine-type=c3-standard-4 --image="$IMG" --shielded-vtpm --shielded-integrity-monitoring \
  --network-interface=nic-type=GVNIC --metadata=block-project-ssh-keys=TRUE,ssh-keys="runink:$(cat ~/.ssh/id_ed25519.pub)"
gcloud compute instances get-serial-port-output river-check-1 --zone=<zone> | grep RUNINK-HEALTH
# share THIS image publicly (never a project-level binding)
gcloud compute images add-iam-policy-binding "$IMG" --project="$PROJECT" \
  --member=allAuthenticatedUsers --role=roles/compute.imageUser
# retire the oldest beyond N=3
gcloud compute images deprecate <oldest> --project="$PROJECT" --state=DEPRECATED --replacement="$IMG"
```

Users then run:

```sh
gcloud compute instances create my-river --image-family=runink-river --image-project=<image-project> \
  --metadata=ssh-keys="runink:$(cat ~/.ssh/id_ed25519.pub)"
```

Secure Boot, once section 2.2 is built: add
`--signature-database-file=<runink-river-db.der>` (and, if we own the full chain,
`--platform-key-file` and `--key-exchange-key-file`) to `images create`. Drop
`--no-shielded-secure-boot` from instance commands.

## 8. What phase 1 proves, and how

- **Unit tests** (`installer/`, run with `-race`):
  - `TestWaitForChange` and `TestFileHandlerFollowsFile`: the metadata long-poll and its
    stand-in;
  - `TestWatchLoop`: keys applied at start and after a change, a failed apply retried, no
    apply while metadata is unchanged;
  - `TestProfile`;
  - `TestCloudS6Services`: every s6 definition is well-formed, calls a subcommand that
    exists, and depends only on known services.
- **A boot test that emulates Compute Engine.** It used OVMF (UEFI), the disk on NVMe and on
  virtio-scsi (vendor `Google`, product `PersistentDisk`), a virtio-net NIC, the serial
  console to a file, and `installer/fakemeta` at `169.254.169.254:80` through QEMU's
  `guestfwd`. The disk was a GCE-format `disk.raw` (GPT, ESP, a whole number of GiB, archived
  with `--format=oldgnu`) holding:
  - this branch's `river-cloud-init` and `installer/cloud/s6` services;
  - the `linux-runink` 7.2.7 package built from this tree's PKGBUILD;
  - the distribution's s6 userland, with sshd and the serial getty configured exactly as
    `78-cloud-target` writes them.

  It proved:
  - SSH-key login from instance metadata;
  - the hostname from metadata;
  - the project key blocked by `block-project-ssh-keys`;
  - root login and password login refused;
  - a key added to metadata accepted, and a removed key refused, without a reboot;
  - the serial getty;
  - `RUNINK-HEALTH ready`.

  Measured on 2026-11-23, both controllers:
  - 10 of 10 checks passed;
  - `RUNINK-HEALTH ready` reached 15 s after boot on NVMe and 55 s on virtio-scsi, on a
    loaded build host;
  - key additions and removals in metadata took effect within 3 s.

  OVMF booted `UEFI QEMU NVMe Ctrl` and `UEFI Google PersistentDisk`. The guest had `nvme`,
  `virtio_net`, `virtio_scsi` and `gve` loaded. The fixture's `.tar.gz` (494 MB, holding only
  `disk.raw`) is the format section 2.1 describes.

  The built `linux-runink-headers` 7.2.7 package's `.config` has `KVM_GUEST=y`,
  `PARAVIRT_CLOCK=y`, `VIRTIO_PCI=y`, `SCSI_VIRTIO=y`, `PCI_MSI=y`,
  `SERIAL_8250_CONSOLE=y`, `TCG_TPM=y`, `VIRTIO_NET=m`, `BLK_DEV_NVME=m` and `GVE=m`. It
  also has `KEXEC` and `KEXEC_FILE` not set, `INIT_ON_FREE_DEFAULT_ON=y`, and
  `MODULE_SIG_FORCE` not set.

  The test disk's root was **ext4**, assembled without root privileges, because creating a
  ZFS pool needs root. Disk growth (ZFS only) was therefore reported as
  `skipped-not-zfs`, and the ZFS-specific parts were not exercised. The full image path
  (`cloud-image.sh` → `cloud-image-test.sh`) needs the in-tree cloud edition (section 3) and
  an ISO, whose last build step needs root once.
- **Not proved:** anything on real Compute Engine (gVNIC, Shielded VM, a real metadata
  server, a real resize). Section 7's test instance is that proof.
