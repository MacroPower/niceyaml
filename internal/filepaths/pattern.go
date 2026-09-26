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
// Create instances with [NewPattern].
type Pattern struct {
	glob string
}

// NewPattern creates a [Pattern] from the given glob pattern string.
// Returns [ErrInvalidPattern] if the pattern syntax is invalid.
//
// NewPattern drops "." elements such as a leading "./", repeated
// separators, and a trailing separator from the pattern, as
// [Pattern.Match] does for the path, so those spellings do not change
// what the pattern matches.
func NewPattern(pattern string) (Pattern, error) {
	if !doublestar.ValidatePattern(pattern) {
		return Pattern{}, ErrInvalidPattern
	}

	return Pattern{glob: normalizePattern(pattern)}, nil
}

// Match reports whether the path matches the pattern.
//
// Match cleans the path and normalizes its separators to forward slashes
// before matching, so "./config.yaml" and "config.yaml" both match the
// root-only pattern "*.yaml".
func (p Pattern) Match(path string) bool {
	if p.glob == "" || path == "" {
		return false
	}

	// A pattern error is path dependent, since doublestar.ValidatePattern
	// accepts some patterns that Match rejects for a multi-segment path,
	// such as a "{" inside a character class. A pattern Match cannot
	// interpret matches nothing, which is what a false result says already.
	matched, _ := doublestar.Match(p.glob, normalizePath(path)) //nolint:errcheck // A pattern error means no match.

	return matched
}

// normalizePath returns path cleaned, with forward slashes as separators,
// which is the form the patterns match against. Cleaning drops a leading
// "./", collapses repeated separators, and resolves ".." elements, so the
// spelling of a path does not decide whether it matches.
func normalizePath(path string) string {
	return filepath.ToSlash(filepath.Clean(path))
}

// normalizePattern returns pattern without "." elements, repeated
// separators, or a trailing separator. A cleaned path carries none of
// them, so a pattern that kept them would match no path. A pattern left
// with no other element reads as "." or "/", as [filepath.Clean] reads
// it. It leaves ".." elements alone, since a glob element before a ".."
// could stand for any number of directories.
func normalizePattern(pattern string) string {
	if pattern == "" {
		return ""
	}

	elems := strings.Split(pattern, "/")
	kept := elems[:0]

	for _, elem := range elems {
		if elem != "" && elem != "." {
			kept = append(kept, elem)
		}
	}

	glob := strings.Join(kept, "/")

	switch {
	case strings.HasPrefix(pattern, "/"):
		return "/" + glob
	case glob == "":
		return "."
	default:
		return glob
	}
}

// MatchAny reports whether path matches any of the glob patterns, with
// the semantics VS Code and yaml-language-server give a schema fileMatch
// pattern. A pattern applies at any depth of the tree, so "*.yaml" matches
// "some/dir/config.yaml" and ".github/workflows/*.yml" matches
// "/repo/.github/workflows/ci.yml". Every pattern gets an implicit "**/"
// prefix unless it already has one, and drops a leading "/" first.
//
// MatchAny cleans the path and normalizes its separators to forward
// slashes before matching, as [Pattern.Match] does.
//
// # Pattern Validation
//
// MatchAny skips an invalid pattern without error, so a typo in a
// SchemaStore catalog entry does not break validation. To validate a
// pattern upfront, use [NewPattern] instead.
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
// depth of the tree. It normalizes the pattern as [NewPattern] does and
// drops a leading "/". A pattern that then starts with "**/" gets no
// second prefix.
func anyDepth(pattern string) string {
	pattern = strings.TrimPrefix(normalizePattern(pattern), "/")

	if strings.HasPrefix(pattern, "**/") {
		return pattern
	}

	return "**/" + pattern
}

// MaxBraceExpansions is the most patterns [ExpandBraces] produces for one
// pattern. Brace groups multiply out, so a pattern from an untrusted source
// could otherwise stand for millions of patterns.
const MaxBraceExpansions = 1024

// maxBraceWork is the most bytes of intermediate patterns [ExpandBraces]
// builds for one pattern. Each brace group rebuilds the pattern around
// it, so a long pattern with many groups could otherwise cost time and
// memory quadratic in its length while expanding to few patterns.
const maxBraceWork = 1 << 20

// ExpandBraces returns the patterns the brace alternatives of pattern
// stand for, so "*.{yml,yaml}" yields "*.yml" and "*.yaml", and nested
// groups multiply out. A backslash escapes the character after it. A
// pattern with no brace group, or with an unclosed one, yields itself, and
// so does a pattern that would expand to more than [MaxBraceExpansions]
// patterns. A pattern also yields itself when expanding it would take too
// much work, such as a very long pattern or one with very many brace
// groups.
func ExpandBraces(pattern string) []string {
	work := maxBraceWork

	expanded, ok := expandBraces(pattern, MaxBraceExpansions, &work)
	if !ok {
		return []string{pattern}
	}

	return expanded
}

// expandBraces is [ExpandBraces] with a budget of patterns left to
// produce and the bytes of intermediate patterns left to build, which
// work points to and which all calls of one expansion share. It reports
// false once the expansion outgrows either.
func expandBraces(pattern string, budget int, work *int) ([]string, bool) {
	open, closing := braceGroup(pattern)
	if open < 0 {
		return []string{pattern}, budget >= 1
	}

	prefix, suffix := pattern[:open], pattern[closing+1:]

	var expanded []string

	for _, alt := range splitAlternatives(pattern[open+1 : closing]) {
		// Counting one byte more than the pattern holds makes an empty
		// pattern cost something too, which also caps the recursion depth.
		*work -= len(prefix) + len(alt) + len(suffix) + 1
		if *work < 0 {
			return nil, false
		}

		more, ok := expandBraces(prefix+alt+suffix, budget-len(expanded), work)
		if !ok {
			return nil, false
		}

		expanded = append(expanded, more...)
	}

	return expanded, true
}

// braceGroup returns the indexes of the first unescaped "{" in pattern
// and of the "}" that closes it, or -1 for both when pattern holds no
// closed brace group. A brace inside a character class is part of the
// class, as [Pattern.Match] reads it.
func braceGroup(pattern string) (int, int) {
	open, depth := -1, 0

	for i := 0; i < len(pattern); i++ {
		switch pattern[i] {
		case '\\':
			i++ // Skip the escaped character.

		case '[':
			if end := classEnd(pattern, i); end >= 0 {
				i = end
			}

		case '{':
			if depth == 0 {
				open = i
			}

			depth++

		case '}':
			if depth == 0 {
				continue
			}

			depth--

			if depth == 0 {
				return open, i
			}
		}
	}

	return -1, -1
}

// splitAlternatives splits the body of a brace group on the commas at its
// top level, leaving commas inside nested groups, character classes, and
// escaped commas in place.
func splitAlternatives(body string) []string {
	var (
		alts  []string
		start int
		depth int
	)

	for i := 0; i < len(body); i++ {
		switch body[i] {
		case '\\':
			i++ // Skip the escaped character.

		case '[':
			if end := classEnd(body, i); end >= 0 {
				i = end
			}

		case '{':
			depth++

		case '}':
			depth--

		case ',':
			if depth == 0 {
				alts = append(alts, body[start:i])
				start = i + 1
			}
		}
	}

	return append(alts, body[start:])
}

// classEnd returns the index of the "]" that closes the character class
// opening at pattern[i], or -1 when nothing closes it, so the "[" reads
// as a literal. A "]" right after the "[", or after a leading "!" or "^",
// is a member of the class rather than its end.
func classEnd(pattern string, i int) int {
	j := i + 1

	if j < len(pattern) && (pattern[j] == '!' || pattern[j] == '^') {
		j++
	}

	if j < len(pattern) && pattern[j] == ']' {
		j++
	}

	for ; j < len(pattern); j++ {
		switch pattern[j] {
		case '\\':
			j++ // Skip the escaped character.
		case ']':
			return j
		}
	}

	return -1
}
