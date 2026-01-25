package theme

import (
	"charm.land/lipgloss/v2"

	"jacobcolvin.com/niceyaml/style"
)

// Lovelace returns [style.Styles] using lovelace colors.
func Lovelace() style.Styles {
	base := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#000000")).
		Background(lipgloss.Color("#ffffff"))

	return style.NewStyles(base,
		style.Set(
			style.Comment,
			base.Foreground(lipgloss.Color("#888888")).Italic(true),
		),
		style.Set(
			style.GenericDeleted,
			base.Foreground(lipgloss.Color("#c02828")),
		),
		style.Set(
			style.GenericError,
			base.Foreground(lipgloss.Color("#c02828")),
		),
		style.Set(
			style.GenericInserted,
			base.Foreground(lipgloss.Color("#388038")),
		),
		style.Set(
			style.LiteralNumber,
			base.Foreground(lipgloss.Color("#444444")),
		),
		style.Set(
			style.LiteralString,
			base.Foreground(lipgloss.Color("#b83838")),
		),
		style.Set(
			style.NameDecorator,
			base.Foreground(lipgloss.Color("#287088")),
		),
		style.Set(
			style.NameTag,
			base.Foreground(lipgloss.Color("#2838b0")),
		),
		style.Set(
			style.Punctuation,
			base.Foreground(lipgloss.Color("#888888")),
		),
		style.Set(
			style.Generic,
			base.Italic(true),
		),
		style.Set(
			style.LiteralBoolean,
			base.Foreground(lipgloss.Color("#444444")).Italic(true),
		),
		style.Set(
			style.Name,
			base.Foreground(lipgloss.Color("#388038")),
		),
		style.Set(
			style.PunctuationHeading,
			base.Foreground(lipgloss.Color("#289870")),
		),
	)
}
