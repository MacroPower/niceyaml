package position

import (
	"fmt"
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

// String returns the position in "line:col" format with 1-indexed values.
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
// 1-indexed values.
func (r Range) String() string {
	return fmt.Sprintf("%s-%s", r.Start.String(), r.End.String())
}

// SliceLines splits a multi-line range into per-line ranges.
func (r Range) SliceLines() []Range {
	if r.Start.Line == r.End.Line {
		return []Range{r}
	}

	lineCount := r.End.Line - r.Start.Line + 1
	result := make([]Range, lineCount)

	for i := range lineCount {
		line := r.Start.Line + i

		var start, end Position

		switch i {
		case 0:
			start = Position{Line: line, Col: r.Start.Col}
			end = Position{Line: line, Col: maxCol}

		case lineCount - 1:
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
// The Start is decreased by amount and End is increased by amount.
// Note: This does not clamp values; use [Spans.Clamp] afterward if needed.
func (s Spans) Expand(amount int) Spans {
	if len(s) == 0 {
		return nil
	}

	result := make(Spans, len(s))
	for i, span := range s {
		result[i] = NewSpan(span.Start-amount, span.End+amount)
	}

	return result
}

// Clamp returns new spans with all values clamped to [lower, upper).
// Start values are clamped to be >= lower, End values are clamped to be <= upper.
func (s Spans) Clamp(lower, upper int) Spans {
	if len(s) == 0 {
		return nil
	}

	result := make(Spans, len(s))
	for i, span := range s {
		result[i] = NewSpan(
			max(span.Start, lower),
			min(span.End, upper),
		)
	}

	return result
}

// Ranges represents a slice of [Range] values.
type Ranges []Range

// UniqueValues returns all unique [Range] values in the collection,
// preserving insertion order.
func (rs Ranges) UniqueValues() []Range {
	if len(rs) == 0 {
		return nil
	}

	seen := make(map[Range]struct{})
	result := make([]Range, 0, len(rs))

	for _, r := range rs {
		if _, exists := seen[r]; !exists {
			seen[r] = struct{}{}
			result = append(result, r)
		}
	}

	return result
}

// LineIndices returns all line indices covered by the [Ranges].
// For multi-line ranges, each line within the range is included.
// Duplicate line indices are returned if covered by multiple ranges.
func (rs Ranges) LineIndices() []int {
	if len(rs) == 0 {
		return nil
	}

	var result []int
	for _, r := range rs {
		for line := r.Start.Line; line <= r.End.Line; line++ {
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

// GroupIndices groups sorted indices into [Span] values where indices within
// context distance are merged.
//
// Uses threshold = 2*context + 1 which ensures indices merge when their context
// windows would overlap or be adjacent.
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

	// Merge indices when their context windows would be adjacent or overlapping.
	// Index at I1 has context [I1-C, I1+C], index at I2 has context [I2-C, I2+C].
	// Merge if I2-C <= I1+C+1, i.e., I2 < I1 + 2C + 2.
	// Since spans are half-open [Start, End), we use End (which is I1+1) + threshold.
	threshold := context*2 + 1

	spans := Spans{NewSpan(indices[0], indices[0]+1)}

	for _, idx := range indices[1:] {
		lastSpan := &spans[len(spans)-1]
		if idx < lastSpan.End+threshold {
			// Merge into current span.
			lastSpan.End = idx + 1
		} else {
			// Start a new span.
			spans = append(spans, NewSpan(idx, idx+1))
		}
	}

	return spans
}

// PrefixSums holds precomputed prefix sums for O(1) range queries.
// Create instances with [NewPrefixSums].
type PrefixSums struct {
	sums []int // Sums[i] = sum of elements 0..i-1.
}

// NewPrefixSums creates a [*PrefixSums] from n elements.
// The valueFn returns the value at index i.
func NewPrefixSums(n int, valueFn func(i int) int) *PrefixSums {
	sums := make([]int, n+1)
	for i := range n {
		sums[i+1] = sums[i] + valueFn(i)
	}

	return &PrefixSums{sums: sums}
}

// At returns the cumulative sum up to (but not including) index i.
func (p *PrefixSums) At(i int) int {
	return p.sums[i]
}

// Range returns the sum of elements in the given [Span].
func (p *PrefixSums) Range(s Span) int {
	return p.sums[s.End] - p.sums[s.Start]
}
