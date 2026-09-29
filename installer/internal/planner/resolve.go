// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

package planner

import (
	"fmt"
	"sort"
	"strings"

	"github.com/org-runink/river/installer/internal/hw"
)

// Resolved maps a plan onto the disks present NOW. Kernel names (sda, nvme1n1) are not
// stable across boots or hot-plug, so a plan identifies every disk by confirm_id (its
// serial) and is re-resolved against a fresh probe immediately before anything is wiped.
type Resolved struct {
	BootDisk     string   // /dev/<name>
	DataDisks    []string // /dev/<name>, in plan vdev order
	SpecialDisks []string
	Topology     string // single | mirror | raidz1 | raidz2
	VdevWidth    int    // disks per data vdev
}

// Resolve checks that the plan is installable on the machine described by fresh and that
// the operator confirmed EVERY disk the plan will wipe by its confirm_id — no more, no less.
// confirmed may carry each id once; order does not matter.
func Resolve(pl *Plan, fresh *hw.Probe, confirmed []string) (*Resolved, error) {
	if pl.Schema != SchemaVersion {
		return nil, fmt.Errorf("plan schema %q, want %q", pl.Schema, SchemaVersion)
	}
	if pl.Verdict == "refused" {
		return nil, fmt.Errorf("the plan is REFUSED: %s", strings.Join(pl.Refusals, "; "))
	}
	if len(pl.Storage.Disks) == 0 {
		return nil, fmt.Errorf("the plan selects no target disk")
	}

	byID := map[string][]hw.Disk{}
	for _, d := range fresh.Disks {
		byID[d.ConfirmID] = append(byID[d.ConfirmID], d)
	}
	current := map[string]string{} // plan name -> current /dev path
	want := map[string]bool{}
	for _, pd := range pl.Storage.Disks {
		want[pd.ConfirmID] = true
		ds := byID[pd.ConfirmID]
		switch {
		case len(ds) == 0:
			return nil, fmt.Errorf("disk %s (serial %s) from the plan is not present now", pd.Name, pd.ConfirmID)
		case len(ds) > 1:
			return nil, fmt.Errorf("serial %s matches %d disks; refusing to guess", pd.ConfirmID, len(ds))
		}
		d := ds[0]
		if d.BootMedia {
			return nil, fmt.Errorf("disk %s (serial %s) is the installer's boot medium", d.Name, d.ConfirmID)
		}
		if !d.Eligible {
			return nil, fmt.Errorf("disk %s (serial %s) is not eligible now: %s", d.Name, d.ConfirmID, strings.Join(d.IneligibleReasons, "; "))
		}
		if d.SizeBytes != pd.SizeBytes {
			return nil, fmt.Errorf("disk serial %s is %d bytes now, the plan says %d", pd.ConfirmID, d.SizeBytes, pd.SizeBytes)
		}
		current[pd.Name] = d.Path
	}

	got := map[string]bool{}
	for _, c := range confirmed {
		c = strings.TrimSpace(c)
		if c == "" {
			continue
		}
		if !want[c] {
			return nil, fmt.Errorf("confirmed serial %q is not a disk this plan will wipe", c)
		}
		got[c] = true
	}
	var missing []string
	for id := range want {
		if !got[id] {
			missing = append(missing, id)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return nil, fmt.Errorf("not confirmed by serial: %s (every disk the plan wipes must be confirmed)", strings.Join(missing, ", "))
	}

	r := &Resolved{Topology: pl.Storage.Layout, BootDisk: current[pl.Storage.BootDisk]}
	for _, v := range pl.Storage.DataVdevs {
		r.VdevWidth = len(v.Disks)
		for _, n := range v.Disks {
			r.DataDisks = append(r.DataDisks, current[n])
		}
	}
	if pl.Storage.SpecialVdev != nil {
		for _, n := range pl.Storage.SpecialVdev.Disks {
			r.SpecialDisks = append(r.SpecialDisks, current[n])
		}
	}
	return r, nil
}

// Env renders the resolved plan as POSIX sh assignments for the installer steps.
func (r *Resolved) Env(pl *Plan) string {
	var b strings.Builder
	kv := func(k, v string) { fmt.Fprintf(&b, "%s=%s\n", k, shQuote(v)) }
	kv("RUNINK_PLAN_VERDICT", pl.Verdict)
	kv("RUNINK_DISK", r.BootDisk)
	kv("RUNINK_POOL_TOPOLOGY", r.Topology)
	kv("RUNINK_POOL_DISKS", strings.Join(r.DataDisks, " "))
	kv("RUNINK_POOL_VDEV_WIDTH", fmt.Sprint(r.VdevWidth))
	kv("RUNINK_SPECIAL_DISKS", strings.Join(r.SpecialDisks, " "))
	kv("RUNINK_ZFS_ARC_MAX", fmt.Sprint(pl.ZFS.ARCMaxBytes))
	kv("RUNINK_ZRAM_SIZE", fmt.Sprintf("%dM", pl.Swap.ZramMiB))
	kv("RUNINK_INFERENCE_THREADS", fmt.Sprint(pl.CPU.InferenceThreads))
	return b.String()
}

func shQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
