// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: GPL-2.0-only

package main

import (
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The pinned kernel's tools/include/uapi/linux/bpf.h and the bpf_helper_defs.h that UPSTREAM
// scripts/bpf_doc.py generated from it (see testdata/README). Both move with the kernel pin.
const (
	fixtureBPFH    = "testdata/bpf-7.2.7.h.gz"
	fixtureHelpers = "testdata/bpf_helper_defs-7.2.7.h.gz"
)

func gunzip(t *testing.T, path string) []byte {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	z, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	b, err := io.ReadAll(z)
	if err != nil {
		t.Fatal(err)
	}
	if len(b) == 0 {
		t.Fatalf("%s is empty", path)
	}
	return b
}

func writeTemp(t *testing.T, name string, b []byte) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, b, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// run runs the command line the kernel's tools/lib/bpf/Makefile uses.
func runHeader(t *testing.T, bpfh []byte) ([]byte, error) {
	t.Helper()
	var out bytes.Buffer
	err := generate("/nonexistent/scripts/bpf_doc.py",
		[]string{"--header", "--file", writeTemp(t, "bpf.h", bpfh)}, &out)
	return out.Bytes(), err
}

// TestByteIdenticalToUpstream is the contract: for the pinned kernel's bpf.h, the output is
// byte for byte what upstream bpf_doc.py produced.
func TestByteIdenticalToUpstream(t *testing.T) {
	want := gunzip(t, fixtureHelpers)
	got, err := runHeader(t, gunzip(t, fixtureBPFH))
	if err != nil {
		t.Fatalf("generation failed: %v", err)
	}
	if bytes.Equal(got, want) {
		return
	}
	gl, wl := strings.Split(string(got), "\n"), strings.Split(string(want), "\n")
	for i := 0; i < len(gl) || i < len(wl); i++ {
		var g, w string
		if i < len(gl) {
			g = gl[i]
		}
		if i < len(wl) {
			w = wl[i]
		}
		if g != w {
			t.Fatalf("output differs from upstream bpf_doc.py at line %d:\n got: %q\nwant: %q\n(%d vs %d bytes)",
				i+1, g, w, len(got), len(want))
		}
	}
	t.Fatalf("output differs from upstream bpf_doc.py (%d vs %d bytes)", len(got), len(want))
}

// The fixture must exercise what the port re-implements, or byte-identity proves little.
func TestFixtureCoverage(t *testing.T) {
	want := string(gunzip(t, fixtureHelpers))
	for _, s := range []string{
		"static __bpf_fastcall __u32 (* const bpf_get_smp_processor_id)(void) = (void *) 8;",         // Attributes
		"#define __bpf_fastcall __attribute__((bpf_fastcall))",                                       // used_attrs block
		"static long (* const bpf_trace_printk)(const char *fmt, __u32 fmt_size, ...) = (void *) 6;", // "..."
		"static __u64 (* const bpf_get_socket_cookie)(void *ctx) = (void *) 46;",                     // overloaded helper
		"static void *(* const bpf_map_lookup_elem)(void *map, const void *key) = (void *) 1;",       // ret star, mapped type
	} {
		if !strings.Contains(want, s) {
			t.Errorf("fixture no longer contains %q", s)
		}
	}
}

// Each consistency check bpf_doc.py enforces must still reject a broken bpf.h.
func TestRejectsInconsistentHeaders(t *testing.T) {
	src := string(gunzip(t, fixtureBPFH))
	mutate := func(old, new string) []byte {
		t.Helper()
		if !strings.Contains(src, old) {
			t.Fatalf("fixture lacks %q", old)
		}
		return []byte(strings.Replace(src, old, new, 1))
	}
	cases := []struct {
		name, want string
		in         []byte
	}{
		{"described helper missing from FN list", "Helper bpf_map_lookup_elem is missing from enum bpf_func_id",
			mutate("\tFN(map_lookup_elem, 1, ##ctx)\t\t\t\\\n", "")},
		{"FN entry without a description", "doesn't match the number of unique helper defined in ___BPF_FUNC_MAPPER (212)",
			mutate("\tFN(cgrp_storage_delete, 211, ##ctx)\t\t\\\n",
				"\tFN(cgrp_storage_delete, 211, ##ctx)\t\t\\\n\tFN(undocumented, 212, ##ctx)\t\t\\\n")},
		{"duplicated helper number", "Helper bpf_map_update_elem has duplicated value 1",
			mutate("FN(map_update_elem, 2, ##ctx)", "FN(map_update_elem, 1, ##ctx)")},
		{"description out of enum order", "comment order (#1) must be aligned with its position (#2)",
			mutate("\tFN(map_lookup_elem, 1, ##ctx)\t\t\t\\\n\tFN(map_update_elem, 2, ##ctx)\t\t\t\\\n",
				"\tFN(map_update_elem, 2, ##ctx)\t\t\t\\\n\tFN(map_lookup_elem, 1, ##ctx)\t\t\t\\\n")},
		{"unknown argument type", "Unrecognized type 'struct nosuchtype'",
			mutate(" * long bpf_map_update_elem(struct bpf_map *map,", " * long bpf_map_update_elem(struct nosuchtype *map,")},
		{"unknown attribute", "Unexpected attribute",
			mutate(" * \t\t__bpf_fastcall\n", " * \t\t__bpf_slowcall\n")},
		{"no description section", "No description section found",
			mutate(" * void *bpf_map_lookup_elem(struct bpf_map *map, const void *key)\n * \tDescription\n",
				" * void *bpf_map_lookup_elem(struct bpf_map *map, const void *key)\n * \tDescriptio\n")},
		{"no helper list", "Could not find start of eBPF helper descriptions list",
			mutate(helpersDocStart, "Start of nothing")},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out, err := runHeader(t, c.in)
			if err == nil {
				t.Fatalf("accepted a broken bpf.h (%d bytes of output)", len(out))
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Fatalf("error %q does not mention %q", err, c.want)
			}
			if len(out) != 0 {
				t.Fatalf("wrote %d bytes of output on failure", len(out))
			}
		})
	}
}

// Captures that depend on Python's backtracking order.
func TestBreakDown(t *testing.T) {
	cases := []struct {
		in, ret, star, name string
		args                []arg
	}{
		{"void *bpf_map_lookup_elem(struct bpf_map *map, const void *key)", "void", "*", "bpf_map_lookup_elem",
			[]arg{{"struct bpf_map", "*", "map", true}, {"const void", "*", "key", true}}},
		{"long bpf_trace_printk(const char *fmt, u32 fmt_size, ...)", "long", "", "bpf_trace_printk",
			[]arg{{"const char", "*", "fmt", true}, {"u32", "", "fmt_size", true}, {"...", "", "", false}}},
		{"u64 bpf_ktime_get_ns(void)", "u64", "", "bpf_ktime_get_ns", []arg{{"void", "", "", false}}},
		{"const struct x **f(const struct bpf_dynptr *ptr)", "const struct x", "**", "f",
			[]arg{{"const struct bpf_dynptr", "*", "ptr", true}}},
	}
	for _, c := range cases {
		p, err := breakDown(c.in)
		if err != nil {
			t.Fatalf("%s: %v", c.in, err)
		}
		if p.retType != c.ret || p.retStar != c.star || p.name != c.name || len(p.args) != len(c.args) {
			t.Fatalf("%s: got %+v", c.in, p)
		}
		for i := range c.args {
			if p.args[i] != c.args[i] {
				t.Errorf("%s: arg %d = %+v, want %+v", c.in, i, p.args[i], c.args[i])
			}
		}
	}
}

func TestSectionText(t *testing.T) {
	cases := []struct {
		in, want string
		ok       bool
	}{
		{" * \t\ttext\n", "text", true},
		{" * \t\t\tindented\n", "\tindented", true},
		{" *\t\ttext\n", "text", true},
		{" *                 text\n", "text", true},   // space, 8 spaces, 8 spaces
		{" *                  text\n", " text", true}, // one more space stays in the text
		{" * \tReturn\n", "", false},
		{" *\n", "", false},
	}
	for _, c := range cases {
		got, ok := sectionText(c.in)
		if got != c.want || ok != c.ok {
			t.Errorf("sectionText(%q) = %q, %v; want %q, %v", c.in, got, ok, c.want, c.ok)
		}
	}
}

// The kernel Makefile passes --file, which argparse resolves as an abbreviation of --filename.
func TestCommandLine(t *testing.T) {
	bpfh := writeTemp(t, "bpf.h", gunzip(t, fixtureBPFH))
	want := gunzip(t, fixtureHelpers)
	for _, argv := range [][]string{
		{"--header", "--file", bpfh},
		{"--header", "--filename=" + bpfh},
		{"--filename", bpfh, "--hea", "helpers"},
	} {
		var out bytes.Buffer
		if err := generate("x", argv, &out); err != nil || !bytes.Equal(out.Bytes(), want) {
			t.Errorf("%v: err=%v, %d bytes", argv, err, out.Len())
		}
	}
	for _, c := range []struct {
		argv []string
		code int
	}{
		{[]string{"--json", "--file", bpfh}, 2},
		{[]string{"--file", bpfh}, 2}, // RST: not ported
		{[]string{"--header", "--file", bpfh, "syscall"}, 1},
		{[]string{"--he", "--file", bpfh}, 2}, // ambiguous: --help, --header
		{[]string{"--header"}, 2},             // no default bpf.h next to "x"
	} {
		var out bytes.Buffer
		err := generate("x", c.argv, &out)
		var e *exitError
		if !errors.As(err, &e) || e.code != c.code || out.Len() != 0 {
			t.Errorf("%v: err=%v, want exit %d and no output", c.argv, err, c.code)
		}
	}
}
