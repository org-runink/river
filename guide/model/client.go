// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

// Package model is a minimal client for the OpenAI-compatible chat endpoint that the
// install medium's own model server (mistral.rs) exposes on the IPv6 loopback.
//
// It speaks to a LOCAL process only. There is no API key, no vendor SDK and no remote
// default: a URL that is not loopback is refused unless the caller opts in, so a
// misconfiguration cannot send an operator's questions off the machine.
package model

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Message is one chat turn.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// Chatter is what the agent needs from a model; tests supply a fake.
type Chatter interface {
	Chat(ctx context.Context, msgs []Message) (string, error)
}

// Client talks to one OpenAI-compatible server.
type Client struct {
	BaseURL     string // e.g. http://[::1]:8187/v1
	Model       string // may be empty: the server serves what it loaded
	MaxTokens   int
	Temperature float64
	HTTP        *http.Client
}

// New validates baseURL. allowRemote must be true to use a non-loopback host.
func New(baseURL, modelName string, allowRemote bool) (*Client, error) {
	u, err := url.Parse(baseURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("model url %q: want http(s)://host:port/v1", baseURL)
	}
	if !allowRemote && !isLoopback(u.Hostname()) {
		return nil, fmt.Errorf("model url %q is not loopback; river-guide only talks to the model served on this machine", baseURL)
	}
	return &Client{
		BaseURL:     strings.TrimSuffix(baseURL, "/"),
		Model:       modelName,
		MaxTokens:   320,
		Temperature: 0,
		// Generous: a 1.5B model on a 4-core CPU can take tens of seconds for 300 tokens.
		HTTP: &http.Client{Timeout: 4 * time.Minute},
	}, nil
}

func isLoopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// Ready reports whether the server answers /models.
func (c *Client) Ready(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/models", nil)
	if err != nil {
		return err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET /models: %s", resp.Status)
	}
	return nil
}

// Chat sends a non-streaming completion request and returns the assistant text.
func (c *Client) Chat(ctx context.Context, msgs []Message) (string, error) {
	body := map[string]any{
		"messages":    msgs,
		"max_tokens":  c.MaxTokens,
		"temperature": c.Temperature,
		"stream":      false,
	}
	if c.Model != "" {
		body["model"] = c.Model
	} else {
		body["model"] = "default"
	}
	buf, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/chat/completions", bytes.NewReader(buf))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("chat: %s: %s", resp.Status, strings.TrimSpace(string(raw)))
	}
	var out struct {
		Choices []struct {
			Message Message `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", fmt.Errorf("chat: decode: %w", err)
	}
	if len(out.Choices) == 0 {
		return "", errors.New("chat: no choices")
	}
	return stripThink(out.Choices[0].Message.Content), nil
}

// stripThink removes a hybrid-reasoning model's <think>…</think> block.
func stripThink(s string) string {
	for {
		i := strings.Index(s, "<think>")
		if i < 0 {
			return strings.TrimSpace(s)
		}
		j := strings.Index(s[i:], "</think>")
		if j < 0 {
			return strings.TrimSpace(s[:i])
		}
		s = s[:i] + s[i+j+len("</think>"):]
	}
}
