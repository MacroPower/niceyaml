package theme

import (
	"charm.land/lipgloss/v2"

	"jacobcolvin.com/niceyaml/style"
)

// SolarizedLight returns [style.Styles] using solarized-light colors.
func SolarizedLight() style.Styles {
	base := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#586e75")).
		Background(lipgloss.Color("#eee8d5"))

	return style.NewStyles(base,
		style.Set(
			style.Comment,
			base.Foreground(lipgloss.Color("#93a1a1")).Italic(true),
		),
		style.Set(
			style.Generic,
			base.Foreground(lipgloss.Color("#d33682")),
		),
		style.Set(
			style.LiteralNumber,
			base.Bold(true),
		),
		style.Set(
			style.Name,
			base.Foreground(lipgloss.Color("#268bd2")),
		),
		style.Set(
			style.NameTag,
			base.Bold(true),
		),
		style.Set(
			style.LiteralBoolean,
			base.Bold(true),
		),
		style.Set(
			style.LiteralString,
			base.Foreground(lipgloss.Color("#2aa198")),
		),
		style.Set(
			style.Punctuation,
			base.Foreground(lipgloss.Color("#859900")),
		),
		style.Set(
			style.Search,
			lipgloss.NewStyle().Background(lipgloss.Darken(lipgloss.Color("#eee8d5"), 0.15)),
		),
		style.Set(
			style.SearchSelected,
			lipgloss.NewStyle().Background(lipgloss.Darken(lipgloss.Color("#eee8d5"), 0.30)),
		),
	)
}
