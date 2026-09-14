package tokens

import (
	"iter"
	"strings"
	"unicode/utf8"

	"github.com/goccy/go-yaml/token"

	"go.jacobcolvin.com/niceyaml/position"
)

// Segment pairs an original source [*token.Token] with a segmented part token.
//
// Multiple [Segment] values may share the same [Segment.Source] pointer while
// having distinct [Segment.Part] values.
//
// A Segment never modifies its tokens, and its accessors return the stored
// pointers rather than copies. Treat the returned tokens as read-only, and
// call [token.Token.Clone] before changing one.
//
// Create instances with [NewSegment].
type Segment struct {
	// Source is a reference to the original token from the lexer.
	//
	// Multiple [Segment] values within the same [Segments] may share the same
	// Source pointer.
	//
	// Source must never be modified.
	source *token.Token

	// Part is a segment of the source token with Position adjusted.
	//
	// It may in some cases contain identical content to the source token, if there
	// is no segmentation needed (e.g. a single-line token).
	part *token.Token

	// Width is the cached rune count of part.Origin, excluding trailing newline.
	// NewSegment computes it once.
	width int
}

// NewSegment creates a new [Segment].
func NewSegment(source, part *token.Token) Segment {
	var w int

	if part != nil {
		w = utf8.RuneCountInString(strings.TrimSuffix(part.Origin, "\n"))
	}

	return Segment{
		source: source,
		part:   part,
		width:  w,
	}
}

// Width returns the rune count of this [Segment]'s part, excluding any
// trailing newline.
func (s Segment) Width() int {
	return s.width
}

// Contains reports whether the given [*token.Token] matches the source or part
// pointer of this [Segment].
func (s Segment) Contains(tk *token.Token) bool {
	return s.source == tk || s.part == tk
}

// SourceEquals reports whether the given [*token.Token] matches the source
// pointer.
func (s Segment) SourceEquals(tk *token.Token) bool {
	return s.source == tk
}

// PartEquals reports whether the given [*token.Token] matches the part pointer.
func (s Segment) PartEquals(tk *token.Token) bool {
	return s.part == tk
}

// Source returns the [Segment]'s source [*token.Token].
//
// The token is shared with every other Segment cut from it and with the
// caller that supplied it. Treat it as read-only.
func (s Segment) Source() *token.Token {
	return s.source
}

// Part returns the [Segment]'s part [*token.Token].
//
// The token is shared with every copy of this Segment. Treat it as read-only.
func (s Segment) Part() *token.Token {
	return s.part
}

// Segments is a sequence of [Segment] values, typically representing a single
// line's tokens.
//
// When constructed from a complete token stream, unique [Segment.Source]
// pointers represent the original tokens (deduplicated via
// [Segments.SourceTokens]).
//
// For multiline tokens, multiple consecutive [Segment] values share the same
// [Segment.Source] pointer.
type Segments []Segment

// Append appends a new [Segment] to the [Segments].
func (s Segments) Append(source, part *token.Token) Segments {
	return append(s, NewSegment(source, part))
}

// Merge combines this [Segments] with others, preserving source pointer identity.
// Returns a new [Segments] containing all [Segment] values in order.
func (s Segments) Merge(others ...Segments) Segments {
	for _, o := range others {
		s = append(s, o...)
	}

	return s
}

// Clone returns a copy of the [Segments] slice.
//
// The copy shares the source and part tokens with the original, since neither
// is modified after segmentation. Appending to one copy does not affect the
// other.
func (s Segments) Clone() Segments {
	if len(s) == 0 {
		return nil
	}

	result := make(Segments, len(s))
	copy(result, s)

	return result
}

// SourceTokens returns the unique source tokens in order.
//
// This is the inverse of segmentation. [Segment] values that share a
// [Segment.Source] pointer collapse to that original [*token.Token], which
// appears once. The slice is new, but the tokens are shared and keep the Next
// and Prev links the lexer gave them. Treat them as read-only.
func (s Segments) SourceTokens() token.Tokens {
	if len(s) == 0 {
		return nil
	}

	result := token.Tokens{}

	var lastSource *token.Token

	for _, seg := range s {
		if seg.source != lastSource {
			result = append(result, seg.source)

			lastSource = seg.source
		}
	}

	return result
}

// PartTokens returns all [Segment.Part] [*token.Token]s in order.
//
// The slice is new, but the tokens are shared. Treat them as read-only.
func (s Segments) PartTokens() token.Tokens {
	if len(s) == 0 {
		return nil
	}

	result := make(token.Tokens, 0, len(s))
	for _, seg := range s {
		result = append(result, seg.part)
	}

	return result
}

// NextColumn returns the next available column (0-indexed).
//
// Note that [token.Position] is 1-indexed, in which case this value + 1 can be
// used.
//
// Returns 0 if empty or no [Segment] has a valid position.
func (s Segments) NextColumn() int {
	col := 0
	for _, seg := range s {
		if seg.part != nil && seg.part.Position != nil && seg.part.Position.Column > col {
			col = seg.part.Position.Column
		}
	}

	return col
}

// SourceTokenAt returns the source [*token.Token] at the given 0-indexed
// column.
//
// The token is the shared original, so it compares equal by pointer to the
// token the lexer produced and to [Segment.Source]. Treat it as read-only.
//
// Returns nil if no token exists at that column.
func (s Segments) SourceTokenAt(col int) *token.Token {
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

// Segments2 represents a slice of [Segments], i.e. a 2-dimensional collection
// of [Segment] values. Each element typically represents one line's [Segments].
type Segments2 []Segments

// TokenRangesAt returns [position.Ranges] for all [Segment] values that share
// the same source token as the [Segment] at the given 0-indexed idx (typically
// line) and col.
//
// Positions are calculated based on [Segment] widths.
//
// Returns nil if the position is out of bounds or no [Segment] exists there.
func (s2 Segments2) TokenRangesAt(idx, col int) position.Ranges {
	if idx < 0 || idx >= len(s2) {
		return nil
	}

	source := s2[idx].SourceTokenAt(col)
	if source == nil {
		return nil
	}

	var ranges position.Ranges

	for i, segs := range s2 {
		lineCol := 0

		for _, seg := range segs {
			w := seg.Width()
			if seg.source == source && w > 0 {
				ranges = append(ranges, position.NewRange(
					position.New(i, lineCol),
					position.New(i, lineCol+w),
				))
			}

			lineCol += w
		}
	}

	return ranges
}

// countLeadingSpaces returns the number of leading space characters in s.
func countLeadingSpaces(s string) int {
	return len(s) - len(strings.TrimLeft(s, " "))
}

// countTrailingSpaces returns the number of trailing space characters in s.
func countTrailingSpaces(s string) int {
	return len(s) - len(strings.TrimRight(s, " "))
}

// ContentRangesAt returns [position.Ranges] for content at the given position,
// excluding leading and trailing spaces.
//
// Returns nil if there is no content at the given position or if the token is
// all whitespace.
func (s2 Segments2) ContentRangesAt(idx, col int) position.Ranges {
	if idx < 0 || idx >= len(s2) {
		return nil
	}

	source := s2[idx].SourceTokenAt(col)
	if source == nil {
		return nil
	}

	var ranges position.Ranges

	for i, segs := range s2 {
		lineCol := 0

		for _, seg := range segs {
			w := seg.Width()
			if seg.source == source && w > 0 {
				part := seg.Part()
				origin := strings.TrimSuffix(part.Origin, "\n")
				origin = strings.TrimSuffix(origin, "\r")

				leading := countLeadingSpaces(origin)
				trailing := countTrailingSpaces(origin)
				contentWidth := w - leading - trailing

				if contentWidth > 0 {
					ranges = append(ranges, position.NewRange(
						position.New(i, lineCol+leading),
						position.New(i, lineCol+leading+contentWidth),
					))
				}
			}

			lineCol += w
		}
	}

	return ranges
}

// ValueOffset calculates the byte offset where Value starts within the first
// non-empty line of the [*token.Token]'s Origin.
//
// This offset is used for string slicing operations.
func ValueOffset(tk *token.Token) int {
	firstLine, _, _ := strings.Cut(tk.Origin, "\n")
	if firstLine == "" {
		return 0
	}

	if idx := strings.Index(firstLine, tk.Value); idx >= 0 {
		return idx
	}

	return 0
}

// SplitDocumentsOption configures [SplitDocuments].
//
// Available options:
//   - [WithResetPositions]
type SplitDocumentsOption func(*splitDocumentsConfig)

type splitDocumentsConfig struct {
	resetPositions bool
}

// WithResetPositions is a [SplitDocumentsOption] that resets token positions
// so each document starts from line 1, column 1.
//
// When enabled, tokens are cloned and their positions adjusted relative to the
// document's start. By default, positions are preserved from the original source.
func WithResetPositions() SplitDocumentsOption {
	return func(cfg *splitDocumentsConfig) {
		cfg.resetPositions = true
	}
}

// CloneWithResetPositions clones tokens and adjusts positions relative to
// the document's starting position.
//
// The first token with a non-nil position determines the starting line, column,
// and offset. All subsequent token positions are adjusted relative to this start,
// so the first positioned token ends up at line 1, column 1, offset 0.
//
// Tokens with nil positions are cloned but left with nil positions.
func CloneWithResetPositions(tks token.Tokens) token.Tokens {
	if len(tks) == 0 {
		return tks
	}

	// Find starting position from first token with position.
	var startLine, startCol, startOffset int

	for _, tk := range tks {
		if tk != nil && tk.Position != nil {
			startLine = tk.Position.Line
			startCol = tk.Position.Column
			startOffset = tk.Position.Offset

			break
		}
	}

	result := make(token.Tokens, 0, len(tks))

	for _, tk := range tks {
		clone := tk.Clone()
		if clone.Position != nil {
			clone.Position.Line = clone.Position.Line - startLine + 1
			if clone.Position.Line == 1 {
				clone.Position.Column = clone.Position.Column - startCol + 1
			}

			clone.Position.Offset -= startOffset
		}

		result.Add(clone)
	}

	return result
}

// SplitDocuments splits a token stream into multiple token streams, one for
// each YAML document.
//
// A document header token ('---') starts a new document and is included at
// the start of it. A document end token ('...') closes the current document
// and is included at the end of it, so content that follows without a header
// forms a new document. These are the boundaries the go-yaml parser uses for
// well-formed streams. The parser may produce fewer documents than this
// function yields, for example when consecutive headers collapse, so pair
// the two by token offset rather than by index.
//
// The returned slices each contain tokens for a single document, preserving
// original token order and positions. The tokens are the caller's, not
// copies, and they keep the Next and Prev links of the full stream, so a
// document's first token still links back to the previous document. Pass
// [WithResetPositions] to receive clones instead.
func SplitDocuments(tks token.Tokens, opts ...SplitDocumentsOption) iter.Seq2[int, token.Tokens] {
	cfg := &splitDocumentsConfig{}
	for _, opt := range opts {
		opt(cfg)
	}

	return func(yield func(int, token.Tokens) bool) {
		var (
			docIdx  int
			current token.Tokens
		)

		yieldDoc := func(doc token.Tokens) bool {
			if cfg.resetPositions {
				doc = CloneWithResetPositions(doc)
			}

			return yield(docIdx, doc)
		}

		for _, tk := range tks {
			if tk.Type == token.DocumentHeaderType && len(current) > 0 {
				if !yieldDoc(current) {
					return
				}

				current = token.Tokens{}
				docIdx++
			}

			current = append(current, tk)

			if tk.Type == token.DocumentEndType {
				if !yieldDoc(current) {
					return
				}

				current = token.Tokens{}
				docIdx++
			}
		}

		if len(current) > 0 {
			if !yieldDoc(current) {
				return
			}
		}
	}
}
