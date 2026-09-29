// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

package repocmd

import (
	"archive/tar"
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"

	"github.com/org-runink/river/pkg/pipe"
)

// repoFileRE is every name a repository directory may hold: packages, the database and file
// list (with repo-add's symlinks), SHA256SUMS, and a detached .sig for each.
var repoFileRE = regexp.MustCompile(`^(?:[A-Za-z0-9._-]+\.pkg\.tar\.zst|` + RepoName + `\.(?:db|files)(?:\.tar\.zst)?|SHA256SUMS)(?:\.sig)?$`)

// Verify checks dir is a complete repository signed by the key fpr, using ONLY the public key
// in keyFile (never a local keyring). It fails closed: a missing tool, file or signature, a
// file it does not expect, a checksum or a database that disagrees with the packages.
func Verify(ctx context.Context, dir, fpr, keyFile string, w io.Writer) error {
	if dir == "" {
		return fmt.Errorf("%s: verify needs --dir", tool)
	}
	fpr, err := normFpr(fpr)
	if err != nil {
		return err
	}
	if err := needTool("gpgv", "zstd"); err != nil {
		return err
	}
	var problems []string
	bad := func(format string, a ...any) { problems = append(problems, fmt.Sprintf(format, a...)) }

	ents, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("%s: %w", tool, err)
	}
	have := map[string]bool{}
	var pkgs, signed []string
	for _, e := range ents {
		n := e.Name()
		if !repoFileRE.MatchString(n) {
			bad("%s: not a repository file; a published directory holds nothing else", n)
			continue
		}
		have[n] = true
		if strings.HasSuffix(n, pkgSuffix) {
			pkgs = append(pkgs, n)
		}
	}
	sort.Strings(pkgs)
	if len(pkgs) == 0 {
		bad("no *%s", pkgSuffix)
	}
	// What must be signed: every package, the database and file list (as the names pacman
	// fetches and as the archives), and SHA256SUMS.
	must := append(append([]string{}, pkgs...),
		RepoName+".db", dbFile, RepoName+".files", filesFile, "SHA256SUMS")
	for _, n := range must {
		switch {
		case !have[n]:
			bad("%s: missing", n)
		case !have[n+".sig"]:
			bad("%s: no signature (%s.sig); the owner signs with base/river-sign", n, n)
		default:
			signed = append(signed, n)
		}
	}
	for n := range have {
		if strings.HasSuffix(n, ".sig") && !have[strings.TrimSuffix(n, ".sig")] {
			bad("%s: a signature of nothing", n)
		}
	}
	problems = append(problems, checkSums(dir, pkgs)...)
	if have[RepoName+".db"] {
		problems = append(problems, checkDB(ctx, dir, pkgs)...)
	}

	kr, err := keyringFromArmor(keyFile)
	if err != nil {
		return fmt.Errorf("%s: %w", tool, err)
	}
	tmp, err := os.MkdirTemp("", "river-repo-verify-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	krPath := filepath.Join(tmp, "release.gpg")
	if err := os.WriteFile(krPath, kr, 0o600); err != nil {
		return err
	}
	p := pipe.New(ctx)
	res := pipe.ParallelMap(p, pipe.Slice(p, signed...), max(1, runtime.NumCPU()),
		func(ctx context.Context, n string) (string, error) {
			return verifySig(ctx, krPath, filepath.Join(dir, n), fpr), nil
		})
	msgs, err := pipe.Collect(p, res)
	if err != nil {
		return fmt.Errorf("%s: %w", tool, err)
	}
	for _, m := range msgs {
		if m != "" {
			problems = append(problems, m)
		}
	}
	if len(problems) > 0 {
		sort.Strings(problems)
		return fmt.Errorf("%s: verify FAILED for %s: %d problem(s):\n  %s", tool, dir, len(problems), strings.Join(problems, "\n  "))
	}
	fmt.Fprintf(w, "%s: %s verified: %d package(s), the database and SHA256SUMS all signed by %s\n", tool, dir, len(pkgs), fpr)
	return nil
}

// verifySig checks one detached signature with gpgv against the given keyring alone. It
// returns "" when the signature is good and chains to the primary key fpr.
func verifySig(ctx context.Context, keyring, file, fpr string) string {
	out, err := pipe.Output(ctx, nil, pipe.Cmd("gpgv", "--keyring", keyring, "--status-fd", "1", "--", file+".sig", file))
	name := filepath.Base(file)
	if err != nil {
		return fmt.Sprintf("%s: signature does not verify against the release key (%s)", name, lastLine(err))
	}
	if why := checkStatus(string(out), fpr); why != "" {
		return name + ": " + why
	}
	return ""
}

// checkStatus reads gpgv's --status-fd output. The signature is good only if there is a
// VALIDSIG whose last field (the primary key's fingerprint, whichever subkey signed) is fpr,
// and nothing reports a bad, expired or revoked signature or key.
func checkStatus(status, fpr string) string {
	ok := false
	for _, line := range strings.Split(status, "\n") {
		f := strings.Fields(line)
		if len(f) < 2 || f[0] != "[GNUPG:]" {
			continue
		}
		switch f[1] {
		case "BADSIG", "ERRSIG", "EXPSIG", "EXPKEYSIG", "REVKEYSIG", "NO_PUBKEY", "KEYREVOKED":
			return "gpgv reports " + f[1]
		case "VALIDSIG":
			if strings.EqualFold(f[len(f)-1], fpr) {
				ok = true
			} else {
				return fmt.Sprintf("signed by primary key %s, not %s", f[len(f)-1], fpr)
			}
		}
	}
	if !ok {
		return "no VALIDSIG from " + fpr
	}
	return ""
}

// checkSums checks SHA256SUMS: every package and database archive listed, every listed file
// present with that hash.
func checkSums(dir string, pkgs []string) []string {
	var problems []string
	b, err := os.ReadFile(filepath.Join(dir, "SHA256SUMS"))
	if err != nil {
		return []string{"SHA256SUMS: " + err.Error()}
	}
	listed := map[string]bool{}
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) != 2 {
			problems = append(problems, fmt.Sprintf("SHA256SUMS: malformed line %q", sc.Text()))
			continue
		}
		n := strings.TrimPrefix(f[1], "*")
		listed[n] = true
		got, err := sha256File(filepath.Join(dir, n))
		switch {
		case err != nil:
			problems = append(problems, fmt.Sprintf("%s: listed in SHA256SUMS but unreadable: %v", n, err))
		case got != f[0]:
			problems = append(problems, fmt.Sprintf("%s: sha256 differs from SHA256SUMS (changed after signing?)", n))
		}
	}
	for _, n := range append(append([]string{}, pkgs...), dbFile, filesFile) {
		if !listed[n] {
			problems = append(problems, n+": not in SHA256SUMS")
		}
	}
	return problems
}

// checkDB compares the package file names the database lists with the packages present.
func checkDB(ctx context.Context, dir string, pkgs []string) []string {
	var listed []string
	db, err := filepath.EvalSymlinks(filepath.Join(dir, RepoName+".db"))
	if err != nil {
		return []string{RepoName + ".db: " + err.Error()}
	}
	err = withZstd(ctx, db, func(r io.Reader) error {
		var e error
		listed, e = dbFilenames(r)
		return e
	})
	if err != nil {
		return []string{RepoName + ".db: " + err.Error()}
	}
	var problems []string
	in := map[string]bool{}
	for _, n := range listed {
		in[n] = true
	}
	present := map[string]bool{}
	for _, n := range pkgs {
		present[n] = true
		if !in[n] {
			problems = append(problems, n+": present but not in "+RepoName+".db")
		}
	}
	for _, n := range listed {
		if !present[n] {
			problems = append(problems, n+": in "+RepoName+".db but missing (pacman would 404)")
		}
	}
	return problems
}

// dbFilenames reads a repo-add database (uncompressed tar) and returns each %FILENAME%.
func dbFilenames(r io.Reader) ([]string, error) {
	var names []string
	tr := tar.NewReader(r)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return names, nil
		}
		if err != nil {
			return nil, err
		}
		if filepath.Base(h.Name) != "desc" {
			continue
		}
		b, err := io.ReadAll(tr)
		if err != nil {
			return nil, err
		}
		lines := strings.Split(string(b), "\n")
		for i := 0; i+1 < len(lines); i++ {
			if lines[i] == "%FILENAME%" {
				names = append(names, strings.TrimSpace(lines[i+1]))
			}
		}
	}
}

// keyringFromArmor decodes an ASCII-armored public key into the binary keyring gpgv reads,
// so verification uses that key alone and needs no gpg home.
func keyringFromArmor(file string) ([]byte, error) {
	b, err := os.ReadFile(file)
	if err != nil {
		return nil, fmt.Errorf("the release public key: %w", err)
	}
	const begin, end = "-----BEGIN PGP PUBLIC KEY BLOCK-----", "-----END PGP PUBLIC KEY BLOCK-----"
	s := string(b)
	i, j := strings.Index(s, begin), strings.Index(s, end)
	if i < 0 || j < i {
		return nil, fmt.Errorf("%s: not an armored OpenPGP public key", file)
	}
	body := strings.Split(strings.ReplaceAll(s[i+len(begin):j], "\r", ""), "\n")
	// Armor headers ("Comment: ...") end at the first blank line; the body ends before the
	// "=XXXX" checksum line.
	var data strings.Builder
	inHeaders := true
	for _, line := range body[1:] {
		line = strings.TrimSpace(line)
		if inHeaders {
			if line == "" {
				inHeaders = false
				continue
			}
			if strings.Contains(line, ": ") {
				continue
			}
			inHeaders = false
		}
		if strings.HasPrefix(line, "=") {
			break
		}
		data.WriteString(line)
	}
	out, err := base64.StdEncoding.DecodeString(data.String())
	if err != nil || len(out) == 0 {
		return nil, fmt.Errorf("%s: bad armor: %v", file, err)
	}
	return out, nil
}

// lastLine is the end of a failed command's stderr (what gpgv says last is why), or the
// error itself.
func lastLine(err error) string {
	var se *pipe.StageError
	if errors.As(err, &se) && se.Stderr != "" {
		lines := strings.Split(se.Stderr, "\n")
		return strings.TrimSpace(lines[len(lines)-1])
	}
	return err.Error()
}
