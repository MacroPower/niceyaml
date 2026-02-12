package theme

import (
	"charm.land/lipgloss/v2"

	"go.jacobcolvin.com/niceyaml/style"
)

// Tango returns [style.Styles] using tango colors.
func Tango() style.Styles {
	base := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#000000")).
		Background(lipgloss.Color("#f8f8f8"))

	return style.NewStyles(base,
		style.Set(
			style.Comment,
			base.Foreground(lipgloss.Color("#8f5902")).Italic(true),
		),
		style.Set(
			style.Generic,
			base.Foreground(lipgloss.Color("#000000")),
		),
		style.Set(
			style.GenericDeleted,
			base.Foreground(lipgloss.Color("#a40000")),
		),
		style.Set(
			style.GenericError,
			base.Foreground(lipgloss.Color("#ef2929")),
		),
		style.Set(
			style.GenericInserted,
			base.Foreground(lipgloss.Color("#00a000")),
		),
		style.Set(
			style.LiteralNumber,
			base.Foreground(lipgloss.Color("#0000cf")).Bold(true),
		),
		style.Set(
			style.LiteralNumberFloat,
			base.Foreground(lipgloss.Color("#0000cf")).Bold(true),
		),
		style.Set(
			style.LiteralNumberHex,
			base.Foreground(lipgloss.Color("#0000cf")).Bold(true),
		),
		style.Set(
			style.LiteralNumberInteger,
			base.Foreground(lipgloss.Color("#0000cf")).Bold(true),
		),
		style.Set(
			style.LiteralNumberOct,
			base.Foreground(lipgloss.Color("#0000cf")).Bold(true),
		),
		style.Set(
			style.LiteralString,
			base.Foreground(lipgloss.Color("#4e9a06")),
		),
		style.Set(
			style.LiteralStringDouble,
			base.Foreground(lipgloss.Color("#4e9a06")),
		),
		style.Set(
			style.LiteralStringSingle,
			base.Foreground(lipgloss.Color("#4e9a06")),
		),
		style.Set(
			style.Name,
			base.Foreground(lipgloss.Color("#000000")),
		),
		style.Set(
			style.NameDecorator,
			base.Foreground(lipgloss.Color("#5c35cc")).Bold(true),
		),
		style.Set(
			style.NameTag,
			base.Foreground(lipgloss.Color("#204a87")).Bold(true),
		),
		style.Set(
			style.Punctuation,
			base.Foreground(lipgloss.Color("#000000")).Bold(true),
		),
		style.Set(
			style.LiteralBoolean,
			base.Foreground(lipgloss.Color("#204a87")).Bold(true),
		),
		style.Set(
			style.PunctuationHeading,
			base.Foreground(lipgloss.Color("#000000")),
		),
		style.Set(
			style.GenericHeading,
			lipgloss.NewStyle().
				Foreground(lipgloss.Color("#f8f8f8")).
				Background(lipgloss.Color("#204a87")).
				Bold(true),
		),
		style.Set(
			style.GenericHeadingAccent,
			base.Background(lipgloss.Darken(lipgloss.Color("#f8f8f8"), 0.30)).
				Foreground(lipgloss.Darken(lipgloss.Color("#000000"), 0.15)),
		),
		style.Set(
			style.GenericHeadingSubtle,
			base.Background(lipgloss.Darken(lipgloss.Color("#f8f8f8"), 0.15)),
		),
		style.Set(
			style.TextAccentDim,
			base.Foreground(lipgloss.Darken(lipgloss.Color("#204a87"), 0.15)),
		),
		style.Set(
			style.TextAccent,
			base.Foreground(lipgloss.Color("#204a87")),
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
			lipgloss.NewStyle().Background(lipgloss.Darken(lipgloss.Color("#f8f8f8"), 0.15)),
		),
		style.Set(
			style.GenericHighlight,
			lipgloss.NewStyle().Background(lipgloss.Darken(lipgloss.Color("#f8f8f8"), 0.30)),
		),
		style.Set(
			style.GenericHeadingOK,
			lipgloss.NewStyle().
				Foreground(lipgloss.Color("#f8f8f8")).
				Background(lipgloss.Color("#00a000")).
				Bold(true),
		),
		style.Set(
			style.GenericHeadingWarn,
			lipgloss.NewStyle().
				Foreground(lipgloss.Color("#f8f8f8")).
				Background(lipgloss.Color("#c4a000")).
				Bold(true),
		),
		style.Set(
			style.GenericHeadingError,
			lipgloss.NewStyle().
				Foreground(lipgloss.Color("#f8f8f8")).
				Background(lipgloss.Color("#a40000")).
				Bold(true),
		),
		style.Set(
			style.TextOK,
			base.Foreground(lipgloss.Color("#00a000")),
		),
		style.Set(
			style.TextWarn,
			base.Foreground(lipgloss.Color("#c4a000")),
		),
		style.Set(
			style.TextError,
			base.Foreground(lipgloss.Color("#a40000")),
		),
	)
}
