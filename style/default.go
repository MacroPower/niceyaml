package style

import (
	"image/color"
	"sync"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/exp/charmtone"
	"github.com/lucasb-eyer/go-colorful"

	"go.jacobcolvin.com/niceyaml/style/kind"
)

// The [Styles] that [Default] returns for each background. The first call
// for a background builds its value, and every later call returns the same
// one.
var (
	defaultDark  = sync.OnceValue(buildDefaultDark)
	defaultLight = sync.OnceValue(buildDefaultLight)
)

// Default returns the [Styles] for output without a theme, in the CharmTone
// palette for a dark or a light terminal. It does not set a surface, so the
// text shows the terminal's own background.
//
// The isDark argument names that background. Default(true) returns pale
// text for a dark terminal, and Default(false) returns dark text for a
// light one. Every color that Default(false) gives a YAML token keeps a
// contrast of at least 4.5:1 against white, in true color and on a
// terminal with 256 colors. This package does not read the terminal, so
// the caller detects the background and passes it in:
//
//	isDark := lipgloss.HasDarkBackground(os.Stdin, os.Stdout)
//	p := printer.New(printer.WithStyles(style.Default(isDark)))
//
// A Bubble Tea program reads the same answer from the IsDark method of a
// [charm.land/bubbletea/v2.BackgroundColorMsg]. A program that does not
// detect the background passes true. A printer without styles renders
// with that same dark palette.
//
// [go.jacobcolvin.com/niceyaml/style/theme.Charm] paints the dark palette
// on a Pepper surface, and
// [go.jacobcolvin.com/niceyaml/style/theme.CharmLight] paints the light
// palette on a Salt surface.
//
// Every call with the same argument returns the same shared value. Extend
// it with [Styles.With], which returns a copy.
func Default(isDark bool) Styles {
	if isDark {
		return defaultDark()
	}

	return defaultLight()
}

// buildDefaultDark builds the [Styles] that [Default] returns for a dark
// background.
func buildDefaultDark() Styles {
	base := lipgloss.NewStyle().Foreground(charmtone.Smoke)

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
		Set(kind.GenericWarn, base.Foreground(charmtone.Pepper).Background(charmtone.Cumin)),
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

// buildDefaultLight builds the [Styles] that [Default] returns for a light
// background. Each YAML token takes the hue that [buildDefaultDark] gives
// it. Where CharmTone does not hold that hue dark enough to read on white,
// the token takes a shade of a CharmTone color of that hue.
//
// The highlights take other hues. The dark palette fills the selected
// match with indigo, which hides the dark text of this palette. The
// selected match takes a yellow fill here.
func buildDefaultLight() Styles {
	base := lipgloss.NewStyle().Foreground(charmtone.Char)

	// A terminal with 256 colors draws each color as the nearest entry of
	// its table. A muted shade lands on a gray entry beside the comments or
	// on the entry of another hue. Each shade here lands on an entry of its
	// own hue that reads on white.
	//
	// The shades of Zest with a contrast between 4.5:1 and 8:1 on white
	// land on a green entry or on gray. The darker shades that reach the
	// olive entry are dull in true color and about as dark as the body
	// text. The olive is thus a shade of Citron, which has the hue of Zest
	// at full strength. Every shade of Cumin that reads on white lands on
	// gray, on a red beside the brick, or on the entry of the olive. The
	// brown is thus a shade of Tang. Prince lands on an entry too pale for
	// white, so the purple is a darker shade of Prince.
	//
	// The dimmed accent moves away from the background, as the dark
	// palette's does. It is thus a darker shade of the purple, and it
	// lands on an entry apart from the purple's.
	var (
		green     = lipgloss.Darken(charmtone.Guac, 0.4)
		brown     = lipgloss.Darken(charmtone.Tang, 0.5)
		brick     = lipgloss.Darken(charmtone.Bengal, 0.35)
		olive     = lipgloss.Darken(charmtone.Citron, 0.55)
		purple    = lipgloss.Darken(charmtone.Prince, 0.12)
		purpleDim = lipgloss.Darken(charmtone.Prince, 0.4)
	)

	return New(
		base,
		Set(kind.Comment, base.Foreground(charmtone.Oyster)),
		Set(kind.CommentPreproc, base.Foreground(charmtone.Char)),
		// The chrome takes the comment color alone, so a later Comment
		// override leaves the gutter as it was.
		Set(kind.UI, base.Foreground(charmtone.Oyster)),
		// A diff line fills its whole row, so each fill is a pale tint that
		// stays close to a light background.
		Set(kind.GenericDeleted, base.Foreground(charmtone.Pom).Background(tint(charmtone.Cherry, 0.2))),
		Set(kind.GenericInserted, base.Foreground(charmtone.Gator).Background(tint(charmtone.Guac, 0.3))),
		Set(kind.GenericError, base.Foreground(charmtone.Butter).Background(charmtone.Pom)),
		Set(kind.GenericWarn, base.Foreground(charmtone.Pepper).Background(charmtone.Cumin)),
		Set(kind.LiteralBoolean, base.Foreground(charmtone.Oceania)),
		Set(kind.LiteralNull, base.Foreground(charmtone.Oceania)),
		Set(kind.LiteralNumber, base.Foreground(green)),
		Set(kind.LiteralString, base.Foreground(brown)),
		Set(kind.NameAlias, base.Foreground(brick)),
		Set(kind.NameAnchor, base.Foreground(brick)),
		Set(kind.NameDecorator, base.Foreground(brick)),
		Set(kind.NameTag, base.Foreground(purple)),
		Set(kind.Punctuation, base.Foreground(olive)),
		Set(kind.PunctuationHeading, base.Foreground(charmtone.Char)),
		Set(kind.GenericHeading, base.Foreground(charmtone.Butter).Background(purple).Bold(true)),
		// The dim highlight and the two quiet headings are fills that a
		// reader must tell from the background. Smoke is the palest
		// CharmTone gray that shows after a blend into a Salt surface.
		// Steep is the palest that shows on Salt without a blend.
		Set(kind.GenericHeadingAccent, base.Background(charmtone.Smoke).Foreground(charmtone.Pepper)),
		Set(kind.GenericHeadingSubtle, base.Background(charmtone.Steep)),
		Set(kind.TextAccentDim, base.Foreground(purpleDim)),
		Set(kind.TextAccent, base.Foreground(purple)),
		Set(kind.TextSubtleDim, base.Foreground(charmtone.Squid)),
		Set(kind.TextSubtle, base.Foreground(charmtone.Oyster)),
		Set(kind.GenericHighlightDim, lipgloss.NewStyle().Background(charmtone.Smoke)),
		Set(kind.GenericHighlight, lipgloss.NewStyle().Background(charmtone.Mustard)),
		Set(
			kind.GenericHeadingOK,
			lipgloss.NewStyle().Foreground(charmtone.Pepper).Background(charmtone.Guac).Bold(true),
		),
		Set(
			kind.GenericHeadingWarn,
			lipgloss.NewStyle().Foreground(charmtone.Pepper).Background(charmtone.Cumin).Bold(true),
		),
		Set(
			kind.GenericHeadingError,
			lipgloss.NewStyle().Foreground(charmtone.Pepper).Background(charmtone.Cherry).Bold(true),
		),
		Set(kind.TextOK, base.Foreground(green)),
		Set(kind.TextWarn, base.Foreground(brown)),
		Set(kind.TextError, base.Foreground(charmtone.Pom)),
	)
}

// tint returns white with a share of c mixed in, from 0 for white to 1 for
// c. It mixes in the LAB color space, so the tint keeps the hue of c.
func tint(c color.Color, share float64) color.Color {
	cf, _ := colorful.MakeColor(c)
	white := colorful.Color{R: 1, G: 1, B: 1}

	return color.RGBAModel.Convert(white.BlendLab(cf, share).Clamped())
}
