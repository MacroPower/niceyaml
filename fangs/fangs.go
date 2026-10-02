package fangs

import (
	"fmt"
	"io"
	"regexp"
	"strings"

	"charm.land/fang/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/printer"
)

// ErrorHandler is the [fang.ErrorHandler] that [NewErrorHandler] returns
// with no options, so [*go.jacobcolvin.com/niceyaml.SourceError] values
// render with a [printer.Printer] from [printer.New].
//
//nolint:gocritic // hugeParam: required by [fang.ErrorHandler] signature.
func ErrorHandler(w io.Writer, styles fang.Styles, err error) {
	handleError(w, styles, err, newConfig(nil))
}

// Indent is the number of columns [NewErrorHandler] puts in front of every
// line [printer.Printer.PrintError] renders.
const Indent = 2

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
// with [printer.WithWrap], controls word wrapping. Its styles color the
// highlighted locations, and [printer.WithContextLines] sets the context
// lines around each one.
//
// The handler indents every line by [Indent] columns, and
// [printer.Printer.PrintError] fits each excerpt and the frame of its
// container within the width. To fit a terminal w columns wide, set the
// width to w - [Indent], as the [NewErrorHandler] example does.
func WithPrinter(p *printer.Printer) Option {
	return func(cfg *config) {
		cfg.printer = p
	}
}

// NewErrorHandler creates a new [fang.ErrorHandler] that renders
// [*go.jacobcolvin.com/niceyaml.SourceError] values with their annotated
// source, using opts for the [printer.Printer]. To fit the output in a
// terminal that is width columns wide, subtract [Indent] from the width
// the printer wraps at:
//
//	p := printer.New(printer.WithWrap(width - fangs.Indent))
//
//	err := fang.Execute(ctx, rootCmd,
//	    fang.WithErrorHandler(fangs.NewErrorHandler(fangs.WithPrinter(p))),
//	)
//
// The handler writes the error header, then what
// [printer.Printer.PrintError] renders for err. That output starts with the
// message as a tree, with the context its wrappers added in front and a
// connector before each nested error. The excerpt of each
// [*go.jacobcolvin.com/niceyaml.SourceError] in the error's tree follows,
// so a joined error annotates each failure it holds. The handler indents
// each line by [Indent] columns and ends the output with a blank line. An
// error with no SourceError in its tree renders as that tree alone, and
// the handler drops the line breaks that end it, such as the one after
// the suggestions Cobra lists for an unknown command.
//
// Fang hands the handler a [*colorprofile.Writer] that holds the color
// profile of the error stream. When that profile has no color, as in a
// pipe, a file, or a terminal with NO_COLOR set, the handler writes what
// [niceyaml.FormatError] renders in place of the PrintError output, with
// the context lines of the printer. The carets of FormatError then mark
// each range that color marks in a terminal, and its lines do not wrap.
// A writer of any other type gets the PrintError output.
//
// Unlike [fang.DefaultErrorHandler], which wraps errors in a lipgloss style
// that can break multi-line output, this handler styles only the error
// header and leaves the rendered lines intact.
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

	msg := render(w, err, cfg.printer)

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

// render returns err as the handler writes it to w: what
// [niceyaml.FormatError] renders when w is a [*colorprofile.Writer] whose
// profile has no color, and what [printer.Printer.PrintError] renders
// otherwise.
func render(w io.Writer, err error, p *printer.Printer) string {
	if cw, ok := w.(*colorprofile.Writer); ok && cw.Profile <= colorprofile.ASCII {
		return niceyaml.FormatError(err, p.ContextLines())
	}

	return p.PrintError(err)
}

// isBound reports whether the tree of err holds an error bound to a
// source, for which [printer.Printer.PrintError] renders an excerpt after
// the tree.
func isBound(err error) bool {
	for range niceyaml.Bindings(err) {
		return true
	}

	return false
}

// ignoreN discards the result of a write to the error writer. A handler that
// cannot reach the writer has nowhere to report that.
func ignoreN(_ int, _ error) {}

// argCountError matches the messages of Cobra's argument-count validators,
// such as "accepts at most 2 arg(s), received 3".
var argCountError = regexp.MustCompile(
	`^(accepts (at most \d+|between \d+ and \d+|\d+)|requires at least \d+) arg\(s\)`,
)

// isUsageError reports whether err looks like a Cobra usage error. The
// patterns cover Cobra's flag parser, its command lookup, its argument
// validators, its required-flag check, and its flag groups. They follow
// Cobra's exact wording, so an application error that opens with a word
// such as "accepts" gets no usage hint. Cobra never binds an error to a
// source, so an error that holds a binding gets no usage hint either, even
// when its message opens with a source name that matches a pattern.
// This is a workaround until Cobra exposes a proper usage error type.
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
