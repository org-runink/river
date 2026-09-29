#!/bin/sh
# SPDX-FileCopyrightText: 2026 The RIVER Authors
# SPDX-License-Identifier: Apache-2.0
#
# gen-audio.sh OUTDIR — synthesise the STT fixtures locally (no network, no third-party
# recordings) from the committed transcripts in fixtures/audio/<lang>.txt:
#
#   en        piper (neural TTS) with a local voice model: $PIPER + $PIPER_VOICE
#   es fr pt  espeak-ng (formant synthesis). Robotic, so their WER is a PESSIMISTIC bound;
#             natural-speech es/fr/pt fixtures are still missing and are not faked.
#
# Every WAV is converted to exactly what the STT tier accepts: PCM s16le, 16 kHz, mono.
# The WAVs are not committed (the voices' outputs are not ours to relicense); <lang>.src
# records how each was made, and run.sh records their sha256 in the evidence.
set -eu
here=$(cd "$(dirname "$0")" && pwd)
out=${1:?usage: gen-audio.sh OUTDIR}
PIPER=${PIPER:-$HOME/.local/share/ab-piper/piper-bin-linux/piper}
PIPER_VOICE=${PIPER_VOICE:-$HOME/.local/share/ab-piper/piper-voice-jenny/voice.onnx}
# piper wants <model>.json; this voice ships it as voice.json beside voice.onnx.
PIPER_CONFIG=${PIPER_CONFIG:-$(dirname "$PIPER_VOICE")/voice.json}
mkdir -p "$out"
tmp=$(mktemp -d "${TMPDIR:-/tmp}/genaudio.XXXXXX")
trap 'rm -rf "$tmp"' EXIT

to16k() { ffmpeg -nostdin -loglevel error -y -i "$1" -map_metadata -1 -fflags +bitexact -flags:a +bitexact -ac 1 -ar 16000 -c:a pcm_s16le "$2"; }

for lang in en es fr pt; do
	cp "$here/audio/$lang.txt" "$out/$lang.txt"
	rm -f "$out/$lang.wav" "$out/$lang.src"
done

if [ -x "$PIPER" ] && [ -f "$PIPER_VOICE" ]; then
	# piper needs its config and espeak-ng-data passed explicitly.
	LD_LIBRARY_PATH=$(dirname "$PIPER") "$PIPER" --model "$PIPER_VOICE" --config "$PIPER_CONFIG" \
		--espeak_data "$(dirname "$PIPER")/espeak-ng-data" --output_file "$tmp/en.wav" <"$here/audio/en.txt" >/dev/null 2>&1
	to16k "$tmp/en.wav" "$out/en.wav"
	echo "piper $(basename "$(dirname "$PIPER_VOICE")")/$(basename "$PIPER_VOICE") (neural, en_GB)" >"$out/en.src"
else
	echo "gen-audio: piper or voice not found ($PIPER, $PIPER_VOICE); en fixture MISSING" >&2
	rm -f "$out/en.txt"
fi

if command -v espeak-ng >/dev/null 2>&1; then
	for pair in es:es fr:fr-fr pt:pt-br; do
		lang=${pair%%:*} voice=${pair#*:}
		espeak-ng -v "$voice" -s 150 -w "$tmp/$lang.wav" -f "$here/audio/$lang.txt"
		to16k "$tmp/$lang.wav" "$out/$lang.wav"
		echo "espeak-ng $(espeak-ng --version | awk '{print $4}') voice=$voice (formant synthesis; pessimistic)" >"$out/$lang.src"
	done
else
	echo "gen-audio: espeak-ng not found; es/fr/pt fixtures MISSING" >&2
	for lang in es fr pt; do rm -f "$out/$lang.txt"; done
fi
ls -l "$out"
