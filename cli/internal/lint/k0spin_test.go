// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

package lint

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/org-runink/river/cli/internal/rootcmd"
)

// lbWrite writes body to dir/name, creating parents.
func lbWrite(t *testing.T, dir, name, body string) {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

const k0sOverride = "spec:\n  images:\n    kuberouter:\n      cni:\n        image: quay.io/k0sproject/kube-router\n        version: v2.5.0-iptables1.8.11-0\n  network:\n    provider: kuberouter\n"

// k0sFixture is a tree that passes: k0s 1.31.2 with the kube-router override, an airgap
// package and image lock for it, and a localrepo holding exactly the pinned packages.
func k0sFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	lbWrite(t, dir, k0sCfg, "K0S_VERSION=\"v1.31.2+k0s.0\"\n")
	lbWrite(t, dir, k0sPKGBUILD, "pkgname=runink-k0s\npkgver=1.31.2\npkgrel=1\n_k0sver=\"v1.31.2+k0s.0\"\nsha256sums=('c18d')\n")
	lbWrite(t, dir, k0sAirgap, "pkgname=runink-k0s-airgap\npkgver=1.31.2\npkgrel=1\n_k0sver=\"v1.31.2+k0s.0\"\n")
	lbWrite(t, dir, k0sImageLock, "# k0s-images.lock — the k0s v1.31.2+k0s.0 system images for build/k0s/k0s.yaml.\nquay.io/k0sproject/kube-router:v2.5.0 sha256:aa sha256:bb\n")
	lbWrite(t, dir, "build/k0s/k0s.yaml", k0sOverride)
	lbWrite(t, dir, "iso-profiles/river/root-overlay/etc/k0s/k0s.yaml", k0sOverride)
	lbWrite(t, dir, "localrepo/runink-k0s-1.31.2-1-x86_64.pkg.tar.zst", "")
	lbWrite(t, dir, "localrepo/runink-k0s-airgap-1.31.2-1-x86_64.pkg.tar.zst", "")
	return dir
}

func k0sRun(t *testing.T, dir, localrepo string) (string, error) {
	t.Helper()
	var errb, outb bytes.Buffer
	err := K0sPin(context.Background(), dir, localrepo, &errb, &outb)
	return errb.String() + outb.String(), err
}

func TestK0sPinClean(t *testing.T) {
	out, err := k0sRun(t, k0sFixture(t), "localrepo")
	if err != nil {
		t.Fatalf("clean tree failed: %v\n%s", err, out)
	}
	if !strings.Contains(out, "lint-k0s-pin: ok (k0s v1.31.2+k0s.0, pkg 1.31.2-1)") {
		t.Fatalf("unexpected OK line: %q", out)
	}
}

func TestK0sPinFailures(t *testing.T) {
	cases := []struct {
		name, want string
		mutate     func(t *testing.T, dir string)
	}{
		{"config drift", "version drift: build/config.env K0S_VERSION=v1.33.13+k0s.1", func(t *testing.T, d string) {
			lbWrite(t, d, k0sCfg, "K0S_VERSION=\"v1.33.13+k0s.1\"\n")
		}},
		{"unreadable pin", "could not read K0S_VERSION", func(t *testing.T, d string) {
			lbWrite(t, d, k0sCfg, "# nothing\n")
		}},
		{"pkgver mismatch", "pkgver=1.31.3 does not match _k0sver=v1.31.2+k0s.0 (expected pkgver=1.31.2)", func(t *testing.T, d string) {
			lbWrite(t, d, k0sPKGBUILD, "pkgver=1.31.3\npkgrel=1\n_k0sver=\"v1.31.2+k0s.0\"\nsha256sums=('c18d')\n")
		}},
		{"no override below the floor", "lint-k0s-pin: river pins k0s 1.31.2 (< 1.33)", func(t *testing.T, d string) {
			// The flat form passes `k0s config validate` and is ignored: it must not count.
			lbWrite(t, d, "iso-profiles/river/root-overlay/etc/k0s/k0s.yaml", "spec:\n  images:\n    kuberouter:\n      image: quay.io/k0sproject/kube-router\n      version: v2.5.0\n")
		}},
		{"legacy override in the reference", "reference pins k0s 1.31.2 (< 1.33)", func(t *testing.T, d string) {
			lbWrite(t, d, "build/k0s/k0s.yaml", strings.Replace(k0sOverride, "v2.5.0", "v2.2.1", 1))
		}},
		{"SKIP sums", "sha256sums=SKIP", func(t *testing.T, d string) {
			lbWrite(t, d, k0sPKGBUILD, "pkgver=1.31.2\npkgrel=1\n_k0sver=\"v1.31.2+k0s.0\"\nsha256sums=('SKIP')\n")
		}},
		{"stale package", "stale package in localrepo: runink-k0s-1.30.0-1-x86_64.pkg.tar.zst (pin is 1.31.2-1)", func(t *testing.T, d string) {
			lbWrite(t, d, "localrepo/runink-k0s-1.30.0-1-x86_64.pkg.tar.zst", "")
		}},
		{"stale airgap package", "stale package in localrepo: runink-k0s-airgap-1.30.0-1-x86_64.pkg.tar.zst (k0s pin is 1.31.2)", func(t *testing.T, d string) {
			lbWrite(t, d, "localrepo/runink-k0s-airgap-1.30.0-1-x86_64.pkg.tar.zst", "")
		}},
		{"airgap for another k0s", "_k0sver=v1.30.0+k0s.0, runink-k0s is v1.31.2+k0s.0", func(t *testing.T, d string) {
			lbWrite(t, d, k0sAirgap, "pkgver=1.31.2\n_k0sver=\"v1.30.0+k0s.0\"\n")
		}},
		{"lock for another k0s", "was not written for k0s v1.31.2+k0s.0", func(t *testing.T, d string) {
			lbWrite(t, d, k0sImageLock, "# k0s-images.lock — the k0s v1.30.0+k0s.0 system images\nimg sha256:aa sha256:bb\n")
		}},
		{"lock pins nothing", "build/k0s-images.lock pins no image", func(t *testing.T, d string) {
			lbWrite(t, d, k0sImageLock, "# k0s-images.lock — the k0s v1.31.2+k0s.0 system images\n")
		}},
		{"airgap missing", "runink-k0s-airgap/PKGBUILD missing: a server without it", func(t *testing.T, d string) {
			os.Remove(filepath.Join(d, k0sAirgap))
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := k0sFixture(t)
			c.mutate(t, dir)
			out, err := k0sRun(t, dir, "localrepo")
			if err == nil {
				t.Fatalf("passed:\n%s", out)
			}
			if !strings.Contains(out, "lint-k0s-pin: ") || !strings.Contains(out, c.want) {
				t.Fatalf("want %q in:\n%s", c.want, out)
			}
		})
	}
}

func TestK0sPinAboveFloorNeedsNoOverride(t *testing.T) {
	dir := k0sFixture(t)
	lbWrite(t, dir, k0sCfg, "K0S_VERSION=\"v1.33.13+k0s.1\"\n")
	lbWrite(t, dir, k0sPKGBUILD, "pkgver=1.33.13\npkgrel=1\n_k0sver=\"v1.33.13+k0s.1\"\nsha256sums=('aa')\n")
	lbWrite(t, dir, k0sAirgap, "pkgver=1.33.13\n_k0sver=\"v1.33.13+k0s.1\"\n")
	lbWrite(t, dir, k0sImageLock, "# k0s-images.lock — the k0s v1.33.13+k0s.1 system images\nimg sha256:aa sha256:bb\n")
	lbWrite(t, dir, "build/k0s/k0s.yaml", "spec: {}\n")
	os.RemoveAll(filepath.Join(dir, "localrepo"))
	if out, err := k0sRun(t, dir, "localrepo"); err != nil {
		t.Fatalf("k0s 1.33 without an override failed: %v\n%s", err, out)
	}
}

func TestK0sPinMissingConfig(t *testing.T) {
	dir := k0sFixture(t)
	os.Remove(filepath.Join(dir, k0sCfg))
	if out, err := k0sRun(t, dir, "localrepo"); err == nil || !strings.Contains(out, "lint-k0s-pin: build/config.env missing") {
		t.Fatalf("want a missing-config failure, got %v\n%s", err, out)
	}
}

// The Makefile and build/local-iso.sh pass LOCALREPO=…; the command still reads it.
func TestK0sPinReadsLOCALREPO(t *testing.T) {
	dir := k0sFixture(t)
	other := t.TempDir()
	lbWrite(t, other, "runink-k0s-1.29.0-1-x86_64.pkg.tar.zst", "")
	t.Setenv("LOCALREPO", other)
	root := rootcmd.New()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"lint", "k0s-pin", "--repo", dir})
	if err := root.Execute(); err == nil || !strings.Contains(out.String(), "stale package in "+other+": runink-k0s-1.29.0-1") {
		t.Fatalf("LOCALREPO was not read: %v\n%s", err, out.String())
	}
}
