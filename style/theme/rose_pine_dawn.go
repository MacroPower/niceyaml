package theme

import (
	"charm.land/lipgloss/v2"

	"jacobcolvin.com/niceyaml/style"
)

// RosePineDawn returns [style.Styles] using rose-pine-dawn colors.
func RosePineDawn() style.Styles {
	base := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#575279")).
		Background(lipgloss.Color("#faf4ed"))

	return style.NewStyles(base,
		style.Set(
			style.Comment,
			base.Foreground(lipgloss.Color("#9893a5")),
		),
		style.Set(
			style.GenericDeleted,
			base.Foreground(lipgloss.Color("#b4637a")),
		),
		style.Set(
			style.GenericInserted,
			base.Foreground(lipgloss.Color("#56949f")),
		),
		style.Set(
			style.LiteralNumber,
			base.Foreground(lipgloss.Color("#ea9d34")),
		),
		style.Set(
			style.LiteralString,
			base.Foreground(lipgloss.Color("#ea9d34")),
		),
		style.Set(
			style.Name,
			base.Foreground(lipgloss.Color("#d7827e")),
		),
		style.Set(
			style.NameDecorator,
			base.Foreground(lipgloss.Color("#797593")),
		),
		style.Set(
			style.NameTag,
			base.Foreground(lipgloss.Color("#d7827e")),
		),
		style.Set(
			style.Punctuation,
			base.Foreground(lipgloss.Color("#797593")),
		),
		style.Set(
			style.GenericError,
			base.Foreground(lipgloss.Color("#b4637a")),
		),
		style.Set(
			style.Generic,
			base.Italic(true),
		),
	)
}
