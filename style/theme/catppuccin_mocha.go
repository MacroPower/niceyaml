package theme

import (
	"charm.land/lipgloss/v2"

	"go.jacobcolvin.com/niceyaml/style"
)

// CatppuccinMocha returns [style.Styles] using catppuccin-mocha colors.
func CatppuccinMocha() style.Styles {
	base := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#cdd6f4")).
		Background(lipgloss.Color("#1e1e2e"))

	return style.NewStyles(base,
		style.Set(
			style.Comment,
			base.Foreground(lipgloss.Color("#6c7086")).Italic(true),
		),
		style.Set(
			style.Generic,
			base.Foreground(lipgloss.Color("#cdd6f4")),
		),
		style.Set(
			style.GenericDeleted,
			base.Foreground(lipgloss.Color("#f38ba8")).Background(lipgloss.Color("#313244")),
		),
		style.Set(
			style.GenericError,
			base.Foreground(lipgloss.Color("#f38ba8")),
		),
		style.Set(
			style.GenericInserted,
			base.Foreground(lipgloss.Color("#a6e3a1")).Background(lipgloss.Color("#313244")),
		),
		style.Set(
			style.LiteralNumber,
			base.Foreground(lipgloss.Color("#fab387")),
		),
		style.Set(
			style.LiteralNumberBin,
			base.Foreground(lipgloss.Color("#fab387")),
		),
		style.Set(
			style.LiteralNumberFloat,
			base.Foreground(lipgloss.Color("#fab387")),
		),
		style.Set(
			style.LiteralNumberHex,
			base.Foreground(lipgloss.Color("#fab387")),
		),
		style.Set(
			style.LiteralNumberInteger,
			base.Foreground(lipgloss.Color("#fab387")),
		),
		style.Set(
			style.LiteralNumberOct,
			base.Foreground(lipgloss.Color("#fab387")),
		),
		style.Set(
			style.LiteralString,
			base.Foreground(lipgloss.Color("#a6e3a1")),
		),
		style.Set(
			style.LiteralStringDouble,
			base.Foreground(lipgloss.Color("#a6e3a1")),
		),
		style.Set(
			style.LiteralStringSingle,
			base.Foreground(lipgloss.Color("#a6e3a1")),
		),
		style.Set(
			style.Name,
			base.Foreground(lipgloss.Color("#cdd6f4")),
		),
		style.Set(
			style.NameDecorator,
			base.Foreground(lipgloss.Color("#89b4fa")).Bold(true),
		),
		style.Set(
			style.NameTag,
			base.Foreground(lipgloss.Color("#cba6f7")),
		),
		style.Set(
			style.CommentPreproc,
			base.Foreground(lipgloss.Color("#6c7086")).Bold(true),
		),
		style.Set(
			style.LiteralBoolean,
			base.Foreground(lipgloss.Color("#fab387")),
		),
		style.Set(
			style.PunctuationHeading,
			base.Foreground(lipgloss.Color("#fab387")),
		),
		style.Set(
			style.Punctuation,
			base.Foreground(lipgloss.Color("#89dceb")).Bold(true),
		),
		style.Set(
			style.Search,
			lipgloss.NewStyle().Background(lipgloss.Lighten(lipgloss.Color("#1e1e2e"), 0.15)),
		),
		style.Set(
			style.SearchSelected,
			lipgloss.NewStyle().Background(lipgloss.Lighten(lipgloss.Color("#1e1e2e"), 0.30)),
		),
	)
}
