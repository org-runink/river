package report

import (
	"bytes"
	"strings"
	"testing"

	"github.com/org-runink/river/bench/analytics/internal/sysinfo"
)

func run(kernel string, samples ...float64) *Results {
	r := &Results{Schema: Schema, Reps: len(samples), Host: sysinfo.Info{KernelRelease: kernel, CPUModel: "cpu", MemTotalKiB: 1}}
	r.Add(Metric{Suite: "s", Name: "m", Unit: "x", HigherIsBetter: true, Samples: samples})
	return r
}

func TestCompareMarksNoiseAndChange(t *testing.T) {
	var b bytes.Buffer
	base := run("a", 100, 100, 100)
	noisy := run("b", 95, 101, 110) // median 101, spread ~15% > 1% change
	clear := run("c", 120, 120, 120)
	if err := Compare(&b, []*Results{base, noisy, clear}); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	if !strings.Contains(out, "(~+1.0%)") {
		t.Errorf("expected a within-noise marker:\n%s", out)
	}
	if !strings.Contains(out, "(+20.0%)") {
		t.Errorf("expected a +20%% change:\n%s", out)
	}
}

func TestCompareShowsMissingAsNotRun(t *testing.T) {
	var b bytes.Buffer
	other := &Results{Schema: Schema, Host: sysinfo.Info{KernelRelease: "b", CPUModel: "cpu", MemTotalKiB: 1}}
	if err := Compare(&b, []*Results{run("a", 1, 1, 1), other}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), "not run") {
		t.Errorf("missing metric must show as not run:\n%s", b.String())
	}
}

func TestCompareWarnsOnDifferentHardwareAndSmoke(t *testing.T) {
	var b bytes.Buffer
	x := run("b", 1)
	x.Host.CPUModel = "other"
	x.Smoke = true
	_ = Compare(&b, []*Results{run("a", 1), x})
	if !strings.Contains(b.String(), "different hardware") || !strings.Contains(b.String(), "smoke run") {
		t.Errorf("expected hardware and smoke warnings:\n%s", b.String())
	}
}
