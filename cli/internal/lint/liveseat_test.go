// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

package lint

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	lsGoodToml = "[terminal]\nvt = 1\n\n[initial_session]\ncommand = \"env LIBSEAT_BACKEND=seatd weston --continue-without-input\"\nuser = \"runink\"\n\n[default_session]\ncommand = \"cage -s -- gtkgreet\"\nuser = \"greeter\"\n"
	lsGoodRun  = "#!/bin/execlineb -P\n# a comment\nfdmove -c 2 1\n/usr/bin/seatd -g video\n"
	// lsGoodPKG is the shape of the greetd s6 package's seatd service, as far as the lint reads it.
	lsGoodPKG = "package() {\n\tlocal ss=\"$pkgdir/etc/s6/sv/seatd\"\n\tinstall -d \"$ss\"\n\tprintf 'longrun\\n' > \"$ss/type\"\n\tcat > \"$ss/run\" <<'RUN'\n#!/bin/execlineb -P\nfdmove -c 2 1\n/usr/bin/seatd -g video\nRUN\n}\n"
)

// lsProfile writes a live overlay with the greetd wiring (the seat service itself is defined by
// the package or by an overlay, chosen by the caller), then lets the caller break one piece.
func lsProfile(t *testing.T, mutate func(dir string)) string {
	t.Helper()
	dir := t.TempDir()
	lbWrite(t, dir, "live-overlay/etc/greetd/live.toml", lsGoodToml)
	lbWrite(t, dir, "live-overlay/etc/s6/sv/greetd/dependencies.d/seatd", "")
	if mutate != nil {
		mutate(dir)
	}
	return dir
}

func lsOverlayService(t *testing.T, d string) {
	t.Helper()
	lbWrite(t, d, "live-overlay/etc/s6/sv/seatd/type", "longrun\n")
	lbWrite(t, d, "live-overlay/etc/s6/sv/seatd/run", lsGoodRun)
}

func TestInitialSessionCommand(t *testing.T) {
	if got := pmInitialSessionCommand(lsGoodToml); !strings.Contains(got, "weston") || strings.Contains(got, "cage") {
		t.Fatalf("read the wrong section's command: %q", got)
	}
	if got := pmInitialSessionCommand("[default_session]\ncommand = \"cage\"\n"); got != "" {
		t.Fatalf("a default session is not a first session: %q", got)
	}
}

// The real image's wiring passes (its seatd service comes from the greetd s6 package), the same
// wiring defined by a profile's own overlay passes too, and removing any one piece fails with
// that piece's reason. The first case reads the shipped files, so the check cannot pass on a
// fixture alone.
func TestLiveSeatWiring(t *testing.T) {
	repo, _ := filepath.Abs("../../..")
	prof := filepath.Join(repo, "iso-profiles/river")
	yb, err := os.ReadFile(filepath.Join(prof, "profile.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	pkg, err := os.ReadFile(filepath.Join(repo, "build/pkgbuilds/runink-greetd-s6/PKGBUILD"))
	if err != nil {
		t.Fatal(err)
	}
	var services []string
	if strings.Contains(string(yb), "\n    - seatd\n") {
		services = append(services, "seatd")
	}
	if why := pmLiveSeat(prof, services, string(pkg)); len(why) != 0 {
		t.Fatalf("the image's live session is missing seat wiring: %v", why)
	}

	if why := pmLiveSeat(lsProfile(t, func(d string) { lsOverlayService(t, d) }), []string{"seatd"}, ""); len(why) != 0 {
		t.Fatalf("a service defined by the profile's own overlay failed: %v", why)
	}
	if why := pmLiveSeat(lsProfile(t, nil), []string{"seatd"}, lsGoodPKG); len(why) != 0 {
		t.Fatalf("a service defined by the package failed: %v", why)
	}

	cases := []struct {
		name     string
		mutate   func(dir string)
		services []string
		pkg      string
		want     string
	}{
		{"no backend variable", func(d string) {
			lbWrite(t, d, "live-overlay/etc/greetd/live.toml", strings.Replace(lsGoodToml, "env LIBSEAT_BACKEND=seatd ", "", 1))
		}, []string{"seatd"}, lsGoodPKG, "LIBSEAT_BACKEND=seatd"},
		{"no service anywhere", nil, []string{"seatd"}, "", "no seatd service is defined"},
		{"package service without a group", nil, []string{"seatd"},
			strings.Replace(lsGoodPKG, "seatd -g video", "seatd", 1), "no seatd service is defined"},
		{"package service not a longrun", nil, []string{"seatd"},
			strings.Replace(lsGoodPKG, "longrun", "oneshot", 1), "no seatd service is defined"},
		{"overlay run without a group", func(d string) {
			lbWrite(t, d, "live-overlay/etc/s6/sv/seatd/type", "longrun\n")
			lbWrite(t, d, "live-overlay/etc/s6/sv/seatd/run", "#!/bin/execlineb -P\n/usr/bin/seatd\n")
		}, []string{"seatd"}, "", "no seatd service is defined"},
		{"overlay run is not seatd", func(d string) {
			lbWrite(t, d, "live-overlay/etc/s6/sv/seatd/type", "longrun\n")
			lbWrite(t, d, "live-overlay/etc/s6/sv/seatd/run", "#!/bin/execlineb -P\n/usr/bin/true -g video\n")
		}, []string{"seatd"}, "", "no seatd service is defined"},
		{"greetd does not wait for it", func(d string) {
			_ = os.Remove(filepath.Join(d, "live-overlay/etc/s6/sv/greetd/dependencies.d/seatd"))
		}, []string{"seatd"}, lsGoodPKG, "dependencies.d/seatd is missing"},
		{"not enabled", nil, []string{"greetd"}, lsGoodPKG, "does not enable seatd"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			why := strings.Join(pmLiveSeat(lsProfile(t, tc.mutate), tc.services, tc.pkg), "; ")
			if !strings.Contains(why, tc.want) {
				t.Fatalf("want a failure mentioning %q, got %q", tc.want, why)
			}
		})
	}
}

// A profile with no live greetd session, or a live session that opens no compositor, has nothing to seat.
func TestLiveSeatNotApplicable(t *testing.T) {
	if why := pmLiveSeat(t.TempDir(), nil, ""); len(why) != 0 {
		t.Fatalf("a profile with no live greetd config failed: %v", why)
	}
	dir := t.TempDir()
	lbWrite(t, dir, "live-overlay/etc/greetd/live.toml", "[default_session]\ncommand = \"cage\"\n")
	if why := pmLiveSeat(dir, nil, ""); len(why) != 0 {
		t.Fatalf("a live session with no first session failed: %v", why)
	}
}
