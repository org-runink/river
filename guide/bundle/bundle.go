// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

// Package bundle loads "guide bundles": the markdown knowledge river-guide answers from
// and the ordered install steps it walks.
//
// A bundle is one or more markdown documents. Headings split them into sections, and
// every section has a stable anchor (GitHub's slug of its heading) that answers cite as
// [<bundle>#<anchor>]. A section becomes an install STEP when an HTML comment directive
// follows its heading:
//
//	## Probe the hardware
//	<!-- river-guide:step id=hwprobe kind=check run="river-hwprobe --json" -->
//
// kind is one of:
//
//	info         read, then the operator marks it done
//	check        read-only command river-guide may run after approval
//	action       non-destructive command river-guide may run after approval
//	destructive  NEVER run by river-guide; the operator runs it at the console
//
// The public Runink River install guide is the built-in bundle (package bundles). A downstream
// platform may supply a PRIVATE bundle, fetched after sign-in and verified with an
// ed25519 key (see Encode/Decode/Verify and docs/INSTALL-GUIDE-AGENT.md).
package bundle

import (
	"errors"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"
	"unicode"
)

// Kind classifies a step by what river-guide is allowed to do with it.
type Kind string

const (
	KindInfo        Kind = "info"
	KindCheck       Kind = "check"
	KindAction      Kind = "action"
	KindDestructive Kind = "destructive"
)

// Runnable reports whether river-guide itself may execute the step's command.
func (k Kind) Runnable() bool { return k == KindCheck || k == KindAction }

// StepDef is the static definition of one install step, taken verbatim from a bundle.
type StepDef struct {
	ID     string
	Kind   Kind
	Run    string // the ONLY command river-guide may execute for this step (argv, no shell)
	Title  string
	Ref    string // bundle#anchor
	Bundle string
}

// Section is one heading and the text under it.
type Section struct {
	Bundle string
	Doc    string
	Anchor string
	Title  string
	Level  int
	Body   string   // markdown, directive comments removed
	Code   []string // inline code spans and fenced-code lines, normalised
	Step   *StepDef
}

// Ref is the citation token for the section.
func (s *Section) Ref() string { return s.Bundle + "#" + s.Anchor }

// Bundle is a loaded, parsed guide bundle.
type Bundle struct {
	Name     string
	Version  string
	Private  bool // supplied by a downstream platform; never quoted outside the local console
	Sections []*Section
	Steps    []*StepDef
}

// Doc is one markdown document of a bundle.
type Doc struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

// Parse builds a bundle from documents, in the order given (callers sort by path).
func Parse(name, version string, docs []Doc) (*Bundle, error) {
	if !validName(name) {
		return nil, fmt.Errorf("bundle name %q: want [a-z0-9-]+", name)
	}
	b := &Bundle{Name: name, Version: version}
	seenAnchor := map[string]int{}
	seenStep := map[string]bool{}
	for _, d := range docs {
		secs, err := parseDoc(name, d, seenAnchor)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", d.Path, err)
		}
		for _, s := range secs {
			if s.Step != nil {
				if seenStep[s.Step.ID] {
					return nil, fmt.Errorf("%s: duplicate step id %q", d.Path, s.Step.ID)
				}
				seenStep[s.Step.ID] = true
				b.Steps = append(b.Steps, s.Step)
			}
		}
		b.Sections = append(b.Sections, secs...)
	}
	if len(b.Sections) == 0 {
		return nil, errors.New("bundle has no sections")
	}
	return b, nil
}

// LoadFS reads every *.md file under root of fsys (sorted by path) as one bundle.
func LoadFS(fsys fs.FS, root, name, version string) (*Bundle, error) {
	var docs []Doc
	err := fs.WalkDir(fsys, root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || path.Ext(p) != ".md" {
			return nil
		}
		buf, err := fs.ReadFile(fsys, p)
		if err != nil {
			return err
		}
		rel := strings.TrimPrefix(strings.TrimPrefix(p, root), "/")
		docs = append(docs, Doc{Path: rel, Content: string(buf)})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(docs, func(i, j int) bool { return docs[i].Path < docs[j].Path })
	return Parse(name, version, docs)
}

func validName(s string) bool {
	if s == "" || len(s) > 64 {
		return false
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-') {
			return false
		}
	}
	return true
}

const directivePrefix = "<!-- river-guide:step"

func parseDoc(bundle string, d Doc, seen map[string]int) ([]*Section, error) {
	var out []*Section
	var cur *Section
	var body []string
	inFence := false
	flush := func() {
		if cur == nil {
			return
		}
		cur.Body = strings.TrimSpace(strings.Join(body, "\n"))
		cur.Code = extractCode(cur.Body)
		if cur.Step != nil && cur.Step.Run != "" {
			cur.Code = append(cur.Code, cur.Step.Run)
		}
		out = append(out, cur)
	}
	for _, line := range strings.Split(strings.ReplaceAll(d.Content, "\r\n", "\n"), "\n") {
		trim := strings.TrimSpace(line)
		if strings.HasPrefix(trim, "```") || strings.HasPrefix(trim, "~~~") {
			inFence = !inFence
		}
		if !inFence {
			if lvl, title := heading(line); lvl > 0 {
				flush()
				anchor := Slug(title)
				if n := seen[anchor]; n > 0 {
					seen[anchor] = n + 1
					anchor = fmt.Sprintf("%s-%d", anchor, n)
				} else {
					seen[anchor] = 1
				}
				cur = &Section{Bundle: bundle, Doc: d.Path, Anchor: anchor, Title: title, Level: lvl}
				body = nil
				continue
			}
			if strings.HasPrefix(trim, directivePrefix) {
				if cur == nil {
					return nil, errors.New("step directive before any heading")
				}
				if cur.Step != nil {
					return nil, fmt.Errorf("section %q has two step directives", cur.Title)
				}
				st, err := parseDirective(trim)
				if err != nil {
					return nil, fmt.Errorf("section %q: %w", cur.Title, err)
				}
				st.Title, st.Bundle, st.Ref = cur.Title, bundle, bundle+"#"+cur.Anchor
				cur.Step = st
				continue
			}
		}
		if cur == nil {
			// Text before the first heading: give it a synthetic section so it is searchable.
			cur = &Section{Bundle: bundle, Doc: d.Path, Anchor: Slug(strings.TrimSuffix(path.Base(d.Path), ".md")), Title: d.Path, Level: 1}
		}
		body = append(body, line)
	}
	if inFence {
		return nil, errors.New("unterminated code fence")
	}
	flush()
	return out, nil
}

func heading(line string) (int, string) {
	if !strings.HasPrefix(line, "#") {
		return 0, ""
	}
	n := 0
	for n < len(line) && line[n] == '#' {
		n++
	}
	if n > 6 || n >= len(line) || line[n] != ' ' {
		return 0, ""
	}
	return n, strings.TrimSpace(strings.TrimRight(strings.TrimSpace(line[n:]), "#"))
}

// Slug is GitHub's heading-anchor algorithm: lowercase, drop punctuation other than
// '-' and '_', spaces become '-'.
func Slug(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(s)) {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_':
			b.WriteRune(r)
		case r == ' ':
			b.WriteByte('-')
		}
	}
	return b.String()
}

func parseDirective(s string) (*StepDef, error) {
	s = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(s, directivePrefix), "-->"))
	kv, err := splitKV(s)
	if err != nil {
		return nil, err
	}
	st := &StepDef{ID: kv["id"], Kind: Kind(kv["kind"]), Run: normalize(kv["run"])}
	for k := range kv {
		switch k {
		case "id", "kind", "run":
		default:
			return nil, fmt.Errorf("unknown directive key %q", k)
		}
	}
	if !validName(st.ID) {
		return nil, fmt.Errorf("step id %q: want [a-z0-9-]+", st.ID)
	}
	switch st.Kind {
	case KindInfo:
		if st.Run != "" {
			return nil, fmt.Errorf("step %s: kind=info takes no run=", st.ID)
		}
	case KindCheck, KindAction, KindDestructive:
		if st.Run == "" {
			return nil, fmt.Errorf("step %s: kind=%s needs run=", st.ID, st.Kind)
		}
	default:
		return nil, fmt.Errorf("step %s: unknown kind %q", st.ID, st.Kind)
	}
	if st.Kind.Runnable() {
		if _, err := SplitArgs(st.Run); err != nil {
			return nil, fmt.Errorf("step %s: %w", st.ID, err)
		}
	}
	return st, nil
}

// splitKV parses `k=v k="quoted v"`.
func splitKV(s string) (map[string]string, error) {
	out := map[string]string{}
	toks, err := SplitArgs(s)
	if err != nil {
		return nil, err
	}
	for _, t := range toks {
		k, v, ok := strings.Cut(t, "=")
		if !ok || k == "" {
			return nil, fmt.Errorf("directive token %q is not key=value", t)
		}
		if _, dup := out[k]; dup {
			return nil, fmt.Errorf("directive key %q repeated", k)
		}
		out[k] = v
	}
	return out, nil
}

// SplitArgs splits a command line on spaces, honouring double and single quotes. There
// is no shell: no globbing, no variables, no pipes. Metacharacters are refused outright
// so a bundle cannot smuggle a pipeline into a step that river-guide executes.
func SplitArgs(s string) ([]string, error) {
	var out []string
	var cur strings.Builder
	var quote rune
	have := false
	for _, r := range s {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				cur.WriteRune(r)
			}
		case r == '"' || r == '\'':
			quote, have = r, true
		case r == ' ' || r == '\t':
			if have {
				out = append(out, cur.String())
				cur.Reset()
				have = false
			}
		case strings.ContainsRune("|;&<>`$(){}\\", r):
			return nil, fmt.Errorf("shell metacharacter %q is not allowed in a step command", r)
		default:
			cur.WriteRune(r)
			have = true
		}
	}
	if quote != 0 {
		return nil, errors.New("unterminated quote")
	}
	if have {
		out = append(out, cur.String())
	}
	return out, nil
}

// normalize collapses whitespace so command comparisons are not defeated by spacing.
func normalize(s string) string { return strings.Join(strings.Fields(s), " ") }

// Normalize is exported for callers comparing a model's command against the guide.
func Normalize(s string) string { return normalize(strings.TrimPrefix(strings.TrimSpace(s), "$ ")) }

// extractCode returns inline code spans and fenced-code lines, normalised. These are the
// only strings river-guide will accept as "a command the guide contains".
func extractCode(body string) []string {
	var out []string
	inFence := false
	for _, line := range strings.Split(body, "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~") {
			inFence = !inFence
			continue
		}
		if inFence {
			if c := Normalize(t); c != "" && !strings.HasPrefix(c, "#") {
				out = append(out, c)
			}
			continue
		}
		for {
			i := strings.IndexByte(line, '`')
			if i < 0 {
				break
			}
			j := strings.IndexByte(line[i+1:], '`')
			if j < 0 {
				break
			}
			if c := Normalize(line[i+1 : i+1+j]); c != "" {
				out = append(out, c)
			}
			line = line[i+1+j+1:]
		}
	}
	return out
}

// Section returns the section with the given anchor, or nil.
func (b *Bundle) Section(anchor string) *Section {
	for _, s := range b.Sections {
		if s.Anchor == anchor {
			return s
		}
	}
	return nil
}
