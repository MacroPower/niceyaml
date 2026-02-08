package theme

import (
	"charm.land/lipgloss/v2"

	"go.jacobcolvin.com/niceyaml/style"
)

// HrHighContrast returns [style.Styles] using hr-high-contrast colors.
func HrHighContrast() style.Styles {
	base := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#d5d500")).
		Background(lipgloss.Color("#000000"))

	return style.NewStyles(base,
		style.Set(
			style.Comment,
			base.Foreground(lipgloss.Color("#5a8349")),
		),
		style.Set(
			style.LiteralNumber,
			base.Foreground(lipgloss.Color("#ffffff")),
		),
		style.Set(
			style.LiteralString,
			base.Foreground(lipgloss.Color("#a87662")),
		),
		style.Set(
			style.Name,
			base.Foreground(lipgloss.Color("#ffffff")),
		),
		style.Set(
			style.NameTag,
			base.Foreground(lipgloss.Color("#467faf")),
		),
		style.Set(
			style.LiteralBoolean,
			base.Foreground(lipgloss.Color("#467faf")),
		),
		style.Set(
			style.Punctuation,
			base.Foreground(lipgloss.Color("#e4e400")),
		),
		style.Set(
			style.Title,
			lipgloss.NewStyle().
				Foreground(lipgloss.Color("#000000")).
				Background(lipgloss.Color("#467faf")).
				Bold(true),
		),
		style.Set(
			style.TitleAccent,
			base.Background(lipgloss.Lighten(lipgloss.Color("#000000"), 0.30)).
				Foreground(lipgloss.Lighten(lipgloss.Color("#d5d500"), 0.15)),
		),
		style.Set(
			style.TitleSubtle,
			base.Background(lipgloss.Lighten(lipgloss.Color("#000000"), 0.15)),
		),
		style.Set(
			style.TextAccent,
			base.Foreground(lipgloss.Lighten(lipgloss.Color("#d5d500"), 0.15)),
		),
		style.Set(
			style.TextAccentSelected,
			base.Foreground(lipgloss.Color("#d5d500")),
		),
		style.Set(
			style.TextSubtle,
			base.Foreground(lipgloss.Darken(lipgloss.Color("#d5d500"), 0.15)),
		),
		style.Set(
			style.TextSubtleSelected,
			base.Foreground(lipgloss.Color("#d5d500")),
		),
		style.Set(
			style.Highlight,
			lipgloss.NewStyle().Background(lipgloss.Lighten(lipgloss.Color("#000000"), 0.15)),
		),
		style.Set(
			style.HighlightSelected,
			lipgloss.NewStyle().Background(lipgloss.Lighten(lipgloss.Color("#000000"), 0.30)),
		),
		style.Set(
			style.TitleOK,
			lipgloss.NewStyle().
				Foreground(lipgloss.Color("#000000")).
				Background(lipgloss.Color("#00ff00")).
				Bold(true),
		),
		style.Set(
			style.TitleWarn,
			lipgloss.NewStyle().
				Foreground(lipgloss.Color("#000000")).
				Background(lipgloss.Color("#e5c07b")).
				Bold(true),
		),
		style.Set(
			style.TitleError,
			lipgloss.NewStyle().
				Foreground(lipgloss.Color("#000000")).
				Background(lipgloss.Color("#ff0000")).
				Bold(true),
		),
		style.Set(
			style.TextOK,
			base.Foreground(lipgloss.Color("#00ff00")),
		),
		style.Set(
			style.TextWarn,
			base.Foreground(lipgloss.Color("#e5c07b")),
		),
		style.Set(
			style.TextError,
			base.Foreground(lipgloss.Color("#ff0000")),
		),
	)
}
