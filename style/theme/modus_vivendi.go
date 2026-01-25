package theme

import (
	"charm.land/lipgloss/v2"

	"jacobcolvin.com/niceyaml/style"
)

// ModusVivendi returns [style.Styles] using modus-vivendi colors.
func ModusVivendi() style.Styles {
	base := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#ffffff")).
		Background(lipgloss.Color("#000000"))

	return style.NewStyles(base,
		style.Set(
			style.Comment,
			base.Foreground(lipgloss.Color("#a8a8a8")),
		),
		style.Set(
			style.LiteralString,
			base.Foreground(lipgloss.Color("#79a8ff")),
		),
		style.Set(
			style.NameTag,
			base.Foreground(lipgloss.Color("#b6a0ff")),
		),
		style.Set(
			style.LiteralBoolean,
			base.Foreground(lipgloss.Color("#00bcff")),
		),
		style.Set(
			style.Name,
			base.Foreground(lipgloss.Color("#f78fe7")),
		),
		style.Set(
			style.Punctuation,
			base.Foreground(lipgloss.Color("#00d3d0")),
		),
	)
}
