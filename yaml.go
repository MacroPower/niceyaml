package niceyaml

import (
	"github.com/goccy/go-yaml"
)

// Path is an alias for [yaml.Path] for use with YAML path expressions.
type Path = yaml.Path

// NewPathBuilder returns a new [yaml.PathBuilder] initialized to the root path.
func NewPathBuilder() *yaml.PathBuilder {
	pb := &yaml.PathBuilder{}

	return pb.Root()
}

// NewPath returns a new [*Path] pointing to the root path, appending any provided children.
// It is a convenience function that can be used to build simple paths (e.g. '$.kind').
// For more complex paths (e.g. with array indices), use [NewPathBuilder].
func NewPath(children ...string) *Path {
	pb := NewPathBuilder()
	for _, child := range children {
		pb = pb.Child(child)
	}

	return pb.Build()
}
