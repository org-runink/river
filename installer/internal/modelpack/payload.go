// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

package modelpack

// Generic downstream payloads (docs/PAYLOADS.md).
//
// A downstream platform may carry more than models on a medium: container image archives,
// source bundles, anything that is a set of files. Each set is one payload in
// PayloadFormat, described by a generic LOCK instead of models.lock:
//
//	river-payload-lock 1
//	kind <kind>
//	group <group>
//	file <size> <sha256> <dest>
//	...
//
// kind and group are lower-case words ([a-z0-9][a-z0-9-]*); River attaches no meaning to
// them beyond the medium layout /river-<kind>/<group>/. Every other rule (clean, unique,
// relative dest; lower-case hex sha256; at least one file) is the models.lock rule.

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// LockHeader is the first line of a generic payload LOCK.
const LockHeader = "river-payload-lock 1"

// LockName is the file name of a payload's LOCK, next to its MANIFEST.
const LockName = "LOCK"

// LockInfo says which format a lock belongs to and, for a generic LOCK, its kind and group.
type LockInfo struct {
	Format      Format
	Kind, Group string
}

// ParseAnyLock parses either lock format: a generic payload LOCK (first line LockHeader)
// or a models.lock.
func ParseAnyLock(lock []byte) ([]Row, LockInfo, error) {
	first := ""
	sc := bufio.NewScanner(bytes.NewReader(lock))
	for sc.Scan() {
		if l := strings.TrimSpace(sc.Text()); l != "" && !strings.HasPrefix(l, "#") {
			first = l
			break
		}
	}
	if first != LockHeader {
		rows, err := ParseLock(bytes.NewReader(lock))
		return rows, LockInfo{Format: ModelFormat}, err
	}
	rows, kind, group, err := parsePayloadLock(lock)
	return rows, LockInfo{Format: PayloadFormat, Kind: kind, Group: group}, err
}

// IsWord reports whether s is a valid kind or group name.
func IsWord(s string) bool {
	if s == "" || len(s) > 64 {
		return false
	}
	for i, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' && i > 0) {
			return false
		}
	}
	return true
}

func parsePayloadLock(lock []byte) (rows []Row, kind, group string, err error) {
	seen := map[string]int{}
	sc := bufio.NewScanner(bytes.NewReader(lock))
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	ln, header := 0, false
	for sc.Scan() {
		ln++
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if !header {
			header = true // ParseAnyLock checked it
			continue
		}
		f := strings.Fields(line)
		bad := func(why string) error { return fmt.Errorf("LOCK:%d: %s", ln, why) }
		switch f[0] {
		case "kind", "group":
			if len(f) != 2 || !IsWord(f[1]) {
				return nil, "", "", bad("malformed " + f[0])
			}
			p := &kind
			if f[0] == "group" {
				p = &group
			}
			if *p != "" {
				return nil, "", "", bad("repeated " + f[0])
			}
			*p = f[1]
		case "file":
			if len(f) != 4 {
				return nil, "", "", bad("expected: file <size> <sha256> <dest>")
			}
			size, perr := strconv.ParseInt(f[1], 10, 64)
			if perr != nil || size < 0 {
				return nil, "", "", bad("bad size")
			}
			if !isHex(f[2], 64) {
				return nil, "", "", bad("sha256 must be 64 lowercase hex characters")
			}
			if cerr := checkRel(f[3]); cerr != nil {
				return nil, "", "", bad(fmt.Sprintf("dest %q: %v", f[3], cerr))
			}
			if prev, dup := seen[f[3]]; dup {
				return nil, "", "", bad(fmt.Sprintf("dest %q already used on line %d", f[3], prev))
			}
			seen[f[3]] = ln
			rows = append(rows, Row{Role: kind, File: f[3], Size: size, SHA256: f[2], Dest: f[3]})
		default:
			return nil, "", "", bad(fmt.Sprintf("unknown line %q", f[0]))
		}
	}
	if err := sc.Err(); err != nil {
		return nil, "", "", err
	}
	if kind == "" || group == "" {
		return nil, "", "", errors.New("LOCK: needs a kind and a group line")
	}
	if len(rows) == 0 {
		return nil, "", "", errors.New("LOCK has no file lines")
	}
	return rows, kind, group, nil
}

// StagedPayload is one payload copied by Stage; it is one INDEX line.
type StagedPayload struct {
	Kind, Group, LockSHA256 string
	Files                   int
	Bytes, Ciphertext       int64
}

// IndexHeader is the first line of the INDEX that Stage writes.
const IndexHeader = "river-payload-index 1"

// IndexName is the file Stage writes at the root of its destination.
const IndexName = "INDEX"

// Stage copies every downstream payload found under src (a directory holding river-<kind>/
// directories, e.g. the root of an install medium) into dest/<kind>/<group>/, STILL
// ENCRYPTED, and writes dest/INDEX. The model payload (river-models) is not a downstream
// payload and is skipped: the installer unpacks it.
//
// Nothing is trusted: every payload directory must hold exactly MANIFEST, LOCK and the parts
// the MANIFEST names; the LOCK must be a generic LOCK whose kind and group match its place and
// whose sha256 is the MANIFEST's lock-sha256; and every part is hashed while it is copied and
// must match the MANIFEST. Directories are created 0700 and files 0600 (the caller owns them;
// the installer runs as root). dest must be absent or empty. On error dest is left partially
// filled; the caller discards it (the installer destroys the dataset).
func Stage(src, dest string, log io.Writer) ([]StagedPayload, error) {
	if log == nil {
		log = io.Discard
	}
	if ents, err := os.ReadDir(dest); err == nil && len(ents) > 0 {
		return nil, fmt.Errorf("stage: %s is not empty", dest)
	}
	kinds, err := os.ReadDir(src)
	if err != nil {
		return nil, err
	}
	var out []StagedPayload
	for _, k := range kinds {
		name := k.Name()
		if !k.IsDir() || !strings.HasPrefix(name, "river-") || name == "river-models" {
			continue
		}
		kind := strings.TrimPrefix(name, "river-")
		if !IsWord(kind) {
			return nil, fmt.Errorf("stage: %s: not a payload kind name", name)
		}
		groups, err := os.ReadDir(filepath.Join(src, name))
		if err != nil {
			return nil, err
		}
		for _, g := range groups {
			if !g.IsDir() {
				return nil, fmt.Errorf("stage: %s/%s: only group directories belong here", name, g.Name())
			}
			if !IsWord(g.Name()) {
				return nil, fmt.Errorf("stage: %s/%s: not a group name", name, g.Name())
			}
			sp, err := stageOne(filepath.Join(src, name, g.Name()), filepath.Join(dest, kind, g.Name()), kind, g.Name())
			if err != nil {
				return nil, err
			}
			fmt.Fprintf(log, "stage %s/%s: %d part(s), %d bytes, lock %s ok\n", kind, g.Name(), sp.Files, sp.Ciphertext, sp.LockSHA256[:12])
			out = append(out, sp)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("stage: no downstream payload (river-<kind>/<group>/MANIFEST) under %s", src)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		return out[i].Group < out[j].Group
	})
	var b bytes.Buffer
	fmt.Fprintln(&b, IndexHeader)
	for _, p := range out {
		fmt.Fprintf(&b, "payload %s %s %s %d %d %d\n", p.Kind, p.Group, p.LockSHA256, p.Files, p.Bytes, p.Ciphertext)
	}
	if err := os.WriteFile(filepath.Join(dest, IndexName), b.Bytes(), 0o600); err != nil {
		return nil, err
	}
	return out, nil
}

func stageOne(src, dest, kind, group string) (StagedPayload, error) {
	sp := StagedPayload{Kind: kind, Group: group}
	where := kind + "/" + group
	m, err := LoadManifest(src)
	if err != nil {
		return sp, fmt.Errorf("stage %s: %v", where, err)
	}
	if m.Format != PayloadFormat {
		return sp, fmt.Errorf("stage %s: MANIFEST is %s, not a downstream payload", where, m.Format.Name)
	}
	lock, err := os.ReadFile(filepath.Join(src, LockName))
	if err != nil {
		return sp, fmt.Errorf("stage %s: %v", where, err)
	}
	if LockDigest(lock) != m.LockSHA256 {
		return sp, fmt.Errorf("stage %s: LOCK does not match the MANIFEST's lock-sha256", where)
	}
	rows, info, err := ParseAnyLock(lock)
	if err != nil {
		return sp, fmt.Errorf("stage %s: %v", where, err)
	}
	if info.Format != PayloadFormat || info.Kind != kind || info.Group != group {
		return sp, fmt.Errorf("stage %s: LOCK says %s/%s", where, info.Kind, info.Group)
	}
	if len(rows) != m.Files {
		return sp, fmt.Errorf("stage %s: LOCK has %d file(s), MANIFEST %d", where, len(rows), m.Files)
	}
	want := map[string]bool{ManifestName: true, LockName: true}
	for _, p := range m.Parts {
		want[p.Name] = true
	}
	ents, err := os.ReadDir(src)
	if err != nil {
		return sp, err
	}
	for _, e := range ents {
		if !want[e.Name()] || !e.Type().IsRegular() {
			return sp, fmt.Errorf("stage %s: unexpected entry %q", where, e.Name())
		}
	}
	if err := os.MkdirAll(dest, 0o700); err != nil {
		return sp, err
	}
	// MkdirAll's mode is subject to the umask and applies only to what it creates:
	// <dest root>, <kind> and <group> are all set explicitly.
	for _, d := range []string{dest, filepath.Dir(dest), filepath.Dir(filepath.Dir(dest))} {
		if err := os.Chmod(d, 0o700); err != nil { // #nosec G302 -- directories: 0700 is owner-only (x is traversal)
			return sp, err
		}
	}
	for _, p := range m.Parts {
		n, sum, err := copyHashed(filepath.Join(src, p.Name), filepath.Join(dest, p.Name))
		if err != nil {
			return sp, fmt.Errorf("stage %s: %s: %v", where, p.Name, err)
		}
		if n != p.Size || sum != p.SHA256 {
			return sp, fmt.Errorf("stage %s: %w: part %s does not match MANIFEST (size or sha256)", where, ErrMedium, p.Name)
		}
		sp.Ciphertext += n
	}
	if err := os.WriteFile(filepath.Join(dest, LockName), lock, 0o600); err != nil { // #nosec G703 -- dest is the caller's staging directory; the name is a constant
		return sp, err
	}
	if err := os.WriteFile(filepath.Join(dest, ManifestName), m.Marshal(), 0o600); err != nil {
		return sp, err
	}
	sp.LockSHA256, sp.Files, sp.Bytes = m.LockSHA256, m.Files, m.Bytes
	return sp, nil
}

func copyHashed(from, to string) (int64, string, error) {
	in, err := os.Open(from)
	if err != nil {
		return 0, "", err
	}
	defer in.Close()
	out, err := os.OpenFile(to, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return 0, "", err
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(out, h), in)
	if err == nil {
		err = out.Sync()
	}
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	return n, hex.EncodeToString(h.Sum(nil)), err
}

// HashFile returns a file's size and sha256 (lower-case hex).
func HashFile(p string) (int64, string, error) {
	f, err := os.Open(p)
	if err != nil {
		return 0, "", err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	return n, hex.EncodeToString(h.Sum(nil)), err
}
