package theme

import (
	"charm.land/lipgloss/v2"

	"go.jacobcolvin.com/niceyaml/style"
)

// ParaisoDark returns [style.Styles] using paraiso-dark colors.
func ParaisoDark() style.Styles {
	base := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#e7e9db")).
		Background(lipgloss.Color("#2f1e2e"))

	return style.NewStyles(base,
		style.Set(
			style.Comment,
			base.Foreground(lipgloss.Color("#776e71")),
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
			base.Foreground(lipgloss.Color("#e7e9db")),
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
			base.Foreground(lipgloss.Color("#e7e9db")),
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
			style.GenericHeading,
			lipgloss.NewStyle().
				Foreground(lipgloss.Color("#2f1e2e")).
				Background(lipgloss.Color("#5bc4bf")).
				Bold(true),
		),
		style.Set(
			style.GenericHeadingAccent,
			base.Background(lipgloss.Lighten(lipgloss.Color("#2f1e2e"), 0.30)).
				Foreground(lipgloss.Lighten(lipgloss.Color("#e7e9db"), 0.15)),
		),
		style.Set(
			style.GenericHeadingSubtle,
			base.Background(lipgloss.Lighten(lipgloss.Color("#2f1e2e"), 0.15)),
		),
		style.Set(
			style.TextAccentDim,
			base.Foreground(lipgloss.Lighten(lipgloss.Color("#5bc4bf"), 0.15)),
		),
		style.Set(
			style.TextAccent,
			base.Foreground(lipgloss.Color("#5bc4bf")),
		),
		style.Set(
			style.TextSubtleDim,
			base.Foreground(lipgloss.Darken(lipgloss.Color("#e7e9db"), 0.15)),
		),
		style.Set(
			style.TextSubtle,
			base.Foreground(lipgloss.Color("#e7e9db")),
		),
		style.Set(
			style.GenericHighlightDim,
			lipgloss.NewStyle().Background(lipgloss.Lighten(lipgloss.Color("#2f1e2e"), 0.15)),
		),
		style.Set(
			style.GenericHighlight,
			lipgloss.NewStyle().Background(lipgloss.Lighten(lipgloss.Color("#2f1e2e"), 0.30)),
		),
		style.Set(
			style.GenericHeadingOK,
			lipgloss.NewStyle().
				Foreground(lipgloss.Color("#2f1e2e")).
				Background(lipgloss.Color("#48b685")).
				Bold(true),
		),
		style.Set(
			style.GenericHeadingWarn,
			lipgloss.NewStyle().
				Foreground(lipgloss.Color("#2f1e2e")).
				Background(lipgloss.Color("#f99b15")).
				Bold(true),
		),
		style.Set(
			style.GenericHeadingError,
			lipgloss.NewStyle().
				Foreground(lipgloss.Color("#2f1e2e")).
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
