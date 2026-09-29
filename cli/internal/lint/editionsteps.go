// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

package lint

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// edition-steps — an installer step nobody runs is a fix that never ships.
//
// A step runs only when a step LIST names it: the edition descriptors the graphical installer
// reads (<profile>/live-overlay/usr/share/river/installer/editions/*.json, "steps") and the
// text-mode driver's `for step in` list (<profile>/root-overlay/usr/local/bin/runink-install).
// Neither complains about a step file it does not name. 35-pacman-keyring was added in #45
// to populate the installed system's pacman keyring and was never put in any list, so every
// installed system shipped with no keyring while the fix sat in the tree, reported as done.
// The graphical installer also SKIPS a listed step its image does not carry, silently.
//
// So, for every in-tree profile:
//
//   - every step its overlay ships (root-overlay/usr/local/lib/runink-install/NN-*.sh) is
//     named by one of that profile's step lists;
//   - every step a list names is shipped by that profile's overlay;
//
// and every step in installer/lib that no in-tree profile ships (a downstream or cloud-image
// step) is in scripts/edition-steps.allow with its reason. An allow entry must name an
// existing step and must still be needed; a stale one fails, so the list only says true things.
//
// Usage: river lint edition-steps

const (
	editionStepsAllow = "scripts/edition-steps.allow"
	profileStepsDir   = "root-overlay/usr/local/lib/runink-install"
	profileEditions   = "live-overlay/usr/share/river/installer/editions"
	profileDriver     = "root-overlay/usr/local/bin/runink-install"
)

// stepFile is an install step's file name: two digits, a dash, a name, .sh. Helpers the steps
// source (hwplan.sh, memtune.sh, pair-menu.sh) have no number and are not steps.
var stepFile = regexp.MustCompile(`^([0-9]{2}-[a-z0-9-]+)\.sh$`)

func init() { extraLints = append(extraLints, editionStepsCmd) }

func editionStepsCmd(v *viper.Viper) *cobra.Command {
	return &cobra.Command{
		Use:   "edition-steps",
		Short: "every installer step is run by a step list (an edition or runink-install), or allow-listed with a reason",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return EditionSteps(cmd.Context(), v.GetString("repo"), cmd.ErrOrStderr(), cmd.OutOrStdout())
		},
	}
}

// EditionSteps runs the lint over repo. Findings go to outw.
func EditionSteps(_ context.Context, repo string, _, outw io.Writer) error {
	const name = "lint-edition-steps"
	r := &report{name: name, w: outw}

	lib, err := stepsIn(filepath.Join(repo, installerSrc))
	if err != nil || len(lib) == 0 {
		return fmt.Errorf("%s: no NN-*.sh steps in %s (this check would otherwise pass having examined nothing)", name, installerSrc)
	}
	allow, err := readStepAllow(filepath.Join(repo, editionStepsAllow))
	if err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	used := map[string]bool{}

	profiles, _ := filepath.Glob(filepath.Join(repo, "iso-profiles", "*"))
	shipped := map[string]bool{} // a step some in-tree profile's overlay carries
	nProfiles, nLists := 0, 0
	for _, p := range profiles {
		ovl, err := stepsIn(filepath.Join(p, profileStepsDir))
		if err != nil || len(ovl) == 0 {
			continue
		}
		rel, _ := filepath.Rel(repo, p)
		rel = filepath.ToSlash(rel)
		nProfiles++
		lists, err := profileStepLists(p)
		if err != nil {
			r.fail("%s: %v", rel, err)
			continue
		}
		if len(lists) == 0 {
			r.fail("%s ships install steps but has no step list (no edition descriptor, no runink-install)", rel)
			continue
		}
		nLists += len(lists)
		named := map[string]bool{}
		for _, l := range lists {
			for _, s := range l.steps {
				named[s] = true
				if !slices.Contains(ovl, s) {
					r.fail("%s names step %s, which %s/%s does not ship (the installer skips or aborts on it)", l.src, s, rel, profileStepsDir)
				}
			}
		}
		for _, s := range ovl {
			shipped[s] = true
			if named[s] {
				continue
			}
			if _, ok := allow[s]; ok {
				used[s] = true
				continue
			}
			r.fail("%s/%s/%s.sh is in no step list of %s: it never runs (add it to the edition and runink-install in order, or to %s with a reason)",
				rel, profileStepsDir, s, rel, editionStepsAllow)
		}
	}
	if nProfiles == 0 {
		return errors.New(name + ": no profile ships installer steps under iso-profiles/*\n" +
			"  (this check would otherwise pass having examined nothing)")
	}

	for _, s := range lib {
		if shipped[s] {
			continue
		}
		if _, ok := allow[s]; ok {
			used[s] = true
			continue
		}
		r.fail("%s/%s.sh is shipped and run by no in-tree profile: list it in a profile, or add it to %s with the reason (a downstream or cloud-image step)",
			installerSrc, s, editionStepsAllow)
	}

	names := make([]string, 0, len(allow))
	for s := range allow {
		names = append(names, s)
	}
	sort.Strings(names)
	for _, s := range names {
		switch {
		case !slices.Contains(lib, s) && !shipped[s]:
			r.fail("%s: %s is not a step in %s or any profile: delete the entry", editionStepsAllow, s, installerSrc)
		case !used[s]:
			r.fail("%s: %s is run by a step list now: delete the entry", editionStepsAllow, s)
		}
	}

	if err := r.err(); err != nil {
		return err
	}
	fmt.Fprintf(outw, "%s: %d step(s) in %s, %d profile(s), %d step list(s): every shipped step is listed, every listed step shipped, %d allow-listed ✓\n",
		name, len(lib), installerSrc, nProfiles, nLists, len(allow))
	return nil
}

// stepsIn is the step names (without .sh) in dir, sorted; a missing dir is an error.
func stepsIn(dir string) ([]string, error) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range ents {
		if m := stepFile.FindStringSubmatch(e.Name()); m != nil && e.Type().IsRegular() {
			out = append(out, m[1])
		}
	}
	return out, nil
}

type stepList struct {
	src   string // the file, as printed
	steps []string
}

// profileStepLists is every step list profile p carries: each edition descriptor's "steps",
// and the runink-install driver's `for step in` list. A list that exists but cannot be read,
// or reads as empty, is an error: an unparsable list would otherwise pass as "names nothing".
func profileStepLists(p string) ([]stepList, error) {
	base := filepath.Base(filepath.Dir(p)) + "/" + filepath.Base(p)
	var out []stepList
	eds, _ := filepath.Glob(filepath.Join(p, profileEditions, "*.json"))
	sort.Strings(eds)
	for _, f := range eds {
		src := base + "/" + profileEditions + "/" + filepath.Base(f)
		b, err := os.ReadFile(f)
		if err != nil {
			return nil, err
		}
		var e struct {
			Steps []string `json:"steps"`
		}
		if err := json.Unmarshal(b, &e); err != nil {
			return nil, fmt.Errorf("%s: %w", src, err)
		}
		if len(e.Steps) == 0 {
			return nil, fmt.Errorf("%s has no \"steps\"", src)
		}
		out = append(out, stepList{src, e.Steps})
	}
	drv := filepath.Join(p, profileDriver)
	if b, err := os.ReadFile(drv); err == nil {
		src := base + "/" + profileDriver
		steps := driverSteps(string(b))
		if len(steps) == 0 {
			return nil, fmt.Errorf("%s has no `for step in \\` list", src)
		}
		out = append(out, stepList{src, steps})
	}
	return out, nil
}

// driverSteps is runink-install's step list: the words after a line "for step in \" up to the
// line "do".
func driverSteps(script string) []string {
	var out []string
	on := false
	for _, line := range strings.Split(script, "\n") {
		t := strings.TrimSpace(line)
		if !on {
			on = t == `for step in \`
			continue
		}
		if t == "do" {
			return out
		}
		out = append(out, strings.Fields(strings.TrimSuffix(t, `\`))...)
	}
	return nil // no list, or one never closed by "do"
}

// readStepAllow reads the allow-list: "step | reason" per line, # comments. A missing reason
// or a repeated step is an error.
func readStepAllow(f string) (map[string]string, error) {
	b, err := os.ReadFile(f)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for i, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		step, reason, ok := strings.Cut(line, "|")
		step, reason = strings.TrimSpace(step), strings.TrimSpace(reason)
		if !ok || reason == "" || !stepFile.MatchString(step+".sh") {
			return nil, fmt.Errorf("%s:%d: want \"NN-step | reason\", got %q", editionStepsAllow, i+1, line)
		}
		if _, dup := out[step]; dup {
			return nil, fmt.Errorf("%s:%d: %s listed twice", editionStepsAllow, i+1, step)
		}
		out[step] = reason
	}
	return out, nil
}
