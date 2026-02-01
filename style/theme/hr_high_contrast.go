package theme

import (
	"charm.land/lipgloss/v2"

	"go.jacobcolvin.com/niceyaml/style"
)

// HrHighContrast returns [style.Styles] using hr-high-contrast colors.
func HrHighContrast() style.Styles {
	base := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#d5d500")).
		Background(lipgloss.Color("#000000"))

	return style.NewStyles(base,
		style.Set(
			style.Comment,
			base.Foreground(lipgloss.Color("#5a8349")),
		),
		style.Set(
			style.LiteralNumber,
			base.Foreground(lipgloss.Color("#ffffff")),
		),
		style.Set(
			style.LiteralString,
			base.Foreground(lipgloss.Color("#a87662")),
		),
		style.Set(
			style.Name,
			base.Foreground(lipgloss.Color("#ffffff")),
		),
		style.Set(
			style.NameTag,
			base.Foreground(lipgloss.Color("#467faf")),
		),
		style.Set(
			style.LiteralBoolean,
			base.Foreground(lipgloss.Color("#467faf")),
		),
		style.Set(
			style.Punctuation,
			base.Foreground(lipgloss.Color("#e4e400")),
		),
		style.Set(
			style.Search,
			lipgloss.NewStyle().Background(lipgloss.Lighten(lipgloss.Color("#000000"), 0.15)),
		),
		style.Set(
			style.SearchSelected,
			lipgloss.NewStyle().Background(lipgloss.Lighten(lipgloss.Color("#000000"), 0.30)),
		),
	)
}
