// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

// Package shacrypt computes SHA-512 crypt(3) password hashes ("$6$", Ulrich Drepper's
// "Unix crypt using SHA-256 and SHA-512", as glibc implements it), standard library only.
//
// The graphical installer hashes the admin password in its own memory and hands the installer
// step only the hash (RUNINK_ADMIN_PASSWORD_HASH, which chpasswd -e stores in /etc/shadow).
// The password itself is never written anywhere, passed to another process or logged.
package shacrypt

import (
	"crypto/rand"
	"crypto/sha512"
	"errors"
	"strconv"
	"strings"
)

const (
	defaultRounds = 5000
	minRounds     = 1000
	maxRounds     = 999999999
	saltChars     = "./0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
)

// Hash returns a "$6$<salt>$<hash>" crypt string for password, with a random 16-character
// salt and the given number of rounds (0 means the default, 5000, which crypt(3) leaves out
// of the string).
func Hash(password []byte, rounds int) (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	salt := make([]byte, 16)
	for i, b := range raw {
		salt[i] = saltChars[int(b)%len(saltChars)]
	}
	return crypt(password, string(salt), rounds)
}

// Crypt hashes password with the salt specification of an existing "$6$..." string
// (the salt and, when present, "rounds=N$"), so Crypt(pw, h) == h verifies a password.
func Crypt(password []byte, setting string) (string, error) {
	if !strings.HasPrefix(setting, "$6$") {
		return "", errors.New("shacrypt: not a $6$ setting")
	}
	rest := setting[3:]
	rounds := 0
	if strings.HasPrefix(rest, "rounds=") {
		end := strings.IndexByte(rest, '$')
		if end < 0 {
			return "", errors.New("shacrypt: malformed rounds")
		}
		n, err := strconv.Atoi(rest[len("rounds="):end])
		if err != nil {
			return "", errors.New("shacrypt: malformed rounds")
		}
		rounds = n
		if rounds == 0 {
			rounds = minRounds
		}
		rest = rest[end+1:]
	}
	salt := rest
	if i := strings.IndexByte(salt, '$'); i >= 0 {
		salt = salt[:i]
	}
	return crypt(password, salt, rounds)
}

func crypt(password []byte, salt string, rounds int) (string, error) {
	if len(salt) > 16 {
		salt = salt[:16]
	}
	custom := rounds != 0
	if rounds == 0 {
		rounds = defaultRounds
	}
	if rounds < minRounds {
		rounds = minRounds
	}
	if rounds > maxRounds {
		rounds = maxRounds
	}
	s := []byte(salt)

	b := sha512.New()
	b.Write(password)
	b.Write(s)
	b.Write(password)
	sumB := b.Sum(nil)

	a := sha512.New()
	a.Write(password)
	a.Write(s)
	for i := len(password); i > 0; i -= 64 {
		if i > 64 {
			a.Write(sumB)
		} else {
			a.Write(sumB[:i])
		}
	}
	for i := len(password); i > 0; i >>= 1 {
		if i&1 != 0 {
			a.Write(sumB)
		} else {
			a.Write(password)
		}
	}
	sumA := a.Sum(nil)

	dp := sha512.New()
	for i := 0; i < len(password); i++ {
		dp.Write(password)
	}
	sumDP := dp.Sum(nil)
	p := make([]byte, 0, len(password))
	for i := len(password); i > 0; i -= 64 {
		if i > 64 {
			p = append(p, sumDP...)
		} else {
			p = append(p, sumDP[:i]...)
		}
	}

	ds := sha512.New()
	for i := 0; i < 16+int(sumA[0]); i++ {
		ds.Write(s)
	}
	sumDS := ds.Sum(nil)
	sp := make([]byte, 0, len(s))
	for i := len(s); i > 0; i -= 64 {
		if i > 64 {
			sp = append(sp, sumDS...)
		} else {
			sp = append(sp, sumDS[:i]...)
		}
	}

	c := sumA
	for i := 0; i < rounds; i++ {
		h := sha512.New()
		if i&1 != 0 {
			h.Write(p)
		} else {
			h.Write(c)
		}
		if i%3 != 0 {
			h.Write(sp)
		}
		if i%7 != 0 {
			h.Write(p)
		}
		if i&1 != 0 {
			h.Write(c)
		} else {
			h.Write(p)
		}
		c = h.Sum(nil)
	}
	for i := range p {
		p[i] = 0
	}

	var out strings.Builder
	out.WriteString("$6$")
	if custom {
		out.WriteString("rounds=" + strconv.Itoa(rounds) + "$")
	}
	out.WriteString(salt)
	out.WriteByte('$')
	order := [][3]int{
		{0, 21, 42}, {22, 43, 1}, {44, 2, 23}, {3, 24, 45}, {25, 46, 4}, {47, 5, 26}, {6, 27, 48},
		{28, 49, 7}, {50, 8, 29}, {9, 30, 51}, {31, 52, 10}, {53, 11, 32}, {12, 33, 54},
		{34, 55, 13}, {56, 14, 35}, {15, 36, 57}, {37, 58, 16}, {59, 17, 38}, {18, 39, 60},
		{40, 61, 19}, {62, 20, 41},
	}
	for _, o := range order {
		b64(&out, uint(c[o[0]])<<16|uint(c[o[1]])<<8|uint(c[o[2]]), 4)
	}
	b64(&out, uint(c[63]), 2)
	return out.String(), nil
}

func b64(out *strings.Builder, v uint, n int) {
	for ; n > 0; n-- {
		out.WriteByte(saltChars[v&0x3f])
		v >>= 6
	}
}
