package theme

import (
	"charm.land/lipgloss/v2"

	"go.jacobcolvin.com/niceyaml/style"
)

// Nordic returns [style.Styles] using nordic colors.
func Nordic() style.Styles {
	base := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#BBC3D4")).
		Background(lipgloss.Color("#242933"))

	return style.NewStyles(base,
		style.Set(
			style.Comment,
			base.Foreground(lipgloss.Color("#4C566A")).Italic(true),
		),
		style.Set(
			style.GenericDeleted,
			base.Foreground(lipgloss.Color("#C5727A")),
		),
		style.Set(
			style.GenericError,
			base.Foreground(lipgloss.Color("#C5727A")),
		),
		style.Set(
			style.GenericInserted,
			base.Foreground(lipgloss.Color("#A3BE8C")),
		),
		style.Set(
			style.LiteralNumber,
			base.Foreground(lipgloss.Color("#B48EAD")),
		),
		style.Set(
			style.LiteralString,
			base.Foreground(lipgloss.Color("#A3BE8C")),
		),
		style.Set(
			style.Name,
			base.Foreground(lipgloss.Color("#BBC3D4")),
		),
		style.Set(
			style.NameDecorator,
			base.Foreground(lipgloss.Color("#D08770")),
		),
		style.Set(
			style.NameTag,
			base.Foreground(lipgloss.Color("#5E81AC")),
		),
		style.Set(
			style.Punctuation,
			base.Foreground(lipgloss.Color("#ECEFF4")),
		),
		style.Set(
			style.Generic,
			base.Italic(true),
		),
		style.Set(
			style.PunctuationHeading,
			base.Foreground(lipgloss.Color("#8FBCBB")),
		),
		style.Set(
			style.Title,
			lipgloss.NewStyle().
				Foreground(lipgloss.Color("#242933")).
				Background(lipgloss.Color("#5E81AC")).
				Bold(true),
		),
		style.Set(
			style.TitleAccent,
			base.Background(lipgloss.Lighten(lipgloss.Color("#242933"), 0.30)).
				Foreground(lipgloss.Lighten(lipgloss.Color("#BBC3D4"), 0.15)),
		),
		style.Set(
			style.TitleSubtle,
			base.Background(lipgloss.Lighten(lipgloss.Color("#242933"), 0.15)),
		),
		style.Set(
			style.TextAccent,
			base.Foreground(lipgloss.Lighten(lipgloss.Color("#BBC3D4"), 0.15)),
		),
		style.Set(
			style.TextAccentSelected,
			base.Foreground(lipgloss.Color("#BBC3D4")),
		),
		style.Set(
			style.TextSubtle,
			base.Foreground(lipgloss.Darken(lipgloss.Color("#BBC3D4"), 0.15)),
		),
		style.Set(
			style.TextSubtleSelected,
			base.Foreground(lipgloss.Color("#BBC3D4")),
		),
		style.Set(
			style.Highlight,
			lipgloss.NewStyle().Background(lipgloss.Lighten(lipgloss.Color("#242933"), 0.15)),
		),
		style.Set(
			style.HighlightSelected,
			lipgloss.NewStyle().Background(lipgloss.Lighten(lipgloss.Color("#242933"), 0.30)),
		),
		style.Set(
			style.TitleOK,
			lipgloss.NewStyle().
				Foreground(lipgloss.Color("#242933")).
				Background(lipgloss.Color("#A3BE8C")).
				Bold(true),
		),
		style.Set(
			style.TitleWarn,
			lipgloss.NewStyle().
				Foreground(lipgloss.Color("#242933")).
				Background(lipgloss.Color("#ebcb8b")).
				Bold(true),
		),
		style.Set(
			style.TitleError,
			lipgloss.NewStyle().
				Foreground(lipgloss.Color("#242933")).
				Background(lipgloss.Color("#C5727A")).
				Bold(true),
		),
		style.Set(
			style.TextOK,
			base.Foreground(lipgloss.Color("#A3BE8C")),
		),
		style.Set(
			style.TextWarn,
			base.Foreground(lipgloss.Color("#ebcb8b")),
		),
		style.Set(
			style.TextError,
			base.Foreground(lipgloss.Color("#C5727A")),
		),
	)
}
