package theme

import (
	"charm.land/lipgloss/v2"

	"go.jacobcolvin.com/niceyaml/style"
)

// Murphy returns [style.Styles] using murphy colors.
func Murphy() style.Styles {
	base := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#000000")).
		Background(lipgloss.Color("#ffffff"))

	return style.NewStyles(base,
		style.Set(
			style.Comment,
			base.Foreground(lipgloss.Color("#666666")).Italic(true),
		),
		style.Set(
			style.GenericDeleted,
			base.Foreground(lipgloss.Color("#a00000")),
		),
		style.Set(
			style.GenericError,
			base.Foreground(lipgloss.Color("#ff0000")),
		),
		style.Set(
			style.GenericInserted,
			base.Foreground(lipgloss.Color("#00a000")),
		),
		style.Set(
			style.LiteralNumber,
			base.Foreground(lipgloss.Color("#6600ee")).Bold(true),
		),
		style.Set(
			style.LiteralNumberFloat,
			base.Foreground(lipgloss.Color("#6600ee")).Bold(true),
		),
		style.Set(
			style.LiteralNumberHex,
			base.Foreground(lipgloss.Color("#005588")).Bold(true),
		),
		style.Set(
			style.LiteralNumberInteger,
			base.Foreground(lipgloss.Color("#6666ff")).Bold(true),
		),
		style.Set(
			style.LiteralNumberOct,
			base.Foreground(lipgloss.Color("#4400ee")).Bold(true),
		),
		style.Set(
			style.LiteralString,
			base.Background(lipgloss.Color("#e0e0ff")),
		),
		style.Set(
			style.NameDecorator,
			base.Foreground(lipgloss.Color("#555555")).Bold(true),
		),
		style.Set(
			style.NameTag,
			base.Foreground(lipgloss.Color("#007700")),
		),
		style.Set(
			style.Generic,
			base.Italic(true),
		),
		style.Set(
			style.Name,
			base.Foreground(lipgloss.Color("#007722")),
		),
		style.Set(
			style.PunctuationHeading,
			base.Foreground(lipgloss.Color("#0e84b5")).Bold(true),
		),
		style.Set(
			style.Punctuation,
			base.Foreground(lipgloss.Color("#333333")),
		),
		style.Set(
			style.Search,
			lipgloss.NewStyle().Background(lipgloss.Darken(lipgloss.Color("#ffffff"), 0.15)),
		),
		style.Set(
			style.SearchSelected,
			lipgloss.NewStyle().Background(lipgloss.Darken(lipgloss.Color("#ffffff"), 0.30)),
		),
	)
}
