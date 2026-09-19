package matcher

import (
	"context"
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
// Match decodes the value with [niceyaml.Document.Get] as a T and compares
// the result to want, so the type of want decides how the YAML is read:
// a string matches the text of a scalar, and a number matches its numeric
// value however the document spells it. A document without the path, or
// whose value does not decode into T, does not match:
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
func (m *contentMatcher[T]) Match(ctx context.Context, doc *niceyaml.Document) bool {
	got, err := doc.Get[T](ctx, m.path)
	if err != nil {
		return false
	}

	// T may be an interface such as any, whose dynamic type decides whether
	// == is defined. A value == cannot compare, such as a map, matches
	// nothing rather than panicking.
	if !reflect.ValueOf(&got).Elem().Comparable() {
		return false
	}

	return got == m.want
}
