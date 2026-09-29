// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

package wizard

import (
	"context"
	"io"
)

// System is everything the wizard does to the machine. The real implementation (sys_linux.go)
// runs the existing installer pieces: river-netsetup / river-netcheck, river-hwprobe /
// river-plan, the installer/lib steps, reboot. Tests and the --demo mode use a fake, so the
// state machine and the API are exercised without touching a disk.
type System interface {
	// NetAuto runs `river-netsetup --auto` and returns the recorded state
	// (/run/river/net-state.json).
	NetAuto(ctx context.Context) (NetState, error)
	// NetCheck re-reads the current network state without changing anything.
	NetCheck(ctx context.Context) (NetState, error)
	// WifiScan lists the networks in range.
	WifiScan(ctx context.Context) ([]WifiNet, error)
	// WifiConnect applies a Wi-Fi network through river-netsetup --config (the passphrase
	// travels in a 0600 file on /run that is removed before this returns).
	WifiConnect(ctx context.Context, ssid, psk string) (NetState, error)

	// Probe runs river-hwprobe --json; Plan runs river-plan on that probe.
	Probe(ctx context.Context) ([]byte, error)
	Plan(ctx context.Context, probe []byte, lab bool, profile string, models bool) (plan []byte, refused bool, err error)
	// Resolve runs river-plan --plan-file --probe (fresh) --env --confirm..., returning the
	// sh assignments for the steps.
	Resolve(ctx context.Context, plan []byte, confirmIDs []string) (map[string]string, error)

	// RunStep runs one installer step (sh LIB/<step>.sh) with env; stdin is given to the step
	// (the recovery key for 10-disk-zfs, nothing otherwise); every output line goes to out.
	RunStep(ctx context.Context, step string, env []string, stdin io.Reader, out func(line string)) error
	HasStep(step string) bool

	// Removable lists removable drives the recovery key may be saved to (never the boot
	// medium, never a disk the plan erases).
	Removable(ctx context.Context, exclude []string) ([]USBDrive, error)
	// SaveToUSB writes name with content to the drive's first mountable filesystem.
	SaveToUSB(ctx context.Context, dev, name string, content []byte) error

	// ApplyKeyboard sets the live session's keyboard (console, kiosk or desktop).
	ApplyKeyboard(l layout) error
	// PrepareRetry undoes a failed attempt's mounts and imports (umount the target, export the
	// pool) so the install can run again from the start.
	PrepareRetry(ctx context.Context, pool string) error
	// Editions lists the edition descriptors of the medium (Self and Present filled in).
	Editions() []Edition
	// MediumPayloads reports whether the boot medium carries encrypted payloads.
	MediumPayloads() bool

	Reboot() error
}

// NetState is the part of river.net-state/v1 the UI shows.
type NetState struct {
	State     string   `json:"state"` // online | lan-only | offline
	Method    string   `json:"method,omitempty"`
	Wired     bool     `json:"wired"`
	Wifi      bool     `json:"wifi"`
	IPv4      bool     `json:"ipv4"`
	IPv6      bool     `json:"ipv6"`
	Internet4 bool     `json:"internet4"`
	Internet6 bool     `json:"internet6"`
	Iface     string   `json:"iface,omitempty"`
	Addrs     []string `json:"addrs,omitempty"`
	Hostname  string   `json:"hostname,omitempty"`
	HasWifi   bool     `json:"has_wifi"` // a Wi-Fi device exists
}

type WifiNet struct {
	SSID   string `json:"ssid"`
	Signal int    `json:"signal"`
	Secure bool   `json:"secure"`
}

type USBDrive struct {
	Dev    string `json:"dev"`
	Model  string `json:"model"`
	SizeGB int    `json:"size_gb"`
}
