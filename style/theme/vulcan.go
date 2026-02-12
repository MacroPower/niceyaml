package theme

import (
	"charm.land/lipgloss/v2"

	"go.jacobcolvin.com/niceyaml/style"
)

// Vulcan returns [style.Styles] using vulcan colors.
func Vulcan() style.Styles {
	base := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#c9c9c9")).
		Background(lipgloss.Color("#282c34"))

	return style.NewStyles(base,
		style.Set(
			style.Comment,
			base.Foreground(lipgloss.Color("#3e4460")),
		),
		style.Set(
			style.Generic,
			base.Foreground(lipgloss.Color("#c9c9c9")),
		),
		style.Set(
			style.GenericDeleted,
			base.Foreground(lipgloss.Color("#cf5967")),
		),
		style.Set(
			style.GenericError,
			base.Foreground(lipgloss.Color("#cf5967")).Bold(true),
		),
		style.Set(
			style.GenericInserted,
			base.Foreground(lipgloss.Color("#ecbe7b")),
		),
		style.Set(
			style.LiteralNumber,
			base.Foreground(lipgloss.Color("#56b6c2")),
		),
		style.Set(
			style.LiteralNumberBin,
			base.Foreground(lipgloss.Color("#57c7ff")),
		),
		style.Set(
			style.LiteralNumberFloat,
			base.Foreground(lipgloss.Color("#56b6c2")),
		),
		style.Set(
			style.LiteralNumberHex,
			base.Foreground(lipgloss.Color("#57c7ff")),
		),
		style.Set(
			style.LiteralNumberInteger,
			base.Foreground(lipgloss.Color("#56b6c2")),
		),
		style.Set(
			style.LiteralNumberOct,
			base.Foreground(lipgloss.Color("#57c7ff")),
		),
		style.Set(
			style.LiteralString,
			base.Foreground(lipgloss.Color("#82cc6a")),
		),
		style.Set(
			style.LiteralStringDouble,
			base.Foreground(lipgloss.Color("#82cc6a")),
		),
		style.Set(
			style.LiteralStringSingle,
			base.Foreground(lipgloss.Color("#82cc6a")),
		),
		style.Set(
			style.Name,
			base.Foreground(lipgloss.Color("#c9c9c9")),
		),
		style.Set(
			style.NameDecorator,
			base.Foreground(lipgloss.Color("#ecbe7b")),
		),
		style.Set(
			style.NameTag,
			base.Foreground(lipgloss.Color("#bc74c4")),
		),
		style.Set(
			style.Punctuation,
			base.Foreground(lipgloss.Color("#56b6c2")),
		),
		style.Set(
			style.LiteralBoolean,
			base.Foreground(lipgloss.Color("#cf5967")).Background(lipgloss.Color("#43454f")),
		),
		style.Set(
			style.PunctuationHeading,
			base.Foreground(lipgloss.Color("#c9c9c9")),
		),
		style.Set(
			style.GenericHeading,
			lipgloss.NewStyle().
				Foreground(lipgloss.Color("#282c34")).
				Background(lipgloss.Color("#bc74c4")).
				Bold(true),
		),
		style.Set(
			style.GenericHeadingAccent,
			base.Background(lipgloss.Lighten(lipgloss.Color("#282c34"), 0.30)).
				Foreground(lipgloss.Lighten(lipgloss.Color("#c9c9c9"), 0.15)),
		),
		style.Set(
			style.GenericHeadingSubtle,
			base.Background(lipgloss.Lighten(lipgloss.Color("#282c34"), 0.15)),
		),
		style.Set(
			style.TextAccentDim,
			base.Foreground(lipgloss.Lighten(lipgloss.Color("#bc74c4"), 0.15)),
		),
		style.Set(
			style.TextAccent,
			base.Foreground(lipgloss.Color("#bc74c4")),
		),
		style.Set(
			style.TextSubtleDim,
			base.Foreground(lipgloss.Darken(lipgloss.Color("#c9c9c9"), 0.15)),
		),
		style.Set(
			style.TextSubtle,
			base.Foreground(lipgloss.Color("#c9c9c9")),
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
				Background(lipgloss.Color("#ecbe7b")).
				Bold(true),
		),
		style.Set(
			style.GenericHeadingWarn,
			lipgloss.NewStyle().
				Foreground(lipgloss.Color("#282c34")).
				Background(lipgloss.Color("#ffb86c")).
				Bold(true),
		),
		style.Set(
			style.GenericHeadingError,
			lipgloss.NewStyle().
				Foreground(lipgloss.Color("#282c34")).
				Background(lipgloss.Color("#cf5967")).
				Bold(true),
		),
		style.Set(
			style.TextOK,
			base.Foreground(lipgloss.Color("#ecbe7b")),
		),
		style.Set(
			style.TextWarn,
			base.Foreground(lipgloss.Color("#ffb86c")),
		),
		style.Set(
			style.TextError,
			base.Foreground(lipgloss.Color("#cf5967")),
		),
	)
}
