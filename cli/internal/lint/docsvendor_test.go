// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

package lint

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/org-runink/river/pkg/pipe"
)

// docsVendorFixture writes a vendored website and points the pins at it.
func docsVendorFixture(t *testing.T) string {
	t.Helper()
	dir := fixtureRepo(t)
	files := map[string]string{
		"website/go.mod":              "module example.org/site\n\ngo 1.24\n\nrequire github.com/imfing/hextra v9.9.9\n",
		"website/go.sum":              "github.com/imfing/hextra v9.9.9 h1:abc=\ngithub.com/imfing/hextra v9.9.9/go.mod h1:def=\n",
		"website/_vendor/modules.txt": "# github.com/imfing/hextra v9.9.9\n",

		"website/_vendor/github.com/imfing/hextra/LICENSE":       "MIT\n",
		"website/assets/lib/flexsearch/flexsearch.bundle.min.js": "/* flexsearch */\n",
	}
	for i := 0; i < 110; i++ {
		// Names that sort differently per directory and as whole paths ("a.b" < "a/b").
		files[fmt.Sprintf("website/_vendor/github.com/imfing/hextra/layouts/p%03d.html", i)] = fmt.Sprintf("<p>%d</p>\n", i)
	}
	files["website/_vendor/github.com/imfing/hextra/layouts.txt"] = "sorts before layouts/\n"
	writeTree(t, dir, files)

	n, digest, err := vendorTreeDigest(filepath.Join(dir, "website/_vendor"))
	if err != nil || n != 113 {
		t.Fatalf("vendorTreeDigest: %d files, %v", n, err)
	}
	flex, err := sha256OfFile(filepath.Join(dir, "website/assets/lib/flexsearch/flexsearch.bundle.min.js"))
	if err != nil {
		t.Fatal(err)
	}
	saved := [3]string{hextraVersion, hextraTreeSHA256, flexsearchSHA256}
	t.Cleanup(func() { hextraVersion, hextraTreeSHA256, flexsearchSHA256 = saved[0], saved[1], saved[2] })
	hextraVersion, hextraTreeSHA256, flexsearchSHA256 = "v9.9.9", digest, flex
	return dir
}

func runDocsVendor(t *testing.T, dir string) (string, error) {
	t.Helper()
	var errb, outb bytes.Buffer
	err := DocsVendor(context.Background(), dir, &errb, &outb)
	return errb.String() + outb.String(), err
}

func TestDocsVendorClean(t *testing.T) {
	dir := docsVendorFixture(t)
	out, err := runDocsVendor(t, dir)
	if err != nil || out != "lint-docs-vendor: OK (Hextra v9.9.9, 113 files; FlexSearch pinned)\n" {
		t.Fatalf("clean tree: %v\n%s", err, out)
	}
}

// The digest is the one the script computed, so a pin can still be checked by hand.
func TestDocsVendorDigestIsTheShellOne(t *testing.T) {
	for _, tool := range []string{"find", "sort", "xargs", "sha256sum", "cut"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skip(tool + " not installed")
		}
	}
	dir := docsVendorFixture(t)
	vendor := filepath.Join(dir, "website/_vendor")
	out, err := pipe.Output(context.Background(), nil,
		pipe.Command{Name: "find", Args: []string{".", "-type", "f", "-print0"}, Dir: vendor},
		pipe.Command{Name: "sort", Args: []string{"-z"}, Env: []string{"LC_ALL=C"}},
		pipe.Command{Name: "xargs", Args: []string{"-0", "sha256sum"}, Dir: vendor},
		pipe.Cmd("sha256sum"),
		pipe.Cmd("cut", "-d ", "-f1"))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(out)); got != hextraTreeSHA256 {
		t.Fatalf("shell digest %s != Go digest %s", got, hextraTreeSHA256)
	}
}

func TestDocsVendorFailures(t *testing.T) {
	cases := []struct {
		name, want string
		break_     func(dir string) error
	}{
		{"go.mod bumped", "website/go.mod does not require github.com/imfing/hextra v9.9.9", func(d string) error {
			return os.WriteFile(filepath.Join(d, "website/go.mod"), []byte("require github.com/imfing/hextra v9.9.10\n"), 0o644)
		}},
		{"go.sum missing", "website/go.sum has no hash", func(d string) error { return os.Remove(filepath.Join(d, "website/go.sum")) }},
		{"modules.txt stale", "website/_vendor/modules.txt is not", func(d string) error {
			return os.WriteFile(filepath.Join(d, "website/_vendor/modules.txt"), []byte("# github.com/imfing/hextra v9.9.8\n"), 0o644)
		}},
		{"licence dropped", "the vendored Hextra has no LICENSE", func(d string) error {
			return os.Remove(filepath.Join(d, "website/_vendor/github.com/imfing/hextra/LICENSE"))
		}},
		{"vendored file edited", "website/_vendor digest", func(d string) error {
			return os.WriteFile(filepath.Join(d, "website/_vendor/github.com/imfing/hextra/layouts/p007.html"), []byte("<p>edited</p>\n"), 0o644)
		}},
		{"tree emptied", "website/_vendor has only 0 files (expected >= 100)", func(d string) error {
			return os.RemoveAll(filepath.Join(d, "website/_vendor"))
		}},
		{"flexsearch edited", "flexsearch.bundle.min.js sha256", func(d string) error {
			return os.WriteFile(filepath.Join(d, "website/assets/lib/flexsearch/flexsearch.bundle.min.js"), []byte("x"), 0o644)
		}},
		{"flexsearch missing", "flexsearch.bundle.min.js is missing", func(d string) error {
			return os.Remove(filepath.Join(d, "website/assets/lib/flexsearch/flexsearch.bundle.min.js"))
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := docsVendorFixture(t)
			if err := c.break_(dir); err != nil {
				t.Fatal(err)
			}
			out, err := runDocsVendor(t, dir)
			if err == nil {
				t.Fatalf("passed:\n%s", out)
			}
			if !strings.Contains(out, "lint-docs-vendor: ") || !strings.Contains(out, c.want) {
				t.Fatalf("want %q in:\n%s", c.want, out)
			}
		})
	}
}
