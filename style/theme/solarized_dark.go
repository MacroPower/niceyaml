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
			style.Search,
			lipgloss.NewStyle().Background(lipgloss.Lighten(lipgloss.Color("#002b36"), 0.15)),
		),
		style.Set(
			style.SearchSelected,
			lipgloss.NewStyle().Background(lipgloss.Lighten(lipgloss.Color("#002b36"), 0.30)),
		),
	)
}
