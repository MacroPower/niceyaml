package theme

import (
	"charm.land/lipgloss/v2"

	"go.jacobcolvin.com/niceyaml/style"
)

// Average returns [style.Styles] using average colors.
func Average() style.Styles {
	base := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#757575")).
		Background(lipgloss.Color("#000000"))

	return style.NewStyles(base,
		style.Set(
			style.Comment,
			base.Foreground(lipgloss.Color("#757575")),
		),
		style.Set(
			style.Generic,
			base.Foreground(lipgloss.Color("#757575")),
		),
		style.Set(
			style.GenericDeleted,
			base.Foreground(lipgloss.Color("#ec0000")),
		),
		style.Set(
			style.GenericError,
			base.Foreground(lipgloss.Color("#ec0000")),
		),
		style.Set(
			style.GenericInserted,
			base.Foreground(lipgloss.Color("#757575")).Bold(true),
		),
		style.Set(
			style.LiteralNumber,
			base.Foreground(lipgloss.Color("#008900")),
		),
		style.Set(
			style.LiteralNumberBin,
			base.Foreground(lipgloss.Color("#008900")),
		),
		style.Set(
			style.LiteralNumberFloat,
			base.Foreground(lipgloss.Color("#008900")),
		),
		style.Set(
			style.LiteralNumberHex,
			base.Foreground(lipgloss.Color("#008900")),
		),
		style.Set(
			style.LiteralNumberInteger,
			base.Foreground(lipgloss.Color("#008900")),
		),
		style.Set(
			style.LiteralNumberOct,
			base.Foreground(lipgloss.Color("#008900")),
		),
		style.Set(
			style.LiteralString,
			base.Foreground(lipgloss.Color("#008900")),
		),
		style.Set(
			style.LiteralStringDouble,
			base.Foreground(lipgloss.Color("#008900")),
		),
		style.Set(
			style.LiteralStringSingle,
			base.Foreground(lipgloss.Color("#008900")),
		),
		style.Set(
			style.Name,
			base.Foreground(lipgloss.Color("#757575")),
		),
		style.Set(
			style.NameDecorator,
			base.Foreground(lipgloss.Color("#008900")),
		),
		style.Set(
			style.NameTag,
			base.Foreground(lipgloss.Color("#ec0000")),
		),
		style.Set(
			style.Punctuation,
			base.Foreground(lipgloss.Color("#757575")),
		),
		style.Set(
			style.LiteralBoolean,
			base.Foreground(lipgloss.Color("#ec0000")),
		),
		style.Set(
			style.PunctuationHeading,
			base.Foreground(lipgloss.Color("#757575")),
		),
		style.Set(
			style.GenericHeading,
			lipgloss.NewStyle().
				Foreground(lipgloss.Color("#000000")).
				Background(lipgloss.Color("#ec0000")).
				Bold(true),
		),
		style.Set(
			style.GenericHeadingAccent,
			base.Background(lipgloss.Lighten(lipgloss.Color("#000000"), 0.30)).
				Foreground(lipgloss.Lighten(lipgloss.Color("#757575"), 0.15)),
		),
		style.Set(
			style.GenericHeadingSubtle,
			base.Background(lipgloss.Lighten(lipgloss.Color("#000000"), 0.15)),
		),
		style.Set(
			style.TextAccentDim,
			base.Foreground(lipgloss.Lighten(lipgloss.Color("#ec0000"), 0.15)),
		),
		style.Set(
			style.TextAccent,
			base.Foreground(lipgloss.Color("#ec0000")),
		),
		style.Set(
			style.TextSubtleDim,
			base.Foreground(lipgloss.Darken(lipgloss.Color("#757575"), 0.15)),
		),
		style.Set(
			style.TextSubtle,
			base.Foreground(lipgloss.Color("#757575")),
		),
		style.Set(
			style.GenericHighlightDim,
			lipgloss.NewStyle().Background(lipgloss.Lighten(lipgloss.Color("#000000"), 0.15)),
		),
		style.Set(
			style.GenericHighlight,
			lipgloss.NewStyle().Background(lipgloss.Lighten(lipgloss.Color("#000000"), 0.30)),
		),
		style.Set(
			style.GenericHeadingOK,
			lipgloss.NewStyle().
				Foreground(lipgloss.Color("#000000")).
				Background(lipgloss.Color("#5faf5f")).
				Bold(true),
		),
		style.Set(
			style.GenericHeadingWarn,
			lipgloss.NewStyle().
				Foreground(lipgloss.Color("#000000")).
				Background(lipgloss.Color("#e5c07b")).
				Bold(true),
		),
		style.Set(
			style.GenericHeadingError,
			lipgloss.NewStyle().
				Foreground(lipgloss.Color("#000000")).
				Background(lipgloss.Color("#ec0000")).
				Bold(true),
		),
		style.Set(
			style.TextOK,
			base.Foreground(lipgloss.Color("#5faf5f")),
		),
		style.Set(
			style.TextWarn,
			base.Foreground(lipgloss.Color("#e5c07b")),
		),
		style.Set(
			style.TextError,
			base.Foreground(lipgloss.Color("#ec0000")),
		),
	)
}
