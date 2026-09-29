#!/bin/sh
# SPDX-FileCopyrightText: 2026 The RIVER Authors
# SPDX-License-Identifier: MIT
#
# cloud-image.sh — build a server CLOUD IMAGE from a server ISO built from a downstream server
# profile (RIVER_PROFILE_DIR names it; Runink River itself is a workstation and has no cloud
# installer), as the calling user (no root; needs a writable /dev/kvm). docs/CLOUD-IMAGES.md is
# the design.
#
#   build/cloud-image.sh [options] ISO
#
# It boots the ISO under QEMU/KVM with UEFI (OVMF), overlays this checkout's cloud installer
# (the steps, runink-autoinstall, river-cloud-init and its s6 services) on the live system,
# runs `runink-autoinstall --cloud gce` onto a sparse raw disk, powers off, and packages
# the disk the way Compute Engine imports it:
#
#   <out>/<name>/disk.raw             the image (GPT, UEFI, ESP + ZFS), a whole number of GiB
#   <out>/<name>/<name>.tar.gz        disk.raw alone, GNU tar --format=oldgnu, sparse, gzip
#   <out>/<name>/<name>.tar.gz.sha256
#   <out>/<name>/image.env            what went in: ISO, commit, payload, models, overlay list
#
# A DOWNSTREAM PLATFORM PAYLOAD (RIVER_PAYLOAD_DIR, docs/BUILD.md) comes in with the ISO: an
# ISO built with one yields an image with one. The script says which it found. An image
# with a payload is PRIVATE: <name> ends in -payload, image.env says PRIVATE=1, and a
# PRIVATE-DO-NOT-PUBLISH file sits beside it. This script uploads nothing and has no publish
# step; docs/CLOUD-IMAGES.md has the owner's upload commands, for the payload-free image.
#
# Options:
#   --cloud NAME           the target cloud (default gce; the only one built so far)
#   --name NAME            image name (default runink-river-server-x86-64-<ISO date>[-payload])
#   --out DIR              default ~/.cache/river-build/cloud-out
#   --disk-size SIZE       raw disk size in GiB, like 64G (default 64G: the install planner's
#                          smallest target disk, and room for the model payload to unpack)
#   --no-models            do not carry the ISO's model payload into the image
#   --expect-payload none|present   fail unless the ISO's downstream payload is as stated
#   --mem MiB --cpus N     build VM size (default 6144, 4)
#   --work DIR             work directory (default ~/.cache/river-build/cloud-work/<time>)
#   --keep                 keep the work directory
#   --dry-run              check the arguments and print the plan; boot nothing
#
# Prerequisites: qemu-system-x86_64, qemu-img, OVMF (edk2-ovmf), mkfs.vfat + mcopy, go (to
# build river-cloud-init), GNU tar, gzip, sha256sum. Big files go under ~/.cache, never /tmp.
set -eu

HERE="$(cd "$(dirname "$0")" && pwd)"
REPO="$(cd "$HERE/.." && pwd)"
CACHE="${XDG_CACHE_HOME:-$HOME/.cache}"
# shellcheck source=build/qemu-lib.sh
. "$HERE/qemu-lib.sh"

CLOUD=gce NAME="" OUT="$CACHE/river-build/cloud-out" DISK_SIZE=64G MODELS=1 EXPECT_PAYLOAD=""
MEM=6144 CPUS=4 WORK="" KEEP=0 DRY=0 ISO=""
SERIAL_ID="RIVERCLOUD0001"

die() { echo "cloud-image: $*" >&2; exit 2; }
usage() { sed -n '5,43p' "$0" | sed 's/^# \{0,1\}//'; exit 2; }

while [ $# -gt 0 ]; do
	case "$1" in
		--cloud) CLOUD="${2:?}"; shift ;;
		--name) NAME="${2:?}"; shift ;;
		--out) OUT="${2:?}"; shift ;;
		--disk-size) DISK_SIZE="${2:?}"; shift ;;
		--no-models) MODELS=0 ;;
		--expect-payload) EXPECT_PAYLOAD="${2:?}"; shift ;;
		--mem) MEM="${2:?}"; shift ;;
		--cpus) CPUS="${2:?}"; shift ;;
		--work) WORK="${2:?}"; shift ;;
		--keep) KEEP=1 ;;
		--dry-run) DRY=1 ;;
		--publish*|--upload*) die "this script never publishes or uploads (docs/CLOUD-IMAGES.md has the owner's commands)" ;;
		-h|--help) usage ;;
		-*) die "unknown option $1 (see --help)" ;;
		*) [ -z "$ISO" ] || die "one ISO only"; ISO="$1" ;;
	esac
	shift
done
[ -n "$ISO" ] || usage
[ -f "$ISO" ] || die "no ISO at $ISO"
case "$CLOUD" in gce) ;; aws|azure) die "--cloud $CLOUD is planned, not built yet (docs/CLOUD-IMAGES.md)" ;; *) die "unknown --cloud $CLOUD" ;; esac
case "$DISK_SIZE" in *[!0-9G]*|G*|'') die "--disk-size in whole GiB, like 64G" ;; *G) ;; *) die "--disk-size in whole GiB, like 64G" ;; esac
[ "${DISK_SIZE%G}" -ge 64 ] || die "--disk-size below 64G: the install planner refuses target disks under 64 GiB"
case "$EXPECT_PAYLOAD" in ''|none|present) ;; *) die "--expect-payload none or present" ;; esac
case "$MEM$CPUS" in *[!0-9]*) die "--mem and --cpus are numbers" ;; esac
missing=""
for t in qemu-system-x86_64 qemu-img mkfs.vfat mcopy go tar gzip sha256sum blkid; do command -v "$t" >/dev/null 2>&1 || missing="$missing $t"; done
ovmf_check || missing="$missing OVMF(edk2-ovmf)"
[ -w /dev/kvm ] || missing="$missing /dev/kvm(writable)"
tar --version 2>/dev/null | grep -q 'GNU tar' || missing="$missing GNU-tar"
[ -z "$missing" ] || die "missing prerequisites:$missing"
LABEL="$(blkid -s LABEL -o value "$ISO" 2>/dev/null || true)"
[ -n "$LABEL" ] || die "cannot read the ISO volume label of $ISO"
if [ -z "$NAME" ]; then
	d="$(basename "$ISO" | grep -Eo '[0-9]{8}' | head -1 || true)"
	NAME="runink-river-server-x86-64-${d:-$(date +%Y%m%d)}"
fi
echo "$NAME" | grep -Eq '^[a-z]([-a-z0-9]{0,52}[a-z0-9])?$' || die "--name must be a Compute Engine image name ([a-z][-a-z0-9]*, at most 54 characters before -payload)"
[ -n "$WORK" ] || WORK="$CACHE/river-build/cloud-work/$(date +%Y%m%d-%H%M%S)"
for d in "$WORK" "$OUT"; do case "$d" in /tmp/*) die "$d is on tmpfs; the disk image grows to tens of GB (use ~/.cache)" ;; esac; done

# The ISO as a CD-ROM: the build needs no USB fidelity, and OVMF's USB boot was seen to miss
# the medium once ("No bootable option or device was found").
MEDIUM_ARGS="-drive if=none,id=medium,format=raw,readonly=on,media=cdrom,file=$ISO -device ide-cd,drive=medium,bootindex=0"
# The build disk sits on virtio-scsi, as on GCE's first- and second-generation machines; the
# cloud test boots the image from NVMe, which proves the initramfs carries both.
QEMU_ARGS="-enable-kvm -machine q35 -cpu host -m $MEM -smp $CPUS -nic none -display none
 -drive if=pflash,format=raw,readonly=on,file=$OVMF_CODE -drive if=pflash,format=raw,file=$WORK/vars.fd
 -device virtio-scsi-pci,id=scsi0
 -drive if=none,id=target,format=raw,file=$WORK/disk.raw -device scsi-hd,drive=target,bus=scsi0.0,serial=$SERIAL_ID,bootindex=1
 -drive if=none,id=kit,format=raw,file=$WORK/kit.img -device virtio-blk-pci,drive=kit,serial=RIVERTESTKIT"

echo "cloud-image: ISO $ISO (label $LABEL) -> $CLOUD image $NAME, disk $DISK_SIZE, models=$MODELS"
echo "cloud-image: work $WORK, out $OUT"
if [ "$DRY" -eq 1 ]; then
	echo "cloud-image: DRY RUN. Would run: qemu-system-x86_64 $(echo "$QEMU_ARGS" | tr '\n' ' ') $MEDIUM_ARGS"
	exit 0
fi
mkdir -p "$WORK"
trap 'qemu_cleanup' EXIT INT TERM

# --- the kit: this checkout's cloud installer, overlaid on the live system -----------------
KIT="$WORK/kit"
rm -rf "$KIT"; mkdir -p "$KIT/overlay/usr/local/bin" "$KIT/overlay/usr/local/lib/runink-install" "$KIT/overlay/etc/s6/sv"
# The cloud installer (runink-autoinstall, the river-cloud-* s6 services, the private firewall
# ruleset) belongs to a server distribution's profile, not to Runink River's: RIVER_PROFILE_DIR
# names that downstream profile (docs/BUILD.md, "Downstream distributions").
[ -n "${RIVER_PROFILE_DIR:-}" ] || die "set RIVER_PROFILE_DIR to the downstream server profile the ISO was built from (Runink River has no cloud installer)"
OVL="$RIVER_PROFILE_DIR/root-overlay"
for f in usr/local/bin/runink-autoinstall usr/local/bin/river-perms usr/local/lib/runink-net/runink-firewall.nft; do
	[ -f "$OVL/$f" ] || die "$OVL has no $f (not a server profile with the cloud installer?)"
done
echo "cloud-image: building river-cloud-init (GOAMD64=v1, stdlib only)"
( cd "$REPO/installer" && CGO_ENABLED=0 GOAMD64=v1 go build -trimpath -ldflags='-s -w' \
	-o "$KIT/overlay/usr/local/bin/river-cloud-init" ./cloudinit )
cp "$OVL/usr/local/bin/runink-autoinstall" "$OVL/usr/local/bin/river-perms" "$KIT/overlay/usr/local/bin/"
cp "$OVL"/usr/local/lib/runink-install/*.sh "$KIT/overlay/usr/local/lib/runink-install/"
cp -R "$OVL"/etc/s6/sv/river-cloud-* "$KIT/overlay/etc/s6/sv/"
mkdir -p "$KIT/overlay/usr/local/lib/runink-net"
cp "$OVL/usr/local/lib/runink-net/runink-firewall.nft" "$KIT/overlay/usr/local/lib/runink-net/"
cat > "$KIT/config" <<EOF
TARGET_SERIAL=$SERIAL_ID
SOURCE_REF=$(git -C "$REPO" rev-parse HEAD 2>/dev/null || echo main)
CLOUD=$CLOUD
MODELS=$MODELS
EOF
cat > "$KIT/live.sh" <<'EOF'
#!/bin/sh
# Runs as root on the LIVE system; results go to the serial port.
exec >/dev/ttyS0 2>&1
set -u
export PATH=/usr/local/sbin:/usr/local/bin:/usr/bin:/usr/sbin:/bin:/sbin
. /run/rt/config
ok() { echo "RIVERTEST OK $1"; }
ko() { echo "RIVERTEST FAIL $1${2:+ ($2)}"; }
echo "RIVERTEST BEGIN cloud-build"
( cd /run/rt/overlay && find . -type f ) | while read -r f; do
	f="${f#./}"
	mkdir -p "/$(dirname "$f")" && cp "/run/rt/overlay/$f" "/$f"
	case "$f" in usr/local/bin/*|usr/local/lib/runink-install/*) chmod 0755 "/$f" ;; *) chmod 0644 "/$f" ;; esac
	echo "RIVERTEST NOTE overlay /$f"
done
if [ -f /usr/local/share/runink/core/PAYLOAD-NONE ]; then echo "RIVERTEST PAYLOAD none"
elif [ -d /usr/local/share/runink/core ]; then echo "RIVERTEST PAYLOAD present"
else echo "RIVERTEST PAYLOAD unknown"; fi
river-hwprobe --json > /run/rt-probe.json && ok hwprobe || ko hwprobe
rc=0
river-plan --probe /run/rt-probe.json --manifest /usr/local/share/runink/models.tiers --lab --json > /run/rt-plan.json || rc=$?
{ [ "$rc" -eq 0 ] || [ "$rc" -eq 3 ]; } && ok plan || ko plan "exit $rc"
export RUNINK_SOURCE_REF="$SOURCE_REF"
set -- --plan-file /run/rt-plan.json --yes-i-have-checked-serial="$TARGET_SERIAL" --cloud "$CLOUD"
[ "$MODELS" = 0 ] && set -- "$@" --cloud-no-models
# A PUBLIC image (no downstream platform payload) carries no payload of any kind
# (docs/PAYLOADS.md): never the models, whatever the medium holds.
[ -f /usr/local/share/runink/core/PAYLOAD-NONE ] && { echo "RIVERTEST NOTE public image: --cloud-no-models enforced"; set -- "$@" --cloud-no-models; }
runink-autoinstall "$@" 2>&1 | tee /run/rt-install.log
grep -q AUTOINSTALL-DONE /run/rt-install.log && ok install || ko install
echo "RIVERTEST END cloud-build"
sync
poweroff -f 2>/dev/null || poweroff
EOF
rm -f "$WORK/kit.img"
mkfs.vfat -C -n RIVERTEST "$WORK/kit.img" 131072 >/dev/null
for f in "$KIT"/*; do mcopy -s -i "$WORK/kit.img" "$f" ::/; done
OVERLAY_LIST="$(cd "$KIT/overlay" && find . -type f | sed 's|^\.||' | sort | tr '\n' ' ')"

# --- build: boot the ISO, install to the raw disk ------------------------------------------------
rm -f "$WORK/disk.raw"
truncate -s "$DISK_SIZE" "$WORK/disk.raw"     # sparse; qemu writes it as this user
cp "$OVMF_VARS" "$WORK/vars.fd"
: > "$WORK/serial-build.log"
echo "cloud-image: booting the live ISO and installing (this copies the model payload too; allow 30-60 min)"
# shellcheck disable=SC2086
start_vm build $MEDIUM_ARGS
sleep 30
# Firmware that found nothing to boot: power-cycle once with fresh variables.
if seen "No bootable option or device was found" build; then
	echo "cloud-image: the firmware found no boot medium; retrying once" >&2
	wait_exit 1 || true
	cp "$OVMF_VARS" "$WORK/vars.fd"
	# shellcheck disable=SC2086
	start_vm build $MEDIUM_ARGS
	sleep 30
fi
BOOT="sudo sh -c \"mkdir -p /run/rt; mount -r -L RIVERTEST /run/rt; sh /run/rt/live.sh\""
tries=0
until seen "RIVERTEST BEGIN cloud-build" build; do
	tries=$((tries + 1))
	[ "$tries" -le 4 ] || { echo "cloud-image: the live system never ran the kit (see $WORK/*.png)" >&2; shot never-ran; exit 1; }
	sleep 40
	mon "sendkey alt-f2"; sleep 2
	type_line runink; sleep 2; type_line runink; sleep 3
	type_line "$BOOT"
	sleep 20
done
wait_for "RIVERTEST END cloud-build" build 5400 || { shot build-end; echo "cloud-image: the install did not finish" >&2; }
wait_exit 180 || true
tr -d '\r' < "$WORK/serial-build.log" | grep -a 'RIVERTEST ' > "$WORK/results-build.txt" || true
grep -q 'RIVERTEST OK install$' "$WORK/results-build.txt" || {
	echo "cloud-image: FAIL — the cloud install did not complete; serial log: $WORK/serial-build.log" >&2
	grep -a -E 'AUTOINSTALL FAILED|FAIL|cloud-' "$WORK/serial-build.log" | tail -20 >&2
	exit 1
}
PAYLOAD="$(sed -n 's/^RIVERTEST PAYLOAD //p' "$WORK/results-build.txt" | head -1)"
echo "cloud-image: downstream platform payload in the ISO: ${PAYLOAD:-unknown}"
if [ -n "$EXPECT_PAYLOAD" ] && [ "$PAYLOAD" != "$EXPECT_PAYLOAD" ]; then
	echo "cloud-image: FAIL — --expect-payload $EXPECT_PAYLOAD, the ISO has: $PAYLOAD" >&2; exit 1
fi
PRIVATE=0
case "$PAYLOAD" in none) ;; *) PRIVATE=1; NAME="$NAME-payload" ;; esac
CARRIES_MODELS=no
grep -aq 'payload in the image:' "$WORK/serial-build.log" && CARRIES_MODELS=yes
if [ "$PRIVATE" = 0 ] && [ "$CARRIES_MODELS" = yes ]; then
	echo "cloud-image: FAIL — a PUBLIC image (no downstream payload) must carry no model payload (docs/PAYLOADS.md)" >&2
	rm -f "$WORK/disk.raw"
	exit 1
fi

# --- package: disk.raw in <name>.tar.gz ----------------------------------------------------------
DEST="$OUT/$NAME"
mkdir -p "$DEST"
mv "$WORK/disk.raw" "$DEST/disk.raw"
bytes="$(stat -c %s "$DEST/disk.raw")"
[ $((bytes % 1073741824)) -eq 0 ] || { echo "cloud-image: disk.raw is not a whole number of GiB ($bytes)" >&2; exit 1; }
qemu-img info -f raw "$DEST/disk.raw" | sed 's/^/  /'
echo "cloud-image: packaging $DEST/$NAME.tar.gz (GNU tar, oldgnu, sparse, gzip)"
( cd "$DEST" && rm -f "$NAME.tar.gz" && tar --format=oldgnu -Sczf "$NAME.tar.gz" disk.raw )
[ "$(tar -tzf "$DEST/$NAME.tar.gz")" = disk.raw ] || { echo "cloud-image: the archive does not hold exactly disk.raw" >&2; exit 1; }
( cd "$DEST" && sha256sum "$NAME.tar.gz" > "$NAME.tar.gz.sha256" )
cat > "$DEST/image.env" <<EOF
NAME=$NAME
CLOUD=$CLOUD
BUILT=$(date -u +%Y-%m-%dT%H:%M:%SZ)
ISO=$(basename "$ISO")
ISO_LABEL=$LABEL
COMMIT=$(git -C "$REPO" rev-parse HEAD 2>/dev/null || echo unknown)
DISK_GIB=$((bytes / 1073741824))
DOWNSTREAM_PAYLOAD=${PAYLOAD:-unknown}
PRIVATE=$PRIVATE
MODEL_PAYLOAD=$CARRIES_MODELS
OVERLAY="$OVERLAY_LIST"
TAR_SHA256=$(cut -d' ' -f1 "$DEST/$NAME.tar.gz.sha256")
EOF
if [ "$PRIVATE" = 1 ]; then
	cat > "$DEST/PRIVATE-DO-NOT-PUBLISH" <<EOF
This image carries a downstream platform payload. It is PRIVATE: never upload it to a public
bucket, mirror or release. Only the downstream's own private project may import it.
EOF
fi
cp "$WORK/serial-build.log" "$DEST/build-serial.log"
[ "$KEEP" -eq 1 ] || rm -rf "$WORK"
echo "cloud-image: DONE"
echo "  image     $DEST/$NAME.tar.gz ($(du -h "$DEST/$NAME.tar.gz" | cut -f1); disk.raw $((bytes / 1073741824)) GiB, $(du -h "$DEST/disk.raw" | cut -f1) allocated)"
echo "  sha256    $(cut -d' ' -f1 "$DEST/$NAME.tar.gz.sha256")"
echo "  payload   ${PAYLOAD:-unknown}$([ "$PRIVATE" = 1 ] && echo ' — PRIVATE, see PRIVATE-DO-NOT-PUBLISH')"
echo "  models    $CARRIES_MODELS"
echo "  test it:  build/cloud-image-test.sh $DEST"
