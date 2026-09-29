// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

package wizard

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func mustAddr(s string) *net.TCPAddr {
	a, err := net.ResolveTCPAddr("tcp", s)
	if err != nil {
		panic(err)
	}
	return a
}

func writePage(t *testing.T, dir, hook, body string, mode os.FileMode) {
	t.Helper()
	d := filepath.Join(dir, hook)
	if err := os.MkdirAll(d, 0o700); err != nil {
		t.Fatal(err)
	}
	os.Chmod(d, 0o700)
	if err := os.WriteFile(filepath.Join(d, "setup.json"), []byte(body), mode); err != nil {
		t.Fatal(err)
	}
	os.Chmod(filepath.Join(d, "setup.json"), mode)
}

const token = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func TestFirstbootPages(t *testing.T) {
	dir := t.TempDir()
	fs := &FakeFirstboot{Dir: dir, HookList: []string{"10-a", "20-b", "30-bad", "40-mode", "50-none"},
		Status: map[string]string{"10-a": "registered 0", "20-b": "registered 0", "30-bad": "registered 0",
			"40-mode": "registered 0"}, Done: map[string]bool{"50-none": true}}
	writePage(t, dir, "10-a", `{"version":1,"title":"Second","url":"http://[::1]:8420/?token=`+token+`","order":60}`, 0o600)
	writePage(t, dir, "20-b", `{"version":1,"title":"First","url":"http://127.0.0.1:8421/","order":10}`, 0o600)
	writePage(t, dir, "30-bad", `{"version":1,"title":"Evil","url":"http://example.org/","order":1}`, 0o600)
	writePage(t, dir, "40-mode", `{"version":1,"title":"Loose","url":"http://[::1]:1/","order":2}`, 0o644)
	f := NewFirstboot(fs, "Runink River Server", "en", nil)
	f.Run(ctxTimeout(t, 1)) // checks settle at once with Delay 0; Run returns when ctx ends

	v := f.View()
	if v.Screen != "page" || v.Current != "20-b" {
		t.Fatalf("first page: screen %s current %s", v.Screen, v.Current)
	}
	byName := map[string]PageView{}
	for _, p := range v.Pages {
		byName[p.Name] = p
	}
	if byName["30-bad"].Error != "url host" || byName["40-mode"].Error != "file mode" {
		t.Fatalf("refusals: %+v", v.Pages)
	}
	b, _ := json.Marshal(v)
	if strings.Contains(string(b), token) || strings.Contains(string(b), "8420") {
		t.Fatal("the view carries a page URL")
	}
	if u, _ := f.PageURL("10-a"); !strings.Contains(u, token) {
		t.Fatal("PageURL lost the token")
	}
	if _, err := f.PageURL("30-bad"); err == nil {
		t.Fatal("a refused page's URL was served")
	}
	// The first page finishes (its dir disappears when the runner retires it); the next one.
	os.RemoveAll(filepath.Join(dir, "20-b"))
	if v := f.View(); v.Current != "10-a" {
		t.Fatalf("second page: %s", v.Current)
	}
	f.SkipPage("10-a")
	v = f.View()
	if v.Screen != "ready" || v.Ready == nil || v.Ready.Title != "Runink River Server" {
		t.Fatalf("after skip: %s %+v", v.Screen, v.Ready)
	}
	if err := f.Finish(context.Background()); err != nil {
		t.Fatal(err)
	}
	time.Sleep(2500 * time.Millisecond)
	if !fs.IsFinished() || f.View().Screen != "finished" {
		t.Fatal("finish did not run")
	}
}

func TestFirstbootFailedHookRetry(t *testing.T) {
	fs := &FakeFirstboot{Dir: t.TempDir(), HookList: []string{"10-x"}, Status: map[string]string{"10-x": "failed 3"}}
	f := NewFirstboot(fs, "T", "en", nil)
	f.Run(ctxTimeout(t, 1))
	v := f.View()
	if v.Hooks[0].Outcome != "failed" || v.Hooks[0].RC != 3 {
		t.Fatalf("hook: %+v", v.Hooks)
	}
	if err := f.Retry(); err != nil || fs.retries() != 1 {
		t.Fatal("retry")
	}
	// A deferred hook is not "busy": the first boot can end at ready.
	fs.setStatus("10-x", "deferred 75")
	if s := f.View().Screen; s != "ready" {
		t.Fatalf("with a deferred hook: %s", s)
	}
	// A running hook keeps the settle screen.
	fs.setStatus("10-x", "running 0")
	if s := f.View().Screen; s != "settle" {
		t.Fatalf("with a running hook: %s", s)
	}
}

func TestCheckPageURL(t *testing.T) {
	for u, ok := range map[string]bool{
		"http://[::1]:8420/setup?token=x": true, "http://127.0.0.1/": true,
		"https://[::1]:8420/": false, "http://localhost:1/": false, "http://user@[::1]:1/": false,
		"http://[::1]:1/#frag": false, "http://192.0.2.1/": false, "javascript:alert(1)": false,
		"http://[::1]:99999/": false,
	} {
		if err := checkPageURL(u); (err == nil) != ok {
			t.Errorf("%s: %v", u, err)
		}
	}
}

func ctxTimeout(t *testing.T, s int) context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(s)*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func TestEditionValidate(t *testing.T) {
	good := DemoEditions()[0]
	if err := good.Validate(); err != nil {
		t.Fatal(err)
	}
	bad := []func(e *Edition){
		func(e *Edition) { e.Version = 2 },
		func(e *Edition) { e.ID = "Bad ID" },
		func(e *Edition) { e.PlanProfile = "laptop" },
		func(e *Edition) { e.Steps = []string{"../../etc/passwd"} },
		func(e *Edition) { e.Hostname = "Not_OK" },
		func(e *Edition) {
			e.Roles = []Role{{ID: "a", Title: map[string]string{"en": "A"}}}
			e.DefaultRole = "b"
		},
	}
	for i, mut := range bad {
		e := good
		mut(&e)
		if e.Validate() == nil {
			t.Errorf("bad descriptor %d validated", i)
		}
	}
	dir := t.TempDir()
	b, _ := json.Marshal(good)
	os.WriteFile(filepath.Join(dir, "server.json"), b, 0o644)
	os.WriteFile(filepath.Join(dir, "broken.json"), []byte(`{"version":1,"id":"x","surprise":1}`), 0o644)
	w := DemoEditions()[1]
	b, _ = json.Marshal(w)
	os.WriteFile(filepath.Join(dir, "workstation.json"), b, 0o644)
	eds, skipped := LoadEditions(dir, "RIVER", func(l string) bool { return l == "RUNINK_WORKSTATION" })
	if len(eds) != 2 || len(skipped) != 1 || !eds[0].Self || !eds[1].Present || eds[1].Self {
		t.Fatalf("load: %+v skipped %v", eds, skipped)
	}

	// One medium, two editions under one label: river.edition= picks the running one, and the
	// other is present (it is on the same medium) without any volume lookup.
	one := t.TempDir()
	for _, e := range []Edition{good, w} {
		e.MediumLabel = "EXAMPLE"
		b, _ = json.Marshal(e)
		os.WriteFile(filepath.Join(one, e.ID+".json"), b, 0o644)
	}
	for _, self := range []string{good.ID, w.ID} {
		eds, skipped = LoadEditionsFor(one, "EXAMPLE", self, nil)
		if len(eds) != 2 || len(skipped) != 0 {
			t.Fatalf("one medium: %+v skipped %v", eds, skipped)
		}
		n := 0
		for _, e := range eds {
			if e.Self {
				n++
				if e.ID != self {
					t.Errorf("river.edition=%s: %s is self", self, e.ID)
				}
			}
			if !e.Present {
				t.Errorf("river.edition=%s: %s not present", self, e.ID)
			}
		}
		if n != 1 || selfEdition(eds).ID != self {
			t.Errorf("river.edition=%s: %d self, selfEdition %s", self, n, selfEdition(eds).ID)
		}
	}
	// An edition id that is not on the medium makes nothing self.
	if eds, _ = LoadEditionsFor(one, "EXAMPLE", "desktop", nil); eds[0].Self || eds[1].Self {
		t.Errorf("unknown river.edition matched: %+v", eds)
	}
}

// The repository's descriptors must load (they ship on the live media).
func TestRepoDescriptors(t *testing.T) {
	files, _ := filepath.Glob("../../../iso-profiles/*/live-overlay/usr/share/river/installer/editions/*.json")
	if len(files) == 0 {
		t.Fatal("no edition descriptors in iso-profiles")
	}
	for _, f := range files {
		eds, skipped := LoadEditions(filepath.Dir(f), "", nil)
		if len(skipped) > 0 || len(eds) == 0 {
			t.Errorf("%s: %v", f, skipped)
		}
		lib, _ := filepath.Glob("../../lib/*.sh")
		have := map[string]bool{}
		for _, l := range lib {
			have[strings.TrimSuffix(filepath.Base(l), ".sh")] = true
		}
		for _, e := range eds {
			for _, s := range e.Steps {
				overlay := filepath.Join(filepath.Dir(f), "../../../../../../root-overlay/usr/local/lib/runink-install", s+".sh")
				if !have[s] {
					if _, err := os.Stat(overlay); err != nil {
						t.Errorf("%s: step %s exists nowhere", f, s)
					}
				}
			}
		}
	}
}
