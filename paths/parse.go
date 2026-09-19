package paths

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// ErrInvalidPath indicates a path expression that [Parse] cannot read.
var ErrInvalidPath = errors.New("invalid path")

// Parse parses a path expression into a [Path] targeting [PartNode].
//
// An expression starts with `$` for the document root, followed by any number
// of selectors:
//
//	.name     a mapping entry by key
//	.'name'   a key containing reserved characters, with `\` escaping `'`
//	.''       the empty key
//	..name    every mapping entry with that key, at any depth
//	..'name'  the same for a key containing reserved characters
//	[n]       a sequence element by 0-based index, written in decimal
//	          with no sign and no leading zero
//	[*]       every sequence element
//
// Use [Path.Key] on the result to target the key of a mapping entry rather
// than its value:
//
//	p, err := paths.Parse("$.metadata.name")
//	if err != nil {
//		return err
//	}
//	keyPath := p.Key()
//
// Returns an error wrapping [ErrInvalidPath] for a malformed expression.
func Parse(expr string) (Path, error) {
	segs, err := parseSegments(expr)
	if err != nil {
		return Path{}, fmt.Errorf("parse path %q: %w: %w", expr, ErrInvalidPath, err)
	}

	return Path{segments: segs}, nil
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

// parseSegments reads the selectors of expr after its `$` root.
func parseSegments(expr string) ([]segment, error) {
	if !strings.HasPrefix(expr, "$") {
		return nil, errors.New("expression must start with $")
	}

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
			seg, rest, err = parseRecursive(rest[2:])
		case strings.HasPrefix(rest, ".'"):
			seg, rest, err = parseQuoted(rest[2:], segmentChild)
		case strings.HasPrefix(rest, "."):
			seg, rest, err = parseChild(rest[1:])
		case strings.HasPrefix(rest, "["):
			seg, rest, err = parseIndex(rest[1:])
		default:
			return nil, fmt.Errorf("unexpected %q at %d", rest[0], len(expr)-len(rest))
		}

		if err != nil {
			return nil, err
		}

		segs = append(segs, seg)
	}

	return segs, nil
}

// parseName reads an unquoted selector name up to the next `.` or `[`.
func parseName(rest string) (string, string, error) {
	end := strings.IndexAny(rest, ".[")
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

// parseChild reads the name after `.`.
func parseChild(rest string) (segment, string, error) {
	name, remaining, err := parseName(rest)
	if err != nil {
		return segment{}, "", err
	}

	return segment{kind: segmentChild, name: name}, remaining, nil
}

// parseRecursive reads the name after `..`.
func parseRecursive(rest string) (segment, string, error) {
	name, remaining, err := parseName(rest)
	if err != nil {
		return segment{}, "", err
	}

	return segment{kind: segmentRecursive, name: name}, remaining, nil
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
	// as it was written.
	if !isCanonicalIndex(body) {
		return segment{}, "", fmt.Errorf("index %q is not a non-negative integer", body)
	}

	idx, err := strconv.Atoi(body)
	if err != nil {
		return segment{}, "", fmt.Errorf("index %q is not a non-negative integer", body)
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
