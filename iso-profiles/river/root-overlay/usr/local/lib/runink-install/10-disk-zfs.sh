#!/bin/sh
# 10-disk-zfs — partition the target, create-or-import the ZFS pool, create the BE.
#
# Layout: GPT with an EFI system partition (FAT, mounted at /boot: kernel + initramfs +
# GRUB) + a ZFS partition holding ONE natively-encrypted pool (aes-256-gcm encryption root
# at the pool root, inherited by every dataset). If the named pool already exists on the
# disk it is IMPORTED (existing-fleet / reinstall case) rather than recreated — and refused
# if it is unencrypted, because ZFS cannot encrypt in place (docs/ENCRYPTION.md).
# The boot environment dataset is created with canmount=noauto mountpoint=/ and mounted at
# $RUNINK_TARGET.
set -eu

DISK="${RUNINK_DISK:?}"
POOL="${RUNINK_POOL:?}"
BE="${RUNINK_BE:?}"
TARGET="${RUNINK_TARGET:?}"

# RUNINK_ZFS_KEY=stdin (the graphical installer): take the key off stdin FIRST, before any tool
# below could read it, and keep it only in this shell (see "the key" below).
STDIN_KEY=""
if [ "${RUNINK_ZFS_KEY:-}" = stdin ]; then
	IFS= read -r STDIN_KEY || STDIN_KEY=""
	exec </dev/null
fi

# MULTI-DISK LAYOUTS come from the hardware install plan (river-plan; the drivers export
# these after the operator confirmed every disk by serial, and 05-hwplan-verify re-checked
# them). Unset, the layout is the single-disk one this step always made:
#   RUNINK_POOL_TOPOLOGY    single | mirror | raidz1 | raidz2
#   RUNINK_POOL_DISKS       the data disks, space-separated; RUNINK_DISK (the boot disk) is one
#   RUNINK_POOL_VDEV_WIDTH  disks per data vdev (several equal raidz2 vdevs on wide sets)
#   RUNINK_SPECIAL_DISKS    two flash disks for a mirrored special (metadata) vdev, or empty
# Every data disk gets the same GPT (ESP + ZFS partition), so any of them can be made
# bootable later; only the boot disk's ESP is mounted at /boot and gets GRUB. Special-vdev
# disks are given to ZFS whole.
TOPOLOGY="${RUNINK_POOL_TOPOLOGY:-single}"
POOL_DISKS="${RUNINK_POOL_DISKS:-$DISK}"
SPECIAL_DISKS="${RUNINK_SPECIAL_DISKS:-}"

part_of() { # partition device name for disk $1, index $2 (nvme/mmc use a p-suffix)
	case "$1" in
		*nvme*|*mmcblk*) echo "${1}p$2" ;;
		*) echo "${1}$2" ;;
	esac
}
part() { part_of "$DISK" "$1"; }
EFI_PART="$(part 1)"
ZFS_PART="$(part 2)"

set -f # the disk lists are word-split on purpose below; never glob them
ndisks=0
for _d in $POOL_DISKS; do ndisks=$((ndisks + 1)); done
case " $POOL_DISKS " in
	*" $DISK "*) ;;
	*) echo "disk-zfs: boot disk $DISK is not one of the pool disks ($POOL_DISKS)" >&2; exit 1 ;;
esac
case "$TOPOLOGY" in
	single) [ "$ndisks" -eq 1 ] || { echo "disk-zfs: topology single with $ndisks disks" >&2; exit 1; } ;;
	mirror|raidz1|raidz2) [ "$ndisks" -ge 2 ] || { echo "disk-zfs: topology $TOPOLOGY with $ndisks disk(s)" >&2; exit 1; } ;;
	*) echo "disk-zfs: unknown RUNINK_POOL_TOPOLOGY '$TOPOLOGY'" >&2; exit 1 ;;
esac
WIDTH="${RUNINK_POOL_VDEV_WIDTH:-0}"
[ "$WIDTH" -gt 0 ] 2>/dev/null || WIDTH="$ndisks"
[ $((ndisks % WIDTH)) -eq 0 ] || { echo "disk-zfs: $ndisks disks do not split into vdevs of $WIDTH" >&2; exit 1; }

# A fresh pool is stamped with the live env's hostid at `zpool create`; make sure that
# is a real non-zero value first (a 0 hostid disables multihost and is fragile), so
# 40-boot-grub-zfs can bake a matching hostid into the target initramfs. A coexistence
# import keeps the existing pool's hostid, which 40 reads back regardless.
if command -v zgenhostid >/dev/null 2>&1 && [ ! -s /etc/hostid ]; then
	echo "disk-zfs: no live-env hostid — generating one before creating the pool"
	zgenhostid -f
fi

# zfs-dkms's pacman install hook does not reliably trigger the actual DKMS
# build — confirmed live on real hardware: `dkms status` showed `zfs/x.y.z:
# added` (registered, never built) even with matching linux-lts-headers
# already installed. `modprobe zfs` then fails with "Module zfs not found"
# since there's genuinely nothing built yet, not just unloaded. Build it
# explicitly before trying to load it — dkms install is a no-op if a prior
# hook run already succeeded.
if ! modinfo zfs >/dev/null 2>&1; then
	echo "disk-zfs: zfs module not built — building it now via dkms"
	zfs_dkms_ver="$(dkms status 2>/dev/null | sed -n 's|^zfs/\([^,]*\),.*|\1|p; s|^zfs/\([^:]*\):.*|\1|p' | head -1)"
	if [ -n "$zfs_dkms_ver" ]; then
		dkms install "zfs/$zfs_dkms_ver"
	else
		echo "disk-zfs: no zfs source registered with dkms — cannot build" >&2
		exit 1
	fi
fi

if ! lsmod 2>/dev/null | grep -q '^zfs '; then
	echo "disk-zfs: zfs module not loaded — loading it"
	modprobe zfs
fi

# --- ZFS native encryption: the key ------------------------------------------------
# EVERY dataset on a new pool is encrypted (aes-256-gcm) by inheritance from ONE encryption
# root: the pool's root dataset. Nothing else can be created unencrypted under it by
# accident, because ZFS children inherit `encryption` and it cannot be turned off later.
#
# The key is GENERATED IN MEMORY: 32 random bytes rendered as 64 hex characters, held in a
# shell variable of THIS process only (not exported, never passed to another step, never
# written to any file — not even tmpfs), and piped straight into `zpool create` on stdin.
# It is keyformat=passphrase: the 64-hex string IS the passphrase, which keeps it typeable
# at a console. It is printed ONCE below as the recovery key and is not recoverable from
# the node afterwards. ZFS has exactly ONE wrapping key per encryption root (no LUKS-style
# key slots), so this recovery key and the boot-time unlock key are the same secret; a
# future unattended provider (docs/ENCRYPTION.md, "Key providers") seals this same string.
#
# RUNINK_ZFS_KEY=own asks the operator for a passphrase instead (typed twice, min 12
# chars, read with echo off). RUNINK_ZFS_KEY=stdin reads the key from this step's stdin (one
# line): the graphical installer generated it in its own memory and has ALREADY shown it to the
# operator as the recovery key (with a QR code), so this step neither prints it nor waits; the
# key still never touches a file. Default: generated.
gen_key() { od -An -tx1 -N32 /dev/urandom | tr -d ' \n'; }

group_key() { # print the key in 8 groups of 8 for transcription
	printf '%s\n' "$1" | sed 's/\(........\)/\1 /g; s/ $//'
}

# pick_groups — three distinct group numbers (1..8), ascending. Random, so the answer has to
# come from what the operator wrote down rather than from memory of a fixed prompt.
pick_groups() {
	od -An -tu1 -N64 /dev/urandom | tr -s ' ' '\n' | grep -v '^$' |
		awk '{ g = $1 % 8 + 1; if (!seen[g]++) { print g; n++ } } n == 3 { exit }' | sort -n
}

# confirm_key KEY — ask for a few of its groups back, to prove it really was recorded.
# "Press Enter" proved nothing: the key is shown ONCE, is stored nowhere, and without it every
# file on the disk is gone — so a glance instead of a transcription destroys the node later.
# A wrong answer is not fatal; it just asks again, with the key still on screen above. If the
# input closes (no terminal behind it) it gives up rather than spinning forever.
confirm_key() {
	printf '\n Confirm you recorded it: type these groups back (counting left to right, 1 to 8).\n\n' >&2
	for _g in $(pick_groups); do
		_want="$(printf '%s' "$1" | cut -c "$(( (_g - 1) * 8 + 1 ))-$(( _g * 8 ))")"
		while :; do
			printf '   group %s of 8: ' "$_g" >&2
			if ! read -r _got; then
				printf '\n disk-zfs: no input to confirm with — the key above is the ONLY copy.\n' >&2
				return 0
			fi
			_got="$(printf '%s' "$_got" | tr -d ' \t' | tr 'A-Z' 'a-z')"
			[ "$_got" = "$_want" ] && break
			printf '   that is not group %s — read it off what you recorded, and try again\n' "$_g" >&2
		done
	done
	printf '\n Recorded. Keep it where you can reach it if this machine will not boot.\n\n' >&2
}

read_own_passphrase() {
	[ -t 0 ] || { echo "disk-zfs: RUNINK_ZFS_KEY=own needs a terminal" >&2; exit 1; }
	while :; do
		stty -echo 2>/dev/null || true
		printf 'ZFS encryption passphrase (min 12 chars): ' >&2; read -r _p1 || _p1=""
		printf '\nRepeat: ' >&2; read -r _p2 || _p2=""
		stty echo 2>/dev/null || true
		printf '\n' >&2
		if [ "${#_p1}" -lt 12 ]; then echo "  too short" >&2; continue; fi
		if [ "$_p1" != "$_p2" ]; then echo "  did not match" >&2; continue; fi
		ZFS_KEY="$_p1"; unset _p1 _p2; return 0
	done
}

show_recovery_key() {
	cat >&2 <<-EOF

		================================================================================
		 RIVER ZFS RECOVERY KEY — pool $POOL (aes-256-gcm, keyformat=passphrase)

		     $(group_key "$ZFS_KEY")

		 Type it WITHOUT the spaces when the boot console asks for the passphrase
		 for '$POOL'. This is shown ONCE. It is not stored anywhere on this node or on
		 the install media. Lose it and every file on this disk is unrecoverable.
		================================================================================

	EOF
	# runink-autoinstall sets RUNINK_UNATTENDED=1: it must never wait for a key press, even
	# when it runs on a console (as build/qemu-test.sh runs it); it used to wait here forever.
	if [ -t 0 ] && [ "${RUNINK_UNATTENDED:-0}" != 1 ]; then
		confirm_key "$ZFS_KEY"
	fi
}

# Import an existing pool if present on this disk; else create fresh. RUNINK_POOL_FRESH=1 (the
# graphical installer, whose operator chose "Erase and install") never imports: the confirmed
# disks are repartitioned and a new pool is created on them.
if [ "${RUNINK_POOL_FRESH:-0}" != 1 ] && zpool import 2>/dev/null | grep -qw "$POOL"; then
	echo "disk-zfs: importing existing pool $POOL"
	# -N: mount nothing yet. An encrypted pool's datasets cannot mount before its key is
	# loaded, and the BE must be mounted at $TARGET before the siblings mount under it.
	zpool import -N -f -R "$TARGET" "$POOL"
	# Existing EFI partition assumed at part 1; do not repartition.
	enc="$(zfs get -H -o value encryption "$POOL" 2>/dev/null || echo off)"
	if [ "$enc" = off ]; then
		# ZFS encryption applies only to datasets CREATED encrypted — an existing pool can
		# NOT be encrypted in place. Installing into it would put the new BE on plaintext
		# disk while this image's contract says every file is encrypted at rest.
		cat >&2 <<-EOF
			disk-zfs: REFUSING — pool $POOL is NOT encrypted (encryption=off on its root).
			  ZFS cannot encrypt existing datasets in place. Migrate with send/recv into a
			  new encrypted root first (docs/ENCRYPTION.md, "Migrating an existing pool"),
			  or install fresh onto a wiped disk. To knowingly install onto the plaintext
			  pool anyway, re-run with RUNINK_ALLOW_PLAINTEXT_POOL=1.
		EOF
		if [ "${RUNINK_ALLOW_PLAINTEXT_POOL:-0}" != 1 ]; then
			zpool export "$POOL" 2>/dev/null || true
			exit 1
		fi
		echo "disk-zfs: WARN — RUNINK_ALLOW_PLAINTEXT_POOL=1: installing onto an UNENCRYPTED pool" >&2
	else
		echo "disk-zfs: pool $POOL is encrypted ($enc) — loading its key"
		# Prompts on the console (keylocation=prompt). -r: every encryption root under it.
		if [ "$(zfs get -H -o value keystatus "$POOL")" != available ]; then
			zfs load-key -r "$POOL"
		fi
	fi
else
	for _d in $POOL_DISKS; do
		echo "disk-zfs: creating fresh GPT on $_d (EFI + ZFS)"
		parted -s "$_d" mklabel gpt
		parted -s "$_d" mkpart ESP fat32 1MiB 1025MiB
		parted -s "$_d" set 1 esp on
		parted -s "$_d" mkpart runink 1025MiB 100%
	done
	if [ "$ndisks" -gt 1 ] && command -v udevadm >/dev/null 2>&1; then
		udevadm settle || true # partition nodes of every disk must exist before mkfs / zpool
	fi
	mkfs.fat -F32 -n RUNINK_EFI "$EFI_PART"
	# The other data disks' ESPs: formatted, not mounted, not yet kept in sync with /boot.
	# Label RIVER_ESP<n> (FAT labels are at most 11 characters).
	_n=0
	for _d in $POOL_DISKS; do
		[ "$_d" = "$DISK" ] && continue
		_n=$((_n + 1))
		mkfs.fat -F32 -n "RIVER_ESP$_n" "$(part_of "$_d" 1)"
	done

	# The vdev spec. Single disk: exactly the partition this step always used.
	if [ "$TOPOLOGY" = single ]; then
		VDEVS="$ZFS_PART"
	else
		VDEVS=""
		_i=0
		for _d in $POOL_DISKS; do
			[ $((_i % WIDTH)) -eq 0 ] && VDEVS="$VDEVS $TOPOLOGY"
			VDEVS="$VDEVS $(part_of "$_d" 2)"
			_i=$((_i + 1))
		done
	fi
	if [ -n "$SPECIAL_DISKS" ]; then
		VDEVS="$VDEVS special mirror $SPECIAL_DISKS"
	fi
	echo "disk-zfs: vdevs:$VDEVS"

	case "${RUNINK_ZFS_KEY:-generated}" in
		own) read_own_passphrase ;;
		generated) ZFS_KEY="$(gen_key)" ;;
		stdin) ZFS_KEY="$STDIN_KEY" ;;
		*) echo "disk-zfs: RUNINK_ZFS_KEY must be 'generated', 'own' or 'stdin'" >&2; exit 1 ;;
	esac
	[ "${#ZFS_KEY}" -ge 12 ] || { echo "disk-zfs: key generation failed" >&2; exit 1; }

	echo "disk-zfs: creating ENCRYPTED pool $POOL ($TOPOLOGY) (aes-256-gcm)"
	# ashift=12, compression + atime tuned for a server.
	#
	# ENCRYPTION (this replaced "do NOT enable encryption — at-rest is the envelope layer's
	# job"). The envelope layer seals application secrets; it never covered the rest of the
	# disk — k0s state, registry blobs, logs, /home. The pool root is now the encryption root
	# and every dataset inherits it. The price is GRUB: its ZFS reader refuses a pool with
	# the encryption feature active, so /boot is no longer on ZFS at all — the kernel,
	# initramfs and grub.cfg live on the FAT ESP mounted at /boot (see below and
	# 40-boot-grub-zfs.sh). The key arrives on stdin (keylocation=prompt reads stdin when it
	# is not a TTY) and never touches a file.
	# shellcheck disable=SC2086 # $VDEVS is a word list by construction (set -f above)
	printf '%s\n' "$ZFS_KEY" | zpool create -f \
		-o ashift=12 \
		-o autotrim=on \
		-O encryption=aes-256-gcm \
		-O keyformat=passphrase \
		-O keylocation=prompt \
		-O compression=zstd \
		-O atime=off \
		-O xattr=sa -O acltype=posixacl \
		-O mountpoint=none \
		-R "$TARGET" \
		"$POOL" $VDEVS
	[ "$(zfs get -H -o value encryption "$POOL")" = aes-256-gcm ] \
		|| { echo "disk-zfs: pool $POOL was created WITHOUT encryption — aborting" >&2; exit 1; }
	if [ "${RUNINK_ZFS_KEY:-generated}" = stdin ]; then
		echo "disk-zfs: recovery key: the one the graphical installer showed (not printed here)"
	else
		show_recovery_key
	fi
	unset ZFS_KEY STDIN_KEY
	zfs create -o mountpoint=none "$POOL/ROOT"
fi

# Boot environment.
if zfs list "$POOL/ROOT/$BE" >/dev/null 2>&1; then
	echo "disk-zfs: BE $POOL/ROOT/$BE already exists — reusing"
else
	zfs create -o canmount=noauto -o mountpoint=/ "$POOL/ROOT/$BE"
fi
zpool set bootfs="$POOL/ROOT/$BE" "$POOL"
zfs mount "$POOL/ROOT/$BE" 2>/dev/null || { mkdir -p "$TARGET"; zfs mount "$POOL/ROOT/$BE"; }

# A home dataset (keeps the ~11 GB models + rootless container store off the BE snapshot).
zfs list "$POOL/home" >/dev/null 2>&1 || zfs create -o mountpoint=/home "$POOL/home"

# THE PLATFORM'S BULK STATE, KEPT OFF THE BE SNAPSHOT TOO.
#
# The /home dataset above exists for exactly this reason and the reasoning still
# holds — the data just grew somewhere else. Everything the platform actually
# accumulates now lives under /var/lib, which is INSIDE the boot environment, so
# runink-backup.sh's `zfs snapshot -r` captured all of it every day.
#
# Measured on the live box 2026-09-07, seven daily snapshots (BACKUP_KEEP=7,
# working exactly as designed) were holding 252G against a 171G live filesystem:
#
#   zprnkos/ROOT/rnk-   USED 424G   USEDSNAP 252G   USEDDS 171G   AVAIL 30.1G
#
# The pool sat at 90% and kubelet evicted every build job for a day. Deleting
# files freed nothing, because the snapshots still referenced them — which is the
# part that costs hours: `df` shows space that `zfs destroy` has to release.
#
# These two are the whole problem, and both are REPRODUCIBLE, which is why they
# belong outside a backup rather than inside one:
#
#   /var/lib/core   the node-local registry (91G of blobs) + model weights (72G).
#                   The registry is rebuildable from CI; weights re-fetch via
#                   cmd/modelfetch. Snapshotting a cache is paying to preserve
#                   something you can regenerate.
#   /var/lib/k0s    containerd's layer store (25G). Every image re-pulls.
#
# They are created as SIBLINGS of ROOT, not children of the BE, because
# `zfs snapshot -r` recurses into descendants only — so this needs no exclusion
# list to maintain and cannot silently stop working when someone renames a BE.
# com.sun:auto-snapshot=false is redundant given that, and set anyway: it states
# the intent to any future tooling that honours it, and to the next reader.
#
# NEW INSTALLS ONLY. An existing box has this state inside its BE already and
# moving it is a migration, not a config change. Until then the mitigation there
# is BACKUP_KEEP — seven days of a churning 170G filesystem is what 252G looks
# like, and 2 would have kept the pool healthy.
# SERVER PLANS ONLY (RUNINK_PLAN_PROFILE, set by the driver and the graphical installer from the
# edition): a workstation runs neither, and an empty unmounted dataset for each is only noise.
siblings=""
[ "${RUNINK_PLAN_PROFILE:-server}" = workstation ] || siblings="state:/var/lib/core containers:/var/lib/k0s"
for spec in $siblings; do
	ds="${spec%%:*}"
	mp="${spec#*:}"
	if ! zfs list "$POOL/$ds" >/dev/null 2>&1; then
		zfs create -o mountpoint="$mp" -o com.sun:auto-snapshot=false "$POOL/$ds"
	fi
done

# Import path: the siblings (/home, /var/lib/core, /var/lib/k0s) were imported with -N;
# mount them now that the BE sits at $TARGET. No-op on a fresh pool (zfs create mounted them).
zfs mount -a 2>/dev/null || true

# Every dataset must be encrypted and unlocked. On a fresh pool this cannot fail (they all
# inherit from the pool root); on an imported pool it catches a plaintext dataset that
# predates the encryption root. Warn, don't abort — RUNINK_ALLOW_PLAINTEXT_POOL already
# made the decision explicit above.
zfs list -H -o name,encryption,keystatus -r "$POOL" | while read -r _n _e _k; do
	if [ "$_e" = off ]; then
		echo "disk-zfs: WARN — dataset $_n is NOT encrypted" >&2
	elif [ "$_k" != available ]; then
		echo "disk-zfs: WARN — dataset $_n is encrypted but its key is not loaded ($_k)" >&2
	fi
done

# The ESP is mounted at /boot — NOT /boot/efi — because GRUB cannot read an encrypted pool,
# so the kernel, the initramfs and grub.cfg must all sit on the unencrypted FAT partition.
# (A separate unencrypted "bpool" with GRUB's feature set would also work; it costs a third
# partition, a second pool to import and keep feature-compatible, and buys nothing the ESP
# does not already give. The ESP is 1 GiB — room for the kernel plus default + fallback
# initramfs several times over.) What stays unencrypted: the kernel, the initramfs, GRUB
# and its config/theme — code and public config, no node state and no key material.
# 20-clone-rootfs excludes /boot/* for exactly this reason: rsync -aHAXS onto FAT fails on
# ownership/ACLs/xattrs, and nothing from the live /boot belongs there.
mkdir -p "$TARGET/boot"
mount "$EFI_PART" "$TARGET/boot"

echo "disk-zfs: pool $POOL, BE $BE mounted at $TARGET, ESP at $TARGET/boot"
