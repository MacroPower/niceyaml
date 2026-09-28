// Package position provides 0-indexed coordinates for locating and spanning
// regions within text documents.
//
// YAML processing often requires tracking where tokens appear in source text,
// highlighting search matches, or marking regions for visual styling.
//
// This package provides the coordinate types for these operations.
//
// # Coordinate System
//
// All types use 0-indexed coordinates and half-open intervals [Start, End)
// where Start is inclusive and End is exclusive.
//
// This matches Go slice semantics. A span from 5 to 10 contains 5 elements
// (indices 5, 6, 7, 8, 9). Length is End - Start.
//
// When converting from external sources like go-yaml tokens (which use
// 1-indexed positions), use [NewFromToken] to handle the offset automatically.
//
// # 2D Coordinates
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
// A range is a pair of coordinates and knows nothing of the text it lies
// on. To split a multi-line range into one range per line, each ending
// where its line does, call a method of the lines that hold the text,
// [go.jacobcolvin.com/niceyaml/line.Lines.SliceLines].
//
// [Ranges] collects multiple ranges and provides methods like
// [Ranges.LineIndices] for querying which lines the ranges cover and
// [Ranges.UniqueValues] for deduplication.
//
// # 1D Spans
//
// [Span] represents a half-open range of integers, useful for column
// spans within a line or line ranges within a document.
//
// [Spans] is a slice type with chainable transformations:
//
//	spans := position.GroupIndices([]int{0, 2, 10, 12}, 2) // Group with context.
//	spans = spans.Expand(3).Clamp(0, 100)                  // Expand then clamp.
//
// [GroupIndices] builds context windows around matched lines and merges
// adjacent matches when their context would overlap. [ContextSpans] runs
// that chain in one call. It groups the indices, expands each span by the
// context, and clamps the result to the document:
//
//	hunks := position.ContextSpans(errorLines, 2, lines.Len())
package position
