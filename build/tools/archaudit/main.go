// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

// archaudit: the release gate's vulnerability check for the distribution packages in a
// Runink River image. It compares the image's pacman database with the Arch Linux security
// tracker (https://security.archlinux.org/all.json, the data arch-audit uses), because
// generic scanners have no advisory data for this distribution and match its packages
// against unrelated upstream records.
//
//	archaudit -db <rootfs>/var/lib/pacman/local -avg all.json [-waivers waivers.txt] [-report r.json]
//
// A package BLOCKS the release when an advisory group lists it, a fixed version exists, the
// installed version is older than that fix (libalpm's vercmp), and the severity is High or
// Critical, unless a waiver that has not expired names the group (AVG-…) or one of its
// issues (CVE-…). Everything else that affects the image is reported, not blocking. An
// expired waiver blocks on its own. Exit 0: pass; 1: blocked; 2: error.
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type avg struct {
	Name     string   `json:"name"`
	Packages []string `json:"packages"`
	Status   string   `json:"status"`
	Severity string   `json:"severity"`
	Type     string   `json:"type"`
	Affected string   `json:"affected"`
	Fixed    *string  `json:"fixed"`
	Issues   []string `json:"issues"`
}

type finding struct {
	Package   string   `json:"package"`
	Installed string   `json:"installed"`
	Group     string   `json:"group"`
	Severity  string   `json:"severity"`
	Fixed     string   `json:"fixed,omitempty"`
	Issues    []string `json:"issues"`
	Blocking  bool     `json:"blocking"`
	WaivedBy  string   `json:"waived_by,omitempty"`
}

type waiver struct{ id, expires, owner string }

func main() {
	// `archaudit ownfiles <rootfs>`: print the files the image's authors added or changed.
	if len(os.Args) == 3 && os.Args[1] == "ownfiles" {
		files, err := ownFiles(os.Args[2])
		if err != nil {
			fmt.Fprintf(os.Stderr, "archaudit ownfiles: %v\n", err)
			os.Exit(2)
		}
		for _, f := range files {
			fmt.Println(f)
		}
		return
	}
	db := flag.String("db", "", "the image's pacman local database (…/var/lib/pacman/local)")
	avgPath := flag.String("avg", "", "Arch security tracker all.json")
	waivers := flag.String("waivers", "", "waiver file: <id> <expires YYYY-MM-DD> <owner> <reason…>")
	report := flag.String("report", "", "write the JSON report here")
	today := flag.String("today", time.Now().UTC().Format("2006-01-02"), "date waivers are checked against")
	flag.Parse()
	if *db == "" || *avgPath == "" {
		fmt.Fprintln(os.Stderr, "usage: archaudit -db <pacman local db> -avg <all.json> [-waivers f] [-report f]")
		os.Exit(2)
	}
	pkgs, err := installed(*db)
	if err != nil || len(pkgs) == 0 {
		fmt.Fprintf(os.Stderr, "archaudit: no packages read from %s: %v\n", *db, err)
		os.Exit(2)
	}
	groups, err := readAVG(*avgPath)
	if err != nil || len(groups) == 0 {
		fmt.Fprintf(os.Stderr, "archaudit: no advisories read from %s: %v\n", *avgPath, err)
		os.Exit(2)
	}
	ws, expired, err := readWaivers(*waivers, *today)
	if err != nil {
		fmt.Fprintf(os.Stderr, "archaudit: %v\n", err)
		os.Exit(2)
	}
	fs := audit(pkgs, groups, ws)
	blocking := 0
	for _, f := range fs {
		if f.Blocking {
			blocking++
		}
	}
	if *report != "" {
		b, _ := json.MarshalIndent(map[string]any{
			"packages": len(pkgs), "advisory_groups": len(groups), "findings": fs,
			"blocking": blocking, "expired_waivers": expired,
		}, "", "  ")
		if err := os.WriteFile(*report, append(b, '\n'), 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "archaudit: %v\n", err)
			os.Exit(2)
		}
	}
	fmt.Printf("archaudit: %d packages, %d advisory groups, %d findings, %d blocking\n", len(pkgs), len(groups), len(fs), blocking)
	for _, f := range fs {
		mark := "  report"
		switch {
		case f.Blocking:
			mark = "BLOCKING"
		case f.WaivedBy != "":
			mark = "  waived"
		}
		fix := f.Fixed
		if fix == "" {
			fix = "no fix yet"
		}
		fmt.Printf("%s %-9s %-24s %-18s -> %-18s %s %s\n", mark, f.Severity, f.Package, f.Installed, fix, f.Group, strings.Join(f.Issues, ","))
	}
	for _, e := range expired {
		fmt.Printf("BLOCKING expired waiver: %s\n", e)
	}
	if blocking > 0 || len(expired) > 0 {
		os.Exit(1)
	}
}

// installed reads name -> version from a pacman local database (<name>-<ver>/desc).
func installed(dir string) (map[string]string, error) {
	descs, err := filepath.Glob(filepath.Join(dir, "*", "desc"))
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, d := range descs {
		f, err := os.Open(d)
		if err != nil {
			return nil, err
		}
		var name, ver, key string
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			line := sc.Text()
			switch {
			case strings.HasPrefix(line, "%") && strings.HasSuffix(line, "%"):
				key = line
			case line == "":
				key = ""
			case key == "%NAME%":
				name = line
			case key == "%VERSION%":
				ver = line
			}
		}
		f.Close()
		if name != "" && ver != "" {
			out[name] = ver
		}
	}
	return out, nil
}

func readAVG(path string) ([]avg, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var gs []avg
	return gs, json.Unmarshal(b, &gs)
}

func readWaivers(path, today string) (map[string]waiver, []string, error) {
	ws := map[string]waiver{}
	var expired []string
	if path == "" {
		return ws, nil, nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for n := 1; sc.Scan(); n++ {
		fields := strings.Fields(sc.Text())
		if len(fields) == 0 || strings.HasPrefix(fields[0], "#") {
			continue
		}
		if len(fields) < 4 {
			return nil, nil, fmt.Errorf("%s:%d: want <id> <expires> <owner> <reason…>", path, n)
		}
		if _, err := time.Parse("2006-01-02", fields[1]); err != nil {
			return nil, nil, fmt.Errorf("%s:%d: expiry %q is not YYYY-MM-DD", path, n, fields[1])
		}
		if fields[1] < today {
			expired = append(expired, fmt.Sprintf("%s (expired %s, owner %s)", fields[0], fields[1], fields[2]))
			continue
		}
		ws[fields[0]] = waiver{fields[0], fields[1], fields[2]}
	}
	return ws, expired, sc.Err()
}

func audit(pkgs map[string]string, groups []avg, ws map[string]waiver) []finding {
	var fs []finding
	for _, g := range groups {
		if g.Status == "Not affected" {
			continue
		}
		for _, p := range g.Packages {
			ver, ok := pkgs[p]
			if !ok {
				continue
			}
			fixed := ""
			if g.Fixed != nil {
				fixed = *g.Fixed
			}
			if fixed != "" && vercmp(ver, fixed) >= 0 {
				continue // installed at or past the fix
			}
			if fixed == "" && g.Affected != "" && vercmp(ver, g.Affected) < 0 {
				continue // older than the first affected version, and no fix to compare
			}
			f := finding{Package: p, Installed: ver, Group: g.Name, Severity: g.Severity, Fixed: fixed, Issues: g.Issues}
			severe := g.Severity == "Critical" || g.Severity == "High"
			if fixed != "" && severe {
				f.Blocking = true
				if w, ok := waivedBy(ws, g); ok {
					f.Blocking, f.WaivedBy = false, w.id+" (until "+w.expires+", "+w.owner+")"
				}
			}
			fs = append(fs, f)
		}
	}
	sort.Slice(fs, func(i, j int) bool {
		if fs[i].Blocking != fs[j].Blocking {
			return fs[i].Blocking
		}
		if fs[i].Package != fs[j].Package {
			return fs[i].Package < fs[j].Package
		}
		return fs[i].Group < fs[j].Group
	})
	return fs
}

func waivedBy(ws map[string]waiver, g avg) (waiver, bool) {
	if w, ok := ws[g.Name]; ok {
		return w, true
	}
	for _, id := range g.Issues {
		if w, ok := ws[id]; ok {
			return w, true
		}
	}
	return waiver{}, false
}
