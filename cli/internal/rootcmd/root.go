// Package rootcmd builds the `river` command tree: one cobra root, with viper binding every
// flag to a config file and to the RIVER_* environment the shell scripts used, so a port
// from a script keeps its existing variables working.
package rootcmd

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
)

// EnvPrefix is the environment prefix: --profile-dir reads RIVER_PROFILE_DIR.
const EnvPrefix = "RIVER"

// Version is stamped at build time (-ldflags "-X .../rootcmd.Version=...").
var Version = "dev"

// New returns the root command. Groups add their subcommands through Register.
func New() *cobra.Command {
	v := viper.New()
	root := &cobra.Command{
		Use:           "river",
		Short:         "Runink River: build, install and run the image",
		Long:          "river is the one command line for Runink River's internals. Every flag can also be set\nin the config file (--config, else /etc/river/river.yaml, else $XDG_CONFIG_HOME/river/river.yaml)\nor as RIVER_<FLAG> in the environment, with dashes as underscores.",
		SilenceUsage:  true,
		SilenceErrors: true,
		Version:       Version,
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			return bind(v, cmd)
		},
	}
	root.PersistentFlags().String("config", "", "config file (YAML, TOML or JSON)")
	for _, g := range groups {
		root.AddCommand(g(v))
	}
	return root
}

var groups []func(*viper.Viper) *cobra.Command

// Register adds a command group to the root. Each group package calls it from init, so
// cmd/river only needs to import the group for its commands to exist.
func Register(g func(v *viper.Viper) *cobra.Command) { groups = append(groups, g) }

// bind loads the config file and makes every flag of cmd readable through v, with this
// precedence: flag set on the command line, then environment, then config file, then the
// flag's default.
func bind(v *viper.Viper, cmd *cobra.Command) error {
	v.SetEnvPrefix(EnvPrefix)
	v.SetEnvKeyReplacer(strings.NewReplacer("-", "_"))
	v.AutomaticEnv()

	cfg, _ := cmd.Flags().GetString("config")
	if cfg != "" {
		v.SetConfigFile(cfg)
	} else {
		v.SetConfigName("river")
		v.AddConfigPath("/etc/river")
		if xdg, err := os.UserConfigDir(); err == nil {
			v.AddConfigPath(filepath.Join(xdg, "river"))
		}
	}
	if err := v.ReadInConfig(); err != nil {
		var notFound viper.ConfigFileNotFoundError
		if cfg != "" || !errors.As(err, &notFound) {
			return fmt.Errorf("config: %w", err)
		}
	}

	var bindErr error
	cmd.Flags().VisitAll(func(f *pflag.Flag) {
		if f.Name == "config" || f.Name == "help" {
			return
		}
		if err := v.BindPFlag(f.Name, f); err != nil && bindErr == nil {
			bindErr = err
		}
	})
	return bindErr
}
