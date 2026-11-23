// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

package main

import (
	"encoding/json"
	"fmt"
	"image"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

const (
	Pass = "PASS"
	Fail = "FAIL"
	NPQ  = "NOT PROVABLE IN QEMU"
)

type CheckResult struct {
	Kind   string `json:"kind"`
	Reason string `json:"reason"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail"`
}

type RowResult struct {
	N          int           `json:"n"`
	ID         string        `json:"id"`
	Name       string        `json:"name"`
	Title      string        `json:"title"`
	Status     string        `json:"status"`
	Since      string        `json:"since,omitempty"`
	Unprovable string        `json:"unprovable,omitempty"`
	Hardware   string        `json:"hardware,omitempty"`
	Checks     []CheckResult `json:"checks,omitempty"`
	Frames     []string      `json:"frames,omitempty"`
	Seconds    float64       `json:"seconds"`
}

// Env is everything a run touches, so the runner can be tested against fakes.
type Env struct {
	Q        Conn                              // QMP
	G        GuestExec                         // the guest command channel
	Shutdown func(time.Duration) bool          // QEMU powered off within the time
	Harness  string                            // the harness's own frames (to adopt)
	Art      string                            // artifacts directory: numbered PNGs, results
	Vars     map[string]string                 // ${NAME} in typed text and guest commands
	Sleep    func(time.Duration)               // time.Sleep, replaceable
	Log      io.Writer                         // progress
	Shoot    func(path string, head int) error // screendump; default through Q
	UsbImage string
	Work     string // the harness work directory (packet captures)
	UsbBus   string
}

type frame struct{ key, path string }

type runner struct {
	env    Env
	frames []frame // in the order taken
	seq    int
	rowID  string
}

func (e *Env) defaults() {
	if e.Sleep == nil {
		e.Sleep = time.Sleep
	}
	if e.Log == nil {
		e.Log = io.Discard
	}
	if e.Shoot == nil {
		e.Shoot = func(p string, h int) error { return Screendump(e.Q, p, h) }
	}
	if e.UsbBus == "" {
		e.UsbBus = "xhci.0"
	}
}

// Run executes the rows (all, or those with n in only when it is not nil) and returns their results.
func Run(cl *Checklist, env Env, only map[int]bool) []RowResult {
	env.defaults()
	os.MkdirAll(env.Art, 0o750)
	r := &runner{env: env}
	var out []RowResult
	for _, row := range cl.Rows {
		if only != nil && !only[row.N] {
			continue
		}
		res := r.row(row)
		fmt.Fprintf(env.Log, "desk-proof: %02d %-18s %s\n", row.N, row.ID, res.Status)
		out = append(out, res)
	}
	return out
}

func (r *runner) row(row Row) RowResult {
	t0 := time.Now()
	res := RowResult{N: row.N, ID: row.ID, Name: RowName(row), Title: row.Title, Since: row.Since,
		Unprovable: row.Unprovable, Hardware: row.Hardware}
	if row.Hardware != "" {
		res.Status = NPQ
		return res
	}
	r.rowID = row.ID
	for i, s := range row.Steps {
		if err := r.step(s); err != nil {
			res.Checks = append(res.Checks, CheckResult{Kind: "step", Reason: fmt.Sprintf("step %d must run", i+1), Detail: err.Error()})
		}
	}
	res.Status = Pass
	for _, c := range row.Checks {
		cr := r.check(row, c)
		res.Checks = append(res.Checks, cr)
	}
	for _, c := range res.Checks {
		if !c.OK {
			res.Status = Fail
		}
	}
	for _, f := range r.frames {
		if f.key == row.ID || strings.HasPrefix(f.key, row.ID+":") {
			res.Frames = append(res.Frames, filepath.Base(f.path))
		}
	}
	res.Seconds = time.Since(t0).Seconds()
	return res
}

func (r *runner) expand(s string) string {
	return os.Expand(s, func(k string) string { return r.env.Vars[k] })
}

func (r *runner) step(s Step) error {
	q := r.env.Q
	switch {
	case s.Key != "":
		k, _ := ParseCombo(s.Key)
		hold := 120 * time.Millisecond
		if s.Hold != "" {
			hold, _ = time.ParseDuration(s.Hold)
		}
		return SendKeys(q, k, hold)
	case s.Type != "":
		return TypeText(q, r.expand(s.Type))
	case s.Down != "":
		k, _ := ParseCombo(s.Down)
		return KeyEvents(q, k, true)
	case s.Up != "":
		k, _ := ParseCombo(s.Up)
		return KeyEvents(q, k, false)
	case len(s.Click) == 2:
		b := s.Button
		if b == "" {
			b = "left"
		}
		return Click(q, s.Click[0], s.Click[1], b)
	case s.Guest != "":
		user := s.User
		if user == "" {
			user = "runink"
		}
		rc, out, err := r.env.G.Run(user, r.expand(s.Guest), 90*time.Second, !s.NoWait)
		if err != nil {
			return err
		}
		fmt.Fprintf(r.env.Log, "desk-proof:   guest rc=%d %s\n", rc, oneLine(out))
		return nil
	case s.Shot != "":
		_, err := r.shoot(s.Shot, s.Head)
		return err
	case s.Adopt != "":
		return r.adopt(s.Adopt, s.As)
	case s.USB != "":
		return UsbAdd(q, r.env.UsbImage, r.env.UsbBus)
	case s.Wait != "":
		d, _ := time.ParseDuration(s.Wait)
		r.env.Sleep(d)
	}
	return nil
}

// path returns the numbered artifact name of a frame: 004-desk-lit-label.png.
func (r *runner) path(label string) string {
	r.seq++
	name := fmt.Sprintf("%03d-%s", r.seq, r.rowID)
	if label != "" && label != r.rowID {
		name += "-" + label
	}
	return filepath.Join(r.env.Art, name+".png")
}

func (r *runner) keyOf(label string) string {
	if label == "" {
		return r.rowID
	}
	return r.rowID + ":" + label
}

// put records a frame under its key; a re-photograph of the same key replaces the old frame in
// place (and its file), so a retry loop does not fill the artifacts with near copies.
func (r *runner) put(label, path string) {
	k := r.keyOf(label)
	for i := range r.frames {
		if r.frames[i].key == k {
			if r.frames[i].path != path {
				os.Remove(r.frames[i].path)
			}
			r.frames[i].path = path
			return
		}
	}
	r.frames = append(r.frames, frame{k, path})
}

func (r *runner) shoot(label string, head int) (string, error) {
	k := r.keyOf(label)
	p := ""
	for _, f := range r.frames { // reuse the file name of a retried frame
		if f.key == k {
			p = f.path
		}
	}
	if p == "" {
		p = r.path(label)
	}
	if err := r.env.Shoot(p, head); err != nil {
		return "", err
	}
	r.put(label, p)
	return p, nil
}

func (r *runner) adopt(glob, label string) error {
	m, _ := filepath.Glob(filepath.Join(r.env.Harness, glob))
	if len(m) == 0 {
		return fmt.Errorf("the harness took no frame matching %s", glob)
	}
	sort.Strings(m)
	b, err := os.ReadFile(m[len(m)-1])
	if err != nil {
		return err
	}
	p := r.path(label)
	if err := os.WriteFile(p, b, 0o600); err != nil {
		return err
	}
	r.put(label, p)
	return nil
}

// find returns the frame for a key and the one taken just before it.
func (r *runner) find(key string) (cur, prev string, ok bool) {
	for i, f := range r.frames {
		if f.key == key {
			if i > 0 {
				prev = r.frames[i-1].path
			}
			return f.path, prev, true
		}
	}
	return "", "", false
}

func (r *runner) lastOfRow(row string) string {
	last := ""
	for _, f := range r.frames {
		if f.key == row || strings.HasPrefix(f.key, row+":") {
			last = f.path
		}
	}
	return last
}

func (r *runner) check(row Row, c Check) CheckResult {
	cr := CheckResult{Kind: c.Kind, Reason: c.Reason}
	var retry time.Duration
	if c.Retry != "" {
		retry, _ = time.ParseDuration(c.Retry)
	}
	deadline := time.Now().Add(retry)
	for {
		switch c.Kind {
		case "screen":
			cr.OK, cr.Detail = r.screen(c, retry > 0)
		case "guest":
			cr.OK, cr.Detail = r.guest(c)
		case "file-absent":
			cr.OK, cr.Detail = r.fileAbsent(c)
		case "qemu-exit":
			d, _ := time.ParseDuration(c.Within)
			cr.OK = r.env.Shutdown != nil && r.env.Shutdown(d)
			cr.Detail = fmt.Sprintf("powered off within %s: %v", c.Within, cr.OK)
		}
		if cr.OK || retry == 0 || time.Now().After(deadline) {
			return cr
		}
		r.env.Sleep(4 * time.Second)
	}
}

func (r *runner) screen(c Check, reshoot bool) (bool, string) {
	var path string
	if reshoot {
		p, err := r.shoot(c.Shot, c.Head)
		if err != nil {
			return false, err.Error()
		}
		path = p
	} else if c.Shot != "" {
		p, _, ok := r.find(r.keyOf(c.Shot))
		if !ok {
			return false, "no frame " + c.Shot + " (its step failed)"
		}
		path = p
	} else if path = r.lastOfRow(r.rowID); path == "" {
		return false, "the row took no frame"
	}
	img, err := LoadPNG(path)
	if err != nil {
		return false, err.Error()
	}
	want := map[string]Bound{}
	for k, v := range Presets[c.Preset] {
		want[k] = v
	}
	for k, v := range c.Expect {
		want[k] = v
	}
	ok, detail := true, ""
	if len(want) > 0 {
		ok, detail = Evaluate(Measure(img, c.Region), want)
	}
	if c.Changed != nil {
		var basePath string
		switch c.Vs {
		case "prev":
			k := r.keyOf(c.Shot)
			if c.Shot == "" {
				k = ""
				for _, f := range r.frames {
					if f.path == path {
						k = f.key
					}
				}
			}
			_, basePath, _ = r.find(k)
		default:
			if _, _, found := r.find(c.Vs); found {
				basePath, _, _ = r.find(c.Vs)
			} else {
				basePath = r.lastOfRow(c.Vs)
			}
		}
		if basePath == "" {
			return false, strings.TrimSpace(detail + " no frame to compare with (" + c.Vs + ")")
		}
		base, err := LoadPNG(basePath)
		if err != nil {
			return false, err.Error()
		}
		f := ChangedFrac(base, image.Image(img), c.Region)
		cok := c.Changed.ok(f)
		ok = ok && cok
		detail = strings.TrimSpace(fmt.Sprintf("%s changed_vs_%s=%.4g want %s", detail, c.Vs, f, c.Changed))
	}
	return ok, detail + " [" + filepath.Base(path) + "]"
}

func (r *runner) guest(c Check) (bool, string) {
	user := c.User
	if user == "" {
		user = "runink"
	}
	rc, out, err := r.env.G.Run(user, r.expand(c.Cmd), 90*time.Second, true)
	if err != nil {
		return false, err.Error()
	}
	want := 0
	if c.RC != nil {
		want = *c.RC
	}
	var bad []string
	if rc != want {
		bad = append(bad, fmt.Sprintf("exit %d, want %d", rc, want))
	}
	if c.Match != "" && !regexp.MustCompile(c.Match).MatchString(out) {
		bad = append(bad, fmt.Sprintf("output does not match /%s/", c.Match))
	}
	if c.NotMatch != "" && regexp.MustCompile(c.NotMatch).MatchString(out) {
		bad = append(bad, fmt.Sprintf("output matches /%s/", c.NotMatch))
	}
	d := fmt.Sprintf("rc=%d out=%s", rc, oneLine(out))
	if len(bad) > 0 {
		return false, strings.Join(bad, "; ") + ": " + d
	}
	return true, d
}

func oneLine(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 160 {
		s = s[:160] + "..."
	}
	return s
}

// --- results -----------------------------------------------------------------------------------

// WriteResults writes results.json (machine readable), results.tsv (one line a row) and
// rows.txt (the RIVERTEST lines the harness's verdict reads), and returns whether no row failed.
func WriteResults(dir string, rows []RowResult, meta map[string]string) (bool, error) {
	counts := map[string]int{}
	var tsv, rt strings.Builder
	good := true
	for _, r := range rows {
		counts[r.Status]++
		detail := ""
		for _, c := range r.Checks {
			if !c.OK {
				detail += c.Kind + ": " + c.Detail + " | "
			}
		}
		if r.Status == NPQ {
			detail = r.Hardware
		} else if r.Unprovable != "" {
			detail += "(not shown by QEMU: " + r.Unprovable + ")"
		}
		fmt.Fprintf(&tsv, "%02d\t%s\t%s\t%s\t%s\n", r.N, r.ID, r.Status, r.Title, strings.TrimSuffix(detail, " | "))
		switch r.Status {
		case Pass:
			fmt.Fprintf(&rt, "RIVERTEST OK %s\n", r.Name)
		case NPQ:
			fmt.Fprintf(&rt, "RIVERTEST SKIP %s not provable in QEMU: %s\n", r.Name, r.Hardware)
		default:
			good = false
			fmt.Fprintf(&rt, "RIVERTEST FAIL %s (%s)\n", r.Name, oneLine(strings.TrimSuffix(detail, " | ")))
		}
	}
	doc := map[string]any{"meta": meta, "counts": counts, "rows": rows}
	b, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return false, err
	}
	for name, data := range map[string][]byte{"results.json": append(b, '\n'), "results.tsv": []byte(tsv.String()), "rows.txt": []byte(rt.String())} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o600); err != nil {
			return false, err
		}
	}
	return good, nil
}

// fileAbsent is the check that nothing was sent: the capture must exist and hold packets (a
// missing or empty capture would pass vacuously) and must not contain the needle.
func (r *runner) fileAbsent(c Check) (bool, string) {
	p := filepath.Join(r.env.Work, filepath.Base(c.Path))
	b, err := os.ReadFile(p)
	if err != nil {
		return false, err.Error()
	}
	if len(b) < 64 {
		return false, fmt.Sprintf("%s holds %d bytes: no capture to judge", c.Path, len(b))
	}
	if strings.Contains(string(b), c.Needle) {
		return false, fmt.Sprintf("%s contains %q", c.Path, c.Needle)
	}
	return true, fmt.Sprintf("%s: %d bytes, no %q", c.Path, len(b), c.Needle)
}
