// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

package planner

import (
	"bufio"
	"fmt"
	"io"
	"math"
	"sort"
	"strconv"
	"strings"
)

// Tier names. These are generic capability roles, not products.
const (
	TierGeneral   = "general"
	TierCoder     = "coder"
	TierVision    = "vision"
	TierSTT       = "stt"
	TierEmbedding = "embedding"
	TierTTS       = "tts"
)

// MandatoryTiers must fit or the install is refused. Their order is the placement order.
var MandatoryTiers = []string{TierEmbedding, TierSTT, TierTTS}

// OptionalTiers are placed after the mandatory ones, in this priority order.
var OptionalTiers = []string{TierGeneral, TierCoder, TierVision}

func knownTier(t string) bool {
	for _, k := range append(append([]string{}, MandatoryTiers...), OptionalTiers...) {
		if k == t {
			return true
		}
	}
	return false
}

// Variant is one model a tier can be served by. A tier may list several; rank 1 is the
// preferred one, higher ranks are the fallbacks tried in order when it does not fit.
type Variant struct {
	Tier string
	Name string
	Rank int
	// ResidentMiB is the model's resident memory while serving, EXCLUDING the KV cache:
	// weights as loaded (after any load-time quantization) plus the runtime's own working
	// memory.
	ResidentMiB int64
	// KVMiBPer1K is the KV-cache cost of 1024 tokens of context at the serving dtype.
	KVMiBPer1K int64
	CtxMin     int // the smallest context the tier is useful at; never planned below
	CtxMax     int // the largest context worth reserving
	ThreadsMax int // 0 = no cap
	LockRole   string
	LockRepo   string
	Line       int
}

// Manifest is the parsed models.tiers file.
type Manifest struct {
	Variants []Variant
	// Reserves overrides the default non-model RAM reserves by name (os, k0s, platform).
	Reserves map[string]int64
	// Pending lists the tiers whose model is being re-selected (`pending=<tier>`). A pending
	// tier has no variant, is never placed and never budgeted, and does not refuse the
	// install even when it is mandatory: the plan reports it as "pending".
	Pending map[string]bool
	// Unpinned lists the variants whose lock_repo models.lock does not pin but whose resident_mib
	// is known (ApplyLock). They are still planned from resident_mib, and the plan warns: a
	// lock row only pins WEIGHTS, and a medium that carries weights is verified against the
	// lock again when they are installed (river-modelpack verify), so a missing row must not
	// stop the planner — e.g. a tier re-selected in the tiers file before the lock catches up.
	Unpinned []string
}

// ByTier returns a tier's variants in rank order.
func (m *Manifest) ByTier(t string) []Variant {
	var out []Variant
	for _, v := range m.Variants {
		if v.Tier == t {
			out = append(out, v)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Rank < out[j].Rank })
	return out
}

// ParseManifest reads models.tiers: one record per line, whitespace-separated key=value
// fields, `#` comments. Two record kinds:
//
//	tier=<name> variant=<id> rank=<n> resident_mib=<n> kv_mib_per_1k=<n> ctx_min=<n> ctx_max=<n>
//	    [threads_max=<n>] [lock_role=<role>] [lock_repo=<org/name>]
//	reserve=<os|k0s|platform> mib=<n>
//	pending=<tier>
//
// Unknown keys are an error, so a typo cannot silently drop a constraint. A pending tier
// must not also list a variant: the file says either what serves the tier or that nothing
// does yet, never both.
func ParseManifest(r io.Reader) (*Manifest, error) {
	m := &Manifest{Reserves: map[string]int64{}, Pending: map[string]bool{}}
	pendingLine := map[string]int{}
	sc := bufio.NewScanner(r)
	ln := 0
	seen := map[string]bool{}
	for sc.Scan() {
		ln++
		line := sc.Text()
		if i := strings.IndexByte(line, '#'); i >= 0 {
			line = line[:i]
		}
		fs := strings.Fields(line)
		if len(fs) == 0 {
			continue
		}
		kv := map[string]string{}
		for _, f := range fs {
			k, v, ok := strings.Cut(f, "=")
			if !ok || k == "" || v == "" {
				return nil, fmt.Errorf("models.tiers:%d: %q is not key=value", ln, f)
			}
			if _, dup := kv[k]; dup {
				return nil, fmt.Errorf("models.tiers:%d: key %q repeated", ln, k)
			}
			kv[k] = v
		}
		if name, ok := kv["reserve"]; ok {
			if len(kv) != 2 {
				return nil, fmt.Errorf("models.tiers:%d: a reserve line takes exactly reserve= and mib=", ln)
			}
			switch name {
			case "os", "k0s", "platform":
			default:
				return nil, fmt.Errorf("models.tiers:%d: unknown reserve %q (os, k0s, platform)", ln, name)
			}
			n, err := strconv.ParseInt(kv["mib"], 10, 64)
			if err != nil || n < 0 {
				return nil, fmt.Errorf("models.tiers:%d: reserve mib must be a non-negative integer", ln)
			}
			m.Reserves[name] = n
			continue
		}
		if name, ok := kv["pending"]; ok {
			if len(kv) != 1 {
				return nil, fmt.Errorf("models.tiers:%d: a pending line takes exactly pending=<tier>", ln)
			}
			if !knownTier(name) {
				return nil, fmt.Errorf("models.tiers:%d: unknown tier %q", ln, name)
			}
			if m.Pending[name] {
				return nil, fmt.Errorf("models.tiers:%d: pending=%s declared twice", ln, name)
			}
			m.Pending[name] = true
			pendingLine[name] = ln
			continue
		}
		v := Variant{Line: ln}
		for k, val := range kv {
			var err error
			switch k {
			case "tier":
				v.Tier = val
			case "variant":
				v.Name = val
			case "rank":
				v.Rank, err = strconv.Atoi(val)
			case "resident_mib":
				v.ResidentMiB, err = strconv.ParseInt(val, 10, 64)
			case "kv_mib_per_1k":
				v.KVMiBPer1K, err = strconv.ParseInt(val, 10, 64)
			case "ctx_min":
				v.CtxMin, err = strconv.Atoi(val)
			case "ctx_max":
				v.CtxMax, err = strconv.Atoi(val)
			case "threads_max":
				v.ThreadsMax, err = strconv.Atoi(val)
			case "lock_role":
				v.LockRole = val
			case "lock_repo":
				v.LockRepo = val
			default:
				return nil, fmt.Errorf("models.tiers:%d: unknown key %q", ln, k)
			}
			if err != nil {
				return nil, fmt.Errorf("models.tiers:%d: %s: %v", ln, k, err)
			}
		}
		switch {
		case !knownTier(v.Tier):
			return nil, fmt.Errorf("models.tiers:%d: unknown tier %q", ln, v.Tier)
		case v.Name == "":
			return nil, fmt.Errorf("models.tiers:%d: variant= is required", ln)
		case v.Rank < 1:
			return nil, fmt.Errorf("models.tiers:%d: rank must be >= 1", ln)
		case v.ResidentMiB < 0 || v.KVMiBPer1K < 0 || v.CtxMin < 0 || v.ThreadsMax < 0:
			return nil, fmt.Errorf("models.tiers:%d: sizes must be non-negative", ln)
		case v.CtxMax < v.CtxMin:
			return nil, fmt.Errorf("models.tiers:%d: ctx_max < ctx_min", ln)
		}
		if _, ok := kv["resident_mib"]; !ok && v.LockRepo == "" {
			return nil, fmt.Errorf("models.tiers:%d: resident_mib is required (or lock_repo= to derive it from models.lock)", ln)
		}
		if _, ok := kv["resident_mib"]; !ok {
			v.ResidentMiB = -1 // derive from the lock
		}
		key := v.Tier + "/" + v.Name
		if seen[key] {
			return nil, fmt.Errorf("models.tiers:%d: %s declared twice", ln, key)
		}
		seen[key] = true
		m.Variants = append(m.Variants, v)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	for t, pl := range pendingLine {
		if vs := m.ByTier(t); len(vs) > 0 {
			return nil, fmt.Errorf("models.tiers:%d: tier %q is pending but line %d gives it a variant", pl, t, vs[0].Line)
		}
	}
	for _, t := range MandatoryTiers {
		if m.Pending[t] {
			continue
		}
		if len(m.ByTier(t)) == 0 {
			return nil, fmt.Errorf("models.tiers: mandatory tier %q has no variant", t)
		}
	}
	return m, nil
}

// lockRoleAlias maps models.lock role names onto tier names.
var lockRoleAlias = map[string]string{"voice": TierSTT, "embed": TierEmbedding}

// ApplyLock cross-checks every lock_repo= reference against models.lock (the 8-column
// file: role repo revision file size sha256 license dest) and derives resident_mib for
// variants that omitted it: the sum of the locked file sizes for that role+repo, plus 10%
// runtime overhead. A reference with no matching lock row is recorded in Unpinned when the
// variant states resident_mib, and is an error only when its size can come from nothing else.
func (m *Manifest) ApplyLock(r io.Reader) error {
	sizes := map[string]int64{} // role|repo -> bytes
	sc := bufio.NewScanner(r)
	ln := 0
	for sc.Scan() {
		ln++
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fs := strings.Fields(line)
		if len(fs) != 8 {
			return fmt.Errorf("models.lock:%d: expected 8 columns, got %d", ln, len(fs))
		}
		n, err := strconv.ParseInt(fs[4], 10, 64)
		if err != nil {
			return fmt.Errorf("models.lock:%d: size: %v", ln, err)
		}
		role := fs[0]
		if a, ok := lockRoleAlias[role]; ok {
			role = a
		}
		sizes[role+"|"+fs[1]] += n
	}
	if err := sc.Err(); err != nil {
		return err
	}
	for i := range m.Variants {
		v := &m.Variants[i]
		if v.LockRepo == "" {
			continue
		}
		role := v.LockRole
		if role == "" {
			role = v.Tier
		}
		if a, ok := lockRoleAlias[role]; ok {
			role = a
		}
		b, ok := sizes[role+"|"+v.LockRepo]
		if !ok {
			if v.ResidentMiB < 0 {
				return fmt.Errorf("models.tiers:%d: %s/%s names lock_repo=%s, which models.lock does not pin for role %s, and states no resident_mib",
					v.Line, v.Tier, v.Name, v.LockRepo, role)
			}
			m.Unpinned = append(m.Unpinned, fmt.Sprintf("%s/%s: lock_repo=%s is not pinned by models.lock (role %s); planned from its resident_mib=%d",
				v.Tier, v.Name, v.LockRepo, role, v.ResidentMiB))
			continue
		}
		if v.ResidentMiB < 0 {
			v.ResidentMiB = int64(math.Ceil(float64(b) / (1 << 20) * 1.10))
		}
	}
	for _, v := range m.Variants {
		if v.ResidentMiB < 0 {
			return fmt.Errorf("models.tiers:%d: resident_mib missing and not derivable", v.Line)
		}
	}
	return nil
}

// Resolve fails when a variant still needs the lock to know its size.
func (m *Manifest) Resolve() error {
	for _, v := range m.Variants {
		if v.ResidentMiB < 0 {
			return fmt.Errorf("models.tiers:%d: %s/%s has no resident_mib; pass --lock models.lock to derive it", v.Line, v.Tier, v.Name)
		}
	}
	return nil
}
