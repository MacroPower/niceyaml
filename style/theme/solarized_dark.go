package theme

import (
	"charm.land/lipgloss/v2"

	"go.jacobcolvin.com/niceyaml/style"
)

// SolarizedDark returns [style.Styles] using solarized-dark colors.
func SolarizedDark() style.Styles {
	base := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#93a1a1")).
		Background(lipgloss.Color("#002b36"))

	return style.NewStyles(base,
		style.Set(
			style.Comment,
			base.Foreground(lipgloss.Color("#586e75")),
		),
		style.Set(
			style.GenericDeleted,
			base.Foreground(lipgloss.Color("#dc322f")),
		),
		style.Set(
			style.GenericError,
			base.Foreground(lipgloss.Color("#dc322f")).Bold(true),
		),
		style.Set(
			style.GenericInserted,
			base.Foreground(lipgloss.Color("#719e07")),
		),
		style.Set(
			style.LiteralNumber,
			base.Foreground(lipgloss.Color("#2aa198")),
		),
		style.Set(
			style.LiteralString,
			base.Foreground(lipgloss.Color("#2aa198")),
		),
		style.Set(
			style.NameDecorator,
			base.Foreground(lipgloss.Color("#268bd2")),
		),
		style.Set(
			style.NameTag,
			base.Foreground(lipgloss.Color("#268bd2")),
		),
		style.Set(
			style.Generic,
			base.Italic(true),
		),
		style.Set(
			style.LiteralBoolean,
			base.Foreground(lipgloss.Color("#cb4b16")),
		),
		style.Set(
			style.Name,
			base.Foreground(lipgloss.Color("#b58900")),
		),
		style.Set(
			style.Punctuation,
			base.Foreground(lipgloss.Color("#719e07")),
		),
		style.Set(
			style.Title,
			lipgloss.NewStyle().
				Foreground(lipgloss.Color("#002b36")).
				Background(lipgloss.Color("#268bd2")).
				Bold(true),
		),
		style.Set(
			style.TitleAccent,
			base.Background(lipgloss.Lighten(lipgloss.Color("#002b36"), 0.30)).
				Foreground(lipgloss.Lighten(lipgloss.Color("#93a1a1"), 0.15)),
		),
		style.Set(
			style.TitleSubtle,
			base.Background(lipgloss.Lighten(lipgloss.Color("#002b36"), 0.15)),
		),
		style.Set(
			style.TextAccent,
			base.Foreground(lipgloss.Lighten(lipgloss.Color("#93a1a1"), 0.15)),
		),
		style.Set(
			style.TextAccentSelected,
			base.Foreground(lipgloss.Color("#93a1a1")),
		),
		style.Set(
			style.TextSubtle,
			base.Foreground(lipgloss.Darken(lipgloss.Color("#93a1a1"), 0.15)),
		),
		style.Set(
			style.TextSubtleSelected,
			base.Foreground(lipgloss.Color("#93a1a1")),
		),
		style.Set(
			style.Highlight,
			lipgloss.NewStyle().Background(lipgloss.Lighten(lipgloss.Color("#002b36"), 0.15)),
		),
		style.Set(
			style.HighlightSelected,
			lipgloss.NewStyle().Background(lipgloss.Lighten(lipgloss.Color("#002b36"), 0.30)),
		),
		style.Set(
			style.TitleOK,
			lipgloss.NewStyle().
				Foreground(lipgloss.Color("#002b36")).
				Background(lipgloss.Color("#719e07")).
				Bold(true),
		),
		style.Set(
			style.TitleWarn,
			lipgloss.NewStyle().
				Foreground(lipgloss.Color("#002b36")).
				Background(lipgloss.Color("#b58900")).
				Bold(true),
		),
		style.Set(
			style.TitleError,
			lipgloss.NewStyle().
				Foreground(lipgloss.Color("#002b36")).
				Background(lipgloss.Color("#dc322f")).
				Bold(true),
		),
		style.Set(
			style.TextOK,
			base.Foreground(lipgloss.Color("#719e07")),
		),
		style.Set(
			style.TextWarn,
			base.Foreground(lipgloss.Color("#b58900")),
		),
		style.Set(
			style.TextError,
			base.Foreground(lipgloss.Color("#dc322f")),
		),
	)
}
