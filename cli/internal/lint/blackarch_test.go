// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

package lint

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const baRepo = "../../.."

// baFixture copies the real inputs of the blackarch lint into a temp tree.
func baFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, rel := range []string{baPin, baMirrorlist, baConf, baInstaller,
		baKeyrings + "/blackarch.gpg", baKeyrings + "/blackarch-trusted", baKeyrings + "/blackarch-revoked",
		"iso-profiles/river/Packages-Root", "iso-profiles/river/Packages-Live"} {
		b, err := os.ReadFile(filepath.Join(baRepo, rel))
		if err != nil {
			t.Fatal(err)
		}
		lbWrite(t, dir, rel, string(b))
	}
	return dir
}

func baRun(t *testing.T, dir string) (string, error) {
	t.Helper()
	var errb, outb bytes.Buffer
	err := Blackarch(context.Background(), dir, &errb, &outb)
	return errb.String() + outb.String(), err
}

func baEdit(t *testing.T, dir, rel string, f func(string) string) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, rel))
	if err != nil {
		t.Fatal(err)
	}
	lbWrite(t, dir, rel, f(string(b)))
}

func TestBlackarchTreePasses(t *testing.T) {
	out, err := baRun(t, baRepo)
	if err != nil {
		t.Fatalf("the tree fails: %v\n%s", err, out)
	}
	if !strings.Contains(out, "4 trusted keys") {
		t.Fatalf("unexpected output: %s", out)
	}
}

// The fingerprints come from the key packets: the vendored keyring holds the repository signer.
func TestBlackarchFingerprintsFromPackets(t *testing.T) {
	b, err := os.ReadFile(filepath.Join(baRepo, baKeyrings, "blackarch.gpg"))
	if err != nil {
		t.Fatal(err)
	}
	fp, err := pgpKeyringFingerprints(b)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(fp, "F9A6E68A711354D84A9B91637533BAFE69A25079") {
		t.Fatalf("signer not among %v", fp)
	}
	if _, err := pgpKeyringFingerprints([]byte("-----BEGIN PGP PUBLIC KEY BLOCK-----\n\nnotbase64!\n")); err == nil {
		t.Fatal("a broken armor block was accepted")
	}
}

func TestBlackarchFailures(t *testing.T) {
	cases := []struct {
		name, rel, want string
		edit            func(string) string
	}{
		{"keyring bytes changed", baKeyrings + "/blackarch.gpg", "sha256",
			func(s string) string { return s + "x" }},
		{"trusted list changed", baKeyrings + "/blackarch-trusted", "blackarch-trusted lists",
			func(s string) string { return s + "CBA3C7D4798912702DCF568E67D8BDF42AD93F4E:4:\n" }},
		{"not last", baConf, "LAST section",
			func(s string) string { return s + "\n[late]\nServer = https://example.org/$repo\n" }},
		{"siglevel weakened", baConf, `"SigLevel = Required"`,
			func(s string) string {
				return strings.Replace(s, "[blackarch]\nSigLevel = Required", "[blackarch]\nSigLevel = Optional TrustAll", 1)
			}},
		{"install allowed at build", baConf, `"Usage = Sync Search"`,
			func(s string) string { return strings.Replace(s, "Usage = Sync Search\n", "", 1) }},
		{"plain http mirror", baMirrorlist, "must be an https://",
			func(s string) string { return s + "Server = http://example.org/blackarch/$repo/os/$arch\n" }},
		{"too few mirrors", baMirrorlist, "want at least 3",
			func(s string) string {
				var keep []string
				n := 0
				for _, l := range strings.Split(s, "\n") {
					if strings.HasPrefix(l, "Server") {
						if n++; n > 2 {
							continue
						}
					}
					keep = append(keep, l)
				}
				return strings.Join(keep, "\n")
			}},
		{"tool in a package list", "iso-profiles/river/Packages-Root", "names blackarch-recon",
			func(s string) string { return s + "blackarch-recon\n" }},
		{"installer never populates", baInstaller, "--populate blackarch",
			func(s string) string { return strings.ReplaceAll(s, "--populate blackarch", "--populate nothing") }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := baFixture(t)
			baEdit(t, dir, c.rel, c.edit)
			out, err := baRun(t, dir)
			if err == nil || !strings.Contains(out, c.want) {
				t.Fatalf("want failure mentioning %q, got err=%v\n%s", c.want, err, out)
			}
		})
	}
}

// A guard that examined nothing must fail: an empty tree is not a pass.
func TestBlackarchEmptyTreeFails(t *testing.T) {
	if out, err := baRun(t, t.TempDir()); err == nil {
		t.Fatalf("empty tree passed:\n%s", out)
	}
}
