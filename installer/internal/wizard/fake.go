// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

package wizard

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/org-runink/river/installer/internal/hw"
	"github.com/org-runink/river/installer/internal/planner"
)

//go:embed demo/probe.json
var demoProbe []byte

// FakeSystem is a System that touches nothing: river-installer --demo (to look at the UI on
// any machine, and to take the documentation's screenshots) and the unit tests use it. It
// plans with the real planner on a fixture probe, so the machine screen shows a real plan.
type FakeSystem struct {
	Delay      time.Duration // per simulated action; 0 = instant
	FailStep   string        // a step that fails once
	failed     bool
	Offline    bool
	RefuseAll  bool // make every plan refused (a machine below the minimums)
	EditionsIn []Edition
	Keyboards  []string
	Reboots    int
	USB        []USBDrive
	Saved      map[string][]byte
	StepEnv    map[string][]string // the environment each step ran with
	StepStdin  map[string]string
	mu         sync.Mutex
}

func (f *FakeSystem) sleep(ctx context.Context) error {
	if f.Delay == 0 {
		return nil
	}
	select {
	case <-time.After(f.Delay):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (f *FakeSystem) net() NetState {
	if f.Offline {
		return NetState{State: "offline", HasWifi: true}
	}
	return NetState{State: "online", Method: "auto", Wired: true, IPv4: true, IPv6: true, Internet4: true,
		Internet6: true, Iface: "eno1", Addrs: []string{"192.0.2.10/24", "2001:db8::10/64"}, HasWifi: true}
}

func (f *FakeSystem) NetAuto(ctx context.Context) (NetState, error)  { return f.net(), f.sleep(ctx) }
func (f *FakeSystem) NetCheck(ctx context.Context) (NetState, error) { return f.net(), nil }
func (f *FakeSystem) WifiScan(ctx context.Context) ([]WifiNet, error) {
	return []WifiNet{{"Example Lab", 82, true}, {"Guest", 40, false}}, f.sleep(ctx)
}
func (f *FakeSystem) WifiConnect(ctx context.Context, ssid, psk string) (NetState, error) {
	if err := f.sleep(ctx); err != nil {
		return NetState{}, err
	}
	if psk == "wrong-passphrase" {
		return NetState{}, errors.New("secrets were required, but not provided")
	}
	return NetState{State: "online", Method: "wifi", Wifi: true, IPv4: true, IPv6: true, Internet4: true,
		Internet6: true, Iface: "wlan0", Addrs: []string{"192.0.2.20/24"}, HasWifi: true}, nil
}

func (f *FakeSystem) Probe(ctx context.Context) ([]byte, error) { return demoProbe, f.sleep(ctx) }

func (f *FakeSystem) Plan(ctx context.Context, probe []byte, lab bool, profile string, models bool) ([]byte, bool, error) {
	var p hw.Probe
	if err := json.Unmarshal(probe, &p); err != nil {
		return nil, false, err
	}
	if f.RefuseAll && !lab {
		p.Memory.TotalBytes = 4 << 30
	}
	pl := planner.Build(&p, nil, planner.Options{Lab: lab, Profile: profile})
	b, err := json.Marshal(pl)
	return b, pl.Verdict == "refused", err
}

func (f *FakeSystem) Resolve(ctx context.Context, plan []byte, ids []string) (map[string]string, error) {
	if len(ids) == 0 {
		return nil, errors.New("no confirmed disk")
	}
	return map[string]string{"RUNINK_DISK": "/dev/nvme0n1", "RUNINK_POOL_TOPOLOGY": "mirror",
		"RUNINK_POOL_DISKS": "/dev/nvme0n1 /dev/nvme1n1", "RUNINK_SPECIAL_DISKS": ""}, nil
}

func (f *FakeSystem) HasStep(string) bool { return true }

func (f *FakeSystem) RunStep(ctx context.Context, step string, env []string, stdin io.Reader, out func(string)) error {
	f.mu.Lock()
	if f.StepEnv == nil {
		f.StepEnv, f.StepStdin = map[string][]string{}, map[string]string{}
	}
	f.StepEnv[step] = env
	if stdin != nil {
		b, _ := io.ReadAll(stdin)
		f.StepStdin[step] = string(b)
	}
	fail := step == f.FailStep && !f.failed
	if fail {
		f.failed = true
	}
	f.mu.Unlock()
	out("===> " + step)
	if step == "20-clone-rootfs" {
		for p := 0; p <= 100; p += 20 {
			out(fmt.Sprintf("    1,234,567,890  %d%%   98.76MB/s    0:00:12 (xfr#1, to-chk=0/1)", p))
			if err := f.sleep(ctx); err != nil {
				return err
			}
		}
	} else if err := f.sleep(ctx); err != nil {
		return err
	}
	if step == "10-disk-zfs" && stdin != nil {
		// A step that misbehaved and printed its input must still not leak it to the log.
		out("disk-zfs: debug stdin=" + strings.TrimSpace(f.StepStdin[step]))
	}
	if fail {
		out(step + ": simulated failure")
		return errors.New("exit status 1")
	}
	return nil
}

func (f *FakeSystem) Removable(ctx context.Context, exclude []string) ([]USBDrive, error) {
	var out []USBDrive
	for _, d := range f.USB {
		skip := false
		for _, x := range exclude {
			skip = skip || x == d.Dev
		}
		if !skip {
			out = append(out, d)
		}
	}
	return out, nil
}

func (f *FakeSystem) SaveToUSB(ctx context.Context, dev, name string, content []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Saved == nil {
		f.Saved = map[string][]byte{}
	}
	f.Saved[dev+"/"+name] = content
	return nil
}

func (f *FakeSystem) ApplyKeyboard(l layout) error {
	f.mu.Lock()
	f.Keyboards = append(f.Keyboards, l.XKB)
	f.mu.Unlock()
	return nil
}

func (f *FakeSystem) PrepareRetry(context.Context, string) error { return nil }

func (f *FakeSystem) Editions() []Edition {
	if f.EditionsIn != nil {
		return f.EditionsIn
	}
	return DemoEditions()
}

func (f *FakeSystem) MediumPayloads() bool { return false }

func (f *FakeSystem) Reboot() error {
	f.mu.Lock()
	f.Reboots++
	f.mu.Unlock()
	return nil
}

// DemoEditions are two editions like the ones this repository's profiles ship: a server that
// is running, and a workstation on the same stick.
func DemoEditions() []Edition {
	desc := func(en string) map[string]string { return map[string]string{"en": en} }
	return []Edition{
		{Version: 1, ID: "server", Title: "Runink River Server", MediumLabel: "RIVER", PlanProfile: "server",
			Description: desc("A server that runs your services on its own cluster."),
			Steps: []string{"00-preflight", "05-hwplan-verify", "10-disk-zfs", "20-clone-rootfs",
				"30-target-config", "32-locale-keyboard", "40-boot-grub-zfs", "50-runink-user",
				"75-install-plan", "77-firstboot-ui", "80-enable-s6", "90-export"},
			FirstbootUI: true, Hostname: "river-server", AdminUser: "runink", Self: true, Present: true},
		{Version: 1, ID: "workstation", Title: "Runink River Workstation", MediumLabel: "RUNINK_WORKSTATION",
			PlanProfile: "workstation", Description: desc("A desktop computer for everyday work."),
			Steps:    []string{"00-preflight", "10-disk-zfs", "20-clone-rootfs", "90-export"},
			Hostname: "river-workstation", AdminUser: "runink", Present: true},
	}
}

// FakeFirstboot is a FirstbootSystem for --demo and the tests: the checks settle after Delay,
// and hooks and pages come from the maps (a test writes real setup.json files under Dir).
type FakeFirstboot struct {
	Delay    time.Duration
	Dir      string // pages dir
	HookList []string
	Status   map[string]string
	Done     map[string]bool
	Retries  int
	Finished bool
	NoK0s    bool
	mu       sync.Mutex
}

func (f *FakeFirstboot) wait(ctx context.Context) {
	select {
	case <-time.After(f.Delay):
	case <-ctx.Done():
	}
}
func (f *FakeFirstboot) Net(ctx context.Context) (NetState, error) {
	f.wait(ctx)
	return NetState{State: "online", Wired: true, IPv4: true, IPv6: true, Iface: "eno1"}, nil
}
func (f *FakeFirstboot) SyncClock(ctx context.Context) (string, error) {
	f.wait(ctx)
	return "192.0.2.1", nil
}
func (f *FakeFirstboot) Firewall(ctx context.Context) (bool, error) { f.wait(ctx); return true, nil }
func (f *FakeFirstboot) HasK0s() bool                               { return !f.NoK0s }
func (f *FakeFirstboot) K0sReady(ctx context.Context) (bool, string, error) {
	f.wait(ctx)
	return true, "node river-server Ready", nil
}
func (f *FakeFirstboot) Hooks() []string { return f.HookList }
func (f *FakeFirstboot) HookStatus(n string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.Status[n]
}
func (f *FakeFirstboot) HookDone(n string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.Done[n]
}
func (f *FakeFirstboot) RetryHooks() error {
	f.mu.Lock()
	f.Retries++
	f.mu.Unlock()
	return nil
}
func (f *FakeFirstboot) PagesDir() string { return f.Dir }
func (f *FakeFirstboot) Ready(context.Context) Ready {
	return Ready{Hostname: "river-server", Addresses: []string{"192.0.2.10", "2001:db8::10"}, AdminUser: "runink",
		Fingerprints: []string{"256 SHA256:3q2+7wAAAAAAexampleexampleexampleexampleAAA root@river-server (ED25519)"}}
}
func (f *FakeFirstboot) Finish(context.Context) error {
	f.mu.Lock()
	f.Finished = true
	f.mu.Unlock()
	return nil
}

// RebootCount and IsFinished read the fakes' counters under their lock (the tests run with -race).
func (f *FakeSystem) RebootCount() int { f.mu.Lock(); defer f.mu.Unlock(); return f.Reboots }

func (f *FakeSystem) stdinOf(step string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.StepStdin[step]
}

func (f *FakeSystem) envOf(step string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.StepEnv[step]...)
}

func (f *FakeFirstboot) IsFinished() bool { f.mu.Lock(); defer f.mu.Unlock(); return f.Finished }

func (f *FakeFirstboot) setStatus(name, st string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Status[name] = st
}

func (f *FakeFirstboot) retries() int { f.mu.Lock(); defer f.mu.Unlock(); return f.Retries }
