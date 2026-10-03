// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

package testcmd

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/org-runink/river/cli/internal/evidence"
	"github.com/org-runink/river/pkg/pipe"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func sddmThemeCmd(v *viper.Viper) *cobra.Command {
	return &cobra.Command{
		Use:   "sddm-theme <out-dir>",
		Short: "render the SDDM theme and the Plasma splash offscreen, one PNG per state (needs qml6)",
		Long: `Render the Runink River SDDM theme and the Plasma splash offscreen and save one PNG per
scenario in <out-dir>, failing on any QML warning or error. It runs the SHIPPED copies (the
profile overlay) through tests/sddm/Harness.qml, which stands in for the greeter.

Needs qml6 (qt6-declarative) and the Qt SVG image plugin (qt6-svg) on the host; it is a
developer check, not part of Tier 1 (the Tier 1 container has no Qt). Look at the PNGs:
  mark-phase-{0,25,75}-1920x1080  the animation, three phases of the loop
  rest-1024x768                    the smallest supported screen
  rest-3840x2160                   a 4K screen
  focus-password                   the password field focused, with text
  failed-login                     after a failed sign-in (and caps lock on)
  tabfocus-button                  keyboard focus on a button: the sage focus ring
  no-user-list                     SDDM lists no users: a user name field
  secondary-screen                 a screen that is not the primary one
  handoff                          after a sign-in: the lockup where the splash draws it
  splash-1920x1080                 the Plasma start-up splash
With ImageMagick (magick) installed it also checks that the mark moves between two phases.`,
		Args: func(_ *cobra.Command, args []string) error {
			if len(args) != 1 {
				return errors.New("usage: river test sddm-theme <out-dir>")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			repo := v.GetString("repo")
			return withEvidence(cmd.Context(), evidenceOptsFrom(v), "river-dev/sddm-theme", sddmThemeChecks, repo,
				func(ctx context.Context) error {
					return SDDMTheme(ctx, repo, args[0], cmd.OutOrStdout(), cmd.ErrOrStderr())
				})
		},
	}
}

// sddmShots are the scenarios: PNG name, size, harness scenario, animation phase, and whether
// it renders the splash rather than the greeter theme.
var sddmShots = []struct {
	name, w, h, scenario, phase string
	splash                      bool
}{
	{"mark-phase-0-1920x1080", "1920", "1080", "rest", "0", false},
	{"mark-phase-25-1920x1080", "1920", "1080", "rest", "0.25", false},
	{"mark-phase-75-1920x1080", "1920", "1080", "rest", "0.75", false},
	{"rest-1024x768", "1024", "768", "rest", "0.25", false},
	{"rest-3840x2160", "3840", "2160", "rest", "0.25", false},
	{"focus-password", "1920", "1080", "focus", "0.25", false},
	{"failed-login", "1920", "1080", "failed", "0.25", false},
	{"tabfocus-button", "1920", "1080", "tabfocus", "0.25", false},
	{"no-user-list", "1920", "1080", "users0", "0.25", false},
	{"secondary-screen", "1920", "1080", "secondary", "0.25", false},
	{"handoff", "1920", "1080", "handoff", "0.25", false},
	{"splash-1920x1080", "1920", "1080", "splash", "0.25", true},
}

// sddmThemeChecks is every check SDDMTheme reports, by its stable evidence name: one render
// per scenario of the static table above, and whether the mark moves (skipped, with the
// reason, without ImageMagick).
var sddmThemeChecks = func() []string {
	var names []string
	for _, s := range sddmShots {
		names = append(names, "render-"+s.name)
	}
	return append(names, "mark-moves")
}()

// SDDMTheme renders every scenario into out and checks the harness's output. Was
// tests/sddm-theme.sh.
func SDDMTheme(ctx context.Context, repo, out string, outw, errw io.Writer) error {
	root, err := filepath.Abs(repo)
	if err != nil {
		return err
	}
	if out, err = filepath.Abs(out); err != nil {
		return err
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		return err
	}
	o := filepath.Join(root, "iso-profiles/river/root-overlay")
	theme := filepath.Join(o, "usr/share/sddm/themes/runink-river")
	splash := filepath.Join(o, "usr/share/plasma/look-and-feel/org.runink.river.desktop/contents/splash")
	harness := filepath.Join(root, "tests/sddm/Harness.qml")
	qml, err := exec.LookPath("qml6")
	if err != nil {
		if qml, err = exec.LookPath("qml"); err != nil {
			fmt.Fprintln(errw, "sddm-theme: qml6 not found (qt6-declarative)")
			return errors.New("sddm-theme: qml6 not found")
		}
	}
	if !isFile(filepath.Join(theme, "Main.qml")) || !isFile(filepath.Join(splash, "Splash.qml")) {
		fmt.Fprintln(errw, "sddm-theme: theme or splash missing; run branding/render.sh")
		return errors.New("sddm-theme: theme or splash missing")
	}

	// The human output is this command's own; the evidence only records alongside it.
	ev := newChecks(ctx, io.Discard, io.Discard)
	bad := 0
	for _, s := range sddmShots {
		dir := theme
		if s.splash {
			dir = splash
		}
		ok, detail := sddmShot(ctx, qml, harness, out, dir, s.name, s.w, s.h, s.scenario, s.phase, outw)
		if !ok {
			bad++
			ev.record("render-"+s.name, evidence.Fail, detail)
		} else {
			ev.record("render-"+s.name, evidence.Pass, detail)
		}
		if e := evidenceFrom(ctx); e != nil {
			if b, err := os.ReadFile(filepath.Join(out, s.name+".log")); err == nil {
				sum := sha256.Sum256(b)
				e.run.AddLog(s.name+".log", s.name+".log", hex.EncodeToString(sum[:]))
			}
		}
	}

	// The mark really moves: two phases of the loop must differ.
	if magick, err := exec.LookPath("magick"); err == nil {
		// compare exits 1 when the images differ; only the count it prints matters.
		d, _ := combined(ctx, pipe.Cmd(magick, "compare", "-metric", "AE",
			filepath.Join(out, "mark-phase-25-1920x1080.png"), filepath.Join(out, "mark-phase-75-1920x1080.png"), "null:"))
		n, _, _ := strings.Cut(d, " ")
		switch n {
		case "0", "":
			fmt.Fprintln(outw, "sddm-theme: the mark does not move between phases 0.25 and 0.75")
			bad++
			ev.record("mark-moves", evidence.Fail, "the mark does not move between phases 0.25 and 0.75")
		default:
			fmt.Fprintf(outw, "sddm-theme: phases 0.25 and 0.75 differ in %s pixels\n", n)
			ev.record("mark-moves", evidence.Pass, fmt.Sprintf("phases 0.25 and 0.75 differ in %s pixels", n))
		}
	} else {
		ev.record("mark-moves", evidence.Skip, "ImageMagick (magick) is not installed on this host")
	}
	if bad != 0 {
		fmt.Fprintln(errw, "sddm-theme: FAILED")
		return fmt.Errorf("sddm-theme: %d problem(s)", bad)
	}
	fmt.Fprintln(outw, "sddm-theme: OK")
	return nil
}

// sddmShot renders one scenario to <out>/<name>.png with its log beside it, and reports
// whether it came out clean: the harness exited 0 within a minute, printed nothing but its
// own "qml: harness: " lines, and wrote a non-empty PNG. The detail, for the evidence, says
// why not without naming a path.
func sddmShot(ctx context.Context, qml, harness, out, dir, name, w, h, scenario, phase string, outw io.Writer) (bool, string) {
	logPath := filepath.Join(out, name+".log")
	png := filepath.Join(out, name+".png")
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	cmd := pipe.Cmd(qml, harness, "--", "dir="+dir, "out="+png, "w="+w, "h="+h, "scenario="+scenario, "phase="+phase)
	cmd.Env = []string{"QT_QPA_PLATFORM=offscreen", "QT_QUICK_BACKEND=software", "QT_FORCE_STDERR_LOGGING=1"}
	log, err := combined(ctx, cmd)
	if werr := os.WriteFile(logPath, []byte(log), 0o644); werr != nil && err == nil {
		err = werr
	}
	if err != nil {
		fmt.Fprintf(outw, "sddm-theme: %s: harness failed\n%s", name, log)
		return false, "the harness failed (see the log)"
	}
	clean := true
	for _, l := range lines(log) {
		// anything but the harness's own lines is a QML warning or error
		if l != "" && !strings.HasPrefix(l, "qml: harness: ") {
			clean = false
			break
		}
	}
	if !clean {
		fmt.Fprintf(outw, "sddm-theme: %s: QML output:\n", name)
		for _, l := range lines(log) {
			fmt.Fprintf(outw, "  %s\n", l)
		}
	}
	if st, err := os.Stat(png); err != nil || st.Size() == 0 {
		fmt.Fprintf(outw, "sddm-theme: %s: no PNG\n", name)
		return false, "no PNG written"
	}
	fmt.Fprintf(outw, "sddm-theme: %s\n", png)
	if !clean {
		return false, "QML warnings or errors (see the log)"
	}
	return true, name + ".png rendered, no QML warnings"
}
