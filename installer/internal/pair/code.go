// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

// Package pair implements opt-in LAN install pairing for the Runink River live installer
// (docs/INSTALL.md, "LAN installs"): a target machine whose local operator opted in shows a
// one-time pairing code and announces itself on the local link; an operator machine that
// learns the code from the target's screen proves it knows the code, and the target's owner
// then allows or refuses the pairing on the target's own console.
//
// This package holds the pieces both sides share and that tests pin: the code format, the
// HMAC exchange, the session state (expiry, lockout, single use), the link-local beacon, the
// SSH key handling and the restricted RPC command grammar. Standard library only.
package pair

import (
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"io"
	"strings"
)

// CodeAlphabet is Crockford's base32: no I, L, O or U, so a code read off a screen is not
// misread. Normalize maps I and L to 1 and O to 0, as Crockford specifies.
const CodeAlphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// CodeLen is the length of a canonical code: CodeDataLen random characters and one check
// character.
const (
	CodeLen     = 8
	CodeDataLen = CodeLen - 1
)

// Code errors. A malformed code is caught on the operator's machine, before it is sent, so a
// typo never costs one of the target's five attempts.
var (
	ErrCodeLength   = errors.New("a pairing code has 8 characters (XXXX-XXXX)")
	ErrCodeChar     = errors.New("a pairing code uses only 0-9 and A-Z without I, L, O and U")
	ErrCodeChecksum = errors.New("the pairing code's check character does not match (mistyped?)")
)

// NewCode returns a fresh canonical code (35 random bits and a check character) read from r
// (crypto/rand.Reader when r is nil).
func NewCode(r io.Reader) (string, error) {
	if r == nil {
		r = rand.Reader
	}
	var b [CodeDataLen]byte
	if _, err := io.ReadFull(r, b[:]); err != nil {
		return "", err
	}
	out := make([]byte, 0, CodeLen)
	for _, c := range b {
		out = append(out, CodeAlphabet[c&31]) // 256 is a multiple of 32: uniform
	}
	out = append(out, checkChar(string(out)))
	return string(out), nil
}

func checkChar(data string) byte {
	h := sha256.Sum256([]byte("river-pair/v1 check\n" + data))
	return CodeAlphabet[h[0]&31]
}

// Normalize turns what an operator typed into the canonical code: case, spaces and dashes
// do not matter, I and L read as 1 and O as 0. It verifies the check character.
func Normalize(in string) (string, error) {
	var b strings.Builder
	for _, r := range strings.ToUpper(in) {
		switch r {
		case ' ', '-', '\t':
			continue
		case 'I', 'L':
			r = '1'
		case 'O':
			r = '0'
		}
		if r > 127 || strings.IndexByte(CodeAlphabet, byte(r)) < 0 {
			return "", ErrCodeChar
		}
		b.WriteRune(r)
	}
	c := b.String()
	if len(c) != CodeLen {
		return "", ErrCodeLength
	}
	if checkChar(c[:CodeDataLen]) != c[CodeDataLen] {
		return "", ErrCodeChecksum
	}
	return c, nil
}

// FormatCode renders a canonical code for a screen: XXXX-XXXX.
func FormatCode(c string) string {
	if len(c) != CodeLen {
		return c
	}
	return c[:4] + "-" + c[4:]
}

// NewShortID returns 6 lowercase base32 characters naming one pairing session
// ("river-install-<id>").
func NewShortID(r io.Reader) (string, error) {
	if r == nil {
		r = rand.Reader
	}
	var b [6]byte
	if _, err := io.ReadFull(r, b[:]); err != nil {
		return "", err
	}
	out := make([]byte, len(b))
	for i, c := range b {
		out[i] = strings.ToLower(CodeAlphabet)[c&31]
	}
	return string(out), nil
}
