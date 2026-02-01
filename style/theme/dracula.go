package theme

import (
	"charm.land/lipgloss/v2"

	"jacobcolvin.com/niceyaml/style"
)

// Dracula returns [style.Styles] using dracula colors.
func Dracula() style.Styles {
	base := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#f8f8f2")).
		Background(lipgloss.Color("#282a36"))

	return style.NewStyles(base,
		style.Set(
			style.Comment,
			base.Foreground(lipgloss.Color("#6272a4")),
		),
		style.Set(
			style.Generic,
			base.Foreground(lipgloss.Color("#f8f8f2")),
		),
		style.Set(
			style.GenericDeleted,
			base.Foreground(lipgloss.Color("#ff5555")),
		),
		style.Set(
			style.GenericError,
			base.Foreground(lipgloss.Color("#f8f8f2")),
		),
		style.Set(
			style.GenericInserted,
			base.Foreground(lipgloss.Color("#50fa7b")).Bold(true),
		),
		style.Set(
			style.LiteralNumber,
			base.Foreground(lipgloss.Color("#bd93f9")),
		),
		style.Set(
			style.LiteralNumberBin,
			base.Foreground(lipgloss.Color("#bd93f9")),
		),
		style.Set(
			style.LiteralNumberFloat,
			base.Foreground(lipgloss.Color("#bd93f9")),
		),
		style.Set(
			style.LiteralNumberHex,
			base.Foreground(lipgloss.Color("#bd93f9")),
		),
		style.Set(
			style.LiteralNumberInteger,
			base.Foreground(lipgloss.Color("#bd93f9")),
		),
		style.Set(
			style.LiteralNumberOct,
			base.Foreground(lipgloss.Color("#bd93f9")),
		),
		style.Set(
			style.LiteralString,
			base.Foreground(lipgloss.Color("#f1fa8c")),
		),
		style.Set(
			style.LiteralStringDouble,
			base.Foreground(lipgloss.Color("#f1fa8c")),
		),
		style.Set(
			style.LiteralStringSingle,
			base.Foreground(lipgloss.Color("#f1fa8c")),
		),
		style.Set(
			style.Name,
			base.Foreground(lipgloss.Color("#f8f8f2")),
		),
		style.Set(
			style.NameDecorator,
			base.Foreground(lipgloss.Color("#f8f8f2")),
		),
		style.Set(
			style.NameTag,
			base.Foreground(lipgloss.Color("#ff79c6")),
		),
		style.Set(
			style.Punctuation,
			base.Foreground(lipgloss.Color("#f8f8f2")),
		),
		style.Set(
			style.LiteralBoolean,
			base.Foreground(lipgloss.Color("#ff79c6")),
		),
		style.Set(
			style.PunctuationHeading,
			base.Foreground(lipgloss.Color("#f8f8f2")),
		),
		style.Set(
			style.Search,
			lipgloss.NewStyle().Background(lipgloss.Lighten(lipgloss.Color("#282a36"), 0.15)),
		),
		style.Set(
			style.SearchSelected,
			lipgloss.NewStyle().Background(lipgloss.Lighten(lipgloss.Color("#282a36"), 0.30)),
		),
	)
}
