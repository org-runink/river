// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

package planner

import (
	"strings"
	"testing"

	"github.com/org-runink/river/installer/internal/hw"
)

type probeT = hw.Probe

const mandatoryOnly = `
tier=embedding variant=e rank=1 resident_mib=1 kv_mib_per_1k=0 ctx_min=0 ctx_max=0
tier=stt variant=s rank=1 resident_mib=1 kv_mib_per_1k=0 ctx_min=0 ctx_max=0
tier=tts variant=t rank=1 resident_mib=1 kv_mib_per_1k=0 ctx_min=0 ctx_max=0
`

func TestParseManifestErrors(t *testing.T) {
	cases := []struct{ in, want string }{
		{"tier=general variant=g rank=1 resident_mib=1 ctx_min=1 ctx_max=1 bogus=1" + mandatoryOnly, `unknown key "bogus"`},
		{"tier=gpu variant=g rank=1 resident_mib=1" + mandatoryOnly, `unknown tier "gpu"`},
		{"tier=general variant=g rank=0 resident_mib=1" + mandatoryOnly, "rank must be >= 1"},
		{"tier=general variant=g rank=1 resident_mib=1 ctx_min=10 ctx_max=5" + mandatoryOnly, "ctx_max < ctx_min"},
		{"tier=general variant=g rank=1" + mandatoryOnly, "resident_mib is required"},
		{"tier=general variant=g rank=1 resident_mib=1\ntier=general variant=g rank=2 resident_mib=1" + mandatoryOnly, "declared twice"},
		{"reserve=gpu mib=1" + mandatoryOnly, `unknown reserve "gpu"`},
		{"tier general" + mandatoryOnly, "is not key=value"},
		{"tier=embedding variant=e rank=1 resident_mib=1", `mandatory tier "stt" has no variant`},
	}
	for _, c := range cases {
		_, err := ParseManifest(strings.NewReader(c.in))
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%q: err %v, want %q", c.in, err, c.want)
		}
	}
}

func TestApplyLock(t *testing.T) {
	man := `
tier=general variant=g rank=1 kv_mib_per_1k=100 ctx_min=1024 ctx_max=1024 lock_repo=example/general-gguf
tier=stt variant=s rank=1 resident_mib=100 lock_role=voice lock_repo=example/stt
tier=embedding variant=e rank=1 resident_mib=10 lock_repo=example/embed
tier=tts variant=t rank=1 resident_mib=1
`
	lock := `# role repo revision file size sha256 license dest
general example/general-gguf 0123456789012345678901234567890123456789 g.gguf 1048576000 aa apache-2.0 -
voice example/stt 0123456789012345678901234567890123456789 s.safetensors 10 bb apache-2.0 stt/s
voice example/stt 0123456789012345678901234567890123456789 params.json 10 cc apache-2.0 stt/p
embed example/embed 0123456789012345678901234567890123456789 e.uqff 10 dd apache-2.0 e/e
`
	m, err := ParseManifest(strings.NewReader(man))
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Resolve(); err == nil {
		t.Fatal("Resolve accepted a variant with no size and no lock")
	}
	if err := m.ApplyLock(strings.NewReader(lock)); err != nil {
		t.Fatal(err)
	}
	if got := m.ByTier("general")[0].ResidentMiB; got != 1100 { // 1000 MiB of files + 10%
		t.Errorf("derived resident %d, want 1100", got)
	}
	if got := m.ByTier("stt")[0].ResidentMiB; got != 100 {
		t.Errorf("explicit resident overwritten: %d", got)
	}

	// A lock_repo the lock does not pin: planned from its resident_mib, and recorded.
	soft, _ := ParseManifest(strings.NewReader(strings.Replace(man, "example/embed", "example/other", 1)))
	if err := soft.ApplyLock(strings.NewReader(lock)); err != nil {
		t.Fatalf("an unpinned variant with a resident_mib stopped the planner: %v", err)
	}
	if len(soft.Unpinned) != 1 || !strings.Contains(soft.Unpinned[0], "example/other") {
		t.Errorf("Unpinned = %q, want the one embedding variant", soft.Unpinned)
	}
	if got := soft.ByTier("embedding")[0].ResidentMiB; got != 10 {
		t.Errorf("unpinned variant resident %d, want its stated 10", got)
	}
	// ... but with no resident_mib there is nothing to plan it from: still an error.
	hard, _ := ParseManifest(strings.NewReader(strings.Replace(man, "example/general-gguf", "example/other", 1)))
	if err := hard.ApplyLock(strings.NewReader(lock)); err == nil || !strings.Contains(err.Error(), "does not pin") {
		t.Errorf("unpinned lock_repo with no size accepted: %v", err)
	}
	if err := m.ApplyLock(strings.NewReader("general a b c 1 d e\n")); err == nil {
		t.Error("7-column lock row accepted")
	}
}

func TestPendingTier(t *testing.T) {
	rest := `
tier=stt variant=s rank=1 resident_mib=1 kv_mib_per_1k=0 ctx_min=0 ctx_max=0
tier=tts variant=t rank=1 resident_mib=1 kv_mib_per_1k=0 ctx_min=0 ctx_max=0
`
	for _, c := range []struct{ in, want string }{
		{"pending=gpu" + rest, `unknown tier "gpu"`},
		{"pending=embedding extra=1" + rest, "exactly pending=<tier>"},
		{"pending=embedding\npending=embedding" + rest, "declared twice"},
		{"pending=embedding\ntier=embedding variant=e rank=1 resident_mib=1" + rest, `tier "embedding" is pending but line 2 gives it a variant`},
		{rest, `mandatory tier "embedding" has no variant`},
	} {
		if _, err := ParseManifest(strings.NewReader(c.in)); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%q: err %v, want %q", c.in, err, c.want)
		}
	}
	m, err := ParseManifest(strings.NewReader("pending=embedding" + rest))
	if err != nil {
		t.Fatalf("a pending mandatory tier must parse: %v", err)
	}
	if !m.Pending["embedding"] || len(m.ByTier("embedding")) != 0 {
		t.Fatalf("pending not recorded: %+v", m.Pending)
	}
}
