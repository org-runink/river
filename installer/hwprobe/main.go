// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

// river-hwprobe — read-only hardware inventory for the RIVER installer.
//
//	river-hwprobe --json            JSON document (schema river.hwprobe/v1) on stdout
//	river-hwprobe                   human summary on stdout
//	river-hwprobe --root DIR ...    read /proc and /sys from DIR (fixtures, offline debugging)
//
// It reads /proc and /sys (and the udev database under /run/udev/data) and never opens
// a block device or writes a file. It needs no root: every source it reads is
// world-readable. Exit codes: 0 ok, 1 probe failed, 2 usage.
//
// The CLI and the JSON schema are a contract (docs/INSTALLER-HARDWARE.md, "Contract").
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/org-runink/river/installer/internal/hw"
)

func main() {
	fs := flag.NewFlagSet("river-hwprobe", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "emit the JSON document (schema "+hw.SchemaVersion+")")
	root := fs.String("root", "/", "filesystem root to read /proc and /sys from")
	if err := fs.Parse(os.Args[1:]); err != nil {
		os.Exit(2)
	}
	if fs.NArg() != 0 {
		fmt.Fprintf(os.Stderr, "river-hwprobe: unexpected argument %q\n", fs.Arg(0))
		os.Exit(2)
	}
	p, err := (&hw.Prober{Root: *root, Now: time.Now}).Probe()
	if err != nil {
		fmt.Fprintf(os.Stderr, "river-hwprobe: %v\n", err)
		os.Exit(1)
	}
	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(p); err != nil {
			fmt.Fprintf(os.Stderr, "river-hwprobe: %v\n", err)
			os.Exit(1)
		}
		return
	}
	summary(p)
}

func gib(b uint64) float64 { return float64(b) / (1 << 30) }

func summary(p *hw.Probe) {
	c := p.CPU
	fmt.Printf("CPU      %s (%s)\n", c.Model, c.Vendor)
	fmt.Printf("         %s, %d socket(s), %d physical cores, %d threads\n", c.PsABI, c.Sockets, c.PhysicalCores, c.Threads)
	var isa []string
	for _, f := range []struct {
		on   bool
		name string
	}{{c.AVX2, "AVX2"}, {c.AVX512F, "AVX-512"}, {c.AVX512BF16, "AVX512-BF16"}, {c.AVX512VNNI, "AVX512-VNNI"},
		{c.AVXVNNI, "AVX-VNNI"}, {c.AMXTile, "AMX"}} {
		if f.on {
			isa = append(isa, f.name)
		}
	}
	fmt.Printf("         %s\n", strings.Join(isa, " "))
	if len(c.MissingForV3) > 0 {
		fmt.Printf("         missing for x86-64-v3: %s\n", strings.Join(c.MissingForV3, " "))
	}
	fmt.Printf("Memory   %.1f GiB total, %.1f GiB available\n", gib(p.Memory.TotalBytes), gib(p.Memory.AvailableBytes))
	fw := "BIOS (legacy)"
	if p.Firmware.UEFI {
		fw = "UEFI"
		if p.Firmware.SecureBoot != nil {
			fw += fmt.Sprintf(", Secure Boot %v", map[bool]string{true: "on", false: "off"}[*p.Firmware.SecureBoot])
		}
	}
	fmt.Printf("Firmware %s\n", fw)
	tpm := "none"
	if p.TPM.Present {
		tpm = p.TPM.Device + " " + p.TPM.Version
	}
	fmt.Printf("TPM      %s\n", tpm)
	for _, g := range p.GPUs {
		v := "VRAM unknown"
		if g.VRAMKnown {
			v = fmt.Sprintf("%.1f GiB VRAM", gib(g.VRAMBytes))
		}
		kind := "discrete"
		if g.Integrated {
			kind = "integrated"
		}
		fmt.Printf("GPU      %s %s:%s %s (%s, %s, driver %s)\n", g.PCIAddress, g.VendorID, g.DeviceID, g.Vendor, kind, v, g.Driver)
	}
	fmt.Println("Disks")
	for _, d := range p.Disks {
		state := "eligible"
		if !d.Eligible {
			state = "NOT eligible: " + strings.Join(d.IneligibleReasons, "; ")
		}
		if d.BootMedia {
			state = "BOOT MEDIUM — " + state
		}
		fmt.Printf("  %-10s %8.1f GiB %-4s %-7s serial=%s model=%q\n             %s\n",
			d.Name, gib(d.SizeBytes), d.Kind, d.Transport, d.ConfirmID, d.Model, state)
	}
	fmt.Println("Network")
	for _, n := range p.NICs {
		if !n.Physical {
			continue
		}
		sp := "?"
		if n.SpeedMbps > 0 {
			sp = fmt.Sprintf("%d Mb/s", n.SpeedMbps)
		}
		v6 := "IPv6 off"
		if n.IPv6Enabled {
			v6 = "IPv6 on"
			if n.IPv6Global {
				v6 += " (global)"
			} else if n.IPv6LinkLocal {
				v6 += " (link-local only)"
			}
		}
		fmt.Printf("  %-12s %-5s carrier=%v %s %s\n", n.Name, n.Operstate, n.Carrier, sp, v6)
	}
	for _, w := range p.Warnings {
		fmt.Printf("WARN     %s\n", w)
	}
}
