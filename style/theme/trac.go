package theme

import (
	"charm.land/lipgloss/v2"

	"go.jacobcolvin.com/niceyaml/style"
)

// Trac returns [style.Styles] using trac colors.
func Trac() style.Styles {
	base := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#000000")).
		Background(lipgloss.Color("#ffffff"))

	return style.NewStyles(base,
		style.Set(
			style.Comment,
			base.Foreground(lipgloss.Color("#999988")).Italic(true),
		),
		style.Set(
			style.GenericDeleted,
			base.Foreground(lipgloss.Color("#000000")).Background(lipgloss.Color("#ffdddd")),
		),
		style.Set(
			style.GenericError,
			base.Foreground(lipgloss.Color("#aa0000")),
		),
		style.Set(
			style.GenericInserted,
			base.Foreground(lipgloss.Color("#000000")).Background(lipgloss.Color("#ddffdd")),
		),
		style.Set(
			style.LiteralNumber,
			base.Foreground(lipgloss.Color("#009999")),
		),
		style.Set(
			style.LiteralString,
			base.Foreground(lipgloss.Color("#bb8844")),
		),
		style.Set(
			style.NameTag,
			base.Foreground(lipgloss.Color("#000080")),
		),
		style.Set(
			style.NameDecorator,
			base.Foreground(lipgloss.Color("#999999")).Bold(true),
		),
		style.Set(
			style.Generic,
			base.Italic(true),
		),
		style.Set(
			style.Name,
			base.Foreground(lipgloss.Color("#999999")),
		),
		style.Set(
			style.PunctuationHeading,
			base.Foreground(lipgloss.Color("#555555")),
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
