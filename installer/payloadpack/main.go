// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

// Command river-payloadpack builds, stages and opens the encrypted payloads a Runink River
// Server install medium can carry next to its live image (docs/PAYLOADS.md): the model set
// (the river-modelpack format, which river-modelpack also handles) and any downstream payload
// described by a generic LOCK.
//
//	river-payloadpack genpass                                   a new random passphrase
//	river-payloadpack lock   --kind K --group G --dir D         print a LOCK for every file in D
//	river-payloadpack pack   --lock L --dir D --out O --passphrase-file F
//	river-payloadpack check  --payload P [--lock L] [--passphrase-file F]
//	river-payloadpack unpack --payload P --lock L --dest D --passphrase-file F
//	river-payloadpack verify --dir D --lock L                   every file against the lock
//	river-payloadpack stage  --src S --dest D                   copy river-<kind>/<group>/ payloads,
//	                                                            still encrypted, verified, 0600/0700
//	river-payloadpack imagecheck --archive A --refs R           a docker-archive against "ref digest" lines
//
// Exit status: 0 success, 1 failure, 2 usage.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/org-runink/river/installer/internal/modelpack"
)

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	cmd, args := os.Args[1], os.Args[2:]
	var err error
	switch cmd {
	case "genpass":
		var p string
		if p, err = modelpack.GeneratePassphrase(); err == nil {
			fmt.Println(p)
		}
	case "lock":
		err = lock(args)
	case "pack":
		err = pack(args)
	case "check":
		err = check(args)
	case "unpack":
		err = unpack(args)
	case "verify":
		err = verify(args)
	case "stage":
		err = stage(args)
	case "imagecheck":
		err = imagecheck(args)
	case "-h", "--help", "help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "river-payloadpack: unknown command %q\n", cmd)
		usage()
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "river-payloadpack %s: %v\n", cmd, err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `usage:
  river-payloadpack genpass
  river-payloadpack lock   --kind WORD --group WORD --dir DIR
  river-payloadpack pack   --lock FILE --dir DIR --out DIR --passphrase-file FILE
                           [--part-size MiB] [--zstd-level N] [--zstd-threads N]
  river-payloadpack check  --payload DIR [--lock FILE] [--passphrase-file FILE]
  river-payloadpack unpack --payload DIR --lock FILE --dest DIR --passphrase-file FILE|-
  river-payloadpack verify --dir DIR --lock FILE
  river-payloadpack stage  --src DIR --dest DIR
  river-payloadpack imagecheck --archive FILE --refs FILE
`)
	os.Exit(2)
}

func parse(fs *flag.FlagSet, args []string, required ...string) {
	fs.Usage = usage
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 {
		usage()
	}
	for _, r := range required {
		if f := fs.Lookup(r); f == nil || f.Value.String() == "" {
			fmt.Fprintf(os.Stderr, "river-payloadpack: --%s is required\n", r)
			usage()
		}
	}
}

// lock prints a generic LOCK for every regular file under dir (sorted, relative paths).
func lock(args []string) error {
	fs := flag.NewFlagSet("lock", flag.ContinueOnError)
	kind := fs.String("kind", "", "payload kind (a lower-case word)")
	group := fs.String("group", "", "payload group (a lower-case word)")
	dir := fs.String("dir", "", "directory holding the payload's files")
	parse(fs, args, "kind", "group", "dir")
	if !modelpack.IsWord(*kind) || !modelpack.IsWord(*group) {
		return fmt.Errorf("--kind and --group must be lower-case words ([a-z0-9][a-z0-9-]*)")
	}
	var files []string
	err := filepath.WalkDir(*dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if !d.Type().IsRegular() {
			return fmt.Errorf("%s: not a regular file", p)
		}
		rel, err := filepath.Rel(*dir, p)
		if err != nil {
			return err
		}
		if filepath.ToSlash(rel) == modelpack.LockName {
			return nil
		}
		files = append(files, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return err
	}
	if len(files) == 0 {
		return fmt.Errorf("%s holds no files", *dir)
	}
	sort.Strings(files)
	var b bytes.Buffer
	fmt.Fprintf(&b, "%s\nkind %s\ngroup %s\n", modelpack.LockHeader, *kind, *group)
	for _, f := range files {
		size, sum, err := modelpack.HashFile(filepath.Join(*dir, filepath.FromSlash(f)))
		if err != nil {
			return err
		}
		fmt.Fprintf(&b, "file %d %s %s\n", size, sum, f)
	}
	if _, _, err := modelpack.ParseAnyLock(b.Bytes()); err != nil {
		return err
	}
	_, err = os.Stdout.Write(b.Bytes())
	return err
}

func pack(args []string) error {
	fs := flag.NewFlagSet("pack", flag.ContinueOnError)
	lk := fs.String("lock", "", "a generic LOCK or a models.lock")
	dir := fs.String("dir", "", "directory holding the files the lock names")
	out := fs.String("out", "", "output directory (created; must be empty)")
	pf := fs.String("passphrase-file", "", "file holding the passphrase (first line)")
	partMiB := fs.Int64("part-size", modelpack.DefaultPartSize>>20, "part size in MiB (keep below 2048)")
	level := fs.Int("zstd-level", 12, "zstd level")
	threads := fs.Int("zstd-threads", 0, "zstd threads (0 = zstd default)")
	parse(fs, args, "lock", "dir", "out", "passphrase-file")
	if *partMiB <= 0 || *partMiB >= 2048 {
		return fmt.Errorf("--part-size must be between 1 and 2047 MiB")
	}
	lb, err := os.ReadFile(*lk)
	if err != nil {
		return err
	}
	pass, err := modelpack.ReadPassphrase(*pf)
	if err != nil {
		return err
	}
	m, err := modelpack.Pack(modelpack.PackOptions{Lock: lb, ModelsDir: *dir, OutDir: *out,
		Passphrase: pass, PartSize: *partMiB << 20, ZstdLevel: *level, ZstdThreads: *threads,
		Log: os.Stderr})
	if err != nil {
		return err
	}
	// A generic payload carries its LOCK next to its MANIFEST (docs/PAYLOADS.md).
	if m.Format == modelpack.PayloadFormat {
		if err := os.WriteFile(filepath.Join(*out, modelpack.LockName), lb, 0o644); err != nil { // #nosec G306 G703 -- the LOCK holds no secret; --out is the operator's own output directory
			return err
		}
	}
	var ct int64
	for _, p := range m.Parts {
		ct += p.Size
	}
	fmt.Printf("packed %d file(s), %d bytes -> %d part(s), %d bytes (%.1f%%) in %s\n",
		m.Files, m.Bytes, len(m.Parts), ct, 100*float64(ct)/float64(max(m.Bytes, 1)), *out)
	return nil
}

func check(args []string) error {
	fs := flag.NewFlagSet("check", flag.ContinueOnError)
	payload := fs.String("payload", "", "payload directory (MANIFEST + parts)")
	lk := fs.String("lock", "", "optional: the lock it must be built from (default: its own LOCK, if any)")
	pf := fs.String("passphrase-file", "", "optional: also check that this passphrase opens the payload")
	parse(fs, args, "payload")
	m, err := modelpack.LoadManifest(*payload)
	if err != nil {
		return err
	}
	lp := *lk
	if lp == "" && m.Format == modelpack.PayloadFormat {
		lp = filepath.Join(*payload, modelpack.LockName)
	}
	if lp != "" {
		lb, err := os.ReadFile(lp)
		if err != nil {
			return err
		}
		if modelpack.LockDigest(lb) != m.LockSHA256 {
			return fmt.Errorf("%s does not match the MANIFEST's lock-sha256", lp)
		}
		if _, info, err := modelpack.ParseAnyLock(lb); err != nil {
			return err
		} else if info.Format != m.Format {
			return fmt.Errorf("%s is a %s lock, the payload is %s", lp, info.Format.Name, m.Format.Name)
		}
	}
	if *pf != "" {
		pass, err := modelpack.ReadPassphrase(*pf)
		if err != nil {
			return err
		}
		if err := modelpack.CheckKey(*payload, m, pass); err != nil {
			return err
		}
		fmt.Println("passphrase opens the payload")
	}
	if err := modelpack.CheckParts(*payload, m); err != nil {
		return err
	}
	fmt.Printf("payload OK: %s, %d part(s), %d file(s), %d bytes, lock sha256 %s\n",
		m.Format.Name, len(m.Parts), m.Files, m.Bytes, m.LockSHA256)
	return nil
}

func unpack(args []string) error {
	fs := flag.NewFlagSet("unpack", flag.ContinueOnError)
	payload := fs.String("payload", "", "payload directory (MANIFEST + parts)")
	lk := fs.String("lock", "", "the lock to open it against (a trusted copy)")
	dest := fs.String("dest", "", "an existing, empty directory")
	pf := fs.String("passphrase-file", "", "file holding the passphrase, or - for stdin")
	parse(fs, args, "payload", "lock", "dest", "passphrase-file")
	lb, err := os.ReadFile(*lk)
	if err != nil {
		return err
	}
	pass, err := modelpack.ReadPassphrase(*pf)
	if err != nil {
		return err
	}
	if g := strings.ReplaceAll(pass, " ", ""); len(g) == 64 && strings.Trim(g, "0123456789abcdef") == "" {
		pass = g
	}
	if err := modelpack.Unpack(modelpack.UnpackOptions{PayloadDir: *payload, Lock: lb, Dest: *dest,
		Passphrase: pass, Log: os.Stderr}); err != nil {
		return err
	}
	fmt.Printf("unpacked and verified against %s into %s\n", *lk, *dest)
	return nil
}

func verify(args []string) error {
	fs := flag.NewFlagSet("verify", flag.ContinueOnError)
	dir := fs.String("dir", "", "directory to check")
	lk := fs.String("lock", "", "a generic LOCK or a models.lock")
	parse(fs, args, "dir", "lock")
	lb, err := os.ReadFile(*lk)
	if err != nil {
		return err
	}
	if err := modelpack.Verify(*dir, lb, os.Stderr); err != nil {
		return err
	}
	fmt.Printf("all files match %s\n", *lk)
	return nil
}

func stage(args []string) error {
	fs := flag.NewFlagSet("stage", flag.ContinueOnError)
	src := fs.String("src", "", "directory holding river-<kind>/<group>/ payloads (e.g. the medium root)")
	dest := fs.String("dest", "", "destination (absent or empty; e.g. /var/lib/runink/payloads)")
	parse(fs, args, "src", "dest")
	sp, err := modelpack.Stage(*src, *dest, os.Stderr)
	if err != nil {
		return err
	}
	var n int64
	for _, p := range sp {
		n += p.Ciphertext
	}
	fmt.Printf("staged %d payload(s), %d bytes, still encrypted, into %s (index: %s)\n",
		len(sp), n, *dest, filepath.Join(*dest, modelpack.IndexName))
	return nil
}

func imagecheck(args []string) error {
	fs := flag.NewFlagSet("imagecheck", flag.ContinueOnError)
	archive := fs.String("archive", "", "a docker-archive (podman save --multi-image-archive)")
	refs := fs.String("refs", "", `file of "<ref> sha256:<config digest>" lines`)
	parse(fs, args, "archive", "refs")
	f, err := os.Open(*refs)
	if err != nil {
		return err
	}
	want, err := modelpack.ParseExpected(f)
	_ = f.Close() // read-only
	if err != nil {
		return err
	}
	found, err := modelpack.CheckImageArchive(*archive, want)
	if err != nil {
		return err
	}
	layers := 0
	for _, i := range found {
		layers += i.Layers
	}
	fmt.Printf("archive OK: %d image(s), %d reference(s), %d layer reference(s), every layer and config verified\n",
		len(found), len(want), layers)
	return nil
}
