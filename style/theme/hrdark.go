package theme

import (
	"charm.land/lipgloss/v2"

	"github.com/macropower/niceyaml/style"
)

// Hrdark returns [style.Styles] using hrdark colors.
func Hrdark() style.Styles {
	base := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#ffffff")).
		Background(lipgloss.Color("#1d2432"))

	return style.NewStyles(base,
		style.Set(
			style.Comment,
			base.Foreground(lipgloss.Color("#828b96")).Italic(true),
		),
		style.Set(
			style.Name,
			base.Foreground(lipgloss.Color("#58a1dd")),
		),
		style.Set(
			style.NameTag,
			base.Foreground(lipgloss.Color("#ff636f")),
		),
		style.Set(
			style.LiteralString,
			base.Foreground(lipgloss.Color("#a6be9d")),
		),
		style.Set(
			style.Punctuation,
			base.Foreground(lipgloss.Color("#ff636f")),
		),
	)
}
