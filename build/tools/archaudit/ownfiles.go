// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

package main

import (
	"bufio"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ownFiles lists the regular files under root that the image's authors put there: files no
// pacman package owns, and package-owned files whose content differs from what the package
// shipped (the sha256digest in the package's mtree). Those are the files a secret could have
// been baked into; unmodified upstream package files are not ours to scan.
func ownFiles(root string) ([]string, error) {
	dbDir := filepath.Join(root, "var/lib/pacman/local")
	shipped := map[string]string{} // "path" (no leading /) -> sha256 as shipped
	mtrees, err := filepath.Glob(filepath.Join(dbDir, "*", "mtree"))
	if err != nil {
		return nil, err
	}
	if len(mtrees) == 0 {
		return nil, fmt.Errorf("no package mtree under %s", dbDir)
	}
	for _, m := range mtrees {
		if err := readMtree(m, shipped); err != nil {
			return nil, fmt.Errorf("%s: %w", m, err)
		}
	}
	var out []string
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // unreadable entries are skipped, not fatal
		}
		rel, _ := filepath.Rel(root, p)
		if d.IsDir() {
			// The package database itself and pseudo filesystems are not image content.
			switch rel {
			case "var/lib/pacman", "proc", "sys", "dev", "run", "tmp":
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		want, owned := shipped[rel]
		if owned {
			got, err := sha256File(p)
			if err != nil || got == want {
				return nil
			}
		}
		out = append(out, rel)
		return nil
	})
	sort.Strings(out)
	return out, err
}

// readMtree adds each regular file's sha256digest from a gzipped libarchive mtree.
func readMtree(path string, into map[string]string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer zr.Close()
	defType := ""
	sc := bufio.NewScanner(zr)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if fields[0] == "/set" {
			for _, kv := range fields[1:] {
				if v, ok := strings.CutPrefix(kv, "type="); ok {
					defType = v
				}
			}
			continue
		}
		if !strings.HasPrefix(fields[0], "./") {
			continue
		}
		name := mtreeUnescape(strings.TrimPrefix(fields[0], "./"))
		typ, sum := defType, ""
		for _, kv := range fields[1:] {
			if v, ok := strings.CutPrefix(kv, "type="); ok {
				typ = v
			}
			if v, ok := strings.CutPrefix(kv, "sha256digest="); ok {
				sum = v
			}
		}
		if typ == "file" && sum != "" && !strings.HasPrefix(name, ".") {
			into[name] = sum
		}
	}
	return sc.Err()
}

// mtreeUnescape decodes mtree's \ooo octal escapes (spaces and other characters in names).
func mtreeUnescape(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+4 <= len(s) && isOctal(s[i+1]) && isOctal(s[i+2]) && isOctal(s[i+3]) {
			b.WriteByte((s[i+1]-'0')<<6 | (s[i+2]-'0')<<3 | (s[i+3] - '0'))
			i += 3
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

func isOctal(c byte) bool { return c >= '0' && c <= '7' }

func sha256File(p string) (string, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
