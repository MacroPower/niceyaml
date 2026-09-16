// Package main provides the nyaml CLI for viewing and validating YAML files.
package main

import (
	"context"
	"os"

	"charm.land/fang/v2"
	"github.com/charmbracelet/x/term"
	"github.com/spf13/cobra"
	"go.jacobcolvin.com/x/cobras/profile"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/fangs"
	"go.jacobcolvin.com/niceyaml/printer"
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

	// The error printer carries the terminal width so annotated source
	// excerpts wrap to it.
	errPrinter := printer.New(printer.WithWidth(terminalWidth()))

	err := fang.Execute(context.Background(), rootCmd,
		fang.WithErrorHandler(fangs.NewErrorHandler(niceyaml.WithPrinter(errPrinter))),
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

// terminalWidth returns the width error output wraps to: the width of the
// terminal on stderr less the handler's margin, or 88 when stderr is not a
// terminal.
func terminalWidth() int {
	width := 90

	if term.IsTerminal(os.Stderr.Fd()) {
		w, _, err := term.GetSize(os.Stderr.Fd())
		if err == nil {
			width = w
		}
	}

	return max(0, width-2)
}
