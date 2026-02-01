package theme

import (
	"charm.land/lipgloss/v2"

	"jacobcolvin.com/niceyaml/style"
)

// Tango returns [style.Styles] using tango colors.
func Tango() style.Styles {
	base := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#000000")).
		Background(lipgloss.Color("#f8f8f8"))

	return style.NewStyles(base,
		style.Set(
			style.Comment,
			base.Foreground(lipgloss.Color("#8f5902")).Italic(true),
		),
		style.Set(
			style.Generic,
			base.Foreground(lipgloss.Color("#000000")),
		),
		style.Set(
			style.GenericDeleted,
			base.Foreground(lipgloss.Color("#a40000")),
		),
		style.Set(
			style.GenericError,
			base.Foreground(lipgloss.Color("#ef2929")),
		),
		style.Set(
			style.GenericInserted,
			base.Foreground(lipgloss.Color("#00a000")),
		),
		style.Set(
			style.LiteralNumber,
			base.Foreground(lipgloss.Color("#0000cf")).Bold(true),
		),
		style.Set(
			style.LiteralNumberFloat,
			base.Foreground(lipgloss.Color("#0000cf")).Bold(true),
		),
		style.Set(
			style.LiteralNumberHex,
			base.Foreground(lipgloss.Color("#0000cf")).Bold(true),
		),
		style.Set(
			style.LiteralNumberInteger,
			base.Foreground(lipgloss.Color("#0000cf")).Bold(true),
		),
		style.Set(
			style.LiteralNumberOct,
			base.Foreground(lipgloss.Color("#0000cf")).Bold(true),
		),
		style.Set(
			style.LiteralString,
			base.Foreground(lipgloss.Color("#4e9a06")),
		),
		style.Set(
			style.LiteralStringDouble,
			base.Foreground(lipgloss.Color("#4e9a06")),
		),
		style.Set(
			style.LiteralStringSingle,
			base.Foreground(lipgloss.Color("#4e9a06")),
		),
		style.Set(
			style.Name,
			base.Foreground(lipgloss.Color("#000000")),
		),
		style.Set(
			style.NameDecorator,
			base.Foreground(lipgloss.Color("#5c35cc")).Bold(true),
		),
		style.Set(
			style.NameTag,
			base.Foreground(lipgloss.Color("#204a87")).Bold(true),
		),
		style.Set(
			style.Punctuation,
			base.Foreground(lipgloss.Color("#000000")).Bold(true),
		),
		style.Set(
			style.LiteralBoolean,
			base.Foreground(lipgloss.Color("#204a87")).Bold(true),
		),
		style.Set(
			style.PunctuationHeading,
			base.Foreground(lipgloss.Color("#000000")),
		),
		style.Set(
			style.Search,
			lipgloss.NewStyle().Background(lipgloss.Darken(lipgloss.Color("#f8f8f8"), 0.15)),
		),
		style.Set(
			style.SearchSelected,
			lipgloss.NewStyle().Background(lipgloss.Darken(lipgloss.Color("#f8f8f8"), 0.30)),
		),
	)
}
