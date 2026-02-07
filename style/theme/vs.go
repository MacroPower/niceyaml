package theme

import (
	"charm.land/lipgloss/v2"

	"go.jacobcolvin.com/niceyaml/style"
)

// Vs returns [style.Styles] using vs colors.
func Vs() style.Styles {
	base := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#000000")).
		Background(lipgloss.Color("#ffffff"))

	return style.NewStyles(base,
		style.Set(
			style.Comment,
			base.Foreground(lipgloss.Color("#008000")),
		),
		style.Set(
			style.LiteralString,
			base.Foreground(lipgloss.Color("#a31515")),
		),
		style.Set(
			style.NameDecorator,
			base.Foreground(lipgloss.Color("#0000ff")),
		),
		style.Set(
			style.Generic,
			base.Italic(true),
		),
		style.Set(
			style.NameTag,
			base.Foreground(lipgloss.Color("#0000ff")),
		),
		style.Set(
			style.Name,
			base.Foreground(lipgloss.Color("#2b91af")),
		),
		style.Set(
			style.Punctuation,
			base.Foreground(lipgloss.Color("#0000ff")),
		),
		style.Set(
			style.Title,
			lipgloss.NewStyle().
				Foreground(lipgloss.Color("#ffffff")).
				Background(lipgloss.Color("#0000ff")).
				Bold(true),
		),
		style.Set(
			style.TitleAccent,
			base.Background(lipgloss.Darken(lipgloss.Color("#ffffff"), 0.30)).
				Foreground(lipgloss.Darken(lipgloss.Color("#000000"), 0.15)),
		),
		style.Set(
			style.TitleSubtle,
			base.Background(lipgloss.Darken(lipgloss.Color("#ffffff"), 0.15)),
		),
		style.Set(
			style.TextAccent,
			base.Foreground(lipgloss.Darken(lipgloss.Color("#000000"), 0.15)),
		),
		style.Set(
			style.TextAccentSelected,
			base.Foreground(lipgloss.Color("#000000")),
		),
		style.Set(
			style.TextSubtle,
			base.Foreground(lipgloss.Lighten(lipgloss.Color("#000000"), 0.15)),
		),
		style.Set(
			style.TextSubtleSelected,
			base.Foreground(lipgloss.Color("#000000")),
		),
		style.Set(
			style.Highlight,
			lipgloss.NewStyle().Background(lipgloss.Darken(lipgloss.Color("#ffffff"), 0.15)),
		),
		style.Set(
			style.HighlightSelected,
			lipgloss.NewStyle().Background(lipgloss.Darken(lipgloss.Color("#ffffff"), 0.30)),
		),
	)
}
