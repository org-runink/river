#!/bin/sh
# models-fetch.sh — fetch EXACTLY models.lock into MODELS_DIR, verifying every sha256.
#
# The platform's AI models are pinned to their upstream Hugging Face repos (commit revision
# plus sha256 per file) and fetched at ISO BUILD time only. An installed node never fetches
# a model: it verifies what it was given against models.manifest (generated from this lock
# by 50-models-manifest.sh).
#
# Usage:
#   build/models-fetch.sh           fetch what is missing or wrong, verify everything
#   build/models-fetch.sh --check   verify only; no network. Exit 1 on any missing/bad file
#
# Environment:
#   MODELS_DIR           destination (default: ${XDG_CACHE_HOME:-$HOME/.cache}/river-build/models)
#   MODELS_LOCK          the lock (default: models.lock at the repo root)
#   RIVER_MODELS_MIRROR  optional fallback base URL, tried when upstream fails or serves
#                        the wrong bytes. Layout (content-addressed, so a mirror can only
#                        ever serve the pinned bytes):
#                          $RIVER_MODELS_MIRROR/<sha256>                  the whole file, or
#                          $RIVER_MODELS_MIRROR/<sha256>.part000, .part001, ...
#                        split parts (GitHub Release assets are capped at 2 GiB), joined in
#                        order. The mirror itself is defined later; this is its contract.
#   RIVER_MODELS_UPSTREAM  upstream base (default https://huggingface.co)
#
# Every download resumes (`curl -C -`) into <dest>.partial and is renamed into place only
# after its size and sha256 match the lock. A file that already matches is not re-fetched.
set -eu

HERE="$(cd "$(dirname "$0")" && pwd)"
LOCK="${MODELS_LOCK:-$HERE/../models.lock}"
MODELS_DIR="${MODELS_DIR:-${XDG_CACHE_HOME:-$HOME/.cache}/river-build/models}"
UPSTREAM="${RIVER_MODELS_UPSTREAM:-https://huggingface.co}"
MIRROR="${RIVER_MODELS_MIRROR:-}"
CHECK_ONLY=0

case "${1:-}" in
	--check) CHECK_ONLY=1 ;;
	'') ;;
	*) echo "usage: $0 [--check]" >&2; exit 2 ;;
esac

[ -f "$LOCK" ] || { echo "models-fetch: lock $LOCK not found" >&2; exit 1; }

log() { echo "models-fetch: $*" >&2; }

# matches FILE SIZE SHA256 — true iff the file exists with exactly that size and hash.
# The size test first, so a truncated or partial file costs a stat, not a full hash.
matches() {
	[ -f "$1" ] || return 1
	[ "$(wc -c < "$1" | tr -d ' ')" = "$2" ] || return 1
	[ "$(sha256sum "$1" | cut -d' ' -f1)" = "$3" ]
}

# curl_to URL OUT — resumable, fails on HTTP errors, follows the CDN redirect.
curl_to() {
	curl -sS -L --fail --retry 5 --retry-delay 5 --connect-timeout 30 -C - -o "$2" "$1"
}

# from_mirror SHA OUT — whole file first, then numbered parts.
from_mirror() {
	curl_to "$MIRROR/$1" "$2" && return 0
	rm -f "$2"
	n=0
	while :; do
		p=$(printf '%s.part%03d' "$1" "$n")
		# Probe with a 1-byte ranged GET (a HEAD is not reliable against signed
		# release-asset redirects). No part 000 means the mirror does not have it.
		if ! curl -sS -L --fail -r 0-0 -o /dev/null "$MIRROR/$p" 2>/dev/null; then
			[ "$n" -gt 0 ] && break
			return 1
		fi
		curl_to "$MIRROR/$p" "$2.$n" || return 1
		cat "$2.$n" >> "$2" && rm -f "$2.$n"
		n=$((n + 1))
	done
}

total=0
bad=0
rows=0
# Columns: role repo revision file size sha256 license dest   ('#' starts a comment)
while read -r role repo rev file size sha lic dest; do
	case "$role" in ''|\#*) continue ;; esac
	if [ -z "$dest" ] || [ -z "$lic" ]; then
		log "malformed lock row for $repo $file (need 8 columns)"; exit 1
	fi
	rows=$((rows + 1))
	[ "$dest" = "-" ] && dest="${file##*/}"
	out="$MODELS_DIR/$dest"
	total=$((total + size))

	if matches "$out" "$size" "$sha"; then
		log "ok       $dest"
		continue
	fi
	if [ "$CHECK_ONLY" = 1 ]; then
		log "MISSING/BAD $dest ($role, $repo@$rev $file)"
		bad=$((bad + 1))
		continue
	fi

	mkdir -p "$(dirname "$out")"
	tmp="$out.partial"
	# A leftover .partial larger than the pin can never become right by resuming.
	if [ -f "$tmp" ] && [ "$(wc -c < "$tmp" | tr -d ' ')" -gt "$size" ]; then rm -f "$tmp"; fi

	log "fetch    $dest <- $repo@$rev $file ($size bytes)"
	if curl_to "$UPSTREAM/$repo/resolve/$rev/$file" "$tmp"; then
		if matches "$tmp" "$size" "$sha"; then
			mv -f "$tmp" "$out"
			log "verified $dest"
			continue
		fi
		# Complete download, wrong bytes: nothing to resume, never keep it.
		log "upstream served bytes that do not match the pin for $dest"
		rm -f "$tmp"
	fi
	# A curl failure keeps $tmp, so the next run resumes instead of starting over.
	if [ -n "$MIRROR" ]; then
		log "trying mirror $MIRROR for $dest"
		mtmp="$out.mirror.partial"
		rm -f "$mtmp"
		if from_mirror "$sha" "$mtmp" && matches "$mtmp" "$size" "$sha"; then
			mv -f "$mtmp" "$out"
			rm -f "$tmp"
			log "verified $dest (mirror)"
			continue
		fi
		rm -f "$mtmp"
	fi
	log "FAILED   $dest: no source served $size bytes with sha256 $sha"
	bad=$((bad + 1))
done < "$LOCK"

[ "$rows" -gt 0 ] || { log "lock $LOCK has no rows"; exit 1; }
log "$rows file(s), $total bytes pinned in $LOCK; $bad missing or bad under $MODELS_DIR"
[ "$bad" -eq 0 ]
