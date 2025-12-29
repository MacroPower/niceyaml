package niceyaml

import (
	"strings"

	"github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/token"
)

// Position represents a 0-indexed line and column location.
// Note that it is not simply an offset of [token.Position],
// rather it represents the absolute line and column in the
// document, including in cases where multiple instances of
// the same [token.Position] exist (e.g. in diffs).
type Position struct {
	Line, Col int
}

// NewPosition creates a new [Position].
func NewPosition(line, col int) Position {
	return Position{Line: line, Col: col}
}

// PositionRange represents a half-open range [Start, End)
// between two [Position]s.
type PositionRange struct {
	Start, End Position
}

// NewPositionRange creates a new [PositionRange].
func NewPositionRange(start, end Position) PositionRange {
	return PositionRange{Start: start, End: end}
}

// Contains returns true if the given [Position] is within this [PositionRange].
// The range is [Start, End) - Start is inclusive, End is exclusive.
func (r PositionRange) Contains(pos Position) bool {
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

// NewPathBuilder returns a new [yaml.PathBuilder] initialized to the root path.
func NewPathBuilder() *yaml.PathBuilder {
	pb := &yaml.PathBuilder{}

	return pb.Root()
}

// NewPath returns a new [*yaml.Path] pointing to the root path, appending any provided children.
// It is a convenience function that can be used to build simple paths (e.g. '$.kind').
// For more complex paths (e.g. with array indices), use [NewPathBuilder].
func NewPath(children ...string) *yaml.Path {
	pb := NewPathBuilder()
	for _, child := range children {
		pb = pb.Child(child)
	}

	return pb.Build()
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
