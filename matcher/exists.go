package matcher

import (
	"context"
	"errors"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/paths"
)

// existsMatcher matches documents that hold a node at a YAML path.
type existsMatcher struct {
	path paths.Path
}

// Exists creates a new [Matcher] that matches documents that hold a node
// at path, whatever its value. The path resolves as [niceyaml.Node.At]
// resolves it. Keys with a null or empty value count as present, so
// `kind:` and `kind: ""` both match. Documents without the path do not
// match. Match returns the error of a path that cannot resolve. Such a
// path holds a wildcard selector, or an alias that does not name an
// earlier anchor. An alias at the end of the path that reads a reference
// document of [niceyaml.WithReferences] resolves, so the document matches.
// A path through such an alias returns an error that wraps
// [paths.ErrReferenceAlias]. To match a particular value, use [Content].
//
// The Matcher describes itself as `$.kind exists`, through [Describer].
// That description is the reason that
// [go.jacobcolvin.com/niceyaml/schema.When] gives when it declines.
//
//	// Matches documents that have a kind field.
//	matcher.Exists(paths.Doc().Child("kind"))
//
// To require several fields, combine with [All]:
//
//	// Matches Kubernetes manifests (documents with both apiVersion and kind).
//	matcher.All(
//	    matcher.Exists(paths.Doc().Child("apiVersion")),
//	    matcher.Exists(paths.Doc().Child("kind")),
//	)
func Exists(path paths.Path) Matcher {
	return &existsMatcher{path: path}
}

// Describe implements [Describer].
func (m *existsMatcher) Describe() string {
	return m.path.String() + " exists"
}

// Match implements [Matcher].
func (m *existsMatcher) Match(ctx context.Context, doc *niceyaml.Node) (bool, error) {
	err := ctx.Err()
	if err != nil {
		//nolint:wrapcheck // The error of the context is the reason the matcher cannot decide.
		return false, err
	}

	_, err = doc.At(m.path)
	if errors.Is(err, paths.ErrNotFound) {
		return false, nil
	}

	if err != nil {
		//nolint:wrapcheck // The Node binds the error already.
		return false, err
	}

	return true, nil
}
