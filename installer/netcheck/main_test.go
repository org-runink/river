// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The config example in docs/INSTALL.md ("Network first") must be one river-netsetup accepts.
func TestDocsConfigExampleParses(t *testing.T) {
	raw, err := os.ReadFile("../../docs/INSTALL.md")
	if err != nil {
		t.Fatal(err)
	}
	_, after, ok := strings.Cut(string(raw), "\n```ini\n")
	if !ok {
		t.Fatal("docs/INSTALL.md has no ```ini config example")
	}
	example, _, _ := strings.Cut(after, "\n```")
	p := filepath.Join(t.TempDir(), "net.conf")
	os.WriteFile(p, []byte(example), 0o600)
	var out, errb bytes.Buffer
	if code := run([]string{"config", p}, &out, &errb); code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	if !strings.Contains(out.String(), "NET_METHOD='wifi'\n") || !strings.Contains(out.String(), "NET_SSID='Example Lab'\n") {
		t.Fatalf("%s", out.String())
	}
}

func TestCmdlineCommand(t *testing.T) {
	p := filepath.Join(t.TempDir(), "cmdline")
	for cmdline, want := range map[string]string{
		"BOOT_IMAGE=/vmlinuz quiet\n":                         "NET_METHOD=''\n",
		"quiet river.net=dhcp,v6only\n":                       "NET_V6ONLY='1'\n",
		"river.net=static:192.0.2.10/24,gw=192.0.2.1 quiet\n": "NET_GW4='192.0.2.1'\n",
	} {
		os.WriteFile(p, []byte(cmdline), 0o644)
		var out, errb bytes.Buffer
		if code := run([]string{"cmdline", "--file", p}, &out, &errb); code != 0 || !strings.Contains(out.String(), want) {
			t.Errorf("%q: exit %d, out %s err %s", cmdline, code, out.String(), errb.String())
		}
	}
	os.WriteFile(p, []byte("river.net=wifi:x\n"), 0o644)
	var out, errb bytes.Buffer
	if code := run([]string{"cmdline", "--file", p}, &out, &errb); code != 1 || out.Len() != 0 {
		t.Fatalf("an invalid river.net= must print nothing to eval and fail: exit %d, %q", code, out.String())
	}
}

func TestCheckUsage(t *testing.T) {
	var out, errb bytes.Buffer
	for _, args := range [][]string{
		{"check", "--host", "exa mple"},
		{"check", "--port", "0"},
		{"check", "--proxy", "ftp://x:1"},
		{"check", "--hostname", "Bad_Name"},
		{"nope"},
		{},
	} {
		if code := run(args, &out, &errb); code != 2 {
			t.Errorf("%v: exit %d, want 2", args, code)
		}
	}
}
