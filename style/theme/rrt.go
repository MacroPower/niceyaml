package theme

import (
	"charm.land/lipgloss/v2"

	"go.jacobcolvin.com/niceyaml/style"
)

// Rrt returns [style.Styles] using rrt colors.
func Rrt() style.Styles {
	base := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#f8f8f2")).
		Background(lipgloss.Color("#000000"))

	return style.NewStyles(base,
		style.Set(
			style.Comment,
			base.Foreground(lipgloss.Color("#00ff00")),
		),
		style.Set(
			style.GenericDeleted,
			base.Foreground(lipgloss.Color("#f00")),
		),
		style.Set(
			style.GenericInserted,
			base.Foreground(lipgloss.Color("#0f0")),
		),
		style.Set(
			style.LiteralNumber,
			base.Foreground(lipgloss.Color("#ff6600")),
		),
		style.Set(
			style.LiteralString,
			base.Foreground(lipgloss.Color("#87ceeb")),
		),
		style.Set(
			style.NameDecorator,
			base.Foreground(lipgloss.Color("#e5e5e5")),
		),
		style.Set(
			style.Generic,
			base.Italic(true),
		),
		style.Set(
			style.NameTag,
			base.Foreground(lipgloss.Color("#ff0000")),
		),
		style.Set(
			style.Name,
			base.Foreground(lipgloss.Color("#7fffd4")),
		),
		style.Set(
			style.Search,
			lipgloss.NewStyle().Background(lipgloss.Lighten(lipgloss.Color("#000000"), 0.15)),
		),
		style.Set(
			style.SearchSelected,
			lipgloss.NewStyle().Background(lipgloss.Lighten(lipgloss.Color("#000000"), 0.30)),
		),
	)
}
