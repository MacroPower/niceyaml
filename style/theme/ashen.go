package theme

import (
	"charm.land/lipgloss/v2"

	"github.com/macropower/niceyaml/style"
)

// Ashen returns [style.Styles] using ashen colors.
func Ashen() style.Styles {
	base := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#b4b4b4")).
		Background(lipgloss.Color("#121212"))

	return style.NewStyles(base,
		style.Set(
			style.Comment,
			base.Foreground(lipgloss.Color("#737373")).Italic(true),
		),
		style.Set(
			style.Generic,
			base.Foreground(lipgloss.Color("#b4b4b4")),
		),
		style.Set(
			style.GenericDeleted,
			base.Foreground(lipgloss.Color("#C53030")),
		),
		style.Set(
			style.GenericError,
			base.Foreground(lipgloss.Color("#C53030")),
		),
		style.Set(
			style.GenericInserted,
			base.Foreground(lipgloss.Color("#b4b4b4")).Bold(true),
		),
		style.Set(
			style.LiteralNumber,
			base.Foreground(lipgloss.Color("#4A8B8B")),
		),
		style.Set(
			style.LiteralNumberBin,
			base.Foreground(lipgloss.Color("#4A8B8B")),
		),
		style.Set(
			style.LiteralNumberFloat,
			base.Foreground(lipgloss.Color("#4A8B8B")),
		),
		style.Set(
			style.LiteralNumberHex,
			base.Foreground(lipgloss.Color("#4A8B8B")),
		),
		style.Set(
			style.LiteralNumberInteger,
			base.Foreground(lipgloss.Color("#4A8B8B")),
		),
		style.Set(
			style.LiteralNumberOct,
			base.Foreground(lipgloss.Color("#4A8B8B")),
		),
		style.Set(
			style.LiteralString,
			base.Foreground(lipgloss.Color("#DF6464")),
		),
		style.Set(
			style.LiteralStringDouble,
			base.Foreground(lipgloss.Color("#DF6464")),
		),
		style.Set(
			style.LiteralStringSingle,
			base.Foreground(lipgloss.Color("#DF6464")),
		),
		style.Set(
			style.Name,
			base.Foreground(lipgloss.Color("#b4b4b4")),
		),
		style.Set(
			style.NameDecorator,
			base.Foreground(lipgloss.Color("#b4b4b4")).Bold(true).Italic(true),
		),
		style.Set(
			style.NameTag,
			base.Foreground(lipgloss.Color("#D87C4A")).Italic(true),
		),
		style.Set(
			style.Punctuation,
			base.Foreground(lipgloss.Color("#d5d5d5")),
		),
		style.Set(
			style.LiteralBoolean,
			base.Foreground(lipgloss.Color("#4A8B8B")),
		),
		style.Set(
			style.PunctuationHeading,
			base.Foreground(lipgloss.Color("#D87C4A")).Bold(true),
		),
	)
}
