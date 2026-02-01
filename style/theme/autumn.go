package theme

import (
	"charm.land/lipgloss/v2"

	"jacobcolvin.com/niceyaml/style"
)

// Autumn returns [style.Styles] using autumn colors.
func Autumn() style.Styles {
	base := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#000000")).
		Background(lipgloss.Color("#ffffff"))

	return style.NewStyles(base,
		style.Set(
			style.Comment,
			base.Foreground(lipgloss.Color("#aaaaaa")).Italic(true),
		),
		style.Set(
			style.GenericDeleted,
			base.Foreground(lipgloss.Color("#aa0000")),
		),
		style.Set(
			style.GenericError,
			base.Foreground(lipgloss.Color("#aa0000")),
		),
		style.Set(
			style.GenericInserted,
			base.Foreground(lipgloss.Color("#00aa00")),
		),
		style.Set(
			style.LiteralNumber,
			base.Foreground(lipgloss.Color("#009999")),
		),
		style.Set(
			style.LiteralString,
			base.Foreground(lipgloss.Color("#aa5500")),
		),
		style.Set(
			style.NameDecorator,
			base.Foreground(lipgloss.Color("#888888")),
		),
		style.Set(
			style.NameTag,
			base.Foreground(lipgloss.Color("#1e90ff")).Bold(true),
		),
		style.Set(
			style.Generic,
			base.Italic(true),
		),
		style.Set(
			style.Name,
			base.Foreground(lipgloss.Color("#00aaaa")),
		),
		style.Set(
			style.PunctuationHeading,
			base.Foreground(lipgloss.Color("#00aaaa")).Underline(true),
		),
		style.Set(
			style.Punctuation,
			base.Foreground(lipgloss.Color("#0000aa")),
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
