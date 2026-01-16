package theme

import (
	"charm.land/lipgloss/v2"

	"github.com/macropower/niceyaml/style"
)

// Pygments returns [style.Styles] using pygments colors.
func Pygments() style.Styles {
	base := lipgloss.NewStyle()

	return style.NewStyles(base,
		style.Set(
			style.Comment,
			base.Foreground(lipgloss.Color("#408080")).Italic(true),
		),
		style.Set(
			style.GenericDeleted,
			base.Foreground(lipgloss.Color("#a00000")),
		),
		style.Set(
			style.GenericError,
			base.Foreground(lipgloss.Color("#ff0000")),
		),
		style.Set(
			style.GenericInserted,
			base.Foreground(lipgloss.Color("#00a000")),
		),
		style.Set(
			style.LiteralNumber,
			base.Foreground(lipgloss.Color("#666666")),
		),
		style.Set(
			style.LiteralString,
			base.Foreground(lipgloss.Color("#ba2121")),
		),
		style.Set(
			style.NameDecorator,
			base.Foreground(lipgloss.Color("#aa22ff")),
		),
		style.Set(
			style.NameTag,
			base.Foreground(lipgloss.Color("#008000")).Bold(true),
		),
		style.Set(
			style.Generic,
			base.Italic(true),
		),
		style.Set(
			style.Name,
			base.Foreground(lipgloss.Color("#008000")),
		),
		style.Set(
			style.PunctuationHeading,
			base.Foreground(lipgloss.Color("#0000ff")).Bold(true),
		),
		style.Set(
			style.Punctuation,
			base.Foreground(lipgloss.Color("#666666")),
		),
	)
}
