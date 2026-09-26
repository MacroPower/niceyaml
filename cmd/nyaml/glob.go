package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
)

var (
	// The error for a glob pattern that matches no file.
	errNoMatch = errors.New("no files match pattern")

	// The error for an argument that names a directory.
	errIsDirectory = errors.New("is a directory")
)

// glob returns the file paths matching pattern. It excludes directories
// and symlinks to directories from the matches.
//
// Wildcards do not follow symlinked directories, so a symlink loop cannot
// repeat a file or make a recursive pattern walk forever. A symlink in the
// literal part of the pattern, before any metacharacter, is still
// followed.
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
	matches, err := doublestar.FilepathGlob(
		pattern,
		doublestar.WithFilesOnly(),
		doublestar.WithNoFollow(),
	)
	if err != nil {
		return nil, fmt.Errorf("glob %q: %w", pattern, err)
	}

	// WithNoFollow reports a symlink to a directory as a file, so drop it.
	files := matches[:0]
	for _, match := range matches {
		info, err := os.Stat(match)
		if err == nil && info.IsDir() {
			continue
		}

		files = append(files, match)
	}

	// A recursive pattern reports its matches in directory walk order,
	// which puts every file of a directory ahead of its subdirectories.
	slices.Sort(files)

	return files, nil
}

// containsGlobChars reports whether s contains glob metacharacters.
func containsGlobChars(s string) bool {
	return strings.ContainsAny(s, "*?[{")
}

// expandPaths expands arguments containing glob patterns into a list of
// file paths. The list keeps the order of the arguments, with each pattern's
// matches sorted in its place, and names each file once, so the order of
// two explicit files decides which revision a diff treats as older. An
// argument without glob metacharacters joins the list as-is, and one that
// names a directory is an error wrapping [errIsDirectory].
//
// An argument with glob metacharacters that names an existing file is that
// file, even when it also matches other files as a pattern, so a file such
// as "cfg[1].yaml" or "report[2024.txt" is still reachable. One that names
// an existing directory is an error wrapping [errIsDirectory]. Any other
// argument with metacharacters expands as a pattern. A pattern that matches
// no file is an error wrapping [errNoMatch], and an invalid pattern is its
// own error.
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
			// A directory is no file to read, as the glob path excludes
			// one; a name that does not exist passes through to the read,
			// which reports it.
			info, err := os.Stat(arg)
			if err == nil && info.IsDir() {
				return nil, fmt.Errorf("%w: %q", errIsDirectory, arg)
			}

			add(arg)

			continue
		}

		// A name that holds a metacharacter and names a file that exists
		// is that file, even when it also matches others as a pattern, so
		// the file the user named is never shadowed. A name that is no
		// valid pattern, such as one with a stray bracket, names a file
		// the same way. A name that names a directory is an error, as
		// without a metacharacter, rather than a pattern that could
		// select an unrelated file.
		info, statErr := os.Stat(arg)
		if statErr == nil {
			if info.IsDir() {
				return nil, fmt.Errorf("%w: %q", errIsDirectory, arg)
			}

			add(arg)

			continue
		}

		matches, err := glob(arg)
		if err != nil {
			return nil, err
		}

		if len(matches) == 0 {
			return nil, fmt.Errorf("%w: %q", errNoMatch, arg)
		}

		for _, match := range matches {
			add(match)
		}
	}

	return result, nil
}
