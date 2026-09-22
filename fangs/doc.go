// Package fangs provides CLI utilities for applications built with [fang], a
// Cobra companion library.
//
// # Error Handling
//
// [fang]'s default error handler wraps the entire error message in a lipgloss
// style, which breaks multi-line output.
//
// This breaks niceyaml errors that include source context and annotations
// spanning multiple lines.
//
// [ErrorHandler] solves this by styling only the error header while preserving
// the error message formatting. Pass it to [fang.Execute]:
//
//	err := fang.Execute(ctx, rootCmd,
//	    fang.WithErrorHandler(fangs.ErrorHandler),
//	)
//
// # Color Schemes
//
// [ColorScheme] and [ColorSchemeFunc] translate the styles of a
// [style.Styler], such as a theme, to [fang.ColorScheme].
//
// The YAML viewer and CLI help output then use the same colors:
//
//	err := fang.Execute(ctx, rootCmd,
//	    fang.WithColorSchemeFunc(fangs.ColorSchemeFunc(theme.Charm)),
//	)
package fangs
