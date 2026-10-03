// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

package evidence

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func subj() Subject {
	return Subject{Kind: "pacman-repo", Name: "runink", Commit: "5aff1f21943836fbdd22d6f787ef866622342d9f"}
}
func env() Environment { return Environment{HostKind: "dev-box", Arch: "x86_64"} }

func newRun(declared ...string) *Run {
	return New("test-harness", "0123456789abcdef0123456789abcdef01234567", subj(), env(), declared, nil)
}

// THE RULE THIS PACKAGE EXISTS FOR: a declared check that never reports makes the run fail, and
// the document names which one. A harness that dies halfway, or never runs at all, must not be
// indistinguishable from one that passed -- that happened on 2026-10-03 and is why this is here.
func TestDeclaredButNeverReportedFails(t *testing.T) {
	r := newRun("a", "b", "c")
	r.Pass("a", "")
	r.Pass("b", "")
	d := r.Finish()

	if d.Verdict != Fail {
		t.Errorf("verdict = %q, want fail: check \"c\" never reported", d.Verdict)
	}
	if d.Totals.Fail != 1 {
		t.Errorf("totals.fail = %d, want 1", d.Totals.Fail)
	}
	var named bool
	for _, c := range d.Checks {
		if c.Name == "c" && c.Verdict == Fail && strings.Contains(c.Detail, "never reported") {
			named = true
		}
	}
	if !named {
		t.Error("the missing check is not named in the document; a short count alone does not say WHICH")
	}
}

// A harness that reports nothing at all is the extreme case of the same rule.
func TestNothingReportedIsNotAPass(t *testing.T) {
	d := newRun("a", "b").Finish()
	if d.Verdict != Fail || d.Totals.Fail != 2 {
		t.Errorf("verdict=%q fail=%d, want fail/2 for a harness that never ran", d.Verdict, d.Totals.Fail)
	}
	if d.Totals.Pass != 0 {
		t.Errorf("totals.pass = %d, want 0", d.Totals.Pass)
	}
}

// The consumer recomputes the verdict from declared_checks + checks + totals, so those three
// must agree in anything we emit. This asserts the producer's side of that bargain.
func TestTotalsAndVerdictAgreeWithChecks(t *testing.T) {
	r := newRun("p", "f", "s")
	r.Pass("p", "")
	r.Fail("f", "because")
	r.Skip("s", "a stated reason")
	d := r.Finish()

	if d.Totals != (Totals{Pass: 1, Fail: 1, Skip: 1}) {
		t.Errorf("totals = %+v, want 1/1/1", d.Totals)
	}
	if d.Verdict != Fail {
		t.Errorf("verdict = %q, want fail", d.Verdict)
	}
	// recompute exactly as the consumer will
	var p, f, s int
	seen := map[string]bool{}
	for _, c := range d.Checks {
		seen[c.Name] = true
		switch c.Verdict {
		case Pass:
			p++
		case Fail:
			f++
		case Skip:
			s++
		}
	}
	for _, n := range d.DeclaredChecks {
		if !seen[n] {
			t.Errorf("declared %q absent from checks", n)
		}
	}
	if p != d.Totals.Pass || f != d.Totals.Fail || s != d.Totals.Skip {
		t.Errorf("recomputed %d/%d/%d disagrees with stated %+v", p, f, s, d.Totals)
	}
}

// A skip is never a pass, and an unexplained skip is worse than a failure because it hides.
func TestSkipNeedsAReasonAndIsNotAPass(t *testing.T) {
	r := newRun("quiet", "loud")
	r.Skip("quiet", "")
	r.Skip("loud", "no network on this node")
	d := r.Finish()

	if d.Verdict != Fail {
		t.Errorf("verdict = %q: an unexplained skip must not pass", d.Verdict)
	}
	for _, c := range d.Checks {
		if c.Name == "quiet" && c.Verdict != Fail {
			t.Errorf("unexplained skip recorded as %q, want fail", c.Verdict)
		}
		if c.Name == "loud" && c.Verdict != Skip {
			t.Errorf("explained skip recorded as %q, want skip", c.Verdict)
		}
	}
	// and a run whose only non-pass is an explained skip is still not counted as a pass check
	r2 := newRun("only")
	r2.Skip("only", "stated")
	if d2 := r2.Finish(); d2.Totals.Pass != 0 || d2.Verdict != Pass {
		// no failures, so the run passes -- but the skip is NOT a pass in the totals
		t.Errorf("totals.pass=%d verdict=%q; want pass=0 and verdict=pass (no failures)", d2.Totals.Pass, d2.Verdict)
	}
}

// An undeclared check is a harness bug and is recorded as a failure, not quietly accepted.
// Otherwise a harness could report whatever it happened to run instead of what it promised.
func TestUndeclaredCheckIsAFailure(t *testing.T) {
	r := newRun("declared")
	r.Pass("declared", "")
	r.Pass("surprise", "")
	d := r.Finish()
	if d.Verdict != Fail {
		t.Errorf("verdict = %q, want fail for an undeclared check", d.Verdict)
	}
}

// Each run gets its own id: the consumer refuses a run_id it has already stored, which is how
// the "matched a previous run" failure is closed at that end too. The 200 ids here are generated
// far faster than a millisecond apart, so this also proves the randomness half is doing the work
// and the id is not effectively a timestamp.
func TestRunIDIsUniquePerRun(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 200; i++ {
		id := newRun("a").RunID()
		if id == "" {
			t.Fatal("empty run_id")
		}
		if seen[id] {
			t.Fatalf("duplicate run_id %q", id)
		}
		seen[id] = true
	}
}

// The shape is org-runink-30's validator's requirement: a 26-character Crockford base32 ULID,
// sortable by time. Matching it exactly is cheaper than having them loosen the validator.
func TestRunIDIsAULID(t *testing.T) {
	id := newRun("a").RunID()
	if len(id) != 26 {
		t.Errorf("run_id %q is %d characters, want 26", id, len(id))
	}
	for i, c := range id {
		if !strings.ContainsRune(crockford, c) {
			t.Errorf("run_id %q character %d (%q) is not Crockford base32", id, i, c)
		}
	}
	// Crockford excludes I, L, O and U so a transcribed id cannot be misread.
	if strings.ContainsAny(id, "ILOU") {
		t.Errorf("run_id %q contains an ambiguous character", id)
	}
	// Sortable by time: an id made later must not sort before an earlier one.
	first := newRun("a").RunID()
	time.Sleep(2 * time.Millisecond)
	second := newRun("a").RunID()
	if second < first {
		t.Errorf("ULIDs are not time-sortable: %q (later) sorts before %q (earlier)", second, first)
	}
}

// No agent signs. The document says so, and a document claiming otherwise will not be written.
func TestSigningIsAlwaysFalseAndStated(t *testing.T) {
	d := newRun("a").Finish()
	if d.Signing.Signed {
		t.Error("signing.signed is true in an autonomous run")
	}
	if d.Signing.Reason == "" {
		t.Error("signing.reason is empty: the property must be legible in the published document")
	}
	d.Signing.Signed = true
	if bad := Lint(d); len(bad) == 0 {
		t.Error("Lint accepted a document claiming an agent signed the release")
	}
}

// Evidence is PUBLISHED, so the producer lints it too rather than leaning on CORE's backstop.
// A leak cannot be unpublished -- that happened to this session on 2026-10-03.
func TestLintRefusesUnpublishableFields(t *testing.T) {
	// THE ADDRESS VECTORS ARE ASSEMBLED, NOT WRITTEN AS LITERALS, and that is deliberate.
	// `river lint public-leak` is absolute about RFC1918 and ULA literals in this repository's
	// source: it cannot tell a documentation example from our actual infrastructure. It is right
	// to be absolute -- earlier on 2026-10-03 this very test held our real internal address and
	// it reached the public repository, where a force-push does not unpublish it. So the lint
	// stays strict and these vectors are built from octets, which exercises the regexes without
	// putting an address-shaped literal in a published file.
	for _, c := range []struct{ name, detail string }{
		{"host path", "failed reading /home/" + "me/.cache/river-build/x"},
		{"rfc1918", fmt.Sprintf("dialled %d.%d.%d.%d and got nothing", 10, 20, 30, 40)},
		{"cgnat", fmt.Sprintf("node %d.%d.%d.%d unreachable", 100, 80, 1, 2)},
		{"ula", fmt.Sprintf("bound fd%02x:%04x::1", 0xaa, 0xbb01)},
		{"internal host", "core.svc" + ".cluster.local refused"},
		{"token", "used ghp_abcdefghijklmnopqrstuvwxyz01"},
		{"private key", "-----BEGIN RSA PRIVATE KEY-----"},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := newRun("a")
			r.Fail("a", c.detail)
			if bad := Lint(r.Finish()); len(bad) == 0 {
				t.Errorf("Lint accepted %q", c.detail)
			}
		})
	}
}

func TestLintRequiresProvenanceFields(t *testing.T) {
	for _, c := range []struct {
		name string
		mut  func(*Document)
	}{
		{"no commit", func(d *Document) { d.Subject.Commit = "" }},
		{"no harness_version", func(d *Document) { d.HarnessVersion = "" }},
		{"no run_id", func(d *Document) { d.RunID = "" }},
		{"no declared_checks", func(d *Document) { d.DeclaredChecks = nil }},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := newRun("a")
			r.Pass("a", "")
			d := r.Finish()
			c.mut(&d)
			if bad := Lint(d); len(bad) == 0 {
				t.Errorf("Lint accepted a document with %s", c.name)
			}
		})
	}
}

// A log path must be a relative basename; an absolute one leaks the producing machine's layout.
func TestLogPathsAreBasenames(t *testing.T) {
	r := newRun("a")
	r.Pass("a", "")
	r.AddLog("pacman", "/home/me/.cache/x/pacman.log", "")
	d := r.Finish()
	if got := d.Logs[0].Path; got != "pacman.log" {
		t.Errorf("log path = %q, want the basename", got)
	}
	if bad := Lint(d); len(bad) != 0 {
		t.Errorf("basename log path rejected: %v", bad)
	}
}

// WriteFile is atomic and refuses an unpublishable document rather than writing it.
func TestWriteFileAtomicAndRefusesBadDocuments(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "sub", "evidence.json")

	r := newRun("a")
	r.Pass("a", "fine")
	if err := WriteFile(p, r.Finish()); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	var back Document
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatalf("written document is not valid JSON: %v", err)
	}
	if back.Schema != Schema || back.Verdict != Pass {
		t.Errorf("round-trip lost fields: %+v", back)
	}
	// no temp files left behind
	ents, _ := os.ReadDir(filepath.Dir(p))
	for _, e := range ents {
		if strings.HasPrefix(e.Name(), ".evidence-") {
			t.Errorf("temp file left behind: %s", e.Name())
		}
	}

	bad := newRun("a")
	bad.Fail("a", "see /home/"+"someone/secret")
	if err := WriteFile(filepath.Join(dir, "bad.json"), bad.Finish()); err == nil {
		t.Error("WriteFile wrote a document with a host path in it")
	}
	if _, err := os.Stat(filepath.Join(dir, "bad.json")); !os.IsNotExist(err) {
		t.Error("a refused document was still created on disk")
	}
}

// The contract's closed vocabularies, as org-runink-30's validator enforces them. Checking them
// in the producer means a document the consumer would refuse is never written: a rejection
// arriving from CORE after a long run is a much worse failure to debug.
func TestLintEnforcesTheContractVocabularies(t *testing.T) {
	for _, c := range []struct {
		name string
		mut  func(*Document)
	}{
		{"arch amd64 (Go's name, not the contract's)", func(d *Document) { d.Environment.Arch = "amd64" }},
		{"arch nonsense", func(d *Document) { d.Environment.Arch = "sparc" }},
		{"subject.kind unknown", func(d *Document) { d.Subject.Kind = "tarball" }},
		{"host_kind is a hostname", func(d *Document) { d.Environment.HostKind = "rnk" }},
		{"harness name upper-case", func(d *Document) { d.Harness = "River-Tier1" }},
		{"harness name trailing slash", func(d *Document) { d.Harness = "river-tier1/" }},
		{"harness_version short", func(d *Document) { d.HarnessVersion = "deadbeef" }},
		{"subject.commit short", func(d *Document) { d.Subject.Commit = "5aff1f2" }},
		{"run_id not a ULID", func(d *Document) { d.RunID = "not-a-ulid" }},
		{"run_id uses an excluded letter", func(d *Document) { d.RunID = "0123456789ABCDEFGHIJKMNPQR" }},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := newRun("a")
			r.Pass("a", "")
			d := r.Finish()
			c.mut(&d)
			if bad := Lint(d); len(bad) == 0 {
				t.Errorf("Lint accepted %s", c.name)
			}
		})
	}
}

// A hierarchical harness name is valid: river#25 emits river-tier1/memtune.
func TestHierarchicalHarnessNameIsValid(t *testing.T) {
	r := New("river-tier1/memtune", "0123456789abcdef0123456789abcdef01234567",
		Subject{Kind: "source", Name: "river", Commit: "0123456789abcdef0123456789abcdef01234567"},
		Environment{HostKind: "ci", Arch: "aarch64"}, []string{"a"}, nil)
	r.Pass("a", "")
	if bad := Lint(r.Finish()); len(bad) != 0 {
		t.Errorf("a valid hierarchical name and the source kind were rejected: %v", bad)
	}
}

// GOARCH is mapped, not passed through.
func TestGoArchIsMappedToTheContractNames(t *testing.T) {
	got := goArch()
	if !validArch[got] {
		t.Errorf("goArch() = %q, which the consumer refuses", got)
	}
}
