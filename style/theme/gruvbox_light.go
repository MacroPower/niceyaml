package theme

import (
	"charm.land/lipgloss/v2"

	"go.jacobcolvin.com/niceyaml/style"
)

// GruvboxLight returns [style.Styles] using gruvbox-light colors.
func GruvboxLight() style.Styles {
	base := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#3c3836")).
		Background(lipgloss.Color("#fbf1c7"))

	return style.NewStyles(base,
		style.Set(
			style.Comment,
			base.Foreground(lipgloss.Color("#928374")).Italic(true),
		),
		style.Set(
			style.Generic,
			base.Foreground(lipgloss.Color("#3c3836")),
		),
		style.Set(
			style.GenericDeleted,
			base.Foreground(lipgloss.Color("#282828")).Background(lipgloss.Color("#9d0006")),
		),
		style.Set(
			style.GenericError,
			base.Background(lipgloss.Color("#9d0006")).Bold(true),
		),
		style.Set(
			style.GenericInserted,
			base.Foreground(lipgloss.Color("#282828")).Background(lipgloss.Color("#79740e")),
		),
		style.Set(
			style.LiteralNumber,
			base.Foreground(lipgloss.Color("#8f3f71")),
		),
		style.Set(
			style.LiteralNumberFloat,
			base.Foreground(lipgloss.Color("#8f3f71")),
		),
		style.Set(
			style.LiteralString,
			base.Foreground(lipgloss.Color("#79740e")),
		),
		style.Set(
			style.Name,
			base.Foreground(lipgloss.Color("#3c3836")),
		),
		style.Set(
			style.NameTag,
			base.Foreground(lipgloss.Color("#9d0006")),
		),
		style.Set(
			style.NameDecorator,
			base.Foreground(lipgloss.Color("#427b58")),
		),
		style.Set(
			style.Punctuation,
			base.Foreground(lipgloss.Color("#af3a03")),
		),
		style.Set(
			style.Title,
			lipgloss.NewStyle().
				Foreground(lipgloss.Color("#fbf1c7")).
				Background(lipgloss.Color("#9d0006")).
				Bold(true),
		),
		style.Set(
			style.TitleAccent,
			base.Background(lipgloss.Darken(lipgloss.Color("#fbf1c7"), 0.30)).
				Foreground(lipgloss.Darken(lipgloss.Color("#3c3836"), 0.15)),
		),
		style.Set(
			style.TitleSubtle,
			base.Background(lipgloss.Darken(lipgloss.Color("#fbf1c7"), 0.15)),
		),
		style.Set(
			style.TextAccent,
			base.Foreground(lipgloss.Darken(lipgloss.Color("#3c3836"), 0.15)),
		),
		style.Set(
			style.TextAccentSelected,
			base.Foreground(lipgloss.Color("#3c3836")),
		),
		style.Set(
			style.TextSubtle,
			base.Foreground(lipgloss.Lighten(lipgloss.Color("#3c3836"), 0.15)),
		),
		style.Set(
			style.TextSubtleSelected,
			base.Foreground(lipgloss.Color("#3c3836")),
		),
		style.Set(
			style.Highlight,
			lipgloss.NewStyle().Background(lipgloss.Darken(lipgloss.Color("#fbf1c7"), 0.15)),
		),
		style.Set(
			style.HighlightSelected,
			lipgloss.NewStyle().Background(lipgloss.Darken(lipgloss.Color("#fbf1c7"), 0.30)),
		),
	)
}
