// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

// Package planner turns a hardware probe plus a models manifest into an install plan.
//
// Build is a PURE function: same inputs, same plan. It reads no files, no clock (the
// caller stamps GeneratedAt) and no environment, so every decision is covered by the
// table-driven tests with fixture probes. The thresholds below are the documented
// minimums in docs/INSTALLER-HARDWARE.md; change both together.
package planner

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/org-runink/river/installer/internal/hw"
)

// SchemaVersion identifies the plan document format.
const SchemaVersion = "river.install-plan/v1"

// Documented minimums and policy constants.
const (
	MinPsABILevel     = 3     // the image and the inference runtime are built for x86-64-v3
	MinPhysicalCores  = 4     //
	MinMemTotalMiB    = 15360 // "16 GB" of installed RAM reports ~15.5 GiB MemTotal
	MinTargetDiskGiB  = 64    // smaller eligible disks are listed as unused
	MinPoolUsableGiB  = 200   // system + baked models + image store + state
	ESPMiB            = 1024  // per data disk (10-disk-zfs.sh)
	SameSizeTolerance = 0.10  // disks within 10% of each other are one size group
	MaxRaidzWidth     = 12    // wider sets are split into equal raidz2 vdevs

	DefaultOSMiB       = 1024 // kernel, s6, udev, sshd, NetworkManager, ZFS userland
	DefaultK0sMiB      = 2048 // k0s controller+worker, containerd, CNI, cluster DNS
	DefaultPlatformMiB = 6144 // the downstream platform's non-inference services
	ARCMinMiB          = 1024
	ARCMaxMiB          = 16384
	// ARCUnifiedMaxMiB caps zfs_arc_max on a node whose GPU has no memory of its own and so
	// serves models out of the same MemTotal the ARC caches into. There RAM/16 is not a
	// conservative slice of a large pool, it is a direct subtraction from what inference can
	// load: on a 128 GiB unified-memory box RAM/16 reserves 8 GiB of ARC the model can never
	// use. Owner decision 2026-10-02 for GB10/Grace-class hardware: "inference wins, cap ARC
	// hard". 2 GiB keeps metadata and a small data cache, so the pool is not crawling, and
	// returns the rest to the model. The trade is accepted and explicit: storage reads are
	// slower on these nodes.
	ARCUnifiedMaxMiB = 2048
	HeadroomMinMiB   = 1024
	HeadroomPercent    = 10
	CtxStep            = 4096 // context grows in these steps between ctx_min and ctx_max
	ZramMaxMiB         = 16384
)

// Image profiles. The server is the default; a workstation is a desktop that runs no
// workloads, so it plans no model tiers, reserves nothing for k0s or a platform, and needs
// only desktop minimums (docs/INSTALLER-HARDWARE.md, "Profiles").
const (
	ProfileServer      = "server"
	ProfileWorkstation = "workstation"

	WorkstationMinPhysicalCores = 2
	WorkstationMinMemTotalMiB   = 7168 // "8 GB" of installed RAM reports ~7.5 GiB MemTotal
)

// ISONames returns the image a plan is for, as recorded in it: "river" for the workstation
// profile (Runink River, iso-profiles/river), "server" for the server profile (a downstream
// server distribution's image; since 2026-09-26 no server profile lives in this repository).
func ISONames(profile string) string {
	if profile == ProfileWorkstation {
		return "river"
	}
	return "server"
}

// Options are the caller's switches.
type Options struct {
	// Lab lets a plan that breaks a documented minimum through as "degraded" instead of
	// "refused" (VMs, test rigs). The plan records it; a production node must not have it.
	Lab bool
	// Profile is ProfileServer (the default when empty) or ProfileWorkstation.
	Profile string
}

// Plan is the install plan document (schema river.install-plan/v1). It is written to
// /etc/runink/install-plan.json on the installed node as the per-node source of truth for
// the inference deployment of a downstream platform.
type Plan struct {
	Schema      string       `json:"schema"`
	GeneratedAt string       `json:"generated_at,omitempty"`
	Verdict     string       `json:"verdict"` // ok | degraded | refused
	Refusals    []string     `json:"refusals,omitempty"`
	Warnings    []string     `json:"warnings,omitempty"`
	Notes       []string     `json:"notes,omitempty"`
	Lab         bool         `json:"lab"`
	Profile     Profile      `json:"profile"`
	Hardware    Hardware     `json:"hardware"`
	CPU         CPUPlan      `json:"cpu"`
	Memory      MemoryPlan   `json:"memory"`
	Tiers       []TierPlan   `json:"tiers"`
	Accelerator Accelerator  `json:"accelerator"`
	Storage     StoragePlan  `json:"storage"`
	ZFS         ZFSTunables  `json:"zfs"`
	Swap        SwapPlan     `json:"swap"`
	Network     NetworkCheck `json:"network"`
}

// Profile names the image profile and the node's size class.
type Profile struct {
	ISO   string `json:"iso"`   // "river" (Runink River, the workstation) or "server"
	Class string `json:"class"` // edge (<32 GiB) | standard (<128 GiB) | large
}

// Hardware is the probe, summarised: what the plan was made for.
type Hardware struct {
	PsABI         string `json:"psabi"`
	AVX512        bool   `json:"avx512"`
	AMX           bool   `json:"amx"`
	PhysicalCores int    `json:"physical_cores"`
	Threads       int    `json:"threads"`
	Sockets       int    `json:"sockets"`
	MemTotalMiB   int64  `json:"mem_total_mib"`
	UEFI          bool   `json:"uefi"`
	TPM2          bool   `json:"tpm2"`
	GPUs          int    `json:"gpus"`
	Virtual       bool   `json:"virtual"`
}

// CPUPlan splits the physical cores between the node and inference.
type CPUPlan struct {
	PhysicalCores    int    `json:"physical_cores"`
	ReservedCores    int    `json:"reserved_cores"`
	InferenceThreads int    `json:"inference_threads"`
	ISABuild         string `json:"isa_build"`
}

// BudgetLine is one row of the RAM budget table.
type BudgetLine struct {
	Item string `json:"item"`
	MiB  int64  `json:"mib"`
	Note string `json:"note,omitempty"`
}

// MemoryPlan is the RAM budget: reserves + every resident model <= total - headroom.
type MemoryPlan struct {
	TotalMiB     int64        `json:"total_mib"`
	Budget       []BudgetLine `json:"budget"`
	ReservedMiB  int64        `json:"reserved_mib"`
	HeadroomMiB  int64        `json:"headroom_mib"`
	ModelsMiB    int64        `json:"models_mib"`
	UnplannedMiB int64        `json:"unplanned_mib"` // left after reserves, headroom and models
}

// TierPlan is one model tier's decision.
type TierPlan struct {
	Tier          string            `json:"tier"`
	Mandatory     bool              `json:"mandatory"`
	Status        string            `json:"status"` // enabled | degraded | disabled | pending
	Variant       string            `json:"variant,omitempty"`
	Rank          int               `json:"rank,omitempty"`
	ContextTokens int               `json:"context_tokens,omitempty"`
	ResidentMiB   int64             `json:"resident_mib,omitempty"`
	KVBudgetMiB   int64             `json:"kv_budget_mib,omitempty"`
	TotalMiB      int64             `json:"total_mib,omitempty"`
	Threads       int               `json:"threads,omitempty"`
	Env           map[string]string `json:"env,omitempty"`
	Reasons       []string          `json:"reasons,omitempty"`
}

// Placed reports whether the tier was given RAM (enabled or degraded). A disabled tier did
// not fit; a pending tier has no model to place (models.tiers `pending=<tier>`).
func (t TierPlan) Placed() bool { return t.Status == "enabled" || t.Status == "degraded" }

// Accelerator records the GPU decision.
type Accelerator struct {
	Mode                 string   `json:"mode"` // cpu
	GPUOffloadCandidates []string `json:"gpu_offload_candidates,omitempty"`
	Note                 string   `json:"note,omitempty"`
}

// PoolDisk is a disk the plan puts in the pool. ConfirmID is what the operator must type.
type PoolDisk struct {
	Name      string `json:"name"` // kernel name AT PROBE TIME; re-resolved by confirm_id
	ByID      string `json:"by_id,omitempty"`
	ConfirmID string `json:"confirm_id"`
	SizeBytes uint64 `json:"size_bytes"`
	Kind      string `json:"kind"`
	Model     string `json:"model,omitempty"`
}

// Vdev is one top-level vdev.
type Vdev struct {
	Type  string   `json:"type"` // disk | mirror | raidz1 | raidz2
	Disks []string `json:"disks"`
}

// UnusedDisk is an eligible-or-not disk the plan leaves alone, and why.
type UnusedDisk struct {
	Name   string `json:"name"`
	Reason string `json:"reason"`
}

// StoragePlan is the ZFS pool layout.
type StoragePlan struct {
	Layout         string       `json:"layout"` // single | mirror | raidz1 | raidz2 | none
	BootDisk       string       `json:"boot_disk,omitempty"`
	Disks          []PoolDisk   `json:"disks"` // every disk that will be WIPED, boot disk first
	DataVdevs      []Vdev       `json:"data_vdevs,omitempty"`
	SpecialVdev    *Vdev        `json:"special_vdev,omitempty"`
	UsableBytes    uint64       `json:"usable_bytes_estimate"`
	Unused         []UnusedDisk `json:"unused,omitempty"`
	EncryptionNote string       `json:"encryption"`
}

// ZFSTunables are module parameters the plan recommends.
type ZFSTunables struct {
	ARCMaxBytes uint64 `json:"arc_max_bytes"`
}

// SwapPlan: no swap on ZFS, ever; a zram device as the OOM backstop.
type SwapPlan struct {
	DiskSwap  bool   `json:"disk_swap"`
	Zram      bool   `json:"zram"`
	ZramMiB   int64  `json:"zram_mib"`
	Algorithm string `json:"algorithm"`
}

// NetworkCheck summarises NICs (the host is dual-stack; the k0s cluster needs host IPv6).
type NetworkCheck struct {
	PhysicalNICs int  `json:"physical_nics"`
	WithCarrier  int  `json:"with_carrier"`
	IPv6Global   bool `json:"ipv6_global"`
	MaxSpeedMbps int  `json:"max_speed_mbps"`
}

// Build makes the plan. m may be nil (no models manifest on the medium): tiers are then
// not planned and the plan says so, but hardware and storage are still decided.
func Build(p *hw.Probe, m *Manifest, opt Options) *Plan {
	pl := &Plan{Schema: SchemaVersion, Lab: opt.Lab, Tiers: []TierPlan{}}
	refuse := func(format string, a ...any) { pl.Refusals = append(pl.Refusals, fmt.Sprintf(format, a...)) }
	warn := func(format string, a ...any) { pl.Warnings = append(pl.Warnings, fmt.Sprintf(format, a...)) }
	note := func(format string, a ...any) { pl.Notes = append(pl.Notes, fmt.Sprintf(format, a...)) }

	memMiB := p.MemTotalMiB()
	pl.Hardware = Hardware{
		PsABI: p.CPU.PsABI, AVX512: p.CPU.AVX512F, AMX: p.CPU.AMXTile,
		PhysicalCores: p.CPU.PhysicalCores, Threads: p.CPU.Threads, Sockets: p.CPU.Sockets,
		MemTotalMiB: memMiB, UEFI: p.Firmware.UEFI, TPM2: p.TPM.Present && p.TPM.Version == "2.0",
		GPUs: len(p.GPUs), Virtual: p.Host.Virtual,
	}
	ws := opt.Profile == ProfileWorkstation
	minCores, minMem, minMemNote, minPoolGiB := MinPhysicalCores, int64(MinMemTotalMiB), "16 GB", int64(MinPoolUsableGiB)
	if ws {
		// A desktop: one eligible disk of MinTargetDiskGiB is enough, and no models are planned.
		minCores, minMem, minMemNote, minPoolGiB = WorkstationMinPhysicalCores, WorkstationMinMemTotalMiB, "8 GB", 0
		if m != nil {
			note("workstation profile: the models manifest is ignored (a workstation serves no models)")
			m = nil
		}
		pl.Profile = Profile{ISO: ISONames(ProfileWorkstation), Class: sizeClass(memMiB)}
	} else {
		pl.Profile = Profile{ISO: ISONames(ProfileServer), Class: sizeClass(memMiB)}
	}

	// ---- platform minimums --------------------------------------------------------------
	if !p.Firmware.UEFI {
		refuse("firmware booted in legacy BIOS mode: RIVER requires UEFI (the ESP is /boot, GRUB installs to the removable EFI path)")
	}
	if p.CPU.PsABILevel < MinPsABILevel {
		refuse("CPU is %s (missing %s): the image and the inference runtime are built for x86-64-v3 (AVX2/FMA/F16C/BMI2)",
			p.CPU.PsABI, strings.Join(p.CPU.MissingForV3, " "))
	}
	if p.CPU.PhysicalCores < minCores {
		refuse("%d physical cores: the minimum is %d", p.CPU.PhysicalCores, minCores)
	}
	if memMiB < minMem {
		refuse("%d MiB RAM (MemTotal): the minimum is %d MiB (%s installed)", memMiB, minMem, minMemNote)
	}
	if p.CPU.AVX512F {
		note("AVX-512 present; the shipped inference build targets x86-64-v3 and does not use it")
	}
	if p.CPU.AMXTile {
		note("AMX present; not used by the shipped CPU inference build")
	}
	if p.Host.Virtual {
		note("running under a hypervisor: core counts and RAM are what the VM was given")
	}
	if pl.Hardware.TPM2 {
		note("TPM 2.0 present: eligible for an unattended-unlock key provider (none ships yet; the pool passphrase is typed at boot)")
	} else {
		note("no TPM 2.0: the pool passphrase is typed at the console on every boot")
	}

	// ---- CPU split ----------------------------------------------------------------------
	reserved := p.CPU.PhysicalCores / 8
	if reserved < 2 {
		reserved = 2
	}
	if reserved > 8 {
		reserved = 8
	}
	inf := p.CPU.PhysicalCores - reserved
	if inf < 1 {
		inf = 1
	}
	pl.CPU = CPUPlan{PhysicalCores: p.CPU.PhysicalCores, ReservedCores: reserved, InferenceThreads: inf, ISABuild: "x86-64-v3"}
	if p.CPU.Sockets > 1 {
		note("%d sockets: inference threads are not NUMA-pinned by this plan", p.CPU.Sockets)
	}

	// ---- RAM budget ---------------------------------------------------------------------
	res := map[string]int64{"os": DefaultOSMiB, "k0s": DefaultK0sMiB, "platform": DefaultPlatformMiB}
	if ws {
		// No cluster and no platform services run on a workstation.
		res["k0s"], res["platform"] = 0, 0
	}
	if m != nil {
		for k, v := range m.Reserves {
			res[k] = v
		}
	}
	arc := clamp(memMiB/16, ARCMinMiB, ARCMaxMiB)
	arcNote := "zfs_arc_max: RAM/16, clamped to [1 GiB, 16 GiB]"
	if unified, why := unifiedMemory(p, ws); unified {
		arc = min(arc, ARCUnifiedMaxMiB)
		arcNote = fmt.Sprintf("zfs_arc_max: capped to %d MiB — %s, so ARC and the models draw on one pool and inference wins", arc, why)
		note("unified memory: %s. zfs_arc_max is capped to %d MiB instead of RAM/16 (%d MiB) so the models keep that memory; storage reads are slower in exchange.",
			why, arc, clamp(memMiB/16, ARCMinMiB, ARCMaxMiB))
	}
	head := max(HeadroomMinMiB, memMiB*HeadroomPercent/100)
	pl.Memory.TotalMiB = memMiB
	pl.Memory.Budget = []BudgetLine{{"os", res["os"], "kernel, s6, udev, sshd, NetworkManager, ZFS userland"}}
	if !ws {
		pl.Memory.Budget = append(pl.Memory.Budget,
			BudgetLine{"k0s", res["k0s"], "controller + worker, containerd, CNI, cluster DNS"},
			BudgetLine{"platform", res["platform"], "the downstream platform's non-inference services"})
	}
	pl.Memory.Budget = append(pl.Memory.Budget, BudgetLine{"zfs-arc", arc, arcNote})
	pl.Memory.ReservedMiB = res["os"] + res["k0s"] + res["platform"] + arc
	pl.Memory.HeadroomMiB = head
	avail := memMiB - pl.Memory.ReservedMiB - head
	pl.ZFS.ARCMaxBytes = uint64(arc) << 20 // #nosec G115 -- arc is clamped to [ARCMinMiB, ARCMaxMiB], always positive

	// ---- tiers --------------------------------------------------------------------------
	if ws {
		note("workstation profile: no model tiers are planned (a workstation serves no models)")
	} else if m == nil {
		warn("no models manifest: model tiers were not planned (the node installs, the inference deployment has no per-node plan)")
	} else {
		used := placeTiers(pl, m, avail)
		pl.Memory.ModelsMiB = used
		for _, t := range pl.Tiers {
			if t.Status == "pending" {
				warn("tier %s is pending in the models manifest: its model is being re-selected, so the node installs without it and no RAM is budgeted for it", t.Tier)
			}
		}
		for _, t := range pl.Tiers {
			if t.Placed() {
				pl.Memory.Budget = append(pl.Memory.Budget, BudgetLine{
					Item: "tier:" + t.Tier, MiB: t.TotalMiB,
					Note: fmt.Sprintf("%s: %d resident + %d KV (%d tokens)", t.Variant, t.ResidentMiB, t.KVBudgetMiB, t.ContextTokens),
				})
			}
		}
		for _, t := range pl.Tiers {
			if t.Mandatory && t.Status == "disabled" {
				refuse("mandatory tier %s does not fit: %s", t.Tier, strings.Join(t.Reasons, "; "))
			}
		}
	}
	pl.Memory.Budget = append(pl.Memory.Budget, BudgetLine{"headroom", head, fmt.Sprintf("max(1 GiB, %d%% of RAM), never allocated", HeadroomPercent)})
	pl.Memory.UnplannedMiB = memMiB - pl.Memory.ReservedMiB - head - pl.Memory.ModelsMiB

	// ---- accelerators -------------------------------------------------------------------
	pl.Accelerator = accelerators(p, pl.Tiers)

	// ---- storage ------------------------------------------------------------------------
	pl.Storage = storage(p, minPoolGiB, refuse, warn)

	// ---- swap ---------------------------------------------------------------------------
	z := memMiB / 4
	if memMiB < 32768 {
		z = memMiB / 2
	}
	pl.Swap = SwapPlan{DiskSwap: false, Zram: true, ZramMiB: min(z, ZramMaxMiB), Algorithm: "zstd"}

	// ---- network ------------------------------------------------------------------------
	for _, n := range p.NICs {
		if !n.Physical {
			continue
		}
		pl.Network.PhysicalNICs++
		if n.Carrier {
			pl.Network.WithCarrier++
		}
		if n.IPv6Global {
			pl.Network.IPv6Global = true
		}
		pl.Network.MaxSpeedMbps = max(pl.Network.MaxSpeedMbps, n.SpeedMbps)
	}
	if pl.Network.PhysicalNICs == 0 {
		warn("no physical network interface found")
	} else if pl.Network.WithCarrier == 0 {
		warn("no network interface has link: the node needs a network, and its k0s cluster needs a global IPv6 address on the host")
	} else if !pl.Network.IPv6Global {
		warn("no global IPv6 address on any interface yet: the host may be dual-stack, but its k0s cluster is IPv6-only and needs one (router advertisements / DHCPv6)")
	}

	// ---- verdict ------------------------------------------------------------------------
	degraded := false
	if m != nil {
		for _, u := range m.Unpinned {
			warn("%s (models.lock catches up when the model set is confirmed)", u)
			degraded = true
		}
	}
	for _, t := range pl.Tiers {
		if t.Status != "enabled" {
			degraded = true
		}
	}
	switch {
	case len(pl.Refusals) > 0 && !opt.Lab:
		pl.Verdict = "refused"
	case len(pl.Refusals) > 0:
		pl.Verdict = "degraded"
		warn("LAB OVERRIDE: %d refusal(s) waived; this node is below the documented minimums", len(pl.Refusals))
	case degraded || (m == nil && !ws):
		pl.Verdict = "degraded"
	default:
		pl.Verdict = "ok"
	}
	return pl
}

func sizeClass(mib int64) string {
	switch {
	case mib < 32*1024-1024:
		return "edge"
	case mib < 128*1024-2048:
		return "standard"
	}
	return "large"
}

func clamp(v, lo, hi int64) int64 { return max(lo, min(hi, v)) }

// threadsFor gives heavy tiers every inference thread and light tiers a share of them.
func threadsFor(tier string, inf, limit int) int {
	t := inf
	switch tier {
	case TierSTT:
		t = clampInt(inf/2, 2, 8)
	case TierEmbedding:
		t = clampInt(inf/2, 1, 4)
	case TierTTS:
		t = clampInt(inf, 1, 2)
	}
	if t > inf {
		t = inf
	}
	if limit > 0 && t > limit {
		t = limit
	}
	return max(1, t)
}

func clampInt(v, lo, hi int) int { return max(lo, min(hi, v)) }

func kvFor(v Variant, ctx int) int64 {
	return (v.KVMiBPer1K*int64(ctx) + 1023) / 1024
}

// placeTiers fits the tiers into avail MiB. Pass 1 places each tier at its ctx_min, the
// mandatory ones first, then the optional ones in priority order, each taking its best
// ranked variant that fits (a fallback variant is a DEGRADED tier). Pass 2 grows the
// context of every placed tier toward ctx_max, in the same order, in CtxStep steps.
func placeTiers(pl *Plan, m *Manifest, avail int64) int64 {
	left := avail
	type placed struct {
		idx int
		v   Variant
	}
	var order []placed
	for _, tier := range m.Tiers() {
		mandatory := slices.Contains(MandatoryTiers, tier)
		tp := TierPlan{Tier: tier, Mandatory: mandatory, Status: "disabled"}
		if m.Pending[tier] {
			tp.Status = "pending"
			tp.Reasons = append(tp.Reasons, "pending in the models manifest: the model is being re-selected; nothing is placed or budgeted")
			pl.Tiers = append(pl.Tiers, tp)
			continue
		}
		vs := m.ByTier(tier)
		if len(vs) == 0 {
			tp.Reasons = append(tp.Reasons, "no variant in the models manifest")
			pl.Tiers = append(pl.Tiers, tp)
			continue
		}
		var tried []string
		for _, v := range vs {
			need := v.ResidentMiB + kvFor(v, v.CtxMin)
			if need <= left {
				left -= need
				tp.Variant, tp.Rank, tp.ContextTokens = v.Name, v.Rank, v.CtxMin
				tp.ResidentMiB, tp.KVBudgetMiB, tp.TotalMiB = v.ResidentMiB, kvFor(v, v.CtxMin), need
				tp.Status = "enabled"
				if v.Rank != vs[0].Rank {
					tp.Status = "degraded"
					tp.Reasons = append(tp.Reasons, tried...)
					tp.Reasons = append(tp.Reasons, fmt.Sprintf("fell back to %s (rank %d)", v.Name, v.Rank))
				}
				order = append(order, placed{len(pl.Tiers), v})
				break
			}
			tried = append(tried, fmt.Sprintf("%s needs %d MiB at %d tokens, %d MiB left", v.Name, need, v.CtxMin, left))
		}
		if tp.Status == "disabled" {
			tp.Reasons = append(tp.Reasons, tried...)
		}
		pl.Tiers = append(pl.Tiers, tp)
	}
	// Pass 2: grow context.
	for _, o := range order {
		tp := &pl.Tiers[o.idx]
		v := o.v
		for ctx := tp.ContextTokens; ctx < v.CtxMax; {
			next := min(ctx+CtxStep, v.CtxMax)
			extra := kvFor(v, next) - kvFor(v, ctx)
			if extra > left {
				break
			}
			left -= extra
			ctx = next
			tp.ContextTokens, tp.KVBudgetMiB = ctx, kvFor(v, ctx)
			tp.TotalMiB = tp.ResidentMiB + tp.KVBudgetMiB
		}
		if tp.ContextTokens < v.CtxMax {
			if tp.Status == "enabled" {
				tp.Status = "degraded"
			}
			tp.Reasons = append(tp.Reasons, fmt.Sprintf("context %d of %d tokens (RAM)", tp.ContextTokens, v.CtxMax))
		}
	}
	var used int64
	for i := range pl.Tiers {
		tp := &pl.Tiers[i]
		if !tp.Placed() {
			continue
		}
		used += tp.TotalMiB
		var limit int
		for _, v := range m.ByTier(tp.Tier) {
			if v.Name == tp.Variant {
				limit = v.ThreadsMax
			}
		}
		tp.Threads = threadsFor(tp.Tier, pl.CPU.InferenceThreads, limit)
		tp.Env = map[string]string{"RAYON_NUM_THREADS": fmt.Sprint(tp.Threads)}
	}
	return used
}

// unifiedMemory reports whether this node's GPU memory IS its system memory, and says why in
// words the plan can print. It is deliberately derived from what the probe already collects
// rather than from a list of known boards: an integrated GPU is, by definition, one with no
// memory of its own, so it allocates from MemTotal. That is the whole condition, and it is
// true of GB10/Grace as it is of an APU.
//
// A GB10-specific device-tree match was the alternative and was rejected: it would have to be
// written against hardware we cannot test on (the one aarch64 box we have is the training
// host), so it would be a guess that silently matches nothing. This rule is testable today on
// x86_64 with a probe fixture, which is the only way it gets exercised before the hardware
// window opens.
//
// A workstation is excluded because nothing competes there: it plans no model tiers at all
// ("a workstation serves no models"), so capping its ARC would cost cache for no gain. The
// cap exists to settle a contest between ARC and inference, and on a workstation there is no
// contest.
func unifiedMemory(p *hw.Probe, ws bool) (bool, string) {
	if ws || len(p.GPUs) == 0 {
		return false, ""
	}
	for _, g := range p.GPUs {
		if !g.Integrated {
			// Any discrete GPU means the models have memory of their own to live in, so the
			// ARC is not taking it from them and RAM/16 is the right reservation.
			return false, ""
		}
	}
	vendor := p.GPUs[0].Vendor
	if len(p.GPUs) > 1 {
		return true, fmt.Sprintf("%d integrated GPUs (%s) and no discrete GPU, so GPU memory is system memory", len(p.GPUs), vendor)
	}
	return true, fmt.Sprintf("an integrated %s GPU and no discrete GPU, so GPU memory is system memory", vendor)
}

func accelerators(p *hw.Probe, tiers []TierPlan) Accelerator {
	a := Accelerator{Mode: "cpu"}
	var discrete []hw.GPU
	for _, g := range p.GPUs {
		if !g.Integrated {
			discrete = append(discrete, g)
		}
	}
	if len(discrete) == 0 {
		return a
	}
	var best uint64
	known := false
	for _, g := range discrete {
		if g.VRAMKnown {
			known = true
			best = max(best, g.VRAMBytes)
		}
	}
	a.Note = fmt.Sprintf("%d discrete GPU(s) detected; the shipped inference build is CPU-only, so no tier is offloaded", len(discrete))
	if !known {
		a.Note += " (VRAM not readable from sysfs for this vendor)"
		return a
	}
	vramMiB := int64(best>>20) * 9 / 10
	for _, t := range tiers {
		if t.Placed() && t.TotalMiB <= vramMiB {
			a.GPUOffloadCandidates = append(a.GPUOffloadCandidates, t.Tier)
		}
	}
	return a
}

// ---------------------------------------------------------------------------- storage

func fast(k string) bool { return k == "nvme" || k == "ssd" }

// storage plans the pool. minPoolGiB is the profile's minimum usable pool size (0: any pool
// on an eligible disk will do).
func storage(p *hw.Probe, minPoolGiB int64, refuse, warn func(string, ...any)) StoragePlan {
	sp := StoragePlan{Layout: "none", Disks: []PoolDisk{},
		EncryptionNote: "pool root is an aes-256-gcm encryption root; every dataset inherits it"}
	var cands []hw.Disk
	for _, d := range p.Disks {
		switch {
		case d.BootMedia:
			sp.Unused = append(sp.Unused, UnusedDisk{d.Name, "boot medium of this installer: never a target"})
		case !d.Eligible:
			sp.Unused = append(sp.Unused, UnusedDisk{d.Name, strings.Join(d.IneligibleReasons, "; ")})
		case d.SizeBytes < MinTargetDiskGiB<<30:
			sp.Unused = append(sp.Unused, UnusedDisk{d.Name, fmt.Sprintf("smaller than %d GiB", MinTargetDiskGiB)})
		default:
			cands = append(cands, d)
		}
	}
	if len(cands) == 0 {
		refuse("no eligible target disk (need a non-removable, non-USB, unused disk of at least %d GiB)", MinTargetDiskGiB)
		return sp
	}
	var fastD, hdd []hw.Disk
	for _, d := range cands {
		if fast(d.Kind) {
			fastD = append(fastD, d)
		} else {
			hdd = append(hdd, d)
		}
	}
	// Fast devices first (NVMe before SATA SSD), then larger first.
	sort.SliceStable(fastD, func(i, j int) bool {
		if (fastD[i].Kind == "nvme") != (fastD[j].Kind == "nvme") {
			return fastD[i].Kind == "nvme"
		}
		return fastD[i].SizeBytes > fastD[j].SizeBytes
	})

	var data []hw.Disk
	var special []hw.Disk
	switch {
	case len(hdd) >= 2 && len(fastD) >= 2:
		data, special = hdd, fastD[:2]
		for _, d := range fastD[2:] {
			sp.Unused = append(sp.Unused, UnusedDisk{d.Name, "flash beyond the special-vdev mirror"})
		}
	case len(hdd) > len(fastD):
		data = hdd
		for _, d := range fastD {
			sp.Unused = append(sp.Unused, UnusedDisk{d.Name, "a single flash device cannot be a special vdev (it would be a single point of failure for the pool)"})
		}
	default:
		data = fastD
		for _, d := range hdd {
			sp.Unused = append(sp.Unused, UnusedDisk{d.Name, "rotational disk alongside a flash pool (mixing classes in one vdev runs at the slowest disk)"})
		}
	}

	group, rest := largestSizeGroup(data)
	for _, d := range rest {
		sp.Unused = append(sp.Unused, UnusedDisk{d.Name, "size differs by more than 10% from the pool's disks"})
	}
	sort.Slice(group, func(i, j int) bool { return group[i].Name < group[j].Name })
	n := len(group)
	hddPool := !fast(group[0].Kind)
	minSize := group[0].SizeBytes
	for _, d := range group {
		minSize = min(minSize, d.SizeBytes)
	}
	per := minSize - ESPMiB<<20

	names := func(ds []hw.Disk) []string {
		var s []string
		for _, d := range ds {
			s = append(s, d.Name)
		}
		return s
	}
	switch {
	case n == 1:
		sp.Layout = "single"
		sp.DataVdevs = []Vdev{{"disk", names(group)}}
		sp.UsableBytes = per
		warn("single-disk pool: no redundancy; a disk failure loses the node")
	case n == 2:
		sp.Layout = "mirror"
		sp.DataVdevs = []Vdev{{"mirror", names(group)}}
		sp.UsableBytes = per
	case n == 3 || (n <= 5 && !hddPool):
		sp.Layout = "raidz1"
		sp.DataVdevs = []Vdev{{"raidz1", names(group)}}
		sp.UsableBytes = per * uint64(n-1) // #nosec G115 -- n >= 3 in this case
	default:
		sp.Layout = "raidz2"
		k := (n + MaxRaidzWidth - 1) / MaxRaidzWidth
		w := n / k
		for i := 0; i < k; i++ {
			sp.DataVdevs = append(sp.DataVdevs, Vdev{"raidz2", names(group[i*w : (i+1)*w])})
		}
		for _, d := range group[k*w:] {
			sp.Unused = append(sp.Unused, UnusedDisk{d.Name, "left over after equal-width raidz2 vdevs"})
		}
		group = group[:k*w]
		sp.UsableBytes = per * uint64(k*(w-2)) // #nosec G115 -- raidz2 vdevs are at least 4 wide, so w-2 > 0
	}
	for _, d := range group {
		sp.Disks = append(sp.Disks, poolDisk(d))
	}
	sp.BootDisk = group[0].Name
	if len(special) == 2 {
		sp.SpecialVdev = &Vdev{"mirror", names(special)}
		for _, d := range special {
			sp.Disks = append(sp.Disks, poolDisk(d))
		}
	}
	if hddPool && sp.SpecialVdev == nil {
		warn("pool is on rotational disks with no flash special vdev: model load and image pulls will be slow")
	}
	if minPoolGiB > 0 && sp.UsableBytes < uint64(minPoolGiB)<<30 { // #nosec G115 -- minPoolGiB > 0 here
		refuse("pool would have ~%d GiB usable: the minimum is %d GiB", sp.UsableBytes>>30, minPoolGiB)
	}
	sort.Slice(sp.Unused, func(i, j int) bool { return sp.Unused[i].Name < sp.Unused[j].Name })
	return sp
}

func poolDisk(d hw.Disk) PoolDisk {
	return PoolDisk{Name: d.Name, ByID: d.ByID, ConfirmID: d.ConfirmID, SizeBytes: d.SizeBytes, Kind: d.Kind, Model: d.Model}
}

// largestSizeGroup returns the biggest set of disks within SameSizeTolerance of each other
// (ties: the larger disks), and the rest.
func largestSizeGroup(ds []hw.Disk) (group, rest []hw.Disk) {
	s := append([]hw.Disk(nil), ds...)
	sort.Slice(s, func(i, j int) bool { return s[i].SizeBytes < s[j].SizeBytes })
	bi, bj := 0, 1
	for i := range s {
		j := i
		for j < len(s) && float64(s[j].SizeBytes) <= float64(s[i].SizeBytes)*(1+SameSizeTolerance) {
			j++
		}
		if j-i > bj-bi || (j-i == bj-bi && i > bi) {
			bi, bj = i, j
		}
	}
	group = append(group, s[bi:bj]...)
	rest = append(append(rest, s[:bi]...), s[bj:]...)
	return
}
