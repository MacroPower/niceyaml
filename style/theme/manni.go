package theme

import (
	"charm.land/lipgloss/v2"

	"go.jacobcolvin.com/niceyaml/style"
)

// Manni returns [style.Styles] using manni colors.
func Manni() style.Styles {
	base := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#000000")).
		Background(lipgloss.Color("#f0f3f3"))

	return style.NewStyles(base,
		style.Set(
			style.Comment,
			base.Foreground(lipgloss.Color("#0099ff")).Italic(true),
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
			base.Foreground(lipgloss.Color("#ff6600")),
		),
		style.Set(
			style.LiteralString,
			base.Foreground(lipgloss.Color("#cc3300")),
		),
		style.Set(
			style.NameDecorator,
			base.Foreground(lipgloss.Color("#9999ff")),
		),
		style.Set(
			style.NameTag,
			base.Foreground(lipgloss.Color("#330099")).Bold(true),
		),
		style.Set(
			style.Generic,
			base.Italic(true),
		),
		style.Set(
			style.Name,
			base.Foreground(lipgloss.Color("#336666")),
		),
		style.Set(
			style.PunctuationHeading,
			base.Foreground(lipgloss.Color("#00ccff")).Bold(true),
		),
		style.Set(
			style.Punctuation,
			base.Foreground(lipgloss.Color("#555555")),
		),
		style.Set(
			style.Search,
			lipgloss.NewStyle().Background(lipgloss.Darken(lipgloss.Color("#f0f3f3"), 0.15)),
		),
		style.Set(
			style.SearchSelected,
			lipgloss.NewStyle().Background(lipgloss.Darken(lipgloss.Color("#f0f3f3"), 0.30)),
		),
	)
}
