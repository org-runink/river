// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/org-runink/river/guide/agent"
	"github.com/org-runink/river/guide/bundles"
	"github.com/org-runink/river/guide/model"
)

// evalCase is one fixed question. Expect is "answer" (the guide covers it: a model
// answer citing one of Cites) or "not-found" (out of scope: the guide must say so
// rather than make something up).
type evalCase struct {
	Q      string   `json:"q"`
	Expect string   `json:"expect"`
	Cites  []string `json:"cites,omitempty"`
	Must   []string `json:"must,omitempty"`     // substrings the answer must contain (case-insensitive)
	MustNo []string `json:"must_not,omitempty"` // substrings it must not contain
}

type evalResult struct {
	Q        string   `json:"q"`
	Mode     string   `json:"mode"`
	Pass     bool     `json:"pass"`
	Why      string   `json:"why,omitempty"`
	Cites    []string `json:"cites"`
	Seconds  float64  `json:"seconds"`
	Note     string   `json:"note,omitempty"`
	Answer   string   `json:"answer"`
	Expected string   `json:"expected"`
}

// cmdEval scores the guide model: grounding (no withheld answers), citation accuracy,
// refusal on out-of-scope questions, and latency. It is how a model is chosen for the
// medium, and its JSON output is the evidence recorded in guide-model.lock's notes.
func cmdEval(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("eval", flag.ContinueOnError)
	fs.SetOutput(stderr)
	cases := fs.String("cases", "eval/cases.json", "question set")
	url := fs.String("model-url", "http://[::1]:8187/v1", "model endpoint (loopback)")
	name := fs.String("model-name", "", "model id")
	asJSON := fs.Bool("json", false, "emit JSON results")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	raw, err := os.ReadFile(*cases)
	if err != nil {
		fmt.Fprintln(stderr, "eval:", err)
		return 1
	}
	var cs []evalCase
	if err := json.Unmarshal(raw, &cs); err != nil {
		fmt.Fprintln(stderr, "eval:", err)
		return 1
	}
	river, err := bundles.River()
	if err != nil {
		fmt.Fprintln(stderr, "eval:", err)
		return 1
	}
	c, err := model.New(*url, *name, false)
	if err != nil {
		fmt.Fprintln(stderr, "eval:", err)
		return 1
	}
	ctx := context.Background()
	if err := c.Ready(ctx); err != nil {
		fmt.Fprintln(stderr, "eval: model not ready:", err)
		return 1
	}
	ag := agent.New(c, river)
	var res []evalResult
	pass := 0
	var total time.Duration
	for _, tc := range cs {
		t0 := time.Now()
		ans := ag.Ask(ctx, tc.Q, agent.ScopeLocal)
		d := time.Since(t0)
		total += d
		r := evalResult{Q: tc.Q, Mode: string(ans.Mode), Cites: ans.Citations, Seconds: d.Seconds(), Note: ans.Note, Answer: ans.Text, Expected: tc.Expect}
		r.Pass, r.Why = score(tc, ans)
		if r.Pass {
			pass++
		}
		res = append(res, r)
	}
	if *asJSON {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(map[string]any{"passed": pass, "total": len(cs), "mean_seconds": total.Seconds() / float64(max(len(cs), 1)), "results": res})
	} else {
		for _, r := range res {
			mark := "PASS"
			if !r.Pass {
				mark = "FAIL"
			}
			fmt.Fprintf(stdout, "%s %5.1fs %-9s %s", mark, r.Seconds, r.Mode, r.Q)
			if r.Why != "" {
				fmt.Fprintf(stdout, "  (%s)", r.Why)
			}
			fmt.Fprintln(stdout)
		}
		fmt.Fprintf(stdout, "%d/%d passed, mean %.1fs\n", pass, len(cs), total.Seconds()/float64(max(len(cs), 1)))
	}
	if pass != len(cs) {
		return 1
	}
	return 0
}

func score(tc evalCase, ans agent.Answer) (bool, string) {
	low := strings.ToLower(ans.Text)
	switch tc.Expect {
	case "not-found":
		if ans.Mode == agent.ModeModel {
			return false, "answered an out-of-scope question"
		}
		return true, ""
	default:
		if ans.Mode != agent.ModeModel {
			return false, "no grounded model answer (" + string(ans.Mode) + ": " + ans.Note + ")"
		}
		hit := len(tc.Cites) == 0
		for _, want := range tc.Cites {
			for _, got := range ans.Citations {
				if got == want {
					hit = true
				}
			}
		}
		if !hit {
			return false, "cited " + strings.Join(ans.Citations, ",") + ", want one of " + strings.Join(tc.Cites, ",")
		}
		for _, m := range tc.Must {
			if !strings.Contains(low, strings.ToLower(m)) {
				return false, "missing " + m
			}
		}
		for _, m := range tc.MustNo {
			if strings.Contains(low, strings.ToLower(m)) {
				return false, "contains " + m
			}
		}
		return true, ""
	}
}
