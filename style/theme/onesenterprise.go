package theme

import (
	"charm.land/lipgloss/v2"

	"go.jacobcolvin.com/niceyaml/style"
)

// Onesenterprise returns [style.Styles] using onesenterprise colors.
func Onesenterprise() style.Styles {
	base := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#000000"))

	return style.NewStyles(base,
		style.Set(
			style.Comment,
			base.Foreground(lipgloss.Color("#008000")),
		),
		style.Set(
			style.LiteralString,
			base.Foreground(lipgloss.Color("#000000")),
		),
		style.Set(
			style.Name,
			base.Foreground(lipgloss.Color("#0000ff")),
		),
		style.Set(
			style.Punctuation,
			base.Foreground(lipgloss.Color("#ff0000")),
		),
		style.Set(
			style.NameDecorator,
			base.Foreground(lipgloss.Color("#963200")),
		),
		style.Set(
			style.NameTag,
			base.Foreground(lipgloss.Color("#ff0000")),
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
