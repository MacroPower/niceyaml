package theme

import (
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/exp/charmtone"

	"github.com/macropower/niceyaml/style"
)

// Charm returns [style.Styles] using CharmTone colors.
func Charm() style.Styles {
	base := lipgloss.NewStyle().
		Foreground(charmtone.Smoke).
		Background(charmtone.Pepper)

	return style.NewStyles(base,
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
	)
}
