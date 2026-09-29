// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

package wizard

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
)

// EditionsDir is where a live medium describes what it can install: one JSON file per edition
// (docs/INSTALL.md, "Edition descriptors"). The installer knows no edition by name; a
// distribution built from Runink River ships its own descriptors in its profile.
const EditionsDir = "/usr/share/river/installer/editions"

// Edition is one installable image, as its descriptor declares it.
type Edition struct {
	Version     int               `json:"version"`
	ID          string            `json:"id"`
	Title       string            `json:"title"`
	Description map[string]string `json:"description,omitempty"` // by language; "en" is the fallback
	// MediumLabel is the ISO volume label of the image. The running image is the edition whose
	// label is on the kernel command line; another edition is offered when a volume with its
	// label is present (a stick that carries both images).
	MediumLabel string `json:"medium_label"`
	// PlanProfile is river-plan's --profile (server | workstation); PlanModels plans the
	// model tiers from the image's models manifest when it has one.
	PlanProfile string `json:"plan_profile"`
	PlanModels  bool   `json:"plan_models"`
	// Steps are the installer steps (installer/lib), in order.
	Steps []string `json:"steps"`
	// Roles, when present, are offered on the "What to install" screen; the choice reaches the
	// node as /etc/runink/role and the first-boot hooks as RIVER_ROLE.
	Roles       []Role `json:"roles,omitempty"`
	DefaultRole string `json:"default_role,omitempty"`
	// FirstbootUI: the installed machine starts with the graphical first boot.
	FirstbootUI bool `json:"firstboot_ui"`
	// ModelsRequired: the install fails unless the medium's model payload is unpacked (the
	// account screen then requires the medium passphrase; 72-models-payload gets
	// RUNINK_MODELS_REQUIRED=1). A downstream edition whose nodes must carry their models sets it.
	ModelsRequired bool `json:"models_required,omitempty"`
	// Defaults for the name-and-admin screen.
	Hostname  string `json:"hostname"`
	AdminUser string `json:"admin_user"`
	// Icon (optional): the edition's own mark, an absolute path to a .svg, .png or .ico under
	// /usr/share; the UI uses it as its favicon and header mark (served at /edition-icon).
	Icon string `json:"icon,omitempty"`

	Self    bool `json:"self"`    // the image that is running
	Present bool `json:"present"` // on the boot medium (always true for Self)
}

// Role is one kind of node an edition can install.
type Role struct {
	ID          string            `json:"id"`
	Title       map[string]string `json:"title"`
	Description map[string]string `json:"description,omitempty"`
}

var (
	idRe    = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)
	labelRe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,32}$`)
	stepRe  = regexp.MustCompile(`^[0-9]{2}-[a-z0-9-]{1,40}$`)
)

// Validate checks a descriptor; the installer refuses one it cannot trust to be complete.
func (e *Edition) Validate() error {
	switch {
	case e.Version != 1:
		return fmt.Errorf("version %d (want 1)", e.Version)
	case !idRe.MatchString(e.ID):
		return fmt.Errorf("bad id %q", e.ID)
	case e.Title == "" || len(e.Title) > 60:
		return fmt.Errorf("bad title")
	case !labelRe.MatchString(e.MediumLabel):
		return fmt.Errorf("bad medium_label %q", e.MediumLabel)
	case e.PlanProfile != "server" && e.PlanProfile != "workstation":
		return fmt.Errorf("plan_profile %q (server | workstation)", e.PlanProfile)
	case len(e.Steps) == 0:
		return fmt.Errorf("no steps")
	}
	for _, s := range e.Steps {
		if !stepRe.MatchString(s) {
			return fmt.Errorf("bad step %q", s)
		}
	}
	if validHostname(e.Hostname) != "" {
		return fmt.Errorf("bad hostname %q", e.Hostname)
	}
	if validUsername(e.AdminUser) != "" {
		return fmt.Errorf("bad admin_user %q", e.AdminUser)
	}
	if e.ModelsRequired && !slices.Contains(e.Steps, "72-models-payload") {
		return fmt.Errorf("models_required without the 72-models-payload step")
	}
	if e.Icon != "" && !iconOK(e.Icon) {
		return fmt.Errorf("bad icon %q (an absolute .svg, .png or .ico under /usr/share)", e.Icon)
	}
	seen := map[string]bool{}
	for _, r := range e.Roles {
		if !idRe.MatchString(r.ID) || seen[r.ID] || r.Title["en"] == "" {
			return fmt.Errorf("bad role %q", r.ID)
		}
		seen[r.ID] = true
	}
	if len(e.Roles) > 0 && !seen[e.DefaultRole] {
		return fmt.Errorf("default_role %q is not a role", e.DefaultRole)
	}
	return nil
}

// LoadEditions reads every descriptor in dir. selfLabel is the running image's volume label
// (label= on the kernel command line); present reports whether a volume label exists on this
// machine. A descriptor that does not validate is skipped with a message in skipped.
func LoadEditions(dir, selfLabel string, present func(label string) bool) (eds []Edition, skipped []string) {
	return LoadEditionsFor(dir, selfLabel, "", present)
}

// LoadEditionsFor is LoadEditions for a medium that may carry several editions under ONE
// volume label, each booted from its own menu entry: selfID (river.edition= on the kernel
// command line) then names the running edition among the descriptors that carry selfLabel.
// Without it, the label alone decides, as on a medium with one edition. The other editions
// with the same label are present (they are on this very medium).
func LoadEditionsFor(dir, selfLabel, selfID string, present func(label string) bool) (eds []Edition, skipped []string) {
	files, _ := filepath.Glob(filepath.Join(dir, "*.json"))
	sort.Strings(files)
	for _, f := range files {
		b, err := os.ReadFile(f) // #nosec G304 -- the image's own descriptors
		if err != nil {
			skipped = append(skipped, fmt.Sprintf("%s: %v", filepath.Base(f), err))
			continue
		}
		var e Edition
		dec := json.NewDecoder(strings.NewReader(string(b)))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&e); err != nil {
			skipped = append(skipped, fmt.Sprintf("%s: %v", filepath.Base(f), err))
			continue
		}
		if err := e.Validate(); err != nil {
			skipped = append(skipped, fmt.Sprintf("%s: %v", filepath.Base(f), err))
			continue
		}
		e.Self = e.MediumLabel == selfLabel && (selfID == "" || e.ID == selfID)
		e.Present = e.Self || (selfLabel != "" && e.MediumLabel == selfLabel) || (present != nil && present(e.MediumLabel))
		eds = append(eds, e)
	}
	return eds, skipped
}

// selfEdition returns the running image's edition, or a minimal fallback that installs with
// the steps every image has (so a medium with a broken descriptor still installs).
func selfEdition(eds []Edition) Edition {
	for _, e := range eds {
		if e.Self {
			return e
		}
	}
	if len(eds) == 1 {
		e := eds[0]
		e.Self, e.Present = true, true
		return e
	}
	return Edition{Version: 1, ID: "river", Title: "Runink River", MediumLabel: "RIVER", PlanProfile: "server",
		Steps: []string{"00-preflight", "05-hwplan-verify", "10-disk-zfs", "20-clone-rootfs", "30-target-config",
			"35-pacman-keyring", "40-boot-grub-zfs", "50-runink-user", "80-enable-s6", "90-export"},
		Hostname: "river", AdminUser: "runink", Self: true, Present: true}
}

func iconOK(p string) bool {
	if !strings.HasPrefix(p, "/usr/share/") || strings.Contains(p, "..") {
		return false
	}
	switch strings.ToLower(filepath.Ext(p)) {
	case ".svg", ".png", ".ico":
		return true
	}
	return false
}

// iconType is the Content-Type of an edition icon.
func iconType(p string) string {
	switch strings.ToLower(filepath.Ext(p)) {
	case ".svg":
		return "image/svg+xml"
	case ".png":
		return "image/png"
	}
	return "image/x-icon"
}
