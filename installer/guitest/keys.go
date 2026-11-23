// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

package main

import (
	"fmt"
	"strings"
)

// Key names a test author writes ("super+enter", "ctrl+alt+del") and the QEMU qcodes they become
// (QMP send-key / input-send-event "qcode" values, US layout).
var modAlias = map[string]string{
	"ctrl": "ctrl", "control": "ctrl", "alt": "alt", "shift": "shift",
	"super": "meta_l", "meta": "meta_l", "win": "meta_l",
}

var nameAlias = map[string]string{
	"enter": "ret", "return": "ret", "ret": "ret", "esc": "esc", "escape": "esc", "tab": "tab",
	"space": "spc", "spc": "spc", "del": "delete", "delete": "delete", "bs": "backspace",
	"backspace": "backspace", "left": "left", "right": "right", "up": "up", "down": "down",
	"home": "home", "end": "end", "pgup": "pgup", "pgdn": "pgdn", "print": "print",
	"minus": "minus", "equal": "equal", "dot": "dot", "comma": "comma", "slash": "slash",
}

// ParseCombo turns "ctrl+alt+del" into the qcodes ["ctrl" "alt" "delete"], in press order.
func ParseCombo(s string) ([]string, error) {
	if strings.TrimSpace(s) == "" {
		return nil, fmt.Errorf("empty key combination")
	}
	var out []string
	for _, p := range strings.Split(strings.ToLower(strings.TrimSpace(s)), "+") {
		switch {
		case modAlias[p] != "":
			out = append(out, modAlias[p])
		case nameAlias[p] != "":
			out = append(out, nameAlias[p])
		case len(p) == 1 && (p[0] >= 'a' && p[0] <= 'z' || p[0] >= '0' && p[0] <= '9'):
			out = append(out, p)
		case len(p) >= 2 && len(p) <= 3 && p[0] == 'f' && isDigits(p[1:]) && atoi(p[1:]) >= 1 && atoi(p[1:]) <= 12:
			out = append(out, p)
		default:
			return nil, fmt.Errorf("unknown key %q in %q", p, s)
		}
	}
	return out, nil
}

func isDigits(s string) bool {
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return s != ""
}

func atoi(s string) int {
	n := 0
	for _, c := range s {
		n = n*10 + int(c-'0')
	}
	return n
}

// charKeys is the key chord that types one character on a US keyboard.
func charKeys(c rune) ([]string, bool) {
	switch {
	case c >= 'a' && c <= 'z', c >= '0' && c <= '9':
		return []string{string(c)}, true
	case c >= 'A' && c <= 'Z':
		return []string{"shift", strings.ToLower(string(c))}, true
	}
	plain := map[rune]string{
		' ': "spc", '\n': "ret", '\t': "tab", '-': "minus", '=': "equal", '[': "bracket_left",
		']': "bracket_right", '\\': "backslash", ';': "semicolon", '\'': "apostrophe",
		'`': "grave_accent", ',': "comma", '.': "dot", '/': "slash",
	}
	if k, ok := plain[c]; ok {
		return []string{k}, true
	}
	shifted := map[rune]string{
		'!': "1", '@': "2", '#': "3", '$': "4", '%': "5", '^': "6", '&': "7", '*': "8", '(': "9",
		')': "0", '_': "minus", '+': "equal", '{': "bracket_left", '}': "bracket_right",
		'|': "backslash", ':': "semicolon", '"': "apostrophe", '~': "grave_accent", '<': "comma",
		'>': "dot", '?': "slash",
	}
	if k, ok := shifted[c]; ok {
		return []string{"shift", k}, true
	}
	return nil, false
}

// TypeChords is the chord list that types text on the guest keyboard.
func TypeChords(text string) ([][]string, error) {
	var out [][]string
	for _, c := range text {
		k, ok := charKeys(c)
		if !ok {
			return nil, fmt.Errorf("cannot type %q on a US keyboard", c)
		}
		out = append(out, k)
	}
	return out, nil
}
