package tokens

import (
	"strings"

	"github.com/goccy/go-yaml/token"

	"github.com/macropower/niceyaml/position"
)

// Segment pairs an original source [*token.Token] with a segmented part token.
// Multiple Segments may share the same Source pointer while having distinct Parts.
// Create instances with [NewSegment].
type Segment struct {
	// Source is a reference to the original token from the lexer.
	// Multiple [Segment]s within the same [Segments] may share the same Source pointer.
	// Source must never be modified.
	source *token.Token

	// Part is a segment of the source token with Position adjusted.
	// It may in some cases contain identical content to the source token,
	// if there is no segmentation needed (e.g. a single-line token).
	part *token.Token

	// Width is the cached rune count of part.Origin, excluding trailing newline.
	// Computed once at creation to avoid repeated allocations.
	width int
}

// NewSegment creates a new [Segment].
func NewSegment(source, part *token.Token) Segment {
	var w int
	if part != nil {
		w = len([]rune(strings.TrimSuffix(part.Origin, "\n")))
	}

	return Segment{
		source: source,
		part:   part,
		width:  w,
	}
}

// Width returns the rune count of this segment's part, excluding any trailing newline.
func (s Segment) Width() int {
	return s.width
}

// Contains returns true if the given [*token.Token] matches the source or part pointer of this Segment.
func (s Segment) Contains(tk *token.Token) bool {
	return s.source == tk || s.part == tk
}

// SourceEquals returns true if the given [*token.Token] matches the source pointer.
func (s Segment) SourceEquals(tk *token.Token) bool {
	return s.source == tk
}

// PartEquals returns true if the given [*token.Token] matches the part pointer.
func (s Segment) PartEquals(tk *token.Token) bool {
	return s.part == tk
}

// Source returns a clone of the Segment's Source token.
func (s Segment) Source() *token.Token {
	if s.source == nil {
		return nil
	}

	return s.source.Clone()
}

// Part returns a clone of the Segment's Part token.
func (s Segment) Part() *token.Token {
	if s.part == nil {
		return nil
	}

	return s.part.Clone()
}

// Segments is a sequence of [Segment]s.
// When constructed from a complete token stream, unique Source pointers
// represent the original tokens (deduplicated via [Segments.SourceTokens]).
// For multiline tokens, multiple consecutive segments share the same Source pointer.
type Segments []Segment

// Append appends a new Segment to the Segments.
func (s Segments) Append(source, part *token.Token) Segments {
	return append(s, NewSegment(source, part))
}

// Merge combines this Segments with others, preserving source pointer identity.
// Returns a new Segments containing all segments in order.
func (s Segments) Merge(others ...Segments) Segments {
	for _, o := range others {
		s = append(s, o...)
	}

	return s
}

// Clone returns a copy of the Segments with cloned Parts but shared Source pointers.
// Sources are intentionally shared since they are immutable references to original tokens.
func (s Segments) Clone() Segments {
	if len(s) == 0 {
		return nil
	}

	result := make(Segments, 0, len(s))
	for _, seg := range s {
		result = append(result, NewSegment(seg.source, seg.Part()))
	}

	return result
}

// SourceTokens returns clones of unique source tokens in order.
// This is the inverse of segmentation: segments that share a Source pointer
// are deduplicated to return a clone of each original token once.
func (s Segments) SourceTokens() token.Tokens {
	if len(s) == 0 {
		return nil
	}

	result := token.Tokens{}

	var lastSource *token.Token

	for _, seg := range s {
		if seg.source != lastSource {
			result.Add(seg.Source())

			lastSource = seg.source
		}
	}

	return result
}

// PartTokens returns clones of all [Segment.Part] tokens in order.
func (s Segments) PartTokens() token.Tokens {
	if len(s) == 0 {
		return nil
	}

	result := make(token.Tokens, 0, len(s))
	for _, seg := range s {
		result.Add(seg.Part())
	}

	return result
}

// NextColumn returns the next available column (0-indexed).
// Note that [*token.Position] is 1-indexed, in which case this value + 1 can be used.
// Returns 0 if empty or no segment has a valid position.
func (s Segments) NextColumn() int {
	col := 0
	for _, seg := range s {
		if seg.part != nil && seg.part.Position != nil && seg.part.Position.Column > col {
			col = seg.part.Position.Column
		}
	}

	return col
}

// SourceTokenAt returns a clone of the Source token at the given 0-indexed column.
// Returns nil if no token exists at that column.
func (s Segments) SourceTokenAt(col int) *token.Token {
	tk := s.sourceTokenAtPtr(col)
	if tk == nil {
		return nil
	}

	return tk.Clone()
}

// sourceTokenAtPtr returns the raw Source token pointer at the given 0-indexed column.
// This is for internal use where pointer identity is needed for segment matching.
// Returns nil if no token exists at that column.
func (s Segments) sourceTokenAtPtr(col int) *token.Token {
	c := 0
	for _, seg := range s {
		w := seg.Width()
		if col >= c && col < c+w {
			return seg.source
		}

		c += w
	}

	return nil
}

// Segments2 represents a slice of [Segments], i.e. 2-dimensional [Segment]s.
// Each element typically represents one line's [Segments].
type Segments2 []Segments

// TokenRangesAt returns position ranges for all [Segments] that share the same
// source token as the segment at the given 0-indexed idx (typically line) and col.
// Positions are calculated based on segment widths.
// Returns nil if the position is out of bounds or no segment exists there.
func (s2 Segments2) TokenRangesAt(idx, col int) *position.Ranges {
	if idx < 0 || idx >= len(s2) {
		return nil
	}

	source := s2[idx].sourceTokenAtPtr(col)
	if source == nil {
		return nil
	}

	ranges := position.NewRanges()

	for i, segs := range s2 {
		lineCol := 0

		for _, seg := range segs {
			w := seg.Width()
			if seg.source == source && w > 0 {
				ranges.Add(position.NewRange(
					position.New(i, lineCol),
					position.New(i, lineCol+w),
				))
			}

			lineCol += w
		}
	}

	return ranges
}
