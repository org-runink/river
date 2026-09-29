// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

package agent

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/org-runink/river/guide/bundle"
	"github.com/org-runink/river/guide/bundles"
	"github.com/org-runink/river/guide/model"
)

// fakeModel returns a canned reply and records the prompt it saw.
type fakeModel struct {
	reply string
	err   error
	saw   string
}

func (f *fakeModel) Chat(_ context.Context, msgs []model.Message) (string, error) {
	f.saw = msgs[len(msgs)-1].Content
	return f.reply, f.err
}

func river(t *testing.T) *bundle.Bundle {
	t.Helper()
	b, err := bundles.River()
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func platform(t *testing.T) *bundle.Bundle {
	t.Helper()
	b, err := bundle.Parse("platform", "1", []bundle.Doc{{Path: "p.md", Content: "## Secret platform bootstrap\n<!-- river-guide:step id=bootstrap kind=action run=\"platformctl bootstrap\" -->\nThe CPU bootstrap uses the zanzibar quokka procedure with platformctl.\n"}})
	if err != nil {
		t.Fatal(err)
	}
	b.Private = true
	return b
}

func TestGroundedAnswerKeepsValidCitations(t *testing.T) {
	m := &fakeModel{reply: "Run `sudo runink-install` on the second console; river-guide does not run it [river#run-the-installer] [river#made-up]."}
	a := New(m, river(t))
	ans := a.Ask(context.Background(), "what command installs to the disk", ScopeLocal)
	if ans.Mode != ModeModel {
		t.Fatalf("mode %s note %q", ans.Mode, ans.Note)
	}
	if len(ans.Citations) != 1 || ans.Citations[0] != "river#run-the-installer" {
		t.Fatalf("citations %v", ans.Citations)
	}
	if strings.Contains(ans.Text, "made-up") {
		t.Fatal("invalid citation kept")
	}
	if !strings.Contains(m.saw, "[river#run-the-installer]") {
		t.Fatal("prompt did not carry the excerpt ids")
	}
}

func TestInventedCommandIsWithheld(t *testing.T) {
	m := &fakeModel{reply: "Wipe it first with `dd if=/dev/zero of=/dev/nvme0n1` then `sudo runink-install` [river#run-the-installer]."}
	ans := New(m, river(t)).Ask(context.Background(), "how do I install to the disk", ScopeLocal)
	if ans.Mode != ModeDegraded || !strings.Contains(ans.Note, "not in the guide") {
		t.Fatalf("invented command not withheld: %+v", ans)
	}
	if strings.Contains(ans.Text, "dd if=") {
		t.Fatal("the invented command reached the operator")
	}
}

func TestPromptLineCommandsAreChecked(t *testing.T) {
	m := &fakeModel{reply: "Do this:\n$ zpool destroy -f zriver\n[river#run-the-installer]"}
	ans := New(m, river(t)).Ask(context.Background(), "how do I install to the disk", ScopeLocal)
	if ans.Mode == ModeModel {
		t.Fatalf("shell-prompt command slipped through: %+v", ans)
	}
}

func TestOutsideKnowledgeIsWithheld(t *testing.T) {
	m := &fakeModel{reply: "Install Windows first using the Microsoft media creation tool, shrink the NTFS partition in Disk Management, then boot Windows Boot Manager and choose dual boot."}
	ans := New(m, river(t)).Ask(context.Background(), "how do I install windows alongside river", ScopeLocal)
	if ans.Mode == ModeModel {
		t.Fatalf("unsupported answer accepted: %+v", ans)
	}
}

func TestNotInGuide(t *testing.T) {
	ans := New(&fakeModel{reply: "NOT IN GUIDE"}, river(t)).Ask(context.Background(), "how do I configure the disk write cache", ScopeLocal)
	if ans.Mode != ModeNotFound {
		t.Fatalf("mode %s", ans.Mode)
	}
	ans = New(&fakeModel{reply: "x"}, river(t)).Ask(context.Background(), "qqqq zzzz", ScopeLocal)
	if ans.Mode != ModeNotFound {
		t.Fatalf("no-hit mode %s", ans.Mode)
	}
}

func TestDegradedWithoutModelAndOnModelError(t *testing.T) {
	a := New(nil, river(t))
	ans := a.Ask(context.Background(), "recovery key", ScopeLocal)
	if ans.Mode != ModeDegraded || len(ans.Citations) == 0 || !strings.Contains(ans.Text, "[river#") {
		t.Fatalf("%+v", ans)
	}
	a.SetModel(&fakeModel{err: errors.New("connection refused")})
	ans = a.Ask(context.Background(), "recovery key", ScopeLocal)
	if ans.Mode != ModeDegraded || !strings.Contains(ans.Note, "unavailable") {
		t.Fatalf("%+v", ans)
	}
}

func TestPublicScopeNeverSeesPrivateBundle(t *testing.T) {
	m := &fakeModel{reply: "NOT IN GUIDE"}
	a := New(m, river(t))
	a.AddBundle(platform(t))
	local := a.Ask(context.Background(), "zanzibar quokka bootstrap", ScopeLocal)
	if !strings.Contains(m.saw, "zanzibar") {
		t.Fatalf("local scope did not retrieve the platform bundle: %+v", local)
	}
	m.saw = ""
	pub := a.Ask(context.Background(), "zanzibar quokka bootstrap", ScopePublic)
	if strings.Contains(m.saw, "zanzibar") || strings.Contains(pub.Text, "zanzibar") {
		t.Fatal("private bundle text reached a public-scope prompt or answer")
	}
	for _, c := range pub.Citations {
		if strings.HasPrefix(c, "platform#") {
			t.Fatal("public answer cites the private bundle")
		}
	}
}
