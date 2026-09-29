// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/org-runink/river/guide/bundles"
)

// TestEvalCasesCiteRealAnchors keeps eval/cases.json honest: every citation it expects must
// be a section of the built-in guide, or a renamed heading would silently turn a case into
// one no answer can pass.
func TestEvalCasesCiteRealAnchors(t *testing.T) {
	raw, err := os.ReadFile("../../eval/cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []evalCase
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	if len(cases) == 0 {
		t.Fatal("no eval cases: this check would pass having examined nothing")
	}
	b, err := bundles.River()
	if err != nil {
		t.Fatal(err)
	}
	refs := map[string]bool{}
	for _, s := range b.Sections {
		refs[s.Ref()] = true
	}
	cited := 0
	for _, c := range cases {
		if c.Expect == "answer" && len(c.Cites) == 0 {
			t.Errorf("%q expects an answer but cites nothing", c.Q)
		}
		for _, ref := range c.Cites {
			cited++
			if !refs[ref] {
				t.Errorf("%q cites %s, which is not a section of the built-in guide", c.Q, ref)
			}
		}
	}
	// Network first: the eval set asks about it.
	if !strings.Contains(string(raw), "river#set-up-the-network-first") {
		t.Error("no eval case covers the network-first step")
	}
	t.Logf("%d cases, %d citations checked", len(cases), cited)
}
