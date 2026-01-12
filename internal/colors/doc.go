// Package colors provides utilities for color and style manipulation.
//
// This package is used internally by [niceyaml.Printer] to handle overlapping
// style ranges when multiple highlights are applied to the same text region.
//
// # Color Functions
//
// [Override] returns the overlay color if valid, otherwise the base color.
// [Blend] blends two colors using LAB color space for perceptually uniform
// results, using [github.com/lucasb-eyer/go-colorful].
//
// # Style Functions
//
// [BlendStyles] combines two [lipgloss.Style]s: colors are blended via LAB,
// and transforms are composed (overlay wraps base).
// [OverrideStyles] applies overlay on top of base: overlay properties replace
// base properties without blending.
package colors
