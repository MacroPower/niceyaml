package theme

import (
	"charm.land/lipgloss/v2"

	"github.com/macropower/niceyaml/style"
)

// Vs returns [style.Styles] using vs colors.
func Vs() style.Styles {
	base := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#000000")).
		Background(lipgloss.Color("#ffffff"))

	return style.NewStyles(base,
		style.Set(
			style.Comment,
			base.Foreground(lipgloss.Color("#008000")),
		),
		style.Set(
			style.LiteralString,
			base.Foreground(lipgloss.Color("#a31515")),
		),
		style.Set(
			style.NameDecorator,
			base.Foreground(lipgloss.Color("#0000ff")),
		),
		style.Set(
			style.Generic,
			base.Italic(true),
		),
		style.Set(
			style.NameTag,
			base.Foreground(lipgloss.Color("#0000ff")),
		),
		style.Set(
			style.Name,
			base.Foreground(lipgloss.Color("#2b91af")),
		),
		style.Set(
			style.Punctuation,
			base.Foreground(lipgloss.Color("#0000ff")),
		),
	)
}
