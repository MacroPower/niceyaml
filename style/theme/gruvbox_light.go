package theme

import (
	"charm.land/lipgloss/v2"

	"jacobcolvin.com/niceyaml/style"
)

// GruvboxLight returns [style.Styles] using gruvbox-light colors.
func GruvboxLight() style.Styles {
	base := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#3c3836")).
		Background(lipgloss.Color("#fbf1c7"))

	return style.NewStyles(base,
		style.Set(
			style.Comment,
			base.Foreground(lipgloss.Color("#928374")).Italic(true),
		),
		style.Set(
			style.Generic,
			base.Foreground(lipgloss.Color("#3c3836")),
		),
		style.Set(
			style.GenericDeleted,
			base.Foreground(lipgloss.Color("#282828")).Background(lipgloss.Color("#9d0006")),
		),
		style.Set(
			style.GenericError,
			base.Background(lipgloss.Color("#9d0006")).Bold(true),
		),
		style.Set(
			style.GenericInserted,
			base.Foreground(lipgloss.Color("#282828")).Background(lipgloss.Color("#79740e")),
		),
		style.Set(
			style.LiteralNumber,
			base.Foreground(lipgloss.Color("#8f3f71")),
		),
		style.Set(
			style.LiteralNumberFloat,
			base.Foreground(lipgloss.Color("#8f3f71")),
		),
		style.Set(
			style.LiteralString,
			base.Foreground(lipgloss.Color("#79740e")),
		),
		style.Set(
			style.Name,
			base.Foreground(lipgloss.Color("#3c3836")),
		),
		style.Set(
			style.NameTag,
			base.Foreground(lipgloss.Color("#9d0006")),
		),
		style.Set(
			style.NameDecorator,
			base.Foreground(lipgloss.Color("#427b58")),
		),
		style.Set(
			style.Punctuation,
			base.Foreground(lipgloss.Color("#af3a03")),
		),
	)
}
