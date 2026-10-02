package fangs

import (
	"image/color"
	"reflect"

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
// viewer and CLI help output use the same colors. A nil styles, or one
// holding a nil pointer such as a nil [*style.Styles], selects
// [style.Default].
func ColorScheme(styles style.Styler) fang.ColorScheme {
	if isNil(styles) {
		styles = style.Default()
	}

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
// ignores its [lipgloss.LightDarkFunc] parameter. A nil styles, or one
// holding a nil pointer, selects [style.Default].
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
// [ColorSchemeFunc] to apply one theme on both. A nil light or dark, or
// one holding a nil pointer, selects [style.Default] for that background.
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

// isNil reports whether styles is nil or holds a nil pointer or nil func.
// A nil [*style.Styles] satisfies [style.Styler] through its value method,
// and a call to that method panics.
//
// The packages of niceyaml make the same check on their own options. This
// package builds on the public API of niceyaml alone, so it keeps its own
// copy.
func isNil(styles style.Styler) bool {
	if styles == nil {
		return true
	}

	v := reflect.ValueOf(styles)

	switch v.Kind() {
	case reflect.Pointer, reflect.Func:
		return v.IsNil()
	default:
		return false
	}
}
