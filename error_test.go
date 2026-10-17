package niceyaml_test

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"math"
	"net"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/lexer"
	"github.com/goccy/go-yaml/token"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.jacobcolvin.com/x/stringtest"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/diff"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/line"
	"go.jacobcolvin.com/niceyaml/paths"
	"go.jacobcolvin.com/niceyaml/position"
	"go.jacobcolvin.com/niceyaml/printer"
	"go.jacobcolvin.com/niceyaml/schema"
	"go.jacobcolvin.com/niceyaml/schema/matcher"
	"go.jacobcolvin.com/niceyaml/style/kind"
)

// customTestError is a test error type for errors.As testing.
type customTestError struct {
	msg string
}

func (e *customTestError) Error() string {
	return e.msg
}

// wrapError returns the [*niceyaml.Error] that [niceyaml.Invalid]
// creates around err, so a test reads the accessors of the Error. It
// fails the test for an err that Invalid returns nil for.
func wrapError(tb testing.TB, err error, opts ...niceyaml.ErrorOption) *niceyaml.Error {
	tb.Helper()

	return yamltest.RequireError(tb, niceyaml.Invalid(err, opts...))
}

// requireBound binds err to the single document of source, as
// [yamltest.Bind] does, and returns the binding. It fails the test when
// the result holds none.
func requireBound(tb testing.TB, source *niceyaml.Source, err error) *niceyaml.SourceError {
	tb.Helper()

	return yamltest.RequireSourceError(tb, yamltest.Bind(tb, source, err))
}

// errOf returns the error of a call that returns a value beside it, so a
// table case holds the error of such a call in one expression.
func errOf[T any](_ T, err error) error {
	return err
}

// render formats err the way %+v does, which prints the annotated source
// when the error resolves to a location and the plain message otherwise. A
// [*niceyaml.SourceError] renders with [yamltest.NewXMLPrinter] and two
// lines of context, so tests can assert on styled text without escape
// sequences. The printer pads its rows with spaces, which render trims
// from the end of each line.
func render(err error) string {
	return renderContext(err, 2)
}

// report returns the message part of the %+v output of err: the headline
// and one line per nested error, before the blank line and the excerpt.
func report(err error) string {
	msg, _, _ := strings.Cut(fmt.Sprintf("%+v", err), "\n\n")

	return msg
}

// renderParts splits what [render] prints for err at the blank line
// after the tree, and returns the tree and the excerpt below it.
func renderParts(err error) (string, string) {
	tree, excerpt, _ := strings.Cut(render(err), "\n\n")

	return tree, excerpt
}

// renderContext is [render] with the given number of context lines.
func renderContext(err error, lines int) string {
	return renderWith(err, yamltest.NewXMLPrinter(), lines)
}

// renderWith is [render] with the given printer and number of context
// lines. An error that is not a [*niceyaml.SourceError] formats with %+v.
func renderWith(err error, p *printer.Printer, lines int) string {
	if bound, ok := err.(*niceyaml.SourceError); ok { //nolint:errorlint // Mirrors %+v, which formats the top-level value.
		return stringtest.TrimLineEnds(p.PrintError(bound, niceyaml.WithContextLines(lines)))
	}

	return stringtest.TrimLineEnds(fmt.Sprintf("%+v", err))
}

// errorTexts returns the message of the binding of each node, or nil
// when nodes is empty.
func errorTexts(nodes []niceyaml.ErrorTree) []string {
	var texts []string

	for _, n := range nodes {
		texts = append(texts, n.Bound.Error())
	}

	return texts
}

// bindingTexts returns the message of each binding in the tree of err, in
// the order [yamltest.BoundIn] returns them. The result is empty rather
// than nil when the tree holds no binding.
func bindingTexts(err error) []string {
	texts := []string{}
	for _, bound := range yamltest.BoundIn(err) {
		texts = append(texts, bound.Error())
	}

	return texts
}

func TestError(t *testing.T) {
	t.Parallel()

	threeKeys := stringtest.Input(`
		a: b
		foo: bar
		key: value
	`)
	tokens := lexer.Tokenize(threeKeys)

	// Each case binds its error to a source of input, or leaves the error
	// bound to no source when input is empty. The rendering shows two
	// lines of context around a location unless contextLines says
	// otherwise.
	tcs := map[string]struct {
		err          error
		input        string
		want         string
		contextLines int
	}{
		"empty message returns empty string": {
			err:  niceyaml.NewError(""),
			want: "",
		},
		"no path or token returns plain error": {
			err:  niceyaml.NewError("something went wrong"),
			want: "something went wrong",
		},
		"plain error bound to a source stays plain": {
			input: threeKeys,
			err:   errors.New("plain error"),
			want:  "plain error",
		},
		"with path and source shows annotated source": {
			input: threeKeys,
			err:   niceyaml.NewError("invalid value", niceyaml.AtPath(paths.Current().Child("key").Key())),
			want: stringtest.JoinLF(
				"3:1: $.key~: invalid value",
				"",
				"<nameTag>a</nameTag><punctuationMappingValue>:</punctuationMappingValue><text> </text><literalString>b</literalString>",
				"<nameTag>foo</nameTag><punctuationMappingValue>:</punctuationMappingValue><text> </text><literalString>bar</literalString>",
				"<genericError>key</genericError><punctuationMappingValue>:</punctuationMappingValue><text> </text><literalString>value</literalString>",
				"<textError>^^^</textError>",
			),
		},
		"with direct token bypasses path resolution": {
			input: threeKeys,
			err:   niceyaml.NewError("bad token", niceyaml.AtPosition(position.NewFromToken(tokens[0]))),
			want: stringtest.JoinLF(
				"1:1: bad token",
				"",
				"<genericError>a</genericError><punctuationMappingValue>:</punctuationMappingValue><text> </text><literalString>b</literalString>",
				"<textError>^</textError>",
				"<nameTag>foo</nameTag><punctuationMappingValue>:</punctuationMappingValue><text> </text><literalString>bar</literalString>",
				"<nameTag>key</nameTag><punctuationMappingValue>:</punctuationMappingValue><text> </text><literalString>value</literalString>",
			),
		},
		"path that does not resolve names the reason": {
			input: threeKeys,
			err:   niceyaml.NewError("not found", niceyaml.AtPath(paths.Current().Child("nonexistent").Key())),
			want:  stringtest.JoinLF("$.nonexistent~: not found", "", "no excerpt: resolve $.nonexistent~: not found"),
		},
		"path without source": {
			err:  niceyaml.NewError("missing source", niceyaml.AtPath(paths.Current().Child("key").Key())),
			want: "@.key~: missing source",
		},
		// No path resolves in a document with no content, so the reason
		// would add nothing to the path the message names.
		"empty source": {
			err: yamltest.Bind(t, niceyaml.NewSourceFromTokens(lexer.Tokenize("")), niceyaml.NewError(
				"error in empty source",
				niceyaml.AtPath(paths.Current().Child("key").Key()),
			)),
			want: "$.key~: error in empty source",
		},
		// The headline counts from 1, and the highlight covers the range
		// rather than the token under it.
		"range highlights its columns": {
			input: "key: some value\n",
			err:   niceyaml.NewError("bad word", niceyaml.AtRange(yamltest.LineRange(0, 5, 9))),
			want: stringtest.JoinLF(
				"1:6: bad word",
				"",
				"<nameTag>key</nameTag><punctuationMappingValue>:</punctuationMappingValue><text> </text><genericError>some</genericError><literalString> value</literalString>",
				"<textError>     ^^^^</textError>",
			),
		},
		"nested path shows correct key": {
			input: stringtest.Input(`
				foo:
				  bar: value
			`),
			err: niceyaml.NewError("nested error", niceyaml.AtPath(paths.Current().Child("foo", "bar").Key())),
			want: stringtest.JoinLF(
				"2:3: $.foo.bar~: nested error",
				"",
				"<nameTag>foo</nameTag><punctuationMappingValue>:</punctuationMappingValue>",
				"<text>  </text><genericError>bar</genericError><punctuationMappingValue>:</punctuationMappingValue><text> </text><literalString>value</literalString>",
				"<textError>  ^^^</textError>",
			),
		},
		"array element path - first item": {
			input: stringtest.Input(`
				items:
				  - first
				  - second
			`),
			err: niceyaml.NewError("array error", niceyaml.AtPath(paths.Current().Child("items").Index(0).Key())),
			want: stringtest.JoinLF(
				"2:5: $.items[0]~: array error",
				"",
				"<nameTag>items</nameTag><punctuationMappingValue>:</punctuationMappingValue>",
				"<text>  </text><punctuationSequenceEntry>-</punctuationSequenceEntry><text> </text><genericError>first</genericError>",
				"<textError>    ^^^^^</textError>",
				"<text>  </text><punctuationSequenceEntry>-</punctuationSequenceEntry><text> </text><literalString>second</literalString>",
			),
		},
		"array element path - nested object in array": {
			input: stringtest.Input(`
				users:
				  - name: alice
				    age: 30
			`),
			err: niceyaml.NewError(
				"nested array error",
				niceyaml.AtPath(paths.Current().Child("users").Index(0).Child("name").Key()),
			),
			want: stringtest.JoinLF(
				"2:5: $.users[0].name~: nested array error",
				"",
				"<nameTag>users</nameTag><punctuationMappingValue>:</punctuationMappingValue>",
				"<text>  </text><punctuationSequenceEntry>-</punctuationSequenceEntry><text> </text><genericError>name</genericError><punctuationMappingValue>:</punctuationMappingValue><text> </text><literalString>alice</literalString>",
				"<textError>    ^^^^</textError>",
				"<text>    </text><nameTag>age</nameTag><punctuationMappingValue>:</punctuationMappingValue><text> </text><literalNumberInteger>30</literalNumberInteger>",
			),
		},
		"single top-level key path": {
			input: "key: value",
			err:   niceyaml.NewError("top level error", niceyaml.AtPath(paths.Current().Child("key").Key())),
			want: stringtest.JoinLF(
				"1:1: $.key~: top level error",
				"",
				"<genericError>key</genericError><punctuationMappingValue>:</punctuationMappingValue><text> </text><literalString>value</literalString>",
				"<textError>^^^</textError>",
			),
		},
		"with custom source lines": {
			input: stringtest.Input(`
				line1: a
				line2: b
				line3: c
				line4: d
				line5: e
			`),
			err:          niceyaml.NewError("middle error", niceyaml.AtPath(paths.Current().Child("line3").Key())),
			contextLines: 1,
			want: stringtest.JoinLF(
				"3:1: $.line3~: middle error",
				"",
				"<nameTag>line2</nameTag><punctuationMappingValue>:</punctuationMappingValue><text> </text><literalString>b</literalString>",
				"<genericError>line3</genericError><punctuationMappingValue>:</punctuationMappingValue><text> </text><literalString>c</literalString>",
				"<textError>^^^^^</textError>",
				"<nameTag>line4</nameTag><punctuationMappingValue>:</punctuationMappingValue><text> </text><literalString>d</literalString>",
			),
		},
		"value selection highlights value token": {
			input: "key: value",
			err:   yamltest.ErrorAt("invalid value", "key"),
			want: stringtest.JoinLF(
				"1:6: $.key: invalid value",
				"",
				"<nameTag>key</nameTag><punctuationMappingValue>:</punctuationMappingValue><text> </text><genericError>value</genericError>",
				"<textError>     ^^^^^</textError>",
			),
		},
		"nested path with value target": {
			input: stringtest.Input(`
				foo:
				  bar: nested_value
			`),
			err: yamltest.ErrorAt("nested value error", "foo", "bar"),
			want: stringtest.JoinLF(
				"2:8: $.foo.bar: nested value error",
				"",
				"<nameTag>foo</nameTag><punctuationMappingValue>:</punctuationMappingValue>",
				"<text>  </text><nameTag>bar</nameTag><punctuationMappingValue>:</punctuationMappingValue><text> </text><genericError>nested_value</genericError>",
				"<textError>       ^^^^^^^^^^^^</textError>",
			),
		},
		"array element works same as key target": {
			// Array elements do not have keys, so both targets return the value token.
			input: stringtest.Input(`
				items:
				  - first
				  - second
			`),
			err: niceyaml.NewError("array error", niceyaml.AtPath(paths.Current().Child("items").Index(0))),
			want: stringtest.JoinLF(
				"2:5: $.items[0]: array error",
				"",
				"<nameTag>items</nameTag><punctuationMappingValue>:</punctuationMappingValue>",
				"<text>  </text><punctuationSequenceEntry>-</punctuationSequenceEntry><text> </text><genericError>first</genericError>",
				"<textError>    ^^^^^</textError>",
				"<text>  </text><punctuationSequenceEntry>-</punctuationSequenceEntry><text> </text><literalString>second</literalString>",
			),
		},
		"root level array - element has no key": {
			// A key target on a sequence element falls back to the element.
			input: stringtest.Input(`
				- first
				- second
				- third
			`),
			err: niceyaml.NewError("array element error", niceyaml.AtPath(paths.Current().Index(1).Key())),
			want: stringtest.JoinLF(
				"2:3: $[1]~: array element error",
				"",
				"<punctuationSequenceEntry>-</punctuationSequenceEntry><text> </text><literalString>first</literalString>",
				"<punctuationSequenceEntry>-</punctuationSequenceEntry><text> </text><genericError>second</genericError>",
				"<textError>  ^^^^^^</textError>",
				"<punctuationSequenceEntry>-</punctuationSequenceEntry><text> </text><literalString>third</literalString>",
			),
		},
		"document root - no entry selected": {
			// A key target on the root falls back to the mapping's first key.
			input: stringtest.Input(`
				key: value
				another: line
			`),
			err: niceyaml.NewError("document root error", niceyaml.AtPath(paths.Current().Key())),
			want: stringtest.JoinLF(
				"1:1: $~: document root error",
				"",
				"<genericError>key</genericError><punctuationMappingValue>:</punctuationMappingValue><text> </text><literalString>value</literalString>",
				"<textError>^^^</textError>",
				"<nameTag>another</nameTag><punctuationMappingValue>:</punctuationMappingValue><text> </text><literalString>line</literalString>",
			),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			err := tc.err
			if tc.input != "" {
				err = yamltest.Bind(t, niceyaml.NewSourceFromString(tc.input), err)
			}

			assert.Equal(t, tc.want, renderContext(err, cmp.Or(tc.contextLines, 2)))
		})
	}
}

// blankError models a wrapper that blanks the text of the error it wraps.
type blankError struct {
	err error
}

// Error returns an empty message.
func (blankError) Error() string {
	return ""
}

// Unwrap returns the wrapped error.
func (e blankError) Unwrap() error {
	return e.err
}

func TestError_Error(t *testing.T) {
	t.Parallel()

	port := yamltest.ErrorAt("0 is less than 1", "port")
	open := yamltest.ErrorAt("bad open", "open")
	closeErr := yamltest.ErrorAt("bad close", "close")

	// The message of an Error carries no location, so a program that
	// prints an unbound one with fmt sees the message alone. FormatError
	// reads the path from the Error, through any wrapper, and so do the
	// %+v verb and LogValue of the Error itself. A join and a wrapper from
	// fmt.Errorf have no Format method, so %+v prints their message.
	tcs := map[string]struct {
		err        error
		want       string
		wantPlus   string
		wantFormat string
	}{
		"a located Error": {
			err:        port,
			want:       "0 is less than 1",
			wantPlus:   "@.port: 0 is less than 1",
			wantFormat: "@.port: 0 is less than 1",
		},
		// A range puts no position in the message until a source binds
		// it.
		"an Error at a range": {
			err:        niceyaml.NewError("bad word", niceyaml.AtRange(yamltest.LineRange(0, 5, 9))),
			want:       "bad word",
			wantPlus:   "bad word",
			wantFormat: "bad word",
		},
		"an Error with no message": {
			err:        yamltest.ErrorAt("", "a"),
			want:       "",
			wantPlus:   "@.a:",
			wantFormat: "@.a:",
		},
		"a join of located Errors": {
			err:        errors.Join(open, closeErr),
			want:       stringtest.JoinLF("bad open", "bad close"),
			wantPlus:   stringtest.JoinLF("bad open", "bad close"),
			wantFormat: stringtest.JoinLF("|-- @.open: bad open", "`-- @.close: bad close"),
		},
		"a wrapper around a located Error": {
			err:        fmt.Errorf("check: %w", open),
			want:       "check: bad open",
			wantPlus:   "check: bad open",
			wantFormat: "@.open: check: bad open",
		},
		"a wrapper that rewrites the text": {
			err:        yamltest.RewriteError{Err: open},
			want:       "rewritten",
			wantPlus:   "rewritten",
			wantFormat: "@.open: rewritten",
		},
		"a wrapper that blanks the text": {
			err:        blankError{err: open},
			want:       "",
			wantPlus:   "",
			wantFormat: "@.open:",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, tc.err.Error())
			assert.Equal(t, tc.want, fmt.Sprint(tc.err))
			assert.Equal(t, tc.want, fmt.Sprintf("%v", tc.err))
			assert.Equal(t, tc.wantPlus, fmt.Sprintf("%+v", tc.err))
			assert.Equal(t, tc.wantFormat, niceyaml.FormatError(tc.err, niceyaml.WithContextLines(0)))
		})
	}
}

func TestSourceError_Error_Name(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		opts []niceyaml.SourceOption
		want string
	}{
		"no name puts the position alone in front": {
			want: "2:8: $.value: bad value",
		},
		"name goes in front of the position": {
			opts: []niceyaml.SourceOption{niceyaml.WithName("config")},
			want: "config:2:8: $.value: bad value",
		},
		"file path names the source": {
			opts: []niceyaml.SourceOption{niceyaml.WithFilePath("dir/config.yaml")},
			want: "dir/config.yaml:2:8: $.value: bad value",
		},
		"name wins over file path": {
			opts: []niceyaml.SourceOption{
				niceyaml.WithName("config"),
				niceyaml.WithFilePath("dir/config.yaml"),
			},
			want: "config:2:8: $.value: bad value",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			source := niceyaml.NewSourceFromString("name: test\nvalue: 123\n", tc.opts...)
			err := yamltest.Bind(t, source, yamltest.ErrorAt("bad value", "value"))

			assert.Equal(t, tc.want, err.Error())
			assert.Equal(t, "document 0: "+tc.want, fmt.Errorf("document 0: %w", err).Error())
		})
	}

	// An error without a location has no position for the name to go in
	// front of, so the name stands alone.
	noLocation := map[string]struct {
		err  error
		name string
		want string
		// The reason the location did not resolve, which the detail names
		// below the message, or "" for an error that carries no location.
		wantDetail string
	}{
		"no name leaves the message alone": {
			err:  errors.New("bad value"),
			want: "bad value",
		},
		"name stands alone in front of a plain error": {
			err:  errors.New("bad value"),
			name: "config",
			want: "config: bad value",
		},
		"name stands alone in front of an Error without a location": {
			err:  niceyaml.NewError("bad value"),
			name: "config",
			want: "config: bad value",
		},
		"name stands alone in front of a path that does not resolve": {
			err:        niceyaml.NewError("bad value", niceyaml.AtPath(paths.Current().Child("missing").Index(0))),
			name:       "config",
			want:       "config: $.missing[0]: bad value",
			wantDetail: "no excerpt: resolve $.missing[0]: not found",
		},
		"name stands alone in front of an empty message": {
			err:  niceyaml.NewError(""),
			name: "config",
			want: "config:",
		},
	}

	for name, tc := range noLocation {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			source := niceyaml.NewSourceFromString("name: test\nvalue: 123\n", niceyaml.WithName(tc.name))
			err := yamltest.Bind(t, source, tc.err)

			assert.Equal(t, tc.want, err.Error())

			wantPlus := tc.want
			if tc.wantDetail != "" {
				wantPlus = stringtest.JoinLF(tc.want, "", tc.wantDetail)
			}

			assert.Equal(t, wantPlus, fmt.Sprintf("%+v", err), "no excerpt without a location")
		})
	}

	// The last line or column an int holds still counts from 1 without
	// wrapping around to a negative number.
	maxOneBased := strconv.FormatUint(uint64(math.MaxInt)+1, 10)

	far := map[string]struct {
		pos  position.Position
		want string
		// The reason the location did not resolve, or "" when it did.
		wantUnresolved string
	}{
		"column past the end of its line": {
			pos:  position.New(0, math.MaxInt),
			want: "a.yaml:1:" + maxOneBased + ": far",
		},
		"line past the end of the source": {
			pos:            position.New(math.MaxInt, 0),
			want:           "a.yaml: far",
			wantUnresolved: "line " + maxOneBased + " not in lines 1-2",
		},
	}

	for name, tc := range far {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			source := niceyaml.NewSourceFromString("a: 1\nb: 2\n", niceyaml.WithName("a.yaml"))
			err := yamltest.Bind(t, source, niceyaml.NewError("far", niceyaml.AtPosition(tc.pos)))

			assert.Equal(t, tc.want, err.Error())

			bound := yamltest.RequireSourceError(t, err)

			if tc.wantUnresolved == "" {
				require.NoError(t, bound.Unresolved())

				return
			}

			require.ErrorIs(t, bound.Unresolved(), niceyaml.ErrOutOfRange)
			assert.Contains(t, bound.Unresolved().Error(), tc.wantUnresolved)
		})
	}
}

func TestSourceError_Error_NameControl(t *testing.T) {
	t.Parallel()

	// The name of a file can hold a line feed or an escape sequence. A
	// message that wrote it as it is would start a line that reads as an
	// error of another file, and would write to a terminal.
	const (
		name  = "a.yaml: valid\nb.yaml\x1b[31m\tx"
		shown = "a.yaml: valid\u240ab.yaml\u241b[31m    x"
	)

	source := niceyaml.NewSourceFromString("a: 1\nb: 2\n", niceyaml.WithName(name))

	doc := yamltest.Document(t, source)

	atA := niceyaml.NewError("x", niceyaml.AtPath(paths.Doc().Child("a")))
	atB := niceyaml.NewError("y", niceyaml.AtPath(paths.Doc().Child("b")))

	tcs := map[string]struct {
		err  error
		want string
	}{
		"in front of a position": {
			err:  doc.Bind(atB),
			want: shown + ":2:4: $.b: y",
		},
		"in front of a message with no position": {
			err:  doc.Bind(errors.New("bad")),
			want: shown + ": bad",
		},
		"on every line of a list": {
			err: doc.Bind(niceyaml.NewSummary("two", atA, atB)),
			want: stringtest.JoinLF(
				shown+": two",
				shown+":1:4: $.a: x",
				shown+":2:4: $.b: y",
			),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			require.EqualError(t, tc.err, tc.want)
			assert.NotContains(t, tc.err.Error(), "\x1b")
			assert.Contains(t, niceyaml.FormatError(tc.err, niceyaml.WithContextLines(0)), shown)
		})
	}

	// The Source keeps the name as the caller gave it.
	assert.Equal(t, name, source.Name())
}

func TestSourceError_Error_Document(t *testing.T) {
	t.Parallel()

	input := stringtest.LinesLF(
		"a: 1",
		"---",
		"b: 2",
		"---",
		"kind: Wat",
		"items:",
		"  - x",
	)

	itemsPath := paths.Current().Child("items")

	source := niceyaml.NewSourceFromString(input, niceyaml.WithName("m.yaml"))
	docs := yamltest.Documents(t, source, 3)

	tcs := map[string]struct {
		err  error
		want string
		// The message of the binding, which carries no document.
		message string
		// The binding resolved a location, so its position stands in front.
		located bool
	}{
		"a plain error names its document": {
			err:     docs[2].Bind(errors.New("plain")),
			want:    "m.yaml: document 3: plain",
			message: "plain",
		},
		"an Error without a location names its document": {
			err:     docs[0].Bind(niceyaml.NewError("bad")),
			want:    "m.yaml: document 1: bad",
			message: "bad",
		},
		"a path that does not resolve names its document": {
			err:     docs[2].Bind(niceyaml.NewError("required", niceyaml.AtPath(itemsPath.Index(7)))),
			want:    "m.yaml: document 3: $.items[7]: required",
			message: "required",
		},
		"a position says which document, so no document goes beside it": {
			err:     docs[2].Bind(yamltest.ErrorAt("bad", "kind")),
			want:    "m.yaml:5:7: $.kind: bad",
			message: "bad",
			located: true,
		},
		"each listed line without a position names its document": {
			err: docs[2].Bind(niceyaml.NewSummary("2 problems",
				yamltest.ErrorAt("bad", "kind"),
				errors.New("plain"),
			)),
			want: stringtest.JoinLF(
				"m.yaml: document 3: 2 problems",
				"m.yaml:5:7: $.kind: bad",
				"m.yaml: document 3: plain",
			),
			message: "2 problems",
		},
		"a scoped Node names its document for an error that gains no location": {
			err:     yamltest.At(t, docs[2], itemsPath).DecodeInto(t.Context(), nil),
			want:    "m.yaml: document 3: decode target is not a non-nil pointer: got nil",
			message: "decode target is not a non-nil pointer: got nil",
		},
		"an error of a Node's own operation names its document": {
			err:     errOf(docs[1].At(paths.Current().ChildAll())),
			want:    "m.yaml: document 2: resolve $.*: wildcard path matches any number of nodes",
			message: "resolve $.*: wildcard path matches any number of nodes",
		},
		"the source binds an error with no location to no document": {
			err:     source.Bind(errors.New("plain")),
			want:    "m.yaml: plain",
			message: "plain",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			require.EqualError(t, tc.err, tc.want)

			bound := yamltest.RequireSourceError(t, tc.err)
			assert.Equal(t, tc.message, bound.Message())

			_, ok := bound.Range()
			assert.Equal(t, tc.located, ok)
		})
	}

	t.Run("a source with one document names none", func(t *testing.T) {
		t.Parallel()

		source := niceyaml.NewSourceFromString("kind: Wat\n", niceyaml.WithName("m.yaml"))

		require.EqualError(t, yamltest.Bind(t, source, errors.New("plain")), "m.yaml: plain")
	})

	t.Run("a document below an empty one keeps its place in the file", func(t *testing.T) {
		t.Parallel()

		// The source holds one document for a decode, and the message
		// counts the empty document above it.
		source := niceyaml.NewSourceFromString(
			stringtest.LinesLF("---", "---", "kind: Wat"),
			niceyaml.WithName("m.yaml"),
		)

		err := yamltest.Bind(t, source, errors.New("plain"))
		require.EqualError(t, err, "m.yaml: document 2: plain")

		bound := yamltest.RequireSourceError(t, err)

		index, ok := bound.DocumentIndex()
		require.True(t, ok)
		assert.Equal(t, 1, index)
	})

	t.Run("a source with no name leads with the document", func(t *testing.T) {
		t.Parallel()

		docs := yamltest.Documents(t, niceyaml.NewSourceFromString(input), 3)

		require.EqualError(t, docs[1].Bind(errors.New("plain")), "document 2: plain")
		require.EqualError(t,
			docs[2].Bind(yamltest.ErrorAt("bad", "kind")),
			"5:7: $.kind: bad",
		)
	})

	t.Run("a wrapper around a binding keeps the document the binding wrote", func(t *testing.T) {
		t.Parallel()

		wrapped := fmt.Errorf("check: %w", docs[1].Bind(errors.New("plain")))
		require.EqualError(t, wrapped, "check: m.yaml: document 2: plain")

		// The binding names its document already, so binding the wrapper
		// through another document adds none.
		require.EqualError(t, docs[0].Bind(wrapped), "check: m.yaml: document 2: plain")
	})

	t.Run("a join of one binding per document names each", func(t *testing.T) {
		t.Parallel()

		joined := errors.Join(
			docs[0].Bind(errors.New("first")),
			docs[2].Bind(errors.New("third")),
		)

		require.EqualError(t, joined, stringtest.JoinLF(
			"m.yaml: document 1: first",
			"m.yaml: document 3: third",
		))
		assert.Equal(t, stringtest.JoinLF(
			"|-- m.yaml: document 1: first",
			"`-- m.yaml: document 3: third",
		), niceyaml.FormatError(joined))
	})

	t.Run("a row of the tree names a document its parent does not", func(t *testing.T) {
		t.Parallel()

		// The first nested error is bound to the second document, and the
		// other binds with its parent to the first.
		err := docs[0].Bind(niceyaml.NewSummary("summary",
			docs[1].Bind(errors.New("elsewhere")),
			errors.New("here"),
		))

		require.EqualError(t, err, stringtest.JoinLF(
			"m.yaml: document 1: summary",
			"m.yaml: document 2: elsewhere",
			"m.yaml: document 1: here",
		))
		assert.Equal(t, stringtest.JoinLF(
			"m.yaml: document 1: summary",
			"|-- document 2: elsewhere",
			"`-- here",
		), niceyaml.FormatError(err))
	})
}

func TestSourceError_Error_MultiError(t *testing.T) {
	t.Parallel()

	errSentinel := errors.New("sentinel")
	plain := errors.New("plain")
	source := errorTreeSource()
	boundA := yamltest.Bind(t, source, yamltest.ErrorAt("bad a", "a"))
	boundB := yamltest.Bind(t, source, yamltest.ErrorAt("bad b", "b"))

	tcs := map[string]struct {
		err  error
		want string
	}{
		// A bound join lists its branches, each behind the name of its own
		// source, so the line of a binding stays as it is and a plain branch
		// takes the name. A branch with no message adds no line, whether it
		// is an empty message or a nil pointer.
		"join led by the binding": {
			err:  errors.Join(boundA, plain),
			want: stringtest.JoinLF("f.yaml:1:4: $.a: bad a", "f.yaml: plain"),
		},
		"join led by an empty message": {
			err:  errors.Join(errors.New(""), boundA, plain),
			want: stringtest.JoinLF("f.yaml:1:4: $.a: bad a", "f.yaml: plain"),
		},
		"join led by a nil Error": {
			err:  errors.Join((*niceyaml.Error)(nil), boundA, plain),
			want: stringtest.JoinLF("f.yaml:1:4: $.a: bad a", "f.yaml: plain"),
		},
		"join led by a nil SourceError": {
			err:  errors.Join((*niceyaml.SourceError)(nil), boundA, plain),
			want: stringtest.JoinLF("f.yaml:1:4: $.a: bad a", "f.yaml: plain"),
		},
		// A wrapper with several %w verbs leads with the binding it keeps, as
		// a wrapper with one %w verb does, so its first line names the source
		// already. A multi-error of its own type leads with its first branch
		// when its message starts with the message of that branch, as a list
		// does. A count of the branches starts with no binding, so the name
		// goes in front whichever branch holds the binding. The message of a
		// list holds its branches, so nothing follows it, and a count holds
		// none, so each branch follows on a line of its own.
		"wrapper with one verb": {
			err:  errors.Join(fmt.Errorf("ctx: %w", boundA), plain),
			want: stringtest.JoinLF("ctx: f.yaml:1:4: $.a: bad a", "f.yaml: plain"),
		},
		"sentinel wrapper": {
			err:  errors.Join(fmt.Errorf("%w: %w", errSentinel, boundA), plain),
			want: stringtest.JoinLF("sentinel: f.yaml:1:4: $.a: bad a", "f.yaml: plain"),
		},
		"wrapper of two bindings": {
			err:  errors.Join(fmt.Errorf("%w and %w", boundA, boundB), plain),
			want: stringtest.JoinLF("f.yaml:1:4: $.a: bad a and f.yaml:2:4: $.b: bad b", "f.yaml: plain"),
		},
		"multi-error with the binding first": {
			err: violationsError{boundA, plain},
			want: stringtest.JoinLF(
				"f.yaml: 2 violations",
				"f.yaml:1:4: $.a: bad a",
				"f.yaml: plain",
			),
		},
		"multi-error with the binding last": {
			err: violationsError{plain, boundA},
			want: stringtest.JoinLF(
				"f.yaml: 2 violations",
				"f.yaml:1:4: $.a: bad a",
				"f.yaml: plain",
			),
		},
		"list with the binding first": {
			err:  listError{boundA, plain},
			want: "f.yaml:1:4: $.a: bad a; plain",
		},
		"list with the binding last": {
			err:  listError{plain, boundA},
			want: "f.yaml: plain; f.yaml:1:4: $.a: bad a",
		},
		// A multi-error of its own type keeps the message it wrote. That
		// message holds the text of a branch at most and never the location a
		// binding gives it, so each branch the binding locates follows on a
		// line of its own, whatever the message holds. A branch with no
		// location follows only when the message does not hold the first
		// branch.
		"a list of located errors": {
			err: listError{
				yamltest.ErrorAt("bad a", "a"),
				yamltest.ErrorAt("bad b", "b"),
			},
			want: stringtest.JoinLF(
				"f.yaml: bad a; bad b",
				"f.yaml:1:4: $.a: bad a",
				"f.yaml:2:4: $.b: bad b",
			),
		},
		"a heading that holds a short message by chance": {
			err: headedError{msg: "invalid config: 2 problems", errs: []error{
				yamltest.ErrorAt("invalid", "a"),
				yamltest.ErrorAt("too old", "b"),
			}},
			want: stringtest.JoinLF(
				"f.yaml: invalid config: 2 problems",
				"f.yaml:1:4: $.a: invalid",
				"f.yaml:2:4: $.b: too old",
			),
		},
		"a branch whose path does not resolve": {
			err: listError{
				niceyaml.NewError("bad x", niceyaml.AtPath(paths.Current().Child("x").Index(0))),
				yamltest.ErrorAt("bad a", "a"),
			},
			want: stringtest.JoinLF(
				"f.yaml: bad x; bad a",
				"f.yaml:1:4: $.a: bad a",
				"f.yaml: $.x[0]: bad x",
			),
		},
		"a list with located and unlocated branches": {
			err: listError{
				errors.New("plain"),
				yamltest.ErrorAt("bad a", "a"),
			},
			want: stringtest.JoinLF(
				"f.yaml: plain; bad a",
				"f.yaml:1:4: $.a: bad a",
			),
		},
		"unlocated branches the heading holds": {
			err: headedError{msg: "invalid config: 2 problems", errs: []error{
				errors.New("invalid"),
				errors.New("config"),
			}},
			want: "f.yaml: invalid config: 2 problems",
		},
		// The message holds the text a branch had before the binding bound
		// it, and the text of a summary names none of the problems it
		// heads. Those problems follow in place of the branch, each by the
		// rule for a branch.
		"summaries the heading holds": {
			err: listError{
				niceyaml.NewSummary("2 schema violations",
					yamltest.ErrorAt("bad a", "a"),
					yamltest.ErrorAt("bad b", "b")),
				niceyaml.NewSummary("2 name violations",
					yamltest.ErrorAt("long a", "a"),
					yamltest.ErrorAt("long b", "b")),
			},
			want: stringtest.JoinLF(
				"f.yaml: 2 schema violations; 2 name violations",
				"f.yaml:1:4: $.a: bad a",
				"f.yaml:1:4: $.a: long a",
				"f.yaml:2:4: $.b: bad b",
				"f.yaml:2:4: $.b: long b",
			),
		},
		"summaries under two %w verbs": {
			err: fmt.Errorf("%w; %w",
				niceyaml.NewSummary("2 schema violations",
					yamltest.ErrorAt("bad a", "a"),
					yamltest.ErrorAt("bad b", "b")),
				niceyaml.NewSummary("2 name violations",
					yamltest.ErrorAt("long a", "a"),
					yamltest.ErrorAt("long b", "b")),
			),
			want: stringtest.JoinLF(
				"f.yaml: 2 schema violations; 2 name violations",
				"f.yaml:1:4: $.a: bad a",
				"f.yaml:1:4: $.a: long a",
				"f.yaml:2:4: $.b: bad b",
				"f.yaml:2:4: $.b: long b",
			),
		},
		"a summary of plain errors the heading holds": {
			err: listError{
				niceyaml.NewSummary("2 problems", errors.New("first"), errors.New("second")),
				plain,
			},
			want: stringtest.JoinLF(
				"f.yaml: 2 problems; plain",
				"f.yaml: first",
				"f.yaml: second",
			),
		},
		"a summary inside a list the heading holds": {
			err: listError{
				listError{niceyaml.NewSummary("2 schema violations",
					yamltest.ErrorAt("bad a", "a"),
					yamltest.ErrorAt("bad b", "b"))},
				plain,
			},
			want: stringtest.JoinLF(
				"f.yaml: 2 schema violations; plain",
				"f.yaml:1:4: $.a: bad a",
				"f.yaml:2:4: $.b: bad b",
			),
		},
		// A branch that was a binding already lists its problems in its own
		// text, which the message holds, so no line follows.
		"a bound summary the heading holds": {
			err: listError{
				yamltest.Bind(t, source, niceyaml.NewSummary("2 schema violations",
					yamltest.ErrorAt("bad a", "a"),
					yamltest.ErrorAt("bad b", "b"))),
				plain,
			},
			want: stringtest.JoinLF(
				"f.yaml: 2 schema violations",
				"f.yaml:1:4: $.a: bad a",
				"f.yaml:2:4: $.b: bad b; plain",
			),
		},
		// The message holds the text of each branch of a join inside a
		// branch, so only the ones that branch located follow.
		"a join of located and plain errors inside a branch": {
			err: listError{
				fmt.Errorf("check: %w", errors.Join(
					yamltest.ErrorAt("bad a", "a"),
					errors.New("unplaced"),
				)),
				plain,
			},
			want: stringtest.JoinLF(
				"f.yaml: check: bad a",
				"unplaced; plain",
				"f.yaml:1:4: $.a: bad a",
			),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			err := source.Bind(tc.err)

			require.Error(t, err)
			assert.Equal(t, tc.want, err.Error())
		})
	}
}

// headedError is a multi-error of its own type whose message is a heading
// of its own, which can hold the message of a branch by chance.
type headedError struct {
	msg  string
	errs []error
}

// Error returns the heading.
func (e headedError) Error() string {
	return e.msg
}

// Unwrap returns the branches.
func (e headedError) Unwrap() []error {
	return e.errs
}

func TestSourceError_Error_List(t *testing.T) {
	t.Parallel()

	source := niceyaml.NewSourceFromString(stringtest.LinesLF("a: 1", "b: 2", "c: 3"), niceyaml.WithName("f.yaml"))
	badA := yamltest.ErrorAt("bad a", "a")
	badB := yamltest.ErrorAt("bad b", "b")
	badC := yamltest.ErrorAt("bad c", "c")
	summary := niceyaml.NewSummary("2 problems", badB, badA)

	// The message of a heading lists the problems it heads, one per line
	// behind its own position, in the order the tree shows them. A problem
	// is one line, whatever details it holds.
	tcs := map[string]struct {
		err  error
		want string
	}{
		"summary lists each error it heads by position": {
			err: summary,
			want: stringtest.JoinLF(
				"f.yaml: 2 problems",
				"f.yaml:1:4: $.a: bad a",
				"f.yaml:2:4: $.b: bad b",
			),
		},
		"located error keeps its details out": {
			err: niceyaml.NewError(
				"conflict",
				niceyaml.AtPath(paths.Current().Child("c")),
				niceyaml.WithDetails(badA, badB),
			),
			want: "f.yaml:3:4: $.c: conflict",
		},
		"problem a summary heads keeps its details out": {
			err: niceyaml.NewSummary("2 problems",
				niceyaml.NewError("bad c", niceyaml.AtPath(paths.Current().Child("c")), niceyaml.WithDetails(
					errors.New("reason"),
				)),
				badA,
			),
			want: stringtest.JoinLF(
				"f.yaml: 2 problems",
				"f.yaml:1:4: $.a: bad a",
				"f.yaml:3:4: $.c: bad c",
			),
		},
		"summary in a summary lists the errors it heads after its own line": {
			err: niceyaml.NewSummary("2 documents",
				niceyaml.NewSummary("first", badA, badB),
				niceyaml.NewError("second", niceyaml.WithDetails(badC)),
			),
			want: stringtest.JoinLF(
				"f.yaml: 2 documents",
				"f.yaml: first",
				"f.yaml:1:4: $.a: bad a",
				"f.yaml:2:4: $.b: bad b",
				"f.yaml: second",
			),
		},
		"details of an error with no location stay out": {
			err: niceyaml.NewError("no match", niceyaml.WithDetails(
				errors.New("no directive"),
				errors.New("no kind"),
			)),
			want: "f.yaml: no match",
		},
		"summary lists problems with no location": {
			// It has the shape of the error with details above, and the
			// constructor tells the two apart.
			err: niceyaml.NewSummary("2 problems",
				errors.New("no directive"),
				errors.New("no kind"),
			),
			want: stringtest.JoinLF(
				"f.yaml: 2 problems",
				"f.yaml: no directive",
				"f.yaml: no kind",
			),
		},
		"join is its branches alone": {
			err: errors.Join(badB, badA),
			want: stringtest.JoinLF(
				"f.yaml:1:4: $.a: bad a",
				"f.yaml:2:4: $.b: bad b",
			),
		},
		"summary in a join follows the located branches": {
			err: errors.Join(summary, badC),
			want: stringtest.JoinLF(
				"f.yaml:3:4: $.c: bad c",
				"f.yaml: 2 problems",
				"f.yaml:1:4: $.a: bad a",
				"f.yaml:2:4: $.b: bad b",
			),
		},
		"wrapper around a join keeps its text in front": {
			err: fmt.Errorf("ctx: %w", errors.Join(badA, badB)),
			want: stringtest.JoinLF(
				"f.yaml: ctx:",
				"f.yaml:1:4: $.a: bad a",
				"f.yaml:2:4: $.b: bad b",
			),
		},
		"wrapper with no text of its own around a join is the join": {
			err: fmt.Errorf("%w", errors.Join(badA, badB)),
			want: stringtest.JoinLF(
				"f.yaml:1:4: $.a: bad a",
				"f.yaml:2:4: $.b: bad b",
			),
		},
		"details around a bound summary stay out of its list": {
			err: niceyaml.Invalid(
				fmt.Errorf("load: %w", yamltest.Bind(t, source, summary)),
				niceyaml.WithDetails(badC),
			),
			want: stringtest.JoinLF(
				"load: f.yaml: 2 problems",
				"f.yaml:1:4: $.a: bad a",
				"f.yaml:2:4: $.b: bad b",
			),
		},
		"wrapper that rewrites a bound summary keeps its text": {
			err: niceyaml.Invalid(
				yamltest.UpperError{Err: yamltest.Bind(t, source, summary)},
				niceyaml.WithDetails(badC),
			),
			want: stringtest.JoinLF(
				"F.YAML: 2 PROBLEMS",
				"F.YAML:1:4: $.A: BAD A",
				"F.YAML:2:4: $.B: BAD B",
			),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			err := yamltest.Bind(t, source, tc.err)

			require.Error(t, err)
			assert.Equal(t, tc.want, err.Error())
		})
	}

	t.Run("a wrapper carries the list", func(t *testing.T) {
		t.Parallel()

		err := fmt.Errorf("load config: %w", yamltest.Bind(t, source, summary))

		want := stringtest.JoinLF(
			"load config: f.yaml: 2 problems",
			"f.yaml:1:4: $.a: bad a",
			"f.yaml:2:4: $.b: bad b",
		)

		assert.Equal(t, want, err.Error())
		assert.Equal(t, want, fmt.Sprintf("%+v", err))
	})

	t.Run("a bound join reads as the join of its bound branches", func(t *testing.T) {
		t.Parallel()

		bound := yamltest.Bind(t, source, errors.Join(badA, summary))
		joined := errors.Join(yamltest.Bind(t, source, badA), yamltest.Bind(t, source, summary))

		assert.Equal(t, joined.Error(), bound.Error())
	})
}

// keyErrors returns n errors, one at each key from index first on, as
// [yamltest.ErrorAt] creates them. The format names the key of an index,
// such as "k%d", and the message of each error is "bad " and its key.
func keyErrors(format string, first, n int) []error {
	errs := make([]error, 0, n)
	for i := first; i < first+n; i++ {
		key := fmt.Sprintf(format, i)
		errs = append(errs, yamltest.ErrorAt("bad "+key, key))
	}

	return errs
}

func TestSourceError_Error_ListLimit(t *testing.T) {
	t.Parallel()

	input := yamltest.GenerateLines(2*niceyaml.ErrorListLimit, "k%d: 0")

	// Located returns n errors, one per key of the input from the key at
	// first on.
	located := func(first, n int) []error { return keyErrors("k%d", first, n) }

	// Lines returns the line of each of n errors from the key at first on.
	lines := func(name string, first, n int) []string {
		out := make([]string, 0, n)
		for i := first; i < first+n; i++ {
			out = append(out, fmt.Sprintf("%s%d:5: $.k%d: bad k%d", name, i+1, i, i))
		}

		return out
	}

	limit := niceyaml.ErrorListLimit

	tcs := map[string]struct {
		err  error
		name string
		want []string
		// The number of problems the tree lists.
		problems int
	}{
		"list at the limit is whole": {
			name:     "f.yaml",
			err:      niceyaml.NewSummary("summary", located(0, limit)...),
			want:     append([]string{"f.yaml: summary"}, lines("f.yaml:", 0, limit)...),
			problems: limit,
		},
		"list past the limit counts the rest": {
			name:     "f.yaml",
			err:      niceyaml.NewSummary("summary", located(0, limit+2)...),
			want:     append(append([]string{"f.yaml: summary"}, lines("f.yaml:", 0, limit)...), "f.yaml: and 2 more"),
			problems: limit + 2,
		},
		"join past the limit counts the rest": {
			name:     "f.yaml",
			err:      errors.Join(located(0, limit+1)...),
			want:     append(lines("f.yaml:", 0, limit), "f.yaml: and 1 more"),
			problems: limit + 1,
		},
		"source with no name counts the rest without one": {
			err:      niceyaml.NewSummary("summary", located(0, limit+2)...),
			want:     append(append([]string{"summary"}, lines("", 0, limit)...), "and 2 more"),
			problems: limit + 2,
		},
		"lists below the list share its limit": {
			name: "f.yaml",
			err: niceyaml.NewSummary("summary",
				niceyaml.NewSummary("first", located(0, limit-2)...),
				niceyaml.NewSummary("second", located(limit, limit)...),
			),
			want: append(
				append([]string{"f.yaml: summary", "f.yaml: first"}, lines("f.yaml:", 0, limit-2)...),
				"f.yaml: second",
				"f.yaml: and "+strconv.Itoa(limit)+" more",
			),
			problems: 2*limit - 2,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			source := niceyaml.NewSourceFromString(input, niceyaml.WithName(tc.name))
			err := yamltest.Bind(t, source, tc.err)

			require.Error(t, err)
			assert.Equal(t, stringtest.JoinLF(tc.want...), err.Error())

			// The tree holds every error, whatever the message leaves out.
			assert.Len(t, slices.Collect(niceyaml.NewErrorTree(err).Problems()), tc.problems)
		})
	}
}

func TestSourceError_Format_ListLimit(t *testing.T) {
	t.Parallel()

	limit := niceyaml.ErrorListLimit

	input := yamltest.GenerateLines(2*limit, "k%02d: 0")
	source := niceyaml.NewSourceFromString(input, niceyaml.WithName("f.yaml"))
	other := niceyaml.NewSourceFromString("port: 0\n", niceyaml.WithName("other.yaml"))

	key := func(i int) string { return fmt.Sprintf("k%02d", i) }

	// Located returns n errors, one per key of the input from the key at
	// first on.
	located := func(first, n int) []error { return keyErrors("k%02d", first, n) }

	// Bind returns each of errs bound to the source.
	bind := func(errs ...error) []error {
		bound := make([]error, 0, len(errs))
		for _, err := range errs {
			bound = append(bound, yamltest.Bind(t, source, err))
		}

		return bound
	}

	// A binding that the first problem and the last share as a detail.
	declared := yamltest.Bind(t, source, yamltest.ErrorAt("declared here", key(15)))

	// Rows returns the row of the tree for each of those errors, behind
	// prefix.
	rows := func(prefix string, first, n int) []string {
		out := make([]string, 0, n)
		for i := first; i < first+n; i++ {
			out = append(out, fmt.Sprintf("%s%d:6: $.%s: bad %s", prefix, i+1, key(i), key(i)))
		}

		return out
	}

	// Lines returns the rows of an excerpt for lines first up to end of
	// the input, with the message of each line that marks holds below
	// it.
	lines := func(first, end int, marks map[int]string) []string {
		var out []string

		for i := first; i < end; i++ {
			out = append(out, fmt.Sprintf("%4d | %s: 0", i+1, key(i)))

			if msg, ok := marks[i]; ok {
				out = append(out, "     |      ^ "+msg)
			}
		}

		return out
	}

	// The messages of the first limit problems, each on its own line.
	firstMarks := make(map[int]string, limit)
	for i := range limit {
		firstMarks[i] = "bad " + key(i)
	}

	// The excerpt of the first limit problems, with two context lines
	// after them.
	firstLines := lines(0, limit+2, firstMarks)

	tcs := map[string]struct {
		err error
		// The rows of the tree the %+v verb and LogValue draw.
		tree []string
		// The rows of the excerpts the %+v verb draws below the tree.
		excerpt []string
		// The number of problems FormatError draws.
		problems int
	}{
		"problems at the limit are whole": {
			err:      niceyaml.NewSummary("summary", located(0, limit)...),
			tree:     append([]string{"f.yaml: summary"}, branches(rows("", 0, limit)...)...),
			excerpt:  firstLines,
			problems: limit,
		},
		"problems past the limit count as more": {
			err:      niceyaml.NewSummary("summary", located(0, limit+2)...),
			tree:     append([]string{"f.yaml: summary"}, branches(append(rows("", 0, limit), "and 2 more")...)...),
			excerpt:  firstLines,
			problems: limit + 2,
		},
		"join past the limit counts the rest": {
			err:      errors.Join(located(0, limit+1)...),
			tree:     branches(append(rows("f.yaml:", 0, limit), "and 1 more")...),
			excerpt:  firstLines,
			problems: limit + 1,
		},
		"details stay with their problem and count as none": {
			err: niceyaml.NewSummary("summary", append([]error{
				yamltest.ErrorAt("bad k00", key(0)).With(niceyaml.WithDetails(
					errors.New("reason one"),
					errors.New("reason two"),
					yamltest.ErrorAt("declared here", key(15)),
				)),
			}, located(1, limit+1)...)...),
			tree: slices.Concat(
				[]string{
					"f.yaml: summary",
					"|-- 1:6: $.k00: bad k00",
					"|   |-- 16:6: $.k15: declared here",
					"|   |-- reason one",
					"|   `-- reason two",
				},
				branches(append(rows("", 1, limit-1), "and 2 more")...),
			),
			excerpt: slices.Concat(
				firstLines,
				[]string{"     | ..."},
				lines(13, 18, map[int]string{15: "declared here"}),
			),
			problems: limit + 2,
		},
		"lists below a list share the limit": {
			err: niceyaml.NewSummary("summary",
				niceyaml.NewSummary("first", located(0, limit-2)...),
				niceyaml.NewSummary("second", located(limit-2, limit)...),
			),
			tree: slices.Concat(
				[]string{"f.yaml: summary", "|-- first"},
				indent("|   ", branches(rows("", 0, limit-2)...)),
				[]string{"|-- second"},
				indent("|   ", branches(rows("", limit-2, 2)...)),
				[]string{fmt.Sprintf("`-- and %d more", limit-2)},
			),
			excerpt:  firstLines,
			problems: 2*limit - 2,
		},
		"heading with no problem left goes": {
			err: niceyaml.NewSummary("summary",
				niceyaml.NewSummary("first", located(0, limit)...),
				niceyaml.NewSummary("second", located(limit, 3)...),
			),
			tree: slices.Concat(
				[]string{"f.yaml: summary", "|-- first"},
				indent("|   ", branches(rows("", 0, limit)...)),
				[]string{"`-- and 3 more"},
			),
			excerpt:  firstLines,
			problems: limit + 3,
		},
		"problem left out in another source shows no excerpt of it": {
			err: errors.Join(append(bind(located(0, limit)...), yamltest.Bind(t, other,
				yamltest.ErrorAt("bad port", "port"),
			))...),
			tree:     branches(append(rows("f.yaml:", 0, limit), "and 1 more")...),
			excerpt:  firstLines,
			problems: limit + 1,
		},
		"binding a shown problem shares with one left out stays marked": {
			err: niceyaml.NewSummary("summary", slices.Concat(
				[]error{yamltest.ErrorAt("bad k00", key(0)).With(niceyaml.WithDetails(declared))},
				located(1, limit-1),
				[]error{yamltest.ErrorAt("bad k10", key(limit)).With(niceyaml.WithDetails(declared))},
			)...),
			tree: slices.Concat(
				[]string{
					"f.yaml: summary",
					"|-- 1:6: $.k00: bad k00",
					"|   `-- 16:6: $.k15: declared here",
				},
				branches(append(rows("", 1, limit-1), "and 1 more")...),
			),
			excerpt: slices.Concat(
				firstLines,
				[]string{"     | ..."},
				lines(13, 18, map[int]string{15: "declared here"}),
			),
			problems: limit + 1,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			err := yamltest.Bind(t, source, tc.err)

			tree := stringtest.JoinLF(tc.tree...)

			assert.Equal(t, tree+"\n\n"+stringtest.JoinLF(tc.excerpt...), fmt.Sprintf("%+v", err))
			assert.Equal(t, tree, logged(t, err))

			// FormatError draws every problem, however many there are.
			all := niceyaml.FormatError(err)

			assert.Equal(t, tc.problems, strings.Count(all, ": bad "))
			assert.NotContains(t, all, " more")
		})
	}
}

// branches returns rows as the branches of a tree, each behind its
// connector.
func branches(rows ...string) []string {
	out := make([]string, 0, len(rows))

	for i, row := range rows {
		connector := "|-- "
		if i == len(rows)-1 {
			connector = "`-- "
		}

		out = append(out, connector+row)
	}

	return out
}

// indent returns rows with prefix in front of each.
func indent(prefix string, rows []string) []string {
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		out = append(out, prefix+row)
	}

	return out
}

// errorLocation holds what the accessors of an [*niceyaml.Error] report:
// a path, and a position or a range, each nil when its accessor reports
// false.
type errorLocation struct {
	path any //nolint:unused // Only reflect reads the field.
	pos  any
	rng  any
}

// locationOf reads the accessors of e. It fails the test when e reports
// both a position and a range.
func locationOf(t *testing.T, e *niceyaml.Error) errorLocation {
	t.Helper()

	var got errorLocation

	if p, ok := e.Path(); ok {
		got.path = p
	}

	if p, ok := e.Position(); ok {
		got.pos = p
	}

	if r, ok := e.Range(); ok {
		got.rng = r
	}

	require.False(t, got.pos != nil && got.rng != nil, "an Error reports a position or a range, not both")

	return got
}

func TestError_Location(t *testing.T) {
	t.Parallel()

	path := paths.Current().Child("foo")
	pos := position.New(1, 4)
	rng := yamltest.LineRange(1, 2, 5)

	tcs := map[string]struct {
		err  *niceyaml.Error
		want errorLocation
	}{
		"nil": {
			err: nil,
		},
		"no location": {
			err: niceyaml.NewError("test"),
		},
		"path": {
			err:  yamltest.ErrorAt("test", "foo"),
			want: errorLocation{path: path},
		},
		"key": {
			err:  niceyaml.NewError("test", niceyaml.AtPath(path.Key())),
			want: errorLocation{path: path.Key()},
		},
		"key replaces a path": {
			err:  niceyaml.NewError("test", niceyaml.AtPath(path), niceyaml.AtPath(path.Key())),
			want: errorLocation{path: path.Key()},
		},
		"position": {
			err:  niceyaml.NewError("test", niceyaml.AtPosition(pos)),
			want: errorLocation{pos: pos},
		},
		"range": {
			err:  niceyaml.NewError("test", niceyaml.AtRange(rng)),
			want: errorLocation{rng: rng},
		},
		"a path and a position combine": {
			err:  niceyaml.NewError("test", niceyaml.AtPath(path), niceyaml.AtPosition(pos)),
			want: errorLocation{path: path, pos: pos},
		},
		"a path and a range combine in either order": {
			err:  niceyaml.NewError("test", niceyaml.AtRange(rng), niceyaml.AtPath(path)),
			want: errorLocation{path: path, rng: rng},
		},
		"a range replaces a position": {
			err:  niceyaml.NewError("test", niceyaml.AtPosition(pos), niceyaml.AtRange(rng)),
			want: errorLocation{rng: rng},
		},
		"a position replaces a range": {
			err:  niceyaml.NewError("test", niceyaml.AtRange(rng), niceyaml.AtPosition(pos)),
			want: errorLocation{pos: pos},
		},
		"path on a wrapped error": {
			err: wrapError(t,
				fmt.Errorf("context: %w", yamltest.ErrorAt("test", "foo")),
			),
			want: errorLocation{path: path},
		},
		"position on a wrapped error": {
			err: wrapError(t,
				fmt.Errorf("context: %w", niceyaml.NewError("test", niceyaml.AtPosition(pos))),
			),
			want: errorLocation{pos: pos},
		},
		"range on a wrapped error": {
			err: wrapError(t,
				fmt.Errorf("context: %w", niceyaml.NewError("test", niceyaml.AtRange(rng))),
			),
			want: errorLocation{rng: rng},
		},
		"a path and a range on a wrapped error": {
			err: wrapError(t,
				fmt.Errorf("context: %w", niceyaml.NewError("test", niceyaml.AtPath(path), niceyaml.AtRange(rng))),
			),
			want: errorLocation{path: path, rng: rng},
		},
		"own location wins over a wrapped one": {
			err: wrapError(t,
				yamltest.ErrorAt("test", "foo"),
				niceyaml.AtPosition(pos),
			),
			want: errorLocation{pos: pos},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, locationOf(t, tc.err))
		})
	}
}

func TestError_Unwrap(t *testing.T) {
	t.Parallel()

	// Each case lists the message of every error Unwrap returns, and a
	// case with no messages wants nil. The cause of an Error with an
	// empty message is an error of its own with that message.
	tcs := map[string]struct {
		err  *niceyaml.Error
		want []string
	}{
		"the zero Error unwraps to nil": {
			err: &niceyaml.Error{},
		},
		"unwraps underlying error": {
			err:  wrapError(t, errors.New("underlying error")),
			want: []string{"underlying error"},
		},
		"an empty message unwraps to its cause": {
			err:  niceyaml.NewError(""),
			want: []string{""},
		},
		"an empty message with details unwraps to its cause": {
			err:  niceyaml.NewError("", niceyaml.WithDetails(errors.New("nested error"))),
			want: []string{""},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			unwrapped := tc.err.Unwrap()
			if tc.want == nil {
				assert.Nil(t, unwrapped)

				return
			}

			var got []string

			for _, err := range unwrapped {
				got = append(got, err.Error())
			}

			assert.Equal(t, tc.want, got)
		})
	}
}

func TestError_ChainComparable(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		err func(t *testing.T) error
	}{
		"a decode rejection": {
			err: func(t *testing.T) error {
				t.Helper()

				var v struct {
					N int `yaml:"n"`
				}

				return yamltest.FirstDocument(t, "n: [1]\n").DecodeInto(t.Context(), &v)
			},
		},
		"a syntax error": {
			err: func(*testing.T) error {
				_, err := niceyaml.NewSourceFromString("a: [\n").File()

				return err
			},
		},
		"a path that selects nothing": {
			err: func(t *testing.T) error {
				t.Helper()

				_, err := yamltest.FirstDocument(t, "a: 1\n").At(paths.Doc().Child("b"))

				return err //nolint:wrapcheck // The test inspects the error as it is.
			},
		},
		"a panic in an unmarshaler": {
			err: func(t *testing.T) error {
				t.Helper()

				ctx := context.WithValue(t.Context(), panicValueKey{}, "boom")

				var v panicsWith

				return yamltest.FirstDocument(t, "a: 1\n").DecodeInto(ctx, &v)
			},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			err := tc.err(t)
			require.Error(t, err)

			// Every error in the chain works as a map key, as a caller that
			// dedupes errors uses it.
			seen := map[error]bool{}

			var walk func(error)

			walk = func(err error) {
				if err == nil {
					return
				}

				seen[err] = true

				switch x := err.(type) { //nolint:errorlint // Walks the chain one node at a time.
				case interface{ Unwrap() []error }:
					for _, branch := range x.Unwrap() {
						walk(branch)
					}

				case interface{ Unwrap() error }:
					walk(x.Unwrap())
				}
			}

			assert.NotPanics(t, func() { walk(err) })
			assert.NotEmpty(t, seen)
		})
	}
}

func TestError_MultiError(t *testing.T) {
	t.Parallel()

	record := stringtest.Input(`
		name: test
		value: 123
		other: data
	`)
	pair := stringtest.Input(`
		key: value
		foo: bar
	`)

	// The tokens of the keys of pair, for the errors that carry the
	// position of a token.
	tokens := lexer.Tokenize(pair)
	keyToken := tokens[0]
	fooIndex := slices.IndexFunc(tokens, func(tk *token.Token) bool { return tk.Value == "foo" })
	require.NotEqual(t, -1, fooIndex, "failed to find foo token")

	fooToken := tokens[fooIndex]

	namePath := paths.Current().Child("name")
	otherPath := paths.Current().Child("other")
	keyPath := paths.Current().Child("key")
	missingPath := paths.Current().Child("nonexistent")

	tcs := map[string]struct {
		source string
		err    error
		// The message part the rendering opens with, when the case pins it.
		wantPrefix string
		// The text the rendering holds, and the text it leaves out.
		want    []string
		wantNot []string
	}{
		"single nested error with path": {
			source: record,
			err: niceyaml.NewError("validation failed", niceyaml.AtPath(namePath.Key()), niceyaml.WithDetails(
				yamltest.ErrorAt("invalid type", "value"),
			)),
			// The output highlights the main token and annotates the nested error.
			want: []string{
				"<genericError>name</genericError>",
				"<genericError>123</genericError>",
				"^ invalid type",
			},
		},
		"multiple nested errors on different lines": {
			source: record,
			err: niceyaml.NewError("validation failed", niceyaml.AtPath(namePath.Key()), niceyaml.WithDetails(
				yamltest.ErrorAt("invalid type", "value"),
				niceyaml.NewError("missing field", niceyaml.AtPath(otherPath.Key())),
			)),
			// The output holds both annotations.
			want: []string{"^ invalid type", "^ missing field"},
		},
		"multiple nested errors on same line": {
			source: "key: value",
			// Both errors point to the same line.
			err: niceyaml.NewError(
				"validation failed",
				niceyaml.AtPosition(position.NewFromToken(keyToken)),
				niceyaml.WithDetails(
					niceyaml.NewError("error1", niceyaml.AtPath(keyPath.Key())),
					yamltest.ErrorAt("error2", "key"),
				),
			),
			// Each error on the same line gets a row under its own range.
			want: []string{"^^^ error1<", "     ^^^^^ error2<"},
		},
		"nested errors on same line out of column order": {
			source: "key: [alpha, beta]",
			// The children arrive in reverse column order.
			err: niceyaml.NewSummary(
				"2 problems",
				niceyaml.NewError("bad beta", niceyaml.AtPath(keyPath.Index(1))),
				niceyaml.NewError("bad alpha", niceyaml.AtPath(keyPath.Index(0))),
			),
			// The rows follow the column order of the carets.
			want: []string{"^^^^^ bad alpha<", "<textError>             ^^^^ bad beta<"},
		},
		"nested errors that repeat a message": {
			source: "key: [alpha, beta]",
			err: niceyaml.NewSummary(
				"4 problems",
				niceyaml.NewError("too long", niceyaml.AtPath(keyPath.Index(0))),
				niceyaml.NewError("too long", niceyaml.AtPath(keyPath.Index(1))),
				niceyaml.NewError("too long", niceyaml.AtPath(keyPath.Index(0))),
				niceyaml.NewError("no digit", niceyaml.AtPath(keyPath.Index(0))),
			),
			// The message reads once on the range it repeats on, joined
			// with the other message there, and again on the other range.
			want: []string{"^^^^^ too long; no digit<", "<textError>             ^^^^ too long<"},
		},
		"nested error with direct token": {
			source: pair,
			err: niceyaml.NewError(
				"main error",
				niceyaml.AtPosition(position.NewFromToken(keyToken)),
				niceyaml.WithDetails(
					niceyaml.NewError("nested with token", niceyaml.AtPosition(position.NewFromToken(fooToken))),
				),
			),
			// The output highlights both tokens.
			want: []string{
				"<genericError>key</genericError>",
				"<genericError>foo</genericError>",
				"^ nested with token",
			},
		},
		"nested error that does not resolve stays in the message": {
			source: "key: value",
			err: niceyaml.NewError("main error", niceyaml.AtPath(keyPath.Key()), niceyaml.WithDetails(
				niceyaml.NewError("nested error", niceyaml.AtPath(missingPath.Key())),
			)),
			// The nested error has no line to annotate, so the message is the
			// only place it appears.
			wantPrefix: stringtest.LinesLF("1:1: $.key~: main error", "└── $.nonexistent~: nested error", ""),
			want:       []string{"<genericError>key</genericError>"},
			wantNot:    []string{"^ nested error", "\n\n$.nonexistent~"},
		},
		"nested error without path or token stays in the message": {
			source: "key: value",
			err: niceyaml.NewError("main error", niceyaml.AtPath(keyPath.Key()), niceyaml.WithDetails(
				niceyaml.NewError("no location"),
			)),
			wantPrefix: stringtest.LinesLF("1:1: $.key~: main error", "└── no location", ""),
			wantNot:    []string{"^ no location", "\n\nno location"},
		},
		"nested-only error renders with annotations": {
			source: stringtest.Input(`
				name: test
				value: 123
			`),
			// The error has no path of its own, and its nested error has one.
			err: niceyaml.NewError("validation failed at 1 location", niceyaml.WithDetails(
				yamltest.ErrorAt("got number, want string", "value"),
			)),
			// The output holds the main message, the annotation of the nested
			// error, the YAML content as well as the tree, and the highlight
			// on the value of the nested error.
			want: []string{
				"validation failed at 1 location",
				"got number, want string",
				"value",
				"123",
				"<genericError>123</genericError>",
			},
		},
		"nested-only error with multiple lines": {
			source: record,
			// The nested errors sit on different lines.
			err: niceyaml.NewSummary(
				"validation failed at 2 locations",
				yamltest.ErrorAt("type error on value", "value"),
				niceyaml.NewError("unexpected property", niceyaml.AtPath(otherPath.Key())),
			),
			// The output holds both annotations and highlights both locations.
			want: []string{
				"type error on value",
				"unexpected property",
				"<genericError>123</genericError>",
				"<genericError>other</genericError>",
			},
		},
		"nested-only error with resolvable and unresolvable nested errors": {
			source: stringtest.Input(`
				name: test
				value: 123
			`),
			// Some nested errors resolve and some do not.
			err: niceyaml.NewSummary(
				"validation failed",
				yamltest.ErrorAt("resolvable error", "value"),
				niceyaml.NewError("unresolvable error", niceyaml.AtPath(missingPath.Key())),
			),
			// Both nested errors are part of the message, and only the one that
			// resolves annotates the excerpt.
			wantPrefix: stringtest.LinesLF(
				"validation failed",
				"├── 2:8: $.value: resolvable error",
				"└── $.nonexistent~: unresolvable error",
				"",
			),
			want:    []string{"^ resolvable error"},
			wantNot: []string{"^ unresolvable error", "\n\n$.nonexistent~"},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			err := yamltest.Bind(t, niceyaml.NewSourceFromString(tc.source), tc.err)

			got := render(err)

			assert.True(t, strings.HasPrefix(got, tc.wantPrefix), got)

			for _, want := range tc.want {
				assert.Contains(t, got, want)
			}

			for _, wantNot := range tc.wantNot {
				assert.NotContains(t, got, wantNot)
			}
		})
	}

	t.Run("plain error with nested errors keeps its own message", func(t *testing.T) {
		t.Parallel()

		nested1 := niceyaml.NewError("nested 1")
		nested2 := niceyaml.NewError("nested 2")

		summary := yamltest.RequireError(t, niceyaml.NewSummary("main error", nested1, nested2))

		detailed := niceyaml.NewError("main error", niceyaml.WithDetails(nested1, nested2))

		// The errors below are structure, not text. Errors returns the
		// errors a summary heads and Details the details of a problem,
		// while the message stays one line and %+v lists them as a tree
		// under it.
		for _, err := range []*niceyaml.Error{summary, detailed} {
			assert.Equal(t, "main error", err.Error())
			assert.Equal(t, stringtest.JoinLF(
				"main error",
				"|-- nested 1",
				"`-- nested 2",
			), render(err))
		}

		// A summary matches each problem it heads, and a problem matches
		// none of its details.
		require.ErrorIs(t, summary, nested1)
		require.ErrorIs(t, summary, nested2)
		require.NotErrorIs(t, detailed, nested1)
		require.NotErrorIs(t, detailed, nested2)

		assert.Equal(t, []error{nested1, nested2}, yamltest.ErrsOf(yamltest.ProblemsOf(summary)))
		assert.Empty(t, yamltest.DetailsOf(summary))
		assert.Equal(t, []error{nested1, nested2}, yamltest.ErrsOf(yamltest.DetailsOf(detailed)))
		assert.Empty(t, yamltest.ProblemsOf(detailed))
	})

	t.Run("errors.As works with the errors a summary heads", func(t *testing.T) {
		t.Parallel()

		customErr := &customTestError{msg: "custom error"}

		err := niceyaml.NewSummary(
			"main",
			niceyaml.NewError("other"),
			niceyaml.Invalid(customErr),
		)

		var target *customTestError

		require.ErrorAs(t, err, &target)
		assert.Equal(t, "custom error", target.msg)
	})

	t.Run("errors.As passes over a detail", func(t *testing.T) {
		t.Parallel()

		customErr := &customTestError{msg: "custom error"}

		err := niceyaml.NewError(
			"main",
			niceyaml.WithDetails(
				niceyaml.Invalid(customErr),
			),
		)

		var target *customTestError

		require.NotErrorAs(t, err, &target)

		// The detail holds the custom error, and a caller reads it there.
		require.ErrorAs(t, yamltest.DetailsOf(err)[0].Err, &target)
		assert.Equal(t, "custom error", target.msg)
	})

	t.Run("nested-only error without source falls back to plain", func(t *testing.T) {
		t.Parallel()

		// The error has no path and no source, and its nested error has a path.
		err := niceyaml.NewError(
			"validation failed",
			niceyaml.WithDetails(
				yamltest.ErrorAt("nested error", "value"),
			),
		)

		// Without a source, the tree renders with no excerpt, and the
		// nested error waits for a binding to put its position in front.
		assert.Equal(t, "validation failed", err.Error())
		assert.Equal(t, stringtest.JoinLF("validation failed", "`-- @.value: nested error"), render(err))
	})
}

func TestSourceError_Excerpt_NestedLocations(t *testing.T) {
	t.Parallel()

	// A nested error with a location annotates the source excerpt. A nested
	// error without one appears in the message alone.

	source := stringtest.Input(`
		key: value
		other: data
	`)

	tcs := map[string]struct {
		err  error
		want string
	}{
		"no nested errors": {
			err:  niceyaml.NewError("main error"),
			want: "main error",
		},
		"nested errors without locations": {
			err: niceyaml.NewSummary(
				"main error",
				niceyaml.NewError("nested 1"),
				niceyaml.NewError("nested 2"),
			),
			want: stringtest.JoinLF(
				"main error",
				"├── nested 1",
				"└── nested 2",
			),
		},
		"detail without a location": {
			err: niceyaml.NewError("main error", niceyaml.WithDetails(
				niceyaml.NewError("nested without location"),
			)),
			want: stringtest.JoinLF(
				"main error",
				"└── nested without location",
			),
		},
		"nested error with path": {
			err: niceyaml.NewError(
				"main error",
				niceyaml.WithDetails(
					niceyaml.NewError(
						"nested with path",
						niceyaml.AtPath(paths.Current().Child("key").Key()),
					),
				),
			),
			want: stringtest.JoinLF(
				"main error",
				"└── 1:1: $.key~: nested with path",
				"",
				"<genericError>key</genericError><punctuationMappingValue>:</punctuationMappingValue><text> </text><literalString>value</literalString>",
				"<textError>^^^ nested with path</textError>",
				"<nameTag>other</nameTag><punctuationMappingValue>:</punctuationMappingValue><text> </text><literalString>data</literalString>",
			),
		},
		"nested error with token": {
			err: niceyaml.NewError(
				"main error",
				niceyaml.WithDetails(
					niceyaml.NewError(
						"nested with token",
						niceyaml.AtPosition(position.NewFromToken(lexer.Tokenize(source)[0])),
					),
				),
			),
			want: stringtest.JoinLF(
				"main error",
				"└── 1:1: nested with token",
				"",
				"<genericError>key</genericError><punctuationMappingValue>:</punctuationMappingValue><text> </text><literalString>value</literalString>",
				"<textError>^^^ nested with token</textError>",
				"<nameTag>other</nameTag><punctuationMappingValue>:</punctuationMappingValue><text> </text><literalString>data</literalString>",
			),
		},
		"mix of located and unlocated nested errors": {
			err: niceyaml.NewSummary(
				"main error",
				niceyaml.NewError("no path"),
				niceyaml.NewError(
					"has path",
					niceyaml.AtPath(paths.Current().Child("key").Key()),
				),
				niceyaml.NewError("also no path"),
			),
			want: stringtest.JoinLF(
				"main error",
				"├── 1:1: $.key~: has path",
				"├── no path",
				"└── also no path",
				"",
				"<genericError>key</genericError><punctuationMappingValue>:</punctuationMappingValue><text> </text><literalString>value</literalString>",
				"<textError>^^^ has path</textError>",
				"<nameTag>other</nameTag><punctuationMappingValue>:</punctuationMappingValue><text> </text><literalString>data</literalString>",
			),
		},
		"nil details only": {
			err:  niceyaml.NewError("main error", niceyaml.WithDetails(nil, nil)),
			want: "main error",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			err := yamltest.Bind(t, niceyaml.NewSourceFromString(source), tc.err)

			assert.Equal(t, tc.want, render(err))
		})
	}
}

func TestSourceError_UnresolvedNestedInTree(t *testing.T) {
	t.Parallel()

	source := niceyaml.NewSourceFromString("a: 1\n")
	err := yamltest.Bind(t, source, niceyaml.NewSummary(
		"2 schema violations",
		niceyaml.NewError("bad x", niceyaml.AtPath(paths.Current().Child("x").Index(0))),
		niceyaml.NewError("bad y", niceyaml.AtPath(paths.Current().Child("y").Index(0))),
	))

	// The message and the %+v verb list each nested error behind its
	// path, and since no location resolves, the printer adds nothing
	// beyond the connectors of the tree.
	assert.Equal(t, stringtest.JoinLF(
		"2 schema violations",
		"$.x[0]: bad x",
		"$.y[0]: bad y",
	), err.Error())
	assert.Equal(t, stringtest.JoinLF(
		"2 schema violations",
		"|-- $.x[0]: bad x",
		"`-- $.y[0]: bad y",
	), report(err))
	assert.Equal(t, stringtest.JoinLF(
		"2 schema violations",
		"├── $.x[0]: bad x",
		"└── $.y[0]: bad y",
	), render(err))

	// The summary carries no location of its own, so it has no reason to
	// give, and each nested error names why its path did not resolve.
	bound := yamltest.RequireSourceError(t, err)

	_, ok := bound.Excerpt()
	assert.False(t, ok)
	require.NoError(t, bound.Unresolved())

	children := yamltest.ProblemsOf(bound)
	require.Len(t, children, 2)

	for _, child := range children {
		require.ErrorIs(t, child.Bound.Unresolved(), paths.ErrNotFound)
	}
}

func TestSourceError_Format(t *testing.T) {
	t.Parallel()

	source := niceyaml.NewSourceFromString("a: 1\n")
	err := yamltest.Bind(t, source, yamltest.ErrorAt("x", "a"))

	tcs := map[string]struct {
		format string
		want   string
	}{
		"v prints the message": {
			format: "%v",
			want:   "1:4: $.a: x",
		},
		"s prints the message": {
			format: "%s",
			want:   "1:4: $.a: x",
		},
		"q quotes the message": {
			format: "%q",
			want:   `"1:4: $.a: x"`,
		},
		"width pads the message on the left": {
			format: "%13s",
			want:   "  1:4: $.a: x",
		},
		"minus flag pads the message on the right": {
			format: "%-13v",
			want:   "1:4: $.a: x  ",
		},
		"precision cuts the message": {
			format: "%.3s",
			want:   "1:4",
		},
		"sharp q quotes with backquotes": {
			format: "%#q",
			want:   "`1:4: $.a: x`",
		},
		"x prints the message in hex": {
			format: "%x",
			want:   "313a343a20242e613a2078",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, fmt.Sprintf(tc.format, err))
		})
	}
}

func TestSourceError_Format_Plain(t *testing.T) {
	t.Parallel()

	lines := stringtest.LinesLF("a: 1", "b: 2", "c: 3")

	namePath := paths.Current().Child("名前")

	tcs := map[string]struct {
		err   error
		input string
		want  string
	}{
		"marks the location": {
			input: lines,
			err:   yamltest.ErrorAt("bad value", "b"),
			want: stringtest.JoinLF(
				"2:4: $.b: bad value",
				"",
				"   1 | a: 1",
				"   2 | b: 2",
				"     |    ^",
				"   3 | c: 3",
			),
		},
		// Each CJK rune renders two cells wide, so the columns before the
		// marked ones are worth two carets each.
		"carets sit under a value behind wide runes": {
			input: "名前: value\n",
			err:   yamltest.ErrorAt("bad value", "名前"),
			want: stringtest.JoinLF(
				"1:5: $.名前: bad value",
				"",
				"   1 | 名前: value",
				"     |       ^^^^^",
			),
		},
		"carets sit under a key of wide runes": {
			input: "名前: value\n",
			err:   niceyaml.NewError("bad key", niceyaml.AtPath(namePath.Key())),
			want: stringtest.JoinLF(
				"1:1: $.名前~: bad key",
				"",
				"   1 | 名前: value",
				"     | ^^^^",
			),
		},
		"nested errors annotate their lines": {
			input: lines,
			err: niceyaml.NewSummary("2 problems",
				niceyaml.NewError("bad a", niceyaml.AtPath(paths.Current().Child("a").Key())),
				yamltest.ErrorAt("bad c", "c"),
			),
			want: stringtest.JoinLF(
				"2 problems",
				"|-- 1:1: $.a~: bad a",
				"`-- 3:4: $.c: bad c",
				"",
				"   1 | a: 1",
				"     | ^ bad a",
				"   2 | b: 2",
				"   3 | c: 3",
				"     |    ^ bad c",
			),
		},
		"names why no excerpt resolves": {
			input: "a: 1\n",
			err:   niceyaml.NewError("bad value", niceyaml.AtPath(paths.Current().Child("b").Index(0))),
			want:  stringtest.JoinLF("$.b[0]: bad value", "", "no excerpt: resolve $.b[0]: not found"),
		},
		"control characters render as pictures": {
			input: stringtest.LinesLF(`a: "x\ty"`),
			err: niceyaml.NewError("bad", niceyaml.AtRange(
				yamltest.LineRange(0, 3, 9),
			)),
			want: stringtest.JoinLF(
				"1:4: bad",
				"",
				`   1 | a: "x\ty"`,
				"     |    ^^^^^^",
			),
		},
		// The root carries no message in the excerpt, so the caret alone
		// marks where a location with nothing to highlight sits.
		"path to an empty value": {
			input: "a:\nb: 2\n",
			err:   yamltest.ErrorAt("bad", "a"),
			want: stringtest.JoinLF(
				"1:3: $.a: bad",
				"",
				"   1 | a:",
				"     |   ^",
				"   2 | b: 2",
			),
		},
		"path to an empty value before a comment": {
			input: "a: # c\nb: 2\n",
			err:   yamltest.ErrorAt("bad", "a"),
			want: stringtest.JoinLF(
				"1:3: $.a: bad",
				"",
				"   1 | a: # c",
				"     |   ^",
				"   2 | b: 2",
			),
		},
		"path to an empty value before spaces": {
			input: "a:   \nb: 2\n",
			err:   yamltest.ErrorAt("bad", "a"),
			want: stringtest.JoinLF(
				"1:3: $.a: bad",
				"",
				"   1 | a:   ",
				"     |   ^",
				"   2 | b: 2",
			),
		},
		"path to an empty element before a comment": {
			input: "- # c\n- 1\n",
			err:   niceyaml.NewError("bad", niceyaml.AtPath(paths.Current().Index(0))),
			want: stringtest.JoinLF(
				"1:2: $[0]: bad",
				"",
				"   1 | - # c",
				"     |  ^",
				"   2 | - 1",
			),
		},
		"position past the end of its line": {
			input: "a: 1\n",
			err:   niceyaml.NewError("bad", niceyaml.AtPosition(position.New(0, 500))),
			want: stringtest.JoinLF(
				"1:501: bad",
				"",
				"   1 | a: 1",
				"     |     ^",
			),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			err := yamltest.Bind(t, niceyaml.NewSourceFromString(tc.input), tc.err)

			got := fmt.Sprintf("%+v", err)

			assert.Equal(t, tc.want, got)
			assert.NotContains(t, got, "\x1b")
		})
	}

	t.Run("distant locations render as hunks", func(t *testing.T) {
		t.Parallel()

		assert.Equal(t, stringtest.JoinLF(
			"2:4: $.b: bad b",
			"`-- 8:4: $.h: bad h",
			"",
			"   1 | a: 1",
			"   2 | b: 2",
			"     |    ^",
			"   3 | c: 3",
			"   4 | d: 4",
			"     | ...",
			"   6 | f: 6",
			"   7 | g: 7",
			"   8 | h: 8",
			"     |    ^ bad h",
			"   9 | i: 9",
			"  10 | j: 10",
		), fmt.Sprintf("%+v", excerptError(t)))
	})

	t.Run("control characters in the tree and the reason render as pictures", func(t *testing.T) {
		t.Parallel()

		// The message and the path spell a key of the document. An
		// escape sequence in the message renders as its picture, and the
		// path writes one as an escape, in the tree and in the reason a
		// location did not resolve.
		src := niceyaml.NewSourceFromString("a: 1\n", niceyaml.WithName("f"))
		err := yamltest.Bind(t, src, niceyaml.NewError(
			"m\x1b[31mX",
			niceyaml.AtPath(paths.Current().Child("k\x1b[31mY").Index(0)),
		))

		got := niceyaml.FormatError(err, niceyaml.WithContextLines(0))
		assert.NotContains(t, got, "\x1b")
		assert.Contains(t, got, "m\u241b[31mX")
		assert.Contains(t, got, `no excerpt: resolve $.'k\u001b[31mY'`)
	})
}

func TestError_NilReceiver(t *testing.T) {
	t.Parallel()

	// A nil *Error carries no message, unwraps to nil, logs nothing, holds
	// no problem or detail, and specializes to nothing, in line with
	// Reported, Errors, and Path, which all accept nil.
	var missing *niceyaml.Error

	assert.Empty(t, missing.Error())
	assert.Nil(t, missing.With(niceyaml.AtPath(paths.Current().Child("a"))))
	assert.Nil(t, missing.Unwrap())
	assert.Empty(t, missing.LogValue().String())
	assert.Empty(t, yamltest.ProblemsOf(missing))
	assert.Empty(t, yamltest.DetailsOf(missing))

	// A chain that holds a nil Error behind a real one is safe to walk.
	outer := niceyaml.Invalid(fmt.Errorf("check: %w", missing))

	var bound *niceyaml.SourceError

	assert.NotErrorAs(t, outer, &bound)
}

func TestSourceError_NilReceiver(t *testing.T) {
	t.Parallel()

	// A nil *SourceError reaches a reader through errors.As on a chain that
	// holds one, so every reader of one accepts nil the way Unwrap does.
	src := niceyaml.NewSourceFromString("a: 1\n")

	var missing *niceyaml.SourceError

	assert.Empty(t, missing.Error())
	assert.Empty(t, missing.Message())
	assert.Empty(t, fmt.Sprintf("%+v", missing))
	assert.Empty(t, missing.LogValue().String())
	assert.Nil(t, missing.Source())
	assert.Empty(t, yamltest.ProblemsOf(missing))

	_, hasPath := missing.Path()
	assert.False(t, hasPath)

	near, hasNearest := missing.Nearest()
	assert.False(t, hasNearest)
	assert.Equal(t, paths.Current(), near)

	node, hasNode := missing.Node()
	assert.Nil(t, node)
	assert.False(t, hasNode)

	doc, hasDocument := missing.Document()
	assert.Nil(t, doc)
	assert.False(t, hasDocument)

	index, bound := missing.DocumentIndex()
	assert.Zero(t, index)
	assert.False(t, bound)
	assert.NoError(t, missing.Unwrap()) //nolint:testifylint // Asserts the nil, not a test failure.

	pos, positioned := missing.Position()
	assert.Zero(t, pos)
	require.False(t, positioned)

	rng, resolved := missing.Range()
	assert.Zero(t, rng)
	require.False(t, resolved)
	require.NoError(t, missing.Unresolved())

	_, excerpted := missing.Excerpt()
	require.False(t, excerpted)

	require.False(t, missing.Annotate(src.View()))
}

func TestSourceError_EmptyDocument(t *testing.T) {
	t.Parallel()

	// An explicitly empty document is the null document a schema validates,
	// so an error at the root of one resolves to its header, even when
	// comments sit below the header.
	tcs := map[string]struct {
		input string
		want  string
	}{
		"nothing below the header": {
			input: stringtest.LinesLF("a: 1", "---"),
			want: stringtest.JoinLF(
				"2:1: $: required property 'a' missing",
				"",
				"   1 | a: 1",
				"   2 | ---",
				"     | ^^^",
			),
		},
		"a comment below the header": {
			input: stringtest.LinesLF("a: 1", "---", "# comment"),
			want: stringtest.JoinLF(
				"2:1: $: required property 'a' missing",
				"",
				"   1 | a: 1",
				"   2 | ---",
				"     | ^^^",
				"   3 | # comment",
			),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			docs := yamltest.AllDocuments(t, niceyaml.NewSourceFromString(tc.input), 2)

			bound := docs[1].Bind(niceyaml.NewError("required property 'a' missing", niceyaml.AtPath(paths.Current())))

			se := yamltest.RequireSourceError(t, bound)
			assert.Equal(t, 1, yamltest.RangeOf(t, se).Start.Line)
			assert.Equal(t, "2:1: $: required property 'a' missing", se.Error())
			assert.Equal(t, tc.want, fmt.Sprintf("%+v", bound))
		})
	}
}

func TestError_EmptyInnerMessage(t *testing.T) {
	t.Parallel()

	err := niceyaml.Invalid(niceyaml.NewError(""))
	assert.Empty(t, err.Error())

	wrapped := yamltest.Bind(t, niceyaml.NewSourceFromString("a: 1\n"), err)
	assert.Empty(t, wrapped.Error())
	assert.Empty(t, render(wrapped))
}

func TestError_EmptyMessageWithLocation(t *testing.T) {
	t.Parallel()

	source := niceyaml.NewSourceFromString("a: 1\nb: 2\n")
	tk := source.Lines().TokenAt(position.New(0, 3))
	require.NotNil(t, tk)

	// An Error with an empty message has no text, whatever location it
	// carries. FormatError names the path alone, and binding puts the
	// resolved position and the path in front.
	tcs := map[string]struct {
		err       *niceyaml.Error
		wantTree  string
		wantBound string
	}{
		"token": {
			err:       niceyaml.NewError("", niceyaml.AtPosition(position.NewFromToken(tk))),
			wantTree:  "",
			wantBound: "1:4:",
		},
		"range": {
			err: niceyaml.NewError("",
				niceyaml.AtRange(yamltest.LineRange(1, 3, 4)),
			),
			wantTree:  "",
			wantBound: "2:4:",
		},
		"path": {
			err:       yamltest.ErrorAt("", "b"),
			wantTree:  "@.b:",
			wantBound: "2:4: $.b:",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Empty(t, tc.err.Error())
			assert.Equal(t, tc.wantTree, niceyaml.FormatError(tc.err, niceyaml.WithContextLines(0)))

			bound := yamltest.Bind(t, source, tc.err)
			assert.Equal(t, tc.wantBound, bound.Error())

			got := render(bound)
			assert.True(t, strings.HasPrefix(got, tc.wantBound+"\n\n"), got)
			assert.Contains(t, got, "genericError")
		})
	}
}

func TestError_WrapperDetails(t *testing.T) {
	t.Parallel()

	source := niceyaml.NewSourceFromString("a: 1\nb: 2\n")

	// The details of the wrapper become its children and annotate their
	// lines, and the wrapper still takes its position from the Error it
	// wraps.
	tcs := map[string]struct {
		detail error
		// The tree the %+v verb draws, and the rendering of the printer.
		wantReport string
		want       string
	}{
		"nested errors keep the inner position": {
			detail:     yamltest.ErrorAt("bad b", "b"),
			wantReport: stringtest.JoinLF("1:4: $.a: bad a", "`-- 2:4: $.b: bad b"),
			want: stringtest.JoinLF(
				"1:4: $.a: bad a",
				"└── 2:4: $.b: bad b",
				"",
				"<nameTag>a</nameTag><punctuationMappingValue>:</punctuationMappingValue><text> </text><genericError>1</genericError>",
				"<textError>   ^</textError>",
				"<nameTag>b</nameTag><punctuationMappingValue>:</punctuationMappingValue><text> </text><genericError>2</genericError>",
				"<textError>   ^ bad b</textError>",
			),
		},
		// A nested Error with an empty message names a location and
		// nothing else, so the printer highlights that location and marks
		// it with a caret run alone, with no message beside it.
		"nested location without a message": {
			detail:     yamltest.ErrorAt("", "b"),
			wantReport: stringtest.JoinLF("1:4: $.a: bad a", "`-- 2:4: $.b:"),
			want: stringtest.JoinLF(
				"1:4: $.a: bad a",
				"└── 2:4: $.b:",
				"",
				"<nameTag>a</nameTag><punctuationMappingValue>:</punctuationMappingValue><text> </text><genericError>1</genericError>",
				"<textError>   ^</textError>",
				"<nameTag>b</nameTag><punctuationMappingValue>:</punctuationMappingValue><text> </text><genericError>2</genericError>",
				"<textError>   ^</textError>",
			),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			wrapped := yamltest.Bind(
				t,
				source,
				niceyaml.Invalid(yamltest.ErrorAt("bad a", "a"), niceyaml.WithDetails(tc.detail)),
			)

			assert.Equal(t, "1:4: $.a: bad a", wrapped.Error())
			assert.Equal(t, tc.wantReport, report(wrapped))
			assert.Equal(t, position.New(0, 3), yamltest.RangeOf(t, yamltest.RequireSourceError(t, wrapped)).Start)
			assert.Equal(t, tc.want, render(wrapped))
		})
	}
}

func TestError_HunkDisplay(t *testing.T) {
	t.Parallel()

	// Numbered returns a source of n lines, "line1: a" first, each with the
	// next number and letter.
	numbered := func(n int) string {
		rows := make([]string, 0, n)
		for i := range n {
			rows = append(rows, fmt.Sprintf("line%d: %c", i+1, 'a'+rune(i)))
		}

		return stringtest.JoinLF(rows...)
	}

	// AtKey locates an error at the key of a line of the source.
	atKey := func(key string) niceyaml.ErrorOption {
		return niceyaml.AtPath(paths.Current().Child(key).Key())
	}

	// Some cases compare the whole rendering, and the others check which
	// source lines the excerpt shows, by the text the rendering holds and
	// the text it leaves out.
	tcs := map[string]struct {
		source       string
		err          error
		want         string
		wantContains []string
		wantNot      []string
		lines        int
	}{
		"single nested error shows correct context lines": {
			source: numbered(6),
			err: niceyaml.NewError("validation error", niceyaml.WithDetails(
				niceyaml.NewError("error at line3", atKey("line3")),
			)),
			lines: 1,
			// The excerpt shows context around line3.
			wantContains: []string{"line2", "line3", "line4"},
			wantNot:      []string{"line1", "line5"},
		},
		"nested errors on one line get a row each": {
			source: numbered(3),
			err: niceyaml.NewSummary(
				"validation error",
				niceyaml.NewError("error 1", atKey("line2")),
				yamltest.ErrorAt("error 2", "line2"),
			),
			lines: 0,
			// The excerpt shows line2 with both errors.
			wantContains: []string{"line2", "^^^^^ error 1", "       ^ error 2"},
		},
		"distant errors show separate hunks with separator": {
			source: numbered(10),
			// Errors at line1 and line10 with contextLines=1 fall in separate hunks.
			err: niceyaml.NewSummary(
				"validation error",
				niceyaml.NewError("error at start", atKey("line1")),
				niceyaml.NewError("error at end", atKey("line10")),
			),
			lines: 1,
			// The excerpt shows both errors, a "..." separator between the
			// hunks, and the context lines around the errors.
			wantContains: []string{"error at start", "error at end", "...", "line1", "line2", "line9", "line10"},
			// The excerpt leaves out lines 3-8.
			wantNot: []string{"line5"},
		},
		"position with no token under it still picks its excerpt": {
			source: numbered(10),
			// The position points past the end of line 5, where the view holds
			// no token, so there is nothing to highlight.
			err:   niceyaml.NewError("boom", niceyaml.AtPosition(position.New(4, 49))),
			lines: 2,
			// The excerpt still centers on line 5 with the default two lines of
			// context on either side.
			wantContains: []string{"line3", "line5", "line7"},
			wantNot:      []string{"line2:", "line8", "genericError"},
		},
		"path to an empty value still picks its excerpt": {
			source:       strings.Replace(numbered(10), "line4: d", "line4:", 1),
			err:          yamltest.ErrorAt("missing", "line4"),
			lines:        2,
			wantContains: []string{"line2", "line4", "line6"},
			wantNot:      []string{"line1:", "line7"},
		},
		"main error and nested in different hunks": {
			source: numbered(10),
			// The main error sits at line1 and the nested error at line10.
			err: niceyaml.NewError("main error", atKey("line1"), niceyaml.WithDetails(
				niceyaml.NewError("nested error", atKey("line10")),
			)),
			lines: 1,
			// The excerpt shows both locations, the annotation of the nested
			// error, and a "..." separator between the hunks.
			wantContains: []string{
				"<genericError>line1</genericError>",
				"<genericError>line10</genericError>",
				"nested error",
				"...",
			},
		},
		"nested errors on distant lines show separate hunks": {
			source: numbered(6),
			err: niceyaml.NewError("validation error", niceyaml.WithDetails(
				niceyaml.NewError("error at line1", atKey("line1")),
				niceyaml.NewError("error at line6", atKey("line6")),
			)),
			lines: 0,
			want: stringtest.JoinLF(
				"validation error",
				"├── 1:1: $.line1~: error at line1",
				"└── 6:1: $.line6~: error at line6",
				"",
				"<genericError>line1</genericError><punctuationMappingValue>:</punctuationMappingValue><text> </text><literalString>a</literalString>",
				"<textError>^^^^^ error at line1</textError>",
				"<uiSeparator>...</uiSeparator>",
				"<genericError>line6</genericError><punctuationMappingValue>:</punctuationMappingValue><text> </text><literalString>f</literalString>",
				"<textError>^^^^^ error at line6</textError>",
			),
		},
		"close errors merge into single hunk": {
			source: numbered(5),
			// Errors at line1 and line3 with contextLines=1 merge into one hunk,
			// since they lie within 2*contextLines of each other.
			err: niceyaml.NewSummary(
				"validation error",
				niceyaml.NewError("first error", atKey("line1")),
				niceyaml.NewError("second error", atKey("line3")),
			),
			lines: 1,
			// The hunk runs from line1 through line4 with no separator.
			want: stringtest.JoinLF(
				"validation error",
				"├── 1:1: $.line1~: first error",
				"└── 3:1: $.line3~: second error",
				"",
				"<genericError>line1</genericError><punctuationMappingValue>:</punctuationMappingValue><text> </text><literalString>a</literalString>",
				"<textError>^^^^^ first error</textError>",
				"<nameTag>line2</nameTag><punctuationMappingValue>:</punctuationMappingValue><text> </text><literalString>b</literalString>",
				"<genericError>line3</genericError><punctuationMappingValue>:</punctuationMappingValue><text> </text><literalString>c</literalString>",
				"<textError>^^^^^ second error</textError>",
				"<nameTag>line4</nameTag><punctuationMappingValue>:</punctuationMappingValue><text> </text><literalString>d</literalString>",
			),
		},
		"errors at start and end of file": {
			source: stringtest.Input(`
				first: value
				middle1: a
				middle2: b
				middle3: c
				middle4: d
				middle5: e
				last: value
			`),
			// The errors sit on the first and last lines.
			err: niceyaml.NewSummary(
				"validation error",
				niceyaml.NewError("error at first", atKey("first")),
				niceyaml.NewError("error at last", atKey("last")),
			),
			lines: 1,
			// The context clips to the file, so the excerpt starts at first and
			// ends at last, with a separator between the two hunks.
			want: stringtest.JoinLF(
				"validation error",
				"├── 1:1: $.first~: error at first",
				"└── 7:1: $.last~: error at last",
				"",
				"<genericError>first</genericError><punctuationMappingValue>:</punctuationMappingValue><text> </text><literalString>value</literalString>",
				"<textError>^^^^^ error at first</textError>",
				"<nameTag>middle1</nameTag><punctuationMappingValue>:</punctuationMappingValue><text> </text><literalString>a</literalString>",
				"<uiSeparator>...</uiSeparator>",
				"<nameTag>middle5</nameTag><punctuationMappingValue>:</punctuationMappingValue><text> </text><literalString>e</literalString>",
				"<genericError>last</genericError><punctuationMappingValue>:</punctuationMappingValue><text> </text><literalString>value</literalString>",
				"<textError>^^^^ error at last</textError>",
			),
		},
		"adjacent errors with no context merge into single hunk": {
			source: numbered(3),
			// With contextLines=0, errors at line1 and line2 merge because they
			// lie within the threshold (2*0+1=1) of each other.
			err: niceyaml.NewSummary(
				"validation error",
				niceyaml.NewError("error at line1", atKey("line1")),
				niceyaml.NewError("error at line2", atKey("line2")),
			),
			lines: 0,
			// The hunk holds line1 and line2 with no separator between them.
			want: stringtest.JoinLF(
				"validation error",
				"├── 1:1: $.line1~: error at line1",
				"└── 2:1: $.line2~: error at line2",
				"",
				"<genericError>line1</genericError><punctuationMappingValue>:</punctuationMappingValue><text> </text><literalString>a</literalString>",
				"<textError>^^^^^ error at line1</textError>",
				"<genericError>line2</genericError><punctuationMappingValue>:</punctuationMappingValue><text> </text><literalString>b</literalString>",
				"<textError>^^^^^ error at line2</textError>",
			),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			err := yamltest.Bind(t, niceyaml.NewSourceFromString(tc.source), tc.err)

			got := renderContext(err, tc.lines)

			if tc.want != "" {
				assert.Equal(t, tc.want, got)
			}

			for _, want := range tc.wantContains {
				assert.Contains(t, got, want)
			}

			for _, wantNot := range tc.wantNot {
				assert.NotContains(t, got, wantNot)
			}
		})
	}

	t.Run("negative context lines show the error lines alone", func(t *testing.T) {
		t.Parallel()

		err := yamltest.Bind(t, niceyaml.NewSourceFromString(numbered(6)), niceyaml.NewError(
			"validation error",
			atKey("line1"),
			niceyaml.WithDetails(niceyaml.NewError("error at end", atKey("line6"))),
		))

		// A negative count renders as 0 does, so each error line is a hunk
		// of its own with no context around it.
		want := renderContext(err, 0)
		got := renderContext(err, -1)

		assert.Equal(t, want, got)
		assert.Contains(t, got, "line1")
		assert.Contains(t, got, "line6")
		assert.Contains(t, got, "...")
		assert.NotContains(t, got, "line2")
		assert.NotContains(t, got, "line5")
	})
}

func TestError_Width(t *testing.T) {
	t.Parallel()

	// The YAML holds a value long enough to wrap.
	source := stringtest.Input(`
		key: this is a very long value that should wrap when width is limited to a small number
	`)
	tokens := lexer.Tokenize(source)

	// Bare creates a printer with no gutter or container that wraps at width.
	bare := func(width int) *printer.Printer {
		return printer.New(
			printer.WithGutter(printer.NoGutter),
			printer.WithContainerStyle(lipgloss.NewStyle()),
			printer.WithWrapWidth(width),
		)
	}

	// Width comes from the printer, which wraps only at the width
	// [printer.WithWrapWidth] sets.
	tcs := map[string]struct {
		printer     *printer.Printer
		wantWrapped bool
	}{
		"wraps at narrow width": {
			printer:     bare(40),
			wantWrapped: true,
		},
		"no wrap when width is 0": {
			printer: bare(0),
		},
		"no wrap when width is large": {
			printer: bare(200),
		},
		"wraps with custom styles": {
			printer:     yamltest.NewXMLPrinter(printer.WithWrapWidth(30)),
			wantWrapped: true,
		},
		// A printer with only a width keeps the default styles and gutter.
		"wraps with the default styles and gutter": {
			printer:     printer.New(printer.WithWrapWidth(30)),
			wantWrapped: true,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			err := yamltest.Bind(t, niceyaml.NewSourceFromString(source), niceyaml.NewError("test error",
				niceyaml.AtPosition(position.NewFromToken(tokens[0])),
			))

			output := renderWith(err, tc.printer, 2)

			// Count the rows that hold content, which leaves out the header
			// line and the empty line. Each check is a single word, which
			// stays whole on one row wherever the rows break.
			contentLines := 0
			for line := range strings.SplitSeq(output, "\n") {
				if strings.Contains(line, "key") || strings.Contains(line, "value") ||
					strings.Contains(line, "limited") {
					contentLines++
				}
			}

			if tc.wantWrapped {
				// With wrapping, the content spans several lines.
				assert.Greater(t, contentLines, 1, "expected content to wrap into multiple lines")
			} else {
				// Without wrapping, the YAML content stays on one line.
				assert.Equal(t, 1, contentLines, "expected content on single line")
			}
		})
	}
}

func TestError_Width_AnnotationWrapping(t *testing.T) {
	t.Parallel()

	pair := stringtest.Input(`
		key: value
		other: data
	`)
	keyPath := paths.Current().Child("key")

	// Detailed returns an error at the key of pair with a detail at its
	// value that carries msg.
	detailed := func(msg string) error {
		return niceyaml.NewError("validation failed", niceyaml.AtPath(keyPath.Key()), niceyaml.WithDetails(
			yamltest.ErrorAt(msg, "key"),
		))
	}

	// The message tree and the annotation in the excerpt both wrap to the
	// width, the tree less its connectors.
	tcs := map[string]struct {
		source string
		err    error
		want   string
		width  int
	}{
		"long annotation wraps at narrow width": {
			source: pair,
			width:  40,
			err:    detailed("this is a very long error message that should definitely wrap when the width is limited"),
			want: stringtest.JoinLF(
				"1:1: $.key~: validation failed",
				"└── 1:6: $.key: this is a very long",
				"    error message that should definitely",
				"    wrap when the width is limited",
				"",
				"key: value",
				"^^^",
				"     ^^^^^ this is a very long error",
				"           message that should",
				"           definitely wrap when the",
				"           width is limited",
				"other: data",
			),
		},
		"annotation does not wrap when width is 0": {
			source: pair,
			width:  0,
			err:    detailed("this is a very long error message that should not wrap"),
			want: stringtest.JoinLF(
				"1:1: $.key~: validation failed",
				"└── 1:6: $.key: this is a very long error message that should not wrap",
				"",
				"key: value",
				"^^^",
				"     ^^^^^ this is a very long error message that should not wrap",
				"other: data",
			),
		},
		"short annotation fits on single line": {
			source: pair,
			width:  80,
			err:    detailed("short error"),
			want: stringtest.JoinLF(
				"1:1: $.key~: validation failed",
				"└── 1:6: $.key: short error",
				"",
				"key: value",
				"^^^",
				"     ^^^^^ short error",
				"other: data",
			),
		},
		// Several nested errors carry long messages.
		"annotations on several lines each wrap": {
			source: stringtest.Input(`
				name: test
				value: 123
				other: data
			`),
			width: 50,
			err: niceyaml.NewSummary(
				"validation failed at 2 locations",
				yamltest.ErrorAt("first error with a very long message that should wrap properly", "value"),
				niceyaml.NewError(
					"second error also with a long message for testing wrap behavior",
					niceyaml.AtPath(paths.Current().Child("other").Key()),
				),
			),
			want: stringtest.JoinLF(
				"validation failed at 2 locations",
				"├── 2:8: $.value: first error with a very long",
				"│   message that should wrap properly",
				"└── 3:1: $.other~: second error also with a long",
				"    message for testing wrap behavior",
				"",
				"name: test",
				"value: 123",
				"       ^^^ first error with a very long message",
				"           that should wrap properly",
				"other: data",
				"^^^^^ second error also with a long message for",
				"      testing wrap behavior",
			),
		},
		// Each error on the same line gets a row of its own.
		"annotations on one line each wrap": {
			source: "key: value",
			width:  40,
			err: niceyaml.NewError("validation failed", niceyaml.AtPath(keyPath.Key()), niceyaml.WithDetails(
				niceyaml.NewError("first error message here", niceyaml.AtPath(keyPath.Key())),
				yamltest.ErrorAt("second error message here", "key"),
			)),
			want: stringtest.JoinLF(
				"1:1: $.key~: validation failed",
				"├── 1:1: $.key~: first error message",
				"│   here",
				"└── 1:6: $.key: second error message",
				"    here",
				"",
				"key: value",
				"^^^ first error message here",
				"     ^^^^^ second error message here",
			),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			errPrinter := yamltest.NewPlainPrinter(printer.WithWrapWidth(tc.width))

			err := yamltest.Bind(t, niceyaml.NewSourceFromString(tc.source), tc.err)

			assert.Equal(t, tc.want, renderWith(err, errPrinter, 2))
		})
	}
}

func TestError_TokenRendersFromSource(t *testing.T) {
	t.Parallel()

	source := niceyaml.NewSourceFromString(stringtest.Input(`
		a: 1
		# note
		b: |
		  two
		  lines
		c: 3
	`))

	// A parsed token is a clone of the lexer's token, so it shares position
	// but not identity with the tokens the source built its view from.
	file, err := source.File()
	require.NoError(t, err)

	node, err := paths.NewResolver(file.Docs[0]).Node(paths.Current().Child("b"))
	require.NoError(t, err)

	literal, ok := node.(*ast.LiteralNode)
	require.True(t, ok, "want *ast.LiteralNode, got %T", node)

	tk := literal.Value.GetToken()

	got := render(yamltest.Bind(t, source, niceyaml.NewError(
		"bad block",
		niceyaml.AtPosition(position.NewFromToken(tk)),
		niceyaml.WithDetails(
			yamltest.ErrorAt("bad c", "c"),
		),
	)))

	// The printer highlights both lines of the block scalar, comments keep
	// their place, and the nested path resolves in the same source.
	assert.Contains(t, got, "<genericError>two</genericError>")
	assert.Contains(t, got, "<genericError>lines</genericError>")
	assert.Contains(t, got, "<comment># note</comment>")
	assert.Contains(t, got, "<genericError>3</genericError>")
	assert.Contains(t, got, "^ bad c")
}

// nameDocuments returns the two documents of a source whose first
// document holds the name "first" and whose second holds "second".
func nameDocuments(t *testing.T) []*niceyaml.Node {
	t.Helper()

	return yamltest.Documents(t, niceyaml.NewSourceFromString(stringtest.LinesLF(
		"name: first",
		"---",
		"name: second",
	)), 2)
}

func TestError_BoundDocument(t *testing.T) {
	t.Parallel()

	docs := nameDocuments(t)
	names := []string{"first", "second"}
	located := yamltest.ErrorAt("bad name", "name")

	// A path resolves in the document that binds its error, so the excerpt
	// marks the name of that document and leaves the other alone.
	tcs := map[string]struct {
		err error
		// The message of the binding, which the rendering opens with.
		want string
		// The row below the marked name.
		wantCaret string
		// The index of the document that binds the error.
		doc int
	}{
		"path resolves in the document that bound it": {
			err:       located,
			doc:       1,
			want:      "3:7: $.name: bad name",
			wantCaret: "<textError>      ^^^^^^</textError>",
		},
		"nested errors resolve in the same document": {
			err:       niceyaml.NewError("validation failed", niceyaml.WithDetails(located)),
			doc:       1,
			want:      "document 2: validation failed",
			wantCaret: "<textError>      ^^^^^^ bad name</textError>",
		},
		// A producer that wraps its own Error with context, the way a
		// SelfValidator does, leaves the document to the binder above that
		// wrapping. The producer's context stays as written, behind the
		// resolved position and the path.
		"context above the location": {
			err:       niceyaml.Invalid(fmt.Errorf("validate: %w", located)),
			doc:       1,
			want:      "3:7: $.name: validate: bad name",
			wantCaret: "<textError>      ^^^^^^</textError>",
		},
		// Error wrappers add no message text of their own, so the document
		// attached above them still resolves the location in the message.
		"Error wrapper around the location": {
			err:       niceyaml.Invalid(located),
			doc:       0,
			want:      "1:7: $.name: bad name",
			wantCaret: "<textError>      ^^^^^</textError>",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			err := docs[tc.doc].Bind(tc.err)
			require.EqualError(t, err, tc.want)

			got := render(err)

			assert.True(t, strings.HasPrefix(got, tc.want), got)
			assert.Contains(t, got, stringtest.JoinLF("<genericError>"+names[tc.doc]+"</genericError>", tc.wantCaret))
			assert.NotContains(t, got, "<genericError>"+names[1-tc.doc]+"</genericError>")
		})
	}
}

func TestError_DoesNotMutateSource(t *testing.T) {
	t.Parallel()

	source := niceyaml.NewSourceFromString(stringtest.Input(`
		a: 1
		b: 2
		c: 3
	`))

	err := yamltest.Bind(t, source, niceyaml.NewError(
		"main",
		niceyaml.WithDetails(
			yamltest.ErrorAt("nested", "b"),
		),
	))

	first := render(err)
	second := render(err)

	// Rendering is idempotent.
	assert.Equal(t, first, second)
	assert.Equal(t, 1, strings.Count(second, "^ nested"))
	assert.Equal(t, 1, strings.Count(second, "<genericError>2</genericError>"))

	// The caller's Source is untouched, so a fresh view still renders
	// undecorated.
	assertUnmarked(t, source.View())
}

func TestError_With(t *testing.T) {
	t.Parallel()

	base := niceyaml.NewError("bad key")
	located := base.With(niceyaml.AtPath(paths.Current().Child("key").Key()))

	// The copy carries the option and the receiver keeps its own.
	_, ok := base.Path()
	assert.False(t, ok)

	assert.Equal(t, paths.Current().Child("key").Key(), yamltest.PathOf(t, located))

	// The path stays out of the message, and FormatError names it.
	assert.Equal(t, "bad key", base.Error())
	assert.Equal(t, "bad key", located.Error())
	assert.Equal(t, "@.key~: bad key", niceyaml.FormatError(located, niceyaml.WithContextLines(0)))

	// The details of the copy are its own too.
	reason := errors.New("reason")
	detailed := located.With(niceyaml.WithDetails(reason))
	more := detailed.With(niceyaml.WithDetails(errors.New("more")))

	assert.Empty(t, yamltest.DetailsOf(located))
	assert.Equal(t, []error{reason}, yamltest.ErrsOf(yamltest.DetailsOf(detailed)))
	assert.Len(t, yamltest.DetailsOf(more), 2)
}

func TestNewSummary(t *testing.T) {
	t.Parallel()

	source := errorTreeSource()
	badA := yamltest.ErrorAt("bad a", "a")
	tooOld := errors.New("too old")

	var nilErr *niceyaml.Error

	t.Run("no error returns nil", func(t *testing.T) {
		t.Parallel()

		// The result is a nil interface, so a validator returns it as it
		// is and its caller compares it against nil.
		assert.NoError(t, niceyaml.NewSummary("0 problems"))
		assert.NoError(t, niceyaml.NewSummary("0 problems", nil, nilErr))
	})

	t.Run("one error returns it alone", func(t *testing.T) {
		t.Parallel()

		assert.Same(t, badA, niceyaml.NewSummary("1 problem", nil, badA, nilErr))
		assert.Equal(t, tooOld, niceyaml.NewSummary("1 problem", tooOld))
	})

	t.Run("several errors are separate problems under a heading", func(t *testing.T) {
		t.Parallel()

		err := niceyaml.NewSummary("2 problems", badA, nil, tooOld)
		assert.Equal(t, "2 problems", err.Error())

		summary := yamltest.RequireError(t, err)
		assert.Equal(t, []error{badA, tooOld}, yamltest.ErrsOf(yamltest.ProblemsOf(summary)))
		assert.Empty(t, yamltest.DetailsOf(summary))

		_, ok := summary.Path()
		assert.False(t, ok)

		// The summary is no problem of its own, and it lists each of the
		// problems below its line.
		bound := yamltest.Bind(t, source, err)
		require.EqualError(t, bound, stringtest.JoinLF(
			"f.yaml: 2 problems",
			"f.yaml:1:4: $.a: bad a",
			"f.yaml: too old",
		))
		assert.Equal(t, []string{"bad a", "too old"}, problemRows(niceyaml.NewErrorTree(bound)))
		assert.Equal(t, []string{"@.a: bad a", "too old"}, problemRows(niceyaml.NewErrorTree(err)))
	})

	t.Run("a location on the summary locates each problem with none", func(t *testing.T) {
		t.Parallel()

		summary := yamltest.RequireError(t, niceyaml.NewSummary("2 problems", badA, tooOld))
		err := summary.With(niceyaml.AtPath(paths.Current().Child("b")))

		// The summary keeps its line, with the location in front.
		bound := yamltest.Bind(t, source, err)
		require.EqualError(t, bound, stringtest.JoinLF(
			"f.yaml:2:4: $.b: 2 problems",
			"f.yaml:1:4: $.a: bad a",
			"f.yaml:2:4: $.b: too old",
		))

		var rows []string

		for problem := range niceyaml.Problems(bound) {
			pos := yamltest.PositionOf(t, problem)
			rows = append(rows, fmt.Sprintf("%s %s: %s", pos, yamltest.PathOf(t, problem), problem.Message()))
		}

		assert.Equal(t, []string{"0:3 $.a: bad a", "1:3 $.b: too old"}, rows)

		// The problems carry the location before binding too.
		assert.Equal(t, []string{"@.a: bad a", "@.b: too old"}, problemRows(niceyaml.NewErrorTree(err)))
	})
}

func TestWithDetails(t *testing.T) {
	t.Parallel()

	source := niceyaml.NewSourceFromString(
		stringtest.LinesLF("spec:", "  a: 80", "  b: 80"),
		niceyaml.WithName("cfg.yaml"),
	)
	portA := paths.Current().Child("spec", "a")
	portB := paths.Current().Child("spec", "b")

	t.Run("an error with details is one problem", func(t *testing.T) {
		t.Parallel()

		err := yamltest.Bind(t, source, niceyaml.NewError("port 80 conflicts",
			niceyaml.AtPath(portB),
			niceyaml.WithDetails(niceyaml.NewError("first declared here", niceyaml.AtPath(portA))),
		))

		// The message is the line of the problem, and the tree and the
		// excerpt show the detail below it.
		require.EqualError(t, err, "cfg.yaml:3:6: $.spec.b: port 80 conflicts")
		assert.Equal(t, stringtest.JoinLF(
			"cfg.yaml:3:6: $.spec.b: port 80 conflicts",
			"`-- 2:6: $.spec.a: first declared here",
			"",
			"   2 |   a: 80",
			"     |      ^^ first declared here",
			"   3 |   b: 80",
			"     |      ^^",
		), niceyaml.FormatError(err, niceyaml.WithContextLines(0)))

		// The error is one problem, and its one child is the detail.
		yamltest.OnlyProblem(t, err)

		bound := yamltest.RequireSourceError(t, err)
		assert.Empty(t, yamltest.ProblemsOf(bound))
		assert.Equal(t, []string{"cfg.yaml:2:6: $.spec.a: first declared here"}, errorTexts(yamltest.DetailsOf(bound)))
	})

	t.Run("a detail bound to another file keeps its binding", func(t *testing.T) {
		t.Parallel()

		other := niceyaml.NewSourceFromString(stringtest.LinesLF("base:", "  port: 80"), niceyaml.WithName("base.yaml"))
		declared := yamltest.Bind(t, other, yamltest.ErrorAt("first declared here", "base", "port"))

		err := yamltest.Bind(t, source, niceyaml.NewError("port 80 conflicts",
			niceyaml.AtPath(portB),
			niceyaml.WithDetails(declared),
		))

		require.EqualError(t, err, "cfg.yaml:3:6: $.spec.b: port 80 conflicts")
		assert.Equal(t, stringtest.JoinLF(
			"cfg.yaml:3:6: $.spec.b: port 80 conflicts",
			"`-- base.yaml:2:9: $.base.port: first declared here",
			"",
			"cfg.yaml",
			"   3 |   b: 80",
			"     |      ^^",
			"",
			"base.yaml",
			"   2 |   port: 80",
			"     |         ^^ first declared here",
		), niceyaml.FormatError(err, niceyaml.WithContextLines(0)))

		problem := yamltest.OnlyProblem(t, err)
		require.Len(t, problem.Children, 1)
		assert.Same(t, other, problem.Children[0].Bound.Source())
	})

	t.Run("a detail that is a join gives its place to its branches", func(t *testing.T) {
		t.Parallel()

		build := func() error {
			return niceyaml.NewError("port 80 conflicts",
				niceyaml.AtPath(portB),
				niceyaml.WithDetails(errors.Join(
					niceyaml.NewError("first declared here", niceyaml.AtPath(portA)),
					errors.New("see docs"),
				)),
			)
		}

		// Each branch is a detail of its own, bound or not.
		for _, err := range []error{build(), yamltest.Bind(t, source, build())} {
			problem := yamltest.OnlyProblem(t, err)
			require.Len(t, problem.Children, 2)

			for _, detail := range problem.Children {
				assert.True(t, detail.Detail, detail.Label)
			}
		}
	})
}

func TestError_WrappedContext(t *testing.T) {
	t.Parallel()

	docs := nameDocuments(t)

	inner := yamltest.ErrorAt("bad name", "name")

	wrapped := docs[1].Bind(fmt.Errorf("document 1: %w", inner))

	// The outer context stays as the wrapper wrote it, and the position the
	// path resolves to and the path go in front of the whole message.
	assert.Equal(t, "3:7: $.name: document 1: bad name", wrapped.Error())
	assert.Equal(t, "3:7: $.name: document 1: bad name", fmt.Sprintf("%v", wrapped))
	require.ErrorIs(t, wrapped, inner)

	// Binding first puts the context in front of the position instead.
	boundFirst := fmt.Errorf("document 1: %w", docs[1].Bind(inner))
	assert.Equal(t, "document 1: 3:7: $.name: bad name", boundFirst.Error())

	assert.Equal(t, "$.name", yamltest.PathOf(t, yamltest.RequireError(t, wrapped)).String())

	bound := yamltest.RequireSourceError(t, wrapped)

	excerpt := requireExcerpt(t, bound)

	detail := yamltest.NewXMLPrinter().Print(excerpt)
	assert.Contains(t, detail, "second")
	assert.Contains(t, detail, "^^^^^^", "the range shows its extent")
	assert.NotContains(t, detail, "^ ", "the wrapper's text is no annotation")

	// Binding a bound Error again renders the same output.
	direct := docs[1].Bind(inner)
	twice := docs[1].Bind(direct)
	assert.Equal(t, direct.Error(), twice.Error())
	assert.Equal(t, fmt.Sprintf("%+v", direct), fmt.Sprintf("%+v", twice))
}

func TestError_NestedErrorsRenderAsAnnotations(t *testing.T) {
	t.Parallel()

	source := niceyaml.NewSourceFromString("a: 1\nb: 2\n")
	plain := yamltest.NewPlainPrinter()
	badA := yamltest.ErrorAt("bad a", "a")
	badB := yamltest.ErrorAt("bad b", "b")
	inner := niceyaml.NewSummary(
		"validation failed at 2 locations", badA, badB,
	)

	wrapped := yamltest.Bind(t, source, fmt.Errorf("document 0: %w", inner))

	// The message is the headline behind the context the wrapper added,
	// then the nested errors behind their positions. The %+v verb draws
	// them as branches, they are reachable through Unwrap, the printer
	// draws the same tree, and they appear in the excerpt as annotations.
	assert.Equal(t, stringtest.JoinLF(
		"document 0: validation failed at 2 locations",
		"1:4: $.a: bad a",
		"2:4: $.b: bad b",
	), wrapped.Error())
	assert.Equal(t, stringtest.JoinLF(
		"document 0: validation failed at 2 locations",
		"|-- 1:4: $.a: bad a",
		"`-- 2:4: $.b: bad b",
	), report(wrapped))
	require.ErrorIs(t, wrapped, badA)
	require.ErrorIs(t, wrapped, badB)

	bound := yamltest.RequireSourceError(t, wrapped)

	got := stringtest.TrimLineEnds(plain.PrintError(bound))

	message, detail, _ := strings.Cut(got, "\n\n")

	assert.Equal(t, stringtest.JoinLF(
		"document 0: validation failed at 2 locations",
		"├── 1:4: $.a: bad a",
		"└── 2:4: $.b: bad b",
	), message)
	assert.NotContains(t, detail, "$.a")
	assert.Contains(t, detail, "^ bad a")
	assert.Contains(t, detail, "^ bad b")
}

func TestError_NestedErrorChains(t *testing.T) {
	t.Parallel()

	// A nested error is a chain like the main one. It resolves through the
	// Error inside it, in the document the binder picked, and annotates the
	// line with its message alone.

	t.Run("location behind foreign wrapping resolves", func(t *testing.T) {
		t.Parallel()

		source := niceyaml.NewSourceFromString(stringtest.LinesLF("a: 1", "b: 2", "c: 3"))
		err := yamltest.Bind(t, source, niceyaml.NewError(
			"outer",
			niceyaml.AtPath(paths.Current().Child("a")),
			niceyaml.WithDetails(
				niceyaml.Invalid(fmt.Errorf("ctx: %w",
					yamltest.ErrorAt("bad", "b"),
				)),
			),
		))

		bound := yamltest.RequireSourceError(t, err)

		excerpt := requireExcerpt(t, bound)

		got := stringtest.TrimLineEnds(yamltest.NewXMLPrinter().Print(excerpt))
		assert.Contains(t, got, "<genericError>2</genericError>")
		assert.Contains(t, got, "^ ctx: bad")

		// The excerpt follows the message, which lists the nested error
		// with its position and its path in front of the context it
		// carries. The root has no message beside its range, so the printer
		// marks the range with a caret run.
		assert.Equal(t, stringtest.JoinLF(
			"1:4: $.a: outer",
			"└── 2:4: $.b: ctx: bad",
			"",
			"<nameTag>a</nameTag><punctuationMappingValue>:</punctuationMappingValue><text> </text><genericError>1</genericError>",
			"<textError>   ^</textError>",
			"<nameTag>b</nameTag><punctuationMappingValue>:</punctuationMappingValue><text> </text><genericError>2</genericError>",
			"<textError>   ^ ctx: bad</textError>",
			"<nameTag>c</nameTag><punctuationMappingValue>:</punctuationMappingValue><text> </text><literalNumberInteger>3</literalNumberInteger>",
		), render(err))
	})

	t.Run("annotation drops the position the caret marks", func(t *testing.T) {
		t.Parallel()

		source := niceyaml.NewSourceFromString("a: 1\nb: 2\n")
		tk := source.Lines().TokenAt(position.New(1, 0))
		require.NotNil(t, tk)

		err := yamltest.Bind(t, source, niceyaml.NewError(
			"outer",
			niceyaml.AtPath(paths.Current().Child("a")),
			niceyaml.WithDetails(
				niceyaml.Invalid(niceyaml.NewError("inner", niceyaml.AtPosition(position.NewFromToken(tk)))),
				niceyaml.Invalid(yamltest.ErrorAt("also", "b")),
			),
		))

		_, detail := renderParts(err)
		assert.Contains(t, detail, "^ inner")
		assert.Contains(t, detail, "^ also")
		assert.NotContains(t, detail, "2:1:")
		assert.NotContains(t, detail, "$.b")
	})
}

func TestError_NestedMessageSpansLines(t *testing.T) {
	t.Parallel()

	source := niceyaml.NewSourceFromString("a: 1\nb: 2\n")
	err := yamltest.Bind(t, source, niceyaml.NewSummary(
		"validation failed",
		yamltest.ErrorAt("bad a\n  see docs for details", "a"),
		yamltest.ErrorAt("bad b", "b"),
	))

	// The message lists the nested message under the headline whole,
	// continuation line included. So does the %+v verb, the tree indents
	// the continuation line under the branch, and the annotation carries
	// it as well.
	assert.Equal(t, stringtest.JoinLF(
		"validation failed",
		"1:4: $.a: bad a",
		"  see docs for details",
		"2:4: $.b: bad b",
	), err.Error())
	assert.Equal(t,
		stringtest.JoinLF(
			"validation failed",
			"|-- 1:4: $.a: bad a",
			"|     see docs for details",
			"`-- 2:4: $.b: bad b",
		),
		report(err),
	)

	message, detail := renderParts(err)

	assert.Equal(t,
		stringtest.JoinLF(
			"validation failed",
			"├── 1:4: $.a: bad a",
			"│     see docs for details",
			"└── 2:4: $.b: bad b",
		),
		message,
	)
	assert.Contains(t, detail, "see docs for details")
}

func TestSourceError_NestedPositionsBehindWrappers(t *testing.T) {
	t.Parallel()

	source := errorTreeSource()
	inner := niceyaml.NewSummary("2 problems",
		yamltest.ErrorAt("bad a", "a"),
		niceyaml.NewError("bad b", niceyaml.AtPath(paths.Current().Child("missing").Index(0))),
	)

	tcs := map[string]struct {
		err error
		// The tree the %+v verb draws.
		want string
		// The message.
		wantMsg string
	}{
		"bare": {
			err: inner,
			want: stringtest.JoinLF(
				"f.yaml: 2 problems",
				"|-- 1:4: $.a: bad a",
				"`-- $.missing[0]: bad b",
			),
			wantMsg: stringtest.JoinLF(
				"f.yaml: 2 problems",
				"f.yaml:1:4: $.a: bad a",
				"f.yaml: $.missing[0]: bad b",
			),
		},
		"context in front keeps the nested lines at the end": {
			err: fmt.Errorf("document 0: %w", inner),
			want: stringtest.JoinLF(
				"f.yaml: document 0: 2 problems",
				"|-- 1:4: $.a: bad a",
				"`-- $.missing[0]: bad b",
			),
			wantMsg: stringtest.JoinLF(
				"f.yaml: document 0: 2 problems",
				"f.yaml:1:4: $.a: bad a",
				"f.yaml: $.missing[0]: bad b",
			),
		},
		"details inside details": {
			err: niceyaml.NewError("outer", niceyaml.WithDetails(
				niceyaml.NewError("middle", niceyaml.AtPath(paths.Current().Child("b")), niceyaml.WithDetails(
					yamltest.ErrorAt("leaf", "a"),
				)),
			)),
			want: stringtest.JoinLF(
				"f.yaml: outer",
				"`-- 2:4: $.b: middle",
				"    `-- 1:4: $.a: leaf",
			),
			wantMsg: "f.yaml: outer",
		},
		"a wrapper that rewrites the message keeps the nested lines": {
			err: yamltest.RewriteError{Err: inner},
			want: stringtest.JoinLF(
				"f.yaml: rewritten",
				"|-- 1:4: $.a: bad a",
				"`-- $.missing[0]: bad b",
			),
			wantMsg: stringtest.JoinLF(
				"f.yaml: rewritten",
				"f.yaml:1:4: $.a: bad a",
				"f.yaml: $.missing[0]: bad b",
			),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			bound := yamltest.Bind(t, source, tc.err)

			// The %+v verb draws the nested errors as branches, each as its
			// own bound error, and the message lists the problems each
			// heading heads.
			assert.Equal(t, tc.want, report(bound))
			assert.Equal(t, tc.wantMsg, bound.Error())
		})
	}
}

func TestSourceError_KeepsWrappedText(t *testing.T) {
	t.Parallel()

	docs := nameDocuments(t)
	namePath := paths.Current().Child("name")
	badName := yamltest.ErrorAt("bad name", "name")

	// The token of the name of the second document.
	tk := docs[0].Source().Lines().TokenAt(position.New(2, 6))
	badToken := niceyaml.NewError("bad token", niceyaml.AtPosition(position.NewFromToken(tk)))

	// The first document binds each error. The position and the path come
	// from the Error in the chain, not from its text, and go in front of
	// the whole message.
	tcs := map[string]struct {
		err error
		// The Error in the chain, which the binding still matches.
		is error
		// The message before the binding and after it.
		unbound string
		bound   string
	}{
		"nested wrappers keep their text behind the position": {
			err:     fmt.Errorf("outer: %w", fmt.Errorf("inner: %w", badName)),
			is:      badName,
			unbound: "outer: inner: bad name",
			bound:   "1:7: $.name: outer: inner: bad name",
		},
		// The binding puts the position and the path in front of the
		// empty text, so the line still says where the problem is.
		"a wrapper that blanks the message still gets the position": {
			err:     blankError{err: badName},
			is:      badName,
			unbound: "",
			bound:   "1:7: $.name:",
		},
		// A wrapper that hides the text of the Error hides neither its
		// position nor its path.
		"a wrapper that rewrites the message still gets the position": {
			err:     fmt.Errorf("outer: %w", yamltest.RewriteError{Err: badName}),
			is:      badName,
			unbound: "outer: rewritten",
			bound:   "1:7: $.name: outer: rewritten",
		},
		// The Error carries no position in its text, and binding puts the
		// token's in front of the whole message, as it does for a path.
		"a token error gets its position in front of the wrapper's text": {
			err:     fmt.Errorf("document 1: %w", badToken),
			is:      badToken,
			unbound: "document 1: bad token",
			bound:   "3:7: document 1: bad token",
		},
		// The path anchor resolves in the first document, and the message
		// carries that one position, which agrees with the highlight.
		"a path anchor resolves its path rather than the error it wraps": {
			err:     niceyaml.Invalid(badToken, niceyaml.AtPath(namePath)),
			is:      badToken,
			unbound: "bad token",
			bound:   "1:7: $.name: bad token",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.unbound, tc.err.Error())

			bound := docs[0].Bind(tc.err)
			require.EqualError(t, bound, tc.bound)
			require.ErrorIs(t, bound, tc.is)
		})
	}

	t.Run("a bound join binds each branch as a child", func(t *testing.T) {
		t.Parallel()

		first := yamltest.ErrorAt("bad first", "name")
		second := yamltest.ErrorAt("bad second", "name")
		wrapped := docs[0].Bind(errors.Join(
			fmt.Errorf("a: %w", first),
			fmt.Errorf("b: %w", second),
		))

		// The join carries no location of its own, so the message is its
		// branches, and each branch is a child with its position and its
		// path in front of the text its wrapper wrote.
		assert.Equal(t, stringtest.JoinLF("1:7: $.name: a: bad first", "1:7: $.name: b: bad second"), wrapped.Error())
		require.ErrorIs(t, wrapped, first)
		require.ErrorIs(t, wrapped, second)

		bound := yamltest.RequireSourceError(t, wrapped)
		assert.Equal(t, []string{
			"1:7: $.name: a: bad first",
			"1:7: $.name: b: bad second",
		}, errorTexts(yamltest.ProblemsOf(bound)))
		assert.Len(t, slices.Collect(niceyaml.Bindings(wrapped)), 1)
	})

	t.Run("binding each branch reports every position", func(t *testing.T) {
		t.Parallel()

		first := yamltest.ErrorAt("bad first", "name")
		second := yamltest.ErrorAt("bad second", "name")
		joined := errors.Join(
			fmt.Errorf("a: %w", docs[0].Bind(first)),
			fmt.Errorf("b: %w", docs[1].Bind(second)),
		)

		// Each branch resolves in the document that bound it.
		assert.Equal(t, stringtest.JoinLF("a: 1:7: $.name: bad first", "b: 3:7: $.name: bad second"), joined.Error())
	})

	t.Run("an anchor with details takes the position of the error it wraps", func(t *testing.T) {
		t.Parallel()

		nested := yamltest.ErrorAt("bad name", "name")

		tcs := map[string]struct {
			err     error
			unbound string
			bound   string
		}{
			"direct": {
				err: niceyaml.Invalid(
					niceyaml.NewError("bad token", niceyaml.AtPosition(position.NewFromToken(tk))),
					niceyaml.WithDetails(nested),
				),
				unbound: "bad token",
				bound:   "3:7: bad token",
			},
			"behind context": {
				err: niceyaml.Invalid(
					fmt.Errorf(
						"document 1: %w",
						niceyaml.NewError("bad token", niceyaml.AtPosition(position.NewFromToken(tk))),
					),
					niceyaml.WithDetails(nested),
				),
				unbound: "document 1: bad token",
				bound:   "3:7: document 1: bad token",
			},
			"range": {
				err: niceyaml.Invalid(
					niceyaml.NewError("bad range",
						niceyaml.AtRange(yamltest.LineRange(2, 6, 12)),
					),
					niceyaml.WithDetails(nested),
				),
				unbound: "bad range",
				bound:   "3:7: bad range",
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				// The outer Error takes the location of the Error it wraps,
				// and the detail becomes a child with its own position, which
				// the %+v verb lists after the message.
				assert.Equal(t, tc.unbound, tc.err.Error())
				assert.Equal(t, tc.bound, docs[0].Bind(tc.err).Error())
				assert.Equal(t, stringtest.JoinLF(tc.bound, "`-- 1:7: $.name: bad name"), report(docs[0].Bind(tc.err)))
			})
		}
	})

	t.Run("a path anchor replaces the path of the error it wraps", func(t *testing.T) {
		t.Parallel()

		inner := yamltest.ErrorAt("bad token", "other")
		outer := wrapError(t, inner, niceyaml.AtPath(namePath))

		// The tree and the binding name the one location Path reports, not
		// both.
		assert.Equal(t, namePath, yamltest.PathOf(t, outer))
		assert.Equal(t, "bad token", outer.Error())
		assert.Equal(t, "@.name: bad token", niceyaml.FormatError(outer, niceyaml.WithContextLines(0)))
		assert.Equal(t, "1:7: $.name: bad token", docs[0].Bind(outer).Error())
	})

	t.Run("a second binding adds no position", func(t *testing.T) {
		t.Parallel()

		inner := yamltest.ErrorAt("bad name", "name")
		once := docs[0].Bind(inner)
		wrapper := fmt.Errorf("document 0: %w", once)
		twice := docs[0].Bind(wrapper)

		// The chain is bound to this source already, so the second binding
		// returns the wrapper as it is.
		assert.Same(t, wrapper, twice)
		assert.Equal(t, "document 0: 1:7: $.name: bad name", twice.Error())
	})
}

func TestSourceError_Range(t *testing.T) {
	t.Parallel()

	twoDocuments := yamltest.Documents(t, niceyaml.NewSourceFromString(stringtest.Input(`
		name: test
		value: 123
		---
		name: second
	`)), 2)
	threeKeys := yamltest.FirstDocument(t, stringtest.LinesLF("a: 1", "b: 2", "c: 3"))

	// A plain scalar that spans two lines.
	folded := yamltest.FirstDocument(t, stringtest.Input(`
		text: first
		  second
	`))

	// Empty returns the empty range at a position.
	empty := func(line, col int) position.Range {
		return yamltest.LineRange(line, col, col)
	}

	// Each case binds an error at a location through bind, the Bind
	// method of a document or of a source. A case that sets want checks
	// the message too, and a case that sets lines checks the numbers of
	// the lines an excerpt with no context shows.
	tcs := map[string]struct {
		bind func(error) error
		at   niceyaml.ErrorOption
		// The message, which opens with the position the binding reports.
		want  string
		rng   position.Range
		lines []int
	}{
		"path targets the value token": {
			bind: twoDocuments[0].Bind,
			at:   niceyaml.AtPath(paths.Current().Child("value")),
			rng:  yamltest.LineRange(1, 7, 10),
		},
		"path targets the key token": {
			bind: twoDocuments[0].Bind,
			at:   niceyaml.AtPath(paths.Current().Child("value").Key()),
			rng:  yamltest.LineRange(1, 0, 5),
		},
		"path resolves in the bound document": {
			bind: twoDocuments[1].Bind,
			at:   niceyaml.AtPath(paths.Current().Child("name")),
			rng:  yamltest.LineRange(3, 6, 12),
		},
		"range is returned as given": {
			bind: twoDocuments[0].Bind,
			at:   niceyaml.AtRange(yamltest.LineRange(0, 1, 3)),
			rng:  yamltest.LineRange(0, 1, 3),
		},
		"position covers the content of its token": {
			bind: twoDocuments[0].Bind,
			at:   niceyaml.AtPosition(position.New(0, 6)),
			rng:  yamltest.LineRange(0, 6, 10),
		},
		"position inside its token covers the whole token": {
			bind: twoDocuments[0].Bind,
			at:   niceyaml.AtPosition(position.New(1, 8)),
			rng:  yamltest.LineRange(1, 7, 10),
		},
		"range end before the first column on a later line": {
			bind: twoDocuments[0].Bind,
			at:   niceyaml.AtRange(position.NewRange(position.New(0, 1), position.New(1, -3))),
			rng:  position.NewRange(position.New(0, 1), position.New(1, 0)),
		},
		// An inverted range collapses to its start, and the excerpt shows
		// the line it starts on.
		"end on an earlier line": {
			bind:  threeKeys.Bind,
			at:    niceyaml.AtRange(position.NewRange(position.New(2, 2), position.New(0, 1))),
			rng:   yamltest.LineRange(2, 2, 2),
			lines: []int{3},
		},
		"end at an earlier column": {
			bind:  threeKeys.Bind,
			at:    niceyaml.AtRange(yamltest.LineRange(1, 3, 1)),
			rng:   yamltest.LineRange(1, 3, 3),
			lines: []int{2},
		},
		"start past the end of the last line, end past it": {
			bind:  threeKeys.Bind,
			at:    niceyaml.AtRange(position.NewRange(position.New(2, 10), position.New(9, 0))),
			rng:   yamltest.LineRange(2, 10, 10),
			lines: []int{3},
		},
		// A path to an empty value resolves to a token the parser makes for
		// it, so the range is empty at the position the message reports, even
		// when a comment or the spaces after the colon sit at that column.
		"empty value with nothing after the colon": {
			bind: yamltest.FirstDocument(t, stringtest.LinesLF("a:", "b: 1")).Bind,
			at:   niceyaml.AtPath(paths.Current().Child("a")),
			want: "1:3: $.a: bad",
			rng:  empty(0, 2),
		},
		"empty value with a comment after the colon": {
			bind: yamltest.FirstDocument(t, stringtest.LinesLF("a: # c", "b: 1")).Bind,
			at:   niceyaml.AtPath(paths.Current().Child("a")),
			want: "1:3: $.a: bad",
			rng:  empty(0, 2),
		},
		"empty value with spaces after the colon": {
			bind: yamltest.FirstDocument(t, stringtest.LinesLF("a:   ", "b: 1")).Bind,
			at:   niceyaml.AtPath(paths.Current().Child("a")),
			want: "1:3: $.a: bad",
			rng:  empty(0, 2),
		},
		"empty value with a comment after the dash": {
			bind: yamltest.FirstDocument(t, stringtest.LinesLF("- # c", "- 1")).Bind,
			at:   niceyaml.AtPath(paths.Current().Index(0)),
			want: "1:2: $[0]: bad",
			rng:  empty(0, 1),
		},
		// The lexer bundles the indentation of the third line into the plain
		// scalar "x" above it, yet the range covers the key on the third line.
		"position in the indentation": {
			bind: yamltest.FirstDocument(t, stringtest.JoinLF("parent:", "  child: x", "  other: 1")).Bind,
			at:   niceyaml.AtPosition(position.New(2, 0)),
			want: "3:1: bad",
			rng:  yamltest.LineRange(2, 2, 7),
		},
		// The plain scalar spans two lines, so the range ends on the second.
		"path to a token of two lines": {
			bind: folded.Bind,
			at:   niceyaml.AtPath(paths.Current().Child("text")),
			want: "1:7: $.text: bad",
			rng:  position.NewRange(position.New(0, 6), position.New(1, 8)),
		},
		// The position falls on the second line of the plain scalar, so the
		// range starts on the first line where the token does, while the
		// message reports the position as the error gave it.
		"position inside a token of two lines": {
			bind: folded.Bind,
			at:   niceyaml.AtPosition(position.New(1, 4)),
			want: "2:5: bad",
			rng:  position.NewRange(position.New(0, 6), position.New(1, 8)),
		},
		// A range that starts on a line of the source binds, and its end past
		// the last line clamps to the end of that line, so the range reported
		// names lines the source has.
		"range that ends past the last line": {
			bind: threeKeys.Bind,
			at:   niceyaml.AtRange(position.NewRange(position.New(1, 0), position.New(99, 3))),
			want: "2:1: bad",
			rng:  position.NewRange(position.New(1, 0), position.New(2, 4)),
		},
		// A line that holds only a tab precedes a token that starts with
		// the line ending, so no token holds text after the tab. The range
		// stays on the tab line rather than covering the token on the next
		// line. These cases bind through the source, which sends the
		// position to the document that holds its line.
		"tab line in mapping": {
			bind: niceyaml.NewSourceFromString(stringtest.LinesLF("a: 1", "\t", "b: 2")).Bind,
			at:   niceyaml.AtPosition(position.New(1, 0)),
			rng:  empty(1, 0),
		},
		"tab line in sequence": {
			bind: niceyaml.NewSourceFromString(stringtest.LinesLF("- a", "\t", "- b")).Bind,
			at:   niceyaml.AtPosition(position.New(1, 0)),
			rng:  empty(1, 0),
		},
		"tab line in nested sequence": {
			bind: niceyaml.NewSourceFromString(stringtest.LinesLF("a:", "  - 1", "\t", "  - 2")).Bind,
			at:   niceyaml.AtPosition(position.New(2, 0)),
			rng:  empty(2, 0),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			bound := yamltest.RequireSourceError(t, tc.bind(niceyaml.NewError("bad", tc.at)))

			assert.Equal(t, tc.rng, yamltest.RangeOf(t, bound))

			if tc.want != "" {
				assert.Equal(t, tc.want, bound.Error())
			}

			if tc.lines != nil {
				excerpt := requireExcerpt(t, bound, niceyaml.WithContextLines(0))
				assert.Equal(t, tc.lines, yamltest.LineNumbers(excerpt))
			}
		})
	}
}

func TestSourceError_Range_Collection(t *testing.T) {
	t.Parallel()

	doc := yamltest.FirstDocument(t, stringtest.Input(`
		name: api
		spec:
		  ports:
		    - name: web
		      port: 80
		    - {name: admin}
		  tags: [a, b]
		  matrix: [[1], {k: v}]
		  empty: {}
		base: &base
		  x: 1
		copy: *base
	`), niceyaml.WithName("x.yaml"))

	// An error at a mapping or a sequence points at the token that
	// introduces it, and one at a scalar or an alias at its own token.
	tcs := map[string]struct {
		path string
		want string
		rng  position.Range
	}{
		"block mapping at the root covers its first key": {
			path: "$",
			want: "x.yaml:1:1: $: bad",
			rng:  yamltest.LineRange(0, 0, 4),
		},
		"block mapping under a key covers the key": {
			path: "$.spec",
			want: "x.yaml:2:1: $.spec: bad",
			rng:  yamltest.LineRange(1, 0, 4),
		},
		"block sequence under a key covers the key": {
			path: "$.spec.ports",
			want: "x.yaml:3:3: $.spec.ports: bad",
			rng:  yamltest.LineRange(2, 2, 7),
		},
		"block mapping element covers its dash": {
			path: "$.spec.ports[0]",
			want: "x.yaml:4:5: $.spec.ports[0]: bad",
			rng:  yamltest.LineRange(3, 4, 5),
		},
		"key selector on an element covers its dash": {
			path: "$.spec.ports[0]~",
			want: "x.yaml:4:5: $.spec.ports[0]~: bad",
			rng:  yamltest.LineRange(3, 4, 5),
		},
		"flow mapping element of a block sequence covers its dash": {
			path: "$.spec.ports[1]",
			want: "x.yaml:6:5: $.spec.ports[1]: bad",
			rng:  yamltest.LineRange(5, 4, 5),
		},
		"flow sequence under a key covers the key": {
			path: "$.spec.tags",
			want: "x.yaml:7:3: $.spec.tags: bad",
			rng:  yamltest.LineRange(6, 2, 6),
		},
		"flow sequence in a flow sequence covers its bracket": {
			path: "$.spec.matrix[0]",
			want: "x.yaml:8:12: $.spec.matrix[0]: bad",
			rng:  yamltest.LineRange(7, 11, 12),
		},
		"flow mapping in a flow sequence covers its brace": {
			path: "$.spec.matrix[1]",
			want: "x.yaml:8:17: $.spec.matrix[1]: bad",
			rng:  yamltest.LineRange(7, 16, 17),
		},
		"empty mapping under a key covers the key": {
			path: "$.spec.empty",
			want: "x.yaml:9:3: $.spec.empty: bad",
			rng:  yamltest.LineRange(8, 2, 7),
		},
		"anchored mapping under a key covers the key": {
			path: "$.base",
			want: "x.yaml:10:1: $.base: bad",
			rng:  yamltest.LineRange(9, 0, 4),
		},
		"alias to a mapping covers the alias": {
			path: "$.copy",
			want: "x.yaml:12:7: $.copy: bad",
			rng:  yamltest.LineRange(11, 6, 7),
		},
		"scalar under a key covers the scalar": {
			path: "$.spec.ports[0].name",
			want: "x.yaml:4:13: $.spec.ports[0].name: bad",
			rng:  yamltest.LineRange(3, 12, 15),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			path := paths.MustParse(tc.path)

			err := doc.Bind(niceyaml.NewError("bad", niceyaml.AtPath(path)))
			require.EqualError(t, err, tc.want)

			bound := yamltest.RequireSourceError(t, err)

			assert.Equal(t, tc.rng, yamltest.RangeOf(t, bound))

			// The path of the error still names the value.
			assert.Equal(t, path, yamltest.PathOf(t, bound))

			ranges, err := doc.Ranges(path)
			require.NoError(t, err)
			assert.Equal(t, position.Ranges{tc.rng}, ranges)
		})
	}

	t.Run("the excerpt marks the line that names the value", func(t *testing.T) {
		t.Parallel()

		err := doc.Bind(niceyaml.NewError("at most 1 port", niceyaml.AtPath(paths.Doc().Child("spec", "ports"))))

		assert.Equal(t, stringtest.JoinLF(
			"x.yaml:3:3: $.spec.ports: at most 1 port",
			"",
			"   3 |   ports:",
			"     |   ^^^^^",
		), niceyaml.FormatError(err, niceyaml.WithContextLines(0)))
	})
}

func TestSourceError_Position(t *testing.T) {
	t.Parallel()

	source := niceyaml.NewSourceFromString("name: hello\nvalue: 123\n")

	tcs := map[string]struct {
		err error
		// The start of the range the error resolves to, which differs from
		// the position only for a position inside a token.
		start position.Position
		want  position.Position
		ok    bool
	}{
		"path": {
			err:   yamltest.ErrorAt("bad", "value"),
			want:  position.New(1, 7),
			start: position.New(1, 7),
			ok:    true,
		},
		"range": {
			err: niceyaml.NewError("bad", niceyaml.AtRange(
				yamltest.LineRange(0, 6, 9),
			)),
			want:  position.New(0, 6),
			start: position.New(0, 6),
			ok:    true,
		},
		"position at the start of a token": {
			err:   niceyaml.NewError("bad", niceyaml.AtPosition(position.New(1, 7))),
			want:  position.New(1, 7),
			start: position.New(1, 7),
			ok:    true,
		},
		"position inside a token": {
			err:   niceyaml.NewError("bad", niceyaml.AtPosition(position.New(0, 8))),
			want:  position.New(0, 8),
			start: position.New(0, 6),
			ok:    true,
		},
		"binding around a binding": {
			err: niceyaml.Invalid(
				yamltest.Bind(t, source, niceyaml.NewError("bad", niceyaml.AtPosition(position.New(0, 8)))),
				niceyaml.WithDetails(errors.New("see docs")),
			),
			want:  position.New(0, 8),
			start: position.New(0, 6),
			ok:    true,
		},
		"no location": {
			err: niceyaml.NewError("bad"),
		},
		"position past the last line": {
			err: niceyaml.NewError("bad", niceyaml.AtPosition(position.New(99, 0))),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			bound := requireBound(t, source, tc.err)

			got, ok := bound.Position()
			assert.Equal(t, tc.ok, ok)
			assert.Equal(t, tc.want, got)

			rng, rangeOK := bound.Range()
			assert.Equal(t, tc.ok, rangeOK)
			assert.Equal(t, tc.start, rng.Start)

			if !ok {
				return
			}

			// The message opens with the position counted from 1.
			prefix := strconv.Itoa(got.Line+1) + ":" + strconv.Itoa(got.Col+1) + ":"
			assert.True(t, strings.HasPrefix(bound.Error(), prefix), "%q does not open with %q", bound.Error(), prefix)
		})
	}
}

func TestSourceError_Excerpt_Errors(t *testing.T) {
	t.Parallel()

	source := niceyaml.NewSourceFromString("name: test\nvalue: 123\n")

	tcs := map[string]struct {
		err        error
		is         error
		wantRender string
	}{
		"no location": {
			err:        niceyaml.NewError("bad"),
			wantRender: "bad",
		},
		"path that does not resolve": {
			err:        niceyaml.NewError("bad", niceyaml.AtPath(paths.Current().Child("missing").Index(0))),
			is:         paths.ErrNotFound,
			wantRender: stringtest.JoinLF("$.missing[0]: bad", "", "no excerpt: resolve $.missing[0]: not found"),
		},
		"range past the last line": {
			err: niceyaml.NewError("bad",
				niceyaml.AtRange(yamltest.LineRange(9, 0, 3)),
			),
			is:         niceyaml.ErrOutOfRange,
			wantRender: stringtest.JoinLF("bad", "", "no excerpt: location outside source: line 10 not in lines 1-2"),
		},
		"range before the first line": {
			err: niceyaml.NewError("bad",
				niceyaml.AtRange(yamltest.LineRange(-1, 0, 2)),
			),
			is:         niceyaml.ErrOutOfRange,
			wantRender: stringtest.JoinLF("bad", "", "no excerpt: location outside source: line 0 not in lines 1-2"),
		},
		"range before the first column": {
			err: niceyaml.NewError("bad",
				niceyaml.AtRange(yamltest.LineRange(0, -2, 2)),
			),
			is:         niceyaml.ErrOutOfRange,
			wantRender: stringtest.JoinLF("bad", "", "no excerpt: location outside source: column -1 of line 1"),
		},
		"position from other text": {
			err:        niceyaml.NewError("bad", niceyaml.AtPosition(position.New(8, 0))),
			is:         niceyaml.ErrOutOfRange,
			wantRender: stringtest.JoinLF("bad", "", "no excerpt: location outside source: line 9 not in lines 1-2"),
		},
		"position before the first column": {
			err:        niceyaml.NewError("bad", niceyaml.AtPosition(position.New(1, -5))),
			is:         niceyaml.ErrOutOfRange,
			wantRender: stringtest.JoinLF("bad", "", "no excerpt: location outside source: column -4 of line 2"),
		},
		"nested range before the first line": {
			err: niceyaml.NewError("bad",
				niceyaml.AtPath(paths.Current().Child("missing").Index(0)),
				niceyaml.WithDetails(
					niceyaml.NewError("first",
						niceyaml.AtRange(yamltest.LineRange(-1, 0, 2)),
					),
				),
			),
			is: paths.ErrNotFound,
			wantRender: stringtest.JoinLF(
				"$.missing[0]: bad",
				"└── first",
				"",
				"no excerpt: resolve $.missing[0]: not found",
			),
		},
		"every nested error unresolved": {
			err: niceyaml.NewSummary("bad",
				niceyaml.NewError("first", niceyaml.AtPath(paths.Current().Child("missing").Index(0))),
				niceyaml.NewError("second", niceyaml.AtPosition(position.New(9, 0))),
			),
			wantRender: stringtest.JoinLF(
				"bad",
				"├── $.missing[0]: first",
				"└── second",
			),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			bound := requireBound(t, source, tc.err)

			got, ok := bound.Excerpt()
			require.False(t, ok)
			assert.Nil(t, got)
			requireUnresolved(t, bound, tc.is)

			// With no excerpt to show, the detail names why the location did
			// not resolve, unless the error carries none, and the message
			// carries no position, since the source holds none for it. The
			// nested errors are part of the message whether they resolve or
			// not.
			assert.Equal(t, tc.wantRender, render(bound))
		})
	}

	t.Run("one resolved location renders without error", func(t *testing.T) {
		t.Parallel()

		bound := requireBound(t, source, niceyaml.NewSummary("bad",
			niceyaml.NewError("first", niceyaml.AtPath(paths.Current().Child("missing").Index(0))),
			yamltest.ErrorAt("second", "value"),
		))

		excerpt := requireExcerpt(t, bound)

		got := yamltest.NewXMLPrinter().Print(excerpt)
		assert.Contains(t, got, "^ second")
		assert.NotContains(t, got, "first")
	})

	t.Run("unresolved root with a resolved child prints no reason", func(t *testing.T) {
		t.Parallel()

		bound := requireBound(t, source, niceyaml.NewError("bad",
			niceyaml.AtPath(paths.Current().Child("missing").Index(0)),
			niceyaml.WithDetails(yamltest.ErrorAt("first", "name")),
		))

		requireUnresolved(t, bound, paths.ErrNotFound)

		requireExcerpt(t, bound, niceyaml.WithContextLines(0))

		// The child resolves, so the excerpt marks it, and the root keeps
		// its message in the tree without a position or a reason.
		assert.Equal(t,
			stringtest.JoinLF(
				"$.missing[0]: bad",
				"`-- 1:7: $.name: first",
				"",
				"   1 | name: test",
				"     |       ^^^^ first",
			),
			niceyaml.FormatError(bound, niceyaml.WithContextLines(0)),
		)
		assert.NotContains(t, render(bound), "no excerpt:")
	})
}

// excerptSource returns a new ten-line source whose second and eighth
// lines are far enough apart that an excerpt with one line of context
// shows them as two hunks.
func excerptSource() *niceyaml.Source {
	return niceyaml.NewSourceFromString(stringtest.LinesLF(
		"a: 1",
		"b: 2",
		"c: 3",
		"d: 4",
		"e: 5",
		"f: 6",
		"g: 7",
		"h: 8",
		"i: 9",
		"j: 10",
	))
}

// excerptError binds an error on the value of b with a nested error on the
// value of h to the source [excerptSource] returns, and returns the bound
// error.
func excerptError(t *testing.T) *niceyaml.SourceError {
	t.Helper()

	err := yamltest.Bind(t, excerptSource(), niceyaml.NewError(
		"bad b",
		niceyaml.AtPath(paths.Current().Child("b")),
		niceyaml.WithDetails(
			yamltest.ErrorAt("bad h", "h"),
		),
	))

	return yamltest.RequireSourceError(t, err)
}

// requireExcerpt returns the excerpt of bound, and fails the test when
// bound has none.
func requireExcerpt(tb testing.TB, bound *niceyaml.SourceError, opts ...niceyaml.ExcerptOption) *line.View {
	tb.Helper()

	excerpt, ok := bound.Excerpt(opts...)
	require.True(tb, ok)

	return excerpt
}

// assertSameMarks asserts that each line of got carries the overlays and
// the annotations of the line of want at the same index.
func assertSameMarks(t *testing.T, want, got *line.View) {
	t.Helper()

	for i := range want.All() {
		assert.Equal(t, want.Overlays(i), got.Overlays(i), "overlays of line %d", i)
		assert.Equal(t, want.Annotations(i), got.Annotations(i), "annotations of line %d", i)
	}
}

// assertUnmarked asserts that no line of view carries an overlay or an
// annotation. The check skips the lines at the indices in except.
func assertUnmarked(t *testing.T, view *line.View, except ...int) {
	t.Helper()

	for i := range view.All() {
		if slices.Contains(except, i) {
			continue
		}

		assert.Empty(t, view.Overlays(i), "line %d carries no overlay", i)
		assert.Empty(t, view.Annotations(i), "line %d carries no annotation", i)
	}
}

func TestSourceError_Excerpt(t *testing.T) {
	t.Parallel()

	t.Run("holds only the hunk lines with their source numbers", func(t *testing.T) {
		t.Parallel()

		excerpt := requireExcerpt(t, excerptError(t), niceyaml.WithContextLines(1))

		assert.Equal(t, []int{1, 2, 3, 7, 8, 9}, yamltest.LineNumbers(excerpt))
		assert.Equal(t, "b: 2", excerpt.Line(1).Content())
		assert.Equal(t, "h: 8", excerpt.Line(7).Content())
		assert.True(t, excerpt.Contains(7))
		assert.False(t, excerpt.Contains(4), "the excerpt keeps the indices of the source")
	})

	t.Run("overlays the error ranges with the error style", func(t *testing.T) {
		t.Parallel()

		excerpt := requireExcerpt(t, excerptError(t), niceyaml.WithContextLines(1))

		want := yamltest.ErrorOverlays(position.NewSpan(3, 4))
		assert.Equal(t, want, excerpt.Overlays(1), "the main error covers the value of b")
		assert.Equal(t, want, excerpt.Overlays(7), "the nested error covers the value of h")

		for _, i := range []int{0, 2, 6, 8} {
			assert.Empty(t, excerpt.Overlays(i), "line %d carries no overlay", i)
		}
	})

	t.Run("a line several errors mark carries one annotation per message", func(t *testing.T) {
		t.Parallel()

		source := niceyaml.NewSourceFromString("key: value\n")

		// The second and third problems say the same of one value, as two
		// branches of an anyOf do.
		bound := requireBound(t, source, niceyaml.NewSummary("3 problems",
			yamltest.ErrorAt("about value", "key"),
			yamltest.ErrorAt("about value", "key"),
			niceyaml.NewError("about key", niceyaml.AtPosition(position.New(0, 0))),
		))

		excerpt := requireExcerpt(t, bound, niceyaml.WithContextLines(0))

		assert.Equal(t, line.Annotations{
			yamltest.ErrorAnnotation(position.NewSpan(5, 10), "about value"),
			yamltest.ErrorAnnotation(position.NewSpan(0, 3), "about key"),
		}, excerpt.Annotations(0))
		assert.Equal(t,
			yamltest.ErrorOverlays(position.NewSpan(5, 10), position.NewSpan(0, 3)),
			excerpt.Overlays(0),
		)

		// A renderer gives each message a row beside the carets of its own
		// range, in the order of their columns.
		assert.Equal(t, stringtest.JoinLF(
			"   1 | key: value",
			"     | ^^^ about key",
			"     |      ^^^^^ about value",
		), excerpt.String())
	})

	t.Run("a range that covers no column still shows its line", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, "a: 1\nb: 2\n")

		tcs := map[string]struct {
			rng position.Range
		}{
			"empty range": {
				rng: yamltest.LineRange(1, 2, 2),
			},
			"range past the end of the line": {
				rng: yamltest.LineRange(1, 10, 12),
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				bound := yamltest.RequireSourceError(t, dd.Bind(niceyaml.NewError("bad", niceyaml.AtRange(tc.rng))))

				excerpt := requireExcerpt(t, bound, niceyaml.WithContextLines(0))
				assert.Equal(t, []int{2}, yamltest.LineNumbers(excerpt))
			})
		}
	})

	t.Run("the line a location names joins the excerpt when its highlight lies elsewhere", func(t *testing.T) {
		t.Parallel()

		tcs := map[string]struct {
			opt  niceyaml.ErrorOption
			src  string
			row  string
			want []int
		}{
			"position on a line of only spaces in a block scalar": {
				src:  stringtest.LinesLF("script: |", "  echo a", "  ", "  echo b"),
				opt:  niceyaml.AtPosition(position.New(2, 0)),
				want: []int{2, 3, 4},
				row:  stringtest.LinesLF("   3 |   ", "     | ^"),
			},
			"range starting at the end of its first line": {
				src:  stringtest.LinesLF("a: 1", "b: 2"),
				opt:  niceyaml.AtRange(position.NewRange(position.New(0, 4), position.New(1, 1))),
				want: []int{1, 2},
				row:  stringtest.LinesLF("   1 | a: 1", "     |     ^"),
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				src := niceyaml.NewSourceFromString(tc.src)
				bound := requireBound(t, src, niceyaml.NewError("bad", tc.opt))

				excerpt := requireExcerpt(t, bound, niceyaml.WithContextLines(0))
				assert.Equal(t, tc.want, yamltest.LineNumbers(excerpt))

				formatted := niceyaml.FormatError(bound, niceyaml.WithContextLines(0))
				assert.Contains(t, formatted, tc.row)
				assert.NotContains(t, formatted, "...")
			})
		}
	})

	t.Run("an annotation past the end of its line sits after the last rune", func(t *testing.T) {
		t.Parallel()

		// A renderer that spent a cell on every column up to this one
		// would panic.
		far := position.New(0, math.MaxInt/2)

		tcs := map[string]struct {
			err     *niceyaml.Error
			want    line.Annotations
			row     string
			printed string
		}{
			"nested position": {
				err: niceyaml.NewError("top", niceyaml.WithDetails(
					niceyaml.NewError("far", niceyaml.AtPosition(far)),
				)),
				want:    line.Annotations{yamltest.ErrorAnnotation(position.NewSpan(4, 4), "far")},
				row:     "     |     ^ far",
				printed: "<textError>    ^ far</textError>",
			},
			"nested range": {
				err: niceyaml.NewError("top", niceyaml.WithDetails(
					niceyaml.NewError("far", niceyaml.AtRange(position.NewRange(far, position.New(0, far.Col+2)))),
				)),
				want:    line.Annotations{yamltest.ErrorAnnotation(position.NewSpan(4, 4), "far")},
				row:     "     |     ^ far",
				printed: "<textError>    ^ far</textError>",
			},
			"root position": {
				err:     niceyaml.NewError("far", niceyaml.AtPosition(far)),
				want:    line.Annotations{yamltest.ErrorAnnotation(position.NewSpan(4, 4), "")},
				row:     "     |     ^",
				printed: "<textError>    ^</textError>",
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				bound := requireBound(t, niceyaml.NewSourceFromString("a: 1\n"), tc.err)

				excerpt := requireExcerpt(t, bound, niceyaml.WithContextLines(0))
				assert.Equal(t, tc.want, excerpt.Annotations(0))

				formatted := niceyaml.FormatError(bound, niceyaml.WithContextLines(0))
				assert.Contains(t, formatted, fmt.Sprintf("1:%d: far", far.Col+1), "the tree keeps the column as given")
				assert.True(t, strings.HasSuffix(formatted, "\n"+tc.row), formatted)
				assert.True(t, strings.HasSuffix(renderContext(bound, 0), tc.printed))
			})
		}
	})

	t.Run("annotates nested messages below their lines", func(t *testing.T) {
		t.Parallel()

		excerpt := requireExcerpt(t, excerptError(t), niceyaml.WithContextLines(1))

		assert.Equal(
			t,
			line.Annotations{yamltest.ErrorAnnotation(position.NewSpan(3, 4), "bad h")},
			excerpt.Annotations(7).Filter(line.Below),
		)
		assert.Equal(
			t,
			line.Annotations{yamltest.ErrorAnnotation(position.NewSpan(3, 4), "")},
			excerpt.Annotations(1),
			"the main error has no message of its own, so its line carries a marker alone",
		)
	})

	t.Run("separates hunks after the first with an ellipsis", func(t *testing.T) {
		t.Parallel()

		excerpt := requireExcerpt(t, excerptError(t), niceyaml.WithContextLines(1))

		assert.Equal(t, line.Annotations{
			{Content: "...", Kind: kind.UISeparator, Placement: line.Above},
		}, excerpt.Annotations(6))
		assert.Empty(t, excerpt.Annotations(0), "the first hunk has no separator")

		for _, i := range []int{1, 2, 7, 8} {
			assert.Empty(t, excerpt.Annotations(i).Filter(line.Above), "line %d has no separator", i)
		}
	})

	t.Run("prints as the render body", func(t *testing.T) {
		t.Parallel()

		bound := excerptError(t)

		excerpt := requireExcerpt(t, bound, niceyaml.WithContextLines(1))

		want := stringtest.JoinLF(
			"<nameTag>a</nameTag><punctuationMappingValue>:</punctuationMappingValue><text> </text><literalNumberInteger>1</literalNumberInteger>",
			"<nameTag>b</nameTag><punctuationMappingValue>:</punctuationMappingValue><text> </text><genericError>2</genericError>",
			"<textError>   ^</textError>",
			"<nameTag>c</nameTag><punctuationMappingValue>:</punctuationMappingValue><text> </text><literalNumberInteger>3</literalNumberInteger>",
			"<uiSeparator>...</uiSeparator>",
			"<nameTag>g</nameTag><punctuationMappingValue>:</punctuationMappingValue><text> </text><literalNumberInteger>7</literalNumberInteger>",
			"<nameTag>h</nameTag><punctuationMappingValue>:</punctuationMappingValue><text> </text><genericError>8</genericError>",
			"<textError>   ^ bad h</textError>",
			"<nameTag>i</nameTag><punctuationMappingValue>:</punctuationMappingValue><text> </text><literalNumberInteger>9</literalNumberInteger>",
		)

		got := stringtest.TrimLineEnds(yamltest.NewXMLPrinter().Print(excerpt))
		assert.Equal(t, want, got)

		assert.Equal(
			t,
			stringtest.LinesLF("2:4: $.b: bad b", "└── 8:4: $.h: bad h", "")+want,
			renderContext(bound, 1),
		)
	})

	t.Run("negative context shows the marked lines alone", func(t *testing.T) {
		t.Parallel()

		bound := excerptError(t)

		zero := requireExcerpt(t, bound, niceyaml.WithContextLines(0))

		negative := requireExcerpt(t, bound, niceyaml.WithContextLines(-1))

		assert.Equal(t, []int{2, 8}, yamltest.LineNumbers(zero))
		assert.Equal(t, yamltest.LineNumbers(zero), yamltest.LineNumbers(negative))
		assert.Equal(t, yamltest.NewXMLPrinter().Print(zero), yamltest.NewXMLPrinter().Print(negative))
	})
}

func TestSourceError_Annotate(t *testing.T) {
	t.Parallel()

	t.Run("marks the error and every binding below it with their messages", func(t *testing.T) {
		t.Parallel()

		bound := excerptError(t)
		view := bound.Source().View()

		require.True(t, bound.Annotate(view))

		assert.Equal(t, []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}, yamltest.LineNumbers(view))

		want := yamltest.ErrorOverlays(position.NewSpan(3, 4))
		assert.Equal(t, want, view.Overlays(1))
		assert.Equal(t, want, view.Overlays(7))
		assert.Equal(
			t,
			line.Annotations{yamltest.ErrorAnnotation(position.NewSpan(3, 4), "bad b")},
			view.Annotations(1),
		)
		assert.Equal(
			t,
			line.Annotations{yamltest.ErrorAnnotation(position.NewSpan(3, 4), "bad h")},
			view.Annotations(7),
		)

		assertUnmarked(t, view, 1, 7)

		// The excerpt is the same view cut down to its hunks, with the
		// indices of the source, except that the root's line carries a
		// caret run alone since the tree above the excerpt names it.
		excerpt := requireExcerpt(t, bound, niceyaml.WithContextLines(0))
		assert.Equal(t, view.Overlays(1), excerpt.Overlays(1))
		assert.Equal(t, view.Overlays(7), excerpt.Overlays(7))
		assert.Equal(
			t,
			line.Annotations{yamltest.ErrorAnnotation(position.NewSpan(3, 4), "")},
			excerpt.Annotations(1).Filter(line.Below),
		)
		assert.Equal(t, view.Annotations(7), excerpt.Annotations(7).Filter(line.Below))
	})

	t.Run("a tab in the message becomes four spaces", func(t *testing.T) {
		t.Parallel()

		source := niceyaml.NewSourceFromString("a: 1\n")
		err := yamltest.Bind(t, source, yamltest.ErrorAt("did you mean:\n\tb\x1b", "a"))

		bound := yamltest.RequireSourceError(t, err)

		view := source.View()

		require.True(t, bound.Annotate(view))

		// The annotation keeps the other control characters, which a
		// renderer draws as their pictures.
		assert.Equal(
			t,
			line.Annotations{yamltest.ErrorAnnotation(position.NewSpan(3, 4), "did you mean:\n    b\x1b")},
			view.Annotations(0),
		)
		assert.Contains(t, view.String(), "^ did you mean:\u240a    b\u241b")
	})

	t.Run("messages on one line get a row each in column order", func(t *testing.T) {
		t.Parallel()

		source := niceyaml.NewSourceFromString("key: value\n")
		err := yamltest.Bind(t, source, errors.Join(
			niceyaml.NewError("about value", niceyaml.AtPosition(position.New(0, 5))),
			niceyaml.NewError("about key", niceyaml.AtPosition(position.New(0, 0))),
		))

		// The join binds with no location of its own, so only its
		// children mark the view.
		view := source.View()
		require.True(t, niceyaml.Annotate(err, view))

		rows := stringtest.JoinLF(
			"     | ^^^ about key",
			"     |      ^^^^^ about value",
		)
		assert.Contains(t, view.String(), rows)
		assert.Contains(t, niceyaml.FormatError(err, niceyaml.WithContextLines(0)), rows)
	})

	t.Run("a binding the tree reaches twice marks its line once", func(t *testing.T) {
		t.Parallel()

		source := niceyaml.NewSourceFromString("a: 1\nb: 2\n")
		badA := yamltest.Bind(t, source, yamltest.ErrorAt("bad a", "a"))
		parent := yamltest.Bind(t, source, niceyaml.NewError("parent", niceyaml.WithDetails(badA)))
		err := yamltest.Bind(t, source, errors.Join(parent, badA))

		got := niceyaml.FormatError(err, niceyaml.WithContextLines(1))
		assert.Contains(t, got, "^ bad a")
		assert.NotContains(t, got, "bad a; bad a")

		// The excerpt marks the view as Annotate marks it.
		view := source.View()
		require.True(t, niceyaml.Annotate(err, view))

		bound := yamltest.RequireSourceError(t, err)

		excerpt := requireExcerpt(t, bound, niceyaml.WithContextLines(0))
		assert.Equal(t, view.Annotations(0), excerpt.Annotations(0))

		// A walk of the tree meets the binding once per node that holds it,
		// and the line keeps the mark of the first.
		walked := source.View()
		for node := range niceyaml.NewErrorTree(err).All() {
			node.Bound.Annotate(walked)
		}

		assertSameMarks(t, view, walked)
	})

	t.Run("a binding reached along many paths marks in linear time", func(t *testing.T) {
		t.Parallel()

		// Every binding nests the one before it twice, so a walk that
		// visits a binding once per path takes 2^40 steps.
		source := niceyaml.NewSourceFromString("a: 1\n")
		err := yamltest.Bind(t, source, yamltest.ErrorAt("bad a", "a"))

		for range 40 {
			err = yamltest.Bind(t, source, niceyaml.NewSummary("summary", err, err))
		}

		bound := yamltest.RequireSourceError(t, err)

		excerpt := requireExcerpt(t, bound, niceyaml.WithContextLines(0))
		assert.Equal(
			t,
			line.Annotations{yamltest.ErrorAnnotation(position.NewSpan(3, 4), "bad a")},
			excerpt.Annotations(0).Filter(line.Below),
		)
	})

	t.Run("two errors annotate one view", func(t *testing.T) {
		t.Parallel()

		source := excerptSource()
		view := source.View()

		first := requireBound(t, source, yamltest.ErrorAt("bad b", "b"))
		second := requireBound(t, source, niceyaml.NewError(
			"bad d",
			niceyaml.WithDetails(yamltest.ErrorAt("too big", "d")),
		))

		require.True(t, first.Annotate(view))
		require.True(t, second.Annotate(view), "the detail marks its line for an error with no location of its own")

		want := yamltest.ErrorOverlays(position.NewSpan(3, 4))
		assert.Equal(t, want, view.Overlays(1))
		assert.Equal(t, want, view.Overlays(3))
		assert.Equal(
			t,
			line.Annotations{yamltest.ErrorAnnotation(position.NewSpan(3, 4), "bad b")},
			view.Annotations(1),
		)
		assert.Equal(
			t,
			line.Annotations{yamltest.ErrorAnnotation(position.NewSpan(3, 4), "too big")},
			view.Annotations(3),
		)
	})

	t.Run("each error at one value marks its line", func(t *testing.T) {
		t.Parallel()

		tcs := map[string]struct {
			// The message of each error bound at the value of b, in order.
			msgs []string
			want line.Annotations
			// The row a renderer draws below the line.
			row string
		}{
			"two errors on one line each add an annotation": {
				msgs: []string{"bad b", "too big"},
				want: line.Annotations{
					yamltest.ErrorAnnotation(position.NewSpan(3, 4), "bad b"),
					yamltest.ErrorAnnotation(position.NewSpan(3, 4), "too big"),
				},
				row: "     |    ^ bad b; too big",
			},
			// Two bindings say the same of one value, as two branches of an
			// anyOf do.
			"a message that repeats at one column reads once": {
				msgs: []string{"bad b", "bad b"},
				want: line.Annotations{yamltest.ErrorAnnotation(position.NewSpan(3, 4), "bad b")},
				row:  "     |    ^ bad b",
			},
			"an error with no message marks a caret run alone": {
				msgs: []string{""},
				want: line.Annotations{yamltest.ErrorAnnotation(position.NewSpan(3, 4), "")},
				row:  "     |    ^",
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				source := excerptSource()
				view := source.View()

				for _, msg := range tc.msgs {
					bound := requireBound(t, source, yamltest.ErrorAt(msg, "b"))
					require.True(t, bound.Annotate(view), "the view holds the line, marked or not")
				}

				assert.Equal(t, yamltest.ErrorOverlays(position.NewSpan(3, 4)), view.Overlays(1))
				assert.Equal(t, tc.want, view.Annotations(1))
				assert.Equal(t, stringtest.JoinLF(
					"   2 | b: 2",
					tc.row,
				), view.Slice(position.NewSpan(1, 2)).String())
			})
		}
	})

	t.Run("marking a view twice leaves it as marking it once does", func(t *testing.T) {
		t.Parallel()

		b := paths.Current().Child("b")

		tcs := map[string]struct {
			err error
			// The lines of the source the error marks, by index.
			want []int
		}{
			"error at a path": {
				err:  yamltest.ErrorAt("bad b", "b"),
				want: []int{1},
			},
			"error with no message": {
				err:  yamltest.ErrorAt("", "b"),
				want: []int{1},
			},
			"position past the end of its line": {
				err:  niceyaml.NewError("far", niceyaml.AtPosition(position.New(1, 99))),
				want: []int{1},
			},
			"range over two lines": {
				err: niceyaml.NewError("wide", niceyaml.AtRange(
					position.NewRange(position.New(1, 0), position.New(2, 4)),
				)),
				want: []int{1, 2},
			},
			"detail below a problem": {
				err: niceyaml.NewError("bad b", niceyaml.AtPath(b), niceyaml.WithDetails(
					yamltest.ErrorAt("bad h", "h"),
				)),
				want: []int{1, 7},
			},
			"two problems on one line": {
				err: niceyaml.NewSummary("2 problems",
					niceyaml.NewError("bad key", niceyaml.AtPosition(position.New(1, 0))),
					yamltest.ErrorAt("bad value", "b"),
				),
				want: []int{1},
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				source := excerptSource()
				err := yamltest.Bind(t, source, tc.err)

				once, twice := source.View(), source.View()

				for _, bound := range yamltest.BoundIn(err) {
					bound.Annotate(once)
				}

				for range 2 {
					for _, bound := range yamltest.BoundIn(err) {
						bound.Annotate(twice)
					}
				}

				var marked []int

				for i := range once.Hunks(0).All() {
					marked = append(marked, i)
				}

				assert.Equal(t, tc.want, marked)

				assertSameMarks(t, once, twice)
				assert.Equal(t, once.String(), twice.String())
			})
		}
	})

	t.Run("a position past the end of its line marks the column after the last rune", func(t *testing.T) {
		t.Parallel()

		source := niceyaml.NewSourceFromString("a: 1\n")
		view := source.View()

		bound := requireBound(t, source, niceyaml.NewError(
			"far", niceyaml.AtPosition(position.New(0, math.MaxInt/2)),
		))
		require.True(t, bound.Annotate(view))

		// The location covers no column, so it gets no overlay, and the
		// annotation of no width marks it.
		assert.Empty(t, view.Overlays(0))
		assert.Equal(t, line.Annotations{yamltest.ErrorAnnotation(position.NewSpan(4, 4), "far")}, view.Annotations(0))
		assert.Equal(t, stringtest.JoinLF(
			"   1 | a: 1",
			"     |     ^ far",
		), view.String())
	})

	t.Run("marks a slice of the source by line identity", func(t *testing.T) {
		t.Parallel()

		bound := excerptError(t)

		// Lines 7-10 of the source, which hold h at index 7 and not b.
		view := bound.Source().View().Slice(position.NewSpan(6, 10))

		require.True(t, bound.Annotate(view), "the slice holds the line of the detail")

		assert.Equal(t, []int{7, 8, 9, 10}, yamltest.LineNumbers(view))
		assert.Equal(t, yamltest.ErrorOverlays(position.NewSpan(3, 4)), view.Overlays(7))
		assert.Equal(
			t,
			line.Annotations{yamltest.ErrorAnnotation(position.NewSpan(3, 4), "bad h")},
			view.Annotations(7),
		)

		assertUnmarked(t, view, 7)

		// The line of b is outside the slice, so its mark never renders.
		assert.False(t, view.Contains(1))
		assert.NotContains(t, view.String(), "b: 2")
	})

	t.Run("marks a line once however many spans select it", func(t *testing.T) {
		t.Parallel()

		bound := excerptError(t)
		view := bound.Source().View().Slice(position.NewSpan(1, 2), position.NewSpan(1, 2))

		require.True(t, bound.Annotate(view))

		require.Equal(t, 1, view.Count())
		assert.Equal(t, yamltest.ErrorOverlays(position.NewSpan(3, 4)), view.Overlays(1))
		assert.Equal(t, stringtest.JoinLF(
			"   2 | b: 2",
			"     |    ^ bad b",
		), view.String())
	})

	t.Run("marks a diff on the lines of this revision", func(t *testing.T) {
		t.Parallel()

		before := niceyaml.NewSourceFromString("a: 1\nb: 3\n")
		after := niceyaml.NewSourceFromString("a: 1\nb: 2\n")

		bound := requireBound(t, after, yamltest.ErrorAt("bad b", "b"))

		// The unified view holds a from after, then b from before as a
		// deleted line, then b from after as an inserted one.
		view := diff.Diff(before.Lines(), after.Lines()).Unified()
		require.Equal(t, 3, view.Count())

		require.True(t, bound.Annotate(view))

		assert.Empty(t, view.Overlays(0))
		assert.Empty(t, view.Overlays(1), "the deleted line of the other revision stays unmarked")
		assert.Equal(t, yamltest.ErrorOverlays(position.NewSpan(3, 4)), view.Overlays(2))
	})

	t.Run("marks a diff on the deleted lines of the before revision", func(t *testing.T) {
		t.Parallel()

		// The unified view holds a from after, b from before as a deleted
		// line, b from after as an inserted one, then c from after.
		unified := func(r *diff.Result) *line.View { return r.Unified() }
		hunks := func(r *diff.Result) *line.View { return r.Hunks(2) }
		aligned := func(r *diff.Result) *line.View { return r.Before() }

		tcs := map[string]struct {
			view  func(r *diff.Result) *line.View
			key   string
			index int // The line the error marks when want is true.
			want  bool
		}{
			"unchanged line in the unified view": {
				view: unified,
				key:  "a",
			},
			"unchanged line in the hunks": {
				view: hunks,
				key:  "a",
			},
			"unchanged line in the before view": {
				view:  aligned,
				key:   "a",
				index: 0,
				want:  true,
			},
			"deleted line in the unified view": {
				view:  unified,
				key:   "b",
				index: 1,
				want:  true,
			},
			"deleted line in the hunks": {
				view:  hunks,
				key:   "b",
				index: 1,
				want:  true,
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				before := niceyaml.NewSourceFromString(stringtest.LinesLF("a: 1", "b: 2", "c: 3"))
				after := niceyaml.NewSourceFromString(stringtest.LinesLF("a: 1", "b: 9", "c: 3"))

				bound := requireBound(t, before, yamltest.ErrorAt("bad "+tc.key, tc.key))

				view := tc.view(diff.Diff(before.Lines(), after.Lines()))
				require.Equal(t, tc.want, bound.Annotate(view))

				for i := range view.All() {
					if tc.want && i == tc.index {
						assert.Equal(t,
							yamltest.ErrorOverlays(position.NewSpan(3, 4)),
							view.Overlays(i))

						continue
					}

					assert.Empty(t, view.Overlays(i), "line %d carries no overlay", i)
				}
			})
		}
	})

	t.Run("marks nothing on a view without the marked lines", func(t *testing.T) {
		t.Parallel()

		bound := excerptError(t)

		tcs := map[string]struct {
			view *line.View
		}{
			"slice that holds neither line": {
				view: bound.Source().View().Slice(position.NewSpan(3, 6)),
			},
			"view of another source with the same text": {
				view: excerptSource().View(),
			},
			"empty view": {
				view: line.NewView(line.Lines{}),
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				// The location resolved, and the view holds none of its lines.
				require.False(t, bound.Annotate(tc.view))
				require.NoError(t, bound.Unresolved())
				assertUnmarked(t, tc.view)
			})
		}
	})

	t.Run("leaves the view unmarked when nothing resolves", func(t *testing.T) {
		t.Parallel()

		source := excerptSource()

		tcs := map[string]struct {
			err *niceyaml.Error
			is  error
		}{
			"no location": {
				err: niceyaml.NewError("bad"),
			},
			"path that does not resolve": {
				err: niceyaml.NewError("bad", niceyaml.AtPath(paths.Current().Child("missing").Index(0))),
				is:  paths.ErrNotFound,
			},
			"range past the last line": {
				err: niceyaml.NewError("bad",
					niceyaml.AtRange(yamltest.LineRange(20, 0, 1)),
				),
				is: niceyaml.ErrOutOfRange,
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				bound := requireBound(t, source, tc.err)

				view := source.View()

				require.False(t, bound.Annotate(view))
				requireUnresolved(t, bound, tc.is)
				assertUnmarked(t, view)
			})
		}
	})
}

// positivePort is a port that must be positive, for the decodes of
// [TestAnnotate].
type positivePort int

// Validate implements [niceyaml.SelfValidator].
func (p positivePort) Validate() error {
	if p < 1 {
		return niceyaml.NewError("port must be positive")
	}

	return nil
}

// portsConfig is the target the decodes of [TestAnnotate] fill.
type portsConfig struct {
	Name string       `yaml:"name"`
	A    positivePort `yaml:"a"`
	B    positivePort `yaml:"b"`
}

func TestAnnotate(t *testing.T) {
	t.Parallel()

	// Decode input into a [portsConfig] and return its source with the
	// error of the decode.
	decode := func(input string) func(*testing.T) (*niceyaml.Source, error) {
		return func(t *testing.T) (*niceyaml.Source, error) {
			t.Helper()

			source := niceyaml.NewSourceFromString(input)

			var cfg portsConfig

			err := source.DecodeInto(t.Context(), &cfg)
			require.Error(t, err)

			return source, err
		}
	}

	t.Run("every walk of an error marks its whole tree", func(t *testing.T) {
		t.Parallel()

		// Each walk marks view with err the way a caller might, and
		// reports whether any call of it marked a line.
		walks := map[string]func(err error, view *line.View) bool{
			"Annotate": niceyaml.Annotate,
			"Annotate twice": func(err error, view *line.View) bool {
				niceyaml.Annotate(err, view)

				return niceyaml.Annotate(err, view)
			},
			"errors.As then Annotate of the binding": func(err error, view *line.View) bool {
				var bound *niceyaml.SourceError

				return errors.As(err, &bound) && niceyaml.Annotate(bound, view)
			},
			"errors.As then SourceError.Annotate": func(err error, view *line.View) bool {
				var bound *niceyaml.SourceError

				return errors.As(err, &bound) && bound.Annotate(view)
			},
			"loop over Bindings": func(err error, view *line.View) bool {
				marked := false

				for bound := range niceyaml.Bindings(err) {
					marked = bound.Annotate(view) || marked
				}

				return marked
			},
			"loop over the bindings of the tree": func(err error, view *line.View) bool {
				marked := false

				for _, bound := range yamltest.BoundIn(err) {
					marked = bound.Annotate(view) || marked
				}

				return marked
			},
			"loop over ErrorTree.Problems": func(err error, view *line.View) bool {
				marked := false

				for problem := range niceyaml.NewErrorTree(err).Problems() {
					marked = problem.Bound.Annotate(view) || marked
				}

				return marked
			},
			"loop over ErrorTree.All": func(err error, view *line.View) bool {
				marked := false

				for node := range niceyaml.NewErrorTree(err).All() {
					marked = node.Bound.Annotate(view) || marked
				}

				return marked
			},
		}

		tcs := map[string]struct {
			build func(t *testing.T) (*niceyaml.Source, error)
			want  string
		}{
			"one decode error": {
				build: decode(stringtest.LinesLF("name: x", "a: many", "b: 2")),
				want: stringtest.JoinLF(
					"   1 | name: x",
					"   2 | a: many",
					"     |    ^^^^ expected integer, got string",
					"   3 | b: 2",
				),
			},
			// The decode returns both as one binding of a join, which has
			// no location of its own.
			"two self-validation errors": {
				build: decode(stringtest.LinesLF("name: x", "a: 0", "b: -1")),
				want: stringtest.JoinLF(
					"   1 | name: x",
					"   2 | a: 0",
					"     |    ^ port must be positive",
					"   3 | b: -1",
					"     |    ^^ port must be positive",
				),
			},
			"summary over a problem with a detail and a second problem": {
				build: func(t *testing.T) (*niceyaml.Source, error) {
					t.Helper()

					source := niceyaml.NewSourceFromString(stringtest.LinesLF("a: 1", "b: 2", "c: 3"))

					return source, yamltest.Bind(t, source, niceyaml.NewSummary("2 problems",
						niceyaml.NewError("a conflicts",
							niceyaml.AtPath(paths.Current().Child("a")),
							niceyaml.WithDetails(
								yamltest.ErrorAt("with b", "b"),
							),
						),
						yamltest.ErrorAt("bad c", "c"),
					))
				},
				want: stringtest.JoinLF(
					"   1 | a: 1",
					"     |    ^ a conflicts",
					"   2 | b: 2",
					"     |    ^ with b",
					"   3 | c: 3",
					"     |    ^ bad c",
				),
			},
			// The join has no line of its own, so its location at c adds
			// no mark beside those of its problems.
			"join with a location over located problems": {
				build: func(t *testing.T) (*niceyaml.Source, error) {
					t.Helper()

					source := niceyaml.NewSourceFromString(stringtest.LinesLF("a: 1", "b: 2", "c: 3"))

					return source, yamltest.Bind(t, source, niceyaml.Invalid(
						errors.Join(yamltest.ErrorAt("bad a", "a"), yamltest.ErrorAt("bad b", "b")),
						niceyaml.AtPath(paths.Current().Child("c")),
					))
				},
				want: stringtest.JoinLF(
					"   1 | a: 1",
					"     |    ^ bad a",
					"   2 | b: 2",
					"     |    ^ bad b",
					"   3 | c: 3",
				),
			},
		}

		for name, tc := range tcs {
			for walkName, walk := range walks {
				t.Run(name+"/"+walkName, func(t *testing.T) {
					t.Parallel()

					source, err := tc.build(t)

					view := source.View()
					require.True(t, walk(err, view))
					assert.Equal(t, tc.want, view.String())

					want := source.View()
					require.True(t, niceyaml.Annotate(err, want))
					assertSameMarks(t, want, view)
				})
			}
		}
	})

	t.Run("a loop that skips a binding still marks it below one it keeps", func(t *testing.T) {
		t.Parallel()

		source := niceyaml.NewSourceFromString("a: 1\nb: 2\n")
		err := yamltest.Bind(t, source, niceyaml.NewError("a conflicts",
			niceyaml.AtPath(paths.Current().Child("a")),
			niceyaml.WithDetails(yamltest.ErrorAt("with b", "b")),
		))

		view := source.View()

		for _, bound := range yamltest.BoundIn(err) {
			if len(yamltest.DetailsOf(bound)) > 0 {
				bound.Annotate(view)
			}
		}

		assert.Equal(t, stringtest.JoinLF(
			"   1 | a: 1",
			"     |    ^ a conflicts",
			"   2 | b: 2",
			"     |    ^ with b",
		), view.String())
	})

	t.Run("several errors mark one view", func(t *testing.T) {
		t.Parallel()

		source := niceyaml.NewSourceFromString("a: 1\nb: 2\n")
		badA := yamltest.Bind(t, source, yamltest.ErrorAt("bad a", "a"))
		badB := yamltest.Bind(t, source, yamltest.ErrorAt("bad b", "b"))

		// The second call holds the binding of the first again, and its
		// line keeps the one mark.
		view := source.View()
		require.True(t, niceyaml.Annotate(badA, view))
		require.True(t, niceyaml.Annotate(errors.Join(badA, badB), view))

		assert.Equal(t, stringtest.JoinLF(
			"   1 | a: 1",
			"     |    ^ bad a",
			"   2 | b: 2",
			"     |    ^ bad b",
		), view.String())
	})

	t.Run("errors of two files mark the view of one with its own errors", func(t *testing.T) {
		t.Parallel()

		// Both files hold the same text, so only the identity of a line
		// tells them apart.
		const text = "a: 1\nb: 2\n"

		manifest := niceyaml.NewSourceFromString(text, niceyaml.WithName("manifest.yaml"))
		values := niceyaml.NewSourceFromString(text, niceyaml.WithName("values.yaml"))

		err := errors.Join(
			yamltest.Bind(t, manifest, yamltest.ErrorAt("bad a", "a")),
			yamltest.Bind(t, values, yamltest.ErrorAt("bad b", "b")),
		)

		view := values.View()
		require.True(t, niceyaml.Annotate(err, view))
		assert.Equal(t, stringtest.JoinLF(
			"   1 | a: 1",
			"   2 | b: 2",
			"     |    ^ bad b",
		), view.String())

		view = manifest.View()
		require.True(t, niceyaml.Annotate(err, view))
		assert.Equal(t, stringtest.JoinLF(
			"   1 | a: 1",
			"     |    ^ bad a",
			"   2 | b: 2",
		), view.String())
	})

	t.Run("marks nothing and reports false", func(t *testing.T) {
		t.Parallel()

		const text = "a: 1\n"

		source := niceyaml.NewSourceFromString(text)
		located := yamltest.Bind(t, source, yamltest.ErrorAt("bad a", "a"))

		tcs := map[string]struct {
			err  error
			view *line.View
		}{
			"nil error": {
				view: source.View(),
			},
			"error bound to no source": {
				err:  yamltest.ErrorAt("bad a", "a"),
				view: source.View(),
			},
			"nil binding": {
				err:  (*niceyaml.SourceError)(nil),
				view: source.View(),
			},
			"binding with no location": {
				err:  yamltest.Bind(t, source, errors.New("plain")),
				view: source.View(),
			},
			"binding whose path selects nothing": {
				err: yamltest.Bind(t, source, niceyaml.NewError("bad",
					niceyaml.AtPath(paths.Current().Child("missing").Index(0)),
				)),
				view: source.View(),
			},
			"view of another source with the same text": {
				err:  located,
				view: niceyaml.NewSourceFromString(text).View(),
			},
			"view that holds no line": {
				err:  located,
				view: source.View().Slice(position.Span{}),
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				require.False(t, niceyaml.Annotate(tc.err, tc.view))

				for i := range source.Lines().Len() {
					assert.Empty(t, tc.view.Overlays(i), "line %d carries no overlay", i)
					assert.Empty(t, tc.view.Annotations(i), "line %d carries no annotation", i)
				}
			})
		}
	})
}

func TestSourceError_TreeBranches(t *testing.T) {
	t.Parallel()

	// The excerpt marks every located Error in the tree, however it got there.

	source := niceyaml.NewSourceFromString(stringtest.LinesLF("a: 1", "b: 2", "c: 3"))
	badA := yamltest.ErrorAt("bad a", "a")
	badB := yamltest.ErrorAt("bad b", "b")
	badC := yamltest.ErrorAt("bad c", "c")

	t.Run("join branches bind as children", func(t *testing.T) {
		t.Parallel()

		err := yamltest.Bind(t, source, errors.Join(badA, badB))

		// The join has no location of its own, each branch is a child
		// with one, and the excerpt marks both with their messages. The
		// %+v verb leads with the branches behind their positions, since
		// the join says nothing they do not.
		assert.Equal(t, stringtest.JoinLF("|-- 1:4: $.a: bad a", "`-- 2:4: $.b: bad b"), report(err))
		require.Len(t, slices.Collect(niceyaml.Bindings(err)), 1)

		tree, excerpt := renderParts(err)
		assert.Equal(t, stringtest.JoinLF("├── 1:4: $.a: bad a", "└── 2:4: $.b: bad b"), tree)
		assert.Contains(t, excerpt, "<genericError>1</genericError>")
		assert.Contains(t, excerpt, "<genericError>2</genericError>")
		assert.Contains(t, excerpt, "^ bad a")
		assert.Contains(t, excerpt, "^ bad b")
	})

	t.Run("a join of bound branches lists each branch once", func(t *testing.T) {
		t.Parallel()

		// Each branch printed the position its own binding resolved, so
		// the join's message is the text of its children as they read,
		// and the %+v verb leads with them instead of repeating both.
		err := yamltest.Bind(t, source, errors.Join(
			yamltest.Bind(t, source, badA),
			yamltest.Bind(t, source, badB),
		))

		assert.Equal(t, stringtest.JoinLF("1:4: $.a: bad a", "2:4: $.b: bad b"), err.Error())
		assert.Equal(t, stringtest.JoinLF("|-- 1:4: $.a: bad a", "`-- 2:4: $.b: bad b"), report(err))
	})

	t.Run("a join in a named source names the source of each branch", func(t *testing.T) {
		t.Parallel()

		named := func(name string) *niceyaml.Source {
			return niceyaml.NewSourceFromString(stringtest.LinesLF("a: 1", "b: 2", "c: 3"), niceyaml.WithName(name))
		}

		src := named("f.yaml")
		joinSrc := named("c.yaml")
		joinOfOthers := yamltest.Bind(t, src, errors.Join(
			yamltest.Bind(t, named("a.yaml"), badA),
			yamltest.Bind(t, named("b.yaml"), badB),
		))

		unnamed := niceyaml.NewSourceFromString(stringtest.LinesLF("a: 1", "b: 2", "c: 3"))
		ownLead := yamltest.Bind(t, src, badA)
		unnamedB := yamltest.Bind(t, unnamed, badB)

		// Each case holds the join as its source binds it.
		tcs := map[string]struct {
			err  error
			want string
		}{
			// Each branch names its own source, so the join puts no name in
			// front of them, whichever source it binds to.
			"bindings of the source of the join": {
				err: yamltest.Bind(t, src, errors.Join(
					yamltest.Bind(t, src, badA),
					yamltest.Bind(t, src, badB),
				)),
				want: stringtest.JoinLF("f.yaml:1:4: $.a: bad a", "f.yaml:2:4: $.b: bad b"),
			},
			"bindings of two other sources": {
				err: yamltest.Bind(t, named("c.yaml"), errors.Join(
					yamltest.Bind(t, named("a.yaml"), badA),
					yamltest.Bind(t, named("b.yaml"), badB),
				)),
				want: stringtest.JoinLF("a.yaml:1:4: $.a: bad a", "b.yaml:2:4: $.b: bad b"),
			},
			// A binding of another source names that source alone, and a
			// branch the join binds itself names the source of the join. A
			// branch that joins bindings of other sources holds no text of its
			// own, so it adds no name.
			"binding of another source beside a branch the join binds": {
				err:  yamltest.Bind(t, named("c.yaml"), errors.Join(yamltest.Bind(t, named("a.yaml"), badA), badB)),
				want: stringtest.JoinLF("a.yaml:1:4: $.a: bad a", "c.yaml:2:4: $.b: bad b"),
			},
			"join of bindings of other sources as a branch": {
				err: yamltest.Bind(t, named("c.yaml"), errors.Join(
					errors.Join(yamltest.Bind(t, named("a.yaml"), badA), yamltest.Bind(t, named("b.yaml"), badB)),
					yamltest.Bind(t, named("b.yaml"), badB),
				)),
				want: stringtest.JoinLF(
					"a.yaml:1:4: $.a: bad a",
					"b.yaml:2:4: $.b: bad b",
					"b.yaml:2:4: $.b: bad b",
				),
			},
			// So does an Error that only wraps a binding of such a join.
			"Error that wraps a binding of a join of another source": {
				err: yamltest.Bind(t, joinSrc, errors.Join(
					yamltest.Bind(t, named("a.yaml"), badA),
					niceyaml.Invalid(yamltest.Bind(t, joinSrc, errors.Join(
						yamltest.Bind(t, named("b.yaml"), badB),
					))),
				)),
				want: stringtest.JoinLF("a.yaml:1:4: $.a: bad a", "b.yaml:2:4: $.b: bad b"),
			},
			// A binding of such a join to this source has no line of its own,
			// so its branches stand in its place. A join that leads with that
			// binding, or a summary that heads it, lists them beside the child
			// that belongs to this source, with the sources in the order they
			// first appear.
			"join that leads with a binding of a join of other sources": {
				err: yamltest.Bind(t, src, errors.Join(joinOfOthers, badC)),
				want: stringtest.JoinLF(
					"a.yaml:1:4: $.a: bad a",
					"b.yaml:2:4: $.b: bad b",
					"f.yaml:3:4: $.c: bad c",
				),
			},
			"summary that heads a binding of a join of other sources": {
				err: yamltest.Bind(t, src, niceyaml.NewSummary("2 checks", joinOfOthers, badC)),
				want: stringtest.JoinLF(
					"f.yaml: 2 checks",
					"a.yaml:1:4: $.a: bad a",
					"b.yaml:2:4: $.b: bad b",
					"f.yaml:3:4: $.c: bad c",
				),
			},
			// A binding of a source with no name has no name to put in front
			// of its branches, so a branch of that source carries its position
			// alone, and one bound to this source keeps the name it carries.
			"join that leads with a binding of a source with no name": {
				err: yamltest.Bind(t, src, errors.Join(
					yamltest.Bind(t, unnamed, errors.Join(ownLead, unnamedB)),
					badC,
				)),
				want: stringtest.JoinLF(
					"f.yaml:1:4: $.a: bad a",
					"f.yaml:3:4: $.c: bad c",
					"2:4: $.b: bad b",
				),
			},
			"summary of a source with no name that heads a binding": {
				err: yamltest.Bind(t, src, errors.Join(
					yamltest.Bind(t, unnamed, niceyaml.NewSummary("2 checks",
						yamltest.Bind(t, unnamed, errors.Join(ownLead)),
						unnamedB,
					)),
					badC,
				)),
				want: stringtest.JoinLF(
					"f.yaml:3:4: $.c: bad c",
					"2 checks",
					"f.yaml:1:4: $.a: bad a",
					"2:4: $.b: bad b",
				),
			},
			// Every line takes the name, so a join names the source in front
			// of each branch it binds, a branch nested in another join
			// included, and the lines read in the order of their positions.
			"binding in front of a branch the join binds": {
				err:  yamltest.Bind(t, src, errors.Join(yamltest.Bind(t, src, badA), badB)),
				want: stringtest.JoinLF("f.yaml:1:4: $.a: bad a", "f.yaml:2:4: $.b: bad b"),
			},
			"binding nested in another join": {
				err: yamltest.Bind(t, src, errors.Join(
					errors.Join(yamltest.Bind(t, src, badA), badB),
					badC,
				)),
				want: stringtest.JoinLF(
					"f.yaml:1:4: $.a: bad a",
					"f.yaml:2:4: $.b: bad b",
					"f.yaml:3:4: $.c: bad c",
				),
			},
			"binding behind a branch the join binds": {
				err:  yamltest.Bind(t, src, errors.Join(badB, yamltest.Bind(t, src, badA))),
				want: stringtest.JoinLF("f.yaml:1:4: $.a: bad a", "f.yaml:2:4: $.b: bad b"),
			},
			// An Error with details above a binding writes the text of the
			// binding as it is, and the details stay out of the line. An Error
			// that carries a position alone binds anew, so its line carries its
			// own position in front of the one the binding wrote.
			"details above a binding": {
				err: yamltest.Bind(t, src, errors.Join(
					niceyaml.Invalid(yamltest.Bind(t, src, badA), niceyaml.WithDetails(badB)),
					badC,
				)),
				want: stringtest.JoinLF("f.yaml:1:4: $.a: bad a", "f.yaml:3:4: $.c: bad c"),
			},
			"position alone above a binding": {
				err: yamltest.Bind(t, src, errors.Join(
					niceyaml.Invalid(yamltest.Bind(t, src, badA), niceyaml.AtPosition(position.New(0, 3))),
					badC,
				)),
				want: stringtest.JoinLF("f.yaml:1:4: f.yaml:1:4: $.a: bad a", "f.yaml:3:4: $.c: bad c"),
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				assert.Equal(t, tc.want, tc.err.Error())
			})
		}
	})

	t.Run("every multi-error binds the same way", func(t *testing.T) {
		t.Parallel()

		// A join, a wrapper with two %w verbs, and a join inside a
		// wrapper all bind as a root without a location and one child
		// per branch. The message of a join is its branches behind their
		// positions, under the text a wrapper put in front. A wrapper with
		// two %w verbs keeps the message it wrote, which holds the text of
		// its branches and none of their locations, so its branches follow
		// behind their positions.
		tcs := map[string]struct {
			err  error
			want string
		}{
			"join": {
				err:  errors.Join(badA, badB),
				want: stringtest.JoinLF("1:4: $.a: bad a", "2:4: $.b: bad b"),
			},
			"two %w verbs": {
				err: fmt.Errorf("%w; %w", badA, badB),
				want: stringtest.JoinLF(
					"bad a; bad b",
					"1:4: $.a: bad a",
					"2:4: $.b: bad b",
				),
			},
			"wrapped join": {
				err: fmt.Errorf("ctx: %w", errors.Join(badA, badB)),
				want: stringtest.JoinLF(
					"ctx:",
					"1:4: $.a: bad a",
					"2:4: $.b: bad b",
				),
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				err := yamltest.Bind(t, source, tc.err)

				bound := yamltest.RequireSourceError(t, err)
				assert.Equal(t, tc.want, bound.Error())

				requireUnresolved(t, bound, nil)
				assert.Equal(t, []string{"1:4: $.a: bad a", "2:4: $.b: bad b"}, errorTexts(yamltest.ProblemsOf(bound)))
			})
		}
	})

	t.Run("sentinel wrapper binds as the error it classifies", func(t *testing.T) {
		t.Parallel()

		errInvalid := errors.New("invalid")

		// A wrapper with two %w verbs whose other branches carry no
		// location and add no errors binds where its one located branch
		// does, as a wrapper with one %w verb would.
		tcs := map[string]struct {
			err  error
			want string
		}{
			"sentinel first": {
				err:  fmt.Errorf("%w: %w", errInvalid, badB),
				want: "2:4: $.b: invalid: bad b",
			},
			"sentinel last": {
				err:  fmt.Errorf("%w: %w", badB, errInvalid),
				want: "2:4: $.b: bad b: invalid",
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				err := yamltest.Bind(t, source, tc.err)
				require.ErrorIs(t, err, errInvalid)
				assert.Equal(t, tc.want, report(err))

				bound := yamltest.RequireSourceError(t, err)
				assert.Equal(t, 1, yamltest.RangeOf(t, bound).Start.Line)
				assert.Empty(t, yamltest.ProblemsOf(bound))
			})
		}

		t.Run("a summary", func(t *testing.T) {
			t.Parallel()

			err := yamltest.Bind(t, source, fmt.Errorf("%w: %w",
				errInvalid, niceyaml.NewSummary("summary", badA, badC)))

			assert.Equal(t, stringtest.JoinLF(
				"invalid: summary",
				"|-- 1:4: $.a: bad a",
				"`-- 3:4: $.c: bad c",
			), report(err))
		})
	})

	t.Run("branches below a wrapper are annotated", func(t *testing.T) {
		t.Parallel()

		err := yamltest.Bind(t, source, fmt.Errorf("document 0: %w", errors.Join(
			fmt.Errorf("first: %w", badA),
			niceyaml.NewSummary("summary", badB, badC),
		)))

		assert.Equal(t, stringtest.JoinLF(
			"document 0:",
			"1:4: $.a: first: bad a",
			"summary",
			"2:4: $.b: bad b",
			"3:4: $.c: bad c",
		), err.Error())
		assert.Equal(t, stringtest.JoinLF(
			"document 0:",
			"|-- 1:4: $.a: first: bad a",
			"`-- summary",
			"    |-- 2:4: $.b: bad b",
			"    `-- 3:4: $.c: bad c",
		), report(err))

		got := render(err)
		assert.Contains(t, got, "<genericError>1</genericError>")
		assert.Contains(t, got, "^ first: bad a")
		assert.Contains(t, got, "^ bad b")
		assert.Contains(t, got, "^ bad c")
	})

	t.Run("a join branch that does not resolve is listed without a position", func(t *testing.T) {
		t.Parallel()

		missing := niceyaml.NewError("bad x", niceyaml.AtPath(paths.Current().Child("x").Index(0)))
		err := yamltest.Bind(t, source, errors.Join(badA, missing))

		tree, excerpt := renderParts(err)
		assert.Equal(t, stringtest.JoinLF("├── 1:4: $.a: bad a", "└── $.x[0]: bad x"), tree)
		assert.NotContains(t, excerpt, "$.x[0]: bad x")
	})

	t.Run("a bound error inside the tree keeps its own binding", func(t *testing.T) {
		t.Parallel()

		other := niceyaml.NewSourceFromString("z: 9\n")
		inner := yamltest.Bind(t, other, yamltest.ErrorAt("bad z", "z"))
		err := yamltest.Bind(t, source, errors.Join(badA, inner))

		bound := yamltest.RequireSourceError(t, err)
		assert.Same(t, source, bound.Source())

		// The outer binding marks its own branch, and the inner keeps its
		// excerpt from the other source.
		excerpt := requireExcerpt(t, bound, niceyaml.WithContextLines(0))
		assert.Equal(t, 1, excerpt.Count())

		got := render(err)
		assert.Contains(t, got, "<genericError>1</genericError>")
		assert.Contains(t, got, "<genericError>9</genericError>")
	})
}

func TestBindings(t *testing.T) {
	t.Parallel()

	docs := yamltest.Documents(t, niceyaml.NewSourceFromString(stringtest.LinesLF("a: 1", "---", "b: 2")), 2)

	first := docs[0].Bind(yamltest.ErrorAt("bad a", "a"))
	second := docs[1].Bind(yamltest.ErrorAt("bad b", "b"))
	outer := yamltest.Bind(
		t,
		niceyaml.NewSourceFromString("c: 3\n"),
		niceyaml.NewError("outer", niceyaml.WithDetails(first)),
	)

	tcs := map[string]struct {
		err  error
		want []error
	}{
		"nil": {
			err: nil,
		},
		"no binding": {
			err: errors.New("plain"),
		},
		"nil binding is none": {
			err: fmt.Errorf("ctx: %w", (*niceyaml.SourceError)(nil)),
		},
		"nil binding in a join is none": {
			err: errors.Join(errors.New("plain"), (*niceyaml.SourceError)(nil)),
		},
		"one binding": {
			err:  fmt.Errorf("document 0: %w", first),
			want: []error{first},
		},
		"joined bindings in order": {
			err: errors.Join(
				fmt.Errorf("document 0: %w", first),
				fmt.Errorf("document 1: %w", second),
			),
			want: []error{first, second},
		},
		"binding of a binding is that binding": {
			err:  yamltest.Bind(t, niceyaml.NewSourceFromString("c: 3\n"), fmt.Errorf("ctx: %w", first)),
			want: []error{first},
		},
		"a child stays below its parent whatever source it is bound to": {
			err:  outer,
			want: []error{outer},
		},
		"binding in a detail of an Error bound to no source": {
			err:  niceyaml.Unchecked(errors.New("defaults do not load"), niceyaml.WithDetails(first)),
			want: []error{first},
		},
		"bindings an Error bound to no source wraps, heads, and holds as a detail": {
			err: fmt.Errorf("ctx: %w", niceyaml.Invalid(
				niceyaml.NewSummary("2 problems", first, errors.New("plain")),
				niceyaml.WithDetails(second),
			)),
			want: []error{first, second},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := slices.Collect(niceyaml.Bindings(tc.err))

			require.Len(t, got, len(tc.want))

			for i, want := range tc.want {
				assert.Same(t, want, got[i])
			}
		})
	}

	t.Run("stops when the caller does", func(t *testing.T) {
		t.Parallel()

		var got []*niceyaml.SourceError

		for b := range niceyaml.Bindings(errors.Join(first, second)) {
			got = append(got, b)

			break
		}

		require.Len(t, got, 1)
		assert.Same(t, first, got[0])
	})
}

// excerptSecret is the text every secret holds in the tests of
// [niceyaml.WithExcerpts], so a test finds a line of a source with
// excerpts off in any output.
const excerptSecret = "hunter2"

// secretConfig is the value the tests of [niceyaml.WithExcerpts] decode a
// source into.
type secretConfig struct {
	Limits   map[string]int `yaml:"limits"`
	Password string         `yaml:"password"`
	Token    string         `yaml:"token"`
	Address  net.IP         `yaml:"address"`
	Timeout  time.Duration  `yaml:"timeout"`
	Port     int            `yaml:"port"`
	Retries  uint8          `yaml:"retries"`
}

// checkedSecretConfig is a [secretConfig] that validates itself.
type checkedSecretConfig secretConfig

// Validate reports a port below 1.
func (c checkedSecretConfig) Validate() error {
	if c.Port < 1 {
		return yamltest.ErrorAt("port must be at least 1", "port")
	}

	return nil
}

// chainMessages returns the message of err and of each error below it in
// its chain, as a reporter that follows Unwrap reads them. An error that
// unwraps to several adds the chain of each in order.
func chainMessages(err error) []string {
	if err == nil {
		return nil
	}

	msgs := []string{err.Error()}

	switch x := err.(type) { //nolint:errorlint // The walk reads each error of the chain itself.
	case interface{ Unwrap() error }:
		msgs = append(msgs, chainMessages(x.Unwrap())...)

	case interface{ Unwrap() []error }:
		for _, inner := range x.Unwrap() {
			msgs = append(msgs, chainMessages(inner)...)
		}
	}

	return msgs
}

// renderings returns err as each renderer of an error prints it, under
// the name of the renderer, so a test looks for a text in all of them.
// Each excerpt keeps every line of its source. The entry "Unwrap" holds
// what [chainMessages] returns, one message per row.
func renderings(err error) map[string]string {
	const whole = 1000

	var logged bytes.Buffer

	slog.New(slog.NewTextHandler(&logged, nil)).Error("load", slog.Any("err", err))
	slog.New(slog.NewJSONHandler(&logged, nil)).Error("load", slog.Any("err", err))

	var excerpts, rows []string

	for _, excerpt := range niceyaml.NewErrorReport(err, niceyaml.WithContextLines(whole)).Excerpts {
		excerpts = append(excerpts, excerpt.View.String())
	}

	for _, bound := range yamltest.BoundIn(err) {
		if excerpt, ok := bound.Excerpt(niceyaml.WithContextLines(whole)); ok {
			excerpts = append(excerpts, excerpt.String())
		}

		for _, excerpt := range niceyaml.NewErrorReport(bound, niceyaml.WithContextLines(whole)).Excerpts {
			excerpts = append(excerpts, excerpt.View.String())
		}
	}

	for node := range niceyaml.NewErrorTree(err).All() {
		rows = append(rows, node.Label)
	}

	return map[string]string{
		"Error":       err.Error(),
		"%v":          fmt.Sprintf("%v", err),
		"%+v":         fmt.Sprintf("%+v", err),
		"FormatError": niceyaml.FormatError(err, niceyaml.WithContextLines(whole)),
		"PrintError":  printer.New().PrintError(err, niceyaml.WithContextLines(whole)),
		"Excerpts":    stringtest.JoinLF(excerpts...),
		"ErrorTree":   stringtest.JoinLF(rows...),
		"slog":        logged.String(),
		"Unwrap":      stringtest.JoinLF(chainMessages(err)...),
	}
}

func TestWithExcerptWidth(t *testing.T) {
	t.Parallel()

	// The value is 200 columns long, so the line runs past the default
	// width.
	value := strings.Repeat("0123456789", 20)
	input := "port: " + value + "\n"

	// Rows returns the excerpt of an error at the port that shows the
	// first shown columns of the value, with an ellipsis after them when
	// cut is set.
	rows := func(shown int, cut bool) string {
		row := "   1 | port: " + value[:shown]
		if cut {
			row += "..."
		}

		return stringtest.JoinLF(row, "     |       "+strings.Repeat("^", shown))
	}

	tcs := map[string]struct {
		excerpt string
		opts    []niceyaml.ExcerptOption
	}{
		"no option": {
			excerpt: rows(niceyaml.DefaultExcerptWidth-6, true),
		},
		"width": {
			opts:    []niceyaml.ExcerptOption{niceyaml.WithExcerptWidth(20)},
			excerpt: rows(14, true),
		},
		"width of the line shows it whole": {
			opts:    []niceyaml.ExcerptOption{niceyaml.WithExcerptWidth(206)},
			excerpt: rows(200, false),
		},
		"zero shows whole lines": {
			opts:    []niceyaml.ExcerptOption{niceyaml.WithExcerptWidth(0)},
			excerpt: rows(200, false),
		},
		"negative counts as zero": {
			opts:    []niceyaml.ExcerptOption{niceyaml.WithExcerptWidth(-1)},
			excerpt: rows(200, false),
		},
		"the last option wins": {
			opts:    []niceyaml.ExcerptOption{niceyaml.WithExcerptWidth(0), niceyaml.WithExcerptWidth(20)},
			excerpt: rows(14, true),
		},
		"a nil option changes nothing": {
			opts:    []niceyaml.ExcerptOption{nil, niceyaml.WithExcerptWidth(20), nil},
			excerpt: rows(14, true),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			source := niceyaml.NewSourceFromString(input)
			err := yamltest.Bind(t, source, niceyaml.NewError("bad port", niceyaml.AtPath(paths.Doc().Child("port"))))

			bound := yamltest.RequireSourceError(t, err)

			opts := append([]niceyaml.ExcerptOption{niceyaml.WithContextLines(0)}, tc.opts...)

			excerpt := requireExcerpt(t, bound, opts...)
			assert.Equal(t, tc.excerpt, excerpt.String())

			// The excerpt keeps every column, so a caller shows it whole.
			assert.Equal(t, rows(200, false), excerpt.Clip(0).String())

			// Every renderer takes the same options.
			assert.Equal(
				t,
				stringtest.LinesLF("1:7: $.port: bad port", "")+tc.excerpt,
				niceyaml.FormatError(err, opts...),
			)

			rep := niceyaml.NewErrorReport(err, opts...)
			require.Len(t, rep.Excerpts, 1)
			assert.Equal(t, tc.excerpt, rep.Excerpts[0].View.String())
		})
	}
}

func TestFormatError_ExcerptWidth(t *testing.T) {
	t.Parallel()

	// One line holds every value, as a minified document does, and the
	// lines beside the short one run long.
	const (
		flow = `{name: shop, tags: [aa, bb, cc, dd, ee, ff, gg, hh, ii, jj], port: 0}`
		note = "this note runs well past the width of the excerpt"
	)

	block := stringtest.LinesLF(
		"head: "+note,
		"port: 0",
		"tail: "+note,
	)

	narrow := niceyaml.WithExcerptWidth(20)
	port := paths.Doc().Child("port")

	badPort := niceyaml.NewError("port must be at least 1", niceyaml.AtPath(port))
	badName := niceyaml.NewError("name is taken", niceyaml.AtPath(paths.Doc().Child("name")))

	flowSrc := niceyaml.NewSourceFromString(flow, niceyaml.WithName("flow.yaml"))
	blockSrc := niceyaml.NewSourceFromString(block, niceyaml.WithName("block.yaml"))

	tcs := map[string]struct {
		err  error
		opts []niceyaml.ExcerptOption
		want []string
	}{
		"location at the end of a long line": {
			err:  yamltest.Bind(t, flowSrc, badPort),
			opts: []niceyaml.ExcerptOption{narrow},
			want: []string{
				"flow.yaml:1:68: $.port: port must be at least 1",
				"",
				"   1 | ...h, ii, jj], port: 0}",
				"     |                      ^",
			},
		},
		"two locations far apart on one line": {
			err:  yamltest.Bind(t, flowSrc, niceyaml.NewSummary("2 violations", badPort, badName)),
			opts: []niceyaml.ExcerptOption{narrow},
			want: []string{
				"flow.yaml: 2 violations",
				"|-- 1:8: $.name: name is taken",
				"`-- 1:68: $.port: port must be at least 1",
				"",
				"   1 | {name: shop, tags: [...h, ii, jj], port: 0}",
				"     |        ^^^^ name is taken",
				"     |                                          ^ port must be at least 1",
			},
		},
		"context lines keep their first columns": {
			err:  yamltest.Bind(t, blockSrc, badPort),
			opts: []niceyaml.ExcerptOption{narrow},
			want: []string{
				"block.yaml:2:7: $.port: port must be at least 1",
				"",
				"   1 | head: this note runs...",
				"   2 | port: 0",
				"     |       ^",
				"   3 | tail: this note runs...",
			},
		},
		"whole lines": {
			err:  yamltest.Bind(t, blockSrc, badPort),
			opts: []niceyaml.ExcerptOption{niceyaml.WithExcerptWidth(0)},
			want: []string{
				"block.yaml:2:7: $.port: port must be at least 1",
				"",
				"   1 | head: " + note,
				"   2 | port: 0",
				"     |       ^",
				"   3 | tail: " + note,
			},
		},
		"one width clips every source": {
			err:  errors.Join(yamltest.Bind(t, flowSrc, badPort), yamltest.Bind(t, blockSrc, badPort)),
			opts: []niceyaml.ExcerptOption{narrow},
			want: []string{
				"|-- flow.yaml:1:68: $.port: port must be at least 1",
				"`-- block.yaml:2:7: $.port: port must be at least 1",
				"",
				"flow.yaml",
				"   1 | ...h, ii, jj], port: 0}",
				"     |                      ^ port must be at least 1",
				"",
				"block.yaml",
				"   1 | head: this note runs...",
				"   2 | port: 0",
				"     |       ^ port must be at least 1",
				"   3 | tail: this note runs...",
			},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			opts := append([]niceyaml.ExcerptOption{niceyaml.WithContextLines(1)}, tc.opts...)

			assert.Equal(t, stringtest.JoinLF(tc.want...), niceyaml.FormatError(tc.err, opts...))
		})
	}

	t.Run("the %+v verb prints at the default width", func(t *testing.T) {
		t.Parallel()

		// The line is 69 columns, which is under the default width, so
		// the verb shows it whole.
		assert.Equal(t, stringtest.JoinLF(
			"flow.yaml:1:68: $.port: port must be at least 1",
			"",
			"   1 | "+flow,
			"     | "+strings.Repeat(" ", 67)+"^",
		), fmt.Sprintf("%+v", yamltest.Bind(t, flowSrc, badPort)))

		// A line past the default width clips there.
		long := strings.Repeat("0123456789", 20)
		wide := niceyaml.NewSourceFromString("port: "+long+"\n", niceyaml.WithName("wide.yaml"))

		assert.Equal(t, stringtest.JoinLF(
			"wide.yaml:1:7: $.port: port must be at least 1",
			"",
			"   1 | port: "+long[:niceyaml.DefaultExcerptWidth-6]+"...",
			"     |       "+strings.Repeat("^", niceyaml.DefaultExcerptWidth-6),
		), fmt.Sprintf("%+v", yamltest.Bind(t, wide, badPort)))
	})
}

func TestFormatError_ExcerptsOff(t *testing.T) {
	t.Parallel()

	// Every line but the one that holds the port holds a secret, so any
	// excerpt of the source shows one.
	text := stringtest.LinesLF(
		"db:",
		"  password: hunter2",
		"  port: 0",
		"  token: hunter2-token",
	)

	secrets := niceyaml.NewSourceFromString(text, niceyaml.WithName("secrets.yaml"), niceyaml.WithExcerpts(false))
	open := niceyaml.NewSourceFromString("host: example.com\nport: 0\n", niceyaml.WithName("open.yaml"))

	db := paths.Doc().Child("db")
	message := "port must be at least 1"

	badPort := yamltest.Bind(t, secrets, niceyaml.NewError(message, niceyaml.AtPath(db.Child("port"))))
	openPort := yamltest.Bind(t, open, niceyaml.NewError(message, niceyaml.AtPath(paths.Doc().Child("port"))))

	openExcerpt := stringtest.JoinLF(
		"open.yaml",
		"   1 | host: example.com",
		"   2 | port: 0",
		"     |       ^ port must be at least 1",
	)

	tcs := map[string]struct {
		err  error
		want string
	}{
		"a path keeps its position and its message": {
			err:  badPort,
			want: "secrets.yaml:3:9: $.db.port: port must be at least 1",
		},
		"a position on a secret": {
			err:  yamltest.Bind(t, secrets, niceyaml.NewError("too short", niceyaml.AtPosition(position.New(1, 12)))),
			want: "secrets.yaml:2:13: too short",
		},
		"a range across every line": {
			err: yamltest.Bind(t, secrets, niceyaml.NewError("too long", niceyaml.AtRange(
				position.NewRange(position.New(0, 0), position.New(3, 21)),
			))),
			want: "secrets.yaml:1:1: too long",
		},
		"a key the mapping lacks": {
			err:  yamltest.Bind(t, secrets, niceyaml.NewError("name is required", niceyaml.AtPath(db.Child("name")))),
			want: "secrets.yaml:1:1: $.db.name: name is required",
		},
		"a wrapper keeps its context": {
			err:  fmt.Errorf("load config: %w", badPort),
			want: "load config: secrets.yaml:3:9: $.db.port: port must be at least 1",
		},
		"a summary keeps every branch": {
			err: yamltest.Bind(t, secrets, niceyaml.NewSummary("2 problems",
				niceyaml.NewError(message, niceyaml.AtPath(db.Child("port"))),
				niceyaml.NewError("too short", niceyaml.AtPath(db.Child("password"))),
			)),
			want: stringtest.JoinLF(
				"secrets.yaml: 2 problems",
				"|-- 2:13: $.db.password: too short",
				"`-- 3:9: $.db.port: port must be at least 1",
			),
		},
		"several bindings add no excerpt": {
			err: errors.Join(badPort, yamltest.Bind(t, secrets,
				niceyaml.NewError("too short", niceyaml.AtPath(db.Child("password"))))),
			want: stringtest.JoinLF(
				"|-- secrets.yaml:3:9: $.db.port: port must be at least 1",
				"`-- secrets.yaml:2:13: $.db.password: too short",
			),
		},
		// The reason names the path, which the tree names already, and no
		// text of the source.
		"a location that does not resolve keeps its reason": {
			err: yamltest.Bind(t, secrets, niceyaml.NewError("not a list", niceyaml.AtPath(db.Child("port").Index(3)))),
			want: stringtest.JoinLF(
				"secrets.yaml: $.db.port[3]: not a list",
				"",
				"no excerpt: resolve $.db.port[3]: not found",
			),
		},
		"a binding in another source keeps its excerpt": {
			err: errors.Join(openPort, badPort),
			want: stringtest.JoinLF(
				"|-- open.yaml:2:7: $.port: port must be at least 1",
				"`-- secrets.yaml:3:9: $.db.port: port must be at least 1",
				"",
				openExcerpt,
			),
		},
		"a detail in another source keeps its excerpt": {
			err: yamltest.Bind(t, secrets, niceyaml.NewError(
				"ports differ",
				niceyaml.AtPath(db.Child("port")),
				niceyaml.WithDetails(openPort),
			)),
			want: stringtest.JoinLF(
				"secrets.yaml:3:9: $.db.port: ports differ",
				"`-- open.yaml:2:7: $.port: port must be at least 1",
				"",
				openExcerpt,
			),
		},
		"a detail in the source adds no excerpt to its parent": {
			err: yamltest.Bind(t, open, niceyaml.NewError(
				"ports differ",
				niceyaml.AtPath(paths.Doc().Child("port")),
				niceyaml.WithDetails(badPort),
			)),
			want: stringtest.JoinLF(
				"open.yaml:2:7: $.port: ports differ",
				"`-- secrets.yaml:3:9: $.db.port: port must be at least 1",
				"",
				"open.yaml",
				"   1 | host: example.com",
				"   2 | port: 0",
				"     |       ^",
			),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, niceyaml.FormatError(tc.err))

			for renderer, got := range renderings(tc.err) {
				assert.NotContains(t, got, excerptSecret, renderer)
			}
		})
	}

	t.Run("the same error shows the secret with excerpts on", func(t *testing.T) {
		t.Parallel()

		shown := niceyaml.NewSourceFromString(text, niceyaml.WithName("secrets.yaml"))
		err := yamltest.Bind(t, shown, niceyaml.NewError(message, niceyaml.AtPath(db.Child("port"))))

		assert.Equal(t, stringtest.JoinLF(
			"secrets.yaml:3:9: $.db.port: port must be at least 1",
			"",
			"   1 | db:",
			"   2 |   password: hunter2",
			"   3 |   port: 0",
			"     |         ^",
			"   4 |   token: hunter2-token",
		), niceyaml.FormatError(err))

		// The renderers that print an excerpt are the ones the option
		// changes.
		got := renderings(err)

		for _, renderer := range []string{"%+v", "FormatError", "PrintError", "Excerpts"} {
			assert.Contains(t, got[renderer], excerptSecret, renderer)
		}
	})
}

func TestFormatError_ExcerptsOff_Documents(t *testing.T) {
	t.Parallel()

	// The error of the document that did not parse, among those
	// Documents returns.
	documents := func(t *testing.T, source *niceyaml.Source) error {
		t.Helper()

		for _, doc := range source.Documents() {
			err := doc.Err()
			if err != nil {
				return err //nolint:wrapcheck // The test reads the error as the document returns it.
			}
		}

		return nil
	}

	file := func(_ *testing.T, source *niceyaml.Source) error {
		_, err := source.File()

		return err
	}

	decode := func(t *testing.T, source *niceyaml.Source) error {
		t.Helper()

		_, err := source.Decode[checkedSecretConfig](
			t.Context(),
			niceyaml.WithUnknownFields(niceyaml.UnknownFieldsReject),
		)

		return err
	}

	requiresName := schema.MustCompile([]byte(`{"required": ["name"]}`))

	// A document whose flow sequence stays open, below the line of the
	// secret.
	openList := stringtest.LinesLF(
		"password: hunter2",
		"list: [1, 2",
		"port: 1",
	)

	// The errors the module finds in a source name no line of it, whether
	// the document parsed or not.
	tcs := map[string]struct {
		fail  func(t *testing.T, source *niceyaml.Source) error
		input string
		want  string
	}{
		"a flow sequence left open": {
			input: openList,
			fail:  documents,
			want:  "secrets.yaml:3:1: ',' or ']' must be specified",
		},
		"a flow sequence left open on the secret": {
			input: stringtest.LinesLF("password: [hunter2, 2", "port: 1"),
			fail:  file,
			want:  "secrets.yaml:2:1: ',' or ']' must be specified",
		},
		// The parser reports the tab at the value before it, which is the
		// secret.
		"a tab in the indentation": {
			input: stringtest.LinesLF("password: hunter2", "\tport: 1"),
			fail:  documents,
			want:  "secrets.yaml:1:11: found character '␉' that cannot start any token",
		},
		"a later document that did not parse": {
			input: stringtest.LinesLF("port: 1", "---", "password: hunter2", "list: [1"),
			fail:  documents,
			want:  "secrets.yaml:4:7: sequence end token ']' not found",
		},
		"a decode of a document that did not parse": {
			input: openList,
			fail:  decode,
			want:  "secrets.yaml:3:1: ',' or ']' must be specified",
		},
		// The reason is the syntax error, which names its position.
		"a path bound in a document that did not parse": {
			input: openList,
			fail: func(_ *testing.T, source *niceyaml.Source) error {
				return source.Bind(niceyaml.NewError("too short", niceyaml.AtPath(paths.Doc().Child("password"))))
			},
			want: stringtest.JoinLF(
				"secrets.yaml: $.password: too short",
				"",
				"no excerpt: path needs a document to resolve in: $.password: "+
					"secrets.yaml:3:1: ',' or ']' must be specified",
			),
		},
		"a value of the wrong type": {
			input: stringtest.LinesLF("password: hunter2", "port: eighty", "token: hunter2-token"),
			fail:  decode,
			want:  "secrets.yaml:2:7: $.port: expected integer, got string",
		},
		"an unknown field": {
			input: stringtest.LinesLF("password: hunter2", "extra: 1", "token: hunter2-token"),
			fail:  decode,
			want:  `secrets.yaml:2:1: $.extra~: unknown field "extra"`,
		},
		"a value that fails its own validation": {
			input: stringtest.LinesLF("password: hunter2", "port: 0", "token: hunter2-token"),
			fail:  decode,
			want:  "secrets.yaml:2:7: $.port: port must be at least 1",
		},
		"a schema violation": {
			input: stringtest.LinesLF("password: hunter2", "port: 1"),
			fail: func(t *testing.T, source *niceyaml.Source) error {
				t.Helper()

				return source.ValidateDocuments(t.Context(), requiresName)
			},
			want: `secrets.yaml:1:1: $.name: missing required property "name"`,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			source := niceyaml.NewSourceFromString(tc.input,
				niceyaml.WithName("secrets.yaml"), niceyaml.WithExcerpts(false))

			err := tc.fail(t, source)
			require.Error(t, err)

			assert.Equal(t, tc.want, niceyaml.FormatError(err))

			for renderer, got := range renderings(err) {
				assert.NotContains(t, got, excerptSecret, renderer)
			}
		})
	}
}

func TestFormatError_ExcerptsOff_EchoedText(t *testing.T) {
	t.Parallel()

	// A message that quotes what it rejects prints it, and a key prints
	// in the path of an error. [niceyaml.WithExcerpts] names each of
	// these, and the output holds the message alone, with no line of the
	// source after it.
	tcs := map[string]struct {
		input string
		want  string
	}{
		"a duration quotes its value": {
			input: "timeout: hunter2\n",
			want:  `secrets.yaml:1:10: $.timeout: time: invalid duration "hunter2"`,
		},
		"an IP address quotes its value": {
			input: "address: hunter2\n",
			want:  "secrets.yaml:1:10: $.address: invalid IP address: hunter2",
		},
		"a number out of range names itself": {
			input: "retries: 300\n",
			want:  "secrets.yaml:1:10: $.retries: expected integer from 0 to 255, got 300",
		},
		"an unknown field names its key": {
			input: "hunter2: 1\n",
			want:  `secrets.yaml:1:1: $.hunter2~: unknown field "hunter2"`,
		},
		"a path names each key on the way": {
			input: stringtest.LinesLF("limits:", "  hunter2: many"),
			want:  "secrets.yaml:2:12: $.limits.hunter2: expected integer, got string",
		},
		"a missing anchor names its alias": {
			input: "port: *hunter2\n",
			want:  `secrets.yaml:1:7: $.port: could not find alias "hunter2"`,
		},
		"a duplicate key names itself": {
			input: stringtest.LinesLF("hunter2: 1", "hunter2: 2"),
			want:  `secrets.yaml:2:1: mapping key "hunter2" already defined at [1:1]`,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			source := niceyaml.NewSourceFromString(tc.input,
				niceyaml.WithName("secrets.yaml"), niceyaml.WithExcerpts(false))

			_, err := source.Decode[secretConfig](t.Context(), niceyaml.WithUnknownFields(niceyaml.UnknownFieldsReject))
			require.Error(t, err)

			assert.Equal(t, tc.want, niceyaml.FormatError(err))
			assert.Equal(t, tc.want, fmt.Sprintf("%+v", err))
			assert.Empty(t, niceyaml.NewErrorReport(err, niceyaml.WithContextLines(0)).Excerpts)
		})
	}
}

func TestSourceError_Format_ExcerptsOff(t *testing.T) {
	t.Parallel()

	secrets := niceyaml.NewSourceFromString("password: hunter2\nport: 0\n",
		niceyaml.WithName("secrets.yaml"), niceyaml.WithExcerpts(false))
	open := niceyaml.NewSourceFromString("port: 0\n", niceyaml.WithName("open.yaml"))

	port := paths.Doc().Child("port")
	openPort := yamltest.Bind(t, open, niceyaml.NewError("port is 0", niceyaml.AtPath(port)))

	tcs := map[string]struct {
		err  error
		want string
	}{
		"plus v prints the message alone": {
			err:  niceyaml.NewError("port must be at least 1", niceyaml.AtPath(port)),
			want: "secrets.yaml:2:7: $.port: port must be at least 1",
		},
		"plus v prints the tree of a summary": {
			err: niceyaml.NewSummary("2 problems",
				niceyaml.NewError("port must be at least 1", niceyaml.AtPath(port)),
				niceyaml.NewError("too short", niceyaml.AtPath(paths.Doc().Child("password"))),
			),
			want: stringtest.JoinLF(
				"secrets.yaml: 2 problems",
				"|-- 1:11: $.password: too short",
				"`-- 2:7: $.port: port must be at least 1",
			),
		},
		"plus v keeps the excerpt of another source": {
			err: niceyaml.NewError("ports differ", niceyaml.AtPath(port), niceyaml.WithDetails(openPort)),
			want: stringtest.JoinLF(
				"secrets.yaml:2:7: $.port: ports differ",
				"`-- open.yaml:1:7: $.port: port is 0",
				"",
				"open.yaml",
				"   1 | port: 0",
				"     |       ^ port is 0",
			),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := fmt.Sprintf("%+v", yamltest.Bind(t, secrets, tc.err))

			assert.Equal(t, tc.want, got)
			assert.NotContains(t, got, excerptSecret)
		})
	}
}

func TestError_Accessors(t *testing.T) {
	t.Parallel()

	cause := errors.New("boom")
	nested := yamltest.ErrorAt("nested", "a")

	var nilNested *niceyaml.Error

	err := wrapError(t, cause, niceyaml.WithDetails(nil, nested, nilNested))

	assert.Equal(t, []error{nested}, yamltest.ErrsOf(yamltest.DetailsOf(err)))
	assert.Empty(t, yamltest.ProblemsOf(err))
	assert.Equal(t, []error{cause}, err.Unwrap())
	// The details leave the cause in the chain that errors.Is walks.
	require.ErrorIs(t, err, cause)

	other := niceyaml.NewError("other")

	summary := yamltest.RequireError(t, niceyaml.NewSummary("2 problems", nil, nested, nilNested, other))

	assert.Equal(t, []error{nested, other}, yamltest.ErrsOf(yamltest.ProblemsOf(summary)))
	assert.Empty(t, yamltest.DetailsOf(summary))

	unwrapped := summary.Unwrap()
	require.Len(t, unwrapped, 3)
	require.EqualError(t, unwrapped[0], "2 problems")
	assert.Equal(t, []error{nested, other}, unwrapped[1:])

	assert.Empty(t, yamltest.ProblemsOf(niceyaml.NewError("plain")))
	assert.Empty(t, yamltest.DetailsOf(niceyaml.NewError("plain")))

	// An aggregate of k8s.io/apimachinery and the error field of zap read
	// a method named Errors as the whole list of an error, so they would
	// read an Error that heads nothing as an empty list.
	_, lists := any(other).(interface{ Errors() []error })
	assert.False(t, lists)

	// Github.com/pkg/errors and github.com/cockroachdb/errors read a
	// method named Cause as the one error below an error. They would
	// follow it past the errors a summary heads.
	_, causes := any(other).(interface{ Cause() error })
	assert.False(t, causes)
}

func TestError_Accessors_Wrapped(t *testing.T) {
	t.Parallel()

	server := paths.Current().Child("server")
	detail := yamltest.ErrorAt("must be below 65536", "max")
	port := niceyaml.NewError("port out of range",
		niceyaml.AtPath(paths.Current().Child("port")), niceyaml.WithDetails(detail))
	tooLow := yamltest.ErrorAt("too low", "a")
	tooHigh := yamltest.ErrorAt("too high", "b")
	summary := niceyaml.NewSummary("2 violations", tooLow, tooHigh)
	errCheck := errors.New("check")

	// The path of each error, as [niceyaml.Error.Path] reports it through
	// the Rebase around it.
	pathsOf := func(t *testing.T, errs []error) []string {
		t.Helper()

		var got []string

		for _, err := range errs {
			got = append(got, yamltest.PathOf(t, yamltest.RequireError(t, err)).String())
		}

		return got
	}

	tcs := map[string]struct {
		err          error
		wantProblems []string
		wantDetails  []string
	}{
		"Rebase of a summary heads its problems under the base": {
			err:          niceyaml.Rebase(summary, server),
			wantProblems: []string{"@.server.a", "@.server.b"},
		},
		"Rebase of a problem has its details under the base": {
			err:         niceyaml.Rebase(port, server),
			wantDetails: []string{"@.server.max"},
		},
		"two Rebases join both bases": {
			err:         niceyaml.Rebase(niceyaml.Rebase(port, server), paths.Current().Child("spec")),
			wantDetails: []string{"@.spec.server.max"},
		},
		"a wrapper between the Rebase and the problem": {
			err:         niceyaml.Rebase(fmt.Errorf("check: %w", port), server),
			wantDetails: []string{"@.server.max"},
		},
		"Invalid with a path of its own leaves the details as they are": {
			err:         niceyaml.Invalid(port, niceyaml.AtPath(paths.Current().Child("cfg"))),
			wantDetails: []string{"@.max"},
		},
		"Invalid with no options reads through to the details": {
			err:         niceyaml.Invalid(port),
			wantDetails: []string{"@.max"},
		},
		"the details of the outer Error come first": {
			err: niceyaml.Invalid(niceyaml.Rebase(port, server),
				niceyaml.WithDetails(yamltest.ErrorAt("see docs", "docs"))),
			wantDetails: []string{"@.docs", "@.server.max"},
		},
		"a wrapper with several %w verbs keeps the summary as its one branch": {
			err:          niceyaml.Invalid(fmt.Errorf("%w: %w", errCheck, summary)),
			wantProblems: []string{"@.a", "@.b"},
		},
		"a wrapper with several %w verbs keeps the problem as its one branch": {
			err:         niceyaml.Invalid(fmt.Errorf("%w: %w", errCheck, port)),
			wantDetails: []string{"@.max"},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := yamltest.RequireError(t, tc.err)

			assert.Equal(t, tc.wantProblems, pathsOf(t, yamltest.ErrsOf(yamltest.ProblemsOf(got))))
			assert.Equal(t, tc.wantDetails, pathsOf(t, yamltest.ErrsOf(yamltest.DetailsOf(got))))
		})
	}

	t.Run("a rebased problem still matches the error it came from", func(t *testing.T) {
		t.Parallel()

		got := yamltest.RequireError(t, niceyaml.Rebase(summary, server))

		problems := yamltest.ErrsOf(yamltest.ProblemsOf(got))
		require.Len(t, problems, 2)
		require.ErrorIs(t, problems[0], tooLow)
		require.ErrorIs(t, problems[1], tooHigh)

		details := yamltest.RequireError(t, niceyaml.Rebase(port, server))
		require.Len(t, yamltest.DetailsOf(details), 1)
		require.ErrorIs(t, yamltest.DetailsOf(details)[0].Err, detail)
	})
}

// ruleError is a cause of a type the caller defines.
type ruleError struct {
	id string
}

func (e *ruleError) Error() string {
	return "rule " + e.id
}

func TestSourceError_As(t *testing.T) {
	t.Parallel()

	source := niceyaml.NewSourceFromString(stringtest.LinesLF("x:", "  a: 1", "  b: 2"))
	x := paths.Current().Child("x")

	rule := &ruleError{id: "a"}
	located := niceyaml.Invalid(rule, niceyaml.AtPath(paths.Current().Child("a")))

	t.Run("a summary", func(t *testing.T) {
		t.Parallel()

		// A summary heads one error per violation. [errors.As] on its
		// binding descends into them and finds the rule of the first, and
		// on the error of each problem the binding heads it finds the rule
		// of that one.
		ruleB := &ruleError{id: "b"}
		summary := niceyaml.NewSummary("2 violations",
			located,
			niceyaml.Invalid(ruleB, niceyaml.AtPath(paths.Current().Child("b"))),
		)

		bound := requireBound(t, source, niceyaml.Rebase(summary, x))

		found, ok := errors.AsType[*ruleError](bound)
		require.True(t, ok)
		assert.Same(t, rule, found)

		children := yamltest.ProblemsOf(bound)
		require.Len(t, children, 2)

		first, ok := errors.AsType[*ruleError](children[0].Err)
		require.True(t, ok)
		assert.Same(t, rule, first)

		second, ok := errors.AsType[*ruleError](children[1].Err)
		require.True(t, ok)
		assert.Same(t, ruleB, second)
	})

	t.Run("no Cause method", func(t *testing.T) {
		t.Parallel()

		// Github.com/cockroachdb/errors unwraps through a method named
		// Cause before it tries Unwrap. Its Is and As would step from a
		// binding to the error it reports, past the Error that holds the
		// errors a summary heads.
		bound := requireBound(t, source, yamltest.ErrorAt("bad", "x"))

		_, causes := any(bound).(interface{ Cause() error })
		assert.False(t, causes)
	})
}

func TestSourceError_Errors(t *testing.T) {
	t.Parallel()

	source := niceyaml.NewSourceFromString("a: 1\nb: 2\n")
	badA := yamltest.ErrorAt("bad a", "a")
	badX := niceyaml.NewError("bad x", niceyaml.AtPath(paths.Current().Child("x").Index(0)))
	deep := yamltest.ErrorAt("deep", "b")
	mid := niceyaml.NewError("mid", niceyaml.WithDetails(deep))

	bound := requireBound(t, source, niceyaml.NewSummary("main", badA, badX, nil, mid))

	// One child per nested error, each bound to the same source and
	// unwrapping to the error it binds. The one with a position comes
	// first, and the rest follow in the order given.
	children := yamltest.ProblemsOf(bound)
	require.Len(t, children, 3)

	for i, want := range []error{badA, badX, mid} {
		assert.Same(t, source, children[i].Source())
		require.ErrorIs(t, children[i].Err, want)
	}

	assert.Equal(t, position.New(0, 3), yamltest.RangeOf(t, children[0]).Start)
	assert.Equal(t, "1:4: $.a: bad a", children[0].Bound.Error())

	// One that did not resolve reports why, and one that carries no
	// location reports that.
	requireUnresolved(t, children[1].Bound, paths.ErrNotFound)
	requireUnresolved(t, children[2].Bound, nil)

	// The detail of a problem the summary heads binds below that problem,
	// which heads nothing, and the summary holds no details.
	assert.Empty(t, yamltest.DetailsOf(bound))
	assert.Empty(t, yamltest.ProblemsOf(children[2].Bound))

	grandchildren := yamltest.DetailsOf(children[2].Bound)
	require.Len(t, grandchildren, 1)

	assert.Equal(t, position.New(1, 3), yamltest.RangeOf(t, grandchildren[0]).Start)

	// Every branch of a join is a child, and a branch that carries no
	// location binds without one.
	joined := requireBound(t, source, errors.Join(badA, errors.New("plain")))

	branches := yamltest.ProblemsOf(joined)
	require.Len(t, branches, 2)

	requireUnresolved(t, branches[1].Bound, nil)
}

func TestSourceError_Errors_AboveBinding(t *testing.T) {
	t.Parallel()

	dd := yamltest.FirstDocument(t, stringtest.LinesLF("name: x", "hours:", "  open: 1"))
	bound := dd.Bind(yamltest.ErrorAt("root", "name"))
	kid := yamltest.ErrorAt("kid", "hours", "open")

	tcs := map[string]struct {
		err  error
		want []string
	}{
		"details above a binding bind as children": {
			err:  niceyaml.Invalid(bound, niceyaml.WithDetails(kid)),
			want: []string{"1:7: $.name: root", "3:9: $.hours.open: kid"},
		},
		"a binding with nothing above it comes back as it is": {
			err:  niceyaml.Invalid(bound),
			want: []string{"1:7: $.name: root"},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, bindingTexts(dd.Bind(tc.err)))
		})
	}
}

func TestSourceError_Node(t *testing.T) {
	t.Parallel()

	source := niceyaml.NewSourceFromString(stringtest.LinesLF("a:", "  b: 1"))
	doc := yamltest.Document(t, source)
	scoped := yamltest.At(t, doc, paths.Doc().Child("a"))

	several := niceyaml.NewSourceFromString(stringtest.LinesLF("a: 1", "---", "b: 2"))
	bad := yamltest.ErrorAt("bad", "b")

	tcs := map[string]struct {
		err error
		// The Node of the binding and the root of its document, or nil for
		// a binding to none.
		wantNode     *niceyaml.Node
		wantDocument *niceyaml.Node
	}{
		"bound by a scoped node": {
			err:          scoped.Bind(bad),
			wantNode:     scoped,
			wantDocument: doc,
		},
		"bound by the source at no location": {
			err: source.Bind(errors.New("quota service down")),
		},
		"bound by the source at a path among several documents": {
			err: several.Bind(bad),
		},
		"bound to a value that came from no document": {
			err: niceyaml.BindValue(bad),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			bound := yamltest.RequireSourceError(t, tc.err)

			node, ok := bound.Node()
			assert.Equal(t, tc.wantNode != nil, ok)
			assert.Same(t, tc.wantNode, node)

			root, ok := bound.Document()
			assert.Equal(t, tc.wantDocument != nil, ok)
			assert.Same(t, tc.wantDocument, root)
		})
	}
}

func TestSourceError_Name(t *testing.T) {
	t.Parallel()

	named := niceyaml.NewSourceFromString(
		"a: 1\n",
		niceyaml.WithName("cafe.yaml"),
		niceyaml.WithFilePath("/srv/cafe.yaml"),
	)
	pathOnly := niceyaml.NewSourceFromString("a: 1\n", niceyaml.WithFilePath("/srv/menu.yaml"))
	bare := niceyaml.NewSourceFromString("a: 1\n")
	bad := yamltest.ErrorAt("bad", "a")

	tcs := map[string]struct {
		err          error
		wantName     string
		wantFilePath string
	}{
		"source with a name and a file path": {
			err:          yamltest.Bind(t, named, bad),
			wantName:     "cafe.yaml",
			wantFilePath: "/srv/cafe.yaml",
		},
		"source with a file path alone": {
			err:          yamltest.Bind(t, pathOnly, bad),
			wantName:     "/srv/menu.yaml",
			wantFilePath: "/srv/menu.yaml",
		},
		"source with neither": {
			err: yamltest.Bind(t, bare, bad),
		},
		"value that came from no document": {
			err: niceyaml.BindValue(bad),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			bound := yamltest.RequireSourceError(t, tc.err)

			assert.Equal(t, tc.wantName, bound.Name())
			assert.Equal(t, bound.Source().Name(), bound.Name())
			assert.Equal(t, tc.wantFilePath, bound.FilePath())
			assert.Equal(t, bound.Source().FilePath(), bound.FilePath())
		})
	}

	t.Run("nil SourceError", func(t *testing.T) {
		t.Parallel()

		var bound *niceyaml.SourceError

		assert.Empty(t, bound.Name())
		assert.Empty(t, bound.FilePath())
	})
}

func TestSourceError_Document(t *testing.T) {
	t.Parallel()

	source := niceyaml.NewSourceFromString(stringtest.LinesLF("a: 1", "---", "b: 2"))
	docs := yamltest.Documents(t, source, 2)

	// The second document of three does not parse.
	broken := niceyaml.NewSourceFromString(stringtest.LinesLF(
		"a: 1",
		"---",
		"b: [",
		"---",
		"c: 3",
	))
	brokenDocs := yamltest.AllDocuments(t, broken, 3)

	// The header follows an anchor with no value, so both documents parse
	// together and share the syntax error of the second.
	together := yamltest.AllDocuments(t, niceyaml.NewSourceFromString(stringtest.LinesLF("a: &x", "---", "b: [1")), 2)

	tcs := map[string]struct {
		err error
		// The document of the binding, or nil for a binding to none.
		want *niceyaml.Node
		// The index DocumentIndex reports, which is that of the document.
		index int
	}{
		"bound by the first document": {
			err:  docs[0].Bind(niceyaml.NewError("bad")),
			want: docs[0],
		},
		"bound by the second document": {
			err:   docs[1].Bind(yamltest.ErrorAt("bad", "b")),
			want:  docs[1],
			index: 1,
		},
		"bound by a scoped node of the second document": {
			err:   yamltest.At(t, docs[1], paths.Current().Child("b")).Bind(niceyaml.NewError("bad")),
			want:  docs[1],
			index: 1,
		},
		"produced by a document": {
			err:  errOf(docs[0].Ranges(paths.Current().Child("missing"))),
			want: docs[0],
		},
		"bound by the source at a position in the first document": {
			err:  source.Bind(niceyaml.NewError("bad", niceyaml.AtPosition(position.New(0, 0)))),
			want: docs[0],
		},
		"bound by the source at a position in the second document": {
			err:   source.Bind(niceyaml.NewError("bad", niceyaml.AtPosition(position.New(2, 0)))),
			want:  docs[1],
			index: 1,
		},
		"bound by the source at no location": {
			err: source.Bind(niceyaml.NewError("bad")),
		},
		"bound by the source on a line no document holds": {
			err: source.Bind(niceyaml.NewError("bad", niceyaml.AtPosition(position.New(9, 0)))),
		},
		"bound by the source at a path among several documents": {
			err: source.Bind(yamltest.ErrorAt("bad", "b")),
		},
		"syntax error of the second document of three": {
			err:   errOf(broken.File()),
			want:  brokenDocs[1],
			index: 1,
		},
		"syntax error from the document that holds it": {
			err:   brokenDocs[1].Validate(t.Context(), nil),
			want:  brokenDocs[1],
			index: 1,
		},
		"syntax error from the document that parsed together with its own": {
			err:   together[0].Validate(t.Context(), nil),
			want:  together[1],
			index: 1,
		},
		"bound by the source at a position beside a syntax error": {
			err:   broken.Bind(niceyaml.NewError("bad", niceyaml.AtPosition(position.New(4, 0)))),
			want:  brokenDocs[2],
			index: 2,
		},
		"bound by the source at a path beside a syntax error": {
			err: broken.Bind(yamltest.ErrorAt("bad", "c")),
		},
		"wrapping a binding keeps its document": {
			err:  docs[1].Bind(fmt.Errorf("context: %w", docs[0].Bind(niceyaml.NewError("bad")))),
			want: docs[0],
		},
		"nesting errors around a binding keeps its document": {
			err: docs[1].Bind(niceyaml.Invalid(
				docs[0].Bind(niceyaml.NewError("bad")),
				niceyaml.WithDetails(yamltest.ErrorAt("b", "b")),
			)),
			want: docs[0],
		},
		"nesting errors around a binding to no document keeps none": {
			err: docs[1].Bind(niceyaml.Invalid(
				source.Bind(niceyaml.NewError("bad")),
				niceyaml.WithDetails(yamltest.ErrorAt("b", "b")),
			)),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			bound := yamltest.RequireSourceError(t, tc.err)

			doc, ok := bound.Document()
			assert.Equal(t, tc.want != nil, ok)
			assert.Same(t, tc.want, doc)

			index, ok := bound.DocumentIndex()
			assert.Equal(t, tc.want != nil, ok)
			assert.Equal(t, tc.index, index)
		})
	}

	t.Run("a report reads the document of every problem from its binding", func(t *testing.T) {
		t.Parallel()

		// A syntax error, a violation in a document that parsed, and an
		// error bound to no source, which has no binding to read.
		err := errors.Join(
			broken.ValidateDocuments(t.Context(), nil),
			brokenDocs[2].Bind(yamltest.ErrorAt("bad c", "c")),
			errors.New("schema server unreachable"),
		)

		var got []string

		for problem := range niceyaml.NewErrorTree(err).Problems() {
			index, ok := problem.Bound.DocumentIndex()
			got = append(got, fmt.Sprintf("%d %t %s", index, ok, problem.Message()))
		}

		assert.Equal(t, []string{
			"1 true sequence end token ']' not found",
			"2 true bad c",
			"0 false schema server unreachable",
		}, got)
	})
}

// rebasedHours is a [niceyaml.SelfValidator] that writes an `@` path, which
// reads from the value, as a type validated on its own does.
type rebasedHours struct {
	Open  string `yaml:"open"`
	Close string `yaml:"close"`
}

func (h rebasedHours) Validate() error {
	if h.Close < h.Open {
		return yamltest.ErrorAt("closes before it opens", "close")
	}

	return nil
}

// rebasedConfig holds a value that validates itself under a field.
type rebasedConfig struct {
	Name  string       `yaml:"name"`
	Hours rebasedHours `yaml:"hours"`
}

// joinedHours is a [niceyaml.SelfValidator] that reports its violations
// as a join of its own type, with `@` paths that read from the value.
type joinedHours struct {
	Open  string `yaml:"open"`
	Close string `yaml:"close"`
}

func (h joinedHours) Validate() error {
	return sparseJoinError{errs: []error{
		yamltest.ErrorAt("bad open", "open"),
		yamltest.ErrorAt("bad close", "close"),
	}}
}

// joinedConfig holds a [joinedHours] under a field.
type joinedConfig struct {
	Name  string      `yaml:"name"`
	Hours joinedHours `yaml:"hours"`
}

var (
	// ErrInvalidHours is the sentinel [classifiedJoinError] matches.
	errInvalidHours = errors.New("invalid hours")

	// ErrRegistry is the error the code of a caller panics with.
	errRegistry = errors.New("registry not initialized")

	// ErrLicense is the I/O error that [uncheckedText] and
	// [uncheckedUnmarshaler] wrap in [niceyaml.Unchecked], as a check that
	// cannot read the file a value names does.
	errLicense = fmt.Errorf("stat license: %w", fs.ErrPermission)
)

// classifiedJoinError is a join of its own type, as [sparseJoinError] is,
// that matches [errInvalidHours] through an Is method and fills a
// [*customTestError] through an As method.
type classifiedJoinError struct {
	sparseJoinError
}

func (e classifiedJoinError) Is(target error) bool {
	return target == errInvalidHours
}

func (e classifiedJoinError) As(target any) bool {
	custom, ok := target.(**customTestError)
	if ok {
		*custom = &customTestError{msg: "invalid hours"}
	}

	return ok
}

func TestRebase(t *testing.T) {
	t.Parallel()

	input := stringtest.Input(`
		name: cafe
		hours:
		  open: "09:00"
		  close: "08:00"
	`)
	hours := paths.Current().Child("hours")
	dd := yamltest.FirstDocument(t, input)

	// A document that holds its hours one level down, under spec.
	nested := yamltest.FirstDocument(t, stringtest.LinesLF(
		"spec:",
		"  hours:",
		"    open: 1",
		"    close: 0",
	))

	t.Run("nil returns nil", func(t *testing.T) {
		t.Parallel()

		var nilErr *niceyaml.Error

		assert.NoError(t, niceyaml.Rebase(nil, hours))
		assert.NoError(t, niceyaml.Rebase(nilErr, hours))
	})

	t.Run("a join of nil pointers stays an error at the base", func(t *testing.T) {
		t.Parallel()

		var openErr, closeErr *niceyaml.Error

		joined := errors.Join(openErr, closeErr)
		require.Error(t, joined)

		err := niceyaml.Rebase(joined, hours)
		require.Error(t, err)

		assert.Equal(t, hours, yamltest.PathOf(t, yamltest.RequireError(t, err)))

		// The join heads no error, so it is one problem rather than a
		// heading, and a problem with no location takes the base, as a
		// member of a summary as well.
		report := niceyaml.NewSummary("invalid hours", joined, errors.New("bad open"))
		bound := dd.Bind(niceyaml.Rebase(report, hours))

		se := yamltest.RequireSourceError(t, bound)
		require.Len(t, yamltest.ProblemsOf(se), 2)

		child := yamltest.ProblemsOf(se)[0].Bound
		assert.NotPanics(t, func() {
			assert.Equal(t, "2:1: $.hours:", strings.TrimSpace(child.Error()))
			assert.Equal(t, "\n", child.Message())
			assert.Contains(t, niceyaml.FormatError(bound, niceyaml.WithContextLines(1)), "invalid hours\n")
		})

		var rows []string

		for problem := range niceyaml.NewErrorTree(dd.Bind(niceyaml.Rebase(joined, hours))).Problems() {
			rows = append(rows, strings.TrimSpace(problem.Label))
		}

		assert.Equal(t, []string{"2:1: $.hours:"}, rows)
	})

	inner := yamltest.ErrorAt("closes before it opens", "close")
	spec := paths.Current().Child("spec")

	// The message of a rebased error stays as its chain wrote it. The path
	// joins every base, and FormatError and a binding each name it once,
	// in front of the whole message.
	composed := map[string]struct {
		err error
		// The document that binds the error.
		doc *niceyaml.Node
		// The message, and the path and the position with the path that
		// FormatError and the binding put in front of it.
		want      string
		wantPath  paths.Path
		wantBound string
	}{
		"path composes with the base": {
			err:       niceyaml.Rebase(inner, hours),
			doc:       dd,
			want:      "closes before it opens",
			wantPath:  hours.Child("close"),
			wantBound: "4:10: $.hours.close",
		},
		"rebases compose": {
			err:       niceyaml.Rebase(niceyaml.Rebase(inner, hours), spec),
			doc:       nested,
			want:      "closes before it opens",
			wantPath:  spec.Child("hours", "close"),
			wantBound: "4:12: $.spec.hours.close",
		},
		// Each level wraps the result of the level below and rebases it, so
		// the text holds every wrapper and binding names the joined path
		// once, in front of all of them.
		"wrappers between rebases add no paths": {
			err: niceyaml.Rebase(
				fmt.Errorf("check spec: %w", niceyaml.Rebase(fmt.Errorf("check hours: %w", inner), hours)),
				spec,
			),
			doc:       nested,
			want:      "check spec: check hours: closes before it opens",
			wantPath:  spec.Child("hours", "close"),
			wantBound: "4:12: $.spec.hours.close",
		},
		// The wrapper holds the message alone, so binding names the joined
		// path once, in front of the text the wrapper added.
		"a wrapper between the check and the rebase adds no path": {
			err:       niceyaml.Rebase(fmt.Errorf("checking hours: %w", inner), hours),
			doc:       dd,
			want:      "checking hours: closes before it opens",
			wantPath:  hours.Child("close"),
			wantBound: "4:10: $.hours.close",
		},
		"a location set on the rebased error is under the base": {
			err: yamltest.RequireError(t, niceyaml.Rebase(inner, hours)).
				With(niceyaml.AtPath(paths.Current().Child("open"))),
			doc:       dd,
			want:      "closes before it opens",
			wantPath:  hours.Child("open"),
			wantBound: "3:9: $.hours.open",
		},
	}

	for name, tc := range composed {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			require.ErrorIs(t, tc.err, inner)
			assert.Equal(t, tc.want, tc.err.Error())
			assert.Equal(t, tc.wantPath, yamltest.PathOf(t, yamltest.RequireError(t, tc.err)))
			assert.Equal(
				t,
				tc.wantPath.String()+": "+tc.want,
				niceyaml.FormatError(tc.err, niceyaml.WithContextLines(0)),
			)

			bound := tc.doc.Bind(tc.err)
			require.EqualError(t, bound, tc.wantBound+": "+tc.want)
			assert.Equal(t, tc.want, yamltest.RequireSourceError(t, bound).Message())
		})
	}

	t.Run("a decode rebases a nested self validator the same way", func(t *testing.T) {
		t.Parallel()

		doc := yamltest.FirstDocument(t, input, niceyaml.WithName("cafe.yaml"))

		_, err := doc.Decode[rebasedConfig](t.Context())
		require.EqualError(t, err, "cafe.yaml:4:10: $.hours.close: closes before it opens")
		assert.Equal(t, stringtest.JoinLF(
			"cafe.yaml:4:10: $.hours.close: closes before it opens",
			"",
			"   2 | hours:",
			`   3 |   open: "09:00"`,
			`   4 |   close: "08:00"`,
			"     |          ^^^^^^^",
		), fmt.Sprintf("%+v", err))
	})

	t.Run("an error without a location points at the base", func(t *testing.T) {
		t.Parallel()

		err := dd.Bind(niceyaml.Rebase(errors.New("bad hours"), hours))
		require.EqualError(t, err, "2:1: $.hours: bad hours")

		assert.Equal(t, "$.hours", yamltest.PathOf(t, yamltest.RequireError(t, err)).String())
	})

	t.Run("the errors a summary heads compose too", func(t *testing.T) {
		t.Parallel()

		report := niceyaml.NewSummary("invalid hours",
			yamltest.ErrorAt("bad open", "open"),
			yamltest.ErrorAt("bad close", "close"),
		)

		// The summary is a heading, so it takes no location from the base.
		err := dd.Bind(niceyaml.Rebase(report, hours))
		require.EqualError(t, err, stringtest.JoinLF(
			"invalid hours",
			"3:9: $.hours.open: bad open",
			"4:10: $.hours.close: bad close",
		))

		problems := yamltest.ProblemsOf(yamltest.RequireSourceError(t, err))
		assert.Equal(t, []string{
			"3:9: $.hours.open: bad open",
			"4:10: $.hours.close: bad close",
		}, errorTexts(problems))
		assert.Equal(t, "bad open", problems[0].Bound.Message())
		assert.Equal(t, "$.hours.open", yamltest.PathOf(t, problems[0].Bound).String())
	})

	t.Run("an unbound tree shows nested paths under the base", func(t *testing.T) {
		t.Parallel()

		report := niceyaml.NewError("invalid hours", niceyaml.WithDetails(
			yamltest.ErrorAt("bad open", "open"),
		))

		assert.Equal(t, stringtest.JoinLF(
			"@.hours: invalid hours",
			"`-- @.hours.open: bad open",
		), fmt.Sprintf("%+v", niceyaml.Rebase(report, hours)))
	})

	t.Run("a join rebases branch by branch", func(t *testing.T) {
		t.Parallel()

		open := yamltest.ErrorAt("bad open", "open")
		other := errors.New("bad hours")

		err := niceyaml.Rebase(errors.Join(open, other), hours)
		require.EqualError(t, err, stringtest.JoinLF("bad open", "bad hours"))
		assert.Equal(
			t,
			stringtest.JoinLF("|-- @.hours.open: bad open", "`-- @.hours: bad hours"),
			niceyaml.FormatError(err, niceyaml.WithContextLines(0)),
		)
		require.ErrorIs(t, err, open)
		require.ErrorIs(t, err, other)

		// The bound join is no node of the tree, so each branch is a
		// binding of its own, by position.
		assert.Equal(t, []string{
			"2:1: $.hours: bad hours",
			"3:9: $.hours.open: bad open",
		}, bindingTexts(dd.Bind(err)))
	})

	t.Run("an error that only wraps a join rebases as the join", func(t *testing.T) {
		t.Parallel()

		open := yamltest.ErrorAt("bad open", "open")
		closeErr := yamltest.ErrorAt("bad close", "close")

		err := niceyaml.Rebase(niceyaml.Invalid(errors.Join(open, closeErr)), hours)
		require.EqualError(t, err, stringtest.JoinLF("bad open", "bad close"))
		require.ErrorIs(t, err, open)
		require.ErrorIs(t, err, closeErr)

		require.ErrorAs(t, err, new(*niceyaml.Error))

		assert.Equal(t, []string{
			"3:9: $.hours.open: bad open",
			"4:10: $.hours.close: bad close",
		}, bindingTexts(dd.Bind(err)))

		bare := dd.Bind(niceyaml.Rebase(errors.Join(open, closeErr), hours))
		assert.Equal(
			t,
			niceyaml.FormatError(bare, niceyaml.WithContextLines(-1)),
			niceyaml.FormatError(dd.Bind(err), niceyaml.WithContextLines(-1)),
		)
	})

	t.Run("a join of the caller's own type keeps its type", func(t *testing.T) {
		t.Parallel()

		open := yamltest.ErrorAt("bad open", "open")
		closeErr := yamltest.ErrorAt("bad close", "close")
		joined := sparseJoinError{errs: []error{open, nil, closeErr}}

		bare := niceyaml.Rebase(errors.Join(open, closeErr), hours)

		tcs := map[string]struct {
			err error
		}{
			"join": {
				err: joined,
			},
			"error that only wraps the join": {
				err: niceyaml.Invalid(joined),
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				err := niceyaml.Rebase(tc.err, hours)
				require.EqualError(t, err, stringtest.JoinLF("bad open", "bad close"))
				require.ErrorIs(t, err, open)
				require.ErrorIs(t, err, closeErr)

				var got sparseJoinError

				require.ErrorAs(t, err, &got)
				assert.Equal(t, joined, got)

				assert.Equal(t, bindingTexts(dd.Bind(bare)), bindingTexts(dd.Bind(err)))
				assert.Equal(
					t,
					niceyaml.FormatError(dd.Bind(bare), niceyaml.WithContextLines(-1)),
					niceyaml.FormatError(dd.Bind(err), niceyaml.WithContextLines(-1)),
				)
			})
		}
	})

	t.Run("a join of the caller's own type keeps its Is and As methods", func(t *testing.T) {
		t.Parallel()

		joined := classifiedJoinError{sparseJoinError{errs: []error{
			yamltest.ErrorAt("bad open", "open"),
			yamltest.ErrorAt("bad close", "close"),
		}}}

		err := niceyaml.Rebase(joined, hours)
		require.EqualError(t, err, stringtest.JoinLF("bad open", "bad close"))
		require.ErrorIs(t, err, errInvalidHours)

		var custom *customTestError

		require.ErrorAs(t, err, &custom)
		assert.Equal(t, "invalid hours", custom.Error())
	})

	t.Run("a decode keeps the type of a join a self validator returns", func(t *testing.T) {
		t.Parallel()

		_, err := dd.Decode[joinedConfig](t.Context())
		require.EqualError(t, err, stringtest.JoinLF("3:9: $.hours.open: bad open", "4:10: $.hours.close: bad close"))

		var got sparseJoinError

		require.ErrorAs(t, err, &got)
	})

	t.Run("a problem without a location in a summary points at the base", func(t *testing.T) {
		t.Parallel()

		report := niceyaml.NewSummary("invalid hours", errors.New("bad open"), errors.New("bad close"))

		err := dd.Bind(niceyaml.Rebase(report, hours))

		bound := yamltest.RequireSourceError(t, err)
		assert.Equal(
			t,
			[]string{"2:1: $.hours: bad open", "2:1: $.hours: bad close"},
			errorTexts(yamltest.ProblemsOf(bound)),
		)

		_, ok := bound.Range()
		assert.False(t, ok)
	})

	t.Run("a detail without a location takes none from the base", func(t *testing.T) {
		t.Parallel()

		report := niceyaml.NewError("invalid hours", niceyaml.WithDetails(errors.New("bad hours")))

		err := dd.Bind(niceyaml.Rebase(report, hours))
		require.EqualError(t, err, "2:1: $.hours: invalid hours")

		bound := yamltest.RequireSourceError(t, err)
		assert.Empty(t, yamltest.ProblemsOf(bound))
		require.Len(t, yamltest.DetailsOf(bound), 1)

		detail := yamltest.DetailsOf(bound)[0].Bound
		assert.Equal(t, "bad hours", detail.Error())

		_, ok := detail.Path()
		assert.False(t, ok)
	})

	t.Run("an unlocated problem in a summary under a root base points at the root", func(t *testing.T) {
		t.Parallel()

		report := niceyaml.NewSummary("invalid", errors.New("bad child"), errors.New("bad sibling"))
		rebased := niceyaml.Rebase(report, paths.Current())

		err := dd.Bind(rebased)
		require.EqualError(t, err, stringtest.JoinLF(
			"invalid",
			"1:1: $: bad child",
			"1:1: $: bad sibling",
		))

		problems := yamltest.ProblemsOf(yamltest.RequireSourceError(t, err))
		assert.Equal(t, []string{"1:1: $: bad child", "1:1: $: bad sibling"}, errorTexts(problems))
		assert.Equal(t, paths.Doc(), yamltest.PathOf(t, problems[0].Bound))

		assert.Equal(t, stringtest.JoinLF(
			"invalid",
			"|-- @: bad child",
			"`-- @: bad sibling",
		), fmt.Sprintf("%+v", rebased))
	})

	t.Run("a position stays as it is", func(t *testing.T) {
		t.Parallel()

		err := dd.Bind(niceyaml.Rebase(niceyaml.NewError("bad", niceyaml.AtPosition(position.New(0, 6))), hours))
		require.EqualError(t, err, "1:7: bad")
	})

	t.Run("a bound error comes back as it is", func(t *testing.T) {
		t.Parallel()

		bound := dd.Bind(yamltest.ErrorAt("bad", "close"))
		require.Error(t, bound)

		assert.Same(t, bound, niceyaml.Rebase(bound, hours))

		wrapped := fmt.Errorf("context: %w", bound)
		assert.Same(t, wrapped, niceyaml.Rebase(wrapped, hours))
	})

	t.Run("details above a binding join the base", func(t *testing.T) {
		t.Parallel()

		namePath := paths.Current().Child("name")
		bound := dd.Bind(yamltest.ErrorAt("bad name", "name"))
		wrapped := niceyaml.Invalid(bound, niceyaml.WithDetails(
			yamltest.ErrorAt("bad open", "open"),
		))

		r := niceyaml.Rebase(wrapped, hours)
		assert.Equal(t, "1:7: $.name: bad name", r.Error())

		assert.Equal(t, paths.Doc().Join(namePath), yamltest.PathOf(t, yamltest.RequireError(t, r)))

		rebound := yamltest.RequireSourceError(t, dd.Bind(r))
		assert.Equal(t, "1:7: $.name: bad name", rebound.Error())

		assert.Equal(t, paths.Doc().Join(namePath), yamltest.PathOf(t, rebound))

		assert.Empty(t, yamltest.ProblemsOf(rebound))
		assert.Equal(t, []string{"3:9: $.hours.open: bad open"}, errorTexts(yamltest.DetailsOf(rebound)))
	})

	t.Run("an unlocated binding under the base keeps no path", func(t *testing.T) {
		t.Parallel()

		bound := dd.Bind(errors.New("plain"))
		wrapped := niceyaml.Invalid(bound, niceyaml.WithDetails(
			yamltest.ErrorAt("bad open", "open"),
		))

		r := niceyaml.Rebase(wrapped, hours)
		assert.Equal(t, bound.Error(), r.Error())

		_, ok := yamltest.RequireError(t, r).Path()
		assert.False(t, ok)

		rebound := yamltest.RequireSourceError(t, dd.Bind(r))

		_, ok = rebound.Path()
		assert.False(t, ok)
	})

	t.Run("a check runs under the scope it is written for", func(t *testing.T) {
		t.Parallel()

		checkHours := func(h *rebasedHours) error {
			return h.Validate()
		}

		c, err := dd.Decode[rebasedConfig](t.Context(), niceyaml.WithSelfValidation(false))
		require.NoError(t, err)

		err = dd.Bind(niceyaml.Rebase(checkHours(&c.Hours), hours))
		require.EqualError(t, err, "4:10: $.hours.close: closes before it opens")
	})
}

func TestRebase_Anchors(t *testing.T) {
	t.Parallel()

	doc := yamltest.FirstDocument(t, stringtest.Input(`
		name: cafe
		spec:
		  hours:
		    open: "09:00"
	`))

	hoursPath := paths.Doc().Child("spec", "hours")
	hours := yamltest.At(t, doc, hoursPath)

	open := yamltest.ErrorAt("bad", "open")
	name := niceyaml.NewError("bad", niceyaml.AtPath(paths.Doc().Child("name")))

	tcs := map[string]struct {
		err error
		// The node that binds the result.
		node *niceyaml.Node
		// The path of the unbound result, and the message of its binding.
		wantPath string
		want     string
	}{
		"relative path under a relative base": {
			err:      niceyaml.Rebase(open, paths.Current().Child("spec", "hours")),
			node:     doc,
			wantPath: "@.spec.hours.open",
			want:     `4:11: $.spec.hours.open: bad`,
		},
		"relative path under an absolute base": {
			err:      niceyaml.Rebase(open, hoursPath),
			node:     doc,
			wantPath: "$.spec.hours.open",
			want:     `4:11: $.spec.hours.open: bad`,
		},
		"absolute path under a relative base": {
			err:      niceyaml.Rebase(name, paths.Current().Child("spec", "hours")),
			node:     doc,
			wantPath: "$.name",
			want:     `1:7: $.name: bad`,
		},
		"absolute path under an absolute base": {
			err:      niceyaml.Rebase(name, hoursPath),
			node:     doc,
			wantPath: "$.name",
			want:     `1:7: $.name: bad`,
		},
		"no location under an absolute base": {
			err:      niceyaml.Rebase(errors.New("bad"), hoursPath),
			node:     doc,
			wantPath: "$.spec.hours",
			want:     `3:3: $.spec.hours: bad`,
		},
		"absolute base stops the bases above it": {
			err:      niceyaml.Rebase(niceyaml.Rebase(open, hoursPath), paths.Current().Child("x")),
			node:     doc,
			wantPath: "$.spec.hours.open",
			want:     `4:11: $.spec.hours.open: bad`,
		},
		"scoped Node binds under its own path": {
			err:      niceyaml.Rebase(open, hours.Path()),
			node:     hours,
			wantPath: "$.spec.hours.open",
			want:     `4:11: $.spec.hours.open: bad`,
		},
		"scoped Node puts its path in front of a relative path": {
			err:      niceyaml.Rebase(open, paths.Current()),
			node:     hours,
			wantPath: "@.open",
			want:     `4:11: $.spec.hours.open: bad`,
		},
		"scoped Node leaves an absolute path": {
			err:      name,
			node:     hours,
			wantPath: "$.name",
			want:     `1:7: $.name: bad`,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.wantPath, yamltest.PathOf(t, yamltest.RequireError(t, tc.err)).String())

			bound := tc.node.Bind(tc.err)
			require.EqualError(t, bound, tc.want)

			boundPath := yamltest.PathOf(t, yamltest.RequireSourceError(t, bound))
			assert.True(t, boundPath.IsAbsolute(), boundPath.String())
		})
	}
}

// valueRuleError is the error of a check that knows nothing of YAML, as a
// policy engine reports one.
type valueRuleError struct {
	msg string
}

func (e *valueRuleError) Error() string {
	return e.msg
}

// valueErrors returns the first n errors of a check on a request, under a
// summary when n is 2. Each error carries a path that reads from the
// value.
func valueErrors(n int) error {
	errs := []error{
		niceyaml.Invalid(&valueRuleError{msg: "0 is less than 1"}, niceyaml.AtPath(paths.Current().Child("port"))),
		yamltest.ErrorAt("name is required", "name"),
	}

	return niceyaml.NewSummary("2 violations", errs[:n]...)
}

// valueConfig holds a [valueRequest] under the key request.
type valueConfig struct {
	Request valueRequest `yaml:"request"`
}

// valueRequest is a [niceyaml.SelfValidator] whose Validate returns its
// error through [niceyaml.BindValue], so a caller outside a decode
// prints the path. With fixed, it returns that error inside a
// [yamltest.RewriteError].
type valueRequest struct {
	Port int `yaml:"port"`

	fixed bool
}

func (r valueRequest) Validate() error {
	if r.Port >= 1 {
		return nil
	}

	err := niceyaml.BindValue(yamltest.ErrorAt("0 is less than 1", "port"))
	if r.fixed {
		return yamltest.RewriteError{Err: err}
	}

	//nolint:wrapcheck // The test checks where a decode places the result as it is.
	return err
}

func TestBindValue(t *testing.T) {
	t.Parallel()

	port := yamltest.ErrorAt("0 is less than 1", "port")

	// The value came from no document, so the binding names each path
	// from the value, with no file and no position in front.
	tcs := map[string]struct {
		err        error
		want       string
		wantFormat string
		wantPaths  []string
	}{
		"one problem": {
			err:        valueErrors(1),
			want:       "$.port: 0 is less than 1",
			wantFormat: "$.port: 0 is less than 1",
			wantPaths:  []string{"$.port"},
		},
		"summary of two problems": {
			err: valueErrors(2),
			want: stringtest.JoinLF(
				"2 violations",
				"$.port: 0 is less than 1",
				"$.name: name is required",
			),
			wantFormat: stringtest.JoinLF(
				"2 violations",
				"|-- $.port: 0 is less than 1",
				"`-- $.name: name is required",
			),
			wantPaths: []string{"$.port", "$.name"},
		},
		"join of two problems": {
			err: errors.Join(
				port,
				yamltest.ErrorAt("name is required", "name"),
			),
			want: stringtest.JoinLF(
				"$.port: 0 is less than 1",
				"$.name: name is required",
			),
			wantFormat: stringtest.JoinLF(
				"|-- $.port: 0 is less than 1",
				"`-- $.name: name is required",
			),
			wantPaths: []string{"$.port", "$.name"},
		},
		"wrapper around a problem": {
			err:        fmt.Errorf("check: %w", port),
			want:       "$.port: check: 0 is less than 1",
			wantFormat: "$.port: check: 0 is less than 1",
			wantPaths:  []string{"$.port"},
		},
		"path from the root": {
			err:        niceyaml.NewError("0 is less than 1", niceyaml.AtPath(paths.Doc().Child("port"))),
			want:       "$.port: 0 is less than 1",
			wantFormat: "$.port: 0 is less than 1",
			wantPaths:  []string{"$.port"},
		},
		"no location": {
			err:        errors.New("request refused"),
			want:       "request refused",
			wantFormat: "request refused",
		},
		"problem with a detail": {
			err: niceyaml.NewError("ports conflict", niceyaml.WithDetails(
				yamltest.ErrorAt("first declared here", "port"),
			)),
			want: "ports conflict",
			wantFormat: stringtest.JoinLF(
				"ports conflict",
				"`-- $.port: first declared here",
			),
			wantPaths: []string{"$.port"},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := niceyaml.BindValue(tc.err)
			require.EqualError(t, got, tc.want)

			// A wrapper and a join keep the text of the error they hold,
			// so each path shows through both.
			assert.Equal(t, tc.want, fmt.Sprintf("%v", got))
			require.EqualError(t, fmt.Errorf("invalid request: %w", got), "invalid request: "+tc.want)
			require.EqualError(t, errors.Join(errors.New("other failure"), got),
				stringtest.JoinLF("other failure", tc.want))

			// The source holds no line to excerpt, so the tree stands
			// alone, with no line that says so.
			assert.Equal(t, tc.wantFormat, niceyaml.FormatError(got))
			assert.Equal(t, tc.wantFormat, fmt.Sprintf("%+v", got))

			// No binding of the result names a Node, a document, or a
			// position, and its source holds no name and no line.
			var gotPaths []string

			for _, b := range yamltest.BoundIn(got) {
				_, ok := b.Node()
				assert.False(t, ok)

				_, ok = b.Document()
				assert.False(t, ok)

				assert.Empty(t, b.Source().Name())
				assert.Zero(t, b.Source().Lines().Len())

				_, ok = b.DocumentIndex()
				assert.False(t, ok)

				_, ok = b.Position()
				assert.False(t, ok)

				_, ok = b.Range()
				assert.False(t, ok)

				path, ok := b.Path()
				if !ok {
					require.NoError(t, b.Unresolved())

					continue
				}

				// The reason is the one of a document with no content,
				// which FormatError leaves out.
				require.ErrorIs(t, b.Unresolved(), paths.ErrNoDocument)

				gotPaths = append(gotPaths, path.String())
			}

			assert.Equal(t, tc.wantPaths, gotPaths)
		})
	}

	t.Run("nothing to bind", func(t *testing.T) {
		t.Parallel()

		var (
			nilErr   *niceyaml.Error
			nilBound *niceyaml.SourceError
		)

		require.NoError(t, niceyaml.BindValue(nil))
		require.NoError(t, niceyaml.BindValue(nilErr))
		require.NoError(t, niceyaml.BindValue(nilBound))
	})

	t.Run("a bound error comes back as it is", func(t *testing.T) {
		t.Parallel()

		doc := yamltest.FirstDocument(t, "port: 0\n", niceyaml.WithName("app.yaml"))

		inDocument := doc.Bind(port)
		inNone := niceyaml.BindValue(port)
		wrapped := fmt.Errorf("check: %w", inNone)

		assert.Same(t, inDocument, niceyaml.BindValue(inDocument))
		assert.Same(t, inNone, niceyaml.BindValue(inNone))
		assert.Same(t, wrapped, niceyaml.BindValue(wrapped))
		require.EqualError(t, wrapped, "check: $.port: 0 is less than 1")
	})

	t.Run("the result matches what its error matches", func(t *testing.T) {
		t.Parallel()

		errCheck := errors.New("check")

		got := niceyaml.BindValue(fmt.Errorf("%w: %w", errCheck, valueErrors(2)))
		require.ErrorIs(t, got, errCheck)
		assert.True(t, niceyaml.IsInvalid(got))

		var rule *valueRuleError

		require.ErrorAs(t, got, &rule)
		assert.Equal(t, "0 is less than 1", rule.msg)

		// BindValue declares nothing about the error it binds.
		assert.False(t, niceyaml.IsInvalid(niceyaml.BindValue(errors.New("request refused"))))
	})

	t.Run("a base moves the result before it binds again", func(t *testing.T) {
		t.Parallel()

		// Rebase reads the result as the error it was made from, which
		// nothing bound, so the value of one check goes under a field of
		// a larger value.
		inner := niceyaml.BindValue(valueErrors(2))
		outer := niceyaml.BindValue(niceyaml.Rebase(inner, paths.Current().Child("request")))

		require.EqualError(t, outer, stringtest.JoinLF(
			"2 violations",
			"$.request.port: 0 is less than 1",
			"$.request.name: name is required",
		))
		require.EqualError(t, inner, stringtest.JoinLF(
			"2 violations",
			"$.port: 0 is less than 1",
			"$.name: name is required",
		))
	})

	t.Run("an Error with a location binds around the result", func(t *testing.T) {
		t.Parallel()

		inner := niceyaml.BindValue(valueErrors(2))
		at := niceyaml.AtPath(paths.Current().Child("request"))

		require.EqualError(t, niceyaml.BindValue(niceyaml.Unchecked(inner, at)), stringtest.JoinLF(
			"$.request: 2 violations",
			"$.port: 0 is less than 1",
			"$.name: name is required",
		))
	})

	t.Run("a position finds no line", func(t *testing.T) {
		t.Parallel()

		got := niceyaml.BindValue(niceyaml.NewError("bad indent", niceyaml.AtPosition(position.New(1, 2))))
		require.EqualError(t, got, "bad indent")

		bound := yamltest.RequireSourceError(t, got)
		require.ErrorIs(t, bound.Unresolved(), niceyaml.ErrOutOfRange)

		// A document places the position as it places a path.
		doc := yamltest.FirstDocument(t, stringtest.LinesLF("request:", "  port: 0"), niceyaml.WithName("app.yaml"))
		require.EqualError(t, doc.Bind(got), "app.yaml:2:3: bad indent")
	})

	t.Run("a caller binds what a Validate method returns", func(t *testing.T) {
		t.Parallel()

		hours := rebasedHours{Open: "17:00", Close: "09:00"}

		require.EqualError(t, hours.Validate(), "closes before it opens")
		require.EqualError(t, niceyaml.BindValue(hours.Validate()), "$.close: closes before it opens")
		require.NoError(t, niceyaml.BindValue(rebasedHours{Open: "09:00", Close: "17:00"}.Validate()))
	})
}

func TestBindValue_Place(t *testing.T) {
	t.Parallel()

	doc := yamltest.FirstDocument(t, stringtest.LinesLF("request:", "  port: 0"), niceyaml.WithName("app.yaml"))
	base := paths.Doc().Child("request")

	// A sentinel a caller wraps beside the error of a check.
	errCheck := errors.New("check")

	// The result stands in no document, so Rebase and Bind place it as
	// they place an error that no source bound yet. Each path then shows
	// once, with or without a wrapper around the result.
	tcs := map[string]struct {
		err         error
		wantValue   string
		wantBound   string
		wantWrapped string
		// The result bound through the root with no Rebase, where each
		// path reads from the root of the document.
		wantRoot string
	}{
		"one problem": {
			err:         valueErrors(1),
			wantValue:   "$.port: 0 is less than 1",
			wantBound:   "app.yaml:2:9: $.request.port: 0 is less than 1",
			wantWrapped: "app.yaml:2:9: $.request.port: check: 0 is less than 1",
			wantRoot:    "app.yaml:1:1: $.port: 0 is less than 1",
		},
		"two problems": {
			err: valueErrors(2),
			wantValue: stringtest.JoinLF(
				"2 violations",
				"$.port: 0 is less than 1",
				"$.name: name is required",
			),
			wantBound: stringtest.JoinLF(
				"app.yaml: 2 violations",
				"app.yaml:1:1: $.request.name: name is required",
				"app.yaml:2:9: $.request.port: 0 is less than 1",
			),
			wantWrapped: stringtest.JoinLF(
				"app.yaml: check: 2 violations",
				"app.yaml:1:1: $.request.name: name is required",
				"app.yaml:2:9: $.request.port: 0 is less than 1",
			),
			wantRoot: stringtest.JoinLF(
				"app.yaml: 2 violations",
				"app.yaml:1:1: $.port: 0 is less than 1",
				"app.yaml:1:1: $.name: name is required",
			),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			err := niceyaml.BindValue(tc.err)
			require.EqualError(t, err, tc.wantValue)

			placed := doc.Bind(niceyaml.Rebase(err, base))
			require.EqualError(t, placed, tc.wantBound)
			assert.True(t, niceyaml.IsInvalid(placed))

			var rule *valueRuleError

			require.ErrorAs(t, placed, &rule)

			for _, b := range yamltest.BoundIn(placed) {
				assert.Same(t, doc.Source(), b.Source())
				assert.Same(t, doc, yamltest.BoundDocument(t, b))
			}

			// A plain error under the same base and the same Node binds
			// to the same text.
			require.EqualError(t, doc.Bind(niceyaml.Rebase(tc.err, base)), tc.wantBound)

			require.EqualError(t,
				doc.Bind(niceyaml.Rebase(fmt.Errorf("check: %w", err), base)),
				tc.wantWrapped,
			)
			require.EqualError(t,
				doc.Bind(niceyaml.Rebase(fmt.Errorf("check: %w", fmt.Errorf("body: %w", err)), base)),
				strings.Replace(tc.wantWrapped, "check: ", "check: body: ", 1),
			)

			// A wrapper that names a sentinel beside the result reads as
			// a wrapper around the result alone, and still matches the
			// sentinel.
			beside := doc.Bind(niceyaml.Rebase(fmt.Errorf("%w: %w", errCheck, err), base))
			require.EqualError(t, beside, tc.wantWrapped)
			require.ErrorIs(t, beside, errCheck)

			// A Node scoped to the value puts its own path in front, and
			// the root reads each path from the root of the document.
			require.EqualError(t, yamltest.At(t, doc, base).Bind(err), tc.wantBound)
			require.EqualError(t, doc.Bind(err), tc.wantRoot)
			require.EqualError(t, doc.Source().Bind(err), tc.wantRoot)
			require.EqualError(t, yamltest.Layers(t, doc).Bind(err), tc.wantRoot)

			// A join places each result it holds.
			require.EqualError(t, doc.Bind(niceyaml.Rebase(errors.Join(err), base)), tc.wantBound)

			// Placing builds a new error and leaves the result as it was.
			require.EqualError(t, err, tc.wantValue)

			for _, b := range yamltest.BoundIn(err) {
				assert.NotSame(t, doc.Source(), b.Source())

				_, ok := b.Document()
				assert.False(t, ok)
			}
		})
	}

	t.Run("a wrapper keeps what it matches", func(t *testing.T) {
		t.Parallel()

		wrapped := &yamltest.CheckError{Err: niceyaml.BindValue(valueErrors(1))}
		require.EqualError(t, wrapped, "check: $.port: 0 is less than 1")

		placed := doc.Bind(niceyaml.Rebase(wrapped, base))
		require.EqualError(t, placed, "app.yaml:2:9: $.request.port: check: 0 is less than 1")
		require.ErrorIs(t, placed, wrapped)

		var got *yamltest.CheckError

		require.ErrorAs(t, placed, &got)
		assert.Same(t, wrapped, got)
	})

	t.Run("a wrapper that rewrites the text keeps its text", func(t *testing.T) {
		t.Parallel()

		one := niceyaml.BindValue(valueErrors(1))
		two := niceyaml.BindValue(valueErrors(2))

		// The text of each wrapper holds the text of the result nowhere, or
		// twice, so no path could leave it. The document places every
		// problem all the same, and the wrapper keeps the text it wrote.
		tcs := map[string]struct {
			err  error
			want string
		}{
			"message of its own": {
				err:  yamltest.RewriteError{Err: one},
				want: "app.yaml:2:9: $.request.port: rewritten",
			},
			"message of its own above two problems": {
				err: yamltest.RewriteError{Err: two},
				want: stringtest.JoinLF(
					"app.yaml: rewritten",
					"app.yaml:1:1: $.request.name: name is required",
					"app.yaml:2:9: $.request.port: 0 is less than 1",
				),
			},
			"message of its own beside a sentinel": {
				err:  fmt.Errorf("%w: %w", errCheck, yamltest.RewriteError{Err: one}),
				want: "app.yaml:2:9: $.request.port: check: rewritten",
			},
			"indented lines": {
				err: yamltest.IndentError{Err: two},
				want: stringtest.JoinLF(
					"app.yaml: validation:",
					"  2 violations",
					"  $.port: 0 is less than 1",
					"  $.name: name is required",
					"app.yaml:1:1: $.request.name: name is required",
					"app.yaml:2:9: $.request.port: 0 is less than 1",
				),
			},
			"text held twice": {
				err:  fmt.Errorf("%w (again: %s)", one, one.Error()),
				want: "app.yaml:2:9: $.request.port: $.port: 0 is less than 1 (again: $.port: 0 is less than 1)",
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				placed := doc.Bind(niceyaml.Rebase(tc.err, base))
				require.EqualError(t, placed, tc.want)
				require.ErrorIs(t, placed, tc.err)
				assert.True(t, niceyaml.IsInvalid(placed))

				var rule *valueRuleError

				require.ErrorAs(t, placed, &rule)

				yamltest.AssertBoundTo(t, doc.Source(), placed)
			})
		}
	})

	t.Run("an Error around the result binds as it does around the error", func(t *testing.T) {
		t.Parallel()

		at := niceyaml.AtPath(paths.Current())
		why := niceyaml.WithDetails(errors.New("the body of the request"))

		// An Error writes the message of the error it wraps, so it binds
		// around the result as it binds around an error that no source
		// bound yet. No line keeps a path from the value.
		tcs := map[string]struct {
			wrap        func(error) error
			problems    int
			want        string
			wantDetails []string
		}{
			"Invalid with no option": {
				wrap:     func(err error) error { return niceyaml.Invalid(err) },
				problems: 1,
				want:     "app.yaml:2:9: $.request.port: 0 is less than 1",
			},
			"Unchecked with no option under a wrapper": {
				wrap:     func(err error) error { return fmt.Errorf("check: %w", niceyaml.Unchecked(err)) },
				problems: 1,
				want:     "app.yaml:2:9: $.request.port: check: 0 is less than 1",
			},
			"location above one problem": {
				wrap:     func(err error) error { return niceyaml.Invalid(err, at) },
				problems: 1,
				want:     "app.yaml:1:1: $.request: 0 is less than 1",
			},
			"location above two problems": {
				wrap:     func(err error) error { return niceyaml.Invalid(err, at) },
				problems: 2,
				want: stringtest.JoinLF(
					"app.yaml:1:1: $.request: 2 violations",
					"app.yaml:1:1: $.request.name: name is required",
					"app.yaml:2:9: $.request.port: 0 is less than 1",
				),
			},
			"wrapper around a location": {
				wrap:     func(err error) error { return fmt.Errorf("check: %w", niceyaml.Unchecked(err, at)) },
				problems: 2,
				want: stringtest.JoinLF(
					"app.yaml:1:1: $.request: check: 2 violations",
					"app.yaml:1:1: $.request.name: name is required",
					"app.yaml:2:9: $.request.port: 0 is less than 1",
				),
			},
			"details beside one problem": {
				wrap:        func(err error) error { return niceyaml.Unchecked(err, why) },
				problems:    1,
				want:        "app.yaml:2:9: $.request.port: 0 is less than 1",
				wantDetails: []string{"the body of the request"},
			},
			"details beside two problems": {
				wrap:     func(err error) error { return niceyaml.Invalid(err, why) },
				problems: 2,
				want: stringtest.JoinLF(
					"app.yaml: 2 violations",
					"app.yaml:1:1: $.request.name: name is required",
					"app.yaml:2:9: $.request.port: 0 is less than 1",
				),
				wantDetails: []string{"the body of the request"},
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				free := valueErrors(tc.problems)

				placed := doc.Bind(niceyaml.Rebase(tc.wrap(niceyaml.BindValue(free)), base))
				require.EqualError(t, placed, tc.want)
				require.EqualError(t, doc.Bind(niceyaml.Rebase(tc.wrap(free), base)), tc.want)
				assert.True(t, niceyaml.IsInvalid(placed))

				bound := yamltest.RequireSourceError(t, placed)

				var gotDetails []string

				for _, detail := range yamltest.DetailsOf(bound) {
					gotDetails = append(gotDetails, detail.Bound.Message())
				}

				assert.Equal(t, tc.wantDetails, gotDetails)

				for _, problem := range yamltest.ProblemsOf(bound) {
					assert.Same(t, doc.Source(), problem.Bound.Source())
				}
			})
		}
	})

	t.Run("a Validate method returns the result", func(t *testing.T) {
		t.Parallel()

		// A call on the value alone prints the path, and a decode places
		// the same error at the value.
		require.EqualError(t, valueRequest{}.Validate(), "$.port: 0 is less than 1")

		var cfg valueConfig

		err := doc.DecodeInto(t.Context(), &cfg)
		require.EqualError(t, err, "app.yaml:2:9: $.request.port: 0 is less than 1")
		assert.True(t, niceyaml.IsInvalid(err))
	})

	t.Run("a Validate method returns the result under a message of its own", func(t *testing.T) {
		t.Parallel()

		cfg := valueConfig{Request: valueRequest{fixed: true}}

		err := doc.DecodeInto(t.Context(), &cfg)
		require.EqualError(t, err, "app.yaml:2:9: $.request.port: rewritten")
		assert.True(t, niceyaml.IsInvalid(err))
	})

	t.Run("a Validator returns the result", func(t *testing.T) {
		t.Parallel()

		check := niceyaml.ValidatorFunc(func(context.Context, *niceyaml.Node) error {
			return niceyaml.BindValue(valueErrors(1))
		})

		err := yamltest.At(t, doc, base).Validate(t.Context(), check)
		require.EqualError(t, err, "app.yaml:2:9: $.request.port: 0 is less than 1")
	})

	t.Run("a wrapper with several verbs places the result beside another problem", func(t *testing.T) {
		t.Parallel()

		// The wrapper heads the result and the Error beside it as two
		// problems, and its text drops the path of the result as a
		// wrapper around the result alone does.
		free := valueErrors(1)
		other := niceyaml.NewError("name is required")
		err := fmt.Errorf("%w; %w", niceyaml.BindValue(free), other)

		want := stringtest.JoinLF(
			"app.yaml: 0 is less than 1; name is required",
			"app.yaml:1:1: $.request: name is required",
			"app.yaml:2:9: $.request.port: 0 is less than 1",
		)

		placed := doc.Bind(niceyaml.Rebase(err, base))
		require.EqualError(t, placed, want)
		require.EqualError(t, doc.Bind(niceyaml.Rebase(fmt.Errorf("%w; %w", free, other), base)), want)
		require.ErrorIs(t, placed, err)
		require.ErrorIs(t, placed, other)
	})

	t.Run("a summary and a detail place the results they hold", func(t *testing.T) {
		t.Parallel()

		port := niceyaml.BindValue(valueErrors(1))
		name := niceyaml.BindValue(
			yamltest.ErrorAt("name is required", "name"),
		)

		summary := niceyaml.NewSummary("request checks", port, name)
		require.EqualError(t, doc.Bind(niceyaml.Rebase(summary, base)), stringtest.JoinLF(
			"app.yaml: request checks",
			"app.yaml:1:1: $.request.name: name is required",
			"app.yaml:2:9: $.request.port: 0 is less than 1",
		))

		detailed := niceyaml.NewError(
			"bad request",
			niceyaml.AtPath(base),
			niceyaml.WithDetails(niceyaml.Rebase(port, base)),
		)

		bound := yamltest.RequireSourceError(t, doc.Bind(detailed))
		require.Len(t, yamltest.DetailsOf(bound), 1)
		assert.Same(t, doc.Source(), yamltest.DetailsOf(bound)[0].Bound.Source())
		require.EqualError(t, yamltest.DetailsOf(bound)[0].Bound, "app.yaml:2:9: $.request.port: 0 is less than 1")
	})

	t.Run("each problem places on its own", func(t *testing.T) {
		t.Parallel()

		result := yamltest.RequireSourceError(t, niceyaml.BindValue(valueErrors(2)))

		problems := yamltest.ProblemsOf(result)
		require.Len(t, problems, 2)

		// A caller that keeps only some of the problems places the ones
		// it keeps, and each stands in the document as it does when the
		// caller places the whole result.
		want := []string{
			"app.yaml:2:9: $.request.port: 0 is less than 1",
			"app.yaml:1:1: $.request.name: name is required",
		}

		for i, problem := range problems {
			placed := doc.Bind(niceyaml.Rebase(problem.Err, base))
			require.EqualError(t, placed, want[i])
			assert.True(t, niceyaml.IsInvalid(placed))

			yamltest.AssertBoundTo(t, doc.Source(), placed)

			// The problem itself stays where the result put it.
			_, ok := problem.Bound.Document()
			assert.False(t, ok)
		}

		kept := errors.Join(problems[1].Err, problems[0].Err)
		require.EqualError(t, yamltest.At(t, doc, base).Bind(kept), stringtest.JoinLF(want[1], want[0]))
	})

	t.Run("a result placed twice gives two errors", func(t *testing.T) {
		t.Parallel()

		other := yamltest.FirstDocument(t, "port: 0\n", niceyaml.WithName("other.yaml"))

		err := niceyaml.BindValue(valueErrors(1))

		first := doc.Bind(niceyaml.Rebase(err, base))
		again := doc.Bind(niceyaml.Rebase(err, base))
		elsewhere := other.Bind(err)

		require.EqualError(t, first, "app.yaml:2:9: $.request.port: 0 is less than 1")
		require.EqualError(t, again, "app.yaml:2:9: $.request.port: 0 is less than 1")
		require.EqualError(t, elsewhere, "other.yaml:1:7: $.port: 0 is less than 1")
		assert.NotSame(t, first, again)

		// A placed error is bound to its document, so a later Bind
		// returns it as it is.
		assert.Same(t, first, other.Bind(first))
		assert.Same(t, first, niceyaml.BindValue(first))

		require.EqualError(t, err, "$.port: 0 is less than 1")
	})
}

func TestError_TokenAfterTrailingSpaces(t *testing.T) {
	t.Parallel()

	// The lexer counts the spaces after a value into its column, and the
	// Source places the token where its text starts, so an error at the
	// token marks the value rather than the spaces after it.
	src := niceyaml.NewSourceFromString("a: 1   \nb: 2\n")

	var value *token.Token

	for _, tk := range src.Tokens() {
		if tk.Value == "1" {
			value = tk
		}
	}

	require.NotNil(t, value)
	assert.Equal(t, position.New(0, 3), position.NewFromToken(value))

	err := yamltest.Bind(t, src, niceyaml.NewError(
		"bad value",
		niceyaml.AtPosition(position.NewFromToken(value)),
	))

	got := render(err)
	assert.True(t, strings.HasPrefix(got, "1:4: bad value\n"), got)
	assert.Contains(t, got, "<genericError>1</genericError>")
}

func TestSourceError_MessageAtHighlightStart(t *testing.T) {
	t.Parallel()

	// A message starts where the highlight of its position starts on the
	// line, as the caret run of a root starts, so a position on the
	// spaces around a token or inside it puts the message under the token.

	tcs := map[string]struct {
		at      position.Position
		input   string
		want    string // The marker row that FormatError and View.String draw.
		wantXML string // The annotation row that the printer draws.
	}{
		"position in the indentation": {
			input:   stringtest.LinesLF("parent:", "  child: x", "  other: 1"),
			at:      position.New(2, 0),
			want:    "     |   ^^^^^ bad",
			wantXML: "<textError>  ^^^^^ bad</textError>",
		},
		"position on the spaces after a value": {
			input:   "a: 1   \n",
			at:      position.New(0, 5),
			want:    "     |    ^ bad",
			wantXML: "<textError>   ^ bad</textError>",
		},
		"position inside a token": {
			input:   "key: value\n",
			at:      position.New(0, 7),
			want:    "     |      ^^^^^ bad",
			wantXML: "<textError>     ^^^^^ bad</textError>",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			source := niceyaml.NewSourceFromString(tc.input)

			nested := yamltest.Bind(t, source, niceyaml.NewError(
				"parent",
				niceyaml.WithDetails(niceyaml.NewError("bad", niceyaml.AtPosition(tc.at))),
			))
			assert.Contains(t, niceyaml.FormatError(nested, niceyaml.WithContextLines(0)), "\n"+tc.want)
			assert.Contains(t, renderContext(nested, 0), "\n"+tc.wantXML)

			bound := requireBound(t, source, niceyaml.NewError(
				"bad", niceyaml.AtPosition(tc.at),
			))

			view := source.View()
			require.True(t, bound.Annotate(view))
			assert.Contains(t, view.String(), "\n"+tc.want)

			// The error still reports the column as given.
			wantPrefix := fmt.Sprintf("%d:%d: ", tc.at.Line+1, tc.at.Col+1)
			assert.True(t, strings.HasPrefix(bound.Error(), wantPrefix), bound.Error())
		})
	}
}

func TestFormatError(t *testing.T) {
	t.Parallel()

	first := niceyaml.NewSourceFromString("a: 1\n", niceyaml.WithName("a.yaml"))
	second := niceyaml.NewSourceFromString("b: 2\n", niceyaml.WithName("b.yaml"))
	docs := yamltest.Documents(t,
		niceyaml.NewSourceFromString(stringtest.LinesLF("a: 1", "---", "b: 2"), niceyaml.WithName("f.yaml")), 2)

	svc := niceyaml.NewSourceFromString("name: x\nport: 0\n", niceyaml.WithName("svc.yaml"))
	badPort := yamltest.ErrorAt("0 is less than 1", "port")
	badName := yamltest.ErrorAt("unknown name", "name")
	svcExcerpt := stringtest.JoinLF(
		"   1 | name: x",
		"     |       ^ unknown name",
		"   2 | port: 0",
		"     |       ^ 0 is less than 1",
	)

	// The documents of one file, as a caller that validates each document
	// of a file reads them.
	multi := yamltest.Documents(t, niceyaml.NewSourceFromString(stringtest.Input(`
		a: x
		---
		a: y
		---
		a: 1
		b: 2
		---
		c: 3
	`), niceyaml.WithName("multi.yaml")), 4)

	tcs := map[string]struct {
		err  error
		opts []niceyaml.ExcerptOption
		want string
	}{
		"nil renders as nothing": {},
		"an error bound to no source renders as its message": {
			err:  errors.New("plain"),
			want: "plain",
		},
		"a located error bound to no source renders behind its path": {
			err:  yamltest.ErrorAt("bad", "a"),
			want: "@.a: bad",
		},
		"lists the nodes below a wrapped binding": {
			err: fmt.Errorf("checking: %w", excerptError(t)),
			want: stringtest.JoinLF(
				"checking: 2:4: $.b: bad b",
				"`-- 8:4: $.h: bad h",
				"",
				"   1 | a: 1",
				"   2 | b: 2",
				"     |    ^",
				"   3 | c: 3",
				"   4 | d: 4",
				"     | ...",
				"   6 | f: 6",
				"   7 | g: 7",
				"   8 | h: 8",
				"     |    ^ bad h",
				"   9 | i: 9",
				"  10 | j: 10",
			),
		},
		"renders one excerpt per source of a join under its name": {
			err: errors.Join(
				yamltest.Bind(t, first, yamltest.ErrorAt("bad a", "a")),
				yamltest.Bind(t, second, yamltest.ErrorAt("bad b", "b")),
			),
			want: stringtest.JoinLF(
				"|-- a.yaml:1:4: $.a: bad a",
				"`-- b.yaml:1:4: $.b: bad b",
				"",
				"a.yaml",
				"   1 | a: 1",
				"     |    ^ bad a",
				"",
				"b.yaml",
				"   1 | b: 2",
				"     |    ^ bad b",
			),
		},
		"names why a binding of a join marks nothing after the excerpts": {
			err: errors.Join(
				docs[0].Bind(niceyaml.NewError("gone", niceyaml.AtPath(paths.Current().Child("x").Index(0)))),
				docs[1].Bind(yamltest.ErrorAt("bad b", "b")),
				docs[1].Bind(errors.New("plain")),
			),
			want: stringtest.JoinLF(
				"|-- f.yaml: document 1: $.x[0]: gone",
				"|-- f.yaml:3:4: $.b: bad b",
				"`-- f.yaml: document 2: plain",
				"",
				"   1 | a: 1",
				"   2 | ---",
				"   3 | b: 2",
				"     |    ^ bad b",
				"",
				"no excerpt: resolve $.x[0]: not found",
			),
		},
		// Each tab becomes four spaces, wherever it falls in its row.
		"a tab in a message with no nested errors": {
			err:  errors.New("Did you mean this?\n\tvalidate"),
			want: stringtest.JoinLF("Did you mean this?", "    validate"),
		},
		"a tab in the branches of a join": {
			err: errors.Join(errors.New("a\tb"), errors.New("Did you mean this?\n\tvalidate")),
			want: stringtest.JoinLF(
				"|-- a    b",
				"`-- Did you mean this?",
				"        validate",
			),
		},
		// A path writes a tab in a key as an escape, so the tree and
		// the reason a location did not resolve spell the key alike
		// and neither lays out a tab.
		"a tab in the key of a path that does not resolve": {
			err: yamltest.Bind(t,
				niceyaml.NewSourceFromString("a: 1\n", niceyaml.WithName("cfg.yaml")),
				niceyaml.NewError("bad", niceyaml.AtPath(paths.Current().Child("ab\tc").Index(0))),
			),
			want: stringtest.JoinLF(
				`cfg.yaml: $.'ab\tc'[0]: bad`,
				"",
				`no excerpt: resolve $.'ab\tc'[0]: not found`,
			),
		},
		// A line feed in the key is an escape too, so the key starts
		// no row of its own in the tree or in the reason.
		"a line feed and a tab in a key": {
			err: yamltest.Bind(t,
				niceyaml.NewSourceFromString("a: 1\n", niceyaml.WithName("cfg.yaml")),
				niceyaml.NewError("bad", niceyaml.AtPath(paths.Current().Child("x\ny\tz").Index(0))),
			),
			want: stringtest.JoinLF(
				`cfg.yaml: $.'x\ny\tz'[0]: bad`,
				"",
				`no excerpt: resolve $.'x\ny\tz'[0]: not found`,
			),
		},
		// The excerpt spells the message beside the caret as the tree
		// spells it.
		"a tab in a message beside a caret": {
			err: yamltest.Bind(t,
				niceyaml.NewSourceFromString("a: 1\nb: 2\n", niceyaml.WithName("cfg.yaml")),
				niceyaml.NewError("invalid", niceyaml.WithDetails(
					yamltest.ErrorAt("bad\ta", "a"),
				)),
			),
			want: stringtest.JoinLF(
				"cfg.yaml: invalid",
				"`-- 1:4: $.a: bad    a",
				"",
				"   1 | a: 1",
				"     |    ^ bad    a",
				"   2 | b: 2",
			),
		},
		// The message of a wrapper around a join holds every branch of the
		// join, and the branches are the children of the node, so the root
		// keeps the text of the wrapper alone and each branch prints once.
		"a wrapper around a join of bindings": {
			err: fmt.Errorf(
				"load config: %w",
				errors.Join(yamltest.Bind(t, svc, badPort), yamltest.Bind(t, svc, badName)),
			),
			opts: []niceyaml.ExcerptOption{niceyaml.WithContextLines(0)},
			want: stringtest.JoinLF(
				"load config:",
				"|-- svc.yaml:2:7: $.port: 0 is less than 1",
				"`-- svc.yaml:1:7: $.name: unknown name",
				"",
				svcExcerpt,
			),
		},
		"a wrapper around a bound join": {
			err:  fmt.Errorf("load config: %w", yamltest.Bind(t, svc, errors.Join(badPort, badName))),
			opts: []niceyaml.ExcerptOption{niceyaml.WithContextLines(0)},
			want: stringtest.JoinLF(
				"load config:",
				"|-- svc.yaml:1:7: $.name: unknown name",
				"`-- svc.yaml:2:7: $.port: 0 is less than 1",
				"",
				svcExcerpt,
			),
		},
		// One binding per document that fails, joined, renders as one
		// excerpt of the file.
		"renders the documents of one file as one excerpt": {
			err: errors.Join(
				multi[0].Bind(yamltest.ErrorAt("not a number", "a")),
				multi[1].Bind(yamltest.ErrorAt("not a number", "a")),
				multi[2].Bind(niceyaml.NewError("invalid document", niceyaml.WithDetails(
					yamltest.ErrorAt("not a string", "b"),
				))),
				multi[3].Bind(niceyaml.NewError("not allowed", niceyaml.AtPath(paths.Current().Child("c").Key()))),
			),
			want: stringtest.JoinLF(
				"|-- multi.yaml:1:4: $.a: not a number",
				"|-- multi.yaml:3:4: $.a: not a number",
				"|-- multi.yaml: document 3: invalid document",
				"|   `-- 6:4: $.b: not a string",
				"`-- multi.yaml:8:1: $.c~: not allowed",
				"",
				"   1 | a: x",
				"     |    ^ not a number",
				"   2 | ---",
				"   3 | a: y",
				"     |    ^ not a number",
				"   4 | ---",
				"   5 | a: 1",
				"   6 | b: 2",
				"     |    ^ not a string",
				"   7 | ---",
				"   8 | c: 3",
				"     | ^ not allowed",
			),
		},
		// Each binding still renders on its own, for a caller that wants
		// one section per document.
		"renders the binding of one document of a file": {
			err:  multi[1].Bind(yamltest.ErrorAt("bad", "a")),
			opts: []niceyaml.ExcerptOption{niceyaml.WithContextLines(0)},
			want: stringtest.JoinLF(
				"multi.yaml:3:4: $.a: bad",
				"",
				"   3 | a: y",
				"     |    ^",
			),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, niceyaml.FormatError(tc.err, tc.opts...))
		})
	}

	t.Run("a bare binding renders as the %+v verb does", func(t *testing.T) {
		t.Parallel()

		err := excerptError(t)
		assert.Equal(t, fmt.Sprintf("%+v", err), niceyaml.FormatError(err))
	})

	t.Run("looks through a wrapper for the excerpt", func(t *testing.T) {
		t.Parallel()

		src := niceyaml.NewSourceFromString("a: 1\nb: 2\n", niceyaml.WithName("x.yaml"))
		bound := yamltest.Bind(t, src, yamltest.ErrorAt("bad", "b"))
		err := fmt.Errorf("load config: %w", bound)

		// The wrapper does not format as the binding does, so the %+v verb
		// prints the message alone.
		assert.Equal(t, "load config: x.yaml:2:4: $.b: bad", fmt.Sprintf("%+v", err))

		assert.Equal(t, stringtest.JoinLF(
			"load config: x.yaml:2:4: $.b: bad",
			"",
			"   1 | a: 1",
			"   2 | b: 2",
			"     |    ^",
		), niceyaml.FormatError(err))
	})
}

func TestFormatError_JoinOfNothing(t *testing.T) {
	t.Parallel()

	// A bound join whose branches all carry nothing renders as an empty
	// tree and no excerpt, so the message stands in rather than nothing.
	// It renders as the tree of one node that holds the message, with
	// control characters as their pictures, as a plain error with the
	// same message renders.
	var nilErr *niceyaml.Error

	tcs := map[string]struct {
		name string
		want string
	}{
		"plain name": {
			name: "f.yaml",
			want: "f.yaml: \n",
		},
		"name with an escape sequence": {
			name: "f\x1b[31m.yaml",
			want: "f\u241b[31m.yaml: \n",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			source := niceyaml.NewSourceFromString("a: 1\n", niceyaml.WithName(tc.name))
			err := source.Bind(errors.Join(nilErr, nilErr))
			require.Error(t, err)

			got := niceyaml.FormatError(err)

			assert.Equal(t, tc.want, got)
			assert.Equal(t, niceyaml.FormatError(errors.New(err.Error())), got)
			assert.Equal(t, got, fmt.Sprintf("%+v", err))
			assert.NotContains(t, got, "\x1b")
		})
	}
}

func TestFormatError_NoContent(t *testing.T) {
	t.Parallel()

	badPort := yamltest.ErrorAt("0 is less than 1", "port")
	noName := yamltest.ErrorAt("name is required", "name")

	// No path resolves in a document with no content, so the tree names
	// each path and no "no excerpt:" line repeats it. One binding and a
	// summary above two thus read alike.
	tcs := map[string]struct {
		err    error
		reason error
		source string
		name   string
		want   string
	}{
		"one error": {
			err:    badPort,
			reason: paths.ErrNoDocument,
			want:   "$.port: 0 is less than 1",
		},
		"two errors under a summary": {
			err: niceyaml.NewSummary("2 problems", badPort, noName),
			want: stringtest.JoinLF(
				"2 problems",
				"|-- $.port: 0 is less than 1",
				"`-- $.name: name is required",
			),
		},
		"two errors joined": {
			err: errors.Join(badPort, noName),
			want: stringtest.JoinLF(
				"|-- $.port: 0 is less than 1",
				"`-- $.name: name is required",
			),
		},
		"error at the root of a named file": {
			err:    niceyaml.NewError(`expected "object", got "null"`, niceyaml.AtPath(paths.Current())),
			reason: paths.ErrNoDocument,
			name:   "app.yaml",
			want:   `app.yaml: $: expected "object", got "null"`,
		},
		"file of comments alone": {
			err:    badPort,
			reason: paths.ErrNoDocument,
			source: "# defaults apply\n",
			name:   "app.yaml",
			want:   "app.yaml: $.port: 0 is less than 1",
		},
		// A position names a line, which an empty source does not hold, so
		// its reason still follows the tree.
		"position keeps its reason": {
			err:    niceyaml.NewError("bad", niceyaml.AtPosition(position.New(3, 0))),
			reason: niceyaml.ErrOutOfRange,
			want:   stringtest.JoinLF("bad", "", "no excerpt: location outside source: line 4 of an empty source"),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var opts []niceyaml.SourceOption

			if tc.name != "" {
				opts = append(opts, niceyaml.WithName(tc.name))
			}

			err := yamltest.Bind(t, niceyaml.NewSourceFromString(tc.source, opts...), tc.err)

			// The binding still reports why its location did not resolve,
			// for a caller that renders the error itself.
			requireUnresolved(t, yamltest.RequireSourceError(t, err), tc.reason)

			assert.Equal(t, tc.want, niceyaml.FormatError(err))
			assert.Equal(t, tc.want, fmt.Sprintf("%+v", err))
		})
	}
}

func TestSourceError_MessageAndPath(t *testing.T) {
	t.Parallel()

	src := niceyaml.NewSourceFromString(stringtest.LinesLF("a:", "  b: 1"), niceyaml.WithName("x.yaml"))
	bPath := paths.Current().Child("a", "b")
	// A binding reports the path from the root of the document.
	wantB := paths.Doc().Child("a", "b")

	// The key "b" on line 2, not the value the path selects, which is the
	// range below.
	keyRange := yamltest.LineRange(1, 2, 3)
	valueRange := yamltest.LineRange(1, 5, 6)

	tcs := map[string]struct {
		err error
		// The text of Error, and of Message, which holds no location.
		want    string
		message string
		// The path and the range the binding reports, each nil when it
		// reports none.
		path any
		rng  any
	}{
		"a path error": {
			err:     niceyaml.NewError("bad", niceyaml.AtPath(bPath)),
			want:    "x.yaml:2:6: $.a.b: bad",
			message: "bad",
			path:    wantB,
			rng:     valueRange,
		},
		"a position error has no path": {
			err:     niceyaml.NewError("bad", niceyaml.AtPosition(position.New(1, 5))),
			want:    "x.yaml:2:6: bad",
			message: "bad",
			rng:     valueRange,
		},
		"a path beside a range names the value and binds at the range": {
			err:     niceyaml.NewError("bad", niceyaml.AtPath(bPath), niceyaml.AtRange(keyRange)),
			want:    "x.yaml:2:3: $.a.b: bad",
			message: "bad",
			path:    wantB,
			rng:     keyRange,
		},
		"a path beside a position binds at the position": {
			err:     niceyaml.NewError("bad", niceyaml.AtPath(bPath), niceyaml.AtPosition(position.New(1, 2))),
			want:    "x.yaml:2:3: $.a.b: bad",
			message: "bad",
			path:    wantB,
			rng:     keyRange,
		},
		"a range locates the error whether or not the path resolves": {
			err: niceyaml.NewError("bad",
				niceyaml.AtPath(paths.Current().Child("nope")),
				niceyaml.AtRange(keyRange),
			),
			want:    "x.yaml:2:3: $.nope: bad",
			message: "bad",
			path:    paths.Doc().Child("nope"),
			rng:     keyRange,
		},
		"a rebased path beside a range joins the base and keeps the range": {
			err: niceyaml.Rebase(
				niceyaml.NewError("bad", niceyaml.AtPath(paths.Current().Child("b")), niceyaml.AtRange(keyRange)),
				paths.Current().Child("a"),
			),
			want:    "x.yaml:2:3: $.a.b: bad",
			message: "bad",
			path:    wantB,
			rng:     keyRange,
		},
		"a rebased path joins the base": {
			err: niceyaml.Rebase(
				yamltest.ErrorAt("bad", "b"),
				paths.Current().Child("a"),
			),
			want:    "x.yaml:2:6: $.a.b: bad",
			message: "bad",
			path:    wantB,
			rng:     valueRange,
		},
		"text a wrapper added stays without the path": {
			err:     fmt.Errorf("ctx: %w", niceyaml.NewError("bad", niceyaml.AtPath(bPath))),
			want:    "x.yaml:2:6: $.a.b: ctx: bad",
			message: "ctx: bad",
			path:    wantB,
			rng:     valueRange,
		},
		"an error without a location has neither": {
			err:     errors.New("plain"),
			want:    "x.yaml: plain",
			message: "plain",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			bound := requireBound(t, src, tc.err)

			assert.Equal(t, tc.want, bound.Error())
			assert.Equal(t, tc.message, bound.Message())
			require.NoError(t, bound.Unresolved())

			var gotPath, gotRange any

			if path, ok := bound.Path(); ok {
				gotPath = path
			}

			if rng, ok := bound.Range(); ok {
				gotRange = rng
			}

			assert.Equal(t, tc.path, gotPath)
			assert.Equal(t, tc.rng, gotRange)
		})
	}

	t.Run("a summary keeps the errors it lists out of its message", func(t *testing.T) {
		t.Parallel()

		bound := requireBound(t, src, niceyaml.NewSummary("2 problems",
			niceyaml.NewError("bad", niceyaml.AtPath(bPath)),
			niceyaml.NewError("worse", niceyaml.AtPath(bPath)),
		))

		assert.Equal(t, stringtest.JoinLF(
			"x.yaml: 2 problems",
			"x.yaml:2:6: $.a.b: bad",
			"x.yaml:2:6: $.a.b: worse",
		), bound.Error())
		assert.Equal(t, "2 problems", bound.Message())
	})

	t.Run("a wrapper around a summary keeps the errors it lists out of its message", func(t *testing.T) {
		t.Parallel()

		summary := yamltest.Bind(t, src, niceyaml.NewSummary("2 problems",
			niceyaml.NewError("bad", niceyaml.AtPath(bPath)),
			niceyaml.NewError("worse", niceyaml.AtPath(bPath)),
		))
		bound := requireBound(t, src, niceyaml.NewSummary("2 checks",
			fmt.Errorf("spec check: %w", summary),
			errors.New("lint check"),
		))

		children := yamltest.ProblemsOf(bound)
		require.Len(t, children, 2)

		assert.Equal(t,
			stringtest.JoinLF(
				"spec check: x.yaml: 2 problems",
				"x.yaml:2:6: $.a.b: bad",
				"x.yaml:2:6: $.a.b: worse",
			),
			children[0].Bound.Error(),
		)
		assert.Equal(t, "spec check: x.yaml: 2 problems", children[0].Bound.Message())
	})

	t.Run("the excerpt marks a range beside a path", func(t *testing.T) {
		t.Parallel()

		bound := requireBound(t, src, niceyaml.NewError("bad", niceyaml.AtPath(bPath), niceyaml.AtRange(keyRange)))

		// The excerpt marks the one character of the range, not the
		// whole value the path selects.
		excerpt := requireExcerpt(t, bound, niceyaml.WithContextLines(0))
		assert.Equal(t, stringtest.JoinLF(
			"   2 |   b: 1",
			"     |   ^",
		), excerpt.String())
	})

	t.Run("a path from a scoped document reads from the document root", func(t *testing.T) {
		t.Parallel()

		doc := yamltest.Document(t, src)

		scoped := yamltest.At(t, doc, paths.Current().Child("a"))

		bound := yamltest.RequireSourceError(
			t,
			scoped.Bind(yamltest.ErrorAt("bad", "b")),
		)

		assert.Equal(t, "x.yaml:2:6: $.a.b: bad", bound.Error())
		assert.Equal(t, "bad", bound.Message())
		assert.Same(t, scoped, yamltest.BoundNode(t, bound))

		assert.Equal(t, wantB, yamltest.PathOf(t, bound))
		assert.Equal(t, 1, yamltest.RangeOf(t, bound).Start.Line)
	})

	t.Run("a binding that wraps a binding reports the path of the inner one", func(t *testing.T) {
		t.Parallel()

		inner := requireBound(t, src, niceyaml.NewError("bad", niceyaml.AtPath(bPath)))

		// A detail that wraps a binding binds as a detail that takes over
		// the inner binding, rather than as the inner binding itself.
		outer := requireBound(t, src, niceyaml.NewError("outer", niceyaml.WithDetails(fmt.Errorf("ctx: %w", inner))))
		require.Len(t, yamltest.DetailsOf(outer), 1)

		child := yamltest.DetailsOf(outer)[0].Bound
		require.NotSame(t, inner, child)
		assert.Equal(t, "ctx: x.yaml:2:6: $.a.b: bad", child.Message())

		assert.Equal(t, wantB, yamltest.PathOf(t, child))
	})

	t.Run("an Error with details around a binding keeps the inner message", func(t *testing.T) {
		t.Parallel()

		inner := requireBound(t, src, niceyaml.NewError("bad", niceyaml.AtPath(bPath)))

		// The Error adds no text of its own, so the message is the one the
		// inner binding annotates with, and Error still reports the
		// position and path in front of it.
		wrapped := requireBound(t, src, niceyaml.Invalid(inner, niceyaml.WithDetails(errors.New("zz"))))
		assert.Equal(t, "bad", wrapped.Message())
		assert.Equal(t, "x.yaml:2:6: $.a.b: bad", wrapped.Error())

		outer := requireBound(t, src, niceyaml.NewError("outer", niceyaml.WithDetails(
			niceyaml.Invalid(inner, niceyaml.WithDetails(errors.New("zz"))),
		)))
		require.NotEmpty(t, yamltest.DetailsOf(outer))
		assert.Equal(t, "bad", yamltest.DetailsOf(outer)[0].Bound.Message())

		got := niceyaml.FormatError(outer, niceyaml.WithContextLines(0))
		assert.Contains(t, got, "^ bad")
		assert.NotContains(t, got, "^ x.yaml:")
	})
}

func TestSourceError_Nearest(t *testing.T) {
	t.Parallel()

	doc := yamltest.FirstDocument(t, stringtest.Input(`
		servers:
		  - port: 80
		    name: a
		  - port: 81
		tls:
		note: hello
	`), niceyaml.WithName("cfg.yaml"))

	second := paths.Doc().Child("servers").Index(1)
	name := second.Child("name")

	// NearCase is an error and what its binding to a document reports.
	type nearCase struct {
		err error
		// The message, the path of the mapping or the alias the error is
		// bound at, when it is bound at one, the range it covers, if any,
		// and the reason it keeps.
		want   string
		near   string
		rng    position.Range
		reason error
	}

	// Check binds the error of each case to node and compares what the
	// binding reports.
	check := func(t *testing.T, node *niceyaml.Node, tcs map[string]nearCase) {
		t.Helper()

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				err := node.Bind(tc.err)
				require.EqualError(t, err, tc.want)

				bound := yamltest.RequireSourceError(t, err)

				near, ok := bound.Nearest()
				assert.Equal(t, tc.near != "", ok)

				if ok {
					assert.Equal(t, tc.near, near.String())
				} else {
					assert.Equal(t, paths.Current(), near)
				}

				rng, located := bound.Range()
				assert.Equal(t, tc.rng != position.Range{}, located)
				assert.Equal(t, tc.rng, rng)

				if tc.reason == nil {
					require.NoError(t, bound.Unresolved())

					return
				}

				require.ErrorIs(t, bound.Unresolved(), tc.reason)
			})
		}
	}

	tcs := map[string]nearCase{
		"key an element of a sequence leaves out": {
			err:  niceyaml.NewError("name is required", niceyaml.AtPath(name)),
			want: "cfg.yaml:4:3: $.servers[1].name: name is required",
			near: "$.servers[1]",
			rng:  yamltest.LineRange(3, 2, 3),
		},
		"key the root leaves out": {
			err:  yamltest.ErrorAt("owner is required", "owner"),
			want: "cfg.yaml:1:1: $.owner: owner is required",
			near: "$",
			rng:  yamltest.LineRange(0, 0, 7),
		},
		"several names below a null": {
			err:  yamltest.ErrorAt("cert is required", "tls", "cert", "file"),
			want: "cfg.yaml:5:1: $.tls.cert.file: cert is required",
			near: "$.tls",
			rng:  yamltest.LineRange(4, 0, 3),
		},
		"rebased under the mapping": {
			err: niceyaml.Rebase(
				yamltest.ErrorAt("name is required", "name"),
				second,
			),
			want: "cfg.yaml:4:3: $.servers[1].name: name is required",
			near: "$.servers[1]",
			rng:  yamltest.LineRange(3, 2, 3),
		},
		"key the document holds": {
			err: niceyaml.NewError(
				"bad name",
				niceyaml.AtPath(paths.Current().Child("servers").Index(0).Child("name")),
			),
			want: "cfg.yaml:3:11: $.servers[0].name: bad name",
			rng:  yamltest.LineRange(2, 10, 11),
		},
		"path beside a range": {
			err: niceyaml.NewError("name is required",
				niceyaml.AtPath(name),
				niceyaml.AtRange(yamltest.LineRange(3, 2, 3)),
			),
			want: "cfg.yaml:4:3: $.servers[1].name: name is required",
			rng:  yamltest.LineRange(3, 2, 3),
		},
		"name in a scalar": {
			err:    yamltest.ErrorAt("bad", "note", "text"),
			want:   "cfg.yaml: $.note.text: bad",
			reason: paths.ErrNotFound,
		},
		"index past the end": {
			err:    niceyaml.NewError("bad", niceyaml.AtPath(paths.Current().Child("servers").Index(5).Child("name"))),
			want:   "cfg.yaml: $.servers[5].name: bad",
			reason: paths.ErrNotFound,
		},
		"key selector on the missing key": {
			err:    niceyaml.NewError("bad", niceyaml.AtPath(name.Key())),
			want:   "cfg.yaml: $.servers[1].name~: bad",
			reason: paths.ErrNotFound,
		},
		"no location": {
			err:  errors.New("bad"),
			want: "cfg.yaml: bad",
		},
	}

	check(t, doc, tcs)

	t.Run("the message keeps the path and the excerpt marks the mapping", func(t *testing.T) {
		t.Parallel()

		err := doc.Bind(niceyaml.NewError("name is required", niceyaml.AtPath(name)))

		assert.Equal(t, stringtest.JoinLF(
			"cfg.yaml:4:3: $.servers[1].name: name is required",
			"",
			"   3 |     name: a",
			"   4 |   - port: 81",
			"     |   ^",
			"   5 | tls:",
		), niceyaml.FormatError(err, niceyaml.WithContextLines(1)))

		bound := yamltest.RequireSourceError(t, err)

		assert.Equal(t, name, yamltest.PathOf(t, bound))
	})

	t.Run("a nested error names the key it misses beside the mapping", func(t *testing.T) {
		t.Parallel()

		err := doc.Bind(niceyaml.NewSummary("2 problems",
			niceyaml.NewError("name is required", niceyaml.AtPath(name)),
			yamltest.ErrorAt("cert is required", "tls", "cert"),
		))

		assert.Equal(t, stringtest.JoinLF(
			"cfg.yaml: 2 problems",
			"|-- 4:3: $.servers[1].name: name is required",
			"`-- 5:1: $.tls.cert: cert is required",
			"",
			"   4 |   - port: 81",
			"     |   ^ name is required",
			"   5 | tls:",
			"     | ^^^ cert is required",
		), niceyaml.FormatError(err, niceyaml.WithContextLines(0)))
	})

	t.Run("a scoped Node binds a key it leaves out at the dash of its element", func(t *testing.T) {
		t.Parallel()

		scoped := yamltest.At(t, doc, second)

		err := scoped.Bind(yamltest.ErrorAt("name is required", "name"))
		require.EqualError(t, err, "cfg.yaml:4:3: $.servers[1].name: name is required")

		bound := yamltest.RequireSourceError(t, err)

		near, ok := bound.Nearest()
		require.True(t, ok)
		assert.Equal(t, second, near)
	})

	t.Run("a SelfValidator reports a field the document leaves out", func(t *testing.T) {
		t.Parallel()

		_, err := doc.Decode[requiredNames](t.Context())
		require.EqualError(t, err, "cfg.yaml:4:3: $.servers[1].name: name is required")
	})

	t.Run("a binding that wraps one bound at a mapping reports that mapping", func(t *testing.T) {
		t.Parallel()

		inner := doc.Bind(niceyaml.NewError("name is required", niceyaml.AtPath(name)))

		outer := yamltest.RequireSourceError(t, doc.Bind(niceyaml.NewError("outer", niceyaml.WithDetails(
			fmt.Errorf("check: %w", inner),
		))))
		require.Len(t, yamltest.DetailsOf(outer), 1)

		near, ok := yamltest.DetailsOf(outer)[0].Bound.Nearest()
		require.True(t, ok)
		assert.Equal(t, second, near)
	})

	t.Run("alias the document cannot follow", func(t *testing.T) {
		t.Parallel()

		// No alias but *base and *loop names an anchor of the document, as
		// an alias to an anchor of a reference document does.
		aliased := yamltest.FirstDocument(t, stringtest.Input(`
			server: *server
			servers:
			  - *server
			base: &base
			  tls: *tls
			copy: *base
			loop: &loop
			  self: *loop
			merged:
			  kind: Deploymnet
			  <<: *shared
			after:
			  <<: *shared
			  server: *server
		`), niceyaml.WithName("app.yaml"))

		server := paths.Doc().Child("server")
		star := yamltest.LineRange(0, 8, 9)

		tcs := map[string]nearCase{
			"value below the alias": {
				err:    niceyaml.NewError("bad", niceyaml.AtPath(server.Child("port"))),
				want:   "app.yaml:1:9: $.server.port: bad",
				near:   "$.server",
				rng:    star,
				reason: paths.ErrAlias,
			},
			"several names below the alias": {
				err:    niceyaml.NewError("bad", niceyaml.AtPath(server.Child("tls", "cert"))),
				want:   "app.yaml:1:9: $.server.tls.cert: bad",
				near:   "$.server",
				rng:    star,
				reason: paths.ErrAlias,
			},
			"key of an entry below the alias": {
				err:    niceyaml.NewError("bad", niceyaml.AtPath(server.Child("port").Key())),
				want:   "app.yaml:1:9: $.server.port~: bad",
				near:   "$.server",
				rng:    star,
				reason: paths.ErrAlias,
			},
			"rebased under the alias": {
				err: niceyaml.Rebase(
					yamltest.ErrorAt("bad", "port"),
					server,
				),
				want:   "app.yaml:1:9: $.server.port: bad",
				near:   "$.server",
				rng:    star,
				reason: paths.ErrAlias,
			},
			"alias that is an element of a sequence": {
				err:    niceyaml.NewError("bad", niceyaml.AtPath(paths.Doc().Child("servers").Index(0).Child("port"))),
				want:   "app.yaml:3:5: $.servers[0].port: bad",
				near:   "$.servers[0]",
				rng:    yamltest.LineRange(2, 4, 5),
				reason: paths.ErrAlias,
			},
			"alias inside an anchor the path reads through": {
				err:    niceyaml.NewError("bad", niceyaml.AtPath(paths.Doc().Child("copy", "tls", "cert"))),
				want:   "app.yaml:5:8: $.copy.tls.cert: bad",
				near:   "$.copy.tls",
				rng:    yamltest.LineRange(4, 7, 8),
				reason: paths.ErrAlias,
			},
			"alias inside the anchor it names": {
				err:    niceyaml.NewError("bad", niceyaml.AtPath(paths.Doc().Child("loop", "self", "port"))),
				want:   "app.yaml:8:9: $.loop.self.port: bad",
				near:   "$.loop.self",
				rng:    yamltest.LineRange(7, 8, 9),
				reason: paths.ErrAlias,
			},
			"alias below a merge key": {
				err:    niceyaml.NewError("bad", niceyaml.AtPath(paths.Doc().Child("after", "server", "port"))),
				want:   "app.yaml:14:11: $.after.server.port: bad",
				near:   "$.after.server",
				rng:    yamltest.LineRange(13, 10, 11),
				reason: paths.ErrAlias,
			},
			"the alias itself": {
				err:  niceyaml.NewError("bad", niceyaml.AtPath(server)),
				want: "app.yaml:1:9: $.server: bad",
				rng:  star,
			},
			"key above a merge of an alias": {
				err:    niceyaml.NewError("bad", niceyaml.AtPath(paths.Doc().Child("merged", "kind"))),
				want:   "app.yaml: $.merged.kind: bad",
				reason: paths.ErrAlias,
			},
			"key a merge of an alias may set": {
				err:    niceyaml.NewError("bad", niceyaml.AtPath(paths.Doc().Child("merged", "replicas"))),
				want:   "app.yaml: $.merged.replicas: bad",
				reason: paths.ErrAlias,
			},
		}

		check(t, aliased, tcs)

		t.Run("the message keeps the path and the excerpt marks the alias", func(t *testing.T) {
			t.Parallel()

			err := aliased.Bind(niceyaml.NewError("bad", niceyaml.AtPath(server.Child("port"))))

			assert.Equal(t, stringtest.JoinLF(
				"app.yaml:1:9: $.server.port: bad",
				"",
				"   1 | server: *server",
				"     |         ^",
				"   2 | servers:",
			), niceyaml.FormatError(err, niceyaml.WithContextLines(1)))

			bound := yamltest.RequireSourceError(t, err)

			assert.Equal(t, server.Child("port"), yamltest.PathOf(t, bound))
		})

		t.Run("a scoped Node binds at an alias below it", func(t *testing.T) {
			t.Parallel()

			scoped := yamltest.At(t, aliased, paths.Doc().Child("base"))

			err := scoped.Bind(yamltest.ErrorAt("bad", "tls", "cert"))
			require.EqualError(t, err, "app.yaml:5:8: $.base.tls.cert: bad")

			bound := yamltest.RequireSourceError(t, err)

			near, ok := bound.Nearest()
			require.True(t, ok)
			assert.Equal(t, paths.Doc().Child("base", "tls"), near)
		})

		t.Run("a binding that wraps one bound at an alias keeps the alias and the reason", func(t *testing.T) {
			t.Parallel()

			inner := aliased.Bind(niceyaml.NewError("bad", niceyaml.AtPath(server.Child("port"))))

			outer := yamltest.RequireSourceError(t, aliased.Bind(niceyaml.NewError("outer", niceyaml.WithDetails(
				fmt.Errorf("check: %w", inner),
			))))
			require.Len(t, yamltest.DetailsOf(outer), 1)

			near, ok := yamltest.DetailsOf(outer)[0].Bound.Nearest()
			require.True(t, ok)
			assert.Equal(t, server, near)
			require.ErrorIs(t, yamltest.DetailsOf(outer)[0].Bound.Unresolved(), paths.ErrAlias)
		})
	})
}

// requiredNames is a value whose servers each need a name, for the test
// of a [niceyaml.SelfValidator] that reports a field the document leaves
// out.
type requiredNames struct {
	Servers []requiredName `yaml:"servers"`
}

// requiredName is a server of [requiredNames].
type requiredName struct {
	Name string `yaml:"name"`
	Port int    `yaml:"port"`
}

// Validate implements [niceyaml.SelfValidator].
func (s requiredName) Validate() error {
	if s.Name == "" {
		return yamltest.ErrorAt("name is required", "name")
	}

	return nil
}

// requireUnresolved asserts that bound resolved no range, and that
// Unresolved reports reason, or nothing when reason is nil.
func requireUnresolved(t *testing.T, bound *niceyaml.SourceError, reason error) {
	t.Helper()

	_, ok := bound.Range()
	require.False(t, ok)

	if reason == nil {
		require.NoError(t, bound.Unresolved())

		return
	}

	require.ErrorIs(t, bound.Unresolved(), reason)
}

func TestError_Format(t *testing.T) {
	t.Parallel()

	err := niceyaml.NewSummary("2 violations",
		yamltest.ErrorAt("bad a", "a"),
		yamltest.ErrorAt("bad b", "b"),
	)

	tcs := map[string]struct {
		format string
		want   string
	}{
		"v prints the message": {
			format: "%v",
			want:   "2 violations",
		},
		"s prints the message": {
			format: "%s",
			want:   "2 violations",
		},
		"q quotes the message": {
			format: "%q",
			want:   `"2 violations"`,
		},
		"plus v prints the tree": {
			format: "%+v",
			want: stringtest.JoinLF(
				"2 violations",
				"|-- @.a: bad a",
				"`-- @.b: bad b",
			),
		},
		"width pads the message on the left": {
			format: "%16s",
			want:   "    2 violations",
		},
		"minus flag pads the message on the right": {
			format: "%-16v",
			want:   "2 violations    ",
		},
		"precision cuts the message": {
			format: "%.5s",
			want:   "2 vio",
		},
		"sharp q quotes with backquotes": {
			format: "%#q",
			want:   "`2 violations`",
		},
		"x prints the message in hex": {
			format: "%x",
			want:   "322076696f6c6174696f6e73",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, fmt.Sprintf(tc.format, err))
		})
	}

	t.Run("plus v matches FormatError", func(t *testing.T) {
		t.Parallel()

		assert.Equal(t, niceyaml.FormatError(err), fmt.Sprintf("%+v", err))
	})

	t.Run("a wrapper prints the message alone", func(t *testing.T) {
		t.Parallel()

		assert.Equal(t, "load: 2 violations", fmt.Errorf("load: %w", err).Error())
	})
}

func TestError_Format_ListLimit(t *testing.T) {
	t.Parallel()

	limit := niceyaml.ErrorListLimit

	// Located returns n errors, each at a key of its own.
	located := func(n int) []error { return keyErrors("k%02d", 0, n) }

	// Rows returns the row of the tree for each of the first n of those
	// errors.
	rows := func(n int) []string {
		out := make([]string, 0, n)
		for i := range n {
			out = append(out, fmt.Sprintf("@.k%02d: bad k%02d", i, i))
		}

		return out
	}

	tcs := map[string]struct {
		err  error
		want []string
		// The number of rows FormatError draws below the root.
		rows int
	}{
		"problems at the limit are whole": {
			err:  niceyaml.NewSummary("summary", located(limit)...),
			want: append([]string{"summary"}, branches(rows(limit)...)...),
			rows: limit,
		},
		"problems past the limit count as more": {
			err:  niceyaml.NewSummary("summary", located(limit+2)...),
			want: append([]string{"summary"}, branches(append(rows(limit), "and 2 more")...)...),
			rows: limit + 2,
		},
		"details of one problem all stay": {
			err:  niceyaml.NewError("outer", niceyaml.WithDetails(located(limit+2)...)),
			want: append([]string{"outer"}, branches(rows(limit+2)...)...),
			rows: limit + 2,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			want := stringtest.JoinLF(tc.want...)

			unbound := yamltest.RequireError(t, tc.err)

			assert.Equal(t, want, fmt.Sprintf("%+v", tc.err))
			assert.Equal(t, want, unbound.LogValue().String())
			assert.Equal(t, want, logged(t, tc.err))

			// FormatError draws every problem, however many there are.
			all := niceyaml.FormatError(tc.err)

			assert.Equal(t, tc.rows, strings.Count(all, ": bad "))
			assert.NotContains(t, all, " more")
		})
	}

	t.Run("binding left out adds no line for its location", func(t *testing.T) {
		t.Parallel()

		input := yamltest.GenerateLines(limit, "k%02d: 0")
		source := niceyaml.NewSourceFromString(input, niceyaml.WithName("f.yaml"))

		// Each of the first limit problems is bound at a key of the
		// source, and the last names a value the source lacks.
		problems := make([]error, 0, limit+1)
		for _, err := range located(limit) {
			problems = append(problems, yamltest.Bind(t, source, err))
		}

		missing := niceyaml.NewError("bad port", niceyaml.AtPath(paths.Current().Child("port").Index(0)))
		err := niceyaml.NewSummary("summary", append(problems, yamltest.Bind(t, source, missing))...)

		const reason = "no excerpt: resolve $.port[0]: not found"

		assert.Contains(t, niceyaml.FormatError(err, niceyaml.WithContextLines(0)), reason)

		got := fmt.Sprintf("%+v", err)

		assert.Contains(t, got, "`-- and 1 more")
		assert.Contains(t, got, "     |      ^ bad k09")
		assert.NotContains(t, got, reason)
	})
}

// logged returns the "err" attribute a JSON handler writes for err.
func logged(t *testing.T, err error) string {
	t.Helper()

	var buf bytes.Buffer

	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	logger.Error("load", slog.Any("err", err))

	var record struct {
		Err string `json:"err"`
	}

	require.NoError(t, json.Unmarshal(buf.Bytes(), &record))

	return record.Err
}

// loggedText returns the record a text handler writes for err.
func loggedText(err error) string {
	var buf bytes.Buffer

	slog.New(slog.NewTextHandler(&buf, nil)).Error("load", slog.Any("err", err))

	return buf.String()
}

func TestError_LogValue(t *testing.T) {
	t.Parallel()

	t.Run("logs the tree of an unbound error", func(t *testing.T) {
		t.Parallel()

		err := niceyaml.NewSummary("2 schema violations",
			yamltest.ErrorAt("bad x", "x"),
			yamltest.ErrorAt("bad y", "y"),
		)

		want := stringtest.JoinLF(
			"2 schema violations",
			"|-- @.x: bad x",
			"`-- @.y: bad y",
		)

		summary := yamltest.RequireError(t, err)

		assert.Equal(t, want, summary.LogValue().String())
		assert.Equal(t, want, logged(t, err))
	})

	t.Run("an error with nothing nested logs its message", func(t *testing.T) {
		t.Parallel()

		err := yamltest.ErrorAt("bad x", "x")

		assert.Equal(t, "@.x: bad x", logged(t, err))
	})

	t.Run("a wrapper around an unbound error logs its message alone", func(t *testing.T) {
		t.Parallel()

		// The wrapper is no LogValuer, so a handler logs the message the
		// wrapper wrote, which holds no path.
		wrapped := fmt.Errorf("check: %w", yamltest.ErrorAt("bad x", "x"))

		assert.Equal(t, "check: bad x", logged(t, wrapped))
		assert.Contains(t, loggedText(wrapped), `err="check: bad x"`)
	})

	t.Run("a join of typed-nil errors logs its message", func(t *testing.T) {
		t.Parallel()

		var nilErr *niceyaml.Error

		err := wrapError(t, errors.Join(nilErr, nilErr))

		assert.Equal(t, "\n", err.LogValue().String())
		assert.Equal(t, niceyaml.FormatError(err), err.LogValue().String())
	})
}

func TestSourceError_LogValue(t *testing.T) {
	t.Parallel()

	source := niceyaml.NewSourceFromString("x: 1\ny: 2\n", niceyaml.WithName("cfg.yaml"))

	t.Run("logs the tree with positions and no excerpt", func(t *testing.T) {
		t.Parallel()

		err := yamltest.Bind(t, source, niceyaml.NewSummary("2 schema violations",
			yamltest.ErrorAt("bad x", "x"),
			yamltest.ErrorAt("bad y", "y"),
		))

		want := stringtest.JoinLF(
			"cfg.yaml: 2 schema violations",
			"|-- 1:4: $.x: bad x",
			"`-- 2:4: $.y: bad y",
		)

		assert.Equal(t, want, logged(t, err))

		text := loggedText(err)

		assert.Contains(t, text, `err="cfg.yaml: 2 schema violations\n|-- 1:4: $.x: bad x\n`)
		assert.Contains(t, text, "`-- 2:4: $.y: bad y\"")
		assert.NotContains(t, text, "   1 | x: 1")
	})

	t.Run("a wrapper logs its own message", func(t *testing.T) {
		t.Parallel()

		err := yamltest.Bind(t, source, yamltest.ErrorAt("bad x", "x"))
		wrapped := fmt.Errorf("load config: %w", err)

		assert.Equal(t, "load config: cfg.yaml:1:4: $.x: bad x", logged(t, wrapped))
	})

	t.Run("a bound join of typed-nil errors logs its message", func(t *testing.T) {
		t.Parallel()

		var nilErr *niceyaml.Error

		err := source.Bind(errors.Join(nilErr, nilErr))
		require.Error(t, err)

		assert.Equal(t, "cfg.yaml: \n", logged(t, err))
		assert.Equal(t, niceyaml.FormatError(err), logged(t, err))
		assert.Contains(t, loggedText(err), `err="cfg.yaml: \n"`)
	})
}

func TestInvalid(t *testing.T) {
	t.Parallel()

	input := stringtest.Input(`
		hours:
		  open: 9
		  close: 5
		license: /root/x
	`)

	hoursPath := paths.Current().Child("hours")
	licensePath := paths.Current().Child("license")
	licenseRange := yamltest.LineRange(3, 9, 16)

	errStat := fmt.Errorf("stat license: %w", fs.ErrPermission)

	doc := yamltest.FirstDocument(t, input, niceyaml.WithName("cfg.yaml"))

	t.Run("nothing to wrap returns nil", func(t *testing.T) {
		t.Parallel()

		var (
			nilErr   *niceyaml.Error
			nilBound *niceyaml.SourceError
		)

		tcs := map[string]struct {
			err error
		}{
			"nil":                     {err: nil},
			"nil Error pointer":       {err: nilErr},
			"nil SourceError pointer": {err: nilBound},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				// NoError compares the interface with nil, and a nil *Error
				// inside it fails that comparison. Unchecked and Locate
				// return nil for the same inputs that Invalid does.
				for constructor, wrap := range map[string]func(error, ...niceyaml.ErrorOption) error{
					"Invalid":   niceyaml.Invalid,
					"Unchecked": niceyaml.Unchecked,
					"Locate":    niceyaml.Locate,
				} {
					require.NoError(t, wrap(tc.err), constructor)
					require.NoError(t, wrap(tc.err,
						niceyaml.AtPath(licensePath),
						niceyaml.AtRange(licenseRange),
						niceyaml.WithDetails(errors.New("named here")),
					), constructor)
				}
			})
		}
	})

	t.Run("wraps the result of a check as it is", func(t *testing.T) {
		t.Parallel()

		tcs := map[string]struct {
			err  error
			want string
		}{
			"valid value": {},
			"invalid value": {
				err:  errors.New("closes before it opens"),
				want: "cfg.yaml:1:1: $.hours: closes before it opens",
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				hours := yamltest.At(t, doc, hoursPath)
				err := hours.Bind(niceyaml.Invalid(tc.err))

				if tc.want == "" {
					require.NoError(t, err)
					assert.False(t, niceyaml.IsInvalid(err))

					return
				}

				require.EqualError(t, err, tc.want)
				assert.True(t, niceyaml.IsInvalid(err))
			})
		}
	})

	t.Run("declares no fault for the error of a context that ended", func(t *testing.T) {
		t.Parallel()

		tcs := map[string]struct {
			err  error
			is   error
			want bool
		}{
			"I/O error": {
				err:  errStat,
				is:   fs.ErrPermission,
				want: true,
			},
			"canceled": {
				err: context.Canceled,
				is:  context.Canceled,
			},
			"deadline behind a wrapper": {
				err: fmt.Errorf("look up name: %w", context.DeadlineExceeded),
				is:  context.DeadlineExceeded,
			},
			"join with a canceled branch": {
				err: errors.Join(errors.New("closes before it opens"), context.Canceled),
				is:  context.Canceled,
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				// The Error takes the options whatever it declares.
				wrapped := wrapError(t, tc.err, niceyaml.AtPath(licensePath))

				assert.Equal(t, licensePath, yamltest.PathOf(t, wrapped))

				require.ErrorIs(t, wrapped, tc.is)
				assert.Equal(t, tc.want, niceyaml.IsInvalid(wrapped), "unbound")
				assert.Equal(t, tc.want, niceyaml.IsInvalid(doc.Bind(wrapped)), "bound")
			})
		}
	})

	t.Run("options on a join locate each branch with no location", func(t *testing.T) {
		t.Parallel()

		closePath := hoursPath.Child("close")
		joined := errors.Join(errStat, errors.New("too many keys"))

		// Each row of wantUnbound is a problem before binding, as
		// locatedLabel writes it, and want holds the lines of the bound
		// error.
		tcs := map[string]struct {
			err         error
			want        string
			wantUnbound []string
		}{
			"path": {
				err: niceyaml.Invalid(joined, niceyaml.AtPath(licensePath)),
				want: stringtest.JoinLF(
					"cfg.yaml:4:10: $.license: stat license: permission denied",
					"cfg.yaml:4:10: $.license: too many keys",
				),
				wantUnbound: []string{
					"@.license: stat license: permission denied",
					"@.license: too many keys",
				},
			},
			"position": {
				err: niceyaml.Invalid(joined, niceyaml.AtPosition(licenseRange.Start)),
				want: stringtest.JoinLF(
					"cfg.yaml:4:10: stat license: permission denied",
					"cfg.yaml:4:10: too many keys",
				),
				wantUnbound: []string{
					"3:9 stat license: permission denied",
					"3:9 too many keys",
				},
			},
			"range": {
				err: niceyaml.Invalid(joined, niceyaml.AtRange(licenseRange)),
				want: stringtest.JoinLF(
					"cfg.yaml:4:10: stat license: permission denied",
					"cfg.yaml:4:10: too many keys",
				),
				wantUnbound: []string{
					"3:9-3:16 stat license: permission denied",
					"3:9-3:16 too many keys",
				},
			},
			"a branch with a location keeps it": {
				err: niceyaml.Invalid(
					errors.Join(errStat, niceyaml.NewError("closes too early", niceyaml.AtPath(closePath))),
					niceyaml.AtPath(licensePath),
				),
				want: stringtest.JoinLF(
					"cfg.yaml:3:10: $.hours.close: closes too early",
					"cfg.yaml:4:10: $.license: stat license: permission denied",
				),
				wantUnbound: []string{
					"@.license: stat license: permission denied",
					"@.hours.close: closes too early",
				},
			},
			"options inside a Rebase win over its base": {
				err: niceyaml.Rebase(
					niceyaml.Invalid(joined, niceyaml.AtPath(paths.Current().Child("close"))),
					hoursPath,
				),
				want: stringtest.JoinLF(
					"cfg.yaml:3:10: $.hours.close: stat license: permission denied",
					"cfg.yaml:3:10: $.hours.close: too many keys",
				),
				wantUnbound: []string{
					"@.hours.close: stat license: permission denied",
					"@.hours.close: too many keys",
				},
			},
			"a Rebase inside the located error wins over the options": {
				err: niceyaml.Invalid(niceyaml.Rebase(joined, licensePath), niceyaml.AtPath(hoursPath)),
				want: stringtest.JoinLF(
					"cfg.yaml:4:10: $.license: stat license: permission denied",
					"cfg.yaml:4:10: $.license: too many keys",
				),
				wantUnbound: []string{
					"@.license: stat license: permission denied",
					"@.license: too many keys",
				},
			},
			"a Rebase between the options and a problem points it at the base": {
				// The summary keeps the location of the options, and the
				// Rebase around it points each of its problems at license.
				err: niceyaml.Invalid(
					niceyaml.Rebase(
						niceyaml.NewSummary("2 problems", errStat, errors.New("too many keys")),
						licensePath,
					),
					niceyaml.AtPath(hoursPath),
				),
				want: stringtest.JoinLF(
					"cfg.yaml:1:1: $.hours: 2 problems",
					"cfg.yaml:4:10: $.license: stat license: permission denied",
					"cfg.yaml:4:10: $.license: too many keys",
				),
				wantUnbound: []string{
					"@.license: stat license: permission denied",
					"@.license: too many keys",
				},
			},
			"a branch from BindValue takes the location as the error it was made from": {
				err: niceyaml.Invalid(
					errors.Join(niceyaml.BindValue(errors.New("too many keys")), errStat),
					niceyaml.AtPath(licensePath),
				),
				want: stringtest.JoinLF(
					"cfg.yaml:4:10: $.license: too many keys",
					"cfg.yaml:4:10: $.license: stat license: permission denied",
				),
				wantUnbound: []string{
					"@.license: too many keys",
					"@.license: stat license: permission denied",
				},
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				var unbound []string

				for problem := range niceyaml.Problems(tc.err) {
					unbound = append(unbound, locatedLabel(problem))
				}

				assert.Equal(t, tc.wantUnbound, unbound)

				err := doc.Bind(tc.err)

				require.EqualError(t, err, tc.want)
				assert.True(t, niceyaml.IsInvalid(err))
			})
		}
	})

	t.Run("the nearest heading with a location wins", func(t *testing.T) {
		t.Parallel()

		closePath := hoursPath.Child("close")

		// The summary carries no location, so it takes the location of the
		// join and gives it to its problems. The Locate below the join
		// carries its own.
		err := doc.Bind(niceyaml.Invalid(errors.Join(
			niceyaml.NewSummary("2 hours problems", errors.New("closes too early"), errors.New("opens too late")),
			niceyaml.Locate(errors.Join(errStat, errors.New("too many keys")), niceyaml.AtPath(licensePath)),
		), niceyaml.AtPath(closePath)))

		require.EqualError(t, err, stringtest.JoinLF(
			"cfg.yaml:3:10: $.hours.close: 2 hours problems",
			"cfg.yaml:3:10: $.hours.close: closes too early",
			"cfg.yaml:3:10: $.hours.close: opens too late",
			"cfg.yaml:4:10: $.license: stat license: permission denied",
			"cfg.yaml:4:10: $.license: too many keys",
		))
	})

	t.Run("a detail gains no location from a heading", func(t *testing.T) {
		t.Parallel()

		err := doc.Bind(niceyaml.Invalid(errors.Join(
			niceyaml.NewError("closes too early", niceyaml.WithDetails(errors.New("the shop closes at 17:00"))),
			errors.New("opens too late"),
		), niceyaml.AtPath(hoursPath.Child("close"))))

		problem := slices.Collect(niceyaml.Problems(err))[0]
		assert.Equal(t, "closes too early", problem.Message())
		assert.Equal(t, yamltest.LineRange(2, 9, 10).Start, yamltest.PositionOf(t, problem))

		require.Len(t, problem.Children, 1)

		_, ok := problem.Children[0].Position()
		assert.False(t, ok)

		_, ok = problem.Children[0].Path()
		assert.False(t, ok)
	})

	t.Run("a scoped Bind reads the location of the options from its node", func(t *testing.T) {
		t.Parallel()

		hours := yamltest.At(t, doc, paths.Doc().Child("hours"))

		err := hours.Bind(niceyaml.Invalid(
			errors.Join(errStat, errors.New("too many keys")),
			niceyaml.AtPath(paths.Current().Child("close")),
		))

		require.EqualError(t, err, stringtest.JoinLF(
			"cfg.yaml:3:10: $.hours.close: stat license: permission denied",
			"cfg.yaml:3:10: $.hours.close: too many keys",
		))
	})
}

// locatedLabel returns the label of a problem bound to no source, behind
// the range or the position that its [niceyaml.Error] carries, if any.
func locatedLabel(problem niceyaml.ErrorTree) string {
	x, ok := errors.AsType[*niceyaml.Error](problem.Err)
	if !ok {
		return problem.Label
	}

	if r, ok := x.Range(); ok {
		return r.String() + " " + problem.Label
	}

	if pos, ok := x.Position(); ok {
		return pos.String() + " " + problem.Label
	}

	return problem.Label
}

func TestUnchecked(t *testing.T) {
	t.Parallel()

	input := stringtest.Input(`
		hours:
		  open: 9
		  close: 5
		license: /root/x
	`)

	hoursPath := paths.Current().Child("hours")
	openPath := hoursPath.Child("open")
	licensePath := paths.Current().Child("license")
	licenseRange := yamltest.LineRange(3, 9, 16)

	errStat := fmt.Errorf("stat license: %w", fs.ErrPermission)

	doc := yamltest.FirstDocument(t, input, niceyaml.WithName("cfg.yaml"))

	t.Run("places as Invalid does and declares no fault", func(t *testing.T) {
		t.Parallel()

		// One I/O error at one path prints the same line from each
		// wrapper, and only Invalid declares the document at fault.
		tcs := map[string]struct {
			err  error
			want bool
		}{
			"Invalid": {
				err:  niceyaml.Invalid(errStat, niceyaml.AtPath(licensePath)),
				want: true,
			},
			"Unchecked": {
				err: niceyaml.Unchecked(errStat, niceyaml.AtPath(licensePath)),
			},
			"Rebase": {
				err: niceyaml.Rebase(errStat, licensePath),
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				err := doc.Bind(tc.err)

				require.EqualError(t, err, "cfg.yaml:4:10: $.license: stat license: permission denied")
				require.ErrorIs(t, err, fs.ErrPermission)
				assert.Equal(t, tc.want, niceyaml.IsInvalid(tc.err), "unbound")
				assert.Equal(t, tc.want, niceyaml.IsInvalid(err), "bound")
			})
		}
	})

	t.Run("takes every option", func(t *testing.T) {
		t.Parallel()

		why := errors.New("the license check reads this file")

		tcs := map[string]struct {
			opts []niceyaml.ErrorOption
			want string
		}{
			"no option": {
				want: "cfg.yaml: stat license: permission denied",
			},
			"path": {
				opts: []niceyaml.ErrorOption{niceyaml.AtPath(licensePath)},
				want: "cfg.yaml:4:10: $.license: stat license: permission denied",
			},
			"position": {
				opts: []niceyaml.ErrorOption{niceyaml.AtPosition(licenseRange.Start)},
				want: "cfg.yaml:4:10: stat license: permission denied",
			},
			"range": {
				opts: []niceyaml.ErrorOption{niceyaml.AtRange(licenseRange)},
				want: "cfg.yaml:4:10: stat license: permission denied",
			},
			"path and range": {
				opts: []niceyaml.ErrorOption{niceyaml.AtPath(licensePath), niceyaml.AtRange(licenseRange)},
				want: "cfg.yaml:4:10: $.license: stat license: permission denied",
			},
			"range and details": {
				opts: []niceyaml.ErrorOption{niceyaml.AtRange(licenseRange), niceyaml.WithDetails(why)},
				want: "cfg.yaml:4:10: stat license: permission denied",
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				unchecked := niceyaml.Unchecked(errStat, tc.opts...)
				want := wrapError(t, errStat, tc.opts...)

				// The Error carries what the same options give Invalid.
				got := yamltest.RequireError(t, unchecked)

				assert.Same(t, errStat, got.Unwrap()[0])
				assert.Equal(t, yamltest.ErrsOf(yamltest.DetailsOf(want)), yamltest.ErrsOf(yamltest.DetailsOf(got)))

				assert.Equal(t, locationOf(t, want), locationOf(t, got))

				err := doc.Bind(unchecked)

				require.EqualError(t, err, tc.want)
				assert.False(t, niceyaml.IsInvalid(unchecked), "unbound")
				assert.False(t, niceyaml.IsInvalid(err), "bound")
			})
		}
	})

	t.Run("declares no fault and clears none", func(t *testing.T) {
		t.Parallel()

		badOpen := niceyaml.NewError("opens too late", niceyaml.AtPath(openPath))

		tcs := map[string]struct {
			err  error
			want bool
		}{
			"I/O error with a located detail from NewError": {
				// The detail explains the I/O error and decides nothing.
				err: niceyaml.Unchecked(errStat,
					niceyaml.AtRange(licenseRange),
					niceyaml.WithDetails(niceyaml.NewError("named here", niceyaml.AtPath(openPath))),
				),
			},
			"NewError": {
				err:  niceyaml.Unchecked(badOpen, niceyaml.AtRange(licenseRange)),
				want: true,
			},
			"fmt wrapper around NewError": {
				err:  niceyaml.Unchecked(fmt.Errorf("check hours: %w", badOpen), niceyaml.AtRange(licenseRange)),
				want: true,
			},
			"Invalid of an I/O error": {
				err:  niceyaml.Unchecked(niceyaml.Invalid(errStat), niceyaml.AtRange(licenseRange)),
				want: true,
			},
			"Invalid around Unchecked": {
				err:  niceyaml.Invalid(niceyaml.Unchecked(errStat, niceyaml.AtRange(licenseRange))),
				want: true,
			},
			"Rebase around Unchecked": {
				err: niceyaml.Rebase(
					niceyaml.Unchecked(errStat, niceyaml.AtPath(paths.Current().Child("open"))),
					hoursPath,
				),
			},
			"canceled context": {
				err: niceyaml.Unchecked(context.Canceled, niceyaml.AtRange(licenseRange)),
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				assert.Equal(t, tc.want, niceyaml.IsInvalid(tc.err), "unbound")
				assert.Equal(t, tc.want, niceyaml.IsInvalid(doc.Bind(tc.err)), "bound")
			})
		}
	})

	t.Run("a scoped Node binds an unchecked error at its value", func(t *testing.T) {
		t.Parallel()

		hours := yamltest.At(t, doc, hoursPath)

		tcs := map[string]struct {
			err  error
			want string
		}{
			"no location": {
				err:  niceyaml.Unchecked(errStat),
				want: "cfg.yaml:1:1: $.hours: stat license: permission denied",
			},
			"path from the value": {
				err:  niceyaml.Unchecked(errStat, niceyaml.AtPath(paths.Current().Child("close"))),
				want: "cfg.yaml:3:10: $.hours.close: stat license: permission denied",
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				err := hours.Bind(tc.err)

				require.EqualError(t, err, tc.want)
				assert.False(t, niceyaml.IsInvalid(err))
			})
		}
	})

	t.Run("a hook presumes no fault for an unchecked error", func(t *testing.T) {
		t.Parallel()

		// Each decode runs a hook that wraps errLicense in Unchecked, so
		// the error keeps its location and matches no decode error.
		tcs := map[string]struct {
			decode func(ctx context.Context) error
			want   string
		}{
			"Validate method": {
				decode: func(ctx context.Context) error {
					return doc.DecodeInto(ctx, &checkedConfig{Hours: selfChecked{check: func() error {
						return niceyaml.Unchecked(errLicense, niceyaml.AtPath(paths.Current().Child("close")))
					}}})
				},
				want: "cfg.yaml:3:10: $.hours.close: stat license: permission denied",
			},
			"WithValueCheck function": {
				decode: func(ctx context.Context) error {
					return errOf(doc.Decode[struct {
						License string `yaml:"license"`
					}](ctx, niceyaml.WithValueCheck(func(context.Context, *string) error {
						return niceyaml.Unchecked(errLicense)
					})))
				},
				want: "cfg.yaml:4:10: $.license: stat license: permission denied",
			},
			"UnmarshalText method": {
				decode: func(ctx context.Context) error {
					return errOf(doc.Decode[struct {
						License uncheckedText `yaml:"license"`
					}](ctx))
				},
				want: "cfg.yaml:4:10: $.license: stat license: permission denied",
			},
			"UnmarshalYAML method": {
				decode: func(ctx context.Context) error {
					return errOf(doc.Decode[struct {
						License uncheckedUnmarshaler `yaml:"license"`
					}](ctx))
				},
				want: "cfg.yaml:4:10: $.license: stat license: permission denied",
			},
			"CustomUnmarshaler function": {
				decode: func(ctx context.Context) error {
					return errOf(doc.Decode[struct {
						License licenseRef `yaml:"license"`
					}](ctx, withLicenseCheck(func(func(any) error) error {
						return niceyaml.Unchecked(errLicense)
					})))
				},
				want: "cfg.yaml:4:10: $.license: stat license: permission denied",
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				err := tc.decode(t.Context())

				require.EqualError(t, err, tc.want)
				require.ErrorIs(t, err, fs.ErrPermission)
				require.NotErrorIs(t, err, niceyaml.ErrDecode)
				assert.False(t, niceyaml.IsInvalid(err))
			})
		}
	})

	t.Run("each problem from a hook answers for itself", func(t *testing.T) {
		t.Parallel()

		closePath := paths.Current().Child("close")

		// Each row of problems is the text of a problem and what IsInvalid
		// reports for it, in the order of the lines they bind at.
		tcs := map[string]struct {
			check    func() error
			problems []string
		}{
			"Invalid around Unchecked": {
				check: func() error {
					return niceyaml.Invalid(niceyaml.Unchecked(errStat, niceyaml.AtPath(closePath)))
				},
				problems: []string{"cfg.yaml:3:10: $.hours.close: stat license: permission denied true"},
			},
			"Unchecked around NewError": {
				check: func() error {
					return niceyaml.Unchecked(niceyaml.NewError("closes before it opens"), niceyaml.AtPath(closePath))
				},
				problems: []string{"cfg.yaml:3:10: $.hours.close: closes before it opens true"},
			},
			"Unchecked around a join that holds NewError": {
				// The Unchecked heads each branch, and the branch from NewError
				// declares its own fault.
				check: func() error {
					return niceyaml.Unchecked(errors.Join(
						niceyaml.NewError("closes before it opens", niceyaml.AtPath(closePath)),
						errStat,
					))
				},
				problems: []string{
					"cfg.yaml:1:1: $.hours: stat license: permission denied false",
					"cfg.yaml:3:10: $.hours.close: closes before it opens true",
				},
			},
			"join of Unchecked and a plain error": {
				// The Unchecked heads one branch, and the decode presumes the
				// other branch a fault.
				check: func() error {
					return errors.Join(
						niceyaml.Unchecked(errStat, niceyaml.AtPath(closePath)),
						errors.New("opens too late"),
					)
				},
				problems: []string{
					"cfg.yaml:1:1: $.hours: opens too late true",
					"cfg.yaml:3:10: $.hours.close: stat license: permission denied false",
				},
			},
			"summary of Unchecked and a plain error": {
				// The heading names the file, so the rows below it do not.
				check: func() error {
					return niceyaml.NewSummary("2 problems",
						niceyaml.Unchecked(errStat, niceyaml.AtPath(closePath)),
						errors.New("opens too late"),
					)
				},
				problems: []string{
					"1:1: $.hours: opens too late true",
					"3:10: $.hours.close: stat license: permission denied false",
				},
			},
			"Invalid around a join that holds Unchecked": {
				check: func() error {
					return niceyaml.Invalid(errors.Join(
						niceyaml.Unchecked(errStat, niceyaml.AtPath(closePath)),
						errors.New("opens too late"),
					))
				},
				problems: []string{
					"cfg.yaml:1:1: $.hours: opens too late true",
					"cfg.yaml:3:10: $.hours.close: stat license: permission denied true",
				},
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				err := doc.DecodeInto(t.Context(), &checkedConfig{Hours: selfChecked{check: tc.check}})
				require.Error(t, err)

				var problems []string

				for problem := range niceyaml.NewErrorTree(err).Problems() {
					problems = append(problems, fmt.Sprintf("%s %t", problem.Label, problem.IsInvalid()))
				}

				assert.Equal(t, tc.problems, problems)
			})
		}
	})

	t.Run("options on a join locate each branch with no location", func(t *testing.T) {
		t.Parallel()

		// The path of the located branch starts at `$`, so Rebase leaves
		// it as it is.
		joined := errors.Join(
			niceyaml.NewError("opens too late", niceyaml.AtPath(paths.Doc().Join(openPath))),
			errStat,
		)

		// Each row of problems is the text of a problem and what Invalid
		// reports for it.
		tcs := map[string]struct {
			err      error
			want     string
			problems []string
		}{
			"no option": {
				err: niceyaml.Unchecked(joined),
				want: stringtest.JoinLF(
					"cfg.yaml:2:9: $.hours.open: opens too late",
					"cfg.yaml: stat license: permission denied",
				),
				problems: []string{
					"cfg.yaml:2:9: $.hours.open: opens too late true",
					"cfg.yaml: stat license: permission denied false",
				},
			},
			"path": {
				err: niceyaml.Unchecked(joined, niceyaml.AtPath(licensePath)),
				want: stringtest.JoinLF(
					"cfg.yaml:2:9: $.hours.open: opens too late",
					"cfg.yaml:4:10: $.license: stat license: permission denied",
				),
				problems: []string{
					"cfg.yaml:2:9: $.hours.open: opens too late true",
					"cfg.yaml:4:10: $.license: stat license: permission denied false",
				},
			},
			"range": {
				err: niceyaml.Unchecked(joined, niceyaml.AtRange(licenseRange)),
				want: stringtest.JoinLF(
					"cfg.yaml:2:9: $.hours.open: opens too late",
					"cfg.yaml:4:10: stat license: permission denied",
				),
				problems: []string{
					"cfg.yaml:2:9: $.hours.open: opens too late true",
					"cfg.yaml:4:10: stat license: permission denied false",
				},
			},
			"Rebase moves each branch under the path": {
				err: niceyaml.Rebase(joined, licensePath),
				want: stringtest.JoinLF(
					"cfg.yaml:2:9: $.hours.open: opens too late",
					"cfg.yaml:4:10: $.license: stat license: permission denied",
				),
				problems: []string{
					"cfg.yaml:2:9: $.hours.open: opens too late true",
					"cfg.yaml:4:10: $.license: stat license: permission denied false",
				},
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				err := doc.Bind(tc.err)

				require.EqualError(t, err, tc.want)
				assert.False(t, niceyaml.IsInvalid(err))

				var problems []string

				for problem := range niceyaml.NewErrorTree(err).Problems() {
					problems = append(problems, fmt.Sprintf("%s %t", problem.Label, problem.IsInvalid()))
				}

				assert.Equal(t, tc.problems, problems)
			})
		}
	})
}

func TestLocate(t *testing.T) {
	t.Parallel()

	input := stringtest.Input(`
		hours:
		  open: 9
		  close: 5
		license: /root/x
	`)

	closePath := paths.Current().Child("close")
	licensePath := paths.Current().Child("license")
	licenseRange := yamltest.LineRange(3, 9, 16)

	errStat := fmt.Errorf("stat license: %w", fs.ErrPermission)

	doc := yamltest.FirstDocument(t, input, niceyaml.WithName("cfg.yaml"))

	t.Run("places as Invalid does and leaves the fault to the error", func(t *testing.T) {
		t.Parallel()

		// One I/O error at one path prints the same line from each
		// wrapper, and Locate adds no fault of its own.
		tcs := map[string]struct {
			err  error
			want bool
		}{
			"I/O error": {
				err: niceyaml.Locate(errStat, niceyaml.AtPath(licensePath)),
			},
			"Unchecked": {
				err: niceyaml.Locate(niceyaml.Unchecked(errStat), niceyaml.AtPath(licensePath)),
			},
			"Invalid": {
				err:  niceyaml.Locate(niceyaml.Invalid(errStat), niceyaml.AtPath(licensePath)),
				want: true,
			},
			"Invalid around Locate": {
				err:  niceyaml.Invalid(niceyaml.Locate(errStat, niceyaml.AtPath(licensePath))),
				want: true,
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				err := doc.Bind(tc.err)

				require.EqualError(t, err, "cfg.yaml:4:10: $.license: stat license: permission denied")
				require.ErrorIs(t, err, fs.ErrPermission)
				assert.Equal(t, tc.want, niceyaml.IsInvalid(tc.err), "unbound")
				assert.Equal(t, tc.want, niceyaml.IsInvalid(err), "bound")
			})
		}
	})

	t.Run("takes every option", func(t *testing.T) {
		t.Parallel()

		why := errors.New("the license check reads this file")

		tcs := map[string]struct {
			opts []niceyaml.ErrorOption
			want string
		}{
			"no option": {
				want: "cfg.yaml: stat license: permission denied",
			},
			"path": {
				opts: []niceyaml.ErrorOption{niceyaml.AtPath(licensePath)},
				want: "cfg.yaml:4:10: $.license: stat license: permission denied",
			},
			"position": {
				opts: []niceyaml.ErrorOption{niceyaml.AtPosition(licenseRange.Start)},
				want: "cfg.yaml:4:10: stat license: permission denied",
			},
			"range": {
				opts: []niceyaml.ErrorOption{niceyaml.AtRange(licenseRange)},
				want: "cfg.yaml:4:10: stat license: permission denied",
			},
			"range and details": {
				opts: []niceyaml.ErrorOption{niceyaml.AtRange(licenseRange), niceyaml.WithDetails(why)},
				want: "cfg.yaml:4:10: stat license: permission denied",
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				located := niceyaml.Locate(errStat, tc.opts...)
				want := wrapError(t, errStat, tc.opts...)

				// The Error carries what the same options give Invalid.
				got := yamltest.RequireError(t, located)

				assert.Same(t, errStat, got.Unwrap()[0])
				assert.Equal(t, yamltest.ErrsOf(yamltest.DetailsOf(want)), yamltest.ErrsOf(yamltest.DetailsOf(got)))

				assert.Equal(t, locationOf(t, want), locationOf(t, got))

				err := doc.Bind(located)

				require.EqualError(t, err, tc.want)
				assert.False(t, niceyaml.IsInvalid(located), "unbound")
				assert.False(t, niceyaml.IsInvalid(err), "bound")
			})
		}
	})

	t.Run("a hook presumes a fault for a located error", func(t *testing.T) {
		t.Parallel()

		// Each decode runs a hook that returns errLicense inside Locate,
		// which leaves the presumption of the hook on.
		tcs := map[string]struct {
			decode func(ctx context.Context) error
			want   string
			// The hook decodes its value, so the error matches ErrDecode.
			decoding bool
		}{
			"Validate method": {
				decode: func(ctx context.Context) error {
					return doc.DecodeInto(ctx, &checkedConfig{Hours: selfChecked{check: func() error {
						return niceyaml.Locate(errLicense, niceyaml.AtPath(closePath))
					}}})
				},
				want: "cfg.yaml:3:10: $.hours.close: stat license: permission denied",
			},
			"WithValueCheck function": {
				decode: func(ctx context.Context) error {
					return errOf(doc.Decode[struct {
						License string `yaml:"license"`
					}](ctx, niceyaml.WithValueCheck(func(context.Context, *string) error {
						return niceyaml.Locate(errLicense, niceyaml.AtPath(paths.Current()))
					})))
				},
				want: "cfg.yaml:4:10: $.license: stat license: permission denied",
			},
			"CustomUnmarshaler function": {
				decode: func(ctx context.Context) error {
					return errOf(doc.Decode[struct {
						License licenseRef `yaml:"license"`
					}](ctx, withLicenseCheck(func(func(any) error) error {
						return niceyaml.Locate(errLicense)
					})))
				},
				want:     "cfg.yaml:4:10: $.license: stat license: permission denied",
				decoding: true,
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				err := tc.decode(t.Context())

				require.EqualError(t, err, tc.want)
				require.ErrorIs(t, err, fs.ErrPermission)
				assert.True(t, niceyaml.IsInvalid(err))

				if tc.decoding {
					require.ErrorIs(t, err, niceyaml.ErrDecode)
				} else {
					require.NotErrorIs(t, err, niceyaml.ErrDecode)
				}
			})
		}
	})

	t.Run("each problem from a hook answers for itself", func(t *testing.T) {
		t.Parallel()

		openPath := paths.Current().Child("open")

		// Each hook decodes the document and returns the error of check
		// from the value under hours.
		hooks := map[string]func(ctx context.Context, check func() error) error{
			"Validate method": func(ctx context.Context, check func() error) error {
				return doc.DecodeInto(ctx, &checkedConfig{Hours: selfChecked{check: check}})
			},
			"WithValueCheck function": func(ctx context.Context, check func() error) error {
				passes := selfChecked{check: func() error { return nil }}

				return doc.DecodeInto(ctx, &checkedConfig{Hours: passes},
					niceyaml.WithValueCheck(func(context.Context, *selfChecked) error {
						return check()
					}))
			},
		}

		// Each row of problems is the text of a problem and what IsInvalid
		// reports for it, in the order of the lines they bind at.
		tcs := map[string]struct {
			check    func() error
			problems []string
		}{
			"Locate around a join of a plain error and Unchecked": {
				// The options locate the plain branch, and the hook presumes
				// a fault for that branch alone.
				check: func() error {
					return niceyaml.Locate(errors.Join(
						errors.New("opens too late"),
						niceyaml.Unchecked(errStat, niceyaml.AtPath(closePath)),
					), niceyaml.AtPath(openPath))
				},
				problems: []string{
					"cfg.yaml:2:9: $.hours.open: opens too late true",
					"cfg.yaml:3:10: $.hours.close: stat license: permission denied false",
				},
			},
			"join of Locate and Unchecked": {
				check: func() error {
					return errors.Join(
						niceyaml.Locate(errors.New("opens too late"), niceyaml.AtPath(openPath)),
						niceyaml.Unchecked(errStat, niceyaml.AtPath(closePath)),
					)
				},
				problems: []string{
					"cfg.yaml:2:9: $.hours.open: opens too late true",
					"cfg.yaml:3:10: $.hours.close: stat license: permission denied false",
				},
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				for hook, decode := range hooks {
					err := decode(t.Context(), tc.check)
					require.Error(t, err, hook)

					var problems []string

					for problem := range niceyaml.NewErrorTree(err).Problems() {
						problems = append(problems, fmt.Sprintf("%s %t", problem.Label, problem.IsInvalid()))
					}

					assert.Equal(t, tc.problems, problems, hook)
				}
			})
		}
	})
}

func TestError_Is(t *testing.T) {
	t.Parallel()

	located := yamltest.ErrorAt("unknown name", "name")

	var nilErr *niceyaml.Error

	// The mark an Error declares the fault with is internal, so
	// [TestIsInvalid] reads it. The method matches no target a caller can
	// name, and any other target matches through the error the Error
	// wraps.
	require.NotErrorIs(t, located, niceyaml.ErrSyntax)
	require.NotErrorIs(t, located, niceyaml.ErrDecode)
	require.NotErrorIs(t, nilErr, niceyaml.ErrSyntax)
	require.ErrorIs(t, niceyaml.Invalid(fs.ErrNotExist), fs.ErrNotExist)
}

// selfChecked is a value whose Validate returns what check returns, so a
// test picks the error a [niceyaml.SelfValidator] reports. A decode
// leaves check as it was, since go-yaml sets no unexported field.
type selfChecked struct {
	check func() error
}

func (s selfChecked) Validate() error {
	return s.check()
}

// checkedConfig holds a [selfChecked] under hours, so the errors of its
// Validate point under $.hours.
type checkedConfig struct {
	Hours selfChecked `yaml:"hours"`
}

// fetchingUnmarshaler decodes itself from a file that does not exist, so
// its decode fails with an I/O error that is no fault of the document.
type fetchingUnmarshaler struct{}

func (*fetchingUnmarshaler) UnmarshalYAML([]byte) error {
	return &fs.PathError{Op: "open", Path: "include.yaml", Err: fs.ErrNotExist}
}

// hintedUnmarshaler rejects its value with an error whose detail wraps
// the error of a context that ended, as a suggestion that took too long
// to look up leaves one.
type hintedUnmarshaler struct{}

func (*hintedUnmarshaler) UnmarshalYAML([]byte) error {
	return niceyaml.Invalid(
		errors.New("unknown name"),
		niceyaml.WithDetails(fmt.Errorf("look up a suggestion: %w", context.DeadlineExceeded)),
	)
}

// uncheckedText decodes itself from text with a check that cannot run, so
// it returns errLicense inside [niceyaml.Unchecked].
type uncheckedText string

func (*uncheckedText) UnmarshalText([]byte) error {
	return niceyaml.Unchecked(errLicense)
}

// uncheckedUnmarshaler decodes itself with a check that cannot run, so it
// returns errLicense inside [niceyaml.Unchecked].
type uncheckedUnmarshaler struct{}

func (*uncheckedUnmarshaler) UnmarshalYAML([]byte) error {
	return niceyaml.Unchecked(errLicense)
}

// licenseRef is a value that a [niceyaml.CustomUnmarshaler] function
// decodes, so each test picks the error its decode returns.
type licenseRef string

// withLicenseCheck returns an option that decodes every [licenseRef] with
// a function that returns what check returns. Check gets the decode
// function of the value, so its error can wrap a rejection of that
// function.
func withLicenseCheck(check func(decode func(any) error) error) niceyaml.DecodeOption {
	return niceyaml.WithUnmarshaler(
		niceyaml.CustomUnmarshaler(func(_ context.Context, _ *licenseRef, decode func(any) error) error {
			return check(decode)
		}),
	)
}

// invalidConfig is a target the decodes of [TestIsInvalid] reject.
type invalidConfig struct {
	Name string `yaml:"name"`
	Port int    `yaml:"port"`
}

func TestIsInvalid(t *testing.T) {
	t.Parallel()

	input := stringtest.Input(`
		name: cafe
		port: 80
		hours:
		  open: 9
		  close: 5
	`)

	namePath := paths.Current().Child("name")
	closePath := paths.Current().Child("close")
	hoursPath := paths.Current().Child("hours")

	ioErr := &fs.PathError{Op: "open", Path: "names.db", Err: fs.ErrNotExist}
	portSchema := schema.MustCompile([]byte(`{"properties": {"port": {"maximum": 10}}}`))

	// Fails returns a validator that reports err for any node.
	fails := func(err error) niceyaml.Validator {
		return niceyaml.ValidatorFunc(func(context.Context, *niceyaml.Node) error { return err })
	}

	unknownName := fails(yamltest.ErrorAt("unknown name", "name"))
	failing := fails(ioErr)
	unlocated := fails(errors.New("closes before it opens"))
	declared := fails(niceyaml.NewError("closes before it opens"))
	joined := fails(niceyaml.Invalid(errors.Join(errors.New("opens too late"), errors.New("closes too early"))))
	rebased := fails(niceyaml.Rebase(fmt.Errorf("stat license: %w", fs.ErrPermission), namePath))
	contextual := niceyaml.ValidatorFunc(func(ctx context.Context, _ *niceyaml.Node) error {
		return ctx.Err()
	})
	denied := fails(fmt.Errorf("read names.db: %w", fs.ErrPermission))
	decoding := niceyaml.ValidatorFunc(func(ctx context.Context, n *niceyaml.Node) error {
		var target struct {
			Port []int `yaml:"port"`
		}

		return n.DecodeInto(ctx, &target)
	})

	unchecked := fails(niceyaml.Unchecked(fmt.Errorf("stat license: %w", fs.ErrPermission), niceyaml.AtPath(namePath)))

	// The explained error is an I/O error placed at the name, with a
	// located detail the document is at fault for.
	explained := niceyaml.Unchecked(
		fmt.Errorf("stat license: %w", fs.ErrPermission),
		niceyaml.AtRange(yamltest.LineRange(0, 6, 10)),
		niceyaml.WithDetails(yamltest.ErrorAt("license named here", "name")),
	)

	canceled, cancel := context.WithCancel(t.Context())
	cancel()

	// The document names its schema in fsys, which holds the files of the
	// case.
	directive := func(fsys fs.FS, ref string) *niceyaml.Node {
		return yamltest.FirstDocument(t,
			yamltest.SchemaDirective(ref, "name: cafe\n"),
			niceyaml.WithFilePath("c.yaml"), niceyaml.WithFS(fsys),
		)
	}

	byDirective := schema.NewRegistry(schema.WithResolvers(schema.Directive()))

	doc := yamltest.FirstDocument(t, input)
	hours := yamltest.At(t, doc, hoursPath)

	// Each of the four helpers below returns the error of one call.
	validated := func(v niceyaml.Validator) error {
		return doc.Validate(t.Context(), v)
	}

	scopedValidated := func(v niceyaml.Validator) error {
		return hours.Validate(t.Context(), v)
	}

	selfValidated := func(err error) error {
		cfg := checkedConfig{Hours: selfChecked{check: func() error { return err }}}

		return doc.DecodeInto(t.Context(), &cfg)
	}

	valueChecked := func(err error) error {
		check := niceyaml.WithValueCheck(func(context.Context, *invalidConfig) error { return err })

		return errOf(doc.Decode[invalidConfig](t.Context(), check))
	}

	decoded := func(text string, opts ...niceyaml.DecodeOption) error {
		return errOf(niceyaml.NewSourceFromString(text).Decode[invalidConfig](t.Context(), opts...))
	}

	// The errors of boundRead and boundPlain are no fault of the document,
	// and each is bound at a value already.
	boundRead := doc.Bind(niceyaml.Rebase(ioErr, namePath))
	boundPlain := hours.Bind(errors.New("closes before it opens"))

	// Each case holds the error of one origin. The is field pins the
	// origin, so a case that stops reaching it fails rather than passing
	// on another error.
	tcs := map[string]struct {
		err  error
		is   error
		want bool
	}{
		"syntax error": {
			err:  decoded("name: [cafe\nport: 80\n"),
			is:   niceyaml.ErrSyntax,
			want: true,
		},
		"decode type mismatch": {
			err:  decoded("name: cafe\nport: eighty\n"),
			is:   niceyaml.ErrDecode,
			want: true,
		},
		"unknown field": {
			err:  decoded("name: cafe\nprot: 80\n", niceyaml.WithUnknownFields(niceyaml.UnknownFieldsReject)),
			is:   niceyaml.ErrDecode,
			want: true,
		},
		"schema violation": {
			err:  validated(portSchema),
			want: true,
		},
		"schema violation with no location": {
			err:  validated(fails(&schema.Violation{Keyword: "type", Message: `expected "object", got "string"`})),
			want: true,
		},
		"SelfValidator NewError with AtPath": {
			err:  selfValidated(yamltest.ErrorAt("closes before it opens", "close")),
			want: true,
		},
		"SelfValidator errors.New on a field": {
			err:  selfValidated(errors.New("closes before it opens")),
			want: true,
		},
		"SelfValidator errors.New at the root": {
			err: doc.DecodeInto(t.Context(), &selfChecked{check: func() error {
				return errors.New("closes before it opens")
			}}),
			want: true,
		},
		"SelfValidator summary of errors with no location": {
			err: selfValidated(niceyaml.NewSummary(
				"2 problems",
				errors.New("opens too late"),
				errors.New("closes too early"),
			)),
			want: true,
		},
		"SelfValidator join of errors with no location": {
			err:  selfValidated(errors.Join(errors.New("opens too late"), errors.New("closes too early"))),
			want: true,
		},
		"SelfValidator I/O error": {
			err:  selfValidated(fmt.Errorf("load holidays: %w", ioErr)),
			is:   fs.ErrNotExist,
			want: true,
		},
		"SelfValidator bound I/O error": {
			// A binding is an error a SelfValidator returns as any other
			// is.
			err:  selfValidated(boundRead),
			is:   fs.ErrNotExist,
			want: true,
		},
		"SelfValidator join of bound errors": {
			err:  selfValidated(errors.Join(boundRead, boundPlain)),
			is:   fs.ErrNotExist,
			want: true,
		},
		"Validator NewError with AtPath": {
			err:  validated(unknownName),
			want: true,
		},
		"Validator NewError with AtPath of the root": {
			err:  scopedValidated(fails(niceyaml.NewError("closes before it opens", niceyaml.AtPath(paths.Current())))),
			want: true,
		},
		"Validator NewError with AtPath bound through Bind": {
			err: scopedValidated(niceyaml.ValidatorFunc(func(_ context.Context, n *niceyaml.Node) error {
				return n.Bind(yamltest.ErrorAt("closes before it opens", "close"))
			})),
			want: true,
		},
		"Validator NewError with no location at the root": {
			err:  validated(declared),
			want: true,
		},
		"Validator NewError with no location on a scoped Node": {
			err:  scopedValidated(declared),
			want: true,
		},
		"Validator Invalid of a join at the root": {
			err:  validated(joined),
			want: true,
		},
		"Validator Invalid of a join on a scoped Node": {
			err:  scopedValidated(joined),
			want: true,
		},
		"Validator join of errors.New": {
			err: validated(fails(errors.Join(errors.New("opens too late"), errors.New("closes too early")))),
		},
		"Validator errors.New at the root": {
			err: validated(unlocated),
		},
		"Validator errors.New on a scoped Node": {
			err: scopedValidated(unlocated),
		},
		"Validator Bind of errors.New on a scoped Node": {
			err: scopedValidated(niceyaml.ValidatorFunc(func(_ context.Context, n *niceyaml.Node) error {
				return n.Bind(errors.New("closes before it opens"))
			})),
		},
		"Validator Rebase of an I/O error": {
			err: validated(rebased),
			is:  fs.ErrPermission,
		},
		"Validator Unchecked of an I/O error at the root": {
			err: validated(unchecked),
			is:  fs.ErrPermission,
		},
		"Validator Unchecked of an I/O error on a scoped Node": {
			err: scopedValidated(fails(niceyaml.Unchecked(ioErr))),
			is:  fs.ErrNotExist,
		},
		"Validator Unchecked of NewError": {
			// Unchecked declares no fault and clears none.
			err:  validated(fails(niceyaml.Unchecked(niceyaml.NewError("unknown name"), niceyaml.AtPath(namePath)))),
			want: true,
		},
		"SelfValidator Unchecked of an I/O error": {
			// The Unchecked turns off the fault the decode presumes.
			err: selfValidated(niceyaml.Unchecked(ioErr, niceyaml.AtPath(closePath))),
			is:  fs.ErrNotExist,
		},
		"SelfValidator Invalid of Unchecked of an I/O error": {
			err:  selfValidated(niceyaml.Invalid(niceyaml.Unchecked(ioErr, niceyaml.AtPath(closePath)))),
			is:   fs.ErrNotExist,
			want: true,
		},
		"SelfValidator Unchecked of NewError": {
			err: selfValidated(
				niceyaml.Unchecked(niceyaml.NewError("closes before it opens"), niceyaml.AtPath(closePath)),
			),
			want: true,
		},
		"SelfValidator Locate of an I/O error": {
			// Locate declares nothing, so the decode presumes a fault.
			err:  selfValidated(niceyaml.Locate(ioErr, niceyaml.AtPath(closePath))),
			is:   fs.ErrNotExist,
			want: true,
		},
		"SelfValidator Locate of Unchecked of an I/O error": {
			err: selfValidated(niceyaml.Locate(niceyaml.Unchecked(ioErr), niceyaml.AtPath(closePath))),
			is:  fs.ErrNotExist,
		},
		"WithValueCheck errors.New": {
			err:  valueChecked(errors.New("name is reserved")),
			want: true,
		},
		"WithValueCheck I/O error": {
			err:  valueChecked(fmt.Errorf("look up names: %w", ioErr)),
			is:   fs.ErrNotExist,
			want: true,
		},
		"WithValueCheck join of errors with no location": {
			err:  valueChecked(errors.Join(errors.New("name is reserved"), errors.New("port is taken"))),
			want: true,
		},
		"WithValueCheck Unchecked of an I/O error": {
			err: valueChecked(niceyaml.Unchecked(ioErr, niceyaml.AtPath(namePath))),
			is:  fs.ErrNotExist,
		},
		"WithValueCheck Unchecked of NewError": {
			err:  valueChecked(niceyaml.Unchecked(niceyaml.NewError("name is reserved"), niceyaml.AtPath(namePath))),
			want: true,
		},
		"WithValueCheck Locate of an I/O error": {
			err:  valueChecked(niceyaml.Locate(ioErr, niceyaml.AtPath(namePath))),
			is:   fs.ErrNotExist,
			want: true,
		},
		"Validator Locate of an I/O error": {
			// A Validator presumes nothing, so the I/O error inside Locate
			// is not a fault.
			err: validated(fails(niceyaml.Locate(ioErr, niceyaml.AtPath(namePath)))),
			is:  fs.ErrNotExist,
		},
		"Validator Locate of NewError": {
			err:  validated(fails(niceyaml.Locate(niceyaml.NewError("unknown name"), niceyaml.AtPath(namePath)))),
			want: true,
		},
		"UnmarshalText Unchecked of an I/O error": {
			err: errOf(yamltest.FirstDocument(t, "v: x\n").Decode[struct {
				V uncheckedText `yaml:"v"`
			}](t.Context())),
			is: fs.ErrPermission,
		},
		"UnmarshalYAML Unchecked of an I/O error": {
			err: errOf(yamltest.FirstDocument(t, "v: x\n").Decode[struct {
				V uncheckedUnmarshaler `yaml:"v"`
			}](t.Context())),
			is: fs.ErrPermission,
		},
		"CustomUnmarshaler Unchecked of an I/O error": {
			err: errOf(yamltest.FirstDocument(t, "v: x\n").Decode[struct {
				V licenseRef `yaml:"v"`
			}](t.Context(), withLicenseCheck(func(func(any) error) error {
				return niceyaml.Unchecked(ioErr)
			}))),
			is: fs.ErrNotExist,
		},
		"CustomUnmarshaler Invalid of Unchecked of an I/O error": {
			err: errOf(yamltest.FirstDocument(t, "v: x\n").Decode[struct {
				V licenseRef `yaml:"v"`
			}](t.Context(), withLicenseCheck(func(func(any) error) error {
				return niceyaml.Invalid(niceyaml.Unchecked(ioErr))
			}))),
			is:   fs.ErrNotExist,
			want: true,
		},
		"CustomUnmarshaler Unchecked of NewError": {
			err: errOf(yamltest.FirstDocument(t, "v: x\n").Decode[struct {
				V licenseRef `yaml:"v"`
			}](t.Context(), withLicenseCheck(func(func(any) error) error {
				return niceyaml.Unchecked(niceyaml.NewError("unknown license"))
			}))),
			want: true,
		},
		"CustomUnmarshaler Unchecked of a rejection of its decode function": {
			// The rejection is a fault of its own, so the Unchecked keeps it.
			err: errOf(yamltest.FirstDocument(t, "v: x\n").Decode[struct {
				V licenseRef `yaml:"v"`
			}](t.Context(), withLicenseCheck(func(decode func(any) error) error {
				var n int

				return niceyaml.Unchecked(decode(&n))
			}))),
			is:   niceyaml.ErrDecode,
			want: true,
		},
		"Validator Unchecked of a rejection from DecodeAt": {
			err: validated(niceyaml.ValidatorFunc(func(ctx context.Context, n *niceyaml.Node) error {
				_, err := n.DecodeAt[int](ctx, namePath)

				return niceyaml.Unchecked(err)
			})),
			is:   niceyaml.ErrDecode,
			want: true,
		},
		"Validator error with no location and a located detail": {
			err: validated(fails(niceyaml.NewError("name taken",
				niceyaml.WithDetails(yamltest.ErrorAt("first used here", "name"))))),
			want: true,
		},
		"schema that does not load": {
			err: directive(fstest.MapFS{}, "./missing.json").Validate(t.Context(), byDirective),
			is:  schema.ErrLoad,
		},
		"schema that does not compile": {
			err: directive(fstest.MapFS{"bad.json": {Data: []byte(`{"type": 12}`)}}, "./bad.json").
				Validate(t.Context(), byDirective),
			is: schema.ErrCompile,
		},
		"$ref that does not resolve": {
			err: validated(schema.MustCompile(
				[]byte(`{"properties": {"port": {"$ref": "https://example.invalid/nope.json"}}}`),
				schema.WithRequireRefs(false),
			)),
			is: schema.ErrValidate,
		},
		"no matching schema with reasons": {
			err:  validated(schema.NewRegistry(schema.WithResolvers(schema.Directive()))),
			is:   schema.ErrNoDirective,
			want: true,
		},
		"no matching schema without reasons": {
			err:  validated(schema.NewRegistry()),
			is:   schema.ErrNoMatch,
			want: true,
		},
		"canceled context in a validator": {
			err: doc.Validate(canceled, contextual),
			is:  context.Canceled,
		},
		"canceled context in a validator on a scoped Node": {
			err: hours.Validate(canceled, contextual),
			is:  context.Canceled,
		},
		"canceled context in a registry": {
			err: directive(fstest.MapFS{}, "./missing.json").Validate(canceled, byDirective),
			is:  context.Canceled,
		},
		"canceled context in the self-validation walk": {
			err: doc.SelfValidate(canceled, &checkedConfig{Hours: selfChecked{check: func() error {
				return errors.New("closes before it opens")
			}}}),
			is: context.Canceled,
		},
		"SelfValidator context error": {
			err: selfValidated(context.Canceled),
			is:  context.Canceled,
		},
		"Validator Invalid of a canceled check": {
			err: hours.Validate(canceled, niceyaml.ValidatorFunc(
				func(ctx context.Context, n *niceyaml.Node) error {
					return n.Bind(niceyaml.Invalid(ctx.Err()))
				},
			)),
			is: context.Canceled,
		},
		"Validator I/O error at the root": {
			err: validated(failing),
			is:  fs.ErrNotExist,
		},
		"Validator I/O error on a scoped Node": {
			err: scopedValidated(failing),
			is:  fs.ErrNotExist,
		},
		"Validator Invalid of an I/O error": {
			err:  validated(fails(niceyaml.Invalid(ioErr, niceyaml.AtPath(namePath)))),
			is:   fs.ErrNotExist,
			want: true,
		},
		"multiple documents": {
			err:  decoded(stringtest.LinesLF("name: cafe", "---", "name: bar")),
			is:   niceyaml.ErrMultipleDocuments,
			want: true,
		},
		"Node.At of a missing key": {
			err:  errOf(doc.At(hoursPath.Child("lunch"))),
			is:   paths.ErrNotFound,
			want: true,
		},
		"Node.At of a missing index": {
			err:  errOf(yamltest.FirstDocument(t, "items: []\n").At(paths.Current().Child("items").Index(0))),
			is:   paths.ErrNotFound,
			want: true,
		},
		"Node.At of a key of a scalar": {
			err:  errOf(doc.At(namePath.Child("first"))),
			is:   paths.ErrNotFound,
			want: true,
		},
		"Node.Ranges of a missing index": {
			err:  errOf(yamltest.FirstDocument(t, "items: []\n").Ranges(paths.Current().Child("items").Index(0))),
			is:   paths.ErrNotFound,
			want: true,
		},
		"Node.At in a document with no content": {
			err:  errOf(yamltest.FirstDocument(t, "# no content\n").At(paths.Current().Child("items"))),
			is:   paths.ErrNoDocument,
			want: true,
		},
		"Node.At of a wildcard path": {
			err: errOf(doc.At(paths.Current().ChildAll())),
			is:  paths.ErrWildcard,
		},
		"UnmarshalYAML I/O error": {
			err: errOf(yamltest.FirstDocument(t, "v: x\n").Decode[struct {
				V fetchingUnmarshaler `yaml:"v"`
			}](t.Context())),
			is:   fs.ErrNotExist,
			want: true,
		},
		"UnmarshalYAML panic": {
			err: errOf(yamltest.FirstDocument(t, "v: x\n").Decode[struct {
				V panickingUnmarshaler `yaml:"v"`
			}](t.Context())),
			is: errUnmarshal,
		},
		"decoder panic a document trips": {
			// The go-yaml decoder indexes the array past its end.
			err: errOf(yamltest.FirstDocument(t, "pair: [a, b, c]\n").Decode[struct {
				Pair [2]string `yaml:"pair"`
			}](t.Context())),
		},
		"parser panic": {
			// The go-yaml parser dereferences the position of this token.
			err: func() error {
				tks := lexer.Tokenize("a: 1\nb: 2\n")
				tks[2].Position = nil

				return errOf(niceyaml.NewSourceFromTokens(tks).File())
			}(),
		},
		"panic beside a rejection": {
			err: errOf(yamltest.FirstDocument(t, "port: abc\nv: x\n").Decode[struct {
				Port int                  `yaml:"port"`
				V    panickingUnmarshaler `yaml:"v"`
			}](t.Context())),
			is: niceyaml.ErrDecode,
		},
		"PanicError around an Error from NewError": {
			// The chain ends at the PanicError, so the Error the code
			// panicked with declares nothing.
			err: &niceyaml.PanicError{Value: niceyaml.NewError("name taken")},
		},
		"wrapper around a PanicError around an Error from NewError": {
			err: fmt.Errorf("load: %w", &niceyaml.PanicError{Value: niceyaml.NewError("name taken")}),
		},
		"target type the decoder refuses": {
			err: errOf(yamltest.FirstDocument(t, "a: 1\n").Decode[struct {
				A int `yaml:"a"`
				B int `yaml:"a"`
			}](t.Context())),
			is:   niceyaml.ErrDecode,
			want: true,
		},
		"decode target that is no pointer": {
			err: doc.DecodeInto(t.Context(), (*invalidConfig)(nil)),
			is:  niceyaml.ErrDecodeTarget,
		},
		"self-validation target that is nil": {
			err: doc.SelfValidate(t.Context(), nil),
			is:  niceyaml.ErrSelfValidateTarget,
		},
		"excessive aliasing": {
			err:  errOf(yamltest.FirstDocument(t, yamltest.AliasLevels(5)).Decode[any](t.Context())),
			is:   niceyaml.ErrExcessiveAliasing,
			want: true,
		},
		"excessive aliasing a schema refuses": {
			err:  yamltest.FirstDocument(t, yamltest.AliasLevels(5)).Validate(t.Context(), portSchema),
			is:   niceyaml.ErrExcessiveAliasing,
			want: true,
		},
		"excessive aliasing a schema refuses in a decoded value": {
			err: func() error {
				trusted := yamltest.FirstDocument(t, yamltest.AliasLevels(5), niceyaml.WithAliasLimit(false))

				data, err := trusted.Decode[any](t.Context())
				require.NoError(t, err)

				return portSchema.ValidateValue(t.Context(), data)
			}(),
			is:   niceyaml.ErrExcessiveAliasing,
			want: true,
		},
		"excessive aliasing a content matcher refuses": {
			err: yamltest.FirstDocument(t, yamltest.AliasLevels(5)).Validate(t.Context(),
				schema.NewRegistry(schema.WithResolvers(schema.When(
					matcher.Content(paths.Doc().Child("a"), "x"),
					portSchema.Ref(),
				))),
			),
			is:   schema.ErrResolve,
			want: true,
		},
		"excessive aliasing of a path": {
			// The path selects far more nodes than the document holds,
			// which is no fault of the document.
			err: errOf(yamltest.FirstDocument(t, yamltest.AliasLevels(5)).
				Nodes(paths.MustParse("$.a[5][*][*][*][*][*]"))),
			is: niceyaml.ErrExcessiveAliasing,
		},
		"source that does not read": {
			err: errOf(niceyaml.NewSourceFromFS(fstest.MapFS{}, "cafe.yaml")),
			is:  fs.ErrNotExist,
		},
		"join of a violation and a schema that does not load": {
			err: errors.Join(
				validated(portSchema),
				directive(fstest.MapFS{}, "./missing.json").Validate(t.Context(), byDirective),
			),
			is: schema.ErrLoad,
		},
		"MultiValidator of an invalid problem and an I/O error": {
			err: validated(niceyaml.MultiValidator(unknownName, failing)),
			is:  fs.ErrNotExist,
		},
		"join of a syntax error and a violation": {
			err:  errors.Join(decoded("name: [cafe\n"), validated(portSchema)),
			is:   niceyaml.ErrSyntax,
			want: true,
		},
		"join of a syntax error and a source that does not read": {
			// The error matches ErrSyntax through one problem, and the
			// other is no fault of the document.
			err: errors.Join(
				decoded("name: [cafe\n"),
				errOf(niceyaml.NewSourceFromFS(fstest.MapFS{}, "cafe.yaml")),
			),
			is: niceyaml.ErrSyntax,
		},
		"unbound located error": {
			err:  yamltest.ErrorAt("unknown name", "name"),
			want: true,
		},
		"unbound Invalid of a join": {
			err:  niceyaml.Invalid(errors.Join(errors.New("opens too late"), ioErr)),
			is:   fs.ErrNotExist,
			want: true,
		},
		"unbound Rebase of Invalid of a join": {
			err: niceyaml.Rebase(
				niceyaml.Invalid(errors.Join(errors.New("opens too late"), ioErr)),
				hoursPath,
			),
			is:   fs.ErrNotExist,
			want: true,
		},
		"unbound Invalid of a join of bound errors": {
			// A branch that is bound already is a problem the Error
			// heads, as any other branch is.
			err:  niceyaml.Invalid(errors.Join(boundRead, boundPlain)),
			is:   fs.ErrNotExist,
			want: true,
		},
		"bound Invalid of a join of a bound error and a plain one": {
			err: doc.Bind(
				niceyaml.Invalid(errors.Join(boundRead, errors.New("opens too late"))),
			),
			is:   fs.ErrNotExist,
			want: true,
		},
		"unbound Invalid of a summary that heads Unchecked": {
			// The Unchecked declares no fault, so the Invalid above it holds.
			err: niceyaml.Invalid(
				niceyaml.NewSummary("2 problems", errors.New("opens too late"), niceyaml.Unchecked(ioErr)),
			),
			is:   fs.ErrNotExist,
			want: true,
		},
		"unbound Rebase of Invalid of a summary that heads Unchecked": {
			err: niceyaml.Rebase(
				niceyaml.Invalid(
					niceyaml.NewSummary("2 problems", errors.New("opens too late"), niceyaml.Unchecked(ioErr)),
				),
				hoursPath,
			),
			is:   fs.ErrNotExist,
			want: true,
		},
		"bound Rebase of Invalid of a summary of bound errors": {
			err: doc.Bind(niceyaml.Rebase(
				niceyaml.Invalid(niceyaml.NewSummary("2 problems", boundRead, boundPlain)),
				hoursPath,
			)),
			is:   fs.ErrNotExist,
			want: true,
		},
		"unbound join of bound errors": {
			// No Invalid heads the join, so each binding answers for
			// itself.
			err: errors.Join(boundRead, boundPlain),
			is:  fs.ErrNotExist,
		},
		"unbound join of plain errors": {
			err: errors.Join(errors.New("opens too late"), ioErr),
			is:  fs.ErrNotExist,
		},
		"unbound summary of a located error and one with no location": {
			err: niceyaml.NewSummary("2 problems",
				yamltest.ErrorAt("unknown name", "name"), ioErr),
			is: fs.ErrNotExist,
		},
		"unbound NewError at a position": {
			err:  niceyaml.NewError("bad name", niceyaml.AtPosition(position.New(0, 6))),
			want: true,
		},
		"unbound NewError over a range": {
			err: niceyaml.NewError("bad name",
				niceyaml.AtRange(yamltest.LineRange(0, 6, 10))),
			want: true,
		},
		"unbound NewError with no location": {
			err:  niceyaml.NewError("name taken"),
			want: true,
		},
		"unbound Invalid of an I/O error": {
			err:  niceyaml.Invalid(ioErr),
			is:   fs.ErrNotExist,
			want: true,
		},
		"unbound Invalid of a located error": {
			err:  niceyaml.Invalid(yamltest.ErrorAt("unknown name", "name")),
			want: true,
		},
		"unbound fmt wrapper around a located error": {
			err:  fmt.Errorf("check names: %w", yamltest.ErrorAt("unknown name", "name")),
			want: true,
		},
		"unbound I/O error": {
			err: ioErr,
			is:  fs.ErrNotExist,
		},
		"unbound fmt wrapper around an I/O error": {
			err: fmt.Errorf("check names: %w", ioErr),
			is:  fs.ErrNotExist,
		},
		"unbound Rebase of an I/O error": {
			err: niceyaml.Rebase(ioErr, namePath),
			is:  fs.ErrNotExist,
		},
		"unbound Unchecked of an I/O error": {
			err: niceyaml.Unchecked(ioErr, niceyaml.AtPath(namePath)),
			is:  fs.ErrNotExist,
		},
		"unbound Unchecked of a fmt wrapper around NewError": {
			err: niceyaml.Unchecked(
				fmt.Errorf("check names: %w", niceyaml.NewError("unknown name")),
				niceyaml.AtPath(namePath),
			),
			want: true,
		},
		"unbound Invalid of Unchecked of an I/O error": {
			err:  niceyaml.Invalid(niceyaml.Unchecked(ioErr, niceyaml.AtPath(namePath))),
			is:   fs.ErrNotExist,
			want: true,
		},
		"unbound Unchecked of a mixed join": {
			// Each branch answers for itself, as it does with no Unchecked.
			err: niceyaml.Unchecked(errors.Join(niceyaml.NewError("unknown name"), ioErr)),
			is:  fs.ErrNotExist,
		},
		"unbound Unchecked of a join of NewErrors": {
			err: niceyaml.Unchecked(errors.Join(
				niceyaml.NewError("unknown name"),
				niceyaml.NewError("closes before it opens"),
			)),
			want: true,
		},
		"unbound Rebase of NewError with no location": {
			err:  niceyaml.Rebase(niceyaml.NewError("name taken"), namePath),
			want: true,
		},
		"unbound Rebase of a located error": {
			err: niceyaml.Rebase(
				yamltest.ErrorAt("closes before it opens", "close"),
				hoursPath,
			),
			want: true,
		},
		"unbound summary of plain errors": {
			err: niceyaml.NewSummary("2 problems", errors.New("opens too late"), ioErr),
			is:  fs.ErrNotExist,
		},
		"MultiValidator of a violation and a permission error": {
			err: validated(niceyaml.MultiValidator(portSchema, denied)),
			is:  fs.ErrPermission,
		},
		"MultiValidator of a decode rejection and a permission error": {
			// The error matches ErrDecode through one problem, and the
			// other is no fault of the document.
			err: validated(niceyaml.MultiValidator(decoding, denied)),
			is:  niceyaml.ErrDecode,
		},
		"Validator I/O error with a located detail": {
			// The detail explains the I/O error and decides nothing.
			err: validated(fails(explained)),
			is:  fs.ErrPermission,
		},
		"unbound I/O error with a located detail": {
			err: explained,
			is:  fs.ErrPermission,
		},
		"wrapper with several verbs around violations and an I/O error": {
			// The wrapper is one error, so the violations decide.
			err:  fmt.Errorf("%w; close: %w", validated(portSchema), ioErr),
			is:   fs.ErrNotExist,
			want: true,
		},
		"wrapper with several verbs around NewError and an I/O error": {
			err:  fmt.Errorf("%w; close: %w", niceyaml.NewError("name taken"), ioErr),
			is:   fs.ErrNotExist,
			want: true,
		},
		"join of violations and an I/O error": {
			// The join holds two problems, and one is no fault of the
			// document.
			err: errors.Join(validated(portSchema), ioErr),
			is:  fs.ErrNotExist,
		},
		"error with an empty message": {
			err: errors.New(""),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			require.Error(t, tc.err)

			if tc.is != nil {
				require.ErrorIs(t, tc.err, tc.is)
			}

			assert.Equal(t, tc.want, niceyaml.IsInvalid(tc.err))

			// The root of the tree answers as IsInvalid does, and each
			// problem answers as IsInvalid does for its error.
			tree := niceyaml.NewErrorTree(tc.err)
			assert.Equal(t, tc.want, tree.IsInvalid())

			for problem := range tree.Problems() {
				assert.Equal(t, niceyaml.IsInvalid(problem.Err), problem.IsInvalid(), problem.Label)
			}
		})
	}

	t.Run("an error a detail wraps decides nothing", func(t *testing.T) {
		t.Parallel()

		// The hint explains a problem and wraps the error of a context
		// that ended, which is about a call and no fault of the document.
		hint := fmt.Errorf("look up a suggestion: %w", context.DeadlineExceeded)
		hinted := niceyaml.WithDetails(hint)
		syntaxErr := decoded("name: [cafe\n")

		// The is field pins what the problem itself matches, and the
		// detail field what the detail wraps, which the error must not
		// match. The path is the one the single problem binds at.
		tcs := map[string]struct {
			err    error
			is     error
			detail error
			path   string
			want   bool
		}{
			"Invalid around Unchecked with a hint": {
				err:    niceyaml.Invalid(niceyaml.Unchecked(errors.New("unknown name"), hinted)),
				detail: context.DeadlineExceeded,
				want:   true,
			},
			"Invalid around a wrapper around NewError with a hint": {
				err:    niceyaml.Invalid(fmt.Errorf("check name: %w", niceyaml.NewError("unknown name", hinted))),
				detail: context.DeadlineExceeded,
				want:   true,
			},
			"Unchecked with a hint": {
				err:    niceyaml.Unchecked(fmt.Errorf("read names.db: %w", fs.ErrPermission), hinted),
				is:     fs.ErrPermission,
				detail: context.DeadlineExceeded,
			},
			"Validator error with a hint on a scoped Node": {
				err:    scopedValidated(fails(niceyaml.NewError("closes before it opens", hinted))),
				detail: context.DeadlineExceeded,
				path:   "$.hours",
				want:   true,
			},
			"SelfValidator error with a hint": {
				err:    selfValidated(niceyaml.NewError("closes before it opens", hinted)),
				detail: context.DeadlineExceeded,
				path:   "$.hours",
				want:   true,
			},
			"unmarshaler error with a hint": {
				err: doc.DecodeInto(t.Context(), &struct {
					Name hintedUnmarshaler `yaml:"name"`
				}{}),
				is:     niceyaml.ErrDecode,
				detail: context.DeadlineExceeded,
				path:   "$.name",
				want:   true,
			},
			"Unchecked with a syntax error as a detail": {
				err:    niceyaml.Unchecked(errors.New("defaults do not load"), niceyaml.WithDetails(syntaxErr)),
				detail: niceyaml.ErrSyntax,
			},
			"Invalid with a syntax error as a detail": {
				err:    niceyaml.Invalid(errors.New("defaults do not merge"), niceyaml.WithDetails(syntaxErr)),
				detail: niceyaml.ErrSyntax,
				want:   true,
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				require.Error(t, tc.err)

				if tc.is != nil {
					require.ErrorIs(t, tc.err, tc.is)
				}

				require.NotErrorIs(t, tc.err, tc.detail)
				assert.Equal(t, tc.want, niceyaml.IsInvalid(tc.err))

				problem := yamltest.OnlyProblem(t, tc.err)
				require.Len(t, problem.Children, 1)
				// The detail still matches for itself.
				require.ErrorIs(t, problem.Children[0].Err, tc.detail)

				if tc.path != "" {
					assert.Equal(t, tc.path, yamltest.PathOf(t, problem).String())
				}
			})
		}
	})

	t.Run("nil is not invalid", func(t *testing.T) {
		t.Parallel()

		var (
			nilErr   *niceyaml.Error
			nilBound *niceyaml.SourceError
		)

		assert.False(t, niceyaml.IsInvalid(nil))
		assert.False(t, niceyaml.IsInvalid(nilErr))
		assert.False(t, niceyaml.IsInvalid(nilBound))
	})

	t.Run("an error that is not invalid shows at a value", func(t *testing.T) {
		t.Parallel()

		tcs := map[string]struct {
			validator niceyaml.Validator
		}{
			"Unchecked with a path": {validator: unchecked},
			"Rebase under a path":   {validator: rebased},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				err := validated(tc.validator)

				bound := yamltest.RequireSourceError(t, err)

				assert.Equal(t, paths.Doc().Join(namePath), yamltest.PathOf(t, bound))
				assert.Equal(t, "1:7: $.name: stat license: permission denied", err.Error())
				assert.False(t, niceyaml.IsInvalid(err))
			})
		}
	})
}

// panicValueKey is the context key of the value a [panicsWith] panics
// with.
type panicValueKey struct{}

// panicCallsKey is the context key of the counter a [panicsWith] counts
// its calls in.
type panicCallsKey struct{}

// panicsWith decodes itself by panicking with the value the context of
// the decode holds under [panicValueKey]. It counts the call first, in
// the counter the context holds under [panicCallsKey].
type panicsWith struct{}

func (*panicsWith) UnmarshalYAML(ctx context.Context, _ []byte) error {
	if calls, ok := ctx.Value(panicCallsKey{}).(*atomic.Int32); ok {
		calls.Add(1)
	}

	panic(ctx.Value(panicValueKey{}))
}

// nilDereference decodes itself by reading through a nil pointer.
type nilDereference struct {
	next *nilDereference
}

func (d *nilDereference) UnmarshalYAML([]byte) error {
	*d = *d.next

	return nil
}

// panickingText is a string that panics when it decodes from text.
type panickingText string

func (*panickingText) UnmarshalText([]byte) error {
	panic("kind registry is empty")
}

// nestedDecode decodes itself with a decode of its own that panics, and
// returns the error of that decode.
type nestedDecode struct{}

func (*nestedDecode) UnmarshalYAML(ctx context.Context, text []byte) error {
	inner := niceyaml.NewSourceFromBytes(text, niceyaml.WithName("inner.yaml"))

	_, err := inner.Decode[panickingUnmarshaler](ctx)

	return err
}

// panickingValidator panics with errRegistry when it validates itself.
type panickingValidator struct {
	Name string `yaml:"name"`
}

func (panickingValidator) Validate() error {
	panic(errRegistry)
}

// requirePanic checks that err holds a PanicError with a stack, and
// that the document is not at fault for err. It returns the value of the
// panic and its stack.
func requirePanic(t *testing.T, err error) (any, string) {
	t.Helper()

	var p *niceyaml.PanicError

	require.ErrorAs(t, err, &p)
	require.NotEmpty(t, p.Stack)
	require.False(t, niceyaml.IsInvalid(err), "the document is at fault for a panic")

	return p.Value, string(p.Stack)
}

func TestPanicError(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		value  any
		unwrap error
		want   string
	}{
		"string": {
			value: "boom",
			want:  "panic: boom",
		},
		"number": {
			value: 42,
			want:  "panic: 42",
		},
		"error": {
			value:  errRegistry,
			unwrap: errRegistry,
			want:   "panic: registry not initialized",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			p := &niceyaml.PanicError{Value: tc.value, Stack: []byte("goroutine 1 [running]:")}

			require.EqualError(t, p, tc.want)
			assert.Equal(t, tc.unwrap, errors.Unwrap(p))
		})
	}
}

func TestPanicError_Recovered(t *testing.T) {
	t.Parallel()

	type config struct {
		Name string `yaml:"name"`
	}

	tcs := map[string]struct {
		decode func(context.Context, *niceyaml.Node) error
		// An error the code panicked with, which the result matches.
		is    error
		input string
		// Text the stack holds, which names the code that panicked.
		stack  string
		want   string
		source []niceyaml.SourceOption
		// The code panicked with a [runtime.Error], which the result
		// matches through errors.As.
		runtimeErr bool
	}{
		"function from CustomUnmarshaler": {
			input:  "name: x\n",
			source: []niceyaml.SourceOption{niceyaml.WithName("c.yaml")},
			decode: decodeAs[config](niceyaml.WithUnmarshaler(niceyaml.CustomUnmarshaler(
				func(context.Context, *config, func(any) error) error { panic(errRegistry) },
			))),
			is:   errRegistry,
			want: "c.yaml:1:1: decoder panicked: registry not initialized",
		},
		"nil dereference in UnmarshalYAML": {
			input: "v: x\n",
			decode: decodeAs[struct {
				V nilDereference `yaml:"v"`
			}](),
			stack:      "nilDereference).UnmarshalYAML",
			want:       "1:1: decoder panicked: runtime error: invalid memory address or nil pointer dereference",
			runtimeErr: true,
		},
		"UnmarshalText": {
			input: "kind: Deployment\n",
			decode: decodeAs[struct {
				Kind panickingText `yaml:"kind"`
			}](),
			stack: "panickingText).UnmarshalText",
			want:  "1:1: decoder panicked: kind registry is empty",
		},
		// The go-yaml decoder indexes the array past its end, so the
		// document alone trips this panic.
		"sequence longer than its array": {
			input:  "# pair\n[a, b, c]\n",
			decode: decodeAs[[2]string](),
			stack:  "go-yaml",
			want:   "2:1: decoder panicked: reflect: array index out of range",
		},
		// The decode of the outer value would mark the error of an
		// unmarshaler as a rejection, which the document is at fault for.
		"error an unmarshaler returns from a decode of its own": {
			input: "v: x\n",
			decode: decodeAs[struct {
				V nestedDecode `yaml:"v"`
			}](),
			is:   errUnmarshal,
			want: "inner.yaml:1:1: decoder panicked: unmarshaler rejected the value",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			doc := yamltest.FirstDocument(t, tc.input, tc.source...)

			var err error

			require.NotPanics(t, func() { err = tc.decode(t.Context(), doc) })
			require.EqualError(t, err, tc.want)
			require.NotErrorIs(t, err, niceyaml.ErrDecode)
			require.NotErrorIs(t, err, niceyaml.ErrSyntax)

			value, stack := requirePanic(t, err)
			assert.Contains(t, stack, tc.stack)

			if tc.is != nil {
				require.ErrorIs(t, err, tc.is)
				assert.Equal(t, tc.is, value)
			}

			if tc.runtimeErr {
				assert.Implements(t, (*runtime.Error)(nil), value)
				require.ErrorAs(t, err, new(runtime.Error))
			}

			require.ErrorAs(t, err, new(*niceyaml.SourceError))
		})
	}

	// The decode placed the panic already, so it calls no unmarshaler a
	// second time to find the value behind the error.
	t.Run("decode calls a value that panics once", func(t *testing.T) {
		t.Parallel()

		var calls atomic.Int32

		ctx := context.WithValue(t.Context(), panicCallsKey{}, &calls)
		ctx = context.WithValue(ctx, panicValueKey{}, errRegistry)

		_, err := yamltest.FirstDocument(t, stringtest.LinesLF(
			"items:",
			"  - v: 1",
		)).Decode[struct {
			Items []struct {
				V panicsWith `yaml:"v"`
			} `yaml:"items"`
		}](ctx)
		require.EqualError(t, err, "1:1: decoder panicked: registry not initialized")
		assert.Equal(t, int32(1), calls.Load())
	})

	t.Run("panic beside another problem", func(t *testing.T) {
		t.Parallel()

		// The decoder rejects the port and goes on to the value that
		// panics. The search then finds the port again.
		_, err := yamltest.FirstDocument(t, "port: abc\nv: x\n").Decode[struct {
			Port int                  `yaml:"port"`
			V    panickingUnmarshaler `yaml:"v"`
		}](t.Context())
		require.EqualError(t, err, stringtest.JoinLF(
			"2 problems",
			"1:1: decoder panicked: unmarshaler rejected the value",
			"1:7: $.port: expected integer, got string",
		))

		// The rejection of the port matches, and the panic keeps the error
		// from being invalid.
		require.ErrorIs(t, err, niceyaml.ErrDecode)
		requirePanic(t, err)

		invalid := map[string]bool{}

		for problem := range niceyaml.NewErrorTree(err).Problems() {
			invalid[problem.Message()] = problem.IsInvalid()

			var p *niceyaml.PanicError

			assert.Equal(t, !problem.IsInvalid(), errors.As(problem.Err, &p), problem.Label)
		}

		assert.Equal(t, map[string]bool{
			"decoder panicked: unmarshaler rejected the value": false,
			"expected integer, got string":                     true,
		}, invalid)
	})
}

func TestPanicError_Recovered_Value(t *testing.T) {
	t.Parallel()

	// Each value is an error the document is at fault for on its own, or
	// one that heads such errors.
	_, decodeErr := niceyaml.NewSourceFromString("port: abc\n", niceyaml.WithName("other.yaml")).
		Decode[struct {
		Port int `yaml:"port"`
	}](t.Context())
	require.ErrorIs(t, decodeErr, niceyaml.ErrDecode)
	require.ErrorIs(t, decodeErr, niceyaml.ErrWrongKind)
	require.True(t, niceyaml.IsInvalid(decodeErr))

	_, syntaxErr := niceyaml.NewSourceFromString("a: [1\n", niceyaml.WithName("other.yaml")).File()
	require.ErrorIs(t, syntaxErr, niceyaml.ErrSyntax)
	require.True(t, niceyaml.IsInvalid(syntaxErr))

	tcs := map[string]struct {
		value error
	}{
		"Error from NewError at a path": {
			value: niceyaml.NewError("name taken", niceyaml.AtPath(paths.Doc().Child("name"))),
		},
		"summary of two Errors": {
			value: niceyaml.NewSummary("2 problems", niceyaml.NewError("a"), niceyaml.NewError("b")),
		},
		"join of two Errors": {
			value: errors.Join(niceyaml.NewError("a"), niceyaml.NewError("b")),
		},
		"decode error of another source": {
			value: decodeErr,
		},
		"syntax error of another source": {
			value: syntaxErr,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			ctx := context.WithValue(t.Context(), panicValueKey{}, tc.value)

			_, err := yamltest.FirstDocument(t, "name: x\nv: 1\n").Decode[struct {
				Name string     `yaml:"name"`
				V    panicsWith `yaml:"v"`
			}](ctx)

			got, _ := requirePanic(t, err)
			assert.Equal(t, tc.value, got)

			// The value is in reach of errors.Is, and it decides neither
			// the stage nor the fault.
			require.ErrorIs(t, err, tc.value)
			require.NotErrorIs(t, err, niceyaml.ErrDecode)
			require.NotErrorIs(t, err, niceyaml.ErrWrongKind)
			require.NotErrorIs(t, err, niceyaml.ErrSyntax)

			// The panic is one problem at the node, whatever the value
			// points at or heads.
			assert.False(t, yamltest.OnlyProblem(t, err).IsInvalid())

			assert.Equal(t, position.New(0, 0), yamltest.PositionOf(t, yamltest.RequireSourceError(t, err)))
		})
	}
}

func TestPanicError_Validators(t *testing.T) {
	t.Parallel()

	// Nothing recovers a panic outside the decoder, so it reaches the
	// caller as the panic it is.
	tcs := map[string]struct {
		decode func(context.Context, *niceyaml.Node) error
	}{
		"panic in a Validator": {
			decode: decodeAs[any](niceyaml.WithValidator(niceyaml.ValidatorFunc(
				func(context.Context, *niceyaml.Node) error { panic(errRegistry) },
			))),
		},
		"panic in a Validate method": {
			decode: decodeAs[panickingValidator](),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			doc := yamltest.FirstDocument(t, "name: x\n")

			require.PanicsWithValue(t, errRegistry, func() {
				_ = tc.decode(t.Context(), doc) //nolint:errcheck // The call panics before it returns.
			})
		})
	}
}
