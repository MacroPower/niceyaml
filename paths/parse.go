package paths

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode"
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

	// A `\u` in a quoted selector with fewer than four hexadecimal digits
	// after it produces errUnicodeEscapeDigits.
	errUnicodeEscapeDigits = errors.New(`\u escape in quoted selector needs four hexadecimal digits`)
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
//   - `~` selects the key of the entry the selector before it picked. It
//     selects nothing after a sequence element and on the root, which do
//     not have a key. No selector after a `~` selects anything, and a `~`
//     cannot follow another `~`.
//
// Inside quotes, `\` escapes the next character, so `\'` is a quote and
// `\\` is a backslash. Parse reads the escapes that [Path.String] writes
// for a control character or a bidirectional embedding, override, or
// isolate. The escape `\n` is a line feed, `\r` is a carriage return, and
// `\t` is a tab. The escape `\u` with four hexadecimal digits is the
// character with that code, such as `\u001b` for the escape character.
// Any other character after `\` stands for itself.
//
// An unquoted name ends at the next `.`, `[`, or `~`, and it must be a
// name that [Path.String] writes without quotes. Parse thus rejects an
// unquoted name that holds whitespace, a control character, a
// bidirectional embedding, override, or isolate, or one of `:`, `'`, `"`,
// `\`, `$`, `*`, and `]`, and the error shows the selector with the
// quotes. An expression with a space or a line feed at its end is
// malformed, and so is a name in double quotes. Quotes around a name that
// does not need them are valid, so `$.'a'` parses to the path that prints
// as `$.a`.
//
// An index is a decimal without a sign or a leading zero. Brackets hold
// an index or `*` and never a quoted name, so the JSONPath form `['name']`
// is malformed. [Path.Key] appends the `~` selector.
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
// Use it for path expressions that you know are valid at compile time.
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
		case strings.HasPrefix(rest, "~~"):
			return nil, fmt.Errorf("~~ at %d; one ~ selects the key", len(expr)-len(rest))
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

// unexpectedError returns an error that names the character that starts
// rest and its byte offset in the expression. A byte that does not start
// valid UTF-8 prints as an escape such as "\xff".
func unexpectedError(rest string, offset int) error {
	r, size := utf8.DecodeRuneInString(rest)
	if r == utf8.RuneError && size == 1 {
		return fmt.Errorf("unexpected %q at %d", rest[:1], offset)
	}

	return fmt.Errorf("unexpected %q at %d", r, offset)
}

// parseNameOrStar reads the selector after `.` or `..`: the `*` of a
// wildcard selector of kind all, such as `.*`, or the unquoted name of a
// selector of kind named, such as `.name`. A `*` is the whole selector, so
// a name that continues after it is malformed, as is any other unquoted
// name with a `*`. The error for text after a `*` shows the wildcard
// selector and the quoted name, because the caller meant one of the two.
func parseNameOrStar(rest string, all, named segmentKind) (segment, string, error) {
	after, ok := strings.CutPrefix(rest, "*")
	if ok && (after == "" || strings.IndexByte(".[~", after[0]) >= 0) {
		return segment{kind: all}, after, nil
	}

	return parseUnquoted(rest, named)
}

// parseUnquoted reads an unquoted name after `.` or `..` as a selector of
// the given kind. The name runs to the next `.`, `[`, or `~`, and it must
// be one that [Path.String] writes without quotes. An unquoted name thus
// has one spelling, and whitespace or the quoting of another path syntax
// does not parse into a name that matches no key.
func parseUnquoted(rest string, kind segmentKind) (segment, string, error) {
	// A name in double quotes can hold a `.`, so read it before the name
	// ends at one.
	if name, ok := doubleQuoted(rest); ok {
		return segment{}, "", fmt.Errorf(`unexpected '"' in selector; quote a name with single quotes, as in %s`,
			quotedSelector(kind, name))
	}

	end := strings.IndexAny(rest, ".[~")
	if end < 0 {
		end = len(rest)
	}

	seg := segment{kind: kind, name: rest[:end]}
	if seg.name == "" {
		// A `[` with a quote after it is the bracket-quoted name of
		// another path syntax, as in `..['name']`.
		if after, ok := strings.CutPrefix(rest, "["); ok {
			err := bracketQuotedError(after, kind)
			if err != nil {
				return segment{}, "", err
			}
		}

		return segment{}, "", errors.New("empty selector")
	}

	if quoteName(seg.name) != seg.name {
		return segment{}, "", unquotedNameError(seg)
	}

	return seg, rest[end:], nil
}

// doubleQuoted returns the name that rest spells in double quotes, as in
// `"name"`. It reads the escapes of [readQuoted], so `\"` does not end the
// name. It reports false when rest does not start with a `"` or when
// readQuoted returns an error for the text after it.
func doubleQuoted(rest string) (string, bool) {
	after, ok := strings.CutPrefix(rest, `"`)
	if !ok {
		return "", false
	}

	name, _, err := readQuoted(after, '"')

	return name, err == nil
}

// quotedSelector returns the child or recursive selector of the given kind
// for name, with the name in single quotes whether or not it needs them,
// as in `.'name'`.
func quotedSelector(kind segmentKind, name string) string {
	if kind == segmentRecursive {
		return ".." + singleQuote(name)
	}

	return "." + singleQuote(name)
}

// unquotedNameError returns the error for the unquoted selector seg, whose
// name [Path.String] writes in quotes. The error names the first character
// that needs the quotes and shows the selector with them.
//
// A name that starts with `*` is a wildcard selector with text after it,
// such as the line feed at the end of `.*\n`. The error for it names the
// first character after the `*` that needs the quotes, and the `*` itself
// only when no later character does. It shows the wildcard selector
// before the quoted one.
func unquotedNameError(seg segment) error {
	text := seg.name

	after, wildcard := strings.CutPrefix(seg.name, "*")
	if wildcard && strings.ContainsFunc(after, forcesQuotes) {
		text = after
	}

	r, _ := utf8.DecodeRuneInString(text[strings.IndexFunc(text, forcesQuotes):])

	var what string

	switch {
	case unicode.IsSpace(r):
		what = "whitespace"
	case unicode.IsControl(r):
		what = "control character"
	default:
		what = strconv.QuoteRune(r)
	}

	if !wildcard {
		return fmt.Errorf("%s in unquoted selector %q; write %s to keep it", what, seg.name, seg)
	}

	all := segment{kind: segmentChildAll}
	if seg.kind == segmentRecursive {
		all.kind = segmentRecursiveAll
	}

	return fmt.Errorf("%s in unquoted selector %q; write %s for the wildcard, or %s to keep it",
		what, seg.name, all, seg)
}

// parseQuoted reads a single-quoted name after `.'` or `..'` as a selector
// of the given kind. Inside the quotes, `\` escapes the next character as
// [Parse] describes. Empty quotes name the empty key.
func parseQuoted(rest string, kind segmentKind) (segment, string, error) {
	name, remaining, err := readQuoted(rest, '\'')
	if err != nil {
		return segment{}, "", err
	}

	return segment{kind: kind, name: name}, remaining, nil
}

// readQuoted reads a name up to the quote character that closes it, and
// it returns the name and the text after that character. Inside the name,
// `\` escapes the next character as [Parse] describes, so an escaped quote
// character does not close the name.
func readQuoted(rest string, quote byte) (string, string, error) {
	var sb strings.Builder

	for i := 0; i < len(rest); i++ {
		switch rest[i] {
		case '\\':
			if i+1 >= len(rest) {
				return "", "", errors.New("unterminated escape in quoted selector")
			}

			i++

			switch rest[i] {
			case 'n':
				sb.WriteByte('\n')
			case 'r':
				sb.WriteByte('\r')
			case 't':
				sb.WriteByte('\t')
			case 'u':
				r, err := parseUnicodeEscape(rest[i+1:])
				if err != nil {
					return "", "", err
				}

				sb.WriteRune(r)

				i += unicodeEscapeDigits

			default:
				sb.WriteByte(rest[i])
			}

		case quote:
			return sb.String(), rest[i+1:], nil

		default:
			sb.WriteByte(rest[i])
		}
	}

	return "", "", errors.New("unterminated quoted selector")
}

// unicodeEscapeDigits is the number of hexadecimal digits after `\u` in
// a quoted selector.
const unicodeEscapeDigits = 4

// parseUnicodeEscape reads the [unicodeEscapeDigits] hexadecimal digits
// that start rest as the code of a character. It returns an error when
// rest starts with fewer such digits. It also returns an error when the
// digits name a surrogate half, which is not a character.
func parseUnicodeEscape(rest string) (rune, error) {
	if len(rest) < unicodeEscapeDigits {
		return 0, errUnicodeEscapeDigits
	}

	var r rune

	for i := range unicodeEscapeDigits {
		digit, ok := hexDigit(rest[i])
		if !ok {
			return 0, errUnicodeEscapeDigits
		}

		r = r<<4 | digit
	}

	if !utf8.ValidRune(r) {
		return 0, fmt.Errorf(`\u%s in quoted selector is a surrogate half, which names no character`,
			rest[:unicodeEscapeDigits])
	}

	return r, nil
}

// hexDigit returns the value of the hexadecimal digit c, and reports
// whether c is one.
func hexDigit(c byte) (rune, bool) {
	switch {
	case c >= '0' && c <= '9':
		return rune(c - '0'), true
	case c >= 'a' && c <= 'f':
		return rune(c-'a') + 10, true
	case c >= 'A' && c <= 'F':
		return rune(c-'A') + 10, true
	default:
		return 0, false
	}
}

// parseIndex reads `n]` or `*]` after `[`. A quote after `[` starts the
// bracket-quoted name of another path syntax, such as `['name']` in
// JSONPath, and the error for it shows the selector that names the key.
func parseIndex(rest string) (segment, string, error) {
	err := bracketQuotedError(rest, segmentChild)
	if err != nil {
		return segment{}, "", err
	}

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
// [Path.String] writes one. Such an integer has at least one digit, and it
// starts with a zero only when the number is zero itself.
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

// bracketQuotedError returns the error for a name in quotes after a `[`,
// as in `'name']` or `"name"]`. The error shows the selector of the given
// kind that names the key. It returns nil when rest does not start with a
// quote.
func bracketQuotedError(rest string, kind segmentKind) error {
	name, ok := bracketQuoted(rest)
	if !ok {
		return nil
	}

	return fmt.Errorf("bracket-quoted names are not supported; write %s", quotedSelector(kind, name))
}

// bracketQuoted returns the name that rest spells in quotes after a `[`,
// as in `'name']` or `"name"]`. It reads the escapes of [readQuoted] in
// either kind of quotes. It reports false when rest does not start with a
// quote. It returns the placeholder `name` when the quotes do not close,
// when a `]` does not follow them, or when readQuoted returns an error for
// an escape.
func bracketQuoted(rest string) (string, bool) {
	if rest == "" || (rest[0] != '\'' && rest[0] != '"') {
		return "", false
	}

	name, remaining, err := readQuoted(rest[1:], rest[0])
	if err != nil || !strings.HasPrefix(remaining, "]") {
		return "name", true
	}

	return name, true
}
