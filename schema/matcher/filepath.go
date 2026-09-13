package matcher

import (
	"context"
	"fmt"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/filepaths"
)

// filePathMatcher matches documents by file path glob pattern.
type filePathMatcher struct {
	pattern filepaths.Pattern
}

// FilePath creates a new [Matcher] that matches documents based on a file path
// glob pattern.
//
// The pattern is matched against the full file path using doublestar glob
// syntax. Returns an error if the pattern syntax is invalid. Use
// [MustFilePath] for patterns known to be valid at compile time.
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
// An empty pattern is rejected, since it would match nothing and silently
// disable the [Matcher].
func FilePath(pattern string) (Matcher, error) {
	if pattern == "" {
		return nil, fmt.Errorf("%w: %q", filepaths.ErrInvalidPattern, pattern)
	}

	p, err := filepaths.NewPattern(pattern)
	if err != nil {
		return nil, fmt.Errorf("%w: %q", err, pattern)
	}

	return &filePathMatcher{pattern: p}, nil
}

// MustFilePath is like [FilePath] but panics if the pattern is invalid.
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
func (m *filePathMatcher) Match(_ context.Context, doc *niceyaml.DocumentDecoder) bool {
	filePath := doc.FilePath()
	if filePath == "" {
		return false
	}

	return m.pattern.Match(filePath)
}
