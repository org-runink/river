// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

// Package retrieve is a small BM25 keyword index over guide sections. It is the whole
// of river-guide's retrieval: it needs no embedding model, so it works the same on a
// machine too small to run the guide model (degraded mode) as on one that can.
package retrieve

import (
	"math"
	"sort"
	"strings"
	"unicode"

	"github.com/org-runink/river/guide/bundle"
)

// Hit is one retrieved section.
type Hit struct {
	Section *bundle.Section
	Score   float64
}

type doc struct {
	sec *bundle.Section
	tf  map[string]float64
	len float64
}

// Index is immutable once built.
type Index struct {
	docs  []doc
	df    map[string]int
	avgdl float64
}

const titleBoost = 3

// New indexes every section of the given bundles.
func New(bundles ...*bundle.Bundle) *Index {
	ix := &Index{df: map[string]int{}}
	var total float64
	for _, b := range bundles {
		for _, s := range b.Sections {
			d := doc{sec: s, tf: map[string]float64{}}
			for _, t := range Tokens(s.Title) {
				d.tf[t] += titleBoost
				d.len += titleBoost
			}
			for _, t := range Tokens(s.Body) {
				d.tf[t]++
				d.len++
			}
			if s.Step != nil {
				d.tf[s.Step.ID] += titleBoost
				for _, t := range Tokens(s.Step.Run) {
					d.tf[t] += titleBoost
					d.len += titleBoost
				}
			}
			for t := range d.tf {
				ix.df[t]++
			}
			total += d.len
			ix.docs = append(ix.docs, d)
		}
	}
	if len(ix.docs) > 0 {
		ix.avgdl = total / float64(len(ix.docs))
	}
	return ix
}

// Search returns up to k sections scoring above zero, best first. filter, when non-nil,
// drops sections before scoring (used to keep private bundles out of remote answers).
func (ix *Index) Search(q string, k int, filter func(*bundle.Section) bool) []Hit {
	const k1, b = 1.2, 0.5
	terms := Tokens(q)
	n := float64(len(ix.docs))
	var hits []Hit
	for _, d := range ix.docs {
		if filter != nil && !filter(d.sec) {
			continue
		}
		var score float64
		seen := map[string]bool{}
		for _, t := range terms {
			if seen[t] {
				continue
			}
			seen[t] = true
			f := d.tf[t]
			if f == 0 {
				continue
			}
			df := float64(ix.df[t])
			idf := math.Log(1 + (n-df+0.5)/(df+0.5))
			score += idf * f * (k1 + 1) / (f + k1*(1-b+b*d.len/ix.avgdl))
		}
		if score > 0 {
			hits = append(hits, Hit{Section: d.sec, Score: score})
		}
	}
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].Score > hits[j].Score })
	if len(hits) > k {
		hits = hits[:k]
	}
	return hits
}

var stop = map[string]bool{}

func init() {
	for _, w := range strings.Fields(`a an and are as at be by can do does for from how i if in
	into is it its me my of on or should so that the then this to what when where which who
	why will with you your we our us there their them it's i'm`) {
		stop[w] = true
	}
}

// Tokens lowercases, splits on non-alphanumerics (keeping '-' inside words so step ids
// and flags survive), drops stop words, and strips a plural 's'.
func Tokens(s string) []string {
	var out []string
	for _, f := range strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !(unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_')
	}) {
		f = strings.Trim(f, "-_")
		if len(f) < 2 || stop[f] {
			continue
		}
		out = append(out, stem(f))
		if strings.Contains(f, "-") {
			for _, p := range strings.Split(f, "-") {
				if len(p) >= 3 && !stop[p] {
					out = append(out, stem(p))
				}
			}
		}
	}
	return out
}

// stem is a deliberately light suffix stripper, so "installer", "installing",
// "installed" and "installs" all meet "install". Words with digits or dashes (step ids,
// flags, versions) are left alone.
func stem(w string) string {
	if strings.ContainsAny(w, "0123456789-_") {
		return w
	}
	for _, suf := range []string{"ing", "ers", "er", "ed", "es", "s"} {
		if len(w) > len(suf)+3 && strings.HasSuffix(w, suf) && !strings.HasSuffix(w, "ss") {
			return w[:len(w)-len(suf)]
		}
	}
	return w
}
