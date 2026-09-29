// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

package testcmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/org-runink/river/pkg/pipe"
)

// FirstbootHooks — the first-boot hook contract, run against river-firstboot-hooks in a
// scratch root (RIVER_FIRSTBOOT_ROOT), no root needed (docs/PAYLOADS.md). Was
// tests/firstboot-hooks.sh.
//
//	done        exit 0, no setup.json       -> <hook>.done at once; not run again
//	registered  exit 0 with setup.json      -> not done until the page writes `done`; then done,
//	                                           and the page directory (the token) is gone
//	deferred    exit 75                     -> not done, not failed; runs again (headless too)
//	failed      any other status            -> not done; status "failed <rc>"; runs again
//	headless    no display this boot        -> no RIVER_PAGE_DIR
//	answers     shredded once every hook is done, kept while one is not
//
// The runner is the image's shell script, run as a program. Tier 1 runs it
// (scripts/ci-tier1.sh).
func FirstbootHooks(ctx context.Context, repo string, outw, errw io.Writer) error {
	runner := filepath.Join(repo, "iso-profiles/river/root-overlay/usr/local/bin/river-firstboot-hooks")
	if !isFile(runner) {
		fmt.Fprintf(errw, "firstboot-hooks: no runner at %s\n", runner)
		return errors.New("firstboot-hooks: no runner")
	}
	t, err := os.MkdirTemp("", "firstboot-hooks.")
	if err != nil {
		return err
	}
	defer os.RemoveAll(t)
	f := &fbFixture{ctx: ctx, runner: runner, t: t, r: filepath.Join(t, "root")}
	c := &checks{out: outw, err: errw}

	fmt.Fprintln(outw, "firstboot-hooks: contract checks")
	if err := firstbootChecks(f, c); err != nil {
		return fmt.Errorf("firstboot-hooks: %w", err)
	}

	if c.n == 0 {
		fmt.Fprintln(errw, "firstboot-hooks: no check ran")
		return errors.New("firstboot-hooks: no check ran")
	}
	if c.failed != 0 {
		fmt.Fprintln(errw, "firstboot-hooks: FAILED")
		return fmt.Errorf("firstboot-hooks: %d of %d checks failed", c.failed, c.n)
	}
	fmt.Fprintf(outw, "firstboot-hooks: OK (%d checks)\n", c.n)
	return nil
}

// fbFixture is one scratch node root and the runner's last output.
type fbFixture struct {
	ctx    context.Context
	runner string
	t, r   string // the scratch directory, and the node root inside it
	out    string // the runner's stdout and stderr, from the last run
}

// setup makes a fresh node root, with a display (a DRM card) or headless.
func (f *fbFixture) setup(display bool) error {
	if err := os.RemoveAll(f.r); err != nil {
		return err
	}
	for _, d := range []string{"usr/local/lib/runink/firstboot.d", "var/lib/runink/firstboot.d", "dev/dri", "etc/runink"} {
		if err := os.MkdirAll(filepath.Join(f.r, d), 0o755); err != nil {
			return err
		}
	}
	if err := touch(f.p("var/lib/runink/firstboot-ui")); err != nil {
		return err
	}
	if display {
		return touch(f.p("dev/dri/card0"))
	}
	return nil
}

// p is a path inside the node root.
func (f *fbFixture) p(rel string) string { return filepath.Join(f.r, rel) }

// hook installs an executable first-boot hook with this body.
func (f *fbFixture) hook(name, body string) error {
	return writeExec(f.p("usr/local/lib/runink/firstboot.d/"+name), "#!/bin/sh\n"+body+"\n", 0o755)
}

// run runs the first-boot runner once over the root. A runner that fails stops the test, as
// `set -e` stopped the script.
func (f *fbFixture) run() error {
	cmd := pipe.Cmd("sh", f.runner)
	cmd.Env = []string{"RIVER_FIRSTBOOT_ROOT=" + f.r, "RIVER_FIRSTBOOT_K0S_WAIT=0", "RIVER_FIRSTBOOT_POLL=0.2"}
	out, err := combined(f.ctx, cmd)
	f.out = out
	if err != nil {
		return fmt.Errorf("river-firstboot-hooks: %w\n%s", err, out)
	}
	return nil
}

func (f *fbFixture) done(hook string) bool {
	return exists(f.p("var/lib/runink/firstboot.d/" + hook + ".done"))
}

func (f *fbFixture) statusIs(hook, want string) bool {
	return readTrim(f.p("run/runink/firstboot-status/"+hook)) == want
}

// count is how many times a hook that logs to count-<n> ran (`grep -c .`, 0 when missing).
func (f *fbFixture) count(n string) string {
	b, err := os.ReadFile(f.p("count-" + n))
	if err != nil {
		return "0"
	}
	k := 0
	for _, l := range lines(string(b)) {
		if l != "" {
			k++
		}
	}
	return fmt.Sprint(k)
}

func firstbootChecks(f *fbFixture, c *checks) error {
	steps := func(fns ...func() error) error {
		for _, fn := range fns {
			if err := fn(); err != nil {
				return err
			}
		}
		return nil
	}
	hook := func(name, body string) func() error { return func() error { return f.hook(name, body) } }
	root := f.r // the hooks name paths in the root literally, as the script's did

	// --- done: exit 0 without setup.json -----------------------------------------------------
	if err := steps(func() error { return f.setup(true) },
		hook("10-done", fmt.Sprintf(`echo run >> "%s/count-10"; exit 0`, root)), f.run); err != nil {
		return err
	}
	c.expect("done: exit 0 without setup.json marks the hook done", f.done("10-done"))
	c.expect("done: status file says done", f.statusIs("10-done", "done 0"))
	if err := f.run(); err != nil {
		return err
	}
	c.expect("done: a done hook never runs again", f.count("10") == "1")

	// --- registered: exit 0 with setup.json, done when the page writes `done` ----------------
	// The hook registers a page, and its "page" (a background job) finishes a moment later.
	if err := steps(func() error { return f.setup(true) }, hook("20-page", `
[ -n "${RIVER_PAGE_DIR:-}" ] || exit 9
[ "$(stat -c %a "$RIVER_PAGE_DIR")" = 700 ] || exit 8
printf '{"version":1,"title":"T","url":"http://[::1]:9/?token=x","order":1}\n' > "$RIVER_PAGE_DIR/setup.json"
( sleep 1; : > "$RIVER_PAGE_DIR/done" ) &
exit 0`), f.run); err != nil {
		return err
	}
	c.expect("registered: done only after the page wrote done", f.done("20-page"))
	c.expect("registered: the page directory (and its token) is deleted", !exists(f.p("run/runink/firstboot-pages/20-page")))
	c.expect("registered: the pages parent is 0711", mode(f.p("run/runink/firstboot-pages")) == "711")
	// A registered page whose first boot ends (UI no longer pending) before it finishes: not done.
	if err := steps(func() error { return f.setup(true) },
		hook("21-page", fmt.Sprintf(`printf '{}' > "$RIVER_PAGE_DIR/setup.json"; ( sleep 1; rm -f "%s/var/lib/runink/firstboot-ui" ) & exit 0`, root)),
		f.run); err != nil {
		return err
	}
	c.expect("registered: not done when the graphical first boot ends first", !f.done("21-page"))
	c.expect("registered: reported as deferred for the next boot", f.statusIs("21-page", "deferred 0"))

	// --- deferred: exit 75, on a display boot and on a headless one --------------------------
	for _, m := range []string{"display", "headless"} {
		if err := steps(func() error { return f.setup(m == "display") },
			hook("30-defer", fmt.Sprintf(`echo run >> "%s/count-30"; exit 75`, root)), f.run, f.run); err != nil {
			return err
		}
		c.expect("deferred ("+m+"): exit 75 is not done", !f.done("30-defer"))
		c.expect("deferred ("+m+"): it runs again", f.count("30") == "2")
		c.expect("deferred ("+m+"): status says deferred", f.statusIs("30-defer", "deferred 75"))
		c.expect("deferred ("+m+"): not logged as a failure", !strings.Contains(f.out, "FAILED"))
	}

	// --- failed: any other status ------------------------------------------------------------
	if err := steps(func() error { return f.setup(true) },
		hook("40-fail", fmt.Sprintf(`echo run >> "%s/count-40"; exit 3`, root)), f.run); err != nil {
		return err
	}
	c.expect("failed: not done", !f.done("40-fail"))
	c.expect("failed: status says failed with the exit code", f.statusIs("40-fail", "failed 3"))
	c.expect("failed: logged", strings.Contains(f.out, "40-fail FAILED (rc=3)"))
	if err := f.run(); err != nil {
		return err
	}
	c.expect("failed: runs again (Retry / next boot)", f.count("40") == "2")

	// --- headless: no display, no RIVER_PAGE_DIR ---------------------------------------------
	const probe = `[ -z "${RIVER_PAGE_DIR:-}" ] || exit 7; exit 0`
	if err := steps(func() error { return f.setup(false) }, hook("50-probe", probe), f.run); err != nil {
		return err
	}
	c.expect("headless: no RIVER_PAGE_DIR without a display", f.done("50-probe"))
	if err := steps(func() error { return f.setup(true) },
		func() error { return os.Remove(f.p("var/lib/runink/firstboot-ui")) },
		hook("51-probe", probe), f.run); err != nil {
		return err
	}
	c.expect("no graphical first boot pending: no RIVER_PAGE_DIR", f.done("51-probe"))

	// --- answers: kept while a hook is not done, shredded once all are -----------------------
	answers := f.p("var/lib/runink/firstboot.d/setup-answers")
	if err := steps(func() error { return f.setup(true) },
		func() error { return os.WriteFile(answers, []byte("secret\n"), 0o644) },
		hook("60-a", "exit 0"),
		hook("61-b", fmt.Sprintf(`[ -f "%s/ok-61" ] || exit 75`, root)),
		f.run); err != nil {
		return err
	}
	c.expect("answers: kept while a hook is deferred", isFile(answers))
	if err := steps(func() error { return touch(f.p("ok-61")) }, f.run); err != nil {
		return err
	}
	c.expect("answers: shredded once every hook is done", !exists(answers))

	// --- role: passed through ----------------------------------------------------------------
	if err := steps(func() error { return f.setup(true) },
		func() error { return os.WriteFile(f.p("etc/runink/role"), []byte("runner\n"), 0o644) },
		hook("70-role", `[ "${RIVER_ROLE:-}" = runner ]`), f.run); err != nil {
		return err
	}
	c.expect("role: RIVER_ROLE is the installer's choice", f.done("70-role"))
	return nil
}
