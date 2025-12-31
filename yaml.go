package niceyaml

import (
	"strings"

	"github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/token"
)

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
