// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

package lint

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// aur-sync — the AUR packaging (packaging/aur/) must describe the SAME kernel and ZFS as the
// in-tree packages (build/pkgbuilds/runink-kernel, build/pkgbuilds/runink-zfs).
//
// The AUR linux-runink fetches config.base / config.delta / config.require from a tag of this
// repository and pins them by sha256. If config.delta changes and the pins do not, the AUR
// package fails to verify for every user; if the pins change and the in-tree PKGBUILD's do
// not, the image build does. So this checks, and fails on any difference:
//
//  1. the in-tree PKGBUILD pins match the three config files' sha256 and bpfdoc's
//     (build/tools/bpfdoc/main.go);
//  2. the AUR PKGBUILD pins every source to the same sha256 as the in-tree PKGBUILD, and has
//     the same _major/_minor/_zenrel/pkgrel;
//  3. each AUR .SRCINFO agrees with its PKGBUILD (pkgver, pkgrel, sha256sums), i.e. it was
//     regenerated with `makepkg --printsrcinfo` after the last edit;
//  4. zfs-linux-runink pins the in-tree OpenZFS version and tarball sha256, and the exact
//     linux-runink pkgver-pkgrel and kernelrelease;
//  5. the AUR packages carry the same signing keys as the in-tree ones.
//
// It examines a fixed list of files and fails if any is missing, so it cannot pass having
// looked at nothing.

const (
	aurK  = "build/pkgbuilds/runink-kernel"
	aurZ  = "build/pkgbuilds/runink-zfs"
	aurB  = "build/tools/bpfdoc/main.go"
	aurA  = "packaging/aur/linux-runink"
	aurAZ = "packaging/aur/zfs-linux-runink"
)

func init() { extraLints = append(extraLints, aurSyncCmd) }

func aurSyncCmd(v *viper.Viper) *cobra.Command {
	return &cobra.Command{
		Use:   "aur-sync",
		Short: "the AUR packaging pins the same kernel, configs and OpenZFS as the tree",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return AURSync(cmd.Context(), v.GetString("repo"), cmd.ErrOrStderr(), cmd.OutOrStdout())
		},
	}
}

var (
	pkgbuildSumRe     = regexp.MustCompile(`'([0-9a-fA-F]{64}|SKIP)'`)
	pkgbuildSumsStart = regexp.MustCompile(`^sha256sums=\(`)
)

// AURSync runs the lint over repo.
func AURSync(_ context.Context, repo string, errw, outw io.Writer) error {
	r := &report{name: "lint-aur-sync", w: errw}
	K, Z, B, A, AZ := aurK, aurZ, aurB, aurA, aurAZ

	text := map[string]string{}
	for _, f := range []string{K + "/PKGBUILD", K + "/config.base", K + "/config.delta", K + "/config.require",
		Z + "/PKGBUILD", B, K + "/bpfdoc.go", A + "/PKGBUILD", A + "/.SRCINFO", AZ + "/PKGBUILD", AZ + "/.SRCINFO"} {
		b, err := os.ReadFile(filepath.Join(repo, f))
		if err != nil {
			return fmt.Errorf("lint-aur-sync: missing %s", f)
		}
		text[f] = string(b)
	}
	sha := func(f string) string {
		s, err := sha256OfFile(filepath.Join(repo, f))
		if err != nil {
			return ""
		}
		return s
	}
	v := func(f, name string) string { return pkgbuildVar(text[f], name) }
	si := func(f, key string) string { return strings.Join(srcinfoValues(text[f], key), "\n") }

	ksums := pkgbuildSums(text[K+"/PKGBUILD"])
	asums := pkgbuildSums(text[A+"/PKGBUILD"])
	if len(ksums) != 8 {
		r.fail("%s/PKGBUILD: expected 8 sha256sums entries", K)
	}

	// 1. In-tree pins match the config files.
	for i, c := range []string{"config.base", "config.delta", "config.require"} {
		have := sha(K + "/" + c)
		if nth(ksums, 5+i) != have {
			r.fail("%s/PKGBUILD pins %s as %s, file is %s", K, c, nth(ksums, 5+i), have)
		}
	}
	// 1b. ...and the 8th pins bpfdoc.go, the Go port of scripts/bpf_doc.py (a link to
	//     build/tools/bpfdoc/main.go), so editing the tool without re-pinning fails here, not
	//     in makepkg.
	if have := sha(B); nth(ksums, 8) != have {
		r.fail("%s/PKGBUILD pins bpfdoc.go as %s, %s is %s", K, nth(ksums, 8), B, have)
	}
	if text[K+"/bpfdoc.go"] != text[B] {
		r.fail("%s/bpfdoc.go is not %s (it must be a link to it)", K, B)
	}

	// 2. AUR PKGBUILD: same sources, same version.
	if strings.Join(asums, "\n") != strings.Join(ksums, "\n") {
		r.fail("%s/PKGBUILD sha256sums differ from %s/PKGBUILD (kernel, zen patch or config pins)", A, K)
	}
	for _, name := range []string{"_major", "_minor", "_zenrel", "pkgrel"} {
		if a, k := v(A+"/PKGBUILD", name), v(K+"/PKGBUILD", name); a != k {
			r.fail("%s differs: %s/PKGBUILD='%s' %s/PKGBUILD='%s'", name, A, a, K, k)
		}
	}
	kver := v(K+"/PKGBUILD", "_major") + "." + v(K+"/PKGBUILD", "_minor")
	kpkgver := kver + ".zen" + v(K+"/PKGBUILD", "_zenrel")
	kpkgrel := v(K+"/PKGBUILD", "pkgrel")
	tag := "v" + kpkgver + "-" + kpkgrel

	// 3. .SRCINFO regenerated.
	if si(A+"/.SRCINFO", "pkgver") != kpkgver {
		r.fail("%s/.SRCINFO pkgver is not %s", A, kpkgver)
	}
	if si(A+"/.SRCINFO", "pkgrel") != kpkgrel {
		r.fail("%s/.SRCINFO pkgrel is not %s", A, kpkgrel)
	}
	if si(A+"/.SRCINFO", "sha256sums") != strings.Join(asums, "\n") {
		r.fail("%s/.SRCINFO sha256sums are stale (run makepkg --printsrcinfo)", A)
	}
	if !strings.Contains(text[A+"/.SRCINFO"], tag+"/build/pkgbuilds/runink-kernel/config.delta") {
		r.fail("%s/.SRCINFO does not fetch the configs from tag %s", A, tag)
	}
	if !regexp.MustCompile(`(?m)` + regexp.QuoteMeta(tag+"/build/tools/bpfdoc/main.go") + `$`).MatchString(text[A+"/.SRCINFO"]) {
		r.fail("%s/.SRCINFO does not fetch bpfdoc from build/tools/bpfdoc/main.go at tag %s", A, tag)
	}

	// 4. zfs-linux-runink pins.
	zver := v(Z+"/PKGBUILD", "pkgver")
	if v(AZ+"/PKGBUILD", "_zfsver") != zver {
		r.fail("%s/PKGBUILD _zfsver is not %s (runink-zfs)", AZ, zver)
	}
	azsums := pkgbuildSums(text[AZ+"/PKGBUILD"])
	if nth(azsums, 1) != nth(pkgbuildSums(text[Z+"/PKGBUILD"]), 1) {
		r.fail("%s/PKGBUILD pins a different zfs-%s.tar.gz sha256 than %s/PKGBUILD", AZ, zver, Z)
	}
	if v(AZ+"/PKGBUILD", "_kernver") != kpkgver+"-"+kpkgrel {
		r.fail("%s/PKGBUILD _kernver is not %s-%s", AZ, kpkgver, kpkgrel)
	}
	if v(AZ+"/PKGBUILD", "_kernrel") != kver+"-zen"+v(K+"/PKGBUILD", "_zenrel")+"-"+kpkgrel+"-runink" {
		r.fail("%s/PKGBUILD _kernrel does not match linux-runink's kernelrelease", AZ)
	}
	if si(AZ+"/.SRCINFO", "sha256sums") != strings.Join(azsums, "\n") {
		r.fail("%s/.SRCINFO sha256sums are stale", AZ)
	}
	zpkgver := zver + "_" + strings.ReplaceAll(kpkgver+"-"+kpkgrel, "-", ".")
	if si(AZ+"/.SRCINFO", "pkgver") != zpkgver {
		r.fail("%s/.SRCINFO pkgver is not %s (stale)", AZ, zpkgver)
	}
	if si(AZ+"/.SRCINFO", "pkgrel") != v(AZ+"/PKGBUILD", "pkgrel") {
		r.fail("%s/.SRCINFO pkgrel is stale", AZ)
	}
	if !regexp.MustCompile(`(?m)` + regexp.QuoteMeta("depends = linux-runink="+kpkgver+"-"+kpkgrel) + `$`).MatchString(text[AZ+"/.SRCINFO"]) {
		r.fail("%s/.SRCINFO is stale (linux-runink pin)", AZ)
	}

	// 5. Keys.
	for _, pair := range [][2]string{{K + "/keys/pgp", A + "/keys/pgp"}, {Z + "/keys/pgp", AZ + "/keys/pgp"}} {
		src, dst := pair[0], pair[1]
		keys, _ := filepath.Glob(filepath.Join(repo, src, "*.asc"))
		n := 0
		for _, k := range keys {
			if st, err := os.Stat(k); err != nil || !st.Mode().IsRegular() {
				continue
			}
			n++
			base := filepath.Base(k)
			a, errA := os.ReadFile(k)
			b, errB := os.ReadFile(filepath.Join(repo, dst, base))
			if errA != nil || errB != nil || !bytes.Equal(a, b) {
				r.fail("%s/%s missing or differs from %s/%s", dst, base, src, base)
			}
		}
		if n == 0 {
			r.fail("no keys found in %s", src)
		}
	}

	if err := r.err(); err != nil {
		return err
	}
	fmt.Fprintf(outw, "lint-aur-sync: OK (linux-runink %s-%s, zfs %s)\n", kpkgver, kpkgrel, zver)
	return nil
}

// pkgbuildSums returns the sha256sums=(...) entries of a PKGBUILD in order: from the line
// that opens the array to the first line holding a ')', comments stripped.
func pkgbuildSums(pkgbuild string) []string {
	var sums []string
	on := false
	for _, line := range strings.Split(pkgbuild, "\n") {
		if pkgbuildSumsStart.MatchString(line) {
			on = true
		}
		if !on {
			continue
		}
		code, _, _ := strings.Cut(line, "#")
		for _, m := range pkgbuildSumRe.FindAllStringSubmatch(code, -1) {
			sums = append(sums, m[1])
		}
		if strings.Contains(line, ")") {
			break
		}
	}
	return sums
}

// pkgbuildVar returns the literal value of the first top-level NAME=value assignment, up to
// a space or '#', with quotes removed.
func pkgbuildVar(pkgbuild, name string) string {
	re := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(name) + `=([^ #\n]*)`)
	m := re.FindStringSubmatch(pkgbuild)
	if m == nil {
		return ""
	}
	return strings.NewReplacer("'", "", `"`, "").Replace(m[1])
}

// srcinfoValues returns the value of every "KEY = value" line of a .SRCINFO, in order.
func srcinfoValues(srcinfo, key string) []string {
	var vs []string
	prefix := key + " = "
	for _, line := range strings.Split(srcinfo, "\n") {
		t := strings.TrimLeft(line, " \t\v\f\r")
		if rest, ok := strings.CutPrefix(t, prefix); ok {
			vs = append(vs, rest)
		}
	}
	return vs
}

// nth is the 1-based i-th element of s, or "" when s is shorter.
func nth(s []string, i int) string {
	if i < 1 || i > len(s) {
		return ""
	}
	return s[i-1]
}
