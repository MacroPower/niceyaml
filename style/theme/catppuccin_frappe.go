package theme

import (
	"charm.land/lipgloss/v2"

	"go.jacobcolvin.com/niceyaml/style"
)

// CatppuccinFrappe returns [style.Styles] using catppuccin-frappe colors.
func CatppuccinFrappe() style.Styles {
	base := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#c6d0f5")).
		Background(lipgloss.Color("#303446"))

	return style.NewStyles(base,
		style.Set(
			style.Comment,
			base.Foreground(lipgloss.Color("#737994")).Italic(true),
		),
		style.Set(
			style.Generic,
			base.Foreground(lipgloss.Color("#c6d0f5")),
		),
		style.Set(
			style.GenericDeleted,
			base.Foreground(lipgloss.Color("#e78284")).Background(lipgloss.Color("#414559")),
		),
		style.Set(
			style.GenericError,
			base.Foreground(lipgloss.Color("#e78284")),
		),
		style.Set(
			style.GenericInserted,
			base.Foreground(lipgloss.Color("#a6d189")).Background(lipgloss.Color("#414559")),
		),
		style.Set(
			style.LiteralNumber,
			base.Foreground(lipgloss.Color("#ef9f76")),
		),
		style.Set(
			style.LiteralNumberBin,
			base.Foreground(lipgloss.Color("#ef9f76")),
		),
		style.Set(
			style.LiteralNumberFloat,
			base.Foreground(lipgloss.Color("#ef9f76")),
		),
		style.Set(
			style.LiteralNumberHex,
			base.Foreground(lipgloss.Color("#ef9f76")),
		),
		style.Set(
			style.LiteralNumberInteger,
			base.Foreground(lipgloss.Color("#ef9f76")),
		),
		style.Set(
			style.LiteralNumberOct,
			base.Foreground(lipgloss.Color("#ef9f76")),
		),
		style.Set(
			style.LiteralString,
			base.Foreground(lipgloss.Color("#a6d189")),
		),
		style.Set(
			style.LiteralStringDouble,
			base.Foreground(lipgloss.Color("#a6d189")),
		),
		style.Set(
			style.LiteralStringSingle,
			base.Foreground(lipgloss.Color("#a6d189")),
		),
		style.Set(
			style.Name,
			base.Foreground(lipgloss.Color("#c6d0f5")),
		),
		style.Set(
			style.NameDecorator,
			base.Foreground(lipgloss.Color("#8caaee")).Bold(true),
		),
		style.Set(
			style.NameTag,
			base.Foreground(lipgloss.Color("#ca9ee6")),
		),
		style.Set(
			style.CommentPreproc,
			base.Foreground(lipgloss.Color("#737994")).Bold(true),
		),
		style.Set(
			style.LiteralBoolean,
			base.Foreground(lipgloss.Color("#ef9f76")),
		),
		style.Set(
			style.PunctuationHeading,
			base.Foreground(lipgloss.Color("#ef9f76")),
		),
		style.Set(
			style.Punctuation,
			base.Foreground(lipgloss.Color("#99d1db")).Bold(true),
		),
		style.Set(
			style.Title,
			lipgloss.NewStyle().
				Foreground(lipgloss.Color("#303446")).
				Background(lipgloss.Color("#ca9ee6")).
				Bold(true),
		),
		style.Set(
			style.TitleAccent,
			base.Background(lipgloss.Lighten(lipgloss.Color("#303446"), 0.30)).
				Foreground(lipgloss.Lighten(lipgloss.Color("#c6d0f5"), 0.15)),
		),
		style.Set(
			style.TitleSubtle,
			base.Background(lipgloss.Lighten(lipgloss.Color("#303446"), 0.15)),
		),
		style.Set(
			style.TextAccent,
			base.Foreground(lipgloss.Lighten(lipgloss.Color("#ca9ee6"), 0.15)),
		),
		style.Set(
			style.TextAccentSelected,
			base.Foreground(lipgloss.Color("#ca9ee6")),
		),
		style.Set(
			style.TextSubtle,
			base.Foreground(lipgloss.Darken(lipgloss.Color("#c6d0f5"), 0.15)),
		),
		style.Set(
			style.TextSubtleSelected,
			base.Foreground(lipgloss.Color("#c6d0f5")),
		),
		style.Set(
			style.Highlight,
			lipgloss.NewStyle().Background(lipgloss.Lighten(lipgloss.Color("#303446"), 0.15)),
		),
		style.Set(
			style.HighlightSelected,
			lipgloss.NewStyle().Background(lipgloss.Lighten(lipgloss.Color("#303446"), 0.30)),
		),
		style.Set(
			style.TitleOK,
			lipgloss.NewStyle().
				Foreground(lipgloss.Color("#303446")).
				Background(lipgloss.Color("#a6d189")).
				Bold(true),
		),
		style.Set(
			style.TitleWarn,
			lipgloss.NewStyle().
				Foreground(lipgloss.Color("#303446")).
				Background(lipgloss.Color("#e5c890")).
				Bold(true),
		),
		style.Set(
			style.TitleError,
			lipgloss.NewStyle().
				Foreground(lipgloss.Color("#303446")).
				Background(lipgloss.Color("#e78284")).
				Bold(true),
		),
		style.Set(
			style.TextOK,
			base.Foreground(lipgloss.Color("#a6d189")),
		),
		style.Set(
			style.TextWarn,
			base.Foreground(lipgloss.Color("#e5c890")),
		),
		style.Set(
			style.TextError,
			base.Foreground(lipgloss.Color("#e78284")),
		),
	)
}
