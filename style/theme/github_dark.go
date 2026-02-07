package theme

import (
	"charm.land/lipgloss/v2"

	"go.jacobcolvin.com/niceyaml/style"
)

// GithubDark returns [style.Styles] using github-dark colors.
func GithubDark() style.Styles {
	base := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#e6edf3")).
		Background(lipgloss.Color("#0d1117"))

	return style.NewStyles(base,
		style.Set(
			style.Comment,
			base.Foreground(lipgloss.Color("#8b949e")).Italic(true),
		),
		style.Set(
			style.Generic,
			base.Foreground(lipgloss.Color("#e6edf3")),
		),
		style.Set(
			style.GenericDeleted,
			base.Foreground(lipgloss.Color("#ffa198")).Background(lipgloss.Color("#490202")),
		),
		style.Set(
			style.GenericError,
			base.Foreground(lipgloss.Color("#ffa198")),
		),
		style.Set(
			style.GenericInserted,
			base.Foreground(lipgloss.Color("#56d364")).Background(lipgloss.Color("#0f5323")),
		),
		style.Set(
			style.Name,
			base.Foreground(lipgloss.Color("#e6edf3")),
		),
		style.Set(
			style.NameDecorator,
			base.Foreground(lipgloss.Color("#d2a8ff")).Bold(true),
		),
		style.Set(
			style.NameTag,
			base.Foreground(lipgloss.Color("#7ee787")),
		),
		style.Set(
			style.LiteralBoolean,
			base.Foreground(lipgloss.Color("#79c0ff")),
		),
		style.Set(
			style.LiteralString,
			base.Foreground(lipgloss.Color("#a5d6ff")),
		),
		style.Set(
			style.PunctuationHeading,
			base.Foreground(lipgloss.Color("#ff7b72")),
		),
		style.Set(
			style.Punctuation,
			base.Foreground(lipgloss.Color("#ff7b72")).Bold(true),
		),
		style.Set(
			style.Title,
			lipgloss.NewStyle().
				Foreground(lipgloss.Color("#0d1117")).
				Background(lipgloss.Color("#7ee787")).
				Bold(true),
		),
		style.Set(
			style.TitleAccent,
			base.Background(lipgloss.Lighten(lipgloss.Color("#0d1117"), 0.30)).
				Foreground(lipgloss.Lighten(lipgloss.Color("#e6edf3"), 0.15)),
		),
		style.Set(
			style.TitleSubtle,
			base.Background(lipgloss.Lighten(lipgloss.Color("#0d1117"), 0.15)),
		),
		style.Set(
			style.TextAccent,
			base.Foreground(lipgloss.Lighten(lipgloss.Color("#e6edf3"), 0.15)),
		),
		style.Set(
			style.TextAccentSelected,
			base.Foreground(lipgloss.Color("#e6edf3")),
		),
		style.Set(
			style.TextSubtle,
			base.Foreground(lipgloss.Darken(lipgloss.Color("#e6edf3"), 0.15)),
		),
		style.Set(
			style.TextSubtleSelected,
			base.Foreground(lipgloss.Color("#e6edf3")),
		),
		style.Set(
			style.Highlight,
			lipgloss.NewStyle().Background(lipgloss.Lighten(lipgloss.Color("#0d1117"), 0.15)),
		),
		style.Set(
			style.HighlightSelected,
			lipgloss.NewStyle().Background(lipgloss.Lighten(lipgloss.Color("#0d1117"), 0.30)),
		),
	)
}
