// Package main provides the nyaml CLI for viewing and validating YAML files.
package main

import (
	"context"
	"os"

	"charm.land/fang/v2"
	"github.com/spf13/cobra"
	"go.jacobcolvin.com/x/cobras/profile"

	"go.jacobcolvin.com/niceyaml/fangs"
	"go.jacobcolvin.com/niceyaml/style"
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

	cfg.MustRegisterCompletions(rootCmd)

	rootCmd.AddCommand(viewCmd())
	rootCmd.AddCommand(validateCmd())

	err := fang.Execute(context.Background(), rootCmd,
		fang.WithErrorHandler(fangs.ErrorHandler),
		fang.WithColorSchemeFunc(fangs.ColorSchemeFunc(style.Default())),
	)

	stopErr := p.Stop()
	if stopErr != nil && err == nil {
		err = stopErr
	}

	if err != nil {
		os.Exit(1)
	}
}
