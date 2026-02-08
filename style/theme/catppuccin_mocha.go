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
			style.Title,
			lipgloss.NewStyle().
				Foreground(lipgloss.Color("#1e1e2e")).
				Background(lipgloss.Color("#cba6f7")).
				Bold(true),
		),
		style.Set(
			style.TitleAccent,
			base.Background(lipgloss.Lighten(lipgloss.Color("#1e1e2e"), 0.30)).
				Foreground(lipgloss.Lighten(lipgloss.Color("#cdd6f4"), 0.15)),
		),
		style.Set(
			style.TitleSubtle,
			base.Background(lipgloss.Lighten(lipgloss.Color("#1e1e2e"), 0.15)),
		),
		style.Set(
			style.TextAccent,
			base.Foreground(lipgloss.Lighten(lipgloss.Color("#cba6f7"), 0.15)),
		),
		style.Set(
			style.TextAccentSelected,
			base.Foreground(lipgloss.Color("#cba6f7")),
		),
		style.Set(
			style.TextSubtle,
			base.Foreground(lipgloss.Darken(lipgloss.Color("#cdd6f4"), 0.15)),
		),
		style.Set(
			style.TextSubtleSelected,
			base.Foreground(lipgloss.Color("#cdd6f4")),
		),
		style.Set(
			style.Highlight,
			lipgloss.NewStyle().Background(lipgloss.Lighten(lipgloss.Color("#1e1e2e"), 0.15)),
		),
		style.Set(
			style.HighlightSelected,
			lipgloss.NewStyle().Background(lipgloss.Lighten(lipgloss.Color("#1e1e2e"), 0.30)),
		),
		style.Set(
			style.TitleOK,
			lipgloss.NewStyle().
				Foreground(lipgloss.Color("#1e1e2e")).
				Background(lipgloss.Color("#a6e3a1")).
				Bold(true),
		),
		style.Set(
			style.TitleWarn,
			lipgloss.NewStyle().
				Foreground(lipgloss.Color("#1e1e2e")).
				Background(lipgloss.Color("#f9e2af")).
				Bold(true),
		),
		style.Set(
			style.TitleError,
			lipgloss.NewStyle().
				Foreground(lipgloss.Color("#1e1e2e")).
				Background(lipgloss.Color("#f38ba8")).
				Bold(true),
		),
		style.Set(
			style.TextOK,
			base.Foreground(lipgloss.Color("#a6e3a1")),
		),
		style.Set(
			style.TextWarn,
			base.Foreground(lipgloss.Color("#f9e2af")),
		),
		style.Set(
			style.TextError,
			base.Foreground(lipgloss.Color("#f38ba8")),
		),
	)
}
