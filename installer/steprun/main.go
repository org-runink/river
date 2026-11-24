// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

// river-steprun — runs one installer step so that a failure is never silent. The text
// installers (runink-install, runink-autoinstall) call it for every step.
//
//	river-steprun init                        start a run: empty notices, open the log
//	river-steprun run --step NAME --file F    run `sh F`; exit with its code; on a failure print
//	                                          the report (step, reason, log, state, next action)
//	river-steprun summary                     print the install-notice lines steps printed
//
// Environment: RUNINK_INSTALL_LOG (default /var/log/runink-install.log), RUNINK_STEPRUN_DIR
// (state, default /run/runink-install/steprun), RUNINK_TARGET (the new system, where the log is
// copied once it exists), RUNINK_UNATTENDED (no dialog box on a failure).
// See installer/internal/steprun for the contract.
package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/org-runink/river/installer/internal/steprun"
)

func main() { os.Exit(run(os.Args[1:])) }

func run(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: river-steprun init | run --step NAME --file F | summary")
		return 2
	}
	state := os.Getenv("RUNINK_STEPRUN_DIR")
	if state == "" {
		state = "/run/runink-install/steprun"
		if st, err := os.Stat("/run"); err != nil || !st.IsDir() {
			state = filepath.Join(os.TempDir(), "runink-install-steprun")
		}
	}
	c := &steprun.Config{Log: os.Getenv("RUNINK_INSTALL_LOG"), State: state, Target: os.Getenv("RUNINK_TARGET")}
	switch args[0] {
	case "init":
		if err := c.Init(); err != nil {
			fmt.Fprintf(os.Stderr, "river-steprun: %v\n", err)
			return 1
		}
		log := c.Log
		if log == "" {
			log = steprun.DefaultLog
		}
		fmt.Printf("The install log is written to %s (and copied to the new system when it exists).\n", log)
		return 0
	case "run":
		fs := flag.NewFlagSet("run", flag.ContinueOnError)
		step := fs.String("step", "", "the step's name, e.g. 20-clone-rootfs")
		file := fs.String("file", "", "the step's script")
		if err := fs.Parse(args[1:]); err != nil || *step == "" || *file == "" {
			fmt.Fprintln(os.Stderr, "usage: river-steprun run --step NAME --file F")
			return 2
		}
		r := c.Run(*step, *file)
		if r.RC == 0 {
			return 0
		}
		rep := c.Report(*step, r)
		fmt.Fprint(os.Stderr, "\n======================================================================\n"+rep+
			"======================================================================\n")
		if os.Getenv("RUNINK_UNATTENDED") == "" {
			if d, err := exec.LookPath("dialog"); err == nil {
				if tmp, err := os.CreateTemp("", "steprun-failure."); err == nil {
					_, _ = tmp.WriteString(rep)
					_ = tmp.Close()
					cmd := exec.Command(d, "--title", "Installation stopped", "--exit-label", "OK", "--textbox", tmp.Name(), "0", "0") // #nosec G204
					cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
					_ = cmd.Run()
					_ = os.Remove(tmp.Name())
				}
			}
		}
		return r.RC
	case "summary":
		fmt.Print(c.Summary())
		return 0
	}
	fmt.Fprintf(os.Stderr, "river-steprun: unknown command %q\n", args[0])
	return 2
}
