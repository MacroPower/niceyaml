package theme

import (
	"charm.land/lipgloss/v2"

	"go.jacobcolvin.com/niceyaml/style"
)

// SolarizedDark256 returns [style.Styles] using solarized-dark256 colors.
func SolarizedDark256() style.Styles {
	base := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#8a8a8a")).
		Background(lipgloss.Color("#1c1c1c"))

	return style.NewStyles(base,
		style.Set(
			style.Comment,
			base.Foreground(lipgloss.Color("#4e4e4e")),
		),
		style.Set(
			style.GenericDeleted,
			base.Foreground(lipgloss.Color("#af0000")),
		),
		style.Set(
			style.GenericError,
			base.Foreground(lipgloss.Color("#af0000")).Bold(true),
		),
		style.Set(
			style.GenericInserted,
			base.Foreground(lipgloss.Color("#5f8700")),
		),
		style.Set(
			style.LiteralNumber,
			base.Foreground(lipgloss.Color("#00afaf")),
		),
		style.Set(
			style.LiteralString,
			base.Foreground(lipgloss.Color("#00afaf")),
		),
		style.Set(
			style.NameDecorator,
			base.Foreground(lipgloss.Color("#0087ff")),
		),
		style.Set(
			style.NameTag,
			base.Foreground(lipgloss.Color("#0087ff")),
		),
		style.Set(
			style.Generic,
			base.Italic(true),
		),
		style.Set(
			style.LiteralBoolean,
			base.Foreground(lipgloss.Color("#d75f00")),
		),
		style.Set(
			style.Name,
			base.Foreground(lipgloss.Color("#0087ff")),
		),
		style.Set(
			style.Punctuation,
			base.Foreground(lipgloss.Color("#8a8a8a")),
		),
		style.Set(
			style.Title,
			lipgloss.NewStyle().
				Foreground(lipgloss.Color("#1c1c1c")).
				Background(lipgloss.Color("#0087ff")).
				Bold(true),
		),
		style.Set(
			style.TitleAccent,
			base.Background(lipgloss.Lighten(lipgloss.Color("#1c1c1c"), 0.30)).
				Foreground(lipgloss.Lighten(lipgloss.Color("#8a8a8a"), 0.15)),
		),
		style.Set(
			style.TitleSubtle,
			base.Background(lipgloss.Lighten(lipgloss.Color("#1c1c1c"), 0.15)),
		),
		style.Set(
			style.TextAccent,
			base.Foreground(lipgloss.Lighten(lipgloss.Color("#8a8a8a"), 0.15)),
		),
		style.Set(
			style.TextAccentSelected,
			base.Foreground(lipgloss.Color("#8a8a8a")),
		),
		style.Set(
			style.TextSubtle,
			base.Foreground(lipgloss.Darken(lipgloss.Color("#8a8a8a"), 0.15)),
		),
		style.Set(
			style.TextSubtleSelected,
			base.Foreground(lipgloss.Color("#8a8a8a")),
		),
		style.Set(
			style.Highlight,
			lipgloss.NewStyle().Background(lipgloss.Lighten(lipgloss.Color("#1c1c1c"), 0.15)),
		),
		style.Set(
			style.HighlightSelected,
			lipgloss.NewStyle().Background(lipgloss.Lighten(lipgloss.Color("#1c1c1c"), 0.30)),
		),
		style.Set(
			style.TitleOK,
			lipgloss.NewStyle().
				Foreground(lipgloss.Color("#1c1c1c")).
				Background(lipgloss.Color("#5f8700")).
				Bold(true),
		),
		style.Set(
			style.TitleWarn,
			lipgloss.NewStyle().
				Foreground(lipgloss.Color("#1c1c1c")).
				Background(lipgloss.Color("#af8700")).
				Bold(true),
		),
		style.Set(
			style.TitleError,
			lipgloss.NewStyle().
				Foreground(lipgloss.Color("#1c1c1c")).
				Background(lipgloss.Color("#af0000")).
				Bold(true),
		),
		style.Set(
			style.TextOK,
			base.Foreground(lipgloss.Color("#5f8700")),
		),
		style.Set(
			style.TextWarn,
			base.Foreground(lipgloss.Color("#af8700")),
		),
		style.Set(
			style.TextError,
			base.Foreground(lipgloss.Color("#af0000")),
		),
	)
}
