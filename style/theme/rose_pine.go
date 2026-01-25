package theme

import (
	"charm.land/lipgloss/v2"

	"jacobcolvin.com/niceyaml/style"
)

// RosePine returns [style.Styles] using rose-pine colors.
func RosePine() style.Styles {
	base := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#e0def4")).
		Background(lipgloss.Color("#191724"))

	return style.NewStyles(base,
		style.Set(
			style.Comment,
			base.Foreground(lipgloss.Color("#6e6a86")),
		),
		style.Set(
			style.GenericDeleted,
			base.Foreground(lipgloss.Color("#eb6f92")),
		),
		style.Set(
			style.GenericInserted,
			base.Foreground(lipgloss.Color("#9ccfd8")),
		),
		style.Set(
			style.LiteralNumber,
			base.Foreground(lipgloss.Color("#f6c177")),
		),
		style.Set(
			style.LiteralString,
			base.Foreground(lipgloss.Color("#f6c177")),
		),
		style.Set(
			style.Name,
			base.Foreground(lipgloss.Color("#ebbcba")),
		),
		style.Set(
			style.NameDecorator,
			base.Foreground(lipgloss.Color("#908caa")),
		),
		style.Set(
			style.NameTag,
			base.Foreground(lipgloss.Color("#ebbcba")),
		),
		style.Set(
			style.Punctuation,
			base.Foreground(lipgloss.Color("#908caa")),
		),
		style.Set(
			style.GenericError,
			base.Foreground(lipgloss.Color("#eb6f92")),
		),
		style.Set(
			style.Generic,
			base.Italic(true),
		),
	)
}
