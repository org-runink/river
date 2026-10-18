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
// RIG. -cpu host, and virtio-vga so /dev/dri/renderD128 exists. With QEMU's default bochs VGA
// there is no render node, kwin_wayland quits with "Found no render device!", and EVERY image
// fails for a reason that belongs to the rig. A disk is attached because the installer plan
// wants one; nothing is written to it.

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
system six questions:

  medium-vt-free    no getty holds the VT the display manager wants
  medium-dm-started the display manager got far enough to write a log at all
  medium-dm-tty     zero HELPER_TTY_ERROR in it
  medium-dm-stable  the supervisor PID is unchanged AND positive across 12s
  medium-desktop    a Plasma session is present in BOTH of two samples
  medium-installer  river-installer is running

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
				v.GetString("ovmf-code"), v.GetString("ovmf-vars"),
				cmd.OutOrStdout(), cmd.ErrOrStderr())
		},
	}
	c.Flags().String("ovmf-code", ovmfCodeDefault, "OVMF_CODE firmware image")
	c.Flags().String("ovmf-vars", ovmfVarsDefault, "OVMF_VARS template (copied, never written in place)")
	return c
}

// probeScript runs inside the booted ISO. One KEY=VALUE per line, RV_DONE last, so a
// truncated transfer can never be mistaken for a verdict.
//
// It settles before sampling. A fixed delay is not enough: sampled too early the log does not
// exist yet (which `grep -c` reports as nothing, indistinguishable from zero errors) and a
// respawning kwin_wayland is briefly in `ps`, so an early probe PASSES a crash-looping image.
// Observed doing exactly that on the 2026-10-18 ISO: one run OK, the next FAIL, same image.
const probeScript = `#!/bin/sh
n=0
while [ "$n" -lt 36 ]; do
	[ -e /var/log/sddm.log ] && break
	n=$((n + 1)); sleep 5
done
if [ -e /var/log/sddm.log ]; then L=1; else L=0; fi
sleep 30
T=$(sudo grep -c HELPER_TTY_ERROR /var/log/sddm.log 2>/dev/null); [ -n "$T" ] || T=0
A=$(sudo s6-svstat -p /run/service/sddm-srv 2>/dev/null)
PA=$(ps -eo comm | grep -cE '^(plasma_session|kwin_wayland)$')
sleep 12
B=$(sudo s6-svstat -p /run/service/sddm-srv 2>/dev/null)
PB=$(ps -eo comm | grep -cE '^(plasma_session|kwin_wayland)$')
echo "RV_DMLOG=${L:-}"
echo "RV_TTYERR=${T:-}"
echo "RV_PIDA=${A:-}"
echo "RV_PIDB=${B:-}"
echo "RV_PLASMAA=${PA:-}"
echo "RV_PLASMAB=${PB:-}"
echo "RV_INST=$(ps -eo comm | grep -c '^river-installer$')"
echo "RV_GETTY=$(sudo s6-rc -a list 2>/dev/null | grep -cx tty1)"
echo RV_DONE
`

var ansiRe = regexp.MustCompile("\x1b\\[[0-9;?]*[a-zA-Z]")

// MediumUnaided boots iso and reports whether it reaches the graphical installer unaided.
func MediumUnaided(ctx context.Context, iso, ovmfCode, ovmfVars string, outw, errw io.Writer) error {
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
	qemu := exec.CommandContext(ctx, "qemu-system-x86_64",
		"-enable-kvm", "-cpu", "host", "-m", "8G", "-smp", "4", "-machine", "q35",
		"-drive", "if=pflash,format=raw,readonly=on,file="+ovmfCode,
		"-drive", "if=pflash,format=raw,file="+vars,
		"-drive", "file="+scratch+",if=virtio,format=qcow2",
		"-cdrom", iso,
		"-netdev", "user,id=n0", "-device", "virtio-net-pci,netdev=n0",
		"-device", "virtio-vga", "-display", "none",
		"-chardev", "socket,id=s0,path="+sock+",logfile="+logPath,
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

	fmt.Fprintf(outw, "--- medium-unaided: %s ---\n", filepath.Base(iso))
	for _, key := range []string{"DMLOG", "TTYERR", "PIDA", "PIDB", "PLASMAA", "PLASMAB", "INST", "GETTY"} {
		fmt.Fprintf(outw, "  RV_%s=%s\n", key, vals[key])
	}
	return report(vals, outw, errw)
}

// report turns the probe's values into RIVERTEST lines. An unparsed value is a broken probe,
// not a verdict, and says so rather than counting as either outcome.
func report(vals map[string]string, outw, errw io.Writer) error {
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
	for _, k := range []string{"DMLOG", "TTYERR", "PIDA", "PIDB", "PLASMAA", "PLASMAB", "INST", "GETTY"} {
		if _, ok := num(k); !ok {
			say("medium-probe", false, fmt.Sprintf("value RV_%s did not parse (broken harness, not a verdict)", k))
		}
	}
	if failed > 0 {
		return fmt.Errorf("medium-unaided: the probe did not return usable values")
	}
	dmlog, _ := num("DMLOG")
	ttyerr, _ := num("TTYERR")
	pidA, _ := num("PIDA")
	pidB, _ := num("PIDB")
	plasmaA, _ := num("PLASMAA")
	plasmaB, _ := num("PLASMAB")
	inst, _ := num("INST")
	getty, _ := num("GETTY")

	say("medium-vt-free", getty == 0, "a tty1 getty is active; it starves the display manager's greeter")
	// "No log" is not "no errors": it means sddm never got far enough to write one.
	say("medium-dm-started", dmlog == 1, "no /var/log/sddm.log after 3m: the display manager never started")
	say("medium-dm-tty", ttyerr == 0, fmt.Sprintf("%d HELPER_TTY_ERROR in sddm.log: the greeter is losing the TTY", ttyerr))
	// s6-svstat -p prints -1 when the service is down; equal-and-negative is not stable.
	say("medium-dm-stable", pidA == pidB && pidA > 0,
		fmt.Sprintf("sddm-srv pid %d -> %d across 12s: not a stable supervisor", pidA, pidB))
	// Two samples: a respawning kwin_wayland is in ps often enough for one sample to catch it.
	say("medium-desktop", plasmaA >= 1 && plasmaB >= 1,
		fmt.Sprintf("no sustained Plasma session (samples: %d, %d)", plasmaA, plasmaB))
	say("medium-installer", inst >= 1, "river-installer is not running")

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
