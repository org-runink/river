# shellcheck shell=sh
# SPDX-FileCopyrightText: 2026 The RIVER Authors
# SPDX-License-Identifier: MIT
#
# qemu-hooks.sh — a profile's own installed-system checks for build/qemu-gui-test.sh (sourced,
# not run). A profile may carry tests/installed.d/*.sh; qemu-gui-test.sh copies every
# EXECUTABLE one into its kit and, on the installed system after the first boot and the
# harness's own checks, runs each (sorted, as root, `sh HOOK`, stdout and stderr on the serial
# log) with this file's run_hooks.
#
# The hook contract:
#   header   `# RIVERTEST-CHECKS: name ...`   the checks the hook reports (required; a hook
#            that declares none is refused: it could pass having examined nothing). Names are
#            [a-z0-9][a-z0-9-]*.
#            `# RIVERTEST-TIMEOUT: SECONDS`   optional, default 1200; the hook is killed after it.
#   output   one line per declared check, the harness's own result lines:
#              RIVERTEST OK <name>
#              RIVERTEST FAIL <name> (<why>)
#              RIVERTEST SKIP <name> (<why>)    a check that is deliberately not run yet
#            and any `RIVERTEST NOTE ...` lines it likes.
#   env      /run/rt/config (PROFILE, OFFLINE, and every RIVER_HOOK_* variable of the
#            harness's environment, exported), PATH as the harness's own.
# The verdict requires every declared check (OK passes, SKIP is listed but does not fail, no
# line fails) plus one check per hook, hook-<basename>, which fails when the hook exits non-zero,
# times out, leaves a declared check unreported or reports one twice, or reports a check it did
# not declare.

HOOK_TIMEOUT_DEFAULT=1200

# hooks_list DIR — the hooks in DIR, sorted: executable *.sh files on the host (the kit's FAT
# copy has no mode bits, so every *.sh there is one that was executable).
hooks_list() {
	[ -d "$1" ] || return 0
	for hk_f in "$1"/*.sh; do
		[ -f "$hk_f" ] || continue
		[ "${HOOKS_ANY_MODE:-0}" = 1 ] || [ -x "$hk_f" ] || continue
		printf '%s\n' "$hk_f"
	done
}

# hook_checks FILE — the names its RIVERTEST-CHECKS header declares; fails on none or a bad one.
hook_checks() {
	hk_c="$(sed -n 's/^#[[:space:]]*RIVERTEST-CHECKS:[[:space:]]*//p' "$1" | tr '\n' ' ')"
	[ -n "$(printf '%s' "$hk_c" | tr -d ' ')" ] || return 1
	for hk_n in $hk_c; do
		printf '%s\n' "$hk_n" | grep -Eq '^[a-z0-9][a-z0-9-]*$' || return 1
	done
	# shellcheck disable=SC2086 # one line, single spaces
	echo $hk_c
}

# hook_timeout FILE — its RIVERTEST-TIMEOUT in seconds (default HOOK_TIMEOUT_DEFAULT).
hook_timeout() {
	hk_t="$(sed -n 's/^#[[:space:]]*RIVERTEST-TIMEOUT:[[:space:]]*\([0-9][0-9]*\).*/\1/p' "$1" | head -1)"
	echo "${hk_t:-$HOOK_TIMEOUT_DEFAULT}"
}

# hooks_want DIR — on the host: every check the hooks in DIR add to the verdict (hook-<base> and
# the declared names), one line. Fails, naming the hook, on a hook without a valid header.
hooks_want() {
	hk_w=""
	for hk_f in $(hooks_list "$1"); do
		hk_c="$(hook_checks "$hk_f")" || { echo "qemu-hooks: $hk_f: no valid '# RIVERTEST-CHECKS: name ...' header" >&2; return 1; }
		hk_w="$hk_w hook-$(basename "$hk_f" .sh) $hk_c"
	done
	# shellcheck disable=SC2086 # one line, single spaces
	echo $hk_w
}

# hooks_budget DIR — on the host: the seconds the hooks in DIR may take together.
hooks_budget() {
	hk_s=0
	for hk_f in $(hooks_list "$1"); do hk_s=$((hk_s + $(hook_timeout "$hk_f") + 15)); done
	echo "$hk_s"
}

# run_hooks DIR LOGDIR — on the installed system: run every hook in DIR, pass its lines through,
# and report hook-<base> for each (the contract above).
run_hooks() {
	for hk_f in $(HOOKS_ANY_MODE=1 hooks_list "$1"); do
		hk_b="$(basename "$hk_f" .sh)"
		hk_log="$2/rt-hook-$hk_b.log"
		if ! hk_checks="$(hook_checks "$hk_f")"; then
			echo "RIVERTEST FAIL hook-$hk_b (no valid RIVERTEST-CHECKS header)"
			continue
		fi
		hk_t="$(hook_timeout "$hk_f")"
		echo "RIVERTEST NOTE hook $hk_b: checks $hk_checks; timeout ${hk_t}s"
		{ hk_x=0; timeout -k 10 "$hk_t" sh "$hk_f" 2>&1 || hk_x=$?; echo "$hk_x" > "$hk_log.rc"; } | tee "$hk_log"
		hk_rc="$(cat "$hk_log.rc" 2>/dev/null || echo lost)"
		hk_why=""
		case "$hk_rc" in
			0) ;;
			124|137) hk_why="timed out after ${hk_t}s;" ;;
			*) hk_why="exit $hk_rc;" ;;
		esac
		for hk_c in $hk_checks; do
			hk_n="$(grep -Ec "^RIVERTEST (OK|FAIL|SKIP) $hk_c( |\$)" "$hk_log" || true)"
			[ "$hk_n" = 1 ] || hk_why="$hk_why $hk_c reported $hk_n times;"
		done
		for hk_c in $(sed -nE 's/^RIVERTEST (OK|FAIL|SKIP) ([^ ]+).*/\2/p' "$hk_log" | sort -u); do
			case " $hk_checks " in *" $hk_c "*) ;; *) hk_why="$hk_why $hk_c not declared;" ;; esac
		done
		if [ -z "$hk_why" ]; then echo "RIVERTEST OK hook-$hk_b"; else echo "RIVERTEST FAIL hook-$hk_b (${hk_why# })"; fi
	done
}

# hooks_env — on the host: every RIVER_HOOK_* variable of this environment as `export` lines for
# the kit's config (values single-quoted).
hooks_env() {
	env | grep -E '^RIVER_HOOK_[A-Za-z0-9_]*=' | while IFS= read -r hk_kv; do
		hk_k="${hk_kv%%=*}" hk_v="${hk_kv#*=}"
		printf "export %s='%s'\n" "$hk_k" "$(printf '%s' "$hk_v" | sed "s/'/'\\\\''/g")"
	done
}

# verdict RESULTS CHECK... — on the host: one line per check from the RIVERTEST lines in
# RESULTS; fails if any check has no OK. A SKIP (with no FAIL) is listed and does not fail.
verdict() {
	hk_r="$1"; shift
	hk_v=0
	for hk_c in "$@"; do
		if grep -q "^RIVERTEST OK $hk_c\$" "$hk_r"; then echo "  PASS  $hk_c"
		elif grep -q "^RIVERTEST SKIP $hk_c\( \|\$\)" "$hk_r" && ! grep -q "^RIVERTEST FAIL $hk_c\( \|\$\)" "$hk_r"; then
			echo "  SKIP  $hk_c $(grep "^RIVERTEST SKIP $hk_c" "$hk_r" | head -1 | sed 's/^RIVERTEST SKIP [^ ]*//')"
		else
			echo "  FAIL  $hk_c $(grep "^RIVERTEST FAIL $hk_c" "$hk_r" | head -1 | sed 's/^RIVERTEST FAIL [^ ]*//')"; hk_v=1
		fi
	done
	return "$hk_v"
}
