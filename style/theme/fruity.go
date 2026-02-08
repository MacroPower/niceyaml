package theme

import (
	"charm.land/lipgloss/v2"

	"go.jacobcolvin.com/niceyaml/style"
)

// Fruity returns [style.Styles] using fruity colors.
func Fruity() style.Styles {
	base := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#ffffff")).
		Background(lipgloss.Color("#111111"))

	return style.NewStyles(base,
		style.Set(
			style.Comment,
			base.Foreground(lipgloss.Color("#008800")).Background(lipgloss.Color("#0f140f")).Italic(true),
		),
		style.Set(
			style.LiteralNumber,
			base.Foreground(lipgloss.Color("#0086f7")).Bold(true),
		),
		style.Set(
			style.LiteralString,
			base.Foreground(lipgloss.Color("#0086d2")),
		),
		style.Set(
			style.NameTag,
			base.Foreground(lipgloss.Color("#fb660a")).Bold(true),
		),
		style.Set(
			style.NameDecorator,
			base.Foreground(lipgloss.Color("#ff0007")).Bold(true),
		),
		style.Set(
			style.Generic,
			base.Foreground(lipgloss.Color("#ffffff")).Bold(true),
		),
		style.Set(
			style.Name,
			base.Foreground(lipgloss.Color("#0086d2")),
		),
		style.Set(
			style.Title,
			lipgloss.NewStyle().
				Foreground(lipgloss.Color("#111111")).
				Background(lipgloss.Color("#fb660a")).
				Bold(true),
		),
		style.Set(
			style.TitleAccent,
			base.Background(lipgloss.Lighten(lipgloss.Color("#111111"), 0.30)).
				Foreground(lipgloss.Lighten(lipgloss.Color("#ffffff"), 0.15)),
		),
		style.Set(
			style.TitleSubtle,
			base.Background(lipgloss.Lighten(lipgloss.Color("#111111"), 0.15)),
		),
		style.Set(
			style.TextAccent,
			base.Foreground(lipgloss.Lighten(lipgloss.Color("#fb660a"), 0.15)),
		),
		style.Set(
			style.TextAccentSelected,
			base.Foreground(lipgloss.Color("#fb660a")),
		),
		style.Set(
			style.TextSubtle,
			base.Foreground(lipgloss.Darken(lipgloss.Color("#ffffff"), 0.15)),
		),
		style.Set(
			style.TextSubtleSelected,
			base.Foreground(lipgloss.Color("#ffffff")),
		),
		style.Set(
			style.Highlight,
			lipgloss.NewStyle().Background(lipgloss.Lighten(lipgloss.Color("#111111"), 0.15)),
		),
		style.Set(
			style.HighlightSelected,
			lipgloss.NewStyle().Background(lipgloss.Lighten(lipgloss.Color("#111111"), 0.30)),
		),
		style.Set(
			style.TitleOK,
			lipgloss.NewStyle().
				Foreground(lipgloss.Color("#111111")).
				Background(lipgloss.Color("#00ff00")).
				Bold(true),
		),
		style.Set(
			style.TitleWarn,
			lipgloss.NewStyle().
				Foreground(lipgloss.Color("#111111")).
				Background(lipgloss.Color("#ffb86c")).
				Bold(true),
		),
		style.Set(
			style.TitleError,
			lipgloss.NewStyle().
				Foreground(lipgloss.Color("#111111")).
				Background(lipgloss.Color("#ff0000")).
				Bold(true),
		),
		style.Set(
			style.TextOK,
			base.Foreground(lipgloss.Color("#00ff00")),
		),
		style.Set(
			style.TextWarn,
			base.Foreground(lipgloss.Color("#ffb86c")),
		),
		style.Set(
			style.TextError,
			base.Foreground(lipgloss.Color("#ff0000")),
		),
	)
}
