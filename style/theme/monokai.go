package theme

import (
	"charm.land/lipgloss/v2"

	"github.com/macropower/niceyaml/style"
)

// Monokai returns [style.Styles] using monokai colors.
func Monokai() style.Styles {
	base := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#f8f8f2")).
		Background(lipgloss.Color("#272822"))

	return style.NewStyles(base,
		style.Set(
			style.Comment,
			base.Foreground(lipgloss.Color("#75715e")),
		),
		style.Set(
			style.GenericDeleted,
			base.Foreground(lipgloss.Color("#f92672")),
		),
		style.Set(
			style.GenericInserted,
			base.Foreground(lipgloss.Color("#a6e22e")),
		),
		style.Set(
			style.LiteralNumber,
			base.Foreground(lipgloss.Color("#ae81ff")),
		),
		style.Set(
			style.LiteralString,
			base.Foreground(lipgloss.Color("#e6db74")),
		),
		style.Set(
			style.Name,
			base.Foreground(lipgloss.Color("#f8f8f2")),
		),
		style.Set(
			style.NameDecorator,
			base.Foreground(lipgloss.Color("#a6e22e")),
		),
		style.Set(
			style.NameTag,
			base.Foreground(lipgloss.Color("#f92672")),
		),
		style.Set(
			style.Punctuation,
			base.Foreground(lipgloss.Color("#f8f8f2")),
		),
		style.Set(
			style.GenericError,
			base.Foreground(lipgloss.Color("#960050")).Background(lipgloss.Color("#1e0010")),
		),
		style.Set(
			style.Generic,
			base.Italic(true),
		),
	)
}
