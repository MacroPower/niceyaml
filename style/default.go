package style

import (
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/exp/charmtone"

	"go.jacobcolvin.com/niceyaml/style/kind"
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
		Set(kind.Comment, base.Foreground(charmtone.Oyster)),
		Set(kind.CommentPreproc, base.Foreground(charmtone.Smoke)),
		Set(kind.GenericDeleted, base.Foreground(charmtone.Cherry).Background(charmtone.Toast)),
		Set(kind.GenericInserted, base.Foreground(charmtone.Julep).Background(charmtone.Spinach)),
		Set(kind.GenericError, base.Foreground(charmtone.Butter).Background(charmtone.Sriracha)),
		Set(kind.LiteralBoolean, base.Foreground(charmtone.Malibu)),
		Set(kind.LiteralNull, base.Foreground(charmtone.Malibu)),
		Set(kind.LiteralNumber, base.Foreground(charmtone.Julep)),
		Set(kind.LiteralString, base.Foreground(charmtone.Cumin)),
		Set(kind.NameAlias, base.Foreground(charmtone.Bengal)),
		Set(kind.NameAnchor, base.Foreground(charmtone.Bengal)),
		Set(kind.NameDecorator, base.Foreground(charmtone.Bengal)),
		Set(kind.NameTag, base.Foreground(charmtone.Mauve)),
		Set(kind.Punctuation, base.Foreground(charmtone.Zest)),
		Set(kind.PunctuationHeading, base.Foreground(charmtone.Smoke)),
		Set(kind.GenericHeading, base.Foreground(charmtone.Pepper).Background(charmtone.Mauve).Bold(true)),
		Set(kind.GenericHeadingAccent, base.Background(charmtone.Iron).Foreground(charmtone.Salt)),
		Set(kind.GenericHeadingSubtle, base.Background(charmtone.Charcoal)),
		Set(kind.TextAccentDim, base.Foreground(lipgloss.Lighten(charmtone.Mauve, 0.15))),
		Set(kind.TextAccent, base.Foreground(charmtone.Mauve)),
		Set(kind.TextSubtleDim, base.Foreground(charmtone.Iron)),
		Set(kind.TextSubtle, base.Foreground(charmtone.Oyster)),
		Set(kind.GenericHighlightDim, lipgloss.NewStyle().Background(charmtone.Iron)),
		Set(kind.GenericHighlight, lipgloss.NewStyle().Background(charmtone.Smoke)),
		Set(
			kind.GenericHeadingOK,
			lipgloss.NewStyle().Foreground(charmtone.Pepper).Background(charmtone.Julep).Bold(true),
		),
		Set(
			kind.GenericHeadingWarn,
			lipgloss.NewStyle().Foreground(charmtone.Pepper).Background(charmtone.Cumin).Bold(true),
		),
		Set(
			kind.GenericHeadingError,
			lipgloss.NewStyle().Foreground(charmtone.Pepper).Background(charmtone.Cherry).Bold(true),
		),
		Set(kind.TextOK, base.Foreground(charmtone.Julep)),
		Set(kind.TextWarn, base.Foreground(charmtone.Cumin)),
		Set(kind.TextError, base.Foreground(charmtone.Cherry)),
	)
}
