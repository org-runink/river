// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

// Package evidence writes the release-evidence document a harness produces: the machine-readable
// result of one autonomous test run, for a CORE DevEx/DataEx capability to collect, store and
// publish (the contract agreed with org-runink-30 on 2026-10-03).
//
// WHY THIS EXISTS. Every release check used to end in a human reading prose -- a screenshot
// somebody eyeballs, an ok/FAIL line in a terminal, a hand install the owner performs. An agent
// cannot consume that, and it does not survive as evidence. Three failures on 2026-10-03 set the
// rules below: a wrapper's `exit=0` hid a real exit 1 and the failure was reported as success; a
// watcher matched a terminal line from a PREVIOUS run in an append-only log and called a running
// build finished; and a harness that had never once run was, from the outside, indistinguishable
// from one that passed.
//
// So the invariant is: A RUN THAT DID NOT HAPPEN MUST NEVER LOOK LIKE A PASS.
//
// A Run therefore DECLARES its check names up front, and those names ship in the document. The
// verdict is a pass only when nothing failed AND every declared check actually reported. The
// consumer recomputes that from the document and refuses any whose verdict or totals disagree,
// so this package being buggy or lying is caught rather than believed -- the producer is not
// trusted (org-runink-30's amendment 1, which is better than the first draft's "the producer
// honours the rule").
//
// NOTHING HERE SIGNS. The release key is the owner's and offline. Signing.Signed is always false
// in an autonomous run and the reason is stated in the document. A harness that could sign would
// be a bug, not a feature.
package evidence

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"
)

// Schema is the document's version. The consumer refuses a schema it does not know, so this
// changes only when a field's meaning changes.
const Schema = "runink.release-evidence/1"

// Verdicts. Skip is NEVER a pass: it must carry a reason, and a release gate may refuse one.
const (
	Pass = "pass"
	Fail = "fail"
	Skip = "skip"
)

// Subject is what was tested. Commit is required: evidence that cannot be tied to a commit
// cannot be re-derived, and a SHA only reaches an artifact through a pin plus a fresh build.
type Subject struct {
	Kind           string `json:"kind"` // pacman-repo | iso | image
	Name           string `json:"name"`
	Version        string `json:"version,omitempty"`
	Commit         string `json:"commit"`
	ArtifactSHA256 string `json:"artifact_sha256,omitempty"`
	ArtifactPath   string `json:"artifact_path,omitempty"` // a relative basename, never a host path
}

// Environment describes WHERE the run happened, in categories rather than identities. HostKind
// is "server-node", "dev-box" or "ci" -- never a hostname, because this document is published.
type Environment struct {
	HostKind       string `json:"host_kind"`
	Arch           string `json:"arch"`
	Runner         string `json:"runner,omitempty"`
	ContainerImage string `json:"container_image,omitempty"`
	KVM            bool   `json:"kvm"`
}

// Check is one reported result.
type Check struct {
	Name    string `json:"name"`
	Verdict string `json:"verdict"`
	Detail  string `json:"detail,omitempty"`
}

// Totals are recomputed on Finish; they are never accumulated by hand.
type Totals struct {
	Pass int `json:"pass"`
	Fail int `json:"fail"`
	Skip int `json:"skip"`
}

// Signing records that no agent signed anything. It exists to make that property legible in the
// published artifact rather than merely true in the code.
type Signing struct {
	Signed bool   `json:"signed"`
	Reason string `json:"reason"`
}

// Log is a file the run produced, named by a RELATIVE basename. An absolute path would leak the
// producing machine's layout into a public document.
type Log struct {
	Name   string `json:"name"`
	SHA256 string `json:"sha256,omitempty"`
	Path   string `json:"path"`
}

// Document is the published shape. Field order here is the JSON order.
type Document struct {
	Schema         string      `json:"schema"`
	Harness        string      `json:"harness"`
	HarnessVersion string      `json:"harness_version"`
	RunID          string      `json:"run_id"`
	Verdict        string      `json:"verdict"`
	StartedAt      string      `json:"started_at"`
	EndedAt        string      `json:"ended_at"`
	DeclaredChecks []string    `json:"declared_checks"`
	Subject        Subject     `json:"subject"`
	Environment    Environment `json:"environment"`
	Checks         []Check     `json:"checks"`
	Totals         Totals      `json:"totals"`
	Signing        Signing     `json:"signing"`
	Logs           []Log       `json:"logs,omitempty"`
}

// Run accumulates one harness run.
type Run struct {
	doc      Document
	declared map[string]bool
	seen     map[string]bool
	out      io.Writer // human progress, so a terminal reader still sees what an agent reads
}

// New starts a run. declared is every check this harness intends to report; a declared check
// that never reports makes the run FAIL, which is the whole point -- it is how "the harness
// never ran" and "the harness died halfway" stop looking like success.
func New(harness, harnessVersion string, subj Subject, env Environment, declared []string, out io.Writer) *Run {
	if env.Arch == "" {
		env.Arch = goArch()
	}
	d := append([]string(nil), declared...)
	sort.Strings(d)
	r := &Run{
		doc: Document{
			Schema:         Schema,
			Harness:        harness,
			HarnessVersion: harnessVersion,
			RunID:          newRunID(),
			StartedAt:      time.Now().UTC().Format(time.RFC3339),
			DeclaredChecks: d,
			Subject:        subj,
			Environment:    env,
			Signing: Signing{
				Signed: false,
				Reason: "the release key is the owner's, offline; no agent signs, by design",
			},
		},
		declared: map[string]bool{},
		seen:     map[string]bool{},
		out:      out,
	}
	for _, n := range d {
		r.declared[n] = true
	}
	return r
}

// RunID is this run's unique id, so a caller can log it next to its own output.
func (r *Run) RunID() string { return r.doc.RunID }

func (r *Run) record(name, verdict, detail string) {
	if !r.declared[name] {
		// An undeclared check is a programming error in the harness, not a result. Recording it
		// as a failure is deliberate: silently accepting it would let a harness report whatever
		// it happened to run instead of what it promised to run.
		verdict, detail = Fail, "undeclared check (harness bug): "+detail
	}
	r.seen[name] = true
	r.doc.Checks = append(r.doc.Checks, Check{Name: name, Verdict: verdict, Detail: detail})
	if r.out != nil {
		tag := map[string]string{Pass: "ok   ", Fail: "FAIL ", Skip: "skip "}[verdict]
		fmt.Fprintf(r.out, "  %s %s%s\n", tag, name, detailSuffix(detail))
	}
}

func detailSuffix(d string) string {
	if d == "" {
		return ""
	}
	return " — " + d
}

// Pass, Fail and Skip report one check. Skip REQUIRES a reason; an unexplained skip is recorded
// as a failure, because "skipped" with no reason is indistinguishable from "quietly broken".
func (r *Run) Pass(name, detail string) { r.record(name, Pass, detail) }
func (r *Run) Fail(name, detail string) { r.record(name, Fail, detail) }
func (r *Run) Skip(name, reason string) {
	if strings.TrimSpace(reason) == "" {
		r.record(name, Fail, "skipped with no reason given")
		return
	}
	r.record(name, Skip, reason)
}

// Expect is Pass when cond holds, else Fail with the same detail.
func (r *Run) Expect(name string, cond bool, detail string) {
	if cond {
		r.Pass(name, detail)
		return
	}
	r.Fail(name, detail)
}

// AddLog attaches a produced file by relative basename.
func (r *Run) AddLog(name, path, sha string) {
	r.doc.Logs = append(r.doc.Logs, Log{Name: name, Path: filepath.Base(path), SHA256: sha})
}

// Finish computes the totals and the verdict and returns the document. Any declared check that
// never reported is appended as a failure first, naming itself, so the document says WHICH check
// is missing rather than only that the count is short.
func (r *Run) Finish() Document {
	var missing []string
	for n := range r.declared {
		if !r.seen[n] {
			missing = append(missing, n)
		}
	}
	sort.Strings(missing)
	for _, n := range missing {
		r.doc.Checks = append(r.doc.Checks, Check{
			Name: n, Verdict: Fail,
			Detail: "declared but never reported: the harness did not run this check (a run that did not happen is not a pass)",
		})
		if r.out != nil {
			fmt.Fprintf(r.out, "  FAIL  %s — declared but never reported\n", n)
		}
	}
	r.doc.Totals = Totals{}
	for _, c := range r.doc.Checks {
		switch c.Verdict {
		case Pass:
			r.doc.Totals.Pass++
		case Fail:
			r.doc.Totals.Fail++
		case Skip:
			r.doc.Totals.Skip++
		}
	}
	r.doc.Verdict = Pass
	if r.doc.Totals.Fail > 0 {
		r.doc.Verdict = Fail
	}
	r.doc.EndedAt = time.Now().UTC().Format(time.RFC3339)
	return r.doc
}

// publicField catches what must never reach a published document. CORE enforces this again
// before storing and before publishing; duplicating it here is deliberate -- a producer that
// relies on someone else's validator to keep host paths out of public evidence is one refactor
// away from leaking, and a leak cannot be unpublished.
var publicField = []struct {
	re  *regexp.Regexp
	why string
}{
	{regexp.MustCompile(`(?i)(^|[\s"'=:(])/(home|root|var/lib|etc|srv|mnt|run)/`), "an absolute host path"},
	{regexp.MustCompile(`\b10\.\d{1,3}\.\d{1,3}\.\d{1,3}\b`), "an RFC1918 address"},
	{regexp.MustCompile(`\b192\.168\.\d{1,3}\.\d{1,3}\b`), "an RFC1918 address"},
	{regexp.MustCompile(`\b172\.(1[6-9]|2\d|3[01])\.\d{1,3}\.\d{1,3}\b`), "an RFC1918 address"},
	{regexp.MustCompile(`\b100\.(6[4-9]|[7-9]\d|1[01]\d|12[0-7])\.\d{1,3}\.\d{1,3}\b`), "a CGNAT/Tailscale address"},
	{regexp.MustCompile(`\b169\.254\.\d{1,3}\.\d{1,3}\b`), "a link-local address"},
	{regexp.MustCompile(`(?i)\bfd[0-9a-f]{2}:`), "a ULA address"},
	{regexp.MustCompile(`(?i)\.(ts\.net|lan|svc\.cluster\.local)\b`), "an internal hostname"},
	{regexp.MustCompile(`(?i)\b(gh[pousr]_[A-Za-z0-9]{16,}|AKIA[0-9A-Z]{12,}|xox[bapr]-[A-Za-z0-9-]{10,})`), "a token"},
	{regexp.MustCompile(`(?i)-----BEGIN [A-Z ]*PRIVATE KEY-----`), "a private key"},
}

// Lint reports every public-field violation in the document, naming what and why. It is run
// before writing, and a violation refuses the write.
func Lint(d Document) []string {
	b, err := json.Marshal(d)
	if err != nil {
		return []string{fmt.Sprintf("document will not marshal: %v", err)}
	}
	s := string(b)
	var bad []string
	for _, p := range publicField {
		if m := p.re.FindString(s); m != "" {
			bad = append(bad, fmt.Sprintf("%s in the document (%q)", p.why, truncate(m, 48)))
		}
	}
	for _, l := range d.Logs {
		if l.Path != filepath.Base(l.Path) || strings.HasPrefix(l.Path, "/") {
			bad = append(bad, fmt.Sprintf("logs[%q].path is not a relative basename: %q", l.Name, l.Path))
		}
	}
	if d.Subject.Commit == "" {
		bad = append(bad, "subject.commit is required")
	}
	if d.HarnessVersion == "" {
		bad = append(bad, "harness_version is required")
	}
	if d.RunID == "" {
		bad = append(bad, "run_id is required")
	}
	if len(d.DeclaredChecks) == 0 {
		bad = append(bad, "declared_checks is required (a harness that declares nothing cannot be held to anything)")
	}
	if d.Signing.Signed {
		bad = append(bad, "signing.signed is true: no agent signs a release, so this is a bug")
	}
	// The closed vocabularies. Checking them here means a document the consumer would refuse is
	// never written in the first place, which is a much easier failure to act on than a
	// rejection arriving from CORE after a long run.
	if !validArch[d.Environment.Arch] {
		bad = append(bad, fmt.Sprintf("environment.arch %q is not x86_64 or aarch64 (Go's GOARCH names are not the contract's)", d.Environment.Arch))
	}
	if !validSubjectKnd[d.Subject.Kind] {
		bad = append(bad, fmt.Sprintf("subject.kind %q is not one of source, pacman-repo, iso, image", d.Subject.Kind))
	}
	if !validHostKind[d.Environment.HostKind] {
		bad = append(bad, fmt.Sprintf("environment.host_kind %q is not one of server-node, dev-box, ci (and must never be a hostname)", d.Environment.HostKind))
	}
	if !harnessName.MatchString(d.Harness) {
		bad = append(bad, fmt.Sprintf("harness %q is not lower-case dash/slash segments", d.Harness))
	}
	if len(d.Harness) > 128 {
		bad = append(bad, "harness name is longer than 128 characters")
	}
	if !hex40.MatchString(d.HarnessVersion) {
		bad = append(bad, fmt.Sprintf("harness_version %q is not a 40-character hex commit", d.HarnessVersion))
	}
	if !hex40.MatchString(d.Subject.Commit) {
		bad = append(bad, fmt.Sprintf("subject.commit %q is not a 40-character hex commit", d.Subject.Commit))
	}
	if !ulid26.MatchString(d.RunID) {
		bad = append(bad, fmt.Sprintf("run_id %q is not a 26-character Crockford base32 ULID", d.RunID))
	}
	return bad
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// WriteFile lints the document and writes it atomically (temp + rename in the same directory),
// so a collector never reads a half-written file -- the same reason the contract requires it.
func WriteFile(path string, d Document) error {
	if bad := Lint(d); len(bad) > 0 {
		return fmt.Errorf("refusing to write evidence that is not publishable:\n  %s", strings.Join(bad, "\n  "))
	}
	b, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".evidence-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer func() { _ = os.Remove(tmp) }()
	if _, err := f.Write(b); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// goArch maps Go's GOARCH onto the contract's architecture enum. org-runink-30's validator
// accepts {x86_64, aarch64} and REFUSES "amd64", so emitting runtime.GOARCH raw would have every
// document this produces rejected at the consumer. Mapping here rather than asking them to widen
// the enum: the published vocabulary should be the platform's, not the Go toolchain's.
func goArch() string {
	switch runtime.GOARCH {
	case "amd64":
		return "x86_64"
	case "arm64":
		return "aarch64"
	default:
		// Not silently passed through: an unmapped value would be refused downstream anyway, and
		// Lint says so here with a name the reader can act on.
		return runtime.GOARCH
	}
}

// The contract's closed vocabularies, as org-runink-30's validator enforces them.
var (
	validArch       = map[string]bool{"x86_64": true, "aarch64": true}
	validSubjectKnd = map[string]bool{"source": true, "pacman-repo": true, "iso": true, "image": true}
	validHostKind   = map[string]bool{"server-node": true, "dev-box": true, "ci": true}
	// A harness name may be hierarchical (river-tier1/memtune).
	harnessName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*(/[a-z0-9][a-z0-9-]*)*$`)
	hex40       = regexp.MustCompile(`^[0-9a-f]{40}$`)
	ulid26      = regexp.MustCompile(`^[0-9A-HJKMNP-TV-Z]{26}$`)
)

// crockford is Crockford base32: no I, L, O or U, so a transcribed run id cannot be misread.
const crockford = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// newRunID is a ULID: 48 bits of millisecond timestamp then 80 bits from crypto/rand, in 26
// Crockford base32 characters. The shape is org-runink-30's requirement so their validator and
// this producer agree without either side loosening (2026-10-03).
//
// Sortable by time is the useful property: evidence lands in an append-only store, and being
// able to order runs without parsing a timestamp field matters there. The 80 random bits are
// what make it unique -- the time half is NOT the identity. Two runs of the same commit in the
// same millisecond are still two runs, and the consumer refuses a run_id it has already stored,
// so a collision would reject a genuine run or, worse, let a stale one pass as new.
func newRunID() string {
	ms := uint64(time.Now().UTC().UnixMilli()) & (1<<48 - 1)
	var r [10]byte
	if _, err := rand.Read(r[:]); err != nil {
		// Not a condition to paper over with a weaker id: see above.
		panic("evidence: crypto/rand unavailable: " + err.Error())
	}
	out := make([]byte, 26)
	// 48 bits of time into 10 characters (50 bits; the first holds the top 3).
	for i := 0; i < 10; i++ {
		out[i] = crockford[(ms>>(45-5*i))&0x1f]
	}
	// 80 bits of randomness into 16 characters, exactly.
	for i := 0; i < 16; i++ {
		bit := i * 5
		idx, off := bit/8, bit%8
		w := uint16(r[idx]) << 8
		if idx+1 < len(r) {
			w |= uint16(r[idx+1])
		}
		out[10+i] = crockford[(w>>(11-off))&0x1f]
	}
	return string(out)
}
