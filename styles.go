package niceyaml

import (
	"image/color"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/exp/charmtone"
	"github.com/lucasb-eyer/go-colorful"
)

// ColorScheme defines styles for YAML syntax highlighting.
// Each field is a [lipgloss.Style] that will be applied to the corresponding token type.
type ColorScheme struct {
	Default      lipgloss.Style
	Key          lipgloss.Style
	String       lipgloss.Style
	Number       lipgloss.Style
	Bool         lipgloss.Style
	Null         lipgloss.Style // E.g.: ~, null.
	Anchor       lipgloss.Style // E.g.: &.
	Alias        lipgloss.Style // E.g.: *, <<.
	Comment      lipgloss.Style
	Error        lipgloss.Style
	Tag          lipgloss.Style // E.g.: !tag.
	Document     lipgloss.Style // E.g.: ---.
	Directive    lipgloss.Style // E.g.: %YAML, %TAG.
	Punctuation  lipgloss.Style // E.g.: -, :, ?, [, ], {, }.
	BlockScalar  lipgloss.Style // E.g.: |, >.
	DiffInserted lipgloss.Style // Lines inserted in diff (+).
	DiffDeleted  lipgloss.Style // Lines deleted in diff (-).
}

// DefaultColorScheme returns a [ColorScheme] using CharmTone colors.
func DefaultColorScheme() ColorScheme {
	base := lipgloss.NewStyle().
		Foreground(charmtone.Smoke).
		Background(charmtone.Pepper)

	return ColorScheme{
		Default:      base,
		Key:          base.Foreground(charmtone.Mauve),  // NameTag.
		String:       base.Foreground(charmtone.Cumin),  // LiteralString.
		Number:       base.Foreground(charmtone.Julep),  // LiteralNumber.
		Bool:         base.Foreground(charmtone.Malibu), // KeywordConstant.
		Null:         base.Foreground(charmtone.Malibu), // KeywordConstant.
		Anchor:       base.Foreground(charmtone.Bengal), // CommentPreproc.
		Alias:        base.Foreground(charmtone.Bengal), // CommentPreproc.
		Comment:      base.Foreground(charmtone.Oyster), // Comment.
		Tag:          base.Foreground(charmtone.Bengal), // CommentPreproc.
		Document:     base.Foreground(charmtone.Smoke),  // NameNamespace.
		Directive:    base.Foreground(charmtone.Smoke),  // NameNamespace.
		Punctuation:  base.Foreground(charmtone.Zest),   // Punctuation.
		BlockScalar:  base.Foreground(charmtone.Zest),   // Punctuation.
		Error:        base.Foreground(charmtone.Butter).Background(charmtone.Sriracha),
		DiffInserted: base.Foreground(charmtone.Julep).Background(charmtone.Spinach), // GenericInserted.
		DiffDeleted:  base.Foreground(charmtone.Cherry).Background(charmtone.Toast),  // GenericDeleted.
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
