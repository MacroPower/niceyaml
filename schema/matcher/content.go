package matcher

import (
	"context"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/paths"
)

// contentMatcher matches documents by a single YAML content value.
type contentMatcher struct {
	path  *paths.YAMLPath
	value string
}

// Content creates a new [Matcher] that matches documents based on a YAML
// content value at the specified path.
//
// Content uses exact string comparison against the YAML scalar value's string
// representation. This means:
//
//   - String values match directly: `kind: Deployment` matches `"Deployment"`
//   - Unquoted values are compared as strings: `version: 2` matches `"2"`
//   - Boolean keywords match their string form: `enabled: true` matches `"true"`
//   - Null values match "null": `value: null` matches `"null"`
//
// Note that quoted and unquoted values are equivalent: both version: "2" and
// version: 2 will match "2". The comparison is case-sensitive.
//
// Panics if path is nil.
//
//	// Matches documents with kind: Deployment.
//	matcher.Content(paths.Root().Child("kind").Path(), "Deployment")
//
// For multiple conditions, use [All] (AND) or [Any] (OR):
//
//	// Matches documents with kind: Deployment AND apiVersion: apps/v1.
//	matcher.All(
//	    matcher.Content(paths.Root().Child("kind").Path(), "Deployment"),
//	    matcher.Content(paths.Root().Child("apiVersion").Path(), "apps/v1"),
//	)
func Content(path *paths.YAMLPath, value string) Matcher {
	if path == nil {
		panic("matcher.Content: path is nil")
	}

	return &contentMatcher{path: path, value: value}
}

// Match implements [Matcher].
func (m *contentMatcher) Match(_ context.Context, doc *niceyaml.DocumentDecoder) bool {
	v, ok := doc.GetValue(m.path)

	return ok && v == m.value
}
