<!--
SPDX-FileCopyrightText: 2026 The RIVER Authors
SPDX-License-Identifier: CC-BY-4.0
-->

# Runink River cloud images (Compute Engine)

> **Scope.** A cloud image is a server image. Since 2026-09-26 Runink River is a workstation
> and the server profile lives in a downstream distribution: `build/cloud-image.sh` builds an
> image from a server ISO built from that profile, and takes the profile's cloud installer
> overlay from `RIVER_PROFILE_DIR`. "Runink River Server" below means that downstream image.

Runink River Server ships as an installer ISO. A cloud image is the same system, installed
once onto a raw disk by the same installer in a **cloud mode**, and packaged the way a
cloud imports a disk. Many instances boot from one image, with no console and no
installer, so three things differ from an installed node: who types the disk key (nobody),
where the per-machine identity comes from (the instance, at first boot), and where the
configuration comes from (the metadata server).

Compute Engine (GCE) is the first target. AWS and Azure are planned with the same pipeline
([Other clouds](#other-clouds)).

> **Status (2026-09-25).** Built and tested in QEMU as a GCE stand-in
> (`build/cloud-image-test.sh`), not yet on Compute Engine. The Secret Manager key provider
> works; the **vTPM key provider is not implemented yet** ([vTPM: next](#vtpm-next)).
> [What is not verified](#what-is-not-verified) lists the rest.

## At a glance

| | |
|---|---|
| Build | `build/cloud-image.sh ISO`: boots the server ISO in QEMU/KVM (UEFI), runs `runink-autoinstall --cloud gce` onto a raw disk, packages it. Rootless. |
| Artifact | `<name>.tar.gz` holding only `disk.raw` (GNU tar `--format=oldgnu`, sparse, gzip), plus its sha256 and `image.env` |
| Disk | GPT, UEFI. ESP 1 GiB (FAT32, `/boot`) + one ZFS partition. 64 GiB image; the pool grows to the instance's disk at boot |
| Boot environment | ZFS, **not encrypted**: public MIT/GPL/CDDL code and public configuration only |
| Node data | ZFS `<pool>/data`, **aes-256-gcm**, per-instance key made at first boot, wrapped by a key provider |
| Key providers | Secret Manager (working), vTPM (next) |
| First boot | `river-cloud-init`: a Go standard-library binary and three s6 oneshots. No guest agent, no cloud-init, no Python |
| Models | the encrypted model payload travels in the image as ciphertext and is unpacked per instance ([Model payload](#model-payload)) |
| Test | `build/cloud-image-test.sh IMAGE_DIR`: QEMU + OVMF + NVMe + user-mode NIC (IPv4 and IPv6) + a fake metadata server |

## The GCE image format

Google's requirements for a manually built image
([Import boot disk images to Compute Engine manually](https://docs.cloud.google.com/compute/docs/import/import-existing-image),
fetched 2026-09-25):

- "The disk image filename must be `disk.raw`."
- "The RAW image file must have a size in an increment of 1 GB." `cloud-image.sh` takes the
  size in whole GiB and checks it.
- "The compressed file must be a `.tar.gz` file that uses gzip compression and the
  `--format=oldgnu` option for the `tar` utility." The script runs
  `tar --format=oldgnu -Sczf <name>.tar.gz disk.raw`; `-S` stores the unwritten parts of the
  64 GiB file as holes, so the archive holds only what the install wrote.
- The same page asks for `qemu-img check` before compressing. That check exists for qcow2
  and similar formats; a raw file has no metadata to check (`qemu-img` says so), so the
  script prints `qemu-img info` and checks the size instead.
- Boot loader: "Add `console=ttyS0,38400n8d`", and no `quiet`, `rhgb` or `splashimage=`.
  The cloud mode sets exactly that console, and the server's kernel command line has no
  splash or quiet.
- The page describes the **BIOS** layout ("a functional MBR partition table or a hybrid
  configuration of a GPT partition table with an MBR bootloader"). This image is **UEFI
  only**, like every Runink River node: GPT with an EFI system partition, GRUB installed to
  the removable path `EFI/BOOT/BOOTX64.EFI`. Compute Engine boots such an image when it is
  created with the `UEFI_COMPATIBLE` guest OS feature, which the commands below set.

Guest OS features ([Create custom images](https://docs.cloud.google.com/compute/docs/images/create-custom),
[gcloud compute images create](https://docs.cloud.google.com/sdk/gcloud/reference/compute/images/create)):
only what the image supports is declared.

| Feature | Declared | Why |
|---|---|---|
| `UEFI_COMPATIBLE` | yes | UEFI-only image; also what Shielded VM requires |
| `GVNIC` | yes | `gve` is built as a module and put in the initramfs by the cloud mode. Not exercised in QEMU (no gVNIC device there) |
| `VIRTIO_SCSI_MULTIQUEUE` | yes | `virtio_scsi` is built into linux-runink; multiqueue is the driver's default |
| `SEV_CAPABLE`, `SEV_SNP_CAPABLE`, `TDX_CAPABLE` | **no** | the kernel has `AMD_MEM_ENCRYPT`, `SEV_GUEST` and `INTEL_TDX_GUEST`, but no Confidential VM boot has been tried |
| `IDPF` | no | bare metal and Cloud RDMA only |

Shielded VM: vTPM and integrity monitoring can be on. **Secure Boot must be off**: GRUB and
the kernel are not signed by a key in the Shielded VM default database (Microsoft UEFI CA),
because Runink River does not ship a signed shim ([SECURE-BOOT.md](SECURE-BOOT.md)).

Drivers Google lists for custom images
([Building custom operating systems](https://docs.cloud.google.com/compute/docs/images/building-custom-os)):
`CONFIG_KVM_GUEST=y`, `CONFIG_VIRTIO_PCI=y`, `CONFIG_SCSI_VIRTIO=y`, `CONFIG_PCI_MSI=y`
(all built in); `CONFIG_VIRTIO_NET`, NVMe ("required for ... all third generation and
later machine series") and gVNIC ("required for ... Tier_1 networking") are modules, so
installer step `78-cloud-target` adds `nvme virtio_net gve` to the initramfs and fails the
build if any is missing. The build machine boots from virtio-scsi and the test from NVMe,
which is what proves the initramfs is not tied to the build machine.

## Building

```sh
build/cloud-image.sh ~/.cache/river-build/iso-out/runink-river-server-<date>-x86_64.iso
build/cloud-image-test.sh ~/.cache/river-build/cloud-out/runink-river-server-x86-64-<date>
```

The script overlays this checkout's cloud installer on the live system (the installer
steps, `runink-autoinstall`, `river-perms`, `river-cloud-init` and its s6 services; the list
is in `image.env` as `OVERLAY`), so an image is the ISO's system plus the checkout's cloud
mode. An ISO built after this change carries all of them itself (`river-cloud-init` is in
the `runink-installer` package).

Options: `--no-models` (an image without the model payload), `--disk-size 96G`,
`--name`, `--expect-payload none|present`, `--keep`, `--dry-run`. It needs `/dev/kvm`,
OVMF, `go`, GNU tar, and about 40 GB free under `~/.cache` with the model payload.

### Building in GitHub Actions

`.github/workflows/images.yml` builds the **public, payload-free** artifacts on
GitHub-hosted `ubuntu-24.04` runners (this public repository never uses a self-hosted runner;
the workflow header lists how each job fits a standard runner's 4 vCPU, 16 GB of RAM and
~14 GB of free disk): the server ISO, the workstation ISO (both with
`MODEL_PAYLOAD=no`), and the GCE base image (`cloud-image.sh --no-models --expect-payload
none`, then `cloud-image-test.sh`). Manual dispatch or a pushed `runink-os-*` tag; never on
pull requests, only in `org-runink/river`, actions pinned by SHA, `contents: read`, no
secrets, artifacts kept 3 to 7 days. A public image carries no payload of any kind: `cloud-image.sh` adds
`--cloud-no-models` itself when the ISO has no downstream payload, and fails an image without
one that still carries models ([PAYLOADS.md](PAYLOADS.md)). The commercial image (a downstream payload and the
model payload) is never built there: the private downstream builds it on its own runner.

| Job | What | Constraint and how it fits |
|---|---|---|
| `kernel` | `linux-runink` + `runink-zfs` in rootless podman, verified | ~4 h on 4 vCPU (6 h limit); ~30 GB of build tree under `/mnt`, which is on the root disk: `build/ci-runner-prep.sh 30` removes unused preinstalled toolchains first and fails at once without 30 GB free. No `actions/cache` (a cache another run wrote must not feed a release; zizmor `cache-poisoning`). The per-build module-signing private key never leaves the job: the artifact carries only its certificate |
| `iso` (matrix: server, workstation) | `build/local-iso.sh` rootless, then `sudo sh build/iso-root-stage.sh` (buildiso needs root; the runner provides passwordless sudo) | podman stores and `XDG_CACHE_HOME` on `/mnt` (`build/ci-runner-prep.sh 20`, which removes ~25 GB of preinstalled toolchains, checks for 20 GB free, and installs `repo-add` from Ubuntu's `pacman-package-manager`) |
| `gce` | QEMU/KVM install and test | `/dev/kvm` via GitHub's documented udev rule; VM 6 GiB of the runner's 16 GB; Ubuntu's OVMF paths passed as `OVMF_CODE`/`OVMF_VARS` |
| `publish` | Releases | disabled: needs `RIVER_RELEASE_PUBLISH=true` and fails until the release key and signing exist ([RELEASE-SIGNING.md](RELEASE-SIGNING.md)) |

**Not run yet.** The workflow passes actionlint and zizmor but has not run on a CI
runner. What may not fit, and the fallback for each:

- `repo-add` on Ubuntu: if `pacman-package-manager` does not provide a working `repo-add`,
  run stages 4 and 5 of `local-iso.sh` inside the Artix builder container instead.
- The ISO job's disk: locally the rootless state of a server build is ~29 GB, of which
  ~17 GB is the model payload this workflow does not build, plus buildiso's work tree;
  not measured on a runner. If 20 GB is short, the job stops at its disk check; build the ISO
  locally with `build/local-iso.sh`.
- Time: the kernel job is the long pole (docs/BUILD.md: about an hour at `-j8`; a 4-vCPU
  runner roughly doubles it; `kernel-build.yml` took ~4 h 10 min on one). If it passes the
  6 h job limit, build the kernel locally (`build/build-kernel-zfs.sh`).

### Downstream payloads and private images

A downstream platform payload (`RIVER_PAYLOAD_DIR`, [BUILD.md](BUILD.md#downstream-payloads))
comes in with the ISO: build the ISO with the payload, then build the image from that ISO,
in the downstream's own private build. `cloud-image.sh` reports what the ISO carried:

- **No payload**: a plain Runink River image, publishable.
- **A payload**: the image name ends in `-payload`, `image.env` says `PRIVATE=1`, and a
  `PRIVATE-DO-NOT-PUBLISH` file sits beside it. Such an image is private: never upload it
  to a public bucket, mirror or release, only into the downstream's own project.
  `--expect-payload present` makes the build fail if the ISO does not carry one.

The script uploads nothing and has no publish option (`--publish*` and `--upload*` are
refused).

## The cloud mode in the installer

`runink-autoinstall --cloud gce` (or `RUNINK_CLOUD=gce`) runs a different step list:

| Step | Node install | Cloud image |
|---|---|---|
| disk | `10-disk-zfs`: encrypted pool root | `12-cloud-disk`: plain pool, `autoexpand=on`, BE only |
| models | `72-models-payload`: unpack now | `74-cloud-payload`: copy the ciphertext into `<pool>/payload` |
| plan | `75-install-plan`: this machine's plan | skipped (the build VM is not the instance); `river-cloud-init` writes the instance's |
| cloud | none | `78-cloud-target`: serial console, initramfs drivers, NIC, SSH, k0s node address, enrollment hook |
| finalize | none | `85-cloud-finalize`: cloud services in the boot database, identity cleared |

The other steps (`00`, `05`, `20`, `30`, `40`, `50`, `80`, `90`) are the node's, unchanged.
The cloud steps live only in the server profile and `installer/lib`; the workstation
profile is not affected.

The per-cloud values are one function per cloud in `78-cloud-target` (`cloud_gce`: serial
device and speed, initramfs modules, metadata address); `12-cloud-disk`,
`runink-autoinstall` and `cloud-image.sh` refuse any cloud without one. `aws` and `azure`
are recognised and refused as "planned".

## Disk layout and size

```
<disk>1  ESP, FAT32, 1 GiB                  /boot   kernel, initramfs, GRUB
<disk>2  zriver (autoexpand=on)
         ├─ zriver/ROOT/runink              /       NOT encrypted (public code and config)
         ├─ zriver/payload                  (image) the model payload ciphertext; destroyed after unpacking
         └─ zriver/data                     aes-256-gcm encryption root, per-instance key
            ├─ etc-runink                   /etc/runink
            ├─ node-state                   /var/lib/runink
            ├─ home                         /home
            ├─ state                        /var/lib/core
            │  └─ (models)                  /var/lib/core/models/shared  (zriver/data/models)
            ├─ containers                   /var/lib/k0s
            └─ appfs                        not mounted: parent for per-application roots
```

**Size: 64 GiB.** That is the install planner's smallest target disk
(`MinTargetDiskGiB`), and it leaves room to unpack the model payload next to its own
ciphertext on first boot (about 16.3 GiB of payload plus 18.5 GiB of models). The archive
holds only written data, so its size does not grow with the disk's.

**Growing.** At every boot `river-cloud-init` compares the ZFS partition's end with the
disk's (sysfs), and when there is more than 1 GiB left it moves the backup GPT to the end
(`sgdisk -e`), recreates the partition from the same first sector to the end with the same
type, unique GUID and name, tells the kernel (`partx -u`), and runs
`zpool online -e zriver <partition>`. Resizing the instance's boot disk later
(`gcloud compute disks resize`) takes effect at the next boot.

**A separate data disk** (`runink-data-device-name`). Attach a persistent disk with a
`device_name` (for example `runink-data`) and set the instance attribute
`runink-data-device-name=runink-data`. At first boot `river-cloud-init` finds the disk
without Google's udev rules (the image does not ship them):

1. `/dev/disk/by-id/google-<name>` or `/dev/disk/by-id/scsi-0Google_PersistentDisk_<name>`
   when a udev rule made one;
2. SCSI (virtio-scsi) disks in sysfs with vendor `Google`, model `PersistentDisk` and the
   device name as unit serial number (VPD page 0x80);
3. NVMe namespaces: the device name is in the JSON GCE writes to the vendor-specific area of
   Identify Namespace (bytes 384 onward), read with one `NVME_IOCTL_ADMIN_CMD`, as Google's
   own `google_nvme_id` does.

A **blank** disk (no partition table or filesystem signature, `blkid -p`) becomes the pool
`zriver-data` (whole disk, `autoexpand=on`) and the data datasets are created on it as
`zriver-data/data`. A disk that already carries `zriver-data` is imported, so a data disk
moved to an instance booted from a newer image keeps its data: the sealed data key is
stored **on the encryption root itself** (a ZFS user property), not on the boot disk. A disk
that is neither is never formatted, and the boot stops before the stack starts (fail
closed; it never falls back to the boot disk when a data disk was asked for). The SCSI path
is tested in QEMU (`cloud-image-test.sh --data-disk`); the NVMe path is not.

## Network

Compute Engine subnets are IPv4, optionally dual-stack; an instance's NIC gets IPv4 from
DHCP and, on a dual-stack subnet, one IPv6 address by DHCPv6. Runink River's fabric is
IPv6-only (a downstream server distribution's invariant), so the cloud mode keeps the cluster IPv6-only and takes whatever
the NIC is given:

- **The NIC** (`/etc/NetworkManager/conf.d/90-river-cloud.conf`): `ipv4.method=auto`,
  `ipv6.method=auto`, and `ethernet.cloned-mac-address=permanent`, because the VPC
  forwards only the instance's own MAC (the node default spoofs it). An IPv4-only subnet
  and a dual-stack one both work.
- **The cluster address.** k0s's pod and service CIDRs stay IPv6 ULA ranges. The node's
  cluster address is a ULA, `fd52:6976:6572:1::1`, on a dummy interface `river0`
  (NetworkManager keyfile). `api.address` in `/etc/k0s/k0s.yaml` and kubelet's `--node-ip`
  use it, so kube-apiserver never picks the NIC's IPv4 address; that mismatch ("service IP
  family must match public address family") is what stopped the apiserver in the earlier VM
  test. The address is host-local: every instance uses the same one and it is never routed
  off the instance. A multi-node cloud cluster needs routed IPv6 between nodes (a
  dual-stack subnet) and is not covered yet.
- **Egress** for pods goes through the image's NAT64 (tayga) to IPv4 destinations, and
  CoreDNS forwards to the instance's resolver through `64:ff9b::/96`. Not exercised in the
  test beyond k0s pulling its images, which the host does over IPv4.
- **SSH** listens on both families (`05-river-cloud.conf`, `AddressFamily any`).
- **Firewall.** Compute Engine's VPC firewall is the perimeter. `runink_fw` stays disarmed
  until enrollment arms it, as on any node. Armed, it takes IPv4 as well as IPv6 whitelist
  entries and accepts DHCPv4 replies, so an IPv4-only NIC keeps its lease.
- **IPv6-only instances.** Compute Engine offers IPv6-only instances only for some Google
  images (Debian, RHEL, Ubuntu), so this image is built for IPv4 and dual-stack NICs; an
  IPv6-only NIC would also work (the node needs no IPv4) but cannot be tested on GCE.
- **Time.** The closure has no NTP daemon; the instance keeps KVM's paravirtual clock.
  Google recommends `metadata.google.internal` as the NTP server
  ([Configuring imported images](https://docs.cloud.google.com/compute/docs/import/configuring-imported-images));
  adding a client is a follow-up for the own base.

## The metadata server and river-cloud-init

`river-cloud-init` (`installer/cloudinit`, MIT, Go standard library, `GOAMD64=v1`) talks to
`http://169.254.169.254/computeMetadata/v1` with `Metadata-Flavor: Google`, and refuses
any answer without that header. It runs as three s6 oneshots, enabled only on cloud images:

| s6 service | Depends on | Does |
|---|---|---|
| `river-cloud-identity` | `remount-root`, `mount-filesystems` | machine-id, SSH host keys, ZFS hostid (all cleared from the image) |
| `river-cloud-init` | `river-cloud-identity`, `NetworkManager-srv` | waits for metadata (5 min); hostname; SSH keys; grows the pool; creates or unlocks the data datasets (boot pool or data disk); stages enrollment, or writes the single-node k0s role. **`rc-local` depends on it**: if the data cannot be unlocked the stack does not start (fail closed) |
| `river-cloud-late` | `river-cloud-init` | starts, DETACHED, the work that must not hold the boot transaction (every getty waits for it): initramfs rebuilt for the new hostid, `zpool reguid`, the model payload, the instance's install plan |

### Metadata contract

These are the keys a deployment (for example a Terraform module or a Marketplace deployment
package) sets. Custom attributes are read from the instance first, then the project.

| Key | Set by | Use |
|---|---|---|
| `instance/id` | GCE | first boot on an instance is when it differs from `/var/lib/river-cloud/instance-id` |
| `instance/hostname` | GCE | hostname = its first label; `/etc/hosts` gets both |
| `ssh-keys` (instance) | deployment | every valid key becomes a key of the **runink** admin (the one account); expired `google-ssh` keys are skipped. Legacy `sshKeys` is read too |
| `block-project-ssh-keys` | deployment | `TRUE` (any case): project-level `ssh-keys` are ignored, only the instance's apply. Recommended |
| `serial-port-enable` | deployment | a GCE platform switch for the INTERACTIVE serial console. The image logs the boot to ttyS0 and runs a getty there either way; `FALSE` only stops anyone from typing into it (`get-serial-port-output` still works). Recommended `FALSE` |
| `instance/service-accounts/default/token` | GCE | the bearer token for Secret Manager, in memory only |
| `project/project-id` | GCE | resolves a bare secret id to `projects/<project-id>/secrets/<id>` |
| `runink-key-secret` | deployment | the data-key wrapping secret: a secret id (`river-kek`) or a full name (`projects/P/secrets/S[/versions/V]`) |
| `runink-data-device-name` | deployment | the `device_name` of an attached data disk; the data datasets go there ([Disk layout](#disk-layout-and-size)) |
| `runink-enrollment-secrets` | deployment | comma-separated secret ids (or names). Each secret's payload is an `enrollment.env` fragment (`KEY=value` lines, [INSTALL.md](INSTALL.md)); they are fetched with the service account and staged, in order, into `/etc/runink/enrollment.env` (0600) for `runink-firstboot.sh`, which consumes and removes it. All or nothing: one unreadable secret stages nothing, and the next boot tries again |
| `runink-enrollment` | deployment | a non-secret `enrollment.env` body, staged before the secrets' fragments |
| `runink-models-passphrase-secret` | deployment | the model payload passphrase, as a secret id or name |
| `runink-models-passphrase` | deployment | the passphrase itself (weaker: every metadata reader sees it) |
| `runink-secretmanager-endpoint` | test only | accepted only as `http://169.254.169.254/...` (the fake metadata server of the cloud test). The token is only ever sent back to the host that issued it |

The instance's service account needs `roles/secretmanager.secretAccessor` on
`runink-key-secret`, each `runink-enrollment-secrets` entry and
`runink-models-passphrase-secret`, and nothing else.

SSH keys go to `/etc/ssh/authorized_keys.d/runink` on the boot environment (public keys,
root-owned 0644), which `05-river-cloud.conf` adds to `AuthorizedKeysFile`. That way the
admin can log in to repair a node whose data did not unlock. They are rewritten at every
boot; **keys added to metadata while the instance runs apply at the next boot** (the
guest agent watches metadata; `river-cloud-init` does not yet). Passwords are locked;
root cannot log in; login is key-only.

### Health on the serial console

A deployment test reads the instance's serial port output
(`gcloud compute instances get-serial-port-output`), so the image reports its state there:

| Stage | Serial console shows |
|---|---|
| `boot` | a `login:` prompt (the ttyS0 getty) and no `RUNINK-HEALTH failed` line |
| `ready` | `RUNINK-HEALTH ready (Runink River node ready)`, printed once by `river-cloud-late` when the staged enrollment (if any) has been consumed (`/var/lib/runink/.enrolled`), the data datasets are unlocked, the k0s node is `Ready`, and every executable in `/usr/local/share/runink/core/ready.d/` exits 0 |
| failure | `RUNINK-HEALTH failed (<reason>)`: the data datasets did not unlock, or a stage was not reached within 30 minutes |

`ready.d` is the downstream payload's hook: each executable is run (2 minutes each, retried
every 10 s) until it exits 0, so a payload can hold `ready` back until its own services
answer. A base image has none. The model payload is not part of readiness; it may still be
unpacking (`river-cloud-init status`).

## Encryption

### The model

- **The boot environment is not encrypted.** It holds only public code (MIT userspace,
  GPL-2.0-only kernel, CDDL ZFS) and public configuration, identical on every instance.
  There is nothing to protect, and encrypting it would need a key at the initramfs, where
  a cloud instance has no console.
- **Everything node-specific is encrypted**: `/etc/runink` (enrollment secrets, k0s role,
  install plan), `/var/lib/runink`, `/home` (the admin's and the runner's secrets),
  `/var/lib/core` (payload state, registry, models), `/var/lib/k0s` (cluster state,
  containerd) and future per-application roots under `appfs`. All are children of
  `<pool>/data` (`zriver/data` on the boot disk, or `zriver-data/data` on a data disk),
  one aes-256-gcm encryption root, marked `org.runink:role=cloud-data`.
- **The key is per instance.** `river-cloud-init` makes 32 random bytes at the instance's
  first boot. It is never written anywhere in the clear, never printed, and never leaves
  the instance; it reaches `zfs create` and `zfs load-key` on stdin (`keyformat=hex`).
- **A key provider wraps it.** The wrapped blob is stored as a ZFS user property of the
  encryption root itself (`org.runink:key.<provider>`, base64), so it travels with the data
  (a data disk moved to a new boot disk keeps it); without the provider it is useless. The key is sealed and the blob is **re-opened before** the
  encryption root is created, so a node never creates data it could not unlock after a
  reboot. With no working provider, the first boot stops before the stack starts, SSH
  stays up, and `sudo river-cloud-init boot` retries once a provider is configured.

This departs from invariant 3 ("new pools are an aes-256-gcm encryption root; every dataset
inherits it") **for cloud images only**, and needs a TSC vote ([GOVERNANCE.md](../GOVERNANCE.md)).
What stays true: every node secret and state path is encrypted at rest, under a key that
exists on no other machine. `tests/assert-golden.sh` accepts plain datasets on a cloud
image only for `<pool>`, `<pool>/ROOT*` and `<pool>/payload`, and requires `<pool>/data`
to be the encryption root.

Swap is zram (RAM). Compute Engine also encrypts every persistent disk at rest with
Google-managed or customer-managed keys; that is independent of this and does not protect
against anyone who can snapshot the disk inside the project.

### Key providers

`installer/internal/cloud/keyprovider.go`:

```go
type KeyProvider interface {
    Name() string
    Seal(ctx context.Context, key []byte) ([]byte, error)
    Unseal(ctx context.Context, blob []byte) ([]byte, error)
}
```

A blob names its provider (`{"v":1,"provider":...}`); several can coexist for one key, and
the unlock tries each. `ErrUnavailable` means "not on this instance, try the next".

**`gcp-secret-manager` (working).** The operator creates one secret of at least 32 random
bytes and grants the instance's service account `roles/secretmanager.secretAccessor` on it.
`river-cloud-init` reads it over HTTPS from userspace, after the network is up, with the
service-account token from the metadata server (no SDK, no credentials file), derives a
wrapping key with HKDF-SHA256 (random salt) and seals the data key with AES-256-GCM, the
dataset name as associated data. Seal pins the **resolved version** (`.../versions/3`), so
moving `latest` never strands a node. Rotation: add a new version, run
`sudo river-cloud-init reseal` on each instance, then disable the old version. The payload
CRC32C Secret Manager returns is checked.

What it protects against: a copy of the disk (snapshot, image, detached disk) outside the
project's IAM, and a stopped instance's disk read by someone without `secretAccessor`.
What it does not: anyone who can run code on the instance as root, or who holds
`secretAccessor` on the secret and a copy of the disk.

**`tpm2` / vTPM: next.** <a id="vtpm-next"></a> Registered, not implemented: it reports
`ErrUnavailable`. Sealing the data key to the Shielded VM vTPM under a PCR policy would
bind the blob to this VM and its measured boot, with no network. The image closure has
`libtss2-*` but no sealing tool (`tpm2-tools` is not in the closure and pulls in `curl`),
and `swtpm` was not available here to test one in QEMU. The plan is the one in
[ENCRYPTION.md](ENCRYPTION.md#follow-up-tpm2-unattended-unlock-for-the-own-userland-base):
a small pure-Go TPM2 client over `/dev/tpmrm0`. With Secure Boot off, PCR 7 is weak; PCRs
0, 2, 4 and 8/9 (boot loader and command line) are the candidates, at the cost of a reseal
on every kernel or GRUB update.

## First-boot sequence

1. Firmware boots `EFI/BOOT/BOOTX64.EFI` (GRUB) from the ESP; the kernel and initramfs are
   on the ESP. The initramfs imports `zriver` (`zfs_force=1`: the image's hostid is not the
   instance's) and mounts the plain boot environment. No key is asked for.
2. s6: `river-cloud-identity` writes a machine-id, generates SSH host keys and a ZFS hostid.
3. NetworkManager brings up the NIC (DHCP) and `river0`.
4. `river-cloud-init boot`: metadata; hostname; SSH keys; grow the partition and pool;
   first boot: generate the data key, seal it (Secret Manager), re-open the blob, create
   `zriver/data` and its children, move the image's content for those paths into them,
   write the single-node k0s role (`--node-ip`, cluster DNS, the image's CoreDNS) or stage
   `runink-enrollment` or `runink-enrollment-secrets`. Later boots: unseal and `zfs load-key`, `zfs mount -a`.
5. `rc-local` (released by step 4): sysctl, zram, firewall, `runink-firstboot.sh` (enrolls
   when `runink-enrollment` or `runink-enrollment-secrets` was given, else no-op), k0s single-node, the downstream payload
   deploy if the image carries one.
6. `river-cloud-late`, in parallel with 5: `zpool reguid`, `mkinitcpio -P` (the new hostid),
   the model payload into `zriver/data/models` (verified, then `zriver/payload` destroyed),
   and `/etc/runink/install-plan.json` for this instance's hardware.

`sudo river-cloud-init status` prints what happened (`data`, `unlock_provider`, `grow`,
`models`, ...); the log is `/var/log/river-cloud-init.log`.

## Model payload

**Decision: ship the encrypted payload in the image, unpack it on first boot** (option a),
not a first-boot download from a release mirror (option b).

- **The server's no-fetch rule and the lock's own rule.** A server node has no fetch tooling, and "an installed
  node never fetches a model" (`models.lock`). A first-boot download needs an HTTP client
  pulling 18.5 GB from the internet on every instance. `river-cloud-init` does make HTTPS
  calls, but only to the metadata server and Secret Manager, for a few hundred bytes.
- **It works without internet egress.** Many deployments give instances no external IP and
  no Cloud NAT; a mirror download fails there, an image-borne payload does not.
- **Integrity is already solved.** `river-modelpack` authenticates every 4 MiB chunk and
  checks every file against the lock; a mirror would add a trust and availability
  dependency on a third party at boot time, and GitHub release rate limits multiply by the
  number of instances.
- **Cost.** About 16.3 GiB more in the image and a 64 GiB minimum disk; the unpack reads
  the ciphertext from the instance's own disk (bounded by the persistent disk's throughput)
  instead of the network, in `river-cloud-late`, **without holding back k0s**. The payload
  dataset is destroyed afterwards, so the space comes back. Measured in the QEMU test:
  see [Tested](#tested).
- **The passphrase is never in the image.** It comes from Secret Manager
  (`runink-models-passphrase-secret`) or, weaker, from metadata (`runink-models-passphrase`).
  Without it the models are deferred (`status: models=deferred`) and the payload stays
  until a later boot has one. For a public listing the passphrase is a transport secret the
  deployment must hand every customer; the models are public open-weight files, so what
  the payload encryption buys in the cloud is that the image never carries them in the
  clear, while the instance's data key is what protects them at rest.
- `--no-models` builds an image without the payload (about 4 GiB of data instead of 20),
  for listings that bring models another way (enrollment's `MODEL_STORE_URL`, rsync).

## Before capture

`85-cloud-finalize` clears, then asserts: `/etc/machine-id` (empty), SSH host keys,
`/etc/hostid`, the random seed, NetworkManager's state (leases and its per-host
`secret_key`) and connection profiles, `/etc/runink` (except the `*.example` files) and
`/var/lib/runink`, any `authorized_keys`, shell histories, build logs, the pacman cache;
root and runink passwords locked. The image is captured without ever having booted, so it
never generated most of them; the assertions make sure. Per instance, at boot:
machine-id, host keys and hostid (`river-cloud-identity`), pool GUID (`zpool reguid`),
initramfs (`mkinitcpio -P`), the data key (`river-cloud-init`).

## Tested

`build/cloud-image-test.sh` boots the image as a GCE stand-in: OVMF, the image on NVMe on
a qcow2 overlay (`disk.raw` is never written) that is 32 GiB larger than the image, a
virtio-net NIC on QEMU user-mode networking (IPv4 and IPv6), and `installer/fakemeta` at
`169.254.169.254:80` through QEMU's `guestfwd` (one process per connection). The fake
serves the instance id, hostname, an `ssh-keys` key generated for the run (and an expired
project key that must be ignored), a token, and the Secret Manager access call for a
random data-key secret and the model passphrase. It asserts `ssh-login`, `hostname`,
`serial-getty`, `pool-expanded`, `data-encrypted`, `key-provider`, `k0s-ready`, `golden`,
`health`, `models`, `reboot-unlock`, and with `--data-disk` also `data-disk`; `vtpm` is reported SKIP. Results of the last run are in the pull
request that added this file.

## What a Marketplace listing needs

From Google Cloud Marketplace's partner documentation, fetched 2026-09-25:

- [Offering VM products](https://docs.cloud.google.com/marketplace/docs/partners/vm): "It
  must use the Cloud Marketplace-hosted image with the attached Compute Engine license",
  comply with the Marketplace open source policy, and be "tested end to end".
- [Building your VM image](https://docs.cloud.google.com/marketplace/docs/partners/vm/build-vm-image):
  "Use one of Google's supported base public images to create a VM, and install your
  app-specific packages and configs"; "Verify Python 2.6 or greater is installed" and
  that gcloud, SSH, sshd, curl and dhcp are installed; create the image with `--licenses`
  (the license from Producer Portal); name it `who-vmOS-image-architecture-date`, new for
  every update; clean user directories and credentials from the disk; re-build and
  re-publish monthly for base-image updates.
- [Configuring imported images](https://docs.cloud.google.com/compute/docs/import/configuring-imported-images):
  the guest environment ("must install ... before you can use key features"), NTP from the
  metadata server, `PermitRootLogin no`, `PasswordAuthentication no`, no SSH keys in the
  image, `ClientAliveInterval 420`, `usermod -L root`, no OS firewall.

### Deviations, and what the image does instead

Runink River is its own operating system and deliberately ships **no Python, no curl, no
gcloud and no Google guest agent** (AGENTS.md: no Python anywhere; a server node has no fetch
tooling on a node). None of them is added for a listing. This table is the written case
for the Marketplace partner team; a listing needs their agreement to a custom-OS product
before submission (other OS vendors are listed, so the route exists, but it is not granted
by default).

| Google expects | Runink River Server image | Equivalent capability |
|---|---|---|
| A Google-supported base public image | Its own OS: zen kernel `linux-runink`, OpenZFS, s6, MIT userland (Artix packages today, the own base next) | Built reproducibly from pinned, signature-checked sources in public CI (`images.yml`); per-release rebuilds |
| Python 2.6+ | none | Nothing on the image needs Python; the first-boot agent is a static Go binary |
| gcloud | none | Administration is from the operator's machine; on the node, `river-cloud-init` reads metadata and Secret Manager with the instance's service account |
| curl | none (the binary is removed; libcurl stays for NetworkManager) | `river-cloud-init` is the only HTTP client, limited to the metadata server and Secret Manager |
| sshd | OpenSSH `sshd`, key-only, `PermitRootLogin no`, `ClientAliveInterval 420` | as expected |
| dhcp | NetworkManager's internal DHCPv4 and DHCPv6 client | as expected |
| The guest environment (google-guest-agent) | `river-cloud-init` (three s6 oneshots) | SSH keys from `ssh-keys` (instance, or project unless blocked), hostname, disk growth, instance identity. **Not** covered: OS Login, keys added while running (applied at next boot), guest attributes, Windows-style password reset, the metadata script runner |
| NTP from `metadata.google.internal` | no NTP client in the closure | KVM paravirtual clock; a client is a follow-up |
| No OS firewall | `runink_fw` exists but stays disarmed unless enrollment arms it | the VPC firewall is the perimeter |
| No user directories or credentials in the image | `85-cloud-finalize` clears and asserts them | per-instance identity at first boot |
| Monthly rebuilds | rebuilt per Runink River release | `images.yml` on each signed `runink-os-*` tag |
| GPL source availability | `/usr/share/river/sources` on the image | [Source offer](#source-offer) |

## Source offer

The image is a binary distribution of GPL and LGPL software (the kernel, glibc, coreutils,
GRUB and more), so it carries its own source offer, in `/usr/share/river/sources/`,
written by `85-cloud-finalize` from the image's package database:

- `MANIFEST.tsv`: every package with its exact version, pkgbase, licence, upstream URL and
  where its complete corresponding source is. Runink River's own packages (`linux-runink`,
  `runink-zfs`, the installer and the other `runink-*` packaging) point at
  `build/pkgbuilds/<package>` in this repository **at the commit the image was built
  from**, whose PKGBUILDs pin every upstream tarball and patch by URL and sha256; every other
  package points at its Artix package source (`gitea.artixlinux.org/packages/<pkgbase>`) at
  the listed version.
- `WRITTEN-OFFER.txt`: a written offer, valid three years, to provide the complete
  corresponding source of any GPL or LGPL component on request (an issue titled "source
  request" in this repository; the maintainers in MAINTAINERS.md answer it).

The sources themselves are not on the image (several GB). Before a public listing, the owner
should also mirror the exact source tarballs of the release (the kernel and OpenZFS
tarballs, the zen patch, and the Artix source packages of that snapshot) as release assets,
so the offer can be met without depending on third-party archives.

## Owner commands

Nothing here has been run. The build machine has no cloud credentials. For the
**payload-free** image (a payload image goes only to the downstream's private project, never
a public bucket). Replace `PROJECT`, `BUCKET`, `REGION` and the image name.

```sh
IMG=runink-river-server-x86-64-20260925
DIR=~/.cache/river-build/cloud-out/$IMG
sha256sum -c "$DIR/$IMG.tar.gz.sha256"

# 1. upload
gcloud storage cp "$DIR/$IMG.tar.gz" gs://BUCKET/$IMG.tar.gz

# 2. the image (only the guest OS features the image supports)
gcloud compute images create "$IMG" --project=PROJECT \
  --source-uri=gs://BUCKET/$IMG.tar.gz \
  --guest-os-features=UEFI_COMPATIBLE,GVNIC,VIRTIO_SCSI_MULTIQUEUE \
  --architecture=X86_64 --storage-location=REGION \
  --family=runink-river-server \
  --description="Runink River Server $IMG (UEFI, ZFS, s6)"
#    For Marketplace, add: --licenses=projects/PROJECT/global/licenses/LICENSE_NAME
#    (the license name comes from Producer Portal).

# 3. the data-key secret, and access for the instances' service account
gcloud iam service-accounts create river-node --project=PROJECT
head -c 32 /dev/urandom | gcloud secrets create river-kek --project=PROJECT \
  --replication-policy=automatic --data-file=-
gcloud secrets add-iam-policy-binding river-kek --project=PROJECT \
  --member=serviceAccount:river-node@PROJECT.iam.gserviceaccount.com \
  --role=roles/secretmanager.secretAccessor
#    and, for an image with the model payload, the passphrase the same way:
gcloud secrets create river-models --project=PROJECT --replication-policy=automatic \
  --data-file=$HOME/.cache/river-build/secrets/models-passphrase
gcloud secrets add-iam-policy-binding river-models --project=PROJECT \
  --member=serviceAccount:river-node@PROJECT.iam.gserviceaccount.com \
  --role=roles/secretmanager.secretAccessor

# 4. a test instance: Shielded VM with vTPM and integrity monitoring, Secure Boot OFF
gcloud compute instances create river-test-1 --project=PROJECT --zone=REGION-a \
  --machine-type=n2-standard-8 --image="$IMG" --boot-disk-size=100GB \
  --shielded-vtpm --shielded-integrity-monitoring --no-shielded-secure-boot \
  --service-account=river-node@PROJECT.iam.gserviceaccount.com \
  --scopes=https://www.googleapis.com/auth/cloud-platform \
  --metadata=block-project-ssh-keys=TRUE,serial-port-enable=FALSE,runink-key-secret=river-kek,runink-models-passphrase-secret=river-models,ssh-keys="runink:$(cat ~/.ssh/id_ed25519.pub)"
gcloud compute instances get-serial-port-output river-test-1 --zone=REGION-a | tail -50
ssh runink@<external-ip> 'sudo river-cloud-init status'
```

A 100 GB boot disk leaves room for the models, the platform's images and data. Use a
third-generation machine type (for example `c3-standard-8`) once to prove the NVMe boot on
real hardware, and `--network-interface=nic-type=GVNIC` to prove gVNIC.

## Other clouds

The pipeline is cloud-neutral up to the per-cloud function and the packaging. Planned, same
pipeline:

- **AWS** (`--cloud aws`): an AMI registered from a raw snapshot (`import-snapshot`, then
  `register-image --boot-mode uefi`); ENA and NVMe drivers in the initramfs (Nitro exposes
  EBS as NVMe); IMDSv2 (a PUT for a session token, then `X-aws-ec2-metadata-token`) in
  place of `Metadata-Flavor`; the serial console on `ttyS0,115200`; a KMS- or Secrets
  Manager-backed key provider, and NitroTPM for the TPM provider.
- **Azure** (`--cloud azure`): a fixed-size VHD; Hyper-V drivers (`hv_*`); the IMDS at
  169.254.169.254 with `Metadata: true`; Key Vault as the provider.

## What is not verified

- Nothing has run on Compute Engine: gVNIC, the real metadata server and Secret Manager,
  Shielded VM, the serial console in the Cloud Console, a real disk resize.
- The vTPM provider (not implemented) and SEV/TDX (not declared).
- Pod egress through NAT64 on a cloud NIC, and multi-node cloud clusters.
- Marketplace acceptance of a non-Google base OS without Python, curl or the guest agent.
- k0s pulls its system images (kube-router, pause, ...) from the internet on first boot, so
  the instance needs egress today. TODO: cloud images must load the same offline image
  payload as the air-gapped ISO once that exists (k0s image bundle and the encrypted
  platform-image payload), carried like the model payload and unpacked per instance.
