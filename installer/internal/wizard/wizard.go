// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

// Package wizard is the backend of the Runink River graphical installer (river-installer):
// a small state machine behind a loopback HTTP/JSON API, and the static web UI it serves.
//
// The same binary serves two flows:
//
//	install    the live medium: welcome, what to install, network, this computer (the one
//	           erase confirmation), name and admin, recovery key, install, finished
//	firstboot  an installed server's first start: network, clock, firewall, k0s Ready, then
//	           the downstream's setup pages, then "ready"
//
// It never re-implements the installer: every action runs the existing pieces (see System).
// Secrets (the admin password, the recovery key, a Wi-Fi passphrase, a setup page's URL)
// are held in memory, never logged, and served only to root and the kiosk user.
package wizard

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/org-runink/river/installer/internal/hw"
	"github.com/org-runink/river/installer/internal/planner"
	"github.com/org-runink/river/installer/internal/shacrypt"
)

// Screens of the install flow, in order.
var screens = []string{"welcome", "edition", "network", "machine", "account", "recovery", "install", "done"}

func screenIndex(s string) int {
	for i, x := range screens {
		if x == s {
			return i
		}
	}
	return -1
}

// Config is how river-installer starts the wizard.
type Config struct {
	Version   string // /etc/runink-os-version
	StateFile string // non-secret progress, so a restarted backend resumes (0600, on /run)
	RunDir    string // private scratch dir on /run (0700)
	Pool      string
	Logf      func(format string, args ...any)
}

// Wizard is the install flow.
type Wizard struct {
	cfg  Config
	sys  System
	self Edition // the running image's edition descriptor
	// liveHost is the live medium's own hostname when the installer started (runink-live on Runink
	// River). It is never offered as the installed machine's name: river#136, every install that
	// kept the default was named after the stick.
	liveHost string
	eds      []Edition

	mu   sync.Mutex
	st   persisted
	key  string // the recovery key: memory only, never persisted, never logged
	pass string // the medium's payload passphrase (optional): memory only
	logs []string
	hub  *hub
	now  func() time.Time
}

// persisted is what survives a backend restart (a 0600 file on /run, which is tmpfs and never
// reaches the installed node). It holds no recovery key and no password, only its hash.
type persisted struct {
	Screen   string       `json:"screen"`
	Lang     string       `json:"lang"`
	Keyboard string       `json:"keyboard"`
	Edition  string       `json:"edition"`
	Role     string       `json:"role,omitempty"`
	SwitchTo string       `json:"switch_to,omitempty"`
	Net      netView      `json:"net"`
	Machine  machineState `json:"machine"`
	Account  accountState `json:"account"`
	Recovery recoveryView `json:"recovery"`
	Install  installView  `json:"install"`
}

type netView struct {
	Status  string    `json:"status"` // idle | running | done
	State   *NetState `json:"state,omitempty"`
	Skipped bool      `json:"skipped"`
	Error   string    `json:"error,omitempty"`
	Wifi    []WifiNet `json:"wifi,omitempty"`
}

type machineState struct {
	Status      string          `json:"status"` // idle | running | done | failed
	Lab         bool            `json:"lab"`
	Probe       json.RawMessage `json:"probe,omitempty"`
	Plan        json.RawMessage `json:"plan,omitempty"`
	Refused     bool            `json:"refused"`
	Confirmed   []string        `json:"confirmed,omitempty"`
	PlanHash    string          `json:"plan_hash,omitempty"`
	EraseDialog bool            `json:"erase_dialog"`
	Error       string          `json:"error,omitempty"`
}

type accountState struct {
	Hostname     string `json:"hostname"`
	Username     string `json:"username"`
	SSHKeys      string `json:"ssh_keys,omitempty"`
	PasswordHash string `json:"password_hash,omitempty"`
	Done         bool   `json:"done"`
}

type recoveryView struct {
	Generated    bool   `json:"generated"`
	Acknowledged bool   `json:"acknowledged"`
	SavedTo      string `json:"saved_to,omitempty"`
	Lost         bool   `json:"lost,omitempty"` // a backend restart dropped an unused key
	// Confirm names the groups of the key that must be typed back before the install
	// starts, as 1-based indices into the 8 groups the screen shows. A tickbox proved
	// nothing: it is satisfied by a glance, and this key is shown exactly once, for a
	// disk it is the only way to decrypt — a mistranscribed character is discovered at
	// the next boot, when nothing can be done about it. Typing randomly chosen groups
	// back proves the copy that leaves the room is readable.
	Confirm []int `json:"confirm,omitempty"`
}

type installView struct {
	Status     string      `json:"status"` // idle | running | failed | done
	Steps      []stepState `json:"steps"`
	Error      string      `json:"error,omitempty"`
	FailedStep string      `json:"failed_step,omitempty"`
	StartedAt  int64       `json:"started_at,omitempty"`
	Percent    int         `json:"percent"`
	ETA        int         `json:"eta_seconds"`
}

type stepState struct {
	ID       string `json:"id"`
	Status   string `json:"status"` // pending | running | done | failed | skipped
	Progress int    `json:"progress"`
	Estimate int    `json:"estimate"` // seconds
	Elapsed  int    `json:"elapsed"`
}

// New builds a wizard, resuming from cfg.StateFile when it exists.
func New(cfg Config, sys System) *Wizard {
	if cfg.Logf == nil {
		cfg.Logf = func(string, ...any) {}
	}
	if cfg.Pool == "" {
		cfg.Pool = "zriver"
	}
	w := &Wizard{cfg: cfg, sys: sys, hub: newHub(), now: time.Now}
	w.liveHost, _ = os.Hostname()
	w.eds = sys.Editions()
	w.self = selfEdition(w.eds)
	w.st = persisted{Screen: "welcome", Lang: "en", Keyboard: "us", Edition: w.self.ID, Role: w.self.DefaultRole}
	w.st.Account.Username = w.self.AdminUser
	if b, err := os.ReadFile(cfg.StateFile); err == nil && cfg.StateFile != "" {
		var p persisted
		if json.Unmarshal(b, &p) == nil && screenIndex(p.Screen) >= 0 {
			w.st = p
			// The recovery key lived only in the previous process. If it was never used, a
			// new one is made and shown again; the old one protects nothing.
			if w.st.Recovery.Generated && w.st.Install.Status != "done" {
				w.st.Recovery = recoveryView{Lost: true}
				if screenIndex(w.st.Screen) > screenIndex("recovery") {
					w.st.Screen = "recovery"
				}
			}
			if w.st.Install.Status == "running" {
				w.st.Install.Status = "failed"
				w.st.Install.Error = "interrupted"
			}
			for _, s := range []*string{&w.st.Net.Status, &w.st.Machine.Status} {
				if *s == "running" {
					*s = "idle"
				}
			}
			cfg.Logf("resumed at screen %s", w.st.Screen)
		}
	}
	return w
}

// ---- view -----------------------------------------------------------------------------------

// View is the state the UI renders. It carries no secret.
type View struct {
	Mode     string         `json:"mode"`
	Profile  string         `json:"profile"`
	Version  string         `json:"version"`
	Screen   string         `json:"screen"`
	Lang     string         `json:"lang"`
	Keyboard string         `json:"keyboard"`
	Edition  editionView    `json:"edition"`
	Net      netView        `json:"net"`
	Machine  *MachineView   `json:"machine"`
	Account  accountView    `json:"account"`
	Recovery recoveryView   `json:"recovery"`
	Install  installView    `json:"install"`
	Options  map[string]any `json:"options"`
}

type editionView struct {
	Choice   string    `json:"choice"`
	Self     string    `json:"self"`
	List     []Edition `json:"list"` // the editions on the medium (present ones)
	SwitchTo string    `json:"switch_to,omitempty"`
	Role     string    `json:"role,omitempty"`
}

type accountView struct {
	Hostname    string `json:"hostname"`
	Username    string `json:"username"`
	HasPassword bool   `json:"has_password"`
	SSHKeys     int    `json:"ssh_keys"`
	Done        bool   `json:"done"`
	Payloads    bool   `json:"payloads"`
}

// MachineView is the friendly summary of the probe and plan.
type MachineView struct {
	Status      string      `json:"status"`
	Error       string      `json:"error,omitempty"`
	CPU         string      `json:"cpu,omitempty"`
	Cores       int         `json:"cores,omitempty"`
	Threads     int         `json:"threads,omitempty"`
	PsABI       string      `json:"psabi,omitempty"`
	MemoryGiB   float64     `json:"memory_gib,omitempty"`
	Virtual     bool        `json:"virtual"`
	UEFI        bool        `json:"uefi"`
	Verdict     string      `json:"verdict,omitempty"`
	Refusals    []string    `json:"refusals,omitempty"`
	Warnings    []string    `json:"warnings,omitempty"`
	Layout      string      `json:"layout,omitempty"`
	UsableBytes uint64      `json:"usable_bytes,omitempty"`
	Disks       []diskView  `json:"disks,omitempty"`
	Unused      []unusedRow `json:"unused,omitempty"`
	Lab         bool        `json:"lab"`
	Confirmed   bool        `json:"confirmed"`
	EraseDialog bool        `json:"erase_dialog"`
}

type diskView struct {
	Name      string `json:"name"`
	Model     string `json:"model"`
	SizeBytes uint64 `json:"size_bytes"`
	Serial    string `json:"serial"` // the confirm_id: serial, WWN or NOSERIAL-<name>-<size>G
	Kind      string `json:"kind"`
	Role      string `json:"role"` // boot | data | special
}

type unusedRow struct {
	Name   string `json:"name"`
	Model  string `json:"model,omitempty"`
	Reason string `json:"reason"`
}

func (w *Wizard) viewLocked() View {
	v := View{
		Mode: "install", Profile: w.self.ID, Version: w.cfg.Version, Screen: w.st.Screen,
		Lang: w.st.Lang, Keyboard: w.st.Keyboard,
		Edition: editionView{Choice: w.st.Edition, Self: w.self.ID, List: w.presentEditions(), SwitchTo: w.st.SwitchTo,
			Role: w.st.Role},
		Net:      w.st.Net,
		Machine:  w.machineViewLocked(),
		Recovery: w.st.Recovery,
		Install:  w.st.Install,
		Account: accountView{Hostname: w.st.Account.Hostname, Username: w.st.Account.Username,
			HasPassword: w.st.Account.PasswordHash != "", Done: w.st.Account.Done, Payloads: w.selfTakesPayloads() && w.sys.MediumPayloads()},
		Options: map[string]any{"languages": sortedKeys(languages), "keyboards": sortedKeys(layouts),
			"erase_word": eraseWords[w.st.Lang]},
	}
	if v.Account.Hostname == "" {
		v.Account.Hostname = w.defaultHostname()
	}
	// The view leaves the lock (it is marshalled after): it must share no slice with the state.
	v.Install.Steps = append([]stepState(nil), w.st.Install.Steps...)
	v.Net.Wifi = append([]WifiNet(nil), w.st.Net.Wifi...)
	if w.st.Account.SSHKeys != "" {
		v.Account.SSHKeys = strings.Count(w.st.Account.SSHKeys, "\n")
	}
	return v
}

func (w *Wizard) defaultHostname() string {
	if w.st.Net.State != nil && w.st.Net.State.Hostname != "" && validHostname(w.st.Net.State.Hostname) == "" &&
		w.st.Net.State.Hostname != "runink" && w.st.Net.State.Hostname != w.liveHost {
		return w.st.Net.State.Hostname
	}
	return w.self.Hostname
}

// selfTakesPayloads: the running edition unpacks the medium's encrypted payloads (the model or a
// downstream payload step), so the account screen offers the medium passphrase. A medium that
// carries several editions carries its payloads for the one that takes them; the others never
// ask for the passphrase.
func (w *Wizard) selfTakesPayloads() bool {
	for _, s := range w.self.Steps {
		if s == "72-models-payload" || s == "73-downstream-payloads" || s == "74-cloud-payload" {
			return true
		}
	}
	return false
}

// presentEditions are the editions the operator can start from this medium; the running one
// first.
func (w *Wizard) presentEditions() []Edition {
	out := []Edition{w.self}
	for _, e := range w.eds {
		if e.Present && !e.Self && e.ID != w.self.ID {
			out = append(out, e)
		}
	}
	return out
}

func (w *Wizard) machineViewLocked() *MachineView {
	m := &w.st.Machine
	mv := &MachineView{Status: m.Status, Error: m.Error, Lab: m.Lab, EraseDialog: m.EraseDialog,
		Confirmed: len(m.Confirmed) > 0 && m.PlanHash == hashOf(m.Plan)}
	if len(m.Probe) > 0 {
		var p hw.Probe
		if json.Unmarshal(m.Probe, &p) == nil {
			mv.CPU = strings.TrimSpace(p.CPU.Model)
			mv.Cores, mv.Threads, mv.PsABI = p.CPU.PhysicalCores, p.CPU.Threads, p.CPU.PsABI
			mv.MemoryGiB = float64(p.Memory.TotalBytes) / (1 << 30)
			mv.Virtual, mv.UEFI = p.Host.Virtual, p.Firmware.UEFI
		}
	}
	if len(m.Plan) > 0 {
		var pl planner.Plan
		if json.Unmarshal(m.Plan, &pl) == nil {
			mv.Verdict, mv.Refusals, mv.Warnings = pl.Verdict, pl.Refusals, pl.Warnings
			mv.Layout, mv.UsableBytes = pl.Storage.Layout, pl.Storage.UsableBytes
			special := map[string]bool{}
			if pl.Storage.SpecialVdev != nil {
				for _, d := range pl.Storage.SpecialVdev.Disks {
					special[d] = true
				}
			}
			for _, d := range pl.Storage.Disks {
				role := "data"
				if d.Name == pl.Storage.BootDisk {
					role = "boot"
				} else if special[d.Name] || special[d.ConfirmID] {
					role = "special"
				}
				mv.Disks = append(mv.Disks, diskView{Name: d.Name, Model: d.Model, SizeBytes: d.SizeBytes,
					Serial: d.ConfirmID, Kind: d.Kind, Role: role})
			}
			for _, u := range pl.Storage.Unused {
				mv.Unused = append(mv.Unused, unusedRow{Name: u.Name, Reason: u.Reason})
			}
		}
	}
	return mv
}

// View returns the current UI state.
func (w *Wizard) View() View {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.viewLocked()
}

// changedLocked persists the state and pushes it to every UI.
func (w *Wizard) changedLocked() {
	if w.cfg.StateFile != "" {
		if b, err := json.Marshal(w.st); err == nil {
			tmp := w.cfg.StateFile + ".tmp"
			if os.WriteFile(tmp, b, 0o600) == nil {
				_ = os.Rename(tmp, w.cfg.StateFile)
			}
		}
	}
	w.hub.publish("state", w.viewLocked())
}

// ---- actions ----------------------------------------------------------------------------------

// ErrState is returned when an action does not fit the current state.
var ErrState = errors.New("err.state")

// APIError is a user-facing error: a message key the UI translates.
type APIError struct{ Key string }

func (e APIError) Error() string { return e.Key }

func (w *Wizard) installStarted() bool {
	s := w.st.Install.Status
	return s == "running" || s == "done" || s == "failed"
}

// SetLocale changes the language and keyboard; any time before the install finishes.
func (w *Wizard) SetLocale(lang, keyboard string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if lang != "" {
		if _, ok := languages[lang]; !ok {
			return APIError{"err.lang"}
		}
		if lang != w.st.Lang && keyboard == "" {
			keyboard = defaultLayout[lang]
		}
		w.st.Lang = lang
	}
	if keyboard != "" {
		l, ok := layouts[keyboard]
		if !ok {
			return APIError{"err.keyboard"}
		}
		if keyboard != w.st.Keyboard {
			if err := w.sys.ApplyKeyboard(l); err != nil {
				w.cfg.Logf("keyboard %s: %v", keyboard, err)
			}
		}
		w.st.Keyboard = keyboard
	}
	w.changedLocked()
	return nil
}

// Next leaves the welcome screen.
func (w *Wizard) Welcome() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.st.Screen != "welcome" {
		return ErrState
	}
	w.st.Screen = "edition"
	w.changedLocked()
	return nil
}

// ChooseEdition picks Server or Workstation (and a downstream role). Choosing the image the
// medium did not boot does not install it: the UI says how to start that image instead.
func (w *Wizard) ChooseEdition(edition, role string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.st.Screen != "edition" {
		return ErrState
	}
	known := false
	for _, e := range w.presentEditions() {
		known = known || e.ID == edition
	}
	if !known {
		return APIError{"err.edition"}
	}
	w.st.Edition = edition
	if edition != w.self.ID {
		// Another image on the same stick: it installs from its own live session.
		w.st.SwitchTo = edition
		w.changedLocked()
		return nil
	}
	w.st.SwitchTo = ""
	w.st.Role = ""
	if len(w.self.Roles) > 0 {
		for _, r := range w.self.Roles {
			if r.ID == role {
				w.st.Role = role
			}
		}
		if w.st.Role == "" {
			return APIError{"err.role"}
		}
	}
	w.st.Screen = "network"
	w.changedLocked()
	if w.st.Net.Status == "" || w.st.Net.Status == "idle" {
		w.startNetAutoLocked()
	}
	return nil
}

// Back goes one screen back, until the install has started.
func (w *Wizard) Back() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	i := screenIndex(w.st.Screen)
	if i <= 0 || w.installStarted() || w.st.Screen == "install" || w.st.Screen == "done" {
		return ErrState
	}
	w.st.Screen = screens[i-1]
	w.st.SwitchTo = ""
	w.st.Machine.EraseDialog = false
	w.changedLocked()
	return nil
}

func (w *Wizard) startNetAutoLocked() {
	w.st.Net.Status, w.st.Net.Error = "running", ""
	w.changedLocked()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		ns, err := w.sys.NetAuto(ctx)
		w.mu.Lock()
		defer w.mu.Unlock()
		w.st.Net.Status = "done"
		if err != nil {
			w.st.Net.Error = "err.net.failed"
			w.cfg.Logf("network: %v", err)
		} else {
			w.st.Net.State = &ns
		}
		w.changedLocked()
	}()
}

// NetAuto (re)runs the automatic network setup.
func (w *Wizard) NetAuto() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.st.Screen != "network" || w.st.Net.Status == "running" {
		return ErrState
	}
	w.startNetAutoLocked()
	return nil
}

// WifiScan lists networks in range (and remembers them for the UI).
func (w *Wizard) WifiScan(ctx context.Context) ([]WifiNet, error) {
	nets, err := w.sys.WifiScan(ctx)
	if err != nil {
		return nil, APIError{"err.wifi.scan"}
	}
	w.mu.Lock()
	w.st.Net.Wifi = nets
	w.changedLocked()
	w.mu.Unlock()
	return nets, nil
}

// WifiConnect joins a Wi-Fi network. The passphrase is passed through and forgotten.
func (w *Wizard) WifiConnect(ssid, psk string) error {
	if k := validWifi(ssid, psk); k != "" {
		return APIError{k}
	}
	w.mu.Lock()
	if w.st.Screen != "network" || w.st.Net.Status == "running" {
		w.mu.Unlock()
		return ErrState
	}
	w.st.Net.Status, w.st.Net.Error = "running", ""
	w.changedLocked()
	w.mu.Unlock()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		ns, err := w.sys.WifiConnect(ctx, ssid, psk)
		w.mu.Lock()
		defer w.mu.Unlock()
		w.st.Net.Status = "done"
		if err != nil {
			w.st.Net.Error = "err.wifi.connect"
			w.cfg.Logf("wifi: connect failed") // the SSID and passphrase are not logged
		} else {
			w.st.Net.State = &ns
		}
		w.changedLocked()
	}()
	return nil
}

// NetContinue leaves the network screen: online, LAN-only or offline are all fine.
func (w *Wizard) NetContinue(offline bool) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.st.Screen != "network" || w.st.Net.Status == "running" {
		return ErrState
	}
	w.st.Net.Skipped = offline
	w.st.Screen = "machine"
	w.changedLocked()
	if w.st.Machine.Status != "done" {
		w.startProbeLocked()
	}
	return nil
}

func (w *Wizard) startProbeLocked() {
	lab := w.st.Machine.Lab
	w.st.Machine = machineState{Status: "running", Lab: lab}
	w.changedLocked()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		probe, err := w.sys.Probe(ctx)
		var plan []byte
		refused := false
		if err == nil {
			plan, refused, err = w.sys.Plan(ctx, probe, lab, w.self.PlanProfile, w.self.PlanModels)
		}
		w.mu.Lock()
		defer w.mu.Unlock()
		if err != nil {
			w.st.Machine.Status, w.st.Machine.Error = "failed", "err.machine.probe"
			w.cfg.Logf("probe/plan: %v", err)
		} else {
			w.st.Machine.Status = "done"
			w.st.Machine.Probe, w.st.Machine.Plan, w.st.Machine.Refused = probe, plan, refused
		}
		w.changedLocked()
	}()
}

// Reprobe runs the hardware probe and plan again (lab waives the documented minimums).
func (w *Wizard) Reprobe(lab bool) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.st.Screen != "machine" || w.st.Machine.Status == "running" {
		return ErrState
	}
	w.st.Machine.Lab = lab
	w.startProbeLocked()
	return nil
}

// EraseDialog opens or closes the "type ERASE" dialog (state, so a kiosk that reconnects
// shows the same thing, and so the test driver can photograph it).
func (w *Wizard) EraseDialog(open bool) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.st.Screen != "machine" || w.st.Machine.Status != "done" || (open && w.st.Machine.Refused) {
		return ErrState
	}
	w.st.Machine.EraseDialog = open
	w.changedLocked()
	return nil
}

// ConfirmErase is the one safety gate: the typed word, and the exact disks the UI showed
// (their serials). Anything but the plan's disks, all of them, is refused.
func (w *Wizard) ConfirmErase(word string, serials []string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.st.Screen != "machine" || w.st.Machine.Status != "done" || w.st.Machine.Refused {
		return ErrState
	}
	if !eraseWordOK(w.st.Lang, word) {
		return APIError{"err.erase.word"}
	}
	mv := w.machineViewLocked()
	if len(mv.Disks) == 0 {
		return APIError{"err.erase.nodisk"}
	}
	want := map[string]bool{}
	for _, d := range mv.Disks {
		want[d.Serial] = true
	}
	got := map[string]bool{}
	for _, s := range serials {
		if !want[s] {
			return APIError{"err.erase.disks"}
		}
		got[s] = true
	}
	if len(got) != len(want) {
		return APIError{"err.erase.disks"}
	}
	w.st.Machine.Confirmed = append([]string(nil), serials...)
	w.st.Machine.PlanHash = hashOf(w.st.Machine.Plan)
	w.st.Machine.EraseDialog = false
	w.st.Screen = "account"
	w.cfg.Logf("erase confirmed for %d disk(s)", len(serials))
	w.changedLocked()
	return nil
}

// AccountInput is the name-and-admin form.
type AccountInput struct {
	Hostname  string `json:"hostname"`
	Username  string `json:"username"`
	Password  string `json:"password"`
	Password2 string `json:"password2"`
	SSHKeys   string `json:"ssh_keys"`
	// Passphrase opens the medium's encrypted payloads (private media only; optional).
	Passphrase string `json:"passphrase"`
}

// SetAccount validates the form, hashes the password and forgets it.
func (w *Wizard) SetAccount(in AccountInput) (map[string]string, error) {
	errs := map[string]string{}
	h := strings.ToLower(strings.TrimSpace(in.Hostname))
	u := strings.TrimSpace(in.Username)
	if k := validHostname(h); k != "" {
		errs["hostname"] = k
	}
	if k := validUsername(u); k != "" {
		errs["username"] = k
	}
	w.mu.Lock()
	keepHash := in.Password == "" && in.Password2 == "" && w.st.Account.PasswordHash != ""
	w.mu.Unlock()
	if !keepHash {
		if k := validPassword(in.Password, in.Password2); k != "" {
			errs["password"] = k
		}
	}
	keys, k := validSSHKeys(in.SSHKeys)
	if k != "" {
		errs["ssh_keys"] = k
	}
	if len(in.Passphrase) > 1024 || strings.ContainsAny(in.Passphrase, "\n\r\x00") {
		errs["passphrase"] = "err.passphrase.invalid"
	} else if w.self.ModelsRequired && in.Passphrase == "" && !w.hasPass() {
		errs["passphrase"] = "err.passphrase.required"
	}
	if len(errs) > 0 {
		return errs, APIError{"err.form"}
	}
	var hash string
	if !keepHash {
		var err error
		if hash, err = shacrypt.Hash([]byte(in.Password), 0); err != nil {
			return nil, err
		}
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.st.Screen != "account" {
		return nil, ErrState
	}
	w.st.Account.Hostname, w.st.Account.Username, w.st.Account.SSHKeys = h, u, keys
	if hash != "" {
		w.st.Account.PasswordHash = hash
	}
	if in.Passphrase != "" {
		w.pass = in.Passphrase
	}
	w.st.Account.Done = true
	w.st.Screen = "recovery"
	if !w.st.Recovery.Generated {
		w.generateKeyLocked()
	}
	w.changedLocked()
	return nil, nil
}

func (w *Wizard) generateKeyLocked() {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("crypto/rand: " + err.Error())
	}
	w.key = hex.EncodeToString(b[:])
	w.st.Recovery = recoveryView{Generated: true, Confirm: pickConfirmGroups()}
}

// keyGroups is how many groups groupKey renders a 64-hex key as; keyConfirmGroups is how many
// of them the operator types back on the recovery screen. Three of eight is enough to catch a
// transcription slip anywhere in the key without making the screen a typing exercise.
const (
	keyGroups        = 8
	keyConfirmGroups = 3
)

// pickConfirmGroups chooses, at random, which groups must be typed back, so an operator who
// installs twice cannot learn which boxes to fill from memory. Returned sorted and 1-based.
func pickConfirmGroups() []int {
	var b [keyGroups]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("crypto/rand: " + err.Error())
	}
	g := make([]int, keyGroups)
	for i := range g {
		g[i] = i + 1
	}
	// Fisher-Yates. The modulo bias over at most 8 values is irrelevant: this picks which
	// groups the operator transcribes, not a secret.
	for i := keyGroups - 1; i > 0; i-- {
		j := int(b[i]) % (i + 1)
		g[i], g[j] = g[j], g[i]
	}
	g = g[:keyConfirmGroups]
	sort.Ints(g)
	return g
}

// RecoveryKey returns the key while it may still be shown: generated, and the install not
// finished. Callers must have checked the peer is root or the kiosk.
func (w *Wizard) RecoveryKey() (string, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.key == "" || w.st.Install.Status == "done" {
		return "", ErrState
	}
	return w.key, nil
}

// USBDrives lists where the key can be saved: removable, not the medium, not an install target.
func (w *Wizard) USBDrives(ctx context.Context) ([]USBDrive, error) {
	w.mu.Lock()
	exclude := w.targetNamesLocked()
	w.mu.Unlock()
	return w.sys.Removable(ctx, exclude)
}

func (w *Wizard) targetNamesLocked() []string {
	var out []string
	for _, d := range w.machineViewLocked().Disks {
		out = append(out, d.Name, d.Serial)
	}
	return out
}

// SaveKeyToUSB writes the recovery key as a text file on a removable drive.
func (w *Wizard) SaveKeyToUSB(ctx context.Context, dev string) error {
	drives, err := w.USBDrives(ctx)
	if err != nil {
		return APIError{"err.usb.list"}
	}
	ok := false
	for _, d := range drives {
		ok = ok || d.Dev == dev
	}
	if !ok {
		return APIError{"err.usb.device"}
	}
	w.mu.Lock()
	key, host := w.key, w.st.Account.Hostname
	w.mu.Unlock()
	if key == "" {
		return ErrState
	}
	content := fmt.Sprintf("Runink River recovery key for %s\r\n\r\n    %s\r\n\r\n"+
		"Type it without the spaces when the computer asks for the passphrase at start-up.\r\n"+
		"Keep this file somewhere safe and away from the computer. Without the key, nothing on\r\n"+
		"its disk can be read if the key provider is lost.\r\n", host, groupKey(key))
	name := "runink-river-recovery-key-" + host + ".txt"
	if err := w.sys.SaveToUSB(ctx, dev, name, []byte(content)); err != nil {
		w.cfg.Logf("usb save to %s failed: %v", dev, err)
		return APIError{"err.usb.write"}
	}
	w.mu.Lock()
	w.st.Recovery.SavedTo = dev
	w.changedLocked()
	w.mu.Unlock()
	return nil
}

func groupKey(k string) string {
	var parts []string
	for i := 0; i < len(k); i += 8 {
		j := i + 8
		if j > len(k) {
			j = len(k)
		}
		parts = append(parts, k[i:j])
	}
	return strings.Join(parts, " ")
}

// AckRecovery checks the groups of the recovery key typed back against the key itself and,
// when every one matches, starts the install. `typed` is keyed by the 1-based group number, as
// Recovery.Confirm names them; spaces and letter case are ignored, since the screen prints the
// key in lower-case groups and an operator reading from paper may do neither.
//
// It returns err.recovery.groups on a mismatch and changes nothing: the key is still on screen,
// so the operator corrects their copy and tries again. There is deliberately no attempt limit —
// locking someone out of the one screen that shows the key would brick the disk they are
// installing, which is a worse outcome than any amount of retyping.
func (w *Wizard) AckRecovery(typed map[string]string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.st.Screen != "recovery" || w.key == "" {
		return ErrState
	}
	groups := strings.Fields(groupKey(w.key))
	if len(w.st.Recovery.Confirm) == 0 {
		return ErrState
	}
	for _, n := range w.st.Recovery.Confirm {
		if n < 1 || n > len(groups) {
			return ErrState
		}
		got := strings.ToLower(strings.Join(strings.Fields(typed[strconv.Itoa(n)]), ""))
		if got != groups[n-1] {
			return APIError{"err.recovery.groups"}
		}
	}
	w.st.Recovery.Acknowledged = true
	w.st.Screen = "install"
	w.changedLocked()
	return w.startInstallLocked()
}

// RetryInstall re-runs the install after a failure.
func (w *Wizard) RetryInstall() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.st.Screen != "install" || w.st.Install.Status != "failed" {
		return ErrState
	}
	if w.key == "" {
		// The key died with a previous backend: show a new one before anything is written.
		w.generateKeyLocked()
		w.st.Screen = "recovery"
		w.st.Install = installView{}
		w.changedLocked()
		return nil
	}
	return w.startInstallLocked()
}

// Reboot restarts the machine (from the finished screen, or the edition switch).
func (w *Wizard) Reboot() error {
	w.mu.Lock()
	ok := w.st.Screen == "done" || w.st.SwitchTo != ""
	w.mu.Unlock()
	if !ok {
		return ErrState
	}
	go func() {
		time.Sleep(time.Second) // let the response reach the UI
		if err := w.sys.Reboot(); err != nil {
			w.cfg.Logf("reboot: %v", err)
		}
	}()
	return nil
}

// Log returns the install log (redacted as it was captured).
func (w *Wizard) Log() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]string(nil), w.logs...)
}

// ---- install -------------------------------------------------------------------------------

type stepDef struct {
	id       string
	estimate int // seconds, for the progress bar and the time left
}

// stepEstimates are rough durations (seconds) for the progress bar and the time left; a step
// the table does not know counts as 10 s.
var stepEstimates = map[string]int{
	"00-preflight": 2, "05-hwplan-verify": 4, "10-disk-zfs": 25, "20-clone-rootfs": 240,
	"30-target-config": 3, "32-locale-keyboard": 8, "35-pacman-keyring": 20, "40-boot-grub-zfs": 90, "50-runink-user": 5,
	"72-models-payload": 5, "73-downstream-payloads": 10, "75-install-plan": 1, "77-firstboot-ui": 2,
	"80-enable-s6": 15, "90-export": 10,
}

// stepPlan is the edition's steps that exist on this image.
func (w *Wizard) stepPlan() []stepDef {
	var out []stepDef
	for _, id := range w.self.Steps {
		if !w.sys.HasStep(id) {
			continue
		}
		e, ok := stepEstimates[id]
		if !ok {
			e = 10
		}
		out = append(out, stepDef{id, e})
	}
	return out
}

func (w *Wizard) startInstallLocked() error {
	if w.st.Account.PasswordHash == "" || !w.st.Account.Done {
		return APIError{"err.account.missing"}
	}
	if w.st.Machine.Status != "done" || w.st.Machine.Refused || len(w.st.Machine.Confirmed) == 0 ||
		w.st.Machine.PlanHash != hashOf(w.st.Machine.Plan) {
		return APIError{"err.erase.missing"}
	}
	if w.key == "" || !w.st.Recovery.Acknowledged {
		return APIError{"err.recovery.ack"}
	}
	steps := w.stepPlan()
	w.st.Install = installView{Status: "running", StartedAt: w.now().Unix()}
	for _, d := range steps {
		w.st.Install.Steps = append(w.st.Install.Steps, stepState{ID: d.id, Status: "pending", Estimate: d.estimate})
	}
	w.logs = nil
	ctx, cancel := context.WithCancel(context.Background())
	w.changedLocked()
	go func() {
		defer cancel()
		w.runInstall(ctx)
	}()
	return nil
}

func (w *Wizard) runInstall(ctx context.Context) {
	w.mu.Lock()
	plan := append([]byte(nil), w.st.Machine.Plan...)
	confirmed := append([]string(nil), w.st.Machine.Confirmed...)
	w.mu.Unlock()

	fail := func(step, key string, err error) {
		w.mu.Lock()
		defer w.mu.Unlock()
		w.st.Install.Status, w.st.Install.Error, w.st.Install.FailedStep = "failed", key, step
		for i := range w.st.Install.Steps {
			if w.st.Install.Steps[i].ID == step {
				w.st.Install.Steps[i].Status = "failed"
			}
		}
		w.appendLogLocked(fmt.Sprintf("installer: %s failed: %v", step, err))
		w.cfg.Logf("install failed at %s", step)
		w.changedLocked()
	}

	// A retry starts from a clean slate: nothing mounted at the target, the pool not imported.
	if err := w.sys.PrepareRetry(ctx, w.cfg.Pool); err != nil {
		fail("00-preflight", "err.install.prepare", err)
		return
	}
	planEnv, err := w.sys.Resolve(ctx, plan, confirmed)
	if err != nil {
		fail("05-hwplan-verify", "err.install.resolve", err)
		return
	}
	env, cleanup, err := w.installEnv(planEnv, confirmed)
	if err != nil {
		fail("00-preflight", "err.install.env", err)
		return
	}
	defer cleanup()

	w.mu.Lock()
	steps := append([]stepState(nil), w.st.Install.Steps...)
	w.mu.Unlock()
	for i, s := range steps {
		start := w.now()
		w.setStep(i, "running", 0, 0)
		var stdin io.Reader
		if s.ID == "10-disk-zfs" {
			w.mu.Lock()
			stdin = strings.NewReader(w.key + "\n")
			w.mu.Unlock()
		}
		err := w.sys.RunStep(ctx, s.ID, env, stdin, func(line string) {
			w.mu.Lock()
			w.appendLogLocked(line)
			// rsync's overall percentage can step back while it discovers files: never show that.
			if p := progressOf(line); p >= 0 && i < len(w.st.Install.Steps) && p > w.st.Install.Steps[i].Progress {
				w.st.Install.Steps[i].Progress = p
				w.recomputeLocked()
				w.changedLocked()
			}
			w.mu.Unlock()
		})
		if err != nil {
			fail(s.ID, "err.install.step", err)
			return
		}
		w.setStep(i, "done", 100, int(w.now().Sub(start).Seconds()))
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.st.Install.Status, w.st.Install.Percent, w.st.Install.ETA = "done", 100, 0
	w.st.Screen = "done"
	// The key has done its job: the operator saw it, the pool is sealed with it. Forget it.
	w.key, w.pass = "", ""
	w.st.Account.PasswordHash = ""
	w.cfg.Logf("install complete")
	w.changedLocked()
}

func (w *Wizard) setStep(i int, status string, progress, elapsed int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if i >= len(w.st.Install.Steps) {
		return
	}
	s := &w.st.Install.Steps[i]
	s.Status, s.Progress = status, progress
	if elapsed > 0 {
		s.Elapsed = elapsed
	}
	w.recomputeLocked()
	w.changedLocked()
}

// recomputeLocked derives the overall percentage and the time left from the step estimates,
// the steps' own progress (rsync's for the copy) and the time the finished steps took.
func (w *Wizard) recomputeLocked() {
	total, done := 0.0, 0.0
	left := 0.0
	for _, s := range w.st.Install.Steps {
		e := float64(s.Estimate)
		total += e
		switch s.Status {
		case "done", "skipped":
			done += e
		case "running":
			done += e * float64(s.Progress) / 100
			left += e * float64(100-s.Progress) / 100
		default:
			left += e
		}
	}
	if total > 0 {
		w.st.Install.Percent = int(done * 100 / total)
	}
	w.st.Install.ETA = int(left)
}

var pctRe = regexp.MustCompile(`\s(\d{1,3})%\s`)

// progressOf reads a percentage from a step's output (rsync --info=progress2 lines), or -1.
func progressOf(line string) int {
	m := pctRe.FindStringSubmatch(line + " ")
	if m == nil {
		return -1
	}
	var p int
	if _, err := fmt.Sscanf(m[1], "%d", &p); err != nil {
		return -1
	}
	if p > 100 {
		return -1
	}
	return p
}

// appendLogLocked keeps the install log, with every secret this process knows removed.
func (w *Wizard) appendLogLocked(line string) {
	line = w.redactLocked(line)
	w.logs = append(w.logs, line)
	if len(w.logs) > 5000 {
		w.logs = w.logs[len(w.logs)-5000:]
	}
	w.hub.publish("log", line)
}

func (w *Wizard) redactLocked(s string) string {
	for _, secret := range []string{w.key, groupKey(w.key), w.pass, w.st.Account.PasswordHash} {
		if len(secret) >= 8 {
			s = strings.ReplaceAll(s, secret, "[redacted]")
		}
	}
	return s
}

// installEnv is the environment of every step: what runink-autoinstall exports, plus the
// graphical installer's answers. Files it needs (SSH keys, the payload passphrase) go to a
// private directory on /run and are removed by cleanup.
func (w *Wizard) installEnv(planEnv map[string]string, confirmed []string) ([]string, func(), error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	l := layouts[w.st.Keyboard]
	env := []string{
		"PATH=/usr/local/sbin:/usr/local/bin:/usr/bin:/usr/sbin:/bin:/sbin",
		"RUNINK_TARGET=/mnt",
		"RUNINK_PROFILE_ROOT=/run/archiso/bootmnt",
		"RUNINK_POOL=" + w.cfg.Pool,
		"RUNINK_BE=runink",
		"RUNINK_HOSTNAME=" + w.st.Account.Hostname,
		// The key comes from the graphical installer on 10-disk-zfs's stdin (it showed it
		// already), and a pool the operator chose to erase is created fresh, never imported.
		"RUNINK_ZFS_KEY=stdin",
		"RUNINK_POOL_FRESH=1",
		"RUNINK_UNATTENDED=1",
		"RUNINK_ADMIN_PASSWORD_HASH=" + w.st.Account.PasswordHash,
		"RUNINK_ADMIN_USER=" + w.st.Account.Username,
		"RUNINK_LANG=" + languages[w.st.Lang],
		"RUNINK_KEYMAP=" + l.Keymap,
		"RUNINK_XKB_LAYOUT=" + l.XKB,
		"RUNINK_XKB_VARIANT=" + l.Variant,
		"RUNINK_NET_STATE_FILE=/run/river/net-state.json",
		"RUNINK_CONFIRMED_IDS=" + strings.Join(confirmed, "\n") + "\n",
		"RUNINK_GRAPHICAL=1",
	}
	if w.st.Net.State != nil {
		env = append(env, "RUNINK_NET_STATE="+w.st.Net.State.State)
	}
	env = append(env, "RUNINK_PLAN_PROFILE="+w.self.PlanProfile, "RUNINK_EDITION="+w.self.ID,
		"RUNINK_EDITION_TITLE="+w.self.Title, "RUNINK_UI_LANG="+w.st.Lang)
	if w.self.FirstbootUI {
		// The installed machine starts with the graphical first boot (77-firstboot-ui).
		env = append(env, "RUNINK_FIRSTBOOT_UI=1")
	}
	if w.self.ModelsRequired {
		// 72-models-payload fails instead of deferring the models.
		env = append(env, "RUNINK_MODELS_REQUIRED=1")
	}
	if w.st.Role != "" {
		env = append(env, "RUNINK_ROLE="+w.st.Role)
	}
	for k, v := range planEnv {
		if strings.HasPrefix(k, "RUNINK_") {
			env = append(env, k+"="+v)
		}
	}
	dir := filepath.Join(w.cfg.RunDir, "install")
	_ = os.RemoveAll(dir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, func() {}, err
	}
	cleanup := func() { _ = os.RemoveAll(dir) }
	planFile := filepath.Join(dir, "plan.json")
	if err := os.WriteFile(planFile, w.st.Machine.Plan, 0o600); err != nil {
		return nil, cleanup, err
	}
	env = append(env, "RUNINK_PLAN_FILE="+planFile)
	if w.st.Account.SSHKeys != "" {
		f := filepath.Join(dir, "authorized_keys")
		if err := os.WriteFile(f, []byte(w.st.Account.SSHKeys), 0o600); err != nil {
			return nil, cleanup, err
		}
		env = append(env, "RUNINK_ADMIN_AUTHORIZED_KEYS="+f)
	}
	if w.pass != "" {
		f := filepath.Join(dir, "medium-passphrase")
		if err := os.WriteFile(f, []byte(w.pass+"\n"), 0o600); err != nil {
			return nil, cleanup, err
		}
		env = append(env, "RUNINK_MODELS_PASSPHRASE_FILE="+f)
	}
	return env, cleanup, nil
}

func hashOf(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// hasPass reports whether the operator already gave the medium passphrase.
func (w *Wizard) hasPass() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.pass != ""
}
