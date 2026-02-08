package theme

import (
	"charm.land/lipgloss/v2"

	"go.jacobcolvin.com/niceyaml/style"
)

// Pygments returns [style.Styles] using pygments colors.
func Pygments() style.Styles {
	base := lipgloss.NewStyle()

	return style.NewStyles(base,
		style.Set(
			style.Comment,
			base.Foreground(lipgloss.Color("#408080")).Italic(true),
		),
		style.Set(
			style.GenericDeleted,
			base.Foreground(lipgloss.Color("#a00000")),
		),
		style.Set(
			style.GenericError,
			base.Foreground(lipgloss.Color("#ff0000")),
		),
		style.Set(
			style.GenericInserted,
			base.Foreground(lipgloss.Color("#00a000")),
		),
		style.Set(
			style.LiteralNumber,
			base.Foreground(lipgloss.Color("#666666")),
		),
		style.Set(
			style.LiteralString,
			base.Foreground(lipgloss.Color("#ba2121")),
		),
		style.Set(
			style.NameDecorator,
			base.Foreground(lipgloss.Color("#aa22ff")),
		),
		style.Set(
			style.NameTag,
			base.Foreground(lipgloss.Color("#008000")).Bold(true),
		),
		style.Set(
			style.Generic,
			base.Italic(true),
		),
		style.Set(
			style.Name,
			base.Foreground(lipgloss.Color("#008000")),
		),
		style.Set(
			style.PunctuationHeading,
			base.Foreground(lipgloss.Color("#0000ff")).Bold(true),
		),
		style.Set(
			style.Punctuation,
			base.Foreground(lipgloss.Color("#666666")),
		),
		style.Set(
			style.Title,
			lipgloss.NewStyle().
				Background(lipgloss.Color("#008000")).
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
			base.Foreground(lipgloss.Darken(lipgloss.Color("#008000"), 0.15)),
		),
		style.Set(
			style.TextAccentSelected,
			base.Foreground(lipgloss.Color("#008000")),
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
				Background(lipgloss.Color("#00a000")).
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
				Background(lipgloss.Color("#a00000")).
				Bold(true),
		),
		style.Set(
			style.TextOK,
			base.Foreground(lipgloss.Color("#00a000")),
		),
		style.Set(
			style.TextWarn,
			base.Foreground(lipgloss.Color("#b07d2b")),
		),
		style.Set(
			style.TextError,
			base.Foreground(lipgloss.Color("#a00000")),
		),
	)
}
