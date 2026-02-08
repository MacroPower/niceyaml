package theme

import (
	"charm.land/lipgloss/v2"

	"go.jacobcolvin.com/niceyaml/style"
)

// Vim returns [style.Styles] using vim colors.
func Vim() style.Styles {
	base := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#cccccc")).
		Background(lipgloss.Color("#000000"))

	return style.NewStyles(base,
		style.Set(
			style.Comment,
			base.Foreground(lipgloss.Color("#000080")),
		),
		style.Set(
			style.GenericDeleted,
			base.Foreground(lipgloss.Color("#cd0000")),
		),
		style.Set(
			style.GenericError,
			base.Foreground(lipgloss.Color("#ff0000")),
		),
		style.Set(
			style.GenericInserted,
			base.Foreground(lipgloss.Color("#00cd00")),
		),
		style.Set(
			style.LiteralNumber,
			base.Foreground(lipgloss.Color("#cd00cd")),
		),
		style.Set(
			style.LiteralString,
			base.Foreground(lipgloss.Color("#cd0000")),
		),
		style.Set(
			style.Generic,
			base.Italic(true),
		),
		style.Set(
			style.NameTag,
			base.Foreground(lipgloss.Color("#cdcd00")),
		),
		style.Set(
			style.Name,
			base.Foreground(lipgloss.Color("#cd00cd")),
		),
		style.Set(
			style.Punctuation,
			base.Foreground(lipgloss.Color("#3399cc")),
		),
		style.Set(
			style.Title,
			lipgloss.NewStyle().
				Foreground(lipgloss.Color("#000000")).
				Background(lipgloss.Color("#cdcd00")).
				Bold(true),
		),
		style.Set(
			style.TitleAccent,
			base.Background(lipgloss.Lighten(lipgloss.Color("#000000"), 0.30)).
				Foreground(lipgloss.Lighten(lipgloss.Color("#cccccc"), 0.15)),
		),
		style.Set(
			style.TitleSubtle,
			base.Background(lipgloss.Lighten(lipgloss.Color("#000000"), 0.15)),
		),
		style.Set(
			style.TextAccent,
			base.Foreground(lipgloss.Lighten(lipgloss.Color("#cdcd00"), 0.15)),
		),
		style.Set(
			style.TextAccentSelected,
			base.Foreground(lipgloss.Color("#cdcd00")),
		),
		style.Set(
			style.TextSubtle,
			base.Foreground(lipgloss.Darken(lipgloss.Color("#cccccc"), 0.15)),
		),
		style.Set(
			style.TextSubtleSelected,
			base.Foreground(lipgloss.Color("#cccccc")),
		),
		style.Set(
			style.Highlight,
			lipgloss.NewStyle().Background(lipgloss.Lighten(lipgloss.Color("#000000"), 0.15)),
		),
		style.Set(
			style.HighlightSelected,
			lipgloss.NewStyle().Background(lipgloss.Lighten(lipgloss.Color("#000000"), 0.30)),
		),
		style.Set(
			style.TitleOK,
			lipgloss.NewStyle().
				Foreground(lipgloss.Color("#000000")).
				Background(lipgloss.Color("#00cd00")).
				Bold(true),
		),
		style.Set(
			style.TitleWarn,
			lipgloss.NewStyle().
				Foreground(lipgloss.Color("#000000")).
				Background(lipgloss.Color("#cdcd00")).
				Bold(true),
		),
		style.Set(
			style.TitleError,
			lipgloss.NewStyle().
				Foreground(lipgloss.Color("#000000")).
				Background(lipgloss.Color("#cd0000")).
				Bold(true),
		),
		style.Set(
			style.TextOK,
			base.Foreground(lipgloss.Color("#00cd00")),
		),
		style.Set(
			style.TextWarn,
			base.Foreground(lipgloss.Color("#cdcd00")),
		),
		style.Set(
			style.TextError,
			base.Foreground(lipgloss.Color("#cd0000")),
		),
	)
}
