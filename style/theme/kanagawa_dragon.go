package theme

import (
	"charm.land/lipgloss/v2"

	"go.jacobcolvin.com/niceyaml/style"
)

// KanagawaDragon returns [style.Styles] using kanagawa-dragon colors.
func KanagawaDragon() style.Styles {
	base := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#c5c9c5")).
		Background(lipgloss.Color("#181616"))

	return style.NewStyles(base,
		style.Set(
			style.Comment,
			base.Foreground(lipgloss.Color("#737c73")).Italic(true),
		),
		style.Set(
			style.CommentPreproc,
			base.Foreground(lipgloss.Color("#c4746e")).Italic(true),
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
			base.Foreground(lipgloss.Color("#a292a3")),
		),
		style.Set(
			style.LiteralString,
			base.Foreground(lipgloss.Color("#8a9a7b")),
		),
		style.Set(
			style.NameDecorator,
			base.Foreground(lipgloss.Color("#b6927b")).Bold(true),
		),
		style.Set(
			style.NameTag,
			base.Foreground(lipgloss.Color("#8ba4b0")),
		),
		style.Set(
			style.Punctuation,
			base.Foreground(lipgloss.Color("#c5c9c5")),
		),
		style.Set(
			style.Title,
			lipgloss.NewStyle().
				Foreground(lipgloss.Color("#181616")).
				Background(lipgloss.Color("#8ba4b0")).
				Bold(true),
		),
		style.Set(
			style.TitleAccent,
			base.Background(lipgloss.Lighten(lipgloss.Color("#181616"), 0.30)).
				Foreground(lipgloss.Lighten(lipgloss.Color("#c5c9c5"), 0.15)),
		),
		style.Set(
			style.TitleSubtle,
			base.Background(lipgloss.Lighten(lipgloss.Color("#181616"), 0.15)),
		),
		style.Set(
			style.TextAccentDim,
			base.Foreground(lipgloss.Lighten(lipgloss.Color("#8ba4b0"), 0.15)),
		),
		style.Set(
			style.TextAccent,
			base.Foreground(lipgloss.Color("#8ba4b0")),
		),
		style.Set(
			style.TextSubtleDim,
			base.Foreground(lipgloss.Darken(lipgloss.Color("#c5c9c5"), 0.15)),
		),
		style.Set(
			style.TextSubtle,
			base.Foreground(lipgloss.Color("#c5c9c5")),
		),
		style.Set(
			style.HighlightDim,
			lipgloss.NewStyle().Background(lipgloss.Lighten(lipgloss.Color("#181616"), 0.15)),
		),
		style.Set(
			style.Highlight,
			lipgloss.NewStyle().Background(lipgloss.Lighten(lipgloss.Color("#181616"), 0.30)),
		),
		style.Set(
			style.TitleOK,
			lipgloss.NewStyle().
				Foreground(lipgloss.Color("#181616")).
				Background(lipgloss.Color("#76946a")).
				Bold(true),
		),
		style.Set(
			style.TitleWarn,
			lipgloss.NewStyle().
				Foreground(lipgloss.Color("#181616")).
				Background(lipgloss.Color("#e6c384")).
				Bold(true),
		),
		style.Set(
			style.TitleError,
			lipgloss.NewStyle().
				Foreground(lipgloss.Color("#181616")).
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
