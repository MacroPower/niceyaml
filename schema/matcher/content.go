package matcher

import (
	"context"
	"errors"
	"reflect"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/paths"
)

// contentMatcher matches documents by a single YAML content value.
type contentMatcher[T comparable] struct {
	want T
	path paths.Path
}

// Content creates a new [Matcher] that matches documents whose value at
// path decodes to want.
//
// Match decodes the value at path, from the scope of the document, with
// [niceyaml.Node.Decode] as a T and compares the result to want, so
// the type of want decides how Match reads the YAML. A string matches
// the text of a scalar, and a number matches its numeric value however
// the document spells it. A document without the path, or whose value
// does not decode into T, does not match. Any other error from the read,
// such as an alias on the path that names no anchor, a path with a
// wildcard selector, or a context that ended, comes back as the error,
// so a registry stops at the document rather than routing it elsewhere:
//
//	// Matches kind: Deployment.
//	matcher.Content(paths.Root().Child("kind"), "Deployment")
//
//	// Matches version: 1.0 and version: 1.
//	matcher.Content(paths.Root().Child("version"), 1.0)
//
// For several conditions, use [All] (AND) or [Any] (OR):
//
//	// Matches kind: Deployment AND apiVersion: apps/v1.
//	matcher.All(
//	    matcher.Content(paths.Root().Child("kind"), "Deployment"),
//	    matcher.Content(paths.Root().Child("apiVersion"), "apps/v1"),
//	)
func Content[T comparable](path paths.Path, want T) Matcher {
	return &contentMatcher[T]{path: path, want: want}
}

// Match implements [Matcher].
func (m *contentMatcher[T]) Match(ctx context.Context, doc *niceyaml.Node) (bool, error) {
	err := ctx.Err()
	if err != nil {
		//nolint:wrapcheck // The error of the context is the reason the matcher cannot decide.
		return false, err
	}

	node, err := doc.At(m.path)
	if errors.Is(err, paths.ErrNotFound) {
		return false, nil
	}

	if err != nil {
		//nolint:wrapcheck // The Document binds the error already.
		return false, err
	}

	// The decoder rejecting the value is the value not reading as T, which
	// is a no rather than a failure. An error the value's own UnmarshalYAML
	// returns is not a rejection, so it comes back as the error.
	got, err := node.Decode[T](ctx)
	if errors.Is(err, niceyaml.ErrDecodeRejected) {
		return false, nil
	}

	if err != nil {
		return false, err
	}

	// T may be an interface such as any, whose dynamic type decides whether
	// == is defined. A value that == cannot compare, such as a map,
	// matches nothing rather than panicking.
	if !reflect.ValueOf(&got).Elem().Comparable() {
		return false, nil
	}

	return got == m.want, nil
}
