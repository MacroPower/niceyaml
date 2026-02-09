// Package main provides the nyaml CLI for viewing and validating YAML files.
package main

import (
	"context"
	"os"

	"github.com/charmbracelet/fang"
	"github.com/spf13/cobra"
	"go.jacobcolvin.com/x/profile"

	"go.jacobcolvin.com/niceyaml/fangs"
	"go.jacobcolvin.com/niceyaml/style/theme"
)

func main() {
	cfg := profile.NewConfig()
	p := cfg.NewProfiler()

	rootCmd := &cobra.Command{
		Use:   "nyaml",
		Short: "A terminal YAML utility with syntax highlighting",
		PersistentPreRunE: func(_ *cobra.Command, _ []string) error {
			return p.Start()
		},
	}

	cfg.RegisterFlags(rootCmd.PersistentFlags())

	_ = cfg.RegisterCompletions(rootCmd) //nolint:errcheck // Best-effort completions.

	rootCmd.AddCommand(viewCmd())
	rootCmd.AddCommand(validateCmd())

	styles, _ := theme.Styles("charm")

	err := fang.Execute(context.Background(), rootCmd,
		fang.WithErrorHandler(fangs.ErrorHandler),
		fang.WithColorSchemeFunc(fangs.ColorSchemeFunc(styles)),
	)

	stopErr := p.Stop()
	if stopErr != nil && err == nil {
		err = stopErr
	}

	if err != nil {
		os.Exit(1)
	}
}
