package theme

import (
	"charm.land/lipgloss/v2"

	"github.com/macropower/niceyaml/style"
)

// Pastie returns [style.Styles] using pastie colors.
func Pastie() style.Styles {
	base := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#000000")).
		Background(lipgloss.Color("#ffffff"))

	return style.NewStyles(base,
		style.Set(
			style.Comment,
			base.Foreground(lipgloss.Color("#888888")),
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
			base.Foreground(lipgloss.Color("#0000dd")).Bold(true),
		),
		style.Set(
			style.LiteralString,
			base.Foreground(lipgloss.Color("#dd2200")).Background(lipgloss.Color("#fff0f0")),
		),
		style.Set(
			style.NameDecorator,
			base.Foreground(lipgloss.Color("#555555")),
		),
		style.Set(
			style.NameTag,
			base.Foreground(lipgloss.Color("#bb0066")).Bold(true),
		),
		style.Set(
			style.Generic,
			base.Italic(true),
		),
		style.Set(
			style.Name,
			base.Foreground(lipgloss.Color("#003388")),
		),
		style.Set(
			style.PunctuationHeading,
			base.Foreground(lipgloss.Color("#bb0066")).Bold(true),
		),
		style.Set(
			style.Punctuation,
			base.Foreground(lipgloss.Color("#008800")),
		),
	)
}
