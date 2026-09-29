// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

package lint

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// k0s-pin — the k0s version is pinned in TWO places and shipped from a THIRD, and until
// 2026-09-04 nothing checked that any of them agreed:
//
//	build/config.env            K0S_VERSION="v1.33.13+k0s.1"   <- read by NO script (docs only)
//	build/pkgbuilds/runink-k0s  _k0sver / pkgver               <- the only effective pin
//	localrepo/runink-k0s-*.pkg.tar.zst                         <- what buildiso ACTUALLY bakes
//
// WHY THIS EXISTS. The pin is not cosmetic: v1.33.13 is the FLOOR at which kube-router's
// image gains the kubernetes-sigs iptables-wrapper and selects the nft backend to match
// kube-proxy. Below it (v2.2.1/v2.4.1) kube-router enforces NetworkPolicy in iptables-LEGACY
// while kube-proxy DNATs ClusterIPs in NFT — two rule engines on the same kernel hooks,
// which drops in-cluster ClusterIP traffic in bursts.
//
// THIS ALREADY SHIPPED BROKEN. On 2026-09-04 the live server was measured running k0s
// v1.31.2+k0s.0 with kube-router v2.2.1-iptables1.8.9-0 — 841 ip6tables rules in the legacy
// backend against 25 in nft — while this repo had pinned 1.33.13 since commit bd37ddd (Aug 22).
// The bump was written and never reached a node: a stale runink-k0s-1.31.2 package sat in
// localrepo/, and buildiso bakes whatever is in localrepo, not whatever the PKGBUILD says.
// CI agents failed for weeks on what looked like DNS, then RBAC, then NetworkPolicy.
//
// A pin nothing enforces is a comment. This makes the three agree or fails the build.
//
// HOW TO FIX A FAILURE:
//
//	version mismatch  -> decide the intended version, then set it in BOTH config.env and the
//	                     PKGBUILD (_k0sver AND pkgver), and re-pin sha256sums from k0s's
//	                     published sha256sums.txt for that release. Never lower it below 1.33.
//	stale localrepo   -> rm the offending localrepo/runink-k0s-*.pkg.tar.zst and rebuild:
//	                     make components && make localrepo. Do NOT ship the old package.
//
// The local repository is --localrepo, also read from RIVER_LOCALREPO and, as the script did,
// LOCALREPO (default localrepo, relative to --repo).

const (
	k0sCfg       = "build/config.env"
	k0sPKGBUILD  = "build/pkgbuilds/runink-k0s/PKGBUILD"
	k0sAirgap    = "build/pkgbuilds/runink-k0s-airgap/PKGBUILD"
	k0sImageLock = "build/k0s-images.lock"
	k0sFloorMaj  = 1
	k0sFloorMin  = 33
)

func init() { extraLints = append(extraLints, k0sPinCmd) }

func k0sPinCmd(v *viper.Viper) *cobra.Command {
	c := &cobra.Command{
		Use:   "k0s-pin",
		Short: "the k0s pin agrees across build/config.env, the PKGBUILDs, the image lock and localrepo/",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return K0sPin(cmd.Context(), v.GetString("repo"), v.GetString("localrepo"), cmd.ErrOrStderr(), cmd.OutOrStdout())
		},
	}
	c.Flags().String("localrepo", "localrepo", "the local package repository buildiso bakes from (relative to --repo)")
	// The script read LOCALREPO; RIVER_LOCALREPO is the CLI's own spelling. Both work.
	_ = v.BindEnv("localrepo", "RIVER_LOCALREPO", "LOCALREPO")
	return c
}

// k0sSedValue returns the first capture of re over the lines of b, like `sed -n 's/…/\1/p' | head -1`.
func k0sSedValue(b []byte, re *regexp.Regexp) string {
	for _, l := range strings.Split(string(b), "\n") {
		if m := re.FindStringSubmatch(l); m != nil {
			return m[1]
		}
	}
	return ""
}

var (
	k0sCfgVerRe  = regexp.MustCompile(`^K0S_VERSION="(.*)"$`)
	k0sK0sverRe  = regexp.MustCompile(`^_k0sver="(.*)"$`)
	k0sPkgverRe  = regexp.MustCompile(`^pkgver=(.*)$`)
	k0sPkgrelRe  = regexp.MustCompile(`^pkgrel=(.*)$`)
	k0sNftKRRe   = regexp.MustCompile(`v2\.(5|6|7|8|9)|v[3-9]\.`)
	k0sDerivedRe = regexp.MustCompile(`\+k0s\..*$`)
)

// k0sKubeRouterCNIVersion returns the `version:` line under spec.images.kuberouter.cni in a
// k0s.yaml, or "". It matches the NESTED cni: key, which is what k0s actually consumes. The flat
// kuberouter.{image,version} form passes `k0s config validate` (exit 0) and is then silently
// ignored, so accepting it here would certify a config that does nothing — measured on the
// box, kube-router stayed on v2.2.1.
func k0sKubeRouterCNIVersion(b []byte) string {
	k, c := false, false
	for _, l := range strings.Split(string(b), "\n") {
		switch {
		case strings.HasPrefix(l, "    kuberouter:"):
			k = true
			continue
		case k && strings.HasPrefix(l, "      cni:"):
			c = true
			continue
		case c && strings.HasPrefix(l, "        version:"):
			return l
		}
		if len(l) > 2 && strings.HasPrefix(l, "  ") && l[2] >= 'a' && l[2] <= 'z' {
			k, c = false, false
		}
	}
	return ""
}

// K0sPin runs the lint over repo. localrepo is relative to repo unless absolute.
func K0sPin(_ context.Context, repo, localrepo string, errw, outw io.Writer) error {
	r := &report{name: "lint-k0s-pin", w: errw}
	if localrepo == "" {
		localrepo = "localrepo"
	}
	lrShown := localrepo // messages name it as given, as the script did
	if !filepath.IsAbs(localrepo) {
		localrepo = filepath.Join(repo, localrepo)
	}
	at := func(p string) string { return filepath.Join(repo, p) }

	cfg, err := os.ReadFile(at(k0sCfg))
	if err != nil {
		fmt.Fprintf(errw, "lint-k0s-pin: %s missing\n", k0sCfg)
		return fmt.Errorf("lint-k0s-pin: %s missing", k0sCfg)
	}
	pkgb, err := os.ReadFile(at(k0sPKGBUILD))
	if err != nil {
		fmt.Fprintf(errw, "lint-k0s-pin: %s missing\n", k0sPKGBUILD)
		return fmt.Errorf("lint-k0s-pin: %s missing", k0sPKGBUILD)
	}

	// 1. config.env K0S_VERSION vs PKGBUILD _k0sver.
	cfgVer := k0sSedValue(cfg, k0sCfgVerRe)
	pkgK0sver := k0sSedValue(pkgb, k0sK0sverRe)
	pkgVer := k0sSedValue(pkgb, k0sPkgverRe)
	pkgRel := k0sSedValue(pkgb, k0sPkgrelRe)
	if cfgVer == "" {
		r.fail("could not read K0S_VERSION from %s", k0sCfg)
	}
	if pkgK0sver == "" {
		r.fail("could not read _k0sver from %s", k0sPKGBUILD)
	}
	if pkgVer == "" {
		r.fail("could not read pkgver from %s", k0sPKGBUILD)
	}
	if cfgVer != "" && pkgK0sver != "" && cfgVer != pkgK0sver {
		r.fail("version drift: %s K0S_VERSION=%s but %s _k0sver=%s", k0sCfg, cfgVer, k0sPKGBUILD, pkgK0sver)
	}
	// pkgver must be _k0sver without the leading 'v' and without the '+k0s.N' build suffix —
	// that is what names the built package file, and therefore what localrepo is checked against.
	if pkgK0sver != "" {
		derived := k0sDerivedRe.ReplaceAllString(strings.TrimPrefix(pkgK0sver, "v"), "")
		if derived != pkgVer {
			r.fail("pkgver=%s does not match _k0sver=%s (expected pkgver=%s)", pkgVer, pkgK0sver, derived)
		}
	}

	// 2. The netfilter guarantee. The REQUIREMENT is "kube-router must not be a hard-legacy
	// build", not "k0s must be >= 1.33". Those were the same thing until k0s 1.33 turned out to
	// be unable to start at all on an IPv6-only serviceCIDR (see the PKGBUILD header), which made
	// a bare version floor demand a configuration that cannot boot. Satisfied EITHER WAY:
	//   a) k0s >= 1.33, whose bundled kube-router already carries the iptables-wrapper, OR
	//   b) k0s < 1.33 WITH an explicit spec.images.kuberouter.cni override to a wrapper build.
	// Below 1.33 with no override remains a hard failure — that is exactly the 1.31.2 that
	// shipped broken and cost weeks of dropped TCP egress.
	if pkgVer != "" && k0sBelowFloor(pkgVer) {
		// Checked for the reference config (build/k0s/k0s.yaml, what the airgap bundle is listed
		// from) and PER PROFILE: a profile that forgot the override would ship the original
		// outage while the reference looked fine.
		yamls := []string{"build/k0s/k0s.yaml"}
		prof, _ := filepath.Glob(filepath.Join(repo, "iso-profiles", "*", "root-overlay", "etc", "k0s", "k0s.yaml"))
		for _, p := range prof {
			rel, _ := filepath.Rel(repo, p)
			yamls = append(yamls, filepath.ToSlash(rel))
		}
		for _, y := range yamls {
			b, err := os.ReadFile(at(y))
			if err != nil {
				continue
			}
			name := "reference"
			if !strings.HasPrefix(y, "build/") {
				name = strings.Split(y, "/")[1]
			}
			if !k0sNftKRRe.MatchString(k0sKubeRouterCNIVersion(b)) {
				r.fail("%s pins k0s %s (< %d.%d) but its k0s.yaml has no spec.images.kuberouter.cni override to an nft-capable kube-router — NetworkPolicy would land in iptables-legacy while kube-proxy uses nft, and egress rules with a TCP port match would never fire.", name, pkgVer, k0sFloorMaj, k0sFloorMin)
			}
		}
	}

	// 3. sha256 must be real, not SKIP. The binary is fetched from the internet at build time;
	// an unverified download is the one thing a sovereign golden image must not do.
	if bytes.Contains(pkgb, []byte(`sha256sums=('SKIP')`)) || bytes.Contains(pkgb, []byte(`sha256sums=("SKIP")`)) {
		r.fail("%s has sha256sums=SKIP — the k0s binary would be baked unverified", k0sPKGBUILD)
	}

	// 4. No stale package in localrepo. buildiso bakes what is HERE, not what the PKGBUILD
	// says. This is the check that maps directly onto the 2026-09-04 incident.
	lrIsDir := false
	if st, err := os.Stat(localrepo); err == nil && st.IsDir() {
		lrIsDir = true
	}
	if lrIsDir && pkgVer != "" {
		for _, base := range k0sGlobBase(localrepo, "runink-k0s-*.pkg.tar.zst") {
			switch {
			case strings.HasPrefix(base, "runink-k0s-airgap-"): // checked in 5.
			case strings.HasPrefix(base, "runink-k0s-"+pkgVer+"-"+pkgRel+"-"):
			default:
				r.fail("stale package in %s: %s (pin is %s-%s) — buildiso would bake THIS, not the pin. rm it and re-run: make components && make localrepo", lrShown, base, pkgVer, pkgRel)
			}
		}
	}

	// 5. The airgap bundle is for THIS k0s. runink-k0s-airgap carries the system images of one
	// k0s version (build/k0s-airgap.sh); a bundle for another version would leave the node
	// pulling on first start, which an air-gapped node cannot do.
	if ab, err := os.ReadFile(at(k0sAirgap)); err == nil {
		aK0sver := k0sSedValue(ab, k0sK0sverRe)
		aVer := k0sSedValue(ab, k0sPkgverRe)
		if aK0sver != pkgK0sver {
			r.fail("%s _k0sver=%s, runink-k0s is %s", k0sAirgap, aK0sver, pkgK0sver)
		}
		if aVer != pkgVer {
			r.fail("%s pkgver=%s, runink-k0s is %s", k0sAirgap, aVer, pkgVer)
		}
		lock, lerr := os.ReadFile(at(k0sImageLock))
		if lerr != nil {
			r.fail("%s missing (build/k0s-airgap.sh --update-lock)", k0sImageLock)
		}
		header, pinned := false, 0
		sc := bufio.NewScanner(bytes.NewReader(lock))
		for sc.Scan() {
			l := sc.Text()
			if strings.HasPrefix(l, "# k0s-images.lock — the k0s "+pkgK0sver+" ") {
				header = true
			}
			if !strings.HasPrefix(l, "#") {
				pinned++
			}
		}
		if !header {
			r.fail("%s was not written for k0s %s (build/k0s-airgap.sh --update-lock)", k0sImageLock, pkgK0sver)
		}
		if pinned == 0 {
			r.fail("%s pins no image", k0sImageLock)
		}
		if lrIsDir {
			for _, base := range k0sGlobBase(localrepo, "runink-k0s-airgap-*.pkg.tar.zst") {
				if !strings.HasPrefix(base, "runink-k0s-airgap-"+pkgVer+"-") {
					r.fail("stale package in %s: %s (k0s pin is %s)", lrShown, base, pkgVer)
				}
			}
		}
	} else {
		r.fail("%s missing: a server without it pulls k0s's system images on first start", k0sAirgap)
	}

	if err := r.err(); err != nil {
		return err
	}
	fmt.Fprintf(outw, "lint-k0s-pin: ok (k0s %s, pkg %s-%s)\n", pkgK0sver, pkgVer, pkgRel)
	return nil
}

// k0sBelowFloor reports whether pkgver (1.31.2) is below the 1.33 floor. A version that does
// not parse is not below it, as `[ "$maj" -lt 1 ] 2>/dev/null` was false in the script.
func k0sBelowFloor(pkgVer string) bool {
	parts := strings.SplitN(pkgVer, ".", 3)
	maj, err := strconv.Atoi(parts[0])
	if err != nil {
		return false
	}
	if maj < k0sFloorMaj {
		return true
	}
	if len(parts) < 2 {
		return false
	}
	min, err := strconv.Atoi(parts[1])
	if err != nil {
		return false
	}
	return maj == k0sFloorMaj && min < k0sFloorMin
}

// k0sGlobBase returns the base names of the files in dir matching pattern.
func k0sGlobBase(dir, pattern string) []string {
	m, _ := filepath.Glob(filepath.Join(dir, pattern))
	out := make([]string, 0, len(m))
	for _, f := range m {
		out = append(out, filepath.Base(f))
	}
	return out
}
