// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

// Package hw runs the installer's hardware tools and turns their output into summaries.
//
// river-guide does not probe hardware itself. It calls the medium's own read-only tools,
// whose CLI and JSON schema are a published contract (docs/INSTALLER-HARDWARE.md):
//
//	river-hwprobe --json                                     schema river.hwprobe/v1
//	river-plan --probe <file> --manifest <file> --json       the install plan
//
// Only the fields named in probeView are ever read, so serials, WWNs, by-id paths and
// MAC addresses in the probe document never reach a summary — they are not decoded at
// all. The remote summary additionally passes through package redact.
package hw

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Tools locates the two binaries and the plan's model manifest.
type Tools struct {
	HWProbe  string // default river-hwprobe
	Plan     string // default river-plan
	Manifest string // models.tiers the planner reads
	WorkDir  string // where the probe document is written for the planner (tmpfs)
	Timeout  time.Duration
}

// Result is one probe (and, when the planner is available, one plan).
type Result struct {
	ProbeJSON []byte
	PlanJSON  []byte
	Probe     probeView
	PlanErr   error
}

type probeView struct {
	Schema   string `json:"schema"`
	Firmware struct {
		UEFI       bool  `json:"uefi"`
		SecureBoot *bool `json:"secure_boot"`
	} `json:"firmware"`
	Host struct {
		Virtual bool `json:"virtual"`
	} `json:"host"`
	CPU struct {
		Vendor        string   `json:"vendor"`
		Model         string   `json:"model"`
		PsABI         string   `json:"psabi"`
		MissingForV3  []string `json:"missing_for_v3"`
		Sockets       int      `json:"sockets"`
		PhysicalCores int      `json:"physical_cores"`
		Threads       int      `json:"threads"`
		AVX2          bool     `json:"avx2"`
		AVX512F       bool     `json:"avx512f"`
	} `json:"cpu"`
	Memory struct {
		TotalBytes uint64 `json:"total_bytes"`
	} `json:"memory"`
	GPUs []struct {
		Vendor     string `json:"vendor"`
		Integrated bool   `json:"integrated"`
		VRAMBytes  uint64 `json:"vram_bytes"`
		VRAMKnown  bool   `json:"vram_known"`
	} `json:"gpus"`
	Disks []struct {
		Name      string `json:"name"`
		SizeBytes uint64 `json:"size_bytes"`
		Kind      string `json:"kind"`
		Transport string `json:"transport"`
		BootMedia bool   `json:"boot_media"`
		Eligible  bool   `json:"eligible"`
		ZFSMember bool   `json:"zfs_member"`
	} `json:"disks"`
	NICs []struct {
		Physical   bool `json:"physical"`
		Wireless   bool `json:"wireless"`
		Carrier    bool `json:"carrier"`
		SpeedMbps  int  `json:"speed_mbps"`
		IPv6Global bool `json:"ipv6_global"`
	} `json:"nics"`
	TPM struct {
		Present bool   `json:"present"`
		Version string `json:"version"`
	} `json:"tpm"`
	Warnings []string `json:"warnings"`
}

func (t Tools) withDefaults() Tools {
	if t.HWProbe == "" {
		t.HWProbe = "river-hwprobe"
	}
	if t.Plan == "" {
		t.Plan = "river-plan"
	}
	if t.WorkDir == "" {
		t.WorkDir = os.TempDir()
	}
	if t.Timeout == 0 {
		t.Timeout = 60 * time.Second
	}
	return t
}

// Run probes, then plans when a manifest is configured.
func (t Tools) Run(ctx context.Context) (*Result, error) {
	t = t.withDefaults()
	probe, err := run(ctx, t.Timeout, t.HWProbe, "--json")
	if err != nil {
		return nil, fmt.Errorf("%s --json: %w", t.HWProbe, err)
	}
	r := &Result{ProbeJSON: probe}
	if err := json.Unmarshal(probe, &r.Probe); err != nil {
		return nil, fmt.Errorf("%s --json: decode: %w", t.HWProbe, err)
	}
	if !strings.HasPrefix(r.Probe.Schema, "river.hwprobe/") {
		return nil, fmt.Errorf("%s: unexpected schema %q", t.HWProbe, r.Probe.Schema)
	}
	if err := os.MkdirAll(t.WorkDir, 0o700); err != nil {
		return nil, err
	}
	pf := filepath.Join(t.WorkDir, "probe.json")
	if err := os.WriteFile(pf, probe, 0o600); err != nil {
		return nil, err
	}
	// No models.tiers on the medium (the contract allows it): plan hardware and storage only.
	args := []string{"--probe", pf, "--no-models", "--json"}
	if _, err := os.Stat(t.Manifest); t.Manifest != "" && err == nil {
		args = []string{"--probe", pf, "--manifest", t.Manifest, "--json"}
	}
	// Exit 3 is verdict "refused": the plan is still printed, and it is the answer.
	plan, err := run(ctx, t.Timeout, t.Plan, args...)
	if err != nil && exitCode(err) == 3 && json.Valid(plan) {
		err = nil
	}
	if err != nil {
		r.PlanErr = fmt.Errorf("%s: %w", t.Plan, err)
	} else if !json.Valid(plan) {
		r.PlanErr = fmt.Errorf("%s: output is not JSON", t.Plan)
	} else {
		r.PlanJSON = plan
	}
	return r, nil
}

func run(ctx context.Context, timeout time.Duration, name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var out, errb bytes.Buffer
	cmd := exec.CommandContext(ctx, name, args...) // #nosec G204 -- callers pass fixed tool paths (river-hwprobe, river-plan), never remote input
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errb.String())
		if len(msg) > 400 {
			msg = msg[:400]
		}
		return out.Bytes(), fmt.Errorf("%w: %s", err, msg)
	}
	return out.Bytes(), nil
}

func gib(b uint64) string { return fmt.Sprintf("%.0f GiB", float64(b)/(1<<30)) }

// Summary is a short human summary built ONLY from non-identifying fields. It carries
// no serial, WWN, MAC, by-id path, hostname or address, because none are decoded.
func (r *Result) Summary() string {
	p := r.Probe
	var b strings.Builder
	fw := "BIOS"
	if p.Firmware.UEFI {
		fw = "UEFI"
		if p.Firmware.SecureBoot != nil {
			fw += fmt.Sprintf(", Secure Boot %s", onOff(*p.Firmware.SecureBoot))
		}
	}
	virt := ""
	if p.Host.Virtual {
		virt = " (virtual machine)"
	}
	fmt.Fprintf(&b, "- Firmware: %s%s\n", fw, virt)
	fmt.Fprintf(&b, "- CPU: %s, %s, %d socket(s), %d cores / %d threads, AVX2 %s, AVX-512 %s\n",
		orUnknown(strings.TrimSpace(p.CPU.Model)), orUnknown(p.CPU.PsABI), p.CPU.Sockets, p.CPU.PhysicalCores, p.CPU.Threads,
		onOff(p.CPU.AVX2), onOff(p.CPU.AVX512F))
	if len(p.CPU.MissingForV3) > 0 {
		fmt.Fprintf(&b, "  - missing for x86-64-v3: %s\n", strings.Join(p.CPU.MissingForV3, " "))
	}
	fmt.Fprintf(&b, "- Memory: %s\n", gib(p.Memory.TotalBytes))
	if len(p.GPUs) == 0 {
		b.WriteString("- GPUs: none\n")
	}
	for _, g := range p.GPUs {
		v := "VRAM unknown"
		if g.VRAMKnown {
			v = gib(g.VRAMBytes) + " VRAM"
		}
		kind := "discrete"
		if g.Integrated {
			kind = "integrated"
		}
		fmt.Fprintf(&b, "- GPU: %s %s, %s\n", g.Vendor, kind, v)
	}
	disks := append([]struct {
		Name      string `json:"name"`
		SizeBytes uint64 `json:"size_bytes"`
		Kind      string `json:"kind"`
		Transport string `json:"transport"`
		BootMedia bool   `json:"boot_media"`
		Eligible  bool   `json:"eligible"`
		ZFSMember bool   `json:"zfs_member"`
	}(nil), p.Disks...)
	sort.Slice(disks, func(i, j int) bool { return disks[i].Name < disks[j].Name })
	elig := 0
	for _, d := range disks {
		var tags []string
		if d.BootMedia {
			tags = append(tags, "install medium")
		}
		if d.Eligible {
			elig++
			tags = append(tags, "eligible")
		}
		if d.ZFSMember {
			tags = append(tags, "has ZFS label")
		}
		fmt.Fprintf(&b, "- Disk %s: %s %s over %s", d.Name, gib(d.SizeBytes), orUnknown(d.Kind), orUnknown(d.Transport))
		if len(tags) > 0 {
			fmt.Fprintf(&b, " (%s)", strings.Join(tags, ", "))
		}
		b.WriteString("\n")
	}
	fmt.Fprintf(&b, "- Eligible install disks: %d\n", elig)
	phys, up, v6 := 0, 0, 0
	for _, n := range p.NICs {
		if !n.Physical {
			continue
		}
		phys++
		if n.Carrier {
			up++
		}
		if n.IPv6Global {
			v6++
		}
	}
	fmt.Fprintf(&b, "- Network: %d physical interface(s), %d with link, %d with a global IPv6 address\n", phys, up, v6)
	if p.TPM.Present {
		fmt.Fprintf(&b, "- TPM: %s\n", orUnknown(p.TPM.Version))
	} else {
		b.WriteString("- TPM: none\n")
	}
	for _, w := range p.Warnings {
		fmt.Fprintf(&b, "- Warning: %s\n", freeText(w))
	}
	return b.String()
}

// planView is the subset of river.install-plan/v1 a summary may show. Like probeView it
// decodes no identifying field: disks carry name, size and kind only — never by_id,
// confirm_id or model — and tier env maps are not decoded.
type planView struct {
	Schema   string   `json:"schema"`
	Verdict  string   `json:"verdict"`
	Refusals []string `json:"refusals"`
	Warnings []string `json:"warnings"`
	Profile  struct {
		Class string `json:"class"`
	} `json:"profile"`
	CPU struct {
		InferenceThreads int    `json:"inference_threads"`
		ISABuild         string `json:"isa_build"`
	} `json:"cpu"`
	Memory struct {
		TotalMiB    int64 `json:"total_mib"`
		ReservedMiB int64 `json:"reserved_mib"`
		ModelsMiB   int64 `json:"models_mib"`
	} `json:"memory"`
	Tiers []struct {
		Tier          string `json:"tier"`
		Status        string `json:"status"`
		Variant       string `json:"variant"`
		ContextTokens int    `json:"context_tokens"`
		Threads       int    `json:"threads"`
	} `json:"tiers"`
	Storage struct {
		Layout string `json:"layout"`
		Disks  []struct {
			Name      string `json:"name"`
			SizeBytes uint64 `json:"size_bytes"`
			Kind      string `json:"kind"`
		} `json:"disks"`
		DataVdevs []struct {
			Type  string   `json:"type"`
			Disks []string `json:"disks"`
		} `json:"data_vdevs"`
		SpecialVdev *struct {
			Type string `json:"type"`
		} `json:"special_vdev"`
		UsableBytes uint64 `json:"usable_bytes_estimate"`
		Unused      []struct {
			Name   string `json:"name"`
			Reason string `json:"reason"`
		} `json:"unused"`
	} `json:"storage"`
	ZFS struct {
		ARCMaxBytes uint64 `json:"arc_max_bytes"`
	} `json:"zfs"`
	Swap struct {
		ZramMiB int64 `json:"zram_mib"`
	} `json:"swap"`
}

// PlanSummary renders the install plan from its non-identifying fields.
func (r *Result) PlanSummary() string {
	if r.PlanJSON == nil {
		if r.PlanErr != nil {
			return "- plan unavailable: " + r.PlanErr.Error() + "\n"
		}
		return "- plan unavailable\n"
	}
	var p planView
	if err := json.Unmarshal(r.PlanJSON, &p); err != nil || !strings.HasPrefix(p.Schema, "river.install-plan/") {
		return "- plan unreadable (want schema river.install-plan/v1)\n"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "- Verdict: **%s** (hardware class %s)\n", orUnknown(p.Verdict), orUnknown(p.Profile.Class))
	for _, s := range p.Refusals {
		fmt.Fprintf(&b, "  - refused: %s\n", freeText(s))
	}
	for _, s := range p.Warnings {
		fmt.Fprintf(&b, "  - warning: %s\n", freeText(s))
	}
	fmt.Fprintf(&b, "- Storage: %s layout, %d disk(s) to erase, ~%s usable\n", orUnknown(p.Storage.Layout), len(p.Storage.Disks), gib(p.Storage.UsableBytes))
	for _, d := range p.Storage.Disks {
		fmt.Fprintf(&b, "  - %s: %s %s\n", d.Name, gib(d.SizeBytes), orUnknown(d.Kind))
	}
	for _, v := range p.Storage.DataVdevs {
		fmt.Fprintf(&b, "  - data vdev: %s of %d\n", v.Type, len(v.Disks))
	}
	if p.Storage.SpecialVdev != nil {
		fmt.Fprintf(&b, "  - special vdev: %s\n", p.Storage.SpecialVdev.Type)
	}
	for _, u := range p.Storage.Unused {
		fmt.Fprintf(&b, "  - not used: %s (%s)\n", u.Name, freeText(u.Reason))
	}
	fmt.Fprintf(&b, "- Memory: %d MiB total, %d MiB reserved, %d MiB for models\n", p.Memory.TotalMiB, p.Memory.ReservedMiB, p.Memory.ModelsMiB)
	if len(p.Tiers) == 0 {
		b.WriteString("- Model tiers: none planned (no model manifest on this medium)\n")
	}
	for _, t := range p.Tiers {
		fmt.Fprintf(&b, "- Tier %s: %s", t.Tier, orUnknown(t.Status))
		if t.Variant != "" {
			fmt.Fprintf(&b, ", %s, %d-token context, %d threads", t.Variant, t.ContextTokens, t.Threads)
		}
		b.WriteString("\n")
	}
	fmt.Fprintf(&b, "- ZFS ARC max %s, zram %d MiB, inference build %s with %d threads\n",
		gib(p.ZFS.ARCMaxBytes), p.Swap.ZramMiB, orUnknown(p.CPU.ISABuild), p.CPU.InferenceThreads)
	return b.String()
}

func onOff(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

func orUnknown(s string) string {
	if strings.TrimSpace(s) == "" {
		return "unknown"
	}
	return s
}

func exitCode(err error) int {
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode()
	}
	return -1
}

var pathRe = regexp.MustCompile(`/[^\s/;,()]+(/[^\s;,()]+)+`)

// freeText scrubs the planner's and probe's human-readable reasons, which can name mount
// points: a removable disk's mount path carries the desktop user's name and the volume's
// serial (/run/media/<user>/<VOLUME-ID>). Multi-segment paths become "[path]"; single ones
// such as /boot stay.
func freeText(s string) string { return pathRe.ReplaceAllString(s, "[path]") }
