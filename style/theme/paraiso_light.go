package theme

import (
	"charm.land/lipgloss/v2"

	"jacobcolvin.com/niceyaml/style"
)

// ParaisoLight returns [style.Styles] using paraiso-light colors.
func ParaisoLight() style.Styles {
	base := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#2f1e2e")).
		Background(lipgloss.Color("#e7e9db"))

	return style.NewStyles(base,
		style.Set(
			style.Comment,
			base.Foreground(lipgloss.Color("#8d8687")),
		),
		style.Set(
			style.GenericDeleted,
			base.Foreground(lipgloss.Color("#ef6155")),
		),
		style.Set(
			style.GenericInserted,
			base.Foreground(lipgloss.Color("#48b685")),
		),
		style.Set(
			style.LiteralNumber,
			base.Foreground(lipgloss.Color("#f99b15")),
		),
		style.Set(
			style.LiteralString,
			base.Foreground(lipgloss.Color("#48b685")),
		),
		style.Set(
			style.Name,
			base.Foreground(lipgloss.Color("#2f1e2e")),
		),
		style.Set(
			style.NameDecorator,
			base.Foreground(lipgloss.Color("#5bc4bf")),
		),
		style.Set(
			style.NameTag,
			base.Foreground(lipgloss.Color("#5bc4bf")),
		),
		style.Set(
			style.Punctuation,
			base.Foreground(lipgloss.Color("#2f1e2e")),
		),
		style.Set(
			style.GenericError,
			base.Foreground(lipgloss.Color("#ef6155")),
		),
		style.Set(
			style.Generic,
			base.Italic(true),
		),
		style.Set(
			style.PunctuationHeading,
			base.Foreground(lipgloss.Color("#fec418")),
		),
		style.Set(
			style.Search,
			lipgloss.NewStyle().Background(lipgloss.Darken(lipgloss.Color("#e7e9db"), 0.15)),
		),
		style.Set(
			style.SearchSelected,
			lipgloss.NewStyle().Background(lipgloss.Darken(lipgloss.Color("#e7e9db"), 0.30)),
		),
	)
}
