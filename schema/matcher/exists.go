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
// resolves it. A key with a null or empty value is present,
// so `kind:` and `kind: ""` both match. A document without the path does
// not match. A path that cannot resolve, because an alias on it names no
// anchor or it holds a wildcard selector, comes back as the error. To
// match a particular value, use [Content].
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
