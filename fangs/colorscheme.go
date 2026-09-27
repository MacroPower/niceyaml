package fangs

import (
	"image/color"

	"charm.land/fang/v2"
	"charm.land/lipgloss/v2"

	"go.jacobcolvin.com/niceyaml/style"
	"go.jacobcolvin.com/niceyaml/style/kind"
)

// ColorScheme creates a [fang.ColorScheme] from the styles of a
// [style.Styler], such as a theme from
// [go.jacobcolvin.com/niceyaml/style/theme] or a
// [go.jacobcolvin.com/niceyaml/style.Styles] value.
//
// This derives CLI styling from the existing theme system, so the YAML
// viewer and CLI help output use the same colors.
func ColorScheme(styles style.Styler) fang.ColorScheme {
	text := styles.Style(kind.Text)
	comment := styles.Style(kind.Comment)
	genericError := styles.Style(kind.GenericError)

	return fang.ColorScheme{
		Base:           text.GetForeground(),
		Title:          styles.Style(kind.NameTag).GetForeground(),
		Description:    text.GetForeground(),
		Codeblock:      text.GetBackground(),
		Program:        styles.Style(kind.NameTag).GetForeground(),
		Command:        styles.Style(kind.NameAnchor).GetForeground(),
		DimmedArgument: comment.GetForeground(),
		Comment:        comment.GetForeground(),
		Flag:           styles.Style(kind.LiteralNumber).GetForeground(),
		FlagDefault:    comment.GetForeground(),
		QuotedString:   styles.Style(kind.LiteralString).GetForeground(),
		Argument:       text.GetForeground(),
		Dash:           styles.Style(kind.Punctuation).GetForeground(),
		ErrorHeader: [2]color.Color{
			genericError.GetForeground(),
			genericError.GetBackground(),
		},
	}
}

// ColorSchemeFunc returns a [fang.ColorSchemeFunc] that creates a
// [fang.ColorScheme] from the styles of a [style.Styler].
//
// This wraps [ColorScheme] for use with [fang.WithColorSchemeFunc].
// Each theme targets a specific light/dark mode, so the returned function
// ignores its [lipgloss.LightDarkFunc] parameter.
func ColorSchemeFunc(styles style.Styler) fang.ColorSchemeFunc {
	return func(_ lipgloss.LightDarkFunc) fang.ColorScheme {
		return ColorScheme(styles)
	}
}

// LightDarkColorSchemeFunc returns a [fang.ColorSchemeFunc] that creates a
// [fang.ColorScheme] from the styles of light on a light terminal
// background and from the styles of dark on a dark one.
//
// This wraps [ColorScheme] for use with [fang.WithColorSchemeFunc]. Help
// output draws most of its text on the terminal's own background, so a
// theme made for one background can be hard to read on the other. Use
// [ColorSchemeFunc] to apply one theme on both.
func LightDarkColorSchemeFunc(light, dark style.Styler) fang.ColorSchemeFunc {
	return func(ld lipgloss.LightDarkFunc) fang.ColorScheme {
		// On a dark background, ld returns its second color, so two
		// distinct colors reveal the background fang detected.
		if ld(color.Black, color.White) == color.White {
			return ColorScheme(dark)
		}

		return ColorScheme(light)
	}
}
