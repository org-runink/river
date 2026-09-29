// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

// Package source fetches a PRIVATE guide bundle that a downstream platform supplies.
//
// The install medium is public, so it carries no bundle content, no credential and no
// key of any downstream: Runink River itself ships NO source descriptor and NO trust anchor.
// A downstream build adds a descriptor (/etc/river-guide/sources.d/*.json) naming where
// its bundle lives. The ed25519 public key it must be signed with (the trust anchor) is
// either in that descriptor or supplied at fetch time (--bundle-key,
// river.guide.bundle_key=, or typed at the console when `login` asks for it):
//
//	{"name":"platform","repo":"example-org/platform","path":"guide/platform.bundle.json",
//	 "ref":"main","pubkey":"ed25519:…","sha256":"","client_id":"Iv1.…"}
//
// The bundle is fetched only after the operator signs in with the device flow, with THAT
// identity's token: an operator who cannot read the repository gets a 404 and the guide
// continues with River-only steps. The bytes are verified before they are parsed, held
// in process memory only, and never written to disk.
package source

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/org-runink/river/guide/bundle"
	"github.com/org-runink/river/guide/github"
)

// Source describes one private bundle.
type Source struct {
	Name     string `json:"name"`
	Repo     string `json:"repo"` // owner/name
	Path     string `json:"path"` // bundle file; the signature is Path + ".sig"
	Ref      string `json:"ref"`
	PubKey   string `json:"pubkey,omitempty"` // trust anchor; may instead be supplied at fetch time
	SHA256   string `json:"sha256,omitempty"`
	ClientID string `json:"client_id,omitempty"` // GitHub App client ID for the device flow (not a secret)
}

// Validate checks a descriptor without network access.
func (s Source) Validate() error {
	if _, _, _, err := github.ParseIssueRef(s.Repo); err != nil {
		return fmt.Errorf("source %q: repo: %w", s.Name, err)
	}
	if s.Path == "" || strings.Contains(s.Path, "..") {
		return fmt.Errorf("source %q: bad path %q", s.Name, s.Path)
	}
	if s.PubKey != "" {
		if _, err := bundle.ParsePublicKey(s.PubKey); err != nil {
			return fmt.Errorf("source %q: %w", s.Name, err)
		}
	}
	return nil
}

// LoadDir reads every *.json descriptor in dir (a missing dir is no sources).
func LoadDir(dir string) ([]Source, error) {
	ents, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Source
	for _, e := range ents {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		var s Source
		dec := json.NewDecoder(strings.NewReader(string(raw)))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&s); err != nil {
			return nil, fmt.Errorf("%s: %w", e.Name(), err)
		}
		if err := s.Validate(); err != nil {
			return nil, fmt.Errorf("%s: %w", e.Name(), err)
		}
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// Fetcher is the slice of the GitHub client Fetch needs.
type Fetcher interface {
	RawFile(ctx context.Context, owner, name, path, ref string) ([]byte, error)
}

// ErrUnavailable means the signed-in identity cannot read the bundle (or it is absent).
type ErrUnavailable struct{ Reason string }

func (e *ErrUnavailable) Error() string { return "platform guide bundle unavailable: " + e.Reason }

// Fetch downloads, verifies and parses the bundle. The result is marked Private.
func Fetch(ctx context.Context, gh Fetcher, s Source) (*bundle.Bundle, error) {
	if err := s.Validate(); err != nil {
		return nil, err
	}
	if s.PubKey == "" {
		return nil, &ErrUnavailable{Reason: "no verification key (trust anchor) was supplied for it"}
	}
	owner, repo, _, _ := github.ParseIssueRef(s.Repo)
	pub, err := bundle.ParsePublicKey(s.PubKey)
	if err != nil {
		return nil, err
	}
	raw, err := gh.RawFile(ctx, owner, repo, s.Path, s.Ref)
	if github.IsNotFound(err) {
		return nil, &ErrUnavailable{Reason: "this GitHub identity cannot read it (or it does not exist)"}
	}
	if err != nil {
		return nil, &ErrUnavailable{Reason: err.Error()}
	}
	sig, err := gh.RawFile(ctx, owner, repo, s.Path+".sig", s.Ref)
	if err != nil {
		return nil, &ErrUnavailable{Reason: "signature not readable: " + err.Error()}
	}
	b, err := bundle.VerifyAndDecode(pub, raw, sig, s.SHA256)
	if err != nil {
		// A bundle that fails verification is an attack or a mistake; either way it is
		// refused, and the reason is loud.
		return nil, fmt.Errorf("platform guide bundle REFUSED: %w", err)
	}
	for i := range raw {
		raw[i] = 0 // the parsed form is all we keep
	}
	b.Private = true
	return b, nil
}
