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
// This allows CLI styling to be derived from the existing theme system,
// providing consistent colors between the YAML viewer and CLI help output.
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
// Since themes are designed for a specific light/dark mode, the
// [lipgloss.LightDarkFunc] parameter is ignored.
func ColorSchemeFunc(styles style.Styler) fang.ColorSchemeFunc {
	return func(_ lipgloss.LightDarkFunc) fang.ColorScheme {
		return ColorScheme(styles)
	}
}
