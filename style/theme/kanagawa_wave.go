package theme

import (
	"charm.land/lipgloss/v2"

	"go.jacobcolvin.com/niceyaml/style"
)

// KanagawaWave returns [style.Styles] using kanagawa-wave colors.
func KanagawaWave() style.Styles {
	base := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#dcd7ba")).
		Background(lipgloss.Color("#1f1f28"))

	return style.NewStyles(base,
		style.Set(
			style.Comment,
			base.Foreground(lipgloss.Color("#727169")).Italic(true),
		),
		style.Set(
			style.CommentPreproc,
			base.Foreground(lipgloss.Color("#e46876")).Italic(true),
		),
		style.Set(
			style.GenericDeleted,
			base.Foreground(lipgloss.Color("#c34043")).Background(lipgloss.Color("#43242b")),
		),
		style.Set(
			style.GenericError,
			base.Foreground(lipgloss.Color("#e82424")),
		),
		style.Set(
			style.GenericInserted,
			base.Foreground(lipgloss.Color("#76946a")).Background(lipgloss.Color("#2b3328")),
		),
		style.Set(
			style.LiteralNumber,
			base.Foreground(lipgloss.Color("#d27e99")),
		),
		style.Set(
			style.LiteralString,
			base.Foreground(lipgloss.Color("#98bb6c")),
		),
		style.Set(
			style.NameDecorator,
			base.Foreground(lipgloss.Color("#ffa066")).Bold(true),
		),
		style.Set(
			style.NameTag,
			base.Foreground(lipgloss.Color("#7e9cd8")),
		),
		style.Set(
			style.Punctuation,
			base.Foreground(lipgloss.Color("#dcd7ba")),
		),
		style.Set(
			style.Title,
			lipgloss.NewStyle().
				Foreground(lipgloss.Color("#1f1f28")).
				Background(lipgloss.Color("#7e9cd8")).
				Bold(true),
		),
		style.Set(
			style.TitleAccent,
			base.Background(lipgloss.Lighten(lipgloss.Color("#1f1f28"), 0.30)).
				Foreground(lipgloss.Lighten(lipgloss.Color("#dcd7ba"), 0.15)),
		),
		style.Set(
			style.TitleSubtle,
			base.Background(lipgloss.Lighten(lipgloss.Color("#1f1f28"), 0.15)),
		),
		style.Set(
			style.TextAccent,
			base.Foreground(lipgloss.Lighten(lipgloss.Color("#dcd7ba"), 0.15)),
		),
		style.Set(
			style.TextAccentSelected,
			base.Foreground(lipgloss.Color("#dcd7ba")),
		),
		style.Set(
			style.TextSubtle,
			base.Foreground(lipgloss.Darken(lipgloss.Color("#dcd7ba"), 0.15)),
		),
		style.Set(
			style.TextSubtleSelected,
			base.Foreground(lipgloss.Color("#dcd7ba")),
		),
		style.Set(
			style.Highlight,
			lipgloss.NewStyle().Background(lipgloss.Lighten(lipgloss.Color("#1f1f28"), 0.15)),
		),
		style.Set(
			style.HighlightSelected,
			lipgloss.NewStyle().Background(lipgloss.Lighten(lipgloss.Color("#1f1f28"), 0.30)),
		),
		style.Set(
			style.TitleOK,
			lipgloss.NewStyle().
				Foreground(lipgloss.Color("#1f1f28")).
				Background(lipgloss.Color("#76946a")).
				Bold(true),
		),
		style.Set(
			style.TitleWarn,
			lipgloss.NewStyle().
				Foreground(lipgloss.Color("#1f1f28")).
				Background(lipgloss.Color("#e6c384")).
				Bold(true),
		),
		style.Set(
			style.TitleError,
			lipgloss.NewStyle().
				Foreground(lipgloss.Color("#1f1f28")).
				Background(lipgloss.Color("#c34043")).
				Bold(true),
		),
		style.Set(
			style.TextOK,
			base.Foreground(lipgloss.Color("#76946a")),
		),
		style.Set(
			style.TextWarn,
			base.Foreground(lipgloss.Color("#e6c384")),
		),
		style.Set(
			style.TextError,
			base.Foreground(lipgloss.Color("#c34043")),
		),
	)
}
