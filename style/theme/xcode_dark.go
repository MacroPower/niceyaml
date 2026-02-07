package theme

import (
	"charm.land/lipgloss/v2"

	"go.jacobcolvin.com/niceyaml/style"
)

// XcodeDark returns [style.Styles] using xcode-dark colors.
func XcodeDark() style.Styles {
	base := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#ffffff")).
		Background(lipgloss.Color("#1f1f24"))

	return style.NewStyles(base,
		style.Set(
			style.Comment,
			base.Foreground(lipgloss.Color("#6c7986")),
		),
		style.Set(
			style.LiteralNumber,
			base.Foreground(lipgloss.Color("#d0bf69")),
		),
		style.Set(
			style.LiteralNumberBin,
			base.Foreground(lipgloss.Color("#d0bf69")),
		),
		style.Set(
			style.LiteralNumberFloat,
			base.Foreground(lipgloss.Color("#d0bf69")),
		),
		style.Set(
			style.LiteralNumberHex,
			base.Foreground(lipgloss.Color("#d0bf69")),
		),
		style.Set(
			style.LiteralNumberInteger,
			base.Foreground(lipgloss.Color("#d0bf69")),
		),
		style.Set(
			style.LiteralNumberOct,
			base.Foreground(lipgloss.Color("#d0bf69")),
		),
		style.Set(
			style.LiteralString,
			base.Foreground(lipgloss.Color("#fc6a5d")),
		),
		style.Set(
			style.Name,
			base.Foreground(lipgloss.Color("#ffffff")),
		),
		style.Set(
			style.Punctuation,
			base.Foreground(lipgloss.Color("#ffffff")),
		),
		style.Set(
			style.NameDecorator,
			base.Foreground(lipgloss.Color("#fd8f3f")),
		),
		style.Set(
			style.GenericError,
			base.Foreground(lipgloss.Color("#960050")),
		),
		style.Set(
			style.NameTag,
			base.Foreground(lipgloss.Color("#fc5fa3")),
		),
		style.Set(
			style.LiteralBoolean,
			base.Foreground(lipgloss.Color("#fc5fa3")),
		),
		style.Set(
			style.Title,
			lipgloss.NewStyle().
				Foreground(lipgloss.Color("#1f1f24")).
				Background(lipgloss.Color("#fc5fa3")).
				Bold(true),
		),
		style.Set(
			style.TitleAccent,
			base.Background(lipgloss.Lighten(lipgloss.Color("#1f1f24"), 0.30)).
				Foreground(lipgloss.Lighten(lipgloss.Color("#ffffff"), 0.15)),
		),
		style.Set(
			style.TitleSubtle,
			base.Background(lipgloss.Lighten(lipgloss.Color("#1f1f24"), 0.15)),
		),
		style.Set(
			style.TextAccent,
			base.Foreground(lipgloss.Lighten(lipgloss.Color("#ffffff"), 0.15)),
		),
		style.Set(
			style.TextAccentSelected,
			base.Foreground(lipgloss.Color("#ffffff")),
		),
		style.Set(
			style.TextSubtle,
			base.Foreground(lipgloss.Darken(lipgloss.Color("#ffffff"), 0.15)),
		),
		style.Set(
			style.TextSubtleSelected,
			base.Foreground(lipgloss.Color("#ffffff")),
		),
		style.Set(
			style.Highlight,
			lipgloss.NewStyle().Background(lipgloss.Lighten(lipgloss.Color("#1f1f24"), 0.15)),
		),
		style.Set(
			style.HighlightSelected,
			lipgloss.NewStyle().Background(lipgloss.Lighten(lipgloss.Color("#1f1f24"), 0.30)),
		),
	)
}
