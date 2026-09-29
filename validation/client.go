// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"
)

// client talks to one OpenAI-compatible endpoint (mistral.rs `serve`, the only engine).
// Every request and response is written verbatim to rawDir, so a verdict can always be
// re-derived from the raw evidence.
type client struct {
	base   string // e.g. http://127.0.0.1:18090/v1
	model  string
	rawDir string
	http   *http.Client
	seq    atomic.Int64
}

type part map[string]any

type message struct {
	Role    string `json:"role"`
	Content any    `json:"content"` // string or []part
}

type chatReq struct {
	Model       string    `json:"model"`
	Messages    []message `json:"messages"`
	Temperature float64   `json:"temperature"`
	MaxTokens   int       `json:"max_tokens,omitempty"`
	Stop        []string  `json:"stop,omitempty"`
	Stream      bool      `json:"stream,omitempty"`
}

type usage struct {
	PromptTokens          int     `json:"prompt_tokens"`
	CompletionTokens      int     `json:"completion_tokens"`
	AvgPromptTokPerSec    float64 `json:"avg_prompt_tok_per_sec"`
	AvgComplTokPerSec     float64 `json:"avg_compl_tok_per_sec"`
	TotalPromptTimeSec    float64 `json:"total_prompt_time_sec"`
	TotalCompletionTimeSe float64 `json:"total_completion_time_sec"`
}

type chatResp struct {
	Choices []struct {
		Message struct {
			Content          string `json:"content"`
			ReasoningContent string `json:"reasoning_content"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage usage `json:"usage"`
}

// result of one chat call, with wall-clock timing.
type chatOut struct {
	Content   string
	Reasoning string
	Finish    string
	Usage     usage
	Wall      time.Duration
	HTTPCode  int
	Err       error
	RawFile   string
}

func newClient(base, model, rawDir string, timeout time.Duration) *client {
	return &client{base: strings.TrimRight(base, "/"), model: model, rawDir: rawDir,
		// No keep-alive: a server that closes idle connections makes a POST on a reused,
		// half-closed connection fail with EOF before the server sees it.
		http: &http.Client{Timeout: timeout, Transport: &http.Transport{DisableKeepAlives: true}}}
}

func (c *client) save(tag string, req, resp []byte) string {
	n := c.seq.Add(1)
	stem := filepath.Join(c.rawDir, fmt.Sprintf("%03d-%s", n, tag))
	// Requests can carry multi-MB base64 media; keep them, they are the evidence.
	_ = os.WriteFile(stem+".req.json", req, 0o644)
	_ = os.WriteFile(stem+".resp.json", resp, 0o644)
	return filepath.Base(stem)
}

func (c *client) post(ctx context.Context, path string, body any) ([]byte, int, time.Duration, error) {
	b, err := json.Marshal(body)
	if err != nil {
		return nil, 0, 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+path, bytes.NewReader(b))
	if err != nil {
		return nil, 0, 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	t0 := time.Now()
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, 0, time.Since(t0), err
	}
	defer resp.Body.Close()
	out, err := io.ReadAll(resp.Body)
	return out, resp.StatusCode, time.Since(t0), err
}

// chat sends one non-streaming chat completion.
func (c *client) chat(ctx context.Context, tag string, r chatReq) chatOut {
	r.Model = c.model
	reqB, _ := json.MarshalIndent(r, "", " ")
	body, code, wall, err := c.post(ctx, "/chat/completions", r)
	o := chatOut{Wall: wall, HTTPCode: code, Err: err}
	if err != nil {
		body = []byte(`{"transport_error":` + mustJSON(err.Error()) + `}`)
	}
	o.RawFile = c.save(tag, reqB, body)
	if err != nil {
		return o
	}
	if code != http.StatusOK {
		o.Err = fmt.Errorf("HTTP %d: %s", code, truncate(string(body), 400))
		return o
	}
	var cr chatResp
	if err := json.Unmarshal(body, &cr); err != nil || len(cr.Choices) == 0 {
		o.Err = fmt.Errorf("unparseable response: %v %s", err, truncate(string(body), 300))
		return o
	}
	o.Content = cr.Choices[0].Message.Content
	o.Reasoning = cr.Choices[0].Message.ReasoningContent
	o.Finish = cr.Choices[0].FinishReason
	o.Usage = cr.Usage
	return o
}

// streamOut measures decode speed from the client side: tokens are counted as
// content-bearing SSE chunks (mistral.rs emits one per token), and the
// rate is taken between the first and the last chunk so prefill is excluded.
type streamOut struct {
	Content    string
	Chunks     int
	TTFT       time.Duration
	DecodeRate float64 // chunks/sec after the first chunk
	Finish     string
	Cancelled  bool // we stopped reading on purpose (maxChunks reached)
	HTTPCode   int
	Err        error
	RawFile    string
}

func (c *client) stream(ctx context.Context, tag string, r chatReq, maxChunks int) streamOut {
	r.Model = c.model
	r.Stream = true
	reqB, _ := json.MarshalIndent(r, "", " ")
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/chat/completions", bytes.NewReader(mustMarshal(r)))
	req.Header.Set("Content-Type", "application/json")
	t0 := time.Now()
	var o streamOut
	var log strings.Builder
	resp, err := c.http.Do(req)
	if err != nil {
		o.Err = err
		o.RawFile = c.save(tag, reqB, []byte(mustJSON(err.Error())))
		return o
	}
	defer resp.Body.Close()
	o.HTTPCode = resp.StatusCode
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		o.Err = fmt.Errorf("HTTP %d: %s", resp.StatusCode, truncate(string(b), 400))
		o.RawFile = c.save(tag, reqB, b)
		return o
	}
	var first, last time.Time
	var sb strings.Builder
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 1<<20), 1<<24)
	for sc.Scan() {
		line := sc.Text()
		log.WriteString(line + "\n")
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			break
		}
		var ch struct {
			Choices []struct {
				Delta struct {
					Content          string `json:"content"`
					ReasoningContent string `json:"reasoning_content"`
				} `json:"delta"`
				FinishReason *string `json:"finish_reason"`
			} `json:"choices"`
		}
		if json.Unmarshal([]byte(data), &ch) != nil || len(ch.Choices) == 0 {
			continue
		}
		d := ch.Choices[0].Delta
		if d.Content != "" || d.ReasoningContent != "" {
			now := time.Now()
			if o.Chunks == 0 {
				first = now
			}
			last = now
			o.Chunks++
			sb.WriteString(d.Content)
			if d.ReasoningContent != "" {
				sb.WriteString("<<reasoning:" + d.ReasoningContent + ">>")
			}
		}
		if ch.Choices[0].FinishReason != nil && *ch.Choices[0].FinishReason != "" {
			o.Finish = *ch.Choices[0].FinishReason
		}
		if maxChunks > 0 && o.Chunks >= maxChunks {
			o.Cancelled = true
			cancel()
			break
		}
	}
	if err := sc.Err(); err != nil && !o.Cancelled {
		o.Err = err
	}
	o.Content = sb.String()
	if o.Chunks > 0 {
		o.TTFT = first.Sub(t0)
		if o.Chunks > 1 {
			o.DecodeRate = float64(o.Chunks-1) / last.Sub(first).Seconds()
		}
	}
	o.RawFile = c.save(tag, reqB, []byte(log.String()))
	return o
}

type embResp struct {
	Data []struct {
		Embedding []float64 `json:"embedding"`
	} `json:"data"`
	Usage struct {
		PromptTokens int `json:"prompt_tokens"`
	} `json:"usage"`
}

func (c *client) embed(ctx context.Context, tag string, inputs []string) ([][]float64, int, error) {
	body := map[string]any{"model": c.model, "input": inputs}
	reqB, _ := json.MarshalIndent(body, "", " ")
	b, code, _, err := c.post(ctx, "/embeddings", body)
	if err != nil {
		c.save(tag, reqB, []byte(mustJSON(err.Error())))
		return nil, 0, err
	}
	c.save(tag, reqB, b)
	if code != http.StatusOK {
		return nil, 0, fmt.Errorf("HTTP %d: %s", code, truncate(string(b), 400))
	}
	var er embResp
	if err := json.Unmarshal(b, &er); err != nil {
		return nil, 0, err
	}
	out := make([][]float64, len(er.Data))
	for i, d := range er.Data {
		out[i] = d.Embedding
	}
	return out, er.Usage.PromptTokens, nil
}

func mustMarshal(v any) []byte { b, _ := json.Marshal(v); return b }
func mustJSON(s string) string { return string(mustMarshal(s)) }

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
