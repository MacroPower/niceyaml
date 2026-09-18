package fangs

import (
	"fmt"
	"io"
	"strings"

	"charm.land/fang/v2"
	"charm.land/lipgloss/v2"

	"go.jacobcolvin.com/niceyaml/printer"
)

// ErrorHandler is the [fang.ErrorHandler] that [NewErrorHandler] returns
// with no options, so [niceyaml.SourceError] values render with a
// [printer.Printer] from [printer.New].
//
//nolint:gocritic // hugeParam: required by [fang.ErrorHandler] signature.
func ErrorHandler(w io.Writer, styles fang.Styles, err error) {
	handleError(w, styles, err, newConfig(nil))
}

// Option configures the [fang.ErrorHandler] that [NewErrorHandler] returns.
//
// Available options:
//   - [WithPrinter]
type Option func(*config)

// config holds the settings an [Option] configures.
type config struct {
	printer *printer.Printer
}

// newConfig applies opts over the defaults: a [printer.Printer] from
// [printer.New].
func newConfig(opts []Option) config {
	var cfg config

	for _, opt := range opts {
		opt(&cfg)
	}

	if cfg.printer == nil {
		cfg.printer = printer.New()
	}

	return cfg
}

// WithPrinter is an [Option] that sets the [*printer.Printer] that renders
// errors through [printer.Printer.PrintError]. The printer's width, set
// with [printer.WithWidth], controls word wrapping, its styles color the
// highlighted locations, and [printer.WithContextLines] sets the context
// lines around each one.
func WithPrinter(p *printer.Printer) Option {
	return func(cfg *config) {
		cfg.printer = p
	}
}

// NewErrorHandler creates a new [fang.ErrorHandler] that renders
// [niceyaml.SourceError] values with their annotated source, using opts for
// the [printer.Printer]:
//
//	err := fang.Execute(ctx, rootCmd,
//	    fang.WithErrorHandler(fangs.NewErrorHandler(
//	        fangs.WithPrinter(printer.New(printer.WithWidth(width))),
//	    )),
//	)
//
// The handler writes the error header, then what
// [printer.Printer.PrintError] renders for err: the message as its
// wrappers wrote it, then the excerpt of each [niceyaml.SourceError] in
// the error's tree, so a joined error annotates each failure it holds.
// Unlike [fang.DefaultErrorHandler], which wraps errors in a lipgloss style
// that can break multi-line output, this handler applies styling only to
// the error header, keeping the rendered lines intact.
func NewErrorHandler(opts ...Option) fang.ErrorHandler {
	cfg := newConfig(opts)

	return func(w io.Writer, styles fang.Styles, err error) {
		handleError(w, styles, err, cfg)
	}
}

// handleError renders err to w as [NewErrorHandler] describes.
//
//nolint:gocritic // hugeParam: fang.Styles is what the handler receives.
func handleError(w io.Writer, styles fang.Styles, err error, cfg config) {
	ignoreN(fmt.Fprintln(w, styles.ErrorHeader.String()))

	for line := range strings.SplitSeq(cfg.printer.PrintError(err), "\n") {
		ignoreN(fmt.Fprintln(w, "  "+line))
	}

	ignoreN(fmt.Fprintln(w))

	if isUsageError(err) {
		ignoreN(fmt.Fprintln(w, lipgloss.JoinHorizontal(
			lipgloss.Left,
			styles.ErrorText.UnsetWidth().Render("Try"),
			styles.Program.Flag.PaddingLeft(1).Render("--help"),
			styles.ErrorText.UnsetWidth().UnsetMargins().UnsetTransform().PaddingLeft(1).Render("for usage."),
		)))
		ignoreN(fmt.Fprintln(w))
	}
}

// ignoreN discards the result of a write to the error writer. A handler that
// cannot reach the writer has nowhere to report that.
func ignoreN(_ int, _ error) {}

// isUsageError returns true if err appears to be a Cobra usage error.
// This is a workaround until Cobra exposes a proper usage error type.
// See: https://github.com/spf13/cobra/pull/2266
func isUsageError(err error) bool {
	if err == nil {
		return false
	}

	s := err.Error()
	for _, prefix := range []string{
		"flag needs an argument:",
		"unknown flag:",
		"unknown shorthand flag:",
		"unknown command",
		"invalid argument",
	} {
		if strings.HasPrefix(s, prefix) {
			return true
		}
	}

	return false
}
