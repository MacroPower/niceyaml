package theme

import (
	"charm.land/lipgloss/v2"

	"go.jacobcolvin.com/niceyaml/style"
)

// AuraThemeDarkSoft returns [style.Styles] using aura-theme-dark-soft colors.
func AuraThemeDarkSoft() style.Styles {
	base := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#bdbdbd")).
		Background(lipgloss.Color("#15141b"))

	return style.NewStyles(base,
		style.Set(
			style.Comment,
			base.Foreground(lipgloss.Color("#6d6d6d")).Italic(true),
		),
		style.Set(
			style.Generic,
			base.Foreground(lipgloss.Color("#bdbdbd")),
		),
		style.Set(
			style.GenericDeleted,
			base.Foreground(lipgloss.Color("#c55858")).Background(lipgloss.Color("#15141b")),
		),
		style.Set(
			style.GenericError,
			base.Foreground(lipgloss.Color("#c55858")),
		),
		style.Set(
			style.GenericInserted,
			base.Foreground(lipgloss.Color("#54c59f")).Background(lipgloss.Color("#15141b")),
		),
		style.Set(
			style.LiteralNumber,
			base.Foreground(lipgloss.Color("#54c59f")),
		),
		style.Set(
			style.LiteralNumberBin,
			base.Foreground(lipgloss.Color("#54c59f")),
		),
		style.Set(
			style.LiteralNumberFloat,
			base.Foreground(lipgloss.Color("#54c59f")),
		),
		style.Set(
			style.LiteralNumberHex,
			base.Foreground(lipgloss.Color("#54c59f")),
		),
		style.Set(
			style.LiteralNumberInteger,
			base.Foreground(lipgloss.Color("#54c59f")),
		),
		style.Set(
			style.LiteralNumberOct,
			base.Foreground(lipgloss.Color("#54c59f")),
		),
		style.Set(
			style.LiteralString,
			base.Foreground(lipgloss.Color("#54c59f")),
		),
		style.Set(
			style.LiteralStringDouble,
			base.Foreground(lipgloss.Color("#54c59f")),
		),
		style.Set(
			style.LiteralStringSingle,
			base.Foreground(lipgloss.Color("#54c59f")),
		),
		style.Set(
			style.Name,
			base.Foreground(lipgloss.Color("#bdbdbd")),
		),
		style.Set(
			style.NameDecorator,
			base.Foreground(lipgloss.Color("#c17ac8")),
		),
		style.Set(
			style.NameTag,
			base.Foreground(lipgloss.Color("#8464c6")),
		),
		style.Set(
			style.Punctuation,
			base.Foreground(lipgloss.Color("#c17ac8")),
		),
		style.Set(
			style.CommentPreproc,
			base.Foreground(lipgloss.Color("#6d6d6d")).Italic(true),
		),
		style.Set(
			style.LiteralBoolean,
			base.Foreground(lipgloss.Color("#54c59f")),
		),
		style.Set(
			style.PunctuationHeading,
			base.Foreground(lipgloss.Color("#bdbdbd")),
		),
		style.Set(
			style.Title,
			lipgloss.NewStyle().
				Foreground(lipgloss.Color("#15141b")).
				Background(lipgloss.Color("#8464c6")).
				Bold(true),
		),
		style.Set(
			style.TitleAccent,
			base.Background(lipgloss.Lighten(lipgloss.Color("#15141b"), 0.30)).
				Foreground(lipgloss.Lighten(lipgloss.Color("#bdbdbd"), 0.15)),
		),
		style.Set(
			style.TitleSubtle,
			base.Background(lipgloss.Lighten(lipgloss.Color("#15141b"), 0.15)),
		),
		style.Set(
			style.TextAccentDim,
			base.Foreground(lipgloss.Lighten(lipgloss.Color("#8464c6"), 0.15)),
		),
		style.Set(
			style.TextAccent,
			base.Foreground(lipgloss.Color("#8464c6")),
		),
		style.Set(
			style.TextSubtleDim,
			base.Foreground(lipgloss.Darken(lipgloss.Color("#bdbdbd"), 0.15)),
		),
		style.Set(
			style.TextSubtle,
			base.Foreground(lipgloss.Color("#bdbdbd")),
		),
		style.Set(
			style.HighlightDim,
			lipgloss.NewStyle().Background(lipgloss.Lighten(lipgloss.Color("#15141b"), 0.15)),
		),
		style.Set(
			style.Highlight,
			lipgloss.NewStyle().Background(lipgloss.Lighten(lipgloss.Color("#15141b"), 0.30)),
		),
		style.Set(
			style.TitleOK,
			lipgloss.NewStyle().
				Foreground(lipgloss.Color("#15141b")).
				Background(lipgloss.Color("#54c59f")).
				Bold(true),
		),
		style.Set(
			style.TitleWarn,
			lipgloss.NewStyle().
				Foreground(lipgloss.Color("#15141b")).
				Background(lipgloss.Color("#ffca85")).
				Bold(true),
		),
		style.Set(
			style.TitleError,
			lipgloss.NewStyle().
				Foreground(lipgloss.Color("#15141b")).
				Background(lipgloss.Color("#c55858")).
				Bold(true),
		),
		style.Set(
			style.TextOK,
			base.Foreground(lipgloss.Color("#54c59f")),
		),
		style.Set(
			style.TextWarn,
			base.Foreground(lipgloss.Color("#ffca85")),
		),
		style.Set(
			style.TextError,
			base.Foreground(lipgloss.Color("#c55858")),
		),
	)
}
