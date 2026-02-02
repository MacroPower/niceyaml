// Package main provides the nyaml CLI for viewing and validating YAML files.
package main

import (
	"context"
	"os"

	"github.com/charmbracelet/fang"
	"github.com/spf13/cobra"

	"go.jacobcolvin.com/niceyaml/fangs"
	"go.jacobcolvin.com/niceyaml/style/theme"
)

func main() {
	profiler := fangs.NewProfiler()

	rootCmd := &cobra.Command{
		Use:   "nyaml",
		Short: "A terminal YAML utility with syntax highlighting",
		PersistentPreRunE: func(_ *cobra.Command, _ []string) error {
			return profiler.Start()
		},
	}

	profiler.RegisterFlags(rootCmd.PersistentFlags())

	rootCmd.AddCommand(viewCmd())
	rootCmd.AddCommand(validateCmd())

	styles, _ := theme.Styles("charm")

	err := fang.Execute(context.Background(), rootCmd,
		fang.WithErrorHandler(fangs.ErrorHandler),
		fang.WithColorSchemeFunc(fangs.ColorSchemeFunc(styles)),
	)

	stopErr := profiler.Stop()
	if stopErr != nil && err == nil {
		err = stopErr
	}

	if err != nil {
		os.Exit(1)
	}
}
