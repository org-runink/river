// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"image/color"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

const tpsFloorText = 2.8

// check is one requirement row's probe; late checks run after every tier's normal checks.
type check struct {
	id   string
	late bool
	fn   func() result
}

func (r *runner) checksFor(ctx context.Context, tier string) []check {
	switch tier {
	case "general":
		return []check{
			{"GEN-CTX", false, func() result { return r.ctxNeedle(ctx, "GEN-CTX") }},
			{"GEN-TOON", false, func() result { return r.toon(ctx) }},
			{"GEN-JSON", false, func() result { return r.jsonOut(ctx) }},
			{"GEN-NOTHINK", false, func() result { return r.noThinkDefault(ctx) }},
			{"GEN-LANG", false, func() result { return r.languages(ctx) }},
			{"GEN-TPS", false, func() result { return r.decode(ctx, "GEN-TPS", tpsFloorText) }},
			{"GEN-OUT8K", true, func() result { return r.out8k(ctx) }},
		}
	case "coder":
		return []check{
			{"COD-CTX", false, func() result { return r.ctxNeedle(ctx, "COD-CTX") }},
			{"COD-NOTHINK", false, func() result { return r.noThinkSwitch(ctx) }},
			{"COD-STOP", false, func() result { return r.stopSeqs(ctx) }},
			{"COD-JSON", false, func() result { return r.toolJSON(ctx) }},
			{"COD-TPS", false, func() result { return r.decode(ctx, "COD-TPS", tpsFloorText) }},
		}
	case "vision":
		return []check{
			{"VIS-BBOX", false, func() result { return r.bbox(ctx) }},
			{"VIS-MULTI", false, func() result { return r.multiImage(ctx) }},
			{"VIS-EDGE", false, func() result { return r.maxEdge(ctx) }},
			{"VIS-TPS", false, func() result { return r.visionDecode(ctx) }},
			{"VIS-RSS", false, func() result {
				return result{ID: "VIS-RSS", Status: "SKIP", Detail: "resolved at end of run from cgroup samples"}
			}},
		}
	case "stt":
		cs := []check{{"STT-AUDIO", false, func() result { return r.sttAll(ctx) }}}
		for _, l := range []string{"en", "es", "fr", "pt"} {
			cs = append(cs, check{"STT-WER-" + strings.ToUpper(l), false, func() result { return r.sttWER(l) }})
		}
		return cs
	case "embedding":
		return []check{
			{"EMB-DIM", false, func() result { return r.embDim(ctx) }},
			{"EMB-PREFIX", false, func() result { return r.embPrefix(ctx) }},
			{"EMB-2048", false, func() result { return r.emb2048(ctx) }},
		}
	case "tts":
		return []check{{"TTS-LANG", false, r.ttsKnown("TTS-LANG")}, {"TTS-VOICE", false, r.ttsKnown("TTS-VOICE")}}
	}
	return []check{{"UNKNOWN-" + tier, false, func() result {
		return result{ID: "UNKNOWN-" + tier, Status: "FAIL", Detail: "unknown tier " + tier}
	}}}
}

// fail records a failed check. A transport error (the server is gone: refused, reset, EOF)
// says nothing about the model, so it is ERROR = NOT MEASURED, never a capability FAIL.
func fail(id string, err error, raw ...string) result {
	st := "FAIL"
	if transportErr(err) {
		st = "ERROR"
	}
	return result{ID: id, Status: st, Detail: err.Error(), Evidence: raw}
}

func transportErr(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	for _, m := range []string{"connection refused", "connection reset", ": EOF", "broken pipe", "no such host", "use of closed network connection"} {
		if strings.Contains(s, m) {
			return true
		}
	}
	return false
}

func pf(ok bool) string {
	if ok {
		return "PASS"
	}
	return "FAIL"
}

func user(s string) message   { return message{Role: "user", Content: s} }
func system(s string) message { return message{Role: "system", Content: s} }

// reasoningLeak reports reasoning that reached the caller: text inside <think>, an
// unclosed <think>, or a non-empty reasoning_content field.
func reasoningLeak(content, reasoning string) (bool, string) {
	if strings.TrimSpace(reasoning) != "" {
		return true, fmt.Sprintf("reasoning_content has %d chars", len(reasoning))
	}
	if found, inner := thinkText(content); found && inner != "" {
		return true, fmt.Sprintf("<think> block with %d chars in content", len(inner))
	}
	return false, ""
}

// ── general / coder: long context ─────────────────────────────────────────────────────

func (r *runner) ctxNeedle(ctx context.Context, id string) result {
	if v, ok := r.memo["ctx"]; ok { // one fill serves both tiers on the same server
		res := v.(result)
		res.ID = id
		res.Detail = "same server, same fill as the first context check. " + res.Detail
		return res
	}
	// Calibrate chars/token on this tokenizer with a ~2k-token probe.
	probe := haystack(8000, "", 2)
	cal := r.c.chat(ctx, "ctx-calibrate", chatReq{Messages: []message{user(probe + "\nReply OK.")}, MaxTokens: 1})
	if cal.Err != nil || cal.Usage.PromptTokens == 0 {
		return fail(id, fmt.Errorf("calibration failed: %v (prompt_tokens=%d)", cal.Err, cal.Usage.PromptTokens), cal.RawFile)
	}
	cpt := float64(len(probe)) / float64(cal.Usage.PromptTokens)
	prefillCal := float64(cal.Usage.PromptTokens) / cal.Wall.Seconds()
	target := r.ctxLen - 600 // leave room for the question, template and a short answer
	question := "\n\nQuestion: according to the records above, what is the maintenance passphrase for the River node? Answer with the passphrase only."
	nChars := int(float64(target) * cpt * 0.985)
	var o chatOut
	for attempt := 0; attempt < 3; attempt++ {
		hs := haystack(nChars, needleText, 0.62)
		cctx, cancel := context.WithTimeout(ctx, r.ctxTO)
		o = r.c.chat(cctx, "ctx-fill", chatReq{Messages: []message{
			system("You answer questions about the provided records. Be exact."),
			user("Records:\n" + hs + question)}, MaxTokens: 32})
		cancel()
		if o.Err != nil && errors.Is(o.Err, context.DeadlineExceeded) {
			est := float64(target) / prefillCal
			res := result{ID: id, Status: "CAPPED", Evidence: []string{cal.RawFile, o.RawFile},
				Measured: map[string]any{"cap_seconds": r.ctxTO.Seconds(), "prefill_tok_s_calibration": round2(prefillCal),
					"extrapolated_fill_seconds": math.Round(est)},
				Detail: fmt.Sprintf("fill of ~%d tokens exceeded the %s cap; EXTRAPOLATION from the 2k-token calibration prefill rate: ~%.0f s", target, r.ctxTO, est)}
			r.memo["ctx"] = res
			return res
		}
		if o.HTTPCode >= 400 && o.HTTPCode < 500 { // over the window: shrink and retry
			nChars = int(float64(nChars) * 0.95)
			continue
		}
		break
	}
	if o.Err != nil {
		res := fail(id, o.Err, cal.RawFile, o.RawFile)
		r.memo["ctx"] = res
		return res
	}
	found := strings.Contains(strings.ToUpper(o.Content), needleAnswer)
	minTok := r.ctxLen - 900
	ok := found && o.Usage.PromptTokens >= minTok
	leak, why := reasoningLeak(o.Content, o.Reasoning)
	res := result{ID: id, Status: pf(ok), Evidence: []string{cal.RawFile, o.RawFile},
		Measured: map[string]any{"prompt_tokens": o.Usage.PromptTokens, "min_prompt_tokens": minTok, "needle_found": found,
			"fill_seconds": round2(o.Wall.Seconds()), "prefill_tok_s": round2(float64(o.Usage.PromptTokens) / o.Wall.Seconds()),
			"answer": truncate(strings.TrimSpace(o.Content), 80)}}
	if leak {
		res.Detail = "note: " + why
	}
	r.memo["ctx"] = res
	return res
}

// ── general: structured output ────────────────────────────────────────────────────────

func (r *runner) toon(ctx context.Context) result {
	prompt := "Convert the data below to TOON (Token-Oriented Object Notation), tabular-array form. " +
		"Example of the format:\nusers[2]{id,name}:\n  1,Ana\n  2,Luis\n\n" +
		"Data: shipments — id 101, city Porto, weight 12; id 102, city Lyon, weight 7; id 103, city Recife, weight 30.\n" +
		"Use the array name shipments and the fields id,city,weight. Output only the TOON, no prose, no code fences."
	o := r.c.chat(ctx, "toon", chatReq{Messages: []message{user(prompt)}, Temperature: 0, MaxTokens: 256})
	if o.Err != nil {
		return fail("GEN-TOON", o.Err, o.RawFile)
	}
	body, fenced := stripFences(o.Content)
	t, err := parseTOON(body)
	m := map[string]any{"fenced": fenced, "reply": truncate(o.Content, 200)}
	if err != nil {
		return result{ID: "GEN-TOON", Status: "FAIL", Measured: m, Detail: err.Error(), Evidence: []string{o.RawFile}}
	}
	want := [][]string{{"101", "Porto", "12"}, {"102", "Lyon", "7"}, {"103", "Recife", "30"}}
	ok := t.Name == "shipments" && strings.Join(t.Fields, ",") == "id,city,weight" && fmt.Sprint(t.Rows) == fmt.Sprint(want)
	res := result{ID: "GEN-TOON", Status: pf(ok), Measured: m, Evidence: []string{o.RawFile}}
	if !ok {
		res.Detail = fmt.Sprintf("parsed but wrong content: name=%s fields=%v rows=%v", t.Name, t.Fields, t.Rows)
	} else if fenced {
		res.Detail = "parsed only after stripping a code fence"
	}
	return res
}

func (r *runner) jsonOut(ctx context.Context) result {
	prompt := `Extract the shipment into a JSON object with keys "city" (string), "pallets" (integer) and "urgent" (boolean). ` +
		"Text: 12 pallets must reach Porto today, it is urgent. Output only the JSON object."
	o := r.c.chat(ctx, "json", chatReq{Messages: []message{user(prompt)}, Temperature: 0, MaxTokens: 128})
	if o.Err != nil {
		return fail("GEN-JSON", o.Err, o.RawFile)
	}
	body, fenced := stripFences(o.Content)
	var v struct {
		City    string `json:"city"`
		Pallets int    `json:"pallets"`
		Urgent  bool   `json:"urgent"`
	}
	strictErr := json.Unmarshal([]byte(strings.TrimSpace(o.Content)), &v)
	err := json.Unmarshal([]byte(body), &v)
	m := map[string]any{"strict_parse": strictErr == nil, "fenced": fenced, "reply": truncate(o.Content, 160),
		"server_decode_tok_s": round2(o.Usage.AvgComplTokPerSec)}
	if err != nil {
		return result{ID: "GEN-JSON", Status: "FAIL", Measured: m, Detail: err.Error(), Evidence: []string{o.RawFile}}
	}
	ok := v.City == "Porto" && v.Pallets == 12 && v.Urgent
	res := result{ID: "GEN-JSON", Status: pf(ok), Measured: m, Evidence: []string{o.RawFile}}
	if !ok {
		res.Detail = fmt.Sprintf("wrong values: %+v", v)
	} else if fenced {
		res.Detail = "parsed only after stripping a code fence"
	}
	return res
}

// ── thinking control ──────────────────────────────────────────────────────────────────

func (r *runner) noThinkDefault(ctx context.Context) result {
	// No /no_think anywhere: if the model reasons, the server must be what disables it.
	o := r.c.chat(ctx, "nothink-default", chatReq{Messages: []message{
		system("You are a concise assistant."),
		user("A truck carries 17 crates of 23 kg each. What is the total mass in kg? Answer with the number only.")},
		Temperature: 0, MaxTokens: 768})
	if o.Err != nil {
		return fail("GEN-NOTHINK", o.Err, o.RawFile)
	}
	leak, why := reasoningLeak(o.Content, o.Reasoning)
	tags := strings.Contains(o.Content, "<think>") || strings.Contains(o.Content, "</think>")
	m := map[string]any{"completion_tokens": o.Usage.CompletionTokens, "reply": truncate(o.Content, 160),
		"think_tags_in_content": tags, "correct": strings.Contains(o.Content, "391")}
	res := result{ID: "GEN-NOTHINK", Status: pf(!leak), Measured: m, Evidence: []string{o.RawFile}, Detail: why}
	if !leak && tags {
		res.Detail = "empty <think></think> tags reach the caller (no reasoning text)"
	}
	return res
}

func (r *runner) noThinkSwitch(ctx context.Context) result {
	o := r.c.chat(ctx, "nothink-switch", chatReq{Messages: []message{
		system("You are a coding agent."),
		user("Write a Go function Max(a, b int) int. Code only. /no_think")},
		Temperature: 0, MaxTokens: 768})
	if o.Err != nil {
		return fail("COD-NOTHINK", o.Err, o.RawFile)
	}
	leak, why := reasoningLeak(o.Content, o.Reasoning)
	m := map[string]any{"completion_tokens": o.Usage.CompletionTokens, "has_func": strings.Contains(o.Content, "func Max"),
		"think_tags_in_content": strings.Contains(o.Content, "<think>")}
	return result{ID: "COD-NOTHINK", Status: pf(!leak && strings.Contains(o.Content, "func Max")), Measured: m, Detail: why, Evidence: []string{o.RawFile}}
}

// ── general: languages ────────────────────────────────────────────────────────────────

func (r *runner) languages(ctx context.Context) result {
	cases := []struct{ lang, want, q string }{
		{"es", "es", "Explica en tres frases por qué es importante mantener la cadena de frío en el transporte de alimentos."},
		{"fr", "fr", "Explique en trois phrases pourquoi il est important de maintenir la chaîne du froid dans le transport des aliments."},
		{"pt", "pt", "Explique em três frases por que é importante manter a cadeia de frio no transporte de alimentos."},
		{"pt-BR", "pt", "Você pode me explicar em três frases por que é tão importante manter a cadeia de frio no transporte de alimentos?"},
	}
	m := map[string]any{}
	var bad []string
	var raws []string
	for _, c := range cases {
		o := r.c.chat(ctx, "lang-"+c.lang, chatReq{Messages: []message{system("You are a helpful assistant."), user(c.q)},
			Temperature: 0, MaxTokens: 256})
		raws = append(raws, o.RawFile)
		if transportErr(o.Err) {
			return fail("GEN-LANG", o.Err, raws...)
		}
		if o.Err != nil {
			bad = append(bad, c.lang+": "+o.Err.Error())
			continue
		}
		got, sc := detectLang(o.Content)
		m[c.lang] = map[string]any{"detected": got, "scores": sc, "reply": truncate(o.Content, 120)}
		if got != c.want {
			bad = append(bad, fmt.Sprintf("%s answered in %s", c.lang, got))
		}
		if leak, why := reasoningLeak(o.Content, o.Reasoning); leak {
			bad = append(bad, c.lang+": "+why)
		}
	}
	return result{ID: "GEN-LANG", Status: pf(len(bad) == 0), Measured: m, Detail: strings.Join(bad, "; "), Evidence: raws}
}

// ── decode speed ──────────────────────────────────────────────────────────────────────

func (r *runner) decode(ctx context.Context, id string, floor float64) result {
	if v, ok := r.memo["decode"]; ok {
		res := v.(result)
		res.ID = id
		res.Status = pf(res.Measured["decode_tok_s"].(float64) >= floor)
		return res
	}
	o := r.c.stream(ctx, "decode", chatReq{Messages: []message{
		user("Write a detailed, plain-prose description of how a river delta forms. At least 300 words. /no_think")},
		Temperature: 0, MaxTokens: 200}, 0)
	if o.Err != nil {
		return fail(id, o.Err, o.RawFile)
	}
	rate := round2(o.DecodeRate)
	res := result{ID: id, Status: pf(rate >= floor), Evidence: []string{o.RawFile},
		Measured: map[string]any{"decode_tok_s": rate, "floor": floor, "tokens": o.Chunks, "ttft_s": round2(o.TTFT.Seconds())},
		Detail:   "client-side: SSE content chunks between first and last chunk (prefill excluded)"}
	r.memo["decode"] = res
	return res
}

func (r *runner) out8k(ctx context.Context) result {
	const n = 48
	o := r.c.stream(ctx, "out8k", chatReq{Messages: []message{
		user("Write an exhaustive, very long technical manual (at least 6000 words) on operating a cold-storage warehouse.")},
		Temperature: 0, MaxTokens: 8192}, n)
	if o.Err != nil {
		return fail("GEN-OUT8K", o.Err, o.RawFile)
	}
	rate := 0.0
	if d, ok := r.memo["decode"]; ok {
		rate = d.(result).Measured["decode_tok_s"].(float64)
	}
	if rate == 0 {
		rate = o.DecodeRate
	}
	m := map[string]any{"admitted": o.HTTPCode == 200, "streamed_tokens_before_cancel": o.Chunks}
	detail := fmt.Sprintf("capped: stream cancelled after %d tokens by design", o.Chunks)
	if rate > 0 {
		est := 8192 / rate
		m["extrapolated_8192_token_seconds"] = math.Round(est)
		detail += fmt.Sprintf("; EXTRAPOLATION: a full 8192-token answer at %.2f tok/s takes ~%.0f s (~%.0f min)", rate, est, est/60)
	}
	return result{ID: "GEN-OUT8K", Status: pf(o.HTTPCode == 200 && o.Chunks >= n), Measured: m, Detail: detail, Evidence: []string{o.RawFile}}
}

// ── coder ─────────────────────────────────────────────────────────────────────────────

func (r *runner) stopSeqs(ctx context.Context) result {
	sys := "You are a coding agent that solves tasks with tools. Always use exactly this format:\n" +
		"Thought: <your reasoning>\nAction: <tool name>\nAction Input: <JSON arguments>\nObservation: <tool result>\n" +
		"... (repeat Thought/Action/Action Input/Observation as needed)\nFinal Answer: <answer>\n\n" +
		"Tools:\n- read_file: {\"path\": string} returns the file contents\n- list_dir: {\"path\": string}"
	o := r.c.chat(ctx, "stop", chatReq{Messages: []message{system(sys), user("What does cmd/main.go contain? /no_think")},
		Temperature: 0, MaxTokens: 256, Stop: []string{"\nObservation:", "\n<|im_end|>"}})
	if o.Err != nil {
		return fail("COD-STOP", o.Err, o.RawFile)
	}
	hasAction := strings.Contains(o.Content, "Action:")
	leaked := strings.Contains(o.Content, "Observation:") || strings.Contains(o.Content, "<|im_end|>")
	m := map[string]any{"finish_reason": o.Finish, "reached_action": hasAction, "text_after_stop": leaked,
		"reply": truncate(o.Content, 240)}
	ok := hasAction && !leaked
	d := ""
	if !hasAction {
		d = "inconclusive: the model never produced an Action, so the stop point was not reached"
	}
	return result{ID: "COD-STOP", Status: pf(ok), Measured: m, Detail: d, Evidence: []string{o.RawFile}}
}

func (r *runner) toolJSON(ctx context.Context) result {
	o := r.c.chat(ctx, "tooljson", chatReq{Messages: []message{
		system(`You call tools by replying with exactly one JSON object {"tool": "<name>", "args": {...}} and nothing else.`),
		user(`Call the read_file tool on the path cmd/main.go. /no_think`)},
		Temperature: 0, MaxTokens: 128})
	if o.Err != nil {
		return fail("COD-JSON", o.Err, o.RawFile)
	}
	body, fenced := stripFences(o.Content)
	if i := strings.Index(body, "{"); i > 0 { // tolerate an empty think block before the object
		body = body[i:]
	}
	var v struct {
		Tool string         `json:"tool"`
		Args map[string]any `json:"args"`
	}
	err := json.Unmarshal([]byte(body), &v)
	m := map[string]any{"fenced": fenced, "reply": truncate(o.Content, 160)}
	if err != nil {
		return result{ID: "COD-JSON", Status: "FAIL", Measured: m, Detail: err.Error(), Evidence: []string{o.RawFile}}
	}
	ok := v.Tool == "read_file" && v.Args["path"] == "cmd/main.go"
	return result{ID: "COD-JSON", Status: pf(ok), Measured: m, Evidence: []string{o.RawFile}}
}

// ── vision ────────────────────────────────────────────────────────────────────────────

var numRe = regexp.MustCompile(`-?\d+(\.\d+)?`)

// firstBBox finds the first "bbox_2d": [a, b, c, d] in a reply.
func firstBBox(s string) (box, bool) {
	i := strings.Index(s, "bbox_2d")
	if i < 0 {
		return box{}, false
	}
	j := strings.Index(s[i:], "[")
	k := strings.Index(s[i:], "]")
	if j < 0 || k < j {
		return box{}, false
	}
	ns := numRe.FindAllString(s[i+j:i+k], -1)
	if len(ns) != 4 {
		return box{}, false
	}
	var f [4]float64
	for x := range ns {
		fmt.Sscan(ns[x], &f[x])
	}
	return box{f[0], f[1], f[2], f[3]}, true
}

func (r *runner) saveFixture(name string, b []byte) {
	_ = os.WriteFile(filepath.Join(r.outDir, "raw", name), b, 0o644)
}

func (r *runner) bbox(ctx context.Context) result {
	img := encJPEG(scene(visW, visH, "rect", color.RGBA{220, 30, 30, 255}, redTruth, true))
	r.saveFixture("fixture-bbox.jpg", img)
	prompt := `Locate the red rectangle in the image. Output JSON only, in the form [{"bbox_2d": [x1, y1, x2, y2], "label": "red rectangle"}].`
	m := map[string]any{"truth_px": redTruth, "image": fmt.Sprintf("%dx%d JPEG %d bytes", visW, visH, len(img))}
	var raws []string
	var verdicts []string
	status := "PASS"
	for _, v := range []struct{ tag, p string }{{"plain", prompt},
		{"pixel-hint", prompt + fmt.Sprintf(" Coordinates are absolute pixels of this %dx%d image.", visW, visH)}} {
		o := r.c.chat(ctx, "bbox-"+v.tag, chatReq{Messages: []message{{Role: "user", Content: []part{
			{"type": "image_url", "image_url": map[string]any{"url": dataURI("image/jpeg", img)}},
			{"type": "text", "text": v.p}}}}, Temperature: 0, MaxTokens: 256})
		raws = append(raws, o.RawFile)
		if o.Err != nil {
			return fail("VIS-BBOX", o.Err, raws...)
		}
		b, ok := firstBBox(o.Content)
		if !ok {
			m[v.tag] = map[string]any{"reply": truncate(o.Content, 200), "bbox": nil}
			verdicts = append(verdicts, v.tag+": no bbox_2d in reply")
			if v.tag == "plain" {
				status = "FAIL"
			}
			continue
		}
		pix := iou(b, redTruth)
		norm := iou(box{b.X1 * float64(visW) / 1000, b.Y1 * float64(visH) / 1000, b.X2 * float64(visW) / 1000, b.Y2 * float64(visH) / 1000}, redTruth)
		looksNorm := norm >= 0.5 && pix < 0.5
		m[v.tag] = map[string]any{"bbox": b, "iou_pixel": round2(pix), "iou_if_normalised_0_1000": round2(norm), "looks_normalised": looksNorm}
		if v.tag == "plain" && pix < 0.5 {
			status = "FAIL"
		}
		switch {
		case looksNorm:
			verdicts = append(verdicts, fmt.Sprintf("%s: box is 0-1000 NORMALISED (IoU as pixels %.2f, as normalised %.2f)", v.tag, pix, norm))
		case pix < 0.5:
			verdicts = append(verdicts, fmt.Sprintf("%s: pixel IoU %.2f < 0.5", v.tag, pix))
		}
	}
	return result{ID: "VIS-BBOX", Status: status, Measured: m, Detail: strings.Join(verdicts, "; ") +
		" (gate = the plain prompt; pixel-hint is informational)", Evidence: raws}
}

func (r *runner) multiImage(ctx context.Context) result {
	a := encJPEG(scene(visW, visH, "rect", color.RGBA{220, 30, 30, 255}, redTruth, false))
	b := encPNG(scene(visW, visH, "disc", color.RGBA{30, 170, 60, 255}, box{300, 150, 540, 390}, false))
	r.saveFixture("fixture-multi-1.jpg", a)
	r.saveFixture("fixture-multi-2.png", b)
	o := r.c.chat(ctx, "multi", chatReq{Messages: []message{{Role: "user", Content: []part{
		{"type": "image_url", "image_url": map[string]any{"url": dataURI("image/jpeg", a)}},
		{"type": "image_url", "image_url": map[string]any{"url": dataURI("image/png", b)}},
		{"type": "text", "text": `Each of the two images contains one coloured shape. Reply JSON only: {"image1": "<colour> <shape>", "image2": "<colour> <shape>"}.`}}}},
		Temperature: 0, MaxTokens: 128})
	if o.Err != nil {
		return fail("VIS-MULTI", o.Err, o.RawFile)
	}
	body, _ := stripFences(o.Content)
	var v map[string]string
	err := json.Unmarshal([]byte(body), &v)
	i1, i2 := strings.ToLower(v["image1"]), strings.ToLower(v["image2"])
	ok := err == nil && strings.Contains(i1, "red") && strings.Contains(i2, "green")
	m := map[string]any{"reply": truncate(o.Content, 200), "sizes_bytes": []int{len(a), len(b)}, "prompt_tokens": o.Usage.PromptTokens}
	d := ""
	if err != nil {
		d = "reply is not JSON: " + err.Error()
		ok = strings.Contains(strings.ToLower(o.Content), "red") && strings.Contains(strings.ToLower(o.Content), "green") &&
			strings.Index(strings.ToLower(o.Content), "red") < strings.Index(strings.ToLower(o.Content), "green")
		if ok {
			d += " (colours correct and in order; JSON strictness is not what this row gates)"
		}
	}
	return result{ID: "VIS-MULTI", Status: pf(ok), Measured: m, Detail: d, Evidence: []string{o.RawFile}}
}

func (r *runner) maxEdge(ctx context.Context) result {
	scale := 1600.0 / float64(visW)
	big := encJPEG(scene(1600, 900, "rect", color.RGBA{220, 30, 30, 255},
		box{redTruth.X1 * scale, redTruth.Y1 * scale, redTruth.X2 * scale, redTruth.Y2 * scale}, true))
	r.saveFixture("fixture-1600.jpg", big)
	o := r.c.chat(ctx, "edge1600", chatReq{Messages: []message{{Role: "user", Content: []part{
		{"type": "image_url", "image_url": map[string]any{"url": dataURI("image/jpeg", big)}},
		{"type": "text", "text": "What colour is the rectangle? One word."}}}}, Temperature: 0, MaxTokens: 16})
	if o.Err != nil {
		return fail("VIS-EDGE", o.Err, o.RawFile)
	}
	ok := strings.Contains(strings.ToLower(o.Content), "red")
	return result{ID: "VIS-EDGE", Status: pf(ok), Evidence: []string{o.RawFile},
		Measured: map[string]any{"input": "1600x900 JPEG", "bytes": len(big), "prompt_tokens": o.Usage.PromptTokens, "reply": o.Content},
		Detail:   "1024-edge admission is exercised by VIS-BBOX; this row adds a 1600-edge input the server must resize"}
}

func (r *runner) visionDecode(ctx context.Context) result {
	img := encJPEG(scene(visW, visH, "rect", color.RGBA{220, 30, 30, 255}, redTruth, true))
	o := r.c.stream(ctx, "vision-decode", chatReq{Messages: []message{{Role: "user", Content: []part{
		{"type": "image_url", "image_url": map[string]any{"url": dataURI("image/jpeg", img)}},
		{"type": "text", "text": "Describe this image in detail, in at least 120 words."}}}}, Temperature: 0, MaxTokens: 128}, 0)
	if o.Err != nil {
		return fail("VIS-TPS", o.Err, o.RawFile)
	}
	rate := round2(o.DecodeRate)
	return result{ID: "VIS-TPS", Status: pf(rate >= 1.0), Evidence: []string{o.RawFile},
		Measured: map[string]any{"decode_tok_s": rate, "floor": 1.0, "tokens": o.Chunks, "ttft_s": round2(o.TTFT.Seconds())}}
}

func (r *runner) residentVerdict(res map[string]any) result {
	g, _ := res["resident_peak_gib"].(float64)
	return result{ID: "VIS-RSS", Tier: "vision", Status: pf(g > 0 && g <= 18), Measured: res,
		Requirement: reqByID("VIS-RSS").Text, Threshold: reqByID("VIS-RSS").Floor, Hard: true,
		Detail: "peak over the whole run (load, 16k fill if any, images)"}
}

// ── stt ───────────────────────────────────────────────────────────────────────────────

func checkWAV(b []byte) error {
	if len(b) < 44 || string(b[0:4]) != "RIFF" || string(b[8:12]) != "WAVE" {
		return fmt.Errorf("not a RIFF/WAVE file")
	}
	ch := binary.LittleEndian.Uint16(b[22:24])
	rate := binary.LittleEndian.Uint32(b[24:28])
	bits := binary.LittleEndian.Uint16(b[34:36])
	fmtTag := binary.LittleEndian.Uint16(b[20:22])
	if fmtTag != 1 || ch != 1 || rate != 16000 || bits != 16 {
		return fmt.Errorf("want PCM 16 kHz mono s16le, got fmt=%d ch=%d rate=%d bits=%d", fmtTag, ch, rate, bits)
	}
	return nil
}

const sttPrompt = "Transcribe this audio verbatim in its original language. Output only the transcript."

// sttRes is one fixture's transcription. The gate protocol is chat `audio_url`; when a
// server rejects it, the same audio is retried as OpenAI `input_audio` so the model's
// accuracy is still measured, but STT-AUDIO stays FAIL.
type sttRes struct {
	hyp, raw, proto string
	audioURLErr     string
	secs            float64
	err             error
}

func (r *runner) sttOne(ctx context.Context, lang string) sttRes {
	if v, ok := r.memo["stt-"+lang]; ok {
		return v.(sttRes)
	}
	res := r.sttDo(ctx, lang)
	r.memo["stt-"+lang] = res
	return res
}

func (r *runner) sttDo(ctx context.Context, lang string) sttRes {
	wav, err := os.ReadFile(filepath.Join(r.audioDir, lang+".wav"))
	if err != nil {
		return sttRes{err: fmt.Errorf("fixture MISSING: %v; not faked", err)}
	}
	if err := checkWAV(wav); err != nil {
		return sttRes{err: fmt.Errorf("fixture %s.wav: %v", lang, err)}
	}
	o := r.c.chat(ctx, "stt-"+lang, chatReq{Messages: []message{{Role: "user", Content: []part{
		{"type": "audio_url", "audio_url": map[string]any{"url": dataURI("audio/wav", wav)}},
		{"type": "text", "text": sttPrompt}}}}, Temperature: 0, MaxTokens: 256})
	if o.Err == nil && strings.TrimSpace(o.Content) != "" {
		return sttRes{hyp: strings.TrimSpace(o.Content), raw: o.RawFile, proto: "audio_url", secs: round2(o.Wall.Seconds())}
	}
	first := "empty transcript"
	if o.Err != nil {
		first = o.Err.Error()
	}
	o2 := r.c.chat(ctx, "stt-"+lang+"-input_audio", chatReq{Messages: []message{{Role: "user", Content: []part{
		{"type": "input_audio", "input_audio": map[string]any{"data": base64.StdEncoding.EncodeToString(wav), "format": "wav"}},
		{"type": "text", "text": sttPrompt}}}}, Temperature: 0, MaxTokens: 256})
	if o2.Err != nil {
		return sttRes{raw: o.RawFile, audioURLErr: first, err: fmt.Errorf("audio_url: %s; input_audio: %v", first, o2.Err)}
	}
	return sttRes{hyp: strings.TrimSpace(o2.Content), raw: o2.RawFile, proto: "input_audio", audioURLErr: first, secs: round2(o2.Wall.Seconds())}
}

func (r *runner) sttAll(ctx context.Context) result {
	m := map[string]any{}
	var errs, raws []string
	okURL := 0
	for _, l := range []string{"en", "es", "fr", "pt"} {
		s := r.sttOne(ctx, l)
		if s.raw != "" {
			raws = append(raws, s.raw)
		}
		switch {
		case s.err != nil:
			errs = append(errs, l+": "+s.err.Error())
		case s.proto != "audio_url":
			errs = append(errs, l+": audio_url REJECTED ("+truncate(s.audioURLErr, 160)+"); transcribed only via input_audio")
			m[l] = map[string]any{"protocol": s.proto, "seconds": s.secs, "transcript": truncate(s.hyp, 160)}
		default:
			okURL++
			m[l] = map[string]any{"protocol": s.proto, "seconds": s.secs, "transcript": truncate(s.hyp, 160)}
		}
	}
	return result{ID: "STT-AUDIO", Status: pf(okURL > 0 && len(errs) == 0),
		Measured: m, Detail: strings.Join(errs, "; "), Evidence: raws}
}

func (r *runner) sttWER(lang string) result {
	id := "STT-WER-" + strings.ToUpper(lang)
	ref, err := os.ReadFile(filepath.Join(r.audioDir, lang+".txt"))
	if err != nil {
		return result{ID: id, Status: "FAIL", Detail: "fixture MISSING: no " + lang + ".txt/.wav; not faked"}
	}
	src, _ := os.ReadFile(filepath.Join(r.audioDir, lang+".src"))
	s := r.sttOne(context.Background(), lang)
	if s.err != nil {
		return fail(id, s.err, s.raw)
	}
	w, e, n := wer(string(ref), s.hyp)
	floor := 0.30
	if lang == "en" {
		floor = 0.15
	}
	res := result{ID: id, Status: pf(w <= floor), Evidence: []string{s.raw},
		Measured: map[string]any{"wer": round2(w), "errors": e, "ref_words": n, "floor": floor, "hypothesis": s.hyp,
			"protocol": s.proto, "fixture_source": strings.TrimSpace(string(src))}}
	if s.proto != "audio_url" {
		res.Detail = "measured over input_audio because this server rejects audio_url (see STT-AUDIO)"
	}
	return res
}

// ── embedding ─────────────────────────────────────────────────────────────────────────

func cosine(a, b []float64) float64 {
	var d, na, nb float64
	for i := range a {
		d += a[i] * b[i]
		na += a[i] * a[i]
		nb += b[i] * b[i]
	}
	return d / math.Sqrt(na*nb)
}

func (r *runner) embDim(ctx context.Context) result {
	v, _, err := r.c.embed(ctx, "emb-dim", []string{"search_query: how many pallets reached Porto"})
	if err != nil {
		return fail("EMB-DIM", err)
	}
	return result{ID: "EMB-DIM", Status: pf(len(v) == 1 && len(v[0]) == 768), Measured: map[string]any{"dim": len(v[0])}}
}

func (r *runner) embPrefix(ctx context.Context) result {
	docs := []string{
		"The cold room must stay between 2 and 4 degrees Celsius; log the temperature every hour.",
		"Forklift batteries are charged overnight in bay 3 and must never be charged in the aisle.",
		"Invoices are issued on the first business day of each month and are payable within 30 days.",
		"Passwords are reset from the account page by requesting a one-time code by e-mail.",
	}
	qs := []struct {
		q    string
		want int
	}{
		{"what temperature should the cold room be", 0},
		{"where do I charge the forklift", 1},
		{"when is payment due on an invoice", 2},
		{"I forgot my password", 3},
	}
	in := make([]string, 0, len(docs)+len(qs))
	for _, d := range docs {
		in = append(in, "search_document: "+d)
	}
	for _, q := range qs {
		in = append(in, "search_query: "+q.q)
	}
	v, _, err := r.c.embed(ctx, "emb-prefix", in)
	if err != nil {
		return fail("EMB-PREFIX", err)
	}
	var bad []string
	margins := []float64{}
	for i, q := range qs {
		qv := v[len(docs)+i]
		type sc struct {
			i int
			s float64
		}
		var s []sc
		for j := range docs {
			s = append(s, sc{j, cosine(qv, v[j])})
		}
		sort.Slice(s, func(a, b int) bool { return s[a].s > s[b].s })
		margins = append(margins, round2(s[0].s-s[1].s))
		if s[0].i != q.want {
			bad = append(bad, fmt.Sprintf("%q ranked doc %d first", q.q, s[0].i))
		}
	}
	return result{ID: "EMB-PREFIX", Status: pf(len(bad) == 0), Measured: map[string]any{"top1_margins": margins},
		Detail: strings.Join(bad, "; ")}
}

func (r *runner) emb2048(ctx context.Context) result {
	probe := "search_document: " + haystack(2000, "", 2)
	_, pt, err := r.c.embed(ctx, "emb-cal", []string{probe})
	if err != nil {
		return fail("EMB-2048", fmt.Errorf("calibration: %v", err))
	}
	target := 1990
	n := 6000
	if pt > 0 {
		n = int(float64(len(probe)) / float64(pt) * float64(target))
	}
	text := "search_document: " + haystack(n, "", 2)
	_, pt2, err := r.c.embed(ctx, "emb-2048", []string{text})
	m := map[string]any{"chars": len(text), "prompt_tokens_reported": pt2}
	if err != nil {
		return result{ID: "EMB-2048", Status: "FAIL", Measured: m, Detail: err.Error()}
	}
	ok := pt2 == 0 || pt2 >= 1900
	d := ""
	if pt2 == 0 {
		d = "server reported no token count; size estimated from calibration"
	}
	return result{ID: "EMB-2048", Status: pf(ok), Measured: m, Detail: d}
}

// ── tts ───────────────────────────────────────────────────────────────────────────────

// The speech service is not a model candidate: these rows record the known gap so the
// tier cannot silently read as green. They are FAIL by source evidence, not a live probe.
func (r *runner) ttsKnown(id string) func() result {
	return func() result {
		switch id {
		case "TTS-LANG":
			return result{ID: id, Status: "FAIL", Measured: map[string]any{"voices": []string{"en"}, "missing": []string{"es", "fr", "pt"}},
				Detail: "KNOWN GAP (source evidence, not a live probe): the shipped TTS service carries one English piper voice; es/fr/pt voices are missing"}
		default:
			return result{ID: id, Status: "FAIL", Detail: "KNOWN GAP (source evidence, not a live probe): the TTS service ignores the request's voice field and always uses its single configured voice"}
		}
	}
}
