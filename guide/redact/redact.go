// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

// Package redact is the last filter every byte passes before it leaves the machine in an
// issue comment. It is deliberately blunt: it would rather mangle a harmless string than
// let one identifier through.
package redact

import (
	"regexp"
	"sort"
	"strings"
)

const mark = "[redacted]"

var patterns = []*regexp.Regexp{
	// Credentials first, so a token is never half-matched by a later rule.
	regexp.MustCompile(`\b(gh[pousr]_[A-Za-z0-9]{20,}|github_pat_[A-Za-z0-9_]{20,})\b`),
	regexp.MustCompile(`-----BEGIN [A-Z ]*(PRIVATE KEY|CERTIFICATE)-----[\s\S]*?-----END [A-Z ]*(PRIVATE KEY|CERTIFICATE)-----`),
	regexp.MustCompile(`\b(ssh-(rsa|ed25519|dss)|ecdsa-sha2-nistp\d+|sk-ssh-ed25519@openssh\.com)\s+[A-Za-z0-9+/=]{16,}`),
	regexp.MustCompile(`\bed25519:[A-Za-z0-9+/=]{40,}`),
	// Long hex / base64 runs: keys, recovery keys, hashes of secrets, WWNs.
	regexp.MustCompile(`\b(0x)?[0-9A-Fa-f]{24,}\b`),
	regexp.MustCompile(`\b[A-Za-z0-9+/]{40,}={0,2}`),
	// MAC / EUI-64.
	regexp.MustCompile(`\b([0-9A-Fa-f]{2}[:-]){5}[0-9A-Fa-f]{2}\b`),
	regexp.MustCompile(`\b([0-9A-Fa-f]{2}[:-]){7}[0-9A-Fa-f]{2}\b`),
	// IPv4 (with optional prefix).
	regexp.MustCompile(`\b(25[0-5]|2[0-4]\d|1?\d?\d)(\.(25[0-5]|2[0-4]\d|1?\d?\d)){3}(/\d{1,2})?\b`),
	// IPv6: anything with two or more colon groups of hex, incl. '::' forms.
	regexp.MustCompile(`(?i)\b[0-9a-f]{0,4}(:[0-9a-f]{0,4}){2,7}(%[a-z0-9]+)?(/\d{1,3})?`),
	// "serial: X", "serial=X", "SN X", and disk by-id paths (they embed the serial).
	regexp.MustCompile(`(?i)\b(serial|s/n|sn|wwn|uuid|confirm_id)\b\s*[:=]?\s*\S+`),
	regexp.MustCompile(`/dev/disk/by-(id|uuid|partuuid|label|path)/\S+`),
	// UUIDs.
	regexp.MustCompile(`(?i)\b[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\b`),
}

// Redactor also knows machine-specific literals (hostname, and anything the caller adds).
type Redactor struct {
	literals []string
}

// New returns a Redactor that additionally scrubs each non-trivial literal.
func New(literals ...string) *Redactor {
	r := &Redactor{}
	for _, l := range literals {
		r.Add(l)
	}
	return r
}

// Add registers another literal to scrub (ignored when shorter than 3 characters, which
// would otherwise shred ordinary words).
func (r *Redactor) Add(l string) {
	l = strings.TrimSpace(l)
	if len(l) < 3 {
		return
	}
	r.literals = append(r.literals, l)
	sort.Slice(r.literals, func(i, j int) bool { return len(r.literals[i]) > len(r.literals[j]) })
}

// String scrubs s.
func (r *Redactor) String(s string) string {
	for _, l := range r.literals {
		s = replaceFold(s, l)
	}
	for _, p := range patterns {
		s = p.ReplaceAllStringFunc(s, func(m string) string {
			// Keep version-like "1:2:3"? No: a false positive costs a word, a miss leaks.
			if onlyColons(m) {
				return m
			}
			return mark
		})
	}
	return s
}

func onlyColons(m string) bool { return strings.Trim(m, ":") == "" }

func replaceFold(s, lit string) string {
	ls, ll := strings.ToLower(s), strings.ToLower(lit)
	var b strings.Builder
	for {
		i := strings.Index(ls, ll)
		if i < 0 {
			b.WriteString(s)
			return b.String()
		}
		b.WriteString(s[:i])
		b.WriteString(mark)
		s, ls = s[i+len(lit):], ls[i+len(lit):]
	}
}
