// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

package planner

import (
	"strings"
	"testing"
)

// A node whose GPU has no memory of its own serves models out of the same MemTotal the ARC
// caches into, so zfs_arc_max is capped to ARCUnifiedMaxMiB instead of RAM/16 (owner, for
// GB10/Grace-class hardware: inference wins, cap ARC hard).
//
// These run on x86_64 against probe fixtures, which is the point: the one aarch64 box we have
// is the training host, so a rule that could only be exercised on real GB10 would ship
// unexercised. The condition is "every GPU is integrated", not a board allowlist, precisely so
// it is checkable here.
func TestUnifiedMemoryCapsARC(t *testing.T) {
	for _, c := range []struct {
		name    string
		probe   string
		profile string
		wantARC string // the RUNINK_ZFS_ARC_MAX line the installer consumes
		unified bool
	}{
		// 128841 MiB of unified memory: RAM/16 would be 8052 MiB. Capped to 2048 MiB, which
		// hands 6004 MiB back to the models.
		{"unified server is capped", "unified-128g.probe.json", ProfileServer,
			"RUNINK_ZFS_ARC_MAX='2147483648'", true},
		// The same box with two DISCRETE GPUs is not unified: the models have memory of their
		// own, so RAM/16 (8052 MiB) is the right reservation and must be left alone. This is
		// the regression that matters -- a cap applied here would shrink the cache on every
		// ordinary GPU server for no reason.
		{"discrete GPUs are untouched", "gpu-128g.probe.json", ProfileServer,
			"RUNINK_ZFS_ARC_MAX='8443133952'", false}, // 8052 MiB = RAM/16, unchanged
		// A workstation plans no model tiers, so nothing competes for the memory and the cap
		// would cost cache for no gain -- even though its iGPU is integrated.
		{"workstation is untouched", "workstation-25g.probe.json", ProfileWorkstation,
			"", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			p := loadProbe(t, c.probe)
			ws := c.profile == ProfileWorkstation
			got, why := unifiedMemory(p, ws)
			if got != c.unified {
				t.Fatalf("unifiedMemory = %v (%q), want %v", got, why, c.unified)
			}
			if got && why == "" {
				t.Error("unified memory reported with no reason to show the operator")
			}

			pl := Build(p, nil, Options{Profile: c.profile})
			if c.wantARC != "" {
				r, err := Resolve(pl, p, []string{"SSG000a", "SSG000b", "SSG000c"})
				if err != nil {
					t.Fatal(err)
				}
				if env := r.Env(pl); !strings.Contains(env, c.wantARC+"\n") {
					t.Errorf("env lacks %s:\n%s", c.wantARC, env)
				}
			}

			// The budget line must say WHY, not just carry a number: an operator reading the
			// plan has to be able to tell a hard cap from the ordinary RAM/16 reservation.
			var line string
			for _, b := range pl.Memory.Budget {
				if b.Item == "zfs-arc" {
					line = b.Note
				}
			}
			if line == "" {
				t.Fatal("no zfs-arc budget line")
			}
			switch {
			case c.unified && !strings.Contains(line, "capped"):
				t.Errorf("unified node, but the budget line reads as a normal reservation: %q", line)
			case !c.unified && strings.Contains(line, "capped"):
				t.Errorf("not a unified node, but the budget line claims a cap: %q", line)
			}
		})
	}
}

// The cap only ever lowers the reservation. It must never raise ARC on a small unified box,
// where RAM/16 is already below the ceiling.
func TestUnifiedCapNeverRaisesARC(t *testing.T) {
	p := loadProbe(t, "unified-128g.probe.json")
	p.Memory.TotalBytes = 16 << 30 // RAM/16 = 1 GiB, the floor, well under the 2 GiB cap
	pl := Build(p, nil, Options{Profile: ProfileServer})
	for _, b := range pl.Memory.Budget {
		if b.Item == "zfs-arc" && b.MiB > ARCMinMiB {
			t.Errorf("ARC rose to %d MiB on a 16 GiB unified box; the floor is %d", b.MiB, ARCMinMiB)
		}
	}
}
