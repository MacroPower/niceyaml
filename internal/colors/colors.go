package colors

import (
	"image/color"

	"charm.land/lipgloss/v2"
	"github.com/lucasb-eyer/go-colorful"
)

// Override returns the overlay color if valid, otherwise the base color.
// Unlike [Blend], this does not blend - overlay takes precedence.
func Override(base, overlay color.Color) color.Color {
	_, isNoColor := overlay.(lipgloss.NoColor)
	if overlay == nil || isNoColor {
		return base
	}

	if _, visible := colorful.MakeColor(overlay); visible {
		return overlay
	}

	return base
}

// Blend blends two colors using LAB color space (50/50 mix) and clamps the
// result to the sRGB gamut, so every channel of the returned color lies in
// [0, 1] and renders as a valid SGR sequence.
// If both colors are nil or [lipgloss.NoColor], it returns nil.
// If one color is nil, [lipgloss.NoColor], or invisible, it returns the
// other, clamped the same way when it lies outside the gamut.
func Blend(c1, c2 color.Color) color.Color {
	_, isNoColor1 := c1.(lipgloss.NoColor)
	_, isNoColor2 := c2.(lipgloss.NoColor)
	noColor1 := c1 == nil || isNoColor1
	noColor2 := c2 == nil || isNoColor2

	if noColor1 && noColor2 {
		return nil
	}

	if noColor1 {
		return clamped(c2)
	}

	if noColor2 {
		return clamped(c1)
	}

	cf1, visible1 := colorful.MakeColor(c1)
	cf2, visible2 := colorful.MakeColor(c2)

	if !visible1 {
		return clamped(c2)
	}

	if !visible2 {
		return clamped(c1)
	}

	return cf1.BlendLab(cf2, 0.5).Clamped()
}

// clamped returns c as it is when every channel lies in [0, 1], and c
// clamped to the sRGB gamut otherwise. Only a [colorful.Color] can hold a
// channel outside that range; every other color type is bounded by its
// integer channels.
func clamped(c color.Color) color.Color {
	if cf, ok := c.(colorful.Color); ok && !cf.IsValid() {
		return cf.Clamped()
	}

	return c
}

// BlendStyles blends two [lipgloss.Style] values: colors via LAB blending,
// transforms composed (overlay wraps base), and text attributes such as
// bold or underline kept from either style.
//
//nolint:gocritic // hugeParam: value semantics match lipgloss.
func BlendStyles(base, overlay lipgloss.Style) lipgloss.Style {
	style := layerAttributes(base, overlay)

	// Blend foreground colors.
	baseFg := style.GetForeground()
	overlayFg := overlay.GetForeground()

	blendedFg := Blend(baseFg, overlayFg)
	if blendedFg != nil {
		style = style.Foreground(blendedFg)
	}

	// Blend background colors.
	baseBg := style.GetBackground()
	overlayBg := overlay.GetBackground()

	blendedBg := Blend(baseBg, overlayBg)
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

	return style
}

// OverrideStyles applies overlay on top of base [lipgloss.Style]: overlay
// properties replace base properties.
//
// Colors are overridden (not blended), transforms are overridden (not
// composed), and text attributes the overlay sets, such as bold or
// underline, apply on top of the base's.
//
//nolint:gocritic // hugeParam: value semantics match lipgloss.
func OverrideStyles(base, overlay lipgloss.Style) lipgloss.Style {
	style := layerAttributes(base, overlay)

	// Override foreground if overlay has one.
	if fg := Override(style.GetForeground(), overlay.GetForeground()); fg != nil {
		style = style.Foreground(fg)
	}

	// Override background if overlay has one.
	if bg := Override(style.GetBackground(), overlay.GetBackground()); bg != nil {
		style = style.Background(bg)
	}

	// Override transform if overlay has one (not composed).
	if overlayTransform := overlay.GetTransform(); overlayTransform != nil {
		style = style.Transform(overlayTransform)
	}

	return style
}

// layerAttributes returns base with every text attribute that over sets
// turned on. A lipgloss.Style reports an unset attribute as false, so an
// overlay cannot turn an attribute of the base off.
//
//nolint:gocritic // hugeParam: value semantics match lipgloss.
func layerAttributes(base, over lipgloss.Style) lipgloss.Style {
	if over.GetBold() {
		base = base.Bold(true)
	}

	if over.GetItalic() {
		base = base.Italic(true)
	}

	if over.GetUnderline() {
		base = base.Underline(true)
	}

	if over.GetStrikethrough() {
		base = base.Strikethrough(true)
	}

	if over.GetFaint() {
		base = base.Faint(true)
	}

	if over.GetBlink() {
		base = base.Blink(true)
	}

	if over.GetReverse() {
		base = base.Reverse(true)
	}

	return base
}
