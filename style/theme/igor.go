package theme

import (
	"charm.land/lipgloss/v2"

	"jacobcolvin.com/niceyaml/style"
)

// Igor returns [style.Styles] using igor colors.
func Igor() style.Styles {
	base := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#000000")).
		Background(lipgloss.Color("#ffffff"))

	return style.NewStyles(base,
		style.Set(
			style.Comment,
			base.Foreground(lipgloss.Color("#ff0000")).Italic(true),
		),
		style.Set(
			style.LiteralString,
			base.Foreground(lipgloss.Color("#009c00")),
		),
		style.Set(
			style.NameDecorator,
			base.Foreground(lipgloss.Color("#cc00a3")),
		),
		style.Set(
			style.NameTag,
			base.Foreground(lipgloss.Color("#0000ff")),
		),
		style.Set(
			style.Name,
			base.Foreground(lipgloss.Color("#007575")),
		),
	)
}
