// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

package rootcmd

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// probe registers a command that prints the value viper resolved for --target.
func probe(t *testing.T) *cobra.Command {
	t.Helper()
	saved := groups
	t.Cleanup(func() { groups = saved })
	groups = nil
	Register(func(v *viper.Viper) *cobra.Command {
		c := &cobra.Command{Use: "probe", RunE: func(cmd *cobra.Command, _ []string) error {
			cmd.Print(v.GetString("target-dir"))
			return nil
		}}
		c.Flags().String("target-dir", "default", "")
		return c
	})
	return New()
}

func run(t *testing.T, args ...string) string {
	t.Helper()
	root := probe(t)
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetArgs(args)
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	return out.String()
}

func TestPrecedence(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir()) // no stray user config
	cfg := filepath.Join(t.TempDir(), "river.yaml")
	if err := os.WriteFile(cfg, []byte("target-dir: from-config\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if got := run(t, "probe"); got != "default" {
		t.Errorf("default: got %q", got)
	}
	if got := run(t, "--config", cfg, "probe"); got != "from-config" {
		t.Errorf("config file: got %q", got)
	}
	t.Setenv("RIVER_TARGET_DIR", "from-env")
	if got := run(t, "--config", cfg, "probe"); got != "from-env" {
		t.Errorf("environment must beat the config file: got %q", got)
	}
	if got := run(t, "--config", cfg, "probe", "--target-dir", "from-flag"); got != "from-flag" {
		t.Errorf("flag must beat the environment: got %q", got)
	}
}

func TestMissingExplicitConfigFails(t *testing.T) {
	root := probe(t)
	root.SetArgs([]string{"--config", filepath.Join(t.TempDir(), "absent.yaml"), "probe"})
	root.SetOut(&bytes.Buffer{})
	if err := root.Execute(); err == nil {
		t.Fatal("a --config that does not exist must fail, not fall back to defaults")
	}
}
