package main

import (
	"errors"
	"fmt"
	"os"
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
// repeat a file or make a recursive pattern walk forever. The function
// still follows a symlink in the literal part of the pattern, before any
// metacharacter.
//
// Unlike [path/filepath.Glob], glob supports ** for recursive directory
// matching. The pattern syntax follows doublestar conventions:
//   - `*` matches any sequence of non-separator characters.
//   - `**` matches any sequence including separators (recursive).
//   - `?` matches any single non-separator character.
//   - `[abc]` matches any character in the set.
//   - `[a-z]` matches any character in the range.
//   - `{a,b}` matches any of the comma-separated alternatives.
//
// The function returns an error when the pattern syntax is invalid.
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
// file paths. The list keeps the order of the arguments, with the matches
// of each pattern sorted in its place. It names each file once. The order
// of two explicit files then decides which revision a diff treats as
// older.
//
// An argument that names a directory is an error wrapping
// [errIsDirectory], with or without glob metacharacters. An argument
// without metacharacters joins the list as-is. An argument with
// metacharacters that names an existing file is that file, even when it
// also matches other files as a pattern, so a file such as "cfg[1].yaml"
// or "report[2024.txt" stays reachable. Any other argument with
// metacharacters expands as a pattern. A pattern that matches no file is
// an error wrapping [errNoMatch], and an invalid pattern is its own error.
func expandPaths(args ...string) ([]string, error) {
	var (
		result    []string
		seenFiles []os.FileInfo
		seenNames = make(map[string]bool)
	)

	add := func(path string) {
		// One file named two ways, such as by a relative and an absolute
		// path, or by a symlink and its target, is one file, so add
		// compares the files the names reach rather than their text.
		// Cleaning a name as text would drop a ".." together with a
		// symlinked directory before it, while the OS steps up from the
		// directory the link leads to, so two different files could look
		// like one.
		info, err := os.Stat(path)
		if err == nil {
			seen := slices.ContainsFunc(seenFiles, func(other os.FileInfo) bool {
				return os.SameFile(other, info)
			})
			if seen {
				return
			}

			seenFiles = append(seenFiles, info)
			result = append(result, path)

			return
		}

		// A name with no file behind it counts once as typed, and the read
		// reports it.
		if seenNames[path] {
			return
		}

		seenNames[path] = true

		result = append(result, path)
	}

	for _, arg := range args {
		// A directory is no file to read, and the glob path excludes one
		// too. So a name that names a directory is an error, with or
		// without a metacharacter, rather than a pattern that could select
		// an unrelated file. A name that names an existing file is that
		// file, even when it also matches others as a pattern, so no
		// pattern match shadows the file the user named. That holds for a
		// name that is no valid pattern too, such as one with a stray
		// bracket. A name without metacharacters that names nothing passes
		// through to the read, which reports it. Any other name expands as
		// a pattern.
		info, err := os.Stat(arg)
		if err == nil && info.IsDir() {
			return nil, fmt.Errorf("%w: %q", errIsDirectory, arg)
		}

		if err == nil || !containsGlobChars(arg) {
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
