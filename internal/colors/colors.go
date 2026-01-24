package colors

import (
	"image/color"
	"sync"

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

// Blend blends two colors using LAB color space (50/50 mix).
// If both colors are nil or [lipgloss.NoColor], it returns nil.
// If one color is nil, [lipgloss.NoColor], or invisible, it returns the other.
func Blend(c1, c2 color.Color) color.Color {
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

// BlendStyles blends two [*lipgloss.Style] values: colors via LAB blending,
// transforms composed (overlay wraps base).
func BlendStyles(base, overlay *lipgloss.Style) *lipgloss.Style {
	style := *base

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

	return &style
}

// OverrideStyles applies overlay on top of base [*lipgloss.Style]: overlay
// properties replace base properties.
//
// Colors are overridden (not blended), transforms are overridden (not composed).
func OverrideStyles(base, overlay *lipgloss.Style) *lipgloss.Style {
	style := *base

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

	return &style
}

// Blender provides cached [*lipgloss.Style] blending and overriding operations.
//
// It assigns unique keys to all styles (including blended results) so that
// identical blend operations return the same pointer, enabling pointer equality.
//
// Create instances with [NewBlender].
type Blender struct {
	keys    map[*lipgloss.Style]uint64 // Pointer to key.
	styles  map[uint64]*lipgloss.Style // Computed blend key to result.
	nextKey uint64                     // Counter for assigning keys.

	mu sync.Mutex
}

// NewBlender creates a new [*Blender].
func NewBlender() *Blender {
	return &Blender{
		keys:    make(map[*lipgloss.Style]uint64),
		styles:  make(map[uint64]*lipgloss.Style),
		nextKey: 1,
	}
}

// key returns the unique key for a style, assigning one if needed.
func (b *Blender) key(s *lipgloss.Style) uint64 {
	if k, ok := b.keys[s]; ok {
		return k
	}

	k := b.nextKey
	b.nextKey++
	b.keys[s] = k

	return k
}

// blendKey computes a cache key for a blend operation.
func (b *Blender) blendKey(base, overlay *lipgloss.Style, override bool) uint64 {
	baseKey := b.key(base)
	overlayKey := b.key(overlay)
	key := baseKey<<32 | overlayKey
	if override {
		key |= 1 << 63 // Use high bit for override flag.
	}

	return key
}

// Blend blends two [*lipgloss.Style] values, caching the result.
//
// If override is true, overlay replaces base (see [OverrideStyles]); if false,
// colors are blended (see [BlendStyles]).
//
// Returns a stable pointer for identical combinations.
func (b *Blender) Blend(base, overlay *lipgloss.Style, override bool) *lipgloss.Style {
	b.mu.Lock()
	defer b.mu.Unlock()

	key := b.blendKey(base, overlay, override)

	if cached, ok := b.styles[key]; ok {
		return cached
	}

	var result *lipgloss.Style
	if override {
		result = OverrideStyles(base, overlay)
	} else {
		result = BlendStyles(base, overlay)
	}

	b.styles[key] = result
	b.keys[result] = b.nextKey
	b.nextKey++

	return result
}
