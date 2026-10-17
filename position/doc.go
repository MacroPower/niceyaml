// Package position provides 0-indexed coordinates for locating and spanning
// regions within text documents.
//
// Code that processes YAML often tracks where tokens appear in source text,
// highlights search matches, or marks regions for visual styling. This
// package provides the coordinate types for that work.
//
// # Coordinate System
//
// All types use 0-indexed coordinates and half-open intervals [Start, End),
// where Start is inclusive and End is exclusive.
//
// Half-open intervals match Go slice semantics. A span from 5 to 10 contains
// 5 elements (indices 5, 6, 7, 8, 9). Its length is End minus Start.
//
// The tokens of go-yaml count lines and columns from 1. [NewFromToken]
// converts the position of such a token to the 0-indexed coordinates of this
// package.
//
// # 2D Coordinates
//
// [Position] represents a line and column location.
//
// [Range] spans the region between two positions, such as a search match or
// the location of an error:
//
//	start := position.New(0, 5)     // Line 0, column 5.
//	end := position.New(0, 10)      // Line 0, column 10.
//	r := position.NewRange(start, end)
//
// A range is a pair of coordinates and does not refer to the text it lies
// on. To split a multi-line range into one range per line, call
// [go.jacobcolvin.com/niceyaml/line.Lines.SliceLines] on the lines that hold
// the text. Each range it returns ends where its line ends.
//
// [Ranges] collects multiple ranges. Its methods include
// [Ranges.LineIndices], which lists the lines the ranges cover, and
// [Ranges.UniqueValues], which drops duplicate ranges.
//
// # 1D Spans
//
// [Span] represents a half-open range of integers, such as the columns of a
// line or the lines of a document.
//
// [Spans] is a slice type with chainable transformations:
//
//	spans := position.GroupIndices([]int{0, 2, 10, 12}, 2) // Group with context.
//	spans = spans.Expand(3).Clamp(0, 100)                  // Expand then clamp.
//
// [GroupIndices] groups the indices of matched lines and merges two matches
// into one span when their context windows overlap or touch. [ContextSpans]
// runs the whole chain in one call. It groups the indices, expands each span
// by the context, and clamps the result to the document. Slicing a
// [go.jacobcolvin.com/niceyaml/line.View] by the spans with
// [go.jacobcolvin.com/niceyaml/line.View.Slice] gives a view of the lines of
// the hunks:
//
//	hunks := position.ContextSpans(errorLines, 2, lines.Len())
//	lipgloss.Println(p.Print(view.Slice(hunks...)))
//
// ContextSpans returns nil when the indices do not include a line of the
// document. A slice by an empty list of spans does not hold any line, so a
// view sliced by that nil result does not render anything.
package position
