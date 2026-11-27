// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

package lint

// liveseat.go -- a live session that starts a compositor must give it a seat that does not
// depend on logind. Part of `river lint profile-manifest`.
//
// THE FAILURE. The live medium's first session is greetd's initial_session running weston. weston
// 15 opens the display only through libseat, which asks seatd and then logind. The image shipped
// no seatd daemon, and logind is not dependable on real hardware (elogind reports seat0
// CanGraphical=no when udev never tagged a late-loading DRM card, or has no session for the
// process): the compositor waited for ever, the screen stayed dead, and on the owner's
// 2026-11-27 photographs the log ended at "Backend 'seatd' failed to open seat, skipping". This
// check fails the build if a profile ships such a session without the whole seat wiring, so the
// pieces cannot be removed one at a time and leave the same dead screen.

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

var (
	pmSectionRe = regexp.MustCompile(`^\s*\[([^\]]+)\]\s*$`)
	pmCommandRe = regexp.MustCompile(`^\s*command\s*=\s*"(.*)"\s*$`)
)

// pmInitialSessionCommand returns the command of a greetd config's [initial_session], or "".
func pmInitialSessionCommand(toml string) string {
	in := false
	for _, ln := range strings.Split(toml, "\n") {
		if m := pmSectionRe.FindStringSubmatch(ln); m != nil {
			in = m[1] == "initial_session"
			continue
		}
		if m := pmCommandRe.FindStringSubmatch(ln); m != nil && in {
			return m[1]
		}
	}
	return ""
}

// pmLiveSeat returns what is wrong with a profile's live-session seat wiring, or nothing when the
// profile has no live greetd session. services is the profile's live-session/services list and
// pkgbuild the text of build/pkgbuilds/runink-greetd-s6/PKGBUILD ("" when there is none), which
// is where the in-tree image's seatd service is defined; a profile may instead define it in its
// own live-overlay/etc/s6/sv/seatd.
func pmLiveSeat(dir string, services []string, pkgbuild string) []string {
	b, err := os.ReadFile(filepath.Join(dir, "live-overlay", "etc", "greetd", "live.toml"))
	if err != nil {
		return nil
	}
	cmd := pmInitialSessionCommand(string(b))
	if cmd == "" {
		return nil // no first session without a login: no compositor to give a seat
	}
	var why []string
	if !strings.Contains(cmd, "LIBSEAT_BACKEND=seatd") {
		why = append(why, fmt.Sprintf("etc/greetd/live.toml: the initial session %q does not set LIBSEAT_BACKEND=seatd "+
			"(libseat would try logind, which can block for ever on real hardware)", cmd))
	}
	if !pmSeatServiceDefined(dir, pkgbuild) {
		why = append(why, "no seatd service is defined: neither live-overlay/etc/s6/sv/seatd (type `longrun`, a run "+
			"file that execs seatd -g <group>) nor the greetd s6 package ships one, so the compositor has no seat provider")
	}
	if _, err := os.Stat(filepath.Join(dir, "live-overlay", "etc", "s6", "sv", "greetd", "dependencies.d", "seatd")); err != nil {
		why = append(why, "live-overlay/etc/s6/sv/greetd/dependencies.d/seatd is missing: greetd could start the "+
			"compositor before the seat socket exists")
	}
	if !slices.Contains(services, "seatd") {
		why = append(why, "profile.yaml live-session/services does not enable seatd (s6 refuses a set where an "+
			"enabled service depends on a disabled one, and nothing would start the seat service)")
	}
	return why
}

// pmRunsSeatd reports whether an s6 run file execs the seatd binary with -g <group>.
func pmRunsSeatd(run string) bool {
	for _, ln := range strings.Split(run, "\n") {
		f := strings.Fields(ln)
		if len(f) == 0 || strings.HasPrefix(f[0], "#") {
			continue
		}
		if filepath.Base(f[0]) == "seatd" {
			for i, a := range f {
				if a == "-g" && i+1 < len(f) {
					return true
				}
			}
		}
	}
	return false
}

// pmSeatServiceDefined reports whether a seatd s6 service exists: in the profile's own live
// overlay (type longrun, a run file that execs seatd with a group), or in the greetd s6 package's
// PKGBUILD, which writes etc/s6/sv/seatd with `seatd -g <group>`.
func pmSeatServiceDefined(dir, pkgbuild string) bool {
	sv := filepath.Join(dir, "live-overlay", "etc", "s6", "sv", "seatd")
	t, terr := os.ReadFile(filepath.Join(sv, "type"))
	run, rerr := os.ReadFile(filepath.Join(sv, "run"))
	if terr == nil && rerr == nil && strings.TrimSpace(string(t)) == "longrun" && pmRunsSeatd(string(run)) {
		return true
	}
	return strings.Contains(pkgbuild, "etc/s6/sv/seatd") &&
		regexp.MustCompile(`(?m)^/usr/bin/seatd -g \S+$`).MatchString(pkgbuild) &&
		regexp.MustCompile(`(?m)^\s*printf 'longrun\\n' > "\$ss/type"`).MatchString(pkgbuild)
}
