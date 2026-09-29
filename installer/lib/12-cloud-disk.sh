#!/bin/sh
# SPDX-FileCopyrightText: 2026 The RIVER Authors
# SPDX-License-Identifier: MIT
#
# 12-cloud-disk — the disk of a CLOUD IMAGE (RUNINK_CLOUD set; runink-autoinstall --cloud),
# in place of 10-disk-zfs. docs/CLOUD-IMAGES.md is the design.
#
# A cloud image is written once and booted by many instances, with no console to type a
# passphrase at. So its pool is laid out differently from an installed node's:
#
#   <disk>1  ESP, FAT32, 1 GiB     -> /boot   kernel, initramfs, GRUB (as on every node)
#   <disk>2  zriver                          autoexpand=on: grows to the instance's disk
#            ├─ zriver/ROOT/<be>    /        NOT encrypted: public MIT/GPL/CDDL code and
#            │                                public configuration only; nothing per-node
#            ├─ zriver/payload      (image)  NOT encrypted by ZFS: holds only the model
#            │                                payload, which is AES-256-GCM ciphertext
#            └─ zriver/data                  created on the INSTANCE at first boot by
#                                             river-cloud-init: the aes-256-gcm encryption
#                                             root, under a per-instance key wrapped by a
#                                             key provider; home, /var/lib/core, models,
#                                             /var/lib/k0s, /etc/runink, /var/lib/runink
#
# Nothing encrypted is created here: an encryption root made at image build would share one
# key across every instance. This departs from invariant 3 ("every dataset inherits the
# pool's encryption root") for cloud images only, which needs a TSC vote (GOVERNANCE.md);
# every node state and secret path still lands encrypted, under a key no other instance has.
#
# Single disk only: the plan's boot disk is the image. Refuses a multi-disk plan.
set -eu

DISK="${RUNINK_DISK:?}"
POOL="${RUNINK_POOL:?}"
BE="${RUNINK_BE:?}"
TARGET="${RUNINK_TARGET:?}"
CLOUD="${RUNINK_CLOUD:?12-cloud-disk: RUNINK_CLOUD is not set (this step is for cloud images only)}"

case "$CLOUD" in
	gce) ;;
	*) echo "cloud-disk: RUNINK_CLOUD=$CLOUD is not supported yet (gce only; docs/CLOUD-IMAGES.md)" >&2; exit 1 ;;
esac
case "${RUNINK_POOL_TOPOLOGY:-single}" in
	single) ;;
	*) echo "cloud-disk: a cloud image is one disk; the plan says ${RUNINK_POOL_TOPOLOGY}" >&2; exit 1 ;;
esac
[ -z "${RUNINK_SPECIAL_DISKS:-}" ] || { echo "cloud-disk: a cloud image has no special vdev" >&2; exit 1; }

part() {
	case "$DISK" in
		*nvme*|*mmcblk*) echo "${DISK}p$1" ;;
		*) echo "${DISK}$1" ;;
	esac
}
EFI_PART="$(part 1)"
ZFS_PART="$(part 2)"

if zpool import 2>/dev/null | grep -qw "$POOL"; then
	echo "cloud-disk: a pool named $POOL is importable on this machine; a cloud image is always built on a blank disk" >&2
	exit 1
fi

# A fresh hostid for the build; the image does not keep it (85-cloud-finalize removes
# /etc/hostid, river-cloud-init makes a per-instance one).
if command -v zgenhostid >/dev/null 2>&1 && [ ! -s /etc/hostid ]; then
	zgenhostid -f
fi
lsmod 2>/dev/null | grep -q '^zfs ' || modprobe zfs

echo "cloud-disk: GPT on $DISK (ESP 1 GiB + ZFS)"
parted -s "$DISK" mklabel gpt
parted -s "$DISK" mkpart ESP fat32 1MiB 1025MiB
parted -s "$DISK" set 1 esp on
parted -s "$DISK" mkpart runink 1025MiB 100%
command -v udevadm >/dev/null 2>&1 && udevadm settle || true
mkfs.fat -F32 -n RUNINK_EFI "$EFI_PART"

echo "cloud-disk: creating pool $POOL (boot environment unencrypted; data encrypted per instance at first boot)"
zpool create -f \
	-o ashift=12 \
	-o autotrim=on \
	-o autoexpand=on \
	-O compression=zstd \
	-O atime=off \
	-O xattr=sa -O acltype=posixacl \
	-O mountpoint=none \
	-R "$TARGET" \
	"$POOL" "$ZFS_PART"
zfs create -o mountpoint=none "$POOL/ROOT"
zfs create -o canmount=noauto -o mountpoint=/ "$POOL/ROOT/$BE"
zpool set bootfs="$POOL/ROOT/$BE" "$POOL"
zfs mount "$POOL/ROOT/$BE"

# No home/state/containers datasets: those paths stay directories on the BE until
# river-cloud-init moves them into <pool>/data on the instance.
mkdir -p "$TARGET/boot"
mount "$EFI_PART" "$TARGET/boot"
echo "cloud-disk: pool $POOL, BE $BE mounted at $TARGET, ESP at $TARGET/boot"
