package style

import (
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/exp/charmtone"
)

// Default returns the [Styles] niceyaml renders with when no theme is chosen.
// It uses the CharmTone palette, and the theme catalog exposes the same
// palette as [go.jacobcolvin.com/niceyaml/style/theme.Charm].
func Default() Styles {
	base := lipgloss.NewStyle().
		Foreground(charmtone.Smoke).
		Background(charmtone.Pepper)

	return NewStyles(
		base,
		Set(Comment, base.Foreground(charmtone.Oyster)),
		Set(CommentPreproc, base.Foreground(charmtone.Smoke)),
		Set(GenericDeleted, base.Foreground(charmtone.Cherry).Background(charmtone.Toast)),
		Set(GenericInserted, base.Foreground(charmtone.Julep).Background(charmtone.Spinach)),
		Set(GenericError, base.Foreground(charmtone.Butter).Background(charmtone.Sriracha)),
		Set(LiteralBoolean, base.Foreground(charmtone.Malibu)),
		Set(LiteralNull, base.Foreground(charmtone.Malibu)),
		Set(LiteralNumber, base.Foreground(charmtone.Julep)),
		Set(LiteralString, base.Foreground(charmtone.Cumin)),
		Set(NameAlias, base.Foreground(charmtone.Bengal)),
		Set(NameAnchor, base.Foreground(charmtone.Bengal)),
		Set(NameTag, base.Foreground(charmtone.Mauve)),
		Set(Punctuation, base.Foreground(charmtone.Zest)),
		Set(PunctuationHeading, base.Foreground(charmtone.Smoke)),
		Set(GenericHeading, base.Foreground(charmtone.Pepper).Background(charmtone.Mauve).Bold(true)),
		Set(GenericHeadingAccent, base.Background(charmtone.Iron).Foreground(charmtone.Salt)),
		Set(GenericHeadingSubtle, base.Background(charmtone.Charcoal)),
		Set(TextAccentDim, base.Foreground(lipgloss.Lighten(charmtone.Mauve, 0.15))),
		Set(TextAccent, base.Foreground(charmtone.Mauve)),
		Set(TextSubtleDim, base.Foreground(charmtone.Iron)),
		Set(TextSubtle, base.Foreground(charmtone.Oyster)),
		Set(GenericHighlightDim, lipgloss.NewStyle().Background(charmtone.Iron)),
		Set(GenericHighlight, lipgloss.NewStyle().Background(charmtone.Smoke)),
		Set(
			GenericHeadingOK,
			lipgloss.NewStyle().Foreground(charmtone.Pepper).Background(charmtone.Julep).Bold(true),
		),
		Set(
			GenericHeadingWarn,
			lipgloss.NewStyle().Foreground(charmtone.Pepper).Background(charmtone.Cumin).Bold(true),
		),
		Set(
			GenericHeadingError,
			lipgloss.NewStyle().Foreground(charmtone.Pepper).Background(charmtone.Cherry).Bold(true),
		),
		Set(TextOK, base.Foreground(charmtone.Julep)),
		Set(TextWarn, base.Foreground(charmtone.Cumin)),
		Set(TextError, base.Foreground(charmtone.Cherry)),
	)
}
