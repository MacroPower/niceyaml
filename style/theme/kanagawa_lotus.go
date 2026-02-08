package theme

import (
	"charm.land/lipgloss/v2"

	"go.jacobcolvin.com/niceyaml/style"
)

// KanagawaLotus returns [style.Styles] using kanagawa-lotus colors.
func KanagawaLotus() style.Styles {
	base := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#545464")).
		Background(lipgloss.Color("#f2ecbc"))

	return style.NewStyles(base,
		style.Set(
			style.Comment,
			base.Foreground(lipgloss.Color("#8a8980")).Italic(true),
		),
		style.Set(
			style.CommentPreproc,
			base.Foreground(lipgloss.Color("#c84053")).Italic(true),
		),
		style.Set(
			style.GenericDeleted,
			base.Foreground(lipgloss.Color("#d7474b")).Background(lipgloss.Color("#d9a594")),
		),
		style.Set(
			style.GenericError,
			base.Foreground(lipgloss.Color("#e82424")),
		),
		style.Set(
			style.GenericInserted,
			base.Foreground(lipgloss.Color("#6e915f")).Background(lipgloss.Color("#b7d0ae")),
		),
		style.Set(
			style.LiteralNumber,
			base.Foreground(lipgloss.Color("#b35b79")),
		),
		style.Set(
			style.LiteralString,
			base.Foreground(lipgloss.Color("#6f894e")),
		),
		style.Set(
			style.NameDecorator,
			base.Foreground(lipgloss.Color("#cc6d00")).Bold(true),
		),
		style.Set(
			style.NameTag,
			base.Foreground(lipgloss.Color("#4d699b")),
		),
		style.Set(
			style.Punctuation,
			base.Foreground(lipgloss.Color("#545464")),
		),
		style.Set(
			style.Title,
			lipgloss.NewStyle().
				Foreground(lipgloss.Color("#f2ecbc")).
				Background(lipgloss.Color("#4d699b")).
				Bold(true),
		),
		style.Set(
			style.TitleAccent,
			base.Background(lipgloss.Darken(lipgloss.Color("#f2ecbc"), 0.30)).
				Foreground(lipgloss.Darken(lipgloss.Color("#545464"), 0.15)),
		),
		style.Set(
			style.TitleSubtle,
			base.Background(lipgloss.Darken(lipgloss.Color("#f2ecbc"), 0.15)),
		),
		style.Set(
			style.TextAccent,
			base.Foreground(lipgloss.Darken(lipgloss.Color("#4d699b"), 0.15)),
		),
		style.Set(
			style.TextAccentSelected,
			base.Foreground(lipgloss.Color("#4d699b")),
		),
		style.Set(
			style.TextSubtle,
			base.Foreground(lipgloss.Lighten(lipgloss.Color("#545464"), 0.15)),
		),
		style.Set(
			style.TextSubtleSelected,
			base.Foreground(lipgloss.Color("#545464")),
		),
		style.Set(
			style.Highlight,
			lipgloss.NewStyle().Background(lipgloss.Darken(lipgloss.Color("#f2ecbc"), 0.15)),
		),
		style.Set(
			style.HighlightSelected,
			lipgloss.NewStyle().Background(lipgloss.Darken(lipgloss.Color("#f2ecbc"), 0.30)),
		),
		style.Set(
			style.TitleOK,
			lipgloss.NewStyle().
				Foreground(lipgloss.Color("#f2ecbc")).
				Background(lipgloss.Color("#6e915f")).
				Bold(true),
		),
		style.Set(
			style.TitleWarn,
			lipgloss.NewStyle().
				Foreground(lipgloss.Color("#f2ecbc")).
				Background(lipgloss.Color("#cc6d00")).
				Bold(true),
		),
		style.Set(
			style.TitleError,
			lipgloss.NewStyle().
				Foreground(lipgloss.Color("#f2ecbc")).
				Background(lipgloss.Color("#d7474b")).
				Bold(true),
		),
		style.Set(
			style.TextOK,
			base.Foreground(lipgloss.Color("#6e915f")),
		),
		style.Set(
			style.TextWarn,
			base.Foreground(lipgloss.Color("#cc6d00")),
		),
		style.Set(
			style.TextError,
			base.Foreground(lipgloss.Color("#d7474b")),
		),
	)
}
