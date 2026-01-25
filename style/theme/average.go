package theme

import (
	"charm.land/lipgloss/v2"

	"jacobcolvin.com/niceyaml/style"
)

// Average returns [style.Styles] using average colors.
func Average() style.Styles {
	base := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#757575")).
		Background(lipgloss.Color("#000000"))

	return style.NewStyles(base,
		style.Set(
			style.Comment,
			base.Foreground(lipgloss.Color("#757575")),
		),
		style.Set(
			style.Generic,
			base.Foreground(lipgloss.Color("#757575")),
		),
		style.Set(
			style.GenericDeleted,
			base.Foreground(lipgloss.Color("#ec0000")),
		),
		style.Set(
			style.GenericError,
			base.Foreground(lipgloss.Color("#ec0000")),
		),
		style.Set(
			style.GenericInserted,
			base.Foreground(lipgloss.Color("#757575")).Bold(true),
		),
		style.Set(
			style.LiteralNumber,
			base.Foreground(lipgloss.Color("#008900")),
		),
		style.Set(
			style.LiteralNumberBin,
			base.Foreground(lipgloss.Color("#008900")),
		),
		style.Set(
			style.LiteralNumberFloat,
			base.Foreground(lipgloss.Color("#008900")),
		),
		style.Set(
			style.LiteralNumberHex,
			base.Foreground(lipgloss.Color("#008900")),
		),
		style.Set(
			style.LiteralNumberInteger,
			base.Foreground(lipgloss.Color("#008900")),
		),
		style.Set(
			style.LiteralNumberOct,
			base.Foreground(lipgloss.Color("#008900")),
		),
		style.Set(
			style.LiteralString,
			base.Foreground(lipgloss.Color("#008900")),
		),
		style.Set(
			style.LiteralStringDouble,
			base.Foreground(lipgloss.Color("#008900")),
		),
		style.Set(
			style.LiteralStringSingle,
			base.Foreground(lipgloss.Color("#008900")),
		),
		style.Set(
			style.Name,
			base.Foreground(lipgloss.Color("#757575")),
		),
		style.Set(
			style.NameDecorator,
			base.Foreground(lipgloss.Color("#008900")),
		),
		style.Set(
			style.NameTag,
			base.Foreground(lipgloss.Color("#ec0000")),
		),
		style.Set(
			style.Punctuation,
			base.Foreground(lipgloss.Color("#757575")),
		),
		style.Set(
			style.LiteralBoolean,
			base.Foreground(lipgloss.Color("#ec0000")),
		),
		style.Set(
			style.PunctuationHeading,
			base.Foreground(lipgloss.Color("#757575")),
		),
	)
}
