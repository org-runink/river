// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

// drive walks a flow through the API exactly as the UI does, for unattended tests of the
// graphical installer (build/qemu-gui-test.sh). Each time a screen is up it prints
// "RIVERGUI SCREEN <name>" and pauses, so the harness can take a screendump of the kiosk.
// It prints the recovery key as "RIVERGUI KEY <key>": the harness needs it to unlock the
// installed disk. That line exists only in this test driver; the installer never logs it.
func drive(args []string) int {
	fs := flag.NewFlagSet("drive", flag.ContinueOnError)
	base := fs.String("url", "http://[::1]:47660", "the installer's URL")
	lab := fs.Bool("lab", false, "accept a refused plan through \"Try anyway (lab)\"")
	pause := fs.Duration("pause", 5*time.Second, "pause after each screen (for the screendump)")
	password := fs.String("password", "correct-horse-battery", "the admin password to type")
	timeout := fs.Duration("timeout", 90*time.Minute, "give up after")
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: river-installer drive install|firstboot [flags]")
		return 2
	}
	flow := args[0]
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	d := &driver{base: strings.TrimRight(*base, "/"), pause: *pause, deadline: time.Now().Add(*timeout)}
	var err error
	switch flow {
	case "install":
		err = d.install(*lab, *password)
	case "firstboot":
		err = d.firstboot()
	default:
		err = fmt.Errorf("unknown flow %q", flow)
	}
	if err != nil {
		fmt.Printf("RIVERGUI FAIL %v\n", err)
		return 1
	}
	fmt.Println("RIVERGUI DONE " + flow)
	return 0
}

type driver struct {
	base     string
	pause    time.Duration
	deadline time.Time
	c        http.Client
}

type state map[string]any

func (s state) str(path ...string) string {
	var cur any = map[string]any(s)
	for _, p := range path {
		m, ok := cur.(map[string]any)
		if !ok {
			return ""
		}
		cur = m[p]
	}
	switch v := cur.(type) {
	case string:
		return v
	case bool:
		return fmt.Sprint(v)
	case float64:
		return fmt.Sprint(int(v))
	}
	return ""
}

func (s state) obj(path ...string) any {
	var cur any = map[string]any(s)
	for _, p := range path {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur = m[p]
	}
	return cur
}

func (d *driver) get(path string) (state, error) {
	r, err := d.c.Get(d.base + path)
	if err != nil {
		return nil, err
	}
	defer r.Body.Close()
	var s state
	if err := json.NewDecoder(r.Body).Decode(&s); err != nil {
		return nil, err
	}
	if r.StatusCode != 200 {
		return s, fmt.Errorf("GET %s: %d %v", path, r.StatusCode, s["error"])
	}
	return s, nil
}

func (d *driver) post(path string, body any) (state, error) {
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest(http.MethodPost, d.base+path, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-River", "1")
	r, err := d.c.Do(req)
	if err != nil {
		return nil, err
	}
	defer r.Body.Close()
	raw, _ := io.ReadAll(r.Body)
	var s state
	_ = json.Unmarshal(raw, &s)
	if r.StatusCode != 200 {
		return s, fmt.Errorf("POST %s: %d %s", path, r.StatusCode, strings.TrimSpace(string(raw)))
	}
	return s, nil
}

// until polls the state until ok says so.
func (d *driver) until(what string, ok func(state) bool) (state, error) {
	for {
		s, err := d.get("/api/state")
		if err == nil && ok(s) {
			return s, nil
		}
		if time.Now().After(d.deadline) {
			return s, fmt.Errorf("timed out waiting for %s (last error %v)", what, err)
		}
		time.Sleep(time.Second)
	}
}

func (d *driver) screen(name string) {
	time.Sleep(1500 * time.Millisecond) // the UI renders the pushed state
	fmt.Printf("RIVERGUI SCREEN %s\n", name)
	time.Sleep(d.pause)
}

func onScreen(name string) func(state) bool {
	return func(s state) bool { return s.str("screen") == name }
}

func (d *driver) install(lab bool, password string) error {
	s, err := d.until("the installer", func(s state) bool { return s.str("mode") == "install" })
	if err != nil {
		return err
	}
	if s.str("screen") != "welcome" {
		return fmt.Errorf("the installer is at %q, not the welcome screen", s.str("screen"))
	}
	d.screen("welcome")
	if _, err := d.post("/api/locale", map[string]string{"lang": "en", "keyboard": "us"}); err != nil {
		return err
	}
	if _, err := d.post("/api/welcome", map[string]any{}); err != nil {
		return err
	}
	s, err = d.until("edition", onScreen("edition"))
	if err != nil {
		return err
	}
	d.screen("edition")
	role := ""
	if list, ok := s.obj("edition", "list").([]any); ok && len(list) > 0 {
		if self, ok := list[0].(map[string]any); ok {
			role, _ = self["default_role"].(string)
		}
	}
	if _, err := d.post("/api/edition", map[string]string{"edition": s.str("edition", "self"), "role": role}); err != nil {
		return err
	}
	s, err = d.until("the network check", func(s state) bool {
		return s.str("screen") == "network" && s.str("net", "status") == "done"
	})
	if err != nil {
		return err
	}
	fmt.Printf("RIVERGUI NOTE network %s\n", s.str("net", "state", "state"))
	d.screen("network")
	if _, err := d.post("/api/network/continue", map[string]bool{"offline": s.str("net", "state", "state") == "offline"}); err != nil {
		return err
	}
	machineDone := func(s state) bool {
		st := s.str("machine", "status")
		return s.str("screen") == "machine" && (st == "done" || st == "failed")
	}
	s, err = d.until("the hardware plan", machineDone)
	if err != nil {
		return err
	}
	if s.str("machine", "status") == "failed" {
		return fmt.Errorf("probe/plan failed: %s", s.str("machine", "error"))
	}
	if s.str("machine", "verdict") == "refused" {
		d.screen("machine-refused")
		if !lab {
			return fmt.Errorf("this machine is refused and --lab was not given")
		}
		if _, err := d.post("/api/machine/probe", map[string]bool{"lab": true}); err != nil {
			return err
		}
		time.Sleep(2 * time.Second)
		if s, err = d.until("the lab plan", machineDone); err != nil {
			return err
		}
		if s.str("machine", "verdict") == "refused" {
			return fmt.Errorf("refused even in lab mode")
		}
	}
	d.screen("machine")
	var serials []string
	if disks, ok := s.obj("machine", "disks").([]any); ok {
		for _, x := range disks {
			if m, ok := x.(map[string]any); ok {
				serials = append(serials, fmt.Sprint(m["serial"]))
			}
		}
	}
	fmt.Printf("RIVERGUI NOTE erase %s\n", strings.Join(serials, " "))
	if _, err := d.post("/api/machine/erase-dialog", map[string]bool{"open": true}); err != nil {
		return err
	}
	d.screen("erase")
	word := s.str("options", "erase_word")
	if word == "" {
		word = "ERASE"
	}
	if _, err := d.post("/api/machine/confirm", map[string]any{"word": word, "disks": serials}); err != nil {
		return err
	}
	s, err = d.until("account", onScreen("account"))
	if err != nil {
		return err
	}
	d.screen("account")
	if _, err := d.post("/api/account", map[string]string{"hostname": s.str("account", "hostname"),
		"username": s.str("account", "username"), "password": password, "password2": password}); err != nil {
		return err
	}
	if _, err = d.until("recovery", onScreen("recovery")); err != nil {
		return err
	}
	k, err := d.get("/api/recovery")
	if err != nil {
		return err
	}
	d.screen("recovery")
	fmt.Printf("RIVERGUI KEY %s\n", strings.ReplaceAll(k.str("key"), " ", ""))
	if _, err := d.post("/api/recovery/ack", map[string]bool{"written": true}); err != nil {
		return err
	}
	shot := false
	last := ""
	s, err = d.until("the install", func(s state) bool {
		st := s.str("install", "status")
		line := s.str("install", "percent") + "% eta " + s.str("install", "eta_seconds") + "s"
		if line != last {
			fmt.Printf("RIVERGUI NOTE install %s\n", line)
			last = line
		}
		if pct, _ := strconv.Atoi(s.str("install", "percent")); !shot && st == "running" && pct >= 2 {
			shot = true
			fmt.Println("RIVERGUI SCREEN install")
		}
		return st == "done" || st == "failed"
	})
	if err != nil {
		return err
	}
	if s.str("install", "status") == "failed" {
		d.screen("install-failed")
		if l, err := d.get("/api/install/log"); err == nil {
			if lines, ok := l["lines"].([]any); ok {
				for i := len(lines) - 40; i < len(lines); i++ {
					if i >= 0 {
						fmt.Printf("RIVERGUI LOG %v\n", lines[i])
					}
				}
			}
		}
		return fmt.Errorf("install failed at %s", s.str("install", "failed_step"))
	}
	if _, err = d.until("finished", onScreen("done")); err != nil {
		return err
	}
	d.screen("done")
	return nil
}

func (d *driver) firstboot() error {
	if _, err := d.until("the first boot UI", func(s state) bool { return s.str("mode") == "firstboot" }); err != nil {
		return err
	}
	d.screen("firstboot-settle")
	for {
		s, err := d.until("a settled first boot", func(s state) bool { return s.str("screen") != "settle" })
		if err != nil {
			return err
		}
		switch s.str("screen") {
		case "page":
			d.screen("firstboot-page")
			if _, err := d.post("/api/firstboot/skip", map[string]string{"name": s.str("current")}); err != nil {
				return err
			}
			continue
		case "ready":
			if checks, ok := s.obj("checks").([]any); ok {
				for _, c := range checks {
					m, _ := c.(map[string]any)
					fmt.Printf("RIVERGUI CHECK %v %v %v\n", m["id"], m["status"], m["detail"])
				}
			}
			d.screen("firstboot-ready")
			if _, err := d.post("/api/firstboot/finish", map[string]any{}); err != nil {
				return err
			}
			d.screen("firstboot-finished")
			return nil
		default:
			return fmt.Errorf("unexpected first-boot screen %q", s.str("screen"))
		}
	}
}
