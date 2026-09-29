// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

// Package hw holds the JSON schema river-hwprobe emits and river-plan consumes.
//
// The schema is a published CONTRACT (docs/INSTALLER-HARDWARE.md, "Contract"): other
// tools on the install medium read it. Add fields freely; never rename or retype one
// without bumping SchemaVersion.
package hw

// SchemaVersion identifies the probe document format.
const SchemaVersion = "river.hwprobe/v1"

// Probe is everything the installer needs to know about the machine it runs on.
type Probe struct {
	Schema   string   `json:"schema"`
	ProbedAt string   `json:"probed_at,omitempty"` // RFC 3339, UTC
	Host     Host     `json:"host"`
	Firmware Firmware `json:"firmware"`
	CPU      CPU      `json:"cpu"`
	Memory   Memory   `json:"memory"`
	GPUs     []GPU    `json:"gpus"`
	Disks    []Disk   `json:"disks"`
	NICs     []NIC    `json:"nics"`
	TPM      TPM      `json:"tpm"`
	Warnings []string `json:"warnings,omitempty"`
}

// Host is the DMI identity (world-readable fields only; never the chassis serial).
type Host struct {
	Vendor  string `json:"vendor,omitempty"`
	Product string `json:"product,omitempty"`
	Virtual bool   `json:"virtual"` // the CPU advertises a hypervisor
}

// Firmware describes how the machine booted.
type Firmware struct {
	UEFI bool `json:"uefi"`
	// SecureBoot is nil when it could not be read (BIOS boot, or no efivars).
	SecureBoot *bool `json:"secure_boot,omitempty"`
}

// CPU is the instruction-set and topology summary.
type CPU struct {
	Vendor string `json:"vendor"`
	Model  string `json:"model"`
	// PsABILevel is the x86-64 psABI microarchitecture level: 1 (baseline), 2, 3 or 4.
	PsABILevel int    `json:"psabi_level"`
	PsABI      string `json:"psabi"` // "x86-64-v3"
	// MissingForV3 lists the flags that keep this CPU below x86-64-v3 (empty at v3+).
	MissingForV3  []string `json:"missing_for_v3,omitempty"`
	Sockets       int      `json:"sockets"`
	PhysicalCores int      `json:"physical_cores"`
	Threads       int      `json:"threads"`
	AVX2          bool     `json:"avx2"`
	AVX512F       bool     `json:"avx512f"`
	AVX512BF16    bool     `json:"avx512_bf16"`
	AVX512VNNI    bool     `json:"avx512_vnni"`
	AVXVNNI       bool     `json:"avx_vnni"`
	AMXTile       bool     `json:"amx_tile"`
	AMXBF16       bool     `json:"amx_bf16"`
	AMXInt8       bool     `json:"amx_int8"`
}

// Memory is from /proc/meminfo.
type Memory struct {
	TotalBytes     uint64 `json:"total_bytes"`
	AvailableBytes uint64 `json:"available_bytes"`
	SwapTotalBytes uint64 `json:"swap_total_bytes"`
}

// GPU is one PCI display-class device.
type GPU struct {
	PCIAddress string `json:"pci_address"`
	VendorID   string `json:"vendor_id"` // "0x10de"
	DeviceID   string `json:"device_id"`
	Vendor     string `json:"vendor"` // nvidia | amd | intel | other
	Driver     string `json:"driver,omitempty"`
	Integrated bool   `json:"integrated"`
	// VRAMBytes is 0 when VRAMKnown is false. Only amdgpu exposes it in sysfs; NVIDIA's
	// needs the vendor tool, which the live medium does not carry.
	VRAMBytes uint64 `json:"vram_bytes"`
	VRAMKnown bool   `json:"vram_known"`
}

// Disk is one whole block device.
type Disk struct {
	Name       string `json:"name"`            // nvme0n1
	Path       string `json:"path"`            // /dev/nvme0n1
	ByID       string `json:"by_id,omitempty"` // /dev/disk/by-id/... (stable name for zpool)
	SizeBytes  uint64 `json:"size_bytes"`
	Rotational bool   `json:"rotational"`
	Removable  bool   `json:"removable"`
	ReadOnly   bool   `json:"read_only"`
	// Transport: nvme | sata | sas | usb | virtio | mmc | scsi | unknown.
	Transport string `json:"transport"`
	// Kind is the class the pool layout uses: nvme | ssd | hdd.
	Kind   string `json:"kind"`
	Model  string `json:"model,omitempty"`
	Serial string `json:"serial,omitempty"`
	WWN    string `json:"wwn,omitempty"`
	// ConfirmID is what an operator types to confirm this disk as a target: the serial,
	// else the WWN, else "NOSERIAL-<name>-<size>G" (virtual disks often have neither).
	ConfirmID string `json:"confirm_id"`
	// BootMedia marks the device the live installer booted from. It is NEVER eligible.
	BootMedia bool `json:"boot_media"`
	InUse     bool `json:"in_use"`
	// ZFSMember is true when a partition (or the disk) carries a ZFS label: an existing
	// pool that a reinstall may import. Informational; it does not make a disk ineligible.
	ZFSMember         bool     `json:"zfs_member"`
	Eligible          bool     `json:"eligible"`
	IneligibleReasons []string `json:"ineligible_reasons,omitempty"`
}

// NIC is one network interface.
type NIC struct {
	Name          string `json:"name"`
	MAC           string `json:"mac,omitempty"`
	Driver        string `json:"driver,omitempty"`
	Physical      bool   `json:"physical"` // backed by a device (not a bridge/veth/tun)
	Wireless      bool   `json:"wireless"`
	Operstate     string `json:"operstate"`
	Carrier       bool   `json:"carrier"`
	SpeedMbps     int    `json:"speed_mbps"` // -1 when unknown (link down, wireless)
	IPv6Enabled   bool   `json:"ipv6_enabled"`
	IPv6LinkLocal bool   `json:"ipv6_link_local"`
	IPv6Global    bool   `json:"ipv6_global"` // has a global-scope address now
}

// TPM describes the first TPM device, if any.
type TPM struct {
	Present bool   `json:"present"`
	Version string `json:"version,omitempty"` // "2.0" | "1.2"
	Device  string `json:"device,omitempty"`  // tpm0
}

// Convenience accessors used by the planner.

// MemTotalMiB is MemTotal in MiB.
func (p *Probe) MemTotalMiB() int64 { return int64(p.Memory.TotalBytes >> 20) }
