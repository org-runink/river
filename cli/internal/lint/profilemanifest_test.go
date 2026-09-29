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
)

// pmFixture is a repository with one workstation profile on artools' common.yaml (river) and,
// when server is set, a server profile carrying its own common.yaml.
func pmFixture(t *testing.T, server bool) string {
	t.Helper()
	dir := t.TempDir()
	lbWrite(t, dir, "iso-profiles/river/profile.yaml", `# the workstation
rootfs:
  packages:
    - base
    - linux-runink   # the kernel
  packages-init:
    s6:
      - openssh-s6
livefs:
  packages:
    - river-live
`)
	lbWrite(t, dir, "iso-profiles/river/Packages-Root", "# allow-list\nbase linux-runink\nopenssh-s6\nplasma-desktop\n")
	lbWrite(t, dir, "iso-profiles/river/Packages-Live", "river-live\n")
	lbWrite(t, dir, "iso-profiles/river/live-overlay/etc/hostname", "river-live\n")
	if server {
		s := "iso-profiles/srv/"
		lbWrite(t, dir, s+"river-profile.env", "KIND=server\nISO_LABEL=SRV\nISO_NAME=srv\nGRUB_TITLE=Server\n")
		lbWrite(t, dir, s+"profile.yaml", "rootfs:\n  packages:\n    - nftables\n")
		lbWrite(t, dir, s+"common.yaml", "packages-base:\n  - base\npackages-init:\n  s6:\n    - s6\npackages-xorg: []\n")
		lbWrite(t, dir, s+"Packages-Root", "base\ns6\nnftables\n")
	}
	return dir
}

func pmRun(t *testing.T, dir string, args ...string) (string, error) {
	t.Helper()
	var errb, outb bytes.Buffer
	err := ProfileManifest(context.Background(), dir, args, &errb, &outb)
	return errb.String() + outb.String(), err
}

func TestProfileManifestClean(t *testing.T) {
	out, err := pmRun(t, pmFixture(t, true))
	if err != nil {
		t.Fatalf("clean tree failed: %v\n%s", err, out)
	}
	if !strings.Contains(out, "lint-profile-manifest: OK (2 profile(s): livefs == Packages-Live, live-overlay applied; 1 with a common.yaml: rootfs == Packages-Root; 1 on artools' common.yaml: rootfs ⊆ Packages-Root)") {
		t.Fatalf("unexpected OK line: %q", out)
	}
}

func TestProfileManifestFailures(t *testing.T) {
	cases := []struct {
		name, want string
		mutate     func(t *testing.T, dir string)
	}{
		{"unparseable", "cannot parse iso-profiles/river/profile.yaml:", func(t *testing.T, d string) {
			f := filepath.Join(d, "iso-profiles/river/profile.yaml")
			b, _ := os.ReadFile(f)
			os.WriteFile(f, append(b, []byte("  { flow: mapping }\n")...), 0o644)
		}},
		{"live overlay without livefs", "river: has live-overlay/ but profile.yaml has no livefs: key", func(t *testing.T, d string) {
			lbWrite(t, d, "iso-profiles/river/profile.yaml", "rootfs:\n  packages:\n    - base\n")
			os.Remove(filepath.Join(d, "iso-profiles/river/Packages-Live"))
		}},
		{"livefs drift", "river livefs: installed by the profile but not in the allow-list: river-live ", func(t *testing.T, d string) {
			lbWrite(t, d, "iso-profiles/river/Packages-Live", "other\n")
		}},
		{"livefs without Packages-Live", "river: profile.yaml has livefs packages but there is no Packages-Live", func(t *testing.T, d string) {
			os.Remove(filepath.Join(d, "iso-profiles/river/Packages-Live"))
		}},
		{"artix live", "river: profile.yaml installs Artix's live-session setup: artix-live-base ", func(t *testing.T, d string) {
			lbWrite(t, d, "iso-profiles/river/profile.yaml", "rootfs:\n  packages:\n    - base\nlivefs:\n  packages:\n    - river-live\n    - artix-live-base\n")
			lbWrite(t, d, "iso-profiles/river/Packages-Live", "river-live artix-live-base\n")
		}},
		{"subset violated", "river rootfs: installed by profile.yaml but not in the allow-list (Packages-Root): linux-runink ", func(t *testing.T, d string) {
			lbWrite(t, d, "iso-profiles/river/Packages-Root", "base\nopenssh-s6\n")
		}},
		{"server without common.yaml", "srv: a server profile without its own common.yaml", func(t *testing.T, d string) {
			os.Remove(filepath.Join(d, "iso-profiles/srv/common.yaml"))
			lbWrite(t, d, "iso-profiles/srv/Packages-Root", "nftables\n")
		}},
		{"server rootfs missing a package", "srv rootfs: in the allow-list but not installed by the profile: extra ", func(t *testing.T, d string) {
			lbWrite(t, d, "iso-profiles/srv/Packages-Root", "base\ns6\nnftables\nextra\n")
		}},
		{"server desktop list", "srv: common.yaml adds display/desktop packages: xorg-server ", func(t *testing.T, d string) {
			lbWrite(t, d, "iso-profiles/srv/common.yaml", "packages-base:\n  - base\npackages-init:\n  s6:\n    - s6\npackages-xorg:\n  - xorg-server\n")
		}},
		{"listed twice", "srv: listed twice across common.yaml and profile.yaml: base ", func(t *testing.T, d string) {
			lbWrite(t, d, "iso-profiles/srv/profile.yaml", "rootfs:\n  packages:\n    - nftables\n    - base\n")
		}},
		{"empty allow-list", "srv rootfs: the allow-list is empty (checked nothing)", func(t *testing.T, d string) {
			lbWrite(t, d, "iso-profiles/srv/Packages-Root", "# nothing\n")
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := pmFixture(t, true)
			c.mutate(t, dir)
			out, err := pmRun(t, dir)
			if err == nil {
				t.Fatalf("passed:\n%s", out)
			}
			if !strings.Contains(out, "lint-profile-manifest: "+c.want) {
				t.Fatalf("want %q in:\n%s", c.want, out)
			}
		})
	}
}

// A profile named on the command line (build/local-iso.sh passes a staged external one) is
// checked alone, from any path.
func TestProfileManifestNamedProfile(t *testing.T) {
	dir := pmFixture(t, true)
	ext := filepath.Join(t.TempDir(), "example-srv")
	for _, f := range []string{"river-profile.env", "profile.yaml", "common.yaml", "Packages-Root"} {
		b, _ := os.ReadFile(filepath.Join(dir, "iso-profiles/srv", f))
		lbWrite(t, ext, f, string(b))
	}
	if out, err := pmRun(t, dir, ext+"/"); err != nil || !strings.Contains(out, "OK (1 profile(s)") {
		t.Fatalf("external server profile: %v\n%s", err, out)
	}
	os.Remove(filepath.Join(ext, "common.yaml"))
	if out, err := pmRun(t, dir, ext); err == nil || !strings.Contains(out, "example-srv: a server profile without its own common.yaml") {
		t.Fatalf("external server profile without common.yaml passed: %v\n%s", err, out)
	}
	if out, err := pmRun(t, dir, filepath.Join(dir, "nowhere")); err == nil || !strings.Contains(out, "no profile.yaml was checked") {
		t.Fatalf("a run that checked nothing passed: %v\n%s", err, out)
	}
}

func TestProfileManifestYAML(t *testing.T) {
	items, perr := pmYAMLLists("x", []byte("---\na:\n  b:\n    - one  # c\n  c: []\n  d:\n  - two\ne:\n- three\r\n"))
	if len(perr) != 0 {
		t.Fatalf("parse errors: %v", perr)
	}
	got := []string{}
	for _, it := range items {
		got = append(got, it.path+"="+it.item)
	}
	if want := "a/b=one a/d=two e=three"; strings.Join(got, " ") != want {
		t.Fatalf("got %q, want %q", strings.Join(got, " "), want)
	}
}

func TestProfileKind(t *testing.T) {
	dir := t.TempDir()
	if k, ok := pmProfileKind(dir, "river"); !ok || k != "workstation" {
		t.Fatalf("river built in: %q %v", k, ok)
	}
	if _, ok := pmProfileKind(dir, "other"); ok {
		t.Fatal("a profile with no river-profile.env was read")
	}
	lbWrite(t, dir, "river-profile.env", "KIND=server\nISO_LABEL=bad label\nISO_NAME=x\nGRUB_TITLE=X\n")
	if _, ok := pmProfileKind(dir, "other"); ok {
		t.Fatal("an invalid ISO_LABEL was accepted")
	}
	lbWrite(t, dir, "river-profile.env", "KIND=server\nISO_LABEL=X\nISO_NAME=x\nGRUB_TITLE=X\nEDITION=ok-1\n")
	if k, ok := pmProfileKind(dir, "other"); !ok || k != "server" {
		t.Fatalf("valid server profile: %q %v", k, ok)
	}
}
