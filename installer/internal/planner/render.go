// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

package planner

import (
	"fmt"
	"strings"
)

func gib(b uint64) float64 { return float64(b) / (1 << 30) }

// Text renders the plan for an operator: verdict first, then the tables they must check.
func Text(pl *Plan) string {
	var b strings.Builder
	w := func(format string, a ...any) { fmt.Fprintf(&b, format, a...) }

	w("RIVER install plan — verdict: %s", strings.ToUpper(pl.Verdict))
	if pl.Lab {
		w("  (LAB OVERRIDE)")
	}
	w("\n\n")
	label := "REFUSED"
	if pl.Verdict != "refused" {
		label = "WAIVED (lab)"
	}
	for _, r := range pl.Refusals {
		w("  %s: %s\n", label, r)
	}
	for _, r := range pl.Warnings {
		w("  WARNING: %s\n", r)
	}
	if len(pl.Refusals)+len(pl.Warnings) > 0 {
		w("\n")
	}

	h := pl.Hardware
	w("Hardware  %s, %d physical cores (%d threads, %d socket(s)), %.1f GiB RAM, UEFI=%v, TPM2=%v, GPUs=%d, class=%s\n",
		h.PsABI, h.PhysicalCores, h.Threads, h.Sockets, float64(h.MemTotalMiB)/1024, h.UEFI, h.TPM2, h.GPUs, pl.Profile.Class)
	if pl.Profile.ISO == ISONames(ProfileWorkstation) {
		w("Profile   Runink River (workstation): desktop minimums, no model tiers, no k0s/platform reserve\n\n")
	} else {
		w("CPU       %d cores reserved for the node and k0s, %d inference threads (RAYON_NUM_THREADS), build %s\n\n",
			pl.CPU.ReservedCores, pl.CPU.InferenceThreads, pl.CPU.ISABuild)
	}

	w("Model tiers\n")
	w("  %-10s %-9s %-4s %-18s %8s %9s %8s %8s %7s\n", "tier", "status", "req", "variant", "context", "resident", "kv", "total", "threads")
	for _, t := range pl.Tiers {
		req := ""
		if t.Mandatory {
			req = "yes"
		}
		if !t.Placed() {
			w("  %-10s %-9s %-4s %s\n", t.Tier, t.Status, req, strings.Join(t.Reasons, "; "))
			continue
		}
		w("  %-10s %-9s %-4s %-18s %8d %9d %8d %8d %7d\n", t.Tier, t.Status, req, t.Variant, t.ContextTokens,
			t.ResidentMiB, t.KVBudgetMiB, t.TotalMiB, t.Threads)
		for _, r := range t.Reasons {
			w("  %-10s   - %s\n", "", r)
		}
	}
	if len(pl.Tiers) == 0 {
		w("  (not planned: no models manifest)\n")
	}

	w("\nRAM budget (MiB)\n")
	for _, l := range pl.Memory.Budget {
		w("  %-16s %8d  %s\n", l.Item, l.MiB, l.Note)
	}
	w("  %-16s %8d\n", "= planned", pl.Memory.ReservedMiB+pl.Memory.ModelsMiB+pl.Memory.HeadroomMiB)
	w("  %-16s %8d\n", "total RAM", pl.Memory.TotalMiB)
	w("  %-16s %8d\n", "unplanned", pl.Memory.UnplannedMiB)

	s := pl.Storage
	w("\nStorage   layout=%s, ~%.0f GiB usable, %s\n", s.Layout, gib(s.UsableBytes), s.EncryptionNote)
	for _, v := range s.DataVdevs {
		w("  data    %-7s %s\n", v.Type, strings.Join(v.Disks, " "))
	}
	if s.SpecialVdev != nil {
		w("  special %-7s %s  (metadata on flash)\n", s.SpecialVdev.Type, strings.Join(s.SpecialVdev.Disks, " "))
	}
	if len(s.Disks) == 0 {
		w("  no disk selected: nothing will be erased\n")
	} else {
		w("  THESE DISKS WILL BE ERASED:\n")
	}
	for _, d := range s.Disks {
		boot := ""
		if d.Name == s.BootDisk {
			boot = " [boot: ESP + GRUB]"
		}
		w("    %-10s %8.1f GiB %-4s serial=%s  %s%s\n", d.Name, gib(d.SizeBytes), d.Kind, d.ConfirmID, d.Model, boot)
	}
	for _, u := range s.Unused {
		w("  untouched %-10s %s\n", u.Name, u.Reason)
	}
	w("\nZFS       zfs_arc_max=%d MiB\n", pl.ZFS.ARCMaxBytes>>20)
	if b := pl.Build; b.Mode != "" && b.Mode != BuildNone {
		w("Build     %s, %d MiB reserved, work dir %s (%d MiB)\n", b.Mode, b.ReservedMiB, b.WorkDir, b.WorkDirMiB)
	}
	w("Swap      no disk swap; zram %d MiB (%s)\n", pl.Swap.ZramMiB, pl.Swap.Algorithm)
	w("Network   %d physical NIC(s), %d with link, global IPv6=%v\n", pl.Network.PhysicalNICs, pl.Network.WithCarrier, pl.Network.IPv6Global)
	w("Compute   %s", pl.Accelerator.Mode)
	if pl.Accelerator.Note != "" {
		w(" — %s", pl.Accelerator.Note)
	}
	w("\n")
	for _, n := range pl.Notes {
		w("Note      %s\n", n)
	}
	return b.String()
}

// DiskList is one tab-separated line per disk the plan wipes, boot disk first:
// name, confirm_id, size in GiB, kind, role (boot | data | special), model.
func DiskList(pl *Plan) string {
	var b strings.Builder
	special := map[string]bool{}
	if pl.Storage.SpecialVdev != nil {
		for _, n := range pl.Storage.SpecialVdev.Disks {
			special[n] = true
		}
	}
	for _, d := range pl.Storage.Disks {
		role := "data"
		switch {
		case d.Name == pl.Storage.BootDisk:
			role = "boot"
		case special[d.Name]:
			role = "special"
		}
		model := strings.NewReplacer("\t", " ", "\n", " ").Replace(d.Model)
		fmt.Fprintf(&b, "%s\t%s\t%.0f\t%s\t%s\t%s\n", d.Name, d.ConfirmID, gib(d.SizeBytes), d.Kind, role, model)
	}
	return b.String()
}
