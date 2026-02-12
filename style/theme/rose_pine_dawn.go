package theme

import (
	"charm.land/lipgloss/v2"

	"go.jacobcolvin.com/niceyaml/style"
)

// RosePineDawn returns [style.Styles] using rose-pine-dawn colors.
func RosePineDawn() style.Styles {
	base := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#575279")).
		Background(lipgloss.Color("#faf4ed"))

	return style.NewStyles(base,
		style.Set(
			style.Comment,
			base.Foreground(lipgloss.Color("#9893a5")),
		),
		style.Set(
			style.GenericDeleted,
			base.Foreground(lipgloss.Color("#b4637a")),
		),
		style.Set(
			style.GenericInserted,
			base.Foreground(lipgloss.Color("#56949f")),
		),
		style.Set(
			style.LiteralNumber,
			base.Foreground(lipgloss.Color("#ea9d34")),
		),
		style.Set(
			style.LiteralString,
			base.Foreground(lipgloss.Color("#ea9d34")),
		),
		style.Set(
			style.Name,
			base.Foreground(lipgloss.Color("#d7827e")),
		),
		style.Set(
			style.NameDecorator,
			base.Foreground(lipgloss.Color("#797593")),
		),
		style.Set(
			style.NameTag,
			base.Foreground(lipgloss.Color("#d7827e")),
		),
		style.Set(
			style.Punctuation,
			base.Foreground(lipgloss.Color("#797593")),
		),
		style.Set(
			style.GenericError,
			base.Foreground(lipgloss.Color("#b4637a")),
		),
		style.Set(
			style.Generic,
			base.Italic(true),
		),
		style.Set(
			style.GenericHeading,
			lipgloss.NewStyle().
				Foreground(lipgloss.Color("#faf4ed")).
				Background(lipgloss.Color("#d7827e")).
				Bold(true),
		),
		style.Set(
			style.GenericHeadingAccent,
			base.Background(lipgloss.Darken(lipgloss.Color("#faf4ed"), 0.30)).
				Foreground(lipgloss.Darken(lipgloss.Color("#575279"), 0.15)),
		),
		style.Set(
			style.GenericHeadingSubtle,
			base.Background(lipgloss.Darken(lipgloss.Color("#faf4ed"), 0.15)),
		),
		style.Set(
			style.TextAccentDim,
			base.Foreground(lipgloss.Darken(lipgloss.Color("#d7827e"), 0.15)),
		),
		style.Set(
			style.TextAccent,
			base.Foreground(lipgloss.Color("#d7827e")),
		),
		style.Set(
			style.TextSubtleDim,
			base.Foreground(lipgloss.Lighten(lipgloss.Color("#575279"), 0.15)),
		),
		style.Set(
			style.TextSubtle,
			base.Foreground(lipgloss.Color("#575279")),
		),
		style.Set(
			style.GenericHighlightDim,
			lipgloss.NewStyle().Background(lipgloss.Darken(lipgloss.Color("#faf4ed"), 0.15)),
		),
		style.Set(
			style.GenericHighlight,
			lipgloss.NewStyle().Background(lipgloss.Darken(lipgloss.Color("#faf4ed"), 0.30)),
		),
		style.Set(
			style.GenericHeadingOK,
			lipgloss.NewStyle().
				Foreground(lipgloss.Color("#faf4ed")).
				Background(lipgloss.Color("#56949f")).
				Bold(true),
		),
		style.Set(
			style.GenericHeadingWarn,
			lipgloss.NewStyle().
				Foreground(lipgloss.Color("#faf4ed")).
				Background(lipgloss.Color("#ea9d34")).
				Bold(true),
		),
		style.Set(
			style.GenericHeadingError,
			lipgloss.NewStyle().
				Foreground(lipgloss.Color("#faf4ed")).
				Background(lipgloss.Color("#b4637a")).
				Bold(true),
		),
		style.Set(
			style.TextOK,
			base.Foreground(lipgloss.Color("#56949f")),
		),
		style.Set(
			style.TextWarn,
			base.Foreground(lipgloss.Color("#ea9d34")),
		),
		style.Set(
			style.TextError,
			base.Foreground(lipgloss.Color("#b4637a")),
		),
	)
}
