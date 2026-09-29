// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

// Package fakegh is an in-memory GitHub for tests: the device flow, one repository's
// issues and comments, and raw file contents. It records every comment posted so tests
// can assert on exactly what would have left the machine.
package fakegh

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Comment as stored.
type Comment struct {
	ID          int64
	Body        string
	Login       string
	Association string
	Created     time.Time
}

// Server is the fake.
type Server struct {
	*httptest.Server
	mu       sync.Mutex
	Token    string // the token the device flow issues and the API accepts
	Login    string // the signed-in user
	Private  bool
	Repo     string // owner/name
	Issues   map[int][]string
	Labels   map[int][]string
	comments map[int][]Comment
	Files    map[string][]byte // path -> bytes (readable only with Token)
	nextID   int64
	polls    int
	pending  int // device flow: authorization_pending replies before success
}

// New starts a fake for repo (owner/name).
func New(repo string) *Server {
	s := &Server{Token: "ghu_faketoken0123456789abcdefghij", Login: "operator", Repo: repo, Private: true, // gitleaks:allow (fake token for the in-memory GitHub)
		Issues: map[int][]string{}, Labels: map[int][]string{}, comments: map[int][]Comment{}, Files: map[string][]byte{}, nextID: 1000, pending: 1}
	s.Server = httptest.NewServer(http.HandlerFunc(s.serve))
	return s
}

// AddComment injects a comment from someone else.
func (s *Server) AddComment(issue int, login, assoc, body string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextID++
	s.comments[issue] = append(s.comments[issue], Comment{ID: s.nextID, Body: body, Login: login, Association: assoc, Created: time.Now().UTC()})
}

// Posted returns every comment the client posted (as the signed-in user).
func (s *Server) Posted(issue int) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for _, c := range s.comments[issue] {
		if c.Login == s.Login {
			out = append(out, c.Body)
		}
	}
	return out
}

// All returns every comment on the issue in order.
func (s *Server) All(issue int) []Comment {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Comment(nil), s.comments[issue]...)
}

func (s *Server) authed(r *http.Request) bool {
	return r.Header.Get("Authorization") == "Bearer "+s.Token
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := r.URL.Path
	switch {
	case p == "/zen":
		fmt.Fprint(w, "Keep it logically awesome.")
		return
	case p == "/login/device/code" && r.Method == http.MethodPost:
		_ = r.ParseForm()
		if r.Form.Get("client_id") == "" {
			writeJSON(w, 200, map[string]string{"error": "unauthorized_client"})
			return
		}
		writeJSON(w, 200, map[string]any{"device_code": "dc-1", "user_code": "ABCD-1234", "verification_uri": "https://github.com/login/device", "expires_in": 900, "interval": 5})
		return
	case p == "/login/oauth/access_token" && r.Method == http.MethodPost:
		s.polls++
		if s.polls <= s.pending {
			writeJSON(w, 200, map[string]string{"error": "authorization_pending"})
			return
		}
		writeJSON(w, 200, map[string]string{"access_token": s.Token, "token_type": "bearer"})
		return
	}
	if !s.authed(r) {
		writeJSON(w, 401, map[string]string{"message": "Bad credentials"})
		return
	}
	base := "/repos/" + s.Repo
	switch {
	case p == "/user":
		writeJSON(w, 200, map[string]string{"login": s.Login})
	case p == base && r.Method == http.MethodGet:
		writeJSON(w, 200, map[string]any{"full_name": s.Repo, "private": s.Private})
	case p == base+"/issues" && r.Method == http.MethodPost:
		var in struct {
			Title  string   `json:"title"`
			Body   string   `json:"body"`
			Labels []string `json:"labels"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		n := len(s.Issues) + 1
		s.Issues[n] = []string{in.Title, in.Body}
		s.Labels[n] = in.Labels
		writeJSON(w, 201, map[string]any{"number": n, "title": in.Title, "state": "open", "html_url": "https://github.com/" + s.Repo + "/issues/" + strconv.Itoa(n)})
	case strings.HasPrefix(p, base+"/issues/"):
		rest := strings.Split(strings.TrimPrefix(p, base+"/issues/"), "/")
		n, _ := strconv.Atoi(rest[0])
		switch {
		case len(rest) == 2 && rest[1] == "labels" && r.Method == http.MethodPost:
			var in struct {
				Labels []string `json:"labels"`
			}
			_ = json.NewDecoder(r.Body).Decode(&in)
			s.Labels[n] = append(s.Labels[n], in.Labels...)
			writeJSON(w, 200, []any{})
		case len(rest) == 2 && rest[1] == "comments" && r.Method == http.MethodGet:
			since, _ := time.Parse(time.RFC3339, r.URL.Query().Get("since"))
			var out []map[string]any
			for _, c := range s.comments[n] {
				if !since.IsZero() && c.Created.Before(since) {
					continue
				}
				out = append(out, map[string]any{"id": c.ID, "body": c.Body, "user": map[string]string{"login": c.Login},
					"author_association": c.Association, "created_at": c.Created.Format(time.RFC3339Nano)})
			}
			writeJSON(w, 200, out)
		case len(rest) == 2 && rest[1] == "comments" && r.Method == http.MethodPost:
			var in struct {
				Body string `json:"body"`
			}
			_ = json.NewDecoder(r.Body).Decode(&in)
			s.nextID++
			c := Comment{ID: s.nextID, Body: in.Body, Login: s.Login, Association: "OWNER", Created: time.Now().UTC()}
			s.comments[n] = append(s.comments[n], c)
			writeJSON(w, 201, map[string]any{"id": c.ID, "body": c.Body, "user": map[string]string{"login": c.Login}})
		default:
			writeJSON(w, 404, map[string]string{"message": "Not Found"})
		}
	case strings.HasPrefix(p, base+"/contents/"):
		f, ok := s.Files[strings.TrimPrefix(p, base+"/contents/")]
		if !ok {
			writeJSON(w, 404, map[string]string{"message": "Not Found"})
			return
		}
		if r.Header.Get("Accept") != "application/vnd.github.raw" {
			writeJSON(w, 415, map[string]string{"message": "want raw"})
			return
		}
		_, _ = w.Write(f)
	default:
		writeJSON(w, 404, map[string]string{"message": "Not Found"})
	}
}
