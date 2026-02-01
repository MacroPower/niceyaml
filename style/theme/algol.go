package theme

import (
	"charm.land/lipgloss/v2"

	"go.jacobcolvin.com/niceyaml/style"
)

// Algol returns [style.Styles] using algol colors.
func Algol() style.Styles {
	base := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#000000")).
		Background(lipgloss.Color("#ffffff"))

	return style.NewStyles(base,
		style.Set(
			style.Comment,
			base.Foreground(lipgloss.Color("#888888")).Italic(true),
		),
		style.Set(
			style.LiteralString,
			base.Foreground(lipgloss.Color("#666666")).Italic(true),
		),
		style.Set(
			style.NameDecorator,
			base.Foreground(lipgloss.Color("#888888")).Bold(true),
		),
		style.Set(
			style.NameTag,
			base.Bold(true).Underline(true),
		),
		style.Set(
			style.Name,
			base.Bold(true).Italic(true),
		),
		style.Set(
			style.PunctuationHeading,
			base.Foreground(lipgloss.Color("#666666")).Bold(true).Italic(true),
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
