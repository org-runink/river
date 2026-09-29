// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

package lint

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const leakAllowFixture = `# comment
* | scripts/public-leak.allow | * | this file
email | docs/credits.md | someone@upstream-wallpapers.net | not used by default
`

// leakRepo builds a git repository the lint passes: 110 filler files, words that only look
// like product names, the project aliases, and an allow-list whose entries are all used.
func leakRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	for i := 0; i < 110; i++ {
		writeLeak(t, dir, fmt.Sprintf("filler/f%03d.txt", i), "Runink River\n")
	}
	writeLeak(t, dir, "docs/words.md", strings.Join([]string{
		"@font-face{font-family:x} and the puppy's face; a forged request; pipewire-pulse;",
		"Hugging Face; CONFIG_SND_SOC_SOF_LUNARLAKE=m; CONFIG_ZORBTECH_ACPI=m; the core of the kernel; runink-core package.",
		"Report to security@runink.org or conduct@runink.org; example: alice@example.org.",
		"Documentation addresses 192.0.2.1 and 2001:db8::1 are fine; so is org-runink/river.",
	}, "\n")+"\n")
	writeLeak(t, dir, "docs/credits.md", "Wallpaper by someone@upstream-wallpapers.net\n")
	writeLeak(t, dir, publicLeakAllowFile, leakAllowFixture)
	writeLeak(t, dir, "art.png", "\x89PNG\x00 ZORBLAX inside a binary is not scanned\n")
	gitLeak(t, dir, "init", "-q")
	gitLeak(t, dir, "add", "-A")
	return dir
}

// withLeakTerms adds made-up terms to the digest sets for one test, so the tests exercise the
// real matching without naming a real product, repository, label or machine.
func withLeakTerms(t *testing.T) {
	t.Helper()
	adds := []struct {
		set  map[string]bool
		term string
	}{
		{leakProductWords, "ZORBLAX"},
		{leakProductNames, "zorblax"},
		{leakPrivateRepos, "zorb-private"},
		{leakRunnerLabels, "zorb-egress"},
		{leakTargetHW, "zorbtech"},
	}
	for _, a := range adds {
		a.set[leakDigest(a.term)] = true
	}
	t.Cleanup(func() {
		for _, a := range adds {
			delete(a.set, leakDigest(a.term))
		}
	})
}

func writeLeak(t *testing.T, dir, name, body string) {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func gitLeak(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=Test", "-c", "user.email=test@example.org", "-c", "commit.gpgsign=false"}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func runLeak(t *testing.T, dir string) (error, string, string) {
	t.Helper()
	var errb, outb bytes.Buffer
	err := PublicLeak(context.Background(), dir, &errb, &outb)
	return err, errb.String(), outb.String()
}

func TestPublicLeakClean(t *testing.T) {
	withLeakTerms(t)
	dir := leakRepo(t)
	err, errs, out := runLeak(t, dir)
	if err != nil {
		t.Fatalf("clean repo failed: %v\n%s", err, errs)
	}
	if !strings.Contains(out, "lint-public-leak: OK (") {
		t.Fatalf("unexpected OK line: %q", out)
	}
}

func TestPublicLeakFlagsEachRule(t *testing.T) {
	cases := map[string]struct{ line, rule string }{
		"product":       {"ships beside ZORBLAX", "product"},
		"product-space": {"the Runink Zorblax console", "product"},
		"host":          {"curl https://zorblax.runink.org/healthz", "product-host"},
		"lan-host":      {"ssh node.runink.lan", "product-host"},
		"repo":          {"see github.com/org-runink/zorb-private/pull/1", "private-repo"},
		"lan":           {"the box at 10.1.2.3", "internal-ip"},
		"tunnel":        {"tunnel 2001:470:1d:4c9::1/64", "internal-ip"},
		"ula":           {"cluster at fd42:dead:beef::/48", "ula"},
		"runner":        {"runs-on: [self-hosted, zorb-egress]", "runner-label"},
		"label":         {"the job needs zorb-egress", "runner-label"},
		"hardware":      {"sized on the ZorbTech box", "target-hw"},
		"server":        {"the runink-server ISO", "server-name"},
		"email":         {"mail someone@runink.org", "email"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			withLeakTerms(t)
			dir := leakRepo(t)
			writeLeak(t, dir, "docs/leak.md", "intro\n"+c.line+"\n")
			gitLeak(t, dir, "add", "-A")
			err, errs, _ := runLeak(t, dir)
			if err == nil {
				t.Fatalf("%q passed", c.line)
			}
			if !strings.Contains(errs, "docs/leak.md:2: "+c.rule+" ") {
				t.Fatalf("report does not name file, line and rule %s:\n%s", c.rule, errs)
			}
		})
	}
}

func TestPublicLeakAllowList(t *testing.T) {
	withLeakTerms(t)
	dir := leakRepo(t)
	writeLeak(t, dir, "docs/leak.md", "tested on a ZorbTech laptop\n")
	writeLeak(t, dir, publicLeakAllowFile, leakAllowFixture+"target-hw | docs/*.md | zorbtech | a hardware compatibility note\n")
	gitLeak(t, dir, "add", "-A")
	if err, errs, _ := runLeak(t, dir); err != nil {
		t.Fatalf("an allowed hit failed: %v\n%s", err, errs)
	}

	// The same text in a path the entry does not cover fails.
	writeLeak(t, dir, "src/other.txt", "ZorbTech\n")
	gitLeak(t, dir, "add", "-A")
	if err, errs, _ := runLeak(t, dir); err == nil || !strings.Contains(errs, "src/other.txt:1: target-hw") {
		t.Fatalf("a hit outside the allowed path passed:\n%s", errs)
	}
}

func TestPublicLeakStaleAllowEntryFails(t *testing.T) {
	withLeakTerms(t)
	dir := leakRepo(t)
	writeLeak(t, dir, publicLeakAllowFile, leakAllowFixture+"product | docs/gone.md | ZORBLAX | the file was reworded\n")
	gitLeak(t, dir, "add", "-A")
	err, errs, _ := runLeak(t, dir)
	if err == nil || !strings.Contains(errs, `matches nothing any more`) {
		t.Fatalf("a stale allow entry passed: %v\n%s", err, errs)
	}
}

func TestPublicLeakAllowFileNeedsReasons(t *testing.T) {
	withLeakTerms(t)
	dir := leakRepo(t)
	for body, want := range map[string]string{
		"product | docs/x.md | ZORBLAX |\n":         "is empty",
		"product | docs/x.md | ZORBLAX\n":           "want `rule | path-glob | match | reason`",
		"nosuchrule | docs/x.md | ZORBLAX | why\n":  "unknown rule",
		"# only a comment\n":                        "no entries",
		"product | docs/[x.md | ZORBLAX | a glob\n": "bad glob",
	} {
		writeLeak(t, dir, publicLeakAllowFile, body)
		err, _, _ := runLeak(t, dir)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("allow file %q: want error containing %q, got %v", body, want, err)
		}
	}
}

func TestPublicLeakRefusesATinyRepo(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	writeLeak(t, dir, publicLeakAllowFile, leakAllowFixture)
	gitLeak(t, dir, "init", "-q")
	gitLeak(t, dir, "add", "-A")
	if err, _, _ := runLeak(t, dir); err == nil || !strings.Contains(err.Error(), "not looking at this repository") {
		t.Fatalf("a near-empty repository passed: %v", err)
	}
}

func TestPublicLeakHistory(t *testing.T) {
	withLeakTerms(t)
	dir := leakRepo(t)
	gitLeak(t, dir, "commit", "-q", "-m", "clean start")
	var errb, outb bytes.Buffer
	if err := PublicLeakHistory(context.Background(), dir, &errb, &outb); err != nil {
		t.Fatalf("clean history failed: %v\n%s", err, errb.String())
	}
	if !strings.Contains(outb.String(), "OK (1 commits)") {
		t.Fatalf("unexpected OK line: %q", outb.String())
	}

	// A leak added and then removed: the tree is clean, the history is not.
	writeLeak(t, dir, "docs/deploy.md", "curl --resolve zorblax.runink.org:443:10.1.2.3\n")
	gitLeak(t, dir, "add", "-A")
	gitLeak(t, dir, "commit", "-q", "-m", "deploy notes for ZORBLAX")
	if err := os.Remove(filepath.Join(dir, "docs/deploy.md")); err != nil {
		t.Fatal(err)
	}
	gitLeak(t, dir, "add", "-A")
	gitLeak(t, dir, "commit", "-q", "-m", "remove the notes")
	if err, errs, _ := runLeak(t, dir); err != nil {
		t.Fatalf("tree should be clean: %v\n%s", err, errs)
	}
	errb.Reset()
	err := PublicLeakHistory(context.Background(), dir, &errb, &bytes.Buffer{})
	if err == nil {
		t.Fatal("history with a removed leak passed")
	}
	for _, want := range []string{
		` docs/deploy.md: product-host "zorblax.runink.org"`,
		` docs/deploy.md: internal-ip "10.1.2.3"`,
		` (message): product "zorblax"`,
	} {
		if !strings.Contains(errb.String(), want) {
			t.Errorf("history report lacks %q:\n%s", want, errb.String())
		}
	}
}
