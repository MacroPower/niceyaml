package theme

import (
	"charm.land/lipgloss/v2"

	"go.jacobcolvin.com/niceyaml/style"
)

// ModusVivendi returns [style.Styles] using modus-vivendi colors.
func ModusVivendi() style.Styles {
	base := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#ffffff")).
		Background(lipgloss.Color("#000000"))

	return style.NewStyles(base,
		style.Set(
			style.Comment,
			base.Foreground(lipgloss.Color("#a8a8a8")),
		),
		style.Set(
			style.LiteralString,
			base.Foreground(lipgloss.Color("#79a8ff")),
		),
		style.Set(
			style.NameTag,
			base.Foreground(lipgloss.Color("#b6a0ff")),
		),
		style.Set(
			style.LiteralBoolean,
			base.Foreground(lipgloss.Color("#00bcff")),
		),
		style.Set(
			style.Name,
			base.Foreground(lipgloss.Color("#f78fe7")),
		),
		style.Set(
			style.Punctuation,
			base.Foreground(lipgloss.Color("#00d3d0")),
		),
		style.Set(
			style.GenericHeading,
			lipgloss.NewStyle().
				Foreground(lipgloss.Color("#000000")).
				Background(lipgloss.Color("#b6a0ff")).
				Bold(true),
		),
		style.Set(
			style.GenericHeadingAccent,
			base.Background(lipgloss.Lighten(lipgloss.Color("#000000"), 0.30)).
				Foreground(lipgloss.Lighten(lipgloss.Color("#ffffff"), 0.15)),
		),
		style.Set(
			style.GenericHeadingSubtle,
			base.Background(lipgloss.Lighten(lipgloss.Color("#000000"), 0.15)),
		),
		style.Set(
			style.TextAccentDim,
			base.Foreground(lipgloss.Lighten(lipgloss.Color("#b6a0ff"), 0.15)),
		),
		style.Set(
			style.TextAccent,
			base.Foreground(lipgloss.Color("#b6a0ff")),
		),
		style.Set(
			style.TextSubtleDim,
			base.Foreground(lipgloss.Darken(lipgloss.Color("#ffffff"), 0.15)),
		),
		style.Set(
			style.TextSubtle,
			base.Foreground(lipgloss.Color("#ffffff")),
		),
		style.Set(
			style.GenericHighlightDim,
			lipgloss.NewStyle().Background(lipgloss.Lighten(lipgloss.Color("#000000"), 0.15)),
		),
		style.Set(
			style.GenericHighlight,
			lipgloss.NewStyle().Background(lipgloss.Lighten(lipgloss.Color("#000000"), 0.30)),
		),
		style.Set(
			style.GenericHeadingOK,
			lipgloss.NewStyle().
				Foreground(lipgloss.Color("#000000")).
				Background(lipgloss.Color("#44bc44")).
				Bold(true),
		),
		style.Set(
			style.GenericHeadingWarn,
			lipgloss.NewStyle().
				Foreground(lipgloss.Color("#000000")).
				Background(lipgloss.Color("#d0bc00")).
				Bold(true),
		),
		style.Set(
			style.GenericHeadingError,
			lipgloss.NewStyle().
				Foreground(lipgloss.Color("#000000")).
				Background(lipgloss.Color("#ff5f59")).
				Bold(true),
		),
		style.Set(
			style.TextOK,
			base.Foreground(lipgloss.Color("#44bc44")),
		),
		style.Set(
			style.TextWarn,
			base.Foreground(lipgloss.Color("#d0bc00")),
		),
		style.Set(
			style.TextError,
			base.Foreground(lipgloss.Color("#ff5f59")),
		),
	)
}
