package theme

import (
	"charm.land/lipgloss/v2"

	"jacobcolvin.com/niceyaml/style"
)

// Arduino returns [style.Styles] using arduino colors.
func Arduino() style.Styles {
	base := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#000000")).
		Background(lipgloss.Color("#ffffff"))

	return style.NewStyles(base,
		style.Set(
			style.Comment,
			base.Foreground(lipgloss.Color("#95a5a6")),
		),
		style.Set(
			style.LiteralNumber,
			base.Foreground(lipgloss.Color("#8a7b52")),
		),
		style.Set(
			style.LiteralString,
			base.Foreground(lipgloss.Color("#7f8c8d")),
		),
		style.Set(
			style.Name,
			base.Foreground(lipgloss.Color("#434f54")),
		),
		style.Set(
			style.NameDecorator,
			base.Foreground(lipgloss.Color("#728e00")),
		),
		style.Set(
			style.GenericError,
			base.Foreground(lipgloss.Color("#a61717")),
		),
		style.Set(
			style.NameTag,
			base.Foreground(lipgloss.Color("#728e00")),
		),
		style.Set(
			style.LiteralBoolean,
			base.Foreground(lipgloss.Color("#00979d")),
		),
		style.Set(
			style.Punctuation,
			base.Foreground(lipgloss.Color("#728e00")),
		),
		style.Set(
			style.Search,
			lipgloss.NewStyle().Background(lipgloss.Darken(lipgloss.Color("#ffffff"), 0.15)),
		),
		style.Set(
			style.SearchSelected,
			lipgloss.NewStyle().Background(lipgloss.Darken(lipgloss.Color("#ffffff"), 0.30)),
		),
	)
}
