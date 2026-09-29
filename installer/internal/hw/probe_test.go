// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

package hw

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type tree struct {
	t    *testing.T
	root string
}

func (tr tree) file(path, content string) {
	tr.t.Helper()
	p := filepath.Join(tr.root, path)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		tr.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		tr.t.Fatal(err)
	}
}

func (tr tree) dir(path string) {
	tr.t.Helper()
	if err := os.MkdirAll(filepath.Join(tr.root, path), 0o755); err != nil {
		tr.t.Fatal(err)
	}
}

func (tr tree) link(path, target string) {
	tr.t.Helper()
	p := filepath.Join(tr.root, path)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		tr.t.Fatal(err)
	}
	if err := os.Symlink(target, p); err != nil {
		tr.t.Fatal(err)
	}
}

// blockdev creates /sys/devices/<devpath>/block/<name> and the /sys/block/<name> link.
func (tr tree) blockdev(name, devpath, dev string, sectors string, attrs map[string]string, parts map[string]string) {
	base := filepath.Join("sys/devices", devpath, "block", name)
	tr.file(filepath.Join(base, "size"), sectors)
	tr.file(filepath.Join(base, "dev"), dev)
	for k, v := range attrs {
		tr.file(filepath.Join(base, k), v)
	}
	for p, pdev := range parts {
		tr.file(filepath.Join(base, p, "partition"), "1")
		tr.file(filepath.Join(base, p, "dev"), pdev)
		tr.dir(filepath.Join(base, p, "holders"))
	}
	rel, _ := filepath.Rel(filepath.Join(tr.root, "sys/block"), filepath.Join(tr.root, base))
	tr.link(filepath.Join("sys/block", name), rel)
}

const v3flags = "fpu cx16 lahf_lm popcnt sse4_1 sse4_2 ssse3 avx avx2 bmi1 bmi2 f16c fma abm movbe xsave"

func fixtureTree(t *testing.T) string {
	tr := tree{t, t.TempDir()}
	// Two SMT siblings of one physical core, plus one more core: 2 cores, 3 threads.
	tr.file("proc/cpuinfo", strings.Join([]string{
		"processor\t: 0\nvendor_id\t: GenuineIntel\nmodel name\t: Example CPU\nphysical id\t: 0\ncore id\t: 0\nflags\t\t: " + v3flags + "\n",
		"processor\t: 1\nvendor_id\t: GenuineIntel\nmodel name\t: Example CPU\nphysical id\t: 0\ncore id\t: 0\nflags\t\t: " + v3flags + "\n",
		"processor\t: 2\nvendor_id\t: GenuineIntel\nmodel name\t: Example CPU\nphysical id\t: 0\ncore id\t: 1\nflags\t\t: " + v3flags + "\n",
	}, "\n"))
	for i, core := range []string{"0", "0", "1"} {
		d := filepath.Join("sys/devices/system/cpu", "cpu"+string(rune('0'+i)), "topology")
		tr.file(filepath.Join(d, "physical_package_id"), "0")
		tr.file(filepath.Join(d, "core_id"), core)
	}
	tr.file("proc/meminfo", "MemTotal:       65536000 kB\nMemFree:  1 kB\nMemAvailable:   60000000 kB\nSwapTotal: 0 kB\n")

	// The live USB stick: sda, its partition mounted as the live medium.
	tr.blockdev("sda", "pci0000:00/0000:00:14.0/usb2/2-1/2-1:1.0/host6/target6:0:0/6:0:0:0", "8:0", "62500000",
		map[string]string{"removable": "1", "queue/rotational": "1", "ro": "0", "device/model": "Example Stick"},
		map[string]string{"sda1": "8:1"})
	tr.file("run/udev/data/b8:0", "E:ID_SERIAL_SHORT=USBSERIAL\nE:ID_BUS=usb\n")
	tr.file("proc/mounts", "/dev/sda1 /run/archiso/bootmnt iso9660 ro 0 0\noverlay / overlay rw 0 0\n")
	tr.file("proc/cmdline", "BOOT_IMAGE=/boot/vmlinuz label=RIVER quiet")
	tr.link("dev/disk/by-label/RIVER", "../../sda1")

	// An NVMe drive with a serial in sysfs only (no udev data).
	tr.blockdev("nvme0n1", "pci0000:00/0000:00:01.0/0000:01:00.0/nvme/nvme0", "259:0", "1953525168",
		map[string]string{"removable": "0", "queue/rotational": "0", "ro": "0", "device/serial": "  NVSERIAL  ", "device/model": "Example NVMe"}, nil)
	tr.link("dev/disk/by-id/nvme-eui.0001", "../../nvme0n1")
	tr.link("dev/disk/by-id/nvme-Example_NVMe_NVSERIAL", "../../nvme0n1")

	// A SATA HDD with a VPD page-80 serial and an old ZFS pool on its partition.
	tr.blockdev("sdb", "pci0000:00/0000:00:17.0/ata2/host1/target1:0:0/1:0:0:0", "8:16", "15628053168",
		map[string]string{"removable": "0", "queue/rotational": "1", "ro": "0", "device/vpd_pg80": "\x00\x80\x00\x08 HDSER01"},
		map[string]string{"sdb1": "8:17"})
	tr.file("run/udev/data/b8:17", "E:ID_FS_TYPE=zfs_member\n")
	tr.link("dev/disk/by-id/wwn-0x5000c500a1b2c3d4", "../../sdb")
	tr.link("dev/disk/by-id/ata-Example_HDD_HDSER01", "../../sdb")

	// A SATA SSD whose partition is held by device-mapper: in use.
	tr.blockdev("sdc", "pci0000:00/0000:00:17.0/ata3/host2/target2:0:0/2:0:0:0", "8:32", "976773168",
		map[string]string{"removable": "0", "queue/rotational": "0", "ro": "0"}, map[string]string{"sdc1": "8:33"})
	tr.file("sys/devices/pci0000:00/0000:00:17.0/ata3/host2/target2:0:0/2:0:0:0/block/sdc/sdc1/holders/dm-0", "")

	// Skipped kinds.
	tr.blockdev("loop0", "virtual", "7:0", "100", nil, nil)
	tr.blockdev("zram0", "virtual", "252:0", "100", nil, nil)

	tr.dir("sys/firmware/efi/efivars")
	tr.file("sys/firmware/efi/efivars/SecureBoot-"+efiGlobalGUID, "\x06\x00\x00\x00\x01")
	tr.file("sys/class/tpm/tpm0/tpm_version_major", "2")
	tr.file("sys/bus/pci/devices/0000:00:02.0/class", "0x030000")
	tr.file("sys/bus/pci/devices/0000:00:02.0/vendor", "0x8086")
	tr.file("sys/bus/pci/devices/0000:00:02.0/device", "0x7d55")
	tr.file("sys/bus/pci/devices/0000:01:00.0/class", "0x010802") // NVMe controller: not a GPU

	tr.file("sys/class/net/eth0/address", "02:00:00:00:00:01")
	tr.file("sys/class/net/eth0/operstate", "up")
	tr.file("sys/class/net/eth0/carrier", "1")
	tr.file("sys/class/net/eth0/speed", "10000")
	tr.dir("sys/class/net/eth0/device")
	tr.file("sys/class/net/br0/operstate", "down")
	tr.file("sys/class/net/lo/operstate", "unknown")
	tr.file("proc/net/if_inet6",
		"fe800000000000000000000000000001 02 40 20 80 eth0\n20010db8000000000000000000000001 02 40 00 00 eth0\n")
	return tr.root
}

func TestProbeFixtureTree(t *testing.T) {
	p, err := (&Prober{Root: fixtureTree(t)}).Probe()
	if err != nil {
		t.Fatal(err)
	}
	c := p.CPU
	if c.PsABILevel != 3 || c.PsABI != "x86-64-v3" || !c.AVX2 || c.AVX512F {
		t.Errorf("cpu level %+v", c)
	}
	if c.PhysicalCores != 2 || c.Threads != 3 || c.Sockets != 1 {
		t.Errorf("topology cores=%d threads=%d sockets=%d", c.PhysicalCores, c.Threads, c.Sockets)
	}
	if p.Memory.TotalBytes != 65536000*1024 {
		t.Errorf("memtotal %d", p.Memory.TotalBytes)
	}
	if !p.Firmware.UEFI || p.Firmware.SecureBoot == nil || !*p.Firmware.SecureBoot {
		t.Errorf("firmware %+v", p.Firmware)
	}
	if !p.TPM.Present || p.TPM.Version != "2.0" {
		t.Errorf("tpm %+v", p.TPM)
	}
	if len(p.GPUs) != 1 || p.GPUs[0].Vendor != "intel" || !p.GPUs[0].Integrated {
		t.Errorf("gpus %+v", p.GPUs)
	}

	disks := map[string]Disk{}
	for _, d := range p.Disks {
		disks[d.Name] = d
	}
	if len(disks) != 4 {
		t.Fatalf("want sda nvme0n1 sdb sdc, got %v", p.Disks)
	}
	sda := disks["sda"]
	if !sda.BootMedia || sda.Eligible || sda.Transport != "usb" || sda.ConfirmID != "USBSERIAL" {
		t.Errorf("boot usb %+v", sda)
	}
	nv := disks["nvme0n1"]
	if !nv.Eligible || nv.Kind != "nvme" || nv.Serial != "NVSERIAL" || nv.ByID != "/dev/disk/by-id/nvme-Example_NVMe_NVSERIAL" {
		t.Errorf("nvme %+v", nv)
	}
	if nv.SizeBytes != 1953525168*512 {
		t.Errorf("nvme size %d", nv.SizeBytes)
	}
	sdb := disks["sdb"]
	if !sdb.Eligible || sdb.Kind != "hdd" || sdb.Transport != "sata" || sdb.Serial != "HDSER01" || !sdb.ZFSMember ||
		sdb.ByID != "/dev/disk/by-id/ata-Example_HDD_HDSER01" {
		t.Errorf("sata hdd %+v", sdb)
	}
	sdc := disks["sdc"]
	if sdc.Eligible || !sdc.InUse || !strings.Contains(strings.Join(sdc.IneligibleReasons, ";"), "held by dm-0") {
		t.Errorf("held ssd %+v", sdc)
	}
	if sdc.ConfirmID != "NOSERIAL-sdc-465G" {
		t.Errorf("serial-less confirm id %q", sdc.ConfirmID)
	}

	if len(p.NICs) != 2 || p.NICs[0].Name != "eth0" {
		t.Fatalf("nics %+v", p.NICs)
	}
	e := p.NICs[0]
	if !e.Physical || !e.Carrier || e.SpeedMbps != 10000 || !e.IPv6LinkLocal || !e.IPv6Global || !e.IPv6Enabled {
		t.Errorf("eth0 %+v", e)
	}
	if p.NICs[1].Physical || p.NICs[1].SpeedMbps != -1 {
		t.Errorf("br0 %+v", p.NICs[1])
	}
}

func TestBootMediaByCmdlineOnly(t *testing.T) {
	root := fixtureTree(t)
	// No live-medium mount (copytoram): the kernel command line alone must mark it.
	if err := os.WriteFile(filepath.Join(root, "proc/mounts"), []byte("overlay / overlay rw 0 0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	p, err := (&Prober{Root: root}).Probe()
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range p.Disks {
		if d.Name == "sda" && (!d.BootMedia || d.Eligible) {
			t.Errorf("sda not marked as boot media from label=RIVER: %+v", d)
		}
	}
}

func TestPsABILevel(t *testing.T) {
	set := func(s string) map[string]bool {
		m := map[string]bool{}
		for _, f := range strings.Fields(s) {
			m[f] = true
		}
		return m
	}
	cases := []struct {
		flags   string
		level   int
		missing string
	}{
		{"", 1, "cx16 lahf_lm popcnt sse4_1 sse4_2 ssse3 avx avx2 bmi1 bmi2 f16c fma abm movbe xsave"},
		{"cx16 lahf_lm popcnt sse4_1 sse4_2 ssse3", 2, "avx avx2 bmi1 bmi2 f16c fma abm movbe xsave"},
		{v3flags, 3, ""},
		{v3flags + " avx512f avx512bw avx512cd avx512dq avx512vl", 4, ""},
		{v3flags + " avx512f", 3, ""}, // partial AVX-512 is not v4
		{"cx16 lahf_lm popcnt sse4_1 sse4_2 ssse3 avx avx2 bmi1 bmi2 fma abm movbe xsave", 2, "f16c"},
	}
	for _, c := range cases {
		l, m := PsABILevel(set(c.flags))
		if l != c.level || strings.Join(m, " ") != c.missing {
			t.Errorf("%q: level %d missing %v, want %d %q", c.flags, l, m, c.level, c.missing)
		}
	}
}

func TestProbeErrors(t *testing.T) {
	if _, err := (&Prober{Root: t.TempDir()}).Probe(); err == nil {
		t.Error("probe of an empty root succeeded")
	}
}

// TestDiskSerialSources pins where each bus keeps its serial. The confirm-by-serial gate is
// only as good as this: a disk probed as NOSERIAL-<name> cannot be confirmed with the serial
// printed on it (or given to a VM), and the installer refuses it. That is how the first
// server ISO failed its QEMU test: virtio-blk keeps the serial at /sys/block/vda/serial, and
// udev gives it ID_SERIAL but no ID_SERIAL_SHORT.
func TestDiskSerialSources(t *testing.T) {
	tr := tree{t, t.TempDir()}
	tr.file("proc/cpuinfo", "processor\t: 0\nflags\t\t: "+v3flags+"\n")
	tr.file("proc/meminfo", "MemTotal: 16000000 kB\n")
	tr.file("proc/mounts", "overlay / overlay rw 0 0\n")
	tr.file("proc/cmdline", "BOOT_IMAGE=/boot/vmlinuz label=RIVER")
	const g100 = "209715200" // 100 GiB in 512-byte sectors
	virtio := func(name, slot, dev string, attrs map[string]string) {
		tr.blockdev(name, "pci0000:00/0000:00:"+slot+"/virtio"+slot[:2], dev, g100, attrs, nil)
		tr.file(filepath.Join("sys/devices/pci0000:00/0000:00:"+slot+"/virtio"+slot[:2], "vendor"), "0x1af4")
	}
	// virtio-blk with a serial (qemu -device virtio-blk-pci,serial=..., cloud volumes): the
	// serial is on the disk, and udev copies it to ID_SERIAL only.
	virtio("vda", "02.0", "254:0", map[string]string{"serial": "RIVERQEMU0001", "queue/rotational": "1"})
	tr.file("run/udev/data/b254:0", "E:ID_SERIAL=RIVERQEMU0001\nE:ID_PATH=pci-0000:00:02.0\n")
	// virtio-blk on a kernel or udev that only has the udev key.
	virtio("vdb", "03.0", "254:16", nil)
	tr.file("run/udev/data/b254:16", "E:ID_SERIAL=CLOUDVOL-7\n")
	// virtio-blk with no serial at all: the fallback confirm id.
	virtio("vdc", "04.0", "254:32", nil)
	// scsi-hd on virtio-scsi (the transport reads "virtio") with no udev data: VPD page 0x80
	// and a wwid, as on SAS and on SATA behind libata.
	tr.blockdev("sdd", "pci0000:00/0000:00:05.0/virtio5/host2/target2:0:0/2:0:0:0", "8:48", g100,
		map[string]string{"device/vpd_pg80": "\x00\x80\x00\x0cSCSISER00042", "device/wwid": "naa.5000c500a1b2c3d4"}, nil)
	// SATA with udev: ID_SERIAL is model_serial and must NOT win over ID_SERIAL_SHORT.
	tr.blockdev("sde", "pci0000:00/0000:00:17.0/ata4/host3/target3:0:0/3:0:0:0", "8:64", g100, nil, nil)
	tr.file("run/udev/data/b8:64", "E:ID_SERIAL=Example_SSD_ATASER9\nE:ID_SERIAL_SHORT=ATASER9\nE:ID_BUS=ata\nE:ID_WWN=0x5002538e40a1b2c3\n")
	// SATA whose udev record has ID_SERIAL but no ID_SERIAL_SHORT: ID_SERIAL is still not a
	// serial, so the disk falls back to its WWN.
	tr.blockdev("sdf", "pci0000:00/0000:00:17.0/ata5/host4/target4:0:0/4:0:0:0", "8:80", g100, nil, nil)
	tr.file("run/udev/data/b8:80", "E:ID_SERIAL=Example_SSD_ATASER10\nE:ID_BUS=ata\nE:ID_WWN=0x5002538e40a1b2c4\n")
	// NVMe: the namespace's device/ is the controller, which carries the serial; wwid on the
	// namespace.
	tr.blockdev("nvme1n1", "pci0000:00/0000:00:06.0/0000:02:00.0/nvme/nvme1", "259:4", g100,
		map[string]string{"device/serial": "NVME-SER-77  ", "wwid": "eui.0025388b91b2c3d4"}, nil)

	p, err := (&Prober{Root: tr.root}).Probe()
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]Disk{}
	for _, d := range p.Disks {
		got[d.Name] = d
	}
	cases := []struct {
		name, transport, serial, wwn, confirm string
	}{
		{"vda", "virtio", "RIVERQEMU0001", "", "RIVERQEMU0001"},
		{"vdb", "virtio", "CLOUDVOL-7", "", "CLOUDVOL-7"},
		{"vdc", "virtio", "", "", "NOSERIAL-vdc-100G"},
		{"sdd", "virtio", "SCSISER00042", "naa.5000c500a1b2c3d4", "SCSISER00042"},
		{"sde", "sata", "ATASER9", "0x5002538e40a1b2c3", "ATASER9"},
		{"sdf", "sata", "", "0x5002538e40a1b2c4", "0x5002538e40a1b2c4"},
		{"nvme1n1", "nvme", "NVME-SER-77", "eui.0025388b91b2c3d4", "NVME-SER-77"},
	}
	for _, c := range cases {
		d, ok := got[c.name]
		if !ok {
			t.Errorf("%s: not probed (have %v)", c.name, p.Disks)
			continue
		}
		if d.Transport != c.transport || d.Serial != c.serial || d.WWN != c.wwn || d.ConfirmID != c.confirm {
			t.Errorf("%s: transport=%q serial=%q wwn=%q confirm=%q, want %q %q %q %q",
				c.name, d.Transport, d.Serial, d.WWN, d.ConfirmID, c.transport, c.serial, c.wwn, c.confirm)
		}
		if !d.Eligible {
			t.Errorf("%s: not eligible: %v", c.name, d.IneligibleReasons)
		}
	}
}
