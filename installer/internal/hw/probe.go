// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

package hw

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Prober reads a machine's kernel interfaces under Root ("/" on a real machine; a fixture
// tree in tests). It never writes anything and never opens a block device.
type Prober struct {
	Root string
	Now  func() time.Time
}

func (pr *Prober) p(elem ...string) string {
	return filepath.Join(append([]string{pr.Root}, elem...)...)
}

func (pr *Prober) read(elem ...string) string {
	b, err := os.ReadFile(pr.p(elem...))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

func (pr *Prober) exists(elem ...string) bool {
	_, err := os.Stat(pr.p(elem...))
	return err == nil
}

// Probe gathers the whole document. Individual subsystems that cannot be read degrade to
// zero values plus a warning; only an unreadable /proc/cpuinfo or /proc/meminfo is an error,
// because without them no plan can be made.
func (pr *Prober) Probe() (*Probe, error) {
	out := &Probe{Schema: SchemaVersion, GPUs: []GPU{}, Disks: []Disk{}, NICs: []NIC{}}
	if pr.Now != nil {
		out.ProbedAt = pr.Now().UTC().Format(time.RFC3339)
	}
	cpu, virt, err := pr.cpu()
	if err != nil {
		return nil, err
	}
	out.CPU = cpu
	mem, err := pr.memory()
	if err != nil {
		return nil, err
	}
	out.Memory = mem
	out.Host = Host{
		Vendor:  pr.read("sys/class/dmi/id/sys_vendor"),
		Product: pr.read("sys/class/dmi/id/product_name"),
		Virtual: virt,
	}
	out.Firmware = pr.firmware()
	out.GPUs = pr.gpus()
	disks, warn := pr.disks()
	out.Disks = disks
	out.Warnings = append(out.Warnings, warn...)
	out.NICs = pr.nics()
	out.TPM = pr.tpm()
	return out, nil
}

// ---------------------------------------------------------------------------------- CPU

// psABI level requirements (System V x86-64 psABI, as the Linux flag names spell them).
// OSXSAVE is not listed in /proc/cpuinfo; the kernel only reports avx/avx2 when the OS
// has enabled XSAVE state for them, so `xsave` + `avx` covers it.
var (
	levelV2 = []string{"cx16", "lahf_lm", "popcnt", "sse4_1", "sse4_2", "ssse3"}
	levelV3 = []string{"avx", "avx2", "bmi1", "bmi2", "f16c", "fma", "abm", "movbe", "xsave"}
	levelV4 = []string{"avx512f", "avx512bw", "avx512cd", "avx512dq", "avx512vl"}
)

// PsABILevel returns the x86-64 microarchitecture level a flag set satisfies, and the flags
// missing for v3 (the level this image is built for).
func PsABILevel(flags map[string]bool) (int, []string) {
	has := func(req []string) (missing []string) {
		for _, f := range req {
			if !flags[f] {
				missing = append(missing, f)
			}
		}
		return
	}
	m2, m3, m4 := has(levelV2), has(levelV3), has(levelV4)
	level := 1
	if len(m2) == 0 {
		level = 2
		if len(m3) == 0 {
			level = 3
			if len(m4) == 0 {
				level = 4
			}
		}
	}
	return level, append(m2, m3...)
}

func (pr *Prober) cpu() (CPU, bool, error) {
	f, err := os.Open(pr.p("proc/cpuinfo"))
	if err != nil {
		return CPU{}, false, fmt.Errorf("read /proc/cpuinfo: %w", err)
	}
	defer f.Close()

	var c CPU
	flags := map[string]bool{}
	pairs := map[string]bool{}
	sockets := map[string]bool{}
	var phys, core string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Text()
		if strings.TrimSpace(line) == "" {
			if phys != "" || core != "" {
				pairs[phys+"/"+core] = true
			}
			phys, core = "", ""
			continue
		}
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		switch k {
		case "processor":
			c.Threads++
		case "vendor_id":
			if c.Vendor == "" {
				c.Vendor = v
			}
		case "model name":
			if c.Model == "" {
				c.Model = v
			}
		case "flags":
			if len(flags) == 0 {
				for _, fl := range strings.Fields(v) {
					flags[fl] = true
				}
			}
		case "physical id":
			phys = v
			sockets[v] = true
		case "core id":
			core = v
		}
	}
	if phys != "" || core != "" {
		pairs[phys+"/"+core] = true
	}
	if err := sc.Err(); err != nil {
		return CPU{}, false, fmt.Errorf("read /proc/cpuinfo: %w", err)
	}
	if c.Threads == 0 {
		return CPU{}, false, fmt.Errorf("/proc/cpuinfo lists no processors")
	}

	// Physical cores: unique (package, core) pairs from sysfs topology, which is present
	// for every online CPU on every architecture; /proc/cpuinfo's ids are the fallback.
	sysPairs := map[string]bool{}
	sysSockets := map[string]bool{}
	cpus, _ := filepath.Glob(pr.p("sys/devices/system/cpu/cpu[0-9]*"))
	for _, d := range cpus {
		pkg, err1 := os.ReadFile(filepath.Join(d, "topology/physical_package_id"))
		cid, err2 := os.ReadFile(filepath.Join(d, "topology/core_id"))
		if err1 != nil || err2 != nil {
			continue
		}
		p := strings.TrimSpace(string(pkg))
		sysPairs[p+"/"+strings.TrimSpace(string(cid))] = true
		sysSockets[p] = true
	}
	switch {
	case len(sysPairs) > 0:
		c.PhysicalCores, c.Sockets = len(sysPairs), len(sysSockets)
	case len(pairs) > 0 && !(len(pairs) == 1 && pairs["/"]):
		c.PhysicalCores, c.Sockets = len(pairs), max(1, len(sockets))
	default:
		c.PhysicalCores, c.Sockets = c.Threads, 1
	}

	c.PsABILevel, c.MissingForV3 = PsABILevel(flags)
	c.PsABI = fmt.Sprintf("x86-64-v%d", c.PsABILevel)
	if c.PsABILevel == 1 {
		c.PsABI = "x86-64"
	}
	c.AVX2 = flags["avx2"]
	c.AVX512F = flags["avx512f"]
	c.AVX512BF16 = flags["avx512_bf16"]
	c.AVX512VNNI = flags["avx512_vnni"]
	c.AVXVNNI = flags["avx_vnni"]
	c.AMXTile = flags["amx_tile"]
	c.AMXBF16 = flags["amx_bf16"]
	c.AMXInt8 = flags["amx_int8"]
	return c, flags["hypervisor"], nil
}

// ------------------------------------------------------------------------------- memory

func (pr *Prober) memory() (Memory, error) {
	s := pr.read("proc/meminfo")
	if s == "" {
		return Memory{}, fmt.Errorf("read /proc/meminfo: empty or missing")
	}
	var m Memory
	for _, line := range strings.Split(s, "\n") {
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		fs := strings.Fields(v)
		if len(fs) == 0 {
			continue
		}
		n, err := strconv.ParseUint(fs[0], 10, 64)
		if err != nil {
			continue
		}
		if len(fs) > 1 && fs[1] == "kB" {
			n *= 1024
		}
		switch k {
		case "MemTotal":
			m.TotalBytes = n
		case "MemAvailable":
			m.AvailableBytes = n
		case "SwapTotal":
			m.SwapTotalBytes = n
		}
	}
	if m.TotalBytes == 0 {
		return Memory{}, fmt.Errorf("/proc/meminfo has no MemTotal")
	}
	return m, nil
}

// ----------------------------------------------------------------------------- firmware

const efiGlobalGUID = "8be4df61-93ca-11d2-aa0d-00e098032b8c"

func (pr *Prober) firmware() Firmware {
	fw := Firmware{UEFI: pr.exists("sys/firmware/efi")}
	if !fw.UEFI {
		return fw
	}
	b, err := os.ReadFile(pr.p("sys/firmware/efi/efivars", "SecureBoot-"+efiGlobalGUID))
	if err == nil && len(b) >= 5 { // 4 attribute bytes, then the 1-byte value
		on := b[4] == 1
		fw.SecureBoot = &on
	}
	return fw
}

// ---------------------------------------------------------------------------------- GPU

var gpuVendors = map[string]string{"0x10de": "nvidia", "0x1002": "amd", "0x8086": "intel"}

func (pr *Prober) gpus() []GPU {
	out := []GPU{}
	devs, _ := filepath.Glob(pr.p("sys/bus/pci/devices/*"))
	sort.Strings(devs)
	for _, d := range devs {
		class, _ := os.ReadFile(filepath.Join(d, "class"))
		if !strings.HasPrefix(strings.TrimSpace(string(class)), "0x03") { // display controller
			continue
		}
		addr := filepath.Base(d)
		g := GPU{PCIAddress: addr}
		g.VendorID = readTrim(filepath.Join(d, "vendor"))
		g.DeviceID = readTrim(filepath.Join(d, "device"))
		g.Vendor = gpuVendors[g.VendorID]
		if g.Vendor == "" {
			g.Vendor = "other"
		}
		if l, err := os.Readlink(filepath.Join(d, "driver")); err == nil {
			g.Driver = filepath.Base(l)
		}
		if v := readTrim(filepath.Join(d, "mem_info_vram_total")); v != "" { // amdgpu
			if n, err := strconv.ParseUint(v, 10, 64); err == nil {
				g.VRAMBytes, g.VRAMKnown = n, true
			}
		}
		// Integrated: Intel's iGPU is always 00:02.0. AMD Zen APUs put the iGPU behind the
		// internal bridge at 00:08.x. Heuristics — informational only, the plan does not
		// offload to GPUs (see docs/INSTALLER-HARDWARE.md).
		switch g.Vendor {
		case "intel":
			g.Integrated = strings.HasSuffix(addr, ":00:02.0")
		case "amd":
			if real, err := filepath.EvalSymlinks(d); err == nil {
				g.Integrated = strings.Contains(real, "/0000:00:08.")
			}
		}
		out = append(out, g)
	}
	return out
}

func readTrim(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// -------------------------------------------------------------------------------- disks

var skipDisk = regexp.MustCompile(`^(loop|ram|zram|dm-|md|sr|fd|nbd|zd)`)

func (pr *Prober) udev(devnum string) map[string]string {
	m := map[string]string{}
	s := pr.read("run/udev/data", "b"+devnum)
	for _, line := range strings.Split(s, "\n") {
		if kv, ok := strings.CutPrefix(line, "E:"); ok {
			if k, v, ok := strings.Cut(kv, "="); ok {
				m[k] = v
			}
		}
	}
	return m
}

// devName reduces a /dev path (possibly a /dev/disk/by-* symlink) to a kernel name.
func (pr *Prober) devName(dev string) string {
	if !strings.HasPrefix(dev, "/dev/") {
		return ""
	}
	if strings.HasPrefix(dev, "/dev/disk/") {
		l, err := os.Readlink(pr.p(dev))
		if err != nil {
			return ""
		}
		return filepath.Base(l)
	}
	return filepath.Base(dev)
}

// bootSignals returns the kernel names (disk or partition) the live system booted from:
// whatever is mounted under the live medium's mount points, plus the device the kernel
// command line names by label or path.
func (pr *Prober) bootSignals(mounts [][2]string) map[string]string {
	sig := map[string]string{}
	for _, m := range mounts {
		src, mp := m[0], m[1]
		if mp == "/run/archiso/bootmnt" || strings.HasPrefix(mp, "/run/archiso/") ||
			mp == "/bootmnt" || mp == "/run/initramfs/live" || mp == "/run/rootfsbase" {
			if n := pr.devName(src); n != "" {
				sig[n] = "mounted as the live medium at " + mp
			}
		}
	}
	for _, tok := range strings.Fields(pr.read("proc/cmdline")) {
		k, v, ok := strings.Cut(tok, "=")
		if !ok || v == "" {
			continue
		}
		var n string
		switch k {
		case "label", "archisolabel", "img_label":
			n = pr.devName("/dev/disk/by-label/" + v)
		case "archisodevice", "img_dev":
			n = pr.devName(v)
		}
		if n != "" {
			sig[n] = "named by the kernel command line (" + k + "=" + v + ")"
		}
	}
	return sig
}

func (pr *Prober) mounts() [][2]string {
	var out [][2]string
	for _, line := range strings.Split(pr.read("proc/mounts"), "\n") {
		fs := strings.Fields(line)
		if len(fs) >= 2 {
			out = append(out, [2]string{fs[0], fs[1]})
		}
	}
	return out
}

func (pr *Prober) swaps() []string {
	var out []string
	for i, line := range strings.Split(pr.read("proc/swaps"), "\n") {
		fs := strings.Fields(line)
		if i == 0 || len(fs) == 0 {
			continue
		}
		out = append(out, fs[0])
	}
	return out
}

// byIDRank orders /dev/disk/by-id names: human-readable model_serial names first (what an
// operator can match against a drive label), WWN/EUI names after.
func byIDRank(n string) int {
	switch {
	case strings.HasPrefix(n, "nvme-eui."), strings.HasPrefix(n, "nvme-nvme."):
		return 5
	case strings.HasPrefix(n, "wwn-"):
		return 4
	case strings.HasPrefix(n, "ata-"), strings.HasPrefix(n, "nvme-"):
		return 0
	case strings.HasPrefix(n, "scsi-S"), strings.HasPrefix(n, "virtio-"):
		return 1
	case strings.HasPrefix(n, "scsi-"):
		return 2
	}
	return 3
}

func (pr *Prober) byID() map[string]string {
	best := map[string]string{}
	ents, _ := os.ReadDir(pr.p("dev/disk/by-id"))
	for _, e := range ents {
		n := e.Name()
		if strings.Contains(n, "-part") {
			continue
		}
		l, err := os.Readlink(pr.p("dev/disk/by-id", n))
		if err != nil {
			continue
		}
		k := filepath.Base(l)
		cur, ok := best[k]
		if !ok || byIDRank(n) < byIDRank(cur) || (byIDRank(n) == byIDRank(cur) && len(n) < len(cur)) {
			best[k] = n
		}
	}
	return best
}

func transportOf(name, real, bus string) string {
	switch {
	case strings.HasPrefix(name, "nvme"):
		return "nvme"
	case strings.Contains(real, "/usb"):
		return "usb"
	case strings.HasPrefix(name, "mmcblk"):
		return "mmc"
	case strings.HasPrefix(name, "vd"), strings.Contains(real, "/virtio"):
		return "virtio"
	case strings.Contains(real, "/ata"):
		return "sata"
	case strings.Contains(real, "/end_device-"), strings.Contains(real, "/sas"), strings.Contains(real, "/port-"):
		return "sas"
	}
	switch bus {
	case "ata":
		return "sata"
	case "usb":
		return "usb"
	case "scsi":
		return "scsi"
	}
	return "unknown"
}

// vpdSerial decodes a SCSI VPD page 0x80 (unit serial number): 4-byte header, then ASCII.
func vpdSerial(b []byte) string {
	if len(b) <= 4 {
		return ""
	}
	return strings.TrimSpace(strings.Trim(string(b[4:]), "\x00"))
}

// serial returns the disk's own serial number, from the first source that has one:
//
//  1. udev's ID_SERIAL_SHORT: the bare serial for ATA, SCSI, NVMe and USB disks.
//  2. /sys/block/<name>/serial: virtio-blk puts the serial on the DISK, not under device/
//     (device/ is the virtio device, which has none). Cloud and VM servers use virtio, and
//     udev sets no ID_SERIAL_SHORT for it.
//  3. /sys/block/<name>/device/serial: the NVMe controller of a namespace, and SCSI disks
//     on kernels that expose it.
//  4. /sys/block/<name>/device/vpd_pg80: the SCSI unit serial number page (SATA behind
//     libata, SAS, scsi-hd).
//  5. udev's ID_SERIAL, for virtio-blk (vd*) only: udev's virtio rule copies the serial
//     attribute there verbatim. For every other bus ID_SERIAL is "<model>_<serial>", which is not
//     what is printed on the drive, so it is never used for them.
func (pr *Prober) serial(name, base string, ud map[string]string) string {
	if s := strings.TrimSpace(ud["ID_SERIAL_SHORT"]); s != "" {
		return s
	}
	if s := pr.read("sys/block", name, "serial"); s != "" {
		return s
	}
	if s := pr.read("sys/block", name, "device/serial"); s != "" {
		return s
	}
	if b, err := os.ReadFile(filepath.Join(base, "device/vpd_pg80")); err == nil {
		if s := vpdSerial(b); s != "" {
			return s
		}
	}
	if strings.HasPrefix(name, "vd") {
		return strings.TrimSpace(ud["ID_SERIAL"])
	}
	return ""
}

func (pr *Prober) disks() ([]Disk, []string) {
	var warn []string
	mounts := pr.mounts()
	boot := pr.bootSignals(mounts)
	mounted := map[string]string{}
	for _, m := range mounts {
		if n := pr.devName(m[0]); n != "" {
			mounted[n] = m[1]
		}
	}
	swap := map[string]bool{}
	for _, s := range pr.swaps() {
		if n := pr.devName(s); n != "" {
			swap[n] = true
		}
	}
	ids := pr.byID()
	if !pr.exists("run/udev/data") {
		warn = append(warn, "no udev database at /run/udev/data: disk serials come from sysfs only")
	}

	ents, err := os.ReadDir(pr.p("sys/block"))
	if err != nil {
		return []Disk{}, append(warn, "cannot list /sys/block: "+err.Error())
	}
	out := []Disk{}
	for _, e := range ents {
		name := e.Name()
		if skipDisk.MatchString(name) {
			continue
		}
		base := pr.p("sys/block", name)
		sectors, _ := strconv.ParseUint(pr.read("sys/block", name, "size"), 10, 64)
		if sectors == 0 {
			continue // empty card reader / tray
		}
		real, _ := filepath.EvalSymlinks(base)
		devnum := pr.read("sys/block", name, "dev")
		ud := pr.udev(devnum)
		d := Disk{
			Name:       name,
			Path:       "/dev/" + name,
			SizeBytes:  sectors * 512,
			Rotational: pr.read("sys/block", name, "queue/rotational") == "1",
			Removable:  pr.read("sys/block", name, "removable") == "1",
			ReadOnly:   pr.read("sys/block", name, "ro") == "1",
		}
		if id, ok := ids[name]; ok {
			d.ByID = "/dev/disk/by-id/" + id
		}
		d.Transport = transportOf(name, real, ud["ID_BUS"])
		switch {
		case d.Transport == "nvme":
			d.Kind = "nvme"
		case d.Rotational:
			d.Kind = "hdd"
		default:
			d.Kind = "ssd"
		}
		d.Model = pr.read("sys/block", name, "device/model")
		if d.Model == "" {
			d.Model = strings.ReplaceAll(ud["ID_MODEL"], "_", " ")
		}
		d.Serial = pr.serial(name, base, ud)
		d.WWN = ud["ID_WWN"]
		if d.WWN == "" {
			d.WWN = pr.read("sys/block", name, "wwid")
		}
		if d.WWN == "" {
			d.WWN = pr.read("sys/block", name, "device/wwid")
		}
		switch {
		case d.Serial != "":
			d.ConfirmID = d.Serial
		case d.WWN != "":
			d.ConfirmID = d.WWN
		default:
			d.ConfirmID = fmt.Sprintf("NOSERIAL-%s-%dG", name, d.SizeBytes>>30)
		}

		// Whole disk and its partitions: boot medium, mounts, swap, holders, ZFS labels.
		var inUse []string
		check := func(n, dir string) {
			if why, ok := boot[n]; ok {
				d.BootMedia = true
				inUse = append(inUse, n+" "+why)
			}
			if mp, ok := mounted[n]; ok {
				inUse = append(inUse, n+" is mounted at "+mp)
			}
			if swap[n] {
				inUse = append(inUse, n+" is active swap")
			}
			if hs, _ := os.ReadDir(filepath.Join(dir, "holders")); len(hs) > 0 {
				inUse = append(inUse, n+" is held by "+hs[0].Name())
			}
			if pr.udev(pr.read(strings.TrimPrefix(dir, pr.Root), "dev"))["ID_FS_TYPE"] == "zfs_member" {
				d.ZFSMember = true
			}
		}
		check(name, base)
		subs, _ := os.ReadDir(base)
		for _, s := range subs {
			if pr.exists("sys/block", name, s.Name(), "partition") {
				check(s.Name(), filepath.Join(base, s.Name()))
			}
		}
		d.InUse = len(inUse) > 0

		var why []string
		if d.BootMedia {
			why = append(why, "boot medium of this installer")
		}
		why = append(why, inUse...)
		if d.Transport == "usb" {
			why = append(why, "USB-attached")
		}
		if d.Removable {
			why = append(why, "removable media")
		}
		if d.ReadOnly {
			why = append(why, "read-only")
		}
		if d.SizeBytes < 8<<30 {
			why = append(why, "smaller than 8 GiB")
		}
		d.Eligible = len(why) == 0
		if !d.Eligible {
			d.IneligibleReasons = dedupe(why)
		}
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, warn
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// --------------------------------------------------------------------------------- NICs

func (pr *Prober) nics() []NIC {
	out := []NIC{}
	inet6 := map[string][2]bool{} // name -> {link-local, global}
	for _, line := range strings.Split(pr.read("proc/net/if_inet6"), "\n") {
		fs := strings.Fields(line)
		if len(fs) < 6 {
			continue
		}
		cur := inet6[fs[5]]
		switch fs[3] { // scope
		case "20":
			cur[0] = true
		case "00":
			cur[1] = true
		}
		inet6[fs[5]] = cur
	}
	ents, _ := os.ReadDir(pr.p("sys/class/net"))
	for _, e := range ents {
		name := e.Name()
		if name == "lo" {
			continue
		}
		n := NIC{
			Name:      name,
			MAC:       pr.read("sys/class/net", name, "address"),
			Physical:  pr.exists("sys/class/net", name, "device"),
			Wireless:  pr.exists("sys/class/net", name, "wireless") || pr.exists("sys/class/net", name, "phy80211"),
			Operstate: pr.read("sys/class/net", name, "operstate"),
			Carrier:   pr.read("sys/class/net", name, "carrier") == "1",
			SpeedMbps: -1,
		}
		if l, err := os.Readlink(pr.p("sys/class/net", name, "device/driver")); err == nil {
			n.Driver = filepath.Base(l)
		}
		if s, err := strconv.Atoi(pr.read("sys/class/net", name, "speed")); err == nil && s > 0 {
			n.SpeedMbps = s
		}
		n.IPv6Enabled = pr.read("proc/sys/net/ipv6/conf", name, "disable_ipv6") != "1"
		a := inet6[name]
		n.IPv6LinkLocal, n.IPv6Global = a[0], a[1]
		out = append(out, n)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Physical != out[j].Physical {
			return out[i].Physical
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// ---------------------------------------------------------------------------------- TPM

func (pr *Prober) tpm() TPM {
	ents, _ := os.ReadDir(pr.p("sys/class/tpm"))
	for _, e := range ents {
		n := e.Name()
		if !strings.HasPrefix(n, "tpm") || strings.HasPrefix(n, "tpmrm") {
			continue
		}
		t := TPM{Present: true, Device: n}
		switch pr.read("sys/class/tpm", n, "tpm_version_major") {
		case "2":
			t.Version = "2.0"
		case "1":
			t.Version = "1.2"
		default:
			// Older kernels: a TPM 2.0 exposes a resource-manager node, a 1.2 does not.
			if pr.exists("sys/class/tpm", "tpmrm"+strings.TrimPrefix(n, "tpm")) {
				t.Version = "2.0"
			}
		}
		return t
	}
	return TPM{}
}
