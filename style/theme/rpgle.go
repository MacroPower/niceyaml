package theme

import (
	"charm.land/lipgloss/v2"

	"go.jacobcolvin.com/niceyaml/style"
)

// Rpgle returns [style.Styles] using rpgle colors.
func Rpgle() style.Styles {
	base := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#272822")).
		Background(lipgloss.Color("#fafafa"))

	return style.NewStyles(base,
		style.Set(
			style.Comment,
			base.Foreground(lipgloss.Color("#75715e")),
		),
		style.Set(
			style.LiteralNumber,
			base.Foreground(lipgloss.Color("#ae81ff")),
		),
		style.Set(
			style.LiteralString,
			base.Foreground(lipgloss.Color("#d88200")),
		),
		style.Set(
			style.Name,
			base.Foreground(lipgloss.Color("#111111")),
		),
		style.Set(
			style.NameDecorator,
			base.Foreground(lipgloss.Color("#75af00")),
		),
		style.Set(
			style.NameTag,
			base.Foreground(lipgloss.Color("#f92672")),
		),
		style.Set(
			style.Punctuation,
			base.Foreground(lipgloss.Color("#ff0000")),
		),
		style.Set(
			style.GenericError,
			base.Foreground(lipgloss.Color("#960050")).Background(lipgloss.Color("#1e0010")),
		),
		style.Set(
			style.Generic,
			base.Italic(true),
		),
		style.Set(
			style.GenericHeading,
			lipgloss.NewStyle().
				Foreground(lipgloss.Color("#fafafa")).
				Background(lipgloss.Color("#f92672")).
				Bold(true),
		),
		style.Set(
			style.GenericHeadingAccent,
			base.Background(lipgloss.Darken(lipgloss.Color("#fafafa"), 0.30)).
				Foreground(lipgloss.Darken(lipgloss.Color("#272822"), 0.15)),
		),
		style.Set(
			style.GenericHeadingSubtle,
			base.Background(lipgloss.Darken(lipgloss.Color("#fafafa"), 0.15)),
		),
		style.Set(
			style.TextAccentDim,
			base.Foreground(lipgloss.Darken(lipgloss.Color("#f92672"), 0.15)),
		),
		style.Set(
			style.TextAccent,
			base.Foreground(lipgloss.Color("#f92672")),
		),
		style.Set(
			style.TextSubtleDim,
			base.Foreground(lipgloss.Lighten(lipgloss.Color("#272822"), 0.15)),
		),
		style.Set(
			style.TextSubtle,
			base.Foreground(lipgloss.Color("#272822")),
		),
		style.Set(
			style.GenericHighlightDim,
			lipgloss.NewStyle().Background(lipgloss.Darken(lipgloss.Color("#fafafa"), 0.15)),
		),
		style.Set(
			style.GenericHighlight,
			lipgloss.NewStyle().Background(lipgloss.Darken(lipgloss.Color("#fafafa"), 0.30)),
		),
		style.Set(
			style.GenericHeadingOK,
			lipgloss.NewStyle().
				Foreground(lipgloss.Color("#fafafa")).
				Background(lipgloss.Color("#22863a")).
				Bold(true),
		),
		style.Set(
			style.GenericHeadingWarn,
			lipgloss.NewStyle().
				Foreground(lipgloss.Color("#fafafa")).
				Background(lipgloss.Color("#d08700")).
				Bold(true),
		),
		style.Set(
			style.GenericHeadingError,
			lipgloss.NewStyle().
				Foreground(lipgloss.Color("#fafafa")).
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
