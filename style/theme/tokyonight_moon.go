package theme

import (
	"charm.land/lipgloss/v2"

	"go.jacobcolvin.com/niceyaml/style"
)

// TokyonightMoon returns [style.Styles] using tokyonight-moon colors.
func TokyonightMoon() style.Styles {
	base := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#c8d3f5")).
		Background(lipgloss.Color("#222436"))

	return style.NewStyles(base,
		style.Set(
			style.Comment,
			base.Foreground(lipgloss.Color("#444a73")).Italic(true),
		),
		style.Set(
			style.Generic,
			base.Foreground(lipgloss.Color("#c8d3f5")),
		),
		style.Set(
			style.GenericDeleted,
			base.Foreground(lipgloss.Color("#c53b53")).Background(lipgloss.Color("#1b1d2b")),
		),
		style.Set(
			style.GenericError,
			base.Foreground(lipgloss.Color("#c53b53")),
		),
		style.Set(
			style.GenericInserted,
			base.Foreground(lipgloss.Color("#c3e88d")).Background(lipgloss.Color("#1b1d2b")),
		),
		style.Set(
			style.LiteralNumber,
			base.Foreground(lipgloss.Color("#ffc777")),
		),
		style.Set(
			style.LiteralNumberBin,
			base.Foreground(lipgloss.Color("#ffc777")),
		),
		style.Set(
			style.LiteralNumberFloat,
			base.Foreground(lipgloss.Color("#ffc777")),
		),
		style.Set(
			style.LiteralNumberHex,
			base.Foreground(lipgloss.Color("#ffc777")),
		),
		style.Set(
			style.LiteralNumberInteger,
			base.Foreground(lipgloss.Color("#ffc777")),
		),
		style.Set(
			style.LiteralNumberOct,
			base.Foreground(lipgloss.Color("#ffc777")),
		),
		style.Set(
			style.LiteralString,
			base.Foreground(lipgloss.Color("#c3e88d")),
		),
		style.Set(
			style.LiteralStringDouble,
			base.Foreground(lipgloss.Color("#c3e88d")),
		),
		style.Set(
			style.LiteralStringSingle,
			base.Foreground(lipgloss.Color("#c3e88d")),
		),
		style.Set(
			style.Name,
			base.Foreground(lipgloss.Color("#c8d3f5")),
		),
		style.Set(
			style.NameDecorator,
			base.Foreground(lipgloss.Color("#82aaff")).Bold(true),
		),
		style.Set(
			style.NameTag,
			base.Foreground(lipgloss.Color("#c099ff")),
		),
		style.Set(
			style.CommentPreproc,
			base.Foreground(lipgloss.Color("#444a73")).Bold(true),
		),
		style.Set(
			style.LiteralBoolean,
			base.Foreground(lipgloss.Color("#ffc777")),
		),
		style.Set(
			style.PunctuationHeading,
			base.Foreground(lipgloss.Color("#ffc777")),
		),
		style.Set(
			style.Punctuation,
			base.Foreground(lipgloss.Color("#c3e88d")).Bold(true),
		),
		style.Set(
			style.Search,
			lipgloss.NewStyle().Background(lipgloss.Lighten(lipgloss.Color("#222436"), 0.15)),
		),
		style.Set(
			style.SearchSelected,
			lipgloss.NewStyle().Background(lipgloss.Lighten(lipgloss.Color("#222436"), 0.30)),
		),
	)
}
