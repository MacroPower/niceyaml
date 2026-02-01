package theme

import (
	"charm.land/lipgloss/v2"

	"jacobcolvin.com/niceyaml/style"
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
			style.Search,
			lipgloss.NewStyle().Background(lipgloss.Lighten(lipgloss.Color("#282c34"), 0.15)),
		),
		style.Set(
			style.SearchSelected,
			lipgloss.NewStyle().Background(lipgloss.Lighten(lipgloss.Color("#282c34"), 0.30)),
		),
	)
}
