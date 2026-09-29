// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

package main

import "strings"

// vercmp compares two pacman package versions ([epoch:]version[-release]) exactly as
// libalpm's alpm_pkg_vercmp does: <0 if a is older, 0 if equal, >0 if newer.
func vercmp(a, b string) int {
	if a == b {
		return 0
	}
	ea, va, ra := parseEVR(a)
	eb, vb, rb := parseEVR(b)
	if c := rpmvercmp(ea, eb); c != 0 {
		return c
	}
	if c := rpmvercmp(va, vb); c != 0 {
		return c
	}
	if ra != "" && rb != "" {
		return rpmvercmp(ra, rb)
	}
	return 0
}

// parseEVR splits [epoch:]version[-release]; a missing epoch is "0".
func parseEVR(s string) (epoch, version, release string) {
	epoch = "0"
	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	if i < len(s) && s[i] == ':' {
		if i > 0 {
			epoch = s[:i]
		}
		s = s[i+1:]
	}
	if j := strings.LastIndexByte(s, '-'); j >= 0 {
		return epoch, s[:j], s[j+1:]
	}
	return epoch, s, ""
}

func isAlnum(c byte) bool { return isDigit(c) || isAlpha(c) }
func isDigit(c byte) bool { return c >= '0' && c <= '9' }
func isAlpha(c byte) bool { return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') }

// rpmvercmp is libalpm's rpmvercmp: alternating numeric and alphabetic segments,
// numeric segments compared as numbers and always newer than alphabetic ones.
func rpmvercmp(a, b string) int {
	if a == b {
		return 0
	}
	one, two := 0, 0
	for one < len(a) && two < len(b) {
		for one < len(a) && !isAlnum(a[one]) {
			one++
		}
		for two < len(b) && !isAlnum(b[two]) {
			two++
		}
		if one >= len(a) || two >= len(b) {
			break
		}
		// The separator runs before this segment decide when they differ in length.
		if seps(a, one) != seps(b, two) {
			if seps(a, one) < seps(b, two) {
				return -1
			}
			return 1
		}
		p, q := one, two
		numeric := isDigit(a[p])
		if numeric {
			for p < len(a) && isDigit(a[p]) {
				p++
			}
			for q < len(b) && isDigit(b[q]) {
				q++
			}
		} else {
			for p < len(a) && isAlpha(a[p]) {
				p++
			}
			for q < len(b) && isAlpha(b[q]) {
				q++
			}
		}
		sa, sb := a[one:p], b[two:q]
		if sb == "" {
			// Different segment types: numeric is newer.
			if numeric {
				return 1
			}
			return -1
		}
		if numeric {
			sa = strings.TrimLeft(sa, "0")
			sb = strings.TrimLeft(sb, "0")
			if len(sa) != len(sb) {
				if len(sa) > len(sb) {
					return 1
				}
				return -1
			}
		}
		if c := strings.Compare(sa, sb); c != 0 {
			return c
		}
		one, two = p, q
	}
	ra, rb := one >= len(a), two >= len(b)
	switch {
	case ra && rb:
		return 0
	// The remaining part decides: "1.0" < "1.0.1", but an alphabetic remainder is older
	// ("1.0alpha" < "1.0").
	case ra:
		if isAlpha(b[two]) {
			return 1
		}
		return -1
	case rb:
		if isAlpha(a[one]) {
			return -1
		}
		return 1
	}
	return 0
}

// seps counts the separator characters immediately before position i.
func seps(s string, i int) int {
	n := 0
	for j := i - 1; j >= 0 && !isAlnum(s[j]); j-- {
		n++
	}
	return n
}
