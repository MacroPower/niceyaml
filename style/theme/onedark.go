package theme

import (
	"charm.land/lipgloss/v2"

	"jacobcolvin.com/niceyaml/style"
)

// Onedark returns [style.Styles] using onedark colors.
func Onedark() style.Styles {
	base := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#ABB2BF")).
		Background(lipgloss.Color("#282C34"))

	return style.NewStyles(base,
		style.Set(
			style.Comment,
			base.Foreground(lipgloss.Color("#7F848E")),
		),
		style.Set(
			style.GenericDeleted,
			base.Foreground(lipgloss.Color("#E06C75")),
		),
		style.Set(
			style.GenericInserted,
			base.Foreground(lipgloss.Color("#98C379")).Bold(true),
		),
		style.Set(
			style.LiteralNumber,
			base.Foreground(lipgloss.Color("#D19A66")),
		),
		style.Set(
			style.LiteralString,
			base.Foreground(lipgloss.Color("#98C379")),
		),
		style.Set(
			style.Name,
			base.Foreground(lipgloss.Color("#E06C75")),
		),
		style.Set(
			style.NameDecorator,
			base.Foreground(lipgloss.Color("#61AFEF")),
		),
		style.Set(
			style.NameTag,
			base.Foreground(lipgloss.Color("#E06C75")),
		),
		style.Set(
			style.Punctuation,
			base.Foreground(lipgloss.Color("#ABB2BF")),
		),
		style.Set(
			style.LiteralBoolean,
			base.Foreground(lipgloss.Color("#E5C07B")),
		),
		style.Set(
			style.Search,
			lipgloss.NewStyle().Background(lipgloss.Lighten(lipgloss.Color("#282C34"), 0.15)),
		),
		style.Set(
			style.SearchSelected,
			lipgloss.NewStyle().Background(lipgloss.Lighten(lipgloss.Color("#282C34"), 0.30)),
		),
	)
}
