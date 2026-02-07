package theme

import (
	"charm.land/lipgloss/v2"

	"go.jacobcolvin.com/niceyaml/style"
)

// DoomOne returns [style.Styles] using doom-one colors.
func DoomOne() style.Styles {
	base := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#b0c4de")).
		Background(lipgloss.Color("#282c34"))

	return style.NewStyles(base,
		style.Set(
			style.Comment,
			base.Foreground(lipgloss.Color("#8a93a5")).Italic(true),
		),
		style.Set(
			style.Generic,
			base.Foreground(lipgloss.Color("#b0c4de")),
		),
		style.Set(
			style.GenericInserted,
			base.Foreground(lipgloss.Color("#a6e22e")),
		),
		style.Set(
			style.LiteralNumber,
			base.Foreground(lipgloss.Color("#d19a66")),
		),
		style.Set(
			style.LiteralString,
			base.Foreground(lipgloss.Color("#98c379")),
		),
		style.Set(
			style.LiteralStringDouble,
			base.Foreground(lipgloss.Color("#63c381")),
		),
		style.Set(
			style.LiteralStringSingle,
			base.Foreground(lipgloss.Color("#98c379")),
		),
		style.Set(
			style.Name,
			base.Foreground(lipgloss.Color("#c1abea")),
		),
		style.Set(
			style.NameDecorator,
			base.Foreground(lipgloss.Color("#e5c07b")),
		),
		style.Set(
			style.NameTag,
			base.Foreground(lipgloss.Color("#e06c75")),
		),
		style.Set(
			style.Punctuation,
			base.Foreground(lipgloss.Color("#b0c4de")),
		),
		style.Set(
			style.GenericError,
			base.Foreground(lipgloss.Color("#b0c4de")),
		),
		style.Set(
			style.LiteralBoolean,
			base.Foreground(lipgloss.Color("#b756ff")).Bold(true),
		),
		style.Set(
			style.PunctuationHeading,
			base.Foreground(lipgloss.Color("#76a9f9")),
		),
		style.Set(
			style.Title,
			lipgloss.NewStyle().
				Foreground(lipgloss.Color("#282c34")).
				Background(lipgloss.Color("#e06c75")).
				Bold(true),
		),
		style.Set(
			style.TitleAccent,
			base.Background(lipgloss.Lighten(lipgloss.Color("#282c34"), 0.30)).
				Foreground(lipgloss.Lighten(lipgloss.Color("#b0c4de"), 0.15)),
		),
		style.Set(
			style.TitleSubtle,
			base.Background(lipgloss.Lighten(lipgloss.Color("#282c34"), 0.15)),
		),
		style.Set(
			style.TextAccent,
			base.Foreground(lipgloss.Lighten(lipgloss.Color("#b0c4de"), 0.15)),
		),
		style.Set(
			style.TextAccentSelected,
			base.Foreground(lipgloss.Color("#b0c4de")),
		),
		style.Set(
			style.TextSubtle,
			base.Foreground(lipgloss.Darken(lipgloss.Color("#b0c4de"), 0.15)),
		),
		style.Set(
			style.TextSubtleSelected,
			base.Foreground(lipgloss.Color("#b0c4de")),
		),
		style.Set(
			style.Highlight,
			lipgloss.NewStyle().Background(lipgloss.Lighten(lipgloss.Color("#282c34"), 0.15)),
		),
		style.Set(
			style.HighlightSelected,
			lipgloss.NewStyle().Background(lipgloss.Lighten(lipgloss.Color("#282c34"), 0.30)),
		),
	)
}
