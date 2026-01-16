package theme

import (
	"charm.land/lipgloss/v2"

	"github.com/macropower/niceyaml/style"
)

// Evergarden returns [style.Styles] using evergarden colors.
func Evergarden() style.Styles {
	base := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#D6CBB4")).
		Background(lipgloss.Color("#252B2E"))

	return style.NewStyles(base,
		style.Set(
			style.Comment,
			base.Foreground(lipgloss.Color("#859289")).Italic(true),
		),
		style.Set(
			style.Generic,
			base.Foreground(lipgloss.Color("#D6CBB4")),
		),
		style.Set(
			style.GenericDeleted,
			base.Foreground(lipgloss.Color("#252B2E")).Background(lipgloss.Color("#E67E80")),
		),
		style.Set(
			style.GenericError,
			base.Background(lipgloss.Color("#E67E80")).Bold(true),
		),
		style.Set(
			style.GenericInserted,
			base.Foreground(lipgloss.Color("#252B2E")).Background(lipgloss.Color("#B2C98F")),
		),
		style.Set(
			style.LiteralNumber,
			base.Foreground(lipgloss.Color("#D699B6")),
		),
		style.Set(
			style.LiteralNumberFloat,
			base.Foreground(lipgloss.Color("#D699B6")),
		),
		style.Set(
			style.LiteralString,
			base.Foreground(lipgloss.Color("#B2C98F")),
		),
		style.Set(
			style.Name,
			base.Foreground(lipgloss.Color("#D6CBB4")),
		),
		style.Set(
			style.NameTag,
			base.Foreground(lipgloss.Color("#7a8478")),
		),
		style.Set(
			style.NameDecorator,
			base.Foreground(lipgloss.Color("#E67E80")),
		),
		style.Set(
			style.Punctuation,
			base.Foreground(lipgloss.Color("#7a8478")),
		),
	)
}
