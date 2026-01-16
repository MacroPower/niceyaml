package theme

import (
	"charm.land/lipgloss/v2"

	"github.com/macropower/niceyaml/style"
)

// CatppuccinMacchiato returns [style.Styles] using catppuccin-macchiato colors.
func CatppuccinMacchiato() style.Styles {
	base := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#cad3f5")).
		Background(lipgloss.Color("#24273a"))

	return style.NewStyles(base,
		style.Set(
			style.Comment,
			base.Foreground(lipgloss.Color("#6e738d")).Italic(true),
		),
		style.Set(
			style.Generic,
			base.Foreground(lipgloss.Color("#cad3f5")),
		),
		style.Set(
			style.GenericDeleted,
			base.Foreground(lipgloss.Color("#ed8796")).Background(lipgloss.Color("#363a4f")),
		),
		style.Set(
			style.GenericError,
			base.Foreground(lipgloss.Color("#ed8796")),
		),
		style.Set(
			style.GenericInserted,
			base.Foreground(lipgloss.Color("#a6da95")).Background(lipgloss.Color("#363a4f")),
		),
		style.Set(
			style.LiteralNumber,
			base.Foreground(lipgloss.Color("#f5a97f")),
		),
		style.Set(
			style.LiteralNumberBin,
			base.Foreground(lipgloss.Color("#f5a97f")),
		),
		style.Set(
			style.LiteralNumberFloat,
			base.Foreground(lipgloss.Color("#f5a97f")),
		),
		style.Set(
			style.LiteralNumberHex,
			base.Foreground(lipgloss.Color("#f5a97f")),
		),
		style.Set(
			style.LiteralNumberInteger,
			base.Foreground(lipgloss.Color("#f5a97f")),
		),
		style.Set(
			style.LiteralNumberOct,
			base.Foreground(lipgloss.Color("#f5a97f")),
		),
		style.Set(
			style.LiteralString,
			base.Foreground(lipgloss.Color("#a6da95")),
		),
		style.Set(
			style.LiteralStringDouble,
			base.Foreground(lipgloss.Color("#a6da95")),
		),
		style.Set(
			style.LiteralStringSingle,
			base.Foreground(lipgloss.Color("#a6da95")),
		),
		style.Set(
			style.Name,
			base.Foreground(lipgloss.Color("#cad3f5")),
		),
		style.Set(
			style.NameDecorator,
			base.Foreground(lipgloss.Color("#8aadf4")).Bold(true),
		),
		style.Set(
			style.NameTag,
			base.Foreground(lipgloss.Color("#c6a0f6")),
		),
		style.Set(
			style.CommentPreproc,
			base.Foreground(lipgloss.Color("#6e738d")).Bold(true),
		),
		style.Set(
			style.LiteralBoolean,
			base.Foreground(lipgloss.Color("#f5a97f")),
		),
		style.Set(
			style.PunctuationHeading,
			base.Foreground(lipgloss.Color("#f5a97f")),
		),
		style.Set(
			style.Punctuation,
			base.Foreground(lipgloss.Color("#91d7e3")).Bold(true),
		),
	)
}
