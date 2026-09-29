#!/bin/sh
# SPDX-FileCopyrightText: 2026 The RIVER Authors
# SPDX-License-Identifier: Apache-2.0
#
# run.sh CANDIDATE...   — validate model candidates, one resident model at a time.
#
# For each candidate: take the host-wide inference lock (flock $MODELS/.inference.lock, so
# no other model server is resident), start its server in podman, wait for /health, run
# the rivervalidate checks for the tiers the candidate is proposed for, save the server
# log, stop the server, release the lock. Evidence lands in $EVID/<candidate>/ (raw
# requests/responses, server.log, summary.json); `run.sh merge` writes results.json.
#
#   MODELS   model cache (default ~/.cache/river-build/models), mounted read-only at /models
#   PORT     host port for the server under test (default 18090, bound to 127.0.0.1)
#   ONLY     comma list of check IDs (partial re-run)
#   CTX_TIMEOUT  cap for the long-context fill (default 50m)
#
# Hard rules this script keeps: one resident model (the lock); it aborts a load that
# drives MemAvailable under $FLOOR_MB or fills /tmp past 90% (a full tmpfs kills servers
# silently); it records the container exit code, so an OOM kill (137) is never mistaken
# for a slow model.
#
# mistral.rs is the only inference engine (decided 2026-09-27), so every candidate is served
# by it: a run on any other server is not evidence, and `rivervalidate merge` marks it
# not_evidence and never lets it pass a tier. The embedding tier has no candidate here: it is
# PENDING (models.tiers) until a 768-dimension model mistral.rs serves is chosen; add it as a
# `serve ... embedding` candidate then.
set -eu
here=$(cd "$(dirname "$0")" && pwd)
MODELS=${MODELS:-$HOME/.cache/river-build/models}
EVID=${EVID:-$MODELS/evidence/validation}
LOCK=$MODELS/.inference.lock
PORT=${PORT:-18090}
FLOOR_MB=${FLOOR_MB:-1024}
CTX_TIMEOUT=${CTX_TIMEOUT:-50m}
MISTRALRS_IMG=${MISTRALRS_IMG:-localhost/runink/mistralrs:cpu-0.9.2-patched}
BIN=$EVID/bin/rivervalidate
AUDIO=$EVID/fixtures/audio
TPL=$EVID/templates

log() { printf '%s %s\n' "$(date -u +%H:%M:%S)" "$*" >&2; }

# candidate NAME sets: TIERS IMG ENTRY ARGS ARTIFACTS CTX. Paths in ARGS are container
# paths (/models = $MODELS, /tpl = $TPL). Every chat server gets --max-seq-len = CTX.
candidate() {
	CTX=16384
	ENTRY=mistralrs
	IMG=$MISTRALRS_IMG
	c=/models/candidates
	serve="serve --host :: --port 8080 --no-ui"
	case $1 in
	qwen3.8-27b-q4km-lmstudio)
		TIERS=general,coder,vision
		ARGS="$serve --jinja-explicit $c/qwen3.8-assets/chat_template.nothink.jinja multimodal --model-id $c/lmstudio --format gguf --quantized-file Qwen3.8-27B-Q4_K_M.gguf --mmproj mmproj-Qwen3.8-27B-BF16.gguf --tok-model-id $c/qwen3.8-assets --max-edge 1024 --max-seq-len $CTX --cpu"
		ARTIFACTS="$MODELS/candidates/lmstudio/Qwen3.8-27B-Q4_K_M.gguf,$MODELS/candidates/lmstudio/mmproj-Qwen3.8-27B-BF16.gguf" ;;
	qwen3.8-27b-q4km-ud)
		TIERS=general,coder,vision
		ARGS="$serve --jinja-explicit $c/qwen3.8-assets/chat_template.nothink.jinja multimodal --model-id $c --format gguf --quantized-file Qwen3.8-27B-UD-Q4_K_M.gguf --mmproj mmproj-F16.gguf --tok-model-id $c/qwen3.8-assets --max-edge 1024 --max-seq-len $CTX --cpu"
		ARTIFACTS="$MODELS/candidates/Qwen3.8-27B-UD-Q4_K_M.gguf,$MODELS/candidates/mmproj-F16.gguf" ;;
	qwen3.8-27b-q3km)
		TIERS=general,coder,vision
		ARGS="$serve --jinja-explicit $c/qwen3.8-assets/chat_template.nothink.jinja multimodal --model-id $c/bartowski-q3 --format gguf --quantized-file Qwen3.8-27B-Q3_K_M.gguf --mmproj ../lmstudio/mmproj-Qwen3.8-27B-BF16.gguf --tok-model-id $c/qwen3.8-assets --max-edge 1024 --max-seq-len $CTX --cpu"
		ARTIFACTS="$MODELS/candidates/bartowski-q3/Qwen3.8-27B-Q3_K_M.gguf,$MODELS/candidates/lmstudio/mmproj-Qwen3.8-27B-BF16.gguf" ;;
	qwen3-14b-q4km)
		# As the coder tier is deployed today (stock template, thinking on unless /no_think),
		# but --max-seqs 1: with --max-seq-len 16384 and 2 sequences the load was OOM-killed
		# on the 25 GiB workstation (exit 137, 2026-09-24).
		TIERS=general,coder
		ARGS="$serve --max-seqs 1 text --model-id /models --format gguf --quantized-file Qwen3-14B-Q4_K_M.gguf --max-seq-len $CTX --cpu"
		ARTIFACTS="$MODELS/Qwen3-14B-Q4_K_M.gguf" ;;
	qwen3-14b-q4km-nothink)
		# Same weights, thinking disabled server-side by the chat template.
		TIERS=general,coder
		ARGS="$serve --max-seqs 1 --jinja-explicit /tpl/qwen3-14b.nothink.jinja text --model-id /models --format gguf --quantized-file Qwen3-14B-Q4_K_M.gguf --max-seq-len $CTX --cpu"
		ARTIFACTS="$MODELS/Qwen3-14B-Q4_K_M.gguf" ;;
	qwen3-vl-2b-q4km)
		TIERS=vision
		ARGS="$serve multimodal --model-id $c/qwen3-vl-2b --format gguf --quantized-file Qwen3VL-2B-Instruct-Q4_K_M.gguf --mmproj mmproj-Qwen3VL-2B-Instruct-Q8_0.gguf --tok-model-id $c/qwen3-vl-2b-assets --max-edge 1024 --max-seq-len $CTX --cpu"
		ARTIFACTS="$MODELS/candidates/qwen3-vl-2b/Qwen3VL-2B-Instruct-Q4_K_M.gguf,$MODELS/candidates/qwen3-vl-2b/mmproj-Qwen3VL-2B-Instruct-Q8_0.gguf" ;;
	voxtral-mini-3b-2507-q4km)
		TIERS=stt
		CTX=8192
		ARGS="$serve multimodal --model-id $c/voxtral-3b --format gguf --quantized-file Voxtral-Mini-3B-2507-Q4_K_M.gguf --mmproj mmproj-Voxtral-Mini-3B-2507-Q8_0.gguf --tok-model-id $c/voxtral-3b-assets --max-seq-len $CTX --cpu"
		ARTIFACTS="$MODELS/candidates/voxtral-3b/Voxtral-Mini-3B-2507-Q4_K_M.gguf,$MODELS/candidates/voxtral-3b/mmproj-Voxtral-Mini-3B-2507-Q8_0.gguf" ;;
	voxtral-mini-4b-realtime-2602)
		TIERS=stt
		CTX=8192
		ARGS="$serve multimodal --model-id /models/voxtral-mini-4b-realtime-2602 --isq q4k --max-seq-len $CTX --cpu"
		ARTIFACTS="$MODELS/voxtral-mini-4b-realtime-2602/consolidated.safetensors" ;;
	tts-known-gap)
		TIERS=tts IMG="" ;;
	*)
		log "unknown candidate $1"
		return 1 ;;
	esac
}

prepare() {
	mkdir -p "$EVID/bin" "$TPL"
	(cd "$here" && go build -o "$BIN" .)
	[ -f "$AUDIO/en.wav" ] || "$here/fixtures/gen-audio.sh" "$AUDIO" >/dev/null
	# Qwen3 server-side no-think template, derived from the upstream chat_template.
	if [ ! -f "$TPL/qwen3-14b.nothink.jinja" ]; then
		tc=$(find "$MODELS/evidence" -path '*Qwen_Qwen3-14B@*' -name tokenizer_config.json | head -1)
		jq -r .chat_template "$tc" |
			sed 's/enable_thinking is defined and enable_thinking is false/true/' >"$TPL/qwen3-14b.nothink.jinja"
	fi
}

memavail_mb() { awk '/MemAvailable/{print int($2/1024)}' /proc/meminfo; }
tmp_pct() { df --output=pcent /tmp | tail -1 | tr -dc 0-9; }

# validate_one NAME — runs INSIDE the lock.
validate_one() {
	name=$1
	candidate "$name"
	out=$EVID/$name${ONLY:+-partial}
	mkdir -p "$out"
	if [ -z "$IMG" ]; then
		"$BIN" run -tiers "$TIERS" -label "${out##*/}" -out "$out" -endpoint none
		return 0
	fi
	cname=val-$name
	podman rm -f "$cname" >/dev/null 2>&1 || true
	log "$name: starting ($IMG $ENTRY), MemAvailable $(memavail_mb) MB, /tmp $(tmp_pct)%"
	# shellcheck disable=SC2086 # ARGS is a word list by design
	podman run -d --name "$cname" -p "127.0.0.1:$PORT:8080" \
		-e RAYON_NUM_THREADS=8 -e OMP_NUM_THREADS=8 \
		-v "$MODELS:/models:ro" -v "$TPL:/tpl:ro" \
		--entrypoint "$ENTRY" "$IMG" $ARGS >/dev/null
	t0=$(date +%s)
	minavail=999999
	status=loading
	while [ "$status" = loading ]; do
		a=$(memavail_mb)
		[ "$a" -lt "$minavail" ] && minavail=$a
		st=$(podman inspect -f '{{.State.Status}}' "$cname" 2>/dev/null || echo gone)
		if [ "$st" != running ]; then
			status="exited(rc=$(podman inspect -f '{{.State.ExitCode}}' "$cname" 2>/dev/null || echo ?))"
		elif [ "$a" -lt "$FLOOR_MB" ]; then
			status="aborted(MemAvailable ${a}MB < ${FLOOR_MB}MB)"
		elif [ "$(tmp_pct)" -gt 90 ]; then
			status="aborted(/tmp $(tmp_pct)% full)"
		elif curl -sf -m 3 "http://127.0.0.1:$PORT/health" >/dev/null 2>&1; then
			status=healthy
		elif [ $(($(date +%s) - t0)) -gt 1800 ]; then
			status="timeout(1800s)"
		else
			sleep 2
		fi
	done
	load_s=$(($(date +%s) - t0))
	log "$name: $status after ${load_s}s (min MemAvailable ${minavail} MB)"
	cid=$(podman inspect -f '{{.Id}}' "$cname" 2>/dev/null || true)
	cg=$(find /sys/fs/cgroup/user.slice -maxdepth 6 -type d -name "libpod-$cid.scope" 2>/dev/null | head -1)
	if [ "$status" = healthy ]; then
		set +e
		"$BIN" run -tiers "${TIERS}" -label "${out##*/}" -out "$out" -ctx "$CTX" \
			-endpoint "http://127.0.0.1:$PORT/v1" -model default -audio "$AUDIO" \
			-cgroup "$cg" -artifacts "$ARTIFACTS" -ctx-timeout "$CTX_TIMEOUT" -only "${ONLY:-}" \
			-server-args "$ENTRY $ARGS" 2>&1 | tee "$out/harness.log"
		set -e
	else
		# No server: every row is recorded as ERROR (not measured), nothing is probed.
		"$BIN" run -tiers "${TIERS}" -label "${out##*/}" -out "$out" -server-failed "$status (see server.log)" \
			-server-args "$ENTRY $ARGS" -only "${ONLY:-}" >"$out/harness.log" 2>&1 || true
	fi
	rc=$(podman inspect -f '{{.State.ExitCode}} {{.State.OOMKilled}}' "$cname" 2>/dev/null || echo "? ?")
	podman logs "$cname" >"$out/server.log" 2>&1 || true
	podman rm -f "$cname" >/dev/null 2>&1 || true
	printf '{"server_status":"%s","load_seconds":%s,"min_memavailable_mb":%s,"exit":"%s"}\n' \
		"$status" "$load_s" "$minavail" "$rc" >"$out/server.json"
	log "$name: done, server stopped (exit/oom: $rc)"
}

if [ "${1:-}" = merge ]; then
	mkdir -p "$EVID/bin"
	(cd "$here" && go build -o "$BIN" .)
	# shellcheck disable=SC2046 # one path per candidate dir, no spaces
	"$BIN" merge -o "$here/results.json" $(ls "$EVID"/*/summary.json)
	exit 0
fi
if [ "${1:-}" = --locked ]; then
	shift
	validate_one "$1"
	exit 0
fi
[ $# -gt 0 ] || { echo "usage: run.sh CANDIDATE... | run.sh merge" >&2; exit 2; }
prepare
for cand in "$@"; do
	candidate "$cand" >/dev/null
	t=$(date +%s)
	log "$cand: waiting for $LOCK"
	flock "$LOCK" "$0" --locked "$cand"
	log "$cand: total $(($(date +%s) - t))s including lock wait"
done
