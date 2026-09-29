// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

package planner

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/org-runink/river/installer/internal/hw"
)

func loadProbe(t *testing.T, name string) *hw.Probe {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	p := &hw.Probe{}
	if err := json.Unmarshal(b, p); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	if p.Schema != hw.SchemaVersion {
		t.Fatalf("%s: schema %q", name, p.Schema)
	}
	return p
}

func loadManifest(t *testing.T) *Manifest {
	t.Helper()
	f, err := os.Open(filepath.Join("testdata", "models.tiers"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	m, err := ParseManifest(f)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func tier(pl *Plan, name string) TierPlan {
	for _, t := range pl.Tiers {
		if t.Tier == name {
			return t
		}
	}
	return TierPlan{}
}

// tierWant is "status/variant", or "disabled".
type tierWant map[string]string

func TestFixtureProbes(t *testing.T) {
	cases := []struct {
		probe        string
		verdict      string
		class        string
		tiers        tierWant
		layout       string
		special      bool
		wiped        []string // confirm_ids, boot disk first
		refusal      string   // substring of some refusal
		infThreads   int
		reservedCore int
	}{
		{
			probe: "workstation-25g.probe.json", verdict: "degraded", class: "edge",
			tiers: tierWant{
				"embedding": "enabled/embed-small", "stt": "degraded/stt-4b", "tts": "enabled/tts-small",
				"general": "degraded/general-8b", "coder": "disabled", "vision": "disabled",
			},
			layout: "single", wiped: []string{"WS0001"}, infThreads: 6, reservedCore: 2,
		},
		{
			probe: "server-64g.probe.json", verdict: "degraded", class: "standard",
			tiers: tierWant{
				"embedding": "enabled/embed-small", "stt": "enabled/stt-4b", "tts": "enabled/tts-small",
				"general": "degraded/general-27b", "coder": "degraded/coder-14b", "vision": "degraded/vision-2b",
			},
			layout: "mirror", wiped: []string{"NVA0001", "NVA0002"}, infThreads: 14, reservedCore: 2,
		},
		{
			probe: "server-256g-avx512.probe.json", verdict: "ok", class: "large",
			tiers: tierWant{
				"embedding": "enabled/embed-small", "stt": "enabled/stt-4b", "tts": "enabled/tts-small",
				"general": "enabled/general-27b", "coder": "enabled/coder-14b", "vision": "enabled/vision-8b",
			},
			layout: "raidz2", special: true,
			wiped:      []string{"HD000a", "HD000b", "HD000c", "HD000d", "HD000e", "HD000f", "HD000g", "HD000h", "NVB0001", "NVB0002"},
			infThreads: 56, reservedCore: 8,
		},
		{
			probe: "edge-16g.probe.json", verdict: "refused", class: "edge",
			tiers:  tierWant{"stt": "disabled", "general": "disabled"},
			layout: "single", wiped: []string{"SSD0256"}, refusal: "mandatory tier stt does not fit",
			infThreads: 2, reservedCore: 2,
		},
		{
			probe: "gpu-128g.probe.json", verdict: "ok", class: "standard",
			tiers:  tierWant{"general": "enabled/general-27b", "vision": "enabled/vision-8b"},
			layout: "raidz1", wiped: []string{"SSG000a", "SSG000b", "SSG000c"}, infThreads: 21, reservedCore: 3,
		},
	}
	m := loadManifest(t)
	for _, c := range cases {
		t.Run(c.probe, func(t *testing.T) {
			p := loadProbe(t, c.probe)
			pl := Build(p, m, Options{})
			if pl.Verdict != c.verdict {
				t.Errorf("verdict %s, want %s (refusals %v)", pl.Verdict, c.verdict, pl.Refusals)
			}
			if pl.Profile.Class != c.class {
				t.Errorf("class %s, want %s", pl.Profile.Class, c.class)
			}
			for name, want := range c.tiers {
				tp := tier(pl, name)
				got := tp.Status
				if tp.Status != "disabled" {
					got += "/" + tp.Variant
				}
				if got != want {
					t.Errorf("tier %s = %s, want %s (%v)", name, got, want, tp.Reasons)
				}
			}
			if pl.Storage.Layout != c.layout {
				t.Errorf("layout %s, want %s", pl.Storage.Layout, c.layout)
			}
			if (pl.Storage.SpecialVdev != nil) != c.special {
				t.Errorf("special vdev %v, want %v", pl.Storage.SpecialVdev, c.special)
			}
			var ids []string
			for _, d := range pl.Storage.Disks {
				ids = append(ids, d.ConfirmID)
			}
			if strings.Join(ids, ",") != strings.Join(c.wiped, ",") {
				t.Errorf("wiped %v, want %v", ids, c.wiped)
			}
			if c.refusal != "" && !strings.Contains(strings.Join(pl.Refusals, "|"), c.refusal) {
				t.Errorf("refusals %v lack %q", pl.Refusals, c.refusal)
			}
			if pl.CPU.InferenceThreads != c.infThreads || pl.CPU.ReservedCores != c.reservedCore {
				t.Errorf("cpu split %d reserved / %d inference, want %d / %d",
					pl.CPU.ReservedCores, pl.CPU.InferenceThreads, c.reservedCore, c.infThreads)
			}
			// Invariants every plan must hold.
			for _, d := range pl.Storage.Disks {
				for _, pd := range p.Disks {
					if pd.Name == d.Name && (pd.BootMedia || !pd.Eligible) {
						t.Errorf("plan wipes %s, which is boot media or ineligible", d.Name)
					}
				}
			}
			if pl.Verdict != "refused" {
				sum := pl.Memory.ReservedMiB + pl.Memory.ModelsMiB + pl.Memory.HeadroomMiB
				if sum > pl.Memory.TotalMiB {
					t.Errorf("RAM over-committed: %d planned > %d total", sum, pl.Memory.TotalMiB)
				}
				var budget int64
				for _, l := range pl.Memory.Budget {
					budget += l.MiB
				}
				if budget != sum {
					t.Errorf("budget table sums to %d, plan says %d", budget, sum)
				}
			}
			for _, tp := range pl.Tiers {
				if tp.Status == "disabled" {
					continue
				}
				if tp.Env["RAYON_NUM_THREADS"] == "" || tp.Threads < 1 || tp.Threads > pl.CPU.InferenceThreads {
					t.Errorf("tier %s threads %d (env %v), inference threads %d", tp.Tier, tp.Threads, tp.Env, pl.CPU.InferenceThreads)
				}
				if tp.TotalMiB != tp.ResidentMiB+tp.KVBudgetMiB {
					t.Errorf("tier %s total %d != resident %d + kv %d", tp.Tier, tp.TotalMiB, tp.ResidentMiB, tp.KVBudgetMiB)
				}
			}
			if pl.Swap.DiskSwap || !pl.Swap.Zram {
				t.Errorf("swap policy %+v: never disk swap, always zram", pl.Swap)
			}
			if Text(pl) == "" {
				t.Error("empty text rendering")
			}
		})
	}
}

func TestGeneralFallbackIsExplained(t *testing.T) {
	pl := Build(loadProbe(t, "workstation-25g.probe.json"), loadManifest(t), Options{})
	g := tier(pl, "general")
	joined := strings.Join(g.Reasons, "|")
	if !strings.Contains(joined, "general-27b needs") || !strings.Contains(joined, "fell back to general-8b") {
		t.Errorf("general fallback not explained: %v", g.Reasons)
	}
}

func TestSpecialVdevAndUnusedFlash(t *testing.T) {
	pl := Build(loadProbe(t, "server-256g-avx512.probe.json"), loadManifest(t), Options{})
	if got := pl.Storage.SpecialVdev; got == nil || got.Type != "mirror" || strings.Join(got.Disks, " ") != "nvme0n1 nvme1n1" {
		t.Fatalf("special vdev %+v", got)
	}
	unused := map[string]string{}
	for _, u := range pl.Storage.Unused {
		unused[u.Name] = u.Reason
	}
	if !strings.Contains(unused["sdi"], "flash beyond the special-vdev mirror") {
		t.Errorf("sdi should be unused flash, got %q", unused["sdi"])
	}
	if !strings.Contains(unused["sdz"], "boot medium") {
		t.Errorf("boot USB not reported untouched: %q", unused["sdz"])
	}
	if pl.Storage.BootDisk != "sda" {
		t.Errorf("boot disk %s", pl.Storage.BootDisk)
	}
}

func TestMinimumsRefuse(t *testing.T) {
	m := loadManifest(t)
	cases := []struct {
		name   string
		mutate func(p *hw.Probe)
		want   string
	}{
		{"bios", func(p *hw.Probe) { p.Firmware.UEFI = false }, "requires UEFI"},
		{"v2-cpu", func(p *hw.Probe) {
			p.CPU.PsABILevel, p.CPU.PsABI, p.CPU.MissingForV3 = 2, "x86-64-v2", []string{"avx2", "fma"}
		}, "x86-64-v2 (missing avx2 fma)"},
		{"two-cores", func(p *hw.Probe) { p.CPU.PhysicalCores = 2 }, "2 physical cores"},
		{"8g-ram", func(p *hw.Probe) { p.Memory.TotalBytes = 8 << 30 }, "the minimum is 15360 MiB"},
		{"no-disk", func(p *hw.Probe) { p.Disks = p.Disks[1:] }, "no eligible target disk"},
		{"small-disk", func(p *hw.Probe) { p.Disks[0].SizeBytes = 128 << 30 }, "the minimum is 200 GiB"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := loadProbe(t, "server-64g.probe.json")
			if c.name == "no-disk" || c.name == "small-disk" {
				p = loadProbe(t, "workstation-25g.probe.json")
			}
			c.mutate(p)
			pl := Build(p, m, Options{})
			if pl.Verdict != "refused" {
				t.Fatalf("verdict %s, want refused", pl.Verdict)
			}
			if !strings.Contains(strings.Join(pl.Refusals, "|"), c.want) {
				t.Errorf("refusals %v lack %q", pl.Refusals, c.want)
			}
			lab := Build(p, m, Options{Lab: true})
			if lab.Verdict != "degraded" || !lab.Lab {
				t.Errorf("lab override: verdict %s lab %v", lab.Verdict, lab.Lab)
			}
			if _, err := Resolve(pl, p, nil); err == nil {
				t.Error("Resolve accepted a refused plan")
			}
		})
	}
}

func TestNoManifest(t *testing.T) {
	pl := Build(loadProbe(t, "server-64g.probe.json"), nil, Options{})
	if pl.Verdict != "degraded" || len(pl.Tiers) != 0 {
		t.Fatalf("verdict %s tiers %d", pl.Verdict, len(pl.Tiers))
	}
	if !strings.Contains(strings.Join(pl.Warnings, "|"), "no models manifest") {
		t.Errorf("warnings %v", pl.Warnings)
	}
	if pl.Storage.Layout != "mirror" {
		t.Errorf("layout %s", pl.Storage.Layout)
	}
}

func TestLayoutTable(t *testing.T) {
	mk := func(kinds string, sizesGiB ...uint64) *hw.Probe {
		p := loadProbe(t, "server-64g.probe.json")
		p.Disks = nil
		for i, k := range kinds {
			kind := map[rune]string{'n': "nvme", 's': "ssd", 'h': "hdd"}[k]
			sz := uint64(1000)
			if i < len(sizesGiB) {
				sz = sizesGiB[i]
			}
			name := string(rune('a'+i)) + "disk"
			p.Disks = append(p.Disks, hw.Disk{Name: name, Path: "/dev/" + name, Kind: kind, SizeBytes: sz << 30,
				ConfirmID: "S-" + name, Eligible: true})
		}
		return p
	}
	cases := []struct {
		kinds   string
		sizes   []uint64
		layout  string
		vdevs   int
		special bool
		unused  int
	}{
		{"n", nil, "single", 1, false, 0},
		{"nn", nil, "mirror", 1, false, 0},
		{"sss", nil, "raidz1", 1, false, 0},
		{"ssss", nil, "raidz1", 1, false, 0},
		{"hhhh", nil, "raidz2", 1, false, 0},
		{"hhh", nil, "raidz1", 1, false, 0},
		{"hhhhhh", nil, "raidz2", 1, false, 0},
		{"hhhhnn", nil, "raidz2", 1, true, 0},
		{"hhhhn", nil, "raidz2", 1, false, 1},                      // one flash device: no special vdev
		{"nnh", nil, "mirror", 1, false, 1},                        // the HDD is left out of a flash pool
		{"nnn", []uint64{1000, 1000, 4000}, "mirror", 1, false, 1}, // size mismatch
		{"hhhhhhhhhhhhhh", nil, "raidz2", 2, false, 0},             // 14 disks -> 2 x 7-wide
		{"hhhhhhhhhhhhhhh", nil, "raidz2", 2, false, 1},            // 15 disks -> 2 x 7-wide + 1 spare
		{"n", []uint64{63}, "none", 0, false, 1},                   // below the per-disk minimum
	}
	for _, c := range cases {
		t.Run(c.kinds, func(t *testing.T) {
			pl := Build(mk(c.kinds, c.sizes...), loadManifest(t), Options{})
			s := pl.Storage
			if s.Layout != c.layout || len(s.DataVdevs) != c.vdevs || (s.SpecialVdev != nil) != c.special || len(s.Unused) != c.unused {
				t.Errorf("got layout=%s vdevs=%d special=%v unused=%v", s.Layout, len(s.DataVdevs), s.SpecialVdev != nil, s.Unused)
			}
			if c.vdevs > 1 {
				w := len(s.DataVdevs[0].Disks)
				for _, v := range s.DataVdevs {
					if len(v.Disks) != w {
						t.Errorf("unequal vdev widths %v", s.DataVdevs)
					}
				}
			}
		})
	}
}

// TestWorkstationProfile: a Runink River (workstation) plan is made against desktop minimums
// (x86-64-v3, 2 physical cores, "8 GB", one eligible disk of 64 GiB, UEFI), with no model
// tiers and no k0s/platform reserve; the server minimums must not refuse it.
func TestWorkstationProfile(t *testing.T) {
	ws := Options{Profile: ProfileWorkstation}
	for _, c := range []struct {
		probe, layout string
		wiped         []string
	}{
		{"workstation-25g.probe.json", "single", []string{"WS0001"}},
		// Refused as a server (16 GiB, 4 cores, a 256 GB disk under the 200 GiB pool minimum).
		{"edge-16g.probe.json", "single", []string{"SSD0256"}},
	} {
		t.Run(c.probe, func(t *testing.T) {
			// The manifest is passed on purpose: the workstation profile must ignore it.
			pl := Build(loadProbe(t, c.probe), loadManifest(t), ws)
			if pl.Verdict != "ok" || len(pl.Refusals) != 0 {
				t.Fatalf("verdict %s, refusals %v, warnings %v", pl.Verdict, pl.Refusals, pl.Warnings)
			}
			if pl.Profile.ISO != "river" || len(pl.Tiers) != 0 || pl.Memory.ModelsMiB != 0 {
				t.Errorf("profile %+v, %d tiers, %d MiB of models", pl.Profile, len(pl.Tiers), pl.Memory.ModelsMiB)
			}
			var budget int64
			for _, l := range pl.Memory.Budget {
				if l.Item == "k0s" || l.Item == "platform" || strings.HasPrefix(l.Item, "tier:") {
					t.Errorf("workstation budget has %s", l.Item)
				}
				budget += l.MiB
			}
			if budget != pl.Memory.ReservedMiB+pl.Memory.HeadroomMiB || pl.Memory.UnplannedMiB <= 0 {
				t.Errorf("budget %d, reserved %d + headroom %d, unplanned %d", budget, pl.Memory.ReservedMiB, pl.Memory.HeadroomMiB, pl.Memory.UnplannedMiB)
			}
			if pl.Storage.Layout != c.layout || len(pl.Storage.Disks) != len(c.wiped) || pl.Storage.Disks[0].ConfirmID != c.wiped[0] {
				t.Errorf("storage %s %+v", pl.Storage.Layout, pl.Storage.Disks)
			}
			if !strings.Contains(Text(pl), "Runink River (workstation)") {
				t.Error("text rendering does not name the workstation profile")
			}
		})
	}
	if pl := Build(loadProbe(t, "edge-16g.probe.json"), loadManifest(t), Options{}); pl.Verdict != "refused" {
		t.Fatalf("the server profile must still refuse the edge machine: %s", pl.Verdict)
	}

	// The desktop minimums, one at a time, on the laptop fixture.
	for name, c := range map[string]struct {
		mut     func(p *hw.Probe)
		refusal string
	}{
		"2 cores, 8 GB is enough": {func(p *hw.Probe) { p.CPU.PhysicalCores = 2; p.Memory.TotalBytes = 7680 << 20 }, ""},
		"1 core":                  {func(p *hw.Probe) { p.CPU.PhysicalCores = 1 }, "physical cores: the minimum is 2"},
		"6 GiB":                   {func(p *hw.Probe) { p.Memory.TotalBytes = 6 << 30 }, "the minimum is 7168 MiB (8 GB installed)"},
		"x86-64-v2":               {func(p *hw.Probe) { p.CPU.PsABILevel = 2; p.CPU.PsABI = "x86-64-v2" }, "x86-64-v3"},
		"legacy BIOS":             {func(p *hw.Probe) { p.Firmware.UEFI = false }, "UEFI"},
		"only a 32 GiB disk": {func(p *hw.Probe) {
			for i := range p.Disks {
				p.Disks[i].SizeBytes = 32 << 30
			}
		}, "no eligible target disk"},
	} {
		t.Run(name, func(t *testing.T) {
			p := loadProbe(t, "workstation-25g.probe.json")
			c.mut(p)
			pl := Build(p, nil, ws)
			if c.refusal == "" {
				if pl.Verdict != "ok" {
					t.Fatalf("verdict %s, refusals %v", pl.Verdict, pl.Refusals)
				}
				return
			}
			if pl.Verdict != "refused" || !strings.Contains(strings.Join(pl.Refusals, "|"), c.refusal) {
				t.Fatalf("verdict %s, refusals %v, want one mentioning %q", pl.Verdict, pl.Refusals, c.refusal)
			}
		})
	}
}

// A pending mandatory tier is reported, never placed or budgeted, and does not refuse.
func TestPendingTierPlan(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("testdata", "models.tiers"))
	if err != nil {
		t.Fatal(err)
	}
	src := strings.Replace(string(b),
		"tier=embedding variant=embed-small   rank=1 resident_mib=1024  kv_mib_per_1k=112 ctx_min=2048  ctx_max=2048",
		"pending=embedding", 1)
	if src == string(b) {
		t.Fatal("fixture embedding line not found; the test is not exercising pending")
	}
	m, err := ParseManifest(strings.NewReader(src))
	if err != nil {
		t.Fatal(err)
	}
	pl := Build(loadProbe(t, "server-256g-avx512.probe.json"), m, Options{})
	e := tier(pl, "embedding")
	if e.Status != "pending" || !e.Mandatory || e.Placed() || e.TotalMiB != 0 || e.Threads != 0 {
		t.Fatalf("embedding tier: %+v", e)
	}
	if pl.Verdict != "degraded" || len(pl.Refusals) != 0 {
		t.Fatalf("verdict %s, refusals %v; want degraded with none", pl.Verdict, pl.Refusals)
	}
	for _, l := range pl.Memory.Budget {
		if l.Item == "tier:embedding" {
			t.Fatalf("pending tier budgeted: %+v", l)
		}
	}
	if !strings.Contains(strings.Join(pl.Warnings, "\n"), "tier embedding is pending") {
		t.Fatalf("no pending warning in %v", pl.Warnings)
	}
}

// The shipped models.tiers must parse and cross-check against the shipped models.lock.
func TestShippedManifest(t *testing.T) {
	f, err := os.Open(filepath.Join("..", "..", "..", "models.tiers"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	m, err := ParseManifest(f)
	if err != nil {
		t.Fatal(err)
	}
	l, err := os.Open(filepath.Join("..", "..", "..", "models.lock"))
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	if err := m.ApplyLock(l); err != nil {
		t.Fatal(err)
	}
	if err := m.Resolve(); err != nil {
		t.Fatal(err)
	}
	for _, tr := range MandatoryTiers {
		if len(m.ByTier(tr)) == 0 && !m.Pending[tr] {
			t.Errorf("mandatory tier %s has neither a variant nor pending=", tr)
		}
	}
}
