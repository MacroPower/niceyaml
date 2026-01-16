package theme

import (
	"charm.land/lipgloss/v2"

	"github.com/macropower/niceyaml/style"
)

// ModusOperandi returns [style.Styles] using modus-operandi colors.
func ModusOperandi() style.Styles {
	base := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#000000")).
		Background(lipgloss.Color("#ffffff"))

	return style.NewStyles(base,
		style.Set(
			style.Comment,
			base.Foreground(lipgloss.Color("#505050")),
		),
		style.Set(
			style.LiteralString,
			base.Foreground(lipgloss.Color("#2544bb")),
		),
		style.Set(
			style.NameTag,
			base.Foreground(lipgloss.Color("#5317ac")),
		),
		style.Set(
			style.LiteralBoolean,
			base.Foreground(lipgloss.Color("#0000c0")),
		),
		style.Set(
			style.Name,
			base.Foreground(lipgloss.Color("#8f0075")),
		),
		style.Set(
			style.Punctuation,
			base.Foreground(lipgloss.Color("#00538b")),
		),
	)
}
