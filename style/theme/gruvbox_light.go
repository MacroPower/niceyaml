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
			style.GenericHeading,
			lipgloss.NewStyle().
				Foreground(lipgloss.Color("#fbf1c7")).
				Background(lipgloss.Color("#9d0006")).
				Bold(true),
		),
		style.Set(
			style.GenericHeadingAccent,
			base.Background(lipgloss.Darken(lipgloss.Color("#fbf1c7"), 0.30)).
				Foreground(lipgloss.Darken(lipgloss.Color("#3c3836"), 0.15)),
		),
		style.Set(
			style.GenericHeadingSubtle,
			base.Background(lipgloss.Darken(lipgloss.Color("#fbf1c7"), 0.15)),
		),
		style.Set(
			style.TextAccentDim,
			base.Foreground(lipgloss.Darken(lipgloss.Color("#9d0006"), 0.15)),
		),
		style.Set(
			style.TextAccent,
			base.Foreground(lipgloss.Color("#9d0006")),
		),
		style.Set(
			style.TextSubtleDim,
			base.Foreground(lipgloss.Lighten(lipgloss.Color("#3c3836"), 0.15)),
		),
		style.Set(
			style.TextSubtle,
			base.Foreground(lipgloss.Color("#3c3836")),
		),
		style.Set(
			style.GenericHighlightDim,
			lipgloss.NewStyle().Background(lipgloss.Darken(lipgloss.Color("#fbf1c7"), 0.15)),
		),
		style.Set(
			style.GenericHighlight,
			lipgloss.NewStyle().Background(lipgloss.Darken(lipgloss.Color("#fbf1c7"), 0.30)),
		),
		style.Set(
			style.GenericHeadingOK,
			lipgloss.NewStyle().
				Foreground(lipgloss.Color("#fbf1c7")).
				Background(lipgloss.Color("#79740e")).
				Bold(true),
		),
		style.Set(
			style.GenericHeadingWarn,
			lipgloss.NewStyle().
				Foreground(lipgloss.Color("#fbf1c7")).
				Background(lipgloss.Color("#b57614")).
				Bold(true),
		),
		style.Set(
			style.GenericHeadingError,
			lipgloss.NewStyle().
				Foreground(lipgloss.Color("#fbf1c7")).
				Background(lipgloss.Color("#9d0006")).
				Bold(true),
		),
		style.Set(
			style.TextOK,
			base.Foreground(lipgloss.Color("#79740e")),
		),
		style.Set(
			style.TextWarn,
			base.Foreground(lipgloss.Color("#b57614")),
		),
		style.Set(
			style.TextError,
			base.Foreground(lipgloss.Color("#9d0006")),
		),
	)
}
