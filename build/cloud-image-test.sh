#!/bin/sh
# SPDX-FileCopyrightText: 2026 The RIVER Authors
# SPDX-License-Identifier: MIT
#
# cloud-image-test.sh — boot a cloud image built by build/cloud-image.sh under QEMU/KVM as a
# stand-in for a Compute Engine instance, as the calling user, and check it.
#
#   build/cloud-image-test.sh [options] IMAGE_DIR      (the directory holding disk.raw)
#
# The stand-in: OVMF (UEFI), the image on an NVMe disk (the build used virtio-scsi, so the
# initramfs must carry both), grown to a larger disk than the image, a virtio-net NIC on
# QEMU user-mode networking with IPv4 and IPv6, and a FAKE METADATA SERVER at
# 169.254.169.254:80 (installer/fakemeta, run per connection through QEMU's guestfwd). It
# serves instance/id, instance/hostname, ssh-keys, a service-account token, and the Secret
# Manager access call for the data-key secret and the model passphrase. disk.raw is never
# written: the guest runs on a qcow2 overlay.
#
# Assertions (each PASS/FAIL/SKIP):
#   ssh-login      the metadata ssh-keys key logs in as runink (and nothing else was needed)
#   hostname       the hostname is the first label of instance/hostname
#   serial-getty   a login prompt reached the serial console (ttyS0)
#   pool-expanded  the pool grew past the image's size to the larger disk
#   data-encrypted every <pool>/data dataset is aes-256-gcm under the <pool>/data root, mounted
#   key-provider   the data key was sealed and unsealed by the Secret Manager provider
#   k0s-ready      the k0s node reports Ready
#   health         "RUNINK-HEALTH ready" reached the serial console (docs/CLOUD-IMAGES.md)
#   golden         tests/assert-golden.sh passes on the instance
#   models         the model payload was unpacked into <pool>/data/models and verifies
#                  (SKIP when the image carries no model payload)
#   reboot-unlock  after a reboot the data datasets unlock through the provider again, with
#                  the same instance identity (host key unchanged)
#   vtpm           SKIP: TPM sealing is not implemented (and swtpm is needed to test it)
#
# Options:
#   --models-passphrase-file F  the model payload passphrase (default
#                               ~/.cache/river-build/secrets/models-passphrase when present)
#   --disk-size SIZE            the instance's disk (default: image + 32G)
#   --mem MiB --cpus N          default 6144, 4
#   --data-disk                 also attach a blank 32 GiB data disk (virtio-scsi, vendor Google,
#                               model PersistentDisk, device name runink-data) and set
#                               runink-data-device-name: the data datasets must land on it
#   --work DIR --keep
set -eu

HERE="$(cd "$(dirname "$0")" && pwd)"
REPO="$(cd "$HERE/.." && pwd)"
CACHE="${XDG_CACHE_HOME:-$HOME/.cache}"
# shellcheck source=build/qemu-lib.sh
. "$HERE/qemu-lib.sh"

PASSF="" DISK_SIZE="" MEM=6144 CPUS=4 WORK="" KEEP=0 IMG="" DATA_DISK=0
[ -r "$CACHE/river-build/secrets/models-passphrase" ] && PASSF="$CACHE/river-build/secrets/models-passphrase"
die() { echo "cloud-image-test: $*" >&2; exit 2; }
while [ $# -gt 0 ]; do
	case "$1" in
		--models-passphrase-file) PASSF="${2:?}"; shift ;;
		--disk-size) DISK_SIZE="${2:?}"; shift ;;
		--mem) MEM="${2:?}"; shift ;;
		--cpus) CPUS="${2:?}"; shift ;;
		--work) WORK="${2:?}"; shift ;;
		--keep) KEEP=1 ;;
		--data-disk) DATA_DISK=1 ;;
		-h|--help) sed -n '5,44p' "$0" | sed 's/^# \{0,1\}//'; exit 2 ;;
		-*) die "unknown option $1" ;;
		*) IMG="$1" ;;
	esac
	shift
done
[ -n "$IMG" ] && [ -f "$IMG/disk.raw" ] || die "usage: $0 [options] IMAGE_DIR (with disk.raw)"
missing=""
for t in qemu-system-x86_64 qemu-img go ssh ssh-keygen scp base64; do command -v "$t" >/dev/null 2>&1 || missing="$missing $t"; done
ovmf_check || missing="$missing OVMF"
[ -w /dev/kvm ] || missing="$missing /dev/kvm"
[ -z "$missing" ] || die "missing:$missing"
img_gib=$(( $(stat -c %s "$IMG/disk.raw") / 1073741824 ))
[ -n "$DISK_SIZE" ] || DISK_SIZE="$((img_gib + 32))G"
[ -n "$WORK" ] || WORK="$CACHE/river-build/cloud-test/$(date +%Y%m%d-%H%M%S)"
case "$WORK" in /tmp/*) die "$WORK is on tmpfs; the overlay grows by the unpacked models (~20 GB)" ;; esac
mkdir -p "$WORK"
chmod 0700 "$WORK"
MODEL_PAYLOAD="$(sed -n 's/^MODEL_PAYLOAD=//p' "$IMG/image.env" 2>/dev/null || echo unknown)"
DOWNSTREAM="$(sed -n 's/^DOWNSTREAM_PAYLOAD=//p' "$IMG/image.env" 2>/dev/null || echo unknown)"

# --- the stand-in instance -----------------------------------------------------------------------
cleanup() {
	qemu_cleanup
	rm -f "$WORK/meta.json"   # holds the test secrets (the KEK and, if given, the passphrase)
	[ "$KEEP" -eq 1 ] || rm -f "$WORK/overlay.qcow2" "$WORK/data.qcow2"
}
trap cleanup EXIT INT TERM
( cd "$REPO/installer" && CGO_ENABLED=0 go build -trimpath -o "$WORK/river-fake-metadata" ./fakemeta )
rm -f "$WORK/id_ed25519" "$WORK/id_ed25519.pub" "$WORK/known_hosts"
ssh-keygen -q -t ed25519 -N '' -C cloud-test -f "$WORK/id_ed25519"
PUB="$(cut -d' ' -f1,2 "$WORK/id_ed25519.pub")"
KEK="$(head -c 32 /dev/urandom | base64 -w0)"
PASS_B64=""
[ -n "$PASSF" ] && [ -r "$PASSF" ] && PASS_B64="$(head -n 1 "$PASSF" | tr -d '\n' | base64 -w0)"
DATA_ATTR="" DPOOL=zriver DATA_ARGS=""
if [ "$DATA_DISK" -eq 1 ]; then
	DATA_ATTR=',
    "runink-data-device-name": "runink-data"'
	DPOOL=zriver-data
	qemu-img create -q -f qcow2 "$WORK/data.qcow2" 32G
	DATA_ARGS="-device virtio-scsi-pci,id=scsi1 -drive if=none,id=d1,format=qcow2,file=$WORK/data.qcow2
 -device scsi-hd,drive=d1,bus=scsi1.0,vendor=Google,product=PersistentDisk,serial=runink-data"
fi
IID="$(od -An -tu8 -N8 /dev/urandom | tr -d ' ' | cut -c1-19)"
HOSTNAME_FQDN="river-cloud-test.europe-west4-a.c.example-project.internal"
( umask 077; cat > "$WORK/meta.json" <<EOF
{
  "instance_id": "$IID",
  "hostname": "$HOSTNAME_FQDN",
  "token": "test-token-$(od -An -tx1 -N8 /dev/urandom | tr -d ' ')",
  "project_id": "example-project",
  "project_number": "123456789012",
  "attributes": {
    "ssh-keys": "tester:$PUB tester@example.org",
    "block-project-ssh-keys": "TRUE",
    "serial-port-enable": "FALSE",
    "runink-key-secret": "river-kek",
    "runink-models-passphrase-secret": "projects/example-project/secrets/river-models",
    "runink-secretmanager-endpoint": "http://169.254.169.254/secretmanager/v1"$DATA_ATTR
  },
  "project_attributes": {
    "ssh-keys": "expired:ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIExpiredExpiredExpiredExpiredExpiredExpired00 google-ssh {\"userName\":\"expired@example.org\",\"expireOn\":\"2020-01-01T00:00:00+0000\"}"
  },
  "secrets": {
    "projects/example-project/secrets/river-kek": {"1": "$KEK"}$( [ -n "$PASS_B64" ] && printf ',\n    "projects/example-project/secrets/river-models": {"1": "%s"}' "$PASS_B64")
  }
}
EOF
)
qemu-img create -q -f qcow2 -b "$IMG/disk.raw" -F raw "$WORK/overlay.qcow2" "$DISK_SIZE"
cp "$OVMF_VARS" "$WORK/vars.fd"
PORT=$(( 20000 + $(od -An -tu2 -N2 /dev/urandom | tr -d ' ') % 20000 ))
# QEMU's user-mode network only forwards addresses INSIDE its own virtual network, so the
# guest network is 169.254.0.0/16 (the guest gets 169.254.0.15 by DHCP) and 169.254.169.254
# falls inside it, as the metadata address does on GCE (link-local, next to the VPC subnet).
# QEMU runs this once per guest connection, with the socket on stdin/stdout. A wrapper,
# because QEMU_ARGS is word-split and a command with arguments would be split with it.
FAKE="$WORK/fake-metadata.sh"
printf '#!/bin/sh\nexec "%s/river-fake-metadata" --stdio --config "%s/meta.json" --log "%s/metadata-requests.log"\n' "$WORK" "$WORK" "$WORK" > "$FAKE"
chmod 0700 "$FAKE"
case "$FAKE" in *[,\ ]*) die "the work path must not contain a comma or a space (QEMU option syntax)" ;; esac
# vTPM: Shielded VM gives the instance a TPM 2.0; the stand-in would need swtpm.
TPM_ARGS=""
if command -v swtpm >/dev/null 2>&1; then
	mkdir -p "$WORK/tpm"
	swtpm socket --tpm2 --tpmstate dir="$WORK/tpm" --ctrl type=unixio,path="$WORK/tpm/sock" --daemon
	TPM_ARGS="-chardev socket,id=tpm0,path=$WORK/tpm/sock -tpmdev emulator,id=tpm0,chardev=tpm0 -device tpm-crb,tpmdev=tpm0"
fi
QEMU_ARGS="-enable-kvm -machine q35 -cpu host -m $MEM -smp $CPUS -display none
 -drive if=pflash,format=raw,readonly=on,file=$OVMF_CODE -drive if=pflash,format=raw,file=$WORK/vars.fd
 -drive if=none,id=d0,format=qcow2,file=$WORK/overlay.qcow2 -device nvme,drive=d0,serial=RIVERGCE0001,bootindex=0
 -netdev user,id=n0,ipv4=on,ipv6=on,net=169.254.0.0/16,host=169.254.0.2,dns=169.254.0.3,dhcpstart=169.254.0.15,hostfwd=tcp:127.0.0.1:$PORT-:22,guestfwd=tcp:169.254.169.254:80-cmd:$FAKE
 -device virtio-net-pci,netdev=n0 $TPM_ARGS $DATA_ARGS"

SSH="ssh -p $PORT -i $WORK/id_ed25519 -o IdentitiesOnly=yes -o BatchMode=yes -o ConnectTimeout=10
 -o StrictHostKeyChecking=accept-new -o UserKnownHostsFile=$WORK/known_hosts -o LogLevel=ERROR runink@127.0.0.1"
# shellcheck disable=SC2086
g() { $SSH "$@"; }
wait_ssh() { # seconds
	i=0
	while [ "$i" -lt "$1" ]; do
		g true 2>/dev/null && return 0
		kill -0 "$QPID" 2>/dev/null || return 1
		sleep 10; i=$((i + 10))
	done
	return 1
}
R="$WORK/results.txt"; : > "$R"
res() { echo "$1 $2${3:+ ($3)}" | tee -a "$R"; }
status() { g "sudo river-cloud-init status" 2>/dev/null | sed -n "s/^$1=//p"; }

echo "cloud-image-test: image $IMG (disk $img_gib GiB, model payload $MODEL_PAYLOAD, downstream payload $DOWNSTREAM)"
echo "cloud-image-test: instance disk $DISK_SIZE on NVMe, NIC virtio-net (user mode, IPv4+IPv6), ssh on 127.0.0.1:$PORT, work $WORK"
: > "$WORK/serial-boot1.log"
start_vm boot1
if wait_ssh 900; then
	res PASS ssh-login "metadata key for 'tester' logs in as runink"
else
	res FAIL ssh-login "no SSH within 15 min; serial log $WORK/serial-boot1.log"
	tail -40 "$WORK/serial-boot1.log" | tr -d '\r' >&2
	exit 1
fi
hk1="$(ssh-keygen -F "[127.0.0.1]:$PORT" -f "$WORK/known_hosts" | grep -v '^#' | awk '{print $3}' | sort | head -1)"

# Wait for river-cloud-init to finish (rc-local depends on it).
i=0; while [ "$i" -lt 600 ] && [ -z "$(status data)" ]; do sleep 10; i=$((i + 10)); done
g "sudo river-cloud-init status" | sed 's/^/  status: /'

h="$(g hostname)"
[ "$h" = river-cloud-test ] && res PASS hostname "$h" || res FAIL hostname "got $h"
if tr -d '\r' < "$WORK/serial-boot1.log" | grep -aq 'login:'; then res PASS serial-getty; else res FAIL serial-getty "no login prompt on ttyS0"; fi

size="$(g "sudo zpool list -Hp -o size zriver" 2>/dev/null || echo 0)"
if [ "${size:-0}" -gt $((img_gib * 1073741824)) ]; then res PASS pool-expanded "$((size / 1073741824)) GiB > image $img_gib GiB ($(status grow))"
else res FAIL pool-expanded "size $size, $(status grow)"; fi

enc="$(g "sudo zfs list -H -r -o name,encryption,encryptionroot,keystatus,mounted,canmount $DPOOL/data" 2>/dev/null || true)"
printf '%s\n' "$enc" | sed 's/^/  /'
nbad="$(printf '%s\n' "$enc" | awk -v dr="$DPOOL/data" '$1 != "" && ($2 != "aes-256-gcm" || $3 != dr || $4 != "available" || ($6 == "on" && $5 != "yes")) {n++} END {print n+0}')"
ndata="$(printf '%s\n' "$enc" | grep -c .)"
if [ "$ndata" -ge 7 ] && [ "$nbad" -eq 0 ]; then res PASS data-encrypted "$ndata datasets under $DPOOL/data"
else res FAIL data-encrypted "$ndata datasets, $nbad wrong"; fi

prov="$(status unlock_provider)"
blob="$(g "sudo zfs get -H -o value -s local org.runink:key.gcp-secret-manager $DPOOL/data" 2>/dev/null | head -c 12 || true)"
if [ "$prov" = gcp-secret-manager ] && [ -n "$blob" ]; then res PASS key-provider "sealed + unlocked via $prov; sealed key stored on $DPOOL/data"
else res FAIL key-provider "provider '$prov', sealed key '$blob'"; fi
if [ "$DATA_DISK" -eq 1 ]; then
	dd="$(status data_disk)"
	if [ -n "$dd" ] && ! g "sudo zfs list zriver/data" >/dev/null 2>&1; then res PASS data-disk "$DPOOL on $dd (found by device name runink-data)"
	else res FAIL data-disk "data_disk='$dd'"; fi
fi

i=0; ready=""
while [ "$i" -lt 1200 ]; do
	ready="$(g "sudo k0s kubectl get nodes --no-headers" 2>/dev/null || true)"
	printf '%s\n' "$ready" | awk '$2 == "Ready"' | grep -q . && break
	sleep 20; i=$((i + 20))
done
if printf '%s\n' "$ready" | awk '$2 == "Ready"' | grep -q .; then res PASS k0s-ready "$(printf '%s' "$ready" | tr -s ' ' | head -1)"
else
	res FAIL k0s-ready "${ready:-no node}"
	g "sudo k0s kubectl get pods -A -o wide; sudo tail -30 /var/log/k0s.log 2>/dev/null" 2>&1 | sed 's/^/  /' | tail -40 || true
fi

i=0
while [ "$i" -lt 1200 ] && ! tr -d '\r' < "$WORK/serial-boot1.log" | grep -aq 'RUNINK-HEALTH '; do sleep 10; i=$((i + 10)); done
hl="$(tr -d '\r' < "$WORK/serial-boot1.log" | grep -a -o 'RUNINK-HEALTH [a-z]*.*' | head -1)"
case "$hl" in "RUNINK-HEALTH ready"*) res PASS health "$hl on the serial console" ;; *) res FAIL health "${hl:-no RUNINK-HEALTH line on the serial console}" ;; esac

if [ "$MODEL_PAYLOAD" = yes ]; then
	i=0; m=""
	while [ "$i" -lt 3600 ]; do
		m="$(status models)"
		case "$m" in installed*|failed*|deferred*) break ;; esac
		sleep 30; i=$((i + 30))
	done
	if case "$m" in installed*) true ;; *) false ;; esac &&
		g "cd /var/lib/core/models/shared && sha256sum --quiet -c /usr/local/share/runink/models.manifest && river-modelpack verify --dir . --lock /usr/local/share/runink/models.lock >/dev/null && ! sudo zfs list zriver/payload >/dev/null 2>&1"; then
		res PASS models "$m; payload dataset destroyed"
	else res FAIL models "${m:-no status}"; fi
else
	res SKIP models "the image carries no model payload"
fi

scp -q -P "$PORT" -i "$WORK/id_ed25519" -o IdentitiesOnly=yes -o BatchMode=yes -o UserKnownHostsFile="$WORK/known_hosts" \
	"$REPO/tests/assert-golden.sh" runink@127.0.0.1:/tmp/assert-golden.sh
if g "sudo sh /tmp/assert-golden.sh" > "$WORK/golden.txt" 2>&1; then res PASS golden "$(tail -1 "$WORK/golden.txt")"
else res FAIL golden "$(tail -1 "$WORK/golden.txt")"; grep FAIL "$WORK/golden.txt" | sed 's/^/  /'; fi

# --- reboot: unlock again through the provider, same identity ---------------------------------------
echo "cloud-image-test: rebooting the instance"
g "sudo reboot" 2>/dev/null || true
# Wait for the old system to go away before waiting for the new one.
i=0; while [ "$i" -lt 180 ] && g true 2>/dev/null; do sleep 5; i=$((i + 5)); done
if wait_ssh 900 && i=0 && while [ "$i" -lt 600 ] && [ -z "$(status data)" ]; do sleep 10; i=$((i + 10)); done; then
	hk2="$(ssh-keygen -F "[127.0.0.1]:$PORT" -f "$WORK/known_hosts" | grep -v '^#' | awk '{print $3}' | sort | head -1)"
	d="$(status data)"; p="$(status unlock_provider)"; fb="$(status first_boot_on_instance)"
	mnt="$(g "sudo zfs get -H -o value mounted $DPOOL/data/containers $DPOOL/data/etc-runink" | tr '\n' ' ')"
	if [ "$d" = unlocked ] && [ "$p" = gcp-secret-manager ] && [ "$fb" = false ] && [ "$hk1" = "$hk2" ] && [ "$mnt" = "yes yes " ]; then
		res PASS reboot-unlock "data $d via $p, same host key"
	else res FAIL reboot-unlock "data=$d provider=$p first_boot=$fb mounted=$mnt hostkey-same=$([ "$hk1" = "$hk2" ] && echo yes || echo no)"; fi
else
	res FAIL reboot-unlock "the instance did not come back"
fi
if [ -n "$TPM_ARGS" ]; then res SKIP vtpm "a TPM was attached, but TPM sealing is not implemented yet"
else res SKIP vtpm "TPM sealing not implemented; swtpm not installed on this host"; fi
g "sudo poweroff" 2>/dev/null || true
wait_exit 120 || true

echo
echo "cloud-image-test: results ($WORK)"
sed 's/^/  /' "$R"
echo "  metadata requests: $(wc -l < "$WORK/metadata-requests.log" 2>/dev/null || echo 0) (log $WORK/metadata-requests.log)"
if grep -q '^FAIL' "$R"; then echo "cloud-image-test: FAIL"; exit 1; fi
echo "cloud-image-test: PASS"
