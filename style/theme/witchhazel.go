package theme

import (
	"charm.land/lipgloss/v2"

	"go.jacobcolvin.com/niceyaml/style"
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
			style.Title,
			lipgloss.NewStyle().
				Foreground(lipgloss.Color("#433e56")).
				Background(lipgloss.Color("#ffb8d1")).
				Bold(true),
		),
		style.Set(
			style.TitleAccent,
			base.Background(lipgloss.Lighten(lipgloss.Color("#433e56"), 0.30)).
				Foreground(lipgloss.Lighten(lipgloss.Color("#f8f8f2"), 0.15)),
		),
		style.Set(
			style.TitleSubtle,
			base.Background(lipgloss.Lighten(lipgloss.Color("#433e56"), 0.15)),
		),
		style.Set(
			style.TextAccentDim,
			base.Foreground(lipgloss.Lighten(lipgloss.Color("#ffb8d1"), 0.15)),
		),
		style.Set(
			style.TextAccent,
			base.Foreground(lipgloss.Color("#ffb8d1")),
		),
		style.Set(
			style.TextSubtleDim,
			base.Foreground(lipgloss.Darken(lipgloss.Color("#f8f8f2"), 0.15)),
		),
		style.Set(
			style.TextSubtle,
			base.Foreground(lipgloss.Color("#f8f8f2")),
		),
		style.Set(
			style.HighlightDim,
			lipgloss.NewStyle().Background(lipgloss.Lighten(lipgloss.Color("#433e56"), 0.15)),
		),
		style.Set(
			style.Highlight,
			lipgloss.NewStyle().Background(lipgloss.Lighten(lipgloss.Color("#433e56"), 0.30)),
		),
		style.Set(
			style.TitleOK,
			lipgloss.NewStyle().
				Foreground(lipgloss.Color("#433e56")).
				Background(lipgloss.Color("#a6e22e")).
				Bold(true),
		),
		style.Set(
			style.TitleWarn,
			lipgloss.NewStyle().
				Foreground(lipgloss.Color("#433e56")).
				Background(lipgloss.Color("#fff352")).
				Bold(true),
		),
		style.Set(
			style.TitleError,
			lipgloss.NewStyle().
				Foreground(lipgloss.Color("#433e56")).
				Background(lipgloss.Color("#f92672")).
				Bold(true),
		),
		style.Set(
			style.TextOK,
			base.Foreground(lipgloss.Color("#a6e22e")),
		),
		style.Set(
			style.TextWarn,
			base.Foreground(lipgloss.Color("#fff352")),
		),
		style.Set(
			style.TextError,
			base.Foreground(lipgloss.Color("#f92672")),
		),
	)
}
