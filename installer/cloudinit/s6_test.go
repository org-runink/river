// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

package main

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// The s6 services of a Runink River cloud image (installer/cloud/s6) must be well-formed s6-rc
// source definitions, call only river-cloud-init subcommands that exist, and depend only on
// each other or on services every image's s6 base provides.
func TestCloudS6Services(t *testing.T) {
	const dir = "../cloud/s6"
	src, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range ents {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	want := []string{"river-cloud-identity", "river-cloud-init", "river-cloud-late", "river-cloud-watch"}
	if !slices.Equal(names, want) {
		t.Fatalf("services %v, want %v", names, want)
	}
	base := map[string]bool{"mount-filesystems": true, "remount-root": true, "NetworkManager-srv": true}
	call := regexp.MustCompile(`/usr/local/bin/river-cloud-init ([a-z]+)`)
	for _, n := range names {
		typ, err := os.ReadFile(filepath.Join(dir, n, "type"))
		if err != nil {
			t.Fatalf("%s: %v", n, err)
		}
		script := map[string]string{"oneshot": "up", "longrun": "run"}[strings.TrimSpace(string(typ))]
		if script == "" {
			t.Fatalf("%s: type %q", n, typ)
		}
		p := filepath.Join(dir, n, script)
		fi, err := os.Stat(p)
		if err != nil {
			t.Fatalf("%s: no %s file: %v", n, script, err)
		}
		if script == "run" && fi.Mode()&0o111 == 0 {
			t.Errorf("%s: run is not executable", n)
		}
		body, _ := os.ReadFile(p)
		if !strings.HasPrefix(string(body), "#!/bin/execlineb -P\n") {
			t.Errorf("%s/%s: not an execline script", n, script)
		}
		m := call.FindStringSubmatch(string(body))
		if m == nil {
			t.Errorf("%s/%s: calls no river-cloud-init subcommand", n, script)
		} else if !strings.Contains(string(src), "case \""+m[1]+"\":") {
			t.Errorf("%s/%s: river-cloud-init has no subcommand %q", n, script, m[1])
		}
		deps, err := os.ReadDir(filepath.Join(dir, n, "dependencies.d"))
		if err != nil || len(deps) == 0 {
			t.Errorf("%s: no dependencies.d entries (%v)", n, err)
		}
		for _, d := range deps {
			if !base[d.Name()] && !slices.Contains(names, d.Name()) {
				t.Errorf("%s: depends on unknown service %s", n, d.Name())
			}
		}
	}
}
