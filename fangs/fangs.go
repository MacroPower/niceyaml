package fangs

import (
	"fmt"
	"io"
	"strings"

	"charm.land/fang/v2"
	"charm.land/lipgloss/v2"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/printer"
)

// ErrorHandler is the [fang.ErrorHandler] that [NewErrorHandler] returns
// with no options, so [niceyaml.SourceError] values render with a default
// [printer.Printer] and two lines of context.
//
//nolint:gocritic // hugeParam: required by [fang.ErrorHandler] signature.
func ErrorHandler(w io.Writer, styles fang.Styles, err error) {
	handleError(w, styles, err, newConfig(nil))
}

// Option configures the [fang.ErrorHandler] that [NewErrorHandler] returns.
//
// Available options:
//   - [WithPrinter]
//   - [WithContextLines]
type Option func(*config)

// config holds the settings an [Option] configures.
type config struct {
	printer *printer.Printer
	context int
}

// newConfig applies opts over the defaults: a [printer.Printer] from
// [printer.New] and two lines of context.
func newConfig(opts []Option) config {
	cfg := config{context: defaultContextLines}
	for _, opt := range opts {
		opt(&cfg)
	}

	if cfg.printer == nil {
		cfg.printer = printer.New()
	}

	return cfg
}

// defaultContextLines is the number of context lines shown around an error
// when [WithContextLines] is not given, which matches the %+v verb.
const defaultContextLines = 2

// WithPrinter is an [Option] that sets the [*printer.Printer] that renders
// the source excerpt of a [niceyaml.SourceError]. The printer's width, set
// with [printer.WithWidth], controls word wrapping, and its styles color
// the highlighted locations.
func WithPrinter(p *printer.Printer) Option {
	return func(cfg *config) {
		cfg.printer = p
	}
}

// WithContextLines is an [Option] that sets the number of context lines
// shown around each error location. The default is 2, and a negative count
// shows the error lines alone, as 0 does.
func WithContextLines(lines int) Option {
	return func(cfg *config) {
		cfg.context = lines
	}
}

// NewErrorHandler creates a new [fang.ErrorHandler] that renders
// [niceyaml.SourceError] values with their annotated source, using opts for
// the [printer.Printer] and the context lines:
//
//	fang.WithErrorHandler(fangs.NewErrorHandler(
//		fangs.WithPrinter(printer.New(printer.WithWidth(width))),
//	))
//
// The handler prints a [niceyaml.SourceError] at the top with
// [niceyaml.SourceError.Render], which is its message followed by its
// [niceyaml.SourceError.Excerpt]. Any other error prints with the %+v
// verb, and the message stays as the error's wrappers wrote it. The message
// of a SourceError inside that error is part of that text already, so the
// handler prints the excerpt of each SourceError in the tree below the
// message, in the order the SourceErrors appear in it, and a joined error
// annotates each failure it holds. Unlike [fang.DefaultErrorHandler], which
// wraps errors in a lipgloss style that can break multi-line output, this
// handler applies styling only to the error header, keeping the rendered
// lines intact.
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

	var parts []string

	if top, ok := err.(*niceyaml.SourceError); ok { //nolint:errorlint // Mirrors %+v, which formats the top-level value.
		parts = append(parts, top.Render(cfg.printer, cfg.context))
	} else {
		parts = append(parts, fmt.Sprintf("%+v", err))

		for _, yamlErr := range yamlErrors(err) {
			excerpt, excerptErr := yamlErr.Excerpt(cfg.context)
			if excerptErr == nil {
				parts = append(parts, cfg.printer.Print(excerpt))
			}
		}
	}

	msg := strings.Join(parts, "\n\n")

	for line := range strings.SplitSeq(msg, "\n") {
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

// yamlErrors returns the outermost [niceyaml.SourceError] values in err's
// tree, in the order their messages appear in the rendered text. It stops at
// each SourceError it finds, because a SourceError's detail covers the
// errors it holds.
func yamlErrors(err error) []*niceyaml.SourceError {
	var found []*niceyaml.SourceError

	var walk func(error)

	walk = func(cur error) {
		for cur != nil {
			//nolint:errorlint // Identity at this level, not a chain search.
			if yamlErr, ok := cur.(*niceyaml.SourceError); ok {
				found = append(found, yamlErr)

				return
			}

			switch x := cur.(type) { //nolint:errorlint // Unwrap shape, not a target match.
			case interface{ Unwrap() error }:
				cur = x.Unwrap()
			case interface{ Unwrap() []error }:
				for _, sub := range x.Unwrap() {
					walk(sub)
				}

				return

			default:
				return
			}
		}
	}

	walk(err)

	return found
}

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
