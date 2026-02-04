package theme

import (
	"charm.land/lipgloss/v2"

	"go.jacobcolvin.com/niceyaml/style"
)

// Github returns [style.Styles] using github colors.
func Github() style.Styles {
	base := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#1f2328")).
		Background(lipgloss.Color("#f7f7f7"))

	return style.NewStyles(base,
		style.Set(
			style.Comment,
			base.Foreground(lipgloss.Color("#57606a")),
		),
		style.Set(
			style.CommentPreproc,
			base.Foreground(lipgloss.Color("#57606a")),
		),
		style.Set(
			style.GenericDeleted,
			base.Foreground(lipgloss.Color("#82071e")).Background(lipgloss.Color("#ffebe9")),
		),
		style.Set(
			style.GenericInserted,
			base.Foreground(lipgloss.Color("#116329")).Background(lipgloss.Color("#dafbe1")),
		),
		style.Set(
			style.LiteralNumber,
			base.Foreground(lipgloss.Color("#0550ae")),
		),
		style.Set(
			style.LiteralString,
			base.Foreground(lipgloss.Color("#0a3069")),
		),
		style.Set(
			style.NameDecorator,
			base.Foreground(lipgloss.Color("#0550ae")),
		),
		style.Set(
			style.NameTag,
			base.Foreground(lipgloss.Color("#0550ae")),
		),
		style.Set(
			style.Punctuation,
			base.Foreground(lipgloss.Color("#1f2328")),
		),
		style.Set(
			style.Search,
			lipgloss.NewStyle().Background(lipgloss.Darken(lipgloss.Color("#f7f7f7"), 0.15)),
		),
		style.Set(
			style.SearchSelected,
			lipgloss.NewStyle().Background(lipgloss.Darken(lipgloss.Color("#f7f7f7"), 0.30)),
		),
	)
}
