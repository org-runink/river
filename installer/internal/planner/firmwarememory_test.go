// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

package planner

import (
	"strings"
	"testing"

	"github.com/org-runink/river/installer/internal/hw"
)

// The reported case: 128 GiB soldered, the firmware gives 64 GiB to the iGPU, Linux sees 64.
// The plan must say so plainly, name the setting to change, never refuse because of it, and
// pass the numbers on to the steps.
func TestFirmwareGPUReserveIsReported(t *testing.T) {
	p := loadProbe(t, "evo-x3-64g-of-128g.probe.json")
	pl := Build(p, nil, Options{Profile: ProfileServer})
	f := pl.MemoryFirmware
	if f == nil || f.Kind != MemFirmwareGPU {
		t.Fatalf("finding = %+v", f)
	}
	if f.InstalledMiB != 131072 || f.GPUReservedMiB != 65536 || f.KernelMiB != p.MemTotalMiB() {
		t.Errorf("numbers: %+v", f)
	}
	for _, want := range []string{"128.0 GB", "64.0 GB", "dedicated video memory"} {
		if !strings.Contains(f.Summary, want) {
			t.Errorf("summary lacks %q: %s", want, f.Summary)
		}
	}
	for _, want := range []string{"UMA Frame Buffer", "firmware setup", "You can install now"} {
		if !strings.Contains(f.Fix, want) {
			t.Errorf("fix lacks %q: %s", want, f.Fix)
		}
	}
	for _, r := range pl.Refusals {
		if strings.Contains(r, "firmware") || strings.Contains(r, "video memory") {
			t.Errorf("the memory finding must never be a refusal: %s", r)
		}
	}
	if txt := Text(pl); !strings.Contains(txt, "WHY THE MEMORY IS SPLIT\n  This computer has 128.0 GB") {
		t.Errorf("the plan text lacks the memory block:\n%s", txt)
	}
	r, err := Resolve(pl, p, []string{"SSG000a", "SSG000b", "SSG000c"})
	if err != nil {
		t.Skipf("fixture disks do not resolve: %v", err)
	}
	env := r.Env(pl)
	for _, want := range []string{"RUNINK_MEM_INSTALLED_MIB='131072'", "RUNINK_MEM_GPU_RESERVED_MIB='65536'"} {
		if !strings.Contains(env, want+"\n") {
			t.Errorf("env lacks %s:\n%s", want, env)
		}
	}
}

func TestFirmwareMemoryFindingCases(t *testing.T) {
	gpu := func(vramGiB uint64) []hw.GPU {
		return []hw.GPU{{Vendor: "amd", Integrated: true, VRAMKnown: true, VRAMBytes: vramGiB << 30}}
	}
	mk := func(installedGiB, kernelGiB uint64, known bool, gpus []hw.GPU) *hw.Probe {
		p := &hw.Probe{GPUs: gpus}
		p.Memory = hw.Memory{TotalBytes: kernelGiB << 30, InstalledBytes: installedGiB << 30, InstalledKnown: known}
		return p
	}
	for _, c := range []struct {
		name string
		p    *hw.Probe
		want string // "" = no finding
	}{
		{"nothing missing", mk(128, 126, true, gpu(1)), ""},
		{"normal firmware loss on a big machine", mk(128, 125, true, gpu(1)), ""},
		{"gpu holds the missing half", mk(128, 63, true, gpu(64)), MemFirmwareGPU},
		{"missing with a small gpu reserve", mk(128, 63, true, gpu(1)), MemFirmwareOther},
		{"installed unknown, big gpu reserve", mk(0, 63, false, gpu(64)), MemFirmwareGPU},
		{"installed unknown, ordinary gpu reserve", mk(0, 63, false, gpu(1)), ""},
		{"installed unknown, no gpu", mk(0, 63, false, nil), ""},
		{"discrete card is not an unexplained reserve", mk(64, 63, true, []hw.GPU{{Vendor: "amd", VRAMKnown: true, VRAMBytes: 16 << 30}}), ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := memoryFinding(c.p)
			switch {
			case c.want == "" && f != nil:
				t.Errorf("unexpected finding %+v", f)
			case c.want != "" && (f == nil || f.Kind != c.want):
				t.Errorf("finding = %+v, want kind %s", f, c.want)
			case f != nil && (f.Summary == "" || f.Consequence == "" || f.Fix == ""):
				t.Errorf("finding without its three sentences: %+v", f)
			}
		})
	}
}

// The breakdown is always there, even on a machine where nothing is wrong, and a part no
// source could tell reads "unknown" -- never a guessed number.
func TestMemoryBreakdownAlwaysShownAndNeverGuesses(t *testing.T) {
	evo := Build(loadProbe(t, "evo-x3-64g-of-128g.probe.json"), nil, Options{Profile: ProfileServer})
	want := []string{
		"System memory (what Linux can use): 62.9 GB",
		"Graphics memory (reserved by the firmware): 64.0 GB",
		"Installed total: 128.0 GB",
		"Graphics can borrow from system memory: up to 15.0 GB now",
	}
	for i, w := range want {
		if got := evo.MemoryBreakdown.Lines[i]; !strings.HasPrefix(got, w) {
			t.Errorf("line %d = %q, want prefix %q", i, got, w)
		}
	}
	if txt := Text(evo); !strings.Contains(txt, "MEMORY\n  System memory") {
		t.Errorf("plan text lacks the breakdown:\n%s", txt)
	}

	// An ordinary server: nothing wrong, the breakdown is still printed.
	srv := Build(loadProbe(t, "server-64g.probe.json"), nil, Options{Profile: ProfileServer})
	if srv.MemoryFirmware != nil || srv.MemoryBreakdown == nil || len(srv.MemoryBreakdown.Lines) < 3 {
		t.Fatalf("server: finding=%v breakdown=%+v", srv.MemoryFirmware, srv.MemoryBreakdown)
	}
	if !strings.Contains(strings.Join(srv.MemoryBreakdown.Lines, "\n"), "Installed total: unknown") {
		t.Errorf("no SMBIOS must read unknown: %v", srv.MemoryBreakdown.Lines)
	}

	// An integrated GPU whose driver reports nothing: unknown, not 0.
	p := loadProbe(t, "evo-x3-64g-of-128g.probe.json")
	p.GPUs[0].VRAMKnown, p.GPUs[0].GTTKnown = false, false
	p.Memory.InstalledKnown = false
	got := strings.Join(Build(p, nil, Options{Profile: ProfileServer}).MemoryBreakdown.Lines, "\n")
	for _, w := range []string{"reserved by the firmware): unknown", "Installed total: unknown", "borrow from system memory: unknown"} {
		if !strings.Contains(got, w) {
			t.Errorf("missing %q in:\n%s", w, got)
		}
	}
}
