package matcher

import (
	"context"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/paths"
)

// existsMatcher matches documents that hold a node at a YAML path.
type existsMatcher struct {
	path paths.Path
}

// Exists creates a new [Matcher] that matches documents that hold a node
// at path, whatever its value. A key with a null or empty value is present,
// so `kind:` and `kind: ""` both match. A document without the path, or
// one where the path does not resolve, does not match. To match a
// particular value, use [Content].
//
//	// Matches documents that have a kind field.
//	matcher.Exists(paths.Root().Child("kind"))
//
// To require several fields, combine with [All]:
//
//	// Matches Kubernetes manifests (documents with both apiVersion and kind).
//	matcher.All(
//	    matcher.Exists(paths.Root().Child("apiVersion")),
//	    matcher.Exists(paths.Root().Child("kind")),
//	)
func Exists(path paths.Path) Matcher {
	return &existsMatcher{path: path}
}

// Match implements [Matcher].
func (m *existsMatcher) Match(_ context.Context, doc *niceyaml.Document) bool {
	_, err := m.path.Node(doc.Node())

	return err == nil
}
