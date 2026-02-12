package theme

import (
	"charm.land/lipgloss/v2"

	"go.jacobcolvin.com/niceyaml/style"
)

// DoomOne2 returns [style.Styles] using doom-one2 colors.
func DoomOne2() style.Styles {
	base := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#b0c4de")).
		Background(lipgloss.Color("#282c34"))

	return style.NewStyles(base,
		style.Set(
			style.Comment,
			base.Foreground(lipgloss.Color("#8a93a5")).Italic(true),
		),
		style.Set(
			style.Generic,
			base.Foreground(lipgloss.Color("#b0c4de")),
		),
		style.Set(
			style.GenericDeleted,
			base.Foreground(lipgloss.Color("#b0c4de")),
		),
		style.Set(
			style.GenericInserted,
			base.Foreground(lipgloss.Color("#a6e22e")),
		),
		style.Set(
			style.LiteralNumber,
			base.Foreground(lipgloss.Color("#d19a66")),
		),
		style.Set(
			style.LiteralNumberBin,
			base.Foreground(lipgloss.Color("#d19a66")),
		),
		style.Set(
			style.LiteralNumberFloat,
			base.Foreground(lipgloss.Color("#d19a66")),
		),
		style.Set(
			style.LiteralNumberHex,
			base.Foreground(lipgloss.Color("#d19a66")),
		),
		style.Set(
			style.LiteralNumberInteger,
			base.Foreground(lipgloss.Color("#d19a66")),
		),
		style.Set(
			style.LiteralNumberOct,
			base.Foreground(lipgloss.Color("#d19a66")),
		),
		style.Set(
			style.LiteralString,
			base.Foreground(lipgloss.Color("#98c379")),
		),
		style.Set(
			style.LiteralStringDouble,
			base.Foreground(lipgloss.Color("#63c381")),
		),
		style.Set(
			style.LiteralStringSingle,
			base.Foreground(lipgloss.Color("#98c379")),
		),
		style.Set(
			style.Name,
			base.Foreground(lipgloss.Color("#aa89ea")),
		),
		style.Set(
			style.NameDecorator,
			base.Foreground(lipgloss.Color("#e5c07b")),
		),
		style.Set(
			style.NameTag,
			base.Foreground(lipgloss.Color("#76a9f9")),
		),
		style.Set(
			style.Punctuation,
			base.Foreground(lipgloss.Color("#abb2bf")),
		),
		style.Set(
			style.GenericError,
			base.Foreground(lipgloss.Color("#b0c4de")),
		),
		style.Set(
			style.LiteralBoolean,
			base.Foreground(lipgloss.Color("#e5c07b")),
		),
		style.Set(
			style.PunctuationHeading,
			base.Foreground(lipgloss.Color("#ca72ff")),
		),
		style.Set(
			style.GenericHeading,
			lipgloss.NewStyle().
				Foreground(lipgloss.Color("#282c34")).
				Background(lipgloss.Color("#76a9f9")).
				Bold(true),
		),
		style.Set(
			style.GenericHeadingAccent,
			base.Background(lipgloss.Lighten(lipgloss.Color("#282c34"), 0.30)).
				Foreground(lipgloss.Lighten(lipgloss.Color("#b0c4de"), 0.15)),
		),
		style.Set(
			style.GenericHeadingSubtle,
			base.Background(lipgloss.Lighten(lipgloss.Color("#282c34"), 0.15)),
		),
		style.Set(
			style.TextAccentDim,
			base.Foreground(lipgloss.Lighten(lipgloss.Color("#76a9f9"), 0.15)),
		),
		style.Set(
			style.TextAccent,
			base.Foreground(lipgloss.Color("#76a9f9")),
		),
		style.Set(
			style.TextSubtleDim,
			base.Foreground(lipgloss.Darken(lipgloss.Color("#b0c4de"), 0.15)),
		),
		style.Set(
			style.TextSubtle,
			base.Foreground(lipgloss.Color("#b0c4de")),
		),
		style.Set(
			style.GenericHighlightDim,
			lipgloss.NewStyle().Background(lipgloss.Lighten(lipgloss.Color("#282c34"), 0.15)),
		),
		style.Set(
			style.GenericHighlight,
			lipgloss.NewStyle().Background(lipgloss.Lighten(lipgloss.Color("#282c34"), 0.30)),
		),
		style.Set(
			style.GenericHeadingOK,
			lipgloss.NewStyle().
				Foreground(lipgloss.Color("#282c34")).
				Background(lipgloss.Color("#a6e22e")).
				Bold(true),
		),
		style.Set(
			style.GenericHeadingWarn,
			lipgloss.NewStyle().
				Foreground(lipgloss.Color("#282c34")).
				Background(lipgloss.Color("#ecbe7b")).
				Bold(true),
		),
		style.Set(
			style.GenericHeadingError,
			lipgloss.NewStyle().
				Foreground(lipgloss.Color("#282c34")).
				Background(lipgloss.Color("#ff6c6b")).
				Bold(true),
		),
		style.Set(
			style.TextOK,
			base.Foreground(lipgloss.Color("#a6e22e")),
		),
		style.Set(
			style.TextWarn,
			base.Foreground(lipgloss.Color("#ecbe7b")),
		),
		style.Set(
			style.TextError,
			base.Foreground(lipgloss.Color("#ff6c6b")),
		),
	)
}
