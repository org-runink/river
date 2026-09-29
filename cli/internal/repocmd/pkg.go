// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

package repocmd

import (
	"archive/tar"
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"strings"

	"github.com/org-runink/river/pkg/pipe"
)

// Package is what the guard knows about one package file: its name as .PKGINFO states it and
// every member of the archive.
type Package struct {
	Path    string // the package file, symlinks resolved
	File    string // its base name
	Size    int64
	Name    string // .PKGINFO pkgname
	Version string // .PKGINFO pkgver (pkgver-pkgrel)
	Arch    string
	Files   []string // every non-directory member, metadata excluded
	Dirs    []string // every directory member, without the trailing slash
}

// pkgMeta are the archive members makepkg writes for pacman, not installed files.
var pkgMeta = map[string]bool{".PKGINFO": true, ".BUILDINFO": true, ".MTREE": true, ".INSTALL": true, ".CHANGELOG": true}

// readPackage reads an uncompressed package archive.
func readPackage(r io.Reader) (*Package, error) {
	p := &Package{}
	tr := tar.NewReader(r)
	sawInfo := false
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("reading the archive: %w", err)
		}
		name := strings.TrimPrefix(path.Clean("/"+h.Name), "/")
		switch {
		case name == ".PKGINFO":
			if err := parsePkgInfo(tr, p); err != nil {
				return nil, err
			}
			sawInfo = true
		case pkgMeta[name]:
		case h.Typeflag == tar.TypeDir:
			p.Dirs = append(p.Dirs, name)
		default:
			p.Files = append(p.Files, name)
		}
	}
	if !sawInfo {
		return nil, errors.New("no .PKGINFO: not a pacman package")
	}
	if p.Name == "" || p.Version == "" || p.Arch == "" {
		return nil, errors.New(".PKGINFO lacks pkgname, pkgver or arch")
	}
	return p, nil
}

func parsePkgInfo(r io.Reader, p *Package) error {
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		k, val, ok := strings.Cut(sc.Text(), " = ")
		if !ok || strings.HasPrefix(k, "#") {
			continue
		}
		switch strings.TrimSpace(k) {
		case "pkgname":
			p.Name = strings.TrimSpace(val)
		case "pkgver":
			p.Version = strings.TrimSpace(val)
		case "arch":
			p.Arch = strings.TrimSpace(val)
		}
	}
	return sc.Err()
}

// needTool fails closed when a program the step depends on is missing.
func needTool(names ...string) error {
	for _, n := range names {
		if _, err := exec.LookPath(n); err != nil {
			return fmt.Errorf("%s: %s not found in PATH (needed to %s)", tool, n, toolPurpose[n])
		}
	}
	return nil
}

var toolPurpose = map[string]string{
	"zstd":     "read .pkg.tar.zst packages and the database",
	"repo-add": "build the repository database",
	"gpgv":     "verify signatures",
	"gh":       "upload to the GitHub release",
}

// withZstd streams the decompressed content of a zstd file into fn. It always reads the stream
// to its end (or stops zstd) and reports zstd's own failure, so a truncated file is an error.
func withZstd(ctx context.Context, file string, fn func(io.Reader) error) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	pr, pw := io.Pipe()
	done := make(chan error, 1)
	go func() {
		err := pipe.Run(ctx, pipe.IO{Stdout: pw}, pipe.Cmd("zstd", "-dcq", "--", file))
		_ = pw.CloseWithError(err)
		done <- err
	}()
	if err := fn(pr); err != nil {
		cancel()
		_ = pr.CloseWithError(err)
		<-done
		return err
	}
	_, _ = io.Copy(io.Discard, pr)
	if err := <-done; err != nil {
		return fmt.Errorf("zstd -dc %s: %w", file, err)
	}
	return nil
}

// OpenPackage reads a .pkg.tar.zst from disk.
func OpenPackage(ctx context.Context, file string) (*Package, error) {
	st, err := os.Stat(file)
	if err != nil {
		return nil, err
	}
	if !st.Mode().IsRegular() {
		return nil, fmt.Errorf("%s: not a regular file", file)
	}
	var p *Package
	err = withZstd(ctx, file, func(r io.Reader) error {
		var e error
		p, e = readPackage(r)
		return e
	})
	if err != nil {
		return nil, fmt.Errorf("%s: %w", file, err)
	}
	p.Path = file
	p.File = path.Base(file)
	p.Size = st.Size()
	return p, nil
}
