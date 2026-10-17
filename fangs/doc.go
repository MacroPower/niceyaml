// Package fangs provides CLI utilities for applications built with [fang], a
// Cobra companion library.
//
// # Error Handling
//
// The default error handler of [fang] wraps the entire error message in a
// lipgloss style. That style breaks multi-line output, such as the source
// excerpt and annotations of a niceyaml error.
//
// [ErrorHandler] styles only the error header and prints the message as
// [printer.Printer.PrintError] renders it, indented by [Indent] columns,
// so each excerpt keeps its own layout. Pass it to [fang.Execute]:
//
//	err := fang.Execute(ctx, rootCmd,
//	    fang.WithErrorHandler(fangs.ErrorHandler),
//	)
//
// When the error stream does not support color, as in a pipe or under
// NO_COLOR, the handler prints the plain text of [niceyaml.FormatError]
// instead. Carets in that text mark each range.
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
//
// Help output draws most of its text on the terminal's own background.
// Each theme targets one background, so the pale text of a dark theme is
// hard to read on a light terminal. [LightDarkColorSchemeFunc] takes one
// theme for each background and picks the one that matches the background
// fang detects:
//
//	light, _ := theme.Builtin().Get("github")
//	err := fang.Execute(ctx, rootCmd,
//	    fang.WithColorSchemeFunc(fangs.LightDarkColorSchemeFunc(light, theme.Charm)),
//	)
package fangs
