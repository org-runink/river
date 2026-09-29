// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/org-runink/river/guide/agent"
	"github.com/org-runink/river/guide/audit"
	"github.com/org-runink/river/guide/bundle"
	"github.com/org-runink/river/guide/bundles"
	"github.com/org-runink/river/guide/github"
	"github.com/org-runink/river/guide/hw"
	"github.com/org-runink/river/guide/model"
	"github.com/org-runink/river/guide/redact"
	"github.com/org-runink/river/guide/remote"
	"github.com/org-runink/river/guide/source"
)

type app struct {
	cfg     *config
	in      *lineReader
	out     io.Writer
	agent   *agent.Agent
	machine *agent.Machine
	audit   *audit.Log
	red     *redact.Redactor
	tools   hw.Tools
	sources []source.Source

	mu        sync.Mutex
	hwres     *hw.Result
	token     string
	login     string
	session   *remote.Session
	stopRem   context.CancelFunc
	installID string
	modelNote string
}

func newApp(ctx context.Context, cfg *config, stdin io.Reader, stdout io.Writer) (*app, error) {
	river, err := bundles.River()
	if err != nil {
		return nil, fmt.Errorf("built-in guide: %w", err)
	}
	a := &app{
		cfg:     cfg,
		in:      &lineReader{sc: bufio.NewScanner(stdin)},
		out:     stdout,
		agent:   agent.New(nil, river),
		machine: agent.NewMachine(agent.ExecRunner(2*time.Minute), river),
		audit:   auditOrStderr(cfg.auditPath, os.Stderr),
		tools:   hw.Tools{HWProbe: cfg.hwprobe, Plan: cfg.plan, Manifest: cfg.planManifest, WorkDir: cfg.workDir},
	}
	a.machine.SetRemoteApproval(cfg.remoteApprovals)
	a.installID = newID()
	host, _ := os.Hostname()
	a.red = redact.New(host)
	if cfg.proxy != "" {
		_ = os.Setenv("HTTPS_PROXY", cfg.proxy)
	}
	if a.sources, err = source.LoadDir(cfg.sourcesDir); err != nil {
		fmt.Fprintf(stdout, "warning: ignoring guide-bundle sources: %v\n", err)
		a.sources = nil
	}
	if cfg.clientID == "" {
		for _, s := range a.sources {
			if s.ClientID != "" {
				cfg.clientID = s.ClientID
				break
			}
		}
	}
	a.setupModel(ctx)
	a.audit.Record(audit.Event{Channel: "console", Actor: "console", Action: "start", Extra: map[string]string{"install": a.installID, "model": a.modelNote}})
	return a, nil
}

func (a *app) close() {
	a.mu.Lock()
	if a.stopRem != nil {
		a.stopRem()
	}
	a.token = "" // the only copy of the credential
	a.mu.Unlock()
	a.audit.Record(audit.Event{Channel: "console", Actor: "console", Action: "exit"})
	_ = a.audit.Close()
}

func newID() string {
	var b [4]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// setupModel picks model or degraded mode. The model service writes model.state:
// "ready", "loading", or "degraded: <reason>". Absent file = try the endpoint anyway.
func (a *app) setupModel(ctx context.Context) {
	if a.cfg.noModel {
		a.modelNote = "degraded (disabled on the command line)"
		return
	}
	if st, err := os.ReadFile(a.cfg.modelState); err == nil {
		s := strings.TrimSpace(string(st))
		if strings.HasPrefix(s, "degraded") {
			a.modelNote = s
			return
		}
	}
	c, err := model.New(a.cfg.modelURL, a.cfg.modelName, false)
	if err != nil {
		a.modelNote = "degraded (" + err.Error() + ")"
		return
	}
	a.modelNote = "loading"
	// One quick synchronous check, so a server that is already up is used from the first
	// question; otherwise keep trying in the background while weights load.
	qctx, qcancel := context.WithTimeout(ctx, 2*time.Second)
	if c.Ready(qctx) == nil {
		qcancel()
		a.agent.SetModel(c)
		a.modelNote = "ready"
		return
	}
	qcancel()
	go func() {
		// The server may still be loading weights; upgrade to model mode when it answers.
		for i := 0; i < 120; i++ {
			rctx, cancel := context.WithTimeout(ctx, 3*time.Second)
			err := c.Ready(rctx)
			cancel()
			if err == nil {
				a.agent.SetModel(c)
				a.mu.Lock()
				a.modelNote = "ready"
				a.mu.Unlock()
				return
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(5 * time.Second):
			}
		}
		a.mu.Lock()
		a.modelNote = "degraded (model server did not answer)"
		a.mu.Unlock()
	}()
}

func (a *app) waitModel(ctx context.Context, d time.Duration) {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) && !a.agent.HasModel() {
		select {
		case <-ctx.Done():
			return
		case <-time.After(200 * time.Millisecond):
		}
	}
}

func (a *app) mode() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.agent.HasModel() {
		return "model"
	}
	return a.modelNote
}

func (a *app) printf(format string, args ...any) { fmt.Fprintf(a.out, format, args...) }

func (a *app) ask(prompt string) (string, error) {
	a.printf("%s", prompt)
	return a.in.read()
}

func (a *app) banner() {
	a.printf("Runink River install guide %s (guide %s) — install %s\n", version, bundles.Version, a.installID)
	if m := a.mode(); m == "model" {
		a.printf("guide model: ready (a local model phrases answers from the guide; nothing leaves this machine)\n")
	} else {
		a.printf("guide model: %s\n", m)
	}
	if a.agent.HasPrivate() {
		for _, b := range a.agent.Bundles() {
			if b.Private {
				a.printf("platform guide bundle: %s %s\n", b.Name, b.Version)
			}
		}
	} else if len(a.sources) > 0 {
		a.printf("no platform guide bundle loaded (%d source(s) configured: type `login` to fetch)\n", len(a.sources))
	} else {
		a.printf("no platform guide bundle loaded: continuing with Runink River steps\n")
	}
	a.printf("type `help` for commands, `next` to begin, or ask a question.\n")
}

func (a *app) repl(ctx context.Context) int {
	a.banner()
	if a.cfg.remoteOffer {
		ref := a.cfg.tagIssue
		if ref == "" {
			ref = "(issue not set)"
		}
		if ans, err := a.ask(fmt.Sprintf("The boot menu requested the remote issue channel for %s. Enable it now? [y/N] ", ref)); err == nil && yes(ans) {
			a.remoteOn(ctx, a.cfg.tagIssue)
		}
	}
	for {
		line, err := a.ask("river> ")
		if err != nil {
			if !isEOF(err) {
				a.printf("\ninput error: %v\n", err)
			}
			a.printf("\n")
			return 0
		}
		if line == "" {
			continue
		}
		if a.dispatch(ctx, line) {
			return 0
		}
		if ctx.Err() != nil {
			return 0
		}
	}
}

// dispatch handles one console line; it returns true to quit.
func (a *app) dispatch(ctx context.Context, line string) bool {
	verb, arg, _ := strings.Cut(line, " ")
	arg = strings.TrimSpace(arg)
	switch strings.ToLower(verb) {
	case "quit", "exit":
		return true
	case "help", "?":
		a.printf("%s", consoleHelp)
	case "steps":
		for _, s := range a.machine.Steps() {
			a.printf("  %s\n", stepLine(s))
		}
		a.progressLine()
	case "next":
		st, err := a.machine.Next(agent.Local)
		if err != nil {
			a.printf("%v\n", err)
			return false
		}
		a.showStep(st)
		a.logStep("propose", st, nil)
	case "approve":
		a.approve(ctx, arg)
	case "done", "skip", "retry":
		a.settle(ctx, strings.ToLower(verb), arg)
	case "show":
		a.show(arg)
	case "hw", "hardware":
		if r := a.probe(ctx); r != nil {
			a.printf("%s", r.Summary())
		}
	case "plan":
		if r := a.probe(ctx); r != nil {
			a.printf("%s", r.PlanSummary())
		}
	case "login":
		a.doLogin(ctx)
	case "remote":
		a.remoteCmd(ctx, arg)
	case "bundles":
		for _, b := range a.agent.Bundles() {
			kind := "public, built in"
			if b.Private {
				kind = "private, in memory only"
			}
			a.printf("  %s %s (%s): %d sections, %d steps\n", b.Name, b.Version, kind, len(b.Sections), len(b.Steps))
		}
	case "shell":
		a.shell()
	case "ask":
		a.answer(ctx, arg)
	default:
		a.answer(ctx, line)
	}
	return false
}

const consoleHelp = `commands:
  next                 propose the next install step
  approve [step]       run the proposed step (read-only checks only)
  done <step>          record that you completed a step yourself
  skip <step>          skip a step          retry <step>   re-propose a failed step
  steps                list steps and their state
  show <step|anchor>   print a guide section
  hw / plan            hardware summary / install plan
  login                sign in to GitHub (device flow) and fetch a platform guide bundle
  remote on [issue]    mirror this install to a GitHub issue (@river_install tags)
  remote off           stop the issue channel
  remote approvals on|off   let collaborators approve proposed read-only steps
  bundles              loaded guide bundles
  shell                a login shell on this console (exit it to come back)
  quit                 leave the guide
anything else is a question about the install.
`

func (a *app) answer(ctx context.Context, q string) {
	if strings.TrimSpace(q) == "" {
		return
	}
	ans := a.agent.Ask(ctx, q, agent.ScopeLocal)
	a.printf("\n%s\n", ans.Text)
	if ans.Note != "" {
		a.printf("(%s)\n", ans.Note)
	}
	a.printf("\n")
	a.audit.Record(audit.Event{Channel: "console", Actor: "console", Action: "ask", Cites: ans.Citations, Detail: string(ans.Mode)})
}

func (a *app) showStep(st agent.Step) {
	a.printf("\nStep %s: %s  [%s]\n", st.ID, st.Title, st.Kind)
	if sec := a.section(st.Ref); sec != nil {
		a.printf("%s\n", sec.Body)
	}
	switch st.Kind {
	case bundle.KindCheck, bundle.KindAction:
		a.printf("\n→ `approve` runs: %s\n", st.Run)
	case bundle.KindDestructive:
		a.printf("\n→ river-guide will NOT run this. On another console (Alt+F2) run:\n    %s\n  then come back and type `done %s`.\n", st.Run, st.ID)
	default:
		a.printf("\n→ type `done %s` when finished.\n", st.ID)
	}
}

func (a *app) section(ref string) *bundle.Section {
	name, anchor, _ := strings.Cut(ref, "#")
	for _, b := range a.agent.Bundles() {
		if b.Name == name {
			return b.Section(anchor)
		}
	}
	return nil
}

func (a *app) show(arg string) {
	for _, s := range a.machine.Steps() {
		if s.ID == arg {
			a.showStep(s)
			return
		}
	}
	ref := arg
	if !strings.Contains(ref, "#") {
		ref = "river#" + ref
	}
	if sec := a.section(ref); sec != nil {
		a.printf("\n%s [%s]\n%s\n\n", sec.Title, sec.Ref(), sec.Body)
		return
	}
	a.printf("no step or section %q\n", arg)
}

func (a *app) approve(ctx context.Context, id string) {
	if id == "" {
		cur, ok := a.machine.Current()
		if !ok {
			a.printf("nothing is proposed: type `next`\n")
			return
		}
		id = cur.ID
	}
	st, err := a.machine.Approve(ctx, id, agent.Local)
	a.logStep("approve", st, err)
	if err != nil {
		if errors.Is(err, agent.ErrDestructive) {
			a.printf("refused: %v. Run it yourself on Alt+F2:\n    %s\nthen `done %s`.\n", err, st.Run, st.ID)
		} else {
			a.printf("refused: %v\n", err)
		}
		return
	}
	a.printf("%s\n", strings.TrimRight(st.Output, "\n"))
	a.printf("step %s: %s (exit %d)\n", st.ID, st.State, st.Exit)
	a.notify(ctx, st)
}

func (a *app) settle(ctx context.Context, verb, id string) {
	var st agent.Step
	var err error
	switch verb {
	case "done":
		st, err = a.machine.MarkDone(id, agent.Local)
	case "skip":
		st, err = a.machine.Skip(id, agent.Local)
	case "retry":
		st, err = a.machine.Retry(id, agent.Local)
	}
	a.logStep(verb, st, err)
	if err != nil {
		a.printf("%v\n", err)
		return
	}
	a.printf("step %s: %s\n", st.ID, st.State)
	a.notify(ctx, st)
	if verb != "retry" {
		a.progressLine()
	}
}

func (a *app) progressLine() {
	pd, pt, xd, xt := a.machine.Progress()
	a.printf("Runink River steps %d/%d", pd, pt)
	if xt > 0 {
		a.printf(", platform steps %d/%d", xd, xt)
	}
	a.printf(".\n")
}

func (a *app) logStep(action string, st agent.Step, err error) {
	e := audit.Event{Channel: "console", Actor: "console", Action: action, State: string(st.State)}
	if st.StepDef != nil {
		e.Step = st.ID
	}
	if err != nil {
		e.Detail = err.Error()
	}
	a.audit.Record(e)
}

func (a *app) notify(ctx context.Context, st agent.Step) {
	a.mu.Lock()
	s := a.session
	a.mu.Unlock()
	if s != nil {
		s.NotifyStep(ctx, st)
	}
}

func (a *app) probe(ctx context.Context) *hw.Result {
	a.mu.Lock()
	r := a.hwres
	a.mu.Unlock()
	if r != nil {
		return r
	}
	r, err := a.tools.Run(ctx)
	if err != nil {
		a.printf("hardware probe unavailable: %v\n", err)
		return nil
	}
	a.mu.Lock()
	a.hwres = r
	a.mu.Unlock()
	return r
}

func (a *app) shell() {
	sh := os.Getenv("SHELL")
	if sh == "" {
		sh = "/bin/sh"
	}
	a.printf("starting %s; type `exit` to return to the guide\n", sh)
	cmd := exec.Command(sh, "-l") // #nosec G204 G702 -- the console operator asked for their own login shell; $SHELL is theirs, not remote input
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	_ = cmd.Run()
}

func yes(s string) bool {
	s = strings.ToLower(strings.TrimSpace(s))
	return s == "y" || s == "yes"
}

// ---- GitHub: login, platform bundles, remote channel ----

func (a *app) gh(token string) *github.Client {
	c := github.New(token)
	c.API, c.Web = strings.TrimSuffix(a.cfg.apiURL, "/"), strings.TrimSuffix(a.cfg.webURL, "/")
	if a.cfg.devicePoll > 0 {
		c.SetPollInterval(a.cfg.devicePoll)
	}
	return c
}

// ensureToken runs the device flow once per session. The token lives in this process
// only; nothing writes it to disk.
func (a *app) ensureToken(ctx context.Context) (string, error) {
	a.mu.Lock()
	tok := a.token
	a.mu.Unlock()
	if tok != "" {
		return tok, nil
	}
	c := a.gh("")
	if err := c.Preflight(ctx); err != nil {
		return "", err
	}
	lctx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()
	tok, err := c.DeviceLogin(lctx, a.cfg.clientID, func(code, uri string) {
		a.printf("\nOn any device, open %s and enter the code:\n\n    %s\n\nwaiting for authorization…\n", uri, code)
	})
	if err != nil {
		return "", err
	}
	me, err := a.gh(tok).Me(ctx)
	if err != nil {
		return "", fmt.Errorf("signed in, but GET /user failed: %w", err)
	}
	a.mu.Lock()
	a.token, a.login = tok, me.Login
	a.mu.Unlock()
	a.red.Add(tok)
	a.printf("signed in to GitHub as %s (token held in memory only)\n", me.Login)
	a.audit.Record(audit.Event{Channel: "console", Actor: "console", Action: "login", Detail: me.Login})
	return tok, nil
}

func (a *app) doLogin(ctx context.Context) {
	tok, err := a.ensureToken(ctx)
	if err != nil {
		a.printf("sign-in failed: %v\n", err)
		return
	}
	a.fetchPlatform(ctx, tok)
}

func (a *app) fetchPlatform(ctx context.Context, tok string) {
	if len(a.sources) == 0 {
		a.printf("no platform guide bundle loaded: this medium names no bundle source\n")
		return
	}
	c := a.gh(tok)
	for _, s := range a.sources {
		already := false
		for _, b := range a.agent.Bundles() {
			if b.Private && b.Name == s.Name {
				already = true
			}
		}
		if already {
			continue
		}
		if s.PubKey == "" {
			s.PubKey = a.cfg.bundleKey
		}
		if s.PubKey == "" {
			k, err := a.ask(fmt.Sprintf("Verification key for platform guide bundle %q (ed25519:…, blank to skip): ", s.Name))
			if err != nil || strings.TrimSpace(k) == "" {
				a.printf("no platform guide bundle loaded (%s): no verification key supplied\n", s.Name)
				continue
			}
			s.PubKey = strings.TrimSpace(k)
		}
		b, err := source.Fetch(ctx, c, s)
		if err != nil {
			a.printf("no platform guide bundle loaded (%s): %v\n", s.Name, err)
			a.audit.Record(audit.Event{Channel: "console", Actor: "console", Action: "bundle-unavailable", Detail: s.Name})
			continue
		}
		if b.Name == "river" {
			a.printf("platform guide bundle REFUSED: it claims the built-in name %q\n", b.Name)
			continue
		}
		a.agent.AddBundle(b)
		a.machine.Append(b)
		a.mu.Lock()
		if a.session != nil {
			a.session.SetPrivateGuard(a.agent.Bundles()...)
		}
		a.mu.Unlock()
		a.printf("platform guide bundle %s %s loaded: %d steps follow the Runink River steps (held in memory only)\n", b.Name, b.Version, len(b.Steps))
		a.audit.Record(audit.Event{Channel: "console", Actor: "console", Action: "bundle-loaded", Detail: b.Name + " " + b.Version})
	}
}

func (a *app) remoteCmd(ctx context.Context, arg string) {
	sub, rest, _ := strings.Cut(arg, " ")
	switch sub {
	case "on":
		a.remoteOn(ctx, strings.TrimSpace(rest))
	case "off":
		a.mu.Lock()
		if a.stopRem != nil {
			a.stopRem()
			a.stopRem, a.session = nil, nil
		}
		a.mu.Unlock()
		a.printf("remote channel off\n")
		a.audit.Record(audit.Event{Channel: "console", Actor: "console", Action: "remote-off"})
	case "approvals":
		on := strings.TrimSpace(rest) == "on"
		a.machine.SetRemoteApproval(on)
		a.printf("remote approvals %s\n", map[bool]string{true: "ON: collaborators may approve read-only steps proposed here", false: "off"}[on])
		a.audit.Record(audit.Event{Channel: "console", Actor: "console", Action: "remote-approvals", Detail: fmt.Sprint(on)})
	default:
		a.mu.Lock()
		s := a.session
		a.mu.Unlock()
		if s == nil {
			a.printf("remote channel off (`remote on [owner/repo#N]`)\n")
		} else {
			a.printf("remote channel on: %s/%s#%d as %s, approvals %v\n", s.Owner, s.Repo, s.Issue, s.Self, a.machine.RemoteApproval())
		}
	}
}

func (a *app) remoteOn(ctx context.Context, ref string) {
	a.mu.Lock()
	running := a.session != nil
	a.mu.Unlock()
	if running {
		a.printf("remote channel already on\n")
		return
	}
	if ref == "" {
		ref = a.cfg.tagIssue
	}
	if ref == "" {
		var err error
		if ref, err = a.ask("Issue for this install (owner/repo#N, or owner/repo to open one): "); err != nil || ref == "" {
			return
		}
	}
	owner, repo, n, err := github.ParseIssueRef(ref)
	if err != nil {
		a.printf("%v\n", err)
		return
	}
	tok, err := a.ensureToken(ctx)
	if err != nil {
		a.printf("remote channel unavailable: %v\n", err)
		return
	}
	a.fetchPlatform(ctx, tok)
	s := &remote.Session{
		GH: a.gh(tok), Owner: owner, Repo: repo, Issue: n, Self: a.login, InstallID: a.installID,
		Agent: a.agent, Machine: a.machine, Redactor: a.red, Audit: a.audit, Interval: a.cfg.pollInterval,
		Console: func(f string, args ...any) { a.printf("\n"+f+"\n", args...) },
		HWSum: func() string {
			if r := a.probe(ctx); r != nil {
				return r.Summary()
			}
			return ""
		},
		PlanSum: func() string {
			if r := a.probe(ctx); r != nil {
				return r.PlanSummary()
			}
			return ""
		},
	}
	s.SetPrivateGuard(a.agent.Bundles()...)
	warn, err := s.CheckRepo(ctx)
	if err != nil {
		a.printf("cannot read %s/%s: %v\n", owner, repo, err)
		return
	}
	if warn != "" {
		a.printf("WARNING: %s\n", warn)
		if ans, err := a.ask("Use it anyway? [y/N] "); err != nil || !yes(ans) {
			a.printf("remote channel not started\n")
			return
		}
	}
	if err := s.Open(ctx, a.mode()); err != nil {
		a.printf("remote channel not started: %v\n", err)
		return
	}
	rctx, cancel := context.WithCancel(ctx)
	a.mu.Lock()
	a.session, a.stopRem = s, cancel
	a.mu.Unlock()
	go s.Run(rctx)
	a.printf("remote channel on: %s/%s#%d (comments `%s …`). Remote approvals are %s.\n", owner, repo, s.Issue, remote.Tag,
		map[bool]string{true: "ON", false: "off (`remote approvals on` to allow)"}[a.machine.RemoteApproval()])
	a.audit.Record(audit.Event{Channel: "console", Actor: "console", Action: "remote-on", Detail: fmt.Sprintf("%s/%s#%d", owner, repo, s.Issue)})
}
