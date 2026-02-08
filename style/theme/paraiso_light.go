package theme

import (
	"charm.land/lipgloss/v2"

	"go.jacobcolvin.com/niceyaml/style"
)

// ParaisoLight returns [style.Styles] using paraiso-light colors.
func ParaisoLight() style.Styles {
	base := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#2f1e2e")).
		Background(lipgloss.Color("#e7e9db"))

	return style.NewStyles(base,
		style.Set(
			style.Comment,
			base.Foreground(lipgloss.Color("#8d8687")),
		),
		style.Set(
			style.GenericDeleted,
			base.Foreground(lipgloss.Color("#ef6155")),
		),
		style.Set(
			style.GenericInserted,
			base.Foreground(lipgloss.Color("#48b685")),
		),
		style.Set(
			style.LiteralNumber,
			base.Foreground(lipgloss.Color("#f99b15")),
		),
		style.Set(
			style.LiteralString,
			base.Foreground(lipgloss.Color("#48b685")),
		),
		style.Set(
			style.Name,
			base.Foreground(lipgloss.Color("#2f1e2e")),
		),
		style.Set(
			style.NameDecorator,
			base.Foreground(lipgloss.Color("#5bc4bf")),
		),
		style.Set(
			style.NameTag,
			base.Foreground(lipgloss.Color("#5bc4bf")),
		),
		style.Set(
			style.Punctuation,
			base.Foreground(lipgloss.Color("#2f1e2e")),
		),
		style.Set(
			style.GenericError,
			base.Foreground(lipgloss.Color("#ef6155")),
		),
		style.Set(
			style.Generic,
			base.Italic(true),
		),
		style.Set(
			style.PunctuationHeading,
			base.Foreground(lipgloss.Color("#fec418")),
		),
		style.Set(
			style.Title,
			lipgloss.NewStyle().
				Foreground(lipgloss.Color("#e7e9db")).
				Background(lipgloss.Color("#5bc4bf")).
				Bold(true),
		),
		style.Set(
			style.TitleAccent,
			base.Background(lipgloss.Darken(lipgloss.Color("#e7e9db"), 0.30)).
				Foreground(lipgloss.Darken(lipgloss.Color("#2f1e2e"), 0.15)),
		),
		style.Set(
			style.TitleSubtle,
			base.Background(lipgloss.Darken(lipgloss.Color("#e7e9db"), 0.15)),
		),
		style.Set(
			style.TextAccent,
			base.Foreground(lipgloss.Darken(lipgloss.Color("#5bc4bf"), 0.15)),
		),
		style.Set(
			style.TextAccentSelected,
			base.Foreground(lipgloss.Color("#5bc4bf")),
		),
		style.Set(
			style.TextSubtle,
			base.Foreground(lipgloss.Lighten(lipgloss.Color("#2f1e2e"), 0.15)),
		),
		style.Set(
			style.TextSubtleSelected,
			base.Foreground(lipgloss.Color("#2f1e2e")),
		),
		style.Set(
			style.Highlight,
			lipgloss.NewStyle().Background(lipgloss.Darken(lipgloss.Color("#e7e9db"), 0.15)),
		),
		style.Set(
			style.HighlightSelected,
			lipgloss.NewStyle().Background(lipgloss.Darken(lipgloss.Color("#e7e9db"), 0.30)),
		),
		style.Set(
			style.TitleOK,
			lipgloss.NewStyle().
				Foreground(lipgloss.Color("#e7e9db")).
				Background(lipgloss.Color("#48b685")).
				Bold(true),
		),
		style.Set(
			style.TitleWarn,
			lipgloss.NewStyle().
				Foreground(lipgloss.Color("#e7e9db")).
				Background(lipgloss.Color("#f99b15")).
				Bold(true),
		),
		style.Set(
			style.TitleError,
			lipgloss.NewStyle().
				Foreground(lipgloss.Color("#e7e9db")).
				Background(lipgloss.Color("#ef6155")).
				Bold(true),
		),
		style.Set(
			style.TextOK,
			base.Foreground(lipgloss.Color("#48b685")),
		),
		style.Set(
			style.TextWarn,
			base.Foreground(lipgloss.Color("#f99b15")),
		),
		style.Set(
			style.TextError,
			base.Foreground(lipgloss.Color("#ef6155")),
		),
	)
}
