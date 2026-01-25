package theme

import (
	"charm.land/lipgloss/v2"

	"jacobcolvin.com/niceyaml/style"
)

// AuraThemeDarkSoft returns [style.Styles] using aura-theme-dark-soft colors.
func AuraThemeDarkSoft() style.Styles {
	base := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#bdbdbd")).
		Background(lipgloss.Color("#15141b"))

	return style.NewStyles(base,
		style.Set(
			style.Comment,
			base.Foreground(lipgloss.Color("#6d6d6d")).Italic(true),
		),
		style.Set(
			style.Generic,
			base.Foreground(lipgloss.Color("#bdbdbd")),
		),
		style.Set(
			style.GenericDeleted,
			base.Foreground(lipgloss.Color("#c55858")).Background(lipgloss.Color("#15141b")),
		),
		style.Set(
			style.GenericError,
			base.Foreground(lipgloss.Color("#c55858")),
		),
		style.Set(
			style.GenericInserted,
			base.Foreground(lipgloss.Color("#54c59f")).Background(lipgloss.Color("#15141b")),
		),
		style.Set(
			style.LiteralNumber,
			base.Foreground(lipgloss.Color("#54c59f")),
		),
		style.Set(
			style.LiteralNumberBin,
			base.Foreground(lipgloss.Color("#54c59f")),
		),
		style.Set(
			style.LiteralNumberFloat,
			base.Foreground(lipgloss.Color("#54c59f")),
		),
		style.Set(
			style.LiteralNumberHex,
			base.Foreground(lipgloss.Color("#54c59f")),
		),
		style.Set(
			style.LiteralNumberInteger,
			base.Foreground(lipgloss.Color("#54c59f")),
		),
		style.Set(
			style.LiteralNumberOct,
			base.Foreground(lipgloss.Color("#54c59f")),
		),
		style.Set(
			style.LiteralString,
			base.Foreground(lipgloss.Color("#54c59f")),
		),
		style.Set(
			style.LiteralStringDouble,
			base.Foreground(lipgloss.Color("#54c59f")),
		),
		style.Set(
			style.LiteralStringSingle,
			base.Foreground(lipgloss.Color("#54c59f")),
		),
		style.Set(
			style.Name,
			base.Foreground(lipgloss.Color("#bdbdbd")),
		),
		style.Set(
			style.NameDecorator,
			base.Foreground(lipgloss.Color("#c17ac8")),
		),
		style.Set(
			style.NameTag,
			base.Foreground(lipgloss.Color("#8464c6")),
		),
		style.Set(
			style.Punctuation,
			base.Foreground(lipgloss.Color("#c17ac8")),
		),
		style.Set(
			style.CommentPreproc,
			base.Foreground(lipgloss.Color("#6d6d6d")).Italic(true),
		),
		style.Set(
			style.LiteralBoolean,
			base.Foreground(lipgloss.Color("#54c59f")),
		),
		style.Set(
			style.PunctuationHeading,
			base.Foreground(lipgloss.Color("#bdbdbd")),
		),
	)
}
