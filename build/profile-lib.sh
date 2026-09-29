# shellcheck shell=sh
# SPDX-FileCopyrightText: 2026 The RIVER Authors
# SPDX-License-Identifier: MIT
#
# profile-lib.sh — what an ISO profile says about itself. Sourced (POSIX sh) by
# build/local-iso.sh on the host and by scripts/build-iso-box.sh in the build container.
#
# A profile is a directory with profile.yaml, Packages-Root, root-overlay/ ... . The one in-tree
# profile, iso-profiles/river (Runink River, the developer workstation), is known by name.
# A profile that lives OUTSIDE this repository (a downstream distribution, docs/BUILD.md
# "Downstream distributions") must describe itself in river-profile.env, a KEY=value file
# that is READ, never sourced:
#
#   KIND=server | workstation     what the image is: a server takes the model and downstream
#                                 payloads and has a private variant; a workstation takes none
#   ISO_LABEL=EXAMPLE_SERVER      the ISO volume label AND the live root=LABEL kernel argument:
#                                 unique per image, [A-Z0-9_], at most 32 characters. The
#                                 profile's edition descriptor for itself must carry it as
#                                 medium_label.
#   ISO_NAME=example-server       the ISO file name prefix: <ISO_NAME>-<date>-x86_64.iso
#   GRUB_TITLE=Example Server     the live menu title, for a profile that keeps Artix's menu
#                                 (a profile with grub/grub.cfg writes its own entries)
#   EDITION=server                optional: the id of the image's own edition descriptor, for a
#                                 medium that carries SEVERAL editions under one ISO_LABEL, each
#                                 booted from its own menu entry with river.edition=<id>
#                                 (docs/BUILD.md "One medium, several editions")
#
# river_profile_read <dir> <name> sets RP_KIND RP_ISO_LABEL RP_ISO_NAME RP_GRUB_TITLE RP_EDITION, or
# prints why not and returns 1.

river_profile_read() {
	_rp_dir="$1" _rp_name="$2"
	RP_KIND="" RP_ISO_LABEL="" RP_ISO_NAME="" RP_GRUB_TITLE="" RP_EDITION=""
	case "$_rp_name" in
		river) RP_KIND=workstation RP_ISO_LABEL=RIVER RP_ISO_NAME=runink-river RP_GRUB_TITLE="Runink River" ;;
	esac
	if [ -f "$_rp_dir/river-profile.env" ]; then
		while IFS= read -r _rp_line || [ -n "$_rp_line" ]; do
			case "$_rp_line" in ''|\#*) continue ;; esac
			_rp_key="${_rp_line%%=*}" _rp_val="${_rp_line#*=}"
			case "$_rp_key" in
				KIND) RP_KIND="$_rp_val" ;;
				ISO_LABEL) RP_ISO_LABEL="$_rp_val" ;;
				ISO_NAME) RP_ISO_NAME="$_rp_val" ;;
				GRUB_TITLE) RP_GRUB_TITLE="$_rp_val" ;;
				EDITION) RP_EDITION="$_rp_val" ;;
				*) echo "profile: $_rp_dir/river-profile.env: unknown key $_rp_key" >&2; return 1 ;;
			esac
		done < "$_rp_dir/river-profile.env"
	elif [ -z "$RP_KIND" ]; then
		echo "profile: $_rp_dir has no river-profile.env (a profile outside this repository must describe itself; docs/BUILD.md)" >&2
		return 1
	fi
	case "$RP_KIND" in server|workstation) ;; *) echo "profile: $_rp_name: KIND must be server or workstation" >&2; return 1 ;; esac
	case "$RP_ISO_LABEL" in ''|*[!A-Z0-9_]*) echo "profile: $_rp_name: ISO_LABEL must be [A-Z0-9_]" >&2; return 1 ;; esac
	[ "${#RP_ISO_LABEL}" -le 32 ] || { echo "profile: $_rp_name: ISO_LABEL is longer than 32 characters" >&2; return 1; }
	case "$RP_ISO_NAME" in ''|-*|*[!a-z0-9-]*) echo "profile: $_rp_name: ISO_NAME must be [a-z0-9-]" >&2; return 1 ;; esac
	# The title goes into a sed expression and a GRUB menu entry: plain characters only.
	case "$RP_GRUB_TITLE" in ''|*[!A-Za-z0-9\ ._+-]*) echo "profile: $_rp_name: GRUB_TITLE must be letters, digits, spaces and . _ + -" >&2; return 1 ;; esac
	[ "${#RP_GRUB_TITLE}" -le 60 ] || { echo "profile: $_rp_name: GRUB_TITLE is longer than 60 characters" >&2; return 1; }
	# EDITION follows the installer's descriptor id rule (installer/internal/wizard/editions.go).
	case "$RP_EDITION" in
		'') ;;
		[a-z]*[!a-z0-9-]*|[!a-z]*) echo "profile: $_rp_name: EDITION must be a descriptor id ([a-z][a-z0-9-]*)" >&2; return 1 ;;
	esac
	[ "${#RP_EDITION}" -le 32 ] || { echo "profile: $_rp_name: EDITION is longer than 32 characters" >&2; return 1; }
	return 0
}

# river_profile_self_edition <dir> <label> [<edition>] — the profile's edition descriptors
# (live-overlay/usr/share/river/installer/editions/*.json) must include exactly one for the
# image itself, or the graphical installer does not know what it is installing and falls back
# to a minimal edition. The image's own descriptor is the one with medium_label == its ISO
# label; with <edition> (river-profile.env EDITION, a medium carrying several editions under
# one label), the one with that label AND id == <edition>, and then every live menu entry in
# grub/kernels.cfg must pass river.edition=<edition>, which is how the installer tells them
# apart on the running medium.
river_profile_self_edition() {
	_rp_ed="$1/live-overlay/usr/share/river/installer/editions"
	ls "$_rp_ed"/*.json >/dev/null 2>&1 || { echo "profile: $1 has no edition descriptor under live-overlay/usr/share/river/installer/editions/" >&2; return 1; }
	_rp_n=0
	for _rp_f in "$_rp_ed"/*.json; do
		grep -q "\"medium_label\": *\"$2\"" "$_rp_f" || continue
		[ -z "${3:-}" ] || grep -q "\"id\": *\"$3\"" "$_rp_f" || continue
		_rp_n=$((_rp_n + 1))
	done
	[ "$_rp_n" -eq 1 ] || { echo "profile: $1: $_rp_n edition descriptor(s) with medium_label \"$2\"${3:+ and id \"$3\"} (want exactly 1)" >&2; return 1; }
	[ -n "${3:-}" ] || return 0
	[ -f "$1/grub/kernels.cfg" ] || { echo "profile: $1: EDITION=$3 needs grub/kernels.cfg entries that pass river.edition=$3" >&2; return 1; }
	_rp_n="$(grep -c '^[[:space:]]*linux[[:space:]]' "$1/grub/kernels.cfg" || true)"
	[ "$_rp_n" -gt 0 ] || { echo "profile: $1/grub/kernels.cfg has no linux line" >&2; return 1; }
	if grep '^[[:space:]]*linux[[:space:]]' "$1/grub/kernels.cfg" | grep -qv "[[:space:]]river\.edition=$3\([[:space:]]\|\$\)"; then
		echo "profile: $1/grub/kernels.cfg: every linux line must pass river.edition=$3" >&2
		return 1
	fi
	return 0
}

# river_profile_stage <src> <branding-dir or ""> <dest> — the copy a staged build uses: <src>
# copied to <dest> (replaced), then the edition branding layered on: <branding>/overlay/ copied
# over it, and every profile-relative path in <branding>/remove deleted. A remove entry that
# names nothing is an error (a stale list hides what the branding no longer does).
river_profile_stage() {
	_rp_src="$1" _rp_br="$2" _rp_dst="$3"
	[ -f "$_rp_src/profile.yaml" ] || { echo "profile: no profile.yaml in $_rp_src" >&2; return 1; }
	rm -rf "$_rp_dst"
	mkdir -p "$(dirname "$_rp_dst")"
	cp -a "$_rp_src" "$_rp_dst"
	[ -n "$_rp_br" ] || return 0
	[ -d "$_rp_br/overlay" ] || { echo "profile: $_rp_br has no overlay/" >&2; return 1; }
	cp -a "$_rp_br/overlay/." "$_rp_dst/"
	[ -f "$_rp_br/remove" ] || return 0
	while IFS= read -r _rp_p || [ -n "$_rp_p" ]; do
		case "$_rp_p" in
			''|\#*) continue ;;
			/*|*..*) echo "profile: $_rp_br/remove: '$_rp_p' must be profile-relative, without '..'" >&2; return 1 ;;
		esac
		[ -e "$_rp_dst/$_rp_p" ] || { echo "profile: $_rp_br/remove: $_rp_p is not in the profile (stale entry)" >&2; return 1; }
		rm -rf "${_rp_dst:?}/$_rp_p"
	done < "$_rp_br/remove"
	return 0
}
