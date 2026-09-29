// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

package lint

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"

	"github.com/org-runink/river/pkg/pipe"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// reuse — every file says who holds its copyright and under which licence, and every licence
// it names has its text in LICENSES/ (the REUSE Specification 3.3, https://reuse.software/spec-3.3/).
//
// This is the check `reuse lint` makes, in Go, so it runs as a plain process on any runner:
// no container, no Python. What it enforces:
//
//   - every covered file has copyright and licensing information, from its own SPDX header,
//     from a <file>.license sidecar, or from a REUSE.toml annotation (glob paths, the LAST
//     matching annotation of a REUSE.toml wins, nested REUSE.toml files, and the precedences
//     "closest" (default), "aggregate" and "override", as REUSE 3.3 defines them);
//   - every SPDX expression parses, and names SPDX identifiers or LicenseRef-* only;
//   - every licence used has its text in LICENSES/<id>.<ext>, and LICENSES/ holds no licence
//     nothing uses (no missing, unused, extensionless or deprecated licences);
//   - text between REUSE-IgnoreStart and REUSE-IgnoreEnd is not read.
//
// Covered files are the TRACKED files (git ls-files), minus what reuse itself skips: any path
// under a .git, .hg, .sl, .reuse or LICENSES directory, LICENSE*/COPYING* files, *.license
// sidecars, REUSE.toml, SPDX documents, symlinks, empty files and submodules. Unlike reuse,
// untracked files are not linted: a CI checkout has none, and a tool checked out beside the
// repository (as consumers of this check do) must not be linted as part of it.
//
// What this does NOT support: the legacy .reuse/dep5 file (it fails, asking for REUSE.toml),
// DocumentRef- identifiers, and the Meson subprojects exception.

const reuseSpec = "3.3"

func init() { extraLints = append(extraLints, reuseCmd) }

func reuseCmd(v *viper.Viper) *cobra.Command {
	return &cobra.Command{
		Use:   "reuse",
		Short: "REUSE " + reuseSpec + ": every tracked file has copyright and licence information, every licence has its text in LICENSES/",
		Long: "The check `reuse lint` makes, in Go: SPDX headers, .license sidecars and REUSE.toml\n" +
			"annotations (globs, closest/aggregate/override precedence), SPDX expressions, and\n" +
			"LICENSES/ (missing, unused, bad, deprecated, extensionless). Exits non-zero on any problem.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return Reuse(cmd.Context(), v.GetString("repo"), cmd.ErrOrStderr(), cmd.OutOrStdout())
		},
	}
}

// reuseInfo is the copyright and licensing information of one file.
type reuseInfo struct {
	copyrights []string
	exprs      []string // SPDX expressions, as written
}

func (i reuseInfo) hasCopyright() bool { return len(i.copyrights) > 0 }
func (i reuseInfo) hasLicense() bool   { return len(i.exprs) > 0 }

func (i reuseInfo) merge(o reuseInfo) reuseInfo {
	return reuseInfo{
		copyrights: append(slices.Clone(i.copyrights), o.copyrights...),
		exprs:      append(slices.Clone(i.exprs), o.exprs...),
	}
}

// reuseFile is what the lint learned about one covered file.
type reuseFile struct {
	path    string
	info    reuseInfo
	readErr error
	badExpr []string // "expression: why"
}

// reuseResult is the whole lint's findings.
type reuseResult struct {
	files          []reuseFile
	used           map[string][]string // licence or exception id -> files that use it
	licenseFiles   map[string]string   // id -> LICENSES/ path
	noExtension    []string            // LICENSES/ files without an extension
	bad            map[string][]string // id -> where it appears
	deprecated     []string
	missing        map[string][]string // id -> files that use it
	unused         []string
	invalid        []string // "file: expression: why"
	readErrors     []string
	noCopyright    []string
	noLicense      []string
	fatal          []string // problems that make the rest unreliable (bad REUSE.toml, dep5, duplicates)
	withCopyright  int
	withLicense    int
	coveredFiles   int
	reuseTOMLFiles []string
}

func (r *reuseResult) problems() int {
	return len(r.noExtension) + len(r.bad) + len(r.deprecated) + len(r.missing) + len(r.unused) +
		len(r.invalid) + len(r.readErrors) + len(r.noCopyright) + len(r.noLicense) + len(r.fatal)
}

// Reuse lints repo against the REUSE Specification, writing a `reuse lint`-style report to
// outw (and the problems, again, one per line with the lint's prefix, to errw). It returns an
// error when the repository is not compliant.
func Reuse(ctx context.Context, repo string, errw, outw io.Writer) error {
	res, err := reuseLint(ctx, repo)
	if err != nil {
		return fmt.Errorf("lint-reuse: %w", err)
	}
	reuseReport(res, outw)
	if n := res.problems(); n > 0 {
		rp := &report{name: "lint-reuse", w: errw}
		for _, s := range res.fatal {
			rp.fail("%s", s)
		}
		for _, f := range res.noCopyright {
			rp.fail("%s: no copyright information", f)
		}
		for _, f := range res.noLicense {
			rp.fail("%s: no licensing information", f)
		}
		for _, s := range res.invalid {
			rp.fail("invalid SPDX expression in %s", s)
		}
		for _, s := range res.readErrors {
			rp.fail("read error: %s", s)
		}
		for _, id := range sortedKeys(res.bad) {
			rp.fail("bad licence %q (not an SPDX identifier or LicenseRef-): %s", id, strings.Join(res.bad[id], ", "))
		}
		for _, id := range res.deprecated {
			rp.fail("deprecated licence %q", id)
		}
		for _, id := range sortedKeys(res.missing) {
			rp.fail("missing licence text LICENSES/%s.txt, used by: %s", id, strings.Join(res.missing[id], ", "))
		}
		for _, id := range res.unused {
			rp.fail("unused licence %s", res.licenseFiles[id])
		}
		for _, p := range res.noExtension {
			rp.fail("licence file without an extension: %s", p)
		}
		return rp.err()
	}
	return nil
}

// reuseIgnoredDirs and reuseIgnoredFiles are the names reuse never lints (its covered.py).
var (
	reuseIgnoredDirs  = []string{".git", ".hg", ".sl", ".reuse", "LICENSES"}
	reuseIgnoredFiles = []*regexp.Regexp{
		regexp.MustCompile(`^LICEN[CS]E([-.].*)?$`),
		regexp.MustCompile(`^COPYING([-.].*)?$`),
		regexp.MustCompile(`^\.git$`),
		regexp.MustCompile(`^\.hgtags$`),
		regexp.MustCompile(`\.license$`),
		regexp.MustCompile(`^REUSE\.toml$`),
		regexp.MustCompile(`^CAL-1\.0(-Combined-Work-Exception)?(\..+)?$`),
		regexp.MustCompile(`^SHL-2\.1(\..+)?$`),
		regexp.MustCompile(`\.spdx$`),
		regexp.MustCompile(`\.spdx\.(rdf|json|xml|ya?ml)$`),
	}
)

func reuseIgnoredDir(dir string) bool {
	for _, d := range strings.Split(strings.Trim(dir, "/"), "/") {
		if slices.Contains(reuseIgnoredDirs, d) {
			return true
		}
	}
	return false
}

func reuseIgnoredPath(rel string) bool {
	dir, base := path.Split(rel)
	if reuseIgnoredDir(dir) {
		return true
	}
	for _, re := range reuseIgnoredFiles {
		if re.MatchString(base) {
			return true
		}
	}
	return false
}

// trackedFiles lists the tracked files as `git ls-files -s -z` does, without submodules.
func trackedFiles(ctx context.Context, repo string) ([]string, error) {
	out, err := pipe.Output(ctx, nil, pipe.Command{Name: "git", Args: []string{"-C", repo, "ls-files", "-s", "-z"}})
	if err != nil {
		return nil, err
	}
	var files []string
	for _, rec := range strings.Split(string(out), "\x00") {
		meta, name, ok := strings.Cut(rec, "\t")
		if !ok {
			continue
		}
		if strings.HasPrefix(meta, "160000 ") { // a submodule
			continue
		}
		if len(files) > 0 && files[len(files)-1] == name { // unmerged: one line per stage
			continue
		}
		files = append(files, name)
	}
	return files, nil
}

func reuseLint(ctx context.Context, repo string) (*reuseResult, error) {
	tracked, err := trackedFiles(ctx, repo)
	if err != nil {
		return nil, fmt.Errorf("git ls-files: %w", err)
	}
	if len(tracked) == 0 {
		return nil, errors.New("no tracked files; not looking at a repository")
	}
	res := &reuseResult{
		used: map[string][]string{}, licenseFiles: map[string]string{}, bad: map[string][]string{},
		missing: map[string][]string{},
	}

	regular := func(rel string) bool {
		st, err := os.Lstat(filepath.Join(repo, filepath.FromSlash(rel)))
		return err == nil && st.Mode().IsRegular()
	}

	// REUSE.toml files (any depth) and the legacy dep5.
	var tomls []*reuseTOML
	var covered, licenseDir []string
	for _, f := range tracked {
		switch {
		case f == ".reuse/dep5":
			res.fatal = append(res.fatal, ".reuse/dep5 is not supported: convert it to REUSE.toml (`reuse convert-dep5`, or by hand)")
			continue
		case strings.HasPrefix(f, "LICENSES/"):
			if regular(f) {
				licenseDir = append(licenseDir, f)
			}
			continue
		case path.Base(f) == "REUSE.toml" && !reuseIgnoredDir(path.Dir(f)) && regular(f):
			b, err := os.ReadFile(filepath.Join(repo, filepath.FromSlash(f)))
			if err != nil {
				res.fatal = append(res.fatal, fmt.Sprintf("%s: %v", f, err))
				continue
			}
			t, err := parseReuseTOML(f, b)
			if err != nil {
				res.fatal = append(res.fatal, err.Error())
				continue
			}
			tomls = append(tomls, t)
			res.reuseTOMLFiles = append(res.reuseTOMLFiles, f)
		}
		if reuseIgnoredPath(f) {
			continue
		}
		st, err := os.Lstat(filepath.Join(repo, filepath.FromSlash(f)))
		if err != nil || !st.Mode().IsRegular() || st.Size() == 0 {
			continue // deleted in the working tree, a symlink, or empty
		}
		covered = append(covered, f)
	}
	// Outermost first: an override in an outer REUSE.toml beats everything inside it.
	slices.SortStableFunc(tomls, func(a, b *reuseTOML) int { return len(a.dir) - len(b.dir) })
	res.coveredFiles = len(covered)

	// Read every covered file's own information in parallel.
	p := pipe.New(ctx)
	read := pipe.ParallelMap(p, pipe.Slice(p, covered...), runtime.NumCPU(), func(_ context.Context, rel string) (reuseFile, error) {
		return reuseReadFile(repo, rel, tomls), nil
	})
	files, err := pipe.Collect(p, read)
	if err != nil {
		return nil, err
	}
	slices.SortFunc(files, func(a, b reuseFile) int { return strings.Compare(a.path, b.path) })
	res.files = files

	// Annotations' own expressions must parse too.
	for _, t := range tomls {
		for _, a := range t.annotations {
			for _, e := range a.info.exprs {
				if _, err := parseSPDX(e); err != nil {
					res.invalid = append(res.invalid, fmt.Sprintf("%s (annotation %d): %q: %v", t.file, a.index, e, err))
				}
			}
		}
	}

	for _, f := range files {
		if f.readErr != nil {
			res.readErrors = append(res.readErrors, fmt.Sprintf("%s: %v", f.path, f.readErr))
			continue
		}
		for _, s := range f.badExpr {
			res.invalid = append(res.invalid, f.path+": "+s)
		}
		if f.info.hasCopyright() {
			res.withCopyright++
		} else {
			res.noCopyright = append(res.noCopyright, f.path)
		}
		if f.info.hasLicense() {
			res.withLicense++
		} else {
			res.noLicense = append(res.noLicense, f.path)
		}
		for _, e := range f.info.exprs {
			ids, err := parseSPDX(e)
			if err != nil {
				continue // reported above, where it was read
			}
			for _, id := range ids {
				if !slices.Contains(res.used[id], f.path) {
					res.used[id] = append(res.used[id], f.path)
				}
			}
		}
	}

	// LICENSES/.
	for _, f := range licenseDir {
		base := path.Base(f)
		if strings.HasSuffix(base, ".license") {
			continue
		}
		ext := path.Ext(base)
		id := strings.TrimSuffix(base, ext)
		if ext == "" {
			res.noExtension = append(res.noExtension, f)
			id = base
		}
		if prev, dup := res.licenseFiles[id]; dup {
			res.fatal = append(res.fatal, fmt.Sprintf("LICENSES/ has two texts for %s: %s and %s", id, prev, f))
			continue
		}
		res.licenseFiles[id] = f
	}

	list := spdxList()
	check := func(id, where string) {
		switch {
		case licenseRefRe.MatchString(id):
		case list.known(id):
			if list.deprecated[id] && !slices.Contains(res.deprecated, id) {
				res.deprecated = append(res.deprecated, id)
			}
		default:
			if !slices.Contains(res.bad[id], where) {
				res.bad[id] = append(res.bad[id], where)
			}
		}
	}
	for id, fs := range res.used {
		for _, f := range fs {
			check(id, f)
		}
		if _, ok := res.licenseFiles[id]; !ok {
			res.missing[id] = fs
		}
	}
	for id, f := range res.licenseFiles {
		check(id, f)
		if _, ok := res.used[id]; !ok {
			res.unused = append(res.unused, id)
		}
	}
	slices.Sort(res.deprecated)
	slices.Sort(res.unused)
	slices.Sort(res.invalid)
	slices.Sort(res.fatal)
	return res, nil
}

var licenseRefRe = regexp.MustCompile(`^LicenseRef-[A-Za-z0-9.-]+$`)

// reuseReadFile reads one file's information (from its .license sidecar if it has one) and
// resolves it against the REUSE.toml annotations.
func reuseReadFile(repo, rel string, tomls []*reuseTOML) reuseFile {
	out := reuseFile{path: rel}

	// 1. REUSE.toml: the last matching annotation of each REUSE.toml, outermost first.
	var matched []*reuseAnnotation
	for _, t := range tomls {
		if a := t.match(rel); a != nil {
			matched = append(matched, a)
		}
	}
	for _, a := range matched {
		if a.precedence == precOverride { // the outermost override wins, the file is not read
			out.info = a.info
			return out
		}
	}

	// 2. The file's own information.
	src := filepath.Join(repo, filepath.FromSlash(rel))
	if st, err := os.Stat(src + ".license"); err == nil && st.Mode().IsRegular() {
		src += ".license"
	}
	b, err := os.ReadFile(src)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) { // gone between ls-files and now: not a read error
			out.readErr = err
		}
		return out
	}
	own, bad := extractReuseInfo(string(b))
	out.badExpr = bad

	// 3. closest: the innermost matching closest annotation fills what the file lacks.
	//    aggregate: every matching aggregate annotation adds to what the file has.
	info := own
	for i := len(matched) - 1; i >= 0; i-- {
		if a := matched[i]; a.precedence == precClosest {
			if !info.hasCopyright() {
				info.copyrights = a.info.copyrights
			}
			if !info.hasLicense() {
				info.exprs = a.info.exprs
			}
			break
		}
	}
	for _, a := range matched {
		if a.precedence == precAggregate {
			info = info.merge(a.info)
		}
	}
	out.info = info
	return out
}

// The header patterns of reuse's extract.py. A copyright line may sit anywhere in a line
// (after a comment leader); the licence line's trailing comment closers are not part of the
// expression, and neither is the end of a string literal that writes a header into another
// file (`printf '... MIT\n'`), which reuse also accepts.
var (
	copyrightRe = regexp.MustCompile(`(SPDX-(File|Snippet)CopyrightText:|Copyright(\s?\([cC]\))?|©)\s`)
	licenseTag  = "SPDX-License-" + "Identifier:"
	closers     = []string{"*/", "-->", "--}}", "*}", "#}", "]]", `\n`, `"`, "'"}
)

const (
	ignoreStart = "REUSE-" + "IgnoreStart"
	ignoreEnd   = "REUSE-" + "IgnoreEnd"
)

// filterIgnoreBlocks drops every REUSE-IgnoreStart … REUSE-IgnoreEnd block; a start with no
// end drops the rest of the text.
func filterIgnoreBlocks(text string) string {
	var b strings.Builder
	for {
		i := strings.Index(text, ignoreStart)
		if i < 0 {
			b.WriteString(text)
			return b.String()
		}
		b.WriteString(text[:i])
		rest := text[i+len(ignoreStart):]
		j := strings.Index(rest, ignoreEnd)
		if j < 0 {
			return b.String()
		}
		text = rest[j+len(ignoreEnd):]
	}
}

// extractReuseInfo reads the SPDX tags out of a file's text. Expressions that do not parse
// are returned as problems, not as licences.
func extractReuseInfo(text string) (reuseInfo, []string) {
	var info reuseInfo
	var bad []string
	for _, line := range strings.Split(filterIgnoreBlocks(text), "\n") {
		line = strings.TrimSuffix(line, "\r")
		if i := strings.Index(line, licenseTag); i >= 0 {
			rest := line[i+len(licenseTag):]
			if rest != "" && (rest[0] == ' ' || rest[0] == '\t') {
				expr := trimClosers(rest)
				if expr != "" {
					if _, err := parseSPDX(expr); err != nil {
						bad = append(bad, fmt.Sprintf("%q: %v", expr, err))
					} else if !slices.Contains(info.exprs, expr) {
						info.exprs = append(info.exprs, expr)
					}
				}
			}
		}
		if loc := copyrightRe.FindStringIndex(line); loc != nil {
			c := trimClosers(line[loc[0]:])
			if !slices.Contains(info.copyrights, c) {
				info.copyrights = append(info.copyrights, c)
			}
		}
	}
	return info, bad
}

func trimClosers(s string) string {
	for {
		t := strings.TrimSpace(s)
		for _, c := range closers {
			t = strings.TrimSuffix(t, c)
		}
		if t == s {
			return t
		}
		s = t
	}
}

func sortedKeys[V any](m map[string]V) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	slices.Sort(ks)
	return ks
}

// reuseReport writes the report in the shape of `reuse lint`'s.
func reuseReport(r *reuseResult, w io.Writer) {
	section := func(title string) { fmt.Fprintf(w, "# %s\n\n", title) }
	list := func(items []string) {
		for _, s := range items {
			fmt.Fprintf(w, "* %s\n", s)
		}
		fmt.Fprintln(w)
	}
	if len(r.fatal) > 0 {
		section("CONFIGURATION ERRORS")
		list(r.fatal)
	}
	if len(r.bad) > 0 {
		section("BAD LICENSES")
		for _, id := range sortedKeys(r.bad) {
			fmt.Fprintf(w, "'%s' found in:\n", id)
			list(r.bad[id])
		}
	}
	if len(r.deprecated) > 0 {
		section("DEPRECATED LICENSES")
		fmt.Fprintln(w, "The following licenses are deprecated by SPDX:")
		list(r.deprecated)
	}
	if len(r.noExtension) > 0 {
		section("LICENSES WITHOUT FILE EXTENSION")
		fmt.Fprintln(w, "The following licenses have no file extension:")
		list(r.noExtension)
	}
	if len(r.missing) > 0 {
		section("MISSING LICENSES")
		for _, id := range sortedKeys(r.missing) {
			fmt.Fprintf(w, "'%s' found in:\n", id)
			list(r.missing[id])
		}
	}
	if len(r.unused) > 0 {
		section("UNUSED LICENSES")
		fmt.Fprintln(w, "The following licenses are not used:")
		list(r.unused)
	}
	if len(r.readErrors) > 0 {
		section("READ ERRORS")
		fmt.Fprintln(w, "Could not read:")
		list(r.readErrors)
	}
	if len(r.invalid) > 0 {
		section("INVALID SPDX LICENSE EXPRESSIONS")
		list(r.invalid)
	}
	if len(r.noCopyright)+len(r.noLicense) > 0 {
		section("MISSING COPYRIGHT AND LICENSING INFORMATION")
		var both, onlyC, onlyL []string
		noL := map[string]bool{}
		for _, f := range r.noLicense {
			noL[f] = true
		}
		noC := map[string]bool{}
		for _, f := range r.noCopyright {
			noC[f] = true
			if noL[f] {
				both = append(both, f)
			} else {
				onlyC = append(onlyC, f)
			}
		}
		for _, f := range r.noLicense {
			if !noC[f] {
				onlyL = append(onlyL, f)
			}
		}
		if len(both) > 0 {
			fmt.Fprintln(w, "The following files have no copyright and licensing information:")
			list(both)
		}
		if len(onlyC) > 0 {
			fmt.Fprintln(w, "The following files have no copyright information:")
			list(onlyC)
		}
		if len(onlyL) > 0 {
			fmt.Fprintln(w, "The following files have no licensing information:")
			list(onlyL)
		}
	}
	section("SUMMARY")
	fmt.Fprintf(w, "* Bad licenses: %s\n", joinOrZero(sortedKeys(r.bad)))
	fmt.Fprintf(w, "* Deprecated licenses: %s\n", joinOrZero(r.deprecated))
	fmt.Fprintf(w, "* Licenses without file extension: %s\n", joinOrZero(r.noExtension))
	fmt.Fprintf(w, "* Missing licenses: %s\n", joinOrZero(sortedKeys(r.missing)))
	fmt.Fprintf(w, "* Unused licenses: %s\n", joinOrZero(r.unused))
	fmt.Fprintf(w, "* Used licenses: %s\n", strings.Join(sortedKeys(r.used), ", "))
	fmt.Fprintf(w, "* Read errors: %d\n", len(r.readErrors))
	fmt.Fprintf(w, "* Invalid SPDX License Expressions: %d\n", len(r.invalid))
	fmt.Fprintf(w, "* Files with copyright information: %d / %d\n", r.withCopyright, r.coveredFiles)
	fmt.Fprintf(w, "* Files with license information: %d / %d\n", r.withLicense, r.coveredFiles)
	fmt.Fprintln(w)
	if r.problems() == 0 {
		fmt.Fprintf(w, "Congratulations! Your project is compliant with version %s of the REUSE Specification :-)\n", reuseSpec)
	} else {
		fmt.Fprintf(w, "Unfortunately, your project is not compliant with version %s of the REUSE Specification :-(\n", reuseSpec)
	}
}

func joinOrZero(s []string) string {
	if len(s) == 0 {
		return "0"
	}
	return strings.Join(s, ", ")
}
