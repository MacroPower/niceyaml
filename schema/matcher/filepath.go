package matcher

import (
	"context"
	"fmt"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/filepaths"
)

// ErrInvalidPattern reports a glob pattern [FilePath] cannot use, which is
// an empty pattern, one whose syntax does not parse, one whose braces
// expand to more patterns than matching can afford, or one that keeps a
// ".." after a glob element such as "*", which no cleaned path can match.
var ErrInvalidPattern = filepaths.ErrInvalidPattern

// filePathMatcher matches documents by file path glob pattern.
type filePathMatcher struct {
	pattern filepaths.Pattern
}

// FilePath creates a new [Matcher] that matches documents based on a file path
// glob pattern.
//
// Match tests the full file path against the pattern using doublestar
// glob syntax. A pattern whose syntax does not parse comes back as
// [ErrInvalidPattern]. Use [MustFilePath] for patterns known to be valid at
// compile time.
//
//	// Matches any YAML file recursively.
//	m, err := matcher.FilePath("**/*.yaml")
//
//	// Matches any YAML file in k8s directories.
//	m, err := matcher.FilePath("**/k8s/*.yaml")
//
//	// Matches YAML files only in the root directory.
//	m, err := matcher.FilePath("*.yaml")
//
// An empty pattern is [ErrInvalidPattern] too, since it would match nothing
// and silently disable the [Matcher]. So is a pattern that keeps a ".."
// after a glob element, as in "configs/*/../x.yaml", because a cleaned path
// holds a ".." only at its start. A ".." after "**" stays valid when only
// ".." and "**" elements come before it in a relative pattern, since "**"
// can match no directory at all, so "**/../x.yaml" matches "../x.yaml". A
// rooted pattern such as "/**/../x.yaml", or one with a name before the
// "**" such as "a/**/../x.yaml", is [ErrInvalidPattern].
func FilePath(pattern string) (Matcher, error) {
	if pattern == "" {
		return nil, fmt.Errorf("%w: %q", ErrInvalidPattern, pattern)
	}

	p, err := filepaths.NewPattern(pattern)
	if err != nil {
		return nil, fmt.Errorf("%w: %q", err, pattern)
	}

	return &filePathMatcher{pattern: p}, nil
}

// MustFilePath is like [FilePath] but panics on a pattern [FilePath]
// reports as [ErrInvalidPattern].
//
// Use it for patterns known to be valid at compile time:
//
//	matcher.MustFilePath("**/k8s/*.yaml")
func MustFilePath(pattern string) Matcher {
	m, err := FilePath(pattern)
	if err != nil {
		panic("matcher.MustFilePath: " + err.Error())
	}

	return m
}

// Match implements [Matcher].
func (m *filePathMatcher) Match(_ context.Context, doc *niceyaml.Node) (bool, error) {
	return m.pattern.Match(doc.FilePath()), nil
}
