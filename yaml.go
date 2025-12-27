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

// NewPathBuilder returns a new [yaml.PathBuilder] for constructing YAML paths.
func NewPathBuilder() *yaml.PathBuilder {
	return &yaml.PathBuilder{}
}

// NewRootPath returns a new [yaml.PathBuilder] initialized to the root path.
func NewRootPath() *yaml.PathBuilder {
	return NewPathBuilder().Root()
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
