// Package colors provides style combination utilities for layered styling.
//
// When rendering styled text, multiple style layers may apply to the same region.
//
// For example, a YAML key might have syntax highlighting while also being part
// of an error highlight.
//
// This package provides strategies for combining these overlapping styles into
// a coherent visual result.
//
// # Combination Strategies
//
// Two strategies are available for combining a base style with an overlay:
//
// Blending mixes colors in LAB color space for perceptually uniform results.
//
// A red syntax color blended with a yellow error highlight produces an orange
// that visually represents both.
//
// Transforms are composed so both apply:
//
//	result := BlendStyles(baseStyle, overlayStyle)
//	// The result.Foreground is a 50/50 LAB blend.
//	// The result.Transform applies base then overlay.
//
// Overriding replaces properties entirely.
// This is useful when the newer style should completely supersede the base:
//
//	result := OverrideStyles(baseStyle, overlayStyle)
//	// The result.Foreground is overlay's foreground.
//	// The result.Transform is overlay's transform only.
//
// Both strategies handle nil, invisible, and [lipgloss.NoColor] gracefully,
// falling back to whichever color is actually visible.
//
// # Caching with Blender
//
// [Blender] caches combination results and assigns unique keys to each style,
// including derived styles. This enables pointer equality checks.
//
// If you blend the same two styles twice, you get the exact same pointer back:
//
//	b := NewBlender()
//	r1 := b.Blend(base, overlay, false)
//	r2 := b.Blend(base, overlay, false)
//	// Via pointer equality, r1 == r2.
//
// This is valuable when the same style combinations are computed repeatedly
// during rendering, as it avoids redundant allocations and allows fast equality
// comparisons.
package colors
