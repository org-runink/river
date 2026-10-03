// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

package testcmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/org-runink/river/cli/internal/evidence"
	"github.com/org-runink/river/cli/internal/rootcmd"

	// external-profile runs `river lint ...` through its own executable: in a test that is
	// this binary, which TestMain turns into `river` (testcmdAsRiver).
	_ "github.com/org-runink/river/cli/internal/lint"
)

var update = flag.Bool("update", false, "rewrite testdata/golden from the current output")

// testcmdAsRiver makes this test binary act as the river CLI when it is re-executed by a test
// (ExternalProfile runs `<self> lint ...`).
const testcmdAsRiver = "TESTCMD_TEST_BINARY_AS_RIVER"

func TestMain(m *testing.M) {
	if os.Getenv(testcmdAsRiver) == "1" {
		root := rootcmd.New()
		root.SetArgs(os.Args[1:])
		if err := root.ExecuteContext(context.Background()); err != nil {
			fmt.Fprintln(os.Stderr, "river:", err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// sub is one Tier 1 subcommand as `river test` wires it.
type sub struct {
	name     string
	test     Test
	declared []string
	needs    []string // programs it needs on PATH
}

var tier1Subs = []sub{
	{"memtune", Memtune, memtuneChecks, []string{"sh"}},
	{"firstboot-hooks", FirstbootHooks, firstbootChecksDeclared, []string{"sh"}},
	{"external-profile", ExternalProfile, externalProfileChecks, []string{"sh"}},
	{"installed-hooks", InstalledHooks, installedHooksDeclared, []string{"sh"}},
	{"models-required", ModelsRequired, modelsRequiredChecks, []string{"sh"}},
	{"models-fetch", ModelsFetch, modelsFetchChecks, []string{"sh", "curl"}},
}

func need(t *testing.T, progs ...string) {
	t.Helper()
	for _, p := range append([]string{"git"}, progs...) {
		if _, err := exec.LookPath(p); err != nil {
			t.Skipf("no %s", p)
		}
	}
}

// runSub runs one subcommand as `river test` does, with --evidence when withDoc, and returns
// its stdout, stderr, the document (nil without evidence) and its error.
func runSub(t *testing.T, s sub, repo string, withDoc bool) (string, string, *evidence.Document, error) {
	t.Helper()
	if s.name == "external-profile" {
		t.Setenv(testcmdAsRiver, "1")
	}
	var out, errb bytes.Buffer
	opts := evidenceOpts{hostKind: "ci"}
	if withDoc {
		opts.path = filepath.Join(t.TempDir(), s.name+".json")
	}
	err := withEvidence(context.Background(), opts, tier1Harness(s.name), s.declared, repo,
		func(ctx context.Context) error { return s.test(ctx, repo, &out, &errb) })
	if !withDoc {
		return out.String(), errb.String(), nil, err
	}
	return out.String(), errb.String(), readDoc(t, opts.path), err
}

func readDoc(t *testing.T, path string) *evidence.Document {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var d evidence.Document
	if err := json.Unmarshal(b, &d); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	// Every document checked here must also be free of this host's paths, raw.
	for _, p := range hostPaths(t) {
		if strings.Contains(string(b), p) {
			t.Errorf("%s leaks a host path (%s):\n%s", filepath.Base(path), p, b)
		}
	}
	return &d
}

// hostPaths are what must never appear in a document produced on this machine.
func hostPaths(t *testing.T) []string {
	t.Helper()
	ps := []string{repoRoot(t), filepath.Clean(os.TempDir()) + "/"}
	if h, err := os.UserHomeDir(); err == nil && h != "/" {
		ps = append(ps, h)
	}
	return ps
}

var failLine = regexp.MustCompile(`(?m)^  FAIL  `)

// checkDoc asserts what holds for EVERY document: it lints clean, its reported set is exactly
// its declared set, and its failures are the human output's FAIL lines, one for one.
func checkDoc(t *testing.T, s sub, d *evidence.Document, stderr string) {
	t.Helper()
	if d == nil {
		t.Fatalf("%s: no evidence document written", s.name)
	}
	if bad := evidence.Lint(*d); len(bad) > 0 {
		t.Errorf("%s: document does not lint: %v", s.name, bad)
	}
	if d.Schema != evidence.Schema || d.Harness != tier1Harness(s.name) || d.Signing.Signed {
		t.Errorf("%s: schema %q harness %q signed %v", s.name, d.Schema, d.Harness, d.Signing.Signed)
	}
	if d.Subject.Commit == "" || d.HarnessVersion == "" || d.Environment.HostKind != "ci" {
		t.Errorf("%s: subject %+v harness_version %q environment %+v", s.name, d.Subject, d.HarnessVersion, d.Environment)
	}
	want := slices.Clone(s.declared)
	sort.Strings(want)
	if !slices.Equal(d.DeclaredChecks, want) {
		t.Errorf("%s: declared_checks %v, want %v", s.name, d.DeclaredChecks, want)
	}
	var got []string
	for _, c := range d.Checks {
		got = append(got, c.Name)
		if strings.Contains(c.Detail, "undeclared check") || strings.Contains(c.Detail, "never reported") {
			t.Errorf("%s: %s: %s", s.name, c.Name, c.Detail)
		}
		if c.Verdict == evidence.Skip && strings.TrimSpace(c.Detail) == "" {
			t.Errorf("%s: %s skipped without a reason", s.name, c.Name)
		}
	}
	sort.Strings(got)
	if !slices.Equal(got, want) {
		t.Errorf("%s: reported %v\n want exactly the declared %v\n(a new check must be declared in its subcommand's list)", s.name, got, want)
	}
	if n := len(failLine.FindAllString(stderr, -1)); n != d.Totals.Fail {
		t.Errorf("%s: %d FAIL line(s) in the human output, totals.fail = %d", s.name, n, d.Totals.Fail)
	}
}

// Every declared name is a stable machine name: lower-case words joined by dashes, unique in
// its list. The human descriptions embed values; a name must not.
func TestEvidenceDeclaredNamesAreMachineNames(t *testing.T) {
	re := regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)
	lists := map[string][]string{"sddm-theme": sddmThemeChecks}
	for _, s := range tier1Subs {
		lists[s.name] = s.declared
	}
	for name, l := range lists {
		if len(l) == 0 {
			t.Errorf("%s declares nothing", name)
		}
		seen := map[string]bool{}
		for _, n := range l {
			if !re.MatchString(n) {
				t.Errorf("%s: %q is not a machine name", name, n)
			}
			if seen[n] {
				t.Errorf("%s: %q declared twice", name, n)
			}
			seen[n] = true
		}
	}
}

// The whole contract for each subcommand: the human output is byte-identical to the golden
// (captured before evidence existed) with and without --evidence, and the document is valid,
// reports exactly what it declares and passes.
func TestEvidenceEverySubcommand(t *testing.T) {
	root := repoRoot(t)
	for _, s := range tier1Subs {
		t.Run(s.name, func(t *testing.T) {
			need(t, s.needs...)
			out0, err0, _, rerr := runSub(t, s, root, false)
			if rerr != nil {
				t.Fatalf("without evidence: %v\n%s%s", rerr, out0, err0)
			}
			golden := filepath.Join("testdata/golden", s.name+".stdout")
			if *update {
				if err := os.WriteFile(golden, []byte(out0), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if want := read(t, golden); out0 != want {
				t.Errorf("stdout differs from %s (rerun with -update only if the change is intended):\n got: %q\nwant: %q", golden, out0, want)
			}
			out1, err1, d, rerr := runSub(t, s, root, true)
			if rerr != nil {
				t.Fatalf("with evidence: %v\n%s%s", rerr, out1, err1)
			}
			if out1 != out0 || err1 != err0 {
				t.Errorf("--evidence changed the human output:\nstdout %q\n  vs   %q\nstderr %q\n  vs   %q", out1, out0, err1, err0)
			}
			checkDoc(t, s, d, err1)
			if d != nil && (d.Verdict != evidence.Pass || d.Totals.Fail != 0) {
				t.Errorf("verdict %q totals %+v on a passing run", d.Verdict, d.Totals)
			}
		})
	}
}

// A failing check is a failing document, naming the check, and the FAIL count agrees.
func TestEvidenceFailingCheckFailsTheDocument(t *testing.T) {
	need(t, "sh")
	root := repoRoot(t)
	lib := read(t, filepath.Join(root, "installer/lib/memtune.sh"))
	broken := strings.Replace(lib, "MEMTUNE_ARC_MAX_MIB=16384", "MEMTUNE_ARC_MAX_MIB=32768", 1)
	if broken == lib {
		t.Fatal("the fixture no longer matches installer/lib/memtune.sh")
	}
	zram := "iso-profiles/river/root-overlay/usr/local/bin/runink-zram.sh"
	dir := fixtureWith(t, map[string]string{
		"installer/lib/memtune.sh": broken,
		zram:                       read(t, filepath.Join(root, zram)),
	})
	// The fixture is not a checkout; the evidence names the commit of the one it came from.
	if err := os.Symlink(filepath.Join(root, ".git"), filepath.Join(dir, ".git")); err != nil {
		t.Fatal(err)
	}
	s := tier1Subs[0]
	_, stderr, d, err := runSub(t, s, dir, true)
	if err == nil {
		t.Fatal("a broken clamp passed")
	}
	checkDoc(t, s, d, stderr)
	if d.Verdict != evidence.Fail || d.Totals.Fail == 0 {
		t.Fatalf("verdict %q totals %+v, want a failure", d.Verdict, d.Totals)
	}
	var named bool
	for _, c := range d.Checks {
		if c.Name == "arc-ceiling-512g" && c.Verdict == evidence.Fail && strings.Contains(c.Detail, "34359738368") {
			named = true
		}
	}
	if !named {
		t.Errorf("arc-ceiling-512g is not the failure: %+v", d.Checks)
	}
	if strings.Contains(fmt.Sprint(d.Checks), dir) {
		t.Error("the fixture's path leaked into a detail")
	}
}

// The conditional check (a host with a model payload mounted) is a skip with its reason, in
// both medium checks, and the human output keeps its single "ok" line.
func TestEvidenceConditionalSkipCarriesAReason(t *testing.T) {
	need(t, "sh")
	saved := hostHasPayload
	t.Cleanup(func() { hostHasPayload = saved })
	hostHasPayload = func() bool { return true }
	s := tier1Subs[4]
	out, stderr, d, err := runSub(t, s, repoRoot(t), true)
	if err != nil {
		t.Fatalf("%v\n%s%s", err, out, stderr)
	}
	checkDoc(t, s, d, stderr)
	skips := 0
	for _, c := range d.Checks {
		if c.Verdict == evidence.Skip {
			skips++
			if !strings.HasPrefix(c.Name, "medium-") || !strings.Contains(c.Detail, "payload mounted") {
				t.Errorf("unexpected skip %+v", c)
			}
		}
	}
	if skips != 2 || d.Totals.Skip != 2 || d.Verdict != evidence.Pass {
		t.Errorf("skips %d totals %+v verdict %q", skips, d.Totals, d.Verdict)
	}
	if !strings.Contains(out, "  ok    medium search: skipped (this host has a river-models payload mounted)\n") {
		t.Errorf("the human skip line changed:\n%s", out)
	}
}

// A harness that dies before reporting is a failing document that names what never ran; an
// undeclared name fails; a test that errors after every check passed writes NO document.
func TestEvidenceRunsThatDidNotHappen(t *testing.T) {
	need(t)
	root := repoRoot(t)
	declared := []string{"one", "two"}
	write := func(fn func(c *checks) error) (*evidence.Document, error) {
		path := filepath.Join(t.TempDir(), "e.json")
		err := withEvidence(context.Background(), evidenceOpts{path: path}, "river-tier1/probe", declared, root,
			func(ctx context.Context) error { return fn(newChecks(ctx, io.Discard, io.Discard)) })
		return readDoc(t, path), err
	}

	d, err := write(func(c *checks) error { c.ok("one", "first"); return errors.New("died") })
	if err == nil || d == nil || d.Verdict != evidence.Fail || !strings.Contains(fmt.Sprint(d.Checks), "never reported") {
		t.Errorf("a harness that died halfway: err %v doc %+v", err, d)
	}
	d, _ = write(func(c *checks) error { c.ok("one", ""); c.ok("two", ""); c.ok("three", ""); return nil })
	if d == nil || d.Verdict != evidence.Fail || !strings.Contains(fmt.Sprint(d.Checks), "undeclared") {
		t.Errorf("an undeclared check: %+v", d)
	}
	d, err = write(func(c *checks) error { c.ok("one", ""); c.ok("two", ""); return errors.New("late") })
	if err == nil || d != nil {
		t.Errorf("an error after every check passed must write nothing: err %v doc %+v", err, d)
	}
	d, err = write(func(c *checks) error { c.ok("one", ""); c.skipped("x", "", "two"); return nil })
	if err != nil || d == nil || d.Verdict != evidence.Fail {
		t.Errorf("a skip without a reason is a failure: err %v doc %+v", err, d)
	}
}

// Without a commit (not a checkout) the document is refused, and the run exits non-zero.
func TestEvidenceNeedsACommit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "e.json")
	err := withEvidence(context.Background(), evidenceOpts{path: path}, "river-tier1/probe", []string{"one"}, t.TempDir(),
		func(ctx context.Context) error { newChecks(ctx, io.Discard, io.Discard).ok("one", ""); return nil })
	if err == nil || !strings.Contains(err.Error(), "subject.commit") {
		t.Fatalf("err = %v", err)
	}
	if _, serr := os.Stat(path); serr == nil {
		t.Fatal("a document without a commit was written")
	}
}

// Details are redacted, never refused for a path a check happened to mention.
func TestEvidenceRedactsHostPaths(t *testing.T) {
	repo := t.TempDir()
	r := redactor(repo)
	home, _ := os.UserHomeDir()
	for in, want := range map[string]string{
		"missing " + repo + "/build/x.sh":                    "missing <repo>/build/x.sh",
		"in " + filepath.Join(os.TempDir(), "memtune.123/r"): "in <scratch>/r",
		"under " + filepath.Join(home, "src"):                "under ~/src",
		"plain: got '1', want '2'":                           "plain: got '1', want '2'",
	} {
		if got := r(in); got != want {
			t.Errorf("redact(%q) = %q, want %q", in, got, want)
		}
	}
}

// Through the real command line: `river test memtune --evidence FILE`.
func TestEvidenceFlag(t *testing.T) {
	need(t, "sh")
	path := filepath.Join(t.TempDir(), "memtune.json")
	root := rootcmd.New()
	var out, errb bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errb)
	root.SetArgs([]string{"test", "memtune", "--repo", repoRoot(t), "--evidence", path, "--evidence-host-kind", "ci"})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("%v\n%s", err, errb.String())
	}
	if want := read(t, "testdata/golden/memtune.stdout"); out.String() != want {
		t.Errorf("stdout %q, want the golden", out.String())
	}
	checkDoc(t, tier1Subs[0], readDoc(t, path), errb.String())
}

// sddm-theme declares one render per scenario of its table, plus mark-moves.
func TestEvidenceSDDMTheme(t *testing.T) {
	need(t, "sh")
	if len(sddmThemeChecks) != len(sddmShots)+1 {
		t.Fatalf("sddm-theme declares %d, has %d scenarios", len(sddmThemeChecks), len(sddmShots))
	}
	repo := repoRoot(t)
	// A PATH with git and, for the second run, a stand-in renderer; never ImageMagick.
	bin := t.TempDir()
	for _, p := range []string{"git", "sh"} {
		full, _ := exec.LookPath(p)
		if err := os.Symlink(full, filepath.Join(bin, p)); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin)
	run := func() (*evidence.Document, error) {
		path := filepath.Join(t.TempDir(), "sddm.json")
		err := withEvidence(context.Background(), evidenceOpts{path: path}, "river-dev/sddm-theme", sddmThemeChecks, repo,
			func(ctx context.Context) error { return SDDMTheme(ctx, repo, t.TempDir(), io.Discard, io.Discard) })
		return readDoc(t, path), err
	}

	// No renderer: nothing reported, so the document fails and names every scenario.
	d, err := run()
	if err == nil || d == nil || d.Verdict != evidence.Fail || d.Totals.Fail != len(sddmThemeChecks) {
		t.Fatalf("without qml6: err %v doc %+v", err, d)
	}

	// A renderer that draws every scenario cleanly: every render passes, mark-moves is a skip
	// with its reason, and each log is attached by basename.
	if err := writeExec(filepath.Join(bin, "qml6"), `#!/bin/sh
for a; do case "$a" in out=*) printf png > "${a#out=}" ;; esac; done
echo "qml: harness: rendered"
`, 0o755); err != nil {
		t.Fatal(err)
	}
	d, err = run()
	if err != nil || d == nil {
		t.Fatalf("with a renderer: err %v doc %+v", err, d)
	}
	if d.Verdict != evidence.Pass || d.Totals.Pass != len(sddmShots) || d.Totals.Skip != 1 {
		t.Errorf("verdict %q totals %+v", d.Verdict, d.Totals)
	}
	for _, c := range d.Checks {
		if c.Name == "mark-moves" && (c.Verdict != evidence.Skip || !strings.Contains(c.Detail, "magick")) {
			t.Errorf("mark-moves: %+v", c)
		}
	}
	if len(d.Logs) != len(sddmShots) || d.Logs[0].Path != sddmShots[0].name+".log" || d.Logs[0].SHA256 == "" {
		t.Errorf("logs %+v", d.Logs)
	}
}
