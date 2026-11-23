// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"errors"
	"io"
	"log"
	"net/http/httptest"
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
