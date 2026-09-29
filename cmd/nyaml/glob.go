package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/bmatcuk/doublestar/v4"

	"go.jacobcolvin.com/niceyaml/internal/filepaths"
)

var (
	// The error for a glob pattern that matches no file.
	errNoMatch = errors.New("no files match pattern")

	// The error for an argument that names a directory.
	errIsDirectory = errors.New("is a directory")

	// The error for a glob pattern with a ".." element after a
	// metacharacter.
	errDotDotAfterMeta = errors.New(`".." element after a metacharacter`)
)

// glob returns the file paths matching pattern. It excludes directories
// and symlinks to directories from the matches.
//
// Wildcards do not follow symlinked directories, so a symlink loop cannot
// repeat a file or make a recursive pattern walk forever. The function
// still follows a symlink in the literal part of the pattern, before any
// metacharacter. The OS resolves a ".." there, so "link/../*.yaml" steps
// up from the directory the link leads to, as the shell does. A ".."
// element after a metacharacter is an error wrapping
// [errDotDotAfterMeta].
//
// Unlike [path/filepath.Glob], glob supports ** for recursive directory
// matching. The pattern syntax follows doublestar conventions:
//   - `*` matches any sequence of non-separator characters.
//   - `**` matches any sequence including separators (recursive).
//   - `?` matches any single non-separator character.
//   - `[abc]` matches any character in the set.
//   - `[a-z]` matches any character in the range.
//   - `{a,b}` matches any of the comma-separated alternatives. An
//     alternative may hold "." and ".." elements, as in
//     "app/{../shared,.}/*.yaml".
//
// The function returns an error when the pattern syntax is invalid.
func glob(pattern string) ([]string, error) {
	// Doublestar matches a brace group through io/fs, which rejects "."
	// and ".." elements, so an alternative holding one would match
	// nothing. Globbing each alternative on its own puts such elements in
	// its literal part, where the OS resolves them. Braces expand after
	// the separators turn into slashes, so a Windows separator does not
	// read as an escape.
	var matches []string

	for _, alt := range filepaths.ExpandBraces(filepath.ToSlash(pattern)) {
		altMatches, err := globAlternative(alt)
		if err != nil {
			return nil, fmt.Errorf("glob %q: %w", pattern, err)
		}

		matches = append(matches, altMatches...)
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
	// Two alternatives can match one path, so the path appears once.
	slices.Sort(files)

	return slices.Compact(files), nil
}

// globAlternative returns the paths matching pattern, a pattern with
// slash separators and no brace group left to expand. It may include
// directories.
//
// The literal part of the pattern, before the first metacharacter, stays
// as typed, both where the walk starts and in each match. Cleaning it as
// text, as [doublestar.FilepathGlob] does, would drop a ".." together with
// a symlinked directory before it, while the OS steps up from the
// directory the link leads to.
func globAlternative(pattern string) ([]string, error) {
	base, rest := doublestar.SplitPattern(pattern)

	// The rest starts at the element holding the first metacharacter, or
	// is the last element when the pattern holds none. A ".." as that
	// last element names a directory, which matches no file.
	if rest == ".." {
		return nil, nil
	}

	// The walk matches the rest through io/fs, which rejects empty, "."
	// and ".." elements. An empty or "." element inside the rest names
	// the directory before it, so it can go. The walk has no way to step
	// up out of a directory a wildcard matched, so a ".." is an error.
	elems := strings.Split(rest, "/")
	last := elems[len(elems)-1]
	kept := elems[:0]

	for _, elem := range elems {
		switch elem {
		case "", ".":
			continue
		case "..":
			return nil, errDotDotAfterMeta
		}

		kept = append(kept, elem)
	}

	// An empty or "." last element, as in "dir/*/", limits the pattern
	// to directories, which match no file.
	if last == "" || last == "." {
		return nil, nil
	}

	rest = strings.Join(kept, "/")

	// A bare volume name such as "C:" names the current directory of that
	// volume, while the pattern named its root.
	if vol := filepath.VolumeName(base); vol != "" && vol == base {
		base += "/"
	}

	matches, err := doublestar.Glob(
		os.DirFS(filepath.FromSlash(base)),
		rest,
		doublestar.WithFilesOnly(),
		doublestar.WithNoFollow(),
	)
	if err != nil {
		return nil, err
	}

	prefix := base
	switch {
	case base == ".":
		prefix = ""
	case !strings.HasSuffix(base, "/"):
		prefix = base + "/"
	}

	for i, match := range matches {
		matches[i] = filepath.FromSlash(prefix + match)
	}

	return matches, nil
}

// containsGlobChars reports whether s contains glob metacharacters.
func containsGlobChars(s string) bool {
	return strings.ContainsAny(s, "*?[{")
}

// fileKey groups the files [expandPaths] has seen by what one file shows
// through any of its names. Two files with one key can still differ, so
// the key only narrows the files [os.SameFile] compares.
type fileKey struct {
	Size    int64
	ModTime int64
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
		seenFiles = make(map[fileKey][]os.FileInfo)
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
		//
		// One file shows the same size and modification time through
		// every name, so add compares a file only with the files that
		// share both. That keeps a large glob from comparing each match
		// with every match before it.
		info, err := os.Stat(path)
		if err == nil {
			key := fileKey{Size: info.Size(), ModTime: info.ModTime().UnixNano()}

			seen := slices.ContainsFunc(seenFiles[key], func(other os.FileInfo) bool {
				return os.SameFile(other, info)
			})
			if seen {
				return
			}

			seenFiles[key] = append(seenFiles[key], info)
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
