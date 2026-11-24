// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

package planner

import (
	"fmt"

	"github.com/org-runink/river/installer/internal/hw"
)

// Finding kinds of MemoryFinding.Kind.
const (
	// MemFirmwareGPU: the firmware gave a block of the installed RAM to the integrated GPU
	// as dedicated video memory (a "UMA frame buffer"), and Linux sees the rest.
	MemFirmwareGPU = "gpu-reserved"
	// MemFirmwareOther: Linux sees much less than is installed and no GPU memory explains it.
	MemFirmwareOther = "unexplained"
)

// MemoryFinding says that the kernel sees less memory than the machine holds, and why.
// It never blocks an install: it is information for the operator, because only the
// firmware setup can change it, and no software can.
type MemoryFinding struct {
	Kind string `json:"kind"`
	// InstalledMiB is the SMBIOS total; 0 when the firmware did not say (then Kind is
	// MemFirmwareGPU, decided from the GPU's dedicated memory alone).
	InstalledMiB   int64 `json:"installed_mib"`
	KernelMiB      int64 `json:"kernel_mib"`
	GPUReservedMiB int64 `json:"gpu_reserved_mib"`
	GTTMiB         int64 `json:"gtt_mib,omitempty"`
	MissingMiB     int64 `json:"missing_mib"`
	// Summary, Consequence and Fix are plain sentences for the installer screens.
	Summary     string `json:"summary"`
	Consequence string `json:"consequence"`
	Fix         string `json:"fix"`
}

// gpuReserveFloorMiB is the dedicated video memory below which an integrated GPU is "normal"
// (a few hundred MiB to 2 GiB is firmware default almost everywhere) and not worth a warning
// when the installed RAM cannot be read.
const gpuReserveFloorMiB = 4096

// memoryFinding compares the installed RAM (SMBIOS), the kernel's MemTotal and the GPU's
// dedicated memory (amdgpu mem_info_vram_total). It returns nil when there is nothing to say.
func memoryFinding(p *hw.Probe) *MemoryFinding {
	kernel := p.MemTotalMiB()
	var vram, gtt int64
	for _, g := range p.GPUs {
		if g.Integrated && g.VRAMKnown {
			if v := int64(g.VRAMBytes >> 20); v > vram {
				vram = v
				gtt = int64(g.GTTBytes >> 20)
			}
		}
	}
	installed := int64(p.Memory.InstalledBytes >> 20)
	if !p.Memory.InstalledKnown {
		installed = 0
	}
	f := &MemoryFinding{InstalledMiB: installed, KernelMiB: kernel, GPUReservedMiB: vram, GTTMiB: gtt}

	switch {
	case installed > 0:
		f.MissingMiB = installed - kernel
		// The kernel always loses a little to the firmware, the kernel image and page
		// tables: about 1 GiB on a big machine. Anything past 5% (and 2 GiB) is a decision.
		if f.MissingMiB < max(2048, installed/20) {
			return nil
		}
		// The GPU explains it when its dedicated memory is at least three quarters of what
		// is missing.
		if vram > 0 && vram*4 >= f.MissingMiB*3 {
			f.Kind = MemFirmwareGPU
		} else {
			f.Kind = MemFirmwareOther
		}
	case vram >= gpuReserveFloorMiB:
		f.Kind = MemFirmwareGPU
		f.MissingMiB = vram
	default:
		return nil
	}

	g := func(mib int64) string { return fmt.Sprintf("%.1f GB", float64(mib)/1024) }
	switch f.Kind {
	case MemFirmwareGPU:
		if installed > 0 {
			f.Summary = fmt.Sprintf("This computer has %s of memory installed, but Linux can use only %s. The firmware has given %s to the graphics chip as dedicated video memory.",
				g(installed), g(kernel), g(vram))
		} else {
			f.Summary = fmt.Sprintf("The firmware has given %s of this computer's memory to the graphics chip as dedicated video memory, so Linux can use only %s. The installed amount could not be read.",
				g(vram), g(kernel))
		}
		f.Consequence = fmt.Sprintf("Only the graphics chip can use that %s, and only for its own buffers. Everything else (the system, ZFS and every program) gets %s, and this installation is planned for %s.",
			g(vram), g(kernel), g(kernel))
		f.Fix = "You can install now. To get the memory back, restart, open the firmware setup (BIOS/UEFI: usually the Del or F2 key right after power-on), find the integrated graphics memory setting (called UMA Frame Buffer Size, iGPU Memory, Integrated Graphics Memory or similar), set it to the smallest value (512 MB, or Auto), save, and boot this installer again. The installer cannot change the firmware."
	default:
		f.Summary = fmt.Sprintf("This computer has %s of memory installed, but Linux can use only %s (%s missing), and no graphics memory explains it.",
			g(installed), g(kernel), g(f.MissingMiB))
		f.Consequence = fmt.Sprintf("The system, ZFS and services get %s, and this installation is planned for %s.", g(kernel), g(kernel))
		f.Fix = "You can install now. The firmware setup (BIOS/UEFI) may reserve memory or limit it: look for a memory remapping, memory hole or memory limit option and for an integrated graphics memory setting, then boot this installer again. The installer cannot change the firmware."
	}
	return f
}

// MemoryBreakdown is the memory picture the installer always shows, whether or not anything
// looks wrong: what Linux can use, what the firmware gave to the graphics chip, the installed
// total, and how much memory the graphics chip can map from system memory. A part that no
// source could tell is "unknown": no number is ever filled in by guessing.
type MemoryBreakdown struct {
	SystemMiB      int64 `json:"system_mib"` // the kernel's MemTotal
	GraphicsMiB    int64 `json:"graphics_mib,omitempty"`
	GraphicsKnown  bool  `json:"graphics_known"`
	InstalledMiB   int64 `json:"installed_mib,omitempty"`
	InstalledKnown bool  `json:"installed_known"`
	BorrowMiB      int64 `json:"borrow_mib,omitempty"` // GTT now
	BorrowKnown    bool  `json:"borrow_known"`
	// Lines are the same facts as sentences for a plain text screen, in a fixed order.
	Lines []string `json:"lines"`
}

func memoryBreakdown(p *hw.Probe) *MemoryBreakdown {
	b := &MemoryBreakdown{SystemMiB: p.MemTotalMiB()}
	integrated := false
	for _, g := range p.GPUs {
		if !g.Integrated {
			continue
		}
		integrated = true
		if g.VRAMKnown && int64(g.VRAMBytes>>20) >= b.GraphicsMiB {
			b.GraphicsMiB, b.GraphicsKnown = int64(g.VRAMBytes>>20), true
			b.BorrowMiB, b.BorrowKnown = int64(g.GTTBytes>>20), g.GTTKnown
		}
	}
	if p.Memory.InstalledKnown {
		b.InstalledMiB, b.InstalledKnown = int64(p.Memory.InstalledBytes>>20), true
	}
	g := func(mib int64) string { return fmt.Sprintf("%.1f GB", float64(mib)/1024) }
	b.Lines = append(b.Lines, "System memory (what Linux can use): "+g(b.SystemMiB))
	switch {
	case b.GraphicsKnown:
		b.Lines = append(b.Lines, "Graphics memory (reserved by the firmware): "+g(b.GraphicsMiB))
	case integrated:
		b.Lines = append(b.Lines, "Graphics memory (reserved by the firmware): unknown (the graphics driver does not report it)")
	default:
		b.Lines = append(b.Lines, "Graphics memory (reserved by the firmware): none (no integrated graphics found)")
	}
	if b.InstalledKnown {
		b.Lines = append(b.Lines, "Installed total: "+g(b.InstalledMiB))
	} else {
		b.Lines = append(b.Lines, "Installed total: unknown (the firmware did not list the memory modules)")
	}
	switch {
	case b.BorrowKnown:
		b.Lines = append(b.Lines, fmt.Sprintf("Graphics can borrow from system memory: up to %s now; once the borrowing limits are raised, at most all of the system memory (%s)", g(b.BorrowMiB), g(b.SystemMiB)))
	case integrated:
		b.Lines = append(b.Lines, "Graphics can borrow from system memory: unknown (the graphics driver does not report it)")
	}
	return b
}
