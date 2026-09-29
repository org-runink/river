#!/bin/sh
# assert-golden.sh — verify a running node matches the golden-image contract.
# Run ON the target (after install + boot), or via ssh. Exits non-zero on any failure.
set -u

pass=0; fail=0
ok()   { echo "  OK   $*"; pass=$((pass+1)); }
bad()  { echo "  FAIL $*" >&2; fail=$((fail+1)); }

echo "== assert-golden =="

# 1. Single kernel (linux-runink only).
if command -v pacman >/dev/null 2>&1; then
	if pacman -Q linux >/dev/null 2>&1; then bad "mainline 'linux' kernel present (want linux-runink only)"; else ok "no mainline kernel"; fi
	# linux-runink (zen-kernel 7.2.x stable fork) + its matched runink-zfs since 2026-09 — the repo's
	# own kernel/ZFS pair, replacing Artix's linux-lts (os#70).
	pacman -Q linux-runink >/dev/null 2>&1 && ok "linux-runink installed" || bad "linux-runink missing"
	if pacman -Q linux-lts >/dev/null 2>&1; then bad "linux-lts still installed (two kernels)"; else ok "no leftover linux-lts"; fi
	# The ZFS module must actually exist for the kernel that is installed. This is the
	# node-side counterpart of the build-time guard: both ISOs shipped on 2026-09-05 with no
	# module at all, and nothing on the node would have said so.
	if find /usr/lib/modules/*-runink/extra -name 'zfs.ko*' 2>/dev/null | grep -q .; then
		ok "zfs module present for linux-runink"
	else
		bad "no zfs.ko under /usr/lib/modules/*-runink/extra — ZFS root cannot import"
	fi
	# Forbidden desktop stack must be absent. The one exception is the graphical first boot's
	# kiosk (a web view pulls mesa): it is allowed while that first boot is pending, and checked
	# gone once it has finished (section 11).
	if [ -e /var/lib/runink/firstboot-ui ]; then
		ok "graphical first boot pending: its kiosk may pull the desktop stack until it finishes"
	else
		for p in opencv vtk hdf5 glew ffmpeg mesa xorg-server pipewire pulseaudio; do
			pacman -Q "$p" >/dev/null 2>&1 && bad "forbidden package installed: $p" || :
		done
		ok "forbidden desktop stack absent"
	fi
fi

# 2. Init is s6, not systemd.
[ -d /run/s6 ] || [ -x /usr/bin/s6-svscan ] && ok "s6 present" || bad "s6 not detected"
command -v systemctl >/dev/null 2>&1 && bad "systemctl present (should be s6-only)" || ok "no systemd"

# 3. ZFS root.
if command -v zfs >/dev/null 2>&1; then
	rootds="$(findmnt -n -o SOURCE / 2>/dev/null || true)"
	case "$rootds" in */ROOT/*) ok "ZFS root ($rootds)";; *) bad "root is not a ZFS BE ($rootds)";; esac
	[ -s /etc/hostid ] && ok "/etc/hostid present" || bad "/etc/hostid missing"
fi

# 3a. ZFS native encryption on EVERY dataset, every key loaded.
#     New installs make the pool root the aes-256-gcm encryption root (10-disk-zfs), so
#     every filesystem and volume inherits it. A plaintext dataset here is either a
#     pre-encryption install (migrate: docs/ENCRYPTION.md) or something created with
#     encryption=off under a plaintext parent — both are failures of the contract.
#     A CLOUD IMAGE (/etc/river-cloud/cloud.env; docs/CLOUD-IMAGES.md) is the one exception:
#     its boot environment (<pool>, <pool>/ROOT, <pool>/ROOT/*) holds only public code and
#     <pool>/payload only model-payload ciphertext; everything else lives under
#     <pool>/data (the boot pool, or zriver-data on a data disk), an aes-256-gcm encryption
#     root keyed per instance, which must exist.
CLOUD=""
[ -f /etc/river-cloud/cloud.env ] && CLOUD="$(sed -n 's/^RIVER_CLOUD=//p' /etc/river-cloud/cloud.env)"
if command -v zfs >/dev/null 2>&1; then
	n_ds=0; n_bad=0; n_plain=0; n_roots=0
	for pool in $(zpool list -H -o name 2>/dev/null); do
		if [ -n "$CLOUD" ] && [ "$(zfs get -H -o value encryption "$pool/data" 2>/dev/null)" = aes-256-gcm ] &&
			[ "$(zfs get -H -o value encryptionroot "$pool/data" 2>/dev/null)" = "$pool/data" ]; then
			ok "cloud image ($CLOUD): $pool/data is the aes-256-gcm encryption root"; n_roots=$((n_roots+1))
		fi
		while read -r ds enc ks; do
			n_ds=$((n_ds+1))
			if [ "$enc" = off ] && [ -n "$CLOUD" ] && case "$ds" in
				"$pool"|"$pool/ROOT"|"$pool/ROOT/"*|"$pool/payload") true ;; *) false ;; esac; then
				n_plain=$((n_plain+1))
			elif [ "$enc" = off ]; then
				bad "dataset $ds is NOT encrypted"; n_bad=$((n_bad+1))
			elif [ "$ks" != available ]; then
				bad "dataset $ds is encrypted ($enc) but keystatus=$ks"; n_bad=$((n_bad+1))
			fi
		done <<-EOF
			$(zfs get -H -p -r -t filesystem,volume -o name,value encryption,keystatus "$pool" 2>/dev/null \
				| awk '{ v[$1] = v[$1] " " $2 } END { for (d in v) print d v[d] }')
		EOF
	done
	[ -n "$CLOUD" ] && [ "$n_roots" -eq 0 ] && { bad "cloud image ($CLOUD): no <pool>/data encryption root"; n_bad=$((n_bad+1)); }
	[ "$n_ds" -gt 0 ] && [ "$n_bad" -eq 0 ] && ok "all $((n_ds - n_plain)) data datasets encrypted + unlocked${CLOUD:+ ($n_plain boot-environment/payload datasets plain by design)}"
	[ "$n_ds" -eq 0 ] && bad "zfs get encryption,keystatus returned no datasets"
fi

# 3a. Every dataset beside the BE is mounted. The initramfs mounts only the BE; the zfs-mount
#     s6 oneshot mounts <pool>/home, /state, /containers and /models. The first installed
#     server had none of them mounted, so k0s and the platform wrote into the BE underneath.
if command -v zfs >/dev/null 2>&1; then
	unmounted="$(zfs list -H -t filesystem -o name,canmount,mountpoint,mounted 2>/dev/null |
		awk '$2 == "on" && $3 != "none" && $3 != "legacy" && $4 != "yes" { printf "%s ", $1 }')"
	[ -z "$unmounted" ] && ok "every ZFS dataset with a mountpoint is mounted" ||
		bad "ZFS datasets not mounted: ${unmounted}(zfs-mount did not run?)"
fi

# 3b. ESP actually mounted, and mounted by something that survives a reboot.
#     Encrypted installs mount the ESP at /boot: GRUB cannot read an encrypted pool, so the
#     kernel, initramfs and grub.cfg live on FAT. Without the mount a kernel upgrade lands
#     in the /boot DIRECTORY on the encrypted BE where GRUB cannot see it. Nodes installed
#     before encryption keep the ESP at /boot/efi (accepted, with a WARN). Assert BOTH
#     halves — the live mount and the fstab entry that reproduces it on the next boot —
#     and that the mount is root-only (fmask/dmask 0077).
if [ -d /sys/firmware/efi ]; then
	espmp=""
	for mp in /boot /boot/efi; do
		if [ "$(findmnt -n -o FSTYPE "$mp" 2>/dev/null)" = vfat ]; then espmp="$mp"; break; fi
	done
	if [ -z "$espmp" ]; then
		bad "no vfat ESP mounted at /boot (or legacy /boot/efi) — the ESP is unreachable to kernel/bootloader updates"
	else
		ok "ESP mounted at $espmp ($(findmnt -n -o SOURCE "$espmp"))"
		[ "$espmp" = /boot/efi ] && echo "  WARN legacy ESP at /boot/efi (pre-encryption layout)"
		if [ "$espmp" = /boot ] && ! ls /boot/vmlinuz-* >/dev/null 2>&1; then
			bad "no kernel image on the ESP (/boot/vmlinuz-*)"
		fi
		case ",$(findmnt -n -o OPTIONS "$espmp")," in
			*,fmask=0077,*dmask=0077,*) ok "ESP mount is root-only (fmask/dmask 0077)" ;;
			*) bad "ESP mount is not fmask=0077,dmask=0077" ;;
		esac
		if awk -v m="$espmp" '$2 == m { f = 1 } END { exit !f }' /etc/fstab 2>/dev/null; then
			ok "/etc/fstab has a $espmp entry"
		else
			bad "/etc/fstab has no $espmp entry — the ESP will not be mounted after a reboot"
		fi
	fi
else
	echo "  WARN not booted via UEFI — skipping ESP checks"
fi

# 3c. Shadow-grade modes on every node secret/state file (river-perms owns the table).
#     --check reports drift without fixing it; the boot-time oneshot would already have
#     fixed and logged it, so drift here means something rewrote a file since boot.
if [ -x /usr/local/bin/river-perms ]; then
	if /usr/local/bin/river-perms --check >/dev/null 2>&1; then
		ok "river-perms: shadow-grade modes in order"
	else
		bad "river-perms --check found drift:"; /usr/local/bin/river-perms --check 2>&1 | sed 's/^/        /' >&2
	fi
	if [ "$(stat -c %a /etc/runink 2>/dev/null)" = 700 ]; then ok "/etc/runink is 0700"; else bad "/etc/runink is not 0700"; fi
else
	bad "/usr/local/bin/river-perms missing"
fi

# 4. CPU governor performance.
gov="$(cat /sys/devices/system/cpu/cpu0/cpufreq/scaling_governor 2>/dev/null || echo n/a)"
[ "$gov" = performance ] && ok "governor performance" || echo "  WARN governor=$gov (VM?)"

# 5. sysctl.
v="$(sysctl -n net.ipv4.ip_unprivileged_port_start 2>/dev/null || echo n/a)"
[ "$v" = 80 ] && ok "ip_unprivileged_port_start=80" || bad "ip_unprivileged_port_start=$v (want 80)"
# Unprivileged user namespaces (bubblewrap, werf's rootless buildah). linux-runink is the
# zen-kernel 7.2.x fork, so BOTH gates exist: kernel.unprivileged_userns_clone (zen's knob,
# set to 1 in sysctl.d/99-runink.conf) and user.max_user_namespaces > 0. An ABSENT
# unprivileged_userns_clone means the booted kernel is not the zen linux-runink this image
# ships (river#84's vanilla 6.18 had no such key), so it fails too.
v="$(sysctl -n user.max_user_namespaces 2>/dev/null || echo 0)"
[ "${v:-0}" -gt 0 ] 2>/dev/null && ok "user.max_user_namespaces=$v" || bad "user.max_user_namespaces=$v (want > 0)"
if v="$(sysctl -n kernel.unprivileged_userns_clone 2>/dev/null)"; then
	[ "$v" = 1 ] && ok "kernel.unprivileged_userns_clone=1" || bad "kernel.unprivileged_userns_clone=$v (want 1)"
else
	bad "kernel.unprivileged_userns_clone absent (want 1; is the booted kernel the zen linux-runink?)"
fi

# 5b. Analytics / throughput kernel profile (config.delta + sysctl.d/50-runink-analytics.conf
#     + udev 60-runink-readahead.rules; docs/KERNEL.md). The config half is read from the
#     BOOTED kernel's /proc/config.gz, so a node running some other kernel fails here even if
#     the package on disk is right.
if [ -r /proc/config.gz ]; then
	for want in CONFIG_HZ=250 CONFIG_PREEMPT_LAZY=y CONFIG_PREEMPT_DYNAMIC=y \
		CONFIG_TRANSPARENT_HUGEPAGE_MADVISE=y CONFIG_DEFAULT_TCP_CONG='"bbr"' \
		CONFIG_DEFAULT_NET_SCH='"fq"' CONFIG_CPU_MITIGATIONS=y CONFIG_PSI=y CONFIG_IO_URING=y \
		CONFIG_SCHED_CLASS_EXT=y; do
		zcat /proc/config.gz | grep -qx "$want" && ok "kernel $want" || bad "kernel config lacks $want (analytics profile)"
	done
	zcat /proc/config.gz | grep -qx 'CONFIG_ZEN_INTERACTIVE=y' && bad "kernel has CONFIG_ZEN_INTERACTIVE=y (desktop tuning)" || ok "kernel ZEN_INTERACTIVE off"
else
	bad "/proc/config.gz missing (linux-runink builds IKCONFIG_PROC; is this the right kernel?)"
fi
v="$(cat /sys/kernel/mm/transparent_hugepage/enabled 2>/dev/null || echo n/a)"
case "$v" in *"[madvise]"*) ok "THP enabled=madvise" ;; *) bad "THP enabled is '$v' (want [madvise])" ;; esac
v="$(sysctl -n net.ipv4.tcp_congestion_control 2>/dev/null || echo n/a)"
[ "$v" = bbr ] && ok "tcp_congestion_control=bbr" || bad "tcp_congestion_control=$v (want bbr)"
v="$(sysctl -n net.core.default_qdisc 2>/dev/null || echo n/a)"
[ "$v" = fq ] && ok "default_qdisc=fq" || bad "default_qdisc=$v (want fq)"
v="$(sysctl -n vm.max_map_count 2>/dev/null || echo 0)"
[ "${v:-0}" -ge 1048576 ] 2>/dev/null && ok "vm.max_map_count=$v" || bad "vm.max_map_count=$v (want >= 1048576)"
v="$(sysctl -n vm.page-cluster 2>/dev/null || echo n/a)"
[ "$v" = 0 ] && ok "vm.page-cluster=0 (zram swap)" || bad "vm.page-cluster=$v (want 0)"
grep -qw 'mitigations=off' /proc/cmdline && bad "mitigations=off on the kernel command line" || ok "CPU mitigations not disabled on the cmdline"
ra_checked=0
for d in /sys/block/nvme*n* /sys/block/sd* /sys/block/vd*; do
	[ -r "$d/queue/read_ahead_kb" ] || continue
	ra_checked=$((ra_checked+1))
	v="$(cat "$d/queue/read_ahead_kb")"
	[ "$v" = 1024 ] && ok "$(basename "$d") read_ahead_kb=1024" || bad "$(basename "$d") read_ahead_kb=$v (want 1024)"
done
[ "$ra_checked" -gt 0 ] || echo "  WARN no nvme/sd/vd disk found to check read_ahead_kb"

# 6. zram active.
swapon --show=NAME --noheadings 2>/dev/null | grep -q zram && ok "zram swap active" || echo "  WARN no zram swap"
# zswap in front of zram compresses every swapped page twice; runink-zram.sh turns it off.
if swapon --show=NAME --noheadings 2>/dev/null | grep -q zram; then
	v="$(cat /sys/module/zswap/parameters/enabled 2>/dev/null || echo N)"
	[ "$v" = N ] && ok "zswap off (zram is the swap)" || bad "zswap enabled=$v in front of zram swap (want N)"
fi

# 6b. The install plan's memory sizes are applied (installer/lib/memtune.sh, 30-target-config):
#     zfs_arc_max is set in /etc/modprobe.d/zfs.conf, is in force in the running module, and is
#     the plan's zfs.arc_max_bytes; zram.conf carries the plan's swap.zram_mib. The plan is
#     root 0600: run as root for the plan comparisons, otherwise they are skipped with a WARN.
#     A cloud image is exempt (its instance plan is written after the build).
if [ -d /sys/module/zfs ] && [ ! -e /etc/river-cloud/cloud.env ]; then
	arc_conf="$(sed -n 's/^options zfs .*zfs_arc_max=\([0-9][0-9]*\).*/\1/p' /etc/modprobe.d/zfs.conf 2>/dev/null | tail -1)"
	arc_live="$(cat /sys/module/zfs/parameters/zfs_arc_max 2>/dev/null || true)"
	if [ -z "$arc_conf" ] || [ "$arc_conf" = 0 ]; then
		bad "zfs_arc_max not set in /etc/modprobe.d/zfs.conf (the installer writes it)"
	else
		ok "zfs_arc_max=$arc_conf in /etc/modprobe.d/zfs.conf"
		[ "$arc_live" = "$arc_conf" ] && ok "zfs_arc_max in force ($arc_live)" \
			|| bad "zfs module zfs_arc_max=$arc_live, /etc/modprobe.d/zfs.conf says $arc_conf (initramfs without the file?)"
	fi
	plan=/etc/runink/install-plan.json
	if [ "$(id -u)" != 0 ]; then
		echo "  WARN not root: $plan (0600) unread, plan comparisons for ARC and zram skipped"
	elif [ -f "$plan" ]; then
		arc_plan="$(tr -d ' \n\t' < "$plan" | sed -n 's/.*"arc_max_bytes":\([0-9][0-9]*\).*/\1/p')"
		zram_plan="$(tr -d ' \n\t' < "$plan" | sed -n 's/.*"zram_mib":\([0-9][0-9]*\).*/\1/p')"
		[ -n "$arc_plan" ] && [ "$arc_live" = "$arc_plan" ] && ok "zfs_arc_max is the plan's ($arc_plan)" \
			|| bad "zfs_arc_max=$arc_live, the plan's zfs.arc_max_bytes=${arc_plan:-<none>}"
		zram_conf="$(sed -n 's/^RUNINK_ZRAM_SIZE=//p' /etc/runink/zram.conf 2>/dev/null | tail -1)"
		[ -n "$zram_plan" ] && [ "$zram_conf" = "${zram_plan}M" ] && ok "zram.conf is the plan's (${zram_plan}M)" \
			|| bad "/etc/runink/zram.conf RUNINK_ZRAM_SIZE=${zram_conf:-<none>}, the plan's swap.zram_mib=${zram_plan:-<none>}"
	else
		echo "  WARN no $plan: ARC is the installer's RAM/16 fallback, zram the image default"
	fi
fi

# 7. Rootless prereqs.
grep -q '^runink:100000:65536' /etc/subuid && ok "subuid mapping" || bad "subuid mapping missing"
id runink >/dev/null 2>&1 && ok "runink user exists" || bad "runink user missing"

# 7b. river-sandbox: bwrap is unprivileged (not setuid), the runink user can open a
#     sandbox, and the sandbox cannot see the enrollment secrets.
[ -u /usr/bin/bwrap ] && bad "bwrap is setuid (want the unprivileged build)" || ok "bwrap not setuid"
if command -v runuser >/dev/null 2>&1 && id runink >/dev/null 2>&1 &&
	runuser -u runink -- /usr/local/bin/river-sandbox -- sh -c '[ ! -e /etc/runink ] && [ ! -e /home/runink ]' 2>/dev/null; then
	ok "river-sandbox runs as runink and hides /etc/runink + the runink home"
else
	bad "river-sandbox failed as runink (bwrap missing, or unprivileged userns disabled?)"
fi

# 8. No host-side inference binaries. mistral.rs is the only inference engine and runs as a
#    k0s pod (decided 2026-09-27); the image must carry no llama.cpp/whisper.cpp binaries
#    (they would also drag a C/C++ runtime closure back into a node meant to have none).
for b in /usr/local/bin/llama-server /usr/local/bin/llama-server-native \
         /usr/local/bin/llama-server-portable /usr/local/bin/llama-cli-native \
         /usr/local/bin/llama-cli-portable /usr/local/bin/llama-bench \
         /usr/local/bin/whisper-cli; do
	[ -e "$b" ] && bad "host inference binary still present: $b"
done
ok "no host-side llama.cpp/whisper.cpp binaries"

# 9. The node's s6 services are up, and nothing of the live medium came along.
#    The first server ISO's boot database lacked NetworkManager and sshd (its s6 repository
#    sync failed at build time and nothing said so), and installed nodes inherit that database.
if command -v s6-rc >/dev/null 2>&1; then
	active="$(s6-rc -a list 2>/dev/null)"
	for svc in NetworkManager-srv sshd-srv; do
		printf '%s\n' "$active" | grep -qx "$svc" && ok "s6 service $svc is up" || bad "s6 service $svc is not up"
	done
	if [ -d /etc/s6/sv/river-perms ] || [ -d /etc/s6/adminsv/river-perms ]; then
		printf '%s\n' "$active" | grep -qx river-perms && ok "river-perms ran at boot" || bad "river-perms is not in the boot set"
	fi
	printf '%s\n' "$active" | grep -q '^river-guide' && bad "live-only river-guide-model is up on a node" || ok "no river-guide service"
fi
[ -e /usr/local/bin/river-guide ] || [ -e /usr/share/river-guide ] && bad "river-guide (live medium only) is on the node" || ok "river-guide not on the node"
# LAN-install pairing is live-only: no session service template, no pairing user, nothing
# listening on the pairing ports (docs/INSTALL.md, "LAN installs").
[ -e /usr/local/lib/river-pair ] && bad "the LAN-install session service (live medium only) is on the node" || ok "no LAN-install session service"
grep -q '^river-pair:' /etc/passwd /etc/group 2>/dev/null && bad "the LAN-install pairing user reached the node" || ok "no LAN-install pairing user"
if command -v ss >/dev/null 2>&1; then
	ss -Hlnt 2>/dev/null | awk '{print $4}' | grep -Eq ':(47654|47655)$' && bad "something listens on a LAN-install pairing port" || ok "no LAN-install pairing listener"
fi
if grep -l '^[[:space:]]*ARGS=.*--autologin' /etc/s6/config/tty*.conf 2>/dev/null | grep -q .; then
	bad "a console logs in automatically: $(grep -l '^[[:space:]]*ARGS=.*--autologin' /etc/s6/config/tty*.conf | tr '\n' ' ')"
else
	ok "no console autologin"
fi

# 10. Cloud image (docs/CLOUD-IMAGES.md): serial console, the cloud services ran, the
#     per-instance identity exists, and the stack waited for the encrypted data.
if [ -n "$CLOUD" ]; then
	grep -q 'console=ttyS0' /proc/cmdline && ok "cloud: serial console on the kernel command line" || bad "cloud: no console=ttyS0 on the kernel command line"
	if command -v s6-rc >/dev/null 2>&1; then
		active="$(s6-rc -a list 2>/dev/null)"
		for svc in river-cloud-identity river-cloud-init; do
			printf '%s\n' "$active" | grep -qx "$svc" && ok "cloud: s6 $svc is up" || bad "cloud: s6 $svc is not up"
		done
		s6-rc-db dependencies rc-local 2>/dev/null | grep -qx river-cloud-init &&
			ok "cloud: rc-local waits for river-cloud-init" || bad "cloud: rc-local does not depend on river-cloud-init"
	fi
	grep -Eqx '[0-9a-f]{32}' /etc/machine-id 2>/dev/null && ok "cloud: machine-id generated" || bad "cloud: /etc/machine-id is not a generated id"
	ls /etc/ssh/ssh_host_ed25519_key >/dev/null 2>&1 && ok "cloud: SSH host keys generated" || bad "cloud: no SSH host keys"
	[ -s /var/lib/river-cloud/instance-id ] && ok "cloud: instance id recorded" || bad "cloud: river-cloud-init never finished"
fi

# 11. Air-gapped start (docs/PAYLOADS.md). A server carries k0s's own system images and pulls
#     them IfNotPresent, so its cluster needs no registry; the first-boot hand-off is wired.
if [ -x /usr/bin/k0s ]; then
	ls /var/lib/k0s/images/k0s-airgap-bundle-*-amd64.tar >/dev/null 2>&1 \
		&& ok "k0s airgap bundle in /var/lib/k0s/images" || bad "no k0s airgap bundle: first start would pull from the internet"
	[ -s /usr/local/share/runink/k0s-images.lock ] && ok "k0s-images.lock present" || bad "k0s-images.lock missing"
	grep -q '^    default_pull_policy: IfNotPresent$' /etc/k0s/k0s.yaml 2>/dev/null \
		&& ok "k0s pulls IfNotPresent" || bad "k0s.yaml has no spec.images.default_pull_policy: IfNotPresent"
	grep -q '^/usr/local/bin/river-firstboot-hooks &' /etc/s6/rc.local 2>/dev/null \
		&& ok "first-boot hooks wired in rc.local" || bad "river-firstboot-hooks not started by rc.local"
fi
# The graphical installer's kiosk never outlives the graphical first boot: without its marker,
# no web view, no font it pulled in, no kiosk user (docs/INSTALL.md, "The first start").
if [ -e /var/lib/runink/firstboot-ui ]; then
	[ "$(stat -c '%a %U' /var/lib/runink/firstboot-ui)" = "600 root" ] \
		&& ok "graphical first boot pending (marker 0600 root)" || bad "firstboot-ui marker is $(stat -c '%a %U' /var/lib/runink/firstboot-ui)"
else
	if command -v pacman >/dev/null 2>&1 && pacman -Q wpewebkit >/dev/null 2>&1; then
		bad "the installer kiosk (wpewebkit) is still installed with no graphical first boot pending"
	else ok "no installer kiosk on the node"; fi
	if getent passwd river-kiosk >/dev/null 2>&1; then bad "the river-kiosk user outlived the first boot"; else ok "no kiosk user"; fi
fi
if [ -d /var/lib/runink/payloads ]; then
	[ "$(stat -c '%a %U' /var/lib/runink/payloads)" = "700 root" ] \
		&& ok "downstream payloads root-only" || bad "/var/lib/runink/payloads is $(stat -c '%a %U' /var/lib/runink/payloads)"
fi

# 12. Dual-stack HOST. The cluster is IPv6-only by default: the k0s pod and service networks
#     never carry IPv4, and the host's IPv4 never becomes the k0s node address (k0s-keepalive
#     pins it to runink-node-ip6). A profile whose k0s.yaml turns on spec.network.dualStack
#     (a downstream server that must reach IPv4-only LANs from pods) is checked as a
#     dual-stack cluster instead, with IPv6 as the PRIMARY family: IPv6 pod and service ranges,
#     kube-router routing both families, and an IPv6 node address first.
if [ -f /etc/NetworkManager/conf.d/10-dual-stack.conf ] && grep -q '^ipv4.method=auto' /etc/NetworkManager/conf.d/10-dual-stack.conf \
	&& [ ! -e /etc/NetworkManager/conf.d/10-ipv6-only.conf ]; then ok "NetworkManager default is dual-stack"
else bad "NetworkManager default is not dual-stack (10-dual-stack.conf missing or 10-ipv6-only.conf present)"; fi
if [ -f /etc/k0s/k0s.yaml ]; then
	DUAL=0
	grep -A1 '^    dualStack:' /etc/k0s/k0s.yaml | grep -q 'enabled: true' && DUAL=1
	if [ "$DUAL" = 1 ]; then
		if grep -q 'IPv6podCIDR: fd' /etc/k0s/k0s.yaml && grep -q 'IPv6serviceCIDR: fd' /etc/k0s/k0s.yaml \
			&& grep -q 'enable-ipv6: "true"' /etc/k0s/k0s.yaml && grep -q 'enable-ipv4: "true"' /etc/k0s/k0s.yaml; then
			ok "k0s cluster is dual-stack with IPv6 ranges (profile enables spec.network.dualStack)"
		else bad "k0s.yaml enables dualStack but lacks the IPv6 ranges or kube-router routing for both families"; fi
	elif grep -q 'podCIDR: fd' /etc/k0s/k0s.yaml && grep -q 'serviceCIDR: fd' /etc/k0s/k0s.yaml \
		&& grep -q 'enable-ipv4: "false"' /etc/k0s/k0s.yaml; then ok "k0s pod/service networks are IPv6-only"
	else bad "k0s.yaml no longer keeps the cluster IPv6-only"; fi
	[ -x /usr/local/bin/runink-node-ip6 ] && ok "runink-node-ip6 present (k0s node address pin)" || bad "runink-node-ip6 missing"
	if command -v k0s >/dev/null 2>&1 && ips="$(k0s kubectl get nodes -o jsonpath='{.items[*].status.addresses[?(@.type=="InternalIP")].address}' 2>/dev/null)" && [ -n "$ips" ]; then
		if [ "$DUAL" = 1 ]; then
			case "${ips%% *}" in *:*) ok "k0s node addresses, IPv6 first ($ips)" ;; *) bad "the k0s node's first address is not IPv6: $ips" ;; esac
		else
			case "$ips" in *.*.*.*) bad "a k0s node address is IPv4: $ips" ;; *) ok "k0s node address is IPv6 ($ips)" ;; esac
		fi
		# A pinned api.address is the node's own address (an explicit --node-ip, e.g. a cloud
		# image's river0 ULA, is what k0s-keepalive and runink-firstboot pin it to).
		api="$(sed -n 's/^    address: *"\{0,1\}\([^"]*\)"\{0,1\} *$/\1/p' /run/runink-k0s/k0s.yaml /etc/k0s/k0s.yaml 2>/dev/null | head -1)"
		if [ -n "$api" ]; then
			case " $ips " in *" $api "*) ok "k0s api.address is the node address ($api)" ;;
				*) bad "k0s api.address $api is not the node address ($ips)" ;; esac
		fi
	fi
fi

# 13. Host firewall: ALWAYS loaded (not enrollment-gated), both families. Before enrollment the
#     base posture (default-deny, SSH); forward default-drop in every posture, so a LAN
#     peer's IPv4 (or IPv6) sent through this host to an arbitrary destination is dropped:
#     every forward accept must be one of the cluster / NAT64 / established rules.
if command -v nft >/dev/null 2>&1; then
	if command -v s6-rc >/dev/null 2>&1 && [ -d /etc/s6/sv/runink-fw ]; then
		s6-rc -a list 2>/dev/null | grep -qx runink-fw && ok "s6 oneshot runink-fw ran at boot" || bad "runink-fw is not in the boot set"
	fi
	in_c="$(nft list chain inet runink_fw input 2>/dev/null)"
	fw_c="$(nft list chain inet runink_fw forward 2>/dev/null)"
	if [ -z "$in_c" ] || [ -z "$fw_c" ]; then
		bad "no inet runink_fw input/forward chain loaded (the host has NO firewall)"
	else
		printf '%s\n' "$in_c" | grep -q 'hook input .*policy drop' && ok "firewall input is default-deny" || bad "firewall input is not policy drop"
		if [ ! -e /etc/runink/fw-enabled ]; then
			printf '%s\n' "$in_c" | grep -q 'tcp dport 22 accept' && ok "unenrolled node: base posture loaded (default-deny, SSH)" \
				|| bad "unenrolled node: the base posture is not loaded"
		fi
		printf '%s\n' "$fw_c" | grep -q 'hook forward .*policy drop' && ok "firewall forward is default-drop" || bad "firewall forward is not policy drop"
		stray="$(printf '%s\n' "$fw_c" | grep -w accept | grep -v -e 'ct state established,related accept' \
			-e 'ip6 saddr @cluster6 accept' -e 'ip6 daddr @cluster6 accept' -e 'iifname "nat64" ip saddr @nat64pool accept' || true)"
		# A dual-stack cluster (section 12) routes its IPv4 pod and service ranges too.
		if [ "${DUAL:-0}" = 1 ]; then
			stray="$(printf '%s\n' "$stray" | grep -v -e 'ip saddr @cluster4 accept' -e 'ip daddr @cluster4 accept' || true)"
		fi
		[ -z "$stray" ] && ok "forward accepts only cluster, NAT64 and established traffic (a LAN peer's IPv4 is dropped)" \
			|| bad "forward has other accepts: $(printf '%s' "$stray" | tr -s ' \t\n' ' ')"
		# The LAN-install pairing ports are opened on the LIVE medium only (runink-fw seed_pair):
		# on a node both pair sets are empty and the list they come from is absent.
		[ -e /usr/local/lib/river-pair/fw-pair ] && bad "fw-pair (live medium only) is on the node"
		pairs="$(nft list set inet runink_fw pair_tcp 2>/dev/null; nft list set inet runink_fw pair_udp 2>/dev/null)"
		if [ -z "$pairs" ]; then
			bad "the pair_tcp/pair_udp sets are missing from inet runink_fw"
		elif printf '%s\n' "$pairs" | grep -q 'elements'; then
			bad "the node opens LAN-install pairing ports: $(printf '%s\n' "$pairs" | grep elements | tr -s ' \t\n' ' ')"
		else
			ok "no LAN-install pairing port open in the firewall (live medium only)"
		fi
	fi
else
	bad "nft missing: the host firewall cannot be loaded"
fi

echo "== assert-golden: $pass passed, $fail failed =="
[ "$fail" -eq 0 ]
