package theme

import (
	"charm.land/lipgloss/v2"

	"go.jacobcolvin.com/niceyaml/style"
)

// Onesenterprise returns [style.Styles] using onesenterprise colors.
func Onesenterprise() style.Styles {
	base := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#000000"))

	return style.NewStyles(base,
		style.Set(
			style.Comment,
			base.Foreground(lipgloss.Color("#008000")),
		),
		style.Set(
			style.LiteralString,
			base.Foreground(lipgloss.Color("#000000")),
		),
		style.Set(
			style.Name,
			base.Foreground(lipgloss.Color("#0000ff")),
		),
		style.Set(
			style.Punctuation,
			base.Foreground(lipgloss.Color("#ff0000")),
		),
		style.Set(
			style.NameDecorator,
			base.Foreground(lipgloss.Color("#963200")),
		),
		style.Set(
			style.NameTag,
			base.Foreground(lipgloss.Color("#ff0000")),
		),
		style.Set(
			style.GenericHeading,
			lipgloss.NewStyle().
				Background(lipgloss.Color("#ff0000")).
				Bold(true),
		),
		style.Set(
			style.GenericHeadingAccent,
			base.Background(lipgloss.Darken(lipgloss.Color("#ffffff"), 0.30)).
				Foreground(lipgloss.Darken(lipgloss.Color("#000000"), 0.15)),
		),
		style.Set(
			style.GenericHeadingSubtle,
			base.Background(lipgloss.Darken(lipgloss.Color("#ffffff"), 0.15)),
		),
		style.Set(
			style.TextAccentDim,
			base.Foreground(lipgloss.Darken(lipgloss.Color("#ff0000"), 0.15)),
		),
		style.Set(
			style.TextAccent,
			base.Foreground(lipgloss.Color("#ff0000")),
		),
		style.Set(
			style.TextSubtleDim,
			base.Foreground(lipgloss.Lighten(lipgloss.Color("#000000"), 0.15)),
		),
		style.Set(
			style.TextSubtle,
			base.Foreground(lipgloss.Color("#000000")),
		),
		style.Set(
			style.GenericHighlightDim,
			lipgloss.NewStyle().Background(lipgloss.Darken(lipgloss.Color("#ffffff"), 0.15)),
		),
		style.Set(
			style.GenericHighlight,
			lipgloss.NewStyle().Background(lipgloss.Darken(lipgloss.Color("#ffffff"), 0.30)),
		),
		style.Set(
			style.GenericHeadingOK,
			lipgloss.NewStyle().
				Foreground(lipgloss.Color("#ffffff")).
				Background(lipgloss.Color("#22863a")).
				Bold(true),
		),
		style.Set(
			style.GenericHeadingWarn,
			lipgloss.NewStyle().
				Foreground(lipgloss.Color("#ffffff")).
				Background(lipgloss.Color("#986801")).
				Bold(true),
		),
		style.Set(
			style.GenericHeadingError,
			lipgloss.NewStyle().
				Foreground(lipgloss.Color("#ffffff")).
				Background(lipgloss.Color("#cb2431")).
				Bold(true),
		),
		style.Set(
			style.TextOK,
			base.Foreground(lipgloss.Color("#22863a")),
		),
		style.Set(
			style.TextWarn,
			base.Foreground(lipgloss.Color("#986801")),
		),
		style.Set(
			style.TextError,
			base.Foreground(lipgloss.Color("#cb2431")),
		),
	)
}
