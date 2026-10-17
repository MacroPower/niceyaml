package fangs

import (
	"fmt"
	"io"
	"regexp"
	"slices"
	"strings"

	"charm.land/fang/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/printer"
)

// ErrorHandler is the [fang.ErrorHandler] that [NewErrorHandler] returns
// for an empty option list, so [*go.jacobcolvin.com/niceyaml.SourceError]
// values render with a [printer.Printer] from [printer.New].
//
//nolint:gocritic // hugeParam: required by [fang.ErrorHandler] signature.
func ErrorHandler(w io.Writer, styles fang.Styles, err error) {
	handleError(w, styles, err, newConfig(nil))
}

// Indent is the number of columns [NewErrorHandler] puts in front of every
// line [printer.Printer.PrintReport] renders.
const Indent = 2

// Option configures the [fang.ErrorHandler] that [NewErrorHandler] returns.
//
// Available options:
//   - [WithPrinter]
//   - [WithExcerptOptions]
type Option func(*config)

// config holds the settings an [Option] configures.
type config struct {
	printer *printer.Printer
	excerpt []niceyaml.ExcerptOption
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

	// The handler puts Indent columns in front of every line, so the
	// output wraps to the width less those columns and fits the width
	// together with them.
	if w := cfg.printer.WrapWidth(); w > 0 {
		cfg.printer = cfg.printer.With(printer.WithWrapWidth(max(1, w-Indent)))
	}

	return cfg
}

// WithPrinter is an [Option] that sets the [*printer.Printer] that renders
// errors through [printer.Printer.PrintReport]. [printer.WithWrapWidth]
// sets the width of the printer, and the output of the handler fits that
// width. The styles of the printer color the highlighted locations.
// [WithExcerptOptions] sets the context lines around each location and
// how much of a long line an excerpt shows.
//
// The handler indents every line by [Indent] columns and wraps what
// [printer.Printer.PrintReport] renders to the width less those columns.
// The output then fits a terminal of that width. To fit a terminal w
// columns wide, set the width to w, as the [NewErrorHandler] example
// does.
func WithPrinter(p *printer.Printer) Option {
	return func(cfg *config) {
		cfg.printer = p
	}
}

// WithExcerptOptions is an [Option] that sets the [niceyaml.ExcerptOption]
// values that the handler builds the report of each error with.
// [niceyaml.WithContextLines] sets the context lines around each location,
// and [niceyaml.WithExcerptWidth] sets how much of a long line an excerpt
// shows. The styled output and the plain output of the handler draw one
// report, so both show the same lines. A later call replaces the options
// of an earlier one.
//
// WithExcerptOptions copies the options it gets, so a caller that edits
// the slice it passed does not change the handler.
func WithExcerptOptions(opts ...niceyaml.ExcerptOption) Option {
	opts = slices.Clone(opts)

	return func(cfg *config) {
		cfg.excerpt = opts
	}
}

// NewErrorHandler creates a new [fang.ErrorHandler] that renders
// [*go.jacobcolvin.com/niceyaml.SourceError] values with their annotated
// source. Each option in opts configures one setting of the handler, such
// as the [printer.Printer] it renders with. To fit the output in a
// terminal that is width columns wide, give the printer that width:
//
//	p := printer.New(printer.WithWrapWidth(width))
//
//	err := fang.Execute(ctx, rootCmd,
//	    fang.WithErrorHandler(fangs.NewErrorHandler(fangs.WithPrinter(p))),
//	)
//
// The handler writes the error header, then what
// [printer.Printer.PrintReport] renders for the [niceyaml.ErrorReport] of
// err. The handler builds that report with the options that
// [WithExcerptOptions] sets. The output starts with the message as a tree,
// with the context its wrappers added in front of each message and a
// connector before each error below another. The excerpt of each
// [*go.jacobcolvin.com/niceyaml.SourceError] in the error's tree follows,
// so a joined error annotates each failure it holds.
//
// The warnings in the error render as the errors do. They include the
// warnings that [niceyaml.Warnings.Report] puts beside the errors of a
// call. Each has the "warning:" label after its position and its
// location marked in the warning styles of the printer.
//
// The handler indents each line by [Indent] columns and ends the output
// with a blank line. An error without a SourceError in its tree renders as
// that tree alone, without the line breaks that end it. One such line
// break follows the suggestions that Cobra lists for an unknown command.
//
// Fang hands the handler a [*colorprofile.Writer] that holds the color
// profile of the error stream. When that profile does not support color,
// as in a pipe, a file, or a terminal with NO_COLOR set, the handler
// replaces the PrintReport output. It writes what [niceyaml.FormatError]
// renders with the same options. The carets of FormatError then mark each
// range that color marks in a terminal, and its lines do not wrap. A
// writer of any other type gets the PrintReport output.
//
// [fang.DefaultErrorHandler] wraps errors in a lipgloss style that breaks
// multi-line output. This handler styles only the error header and leaves
// the rendered lines intact.
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

	indent := strings.Repeat(" ", Indent)

	msg := render(w, err, cfg)

	// A blank line of the handler's own follows the message, so the line
	// breaks that end a message, such as the one after the suggestions
	// Cobra lists, would add only blank lines before it. The output for
	// an error bound to a source may end with an excerpt, which keeps the
	// blank lines of the document it shows.
	if !isBound(err) {
		msg = strings.TrimRight(msg, "\n")
	}

	for line := range strings.SplitSeq(msg, "\n") {
		ignoreN(fmt.Fprintln(w, indent+line))
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

// render returns err as the handler writes it to w. It returns what
// [niceyaml.FormatError] renders when w is a [*colorprofile.Writer] whose
// profile does not support color, and what [printer.Printer.PrintReport]
// renders otherwise. Both draw the report that the excerpt options of cfg
// build.
func render(w io.Writer, err error, cfg config) string {
	if cw, ok := w.(*colorprofile.Writer); ok && cw.Profile <= colorprofile.ASCII {
		return niceyaml.FormatError(err, cfg.excerpt...)
	}

	return cfg.printer.PrintReport(niceyaml.NewErrorReport(err, cfg.excerpt...))
}

// isBound reports whether the tree of err holds an error bound to a
// source, for which [printer.Printer.PrintReport] renders an excerpt
// after the tree.
func isBound(err error) bool {
	for range niceyaml.Bindings(err) {
		return true
	}

	return false
}

// ignoreN discards the result of a write to the error writer. The handler
// reports only to that writer, so it cannot report a write that fails.
func ignoreN(_ int, _ error) {}

// argCountError matches the messages of Cobra's argument-count validators,
// such as `accepts at most 2 arg(s), received 3`.
var argCountError = regexp.MustCompile(
	`^(accepts (at most \d+|between \d+ and \d+|\d+)|requires at least \d+) arg\(s\)`,
)

// isUsageError reports whether err looks like a Cobra usage error. The
// patterns cover Cobra's flag parser, its command lookup, its argument
// validators, its required-flag check, and its flag groups. They follow
// Cobra's exact wording, so an application error that opens with a word
// such as "accepts" does not get a usage hint. Cobra never binds an error
// to a source. So an error that holds a binding does not get a usage hint
// either, even when its message opens with a source name that matches a
// pattern. These patterns are a workaround until Cobra exposes a usage
// error type.
// See: https://github.com/spf13/cobra/pull/2266
func isUsageError(err error) bool {
	if err == nil {
		return false
	}

	for range niceyaml.Bindings(err) {
		return false
	}

	s := err.Error()
	if argCountError.MatchString(s) {
		return true
	}

	for _, prefix := range []string{
		"flag needs an argument:",
		"unknown flag:",
		"unknown shorthand flag:",
		"bad flag syntax:",
		`unknown command "`,
		`invalid argument "`,
		"required flag(s)",
		"if any flags in the group",
		"at least one of the flags in the group",
	} {
		if strings.HasPrefix(s, prefix) {
			return true
		}
	}

	return false
}
