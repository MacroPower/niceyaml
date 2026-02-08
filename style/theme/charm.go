package theme

import (
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/exp/charmtone"

	"go.jacobcolvin.com/niceyaml/style"
)

// Charm returns [style.Styles] using CharmTone colors.
func Charm() style.Styles {
	base := lipgloss.NewStyle().
		Foreground(charmtone.Smoke).
		Background(charmtone.Pepper)

	return style.NewStyles(
		base,
		style.Set(style.Comment, base.Foreground(charmtone.Oyster)),
		style.Set(style.CommentPreproc, base.Foreground(charmtone.Smoke)),
		style.Set(style.GenericDeleted, base.Foreground(charmtone.Cherry).Background(charmtone.Toast)),
		style.Set(style.GenericInserted, base.Foreground(charmtone.Julep).Background(charmtone.Spinach)),
		style.Set(style.GenericError, base.Foreground(charmtone.Butter).Background(charmtone.Sriracha)),
		style.Set(style.LiteralBoolean, base.Foreground(charmtone.Malibu)),
		style.Set(style.LiteralNull, base.Foreground(charmtone.Malibu)),
		style.Set(style.LiteralNumber, base.Foreground(charmtone.Julep)),
		style.Set(style.LiteralString, base.Foreground(charmtone.Cumin)),
		style.Set(style.NameAlias, base.Foreground(charmtone.Bengal)),
		style.Set(style.NameAnchor, base.Foreground(charmtone.Bengal)),
		style.Set(style.NameTag, base.Foreground(charmtone.Mauve)),
		style.Set(style.Punctuation, base.Foreground(charmtone.Zest)),
		style.Set(style.PunctuationHeading, base.Foreground(charmtone.Smoke)),
		style.Set(style.Title, base.Foreground(charmtone.Pepper).Background(charmtone.Mauve).Bold(true)),
		style.Set(style.TitleAccent, base.Background(charmtone.Iron).Foreground(charmtone.Salt)),
		style.Set(style.TitleSubtle, base.Background(charmtone.Charcoal)),
		style.Set(style.TextAccent, base.Foreground(charmtone.Ash)),
		style.Set(style.TextAccentSelected, base.Foreground(charmtone.Smoke)),
		style.Set(style.TextSubtle, base.Foreground(charmtone.Iron)),
		style.Set(style.TextSubtleSelected, base.Foreground(charmtone.Smoke)),
		style.Set(style.Highlight, lipgloss.NewStyle().Background(charmtone.Iron)),
		style.Set(style.HighlightSelected, lipgloss.NewStyle().Background(charmtone.Smoke)),
		style.Set(
			style.TitleOK,
			lipgloss.NewStyle().Foreground(charmtone.Pepper).Background(charmtone.Julep).Bold(true),
		),
		style.Set(
			style.TitleWarn,
			lipgloss.NewStyle().Foreground(charmtone.Pepper).Background(charmtone.Cumin).Bold(true),
		),
		style.Set(
			style.TitleError,
			lipgloss.NewStyle().Foreground(charmtone.Pepper).Background(charmtone.Cherry).Bold(true),
		),
		style.Set(style.TextOK, base.Foreground(charmtone.Julep)),
		style.Set(style.TextWarn, base.Foreground(charmtone.Cumin)),
		style.Set(style.TextError, base.Foreground(charmtone.Cherry)),
	)
}
