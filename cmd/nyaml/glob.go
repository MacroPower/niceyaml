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
//   - `\` escapes the character after it, so `\*` matches a literal `*`
//     and `{a\,b,c}` matches "a,b" or "c". On Windows, `\` is a
//     separator instead.
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
// The literal part of the pattern, before the first metacharacter, loses
// its escapes and otherwise stays as typed, both where the walk starts
// and in each match. Cleaning it as text, as [doublestar.FilepathGlob]
// does, would drop a ".." together with a symlinked directory before it,
// while the OS steps up from the directory the link leads to.
func globAlternative(pattern string) ([]string, error) {
	base, rest, literal := splitLiteral(pattern)

	// A bare volume name such as "C:" names the current directory of that
	// volume, while the pattern named its root.
	if vol := filepath.VolumeName(base); vol != "" && vol == base {
		base += "/"
	}

	// A pattern without a literal part walks from ".", and its matches get
	// no prefix. A typed "./" stays in each match, as the shell keeps it.
	prefix := base
	switch {
	case rest == pattern:
		prefix = ""
	case !strings.HasSuffix(base, "/"):
		prefix = base + "/"
	}

	// Doublestar removes only the escapes of metacharacters from a
	// pattern without one, so it would look for "a\,b.yaml" rather than
	// "a,b.yaml". Such a pattern names one path, so the function removes
	// every escape and looks that path up itself.
	if literal {
		path := filepath.FromSlash(prefix + unescape(rest))

		_, err := os.Stat(path)
		if err == nil {
			return []string{path}, nil
		}

		// Doublestar reads a path it cannot look up as no match too.
		return nil, nil
	}

	// The rest starts at the element holding the first metacharacter. The
	// walk matches it through io/fs, which rejects empty, "." and ".."
	// elements. An empty or "." element inside the rest names the
	// directory before it, so it can go. The walk has no way to step up
	// out of a directory a wildcard matched, so a ".." is an error.
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

	matches, err := doublestar.Glob(
		os.DirFS(filepath.FromSlash(base)),
		rest,
		doublestar.WithFilesOnly(),
		doublestar.WithNoFollow(),
	)
	if err != nil {
		return nil, err
	}

	for i, match := range matches {
		matches[i] = filepath.FromSlash(prefix + match)
	}

	return matches, nil
}

// splitLiteral splits pattern at the last "/" before its first unescaped
// metacharacter, as [doublestar.SplitPattern] does, and reports whether
// pattern holds no metacharacter. It returns the part before that "/"
// with every escape removed, which is "." when no "/" comes first and "/"
// for a lone leading one. It returns the part after that "/" as typed.
//
// [doublestar.SplitPattern] removes only the escapes of metacharacters,
// so it keeps the backslash of an escaped comma, which
// [filepaths.ExpandBraces] leaves in a brace alternative.
func splitLiteral(pattern string) (string, string, bool) {
	split := -1
	literal := true

	for i := 0; i < len(pattern) && literal; i++ {
		switch pattern[i] {
		case '\\':
			i++ // Skip the escaped character.
		case '/':
			split = i
		case '*', '?', '[', '{':
			literal = false
		}
	}

	switch split {
	case -1:
		return ".", pattern, literal
	case 0:
		return "/", pattern[1:], literal
	}

	return unescape(pattern[:split]), pattern[split+1:], literal
}

// unescape returns s with each backslash escape replaced by the
// character it escapes. A trailing backslash stays.
func unescape(s string) string {
	var b strings.Builder

	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) {
			i++
		}

		b.WriteByte(s[i])
	}

	return b.String()
}

// containsGlobChars reports whether s contains glob metacharacters.
func containsGlobChars(s string) bool {
	return strings.ContainsAny(s, "*?[{")
}

// fileID identifies a file by its device and inode numbers, in that
// order. One file shows the same numbers through every name, even while
// a writer changes its content.
type fileID [2]uint64

// fileSet holds the files [expandPaths] has added. The zero value is an
// empty set.
type fileSet struct {
	ids   map[fileID]bool
	infos []os.FileInfo
}

// add adds the file that info describes and reports whether the set
// lacked it.
func (s *fileSet) add(info os.FileInfo) bool {
	id, ok := fileIdentity(info)
	if !ok {
		// Without the numbers, add compares the file with every file the
		// set holds.
		seen := slices.ContainsFunc(s.infos, func(other os.FileInfo) bool {
			return os.SameFile(other, info)
		})
		if seen {
			return false
		}

		s.infos = append(s.infos, info)

		return true
	}

	if s.ids[id] {
		return false
	}

	if s.ids == nil {
		s.ids = make(map[fileID]bool)
	}

	s.ids[id] = true

	return true
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
		seenFiles fileSet
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
			if seenFiles.add(info) {
				result = append(result, path)
			}

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
