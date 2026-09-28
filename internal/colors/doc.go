// Package colors provides style combination utilities for layered styling.
//
// A printer can apply several style layers to one region of text. For
// example, a YAML key can carry syntax highlighting and an error highlight
// at once. This package combines those overlapping styles into a single
// [lipgloss.Style].
//
// # Combination Strategies
//
// The package combines a base style with an overlay in one of two ways.
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
