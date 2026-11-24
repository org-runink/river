#!/bin/sh
# SPDX-FileCopyrightText: 2026 The RIVER Authors
# SPDX-License-Identifier: MIT
#
# cloud-base-test.sh — boot the cloud edition's disk.raw (build/cloud-image.sh, no downstream
# profile) under QEMU/KVM with Compute Engine-like hardware, as the calling user, and check it.
#
#   build/cloud-base-test.sh [options] IMAGE_DIR      (the directory holding disk.raw)
#   build/cloud-base-test.sh --selftest               (no VM: proves each check can fail)
#
# The stand-in instance: OVMF (UEFI), the disk on NVMe or on virtio-scsi (vendor Google, product
# PersistentDisk), ENLARGED past the image, a virtio-net NIC on QEMU user-mode networking, the
# serial port as the only console (no display), and a FAKE METADATA SERVER at 169.254.169.254:80
# (installer/fakemeta, per connection through QEMU's guestfwd) serving instance/id,
# instance/hostname, `ssh-keys` for the user `tester` (who does not exist in the image) and a
# `startup-script`. disk.raw is never written: the guest runs on a qcow2 overlay.
#
# Evidence is collected into files, and each check below reads only those files, so it can be
# run against crafted evidence (--selftest). Checks (PASS or FAIL, exit status 1 on any FAIL):
#   serial-login    a login prompt reached the serial console (ttyS0)
#   console         the kernel command line has console=ttyS0,115200 and no tty0 console
#   sshd-up         sshd answers on the forwarded port with a host key
#   ssh-key-login   the metadata key logs in as `tester`, an account created at boot
#   sudo            `tester` has passwordless sudo (the metadata users are administrators)
#   hostname        the hostname is the first label of instance/hostname
#   root-resized    the root pool grew to the enlarged disk
#   no-listeners    no socket listens on a non-loopback address except tcp 22 (and the DHCP
#                   client's udp 68)
#   password-refused  a password login is refused
#   root-refused    root login is refused, with the metadata key
#   no-desktop      no desktop package is installed and no desktop service runs
#   startup-script  the startup-script metadata value ran, once, with exit 0
#
# Options:
#   --variant nvme|scsi|both   disk controller (default both: two boots, one after the other)
#   --mem MiB --cpus N         default 4096, 2
#   --mutate NAME              break one thing on purpose; the named check MUST then fail
#                              (the run then exits 0 only if it did): key, hostname, resize,
#                              listener
#   --work DIR --keep
#
# --selftest builds good evidence, expects every check to pass, then breaks one piece of
# evidence per check and expects exactly that check to fail.
set -eu

HERE="$(cd "$(dirname "$0")" && pwd)"
REPO="$(cd "$HERE/.." && pwd)"
CACHE="${XDG_CACHE_HOME:-$HOME/.cache}"

CHECKS="serial-login console sshd-up ssh-key-login sudo hostname root-resized no-listeners password-refused root-refused no-desktop startup-script"
HOSTNAME_FQDN="river-cloud-test.europe-west4-a.c.example-project.internal"
WANT_HOST="river-cloud-test"

# --- the checks: each reads $EV and prints a detail; the return status is the verdict ------------
ev() { cat "$EV/$1" 2>/dev/null || true; }

chk_serial_login() { tr -d '\r' < "$EV/serial.log" 2>/dev/null | grep -aq 'login:'; }
chk_console() {
	c="$(ev cmdline)"
	case " $c " in *" console=ttyS0,115200n8 "*) ;; *) echo "cmdline: $c"; return 1 ;; esac
	case " $c " in *" console=tty0 "*|*" console=tty1 "*) echo "a VT console is on the command line"; return 1 ;; esac
}
chk_sshd_up() { ev keyscan | grep -q ' ssh-\| ecdsa-'; }
chk_ssh_key_login() { [ "$(ev ssh_id)" = tester ]; }
chk_sudo() { [ "$(ev sudo_id)" = 0 ]; }
chk_hostname() { [ "$(ev hostname)" = "$WANT_HOST" ] || { echo "got '$(ev hostname)', want $WANT_HOST"; return 1; }; }
chk_root_resized() {
	p="$(ev pool_bytes)"; d="$(ev disk_bytes)"; i="$(ev image_bytes)"
	case "$p$d$i" in ''|*[!0-9]*) echo "missing numbers: pool=$p disk=$d image=$i"; return 1 ;; esac
	[ "$d" -gt "$i" ] || { echo "the disk ($d) is not larger than the image ($i)"; return 1; }
	[ "$p" -gt "$i" ] && [ "$p" -ge $((d - 1610612736)) ] || { echo "pool $p, disk $d, image $i"; return 1; }
	echo "pool $((p / 1048576)) MiB on a $((d / 1048576)) MiB disk (image $((i / 1048576)) MiB)"
}
# ss -H -lntu lines: Netid State Recv-Q Send-Q Local:Port Peer:Port
chk_no_listeners() {
	f="$EV/listen"
	[ -s "$f" ] || { echo "no socket list collected"; return 1; }
	extra="$(awk '
		{ l = $5; n = split(l, a, ":"); port = a[n]; addr = substr(l, 1, length(l) - length(port) - 1)
		  sub(/%.*/, "", addr)
		  if (addr ~ /^127\./ || addr == "[::1]" || addr == "::1") next
		  if ($1 == "tcp" && port == "22") next
		  if ($1 == "udp" && port == "68") next
		  print $1 " " l }' "$f")"
	[ -z "$extra" ] || { echo "listening: $(echo "$extra" | tr '\n' ';')"; return 1; }
	grep -q '^tcp.*:22 ' "$f" || { echo "nothing listens on tcp 22"; return 1; }
}
chk_password_refused() { grep -q 'Permission denied' "$EV/pwlogin_out" 2>/dev/null && [ "$(ev pwlogin_rc)" != 0 ]; }
chk_root_refused() { grep -q 'Permission denied' "$EV/rootlogin_out" 2>/dev/null && [ "$(ev rootlogin_rc)" != 0 ]; }
chk_no_desktop() {
	[ -s "$EV/pkgs" ] || { echo "no package query collected"; return 1; }
	[ "$(grep -c 'was not found' "$EV/pkgs")" -ge 4 ] || { echo "$(tr '\n' ';' < "$EV/pkgs")"; return 1; }
	grep -q 'service-list-ok' "$EV/services" 2>/dev/null || { echo "no service list collected"; return 1; }
	! grep -qE 'greetd|sddm|gtkgreet|cage|desk' "$EV/services" || { echo "a desktop service runs"; return 1; }
}
chk_startup_script() {
	[ "$(ev startup_status)" = ran-exit-0 ] || { echo "status '$(ev startup_status)'"; return 1; }
	[ "$(grep -c . "$EV/marker" 2>/dev/null)" = 1 ] || { echo "marker has $(grep -c . "$EV/marker" 2>/dev/null || echo 0) line(s), want 1"; return 1; }
}

run_checks() { # run_checks EVDIR -> prints results, appends them to EVDIR/results
	EV="$1"; : > "$EV/results"
	for c in $CHECKS; do
		fn="chk_$(echo "$c" | tr - _)"
		d="$($fn 2>&1)" && r=PASS || { r=FAIL; true; }
		echo "$r $c${d:+ ($d)}"
		echo "$r $c" >> "$EV/results"
	done
}

# --- --selftest: every check must be able to fail --------------------------------------------------
mk_good() { # mk_good DIR
	d="$1"; mkdir -p "$d"
	printf 'Starting\nriver-cloud-test login: \n' > "$d/serial.log"
	echo 'BOOT_IMAGE=/vmlinuz-linux-runink root=ZFS=zriver/ROOT/runink-1 rw console=ttyS0,115200n8' > "$d/cmdline"
	echo '[127.0.0.1]:2222 ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIexample' > "$d/keyscan"
	echo tester > "$d/ssh_id"; echo 0 > "$d/sudo_id"; echo "$WANT_HOST" > "$d/hostname"
	echo 10737418240 > "$d/image_bytes"; echo 16106127360 > "$d/disk_bytes"; echo 14800000000 > "$d/pool_bytes"
	printf 'tcp LISTEN 0 128 0.0.0.0:22 0.0.0.0:*\ntcp LISTEN 0 128 [::]:22 [::]:*\nudp UNCONN 0 0 127.0.0.1:323 0.0.0.0:*\nudp UNCONN 0 0 [::1]:323 [::]:*\nudp UNCONN 0 0 0.0.0.0%%enp0s3:68 0.0.0.0:*\n' > "$d/listen"
	printf 'tester@127.0.0.1: Permission denied (publickey).\n' > "$d/pwlogin_out"; echo 255 > "$d/pwlogin_rc"
	printf 'root@127.0.0.1: Permission denied (publickey).\n' > "$d/rootlogin_out"; echo 255 > "$d/rootlogin_rc"
	printf 'error: package '"'"'greetd'"'"' was not found\nerror: package '"'"'runink-desk'"'"' was not found\nerror: package '"'"'runink-gtkgreet'"'"' was not found\nerror: package '"'"'runink-cage'"'"' was not found\n' > "$d/pkgs"
	printf 'service-list-ok\nsshd-srv\nNetworkManager-srv\n' > "$d/services"
	echo ran-exit-0 > "$d/startup_status"; echo boot-1 > "$d/marker"
}
selftest() {
	root="$(mktemp -d "${TMPDIR:-/tmp}/cloud-base-selftest.XXXXXX")"
	trap 'rm -rf "$root"' EXIT
	mk_good "$root/good"
	echo "selftest: good evidence"
	run_checks "$root/good" | sed "s/^/  /"
	NFAIL="$(grep -c "^FAIL" "$root/good/results" || true)"
	[ "$NFAIL" -eq 0 ] || { echo "selftest: FAIL the good evidence does not pass"; exit 1; }
	bad=0
	mutate() { # mutate CHECK SHELL-COMMAND-IN-EVDIR
		rm -rf "$root/m"; cp -R "$root/good" "$root/m"; rm -f "$root/m/results"
		( cd "$root/m" && eval "$2" )
		EV="$root/m"; fn="chk_$(echo "$1" | tr - _)"
		if $fn >/dev/null 2>&1; then echo "  NOT-CAUGHT $1 (mutation: $2)"; bad=$((bad + 1)); return; fi
		# no other check may fail on this mutation: each mutation breaks exactly one thing
		for c in $CHECKS; do
			[ "$c" = "$1" ] && continue
			f="chk_$(echo "$c" | tr - _)"
			$f >/dev/null 2>&1 || { echo "  OVERBROAD $1: $c also fails (mutation: $2)"; bad=$((bad + 1)); }
		done
		echo "  caught  $1"
	}
	echo "selftest: one mutation per check"
	mutate serial-login ": > serial.log"
	mutate serial-login "echo 'Starting' > serial.log"
	mutate console "echo 'root=ZFS=zriver/ROOT/x rw' > cmdline"
	mutate console "echo 'console=ttyS0,115200n8 console=tty0' > cmdline"
	mutate console "echo 'console=ttyS0,38400n8' > cmdline"
	mutate sshd-up ": > keyscan"
	mutate ssh-key-login "echo root > ssh_id"
	mutate ssh-key-login "rm ssh_id"
	mutate sudo "echo 1000 > sudo_id"
	mutate hostname "echo runink > hostname"
	mutate root-resized "echo 10737418240 > disk_bytes; echo 10700000000 > pool_bytes"
	mutate root-resized "echo 10600000000 > pool_bytes"
	mutate root-resized "rm pool_bytes"
	mutate no-listeners "echo 'tcp LISTEN 0 128 0.0.0.0:2222 0.0.0.0:*' >> listen"
	mutate no-listeners "echo 'udp UNCONN 0 0 *:5353 *:*' >> listen"
	mutate no-listeners "echo 'tcp LISTEN 0 4096 [::]:8080 [::]:*' >> listen"
	mutate no-listeners "grep -v ':22 ' listen > l && mv l listen"
	mutate password-refused "echo 0 > pwlogin_rc; echo ok > pwlogin_out"
	mutate root-refused "echo 0 > rootlogin_rc; echo ok > rootlogin_out"
	mutate no-desktop "echo \"greetd 1.0-1\" > pkgs"
	mutate no-desktop "echo 'greetd-srv' >> services"
	mutate no-desktop "echo 'wlroots' > services"
	mutate startup-script "echo ran-exit-1 > startup_status"
	mutate startup-script "printf 'boot-1\nboot-1\n' > marker"
	mutate startup-script "rm marker"
	[ "$bad" -eq 0 ] || { echo "selftest: FAIL ($bad)"; exit 1; }
	echo "selftest: PASS (every check passes on good evidence and fails on its mutation)"
	exit 0
}

# --- arguments ------------------------------------------------------------------------------------
VARIANT=both MEM=4096 CPUS=2 WORK="" KEEP=0 IMG="" MUTATE=""
die() { echo "cloud-base-test: $*" >&2; exit 2; }
while [ $# -gt 0 ]; do
	case "$1" in
		--selftest) selftest ;;
		--variant) VARIANT="${2:?}"; shift ;;
		--mem) MEM="${2:?}"; shift ;;
		--cpus) CPUS="${2:?}"; shift ;;
		--mutate) MUTATE="${2:?}"; shift ;;
		--work) WORK="${2:?}"; shift ;;
		--keep) KEEP=1 ;;
		-h|--help) sed -n '5,50p' "$0" | sed 's/^# \{0,1\}//'; exit 2 ;;
		-*) die "unknown option $1" ;;
		*) IMG="$1" ;;
	esac
	shift
done
case "$VARIANT" in nvme|scsi|both) ;; *) die "--variant nvme, scsi or both" ;; esac
case "$MUTATE" in ''|key|hostname|resize|listener) ;; *) die "--mutate key, hostname, resize or listener" ;; esac
case "$MEM$CPUS" in *[!0-9]*) die "--mem and --cpus are numbers" ;; esac
[ -n "$IMG" ] && [ -f "$IMG/disk.raw" ] || die "usage: $0 [options] IMAGE_DIR (with disk.raw), or --selftest"

# shellcheck source=build/qemu-lib.sh
. "$HERE/qemu-lib.sh"
missing=""
for t in qemu-system-x86_64 qemu-img go ssh ssh-keygen ssh-keyscan; do command -v "$t" >/dev/null 2>&1 || missing="$missing $t"; done
ovmf_check || missing="$missing OVMF"
[ -w /dev/kvm ] || missing="$missing /dev/kvm"
[ -z "$missing" ] || die "missing:$missing"
img_bytes="$(stat -c %s "$IMG/disk.raw")"
img_gib=$((img_bytes / 1073741824))
DISK_SIZE="$((img_gib + 6))G"
[ "$MUTATE" != resize ] || DISK_SIZE="${img_gib}G"
[ -n "$WORK" ] || WORK="$CACHE/river-build/cloud-base-test/$(date +%Y%m%d-%H%M%S)"
case "$WORK" in /tmp/*) die "$WORK is on tmpfs" ;; esac
case "$WORK" in *[,\ ]*) die "the work path must not contain a comma or a space (QEMU option syntax)" ;; esac
mkdir -p "$WORK"; chmod 0700 "$WORK"
trap 'qemu_cleanup; [ "$KEEP" -eq 1 ] || rm -f "$WORK/overlay.qcow2"' EXIT INT TERM
( cd "$REPO/installer" && CGO_ENABLED=0 go build -trimpath -o "$WORK/river-fake-metadata" ./fakemeta )
rm -f "$WORK/id_ed25519" "$WORK/id_ed25519.pub" "$WORK/other" "$WORK/other.pub" "$WORK/known_hosts"
ssh-keygen -q -t ed25519 -N '' -C cloud-test -f "$WORK/id_ed25519"
ssh-keygen -q -t ed25519 -N '' -C other -f "$WORK/other"
PUB="$(cut -d' ' -f1,2 "$WORK/id_ed25519.pub")"
[ "$MUTATE" != key ] || PUB="$(cut -d' ' -f1,2 "$WORK/other.pub")"     # the metadata carries a key we do not hold
[ "$MUTATE" != hostname ] || WANT_HOST="not-the-hostname"
IID="$(od -An -tu8 -N8 /dev/urandom | tr -d ' ' | cut -c1-19)"
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
    "startup-script": "cat /proc/sys/kernel/random/boot_id >> /var/tmp/river-startup-marker"
  },
  "project_attributes": {}
}
EOF
)
FAKE="$WORK/fake-metadata.sh"
printf '#!/bin/sh\nexec "%s/river-fake-metadata" --stdio --config "%s/meta.json" --log "%s/metadata-requests.log"\n' "$WORK" "$WORK" "$WORK" > "$FAKE"
chmod 0700 "$FAKE"

SSHO="-i $WORK/id_ed25519 -o IdentitiesOnly=yes -o BatchMode=yes -o ConnectTimeout=10 -o StrictHostKeyChecking=accept-new -o UserKnownHostsFile=$WORK/known_hosts -o LogLevel=ERROR"
# shellcheck disable=SC2086
g() { ssh -p "$PORT" $SSHO tester@127.0.0.1 "$@"; }
gsay() { g "$@" 2>/dev/null || true; }

# boot_variant NAME DRIVE-ARGS... — one boot, evidence, checks; leaves $NFAIL
boot_variant() {
	name="$1"; shift
	EV="$WORK/ev-$name"; rm -rf "$EV"; mkdir -p "$EV"
	rm -f "$WORK/overlay.qcow2" "$WORK/known_hosts"
	qemu-img create -q -f qcow2 -b "$IMG/disk.raw" -F raw "$WORK/overlay.qcow2" "$DISK_SIZE"
	cp "$OVMF_VARS" "$WORK/vars.fd"
	PORT=$(( 20000 + $(od -An -tu2 -N2 /dev/urandom | tr -d ' ') % 20000 ))
	# QEMU's user-mode network forwards only inside its own network: use 169.254.0.0/16 so
	# that 169.254.169.254 falls inside it, as on a VPC.
	# shellcheck disable=SC2034 # read by start_vm (qemu-lib.sh)
	QEMU_ARGS="-enable-kvm -machine q35 -cpu host -m $MEM -smp $CPUS -display none
 -drive if=pflash,format=raw,readonly=on,file=$OVMF_CODE -drive if=pflash,format=raw,file=$WORK/vars.fd
 -drive if=none,id=d0,format=qcow2,file=$WORK/overlay.qcow2 $*
 -netdev user,id=n0,net=169.254.0.0/16,host=169.254.0.2,dns=169.254.0.3,dhcpstart=169.254.0.15,hostfwd=tcp:127.0.0.1:$PORT-:22,guestfwd=tcp:169.254.169.254:80-cmd:$FAKE
 -device virtio-net-pci,netdev=n0"
	: > "$WORK/serial-$name.log"
	echo "cloud-base-test: [$name] booting (disk $DISK_SIZE over a ${img_gib}G image, ssh 127.0.0.1:$PORT, serial only)"
	start_vm "$name"
	i=0; up=0
	while [ "$i" -lt 600 ]; do
		g true 2>/dev/null && { up=1; break; }
		kill -0 "$QPID" 2>/dev/null || break
		sleep 10; i=$((i + 10))
	done
	if [ "$up" -eq 1 ]; then
		# river-cloud-init has finished its first boot when the growth is recorded; the startup
		# script runs after it.
		i=0; while [ "$i" -lt 300 ]; do
			st="$(gsay "sudo -n river-cloud-init status")"
			case "$st" in *startup_script=*) break ;; esac
			sleep 5; i=$((i + 5))
		done
		gsay "id -un" > "$EV/ssh_id"
		gsay "sudo -n id -u" > "$EV/sudo_id"
		gsay hostname > "$EV/hostname"
		gsay "cat /proc/cmdline" > "$EV/cmdline"
		gsay "sudo -n zpool list -Hp -o size zriver" > "$EV/pool_bytes"
		gsay "lsblk -dnbo SIZE -e 7,11 | sort -n | tail -1" > "$EV/disk_bytes"
		[ "$MUTATE" != listener ] || gsay "sudo -n sh -c 'setsid /usr/bin/sshd -p 2222 </dev/null >/dev/null 2>&1 &'"
		sleep 2
		gsay "ss -H -lntu" > "$EV/listen"
		gsay "pacman -Q greetd runink-desk runink-gtkgreet runink-cage 2>&1" > "$EV/pkgs"
		{ echo service-list-ok; gsay "ls /run/service"; } > "$EV/services"
		gsay "sudo -n river-cloud-init status" | sed -n 's/^startup_script=//p' > "$EV/startup_status"
		gsay "cat /var/tmp/river-startup-marker" > "$EV/marker"
	else
		echo "cloud-base-test: [$name] no SSH within 10 minutes (serial log $WORK/serial-$name.log)" >&2
	fi
	echo "$img_bytes" > "$EV/image_bytes"
	ssh-keyscan -p "$PORT" -T 5 127.0.0.1 > "$EV/keyscan" 2>/dev/null || true
	# shellcheck disable=SC2086
	ssh -p "$PORT" -o PubkeyAuthentication=no -o PreferredAuthentications=password,keyboard-interactive -o BatchMode=yes -o ConnectTimeout=10 -o StrictHostKeyChecking=accept-new -o UserKnownHostsFile="$WORK/known_hosts" -o LogLevel=ERROR tester@127.0.0.1 true > "$EV/pwlogin_out" 2>&1 && echo 0 > "$EV/pwlogin_rc" || echo $? > "$EV/pwlogin_rc"
	# shellcheck disable=SC2086
	ssh -p "$PORT" $SSHO root@127.0.0.1 true > "$EV/rootlogin_out" 2>&1 && echo 0 > "$EV/rootlogin_rc" || echo $? > "$EV/rootlogin_rc"
	tr -d '\r' < "$WORK/serial-$name.log" > "$EV/serial.log"
	[ "$up" -eq 0 ] || g "sudo -n poweroff" 2>/dev/null || true
	wait_exit 90 || true
	run_checks "$EV" | sed "s/^/  [$name] /"
	NFAIL="$(grep -c "^FAIL" "$EV/results" || true)"
}

TOTAL_FAIL=0
if [ "$VARIANT" = nvme ] || [ "$VARIANT" = both ]; then
	boot_variant nvme "-device nvme,drive=d0,serial=RIVERGCE0001,bootindex=0"
	TOTAL_FAIL=$((TOTAL_FAIL + NFAIL))
fi
if [ "$VARIANT" = scsi ] || [ "$VARIANT" = both ]; then
	boot_variant scsi "-device virtio-scsi-pci,id=scsi0 -device scsi-hd,drive=d0,bus=scsi0.0,vendor=Google,product=PersistentDisk,serial=RIVERGCE0002,bootindex=0"
	TOTAL_FAIL=$((TOTAL_FAIL + NFAIL))
fi

echo
if [ -n "$MUTATE" ]; then
	want="$(case "$MUTATE" in key) echo ssh-key-login;; hostname) echo hostname;; resize) echo root-resized;; listener) echo no-listeners;; esac)"
	if grep -q "^FAIL $want\$" "$WORK"/ev-*/results; then
		echo "cloud-base-test: mutation '$MUTATE': check '$want' FAILED as it must (PASS for this run)"; exit 0
	fi
	echo "cloud-base-test: mutation '$MUTATE' was NOT caught by '$want'" >&2; exit 1
fi
if [ "$TOTAL_FAIL" -ne 0 ]; then echo "cloud-base-test: FAIL ($TOTAL_FAIL failed check(s); evidence under $WORK)"; exit 1; fi
echo "cloud-base-test: PASS"
