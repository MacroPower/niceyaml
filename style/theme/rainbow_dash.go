package theme

import (
	"charm.land/lipgloss/v2"

	"go.jacobcolvin.com/niceyaml/style"
)

// RainbowDash returns [style.Styles] using rainbow-dash colors.
func RainbowDash() style.Styles {
	base := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#4d4d4d")).
		Background(lipgloss.Color("#ffffff"))

	return style.NewStyles(base,
		style.Set(
			style.Comment,
			base.Foreground(lipgloss.Color("#0080ff")).Italic(true),
		),
		style.Set(
			style.GenericDeleted,
			base.Background(lipgloss.Color("#ffcccc")),
		),
		style.Set(
			style.GenericError,
			base.Foreground(lipgloss.Color("#ff0000")),
		),
		style.Set(
			style.GenericInserted,
			base.Background(lipgloss.Color("#ccffcc")),
		),
		style.Set(
			style.LiteralNumber,
			base.Foreground(lipgloss.Color("#5918bb")).Bold(true),
		),
		style.Set(
			style.LiteralString,
			base.Foreground(lipgloss.Color("#00cc66")),
		),
		style.Set(
			style.NameDecorator,
			base.Foreground(lipgloss.Color("#ff8000")).Bold(true),
		),
		style.Set(
			style.NameTag,
			base.Foreground(lipgloss.Color("#2c5dcd")).Bold(true),
		),
		style.Set(
			style.Generic,
			base.Italic(true),
		),
		style.Set(
			style.Name,
			base.Foreground(lipgloss.Color("#5918bb")).Bold(true),
		),
		style.Set(
			style.Punctuation,
			base.Foreground(lipgloss.Color("#2c5dcd")),
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
