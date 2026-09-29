// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

package lint

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const isDriver = `#!/bin/sh
# runink-install: river-netsetup runs first (this comment line does not count)
river-netsetup || exit 1
pair_menu "$@"
hwplan_init
`

const isOverlay = "iso-profiles/river/root-overlay/usr/local/lib/runink-install"

func installerFixture(t *testing.T) string {
	t.Helper()
	dir := fixtureRepo(t)
	writeTree(t, dir, map[string]string{
		"installer/lib/10-disk.sh":                                     "#!/bin/sh\necho disk\n",
		"installer/lib/20-clone.sh":                                    "#!/bin/sh\necho clone\n",
		"installer/lib/05-legacy.sh":                                   "#!/bin/sh\necho only in the mirror\n",
		isOverlay + "/10-disk.sh":                                      "#!/bin/sh\necho disk\n",
		isOverlay + "/20-clone.sh":                                     "#!/bin/sh\necho clone\n",
		isOverlay + "/90-export.sh":                                    "#!/bin/sh\necho only shipped\n",
		"iso-profiles/river/root-overlay/usr/local/bin/runink-install": isDriver,
		"iso-profiles/river/live-overlay/usr/local/bin/runink-install": isDriver,
		"installer/hwprobe/main.go":                                    "package main\n",
		"installer/plan/main.go":                                       "package main\n",
		"installer/fakemeta/main.go":                                   "package main\n",
		installerBin:                                                   "#!/bin/sh\nfor b in hwprobe:river-hwprobe \\\n\tplan:river-plan; do\n\tgo build ./$b\ndone\ncp installer/netsetup/river-netsetup out/\n",
		installerPkg:                                                   "package() {\n  for b in river-hwprobe river-plan river-netsetup; do\n    install $b\n  done\n}\n",
	})
	return dir
}

func runInstallerSync(t *testing.T, dir string, profiles ...string) (string, error) {
	t.Helper()
	var errb, outb bytes.Buffer
	err := InstallerSync(context.Background(), dir, profiles, &errb, &outb)
	return errb.String() + outb.String(), err
}

func TestInstallerSyncClean(t *testing.T) {
	out, err := runInstallerSync(t, installerFixture(t))
	want := "lint-installer-sync: 2 shared step(s) in sync across 1 profile overlay(s) ✓\n" +
		"lint-installer-sync: 1 profile driver pair(s) identical, network first, then the LAN-install menu ✓\n" +
		"lint-installer-sync: 2 installer command(s) + river-netsetup built and packaged ✓\n"
	if err != nil || out != want {
		t.Fatalf("clean tree: %v\n%s", err, out)
	}
}

func TestInstallerSyncFailures(t *testing.T) {
	root := "iso-profiles/river/root-overlay/usr/local/bin/runink-install"
	live := "iso-profiles/river/live-overlay/usr/local/bin/runink-install"
	cases := []struct {
		name   string
		break_ func(t *testing.T, dir string)
		want   string
	}{
		{"step drifted", func(t *testing.T, d string) {
			writeTree(t, d, map[string]string{isOverlay + "/10-disk.sh": "#!/bin/sh\necho disk fixed\n"})
		}, "DRIFT: installer/lib/10-disk.sh != " + isOverlay + "/10-disk.sh\n  the overlay copy is what SHIPS"},
		{"one driver", func(t *testing.T, d string) {
			if err := os.Remove(filepath.Join(d, live)); err != nil {
				t.Fatal(err)
			}
		}, "iso-profiles/river has only one runink-install (want root-overlay AND live-overlay)"},
		{"driver drifted", func(t *testing.T, d string) {
			writeTree(t, d, map[string]string{live: isDriver + "echo extra\n"})
		}, "DRIFT: " + root + " != " + live},
		{"plans before the network", func(t *testing.T, d string) {
			body := "#!/bin/sh\nhwplan_init\nriver-netsetup\npair_menu x\n"
			writeTree(t, d, map[string]string{root: body, live: body})
		}, root + " does not run river-netsetup (step 0) before hwplan_init"},
		{"no pairing menu", func(t *testing.T, d string) {
			body := strings.Replace(isDriver, "pair_menu \"$@\"\n", "", 1)
			writeTree(t, d, map[string]string{root: body, live: body})
		}, root + " does not offer the LAN-install menu (pair_menu) between river-netsetup and hwplan_init"},
		{"command not built", func(t *testing.T, d string) {
			writeTree(t, d, map[string]string{"installer/netcheck/main.go": "package main\n"})
		}, "installer/netcheck is a command but " + installerBin + " does not build it"},
		{"command not packaged", func(t *testing.T, d string) {
			editFixture(t, d, installerPkg, "river-plan ", "")
		}, installerPkg + " does not package river-plan (installer/plan)"},
		{"netsetup not staged", func(t *testing.T, d string) {
			editFixture(t, d, installerBin, "cp installer/netsetup/river-netsetup out/\n", "")
		}, installerBin + " does not stage river-netsetup"},
		{"netsetup not packaged", func(t *testing.T, d string) {
			editFixture(t, d, installerPkg, " river-netsetup", "")
		}, installerPkg + " does not package river-netsetup"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := installerFixture(t)
			c.break_(t, dir)
			out, err := runInstallerSync(t, dir)
			if err == nil {
				t.Fatalf("passed:\n%s", out)
			}
			if !strings.Contains(out, "lint-installer-sync: "+strings.SplitN(c.want, "\n", 2)[0]) || !strings.Contains(out, c.want) {
				t.Fatalf("want %q in:\n%s", c.want, out)
			}
		})
	}
}

// A staged downstream profile, passed as an argument (build/local-iso.sh does), is checked
// instead of the in-tree ones, and a drifted step in it fails.
func TestInstallerSyncExternalProfile(t *testing.T) {
	dir := installerFixture(t)
	ext := filepath.Join(t.TempDir(), "downstream")
	writeTree(t, ext, map[string]string{
		"root-overlay/usr/local/lib/runink-install/20-clone.sh": "#!/bin/sh\necho clone\n",
		"root-overlay/usr/local/bin/runink-install":             isDriver,
		"live-overlay/usr/local/bin/runink-install":             isDriver,
	})
	out, err := runInstallerSync(t, dir, ext)
	if err != nil || !strings.Contains(out, "1 shared step(s) in sync across 1 profile overlay(s)") {
		t.Fatalf("staged profile: %v\n%s", err, out)
	}
	writeTree(t, ext, map[string]string{"root-overlay/usr/local/lib/runink-install/20-clone.sh": "#!/bin/sh\necho drift\n"})
	out, err = runInstallerSync(t, dir, ext)
	if err == nil || !strings.Contains(out, "DRIFT: installer/lib/20-clone.sh != "+ext+"/root-overlay/usr/local/lib/runink-install/20-clone.sh") {
		t.Fatalf("a drifted staged step passed: %v\n%s", err, out)
	}
}

func TestInstallerSyncRefusesNothing(t *testing.T) {
	dir := installerFixture(t)
	if _, err := runInstallerSync(t, dir, filepath.Join(dir, "no-such-profile")); err == nil || !strings.Contains(err.Error(), "no such profile directory") {
		t.Fatalf("a missing profile dir must fail, got %v", err)
	}
	empty := t.TempDir()
	if _, err := runInstallerSync(t, dir, empty); err == nil || !strings.Contains(err.Error(), "no profile installer overlays found") {
		t.Fatalf("a profile with no overlay must fail as comparing nothing, got %v", err)
	}
	if err := os.RemoveAll(filepath.Join(dir, "installer/lib")); err != nil {
		t.Fatal(err)
	}
	if out, err := runInstallerSync(t, dir); err == nil || !strings.Contains(out, "lint-installer-sync: installer/lib missing") {
		t.Fatalf("a missing installer/lib must fail, got %v\n%s", err, out)
	}
}

func TestInstallerBuildList(t *testing.T) {
	got := installerBuildList("x=1\nfor b in a:river-a \\\n\tb:river-b \\\n\tc; do\n\techo $b\ndone\n")
	if want := []string{"a:river-a", "b:river-b", "c"}; !slices.Equal(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
}
