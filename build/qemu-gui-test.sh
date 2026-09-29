#!/bin/sh
# SPDX-FileCopyrightText: 2026 The RIVER Authors
# SPDX-License-Identifier: MIT
#
# qemu-gui-test.sh — drive the GRAPHICAL installer end to end under QEMU/KVM, with no human,
# and photograph every screen (docs/INSTALL.md, "Installing with the graphical installer").
#
#   build/qemu-gui-test.sh [options] ISO
#
# The flow is driven through the installer's API with exactly the calls its UI makes
# (`river-installer drive`, which prints "RIVERGUI SCREEN <name>" as each screen is up); the
# harness takes a QEMU screendump of the real kiosk (server) or desktop browser (workstation)
# at each marker. Checks (serial log "RIVERTEST OK|FAIL <check>"):
#
#   server (--profile server, with RIVER_PROFILE_DIR naming a downstream server profile: this
#   repository has none since Runink River became the workstation)
#     gui-kiosk      the kiosk shows the installer on tty1 (a screendump of the welcome screen)
#     gui-recovery   switching to tty2 pauses the kiosk and shows the text console; back on
#                    tty1 the kiosk returns to the same screen
#     gui-pointer    the mouse works in the kiosk: the harness finds the pointer on the screen
#                    (a screendump; no pointer drawn fails), steers a USB mouse onto "Español"
#                    and clicks, and the click must switch the UI to Spanish
#     gui-edition    the installer says it is the edition whose descriptor matches the medium
#                    (label= / river.edition= on the kernel command line), not the base edition
#     gui-install    the install, all eight screens, erase confirmed through the ERASE gate
#     harness-wired  the harness imports and unlocks the new pool on the scratch disk and wires
#                    in its serial console and rc.local hook (a harness step, not a product check)
#     export         the pool exports cleanly afterwards, so the installed system can import it
#     unlocked       the installed system boots and unlocks with the key the recovery screen showed
#     fb-ready       the graphical first boot reaches "ready" (network, clock, firewall, k0s)
#     fb-k0s         its k0s check is green (the node is Ready)
#     fb-finish      Finish removes the kiosk: no wpewebkit, no kiosk user, no marker
#     golden         tests/assert-golden.sh passes after the first boot
#   workstation (the default: Runink River, iso-profiles/river)
#     gui-desktop    the installer opens full screen in the live Plasma session (Firefox)
#     gui-edition    as above
#     gui-install    as above
#     harness-wired  as above
#     export         as above
#     unlocked       as above
#     home-mounted   /home is the pool's home dataset (zfs-mount ran) and /home/runink exists
#     home-owner     ~/.local and ~/.local/share belong to the user, not root
#     firewall       the inet runink_fw table is loaded
#     machine-id     /etc/machine-id is set (and is not the live medium's)
#     greeting       the admin's login shell is fish and its greeting (fastfetch) names Runink River
#     no-base-name   /etc/lsb-release and os-release never name the base distribution
#     hostname       the installed /etc/hostname is the plan's name, not the live medium's (runink-live)
#     plasma-login   the installed machine boots to SDDM, the admin logs in (the password typed
#                    on the VM keyboard, so the layout the installer set works) and Plasma starts
#     net-ready      after that login the network is up with nothing left to do (a user-mode NIC)
#   (tests/assert-golden.sh describes a server; the workstation is checked by the lines above)
#   either profile, when the profile carries tests/installed.d/*.sh (build/qemu-hooks.sh)
#     hook-<name>    each EXECUTABLE hook ran on the installed system after the checks above,
#                    exited 0 in its time and reported exactly the checks it declares
#     <declared>     every check a hook declares in its "# RIVERTEST-CHECKS: a b" header, as it
#                    reports it (RIVERTEST OK|FAIL|SKIP <check>; SKIP is listed, not failed)
#   Every RIVER_HOOK_* variable of the harness's environment reaches the hooks (exported from
#   /run/rt/config), so a downstream profile can switch its own checks on and off.
#
# Against an EXISTING ISO built before the graphical installer (--kit-overlay-free: this is
# automatic), the harness copies the new files onto the live system from the repository (the
# river-installer binary, the kiosk scripts, the installer steps, the edition descriptors), and
# on a server installs the kiosk packages from --kiosk-pkgs (the ISO has no wpewebkit), then
# starts what a new ISO starts by itself. Everything copied is listed on the serial log, so a
# result is never mistaken for the ISO's own. On an ISO built from this tree nothing is copied.
#
# Options:
#   --profile workstation|server   (default workstation)
#   --kiosk-pkgs DIR   package files for the kiosk (default ~/.cache/river-build/kiosk-pkgs)
#   --offline          the guests get a NIC with no route out (k0s must come up air-gapped)
#   --disk-size SIZE   scratch disk (default 64G)      --mem MiB (default 8192)   --cpus N (4)
#   --work DIR         (default ~/.cache/river-build/qemu-gui-test/<time>)
#   --keep             keep the work directory's scratch disk
set -eu

HERE="$(cd "$(dirname "$0")" && pwd)"
REPO="$(cd "$HERE/.." && pwd)"
CACHE="${XDG_CACHE_HOME:-$HOME/.cache}"
PROFILE=workstation KIOSK_PKGS="$CACHE/river-build/kiosk-pkgs" OFFLINE=0 DISK_SIZE=64G MEM=8192 CPUS=4 WORK="" KEEP=0 ISO=""
PASSWORD="correct-horse-battery"

die() { echo "qemu-gui-test: $*" >&2; exit 2; }
while [ $# -gt 0 ]; do
	case "$1" in
		--profile) PROFILE="${2:?}"; shift ;;
		--kiosk-pkgs) KIOSK_PKGS="${2:?}"; shift ;;
		--offline) OFFLINE=1 ;;
		--disk-size) DISK_SIZE="${2:?}"; shift ;;
		--mem) MEM="${2:?}"; shift ;;
		--cpus) CPUS="${2:?}"; shift ;;
		--work) WORK="${2:?}"; shift ;;
		--keep) KEEP=1 ;;
		-h|--help) sed -n '5,/^set -eu/p' "$0" | sed '$d' | sed 's/^# \{0,1\}//'; exit 2 ;;
		-*) die "unknown option $1" ;;
		*) [ -z "$ISO" ] || die "one ISO only"; ISO="$1" ;;
	esac
	shift
done
[ -f "$ISO" ] || die "no ISO (see --help)"
case "$PROFILE" in server|workstation) ;; *) die "--profile server or workstation" ;; esac
for t in qemu-system-x86_64 qemu-img mkfs.vfat mcopy blkid go magick; do
	command -v "$t" >/dev/null 2>&1 || die "$t is required"
done
[ -w /dev/kvm ] || die "/dev/kvm must be writable"
# shellcheck source=build/qemu-lib.sh
. "$HERE/qemu-lib.sh"
# shellcheck source=build/qemu-hooks.sh
. "$HERE/qemu-hooks.sh"
ovmf_check || die "OVMF (edk2-ovmf) not found"
LABEL="$(blkid -s LABEL -o value "$ISO")"
[ -n "$WORK" ] || WORK="$CACHE/river-build/qemu-gui-test/$(date +%Y%m%d-%H%M%S)-$PROFILE"
mkdir -p "$WORK/shots"
echo "qemu-gui-test: $ISO (label $LABEL), profile $PROFILE, work $WORK"

# --- the kit: scripts, the files a pre-GUI ISO lacks, the kiosk packages ----------------------------
KIT="$WORK/kit"
rm -rf "$KIT"
mkdir -p "$KIT/root/usr/local/bin" "$KIT/root/usr/local/lib/runink-install" "$KIT/root/etc/s6"
P="$REPO/iso-profiles/river"
if [ "$PROFILE" = server ]; then
	[ -n "${RIVER_PROFILE_DIR:-}" ] || die "--profile server needs RIVER_PROFILE_DIR (a downstream server profile)"
	P="$RIVER_PROFILE_DIR"
fi
# The installer commands (runink-installer), built from this tree: an image that lacks one (the
# 20260925 workstation ISO carries none of them) gets it, and the others are copied only where
# they differ from the image's own.
for b in gui:river-installer hwprobe:river-hwprobe plan:river-plan netcheck:river-netcheck \
	modelpack:river-modelpack payloadpack:river-payloadpack; do
	( cd "$REPO/installer" && CGO_ENABLED=0 GOAMD64=v1 go build -trimpath -ldflags='-s -w' \
		-o "$KIT/root/usr/local/bin/${b#*:}" "./${b%%:*}" )
done
cp "$REPO/installer/netsetup/river-netsetup" "$KIT/root/usr/local/bin/"
cp "$P"/root-overlay/usr/local/lib/runink-install/*.sh "$KIT/root/usr/local/lib/runink-install/"
cp -r "$P/live-overlay/usr/share" "$KIT/root/usr/"
for f in river-kiosk river-firstboot-ui river-firstboot-hooks river-perms; do
	[ -f "$P/root-overlay/usr/local/bin/$f" ] && cp "$P/root-overlay/usr/local/bin/$f" "$KIT/root/usr/local/bin/"
done
[ -f "$P/root-overlay/etc/s6/rc.local" ] && cp "$P/root-overlay/etc/s6/rc.local" "$KIT/root/etc/s6/rc.local"
if [ "$PROFILE" = workstation ]; then
	# The whole installed-system overlay (services, firewall, branding, ...): an ISO built before
	# a change gets it on the live system, and the install clones the live system, so the
	# installed phase tests this tree's root-overlay, not the ISO's (every copied file is listed).
	cp -R "$P/root-overlay/." "$KIT/root/"
	cp "$P/live-overlay/usr/local/bin/river-installer-desktop" "$KIT/root/usr/local/bin/"
	mkdir -p "$KIT/root/etc/xdg/autostart"
	cp "$P/live-overlay/etc/xdg/autostart/river-installer.desktop" "$KIT/root/etc/xdg/autostart/"
fi
cp "$REPO/tests/assert-golden.sh" "$KIT/assert-golden.sh"
# The profile's own installed-system checks (tests/installed.d, build/qemu-hooks.sh): only the
# executable hooks are copied, and the checks they declare join the verdict.
cp "$HERE/qemu-hooks.sh" "$KIT/hooks.sh"
if [ -n "$(hooks_list "$P/tests/installed.d")" ]; then
	mkdir -p "$KIT/installed.d"
	hooks_list "$P/tests/installed.d" | while IFS= read -r h; do cp "$h" "$KIT/installed.d/"; done
fi
HOOK_WANT="$(HOOKS_ANY_MODE=1 hooks_want "$KIT/installed.d")" || die "a hook in $P/tests/installed.d declares no checks"
HOOK_BUDGET="$(HOOKS_ANY_MODE=1 hooks_budget "$KIT/installed.d")"
[ -z "$HOOK_WANT" ] || echo "qemu-gui-test: profile hooks ($P/tests/installed.d) add: $HOOK_WANT"
if [ "$PROFILE" = server ]; then
	ls "$KIOSK_PKGS"/*.pkg.tar.zst >/dev/null 2>&1 || die "no kiosk packages in $KIOSK_PKGS (see docs/INSTALL.md, testing)"
	mkdir -p "$KIT/pkgs"
	# FAT cannot hold ':' (epochs in file names); pacman reads the name from inside the package.
	for f in "$KIOSK_PKGS"/*.pkg.tar.zst; do cp "$f" "$KIT/pkgs/$(basename "$f" | tr ':' '_')"; done
fi
cat > "$KIT/config" <<EOF
PROFILE=$PROFILE
PASSWORD=$PASSWORD
OFFLINE=$OFFLINE
EOF
hooks_env >> "$KIT/config"

cat > "$KIT/common.sh" <<'EOF'
export PATH=/usr/local/sbin:/usr/local/bin:/usr/bin:/usr/sbin:/bin:/sbin
. /run/rt/config
ok() { echo "RIVERTEST OK $1"; }
ko() { echo "RIVERTEST FAIL $1${2:+ ($2)}"; }
# overlay: copy a file only when the image's own copy differs (listed, never silent)
overlay() {
	( cd /run/rt/root && find . -type f ) | while read -r f; do
		f="${f#./}"
		if ! cmp -s "/run/rt/root/$f" "/$f" 2>/dev/null; then
			# a new file renamed over the old one: a running binary cannot be overwritten in place
			mkdir -p "/$(dirname "$f")" && cp "/run/rt/root/$f" "/$f.rt-new" && mv -f "/$f.rt-new" "/$f"
			case "$f" in */bin/*|*/runink-install/*|etc/s6/rc.local) chmod 0755 "/$f" ;; esac
			echo "RIVERTEST NOTE overlay /$f"
		fi
	done
}
EOF

cat > "$KIT/live.sh" <<'EOF'
#!/bin/sh
# LIVE medium, as root: bring the graphical installer up (what a new ISO does by itself), check
# the recovery console, drive the install, wire the scratch disk for the installed phase.
exec >/dev/ttyS0 2>&1
set -u
. /run/rt/common.sh
echo "RIVERTEST BEGIN live"
overlay | tee /run/rt-overlay.log
if [ "$PROFILE" = server ] && grep -Eq 'overlay /usr/local/bin/river-(installer|kiosk)$' /run/rt-overlay.log; then
	# The running backend and kiosk are the ISO's own: restart both, so the kit's copies are what
	# the checks see (s6 restarts the backend, tty1 starts the kiosk again).
	echo "RIVERTEST NOTE restarting the installer backend and the kiosk on the kit's copies"
	pkill -x river-installer
	pkill -f '^/bin/sh /usr/local/bin/river-kiosk'
	sleep 10
fi
if [ "$PROFILE" = server ] && [ ! -x /usr/lib/wpe-webkit-2.0/MiniBrowser ] && [ -d /run/rt/pkgs ]; then
	t0=$(date +%s)
	pacman -U --noconfirm --needed --asdeps /run/rt/pkgs/*.pkg.tar.zst >/run/rt-pacman.log 2>&1 \
		&& pacman -D --asexplicit $(sed 's/#.*//' /usr/share/river/installer/kiosk-packages) >>/run/rt-pacman.log 2>&1 \
		&& echo "RIVERTEST NOTE kiosk packages installed on this live medium ($(( $(date +%s) - t0 ))s)" \
		|| { echo "RIVERTEST NOTE pacman failed:"; tail -5 /run/rt-pacman.log; }
fi
if ! river-installer wait --timeout 5s 2>/dev/null; then
	echo "RIVERTEST NOTE starting the installer backend (this ISO predates it)"
	if [ "$PROFILE" = workstation ]; then du=runink; else du=""; fi
	setsid river-installer serve --kiosk-user river-kiosk ${du:+--desktop-user $du} </dev/null >/run/rt-backend.log 2>&1 &
	river-installer wait --timeout 30s || ko gui-backend
fi
if [ "$PROFILE" = server ]; then
	# (the view itself is paused while the harness is on another console: look for the kiosk)
	if ! pgrep -f '^/bin/sh /usr/local/bin/river-kiosk' >/dev/null; then
		echo "RIVERTEST NOTE starting the kiosk on tty1 (this ISO predates it)"
		setsid river-kiosk --vt 1 </dev/null >/run/rt-kiosk.log 2>&1 &
	fi
	i=0; until pgrep -f wpe-webkit-2.0/MiniBrowser >/dev/null || [ $i -ge 60 ]; do sleep 1; i=$((i+1)); done
	sleep 12
	pgrep -u river-kiosk -f MiniBrowser >/dev/null && ok gui-kiosk || ko gui-kiosk "no web view as river-kiosk"
	echo "RIVERGUI SCREEN kiosk-boot"; sleep 5
	# The recovery console: tty2 takes the display (the kiosk pauses), tty1 brings it back.
	river-installer vt 2; sleep 4
	gone=1; pgrep -f wpe-webkit-2.0/MiniBrowser >/dev/null && gone=0
	echo "RIVERGUI SCREEN tty2-recovery"; sleep 5
	river-installer vt 1; sleep 12
	back=0; pgrep -f wpe-webkit-2.0/MiniBrowser >/dev/null && back=1
	echo "RIVERGUI SCREEN kiosk-back"; sleep 5
	[ "$gone$back" = 11 ] && ok gui-recovery || ko gui-recovery "paused=$gone resumed=$back"
	# The mouse: the harness steers the USB mouse onto "Español" and clicks (RIVERGUI POINTER);
	# only a click that reached the page switches the installer's language.
	st() { curl -s -g 'http://[::1]:47660/api/state'; }
	st | grep -q '"lang":"en"' || echo "RIVERTEST NOTE the UI is not in English before the pointer check"
	echo "RIVERGUI POINTER"
	i=0; until st | grep -q '"lang":"es"' || [ $i -ge 240 ]; do sleep 1; i=$((i+1)); done
	sleep 3
	if st | grep -q '"lang":"es"'; then ok gui-pointer; else ko gui-pointer "the click did not reach the page ($(st | grep -o '"lang":"[a-z]*"'))"; fi
	curl -s -g -o /dev/null -X POST -H 'Content-Type: application/json' -H 'X-River: 1' \
		-d '{"lang":"en","keyboard":""}' 'http://[::1]:47660/api/locale'
else
	u=runink
	pid="$(pgrep -u $u -x kwin_wayland | head -1)"
	# The session's environment (WAYLAND_DISPLAY and friends) is plasmashell's, not kwin's.
	spid="$(pgrep -u $u -x plasmashell | head -1)"
	if ! pgrep -u $u -f 'firefox.*47660' >/dev/null; then
		echo "RIVERTEST NOTE opening the installer in the live session (this ISO predates the autostart)"
		env="$(tr '\0' '\n' < /proc/"$spid"/environ | grep -E '^(WAYLAND_DISPLAY|XDG_RUNTIME_DIR|DBUS_SESSION_BUS_ADDRESS|DISPLAY)=' | tr '\n' ' ')"
		# shellcheck disable=SC2086
		setsid runuser -u $u -- env HOME=/home/$u $env /usr/local/bin/river-installer-desktop </dev/null >/run/rt-desktop.log 2>&1 &
	fi
	vt="$(tr '\0' '\n' < /proc/"$pid"/environ | sed -n 's/^XDG_VTNR=//p' | head -1)"
	[ -n "$vt" ] && river-installer vt "$vt"
	i=0; until pgrep -u $u -f 'firefox.*47660' >/dev/null || [ $i -ge 90 ]; do sleep 1; i=$((i+1)); done
	sleep 25
	pgrep -u $u -f 'firefox.*47660' >/dev/null && ok gui-desktop || ko gui-desktop "no installer window"
	pgrep -u $u -f 'firefox.*47660' >/dev/null || sed 's/^/RIVERTEST NOTE desktop: /' /run/rt-desktop.log
fi
# The edition: the running image is the descriptor whose medium_label is the label= on the kernel
# command line (river.edition= picks among several on one medium). The installer must say it is
# THAT edition; a downstream image that shows the base edition's name fails here.
label="$(tr ' ' '\n' < /proc/cmdline | sed -n 's/^label=//p' | head -1)"
want_ed="$(tr ' ' '\n' < /proc/cmdline | sed -n 's/^river\.edition=//p' | head -1)"
if [ -z "$want_ed" ]; then
	for f in /usr/share/river/installer/editions/*.json; do
		[ -f "$f" ] || continue
		tr -d '\n' < "$f" | grep -q "\"medium_label\": *\"$label\"" \
			&& want_ed="$(tr -d '\n' < "$f" | sed -n 's/.*"id": *"\([^"]*\)".*/\1/p')" && break
	done
fi
got_ed="$(curl -s -g 'http://[::1]:47660/api/state' | tr -d '\n' | sed -n 's/.*"self": *"\([^"]*\)".*/\1/p')"
if [ -n "$want_ed" ] && [ "$got_ed" = "$want_ed" ]; then ok gui-edition; else ko gui-edition "label=$label: descriptor '${want_ed:-none}', installer says '${got_ed:-none}'"; fi

lab=--lab
river-installer drive install $lab --password "$PASSWORD" --pause 5s | tee /run/rt-drive.log
grep -q '^RIVERGUI DONE install' /run/rt-drive.log && ok gui-install || ko gui-install "$(grep '^RIVERGUI FAIL' /run/rt-drive.log)"
grep -q '^RIVERGUI DONE install' /run/rt-drive.log || { tail -40 /run/rt-backend.log 2>/dev/null | sed 's/^/RIVERTEST NOTE backend: /'; }
KEY="$(sed -n 's/^RIVERGUI KEY //p' /run/rt-drive.log | head -1)"
tdisk="$(lsblk -dno NAME,SERIAL | awk '$2 == "RIVERQEMU0001" { print $1 }')"
POOL=zriver
if grep -q '^RIVERGUI DONE install' /run/rt-drive.log && [ -n "$KEY" ]; then
	# Harness wiring on the SCRATCH disk only: the serial console, and an rc.local hook that
	# runs installed.sh from this kit.
	if zpool import -N -R /mnt "$POOL" && printf '%s\n' "$KEY" | zfs load-key "$POOL"; then
		be="$(zfs list -H -o name -r "$POOL/ROOT" | sed -n 2p)"
		zfs mount "$be" && zfs mount -a
		echo '[ -e /dev/disk/by-label/RIVERTEST ] && ( mkdir -p /run/rt && mount -r -L RIVERTEST /run/rt && sh /run/rt/installed.sh ) >/dev/null 2>&1 &' >> /mnt/etc/s6/rc.local
		esp="$(lsblk -rno PATH,PARTTYPE "/dev/$tdisk" | awk 'tolower($2) == "c12a7328-f81f-11d2-ba4b-00a0c93ec93b" { print $1; exit }')"
		mkdir -p /run/rt-esp && mount "$esp" /run/rt-esp && \
			sed -i '/^[[:space:]]*linux[[:space:]]/ s/$/ console=tty0 console=ttyS0,115200/' /run/rt-esp/grub/grub.cfg && \
			grep -q 'console=ttyS0' /run/rt-esp/grub/grub.cfg && ok harness-wired || ko harness-wired "esp=$esp"
		umount /run/rt-esp || true
		zfs unmount -a || true
		zpool export "$POOL" && ok export || ko export
	else
		ko harness-wired "import/unlock failed"
	fi
fi
echo "RIVERTEST END live"
sync
poweroff -f
EOF

cat > "$KIT/installed.sh" <<'EOF'
#!/bin/sh
# INSTALLED system, as root (from the rc.local hook): the graphical first boot (server) or the
# desktop login (workstation), then the golden checks.
exec >/dev/ttyS0 2>&1
set -u
. /run/rt/common.sh
echo "RIVERTEST BEGIN installed"
[ "$(zfs get -H -o value keystatus "$(findmnt -n -o SOURCE / | cut -d/ -f1)")" = available ] && ok unlocked || ko unlocked
if [ "$PROFILE" = server ]; then
	# The first boot's kiosk (river-firstboot-ui from rc.local) draws before the driver starts.
	i=0; until pgrep -u river-kiosk -f MiniBrowser >/dev/null || [ $i -ge 300 ]; do sleep 2; i=$((i+2)); done
	sleep 15
	river-installer drive firstboot --pause 5s --timeout 45m | tee /run/rt-fb.log
	grep -q '^RIVERGUI DONE firstboot' /run/rt-fb.log && ok fb-ready || ko fb-ready "$(grep '^RIVERGUI FAIL' /run/rt-fb.log)"
	grep -q '^RIVERGUI CHECK k0s ok' /run/rt-fb.log && ok fb-k0s || ko fb-k0s "$(grep '^RIVERGUI CHECK k0s' /run/rt-fb.log)"
	i=0
	while [ $i -lt 300 ] && { [ -e /var/lib/runink/firstboot-ui ] || pacman -Q wpewebkit >/dev/null 2>&1; }; do sleep 5; i=$((i+5)); done
	sleep 10
	why=""
	[ -e /var/lib/runink/firstboot-ui ] && why="marker left;"
	pacman -Q wpewebkit >/dev/null 2>&1 && why="$why wpewebkit installed;"
	pacman -Q mesa >/dev/null 2>&1 && why="$why mesa installed;"
	getent passwd river-kiosk >/dev/null && why="$why kiosk user left;"
	[ -z "$why" ] && ok fb-finish || ko fb-finish "$why"
	echo "RIVERGUI SCREEN console-after-finish"; sleep 5
else
	# The desktop's preconditions. /home must be the pool's home dataset: without the zfs-mount
	# oneshot it stayed unmounted, SDDM's helper could not chdir to /home/runink and no session
	# ever started (the 2026-09-26 plasma-login failure).
	[ "$(findmnt -n -o FSTYPE /home)" = zfs ] && [ -d /home/runink ] && ok home-mounted \
		|| ko home-mounted "$(findmnt -n -o SOURCE,FSTYPE /home || echo '/home not mounted')"
	[ "$(stat -c %U /home/runink/.local 2>/dev/null)" = runink ] && [ "$(stat -c %U /home/runink/.local/share 2>/dev/null)" = runink ] \
		&& ok home-owner || ko home-owner "$(stat -c '%n %U' /home/runink/.local /home/runink/.local/share 2>&1 | tr '\n' ' ')"
	# rc.local (this hook) depends on runink-fw, so the table is there; wait a little anyway.
	i=0; until nft list table inet runink_fw >/dev/null 2>&1 || [ $i -ge 30 ]; do sleep 1; i=$((i+1)); done
	nft list table inet runink_fw >/dev/null 2>&1 && ok firewall || ko firewall "no inet runink_fw table"
	grep -qE '^[0-9a-f]{32}$' /etc/machine-id && ok machine-id || ko machine-id "/etc/machine-id: '$(cat /etc/machine-id 2>/dev/null)'"
	# The terminal greeting: the admin logs into fish, and its greeting prints this machine as Runink
	# River (fastfetch reads /etc/os-release); no file the user can read still names the base distribution.
	sh_="$(getent passwd runink | cut -d: -f7)"
	gr="$(runuser -u runink -- fish -c 'source /etc/fish/conf.d/runink-greeting.fish; fish_greeting' 2>&1 | sed 's/\x1b\[[0-9;?]*[a-zA-Z]//g')"
	[ "$sh_" = /usr/bin/fish ] && printf '%s\n' "$gr" | grep -q 'OS: Runink River' && ok greeting \
		|| ko greeting "shell $sh_; greeting: $(printf '%s' "$gr" | grep -m1 'OS:')"
	# The installed machine is named for the plan, never for the live medium (river#136).
	h_="$(cat /etc/hostname 2>/dev/null)"
	[ -n "$h_" ] && [ "$h_" != runink-live ] && [ "$(hostname)" = "$h_" ] && ok hostname || ko hostname "/etc/hostname=$h_ hostname=$(hostname)"
	grep -lis artix /etc/lsb-release /etc/os-release /usr/lib/os-release >/dev/null && ko no-base-name "$(grep -lis artix /etc/lsb-release /etc/os-release /usr/lib/os-release | tr '\n' ' ')" || ok no-base-name
	# SDDM's greeter on the desktop VT; the harness types the password (RIVERGUI TYPE).
	i=0; until pgrep -f sddm-greeter >/dev/null || [ $i -ge 300 ]; do sleep 2; i=$((i+2)); done
	sleep 20
	echo "RIVERGUI SCREEN sddm"; sleep 5
	echo "RIVERGUI TYPE password"
	# The first login on software rendering (no GPU in the VM) takes minutes.
	i=0; until pgrep -u runink -x plasmashell >/dev/null || [ $i -ge 900 ]; do sleep 3; i=$((i+3)); done
	sleep 60
	echo "RIVERGUI SCREEN plasma"; sleep 5
	pgrep -u runink -x plasmashell >/dev/null && ok plasma-login || ko plasma-login "no plasmashell for runink"
	pgrep -u runink -x plasmashell >/dev/null || ps -u runink -o comm= | sort -u | tr '\n' ' ' | sed 's/^/RIVERTEST NOTE runink processes: /'
	echo
	act="$(nmcli -t -f NAME,DEVICE connection show --active 2>/dev/null | grep -v ':lo$')"
	echo "RIVERTEST NOTE active connections: $act"
	[ -n "$act" ] && ok net-ready || ko net-ready "no active network connection after login"
fi
[ "$PROFILE" = server ] && { sh /run/rt/assert-golden.sh && ok golden || ko golden; }
# The profile's own checks (tests/installed.d), last: the system as its first boot left it.
if [ -d /run/rt/installed.d ]; then
	. /run/rt/hooks.sh
	run_hooks /run/rt/installed.d /run
fi
echo "RIVERTEST END installed"
sync
poweroff -f
EOF

rm -f "$WORK/kit.img"
kib=$(( $(du -sk "$KIT" | cut -f1) + 32768 ))
mkfs.vfat -C -n RIVERTEST "$WORK/kit.img" "$kib" >/dev/null
for f in "$KIT"/*; do mcopy -s -i "$WORK/kit.img" "$f" ::/; done

# --- QEMU ------------------------------------------------------------------------------------------
qemu-img create -q -f qcow2 "$WORK/target.qcow2" "$DISK_SIZE"
cp "$OVMF_VARS" "$WORK/vars.fd"
NIC="-nic none"
[ "$PROFILE" = workstation ] && NIC="-nic user,model=virtio-net-pci"
[ "$OFFLINE" = 1 ] && NIC="-nic user,model=virtio-net-pci,restrict=on,ipv4=off,ipv6=on,ipv6-prefix=fd00:5eed:0:1::,ipv6-prefixlen=64"
VGA="-vga std"
[ "$PROFILE" = workstation ] && VGA="-vga virtio"
QEMU_ARGS="-enable-kvm -machine q35 -cpu host -m $MEM -smp $CPUS $NIC -display none $VGA
 -drive if=pflash,format=raw,readonly=on,file=$OVMF_CODE -drive if=pflash,format=raw,file=$WORK/vars.fd
 -drive if=none,id=target,format=qcow2,file=$WORK/target.qcow2 -device virtio-blk-pci,drive=target,serial=RIVERQEMU0001,bootindex=1
 -drive if=none,id=kit,format=raw,file=$WORK/kit.img -device virtio-blk-pci,drive=kit,serial=RIVERTESTKIT"
MEDIUM="-device qemu-xhci,id=xhci -drive if=none,id=medium,format=raw,readonly=on,file=$ISO -device usb-storage,bus=xhci.0,drive=medium,bootindex=0"
# A relative USB mouse, like a real one (gui-pointer): the kiosk's WPE ignores absolute pointers.
[ "$PROFILE" = server ] && MEDIUM="$MEDIUM -device usb-mouse,bus=xhci.0,id=mouse"
trap qemu_cleanup EXIT INT TERM

# snap NAME — a PNG screendump into shots/ (and a small JPEG for the docs)
snap() {
	mon "screendump $WORK/shots/$1.ppm"
	sleep 1
	magick "$WORK/shots/$1.ppm" "$WORK/shots/$1.png" 2>/dev/null && rm -f "$WORK/shots/$1.ppm"
	magick "$WORK/shots/$1.png" -resize 1024x -quality 72 "$WORK/shots/$1.jpg" 2>/dev/null || true
	echo "qemu-gui-test: screen $1"
}
# pointer_at PNG — "X Y" of the kiosk's mouse pointer, or nothing: kiosk-pointer.js fills it with
# #FEFEFE, so it is the largest patch of that colour (anti-aliased text can have a stray pixel or
# two of it). X Y is the top left of the fill, a pixel or two from the tip.
pointer_at() {
	magick "$1" -fill black +opaque '#FEFEFE' -fill white -opaque '#FEFEFE' \
		-define connected-components:verbose=true -connected-components 8 null: 2>/dev/null |
		awk '/srgb\(255,255,255\)/ && $4 >= 40 && $4 > best { best = $4; split($2, a, /[x+]/); x = a[3]; y = a[4] }
			END { if (best) print x, y }'
}
# pointer_click X Y — steer the relative USB mouse onto X,Y by looking at the screen (libinput
# accelerates relative motion, so a move of N units is not N pixels), then click. No pointer on
# the screen fails without clicking. Every step is photographed (shots/live-pointer-*).
pointer_click() {
	mon "mouse_move 40 40"; sleep 0.3
	n=0; while [ "$n" -lt 12 ]; do mon "mouse_move -400 -400"; sleep 0.05; n=$((n + 1)); done
	sleep 2
	snap live-pointer-home
	at="$(pointer_at "$WORK/shots/live-pointer-home.png")"
	if [ -z "$at" ]; then
		echo "qemu-gui-test: gui-pointer: no mouse pointer on the screen" | tee -a "$WORK/pointer.log"
		return 1
	fi
	g=1 k=0
	while [ "$k" -lt 14 ]; do
		# shellcheck disable=SC2086 # $at is "X Y"
		set -- "$1" "$2" $at
		dx=$(($1 - $3)) dy=$(($2 - $4))
		echo "qemu-gui-test: pointer at $3,$4 (target $1,$2, gain $g)" >> "$WORK/pointer.log"
		[ "${dx#-}" -le 6 ] && [ "${dy#-}" -le 6 ] && break
		# the move, in steps of at most 30 units, scaled by the gain measured so far
		# shellcheck disable=SC2046 # three numbers
		set -- "$@" $(awk -v x="$dx" -v y="$dy" -v g="$g" 'BEGIN {
			m = (x < 0 ? -x : x); if ((y < 0 ? -y : y) > m) m = (y < 0 ? -y : y)
			s = int(m / g / 30) + 1; printf "%d %d %d", s, x / g / s, y / g / s }')
		steps=$5 sx=$6 sy=$7
		n=0; while [ "$n" -lt "$steps" ]; do mon "mouse_move $sx $sy"; sleep 0.05; n=$((n + 1)); done
		sleep 1
		snap "live-pointer-step$k" >/dev/null
		new="$(pointer_at "$WORK/shots/live-pointer-step$k.png")"
		[ -n "$new" ] || { echo "qemu-gui-test: gui-pointer: the pointer vanished" | tee -a "$WORK/pointer.log"; return 1; }
		# the gain: pixels moved per unit sent, on the axis that moved most
		g=$(echo "$at $new $((sx * steps)) $((sy * steps)) $g" | awk '{ px = $3 - $1; py = $4 - $2; u = $5; p = px
			if ((py < 0 ? -py : py) > (px < 0 ? -px : px)) { u = $6; p = py }
			if (u != 0 && p / u > 0.2) printf "%.3f", p / u; else print $7 }')
		at="$new" k=$((k + 1))
	done
	mon "mouse_button 1"; sleep 0.2; mon "mouse_button 0"
	sleep 3
	snap live-pointer-clicked
}
# follow PHASE SECONDS — photograph every RIVERGUI SCREEN, type on RIVERGUI TYPE, click on
# RIVERGUI POINTER, until END.
follow() {
	done_=""
	i=0
	while [ "$i" -lt "$2" ]; do
		for s in $(grep -a '^RIVERGUI SCREEN ' "$WORK/serial-$1.log" 2>/dev/null | awk '{ print $3 }' | tr -d '\r'); do
			case " $done_ " in *" $s "*) ;; *) done_="$done_ $s"; snap "$1-$(printf '%02d' "$(echo "$done_" | wc -w)")-$s" ;; esac
		done
		if seen "RIVERGUI TYPE password" "$1" && ! echo "$done_" | grep -q typed; then
			done_="$done_ typed"; type_line "$PASSWORD"
		fi
		# the kiosk's "Español" choice on the welcome screen (1280x800, std VGA)
		if seen "RIVERGUI POINTER" "$1" && ! echo "$done_" | grep -q pointed; then
			done_="$done_ pointed"; pointer_click 510 234 || true
		fi
		seen "RIVERTEST END $1" "$1" && return 0
		kill -0 "$QPID" 2>/dev/null || return 1
		sleep 1; i=$((i + 1))
	done
	return 1
}

echo "qemu-gui-test: phase 1 — live"
: > "$WORK/serial-live.log"
# shellcheck disable=SC2086
start_vm live $MEDIUM
# OVMF with fresh variables sometimes finds no boot option on the first try; reset once.
sleep 8
seen "No bootable option" live && { echo "qemu-gui-test: firmware found no boot option; resetting"; mon system_reset; }
sleep 70
[ "$PROFILE" = workstation ] && sleep 110
if [ "$PROFILE" = workstation ]; then VTKEY=ctrl-alt-f2; else VTKEY=alt-f2; fi
BOOT="sudo sh -c \"mkdir -p /run/rt; mount -r -L RIVERTEST /run/rt; sh /run/rt/live.sh\""
tries=0
until seen "RIVERTEST BEGIN live" live; do
	tries=$((tries + 1))
	[ "$tries" -le 4 ] || die "the live system never ran the kit"
	# Workstation: visit VT3 first. The first Ctrl+Alt+F2 away from the live Plasma session can
	# leave a black screen with the keys still going to kwin (river#134); a switch to any other
	# console first always works, and VT2 is reachable from there.
	[ "$PROFILE" = workstation ] && { mon "sendkey ctrl-alt-f3"; sleep 3; }
	mon "sendkey $VTKEY"; sleep 2
	type_line runink; sleep 2; type_line runink; sleep 3
	type_line "$BOOT"
	sleep 30
done
follow live 5400 || echo "qemu-gui-test: live phase did not finish" >&2
wait_exit 120 || true

if seen "RIVERTEST OK export" live; then
	KEY="$(sed -n 's/^RIVERGUI KEY //p' "$WORK/serial-live.log" | tr -d '\r' | head -1)"
	echo "qemu-gui-test: phase 2 — installed"
	: > "$WORK/serial-installed.log"
	start_vm installed
	if wait_for "passphrase" installed 300; then
		# Send the key; resend while the boot has not gone on (a key typed before the prompt
		# is listening is lost).
		n=0
		while [ "$n" -lt 6 ]; do
			sleep 3
			sz=$(wc -c < "$WORK/serial-installed.log")
			timeout 5 sh -c 'printf "%s\n" "$1" > "$2"' _ "$KEY" "$WORK/ser.in" || true
			sleep 40
			[ "$(wc -c < "$WORK/serial-installed.log")" -gt $((sz + 200)) ] && break
			n=$((n + 1))
		done
	fi
	follow installed $((4800 + HOOK_BUDGET)) || echo "qemu-gui-test: installed phase did not finish" >&2
	wait_exit 120 || true
fi

# --- verdict -----------------------------------------------------------------------------------------
cat "$WORK"/serial-*.log | tr -d '\r' | grep -a '^RIVERTEST ' > "$WORK/results.txt" || true
if [ "$PROFILE" = server ]; then
	want="gui-kiosk gui-recovery gui-pointer gui-edition gui-install harness-wired export unlocked fb-ready fb-k0s fb-finish golden"
else
	# (assert-golden checks a server; a workstation is checked by its own lines)
	want="gui-desktop gui-edition gui-install harness-wired export unlocked home-mounted home-owner firewall machine-id greeting no-base-name hostname plasma-login net-ready"
fi
# the profile's hooks (tests/installed.d): hook-<name> and every check each one declares
want="$want $HOOK_WANT"
echo
echo "qemu-gui-test: results ($WORK)"
fail=0
# shellcheck disable=SC2086 # the list of checks
verdict "$WORK/results.txt" $want || fail=1
echo "  screens: $(cd "$WORK/shots" && ls ./*.png 2>/dev/null | tr '\n' ' ')"
[ "$KEEP" = 1 ] || rm -f "$WORK/target.qcow2"
exit "$fail"
