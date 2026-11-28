// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

package lint

import (
	"bytes"
	"context"
	"crypto/sha1" // #nosec G505 -- OpenPGP v4 fingerprints are SHA-1 by definition (RFC 4880 §12.2); the pinned sha256 guards integrity.
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// blackarch — the [blackarch] repository is configured and NOTHING is installed from it
// (docs/BLACKARCH.md). This holds the pieces to each other:
//
//	pacman/blackarch-keyring.pin                    the pin: hashes, trusted fingerprints, signer
//	iso-profiles/river/root-overlay/usr/share/pacman/keyrings/blackarch{.gpg,-trusted,-revoked}
//	                                                the vendored keyring the image ships
//	iso-profiles/river/root-overlay/etc/pacman.d/blackarch-mirrorlist
//	pacman/pacman.conf.in                           the build-time stanza (last, Required, Sync Search)
//	installer/lib/35-pacman-keyring.sh              populates the keyring, writes the node stanza
//	iso-profiles/*/Packages-*                       name no BlackArch package
//
// It checks, offline: each vendored file has the pinned sha256; blackarch-trusted lists exactly
// the pinned fingerprints; every one of them, and the repository signer, is a primary key in
// blackarch.gpg (fingerprints computed from the key packets, not taken from a comment); no
// trusted key is also revoked; [blackarch] is the LAST repository in pacman.conf.in with
// SigLevel = Required, Usage = Sync Search and the mirrorlist Include; the mirrorlist has at
// least three Server lines, all HTTPS; the installer populates the keyring; no package list
// names a blackarch* package.
//
// HOW TO FIX A FAILURE: a hash mismatch means the keyring files and the pin disagree. Do not
// "fix" the pin to whatever the file now hashes to: redo the verification steps in the pin's
// header for the new upstream keyring package and update the files and the pin together.

const (
	baPin        = "pacman/blackarch-keyring.pin"
	baKeyrings   = "iso-profiles/river/root-overlay/usr/share/pacman/keyrings"
	baMirrorlist = "iso-profiles/river/root-overlay/etc/pacman.d/blackarch-mirrorlist"
	baConf       = "pacman/pacman.conf.in"
	baInstaller  = "installer/lib/35-pacman-keyring.sh"
	baInclude    = "/etc/pacman.d/blackarch-mirrorlist"
)

func init() { extraLints = append(extraLints, blackarchCmd) }

func blackarchCmd(v *viper.Viper) *cobra.Command {
	return &cobra.Command{
		Use:   "blackarch",
		Short: "[blackarch] is configured with its pinned keyring and HTTPS mirrors, and no package list installs from it",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return Blackarch(cmd.Context(), v.GetString("repo"), cmd.ErrOrStderr(), cmd.OutOrStdout())
		},
	}
}

var (
	baPinRe = regexp.MustCompile(`^([A-Z0-9_]+)="([^"]*)"$`)
	baFprRe = regexp.MustCompile(`^[0-9A-F]{40}$`)
)

// Blackarch runs the check against the tree at repo.
func Blackarch(_ context.Context, repo string, stderr, stdout io.Writer) error {
	rp := &report{name: "blackarch", w: stderr}
	read := func(rel string) []byte {
		b, err := os.ReadFile(filepath.Join(repo, rel))
		if err != nil {
			rp.fail("%s: %v", rel, err)
			return nil
		}
		return b
	}

	pin := map[string]string{}
	for _, l := range strings.Split(string(read(baPin)), "\n") {
		if m := baPinRe.FindStringSubmatch(strings.TrimSpace(l)); m != nil {
			pin[m[1]] = m[2]
		}
	}
	for _, k := range []string{"UPSTREAM_PKG", "UPSTREAM_PKG_SHA256", "REPO_SIGNER", "TRUSTED",
		"SHA256_BLACKARCH_GPG", "SHA256_BLACKARCH_TRUSTED", "SHA256_BLACKARCH_REVOKED"} {
		if pin[k] == "" {
			rp.fail("%s: %s is missing or empty", baPin, k)
		}
	}

	// 1. The vendored files are the pinned bytes.
	files := map[string][]byte{}
	for name, key := range map[string]string{
		"blackarch.gpg":     "SHA256_BLACKARCH_GPG",
		"blackarch-trusted": "SHA256_BLACKARCH_TRUSTED",
		"blackarch-revoked": "SHA256_BLACKARCH_REVOKED",
	} {
		b := read(filepath.Join(baKeyrings, name))
		files[name] = b
		if b == nil {
			continue
		}
		sum := sha256.Sum256(b)
		if got := hex.EncodeToString(sum[:]); got != pin[key] {
			rp.fail("%s/%s: sha256 %s, %s pins %s", baKeyrings, name, got, baPin, pin[key])
		}
	}

	// 2. blackarch-trusted is exactly the pinned list; nothing trusted is revoked; the signer is trusted.
	pinned := strings.Fields(pin["TRUSTED"])
	trusted := baFingerprints(files["blackarch-trusted"])
	if !slices.Equal(trusted, pinned) {
		rp.fail("%s/blackarch-trusted lists %v, %s pins %v", baKeyrings, trusted, baPin, pinned)
	}
	for _, f := range append(slices.Clone(pinned), pin["REPO_SIGNER"]) {
		if !baFprRe.MatchString(f) {
			rp.fail("%s: %q is not a 40-hex fingerprint", baPin, f)
		}
	}
	if !slices.Contains(pinned, pin["REPO_SIGNER"]) {
		rp.fail("%s: REPO_SIGNER %s is not in TRUSTED", baPin, pin["REPO_SIGNER"])
	}
	for _, r := range baFingerprints(files["blackarch-revoked"]) {
		if slices.Contains(trusted, r) {
			rp.fail("%s: %s is both trusted and revoked", baKeyrings, r)
		}
	}

	// 3. Every trusted fingerprint is a primary key in blackarch.gpg.
	if files["blackarch.gpg"] != nil {
		prim, err := pgpKeyringFingerprints(files["blackarch.gpg"])
		if err != nil {
			rp.fail("%s/blackarch.gpg: %v", baKeyrings, err)
		}
		for _, f := range pinned {
			if !slices.Contains(prim, f) {
				rp.fail("%s/blackarch.gpg has no primary key %s", baKeyrings, f)
			}
		}
	}

	// 4. pacman.conf.in: [blackarch] is the last repository, signature-required, install-proof.
	if b := read(baConf); b != nil {
		secs, body := pacmanSections(b)
		switch {
		case len(secs) == 0 || secs[len(secs)-1] != "blackarch":
			rp.fail("%s: [blackarch] must be the LAST section (pacman takes a package from the first repository that has it); sections: %v", baConf, secs)
		default:
			opts := body["blackarch"]
			for _, want := range []string{"SigLevel = Required", "Usage = Sync Search", "Include = " + baInclude} {
				if !slices.Contains(opts, want) {
					rp.fail("%s: [blackarch] lacks %q (has %v)", baConf, want, opts)
				}
			}
			for _, o := range opts {
				if strings.HasPrefix(o, "Server") {
					rp.fail("%s: [blackarch] must use the mirrorlist, not %q", baConf, o)
				}
			}
		}
	}

	// 5. The mirrorlist: several mirrors, HTTPS only.
	if b := read(baMirrorlist); b != nil {
		n := 0
		for _, l := range strings.Split(string(b), "\n") {
			l = strings.TrimSpace(l)
			if !strings.HasPrefix(l, "Server") {
				continue
			}
			n++
			u := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(strings.TrimPrefix(l, "Server")), "="))
			if !strings.HasPrefix(u, "https://") || !strings.Contains(u, "$repo") || !strings.Contains(u, "$arch") {
				rp.fail("%s: %q must be an https:// URL with $repo and $arch", baMirrorlist, u)
			}
		}
		if n < 3 {
			rp.fail("%s: %d Server lines, want at least 3", baMirrorlist, n)
		}
	}

	// 6. The installer populates the keyring and writes the node stanza.
	if b := read(baInstaller); b != nil {
		for _, want := range []string{"pacman-key --populate blackarch", `[blackarch]\nSigLevel = Required\nInclude = ` + baInclude} {
			if !bytes.Contains(b, []byte(want)) {
				rp.fail("%s: does not contain %q", baInstaller, want)
			}
		}
	}

	// 7. No package list names a BlackArch package.
	lists, _ := filepath.Glob(filepath.Join(repo, "iso-profiles", "*", "Packages-*"))
	for _, p := range lists {
		b, err := os.ReadFile(p)
		if err != nil {
			rp.fail("%s: %v", p, err)
			continue
		}
		for _, name := range pmManifest(b) {
			if strings.HasPrefix(name, "blackarch") {
				rp.fail("%s: names %s; the image installs no BlackArch package (docs/BLACKARCH.md)", p, name)
			}
		}
	}

	if err := rp.err(); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "blackarch: keyring %s pinned (%d trusted keys), %s last with SigLevel = Required, no package list installs from it\n",
		pin["UPSTREAM_PKG"], len(pinned), baConf)
	return nil
}

// baFingerprints returns the fingerprint column of a pacman-key trusted/revoked file.
func baFingerprints(b []byte) []string {
	var out []string
	for _, l := range strings.Split(string(b), "\n") {
		if i := strings.IndexByte(l, '#'); i >= 0 {
			l = l[:i]
		}
		if f, _, _ := strings.Cut(strings.TrimSpace(l), ":"); f != "" {
			out = append(out, f)
		}
	}
	return out
}

// pacmanSections returns the section names of a pacman.conf in order, and each section's
// non-comment lines with whitespace around "=" normalised to "key = value".
func pacmanSections(b []byte) ([]string, map[string][]string) {
	var secs []string
	body := map[string][]string{}
	cur := ""
	for _, l := range strings.Split(string(b), "\n") {
		if i := strings.IndexByte(l, '#'); i >= 0 {
			l = l[:i]
		}
		l = strings.TrimSpace(l)
		switch {
		case l == "":
		case strings.HasPrefix(l, "[") && strings.HasSuffix(l, "]"):
			cur = l[1 : len(l)-1]
			secs = append(secs, cur)
		default:
			if k, v, ok := strings.Cut(l, "="); ok {
				l = strings.TrimSpace(k) + " = " + strings.Join(strings.Fields(v), " ")
			}
			body[cur] = append(body[cur], l)
		}
	}
	return secs, body
}

// pgpPrimaryFingerprints returns the v4 fingerprints of the primary public keys (packet tag 6)
// in a binary OpenPGP keyring: SHA-1 over 0x99, the two-byte body length and the body
// (RFC 4880 §12.2). Subkeys (tag 14) are skipped. Armored input is rejected: see
// pgpKeyringFingerprints.
func pgpPrimaryFingerprints(b []byte) ([]string, error) {
	var out []string
	for len(b) > 0 {
		h := b[0]
		if h&0x80 == 0 {
			return nil, errors.New("not a binary OpenPGP keyring (bad packet header)")
		}
		var tag int
		var n, hl int
		if h&0x40 != 0 { // new format
			tag = int(h & 0x3f)
			if len(b) < 2 {
				return nil, errors.New("truncated packet header")
			}
			switch l0 := int(b[1]); {
			case l0 < 192:
				n, hl = l0, 2
			case l0 < 224:
				if len(b) < 3 {
					return nil, errors.New("truncated packet header")
				}
				n, hl = (l0-192)<<8+int(b[2])+192, 3
			case l0 == 255:
				if len(b) < 6 {
					return nil, errors.New("truncated packet header")
				}
				n, hl = int(b[2])<<24|int(b[3])<<16|int(b[4])<<8|int(b[5]), 6
			default:
				return nil, errors.New("partial body lengths are not valid in a keyring")
			}
		} else { // old format
			tag = int(h>>2) & 0x0f
			switch h & 3 {
			case 0:
				n, hl = int(b[1]), 2
			case 1:
				n, hl = int(b[1])<<8|int(b[2]), 3
			case 2:
				n, hl = int(b[1])<<24|int(b[2])<<16|int(b[3])<<8|int(b[4]), 5
			default:
				return nil, errors.New("indeterminate packet length")
			}
			if len(b) < hl {
				return nil, errors.New("truncated packet header")
			}
		}
		if n < 0 || len(b) < hl+n {
			return nil, errors.New("truncated packet")
		}
		body := b[hl : hl+n]
		if tag == 6 {
			if len(body) == 0 || body[0] != 4 {
				return nil, fmt.Errorf("primary key packet version %d, only v4 is supported", firstByte(body))
			}
			h := sha1.New()
			h.Write([]byte{0x99, byte(n >> 8), byte(n)})
			h.Write(body)
			out = append(out, strings.ToUpper(hex.EncodeToString(h.Sum(nil))))
		}
		b = b[hl+n:]
	}
	return out, nil
}

func firstByte(b []byte) int {
	if len(b) == 0 {
		return -1
	}
	return int(b[0])
}

// pgpKeyringFingerprints accepts a keyring as pacman-key reads it, binary or ASCII-armored
// (BlackArch ships blackarch.gpg armored), and returns its primary key fingerprints.
func pgpKeyringFingerprints(b []byte) ([]string, error) {
	if !bytes.HasPrefix(bytes.TrimSpace(b), []byte("-----BEGIN PGP PUBLIC KEY BLOCK-----")) {
		return pgpPrimaryFingerprints(b)
	}
	var out []string
	blocks := 0
	in, body := false, false
	var enc strings.Builder
	for _, l := range strings.Split(string(b), "\n") {
		l = strings.TrimRight(l, "\r")
		switch {
		case l == "-----BEGIN PGP PUBLIC KEY BLOCK-----":
			in, body = true, false
			enc.Reset()
		case l == "-----END PGP PUBLIC KEY BLOCK-----":
			if !in {
				return nil, errors.New("armor END without BEGIN")
			}
			raw, err := base64.StdEncoding.DecodeString(enc.String())
			if err != nil {
				return nil, fmt.Errorf("armor: %v", err)
			}
			fp, err := pgpPrimaryFingerprints(raw)
			if err != nil {
				return nil, err
			}
			out = append(out, fp...)
			blocks++
			in = false
		case !in:
		case !body:
			// Armor headers ("Version: …") end at the first blank line.
			if strings.TrimSpace(l) == "" {
				body = true
			}
		case strings.HasPrefix(l, "="): // the CRC-24 line
		default:
			enc.WriteString(strings.TrimSpace(l))
		}
	}
	if blocks == 0 || in {
		return nil, errors.New("armor: no complete PUBLIC KEY BLOCK")
	}
	return out, nil
}
