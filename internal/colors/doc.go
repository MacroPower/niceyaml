// Package colors combines layered styles and the colors that they set.
//
// A printer can apply several style layers to one region of text. For
// example, a YAML key can carry syntax highlighting and an error highlight
// at once. This package combines those overlapping styles into a single
// [lipgloss.Style].
//
// # Combination Strategies
//
// The package combines a base style with an overlay by blending or by
// overriding.
//
// Blending mixes colors in the CIELAB (LAB) color space for perceptually
// uniform results.
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
//	// The result.Foreground is overlay's foreground when it sets one.
//	// The result.Transform is overlay's transform when it sets one, not a composition.
//
// Both strategies use whichever color is visible when the other is nil,
// invisible, or [lipgloss.NoColor].
package colors
