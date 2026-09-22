// Package colors provides style combination utilities for layered styling.
//
// When rendering styled text, multiple style layers may apply to the same region.
//
// For example, a YAML key might have syntax highlighting while also being part
// of an error highlight.
//
// This package combines these overlapping styles into a single
// [lipgloss.Style].
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
// Blending composes transforms so both apply:
//
//	result := BlendStyles(baseStyle, overlayStyle)
//	// The result.Foreground is a 50/50 LAB blend.
//	// The result.Transform applies base then overlay.
//
// Overriding replaces properties entirely.
// Use it when the overlay should replace the base rather than mix with it:
//
//	result := OverrideStyles(baseStyle, overlayStyle)
//	// The result.Foreground is overlay's foreground.
//	// The result.Transform is overlay's transform only.
//
// Both strategies fall back to whichever color is visible when the other is
// nil, invisible, or [lipgloss.NoColor].
package colors
