package filepaths

import (
	"cmp"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/bmatcuk/doublestar/v4"
)

var (
	// ErrInvalidPattern indicates that the pattern syntax is invalid.
	ErrInvalidPattern = errors.New("invalid glob pattern")

	// ErrBraceLimit indicates that the braces of a pattern expand to
	// more than [MaxBraceExpansions] patterns, or that expanding them
	// takes too much work.
	ErrBraceLimit = errors.New("braces expand past the limit")
)

// Pattern represents a validated glob pattern for file path matching.
//
// Create instances with [NewPattern].
type Pattern struct {
	// The normalized patterns ExpandBraces yields for the pattern.
	globs []string
}

// NewPattern creates a new [Pattern] from a glob pattern string. It
// returns [ErrInvalidPattern] when the pattern syntax is invalid. It also
// returns [ErrInvalidPattern], wrapping [ErrBraceLimit], when
// [ExpandBraces] reports that the braces expand past its limits. A
// cleaned path is never empty, so NewPattern returns [ErrInvalidPattern]
// for an empty pattern. It does the same for a pattern such as "{,}"
// whose braces expand only to empty patterns.
//
// NewPattern drops "." elements such as a leading "./", repeated
// separators, and a trailing separator from the pattern, as
// [Pattern.Match] does for the path. So those spellings do not change
// what the pattern matches. It also resolves a ".." that follows an
// element without glob syntax, so "configs/../*.yaml" matches "x.yaml"
// as "*.yaml" does. It normalizes each pattern [ExpandBraces] yields, so
// "{.,configs}/*.yaml" matches "values.yaml" as "./*.yaml" does. A "/"
// or "." inside a character class, as in "a[/.]b", stays part of the
// class. An escaped "/" or "." outside a class reads as the bare
// character, so "a\/b/../*.yaml" matches "a/x.yaml" as "a/b/../*.yaml"
// does, and "a\/**" matches "a" as "a/**" does.
//
// A cleaned path holds a ".." only at its start. So a pattern that keeps
// a ".." after a glob element such as "*" does not match any path, and
// NewPattern returns [ErrInvalidPattern] for it. A ".." after "**" stays
// valid when only ".." and "**" elements come before it in a relative
// pattern, because "**" can match zero directories. So "**/../x.yaml"
// matches "../x.yaml". NewPattern returns [ErrInvalidPattern] for a
// rooted pattern such as "/**/../x.yaml", and for a pattern with a name
// before the "**", such as "a/**/../x.yaml".
func NewPattern(pattern string) (Pattern, error) {
	if !doublestar.ValidatePattern(pattern) {
		return Pattern{}, ErrInvalidPattern
	}

	globs, err := expandPattern(pattern, normalizePattern)
	if err != nil {
		return Pattern{}, fmt.Errorf("%w: %w", ErrInvalidPattern, err)
	}

	if !slices.ContainsFunc(globs, func(glob string) bool { return glob != "" }) {
		return Pattern{}, fmt.Errorf("%w: an empty pattern matches no path", ErrInvalidPattern)
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
// a leading run of ".." and "**" elements of a relative pattern. A ".."
// of a cleaned path can pair with a pattern element only in that run. A
// "**" in that run can match zero directories, so a ".." after it can
// still pair with a ".." at the start of the path.
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
//
// A cleaned path that leads out of the directory it starts in opens with
// ".." elements. Each of them matches a ".." element of the pattern, or
// counts among the directories a "**" matches. The "*" and "?" wildcards
// and character classes match only the name of a file or a directory, so
// "*/x.yaml" matches "a/x.yaml" but not "../x.yaml".
func (p Pattern) Match(path string) bool {
	if path == "" {
		return false
	}

	clean := CleanPath(path)

	parents, rest := cutParents(clean)
	if parents == 0 {
		return matchAnyGlob(p.globs, clean)
	}

	return slices.ContainsFunc(p.globs, func(glob string) bool {
		return matchParents(glob, parents, rest)
	})
}

// cutParents returns the number of ".." elements that open path and the
// rest of path after them. The path must already be clean.
func cutParents(path string) (int, string) {
	parents := 0

	for {
		switch {
		case path == "..":
			return parents + 1, ""

		case strings.HasPrefix(path, "../"):
			parents++
			path = path[len("../"):]

		default:
			return parents, path
		}
	}
}

// matchParents reports whether glob matches the cleaned path made of
// parents ".." elements followed by rest, as [Pattern.Match] describes.
// Doublestar matches a ".." of the path with any element that matches
// its two dots. So matchParents pairs those elements with glob itself
// and hands doublestar the rest of both.
func matchParents(glob string, parents int, rest string) bool {
	elems := splitElements(glob)

	// Each entry says whether the elements before its index can match the
	// ".." elements read so far.
	at := make([]bool, len(elems)+1)
	at[0] = true

	for range parents {
		skipDoubleStars(elems, at)

		next := make([]bool, len(elems)+1)

		for i, elem := range elems {
			switch {
			case !at[i]:
			case elem == "..":
				next[i+1] = true
			case elem == "**":
				next[i] = true
			}
		}

		at = next
	}

	skipDoubleStars(elems, at)

	// A path of ".." elements alone leaves no element for glob to match.
	if rest == "" {
		return at[len(elems)]
	}

	for i := range elems {
		if at[i] && matchGlob(strings.Join(elems[i:], "/"), rest) {
			return true
		}
	}

	return false
}

// skipDoubleStars marks the index after each "**" of elems that at
// marks, because a "**" can match zero directories.
func skipDoubleStars(elems []string, at []bool) {
	for i, elem := range elems {
		if at[i] && elem == "**" {
			at[i+1] = true
		}
	}
}

// Relative reports whether any pattern the braces expand to is relative.
// A relative pattern does not open with a separator, and it matches a
// path that leads from some directory. So a caller that holds an
// absolute path must know that directory to match the two.
func (p Pattern) Relative() bool {
	return slices.ContainsFunc(p.globs, func(glob string) bool {
		return !strings.HasPrefix(glob, "/")
	})
}

// CleanPath returns path cleaned and with forward slashes as separators.
// The patterns match against paths in that form. Cleaning drops a
// leading "./", collapses repeated separators, and resolves ".."
// elements, so the spelling of a path does not decide whether it
// matches.
func CleanPath(path string) string {
	return filepath.ToSlash(filepath.Clean(path))
}

// expandPattern returns the patterns [ExpandBraces] yields for pattern,
// with rewrite applied to each.
func expandPattern(pattern string, rewrite func(string) string) ([]string, error) {
	globs, err := ExpandBraces(pattern)
	if err != nil {
		return nil, err
	}

	for i, glob := range globs {
		globs[i] = rewrite(glob)
	}

	return globs, nil
}

// normalizePattern returns pattern without "." elements, repeated
// separators, or a trailing separator. A cleaned path does not carry any
// of them, so a pattern that kept them does not match any path. A
// pattern left without any other element reads as "." or "/", as
// [filepath.Clean] reads it.
//
// It also resolves ".." elements as [filepath.Clean] does for a path. It
// drops a ".." and the element before it when that element is a plain
// name, and it drops a ".." immediately after the root of a rooted
// pattern. It keeps a leading "..". It also keeps a ".." after a glob
// element, because a glob cannot name the one directory the ".." leaves.
// [Pattern.Match] pairs a ".." of the path only with a ".." or a "**" of
// the pattern.
//
// It first writes each escaped "/" and "." bare, as
// [unescapeSeparatorsAndDots] does, so these rules see the separators
// and elements doublestar matches.
//
// It reads a brace group as plain text, so the caller must expand the
// braces first.
func normalizePattern(pattern string) string {
	if pattern == "" {
		return ""
	}

	pattern = unescapeSeparatorsAndDots(pattern)
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

// unescapeSeparatorsAndDots returns pattern with each escaped "/" and
// "." outside a character class written bare, and keeps every other
// escape. Doublestar matches an escaped "/" as a separator and an
// escaped "." as a dot, so the bare spelling matches the same paths
// except at the end of the pattern. There doublestar lets a bare "/**"
// or "**/" match zero directories, so "a/**" matches "a" but "a\/**"
// does not.
//
// Doublestar reads a "**" element as a single "*" when an escaped "/"
// ends it. So unescapeSeparatorsAndDots writes that element as "*"
// before the bare "/", because a "**" before a bare "/" matches any
// number of directories.
func unescapeSeparatorsAndDots(pattern string) string {
	var (
		out      = make([]byte, 0, len(pattern))
		elem     int // Where the current element starts in out.
		unclosed bool
	)

	for i := 0; i < len(pattern); i++ {
		switch pattern[i] {
		case '\\':
			if i+1 == len(pattern) {
				out = append(out, '\\') // Nothing follows to escape.

				break
			}

			i++

			switch pattern[i] {
			case '.':
				out = append(out, '.')

			case '/':
				if string(out[elem:]) == "**" {
					out = out[:len(out)-1]
				}

				out = append(out, '/')
				elem = len(out)

			default:
				out = append(out, '\\', pattern[i])
			}

		case '[':
			end := skipClass(pattern, i, &unclosed)
			out = append(out, pattern[i:end+1]...)
			i = end

		case '/':
			out = append(out, '/')
			elem = len(out)

		default:
			out = append(out, pattern[i])
		}
	}

	return string(out)
}

// isPlainName reports whether elem names one directory, as a path
// element other than ".." does. An element with an unescaped glob
// character or brace can match many names, so it does not count.
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
// "*.yml" and "!docker-compose.yml" together match every ".yml" file
// except "docker-compose.yml". A list that holds only exclusions does
// not match any path.
//
// The zero value does not hold any patterns, so it does not match any
// path.
//
// Create instances with [NewAnyDepthPatterns].
type AnyDepthPatterns struct {
	globs    []anyDepthGlob // Most specific first.
	excludes []string
}

// anyDepthGlob is a pattern ready for doublestar, with its specificity.
type anyDepthGlob struct {
	glob        string
	specificity int
}

// NewAnyDepthPatterns creates a new [AnyDepthPatterns] from the given
// glob patterns. It normalizes each pattern upfront, as [NewPattern]
// does, so matching many paths against the patterns does not repeat that
// work. It drops a pattern that holds only "!".
//
// It also drops each pattern for which [ExpandBraces] returns
// [ErrBraceLimit]. Matching such a pattern against a path takes time
// that doubles with each of its brace groups. A dropped pattern does not
// match any path, and a dropped exclusion does not exclude any path. For
// an exclusion, it hands [ExpandBraces] the text after the leading "!".
//
// NewAnyDepthPatterns keeps every other pattern, including invalid
// ones. [AnyDepthPatterns.SpecificityClean] skips each pattern it cannot
// interpret, so a typo in a SchemaStore catalog entry does not break
// validation. To validate a pattern upfront, use [NewPattern] instead.
func NewAnyDepthPatterns(patterns []string) AnyDepthPatterns {
	var p AnyDepthPatterns

	for _, pattern := range patterns {
		exclude, isExclude := strings.CutPrefix(pattern, "!")
		switch {
		case !isExclude:
			globs, err := expandPattern(pattern, anyDepth)
			if err != nil {
				continue
			}

			for _, glob := range globs {
				p.globs = append(p.globs, anyDepthGlob{glob: glob, specificity: specificity(glob)})
			}

		case exclude != "":
			excludes, err := expandPattern(exclude, anyDepth)
			if err != nil {
				continue
			}

			p.excludes = append(p.excludes, excludes...)
		}
	}

	// With the most specific glob first, the first glob that matches a
	// path is also the most specific one that does.
	slices.SortStableFunc(p.globs, func(a, b anyDepthGlob) int {
		return cmp.Compare(b.specificity, a.specificity)
	})

	return p
}

// SpecificityClean reports whether path matches any of the patterns but
// does not match any exclusion. It also returns the specificity of the
// most specific pattern that matches the path. The path must already be
// in the form [CleanPath] returns, so a caller matching one path against
// many pattern sets cleans it once.
//
// The specificity counts the characters a pattern requires literally in
// the names of a path. Every character counts except a separator, a
// "*", a "?", and a character class. So "**/.moon/tasks/**/*.yml" matches
// ".moon/tasks/node.yml" with a specificity of 14, and "**/tasks/*.yml"
// matches it with 9. Each brace alternative counts on its own.
func (p AnyDepthPatterns) SpecificityClean(path string) (int, bool) {
	for _, g := range p.globs {
		if matchGlob(g.glob, path) {
			if matchAnyGlob(p.excludes, path) {
				return 0, false
			}

			return g.specificity, true
		}
	}

	return 0, false
}

// matchAnyGlob reports whether path matches any of globs.
func matchAnyGlob(globs []string, path string) bool {
	return slices.ContainsFunc(globs, func(glob string) bool {
		return matchGlob(glob, path)
	})
}

// matchGlob reports whether path matches glob.
func matchGlob(glob, path string) bool {
	// Whether Match reports a pattern error depends on the path, since
	// doublestar.ValidatePattern accepts some patterns that Match rejects
	// for a multi-segment path, such as a "{" inside a character class.
	// A pattern that errors for this path matches nothing.
	matched, err := doublestar.Match(glob, path)

	return err == nil && matched
}

// specificity returns the number of characters glob requires literally
// in the names of a path. It counts every character but a separator, a
// "*", a "?", or a character class, and it counts an escaped character
// once. An unclosed "[" is a literal and counts. As with
// [normalizePattern], the caller must expand the braces first.
func specificity(glob string) int {
	var (
		n        int
		unclosed bool
	)

	for i := 0; i < len(glob); i++ {
		switch glob[i] {
		case '\\':
			i++ // Count the escaped character.
			n++

		case '[':
			end := skipClass(glob, i, &unclosed)
			if end == i {
				n++
			}

			i = end

		case '/', '*', '?':
			// Not required literally.

		default:
			// A multibyte character counts once, at its first byte.
			if utf8.RuneStart(glob[i]) {
				n++
			}
		}
	}

	return n
}

// anyDepth returns pattern with the "**/" prefix that lets it match at any
// depth of the tree. It normalizes the pattern and drops a leading "/". A
// pattern that then starts with "**/" does not get a second prefix. As
// with [normalizePattern], the caller must expand the braces first.
func anyDepth(pattern string) string {
	pattern = strings.TrimPrefix(normalizePattern(pattern), "/")

	if strings.HasPrefix(pattern, "**/") {
		return pattern
	}

	return "**/" + pattern
}

// MaxBraceExpansions is the most patterns [ExpandBraces] produces for one
// pattern. Brace groups multiply out, so without the limit a pattern from
// an untrusted source can expand to millions of patterns.
const MaxBraceExpansions = 1024

// maxBraceWork is the most bytes of intermediate patterns [ExpandBraces]
// builds for one pattern. Each brace group rebuilds the pattern around
// it. So without the limit, a long pattern with many groups can cost time
// and memory quadratic in its length even when it expands to few
// patterns.
const maxBraceWork = 1 << 20

// ExpandBraces returns the patterns that the brace alternatives of
// pattern stand for. For example, "*.{yml,yaml}" yields "*.yml" and
// "*.yaml". Nested groups multiply out. A backslash escapes the
// character after it. A pattern without a brace group, or with an
// unclosed one, yields itself.
//
// ExpandBraces returns [ErrBraceLimit] for a pattern that expands to more
// than [MaxBraceExpansions] patterns. It also returns [ErrBraceLimit] for
// a pattern that takes too much work to expand, such as a long pattern
// or one with many brace groups. A caller should not hand such a pattern
// to doublestar either. Doublestar expands the braces again on every
// match by backtracking, at a cost that can double with each brace group.
func ExpandBraces(pattern string) ([]string, error) {
	work := maxBraceWork

	expanded, ok := expandBraces(pattern, MaxBraceExpansions, &work)
	if !ok {
		return nil, ErrBraceLimit
	}

	return expanded, nil
}

// expandBraces is [ExpandBraces] with a budget of patterns left to
// produce. The work argument points to the bytes of intermediate
// patterns left to build, and all calls of one expansion share it. It
// reports false when the expansion outgrows either limit.
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
// and of the "}" that closes it, or -1 for both when pattern does not
// hold a closed brace group. A brace inside a character class is part
// of the class, as [Pattern.Match] reads it.
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
// opening at pattern[i]. When the class is unclosed, it returns i, so a
// scan that reads the "[" as a literal moves on by one byte. It sets
// unclosed when a class lacks a "]", and then returns i for every later
// "[" without a search. The scans that call it skip escapes as
// [classEnd] does. So after one unclosed "[", every later "[" is unclosed
// too, and a long run of them costs time linear in the pattern length.
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
// opening at pattern[i]. It returns -1 for an unclosed class, whose "["
// reads as a literal. A "]" immediately after the "[", or after a
// leading "!" or "^", is a member of the class rather than its end.
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
