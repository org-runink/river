// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

package planner

import (
	"fmt"
	"strings"
	"testing"
)

func TestResolve(t *testing.T) {
	m := loadManifest(t)
	base := loadProbe(t, "server-64g.probe.json")
	pl := Build(base, m, Options{})

	t.Run("confirmed", func(t *testing.T) {
		r, err := Resolve(pl, loadProbe(t, "server-64g.probe.json"), []string{"NVA0002", "NVA0001"})
		if err != nil {
			t.Fatal(err)
		}
		env := r.Env(pl)
		for _, want := range []string{
			"RUNINK_DISK='/dev/nvme0n1'", "RUNINK_POOL_TOPOLOGY='mirror'",
			"RUNINK_POOL_DISKS='/dev/nvme0n1 /dev/nvme1n1'", "RUNINK_SPECIAL_DISKS=''",
			"RUNINK_POOL_VDEV_WIDTH='2'", "RUNINK_ZRAM_SIZE=", "RUNINK_ZFS_ARC_MAX=",
		} {
			if !strings.Contains(env, want) {
				t.Errorf("env lacks %q:\n%s", want, env)
			}
		}
	})

	t.Run("renamed disks resolve by serial", func(t *testing.T) {
		fresh := loadProbe(t, "server-64g.probe.json")
		fresh.Disks[0].Name, fresh.Disks[0].Path = "nvme1n1", "/dev/nvme1n1"
		fresh.Disks[1].Name, fresh.Disks[1].Path = "nvme0n1", "/dev/nvme0n1"
		r, err := Resolve(pl, fresh, []string{"NVA0001", "NVA0002"})
		if err != nil {
			t.Fatal(err)
		}
		if r.BootDisk != "/dev/nvme1n1" {
			t.Errorf("boot disk %s: must follow the serial, not the old name", r.BootDisk)
		}
	})

	errs := []struct {
		name    string
		confirm []string
		mutate  func(fresh *probeT)
		want    string
	}{
		{"unconfirmed", []string{"NVA0001"}, nil, "not confirmed by serial: NVA0002"},
		{"nothing confirmed", nil, nil, "not confirmed by serial"},
		{"wrong serial", []string{"NVA0001", "NVA0002", "USBBOOT1"}, nil, "not a disk this plan will wipe"},
		{"disk gone", []string{"NVA0001", "NVA0002"}, func(f *probeT) { f.Disks = f.Disks[1:] }, "not present now"},
		{"now boot media", []string{"NVA0001", "NVA0002"}, func(f *probeT) { f.Disks[0].BootMedia = true }, "boot medium"},
		{"now in use", []string{"NVA0001", "NVA0002"}, func(f *probeT) {
			f.Disks[1].Eligible, f.Disks[1].IneligibleReasons = false, []string{"nvme1n1p1 is mounted at /mnt"}
		}, "not eligible now"},
		{"size changed", []string{"NVA0001", "NVA0002"}, func(f *probeT) { f.Disks[1].SizeBytes-- }, "bytes now"},
		{"duplicate serial", []string{"NVA0001", "NVA0002"}, func(f *probeT) { f.Disks[2].ConfirmID = "NVA0001" }, "matches 2 disks"},
	}
	for _, c := range errs {
		t.Run(c.name, func(t *testing.T) {
			fresh := loadProbe(t, "server-64g.probe.json")
			if c.mutate != nil {
				c.mutate(fresh)
			}
			_, err := Resolve(pl, fresh, c.confirm)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("err %v, want %q", err, c.want)
			}
		})
	}
}

func TestShQuote(t *testing.T) {
	if got := shQuote("a'b c"); got != `'a'\''b c'` {
		t.Errorf("shQuote: %s", got)
	}
}

// The two memory sizes 30-target-config applies (installer/lib/memtune.sh): zfs_arc_max in
// bytes, clamp(RAM/16, 1 GiB, 16 GiB), and the zram device in MiB. `river test memtune` pins
// the shell side's RAM/16 fallback to the same numbers.
func TestMemorySizesInEnv(t *testing.T) {
	for _, c := range []struct {
		probe, serial string
		gib           uint64
		arc, zram     string
	}{
		// 93 GiB: 95232 MiB / 16 = 5952 MiB of ARC (~5.8 GiB); zram RAM/4 = 23808, capped at 16 GiB.
		{"workstation-25g.probe.json", "WS0001", 93, "RUNINK_ZFS_ARC_MAX='6241124352'", "RUNINK_ZRAM_SIZE='16384M'"},
		// 16 GiB: RAM/16 = 1 GiB, the floor; zram RAM/2 below 32 GiB.
		{"edge-16g.probe.json", "SSD0256", 16, "RUNINK_ZFS_ARC_MAX='1073741824'", "RUNINK_ZRAM_SIZE='8192M'"},
		// 512 GiB: RAM/16 = 32 GiB, clamped to the 16 GiB ceiling.
		{"workstation-25g.probe.json", "WS0001", 512, "RUNINK_ZFS_ARC_MAX='17179869184'", "RUNINK_ZRAM_SIZE='16384M'"},
	} {
		t.Run(fmt.Sprintf("%dGiB", c.gib), func(t *testing.T) {
			p := loadProbe(t, c.probe)
			p.Memory.TotalBytes = c.gib << 30
			pl := Build(p, nil, Options{Profile: ProfileWorkstation})
			r, err := Resolve(pl, p, []string{c.serial})
			if err != nil {
				t.Fatal(err)
			}
			env := r.Env(pl)
			for _, want := range []string{c.arc, c.zram} {
				if !strings.Contains(env, want+"\n") {
					t.Errorf("env lacks %s:\n%s", want, env)
				}
			}
		})
	}
}
