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
		style.Set(style.GenericHeading, base.Foreground(charmtone.Pepper).Background(charmtone.Mauve).Bold(true)),
		style.Set(style.GenericHeadingAccent, base.Background(charmtone.Iron).Foreground(charmtone.Salt)),
		style.Set(style.GenericHeadingSubtle, base.Background(charmtone.Charcoal)),
		style.Set(style.TextAccentDim, base.Foreground(lipgloss.Lighten(charmtone.Mauve, 0.15))),
		style.Set(style.TextAccent, base.Foreground(charmtone.Mauve)),
		style.Set(style.TextSubtleDim, base.Foreground(charmtone.Iron)),
		style.Set(style.TextSubtle, base.Foreground(charmtone.Oyster)),
		style.Set(style.GenericHighlightDim, lipgloss.NewStyle().Background(charmtone.Iron)),
		style.Set(style.GenericHighlight, lipgloss.NewStyle().Background(charmtone.Smoke)),
		style.Set(
			style.GenericHeadingOK,
			lipgloss.NewStyle().Foreground(charmtone.Pepper).Background(charmtone.Julep).Bold(true),
		),
		style.Set(
			style.GenericHeadingWarn,
			lipgloss.NewStyle().Foreground(charmtone.Pepper).Background(charmtone.Cumin).Bold(true),
		),
		style.Set(
			style.GenericHeadingError,
			lipgloss.NewStyle().Foreground(charmtone.Pepper).Background(charmtone.Cherry).Bold(true),
		),
		style.Set(style.TextOK, base.Foreground(charmtone.Julep)),
		style.Set(style.TextWarn, base.Foreground(charmtone.Cumin)),
		style.Set(style.TextError, base.Foreground(charmtone.Cherry)),
	)
}
