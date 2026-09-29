#!/bin/sh
# 00-preflight — verify the target can run the golden image.
set -eu

fail() { echo "preflight: FAIL — $*" >&2; exit 1; }
ok()   { echo "preflight: ok — $*"; }

# UEFI (ZFS-root + our GRUB path assume EFI).
[ -d /sys/firmware/efi ] || fail "not booted in UEFI mode (EFI required)"
ok "UEFI firmware present"

# CPU features: AVX2/FMA/F16C (x86-64-v3-ish) — the baseline the platform targets.
for feat in avx2 fma f16c; do
	grep -qw "$feat" /proc/cpuinfo || fail "CPU lacks $feat (need AVX2/FMA/F16C)"
done
ok "CPU has AVX2/FMA/F16C"

# RAM. Both drivers install from a plan (river-plan), and the plan is the authority on RAM:
# it refuses below the documented minimum (docs/INSTALLER-HARDWARE.md), and a --lab plan
# records every minimum it waived. A second, different floor here used to contradict it: a
# 6 GiB lab VM with a --lab plan was refused by this step after the plan accepted it. Without
# a plan (a step run by hand) the old floor stays.
mem_kb="$(awk '/MemTotal/{print $2}' /proc/meminfo)"
if [ -n "${RUNINK_PLAN_FILE:-}" ] && [ -f "$RUNINK_PLAN_FILE" ]; then
	ok "RAM $((mem_kb / 1024)) MiB (checked by the install plan)"
else
	[ "$mem_kb" -ge 8000000 ] || fail "need >= 8 GiB RAM (have $((mem_kb / 1024)) MiB)"
	ok "RAM $((mem_kb / 1024 / 1024)) GiB"
fi

# Target disk present.
[ -b "${RUNINK_DISK:?}" ] || fail "target disk $RUNINK_DISK is not a block device"
ok "target disk $RUNINK_DISK"

# Tools the steps rely on in the live env (install-from-live: no basestrap/pacstrap).
for t in zpool zfs parted mkfs.fat rsync; do
	command -v "$t" >/dev/null 2>&1 || echo "preflight: WARN — $t not found (Packages-Live?)"
done

echo "preflight: passed"
