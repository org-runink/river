// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"
)

// The checklist is data (build/desk-checklist.json): one row per thing a user does on the desk,
// the steps that do it on the guest, and the checks that prove it happened. A check is one of
//
//	screen    a frame judged by structure, never by exact pixels (screen.go): colour and entropy
//	          metrics of a region, and how much of a region changed against an earlier frame
//	guest     a command run inside the guest (as the user or root): its exit status and output
//	qemu-exit QEMU powers off within a time (the shutdown row)
//
// and a row that QEMU cannot prove at all says so (hardware), is reported NOT PROVABLE IN QEMU
// and is never skipped silently. A check always carries its reason: what it proves.
type Checklist struct {
	Version int   `json:"version"`
	Rows    []Row `json:"rows"`
}

type Row struct {
	N     int    `json:"n"`
	ID    string `json:"id"`
	Title string `json:"title"`
	// Since is the DEsk release that introduced the row's feature (empty: always there).
	Since string `json:"since,omitempty"`
	// Hardware, when set, is why QEMU cannot prove this row at all.
	Hardware string `json:"hardware,omitempty"`
	// Unprovable names the part of a provable row that QEMU cannot show (it is reported).
	Unprovable string  `json:"unprovable,omitempty"`
	Steps      []Step  `json:"steps,omitempty"`
	Checks     []Check `json:"checks,omitempty"`
}

// Step is one action; exactly one of Key, Type, Down, Up, Click, Guest, Shot, Adopt, USB, Wait.
type Step struct {
	Key    string    `json:"key,omitempty"`    // press and release a chord: "super+enter"
	Hold   string    `json:"hold,omitempty"`   // with Key: hold time
	Type   string    `json:"type,omitempty"`   // type text ("\n" is Enter); ${NAME} expands
	Down   string    `json:"down,omitempty"`   // press and hold a chord
	Up     string    `json:"up,omitempty"`     // release it
	Click  []float64 `json:"click,omitempty"`  // [fx, fy] fractions of the screen
	Button string    `json:"button,omitempty"` // with Click: left (default), right, middle
	Guest  string    `json:"guest,omitempty"`  // a shell command in the guest
	User   string    `json:"user,omitempty"`   // with Guest: runink (default) or root
	NoWait bool      `json:"nowait,omitempty"` // with Guest: send it and do not wait (power off)
	Shot   string    `json:"shot,omitempty"`   // photograph the screen, labelled
	Head   int       `json:"head,omitempty"`   // with Shot: display head (0, 1)
	Adopt  string    `json:"adopt,omitempty"`  // take a frame the harness already took (glob)
	As     string    `json:"as,omitempty"`     // with Adopt: its label
	USB    string    `json:"usb,omitempty"`    // "add": hot-plug the USB stick
	Wait   string    `json:"wait,omitempty"`   // sleep, "3s"
}

type Check struct {
	Kind   string `json:"kind"`
	Reason string `json:"reason"`
	// screen
	Shot    string           `json:"shot,omitempty"`    // label within the row; empty: the row's last
	Head    int              `json:"head,omitempty"`    // with Retry: which head to photograph
	Preset  string           `json:"preset,omitempty"`  // greeter | dark-alive | lit
	Region  Region           `json:"region,omitempty"`  // [x0 y0 x1 y1] fractions
	Expect  map[string]Bound `json:"expect,omitempty"`  // metric bounds
	Vs      string           `json:"vs,omitempty"`      // prev | <row> | <row>:<label>
	Changed *Bound           `json:"changed,omitempty"` // bound of the changed fraction against Vs
	// guest
	User     string `json:"user,omitempty"`
	Cmd      string `json:"cmd,omitempty"`
	RC       *int   `json:"rc,omitempty"` // expected exit status (default 0)
	Match    string `json:"match,omitempty"`
	NotMatch string `json:"not_match,omitempty"`
	// qemu-exit
	Within string `json:"within,omitempty"`
	// file-absent: a file in the work directory (a packet capture) exists, is not empty, and
	// does not contain Needle
	Path   string `json:"path,omitempty"`
	Needle string `json:"needle,omitempty"`
	// every kind: keep trying (re-photographing / re-running) until it passes or this long
	Retry string `json:"retry,omitempty"`
}

func LoadChecklist(path string) (*Checklist, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cl Checklist
	dec := json.NewDecoder(strings.NewReader(string(b)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cl); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if err := cl.Validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &cl, nil
}

var idRe = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

var knownMetrics = map[string]bool{"luma_mean": true, "entropy": true, "colors": true, "nonblack_frac": true,
	"dark_frac": true, "bright_frac": true, "warm_frac": true, "paper_frac": true, "red_frac": true}

// Validate catches a checklist that cannot run or that proves nothing, before a boot is spent.
func (cl *Checklist) Validate() error {
	if cl.Version != 1 {
		return fmt.Errorf("checklist version %d, want 1", cl.Version)
	}
	seen := map[string]bool{}
	labels := map[string]bool{}
	for i, r := range cl.Rows {
		where := fmt.Sprintf("row %d (%s)", i+1, r.ID)
		if r.N != i+1 {
			return fmt.Errorf("%s: n is %d, rows are numbered 1..N in order", where, r.N)
		}
		if !idRe.MatchString(r.ID) || seen[r.ID] {
			return fmt.Errorf("%s: id must be lower-case words with hyphens and unique", where)
		}
		if r.Title == "" {
			return fmt.Errorf("%s: no title", where)
		}
		if r.Hardware != "" {
			if len(r.Steps) > 0 || len(r.Checks) > 0 {
				return fmt.Errorf("%s: a hardware-only row has no steps or checks", where)
			}
			seen[r.ID] = true
			continue
		}
		if len(r.Checks) == 0 {
			return fmt.Errorf("%s: no checks (a row that proves nothing must say it is hardware-only)", where)
		}
		for j, s := range r.Steps {
			if err := s.validate(); err != nil {
				return fmt.Errorf("%s step %d: %w", where, j+1, err)
			}
			if s.Shot != "" {
				labels[r.ID+":"+s.Shot] = true
			}
			if s.Adopt != "" {
				labels[r.ID+":"+s.As] = true
			}
		}
		seen[r.ID] = true
		for j, c := range r.Checks {
			if err := c.validate(seen, labels, r.ID); err != nil {
				return fmt.Errorf("%s check %d: %w", where, j+1, err)
			}
			if c.Kind == "screen" && c.Retry != "" {
				labels[r.ID+":"+c.Shot] = true // a retried check photographs its own frame
			}
		}
	}
	return nil
}

func (s Step) validate() error {
	n := 0
	for _, set := range []bool{s.Key != "", s.Type != "", s.Down != "", s.Up != "", len(s.Click) > 0,
		s.Guest != "", s.Shot != "", s.Adopt != "", s.USB != "", s.Wait != ""} {
		if set {
			n++
		}
	}
	if n != 1 {
		return fmt.Errorf("a step does exactly one thing, this one sets %d", n)
	}
	for _, k := range []string{s.Key, s.Down, s.Up} {
		if k != "" {
			if _, err := ParseCombo(k); err != nil {
				return err
			}
		}
	}
	for _, d := range []string{s.Wait, s.Hold} {
		if d != "" {
			if _, err := time.ParseDuration(d); err != nil {
				return err
			}
		}
	}
	if len(s.Click) > 0 && (len(s.Click) != 2) {
		return fmt.Errorf("click is [fx, fy]")
	}
	if s.Adopt != "" && s.As == "" {
		return fmt.Errorf("adopt needs as (the label)")
	}
	if s.USB != "" && s.USB != "add" {
		return fmt.Errorf("usb is \"add\"")
	}
	if s.User != "" && s.User != "root" && s.User != "runink" {
		return fmt.Errorf("user is runink or root")
	}
	return nil
}

func (c Check) validate(rows, labels map[string]bool, row string) error {
	if strings.TrimSpace(c.Reason) == "" {
		return fmt.Errorf("a check says why (what it proves)")
	}
	if c.Retry != "" {
		if _, err := time.ParseDuration(c.Retry); err != nil {
			return err
		}
	}
	switch c.Kind {
	case "screen":
		if c.Preset != "" && Presets[c.Preset] == nil {
			return fmt.Errorf("unknown preset %q", c.Preset)
		}
		for m := range c.Expect {
			if !knownMetrics[m] {
				return fmt.Errorf("unknown metric %q", m)
			}
		}
		if c.Preset == "" && len(c.Expect) == 0 && c.Changed == nil {
			return fmt.Errorf("a screen check needs a preset, expect or changed")
		}
		if (c.Changed != nil) != (c.Vs != "") {
			return fmt.Errorf("changed and vs go together")
		}
		if c.Vs != "" && c.Vs != "prev" {
			if !rows[c.Vs] && !labels[c.Vs] {
				return fmt.Errorf("vs %q names no earlier row or frame", c.Vs)
			}
		}
		if c.Shot != "" && !labels[row+":"+c.Shot] && c.Retry == "" {
			return fmt.Errorf("shot %q is not taken by this row", c.Shot)
		}
	case "guest":
		if c.Cmd == "" {
			return fmt.Errorf("a guest check has a cmd")
		}
		for _, re := range []string{c.Match, c.NotMatch} {
			if _, err := regexp.Compile(re); err != nil {
				return err
			}
		}
	case "file-absent":
		if c.Path == "" || c.Needle == "" {
			return fmt.Errorf("file-absent needs path and needle")
		}
	case "qemu-exit":
		if _, err := time.ParseDuration(c.Within); err != nil {
			return fmt.Errorf("qemu-exit needs within: %w", err)
		}
	default:
		return fmt.Errorf("unknown check kind %q", c.Kind)
	}
	return nil
}

// Names are the checks the verdict lists, one per row (desk-01-boot-splash).
func (cl *Checklist) Names() []string {
	var out []string
	for _, r := range cl.Rows {
		out = append(out, RowName(r))
	}
	return out
}

func RowName(r Row) string { return fmt.Sprintf("desk-%02d-%s", r.N, r.ID) }

// Labels returns the frame labels the checklist takes, sorted (for the docs and the tests).
func (cl *Checklist) Labels() []string {
	var out []string
	for _, r := range cl.Rows {
		for _, s := range r.Steps {
			if s.Shot != "" {
				out = append(out, r.ID+":"+s.Shot)
			}
		}
	}
	sort.Strings(out)
	return out
}
