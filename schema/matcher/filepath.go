package matcher

import (
	"context"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/filepaths"
)

// filePathMatcher matches documents by file path glob pattern.
type filePathMatcher struct {
	pattern filepaths.Pattern
}

// FilePath creates a new [Matcher] that matches documents based on file path
// glob patterns.
//
// The pattern is matched against the full file path using doublestar glob
// syntax. Use [filepaths.MustPattern] to create patterns at init time for
// compile-time validation.
//
//	// Matches any YAML file recursively
//	matcher.FilePath(filepaths.MustPattern("**/*.yaml"))
//
//	// Matches any YAML file in k8s directories
//	matcher.FilePath(filepaths.MustPattern("**/k8s/*.yaml"))
//
//	// Matches specific config files in the root
//	matcher.FilePath(filepaths.MustPattern("config.yaml"))
//
//	// Matches YAML files only in the root directory
//	matcher.FilePath(filepaths.MustPattern("*.yaml"))
func FilePath(pattern filepaths.Pattern) Matcher {
	return &filePathMatcher{pattern: pattern}
}

// Match implements [Matcher].
func (m *filePathMatcher) Match(_ context.Context, doc *niceyaml.DocumentDecoder) bool {
	filePath := doc.FilePath()
	if filePath == "" {
		return false
	}

	return m.pattern.Match(filePath)
}
