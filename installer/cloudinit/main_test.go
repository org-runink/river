// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/org-runink/river/installer/internal/cloud"
	"github.com/org-runink/river/installer/internal/cloud/fake"
)

func TestMovedMountpoint(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"/var/lib/core", "/var/lib/tide"},
		{"/var/lib/core/models/shared", "/var/lib/tide/models/shared"},
		{"/var/lib/tide", ""},
		{"/var/lib/core-runner", ""},
		{"/var/lib/corex/models", ""},
		{"/home", ""},
		{"", ""},
	} {
		got, ok := movedMountpoint(c.in)
		if got != c.want || ok != (c.want != "") {
			t.Errorf("movedMountpoint(%q) = %q, %v; want %q", c.in, got, ok, c.want)
		}
	}
}

// Every data dataset an older image mounted under /var/lib/core must map onto its current
// mountpoint, or the migration would never fire for it.
func TestLegacyDatasetsMapToCurrent(t *testing.T) {
	for _, d := range dataDatasets {
		old := ""
		switch d.name {
		case "state":
			old = legacyStateDir
		case "models":
			old = legacyStateDir + "/models/shared"
		default:
			continue
		}
		if got, ok := movedMountpoint(old); !ok || got != d.mountpoint {
			t.Errorf("%s: old path %s maps to %q, current mountpoint is %s", d.name, old, got, d.mountpoint)
		}
	}
}

func TestProfile(t *testing.T) {
	for _, c := range []struct {
		in, want string
		bad      bool
	}{
		{"", profileNode, false},
		{"node", profileNode, false},
		{"base", profileBase, false},
		{"Base", "", true},
		{"desktop", "", true},
	} {
		got, err := profile(map[string]string{"RIVER_CLOUD_PROFILE": c.in})
		if (err != nil) != c.bad || got != c.want {
			t.Errorf("profile(%q) = %q, %v; want %q (error %v)", c.in, got, err, c.want, c.bad)
		}
	}
}

// The watch loop applies the keys once at start, again after each metadata change, retries a
// failed apply instead of skipping that change, and does nothing while metadata is unchanged.
func TestWatchLoop(t *testing.T) {
	logger = log.New(io.Discard, "", 0)
	cfg := &fake.Config{InstanceID: "1", Hostname: "h.example.internal",
		Attributes: map[string]string{"ssh-keys": "a:ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIA a"}}
	srv := httptest.NewServer(cfg.Handler())
	defer srv.Close()
	md := &cloud.Metadata{Base: srv.URL + "/computeMetadata/v1"}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	seen := make(chan string, 16)
	var failNext atomic.Bool
	apply := func(ctx context.Context) error {
		v, err := md.Get(ctx, "instance/attributes/ssh-keys")
		if err != nil {
			return err
		}
		if failNext.CompareAndSwap(true, false) {
			seen <- "failed"
			return errors.New("injected")
		}
		seen <- v
		return nil
	}
	done := make(chan struct{})
	go func() { _ = watchLoop(ctx, md, apply, 2, 10*time.Millisecond); close(done) }()

	next := func() string {
		t.Helper()
		select {
		case v := <-seen:
			return v
		case <-time.After(10 * time.Second):
			t.Fatal("apply was not called")
			return ""
		}
	}
	if v := next(); !strings.HasPrefix(v, "a:") {
		t.Fatalf("first apply saw %q", v)
	}
	// Unchanged metadata: the 2 s held request times out and nothing is applied.
	select {
	case v := <-seen:
		t.Fatalf("applied %q with no change", v)
	case <-time.After(2500 * time.Millisecond):
	}
	failNext.Store(true)
	cfg.Update(func(c *fake.Config) { c.Attributes["ssh-keys"] = "b:ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIB b" })
	if v := next(); v != "failed" {
		t.Fatalf("change not delivered: %q", v)
	}
	if v := next(); !strings.HasPrefix(v, "b:") {
		t.Fatalf("failed apply was not retried: %q", v)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("watchLoop did not stop on cancel")
	}
}

func okEnsure(string) error { return nil }

// A base image writes one key file per account, removes the files it wrote for accounts that
// left metadata, never touches a file it did not write, and gives no key to an account it
// could not create.
func TestUserKeys(t *testing.T) {
	logger = log.New(io.Discard, "", 0)
	dir := t.TempDir()
	t.Setenv("TMPDIR", dir) // setStatus writes /run, which is not writable here; errors are ignored
	own := filepath.Join(dir, "unmanaged")
	if err := os.WriteFile(own, []byte("ssh-ed25519 AAAA admin-made\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	k := func(s string) []string { return []string{"ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI" + s + " metadata:x"} }
	if err := userKeys(dir, map[string][]string{"alice": k("A"), "bob": k("B")}, okEnsure); err != nil {
		t.Fatal(err)
	}
	for _, u := range []string{"alice", "bob"} {
		b, err := os.ReadFile(filepath.Join(dir, u))
		if err != nil || !strings.HasPrefix(string(b), managedHeader) || !strings.Contains(string(b), "ssh-ed25519") {
			t.Fatalf("%s: %q, %v", u, b, err)
		}
	}
	// bob leaves metadata, carol cannot be created.
	ensure := func(u string) error {
		if u == "carol" {
			return errors.New("useradd failed")
		}
		return nil
	}
	err := userKeys(dir, map[string][]string{"alice": k("A"), "carol": k("C")}, ensure)
	if err == nil || !strings.Contains(err.Error(), "carol") {
		t.Fatalf("want an error naming carol, got %v", err)
	}
	for u, want := range map[string]bool{"alice": true, "bob": false, "carol": false, "unmanaged": true} {
		if _, e := os.Stat(filepath.Join(dir, u)); (e == nil) != want {
			t.Errorf("%s present=%v, want %v", u, e == nil, want)
		}
	}
}

// A metadata error that is not "not found" must not be read as "no keys".
func TestMetadataSSHKeysError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Metadata-Flavor", "Google")
		if strings.HasSuffix(r.URL.Path, "/block-project-ssh-keys") {
			http.Error(w, "boom", http.StatusInternalServerError)
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()
	md := &cloud.Metadata{Base: srv.URL + "/computeMetadata/v1"}
	if v, err := metadataSSHKeys(context.Background(), md); err == nil {
		t.Fatalf("a 500 was read as %q, nil error", v)
	}
	cfg := &fake.Config{InstanceID: "1", Hostname: "h.example.internal"}
	ok := httptest.NewServer(cfg.Handler())
	defer ok.Close()
	v, err := metadataSSHKeys(context.Background(), &cloud.Metadata{Base: ok.URL + "/computeMetadata/v1"})
	if err != nil || v != "" {
		t.Fatalf("no keys at all is not an error: %q, %v", v, err)
	}
}

func TestRunStartupScript(t *testing.T) {
	logger = log.New(io.Discard, "", 0)
	dir := t.TempDir()
	marker, logp, sp := filepath.Join(dir, "marker"), filepath.Join(dir, "log"), filepath.Join(dir, "script")
	// no shebang: run with sh; the exit code is passed on
	code, err := runStartupScript("echo ran > "+marker+"\necho out-line\nexit 3\n", sp, logp)
	if err != nil || code != 3 {
		t.Fatalf("code %d, err %v", code, err)
	}
	if b, _ := os.ReadFile(marker); strings.TrimSpace(string(b)) != "ran" {
		t.Errorf("script did not run: %q", b)
	}
	lb, _ := os.ReadFile(logp)
	if !strings.Contains(string(lb), "out-line") || !strings.Contains(string(lb), "exit 3") {
		t.Errorf("log %q", lb)
	}
	if exists(sp) {
		t.Error("the script was left on disk")
	}
	if st, _ := os.Stat(logp); st.Mode().Perm() != 0o600 {
		t.Errorf("log mode %v", st.Mode().Perm())
	}
	// shebang: run directly
	code, err = runStartupScript("#!/bin/sh\necho direct >> "+marker+"\n", sp, logp)
	if err != nil || code != 0 {
		t.Fatalf("code %d, err %v", code, err)
	}
	if b, _ := os.ReadFile(marker); !strings.Contains(string(b), "direct") {
		t.Errorf("shebang script did not run: %q", b)
	}
	// a shebang naming a missing interpreter is a start failure, not an exit code
	if _, err = runStartupScript("#!/nonexistent/interp\n", sp, logp); err == nil {
		t.Error("a missing interpreter was not an error")
	}
}
