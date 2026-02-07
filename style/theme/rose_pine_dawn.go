package theme

import (
	"charm.land/lipgloss/v2"

	"go.jacobcolvin.com/niceyaml/style"
)

// RosePineDawn returns [style.Styles] using rose-pine-dawn colors.
func RosePineDawn() style.Styles {
	base := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#575279")).
		Background(lipgloss.Color("#faf4ed"))

	return style.NewStyles(base,
		style.Set(
			style.Comment,
			base.Foreground(lipgloss.Color("#9893a5")),
		),
		style.Set(
			style.GenericDeleted,
			base.Foreground(lipgloss.Color("#b4637a")),
		),
		style.Set(
			style.GenericInserted,
			base.Foreground(lipgloss.Color("#56949f")),
		),
		style.Set(
			style.LiteralNumber,
			base.Foreground(lipgloss.Color("#ea9d34")),
		),
		style.Set(
			style.LiteralString,
			base.Foreground(lipgloss.Color("#ea9d34")),
		),
		style.Set(
			style.Name,
			base.Foreground(lipgloss.Color("#d7827e")),
		),
		style.Set(
			style.NameDecorator,
			base.Foreground(lipgloss.Color("#797593")),
		),
		style.Set(
			style.NameTag,
			base.Foreground(lipgloss.Color("#d7827e")),
		),
		style.Set(
			style.Punctuation,
			base.Foreground(lipgloss.Color("#797593")),
		),
		style.Set(
			style.GenericError,
			base.Foreground(lipgloss.Color("#b4637a")),
		),
		style.Set(
			style.Generic,
			base.Italic(true),
		),
		style.Set(
			style.Title,
			lipgloss.NewStyle().
				Foreground(lipgloss.Color("#faf4ed")).
				Background(lipgloss.Color("#d7827e")).
				Bold(true),
		),
		style.Set(
			style.TitleAccent,
			base.Background(lipgloss.Darken(lipgloss.Color("#faf4ed"), 0.30)).
				Foreground(lipgloss.Darken(lipgloss.Color("#575279"), 0.15)),
		),
		style.Set(
			style.TitleSubtle,
			base.Background(lipgloss.Darken(lipgloss.Color("#faf4ed"), 0.15)),
		),
		style.Set(
			style.TextAccent,
			base.Foreground(lipgloss.Darken(lipgloss.Color("#575279"), 0.15)),
		),
		style.Set(
			style.TextAccentSelected,
			base.Foreground(lipgloss.Color("#575279")),
		),
		style.Set(
			style.TextSubtle,
			base.Foreground(lipgloss.Lighten(lipgloss.Color("#575279"), 0.15)),
		),
		style.Set(
			style.TextSubtleSelected,
			base.Foreground(lipgloss.Color("#575279")),
		),
		style.Set(
			style.Highlight,
			lipgloss.NewStyle().Background(lipgloss.Darken(lipgloss.Color("#faf4ed"), 0.15)),
		),
		style.Set(
			style.HighlightSelected,
			lipgloss.NewStyle().Background(lipgloss.Darken(lipgloss.Color("#faf4ed"), 0.30)),
		),
	)
}
