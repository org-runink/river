// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

package repocmd

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// PublicPackages is the allow-list: the only package names [runink] may carry, each one built by
// River's own build/pkgbuilds (a test holds the two together). Anything else, a downstream's
// own package above all, is refused, whatever its contents.
var PublicPackages = map[string]string{
	"linux-runink":         "build/pkgbuilds/runink-kernel",
	"linux-runink-headers": "build/pkgbuilds/runink-kernel",
	"runink-zfs":           "build/pkgbuilds/runink-zfs",
	"runink-zfs-utils":     "build/pkgbuilds/runink-zfs",
	"runink-k0s":           "build/pkgbuilds/runink-k0s",
	"runink-k0s-airgap":    "build/pkgbuilds/runink-k0s-airgap",
	"runink-installer":     "build/pkgbuilds/runink-installer",
	"runink-grub-live":     "build/pkgbuilds/runink-grub-live",
	"runink-tayga":         "build/pkgbuilds/runink-tayga",
	"river-guide":          "build/pkgbuilds/river-guide",
	"runink-core":          "build/pkgbuilds/runink-core",
	"runink-runtime":       "build/pkgbuilds/runink-runtime",
}

// payloadCarriers are the two packages whose contents come from a downstream payload
// (RIVER_PAYLOAD_DIR) when there is one. Built for Runink River alone they hold nothing of
// it, and only that build is public: each may contain exactly these files and nothing else.
var payloadCarriers = map[string]struct {
	files    []string // the only files allowed
	required []string // of those, the ones that must be present
}{
	// A base build writes the PAYLOAD-NONE marker and nothing else; a downstream build fills
	// the tree (deploy, enroll.d, firstboot.d) instead.
	"runink-core": {
		files:    []string{"usr/local/share/runink/core/PAYLOAD-NONE"},
		required: []string{"usr/local/share/runink/core/PAYLOAD-NONE"},
	},
	// A base build carries the models manifest (from the public models.lock) at most; a
	// downstream build adds its host binaries under usr/local/bin and OCI images.
	"runink-runtime": {
		files: []string{"usr/local/share/runink/models.manifest"},
	},
}

// payloadPrefixes are where a downstream payload lands. No public package installs anything
// under them, whatever its name (runink-core's PAYLOAD-NONE marker aside).
var payloadPrefixes = []string{
	"usr/local/share/runink/core/",
	"usr/local/lib/runink/firstboot.d/",
	"usr/local/share/runink/images/",
}

// privatePath reports a path that belongs to a private build: its state directories are
// named for the private variant or for the downstream server profile. The server profile's
// name is matched by SHA-256 digest (like `river lint public-leak`), so this public file
// does not carry it.
func privatePath(p string) bool {
	l := strings.ToLower(p)
	if strings.Contains(l, "private") {
		return true
	}
	parts := strings.FieldsFunc(l, func(r rune) bool { return r == '/' || r == '-' || r == '_' || r == '.' })
	for i := 0; i+1 < len(parts); i++ {
		sum := sha256.Sum256([]byte(parts[i] + "-" + parts[i+1]))
		if hex.EncodeToString(sum[:]) == serverProfileDigest {
			return true
		}
	}
	return false
}

// serverProfileDigest is the SHA-256 of the downstream server profile's directory name.
const serverProfileDigest = "ba42872b5bbd81f3c608a8e906caf1e29710feb2736839eb8c7f78c7861107cc"

// fileNameOK is what GitHub keeps unchanged in a release asset name. A character outside it
// is rewritten on upload, and pacman would then fetch a name that does not exist.
var fileNameOK = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// CheckPublic returns every reason p may not be published; none means it may.
func CheckPublic(p *Package) []string {
	var why []string
	if privatePath(p.Path) {
		why = append(why, fmt.Sprintf("comes from a private build directory (%s)", p.Path))
	}
	if _, ok := PublicPackages[p.Name]; !ok {
		why = append(why, fmt.Sprintf("package %q is not one River builds (not on the public allow-list)", p.Name))
	}
	if want := p.Name + "-" + p.Version + "-" + p.Arch + ".pkg.tar.zst"; p.File != want {
		why = append(why, fmt.Sprintf("file name does not match its .PKGINFO (want %s)", want))
	}
	if p.Arch != "x86_64" && p.Arch != "any" {
		why = append(why, fmt.Sprintf("arch %q is not x86_64 or any", p.Arch))
	}
	if !fileNameOK.MatchString(p.File) {
		why = append(why, "file name has a character GitHub would rewrite in a release asset name")
	}
	carrier, isCarrier := payloadCarriers[p.Name]
	var payload []string
	for _, f := range p.Files {
		if isCarrier && slices.Contains(carrier.files, f) {
			continue
		}
		if isCarrier {
			payload = append(payload, f)
			continue
		}
		for _, pre := range payloadPrefixes {
			if strings.HasPrefix(f, pre) {
				payload = append(payload, f)
				break
			}
		}
	}
	if len(payload) > 0 {
		allowed := "nothing under " + strings.Join(payloadPrefixes, ", ")
		if isCarrier {
			allowed = "only " + strings.Join(carrier.files, ", ")
		}
		why = append(why, fmt.Sprintf("carries %d downstream payload file(s), e.g. %s (a public %s holds %s)",
			len(payload), strings.Join(payload[:min(3, len(payload))], ", "), p.Name, allowed))
	}
	for _, r := range carrier.required {
		if !slices.Contains(p.Files, r) {
			why = append(why, fmt.Sprintf("has no %s: not the empty tree of a build without a downstream payload", r))
		}
	}
	return why
}
