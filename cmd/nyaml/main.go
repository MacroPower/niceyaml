// Package main provides the nyaml CLI for viewing and validating YAML files.
package main

import (
	"context"
	"fmt"
	"os"

	"charm.land/fang/v2"
	"github.com/charmbracelet/x/term"
	"github.com/spf13/cobra"
	"go.jacobcolvin.com/x/cobras/profile"

	"go.jacobcolvin.com/niceyaml/fangs"
	"go.jacobcolvin.com/niceyaml/printer"
	"go.jacobcolvin.com/niceyaml/style"
)

func main() {
	rootCmd, stopProfiler := newRootCmd()

	// The error printer carries the terminal width so annotated source
	// excerpts wrap to it.
	errPrinter := printer.New(printer.WithWrap(terminalWidth()))

	err := fang.Execute(context.Background(), rootCmd,
		fang.WithErrorHandler(fangs.NewErrorHandler(fangs.WithPrinter(errPrinter))),
		fang.WithColorSchemeFunc(fangs.ColorSchemeFunc(style.Default())),
	)

	code := 0
	if err != nil {
		code = 1
	}

	// Errors returned by fang.Execute are rendered by its handler, so a
	// profile that fails to write this late prints itself.
	stopErr := stopProfiler()
	if stopErr != nil {
		fmt.Fprintln(os.Stderr, "stop profiler:", stopErr)

		code = 1
	}

	if code != 0 {
		os.Exit(code)
	}
}

// newRootCmd builds the nyaml root command and the function that stops
// profiling and writes the snapshot profiles. Call the returned function
// after the command runs, whatever it returned.
func newRootCmd() (*cobra.Command, func() error) {
	cfg := profile.NewConfig()

	// NewProfiler copies the config, so the profiler is built inside
	// PersistentPreRunE, after Cobra has parsed the flags bound to cfg. A
	// profiler built here would carry the empty paths cfg starts with and
	// write nothing.
	var p *profile.Profiler

	rootCmd := &cobra.Command{
		Use:   "nyaml",
		Short: "A terminal YAML utility with syntax highlighting",
		PersistentPreRunE: func(_ *cobra.Command, _ []string) error {
			p = cfg.NewProfiler()

			return p.Start()
		},
	}

	cfg.RegisterFlags(rootCmd.PersistentFlags())

	cfg.MustRegisterCompletions(rootCmd)

	rootCmd.AddCommand(viewCmd())
	rootCmd.AddCommand(validateCmd())

	return rootCmd, func() error {
		// PersistentPreRunE does not run for --help or a flag parse error,
		// which leaves no profiler to stop.
		if p == nil {
			return nil
		}

		return p.Stop() //nolint:wrapcheck // Reported as it is, from main.
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
