// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

// river-installer — the Runink River graphical installer's backend (docs/INSTALL.md,
// "Installing with the graphical installer").
//
//	river-installer serve [--listen [::1]:47660] [--kiosk-user U] [--desktop-user U] [--demo]
//	    the install flow on a live medium: a loopback HTTP/JSON API and the web UI it serves;
//	    the kiosk (server) or the desktop browser (workstation) shows it full screen
//	river-installer firstboot [--listen [::1]:47660] [--demo]
//	    the graphical first boot of an installed machine
//	river-installer drive install|firstboot [--url URL] [--lab] [--pause S]
//	    a test driver: walks the flow through the API with the calls the UI makes, printing
//	    "RIVERGUI SCREEN <name>" as each screen is up (build/qemu-gui-test.sh photographs them)
//	river-installer wait [--url URL] [--timeout D]
//	    exit 0 once the UI answers (the kiosk launcher waits for it)
//	river-installer vt N
//	    switch to virtual terminal N (what chvt does, without kbd on the image)
//
// Standard library only, like the rest of installer/. It binds loopback only and answers only
// root and the named kiosk/desktop users (docs/INSTALL.md, "Security").
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/org-runink/river/installer/internal/wizard"
)

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	switch os.Args[1] {
	case "serve":
		os.Exit(serve(os.Args[2:], false))
	case "firstboot":
		os.Exit(serve(os.Args[2:], true))
	case "drive":
		os.Exit(drive(os.Args[2:]))
	case "wait":
		os.Exit(waitUp(os.Args[2:]))
	case "vt":
		os.Exit(switchVT(os.Args[2:]))
	case "-h", "--help", "help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "river-installer: unknown command %q\n", os.Args[1])
		usage()
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: river-installer serve|firstboot|drive ... (see the source header or docs/INSTALL.md)")
	os.Exit(2)
}

func serve(args []string, firstboot bool) int {
	fs := flag.NewFlagSet("river-installer", flag.ContinueOnError)
	listen := fs.String("listen", "[::1]:47660", "loopback address to serve on")
	kiosk := fs.String("kiosk-user", "river-kiosk", "the kiosk user the API answers besides root")
	desktop := fs.String("desktop-user", "", "the live desktop user whose browser shows the installer (workstation)")
	stateDir := fs.String("state-dir", "/run/river-installer", "private state directory (0700, on /run)")
	demo := fs.Bool("demo", false, "touch nothing: a simulated machine (screenshots, UI work)")
	edDir := fs.String("editions", wizard.EditionsDir, "edition descriptors")
	lib := fs.String("lib", "/usr/local/lib/runink-install", "installer steps")
	marker := fs.String("marker", "/var/lib/runink/firstboot-ui", "first boot: the graphical first boot's marker")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	host, portS, err := net.SplitHostPort(*listen)
	port, perr := strconv.Atoi(portS)
	if err != nil || perr != nil {
		fmt.Fprintln(os.Stderr, "river-installer: --listen HOST:PORT")
		return 2
	}
	if ip := net.ParseIP(host); host != "localhost" && (ip == nil || !ip.IsLoopback()) {
		fmt.Fprintln(os.Stderr, "river-installer: refusing a non-loopback --listen address")
		return 2
	}
	logger := log.New(os.Stderr, "river-installer: ", 0)
	logf := func(f string, a ...any) { logger.Printf(f, a...) }
	if err := os.MkdirAll(*stateDir, 0o700); err != nil {
		logf("state dir: %v", err)
		return 1
	}
	_ = os.Chmod(*stateDir, 0o700) // #nosec G302 -- a directory: 0700, owner only
	guard := wizard.Guard{Port: port, ProcRoot: "/proc", AllowUsers: []string{*kiosk}}
	if *desktop != "" {
		guard.AllowUsers = append(guard.AllowUsers, *desktop)
	}
	if *demo {
		guard.ProcRoot = ""
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	var h http.Handler
	if firstboot {
		var fsys wizard.FirstbootSystem
		lang, title := "en", "Runink River"
		if *demo {
			fsys = &wizard.FakeFirstboot{Delay: 2 * time.Second, Dir: *stateDir + "/pages"}
		} else {
			ns := &wizard.NodeSystem{Marker: *marker, RunDir: *stateDir, Logf: logf}
			m := ns.MarkerValues()
			if m["EDITION_TITLE"] != "" {
				title = m["EDITION_TITLE"]
			}
			if m["LANG"] != "" {
				lang = m["LANG"]
			}
			fsys = ns
		}
		fb := wizard.NewFirstboot(fsys, title, lang, logf)
		go fb.Run(ctx)
		// After Finish has cleaned up, stop serving: river-firstboot-ui waits for this process.
		go func() {
			<-fb.Done()
			stop()
		}()
		h = fb.Handler(guard)
	} else {
		var sys wizard.System
		if *demo {
			sys = &wizard.FakeSystem{Delay: 1500 * time.Millisecond,
				USB: []wizard.USBDrive{{Dev: "/dev/sdx", Model: "Example USB drive", SizeGB: 16}}}
		} else {
			sys = &wizard.LiveSystem{Lib: *lib, RunDir: *stateDir, EditionsDir: *edDir,
				Manifest: "/usr/local/share/runink/models.tiers", Lock: "/usr/local/share/runink/models.lock",
				KioskUser: *kiosk, DesktopUser: *desktop, Logf: logf}
		}
		version := strings.TrimSpace(readFile("/etc/runink-os-version"))
		wz := wizard.New(wizard.Config{Version: version, StateFile: *stateDir + "/state.json", RunDir: *stateDir, Logf: logf}, sys)
		h = wz.Handler(guard)
	}
	srv := &http.Server{Addr: *listen, Handler: h, ReadHeaderTimeout: 10 * time.Second}
	ln, err := net.Listen("tcp", *listen)
	if err != nil {
		logf("listen %s: %v", *listen, err)
		return 1
	}
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = srv.Shutdown(sctx)
	}()
	mode := "install"
	if firstboot {
		mode = "first boot"
	}
	logf("%s UI on http://%s/ (demo=%v)", mode, *listen, *demo)
	if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
		logf("serve: %v", err)
		return 1
	}
	return 0
}

func readFile(p string) string {
	b, _ := os.ReadFile(p) // #nosec G304 -- a fixed path
	return string(b)
}
