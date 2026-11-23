// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

package main

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func solid(w, h int, c color.RGBA) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for i := 0; i < len(img.Pix); i += 4 {
		img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = c.R, c.G, c.B, 255
	}
	return img
}

// room draws a stand-in for a lit room: a wooden desk (warm browns, noisy), a bright lamp and a
// paper sheet, so every structural metric has something to find.
func room(w, h int) *image.RGBA {
	img := solid(w, h, color.RGBA{60, 40, 25, 255})
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetRGBA(x, y, color.RGBA{uint8(80 + (x*7+y*13)%150), uint8(50 + (x*5+y*11)%100), uint8(20 + (x*3+y*17)%60), 255})
		}
	}
	for y := h / 5; y < h/2; y++ { // paper
		for x := w / 4; x < w/2; x++ {
			img.SetRGBA(x, y, color.RGBA{235, 232, 225, 255})
		}
	}
	for y := 0; y < h/6; y++ { // lamp light
		for x := w * 3 / 4; x < w; x++ {
			img.SetRGBA(x, y, color.RGBA{255, 220, uint8(120 + x%60), 255})
		}
	}
	return img
}

func writePNG(t *testing.T, path string, img image.Image) {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestParseCombo(t *testing.T) {
	cases := map[string][]string{
		"super+enter":  {"meta_l", "ret"},
		"Ctrl+Alt+Del": {"ctrl", "alt", "delete"},
		"alt+tab":      {"alt", "tab"},
		"super+left":   {"meta_l", "left"},
		"print":        {"print"},
		"shift+print":  {"shift", "print"},
		"super+a":      {"meta_l", "a"},
		"ctrl+f12":     {"ctrl", "f12"},
	}
	for in, want := range cases {
		got, err := ParseCombo(in)
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Errorf("ParseCombo(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "super+", "hyper+a", "f13", "ctrl+é"} {
		if _, err := ParseCombo(bad); err == nil {
			t.Errorf("ParseCombo(%q) accepted", bad)
		}
	}
}

func TestTypeChords(t *testing.T) {
	got, err := TypeChords("Ab-1!\n")
	want := [][]string{{"shift", "a"}, {"b"}, {"minus"}, {"1"}, {"shift", "1"}, {"ret"}}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, %v", got, err)
	}
	if _, err := TypeChords("é"); err == nil {
		t.Error("typed a character the US layout has no key for")
	}
}

// The reason this harness exists: a black or flat screen must fail the structural presets that a
// lit room passes. The old check (a desk process plus some luma entropy) passed a black frame
// with a cursor on it.
func TestPresetsRejectBlackAndAcceptARoom(t *testing.T) {
	black := solid(640, 400, color.RGBA{0, 0, 0, 255})
	cursor := solid(640, 400, color.RGBA{0, 0, 0, 255})
	for y := 100; y < 120; y++ {
		for x := 100; x < 104; x++ {
			cursor.SetRGBA(x, y, color.RGBA{255, 255, 255, 255})
		}
	}
	grey := solid(640, 400, color.RGBA{128, 128, 128, 255})
	lit := room(640, 400)
	for name, img := range map[string]*image.RGBA{"black": black, "black with a cursor": cursor, "flat grey": grey} {
		if ok, why := Evaluate(Measure(img, Region{}), Presets["lit"]); ok {
			t.Errorf("%s passed the lit preset (%s)", name, why)
		}
	}
	if ok, why := Evaluate(Measure(lit, Region{}), Presets["lit"]); !ok {
		t.Errorf("a lit room failed the lit preset: %s", why)
	}
	// a dark room with a lamp is alive; pure black is not
	dark := solid(640, 400, color.RGBA{8, 6, 4, 255})
	for y := 20; y < 90; y++ {
		for x := 480; x < 600; x++ {
			dark.SetRGBA(x, y, color.RGBA{uint8(150 + x%100), uint8(100 + y%100), uint8(40 + (x+y)%90), 255})
		}
	}
	if ok, why := Evaluate(Measure(dark, Region{}), Presets["dark-alive"]); !ok {
		t.Errorf("a dark room with a lamp failed dark-alive: %s", why)
	}
	if ok, _ := Evaluate(Measure(black, Region{}), Presets["dark-alive"]); ok {
		t.Error("black passed dark-alive")
	}
	// the greeter preset: dark wallpaper with a box
	greeter := solid(640, 400, color.RGBA{14, 20, 18, 255})
	for y := 120; y < 280; y++ {
		for x := 160; x < 480; x++ {
			greeter.SetRGBA(x, y, color.RGBA{uint8(40 + x%90), uint8(70 + y%60), uint8(30 + (x+y)%80), 255})
		}
	}
	if ok, why := Evaluate(Measure(greeter, Region{}), Presets["greeter"]); !ok {
		t.Errorf("a greeter failed its preset: %s", why)
	}
	if ok, _ := Evaluate(Measure(black, Region{}), Presets["greeter"]); ok {
		t.Error("black passed the greeter preset")
	}
}

func TestRegionAndChanged(t *testing.T) {
	a := room(400, 300)
	b := room(400, 300)
	if f := ChangedFrac(a, b, Region{}); f != 0 {
		t.Fatalf("identical frames changed %.3f", f)
	}
	for y := 100; y < 200; y++ { // a window opens in the middle
		for x := 100; x < 300; x++ {
			b.SetRGBA(x, y, color.RGBA{240, 240, 240, 255})
		}
	}
	whole := ChangedFrac(a, b, Region{})
	mid := ChangedFrac(a, b, Region{0.25, 0.33, 0.75, 0.66})
	if whole <= 0.1 || whole >= 0.2 || mid <= whole {
		t.Fatalf("whole %.3f mid %.3f", whole, mid)
	}
	if m := Measure(b, Region{0.25, 0.33, 0.75, 0.66}); m["paper_frac"] < 0.9 {
		t.Errorf("paper_frac of the window region %.3f", m["paper_frac"])
	}
	// a cursor-sized difference is noise, not a change
	c := room(400, 300)
	c.SetRGBA(5, 5, color.RGBA{255, 255, 255, 255})
	if f := ChangedFrac(a, c, Region{}); f > 0.001 {
		t.Errorf("one pixel changed %.4f", f)
	}
}

func TestPPMToPNG(t *testing.T) {
	dir := t.TempDir()
	var b bytes.Buffer
	b.WriteString("P6\n# a comment-free header\n2 2\n255\n")
	b.Write([]byte{255, 0, 0, 0, 255, 0, 0, 0, 255, 10, 20, 30})
	// the comment line is not valid PPM for our reader's Fscan; QEMU writes none
	src := filepath.Join(dir, "a.ppm")
	os.WriteFile(src, []byte("P6\n2 2\n255\n\xff\x00\x00\x00\xff\x00\x00\x00\xff\x0a\x14\x1e"), 0o644)
	dst := filepath.Join(dir, "a.png")
	if err := PPMFileToPNG(src, dst); err != nil {
		t.Fatal(err)
	}
	img, err := LoadPNG(dst)
	if err != nil {
		t.Fatal(err)
	}
	if r, g, bl := rgb8(img.At(1, 1)); r != 10 || g != 20 || bl != 30 {
		t.Errorf("pixel (1,1) = %d,%d,%d", r, g, bl)
	}
	if r, _, _ := rgb8(img.At(0, 0)); r != 255 {
		t.Errorf("pixel (0,0) red = %d", r)
	}
	os.WriteFile(src, []byte("P6\n2 2\n255\n\x00"), 0o644) // truncated
	if err := PPMFileToPNG(src, dst); err == nil {
		t.Error("a truncated PPM converted")
	}
}

// fakeQMP is a QMP server on a unix socket that records every command.
type fakeQMP struct {
	path string
	mu   chan struct{}
	cmds []map[string]any
	fail map[string]bool
}

func startFakeQMP(t *testing.T) *fakeQMP {
	t.Helper()
	dir, err := os.MkdirTemp("", "gt")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	f := &fakeQMP{path: filepath.Join(dir, "q"), mu: make(chan struct{}, 1), fail: map[string]bool{}}
	f.mu <- struct{}{}
	l, err := net.Listen("unix", f.path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				fmt.Fprintln(c, `{"QMP":{"version":{},"capabilities":[]}}`)
				sc := bufio.NewScanner(c)
				for sc.Scan() {
					var m map[string]any
					json.Unmarshal(sc.Bytes(), &m)
					<-f.mu
					f.cmds = append(f.cmds, m)
					f.mu <- struct{}{}
					cmd, _ := m["execute"].(string)
					fmt.Fprintln(c, `{"event":"STOP"}`)
					if f.fail[cmd] {
						fmt.Fprintln(c, `{"error":{"class":"GenericError","desc":"no such thing"}}`)
						continue
					}
					if cmd == "screendump" {
						args := m["arguments"].(map[string]any)
						if args["format"] == "png" {
							os.WriteFile(args["filename"].(string), nil, 0o644)
						}
					}
					fmt.Fprintln(c, `{"return":{}}`)
				}
			}()
		}
	}()
	return f
}

func (f *fakeQMP) executed() []string {
	<-f.mu
	defer func() { f.mu <- struct{}{} }()
	var out []string
	for _, m := range f.cmds {
		s := m["execute"].(string)
		if a, ok := m["arguments"]; ok {
			b, _ := json.Marshal(a)
			s += " " + string(b)
		}
		out = append(out, s)
	}
	return out
}

func TestQMPInput(t *testing.T) {
	f := startFakeQMP(t)
	q, err := DialQMP(f.path, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()
	if err := SendKeys(q, []string{"meta_l", "ret"}, 100*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if err := KeyEvents(q, []string{"alt", "tab"}, true); err != nil {
		t.Fatal(err)
	}
	if err := KeyEvents(q, []string{"alt", "tab"}, false); err != nil {
		t.Fatal(err)
	}
	if err := MoveTo(q, 0.5, 1); err != nil {
		t.Fatal(err)
	}
	if err := MoveTo(q, 1.5, 0); err == nil {
		t.Error("a pointer off the screen was sent")
	}
	if err := Button(q, "left", true); err != nil {
		t.Fatal(err)
	}
	got := strings.Join(f.executed(), "\n")
	for _, want := range []string{
		`qmp_capabilities`,
		`send-key {"hold-time":100,"keys":[{"data":"meta_l","type":"qcode"},{"data":"ret","type":"qcode"}]}`,
		`"down":true,"key":{"data":"alt","type":"qcode"}`,
		`{"data":{"axis":"x","value":16383},"type":"abs"}`,
		`{"data":{"axis":"y","value":32767},"type":"abs"}`,
		`{"data":{"button":"left","down":true},"type":"btn"}`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("QMP traffic lacks %s\n%s", want, got)
		}
	}
	// the release is in reverse order: tab up before alt up
	if i, j := strings.Index(got, `"down":false,"key":{"data":"tab"`), strings.Index(got, `"down":false,"key":{"data":"alt"`); i < 0 || j < 0 || i > j {
		t.Errorf("keys released in the wrong order\n%s", got)
	}
}

func TestQMPErrorAndScreendumpFallback(t *testing.T) {
	f := startFakeQMP(t)
	q, err := DialQMP(f.path, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()
	f.fail["send-key"] = true
	if err := SendKeys(q, []string{"a"}, 0); err == nil || !strings.Contains(err.Error(), "no such thing") {
		t.Errorf("a QMP error was not returned: %v", err)
	}
	// a QEMU that refuses PNG: the PPM is converted
	f.fail["screendump"] = true
	dst := filepath.Join(t.TempDir(), "s.png")
	if err := Screendump(q, dst, 0); err == nil {
		t.Error("both screendumps failed but no error")
	}
}

func TestParseResultAndGuest(t *testing.T) {
	out := base64.StdEncoding.EncodeToString([]byte("hello\n"))
	rc, got, ok, err := ParseResult("RIVERRES 4 3 "+out, "4")
	if !ok || err != nil || rc != 3 || got != "hello\n" {
		t.Fatalf("%d %q %v %v", rc, got, ok, err)
	}
	if _, _, ok, _ := ParseResult("RIVERRES 5 0 ", "4"); ok {
		t.Error("another request's answer taken")
	}
	if _, _, ok, _ := ParseResult("kernel: noise", "4"); ok {
		t.Error("noise taken for an answer")
	}
	rc, got, ok, err = ParseResult("RIVERRES 4 0", "4")
	if !ok || err != nil || rc != 0 || got != "" {
		t.Errorf("empty output: %d %q %v %v", rc, got, ok, err)
	}
	if _, _, ok, err := ParseResult("RIVERRES 4 x y", "4"); !ok || err == nil {
		t.Error("a bad status was accepted")
	}
}

// a guest agent on a unix socket that speaks the harness protocol and runs nothing real
func fakeAgent(t *testing.T, noise bool) string {
	t.Helper()
	dir, _ := os.MkdirTemp("", "ga")
	t.Cleanup(func() { os.RemoveAll(dir) })
	p := filepath.Join(dir, "g")
	l, err := net.Listen("unix", p)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	go func() {
		c, err := l.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		sc := bufio.NewScanner(c)
		pings := 0
		for sc.Scan() {
			f := strings.Fields(sc.Text())
			switch {
			case len(f) == 2 && f[0] == "RIVERPING":
				if pings++; pings < 2 { // the agent is slow to come up: the first ping is lost
					continue
				}
				fmt.Fprintf(c, "RIVERPONG %s\n", f[1])
			case len(f) == 4 && f[0] == "RIVERCMD":
				cmd, _ := base64.StdEncoding.DecodeString(f[3])
				if noise {
					fmt.Fprintf(c, "[    1.0] some kernel line\nRIVERRES 99 0 \n")
				}
				fmt.Fprintf(c, "RIVERRES %s 7 %s\n", f[1], base64.StdEncoding.EncodeToString([]byte(f[2]+":"+string(cmd))))
			}
		}
	}()
	return p
}

func TestGuestRoundTrip(t *testing.T) {
	p := fakeAgent(t, true)
	g, err := DialGuest(p, 20*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	rc, out, err := g.Run("root", "echo hi", 5*time.Second, true)
	if err != nil || rc != 7 || out != "root:echo hi" {
		t.Fatalf("%d %q %v", rc, out, err)
	}
	if _, _, err := g.Run("root", "x", time.Second, false); err != nil {
		t.Errorf("nowait: %v", err)
	}
}

func TestShutdownEvent(t *testing.T) {
	dir, _ := os.MkdirTemp("", "sd")
	defer os.RemoveAll(dir)
	p := filepath.Join(dir, "q")
	l, _ := net.Listen("unix", p)
	defer l.Close()
	go func() {
		c, _ := l.Accept()
		fmt.Fprintln(c, `{"QMP":{}}`)
		sc := bufio.NewScanner(c)
		sc.Scan()
		fmt.Fprintln(c, `{"return":{}}`)
		time.Sleep(100 * time.Millisecond)
		fmt.Fprintln(c, `{"event":"SHUTDOWN","data":{}}`)
	}()
	q, err := DialQMP(p, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if !q.WaitShutdown(3 * time.Second) {
		t.Error("SHUTDOWN not seen")
	}
}

func TestRealChecklistIsValidAndComplete(t *testing.T) {
	cl, err := LoadChecklist("../../build/desk-checklist.json")
	if err != nil {
		t.Fatal(err)
	}
	if len(cl.Rows) < 30 {
		t.Fatalf("%d rows, the parity checklist has 30", len(cl.Rows))
	}
	// the parity analysis's 30 rows keep their numbers; hardware-only rows follow them
	want := []string{"boot-splash", "greeter", "desk-dark", "desk-lit", "terminal", "post-box", "drawer", "bookcase",
		"all-apps-search", "control-panel", "volume", "bluetooth", "tray", "settings-system", "lang-ja", "weather-off",
		"camera", "film", "door-lock", "door-no", "door-open", "alt-tab", "half-snap", "two-screens", "logout",
		"second-login", "reset", "battery-letter", "usb", "shutdown"}
	for i, id := range want {
		if cl.Rows[i].ID != id {
			t.Errorf("row %d is %s, want %s", i+1, cl.Rows[i].ID, id)
		}
	}
	hw := map[string]bool{}
	for _, r := range cl.Rows {
		if r.Hardware != "" {
			hw[r.ID] = true
		}
	}
	for _, id := range []string{"battery-letter", "hw-backlight", "hw-rfkill", "hw-suspend", "hw-touch", "hw-pen", "hw-autorotate"} {
		if !hw[id] {
			t.Errorf("%s must be marked hardware-only", id)
		}
	}
}

func TestValidateRejectsAProofThatProvesNothing(t *testing.T) {
	good := `{"version":1,"rows":[{"n":1,"id":"a","title":"t","checks":[{"kind":"guest","cmd":"true","reason":"r"}]}]}`
	cases := map[string]string{
		"no checks":      `{"version":1,"rows":[{"n":1,"id":"a","title":"t"}]}`,
		"no reason":      `{"version":1,"rows":[{"n":1,"id":"a","title":"t","checks":[{"kind":"guest","cmd":"true"}]}]}`,
		"bad numbering":  `{"version":1,"rows":[{"n":2,"id":"a","title":"t","hardware":"x"}]}`,
		"two actions":    `{"version":1,"rows":[{"n":1,"id":"a","title":"t","steps":[{"key":"a","wait":"1s"}],"checks":[{"kind":"guest","cmd":"true","reason":"r"}]}]}`,
		"bad key":        `{"version":1,"rows":[{"n":1,"id":"a","title":"t","steps":[{"key":"hyper+a"}],"checks":[{"kind":"guest","cmd":"true","reason":"r"}]}]}`,
		"unknown metric": `{"version":1,"rows":[{"n":1,"id":"a","title":"t","steps":[{"shot":"s"}],"checks":[{"kind":"screen","shot":"s","expect":{"vibes":{"min":1}},"reason":"r"}]}]}`,
		"vs nowhere":     `{"version":1,"rows":[{"n":1,"id":"a","title":"t","steps":[{"shot":"s"}],"checks":[{"kind":"screen","shot":"s","vs":"zzz","changed":{"min":0.1},"reason":"r"}]}]}`,
		"unknown field":  `{"version":1,"rows":[{"n":1,"id":"a","title":"t","chek":[]}]}`,
		"empty screen":   `{"version":1,"rows":[{"n":1,"id":"a","title":"t","steps":[{"shot":"s"}],"checks":[{"kind":"screen","shot":"s","reason":"r"}]}]}`,
	}
	dir := t.TempDir()
	write := func(s string) string {
		p := filepath.Join(dir, fmt.Sprint(len(s), ".json"))
		os.WriteFile(p, []byte(s), 0o644)
		return p
	}
	if _, err := LoadChecklist(write(good)); err != nil {
		t.Fatalf("good checklist: %v", err)
	}
	for name, s := range cases {
		if _, err := LoadChecklist(write(s)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

// fakeGuest answers guest commands from a table keyed by substring of the command.
type fakeGuest struct {
	answers map[string]struct {
		rc  int
		out string
	}
	ran []string
}

func (f *fakeGuest) Run(user, cmd string, _ time.Duration, wait bool) (int, string, error) {
	f.ran = append(f.ran, user+":"+cmd)
	for k, v := range f.answers {
		if strings.Contains(cmd, k) {
			return v.rc, v.out, nil
		}
	}
	return 1, "", nil
}

func TestRunnerEndToEnd(t *testing.T) {
	dark := solid(320, 200, color.RGBA{6, 5, 4, 255})
	frames := map[int]image.Image{0: dark}
	cur := 0
	shoot := func(p string, head int) error {
		var b bytes.Buffer
		png.Encode(&b, frames[cur])
		return os.WriteFile(p, b.Bytes(), 0o644)
	}
	cl := &Checklist{Version: 1, Rows: []Row{
		{N: 1, ID: "lit", Title: "lit room", Steps: []Step{{Wait: "1s"}}, Checks: []Check{
			{Kind: "screen", Shot: "lit", Preset: "lit", Reason: "lit", Retry: "1s"},
			{Kind: "guest", Cmd: "pgrep desk", Match: "^42", Reason: "alive"}}},
		{N: 2, ID: "win", Title: "window", Steps: []Step{{Key: "super+enter"}, {Shot: "w"}}, Checks: []Check{
			{Kind: "screen", Shot: "w", Vs: "lit", Changed: &Bound{Min: ptr(0.05)}, Reason: "window opened"}}},
		{N: 3, ID: "hw-x", Title: "hardware", Hardware: "no radio in QEMU"},
	}}
	if err := cl.Validate(); err != nil {
		t.Fatal(err)
	}
	q := startFakeQMP(t)
	qc, err := DialQMP(q.path, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer qc.Close()
	art := t.TempDir()
	g := &fakeGuest{answers: map[string]struct {
		rc  int
		out string
	}{"pgrep desk": {0, "42\n"}}}
	env := Env{Q: qc, G: g, Art: art, Sleep: func(time.Duration) {}, Shoot: shoot}

	// the screen stays black: the lit row FAILS, the window row fails too (nothing changed)
	res := Run(cl, env, nil)
	if res[0].Status != Fail || res[1].Status != Fail || res[2].Status != NPQ {
		t.Fatalf("black screen: %s %s %s", res[0].Status, res[1].Status, res[2].Status)
	}

	// now the room is lit and a window opens
	cur = 1
	frames[1] = room(320, 200)
	env2 := env
	env2.Art = t.TempDir()
	env2.Shoot = func(p string, head int) error {
		var b bytes.Buffer
		img := frames[1]
		if strings.Contains(p, "-w.png") {
			w := room(320, 200)
			for y := 40; y < 160; y++ {
				for x := 40; x < 280; x++ {
					w.SetRGBA(x, y, color.RGBA{240, 240, 240, 255})
				}
			}
			img = w
		}
		png.Encode(&b, img)
		return os.WriteFile(p, b.Bytes(), 0o644)
	}
	res = Run(cl, env2, nil)
	if res[0].Status != Pass || res[1].Status != Pass || res[2].Status != NPQ {
		for _, r := range res {
			t.Logf("%+v", r)
		}
		t.Fatalf("lit room: %s %s %s", res[0].Status, res[1].Status, res[2].Status)
	}
	// numbered PNGs with the row id in the name
	ents, _ := os.ReadDir(env2.Art)
	var names []string
	for _, e := range ents {
		names = append(names, e.Name())
	}
	if !reflect.DeepEqual(names, []string{"001-lit.png", "002-win-w.png"}) {
		t.Errorf("artifacts %v", names)
	}
	good, err := WriteResults(env2.Art, res, map[string]string{"k": "v"})
	if err != nil || !good {
		t.Fatalf("WriteResults: %v %v", good, err)
	}
	rt, _ := os.ReadFile(filepath.Join(env2.Art, "rows.txt"))
	for _, want := range []string{"RIVERTEST OK desk-01-lit", "RIVERTEST OK desk-02-win", "RIVERTEST SKIP desk-03-hw-x not provable in QEMU: no radio in QEMU"} {
		if !strings.Contains(string(rt), want) {
			t.Errorf("rows.txt lacks %q:\n%s", want, rt)
		}
	}
	var doc struct {
		Counts map[string]int
		Rows   []RowResult
	}
	b, _ := os.ReadFile(filepath.Join(env2.Art, "results.json"))
	if err := json.Unmarshal(b, &doc); err != nil || doc.Counts[Pass] != 2 || doc.Counts[NPQ] != 1 {
		t.Errorf("results.json: %v %+v", err, doc.Counts)
	}
	// a failing run is reported as such
	good, _ = WriteResults(t.TempDir(), Run(cl, env, nil), nil)
	if good {
		t.Error("a failing run was reported good")
	}
}

func ptr(f float64) *float64 { return &f }

func TestFileAbsent(t *testing.T) {
	dir := t.TempDir()
	r := &runner{env: Env{Work: dir}}
	c := Check{Kind: "file-absent", Path: "n.pcap", Needle: "open-meteo", Reason: "r"}
	if ok, _ := r.fileAbsent(c); ok {
		t.Error("a missing capture passed")
	}
	os.WriteFile(filepath.Join(dir, "n.pcap"), []byte("short"), 0o644)
	if ok, _ := r.fileAbsent(c); ok {
		t.Error("an empty capture passed")
	}
	os.WriteFile(filepath.Join(dir, "n.pcap"), bytes.Repeat([]byte("x"), 200), 0o644)
	if ok, why := r.fileAbsent(c); !ok {
		t.Error(why)
	}
	os.WriteFile(filepath.Join(dir, "n.pcap"), append(bytes.Repeat([]byte("x"), 100), []byte("\x0aopen-meteo\x03com")...), 0o644)
	if ok, _ := r.fileAbsent(c); ok {
		t.Error("a capture with the host in it passed")
	}
}
