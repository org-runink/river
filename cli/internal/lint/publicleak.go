// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

package lint

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/org-runink/river/pkg/pipe"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// public-leak — this repository is public (AGENTS.md, "Public repository"): it names no
// downstream product, private repository, internal address, runner label, target machine or
// personal mailbox. The lint keeps that true. Every tracked text file outside the vendored
// trees is matched line by line against leakRules; a hit fails unless an entry in
// publicLeakAllowFile allows exactly that rule, path and matched text, with a reason.
// An allow entry that no longer matches anything fails too, so the list only holds live
// exceptions.
//
// --history runs the same rules over every commit reachable from any ref (`git log --all`):
// the lines each commit ADDED, its message, and its author and committer identities. History
// cannot be fixed by a commit, so this mode reports and fails but is not part of Tier 1; it is
// the check the owner runs before publishing (docs/PUBLICATION.md).
//
// What this does NOT prove: that no leak exists in a form the rules do not describe (a
// paraphrase, an unlisted product, a secret; gitleaks in CI covers secrets), or in binary
// files (artwork, fonts), which are skipped.

const publicLeakAllowFile = "scripts/public-leak.allow"

// leakRule is one kind of leak: an id used by the allow-list, a description, and a finder
// that returns every match on a line.
type leakRule struct {
	id, what string
	find     func(line string) []string
}

// The names a public file must not carry are kept here as SHA-256 digests, never as words,
// so this file does not itself publish what it keeps out. (Digests of short words can be
// reversed with a dictionary; the point is that no plain name sits in the tree, not secrecy.)
// Add a term with `printf %s TERM | sha256sum`; which spelling to hash is said per set.
var (
	// leakProductWords: a downstream product's name as a word, hashed in the exact case it is
	// written (upper case, or the capitalised form of a name that is not an English word). The
	// lower-case English words some products are named after stay usable.
	leakProductWords = digestSet(
		"215db504e513fdefdf02fc110ce55f5b58ba0f39f4d5ed9dff3aed1ac594c6c0",
		"64ab2b69dba510be526608f4e67142d34068fb7cfd6771529a7c54b5cd7a0675",
		"66f7c3f38607d52236f747252c8feba4866900d6c174ecfd2e0dffbc608b1623",
		"1b36eeed3b6bef2d211b3d6b813e5c4af8e44afe76f0c2077bcb2d9bbaf71005",
		"9d77a24d0f4c91a2e968ca607e49dd3b15d5489f66a5bc81fa72803b32443419",
		"52291bc56328bfc84798003016e4dc8c91bf04dc037b051fac652497cec2e12d",
		"fc9cdf5b6083ffc06e3a290a4fdabfb3091ca9948c8246edcec86d37b25c9b2c",
	)
	// leakProductNames: a product in a form only a product takes, hashed lower case: the X of
	// "Runink X" and of the host X.runink.org.
	leakProductNames = digestSet(
		"0282d9b79f42c74c1550b20ff2dd16aafc3fe5d8ae9a00b2f66996d0ae882775",
		"41b589ea15f2d94a20fbdde82b0ed6988fb46742a7cdb44e396b0dd4747ffbc2",
		"970ec274ca867815174ebe4eff19282000f9495a6c7254e94991d1fb4dc3df30",
		"71b41d6dd48dc58eba8f5cf9edf30fef6597fdf285a521bb8fcbad4b3d50887d",
		"d1d2949eaad15372c62102edb9880901481f043fa59f4bd4bc0e96e817cae2aa",
		"40b7335d644b2fd749c44444918de14f484b2362a0006e4501014f3286b2f176",
		"0d45f5fd462b8c70bffb10021ac1bcff3f58f29b1faf7568595095427d42812c",
		"c332ca4351c7e559bc72a394d0344b68f4084dfe788194d0aa38452408ff4b61",
		"1fd94ddb08adadce71cdb8a80a30d89905ec878e87a6b939a37f99d51108f5d3",
	)
	// leakPrivateRepos: the organisation's private repositories, hashed lower case: the X of
	// org-runink/X.
	leakPrivateRepos = digestSet(
		"0d45f5fd462b8c70bffb10021ac1bcff3f58f29b1faf7568595095427d42812c",
		"0282d9b79f42c74c1550b20ff2dd16aafc3fe5d8ae9a00b2f66996d0ae882775",
		"41b589ea15f2d94a20fbdde82b0ed6988fb46742a7cdb44e396b0dd4747ffbc2",
		"970ec274ca867815174ebe4eff19282000f9495a6c7254e94991d1fb4dc3df30",
		"d1d2949eaad15372c62102edb9880901481f043fa59f4bd4bc0e96e817cae2aa",
		"71b41d6dd48dc58eba8f5cf9edf30fef6597fdf285a521bb8fcbad4b3d50887d",
		"df6b07176a9b17cc4c9afc257bd404732e7d09b76436c7890f7b7be14e579794",
		"3069bc5a4993b81fc6b4c6eba8321b7cbfbf5b10c8e4e7f83d95b79bdd82b516",
		"d3ef7de562f9a4a34a9a0b05a112955fdecdd0102c3faae5eeb03a195091a5e4",
		"0c95c7ece1ce1a9750275ef1c6d7ad6b278f70d66592207783ffe7d58474cc01",
		"824d80d71985f082a26997a8db88b5d1dd45b777d73585d03d236303e21bde97",
		"d30ca7a7a32bf5772dc5eb2a2e7bd35737eff795ad74f2479b359716b59abdfa",
		"5d2d3ceb7abe552344276d47d36a8175b7aeb250a9bf0bf00e850cd23ecf2e43",
		"925733dafd743699fa17409329abdb2728da89ba5736e44ad0fd4a67836e9f9c",
		"5d58d41913d9fea4e42cecd7a5d1b692aa6d0d792977ad7c6d6bb1507e8f3dbd",
		"71c6f2f5e422a76ab2803802b7ffe0b3fab91f51ccad5b1dbbba22e9c262d376",
		"4b5e57f6eb2f42b9039b3d1e13929295f231749c510cbe341cd68036d9af97e2",
		"a1cb100f57e971cacf269e7c26e4630a25a8e9d4bdd35e32df1a80b66b896254",
		"d195d36bd79da7df853b78f0b539b75f002819330c13b6438bbbe89a8f4f267a",
		"69c248ffbd339ea5a88feaddd56713ae65a473c01c75a07bc8ec1a32fd228e63",
		"14cac63cd6e0d70e9e6b79ba841681ba50805d642440ac852fb2778bc633a114",
		"bdcc6a2a85f645f62724fe8dafbf0581cb0c1d65f6a76cb2985a9172e31a473c",
	)
	// leakRunnerLabels: self-hosted runner labels, hashed lower case, hyphens included.
	leakRunnerLabels = digestSet(
		"81169cd4b8b22bae465e31676918fa1dfe69246525ff8319997d318e9ec73f9b",
		"9ae6d0937c90d34e9bd166c8590de0f6b67ee2affb7d3a72c2e18a515eb1fd0c",
	)
	// leakTargetHW: the private deployment's hardware vendor, hashed lower case.
	leakTargetHW = digestSet(
		"0d52825aa423b22991fc85cea59f82ba2562b349d124e8b0186b1603bd7d46ff",
	)
)

var (
	leakWordRE = regexp.MustCompile(`[A-Za-z][A-Za-z0-9]*`)
	// leakWholeWordRE skips a word inside an identifier (a kernel option names hardware vendors).
	leakWholeWordRE = regexp.MustCompile(`\b[A-Za-z][A-Za-z0-9]*\b`)
	leakLabelRE     = regexp.MustCompile(`(?i)\b[a-z0-9]+(?:-[a-z0-9]+)+\b`)
	leakRuninkRE    = regexp.MustCompile(`(?i)\brunink[ _]([a-z0-9]+)\b`)
	leakHostRE      = regexp.MustCompile(`(?i)\b([a-z0-9-]+)\.runink\.org\b`)
	leakRepoRE      = regexp.MustCompile(`(?i)\borg-runink/([a-z0-9._-]+)`)
	leakSelfHosRE   = regexp.MustCompile(`(?i)runs-on:[^#]*self-hosted|\[\s*self-hosted\s*,`)
	leakLanRE       = regexp.MustCompile(`(?i)\brunink\.lan\b`)
)

// leakRules are the publication rules (AGENTS.md, "Public repository").
var leakRules = []leakRule{
	{"product", "a downstream product name", func(l string) []string {
		out := wordsIn(l, leakWordRE, leakProductWords, false)
		return append(out, groupsIn(l, leakRuninkRE, leakProductNames)...)
	}},
	{"product-host", "a downstream product's host name", func(l string) []string {
		return append(groupsIn(l, leakHostRE, leakProductNames), leakLanRE.FindAllString(l, -1)...)
	}},
	{"private-repo", "a private repository", func(l string) []string {
		return groupsIn(l, leakRepoRE, leakPrivateRepos)
	}},
	{"internal-ip", "a private-range (RFC 1918) IPv4 address or a tunnel-broker IPv6 prefix",
		reFind(`\b(10\.\d{1,3}|172\.(1[6-9]|2\d|3[01])|192\.168)\.\d{1,3}\.\d{1,3}\b|(?i)\b2001:470:[0-9a-f]{1,4}:`)},
	{"ula", "an IPv6 unique-local prefix (only the documented example and test prefixes)",
		reFind(`(?i)\bfd[0-9a-f]{2}:[0-9a-f]{0,4}`)},
	{"runner-label", "a self-hosted runner label", func(l string) []string {
		return append(wordsIn(l, leakLabelRE, leakRunnerLabels, true), leakSelfHosRE.FindAllString(l, -1)...)
	}},
	{"target-hw", "the private deployment's hardware", func(l string) []string {
		return wordsIn(l, leakWholeWordRE, leakTargetHW, true)
	}},
	{"server-name", "the downstream server image's name",
		reFind(`(?i)\brunink[ -]server\b`)},
	{"email", "an e-mail address that is not a project alias, an example or a hosting no-reply", func(l string) []string {
		var out []string
		for _, m := range leakEmailRE.FindAllString(l, -1) {
			if !leakEmailOK.MatchString(m) {
				out = append(out, m)
			}
		}
		return out
	}},
}

var leakEmailRE = regexp.MustCompile(`(?i)\b[a-z0-9._%+-]+@[a-z0-9-]+(\.[a-z0-9-]+)*\.[a-z]{2,}\b`)

// leakEmailOK are the addresses any file or commit may carry: the project aliases, the
// reserved example domains (RFC 2606, and the .invalid/.test/.example TLDs of RFC 6761) and the hosting services' no-reply identities.
var leakEmailOK = regexp.MustCompile(`(?i)^(security|conduct)@runink\.org$|@([a-z0-9-]+\.)*(example\.(org|com|net)|invalid|test|example)$|@users\.noreply\.github\.com$|^(noreply|support)@github\.com$|^noreply@anthropic\.com$`)

// reFind is a finder for a plain pattern.
func reFind(pattern string) func(string) []string {
	re := regexp.MustCompile(pattern)
	return func(l string) []string { return re.FindAllString(l, -1) }
}

// digestSet builds a set of hex SHA-256 digests.
func digestSet(hex ...string) map[string]bool {
	s := make(map[string]bool, len(hex))
	for _, h := range hex {
		s[h] = true
	}
	return s
}

func leakDigest(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// wordsIn returns the matches of re whose digest (lower-cased first when fold) is in set.
func wordsIn(line string, re *regexp.Regexp, set map[string]bool, fold bool) []string {
	var out []string
	for _, w := range re.FindAllString(line, -1) {
		k := w
		if fold {
			k = strings.ToLower(w)
		}
		if len(k) <= 40 && set[leakDigest(k)] {
			out = append(out, w)
		}
	}
	return out
}

// groupsIn returns the whole matches of re whose first group, lower case (and without a
// ".git" suffix), has its digest in set.
func groupsIn(line string, re *regexp.Regexp, set map[string]bool) []string {
	var out []string
	for _, m := range re.FindAllStringSubmatch(line, -1) {
		g := strings.TrimSuffix(strings.ToLower(m[1]), ".git")
		if set[leakDigest(g)] {
			out = append(out, m[0])
		}
	}
	return out
}

// publicLeakSkip are tracked trees that are not ours: vendored upstream code.
var publicLeakSkip = []string{"cli/vendor/", "website/_vendor/"}

// leakAllow is one allow-list entry: rule, path glob, matched text (case-insensitive; "*" is
// any) and the reason.
type leakAllow struct {
	rule, glob, match, reason string
	line                      int
	used                      bool
}

func init() { extraLints = append(extraLints, publicLeakCmd) }

func publicLeakCmd(v *viper.Viper) *cobra.Command {
	c := &cobra.Command{
		Use:   "public-leak",
		Short: "no downstream product, private repo, internal address, runner label or personal mailbox in the public tree",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if v.GetBool("history") {
				return PublicLeakHistory(cmd.Context(), v.GetString("repo"), cmd.ErrOrStderr(), cmd.OutOrStdout())
			}
			return PublicLeak(cmd.Context(), v.GetString("repo"), cmd.ErrOrStderr(), cmd.OutOrStdout())
		},
	}
	c.Flags().Bool("history", false, "scan every commit reachable from any ref (added lines, messages, identities) instead of the tree")
	return c
}

// PublicLeak runs the lint over the tracked tree of repo.
func PublicLeak(ctx context.Context, repo string, errw, outw io.Writer) error {
	r := &report{name: "lint-public-leak", w: errw}
	allow, err := readLeakAllow(filepath.Join(repo, publicLeakAllowFile))
	if err != nil {
		return fmt.Errorf("lint-public-leak: %w", err)
	}
	files, err := gitLines(ctx, repo, "ls-files")
	if err != nil {
		return fmt.Errorf("lint-public-leak: git ls-files: %w", err)
	}
	if len(files) < 100 {
		return fmt.Errorf("lint-public-leak: only %d tracked files; not looking at this repository", len(files))
	}

	scanned := 0
	for _, f := range files {
		if leakSkipped(f) {
			continue
		}
		p := filepath.Join(repo, f)
		st, err := os.Lstat(p)
		if err != nil || !st.Mode().IsRegular() {
			continue // deleted in the working tree, a symlink, or a submodule
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return fmt.Errorf("lint-public-leak: %w", err)
		}
		if isBinary(b) {
			continue
		}
		scanned++
		sc := bufio.NewScanner(bytes.NewReader(b))
		sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
		for n := 1; sc.Scan(); n++ {
			for _, h := range leakHits(sc.Text()) {
				if !allowed(allow, h.rule, f, h.match) {
					r.fail("%s:%d: %s (%s) %q:\n    %s", f, n, h.rule, h.what, h.match, strings.TrimSpace(sc.Text()))
				}
			}
		}
		if err := sc.Err(); err != nil {
			return fmt.Errorf("lint-public-leak: %s: %w", f, err)
		}
	}
	if scanned < 100 {
		return fmt.Errorf("lint-public-leak: only %d text files scanned; not looking at this repository", scanned)
	}
	for _, a := range allow {
		if !a.used {
			r.fail("%s:%d: allow entry %q %q %q matches nothing any more; remove it", publicLeakAllowFile, a.line, a.rule, a.glob, a.match)
		}
	}
	if err := r.err(); err != nil {
		return err
	}
	fmt.Fprintf(outw, "lint-public-leak: OK (%d text files, %d rules, %d allow entries)\n", scanned, len(leakRules), len(allow))
	return nil
}

// PublicLeakHistory runs the rules over every commit reachable from any ref: the lines each
// commit added (outside the vendored trees), its message and its identities. The allow-list
// applies to added lines by path; messages and identities have no allow entries.
func PublicLeakHistory(ctx context.Context, repo string, errw, outw io.Writer) error {
	r := &report{name: "lint-public-leak --history", w: errw}
	allow, err := readLeakAllow(filepath.Join(repo, publicLeakAllowFile))
	if err != nil {
		return fmt.Errorf("lint-public-leak: %w", err)
	}
	const mark = "\x01COMMIT "
	args := []string{"-C", repo, "log", "--all", "-p", "--no-color", "--no-ext-diff", "--no-renames",
		"--format=%x01COMMIT %H%n%an <%ae>%n%cn <%ce>%n%B%n%x01END", "--", "."}
	for _, s := range publicLeakSkip {
		args = append(args, ":(exclude)"+strings.TrimSuffix(s, "/"))
	}
	out, err := pipe.Output(ctx, nil, pipe.Cmd("git", args...))
	if err != nil {
		return fmt.Errorf("lint-public-leak: git log: %w", err)
	}

	type key struct{ commit, where, rule, match string }
	seen := map[key]int{}
	var order []key
	add := func(k key) {
		if seen[k] == 0 {
			order = append(order, k)
		}
		seen[k]++
	}
	commits := 0
	var commit, file string
	inHeader := false
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	headerLine := 0
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, mark):
			commit, file, inHeader, headerLine = strings.TrimPrefix(line, mark)[:12], "", true, 0
			commits++
			continue
		case inHeader && line == "\x01END":
			inHeader = false
			continue
		case inHeader:
			headerLine++
			where := "(message)"
			if headerLine <= 2 {
				where = "(identity)"
			}
			for _, h := range leakHits(line) {
				add(key{commit, where, h.rule, strings.ToLower(h.match)})
			}
			continue
		case strings.HasPrefix(line, "diff --git "):
			if i := strings.LastIndex(line, " b/"); i >= 0 {
				file = line[i+3:]
			}
			continue
		case strings.HasPrefix(line, "+++ "), !strings.HasPrefix(line, "+"):
			continue
		}
		for _, h := range leakHits(line[1:]) {
			if !allowed(allow, h.rule, file, h.match) {
				add(key{commit, file, h.rule, strings.ToLower(h.match)})
			}
		}
	}
	if err := sc.Err(); err != nil {
		return fmt.Errorf("lint-public-leak: git log: %w", err)
	}
	if commits == 0 {
		return fmt.Errorf("lint-public-leak: git log listed no commit; not looking at a repository")
	}
	for _, k := range order {
		r.fail("%s %s: %s %q (%d line(s))", k.commit, k.where, k.rule, k.match, seen[k])
	}
	if err := r.err(); err != nil {
		return err
	}
	fmt.Fprintf(outw, "lint-public-leak --history: OK (%d commits)\n", commits)
	return nil
}

type leakHit struct{ rule, what, match string }

// leakHits returns every rule match on one line, minus the addresses any file may carry.
func leakHits(line string) []leakHit {
	var hits []leakHit
	for _, rule := range leakRules {
		for _, m := range rule.find(line) {
			hits = append(hits, leakHit{rule.id, rule.what, m})
		}
	}
	return hits
}

func leakSkipped(f string) bool {
	for _, s := range publicLeakSkip {
		if strings.HasPrefix(f, s) {
			return true
		}
	}
	return false
}

// isBinary is git's own test: a NUL byte in the first 8000 bytes.
func isBinary(b []byte) bool {
	return bytes.IndexByte(b[:min(len(b), 8000)], 0) >= 0
}

// allowed reports whether an entry allows match of rule in file, and marks it used.
func allowed(allow []*leakAllow, rule, file, match string) bool {
	ok := false
	for _, a := range allow {
		if a.rule != rule && a.rule != "*" {
			continue
		}
		if !globMatch(a.glob, file) {
			continue
		}
		if a.match != "*" && !strings.EqualFold(a.match, match) {
			continue
		}
		a.used, ok = true, true
	}
	return ok
}

// globMatch is path.Match, plus "**" for every path and a trailing "/**" for a whole tree.
func globMatch(glob, file string) bool {
	if glob == "**" {
		return true
	}
	if dir, ok := strings.CutSuffix(glob, "/**"); ok {
		return strings.HasPrefix(file, dir+"/")
	}
	m, err := path.Match(glob, file)
	return err == nil && m
}

// readLeakAllow parses the allow-list: `rule | path-glob | match | reason`, one per line;
// blank lines and lines starting with # are comments. Every field is required.
func readLeakAllow(p string) ([]*leakAllow, error) {
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	ids := []string{"*"}
	for _, r := range leakRules {
		ids = append(ids, r.id)
	}
	var out []*leakAllow
	for i, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		f := strings.Split(line, "|")
		if len(f) != 4 {
			return nil, fmt.Errorf("%s:%d: want `rule | path-glob | match | reason`", publicLeakAllowFile, i+1)
		}
		for j := range f {
			f[j] = strings.TrimSpace(f[j])
			if f[j] == "" {
				return nil, fmt.Errorf("%s:%d: field %d is empty (every entry needs a reason)", publicLeakAllowFile, i+1, j+1)
			}
		}
		if !slices.Contains(ids, f[0]) {
			return nil, fmt.Errorf("%s:%d: unknown rule %q (rules: %s)", publicLeakAllowFile, i+1, f[0], strings.Join(ids, ", "))
		}
		if _, err := path.Match(strings.TrimSuffix(f[1], "/**"), ""); err != nil {
			return nil, fmt.Errorf("%s:%d: bad glob %q: %v", publicLeakAllowFile, i+1, f[1], err)
		}
		out = append(out, &leakAllow{rule: f[0], glob: f[1], match: f[2], reason: f[3], line: i + 1})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%s: no entries (the lint's own files at least must be listed)", publicLeakAllowFile)
	}
	return out, nil
}
