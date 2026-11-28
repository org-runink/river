// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

package isocmd

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"
)

// Profile is what `river iso build` reads from a profile. From profile.yaml: the live session
// (user, password, services) and whether there is a `livefs:` key (two layers, or one). The
// package lists on the Arch path are the profile's allow-lists, installed exactly:
//
//	rootfs.img  Packages-Root.arch
//	livefs.img  Packages-Live.arch
//
// (profile.yaml's own package lists name Artix packages and stay for the Artix path.)
type Profile struct {
	Dir string

	LiveSession struct {
		User         string   `yaml:"user"`
		Password     string   `yaml:"password"`
		Services     []string `yaml:"services"`
		UserServices []string `yaml:"user-services"`
	} `yaml:"live-session"`
	// Livefs is nil without a `livefs:` key: then the image has one layer, and the live
	// session is configured on the rootfs itself (artools does the same).
	Livefs *struct {
		Packages []string `yaml:"packages"`
	} `yaml:"livefs"`

	RootPackages []string // Packages-Root.arch
	LivePackages []string // Packages-Live.arch
}

// Files the Arch builder reads beside profile.yaml.
const (
	rootManifest = "Packages-Root.arch"
	liveManifest = "Packages-Live.arch"
)

// The packages the builder itself relies on: their absence fails before anything is installed.
var (
	requiredRoot = []string{"linux-runink", "mkinitcpio", "grub", "intel-ucode", "amd-ucode"}
	requiredLive = []string{"runink-live-initcpio"}
)

// LoadProfile reads <dir>/profile.yaml and the two Arch manifests.
func LoadProfile(dir string) (*Profile, error) {
	b, err := os.ReadFile(filepath.Join(dir, "profile.yaml"))
	if err != nil {
		return nil, err
	}
	p, err := parseProfile(dir, b)
	if err != nil {
		return nil, err
	}
	if p.RootPackages, err = readManifest(filepath.Join(dir, rootManifest)); err != nil {
		return nil, err
	}
	if p.HasLivefs() {
		if p.LivePackages, err = readManifest(filepath.Join(dir, liveManifest)); err != nil {
			return nil, err
		}
	}
	return p, p.check()
}

func parseProfile(dir string, b []byte) (*Profile, error) {
	p := &Profile{Dir: dir}
	if err := yaml.Unmarshal(b, p); err != nil {
		return nil, fmt.Errorf("%s/profile.yaml: %w", dir, err)
	}
	if p.LiveSession.User == "" || p.LiveSession.Password == "" {
		return nil, fmt.Errorf("%s/profile.yaml: live-session needs user and password", dir)
	}
	return p, nil
}

// check: both lists non-empty, the builder's own packages present (the live initramfs hooks
// go wherever the live system is: livefs with two layers, else rootfs), no name in both.
func (p *Profile) check() error {
	if len(p.RootPackages) == 0 {
		return fmt.Errorf("%s/%s lists no package", p.Dir, rootManifest)
	}
	if p.HasLivefs() && len(p.LivePackages) == 0 {
		return fmt.Errorf("profile.yaml has a livefs: key but %s/%s lists no package", p.Dir, liveManifest)
	}
	need := map[string][]string{rootManifest: requiredRoot}
	live := p.LivePackages
	if p.HasLivefs() {
		need[liveManifest] = requiredLive
	} else {
		need[rootManifest] = append(slices.Clone(requiredRoot), requiredLive...)
		live = p.RootPackages
	}
	for file, names := range need {
		list := p.RootPackages
		if file == liveManifest {
			list = live
		}
		var missing []string
		for _, n := range names {
			if !slices.Contains(list, n) {
				missing = append(missing, n)
			}
		}
		if len(missing) > 0 {
			return fmt.Errorf("%s/%s must list %s (the ISO builder needs it)", p.Dir, file, strings.Join(missing, " "))
		}
	}
	for _, n := range p.LivePackages {
		if slices.Contains(p.RootPackages, n) {
			return fmt.Errorf("%s is in both %s and %s", n, rootManifest, liveManifest)
		}
	}
	return nil
}

// HasLivefs reports whether the image has the second, live-only layer.
func (p *Profile) HasLivefs() bool { return p.Livefs != nil }

// readManifest returns the package names of a Packages-* file, sorted and without
// duplicates (one or more names per line; # comments and blank lines ignored).
func readManifest(path string) ([]string, error) {
	names, err := readNames(path)
	if err != nil {
		return nil, err
	}
	slices.Sort(names)
	return slices.Compact(names), nil
}

func readNames(path string) ([]string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, l := range strings.Split(string(b), "\n") {
		if i := strings.IndexByte(l, '#'); i >= 0 {
			l = l[:i]
		}
		out = append(out, strings.Fields(l)...)
	}
	return out, nil
}

// readForbidden returns every package name in a forbidden.closure file. A missing file is
// no list.
func readForbidden(path string) ([]string, error) {
	n, err := readNames(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	return n, err
}
