package theme

import (
	"charm.land/lipgloss/v2"

	"go.jacobcolvin.com/niceyaml/style"
)

// Autumn returns [style.Styles] using autumn colors.
func Autumn() style.Styles {
	base := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#000000")).
		Background(lipgloss.Color("#ffffff"))

	return style.NewStyles(base,
		style.Set(
			style.Comment,
			base.Foreground(lipgloss.Color("#aaaaaa")).Italic(true),
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
			base.Foreground(lipgloss.Color("#009999")),
		),
		style.Set(
			style.LiteralString,
			base.Foreground(lipgloss.Color("#aa5500")),
		),
		style.Set(
			style.NameDecorator,
			base.Foreground(lipgloss.Color("#888888")),
		),
		style.Set(
			style.NameTag,
			base.Foreground(lipgloss.Color("#1e90ff")).Bold(true),
		),
		style.Set(
			style.Generic,
			base.Italic(true),
		),
		style.Set(
			style.Name,
			base.Foreground(lipgloss.Color("#00aaaa")),
		),
		style.Set(
			style.PunctuationHeading,
			base.Foreground(lipgloss.Color("#00aaaa")).Underline(true),
		),
		style.Set(
			style.Punctuation,
			base.Foreground(lipgloss.Color("#0000aa")),
		),
		style.Set(
			style.GenericHeading,
			lipgloss.NewStyle().
				Foreground(lipgloss.Color("#ffffff")).
				Background(lipgloss.Color("#1e90ff")).
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
			base.Foreground(lipgloss.Darken(lipgloss.Color("#1e90ff"), 0.15)),
		),
		style.Set(
			style.TextAccent,
			base.Foreground(lipgloss.Color("#1e90ff")),
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
				Background(lipgloss.Color("#00aa00")).
				Bold(true),
		),
		style.Set(
			style.GenericHeadingWarn,
			lipgloss.NewStyle().
				Foreground(lipgloss.Color("#ffffff")).
				Background(lipgloss.Color("#aa5500")).
				Bold(true),
		),
		style.Set(
			style.GenericHeadingError,
			lipgloss.NewStyle().
				Foreground(lipgloss.Color("#ffffff")).
				Background(lipgloss.Color("#aa0000")).
				Bold(true),
		),
		style.Set(
			style.TextOK,
			base.Foreground(lipgloss.Color("#00aa00")),
		),
		style.Set(
			style.TextWarn,
			base.Foreground(lipgloss.Color("#aa5500")),
		),
		style.Set(
			style.TextError,
			base.Foreground(lipgloss.Color("#aa0000")),
		),
	)
}
