// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

package main

// report.go — the machine describes itself.
//
// WHY THIS EXISTS. Every failure of the live medium on real hardware has been diagnosed from a
// photograph of a console, by someone who could not see the machine. The medium could say THAT
// the desktop did not start. It could not say what hardware it was on, which PCI devices had no
// driver, what NetworkManager could see, or what the kernel had complained about, and the one
// block it did print (the graphics state) scrolled off the screen under the installer's own
// output before anyone could photograph it. Testing "each piece of hardware on the planet" is
// not possible; a medium that reports what it found is.
//
// Everything here is a plain file read under a root directory, so it works on a medium with no
// lspci and no dmesg binary, cannot hang on a device, and can be pointed at a fake tree by a
// test. The only things that are not file reads are /dev/kmsg (a non-blocking read) and one
// bounded `loginctl` call when the seat state is not on disk.
//
// There are two views of the same facts. SUMMARY fits one screen so a photograph of it is the
// whole answer; FULL goes to a file on the stick (see stick.go) and is much longer.

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"
)

// reportScreenWidth is where summary lines are cut. A line that wraps costs two screen rows,
// and the summary has to fit on one screen next to the "desktop did not start" notice.
const reportScreenWidth = 112

// rpt reads a tree rooted at root ("/" on the medium).
type rpt struct{ root string }

func (r rpt) path(p string) string { return filepath.Join(r.root, p) }

// read returns the file's contents with surrounding space trimmed, or "" if it cannot be read.
func (r rpt) read(p string) string {
	b, err := os.ReadFile(r.path(p))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// link returns the last element of a symlink's target, or "" if it is not a symlink.
func (r rpt) link(p string) string {
	dst, err := os.Readlink(r.path(p))
	if err != nil {
		return ""
	}
	return filepath.Base(dst)
}

func (r rpt) glob(pattern string) []string {
	m, _ := filepath.Glob(r.path(pattern))
	sort.Strings(m)
	return m
}

func (r rpt) exists(p string) bool { _, err := os.Stat(r.path(p)); return err == nil }

// hex strips the 0x prefix sysfs puts on IDs.
func hex(s string) string { return strings.TrimPrefix(strings.TrimSpace(s), "0x") }

// cut shortens a line to the screen width.
func cut(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n-1]) + "~" // runes, not bytes: never cut a character in half
}

// --- machine ------------------------------------------------------------------------------

func (r rpt) machine() []string {
	var l []string
	dmi := func(f string) string { return r.read("/sys/class/dmi/id/" + f) }
	l = append(l, "machine      "+strings.TrimSpace(dmi("sys_vendor")+" "+dmi("product_name")+" "+dmi("product_version")))
	if v := strings.TrimSpace(dmi("bios_vendor") + " " + dmi("bios_version") + " " + dmi("bios_date")); v != "" {
		l = append(l, "firmware     "+v)
	}
	boot := "BIOS"
	if r.exists("/sys/firmware/efi") {
		boot = "UEFI"
	}
	cpu, n := "", 0
	for _, ln := range strings.Split(r.read("/proc/cpuinfo"), "\n") {
		if strings.HasPrefix(ln, "model name") {
			n++
			if cpu == "" {
				cpu = strings.TrimSpace(ln[strings.Index(ln, ":")+1:])
			}
		}
	}
	mem := ""
	for _, ln := range strings.Split(r.read("/proc/meminfo"), "\n") {
		if strings.HasPrefix(ln, "MemTotal:") {
			mem = strings.TrimSpace(strings.TrimPrefix(ln, "MemTotal:"))
		}
	}
	l = append(l, fmt.Sprintf("cpu          %s x%d   memory %s   boot %s", cpu, n, mem, boot))
	if v := r.read("/proc/version"); v != "" {
		f := strings.Fields(v)
		if len(f) >= 3 {
			l = append(l, "kernel       "+f[2])
		}
	}
	if osr := r.read("/etc/os-release"); osr != "" {
		for _, ln := range strings.Split(osr, "\n") {
			if strings.HasPrefix(ln, "PRETTY_NAME=") || strings.HasPrefix(ln, "BUILD_ID=") {
				l = append(l, "image        "+strings.Trim(ln[strings.Index(ln, "=")+1:], `"`))
			}
		}
	}
	return l
}

// --- display ------------------------------------------------------------------------------

// udevTags returns the tags udev recorded for a device node ("226:0"), and whether a database
// entry exists at all. The tags `seat` and `master-of-seat` are what make elogind count a GPU as
// graphics; their absence is how this medium once sat for six minutes with a working GPU and no
// desktop (see rc.local, "GPU seat watcher").
func (r rpt) udevTags(majmin string) (tags []string, have bool) {
	db := r.read("/run/udev/data/c" + majmin)
	if db == "" {
		return nil, false
	}
	seen := map[string]bool{}
	add := func(t string) {
		for _, x := range strings.FieldsFunc(t, func(c rune) bool { return c == ':' || c == ' ' }) {
			if x != "" && !seen[x] {
				seen[x] = true
				tags = append(tags, x)
			}
		}
	}
	for _, ln := range strings.Split(db, "\n") {
		switch {
		case strings.HasPrefix(ln, "G:"), strings.HasPrefix(ln, "Q:"):
			add(ln[2:])
		case strings.HasPrefix(ln, "E:TAGS="), strings.HasPrefix(ln, "E:CURRENT_TAGS="):
			add(ln[strings.Index(ln, "=")+1:])
		}
	}
	sort.Strings(tags)
	return tags, true
}

// seatGraphical answers "can seat0 do graphics", from elogind's state file when it is there and
// from loginctl (bounded) when it is not. The second return says where the answer came from.
func (r rpt) seatGraphical() (string, string) {
	if st := r.read("/run/systemd/seats/seat0"); st != "" {
		for _, ln := range strings.Split(st, "\n") {
			if strings.HasPrefix(ln, "CAN_GRAPHICAL=") {
				if strings.HasSuffix(ln, "=1") {
					return "yes", "/run/systemd/seats/seat0"
				}
				return "NO", "/run/systemd/seats/seat0"
			}
		}
	}
	if r.root == "/" {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if out, err := exec.CommandContext(ctx, "loginctl", "show-seat", "seat0", "-p", "CanGraphical").Output(); err == nil {
			v := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(string(out)), "CanGraphical="))
			switch v {
			case "yes":
				return "yes", "loginctl"
			case "no":
				return "NO", "loginctl"
			}
		}
	}
	return "unknown", "no seat state on disk and loginctl did not answer"
}

func (r rpt) display() []string {
	var l []string
	if ents, err := os.ReadDir(r.path("/dev/dri")); err == nil && len(ents) > 0 {
		var names []string
		for _, e := range ents {
			names = append(names, e.Name())
		}
		l = append(l, "/dev/dri     "+strings.Join(names, " "))
	} else {
		l = append(l, "/dev/dri     EMPTY or missing: no DRM device, nothing can draw")
	}

	for _, c := range r.glob("/sys/class/drm/card[0-9]*") {
		name := filepath.Base(c)
		if strings.Contains(name, "-") { // card0-HDMI-A-1 is a connector, handled below
			continue
		}
		drv := r.link("/sys/class/drm/" + name + "/device/driver")
		if drv == "" {
			drv = "(none)"
		}
		line := fmt.Sprintf("%-12s driver %s", name, drv)
		if mm := r.read("/sys/class/drm/" + name + "/dev"); mm != "" {
			if tags, have := r.udevTags(mm); !have {
				line += "   udev: NO DATABASE ENTRY"
			} else if !contains(tags, "seat") && !contains(tags, "master-of-seat") {
				line += "   udev tags: NONE (the seat cannot see this GPU)"
			} else {
				line += "   udev tags: " + strings.Join(tags, ",")
			}
		}
		l = append(l, line)
	}
	for _, st := range r.glob("/sys/class/drm/card*-*/status") {
		l = append(l, fmt.Sprintf("%-12s %s", "  "+filepath.Base(filepath.Dir(st)), r.read(strings.TrimPrefix(st, r.root))))
	}

	g, src := r.seatGraphical()
	l = append(l, fmt.Sprintf("seat0        CanGraphical=%s   (%s)", g, src))
	if w := r.read("/run/river-gpu-seat.log"); w != "" {
		for _, ln := range strings.Split(w, "\n") {
			l = append(l, "  watcher    "+ln)
		}
	}

	var found []string
	mods := r.read("/proc/modules")
	for _, want := range []string{"i915", "xe", "amdgpu", "nouveau", "simpledrm", "virtio_gpu", "bochs"} {
		for _, ln := range strings.Split(mods, "\n") {
			if strings.HasPrefix(ln, want+" ") {
				found = append(found, want)
				break
			}
		}
	}
	if len(found) == 0 {
		l = append(l, "gpu modules  none of i915/xe/amdgpu/nouveau/simpledrm loaded")
	} else {
		l = append(l, "gpu modules  "+strings.Join(found, " "))
	}
	return l
}

func contains(s []string, x string) bool {
	for _, v := range s {
		if v == x {
			return true
		}
	}
	return false
}

// --- network ------------------------------------------------------------------------------

func (r rpt) network() []string {
	var l []string
	n := 0
	for _, d := range r.glob("/sys/class/net/*") {
		name := filepath.Base(d)
		if name == "lo" {
			continue
		}
		n++
		kind := "other"
		switch {
		case r.exists("/sys/class/net/" + name + "/wireless"), r.exists("/sys/class/net/" + name + "/phy80211"):
			kind = "wifi"
		case r.read("/sys/class/net/"+name+"/type") == "1":
			kind = "ethernet"
		}
		drv := r.link("/sys/class/net/" + name + "/device/driver")
		if drv == "" {
			drv = "(none)"
		}
		l = append(l, fmt.Sprintf("%-12s %-8s driver %-12s %s", name, kind, drv, r.read("/sys/class/net/"+name+"/operstate")))
	}
	if n == 0 {
		l = append(l, "no network device exists besides lo: no driver bound to any NIC or Wi-Fi adapter")
	}
	for _, rf := range r.glob("/sys/class/rfkill/rfkill*") {
		name := filepath.Base(rf)
		soft, hard := r.read("/sys/class/rfkill/"+name+"/soft"), r.read("/sys/class/rfkill/"+name+"/hard")
		state := "ok"
		if hard == "1" {
			state = "HARD-BLOCKED (a hardware switch or firmware)"
		} else if soft == "1" {
			state = "SOFT-BLOCKED (software)"
		}
		l = append(l, fmt.Sprintf("rfkill       %-10s %-22s %s", r.read("/sys/class/rfkill/"+name+"/type"), r.read("/sys/class/rfkill/"+name+"/name"), state))
	}
	return l
}

// --- PCI ----------------------------------------------------------------------------------

type pciDev struct{ addr, id, class, driver string }

// interesting is a device whose missing driver would explain a symptom the owner can see: a GPU
// (class 03), a NIC or Wi-Fi adapter (02), or another wireless controller (0d).
func (p pciDev) interesting() bool {
	return strings.HasPrefix(p.class, "03") || strings.HasPrefix(p.class, "02") || strings.HasPrefix(p.class, "0d")
}

func (r rpt) pci() []pciDev {
	var out []pciDev
	for _, d := range r.glob("/sys/bus/pci/devices/*") {
		addr := filepath.Base(d)
		p := pciDev{
			addr:   addr,
			id:     hex(r.read("/sys/bus/pci/devices/"+addr+"/vendor")) + ":" + hex(r.read("/sys/bus/pci/devices/"+addr+"/device")),
			class:  hex(r.read("/sys/bus/pci/devices/" + addr + "/class")),
			driver: r.link("/sys/bus/pci/devices/" + addr + "/driver"),
		}
		out = append(out, p)
	}
	return out
}

func pciClassName(c string) string {
	switch {
	case strings.HasPrefix(c, "0300"), strings.HasPrefix(c, "0302"):
		return "GPU"
	case strings.HasPrefix(c, "03"):
		return "display"
	case strings.HasPrefix(c, "0280"):
		return "Wi-Fi/other net"
	case strings.HasPrefix(c, "02"):
		return "network"
	case strings.HasPrefix(c, "0d"):
		return "wireless"
	}
	return "class " + c
}

// --- kernel messages ----------------------------------------------------------------------

// kmsgKeywords make a kernel message relevant to "why is there no desktop / no network card".
var kmsgKeywords = []string{
	"firmware", "failed", "error", "timed out", "unable to", "i915", "amdgpu", "nouveau", "[drm", " drm",
	"iwlwifi", "r8169", "r8125", "igc ", "e1000", "rtw8", "ath1", "ath9", "brcm", "mt76", "mt79",
	"bluetooth", "btusb", "simpledrm", "efifb", "seat", "sddm",
}

// readKmsg returns the kernel messages worth showing, oldest first, at most max of them.
//
// It reads with raw syscalls on purpose. /dev/kmsg is pollable, and an os.File on a pollable
// descriptor does not hand EAGAIN back: the runtime parks the goroutine until there is more to
// read, which on a quiet log is never. As root on the live medium that blocked the whole fallback
// before it printed or saved anything (2026-10-21, found only by booting the built ISO; a
// workstation user cannot open /dev/kmsg, so the hang never showed there). Here EAGAIN means "no
// more records right now" and ends the read.
func readKmsg(path string, max int) []string {
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_NONBLOCK|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil
	}
	defer syscall.Close(fd)
	var lines []string
	buf := make([]byte, 8192) // one /dev/kmsg record per read, at most 8 KiB
	for reads := 0; reads < 100000; reads++ {
		n, err := syscall.Read(fd, buf)
		switch {
		case err == syscall.EINTR, err == syscall.EPIPE: // EPIPE: records were overwritten; the next read continues
			continue
		case err != nil || n == 0: // EAGAIN: nothing more right now; 0: end of a plain file
			return filterKmsg(lines, max)
		}
		lines = append(lines, strings.Split(strings.TrimRight(string(buf[:n]), "\n"), "\n")...)
	}
	return filterKmsg(lines, max)
}

// filterKmsg keeps warnings and worse, plus anything naming a relevant driver or firmware, and
// returns the last max. A /dev/kmsg record is "prio,seq,usec,flags;message"; continuation lines
// (key=value metadata) start with a space and are dropped.
func filterKmsg(lines []string, max int) []string {
	var keep []string
	for _, ln := range lines {
		if ln == "" || ln[0] == ' ' {
			continue
		}
		semi := strings.Index(ln, ";")
		if semi < 0 {
			continue
		}
		hdr, msg := strings.Split(ln[:semi], ","), ln[semi+1:]
		prio := 6
		if len(hdr) > 0 {
			if _, err := fmt.Sscanf(hdr[0], "%d", &prio); err != nil {
				prio = 6 // unparseable: treat as informational, as the default above does
			}
		}
		prio &= 7
		low := strings.ToLower(msg)
		want := prio <= 4
		for _, k := range kmsgKeywords {
			if strings.Contains(low, k) {
				want = true
				break
			}
		}
		if !want {
			continue
		}
		ts := ""
		if len(hdr) > 2 {
			var us int64
			if _, err := fmt.Sscanf(hdr[2], "%d", &us); err == nil {
				ts = fmt.Sprintf("[%7.2f] ", float64(us)/1e6)
			}
		}
		keep = append(keep, ts+msg)
	}
	if max > 0 && len(keep) > max {
		keep = keep[len(keep)-max:]
	}
	return keep
}

// --- the two views ------------------------------------------------------------------------

// sddmTail returns the last n lines of the display manager's own log, and which file it was.
func (r rpt) sddmTail(n int) (string, []string) {
	for _, p := range []string{"/run/log/sddm", "/var/log/sddm.log", "/run/sddm.log"} {
		if v := r.read(p); v != "" {
			lines := strings.Split(v, "\n")
			if len(lines) > n {
				lines = lines[len(lines)-n:]
			}
			return p, lines
		}
	}
	return "", nil
}

// hardwareReport builds the report. full=false is the one-screen summary; full=true is the file.
// kmsg is passed in already read so the whole thing stays deterministic under test.
func hardwareReport(root string, kmsg []string, full bool) string {
	r := rpt{root: root}
	var b strings.Builder
	w := func(s string) {
		if !full {
			s = cut(s, reportScreenWidth)
		}
		b.WriteString("  " + s + "\n")
	}
	section := func(title string) { b.WriteString("\n  -- " + title + " --\n") }

	if full {
		b.WriteString("Runink River hardware report\n")
		b.WriteString("Written by river-live-fallback on the live medium. Plain file reads of /sys, /proc and /run.\n")
	} else {
		b.WriteString("  What this computer reports (photograph this screen if you report the problem):\n")
	}

	section("machine")
	for _, l := range r.machine() {
		w(l)
	}
	section("display")
	for _, l := range r.display() {
		w(l)
	}
	section("network")
	for _, l := range r.network() {
		w(l)
	}

	devs := r.pci()
	var bare []pciDev
	for _, d := range devs {
		if d.interesting() && d.driver == "" {
			bare = append(bare, d)
		}
	}
	section("PCI devices that matter and have NO driver")
	if len(bare) == 0 {
		w(fmt.Sprintf("none (of %d PCI devices, every GPU / network / wireless device has a driver)", len(devs)))
	}
	for _, d := range bare {
		w(fmt.Sprintf("%s  %s  %s  NO DRIVER", d.addr, d.id, pciClassName(d.class)))
	}
	if full {
		section("all PCI devices")
		for _, d := range devs {
			drv := d.driver
			if drv == "" {
				drv = "-"
			}
			w(fmt.Sprintf("%s  %s  class %s  driver %s", d.addr, d.id, d.class, drv))
		}
		section("USB devices")
		for _, d := range r.glob("/sys/bus/usb/devices/[0-9]*") {
			n := filepath.Base(d)
			if strings.Contains(n, ":") {
				continue
			}
			w(fmt.Sprintf("%s  %s:%s  %s", n, hex(r.read("/sys/bus/usb/devices/"+n+"/idVendor")),
				hex(r.read("/sys/bus/usb/devices/"+n+"/idProduct")), r.read("/sys/bus/usb/devices/"+n+"/product")))
		}
		section("kernel command line")
		w(r.read("/proc/cmdline"))
		section("modules loaded")
		var names []string
		for _, ln := range strings.Split(r.read("/proc/modules"), "\n") {
			if f := strings.Fields(ln); len(f) > 0 {
				names = append(names, f[0])
			}
		}
		w(strings.Join(names, " "))
	}

	section("kernel messages that matter")
	show := kmsg
	lim := 8
	if full {
		lim = len(show)
	}
	if len(show) > lim {
		show = show[len(show)-lim:]
	}
	if len(show) == 0 {
		w("(none read: /dev/kmsg was not readable, or nothing relevant was logged)")
	}
	for _, m := range show {
		w(m)
	}

	n := 4
	if full {
		n = 30
	}
	if p, lines := r.sddmTail(n); p != "" {
		section("display manager log, last " + fmt.Sprint(len(lines)) + " (" + p + ")")
		for _, ln := range lines {
			w(ln)
		}
	} else {
		section("display manager log")
		w("(no log: SDDM never wrote one, or it is somewhere this does not look)")
	}
	b.WriteString("\n")
	return b.String()
}
