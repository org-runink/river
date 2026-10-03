// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

package testcmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/org-runink/river/pkg/pipe"
)

// evalHookEnv is the third fixed piece of shell: it evaluates the config hooks_env wrote ($1),
// as the kit does, and checks that a single quote in a value survived.
const evalHookEnv = `eval "$1"; [ "$RIVER_HOOK_Q" = "it's" ]`

// installedHooksDeclared is every check InstalledHooks reports, by its stable evidence name
// (static: see testevidence.go).
var installedHooksDeclared = []string{
	"host-only-executable-hooks", "host-any-mode-in-kit", "host-declared-join-verdict",
	"host-missing-dir-adds-nothing", "host-budget", "host-no-header-refused", "host-bad-name-refused",
	"host-env-exported", "host-env-quote-survives",
	"vm-lines-pass-through", "vm-complete-hook-passes", "vm-skip-is-a-report", "vm-nonexec-kit-hook-runs",
	"vm-broken-exit-status", "vm-broken-unreported", "vm-broken-reported-twice", "vm-broken-undeclared",
	"vm-timeout-killed", "vm-kit-no-header-fails",
	"verdict-ok-and-skip-pass", "verdict-skip-listed", "verdict-fail-fails", "verdict-silent-fails",
	"verdict-skip-then-fail-fails",
}

// InstalledHooks — the profile hook contract of build/qemu-gui-test.sh (build/qemu-hooks.sh),
// exercised without a VM against fake hooks in a scratch directory. Was
// tests/installed-hooks.sh.
//
//	host side  only executable *.sh are hooks; a hook without a RIVERTEST-CHECKS header is
//	           refused; the verdict gains hook-<name> + every declared check; RIVER_HOOK_*
//	           variables reach the hooks' config, quoted
//	in the VM  run_hooks passes the lines through and fails hook-<name> on a non-zero exit, a
//	           timeout, a declared check left unreported or reported twice, an undeclared one
//	verdict    OK passes, FAIL and silence fail, SKIP is listed and does not fail
//
// INSTALLED_HOOKS_DEBUG=1 copies run_hooks' output to stderr. Tier 1 runs it
// (scripts/ci-tier1.sh).
func InstalledHooks(ctx context.Context, repo string, outw, errw io.Writer) error {
	lib, err := filepath.Abs(filepath.Join(repo, "build/qemu-hooks.sh"))
	if err != nil {
		return err
	}
	if !isFile(lib) {
		return fmt.Errorf("installed-hooks: no %s", lib)
	}
	t, err := os.MkdirTemp("", "installed-hooks.")
	if err != nil {
		return err
	}
	defer os.RemoveAll(t)
	c := newChecks(ctx, outw, errw)
	if err := installedHooksChecks(ctx, lib, t, c, errw); err != nil {
		return fmt.Errorf("installed-hooks: %w", err)
	}
	if c.n < 20 {
		fmt.Fprintf(errw, "installed-hooks: only %d checks ran\n", c.n)
		return fmt.Errorf("installed-hooks: only %d checks ran", c.n)
	}
	if c.failed != 0 {
		return fmt.Errorf("installed-hooks: %d of %d checks failed", c.failed, c.n)
	}
	fmt.Fprintf(outw, "installed-hooks: %d checks passed\n", c.n)
	return nil
}

func installedHooksChecks(ctx context.Context, lib, t string, c *checks, errw io.Writer) error {
	d, logDir, bad := filepath.Join(t, "d"), filepath.Join(t, "log"), filepath.Join(t, "bad")
	for _, dir := range []string{d, logDir, bad} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	// hook NAME MODE BODY: the header lines are part of BODY.
	hook := func(name string, perm os.FileMode, body string) error {
		return writeExec(filepath.Join(d, name+".sh"), "#!/bin/sh\n"+body+"\n", perm)
	}
	out := func(env []string, fn string, args ...string) (string, error) {
		cmd := libCall(lib, fn, args...)
		cmd.Env = env
		return capture(ctx, cmd, errw)
	}
	quiet := func(fn string, args ...string) error {
		_, err := capture(ctx, libCall(lib, fn, args...), nil)
		return err
	}

	for _, h := range []struct {
		name string
		perm os.FileMode
		body string
	}{
		{"10-good", 0o755, `# RIVERTEST-CHECKS: a-one b-two
echo "RIVERTEST NOTE waiting"
echo "RIVERTEST OK a-one"
echo "RIVERTEST FAIL b-two (answered 503)"`},
		{"20-skip", 0o755, `# RIVERTEST-CHECKS: c-later
[ "${RIVER_HOOK_LATER:-0}" = 1 ] && echo "RIVERTEST OK c-later" || echo "RIVERTEST SKIP c-later (not enabled)"`},
		{"30-off", 0o644, `# RIVERTEST-CHECKS: never
echo "RIVERTEST OK never"`},
	} {
		if err := hook(h.name, h.perm, h.body); err != nil {
			return err
		}
	}
	if err := os.WriteFile(filepath.Join(d, "README"), []byte("not a hook\n"), 0o644); err != nil {
		return err
	}

	// --- host side ---------------------------------------------------------------------------
	list, _ := out(nil, "hooks_list", d)
	var bases strings.Builder
	for _, l := range lines(list) {
		bases.WriteString(filepath.Base(l) + " ")
	}
	c.expect("host-only-executable-hooks", "only executable *.sh are hooks", bases.String() == "10-good.sh 20-skip.sh ")
	all, _ := out([]string{"HOOKS_ANY_MODE=1"}, "hooks_list", d)
	c.expect("host-any-mode-in-kit", "every *.sh counts in the kit (FAT has no mode bits)", len(lines(all)) == 3)
	want, _ := out(nil, "hooks_want", d)
	c.expect("host-declared-join-verdict", "the declared checks join the verdict", want == "hook-10-good a-one b-two hook-20-skip c-later")
	none, _ := out(nil, "hooks_want", filepath.Join(t, "none"))
	c.expect("host-missing-dir-adds-nothing", "a missing hook directory adds nothing", none == "")
	budget, _ := out(nil, "hooks_budget", d)
	c.expect("host-budget", "the budget is the timeouts plus a margin", budget == fmt.Sprint(2*(1200+15)))
	nohdr := filepath.Join(bad, "nohdr.sh")
	if err := writeExec(nohdr, "#!/bin/sh\necho \"RIVERTEST OK x\"\n", 0o755); err != nil {
		return err
	}
	c.expect("host-no-header-refused", "a hook without a header is refused", quiet("hooks_want", bad) != nil)
	if err := os.WriteFile(nohdr, []byte("#!/bin/sh\n# RIVERTEST-CHECKS: Bad_Name\n"), 0o755); err != nil {
		return err
	}
	c.expect("host-bad-name-refused", "a check name outside [a-z0-9-] is refused", quiet("hooks_want", bad) != nil)
	envOut, _ := out([]string{"RIVER_HOOK_LATER=1", "RIVER_HOOK_Q=it's", "NOT_A_HOOK=1"}, "hooks_env")
	laterLine := regexp.MustCompile(`^export RIVER_HOOK_LATER=.1.$`)
	c.expect("host-env-exported", "RIVER_HOOK_* variables are exported, others are not",
		len(matching(envOut, laterLine)) > 0 && !strings.Contains(envOut, "NOT_A_HOOK"))
	c.expect("host-env-quote-survives", "a quote in a value survives the config",
		pipe.Run(ctx, pipe.IO{}, pipe.Cmd("sh", "-c", evalHookEnv, "sh", envOut)) == nil)

	// --- in the VM ---------------------------------------------------------------------------
	if err := hook("40-broken", 0o755, `# RIVERTEST-CHECKS: d-missing e-twice
# RIVERTEST-TIMEOUT: 30
echo "RIVERTEST OK e-twice"
echo "RIVERTEST OK e-twice"
echo "RIVERTEST OK f-stray"
exit 3`); err != nil {
		return err
	}
	if err := hook("50-slow", 0o755, `# RIVERTEST-CHECKS: g-slow
# RIVERTEST-TIMEOUT: 1
sleep 30
echo "RIVERTEST OK g-slow"`); err != nil {
		return err
	}
	run, err := combined(ctx, libCall(lib, "run_hooks", d, logDir))
	if err != nil {
		return fmt.Errorf("run_hooks: %w\n%s", err, run)
	}
	if os.Getenv("INSTALLED_HOOKS_DEBUG") != "" {
		fmt.Fprint(errw, run)
	}
	results := matching(run, regexp.MustCompile(`^RIVERTEST`))
	resultsFile := filepath.Join(t, "results.txt")
	if err := os.WriteFile(resultsFile, []byte(joinLines(results)), 0o644); err != nil {
		return err
	}
	c.expect("vm-lines-pass-through", "a hook's lines pass through", hasLine(run, "RIVERTEST OK a-one"))
	c.expect("vm-complete-hook-passes", "a hook reporting every declared check passes", hasLine(run, "RIVERTEST OK hook-10-good"))
	c.expect("vm-skip-is-a-report", "a SKIP is a report", hasLine(run, "RIVERTEST OK hook-20-skip"))
	c.expect("vm-nonexec-kit-hook-runs", "a non-executable hook in the kit still runs", hasLine(run, "RIVERTEST OK hook-30-off"))
	broken := strings.Join(matching(run, regexp.MustCompile(`^RIVERTEST FAIL hook-40-broken`)), "\n")
	for _, w := range []struct{ name, why string }{
		{"vm-broken-exit-status", "exit 3"},
		{"vm-broken-unreported", "d-missing reported 0 times"},
		{"vm-broken-reported-twice", "e-twice reported 2 times"},
		{"vm-broken-undeclared", "f-stray not declared"},
	} {
		c.expect(w.name, "a broken hook fails: "+w.why, strings.Contains(broken, w.why))
	}
	c.expect("vm-timeout-killed", "a hook past its timeout is killed and fails",
		len(matching(run, regexp.MustCompile(`^RIVERTEST FAIL hook-50-slow \(timed out after 1s`))) > 0)
	kitNoHdr := filepath.Join(d, "60-nohdr.sh")
	if err := os.WriteFile(kitNoHdr, []byte("#!/bin/sh\necho hi\n"), 0o644); err != nil {
		return err
	}
	// Only its output matters here, as in `run_hooks ... | grep -q`.
	again, _ := capture(ctx, libCall(lib, "run_hooks", d, logDir), errw)
	c.expect("vm-kit-no-header-fails", "a kit hook without a header fails in the VM too",
		len(matching(again, regexp.MustCompile(`^RIVERTEST FAIL hook-60-nohdr`))) > 0)
	if err := os.Remove(kitNoHdr); err != nil {
		return err
	}

	// --- the verdict -------------------------------------------------------------------------
	v, verr := combined(ctx, libCall(lib, "verdict", resultsFile, "a-one", "hook-10-good", "c-later"))
	c.expect("verdict-ok-and-skip-pass", "OK and SKIP pass the verdict", verr == nil)
	c.expect("verdict-skip-listed", "a SKIP is listed as SKIP", len(matching(v, regexp.MustCompile(`SKIP  c-later .*\(not enabled\)`))) > 0)
	verdictFails := func(results string, check string) bool {
		_, err := capture(ctx, libCall(lib, "verdict", results, check), errw)
		return err != nil
	}
	c.expect("verdict-fail-fails", "a FAIL line fails", verdictFails(resultsFile, "b-two"))
	c.expect("verdict-silent-fails", "a silent check fails", verdictFails(resultsFile, "d-missing"))
	sf := filepath.Join(t, "sf.txt")
	if err := os.WriteFile(sf, []byte("RIVERTEST SKIP z\nRIVERTEST FAIL z (then broke)\n"), 0o644); err != nil {
		return err
	}
	c.expect("verdict-skip-then-fail-fails", "SKIP with a FAIL fails", verdictFails(sf, "z"))
	return nil
}

// joinLines is the lines, each ending in a newline (what `grep ... > file` writes).
func joinLines(ls []string) string {
	if len(ls) == 0 {
		return ""
	}
	return strings.Join(ls, "\n") + "\n"
}
