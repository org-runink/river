// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

package retrieve

import (
	"testing"

	"github.com/org-runink/river/guide/bundle"
)

func TestSearchRanksTitleAndFilters(t *testing.T) {
	pub, _ := bundle.Parse("river", "1", []bundle.Doc{{Path: "a.md", Content: "## Hardware requirements\nAn x86-64-v3 CPU with AVX2.\n\n## Network requirements\nIPv6 only.\n"}})
	priv, _ := bundle.Parse("platform", "1", []bundle.Doc{{Path: "a.md", Content: "## Platform CPU tuning\nCPU pinning for the platform.\n"}})
	priv.Private = true
	ix := New(pub, priv)
	hits := ix.Search("what CPU do I need", 3, nil)
	if len(hits) < 2 || hits[0].Section.Anchor != "platform-cpu-tuning" && hits[0].Section.Anchor != "hardware-requirements" {
		t.Fatalf("hits = %+v", hits)
	}
	hits = ix.Search("cpu", 3, func(s *bundle.Section) bool { return s.Bundle != "platform" })
	for _, h := range hits {
		if h.Section.Bundle == "platform" {
			t.Fatal("filter let a private section through")
		}
	}
	if len(ix.Search("zzzz qqqq", 3, nil)) != 0 {
		t.Fatal("nonsense matched")
	}
}
