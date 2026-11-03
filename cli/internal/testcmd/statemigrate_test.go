// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

package testcmd

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// migrateFunc returns migrate_mountpoint() from the disk step (installer/lib/10-disk-zfs.sh):
// the part that moves an older image's /var/lib/core datasets to /var/lib/tide on import.
func migrateFunc(t *testing.T, step string) string {
	t.Helper()
	i := strings.Index(step, "migrate_mountpoint() {")
	if i < 0 {
		t.Fatal("10-disk-zfs.sh has no migrate_mountpoint function")
	}
	j := strings.Index(step[i:], "\n}\n")
	if j < 0 {
		t.Fatal("migrate_mountpoint is not closed")
	}
	return step[i : i+j+3]
}

// runMigrate runs the function against a fake zfs holding the given datasets (name -> mountpoint
// property), with the altroot prefix zfs prints for an imported pool, and returns the
// "zfs set" lines it made.
func runMigrate(t *testing.T, fn string, datasets map[string]string, ds, old, newp string) string {
	t.Helper()
	dir := t.TempDir()
	state := filepath.Join(dir, "state")
	bin := filepath.Join(dir, "bin")
	for _, d := range []string{state, bin} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for name, mp := range datasets {
		if err := os.WriteFile(filepath.Join(state, strings.ReplaceAll(name, "/", "_")), []byte(mp+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	fake := `#!/bin/sh
f() { echo "$1" | tr / _; }
case "$1 $2 $3" in
"list -H -o") for x in "$FAKE_STATE"/*; do [ -f "$x" ] && echo "$FAKE_PREFIX$(cat "$x")"; done; exit 0 ;;
"list -H "*) [ -f "$FAKE_STATE/$(f "$3")" ] && exit 0; exit 1 ;;
"get -H -o") echo "$FAKE_PREFIX$(cat "$FAKE_STATE/$(f "$6")")"; exit 0 ;;
"set "*) echo "set $2 $3" >> "$FAKE_LOG"; exit 0 ;;
esac
exit 1
`
	if err := writeExec(filepath.Join(bin, "zfs"), fake, 0o755); err != nil {
		t.Fatal(err)
	}
	script := "set -eu\nTARGET=/mnt/target\nPOOL=rpool\n" + fn + "\nmigrate_mountpoint " + ds + " " + old + " " + newp + "\n"
	sp := filepath.Join(dir, "run.sh")
	if err := os.WriteFile(sp, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(dir, "log")
	cmd := exec.Command("sh", sp)
	cmd.Env = append(os.Environ(), "PATH="+bin+":"+os.Getenv("PATH"), "FAKE_STATE="+state, "FAKE_LOG="+log, "FAKE_PREFIX=/mnt/target")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	b, _ := os.ReadFile(log)
	return strings.TrimSpace(string(b))
}

func TestStateMountpointMigration(t *testing.T) {
	needSh(t)
	b, err := os.ReadFile(filepath.Join(repoRoot(t), "installer/lib/10-disk-zfs.sh"))
	if err != nil {
		t.Fatal(err)
	}
	fn := migrateFunc(t, string(b))
	const set = "set mountpoint=/var/lib/tide rpool/state"
	cases := []struct {
		name string
		ds   map[string]string
		want string
	}{
		{"old path moves", map[string]string{"rpool/state": "/var/lib/core"}, set},
		{"new path is left", map[string]string{"rpool/state": "/var/lib/tide"}, ""},
		{"other path is left", map[string]string{"rpool/state": "/data"}, ""},
		{"absent dataset is skipped", map[string]string{"rpool/home": "/home"}, ""},
		{"new path in use is left", map[string]string{"rpool/state": "/var/lib/core", "rpool/other": "/var/lib/tide"}, ""},
	}
	for _, c := range cases {
		if got := runMigrate(t, fn, c.ds, "rpool/state", "/var/lib/core", "/var/lib/tide"); got != c.want {
			t.Errorf("%s: zfs set calls %q, want %q", c.name, got, c.want)
		}
	}
}

// The check must be able to fail: without the comparison with the old path, a dataset at
// another mountpoint would be moved.
func TestStateMountpointMigrationMutation(t *testing.T) {
	needSh(t)
	b, err := os.ReadFile(filepath.Join(repoRoot(t), "installer/lib/10-disk-zfs.sh"))
	if err != nil {
		t.Fatal(err)
	}
	fn := migrateFunc(t, string(b))
	broken := strings.Replace(fn, `[ "$_cur" = "$_old" ] || return 0`, `true`, 1)
	if broken == fn {
		t.Fatal("the step no longer compares the mountpoint with the old path as this test expects")
	}
	if got := runMigrate(t, broken, map[string]string{"rpool/state": "/data"}, "rpool/state", "/var/lib/core", "/var/lib/tide"); got == "" {
		t.Error("a migration that ignores the old path moved nothing; the test cannot see it")
	}
}
