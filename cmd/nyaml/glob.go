package main

import (
	"fmt"
	"sort"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
)

// glob returns the file paths matching pattern.
//
// Unlike [path/filepath.Glob], this supports ** for recursive directory
// matching. The pattern syntax follows doublestar conventions:
//   - `*` matches any sequence of non-separator characters.
//   - `**` matches any sequence including separators (recursive).
//   - `?` matches any single non-separator character.
//   - `[abc]` matches any character in the set.
//   - `[a-z]` matches any character in the range.
//
// Returns an error if the pattern syntax is invalid.
func glob(pattern string) ([]string, error) {
	matches, err := doublestar.FilepathGlob(pattern)
	if err != nil {
		return nil, fmt.Errorf("glob %q: %w", pattern, err)
	}

	return matches, nil
}

// containsGlobChars reports whether s contains glob metacharacters.
func containsGlobChars(s string) bool {
	return strings.ContainsAny(s, "*?[")
}

// expandPaths expands arguments containing glob patterns into a sorted list
// of file paths. Arguments without glob metacharacters are included as-is.
// Returns an error if a pattern is invalid.
func expandPaths(args ...string) ([]string, error) {
	var result []string

	for _, arg := range args {
		if !containsGlobChars(arg) {
			result = append(result, arg)

			continue
		}

		matches, err := glob(arg)
		if err != nil {
			return nil, err
		}

		result = append(result, matches...)
	}

	sort.Strings(result)

	return result, nil
}
