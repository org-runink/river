// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: Apache-2.0

package main

// The requirement table. One row per capability a serving tier must provide, stated as
// a generic, numeric threshold. Each check in this suite protects exactly one row and
// reports its ID; the mapping from a row to the downstream callers that depend on it is
// kept by the consumers, not here.

type requirement struct {
	ID    string
	Tier  string
	Text  string
	Hard  bool // a hard gate: a tier's model must pass every hard row
	Floor string
}

var requirements = []requirement{
	// general — the instruction-following chat tier.
	{"GEN-CTX", "general", "context fill at ≥16384 tokens with needle retrieval", true, "needle found at ≥15.5k prompt tokens"},
	{"GEN-TOON", "general", "TOON output parses at temperature 0", true, "strict TOON tabular array decode, values match"},
	{"GEN-JSON", "general", "JSON output parses at temperature 0", true, "json.Unmarshal of the reply (code fences tolerated and reported)"},
	{"GEN-NOTHINK", "general", "no reasoning trace when the request carries no /no_think", true, "no <think> text, empty reasoning_content"},
	{"GEN-LANG", "general", "answers stay in the request language (es, fr, pt, pt-BR)", true, "language-ID of each reply equals request language"},
	{"GEN-TPS", "general", "decode speed", true, "≥2.8 tok/s"},
	{"GEN-OUT8K", "general", "max_tokens=8192 admitted and streamed without error", true, "HTTP 200 + streaming; full-length time extrapolated"},
	// coder — the tool-using coding tier.
	{"COD-CTX", "coder", "context fill at ≥16384 tokens with needle retrieval", true, "needle found at ≥15.5k prompt tokens"},
	{"COD-NOTHINK", "coder", "the /no_think soft switch is honoured", true, "no non-empty <think> block, empty reasoning_content"},
	{"COD-STOP", "coder", "stop sequences \\nObservation: and \\n<|im_end|> are honoured", true, "reply ends before 'Observation:'"},
	{"COD-JSON", "coder", "JSON tool-call output parses", true, "object with string tool + object args"},
	{"COD-TPS", "coder", "decode speed", true, "≥2.8 tok/s"},
	// vision — image understanding.
	{"VIS-BBOX", "vision", "bbox_2d returned in PIXEL coordinates of the input image", true, "IoU ≥0.5 vs pixel truth; FAIL if the box only matches as 0-1000 normalised"},
	{"VIS-MULTI", "vision", "several images (JPEG + PNG) in one request", true, "both images described correctly, in order"},
	{"VIS-EDGE", "vision", "images up to max edge 1024 (and larger inputs resized server-side)", true, "1024-edge and 1600-edge inputs admitted"},
	{"VIS-RSS", "vision", "resident memory", true, "≤18 GiB (anon + file_mapped + swap, peak while serving)"},
	{"VIS-TPS", "vision", "decode speed", true, "≥1 tok/s"},
	// stt — speech to text through chat audio_url.
	{"STT-AUDIO", "stt", "chat audio_url data-URI WAV 16 kHz mono s16le accepted", true, "HTTP 200 with a non-empty transcript"},
	{"STT-WER-EN", "stt", "English transcription accuracy", true, "WER ≤0.15"},
	{"STT-WER-ES", "stt", "Spanish transcription accuracy", true, "WER ≤0.30 (synthetic-voice fixture)"},
	{"STT-WER-FR", "stt", "French transcription accuracy", true, "WER ≤0.30 (synthetic-voice fixture)"},
	{"STT-WER-PT", "stt", "Portuguese transcription accuracy", true, "WER ≤0.30 (synthetic-voice fixture)"},
	// embedding.
	{"EMB-DIM", "embedding", "vector dimension exactly 768 (index compatibility)", true, "len(vector) == 768"},
	{"EMB-PREFIX", "embedding", "search_query:/search_document: prefixed ranking is sane", true, "relevant document ranks first for every query"},
	{"EMB-2048", "embedding", "2048-token input accepted", true, "HTTP 200 for a ~2000-token input"},
	// tts — text to speech languages.
	{"TTS-LANG", "tts", "voices for en, es, fr, pt", true, "a voice exists for every language"},
	{"TTS-VOICE", "tts", "the request's voice field selects the voice", true, "voice field honoured"},
}

func reqByID(id string) requirement {
	for _, r := range requirements {
		if r.ID == id {
			return r
		}
	}
	return requirement{ID: id}
}
