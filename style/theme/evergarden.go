package theme

import (
	"charm.land/lipgloss/v2"

	"go.jacobcolvin.com/niceyaml/style"
)

// Evergarden returns [style.Styles] using evergarden colors.
func Evergarden() style.Styles {
	base := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#D6CBB4")).
		Background(lipgloss.Color("#252B2E"))

	return style.NewStyles(base,
		style.Set(
			style.Comment,
			base.Foreground(lipgloss.Color("#859289")).Italic(true),
		),
		style.Set(
			style.Generic,
			base.Foreground(lipgloss.Color("#D6CBB4")),
		),
		style.Set(
			style.GenericDeleted,
			base.Foreground(lipgloss.Color("#252B2E")).Background(lipgloss.Color("#E67E80")),
		),
		style.Set(
			style.GenericError,
			base.Background(lipgloss.Color("#E67E80")).Bold(true),
		),
		style.Set(
			style.GenericInserted,
			base.Foreground(lipgloss.Color("#252B2E")).Background(lipgloss.Color("#B2C98F")),
		),
		style.Set(
			style.LiteralNumber,
			base.Foreground(lipgloss.Color("#D699B6")),
		),
		style.Set(
			style.LiteralNumberFloat,
			base.Foreground(lipgloss.Color("#D699B6")),
		),
		style.Set(
			style.LiteralString,
			base.Foreground(lipgloss.Color("#B2C98F")),
		),
		style.Set(
			style.Name,
			base.Foreground(lipgloss.Color("#D6CBB4")),
		),
		style.Set(
			style.NameTag,
			base.Foreground(lipgloss.Color("#7a8478")),
		),
		style.Set(
			style.NameDecorator,
			base.Foreground(lipgloss.Color("#E67E80")),
		),
		style.Set(
			style.Punctuation,
			base.Foreground(lipgloss.Color("#7a8478")),
		),
		style.Set(
			style.Title,
			lipgloss.NewStyle().
				Foreground(lipgloss.Color("#252B2E")).
				Background(lipgloss.Color("#7a8478")).
				Bold(true),
		),
		style.Set(
			style.TitleAccent,
			base.Background(lipgloss.Lighten(lipgloss.Color("#252B2E"), 0.30)).
				Foreground(lipgloss.Lighten(lipgloss.Color("#D6CBB4"), 0.15)),
		),
		style.Set(
			style.TitleSubtle,
			base.Background(lipgloss.Lighten(lipgloss.Color("#252B2E"), 0.15)),
		),
		style.Set(
			style.TextAccentDim,
			base.Foreground(lipgloss.Lighten(lipgloss.Color("#7a8478"), 0.15)),
		),
		style.Set(
			style.TextAccent,
			base.Foreground(lipgloss.Color("#7a8478")),
		),
		style.Set(
			style.TextSubtleDim,
			base.Foreground(lipgloss.Darken(lipgloss.Color("#D6CBB4"), 0.15)),
		),
		style.Set(
			style.TextSubtle,
			base.Foreground(lipgloss.Color("#D6CBB4")),
		),
		style.Set(
			style.HighlightDim,
			lipgloss.NewStyle().Background(lipgloss.Lighten(lipgloss.Color("#252B2E"), 0.15)),
		),
		style.Set(
			style.Highlight,
			lipgloss.NewStyle().Background(lipgloss.Lighten(lipgloss.Color("#252B2E"), 0.30)),
		),
		style.Set(
			style.TitleOK,
			lipgloss.NewStyle().
				Foreground(lipgloss.Color("#252B2E")).
				Background(lipgloss.Color("#B2C98F")).
				Bold(true),
		),
		style.Set(
			style.TitleWarn,
			lipgloss.NewStyle().
				Foreground(lipgloss.Color("#252B2E")).
				Background(lipgloss.Color("#e6b99d")).
				Bold(true),
		),
		style.Set(
			style.TitleError,
			lipgloss.NewStyle().
				Foreground(lipgloss.Color("#252B2E")).
				Background(lipgloss.Color("#E67E80")).
				Bold(true),
		),
		style.Set(
			style.TextOK,
			base.Foreground(lipgloss.Color("#B2C98F")),
		),
		style.Set(
			style.TextWarn,
			base.Foreground(lipgloss.Color("#e6b99d")),
		),
		style.Set(
			style.TextError,
			base.Foreground(lipgloss.Color("#E67E80")),
		),
	)
}
