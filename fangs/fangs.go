package fangs

import (
	"fmt"
	"io"
	"os"
	"regexp"
	"runtime"
	"slices"
	"strings"

	"charm.land/fang/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/term"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/printer"
	"go.jacobcolvin.com/niceyaml/style"
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
//   - [WithLightDarkStyles]
type Option func(*config)

// config holds the settings an [Option] configures.
type config struct {
	printer *printer.Printer
	// The styles for a light and a dark terminal, without their surfaces.
	// Both are nil until [WithLightDarkStyles] sets them.
	light, dark style.Styler
	excerpt     []niceyaml.ExcerptOption
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
// [WithExcerptOptions] sets the context lines around each location, how
// much of a long line an excerpt shows, and how many problems it marks on
// one line.
//
// The handler indents every line by [Indent] columns and wraps what
// [printer.Printer.PrintReport] renders to the width less those columns.
// The output then fits a terminal of that width. A printer without a
// width fits the terminal of the error stream, as [NewErrorHandler]
// describes.
func WithPrinter(p *printer.Printer) Option {
	return func(cfg *config) {
		cfg.printer = p
	}
}

// WithLightDarkStyles is an [Option] that renders errors with the styles
// of light on a light terminal background and with the styles of dark on a
// dark one. It mirrors [LightDarkColorSchemeFunc], so help output and
// error excerpts can use the same pair of themes:
//
//	err := fang.Execute(ctx, rootCmd,
//	    fang.WithErrorHandler(fangs.NewErrorHandler(
//	        fangs.WithLightDarkStyles(theme.CharmLight, theme.Charm),
//	    )),
//	    fang.WithColorSchemeFunc(fangs.LightDarkColorSchemeFunc(theme.CharmLight, theme.Charm)),
//	)
//
// For each error it renders with color, the handler asks the terminal
// behind the error stream for its background. It asks that stream rather
// than stdout, so a run with stdout redirected still matches the terminal
// that shows the error. When the error stream is not a terminal, or the
// terminal does not report its background, the handler picks dark, as
// [lipgloss.HasDarkBackground] does. A terminal that does not answer
// delays the error output until the query times out.
//
// The styles replace the styles of the printer that [WithPrinter] sets,
// and the printer keeps its other settings, such as its gutter and its
// width. The handler removes the surface of each theme, as
// [style.WithSurface] does with nil, so the text shows the terminal's own
// background, as help output does. That removal covers a [style.Styles],
// a [*style.Styles], and a styler with a Styles method that returns
// [style.Styles], such as a theme. Any other styler renders as it is. A
// nil light or dark, or one holding a nil pointer, selects the palette of
// [style.Default] for that background.
func WithLightDarkStyles(light, dark style.Styler) Option {
	if isNil(light) {
		light = style.Default(false)
	}

	if isNil(dark) {
		dark = style.Default(true)
	}

	light, dark = withoutSurface(light), withoutSurface(dark)

	return func(cfg *config) {
		cfg.light, cfg.dark = light, dark
	}
}

// withoutSurface returns styles without the surface of
// [style.WithSurface], or styles itself when it does not hold a
// [style.Styles].
func withoutSurface(styles style.Styler) style.Styler {
	switch s := styles.(type) {
	case style.Styles:
		return s.With(style.WithSurface(nil))
	case *style.Styles:
		return s.With(style.WithSurface(nil))
	case interface{ Styles() style.Styles }:
		return s.Styles().With(style.WithSurface(nil))
	default:
		return styles
	}
}

// WithExcerptOptions is an [Option] that sets the [niceyaml.ExcerptOption]
// values that the handler builds the report of each error with.
// [niceyaml.WithContextLines] sets the context lines around each location,
// [niceyaml.WithExcerptWidth] sets how much of a long line an excerpt
// shows, and [niceyaml.WithExcerptMarks] sets how many problems an excerpt
// marks on one line. [niceyaml.WithProblemLimit] sets how many problems
// the report shows, with a last row that counts the rest:
//
//	h := fangs.NewErrorHandler(fangs.WithExcerptOptions(niceyaml.WithProblemLimit(50)))
//
// The styled output and the plain output of the handler draw one report,
// so both show the same lines. A later call replaces the options of an
// earlier one.
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
// as the [printer.Printer] it renders with:
//
//	p := printer.New(printer.WithGutter(printer.NoGutter))
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
// When the printer has no width and the error stream is a terminal, the
// handler wraps the output to the width of that terminal at each call. A
// width from [printer.WithWrapWidth] takes precedence, and output to a
// stream that is not a terminal does not wrap.
//
// Fang hands the handler a [*colorprofile.Writer] that holds the color
// profile of the error stream and forwards to that stream. When that
// profile does not support color, as in a pipe, a file, or a terminal
// with NO_COLOR set, the handler replaces the PrintReport output. It
// writes what [niceyaml.FormatError] renders with the same options. The
// carets of FormatError then mark each range that color marks in a
// terminal, and its lines do not wrap. A writer of any other type gets the
// PrintReport output.
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

	return printerFor(w, cfg).PrintReport(niceyaml.NewErrorReport(err, cfg.excerpt...))
}

// printerFor returns the printer of cfg fitted to the terminal behind w.
// It picks the styles of [WithLightDarkStyles] for the background of that
// terminal, and it gives a printer without a width the width of that
// terminal less [Indent].
func printerFor(w io.Writer, cfg config) *printer.Printer {
	p := cfg.printer
	f, isTerm := terminal(w)

	if cfg.dark != nil {
		styles := cfg.dark
		if isTerm && !lipgloss.HasDarkBackground(replyInput(f), f) {
			styles = cfg.light
		}

		p = p.With(printer.WithStyles(styles))
	}

	if isTerm && p.WrapWidth() == 0 {
		width, _, err := term.GetSize(f.Fd())
		if err == nil && width > 0 {
			p = p.With(printer.WithWrapWidth(max(1, width-Indent)))
		}
	}

	return p
}

// replyInput returns the file that [lipgloss.HasDarkBackground] reads the
// reply of the terminal behind f from. On Unix, the terminal device of f
// reads as well as it writes, so f serves, even when stdin is a pipe. On
// Windows, a console reads its replies from its input handle and never
// from the output handle of f. Stdin is that input handle, and lipgloss
// opens the console input in its place when stdin is redirected.
func replyInput(f term.File) term.File {
	if runtime.GOOS == "windows" {
		return os.Stdin
	}

	return f
}

// terminal returns the terminal that w writes to, and whether w writes to
// one. It looks through a [*colorprofile.Writer] to the writer it forwards
// to.
func terminal(w io.Writer) (term.File, bool) {
	if cw, ok := w.(*colorprofile.Writer); ok {
		w = cw.Forward
	}

	f, ok := w.(term.File)
	if !ok || !term.IsTerminal(f.Fd()) {
		return nil, false
	}

	return f, true
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
