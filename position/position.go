package position

import (
	"fmt"
	"math"
	"slices"
	"strings"

	"github.com/goccy/go-yaml/token"
)

const (
	// Maximum column value used to indicate "end of line" when slicing ranges.
	//
	// Chosen to be larger than any realistic line length while remaining easy to
	// read in debug output.
	maxCol = 1_000_000
)

// Position represents a 0-indexed line and column location.
//
// Note that it is not simply an offset of [token.Position]s, rather it
// represents the absolute line and column in a document, including in cases
// where multiple instances of the same token exist (e.g. in diffs).
//
// Create instances with [New].
type Position struct {
	Line, Col int
}

// New creates a new [Position].
func New(line, col int) Position {
	return Position{Line: line, Col: col}
}

// NewFromToken creates a new [Position] from a [*token.Token], converting from
// the 1-indexed coordinates used by [token.Position] to the 0-indexed
// coordinates used by this package.
//
// Returns the zero position if tk or its position is nil.
func NewFromToken(tk *token.Token) Position {
	var line, col int

	if tk != nil && tk.Position != nil {
		line = max(0, tk.Position.Line-1)
		col = max(0, tk.Position.Column-1)
	}

	return Position{Line: line, Col: col}
}

// String returns the position in "line:col" format with 1-indexed values,
// which is how editors count, so the output suits people. The fields
// themselves stay 0-indexed.
func (p Position) String() string {
	return fmt.Sprintf("%d:%d", p.Line+1, p.Col+1)
}

// Range represents a half-open range [Start, End) between two [Position] values.
// Create instances with [NewRange].
type Range struct {
	Start, End Position
}

// NewRange creates a new [Range].
func NewRange(start, end Position) Range {
	return Range{Start: start, End: end}
}

// Contains reports whether the given [Position] is within this [Range].
// The range is half-open [Start, End): Start is inclusive, End is exclusive.
func (r Range) Contains(pos Position) bool {
	// Before start?
	if pos.Line < r.Start.Line || (pos.Line == r.Start.Line && pos.Col < r.Start.Col) {
		return false
	}

	// At or after end?
	if pos.Line > r.End.Line || (pos.Line == r.End.Line && pos.Col >= r.End.Col) {
		return false
	}

	return true
}

// String returns the range in "startLine:startCol-endLine:endCol" format with
// 1-indexed values, as [Position.String] does.
func (r Range) String() string {
	return fmt.Sprintf("%s-%s", r.Start.String(), r.End.String())
}

// lastLine returns the last line r covers. A multi-line range that ends at
// column 0 holds nothing on its end line, so it stops at the line before. For
// a range that ends before its start, on an earlier line or at an earlier
// column of the same line, lastLine returns a line before r.Start.Line, so
// the range covers none.
func (r Range) lastLine() int {
	if r.Start.Line == r.End.Line && r.End.Col < r.Start.Col {
		return r.Start.Line - 1
	}

	if r.End.Col == 0 && r.End.Line > r.Start.Line {
		return r.End.Line - 1
	}

	return r.End.Line
}

// SliceLines splits a multi-line range into per-line ranges.
//
// Every line but the last extends to the end of the line. A range that ends
// at column 0 of a later line covers nothing on that line, so SliceLines
// stops at the line before it. A range that ends before its start, on an
// earlier line or at an earlier column of the same line, covers no lines,
// and SliceLines returns nil for it.
func (r Range) SliceLines() Ranges {
	if r.Start.Line == r.End.Line {
		if r.End.Col < r.Start.Col {
			return nil
		}

		return Ranges{r}
	}

	lineCount := r.lastLine() - r.Start.Line + 1
	if lineCount <= 0 {
		return nil
	}

	result := make(Ranges, lineCount)

	for i := range lineCount {
		line := r.Start.Line + i

		var start, end Position

		switch {
		case i == 0:
			start = Position{Line: line, Col: r.Start.Col}
			end = Position{Line: line, Col: maxCol}

		case line == r.End.Line:
			start = Position{Line: line, Col: 0}
			end = Position{Line: line, Col: r.End.Col}

		default:
			start = Position{Line: line, Col: 0}
			end = Position{Line: line, Col: maxCol}
		}

		result[i] = Range{Start: start, End: end}
	}

	return result
}

// Span represents a half-open range [Start, End) of integers.
// Use for 1-dimensional ranges like column spans or line spans.
// Create instances with [NewSpan].
type Span struct {
	Start, End int
}

// NewSpan creates a new [Span].
func NewSpan(start, end int) Span {
	return Span{Start: start, End: end}
}

// Len returns the length of the span.
func (s Span) Len() int {
	return s.End - s.Start
}

// Contains reports whether v is within this [Span] [Start, End).
func (s Span) Contains(v int) bool {
	return v >= s.Start && v < s.End
}

// Overlaps reports whether this [Span] overlaps with another.
// Empty spans (where Start == End) never overlap with anything.
func (s Span) Overlaps(other Span) bool {
	if s.Start >= s.End || other.Start >= other.End {
		return false
	}

	return s.Start < other.End && s.End > other.Start
}

// String returns the span in "start-end" format (0-indexed).
func (s Span) String() string {
	return fmt.Sprintf("%d-%d", s.Start, s.End)
}

// Spans represents a slice of [Span] values with chainable transformation methods.
type Spans []Span

// Expand returns new spans with each span expanded by amount on both sides.
// The Start is decreased by amount and End is increased by amount, and each
// saturates at the int limits rather than wrapping around. A negative amount
// shrinks the spans, which can invert one.
// Note: This does not clamp values; use [Spans.Clamp] afterward if needed.
func (s Spans) Expand(amount int) Spans {
	if len(s) == 0 {
		return nil
	}

	result := make(Spans, len(s))
	for i, span := range s {
		result[i] = NewSpan(subSat(span.Start, amount), addSat(span.End, amount))
	}

	return result
}

// Clamp returns new spans with all values clamped to [lower, upper), dropping
// every span that holds nothing there: one that lies outside the bounds, one
// that is empty, and one whose Start is past its End. Every span it returns
// has a positive [Span.Len] and lies within the bounds. Returns nil when no
// span remains.
func (s Spans) Clamp(lower, upper int) Spans {
	var result Spans

	for _, span := range s {
		clamped := NewSpan(
			min(max(span.Start, lower), upper),
			min(max(span.End, lower), upper),
		)
		if clamped.Len() <= 0 {
			continue
		}

		result = append(result, clamped)
	}

	return result
}

// addSat returns a+b, saturating at [math.MinInt] and [math.MaxInt] instead
// of wrapping around.
func addSat(a, b int) int {
	sum := a + b

	switch {
	case b > 0 && sum < a:
		return math.MaxInt

	case b < 0 && sum > a:
		return math.MinInt

	default:
		return sum
	}
}

// subSat returns a-b, saturating at [math.MinInt] and [math.MaxInt] instead
// of wrapping around.
func subSat(a, b int) int {
	diff := a - b

	switch {
	case b > 0 && diff > a:
		return math.MinInt

	case b < 0 && diff < a:
		return math.MaxInt

	default:
		return diff
	}
}

// Ranges represents a slice of [Range] values.
type Ranges []Range

// UniqueValues returns all unique [Range] values in the collection,
// preserving insertion order.
func (rs Ranges) UniqueValues() Ranges {
	if len(rs) == 0 {
		return nil
	}

	seen := make(map[Range]struct{})
	result := make(Ranges, 0, len(rs))

	for _, r := range rs {
		if _, exists := seen[r]; !exists {
			seen[r] = struct{}{}
			result = append(result, r)
		}
	}

	return result
}

// LineIndices returns all line indices covered by the [Ranges].
// A multi-line range contributes each line within it, except an end line it
// touches only at column 0, which holds none of it. A range that ends on a
// line before its start line contributes none.
// Duplicate line indices are returned if covered by multiple ranges.
func (rs Ranges) LineIndices() []int {
	if len(rs) == 0 {
		return nil
	}

	var result []int

	for _, r := range rs {
		last := r.lastLine()
		for line := r.Start.Line; line <= last; line++ {
			result = append(result, line)
		}
	}

	return result
}

// String returns all [Range] values as a comma-separated list.
func (rs Ranges) String() string {
	if len(rs) == 0 {
		return ""
	}

	var b strings.Builder

	for i, r := range rs {
		if i > 0 {
			b.WriteString(", ")
		}

		b.WriteString(r.String())
	}

	return b.String()
}

// GroupIndices groups indices into [Span] values. Indices within context
// distance of each other share a span. The indices need not be sorted, and
// a negative context counts as 0.
//
// Uses threshold = 2*context + 1 which ensures indices merge when their context
// windows would overlap or be adjacent. The threshold saturates at
// [math.MaxInt], so a huge context merges every index into one span.
//
// Returns half-open [Spans] [Start, End).
//
// For example, with context=2 (threshold=5):
//   - Indices [0, 4] merge because 4 < 0+1+5 → span [0, 5)
//   - Indices [0, 6] don't merge because 6 >= 0+1+5 → spans [0, 1), [6, 7)
func GroupIndices(indices []int, context int) Spans {
	if len(indices) == 0 {
		return nil
	}

	context = max(0, context)

	// Merge indices when their context windows would be adjacent or overlapping.
	// Index at I1 has context [I1-C, I1+C], index at I2 has context [I2-C, I2+C].
	// Merge if I2-C <= I1+C+1, i.e., I2 < I1 + 2C + 2.
	// Since spans are half-open [Start, End), we use End (which is I1+1) + threshold.
	threshold := addSat(addSat(context, context), 1)

	indices = slices.Clone(indices)
	slices.Sort(indices)

	spans := Spans{NewSpan(indices[0], addSat(indices[0], 1))}

	for _, idx := range indices[1:] {
		lastSpan := &spans[len(spans)-1]

		// A limit that saturated reaches every index, so merge on it too.
		limit := addSat(lastSpan.End, threshold)
		if idx < limit || limit == math.MaxInt {
			// Merge into current span.
			lastSpan.End = addSat(idx, 1)
		} else {
			// Start a new span.
			spans = append(spans, NewSpan(idx, addSat(idx, 1)))
		}
	}

	return spans
}

// ContextSpans returns the spans of lines to show around indices: each index
// with context lines on either side, merged where the windows would touch or
// overlap, and clamped to [0, total). It is [GroupIndices] followed by
// [Spans.Expand] and [Spans.Clamp], which is how error excerpts and diff
// hunks pick the lines they render. A negative context counts as 0, and a
// context at or above total covers every line. Every span returned holds at
// least one index of [0, total), so an index outside that range contributes
// none. Returns nil when no span remains, such as when indices is empty.
func ContextSpans(indices []int, context, total int) Spans {
	context = max(0, context)

	// Expand runs before Clamp, so an index just outside the bounds would
	// otherwise pull a window back inside them. Drop it first.
	inRange := make([]int, 0, len(indices))

	for _, idx := range indices {
		if idx >= 0 && idx < total {
			inRange = append(inRange, idx)
		}
	}

	return GroupIndices(inRange, context).Expand(context).Clamp(0, total)
}
