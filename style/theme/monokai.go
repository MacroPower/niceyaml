package theme

import (
	"charm.land/lipgloss/v2"

	"go.jacobcolvin.com/niceyaml/style"
)

// Monokai returns [style.Styles] using monokai colors.
func Monokai() style.Styles {
	base := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#f8f8f2")).
		Background(lipgloss.Color("#272822"))

	return style.NewStyles(base,
		style.Set(
			style.Comment,
			base.Foreground(lipgloss.Color("#75715e")),
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
			base.Foreground(lipgloss.Color("#ae81ff")),
		),
		style.Set(
			style.LiteralString,
			base.Foreground(lipgloss.Color("#e6db74")),
		),
		style.Set(
			style.Name,
			base.Foreground(lipgloss.Color("#f8f8f2")),
		),
		style.Set(
			style.NameDecorator,
			base.Foreground(lipgloss.Color("#a6e22e")),
		),
		style.Set(
			style.NameTag,
			base.Foreground(lipgloss.Color("#f92672")),
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
				Foreground(lipgloss.Color("#272822")).
				Background(lipgloss.Color("#f92672")).
				Bold(true),
		),
		style.Set(
			style.TitleAccent,
			base.Background(lipgloss.Lighten(lipgloss.Color("#272822"), 0.30)).
				Foreground(lipgloss.Lighten(lipgloss.Color("#f8f8f2"), 0.15)),
		),
		style.Set(
			style.TitleSubtle,
			base.Background(lipgloss.Lighten(lipgloss.Color("#272822"), 0.15)),
		),
		style.Set(
			style.TextAccent,
			base.Foreground(lipgloss.Lighten(lipgloss.Color("#f92672"), 0.15)),
		),
		style.Set(
			style.TextAccentSelected,
			base.Foreground(lipgloss.Color("#f92672")),
		),
		style.Set(
			style.TextSubtle,
			base.Foreground(lipgloss.Darken(lipgloss.Color("#f8f8f2"), 0.15)),
		),
		style.Set(
			style.TextSubtleSelected,
			base.Foreground(lipgloss.Color("#f8f8f2")),
		),
		style.Set(
			style.Highlight,
			lipgloss.NewStyle().Background(lipgloss.Lighten(lipgloss.Color("#272822"), 0.15)),
		),
		style.Set(
			style.HighlightSelected,
			lipgloss.NewStyle().Background(lipgloss.Lighten(lipgloss.Color("#272822"), 0.30)),
		),
		style.Set(
			style.TitleOK,
			lipgloss.NewStyle().
				Foreground(lipgloss.Color("#272822")).
				Background(lipgloss.Color("#a6e22e")).
				Bold(true),
		),
		style.Set(
			style.TitleWarn,
			lipgloss.NewStyle().
				Foreground(lipgloss.Color("#272822")).
				Background(lipgloss.Color("#e6db74")).
				Bold(true),
		),
		style.Set(
			style.TitleError,
			lipgloss.NewStyle().
				Foreground(lipgloss.Color("#272822")).
				Background(lipgloss.Color("#f92672")).
				Bold(true),
		),
		style.Set(
			style.TextOK,
			base.Foreground(lipgloss.Color("#a6e22e")),
		),
		style.Set(
			style.TextWarn,
			base.Foreground(lipgloss.Color("#e6db74")),
		),
		style.Set(
			style.TextError,
			base.Foreground(lipgloss.Color("#f92672")),
		),
	)
}
