<!--
SPDX-FileCopyrightText: 2026 The RIVER Authors
SPDX-License-Identifier: CC-BY-4.0
-->

# Runink River cloud edition (Compute Engine)

The cloud edition is the Runink River ISO's system, installed headless onto a raw disk that
Compute Engine (GCE) can import. It is a plain Linux image: it has no desktop, no application
payload and no AI components, and it carries the same kernel (`linux-runink`), ZFS and package
base as the installer ISO. [gcp.md](gcp.md) is the design and the research behind it.

**Status.** The code, the unit tests and the test harness's self-test are in this tree. A full
image build and the boot test (`build/cloud-base-test.sh` on a real `disk.raw`) were not run
when this page was written, because they need a built ISO. Nothing here has run on Compute
Engine. Treat every claim below as "implemented and reviewed", not "measured on a cloud", until
the PR that adds a boot-test record says otherwise.

## What the image contains

- The ISO's rootfs minus the desktop: `installer/lib/38-cloud-headless.sh` removes DEsk, its
  wlroots, greetd and its greeter, Firefox, Plymouth, Mesa and the Vulkan drivers, GStreamer and
  ffmpeg, PipeWire, Bluetooth, CUPS, the desktop fonts and the AMD/Intel GPU firmware. It fails
  the install if DEsk, greetd, the greeter, Firefox or Plymouth are still installed, and
  `85-cloud-finalize` fails it if a desktop service is in the s6 boot database. Packages that a
  kept package needs stay (the step prints which).
- The same kernel and initramfs tooling, with `nvme`, `virtio_net` and `gve` in the initramfs;
  virtio-scsi, virtio-pci, the KVM clock and the 8250 serial console are built into the kernel
  (step 78 checks them in the running kernel).
- s6 as init, s6-rc services, NetworkManager (DHCP on the first NIC; nothing forces an MTU, so
  the VPC's 1460 from DHCP applies), chrony using `metadata.google.internal` only, OpenSSH.
- GRUB on the ESP, kernel and GRUB output on `ttyS0` at 115200 8N1, a getty on `ttyS0`.
- A ZFS root: GPT, a 1 GiB ESP (`/boot`) and one pool, the same layout rules as the cloud
  installer step `12-cloud-disk` (`autoexpand=on`, boot environment `zriver/ROOT/<be>`).
  The root is not encrypted (see below).
- `river-cloud-init`, one Go standard-library binary, run by s6 services (below).
- The developer toolchain of the ISO (git, go, base-devel, vim). The host firewall is the ISO's
  (`runink-fw`, default-deny input); the image opens `tcp 22` in `/etc/runink/fw-open` and
  nothing else.

## What it does not have or enable

- **No Google guest agent or OS Login.** The agent needs systemd. `river-cloud-init` covers the
  items listed below only.
- **Secure Boot is off.** GRUB and the kernel are not signed with a key Compute Engine's default
  database trusts. Do not enable Shielded VM Secure Boot on it.
- **Lockdown is loaded but not enforcing**: the kernel has the lockdown LSM, but there is no
  `lockdown=` on the command line and module signatures are not enforced. Kernel modules are
  signed, not required to be.
- **The root pool is not encrypted** (Compute Engine encrypts disks at rest; per-instance keys
  would need a key provider that does not exist yet).
- No TPM key sealing, no audit daemon, no SBOM or signature on the image yet, no reproducible
  build.
- `startup-script-url`, shutdown scripts, guest attributes, OS Login and Windows features are
  not supported.
- No telemetry: the image contacts only the metadata server, plus NetworkManager's and
  chrony's normal traffic.
- Metadata users are administrators: every account named in `ssh-keys` gets passwordless
  `sudo` (`/etc/sudoers.d/20-river-cloud`), as with Compute Engine's guest environment.

## The guest bootstrap

`river-cloud-init` (`installer/cloudinit`) is started by s6 services that step
`78-cloud-target` writes for the base profile:

| Service | What it does |
|---|---|
| `river-cloud-identity` | machine-id, SSH host keys, ZFS hostid, before the network |
| `river-cloud-init` | waits for the metadata server (5 minutes at most), sets the hostname, installs SSH keys, grows the root partition and pool to the disk |
| `river-cloud-watch` | re-applies SSH keys when instance or project metadata changes |
| `river-cloud-startup` | runs the `startup-script` value once per boot, detached; output in `/var/log/river-cloud-startup.log`; the script file is deleted afterwards |
| `river-cloud-late` | pool reguid and initramfs rebuild for the instance's hostid |

Keys: `ssh-keys` from instance metadata and, unless `block-project-ssh-keys` is true, project
metadata. Each `user:key` line goes to `/etc/ssh/authorized_keys.d/<user>`; a missing account is
created (home directory, bash, no password, group `wheel`). `root` and invalid names are skipped,
expired `google-ssh` keys are dropped, and key files the tool wrote for users who left metadata
are removed. A metadata error other than "not found" leaves the installed keys untouched. If the
metadata server is unreachable the boot goes on: no keys change and no script runs. sshd is
key-only, no password login, no root login (`sshd_config.d/05-river-cloud.conf`).
It reaches the metadata server by IP (`169.254.169.254`), not by name.

## Building the image

Needs the Runink River ISO (`build/local-iso.sh`, public build), QEMU/KVM, OVMF and Go. Rootless.

```sh
build/cloud-image.sh path/to/runink-river-<date>-x86_64.iso        # default disk 10G
```

It boots the ISO with a blank sparse `disk.raw`, overlays this checkout's installer steps and
`installer/runink-cloud-install` on the live system, and runs the unattended install: steps
`12-cloud-disk`, `20-clone-rootfs`, `30-target-config`, `35-pacman-keyring`, `38-cloud-headless`,
`40-boot-grub-zfs`, `50-runink-user`, `78-cloud-target`, `80-enable-s6`, `85-cloud-finalize`,
`90-export`: the ISO installer's steps, with the cloud disk and the cloud steps in place of the
node's. Output under `~/.cache/river-build/cloud-out/<name>/`: `disk.raw` (a whole number of GiB),
`<name>.tar.gz` (GNU tar `--format=oldgnu`, holding only `disk.raw`), its sha256, `image.env`.
The disk grows to the instance's boot disk on first boot.

## Testing the image

```sh
build/cloud-base-test.sh --selftest                       # no VM: every check can fail
build/cloud-base-test.sh path/to/cloud-out/<name>         # NVMe and virtio-scsi boots
build/cloud-base-test.sh --variant nvme --mutate resize path/to/cloud-out/<name>
```

The harness boots `disk.raw` on UEFI QEMU with the disk on NVMe and on virtio-scsi, enlarged, with
a serial console only and a fake metadata server, and checks: serial login prompt, kernel console,
sshd up, metadata-key login (as an account created at boot), sudo, hostname, root pool resized,
no listening socket except tcp 22 (and the DHCP client's udp 68), password and root login refused,
no desktop, startup script ran once. Exit status is 1 on any failure. `--selftest` breaks one piece
of evidence per check and requires exactly that check to fail; `--mutate key|hostname|resize|listener`
breaks the live guest or its metadata and requires the matching check to fail. The VM uses 4 GiB and
2 CPUs by default; run it under `nice -n 19 ionice -c 3` on a shared host.

Not covered by it: MTU 1460 from a real DHCP server (QEMU's does not send one), gVNIC, Shielded VM,
a real metadata server, a reboot.

## Publishing (owner steps; nothing here is run by the build)

1. Build and test the image as above; check `sha256sum -c <name>.tar.gz.sha256`.
2. Create or pick a dedicated Google Cloud project for images, and a bucket in a US region.
3. `gcloud storage cp <name>.tar.gz gs://<bucket>/`.
4. `gcloud compute images create <name> --project=<image-project> --source-uri=gs://<bucket>/<name>.tar.gz --family=runink-river --architecture=X86_64 --guest-os-features=UEFI_COMPATIBLE,GVNIC,VIRTIO_SCSI_MULTIQUEUE`
   (add `--description`). Do not enable Secure Boot on instances.
5. Test on a real instance: `gcloud compute instances create ... --image=<name> --image-project=<image-project> --metadata=ssh-keys="<user>:$(cat key.pub)"`; read the serial output with
   `gcloud compute instances get-serial-port-output`, and ssh in. Repeat on an NVMe machine
   series (C3) and a gVNIC NIC.
6. Share one image publicly: `gcloud compute images add-iam-policy-binding <name> --project=<image-project> --member=allAuthenticatedUsers --role=roles/compute.imageUser`.
7. Delete the bucket object; deprecate old image versions; image storage is billed to the project
   ([gcp.md](gcp.md), "Cost").

Publishing is permanent for anyone who has used the image: sign off the release process
([RELEASE.md](../../RELEASE.md)) first.
