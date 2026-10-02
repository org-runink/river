#!/bin/sh
# 80-enable-s6 — build the target's s6-rc boot database: the node's services enabled, the
# live medium's disabled, installed as the database the node boots on.
#
# Artix's s6 is driven by s6-frontend (`s6`). Services live in the stores /etc/s6/sv (the
# packages', plus this image's river-perms and zfs-mount) and /etc/s6/adminsv (rc-local,
# mount-filesystems, ...). `s6 repository sync` reads the stores into /etc/s6/repo, `s6 set enable|disable` edits
# the working set there, `s6 set commit` compiles it, and `s6 live install --init` installs
# the compiled set as the boot database (/etc/s6/rc/compiled). That is exactly what artools
# does for the live session (lib/iso/services.sh), and the target starts from the live set
# because it is a clone of the running live system (20-clone-rootfs). This step re-asserts
# the node's set rather than trusting that clone.
#
# Until 2026-09-25 this step called s6-rc-bundle-update (not part of s6-frontend-era Artix)
# and, failing that, `s6-rc-compile ... /etc/s6/sv`, which failed ("undefined service name
# rc-local") with its error discarded; the step printed "enabled" and changed nothing.
# Every command here now fails the install instead.
#
# rc-local runs /etc/s6/rc.local, which launches the Runink stack.
set -eu

TARGET="${RUNINK_TARGET:?}"
chroot_run() { chroot "$TARGET" /bin/sh -c "$1"; }

NODE_SERVICES="NetworkManager sshd rc-local"
# zfs-mount: mounts the pool's datasets beside the BE (/home) at boot; without it /home stays
# unmounted and no desktop session can start.
[ -d "$TARGET/etc/s6/sv/zfs-mount" ] && NODE_SERVICES="zfs-mount $NODE_SERVICES"
# river-perms: the shadow-grade drift check, a oneshot (invariant 4).
[ -d "$TARGET/etc/s6/sv/river-perms" ] && NODE_SERVICES="river-perms $NODE_SERVICES"
# runink-fw: the host firewall oneshot, before NetworkManager and sshd (invariant 7).
[ -d "$TARGET/etc/s6/sv/runink-fw" ] && NODE_SERVICES="runink-fw $NODE_SERVICES"
# chrony: the clock, on BOTH images (chrony-s6's `chrony` bundle = chrony-srv + chrony-log).
#
# WITHOUT THIS LINE the package is installed and the service never starts. That is not a
# hypothetical: on the runner box chrony's predecessor sat at "down (not started yet)" while the
# clock ran 30.7 s fast, and every GitHub App JWT was refused as "'exp' too far in the future".
# Enabling it in profile.yaml covers the live medium only; the installed node's default bundle
# is built HERE, and the check below is what proves it arrived.
[ -d "$TARGET/etc/s6/sv/chrony-srv" ] && NODE_SERVICES="chrony $NODE_SERVICES"
# The workstation's desktop services (profile.yaml live-session.services): the display manager,
# Bluetooth and printing. Each is present only where its -s6 package is installed.
for _svc in sddm bluetoothd cupsd; do
	if [ -d "$TARGET/etc/s6/sv/$_svc-srv" ] || [ -d "$TARGET/etc/s6/sv/$_svc" ]; then
		NODE_SERVICES="$NODE_SERVICES $_svc"
	fi
done
# Live-medium services the clone carried over. 20-clone-rootfs excluded their service
# directories, so `s6 repository sync` usually drops them already; disable any that remain.
# artix-live (Artix's live-session setup) is on no Runink River medium; listing it makes the
# boot-database check below fail should it ever arrive with one.
LIVE_ONLY_SERVICES="river-guide-model river-installer artix-live"

mounted=""
cleanup() { for fs in $mounted; do umount -R "$TARGET/$fs" 2>/dev/null || true; done; }
trap cleanup EXIT
for fs in dev proc sys; do mount --rbind "/$fs" "$TARGET/$fs"; mounted="$fs $mounted"; done
# A private /run, as artix-chroot gives artools: nothing from the live system's /run (its own
# s6 live state) is visible to the target's s6 tools.
mount -t tmpfs -o mode=0755 tmpfs "$TARGET/run"; mounted="run $mounted"

chroot_run "command -v s6 >/dev/null" || { echo "s6: no s6-frontend (s6) on the target" >&2; exit 1; }

echo "s6: syncing the service repository with the stores"
chroot_run "s6 repository sync"

for svc in $LIVE_ONLY_SERVICES; do
	if chroot_run "s6 set status" | grep -q "^$svc/"; then
		echo "s6: disabling live-only service $svc"
		chroot_run "s6 set disable $svc"
	fi
done

echo "s6: enabling $NODE_SERVICES"
# shellcheck disable=SC2086 # a word list by construction
chroot_run "s6 set enable --pull-dependencies $NODE_SERVICES"
chroot_run "s6 set commit"
chroot_run "s6 live install --init"

# The boot database must now hold every node service and no live-only one.
db="$(chroot_run "s6-rc-db -c /etc/s6/rc/compiled contents default")"
for svc in $NODE_SERVICES; do
	# A pipeline (the <name>-srv + <name>-log pair) is named after its bundle; its longrun is
	# <name>-srv. Oneshots and plain longruns are listed under their own name.
	if [ -d "$TARGET/etc/s6/sv/$svc-srv" ]; then want="$svc-srv"; else want="$svc"; fi
	printf '%s\n' "$db" | grep -qx "$want" || { echo "s6: $want is not in the boot database's default bundle" >&2; exit 1; }
done
for svc in $LIVE_ONLY_SERVICES; do
	if printf '%s\n' "$db" | grep -q "^$svc"; then
		echo "s6: live-only $svc is still in the boot database" >&2
		exit 1
	fi
done

# Ensure rc.local is executable so rc-local runs it.
chroot_run "chmod +x /etc/s6/rc.local"

echo "s6: boot database installed: $(printf '%s\n' "$db" | grep -cv '^$') services in the default bundle, incl. $NODE_SERVICES"
