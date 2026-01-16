package theme

import (
	"charm.land/lipgloss/v2"

	"github.com/macropower/niceyaml/style"
)

// Nordic returns [style.Styles] using nordic colors.
func Nordic() style.Styles {
	base := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#BBC3D4")).
		Background(lipgloss.Color("#242933"))

	return style.NewStyles(base,
		style.Set(
			style.Comment,
			base.Foreground(lipgloss.Color("#4C566A")).Italic(true),
		),
		style.Set(
			style.GenericDeleted,
			base.Foreground(lipgloss.Color("#C5727A")),
		),
		style.Set(
			style.GenericError,
			base.Foreground(lipgloss.Color("#C5727A")),
		),
		style.Set(
			style.GenericInserted,
			base.Foreground(lipgloss.Color("#A3BE8C")),
		),
		style.Set(
			style.LiteralNumber,
			base.Foreground(lipgloss.Color("#B48EAD")),
		),
		style.Set(
			style.LiteralString,
			base.Foreground(lipgloss.Color("#A3BE8C")),
		),
		style.Set(
			style.Name,
			base.Foreground(lipgloss.Color("#BBC3D4")),
		),
		style.Set(
			style.NameDecorator,
			base.Foreground(lipgloss.Color("#D08770")),
		),
		style.Set(
			style.NameTag,
			base.Foreground(lipgloss.Color("#5E81AC")),
		),
		style.Set(
			style.Punctuation,
			base.Foreground(lipgloss.Color("#ECEFF4")),
		),
		style.Set(
			style.Generic,
			base.Italic(true),
		),
		style.Set(
			style.PunctuationHeading,
			base.Foreground(lipgloss.Color("#8FBCBB")),
		),
	)
}
