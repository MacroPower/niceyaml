package theme

import (
	"charm.land/lipgloss/v2"

	"go.jacobcolvin.com/niceyaml/style"
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
		style.Set(
			style.Title,
			lipgloss.NewStyle().
				Foreground(lipgloss.Color("#1d2432")).
				Background(lipgloss.Color("#ff636f")).
				Bold(true),
		),
		style.Set(
			style.TitleAccent,
			base.Background(lipgloss.Lighten(lipgloss.Color("#1d2432"), 0.30)).
				Foreground(lipgloss.Lighten(lipgloss.Color("#ffffff"), 0.15)),
		),
		style.Set(
			style.TitleSubtle,
			base.Background(lipgloss.Lighten(lipgloss.Color("#1d2432"), 0.15)),
		),
		style.Set(
			style.TextAccent,
			base.Foreground(lipgloss.Lighten(lipgloss.Color("#ff636f"), 0.15)),
		),
		style.Set(
			style.TextAccentSelected,
			base.Foreground(lipgloss.Color("#ff636f")),
		),
		style.Set(
			style.TextSubtle,
			base.Foreground(lipgloss.Darken(lipgloss.Color("#ffffff"), 0.15)),
		),
		style.Set(
			style.TextSubtleSelected,
			base.Foreground(lipgloss.Color("#ffffff")),
		),
		style.Set(
			style.Highlight,
			lipgloss.NewStyle().Background(lipgloss.Lighten(lipgloss.Color("#1d2432"), 0.15)),
		),
		style.Set(
			style.HighlightSelected,
			lipgloss.NewStyle().Background(lipgloss.Lighten(lipgloss.Color("#1d2432"), 0.30)),
		),
		style.Set(
			style.TitleOK,
			lipgloss.NewStyle().
				Foreground(lipgloss.Color("#1d2432")).
				Background(lipgloss.Color("#00ff00")).
				Bold(true),
		),
		style.Set(
			style.TitleWarn,
			lipgloss.NewStyle().
				Foreground(lipgloss.Color("#1d2432")).
				Background(lipgloss.Color("#e5c07b")).
				Bold(true),
		),
		style.Set(
			style.TitleError,
			lipgloss.NewStyle().
				Foreground(lipgloss.Color("#1d2432")).
				Background(lipgloss.Color("#ff0000")).
				Bold(true),
		),
		style.Set(
			style.TextOK,
			base.Foreground(lipgloss.Color("#00ff00")),
		),
		style.Set(
			style.TextWarn,
			base.Foreground(lipgloss.Color("#e5c07b")),
		),
		style.Set(
			style.TextError,
			base.Foreground(lipgloss.Color("#ff0000")),
		),
	)
}
