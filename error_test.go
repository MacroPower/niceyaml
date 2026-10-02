package niceyaml_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"log/slog"
	"maps"
	"math"
	"slices"
	"strconv"
	"strings"
	"testing"

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
	"go.jacobcolvin.com/niceyaml/style"
	"go.jacobcolvin.com/niceyaml/style/kind"
)

// customTestError is a test error type for errors.As testing.
type customTestError struct {
	msg string
}

func (e *customTestError) Error() string {
	return e.msg
}

// render formats err the way %+v does, which prints the annotated source
// when the error resolves to a location and the plain message otherwise. A
// [*niceyaml.SourceError] renders with [newXMLPrinter] and two lines of
// context, so tests can assert on styled text without escape sequences.
func render(err error) string {
	return renderContext(err, 2)
}

// report returns the message part of the %+v output of err: the headline
// and one line per nested error, before the blank line and the excerpt.
func report(err error) string {
	msg, _, _ := strings.Cut(fmt.Sprintf("%+v", err), "\n\n")

	return msg
}

// renderContext is [render] with the given number of context lines.
func renderContext(err error, context int) string {
	return renderWith(err, newXMLPrinter(), context)
}

// renderWith is [render] with the given printer and number of context
// lines. An error that is not a [*niceyaml.SourceError] formats with %+v.
func renderWith(err error, p *printer.Printer, context int) string {
	if bound, ok := err.(*niceyaml.SourceError); ok { //nolint:errorlint // Mirrors %+v, which formats the top-level value.
		return p.With(printer.WithContextLines(context)).PrintError(bound)
	}

	return fmt.Sprintf("%+v", err)
}

// newXMLPrinter creates a [*printer.Printer] that marks styles with XML
// tags and renders no gutter or container, so tests can assert on plain text.
func newXMLPrinter() *printer.Printer {
	return printer.New(
		printer.WithStyles(yamltest.NewXMLStyles()),
		printer.WithGutter(printer.NoGutter),
		printer.WithContainerStyle(lipgloss.NewStyle()),
	)
}

// xmlSource creates a [*niceyaml.Source] for input. Render its errors with
// [render], which uses [newXMLPrinter].
func xmlSource(input string) *niceyaml.Source {
	return niceyaml.NewSourceFromString(input)
}

// trimLines trims trailing whitespace from each line of a string.
// Tests use it to compare styled output, which lipgloss pads.
func trimLines(s string) string {
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimRight(line, " \t")
	}

	return stringtest.JoinLF(lines...)
}

func TestError(t *testing.T) {
	t.Parallel()

	source := stringtest.Input(`
		a: b
		foo: bar
		key: value
	`)
	tokens := lexer.Tokenize(source)

	tcs := map[string]struct {
		err  error
		want string
	}{
		"nil error returns empty string": {
			err:  niceyaml.WrapError(nil),
			want: "",
		},
		"no path or token returns plain error": {
			err:  niceyaml.NewError("something went wrong"),
			want: "something went wrong",
		},
		"with path and source shows annotated source": {
			err: yamltest.Bind(t, xmlSource(source), niceyaml.NewError(
				"invalid value",
				niceyaml.AtPath(paths.Root().Child("key").Key()),
			)),
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
			err: yamltest.Bind(t, xmlSource(source), niceyaml.NewError(
				"bad token",
				niceyaml.AtPosition(position.NewFromToken(tokens[0])),
			)),
			want: stringtest.JoinLF(
				"1:1: bad token",
				"",
				"<genericError>a</genericError><punctuationMappingValue>:</punctuationMappingValue><text> </text><literalString>b</literalString>",
				"<textError>^</textError>",
				"<nameTag>foo</nameTag><punctuationMappingValue>:</punctuationMappingValue><text> </text><literalString>bar</literalString>",
				"<nameTag>key</nameTag><punctuationMappingValue>:</punctuationMappingValue><text> </text><literalString>value</literalString>",
			),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := render(tc.err)

			assert.Equal(t, tc.want, trimLines(got))
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
			err := yamltest.Bind(t, source, niceyaml.NewError(
				"bad value",
				niceyaml.AtPath(paths.Root().Child("value")),
			))

			assert.Equal(t, tc.want, err.Error())
			assert.Equal(t, "document 0: "+tc.want, fmt.Errorf("document 0: %w", err).Error())
		})
	}

	// An error without a location has no position for the name to go in
	// front of, so the name stands alone.
	noLocation := map[string]struct {
		err  error
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
			want: "config: bad value",
		},
		"name stands alone in front of an Error without a location": {
			err:  niceyaml.NewError("bad value"),
			want: "config: bad value",
		},
		"name stands alone in front of a path that does not resolve": {
			err:        niceyaml.NewError("bad value", niceyaml.AtExactPath(paths.Root().Child("missing"))),
			want:       "config: $.missing: bad value",
			wantDetail: "no excerpt: resolve $.missing: not found",
		},
		"name stands alone in front of an empty message": {
			err:  niceyaml.WrapError(nil),
			want: "config:",
		},
	}

	for name, tc := range noLocation {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var opts []niceyaml.SourceOption

			if strings.HasPrefix(tc.want, "config") {
				opts = append(opts, niceyaml.WithName("config"))
			}

			source := niceyaml.NewSourceFromString("name: test\nvalue: 123\n", opts...)
			err := yamltest.Bind(t, source, tc.err)

			assert.Equal(t, tc.want, err.Error())

			wantPlus := tc.want
			if tc.wantDetail != "" {
				wantPlus += "\n\n" + tc.wantDetail
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

			var bound *niceyaml.SourceError

			require.ErrorAs(t, err, &bound)

			if tc.wantUnresolved == "" {
				require.NoError(t, bound.Unresolved())

				return
			}

			require.ErrorIs(t, bound.Unresolved(), niceyaml.ErrOutOfRange)
			assert.Contains(t, bound.Unresolved().Error(), tc.wantUnresolved)
		})
	}
}

func TestSourceError_Error_JoinLead(t *testing.T) {
	t.Parallel()

	source := niceyaml.NewSourceFromString("a: 1\n", niceyaml.WithName("f.yaml"))
	bound := yamltest.Bind(t, source, niceyaml.NewError(
		"bad a",
		niceyaml.AtPath(paths.Root().Child("a")),
	))

	// A binding that supplies the first line of a join names the source
	// there, so the name goes in front only when the first line comes
	// from no binding. A nil branch supplies an empty first line.
	tcs := map[string]struct {
		// The branches in front of the binding.
		lead []error
		want string
	}{
		"binding leads": {
			want: "f.yaml:1:4: $.a: bad a\nplain",
		},
		"empty message leads": {
			lead: []error{errors.New("")},
			want: "f.yaml: \nf.yaml:1:4: $.a: bad a\nplain",
		},
		"nil Error leads": {
			lead: []error{(*niceyaml.Error)(nil)},
			want: "f.yaml: \nf.yaml:1:4: $.a: bad a\nplain",
		},
		"nil SourceError leads": {
			lead: []error{(*niceyaml.SourceError)(nil)},
			want: "f.yaml: \nf.yaml:1:4: $.a: bad a\nplain",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			branches := append(slices.Clone(tc.lead), bound, errors.New("plain"))
			err := source.Bind(errors.Join(branches...))

			require.Error(t, err)
			assert.Equal(t, tc.want, err.Error())
		})
	}
}

func TestSourceError_Error_MultiErrorLead(t *testing.T) {
	t.Parallel()

	errSentinel := errors.New("sentinel")
	plain := errors.New("plain")
	source := niceyaml.NewSourceFromString("a: 1\nb: 2\n", niceyaml.WithName("f.yaml"))
	boundA := yamltest.Bind(t, source, niceyaml.NewError(
		"bad a",
		niceyaml.AtPath(paths.Root().Child("a")),
	))
	boundB := yamltest.Bind(t, source, niceyaml.NewError(
		"bad b",
		niceyaml.AtPath(paths.Root().Child("b")),
	))

	// A wrapper with several %w verbs leads with the binding it keeps, as
	// a wrapper with one %w verb does, so its first line names the source
	// already. A multi-error of its own type leads with its first branch
	// when its message starts with the message of that branch, as a list
	// does. A count of the branches starts with no binding, so the name
	// goes in front whichever branch holds the binding.
	tcs := map[string]struct {
		err  error
		want string
	}{
		"wrapper with one verb": {
			err:  errors.Join(fmt.Errorf("ctx: %w", boundA), plain),
			want: "ctx: f.yaml:1:4: $.a: bad a\nplain",
		},
		"sentinel wrapper": {
			err:  errors.Join(fmt.Errorf("%w: %w", errSentinel, boundA), plain),
			want: "sentinel: f.yaml:1:4: $.a: bad a\nplain",
		},
		"wrapper of two bindings": {
			err:  errors.Join(fmt.Errorf("%w and %w", boundA, boundB), plain),
			want: "f.yaml:1:4: $.a: bad a and f.yaml:2:4: $.b: bad b\nplain",
		},
		"multi-error with the binding first": {
			err:  violationsError{boundA, plain},
			want: "f.yaml: 2 violations",
		},
		"multi-error with the binding last": {
			err:  violationsError{plain, boundA},
			want: "f.yaml: 2 violations",
		},
		"list with the binding first": {
			err:  listError{boundA, plain},
			want: "f.yaml:1:4: $.a: bad a; plain",
		},
		"list with the binding last": {
			err:  listError{plain, boundA},
			want: "f.yaml: plain; f.yaml:1:4: $.a: bad a",
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

func TestDocument_BindRender(t *testing.T) {
	t.Parallel()

	sourceInput := stringtest.Input(`
		name: test
		value: 123
	`)

	tcs := map[string]struct {
		inputErr  func() error
		wantExact string
		wantNil   bool
	}{
		"wrap nil returns nil": {
			inputErr: func() error { return nil },
			wantNil:  true,
		},
		"wrap non-error type names the source alone": {
			inputErr:  func() error { return errors.New("plain error") },
			wantExact: "plain error",
		},
		"wrap error renders with source": {
			inputErr: func() error {
				return niceyaml.NewError(
					"test error",
					niceyaml.AtPath(paths.Root().Child("name").Key()),
				)
			},
			wantExact: stringtest.JoinLF(
				"1:1: $.name~: test error",
				"",
				"<genericError>name</genericError><punctuationMappingValue>:</punctuationMappingValue><text> </text><literalString>test</literalString>",
				"<textError>^^^^</textError>",
				"<nameTag>value</nameTag><punctuationMappingValue>:</punctuationMappingValue><text> </text><literalNumberInteger>123</literalNumberInteger>",
			),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			source := niceyaml.NewSourceFromString(sourceInput)
			inputErr := tc.inputErr()

			got := yamltest.Bind(t, source, inputErr)

			if tc.wantNil {
				assert.NoError(t, got)

				return
			}

			require.Error(t, got)

			if tc.wantExact != "" {
				assert.Equal(t, tc.wantExact, trimLines(render(got)))
			}
		})
	}
}

func TestError_Location(t *testing.T) {
	t.Parallel()

	path := paths.Root().Child("foo")
	pos := position.New(1, 4)
	rng := position.NewRange(position.New(1, 2), position.New(1, 5))

	// The accessors of an Error report a path, and a position or a range,
	// each nil here when the accessor reports false.
	type located struct {
		path any
		pos  any
		rng  any
	}

	// Reads the accessors of e. Fails the test when e reports both a
	// position and a range.
	location := func(t *testing.T, e *niceyaml.Error) located {
		t.Helper()

		var got located

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

	tcs := map[string]struct {
		err  *niceyaml.Error
		want located
	}{
		"nil": {
			err: nil,
		},
		"no location": {
			err: niceyaml.NewError("test"),
		},
		"path": {
			err:  niceyaml.NewError("test", niceyaml.AtPath(path)),
			want: located{path: path},
		},
		"key": {
			err:  niceyaml.NewError("test", niceyaml.AtPath(path.Key())),
			want: located{path: path.Key()},
		},
		"key replaces a path": {
			err:  niceyaml.NewError("test", niceyaml.AtPath(path), niceyaml.AtPath(path.Key())),
			want: located{path: path.Key()},
		},
		"position": {
			err:  niceyaml.NewError("test", niceyaml.AtPosition(pos)),
			want: located{pos: pos},
		},
		"range": {
			err:  niceyaml.NewError("test", niceyaml.AtRange(rng)),
			want: located{rng: rng},
		},
		"a path and a position combine": {
			err:  niceyaml.NewError("test", niceyaml.AtPath(path), niceyaml.AtPosition(pos)),
			want: located{path: path, pos: pos},
		},
		"a path and a range combine in either order": {
			err:  niceyaml.NewError("test", niceyaml.AtRange(rng), niceyaml.AtPath(path)),
			want: located{path: path, rng: rng},
		},
		"a range replaces a position": {
			err:  niceyaml.NewError("test", niceyaml.AtPosition(pos), niceyaml.AtRange(rng)),
			want: located{rng: rng},
		},
		"a position replaces a range": {
			err:  niceyaml.NewError("test", niceyaml.AtRange(rng), niceyaml.AtPosition(pos)),
			want: located{pos: pos},
		},
		"path on a wrapped error": {
			err: niceyaml.WrapError(
				fmt.Errorf("context: %w", niceyaml.NewError("test", niceyaml.AtPath(path))),
			),
			want: located{path: path},
		},
		"position on a wrapped error": {
			err: niceyaml.WrapError(
				fmt.Errorf("context: %w", niceyaml.NewError("test", niceyaml.AtPosition(pos))),
			),
			want: located{pos: pos},
		},
		"range on a wrapped error": {
			err: niceyaml.WrapError(
				fmt.Errorf("context: %w", niceyaml.NewError("test", niceyaml.AtRange(rng))),
			),
			want: located{rng: rng},
		},
		"a path and a range on a wrapped error": {
			err: niceyaml.WrapError(
				fmt.Errorf("context: %w", niceyaml.NewError("test", niceyaml.AtPath(path), niceyaml.AtRange(rng))),
			),
			want: located{path: path, rng: rng},
		},
		"own location wins over a wrapped one": {
			err: niceyaml.WrapError(
				niceyaml.NewError("test", niceyaml.AtPath(path)),
				niceyaml.AtPosition(pos),
			),
			want: located{pos: pos},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := location(t, tc.err)
			assert.Equal(t, tc.want.path, got.path)
			assert.Equal(t, tc.want.pos, got.pos)
			assert.Equal(t, tc.want.rng, got.rng)
		})
	}
}

func TestError_GracefulDegradation(t *testing.T) {
	t.Parallel()

	source := `key: value`

	// Empty tokens for edge case tests.
	emptyTokens := lexer.Tokenize("")

	tcs := map[string]struct {
		err  error
		want string
	}{
		"invalid path": {
			err: yamltest.Bind(t, niceyaml.NewSourceFromString(source), niceyaml.NewError(
				"not found",
				niceyaml.AtPath(paths.Root().Child("nonexistent").Key()),
			)),
			want: "$.nonexistent~: not found\n\nno excerpt: resolve $.nonexistent~: not found",
		},
		"path without source": {
			err: niceyaml.NewError(
				"missing source",
				niceyaml.AtPath(paths.Root().Child("key").Key()),
			),
			want: "$.key~: missing source",
		},
		"empty source": {
			err: yamltest.Bind(t, niceyaml.NewSourceFromTokens(emptyTokens), niceyaml.NewError(
				"error in empty source",
				niceyaml.AtPath(paths.Root().Child("key").Key()),
			)),
			want: "$.key~: error in empty source\n\nno excerpt: resolve $.key~: not found: document has no content",
		},
		"nonexistent path in source": {
			err: yamltest.Bind(t, niceyaml.NewSourceFromString(source), niceyaml.NewError(
				"path not found",
				niceyaml.AtPath(
					paths.Root().Child("nonexistent").Child("deep").Key(),
				),
			)),
			want: "$.nonexistent.deep~: path not found\n\nno excerpt: resolve $.nonexistent.deep~: not found",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := render(tc.err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestErrorAnnotation(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		loc          niceyaml.ErrorOption
		source       string
		errMsg       string
		want         string
		contextLines int
	}{
		"nested path shows correct key": {
			source: stringtest.Input(`
				foo:
				  bar: value
			`),
			loc:    niceyaml.AtPath(paths.Root().Child("foo", "bar").Key()),
			errMsg: "nested error",
			want: stringtest.JoinLF(
				"2:3: $.foo.bar~: nested error",
				"",
				"<nameTag>foo</nameTag><punctuationMappingValue>:</punctuationMappingValue>",
				"<text>  </text><genericError>bar</genericError><punctuationMappingValue>:</punctuationMappingValue><text> </text><literalString>value</literalString>",
				"<textError>  ^^^</textError>",
			),
		},
		"array element path - first item": {
			source: stringtest.Input(`
				items:
				  - first
				  - second
			`),
			loc:    niceyaml.AtPath(paths.Root().Child("items").Index(0).Key()),
			errMsg: "array error",
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
			source: stringtest.Input(`
				users:
				  - name: alice
				    age: 30
			`),
			loc:    niceyaml.AtPath(paths.Root().Child("users").Index(0).Child("name").Key()),
			errMsg: "nested array error",
			want: stringtest.JoinLF(
				"2:5: $.users[0].name~: nested array error",
				"",
				"<nameTag>users</nameTag><punctuationMappingValue>:</punctuationMappingValue>",
				"<text>  </text><punctuationSequenceEntry>-</punctuationSequenceEntry><text> </text><genericError>name</genericError><punctuationMappingValue>:</punctuationMappingValue><text> </text><literalString>alice</literalString>",
				"<textError>    ^^^^</textError>",
				"<text>    </text><nameTag>age</nameTag><punctuationMappingValue>:</punctuationMappingValue><text> </text><literalNumberInteger>30</literalNumberInteger>",
			),
		},
		"root path highlights the first key": {
			source: "key: value",
			loc:    niceyaml.AtPath(paths.Root().Key()),
			errMsg: "root error",
			want: stringtest.JoinLF(
				"1:1: $~: root error",
				"",
				"<genericError>key</genericError><punctuationMappingValue>:</punctuationMappingValue><text> </text><literalString>value</literalString>",
				"<textError>^^^</textError>",
			),
		},
		"single top-level key path": {
			source: "key: value",
			loc:    niceyaml.AtPath(paths.Root().Child("key").Key()),
			errMsg: "top level error",
			want: stringtest.JoinLF(
				"1:1: $.key~: top level error",
				"",
				"<genericError>key</genericError><punctuationMappingValue>:</punctuationMappingValue><text> </text><literalString>value</literalString>",
				"<textError>^^^</textError>",
			),
		},
		"with custom source lines": {
			source: stringtest.Input(`
				line1: a
				line2: b
				line3: c
				line4: d
				line5: e
			`),
			loc:          niceyaml.AtPath(paths.Root().Child("line3").Key()),
			errMsg:       "middle error",
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
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			context := 2
			if tc.contextLines > 0 {
				context = tc.contextLines
			}

			err := yamltest.Bind(t, xmlSource(tc.source),
				niceyaml.NewError(tc.errMsg, tc.loc),
			)

			assert.Equal(t, tc.want, trimLines(renderContext(err, context)))
		})
	}
}

func TestErrorAnnotation_PathTargetValue(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		loc    niceyaml.ErrorOption
		source string
		errMsg string
		want   string
	}{
		"value selection highlights value token": {
			source: "key: value",
			loc:    niceyaml.AtPath(paths.Root().Child("key")),
			errMsg: "invalid value",
			want: stringtest.JoinLF(
				"1:6: $.key: invalid value",
				"",
				"<nameTag>key</nameTag><punctuationMappingValue>:</punctuationMappingValue><text> </text><genericError>value</genericError>",
				"<textError>     ^^^^^</textError>",
			),
		},
		"nested path with value target": {
			source: stringtest.Input(`
				foo:
				  bar: nested_value
			`),
			loc:    niceyaml.AtPath(paths.Root().Child("foo", "bar")),
			errMsg: "nested value error",
			want: stringtest.JoinLF(
				"2:8: $.foo.bar: nested value error",
				"",
				"<nameTag>foo</nameTag><punctuationMappingValue>:</punctuationMappingValue>",
				"<text>  </text><nameTag>bar</nameTag><punctuationMappingValue>:</punctuationMappingValue><text> </text><genericError>nested_value</genericError>",
				"<textError>       ^^^^^^^^^^^^</textError>",
			),
		},
		"array element works same as key target": {
			// Array elements don't have keys, so both targets return the value token.
			source: stringtest.Input(`
				items:
				  - first
				  - second
			`),
			loc:    niceyaml.AtPath(paths.Root().Child("items").Index(0)),
			errMsg: "array error",
			want: stringtest.JoinLF(
				"2:5: $.items[0]: array error",
				"",
				"<nameTag>items</nameTag><punctuationMappingValue>:</punctuationMappingValue>",
				"<text>  </text><punctuationSequenceEntry>-</punctuationSequenceEntry><text> </text><genericError>first</genericError>",
				"<textError>    ^^^^^</textError>",
				"<text>  </text><punctuationSequenceEntry>-</punctuationSequenceEntry><text> </text><literalString>second</literalString>",
			),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			err := yamltest.Bind(t, xmlSource(tc.source), niceyaml.NewError(
				tc.errMsg,
				tc.loc,
			))

			assert.Equal(t, tc.want, trimLines(render(err)))
		})
	}
}

func TestWithPrinter(t *testing.T) {
	t.Parallel()

	source := stringtest.Input(`
		key: value
		foo: bar
	`)
	tokens := lexer.Tokenize(source)

	customPrinter := printer.New(
		printer.WithStyles(yamltest.NewXMLStyles()),
		printer.WithGutter(printer.NoGutter),
		printer.WithContainerStyle(lipgloss.NewStyle()),
	)

	err := yamltest.Bind(t, niceyaml.NewSourceFromString(source), niceyaml.NewError(
		"test error",
		niceyaml.AtPosition(position.NewFromToken(tokens[0])),
	))

	want := stringtest.JoinLF(
		"1:1: test error",
		"",
		"<genericError>key</genericError><punctuationMappingValue>:</punctuationMappingValue><text> </text><literalString>value</literalString>",
		"<textError>^^^</textError>",
		"<nameTag>foo</nameTag><punctuationMappingValue>:</punctuationMappingValue><text> </text><literalString>bar</literalString>",
	)

	var bound *niceyaml.SourceError

	require.ErrorAs(t, err, &bound)
	assert.Equal(t, want, trimLines(customPrinter.PrintError(bound)))
}

func TestError_SpecialParentContext(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		source string
		loc    niceyaml.ErrorOption
		errMsg string
		want   string
	}{
		"root level array - element has no key": {
			// A key target on a sequence element falls back to the element.
			source: stringtest.Input(`
				- first
				- second
				- third
			`),
			loc:    niceyaml.AtPath(paths.Root().Index(1).Key()),
			errMsg: "array element error",
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
			source: stringtest.Input(`
				key: value
				another: line
			`),
			loc:    niceyaml.AtPath(paths.Root().Key()),
			errMsg: "document root error",
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

			err := yamltest.Bind(t, xmlSource(tc.source), niceyaml.NewError(
				tc.errMsg,
				tc.loc,
			))

			assert.Equal(t, tc.want, trimLines(render(err)))
		})
	}
}

func TestError_Unwrap(t *testing.T) {
	t.Parallel()

	t.Run("a nil Error unwraps to nothing", func(t *testing.T) {
		t.Parallel()

		var nilErr *niceyaml.Error

		assert.Nil(t, nilErr.Unwrap())

		// A chain that holds a nil Error behind a real one is safe to walk.
		outer := niceyaml.WrapError(nilErr)

		var bound *niceyaml.SourceError

		assert.NotErrorAs(t, outer, &bound)
	})

	t.Run("unwraps underlying error", func(t *testing.T) {
		t.Parallel()

		underlying := errors.New("underlying error")
		err := niceyaml.WrapError(underlying)

		got := err.Unwrap()

		require.Len(t, got, 1)
		assert.Equal(t, underlying, got[0])
	})

	t.Run("nil error unwraps to nil", func(t *testing.T) {
		t.Parallel()

		err := niceyaml.WrapError(nil)

		got := err.Unwrap()

		assert.Nil(t, got)
	})

	t.Run("errors.Is works through Error wrapper", func(t *testing.T) {
		t.Parallel()

		sentinel := errors.New("sentinel error")
		err := niceyaml.WrapError(sentinel)

		require.ErrorIs(t, err, sentinel)
	})

	t.Run("unwraps nested errors", func(t *testing.T) {
		t.Parallel()

		underlying := errors.New("main error")
		nested1 := errors.New("nested error 1")
		nested2 := errors.New("nested error 2")

		err := niceyaml.WrapError(underlying,
			niceyaml.WithErrors(
				niceyaml.WrapError(nested1),
				niceyaml.WrapError(nested2),
			),
		)

		got := err.Unwrap()

		require.Len(t, got, 3)
		assert.Equal(t, underlying, got[0])
		// The error matches each nested error under require.ErrorIs.
		require.ErrorIs(t, err, nested1)
		require.ErrorIs(t, err, nested2)
	})

	t.Run("skips nil nested errors", func(t *testing.T) {
		t.Parallel()

		underlying := errors.New("main error")
		nested := errors.New("nested error")

		err := niceyaml.WrapError(underlying,
			niceyaml.WithErrors(
				nil,
				niceyaml.WrapError(nested),
				nil,
			),
		)

		got := err.Unwrap()

		require.Len(t, got, 2)
		assert.Equal(t, underlying, got[0])
	})
}

func TestError_MultiError(t *testing.T) {
	t.Parallel()

	t.Run("single nested error with path", func(t *testing.T) {
		t.Parallel()

		source := stringtest.Input(`
			name: test
			value: 123
			other: data
		`)

		err := yamltest.Bind(t, xmlSource(source), niceyaml.NewError(
			"validation failed",
			niceyaml.AtPath(paths.Root().Child("name").Key()),
			niceyaml.WithErrors(
				niceyaml.NewError(
					"invalid type",
					niceyaml.AtPath(paths.Root().Child("value")),
				),
			),
		))

		got := trimLines(render(err))

		// The output highlights the main token and annotates the nested error.
		assert.Contains(t, got, "<genericError>name</genericError>")
		assert.Contains(t, got, "<genericError>123</genericError>")
		assert.Contains(t, got, "^ invalid type")
	})

	t.Run("multiple nested errors on different lines", func(t *testing.T) {
		t.Parallel()

		source := stringtest.Input(`
			name: test
			value: 123
			other: data
		`)

		err := yamltest.Bind(t, xmlSource(source), niceyaml.NewError(
			"validation failed",
			niceyaml.AtPath(paths.Root().Child("name").Key()),
			niceyaml.WithErrors(
				niceyaml.NewError(
					"invalid type",
					niceyaml.AtPath(paths.Root().Child("value")),
				),
				niceyaml.NewError(
					"missing field",
					niceyaml.AtPath(paths.Root().Child("other").Key()),
				),
			),
		))

		got := trimLines(render(err))

		// The output holds both annotations.
		assert.Contains(t, got, "^ invalid type")
		assert.Contains(t, got, "^ missing field")
	})

	t.Run("multiple nested errors on same line", func(t *testing.T) {
		t.Parallel()

		source := stringtest.Input(`
			key: value
		`)
		tokens := lexer.Tokenize(source)

		// Both errors point to the same line.
		err := yamltest.Bind(t, xmlSource(source), niceyaml.NewError(
			"validation failed",
			niceyaml.AtPosition(position.NewFromToken(tokens[0])),
			niceyaml.WithErrors(
				niceyaml.NewError(
					"error1",
					niceyaml.AtPath(paths.Root().Child("key").Key()),
				),
				niceyaml.NewError(
					"error2",
					niceyaml.AtPath(paths.Root().Child("key")),
				),
			),
		))

		got := trimLines(render(err))

		// Errors on the same line combine with "; ".
		assert.Contains(t, got, "error1; error2")
	})

	t.Run("nested errors on same line out of column order", func(t *testing.T) {
		t.Parallel()

		source := stringtest.Input(`
			key: [alpha, beta]
		`)

		// The children arrive in reverse column order.
		err := yamltest.Bind(t, xmlSource(source), niceyaml.NewError(
			"2 problems",
			niceyaml.WithErrors(
				niceyaml.NewError(
					"bad beta",
					niceyaml.AtPath(paths.Root().Child("key").Index(1)),
				),
				niceyaml.NewError(
					"bad alpha",
					niceyaml.AtPath(paths.Root().Child("key").Index(0)),
				),
			),
		))

		got := trimLines(render(err))

		// The joined messages follow the column order of the carets.
		assert.Contains(t, got, "^ bad alpha; bad beta")
	})

	t.Run("nested error with direct token", func(t *testing.T) {
		t.Parallel()

		source := stringtest.Input(`
			key: value
			foo: bar
		`)
		tokens := lexer.Tokenize(source)

		// Find the "foo" token by iterating through tokens.
		var fooToken *token.Token

		for _, tk := range tokens {
			if tk.Value == "foo" {
				fooToken = tk
				break
			}
		}

		require.NotNil(t, fooToken, "failed to find foo token")

		err := yamltest.Bind(t, xmlSource(source), niceyaml.NewError(
			"main error",
			niceyaml.AtPosition(position.NewFromToken(tokens[0])),
			niceyaml.WithErrors(
				niceyaml.NewError(
					"nested with token",
					niceyaml.AtPosition(position.NewFromToken(fooToken)),
				),
			),
		))

		got := trimLines(render(err))

		// The output highlights both tokens.
		assert.Contains(t, got, "<genericError>key</genericError>")
		assert.Contains(t, got, "<genericError>foo</genericError>")
		assert.Contains(t, got, "^ nested with token")
	})

	t.Run("nested error that does not resolve stays in the message", func(t *testing.T) {
		t.Parallel()

		source := stringtest.Input(`
			key: value
		`)

		err := yamltest.Bind(t, xmlSource(source), niceyaml.NewError(
			"main error",
			niceyaml.AtPath(paths.Root().Child("key").Key()),
			niceyaml.WithErrors(
				niceyaml.NewError(
					"nested error",
					niceyaml.AtPath(paths.Root().Child("nonexistent").Key()),
				),
			),
		))

		got := trimLines(render(err))

		// The nested error has no line to annotate, so the message is the
		// only place it appears.
		assert.True(t, strings.HasPrefix(got, "1:1: $.key~: main error\n└── $.nonexistent~: nested error\n\n"), got)
		assert.Contains(t, got, "<genericError>key</genericError>")
		assert.NotContains(t, got, "^ nested error")
		assert.NotContains(t, got, "\n\n$.nonexistent~")
	})

	t.Run("plain error with nested errors keeps its own message", func(t *testing.T) {
		t.Parallel()

		nested1 := niceyaml.NewError("nested 1")
		nested2 := niceyaml.NewError("nested 2")
		err := niceyaml.NewError("main error", niceyaml.WithErrors(nested1, nested2))

		// The nested errors are structure, not text. Errors returns them
		// and they surface through Unwrap, while the message stays one
		// line and %+v lists them as a tree under it.
		assert.Equal(t, "main error", err.Error())
		assert.Equal(t, "main error\n|-- nested 1\n`-- nested 2", render(err))
		assert.Equal(t, []error{nested1, nested2}, err.Errors())
		require.ErrorIs(t, err, nested1)
		require.ErrorIs(t, err, nested2)
	})

	t.Run("nested error without path or token stays in the message", func(t *testing.T) {
		t.Parallel()

		source := stringtest.Input(`
			key: value
		`)

		err := yamltest.Bind(t, xmlSource(source), niceyaml.NewError(
			"main error",
			niceyaml.AtPath(paths.Root().Child("key").Key()),
			niceyaml.WithErrors(
				niceyaml.NewError("no location"),
			),
		))

		got := trimLines(render(err))

		assert.True(t, strings.HasPrefix(got, "1:1: $.key~: main error\n└── no location\n\n"), got)
		assert.NotContains(t, got, "^ no location")
		assert.False(t, strings.HasSuffix(got, "\n\nno location"), got)
	})

	t.Run("errors.Is works with nested errors", func(t *testing.T) {
		t.Parallel()

		sentinel1 := errors.New("sentinel 1")
		sentinel2 := errors.New("sentinel 2")

		err := niceyaml.NewError(
			"main error",
			niceyaml.WithErrors(
				niceyaml.WrapError(sentinel1),
				niceyaml.WrapError(sentinel2),
			),
		)

		require.ErrorIs(t, err, sentinel1)
		require.ErrorIs(t, err, sentinel2)
	})

	t.Run("errors.As works with nested errors", func(t *testing.T) {
		t.Parallel()

		customErr := &customTestError{msg: "custom error"}

		err := niceyaml.NewError(
			"main",
			niceyaml.WithErrors(
				niceyaml.WrapError(customErr),
			),
		)

		// The errors.As function finds the custom error through the nested errors.
		var target *customTestError

		require.ErrorAs(t, err, &target)
		assert.Equal(t, "custom error", target.msg)
	})

	t.Run("nested-only error renders with annotations", func(t *testing.T) {
		t.Parallel()

		source := stringtest.Input(`
			name: test
			value: 123
		`)

		// The error has no path of its own, and its nested error has one.
		err := yamltest.Bind(t, xmlSource(source), niceyaml.NewError(
			"validation failed at 1 location",
			niceyaml.WithErrors(
				niceyaml.NewError(
					"got number, want string",
					niceyaml.AtPath(paths.Root().Child("value")),
				),
			),
		))

		got := trimLines(render(err))

		// The output holds the main message.
		assert.Contains(t, got, "validation failed at 1 location")
		// The output holds the annotation of the nested error.
		assert.Contains(t, got, "got number, want string")
		// The output holds the YAML content as well as the tree.
		assert.Contains(t, got, "value")
		assert.Contains(t, got, "123")
		// The output highlights the value of the nested error.
		assert.Contains(t, got, "<genericError>123</genericError>")
	})

	t.Run("nested-only error without source falls back to plain", func(t *testing.T) {
		t.Parallel()

		// The error has no path and no source, and its nested error has a path.
		err := niceyaml.NewError(
			"validation failed",
			niceyaml.WithErrors(
				niceyaml.NewError(
					"nested error",
					niceyaml.AtPath(paths.Root().Child("value")),
				),
			),
		)

		// Without a source, the tree renders with no excerpt, and the
		// nested error waits for a binding to put its position in front.
		assert.Equal(t, "validation failed", err.Error())
		assert.Equal(t, "validation failed\n`-- $.value: nested error", render(err))
	})

	t.Run("nested-only error with multiple lines", func(t *testing.T) {
		t.Parallel()

		source := stringtest.Input(`
			name: test
			value: 123
			other: data
		`)

		// The nested errors sit on different lines.
		err := yamltest.Bind(t, xmlSource(source), niceyaml.NewError(
			"validation failed at 2 locations",
			niceyaml.WithErrors(
				niceyaml.NewError(
					"type error on value",
					niceyaml.AtPath(paths.Root().Child("value")),
				),
				niceyaml.NewError(
					"unexpected property",
					niceyaml.AtPath(paths.Root().Child("other").Key()),
				),
			),
		))

		got := trimLines(render(err))

		// The output holds both annotations.
		assert.Contains(t, got, "type error on value")
		assert.Contains(t, got, "unexpected property")
		// The output highlights both locations.
		assert.Contains(t, got, "<genericError>123</genericError>")
		assert.Contains(t, got, "<genericError>other</genericError>")
	})

	t.Run("nested-only error with resolvable and unresolvable nested errors", func(t *testing.T) {
		t.Parallel()

		source := stringtest.Input(`
			name: test
			value: 123
		`)

		// Some nested errors resolve and some do not.
		err := yamltest.Bind(t, xmlSource(source), niceyaml.NewError(
			"validation failed",
			niceyaml.WithErrors(
				niceyaml.NewError(
					"resolvable error",
					niceyaml.AtPath(paths.Root().Child("value")),
				),
				niceyaml.NewError(
					"unresolvable error",
					niceyaml.AtPath(paths.Root().Child("nonexistent").Key()),
				),
			),
		))

		got := trimLines(render(err))

		// Both nested errors are part of the message, and only the one that
		// resolves annotates the excerpt.
		assert.True(t, strings.HasPrefix(got,
			"validation failed\n├── 2:8: $.value: resolvable error\n└── $.nonexistent~: unresolvable error\n\n"), got)
		assert.Contains(t, got, "^ resolvable error")
		assert.NotContains(t, got, "^ unresolvable error")
		assert.NotContains(t, got, "\n\n$.nonexistent~")
	})

	t.Run("nested-only error with nested error that has no path or token", func(t *testing.T) {
		t.Parallel()

		source := stringtest.Input(`
			name: test
			value: 123
		`)

		// No nested error has a location, so nothing annotates the source
		// and the message is the whole output.
		err := yamltest.Bind(t, niceyaml.NewSourceFromString(source), niceyaml.NewError(
			"validation failed",
			niceyaml.WithErrors(
				niceyaml.NewError("nested without location"),
			),
		))

		assert.Equal(t, "validation failed\n└── nested without location", render(err))
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
			err: yamltest.Bind(t, niceyaml.NewSourceFromString(source), niceyaml.NewError(
				"main error",
			)),
			want: "main error",
		},
		"nested errors without locations": {
			err: yamltest.Bind(t, niceyaml.NewSourceFromString(source), niceyaml.NewError(
				"main error",
				niceyaml.WithErrors(
					niceyaml.NewError("nested 1"),
					niceyaml.NewError("nested 2"),
				),
			)),
			want: stringtest.JoinLF(
				"main error",
				"├── nested 1",
				"└── nested 2",
			),
		},
		"nested error with path": {
			err: yamltest.Bind(t, xmlSource(source), niceyaml.NewError(
				"main error",
				niceyaml.WithErrors(
					niceyaml.NewError(
						"nested with path",
						niceyaml.AtPath(paths.Root().Child("key").Key()),
					),
				),
			)),
			want: stringtest.JoinLF(
				"main error",
				"└── 1:1: $.key~: nested with path",
				"",
				"<genericError>key</genericError><punctuationMappingValue>:</punctuationMappingValue><text> </text><literalString>value</literalString>",
				"<textError>^ nested with path</textError>",
				"<nameTag>other</nameTag><punctuationMappingValue>:</punctuationMappingValue><text> </text><literalString>data</literalString>",
			),
		},
		"nested error with token": {
			err: func() error {
				tokens := lexer.Tokenize(source)

				return yamltest.Bind(t, xmlSource(source), niceyaml.NewError(
					"main error",
					niceyaml.WithErrors(
						niceyaml.NewError(
							"nested with token",
							niceyaml.AtPosition(position.NewFromToken(tokens[0])),
						),
					),
				))
			}(),
			want: stringtest.JoinLF(
				"main error",
				"└── 1:1: nested with token",
				"",
				"<genericError>key</genericError><punctuationMappingValue>:</punctuationMappingValue><text> </text><literalString>value</literalString>",
				"<textError>^ nested with token</textError>",
				"<nameTag>other</nameTag><punctuationMappingValue>:</punctuationMappingValue><text> </text><literalString>data</literalString>",
			),
		},
		"mix of located and unlocated nested errors": {
			err: yamltest.Bind(t, xmlSource(source), niceyaml.NewError(
				"main error",
				niceyaml.WithErrors(
					niceyaml.NewError("no path"),
					niceyaml.NewError(
						"has path",
						niceyaml.AtPath(paths.Root().Child("key").Key()),
					),
					niceyaml.NewError("also no path"),
				),
			)),
			want: stringtest.JoinLF(
				"main error",
				"├── 1:1: $.key~: has path",
				"├── no path",
				"└── also no path",
				"",
				"<genericError>key</genericError><punctuationMappingValue>:</punctuationMappingValue><text> </text><literalString>value</literalString>",
				"<textError>^ has path</textError>",
				"<nameTag>other</nameTag><punctuationMappingValue>:</punctuationMappingValue><text> </text><literalString>data</literalString>",
			),
		},
		"nil nested errors only": {
			err: yamltest.Bind(t, niceyaml.NewSourceFromString(source), niceyaml.NewError(
				"main error",
				niceyaml.WithErrors(nil, nil),
			)),
			want: "main error",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, trimLines(render(tc.err)))
		})
	}
}

func TestSourceError_UnresolvedNestedInTree(t *testing.T) {
	t.Parallel()

	source := niceyaml.NewSourceFromString("a: 1\n")
	err := yamltest.Bind(t, source, niceyaml.NewError(
		"2 schema violations",
		niceyaml.WithErrors(
			niceyaml.NewError("bad x", niceyaml.AtExactPath(paths.Root().Child("x"))),
			niceyaml.NewError("bad y", niceyaml.AtExactPath(paths.Root().Child("y"))),
		),
	))

	// The %+v verb lists each nested error behind its path, and since no
	// location resolves, the printer adds nothing beyond the connectors of
	// the tree.
	assert.Equal(t, "2 schema violations", err.Error())
	assert.Equal(t, "2 schema violations\n|-- $.x: bad x\n`-- $.y: bad y", report(err))
	assert.Equal(t, "2 schema violations\n├── $.x: bad x\n└── $.y: bad y", render(err))

	// The summary carries no location of its own, so it has no reason to
	// give, and each nested error names why its path did not resolve.
	var bound *niceyaml.SourceError

	require.ErrorAs(t, err, &bound)

	_, ok := bound.Excerpt(2)
	assert.False(t, ok)
	require.NoError(t, bound.Unresolved())

	children := bound.Errors()
	require.Len(t, children, 2)

	for _, child := range children {
		require.ErrorIs(t, child.Unresolved(), paths.ErrNotFound)
	}
}

func TestSourceError_Format(t *testing.T) {
	t.Parallel()

	source := niceyaml.NewSourceFromString("a: 1\n")
	err := yamltest.Bind(t, source, niceyaml.NewError("x", niceyaml.AtPath(paths.Root().Child("a"))))

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

	source := niceyaml.NewSourceFromString("a: 1\nb: 2\nc: 3\n")

	t.Run("marks the location without escape sequences", func(t *testing.T) {
		t.Parallel()

		err := yamltest.Bind(t, source, niceyaml.NewError("bad value", niceyaml.AtPath(paths.Root().Child("b"))))

		got := fmt.Sprintf("%+v", err)

		assert.Equal(t, stringtest.JoinLF(
			"2:4: $.b: bad value",
			"",
			"   1 | a: 1",
			"   2 | b: 2",
			"     |    ^",
			"   3 | c: 3",
		), got)
		assert.NotContains(t, got, "\x1b")
	})

	t.Run("carets sit under wide runes", func(t *testing.T) {
		t.Parallel()

		// Each CJK rune renders two cells wide, so the columns before the
		// marked ones are worth two carets each.
		wide := niceyaml.NewSourceFromString("名前: value\n")
		namePath := paths.Root().Child("名前")

		value := yamltest.Bind(t, wide, niceyaml.NewError("bad value", niceyaml.AtPath(namePath)))

		assert.Equal(t, stringtest.JoinLF(
			"1:5: $.名前: bad value",
			"",
			"   1 | 名前: value",
			"     |       ^^^^^",
		), fmt.Sprintf("%+v", value))

		key := yamltest.Bind(t, wide, niceyaml.NewError("bad key", niceyaml.AtPath(namePath.Key())))

		assert.Equal(t, stringtest.JoinLF(
			"1:1: $.名前~: bad key",
			"",
			"   1 | 名前: value",
			"     | ^^^^",
		), fmt.Sprintf("%+v", key))
	})

	t.Run("nested errors annotate their lines", func(t *testing.T) {
		t.Parallel()

		err := yamltest.Bind(t, source, niceyaml.NewError("2 problems", niceyaml.WithErrors(
			niceyaml.NewError("bad a", niceyaml.AtPath(paths.Root().Child("a").Key())),
			niceyaml.NewError("bad c", niceyaml.AtPath(paths.Root().Child("c"))),
		)))

		assert.Equal(t, stringtest.JoinLF(
			"2 problems",
			"|-- 1:1: $.a~: bad a",
			"`-- 3:4: $.c: bad c",
			"",
			"   1 | a: 1",
			"     | ^ bad a",
			"   2 | b: 2",
			"   3 | c: 3",
			"     |    ^ bad c",
		), fmt.Sprintf("%+v", err))
	})

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

	t.Run("names why no excerpt resolves", func(t *testing.T) {
		t.Parallel()

		src := niceyaml.NewSourceFromString("a: 1\n")
		err := yamltest.Bind(t, src, niceyaml.NewError("bad value", niceyaml.AtExactPath(paths.Root().Child("b"))))

		assert.Equal(t,
			"$.b: bad value\n\nno excerpt: resolve $.b: not found",
			fmt.Sprintf("%+v", err),
		)
	})

	t.Run("control characters in the tree and the reason render as pictures", func(t *testing.T) {
		t.Parallel()

		// The message and the path spell a key of the document, so an
		// escape sequence in either renders as its picture in the tree
		// and in the reason a location did not resolve.
		src := niceyaml.NewSourceFromString("a: 1\n", niceyaml.WithName("f"))
		err := yamltest.Bind(t, src, niceyaml.NewError(
			"m\x1b[31mX",
			niceyaml.AtExactPath(paths.Root().Child("k\x1b[31mY")),
		))

		got := niceyaml.FormatError(err, 0)
		assert.NotContains(t, got, "\x1b")
		assert.Contains(t, got, "m\u241b[31mX")
		assert.Contains(t, got, "no excerpt: resolve $.'k\u241b[31mY'")
	})

	t.Run("control characters render as pictures", func(t *testing.T) {
		t.Parallel()

		src := niceyaml.NewSourceFromString("a: \"x\\ty\"\n")
		err := yamltest.Bind(t, src, niceyaml.NewError("bad", niceyaml.AtRange(
			position.NewRange(position.New(0, 3), position.New(0, 9)),
		)))

		assert.Equal(t, stringtest.JoinLF(
			"1:4: bad",
			"",
			"   1 | a: \"x\\ty\"",
			"     |    ^^^^^^",
		), fmt.Sprintf("%+v", err))
	})

	t.Run("a location with no token under it gets a caret", func(t *testing.T) {
		t.Parallel()

		// The root carries no message in the excerpt, so the caret alone
		// marks where a location with nothing to highlight sits.
		tcs := map[string]struct {
			input string
			opt   niceyaml.ErrorOption
			want  string
		}{
			"path to an empty value": {
				input: "a:\nb: 2\n",
				opt:   niceyaml.AtPath(paths.Root().Child("a")),
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
				opt:   niceyaml.AtPath(paths.Root().Child("a")),
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
				opt:   niceyaml.AtPath(paths.Root().Child("a")),
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
				opt:   niceyaml.AtPath(paths.Root().Index(0)),
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
				opt:   niceyaml.AtPosition(position.New(0, 500)),
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

				src := niceyaml.NewSourceFromString(tc.input)
				err := yamltest.Bind(t, src, niceyaml.NewError("bad", tc.opt))

				assert.Equal(t, tc.want, fmt.Sprintf("%+v", err))
			})
		}
	})
}

func TestError_NilReceiver(t *testing.T) {
	t.Parallel()

	// A nil *Error carries no message and specializes to nothing, and one
	// wrapped by another Error contributes nothing to the message, in line
	// with Cause, Errors, Unwrap, and Path, which all accept nil.
	var missing *niceyaml.Error

	assert.Empty(t, missing.Error())
	assert.Nil(t, missing.With(niceyaml.AtPath(paths.Root().Child("a"))))

	wrapped := niceyaml.WrapError(missing, niceyaml.AtPath(paths.Root().Child("a")))
	assert.Equal(t, "$.a:", wrapped.Error())
}

func TestSourceError_NilReceiver(t *testing.T) {
	t.Parallel()

	// A nil *SourceError reaches a reader through errors.As on a chain that
	// holds one, so every reader of one accepts nil the way Unwrap does.
	src := niceyaml.NewSourceFromString("a: 1\n")

	var missing *niceyaml.SourceError

	assert.Empty(t, missing.Error())
	assert.Empty(t, fmt.Sprintf("%+v", missing))
	assert.Nil(t, missing.Source())
	assert.Nil(t, missing.Document())
	assert.Nil(t, missing.Errors())
	assert.NoError(t, missing.Unwrap()) //nolint:testifylint // Asserts the nil, not a test failure.

	_, resolved := missing.Range()
	require.False(t, resolved)
	require.NoError(t, missing.Unresolved())

	_, excerpted := missing.Excerpt(2)
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
			input: "a: 1\n---\n",
			want: stringtest.JoinLF(
				"2:1: $: required property 'a' missing",
				"",
				"   1 | a: 1",
				"   2 | ---",
				"     | ^^^",
			),
		},
		"a comment below the header": {
			input: "a: 1\n---\n# comment\n",
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

			docs, err := niceyaml.NewSourceFromString(tc.input).Documents()
			require.NoError(t, err)
			require.Len(t, docs, 2)

			bound := docs[1].Bind(niceyaml.NewError("required property 'a' missing", niceyaml.AtPath(paths.Root())))

			var se *niceyaml.SourceError

			require.ErrorAs(t, bound, &se)

			rng, ok := se.Range()
			require.True(t, ok)
			assert.Equal(t, 1, rng.Start.Line)

			assert.Equal(t, "2:1: $: required property 'a' missing", se.Error())
			assert.Equal(t, tc.want, fmt.Sprintf("%+v", bound))
		})
	}
}

func TestError_NilInnerError(t *testing.T) {
	t.Parallel()

	err := niceyaml.WrapError(niceyaml.WrapError(nil))
	assert.Empty(t, err.Error())

	wrapped := yamltest.Bind(t, niceyaml.NewSourceFromString("a: 1\n"), err)
	assert.Empty(t, wrapped.Error())
	assert.Empty(t, render(wrapped))
}

func TestError_NilInnerErrorWithLocation(t *testing.T) {
	t.Parallel()

	source := xmlSource("a: 1\nb: 2\n")
	tk := source.Lines().TokenAt(position.New(0, 3))
	require.NotNil(t, tk)

	// An Error from a nil error has no message, so its text is the path it
	// carries and a colon, or nothing, and binding puts the resolved
	// position in front.
	tcs := map[string]struct {
		err       *niceyaml.Error
		want      string
		wantBound string
	}{
		"token": {
			err:       niceyaml.WrapError(nil, niceyaml.AtPosition(position.NewFromToken(tk))),
			want:      "",
			wantBound: "1:4:",
		},
		"range": {
			err: niceyaml.WrapError(nil,
				niceyaml.AtRange(position.NewRange(position.New(1, 3), position.New(1, 4))),
			),
			want:      "",
			wantBound: "2:4:",
		},
		"path": {
			err:       niceyaml.WrapError(nil, niceyaml.AtPath(paths.Root().Child("b"))),
			want:      "$.b:",
			wantBound: "2:4: $.b:",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, tc.err.Error())

			bound := yamltest.Bind(t, source, tc.err)
			assert.Equal(t, tc.wantBound, bound.Error())

			got := trimLines(render(bound))
			assert.True(t, strings.HasPrefix(got, tc.wantBound+"\n\n"), got)
			assert.Contains(t, got, "genericError")
		})
	}
}

func TestError_NestedErrorsKeepInnerPosition(t *testing.T) {
	t.Parallel()

	source := niceyaml.NewSourceFromString("a: 1\nb: 2\n")
	inner := niceyaml.NewError("bad a", niceyaml.AtPath(paths.Root().Child("a")))
	nested := niceyaml.NewError("bad b", niceyaml.AtPath(paths.Root().Child("b")))

	// Nested errors on the wrapper become its children and add annotations,
	// and the wrapper still takes its position from the Error it wraps.
	wrapped := yamltest.Bind(t, source, niceyaml.WrapError(inner, niceyaml.WithErrors(nested)))
	assert.Equal(t, "1:4: $.a: bad a", wrapped.Error())
	assert.Equal(t, "1:4: $.a: bad a\n`-- 2:4: $.b: bad b", report(wrapped))

	var bound *niceyaml.SourceError

	require.ErrorAs(t, wrapped, &bound)

	rng, ok := bound.Range()
	require.True(t, ok)
	assert.Equal(t, 0, rng.Start.Line)
	assert.Equal(t, 3, rng.Start.Col)

	got := trimLines(render(wrapped))
	assert.Contains(t, got, "<genericError>1</genericError>")
	assert.Contains(t, got, "^ bad b")
}

func TestError_NestedLocationWithoutMessage(t *testing.T) {
	t.Parallel()

	source := niceyaml.NewSourceFromString("a: 1\nb: 2\n")
	inner := niceyaml.NewError("bad a", niceyaml.AtPath(paths.Root().Child("a")))
	nested := niceyaml.WrapError(nil, niceyaml.AtPath(paths.Root().Child("b")))

	// A nested Error built from a nil error names a location and nothing
	// else, so the printer highlights that location and marks it with a
	// caret run alone, with no message beside it.
	wrapped := yamltest.Bind(t, source, niceyaml.WrapError(inner, niceyaml.WithErrors(nested)))

	got := trimLines(render(wrapped))
	assert.Contains(t, got, "<genericError>1</genericError>\n<textError>   ^</textError>")
	assert.Contains(t, got, "<genericError>2</genericError>\n<textError>   ^</textError>")
	assert.NotContains(t, got, "^ ")
}

func TestError_NestedErrorExcerpt(t *testing.T) {
	t.Parallel()

	// These subtests check which source lines the excerpt shows for
	// nested errors.

	t.Run("single nested error shows correct context lines", func(t *testing.T) {
		t.Parallel()

		source := stringtest.Input(`
			line1: a
			line2: b
			line3: c
			line4: d
			line5: e
			line6: f
		`)

		err := yamltest.Bind(t, xmlSource(source), niceyaml.NewError(
			"validation error",
			niceyaml.WithErrors(
				niceyaml.NewError(
					"error at line3",
					niceyaml.AtPath(paths.Root().Child("line3").Key()),
				),
			),
		))

		got := trimLines(renderContext(err, 1))

		// The excerpt shows context around line3.
		assert.Contains(t, got, "line2")
		assert.Contains(t, got, "line3")
		assert.Contains(t, got, "line4")
		assert.NotContains(t, got, "line1")
		assert.NotContains(t, got, "line5")
	})

	t.Run("nested errors on distant lines show separate hunks", func(t *testing.T) {
		t.Parallel()

		source := stringtest.Input(`
			line1: a
			line2: b
			line3: c
			line4: d
			line5: e
			line6: f
		`)

		err := yamltest.Bind(t, xmlSource(source), niceyaml.NewError(
			"validation error", // No extra context.
			niceyaml.WithErrors(
				niceyaml.NewError(
					"error at line1",
					niceyaml.AtPath(paths.Root().Child("line1").Key()),
				),
				niceyaml.NewError(
					"error at line6",
					niceyaml.AtPath(paths.Root().Child("line6").Key()),
				),
			),
		))

		assert.Equal(t, stringtest.JoinLF(
			"validation error",
			"├── 1:1: $.line1~: error at line1",
			"└── 6:1: $.line6~: error at line6",
			"",
			"<genericError>line1</genericError><punctuationMappingValue>:</punctuationMappingValue><text> </text><literalString>a</literalString>",
			"<textError>^ error at line1</textError>",
			"<uiSeparator>...</uiSeparator>",
			"<genericError>line6</genericError><punctuationMappingValue>:</punctuationMappingValue><text> </text><literalString>f</literalString>",
			"<textError>^ error at line6</textError>",
		), trimLines(renderContext(err, 0)))
	})

	t.Run("nested errors on one line share an annotation", func(t *testing.T) {
		t.Parallel()

		source := stringtest.Input(`
			line1: a
			line2: b
			line3: c
		`)

		err := yamltest.Bind(t, xmlSource(source), niceyaml.NewError(
			"validation error",
			niceyaml.WithErrors(
				niceyaml.NewError(
					"error 1",
					niceyaml.AtPath(paths.Root().Child("line2").Key()),
				),
				niceyaml.NewError(
					"error 2",
					niceyaml.AtPath(paths.Root().Child("line2")),
				),
			),
		))

		got := trimLines(renderContext(err, 0))

		// The excerpt shows line2 with both errors.
		assert.Contains(t, got, "line2")
		assert.Contains(t, got, "error 1; error 2")
	})
}

func TestError_HunkDisplay(t *testing.T) {
	t.Parallel()

	t.Run("distant errors show separate hunks with separator", func(t *testing.T) {
		t.Parallel()

		// The source has many lines.
		source := stringtest.Input(`
			line1: a
			line2: b
			line3: c
			line4: d
			line5: e
			line6: f
			line7: g
			line8: h
			line9: i
			line10: j
		`)

		// Errors at line1 and line10 with contextLines=1 fall in separate hunks.
		err := yamltest.Bind(t, xmlSource(source), niceyaml.NewError(
			"validation error",
			niceyaml.WithErrors(
				niceyaml.NewError(
					"error at start",
					niceyaml.AtPath(paths.Root().Child("line1").Key()),
				),
				niceyaml.NewError(
					"error at end",
					niceyaml.AtPath(paths.Root().Child("line10").Key()),
				),
			),
		))

		got := trimLines(renderContext(err, 1))

		// The excerpt shows both errors.
		assert.Contains(t, got, "error at start")
		assert.Contains(t, got, "error at end")
		// A "..." separator sits between the hunks.
		assert.Contains(t, got, "...")
		// The excerpt shows context lines around the errors.
		assert.Contains(t, got, "line1")
		assert.Contains(t, got, "line2")
		assert.Contains(t, got, "line9")
		assert.Contains(t, got, "line10")
		// The excerpt leaves out lines 3-8.
		assert.NotContains(t, got, "line5")
	})

	t.Run("position with no token under it still picks its excerpt", func(t *testing.T) {
		t.Parallel()

		source := stringtest.Input(`
			line1: a
			line2: b
			line3: c
			line4: d
			line5: e
			line6: f
			line7: g
			line8: h
			line9: i
			line10: j
		`)

		// The position points past the end of line 5, where the view holds
		// no token, so there is nothing to highlight.
		err := yamltest.Bind(t, xmlSource(source), niceyaml.NewError(
			"boom",
			niceyaml.AtPosition(position.New(4, 49)),
		))

		got := trimLines(render(err))

		// The excerpt still centers on line 5 with the default two lines of
		// context on either side.
		assert.Contains(t, got, "line3")
		assert.Contains(t, got, "line5")
		assert.Contains(t, got, "line7")
		assert.NotContains(t, got, "line2:")
		assert.NotContains(t, got, "line8")
		assert.NotContains(t, got, "genericError")
	})

	t.Run("path to an empty value still picks its excerpt", func(t *testing.T) {
		t.Parallel()

		source := stringtest.Input(`
			line1: a
			line2: b
			line3: c
			line4:
			line5: e
			line6: f
			line7: g
			line8: h
			line9: i
			line10: j
		`)

		err := yamltest.Bind(t, xmlSource(source), niceyaml.NewError(
			"missing",
			niceyaml.AtPath(paths.Root().Child("line4")),
		))

		got := trimLines(render(err))

		assert.Contains(t, got, "line2")
		assert.Contains(t, got, "line4")
		assert.Contains(t, got, "line6")
		assert.NotContains(t, got, "line1:")
		assert.NotContains(t, got, "line7")
	})

	t.Run("negative context lines show the error lines alone", func(t *testing.T) {
		t.Parallel()

		source := stringtest.Input(`
			line1: a
			line2: b
			line3: c
			line4: d
			line5: e
			line6: f
		`)

		err := yamltest.Bind(t, xmlSource(source), niceyaml.NewError(
			"validation error",
			niceyaml.AtPath(paths.Root().Child("line1").Key()),
			niceyaml.WithErrors(
				niceyaml.NewError(
					"error at end",
					niceyaml.AtPath(paths.Root().Child("line6").Key()),
				),
			),
		))

		// A negative count renders as 0 does, so each error line is a hunk
		// of its own with no context around it.
		want := trimLines(renderContext(err, 0))
		got := trimLines(renderContext(err, -1))

		assert.Equal(t, want, got)
		assert.Contains(t, got, "line1")
		assert.Contains(t, got, "line6")
		assert.Contains(t, got, "...")
		assert.NotContains(t, got, "line2")
		assert.NotContains(t, got, "line5")
	})

	t.Run("close errors merge into single hunk", func(t *testing.T) {
		t.Parallel()

		source := stringtest.Input(`
			line1: a
			line2: b
			line3: c
			line4: d
			line5: e
		`)

		// Errors at line1 and line3 with contextLines=1 merge into one hunk,
		// since they lie within 2*contextLines of each other.
		err := yamltest.Bind(t, xmlSource(source), niceyaml.NewError(
			"validation error",
			niceyaml.WithErrors(
				niceyaml.NewError(
					"first error",
					niceyaml.AtPath(paths.Root().Child("line1").Key()),
				),
				niceyaml.NewError(
					"second error",
					niceyaml.AtPath(paths.Root().Child("line3").Key()),
				),
			),
		))

		// The hunk runs from line1 through line4 with no separator.
		assert.Equal(t, stringtest.JoinLF(
			"validation error",
			"├── 1:1: $.line1~: first error",
			"└── 3:1: $.line3~: second error",
			"",
			"<genericError>line1</genericError><punctuationMappingValue>:</punctuationMappingValue><text> </text><literalString>a</literalString>",
			"<textError>^ first error</textError>",
			"<nameTag>line2</nameTag><punctuationMappingValue>:</punctuationMappingValue><text> </text><literalString>b</literalString>",
			"<genericError>line3</genericError><punctuationMappingValue>:</punctuationMappingValue><text> </text><literalString>c</literalString>",
			"<textError>^ second error</textError>",
			"<nameTag>line4</nameTag><punctuationMappingValue>:</punctuationMappingValue><text> </text><literalString>d</literalString>",
		), trimLines(renderContext(err, 1)))
	})

	t.Run("nested-only distant errors show separate hunks", func(t *testing.T) {
		t.Parallel()

		source := stringtest.Input(`
			line1: a
			line2: b
			line3: c
			line4: d
			line5: e
			line6: f
			line7: g
			line8: h
			line9: i
			line10: j
		`)

		// The error has no path of its own, and its nested errors lie far apart.
		err := yamltest.Bind(t, xmlSource(source), niceyaml.NewError(
			"validation failed at 2 locations",
			niceyaml.WithErrors(
				niceyaml.NewError(
					"first location error",
					niceyaml.AtPath(paths.Root().Child("line1").Key()),
				),
				niceyaml.NewError(
					"second location error",
					niceyaml.AtPath(paths.Root().Child("line10").Key()),
				),
			),
		))

		got := trimLines(renderContext(err, 1))

		// The excerpt shows both errors.
		assert.Contains(t, got, "first location error")
		assert.Contains(t, got, "second location error")
		// A "..." separator sits between the hunks.
		assert.Contains(t, got, "...")
	})

	t.Run("errors at start and end of file", func(t *testing.T) {
		t.Parallel()

		source := stringtest.Input(`
			first: value
			middle1: a
			middle2: b
			middle3: c
			middle4: d
			middle5: e
			last: value
		`)

		// The errors sit on the first and last lines.
		err := yamltest.Bind(t, xmlSource(source), niceyaml.NewError(
			"validation error",
			niceyaml.WithErrors(
				niceyaml.NewError(
					"error at first",
					niceyaml.AtPath(paths.Root().Child("first").Key()),
				),
				niceyaml.NewError(
					"error at last",
					niceyaml.AtPath(paths.Root().Child("last").Key()),
				),
			),
		))

		// The context clips to the file, so the excerpt starts at first and
		// ends at last, with a separator between the two hunks.
		assert.Equal(t, stringtest.JoinLF(
			"validation error",
			"├── 1:1: $.first~: error at first",
			"└── 7:1: $.last~: error at last",
			"",
			"<genericError>first</genericError><punctuationMappingValue>:</punctuationMappingValue><text> </text><literalString>value</literalString>",
			"<textError>^ error at first</textError>",
			"<nameTag>middle1</nameTag><punctuationMappingValue>:</punctuationMappingValue><text> </text><literalString>a</literalString>",
			"<uiSeparator>...</uiSeparator>",
			"<nameTag>middle5</nameTag><punctuationMappingValue>:</punctuationMappingValue><text> </text><literalString>e</literalString>",
			"<genericError>last</genericError><punctuationMappingValue>:</punctuationMappingValue><text> </text><literalString>value</literalString>",
			"<textError>^ error at last</textError>",
		), trimLines(renderContext(err, 1)))
	})

	t.Run("main error and nested in different hunks", func(t *testing.T) {
		t.Parallel()

		source := stringtest.Input(`
			line1: a
			line2: b
			line3: c
			line4: d
			line5: e
			line6: f
			line7: g
			line8: h
			line9: i
			line10: j
		`)

		// The main error sits at line1 and the nested error at line10.
		err := yamltest.Bind(t, xmlSource(source), niceyaml.NewError(
			"main error",
			niceyaml.AtPath(paths.Root().Child("line1").Key()),
			niceyaml.WithErrors(
				niceyaml.NewError(
					"nested error",
					niceyaml.AtPath(paths.Root().Child("line10").Key()),
				),
			),
		))

		got := trimLines(renderContext(err, 1))

		// The excerpt shows both locations.
		assert.Contains(t, got, "<genericError>line1</genericError>")
		assert.Contains(t, got, "<genericError>line10</genericError>")
		// The excerpt shows the annotation of the nested error.
		assert.Contains(t, got, "nested error")
		// A "..." separator sits between the hunks.
		assert.Contains(t, got, "...")
	})

	t.Run("adjacent errors with no context merge into single hunk", func(t *testing.T) {
		t.Parallel()

		source := stringtest.Input(`
			line1: a
			line2: b
			line3: c
		`)

		// With contextLines=0, errors at line1 and line2 merge because they
		// lie within the threshold (2*0+1=1) of each other.
		err := yamltest.Bind(t, xmlSource(source), niceyaml.NewError(
			"validation error",
			niceyaml.WithErrors(
				niceyaml.NewError(
					"error at line1",
					niceyaml.AtPath(paths.Root().Child("line1").Key()),
				),
				niceyaml.NewError(
					"error at line2",
					niceyaml.AtPath(paths.Root().Child("line2").Key()),
				),
			),
		))

		// The hunk holds line1 and line2 with no separator between them.
		assert.Equal(t, stringtest.JoinLF(
			"validation error",
			"├── 1:1: $.line1~: error at line1",
			"└── 2:1: $.line2~: error at line2",
			"",
			"<genericError>line1</genericError><punctuationMappingValue>:</punctuationMappingValue><text> </text><literalString>a</literalString>",
			"<textError>^ error at line1</textError>",
			"<genericError>line2</genericError><punctuationMappingValue>:</punctuationMappingValue><text> </text><literalString>b</literalString>",
			"<textError>^ error at line2</textError>",
		), trimLines(renderContext(err, 0)))
	})
}

func TestError_Width(t *testing.T) {
	t.Parallel()

	// The YAML holds a value long enough to wrap.
	source := stringtest.Input(`
		key: this is a very long value that should wrap when width is limited to a small number
	`)
	tokens := lexer.Tokenize(source)

	tcs := map[string]struct {
		width       int
		wantWrapped bool
	}{
		"wraps at narrow width": {
			width:       40,
			wantWrapped: true,
		},
		"no wrap when width is 0": {
			width:       0,
			wantWrapped: false,
		},
		"no wrap when width is large": {
			width:       200,
			wantWrapped: false,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			errPrinter := printer.New(
				printer.WithGutter(printer.NoGutter),
				printer.WithContainerStyle(lipgloss.NewStyle()),
				printer.WithWrap(tc.width),
			)

			err := yamltest.Bind(t, niceyaml.NewSourceFromString(source), niceyaml.NewError("test error",
				niceyaml.AtPosition(position.NewFromToken(tokens[0])),
			))

			output := renderWith(err, errPrinter, 2)
			lines := strings.Split(output, "\n")

			// Skip the header line and the empty line.
			contentLines := 0
			for _, line := range lines {
				if strings.Contains(line, "key:") || strings.Contains(line, "this is") ||
					strings.Contains(line, "should wrap") || strings.Contains(line, "limited") {
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

func TestError_Width_WithCustomPrinter(t *testing.T) {
	t.Parallel()

	source := stringtest.Input(`
		key: this is a very long value that should wrap when width is limited
	`)
	tokens := lexer.Tokenize(source)

	// Width comes from the printer, which wraps only at the width
	// [printer.WithWrap] sets.
	customPrinter := printer.New(
		printer.WithStyles(yamltest.NewXMLStyles()),
		printer.WithGutter(printer.NoGutter),
		printer.WithContainerStyle(lipgloss.NewStyle()),
	)

	errPrinter := customPrinter.With(printer.WithWrap(30))

	err := yamltest.Bind(t, niceyaml.NewSourceFromString(source), niceyaml.NewError(
		"test error",
		niceyaml.AtPosition(position.NewFromToken(tokens[0])),
	))

	output := renderWith(err, errPrinter, 2)
	lines := strings.Split(output, "\n")

	// Wrapping splits the content over several lines.
	contentLines := 0
	for _, line := range lines {
		if strings.Contains(line, "key") || strings.Contains(line, "this is") ||
			strings.Contains(line, "should wrap") {
			contentLines++
		}
	}

	assert.Greater(t, contentLines, 1, "expected content to wrap into multiple lines with custom printer")

	// The caller's printer keeps its own width.
	assert.Equal(t, 0, customPrinter.Wrap())
}

func TestError_Width_DefaultPrinter(t *testing.T) {
	t.Parallel()

	source := stringtest.Input(`
		key: this is a very long value that should wrap when width is limited
	`)
	tokens := lexer.Tokenize(source)

	// A printer with only a width keeps the default styles and gutter.
	errPrinter := printer.New(printer.WithWrap(30))

	err := yamltest.Bind(t, niceyaml.NewSourceFromString(source), niceyaml.NewError(
		"test error",
		niceyaml.AtPosition(position.NewFromToken(tokens[0])),
	))

	output := renderWith(err, errPrinter, 2)
	lines := strings.Split(output, "\n")

	// Wrapping splits the content over several lines. Each check is a
	// single word, which stays whole on one row wherever the rows break.
	contentLines := 0
	for _, line := range lines {
		if strings.Contains(line, "key") || strings.Contains(line, "value") ||
			strings.Contains(line, "limited") {
			contentLines++
		}
	}

	assert.Greater(t, contentLines, 1, "expected content to wrap into multiple lines with default printer")
}

func TestError_Width_AnnotationWrapping(t *testing.T) {
	t.Parallel()

	source := stringtest.Input(`
		key: value
		other: data
	`)

	tcs := map[string]struct {
		width        int
		nestedErrMsg string
		tree         string
		want         string
	}{
		"long annotation wraps at narrow width": {
			width:        40,
			nestedErrMsg: "this is a very long error message that should definitely wrap when the width is limited",
			tree: stringtest.JoinLF(
				"1:1: $.key~: validation failed",
				"└── 1:6: $.key: this is a very long",
				"    error message that should definitely",
				"    wrap when the width is limited",
			),
			want: stringtest.JoinLF(
				"key: value",
				"     ^ this is a very long error message",
				"       that should definitely wrap when",
				"       the width is limited",
				"other: data",
			),
		},
		"annotation does not wrap when width is 0": {
			width:        0,
			nestedErrMsg: "this is a very long error message that should not wrap",
			tree: stringtest.JoinLF(
				"1:1: $.key~: validation failed",
				"└── 1:6: $.key: this is a very long error message that should not wrap",
			),
			want: stringtest.JoinLF(
				"key: value",
				"     ^ this is a very long error message that should not wrap",
				"other: data",
			),
		},
		"short annotation fits on single line": {
			width:        80,
			nestedErrMsg: "short error",
			tree: stringtest.JoinLF(
				"1:1: $.key~: validation failed",
				"└── 1:6: $.key: short error",
			),
			want: stringtest.JoinLF(
				"key: value",
				"     ^ short error",
				"other: data",
			),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			errPrinter := printer.New(
				printer.WithStyles(&style.Styles{}),
				printer.WithGutter(printer.NoGutter),
				printer.WithContainerStyle(lipgloss.NewStyle()),
				printer.WithWrap(tc.width),
			)

			err := yamltest.Bind(t, niceyaml.NewSourceFromString(source), niceyaml.NewError(
				"validation failed",
				niceyaml.AtPath(paths.Root().Child("key").Key()),
				niceyaml.WithErrors(
					niceyaml.NewError(
						tc.nestedErrMsg,
						niceyaml.AtPath(paths.Root().Child("key")),
					),
				),
			))

			got := trimLines(renderWith(err, errPrinter, 2))

			// The message tree and the annotation in the excerpt both
			// wrap to the width, the tree less its connectors.
			assert.Equal(t, tc.tree+"\n\n"+tc.want, got)
		})
	}
}

func TestError_Width_MultipleAnnotationsWrapping(t *testing.T) {
	t.Parallel()

	source := stringtest.Input(`
		name: test
		value: 123
		other: data
	`)

	// Several nested errors carry long messages.
	errPrinter := printer.New(
		printer.WithStyles(&style.Styles{}),
		printer.WithGutter(printer.NoGutter),
		printer.WithContainerStyle(lipgloss.NewStyle()),
		printer.WithWrap(50),
	)

	err := yamltest.Bind(t, niceyaml.NewSourceFromString(source), niceyaml.NewError(
		"validation failed at 2 locations",
		niceyaml.WithErrors(
			niceyaml.NewError(
				"first error with a very long message that should wrap properly",
				niceyaml.AtPath(paths.Root().Child("value")),
			),
			niceyaml.NewError(
				"second error also with a long message for testing wrap behavior",
				niceyaml.AtPath(paths.Root().Child("other").Key()),
			),
		),
	))
	got := trimLines(renderWith(err, errPrinter, 2))

	want := stringtest.JoinLF(
		"validation failed at 2 locations",
		"├── 2:8: $.value: first error with a very long",
		"│   message that should wrap properly",
		"└── 3:1: $.other~: second error also with a long",
		"    message for testing wrap behavior",
		"",
		"name: test",
		"value: 123",
		"       ^ first error with a very long message that",
		"         should wrap properly",
		"other: data",
		"^ second error also with a long message for",
		"  testing wrap behavior",
	)
	assert.Equal(t, want, got)
}

func TestError_Width_CombinedAnnotationsOnSameLine(t *testing.T) {
	t.Parallel()

	source := stringtest.Input(`
		key: value
	`)

	// Multiple errors on the same line combine with "; ".
	errPrinter := printer.New(
		printer.WithStyles(&style.Styles{}),
		printer.WithGutter(printer.NoGutter),
		printer.WithContainerStyle(lipgloss.NewStyle()),
		printer.WithWrap(40),
	)

	err := yamltest.Bind(t, niceyaml.NewSourceFromString(source), niceyaml.NewError(
		"validation failed",
		niceyaml.AtPath(paths.Root().Child("key").Key()),
		niceyaml.WithErrors(
			niceyaml.NewError(
				"first error message here",
				niceyaml.AtPath(paths.Root().Child("key").Key()),
			),
			niceyaml.NewError(
				"second error message here",
				niceyaml.AtPath(paths.Root().Child("key")),
			),
		),
	))
	got := trimLines(renderWith(err, errPrinter, 2))

	want := stringtest.JoinLF(
		"1:1: $.key~: validation failed",
		"├── 1:1: $.key~: first error message",
		"│   here",
		"└── 1:6: $.key: second error message",
		"    here",
		"",
		"key: value",
		"^ first error message here; second error",
		"  message here",
	)
	assert.Equal(t, want, got)
}

func TestError_TokenRendersFromSource(t *testing.T) {
	t.Parallel()

	source := xmlSource(stringtest.Input(`
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

	node, err := paths.Root().Child("b").Node(file.Docs[0])
	require.NoError(t, err)

	literal, ok := node.(*ast.LiteralNode)
	require.True(t, ok, "want *ast.LiteralNode, got %T", node)

	tk := literal.Value.GetToken()

	got := trimLines(render(yamltest.Bind(t, source, niceyaml.NewError(
		"bad block",
		niceyaml.AtPosition(position.NewFromToken(tk)),
		niceyaml.WithErrors(
			niceyaml.NewError("bad c", niceyaml.AtPath(paths.Root().Child("c"))),
		),
	))))

	// The printer highlights both lines of the block scalar, comments keep
	// their place, and the nested path resolves in the same source.
	assert.Contains(t, got, "<genericError>two</genericError>")
	assert.Contains(t, got, "<genericError>lines</genericError>")
	assert.Contains(t, got, "<comment># note</comment>")
	assert.Contains(t, got, "<genericError>3</genericError>")
	assert.Contains(t, got, "^ bad c")
}

func TestError_BoundDocument(t *testing.T) {
	t.Parallel()

	source := xmlSource("name: first\n---\nname: second\n")
	namePath := paths.Root().Child("name")

	docs, err := source.Documents()
	require.NoError(t, err)
	require.Len(t, docs, 2)

	t.Run("path resolves in the document that bound it", func(t *testing.T) {
		t.Parallel()

		err := docs[1].Bind(niceyaml.NewError("bad name", niceyaml.AtPath(namePath)))

		got := trimLines(render(err))

		assert.True(t, strings.HasPrefix(got, "3:7: $.name: bad name"), got)
		assert.Contains(t, got, "<genericError>second</genericError>")
		assert.NotContains(t, got, "<genericError>first</genericError>")
	})

	t.Run("nested errors resolve in the same document", func(t *testing.T) {
		t.Parallel()

		err := docs[1].Bind(niceyaml.NewError(
			"validation failed",
			niceyaml.WithErrors(
				niceyaml.NewError("bad name", niceyaml.AtPath(namePath)),
			),
		))

		got := trimLines(render(err))

		assert.Contains(t, got, "<genericError>second</genericError>")
		assert.Contains(t, got, "^ bad name")
		assert.NotContains(t, got, "<genericError>first</genericError>")
	})
}

func TestError_DoesNotMutateSource(t *testing.T) {
	t.Parallel()

	source := xmlSource(stringtest.Input(`
		a: 1
		b: 2
		c: 3
	`))

	err := yamltest.Bind(t, source, niceyaml.NewError(
		"main",
		niceyaml.WithErrors(
			niceyaml.NewError("nested", niceyaml.AtPath(paths.Root().Child("b"))),
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
	view := source.View()
	for i := range view.All() {
		assert.Empty(t, view.Overlays(i))
		assert.Empty(t, view.Annotations(i))
	}
}

func TestError_With(t *testing.T) {
	t.Parallel()

	base := niceyaml.NewError("bad key")
	located := base.With(niceyaml.AtPath(paths.Root().Child("key").Key()))

	// The copy carries the option and the receiver keeps its own.
	_, ok := base.Path()
	assert.False(t, ok)

	locatedPath, ok := located.Path()
	require.True(t, ok)
	assert.Equal(t, paths.Root().Child("key").Key(), locatedPath)

	assert.Equal(t, "bad key", base.Error())
	assert.Equal(t, "$.key~: bad key", located.Error())
}

func TestError_WrappedContext(t *testing.T) {
	t.Parallel()

	source := niceyaml.NewSourceFromString("name: first\n---\nname: second\n")

	docs, err := source.Documents()
	require.NoError(t, err)
	require.Len(t, docs, 2)

	inner := niceyaml.NewError("bad name", niceyaml.AtPath(paths.Root().Child("name")))

	wrapped := docs[1].Bind(fmt.Errorf("document 1: %w", inner))

	// The outer context stays as the wrapper wrote it, and the position the
	// path resolves to goes in front of the whole message.
	assert.Equal(t, "3:7: document 1: $.name: bad name", wrapped.Error())
	assert.Equal(t, "3:7: document 1: $.name: bad name", fmt.Sprintf("%v", wrapped))
	require.ErrorIs(t, wrapped, inner)

	// Binding first keeps the position beside the message under the context.
	boundFirst := fmt.Errorf("document 1: %w", docs[1].Bind(inner))
	assert.Equal(t, "document 1: 3:7: $.name: bad name", boundFirst.Error())

	var got *niceyaml.Error

	require.ErrorAs(t, wrapped, &got)

	gotPath, ok := got.Path()
	require.True(t, ok)
	assert.Equal(t, "$.name", gotPath.String())

	var bound *niceyaml.SourceError

	require.ErrorAs(t, wrapped, &bound)

	excerpt, ok := bound.Excerpt(2)
	require.True(t, ok)

	detail := newXMLPrinter().Print(excerpt)
	assert.Contains(t, detail, "second")
	assert.Contains(t, detail, "^^^^^^", "the range shows its extent")
	assert.NotContains(t, detail, "^ ", "the wrapper's text is no annotation")

	// Wrapping a direct Error resolves its position in the message.
	direct := docs[1].Bind(inner)
	assert.Equal(t, "3:7: $.name: bad name", direct.Error())

	// Wrapping twice renders the same output.
	twice := docs[1].Bind(direct)
	assert.Equal(t, direct.Error(), twice.Error())
	assert.Equal(t, fmt.Sprintf("%+v", direct), fmt.Sprintf("%+v", twice))
}

func TestError_ContextAboveLocation(t *testing.T) {
	t.Parallel()

	source := xmlSource("name: first\n---\nname: second\n")

	docs, err := source.Documents()
	require.NoError(t, err)
	require.Len(t, docs, 2)

	// A producer that wraps its own Error with context, the way a SelfValidator
	// does, leaves the document to the binder above that wrapping.
	located := niceyaml.NewError("bad name", niceyaml.AtPath(paths.Root().Child("name")))
	wrapped := docs[1].Bind(niceyaml.WrapError(fmt.Errorf("validate: %w", located)))

	// The producer's context stays as written, behind the resolved position.
	assert.Equal(t, "3:7: validate: $.name: bad name", wrapped.Error())

	// The highlight lands on the second document's value, not the first's.
	var bound *niceyaml.SourceError

	require.ErrorAs(t, wrapped, &bound)

	excerpt, ok := bound.Excerpt(2)
	require.True(t, ok)

	detail := newXMLPrinter().Print(excerpt)
	assert.Contains(t, detail, "<genericError>second</genericError>")
	assert.NotContains(t, detail, "<genericError>first</genericError>")
}

func TestError_NestedErrorsRenderAsAnnotations(t *testing.T) {
	t.Parallel()

	source := niceyaml.NewSourceFromString("a: 1\nb: 2\n")
	plain := printer.New(
		printer.WithStyles(style.Styles{}),
		printer.WithGutter(printer.NoGutter),
		printer.WithContainerStyle(lipgloss.NewStyle()),
	)
	badA := niceyaml.NewError("bad a", niceyaml.AtPath(paths.Root().Child("a")))
	badB := niceyaml.NewError("bad b", niceyaml.AtPath(paths.Root().Child("b")))
	inner := niceyaml.NewError(
		"validation failed at 2 locations",
		niceyaml.WithErrors(badA, badB),
	)

	wrapped := yamltest.Bind(t, source, fmt.Errorf("document 0: %w", inner))

	// The message is the headline behind the context the wrapper added.
	// The %+v verb lists the nested errors behind their positions after
	// it, they are reachable through Unwrap, the printer draws them as
	// branches of the tree, and they appear in the excerpt as annotations.
	assert.Equal(t, "document 0: validation failed at 2 locations", wrapped.Error())
	assert.Equal(t, stringtest.JoinLF(
		"document 0: validation failed at 2 locations",
		"|-- 1:4: $.a: bad a",
		"`-- 2:4: $.b: bad b",
	), report(wrapped))
	require.ErrorIs(t, wrapped, badA)
	require.ErrorIs(t, wrapped, badB)

	var bound *niceyaml.SourceError

	require.ErrorAs(t, wrapped, &bound)

	got := trimLines(plain.PrintError(bound))

	message, detail, _ := strings.Cut(got, "\n\n")

	assert.Equal(t, "document 0: validation failed at 2 locations\n├── 1:4: $.a: bad a\n└── 2:4: $.b: bad b", message)
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

		source := xmlSource("a: 1\nb: 2\nc: 3\n")
		err := yamltest.Bind(t, source, niceyaml.NewError(
			"outer",
			niceyaml.AtPath(paths.Root().Child("a")),
			niceyaml.WithErrors(
				niceyaml.WrapError(fmt.Errorf("ctx: %w",
					niceyaml.NewError("bad", niceyaml.AtPath(paths.Root().Child("b"))),
				)),
			),
		))

		var bound *niceyaml.SourceError

		require.ErrorAs(t, err, &bound)

		excerpt, ok := bound.Excerpt(2)
		require.True(t, ok)

		got := trimLines(newXMLPrinter().Print(excerpt))
		assert.Contains(t, got, "<genericError>2</genericError>")
		assert.Contains(t, got, "^ ctx: $.b: bad")

		// The excerpt follows the message, which lists the nested error
		// with its position in front of the context it carries. The root
		// has no message beside its range, so the printer marks the range
		// with a caret run.
		assert.Equal(t, stringtest.JoinLF(
			"1:4: $.a: outer",
			"└── 2:4: ctx: $.b: bad",
			"",
			"<nameTag>a</nameTag><punctuationMappingValue>:</punctuationMappingValue><text> </text><genericError>1</genericError>",
			"<textError>   ^</textError>",
			"<nameTag>b</nameTag><punctuationMappingValue>:</punctuationMappingValue><text> </text><genericError>2</genericError>",
			"<textError>   ^ ctx: $.b: bad</textError>",
			"<nameTag>c</nameTag><punctuationMappingValue>:</punctuationMappingValue><text> </text><literalNumberInteger>3</literalNumberInteger>",
		), trimLines(render(err)))
	})

	t.Run("annotation drops the position the caret marks", func(t *testing.T) {
		t.Parallel()

		source := xmlSource("a: 1\nb: 2\n")
		tk := source.Lines().TokenAt(position.New(1, 0))
		require.NotNil(t, tk)

		err := yamltest.Bind(t, source, niceyaml.NewError(
			"outer",
			niceyaml.AtPath(paths.Root().Child("a")),
			niceyaml.WithErrors(
				niceyaml.WrapError(niceyaml.NewError("inner", niceyaml.AtPosition(position.NewFromToken(tk)))),
				niceyaml.WrapError(niceyaml.NewError("also", niceyaml.AtPath(paths.Root().Child("b")))),
			),
		))

		_, detail, _ := strings.Cut(trimLines(render(err)), "\n\n")
		assert.Contains(t, detail, "^ inner; also")
		assert.NotContains(t, detail, "2:1:")
		assert.NotContains(t, detail, "$.b")
	})
}

func TestError_NestedMessageSpansLines(t *testing.T) {
	t.Parallel()

	source := niceyaml.NewSourceFromString("a: 1\nb: 2\n")
	err := yamltest.Bind(t, source, niceyaml.NewError(
		"validation failed",
		niceyaml.WithErrors(
			niceyaml.NewError(
				"bad a\n  see docs for details",
				niceyaml.AtPath(paths.Root().Child("a")),
			),
		),
	))

	// The message is the headline alone. The %+v verb lists the nested
	// message after it whole, continuation line included, the tree indents
	// the continuation line under the branch, and the annotation carries
	// it as well.
	assert.Equal(t, "validation failed", err.Error())
	assert.Equal(t, "validation failed\n`-- 1:4: $.a: bad a\n      see docs for details", report(err))

	got := trimLines(render(err))
	message, detail, _ := strings.Cut(got, "\n\n")

	assert.Equal(t, "validation failed\n└── 1:4: $.a: bad a\n      see docs for details", message)
	assert.Contains(t, detail, "see docs for details")
}

func TestSourceError_NestedPositionsBehindWrappers(t *testing.T) {
	t.Parallel()

	source := niceyaml.NewSourceFromString("a: 1\nb: 2\n", niceyaml.WithName("f.yaml"))
	inner := niceyaml.NewError("2 problems", niceyaml.WithErrors(
		niceyaml.NewError("bad a", niceyaml.AtPath(paths.Root().Child("a"))),
		niceyaml.NewError("bad b", niceyaml.AtExactPath(paths.Root().Child("missing"))),
	))

	tcs := map[string]struct {
		err  error
		want string
	}{
		"bare": {
			err:  inner,
			want: "f.yaml: 2 problems\n|-- 1:4: $.a: bad a\n`-- $.missing: bad b",
		},
		"context in front keeps the nested lines at the end": {
			err:  fmt.Errorf("document 0: %w", inner),
			want: "f.yaml: document 0: 2 problems\n|-- 1:4: $.a: bad a\n`-- $.missing: bad b",
		},
		"nested inside nested": {
			err: niceyaml.NewError("outer", niceyaml.WithErrors(
				niceyaml.NewError("middle", niceyaml.AtPath(paths.Root().Child("b")), niceyaml.WithErrors(
					niceyaml.NewError("leaf", niceyaml.AtPath(paths.Root().Child("a"))),
				)),
			)),
			want: "f.yaml: outer\n`-- 2:4: $.b: middle\n    `-- 1:4: $.a: leaf",
		},
		"a wrapper that rewrites the message keeps the nested lines": {
			err:  yamltest.RewriteError{Err: inner},
			want: "f.yaml: rewritten\n|-- 1:4: $.a: bad a\n`-- $.missing: bad b",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			bound := yamltest.Bind(t, source, tc.err)

			// The message is the first line, and the %+v verb lists the
			// nested errors after it, each as its own bound error.
			assert.Equal(t, tc.want, report(bound))
			assert.Equal(t, strings.SplitN(tc.want, "\n", 2)[0], bound.Error())
		})
	}
}

func TestSourceError_KeepsWrappedText(t *testing.T) {
	t.Parallel()

	source := niceyaml.NewSourceFromString("name: first\n---\nname: second\n")
	namePath := paths.Root().Child("name")

	docs, err := source.Documents()
	require.NoError(t, err)
	require.Len(t, docs, 2)

	t.Run("nested wrappers keep their text behind the position", func(t *testing.T) {
		t.Parallel()

		inner := niceyaml.NewError("bad name", niceyaml.AtPath(namePath))
		wrapped := docs[0].Bind(fmt.Errorf("outer: %w", fmt.Errorf("inner: %w", inner)))

		assert.Equal(t, "1:7: outer: inner: $.name: bad name", wrapped.Error())
	})

	t.Run("a bound join binds each branch as a child", func(t *testing.T) {
		t.Parallel()

		first := niceyaml.NewError("bad first", niceyaml.AtPath(namePath))
		second := niceyaml.NewError("bad second", niceyaml.AtPath(namePath))
		wrapped := docs[0].Bind(errors.Join(
			fmt.Errorf("a: %w", first),
			fmt.Errorf("b: %w", second),
		))

		// The join carries no location of its own, so the message stays
		// as the join wrote it, and each branch is a child with its
		// position in front of the text its wrapper wrote.
		assert.Equal(t, "a: $.name: bad first\nb: $.name: bad second", wrapped.Error())
		require.ErrorIs(t, wrapped, first)
		require.ErrorIs(t, wrapped, second)

		var bound *niceyaml.SourceError

		require.ErrorAs(t, wrapped, &bound)
		require.Len(t, bound.Errors(), 2)
		assert.Equal(t, "1:7: a: $.name: bad first", bound.Errors()[0].Error())
		assert.Equal(t, "1:7: b: $.name: bad second", bound.Errors()[1].Error())
		assert.Len(t, slices.Collect(niceyaml.Bindings(wrapped)), 1)
	})

	t.Run("binding each branch reports every position", func(t *testing.T) {
		t.Parallel()

		first := niceyaml.NewError("bad first", niceyaml.AtPath(namePath))
		second := niceyaml.NewError("bad second", niceyaml.AtPath(namePath))
		joined := errors.Join(
			fmt.Errorf("a: %w", docs[0].Bind(first)),
			fmt.Errorf("b: %w", docs[1].Bind(second)),
		)

		// Each branch resolves in the document that bound it.
		assert.Equal(t, "a: 1:7: $.name: bad first\nb: 3:7: $.name: bad second", joined.Error())
	})

	t.Run("a wrapper that rewrites the message still gets the position", func(t *testing.T) {
		t.Parallel()

		inner := niceyaml.NewError("bad name", niceyaml.AtPath(namePath))
		wrapped := docs[0].Bind(fmt.Errorf("outer: %w", yamltest.RewriteError{Err: inner}))

		// The position comes from the Error in the chain, not from its text,
		// so a wrapper that hides the text does not hide the position.
		assert.Equal(t, "1:7: outer: rewritten", wrapped.Error())
		require.ErrorIs(t, wrapped, inner)
	})

	t.Run("a token error gets its position in front of the wrapper's text", func(t *testing.T) {
		t.Parallel()

		tk := source.Lines().TokenAt(position.New(2, 6))
		inner := niceyaml.NewError("bad token", niceyaml.AtPosition(position.NewFromToken(tk)))
		wrapped := docs[0].Bind(fmt.Errorf("document 1: %w", inner))

		// The Error carries no position in its text, and binding puts the
		// token's in front of the whole message, as it does for a path.
		assert.Equal(t, "bad token", inner.Error())
		assert.Equal(t, "3:7: document 1: bad token", wrapped.Error())
	})

	t.Run("an anchor with nested errors takes the position of the error it wraps", func(t *testing.T) {
		t.Parallel()

		tk := source.Lines().TokenAt(position.New(2, 6))
		nested := niceyaml.NewError("bad name", niceyaml.AtPath(namePath))

		tcs := map[string]struct {
			err     *niceyaml.Error
			unbound string
			bound   string
		}{
			"direct": {
				err: niceyaml.WrapError(
					niceyaml.NewError("bad token", niceyaml.AtPosition(position.NewFromToken(tk))),
					niceyaml.WithErrors(nested),
				),
				unbound: "bad token",
				bound:   "3:7: bad token",
			},
			"behind context": {
				err: niceyaml.WrapError(
					fmt.Errorf(
						"document 1: %w",
						niceyaml.NewError("bad token", niceyaml.AtPosition(position.NewFromToken(tk))),
					),
					niceyaml.WithErrors(nested),
				),
				unbound: "document 1: bad token",
				bound:   "3:7: document 1: bad token",
			},
			"range": {
				err: niceyaml.WrapError(
					niceyaml.NewError("bad range",
						niceyaml.AtRange(position.NewRange(position.New(2, 6), position.New(2, 12))),
					),
					niceyaml.WithErrors(nested),
				),
				unbound: "bad range",
				bound:   "3:7: bad range",
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				// The outer Error takes the location of the Error it wraps,
				// and the nested error becomes a child with its own position,
				// which the %+v verb lists after the message.
				assert.Equal(t, tc.unbound, tc.err.Error())
				assert.Equal(t, tc.bound, docs[0].Bind(tc.err).Error())
				assert.Equal(t, tc.bound+"\n`-- 1:7: "+nested.Error(), report(docs[0].Bind(tc.err)))
			})
		}
	})

	t.Run("a path anchor resolves its path rather than the error it wraps", func(t *testing.T) {
		t.Parallel()

		tk := source.Lines().TokenAt(position.New(2, 6))
		inner := niceyaml.NewError("bad token", niceyaml.AtPosition(position.NewFromToken(tk)))
		outer := niceyaml.WrapError(inner, niceyaml.AtPath(namePath))

		// The path anchor resolves in the first document, and the message
		// carries that one position, which agrees with the highlight.
		assert.Equal(t, "$.name: bad token", outer.Error())
		assert.Equal(t, "1:7: $.name: bad token", docs[0].Bind(outer).Error())
	})

	t.Run("a path anchor replaces the path of the error it wraps", func(t *testing.T) {
		t.Parallel()

		inner := niceyaml.NewError("bad token", niceyaml.AtPath(paths.Root().Child("other")))
		outer := niceyaml.WrapError(inner, niceyaml.AtPath(namePath))

		// The message names the one location Path reports, not both.
		p, ok := outer.Path()
		require.True(t, ok)
		assert.Equal(t, namePath, p)
		assert.Equal(t, "$.name: bad token", outer.Error())
		assert.Equal(t, "1:7: $.name: bad token", docs[0].Bind(outer).Error())
	})

	t.Run("a second binding adds no position", func(t *testing.T) {
		t.Parallel()

		inner := niceyaml.NewError("bad name", niceyaml.AtPath(namePath))
		once := docs[0].Bind(inner)
		wrapper := fmt.Errorf("document 0: %w", once)
		twice := docs[0].Bind(wrapper)

		// The chain is bound to this source already, so the second binding
		// returns the wrapper as it is.
		assert.Same(t, wrapper, twice)
		assert.Equal(t, "document 0: 1:7: $.name: bad name", twice.Error())
	})
}

func TestSourceError_Range_ClampsToTheLines(t *testing.T) {
	t.Parallel()

	source := niceyaml.NewSourceFromString("a: 1\nb: 2\nc: 3\n", niceyaml.WithName("f.yaml"))

	// A range that starts on a line of the source binds, and its end past
	// the last line clamps to the end of that line, so the range reported
	// names lines the source has.
	err := niceyaml.NewError("wide", niceyaml.AtRange(position.NewRange(position.New(1, 0), position.New(99, 3))))

	var bound *niceyaml.SourceError

	require.ErrorAs(t, yamltest.Bind(t, source, err), &bound)

	rng, ok := bound.Range()
	require.True(t, ok)
	assert.Equal(t, position.NewRange(position.New(1, 0), position.New(2, 4)), rng)
	assert.Equal(t, "f.yaml:2:1: wide", bound.Error())
}

func TestSourceError_Range_Inverted(t *testing.T) {
	t.Parallel()

	source := niceyaml.NewSourceFromString("a: 1\nb: 2\nc: 3\n")

	tcs := map[string]struct {
		rng  position.Range
		want position.Range
	}{
		"end on an earlier line": {
			rng:  position.NewRange(position.New(2, 2), position.New(0, 1)),
			want: position.NewRange(position.New(2, 2), position.New(2, 2)),
		},
		"end at an earlier column": {
			rng:  position.NewRange(position.New(1, 3), position.New(1, 1)),
			want: position.NewRange(position.New(1, 3), position.New(1, 3)),
		},
		"start past the end of the last line, end past it": {
			rng:  position.NewRange(position.New(2, 10), position.New(9, 0)),
			want: position.NewRange(position.New(2, 10), position.New(2, 10)),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			err := niceyaml.NewError("inverted", niceyaml.AtRange(tc.rng))

			var bound *niceyaml.SourceError

			require.ErrorAs(t, yamltest.Bind(t, source, err), &bound)

			rng, ok := bound.Range()
			require.True(t, ok)
			assert.Equal(t, tc.want, rng)

			excerpt, ok := bound.Excerpt(0)
			require.True(t, ok)
			assert.Equal(t, []int{tc.want.Start.Line + 1}, lineNumbers(excerpt))
		})
	}
}

func TestError_ResolvesThroughErrorWrappers(t *testing.T) {
	t.Parallel()

	source := niceyaml.NewSourceFromString("name: first\n---\nname: second\n")
	located := niceyaml.NewError("bad name", niceyaml.AtPath(paths.Root().Child("name")))

	docs, err := source.Documents()
	require.NoError(t, err)
	require.Len(t, docs, 2)

	// Error wrappers add no message text of their own, so the document
	// attached above them still resolves the location in the message.
	wrapped := docs[0].Bind(niceyaml.WrapError(located))

	assert.Equal(t, "1:7: $.name: bad name", wrapped.Error())

	var got *niceyaml.SourceError

	require.ErrorAs(t, wrapped, &got)

	excerpt, ok := got.Excerpt(2)
	require.True(t, ok)
	require.NotNil(t, excerpt)
	assert.Positive(t, excerpt.Count())
}

func TestError_RangeRendersFromSource(t *testing.T) {
	t.Parallel()

	source := xmlSource("key: some value\n")
	rng := position.NewRange(position.New(0, 5), position.New(0, 9))

	err := yamltest.Bind(t, source, niceyaml.NewError("bad word", niceyaml.AtRange(rng)))

	// The headline is 1-indexed, and the highlight covers the range rather
	// than the token under it.
	got := trimLines(render(err))

	assert.Equal(t, stringtest.JoinLF(
		"1:6: bad word",
		"",
		"<nameTag>key</nameTag><punctuationMappingValue>:</punctuationMappingValue><text> </text><genericError>some</genericError><literalString> value</literalString>",
		"<textError>     ^^^^</textError>",
	), got)

	// A range puts no position in the message until a source binds it.
	bare := niceyaml.NewError("bad word", niceyaml.AtRange(rng))
	assert.Equal(t, "bad word", bare.Error())
}

func TestSourceError_Range(t *testing.T) {
	t.Parallel()

	source := xmlSource(stringtest.Input(`
		name: test
		value: 123
		---
		name: second
	`))

	docs, err := source.Documents()
	require.NoError(t, err)
	require.Len(t, docs, 2)

	tcs := map[string]struct {
		err  *niceyaml.Error
		want position.Range
		is   error
		// The error carries no location, so nothing resolves and there
		// is no reason to report.
		unlocated bool
		doc       int
	}{
		"path targets the value token": {
			err:  niceyaml.NewError("bad", niceyaml.AtPath(paths.Root().Child("value"))),
			want: position.NewRange(position.New(1, 7), position.New(1, 10)),
		},
		"path targets the key token": {
			err:  niceyaml.NewError("bad", niceyaml.AtPath(paths.Root().Child("value").Key())),
			want: position.NewRange(position.New(1, 0), position.New(1, 5)),
		},
		"path resolves in the bound document": {
			err:  niceyaml.NewError("bad", niceyaml.AtPath(paths.Root().Child("name"))),
			want: position.NewRange(position.New(3, 6), position.New(3, 12)),
			doc:  1,
		},
		"range is returned as given": {
			err: niceyaml.NewError("bad",
				niceyaml.AtRange(position.NewRange(position.New(0, 1), position.New(0, 3))),
			),
			want: position.NewRange(position.New(0, 1), position.New(0, 3)),
		},
		"position covers the content of its token": {
			err:  niceyaml.NewError("bad", niceyaml.AtPosition(position.New(0, 6))),
			want: position.NewRange(position.New(0, 6), position.New(0, 10)),
		},
		"position inside its token covers the whole token": {
			err:  niceyaml.NewError("bad", niceyaml.AtPosition(position.New(1, 8))),
			want: position.NewRange(position.New(1, 7), position.New(1, 10)),
		},
		"no location": {
			err:       niceyaml.NewError("bad"),
			unlocated: true,
		},
		"path that does not resolve": {
			err: niceyaml.NewError("bad", niceyaml.AtExactPath(paths.Root().Child("missing"))),
			is:  paths.ErrNotFound,
		},
		"range past the last line": {
			err: niceyaml.NewError("bad",
				niceyaml.AtRange(position.NewRange(position.New(9, 0), position.New(9, 3))),
			),
			is: niceyaml.ErrOutOfRange,
		},
		"range before the first line": {
			err: niceyaml.NewError("bad",
				niceyaml.AtRange(position.NewRange(position.New(-1, 0), position.New(-1, 2))),
			),
			is: niceyaml.ErrOutOfRange,
		},
		"range before the first column": {
			err: niceyaml.NewError("bad",
				niceyaml.AtRange(position.NewRange(position.New(0, -2), position.New(0, 2))),
			),
			is: niceyaml.ErrOutOfRange,
		},
		"range end before the first column on a later line": {
			err: niceyaml.NewError("bad",
				niceyaml.AtRange(position.NewRange(position.New(0, 1), position.New(1, -3))),
			),
			want: position.NewRange(position.New(0, 1), position.New(1, 0)),
		},
		"position from other text": {
			err: niceyaml.NewError("bad", niceyaml.AtPosition(position.New(8, 0))),
			is:  niceyaml.ErrOutOfRange,
		},
		"position before the first column": {
			err: niceyaml.NewError("bad", niceyaml.AtPosition(position.New(1, -5))),
			is:  niceyaml.ErrOutOfRange,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var bound *niceyaml.SourceError

			require.ErrorAs(t, docs[tc.doc].Bind(tc.err), &bound)

			if tc.unlocated || tc.is != nil {
				requireUnresolved(t, bound, tc.is)

				return
			}

			got, ok := bound.Range()
			require.True(t, ok)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestSourceError_Range_EmptyValue(t *testing.T) {
	t.Parallel()

	// A path to an empty value resolves to a token the parser makes for
	// it, so the range is empty at the position the message reports, even
	// when a comment or the spaces after the colon sit at that column.
	tcs := map[string]struct {
		input string
		path  paths.Path
		want  position.Position
	}{
		"nothing after the colon": {
			input: "a:\nb: 1\n",
			path:  paths.Root().Child("a"),
			want:  position.New(0, 2),
		},
		"comment after the colon": {
			input: "a: # c\nb: 1\n",
			path:  paths.Root().Child("a"),
			want:  position.New(0, 2),
		},
		"spaces after the colon": {
			input: "a:   \nb: 1\n",
			path:  paths.Root().Child("a"),
			want:  position.New(0, 2),
		},
		"comment after the dash": {
			input: "- # c\n- 1\n",
			path:  paths.Root().Index(0),
			want:  position.New(0, 1),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var bound *niceyaml.SourceError

			require.ErrorAs(t, yamltest.Bind(t, niceyaml.NewSourceFromString(tc.input),
				niceyaml.NewError("bad", niceyaml.AtPath(tc.path)),
			), &bound)

			got, ok := bound.Range()
			require.True(t, ok)
			assert.Equal(t, position.NewRange(tc.want, tc.want), got)
		})
	}
}

func TestSourceError_Range_Indentation(t *testing.T) {
	t.Parallel()

	source := xmlSource(stringtest.Input(`
		parent:
		  child: x
		  other: 1
	`))

	var bound *niceyaml.SourceError

	require.ErrorAs(t, yamltest.Bind(t, source, niceyaml.NewError("bad",
		niceyaml.AtPosition(position.New(2, 0)),
	)), &bound)

	got, ok := bound.Range()
	require.True(t, ok)

	// The lexer bundles the indentation of the third line into the plain
	// scalar "x" above it, yet the range covers the key on the third line.
	assert.Equal(t, position.NewRange(position.New(2, 2), position.New(2, 7)), got)
}

func TestSourceError_Range_TabLine(t *testing.T) {
	t.Parallel()

	// A line that holds only a tab precedes a token that starts with the
	// line ending, so no token holds text after the tab. The range stays
	// on the tab line rather than covering the token on the next line.
	tcs := map[string]struct {
		input string
		pos   position.Position
	}{
		"tab line in mapping": {
			input: "a: 1\n\t\nb: 2\n",
			pos:   position.New(1, 0),
		},
		"tab line in sequence": {
			input: "- a\n\t\n- b\n",
			pos:   position.New(1, 0),
		},
		"tab line in nested sequence": {
			input: "a:\n  - 1\n\t\n  - 2\n",
			pos:   position.New(2, 0),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			source := xmlSource(tc.input)

			var bound *niceyaml.SourceError

			require.ErrorAs(t, source.Bind(niceyaml.NewError("tab",
				niceyaml.AtPosition(tc.pos),
			)), &bound)

			got, ok := bound.Range()
			require.True(t, ok)
			assert.Equal(t, position.NewRange(tc.pos, tc.pos), got)
		})
	}
}

func TestSourceError_Range_MultiLineToken(t *testing.T) {
	t.Parallel()

	source := xmlSource(stringtest.Input(`
		text: first
		  second
	`))

	var bound *niceyaml.SourceError

	require.ErrorAs(t, yamltest.Bind(t, source, niceyaml.NewError("bad",
		niceyaml.AtPath(paths.Root().Child("text")),
	)), &bound)

	got, ok := bound.Range()
	require.True(t, ok)

	// The plain scalar continues on the second line, so the range ends there.
	assert.Equal(t, position.NewRange(position.New(0, 6), position.New(1, 8)), got)
}

func TestSourceError_Range_PositionInsideToken(t *testing.T) {
	t.Parallel()

	source := xmlSource(stringtest.Input(`
		text: first
		  second
	`))

	var bound *niceyaml.SourceError

	require.ErrorAs(t, yamltest.Bind(t, source, niceyaml.NewError("bad",
		niceyaml.AtPosition(position.New(1, 4)),
	)), &bound)

	got, ok := bound.Range()
	require.True(t, ok)

	// The position falls on the second line of the plain scalar, so the
	// range starts on the first line where the token does, while the
	// message reports the position as the error gave it.
	assert.Equal(t, position.NewRange(position.New(0, 6), position.New(1, 8)), got)
	assert.Equal(t, "2:5: bad", bound.Error())
}

func TestSourceError_Excerpt_Errors(t *testing.T) {
	t.Parallel()

	source := xmlSource("name: test\nvalue: 123\n")

	tcs := map[string]struct {
		err        *niceyaml.Error
		is         error
		wantRender string
	}{
		"no location": {
			err:        niceyaml.NewError("bad"),
			wantRender: "bad",
		},
		"path that does not resolve": {
			err:        niceyaml.NewError("bad", niceyaml.AtExactPath(paths.Root().Child("missing"))),
			is:         paths.ErrNotFound,
			wantRender: "$.missing: bad\n\nno excerpt: resolve $.missing: not found",
		},
		"range past the last line": {
			err: niceyaml.NewError("bad",
				niceyaml.AtRange(position.NewRange(position.New(9, 0), position.New(9, 3))),
			),
			is:         niceyaml.ErrOutOfRange,
			wantRender: "bad\n\nno excerpt: location outside source: line 10 not in lines 1-2",
		},
		"range before the first line": {
			err: niceyaml.NewError("bad",
				niceyaml.AtRange(position.NewRange(position.New(-1, 0), position.New(-1, 2))),
			),
			is:         niceyaml.ErrOutOfRange,
			wantRender: "bad\n\nno excerpt: location outside source: line 0 not in lines 1-2",
		},
		"range before the first column": {
			err: niceyaml.NewError("bad",
				niceyaml.AtRange(position.NewRange(position.New(0, -2), position.New(0, 2))),
			),
			is:         niceyaml.ErrOutOfRange,
			wantRender: "bad\n\nno excerpt: location outside source: column -1 of line 1",
		},
		"nested range before the first line": {
			err: niceyaml.NewError("bad",
				niceyaml.AtExactPath(paths.Root().Child("missing")),
				niceyaml.WithErrors(
					niceyaml.NewError("first",
						niceyaml.AtRange(position.NewRange(position.New(-1, 0), position.New(-1, 2))),
					),
				),
			),
			is:         paths.ErrNotFound,
			wantRender: "$.missing: bad\n└── first\n\nno excerpt: resolve $.missing: not found",
		},
		"every nested error unresolved": {
			err: niceyaml.NewError("bad", niceyaml.WithErrors(
				niceyaml.NewError("first", niceyaml.AtExactPath(paths.Root().Child("missing"))),
				niceyaml.NewError("second", niceyaml.AtPosition(position.New(9, 0))),
			)),
			wantRender: "bad\n├── $.missing: first\n└── second",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var bound *niceyaml.SourceError

			require.ErrorAs(t, yamltest.Bind(t, source, tc.err), &bound)

			got, ok := bound.Excerpt(2)
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

		var bound *niceyaml.SourceError

		require.ErrorAs(t, yamltest.Bind(t, source, niceyaml.NewError("bad", niceyaml.WithErrors(
			niceyaml.NewError("first", niceyaml.AtExactPath(paths.Root().Child("missing"))),
			niceyaml.NewError("second", niceyaml.AtPath(paths.Root().Child("value"))),
		))), &bound)

		excerpt, ok := bound.Excerpt(2)
		require.True(t, ok)

		got := newXMLPrinter().Print(excerpt)
		assert.Contains(t, got, "^ second")
		assert.NotContains(t, got, "first")
	})

	t.Run("unresolved root with a resolved child prints no reason", func(t *testing.T) {
		t.Parallel()

		var bound *niceyaml.SourceError

		require.ErrorAs(t, yamltest.Bind(t, source, niceyaml.NewError("bad",
			niceyaml.AtExactPath(paths.Root().Child("missing")),
			niceyaml.WithErrors(niceyaml.NewError("first", niceyaml.AtPath(paths.Root().Child("name")))),
		)), &bound)

		requireUnresolved(t, bound, paths.ErrNotFound)

		_, ok := bound.Excerpt(0)
		require.True(t, ok)

		// The child resolves, so the excerpt marks it, and the root keeps
		// its message in the tree without a position or a reason.
		assert.Equal(t,
			"$.missing: bad\n`-- 1:7: $.name: first\n\n   1 | name: test\n     |       ^^^^ first",
			niceyaml.FormatError(bound, 0),
		)
		assert.NotContains(t, render(bound), "no excerpt:")
	})
}

// excerptSource is a ten-line source whose second and eighth lines are far
// enough apart that an excerpt with one line of context shows them as two
// hunks.
const excerptSource = "a: 1\nb: 2\nc: 3\nd: 4\ne: 5\nf: 6\ng: 7\nh: 8\ni: 9\nj: 10\n"

// excerptError binds an error on the value of b with a nested error on the
// value of h to a source of [excerptSource], and returns the bound error.
func excerptError(t *testing.T) *niceyaml.SourceError {
	t.Helper()

	err := yamltest.Bind(t, niceyaml.NewSourceFromString(excerptSource), niceyaml.NewError(
		"bad b",
		niceyaml.AtPath(paths.Root().Child("b")),
		niceyaml.WithErrors(
			niceyaml.NewError("bad h", niceyaml.AtPath(paths.Root().Child("h"))),
		),
	))

	var bound *niceyaml.SourceError

	require.ErrorAs(t, err, &bound)

	return bound
}

// lineNumbers returns the source line number of every line in view.
func lineNumbers(view *line.View) []int {
	numbers := make([]int, 0, view.Count())
	for _, l := range view.All() {
		numbers = append(numbers, l.Number())
	}

	return numbers
}

func TestSourceError_Excerpt(t *testing.T) {
	t.Parallel()

	t.Run("holds only the hunk lines with their source numbers", func(t *testing.T) {
		t.Parallel()

		excerpt, ok := excerptError(t).Excerpt(1)
		require.True(t, ok)

		assert.Equal(t, []int{1, 2, 3, 7, 8, 9}, lineNumbers(excerpt))
		assert.Equal(t, "b: 2", excerpt.Lines().Line(1).Content())
		assert.Equal(t, "h: 8", excerpt.Lines().Line(7).Content())
		assert.True(t, excerpt.Contains(7))
		assert.False(t, excerpt.Contains(4), "the excerpt keeps the indices of the source")
	})

	t.Run("overlays the error ranges with the error style", func(t *testing.T) {
		t.Parallel()

		excerpt, ok := excerptError(t).Excerpt(1)
		require.True(t, ok)

		want := line.Overlays{{Kind: kind.GenericError, Cols: position.NewSpan(3, 4)}}
		assert.Equal(t, want, excerpt.Overlays(1), "the main error covers the value of b")
		assert.Equal(t, want, excerpt.Overlays(7), "the nested error covers the value of h")

		for _, i := range []int{0, 2, 6, 8} {
			assert.Empty(t, excerpt.Overlays(i), "line %d carries no overlay", i)
		}
	})

	t.Run("a range that covers no column still shows its line", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, "a: 1\nb: 2\n")

		tcs := map[string]struct {
			rng position.Range
		}{
			"empty range": {
				rng: position.NewRange(position.New(1, 2), position.New(1, 2)),
			},
			"range past the end of the line": {
				rng: position.NewRange(position.New(1, 10), position.New(1, 12)),
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				var bound *niceyaml.SourceError

				require.ErrorAs(t, dd.Bind(niceyaml.NewError("bad", niceyaml.AtRange(tc.rng))), &bound)

				excerpt, ok := bound.Excerpt(0)
				require.True(t, ok)
				assert.Equal(t, []int{2}, lineNumbers(excerpt))
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
				src:  "script: |\n  echo a\n  \n  echo b\n",
				opt:  niceyaml.AtPosition(position.New(2, 0)),
				want: []int{2, 3, 4},
				row:  "   3 |   \n     | ^\n",
			},
			"range starting at the end of its first line": {
				src:  "a: 1\nb: 2\n",
				opt:  niceyaml.AtRange(position.NewRange(position.New(0, 4), position.New(1, 1))),
				want: []int{1, 2},
				row:  "   1 | a: 1\n     |     ^\n",
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				var bound *niceyaml.SourceError

				src := niceyaml.NewSourceFromString(tc.src)
				require.ErrorAs(t, yamltest.Bind(t, src, niceyaml.NewError("bad", tc.opt)), &bound)

				excerpt, ok := bound.Excerpt(0)
				require.True(t, ok)
				assert.Equal(t, tc.want, lineNumbers(excerpt))

				formatted := niceyaml.FormatError(bound, 0)
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
				err: niceyaml.NewError("top", niceyaml.WithErrors(
					niceyaml.NewError("far", niceyaml.AtPosition(far)),
				)),
				want:    line.Annotations{{Content: "far", Kind: kind.TextError, Placement: line.Below, Col: 4}},
				row:     "     |     ^ far",
				printed: "<textError>    ^ far</textError>",
			},
			"nested range": {
				err: niceyaml.NewError("top", niceyaml.WithErrors(
					niceyaml.NewError("far", niceyaml.AtRange(position.NewRange(far, position.New(0, far.Col+2)))),
				)),
				want:    line.Annotations{{Content: "far", Kind: kind.TextError, Placement: line.Below, Col: 4}},
				row:     "     |     ^ far",
				printed: "<textError>    ^ far</textError>",
			},
			"root position": {
				err:     niceyaml.NewError("far", niceyaml.AtPosition(far)),
				want:    line.Annotations{{Kind: kind.TextError, Placement: line.Below, Col: 4}},
				row:     "     |     ^",
				printed: "<textError>    ^</textError>",
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				var bound *niceyaml.SourceError

				require.ErrorAs(t, yamltest.Bind(t, niceyaml.NewSourceFromString("a: 1\n"), tc.err), &bound)

				excerpt, ok := bound.Excerpt(0)
				require.True(t, ok)
				assert.Equal(t, tc.want, excerpt.Annotations(0))

				formatted := niceyaml.FormatError(bound, 0)
				assert.Contains(t, formatted, fmt.Sprintf("1:%d: far", far.Col+1), "the tree keeps the column as given")
				assert.True(t, strings.HasSuffix(formatted, "\n"+tc.row), formatted)
				assert.True(t, strings.HasSuffix(renderContext(bound, 0), tc.printed))
			})
		}
	})

	t.Run("annotates nested messages below their lines", func(t *testing.T) {
		t.Parallel()

		excerpt, ok := excerptError(t).Excerpt(1)
		require.True(t, ok)

		assert.Equal(t, line.Annotations{
			{Content: "bad h", Kind: kind.TextError, Placement: line.Below, Col: 3},
		}, excerpt.Annotations(7).Filter(line.Below))
		assert.Equal(t, line.Annotations{
			{Kind: kind.TextError, Placement: line.Below, Col: 3},
		}, excerpt.Annotations(1), "the main error has no message of its own, so its line carries a marker alone")
	})

	t.Run("separates hunks after the first with an ellipsis", func(t *testing.T) {
		t.Parallel()

		excerpt, ok := excerptError(t).Excerpt(1)
		require.True(t, ok)

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

		excerpt, ok := bound.Excerpt(1)
		require.True(t, ok)

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

		got := trimLines(newXMLPrinter().Print(excerpt))
		assert.Equal(t, want, got)

		assert.Equal(t, "2:4: $.b: bad b\n└── 8:4: $.h: bad h\n\n"+want, trimLines(renderContext(bound, 1)))
	})

	t.Run("negative context shows the marked lines alone", func(t *testing.T) {
		t.Parallel()

		bound := excerptError(t)

		zero, ok := bound.Excerpt(0)
		require.True(t, ok)

		negative, ok := bound.Excerpt(-1)
		require.True(t, ok)

		assert.Equal(t, []int{2, 8}, lineNumbers(zero))
		assert.Equal(t, lineNumbers(zero), lineNumbers(negative))
		assert.Equal(t, newXMLPrinter().Print(zero), newXMLPrinter().Print(negative))
	})

	t.Run("no location", func(t *testing.T) {
		t.Parallel()

		var bound *niceyaml.SourceError

		require.ErrorAs(
			t,
			yamltest.Bind(t, niceyaml.NewSourceFromString(excerptSource), niceyaml.NewError("bad")),
			&bound,
		)

		excerpt, ok := bound.Excerpt(2)
		require.False(t, ok)
		assert.Nil(t, excerpt)
		require.NoError(t, bound.Unresolved())
	})
}

func TestSourceError_Annotate(t *testing.T) {
	t.Parallel()

	// Assert that no line of view carries an overlay or annotation.
	unmarked := func(t *testing.T, view *line.View) {
		t.Helper()

		for i := range view.All() {
			assert.Empty(t, view.Overlays(i), "line %d carries no overlay", i)
			assert.Empty(t, view.Annotations(i), "line %d carries no annotation", i)
		}
	}

	t.Run("marks the line of the error with its message", func(t *testing.T) {
		t.Parallel()

		bound := excerptError(t)
		view := bound.Source().View()

		require.True(t, bound.Annotate(view))

		assert.Equal(t, []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}, lineNumbers(view))

		assert.Equal(t, line.Overlays{{Kind: kind.GenericError, Cols: position.NewSpan(3, 4)}}, view.Overlays(1))
		assert.Equal(t, line.Annotations{
			{Content: "bad b", Kind: kind.TextError, Placement: line.Below, Col: 3},
		}, view.Annotations(1))

		// The child is left to its own Annotate.
		for i := range view.All() {
			if i == 1 {
				continue
			}

			assert.Empty(t, view.Overlays(i), "line %d carries no overlay", i)
			assert.Empty(t, view.Annotations(i), "line %d carries no annotation", i)
		}
	})

	t.Run("every binding marks the tree as the excerpt is marked", func(t *testing.T) {
		t.Parallel()

		bound := excerptError(t)
		view := bound.Source().View()

		for b := range niceyaml.AllBindings(bound) {
			require.True(t, b.Annotate(view))
		}

		want := line.Overlays{{Kind: kind.GenericError, Cols: position.NewSpan(3, 4)}}
		assert.Equal(t, want, view.Overlays(1))
		assert.Equal(t, want, view.Overlays(7))
		assert.Equal(t, line.Annotations{
			{Content: "bad b", Kind: kind.TextError, Placement: line.Below, Col: 3},
		}, view.Annotations(1))
		assert.Equal(t, line.Annotations{
			{Content: "bad h", Kind: kind.TextError, Placement: line.Below, Col: 3},
		}, view.Annotations(7))

		// The excerpt is the same view cut down to its hunks, with the
		// indices of the source, except that the root's line carries a
		// caret run alone since the tree above the excerpt names it.
		excerpt, ok := bound.Excerpt(0)
		require.True(t, ok)
		assert.Equal(t, view.Overlays(1), excerpt.Overlays(1))
		assert.Equal(t, view.Overlays(7), excerpt.Overlays(7))
		assert.Equal(t, line.Annotations{
			{Kind: kind.TextError, Placement: line.Below, Col: 3},
		}, excerpt.Annotations(1).Filter(line.Below))
		assert.Equal(t, view.Annotations(7), excerpt.Annotations(7).Filter(line.Below))
	})

	t.Run("a tab in the message becomes four spaces", func(t *testing.T) {
		t.Parallel()

		source := niceyaml.NewSourceFromString("a: 1\n")
		err := yamltest.Bind(t, source, niceyaml.NewError(
			"did you mean:\n\tb\x1b",
			niceyaml.AtPath(paths.Root().Child("a")),
		))

		var bound *niceyaml.SourceError

		require.ErrorAs(t, err, &bound)

		view := source.View()

		require.True(t, bound.Annotate(view))

		// The annotation keeps the other control characters, which a
		// renderer draws as their pictures.
		assert.Equal(t, line.Annotations{
			{Content: "did you mean:\n    b\x1b", Kind: kind.TextError, Placement: line.Below, Col: 3},
		}, view.Annotations(0))
		assert.Contains(t, view.String(), "^ did you mean:\u240a    b\u241b")
	})

	t.Run("messages on one line join in column order", func(t *testing.T) {
		t.Parallel()

		source := niceyaml.NewSourceFromString("key: value\n")
		err := yamltest.Bind(t, source, errors.Join(
			niceyaml.NewError("about value", niceyaml.AtPosition(position.New(0, 5))),
			niceyaml.NewError("about key", niceyaml.AtPosition(position.New(0, 0))),
		))

		// The join binds with no location of its own, so only its
		// children mark the view.
		view := source.View()
		for b := range niceyaml.AllBindings(err) {
			b.Annotate(view)
		}

		assert.Contains(t, view.String(), "^^^  ^^^^^ about key; about value")
		assert.Contains(t, niceyaml.FormatError(err, 0), "^^^  ^^^^^ about key; about value")
	})

	t.Run("a binding the tree reaches twice marks its line once", func(t *testing.T) {
		t.Parallel()

		source := niceyaml.NewSourceFromString("a: 1\nb: 2\n")
		badA := yamltest.Bind(t, source, niceyaml.NewError("bad a", niceyaml.AtPath(paths.Root().Child("a"))))
		summary := yamltest.Bind(t, source, niceyaml.NewError("summary", niceyaml.WithErrors(badA)))
		err := yamltest.Bind(t, source, errors.Join(summary, badA))

		got := niceyaml.FormatError(err, 1)
		assert.Contains(t, got, "^ bad a")
		assert.NotContains(t, got, "bad a; bad a")

		// The excerpt marks the view as every binding AllBindings yields
		// marks it.
		view := source.View()
		for b := range niceyaml.AllBindings(err) {
			b.Annotate(view)
		}

		var bound *niceyaml.SourceError

		require.ErrorAs(t, err, &bound)

		excerpt, ok := bound.Excerpt(0)
		require.True(t, ok)
		assert.Equal(t, view.Annotations(0), excerpt.Annotations(0))
	})

	t.Run("a binding reached along many paths marks in linear time", func(t *testing.T) {
		t.Parallel()

		// Every binding nests the one before it twice, so a walk that
		// visits a binding once per path takes 2^40 steps.
		source := niceyaml.NewSourceFromString("a: 1\n")
		err := yamltest.Bind(t, source, niceyaml.NewError("bad a", niceyaml.AtPath(paths.Root().Child("a"))))

		for range 40 {
			err = yamltest.Bind(t, source, niceyaml.NewError("summary", niceyaml.WithErrors(err, err)))
		}

		var bound *niceyaml.SourceError

		require.ErrorAs(t, err, &bound)

		excerpt, ok := bound.Excerpt(0)
		require.True(t, ok)
		assert.Equal(t, line.Annotations{
			{Content: "bad a", Kind: kind.TextError, Placement: line.Below, Col: 3},
		}, excerpt.Annotations(0).Filter(line.Below))
	})

	t.Run("two errors annotate one view", func(t *testing.T) {
		t.Parallel()

		source := niceyaml.NewSourceFromString(excerptSource)
		view := source.View()

		var first, second *niceyaml.SourceError

		require.ErrorAs(t, yamltest.Bind(t, source, niceyaml.NewError(
			"bad b", niceyaml.AtPath(paths.Root().Child("b")),
		)), &first)
		require.ErrorAs(t, yamltest.Bind(t, source, niceyaml.NewError(
			"bad d",
			niceyaml.WithErrors(niceyaml.NewError("too big", niceyaml.AtPath(paths.Root().Child("d")))),
		)), &second)

		require.True(t, first.Annotate(view))
		require.False(t, second.Annotate(view), "an error with no location of its own marks nothing")

		for b := range niceyaml.AllBindings(second) {
			b.Annotate(view)
		}

		want := line.Overlays{{Kind: kind.GenericError, Cols: position.NewSpan(3, 4)}}
		assert.Equal(t, want, view.Overlays(1))
		assert.Equal(t, want, view.Overlays(3))
		assert.Equal(t, line.Annotations{
			{Content: "bad b", Kind: kind.TextError, Placement: line.Below, Col: 3},
		}, view.Annotations(1))
		assert.Equal(t, line.Annotations{
			{Content: "too big", Kind: kind.TextError, Placement: line.Below, Col: 3},
		}, view.Annotations(3))
	})

	t.Run("two errors on one line each add an annotation", func(t *testing.T) {
		t.Parallel()

		source := niceyaml.NewSourceFromString(excerptSource)
		view := source.View()

		for _, msg := range []string{"bad b", "too big"} {
			var bound *niceyaml.SourceError

			require.ErrorAs(t, yamltest.Bind(t, source, niceyaml.NewError(
				msg, niceyaml.AtPath(paths.Root().Child("b")),
			)), &bound)
			require.True(t, bound.Annotate(view))
		}

		assert.Equal(t, line.Annotations{
			{Content: "bad b", Kind: kind.TextError, Placement: line.Below, Col: 3},
			{Content: "too big", Kind: kind.TextError, Placement: line.Below, Col: 3},
		}, view.Annotations(1))
		assert.Equal(t, stringtest.JoinLF(
			"   2 | b: 2",
			"     |    ^ bad b; too big",
		), view.Slice(position.NewSpan(1, 2)).String())
	})

	t.Run("an error with no message marks a caret run alone", func(t *testing.T) {
		t.Parallel()

		source := niceyaml.NewSourceFromString(excerptSource)
		view := source.View()

		var bound *niceyaml.SourceError

		require.ErrorAs(t, yamltest.Bind(t, source, niceyaml.NewError(
			"", niceyaml.AtPath(paths.Root().Child("b")),
		)), &bound)
		require.True(t, bound.Annotate(view))

		assert.Equal(t, line.Annotations{
			{Kind: kind.TextError, Placement: line.Below, Col: 3},
		}, view.Annotations(1))
	})

	t.Run("a position past the end of its line marks the column after the last rune", func(t *testing.T) {
		t.Parallel()

		source := niceyaml.NewSourceFromString("a: 1\n")
		view := source.View()

		var bound *niceyaml.SourceError

		require.ErrorAs(t, yamltest.Bind(t, source, niceyaml.NewError(
			"far", niceyaml.AtPosition(position.New(0, math.MaxInt/2)),
		)), &bound)
		require.True(t, bound.Annotate(view))

		assert.Equal(t, line.Overlays{{Kind: kind.GenericError, Cols: position.NewSpan(4, 4)}}, view.Overlays(0))
		assert.Equal(t, line.Annotations{
			{Content: "far", Kind: kind.TextError, Placement: line.Below, Col: 4},
		}, view.Annotations(0))
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

		require.False(t, bound.Annotate(view), "the line of the error is outside the slice")

		for b := range niceyaml.AllBindings(bound) {
			b.Annotate(view)
		}

		assert.Equal(t, []int{7, 8, 9, 10}, lineNumbers(view))
		assert.Equal(t, line.Overlays{{Kind: kind.GenericError, Cols: position.NewSpan(3, 4)}}, view.Overlays(7))
		assert.Equal(t, line.Annotations{
			{Content: "bad h", Kind: kind.TextError, Placement: line.Below, Col: 3},
		}, view.Annotations(7))

		for i := range view.All() {
			if i == 7 {
				continue
			}

			assert.Empty(t, view.Overlays(i), "line %d carries no overlay", i)
			assert.Empty(t, view.Annotations(i), "line %d carries no annotation", i)
		}

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
		assert.Equal(t, line.Overlays{{Kind: kind.GenericError, Cols: position.NewSpan(3, 4)}}, view.Overlays(1))
		assert.Equal(t, stringtest.JoinLF(
			"   2 | b: 2",
			"     |    ^ bad b",
		), view.String())
	})

	t.Run("marks a diff on the lines of this revision", func(t *testing.T) {
		t.Parallel()

		before := niceyaml.NewSourceFromString("a: 1\nb: 3\n")
		after := niceyaml.NewSourceFromString("a: 1\nb: 2\n")

		var bound *niceyaml.SourceError

		require.ErrorAs(t, yamltest.Bind(t, after, niceyaml.NewError(
			"bad b", niceyaml.AtPath(paths.Root().Child("b")),
		)), &bound)

		// The unified view holds a from after, then b from before as a
		// deleted line, then b from after as an inserted one.
		view := diff.Diff(before.Lines(), after.Lines()).Unified()
		require.Equal(t, 3, view.Count())

		require.True(t, bound.Annotate(view))

		assert.Empty(t, view.Overlays(0))
		assert.Empty(t, view.Overlays(1), "the deleted line of the other revision stays unmarked")
		assert.Equal(t, line.Overlays{{Kind: kind.GenericError, Cols: position.NewSpan(3, 4)}}, view.Overlays(2))
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

				before := niceyaml.NewSourceFromString("a: 1\nb: 2\nc: 3\n")
				after := niceyaml.NewSourceFromString("a: 1\nb: 9\nc: 3\n")

				var bound *niceyaml.SourceError

				require.ErrorAs(t, yamltest.Bind(t, before, niceyaml.NewError(
					"bad "+tc.key, niceyaml.AtPath(paths.Root().Child(tc.key)),
				)), &bound)

				view := tc.view(diff.Diff(before.Lines(), after.Lines()))
				require.Equal(t, tc.want, bound.Annotate(view))

				for i := range view.All() {
					if tc.want && i == tc.index {
						assert.Equal(t,
							line.Overlays{{Kind: kind.GenericError, Cols: position.NewSpan(3, 4)}},
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
				view: niceyaml.NewSourceFromString(excerptSource).View(),
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
				unmarked(t, tc.view)
			})
		}
	})

	t.Run("leaves the view unmarked when nothing resolves", func(t *testing.T) {
		t.Parallel()

		source := niceyaml.NewSourceFromString(excerptSource)

		tcs := map[string]struct {
			err *niceyaml.Error
			is  error
		}{
			"no location": {
				err: niceyaml.NewError("bad"),
			},
			"path that does not resolve": {
				err: niceyaml.NewError("bad", niceyaml.AtExactPath(paths.Root().Child("missing"))),
				is:  paths.ErrNotFound,
			},
			"range past the last line": {
				err: niceyaml.NewError("bad",
					niceyaml.AtRange(position.NewRange(position.New(20, 0), position.New(20, 1))),
				),
				is: niceyaml.ErrOutOfRange,
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				var bound *niceyaml.SourceError

				require.ErrorAs(t, yamltest.Bind(t, source, tc.err), &bound)

				view := source.View()

				require.False(t, bound.Annotate(view))
				requireUnresolved(t, bound, tc.is)
				unmarked(t, view)
			})
		}
	})
}

func TestSourceError_TreeBranches(t *testing.T) {
	t.Parallel()

	// The excerpt marks every located Error in the tree, however it got there.

	source := xmlSource("a: 1\nb: 2\nc: 3\n")
	badA := niceyaml.NewError("bad a", niceyaml.AtPath(paths.Root().Child("a")))
	badB := niceyaml.NewError("bad b", niceyaml.AtPath(paths.Root().Child("b")))
	badC := niceyaml.NewError("bad c", niceyaml.AtPath(paths.Root().Child("c")))

	t.Run("join branches bind as children", func(t *testing.T) {
		t.Parallel()

		err := yamltest.Bind(t, source, errors.Join(badA, badB))

		// The join has no location of its own, each branch is a child
		// with one, and the excerpt marks both with their messages. The
		// %+v verb leads with the branches, since the join's own message
		// is their text joined and says nothing they do not.
		assert.Equal(t, "$.a: bad a\n$.b: bad b", err.Error())
		assert.Equal(t, "|-- 1:4: $.a: bad a\n`-- 2:4: $.b: bad b", report(err))
		require.Len(t, slices.Collect(niceyaml.Bindings(err)), 1)

		got := trimLines(newXMLPrinter().PrintError(err))
		assert.Equal(t, "├── 1:4: $.a: bad a\n└── 2:4: $.b: bad b", strings.SplitN(got, "\n\n", 2)[0])
		assert.Contains(t, got, "<genericError>1</genericError>")
		assert.Contains(t, got, "<genericError>2</genericError>")
		assert.Contains(t, got, "^ bad a")
		assert.Contains(t, got, "^ bad b")
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

		assert.Equal(t, "1:4: $.a: bad a\n2:4: $.b: bad b", err.Error())
		assert.Equal(t, "|-- 1:4: $.a: bad a\n`-- 2:4: $.b: bad b", report(err))
	})

	t.Run("a join in a named source names it once", func(t *testing.T) {
		t.Parallel()

		// Each branch names its own source, so the join puts no name in
		// front of them, whichever source it binds to.
		named := func(name string) *niceyaml.Source {
			return niceyaml.NewSourceFromString("a: 1\nb: 2\nc: 3\n", niceyaml.WithName(name))
		}

		src := named("f.yaml")
		err := yamltest.Bind(t, src, errors.Join(
			yamltest.Bind(t, src, badA),
			yamltest.Bind(t, src, badB),
		))
		assert.Equal(t, "f.yaml:1:4: $.a: bad a\nf.yaml:2:4: $.b: bad b", err.Error())

		err = yamltest.Bind(t, named("c.yaml"), errors.Join(
			yamltest.Bind(t, named("a.yaml"), badA),
			yamltest.Bind(t, named("b.yaml"), badB),
		))
		assert.Equal(t, "a.yaml:1:4: $.a: bad a\nb.yaml:2:4: $.b: bad b", err.Error())

		// A binding of another source names that source alone, so a join
		// that leads with one still names its own when a later branch
		// belongs to it. A branch that joins bindings of other sources
		// holds no text of its own, so it adds no name.
		err = yamltest.Bind(t, named("c.yaml"), errors.Join(yamltest.Bind(t, named("a.yaml"), badA), badB))
		assert.Equal(t, "c.yaml: a.yaml:1:4: $.a: bad a\n$.b: bad b", err.Error())

		err = yamltest.Bind(t, named("c.yaml"), errors.Join(
			errors.Join(yamltest.Bind(t, named("a.yaml"), badA), yamltest.Bind(t, named("b.yaml"), badB)),
			yamltest.Bind(t, named("b.yaml"), badB),
		))
		assert.Equal(t, "a.yaml:1:4: $.a: bad a\nb.yaml:2:4: $.b: bad b\nb.yaml:2:4: $.b: bad b", err.Error())

		// So does an Error that only wraps a binding of such a join.
		joinSrc := named("c.yaml")
		err = yamltest.Bind(t, joinSrc, errors.Join(
			yamltest.Bind(t, named("a.yaml"), badA),
			niceyaml.WrapError(yamltest.Bind(t, joinSrc, errors.Join(
				yamltest.Bind(t, named("b.yaml"), badB),
			))),
		))
		assert.Equal(t, "a.yaml:1:4: $.a: bad a\nb.yaml:2:4: $.b: bad b", err.Error())

		// A binding of such a join to this source leaves its message as it
		// is, so its first line names another source. A join that leads
		// with that binding, or an Error that wraps it with errors of its
		// own, still names this source when a child belongs to it.
		joinOfOthers := yamltest.Bind(t, src, errors.Join(
			yamltest.Bind(t, named("a.yaml"), badA),
			yamltest.Bind(t, named("b.yaml"), badB),
		))
		leads := map[string]struct {
			err  error
			want string
		}{
			"join that leads with it": {
				err:  errors.Join(joinOfOthers, badC),
				want: "f.yaml: a.yaml:1:4: $.a: bad a\nb.yaml:2:4: $.b: bad b\n$.c: bad c",
			},
			"errors nested around it": {
				err:  niceyaml.WrapError(joinOfOthers, niceyaml.WithErrors(badC)),
				want: "f.yaml: a.yaml:1:4: $.a: bad a\nb.yaml:2:4: $.b: bad b",
			},
		}

		for name, tc := range leads {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				err := yamltest.Bind(t, src, tc.err)
				assert.Equal(t, tc.want, err.Error())
			})
		}

		// A binding of a source with no name leaves its message untouched
		// too, even when a child belongs to that source. The first line of
		// a join that leads with it comes from the binding below it, so a
		// line that names this source already takes no second name.
		unnamed := niceyaml.NewSourceFromString("a: 1\nb: 2\nc: 3\n")
		ownLead := yamltest.Bind(t, src, badA)
		unnamedB := yamltest.Bind(t, unnamed, badB)
		unnamedLeads := map[string]struct {
			err  error
			want string
		}{
			"join that leads with it": {
				err: errors.Join(
					yamltest.Bind(t, unnamed, errors.Join(ownLead, unnamedB)),
					badC,
				),
				want: "f.yaml:1:4: $.a: bad a\n2:4: $.b: bad b\n$.c: bad c",
			},
			"errors nested around it": {
				err: errors.Join(
					yamltest.Bind(t, unnamed, niceyaml.WrapError(
						yamltest.Bind(t, unnamed, errors.Join(ownLead)),
						niceyaml.WithErrors(unnamedB),
					)),
					badC,
				),
				want: "f.yaml:1:4: $.a: bad a\n$.c: bad c",
			},
		}

		for name, tc := range unnamedLeads {
			t.Run("unnamed "+name, func(t *testing.T) {
				t.Parallel()

				err := yamltest.Bind(t, src, tc.err)
				assert.Equal(t, tc.want, err.Error())
			})
		}

		// Only the first line can take the name, so a join that leads
		// with a binding, even one nested in another join, puts no name
		// in front, and a join that leads with an unbound branch does.
		err = yamltest.Bind(t, src, errors.Join(yamltest.Bind(t, src, badA), badB))
		assert.Equal(t, "f.yaml:1:4: $.a: bad a\n$.b: bad b", err.Error())

		err = yamltest.Bind(t, src, errors.Join(
			errors.Join(yamltest.Bind(t, src, badA), badB),
			badC,
		))
		assert.Equal(t, "f.yaml:1:4: $.a: bad a\n$.b: bad b\n$.c: bad c", err.Error())

		err = yamltest.Bind(t, src, errors.Join(badB, yamltest.Bind(t, src, badA)))
		assert.Equal(t, "f.yaml: $.b: bad b\nf.yaml:1:4: $.a: bad a", err.Error())

		// An Error above a binding that nests errors, or that carries a
		// position alone, writes the text of the binding as it is, so a
		// join that leads with one puts no name in front either.
		tcs := map[string]struct {
			lead error
		}{
			"nested errors": {
				lead: niceyaml.WrapError(yamltest.Bind(t, src, badA), niceyaml.WithErrors(badB)),
			},
			"position alone": {
				lead: niceyaml.WrapError(yamltest.Bind(t, src, badA), niceyaml.AtPosition(position.New(0, 3))),
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				err := yamltest.Bind(t, src, errors.Join(tc.lead, badC))
				assert.Equal(t, "f.yaml:1:4: $.a: bad a\n$.c: bad c", err.Error())
			})
		}
	})

	t.Run("every multi-error binds the same way", func(t *testing.T) {
		t.Parallel()

		// A join, a wrapper with two %w verbs, and a join inside a
		// wrapper all bind as a root without a location and one child
		// per branch.
		tcs := map[string]error{
			"join":         errors.Join(badA, badB),
			"two %w verbs": fmt.Errorf("%w; %w", badA, badB),
			"wrapped join": fmt.Errorf("ctx: %w", errors.Join(badA, badB)),
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				err := yamltest.Bind(t, source, tc)

				var bound *niceyaml.SourceError

				require.ErrorAs(t, err, &bound)
				assert.Equal(t, tc.Error(), bound.Error())

				_, resolved := bound.Range()
				require.False(t, resolved)
				require.NoError(t, bound.Unresolved())

				require.Len(t, bound.Errors(), 2)
				assert.Equal(t, "1:4: $.a: bad a", bound.Errors()[0].Error())
				assert.Equal(t, "2:4: $.b: bad b", bound.Errors()[1].Error())
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
				want: "2:4: invalid: $.b: bad b",
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

				var bound *niceyaml.SourceError

				require.ErrorAs(t, err, &bound)

				rng, ok := bound.Range()
				require.True(t, ok)
				assert.Equal(t, 1, rng.Start.Line)
				assert.Empty(t, bound.Errors())
			})
		}

		t.Run("nested errors", func(t *testing.T) {
			t.Parallel()

			err := yamltest.Bind(t, source, fmt.Errorf("%w: %w",
				errInvalid, niceyaml.NewError("summary", niceyaml.WithErrors(badA, badC))))

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
			niceyaml.NewError("summary", niceyaml.WithErrors(badB, badC)),
		)))

		assert.Equal(t, "document 0: first: $.a: bad a\nsummary", err.Error())
		assert.Equal(t, stringtest.JoinLF(
			"document 0: first: $.a: bad a",
			"summary",
			"|-- 1:4: first: $.a: bad a",
			"`-- summary",
			"    |-- 2:4: $.b: bad b",
			"    `-- 3:4: $.c: bad c",
		), report(err))

		got := trimLines(render(err))
		assert.Contains(t, got, "<genericError>1</genericError>")
		assert.Contains(t, got, "^ first: $.a: bad a")
		assert.Contains(t, got, "^ bad b")
		assert.Contains(t, got, "^ bad c")
	})

	t.Run("a join branch that does not resolve is listed without a position", func(t *testing.T) {
		t.Parallel()

		missing := niceyaml.NewError("bad x", niceyaml.AtExactPath(paths.Root().Child("x")))
		err := yamltest.Bind(t, source, errors.Join(badA, missing))

		got := trimLines(render(err))
		assert.Equal(t, "├── 1:4: $.a: bad a\n└── $.x: bad x", strings.SplitN(got, "\n\n", 2)[0])
		assert.Equal(t, 1, strings.Count(got, "$.x: bad x"))
	})

	t.Run("a bound error inside the tree keeps its own binding", func(t *testing.T) {
		t.Parallel()

		other := xmlSource("z: 9\n")
		inner := yamltest.Bind(t, other, niceyaml.NewError("bad z", niceyaml.AtPath(paths.Root().Child("z"))))
		err := yamltest.Bind(t, source, errors.Join(badA, inner))

		var bound *niceyaml.SourceError

		require.ErrorAs(t, err, &bound)
		assert.Same(t, source, bound.Source())

		// The outer binding marks its own branch, and the inner keeps its
		// excerpt from the other source.
		excerpt, ok := bound.Excerpt(0)
		require.True(t, ok)
		assert.Equal(t, 1, excerpt.Count())

		got := trimLines(newXMLPrinter().PrintError(err))
		assert.Contains(t, got, "<genericError>1</genericError>")
		assert.Contains(t, got, "<genericError>9</genericError>")
	})
}

func TestBindings(t *testing.T) {
	t.Parallel()

	source := niceyaml.NewSourceFromString("a: 1\n---\nb: 2\n")

	docs, err := source.Documents()
	require.NoError(t, err)
	require.Len(t, docs, 2)

	first := docs[0].Bind(niceyaml.NewError("bad a", niceyaml.AtPath(paths.Root().Child("a"))))
	second := docs[1].Bind(niceyaml.NewError("bad b", niceyaml.AtPath(paths.Root().Child("b"))))
	outer := yamltest.Bind(
		t,
		niceyaml.NewSourceFromString("c: 3\n"),
		niceyaml.NewError("outer", niceyaml.WithErrors(first)),
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

func TestSourceError_Excerpts(t *testing.T) {
	t.Parallel()

	manifest := niceyaml.NewSourceFromString("kind: App\nvalues: values.yaml\n", niceyaml.WithFilePath("manifest.yaml"))
	values := niceyaml.NewSourceFromString("port: many\nhost: 7\n", niceyaml.WithFilePath("values.yaml"))

	port := yamltest.Bind(t, values, niceyaml.NewError("not a number", niceyaml.AtPath(paths.Root().Child("port"))))
	host := yamltest.Bind(t, values, niceyaml.NewError("not a name", niceyaml.AtPath(paths.Root().Child("host"))))
	err := yamltest.Bind(t, manifest, niceyaml.NewError(
		"bad values",
		niceyaml.AtPath(paths.Root().Child("values")),
		niceyaml.WithErrors(port, host),
	))

	var bound *niceyaml.SourceError

	require.ErrorAs(t, err, &bound)

	t.Run("one excerpt per source, the binding's source first", func(t *testing.T) {
		t.Parallel()

		var (
			sources  []string
			excerpts []string
		)

		for src, excerpt := range bound.Excerpts(0) {
			sources = append(sources, src.Name())
			excerpts = append(excerpts, excerpt.String())
		}

		assert.Equal(t, []string{"manifest.yaml", "values.yaml"}, sources)
		require.Len(t, excerpts, 2)

		assert.Equal(t, "   2 | values: values.yaml\n     |         ^^^^^^^^^^^", excerpts[0])

		// The sibling children bound to the other source share one
		// excerpt of it, each with its message beside its line.
		assert.Equal(t, stringtest.JoinLF(
			"   1 | port: many",
			"     |       ^^^^ not a number",
			"   2 | host: 7",
			"     |       ^ not a name",
		), excerpts[1])
	})

	t.Run("Excerpt keeps to the source of the binding", func(t *testing.T) {
		t.Parallel()

		excerpt, ok := bound.Excerpt(0)
		require.True(t, ok)
		assert.Equal(t, 1, excerpt.Count())
	})

	t.Run("Annotate marks the lines of any source the view holds", func(t *testing.T) {
		t.Parallel()

		view := values.View()
		require.False(t, bound.Annotate(view), "the binding's own line is in the other source")

		for b := range niceyaml.AllBindings(bound) {
			b.Annotate(view)
		}

		assert.Equal(t, stringtest.JoinLF(
			"   1 | port: many",
			"     |       ^^^^ not a number",
			"   2 | host: 7",
			"     |       ^ not a name",
		), view.String())
	})

	t.Run("FormatError renders every excerpt", func(t *testing.T) {
		t.Parallel()

		got := niceyaml.FormatError(bound, 0)
		assert.Equal(t, stringtest.JoinLF(
			"manifest.yaml:2:9: $.values: bad values",
			"|-- values.yaml:1:7: $.port: not a number",
			"`-- values.yaml:2:7: $.host: not a name",
			"",
			"manifest.yaml",
			"   2 | values: values.yaml",
			"     |         ^^^^^^^^^^^",
			"",
			"values.yaml",
			"   1 | port: many",
			"     |       ^^^^ not a number",
			"   2 | host: 7",
			"     |       ^ not a name",
		), got)
	})

	t.Run("children that alternate between sources mark their own", func(t *testing.T) {
		t.Parallel()

		srcs := []*niceyaml.Source{
			niceyaml.NewSourceFromString("a: 1\nb: 2\n", niceyaml.WithName("s0.yaml")),
			niceyaml.NewSourceFromString("a: 1\nb: 2\n", niceyaml.WithName("s1.yaml")),
			niceyaml.NewSourceFromString("a: 1\nb: 2\n", niceyaml.WithName("s2.yaml")),
		}

		// Each source gets two children on line 1 and one on line 2, in
		// turn, so the tree leaves every source and comes back to it.
		var children []error

		for _, spec := range []struct{ key, msg string }{
			{"a", "first"}, {"a", "second"}, {"b", "third"},
		} {
			for i, src := range srcs {
				children = append(children, yamltest.Bind(t, src, niceyaml.NewError(
					fmt.Sprintf("%s %d", spec.msg, i),
					niceyaml.AtPath(paths.Root().Child(spec.key)),
				)))
			}
		}

		var mixed *niceyaml.SourceError

		require.ErrorAs(t, yamltest.Bind(t, srcs[1], niceyaml.NewError(
			"root",
			niceyaml.AtPath(paths.Root().Child("b")),
			niceyaml.WithErrors(children...),
		)), &mixed)

		got := map[string]string{}

		var names []string

		for src, excerpt := range mixed.Excerpts(0) {
			names = append(names, src.Name())
			got[src.Name()] = excerpt.String()
		}

		assert.Equal(t, []string{"s1.yaml", "s0.yaml", "s2.yaml"}, names)
		assert.Equal(t, map[string]string{
			"s0.yaml": stringtest.JoinLF(
				"   1 | a: 1",
				"     |    ^ first 0; second 0",
				"   2 | b: 2",
				"     |    ^ third 0",
			),
			"s1.yaml": stringtest.JoinLF(
				"   1 | a: 1",
				"     |    ^ first 1; second 1",
				"   2 | b: 2",
				"     |    ^ third 1",
			),
			"s2.yaml": stringtest.JoinLF(
				"   1 | a: 1",
				"     |    ^ first 2; second 2",
				"   2 | b: 2",
				"     |    ^ third 2",
			),
		}, got)
	})

	t.Run("a tree in one source yields one excerpt", func(t *testing.T) {
		t.Parallel()

		var one *niceyaml.SourceError

		require.ErrorAs(t, yamltest.Bind(t, values, niceyaml.NewError("bad", niceyaml.WithErrors(
			niceyaml.NewError("not a number", niceyaml.AtPath(paths.Root().Child("port"))),
		))), &one)

		assert.Len(t, maps.Collect(one.Excerpts(0)), 1)
	})

	t.Run("nothing resolved yields nothing", func(t *testing.T) {
		t.Parallel()

		var none *niceyaml.SourceError

		require.ErrorAs(
			t,
			yamltest.Bind(t, values, niceyaml.NewError("bad", niceyaml.AtExactPath(paths.Root().Child("missing")))),
			&none,
		)

		for range none.Excerpts(0) {
			t.Fatal("no excerpt expected")
		}

		var nilErr *niceyaml.SourceError

		for range nilErr.Excerpts(0) {
			t.Fatal("no excerpt expected")
		}
	})
}

func TestExcerpts(t *testing.T) {
	t.Parallel()

	first := niceyaml.NewSourceFromString("a: 1\nb: 2\n", niceyaml.WithName("first.yaml"))
	second := niceyaml.NewSourceFromString("c: 3\n", niceyaml.WithName("second.yaml"))

	badA := yamltest.Bind(t, first, niceyaml.NewError("bad a", niceyaml.AtPath(paths.Root().Child("a"))))
	badB := yamltest.Bind(t, first, niceyaml.NewError("bad b", niceyaml.AtPath(paths.Root().Child("b"))))
	badC := yamltest.Bind(t, second, niceyaml.NewError("bad c", niceyaml.AtPath(paths.Root().Child("c"))))
	gone := yamltest.Bind(t, second, niceyaml.NewError("gone", niceyaml.AtExactPath(paths.Root().Child("x"))))

	// Each excerpt reads as the name of its source on a row above it.
	collect := func(excerpts iter.Seq2[*niceyaml.Source, *line.View]) []string {
		var got []string

		for src, view := range excerpts {
			got = append(got, src.Name()+"\n"+view.String())
		}

		return got
	}

	tcs := map[string]struct {
		err  error
		want []string
	}{
		"bindings of one source share an excerpt": {
			err: errors.Join(badA, badB),
			want: []string{stringtest.JoinLF(
				"first.yaml",
				"   1 | a: 1",
				"     |    ^ bad a",
				"   2 | b: 2",
				"     |    ^ bad b",
			)},
		},
		"sources come in the order the bindings reach them": {
			err: errors.Join(badC, badA, badB),
			want: []string{
				"second.yaml\n   1 | c: 3\n     |    ^ bad c",
				stringtest.JoinLF(
					"first.yaml",
					"   1 | a: 1",
					"     |    ^ bad a",
					"   2 | b: 2",
					"     |    ^ bad b",
				),
			},
		},
		"a lone binding keeps its caret bare": {
			err:  fmt.Errorf("check: %w", badA),
			want: []string{"first.yaml\n   1 | a: 1\n     |    ^"},
		},
		"a binding the error reaches twice marks its line once": {
			err:  errors.Join(badA, fmt.Errorf("again: %w", badA)),
			want: []string{"first.yaml\n   1 | a: 1\n     |    ^ bad a"},
		},
		"a nested binding joined beside its parent marks its line once": {
			err: errors.Join(
				yamltest.Bind(t, first, niceyaml.NewError("summary", niceyaml.WithErrors(badA))),
				badA,
			),
			want: []string{"first.yaml\n   1 | a: 1\n     |    ^ bad a"},
		},
		"a source with nothing resolved yields nothing": {
			err:  errors.Join(badA, gone),
			want: []string{"first.yaml\n   1 | a: 1\n     |    ^ bad a"},
		},
		"an error bound to no source yields nothing": {
			err: errors.New("plain"),
		},
		"nil yields nothing": {},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, collect(niceyaml.Excerpts(tc.err, 0)))
		})
	}

	t.Run("a lone binding yields what its own Excerpts yields", func(t *testing.T) {
		t.Parallel()

		var bound *niceyaml.SourceError

		require.ErrorAs(t, yamltest.Bind(t, first, niceyaml.NewError(
			"bad a",
			niceyaml.AtPath(paths.Root().Child("a")),
			niceyaml.WithErrors(badC),
		)), &bound)

		want := collect(bound.Excerpts(0))

		require.Len(t, want, 2)
		assert.Equal(t, want, collect(niceyaml.Excerpts(bound, 0)))
	})

	t.Run("stops when the caller does", func(t *testing.T) {
		t.Parallel()

		count := 0

		for range niceyaml.Excerpts(errors.Join(badA, badC), 0) {
			count++

			break
		}

		assert.Equal(t, 1, count)
	})

	t.Run("each excerpt is a view of its own", func(t *testing.T) {
		t.Parallel()

		views := maps.Collect(niceyaml.Excerpts(errors.Join(badA, badB), 0))
		require.Len(t, views, 1)

		// The source keeps no decoration from the excerpt.
		assert.Equal(t, "   1 | a: 1\n   2 | b: 2", first.View().String())
	})
}

func TestError_Accessors(t *testing.T) {
	t.Parallel()

	cause := errors.New("boom")
	nested := niceyaml.NewError("nested", niceyaml.AtPath(paths.Root().Child("a")))

	var nilNested *niceyaml.Error

	err := niceyaml.WrapError(cause, niceyaml.WithErrors(nil, nested, nilNested))

	assert.Equal(t, cause, err.Cause())
	assert.Equal(t, []error{nested}, err.Errors())
	assert.Equal(t, []error{cause, nested}, err.Unwrap())

	// The nested slice is a copy.
	err.Errors()[0] = nil
	assert.Equal(t, []error{nested}, err.Errors())

	require.EqualError(t, niceyaml.NewError("plain").Cause(), "plain")
	assert.Empty(t, niceyaml.NewError("plain").Errors())
	assert.NoError(t, niceyaml.WrapError(nil).Cause())

	var nilErr *niceyaml.Error

	assert.NoError(t, nilErr.Cause())
	assert.Empty(t, nilErr.Errors())
}

func TestSourceError_Errors(t *testing.T) {
	t.Parallel()

	source := niceyaml.NewSourceFromString("a: 1\nb: 2\n")
	badA := niceyaml.NewError("bad a", niceyaml.AtPath(paths.Root().Child("a")))
	badX := niceyaml.NewError("bad x", niceyaml.AtExactPath(paths.Root().Child("x")))
	deep := niceyaml.NewError("deep", niceyaml.AtPath(paths.Root().Child("b")))
	mid := niceyaml.NewError("mid", niceyaml.WithErrors(deep))

	var bound *niceyaml.SourceError

	require.ErrorAs(
		t,
		yamltest.Bind(t, source, niceyaml.NewError("main", niceyaml.WithErrors(badA, badX, nil, mid))),
		&bound,
	)

	// One child per nested error, in the order given, each bound to the
	// same source and unwrapping to the error it binds.
	children := bound.Errors()
	require.Len(t, children, 3)

	for i, want := range []error{badA, badX, mid} {
		assert.Same(t, source, children[i].Source())
		require.ErrorIs(t, children[i], want)
	}

	rng, ok := children[0].Range()
	require.True(t, ok)
	assert.Equal(t, position.New(0, 3), rng.Start)
	assert.Equal(t, "1:4: $.a: bad a", children[0].Error())

	// One that did not resolve reports why, and one that carries no
	// location reports that.
	_, resolved := children[1].Range()
	require.False(t, resolved)
	require.ErrorIs(t, children[1].Unresolved(), paths.ErrNotFound)

	_, resolved = children[2].Range()
	require.False(t, resolved)
	require.NoError(t, children[2].Unresolved())

	// A nested error further in is a child of its own parent.
	grandchildren := children[2].Errors()
	require.Len(t, grandchildren, 1)

	rng, ok = grandchildren[0].Range()
	require.True(t, ok)
	assert.Equal(t, position.New(1, 3), rng.Start)

	// Every branch of a join is a child, and a branch that carries no
	// location binds without one.
	var joined *niceyaml.SourceError

	require.ErrorAs(t, yamltest.Bind(t, source, errors.Join(badA, errors.New("plain"))), &joined)

	branches := joined.Errors()
	require.Len(t, branches, 2)

	_, resolved = branches[1].Range()
	require.False(t, resolved)
	require.NoError(t, branches[1].Unresolved())

	// The slice is a copy.
	children[0] = nil

	assert.NotNil(t, bound.Errors()[0])
}

func TestSourceError_Errors_AboveBinding(t *testing.T) {
	t.Parallel()

	dd := yamltest.FirstDocument(t, "name: x\nhours:\n  open: 1\n")
	bound := dd.Bind(niceyaml.NewError("root", niceyaml.AtPath(paths.Root().Child("name"))))
	kid := niceyaml.NewError("kid", niceyaml.AtPath(paths.Root().Child("hours", "open")))

	tcs := map[string]struct {
		err  error
		want []string
	}{
		"nested errors above a binding bind as children": {
			err:  niceyaml.WrapError(bound, niceyaml.WithErrors(kid)),
			want: []string{"1:7: $.name: root", "3:9: $.hours.open: kid"},
		},
		"a binding with nothing above it comes back as it is": {
			err:  niceyaml.WrapError(bound),
			want: []string{"1:7: $.name: root"},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := []string{}
			for se := range niceyaml.AllBindings(dd.Bind(tc.err)) {
				got = append(got, se.Error())
			}

			assert.Equal(t, tc.want, got)
		})
	}
}

func TestSourceError_Document(t *testing.T) {
	t.Parallel()

	source := niceyaml.NewSourceFromString("a: 1\n---\nb: 2\n")
	docs, err := source.Documents()
	require.NoError(t, err)

	tcs := map[string]struct {
		err  error
		want *niceyaml.Node
	}{
		"bound by a document": {
			err:  docs[1].Bind(niceyaml.NewError("bad", niceyaml.AtPath(paths.Root().Child("b")))),
			want: docs[1],
		},
		"produced by a document": {
			err: func() error {
				_, err := docs[0].Ranges(paths.Root().Child("missing"))

				return err //nolint:wrapcheck // The error is bound already.
			}(),
			want: docs[0],
		},
		"bound by the source at a position": {
			err:  source.Bind(niceyaml.NewError("bad", niceyaml.AtPosition(position.New(0, 0)))),
			want: docs[0],
		},
		"bound by the source at no location": {
			err: source.Bind(niceyaml.NewError("bad")),
		},
		"produced by the source": {
			err: func() error {
				_, err := niceyaml.NewSourceFromString("a: [\n").File()

				return err
			}(),
		},
		"wrapping a binding keeps its document": {
			err:  docs[1].Bind(fmt.Errorf("context: %w", docs[0].Bind(niceyaml.NewError("bad")))),
			want: docs[0],
		},
		"nesting errors around a binding keeps its document": {
			err: docs[1].Bind(niceyaml.WrapError(
				docs[0].Bind(niceyaml.NewError("bad")),
				niceyaml.WithErrors(niceyaml.NewError("b", niceyaml.AtPath(paths.Root().Child("b")))),
			)),
			want: docs[0],
		},
		"nesting errors around a binding to no document keeps none": {
			err: docs[1].Bind(niceyaml.WrapError(
				source.Bind(niceyaml.NewError("bad")),
				niceyaml.WithErrors(niceyaml.NewError("b", niceyaml.AtPath(paths.Root().Child("b")))),
			)),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var bound *niceyaml.SourceError

			require.ErrorAs(t, tc.err, &bound)

			if tc.want == nil {
				assert.Nil(t, bound.Document())
			} else {
				assert.Same(t, tc.want, bound.Document())
			}
		})
	}
}

// rebasedHours is a [niceyaml.SelfValidator] that writes its path from its
// own root, as a type validated on its own does.
type rebasedHours struct {
	Open  string `yaml:"open"`
	Close string `yaml:"close"`
}

func (h rebasedHours) Validate() error {
	if h.Close < h.Open {
		return niceyaml.NewError("closes before it opens", niceyaml.AtPath(paths.Root().Child("close")))
	}

	return nil
}

// rebasedConfig holds a value that validates itself under a field.
type rebasedConfig struct {
	Name  string       `yaml:"name"`
	Hours rebasedHours `yaml:"hours"`
}

// joinedHours is a [niceyaml.SelfValidator] that reports its violations
// as a join of its own type, with paths from its own root.
type joinedHours struct {
	Open  string `yaml:"open"`
	Close string `yaml:"close"`
}

func (h joinedHours) Validate() error {
	return sparseJoinError{errs: []error{
		niceyaml.NewError("bad open", niceyaml.AtPath(paths.Root().Child("open"))),
		niceyaml.NewError("bad close", niceyaml.AtPath(paths.Root().Child("close"))),
	}}
}

// joinedConfig holds a [joinedHours] under a field.
type joinedConfig struct {
	Name  string      `yaml:"name"`
	Hours joinedHours `yaml:"hours"`
}

// errInvalidHours is the sentinel [classifiedJoinError] matches.
var errInvalidHours = errors.New("invalid hours")

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
	hours := paths.Root().Child("hours")
	closePath := paths.Root().Child("close")

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

		var e *niceyaml.Error

		require.ErrorAs(t, err, &e)

		p, ok := e.Path()
		require.True(t, ok)
		assert.Equal(t, hours, p)

		dd := yamltest.FirstDocument(t, input)

		report := niceyaml.NewError("invalid hours", niceyaml.WithErrors(joined))
		bound := dd.Bind(niceyaml.Rebase(report, hours))

		var se *niceyaml.SourceError

		require.ErrorAs(t, bound, &se)
		require.Len(t, se.Errors(), 1)

		child := se.Errors()[0]
		assert.NotPanics(t, func() {
			assert.Equal(t, "3:3: $.hours:", strings.TrimSpace(child.Error()))
			assert.Equal(t, "\n", child.Message())
			assert.Contains(t, niceyaml.FormatError(bound, 1), "3:3: $.hours: invalid hours")
		})
	})

	t.Run("path composes with the base", func(t *testing.T) {
		t.Parallel()

		err := niceyaml.Rebase(niceyaml.NewError("closes before it opens", niceyaml.AtPath(closePath)), hours)

		var e *niceyaml.Error

		require.ErrorAs(t, err, &e)

		p, ok := e.Path()
		require.True(t, ok)
		assert.Equal(t, "$.hours.close", p.String())
		assert.Equal(t, "$.hours.close: closes before it opens", err.Error())
	})

	t.Run("a decode rebases a nested self validator the same way", func(t *testing.T) {
		t.Parallel()

		source := niceyaml.NewSourceFromString(input, niceyaml.WithName("cafe.yaml"))
		doc, err := source.Document()
		require.NoError(t, err)

		_, err = doc.Decode[rebasedConfig](t.Context())
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

		dd := yamltest.FirstDocument(t, input)

		err := dd.Bind(niceyaml.Rebase(errors.New("bad hours"), hours))
		require.EqualError(t, err, "3:3: $.hours: bad hours")

		var e *niceyaml.Error

		require.ErrorAs(t, err, &e)

		p, ok := e.Path()
		require.True(t, ok)
		assert.Equal(t, "$.hours", p.String())
	})

	t.Run("nested errors compose too", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, input)

		report := niceyaml.NewError("invalid hours", niceyaml.WithErrors(
			niceyaml.NewError("bad open", niceyaml.AtPath(paths.Root().Child("open"))),
			niceyaml.NewError("bad close", niceyaml.AtPath(closePath)),
		))

		err := dd.Bind(niceyaml.Rebase(report, hours))
		require.EqualError(t, err, "3:3: $.hours: invalid hours")

		var bound *niceyaml.SourceError

		require.ErrorAs(t, err, &bound)
		require.Len(t, bound.Errors(), 2)

		assert.Equal(t, "3:9: $.hours.open: bad open", bound.Errors()[0].Error())
		assert.Equal(t, "4:10: $.hours.close: bad close", bound.Errors()[1].Error())
		assert.Equal(t, "bad open", bound.Errors()[0].Message())

		p, ok := bound.Errors()[0].Path()
		require.True(t, ok)
		assert.Equal(t, "$.hours.open", p.String())
	})

	t.Run("an unbound tree shows nested paths under the base", func(t *testing.T) {
		t.Parallel()

		report := niceyaml.NewError("invalid hours", niceyaml.WithErrors(
			niceyaml.NewError("bad open", niceyaml.AtPath(paths.Root().Child("open"))),
		))

		assert.Equal(t, stringtest.JoinLF(
			"$.hours: invalid hours",
			"`-- $.hours.open: bad open",
		), fmt.Sprintf("%+v", niceyaml.Rebase(report, hours)))
	})

	t.Run("a join rebases branch by branch", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, input)

		open := niceyaml.NewError("bad open", niceyaml.AtPath(paths.Root().Child("open")))
		other := errors.New("bad hours")

		err := niceyaml.Rebase(errors.Join(open, other), hours)
		require.EqualError(t, err, "$.hours.open: bad open\n$.hours: bad hours")
		require.ErrorIs(t, err, open)
		require.ErrorIs(t, err, other)

		got := []string{}
		for se := range niceyaml.AllBindings(dd.Bind(err)) {
			got = append(got, se.Error())
		}

		assert.Equal(t, []string{
			"$.hours.open: bad open\n$.hours: bad hours",
			"3:9: $.hours.open: bad open",
			"3:3: $.hours: bad hours",
		}, got)
	})

	t.Run("an error that only wraps a join rebases as the join", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, input)

		open := niceyaml.NewError("bad open", niceyaml.AtPath(paths.Root().Child("open")))
		closeErr := niceyaml.NewError("bad close", niceyaml.AtPath(closePath))

		err := niceyaml.Rebase(niceyaml.WrapError(errors.Join(open, closeErr)), hours)
		require.EqualError(t, err, "$.hours.open: bad open\n$.hours.close: bad close")
		require.ErrorIs(t, err, open)
		require.ErrorIs(t, err, closeErr)

		var e *niceyaml.Error

		require.ErrorAs(t, err, &e)

		got := []string{}
		for se := range niceyaml.AllBindings(dd.Bind(err)) {
			got = append(got, se.Error())
		}

		assert.Equal(t, []string{
			"$.hours.open: bad open\n$.hours.close: bad close",
			"3:9: $.hours.open: bad open",
			"4:10: $.hours.close: bad close",
		}, got)

		bare := dd.Bind(niceyaml.Rebase(errors.Join(open, closeErr), hours))
		assert.Equal(t, niceyaml.FormatError(bare, -1), niceyaml.FormatError(dd.Bind(err), -1))
	})

	t.Run("a join of the caller's own type keeps its type", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, input)

		open := niceyaml.NewError("bad open", niceyaml.AtPath(paths.Root().Child("open")))
		closeErr := niceyaml.NewError("bad close", niceyaml.AtPath(closePath))
		joined := sparseJoinError{errs: []error{open, nil, closeErr}}

		bindings := func(err error) []string {
			got := []string{}
			for se := range niceyaml.AllBindings(dd.Bind(err)) {
				got = append(got, se.Error())
			}

			return got
		}

		bare := niceyaml.Rebase(errors.Join(open, closeErr), hours)

		tcs := map[string]struct {
			err error
		}{
			"join": {
				err: joined,
			},
			"error that only wraps the join": {
				err: niceyaml.WrapError(joined),
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				err := niceyaml.Rebase(tc.err, hours)
				require.EqualError(t, err, "$.hours.open: bad open\n$.hours.close: bad close")
				require.ErrorIs(t, err, open)
				require.ErrorIs(t, err, closeErr)

				var got sparseJoinError

				require.ErrorAs(t, err, &got)
				assert.Equal(t, joined, got)

				assert.Equal(t, bindings(bare), bindings(err))
				assert.Equal(t, niceyaml.FormatError(dd.Bind(bare), -1), niceyaml.FormatError(dd.Bind(err), -1))
			})
		}
	})

	t.Run("a join of the caller's own type keeps its Is and As methods", func(t *testing.T) {
		t.Parallel()

		joined := classifiedJoinError{sparseJoinError{errs: []error{
			niceyaml.NewError("bad open", niceyaml.AtPath(paths.Root().Child("open"))),
			niceyaml.NewError("bad close", niceyaml.AtPath(closePath)),
		}}}

		err := niceyaml.Rebase(joined, hours)
		require.EqualError(t, err, "$.hours.open: bad open\n$.hours.close: bad close")
		require.ErrorIs(t, err, errInvalidHours)

		var custom *customTestError

		require.ErrorAs(t, err, &custom)
		assert.Equal(t, "invalid hours", custom.Error())
	})

	t.Run("a decode keeps the type of a join a self validator returns", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, input)

		_, err := dd.Decode[joinedConfig](t.Context())
		require.EqualError(t, err, "$.hours.open: bad open\n$.hours.close: bad close")

		var got sparseJoinError

		require.ErrorAs(t, err, &got)
	})

	t.Run("a nested error without a location points at the base", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, input)

		report := niceyaml.NewError("invalid hours", niceyaml.WithErrors(errors.New("bad hours")))

		err := dd.Bind(niceyaml.Rebase(report, hours))

		var bound *niceyaml.SourceError

		require.ErrorAs(t, err, &bound)
		require.Len(t, bound.Errors(), 1)
		assert.Equal(t, "3:3: $.hours: bad hours", bound.Errors()[0].Error())
	})

	t.Run("an unlocated nested error under a root base points at the root", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, input)

		report := niceyaml.NewError("invalid", niceyaml.WithErrors(errors.New("bad child")))
		rebased := niceyaml.Rebase(report, paths.Root())

		err := dd.Bind(rebased)
		require.EqualError(t, err, "1:1: $: invalid")

		var bound *niceyaml.SourceError

		require.ErrorAs(t, err, &bound)
		require.Len(t, bound.Errors(), 1)

		child := bound.Errors()[0]
		assert.Equal(t, "1:1: $: bad child", child.Error())

		p, ok := child.Path()
		require.True(t, ok)
		assert.Equal(t, paths.Root(), p)

		assert.Equal(t, stringtest.JoinLF(
			"$: invalid",
			"`-- $: bad child",
		), fmt.Sprintf("%+v", rebased))
	})

	t.Run("rebases compose", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, "spec:\n  hours:\n    open: 1\n    close: 0\n")

		inner := niceyaml.NewError("closes before it opens", niceyaml.AtPath(closePath))
		err := niceyaml.Rebase(niceyaml.Rebase(inner, hours), paths.Root().Child("spec"))

		assert.Equal(t, "$.spec.hours.close: closes before it opens", err.Error())
		require.EqualError(t, dd.Bind(err), "4:12: $.spec.hours.close: closes before it opens")
	})

	t.Run("a position stays as it is", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, input)

		err := dd.Bind(niceyaml.Rebase(niceyaml.NewError("bad", niceyaml.AtPosition(position.New(0, 6))), hours))
		require.EqualError(t, err, "1:7: bad")
	})

	t.Run("a bound error comes back as it is", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, input)

		bound := dd.Bind(niceyaml.NewError("bad", niceyaml.AtPath(closePath)))
		require.Error(t, bound)

		assert.Same(t, bound, niceyaml.Rebase(bound, hours))

		wrapped := fmt.Errorf("context: %w", bound)
		assert.Same(t, wrapped, niceyaml.Rebase(wrapped, hours))
	})

	t.Run("errors nested above a binding join the base", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, input)

		namePath := paths.Root().Child("name")
		bound := dd.Bind(niceyaml.NewError("bad name", niceyaml.AtPath(namePath)))
		wrapped := niceyaml.WrapError(bound, niceyaml.WithErrors(
			niceyaml.NewError("bad open", niceyaml.AtPath(paths.Root().Child("open"))),
		))

		r := niceyaml.Rebase(wrapped, hours)
		assert.Equal(t, "1:7: $.name: bad name", r.Error())

		var e *niceyaml.Error

		require.ErrorAs(t, r, &e)

		p, ok := e.Path()
		require.True(t, ok)
		assert.Equal(t, namePath, p)

		var rebound *niceyaml.SourceError

		require.ErrorAs(t, dd.Bind(r), &rebound)
		assert.Equal(t, "1:7: $.name: bad name", rebound.Error())

		p, ok = rebound.Path()
		require.True(t, ok)
		assert.Equal(t, namePath, p)

		require.Len(t, rebound.Errors(), 1)
		assert.Equal(t, "3:9: $.hours.open: bad open", rebound.Errors()[0].Error())
	})

	t.Run("an unlocated binding under the base keeps no path", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, input)

		bound := dd.Bind(errors.New("plain"))
		wrapped := niceyaml.WrapError(bound, niceyaml.WithErrors(
			niceyaml.NewError("bad open", niceyaml.AtPath(paths.Root().Child("open"))),
		))

		r := niceyaml.Rebase(wrapped, hours)
		assert.Equal(t, bound.Error(), r.Error())

		var e *niceyaml.Error

		require.ErrorAs(t, r, &e)

		_, ok := e.Path()
		assert.False(t, ok)

		var rebound *niceyaml.SourceError

		require.ErrorAs(t, dd.Bind(r), &rebound)

		_, ok = rebound.Path()
		assert.False(t, ok)
	})

	t.Run("a location set on the rebased error is under the base", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, input)

		var e *niceyaml.Error

		inner := niceyaml.NewError("closes before it opens", niceyaml.AtPath(closePath))
		require.ErrorAs(t, niceyaml.Rebase(inner, hours), &e)

		moved := e.With(niceyaml.AtPath(paths.Root().Child("open")))

		p, ok := moved.Path()
		require.True(t, ok)
		assert.Equal(t, hours.Child("open"), p)
		assert.Equal(t, "$.hours.open: closes before it opens", moved.Error())
		require.EqualError(t, dd.Bind(moved), `3:9: $.hours.open: closes before it opens`)
	})

	t.Run("text a wrapper added stays as it is", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, input)

		inner := niceyaml.NewError("closes before it opens", niceyaml.AtPath(closePath))
		err := niceyaml.Rebase(fmt.Errorf("checking hours: %w", inner), hours)

		require.ErrorIs(t, err, inner)
		assert.Equal(t, "$.hours.close: checking hours: $.close: closes before it opens", err.Error())
		require.EqualError(t, dd.Bind(err), "4:10: $.hours.close: checking hours: $.close: closes before it opens")
	})

	t.Run("a check runs under the scope it is written for", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, input)

		checkHours := func(h *rebasedHours) error {
			return h.Validate()
		}

		c, err := dd.Decode[rebasedConfig](t.Context(), niceyaml.WithSelfValidation(false))
		require.NoError(t, err)

		err = dd.Bind(niceyaml.Rebase(checkHours(&c.Hours), hours))
		require.EqualError(t, err, "4:10: $.hours.close: closes before it opens")
	})
}

func TestError_TokenAfterTrailingSpaces(t *testing.T) {
	t.Parallel()

	// The lexer counts the spaces after a value into its column, and the
	// Source places the token where its text starts, so an error at the
	// token marks the value rather than the spaces after it.
	src := xmlSource("a: 1   \nb: 2\n")

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

	got := trimLines(render(err))
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
			input:   "parent:\n  child: x\n  other: 1\n",
			at:      position.New(2, 0),
			want:    "     |   ^^^^^ bad",
			wantXML: "<textError>  ^ bad</textError>",
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
			wantXML: "<textError>     ^ bad</textError>",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			source := niceyaml.NewSourceFromString(tc.input)

			nested := yamltest.Bind(t, source, niceyaml.NewError(
				"summary",
				niceyaml.WithErrors(niceyaml.NewError("bad", niceyaml.AtPosition(tc.at))),
			))
			assert.Contains(t, niceyaml.FormatError(nested, 0), "\n"+tc.want)
			assert.Contains(t, trimLines(renderContext(nested, 0)), "\n"+tc.wantXML)

			var bound *niceyaml.SourceError

			require.ErrorAs(t, yamltest.Bind(t, source, niceyaml.NewError(
				"bad", niceyaml.AtPosition(tc.at),
			)), &bound)

			view := source.View()
			require.True(t, bound.Annotate(view))
			assert.Contains(t, view.String(), "\n"+tc.want)

			// The error still reports the column as given.
			wantPrefix := fmt.Sprintf("%d:%d: ", tc.at.Line+1, tc.at.Col+1)
			assert.True(t, strings.HasPrefix(bound.Error(), wantPrefix), bound.Error())
		})
	}
}

func TestFormat(t *testing.T) {
	t.Parallel()

	t.Run("a bare binding renders as the %+v verb does", func(t *testing.T) {
		t.Parallel()

		err := excerptError(t)
		assert.Equal(t, fmt.Sprintf("%+v", err), niceyaml.FormatError(err, 2))
	})

	t.Run("looks through a wrapper for the excerpt", func(t *testing.T) {
		t.Parallel()

		src := niceyaml.NewSourceFromString("a: 1\nb: 2\n", niceyaml.WithName("x.yaml"))
		bound := yamltest.Bind(t, src, niceyaml.NewError("bad", niceyaml.AtPath(paths.Root().Child("b"))))
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
		), niceyaml.FormatError(err, 2))
	})

	t.Run("lists the nodes below a wrapped binding", func(t *testing.T) {
		t.Parallel()

		err := fmt.Errorf("checking: %w", excerptError(t))

		assert.Equal(t, stringtest.JoinLF(
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
		), niceyaml.FormatError(err, 2))
	})

	t.Run("renders one excerpt per source of a join under its name", func(t *testing.T) {
		t.Parallel()

		first := niceyaml.NewSourceFromString("a: 1\n", niceyaml.WithName("a.yaml"))
		second := niceyaml.NewSourceFromString("b: 2\n", niceyaml.WithName("b.yaml"))

		err := errors.Join(
			yamltest.Bind(t, first, niceyaml.NewError("bad a", niceyaml.AtPath(paths.Root().Child("a")))),
			yamltest.Bind(t, second, niceyaml.NewError("bad b", niceyaml.AtPath(paths.Root().Child("b")))),
		)

		assert.Equal(t, stringtest.JoinLF(
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
		), niceyaml.FormatError(err, 2))
	})

	t.Run("renders the documents of one file as one excerpt", func(t *testing.T) {
		t.Parallel()

		source := niceyaml.NewSourceFromString(stringtest.Input(`
			a: x
			---
			a: y
			---
			a: 1
			b: 2
			---
			c: 3
		`), niceyaml.WithName("multi.yaml"))

		docs, err := source.Documents()
		require.NoError(t, err)
		require.Len(t, docs, 4)

		// One binding per document that fails, joined, as a caller that
		// validates each document of a file builds one.
		joined := errors.Join(
			docs[0].Bind(niceyaml.NewError("not a number", niceyaml.AtPath(paths.Root().Child("a")))),
			docs[1].Bind(niceyaml.NewError("not a number", niceyaml.AtPath(paths.Root().Child("a")))),
			docs[2].Bind(niceyaml.NewError("2 problems", niceyaml.WithErrors(
				niceyaml.NewError("not a string", niceyaml.AtPath(paths.Root().Child("b"))),
			))),
			docs[3].Bind(niceyaml.NewError("not allowed", niceyaml.AtPath(paths.Root().Child("c").Key()))),
		)

		assert.Equal(t, stringtest.JoinLF(
			"|-- multi.yaml:1:4: $.a: not a number",
			"|-- multi.yaml:3:4: $.a: not a number",
			"|-- multi.yaml: 2 problems",
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
		), niceyaml.FormatError(joined, 2))

		// Each binding still renders on its own, for a caller that wants
		// one section per document.
		var bound *niceyaml.SourceError

		require.ErrorAs(t, docs[1].Bind(niceyaml.NewError("bad", niceyaml.AtPath(paths.Root().Child("a")))), &bound)

		assert.Equal(t, stringtest.JoinLF(
			"multi.yaml:3:4: $.a: bad",
			"",
			"   3 | a: y",
			"     |    ^",
		), niceyaml.FormatError(bound, 0))
	})

	t.Run("names why a binding of a join marks nothing after the excerpts", func(t *testing.T) {
		t.Parallel()

		source := niceyaml.NewSourceFromString("a: 1\n---\nb: 2\n", niceyaml.WithName("f.yaml"))

		docs, err := source.Documents()
		require.NoError(t, err)

		joined := errors.Join(
			docs[0].Bind(niceyaml.NewError("gone", niceyaml.AtExactPath(paths.Root().Child("x")))),
			docs[1].Bind(niceyaml.NewError("bad b", niceyaml.AtPath(paths.Root().Child("b")))),
			docs[1].Bind(errors.New("plain")),
		)

		assert.Equal(t, stringtest.JoinLF(
			"|-- f.yaml: $.x: gone",
			"|-- f.yaml:3:4: $.b: bad b",
			"`-- f.yaml: plain",
			"",
			"   1 | a: 1",
			"   2 | ---",
			"   3 | b: 2",
			"     |    ^ bad b",
			"",
			"no excerpt: resolve $.x: not found",
		), niceyaml.FormatError(joined, 2))
	})

	t.Run("an error bound to no source renders as its message", func(t *testing.T) {
		t.Parallel()

		assert.Equal(t, "plain", niceyaml.FormatError(errors.New("plain"), 2))

		located := niceyaml.NewError("bad", niceyaml.AtPath(paths.Root().Child("a")))

		assert.Equal(t, "$.a: bad", niceyaml.FormatError(located, 2))
	})

	t.Run("a tab in a message expands to spaces", func(t *testing.T) {
		t.Parallel()

		// Each tab becomes four spaces, wherever it falls in its row.
		tcs := map[string]struct {
			err  error
			want string
		}{
			"message with no nested errors": {
				err:  errors.New("Did you mean this?\n\tvalidate"),
				want: "Did you mean this?\n    validate",
			},
			"branches of a join": {
				err: errors.Join(errors.New("a\tb"), errors.New("Did you mean this?\n\tvalidate")),
				want: stringtest.JoinLF(
					"|-- a    b",
					"`-- Did you mean this?",
					"        validate",
				),
			},
			// The tree and the reason a location did not resolve both spell
			// the key. Their prefixes differ in width by other than a
			// multiple of four, so the two lines match only if the width of
			// a tab does not depend on its column.
			"key of a path that does not resolve": {
				err: yamltest.Bind(t,
					niceyaml.NewSourceFromString("a: 1\n", niceyaml.WithName("cfg.yaml")),
					niceyaml.NewError("bad", niceyaml.AtExactPath(paths.Root().Child("ab\tc"))),
				),
				want: stringtest.JoinLF(
					"cfg.yaml: $.'ab    c': bad",
					"",
					"no excerpt: resolve $.'ab    c': not found",
				),
			},
			// The reason stays on one row, so a line feed in the key is a
			// picture there while it starts a new row in the tree.
			"key with a line feed and a tab": {
				err: yamltest.Bind(t,
					niceyaml.NewSourceFromString("a: 1\n", niceyaml.WithName("cfg.yaml")),
					niceyaml.NewError("bad", niceyaml.AtExactPath(paths.Root().Child("x\ny\tz"))),
				),
				want: stringtest.JoinLF(
					"cfg.yaml: $.'x",
					"y    z': bad",
					"",
					"no excerpt: resolve $.'x\u240ay    z': not found",
				),
			},
			// The excerpt spells the message beside the caret as the tree
			// spells it.
			"message beside a caret": {
				err: yamltest.Bind(t,
					niceyaml.NewSourceFromString("a: 1\nb: 2\n", niceyaml.WithName("cfg.yaml")),
					niceyaml.NewError("2 problems", niceyaml.WithErrors(
						niceyaml.NewError("bad\ta", niceyaml.AtPath(paths.Root().Child("a"))),
					)),
				),
				want: stringtest.JoinLF(
					"cfg.yaml: 2 problems",
					"`-- 1:4: $.a: bad    a",
					"",
					"   1 | a: 1",
					"     |    ^ bad    a",
					"   2 | b: 2",
				),
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				assert.Equal(t, tc.want, niceyaml.FormatError(tc.err, 2))
			})
		}
	})

	t.Run("nil renders as nothing", func(t *testing.T) {
		t.Parallel()

		assert.Empty(t, niceyaml.FormatError(nil, 2))
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

			got := niceyaml.FormatError(err, 2)

			assert.Equal(t, tc.want, got)
			assert.Equal(t, niceyaml.FormatError(errors.New(err.Error()), 2), got)
			assert.Equal(t, got, fmt.Sprintf("%+v", err))
			assert.NotContains(t, got, "\x1b")
		})
	}
}

func TestSourceError_MessageAndPath(t *testing.T) {
	t.Parallel()

	src := niceyaml.NewSourceFromString("a:\n  b: 1\n", niceyaml.WithName("x.yaml"))
	bPath := paths.Root().Child("a", "b")

	bind := func(t *testing.T, err error) *niceyaml.SourceError {
		t.Helper()

		var bound *niceyaml.SourceError

		require.ErrorAs(t, yamltest.Bind(t, src, err), &bound)

		return bound
	}

	t.Run("a path error", func(t *testing.T) {
		t.Parallel()

		bound := bind(t, niceyaml.NewError("bad", niceyaml.AtPath(bPath)))

		assert.Equal(t, "x.yaml:2:6: $.a.b: bad", bound.Error())
		assert.Equal(t, "bad", bound.Message())

		p, ok := bound.Path()
		require.True(t, ok)
		assert.Equal(t, bPath, p)
	})

	t.Run("a position error has no path", func(t *testing.T) {
		t.Parallel()

		bound := bind(t, niceyaml.NewError("bad", niceyaml.AtPosition(position.New(1, 5))))

		assert.Equal(t, "bad", bound.Message())

		_, ok := bound.Path()
		assert.False(t, ok)
	})

	t.Run("a path beside a range names the value and binds at the range", func(t *testing.T) {
		t.Parallel()

		// The key "b" on line 2, not the value the path selects.
		rng := position.NewRange(position.New(1, 2), position.New(1, 3))
		bound := bind(t, niceyaml.NewError("bad", niceyaml.AtPath(bPath), niceyaml.AtRange(rng)))

		assert.Equal(t, "x.yaml:2:3: $.a.b: bad", bound.Error())
		assert.Equal(t, "bad", bound.Message())

		p, ok := bound.Path()
		require.True(t, ok)
		assert.Equal(t, bPath, p)

		got, ok := bound.Range()
		require.True(t, ok)
		assert.Equal(t, rng, got)

		// The excerpt marks the one character of the range, not the
		// whole value the path selects.
		excerpt, ok := bound.Excerpt(0)
		require.True(t, ok)
		assert.Equal(t, stringtest.JoinLF(
			"   2 |   b: 1",
			"     |   ^",
		), excerpt.String())
	})

	t.Run("a path beside a position binds at the position", func(t *testing.T) {
		t.Parallel()

		bound := bind(t, niceyaml.NewError("bad", niceyaml.AtPath(bPath), niceyaml.AtPosition(position.New(1, 2))))

		assert.Equal(t, "x.yaml:2:3: $.a.b: bad", bound.Error())

		p, ok := bound.Path()
		require.True(t, ok)
		assert.Equal(t, bPath, p)

		got, ok := bound.Range()
		require.True(t, ok)
		assert.Equal(t, position.New(1, 2), got.Start)
	})

	t.Run("a range locates the error whether or not the path resolves", func(t *testing.T) {
		t.Parallel()

		rng := position.NewRange(position.New(1, 2), position.New(1, 3))
		nope := paths.Root().Child("nope")
		bound := bind(t, niceyaml.NewError("bad", niceyaml.AtPath(nope), niceyaml.AtRange(rng)))

		require.NoError(t, bound.Unresolved())
		assert.Equal(t, "x.yaml:2:3: $.nope: bad", bound.Error())

		p, ok := bound.Path()
		require.True(t, ok)
		assert.Equal(t, nope, p)

		got, ok := bound.Range()
		require.True(t, ok)
		assert.Equal(t, rng, got)
	})

	t.Run("a rebased path beside a range joins the base and keeps the range", func(t *testing.T) {
		t.Parallel()

		rng := position.NewRange(position.New(1, 2), position.New(1, 3))
		inner := niceyaml.NewError("bad", niceyaml.AtPath(paths.Root().Child("b")), niceyaml.AtRange(rng))
		bound := bind(t, niceyaml.Rebase(inner, paths.Root().Child("a")))

		assert.Equal(t, "x.yaml:2:3: $.a.b: bad", bound.Error())

		p, ok := bound.Path()
		require.True(t, ok)
		assert.Equal(t, bPath, p)

		got, ok := bound.Range()
		require.True(t, ok)
		assert.Equal(t, rng, got)
	})

	t.Run("a rebased path joins the base", func(t *testing.T) {
		t.Parallel()

		inner := niceyaml.NewError("bad", niceyaml.AtPath(paths.Root().Child("b")))
		bound := bind(t, niceyaml.Rebase(inner, paths.Root().Child("a")))

		assert.Equal(t, "bad", bound.Message())

		p, ok := bound.Path()
		require.True(t, ok)
		assert.Equal(t, bPath, p)
	})

	t.Run("a path from a scoped document reads from the document root", func(t *testing.T) {
		t.Parallel()

		doc, err := src.Document()
		require.NoError(t, err)

		scoped, err := doc.At(paths.Root().Child("a"))
		require.NoError(t, err)

		var bound *niceyaml.SourceError

		require.ErrorAs(t, scoped.Bind(niceyaml.NewError("bad", niceyaml.AtPath(paths.Root().Child("b")))), &bound)

		assert.Equal(t, "x.yaml:2:6: $.a.b: bad", bound.Error())
		assert.Equal(t, "bad", bound.Message())
		assert.Same(t, scoped, bound.Node())

		p, ok := bound.Path()
		require.True(t, ok)
		assert.Equal(t, bPath, p)

		rng, ok := bound.Range()
		require.True(t, ok)
		assert.Equal(t, 1, rng.Start.Line)
	})

	t.Run("text a wrapper added stays", func(t *testing.T) {
		t.Parallel()

		bound := bind(t, fmt.Errorf("ctx: %w", niceyaml.NewError("bad", niceyaml.AtPath(bPath))))

		assert.Equal(t, "ctx: $.a.b: bad", bound.Message())

		p, ok := bound.Path()
		require.True(t, ok)
		assert.Equal(t, bPath, p)
	})

	t.Run("a binding that wraps a binding reports the path of the inner one", func(t *testing.T) {
		t.Parallel()

		inner := bind(t, niceyaml.NewError("bad", niceyaml.AtPath(bPath)))

		// A nested error that wraps a binding binds as a child that takes
		// over the inner binding, rather than as the inner binding itself.
		outer := bind(t, niceyaml.NewError("outer", niceyaml.WithErrors(fmt.Errorf("ctx: %w", inner))))
		require.Len(t, outer.Errors(), 1)

		child := outer.Errors()[0]
		require.NotSame(t, inner, child)
		assert.Equal(t, "ctx: x.yaml:2:6: $.a.b: bad", child.Message())

		p, ok := child.Path()
		require.True(t, ok)
		assert.Equal(t, bPath, p)
	})

	t.Run("an Error that nests errors around a binding keeps the inner message", func(t *testing.T) {
		t.Parallel()

		inner := bind(t, niceyaml.NewError("bad", niceyaml.AtPath(bPath)))

		// The Error adds no text of its own, so the message is the one the
		// inner binding annotates with, and Error still reports the
		// position and path in front of it.
		wrapped := bind(t, niceyaml.WrapError(inner, niceyaml.WithErrors(errors.New("zz"))))
		assert.Equal(t, "bad", wrapped.Message())
		assert.Equal(t, "x.yaml:2:6: $.a.b: bad", wrapped.Error())

		outer := bind(t, niceyaml.NewError("outer", niceyaml.WithErrors(
			niceyaml.WrapError(inner, niceyaml.WithErrors(errors.New("zz"))),
		)))
		require.NotEmpty(t, outer.Errors())
		assert.Equal(t, "bad", outer.Errors()[0].Message())

		got := niceyaml.FormatError(outer, 0)
		assert.Contains(t, got, "^ bad")
		assert.NotContains(t, got, "^ x.yaml:")
	})

	t.Run("an error without a location has neither", func(t *testing.T) {
		t.Parallel()

		bound := bind(t, errors.New("plain"))

		assert.Equal(t, "plain", bound.Message())

		_, ok := bound.Path()
		assert.False(t, ok)
	})

	t.Run("nil has neither", func(t *testing.T) {
		t.Parallel()

		var bound *niceyaml.SourceError

		assert.Empty(t, bound.Message())

		_, ok := bound.Path()
		assert.False(t, ok)
	})
}

func TestSourceError_Nearest(t *testing.T) {
	t.Parallel()

	source := niceyaml.NewSourceFromString(stringtest.Input(`
		servers:
		  - port: 80
		    name: a
		  - port: 81
		tls:
		note: hello
	`), niceyaml.WithName("cfg.yaml"))

	doc, err := source.Document()
	require.NoError(t, err)

	second := paths.Root().Child("servers").Index(1)
	name := second.Child("name")

	tcs := map[string]struct {
		err error
		// The message, the path of the mapping the error is bound at,
		// when it is bound at one, and the range it covers, or the
		// reason it has none.
		want   string
		near   string
		rng    position.Range
		reason error
	}{
		"key an element of a sequence leaves out": {
			err:  niceyaml.NewError("name is required", niceyaml.AtPath(name)),
			want: "cfg.yaml:4:5: $.servers[1].name: name is required",
			near: "$.servers[1]",
			rng:  position.NewRange(position.New(3, 4), position.New(3, 8)),
		},
		"key the root leaves out": {
			err:  niceyaml.NewError("owner is required", niceyaml.AtPath(paths.Root().Child("owner"))),
			want: "cfg.yaml:1:1: $.owner: owner is required",
			near: "$",
			rng:  position.NewRange(position.New(0, 0), position.New(0, 7)),
		},
		"several names below a null": {
			err:  niceyaml.NewError("cert is required", niceyaml.AtPath(paths.Root().Child("tls", "cert", "file"))),
			want: "cfg.yaml:5:1: $.tls.cert.file: cert is required",
			near: "$.tls",
			rng:  position.NewRange(position.New(4, 0), position.New(4, 3)),
		},
		"rebased under the mapping": {
			err: niceyaml.Rebase(
				niceyaml.NewError("name is required", niceyaml.AtPath(paths.Root().Child("name"))),
				second,
			),
			want: "cfg.yaml:4:5: $.servers[1].name: name is required",
			near: "$.servers[1]",
			rng:  position.NewRange(position.New(3, 4), position.New(3, 8)),
		},
		"key the document holds": {
			err:  niceyaml.NewError("bad name", niceyaml.AtPath(paths.Root().Child("servers").Index(0).Child("name"))),
			want: "cfg.yaml:3:11: $.servers[0].name: bad name",
			rng:  position.NewRange(position.New(2, 10), position.New(2, 11)),
		},
		"path beside a range": {
			err: niceyaml.NewError("name is required",
				niceyaml.AtPath(name),
				niceyaml.AtRange(position.NewRange(position.New(3, 2), position.New(3, 3))),
			),
			want: "cfg.yaml:4:3: $.servers[1].name: name is required",
			rng:  position.NewRange(position.New(3, 2), position.New(3, 3)),
		},
		"exact path": {
			err:    niceyaml.NewError("name is required", niceyaml.AtExactPath(name)),
			want:   "cfg.yaml: $.servers[1].name: name is required",
			reason: paths.ErrNotFound,
		},
		"exact path the last option replaces": {
			err:  niceyaml.NewError("name is required", niceyaml.AtExactPath(name), niceyaml.AtPath(name)),
			want: "cfg.yaml:4:5: $.servers[1].name: name is required",
			near: "$.servers[1]",
			rng:  position.NewRange(position.New(3, 4), position.New(3, 8)),
		},
		"path the last exact option replaces": {
			err:    niceyaml.NewError("name is required", niceyaml.AtPath(name), niceyaml.AtExactPath(name)),
			want:   "cfg.yaml: $.servers[1].name: name is required",
			reason: paths.ErrNotFound,
		},
		"exact path rebased under the mapping": {
			err: niceyaml.Rebase(
				niceyaml.NewError("name is required", niceyaml.AtExactPath(paths.Root().Child("name"))),
				second,
			),
			want:   "cfg.yaml: $.servers[1].name: name is required",
			reason: paths.ErrNotFound,
		},
		"name in a scalar": {
			err:    niceyaml.NewError("bad", niceyaml.AtPath(paths.Root().Child("note", "text"))),
			want:   "cfg.yaml: $.note.text: bad",
			reason: paths.ErrNotFound,
		},
		"index past the end": {
			err:    niceyaml.NewError("bad", niceyaml.AtPath(paths.Root().Child("servers").Index(5).Child("name"))),
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

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			err := doc.Bind(tc.err)
			require.EqualError(t, err, tc.want)

			var bound *niceyaml.SourceError

			require.ErrorAs(t, err, &bound)

			near, ok := bound.Nearest()
			assert.Equal(t, tc.near != "", ok)

			if ok {
				assert.Equal(t, tc.near, near.String())
			} else {
				assert.Equal(t, paths.Root(), near)
			}

			rng, located := bound.Range()
			if !located {
				requireUnresolved(t, bound, tc.reason)
				assert.Equal(t, position.Range{}, tc.rng, "case expects a range")

				return
			}

			assert.Equal(t, tc.rng, rng)
			require.NoError(t, bound.Unresolved())
		})
	}

	t.Run("the message keeps the path and the excerpt marks the mapping", func(t *testing.T) {
		t.Parallel()

		err := doc.Bind(niceyaml.NewError("name is required", niceyaml.AtPath(name)))

		assert.Equal(t, stringtest.JoinLF(
			"cfg.yaml:4:5: $.servers[1].name: name is required",
			"",
			"   3 |     name: a",
			"   4 |   - port: 81",
			"     |     ^^^^",
			"   5 | tls:",
		), niceyaml.FormatError(err, 1))

		var bound *niceyaml.SourceError

		require.ErrorAs(t, err, &bound)

		path, ok := bound.Path()
		require.True(t, ok)
		assert.Equal(t, name, path)
	})

	t.Run("a nested error names the key it misses beside the mapping", func(t *testing.T) {
		t.Parallel()

		err := doc.Bind(niceyaml.NewError("2 problems", niceyaml.WithErrors(
			niceyaml.NewError("name is required", niceyaml.AtPath(name)),
			niceyaml.NewError("cert is required", niceyaml.AtPath(paths.Root().Child("tls", "cert"))),
		)))

		assert.Equal(t, stringtest.JoinLF(
			"cfg.yaml: 2 problems",
			"|-- 4:5: $.servers[1].name: name is required",
			"`-- 5:1: $.tls.cert: cert is required",
			"",
			"   4 |   - port: 81",
			"     |     ^^^^ name is required",
			"   5 | tls:",
			"     | ^^^ cert is required",
		), niceyaml.FormatError(err, 0))
	})

	t.Run("a scoped Node binds a key it leaves out at its own first key", func(t *testing.T) {
		t.Parallel()

		scoped := yamltest.At(t, doc, second)

		err := scoped.Bind(niceyaml.NewError("name is required", niceyaml.AtPath(paths.Root().Child("name"))))
		require.EqualError(t, err, "cfg.yaml:4:5: $.servers[1].name: name is required")

		var bound *niceyaml.SourceError

		require.ErrorAs(t, err, &bound)

		near, ok := bound.Nearest()
		require.True(t, ok)
		assert.Equal(t, second, near)
	})

	t.Run("a SelfValidator reports a field the document leaves out", func(t *testing.T) {
		t.Parallel()

		_, err := doc.Decode[requiredNames](t.Context())
		require.EqualError(t, err, "cfg.yaml:4:5: $.servers[1].name: name is required")
	})

	t.Run("a binding that wraps one bound at a mapping reports that mapping", func(t *testing.T) {
		t.Parallel()

		inner := doc.Bind(niceyaml.NewError("name is required", niceyaml.AtPath(name)))

		var outer *niceyaml.SourceError

		require.ErrorAs(t, doc.Bind(niceyaml.NewError("outer", niceyaml.WithErrors(
			fmt.Errorf("check: %w", inner),
		))), &outer)
		require.Len(t, outer.Errors(), 1)

		near, ok := outer.Errors()[0].Nearest()
		require.True(t, ok)
		assert.Equal(t, second, near)
	})

	t.Run("nil has none", func(t *testing.T) {
		t.Parallel()

		var bound *niceyaml.SourceError

		near, ok := bound.Nearest()
		assert.False(t, ok)
		assert.Equal(t, paths.Root(), near)
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
		return niceyaml.NewError("name is required", niceyaml.AtPath(paths.Root().Child("name")))
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

func TestAllBindings(t *testing.T) {
	t.Parallel()

	source := niceyaml.NewSourceFromString("a: 1\n---\nb: 2\n")

	docs, err := source.Documents()
	require.NoError(t, err)
	require.Len(t, docs, 2)

	first := docs[0].Bind(niceyaml.NewError("bad a", niceyaml.AtPath(paths.Root().Child("a"))))
	second := docs[1].Bind(niceyaml.NewError("bad b", niceyaml.AtPath(paths.Root().Child("b"))))
	other := yamltest.Bind(
		t,
		niceyaml.NewSourceFromString("c: 3\n"),
		niceyaml.NewError("outer", niceyaml.WithErrors(first)),
	)
	tree := docs[0].Bind(niceyaml.NewError("2 violations", niceyaml.WithErrors(
		niceyaml.NewError("bad a", niceyaml.AtPath(paths.Root().Child("a"))),
		niceyaml.NewError("missing", niceyaml.AtExactPath(paths.Root().Child("nope"))),
	)))
	nested := yamltest.Bind(t, niceyaml.NewSourceFromString("a:\n  b: 1\n  c: 2\n"), niceyaml.NewError(
		"2 violations",
		niceyaml.WithErrors(
			niceyaml.NewError("bad b", niceyaml.AtPath(paths.Root().Child("a", "b"))),
			niceyaml.NewError("bad a", niceyaml.AtPath(paths.Root().Child("a")), niceyaml.WithErrors(
				niceyaml.NewError("bad c", niceyaml.AtPath(paths.Root().Child("a", "c"))),
			)),
		),
	))

	messages := func(err error) []string {
		var got []string

		for b := range niceyaml.AllBindings(err) {
			got = append(got, b.Message())
		}

		return got
	}

	tcs := map[string]struct {
		err  error
		want []string
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
		"one binding through a wrapper": {
			err:  fmt.Errorf("document 0: %w", first),
			want: []string{"bad a"},
		},
		"joined bindings in order": {
			err: errors.Join(
				fmt.Errorf("document 0: %w", first),
				fmt.Errorf("document 1: %w", second),
			),
			want: []string{"bad a", "bad b"},
		},
		"child bound to another source appears once, under its parent": {
			err:  other,
			want: []string{"outer", "bad a"},
		},
		"children bound to the same source appear below their parent": {
			err:  tree,
			want: []string{"2 violations", "bad a", "missing"},
		},
		"the children of a child follow it": {
			err:  nested,
			want: []string{"2 violations", "bad b", "bad a", "bad c"},
		},
		"a binding the tree reaches twice comes once": {
			err:  errors.Join(first, docs[0].Bind(niceyaml.NewError("outer", niceyaml.WithErrors(first)))),
			want: []string{"bad a", "outer"},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, messages(tc.err))
		})
	}

	t.Run("an unresolved child still appears", func(t *testing.T) {
		t.Parallel()

		var unresolved *niceyaml.SourceError

		for b := range niceyaml.AllBindings(tree) {
			if b.Message() == "missing" {
				unresolved = b
			}
		}

		require.NotNil(t, unresolved)

		_, ok := unresolved.Range()
		assert.False(t, ok)
		require.ErrorIs(t, unresolved.Unresolved(), paths.ErrNotFound)
	})

	t.Run("the binding walk leaves out what the report walk yields", func(t *testing.T) {
		t.Parallel()

		assert.Len(t, slices.Collect(niceyaml.Bindings(tree)), 1)
		assert.Len(t, slices.Collect(niceyaml.AllBindings(tree)), 3)
	})
}

func TestError_Format(t *testing.T) {
	t.Parallel()

	err := niceyaml.NewError("2 violations", niceyaml.WithErrors(
		niceyaml.NewError("bad a", niceyaml.AtPath(paths.Root().Child("a"))),
		niceyaml.NewError("bad b", niceyaml.AtPath(paths.Root().Child("b"))),
	))

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
			want:   "2 violations\n|-- $.a: bad a\n`-- $.b: bad b",
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

		assert.Equal(t, niceyaml.FormatError(err, 2), fmt.Sprintf("%+v", err))
	})

	t.Run("a wrapper prints the message alone", func(t *testing.T) {
		t.Parallel()

		assert.Equal(t, "load: 2 violations", fmt.Errorf("load: %w", err).Error())
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

func TestError_LogValue(t *testing.T) {
	t.Parallel()

	t.Run("logs the tree of an unbound error", func(t *testing.T) {
		t.Parallel()

		err := niceyaml.NewError("2 schema violations", niceyaml.WithErrors(
			niceyaml.NewError("bad x", niceyaml.AtPath(paths.Root().Child("x"))),
			niceyaml.NewError("bad y", niceyaml.AtPath(paths.Root().Child("y"))),
		))

		want := stringtest.JoinLF(
			"2 schema violations",
			"|-- $.x: bad x",
			"`-- $.y: bad y",
		)

		assert.Equal(t, want, err.LogValue().String())
		assert.Equal(t, want, logged(t, err))
	})

	t.Run("an error with nothing nested logs its message", func(t *testing.T) {
		t.Parallel()

		err := niceyaml.NewError("bad x", niceyaml.AtPath(paths.Root().Child("x")))

		assert.Equal(t, "$.x: bad x", logged(t, err))
	})

	t.Run("a join of typed-nil errors logs its message", func(t *testing.T) {
		t.Parallel()

		var nilErr *niceyaml.Error

		err := niceyaml.WrapError(errors.Join(nilErr, nilErr))

		assert.Equal(t, "\n", err.LogValue().String())
		assert.Equal(t, niceyaml.FormatError(err, 2), err.LogValue().String())
	})

	t.Run("a nil error logs nothing", func(t *testing.T) {
		t.Parallel()

		var err *niceyaml.Error

		assert.Empty(t, err.LogValue().String())
	})
}

func TestSourceError_LogValue(t *testing.T) {
	t.Parallel()

	source := niceyaml.NewSourceFromString("x: 1\ny: 2\n", niceyaml.WithName("cfg.yaml"))

	t.Run("logs the tree with positions and no excerpt", func(t *testing.T) {
		t.Parallel()

		err := yamltest.Bind(t, source, niceyaml.NewError("2 schema violations", niceyaml.WithErrors(
			niceyaml.NewError("bad x", niceyaml.AtPath(paths.Root().Child("x"))),
			niceyaml.NewError("bad y", niceyaml.AtPath(paths.Root().Child("y"))),
		)))

		want := stringtest.JoinLF(
			"cfg.yaml: 2 schema violations",
			"|-- 1:4: $.x: bad x",
			"`-- 2:4: $.y: bad y",
		)

		assert.Equal(t, want, logged(t, err))

		var buf bytes.Buffer

		slog.New(slog.NewTextHandler(&buf, nil)).Error("load", slog.Any("err", err))

		assert.Contains(t, buf.String(), `err="cfg.yaml: 2 schema violations\n|-- 1:4: $.x: bad x\n`)
		assert.Contains(t, buf.String(), "`-- 2:4: $.y: bad y\"")
		assert.NotContains(t, buf.String(), "   1 | x: 1")
	})

	t.Run("a wrapper logs its own message", func(t *testing.T) {
		t.Parallel()

		err := yamltest.Bind(t, source, niceyaml.NewError("bad x", niceyaml.AtPath(paths.Root().Child("x"))))
		wrapped := fmt.Errorf("load config: %w", err)

		assert.Equal(t, "load config: cfg.yaml:1:4: $.x: bad x", logged(t, wrapped))
	})

	t.Run("a bound join of typed-nil errors logs its message", func(t *testing.T) {
		t.Parallel()

		var nilErr *niceyaml.Error

		err := source.Bind(errors.Join(nilErr, nilErr))
		require.Error(t, err)

		assert.Equal(t, "cfg.yaml: \n", logged(t, err))
		assert.Equal(t, niceyaml.FormatError(err, 2), logged(t, err))

		var buf bytes.Buffer

		slog.New(slog.NewTextHandler(&buf, nil)).Error("load", slog.Any("err", err))

		assert.Contains(t, buf.String(), `err="cfg.yaml: \n"`)
	})

	t.Run("a nil error logs nothing", func(t *testing.T) {
		t.Parallel()

		var err *niceyaml.SourceError

		assert.Empty(t, err.LogValue().String())
	})
}
