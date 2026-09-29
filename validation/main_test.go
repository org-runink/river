// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// hardRows returns one PASS result for every hard requirement of tier t.
func hardRows(t string) []result {
	var rs []result
	for _, rq := range requirements {
		if rq.Tier == t && rq.Hard {
			rs = append(rs, result{ID: rq.ID, Tier: t, Hard: true, Status: "PASS"})
		}
	}
	return rs
}

// Only a run served by mistral.rs can pass a tier; every other engine is not evidence.
func TestJudgeOnlyTheEngineIsEvidence(t *testing.T) {
	runs := []summary{
		{Label: "ours", Tiers: []string{"coder"}, ServerArgs: "mistralrs serve --port 8080 text", Results: hardRows("coder")},
		{Label: "other", Tiers: []string{"coder"}, ServerArgs: "/app/bin/other-server --port 8080", Results: hardRows("coder")},
		{Label: "none", Tiers: []string{"coder"}, Results: hardRows("coder")},
	}
	if len(runs[0].Results) == 0 {
		t.Fatal("no hard coder requirements; the test examines nothing")
	}
	vs := judge(runs)
	got := map[string]verdict{}
	for _, v := range vs {
		got[v.Label] = v
	}
	if v := got["ours"]; !v.HardPass || v.NotEvidence != "" || runs[0].NotEvidence != "" {
		t.Errorf("mistral.rs run: %+v (run mark %q)", v, runs[0].NotEvidence)
	}
	if v := got["other"]; v.HardPass || !strings.HasPrefix(v.NotEvidence, "NOT EVIDENCE: served by other-server") ||
		runs[1].NotEvidence == "" {
		t.Errorf("other engine passed or was not marked: %+v (run mark %q)", v, runs[1].NotEvidence)
	}
	if v := got["none"]; v.HardPass || v.NotEvidence == "" {
		t.Errorf("run with no engine passed: %+v", v)
	}
}

// The committed results.json must agree: no verdict passes unless its run was served by
// mistral.rs, and every run served by anything else carries not_evidence.
func TestCommittedResults(t *testing.T) {
	b, err := os.ReadFile("results.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Verdicts []verdict `json:"verdicts"`
		Runs     []summary `json:"runs"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Runs) < 5 || len(doc.Verdicts) < 5 {
		t.Fatalf("results.json has %d runs, %d verdicts; the test examines nothing", len(doc.Runs), len(doc.Verdicts))
	}
	byLabel := map[string]summary{}
	for _, r := range doc.Runs {
		byLabel[r.Label] = r
		if want := notEvidence(r); r.NotEvidence != want {
			t.Errorf("run %s: not_evidence %q, want %q (re-run `run.sh merge`)", r.Label, r.NotEvidence, want)
		}
	}
	for _, v := range doc.Verdicts {
		r, ok := byLabel[v.Label]
		if !ok {
			t.Errorf("verdict for unknown run %s", v.Label)
			continue
		}
		f := strings.Fields(r.ServerArgs)
		if v.HardPass && (len(f) == 0 || filepath.Base(f[0]) != engine) {
			t.Errorf("verdict %s/%s passes on a run not served by mistral.rs", v.Label, v.Tier)
		}
		if r.NotEvidence != "" && v.NotEvidence == "" {
			t.Errorf("verdict %s/%s is not marked though its run is not evidence", v.Label, v.Tier)
		}
	}
}
