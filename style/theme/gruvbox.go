package theme

import (
	"charm.land/lipgloss/v2"

	"go.jacobcolvin.com/niceyaml/style"
)

// Gruvbox returns [style.Styles] using gruvbox colors.
func Gruvbox() style.Styles {
	base := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#ebdbb2")).
		Background(lipgloss.Color("#282828"))

	return style.NewStyles(base,
		style.Set(
			style.Comment,
			base.Foreground(lipgloss.Color("#928374")).Italic(true),
		),
		style.Set(
			style.Generic,
			base.Foreground(lipgloss.Color("#ebdbb2")),
		),
		style.Set(
			style.GenericDeleted,
			base.Foreground(lipgloss.Color("#282828")).Background(lipgloss.Color("#fb4934")),
		),
		style.Set(
			style.GenericError,
			base.Background(lipgloss.Color("#fb4934")).Bold(true),
		),
		style.Set(
			style.GenericInserted,
			base.Foreground(lipgloss.Color("#282828")).Background(lipgloss.Color("#b8bb26")),
		),
		style.Set(
			style.LiteralNumber,
			base.Foreground(lipgloss.Color("#d3869b")),
		),
		style.Set(
			style.LiteralNumberFloat,
			base.Foreground(lipgloss.Color("#d3869b")),
		),
		style.Set(
			style.LiteralString,
			base.Foreground(lipgloss.Color("#b8bb26")),
		),
		style.Set(
			style.Name,
			base.Foreground(lipgloss.Color("#ebdbb2")),
		),
		style.Set(
			style.NameTag,
			base.Foreground(lipgloss.Color("#fb4934")),
		),
		style.Set(
			style.NameDecorator,
			base.Foreground(lipgloss.Color("#8ec07c")),
		),
		style.Set(
			style.Punctuation,
			base.Foreground(lipgloss.Color("#fe8019")),
		),
		style.Set(
			style.Title,
			lipgloss.NewStyle().
				Foreground(lipgloss.Color("#282828")).
				Background(lipgloss.Color("#fb4934")).
				Bold(true),
		),
		style.Set(
			style.TitleAccent,
			base.Background(lipgloss.Lighten(lipgloss.Color("#282828"), 0.30)).
				Foreground(lipgloss.Lighten(lipgloss.Color("#ebdbb2"), 0.15)),
		),
		style.Set(
			style.TitleSubtle,
			base.Background(lipgloss.Lighten(lipgloss.Color("#282828"), 0.15)),
		),
		style.Set(
			style.TextAccent,
			base.Foreground(lipgloss.Lighten(lipgloss.Color("#ebdbb2"), 0.15)),
		),
		style.Set(
			style.TextAccentSelected,
			base.Foreground(lipgloss.Color("#ebdbb2")),
		),
		style.Set(
			style.TextSubtle,
			base.Foreground(lipgloss.Darken(lipgloss.Color("#ebdbb2"), 0.15)),
		),
		style.Set(
			style.TextSubtleSelected,
			base.Foreground(lipgloss.Color("#ebdbb2")),
		),
		style.Set(
			style.Highlight,
			lipgloss.NewStyle().Background(lipgloss.Lighten(lipgloss.Color("#282828"), 0.15)),
		),
		style.Set(
			style.HighlightSelected,
			lipgloss.NewStyle().Background(lipgloss.Lighten(lipgloss.Color("#282828"), 0.30)),
		),
		style.Set(
			style.TitleOK,
			lipgloss.NewStyle().
				Foreground(lipgloss.Color("#282828")).
				Background(lipgloss.Color("#b8bb26")).
				Bold(true),
		),
		style.Set(
			style.TitleWarn,
			lipgloss.NewStyle().
				Foreground(lipgloss.Color("#282828")).
				Background(lipgloss.Color("#fabd2f")).
				Bold(true),
		),
		style.Set(
			style.TitleError,
			lipgloss.NewStyle().
				Foreground(lipgloss.Color("#282828")).
				Background(lipgloss.Color("#fb4934")).
				Bold(true),
		),
		style.Set(
			style.TextOK,
			base.Foreground(lipgloss.Color("#b8bb26")),
		),
		style.Set(
			style.TextWarn,
			base.Foreground(lipgloss.Color("#fabd2f")),
		),
		style.Set(
			style.TextError,
			base.Foreground(lipgloss.Color("#fb4934")),
		),
	)
}
