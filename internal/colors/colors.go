package colors

import (
	"image/color"

	"charm.land/lipgloss/v2"
	"github.com/lucasb-eyer/go-colorful"
)

// Override returns the overlay color, or the base color when the overlay
// is absent. An overlay is absent when it is nil, [lipgloss.NoColor], or
// invisible, the same rule that [Blend] applies. When both colors are
// absent, Override returns nil, as [Blend] does. Override clamps the color
// that it returns into the sRGB gamut, as [Blend] clamps. The result then
// renders as a valid Select Graphic Rendition (SGR) sequence.
func Override(base, overlay color.Color) color.Color {
	if _, ok := visible(overlay); ok {
		return Clamped(overlay)
	}

	if _, ok := visible(base); ok {
		return Clamped(base)
	}

	return nil
}

// Blend blends two colors with an even mix in LAB color space and clamps
// the result to the sRGB gamut. It returns the result as an 8-bit
// [color.RGBA] that renders as a valid Select Graphic Rendition (SGR)
// sequence. Blend keeps the high byte of each channel, the byte that the
// SGR writer prints for a 16-bit channel of more than 0xff. The writer
// prints a channel of at most 0xff as it is. Without the 8-bit result, a
// blend near black prints a channel of less than 1/256 as a bright one.
//
// A color is absent when it is nil, [lipgloss.NoColor], or invisible. When
// both colors are absent, Blend returns nil. When one is absent, Blend
// returns the other, clamped the same way when it lies outside the gamut.
// Blend also clamps a color outside the gamut before mixing, because the
// RGBA conversion that it reads wraps a negative channel to a bright one.
func Blend(c1, c2 color.Color) color.Color {
	cf1, visible1 := visible(c1)
	cf2, visible2 := visible(c2)

	switch {
	case !visible1 && !visible2:
		return nil
	case !visible1:
		return Clamped(c2)
	case !visible2:
		return Clamped(c1)
	}

	return color.RGBAModel.Convert(cf1.BlendLab(cf2, 0.5).Clamped())
}

// visible converts c for blending and reports whether c is visible: not
// nil, not [lipgloss.NoColor], and with an alpha of more than zero.
func visible(c color.Color) (colorful.Color, bool) {
	if c == nil {
		return colorful.Color{}, false
	}

	if _, ok := c.(lipgloss.NoColor); ok {
		return colorful.Color{}, false
	}

	return colorful.MakeColor(Clamped(c))
}

// Clamped returns c as it is when every channel lies in [0, 1], and c
// clamped to the sRGB gamut otherwise. Only a [colorful.Color] can hold a
// channel outside that range, because integer channels bound every other
// color type.
func Clamped(c color.Color) color.Color {
	if cf, ok := c.(colorful.Color); ok && !cf.IsValid() {
		return cf.Clamped()
	}

	return c
}

// BlendStyles blends two [lipgloss.Style] values. It blends the foreground,
// background, and underline colors in LAB color space and composes
// transforms so overlay wraps base. Text attributes that the overlay sets,
// such as bold or italic, apply on top of the attributes of the base. An
// underline style or a hyperlink that the overlay sets replaces the one of
// the base.
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

	// Blend underline colors.
	blendedUl := Blend(style.GetUnderlineColor(), overlay.GetUnderlineColor())
	if blendedUl != nil {
		style = style.UnderlineColor(blendedUl)
	}

	// Compose transforms so overlay wraps base.
	baseTransform := style.GetTransform()
	overlayTransform := overlay.GetTransform()

	switch {
	case baseTransform != nil && overlayTransform != nil:
		style = style.Transform(func(s string) string {
			return overlayTransform(baseTransform(s))
		})

	case overlayTransform != nil:
		style = style.Transform(overlayTransform)
		// The base transform is nil here. When the overlay has no
		// transform, style already holds the base's transform.
	}

	return style
}

// OverrideStyles applies overlay on top of base [lipgloss.Style], so
// overlay properties replace base properties.
//
// OverrideStyles replaces the foreground, background, and underline colors
// rather than blending them, and transforms rather than composing them.
// Text attributes that the overlay sets, such as bold or italic, apply on
// top of the attributes of the base. An underline style or a hyperlink
// that the overlay sets replaces the one of the base.
//
//nolint:gocritic // hugeParam: value semantics match lipgloss.
func OverrideStyles(base, overlay lipgloss.Style) lipgloss.Style {
	style := layerAttributes(base, overlay)

	// Replace each color. A color that neither style sets stays unset, so
	// the result can still inherit it.
	if fg := Override(style.GetForeground(), overlay.GetForeground()); fg != nil {
		style = style.Foreground(fg)
	}

	if bg := Override(style.GetBackground(), overlay.GetBackground()); bg != nil {
		style = style.Background(bg)
	}

	if ul := Override(style.GetUnderlineColor(), overlay.GetUnderlineColor()); ul != nil {
		style = style.UnderlineColor(ul)
	}

	// Override transform if overlay has one (not composed).
	if overlayTransform := overlay.GetTransform(); overlayTransform != nil {
		style = style.Transform(overlayTransform)
	}

	return style
}

// layerAttributes returns base with every text attribute that over sets
// turned on. It also puts the underline style and the hyperlink that over
// sets in place of those of base, so a curly underline on over stays
// curly. A [lipgloss.Style] reports an unset attribute as false, so an
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

	if u := over.GetUnderlineStyle(); u != lipgloss.UnderlineNone {
		base = base.UnderlineStyle(u)
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

	if link, params := over.GetHyperlink(); link != "" {
		base = base.Hyperlink(link, params)
	}

	return base
}
