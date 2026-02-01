package filepaths

import (
	"fmt"
	"sort"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
)

// Glob returns file paths matching the pattern.
//
// Unlike [path/filepath.Glob], this supports ** for recursive directory
// matching.
//
// The pattern syntax follows doublestar conventions:
//   - `*` matches any sequence of non-separator characters.
//   - `**` matches any sequence including separators (recursive).
//   - `?` matches any single non-separator character.
//   - `[abc]` matches any character in the set.
//   - `[a-z]` matches any character in the range.
//
// Returns an error if the pattern syntax is invalid.
func Glob(pattern string) ([]string, error) {
	matches, err := doublestar.FilepathGlob(pattern)
	if err != nil {
		return nil, fmt.Errorf("glob %q: %w", pattern, err)
	}

	return matches, nil
}

// ContainsGlobChars reports whether s contains glob metacharacters.
func ContainsGlobChars(s string) bool {
	return strings.ContainsAny(s, "*?[")
}

// Expand expands arguments containing glob patterns into a sorted list
// of file paths. Arguments without glob metacharacters are included as-is.
// Returns an error if a glob pattern matches no files.
func Expand(paths ...string) ([]string, error) {
	var result []string

	for _, path := range paths {
		if !ContainsGlobChars(path) {
			result = append(result, path)

			continue
		}

		matches, err := Glob(path)
		if err != nil {
			return nil, err
		}

		result = append(result, matches...)
	}

	sort.Strings(result)

	return result, nil
}
