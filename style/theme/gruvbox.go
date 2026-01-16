package theme

import (
	"charm.land/lipgloss/v2"

	"github.com/macropower/niceyaml/style"
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
	)
}
