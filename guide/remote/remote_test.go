// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

package remote

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/org-runink/river/guide/agent"
	"github.com/org-runink/river/guide/audit"
	"github.com/org-runink/river/guide/bundle"
	"github.com/org-runink/river/guide/bundles"
	"github.com/org-runink/river/guide/github"
	"github.com/org-runink/river/guide/internal/fakegh"
	"github.com/org-runink/river/guide/redact"
)

type runs struct{ argv [][]string }

func (r *runs) run(_ context.Context, argv []string) (string, int, error) {
	r.argv = append(r.argv, argv)
	return "efi output that must stay local: 48:21:0b:5a:9c:01\n", 0, nil
}

func setup(t *testing.T) (*fakegh.Server, *Session, *agent.Machine, *runs, *bytes.Buffer) {
	t.Helper()
	gh := fakegh.New("example-org/installs")
	t.Cleanup(gh.Close)
	c := github.New(gh.Token)
	c.API, c.Web = gh.URL, gh.URL
	river, err := bundles.River()
	if err != nil {
		t.Fatal(err)
	}
	priv, err := bundle.Parse("platform", "3", []bundle.Doc{{Path: "p.md", Content: "## Bootstrap the zanzibar control plane\n<!-- river-guide:step id=zanzibar-bootstrap kind=action run=\"zanzibarctl bootstrap --all\" -->\nThe zanzibar control plane bootstraps from the quokka seed on the first node only.\n"}})
	if err != nil {
		t.Fatal(err)
	}
	priv.Private = true
	r := &runs{}
	m := agent.NewMachine(r.run, river, priv)
	ag := agent.New(nil, river)
	ag.AddBundle(priv)
	var log bytes.Buffer
	s := &Session{
		GH: c, Owner: "example-org", Repo: "installs", Self: gh.Login, InstallID: "a1b2c3d4",
		Agent: ag, Machine: m, Redactor: redact.New("node-7.example.internal"), Audit: audit.To(&log),
		HWSum: func() string {
			return "- Disk nvme0n1: 1863 GiB nvme (eligible)\n- serial S7KHNJ0W123456X on node-7.example.internal\n"
		},
		PlanSum: func() string { return "- Verdict: **ok**\n" },
		Now:     time.Now,
	}
	s.SetPrivateGuard(priv)
	return gh, s, m, r, &log
}

// TestIssueCommentExchange drives the whole tag channel against a fake GitHub and logs the
// resulting conversation (it is the sample in docs/INSTALL-GUIDE-AGENT.md).
func TestIssueCommentExchange(t *testing.T) {
	gh, s, m, r, log := setup(t)
	ctx := context.Background()
	if warn, err := s.CheckRepo(ctx); err != nil || warn != "" {
		t.Fatalf("private repo: warn=%q err=%v", warn, err)
	}
	if err := s.Open(ctx, "model"); err != nil {
		t.Fatal(err)
	}
	if s.Issue != 1 || strings.Join(gh.Labels[1], ",") != Label {
		t.Fatalf("issue %d labels %v", s.Issue, gh.Labels[1])
	}

	// The console walks to the firmware check and proposes it (the network step first; this
	// test is about the tag channel, so it is skipped rather than run).
	m.Next(agent.Local)
	m.Skip("network", agent.Local)
	m.MarkDone("live-session", agent.Local)
	if st, _ := m.Next(agent.Local); st.ID != "live-session" {
		m.Next(agent.Local)
	}
	for {
		st, _ := m.Current()
		if st.StepDef != nil && st.ID == "firmware" {
			break
		}
		m.Next(agent.Local)
	}

	gh.AddComment(1, "alice", "COLLABORATOR", "@river_install status")
	gh.AddComment(1, "alice", "COLLABORATOR", "@river_install hw")
	gh.AddComment(1, "alice", "COLLABORATOR", "@river_install approve firmware")
	gh.AddComment(1, "mallory", "NONE", "@river_install approve firmware")
	gh.AddComment(1, "alice", "COLLABORATOR", "@river_install how is the pool encrypted and where is the recovery key kept?")
	gh.AddComment(1, "alice", "COLLABORATOR", "@river_install zanzibar quokka bootstrap")
	if err := s.PollOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if len(r.argv) != 0 {
		t.Fatal("remote approval ran while approvals were off")
	}

	m.SetRemoteApproval(true)
	gh.AddComment(1, "alice", "COLLABORATOR", "@river_install approve")
	if err := s.PollOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if len(r.argv) != 1 || strings.Join(r.argv[0], " ") != "ls /sys/firmware/efi" {
		t.Fatalf("runs = %v", r.argv)
	}

	// The console reaches the destructive step; nobody remote can run it.
	for {
		st, err := m.Next(agent.Local)
		if err != nil {
			t.Fatal(err)
		}
		if st.ID == "install" {
			break
		}
		if st.Kind.Runnable() {
			m.Approve(ctx, st.ID, agent.Local)
		} else {
			m.MarkDone(st.ID, agent.Local)
		}
	}
	gh.AddComment(1, "alice", "OWNER", "@river_install approve install")
	if err := s.PollOnce(ctx); err != nil {
		t.Fatal(err)
	}
	for _, a := range r.argv {
		if strings.Contains(strings.Join(a, " "), "runink-install") {
			t.Fatal("a destructive step was executed from a tag")
		}
	}
	// A second poll must not answer anything twice.
	before := len(gh.Posted(1))
	if err := s.PollOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if len(gh.Posted(1)) != before {
		t.Fatal("a tag was answered twice")
	}

	var convo strings.Builder
	for _, c := range gh.All(1) {
		convo.WriteString("── @" + c.Login + "\n" + c.Body + "\n")
	}
	t.Logf("issue conversation:\n%s", convo.String())
	all := convo.String()
	// Quote lines echo the collaborator's own words back; everything else is river-guide's.
	var own []string
	for _, l := range strings.Split(strings.Join(gh.Posted(1), "\n"), "\n") {
		if !strings.HasPrefix(l, "> ") {
			own = append(own, l)
		}
	}
	posted := strings.Join(own, "\n")

	for _, leak := range []string{"S7KHNJ0W123456X", "node-7", "48:21:0b", "zanzibar", "quokka", "zanzibar-bootstrap", gh.Token} {
		if strings.Contains(posted, leak) {
			t.Errorf("posted comments leak %q", leak)
		}
	}
	for _, want := range []string{
		"Runink River install a1b2c3d4 started",
		"platform steps 0/1",
		"Refused: remote approvals are off",
		"Runink River step `firmware` (Check the firmware mode): **done** (exit 0)",
		"Refused: Runink River step `install` (Run the installer) is destructive",
		"[river#",
	} {
		if !strings.Contains(all, want) {
			t.Errorf("conversation lacks %q", want)
		}
	}
	if strings.Count(posted, "Refused: remote approvals are off") != 1 {
		t.Error("the non-collaborator's tag was answered")
	}
	if !strings.Contains(log.String(), `"actor":"@mallory (remote)","action":"ignored"`) {
		t.Errorf("ignored tag not audited:\n%s", log.String())
	}
}

func TestPublicRepoWarns(t *testing.T) {
	gh, s, _, _, _ := setup(t)
	gh.Private = false
	warn, err := s.CheckRepo(context.Background())
	if err != nil || !strings.Contains(warn, "PUBLIC") {
		t.Fatalf("warn=%q err=%v", warn, err)
	}
}

func TestExistingIssueIsLabelled(t *testing.T) {
	gh, s, _, _, _ := setup(t)
	s.Issue = 7
	if err := s.Open(context.Background(), "degraded"); err != nil {
		t.Fatal(err)
	}
	if strings.Join(gh.Labels[7], ",") != Label || len(gh.Posted(7)) != 1 {
		t.Fatalf("labels %v posted %d", gh.Labels[7], len(gh.Posted(7)))
	}
}

func TestGuardWithholdsPrivateText(t *testing.T) {
	_, s, _, _, _ := setup(t)
	s.init()
	out := s.guard("hello: The zanzibar control plane bootstraps from the quokka seed on the first node only.")
	if strings.Contains(out, "quokka") || !strings.Contains(out, "withheld") {
		t.Fatalf("guard let private text out: %q", out)
	}
}

func TestParseTags(t *testing.T) {
	got := parseTags("hi\n@river_install status\n  @RIVER_INSTALL   approve firmware\nnot @river_install inline\n@river_installer no")
	if strings.Join(got, "|") != "status|approve firmware" {
		t.Fatalf("%q", got)
	}
}
