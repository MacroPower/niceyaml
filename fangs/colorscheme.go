package fangs

import (
	"image/color"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/fang"

	"go.jacobcolvin.com/niceyaml/style"
)

// ColorScheme creates a [fang.ColorScheme] from [style.Styles].
//
// This allows CLI styling to be derived from the existing theme system,
// providing consistent colors between the YAML viewer and CLI help output.
func ColorScheme(styles style.Styles) fang.ColorScheme {
	text := styles.Style(style.Text)
	comment := styles.Style(style.Comment)
	genericError := styles.Style(style.GenericError)

	return fang.ColorScheme{
		Base:           text.GetForeground(),
		Title:          styles.Style(style.NameTag).GetForeground(),
		Description:    text.GetForeground(),
		Codeblock:      text.GetBackground(),
		Program:        styles.Style(style.NameTag).GetForeground(),
		Command:        styles.Style(style.NameAnchor).GetForeground(),
		DimmedArgument: comment.GetForeground(),
		Comment:        comment.GetForeground(),
		Flag:           styles.Style(style.LiteralNumber).GetForeground(),
		FlagDefault:    comment.GetForeground(),
		QuotedString:   styles.Style(style.LiteralString).GetForeground(),
		Argument:       text.GetForeground(),
		Dash:           styles.Style(style.Punctuation).GetForeground(),
		ErrorHeader: [2]color.Color{
			genericError.GetForeground(),
			genericError.GetBackground(),
		},
	}
}

// ColorSchemeFunc returns a [fang.ColorSchemeFunc] that creates a
// [fang.ColorScheme] from [style.Styles].
//
// This wraps [ColorScheme] for use with [fang.WithColorSchemeFunc].
// Since themes are designed for a specific light/dark mode, the
// [lipgloss.LightDarkFunc] parameter is ignored.
func ColorSchemeFunc(styles style.Styles) fang.ColorSchemeFunc {
	return func(_ lipgloss.LightDarkFunc) fang.ColorScheme {
		return ColorScheme(styles)
	}
}
