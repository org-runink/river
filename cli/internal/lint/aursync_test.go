// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

package lint

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func aurSHA(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }

// aurFixture writes an in-tree kernel and ZFS package and their AUR twins, all agreeing:
// kernel 7.2.7, zen1, pkgrel 2; OpenZFS 2.4.4.
func aurFixture(t *testing.T) string {
	t.Helper()
	dir := fixtureRepo(t)
	cfgBase, cfgDelta, cfgReq, bpfdoc := "CONFIG_A=y\n", "CONFIG_B=m\n", "CONFIG_C=y\n", "package main\n"
	kSums := []string{aurSHA("linux"), aurSHA("linux.sign"), aurSHA("zen.patch"), "SKIP",
		aurSHA(cfgBase), aurSHA(cfgDelta), aurSHA(cfgReq), aurSHA(bpfdoc)}
	sumsArray := func(s []string) string {
		var b strings.Builder
		b.WriteString("sha256sums=(\n")
		for i, v := range s {
			fmt.Fprintf(&b, "  '%s' # source %d\n", v, i+1)
		}
		b.WriteString(")\n")
		return b.String()
	}
	srcinfoSums := func(s []string) string {
		var b strings.Builder
		for _, v := range s {
			fmt.Fprintf(&b, "\tsha256sums = %s\n", v)
		}
		return b.String()
	}
	kpkgbuild := "pkgbase=linux-runink\n_major=7.2\n_minor=7\n_zenrel=1 # zen release\npkgrel='2'\n" + sumsArray(kSums)
	zSums := []string{aurSHA("zfs-2.4.4.tar.gz"), aurSHA("zfs.sign")}
	tag := "v7.2.7.zen1-2"
	azSums := []string{zSums[0], aurSHA("aur-only-patch")}
	writeTree(t, dir, map[string]string{
		aurK + "/PKGBUILD":        kpkgbuild,
		aurK + "/config.base":     cfgBase,
		aurK + "/config.delta":    cfgDelta,
		aurK + "/config.require":  cfgReq,
		aurK + "/bpfdoc.go":       bpfdoc,
		aurK + "/keys/pgp/A.asc":  "key A\n",
		aurB:                      bpfdoc,
		aurZ + "/PKGBUILD":        "pkgname=runink-zfs\npkgver=2.4.4\npkgrel=1\n" + sumsArray(zSums),
		aurZ + "/keys/pgp/Z.asc":  "key Z\n",
		aurA + "/PKGBUILD":        "pkgbase=linux-runink\n_major=7.2\n_minor=7\n_zenrel=1\npkgrel=2\n" + sumsArray(kSums),
		aurA + "/keys/pgp/A.asc":  "key A\n",
		aurAZ + "/keys/pgp/Z.asc": "key Z\n",
		aurA + "/.SRCINFO": "pkgbase = linux-runink\n\tpkgver = 7.2.7.zen1\n\tpkgrel = 2\n" +
			"\tsource = config.delta::https://example.org/river/raw/" + tag + "/build/pkgbuilds/runink-kernel/config.delta\n" +
			"\tsource = bpfdoc.go::https://example.org/river/raw/" + tag + "/build/tools/bpfdoc/main.go\n" +
			srcinfoSums(kSums),
		aurAZ + "/PKGBUILD": "pkgname=zfs-linux-runink\n_zfsver=\"2.4.4\"\n_kernver=7.2.7.zen1-2\n_kernrel=7.2.7-zen1-2-runink\npkgrel=3\n" + sumsArray(azSums),
		aurAZ + "/.SRCINFO": "pkgbase = zfs-linux-runink\n\tpkgver = 2.4.4_7.2.7.zen1.2\n\tpkgrel = 3\n\tdepends = linux-runink=7.2.7.zen1-2\n" + srcinfoSums(azSums),
	})
	return dir
}

func runAURSync(t *testing.T, dir string) (string, error) {
	t.Helper()
	var errb, outb bytes.Buffer
	err := AURSync(context.Background(), dir, &errb, &outb)
	return errb.String() + outb.String(), err
}

func TestAURSyncClean(t *testing.T) {
	out, err := runAURSync(t, aurFixture(t))
	if err != nil || out != "lint-aur-sync: OK (linux-runink 7.2.7.zen1-2, zfs 2.4.4)\n" {
		t.Fatalf("clean tree: %v\n%s", err, out)
	}
}

// editFixture replaces old with new in the fixture file f, failing if old is not there.
func editFixture(t *testing.T, dir, f, old, new string) {
	t.Helper()
	p := filepath.Join(dir, f)
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(b, []byte(old)) {
		t.Fatalf("%s has no %q", f, old)
	}
	if err := os.WriteFile(p, bytes.Replace(b, []byte(old), []byte(new), 1), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestAURSyncFailures(t *testing.T) {
	cases := []struct {
		name   string
		break_ func(t *testing.T, dir string)
		want   string
	}{
		{"1: config edited, pin not", func(t *testing.T, d string) {
			editFixture(t, d, aurK+"/config.delta", "CONFIG_B=m", "CONFIG_B=y")
		}, aurK + "/PKGBUILD pins config.delta as"},
		{"1: a pin dropped", func(t *testing.T, d string) {
			editFixture(t, d, aurK+"/PKGBUILD", "  'SKIP' # source 4\n", "")
		}, aurK + "/PKGBUILD: expected 8 sha256sums entries"},
		{"1b: bpfdoc edited, pin not", func(t *testing.T, d string) {
			writeTree(t, d, map[string]string{aurB: "package main // edited\n", aurK + "/bpfdoc.go": "package main // edited\n"})
		}, aurK + "/PKGBUILD pins bpfdoc.go as"},
		{"1b: bpfdoc.go is not the tool", func(t *testing.T, d string) {
			writeTree(t, d, map[string]string{aurK + "/bpfdoc.go": "package other\n"})
		}, aurK + "/bpfdoc.go is not " + aurB},
		{"2: AUR pins differ", func(t *testing.T, d string) {
			editFixture(t, d, aurA+"/PKGBUILD", aurSHA("zen.patch"), aurSHA("other.patch"))
		}, aurA + "/PKGBUILD sha256sums differ"},
		{"2: AUR pkgrel differs", func(t *testing.T, d string) {
			editFixture(t, d, aurA+"/PKGBUILD", "pkgrel=2", "pkgrel=3")
		}, "pkgrel differs: " + aurA + "/PKGBUILD='3' " + aurK + "/PKGBUILD='2'"},
		{"3: .SRCINFO pkgver stale", func(t *testing.T, d string) {
			editFixture(t, d, aurA+"/.SRCINFO", "pkgver = 7.2.7.zen1", "pkgver = 7.2.6.zen1")
		}, aurA + "/.SRCINFO pkgver is not 7.2.7.zen1"},
		{"3: .SRCINFO sums stale", func(t *testing.T, d string) {
			editFixture(t, d, aurA+"/.SRCINFO", "sha256sums = SKIP", "sha256sums = "+aurSHA("x"))
		}, aurA + "/.SRCINFO sha256sums are stale"},
		{"3: configs from another tag", func(t *testing.T, d string) {
			editFixture(t, d, aurA+"/.SRCINFO", "v7.2.7.zen1-2/build/pkgbuilds", "v7.2.6.zen1-1/build/pkgbuilds")
		}, "does not fetch the configs from tag v7.2.7.zen1-2"},
		{"4: zfs version", func(t *testing.T, d string) {
			editFixture(t, d, aurZ+"/PKGBUILD", "pkgver=2.4.4", "pkgver=2.4.5")
		}, aurAZ + "/PKGBUILD _zfsver is not 2.4.5"},
		{"4: kernel pin", func(t *testing.T, d string) {
			editFixture(t, d, aurAZ+"/PKGBUILD", "_kernver=7.2.7.zen1-2", "_kernver=7.2.7.zen1-1")
		}, aurAZ + "/PKGBUILD _kernver is not 7.2.7.zen1-2"},
		{"4: depends stale", func(t *testing.T, d string) {
			editFixture(t, d, aurAZ+"/.SRCINFO", "depends = linux-runink=7.2.7.zen1-2", "depends = linux-runink=7.2.7.zen1-1")
		}, aurAZ + "/.SRCINFO is stale (linux-runink pin)"},
		{"5: key differs", func(t *testing.T, d string) {
			writeTree(t, d, map[string]string{aurAZ + "/keys/pgp/Z.asc": "another key\n"})
		}, aurAZ + "/keys/pgp/Z.asc missing or differs from " + aurZ + "/keys/pgp/Z.asc"},
		{"5: no keys", func(t *testing.T, d string) {
			if err := os.Remove(filepath.Join(d, aurK+"/keys/pgp/A.asc")); err != nil {
				t.Fatal(err)
			}
		}, "no keys found in " + aurK + "/keys/pgp"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := aurFixture(t)
			c.break_(t, dir)
			out, err := runAURSync(t, dir)
			if err == nil {
				t.Fatalf("passed:\n%s", out)
			}
			if !strings.Contains(out, "lint-aur-sync: ") || !strings.Contains(out, c.want) {
				t.Fatalf("want %q in:\n%s", c.want, out)
			}
		})
	}
}

func TestAURSyncMissingInput(t *testing.T) {
	dir := aurFixture(t)
	if err := os.Remove(filepath.Join(dir, aurAZ+"/.SRCINFO")); err != nil {
		t.Fatal(err)
	}
	_, err := runAURSync(t, dir)
	if err == nil || err.Error() != "lint-aur-sync: missing "+aurAZ+"/.SRCINFO" {
		t.Fatalf("a missing input must fail by name, got %v", err)
	}
}

func TestPKGBUILDParsers(t *testing.T) {
	pb := "_x='1.2' # c\nsha256sums=('a' # ) ends here\n  'b')\n"
	if got := pkgbuildVar(pb, "_x"); got != "1.2" {
		t.Errorf("pkgbuildVar = %q", got)
	}
	// As the script's awk: the first line holding ')' ends the array, even in a comment.
	if got := pkgbuildSums("sha256sums=('" + aurSHA("a") + "' # )\n  '" + aurSHA("b") + "')\n"); len(got) != 1 {
		t.Errorf("pkgbuildSums = %q", got)
	}
}
