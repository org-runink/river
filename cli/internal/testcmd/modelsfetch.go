// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

package testcmd

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/org-runink/river/pkg/pipe"
)

// modelsFetchChecks is every check ModelsFetch reports, by its stable evidence name (static:
// see testevidence.go).
var modelsFetchChecks = []string{
	"subset-listed-fetched", "subset-others-untouched", "subset-token-not-in-output", "subset-unknown-dest-fails",
	"full-mirror-fallback", "full-token-not-in-output",
	"token-sent-upstream", "token-not-sent-to-mirror", "token-dir-removed", "token-file-unreadable-fails",
}

// ModelsFetch — build/models-fetch.sh against a local upstream and mirror (no network):
//
//	subset   MODELS_ONLY fetches exactly the listed dests, and a name that is not a dest of
//	         the lock fails
//	token    HF_TOKEN_FILE reaches the upstream as an Authorization header and never the
//	         mirror, is on no command line, and its private directory is gone afterwards
//
// Tier 1 runs it (scripts/ci-tier1.sh).
func ModelsFetch(ctx context.Context, repo string, outw, errw io.Writer) error {
	script := filepath.Join(repo, "build/models-fetch.sh")
	if !isFile(script) {
		fmt.Fprintf(errw, "models-fetch: missing %s\n", script)
		return errors.New("models-fetch: missing the code under test")
	}
	t, err := os.MkdirTemp("", "models-fetch.")
	if err != nil {
		return err
	}
	defer os.RemoveAll(t)
	c := newChecks(ctx, outw, errw)

	const rev = "0123456789012345678901234567890123456789"
	const token = "hf_contract_test_not_a_secret"
	files := map[string][]byte{"a.bin": []byte("alpha"), "b.bin": []byte("bravo bravo"), "c.bin": []byte("charlie")}
	var mu sync.Mutex
	auth := map[string][]string{} // path prefix (upstream|mirror) -> Authorization headers seen
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		kind := "upstream"
		if strings.HasPrefix(r.URL.Path, "/mirror/") {
			kind = "mirror"
		}
		auth[kind] = append(auth[kind], r.Header.Get("Authorization"))
		mu.Unlock()
		name := filepath.Base(r.URL.Path)
		if kind == "upstream" && r.URL.Path == "/org/repo/resolve/"+rev+"/"+name {
			if b, ok := files[name]; ok && name != "c.bin" { // c.bin only on the mirror
				_, _ = w.Write(b)
				return
			}
		}
		if kind == "mirror" {
			for _, b := range files {
				s := sha256.Sum256(b)
				if name == hex.EncodeToString(s[:]) {
					_, _ = w.Write(b)
					return
				}
			}
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	var lock strings.Builder
	for _, n := range []string{"a.bin", "b.bin", "c.bin"} {
		s := sha256.Sum256(files[n])
		fmt.Fprintf(&lock, "role org/repo %s %s %d %s mit sub/%s\n", rev, n, len(files[n]), hex.EncodeToString(s[:]), n)
	}
	lockF, only, typo, tok := filepath.Join(t, "lock"), filepath.Join(t, "only"), filepath.Join(t, "typo"), filepath.Join(t, "token")
	rt := filepath.Join(t, "rt")
	models := filepath.Join(t, "models")
	for p, s := range map[string]string{lockF: lock.String(), only: "# one file\nsub/b.bin\n", typo: "sub/x.bin\n", tok: token + "\n"} {
		if err := os.WriteFile(p, []byte(s), 0o600); err != nil {
			return err
		}
	}
	if err := os.Mkdir(rt, 0o700); err != nil {
		return err
	}
	run := func(env ...string) (string, bool) {
		cmd := pipe.Cmd("sh", script)
		cmd.Env = append([]string{"PATH=/usr/bin:/bin", "HOME=" + t, "XDG_RUNTIME_DIR=" + rt,
			"MODELS_LOCK=" + lockF, "MODELS_DIR=" + models, "RIVER_MODELS_UPSTREAM=" + srv.URL}, env...)
		out, err := combined(ctx, cmd)
		return out, err == nil
	}

	out, ok := run("MODELS_ONLY="+only, "HF_TOKEN_FILE="+tok)
	c.expect("subset-listed-fetched", "MODELS_ONLY: the listed file is fetched", ok && isFile(filepath.Join(models, "sub/b.bin")))
	c.expect("subset-others-untouched", "MODELS_ONLY: the others are left alone", !exists(filepath.Join(models, "sub/a.bin")) && !exists(filepath.Join(models, "sub/c.bin")))
	c.expect("subset-token-not-in-output", "the token is never in the output", !strings.Contains(out, token))
	_, ok = run("MODELS_ONLY=" + typo)
	c.expect("subset-unknown-dest-fails", "MODELS_ONLY: a name that is not a dest of the lock fails", !ok)

	out, ok = run("HF_TOKEN_FILE="+tok, "RIVER_MODELS_MIRROR="+srv.URL+"/mirror")
	c.expect("full-mirror-fallback", "the whole lock, c.bin from the mirror", ok && isFile(filepath.Join(models, "sub/c.bin")))
	c.expect("full-token-not-in-output", "the token is never in the output", !strings.Contains(out, token))
	mu.Lock()
	up, mirror := auth["upstream"], auth["mirror"]
	mu.Unlock()
	sent := len(up) > 0
	for _, h := range up {
		sent = sent && h == "Bearer "+token
	}
	c.expect("token-sent-upstream", "HF_TOKEN_FILE: every upstream request carries the token", sent)
	clean := len(mirror) > 0
	for _, h := range mirror {
		clean = clean && h == ""
	}
	c.expect("token-not-sent-to-mirror", "HF_TOKEN_FILE: no mirror request carries it", clean)
	left, _ := os.ReadDir(rt)
	c.expect("token-dir-removed", "HF_TOKEN_FILE: the private header directory is removed", len(left) == 0)
	_, ok = run("HF_TOKEN_FILE=" + filepath.Join(t, "missing"))
	c.expect("token-file-unreadable-fails", "an unreadable HF_TOKEN_FILE fails", !ok)

	if c.failed > 0 {
		return fmt.Errorf("models-fetch: %d of %d check(s) failed", c.failed, c.n)
	}
	fmt.Fprintf(outw, "models-fetch: %d check(s) passed\n", c.n)
	return nil
}
