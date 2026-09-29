// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

// river-plan — turn a hardware probe and a models manifest into a RIVER install plan.
//
//	river-plan --probe P --manifest M [--lock L] [--lab] --json   plan JSON (river.install-plan/v1)
//	river-plan --probe P --manifest M [--lock L] [--lab]          the same plan, for humans
//	river-plan --probe P --no-models ...                          plan without model tiers
//	river-plan --probe P --no-models --profile workstation         a Runink River (workstation) plan:
//	    desktop minimums, no model tiers, no k0s/platform reserve (docs/INSTALLER-HARDWARE.md)
//	river-plan --plan-file F [--json]                             re-render an existing plan
//	river-plan --plan-file F --list-disks                         the disks it wipes: name, confirm_id, GiB, kind, role, model
//	river-plan [--plan-file F | --manifest M ...] --probe P --env --confirm SERIAL [--confirm SERIAL ...]
//	    resolve the plan against the disks in P (a FRESH probe), require every disk the plan
//	    wipes to be confirmed by serial, and print sh assignments for the installer steps
//
// It reads only the files it is given, writes only stdout/stderr, and touches no device.
//
// Exit codes: 0 plan made (verdict ok or degraded) / resolved; 1 error; 2 usage;
// 3 verdict REFUSED (the plan is still printed); 4 --env resolution or confirmation failed.
//
// The CLI and the plan schema are a contract (docs/INSTALLER-HARDWARE.md, "Contract").
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/org-runink/river/installer/internal/hw"
	"github.com/org-runink/river/installer/internal/planner"
)

type multi []string

func (m *multi) String() string     { return fmt.Sprint(*m) }
func (m *multi) Set(s string) error { *m = append(*m, s); return nil }

func main() { os.Exit(run(os.Args[1:])) }

func run(args []string) int {
	fs := flag.NewFlagSet("river-plan", flag.ContinueOnError)
	probeF := fs.String("probe", "", "hwprobe JSON (river-hwprobe --json)")
	manF := fs.String("manifest", "", "models.tiers manifest")
	lockF := fs.String("lock", "", "optional models.lock to cross-check lock_repo= references and derive sizes")
	planF := fs.String("plan-file", "", "an existing plan JSON instead of --manifest")
	noModels := fs.Bool("no-models", false, "plan without a models manifest (tiers are not planned)")
	lab := fs.Bool("lab", false, "waive documented minimums (VMs/test rigs only; recorded in the plan)")
	profile := fs.String("profile", planner.ProfileServer, "image profile: server, or workstation (desktop minimums, no model tiers; needs --no-models)")
	asJSON := fs.Bool("json", false, "print the plan as JSON")
	asEnv := fs.Bool("env", false, "resolve against --probe and print sh assignments for the installer")
	listDisks := fs.Bool("list-disks", false, "print the disks the plan wipes, one per line: name, confirm_id, GiB, kind, role, model (tab-separated)")
	var confirm multi
	fs.Var(&confirm, "confirm", "serial (confirm_id) of a disk the plan wipes; repeat for every disk")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	usage := func(msg string) int { fmt.Fprintln(os.Stderr, "river-plan:", msg); return 2 }
	if fs.NArg() != 0 {
		return usage(fmt.Sprintf("unexpected argument %q", fs.Arg(0)))
	}
	if btoi(*asJSON)+btoi(*asEnv)+btoi(*listDisks) > 1 {
		return usage("--json, --env and --list-disks are exclusive")
	}
	profileSet := false
	fs.Visit(func(f *flag.Flag) { profileSet = profileSet || f.Name == "profile" })
	if *profile != planner.ProfileServer && *profile != planner.ProfileWorkstation {
		return usage(fmt.Sprintf("--profile %q: want server or workstation", *profile))
	}
	if *planF != "" && (*manF != "" || *noModels || *lab || *lockF != "" || profileSet) {
		return usage("--plan-file excludes --manifest/--no-models/--lab/--lock/--profile (the plan is already made)")
	}
	if *profile == planner.ProfileWorkstation && *manF != "" {
		return usage("--profile workstation plans no model tiers: use --no-models, not --manifest")
	}
	if *planF == "" && *probeF == "" {
		return usage("need --probe (with --manifest or --no-models), or --plan-file")
	}
	if *planF == "" && (*manF == "") == !*noModels {
		return usage("give exactly one of --manifest or --no-models")
	}
	if *asEnv && *probeF == "" {
		return usage("--env needs --probe: a FRESH probe of this machine to resolve disks against")
	}

	var probe *hw.Probe
	if *probeF != "" {
		probe = &hw.Probe{}
		if err := readJSON(*probeF, probe); err != nil {
			fmt.Fprintln(os.Stderr, "river-plan:", err)
			return 1
		}
		if probe.Schema != hw.SchemaVersion {
			fmt.Fprintf(os.Stderr, "river-plan: %s: schema %q, want %q\n", *probeF, probe.Schema, hw.SchemaVersion)
			return 1
		}
	}

	var pl *planner.Plan
	if *planF != "" {
		pl = &planner.Plan{}
		if err := readJSON(*planF, pl); err != nil {
			fmt.Fprintln(os.Stderr, "river-plan:", err)
			return 1
		}
		if pl.Schema != planner.SchemaVersion {
			fmt.Fprintf(os.Stderr, "river-plan: %s: schema %q, want %q\n", *planF, pl.Schema, planner.SchemaVersion)
			return 1
		}
	} else {
		var man *planner.Manifest
		if !*noModels {
			var err error
			if man, err = loadManifest(*manF, *lockF); err != nil {
				fmt.Fprintln(os.Stderr, "river-plan:", err)
				return 1
			}
		}
		pl = planner.Build(probe, man, planner.Options{Lab: *lab, Profile: *profile})
		pl.GeneratedAt = time.Now().UTC().Format(time.RFC3339)
	}

	if *listDisks {
		fmt.Print(planner.DiskList(pl))
		if pl.Verdict == "refused" {
			return 3
		}
		return 0
	}
	if *asEnv {
		r, err := planner.Resolve(pl, probe, confirm)
		if err != nil {
			fmt.Fprintln(os.Stderr, "river-plan: NOT resolving:", err)
			return 4
		}
		fmt.Print(r.Env(pl))
		return 0
	}
	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(pl); err != nil {
			fmt.Fprintln(os.Stderr, "river-plan:", err)
			return 1
		}
	} else {
		fmt.Print(planner.Text(pl))
	}
	if pl.Verdict == "refused" {
		return 3
	}
	return 0
}

func readJSON(path string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(b, v); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}

func loadManifest(path, lock string) (*planner.Manifest, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	m, err := planner.ParseManifest(f)
	if err != nil {
		return nil, err
	}
	if lock != "" {
		lf, err := os.Open(lock)
		if err != nil {
			return nil, err
		}
		defer lf.Close()
		if err := m.ApplyLock(lf); err != nil {
			return nil, err
		}
	}
	if err := m.Resolve(); err != nil {
		return nil, err
	}
	return m, nil
}

func btoi(b bool) int {
	if b {
		return 1
	}
	return 0
}
