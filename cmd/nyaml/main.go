package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"charm.land/fang/v2"
	"github.com/charmbracelet/x/term"
	"github.com/spf13/cobra"
	"go.jacobcolvin.com/x/cobras/profile"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/fangs"
	"go.jacobcolvin.com/niceyaml/printer"
	"go.jacobcolvin.com/niceyaml/style/theme"
)

func main() {
	rootCmd, stopProfiler := newRootCmd()

	// The error printer carries the terminal width so annotated source
	// excerpts wrap to it.
	errPrinter := printer.New(printer.WithWrap(terminalWidth()))

	ctx, stop := notifyContext(context.Background())

	err := fang.Execute(ctx, rootCmd,
		fang.WithErrorHandler(fangs.NewErrorHandler(fangs.WithPrinter(errPrinter))),
		fang.WithColorSchemeFunc(helpColorScheme()),
	)

	stop()

	code := exitCode(err)

	// The error handler of fang.Execute renders only the errors it returns,
	// so main prints the error of a profile that fails to write this late.
	stopErr := stopProfiler()
	if stopErr != nil {
		fmt.Fprintln(os.Stderr, "stop profiler:", stopErr)

		code = exitFailure
	}

	if code != 0 {
		os.Exit(code)
	}
}

// The statuses nyaml exits with when a command returns an error, which
// [exitCode] picks.
const (
	// The status of a run whose every problem is a fault of a document,
	// such as a syntax error or a schema violation.
	exitInvalid = 1

	// The status of a run that met any other error, such as a file that
	// does not read, a schema that does not load, a canceled run, or a
	// flag that does not parse.
	exitFailure = 2
)

// exitCode returns the status nyaml exits with for err, the error a
// command returned. It is 0 for no error, [exitInvalid] when
// [niceyaml.IsInvalid] reports err, and [exitFailure] otherwise. A run
// whose documents are at fault thus exits 1 only when nothing else went
// wrong, so a script tells a document to fix from a check to retry.
func exitCode(err error) int {
	switch {
	case err == nil:
		return 0
	case niceyaml.IsInvalid(err):
		return exitInvalid
	default:
		return exitFailure
	}
}

// notifyContext returns a copy of parent that the first SIGINT or SIGTERM
// cancels. The run then stops its work, and main still stops the
// profiler, which writes the profiles. The signal then gets its default
// action back, so a second one kills a run stuck in work that ignores
// the context. Call the returned function once the command returns.
func notifyContext(parent context.Context) (context.Context, context.CancelFunc) {
	ctx, stop := signal.NotifyContext(parent, os.Interrupt, syscall.SIGTERM)

	go func() {
		<-ctx.Done()
		stop()
	}()

	return ctx, stop
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

		return p.Stop() //nolint:wrapcheck // main reports it as it is.
	}
}

// terminalWidth returns the width of the terminal on stderr, or 90 when
// stderr is not a terminal, less the [fangs.Indent] the error handler puts
// in front of each line. Error output with styles wraps to that width, and
// the plain output the handler writes to a stream without color does not
// wrap.
func terminalWidth() int {
	width := 90

	if term.IsTerminal(os.Stderr.Fd()) {
		w, _, err := term.GetSize(os.Stderr.Fd())
		if err == nil {
			width = w
		}
	}

	return max(0, width-fangs.Indent)
}
