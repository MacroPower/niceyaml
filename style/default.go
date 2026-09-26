package style

import (
	"sync"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/exp/charmtone"

	"go.jacobcolvin.com/niceyaml/style/kind"
)

// defaultStyles builds the [Styles] that [Default] returns on its first
// call and returns the same value to every later call.
var defaultStyles = sync.OnceValue(buildDefault)

// Default returns the [Styles] niceyaml renders with unless a caller picks a
// theme. It uses the CharmTone palette, and the theme catalog exposes the
// same palette as [go.jacobcolvin.com/niceyaml/style/theme.Charm].
//
// Every call returns the same shared value. Extend it with [Styles.With],
// which returns a copy.
func Default() Styles {
	return defaultStyles()
}

// buildDefault builds the [Styles] that [Default] returns.
func buildDefault() Styles {
	base := lipgloss.NewStyle().
		Foreground(charmtone.Smoke).
		Background(charmtone.Pepper)

	return New(
		base,
		Set(kind.Comment, base.Foreground(charmtone.Oyster)),
		Set(kind.CommentPreproc, base.Foreground(charmtone.Smoke)),
		// The chrome takes the comment color alone, so a later Comment
		// override leaves the gutter as it was.
		Set(kind.UI, base.Foreground(charmtone.Oyster)),
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
		Set(kind.GenericHighlight, lipgloss.NewStyle().Background(charmtone.Ox)),
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
