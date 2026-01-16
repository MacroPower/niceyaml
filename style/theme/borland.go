package theme

import (
	"charm.land/lipgloss/v2"

	"github.com/macropower/niceyaml/style"
)

// Borland returns [style.Styles] using borland colors.
func Borland() style.Styles {
	base := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#000000")).
		Background(lipgloss.Color("#ffffff"))

	return style.NewStyles(base,
		style.Set(
			style.Comment,
			base.Foreground(lipgloss.Color("#008800")).Italic(true),
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
			base.Foreground(lipgloss.Color("#0000ff")),
		),
		style.Set(
			style.LiteralString,
			base.Foreground(lipgloss.Color("#0000ff")),
		),
		style.Set(
			style.NameTag,
			base.Foreground(lipgloss.Color("#000080")).Bold(true),
		),
		style.Set(
			style.NameDecorator,
			base.Foreground(lipgloss.Color("#008080")),
		),
		style.Set(
			style.Generic,
			base.Italic(true),
		),
		style.Set(
			style.Punctuation,
			base.Bold(true),
		),
	)
}
