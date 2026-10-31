// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

package testcmd

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

// goodMedium is what probeScript prints on a medium that reached the installer: greetd stable,
// weston as the live user in both samples, no greeter, the installer and its page up.
func goodMedium() map[string]string {
	v := map[string]string{}
	for _, k := range probeKeys {
		v[k] = "0"
	}
	for k, n := range map[string]string{"DMUP": "1", "PIDA": "612", "PIDB": "612", "KIOSKA": "1", "KIOSKB": "1",
		"INST": "1", "BROWSER": "3", "DESKSESS": "1", "ETH": "2", "ETHMANAGED": "1", "WIFIDEV": "1", "WIFINM": "1",
		"BTVHCI": "1", "BTSUBSYS": "1", "BTUSB": "1", "BTD": "1"} {
		v[k] = n
	}
	return v
}

func TestMediumReportPassesAWorkingMedium(t *testing.T) {
	var out, errw bytes.Buffer
	if err := report(goodMedium(), nil, &out, &errw); err != nil {
		t.Fatalf("%v\n%s", err, errw.String())
	}
	for _, c := range []string{"medium-dm-stable", "medium-kiosk", "medium-autologin", "medium-installer", "medium-screen"} {
		if !strings.Contains(out.String(), "RIVERTEST OK   "+c) {
			t.Errorf("no OK for %s:\n%s", c, out.String())
		}
	}
}

func TestMediumReportFailsEachFault(t *testing.T) {
	for _, tc := range []struct {
		check  string
		change map[string]string
		screen error
	}{
		{"medium-dm-started", map[string]string{"DMUP": "0"}, nil},
		{"medium-dm-stable", map[string]string{"PIDB": "640"}, nil},
		{"medium-dm-stable", map[string]string{"PIDA": "-1", "PIDB": "-1"}, nil},
		{"medium-kiosk", map[string]string{"KIOSKB": "0"}, nil},
		{"medium-autologin", map[string]string{"GREETER": "1"}, nil},
		{"medium-installer", map[string]string{"BROWSER": "0"}, nil},
		{"medium-desk-session", map[string]string{"DESKSESS": "0"}, nil},
		{"medium-screen", nil, errors.New("screen.png: nothing drawn: [752 bytes < 16384]")},
		{"medium-probe", map[string]string{"KIOSKA": ""}, nil},
	} {
		v := goodMedium()
		for k, n := range tc.change {
			v[k] = n
		}
		var out, errw bytes.Buffer
		if err := report(v, tc.screen, &out, &errw); err == nil {
			t.Errorf("%s: a faulty medium passed (%v)", tc.check, tc.change)
		}
		if !strings.Contains(errw.String(), "RIVERTEST FAIL "+tc.check) {
			t.Errorf("%s: not reported:\n%s", tc.check, errw.String())
		}
	}
}
