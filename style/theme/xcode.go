package theme

import (
	"charm.land/lipgloss/v2"

	"go.jacobcolvin.com/niceyaml/style"
)

// Xcode returns [style.Styles] using xcode colors.
func Xcode() style.Styles {
	base := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#000000")).
		Background(lipgloss.Color("#ffffff"))

	return style.NewStyles(base,
		style.Set(
			style.Comment,
			base.Foreground(lipgloss.Color("#177500")),
		),
		style.Set(
			style.LiteralNumber,
			base.Foreground(lipgloss.Color("#1c01ce")),
		),
		style.Set(
			style.LiteralString,
			base.Foreground(lipgloss.Color("#c41a16")),
		),
		style.Set(
			style.Name,
			base.Foreground(lipgloss.Color("#000000")),
		),
		style.Set(
			style.NameDecorator,
			base.Foreground(lipgloss.Color("#000000")),
		),
		style.Set(
			style.NameTag,
			base.Foreground(lipgloss.Color("#000000")),
		),
		style.Set(
			style.GenericError,
			base.Foreground(lipgloss.Color("#000000")),
		),
		style.Set(
			style.Punctuation,
			base.Foreground(lipgloss.Color("#000000")),
		),
		style.Set(
			style.GenericHeading,
			lipgloss.NewStyle().
				Foreground(lipgloss.Color("#ffffff")).
				Background(lipgloss.Color("#000000")).
				Bold(true),
		),
		style.Set(
			style.GenericHeadingAccent,
			base.Background(lipgloss.Darken(lipgloss.Color("#ffffff"), 0.30)).
				Foreground(lipgloss.Darken(lipgloss.Color("#000000"), 0.15)),
		),
		style.Set(
			style.GenericHeadingSubtle,
			base.Background(lipgloss.Darken(lipgloss.Color("#ffffff"), 0.15)),
		),
		style.Set(
			style.TextAccentDim,
			base.Foreground(lipgloss.Darken(lipgloss.Color("#000000"), 0.15)),
		),
		style.Set(
			style.TextAccent,
			base.Foreground(lipgloss.Color("#000000")),
		),
		style.Set(
			style.TextSubtleDim,
			base.Foreground(lipgloss.Lighten(lipgloss.Color("#000000"), 0.15)),
		),
		style.Set(
			style.TextSubtle,
			base.Foreground(lipgloss.Color("#000000")),
		),
		style.Set(
			style.GenericHighlightDim,
			lipgloss.NewStyle().Background(lipgloss.Darken(lipgloss.Color("#ffffff"), 0.15)),
		),
		style.Set(
			style.GenericHighlight,
			lipgloss.NewStyle().Background(lipgloss.Darken(lipgloss.Color("#ffffff"), 0.30)),
		),
		style.Set(
			style.GenericHeadingOK,
			lipgloss.NewStyle().
				Foreground(lipgloss.Color("#ffffff")).
				Background(lipgloss.Color("#22863a")).
				Bold(true),
		),
		style.Set(
			style.GenericHeadingWarn,
			lipgloss.NewStyle().
				Foreground(lipgloss.Color("#ffffff")).
				Background(lipgloss.Color("#d08700")).
				Bold(true),
		),
		style.Set(
			style.GenericHeadingError,
			lipgloss.NewStyle().
				Foreground(lipgloss.Color("#ffffff")).
				Background(lipgloss.Color("#cb2431")).
				Bold(true),
		),
		style.Set(
			style.TextOK,
			base.Foreground(lipgloss.Color("#22863a")),
		),
		style.Set(
			style.TextWarn,
			base.Foreground(lipgloss.Color("#d08700")),
		),
		style.Set(
			style.TextError,
			base.Foreground(lipgloss.Color("#cb2431")),
		),
	)
}
