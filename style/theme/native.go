package theme

import (
	"charm.land/lipgloss/v2"

	"go.jacobcolvin.com/niceyaml/style"
)

// Native returns [style.Styles] using native colors.
func Native() style.Styles {
	base := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#d0d0d0")).
		Background(lipgloss.Color("#202020"))

	return style.NewStyles(base,
		style.Set(
			style.Comment,
			base.Foreground(lipgloss.Color("#999999")).Italic(true),
		),
		style.Set(
			style.GenericDeleted,
			base.Foreground(lipgloss.Color("#d22323")),
		),
		style.Set(
			style.GenericError,
			base.Foreground(lipgloss.Color("#d22323")),
		),
		style.Set(
			style.GenericInserted,
			base.Foreground(lipgloss.Color("#589819")),
		),
		style.Set(
			style.LiteralNumber,
			base.Foreground(lipgloss.Color("#3677a9")),
		),
		style.Set(
			style.LiteralString,
			base.Foreground(lipgloss.Color("#ed9d13")),
		),
		style.Set(
			style.NameDecorator,
			base.Foreground(lipgloss.Color("#ffa500")),
		),
		style.Set(
			style.NameTag,
			base.Foreground(lipgloss.Color("#6ab825")).Bold(true),
		),
		style.Set(
			style.Generic,
			base.Italic(true),
		),
		style.Set(
			style.Name,
			base.Foreground(lipgloss.Color("#24909d")),
		),
		style.Set(
			style.PunctuationHeading,
			base.Foreground(lipgloss.Color("#447fcf")),
		),
		style.Set(
			style.Punctuation,
			base.Foreground(lipgloss.Color("#6ab825")).Bold(true),
		),
		style.Set(
			style.Search,
			lipgloss.NewStyle().Background(lipgloss.Lighten(lipgloss.Color("#202020"), 0.15)),
		),
		style.Set(
			style.SearchSelected,
			lipgloss.NewStyle().Background(lipgloss.Lighten(lipgloss.Color("#202020"), 0.30)),
		),
	)
}
