package theme

import (
	"charm.land/lipgloss/v2"

	"go.jacobcolvin.com/niceyaml/style"
)

// Perldoc returns [style.Styles] using perldoc colors.
func Perldoc() style.Styles {
	base := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#000000")).
		Background(lipgloss.Color("#eeeedd"))

	return style.NewStyles(base,
		style.Set(
			style.Comment,
			base.Foreground(lipgloss.Color("#228b22")),
		),
		style.Set(
			style.GenericDeleted,
			base.Foreground(lipgloss.Color("#aa0000")),
		),
		style.Set(
			style.GenericError,
			base.Foreground(lipgloss.Color("#aa0000")),
		),
		style.Set(
			style.GenericInserted,
			base.Foreground(lipgloss.Color("#00aa00")),
		),
		style.Set(
			style.LiteralNumber,
			base.Foreground(lipgloss.Color("#b452cd")),
		),
		style.Set(
			style.LiteralString,
			base.Foreground(lipgloss.Color("#cd5555")),
		),
		style.Set(
			style.NameDecorator,
			base.Foreground(lipgloss.Color("#707a7c")),
		),
		style.Set(
			style.NameTag,
			base.Foreground(lipgloss.Color("#8b008b")).Bold(true),
		),
		style.Set(
			style.Generic,
			base.Italic(true),
		),
		style.Set(
			style.Name,
			base.Foreground(lipgloss.Color("#658b00")),
		),
		style.Set(
			style.PunctuationHeading,
			base.Foreground(lipgloss.Color("#008b45")).Underline(true),
		),
		style.Set(
			style.Punctuation,
			base.Foreground(lipgloss.Color("#8b008b")),
		),
		style.Set(
			style.GenericHeading,
			lipgloss.NewStyle().
				Foreground(lipgloss.Color("#eeeedd")).
				Background(lipgloss.Color("#8b008b")).
				Bold(true),
		),
		style.Set(
			style.GenericHeadingAccent,
			base.Background(lipgloss.Darken(lipgloss.Color("#eeeedd"), 0.30)).
				Foreground(lipgloss.Darken(lipgloss.Color("#000000"), 0.15)),
		),
		style.Set(
			style.GenericHeadingSubtle,
			base.Background(lipgloss.Darken(lipgloss.Color("#eeeedd"), 0.15)),
		),
		style.Set(
			style.TextAccentDim,
			base.Foreground(lipgloss.Darken(lipgloss.Color("#8b008b"), 0.15)),
		),
		style.Set(
			style.TextAccent,
			base.Foreground(lipgloss.Color("#8b008b")),
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
			lipgloss.NewStyle().Background(lipgloss.Darken(lipgloss.Color("#eeeedd"), 0.15)),
		),
		style.Set(
			style.GenericHighlight,
			lipgloss.NewStyle().Background(lipgloss.Darken(lipgloss.Color("#eeeedd"), 0.30)),
		),
		style.Set(
			style.GenericHeadingOK,
			lipgloss.NewStyle().
				Foreground(lipgloss.Color("#eeeedd")).
				Background(lipgloss.Color("#00aa00")).
				Bold(true),
		),
		style.Set(
			style.GenericHeadingWarn,
			lipgloss.NewStyle().
				Foreground(lipgloss.Color("#eeeedd")).
				Background(lipgloss.Color("#b07d2b")).
				Bold(true),
		),
		style.Set(
			style.GenericHeadingError,
			lipgloss.NewStyle().
				Foreground(lipgloss.Color("#eeeedd")).
				Background(lipgloss.Color("#aa0000")).
				Bold(true),
		),
		style.Set(
			style.TextOK,
			base.Foreground(lipgloss.Color("#00aa00")),
		),
		style.Set(
			style.TextWarn,
			base.Foreground(lipgloss.Color("#b07d2b")),
		),
		style.Set(
			style.TextError,
			base.Foreground(lipgloss.Color("#aa0000")),
		),
	)
}
