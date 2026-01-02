// Package position defines line and column positions and ranges within a document.
package position

// Position represents a 0-indexed line and column location.
// Note that it is not simply an offset of go-yaml [token.Position]s, rather it represents
// the absolute line and column in a document, including in cases where multiple instances
// of the same token exist (e.g. in diffs).
type Position struct {
	Line, Col int
}

// New creates a new [Position].
func New(line, col int) Position {
	return Position{Line: line, Col: col}
}

// Range represents a half-open range [Start, End)
// between two [Position]s.
type Range struct {
	Start, End Position
}

// NewRange creates a new [Range].
func NewRange(start, end Position) Range {
	return Range{Start: start, End: end}
}

// Contains returns true if the given [Position] is within this [Range].
// The range is [Start, End) - Start is inclusive, End is exclusive.
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

// Ranges represents a set of [Range]s.
// Create new instances using [NewRanges].
type Ranges struct {
	value []Range
}

// NewRanges creates new [Ranges].
func NewRanges(ranges ...Range) *Ranges {
	prs := &Ranges{}
	for _, r := range ranges {
		prs.Add(r)
	}

	return prs
}

// Add adds [Range]s to the set in-place.
func (rs *Ranges) Add(r ...Range) {
	rs.value = append(rs.value, r...)
}

// Values returns all [Range]s in the set as a slice.
func (rs *Ranges) Values() []Range {
	return rs.value
}

// UniqueValues returns all unique [Range]s in the set as a slice.
func (rs *Ranges) UniqueValues() []Range {
	if len(rs.value) == 0 {
		return nil
	}

	seen := make(map[Range]struct{})
	result := make([]Range, 0, len(rs.value))

	for _, r := range rs.value {
		if _, exists := seen[r]; !exists {
			seen[r] = struct{}{}
			result = append(result, r)
		}
	}

	return result
}
