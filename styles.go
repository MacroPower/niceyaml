package niceyaml

import (
	"image/color"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/exp/charmtone"
	"github.com/lucasb-eyer/go-colorful"
)

// Style identifies a style category for YAML highlighting.
type Style int

// Style constants for YAML highlighting.
const (
	StyleDefault      Style = iota // Default/fallback style.
	StyleKey                       // Mapping keys.
	StyleString                    // String values.
	StyleNumber                    // Numeric values.
	StyleBool                      // Boolean values.
	StyleNull                      // E.g.: ~, null.
	StyleAnchor                    // E.g.: &.
	StyleAlias                     // E.g.: *, <<.
	StyleComment                   // Comments.
	StyleError                     // Error highlighting.
	StyleTag                       // E.g.: !tag.
	StyleDocument                  // E.g.: ---.
	StyleDirective                 // E.g.: %YAML, %TAG.
	StylePunctuation               // E.g.: -, :, ?, [, ], {, }.
	StyleBlockScalar               // E.g.: |, >.
	StyleDiffInserted              // Lines inserted in diff (+).
	StyleDiffDeleted               // Lines deleted in diff (-).
)

// Styles defines styles for YAML highlighting.
type Styles map[Style]lipgloss.Style

// StyleGetter retrieves styles by category.
type StyleGetter interface {
	GetStyle(s Style) *lipgloss.Style
}

// TokenStyler styles [token.Tokens].
type TokenStyler interface {
	StyleGetter
	AddStyleToRange(style *lipgloss.Style, r PositionRange)
	ClearStyles()
}

// GetStyle returns the [lipgloss.Style] for the given [Style] category.
// If the given [Style] is not defined, it returns [StyleDefault].
// If no [StyleDefault] is defined, it returns an empty [lipgloss.Style].
func (s Styles) GetStyle(style Style) *lipgloss.Style {
	matchedStyle, ok := s[style]
	if ok {
		return &matchedStyle
	}

	defaultStyle, ok := s[StyleDefault]
	if ok {
		return &defaultStyle
	}

	return &lipgloss.Style{}
}

// DefaultStyles returns [Styles] using CharmTone colors.
func DefaultStyles() Styles {
	base := lipgloss.NewStyle().
		Foreground(charmtone.Smoke).
		Background(charmtone.Pepper)

	return Styles{
		StyleDefault:      base,
		StyleKey:          base.Foreground(charmtone.Mauve),  // NameTag.
		StyleString:       base.Foreground(charmtone.Cumin),  // LiteralString.
		StyleNumber:       base.Foreground(charmtone.Julep),  // LiteralNumber.
		StyleBool:         base.Foreground(charmtone.Malibu), // KeywordConstant.
		StyleNull:         base.Foreground(charmtone.Malibu), // KeywordConstant.
		StyleAnchor:       base.Foreground(charmtone.Bengal), // CommentPreproc.
		StyleAlias:        base.Foreground(charmtone.Bengal), // CommentPreproc.
		StyleComment:      base.Foreground(charmtone.Oyster), // Comment.
		StyleTag:          base.Foreground(charmtone.Bengal), // CommentPreproc.
		StyleDocument:     base.Foreground(charmtone.Smoke),  // NameNamespace.
		StyleDirective:    base.Foreground(charmtone.Smoke),  // NameNamespace.
		StylePunctuation:  base.Foreground(charmtone.Zest),   // Punctuation.
		StyleBlockScalar:  base.Foreground(charmtone.Zest),   // Punctuation.
		StyleError:        base.Foreground(charmtone.Butter).Background(charmtone.Sriracha),
		StyleDiffInserted: base.Foreground(charmtone.Julep).Background(charmtone.Spinach), // GenericInserted.
		StyleDiffDeleted:  base.Foreground(charmtone.Cherry).Background(charmtone.Toast),  // GenericDeleted.
	}
}

// rangeStyle represents a style to apply to a character range using 0-indexed positions.
type rangeStyle struct {
	style lipgloss.Style
	rng   PositionRange
}

// overrideColor returns the overlay color if valid, otherwise the base color.
// Unlike blendColors, this does not blend - overlay takes precedence.
func overrideColor(base, overlay color.Color) color.Color {
	_, isNoColor := overlay.(lipgloss.NoColor)
	if overlay == nil || isNoColor {
		return base
	}

	if _, visible := colorful.MakeColor(overlay); visible {
		return overlay
	}

	return base
}

// blendColors blends two colors using LAB color space (50/50 mix).
// If both colors are nil/NoColor, it returns nil.
// If one color is nil/NoColor/invisible, it returns the other.
func blendColors(c1, c2 color.Color) color.Color {
	_, isNoColor1 := c1.(lipgloss.NoColor)
	_, isNoColor2 := c2.(lipgloss.NoColor)
	noColor1 := c1 == nil || isNoColor1
	noColor2 := c2 == nil || isNoColor2

	if noColor1 && noColor2 {
		return nil
	}

	if noColor1 {
		return c2
	}
	if noColor2 {
		return c1
	}

	cf1, visible1 := colorful.MakeColor(c1)
	cf2, visible2 := colorful.MakeColor(c2)

	if !visible1 {
		return c2
	}
	if !visible2 {
		return c1
	}

	return cf1.BlendLab(cf2, 0.5)
}

// blendStyles blends two styles: colors via LAB blending, transforms composed (overlay wraps base).
func blendStyles(base, overlay *lipgloss.Style) *lipgloss.Style {
	style := *base

	// Blend foreground colors.
	baseFg := style.GetForeground()
	overlayFg := overlay.GetForeground()

	blendedFg := blendColors(baseFg, overlayFg)
	if blendedFg != nil {
		style = style.Foreground(blendedFg)
	}

	// Blend background colors.
	baseBg := style.GetBackground()
	overlayBg := overlay.GetBackground()

	blendedBg := blendColors(baseBg, overlayBg)
	if blendedBg != nil {
		style = style.Background(blendedBg)
	}

	// Compose transforms: overlay wraps base.
	baseTransform := style.GetTransform()
	overlayTransform := overlay.GetTransform()

	switch {
	case baseTransform != nil && overlayTransform != nil:
		style = style.Transform(func(s string) string {
			return overlayTransform(baseTransform(s))
		})

	case overlayTransform != nil:
		style = style.Transform(overlayTransform)
		// Base transform is nil: keep base's transform (already in result).
	}

	return &style
}

// overrideStyles applies overlay on top of base: overlay properties replace base properties.
// Colors are overridden (not blended), transforms are overridden (not composed).
func overrideStyles(base, overlay *lipgloss.Style) *lipgloss.Style {
	style := *base

	// Override foreground if overlay has one.
	if fg := overrideColor(style.GetForeground(), overlay.GetForeground()); fg != nil {
		style = style.Foreground(fg)
	}

	// Override background if overlay has one.
	if bg := overrideColor(style.GetBackground(), overlay.GetBackground()); bg != nil {
		style = style.Background(bg)
	}

	// Override transform if overlay has one (not composed).
	if overlayTransform := overlay.GetTransform(); overlayTransform != nil {
		style = style.Transform(overlayTransform)
	}

	return &style
}

// stylesEqual compares two styles for equality (for span grouping purposes).
// Two styles are equal if they produce the same visual output.
func stylesEqual(s1, s2 *lipgloss.Style) bool {
	// Compare rendered output of a test string.
	// This is a pragmatic approach since lipgloss doesn't expose style internals.
	const testStr = "x"
	return s1.Render(testStr) == s2.Render(testStr)
}
