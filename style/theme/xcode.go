package theme

import (
	"charm.land/lipgloss/v2"

	"jacobcolvin.com/niceyaml/style"
)

// Xcode returns [style.Styles] using xcode colors.
func Xcode() style.Styles {
	base := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#000000")).
		Background(lipgloss.Color("#ffffff"))

	return style.NewStyles(base,
		style.Set(
			style.Comment,
			base.Foreground(lipgloss.Color("#177500")),
		),
		style.Set(
			style.LiteralNumber,
			base.Foreground(lipgloss.Color("#1c01ce")),
		),
		style.Set(
			style.LiteralString,
			base.Foreground(lipgloss.Color("#c41a16")),
		),
		style.Set(
			style.Name,
			base.Foreground(lipgloss.Color("#000000")),
		),
		style.Set(
			style.NameDecorator,
			base.Foreground(lipgloss.Color("#000000")),
		),
		style.Set(
			style.NameTag,
			base.Foreground(lipgloss.Color("#000000")),
		),
		style.Set(
			style.GenericError,
			base.Foreground(lipgloss.Color("#000000")),
		),
		style.Set(
			style.Punctuation,
			base.Foreground(lipgloss.Color("#000000")),
		),
	)
}
