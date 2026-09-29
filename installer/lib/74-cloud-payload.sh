#!/bin/sh
# SPDX-FileCopyrightText: 2026 The RIVER Authors
# SPDX-License-Identifier: MIT
#
# 74-cloud-payload — carry the medium's encrypted model payload INTO a cloud image, still
# encrypted, in place of 72-models-payload (which unpacks it). docs/CLOUD-IMAGES.md, "Model
# payload".
#
# An installed node unpacks the payload at install time into <pool>/models under its own
# pool key. A cloud image cannot: the key that protects the data at rest is generated per
# INSTANCE at first boot, so no image can hold the models "decrypted for everyone". The
# payload parts (AES-256-GCM ciphertext, docs/MODEL-PAYLOAD.md) are copied as they are into
# <pool>/payload; river-cloud-init unpacks them on the instance into the encrypted
# <pool>/data/models with a passphrase from the project's secret manager (or instance
# metadata), then destroys <pool>/payload. The passphrase is never in the image.
#
# RUNINK_CLOUD_MODELS=0 builds an image without models (smaller; the models then arrive
# like on any unenrolled node). A medium without a payload gives the same result.
set -eu

TARGET="${RUNINK_TARGET:?}"
POOL="${RUNINK_POOL:?}"
: "${RUNINK_CLOUD:?74-cloud-payload: cloud images only}"
DS="$POOL/payload"
MP="/var/lib/river-cloud/river-models"

log() { echo "cloud-payload: $*"; }

if [ "${RUNINK_CLOUD_MODELS:-1}" = 0 ]; then
	log "RUNINK_CLOUD_MODELS=0: this image carries no models"
	exit 0
fi
PAYLOAD=""
for d in ${RUNINK_MODELS_PAYLOAD:-} /run/initramfs/live/river-models /run/archiso/bootmnt/river-models \
	/run/artix/bootmnt/river-models /run/miso/bootmnt/river-models /bootmnt/river-models \
	$(awk '$3 == "iso9660" { print $2 "/river-models" }' /proc/mounts); do
	[ -f "$d/MANIFEST" ] && { PAYLOAD="$d"; break; }
done
if [ -z "$PAYLOAD" ]; then
	log "no model payload on the medium; this image carries no models"
	exit 0
fi
command -v river-modelpack >/dev/null 2>&1 || { log "river-modelpack missing from the live image" >&2; exit 1; }
log "checking $PAYLOAD (part sizes and sha256 against its MANIFEST)"
river-modelpack check --payload "$PAYLOAD" >/dev/null

# Ciphertext only: ZFS compression would gain nothing, and 1 MiB records suit the 1.9 GiB parts.
zfs create -o mountpoint="$MP" -o com.sun:auto-snapshot=false -o compression=off \
	-o recordsize=1M -o atime=off "$DS"
DEST="$TARGET$MP"
[ -d "$DEST" ] || { zfs destroy -r "$DS"; log "$DS is not mounted at $DEST" >&2; exit 1; }
log "copying the payload into $DS (encrypted as it is: $(du -sh "$PAYLOAD" | cut -f1))"
cp "$PAYLOAD"/MANIFEST "$PAYLOAD"/models.rmp.* "$DEST/"
river-modelpack check --payload "$DEST" >/dev/null || { zfs destroy -r "$DS"; log "the copy does not verify" >&2; exit 1; }
chmod 0700 "$DEST"
chmod 0600 "$DEST"/*
log "payload in the image: $(zfs get -H -o value used "$DS") in $DS; unpacked per instance at first boot"
