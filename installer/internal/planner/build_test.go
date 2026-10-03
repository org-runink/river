// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

package planner

import (
	"encoding/json"
	"strings"
	"testing"
)

// A node that builds images shares its RAM with inference, and inference wins: the build gets
// a standing reserve only out of what the model tiers leave at full context, and otherwise
// waits for a window. These pin the decision on each class of machine, the budget line that
// explains it, and the plan JSON a downstream scheduler reads.
func TestBuildReserve(t *testing.T) {
	for _, c := range []struct {
		name       string
		probe      string
		profile    string
		noModels   bool
		mode       string
		workDir    string
		reserved   int64
		lineSays   string // substring of the build budget line's note ("" when there is no line)
		noteSays   string // substring of some plan note ("" when none is expected)
		wantUnfull bool   // the tiers are NOT all at full context (a small machine)
	}{
		// 256 GiB, discrete: ~146 GiB spare after every tier at full context. The whole build,
		// work dir included, fits in RAM beside inference.
		{name: "large discrete, tmpfs", probe: "server-256g-avx512.probe.json", profile: ProfileServer,
			mode: BuildReserved, workDir: WorkDirTmpfs, reserved: BuildMiB + BuildWorkDirMiB,
			lineSays: "run beside inference"},
		// 128 GiB with discrete GPUs: ~41 GiB spare. Enough for the build, not for the tmpfs
		// as well, so the work dir goes to disk rather than eating into the margin.
		{name: "large discrete, work dir on disk", probe: "gpu-128g.probe.json", profile: ProfileServer,
			mode: BuildReserved, workDir: WorkDirDisk, reserved: BuildMiB,
			lineSays: "work dir is on disk", noteSays: "planned on disk, not tmpfs"},
		// 128 GiB unified: the ARC cap returns memory to the models first, and the build gets
		// a reserve from what is left after them, never the other way round.
		{name: "large unified, work dir on disk", probe: "unified-128g.probe.json", profile: ProfileServer,
			mode: BuildReserved, workDir: WorkDirDisk, reserved: BuildMiB,
			lineSays: "work dir is on disk", noteSays: "planned on disk, not tmpfs"},
		// 64 GiB: the tiers already stop short of full context, so nothing is spare. No
		// standing reserve; the build is windowed.
		{name: "small, windowed", probe: "server-64g.probe.json", profile: ProfileServer,
			mode: BuildWindowed, workDir: WorkDirDisk, reserved: 0,
			lineSays: "inference wins", noteSays: "image builds are windowed", wantUnfull: true},
		// A large machine with no models manifest: the working set is unknown, so the planner
		// does not call anything spare.
		{name: "no models manifest, windowed", probe: "server-256g-avx512.probe.json", profile: ProfileServer, noModels: true,
			mode: BuildWindowed, workDir: WorkDirDisk, reserved: 0,
			lineSays: "inference working set is unknown", noteSays: "image builds are windowed"},
		// A workstation does not build images on the node: no line, no reserve.
		{name: "workstation", probe: "workstation-25g.probe.json", profile: ProfileWorkstation,
			mode: BuildNone, reserved: 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			var m *Manifest
			if !c.noModels {
				m = loadManifest(t)
			}
			pl := Build(loadProbe(t, c.probe), m, Options{Profile: c.profile})
			b := pl.Build
			if b.Mode != c.mode || b.WorkDir != c.workDir || b.ReservedMiB != c.reserved {
				t.Fatalf("build = %+v, want mode %s, work dir %q, %d MiB reserved", b, c.mode, c.workDir, c.reserved)
			}

			var line *BudgetLine
			var sum int64
			for i, l := range pl.Memory.Budget {
				sum += l.MiB
				if l.Item == "build" {
					line = &pl.Memory.Budget[i]
				}
			}
			if c.mode == BuildNone {
				if line != nil {
					t.Errorf("a %s node has a build budget line: %+v", c.profile, *line)
				}
			} else {
				if line == nil {
					t.Fatal("no build budget line")
				}
				if line.MiB != c.reserved || !strings.Contains(line.Note, c.lineSays) {
					t.Errorf("build line %+v, want %d MiB and a note saying %q", *line, c.reserved, c.lineSays)
				}
			}
			if sum != pl.Memory.ReservedMiB+pl.Memory.ModelsMiB+pl.Memory.HeadroomMiB {
				t.Errorf("budget table sums to %d, plan says reserved %d + models %d + headroom %d",
					sum, pl.Memory.ReservedMiB, pl.Memory.ModelsMiB, pl.Memory.HeadroomMiB)
			}
			if pl.Memory.UnplannedMiB < 0 {
				t.Errorf("the build reserve overcommits RAM: unplanned %d MiB", pl.Memory.UnplannedMiB)
			}
			if c.noteSays != "" && !strings.Contains(strings.Join(pl.Notes, "|"), c.noteSays) {
				t.Errorf("no note saying %q: %v", c.noteSays, pl.Notes)
			}

			// Inference wins: on a machine with a reserve every placed tier is at full
			// context, so the build only took what the models could not use.
			if c.mode == BuildReserved {
				for _, tp := range pl.Tiers {
					for _, r := range tp.Reasons {
						if strings.Contains(r, "(RAM)") {
							t.Errorf("tier %s was cut short (%s) on a node that reserves for builds", tp.Tier, r)
						}
					}
				}
			}
			if c.wantUnfull && pl.Memory.UnplannedMiB >= BuildMiB {
				t.Errorf("windowed, yet %d MiB is unplanned", pl.Memory.UnplannedMiB)
			}

			// The JSON a downstream scheduler reads.
			raw, err := json.Marshal(pl)
			if err != nil {
				t.Fatal(err)
			}
			var doc struct {
				Build map[string]any `json:"build"`
			}
			if err := json.Unmarshal(raw, &doc); err != nil {
				t.Fatal(err)
			}
			if doc.Build["mode"] != c.mode {
				t.Errorf("build.mode = %v, want %s", doc.Build["mode"], c.mode)
			}
			if got, _ := doc.Build["reserved_mib"].(float64); int64(got) != c.reserved {
				t.Errorf("build.reserved_mib = %v, want %d", doc.Build["reserved_mib"], c.reserved)
			}
			if c.mode != BuildNone {
				if doc.Build["work_dir"] != c.workDir {
					t.Errorf("build.work_dir = %v, want %s", doc.Build["work_dir"], c.workDir)
				}
				if got, _ := doc.Build["need_mib"].(float64); int64(got) != BuildMiB {
					t.Errorf("build.need_mib = %v, want %d", doc.Build["need_mib"], BuildMiB)
				}
				if !strings.Contains(Text(pl), "Build     "+c.mode) {
					t.Error("text rendering does not show the build decision")
				}
			}
		})
	}
}

// The thresholds, exactly: a reserve needs the whole build, and a tmpfs work dir needs the
// whole build plus the work dir.
func TestBuildPlanThresholds(t *testing.T) {
	for _, c := range []struct {
		spare   int64
		mode    string
		workDir string
	}{
		{0, BuildWindowed, WorkDirDisk},
		{-500, BuildWindowed, WorkDirDisk},
		{BuildMiB - 1, BuildWindowed, WorkDirDisk},
		{BuildMiB, BuildReserved, WorkDirDisk},
		{BuildMiB + BuildWorkDirMiB - 1, BuildReserved, WorkDirDisk},
		{BuildMiB + BuildWorkDirMiB, BuildReserved, WorkDirTmpfs},
	} {
		b := buildPlan(false, true, c.spare)
		if b.Mode != c.mode || b.WorkDir != c.workDir {
			t.Errorf("spare %d MiB: %s/%s, want %s/%s", c.spare, b.Mode, b.WorkDir, c.mode, c.workDir)
		}
		if b.Mode == BuildWindowed && b.ReservedMiB != 0 {
			t.Errorf("spare %d MiB: windowed with %d MiB reserved", c.spare, b.ReservedMiB)
		}
		if b.ReservedMiB > max(c.spare, 0) {
			t.Errorf("spare %d MiB: reserved %d MiB, more than is spare", c.spare, b.ReservedMiB)
		}
	}
}
