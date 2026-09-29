// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

// ── TOON (Token-Oriented Object Notation) ────────────────────────────────────────────
// Only the tabular-array form is needed here:
//
//	name[N]{f1,f2,...}:
//	  v1,v2,...
//	  (N rows)
//
// Strict: the header must be exact, the row count must equal N, every row must have
// exactly len(fields) cells.

var toonHeader = regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_]*)\[(\d+)\]\{([^}]*)\}:\s*$`)

type toonTable struct {
	Name   string
	Fields []string
	Rows   [][]string
}

func parseTOON(s string) (toonTable, error) {
	var t toonTable
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) == 0 {
		return t, fmt.Errorf("empty")
	}
	m := toonHeader.FindStringSubmatch(strings.TrimSpace(lines[0]))
	if m == nil {
		return t, fmt.Errorf("first line is not a TOON tabular header: %q", lines[0])
	}
	n, _ := strconv.Atoi(m[2])
	t.Name = m[1]
	for _, f := range strings.Split(m[3], ",") {
		t.Fields = append(t.Fields, strings.TrimSpace(f))
	}
	for _, l := range lines[1:] {
		if strings.TrimSpace(l) == "" {
			continue
		}
		if !strings.HasPrefix(l, " ") {
			return t, fmt.Errorf("row not indented: %q", l)
		}
		cells := strings.Split(strings.TrimSpace(l), ",")
		if len(cells) != len(t.Fields) {
			return t, fmt.Errorf("row has %d cells, header declares %d: %q", len(cells), len(t.Fields), l)
		}
		for i := range cells {
			cells[i] = strings.Trim(strings.TrimSpace(cells[i]), `"`)
		}
		t.Rows = append(t.Rows, cells)
	}
	if len(t.Rows) != n {
		return t, fmt.Errorf("header declares %d rows, got %d", n, len(t.Rows))
	}
	return t, nil
}

// stripFences removes one surrounding markdown code fence. The bool reports whether
// there was one, so a check can say "parsed, but only after un-fencing".
func stripFences(s string) (string, bool) {
	t := strings.TrimSpace(s)
	if !strings.HasPrefix(t, "```") {
		return t, false
	}
	if i := strings.Index(t, "\n"); i >= 0 {
		t = t[i+1:]
	}
	t = strings.TrimSuffix(strings.TrimSpace(t), "```")
	return strings.TrimSpace(t), true
}

// thinkText returns the text inside <think>…</think> (or after an unclosed <think>).
var thinkRe = regexp.MustCompile(`(?s)<think>(.*?)(</think>|$)`)

func thinkText(s string) (found bool, inner string) {
	m := thinkRe.FindStringSubmatch(s)
	if m == nil {
		return strings.Contains(s, "</think>"), ""
	}
	return true, strings.TrimSpace(m[1])
}

// ── Language ID (function-word vote) ─────────────────────────────────────────────────

var langWords = map[string][]string{
	"en": {"the", "and", "is", "of", "to", "in", "that", "it", "you", "for", "are", "this", "with", "because", "food", "cold", "chain"},
	"es": {"el", "la", "los", "las", "de", "que", "y", "es", "en", "por", "para", "un", "una", "del", "se", "con", "más", "porque", "alimentos", "cadena", "frío", "garantiza", "evita"},
	"fr": {"le", "la", "les", "de", "des", "et", "est", "en", "que", "un", "une", "pour", "du", "il", "ce", "dans", "pas", "qui", "parce", "aliments", "chaîne", "froid", "sont", "au"},
	"pt": {"o", "a", "os", "as", "de", "que", "e", "é", "em", "um", "uma", "para", "do", "da", "não", "com", "se", "mais", "porque", "alimentos", "cadeia", "frio", "garante", "evita", "são", "no", "na"},
}

func words(s string) []string {
	return strings.FieldsFunc(strings.ToLower(s), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
}

// detectLang returns the best-scoring language and all scores. A word shared by k
// languages contributes 1/k to each, so exclusive words decide close calls.
func detectLang(s string) (string, map[string]float64) {
	owners := map[string][]string{}
	for l, ws := range langWords {
		for _, w := range ws {
			owners[w] = append(owners[w], l)
		}
	}
	score := map[string]float64{}
	for _, w := range words(s) {
		for _, l := range owners[w] {
			score[l] += 1 / float64(len(owners[w]))
		}
	}
	best, bs := "", -1.0
	for _, l := range []string{"en", "es", "fr", "pt"} {
		if score[l] > bs {
			best, bs = l, score[l]
		}
	}
	return best, score
}

// ── WER ──────────────────────────────────────────────────────────────────────────────

func wer(ref, hyp string) (float64, int, int) {
	r, h := words(ref), words(hyp)
	d := make([][]int, len(r)+1)
	for i := range d {
		d[i] = make([]int, len(h)+1)
		d[i][0] = i
	}
	for j := 0; j <= len(h); j++ {
		d[0][j] = j
	}
	for i := 1; i <= len(r); i++ {
		for j := 1; j <= len(h); j++ {
			c := 1
			if r[i-1] == h[j-1] {
				c = 0
			}
			d[i][j] = min(d[i-1][j]+1, d[i][j-1]+1, d[i-1][j-1]+c)
		}
	}
	if len(r) == 0 {
		return 0, 0, 0
	}
	return float64(d[len(r)][len(h)]) / float64(len(r)), d[len(r)][len(h)], len(r)
}
