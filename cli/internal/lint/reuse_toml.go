// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

package lint

import (
	_ "embed"
	"errors"
	"fmt"
	"path"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"
)

// REUSE.toml (REUSE 3.3): version = 1 and [[annotations]] tables, each with a path glob (or
// a list of them), an optional precedence, SPDX-FileCopyrightText and SPDX-License-Identifier
// (a string or a list of strings). Paths are relative to the REUSE.toml's directory. `*`
// matches within one path segment, `**` across segments, `\*` is a literal star.
//
// The TOML reader is the subset these files use (comments, strings of all four kinds,
// integers, booleans, arrays, [[annotations]] headers). Anything else is an error, never a
// silent skip.

// The annotation keys, split so that no line of this file reads as its own SPDX header.
const (
	keyCopyright = "SPDX-File" + "CopyrightText"
	keyLicense   = "SPDX-License-" + "Identifier"
)

type reusePrecedence int

const (
	precClosest reusePrecedence = iota
	precAggregate
	precOverride
)

type reuseAnnotation struct {
	index      int // 1-based, for messages
	globs      []*regexp.Regexp
	precedence reusePrecedence
	info       reuseInfo
}

type reuseTOML struct {
	file        string // repository-relative path of the REUSE.toml
	dir         string // its directory, "" at the root, else ending in "/"
	annotations []*reuseAnnotation
}

// match returns the LAST annotation whose glob matches rel, or nil.
func (t *reuseTOML) match(rel string) *reuseAnnotation {
	if !strings.HasPrefix(rel, t.dir) {
		return nil
	}
	sub := rel[len(t.dir):]
	for i := len(t.annotations) - 1; i >= 0; i-- {
		for _, g := range t.annotations[i].globs {
			if g.MatchString(sub) {
				return t.annotations[i]
			}
		}
	}
	return nil
}

// reuseGlob turns a REUSE.toml path glob into an anchored regular expression.
func reuseGlob(glob string) (*regexp.Regexp, error) {
	if strings.HasPrefix(glob, "/") || glob == ".." || strings.HasPrefix(glob, "../") || strings.Contains(glob, "/../") {
		return nil, fmt.Errorf("path %q leaves the REUSE.toml's directory", glob)
	}
	var b strings.Builder
	b.WriteString("^")
	for i := 0; i < len(glob); {
		switch {
		case strings.HasPrefix(glob[i:], `\\`):
			b.WriteString(regexp.QuoteMeta(`\`))
			i += 2
		case strings.HasPrefix(glob[i:], `\*`):
			b.WriteString(regexp.QuoteMeta("*"))
			i += 2
		case strings.HasPrefix(glob[i:], "**"):
			b.WriteString(".*")
			i += 2
		case glob[i] == '*':
			b.WriteString("[^/]*")
			i++
		default:
			r, n := utf8.DecodeRuneInString(glob[i:])
			b.WriteString(regexp.QuoteMeta(string(r)))
			i += n
		}
	}
	b.WriteString("$")
	return regexp.Compile(b.String())
}

// parseReuseTOML reads one REUSE.toml.
func parseReuseTOML(file string, src []byte) (*reuseTOML, error) {
	doc, err := parseTOMLSubset(string(src))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", file, err)
	}
	t := &reuseTOML{file: file}
	if d := path.Dir(file); d != "." {
		t.dir = d + "/"
	}
	v, ok := doc.top["version"]
	if n, isInt := v.(int64); !ok || !isInt || n != 1 {
		return nil, fmt.Errorf("%s: version must be 1", file)
	}
	for i, tbl := range doc.annotations {
		a := &reuseAnnotation{index: i + 1}
		where := fmt.Sprintf("%s: annotation %d", file, i+1)
		paths, err := stringOrList(tbl["path"])
		if err != nil || len(paths) == 0 {
			return nil, fmt.Errorf("%s: path must be a string or a list of strings", where)
		}
		for _, p := range paths {
			g, err := reuseGlob(p)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", where, err)
			}
			a.globs = append(a.globs, g)
		}
		switch p := tbl["precedence"]; p {
		case nil, "closest":
		case "aggregate":
			a.precedence = precAggregate
		case "override":
			a.precedence = precOverride
		default:
			return nil, fmt.Errorf("%s: precedence %v is not closest, aggregate or override", where, p)
		}
		if a.info.copyrights, err = stringOrList(tbl[keyCopyright]); err != nil {
			return nil, fmt.Errorf("%s: %s %w", where, keyCopyright, err)
		}
		if a.info.exprs, err = stringOrList(tbl[keyLicense]); err != nil {
			return nil, fmt.Errorf("%s: %s %w", where, keyLicense, err)
		}
		t.annotations = append(t.annotations, a)
	}
	return t, nil
}

func stringOrList(v any) ([]string, error) {
	switch x := v.(type) {
	case nil:
		return nil, nil
	case string:
		return []string{x}, nil
	case []any:
		out := make([]string, 0, len(x))
		for _, e := range x {
			s, ok := e.(string)
			if !ok {
				return nil, errors.New("must be a string or a list of strings")
			}
			out = append(out, s)
		}
		return out, nil
	}
	return nil, errors.New("must be a string or a list of strings")
}

// ---- the TOML subset -------------------------------------------------------------------

type tomlDoc struct {
	top         map[string]any
	annotations []map[string]any
}

type tomlParser struct {
	s    string
	i    int
	line int
}

func (p *tomlParser) errf(format string, args ...any) error {
	return fmt.Errorf("line %d: %s", p.line, fmt.Sprintf(format, args...))
}

func (p *tomlParser) eof() bool { return p.i >= len(p.s) }

func (p *tomlParser) peek() byte {
	if p.eof() {
		return 0
	}
	return p.s[p.i]
}

// skipSpace skips blanks and, with newlines, line breaks and comments.
func (p *tomlParser) skipSpace(newlines bool) {
	for !p.eof() {
		switch c := p.s[p.i]; {
		case c == ' ' || c == '\t' || c == '\r':
			p.i++
		case c == '#':
			for !p.eof() && p.s[p.i] != '\n' {
				p.i++
			}
		case c == '\n' && newlines:
			p.i++
			p.line++
		default:
			return
		}
	}
}

func parseTOMLSubset(s string) (*tomlDoc, error) {
	p := &tomlParser{s: strings.TrimPrefix(s, "\ufeff"), line: 1}
	doc := &tomlDoc{top: map[string]any{}}
	cur := doc.top
	for {
		p.skipSpace(true)
		if p.eof() {
			return doc, nil
		}
		if strings.HasPrefix(p.s[p.i:], "[[") {
			end := strings.Index(p.s[p.i:], "]]")
			if end < 0 {
				return nil, p.errf("unterminated table header")
			}
			name := strings.TrimSpace(p.s[p.i+2 : p.i+end])
			if name != "annotations" {
				return nil, p.errf("unsupported table [[%s]]", name)
			}
			p.i += end + 2
			cur = map[string]any{}
			doc.annotations = append(doc.annotations, cur)
		} else if p.peek() == '[' {
			return nil, p.errf("unsupported table header (only [[annotations]])")
		} else {
			key, err := p.key()
			if err != nil {
				return nil, err
			}
			p.skipSpace(false)
			if p.peek() != '=' {
				return nil, p.errf("expected = after %q", key)
			}
			p.i++
			p.skipSpace(false)
			v, err := p.value()
			if err != nil {
				return nil, err
			}
			if _, dup := cur[key]; dup {
				return nil, p.errf("duplicate key %q", key)
			}
			cur[key] = v
		}
		p.skipSpace(false)
		if !p.eof() && p.peek() != '\n' {
			return nil, p.errf("unexpected %q at end of line", p.peek())
		}
	}
}

func (p *tomlParser) key() (string, error) {
	if c := p.peek(); c == '"' || c == '\'' {
		v, err := p.value()
		if err != nil {
			return "", err
		}
		return v.(string), nil
	}
	start := p.i
	for !p.eof() {
		c := p.s[p.i]
		if c == '_' || c == '-' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' {
			p.i++
			continue
		}
		break
	}
	if p.i == start {
		return "", p.errf("expected a key")
	}
	if p.peek() == '.' {
		return "", p.errf("dotted keys are not supported")
	}
	return p.s[start:p.i], nil
}

func (p *tomlParser) value() (any, error) {
	rest := p.s[p.i:]
	switch {
	case strings.HasPrefix(rest, `"""`):
		return p.multiline(`"""`, true)
	case strings.HasPrefix(rest, `'''`):
		return p.multiline(`'''`, false)
	case strings.HasPrefix(rest, `"`):
		return p.basicString()
	case strings.HasPrefix(rest, `'`):
		end := strings.IndexAny(rest[1:], "'\n")
		if end < 0 || rest[1+end] != '\'' {
			return nil, p.errf("unterminated literal string")
		}
		p.i += end + 2
		return rest[1 : 1+end], nil
	case strings.HasPrefix(rest, "["):
		p.i++
		var arr []any
		for {
			p.skipSpace(true)
			if p.peek() == ']' {
				p.i++
				return arr, nil
			}
			v, err := p.value()
			if err != nil {
				return nil, err
			}
			arr = append(arr, v)
			p.skipSpace(true)
			switch p.peek() {
			case ',':
				p.i++
			case ']':
			default:
				return nil, p.errf("expected , or ] in array")
			}
		}
	case strings.HasPrefix(rest, "true"):
		p.i += 4
		return true, nil
	case strings.HasPrefix(rest, "false"):
		p.i += 5
		return false, nil
	}
	start := p.i
	for !p.eof() && strings.IndexByte("+-0123456789_", p.s[p.i]) >= 0 {
		p.i++
	}
	if n, err := strconv.ParseInt(strings.ReplaceAll(p.s[start:p.i], "_", ""), 10, 64); err == nil && p.i > start {
		return n, nil
	}
	return nil, p.errf("unsupported value")
}

func (p *tomlParser) basicString() (string, error) {
	p.i++ // opening quote
	var b strings.Builder
	for {
		if p.eof() || p.peek() == '\n' {
			return "", p.errf("unterminated string")
		}
		c := p.s[p.i]
		switch c {
		case '"':
			p.i++
			return b.String(), nil
		case '\\':
			if err := p.escape(&b); err != nil {
				return "", err
			}
		default:
			b.WriteByte(c)
			p.i++
		}
	}
}

func (p *tomlParser) escape(b *strings.Builder) error {
	if p.i+1 >= len(p.s) {
		return p.errf("unterminated escape")
	}
	e := p.s[p.i+1]
	p.i += 2
	switch e {
	case '"', '\\':
		b.WriteByte(e)
	case 'b':
		b.WriteByte('\b')
	case 't':
		b.WriteByte('\t')
	case 'n':
		b.WriteByte('\n')
	case 'f':
		b.WriteByte('\f')
	case 'r':
		b.WriteByte('\r')
	case 'e':
		b.WriteByte(0x1b)
	case 'u', 'U':
		n := 4
		if e == 'U' {
			n = 8
		}
		if p.i+n > len(p.s) {
			return p.errf("short \\%c escape", e)
		}
		r, err := strconv.ParseUint(p.s[p.i:p.i+n], 16, 32)
		if err != nil {
			return p.errf("bad \\%c escape", e)
		}
		b.WriteRune(rune(r))
		p.i += n
	default:
		return p.errf("unknown escape \\%c", e)
	}
	return nil
}

func (p *tomlParser) multiline(delim string, basic bool) (string, error) {
	p.i += 3
	if strings.HasPrefix(p.s[p.i:], "\r\n") {
		p.i += 2
		p.line++
	} else if p.peek() == '\n' {
		p.i++
		p.line++
	}
	var b strings.Builder
	for {
		if p.eof() {
			return "", p.errf("unterminated multi-line string")
		}
		if strings.HasPrefix(p.s[p.i:], delim) {
			p.i += 3
			for n := 0; n < 2 && strings.HasPrefix(p.s[p.i:], delim[:1]); n++ { // up to two quotes may end the content
				b.WriteByte(delim[0])
				p.i++
			}
			return b.String(), nil
		}
		c := p.s[p.i]
		if c == '\n' {
			p.line++
		}
		if basic && c == '\\' {
			// A line-ending backslash trims the newline and the whitespace after it.
			j := p.i + 1
			for j < len(p.s) && (p.s[j] == ' ' || p.s[j] == '\t' || p.s[j] == '\r') {
				j++
			}
			if j < len(p.s) && p.s[j] == '\n' {
				for j < len(p.s) && strings.IndexByte(" \t\r\n", p.s[j]) >= 0 {
					if p.s[j] == '\n' {
						p.line++
					}
					j++
				}
				p.i = j
				continue
			}
			if err := p.escape(&b); err != nil {
				return "", err
			}
			continue
		}
		b.WriteByte(c)
		p.i++
	}
}

// ---- SPDX expressions ------------------------------------------------------------------

// parseSPDX parses an SPDX licence expression (AND, OR, WITH, parentheses; the operators in
// any case) and returns every licence and exception identifier it names.
func parseSPDX(expr string) ([]string, error) {
	var toks []string
	for _, f := range strings.Fields(strings.NewReplacer("(", " ( ", ")", " ) ").Replace(expr)) {
		toks = append(toks, f)
	}
	if len(toks) == 0 {
		return nil, errors.New("empty expression")
	}
	sp := &spdxParser{toks: toks}
	if err := sp.or(); err != nil {
		return nil, err
	}
	if sp.i != len(toks) {
		return nil, fmt.Errorf("unexpected %q", toks[sp.i])
	}
	return sp.ids, nil
}

type spdxParser struct {
	toks []string
	i    int
	ids  []string
}

func (sp *spdxParser) next() string {
	if sp.i < len(sp.toks) {
		return sp.toks[sp.i]
	}
	return ""
}

func isSPDXOp(t string) bool {
	switch strings.ToUpper(t) {
	case "AND", "OR", "WITH":
		return true
	}
	return false
}

func (sp *spdxParser) or() error {
	if err := sp.and(); err != nil {
		return err
	}
	for strings.EqualFold(sp.next(), "OR") {
		sp.i++
		if err := sp.and(); err != nil {
			return err
		}
	}
	return nil
}

func (sp *spdxParser) and() error {
	if err := sp.with(); err != nil {
		return err
	}
	for strings.EqualFold(sp.next(), "AND") {
		sp.i++
		if err := sp.with(); err != nil {
			return err
		}
	}
	return nil
}

func (sp *spdxParser) with() error {
	if err := sp.primary(); err != nil {
		return err
	}
	if strings.EqualFold(sp.next(), "WITH") {
		sp.i++
		t := sp.next()
		if t == "" || t == "(" || t == ")" || isSPDXOp(t) {
			return errors.New("WITH needs an exception identifier")
		}
		sp.ids = append(sp.ids, t)
		sp.i++
	}
	return nil
}

func (sp *spdxParser) primary() error {
	t := sp.next()
	switch {
	case t == "":
		return errors.New("expression ends where a licence was expected")
	case t == "(":
		sp.i++
		if err := sp.or(); err != nil {
			return err
		}
		if sp.next() != ")" {
			return errors.New("unbalanced parenthesis")
		}
		sp.i++
		return nil
	case t == ")" || isSPDXOp(t):
		return fmt.Errorf("unexpected %q where a licence was expected", t)
	}
	sp.ids = append(sp.ids, t)
	sp.i++
	return nil
}

// ---- the SPDX License List -------------------------------------------------------------

//go:embed spdx/list.txt
var spdxListText string

type spdxIDs struct {
	ids        map[string]bool
	deprecated map[string]bool
}

// known reports whether id is on the SPDX License List (licence or exception); a trailing
// "+" (the "or later" operator of SPDX 2) is allowed on a licence.
func (l *spdxIDs) known(id string) bool {
	return l.ids[id] || (strings.HasSuffix(id, "+") && l.ids[strings.TrimSuffix(id, "+")])
}

var spdxList = sync.OnceValue(func() *spdxIDs {
	l := &spdxIDs{ids: map[string]bool{}, deprecated: map[string]bool{}}
	for _, line := range strings.Split(spdxListText, "\n") {
		f := strings.Fields(line)
		if len(f) < 2 || strings.HasPrefix(f[0], "#") {
			continue
		}
		l.ids[f[1]] = true
		if len(f) > 2 && f[2] == "deprecated" {
			l.deprecated[f[1]] = true
		}
	}
	return l
})
