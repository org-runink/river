// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

package wizard

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// The graphical first boot of an installed machine (river-installer firstboot): it shows the
// node settling (network, clock, firewall, k0s), hands over to the downstream's setup pages
// (docs/PAYLOADS.md, "First-boot setup pages"), and ends at "ready" with the addresses and
// the SSH host key fingerprints. It changes nothing a normal boot does not: the checks read
// state, the clock step sets the time, and Finish removes the first-boot kiosk.

// FirstbootSystem is what the first boot reads and does; the real one is in sys_linux.go.
type FirstbootSystem interface {
	Net(ctx context.Context) (NetState, error)
	SyncClock(ctx context.Context) (string, error) // the source it synced from
	Firewall(ctx context.Context) (bool, error)    // the default-deny table is loaded (loads it if not)
	HasK0s() bool
	K0sReady(ctx context.Context) (bool, string, error)
	Hooks() []string                  // executables in firstboot.d, sorted
	HookStatus(name string) string    // "<outcome> <rc>" from /run/runink/firstboot-status, or ""
	HookDone(name string) bool        // /var/lib/runink/firstboot.d/<name>.done
	RetryHooks() error                // run river-firstboot-hooks again (in the background)
	PagesDir() string                 // /run/runink/firstboot-pages
	Ready(ctx context.Context) Ready  // what "ready" shows
	Finish(ctx context.Context) error // remove the first-boot marker and the kiosk
}

// Ready is the last screen's content.
type Ready struct {
	Hostname     string   `json:"hostname"`
	Title        string   `json:"title"`
	Addresses    []string `json:"addresses"`
	AdminUser    string   `json:"admin_user"`
	Fingerprints []string `json:"fingerprints"`
}

// Check is one line of the settling checklist.
type Check struct {
	ID     string `json:"id"`
	Status string `json:"status"` // pending | running | ok | warn | fail | skip
	Detail string `json:"detail,omitempty"`
}

// HookView is one first-boot hook, as the UI shows it.
type HookView struct {
	Name    string `json:"name"`
	Outcome string `json:"outcome"` // pending | running | done | registered | deferred | failed
	RC      int    `json:"rc"`
}

// PageView is one registered setup page, without its URL.
type PageView struct {
	Name    string `json:"name"`
	Title   string `json:"title"`
	Order   int    `json:"order"`
	Skipped bool   `json:"skipped"`
	Error   string `json:"error,omitempty"` // a refused registration: the rule it broke
}

// FirstbootView is the first boot's UI state. It carries no page URL.
type FirstbootView struct {
	Mode    string     `json:"mode"`
	Title   string     `json:"title"` // the edition, e.g. "... Server"
	Lang    string     `json:"lang"`
	Screen  string     `json:"screen"` // settle | page | ready | finished
	Checks  []Check    `json:"checks"`
	Hooks   []HookView `json:"hooks"`
	Pages   []PageView `json:"pages"`
	Current string     `json:"current,omitempty"` // the page on screen
	Ready   *Ready     `json:"ready,omitempty"`
	Elapsed int        `json:"elapsed"`
}

// Firstboot is the first-boot flow.
type Firstboot struct {
	sys   FirstbootSystem
	title string
	logf  func(string, ...any)
	hub   *hub

	mu       sync.Mutex
	lang     string
	checks   []Check
	skipped  map[string]bool
	finished bool
	done     chan struct{} // closed once Finish has cleaned up; the process may exit then
	started  time.Time
	ready    *Ready
}

// NewFirstboot builds the flow; title is the edition's title ("... is ready").
func NewFirstboot(sys FirstbootSystem, title, lang string, logf func(string, ...any)) *Firstboot {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	if _, ok := languages[lang]; !ok {
		lang = "en"
	}
	f := &Firstboot{sys: sys, title: title, logf: logf, hub: newHub(), lang: lang, skipped: map[string]bool{},
		started: time.Now(), done: make(chan struct{})}
	f.checks = []Check{{ID: "network", Status: "pending"}, {ID: "clock", Status: "pending"},
		{ID: "firewall", Status: "pending"}}
	if sys.HasK0s() {
		f.checks = append(f.checks, Check{ID: "k0s", Status: "pending"})
	}
	return f
}

func (f *Firstboot) set(id, status, detail string) {
	f.mu.Lock()
	for i := range f.checks {
		if f.checks[i].ID == id {
			f.checks[i].Status, f.checks[i].Detail = status, detail
		}
	}
	f.mu.Unlock()
	f.publish()
}

func (f *Firstboot) publish() { f.hub.publish("state", f.View()) }

// Run performs the checks, in order, then keeps the view fresh until Finish.
func (f *Firstboot) Run(ctx context.Context) {
	f.set("network", "running", "")
	ns, err := f.sys.Net(ctx)
	switch {
	case err != nil:
		f.set("network", "warn", "err.net.failed")
	case ns.State == "offline":
		f.set("network", "warn", "offline")
	default:
		f.set("network", "ok", netSummary(ns))
	}

	f.set("clock", "running", "")
	if ns.State == "online" || ns.State == "lan-only" {
		src, err := f.sys.SyncClock(ctx)
		if err != nil {
			f.set("clock", "warn", "unsynced")
		} else {
			f.set("clock", "ok", src)
		}
	} else {
		f.set("clock", "warn", "unsynced")
	}

	f.set("firewall", "running", "")
	if ok, err := f.sys.Firewall(ctx); err != nil || !ok {
		f.set("firewall", "fail", "not-loaded")
	} else {
		f.set("firewall", "ok", "default-deny")
	}

	if f.sys.HasK0s() {
		f.set("k0s", "running", "")
		deadline := time.Now().Add(30 * time.Minute)
		for {
			ready, detail, err := f.sys.K0sReady(ctx)
			if err == nil && ready {
				f.set("k0s", "ok", detail)
				break
			}
			if time.Now().After(deadline) {
				f.set("k0s", "fail", "not-ready")
				break
			}
			f.set("k0s", "running", detail)
			select {
			case <-ctx.Done():
				return
			case <-time.After(5 * time.Second):
			}
		}
	}
	r := f.sys.Ready(ctx)
	if r.Title == "" {
		r.Title = f.title
	}
	f.mu.Lock()
	f.ready = &r
	f.mu.Unlock()
	// Hooks and pages change on their own (river-firstboot-hooks); keep the UI current.
	t := time.NewTicker(2 * time.Second)
	defer t.Stop()
	for {
		f.publish()
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		f.mu.Lock()
		done := f.finished
		f.mu.Unlock()
		if done {
			return
		}
	}
}

func netSummary(ns NetState) string {
	kind := "wired"
	if ns.Wifi {
		kind = "wifi"
	}
	fam := []string{}
	if ns.IPv4 {
		fam = append(fam, "IPv4")
	}
	if ns.IPv6 {
		fam = append(fam, "IPv6")
	}
	return kind + ", " + strings.Join(fam, "+")
}

// pageFile is setup.json, contract version 1.
type pageFile struct {
	Version int    `json:"version"`
	Title   string `json:"title"`
	URL     string `json:"url"`
	Order   *int   `json:"order"`
}

type page struct {
	PageView
	url string
}

// readPages reads and checks every registration. A refused one is listed with the rule it
// broke, never with its content.
func (f *Firstboot) readPages() []page {
	dir := f.sys.PagesDir()
	var out []page
	for _, h := range f.sys.Hooks() {
		d := filepath.Join(dir, h)
		p, err := readPage(d)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		pv := page{PageView: PageView{Name: h, Skipped: f.skipped[h]}}
		if err != nil {
			pv.Error = err.Error()
			pv.Title = h
		} else {
			pv.Title, pv.Order, pv.url = p.Title, *p.Order, p.URL
		}
		out = append(out, pv)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Order != out[j].Order {
			return out[i].Order < out[j].Order
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// readPage applies docs/PAYLOADS.md's rules to <dir>/setup.json.
func readPage(dir string) (*pageFile, error) {
	fi, err := os.Lstat(filepath.Join(dir, "setup.json"))
	if err != nil {
		return nil, err
	}
	di, err := os.Lstat(dir)
	if err != nil {
		return nil, err
	}
	if !di.IsDir() || di.Mode().Perm()&0o077 != 0 {
		return nil, errors.New("directory mode")
	}
	if !fi.Mode().IsRegular() || fi.Mode().Perm() != 0o600 {
		return nil, errors.New("file mode")
	}
	fst, ok1 := fi.Sys().(*syscall.Stat_t)
	dst, ok2 := di.Sys().(*syscall.Stat_t)
	if ok1 && ok2 && fst.Uid != 0 && fst.Uid != dst.Uid {
		return nil, errors.New("file owner")
	}
	b, err := os.ReadFile(filepath.Join(dir, "setup.json")) // #nosec G304 -- checked above
	if err != nil {
		return nil, err
	}
	var p pageFile
	if err := json.Unmarshal(b, &p); err != nil {
		return nil, errors.New("not JSON")
	}
	switch {
	case p.Version != 1:
		return nil, fmt.Errorf("version %d", p.Version)
	case p.Title == "" || len([]rune(p.Title)) > 60 || strings.ContainsAny(p.Title, "<>\n"):
		return nil, errors.New("title")
	case p.Order == nil || *p.Order < 0 || *p.Order > 999:
		return nil, errors.New("order")
	}
	if err := checkPageURL(p.URL); err != nil {
		return nil, err
	}
	return &p, nil
}

func checkPageURL(s string) error {
	if len(s) > 2048 {
		return errors.New("url length")
	}
	u, err := url.Parse(s)
	if err != nil || u.Scheme != "http" || u.User != nil || u.Fragment != "" || strings.Contains(s, "#") {
		return errors.New("url form")
	}
	host := u.Hostname()
	if host != "::1" && host != "127.0.0.1" {
		return errors.New("url host")
	}
	if p := u.Port(); p != "" {
		if n, err := strconv.Atoi(p); err != nil || n < 1 || n > 65535 {
			return errors.New("url port")
		}
	}
	return nil
}

// View is the first boot's state; the current page is the first registered, unskipped page
// that is still waiting.
func (f *Firstboot) View() FirstbootView {
	pages := f.readPages()
	f.mu.Lock()
	defer f.mu.Unlock()
	v := FirstbootView{Mode: "firstboot", Title: f.title, Lang: f.lang, Checks: append([]Check(nil), f.checks...),
		Elapsed: int(time.Since(f.started).Seconds())}
	for _, h := range f.sys.Hooks() {
		hv := HookView{Name: h, Outcome: "pending"}
		if f.sys.HookDone(h) {
			hv.Outcome = "done"
		} else if st := f.sys.HookStatus(h); st != "" {
			fs := strings.Fields(st)
			hv.Outcome = fs[0]
			if len(fs) > 1 {
				hv.RC, _ = strconv.Atoi(fs[1])
			}
		}
		v.Hooks = append(v.Hooks, hv)
	}
	for _, p := range pages {
		v.Pages = append(v.Pages, p.PageView)
		if v.Current == "" && p.Error == "" && !p.Skipped {
			v.Current = p.Name
		}
	}
	settled := true
	for _, c := range f.checks {
		if c.Status == "pending" || c.Status == "running" {
			settled = false
		}
	}
	hooksBusy := false
	for _, h := range v.Hooks {
		if h.Outcome == "pending" || h.Outcome == "running" {
			hooksBusy = true
		}
	}
	switch {
	case f.finished:
		v.Screen = "finished"
	case !settled || (hooksBusy && v.Current == ""):
		v.Screen = "settle"
	case v.Current != "":
		v.Screen = "page"
	default:
		v.Screen = "ready"
		v.Ready = f.ready
	}
	return v
}

// PageURL returns a page's URL, with its token, for the kiosk only (the caller checks the peer).
func (f *Firstboot) PageURL(name string) (string, error) {
	for _, p := range f.readPages() {
		if p.Name == name && p.Error == "" {
			return p.url, nil
		}
	}
	return "", ErrState
}

// SkipPage leaves a page for later: it is not done, and its hook runs again at the next boot.
func (f *Firstboot) SkipPage(name string) error {
	f.mu.Lock()
	f.skipped[name] = true
	f.mu.Unlock()
	f.logf("page %s skipped for now", name)
	f.publish()
	return nil
}

// Retry runs the pending hooks again (after a failure).
func (f *Firstboot) Retry() error {
	if err := f.sys.RetryHooks(); err != nil {
		return APIError{"err.firstboot.retry"}
	}
	f.publish()
	return nil
}

// SetLang switches the first boot's language.
func (f *Firstboot) SetLang(lang string) error {
	if _, ok := languages[lang]; !ok {
		return APIError{"err.lang"}
	}
	f.mu.Lock()
	f.lang = lang
	f.mu.Unlock()
	f.publish()
	return nil
}

// Finish ends the graphical first boot from the ready screen.
func (f *Firstboot) Finish(ctx context.Context) error {
	if v := f.View(); v.Screen != "ready" {
		return ErrState
	}
	f.mu.Lock()
	f.finished = true
	f.mu.Unlock()
	f.publish()
	// The clean-up outlives the request that asked for it.
	bg := context.WithoutCancel(ctx)
	go func() {
		time.Sleep(2 * time.Second) // the UI shows "finished" first
		if err := f.sys.Finish(bg); err != nil {
			f.logf("finish: %v", err)
		} else {
			f.logf("first boot finished: the kiosk is removed")
		}
		close(f.done)
	}()
	return nil
}

// Done is closed once Finish has removed the kiosk (river-installer firstboot exits then).
func (f *Firstboot) Done() <-chan struct{} { return f.done }
