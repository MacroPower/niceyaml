package fangs

import (
	"fmt"
	"io"
	"strings"

	"charm.land/fang/v2"
	"charm.land/lipgloss/v2"

	"go.jacobcolvin.com/niceyaml"
)

// ErrorHandler is the [fang.ErrorHandler] that [NewErrorHandler] returns
// with no options, so [niceyaml.SourceError] values render with a default
// [niceyaml.Printer].
//
//nolint:gocritic // hugeParam: required by [fang.ErrorHandler] signature.
func ErrorHandler(w io.Writer, styles fang.Styles, err error) {
	handleError(w, styles, err, nil)
}

// NewErrorHandler creates a new [fang.ErrorHandler] that renders
// [niceyaml.SourceError] values with their annotated source, using opts for
// the [niceyaml.Printer] and the context lines:
//
//	fang.WithErrorHandler(fangs.NewErrorHandler(
//		niceyaml.WithPrinter(niceyaml.NewPrinter(niceyaml.WithWidth(width))),
//	))
//
// The handler prints err with the %+v verb, and a [niceyaml.SourceError] at
// the top with [niceyaml.SourceError.Render], which is its message followed
// by [niceyaml.SourceError.Detail]. A SourceError behind other wrapping
// never reaches its own [fmt.Formatter], so expandYAMLErrors substitutes
// that form in place. It does so for every SourceError in the tree, so a
// joined error annotates each failure it holds. Unlike
// [fang.DefaultErrorHandler], which wraps errors in a lipgloss style that
// can break multi-line output, this handler applies styling only to the
// error header, keeping the rendered lines intact.
func NewErrorHandler(opts ...niceyaml.DetailOption) fang.ErrorHandler {
	return func(w io.Writer, styles fang.Styles, err error) {
		handleError(w, styles, err, opts)
	}
}

// handleError renders err to w as [NewErrorHandler] describes.
//
//nolint:gocritic // hugeParam: fang.Styles is what the handler receives.
func handleError(w io.Writer, styles fang.Styles, err error, opts []niceyaml.DetailOption) {
	mustN(fmt.Fprintln(w, styles.ErrorHeader.String()))

	var msg string

	//nolint:errorlint // Identity of the top-level error, not a chain search.
	if top, ok := err.(*niceyaml.SourceError); ok {
		msg = top.Render(opts...)
	} else {
		msg = expandYAMLErrors(fmt.Sprintf("%+v", err), yamlErrors(err), opts)
	}

	// Apply margin manually to each line to avoid lipgloss block padding.
	for line := range strings.SplitSeq(msg, "\n") {
		mustN(fmt.Fprintln(w, "  "+line))
	}

	mustN(fmt.Fprintln(w))

	if isUsageError(err) {
		mustN(fmt.Fprintln(w, lipgloss.JoinHorizontal(
			lipgloss.Left,
			styles.ErrorText.UnsetWidth().Render("Try"),
			styles.Program.Flag.PaddingLeft(1).Render("--help"),
			styles.ErrorText.UnsetWidth().UnsetMargins().UnsetTransform().PaddingLeft(1).Render("for usage."),
		)))
		mustN(fmt.Fprintln(w))
	}
}

func mustN(_ int, err error) {
	if err != nil {
		panic(err)
	}
}

// yamlErrors returns the outermost [niceyaml.SourceError] values in err's
// tree, in the order their messages appear in the rendered text. It stops at
// each SourceError it finds, because a SourceError renders the errors it
// holds.
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

// expandYAMLErrors replaces the plain rendering of each error in errs inside
// msg with its [niceyaml.SourceError.Render] form under opts, so the
// annotated source appears where the error does.
//
// Each expansion consumes msg up to and including the text it replaced, so two
// errors that render identically expand one after the other instead of both
// landing on the first occurrence.
//
// Falls back to appending the detail when msg does not hold an error's
// rendering verbatim, which happens when a wrapper reformats the message it
// wraps.
func expandYAMLErrors(msg string, errs []*niceyaml.SourceError, opts []niceyaml.DetailOption) string {
	var (
		sb       strings.Builder
		rest     = msg
		appended []string
	)

	for _, yamlErr := range errs {
		plain := yamlErr.Error()

		at := -1
		if plain != "" {
			at = strings.Index(rest, plain)
		}

		if at < 0 {
			detail, err := yamlErr.Detail(opts...)
			if err == nil {
				appended = append(appended, detail)
			}

			continue
		}

		sb.WriteString(rest[:at])
		sb.WriteString(yamlErr.Render(opts...))

		rest = rest[at+len(plain):]
	}

	sb.WriteString(rest)

	for _, detail := range appended {
		sb.WriteString("\n\n")
		sb.WriteString(detail)
	}

	return sb.String()
}

// isUsageError returns true if err appears to be a Cobra usage error.
// This is a workaround until Cobra exposes a proper usage error type.
// See: https://github.com/spf13/cobra/pull/2266
func isUsageError(err error) bool {
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
