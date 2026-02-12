package theme

import (
	"charm.land/lipgloss/v2"

	"go.jacobcolvin.com/niceyaml/style"
)

// Base16Snazzy returns [style.Styles] using base16-snazzy colors.
func Base16Snazzy() style.Styles {
	base := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#e2e4e5")).
		Background(lipgloss.Color("#282a36"))

	return style.NewStyles(base,
		style.Set(
			style.Comment,
			base.Foreground(lipgloss.Color("#78787e")),
		),
		style.Set(
			style.Generic,
			base.Foreground(lipgloss.Color("#e2e4e5")),
		),
		style.Set(
			style.GenericDeleted,
			base.Foreground(lipgloss.Color("#ff5c57")),
		),
		style.Set(
			style.GenericError,
			base.Foreground(lipgloss.Color("#ff5c57")),
		),
		style.Set(
			style.GenericInserted,
			base.Foreground(lipgloss.Color("#e2e4e5")).Bold(true),
		),
		style.Set(
			style.LiteralNumber,
			base.Foreground(lipgloss.Color("#ff9f43")),
		),
		style.Set(
			style.LiteralNumberBin,
			base.Foreground(lipgloss.Color("#ff9f43")),
		),
		style.Set(
			style.LiteralNumberFloat,
			base.Foreground(lipgloss.Color("#ff9f43")),
		),
		style.Set(
			style.LiteralNumberHex,
			base.Foreground(lipgloss.Color("#ff9f43")),
		),
		style.Set(
			style.LiteralNumberInteger,
			base.Foreground(lipgloss.Color("#ff9f43")),
		),
		style.Set(
			style.LiteralNumberOct,
			base.Foreground(lipgloss.Color("#ff9f43")),
		),
		style.Set(
			style.LiteralString,
			base.Foreground(lipgloss.Color("#5af78e")),
		),
		style.Set(
			style.LiteralStringDouble,
			base.Foreground(lipgloss.Color("#5af78e")),
		),
		style.Set(
			style.LiteralStringSingle,
			base.Foreground(lipgloss.Color("#5af78e")),
		),
		style.Set(
			style.Name,
			base.Foreground(lipgloss.Color("#e2e4e5")),
		),
		style.Set(
			style.NameDecorator,
			base.Foreground(lipgloss.Color("#ff9f43")),
		),
		style.Set(
			style.NameTag,
			base.Foreground(lipgloss.Color("#ff6ac1")),
		),
		style.Set(
			style.Punctuation,
			base.Foreground(lipgloss.Color("#e2e4e5")),
		),
		style.Set(
			style.LiteralBoolean,
			base.Foreground(lipgloss.Color("#ff6ac1")),
		),
		style.Set(
			style.PunctuationHeading,
			base.Foreground(lipgloss.Color("#e2e4e5")),
		),
		style.Set(
			style.GenericHeading,
			lipgloss.NewStyle().
				Foreground(lipgloss.Color("#282a36")).
				Background(lipgloss.Color("#ff6ac1")).
				Bold(true),
		),
		style.Set(
			style.GenericHeadingAccent,
			base.Background(lipgloss.Lighten(lipgloss.Color("#282a36"), 0.30)).
				Foreground(lipgloss.Lighten(lipgloss.Color("#e2e4e5"), 0.15)),
		),
		style.Set(
			style.GenericHeadingSubtle,
			base.Background(lipgloss.Lighten(lipgloss.Color("#282a36"), 0.15)),
		),
		style.Set(
			style.TextAccentDim,
			base.Foreground(lipgloss.Lighten(lipgloss.Color("#ff6ac1"), 0.15)),
		),
		style.Set(
			style.TextAccent,
			base.Foreground(lipgloss.Color("#ff6ac1")),
		),
		style.Set(
			style.TextSubtleDim,
			base.Foreground(lipgloss.Darken(lipgloss.Color("#e2e4e5"), 0.15)),
		),
		style.Set(
			style.TextSubtle,
			base.Foreground(lipgloss.Color("#e2e4e5")),
		),
		style.Set(
			style.GenericHighlightDim,
			lipgloss.NewStyle().Background(lipgloss.Lighten(lipgloss.Color("#282a36"), 0.15)),
		),
		style.Set(
			style.GenericHighlight,
			lipgloss.NewStyle().Background(lipgloss.Lighten(lipgloss.Color("#282a36"), 0.30)),
		),
		style.Set(
			style.GenericHeadingOK,
			lipgloss.NewStyle().
				Foreground(lipgloss.Color("#282a36")).
				Background(lipgloss.Color("#5af78e")).
				Bold(true),
		),
		style.Set(
			style.GenericHeadingWarn,
			lipgloss.NewStyle().
				Foreground(lipgloss.Color("#282a36")).
				Background(lipgloss.Color("#f3f99d")).
				Bold(true),
		),
		style.Set(
			style.GenericHeadingError,
			lipgloss.NewStyle().
				Foreground(lipgloss.Color("#282a36")).
				Background(lipgloss.Color("#ff5c57")).
				Bold(true),
		),
		style.Set(
			style.TextOK,
			base.Foreground(lipgloss.Color("#5af78e")),
		),
		style.Set(
			style.TextWarn,
			base.Foreground(lipgloss.Color("#f3f99d")),
		),
		style.Set(
			style.TextError,
			base.Foreground(lipgloss.Color("#ff5c57")),
		),
	)
}
