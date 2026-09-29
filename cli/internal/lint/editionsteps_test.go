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

const (
	esProfile = "iso-profiles/river/"
	esEdition = esProfile + profileEditions + "/river.json"
	esDriver  = esProfile + profileDriver
)

const esDriverBody = `#!/bin/sh
# for step in \ (a comment does not start the list)
for step in \
	00-preflight \
	30-keys \
	90-export
do
	sh "$LIBDIR/$step.sh"
done
`

// editionStepsFixture is a tree that passes: a profile shipping 00, 30 and 90, all listed; a
// cloud-only step in installer/lib, allow-listed; a helper without a number, ignored.
func editionStepsFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	step := "#!/bin/sh\n"
	writeTree(t, dir, map[string]string{
		"installer/lib/00-preflight.sh":                  step,
		"installer/lib/30-keys.sh":                       step,
		"installer/lib/12-cloud-disk.sh":                 step,
		"installer/lib/hwplan.sh":                        step,
		esProfile + profileStepsDir + "/00-preflight.sh": step,
		esProfile + profileStepsDir + "/30-keys.sh":      step,
		esProfile + profileStepsDir + "/90-export.sh":    step,
		esProfile + profileStepsDir + "/hwplan.sh":       step,
		esEdition:         `{"id": "river", "steps": ["00-preflight", "30-keys", "90-export"]}`,
		esDriver:          esDriverBody,
		editionStepsAllow: "# comment\n12-cloud-disk | cloud images only\n",
	})
	return dir
}

func runEditionSteps(t *testing.T, dir string) (string, error) {
	t.Helper()
	var errb, outb bytes.Buffer
	err := EditionSteps(context.Background(), dir, &errb, &outb)
	return errb.String() + outb.String(), err
}

func TestEditionStepsClean(t *testing.T) {
	out, err := runEditionSteps(t, editionStepsFixture(t))
	want := "lint-edition-steps: 3 step(s) in installer/lib, 1 profile(s), 2 step list(s): every shipped step is listed, every listed step shipped, 1 allow-listed ✓\n"
	if err != nil || out != want {
		t.Fatalf("clean tree: %v\n%s", err, out)
	}
}

func TestEditionStepsFailures(t *testing.T) {
	unlisted := esProfile + profileStepsDir + "/30-keys.sh is in no step list of iso-profiles/river: it never runs"
	cases := []struct {
		name   string
		break_ func(t *testing.T, dir string)
		want   string
	}{
		// The regression this lint exists for: the step ships, and no list runs it.
		{"a shipped step in no list", func(t *testing.T, d string) {
			editFixture(t, d, esEdition, `"30-keys", `, "")
			editFixture(t, d, esDriver, "\t30-keys \\\n", "")
		}, unlisted},
		{"a listed step not shipped", func(t *testing.T, d string) {
			editFixture(t, d, esEdition, `"90-export"`, `"90-export", "95-gone"`)
		}, esEdition + " names step 95-gone, which iso-profiles/river/" + profileStepsDir + " does not ship"},
		{"a driver step not shipped", func(t *testing.T, d string) {
			editFixture(t, d, esDriver, "\t90-export\n", "\t90-export \\\n\t95-gone\n")
		}, esDriver + " names step 95-gone"},
		{"a library step nobody ships, not allowed", func(t *testing.T, d string) {
			writeTree(t, d, map[string]string{"installer/lib/78-cloud-target.sh": "#!/bin/sh\n"})
		}, "installer/lib/78-cloud-target.sh is shipped and run by no in-tree profile"},
		{"an allow entry for a gone step", func(t *testing.T, d string) {
			editFixture(t, d, editionStepsAllow, "12-cloud-disk | cloud images only\n", "12-cloud-disk | cloud images only\n13-gone | x\n")
		}, editionStepsAllow + ": 13-gone is not a step"},
		{"an allow entry a list now runs", func(t *testing.T, d string) {
			editFixture(t, d, editionStepsAllow, "12-cloud-disk | cloud images only\n", "12-cloud-disk | cloud images only\n30-keys | stale\n")
		}, editionStepsAllow + ": 30-keys is run by a step list now: delete the entry"},
		{"a profile with steps and no list", func(t *testing.T, d string) {
			for _, f := range []string{esEdition, esDriver} {
				if err := os.Remove(filepath.Join(d, f)); err != nil {
					t.Fatal(err)
				}
			}
		}, "iso-profiles/river ships install steps but has no step list"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := editionStepsFixture(t)
			c.break_(t, dir)
			out, err := runEditionSteps(t, dir)
			if err == nil || !strings.Contains(out, "lint-edition-steps: "+c.want) {
				t.Fatalf("want failure %q, got %v:\n%s", c.want, err, out)
			}
		})
	}
}

// A step list the lint cannot read, and an allow-list entry without a reason, fail as errors,
// never as "names nothing"; so does a tree with nothing to examine.
func TestEditionStepsRefusesNothing(t *testing.T) {
	for name, c := range map[string]struct {
		f, old, new, want string
	}{
		"edition not JSON":      {esEdition, `{"id"`, `{id`, "river.json"},
		"edition with no steps": {esEdition, `"steps": ["00-preflight", "30-keys", "90-export"]`, `"steps": []`, `has no "steps"`},
		"driver with no list":   {esDriver, "for step in \\\n", "for step in 00-preflight; do :; done\n", "has no `for step in \\` list"},
		"allow without reason":  {editionStepsAllow, "12-cloud-disk | cloud images only", "12-cloud-disk |", `want "NN-step | reason"`},
	} {
		t.Run(name, func(t *testing.T) {
			dir := editionStepsFixture(t)
			editFixture(t, dir, c.f, c.old, c.new)
			out, err := runEditionSteps(t, dir)
			if err == nil || !strings.Contains(out+err.Error(), c.want) {
				t.Fatalf("want %q, got %v:\n%s", c.want, err, out)
			}
		})
	}
	dir := editionStepsFixture(t)
	if err := os.RemoveAll(filepath.Join(dir, "iso-profiles")); err != nil {
		t.Fatal(err)
	}
	if _, err := runEditionSteps(t, dir); err == nil || !strings.Contains(err.Error(), "no profile ships installer steps") {
		t.Fatalf("no profiles must fail as examining nothing, got %v", err)
	}
	if err := os.RemoveAll(filepath.Join(dir, "installer/lib")); err != nil {
		t.Fatal(err)
	}
	if _, err := runEditionSteps(t, dir); err == nil || !strings.Contains(err.Error(), "no NN-*.sh steps") {
		t.Fatalf("no installer/lib must fail, got %v", err)
	}
}

func TestDriverSteps(t *testing.T) {
	if got, want := driverSteps(esDriverBody), []string{"00-preflight", "30-keys", "90-export"}; !slices.Equal(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
	if got := driverSteps("for step in \\\n\ta \\\n\tb\n"); got != nil {
		t.Fatalf("a list never closed by do must read as none, got %q", got)
	}
}

// The real tree passes: every step the in-tree profile ships is run by its edition or
// runink-install (35-pacman-keyring was not, from #45 until this lint).
func TestEditionStepsRepo(t *testing.T) {
	repo, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(repo, installerSrc)); err != nil {
		t.Skip("not in a river checkout")
	}
	if out, err := runEditionSteps(t, repo); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
}
