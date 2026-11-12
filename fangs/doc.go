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
// instead. Carets in that text mark each range. When the error stream is a
// terminal, the handler wraps the excerpts to its width.
//
// [WithLightDarkStyles] colors the excerpts with one theme on a light
// terminal and another on a dark one. It reads the background of the
// terminal behind the error stream. [LightDarkColorSchemeFunc] picks the
// theme of help output from the background that fang detects, so help
// and errors can take the same pair of themes:
//
//	err := fang.Execute(ctx, rootCmd,
//	    fang.WithErrorHandler(fangs.NewErrorHandler(
//	        fangs.WithLightDarkStyles(theme.CharmLight(), theme.Charm()),
//	    )),
//	    fang.WithColorSchemeFunc(fangs.LightDarkColorSchemeFunc(theme.CharmLight(), theme.Charm())),
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
//	    fang.WithColorSchemeFunc(fangs.ColorSchemeFunc(theme.Charm())),
//	)
//
// Help output draws most of its text on the terminal's own background.
// Each theme targets one background, so the pale text of a dark theme is
// hard to read on a light terminal. [LightDarkColorSchemeFunc] takes one
// theme for each background and picks the one that matches the background
// fang detects:
//
//	err := fang.Execute(ctx, rootCmd,
//	    fang.WithColorSchemeFunc(fangs.LightDarkColorSchemeFunc(theme.CharmLight(), theme.Charm())),
//	)
//
// A nil theme selects the palette of [style.Default] for its background,
// so two nils follow the terminal with the default palettes:
//
//	err := fang.Execute(ctx, rootCmd,
//	    fang.WithColorSchemeFunc(fangs.LightDarkColorSchemeFunc(nil, nil)),
//	)
package fangs
