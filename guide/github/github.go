// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

// Package github is the small slice of GitHub river-guide uses: the OAuth device flow
// (sign-in with no credential on the medium), issue comments (the remote "tag" channel),
// and reading one file from a repository (a private platform guide bundle).
//
// Everything is outbound HTTPS, polled; nothing listens. Base URLs are fields so tests
// (and GitHub Enterprise) can point elsewhere.
//
// IPv6-only networks: github.com and api.github.com publish no AAAA records. On an
// IPv6-only install network they are reachable only through NAT64/DNS64 or an HTTPS
// proxy. The transport honours HTTPS_PROXY/NO_PROXY, and Preflight turns the resulting
// dial error into an explanation instead of a bare "no route to host".
package github

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

// Client talks to one GitHub instance with one user token.
type Client struct {
	API   string // https://api.github.com
	Web   string // https://github.com
	Token string
	HTTP  *http.Client
	UA    string

	pollOverride time.Duration
}

// New returns a client for github.com. The token is held in memory only.
func New(token string) *Client {
	return &Client{
		API:   "https://api.github.com",
		Web:   "https://github.com",
		Token: token,
		HTTP: &http.Client{
			Timeout:   45 * time.Second,
			Transport: &http.Transport{Proxy: http.ProxyFromEnvironment, ForceAttemptHTTP2: true, TLSHandshakeTimeout: 20 * time.Second},
		},
		UA: "river-guide",
	}
}

// APIError is a non-2xx reply.
type APIError struct {
	Status int
	Body   string
}

func (e *APIError) Error() string { return fmt.Sprintf("github: %d: %s", e.Status, e.Body) }

// IsNotFound reports a 404 — which GitHub also returns for "exists but you may not read it".
func IsNotFound(err error) bool {
	var ae *APIError
	return errors.As(err, &ae) && ae.Status == http.StatusNotFound
}

func (c *Client) do(ctx context.Context, method, path string, in, out any, accept string) (http.Header, error) {
	var body io.Reader
	if in != nil {
		buf, err := json.Marshal(in)
		if err != nil {
			return nil, err
		}
		body = bytes.NewReader(buf)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.API+path, body)
	if err != nil {
		return nil, err
	}
	if accept == "" {
		accept = "application/vnd.github+json"
	}
	req.Header.Set("Accept", accept)
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", c.UA)
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, explainDial(err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg := strings.TrimSpace(string(raw))
		if len(msg) > 300 {
			msg = msg[:300]
		}
		return resp.Header, &APIError{Status: resp.StatusCode, Body: msg}
	}
	if out != nil {
		if b, ok := out.(*[]byte); ok {
			*b = raw
			return resp.Header, nil
		}
		if err := json.Unmarshal(raw, out); err != nil {
			return resp.Header, fmt.Errorf("github: decode %s: %w", path, err)
		}
	}
	return resp.Header, nil
}

// explainDial adds the IPv6-only hint to network errors.
func explainDial(err error) error {
	var ne net.Error
	var oe *net.OpError
	if errors.As(err, &oe) || errors.As(err, &ne) {
		return fmt.Errorf("%w (GitHub publishes no IPv6 address: an IPv6-only network needs NAT64/DNS64 or an HTTPS proxy — set river.guide.proxy= on the kernel command line)", err)
	}
	return err
}

// Preflight checks that api.github.com is reachable through the configured transport.
func (c *Client) Preflight(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.API+"/zen", nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", c.UA)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return explainDial(err)
	}
	_ = resp.Body.Close()
	return nil
}

// Repo is the subset of a repository river-guide reads.
type Repo struct {
	FullName string `json:"full_name"`
	Private  bool   `json:"private"`
}

// GetRepo returns the repository.
func (c *Client) GetRepo(ctx context.Context, owner, name string) (*Repo, error) {
	var r Repo
	_, err := c.do(ctx, http.MethodGet, "/repos/"+esc(owner)+"/"+esc(name), nil, &r, "")
	return &r, err
}

// User is the signed-in identity.
type User struct {
	Login string `json:"login"`
}

// Me returns the token's user.
func (c *Client) Me(ctx context.Context) (*User, error) {
	var u User
	_, err := c.do(ctx, http.MethodGet, "/user", nil, &u, "")
	return &u, err
}

// Issue is the subset of an issue river-guide reads.
type Issue struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
	State  string `json:"state"`
	URL    string `json:"html_url"`
}

// CreateIssue opens an issue with labels.
func (c *Client) CreateIssue(ctx context.Context, owner, name, title, body string, labels []string) (*Issue, error) {
	var is Issue
	_, err := c.do(ctx, http.MethodPost, "/repos/"+esc(owner)+"/"+esc(name)+"/issues",
		map[string]any{"title": title, "body": body, "labels": labels}, &is, "")
	return &is, err
}

// GetIssue reads an issue.
func (c *Client) GetIssue(ctx context.Context, owner, name string, n int) (*Issue, error) {
	var is Issue
	_, err := c.do(ctx, http.MethodGet, fmt.Sprintf("/repos/%s/%s/issues/%d", esc(owner), esc(name), n), nil, &is, "")
	return &is, err
}

// AddLabels labels an issue (creating the label implicitly, as GitHub does).
func (c *Client) AddLabels(ctx context.Context, owner, name string, n int, labels []string) error {
	_, err := c.do(ctx, http.MethodPost, fmt.Sprintf("/repos/%s/%s/issues/%d/labels", esc(owner), esc(name), n),
		map[string]any{"labels": labels}, nil, "")
	return err
}

// Comment is an issue comment.
type Comment struct {
	ID                int64     `json:"id"`
	Body              string    `json:"body"`
	User              User      `json:"user"`
	AuthorAssociation string    `json:"author_association"`
	CreatedAt         time.Time `json:"created_at"`
}

// Comments lists comments created after `since` (GitHub's `since` is on updated_at;
// callers also de-duplicate by ID).
func (c *Client) Comments(ctx context.Context, owner, name string, n int, since time.Time) ([]Comment, error) {
	q := url.Values{"per_page": {"100"}}
	if !since.IsZero() {
		q.Set("since", since.UTC().Format(time.RFC3339))
	}
	var out []Comment
	_, err := c.do(ctx, http.MethodGet, fmt.Sprintf("/repos/%s/%s/issues/%d/comments?%s", esc(owner), esc(name), n, q.Encode()), nil, &out, "")
	return out, err
}

// PostComment adds a comment.
func (c *Client) PostComment(ctx context.Context, owner, name string, n int, body string) (*Comment, error) {
	var out Comment
	_, err := c.do(ctx, http.MethodPost, fmt.Sprintf("/repos/%s/%s/issues/%d/comments", esc(owner), esc(name), n),
		map[string]any{"body": body}, &out, "")
	return &out, err
}

// RawFile reads one file's bytes at ref. A 404 means missing OR not readable by this
// identity; callers must treat both the same way.
func (c *Client) RawFile(ctx context.Context, owner, name, path, ref string) ([]byte, error) {
	p := "/repos/" + esc(owner) + "/" + esc(name) + "/contents/" + escPath(path)
	if ref != "" {
		p += "?ref=" + url.QueryEscape(ref)
	}
	var raw []byte
	_, err := c.do(ctx, http.MethodGet, p, nil, &raw, "application/vnd.github.raw")
	return raw, err
}

func esc(s string) string { return url.PathEscape(s) }

func escPath(p string) string {
	parts := strings.Split(strings.Trim(p, "/"), "/")
	for i, s := range parts {
		parts[i] = url.PathEscape(s)
	}
	return strings.Join(parts, "/")
}

// ParseIssueRef accepts owner/repo#N, owner/repo/N, or owner/repo (N = 0: create one).
func ParseIssueRef(s string) (owner, repo string, n int, err error) {
	s = strings.TrimSpace(s)
	rest := s
	if i := strings.LastIndexAny(s, "#"); i >= 0 {
		rest = s[:i]
		if _, err = fmt.Sscanf(s[i+1:], "%d", &n); err != nil || n <= 0 {
			return "", "", 0, fmt.Errorf("issue ref %q: bad number", s)
		}
	}
	parts := strings.Split(rest, "/")
	if len(parts) == 3 && n == 0 {
		if _, err = fmt.Sscanf(parts[2], "%d", &n); err != nil || n <= 0 {
			return "", "", 0, fmt.Errorf("issue ref %q: bad number", s)
		}
		parts = parts[:2]
	}
	if len(parts) != 2 || !validSeg(parts[0]) || !validSeg(parts[1]) {
		return "", "", 0, fmt.Errorf("issue ref %q: want owner/repo#N or owner/repo", s)
	}
	return parts[0], parts[1], n, nil
}

func validSeg(s string) bool {
	if s == "" || len(s) > 100 {
		return false
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.') {
			return false
		}
	}
	return s != "." && s != ".."
}
