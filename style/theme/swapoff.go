package theme

import (
	"charm.land/lipgloss/v2"

	"go.jacobcolvin.com/niceyaml/style"
)

// Swapoff returns [style.Styles] using swapoff colors.
func Swapoff() style.Styles {
	base := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#e5e5e5")).
		Background(lipgloss.Color("#000000"))

	return style.NewStyles(base,
		style.Set(
			style.Comment,
			base.Foreground(lipgloss.Color("#007f7f")),
		),
		style.Set(
			style.LiteralNumber,
			base.Foreground(lipgloss.Color("#ffff00")).Bold(true),
		),
		style.Set(
			style.LiteralString,
			base.Foreground(lipgloss.Color("#00ffff")).Bold(true),
		),
		style.Set(
			style.NameTag,
			base.Bold(true),
		),
		style.Set(
			style.NameDecorator,
			base.Foreground(lipgloss.Color("#00ff00")).Bold(true),
		),
		style.Set(
			style.GenericError,
			base.Foreground(lipgloss.Color("#ff0000")),
		),
		style.Set(
			style.Generic,
			base.Bold(true),
		),
		style.Set(
			style.Name,
			base.Foreground(lipgloss.Color("#ffffff")).Bold(true),
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
