// Package position provides 0-indexed coordinates for locating and spanning
// regions within text documents.
//
// YAML processing often requires tracking where tokens appear in source text,
// highlighting search matches, or marking regions for visual styling.
//
// This package provides the coordinate types needed for these operations, using
// consistent 0-indexed conventions that simplify arithmetic and integrate
// cleanly with slice operations.
//
// # Coordinate System
//
// All types use 0-indexed coordinates and half-open intervals [Start, End)
// where Start is inclusive and End is exclusive.
//
// This matches Go slice semantics: a span from 5 to 10 contains 5 elements
// (indices 5, 6, 7, 8, 9). Length is simply End - Start.
//
// When converting from external sources like go-yaml tokens (which use
// 1-indexed positions), use [NewFromToken] to handle the offset automatically.
//
// # 2D Positions and Ranges
//
// [Position] represents a line and column location.
//
// [Range] spans between two positions, useful for highlighting search matches
// or error locations:
//
//	start := position.New(0, 5)     // Line 0, column 5.
//	end := position.New(0, 10)      // Line 0, column 10.
//	r := position.NewRange(start, end)
//
// Multi-line ranges can be split into per-line ranges using [Range.SliceLines],
// which is useful when applying line-by-line styling:
//
//	multiLine := position.NewRange(position.New(0, 5), position.New(2, 10))
//	perLine := multiLine.SliceLines() // Returns 3 single-line ranges.
//
// [Ranges] collects multiple ranges and provides methods like
// [Ranges.LineIndices] for querying which lines are covered and
// [Ranges.UniqueValues] for deduplication.
//
// # 1D Spans
//
// [Span] represents a simple half-open range of integers, useful for column
// spans within a line or line ranges within a document.
//
// [Spans] is a slice type with chainable transformations:
//
//	spans := position.GroupIndices([]int{0, 2, 10, 12}, 2) // Group with context.
//	spans = spans.Expand(3).Clamp(0, 100)                  // Expand then clamp.
//
// [GroupIndices] is particularly useful for creating context windows around
// matched lines, merging adjacent matches when their context would overlap.
//
// # Range Sum Queries
//
// [PrefixSums] enables O(1) range sum queries over a sequence, useful for
// computing cumulative widths or offsets:
//
//	widths := []int{3, 5, 2, 8}
//	ps := position.NewPrefixSums(len(widths), func(i int) int { return widths[i] })
//	total := ps.Range(position.NewSpan(1, 3)) // Sum of widths[1:3] = 5 + 2 = 7.
package position
