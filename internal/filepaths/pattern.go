package filepaths

import (
	"errors"
	"path/filepath"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
)

// ErrInvalidPattern indicates the pattern syntax is invalid.
var ErrInvalidPattern = errors.New("invalid glob pattern")

// Pattern represents a validated glob pattern for file path matching.
// Create instances with [NewPattern] or [MustPattern].
type Pattern struct {
	raw string
}

// NewPattern creates a [Pattern] from the given glob pattern string.
// Returns [ErrInvalidPattern] if the pattern syntax is invalid.
func NewPattern(pattern string) (Pattern, error) {
	if !doublestar.ValidatePattern(pattern) {
		return Pattern{}, ErrInvalidPattern
	}

	return Pattern{raw: pattern}, nil
}

// MustPattern creates a [Pattern] from the given glob pattern string.
// Panics if the pattern syntax is invalid. Use this for compile-time
// validated patterns.
//
//	var configPattern = filepaths.MustPattern("**/*.yaml")
func MustPattern(pattern string) Pattern {
	p, err := NewPattern(pattern)
	if err != nil {
		panic("filepaths: " + err.Error() + ": " + pattern)
	}

	return p
}

// Match reports whether the path matches the pattern.
//
// The path is cleaned and its separators normalized to forward slashes
// before matching, so "./config.yaml" and "config.yaml" both match the
// root-only pattern "*.yaml".
func (p Pattern) Match(path string) bool {
	if p.raw == "" || path == "" {
		return false
	}

	// Error is ignored since we validated the pattern at construction time.
	matched, _ := doublestar.Match(p.raw, normalizePath(path)) //nolint:errcheck // Pattern was validated.

	return matched
}

// normalizePath returns path cleaned, with forward slashes as separators,
// which is the form the patterns match against. Cleaning drops a leading
// "./", collapses repeated separators, and resolves ".." elements, so the
// spelling of a path does not decide whether it matches.
func normalizePath(path string) string {
	return filepath.ToSlash(filepath.Clean(path))
}

// String returns the original pattern string.
func (p Pattern) String() string {
	return p.raw
}

// MatchAny reports whether path matches any of the glob patterns, with
// the semantics VS Code and yaml-language-server give a schema fileMatch
// pattern. A pattern applies at any depth of the tree, so "*.yaml" matches
// "some/dir/config.yaml" and ".github/workflows/*.yml" matches
// "/repo/.github/workflows/ci.yml". Every pattern gets an implicit "**/"
// prefix unless it already has one, and a leading "/" is dropped first.
//
// The path is cleaned and its separators normalized to forward slashes
// before matching, as [Pattern.Match] does.
//
// # Pattern Validation
//
// Invalid patterns are silently skipped without error. This is intentional for
// use cases like SchemaStore catalog entries where pattern typos should not
// cause validation failures. For patterns that must be validated upfront, use
// [NewPattern] or [MustPattern] instead.
func MatchAny(path string, patterns []string) bool {
	if path == "" {
		return false
	}

	path = normalizePath(path)

	for _, pattern := range patterns {
		matched, err := doublestar.Match(anyDepth(pattern), path)
		if err == nil && matched {
			return true
		}
	}

	return false
}

// anyDepth returns pattern with the "**/" prefix that lets it match at any
// depth of the tree. A leading "/" is dropped first, and a pattern that
// already starts with "**/" comes back unchanged.
func anyDepth(pattern string) string {
	pattern = strings.TrimPrefix(pattern, "/")

	if strings.HasPrefix(pattern, "**/") {
		return pattern
	}

	return "**/" + pattern
}
