// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: Apache-2.0

// rivervalidate — the Runink River inference validation suite.
//
// It runs one check per requirement row (requirements.go) against an OpenAI-compatible
// endpoint and writes every raw request/response plus a machine summary. It never starts
// a model server itself: run.sh does that, one model at a time, under a host-wide lock.
//
//	rivervalidate run   -tiers general,coder -endpoint http://127.0.0.1:18090/v1 \
//	                    -model default -label qwen3-14b-q4km -out DIR [flags]
//	rivervalidate merge -o results.json DIR/summary.json...
//	rivervalidate sha256 FILE...
//
// Stdlib only, no network beyond the endpoint under test.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type result struct {
	ID          string         `json:"id"`
	Tier        string         `json:"tier"`
	Requirement string         `json:"requirement"`
	Threshold   string         `json:"threshold"`
	Hard        bool           `json:"hard"`
	Status      string         `json:"status"` // PASS | FAIL | CAPPED | SKIP
	Measured    map[string]any `json:"measured,omitempty"`
	Detail      string         `json:"detail,omitempty"`
	Evidence    []string       `json:"evidence,omitempty"` // raw files under the run's raw/ dir
	Seconds     float64        `json:"seconds"`
}

type summary struct {
	Label      string            `json:"label"`
	Tiers      []string          `json:"tiers"`
	Endpoint   string            `json:"endpoint"`
	Model      string            `json:"model"`
	Artifacts  map[string]string `json:"artifacts,omitempty"` // file -> sha256
	ServerArgs string            `json:"server_args,omitempty"`
	MaxSeqLen  int               `json:"max_seq_len"`
	Started    time.Time         `json:"started"`
	Finished   time.Time         `json:"finished"`
	WallSec    float64           `json:"wall_seconds"`
	Resident   map[string]any    `json:"resident,omitempty"`
	Results    []result          `json:"results"`
	Notes      []string          `json:"notes,omitempty"`
	// NotEvidence is set by merge on a run that was not served by the inference engine
	// (engine): its rows are kept as history, and its verdicts can never pass.
	NotEvidence string `json:"not_evidence,omitempty"`
}

// engine is the only inference engine: a run is evidence only when its server was this
// binary (the first word of server_args). Decided 2026-09-27.
const engine = "mistralrs"

// notEvidence says why a run is not evidence, or "" when it is. A run with no server_args
// started no server (every row is ERROR, or FAIL from source evidence), so there is nothing
// to disqualify; hardPass still cannot come from it, see cmdMerge.
func notEvidence(s summary) string {
	f := strings.Fields(s.ServerArgs)
	if len(f) == 0 || filepath.Base(f[0]) == engine {
		return ""
	}
	return fmt.Sprintf("NOT EVIDENCE: served by %s, not mistral.rs; mistral.rs is the only inference engine, so this run is history and passes no gate", filepath.Base(f[0]))
}

type runner struct {
	c        *client
	ctxLen   int
	ctxTO    time.Duration
	audioDir string
	outDir   string
	sampler  *memSampler
	memo     map[string]any
	notes    []string
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: rivervalidate run|merge|sha256 ...")
		os.Exit(2)
	}
	switch os.Args[1] {
	case "run":
		os.Exit(cmdRun(os.Args[2:]))
	case "merge":
		os.Exit(cmdMerge(os.Args[2:]))
	case "sha256":
		for _, f := range os.Args[2:] {
			s, err := fileSHA256(f)
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
			fmt.Printf("%s  %s\n", s, f)
		}
	default:
		fmt.Fprintln(os.Stderr, "unknown subcommand", os.Args[1])
		os.Exit(2)
	}
}

func cmdRun(args []string) int {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	tiers := fs.String("tiers", "general", "comma list: general,coder,vision,stt,embedding,tts")
	endpoint := fs.String("endpoint", "http://127.0.0.1:18090/v1", "OpenAI-compatible base URL")
	model := fs.String("model", "default", "model id to put in requests")
	label := fs.String("label", "", "candidate label (directory name)")
	out := fs.String("out", "", "evidence dir for this candidate")
	ctxLen := fs.Int("ctx", 16384, "the server's --max-seq-len / context size")
	ctxTO := fs.Duration("ctx-timeout", 50*time.Minute, "cap for the long-context fill; beyond it the check is CAPPED")
	reqTO := fs.Duration("timeout", 60*time.Minute, "per-request HTTP timeout")
	audioDir := fs.String("audio", "", "dir with <lang>.wav + <lang>.txt (+ <lang>.src) STT fixtures")
	cgroup := fs.String("cgroup", "", "container cgroup dir to sample memory from")
	artifacts := fs.String("artifacts", "", "comma list of model files to sha256 into the summary")
	serverArgs := fs.String("server-args", "", "the server command line, recorded verbatim")
	only := fs.String("only", "", "comma list of check IDs to run (default: every check of the tiers)")
	serverFailed := fs.String("server-failed", "", "the server never became healthy: record every row as ERROR with this reason, run nothing")
	_ = fs.Parse(args)
	if *label == "" || *out == "" {
		fmt.Fprintln(os.Stderr, "-label and -out are required")
		return 2
	}
	raw := filepath.Join(*out, "raw")
	if err := os.MkdirAll(raw, 0o755); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	r := &runner{c: newClient(*endpoint, *model, raw, *reqTO), ctxLen: *ctxLen, ctxTO: *ctxTO,
		audioDir: *audioDir, outDir: *out, memo: map[string]any{}}
	if *cgroup != "" {
		r.sampler = startSampler(*cgroup)
	}
	s := summary{Label: *label, Tiers: strings.Split(*tiers, ","), Endpoint: *endpoint, Model: *model,
		ServerArgs: *serverArgs, MaxSeqLen: *ctxLen, Started: time.Now().UTC()}
	if *artifacts != "" {
		s.Artifacts = map[string]string{}
		for _, f := range strings.Split(*artifacts, ",") {
			if h, err := fileSHA256(f); err == nil {
				s.Artifacts[filepath.Base(f)] = h
			} else {
				s.Artifacts[filepath.Base(f)] = "ERROR: " + err.Error()
			}
		}
	}
	ctx := context.Background()
	want := map[string]bool{}
	for _, id := range strings.Split(*only, ",") {
		if id = strings.TrimSpace(id); id != "" {
			want[id] = true
		}
	}
	if len(want) > 0 {
		r.notes = append(r.notes, "PARTIAL run: -only "+*only)
	}
	var normal, late []check
	for _, t := range s.Tiers {
		for _, ch := range r.checksFor(ctx, t) {
			if len(want) > 0 && !want[ch.id] {
				continue
			}
			if ch.late { // may leave the server busy (a cancelled 8k stream): run last
				late = append(late, ch)
			} else {
				normal = append(normal, ch)
			}
		}
	}
	if *serverFailed != "" {
		for i := range normal {
			id := normal[i].id
			normal[i].fn = func() result {
				return result{ID: id, Status: "ERROR", Detail: "NOT MEASURED: server did not start: " + *serverFailed}
			}
		}
		for i := range late {
			id := late[i].id
			late[i].fn = func() result {
				return result{ID: id, Status: "ERROR", Detail: "NOT MEASURED: server did not start: " + *serverFailed}
			}
		}
	}
	for _, ch := range append(normal, late...) {
		res := timed(ch.fn)
		logResult(*label, res)
		s.Results = append(s.Results, res)
		writeSummary(*out, &s) // incremental: a killed run still leaves evidence
	}
	if r.sampler != nil {
		s.Resident = r.sampler.stop()
		for i := range s.Results {
			if s.Results[i].ID == "VIS-RSS" {
				s.Results[i] = r.residentVerdict(s.Resident)
				logResult(*label, s.Results[i])
			}
		}
	}
	s.Notes = r.notes
	s.Finished = time.Now().UTC()
	s.WallSec = s.Finished.Sub(s.Started).Seconds()
	writeSummary(*out, &s)
	return 0
}

func timed(f func() result) result {
	t0 := time.Now()
	res := f()
	res.Seconds = time.Since(t0).Seconds()
	rq := reqByID(res.ID)
	res.Tier, res.Requirement, res.Threshold, res.Hard = rq.Tier, rq.Text, rq.Floor, rq.Hard
	return res
}

func logResult(label string, r result) {
	m, _ := json.Marshal(r.Measured)
	fmt.Printf("[%s] %-12s %-6s %6.1fs %s %s\n", label, r.ID, r.Status, r.Seconds, m, truncate(r.Detail, 200))
}

func writeSummary(dir string, s *summary) {
	b, _ := json.MarshalIndent(s, "", "  ")
	_ = os.WriteFile(filepath.Join(dir, "summary.json"), b, 0o644)
}

func fileSHA256(p string) (string, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func cmdMerge(args []string) int {
	fs := flag.NewFlagSet("merge", flag.ExitOnError)
	out := fs.String("o", "results.json", "output file")
	_ = fs.Parse(args)
	var runs []summary
	for _, p := range fs.Args() {
		b, err := os.ReadFile(p)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		var s summary
		if err := json.Unmarshal(b, &s); err != nil {
			fmt.Fprintln(os.Stderr, p, err)
			return 1
		}
		runs = append(runs, s)
	}
	sort.Slice(runs, func(i, j int) bool { return runs[i].Label < runs[j].Label })
	vs := judge(runs)
	doc := map[string]any{
		"schema":       "river.validation/v1",
		"generated":    time.Now().UTC(),
		"requirements": requirements,
		"verdicts":     vs,
		"runs":         runs,
	}
	b, _ := json.MarshalIndent(doc, "", "  ")
	if err := os.WriteFile(*out, append(b, '\n'), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

type verdict struct {
	Label, Tier string
	HardPass    bool
	Failed      []string `json:",omitempty"`
	NotMeasured []string `json:",omitempty"`
	NotEvidence string   `json:",omitempty"`
}

// judge derives one verdict per run and tier, and marks the runs themselves.
//
// A verdict is PASS only when every hard row of the tier was measured and passed, on the
// inference engine. Rows missing from a partial run are NOT_MEASURED. A FAIL whose detail is
// a transport error (the server died or never started) is re-read as ERROR: it says nothing
// about the model. Summaries written before the harness made that distinction get it here.
// A run served by anything but the engine is marked not_evidence and its verdicts fail,
// whatever its rows say.
func judge(runs []summary) []verdict {
	var vs []verdict
	for i := range runs {
		s := &runs[i]
		s.NotEvidence = notEvidence(*s)
		for j := range s.Results {
			r := &s.Results[j]
			if r.Status == "FAIL" && transportErr(errors.New(r.Detail)) {
				r.Status = "ERROR"
			}
		}
		for _, t := range s.Tiers {
			v := verdict{Label: s.Label, Tier: t, HardPass: true}
			seen := map[string]bool{}
			for _, r := range s.Results {
				if r.Tier != t || !r.Hard {
					continue
				}
				seen[r.ID] = true
				switch r.Status {
				case "PASS":
				case "ERROR", "SKIP", "CAPPED":
					v.HardPass = false
					v.NotMeasured = append(v.NotMeasured, r.ID+"="+r.Status)
				default:
					v.HardPass = false
					v.Failed = append(v.Failed, r.ID+"="+r.Status)
				}
			}
			for _, rq := range requirements {
				if rq.Tier == t && rq.Hard && !seen[rq.ID] {
					v.HardPass = false
					v.NotMeasured = append(v.NotMeasured, rq.ID+"=NOT_RUN")
				}
			}
			switch {
			case s.NotEvidence != "":
				v.HardPass, v.NotEvidence = false, s.NotEvidence
			case v.HardPass && strings.TrimSpace(s.ServerArgs) == "":
				v.HardPass, v.NotEvidence = false, "NOT EVIDENCE: no serving engine recorded for this run"
			}
			vs = append(vs, v)
		}
	}
	return vs
}
