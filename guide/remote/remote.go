// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

// Package remote is river-guide's "tag" channel: `@river_install <question|command>`
// comments on one GitHub issue per install, so an operator elsewhere can observe the
// install and ask about it.
//
// What the channel may do is deliberately small:
//
//	observe   status, steps, hardware summary (redacted), plan summary (redacted)
//	ask       questions, answered from PUBLIC guide bundles only
//	approve   run a NON-destructive step that the console already proposed, and only
//	          while the console operator has remote approvals switched on
//
// It can never propose, skip or mark steps done, never trigger a destructive step, never
// see command output, and never receive text from a private (platform) bundle. Every
// outgoing comment passes the redactor and a private-text guard last.
package remote

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/org-runink/river/guide/agent"
	"github.com/org-runink/river/guide/audit"
	"github.com/org-runink/river/guide/bundle"
	"github.com/org-runink/river/guide/github"
	"github.com/org-runink/river/guide/redact"
)

// Label marks install-tracking issues.
const Label = "river-install"

// Marker identifies river-guide's own comments so they are never parsed as tags.
const Marker = "<!-- river-guide -->"

// Tag is the comment prefix.
const Tag = "@river_install"

// GitHub is what the session needs from package github (tests use the real client
// against an httptest server).
type GitHub interface {
	GetRepo(ctx context.Context, owner, name string) (*github.Repo, error)
	CreateIssue(ctx context.Context, owner, name, title, body string, labels []string) (*github.Issue, error)
	AddLabels(ctx context.Context, owner, name string, n int, labels []string) error
	Comments(ctx context.Context, owner, name string, n int, since time.Time) ([]github.Comment, error)
	PostComment(ctx context.Context, owner, name string, n int, body string) (*github.Comment, error)
}

// Session is one install's remote channel.
type Session struct {
	GH        GitHub
	Owner     string
	Repo      string
	Issue     int
	Self      string // the signed-in login (its tags are honoured; its comments carry Marker)
	InstallID string

	Agent    *agent.Agent
	Machine  *agent.Machine
	HWSum    func() string // redacted hardware summary, or ""
	PlanSum  func() string
	Redactor *redact.Redactor
	Audit    *audit.Log
	Console  func(format string, args ...any) // mirrors remote activity on the local console
	Interval time.Duration
	Now      func() time.Time

	mu      sync.Mutex
	seen    map[int64]bool
	since   time.Time
	shingle map[string]bool
}

// CheckRepo returns a warning when the repository is public.
func (s *Session) CheckRepo(ctx context.Context) (warning string, err error) {
	r, err := s.GH.GetRepo(ctx, s.Owner, s.Repo)
	if err != nil {
		return "", err
	}
	if !r.Private {
		return fmt.Sprintf("%s/%s is PUBLIC: anyone can read this install's progress. A private per-client repository is recommended.", s.Owner, s.Repo), nil
	}
	return "", nil
}

// Open creates the issue when Issue is 0, labels it, and posts the opening comment.
func (s *Session) Open(ctx context.Context, mode string) error {
	s.init()
	title := "Runink River install " + s.InstallID
	if s.Issue == 0 {
		is, err := s.GH.CreateIssue(ctx, s.Owner, s.Repo, title,
			s.guard(Marker+"\nInstall-tracking issue for one Runink River install. Comment `"+Tag+" help` to see what this channel can do."), []string{Label})
		if err != nil {
			return fmt.Errorf("create issue: %w", err)
		}
		s.Issue = is.Number
	} else if err := s.GH.AddLabels(ctx, s.Owner, s.Repo, s.Issue, []string{Label}); err != nil {
		return fmt.Errorf("label issue: %w", err)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "**Runink River install %s started.** Guide mode: %s. Remote approvals: %s.\n\n", s.InstallID, mode, onOff(s.Machine.RemoteApproval()))
	if s.HWSum != nil {
		if hw := s.HWSum(); hw != "" {
			b.WriteString("Hardware (redacted):\n\n" + hw + "\n")
		}
	}
	b.WriteString(s.progress())
	b.WriteString("\nComment `" + Tag + " help` for commands.")
	// Start polling from now: tags written before this session are history, not requests.
	s.since = s.now().Add(-5 * time.Second)
	return s.post(ctx, b.String())
}

func (s *Session) init() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.seen == nil {
		s.seen = map[int64]bool{}
	}
	if s.Interval == 0 {
		s.Interval = 20 * time.Second
	}
	if s.Now == nil {
		s.Now = time.Now
	}
	if s.Console == nil {
		s.Console = func(string, ...any) {}
	}
	if s.Redactor == nil {
		s.Redactor = redact.New()
	}
}

func (s *Session) now() time.Time { return s.Now() }

// Run polls until ctx ends. Poll errors are reported on the console and retried with
// backoff; they never end the install.
func (s *Session) Run(ctx context.Context) {
	s.init()
	backoff := s.Interval
	for {
		err := s.PollOnce(ctx)
		if err != nil {
			s.Console("remote: poll failed: %v (retrying in %s)", err, backoff)
			backoff = min(backoff*2, 5*time.Minute)
		} else {
			backoff = s.Interval
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
	}
}

// PollOnce fetches new comments and handles every tag in them.
func (s *Session) PollOnce(ctx context.Context) error {
	s.init()
	cs, err := s.GH.Comments(ctx, s.Owner, s.Repo, s.Issue, s.since)
	if err != nil {
		return err
	}
	for _, c := range cs {
		s.mu.Lock()
		dup := s.seen[c.ID]
		s.seen[c.ID] = true
		s.mu.Unlock()
		if dup || strings.Contains(c.Body, Marker) {
			continue
		}
		if c.CreatedAt.After(s.since) {
			s.since = c.CreatedAt
		}
		for _, cmd := range parseTags(c.Body) {
			s.handle(ctx, c, cmd)
		}
	}
	return nil
}

var tagRe = regexp.MustCompile(`(?im)^\s*` + regexp.QuoteMeta(Tag) + `\b[ \t]*(.*)$`)

// parseTags returns the text after each tag line (at most 3 per comment).
func parseTags(body string) []string {
	var out []string
	for _, m := range tagRe.FindAllStringSubmatch(body, 3) {
		out = append(out, strings.TrimSpace(m[1]))
	}
	return out
}

func (s *Session) authorized(c github.Comment) bool {
	if s.Self != "" && strings.EqualFold(c.User.Login, s.Self) {
		return true
	}
	switch c.AuthorAssociation {
	case "OWNER", "MEMBER", "COLLABORATOR":
		return true
	}
	return false
}

func (s *Session) handle(ctx context.Context, c github.Comment, cmd string) {
	actor := agent.Actor{Remote: true, Login: c.User.Login}
	if !s.authorized(c) {
		s.Audit.Record(audit.Event{Channel: "tag", Actor: actor.String(), Action: "ignored", Detail: "author is not a collaborator"})
		s.Console("remote: ignored a tag from @%s (not a collaborator on %s/%s)", c.User.Login, s.Owner, s.Repo)
		return
	}
	verb, arg, _ := strings.Cut(cmd, " ")
	verb = strings.ToLower(strings.TrimSpace(verb))
	arg = strings.TrimSpace(arg)
	var reply string
	action := verb
	switch verb {
	case "", "help":
		reply = helpText
	case "status", "steps":
		reply = s.progress()
	case "hw", "hardware":
		reply = "Hardware (redacted):\n\n" + callOr(s.HWSum, "not probed yet")
	case "plan":
		reply = "Install plan (redacted):\n\n" + callOr(s.PlanSum, "no plan yet")
	case "approve":
		reply = s.approve(ctx, arg, actor)
	case "ask":
		reply = s.ask(ctx, arg, actor)
	default:
		action = "ask" // the verb is the first word of a question: never log it
		reply = s.ask(ctx, cmd, actor)
	}
	s.Audit.Record(audit.Event{Channel: "tag", Actor: actor.String(), Action: "tag:" + firstWord(action)})
	quote := "> " + Tag + " " + oneLine(cmd, 200)
	if err := s.post(ctx, quote+"\n\n"+reply); err != nil {
		s.Console("remote: could not post reply: %v", err)
	}
}

func (s *Session) ask(ctx context.Context, q string, actor agent.Actor) string {
	if q == "" {
		return "Ask a question after the tag, e.g. `" + Tag + " how is the pool encrypted?`"
	}
	s.Console("remote: %s asks: %s", actor, oneLine(q, 200))
	ans := s.Agent.Ask(ctx, q, agent.ScopePublic)
	s.Audit.Record(audit.Event{Channel: "tag", Actor: actor.String(), Action: "ask", Cites: ans.Citations, Detail: string(ans.Mode)})
	out := ans.Text
	if ans.Note != "" {
		out += "\n\n_" + ans.Note + "_"
	}
	if s.Agent.HasPrivate() {
		out += "\n\n_Answers here come from the public Runink River guide only. Platform-specific guidance is shown on the install console._"
	}
	return out
}

func (s *Session) approve(ctx context.Context, id string, actor agent.Actor) string {
	if id == "" {
		cur, ok := s.Machine.Current()
		if !ok {
			return "Nothing is proposed at the console."
		}
		id = cur.ID
	}
	st, err := s.Machine.Approve(ctx, id, actor)
	s.Audit.Record(audit.Event{Channel: "tag", Actor: actor.String(), Action: "approve", Step: id, State: string(st.State), Detail: errString(err)})
	name := publicName(st)
	switch {
	case errors.Is(err, agent.ErrDestructive):
		return "Refused: " + name + " is destructive. Destructive steps are only ever run by the operator at the console."
	case errors.Is(err, agent.ErrRemoteDisabled):
		return "Refused: remote approvals are off. The console operator can enable them with `remote approvals on`."
	case errors.Is(err, agent.ErrNotProposed):
		return "Refused: " + name + " has not been proposed at the console."
	case errors.Is(err, agent.ErrUnknownStep):
		return "Refused: no such step."
	case err != nil:
		return "Refused: " + err.Error()
	}
	s.Console("remote: %s approved %s → %s (exit %d)", actor, st.ID, st.State, st.Exit)
	return fmt.Sprintf("%s: **%s** (exit %d). Output is shown on the console only.", name, st.State, st.Exit)
}

// NotifyStep posts a generic progress line after a console-side transition.
func (s *Session) NotifyStep(ctx context.Context, st agent.Step) {
	if err := s.post(ctx, fmt.Sprintf("%s: **%s**.\n\n%s", publicName(st), st.State, s.progress())); err != nil {
		s.Console("remote: could not post progress: %v", err)
	}
}

// publicName never reveals a private step's id or title.
func publicName(st agent.Step) string {
	if st.StepDef == nil {
		return "step"
	}
	if st.Private {
		return "a platform step"
	}
	return fmt.Sprintf("Runink River step `%s` (%s)", st.ID, st.Title)
}

func (s *Session) progress() string {
	pd, pt, xd, xt := s.Machine.Progress()
	var b strings.Builder
	fmt.Fprintf(&b, "Progress: Runink River steps %d/%d done", pd, pt)
	if xt > 0 {
		fmt.Fprintf(&b, "; platform steps %d/%d done", xd, xt)
	} else {
		b.WriteString("; no platform guide bundle loaded")
	}
	b.WriteString(".")
	if cur, ok := s.Machine.Current(); ok {
		fmt.Fprintf(&b, " Current: %s, %s.", publicName(cur), cur.State)
	}
	b.WriteString("\n")
	return b.String()
}

// SetPrivateGuard registers the text of private bundles so that no outgoing comment can
// carry a run of it, whatever path the text took to get there.
func (s *Session) SetPrivateGuard(bundles ...*bundle.Bundle) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.shingle = map[string]bool{}
	for _, b := range bundles {
		if !b.Private {
			continue
		}
		for _, sec := range b.Sections {
			for _, sh := range shingles(sec.Title + " " + sec.Body) {
				s.shingle[sh] = true
			}
			if sec.Step != nil {
				s.shingle["id:"+sec.Step.ID] = true
			}
		}
	}
}

const shingleN = 7

func shingles(s string) []string {
	w := strings.Fields(strings.ToLower(s))
	var out []string
	for i := 0; i+shingleN <= len(w); i++ {
		out = append(out, strings.Join(w[i:i+shingleN], " "))
	}
	return out
}

func (s *Session) guard(body string) string {
	body = s.Redactor.String(body)
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.shingle) == 0 {
		return body
	}
	for _, sh := range shingles(body) {
		if s.shingle[sh] {
			return Marker + "\n_This reply was withheld: it contained text from a private guide bundle. See the install console._"
		}
	}
	return body
}

func (s *Session) post(ctx context.Context, body string) error {
	if !strings.Contains(body, Marker) {
		body = Marker + "\n" + body
	}
	_, err := s.GH.PostComment(ctx, s.Owner, s.Repo, s.Issue, s.guard(body))
	return err
}

const helpText = "`" + Tag + "` commands:\n\n" +
	"- `status` — install progress\n" +
	"- `hw` — redacted hardware summary\n" +
	"- `plan` — redacted install plan\n" +
	"- `approve [step]` — run the non-destructive step proposed at the console (only when the console enabled remote approvals)\n" +
	"- anything else — a question, answered from the public Runink River install guide with citations\n\n" +
	"Destructive steps (disk partitioning, pool creation) are never run from here."

func callOr(f func() string, def string) string {
	if f == nil {
		return def
	}
	if s := f(); s != "" {
		return s
	}
	return def
}

func onOff(b bool) string {
	if b {
		return "on"
	}
	return "off"
}

func oneLine(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > n {
		s = s[:n] + "…"
	}
	return s
}

func firstWord(s string) string {
	if s == "" {
		return "help"
	}
	return s
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
