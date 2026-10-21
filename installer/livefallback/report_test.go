// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
	"unicode/utf8"
)

// fake builds a small /sys, /proc and /run tree under a temp root.
type fake struct {
	t    *testing.T
	root string
}

func newFake(t *testing.T) fake { return fake{t: t, root: t.TempDir()} }

func (f fake) put(p, body string) {
	f.t.Helper()
	full := filepath.Join(f.root, p)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
		f.t.Fatal(err)
	}
}

func (f fake) mkdir(p string) {
	f.t.Helper()
	if err := os.MkdirAll(filepath.Join(f.root, p), 0o755); err != nil {
		f.t.Fatal(err)
	}
}

func (f fake) link(p, target string) {
	f.t.Helper()
	full := filepath.Join(f.root, p)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		f.t.Fatal(err)
	}
	if err := os.Symlink(target, full); err != nil {
		f.t.Fatal(err)
	}
}

// gpu adds a card whose device node is 226:0 and whose udev entry is db ("" = no entry at all).
func (f fake) gpu(driver, db string) {
	f.mkdir("/dev/dri")
	f.put("/dev/dri/card0", "")
	f.put("/sys/class/drm/card0/dev", "226:0\n")
	f.link("/sys/class/drm/card0/device/driver", "../../../bus/pci/drivers/"+driver)
	if db != "" {
		f.put("/run/udev/data/c226:0", db)
	}
}

func reportOf(f fake, full bool) string { return hardwareReport(f.root, nil, full) }

func mustHave(t *testing.T, got string, want ...string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(got, w) {
			t.Errorf("report lacks %q\n%s", w, got)
		}
	}
}

func mustNotHave(t *testing.T, got string, bad ...string) {
	t.Helper()
	for _, w := range bad {
		if strings.Contains(got, w) {
			t.Errorf("report must not contain %q\n%s", w, got)
		}
	}
}

// The stall this medium once had: a working GPU that udev never tagged for the seat.
func TestGPUWithoutSeatTagsIsNamed(t *testing.T) {
	f := newFake(t)
	f.gpu("i915", "S:dri/card0\nE:DEVNAME=/dev/dri/card0\n")
	mustHave(t, reportOf(f, false), "card0", "driver i915", "udev tags: NONE")
}

func TestGPUWithSeatTagsShowsThem(t *testing.T) {
	f := newFake(t)
	f.gpu("i915", "G:master-of-seat\nG:seat\nQ:master-of-seat\n")
	got := reportOf(f, false)
	mustHave(t, got, "udev tags: master-of-seat,seat")
	mustNotHave(t, got, "NONE")
}

func TestGPUWithNoUdevEntryIsNotCalledUntagged(t *testing.T) {
	f := newFake(t)
	f.gpu("i915", "")
	got := reportOf(f, false)
	mustHave(t, got, "NO DATABASE ENTRY")
	mustNotHave(t, got, "udev tags: NONE")
}

func TestNoDRMDeviceSaysNothingCanDraw(t *testing.T) {
	f := newFake(t)
	mustHave(t, reportOf(f, false), "EMPTY or missing", "none of i915")
}

// present / absent / unreadable are three states for the seat, not two.
func TestSeatStates(t *testing.T) {
	for name, tc := range map[string]struct{ state, want string }{
		"yes":     {"IS_VTNR=1\nCAN_GRAPHICAL=1\n", "CanGraphical=yes"},
		"no":      {"CAN_GRAPHICAL=0\n", "CanGraphical=NO"},
		"unknown": {"", "CanGraphical=unknown"},
	} {
		t.Run(name, func(t *testing.T) {
			f := newFake(t)
			if tc.state != "" {
				f.put("/run/systemd/seats/seat0", tc.state)
			}
			mustHave(t, reportOf(f, false), tc.want)
		})
	}
}

func TestNoNetworkDeviceIsStatedPlainly(t *testing.T) {
	f := newFake(t)
	f.mkdir("/sys/class/net/lo")
	mustHave(t, reportOf(f, false), "no network device exists besides lo")
}

func TestNetworkKindsAndRfkill(t *testing.T) {
	f := newFake(t)
	f.mkdir("/sys/class/net/lo")
	f.put("/sys/class/net/enp1s0/type", "1\n")
	f.put("/sys/class/net/enp1s0/operstate", "down\n")
	f.link("/sys/class/net/enp1s0/device/driver", "../../../../bus/pci/drivers/r8169")
	f.mkdir("/sys/class/net/wlan0/wireless")
	f.put("/sys/class/net/wlan0/operstate", "up\n")
	f.put("/sys/class/rfkill/rfkill0/type", "wlan\n")
	f.put("/sys/class/rfkill/rfkill0/name", "phy0\n")
	f.put("/sys/class/rfkill/rfkill0/soft", "1\n")
	f.put("/sys/class/rfkill/rfkill0/hard", "0\n")
	got := reportOf(f, false)
	mustHave(t, got, "enp1s0", "ethernet", "driver r8169", "wlan0", "wifi", "SOFT-BLOCKED")
	mustNotHave(t, got, "no network device exists")
}

func TestHardBlockOutranksSoftBlock(t *testing.T) {
	f := newFake(t)
	f.put("/sys/class/rfkill/rfkill0/type", "wlan\n")
	f.put("/sys/class/rfkill/rfkill0/soft", "1\n")
	f.put("/sys/class/rfkill/rfkill0/hard", "1\n")
	mustHave(t, reportOf(f, false), "HARD-BLOCKED")
}

func TestPCIWithoutDriverIsListedAndBoundOnesAreNot(t *testing.T) {
	f := newFake(t)
	f.put("/sys/bus/pci/devices/0000:00:02.0/vendor", "0x8086\n")
	f.put("/sys/bus/pci/devices/0000:00:02.0/device", "0x46a6\n")
	f.put("/sys/bus/pci/devices/0000:00:02.0/class", "0x030000\n")
	f.put("/sys/bus/pci/devices/0000:00:1f.6/vendor", "0x8086\n")
	f.put("/sys/bus/pci/devices/0000:00:1f.6/device", "0x1a1c\n")
	f.put("/sys/bus/pci/devices/0000:00:1f.6/class", "0x020000\n")
	f.link("/sys/bus/pci/devices/0000:00:1f.6/driver", "../../../bus/pci/drivers/e1000e")
	got := reportOf(f, false)
	mustHave(t, got, "0000:00:02.0", "8086:46a6", "NO DRIVER")
	// the bound NIC appears only in the full list, never in the "no driver" section
	summary := strings.SplitN(got, "-- kernel messages", 2)[0]
	mustNotHave(t, summary, "0000:00:1f.6")
	mustHave(t, reportOf(f, true), "0000:00:1f.6", "driver e1000e")
}

func TestAllBoundIsSaidNotSilent(t *testing.T) {
	f := newFake(t)
	f.put("/sys/bus/pci/devices/0000:00:02.0/class", "0x030000\n")
	f.link("/sys/bus/pci/devices/0000:00:02.0/driver", "../../../bus/pci/drivers/i915")
	mustHave(t, reportOf(f, false), "none (of 1 PCI devices")
}

func TestSummaryFitsTheScreenAndFullHasMore(t *testing.T) {
	f := newFake(t)
	f.put("/sys/class/dmi/id/sys_vendor", strings.Repeat("V", 300))
	f.put("/proc/cmdline", "quiet splash nomodeset\n")
	f.put("/proc/modules", "i915 1 0 - Live 0x0\nsnd 1 0 - Live 0x0\n")
	f.put("/run/log/sddm", strings.Repeat("sddm line that is long ", 20)+"\n")
	var kmsg []string
	for i := 0; i < 50; i++ {
		kmsg = append(kmsg, "i915 0000:00:02.0: something failed "+strings.Repeat("x", 200))
	}
	sum := hardwareReport(f.root, kmsg, false)
	full := hardwareReport(f.root, kmsg, true)
	for _, ln := range strings.Split(sum, "\n") {
		if utf8.RuneCountInString(ln) > reportScreenWidth+2 {
			t.Errorf("summary line wraps (%d runes): %q", utf8.RuneCountInString(ln), ln)
		}
	}
	if n := strings.Count(sum, "something failed"); n > 8 {
		t.Errorf("summary shows %d kernel messages, want at most 8", n)
	}
	if n := strings.Count(full, "something failed"); n != 50 {
		t.Errorf("full report has %d kernel messages, want all 50", n)
	}
	mustHave(t, full, "kernel command line", "nomodeset", "modules loaded", "i915 snd")
	mustNotHave(t, sum, "modules loaded", "kernel command line")
}

func TestSDDMLogIsQuotedWhenPresentAndMissingIsSaid(t *testing.T) {
	f := newFake(t)
	mustHave(t, reportOf(f, false), "no log: SDDM never wrote one")
	f.put("/var/log/sddm.log", "one\ntwo\nthree\nfour\nfive\nsix\n")
	got := reportOf(f, false)
	mustHave(t, got, "/var/log/sddm.log", "six", "three")
	mustNotHave(t, got, "  one\n")
}

func TestEmptyTreeDoesNotPanic(t *testing.T) {
	f := newFake(t)
	for _, full := range []bool{false, true} {
		if got := reportOf(f, full); got == "" {
			t.Errorf("empty report (full=%v)", full)
		}
	}
}

func TestFilterKmsg(t *testing.T) {
	in := []string{
		"6,1,1000000,-;usb 1-1: new device",             // info, no keyword: dropped
		"3,2,2000000,-;something broke",                 // err: kept by priority
		"6,3,3000000,-;i915 0000:00:02.0: [drm] probed", // keyword: kept
		" SUBSYSTEM=drm",                                // continuation: dropped
		"6,4,4000000,-;iwlwifi 0000:00:14.3: no ucode",  // keyword
		"garbage with no header",                        // malformed: dropped
		"",
	}
	got := filterKmsg(in, 0)
	if len(got) != 3 {
		t.Fatalf("kept %d, want 3: %q", len(got), got)
	}
	mustHave(t, strings.Join(got, "\n"), "something broke", "[drm]", "iwlwifi", "[   2.00]")
	if last := filterKmsg(in, 1); len(last) != 1 || !strings.Contains(last[0], "iwlwifi") {
		t.Errorf("max=1 must keep the newest, got %q", last)
	}
}

func TestReadKmsgUnreadableIsEmptyNotFatal(t *testing.T) {
	if got := readKmsg(filepath.Join(t.TempDir(), "nope"), 10); got != nil {
		t.Errorf("got %q", got)
	}
}

// --- stick ----------------------------------------------------------------------------------

type fakeStick struct {
	s        stick
	mounts   int
	umounts  int
	umountFn func(n int) error
	free     uint64
	dir      string // where the "partition" is mounted (a real temp dir)
}

func newFakeStick(t *testing.T, withLabel bool) *fakeStick {
	t.Helper()
	d := t.TempDir()
	fs := &fakeStick{free: 4 << 20}
	byLabel := filepath.Join(d, "by-label")
	if withLabel {
		dev := filepath.Join(d, "sda1")
		os.WriteFile(dev, nil, 0o644)
		if err := os.Symlink(dev, byLabel); err != nil {
			t.Fatal(err)
		}
	}
	fs.s = stick{
		byLabel: byLabel,
		mount: func(src, dst, fstype string, flags uintptr, data string) error {
			fs.mounts++
			fs.dir = dst
			if fstype != "vfat" {
				t.Errorf("fstype %q", fstype)
			}
			return nil
		},
		umount: func(dst string) error {
			fs.umounts++
			if fs.umountFn != nil {
				return fs.umountFn(fs.umounts)
			}
			return nil
		},
		freeBytes: func(string) (uint64, error) { return fs.free, nil },
		pause:     func(time.Duration) {},
	}
	return fs
}

func TestSaveWritesAndUnmounts(t *testing.T) {
	fs := newFakeStick(t, true)
	// capture the mount dir's content before MkdirTemp is cleaned up
	var got []byte
	var mode os.FileMode
	orig := fs.s.umount
	fs.s.umount = func(dst string) error {
		got, _ = os.ReadFile(filepath.Join(dst, "river-report.txt"))
		if st, err := os.Stat(filepath.Join(dst, "river-report.txt")); err == nil {
			mode = st.Mode().Perm()
		}
		return orig(dst)
	}
	where, err := fs.s.save("river-report.txt", []byte("hello"))
	if err != nil || where != "RIVER_EFI:/river-report.txt" {
		t.Fatalf("where=%q err=%v", where, err)
	}
	if mode != 0o600 {
		t.Errorf("report file mode %o, want 600 (gosec G302)", mode)
	}
	if string(got) != "hello" || fs.mounts != 1 || fs.umounts != 1 {
		t.Errorf("content=%q mounts=%d umounts=%d", got, fs.mounts, fs.umounts)
	}
}

func TestSaveWithoutPartitionSaysSoAndMountsNothing(t *testing.T) {
	fs := newFakeStick(t, false)
	_, err := fs.s.save("r.txt", []byte("x"))
	if err == nil || !strings.Contains(err.Error(), "no RIVER_EFI partition") || fs.mounts != 0 {
		t.Fatalf("err=%v mounts=%d", err, fs.mounts)
	}
}

// The one way this could do harm: a dirty FAT left mounted on a stick about to be pulled.
func TestWriteFailureStillUnmounts(t *testing.T) {
	fs := newFakeStick(t, true)
	m := fs.s.mount
	fs.s.mount = func(src, dst, fstype string, flags uintptr, data string) error {
		os.Mkdir(filepath.Join(dst, "r.txt"), 0o755) // a directory where the file must go
		return m(src, dst, fstype, flags, data)
	}
	_, err := fs.s.save("r.txt", []byte("x"))
	if err == nil {
		t.Fatal("write into a directory-named file must fail")
	}
	if fs.umounts != 1 {
		t.Errorf("umounts=%d: a failed write must still unmount", fs.umounts)
	}
}

func TestFullPartitionIsRefusedAndUnmounted(t *testing.T) {
	fs := newFakeStick(t, true)
	fs.free = 1000
	_, err := fs.s.save("r.txt", []byte("x"))
	if err == nil || !strings.Contains(err.Error(), "free") || fs.umounts != 1 {
		t.Fatalf("err=%v umounts=%d", err, fs.umounts)
	}
}

func TestUnmountIsRetriedThenReported(t *testing.T) {
	fs := newFakeStick(t, true)
	fs.umountFn = func(n int) error {
		if n < 3 {
			return errors.New("busy")
		}
		return nil
	}
	if _, err := fs.s.save("r.txt", []byte("x")); err != nil || fs.umounts != 3 {
		t.Fatalf("err=%v umounts=%d, want success on the 3rd try", err, fs.umounts)
	}
	fs2 := newFakeStick(t, true)
	fs2.umountFn = func(int) error { return errors.New("busy") }
	_, err := fs2.s.save("r.txt", []byte("x"))
	if err == nil || !strings.Contains(err.Error(), "unmounting") || fs2.umounts != 5 {
		t.Fatalf("err=%v umounts=%d, want a reported failure after 5", err, fs2.umounts)
	}
}

func TestBadNamesAreRefusedBeforeAnythingIsMounted(t *testing.T) {
	for _, n := range []string{"", ".", "..", "a/b", "../x", `a\b`, "a\x00b"} {
		fs := newFakeStick(t, true)
		if _, err := fs.s.save(n, []byte("x")); err == nil || fs.mounts != 0 {
			t.Errorf("name %q: err=%v mounts=%d", n, err, fs.mounts)
		}
	}
}

func TestOversizeReportIsTruncatedNotRefused(t *testing.T) {
	fs := newFakeStick(t, true)
	var size int64
	orig := fs.s.umount
	fs.s.umount = func(dst string) error {
		if st, err := os.Stat(filepath.Join(dst, "r.txt")); err == nil {
			size = st.Size()
		}
		return orig(dst)
	}
	if _, err := fs.s.save("r.txt", make([]byte, 5*maxReportBytes)); err != nil {
		t.Fatal(err)
	}
	if size == 0 || size > maxReportBytes+64 {
		t.Errorf("wrote %d bytes, cap is %d", size, maxReportBytes)
	}
}

func TestCutNeverSplitsACharacter(t *testing.T) {
	got := cut(strings.Repeat("é", 50), 10)
	if !utf8.ValidString(got) || utf8.RuneCountInString(got) != 10 {
		t.Errorf("got %q (%d runes)", got, utf8.RuneCountInString(got))
	}
	if cut("short", 10) != "short" {
		t.Error("a short line must pass through")
	}
}

// /dev/kmsg is a pollable character device. Opened non-blocking and read by Go's os.File, an EMPTY
// read does not return EAGAIN to the caller: the runtime parks the goroutine on the poller until
// there is more to read, which on a quiet log is never. As root (the live medium) that hung the whole
// fallback before it printed or saved anything; as a user the open fails first, which is why it was not
// seen on a workstation. A FIFO that is open for writing but empty behaves the same way.
func emptyPollableFile(t *testing.T) (path string, closeWriter func()) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "kmsg")
	if err := syscall.Mkfifo(p, 0o600); err != nil {
		t.Fatal(err)
	}
	w, err := os.OpenFile(p, os.O_RDWR, 0) // a writer that stays open keeps the read from seeing EOF
	if err != nil {
		t.Fatal(err)
	}
	return p, func() { _ = w.Close() }
}

func TestReadKmsgReturnsWhenThereIsNothingMoreToRead(t *testing.T) {
	p, closeW := emptyPollableFile(t)
	defer closeW()
	done := make(chan []string, 1)
	go func() { done <- readKmsg(p, 10) }()
	select {
	case got := <-done:
		if len(got) != 0 {
			t.Errorf("an empty log read as %q", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("readKmsg waits for more data on an empty non-blocking file: as root on /dev/kmsg it never returns")
	}
}
