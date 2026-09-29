# Runink River inference model selection — measured 2026-09-24

The validation suite (`validation/`) picked one model per serving tier, and this file
records the choices. Every PASS/FAIL below was measured by `run.sh` and `rivervalidate` (the
llama.cpp candidates, NOT EVIDENCE, have since been removed from `run.sh`).
The raw requests, responses and server logs are under
`~/.cache/river-build/models/evidence/validation/<candidate>/`, and the machine summary is
`validation/results.json`. A value marked **EXTRAPOLATION** was computed, not measured. A
row marked **NOT MEASURED** has no measurement behind it.

**Selection rule.** A tier's model must pass every hard gate for that tier. Among the models
that pass, the one with the smaller resident size wins. If no model passes, this file says so
and names the least-bad option and the deployment change it needs.

**Measurement host.** One 25 GiB workstation: AMD Ryzen 7 8840U, 8 cores / 16 threads, AVX2,
CPU only, `RAYON_NUM_THREADS=8`, zram swap. Other sessions were using the host at the same
time. The host lock (`flock $MODELS/.inference.lock`) kept every model server serialized,
but it does not cover other workloads. Treat the tok/s figures as lower bounds for the
93 GiB target node. Memory is a property of the model and the engine, so the resident sizes
carry over.

**Engine: mistral.rs only (decided 2026-09-27).** mistral.rs 0.9.2
(`localhost/runink/mistralrs:cpu-0.9.2-patched`) is the only inference engine. On
2026-09-24 some candidates were also run on llama.cpp, because the model did not load or did
not fit under mistral.rs 0.9.2 on this host. **Every llama.cpp result in this file is NOT
EVIDENCE**: it is kept as the history of what was tried, it passes no gate, and no selection
rests on it. `validation/results.json` marks those runs and their verdicts `not_evidence`
(`rivervalidate merge` sets it for any run not served by mistral.rs, and its test fails on a
committed verdict that passes on another engine). A tier whose only passing run was on
llama.cpp is **NOT MEASURED** until it is re-run on mistral.rs.

## Selection

| Tier | Chosen | HF repo @ revision / file | sha256 | Hard gates (measured) | Resident (measured) | KV MiB / 1k tok | Status |
|---|---|---|---|---|---|---|---|
| general | **Qwen3-14B Q4_K_M**, server-side no-think template (the same weights and server as coder) | `Qwen/Qwen3-14B-GGUF` @ `530227a7d994db8eca5ab5ced2fb692b614357fd` / `Qwen3-14B-Q4_K_M.gguf` | `500a8806e85ee9c83f3ae08420295592451379b4f8cf2d0f41c15dffeb6b81f0` | GEN-CTX PASS (needle at 15,593 prompt tokens) · GEN-TOON PASS · GEN-JSON PASS (strict, unfenced) · GEN-NOTHINK PASS (4 completion tokens, empty reasoning) · GEN-LANG PASS (es/fr/pt/pt-BR) · GEN-TPS PASS 5.90 tok/s · GEN-OUT8K PASS (admitted; full 8192 tokens ≈ 1,388 s EXTRAPOLATION) — all on llama.cpp: **NOT EVIDENCE** | 16.93 GiB peak at 16k on llama.cpp (NOT EVIDENCE); mistral.rs: OOM-killed on this host | 160 | **NOT MEASURED** on mistral.rs (the llama.cpp gates are NOT EVIDENCE) |
| coder | **Qwen3-14B Q4_K_M**, server-side no-think template | `Qwen/Qwen3-14B-GGUF` @ `530227a7d994db8eca5ab5ced2fb692b614357fd` / `Qwen3-14B-Q4_K_M.gguf` | `500a8806e85ee9c83f3ae08420295592451379b4f8cf2d0f41c15dffeb6b81f0` | COD-CTX PASS (needle at 15,593 prompt tokens) · COD-NOTHINK PASS · COD-STOP PASS · COD-JSON PASS · COD-TPS PASS 5.90 tok/s — all on llama.cpp: **NOT EVIDENCE** | 16.93 GiB peak at 16k context on llama.cpp (NOT EVIDENCE). **mistral.rs 0.9.2: OOM-killed (exit 137) on this 25 GiB host at 16k context** | 160 | **NOT MEASURED** on mistral.rs (see coder) |
| vision | **none passes every gate**. Least-bad: **Qwen3-VL-2B-Instruct Q4_K_M + mmproj Q8_0** | `Qwen/Qwen3-VL-2B-Instruct-GGUF` @ `52d6c8ffea26cc873ac5ad116f8631268d7eb503` / `Qwen3VL-2B-Instruct-Q4_K_M.gguf` + `mmproj-Qwen3VL-2B-Instruct-Q8_0.gguf` | `089d75c52f4b7ffc56ba998ffc50aae89fcafc755f9e7208aacca281dca6c2ae` / `f9a68fabba69c3b81e153367b2c7521030b0fa8bb0de400c9599c8e6725f9c82` | **VIS-BBOX FAIL** (box is 0-1000 normalised: pixel IoU 0.00, IoU 0.94 when read as normalised) · VIS-MULTI PASS · VIS-EDGE PASS (1600×900 resized) · VIS-TPS PASS 21.93 tok/s · VIS-RSS PASS 6.97 GiB — mistral.rs | 6.97 GiB (7.19 GiB cgroup peak) | 112 | FAIL (bbox convention) |
| stt | **none passes every gate**. Least-bad: **Voxtral-Mini-4B-Realtime-2602** (safetensors, `--isq q4k`) | `mistralai/Voxtral-Mini-4B-Realtime-2602` @ `2769294da9567371363522aac9bbcfdd19447add` / `consolidated.safetensors` | `263f178fe752c90a2ae58f037a95ed092db8b14768b0978b8c48f66979c8345d` | STT-AUDIO PASS · WER en 0.06 PASS · es 0.05 PASS · **fr 0.74 FAIL** · pt 0.00 PASS | 7.10 GiB serving, **15.80 GiB cgroup peak during load** (safetensors read + ISQ) | 104 | FAIL (fr) |
| embedding | **PENDING: none.** No engine: mistral.rs is the only engine, and the model is being re-selected for one mistral.rs serves (dimension must match the vector indexes, 768) | n/a (the former nomic-embed-text-v1.5 Q8_0 pin is removed from `models.lock`) | n/a | its EMB-DIM / EMB-PREFIX / EMB-2048 PASS rows were measured on llama.cpp: **NOT EVIDENCE** | n/a | n/a | PENDING |
| tts | **none**: known gap | n/a (not a model candidate) | n/a | TTS-LANG FAIL (en only, es/fr/pt missing) · TTS-VOICE FAIL (voice field ignored) | n/a | 0 | FAIL (known, from source) |

The KV cost is computed from each model's config as `2 × layers × kv_heads × head_dim × 2 bytes
(f16) × 1024 / 2^20`. For Qwen3.8-27B only its 16 full-attention layers hold a KV cache; its
48 linear-attention layers keep a fixed-size state instead. The embedding tier is PENDING,
so it has no KV figure.

## Proposed `models.tiers` rows (for the hardware planner)

`resident_mib` excludes the KV cache, as `docs/INSTALLER-HARDWARE.md` defines it. It is the
measured peak (anon + file_mapped + swap, sampled every second while serving) minus
`kv_mib_per_1k × context / 1024` at the measured context. The general and coder rows were
derived from the llama.cpp run and are **NOT EVIDENCE**; the shipped `models.tiers` uses the
mistral.rs serving figure for coder instead. The embedding tier is `pending=embedding`.

```
tier=general   variant=qwen3-14b-q4km-nothink  rank=1 resident_mib=14776 kv_mib_per_1k=160 ctx_min=16384 ctx_max=32768 lock_repo=Qwen/Qwen3-14B-GGUF
tier=coder     variant=qwen3-14b-q4km-nothink  rank=1 resident_mib=14776 kv_mib_per_1k=160 ctx_min=16384 ctx_max=32768 lock_repo=Qwen/Qwen3-14B-GGUF
tier=vision    variant=qwen3-vl-2b-q4km        rank=1 resident_mib=7000  kv_mib_per_1k=112 ctx_min=8192  ctx_max=16384 lock_repo=Qwen/Qwen3-VL-2B-Instruct-GGUF
tier=stt       variant=voxtral-mini-4b-rt-q4k  rank=1 resident_mib=7270  kv_mib_per_1k=104 ctx_min=4096  ctx_max=8192  lock_repo=mistralai/Voxtral-Mini-4B-Realtime-2602
pending=embedding
```

Caveats the planner fields cannot express:

- **general and coder are one model.** Served from one process, they cost one
  `resident_mib`. Two rows as written make the planner count it twice.
- **Load peaks exceed steady state.** Voxtral-4B-Realtime needs 15.8 GiB while loading
  (safetensors read plus in-situ quantization) and 7.1 GiB after. mistral.rs 0.9.2 needed
  more than 22 GiB to load and serve Qwen3-14B at `--max-seq-len 16384` on this host (the
  16.9 GiB llama.cpp figure is NOT EVIDENCE).
- The vision and stt rows are least-bad picks that **fail** a hard gate (see below).

## Time spent per candidate (wall clock, including load; lock waits excluded where noted)

| Candidate | Engine | Wall | Outcome |
|---|---|---|---|
| nomic-embed-text-v1.5 Q8_0 | llama.cpp | 6 s | complete; NOT EVIDENCE |
| Qwen3-Embedding-0.6B Q8_0 | llama.cpp | 11 s | complete; NOT EVIDENCE |
| Voxtral-Mini-4B-Realtime-2602 | mistral.rs | 85 s | complete |
| Voxtral-Mini-3B-2507 Q4_K_M | mistral.rs | 6 s | load refused (NOT MEASURED) |
| Voxtral-Mini-3B-2507 Q4_K_M | llama.cpp | 128 s | complete; NOT EVIDENCE |
| Qwen3-VL-2B Q4_K_M | mistral.rs | 75 s | complete |
| Qwen3-14B Q4_K_M, stock / no-think | mistral.rs | 92 + 149 s / 98 + 223 s | OOM-killed, 4 attempts (NOT MEASURED) |
| Qwen3-14B Q4_K_M, stock | llama.cpp | 1,883 s + 64 s partial re-run | complete; NOT EVIDENCE |
| Qwen3-14B Q4_K_M, no-think | llama.cpp | 1,735 s (includes 129 s lock wait) | complete; NOT EVIDENCE |
| Qwen3.8-27B Q3_K_M | mistral.rs | 757 s | OOM-killed at the start of the 16k fill (NOT MEASURED except VIS-RSS) |
| Qwen3.8-27B Q4_K_M (lmstudio) | mistral.rs | 113 s | OOM-killed during load (NOT MEASURED) |
| Qwen3.8-27B UD-Q4_K_M (unsloth) | mistral.rs | 187 s (includes lock wait) | load refused: IQ4_XS tensors unsupported (NOT MEASURED) |
| Qwen3.8-27B Q3_K_M | llama.cpp | ~18 min, stopped | stopped during the 16k fill for time; about 4k of 15.6k tokens done at 6.5 tok/s prefill; NOT EVIDENCE |
| Qwen3.8-27B Q3_K_M, partial | llama.cpp | 307 s | GEN-NOTHINK, GEN-TPS, VIS-BBOX only; NOT EVIDENCE |

## Per tier

### general: Qwen3-14B Q4_K_M with server-side no-think (NOT MEASURED on mistral.rs)

- **Why this one.** It is the pinned coder model, and one resident model for two tiers is
  cheaper. Its general hard gates passed only on llama.cpp, which is **NOT EVIDENCE**: on
  mistral.rs the gates are NOT MEASURED (the 16k run was OOM-killed on this host), so the
  tier needs a mistral.rs run on a larger host before it can count as passing. Qwen3.8-27B
  was never fully measured either: under mistral.rs 0.9.2 every quantisation was OOM-killed
  or refused on this host. By the selection rule, an unmeasured candidate cannot be chosen.
- **EXTRAPOLATION (labelled, from the llama.cpp run, NOT EVIDENCE):** the 16k fill took
  1,246 s at 12.5 tok/s prefill on this host. A full 8192-token answer at 5.90 tok/s would
  take ≈1,388 s (23 min). mistral.rs figures must replace these.
- **Deployment change needed:** a no-think chat template server-side (`--jinja-explicit`),
  and `--max-seq-len` ≥16384 (32768 preferred for the tier's upper bound; KV 160 MiB per 1k
  tokens). Measure on mistral.rs 0.9.2 on a host with more than 25 GiB.
- **Model card:** Apache-2.0. 32,768 native context. 100+ languages. Thinking on by
  default, switchable. The stock template **fails** GEN-NOTHINK (583 chars of reasoning
  with no `/no_think`), and its reasoning ate the token budget of GEN-CTX, GEN-TOON and
  GEN-JSON (empty answers).
- **Rejected: Qwen3.8-27B** (`lmstudio-community/Qwen3.8-27B-GGUF` @ `5a7da681…` Q4_K_M
  sha256 `e00082f7…e520`; `bartowski/Qwen3.8-27B-GGUF` @ `0c92138c…` Q3_K_M sha256
  `14865aae…f869`; `unsloth/Qwen3.8-27B-GGUF` @ `4ca72078…` UD-Q4_K_M sha256 `322e194f…3482`).
  Apache-2.0, 262,144 native context, thinking on by default. What was measured: on
  mistral.rs, Q3_K_M reached **21.56 GiB** resident (including swap) before the OOM kill.
  (A partial llama.cpp run, NOT EVIDENCE, passed GEN-NOTHINK and GEN-TPS at 3.74 tok/s.)
  Everything else is NOT MEASURED. It needs a host with enough RAM for mistral.rs before it
  can be selected.

### vision: nothing passes every gate

| Candidate | VIS-BBOX (plain prompt) | with "absolute pixels" hint | MULTI | EDGE | TPS | RSS |
|---|---|---|---|---|---|---|
| Qwen3-VL-2B Q4_K_M, mistral.rs | **FAIL**: `[617,515,858,835]` vs truth `[640,300,880,480]`: pixel IoU 0.00, **0.94 as 0-1000** | FAIL, 0.93 as 0-1000 | PASS | PASS | 21.93 | PASS 6.97 GiB |
| Qwen3.8-27B Q3_K_M, llama.cpp (partial, **NOT EVIDENCE**) | **FAIL**: `[625,519,859,832]`: pixel IoU 0.00, **0.99 as 0-1000** | FAIL, 0.98 as 0-1000 | NOT MEASURED | NOT MEASURED | NOT MEASURED | 9.92 GiB (llama.cpp, mmap) |
| Qwen3.8-27B Q3_K_M, mistral.rs | NOT MEASURED (server OOM-killed) | — | — | — | — | **FAIL 21.56 GiB** |

- **Both model families place the box correctly, but in Qwen-VL's normalised 0-1000
  convention, not in pixels** (for Qwen3.8-27B only on llama.cpp, NOT EVIDENCE). Telling them to use absolute pixels does not change that. A
  pixel-coordinate model (the Qwen2.5-VL convention) was not on disk and was not measured.
- **Least-bad option: Qwen3-VL-2B.** It passes every other gate at a third of the 18 GiB
  budget. **Deployment change it needs:** callers must read `bbox_2d` as 0-1000 normalised
  and scale by `(width/1000, height/1000)` of the image they sent. Otherwise, put a
  pixel-convention model through this suite before selecting it.
- **Model card (Qwen3-VL-2B-Instruct):** Apache-2.0. 256K native context. OCR in 32
  languages. Instruct edition, no thinking trace.

### embedding: PENDING (no engine)

- **Status.** No engine: mistral.rs is the only engine, and the embedding model is being
  re-selected for one mistral.rs serves. The dimension must match the vector indexes (768).
  `models.lock` has no embedding row and `models.tiers` says `pending=embedding`, so the
  planner reports the tier as pending and budgets nothing for it. This file does not choose
  the replacement.
- **History, NOT EVIDENCE.** Both embedding runs of 2026-09-24 were served by llama.cpp,
  because mistral.rs 0.9.2 refuses every embedding GGUF ("Embedding models do not support
  GGUF or GGML format") and has no nomic-bert architecture. nomic-embed-text-v1.5 Q8_0
  (Apache-2.0, `language: en`, 768 dimensions) passed EMB-DIM, EMB-PREFIX and EMB-2048
  there; Qwen3-Embedding-0.6B Q8_0 (`Qwen/Qwen3-Embedding-0.6B-GGUF`, sha256
  `06507c7b…c439`) failed EMB-DIM with 1024-dim vectors. Neither result counts for the
  re-selection. The EMB-DIM gate stands: anything that is not exactly 768 cannot query the
  existing indexes without a full re-embed and a dimension migration.

### stt: nothing passes every gate

| Candidate | Engine | STT-AUDIO (`audio_url`) | WER en / es / fr / pt | Load | Resident | Time for 4 clips (~27 s audio) |
|---|---|---|---|---|---|---|
| Voxtral-Mini-4B-Realtime-2602, safetensors `--isq q4k` | mistral.rs 0.9.2 | **PASS** | 0.06 / 0.05 / **0.74** / 0.00 | 28 s | 7.10 GiB (15.80 GiB peak at load) | 39.6 s |
| Voxtral-Mini-3B-2507 Q4_K_M + mmproj Q8_0 (`ggml-org/Voxtral-Mini-3B-2507-GGUF` @ `20616573013f28229fff61de74359d3eeff61f6a`, sha256 `4705be8e…f1a8` / `4f24c4ef…0d02`) | mistral.rs 0.9.2 | **NOT MEASURED: does not load** (`multimodal GGUF architecture 'llama' is not supported by the native multimodal loader`) | — | — | — | — |
| same files | llama.cpp b4ca032 (**NOT EVIDENCE**) | **FAIL**: `audio_url` → HTTP 400 `unsupported content[].type` | 0.00 / 0.05 / 0.22 / 0.20 (measured over OpenAI `input_audio`) | 4 s | 5.64 GiB | 120.1 s |

- **Fixtures.** en is from piper (neural, en_GB voice). es/fr/pt are from espeak-ng
  (formant synthesis), so they are a pessimistic, synthetic bound. There is one short
  sentence per language (n=1). No natural-speech es/fr/pt fixtures exist yet, and none were
  faked. The 4B-Realtime fr failure is real on this fixture: it rendered the sentence partly
  in English (`"The Camio delivery fleet arrive at L'Hôtel-Boulin…"`). A natural-speech fr
  fixture is needed before treating that as a model property rather than a fixture artefact.
- **Least-bad option: Voxtral-Mini-4B-Realtime-2602.** It is the only candidate that serves
  the required `audio_url` protocol on the deployment engine, and it passes en/es/pt. The
  deployment change it needs: serve `--model-id <dir> --isq q4k` from the safetensors with a
  **≥16 GiB memory limit for load** (steady state 7.1 GiB), since the GGUF form does not
  load (see models.lock). It also needs a French natural-speech validation before fr is
  relied on. Voxtral-Mini-3B-2507 is unusable: the mistral.rs GGUF loader rejects it (its
  better fr/pt WER was measured on llama.cpp and is NOT EVIDENCE).
- **Model cards:** both Apache-2.0. The 4B-Realtime card lists 13 languages, including
  English, Spanish, French and Portuguese.

### coder: Qwen3-14B Q4_K_M, no-think template (NOT MEASURED on mistral.rs)

The two llama.cpp rows below are **NOT EVIDENCE**: they are the history of what was tried
while mistral.rs could not run the 16k gates on this host. The coder gates must be re-run on
mistral.rs on a larger host.

| Variant | GEN/COD-CTX | NOTHINK (general / coder) | STOP | JSON | TPS | Notes |
|---|---|---|---|---|---|---|
| stock template, llama.cpp (NOT EVIDENCE) | FAIL: empty answer; 32 tokens of budget spent in `reasoning_content` | general **FAIL** (583 chars of reasoning) / coder PASS (partial re-run) | PASS | PASS | 5.76 | TOON and JSON also FAIL: the thinking trace ate `max_tokens` |
| no-think template, llama.cpp (NOT EVIDENCE) | **PASS** (15,593 tokens, prefill 12.5 tok/s, 1,246 s fill) | PASS / PASS | PASS | PASS | **5.90** | also passes every general gate |
| stock and no-think, mistral.rs 0.9.2 `--max-seq-len 16384` | **NOT MEASURED** | — | — | — | — | OOM-killed on this host: load peaked at ~15.7 GiB anon plus ~20 GiB pushed to zram (exit 137, `OOMKilled=true`), with both `--max-seqs 2` and `--max-seqs 1` |

- **Deployment change the choice needs:** serve it with thinking disabled server-side (a chat
  template where the `enable_thinking is false` branch is always taken, i.e.
  `--jinja-explicit <nothink.jinja>` on mistral.rs), and pass `--max-seq-len 16384`, which no
  deployment passes today. Give the tier a memory limit above what this host could offer.
  mistral.rs 0.9.2 needed more than ~22 GiB (resident + swap) for this 9.0 GB GGUF at
  `--max-seq-len 16384` on this host.
- **Model card:** Apache-2.0. 32,768 native context (131,072 with YaRN). 100+ languages.
  `enable_thinking` switch plus `/think` and `/no_think` soft switches. With thinking on, the
  card notes the model always emits a `<think>` block.
- **Rejected alternatives:** GLM-4-9B-0414 was not on disk and was not measured. Qwen3.8-27B
  as coder is under general.

### tts: known gap (FAIL)

The speech service ships one English piper voice. It has no es/fr/pt voices, and it ignores
the request's `voice` field. These rows are FAIL from source evidence, not from a live
probe, and are kept in the suite so the tier can never read as green by omission.
