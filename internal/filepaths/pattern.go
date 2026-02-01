package filepaths

import (
	"errors"
	"path/filepath"

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
// Path separators are normalized to forward slashes for cross-platform
// consistency.
func (p Pattern) Match(path string) bool {
	if p.raw == "" || path == "" {
		return false
	}

	// Normalize path separators for cross-platform consistency.
	path = filepath.ToSlash(path)

	// Error is ignored since we validated the pattern at construction time.
	matched, _ := doublestar.Match(p.raw, path) //nolint:errcheck // Pattern was validated.

	return matched
}

// String returns the original pattern string.
func (p Pattern) String() string {
	return p.raw
}

// MatchAnyWithBase checks if a file path matches any of the given glob patterns.
// It first tries matching against the base name (for simple patterns like
// "*.yaml"), then tries matching against the full path (for patterns with
// directory components).
//
// Path separators are normalized to forward slashes for cross-platform
// consistency.
//
// # Pattern Validation
//
// Invalid patterns are silently skipped without error. This is intentional for
// use cases like SchemaStore catalog entries where pattern typos should not
// cause validation failures. For patterns that must be validated upfront, use
// [NewPattern] or [MustPattern] instead.
func MatchAnyWithBase(path string, patterns []string) bool {
	if path == "" {
		return false
	}

	// Normalize path separators for cross-platform consistency.
	path = filepath.ToSlash(path)
	baseName := filepath.Base(path)

	for _, pattern := range patterns {
		// Try matching against the base name first (for simple patterns like "*.yaml").
		matched, err := doublestar.Match(pattern, baseName)
		if err == nil && matched {
			return true
		}

		// Try matching against the full path (for patterns with directory components).
		matched, err = doublestar.Match(pattern, path)
		if err == nil && matched {
			return true
		}
	}

	return false
}
