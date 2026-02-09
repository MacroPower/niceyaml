// Package fangs provides CLI utilities for applications built with [fang], a
// Cobra companion library.
//
// # Error Handling
//
// [fang]'s default error handler wraps the entire error message in a lipgloss
// style, which breaks multi-line output.
//
// This is problematic for niceyaml errors that include source context and
// annotations spanning multiple lines.
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
// [ColorScheme] and [ColorSchemeFunc] translate [style.Styles] to
// [fang.ColorScheme], allowing CLI styling to be derived from the existing
// theme system.
//
// This provides consistent colors between the YAML viewer and CLI help output:
//
//	styles, _ := theme.Styles("charm")
//	err := fang.Execute(ctx, rootCmd,
//	    fang.WithColorSchemeFunc(fangs.ColorSchemeFunc(styles)),
//	)
package fangs
