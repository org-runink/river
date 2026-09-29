// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

// Package agent is river-guide's reasoning core: grounded question answering over the
// loaded guide bundles, and the install step machine (steps.go).
//
// Grounding rules, enforced here rather than trusted to the model:
//
//   - Retrieval picks the excerpts; the model only sees those.
//   - Every command in a model answer must appear verbatim in a retrieved excerpt's code.
//     One invented command and the whole model answer is withheld in favour of the
//     excerpts themselves (degraded answer).
//   - Citations are checked against the retrieved set; unknown ones are dropped, and an
//     answer with none gets the top excerpts' anchors appended.
//   - A remote (issue-comment) question is answered from PUBLIC bundles only.
//   - Model output is text for a human. Nothing in this package executes it.
package agent

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"sync"

	"github.com/org-runink/river/guide/bundle"
	"github.com/org-runink/river/guide/model"
	"github.com/org-runink/river/guide/retrieve"
)

// Mode is how an answer was produced.
type Mode string

const (
	ModeModel    Mode = "model"
	ModeDegraded Mode = "degraded"
	ModeNotFound Mode = "not-found"
)

// Answer is a grounded reply.
type Answer struct {
	Text      string
	Citations []string // bundle#anchor
	Mode      Mode
	Note      string // why a model answer was withheld, or why there is no model
}

// Agent holds the loaded bundles.
type Agent struct {
	mu      sync.RWMutex
	bundles []*bundle.Bundle
	index   *retrieve.Index
	Model   model.Chatter // nil = degraded mode; use SetModel after construction
	TopK    int
}

// New builds an agent over the built-in bundle(s).
func New(m model.Chatter, bundles ...*bundle.Bundle) *Agent {
	a := &Agent{Model: m, TopK: 4}
	a.bundles = append(a.bundles, bundles...)
	a.index = retrieve.New(a.bundles...)
	return a
}

// AddBundle loads another bundle (e.g. a verified private platform bundle).
func (a *Agent) AddBundle(b *bundle.Bundle) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.bundles = append(a.bundles, b)
	a.index = retrieve.New(a.bundles...)
}

// Bundles lists the loaded bundles.
func (a *Agent) Bundles() []*bundle.Bundle {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return append([]*bundle.Bundle(nil), a.bundles...)
}

// HasPrivate reports whether a platform bundle is loaded.
func (a *Agent) HasPrivate() bool {
	for _, b := range a.Bundles() {
		if b.Private {
			return true
		}
	}
	return false
}

// Scope selects which bundles an answer may draw on.
type Scope int

const (
	ScopeLocal  Scope = iota // console: every loaded bundle
	ScopePublic              // issue comments: public bundles only
)

const systemPrompt = `You are river-guide, the offline installation assistant on a Runink River install medium.
Rules:
1. Answer ONLY from the guide excerpts in the user message. Do not use outside knowledge.
2. Cite the excerpt ids you used in square brackets, exactly as given, e.g. [river#probe-the-hardware].
3. If the excerpts do not answer the question, reply with exactly: NOT IN GUIDE
4. Only write a command if it appears verbatim in an excerpt. Put commands in backticks.
5. Never tell the operator to run a disk-wiping command other than the guide's own installer step.
6. Be brief: at most 120 words.`

const notInGuide = "NOT IN GUIDE"

// Ask answers q within scope.
func (a *Agent) Ask(ctx context.Context, q string, scope Scope) Answer {
	a.mu.RLock()
	ix, m := a.index, a.Model
	a.mu.RUnlock()
	var filter func(*bundle.Section) bool
	if scope == ScopePublic {
		filter = func(s *bundle.Section) bool { return !a.isPrivate(s.Bundle) }
	}
	hits := ix.Search(q, a.TopK, filter)
	if len(hits) == 0 {
		return Answer{Mode: ModeNotFound, Text: "The loaded guide does not cover that. Try `steps`, or rephrase with the component name (disk, pool, network, firmware, ...)."}
	}
	if m == nil {
		ans := degraded(hits)
		ans.Note = "no guide model on this machine: showing the matching guide sections"
		return ans
	}
	reply, err := m.Chat(ctx, []model.Message{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: userPrompt(q, hits)},
	})
	if err != nil {
		ans := degraded(hits)
		ans.Note = "guide model unavailable (" + err.Error() + "): showing the matching guide sections"
		return ans
	}
	return validate(q, reply, hits)
}

func (a *Agent) isPrivate(name string) bool {
	for _, b := range a.Bundles() {
		if b.Name == name {
			return b.Private
		}
	}
	return false
}

func userPrompt(q string, hits []retrieve.Hit) string {
	var b strings.Builder
	b.WriteString("Guide excerpts:\n")
	for _, h := range hits {
		body := h.Section.Body
		if len(body) > 1800 {
			body = body[:1800] + "\n[…]"
		}
		fmt.Fprintf(&b, "\n[%s] %s\n%s\n", h.Section.Ref(), h.Section.Title, body)
	}
	fmt.Fprintf(&b, "\nQuestion: %s\n", q)
	return b.String()
}

func degraded(hits []retrieve.Hit) Answer {
	var b strings.Builder
	var cites []string
	for i, h := range hits {
		if i >= 2 {
			break
		}
		fmt.Fprintf(&b, "%s [%s]\n%s\n\n", h.Section.Title, h.Section.Ref(), excerpt(h.Section.Body, 700))
		cites = append(cites, h.Section.Ref())
	}
	if len(hits) > 2 {
		b.WriteString("See also:")
		for _, h := range hits[2:] {
			fmt.Fprintf(&b, " [%s]", h.Section.Ref())
			cites = append(cites, h.Section.Ref())
		}
		b.WriteString("\n")
	}
	return Answer{Text: strings.TrimSpace(b.String()), Citations: cites, Mode: ModeDegraded}
}

func excerpt(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := strings.LastIndex(s[:n], "\n")
	if cut < n/2 {
		cut = n
	}
	return s[:cut] + "\n[…]"
}

var (
	citeRe     = regexp.MustCompile(`\[([a-z0-9-]+#[a-z0-9_-]+)\]`)
	inlineRe   = regexp.MustCompile("`([^`\n]+)`")
	shellishRe = regexp.MustCompile(`(?m)^\s*(\$ |sudo |# )(.+)$`)
)

// validate enforces the grounding rules on a model reply.
func validate(q, reply string, hits []retrieve.Hit) Answer {
	reply = strings.TrimSpace(reply)
	if reply == "" || strings.Contains(strings.ToUpper(reply), notInGuide) {
		ans := degraded(hits)
		ans.Mode = ModeNotFound
		ans.Note = "the guide model found no answer in the guide; closest sections follow"
		return ans
	}
	allowed := map[string]bool{}
	valid := map[string]bool{}
	for _, h := range hits {
		valid[h.Section.Ref()] = true
		for _, c := range h.Section.Code {
			allowed[c] = true
		}
	}
	for _, c := range commandsIn(reply) {
		if !commandGrounded(c, allowed) {
			ans := degraded(hits)
			ans.Note = fmt.Sprintf("model answer withheld: it contained a command that is not in the guide (%q)", c)
			return ans
		}
	}
	var cites []string
	seen := map[string]bool{}
	text := citeRe.ReplaceAllStringFunc(reply, func(m string) string {
		ref := m[1 : len(m)-1]
		if !valid[ref] {
			return ""
		}
		if !seen[ref] {
			seen[ref] = true
			cites = append(cites, ref)
		}
		return m
	})
	text = strings.TrimSpace(text)
	if s := support(q, text, hits); s < minSupport {
		ans := degraded(hits)
		ans.Note = fmt.Sprintf("model answer withheld: only %.0f%% of it is supported by the guide text", s*100)
		return ans
	}
	if len(cites) == 0 {
		// Uncited (small models often forget): attribute to the excerpt the answer overlaps
		// most, so the operator can check the source.
		ref := bestSource(text, hits)
		cites = []string{ref}
		text += "\n\nSource: [" + ref + "]"
	}
	return Answer{Text: text, Citations: cites, Mode: ModeModel}
}

// commandsIn extracts what looks like a command from a reply: backticked spans that look
// like commands, and `$ `/`sudo ` lines.
func commandsIn(s string) []string {
	var out []string
	for _, m := range inlineRe.FindAllStringSubmatch(s, -1) {
		c := bundle.Normalize(m[1])
		if looksLikeCommand(c) {
			out = append(out, c)
		}
	}
	for _, m := range shellishRe.FindAllStringSubmatch(s, -1) {
		c := bundle.Normalize(m[2]) // "$ cmd" and "# cmd" (root prompt) carry the command after the prompt
		if m[1] == "sudo " {
			c = bundle.Normalize("sudo " + m[2])
		}
		if looksLikeCommand(c) {
			out = append(out, c)
		}
	}
	return out
}

// looksLikeCommand: more than one word, or a single word that is an executable-style
// token (contains '-' or '/' and no spaces). A bare word like `zriver` or `YES` is a
// value, not a command.
func looksLikeCommand(c string) bool {
	if c == "" {
		return false
	}
	if strings.Contains(c, " ") {
		return true
	}
	return strings.HasPrefix(c, "/") && strings.Count(c, "/") > 1 && !strings.Contains(c, ".")
}

// commandGrounded: the command equals, or is a prefix-free substring of, a guide code
// string. Substring (not only equality) lets an answer quote `sudo runink-install` out
// of a longer fenced line — but never lets it ADD words the guide does not have.
func commandGrounded(c string, allowed map[string]bool) bool {
	if allowed[c] {
		return true
	}
	for a := range allowed {
		if strings.Contains(a, c) {
			return true
		}
	}
	return false
}

// SetModel swaps the model (nil = degraded). Used when the model server finishes loading
// after the guide has already started.
func (a *Agent) SetModel(m model.Chatter) {
	a.mu.Lock()
	a.Model = m
	a.mu.Unlock()
}

// HasModel reports whether answers are model-phrased.
func (a *Agent) HasModel() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.Model != nil
}

// minSupport is the share of an answer's content words that must occur in the retrieved
// guide text (or the question). Measured on the eval set: grounded answers score
// 0.8-1.0; a model answering from outside knowledge scores well under 0.6.
const minSupport = 0.7

// support is the fraction of the answer's content tokens found in the excerpts or the
// question. It is a cheap, model-independent check that the answer paraphrases the guide
// instead of adding to it.
func support(q, answer string, hits []retrieve.Hit) float64 {
	known := map[string]bool{}
	for _, t := range retrieve.Tokens(q) {
		known[t] = true
	}
	for _, h := range hits {
		for _, t := range retrieve.Tokens(h.Section.Title + " " + h.Section.Body) {
			known[t] = true
		}
	}
	toks := retrieve.Tokens(citeRe.ReplaceAllString(answer, ""))
	if len(toks) == 0 {
		return 0
	}
	n := 0
	for _, t := range toks {
		if known[t] || commonWord[t] {
			n++
		}
	}
	return float64(n) / float64(len(toks))
}

// commonWord: function words a paraphrase needs that a guide may not happen to contain.
var commonWord = map[string]bool{}

func init() {
	for _, w := range strings.Fields(`also any all only not no yes must need needs required
	require requires use used using first then after before must make sure ensure following
	step steps guide section note however instead other more less same without within once
	available unavailable yourself type run runs command commands do does done has have had
	was were been being able unable here there these those such each every both either`) {
		commonWord[w] = true
	}
}

// bestSource picks the retrieved section sharing the most tokens with the answer.
func bestSource(answer string, hits []retrieve.Hit) string {
	at := map[string]bool{}
	for _, t := range retrieve.Tokens(answer) {
		at[t] = true
	}
	best, bestN := hits[0].Section.Ref(), -1
	for _, h := range hits {
		n := 0
		seen := map[string]bool{}
		for _, t := range retrieve.Tokens(h.Section.Title + " " + h.Section.Body) {
			if at[t] && !seen[t] {
				seen[t] = true
				n++
			}
		}
		if n > bestN {
			best, bestN = h.Section.Ref(), n
		}
	}
	return best
}
