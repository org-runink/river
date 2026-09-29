// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

package testcmd

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// repoRoot is the checkout this package sits in (cli/internal/testcmd).
func repoRoot(t *testing.T) string {
	t.Helper()
	r, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func needSh(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh")
	}
}

func TestChecksCountAndReport(t *testing.T) {
	var out, errb bytes.Buffer
	c := &checks{out: &out, err: &errb}
	c.ok("first")
	c.expect("second", false)
	c.eq("third", "a", "b")
	c.eq("fourth", "x", "x")
	if c.n != 4 || c.failed != 2 {
		t.Fatalf("n=%d failed=%d, want 4 and 2", c.n, c.failed)
	}
	if out.String() != "  ok    first\n  ok    fourth\n" {
		t.Fatalf("stdout %q", out.String())
	}
	if errb.String() != "  FAIL  second\n  FAIL  third: got 'a', want 'b'\n" {
		t.Fatalf("stderr %q", errb.String())
	}
}

func TestSedFirst(t *testing.T) {
	for _, tc := range []struct{ in, re, repl, want string }{
		{`"a": "RIVER", "b": "RIVER"` + "\nx\n", `"(a|b)": *"RIVER"`, `"$1": "X"`, `"a": "X", "b": "RIVER"` + "\nx\n"},
		{"  linux /vmlinuz ro\ninitrd /i\n", `^([[:space:]]*linux[[:space:]].*)$`, `$1 river.edition=river`, "  linux /vmlinuz ro river.edition=river\ninitrd /i\n"},
		{"no newline", `new`, `old`, "no oldline"},
	} {
		if got := sedFirst(tc.in, tc.re, tc.repl); got != tc.want {
			t.Errorf("sedFirst(%q, %q) = %q, want %q", tc.in, tc.re, got, tc.want)
		}
	}
}

func TestLineHelpers(t *testing.T) {
	s := "# local\noptions zfs zfs_arc_max=12\noptions spl x=1\n"
	if !hasLine(s, "options spl x=1") || hasLine(s, "options spl") {
		t.Fatal("hasLine is not an exact line match")
	}
	if got := arcValue(s); got != "12" {
		t.Fatalf("arcValue = %q", got)
	}
	if lines("") != nil || len(lines("a\n")) != 1 {
		t.Fatal("lines keeps an empty last line")
	}
	if joinLines(nil) != "" || joinLines([]string{"a", "b"}) != "a\nb\n" {
		t.Fatal("joinLines")
	}
}

func TestCopyTreeKeepsModesAndLinks(t *testing.T) {
	src := filepath.Join(t.TempDir(), "src")
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.MkdirAll(filepath.Join(src, "ro/sub"), 0o755))
	must(writeExec(filepath.Join(src, "ro/sub/step.sh"), "#!/bin/sh\n", 0o750))
	must(os.WriteFile(filepath.Join(src, "plain"), []byte("x"), 0o600))
	must(os.Symlink("plain", filepath.Join(src, "link")))
	must(os.Chmod(filepath.Join(src, "ro"), 0o555))
	t.Cleanup(func() { _ = os.Chmod(filepath.Join(src, "ro"), 0o755) })

	dst := filepath.Join(t.TempDir(), "dst")
	must(copyTree(src, dst))
	t.Cleanup(func() { _ = os.Chmod(filepath.Join(dst, "ro"), 0o755) })
	for path, want := range map[string]string{"ro": "555", "ro/sub/step.sh": "750", "plain": "600"} {
		if got := mode(filepath.Join(dst, path)); got != want {
			t.Errorf("%s: mode %s, want %s", path, got, want)
		}
	}
	if l, err := os.Readlink(filepath.Join(dst, "link")); err != nil || l != "plain" {
		t.Errorf("link: %q, %v", l, err)
	}
	a, _ := listTree(src)
	b, _ := listTree(dst)
	if !slices.Equal(a, b) || len(a) != 6 {
		t.Errorf("trees differ: %v vs %v", a, b)
	}
}

// The code under test is shell; the Go side must still fail when that shell is wrong.

func fixtureWith(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestMemtuneMissingLibrary(t *testing.T) {
	var out, errb bytes.Buffer
	err := Memtune(context.Background(), t.TempDir(), &out, &errb)
	if err == nil || !strings.Contains(errb.String(), "memtune: missing ") {
		t.Fatalf("err=%v stderr=%q", err, errb.String())
	}
}

func TestMemtuneCatchesABrokenClamp(t *testing.T) {
	needSh(t)
	root := repoRoot(t)
	lib := read(t, filepath.Join(root, "installer/lib/memtune.sh"))
	broken := strings.Replace(lib, "MEMTUNE_ARC_MAX_MIB=16384", "MEMTUNE_ARC_MAX_MIB=32768", 1)
	if broken == lib {
		t.Fatal("the fixture no longer matches installer/lib/memtune.sh")
	}
	zram := "iso-profiles/river/root-overlay/usr/local/bin/runink-zram.sh"
	dir := fixtureWith(t, map[string]string{
		"installer/lib/memtune.sh": broken,
		zram:                       read(t, filepath.Join(root, zram)),
	})
	var out, errb bytes.Buffer
	err := Memtune(context.Background(), dir, &out, &errb)
	if err == nil {
		t.Fatalf("a broken clamp passed:\n%s", out.String())
	}
	if !strings.Contains(errb.String(), "  FAIL  512 GiB, no plan: the 16 GiB ceiling: got '34359738368', want '17179869184'") ||
		!strings.Contains(errb.String(), "memtune: FAILED") {
		t.Fatalf("stderr: %s", errb.String())
	}
}

func TestInstalledHooksCatchesALibraryThatRunsNothing(t *testing.T) {
	needSh(t)
	lib := read(t, filepath.Join(repoRoot(t), "build/qemu-hooks.sh"))
	// hooks_list that lists every *.sh whatever its mode: the host side is wrong.
	broken := strings.Replace(lib, `[ "${HOOKS_ANY_MODE:-0}" = 1 ] || [ -x "$hk_f" ] || continue`, `:`, 1)
	if broken == lib {
		t.Fatal("the fixture no longer matches build/qemu-hooks.sh")
	}
	dir := fixtureWith(t, map[string]string{"build/qemu-hooks.sh": broken})
	var out, errb bytes.Buffer
	if err := InstalledHooks(context.Background(), dir, &out, &errb); err == nil {
		t.Fatalf("a broken hooks_list passed:\n%s", out.String())
	}
	if !strings.Contains(errb.String(), "  FAIL  only executable *.sh are hooks") {
		t.Fatalf("stderr: %s", errb.String())
	}
}

func TestFirstbootHooksNoRunner(t *testing.T) {
	var out, errb bytes.Buffer
	err := FirstbootHooks(context.Background(), t.TempDir(), &out, &errb)
	if err == nil || !strings.Contains(errb.String(), "firstboot-hooks: no runner at ") {
		t.Fatalf("err=%v stderr=%q", err, errb.String())
	}
}
