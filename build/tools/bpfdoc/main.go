// SPDX-FileCopyrightText: 2018-2019 Netronome Systems, Inc.
// SPDX-FileCopyrightText: 2021 Isovalent, Inc.
// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: GPL-2.0-only

// Command bpfdoc is a Go port of the Linux kernel's scripts/bpf_doc.py, limited to the one
// mode the kernel build runs, so that linux-runink builds with no Python interpreter.
//
// The kernel's tools/lib/bpf/Makefile generates libbpf's bpf_helper_defs.h with
//
//	$(srctree)/scripts/bpf_doc.py --header --file tools/include/uapi/linux/bpf.h
//
// and libbpf is built for tools/bpf/resolve_btfids (CONFIG_DEBUG_INFO_BTF) and for bpftool's
// bootstrap (the PKGBUILD's `make -C tools/bpf/bpftool vmlinux.h`). The kernel PKGBUILD
// compiles this single file with `go build` (standard library only) and installs the binary
// as scripts/bpf_doc.py in the unpacked tree, so the kernel Makefiles run it unchanged.
//
// It is a port, not a rewrite: the parser follows bpf_doc.py's HeaderParser step by step,
// with every consistency check (a helper missing from ___BPF_FUNC_MAPPER, descriptions out of
// enum order, duplicated values, the description/define count check, unknown types), and
// each Python regular expression is re-implemented by hand in Python's matching order
// (greedy, lazy and alternation backtracking), because the captured text depends on it. Its
// output must be BYTE-IDENTICAL to bpf_doc.py's; main_test.go checks it against a
// bpf_helper_defs.h that upstream bpf_doc.py generated from the pinned kernel's bpf.h.
//
// Deliberate differences from bpf_doc.py (none changes the bytes of a successful run):
//   - Only --header for the "helpers" target is implemented. The RST and JSON outputs (man
//     pages, bpftool docs, selftests docs) are not built for the image; asking for them
//     exits 2 with a message.
//   - Output is written only when generation succeeds. bpf_doc.py prints helper by helper
//     and leaves a truncated header behind when it dies on an unknown type.
//   - Errors are one-line messages on stderr (exit 1), not Python tracebacks.
//
// Upstream: scripts/bpf_doc.py of Linux 7.2.7 (GPL-2.0-only). When the kernel pin moves, diff
// that script against the previous series and port any change to the header path here; the
// test fixture moves with it (testdata/README).
package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

const helpersDocStart = "Start of BPF helper function descriptions:"

// attrs is ATTRS in bpf_doc.py: mnemonic -> compiler attribute.
var attrs = map[string]string{"__bpf_fastcall": "bpf_fastcall"}

var typeFwds = []string{
	"struct bpf_fib_lookup",
	"struct bpf_sk_lookup",
	"struct bpf_perf_event_data",
	"struct bpf_perf_event_value",
	"struct bpf_pidns_info",
	"struct bpf_redir_neigh",
	"struct bpf_sock",
	"struct bpf_sock_addr",
	"struct bpf_sock_ops",
	"struct bpf_sock_tuple",
	"struct bpf_spin_lock",
	"struct bpf_sysctl",
	"struct bpf_tcp_sock",
	"struct bpf_tunnel_key",
	"struct bpf_xfrm_state",
	"struct linux_binprm",
	"struct pt_regs",
	"struct sk_reuseport_md",
	"struct sockaddr",
	"struct tcphdr",
	"struct seq_file",
	"struct tcp6_sock",
	"struct tcp_sock",
	"struct tcp_timewait_sock",
	"struct tcp_request_sock",
	"struct udp6_sock",
	"struct unix_sock",
	"struct task_struct",
	"struct cgroup",

	"struct __sk_buff",
	"struct sk_msg_md",
	"struct xdp_md",
	"struct path",
	"struct btf_ptr",
	"struct inode",
	"struct socket",
	"struct file",
	"struct bpf_timer",
	"struct mptcp_sock",
	"struct bpf_dynptr",
	"struct iphdr",
	"struct ipv6hdr",
}

var knownTypes = set(
	"...",
	"void",
	"const void",
	"char",
	"const char",
	"int",
	"long",
	"unsigned long",

	"__be16",
	"__be32",
	"__wsum",

	"struct bpf_fib_lookup",
	"struct bpf_perf_event_data",
	"struct bpf_perf_event_value",
	"struct bpf_pidns_info",
	"struct bpf_redir_neigh",
	"struct bpf_sk_lookup",
	"struct bpf_sock",
	"struct bpf_sock_addr",
	"struct bpf_sock_ops",
	"struct bpf_sock_tuple",
	"struct bpf_spin_lock",
	"struct bpf_sysctl",
	"struct bpf_tcp_sock",
	"struct bpf_tunnel_key",
	"struct bpf_xfrm_state",
	"struct linux_binprm",
	"struct pt_regs",
	"struct sk_reuseport_md",
	"struct sockaddr",
	"struct tcphdr",
	"struct seq_file",
	"struct tcp6_sock",
	"struct tcp_sock",
	"struct tcp_timewait_sock",
	"struct tcp_request_sock",
	"struct udp6_sock",
	"struct unix_sock",
	"struct task_struct",
	"struct cgroup",
	"struct path",
	"const struct path",
	"struct btf_ptr",
	"struct inode",
	"struct socket",
	"struct file",
	"struct bpf_timer",
	"struct mptcp_sock",
	"struct bpf_dynptr",
	"const struct bpf_dynptr",
	"struct iphdr",
	"struct ipv6hdr",
)

var mappedTypes = map[string]string{
	"u8":                   "__u8",
	"u16":                  "__u16",
	"u32":                  "__u32",
	"u64":                  "__u64",
	"s8":                   "__s8",
	"s16":                  "__s16",
	"s32":                  "__s32",
	"s64":                  "__s64",
	"size_t":               "unsigned long",
	"struct bpf_map":       "void",
	"struct sk_buff":       "struct __sk_buff",
	"const struct sk_buff": "const struct __sk_buff",
	"struct sk_msg_buff":   "struct sk_msg_md",
	"struct xdp_buff":      "struct xdp_md",
}

// overloadedHelpers take different context types: their first argument prints as void *ctx.
var overloadedHelpers = set("bpf_get_socket_cookie", "bpf_sk_assign")

func set(xs ...string) map[string]bool {
	m := make(map[string]bool, len(xs))
	for _, x := range xs {
		m[x] = true
	}
	return m
}

// ── Python `re` character classes, for str patterns ──────────────────────────────────────

// isWord is \w: Unicode letters, digits and the underscore.
func isWord(c rune) bool { return c == '_' || unicode.IsLetter(c) || unicode.IsDigit(c) }

// isPySpace is \s and str.isspace(): Unicode white space plus \x1c-\x1f, which Python counts
// as white space.
func isPySpace(c rune) bool { return unicode.IsSpace(c) || (c >= 0x1c && c <= 0x1f) }

// isPyDigit is \d: Unicode decimal digits.
func isPyDigit(c rune) bool { return unicode.Is(unicode.Nd, c) }

func run(s []rune, p int, f func(rune) bool) int {
	n := 0
	for p+n < len(s) && f(s[p+n]) {
		n++
	}
	return n
}

func wordRun(s []rune, p int) int { return run(s, p, isWord) }
func starRun(s []rune, p int) int { return run(s, p, func(c rune) bool { return c == '*' }) }

func at(s []rune, p int, lit string) bool {
	l := []rune(lit)
	if p < 0 || p+len(l) > len(s) {
		return false
	}
	for i, c := range l {
		if s[p+i] != c {
			return false
		}
	}
	return true
}

func runeAt(s []rune, p int) rune {
	if p < 0 || p >= len(s) {
		return -1
	}
	return s[p]
}

// body is a readline() result without its terminator. Python's `$` (no MULTILINE) matches
// at the end of the string or just before a final "\n", and `.` never matches "\n"; a line
// holds at most one "\n", at its end, so dropping it gives both semantics.
func body(line string) []rune { return []rune(strings.TrimSuffix(line, "\n")) }

// indentEnds is ` ?(?:\t| {5,8})` at p: every end position, in the order Python's
// backtracking tries them (the optional space taken first, the tab alternative before the
// spaces, the greedy {5,8} longest first).
func indentEnds(s []rune, p int) []int {
	var out, starts []int
	if runeAt(s, p) == ' ' {
		starts = append(starts, p+1)
	}
	starts = append(starts, p)
	for _, q := range starts {
		if runeAt(s, q) == '\t' {
			out = append(out, q+1)
		}
		n := min(run(s, q, func(c rune) bool { return c == ' ' }), 8)
		for k := n; k >= 5; k-- {
			out = append(out, q+k)
		}
	}
	return out
}

// isSubtitle is ` \* ?(?:\t| {5,8})TITLE$` ("Description", "Return", "Attributes").
func isSubtitle(line, title string) bool {
	s := body(line)
	if !at(s, 0, " *") {
		return false
	}
	for _, e := range indentEnds(s, 2) {
		if string(s[e:]) == title {
			return true
		}
	}
	return false
}

// sectionText is ` \* ?(?:\t| {5,8})(?:\t| {8})(.*)` (re.match, no `$`): the text of a
// section body line, and whether the line is one.
func sectionText(line string) (string, bool) {
	s := body(line)
	if !at(s, 0, " *") {
		return "", false
	}
	for _, e := range indentEnds(s, 2) {
		if runeAt(s, e) == '\t' {
			return string(s[e+1:]), true
		}
		if at(s, e, "        ") {
			return string(s[e+8:]), true
		}
	}
	return "", false
}

// ── parse_proto:
//    ` \* ?((.+) \**\w+\((((const )?(struct )?(\w+|\.\.\.)( \**\w+)?)(, )?){1,5}\))$` ──

// protoArgEnds is `(const )?(struct )?(\w+|\.\.\.)( \**\w+)?` at p: every end position.
func protoArgEnds(s []rune, p int) []int {
	seen := map[int]bool{}
	var out []int
	add := func(e int) {
		if !seen[e] {
			seen[e] = true
			out = append(out, e)
		}
	}
	s0 := []int{p}
	if at(s, p, "const ") {
		s0 = append(s0, p+6)
	}
	for _, a := range s0 {
		s1 := []int{a}
		if at(s, a, "struct ") {
			s1 = append(s1, a+7)
		}
		for _, b := range s1 {
			var cores []int
			for l := 1; l <= wordRun(s, b); l++ {
				cores = append(cores, b+l)
			}
			if at(s, b, "...") {
				cores = append(cores, b+3)
			}
			for _, c := range cores {
				add(c)
				if runeAt(s, c) == ' ' {
					w := c + 1 + starRun(s, c+1)
					for l := 1; l <= wordRun(s, w); l++ {
						add(w + l)
					}
				}
			}
		}
	}
	sort.Ints(out)
	return out
}

// repeatMatch reports whether s[p:] is exactly (UNIT(, )?){1,5}, count units already taken,
// where ends(s, p) lists every end position of one UNIT starting at p.
func repeatMatch(s []rune, p, count int, ends func([]rune, int) []int, memo map[[2]int]bool) bool {
	if count >= 1 && p == len(s) {
		return true
	}
	if count == 5 || p >= len(s) {
		return false
	}
	key := [2]int{p, count}
	if v, ok := memo[key]; ok {
		return v
	}
	ok := false
	for _, e := range ends(s, p) {
		if repeatMatch(s, e, count+1, ends, memo) ||
			(at(s, e, ", ") && repeatMatch(s, e+2, count+1, ends, memo)) {
			ok = true
			break
		}
	}
	memo[key] = ok
	return ok
}

// protoShapeMatches is `(.+) \**\w+\(ARGS\)` over the whole of t.
func protoShapeMatches(t []rune) bool {
	if runeAt(t, len(t)-1) != ')' {
		return false
	}
	closing := len(t) - 1
	for p := 0; p < closing; p++ {
		if t[p] != '(' {
			continue
		}
		// `(.+) \**\w+` must be exactly t[:p]: the word is the maximal \w run ending at p,
		// the stars the maximal run before it, then a space with a character before it.
		w := 0
		for p-w-1 >= 0 && isWord(t[p-w-1]) {
			w++
		}
		if w == 0 {
			continue
		}
		st := 0
		for p-w-st-1 >= 0 && t[p-w-st-1] == '*' {
			st++
		}
		sp := p - w - st
		if sp < 2 || t[sp-1] != ' ' {
			continue
		}
		if repeatMatch(t[p+1:closing], 0, 0, protoArgEnds, map[[2]int]bool{}) {
			return true
		}
	}
	return false
}

// ── proto_break_down: `(.+) (\**)(\w+)\(((([^,]+)(, )?){1,5})\)$` and, per argument,
//    `((\w+ )*?(\w+|...))( (\**)(\w+))?$` ─────────────────────────────────────────────

type arg struct {
	typ, star, name string
	hasName         bool // group 6 matched (the optional ` (\**)(\w+)` part)
}

type proto struct {
	retType, retStar, name string
	args                   []arg
}

// looseArgEnds is `[^,]+` at p, greedy: longest first.
func looseArgEnds(s []rune, p int) []int {
	n := run(s, p, func(c rune) bool { return c != ',' })
	out := make([]int, 0, n)
	for l := n; l >= 1; l-- {
		out = append(out, p+l)
	}
	return out
}

// argTail is `( (\**)(\w+))?$` at c.
func argTail(s []rune, c int) (star, name string, hasName, ok bool) {
	if runeAt(s, c) == ' ' {
		st := starRun(s, c+1)
		w := wordRun(s, c+1+st)
		if w > 0 && c+1+st+w == len(s) {
			return string(s[c+1 : c+1+st]), string(s[c+1+st:]), true, true
		}
	}
	if c == len(s) {
		return "", "", false, true
	}
	return "", "", false, false
}

func breakDownArg(a string) (arg, bool) {
	s := []rune(a)
	p := 0
	for {
		// (\w+|...) after the lazy `(\w+ )*?` repetitions taken so far.
		n := wordRun(s, p)
		for l := n; l >= 1; l-- {
			if star, name, has, ok := argTail(s, p+l); ok {
				return arg{string(s[:p+l]), star, name, has}, true
			}
		}
		// `...` is unescaped in bpf_doc.py: any three characters other than "\n".
		if p+3 <= len(s) && !strings.ContainsRune(string(s[p:p+3]), '\n') {
			if star, name, has, ok := argTail(s, p+3); ok {
				return arg{string(s[:p+3]), star, name, has}, true
			}
		}
		// One more `\w+ ` repetition.
		if n > 0 && runeAt(s, p+n) == ' ' {
			p += n + 1
			continue
		}
		return arg{}, false
	}
}

func breakDown(pr string) (proto, error) {
	t := []rune(pr)
	bad := fmt.Errorf("cannot break down helper prototype %q", pr)
	if runeAt(t, len(t)-1) != ')' || strings.ContainsRune(pr, '\n') {
		return proto{}, bad
	}
	closing := len(t) - 1
	// (.+) is greedy: the rightmost viable space wins.
	for i := len(t) - 1; i >= 1; i-- {
		if t[i] != ' ' {
			continue
		}
		st := starRun(t, i+1)
		n0 := i + 1 + st
		w := wordRun(t, n0)
		if w == 0 || runeAt(t, n0+w) != '(' {
			continue
		}
		a0 := n0 + w + 1
		if a0 > closing || !repeatMatch(t[a0:closing], 0, 0, looseArgEnds, map[[2]int]bool{}) {
			continue
		}
		out := proto{retType: string(t[:i]), retStar: string(t[i+1 : n0]), name: string(t[n0 : n0+w])}
		for _, a := range strings.Split(string(t[a0:closing]), ", ") {
			x, ok := breakDownArg(a)
			if !ok {
				return proto{}, fmt.Errorf("cannot break down argument %q of helper prototype %q", a, pr)
			}
			out.args = append(out.args, x)
		}
		return out, nil
	}
	return proto{}, bad
}

// ── HeaderParser ─────────────────────────────────────────────────────────────────────────

type helper struct {
	proto, desc, ret string
	attrs            []string
	enumVal          int
	hasEnumVal       bool
}

// errEnd is NoHelperFound / NoSyscallCommandFound: the end of a list, not an error.
var errEnd = errors.New("end of list")

type parser struct {
	data              string
	pos               int
	line              string
	helpers           []*helper
	descUniqueHelpers map[string]bool
	defineUnique      []string
	helperEnumVals    map[string]int
	helperEnumPos     map[string]int
}

func newParser(raw []byte) *parser {
	// open(..., 'r') reads with universal newlines.
	d := strings.ReplaceAll(string(raw), "\r\n", "\n")
	d = strings.ReplaceAll(d, "\r", "\n")
	return &parser{
		data:              d,
		descUniqueHelpers: map[string]bool{},
		helperEnumVals:    map[string]int{},
		helperEnumPos:     map[string]int{},
	}
}

func (p *parser) readline() string {
	if p.pos >= len(p.data) {
		return ""
	}
	end := len(p.data)
	if i := strings.IndexByte(p.data[p.pos:], '\n'); i >= 0 {
		end = p.pos + i + 1
	}
	l := p.data[p.pos:end]
	p.pos = end
	return l
}

func (p *parser) seekTo(target, help string, discard int) error {
	b := strings.Index(p.data, target)
	if b < 0 {
		return errors.New(help)
	}
	// bpf_doc.py seeks a text file to str.find()'s CHARACTER offset; for ASCII input that is
	// the byte offset. Reproduce that, and refuse where Python would land mid-character.
	off := len([]rune(p.data[:b]))
	if off < len(p.data) && !isRuneStart(p.data[off]) {
		return fmt.Errorf("seek to %q lands inside a multi-byte character", target)
	}
	p.pos = off
	p.readline()
	for range discard {
		p.readline()
	}
	p.line = p.readline()
	return nil
}

func isRuneStart(b byte) bool { return b&0xC0 != 0x80 }

// parseSection is parse_desc / parse_ret: the subtitle, then body lines until another one.
func (p *parser) parseSection(title, what, pr string) (string, error) {
	if !isSubtitle(p.line, title) {
		return "", fmt.Errorf("No %s section found for %s", what, pr)
	}
	var b strings.Builder
	present := false
	for {
		p.line = p.readline()
		if p.line == " *\n" {
			b.WriteByte('\n')
		} else if t, ok := sectionText(p.line); ok {
			present = true
			b.WriteString(t)
			b.WriteByte('\n')
		} else {
			break
		}
	}
	if !present {
		return "", fmt.Errorf("No %s found for %s", what, pr)
	}
	return b.String(), nil
}

func (p *parser) parseAttrs(pr string) ([]string, error) {
	if !isSubtitle(p.line, "Attributes") {
		return nil, nil
	}
	p.line = p.readline()
	t, ok := sectionText(p.line)
	if !ok {
		return nil, fmt.Errorf("Incomplete 'Attributes' section for %s", pr)
	}
	as := strings.Split(t, " ")
	for _, a := range as {
		if _, ok := attrs[a]; !ok {
			return nil, fmt.Errorf("Unexpected attribute '%s' specified for %s", a, pr)
		}
	}
	p.line = p.readline()
	if p.line != " *\n" {
		return nil, fmt.Errorf("Expecting empty line after 'Attributes' section for %s", pr)
	}
	p.line = p.readline()
	return as, nil
}

// parseSymbol is ` \* ?(BPF\w+)$`.
func (p *parser) parseSymbol() (string, error) {
	s := body(p.line)
	if !at(s, 0, " *") {
		return "", errEnd
	}
	for _, q := range []int{3, 2} {
		if q == 3 && runeAt(s, 2) != ' ' {
			continue
		}
		if at(s, q, "BPF") {
			if w := wordRun(s, q+3); w > 0 && q+3+w == len(s) {
				sym := string(s[q:])
				p.line = p.readline()
				return sym, nil
			}
		}
	}
	return "", errEnd
}

func (p *parser) parseProto() (string, error) {
	s := body(p.line)
	if !at(s, 0, " *") {
		return "", errEnd
	}
	for _, q := range []int{3, 2} {
		if q == 3 && runeAt(s, 2) != ' ' {
			continue
		}
		if q < len(s) && protoShapeMatches(s[q:]) {
			pr := string(s[q:])
			p.line = p.readline()
			return pr, nil
		}
	}
	return "", errEnd
}

func (p *parser) parseDescSyscall() error {
	if err := p.seekTo("* DOC: eBPF Syscall Commands",
		"Could not find start of eBPF syscall descriptions list", 1); err != nil {
		return err
	}
	for {
		sym, err := p.parseSymbol()
		if err == errEnd {
			return nil
		}
		if _, err := p.parseSection("Description", "description", sym); err != nil {
			return err
		}
		if _, err := p.parseSection("Return", "return", sym); err != nil {
			return err
		}
	}
}

// parseEnumSyscall: only the syscall printers use its result, but its seek can fail and it
// moves the reader, so it runs, as in bpf_doc.py.
func (p *parser) parseEnumSyscall() error {
	if err := p.seekTo("enum bpf_cmd {", "Could not find start of bpf_cmd enum", 0); err != nil {
		return err
	}
	for {
		s := []rune(p.line)
		q := run(s, 0, isPySpace)
		// `\s*(BPF\w+)+`: stop at the first line that is not an enum entry.
		if !at(s, q, "BPF") || wordRun(s, q+3) == 0 {
			return nil
		}
		// (`\s*(BPF\w+)\s*=\s*(BPF\w+)` lines are aliases; bpf_doc.py skips them. Either way
		// the reader moves on by one line.)
		p.line = p.readline()
	}
}

func (p *parser) parseDescHelpers() error {
	if err := p.seekTo(helpersDocStart,
		"Could not find start of eBPF helper descriptions list", 1); err != nil {
		return err
	}
	for {
		pr, err := p.parseProto()
		if err == errEnd {
			return nil
		}
		desc, err := p.parseSection("Description", "description", pr)
		if err != nil {
			return err
		}
		ret, err := p.parseSection("Return", "return", pr)
		if err != nil {
			return err
		}
		as, err := p.parseAttrs(pr)
		if err != nil {
			return err
		}
		bd, err := breakDown(pr)
		if err != nil {
			return err
		}
		p.helpers = append(p.helpers, &helper{proto: pr, desc: desc, ret: ret, attrs: as})
		p.descUniqueHelpers[bd.name] = true
	}
}

// fnEntry is `FN\((\w+), (\d+), ##ctx\)` at p.
func fnEntry(s []rune, p int) (end int, name, val string, ok bool) {
	if !at(s, p, "FN(") {
		return 0, "", "", false
	}
	n0 := p + 3
	w := wordRun(s, n0)
	if w == 0 || !at(s, n0+w, ", ") {
		return 0, "", "", false
	}
	d0 := n0 + w + 2
	d := run(s, d0, isPyDigit)
	if d == 0 || !at(s, d0+d, ", ##ctx)") {
		return 0, "", "", false
	}
	return d0 + d + 8, string(s[n0 : n0+w]), string(s[d0 : d0+d]), true
}

func (p *parser) parseDefineHelpers() error {
	// As in bpf_doc.py, the seek discards the line after the #define: FN(unspec, 0, ##ctx).
	if err := p.seekTo("#define ___BPF_FUNC_MAPPER(FN, ctx...)",
		"Could not find start of eBPF helper definition list", 1); err != nil {
		return err
	}
	var defs strings.Builder
	i := 0
	for {
		s := []rune(p.line)
		// `\s*FN\((\w+), (\d+), ##ctx\)|\\\\`
		if _, name, val, ok := fnEntry(s, run(s, 0, isPySpace)); ok {
			defs.WriteString(p.line)
			v, err := strconv.Atoi(val)
			if err != nil {
				return fmt.Errorf("bad helper number %q", val)
			}
			p.helperEnumVals["bpf_"+name] = v
			p.helperEnumPos["bpf_"+name] = i
			i++
		} else if at(s, 0, `\\`) {
			// The second alternative has no groups; bpf_doc.py dies on int(None) here.
			return fmt.Errorf("malformed ___BPF_FUNC_MAPPER line: %s", strings.TrimRight(p.line, "\n"))
		} else {
			break
		}
		p.line = p.readline()
	}
	// re.findall(r'FN\(\w+, \d+, ##ctx\)', fn_defines_str): non-overlapping, left to right.
	s := []rune(defs.String())
	for q := 0; q < len(s); {
		if e, _, _, ok := fnEntry(s, q); ok {
			p.defineUnique = append(p.defineUnique, string(s[q:e]))
			q = e
		} else {
			q++
		}
	}
	return nil
}

func (p *parser) validateHelpers() error {
	lastHelper := ""
	seenHelpers := map[string]bool{}
	seenEnumVals := map[int]bool{}
	i := 0
	for _, h := range p.helpers {
		bd, err := breakDown(h.proto)
		if err != nil {
			return err
		}
		name := bd.name
		enumVal, ok1 := p.helperEnumVals[name]
		enumPos, ok2 := p.helperEnumPos[name]
		if !ok1 || !ok2 {
			return fmt.Errorf("Helper %s is missing from enum bpf_func_id", name)
		}
		if seenHelpers[name] {
			if lastHelper != name {
				return fmt.Errorf("Helper %s has multiple descriptions which are not grouped together", name)
			}
			continue
		}
		if enumPos != i {
			return fmt.Errorf("Helper %s (ID %d) comment order (#%d) must be aligned with its position (#%d) in enum bpf_func_id",
				name, enumVal, i+1, enumPos+1)
		}
		if seenEnumVals[enumVal] {
			return fmt.Errorf("Helper %s has duplicated value %d", name, enumVal)
		}
		seenHelpers[name] = true
		lastHelper = name
		seenEnumVals[enumVal] = true
		h.enumVal, h.hasEnumVal = enumVal, true
		i++
	}
	return nil
}

func (p *parser) run() error {
	for _, step := range []func() error{
		p.parseDescSyscall, p.parseEnumSyscall, p.parseDescHelpers,
		p.parseDefineHelpers, p.validateHelpers,
	} {
		if err := step(); err != nil {
			return err
		}
	}
	return nil
}

// ── PrinterHelpersHeader ─────────────────────────────────────────────────────────────────

func mapType(t string) (string, error) {
	if knownTypes[t] {
		return t, nil
	}
	if m, ok := mappedTypes[t]; ok {
		return m, nil
	}
	return "", fmt.Errorf("Unrecognized type '%s', please add it to known types!", t)
}

// commentLines is print(' *{}{}'.format(' \t' if line else ”, line)) for each line.
func commentLines(b *strings.Builder, s string) {
	for _, line := range strings.Split(s, "\n") {
		b.WriteString(" *")
		if line != "" {
			b.WriteString(" \t")
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
}

func (p *parser) header() ([]byte, error) {
	// elem_number_check(desc_unique_helpers, define_unique_helpers, ...)
	nd, nf := len(p.descUniqueHelpers), len(p.defineUnique)
	if nd != nf {
		msg := fmt.Sprintf("The number of unique helper in description (%d) doesn't match the number of unique helper defined in ___BPF_FUNC_MAPPER (%d)", nd, nf)
		if nd < nf {
			msg += fmt.Sprintf("; the description for %s is not present or formatted correctly", p.defineUnique[nd])
		}
		return nil, errors.New(msg)
	}

	var b strings.Builder
	b.WriteString("/* This is auto-generated file. See bpf_doc.py for details. */\n\n/* Forward declarations of BPF structs */\n")
	for _, f := range typeFwds {
		b.WriteString(f + ";\n")
	}
	b.WriteByte('\n')

	used := map[string]bool{}
	for _, h := range p.helpers {
		for _, a := range h.attrs {
			used[a] = true
		}
	}
	names := make([]string, 0, len(used))
	for a := range used {
		names = append(names, a)
	}
	sort.Strings(names) // sorted(used_attrs): code-point order, as sort.Strings for ASCII
	for _, a := range names {
		fmt.Fprintf(&b, "#ifndef %s\n#if __has_attribute(%s)\n#define %s __attribute__((%s))\n#else\n#define %s\n#endif\n#endif\n",
			a, attrs[a], a, attrs[a], a)
	}
	if len(names) > 0 {
		b.WriteByte('\n')
	}

	seen := map[string]bool{}
	for _, h := range p.helpers {
		pr, err := breakDown(h.proto)
		if err != nil {
			return nil, err
		}
		if seen[pr.name] {
			continue
		}
		seen[pr.name] = true

		b.WriteString("/*\n")
		b.WriteString(" * " + pr.name + "\n")
		b.WriteString(" *\n")
		if h.desc != "" {
			// re.sub('\n$', '', desc, count=1) removes exactly one trailing "\n": with "X\n\n"
			// the leftmost match is the first of the two, which leaves "X\n" either way.
			commentLines(&b, strings.TrimSuffix(h.desc, "\n"))
		}
		if h.ret != "" {
			b.WriteString(" *\n")
			b.WriteString(" * Returns\n")
			commentLines(&b, strings.TrimRightFunc(h.ret, isPySpace))
		}
		b.WriteString(" */\n")
		b.WriteString("static ")
		if len(h.attrs) > 0 {
			b.WriteString(strings.Join(h.attrs, " ") + " ")
		}
		rt, err := mapType(pr.retType)
		if err != nil {
			return nil, err
		}
		fmt.Fprintf(&b, "%s %s(* const %s)(", rt, pr.retStar, pr.name)
		comma := ""
		for i, a := range pr.args {
			t, n := a.typ, a.name
			if overloadedHelpers[pr.name] && i == 0 {
				t, n = "void", "ctx"
			}
			mt, err := mapType(t)
			if err != nil {
				return nil, err
			}
			b.WriteString(comma + mt)
			if n != "" {
				if a.star != "" {
					b.WriteString(" " + a.star)
				} else {
					b.WriteString(" ")
				}
				b.WriteString(n)
			}
			comma = ", "
		}
		if !h.hasEnumVal {
			return nil, fmt.Errorf("no enum value for %s", pr.name)
		}
		fmt.Fprintf(&b, ") = (void *) %d;\n\n", h.enumVal)
	}
	b.WriteByte('\n') // print_footer: print('')
	return []byte(b.String()), nil
}

// ── command line: argparse-compatible for the options bpf_doc.py defines ─────────────────

const usage = "usage: bpf_doc.py [-h] [--header] [--json] [--filename FILENAME] [{helpers,syscall}]"

type exitError struct {
	code int
	msg  string
}

func (e *exitError) Error() string { return e.msg }

// generate runs bpf_doc.py's parse_and_dump for argv (without the program name) and returns
// the output. argv0 locates the default input, as bpf_doc.py does from sys.argv[0].
func generate(argv0 string, argv []string, stdout io.Writer) error {
	var header, json bool
	filename, target := "", ""
	haveFile := false
	longs := []string{"help", "header", "json", "filename"}
	for i := 0; i < len(argv); i++ {
		a := argv[i]
		if a == "-h" {
			fmt.Fprintln(stdout, usage)
			return nil
		}
		if rest, ok := strings.CutPrefix(a, "--"); ok && rest != "" {
			key, inline, hasInline := strings.Cut(rest, "=")
			// argparse accepts any unambiguous prefix of a long option.
			opt := ""
			var hits []string
			for _, l := range longs {
				if l == key {
					opt = l
				}
				if strings.HasPrefix(l, key) {
					hits = append(hits, l)
				}
			}
			if opt == "" && len(hits) == 1 {
				opt = hits[0]
			}
			switch {
			case opt == "":
				return &exitError{2, "unrecognized or ambiguous option: " + a + "\n" + usage}
			case opt == "help":
				fmt.Fprintln(stdout, usage)
				return nil
			case (opt == "header" || opt == "json") && hasInline:
				return &exitError{2, a + " takes no value"}
			case opt == "header":
				header = true
			case opt == "json":
				json = true
			default:
				if hasInline {
					filename = inline
				} else if i+1 < len(argv) {
					i++
					filename = argv[i]
				} else {
					return &exitError{2, "--filename expects one argument"}
				}
				haveFile = true
			}
			continue
		}
		if target != "" {
			return &exitError{2, "unrecognized arguments: " + a}
		}
		target = a
	}
	if target == "" {
		target = "helpers"
	}
	if target != "helpers" && target != "syscall" {
		return &exitError{2, fmt.Sprintf("argument target: invalid choice: '%s' (choose from 'helpers', 'syscall')", target)}
	}
	if !haveFile {
		// bpf_doc.py's default: <script dir>/../include/uapi/linux/bpf.h, if it exists.
		if abs, err := filepath.Abs(argv0); err == nil {
			f := filepath.Join(filepath.Dir(filepath.Dir(abs)), "include/uapi/linux/bpf.h")
			if st, err := os.Stat(f); err == nil && st.Mode().IsRegular() {
				filename, haveFile = f, true
			}
		}
		if !haveFile {
			return &exitError{2, "no input: pass --filename <include/uapi/linux/bpf.h>"}
		}
	}

	raw, err := os.ReadFile(filename)
	if err != nil {
		return &exitError{1, err.Error()}
	}
	if !isUTF8(raw) {
		return &exitError{1, filename + ": not valid UTF-8"}
	}
	p := newParser(raw)
	if err := p.run(); err != nil {
		return &exitError{1, err.Error()}
	}
	if header && json {
		return &exitError{1, "Use either --header or --json, not both"}
	}
	format := "rst"
	if header {
		format = "header"
	} else if json {
		format = "json"
	}
	if target == "syscall" && format == "header" {
		return &exitError{1, fmt.Sprintf("Unsupported target/format combination: \"%s\", \"%s\"", target, format)}
	}
	if target != "helpers" || format != "header" {
		return &exitError{2, fmt.Sprintf("target '%s' in '%s' format is not implemented by this port: the kernel build only runs --header for helpers", target, format)}
	}
	out, err := p.header()
	if err != nil {
		return &exitError{1, err.Error()}
	}
	if _, err := stdout.Write(out); err != nil {
		return &exitError{1, "cannot write output: " + err.Error()}
	}
	return nil
}

func isUTF8(b []byte) bool { return utf8.Valid(b) }

func main() {
	if err := generate(os.Args[0], os.Args[1:], os.Stdout); err != nil {
		code := 1
		var e *exitError
		if errors.As(err, &e) {
			code = e.code
		}
		fmt.Fprintln(os.Stderr, "bpfdoc:", err)
		os.Exit(code)
	}
}
