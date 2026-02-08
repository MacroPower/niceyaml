package theme

import (
	"charm.land/lipgloss/v2"

	"go.jacobcolvin.com/niceyaml/style"
)

// Lovelace returns [style.Styles] using lovelace colors.
func Lovelace() style.Styles {
	base := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#000000")).
		Background(lipgloss.Color("#ffffff"))

	return style.NewStyles(base,
		style.Set(
			style.Comment,
			base.Foreground(lipgloss.Color("#888888")).Italic(true),
		),
		style.Set(
			style.GenericDeleted,
			base.Foreground(lipgloss.Color("#c02828")),
		),
		style.Set(
			style.GenericError,
			base.Foreground(lipgloss.Color("#c02828")),
		),
		style.Set(
			style.GenericInserted,
			base.Foreground(lipgloss.Color("#388038")),
		),
		style.Set(
			style.LiteralNumber,
			base.Foreground(lipgloss.Color("#444444")),
		),
		style.Set(
			style.LiteralString,
			base.Foreground(lipgloss.Color("#b83838")),
		),
		style.Set(
			style.NameDecorator,
			base.Foreground(lipgloss.Color("#287088")),
		),
		style.Set(
			style.NameTag,
			base.Foreground(lipgloss.Color("#2838b0")),
		),
		style.Set(
			style.Punctuation,
			base.Foreground(lipgloss.Color("#888888")),
		),
		style.Set(
			style.Generic,
			base.Italic(true),
		),
		style.Set(
			style.LiteralBoolean,
			base.Foreground(lipgloss.Color("#444444")).Italic(true),
		),
		style.Set(
			style.Name,
			base.Foreground(lipgloss.Color("#388038")),
		),
		style.Set(
			style.PunctuationHeading,
			base.Foreground(lipgloss.Color("#289870")),
		),
		style.Set(
			style.Title,
			lipgloss.NewStyle().
				Foreground(lipgloss.Color("#ffffff")).
				Background(lipgloss.Color("#2838b0")).
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
			base.Foreground(lipgloss.Darken(lipgloss.Color("#2838b0"), 0.15)),
		),
		style.Set(
			style.TextAccentSelected,
			base.Foreground(lipgloss.Color("#2838b0")),
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
		style.Set(
			style.TitleOK,
			lipgloss.NewStyle().
				Foreground(lipgloss.Color("#ffffff")).
				Background(lipgloss.Color("#388038")).
				Bold(true),
		),
		style.Set(
			style.TitleWarn,
			lipgloss.NewStyle().
				Foreground(lipgloss.Color("#ffffff")).
				Background(lipgloss.Color("#b07d2b")).
				Bold(true),
		),
		style.Set(
			style.TitleError,
			lipgloss.NewStyle().
				Foreground(lipgloss.Color("#ffffff")).
				Background(lipgloss.Color("#c02828")).
				Bold(true),
		),
		style.Set(
			style.TextOK,
			base.Foreground(lipgloss.Color("#388038")),
		),
		style.Set(
			style.TextWarn,
			base.Foreground(lipgloss.Color("#b07d2b")),
		),
		style.Set(
			style.TextError,
			base.Foreground(lipgloss.Color("#c02828")),
		),
	)
}
