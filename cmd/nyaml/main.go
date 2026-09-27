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
	"go.jacobcolvin.com/niceyaml/style/theme"
)

func main() {
	rootCmd, stopProfiler := newRootCmd()

	// The error printer carries the terminal width so annotated source
	// excerpts wrap to it.
	errPrinter := printer.New(printer.WithWrap(terminalWidth()))

	err := fang.Execute(context.Background(), rootCmd,
		fang.WithErrorHandler(fangs.NewErrorHandler(fangs.WithPrinter(errPrinter))),
		fang.WithColorSchemeFunc(helpColorScheme()),
	)

	code := 0
	if err != nil {
		code = 1
	}

	// The error handler of fang.Execute renders only the errors it returns,
	// so a profile that fails to write this late prints itself.
	stopErr := stopProfiler()
	if stopErr != nil {
		fmt.Fprintln(os.Stderr, "stop profiler:", stopErr)

		code = 1
	}

	if code != 0 {
		os.Exit(code)
	}
}

// lightHelpTheme names the built-in theme for help output on a light
// terminal. Every color it gives the help output keeps a high contrast
// against a white background.
const lightHelpTheme = "modus-operandi"

// helpColorScheme colors help output with the default charm theme on a dark
// terminal and with [lightHelpTheme] on a light one. Help text sits on the
// terminal's own background, where the pale text of a dark theme is hard to
// read on white.
func helpColorScheme() fang.ColorSchemeFunc {
	light, _ := theme.Builtin().Get(lightHelpTheme)

	return fangs.LightDarkColorSchemeFunc(light, theme.Charm)
}

// newRootCmd builds the nyaml root command and the function that stops
// profiling and writes the snapshot profiles. Call the returned function
// after the command runs, whatever it returned.
func newRootCmd() (*cobra.Command, func() error) {
	cfg := profile.NewConfig()

	// NewProfiler copies the config, so PersistentPreRunE builds the
	// profiler after Cobra has parsed the flags bound to cfg. A profiler
	// built here would carry the empty paths cfg starts with and write
	// nothing.
	var p *profile.Profiler

	rootCmd := &cobra.Command{
		Use:   "nyaml",
		Short: "A terminal YAML utility with syntax highlighting",
		PersistentPreRunE: func(_ *cobra.Command, _ []string) error {
			p = cfg.NewProfiler()

			// Start applies the sampling rates to the whole process, and
			// the flag defaults record every blocking and contention
			// event. Only a block or mutex profile reads those records.
			if p.BlockProfile == "" {
				p.BlockProfileRate = 0
			}

			if p.MutexProfile == "" {
				p.MutexProfileFraction = 0
			}

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

// terminalWidth returns the width of the terminal on stderr less the
// handler's margin, or 88 when stderr is not a terminal. Error output
// wraps to that width.
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
