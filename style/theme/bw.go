package theme

import (
	"charm.land/lipgloss/v2"

	"jacobcolvin.com/niceyaml/style"
)

// Bw returns [style.Styles] using bw colors.
func Bw() style.Styles {
	base := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#000000")).
		Background(lipgloss.Color("#ffffff"))

	return style.NewStyles(base,
		style.Set(
			style.Comment,
			base.Italic(true),
		),
		style.Set(
			style.LiteralString,
			base.Italic(true),
		),
		style.Set(
			style.NameTag,
			base.Bold(true),
		),
		style.Set(
			style.Generic,
			base.Italic(true),
		),
		style.Set(
			style.Name,
			base.Bold(true),
		),
		style.Set(
			style.PunctuationHeading,
			base.Bold(true),
		),
		style.Set(
			style.Punctuation,
			base.Bold(true),
		),
		style.Set(
			style.Search,
			lipgloss.NewStyle().Background(lipgloss.Darken(lipgloss.Color("#ffffff"), 0.15)),
		),
		style.Set(
			style.SearchSelected,
			lipgloss.NewStyle().Background(lipgloss.Darken(lipgloss.Color("#ffffff"), 0.30)),
		),
	)
}
