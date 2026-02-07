package theme

import (
	"charm.land/lipgloss/v2"

	"go.jacobcolvin.com/niceyaml/style"
)

// Nord returns [style.Styles] using nord colors.
func Nord() style.Styles {
	base := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#d8dee9")).
		Background(lipgloss.Color("#2e3440"))

	return style.NewStyles(base,
		style.Set(
			style.Comment,
			base.Foreground(lipgloss.Color("#616e87")).Italic(true),
		),
		style.Set(
			style.GenericDeleted,
			base.Foreground(lipgloss.Color("#bf616a")),
		),
		style.Set(
			style.GenericError,
			base.Foreground(lipgloss.Color("#bf616a")),
		),
		style.Set(
			style.GenericInserted,
			base.Foreground(lipgloss.Color("#a3be8c")),
		),
		style.Set(
			style.LiteralNumber,
			base.Foreground(lipgloss.Color("#b48ead")),
		),
		style.Set(
			style.LiteralString,
			base.Foreground(lipgloss.Color("#a3be8c")),
		),
		style.Set(
			style.Name,
			base.Foreground(lipgloss.Color("#d8dee9")),
		),
		style.Set(
			style.NameDecorator,
			base.Foreground(lipgloss.Color("#d08770")),
		),
		style.Set(
			style.NameTag,
			base.Foreground(lipgloss.Color("#81a1c1")),
		),
		style.Set(
			style.Punctuation,
			base.Foreground(lipgloss.Color("#eceff4")),
		),
		style.Set(
			style.Generic,
			base.Italic(true),
		),
		style.Set(
			style.PunctuationHeading,
			base.Foreground(lipgloss.Color("#8fbcbb")),
		),
		style.Set(
			style.Title,
			lipgloss.NewStyle().
				Foreground(lipgloss.Color("#2e3440")).
				Background(lipgloss.Color("#81a1c1")).
				Bold(true),
		),
		style.Set(
			style.TitleAccent,
			base.Background(lipgloss.Lighten(lipgloss.Color("#2e3440"), 0.30)).
				Foreground(lipgloss.Lighten(lipgloss.Color("#d8dee9"), 0.15)),
		),
		style.Set(
			style.TitleSubtle,
			base.Background(lipgloss.Lighten(lipgloss.Color("#2e3440"), 0.15)),
		),
		style.Set(
			style.TextAccent,
			base.Foreground(lipgloss.Lighten(lipgloss.Color("#d8dee9"), 0.15)),
		),
		style.Set(
			style.TextAccentSelected,
			base.Foreground(lipgloss.Color("#d8dee9")),
		),
		style.Set(
			style.TextSubtle,
			base.Foreground(lipgloss.Darken(lipgloss.Color("#d8dee9"), 0.15)),
		),
		style.Set(
			style.TextSubtleSelected,
			base.Foreground(lipgloss.Color("#d8dee9")),
		),
		style.Set(
			style.Highlight,
			lipgloss.NewStyle().Background(lipgloss.Lighten(lipgloss.Color("#2e3440"), 0.15)),
		),
		style.Set(
			style.HighlightSelected,
			lipgloss.NewStyle().Background(lipgloss.Lighten(lipgloss.Color("#2e3440"), 0.30)),
		),
	)
}
