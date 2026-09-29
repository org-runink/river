// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

// Command river-modelpack builds and opens the encrypted model payload of a Runink River
// Server install medium (docs/MODEL-PAYLOAD.md).
//
//	river-modelpack genpass                                   print a new random passphrase
//	river-modelpack pack   --lock L --models-dir D --out O --passphrase-file F
//	river-modelpack check  --payload P [--passphrase-file F]   part sizes + sha256 (+ key)
//	river-modelpack unpack --payload P --lock L --dest D --passphrase-file F
//	river-modelpack verify --dir D --lock L                   every file against the lock
//
// Exit status: 0 success, 1 failure, 2 usage.
package main

import (
	"flag"
	"fmt"
	"os"
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
	case "pack":
		err = pack(args)
	case "check":
		err = check(args)
	case "unpack":
		err = unpack(args)
	case "verify":
		err = verify(args)
	case "-h", "--help", "help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "river-modelpack: unknown command %q\n", cmd)
		usage()
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "river-modelpack %s: %v\n", cmd, err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `usage:
  river-modelpack genpass
  river-modelpack pack   --lock FILE --models-dir DIR --out DIR --passphrase-file FILE
                         [--part-size MiB] [--zstd-level N] [--zstd-threads N]
  river-modelpack check  --payload DIR [--passphrase-file FILE]
  river-modelpack unpack --payload DIR --lock FILE --dest DIR --passphrase-file FILE|-
  river-modelpack verify --dir DIR --lock FILE
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
			fmt.Fprintf(os.Stderr, "river-modelpack: --%s is required\n", r)
			usage()
		}
	}
}

func pack(args []string) error {
	fs := flag.NewFlagSet("pack", flag.ContinueOnError)
	lock := fs.String("lock", "", "models.lock")
	dir := fs.String("models-dir", "", "the model cache filled by build/models-fetch.sh")
	out := fs.String("out", "", "output directory (created; must be empty)")
	pf := fs.String("passphrase-file", "", "file holding the passphrase (first line)")
	partMiB := fs.Int64("part-size", modelpack.DefaultPartSize>>20, "part size in MiB (keep below 2048)")
	level := fs.Int("zstd-level", 12, "zstd level")
	threads := fs.Int("zstd-threads", 0, "zstd threads (0 = zstd default)")
	parse(fs, args, "lock", "models-dir", "out", "passphrase-file")
	if *partMiB <= 0 || *partMiB >= 2048 {
		return fmt.Errorf("--part-size must be between 1 and 2047 MiB")
	}
	lb, err := os.ReadFile(*lock)
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
	pf := fs.String("passphrase-file", "", "optional: also check that this passphrase opens the payload")
	parse(fs, args, "payload")
	m, err := modelpack.LoadManifest(*payload)
	if err != nil {
		return err
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
	fmt.Printf("payload OK: %d part(s), %d file(s), %d bytes, lock sha256 %s\n",
		len(m.Parts), m.Files, m.Bytes, m.LockSHA256)
	return nil
}

func unpack(args []string) error {
	fs := flag.NewFlagSet("unpack", flag.ContinueOnError)
	payload := fs.String("payload", "", "payload directory (MANIFEST + parts)")
	lock := fs.String("lock", "", "models.lock of this image")
	dest := fs.String("dest", "", "an existing, empty directory")
	pf := fs.String("passphrase-file", "", "file holding the passphrase, or - for stdin")
	parse(fs, args, "payload", "lock", "dest", "passphrase-file")
	lb, err := os.ReadFile(*lock)
	if err != nil {
		return err
	}
	pass, err := modelpack.ReadPassphrase(*pf)
	if err != nil {
		return err
	}
	// A passphrase typed from the grouped display may carry the group spaces; the generated
	// ones are hex, so spaces are never part of them.
	if g := strings.ReplaceAll(pass, " ", ""); len(g) == 64 && strings.Trim(g, "0123456789abcdef") == "" {
		pass = g
	}
	if err := modelpack.Unpack(modelpack.UnpackOptions{PayloadDir: *payload, Lock: lb, Dest: *dest,
		Passphrase: pass, Log: os.Stderr}); err != nil {
		return err
	}
	fmt.Printf("unpacked and verified against %s into %s\n", *lock, *dest)
	return nil
}

func verify(args []string) error {
	fs := flag.NewFlagSet("verify", flag.ContinueOnError)
	dir := fs.String("dir", "", "model directory")
	lock := fs.String("lock", "", "models.lock")
	parse(fs, args, "dir", "lock")
	lb, err := os.ReadFile(*lock)
	if err != nil {
		return err
	}
	if err := modelpack.Verify(*dir, lb, os.Stderr); err != nil {
		return err
	}
	fmt.Printf("all files match %s\n", *lock)
	return nil
}
