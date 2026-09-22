package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
)

// errNoMatch reports a glob pattern that matches no file.
var errNoMatch = errors.New("no files match pattern")

// glob returns the file paths matching pattern. Directories are excluded
// from the matches.
//
// Unlike [path/filepath.Glob], this supports ** for recursive directory
// matching. The pattern syntax follows doublestar conventions:
//   - `*` matches any sequence of non-separator characters.
//   - `**` matches any sequence including separators (recursive).
//   - `?` matches any single non-separator character.
//   - `[abc]` matches any character in the set.
//   - `[a-z]` matches any character in the range.
//   - `{a,b}` matches any of the comma-separated alternatives.
//
// Returns an error if the pattern syntax is invalid.
func glob(pattern string) ([]string, error) {
	matches, err := doublestar.FilepathGlob(pattern, doublestar.WithFilesOnly())
	if err != nil {
		return nil, fmt.Errorf("glob %q: %w", pattern, err)
	}

	return matches, nil
}

// containsGlobChars reports whether s contains glob metacharacters.
func containsGlobChars(s string) bool {
	return strings.ContainsAny(s, "*?[{")
}

// expandPaths expands arguments containing glob patterns into a list of
// file paths. The list keeps the order of the arguments, with each pattern's
// matches sorted in its place, and names each file once, so the order of
// two explicit files decides which revision a diff treats as older.
// Arguments without glob metacharacters are included as-is.
//
// A pattern that matches no file, or that is no valid pattern, falls back
// to the argument itself when a path with that literal name exists, so a
// file such as "cfg[1].yaml" or "report[2024.txt" is still reachable.
// Otherwise a pattern that matches nothing is an error wrapping
// [errNoMatch], and an invalid pattern is its own error.
func expandPaths(args ...string) ([]string, error) {
	var result []string

	seen := make(map[string]bool)
	add := func(path string) {
		// One file named two ways, such as by a relative and an absolute
		// path, is one file.
		key, err := filepath.Abs(path)
		if err != nil {
			key = filepath.Clean(path)
		}

		if seen[key] {
			return
		}

		seen[key] = true

		result = append(result, path)
	}

	for _, arg := range args {
		if !containsGlobChars(arg) {
			add(arg)

			continue
		}

		matches, err := glob(arg)
		if err != nil || len(matches) == 0 {
			// The fallback admits files only, as the glob itself does. A
			// name that is no valid pattern, such as one with a stray
			// bracket, still names a file that exists.
			info, statErr := os.Stat(arg)
			if statErr != nil || info.IsDir() {
				if err != nil {
					return nil, err
				}

				return nil, fmt.Errorf("%w: %q", errNoMatch, arg)
			}

			matches = []string{arg}
		}

		for _, match := range matches {
			add(match)
		}
	}

	return result, nil
}
