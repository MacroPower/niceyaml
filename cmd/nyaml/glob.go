package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// fileEntry holds a file path and its contents.
type fileEntry struct {
	path    string
	content []byte
}

// expandGlobs expands file arguments (which may contain glob patterns)
// into a sorted list of file entries with their contents.
// Files are sorted lexically by their base filename.
func expandGlobs(args []string) ([]fileEntry, error) {
	var paths []string

	for _, arg := range args {
		if containsGlobChars(arg) {
			matches, err := filepath.Glob(arg)
			if err != nil {
				return nil, fmt.Errorf("expand glob %q: %w", arg, err)
			}
			if len(matches) == 0 {
				return nil, fmt.Errorf("glob %q: no matching files", arg)
			}

			paths = append(paths, matches...)
		} else {
			paths = append(paths, arg)
		}
	}

	// Sort by base filename (lexical sort works for 000, 001, 002, etc.)
	sort.Slice(paths, func(i, j int) bool {
		return filepath.Base(paths[i]) < filepath.Base(paths[j])
	})

	entries := make([]fileEntry, 0, len(paths))
	for _, path := range paths {
		content, err := os.ReadFile(path) //nolint:gosec // User-provided file paths are intentional.
		if err != nil {
			return nil, fmt.Errorf("read file %s: %w", path, err)
		}

		entries = append(entries, fileEntry{path: path, content: content})
	}

	return entries, nil
}

// containsGlobChars reports whether the string contains glob metacharacters.
func containsGlobChars(s string) bool {
	return strings.ContainsAny(s, "*?[")
}
