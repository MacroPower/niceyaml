package fangs

import (
	"fmt"
	"io"
	"strings"

	"charm.land/fang/v2"
	"charm.land/lipgloss/v2"

	"go.jacobcolvin.com/niceyaml"
)

// ErrorHandler is an implementation of [fang.ErrorHandler] that renders
// [niceyaml.Error] values with their annotated source.
//
// It prints err with the %+v verb, which a [niceyaml.Error] renders as its
// message followed by [niceyaml.Error.Detail]. An Error behind other wrapping
// never reaches its own [fmt.Formatter], so expandYAMLErrors substitutes that
// form in place. It does so for every Error in the tree, so a joined error
// annotates each failure it holds. Unlike [fang.DefaultErrorHandler], which
// wraps errors in a lipgloss style that can break multi-line output, this
// handler applies styling only to the error header, keeping the rendered lines
// intact.
//
//nolint:gocritic // hugeParam: required by [fang.ErrorHandler] signature.
func ErrorHandler(w io.Writer, styles fang.Styles, err error) {
	mustN(fmt.Fprintln(w, styles.ErrorHeader.String()))

	msg := fmt.Sprintf("%+v", err)

	//nolint:errorlint // Identity of the top-level error, not a chain search.
	if _, top := err.(*niceyaml.Error); !top {
		msg = expandYAMLErrors(msg, yamlErrors(err))
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

// yamlErrors returns the outermost [niceyaml.Error] values in err's tree, in
// the order their messages appear in the rendered text. It stops at each Error
// it finds, because an Error renders the errors it holds.
func yamlErrors(err error) []*niceyaml.Error {
	var found []*niceyaml.Error

	var walk func(error)

	walk = func(cur error) {
		for cur != nil {
			//nolint:errorlint // Identity at this level, not a chain search.
			if yamlErr, ok := cur.(*niceyaml.Error); ok {
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
// msg with its %+v form, so the annotated source appears where the error does
// and no nested message shows up as both a bullet and an annotation.
//
// Each expansion consumes msg up to and including the text it replaced, so two
// errors that render identically expand one after the other instead of both
// landing on the first occurrence.
//
// Falls back to appending the detail when msg does not hold an error's
// rendering verbatim, which happens when a wrapper reformats the message it
// wraps.
func expandYAMLErrors(msg string, errs []*niceyaml.Error) string {
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
			if detail := yamlErr.Detail(); detail != "" {
				appended = append(appended, detail)
			}

			continue
		}

		sb.WriteString(rest[:at])
		fmt.Fprintf(&sb, "%+v", yamlErr)

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
