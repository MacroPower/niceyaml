package theme

import (
	"charm.land/lipgloss/v2"

	"github.com/macropower/niceyaml/style"
)

// CatppuccinLatte returns [style.Styles] using catppuccin-latte colors.
func CatppuccinLatte() style.Styles {
	base := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#4c4f69")).
		Background(lipgloss.Color("#eff1f5"))

	return style.NewStyles(base,
		style.Set(
			style.Comment,
			base.Foreground(lipgloss.Color("#9ca0b0")).Italic(true),
		),
		style.Set(
			style.Generic,
			base.Foreground(lipgloss.Color("#4c4f69")),
		),
		style.Set(
			style.GenericDeleted,
			base.Foreground(lipgloss.Color("#d20f39")).Background(lipgloss.Color("#ccd0da")),
		),
		style.Set(
			style.GenericError,
			base.Foreground(lipgloss.Color("#d20f39")),
		),
		style.Set(
			style.GenericInserted,
			base.Foreground(lipgloss.Color("#40a02b")).Background(lipgloss.Color("#ccd0da")),
		),
		style.Set(
			style.LiteralNumber,
			base.Foreground(lipgloss.Color("#fe640b")),
		),
		style.Set(
			style.LiteralNumberBin,
			base.Foreground(lipgloss.Color("#fe640b")),
		),
		style.Set(
			style.LiteralNumberFloat,
			base.Foreground(lipgloss.Color("#fe640b")),
		),
		style.Set(
			style.LiteralNumberHex,
			base.Foreground(lipgloss.Color("#fe640b")),
		),
		style.Set(
			style.LiteralNumberInteger,
			base.Foreground(lipgloss.Color("#fe640b")),
		),
		style.Set(
			style.LiteralNumberOct,
			base.Foreground(lipgloss.Color("#fe640b")),
		),
		style.Set(
			style.LiteralString,
			base.Foreground(lipgloss.Color("#40a02b")),
		),
		style.Set(
			style.LiteralStringDouble,
			base.Foreground(lipgloss.Color("#40a02b")),
		),
		style.Set(
			style.LiteralStringSingle,
			base.Foreground(lipgloss.Color("#40a02b")),
		),
		style.Set(
			style.Name,
			base.Foreground(lipgloss.Color("#4c4f69")),
		),
		style.Set(
			style.NameDecorator,
			base.Foreground(lipgloss.Color("#1e66f5")).Bold(true),
		),
		style.Set(
			style.NameTag,
			base.Foreground(lipgloss.Color("#8839ef")),
		),
		style.Set(
			style.CommentPreproc,
			base.Foreground(lipgloss.Color("#9ca0b0")).Bold(true),
		),
		style.Set(
			style.LiteralBoolean,
			base.Foreground(lipgloss.Color("#fe640b")),
		),
		style.Set(
			style.PunctuationHeading,
			base.Foreground(lipgloss.Color("#fe640b")),
		),
		style.Set(
			style.Punctuation,
			base.Foreground(lipgloss.Color("#04a5e5")).Bold(true),
		),
	)
}
