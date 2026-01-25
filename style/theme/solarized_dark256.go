package theme

import (
	"charm.land/lipgloss/v2"

	"jacobcolvin.com/niceyaml/style"
)

// SolarizedDark256 returns [style.Styles] using solarized-dark256 colors.
func SolarizedDark256() style.Styles {
	base := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#8a8a8a")).
		Background(lipgloss.Color("#1c1c1c"))

	return style.NewStyles(base,
		style.Set(
			style.Comment,
			base.Foreground(lipgloss.Color("#4e4e4e")),
		),
		style.Set(
			style.GenericDeleted,
			base.Foreground(lipgloss.Color("#af0000")),
		),
		style.Set(
			style.GenericError,
			base.Foreground(lipgloss.Color("#af0000")).Bold(true),
		),
		style.Set(
			style.GenericInserted,
			base.Foreground(lipgloss.Color("#5f8700")),
		),
		style.Set(
			style.LiteralNumber,
			base.Foreground(lipgloss.Color("#00afaf")),
		),
		style.Set(
			style.LiteralString,
			base.Foreground(lipgloss.Color("#00afaf")),
		),
		style.Set(
			style.NameDecorator,
			base.Foreground(lipgloss.Color("#0087ff")),
		),
		style.Set(
			style.NameTag,
			base.Foreground(lipgloss.Color("#0087ff")),
		),
		style.Set(
			style.Generic,
			base.Italic(true),
		),
		style.Set(
			style.LiteralBoolean,
			base.Foreground(lipgloss.Color("#d75f00")),
		),
		style.Set(
			style.Name,
			base.Foreground(lipgloss.Color("#0087ff")),
		),
		style.Set(
			style.Punctuation,
			base.Foreground(lipgloss.Color("#8a8a8a")),
		),
	)
}
