// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// supply writes one /sys/class/power_supply/<name> directory of the fake tree.
func supply(t *testing.T, root, name string, files map[string]string) {
	t.Helper()
	d := filepath.Join(root, name)
	if err := os.MkdirAll(d, 0o755); err != nil {
		t.Fatal(err)
	}
	for f, v := range files {
		if err := os.WriteFile(filepath.Join(d, f), []byte(v+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func bat(capacity, status string) map[string]string {
	return map[string]string{"type": "Battery", "capacity": capacity, "status": status, "scope": "System"}
}

func TestBatteryLettersOnTheWayDownOnce(t *testing.T) {
	root := t.TempDir()
	supply(t, root, "AC", map[string]string{"type": "Mains", "online": "0"})
	supply(t, root, "hidpp_battery_0", map[string]string{"type": "Battery", "capacity": "3", "status": "Discharging", "scope": "Device"})
	var got []string
	say := func(s, b string) { got = append(got, s+": "+b) }
	w := batteryWatch{}
	for _, step := range []struct {
		capacity, status string
		want             []string
	}{
		{"40", "Discharging", nil},
		{"15", "Discharging", []string{"Battery low: BAT0 is at 15%. Plug the computer in soon."}},
		{"12", "Discharging", nil},
		{"5", "Discharging", []string{"Battery critically low: BAT0 is at 5%. Plug the computer in now, or save your work."}},
		{"4", "Discharging", nil},
		{"4", "Charging", nil},
		{"14", "Discharging", []string{"Battery low: BAT0 is at 14%. Plug the computer in soon."}},
	} {
		supply(t, root, "BAT0", bat(step.capacity, step.status))
		got = nil
		w.check(root, say)
		if !reflect.DeepEqual(got, step.want) {
			t.Fatalf("at %s%% %s: got %q, want %q", step.capacity, step.status, got, step.want)
		}
	}
}

func TestBatteryNoneIsQuiet(t *testing.T) {
	root := t.TempDir()
	supply(t, root, "AC", map[string]string{"type": "Mains", "online": "1"})
	supply(t, root, "BAT1", map[string]string{"type": "Battery", "status": "Unknown"}) // no capacity
	if b := readBatteries(root); len(b) != 0 {
		t.Fatalf("read %v from a tree with no usable battery", b)
	}
	if b := readBatteries(filepath.Join(root, "missing")); len(b) != 0 {
		t.Fatalf("read %v from no tree", b)
	}
}

func TestBatteryStopsWithItsContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := battery(ctx, t.TempDir(), func(string, string) {}); err != context.Canceled {
		t.Fatalf("got %v", err)
	}
}

// What gdbus prints for UDisks2 (GVariant text), cut to the properties that matter. sdb1 is a
// stick's filesystem; sdb the stick itself; sda2 the system disk; sdc1 a stick already mounted;
// sdd1 one udisks hides; the drive object ends sdd1's section.
const managedObjects = `({objectpath '/org/freedesktop/UDisks2/block_devices/sda2': {'org.freedesktop.UDisks2.Block': {'Device': <b'/dev/sda2'>, 'HintAuto': <false>, 'HintSystem': <true>, 'HintIgnore': <false>}, 'org.freedesktop.UDisks2.Filesystem': {'MountPoints': <@aay []>}}, ` +
	`objectpath '/org/freedesktop/UDisks2/block_devices/sdb': {'org.freedesktop.UDisks2.Block': {'Device': <b'/dev/sdb'>, 'HintAuto': <true>, 'HintSystem': <false>, 'HintIgnore': <false>}, 'org.freedesktop.UDisks2.PartitionTable': {'Type': <'dos'>}}, ` +
	`objectpath '/org/freedesktop/UDisks2/block_devices/sdb1': {'org.freedesktop.UDisks2.Block': {'Device': <b'/dev/sdb1'>, 'IdLabel': <'STICK'>, 'HintAuto': <true>, 'HintSystem': <false>, 'HintIgnore': <false>}, 'org.freedesktop.UDisks2.Filesystem': {'MountPoints': <@aay []>, 'Size': <uint64 0>}}, ` +
	`objectpath '/org/freedesktop/UDisks2/block_devices/sdc1': {'org.freedesktop.UDisks2.Block': {'Device': <b'/dev/sdc1'>, 'HintAuto': <true>, 'HintSystem': <false>, 'HintIgnore': <false>}, 'org.freedesktop.UDisks2.Filesystem': {'MountPoints': <[b'/run/media/runink/OTHER']>}}, ` +
	`objectpath '/org/freedesktop/UDisks2/block_devices/sdd1': {'org.freedesktop.UDisks2.Block': {'Device': <b'/dev/sdd1'>, 'HintAuto': <true>, 'HintSystem': <false>, 'HintIgnore': <true>}, 'org.freedesktop.UDisks2.Filesystem': {'MountPoints': <@aay []>}}, ` +
	`objectpath '/org/freedesktop/UDisks2/drives/Stick_0001': {'org.freedesktop.UDisks2.Drive': {'Removable': <true>, 'MountPoints': <@aay []>, 'HintAuto': <true>}}},)`

const addedLine = `/org/freedesktop/UDisks2: org.freedesktop.DBus.ObjectManager.InterfacesAdded (objectpath '/org/freedesktop/UDisks2/block_devices/sde1', {'org.freedesktop.UDisks2.Block': {'Device': <b'/dev/sde1'>, 'HintAuto': <true>, 'HintSystem': <false>, 'HintIgnore': <false>}, 'org.freedesktop.UDisks2.Filesystem': {'MountPoints': <@aay []>}})`

func TestToMountFollowsUdisksHints(t *testing.T) {
	if got := toMount(managedObjects); !reflect.DeepEqual(got, []string{"/dev/sdb1"}) {
		t.Fatalf("at login: got %v, want only the stick's filesystem /dev/sdb1", got)
	}
	if got := toMount(addedLine); !reflect.DeepEqual(got, []string{"/dev/sde1"}) {
		t.Fatalf("put in: got %v", got)
	}
	if got := toMount(`/org/freedesktop/UDisks2: org.freedesktop.DBus.ObjectManager.InterfacesRemoved (objectpath '/org/freedesktop/UDisks2/block_devices/sde1', ['org.freedesktop.UDisks2.Block'])`); len(got) != 0 {
		t.Fatalf("taken out: got %v", got)
	}
}

// fakeRunner answers GetManagedObjects, streams monitor lines once, and records mounts.
type fakeRunner struct {
	cancel  context.CancelFunc
	mounted []string
}

func (f *fakeRunner) output(_ context.Context, name string, args ...string) (string, error) {
	if name == "udisksctl" {
		dev := args[len(args)-1]
		f.mounted = append(f.mounted, dev)
		return "Mounted " + dev + " at /run/media/runink/X.", nil
	}
	return managedObjects, nil
}

func (f *fakeRunner) lines(_ context.Context, each func(string), _ string, _ ...string) error {
	readLines(strings.NewReader("/org/freedesktop/UDisks2: org.freedesktop.DBus.ObjectManager.InterfacesRemoved (objectpath '/x', [])\n"+addedLine+"\n"), each)
	f.cancel()
	return nil
}

func TestAutomountMountsWhatIsInAndWhatIsPutIn(t *testing.T) {
	var letters []string
	notify = func(s, b string) { letters = append(letters, s+": "+b) }
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	f := &fakeRunner{cancel: cancel}
	_ = automount(ctx, f)
	if !reflect.DeepEqual(f.mounted, []string{"/dev/sdb1", "/dev/sde1"}) {
		t.Fatalf("mounted %v", f.mounted)
	}
	if len(letters) != 2 || !strings.Contains(letters[1], "Mounted /dev/sde1 at /run/media/runink/X") {
		t.Fatalf("letters %q", letters)
	}
}

func TestUsage(t *testing.T) {
	var e bytes.Buffer
	if run(context.Background(), nil, &e) != 2 || run(context.Background(), []string{"nope"}, &e) != 2 {
		t.Fatalf("no job, or an unknown one, is not a usage error: %s", e.String())
	}
}

// Only the programs river-desk is written to start reach exec; anything else is refused before it.
func TestOnlyListedProgramsStart(t *testing.T) {
	for _, p := range []string{"foot", "xdg-open", "udisksctl", "gdbus", "notify-send"} {
		if err := allowed(p); err != nil {
			t.Errorf("allowed(%q) = %v, want nil", p, err)
		}
	}
	for _, p := range []string{"sh", "bash", "/bin/sh", "rm", ""} {
		if allowed(p) == nil {
			t.Errorf("allowed(%q) = nil, want a refusal", p)
		}
	}
	if _, err := (execRunner{}).output(context.Background(), "sh", "-c", "true"); err == nil {
		t.Error("execRunner.output ran a program outside the list")
	}
}
