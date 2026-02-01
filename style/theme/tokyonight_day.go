package theme

import (
	"charm.land/lipgloss/v2"

	"go.jacobcolvin.com/niceyaml/style"
)

// TokyonightDay returns [style.Styles] using tokyonight-day colors.
func TokyonightDay() style.Styles {
	base := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#3760bf")).
		Background(lipgloss.Color("#e1e2e7"))

	return style.NewStyles(base,
		style.Set(
			style.Comment,
			base.Foreground(lipgloss.Color("#a1a6c5")).Italic(true),
		),
		style.Set(
			style.Generic,
			base.Foreground(lipgloss.Color("#3760bf")),
		),
		style.Set(
			style.GenericDeleted,
			base.Foreground(lipgloss.Color("#c64343")).Background(lipgloss.Color("#e9e9ed")),
		),
		style.Set(
			style.GenericError,
			base.Foreground(lipgloss.Color("#c64343")),
		),
		style.Set(
			style.GenericInserted,
			base.Foreground(lipgloss.Color("#587539")).Background(lipgloss.Color("#e9e9ed")),
		),
		style.Set(
			style.LiteralNumber,
			base.Foreground(lipgloss.Color("#8c6c3e")),
		),
		style.Set(
			style.LiteralNumberBin,
			base.Foreground(lipgloss.Color("#8c6c3e")),
		),
		style.Set(
			style.LiteralNumberFloat,
			base.Foreground(lipgloss.Color("#8c6c3e")),
		),
		style.Set(
			style.LiteralNumberHex,
			base.Foreground(lipgloss.Color("#8c6c3e")),
		),
		style.Set(
			style.LiteralNumberInteger,
			base.Foreground(lipgloss.Color("#8c6c3e")),
		),
		style.Set(
			style.LiteralNumberOct,
			base.Foreground(lipgloss.Color("#8c6c3e")),
		),
		style.Set(
			style.LiteralString,
			base.Foreground(lipgloss.Color("#587539")),
		),
		style.Set(
			style.LiteralStringDouble,
			base.Foreground(lipgloss.Color("#587539")),
		),
		style.Set(
			style.LiteralStringSingle,
			base.Foreground(lipgloss.Color("#587539")),
		),
		style.Set(
			style.Name,
			base.Foreground(lipgloss.Color("#3760bf")),
		),
		style.Set(
			style.NameDecorator,
			base.Foreground(lipgloss.Color("#2e7de9")).Bold(true),
		),
		style.Set(
			style.NameTag,
			base.Foreground(lipgloss.Color("#9854f1")),
		),
		style.Set(
			style.CommentPreproc,
			base.Foreground(lipgloss.Color("#a1a6c5")).Bold(true),
		),
		style.Set(
			style.LiteralBoolean,
			base.Foreground(lipgloss.Color("#8c6c3e")),
		),
		style.Set(
			style.PunctuationHeading,
			base.Foreground(lipgloss.Color("#8c6c3e")),
		),
		style.Set(
			style.Punctuation,
			base.Foreground(lipgloss.Color("#587539")).Bold(true),
		),
		style.Set(
			style.Search,
			lipgloss.NewStyle().Background(lipgloss.Darken(lipgloss.Color("#e1e2e7"), 0.15)),
		),
		style.Set(
			style.SearchSelected,
			lipgloss.NewStyle().Background(lipgloss.Darken(lipgloss.Color("#e1e2e7"), 0.30)),
		),
	)
}
