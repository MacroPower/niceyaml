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

// Get returns the [lipgloss.Style] for the given [Style] category.
// If the given [Style] is not defined, it returns [StyleDefault].
// If no [StyleDefault] is defined, it returns an empty [lipgloss.Style].
func (s Styles) Get(style Style) *lipgloss.Style {
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

// tokenStyle represents a style to apply at a specific token position.
type tokenStyle struct {
	style lipgloss.Style
	pos   Position
}

// rangeStyle represents a style to apply to a character range.
type rangeStyle struct {
	style lipgloss.Style
	rng   PositionRange
}

// blendColors blends two colors using LAB color space (50/50 mix).
// If either color is nil/NoColor, returns the other.
func blendColors(c1, c2 color.Color) color.Color {
	// Handle nil/NoColor cases.
	_, isNoColor1 := c1.(lipgloss.NoColor)
	_, isNoColor2 := c2.(lipgloss.NoColor)

	if c1 == nil || isNoColor1 {
		return c2
	}

	if c2 == nil || isNoColor2 {
		return c1
	}

	// Convert to colorful.Color.
	cf1, ok1 := colorful.MakeColor(c1)
	cf2, ok2 := colorful.MakeColor(c2)

	if !ok1 {
		return c2
	}

	if !ok2 {
		return c1
	}

	// Blend in LAB space.
	return cf1.BlendLab(cf2, 0.5)
}

// blendStyles blends two styles: colors via LAB blending, transforms composed (overlay wraps base).
func blendStyles(base, overlay *lipgloss.Style) *lipgloss.Style {
	style := *base

	// Blend foreground colors.
	baseFg := style.GetForeground()
	overlayFg := overlay.GetForeground()

	if blended := blendColors(baseFg, overlayFg); blended != nil {
		style = style.Foreground(blended)
	}

	// Blend background colors.
	baseBg := style.GetBackground()
	overlayBg := overlay.GetBackground()

	if blended := blendColors(baseBg, overlayBg); blended != nil {
		style = style.Background(blended)
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

// stylesEqual compares two styles for equality (for span grouping purposes).
// Two styles are equal if they produce the same visual output.
func stylesEqual(s1, s2 *lipgloss.Style) bool {
	// Compare rendered output of a test string.
	// This is a pragmatic approach since lipgloss doesn't expose style internals.
	const testStr = "x"
	return s1.Render(testStr) == s2.Render(testStr)
}
