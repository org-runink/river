// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

package testcmd

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// medium-unaided boots an ISO with NOTHING injected and decides whether the MEDIUM reaches
// the graphical installer on its own.
//
// This is the check build/qemu-gui-test.sh tells you to write and is not. That harness
// overlays repo-built binaries over the image's own river-installer and river-kiosk, and
// starts the wizard when the image did not; its own header says so. A green gui-desktop there
// means the INSTALLER works. It cannot mean the medium boots to the wizard unaided, because it
// repairs the thing it is measuring. RELEASE.md criterion 3 requires both.
//
// WHY IT EXISTS (2026-10-18). The shipped 2026-10-18 ISO booted to a text console. SDDM puts
// its greeter on VT 1 and ignores MinimumVT in Wayland mode; the live medium also ran an
// autologin getty there. sddm-helper could not take the TTY, logged
// "Greeter stopped. SDDM::Auth::HELPER_TTY_ERROR / Jumping to VT 1" and respawned forever --
// 194 crashes, screen black, and nothing on the console saying the display manager was dying.
// No existing check could see it: the GUI harness started the wizard itself, and CI never
// boots a medium.
//
// SINCE DESK (2026-10-31). The live medium no longer starts a desktop: greetd's initial_session
// runs weston, as the live user, with the installer full screen (live-overlay/etc/greetd/
// live.toml); river-live-fallback takes over only when no compositor appears. So the questions
// are greetd's and that kiosk's, and the screen itself is judged by size and entropy
// (`river test screen`), because a grey-mean check passed QEMU's "Guest has not initialized the
// display" placeholder on 2026-10-30.
//
// RIG. -cpu host, and virtio-vga so /dev/dri/renderD128 exists. With QEMU's default bochs VGA
// there is no render node, a GPU compositor quits with "Found no render device!", and EVERY
// image fails for a reason that belongs to the rig. A disk is attached because the installer
// plan wants one; nothing is written to it.

const (
	// ovmfCodeDefault and ovmfVarsDefault are overridable because distributions disagree.
	ovmfCodeDefault = "/usr/share/edk2/x64/OVMF_CODE.4m.fd"
	ovmfVarsDefault = "/usr/share/edk2/x64/OVMF_VARS.4m.fd"
)

func mediumUnaidedCmd(v *viper.Viper) *cobra.Command {
	c := &cobra.Command{
		Use:   "medium-unaided <iso>",
		Short: "boot an ISO with nothing injected: does the MEDIUM reach the graphical installer by itself",
		Long: `Boot ISO under QEMU with nothing injected, started or overlaid, and ask the running
system these questions (and the connectivity ones below):

  medium-dm-started   greetd is running within 3m
  medium-dm-stable    its s6 supervisor PID is unchanged AND positive across 12s
  medium-kiosk        the live session (weston, as the live user) is up in BOTH of two samples
  medium-autologin    no login screen (gtkgreet) is waiting: the initial session took effect
  medium-installer    river-installer is running, and its page is open in the kiosk's browser
  medium-desk-session DEsk's session (desk-session, wayland-sessions/desk.desktop) is on the medium
  medium-screen       a screendump shows something drawn (river test screen: size and entropy)

Unlike qemu-gui-test, this overlays nothing and starts nothing: a green run here means the
MEDIUM works, which is the thing an owner is actually asked to trust. Needs qemu-system-x86_64
with KVM and OVMF firmware.`,
		Args: func(_ *cobra.Command, args []string) error {
			if len(args) != 1 {
				return fmt.Errorf("need exactly one ISO")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return MediumUnaided(cmd.Context(), args[0],
				v.GetString("ovmf-code"), v.GetString("ovmf-vars"), v.GetString("screen-out"),
				cmd.OutOrStdout(), cmd.ErrOrStderr())
		},
	}
	c.Flags().String("ovmf-code", ovmfCodeDefault, "OVMF_CODE firmware image")
	c.Flags().String("ovmf-vars", ovmfVarsDefault, "OVMF_VARS template (copied, never written in place)")
	c.Flags().String("screen-out", "medium-unaided-screen.png", "where the screendump medium-screen judged is kept")
	return c
}

// probeScript runs inside the booted ISO. One KEY=VALUE per line, RV_DONE last, so a
// truncated transfer can never be mistaken for a verdict.
//
// It settles before sampling. A fixed delay is not enough: sampled too early the display
// manager is not up yet and a respawning compositor is briefly in `ps`, so an early probe
// PASSES a crash-looping image. Observed doing exactly that on the 2026-10-18 ISO (then with
// SDDM and Plasma): one run OK, the next FAIL, same image.
const probeScript = `#!/bin/sh
n=0
while [ "$n" -lt 36 ]; do
	pgrep -x greetd >/dev/null && break
	n=$((n + 1)); sleep 5
done
if pgrep -x greetd >/dev/null; then L=1; else L=0; fi
sleep 30
A=$(sudo s6-svstat -p /run/service/greetd 2>/dev/null)
KA=$(pgrep -u runink -x weston | grep -c .)
sleep 12
B=$(sudo s6-svstat -p /run/service/greetd 2>/dev/null)
KB=$(pgrep -u runink -x weston | grep -c .)
echo "RV_DMUP=${L:-}"
echo "RV_PIDA=${A:-}"
echo "RV_PIDB=${B:-}"
echo "RV_KIOSKA=${KA:-}"
echo "RV_KIOSKB=${KB:-}"
echo "RV_GREETER=$(pgrep -x gtkgreet | grep -c .)"
echo "RV_INST=$(ps -eo comm | grep -c '^river-installer$')"
echo "RV_BROWSER=$(pgrep -u runink -f 'firefox.*47660' | grep -c .)"
echo "RV_GETTY=$(sudo s6-rc -a list 2>/dev/null | grep -cx tty1)"
echo "RV_DESKSESS=$([ -f /usr/share/wayland-sessions/desk.desktop ] && command -v desk-session >/dev/null && echo 1 || echo 0)"

# --- connectivity ------------------------------------------------------------------
# WIRED is real: QEMU gives the guest an emulated NIC, so this exercises a driver
# binding and NetworkManager actually managing it.
echo "RV_ETH=$(ls /sys/class/net 2>/dev/null | grep -vc '^lo$')"
echo "RV_NMSTATE=$(nmcli -t -f STATE general 2>/dev/null | head -1)"
echo "RV_ETHMANAGED=$(nmcli -t -f DEVICE,TYPE,STATE device 2>/dev/null | grep -c ':ethernet:connected')"

# WIFI and BLUETOOTH are SIMULATED. There is no wifi or Bluetooth hardware in QEMU, so
# these load the kernel's own simulators and test that the STACK is present and wired
# up: drivers, rfkill, NetworkManager's wifi plugin, bluetoothd's controller handling.
# They CANNOT say a particular card is recognised — that needs the real machine.
sudo modprobe mac80211_hwsim radios=1 2>/dev/null
sleep 3
echo "RV_WIFIDEV=$(ls /sys/class/net 2>/dev/null | grep -c '^wlan')"
echo "RV_WIFINM=$(nmcli -t -f DEVICE,TYPE device 2>/dev/null | grep -c ':wifi')"
echo "RV_WIFIBLOCK=$(rfkill list wifi 2>/dev/null | grep -c 'Soft blocked: yes')"
sudo modprobe bluetooth 2>/dev/null
sudo modprobe hci_vhci 2>/dev/null
sleep 3
# /dev/vhci appearing proves the Bluetooth core + hci_vhci driver loaded. It does NOT
# create a controller: that needs btvirt, which the image deliberately does not ship.
echo "RV_BTVHCI=$([ -e /dev/vhci ] && echo 1 || echo 0)"
echo "RV_BTSUBSYS=$([ -d /sys/class/bluetooth ] && echo 1 || echo 0)"
echo "RV_BTUSB=$(find /usr/lib/modules -name 'btusb.ko*' 2>/dev/null | head -1 | grep -c .)"
echo "RV_BTD=$(ps -eo comm | grep -c '^bluetoothd$')"
echo "RV_BTBLOCK=$(rfkill list bluetooth 2>/dev/null | grep -c 'Soft blocked: yes')"
echo RV_DONE
`

var ansiRe = regexp.MustCompile("\x1b\\[[0-9;?]*[a-zA-Z]")

// probeKeys are the values probeScript prints, in the order they are shown.
var probeKeys = []string{"DMUP", "PIDA", "PIDB", "KIOSKA", "KIOSKB", "GREETER", "INST", "BROWSER", "GETTY",
	"DESKSESS", "ETH", "ETHMANAGED", "WIFIDEV", "WIFINM", "WIFIBLOCK", "BTVHCI", "BTSUBSYS", "BTUSB", "BTD", "BTBLOCK"}

// MediumUnaided boots iso and reports whether it reaches the graphical installer unaided. The
// screendump medium-screen judges is kept at screenOut.
func MediumUnaided(ctx context.Context, iso, ovmfCode, ovmfVars, screenOut string, outw, errw io.Writer) error {
	for _, f := range []string{iso, ovmfCode, ovmfVars} {
		if _, err := os.Stat(f); err != nil {
			return fmt.Errorf("medium-unaided: %w", err)
		}
	}
	dir, err := os.MkdirTemp("", "river-medium-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)

	vars := filepath.Join(dir, "vars.fd")
	if err := copyFile(ovmfVars, vars, 0o600); err != nil {
		return err
	}
	scratch := filepath.Join(dir, "scratch.qcow2")
	if out, err := exec.CommandContext(ctx, "qemu-img", "create", "-f", "qcow2", scratch, "128G").CombinedOutput(); err != nil {
		return fmt.Errorf("medium-unaided: qemu-img: %v: %s", err, out)
	}
	sock := filepath.Join(dir, "ser.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		return err
	}
	defer ln.Close()

	logPath := filepath.Join(dir, "serial.log")
	monSock := filepath.Join(dir, "mon.sock")
	qemu := exec.CommandContext(ctx, "qemu-system-x86_64",
		"-enable-kvm", "-cpu", "host", "-m", "8G", "-smp", "4", "-machine", "q35",
		"-drive", "if=pflash,format=raw,readonly=on,file="+ovmfCode,
		"-drive", "if=pflash,format=raw,file="+vars,
		"-drive", "file="+scratch+",if=virtio,format=qcow2",
		"-cdrom", iso,
		// Two NICs on purpose: virtio-net (the paravirtual path) and an emulated e1000
		// (a real PCI device with a real in-tree driver), so medium-net-iface and
		// medium-net-managed exercise an actual driver bind, not just virtio.
		"-netdev", "user,id=n0", "-device", "virtio-net-pci,netdev=n0",
		"-netdev", "user,id=n1", "-device", "e1000,netdev=n1",
		"-device", "virtio-vga", "-display", "none",
		"-chardev", "socket,id=s0,path="+sock+",logfile="+logPath,
		"-monitor", "unix:"+monSock+",server=on,wait=off",
		"-serial", "chardev:s0")
	if err := qemu.Start(); err != nil {
		return fmt.Errorf("medium-unaided: qemu: %w", err)
	}
	defer func() { _ = qemu.Process.Kill(); _ = qemu.Wait() }()

	conn, err := ln.Accept()
	if err != nil {
		return fmt.Errorf("medium-unaided: serial: %w", err)
	}
	defer conn.Close()

	// Drain the serial socket CONTINUOUSLY from here on. If nothing reads it, the socket
	// buffer fills, QEMU blocks writing to it, and the guest's console stalls -- the boot
	// then looks like it never reached a prompt. (It does not: the first Go port of this
	// waited on the logfile without draining and reported "no prompt within 6m" on an image
	// that boots fine.)
	ser := &serialTap{}
	go ser.drain(conn)

	// Wait for ANY prompt. A console shell IS the failure mode, so waiting only for a
	// success marker would hang a broken medium until the deadline and report nothing.
	fmt.Fprintln(outw, "medium-unaided: booting", filepath.Base(iso))
	if !ser.waitForPrompt(ctx, 6*time.Minute) {
		return fmt.Errorf("medium-unaided: no prompt within 6m; the medium did not reach a usable state")
	}
	time.Sleep(45 * time.Second)

	enc := base64.StdEncoding.EncodeToString([]byte(probeScript))
	if _, err := fmt.Fprintf(conn, "echo %s | base64 -d > /tmp/rvprobe.sh; sh /tmp/rvprobe.sh\n", enc); err != nil {
		return fmt.Errorf("medium-unaided: writing probe: %w", err)
	}
	vals, err := ser.readProbe(ctx, 4*time.Minute)
	if err != nil {
		return fmt.Errorf("medium-unaided: %w", err)
	}

	shot := filepath.Join(dir, "screen.png")
	screenErr := screendump(monSock, shot)
	if screenErr == nil {
		_ = os.Remove(screenOut) // copyFile never overwrites
		if screenErr = copyFile(shot, screenOut, 0o644); screenErr == nil {
			_, screenErr = ScreenCheck(screenOut, screenMinBytes, screenMinBits)
		}
	}

	fmt.Fprintf(outw, "--- medium-unaided: %s ---\n", filepath.Base(iso))
	for _, key := range probeKeys {
		fmt.Fprintf(outw, "  RV_%s=%s\n", key, vals[key])
	}
	return report(vals, screenErr, outw, errw)
}

// screendump asks QEMU's monitor for a PNG of the screen and waits until it is written.
func screendump(sock, png string) error {
	c, err := net.Dial("unix", sock)
	if err != nil {
		return fmt.Errorf("monitor: %w", err)
	}
	defer c.Close()
	go func() { _, _ = io.Copy(io.Discard, c) }()
	if _, err := fmt.Fprintf(c, "screendump %s -f png\n", png); err != nil {
		return fmt.Errorf("monitor: %w", err)
	}
	for i := 0; i < 20; i++ {
		time.Sleep(time.Second)
		if st, err := os.Stat(png); err == nil && st.Size() > 0 {
			time.Sleep(time.Second) // the last write
			return nil
		}
	}
	return fmt.Errorf("QEMU wrote no screendump to %s", png)
}

// report turns the probe's values and the screen check into RIVERTEST lines. An unparsed value
// is a broken probe, not a verdict, and says so rather than counting as either outcome.
func report(vals map[string]string, screenErr error, outw, errw io.Writer) error {
	failed := 0
	say := func(name string, ok bool, why string) {
		if ok {
			fmt.Fprintf(outw, "RIVERTEST OK   %s\n", name)
			return
		}
		fmt.Fprintf(errw, "RIVERTEST FAIL %s   %s\n", name, why)
		failed++
	}
	num := func(k string) (int, bool) {
		n, err := strconv.Atoi(strings.TrimSpace(vals[k]))
		return n, err == nil
	}
	for _, k := range probeKeys {
		if _, ok := num(k); !ok {
			say("medium-probe", false, fmt.Sprintf("value RV_%s did not parse (broken harness, not a verdict)", k))
		}
	}
	if failed > 0 {
		return fmt.Errorf("medium-unaided: the probe did not return usable values")
	}
	dmup, _ := num("DMUP")
	pidA, _ := num("PIDA")
	pidB, _ := num("PIDB")
	kioskA, _ := num("KIOSKA")
	kioskB, _ := num("KIOSKB")
	greeter, _ := num("GREETER")
	inst, _ := num("INST")
	browser, _ := num("BROWSER")
	getty, _ := num("GETTY")
	deskSess, _ := num("DESKSESS")

	// A getty on VT 1 is reported, NOT failed: it asserts a cause, not the outcome. Under SDDM
	// (2026-10-18) a tty1 getty starved the GREETER but was harmless under autologin, and the
	// live medium autologins (greetd's initial_session). A starved session shows up below as an
	// unstable supervisor PID or no sustained kiosk.
	if getty != 0 {
		fmt.Fprintln(outw, "RIVERTEST NOTE medium-vt-free   a tty1 getty is active; harmless under "+
			"autologin, fatal if a greeter runs — see medium-dm-stable and medium-kiosk")
	}
	say("medium-dm-started", dmup == 1, "greetd is not running after 3m: the display manager never started")
	// s6-svstat -p prints -1 when the service is down; equal-and-negative is not stable.
	say("medium-dm-stable", pidA == pidB && pidA > 0,
		fmt.Sprintf("greetd pid %d -> %d across 12s: not a stable supervisor", pidA, pidB))
	// Two samples: a respawning compositor is in ps often enough for one sample to catch it.
	say("medium-kiosk", kioskA >= 1 && kioskB >= 1,
		fmt.Sprintf("no sustained live session: weston as the live user in %d, %d of two samples", kioskA, kioskB))
	say("medium-autologin", greeter == 0, "a login screen (gtkgreet) is waiting: greetd's initial_session did not take effect")
	say("medium-installer", inst >= 1 && browser >= 1,
		fmt.Sprintf("the installer is not up: river-installer %d, its page in the browser %d", inst, browser))
	say("medium-desk-session", deskSess == 1, "desk-session or wayland-sessions/desk.desktop is missing: nothing to log in to after the install")
	why := "ok"
	if screenErr != nil {
		why = screenErr.Error()
	}
	say("medium-screen", screenErr == nil, why)

	// --- connectivity --------------------------------------------------------------------
	//
	// WIRED is a real test: QEMU gives the guest an emulated NIC, so a driver really binds
	// and NetworkManager really manages it.
	eth, _ := num("ETH")
	ethManaged, _ := num("ETHMANAGED")
	say("medium-net-iface", eth >= 1, "no network interface besides lo")
	say("medium-net-managed", ethManaged >= 1,
		"NetworkManager has no connected ethernet device (nmcli device shows none :ethernet:connected)")

	// WIFI and BLUETOOTH are SIMULATED — mac80211_hwsim and hci_vhci, the kernel's own
	// simulators. There is no such hardware in QEMU. These prove the STACK is present and
	// wired up: the drivers are in the image, rfkill is not soft-blocking, NetworkManager's
	// wifi plugin enumerates a wlan, and bluetoothd picks up a controller.
	//
	// They CANNOT say a particular card is recognised. That needs the real machine, and the
	// owner's "does not recognize any network card" report is NOT answered by a green run
	// here. Firmware presence was separately ruled out: both the 2026-10-18 ISOs carry the
	// full linux-firmware (rtw88, rtw89, brcm, ath10k/11k, mediatek, qca), so that was never
	// the gap.
	wifiDev, _ := num("WIFIDEV")
	wifiNM, _ := num("WIFINM")
	wifiBlock, _ := num("WIFIBLOCK")
	say("medium-wifi-stack", wifiDev >= 1, "mac80211_hwsim loaded but no wlan interface appeared: the wifi driver stack is not in the image")
	say("medium-wifi-nm", wifiNM >= 1, "NetworkManager does not enumerate the simulated wlan: its wifi plugin is missing or disabled")
	say("medium-wifi-unblocked", wifiBlock == 0, "wifi is SOFT-BLOCKED by rfkill on a fresh boot: runink-fw should clear it")

	btVhci, _ := num("BTVHCI")
	btSubsys, _ := num("BTSUBSYS")
	btUsb, _ := num("BTUSB")
	btd, _ := num("BTD")
	btBlock, _ := num("BTBLOCK")
	// Asserting an instantiated hci controller was WRONG and failed a good image: hci_vhci
	// creates /dev/vhci, it does not create a controller. That needs btvirt, which the image
	// deliberately does not ship (it is a bluez test tool). What IS provable in a VM: the
	// Bluetooth core and the vhci driver load, the subsystem exists, the USB adapter driver
	// that real hardware needs is present, bluetoothd runs, and rfkill is not blocking.
	say("medium-bt-stack", btVhci == 1 && btSubsys == 1,
		"the Bluetooth core/hci_vhci drivers did not load (/dev/vhci or /sys/class/bluetooth absent)")
	say("medium-bt-driver", btUsb >= 1, "btusb.ko is not in the image: no USB Bluetooth adapter could bind")
	say("medium-bt-daemon", btd >= 1, "bluetoothd is not running")
	say("medium-bt-unblocked", btBlock == 0, "Bluetooth is SOFT-BLOCKED by rfkill on a fresh boot: runink-fw should clear it")

	fmt.Fprintln(outw, "RIVERTEST NOTE connectivity      wired is real (emulated NIC); wifi and Bluetooth are "+
		"SIMULATED (mac80211_hwsim, hci_vhci) and prove the stack only, never that a given card is recognised")

	if failed > 0 {
		return fmt.Errorf("medium-unaided: %d check(s) failed: the medium does not reach the graphical installer unaided", failed)
	}
	fmt.Fprintln(outw, "medium-unaided: the medium reaches the graphical installer unaided")
	return nil
}

// serialTap accumulates everything the guest writes to its serial console, so the console
// never blocks on a full socket buffer and so both the prompt wait and the probe read work
// off the same stream.
type serialTap struct {
	mu  sync.Mutex
	buf []byte
}

func (t *serialTap) drain(r io.Reader) {
	b := make([]byte, 8192)
	for {
		n, err := r.Read(b)
		if n > 0 {
			t.mu.Lock()
			t.buf = append(t.buf, b[:n]...)
			t.mu.Unlock()
		}
		if err != nil {
			return
		}
	}
}

func (t *serialTap) text() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return ansiRe.ReplaceAllString(string(t.buf), "")
}

func (t *serialTap) waitForPrompt(ctx context.Context, within time.Duration) bool {
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if ctx.Err() != nil {
			return false
		}
		s := t.text()
		// A console shell IS the failure mode, so we must not wait only for a success
		// marker: a broken medium would then hang here and report nothing useful.
		if strings.Contains(s, "login:") || strings.Contains(s, "$ ") || strings.Contains(s, "# ") {
			return true
		}
		time.Sleep(5 * time.Second)
	}
	return false
}

// readProbe waits for RV_DONE and returns the KEY=VALUE pairs. Returning without RV_DONE is an
// error, never a partial verdict.
func (t *serialTap) readProbe(ctx context.Context, within time.Duration) (map[string]string, error) {
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if ctx.Err() != nil {
			break
		}
		s := t.text()
		if strings.Contains(s, "RV_DONE") {
			vals := map[string]string{}
			for _, line := range strings.Split(s, "\n") {
				line = strings.TrimSpace(line)
				if k, v, ok := strings.Cut(line, "="); ok && strings.HasPrefix(k, "RV_") {
					vals[strings.TrimPrefix(k, "RV_")] = strings.TrimSpace(v)
				}
			}
			return vals, nil
		}
		time.Sleep(5 * time.Second)
	}
	return nil, fmt.Errorf("the probe did not complete (no RV_DONE); no verdict")
}
