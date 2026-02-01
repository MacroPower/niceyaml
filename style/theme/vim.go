package theme

import (
	"charm.land/lipgloss/v2"

	"go.jacobcolvin.com/niceyaml/style"
)

// Vim returns [style.Styles] using vim colors.
func Vim() style.Styles {
	base := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#cccccc")).
		Background(lipgloss.Color("#000000"))

	return style.NewStyles(base,
		style.Set(
			style.Comment,
			base.Foreground(lipgloss.Color("#000080")),
		),
		style.Set(
			style.GenericDeleted,
			base.Foreground(lipgloss.Color("#cd0000")),
		),
		style.Set(
			style.GenericError,
			base.Foreground(lipgloss.Color("#ff0000")),
		),
		style.Set(
			style.GenericInserted,
			base.Foreground(lipgloss.Color("#00cd00")),
		),
		style.Set(
			style.LiteralNumber,
			base.Foreground(lipgloss.Color("#cd00cd")),
		),
		style.Set(
			style.LiteralString,
			base.Foreground(lipgloss.Color("#cd0000")),
		),
		style.Set(
			style.Generic,
			base.Italic(true),
		),
		style.Set(
			style.NameTag,
			base.Foreground(lipgloss.Color("#cdcd00")),
		),
		style.Set(
			style.Name,
			base.Foreground(lipgloss.Color("#cd00cd")),
		),
		style.Set(
			style.Punctuation,
			base.Foreground(lipgloss.Color("#3399cc")),
		),
		style.Set(
			style.Search,
			lipgloss.NewStyle().Background(lipgloss.Lighten(lipgloss.Color("#000000"), 0.15)),
		),
		style.Set(
			style.SearchSelected,
			lipgloss.NewStyle().Background(lipgloss.Lighten(lipgloss.Color("#000000"), 0.30)),
		),
	)
}
