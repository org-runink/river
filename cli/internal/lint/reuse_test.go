// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

package lint

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// The tags are split so that this file's own fixtures are not read as its header.
const (
	tagC = "SPDX-" + "FileCopyrightText: "
	tagL = "SPDX-" + "License-Identifier: "
)

func hdr(c, l string) string {
	s := ""
	if c != "" {
		s += "// " + tagC + c + "\n"
	}
	if l != "" {
		s += "// " + tagL + l + "\n"
	}
	return s + "package x\n"
}

// reuseRepo writes files into a new git repository and stages them. A nil value is skipped.
func reuseRepo(t *testing.T, files map[string]string) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	for name, body := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range [][]string{{"init", "-q"}, {"add", "-A"}} {
		if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return dir
}

// baseRepo is compliant: a header file, a sidecar, an annotated file and the licence texts.
func baseRepo() map[string]string {
	return map[string]string{
		"LICENSES/MIT.txt":        "MIT text\n",
		"LICENSES/Apache-2.0.txt": "Apache text\n",
		"LICENSE":                 "not linted\n",
		"a.go":                    hdr("2026 A", "MIT"),
		"img.png":                 "\x89PNG binary",
		"img.png.license":         tagC + "2026 B\n" + tagL + "Apache-2.0\n",
		"docs/readme.md":          "no header\n",
		"REUSE.toml": `version = 1
[[annotations]]
path = "docs/**"
SPDX-FileCopyrightText = "2026 Docs"
SPDX-License-Identifier = "MIT"
`,
	}
}

func runReuse(t *testing.T, dir string) (string, string, error) {
	t.Helper()
	var errb, outb bytes.Buffer
	err := Reuse(context.Background(), dir, &errb, &outb)
	return outb.String(), errb.String(), err
}

func TestReuseCompliant(t *testing.T) {
	out, errs, err := runReuse(t, reuseRepo(t, baseRepo()))
	if err != nil {
		t.Fatalf("want compliant, got %v\n%s\n%s", err, errs, out)
	}
	for _, want := range []string{
		"* Files with copyright information: 3 / 3",
		"* Files with license information: 3 / 3",
		"* Used licenses: Apache-2.0, MIT",
		"Congratulations!",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("report lacks %q:\n%s", want, out)
		}
	}
}

// Each case breaks the compliant repository one way and names the line the lint must print.
func TestReuseFailures(t *testing.T) {
	cases := []struct {
		name   string
		change map[string]string
		want   []string
	}{
		{"no header at all", map[string]string{"b.go": "package x\n"},
			[]string{"b.go: no copyright information", "b.go: no licensing information"}},
		{"licence only", map[string]string{"b.go": hdr("", "MIT")},
			[]string{"b.go: no copyright information"}},
		{"copyright only", map[string]string{"b.go": hdr("2026 A", "")},
			[]string{"b.go: no licensing information"}},
		{"missing licence text", map[string]string{"b.go": hdr("2026 A", "BSD-3-Clause")},
			[]string{"missing licence text LICENSES/BSD-3-Clause.txt, used by: b.go"}},
		{"unused licence text", map[string]string{"LICENSES/0BSD.txt": "x\n"},
			[]string{"unused licence LICENSES/0BSD.txt"}},
		{"bad licence", map[string]string{"b.go": hdr("2026 A", "Proprietary"), "LICENSES/Proprietary.txt": "x\n"},
			[]string{`bad licence "Proprietary"`}},
		{"deprecated licence", map[string]string{"b.go": hdr("2026 A", "GPL-2.0"), "LICENSES/GPL-2.0.txt": "x\n"},
			[]string{`deprecated licence "GPL-2.0"`}},
		{"licence file without extension", map[string]string{"b.go": hdr("2026 A", "0BSD"), "LICENSES/0BSD": "x\n"},
			[]string{"licence file without an extension: LICENSES/0BSD"}},
		{"dangling operator", map[string]string{"b.go": hdr("2026 A", "MIT AND")},
			[]string{`invalid SPDX expression in b.go: "MIT AND"`}},
		{"two licences, no operator", map[string]string{"b.go": hdr("2026 A", "MIT Apache-2.0")},
			[]string{`invalid SPDX expression in b.go: "MIT Apache-2.0"`}},
		{"unbalanced parenthesis", map[string]string{"b.go": hdr("2026 A", "(MIT OR Apache-2.0")},
			[]string{"unbalanced parenthesis"}},
		{"WITH without an exception", map[string]string{"b.go": hdr("2026 A", "Apache-2.0 WITH")},
			[]string{"WITH needs an exception identifier"}},
		{"exception text missing", map[string]string{"b.go": hdr("2026 A", "Apache-2.0 WITH LLVM-exception")},
			[]string{"missing licence text LICENSES/LLVM-exception.txt"}},
		{"bad expression in REUSE.toml", map[string]string{"REUSE.toml": "version = 1\n[[annotations]]\npath = \"docs/**\"\nSPDX-FileCopyrightText = \"x\"\nSPDX-License-Identifier = \"MIT OR\"\n"},
			[]string{`invalid SPDX expression in REUSE.toml (annotation 1): "MIT OR"`}},
		{"REUSE.toml without version", map[string]string{"REUSE.toml": "[[annotations]]\npath = \"docs/**\"\n"},
			[]string{"REUSE.toml: version must be 1"}},
		{"REUSE.toml syntax error", map[string]string{"REUSE.toml": "version = 1\n[[annotations]]\npath = [\"docs/**\"\n"},
			[]string{"REUSE.toml: line"}},
		{"REUSE.toml path escaping its directory", map[string]string{"REUSE.toml": "version = 1\n[[annotations]]\npath = \"../x\"\n"},
			[]string{"leaves the REUSE.toml's directory"}},
		{"legacy dep5", map[string]string{".reuse/dep5": "Format: x\n"},
			[]string{".reuse/dep5 is not supported"}},
		{"a single star stays in one directory", map[string]string{
			"docs/deep/x.md": "no header\n",
			"REUSE.toml":     "version = 1\n[[annotations]]\npath = \"docs/*\"\nSPDX-FileCopyrightText = \"x\"\nSPDX-License-Identifier = \"MIT\"\n"},
			[]string{"docs/deep/x.md: no copyright information"}},
		{"an unterminated ignore block hides the header", map[string]string{"b.go": "// " + ignoreStart + "\n" + hdr("2026 A", "MIT")},
			[]string{"b.go: no copyright information", "b.go: no licensing information"}},
		{"a sidecar replaces the file's own header", map[string]string{"b.go": hdr("2026 A", "MIT"), "b.go.license": "nothing\n"},
			[]string{"b.go: no copyright information"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			files := baseRepo()
			for k, v := range tc.change {
				files[k] = v
			}
			out, errs, err := runReuse(t, reuseRepo(t, files))
			if err == nil {
				t.Fatalf("want a failure, got compliant:\n%s", out)
			}
			for _, w := range tc.want {
				if !strings.Contains(errs, w) {
					t.Errorf("stderr lacks %q:\n%s", w, errs)
				}
			}
			if !strings.Contains(out, "Unfortunately, your project is not compliant") {
				t.Errorf("report does not say non-compliant:\n%s", out)
			}
		})
	}
}

func TestReusePrecedence(t *testing.T) {
	files := baseRepo()
	files["LICENSES/0BSD.txt"] = "x\n"
	files["REUSE.toml"] = `# comment
version = 1 # trailing comment
SPDX-PackageName = "ignored"

[[annotations]]
path = ["docs/**", "src/**"]
SPDX-FileCopyrightText = ["2026 Docs", 'literal']
SPDX-License-Identifier = "MIT"

# The last match wins: this narrower entry beats the one above for src/.
[[annotations]]
path = "src/**"
precedence = "aggregate"
SPDX-FileCopyrightText = """
2026 Src"""
SPDX-License-Identifier = "0BSD"

[[annotations]]
path = "vendored/**"
precedence = "override"
SPDX-FileCopyrightText = "Upstream \"Authors\""
SPDX-License-Identifier = "Apache-2.0"

[[annotations]]
path = 'star\*.txt'
SPDX-FileCopyrightText = "x"
SPDX-License-Identifier = "MIT"
`
	// override: the file's own (broken) header is never read.
	files["vendored/lib.go"] = "// " + tagL + "NOT A ( VALID EXPRESSION\n"
	// closest: the file's licence is kept and the missing copyright is filled in.
	files["docs/partial.go"] = hdr("", "Apache-2.0")
	// aggregate: both the file's and the annotation's licences are used.
	files["src/both.go"] = hdr("2026 A", "MIT")
	// a literal star in a glob.
	files["star*.txt"] = "text\n"
	// a nested REUSE.toml governs its own directory.
	files["sub/REUSE.toml"] = "version = 1\n[[annotations]]\npath = \"*.md\"\nSPDX-FileCopyrightText = \"sub\"\nSPDX-License-Identifier = \"0BSD\"\n"
	files["sub/x.md"] = "no header\n"
	// ignored: empty files, LICENSE-like files, SPDX documents, headers inside ignore blocks.
	files["empty.txt"] = ""
	files["COPYING.md"] = "not linted\n"
	files["sbom.spdx.json"] = "{}\n"
	files["ig.go"] = hdr("2026 A", "MIT") + "// " + ignoreStart + "\n// " + tagL + "BROKEN (\n// " + ignoreEnd + "\n"
	// a header written by a script into another file, inside a string literal.
	files["gen.sh"] = "# " + tagC + "2026 A\n# " + tagL + "MIT\nprintf '<!-- " + tagL + "MIT\\n'\n"

	dir := reuseRepo(t, files)
	if err := os.Symlink("a.go", filepath.Join(dir, "link.go")); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "-C", dir, "add", "link.go").CombinedOutput(); err != nil {
		t.Fatalf("git add: %v\n%s", err, out)
	}
	// untracked files are not linted.
	if err := os.WriteFile(filepath.Join(dir, "untracked.go"), []byte("package x\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	res, err := reuseLint(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if n := res.problems(); n != 0 {
		var out bytes.Buffer
		reuseReport(res, &out)
		t.Fatalf("want compliant, got %d problem(s):\n%s", n, out.String())
	}
	got := map[string]reuseInfo{}
	var paths []string
	for _, f := range res.files {
		got[f.path] = f.info
		paths = append(paths, f.path)
	}
	wantPaths := []string{"a.go", "docs/partial.go", "docs/readme.md", "gen.sh", "ig.go", "img.png", "src/both.go", "star*.txt", "sub/x.md", "vendored/lib.go"}
	if !slices.Equal(paths, wantPaths) {
		t.Fatalf("covered files:\n got %v\nwant %v", paths, wantPaths)
	}
	check := func(p string, wantC, wantL []string) {
		t.Helper()
		if !slices.Equal(got[p].copyrights, wantC) || !slices.Equal(got[p].exprs, wantL) {
			t.Errorf("%s: got %q %q, want %q %q", p, got[p].copyrights, got[p].exprs, wantC, wantL)
		}
	}
	check("vendored/lib.go", []string{`Upstream "Authors"`}, []string{"Apache-2.0"})
	check("docs/partial.go", []string{"2026 Docs", "literal"}, []string{"Apache-2.0"})
	check("docs/readme.md", []string{"2026 Docs", "literal"}, []string{"MIT"})
	check("src/both.go", []string{tagC + "2026 A", "2026 Src"}, []string{"MIT", "0BSD"})
	check("sub/x.md", []string{"sub"}, []string{"0BSD"})
	check("ig.go", []string{tagC + "2026 A"}, []string{"MIT"})
	check("gen.sh", []string{tagC + "2026 A"}, []string{"MIT"})
}

func TestParseSPDX(t *testing.T) {
	for expr, want := range map[string][]string{
		"MIT":                                    {"MIT"},
		"MIT OR Apache-2.0":                      {"MIT", "Apache-2.0"},
		"(MIT or Apache-2.0) and 0BSD":           {"MIT", "Apache-2.0", "0BSD"},
		"Apache-2.0 WITH LLVM-exception":         {"Apache-2.0", "LLVM-exception"},
		"GPL-2.0-or-later WITH x AND (A OR (B))": {"GPL-2.0-or-later", "x", "A", "B"},
		"LicenseRef-Runink-Proprietary":          {"LicenseRef-Runink-Proprietary"},
	} {
		got, err := parseSPDX(expr)
		if err != nil || !slices.Equal(got, want) {
			t.Errorf("parseSPDX(%q) = %q, %v; want %q", expr, got, err, want)
		}
	}
	for _, bad := range []string{"", "AND", "MIT AND", "MIT OR OR 0BSD", "(MIT", "MIT)", "MIT WITH", "MIT WITH (x)", "MIT 0BSD", "()"} {
		if _, err := parseSPDX(bad); err == nil {
			t.Errorf("parseSPDX(%q) accepted a bad expression", bad)
		}
	}
}

func TestReuseGlob(t *testing.T) {
	for _, tc := range []struct {
		glob, path string
		want       bool
	}{
		{"**", "a/b/c", true},
		{"*", "a", true},
		{"*", "a/b", false},
		{"a/*.md", "a/x.md", true},
		{"a/*.md", "a/b/x.md", false},
		{"a/**/*.md", "a/b/c/x.md", true},
		{"a/**", "a", false},
		{`x\*`, "x*", true},
		{`x\*`, "xy", false},
		{`back\\slash`, `back\slash`, true},
		{"file.txt", "file_txt", false},
	} {
		re, err := reuseGlob(tc.glob)
		if err != nil {
			t.Fatal(err)
		}
		if got := re.MatchString(tc.path); got != tc.want {
			t.Errorf("glob %q on %q = %v, want %v", tc.glob, tc.path, got, tc.want)
		}
	}
}

func TestSPDXListLoaded(t *testing.T) {
	l := spdxList()
	if len(l.ids) < 800 || !l.known("MIT") || !l.known("LLVM-exception") || !l.deprecated["GPL-2.0"] || l.known("Proprietary") {
		t.Fatalf("SPDX list not loaded correctly: %d ids", len(l.ids))
	}
}
