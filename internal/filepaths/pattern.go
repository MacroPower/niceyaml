package filepaths

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
)

// ErrInvalidPattern indicates the pattern syntax is invalid.
var ErrInvalidPattern = errors.New("invalid glob pattern")

// Pattern represents a validated glob pattern for file path matching.
//
// Create instances with [NewPattern].
type Pattern struct {
	// The normalized patterns ExpandBraces yields for the pattern.
	globs []string
}

// NewPattern creates a new [Pattern] from a glob pattern string. It
// returns [ErrInvalidPattern] when the pattern syntax is invalid. It also
// returns [ErrInvalidPattern] when the braces would expand to more than
// [MaxBraceExpansions] patterns or take too much work to expand, the two
// limits at which [ExpandBraces] gives up.
//
// NewPattern drops "." elements such as a leading "./", repeated
// separators, and a trailing separator from the pattern, as
// [Pattern.Match] does for the path, so those spellings do not change
// what the pattern matches. It also resolves a ".." that follows an
// element without glob syntax, so "configs/../*.yaml" matches "x.yaml"
// as "*.yaml" does. It normalizes each pattern [ExpandBraces] yields, so
// "{.,configs}/*.yaml" matches "values.yaml" as "./*.yaml" does. A "/"
// or "." inside a character class, as in "a[/.]b", stays part of the
// class.
//
// A cleaned path holds a ".." only at its start, so NewPattern returns
// [ErrInvalidPattern] for a pattern that keeps a ".." after a glob
// element such as "*", which could match no path. A ".." after "**"
// stays valid when only ".." and "**" elements come before it in a
// relative pattern, since "**" can match no directory at all, so
// "**/../x.yaml" matches "../x.yaml". A rooted pattern such as
// "/**/../x.yaml", or one with a name before the "**" such as
// "a/**/../x.yaml", returns [ErrInvalidPattern].
func NewPattern(pattern string) (Pattern, error) {
	if !doublestar.ValidatePattern(pattern) {
		return Pattern{}, ErrInvalidPattern
	}

	globs, ok := expandPattern(pattern, normalizePattern)
	if !ok {
		return Pattern{}, fmt.Errorf("%w: braces expand past the limit", ErrInvalidPattern)
	}

	for _, glob := range globs {
		if !parentsMatchable(glob) {
			return Pattern{}, fmt.Errorf("%w: '..' after a glob element never matches a cleaned path",
				ErrInvalidPattern)
		}
	}

	return Pattern{globs: globs}, nil
}

// parentsMatchable reports whether every ".." element of glob sits in
// a leading run of ".." and "**" elements of a relative pattern, the
// only place a ".." of a cleaned path can line up with. A "**" in that
// run can match no directory, which leaves the ".." at the start of the
// path.
func parentsMatchable(glob string) bool {
	leading := !strings.HasPrefix(glob, "/")

	for _, elem := range splitElements(glob) {
		switch elem {
		case "", "**":
			continue
		case "..":
			if !leading {
				return false
			}

		default:
			leading = false
		}
	}

	return true
}

// Match reports whether the path matches the pattern.
//
// Match cleans the path and normalizes its separators to forward slashes
// before matching, so "./config.yaml" and "config.yaml" both match the
// root-only pattern "*.yaml".
func (p Pattern) Match(path string) bool {
	if path == "" {
		return false
	}

	return matchAnyGlob(p.globs, CleanPath(path))
}

// CleanPath returns path cleaned, with forward slashes as separators,
// which is the form the patterns match against. Cleaning drops a leading
// "./", collapses repeated separators, and resolves ".." elements, so the
// spelling of a path does not decide whether it matches.
func CleanPath(path string) string {
	return filepath.ToSlash(filepath.Clean(path))
}

// expandPattern expands the braces of pattern as [ExpandBraces] does and
// applies rewrite to each pattern it yields. It reports false when the
// braces expand past the budget, where [ExpandBraces] would yield the
// pattern itself.
// Doublestar would then expand the braces again on every match, by
// backtracking, at a cost that can double with each brace group.
func expandPattern(pattern string, rewrite func(string) string) ([]string, bool) {
	work := maxBraceWork

	globs, ok := expandBraces(pattern, MaxBraceExpansions, &work)
	if !ok {
		return nil, false
	}

	for i, glob := range globs {
		globs[i] = rewrite(glob)
	}

	return globs, true
}

// normalizePattern returns pattern without "." elements, repeated
// separators, or a trailing separator. A cleaned path carries none of
// them, so a pattern that kept them would match no path. A pattern left
// with no other element reads as "." or "/", as [filepath.Clean] reads
// it.
//
// It also resolves ".." elements as [filepath.Clean] does for a path. It
// drops a ".." and the element before it when that element is a plain
// name, and it drops a ".." right after the root of a rooted pattern. It
// keeps a leading ".." and a ".." after a glob element. A glob cannot
// name the one directory the ".." leaves, and doublestar matches a ".."
// only against a literal ".." in the path.
//
// It reads a brace group as plain text, so a caller expands the braces
// first.
func normalizePattern(pattern string) string {
	if pattern == "" {
		return ""
	}

	rooted := strings.HasPrefix(pattern, "/")

	var kept []string

	for _, elem := range splitElements(pattern) {
		switch {
		case elem == "" || elem == ".":
			continue

		case elem == ".." && len(kept) == 0 && rooted:
			continue

		case elem == ".." && len(kept) > 0 && isPlainName(kept[len(kept)-1]):
			kept = kept[:len(kept)-1]

		default:
			kept = append(kept, elem)
		}
	}

	glob := strings.Join(kept, "/")

	switch {
	case rooted:
		return "/" + glob
	case glob == "":
		return "."
	default:
		return glob
	}
}

// isPlainName reports whether elem names one directory, as a path
// element other than ".." does. An element with an unescaped glob
// character or brace can stand for many names, so it does not count.
func isPlainName(elem string) bool {
	if elem == ".." {
		return false
	}

	for i := 0; i < len(elem); i++ {
		switch elem[i] {
		case '\\':
			i++ // Skip the escaped character.
		case '*', '?', '[', '{':
			return false
		}
	}

	return true
}

// splitElements splits pattern on its separators. An escaped "/" and a
// "/" inside a character class belong to the element around them.
func splitElements(pattern string) []string {
	var (
		elems    []string
		start    int
		unclosed bool
	)

	for i := 0; i < len(pattern); i++ {
		switch pattern[i] {
		case '\\':
			i++ // Skip the escaped character.

		case '[':
			i = skipClass(pattern, i, &unclosed)

		case '/':
			elems = append(elems, pattern[start:i])
			start = i + 1
		}
	}

	return append(elems, pattern[start:])
}

// AnyDepthPatterns holds glob patterns prepared for matching at any depth
// of the tree, with the semantics VS Code and yaml-language-server give a
// schema fileMatch pattern.
//
// A pattern applies at any depth, so "*.yaml" matches
// "some/dir/config.yaml" and ".github/workflows/*.yml" matches
// "/repo/.github/workflows/ci.yml". [NewAnyDepthPatterns] drops a leading
// "/" from each pattern and then adds an implicit "**/" prefix unless the
// pattern already has one.
//
// A pattern with a leading "!" excludes the paths the rest of it matches,
// wherever it sits in the list, as yaml-language-server reads it. So
// "*.yml" and "!docker-compose.yml" together match every YAML file but
// "docker-compose.yml", and a list that holds only exclusions matches
// nothing.
//
// The zero value holds no patterns and matches no path.
//
// Create instances with [NewAnyDepthPatterns].
type AnyDepthPatterns struct {
	globs    []string
	excludes []string
}

// NewAnyDepthPatterns creates a new [AnyDepthPatterns] from the given
// glob patterns. It normalizes each pattern as [NewPattern] does, once,
// so matching many paths against the patterns repeats none of that
// work. It drops a "!" that has nothing after it.
//
// It also drops a pattern whose braces [NewPattern] would reject for
// expanding past the limit, so that pattern matches nothing and an
// exclusion excludes nothing. Matching it could otherwise take time
// that doubles with each of its brace groups, for every path.
//
// NewAnyDepthPatterns keeps every other pattern, the invalid ones
// included, and [AnyDepthPatterns.MatchClean] skips a pattern it cannot
// interpret, so a typo in a SchemaStore catalog entry does not break
// validation. To validate a pattern upfront, use [NewPattern] instead.
func NewAnyDepthPatterns(patterns []string) AnyDepthPatterns {
	var p AnyDepthPatterns

	for _, pattern := range patterns {
		exclude, isExclude := strings.CutPrefix(pattern, "!")
		switch {
		case !isExclude:
			if globs, ok := expandPattern(pattern, anyDepth); ok {
				p.globs = append(p.globs, globs...)
			}

		case exclude != "":
			if excludes, ok := expandPattern(exclude, anyDepth); ok {
				p.excludes = append(p.excludes, excludes...)
			}
		}
	}

	return p
}

// MatchClean reports whether path matches any of the patterns and none
// of the exclusions. The path must already be in the form [CleanPath]
// returns, so a caller matching one path against many pattern sets
// cleans it once.
func (p AnyDepthPatterns) MatchClean(path string) bool {
	return matchAnyGlob(p.globs, path) && !matchAnyGlob(p.excludes, path)
}

// matchAnyGlob reports whether path matches any of globs.
func matchAnyGlob(globs []string, path string) bool {
	for _, glob := range globs {
		// Whether Match reports a pattern error depends on the path, since
		// doublestar.ValidatePattern accepts some patterns that Match
		// rejects for a multi-segment path, such as a "{" inside a
		// character class. The loop skips a pattern that errors for this
		// path alone.
		matched, err := doublestar.Match(glob, path)
		if err == nil && matched {
			return true
		}
	}

	return false
}

// anyDepth returns pattern with the "**/" prefix that lets it match at any
// depth of the tree. It normalizes the pattern and drops a leading "/". A
// pattern that then starts with "**/" gets no second prefix. As with
// [normalizePattern], a caller expands the braces first.
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
	var unclosed bool

	open, depth := -1, 0

	for i := 0; i < len(pattern); i++ {
		switch pattern[i] {
		case '\\':
			i++ // Skip the escaped character.

		case '[':
			i = skipClass(pattern, i, &unclosed)

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
// top level. A comma inside a nested group or a character class, and an
// escaped comma, stays in its alternative.
func splitAlternatives(body string) []string {
	var (
		alts     []string
		start    int
		depth    int
		unclosed bool
	)

	for i := 0; i < len(body); i++ {
		switch body[i] {
		case '\\':
			i++ // Skip the escaped character.

		case '[':
			i = skipClass(body, i, &unclosed)

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

// skipClass returns the index of the "]" that closes the character class
// opening at pattern[i], or i when nothing closes it, so a scan that
// reads the "[" as a literal moves on by one byte. It sets unclosed once
// a class has no "]" and then returns i for every later "[" without
// looking for one. The scans that call it skip escapes as [classEnd]
// does, so after one "[" that nothing closes, nothing closes a later "["
// either, and a long run of them costs time linear in the pattern
// length.
func skipClass(pattern string, i int, unclosed *bool) int {
	if *unclosed {
		return i
	}

	end := classEnd(pattern, i)
	if end < 0 {
		*unclosed = true

		return i
	}

	return end
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
