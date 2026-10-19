// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

// Tests for river-netsetup's messages when the machine has no usable network device.
//
// The script is shell, driven by a `case` at the bottom with no sourceable guard, so these run
// the WHOLE script against a fake NetworkManager and scripted answers on stdin. That is slower
// than calling one function and exactly as real: it is the path a person takes.
//
// Why they exist. On 2026-10-19, on real hardware, the live installer said
// "river-netsetup: no wired interface" and then, for Wi-Fi, offered a menu containing one line,
// "Another (hidden) network". Nothing told the person whether the machine had no Wi-Fi adapter
// or merely no network in range, or what NetworkManager could see at all. Those are different
// problems with different fixes, and a photograph of that screen could not tell them apart.
package netsetup

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const fakeNmcli = `#!/bin/sh
# fake NetworkManager. FAKE_DEVICES is "device:type:state;..." and FAKE_WIFI the scan output.
case "$*" in
  *"-f RUNNING general"*) echo running ;;
  *"-f STATE general"*) echo disconnected ;;
  *"device status"*) printf '%s\n' $(echo "$FAKE_DEVICES" | tr ';' ' ') ;;
  *"device wifi list"*) [ -n "$FAKE_WIFI" ] && printf '%s\n' "$FAKE_WIFI" ;;
esac
exit 0
`

// run drives the interactive flow and returns everything it printed. answers are the lines a
// person would type; the script is given a terminal that is not one (no dialog), so it uses
// plain prompts.
func run(t *testing.T, devices, wifi, answers string) string {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write("nmcli", fakeNmcli)
	write("river-netcheck", "#!/bin/sh\necho 'RESULT: offline'\n")
	// is_root asks `id -u`; the test is not root and must not need to be.
	write("id", "#!/bin/sh\n[ \"$1\" = -u ] && echo 0 || exec /usr/bin/id \"$@\"\n")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "sh", "river-netsetup", "--wait", "0")
	cmd.Stdin = strings.NewReader(answers)
	cmd.Env = []string{
		"PATH=" + bin + ":/usr/bin:/bin",
		"RIVER_NETCHECK=" + filepath.Join(bin, "river-netcheck"),
		"RIVER_NET_STATE_DIR=" + filepath.Join(dir, "state"),
		"TMPDIR=" + dir,
		"TERM=dumb",
		"FAKE_DEVICES=" + devices,
		"FAKE_WIFI=" + wifi,
	}
	out, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("river-netsetup did not finish within 30s (a prompt loop on EOF?); output so far:\n%s", out)
	}
	if err != nil {
		t.Logf("river-netsetup exited non-zero (%v); output follows", err)
	}
	return string(out)
}

// With no Wi-Fi device at all, say so — and say what NetworkManager does see — instead of
// offering a scan menu that can only ever be empty.
func TestNoWifiAdapterSaysSo(t *testing.T) {
	out := run(t, "lo:loopback:unmanaged;eth9:ethernet:disconnected", "", "wifi\noff\ncontinue\n")
	for _, want := range []string{
		"No Wi-Fi adapter was found",
		"NetworkManager sees: eth9 (ethernet)",
		"its driver did not load",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	// The failure is that it went on to show a scan menu. After "No Wi-Fi adapter" the wifi
	// menu must not appear; the main menu does (that is where choose_wifi's failure returns to).
	if strings.Contains(out, "Networks in range") {
		t.Errorf("a scan menu was offered although there is no adapter to scan with:\n%s", out)
	}
}

// With no network devices of any kind, the message must still be honest about it.
func TestNothingAtAllIsStatedAsNothing(t *testing.T) {
	out := run(t, "lo:loopback:unmanaged", "", "wifi\noff\ncontinue\n")
	if !strings.Contains(out, "NetworkManager sees: no network devices at all") {
		t.Errorf("an empty device list was not reported as such:\n%s", out)
	}
}

// An adapter that exists but sees nothing is a DIFFERENT problem and gets a different message,
// and the hidden-network entry stays available.
func TestAdapterWithNoNetworksIsNotNoAdapter(t *testing.T) {
	out := run(t, "lo:loopback:unmanaged;wlan0:wifi:disconnected", "", "wifi\n\noff\ncontinue\n")
	if !strings.Contains(out, "Wi-Fi adapter wlan0 is present, but it sees no networks") {
		t.Errorf("missing the adapter-present/no-networks message:\n%s", out)
	}
	if strings.Contains(out, "No Wi-Fi adapter was found") {
		t.Errorf("claimed there is no adapter when wlan0 exists:\n%s", out)
	}
	if !strings.Contains(out, "Another (hidden) network") {
		t.Errorf("the hidden-network entry disappeared:\n%s", out)
	}
}

// A normal machine must see neither warning.
func TestHealthyWifiIsQuiet(t *testing.T) {
	out := run(t, "lo:loopback:unmanaged;wlan0:wifi:disconnected", "HomeNet:80:WPA2", "wifi\n\noff\ncontinue\n")
	for _, bad := range []string{"No Wi-Fi adapter was found", "sees no networks"} {
		if strings.Contains(out, bad) {
			t.Errorf("a healthy Wi-Fi setup printed %q:\n%s", bad, out)
		}
	}
	if !strings.Contains(out, "HomeNet") {
		t.Errorf("the network in range was not listed:\n%s", out)
	}
}

// The wired failure keeps its original wording (anything grepping for it still works) and now
// says what NetworkManager sees.
func TestWiredFailureNamesWhatItSees(t *testing.T) {
	out := run(t, "lo:loopback:unmanaged;wlan0:wifi:disconnected", "", "auto\noff\ncontinue\n")
	if !strings.Contains(out, "river-netsetup: no wired interface; NetworkManager sees: wlan0 (wifi)") {
		t.Errorf("wired failure message wrong:\n%s", out)
	}
}
