package theme

import (
	"charm.land/lipgloss/v2"

	"jacobcolvin.com/niceyaml/style"
)

// Witchhazel returns [style.Styles] using witchhazel colors.
func Witchhazel() style.Styles {
	base := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#f8f8f2")).
		Background(lipgloss.Color("#433e56"))

	return style.NewStyles(base,
		style.Set(
			style.Comment,
			base.Foreground(lipgloss.Color("#b0bec5")),
		),
		style.Set(
			style.GenericDeleted,
			base.Foreground(lipgloss.Color("#f92672")),
		),
		style.Set(
			style.GenericInserted,
			base.Foreground(lipgloss.Color("#a6e22e")),
		),
		style.Set(
			style.LiteralNumber,
			base.Foreground(lipgloss.Color("#c5a3ff")),
		),
		style.Set(
			style.LiteralString,
			base.Foreground(lipgloss.Color("#1bc5e0")),
		),
		style.Set(
			style.Name,
			base.Foreground(lipgloss.Color("#f8f8f2")),
		),
		style.Set(
			style.NameDecorator,
			base.Foreground(lipgloss.Color("#ceb1ff")),
		),
		style.Set(
			style.NameTag,
			base.Foreground(lipgloss.Color("#ffb8d1")),
		),
		style.Set(
			style.Punctuation,
			base.Foreground(lipgloss.Color("#f8f8f2")),
		),
		style.Set(
			style.GenericError,
			base.Foreground(lipgloss.Color("#960050")).Background(lipgloss.Color("#1e0010")),
		),
		style.Set(
			style.Generic,
			base.Italic(true),
		),
		style.Set(
			style.Search,
			lipgloss.NewStyle().Background(lipgloss.Lighten(lipgloss.Color("#433e56"), 0.15)),
		),
		style.Set(
			style.SearchSelected,
			lipgloss.NewStyle().Background(lipgloss.Lighten(lipgloss.Color("#433e56"), 0.30)),
		),
	)
}
