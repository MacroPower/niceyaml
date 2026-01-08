// Package fangs provides utilities for integrating niceyaml with
// [github.com/charmbracelet/fang], a Cobra companion library.
//
// The primary export is [ErrorHandler], a custom error handler that preserves
// multi-line error formatting. This is particularly useful for niceyaml errors,
// which include source context and annotations spanning multiple lines.
package fangs

import (
	"fmt"
	"io"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/fang"
)

// ErrorHandler is an implementation of [fang.ErrorHandler] that preserves
// multi-line error formatting. Unlike [fang.DefaultErrorHandler], which wraps
// errors in a lipgloss style that can break multi-line output, this handler
// applies styling only to the error header, keeping the error message intact.
// This allows niceyaml errors with source context and annotations to render
// correctly.
//
//nolint:gocritic // hugeParam: required by [fang.ErrorHandler] signature.
func ErrorHandler(w io.Writer, styles fang.Styles, err error) {
	mustN(fmt.Fprintln(w, styles.ErrorHeader.String()))
	mustN(fmt.Fprintln(w, lipgloss.NewStyle().MarginLeft(2).Render(err.Error())))
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

func must(err error) {
	if err != nil {
		panic(err)
	}
}

func mustN(_ int, err error) {
	if err != nil {
		panic(err)
	}
}

// XXX: this is a hack to detect usage errors.
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
