package matcher

import (
	"context"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/paths"
)

// existsMatcher matches documents where a YAML path exists with a non-empty
// value.
type existsMatcher struct {
	path *paths.YAMLPath
}

// Exists creates a new [Matcher] that matches documents where the specified
// path exists and has a non-empty value.
//
// A value is considered empty if:
//   - The path does not exist in the document
//   - The value is an empty string
//   - The value is null
//
// This is useful for matching documents that have a particular field present,
// regardless of its specific value. For matching specific values, use [Content]
// instead.
//
// Panics if path is nil.
//
//	// Matches documents that have a kind field with any non-empty value.
//	matcher.Exists(paths.Root().Child("kind").Path())
//
// For requiring multiple fields, combine with [All]:
//
//	// Matches Kubernetes manifests (documents with both apiVersion and kind).
//	matcher.All(
//	    matcher.Exists(paths.Root().Child("apiVersion").Path()),
//	    matcher.Exists(paths.Root().Child("kind").Path()),
//	)
func Exists(path *paths.YAMLPath) Matcher {
	if path == nil {
		panic("matcher.Exists: path is nil")
	}

	return &existsMatcher{path: path}
}

// Match implements [Matcher].
func (m *existsMatcher) Match(_ context.Context, doc *niceyaml.DocumentDecoder) bool {
	v, ok := doc.GetValue(m.path)

	return ok && v != ""
}
