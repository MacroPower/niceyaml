package niceyaml

import (
	"strings"

	"github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/token"
)

// Position represents a line and column location in YAML source.
// Line and Col are 1-indexed to match [token.Position].
type Position struct {
	Line, Col int
}

// PositionRange represents a half-open range [Start, End) between two positions.
// It is used by [Printer.AddStyleToRange] to apply styles across character ranges.
type PositionRange struct {
	Start, End Position
}

// Contains returns true if the given position is within this range.
// The range is [Start, End) - Start is inclusive, End is exclusive.
func (r PositionRange) Contains(line, col int) bool {
	// Before start?
	if line < r.Start.Line || (line == r.Start.Line && col < r.Start.Col) {
		return false
	}
	// At or after end?
	if line > r.End.Line || (line == r.End.Line && col >= r.End.Col) {
		return false
	}

	return true
}

// PositionTracker tracks line and column positions through text.
// Line and Col are 1-indexed to match [token.Position].
type PositionTracker struct {
	Line, Col int
}

// NewPositionTrackerFromTokens creates a tracker initialized from the first token.
// If tokens is empty, returns a tracker at position (1, 1).
func NewPositionTrackerFromTokens(tokens token.Tokens) *PositionTracker {
	if len(tokens) == 0 {
		return &PositionTracker{Line: 1, Col: 1}
	}

	line, col := initialPositionFromToken(tokens[0])

	return &PositionTracker{Line: line, Col: col}
}

// Advance updates the position after processing rune r.
func (t *PositionTracker) Advance(r rune) {
	if r == '\n' {
		t.Line++
		t.Col = 1
	} else {
		t.Col++
	}
}

// AdvanceBy updates the position after processing n non-newline characters.
func (t *PositionTracker) AdvanceBy(n int) {
	t.Col += n
}

// AdvanceNewline updates the position for a newline.
func (t *PositionTracker) AdvanceNewline() {
	t.Line++
	t.Col = 1
}

// Position returns the current position.
func (t *PositionTracker) Position() Position {
	return Position{Line: t.Line, Col: t.Col}
}

// NewPathBuilder returns a new [yaml.PathBuilder] for constructing YAML paths.
func NewPathBuilder() *yaml.PathBuilder {
	return &yaml.PathBuilder{}
}

// initialPositionFromToken calculates the starting line and column for a token.
// It adjusts for leading whitespace in Origin by subtracting the value offset.
// Returns 1-indexed line and column values.
func initialPositionFromToken(tk *token.Token) (int, int) {
	line := tk.Position.Line
	col := max(tk.Position.Column-tokenValueOffset(tk), 1)

	return line, col
}

// tokenValueOffset calculates the byte offset where Value starts within the
// first non-empty line of the token's Origin. This offset is used for string
// slicing operations.
func tokenValueOffset(tk *token.Token) int {
	lines := strings.SplitSeq(tk.Origin, "\n")
	for line := range lines {
		if line != "" {
			idx := strings.Index(line, tk.Value)
			if idx >= 0 {
				return idx
			}

			break
		}
	}

	return 0
}
