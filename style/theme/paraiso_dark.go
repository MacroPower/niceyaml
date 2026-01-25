package theme

import (
	"charm.land/lipgloss/v2"

	"jacobcolvin.com/niceyaml/style"
)

// ParaisoDark returns [style.Styles] using paraiso-dark colors.
func ParaisoDark() style.Styles {
	base := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#e7e9db")).
		Background(lipgloss.Color("#2f1e2e"))

	return style.NewStyles(base,
		style.Set(
			style.Comment,
			base.Foreground(lipgloss.Color("#776e71")),
		),
		style.Set(
			style.GenericDeleted,
			base.Foreground(lipgloss.Color("#ef6155")),
		),
		style.Set(
			style.GenericInserted,
			base.Foreground(lipgloss.Color("#48b685")),
		),
		style.Set(
			style.LiteralNumber,
			base.Foreground(lipgloss.Color("#f99b15")),
		),
		style.Set(
			style.LiteralString,
			base.Foreground(lipgloss.Color("#48b685")),
		),
		style.Set(
			style.Name,
			base.Foreground(lipgloss.Color("#e7e9db")),
		),
		style.Set(
			style.NameDecorator,
			base.Foreground(lipgloss.Color("#5bc4bf")),
		),
		style.Set(
			style.NameTag,
			base.Foreground(lipgloss.Color("#5bc4bf")),
		),
		style.Set(
			style.Punctuation,
			base.Foreground(lipgloss.Color("#e7e9db")),
		),
		style.Set(
			style.GenericError,
			base.Foreground(lipgloss.Color("#ef6155")),
		),
		style.Set(
			style.Generic,
			base.Italic(true),
		),
		style.Set(
			style.PunctuationHeading,
			base.Foreground(lipgloss.Color("#fec418")),
		),
	)
}
