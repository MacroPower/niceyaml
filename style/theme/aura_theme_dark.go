package theme

import (
	"charm.land/lipgloss/v2"

	"jacobcolvin.com/niceyaml/style"
)

// AuraThemeDark returns [style.Styles] using aura-theme-dark colors.
func AuraThemeDark() style.Styles {
	base := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#edecee")).
		Background(lipgloss.Color("#15141b"))

	return style.NewStyles(base,
		style.Set(
			style.Comment,
			base.Foreground(lipgloss.Color("#6d6d6d")).Italic(true),
		),
		style.Set(
			style.Generic,
			base.Foreground(lipgloss.Color("#edecee")),
		),
		style.Set(
			style.GenericDeleted,
			base.Foreground(lipgloss.Color("#ff6767")).Background(lipgloss.Color("#15141b")),
		),
		style.Set(
			style.GenericError,
			base.Foreground(lipgloss.Color("#ff6767")),
		),
		style.Set(
			style.GenericInserted,
			base.Foreground(lipgloss.Color("#61ffca")).Background(lipgloss.Color("#15141b")),
		),
		style.Set(
			style.LiteralNumber,
			base.Foreground(lipgloss.Color("#61ffca")),
		),
		style.Set(
			style.LiteralNumberBin,
			base.Foreground(lipgloss.Color("#61ffca")),
		),
		style.Set(
			style.LiteralNumberFloat,
			base.Foreground(lipgloss.Color("#61ffca")),
		),
		style.Set(
			style.LiteralNumberHex,
			base.Foreground(lipgloss.Color("#61ffca")),
		),
		style.Set(
			style.LiteralNumberInteger,
			base.Foreground(lipgloss.Color("#61ffca")),
		),
		style.Set(
			style.LiteralNumberOct,
			base.Foreground(lipgloss.Color("#61ffca")),
		),
		style.Set(
			style.LiteralString,
			base.Foreground(lipgloss.Color("#61ffca")),
		),
		style.Set(
			style.LiteralStringDouble,
			base.Foreground(lipgloss.Color("#61ffca")),
		),
		style.Set(
			style.LiteralStringSingle,
			base.Foreground(lipgloss.Color("#61ffca")),
		),
		style.Set(
			style.Name,
			base.Foreground(lipgloss.Color("#edecee")),
		),
		style.Set(
			style.NameDecorator,
			base.Foreground(lipgloss.Color("#f694ff")),
		),
		style.Set(
			style.NameTag,
			base.Foreground(lipgloss.Color("#a277ff")),
		),
		style.Set(
			style.Punctuation,
			base.Foreground(lipgloss.Color("#f694ff")),
		),
		style.Set(
			style.CommentPreproc,
			base.Foreground(lipgloss.Color("#6d6d6d")).Italic(true),
		),
		style.Set(
			style.LiteralBoolean,
			base.Foreground(lipgloss.Color("#61ffca")),
		),
		style.Set(
			style.PunctuationHeading,
			base.Foreground(lipgloss.Color("#edecee")),
		),
	)
}
