package theme

import (
	"charm.land/lipgloss/v2"

	"go.jacobcolvin.com/niceyaml/style"
)

// RosePine returns [style.Styles] using rose-pine colors.
func RosePine() style.Styles {
	base := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#e0def4")).
		Background(lipgloss.Color("#191724"))

	return style.NewStyles(base,
		style.Set(
			style.Comment,
			base.Foreground(lipgloss.Color("#6e6a86")),
		),
		style.Set(
			style.GenericDeleted,
			base.Foreground(lipgloss.Color("#eb6f92")),
		),
		style.Set(
			style.GenericInserted,
			base.Foreground(lipgloss.Color("#9ccfd8")),
		),
		style.Set(
			style.LiteralNumber,
			base.Foreground(lipgloss.Color("#f6c177")),
		),
		style.Set(
			style.LiteralString,
			base.Foreground(lipgloss.Color("#f6c177")),
		),
		style.Set(
			style.Name,
			base.Foreground(lipgloss.Color("#ebbcba")),
		),
		style.Set(
			style.NameDecorator,
			base.Foreground(lipgloss.Color("#908caa")),
		),
		style.Set(
			style.NameTag,
			base.Foreground(lipgloss.Color("#ebbcba")),
		),
		style.Set(
			style.Punctuation,
			base.Foreground(lipgloss.Color("#908caa")),
		),
		style.Set(
			style.GenericError,
			base.Foreground(lipgloss.Color("#eb6f92")),
		),
		style.Set(
			style.Generic,
			base.Italic(true),
		),
		style.Set(
			style.Title,
			lipgloss.NewStyle().
				Foreground(lipgloss.Color("#191724")).
				Background(lipgloss.Color("#ebbcba")).
				Bold(true),
		),
		style.Set(
			style.TitleAccent,
			base.Background(lipgloss.Lighten(lipgloss.Color("#191724"), 0.30)).
				Foreground(lipgloss.Lighten(lipgloss.Color("#e0def4"), 0.15)),
		),
		style.Set(
			style.TitleSubtle,
			base.Background(lipgloss.Lighten(lipgloss.Color("#191724"), 0.15)),
		),
		style.Set(
			style.TextAccentDim,
			base.Foreground(lipgloss.Lighten(lipgloss.Color("#ebbcba"), 0.15)),
		),
		style.Set(
			style.TextAccent,
			base.Foreground(lipgloss.Color("#ebbcba")),
		),
		style.Set(
			style.TextSubtleDim,
			base.Foreground(lipgloss.Darken(lipgloss.Color("#e0def4"), 0.15)),
		),
		style.Set(
			style.TextSubtle,
			base.Foreground(lipgloss.Color("#e0def4")),
		),
		style.Set(
			style.HighlightDim,
			lipgloss.NewStyle().Background(lipgloss.Lighten(lipgloss.Color("#191724"), 0.15)),
		),
		style.Set(
			style.Highlight,
			lipgloss.NewStyle().Background(lipgloss.Lighten(lipgloss.Color("#191724"), 0.30)),
		),
		style.Set(
			style.TitleOK,
			lipgloss.NewStyle().
				Foreground(lipgloss.Color("#191724")).
				Background(lipgloss.Color("#9ccfd8")).
				Bold(true),
		),
		style.Set(
			style.TitleWarn,
			lipgloss.NewStyle().
				Foreground(lipgloss.Color("#191724")).
				Background(lipgloss.Color("#f6c177")).
				Bold(true),
		),
		style.Set(
			style.TitleError,
			lipgloss.NewStyle().
				Foreground(lipgloss.Color("#191724")).
				Background(lipgloss.Color("#eb6f92")).
				Bold(true),
		),
		style.Set(
			style.TextOK,
			base.Foreground(lipgloss.Color("#9ccfd8")),
		),
		style.Set(
			style.TextWarn,
			base.Foreground(lipgloss.Color("#f6c177")),
		),
		style.Set(
			style.TextError,
			base.Foreground(lipgloss.Color("#eb6f92")),
		),
	)
}
