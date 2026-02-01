package theme

import (
	"charm.land/lipgloss/v2"

	"jacobcolvin.com/niceyaml/style"
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
			style.Search,
			lipgloss.NewStyle().Background(lipgloss.Lighten(lipgloss.Color("#282a36"), 0.15)),
		),
		style.Set(
			style.SearchSelected,
			lipgloss.NewStyle().Background(lipgloss.Lighten(lipgloss.Color("#282a36"), 0.30)),
		),
	)
}
