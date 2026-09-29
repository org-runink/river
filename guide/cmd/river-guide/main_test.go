// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/org-runink/river/guide/bundle"
	"github.com/org-runink/river/guide/internal/fakegh"
)

// fakeModelServer is an OpenAI-compatible endpoint on the loopback with scripted replies.
func fakeModelServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"default"}]}`))
		case "/v1/chat/completions":
			var req struct {
				Messages []struct{ Content string } `json:"messages"`
			}
			_ = json.NewDecoder(r.Body).Decode(&req)
			q := req.Messages[len(req.Messages)-1].Content
			q = q[strings.LastIndex(q, "Question:"):]
			reply := "NOT IN GUIDE"
			switch {
			case strings.Contains(q, "recovery key"):
				reply = "The installer prints a 64-hex recovery key **once** and never writes it to any file, so record it offline before you continue [river#prepare-to-record-the-recovery-key]."
			case strings.Contains(q, "wipe"):
				reply = "Wipe the disk first with `wipefs -a /dev/nvme0n1`, then run `sudo runink-install` [river#run-the-installer]."
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"role": "assistant", "content": reply}}}})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// scriptReader feeds one line per Read and echoes it into the transcript, so the output
// reads like a real console session. before(line) runs just before a line is delivered.
type scriptReader struct {
	lines  []string
	out    *syncBuf
	before func(string)
}

func (s *scriptReader) Read(p []byte) (int, error) {
	if len(s.lines) == 0 {
		return 0, os.ErrClosed
	}
	l := s.lines[0]
	s.lines = s.lines[1:]
	if s.before != nil {
		s.before(l)
	}
	s.out.WriteString(l + "\n")
	return copy(p, l+"\n"), nil
}

type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}
func (s *syncBuf) WriteString(x string) { s.Write([]byte(x)) }
func (s *syncBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func writeTool(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

// TestConsoleSession is an end-to-end console session: steps, a grounded answer, a
// withheld invented command, the hardware summary, device-flow sign-in, a verified
// private platform bundle fetched at runtime, and the remote channel. Its transcript is
// the sample in docs/INSTALL-GUIDE-AGENT.md.
func TestConsoleSession(t *testing.T) {
	dir := t.TempDir()
	model := fakeModelServer(t)
	// Step commands come from the guide verbatim ("river-hwprobe"), so the fake must be on PATH.
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	gh := fakegh.New("example-org/site-a")
	defer gh.Close()
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	raw, err := bundle.Encode("platform", "2026.09", []bundle.Doc{{Path: "10-platform.md", Content: "# Platform\n\n## Enroll the node\n<!-- river-guide:step id=enroll kind=info -->\nFollow the platform's enrollment procedure.\n"}})
	if err != nil {
		t.Fatal(err)
	}
	gh.Files["guide/platform.bundle.json"] = raw
	gh.Files["guide/platform.bundle.json.sig"] = bundle.Sign(priv, raw)
	os.MkdirAll(filepath.Join(dir, "sources.d"), 0o755)
	os.WriteFile(filepath.Join(dir, "sources.d", "platform.json"),
		[]byte(`{"name":"platform","repo":"example-org/site-a","path":"guide/platform.bundle.json","ref":"main"}`), 0o644)

	probe, _ := filepath.Abs("../../hw/testdata/probe.json")
	hwprobe := writeTool(t, dir, "river-hwprobe", "if [ \"$1\" = --json ]; then cat '"+probe+"'; else echo 'CPU x86-64-v3, 93 GiB, 1 eligible disk'; fi")
	plan := writeTool(t, dir, "river-plan", "echo '{\"schema\":\"river.install-plan/v1\",\"verdict\":\"ok\",\"storage\":{\"layout\":\"single\"}}'")
	// Network first: the guide's first step runs `river-netsetup --check` (read-only).
	writeTool(t, dir, "river-netsetup", "echo 'RESULT: lan-only'")

	out := &syncBuf{}
	in := &scriptReader{out: out, lines: []string{
		"next",
		"approve",
		"next",
		"done live-session",
		"next",
		"approve",
		"next",
		"approve",
		"what happens to the recovery key?",
		"should I wipe the disk first?",
		"hw",
		"login",
		bundle.FormatPublicKey(pub),
		"remote on example-org/site-a",
		"steps",
		"quit",
	}}
	in.before = func(l string) {
		if l == "steps" { // a collaborator asks something while the console works
			gh.AddComment(1, "alice", "COLLABORATOR", "@river_install does river support legacy BIOS boot?")
			time.Sleep(300 * time.Millisecond)
		}
	}
	code := run([]string{
		"--model-url", model.URL + "/v1", "--cmdline", "", "--audit-log", filepath.Join(dir, "audit.jsonl"),
		"--work-dir", filepath.Join(dir, "run"), "--sources", filepath.Join(dir, "sources.d"),
		"--hwprobe", hwprobe, "--plan", plan, "--plan-manifest", filepath.Join(dir, "none.tiers"),
		"--github-api", gh.URL, "--github-web", gh.URL, "--client-id", "Iv1.exampleclient",
		"--device-poll", "10ms", "--poll", "50ms",
	}, in, out, out)
	transcript := out.String()
	t.Logf("console transcript:\n%s", transcript)
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	for _, want := range []string{
		"guide model: ready",
		"no platform guide bundle loaded (1 source(s) configured",
		"Step network: Set up the network first  [check]",
		"step network: done (exit 0)",
		"Step firmware: Check the firmware mode  [check]",
		"step hwprobe: done (exit 0)",
		"[river#prepare-to-record-the-recovery-key]",
		"model answer withheld: it contained a command that is not in the guide",
		"Eligible install disks: 1",
		"ABCD-1234",
		"signed in to GitHub as operator (token held in memory only)",
		"platform guide bundle platform 2026.09 loaded: 1 steps follow the Runink River steps",
		"remote channel on: example-org/site-a#1",
		"remote: @alice (remote) asks: does river support legacy BIOS boot?",
		"pending    enroll",
	} {
		if !strings.Contains(transcript, want) {
			t.Errorf("transcript lacks %q", want)
		}
	}
	if strings.Contains(transcript, "wipefs") && !strings.Contains(transcript, `"wipefs -a /dev/nvme0n1"`) {
		t.Error("the invented command was shown as advice")
	}

	// What left the machine.
	posted := strings.Join(gh.Posted(1), "\n")
	for _, leak := range []string{"S7KHNJ0W123456X", "48:21:0b", "Enroll the node", "enrollment procedure", gh.Token} {
		if strings.Contains(posted, leak) {
			t.Errorf("issue comments leak %q", leak)
		}
	}
	if !strings.Contains(posted, "platform steps 0/1") || !strings.Contains(posted, "[river#") {
		t.Errorf("issue lacks progress or a cited answer:\n%s", posted)
	}

	// The audit log holds actions and citations, never the token or step output.
	audit, _ := os.ReadFile(filepath.Join(dir, "audit.jsonl"))
	if strings.Contains(string(audit), gh.Token) || strings.Contains(string(audit), "x86-64-v3, 93 GiB") {
		t.Error("audit log holds a token or command output")
	}
	for _, want := range []string{`"action":"approve","step":"firmware"`, `"action":"bundle-loaded"`, `"action":"remote-on"`, `"channel":"tag"`} {
		if !strings.Contains(string(audit), want) {
			t.Errorf("audit lacks %s", want)
		}
	}
}

func TestDegradedWhenModelStateSaysSo(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "model.state"), []byte("degraded: 6 GiB RAM, need 8\n"), 0o644)
	out := &syncBuf{}
	in := &scriptReader{out: out, lines: []string{"how do I check every dataset is encrypted?", "quit"}}
	code := run([]string{"--model-state", filepath.Join(dir, "model.state"), "--cmdline", "", "--audit-log", "-",
		"--sources", filepath.Join(dir, "none")}, in, out, &bytes.Buffer{})
	if code != 0 || !strings.Contains(out.String(), "guide model: degraded: 6 GiB RAM") ||
		!strings.Contains(out.String(), "zfs get -r encryption,keystatus zriver") {
		t.Fatalf("exit %d\n%s", code, out.String())
	}
}

func TestCmdlineSettings(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "cmdline")
	os.WriteFile(p, []byte("BOOT_IMAGE=/boot/vmlinuz label=RIVER river.guide.remote=1 river.guide.issue=o/r/5 river.guide.client_id=Iv1.x river.guide.proxy=http://[fd00::1]:3128 river.guide.model=0 river.guide.bundle_key=ed25519:AAAA=\n"), 0o644)
	c, _, err := parseFlags([]string{"--cmdline", p, "--client-id", "Iv1.flag"}, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if !c.remoteOffer || c.tagIssue != "o/r/5" || c.clientID != "Iv1.flag" || c.proxy != "http://[fd00::1]:3128" || !c.noModel || c.bundleKey != "ed25519:AAAA=" {
		t.Fatalf("%+v", c)
	}
	if c.remoteApprovals {
		t.Fatal("the kernel command line must not be able to switch remote approvals on")
	}
}

func TestVerifyBundleCommand(t *testing.T) {
	dir := t.TempDir()
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	raw, _ := bundle.Encode("platform", "1", []bundle.Doc{{Path: "a.md", Content: "## A\ntext\n"}})
	f := filepath.Join(dir, "b.json")
	os.WriteFile(f, raw, 0o644)
	os.WriteFile(f+".sig", bundle.Sign(priv, raw), 0o644)
	var out, errb bytes.Buffer
	if code := run([]string{"verify-bundle", "--pubkey", bundle.FormatPublicKey(pub), f}, nil, &out, &errb); code != 0 || !strings.HasPrefix(out.String(), "OK platform 1") {
		t.Fatalf("%d %s %s", code, out.String(), errb.String())
	}
	other, _, _ := ed25519.GenerateKey(rand.Reader)
	if code := run([]string{"verify-bundle", "--pubkey", bundle.FormatPublicKey(other), f}, nil, &out, &errb); code == 0 {
		t.Fatal("wrong key verified")
	}
}
