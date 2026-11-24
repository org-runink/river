// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

package wizard

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/org-runink/river/installer/internal/hw"
	"github.com/org-runink/river/installer/internal/planner"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

type logSink struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *logSink) logf(f string, a ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	fmt.Fprintf(&l.b, f+"\n", a...)
}
func (l *logSink) String() string { l.mu.Lock(); defer l.mu.Unlock(); return l.b.String() }

type rig struct {
	t    *testing.T
	w    *Wizard
	sys  *FakeSystem
	logs *logSink
	srv  *httptest.Server
	dir  string
}

func newRig(t *testing.T, sys *FakeSystem) *rig {
	t.Helper()
	dir := t.TempDir()
	logs := &logSink{}
	if sys == nil {
		sys = &FakeSystem{USB: []USBDrive{{Dev: "/dev/sdx", Model: "Stick", SizeGB: 16}}}
	}
	w := New(Config{Version: "test", StateFile: filepath.Join(dir, "state.json"), RunDir: dir, Logf: logs.logf,
		LogFile: filepath.Join(dir, "install.log"), TargetRoot: filepath.Join(dir, "target")}, sys)
	r := &rig{t: t, w: w, sys: sys, logs: logs, dir: dir}
	r.srv = httptest.NewServer(w.Handler(Guard{Port: 0}))
	t.Cleanup(r.srv.Close)
	return r
}

// call performs an API call as the UI does; the Host header is the guarded loopback name.
func (r *rig) call(method, path string, body any) (int, map[string]any) {
	r.t.Helper()
	var rd *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	} else {
		rd = bytes.NewReader(nil)
	}
	req, _ := http.NewRequest(method, r.srv.URL+path, rd)
	req.Host = "[::1]:0"
	if method == http.MethodPost {
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-River", "1")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		r.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	var m map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&m)
	return resp.StatusCode, m
}

func (r *rig) post(path string, body any) map[string]any {
	r.t.Helper()
	code, m := r.call(http.MethodPost, path, body)
	if code != 200 {
		r.t.Fatalf("POST %s: %d %v", path, code, m)
	}
	return m
}

func (r *rig) waitView(what string, ok func(View) bool) View {
	r.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		v := r.w.View()
		if ok(v) {
			return v
		}
		if time.Now().After(deadline) {
			r.t.Fatalf("timed out waiting for %s (screen %s)", what, v.Screen)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// ackRecovery answers the recovery screen's challenge correctly: it reads the key the wizard
// generated and types back exactly the groups Recovery.Confirm asked for. Every test that just
// wants to get past this screen uses it; the challenge itself is tested in
// TestRecoveryKeyMustBeTypedBack.
func (r *rig) ackRecovery() {
	r.t.Helper()
	key, err := r.w.RecoveryKey()
	if err != nil {
		r.t.Fatalf("no recovery key to confirm: %v", err)
	}
	r.post("/api/recovery/ack", map[string]any{"groups": confirmGroups(r.w.View().Recovery.Confirm, key)})
}

// confirmGroups renders the asked-for 1-based groups of a 64-hex key as the UI would collect
// them: {"3": "a1b2c3d4", ...}.
func confirmGroups(want []int, key string) map[string]string {
	groups := strings.Fields(groupKey(key))
	out := map[string]string{}
	for _, n := range want {
		out[strconv.Itoa(n)] = groups[n-1]
	}
	return out
}

const pw = "S3cret-Passw0rd!"

// toRecovery walks the flow to the recovery screen.
func (r *rig) toRecovery() {
	r.post("/api/welcome", map[string]any{})
	r.post("/api/edition", map[string]string{"edition": "server"})
	r.waitView("network", func(v View) bool { return v.Net.Status == "done" })
	r.post("/api/network/continue", map[string]bool{"offline": false})
	v := r.waitView("plan", func(v View) bool { return v.Machine.Status == "done" })
	var serials []string
	for _, d := range v.Machine.Disks {
		serials = append(serials, d.Serial)
	}
	r.post("/api/machine/confirm", map[string]any{"word": "ERASE", "disks": serials})
	r.post("/api/account", AccountInput{Hostname: "node-a", Username: "admin", Password: pw, Password2: pw,
		SSHKeys: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIEvOgU6sMALKkGWgtWZvy6ECntxTSuL5/xudaZ5jE16+ test@example.org"})
}

func TestHappyPath(t *testing.T) {
	r := newRig(t, nil)
	if v := r.w.View(); v.Screen != "welcome" || v.Profile != "server" {
		t.Fatalf("start: %s %s", v.Screen, v.Profile)
	}
	r.toRecovery()
	_, rec := r.call(http.MethodGet, "/api/recovery", nil)
	key := strings.ReplaceAll(fmt.Sprint(rec["key"]), " ", "")
	if len(key) != 64 || len(rec["qr"].([]any)) < 21 {
		t.Fatalf("recovery: key %q", key)
	}
	// The key must be typed back before anything is written to the disk.
	v0 := r.w.View()
	if len(v0.Recovery.Confirm) != keyConfirmGroups {
		t.Fatalf("recovery asked for %v groups", v0.Recovery.Confirm)
	}
	wrong := confirmGroups(v0.Recovery.Confirm, key)
	for n := range wrong {
		wrong[n] = "00000000"
	}
	if code, m := r.call(http.MethodPost, "/api/recovery/ack", map[string]any{"groups": wrong}); code != 422 || m["error"] != "err.recovery.groups" {
		t.Fatalf("ack with the wrong groups: %d %v", code, m)
	}
	r.ackRecovery()
	v := r.waitView("done", func(v View) bool { return v.Screen == "done" })
	if v.Install.Status != "done" || v.Install.Percent != 100 {
		t.Fatalf("install: %+v", v.Install)
	}
	// The key reached 10-disk-zfs on stdin, and only there.
	if got := r.sys.stdinOf("10-disk-zfs"); strings.TrimSpace(got) != key {
		t.Fatalf("10-disk-zfs stdin = %q", got)
	}
	env := strings.Join(r.sys.envOf("50-runink-user"), "\n")
	for _, want := range []string{"RUNINK_HOSTNAME=node-a", "RUNINK_ADMIN_USER=admin", "RUNINK_ZFS_KEY=stdin",
		"RUNINK_POOL_FRESH=1", "RUNINK_FIRSTBOOT_UI=1", "RUNINK_ADMIN_PASSWORD_HASH=$6$", "RUNINK_PLAN_PROFILE=server"} {
		if !strings.Contains(env, want) {
			t.Errorf("step env lacks %q", want)
		}
	}
	if strings.Contains(env, key) || strings.Contains(env, pw) {
		t.Fatal("the key or the password is in a step's environment")
	}
	// After the install the key is gone: it can no longer be read.
	if code, _ := r.call(http.MethodGet, "/api/recovery", nil); code == 200 {
		t.Fatal("the recovery key is still served after the install")
	}
	r.post("/api/reboot", map[string]any{})
	time.Sleep(1200 * time.Millisecond)
	if r.sys.RebootCount() != 1 {
		t.Fatal("no reboot")
	}
}

// TestSecretsNeverLogged: neither the password, nor the recovery key (plain or grouped), nor a
// Wi-Fi passphrase appears in the backend's log, the install log, the persisted state or the
// view, even when a step prints its stdin.
func TestSecretsNeverLogged(t *testing.T) {
	r := newRig(t, nil)
	r.post("/api/welcome", map[string]any{})
	r.post("/api/edition", map[string]string{"edition": "server"})
	r.waitView("network", func(v View) bool { return v.Net.Status == "done" })
	r.post("/api/network/wifi", map[string]string{"ssid": "Example Lab", "passphrase": "wifi-secret-99"})
	r.waitView("wifi", func(v View) bool { return v.Net.Status == "done" })
	r.post("/api/network/continue", map[string]bool{})
	v := r.waitView("plan", func(v View) bool { return v.Machine.Status == "done" })
	var serials []string
	for _, d := range v.Machine.Disks {
		serials = append(serials, d.Serial)
	}
	r.post("/api/machine/confirm", map[string]any{"word": "ERASE", "disks": serials})
	r.post("/api/account", AccountInput{Hostname: "node-a", Username: "admin", Password: pw, Password2: pw})
	key, _ := r.w.RecoveryKey()
	r.ackRecovery()
	r.waitView("done", func(v View) bool { return v.Screen == "done" })

	state, _ := os.ReadFile(filepath.Join(r.dir, "state.json"))
	view, _ := json.Marshal(r.w.View())
	_, lg := r.call(http.MethodGet, "/api/install/log", nil)
	logJSON, _ := json.Marshal(lg)
	haystacks := map[string]string{"backend log": r.logs.String(), "install log": string(logJSON),
		"state file": string(state), "view": string(view)}
	for name, h := range haystacks {
		for _, secret := range []string{pw, key, groupKey(key), "wifi-secret-99"} {
			if strings.Contains(h, secret) {
				t.Errorf("%s contains a secret (%.6s...)", name, secret)
			}
		}
	}
	if !strings.Contains(string(logJSON), "[redacted]") {
		t.Error("the step that printed its stdin was not redacted")
	}
	if strings.Contains(string(state), "$6$") {
		t.Error("the password hash outlived the install in the state file")
	}
}

func TestEraseGate(t *testing.T) {
	r := newRig(t, nil)
	r.post("/api/welcome", map[string]any{})
	r.post("/api/edition", map[string]string{"edition": "server"})
	r.waitView("network", func(v View) bool { return v.Net.Status == "done" })
	// Nothing past the network before the machine screen.
	if code, _ := r.call(http.MethodPost, "/api/machine/confirm", map[string]any{"word": "ERASE"}); code != 409 {
		t.Fatalf("confirm on the network screen: %d", code)
	}
	r.post("/api/network/continue", map[string]bool{})
	v := r.waitView("plan", func(v View) bool { return v.Machine.Status == "done" })
	if len(v.Machine.Disks) != 2 {
		t.Fatalf("plan disks: %+v", v.Machine.Disks)
	}
	all := []string{v.Machine.Disks[0].Serial, v.Machine.Disks[1].Serial}
	for _, tc := range []struct {
		word  string
		disks []string
		key   string
	}{
		{"erase", all, ""}, // case-insensitive is fine...
		{"YES", all, "err.erase.word"},
		{"", all, "err.erase.word"},
		{"ERASE", all[:1], "err.erase.disks"},                      // every disk must be named
		{"ERASE", []string{all[0], "USBBOOT1"}, "err.erase.disks"}, // never the boot medium
		{"ERASE", nil, "err.erase.disks"},
	} {
		if tc.key == "" {
			continue
		}
		code, m := r.call(http.MethodPost, "/api/machine/confirm", map[string]any{"word": tc.word, "disks": tc.disks})
		if code != 422 || m["error"] != tc.key {
			t.Errorf("confirm(%q, %v) = %d %v, want %s", tc.word, tc.disks, code, m, tc.key)
		}
	}
	if s := r.w.View().Screen; s != "machine" {
		t.Fatalf("a refused confirmation moved on to %s", s)
	}
	// The word of the screen's language is accepted too.
	r.post("/api/locale", map[string]string{"lang": "es"})
	r.post("/api/machine/confirm", map[string]any{"word": "borrar", "disks": all})
	if s := r.w.View().Screen; s != "account" {
		t.Fatalf("after confirm: %s", s)
	}
	// Changing the plan (lab re-plan) after confirming voids the confirmation.
	r.post("/api/back", map[string]any{})
	r.post("/api/machine/probe", map[string]bool{"lab": true})
	r.waitView("replan", func(v View) bool { return v.Machine.Status == "done" })
	if r.w.View().Machine.Confirmed {
		t.Fatal("a new plan kept the old confirmation")
	}
}

func TestRefusedMachineLab(t *testing.T) {
	r := newRig(t, &FakeSystem{RefuseAll: true})
	r.post("/api/welcome", map[string]any{})
	r.post("/api/edition", map[string]string{"edition": "server"})
	r.waitView("network", func(v View) bool { return v.Net.Status == "done" })
	r.post("/api/network/continue", map[string]bool{})
	v := r.waitView("plan", func(v View) bool { return v.Machine.Status == "done" })
	if v.Machine.Verdict != "refused" || len(v.Machine.Refusals) == 0 {
		t.Fatalf("expected a refused plan: %+v", v.Machine)
	}
	if code, _ := r.call(http.MethodPost, "/api/machine/erase-dialog", map[string]bool{"open": true}); code != 409 {
		t.Fatal("the erase dialog opened on a refused machine")
	}
	r.post("/api/machine/probe", map[string]bool{"lab": true})
	v = r.waitView("lab plan", func(v View) bool { return v.Machine.Status == "done" })
	if v.Machine.Verdict == "refused" || !v.Machine.Lab {
		t.Fatalf("lab plan: %s lab=%v", v.Machine.Verdict, v.Machine.Lab)
	}
}

func TestAccountValidation(t *testing.T) {
	r := newRig(t, nil)
	r.w.st.Screen = "account"
	for _, tc := range []struct {
		in    AccountInput
		field string
		key   string
	}{
		{AccountInput{Hostname: "Bad_Name", Username: "admin", Password: pw, Password2: pw}, "hostname", "err.hostname.invalid"},
		{AccountInput{Hostname: "-x", Username: "admin", Password: pw, Password2: pw}, "hostname", "err.hostname.invalid"},
		{AccountInput{Hostname: "ok", Username: "root", Password: pw, Password2: pw}, "username", "err.username.reserved"},
		{AccountInput{Hostname: "ok", Username: "9lives", Password: pw, Password2: pw}, "username", "err.username.invalid"},
		{AccountInput{Hostname: "ok", Username: "admin", Password: "short", Password2: "short"}, "password", "err.password.short"},
		{AccountInput{Hostname: "ok", Username: "admin", Password: pw, Password2: pw + "x"}, "password", "err.password.mismatch"},
		{AccountInput{Hostname: "ok", Username: "admin", Password: "tab\there12", Password2: "tab\there12"}, "password", "err.password.invalid"},
		{AccountInput{Hostname: "ok", Username: "admin", Password: pw, Password2: pw, SSHKeys: "not a key"}, "ssh_keys", "err.sshkey.invalid"},
		{AccountInput{Hostname: "ok", Username: "admin", Password: pw, Password2: pw,
			SSHKeys: "ssh-ed25519 AAAAB3NzaC1yc2EAAAADAQABAAABAQ== mismatched-type"}, "ssh_keys", "err.sshkey.invalid"},
	} {
		code, m := r.call(http.MethodPost, "/api/account", tc.in)
		fields, _ := m["fields"].(map[string]any)
		if code != 422 || fields[tc.field] != tc.key {
			t.Errorf("%+v: %d %v, want %s=%s", tc.in, code, m, tc.field, tc.key)
		}
	}
	if s := r.w.View().Screen; s != "account" {
		t.Fatalf("invalid input moved on to %s", s)
	}
	keys, k := validSSHKeys("ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIJ8Rc1oY a@b\n\n# comment\nssh-rsa AAAAB3NzaC1yc2EAAAADAQAB c")
	if k != "" || strings.Count(keys, "\n") != 2 {
		t.Fatalf("valid keys: %q %q", keys, k)
	}
}

// The recovery key is shown once, and it is the only way to decrypt the disk the installer is
// about to write. Before river#12 a tickbox stood here: it was satisfied by a glance, so a
// mistranscribed character was discovered at the next boot, when nothing could be done. The
// screen now asks for randomly chosen groups to be typed back.
func TestRecoveryKeyMustBeTypedBack(t *testing.T) {
	r := newRig(t, nil)
	r.toRecovery()
	key, err := r.w.RecoveryKey()
	if err != nil {
		t.Fatal(err)
	}
	want := r.w.View().Recovery.Confirm
	if len(want) != keyConfirmGroups {
		t.Fatalf("asked for %v, want %d groups", want, keyConfirmGroups)
	}
	if !slices.IsSorted(want) {
		t.Errorf("groups %v are not in reading order", want)
	}
	for _, n := range want {
		if n < 1 || n > keyGroups {
			t.Fatalf("group %d is not one of the %d shown", n, keyGroups)
		}
	}

	// One wrong character in one group is refused, and NOTHING is written: the key is still
	// on screen, so the operator can correct their copy.
	bad := confirmGroups(want, key)
	first := strconv.Itoa(want[0])
	bad[first] = "f" + bad[first][1:]
	if bad[first] == confirmGroups(want, key)[first] {
		bad[first] = "0" + bad[first][1:] // the first character already was 'f'
	}
	if code, m := r.call(http.MethodPost, "/api/recovery/ack", map[string]any{"groups": bad}); code != 422 || m["error"] != "err.recovery.groups" {
		t.Fatalf("one wrong character accepted: %d %v", code, m)
	}
	// Nothing has started: the view's install status is still the zero value, not "running".
	if v := r.w.View(); v.Screen != "recovery" || v.Recovery.Acknowledged || v.Install.Status == "running" || v.Install.Status == "done" {
		t.Fatalf("a refused confirmation moved on: screen %s recovery %+v install %q", v.Screen, v.Recovery, v.Install.Status)
	}
	if k2, err := r.w.RecoveryKey(); err != nil || k2 != key {
		t.Fatalf("the key changed under a wrong answer: %v", err)
	}

	// A group left out is refused too — an empty box must not pass as a match.
	short := confirmGroups(want, key)
	delete(short, first)
	if code, _ := r.call(http.MethodPost, "/api/recovery/ack", map[string]any{"groups": short}); code != 422 {
		t.Fatalf("a missing group was accepted: %d", code)
	}

	// Read off paper: upper case and stray spaces are the operator's, not a mistake.
	sloppy := map[string]string{}
	for n, g := range confirmGroups(want, key) {
		sloppy[n] = " " + strings.ToUpper(g[:4]) + " " + g[4:] + " "
	}
	r.post("/api/recovery/ack", map[string]any{"groups": sloppy})
	// Waiting for "done", not merely for the screen to change: the install runs in a
	// goroutine writing under the rig's temp directory, and leaving it mid-flight races the
	// test's own cleanup.
	if v := r.waitView("done", func(v View) bool { return v.Screen == "done" }); !v.Recovery.Acknowledged {
		t.Fatalf("a correct transcription did not start the install: %+v", v)
	}
}

func TestInstallFailureAndRetry(t *testing.T) {
	r := newRig(t, &FakeSystem{FailStep: "40-boot-grub-zfs"})
	r.toRecovery()
	r.ackRecovery()
	v := r.waitView("failure", func(v View) bool { return v.Install.Status == "failed" })
	if v.Install.FailedStep != "40-boot-grub-zfs" || v.Screen != "install" {
		t.Fatalf("failure: %+v", v.Install)
	}
	if code, _ := r.call(http.MethodPost, "/api/back", map[string]any{}); code != 409 {
		t.Fatal("back was allowed after the disk was written")
	}
	r.post("/api/install/retry", map[string]any{})
	r.waitView("done", func(v View) bool { return v.Screen == "done" })
}

// TestResume: a backend restart keeps the progress but never a recovery key it did not keep.
func TestResume(t *testing.T) {
	r := newRig(t, nil)
	r.toRecovery()
	if v := r.w.View(); v.Screen != "recovery" || !v.Recovery.Generated {
		t.Fatalf("before restart: %s", v.Screen)
	}
	w2 := New(Config{StateFile: filepath.Join(r.dir, "state.json"), RunDir: r.dir}, r.sys)
	v := w2.View()
	if v.Screen != "recovery" || v.Recovery.Generated || !v.Recovery.Lost || !v.Account.HasPassword {
		t.Fatalf("after restart: screen %s recovery %+v account %+v", v.Screen, v.Recovery, v.Account)
	}
	if _, err := w2.RecoveryKey(); err == nil {
		t.Fatal("a key survived the restart")
	}
	// Ack is refused until a new key exists; re-submitting the account (password kept) makes one.
	if err := w2.AckRecovery(map[string]string{"1": "whatever"}); !errors.Is(err, ErrState) {
		t.Fatalf("ack without a key: %v", err)
	}
	w2.st.Screen = "account"
	if _, err := w2.SetAccount(AccountInput{Hostname: "node-a", Username: "admin"}); err != nil {
		t.Fatalf("keep password: %v", err)
	}
	if k, err := w2.RecoveryKey(); err != nil || len(k) != 64 {
		t.Fatalf("new key: %v", err)
	}
}

func TestEditionSwitchAndRoles(t *testing.T) {
	eds := DemoEditions()
	eds[0].Roles = []Role{{ID: "server", Title: map[string]string{"en": "Server"}}, {ID: "runner", Title: map[string]string{"en": "Runner"}}}
	eds[0].DefaultRole = "server"
	r := newRig(t, &FakeSystem{EditionsIn: eds})
	r.post("/api/welcome", map[string]any{})
	if code, _ := r.call(http.MethodPost, "/api/edition", map[string]string{"edition": "desktop-x"}); code != 422 {
		t.Fatal("an unknown edition was accepted")
	}
	v := View{}
	json.Unmarshal(mustJSON(r.post("/api/edition", map[string]string{"edition": "workstation"})), &v)
	if v.Screen != "edition" || v.Edition.SwitchTo != "workstation" {
		t.Fatalf("switch: %+v", v.Edition)
	}
	r.post("/api/reboot", map[string]any{})
	if code, m := r.call(http.MethodPost, "/api/edition", map[string]string{"edition": "server", "role": "nope"}); code != 422 || m["error"] != "err.role" {
		t.Fatalf("bad role: %d %v", code, m)
	}
	r.post("/api/edition", map[string]string{"edition": "server", "role": "runner"})
	if v := r.w.View(); v.Screen != "network" || v.Edition.Role != "runner" {
		t.Fatalf("role: %s %s", v.Screen, v.Edition.Role)
	}
}

func mustJSON(m map[string]any) []byte { b, _ := json.Marshal(m); return b }

func TestGuard(t *testing.T) {
	r := newRig(t, nil)
	do := func(host, xriver, ctype string) int {
		req, _ := http.NewRequest(http.MethodPost, r.srv.URL+"/api/welcome", strings.NewReader("{}"))
		req.Host = host
		if xriver != "" {
			req.Header.Set("X-River", xriver)
		}
		if ctype != "" {
			req.Header.Set("Content-Type", ctype)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	if c := do("evil.example.org:0", "1", "application/json"); c != http.StatusMisdirectedRequest {
		t.Errorf("foreign Host: %d", c)
	}
	if c := do("[::1]:0", "", "application/json"); c != http.StatusForbidden {
		t.Errorf("no X-River: %d", c)
	}
	if c := do("[::1]:0", "1", "text/plain"); c != http.StatusForbidden {
		t.Errorf("form post: %d", c)
	}
	if c := do("[::1]:0", "1", "application/json"); c != 200 {
		t.Errorf("good request: %d", c)
	}
	resp, _ := http.Get(r.srv.URL + "/")
	resp.Body.Close()
	if csp := resp.Header.Get("Content-Security-Policy"); !strings.Contains(csp, "frame-ancestors 'none'") {
		t.Errorf("CSP %q", csp)
	}
}

func TestPeerUID(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "net"), 0o755)
	// A client socket [::1]:40000 -> [::1]:47660 owned by uid 1234, and an IPv4 one.
	tcp6 := "  sl  local_address                         remote_address                        st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode\n" +
		"   0: 00000000000000000000000001000000:9C40 00000000000000000000000001000000:BA2C 01 00000000:00000000 00:00000000 00000000  1234        0 1 1 0000000000000000 20 4 30 10 -1\n"
	tcp := "  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode\n" +
		"   0: 0100007F:9C41 0100007F:BA2C 01 00000000:00000000 00:00000000 00000000     0        0 1\n"
	os.WriteFile(filepath.Join(dir, "net/tcp6"), []byte(tcp6), 0o644)
	os.WriteFile(filepath.Join(dir, "net/tcp"), []byte(tcp), 0o644)
	local := mustAddr("[::1]:47660")
	if uid, err := peerUID(dir, local, mustAddr("[::1]:40000")); err != nil || uid != 1234 {
		t.Fatalf("v6 peer: %d %v", uid, err)
	}
	if uid, err := peerUID(dir, mustAddr("127.0.0.1:47660"), mustAddr("127.0.0.1:40001")); err != nil || uid != 0 {
		t.Fatalf("v4 peer: %d %v", uid, err)
	}
	if _, err := peerUID(dir, local, mustAddr("[::1]:40002")); err == nil {
		t.Fatal("an unknown peer resolved")
	}
}

// TestModelsRequired: an edition with models_required refuses the account screen without the
// medium passphrase, and its steps get RUNINK_MODELS_REQUIRED=1 (72-models-payload then fails
// instead of deferring the models).
func TestModelsRequired(t *testing.T) {
	eds := DemoEditions()
	eds[0].ModelsRequired = true
	eds[0].Steps = append([]string{}, eds[0].Steps...)
	eds[0].Steps = slices.Insert(eds[0].Steps, 8, "72-models-payload")
	if err := eds[0].Validate(); err != nil {
		t.Fatal(err)
	}
	bad := eds[0]
	bad.Steps = []string{"00-preflight", "90-export"}
	if err := bad.Validate(); err == nil {
		t.Error("models_required without the models step validated")
	}
	r := newRig(t, &FakeSystem{EditionsIn: eds, USB: []USBDrive{{Dev: "/dev/sdx", Model: "Stick", SizeGB: 16}}})
	r.post("/api/welcome", map[string]any{})
	r.post("/api/edition", map[string]string{"edition": "server"})
	r.waitView("network", func(v View) bool { return v.Net.Status == "done" })
	r.post("/api/network/continue", map[string]bool{"offline": false})
	v := r.waitView("plan", func(v View) bool { return v.Machine.Status == "done" })
	var serials []string
	for _, d := range v.Machine.Disks {
		serials = append(serials, d.Serial)
	}
	r.post("/api/machine/confirm", map[string]any{"word": "ERASE", "disks": serials})
	in := AccountInput{Hostname: "node-a", Username: "admin", Password: pw, Password2: pw}
	code, m := r.call(http.MethodPost, "/api/account", in)
	if code != 422 || !strings.Contains(fmt.Sprint(m), "err.passphrase.required") {
		t.Fatalf("account without the passphrase: %d %v", code, m)
	}
	in.Passphrase = strings.Repeat("ab", 32)
	r.post("/api/account", in)
	r.ackRecovery()
	r.waitView("done", func(v View) bool { return v.Screen == "done" })
	env := strings.Join(r.sys.envOf("72-models-payload"), "\n")
	for _, want := range []string{"RUNINK_MODELS_REQUIRED=1", "RUNINK_MODELS_PASSPHRASE_FILE="} {
		if !strings.Contains(env, want) {
			t.Errorf("72-models-payload env lacks %q", want)
		}
	}
}

// A failed step must show the operator what failed (the step's own last lines and its exit
// code), where the whole log is, and what state the machine is left in; and the log must
// really be on disk, redacted like the screen's copy.
func TestInstallFailureReportsReasonLogAndState(t *testing.T) {
	r := newRig(t, &FakeSystem{FailStep: "50-runink-user"})
	r.toRecovery()
	r.ackRecovery()
	v := r.waitView("failure", func(v View) bool { return v.Install.Status == "failed" })
	in := v.Install
	if in.FailedStep != "50-runink-user" || in.Reason != "exit code 1" {
		t.Errorf("failed step/reason: %q / %q", in.FailedStep, in.Reason)
	}
	if got := strings.Join(in.Tail, "\n"); !strings.Contains(got, "simulated failure") || !strings.Contains(got, "the last thing it said") {
		t.Errorf("the step's last lines are not shown: %q", got)
	}
	if in.State != StatePartial {
		t.Errorf("a failure after the boot loader leaves %q, want %q", in.State, StatePartial)
	}
	if in.LogPath != filepath.Join(r.dir, "install.log") {
		t.Errorf("log path %q", in.LogPath)
	}
	b, err := os.ReadFile(in.LogPath)
	if err != nil || !strings.Contains(string(b), "simulated failure") || !strings.Contains(string(b), "step 50-runink-user exited: exit code 1") {
		t.Errorf("the log file does not carry the failure: %v %q", err, b)
	}
	if st, _ := os.Stat(in.LogPath); st == nil || st.Mode().Perm() != 0o600 {
		t.Errorf("log mode %v", st)
	}
}

// Before the boot loader is written a failure leaves nothing bootable, and the log is copied
// onto the new system when its tree exists (so it can be read after the next boot).
func TestInstallFailureEarlyAndLogOnTarget(t *testing.T) {
	r := newRig(t, &FakeSystem{FailStep: "40-boot-grub-zfs"})
	if err := os.MkdirAll(filepath.Join(r.dir, "target", "etc"), 0o755); err != nil {
		t.Fatal(err)
	}
	r.toRecovery()
	r.ackRecovery()
	v := r.waitView("failure", func(v View) bool { return v.Install.Status == "failed" })
	if v.Install.State != StateNotInstalled {
		t.Errorf("state %q", v.Install.State)
	}
	if v.Install.LogOnTarget != "/var/log/install.log" {
		t.Errorf("log on target %q", v.Install.LogOnTarget)
	}
	if b, err := os.ReadFile(filepath.Join(r.dir, "target/var/log/install.log")); err != nil || !strings.Contains(string(b), "simulated failure") {
		t.Errorf("target log: %v %q", err, b)
	}
}

// A step that succeeds but prints an install-notice (a deferred step) is not silent: the
// notice is kept for the done screen.
func TestInstallNoticesSurviveToTheDoneScreen(t *testing.T) {
	r := newRig(t, &FakeSystem{NoticeStep: "50-runink-user"})
	r.toRecovery()
	r.ackRecovery()
	v := r.waitView("done", func(v View) bool { return v.Screen == "done" })
	if len(v.Install.Notices) != 1 || !strings.Contains(v.Install.Notices[0], "50-runink-user: the models were deferred") {
		t.Errorf("notices: %v", v.Install.Notices)
	}
}

func TestMachineViewShowsMemoryBreakdownAndFirmwareNote(t *testing.T) {
	probe, err := os.ReadFile("../planner/testdata/evo-x3-64g-of-128g.probe.json")
	if err != nil {
		t.Fatal(err)
	}
	var p hw.Probe
	if err := json.Unmarshal(probe, &p); err != nil {
		t.Fatal(err)
	}
	pl := planner.Build(&p, nil, planner.Options{Profile: planner.ProfileServer})
	planJSON, _ := json.Marshal(pl)
	w := New(Config{}, &FakeSystem{})
	w.st.Machine = machineState{Status: "done", Probe: probe, Plan: planJSON}
	mv := w.machineViewLocked()
	if len(mv.MemoryLines) < 4 || !strings.HasPrefix(mv.MemoryLines[2], "Installed total: 128.0 GB") {
		t.Errorf("memory lines: %v", mv.MemoryLines)
	}
	if mv.MemoryFirmware == nil || mv.MemoryFirmware.Kind != planner.MemFirmwareGPU {
		t.Errorf("firmware note: %+v", mv.MemoryFirmware)
	}
}
