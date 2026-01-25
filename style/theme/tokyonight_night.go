package theme

import (
	"charm.land/lipgloss/v2"

	"jacobcolvin.com/niceyaml/style"
)

// TokyonightNight returns [style.Styles] using tokyonight-night colors.
func TokyonightNight() style.Styles {
	base := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#c0caf5")).
		Background(lipgloss.Color("#1a1b26"))

	return style.NewStyles(base,
		style.Set(
			style.Comment,
			base.Foreground(lipgloss.Color("#414868")).Italic(true),
		),
		style.Set(
			style.Generic,
			base.Foreground(lipgloss.Color("#c0caf5")),
		),
		style.Set(
			style.GenericDeleted,
			base.Foreground(lipgloss.Color("#db4b4b")).Background(lipgloss.Color("#15161e")),
		),
		style.Set(
			style.GenericError,
			base.Foreground(lipgloss.Color("#db4b4b")),
		),
		style.Set(
			style.GenericInserted,
			base.Foreground(lipgloss.Color("#9ece6a")).Background(lipgloss.Color("#15161e")),
		),
		style.Set(
			style.LiteralNumber,
			base.Foreground(lipgloss.Color("#e0af68")),
		),
		style.Set(
			style.LiteralNumberBin,
			base.Foreground(lipgloss.Color("#e0af68")),
		),
		style.Set(
			style.LiteralNumberFloat,
			base.Foreground(lipgloss.Color("#e0af68")),
		),
		style.Set(
			style.LiteralNumberHex,
			base.Foreground(lipgloss.Color("#e0af68")),
		),
		style.Set(
			style.LiteralNumberInteger,
			base.Foreground(lipgloss.Color("#e0af68")),
		),
		style.Set(
			style.LiteralNumberOct,
			base.Foreground(lipgloss.Color("#e0af68")),
		),
		style.Set(
			style.LiteralString,
			base.Foreground(lipgloss.Color("#9ece6a")),
		),
		style.Set(
			style.LiteralStringDouble,
			base.Foreground(lipgloss.Color("#9ece6a")),
		),
		style.Set(
			style.LiteralStringSingle,
			base.Foreground(lipgloss.Color("#9ece6a")),
		),
		style.Set(
			style.Name,
			base.Foreground(lipgloss.Color("#c0caf5")),
		),
		style.Set(
			style.NameDecorator,
			base.Foreground(lipgloss.Color("#7aa2f7")).Bold(true),
		),
		style.Set(
			style.NameTag,
			base.Foreground(lipgloss.Color("#bb9af7")),
		),
		style.Set(
			style.CommentPreproc,
			base.Foreground(lipgloss.Color("#414868")).Bold(true),
		),
		style.Set(
			style.LiteralBoolean,
			base.Foreground(lipgloss.Color("#e0af68")),
		),
		style.Set(
			style.PunctuationHeading,
			base.Foreground(lipgloss.Color("#e0af68")),
		),
		style.Set(
			style.Punctuation,
			base.Foreground(lipgloss.Color("#9ece6a")).Bold(true),
		),
	)
}
