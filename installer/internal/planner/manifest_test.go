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
		err := parseResolve(c.in)
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
		{"pending=gpu\npending=embedding" + rest, `unknown tier "gpu"`},
		{"pending=GPU" + rest, `not a tier name`},
		{"pending=embedding extra=1" + rest, "exactly pending=<tier>"},
		{"pending=embedding\npending=embedding" + rest, "declared twice"},
		{"pending=embedding\ntier=embedding variant=e rank=1 resident_mib=1" + rest, `tier "embedding" is pending but line 2 gives it a variant`},
		{rest, `mandatory tier "embedding" has no variant`},
	} {
		if err := parseResolve(c.in); err == nil || !strings.Contains(err.Error(), c.want) {
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

// parseResolve is what river-plan does without --lock: parse, then resolve.
func parseResolve(in string) error {
	m, err := ParseManifest(strings.NewReader(in))
	if err != nil {
		return err
	}
	return m.Resolve()
}

// Tiers that are not built in are accepted when models.lock pins their role, placed after the
// built-in optional tiers in the order the file names them, and refused otherwise.
func TestExtraTiers(t *testing.T) {
	man := mandatoryOnly + `
tier=rerank   variant=r rank=1 resident_mib=100 kv_mib_per_1k=0 ctx_min=0 ctx_max=0 threads_max=2 lock_repo=example/rerank
tier=imagegen variant=i rank=1 kv_mib_per_1k=0 ctx_min=0 ctx_max=0 lock_repo=example/image
tier=guard    variant=g rank=1 resident_mib=50 kv_mib_per_1k=10 ctx_min=1024 ctx_max=2048 lock_role=safety lock_repo=example/guard
tier=general  variant=gen rank=1 resident_mib=10 kv_mib_per_1k=0 ctx_min=0 ctx_max=0
`
	lock := `# role repo revision file size sha256 license dest
rerank example/rerank 0123456789012345678901234567890123456789 r.gguf 10 aa apache-2.0 r/r
imagegen example/image 0123456789012345678901234567890123456789 i.safetensors 1048576000 bb apache-2.0 i/i
safety example/guard 0123456789012345678901234567890123456789 g.gguf 10 cc apache-2.0 g/g
`
	m, err := ParseManifest(strings.NewReader(man))
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Resolve(); err == nil || !strings.Contains(err.Error(), `unknown tier "rerank"`) {
		t.Fatalf("an extra tier resolved without the lock: %v", err)
	}
	if err := m.ApplyLock(strings.NewReader(lock)); err != nil {
		t.Fatal(err)
	}
	if err := m.Resolve(); err != nil {
		t.Fatal(err)
	}
	want := []string{"embedding", "stt", "tts", "general", "coder", "vision", "rerank", "imagegen", "guard"}
	if got := m.Tiers(); strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("tier order %v, want %v", got, want)
	}
	if got := m.ByTier("imagegen")[0].ResidentMiB; got != 1100 { // 1000 MiB of weights + 10%
		t.Errorf("imagegen resident %d, want 1100", got)
	}

	// Planned: optional, after the built-in tiers.
	pl := Build(loadProbe(t, "server-256g-avx512.probe.json"), m, Options{})
	var names []string
	for _, tp := range pl.Tiers {
		names = append(names, tp.Tier)
		if tp.Tier == "rerank" || tp.Tier == "imagegen" || tp.Tier == "guard" {
			if tp.Mandatory || !tp.Placed() {
				t.Errorf("%s: mandatory=%v status=%s %v", tp.Tier, tp.Mandatory, tp.Status, tp.Reasons)
			}
		}
		if tp.Tier == "rerank" && tp.Threads > 2 {
			t.Errorf("rerank threads %d, threads_max 2", tp.Threads)
		}
	}
	if strings.Join(names, " ") != strings.Join(want, " ") {
		t.Errorf("plan tiers %v", names)
	}

	// A tier the lock does not pin a role for: refused (a typo cannot become a tier).
	typo, _ := ParseManifest(strings.NewReader(strings.Replace(man, "tier=rerank ", "tier=rernak ", 1)))
	if err := typo.ApplyLock(strings.NewReader(lock)); err == nil || !strings.Contains(err.Error(), `unknown tier "rernak"`) {
		t.Errorf("typo tier: %v", err)
	}
	// Pending extra tiers follow the same rule.
	pend, err := ParseManifest(strings.NewReader(mandatoryOnly + "pending=rerank\n"))
	if err != nil {
		t.Fatal(err)
	}
	if err := pend.ApplyLock(strings.NewReader(lock)); err != nil || !pend.Pending["rerank"] {
		t.Errorf("pending extra tier: %v", err)
	}
}
