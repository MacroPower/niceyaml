package paths

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"
)

var (
	// ErrInvalidPath indicates a path expression that [Parse] cannot read.
	ErrInvalidPath = errors.New("invalid path")

	// An index selector that is not a canonical non-negative decimal
	// integer produces errInvalidIndex.
	errInvalidIndex = errors.New("not a canonical non-negative integer")

	// An index selector too large for an int produces errIndexOutOfRange.
	errIndexOutOfRange = errors.New("out of range")
)

// Parse parses a path expression into a [Path].
//
// An expression starts with `$` for the document root or `@` for the
// current node, as in RFC 9535 JSONPath, followed by any number of
// selectors:
//
//   - `.name` selects a mapping entry by key.
//   - `.'name'` selects an entry whose key contains reserved characters.
//   - Empty quotes after `.` select the entry with the empty key.
//   - `.*` selects every entry of a mapping, and `.'*'` the entry with the
//     key `*`.
//   - `..name` selects every mapping entry with that key, at any depth.
//   - `..'name'` does the same for a key containing reserved characters.
//   - `..*` selects every node at any depth: the value of each mapping
//     entry and each sequence element. `..'*'` selects every entry with
//     the key `*`.
//   - `[n]` selects a sequence element by 0-based index.
//   - `[*]` selects every sequence element.
//   - `~` selects the key of the entry the selector before it picked.
//
// Inside quotes, `\` escapes the next character, so `\'` is a quote, `\\`
// is a backslash, and `\t` is a plain `t`. An index is a decimal with no
// sign and no leading zero. [Path.Key] appends the `~` selector. A `*` in
// an unquoted name is malformed.
//
// Returns an error wrapping [ErrInvalidPath] for a malformed expression.
func Parse(expr string) (Path, error) {
	absolute := strings.HasPrefix(expr, "$")
	if !absolute && !strings.HasPrefix(expr, "@") {
		return Path{}, fmt.Errorf("parse path %q: %w: expression must start with $ or @", expr, ErrInvalidPath)
	}

	segs, err := parseSegments(expr)
	if err != nil {
		return Path{}, fmt.Errorf("parse path %q: %w: %w", expr, ErrInvalidPath, err)
	}

	return Path{segments: segs, absolute: absolute}, nil
}

// MustParse is like [Parse] but panics if the expression is invalid.
//
// Use it for path expressions known to be valid at compile time.
func MustParse(expr string) Path {
	p, err := Parse(expr)
	if err != nil {
		panic(err)
	}

	return p
}

// parseSegments reads the selectors of expr after the `$` or `@` that
// [Parse] checked.
func parseSegments(expr string) ([]segment, error) {
	var segs []segment

	rest := expr[1:]

	for rest != "" {
		var (
			seg segment
			err error
		)

		switch {
		case strings.HasPrefix(rest, "..'"):
			seg, rest, err = parseQuoted(rest[3:], segmentRecursive)
		case strings.HasPrefix(rest, ".."):
			seg, rest, err = parseNameOrStar(rest[2:], segmentRecursiveAll, segmentRecursive)
		case strings.HasPrefix(rest, ".'"):
			seg, rest, err = parseQuoted(rest[2:], segmentChild)
		case strings.HasPrefix(rest, "."):
			seg, rest, err = parseNameOrStar(rest[1:], segmentChildAll, segmentChild)
		case strings.HasPrefix(rest, "["):
			seg, rest, err = parseIndex(rest[1:])
		case strings.HasPrefix(rest, "~"):
			seg, rest = segment{kind: segmentKey}, rest[1:]
		default:
			return nil, unexpectedError(rest, len(expr)-len(rest))
		}

		if err != nil {
			return nil, err
		}

		segs = append(segs, seg)
	}

	return segs, nil
}

// unexpectedError reports the character that starts rest, found at byte
// offset in the expression. A byte that does not start valid UTF-8 prints
// as an escape such as "\xff".
func unexpectedError(rest string, offset int) error {
	r, size := utf8.DecodeRuneInString(rest)
	if r == utf8.RuneError && size == 1 {
		return fmt.Errorf("unexpected %q at %d", rest[:1], offset)
	}

	return fmt.Errorf("unexpected %q at %d", r, offset)
}

// parseName reads an unquoted selector name up to the next `.`, `[`, or
// `~`.
func parseName(rest string) (string, string, error) {
	end := strings.IndexAny(rest, ".[~")
	if end < 0 {
		end = len(rest)
	}

	name := rest[:end]
	if name == "" {
		return "", "", errors.New("empty selector")
	}

	if i := strings.IndexAny(name, "$*]"); i >= 0 {
		return "", "", fmt.Errorf("unexpected %q in selector %q", name[i], name)
	}

	return name, rest[end:], nil
}

// parseNameOrStar reads the selector after `.` or `..`: the `*` of a
// wildcard selector of kind all, such as `.*`, or the unquoted name of a
// selector of kind named, such as `.name`. A `*` is the whole selector, so
// a name that goes on after it is malformed, as any other unquoted name
// with a `*` is.
func parseNameOrStar(rest string, all, named segmentKind) (segment, string, error) {
	after, ok := strings.CutPrefix(rest, "*")
	if ok && (after == "" || strings.IndexByte(".[~", after[0]) >= 0) {
		return segment{kind: all}, after, nil
	}

	return parseUnquoted(rest, named)
}

// parseUnquoted reads an unquoted name after `.` or `..` as a selector of
// the given kind.
func parseUnquoted(rest string, kind segmentKind) (segment, string, error) {
	name, remaining, err := parseName(rest)
	if err != nil {
		return segment{}, "", err
	}

	return segment{kind: kind, name: name}, remaining, nil
}

// parseQuoted reads a single-quoted name after `.'` or `..'` as a selector
// of the given kind, where `\` escapes the next character. The quotes may
// hold nothing, which names the empty key.
func parseQuoted(rest string, kind segmentKind) (segment, string, error) {
	var sb strings.Builder

	for i := 0; i < len(rest); i++ {
		switch rest[i] {
		case '\\':
			if i+1 >= len(rest) {
				return segment{}, "", errors.New("unterminated escape in quoted selector")
			}

			i++

			sb.WriteByte(rest[i])

		case '\'':
			return segment{kind: kind, name: sb.String()}, rest[i+1:], nil

		default:
			sb.WriteByte(rest[i])
		}
	}

	return segment{}, "", errors.New("unterminated quoted selector")
}

// parseIndex reads `n]` or `*]` after `[`.
func parseIndex(rest string) (segment, string, error) {
	body, remaining, ok := strings.Cut(rest, "]")
	if !ok {
		return segment{}, "", errors.New("unterminated index")
	}

	if body == "*" {
		return segment{kind: segmentIndexAll}, remaining, nil
	}

	// An index is a canonical decimal: digits only, with no sign and no
	// leading zero, so every index has one spelling and a parsed path prints
	// as the caller wrote it.
	if !isCanonicalIndex(body) {
		return segment{}, "", fmt.Errorf("index %q: %w", body, errInvalidIndex)
	}

	// A canonical index is valid syntax, so Atoi fails only when the
	// number does not fit in an int.
	idx, err := strconv.Atoi(body)
	if err != nil {
		return segment{}, "", fmt.Errorf("index %q: %w", body, errIndexOutOfRange)
	}

	return segment{kind: segmentIndex, index: idx}, remaining, nil
}

// isCanonicalIndex reports whether s is a non-negative decimal integer as
// [Path.String] writes one: at least one digit, and no leading zero unless
// the number is zero itself.
func isCanonicalIndex(s string) bool {
	if s == "" || (len(s) > 1 && s[0] == '0') {
		return false
	}

	for i := range len(s) {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}

	return true
}
