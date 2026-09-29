// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/org-runink/river/installer/internal/pair"
)

// console is what the local operator sees and answers. It starts the session as an s6
// service (linked into the live scan directory only for as long as this runs, never part of
// the boot database), attaches to it, and removes it again on the way out.
func console(args []string) error {
	fs := flag.NewFlagSet("river-pair-announce", flag.ContinueOnError)
	iface := fs.String("iface", "", "interface to pair on (default: the one the network step set up)")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: sudo river-pair-announce [--iface IF]   (let another machine on this LAN install this one)")
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := needRootLive(); err != nil {
		return err
	}
	link, err := pair.PickLink(*iface)
	if err != nil {
		return err
	}
	if _, err := os.Stat(consoleSock); err == nil {
		return errors.New("a pairing session is already running on this machine")
	}
	if _, err := os.Stat(svTemplate + "/run"); err != nil {
		return errors.New("the session service is missing (" + svTemplate + ")")
	}

	fmt.Println()
	fmt.Println("=== Let another machine install this one (Runink River LAN install) ===")
	fmt.Println("This machine will announce itself on the local link of " + link.Name + " only, and")
	fmt.Println("accept an operator ONLY with the code below AND your \"y\" on this screen.")
	fmt.Println("Nothing is written to any disk until that operator also types each disk's serial.")
	fmt.Println("Ctrl-C here ends the session at any time.")
	fmt.Println()

	// The s6 service: a private copy of the template, run once, never restarted.
	sv := filepath.Join(svRoot, svName)
	_ = os.RemoveAll(svRoot)
	if err := os.MkdirAll(sv, 0o700); err != nil {
		return err
	}
	tpl, err := os.ReadFile(svTemplate + "/run")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(sv, "run"), tpl, 0o755); err != nil { // #nosec G703 G306 -- fixed path under /run; an s6 run script must be executable
		return err
	}
	if err := os.WriteFile(filepath.Join(sv, "iface"), []byte(link.Name+"\n"), 0o600); err != nil {
		return err
	}
	cleanup := func() {
		_ = exec.Command("s6-svunlink", scanDir, svName).Run()
		_ = os.RemoveAll(svRoot)
	}
	if out, err := exec.Command("s6-svlink", "-t", "10000", scanDir, sv, svName).CombinedOutput(); err != nil { // #nosec G204 -- fixed program and fixed paths
		cleanup()
		return fmt.Errorf("cannot start the session service (s6-svlink %s): %v: %s", scanDir, err, strings.TrimSpace(string(out)))
	}
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	go func() {
		<-sig
		fmt.Println("\nriver-pair-announce: ending the session...")
		cleanup()
		os.Exit(130)
	}()

	var c net.Conn
	for i := 0; i < 100; i++ {
		if c, err = net.Dial("unix", consoleSock); err == nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err != nil {
		cleanup()
		return errors.New("the session service did not start (see /var/log/river-pair/)")
	}
	why, err := runConsole(c)
	if why == "reboot" {
		fmt.Println("Rebooting into the installed system...")
		time.Sleep(time.Minute) // the session reboots the machine
	}
	cleanup()
	if err != nil {
		return err
	}
	fmt.Println("river-pair-announce: session ended: " + why)
	return nil
}

func runConsole(c net.Conn) (string, error) {
	defer c.Close()
	stdin := bufio.NewReader(os.Stdin)
	r := bufio.NewReader(c)
	host := ""
	for {
		line, err := readLine(r, 1<<16)
		if err != nil {
			return "the session stopped", nil
		}
		var e event
		if json.Unmarshal(line, &e) != nil {
			continue
		}
		switch e.Ev {
		case "session":
			host = e.Host
			fmt.Printf(`
  +------------------------------------------------------------------------+
  |  This machine:      %-51s|
  |  PAIRING CODE:      %-51s|
  |  Host key:          %-51s|
  |  Link:              %-51s|
  |  Code expires at:   %-51s|
  +------------------------------------------------------------------------+
  On the operator machine, choose %s, type the code,
  and check that it shows exactly this host key.

`, e.Host, e.Code, e.FP, e.Iface+" "+e.Addr, e.Expires+" (15 minutes, 5 wrong codes lock it)", e.Host)
		case "attempt":
			fmt.Printf("  A pairing attempt from %s used a WRONG code (%d left before pairing locks).\n", e.Addr, e.Left)
		case "locked":
			fmt.Printf("  Pairing LOCKED: %d wrong codes (last from %s).\n", pair.MaxAttempts, e.Addr)
		case "expired":
			fmt.Println("  The pairing code expired.")
		case "confirm":
			fmt.Printf("\n  Paired with %s (from %s).\n", e.FP, e.Addr)
			fmt.Println("  Check that the operator's screen shows this same key.")
			fmt.Print("  Allow this operator to install this machine? [y/N] ")
			ans := make(chan string, 1)
			go func() {
				s, _ := stdin.ReadString('\n')
				ans <- strings.TrimSpace(strings.ToLower(s))
			}()
			allow := false
			select {
			case s := <-ans:
				allow = s == "y" || s == "yes"
			case <-time.After(decisionWait - 5*time.Second):
				fmt.Println("\n  No answer: refused.")
			}
			_ = writeJSON(c, answer{Allow: allow})
			if !allow {
				fmt.Println("  Refused.")
			}
		case "paired":
			fmt.Printf("  Allowed. The operator at %s now drives the install of %s.\n", e.Addr, host)
			fmt.Println("  Keep this screen open: it shows the progress and the recovery key. Ctrl-C aborts.")
		case "rpc":
			fmt.Printf("  operator: %s\n", e.Line)
		case "progress":
			fmt.Printf("  %s\n", e.Line)
		case "recovery":
			fmt.Printf(`
  ================================================================================
   ZFS RECOVERY KEY of this machine (also shown once on the operator's console):

       %s

   Record it offline now. It is not stored anywhere and is needed at every boot
   until a key provider is installed.
  ================================================================================

`, e.Line)
		case "teardown":
			return e.Why, nil
		}
	}
}
