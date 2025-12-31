package niceyaml

import (
	"github.com/goccy/go-yaml"
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
