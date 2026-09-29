// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

package source

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/org-runink/river/guide/bundle"
	"github.com/org-runink/river/guide/github"
	"github.com/org-runink/river/guide/internal/fakegh"
)

func signed(t *testing.T) (ed25519.PublicKey, []byte, []byte) {
	t.Helper()
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	raw, err := bundle.Encode("platform", "9", []bundle.Doc{{Path: "a.md", Content: "## Platform step\n<!-- river-guide:step id=p1 kind=info -->\nDo the platform thing.\n"}})
	if err != nil {
		t.Fatal(err)
	}
	return pub, raw, bundle.Sign(priv, raw)
}

func TestFetchVerifiesAndMarksPrivate(t *testing.T) {
	pub, raw, sig := signed(t)
	gh := fakegh.New("down/stream")
	defer gh.Close()
	gh.Files["guide/platform.bundle.json"] = raw
	gh.Files["guide/platform.bundle.json.sig"] = sig
	c := github.New(gh.Token)
	c.API = gh.URL
	src := Source{Name: "platform", Repo: "down/stream", Path: "guide/platform.bundle.json", Ref: "main", PubKey: bundle.FormatPublicKey(pub)}

	b, err := Fetch(context.Background(), c, src)
	if err != nil {
		t.Fatal(err)
	}
	if !b.Private || b.Name != "platform" || len(b.Steps) != 1 {
		t.Fatalf("%+v", b)
	}

	// An identity without read access: refused by GitHub, so no bundle.
	var ua *ErrUnavailable
	c.Token = "ghu_wrong"
	if _, err := Fetch(context.Background(), c, src); err == nil {
		t.Fatal("fetched without read access")
	}
	c.Token = gh.Token

	// A path this identity cannot see is a 404: unavailable, not an error.
	missing := src
	missing.Path = "guide/missing.json"
	if _, err := Fetch(context.Background(), c, missing); !errors.As(err, &ua) {
		t.Fatalf("missing: %v", err)
	}

	// No trust anchor supplied: unavailable, never "trust on first use".
	noKey := src
	noKey.PubKey = ""
	if _, err := Fetch(context.Background(), c, noKey); !errors.As(err, &ua) {
		t.Fatalf("no key: %v", err)
	}

	// Tampered bundle: refused loudly.
	gh.Files["guide/platform.bundle.json"] = append([]byte(nil), raw[:len(raw)-2]...)
	if _, err := Fetch(context.Background(), c, src); err == nil || !strings.Contains(err.Error(), "REFUSED") {
		t.Fatalf("tampered: %v", err)
	}
	// Wrong trust anchor: refused.
	gh.Files["guide/platform.bundle.json"] = raw
	other, _, _ := ed25519.GenerateKey(rand.Reader)
	wrong := src
	wrong.PubKey = bundle.FormatPublicKey(other)
	if _, err := Fetch(context.Background(), c, wrong); err == nil || !strings.Contains(err.Error(), "REFUSED") {
		t.Fatalf("wrong key: %v", err)
	}
}

func TestLoadDir(t *testing.T) {
	dir := t.TempDir()
	if s, err := LoadDir(filepath.Join(dir, "absent")); err != nil || s != nil {
		t.Fatal("absent dir")
	}
	os.WriteFile(filepath.Join(dir, "a.json"), []byte(`{"name":"platform","repo":"down/stream","path":"g/b.json","ref":"main","client_id":"Iv1.abc"}`), 0o644)
	s, err := LoadDir(dir)
	if err != nil || len(s) != 1 || s[0].PubKey != "" {
		t.Fatalf("%v %v", s, err)
	}
	os.WriteFile(filepath.Join(dir, "b.json"), []byte(`{"name":"x","repo":"down/stream","path":"../etc/shadow"}`), 0o644)
	if _, err := LoadDir(dir); err == nil {
		t.Fatal("path traversal accepted")
	}
}
