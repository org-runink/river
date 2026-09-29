// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

package hw

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeTool writes an executable that prints file and exits with code.
func fakeTool(t *testing.T, dir, name, file string, code int) string {
	t.Helper()
	abs, _ := filepath.Abs(file)
	p := filepath.Join(dir, name)
	script := "#!/bin/sh\ncat '" + abs + "'\nexit " + string(rune('0'+code)) + "\n"
	if err := os.WriteFile(p, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

var identifiers = []string{"alice", "DCC1-80A3", "S7KHNJ0W123456X", "AA00000000011542", "48:21:0b", "c8:5e:a9", "by-id", "eui.", "Samsung", "Netac", "ExampleCorp", "EX-1000", "SECRET_LOOKING"}

func TestSummariesCarryNoIdentifiers(t *testing.T) {
	dir := t.TempDir()
	tools := Tools{
		HWProbe:  fakeTool(t, dir, "river-hwprobe", "testdata/probe.json", 0),
		Plan:     fakeTool(t, dir, "river-plan", "testdata/plan.json", 3), // exit 3 = refused-but-printed
		Manifest: filepath.Join(dir, "models.tiers"),
		WorkDir:  filepath.Join(dir, "work"),
	}
	r, err := tools.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	sum, plan := r.Summary(), r.PlanSummary()
	t.Logf("hardware summary:\n%s\nplan summary:\n%s", sum, plan)
	for _, id := range identifiers {
		if strings.Contains(sum, id) || strings.Contains(plan, id) {
			t.Errorf("summary leaks %q", id)
		}
	}
	for _, want := range []string{"UEFI", "x86-64-v3", "93 GiB", "nvme0n1", "install medium", "Eligible install disks: 1", "TPM: 2.0", "1 with a global IPv6"} {
		if !strings.Contains(sum, want) {
			t.Errorf("hardware summary lacks %q", want)
		}
	}
	for _, want := range []string{"Verdict: **degraded**", "single layout", "embedding: enabled", "no redundancy"} {
		if !strings.Contains(plan, want) {
			t.Errorf("plan summary lacks %q", want)
		}
	}
	if b, err := os.ReadFile(filepath.Join(dir, "work", "probe.json")); err != nil || len(b) == 0 {
		t.Fatal("probe document not handed to the planner")
	}
}

func TestWrongSchemaRefused(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.json")
	os.WriteFile(bad, []byte(`{"schema":"something/v9"}`), 0o644)
	_, err := Tools{HWProbe: fakeTool(t, dir, "p", bad, 0), WorkDir: dir}.Run(context.Background())
	if err == nil {
		t.Fatal("accepted a foreign schema")
	}
}
