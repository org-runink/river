// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

// river-guitest — the host-side helper of build/qemu-gui-test.sh's desk proof. It talks to a
// running QEMU over QMP (keys, mouse, screendumps, a USB stick) and to a small agent in the guest
// over its second serial port (commands run as the user), converts frames to PNG, judges a frame
// by structure instead of exact pixels, and runs the checklist in build/desk-checklist.json.
//
//	river-guitest run     -checklist F -qmp SOCK -guest SOCK -harness DIR -art DIR -work DIR [-usb IMG] [-var K=V]...
//	river-guitest list    -checklist F            the verdict names, one per row
//	river-guitest lint    -checklist F            validate the checklist file
//	river-guitest key     -qmp SOCK super+enter ...   press chords        (type TEXT, click FX FY, usb IMG likewise)
//	river-guitest shot    -qmp SOCK [-head N] OUT.png
//	river-guitest ppm2png IN.ppm OUT.png
//	river-guitest screen  PNG [-preset P] [-region x0,y0,x1,y1] [-expect m=min:max,...] [-vs PNG -changed min:max]
//	river-guitest guest   -guest SOCK [-user U] COMMAND
//
// Exit codes: 0 ok; 1 a check failed; 2 usage or an error. Standard library only.
package main

import (
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

func die(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "river-guitest: "+format+"\n", a...)
	os.Exit(2)
}

type vars map[string]string

func (v vars) String() string { return "" }
func (v vars) Set(s string) error {
	k, val, ok := strings.Cut(s, "=")
	if !ok || k == "" {
		return fmt.Errorf("want NAME=VALUE")
	}
	v[k] = val
	return nil
}

func main() {
	if len(os.Args) < 2 {
		die("usage: river-guitest run|list|lint|key|type|click|shot|usb|ppm2png|screen|guest (see the file header)")
	}
	cmd, args := os.Args[1], os.Args[2:]
	fs := flag.NewFlagSet(cmd, flag.ExitOnError)
	checklist := fs.String("checklist", "", "the checklist file")
	qmp := fs.String("qmp", "", "QMP socket")
	gsock := fs.String("guest", "", "guest command socket")
	harness := fs.String("harness", "", "directory of the harness's own frames")
	art := fs.String("art", "", "artifacts directory")
	work := fs.String("work", "", "harness work directory (packet captures)")
	usb := fs.String("usb", "", "USB stick image")
	rows := fs.String("rows", "", "only these rows, 1-5,9")
	head := fs.Int("head", 0, "display head")
	user := fs.String("user", "runink", "guest user")
	preset := fs.String("preset", "", "screen preset")
	region := fs.String("region", "", "x0,y0,x1,y1 fractions")
	expect := fs.String("expect", "", "metric=min:max,...")
	vs := fs.String("vs", "", "PNG to compare with")
	changed := fs.String("changed", "", "min:max of the changed fraction")
	v := vars{}
	fs.Var(v, "var", "NAME=VALUE for ${NAME} in the checklist (repeatable)")
	// flags may follow positionals (key -qmp S super+d), so parse in rounds
	var pos []string
	for len(args) > 0 {
		fs.Parse(args)
		args = fs.Args()
		if len(args) > 0 {
			pos = append(pos, args[0])
			args = args[1:]
		}
	}
	switch cmd {
	case "list", "lint":
		cl, err := LoadChecklist(*checklist)
		if err != nil {
			die("%v", err)
		}
		if cmd == "list" {
			fmt.Println(strings.Join(cl.Names(), " "))
		} else {
			fmt.Printf("river-guitest: %s: %d rows ok\n", *checklist, len(cl.Rows))
		}
	case "ppm2png":
		if len(pos) != 2 {
			die("ppm2png IN.ppm OUT.png")
		}
		if err := PPMFileToPNG(pos[0], pos[1]); err != nil {
			die("%v", err)
		}
	case "screen":
		os.Exit(screenCmd(pos, *preset, *region, *expect, *vs, *changed))
	case "guest":
		if len(pos) != 1 {
			die("guest COMMAND")
		}
		g, err := DialGuest(*gsock, 30*time.Second)
		if err != nil {
			die("%v", err)
		}
		rc, out, err := g.Run(*user, pos[0], 120*time.Second, true)
		if err != nil {
			die("%v", err)
		}
		fmt.Print(out)
		os.Exit(rc)
	case "key", "type", "click", "shot", "usb":
		q, err := DialQMP(*qmp, 15*time.Second)
		if err != nil {
			die("%v", err)
		}
		defer q.Close()
		if err := inputCmd(q, cmd, pos, *head); err != nil {
			die("%v", err)
		}
	case "run":
		os.Exit(runCmd(*checklist, *qmp, *gsock, *harness, *art, *usb, *work, *rows, v))
	default:
		die("unknown command %q", cmd)
	}
}

func inputCmd(q *QMP, cmd string, pos []string, head int) error {
	switch cmd {
	case "key":
		for _, c := range pos {
			k, err := ParseCombo(c)
			if err != nil {
				return err
			}
			if err := SendKeys(q, k, 120*time.Millisecond); err != nil {
				return err
			}
			time.Sleep(200 * time.Millisecond)
		}
	case "type":
		return TypeText(q, strings.Join(pos, " "))
	case "click":
		if len(pos) < 2 {
			return fmt.Errorf("click FX FY [button]")
		}
		fx, e1 := strconv.ParseFloat(pos[0], 64)
		fy, e2 := strconv.ParseFloat(pos[1], 64)
		if e1 != nil || e2 != nil {
			return fmt.Errorf("click FX FY: fractions 0..1")
		}
		b := "left"
		if len(pos) > 2 {
			b = pos[2]
		}
		return Click(q, fx, fy, b)
	case "shot":
		if len(pos) != 1 {
			return fmt.Errorf("shot OUT.png")
		}
		return Screendump(q, pos[0], head)
	case "usb":
		if len(pos) != 1 {
			return fmt.Errorf("usb IMAGE")
		}
		return UsbAdd(q, pos[0], "xhci.0")
	}
	return nil
}

func parseBound(s string) (Bound, error) {
	lo, hi, ok := strings.Cut(s, ":")
	if !ok {
		return Bound{}, fmt.Errorf("bound %q: want min:max", s)
	}
	var b Bound
	if lo != "" {
		f, err := strconv.ParseFloat(lo, 64)
		if err != nil {
			return b, err
		}
		b.Min = &f
	}
	if hi != "" {
		f, err := strconv.ParseFloat(hi, 64)
		if err != nil {
			return b, err
		}
		b.Max = &f
	}
	return b, nil
}

func screenCmd(pos []string, preset, region, expect, vs, changed string) int {
	if len(pos) != 1 {
		die("screen PNG [-preset P] [-expect m=min:max,...] [-region x0,y0,x1,y1] [-vs PNG -changed min:max]")
	}
	img, err := LoadPNG(pos[0])
	if err != nil {
		die("%v", err)
	}
	var reg Region
	if region != "" {
		p := strings.Split(region, ",")
		if len(p) != 4 {
			die("region is x0,y0,x1,y1")
		}
		for i := range reg {
			if reg[i], err = strconv.ParseFloat(p[i], 64); err != nil {
				die("%v", err)
			}
		}
	}
	want := map[string]Bound{}
	if preset != "" {
		if Presets[preset] == nil {
			die("unknown preset %q", preset)
		}
		for k, b := range Presets[preset] {
			want[k] = b
		}
	}
	for _, e := range strings.Split(expect, ",") {
		if e == "" {
			continue
		}
		k, b, ok := strings.Cut(e, "=")
		if !ok {
			die("expect wants metric=min:max")
		}
		if want[k], err = parseBound(b); err != nil {
			die("%v", err)
		}
	}
	m := Measure(img, reg)
	ok, detail := Evaluate(m, want)
	if vs != "" {
		other, err := LoadPNG(vs)
		if err != nil {
			die("%v", err)
		}
		b, err := parseBound(changed)
		if err != nil {
			die("%v", err)
		}
		f := ChangedFrac(other, img, reg)
		ok = ok && b.ok(f)
		detail += fmt.Sprintf(" changed=%.4g want %s", f, b)
	}
	verdict := "PASS"
	if !ok {
		verdict = "FAIL"
	}
	fmt.Printf("%s %s %s\n", verdict, pos[0], detail)
	if !ok {
		return 1
	}
	return 0
}

func runCmd(checklist, qmp, gsock, harness, art, usb, work, rowSel string, v vars) int {
	cl, err := LoadChecklist(checklist)
	if err != nil {
		die("%v", err)
	}
	if art == "" || qmp == "" || gsock == "" {
		die("run needs -checklist, -qmp, -guest and -art")
	}
	var only map[int]bool
	if rowSel != "" {
		only = map[int]bool{}
		for _, part := range strings.Split(rowSel, ",") {
			lo, hi, isRange := strings.Cut(part, "-")
			a, e1 := strconv.Atoi(lo)
			b := a
			var e2 error
			if isRange {
				b, e2 = strconv.Atoi(hi)
			}
			if e1 != nil || e2 != nil {
				die("bad -rows %q", rowSel)
			}
			for n := a; n <= b; n++ {
				only[n] = true
			}
		}
	}
	q, err := DialQMP(qmp, 30*time.Second)
	if err != nil {
		die("%v", err)
	}
	defer q.Close()
	g, err := DialGuest(gsock, 300*time.Second)
	if err != nil {
		die("%v", err)
	}
	defer g.Close()
	env := Env{Q: q, G: g, Shutdown: q.WaitShutdown, Harness: harness, Art: art, Vars: v, Log: os.Stderr, UsbImage: usb, Work: work}
	res := Run(cl, env, only)
	meta := map[string]string{"checklist": checklist}
	if _, out, err := g.Run("root", "pacman -Q runink-desk 2>/dev/null; uname -r", 20*time.Second, true); err == nil {
		meta["guest"] = oneLine(out)
	}
	good, err := WriteResults(art, res, meta)
	if err != nil {
		die("%v", err)
	}
	counts := map[string]int{}
	for _, r := range res {
		counts[r.Status]++
	}
	fmt.Fprintf(os.Stderr, "desk-proof: %d PASS, %d FAIL, %d NOT PROVABLE IN QEMU (%s)\n",
		counts[Pass], counts[Fail], counts[NPQ], art)
	if !good {
		return 1
	}
	return 0
}
