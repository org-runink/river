// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

package wizard

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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
	w := New(Config{Version: "test", StateFile: filepath.Join(dir, "state.json"), RunDir: dir, Logf: logs.logf}, sys)
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
	// "I have written it down" is required.
	if code, m := r.call(http.MethodPost, "/api/recovery/ack", map[string]bool{"written": false}); code != 422 || m["error"] != "err.recovery.ack" {
		t.Fatalf("ack without the box: %d %v", code, m)
	}
	r.post("/api/recovery/ack", map[string]bool{"written": true})
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
	r.post("/api/recovery/ack", map[string]bool{"written": true})
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

func TestInstallFailureAndRetry(t *testing.T) {
	r := newRig(t, &FakeSystem{FailStep: "40-boot-grub-zfs"})
	r.toRecovery()
	r.post("/api/recovery/ack", map[string]bool{"written": true})
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
	if err := w2.AckRecovery(true); !errors.Is(err, ErrState) {
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
