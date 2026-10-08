package niceyaml_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"iter"
	"log/slog"
	"maps"
	"math"
	"slices"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"

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

// wrapError returns the [*niceyaml.Error] that [niceyaml.Invalid]
// creates around err, so a test reads the accessors of the Error. It
// fails the test for an err that Invalid returns nil for.
func wrapError(tb testing.TB, err error, opts ...niceyaml.ErrorOption) *niceyaml.Error {
	tb.Helper()

	wrapped, ok := errors.AsType[*niceyaml.Error](niceyaml.Invalid(err, opts...))
	require.True(tb, ok)

	return wrapped
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
func renderContext(err error, lines int) string {
	return renderWith(err, newXMLPrinter(), lines)
}

// renderWith is [render] with the given printer and number of context
// lines. An error that is not a [*niceyaml.SourceError] formats with %+v.
func renderWith(err error, p *printer.Printer, lines int) string {
	if bound, ok := err.(*niceyaml.SourceError); ok { //nolint:errorlint // Mirrors %+v, which formats the top-level value.
		return p.With(printer.WithContextLines(lines)).PrintError(bound)
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

// errorTexts returns the message of each binding in bound, or nil when
// bound is empty.
func errorTexts(bound []*niceyaml.SourceError) []string {
	var texts []string

	for _, b := range bound {
		texts = append(texts, b.Error())
	}

	return texts
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
		"empty message returns empty string": {
			err:  niceyaml.NewError(""),
			want: "",
		},
		"no path or token returns plain error": {
			err:  niceyaml.NewError("something went wrong"),
			want: "something went wrong",
		},
		"with path and source shows annotated source": {
			err: yamltest.Bind(t, xmlSource(source), niceyaml.NewError(
				"invalid value",
				niceyaml.AtPath(paths.Current().Child("key").Key()),
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

	port := niceyaml.NewError("0 is less than 1", niceyaml.AtPath(paths.Current().Child("port")))
	open := niceyaml.NewError("bad open", niceyaml.AtPath(paths.Current().Child("open")))
	closeErr := niceyaml.NewError("bad close", niceyaml.AtPath(paths.Current().Child("close")))

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
		"an Error with no message": {
			err:        niceyaml.NewError("", niceyaml.AtPath(paths.Current().Child("a"))),
			want:       "",
			wantPlus:   "@.a:",
			wantFormat: "@.a:",
		},
		"a join of located Errors": {
			err:        errors.Join(open, closeErr),
			want:       "bad open\nbad close",
			wantPlus:   "bad open\nbad close",
			wantFormat: "|-- @.open: bad open\n`-- @.close: bad close",
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
			assert.Equal(t, tc.wantFormat, niceyaml.FormatError(tc.err, 0))
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
				niceyaml.AtPath(paths.Current().Child("value")),
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
			err:        niceyaml.NewError("bad value", niceyaml.AtPath(paths.Current().Child("missing").Index(0))),
			want:       "config: $.missing[0]: bad value",
			wantDetail: "no excerpt: resolve $.missing[0]: not found",
		},
		"name stands alone in front of an empty message": {
			err:  niceyaml.NewError(""),
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

	doc, err := source.Document()
	require.NoError(t, err)

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
			assert.Contains(t, niceyaml.FormatError(tc.err, 0), shown)
		})
	}

	// The Source keeps the name as the caller gave it.
	assert.Equal(t, name, source.Name())
}

func TestSourceError_Error_Document(t *testing.T) {
	t.Parallel()

	const input = "a: 1\n---\nb: 2\n---\nkind: Wat\nitems:\n  - x\n"

	kindPath := paths.Current().Child("kind")
	itemsPath := paths.Current().Child("items")

	tcs := map[string]struct {
		bind func(t *testing.T, source *niceyaml.Source, docs []*niceyaml.Node) error
		want string
		// The message of the binding, which carries no document.
		message string
		// The binding resolved a location, so its position stands in front.
		located bool
	}{
		"a plain error names its document": {
			bind: func(_ *testing.T, _ *niceyaml.Source, docs []*niceyaml.Node) error {
				return docs[2].Bind(errors.New("plain"))
			},
			want:    "m.yaml: document 3: plain",
			message: "plain",
		},
		"an Error without a location names its document": {
			bind: func(_ *testing.T, _ *niceyaml.Source, docs []*niceyaml.Node) error {
				return docs[0].Bind(niceyaml.NewError("bad"))
			},
			want:    "m.yaml: document 1: bad",
			message: "bad",
		},
		"a path that does not resolve names its document": {
			bind: func(_ *testing.T, _ *niceyaml.Source, docs []*niceyaml.Node) error {
				return docs[2].Bind(niceyaml.NewError("required", niceyaml.AtPath(itemsPath.Index(7))))
			},
			want:    "m.yaml: document 3: $.items[7]: required",
			message: "required",
		},
		"a position says which document, so no document goes beside it": {
			bind: func(_ *testing.T, _ *niceyaml.Source, docs []*niceyaml.Node) error {
				return docs[2].Bind(niceyaml.NewError("bad", niceyaml.AtPath(kindPath)))
			},
			want:    "m.yaml:5:7: $.kind: bad",
			message: "bad",
			located: true,
		},
		"each listed line without a position names its document": {
			bind: func(_ *testing.T, _ *niceyaml.Source, docs []*niceyaml.Node) error {
				return docs[2].Bind(niceyaml.NewSummary("2 problems",
					niceyaml.NewError("bad", niceyaml.AtPath(kindPath)),
					errors.New("plain"),
				))
			},
			want: stringtest.JoinLF(
				"m.yaml: document 3: 2 problems",
				"m.yaml:5:7: $.kind: bad",
				"m.yaml: document 3: plain",
			),
			message: "2 problems",
		},
		"a scoped Node names its document for an error that gains no location": {
			bind: func(t *testing.T, _ *niceyaml.Source, docs []*niceyaml.Node) error {
				t.Helper()

				return yamltest.At(t, docs[2], itemsPath).DecodeInto(t.Context(), nil)
			},
			want:    "m.yaml: document 3: decode target is not a non-nil pointer: got nil",
			message: "decode target is not a non-nil pointer: got nil",
		},
		"an error of a Node's own operation names its document": {
			bind: func(_ *testing.T, _ *niceyaml.Source, docs []*niceyaml.Node) error {
				_, err := docs[1].At(paths.Current().ChildAll())

				return err //nolint:wrapcheck // The test inspects the error of the call.
			},
			want:    "m.yaml: document 2: resolve $.*: wildcard path matches any number of nodes",
			message: "resolve $.*: wildcard path matches any number of nodes",
		},
		"the source binds an error with no location to no document": {
			bind: func(_ *testing.T, source *niceyaml.Source, _ []*niceyaml.Node) error {
				return source.Bind(errors.New("plain"))
			},
			want:    "m.yaml: plain",
			message: "plain",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			source := niceyaml.NewSourceFromString(input, niceyaml.WithName("m.yaml"))

			docs, err := source.Documents()
			require.NoError(t, err)

			err = tc.bind(t, source, docs)
			require.EqualError(t, err, tc.want)

			var bound *niceyaml.SourceError

			require.ErrorAs(t, err, &bound)
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

	t.Run("a source with no name leads with the document", func(t *testing.T) {
		t.Parallel()

		docs, err := niceyaml.NewSourceFromString(input).Documents()
		require.NoError(t, err)

		require.EqualError(t, docs[1].Bind(errors.New("plain")), "document 2: plain")
		require.EqualError(t,
			docs[2].Bind(niceyaml.NewError("bad", niceyaml.AtPath(kindPath))),
			"5:7: $.kind: bad",
		)
	})

	t.Run("a wrapper around a binding keeps the document the binding wrote", func(t *testing.T) {
		t.Parallel()

		docs, err := niceyaml.NewSourceFromString(input, niceyaml.WithName("m.yaml")).Documents()
		require.NoError(t, err)

		wrapped := fmt.Errorf("check: %w", docs[1].Bind(errors.New("plain")))
		require.EqualError(t, wrapped, "check: m.yaml: document 2: plain")

		// The binding names its document already, so binding the wrapper
		// through another document adds none.
		require.EqualError(t, docs[0].Bind(wrapped), "check: m.yaml: document 2: plain")
	})

	t.Run("a join of one binding per document names each", func(t *testing.T) {
		t.Parallel()

		docs, err := niceyaml.NewSourceFromString(input, niceyaml.WithName("m.yaml")).Documents()
		require.NoError(t, err)

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
		), niceyaml.FormatError(joined, 2))
	})

	t.Run("a row of the tree names a document its parent does not", func(t *testing.T) {
		t.Parallel()

		docs, err := niceyaml.NewSourceFromString(input, niceyaml.WithName("m.yaml")).Documents()
		require.NoError(t, err)

		// The first nested error is bound to the second document, and the
		// other binds with its parent to the first.
		err = docs[0].Bind(niceyaml.NewSummary("summary",
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
		), niceyaml.FormatError(err, 2))
	})
}

func TestSourceError_Error_JoinLead(t *testing.T) {
	t.Parallel()

	source := niceyaml.NewSourceFromString("a: 1\n", niceyaml.WithName("f.yaml"))
	bound := yamltest.Bind(t, source, niceyaml.NewError(
		"bad a",
		niceyaml.AtPath(paths.Current().Child("a")),
	))

	// A bound join lists its branches, each behind the name of its own
	// source, so the line of a binding stays as it is and a plain branch
	// takes the name. A branch with no message adds no line, whether it
	// is an empty message or a nil pointer.
	tcs := map[string]struct {
		// The branches in front of the binding.
		lead []error
		want string
	}{
		"binding leads": {
			want: "f.yaml:1:4: $.a: bad a\nf.yaml: plain",
		},
		"empty message leads": {
			lead: []error{errors.New("")},
			want: "f.yaml:1:4: $.a: bad a\nf.yaml: plain",
		},
		"nil Error leads": {
			lead: []error{(*niceyaml.Error)(nil)},
			want: "f.yaml:1:4: $.a: bad a\nf.yaml: plain",
		},
		"nil SourceError leads": {
			lead: []error{(*niceyaml.SourceError)(nil)},
			want: "f.yaml:1:4: $.a: bad a\nf.yaml: plain",
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
		niceyaml.AtPath(paths.Current().Child("a")),
	))
	boundB := yamltest.Bind(t, source, niceyaml.NewError(
		"bad b",
		niceyaml.AtPath(paths.Current().Child("b")),
	))

	// A wrapper with several %w verbs leads with the binding it keeps, as
	// a wrapper with one %w verb does, so its first line names the source
	// already. A multi-error of its own type leads with its first branch
	// when its message starts with the message of that branch, as a list
	// does. A count of the branches starts with no binding, so the name
	// goes in front whichever branch holds the binding. The message of a
	// list holds its branches, so nothing follows it, and a count holds
	// none, so each branch follows on a line of its own.
	tcs := map[string]struct {
		err  error
		want string
	}{
		"wrapper with one verb": {
			err:  errors.Join(fmt.Errorf("ctx: %w", boundA), plain),
			want: "ctx: f.yaml:1:4: $.a: bad a\nf.yaml: plain",
		},
		"sentinel wrapper": {
			err:  errors.Join(fmt.Errorf("%w: %w", errSentinel, boundA), plain),
			want: "sentinel: f.yaml:1:4: $.a: bad a\nf.yaml: plain",
		},
		"wrapper of two bindings": {
			err:  errors.Join(fmt.Errorf("%w and %w", boundA, boundB), plain),
			want: "f.yaml:1:4: $.a: bad a and f.yaml:2:4: $.b: bad b\nf.yaml: plain",
		},
		"multi-error with the binding first": {
			err:  violationsError{boundA, plain},
			want: "f.yaml: 2 violations\nf.yaml:1:4: $.a: bad a\nf.yaml: plain",
		},
		"multi-error with the binding last": {
			err:  violationsError{plain, boundA},
			want: "f.yaml: 2 violations\nf.yaml:1:4: $.a: bad a\nf.yaml: plain",
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

func TestSourceError_Error_MultiErrorBranches(t *testing.T) {
	t.Parallel()

	source := niceyaml.NewSourceFromString("a: 1\nb: 2\n", niceyaml.WithName("f.yaml"))
	pathA := paths.Current().Child("a")
	pathB := paths.Current().Child("b")

	// A multi-error of its own type keeps the message it wrote. That
	// message holds the text of a branch at most and never the location a
	// binding gives it, so each branch the binding locates follows on a
	// line of its own, whatever the message holds. A branch with no
	// location follows only when the message does not hold the first
	// branch.
	tcs := map[string]struct {
		err  error
		want string
	}{
		"a list of located errors": {
			err: listError{
				niceyaml.NewError("bad a", niceyaml.AtPath(pathA)),
				niceyaml.NewError("bad b", niceyaml.AtPath(pathB)),
			},
			want: stringtest.JoinLF(
				"f.yaml: bad a; bad b",
				"f.yaml:1:4: $.a: bad a",
				"f.yaml:2:4: $.b: bad b",
			),
		},
		"a heading that holds a short message by chance": {
			err: headedError{msg: "invalid config: 2 problems", errs: []error{
				niceyaml.NewError("invalid", niceyaml.AtPath(pathA)),
				niceyaml.NewError("too old", niceyaml.AtPath(pathB)),
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
				niceyaml.NewError("bad a", niceyaml.AtPath(pathA)),
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
				niceyaml.NewError("bad a", niceyaml.AtPath(pathA)),
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

func TestSourceError_Error_List(t *testing.T) {
	t.Parallel()

	source := niceyaml.NewSourceFromString("a: 1\nb: 2\nc: 3\n", niceyaml.WithName("f.yaml"))
	badA := niceyaml.NewError("bad a", niceyaml.AtPath(paths.Current().Child("a")))
	badB := niceyaml.NewError("bad b", niceyaml.AtPath(paths.Current().Child("b")))
	badC := niceyaml.NewError("bad c", niceyaml.AtPath(paths.Current().Child("c")))
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
				upperError{err: yamltest.Bind(t, source, summary)},
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

func TestSourceError_Error_ListLimit(t *testing.T) {
	t.Parallel()

	var input strings.Builder

	for i := range 2 * niceyaml.ErrorListLimit {
		fmt.Fprintf(&input, "k%d: 0\n", i)
	}

	// Located returns n errors, one per key of the input from the key at
	// first on.
	located := func(first, n int) []error {
		errs := make([]error, 0, n)
		for i := first; i < first+n; i++ {
			key := fmt.Sprintf("k%d", i)
			errs = append(errs, niceyaml.NewError("bad "+key, niceyaml.AtPath(paths.Current().Child(key))))
		}

		return errs
	}

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

			source := niceyaml.NewSourceFromString(input.String(), niceyaml.WithName(tc.name))
			err := yamltest.Bind(t, source, tc.err)

			require.Error(t, err)
			assert.Equal(t, strings.Join(tc.want, "\n"), err.Error())

			// The tree holds every error, whatever the message leaves out.
			assert.Len(t, slices.Collect(niceyaml.NewErrorTree(err).Problems()), tc.problems)
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
					niceyaml.AtPath(paths.Current().Child("name").Key()),
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

	path := paths.Current().Child("foo")
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
			err: wrapError(t,
				fmt.Errorf("context: %w", niceyaml.NewError("test", niceyaml.AtPath(path))),
			),
			want: located{path: path},
		},
		"position on a wrapped error": {
			err: wrapError(t,
				fmt.Errorf("context: %w", niceyaml.NewError("test", niceyaml.AtPosition(pos))),
			),
			want: located{pos: pos},
		},
		"range on a wrapped error": {
			err: wrapError(t,
				fmt.Errorf("context: %w", niceyaml.NewError("test", niceyaml.AtRange(rng))),
			),
			want: located{rng: rng},
		},
		"a path and a range on a wrapped error": {
			err: wrapError(t,
				fmt.Errorf("context: %w", niceyaml.NewError("test", niceyaml.AtPath(path), niceyaml.AtRange(rng))),
			),
			want: located{path: path, rng: rng},
		},
		"own location wins over a wrapped one": {
			err: wrapError(t,
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
				niceyaml.AtPath(paths.Current().Child("nonexistent").Key()),
			)),
			want: "$.nonexistent~: not found\n\nno excerpt: resolve $.nonexistent~: not found",
		},
		"path without source": {
			err: niceyaml.NewError(
				"missing source",
				niceyaml.AtPath(paths.Current().Child("key").Key()),
			),
			want: "@.key~: missing source",
		},
		// No path resolves in a document with no content, so the reason
		// would add nothing to the path the message names.
		"empty source": {
			err: yamltest.Bind(t, niceyaml.NewSourceFromTokens(emptyTokens), niceyaml.NewError(
				"error in empty source",
				niceyaml.AtPath(paths.Current().Child("key").Key()),
			)),
			want: "$.key~: error in empty source",
		},
		"nonexistent path in source": {
			err: yamltest.Bind(t, niceyaml.NewSourceFromString(source), niceyaml.NewError(
				"path not found",
				niceyaml.AtPath(
					paths.Current().Child("nonexistent").Child("deep").Key(),
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
			loc:    niceyaml.AtPath(paths.Current().Child("foo", "bar").Key()),
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
			loc:    niceyaml.AtPath(paths.Current().Child("items").Index(0).Key()),
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
			loc:    niceyaml.AtPath(paths.Current().Child("users").Index(0).Child("name").Key()),
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
			loc:    niceyaml.AtPath(paths.Current().Key()),
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
			loc:    niceyaml.AtPath(paths.Current().Child("key").Key()),
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
			loc:          niceyaml.AtPath(paths.Current().Child("line3").Key()),
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

			lines := 2
			if tc.contextLines > 0 {
				lines = tc.contextLines
			}

			err := yamltest.Bind(t, xmlSource(tc.source),
				niceyaml.NewError(tc.errMsg, tc.loc),
			)

			assert.Equal(t, tc.want, trimLines(renderContext(err, lines)))
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
			loc:    niceyaml.AtPath(paths.Current().Child("key")),
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
			loc:    niceyaml.AtPath(paths.Current().Child("foo", "bar")),
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
			loc:    niceyaml.AtPath(paths.Current().Child("items").Index(0)),
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
			loc:    niceyaml.AtPath(paths.Current().Index(1).Key()),
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
			loc:    niceyaml.AtPath(paths.Current().Key()),
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
		outer := niceyaml.Invalid(fmt.Errorf("check: %w", nilErr))

		var bound *niceyaml.SourceError

		assert.NotErrorAs(t, outer, &bound)
	})

	t.Run("unwraps underlying error", func(t *testing.T) {
		t.Parallel()

		underlying := errors.New("underlying error")
		err := wrapError(t, underlying)

		got := err.Unwrap()

		require.Len(t, got, 1)
		assert.Equal(t, underlying, got[0])
	})

	t.Run("an empty message unwraps to its cause", func(t *testing.T) {
		t.Parallel()

		err := niceyaml.NewError("")

		got := err.Unwrap()

		require.Len(t, got, 1)
		assert.Equal(t, err.Cause(), got[0])
		require.EqualError(t, got[0], "")
	})

	t.Run("the zero Error unwraps to nil", func(t *testing.T) {
		t.Parallel()

		var err niceyaml.Error

		assert.Nil(t, err.Unwrap())
	})

	t.Run("errors.Is works through Error wrapper", func(t *testing.T) {
		t.Parallel()

		sentinel := errors.New("sentinel error")
		err := niceyaml.Invalid(sentinel)

		require.ErrorIs(t, err, sentinel)
	})

	t.Run("unwraps nested errors", func(t *testing.T) {
		t.Parallel()

		underlying := errors.New("main error")
		nested1 := errors.New("nested error 1")
		nested2 := errors.New("nested error 2")

		err := wrapError(t, underlying,
			niceyaml.WithDetails(
				niceyaml.Invalid(nested1),
				niceyaml.Invalid(nested2),
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

		err := wrapError(t, underlying,
			niceyaml.WithDetails(
				nil,
				niceyaml.Invalid(nested),
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
			niceyaml.AtPath(paths.Current().Child("name").Key()),
			niceyaml.WithDetails(
				niceyaml.NewError(
					"invalid type",
					niceyaml.AtPath(paths.Current().Child("value")),
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
			niceyaml.AtPath(paths.Current().Child("name").Key()),
			niceyaml.WithDetails(
				niceyaml.NewError(
					"invalid type",
					niceyaml.AtPath(paths.Current().Child("value")),
				),
				niceyaml.NewError(
					"missing field",
					niceyaml.AtPath(paths.Current().Child("other").Key()),
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
			niceyaml.WithDetails(
				niceyaml.NewError(
					"error1",
					niceyaml.AtPath(paths.Current().Child("key").Key()),
				),
				niceyaml.NewError(
					"error2",
					niceyaml.AtPath(paths.Current().Child("key")),
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
		err := yamltest.Bind(t, xmlSource(source), niceyaml.NewSummary(
			"2 problems",
			niceyaml.NewError(
				"bad beta",
				niceyaml.AtPath(paths.Current().Child("key").Index(1)),
			),
			niceyaml.NewError(
				"bad alpha",
				niceyaml.AtPath(paths.Current().Child("key").Index(0)),
			),
		))

		got := trimLines(render(err))

		// The joined messages follow the column order of the carets.
		assert.Contains(t, got, "^ bad alpha; bad beta")
	})

	t.Run("nested errors that repeat a message", func(t *testing.T) {
		t.Parallel()

		source := stringtest.Input(`
			key: [alpha, beta]
		`)

		bad := func(message string, index int) error {
			return niceyaml.NewError(message, niceyaml.AtPath(paths.Current().Child("key").Index(index)))
		}

		err := yamltest.Bind(t, xmlSource(source), niceyaml.NewSummary(
			"4 problems", bad("too long", 0), bad("too long", 1), bad("too long", 0), bad("no digit", 0),
		))

		got := trimLines(render(err))

		// The message reads once at the column it repeats at, and again
		// at the other column.
		assert.Contains(t, got, "^ too long; no digit; too long<")
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
			niceyaml.WithDetails(
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
			niceyaml.AtPath(paths.Current().Child("key").Key()),
			niceyaml.WithDetails(
				niceyaml.NewError(
					"nested error",
					niceyaml.AtPath(paths.Current().Child("nonexistent").Key()),
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

		var summary *niceyaml.Error

		require.ErrorAs(t, niceyaml.NewSummary("main error", nested1, nested2), &summary)

		detailed := niceyaml.NewError("main error", niceyaml.WithDetails(nested1, nested2))

		// The errors below are structure, not text. Errors returns the
		// errors a summary heads and Details the details of a problem,
		// and both surface through Unwrap, while the message stays one
		// line and %+v lists them as a tree under it.
		for _, err := range []*niceyaml.Error{summary, detailed} {
			assert.Equal(t, "main error", err.Error())
			assert.Equal(t, "main error\n|-- nested 1\n`-- nested 2", render(err))
			require.ErrorIs(t, err, nested1)
			require.ErrorIs(t, err, nested2)
		}

		assert.Equal(t, []error{nested1, nested2}, summary.Errors())
		assert.Empty(t, summary.Details())
		assert.Equal(t, []error{nested1, nested2}, detailed.Details())
		assert.Empty(t, detailed.Errors())
	})

	t.Run("nested error without path or token stays in the message", func(t *testing.T) {
		t.Parallel()

		source := stringtest.Input(`
			key: value
		`)

		err := yamltest.Bind(t, xmlSource(source), niceyaml.NewError(
			"main error",
			niceyaml.AtPath(paths.Current().Child("key").Key()),
			niceyaml.WithDetails(
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

		err := niceyaml.NewSummary(
			"main error",
			niceyaml.Invalid(sentinel1),
			niceyaml.Invalid(sentinel2),
		)

		require.ErrorIs(t, err, sentinel1)
		require.ErrorIs(t, err, sentinel2)
	})

	t.Run("errors.As works with nested errors", func(t *testing.T) {
		t.Parallel()

		customErr := &customTestError{msg: "custom error"}

		err := niceyaml.NewError(
			"main",
			niceyaml.WithDetails(
				niceyaml.Invalid(customErr),
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
			niceyaml.WithDetails(
				niceyaml.NewError(
					"got number, want string",
					niceyaml.AtPath(paths.Current().Child("value")),
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
			niceyaml.WithDetails(
				niceyaml.NewError(
					"nested error",
					niceyaml.AtPath(paths.Current().Child("value")),
				),
			),
		)

		// Without a source, the tree renders with no excerpt, and the
		// nested error waits for a binding to put its position in front.
		assert.Equal(t, "validation failed", err.Error())
		assert.Equal(t, "validation failed\n`-- @.value: nested error", render(err))
	})

	t.Run("nested-only error with multiple lines", func(t *testing.T) {
		t.Parallel()

		source := stringtest.Input(`
			name: test
			value: 123
			other: data
		`)

		// The nested errors sit on different lines.
		err := yamltest.Bind(t, xmlSource(source), niceyaml.NewSummary(
			"validation failed at 2 locations",
			niceyaml.NewError(
				"type error on value",
				niceyaml.AtPath(paths.Current().Child("value")),
			),
			niceyaml.NewError(
				"unexpected property",
				niceyaml.AtPath(paths.Current().Child("other").Key()),
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
		err := yamltest.Bind(t, xmlSource(source), niceyaml.NewSummary(
			"validation failed",
			niceyaml.NewError(
				"resolvable error",
				niceyaml.AtPath(paths.Current().Child("value")),
			),
			niceyaml.NewError(
				"unresolvable error",
				niceyaml.AtPath(paths.Current().Child("nonexistent").Key()),
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
			niceyaml.WithDetails(
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
			err: yamltest.Bind(t, niceyaml.NewSourceFromString(source), niceyaml.NewSummary(
				"main error",
				niceyaml.NewError("nested 1"),
				niceyaml.NewError("nested 2"),
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
				niceyaml.WithDetails(
					niceyaml.NewError(
						"nested with path",
						niceyaml.AtPath(paths.Current().Child("key").Key()),
					),
				),
			)),
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
			err: func() error {
				tokens := lexer.Tokenize(source)

				return yamltest.Bind(t, xmlSource(source), niceyaml.NewError(
					"main error",
					niceyaml.WithDetails(
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
				"<textError>^^^ nested with token</textError>",
				"<nameTag>other</nameTag><punctuationMappingValue>:</punctuationMappingValue><text> </text><literalString>data</literalString>",
			),
		},
		"mix of located and unlocated nested errors": {
			err: yamltest.Bind(t, xmlSource(source), niceyaml.NewSummary(
				"main error",
				niceyaml.NewError("no path"),
				niceyaml.NewError(
					"has path",
					niceyaml.AtPath(paths.Current().Child("key").Key()),
				),
				niceyaml.NewError("also no path"),
			)),
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
			err: yamltest.Bind(t, niceyaml.NewSourceFromString(source), niceyaml.NewError(
				"main error", niceyaml.WithDetails(nil, nil),
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
	err := yamltest.Bind(t, source, niceyaml.NewSummary(
		"2 schema violations",
		niceyaml.NewError("bad x", niceyaml.AtPath(paths.Current().Child("x").Index(0))),
		niceyaml.NewError("bad y", niceyaml.AtPath(paths.Current().Child("y").Index(0))),
	))

	// The message and the %+v verb list each nested error behind its
	// path, and since no location resolves, the printer adds nothing
	// beyond the connectors of the tree.
	assert.Equal(t, "2 schema violations\n$.x[0]: bad x\n$.y[0]: bad y", err.Error())
	assert.Equal(t, "2 schema violations\n|-- $.x[0]: bad x\n`-- $.y[0]: bad y", report(err))
	assert.Equal(t, "2 schema violations\n├── $.x[0]: bad x\n└── $.y[0]: bad y", render(err))

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
	err := yamltest.Bind(t, source, niceyaml.NewError("x", niceyaml.AtPath(paths.Current().Child("a"))))

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

		err := yamltest.Bind(t, source, niceyaml.NewError("bad value", niceyaml.AtPath(paths.Current().Child("b"))))

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
		namePath := paths.Current().Child("名前")

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

		err := yamltest.Bind(t, source, niceyaml.NewSummary("2 problems",
			niceyaml.NewError("bad a", niceyaml.AtPath(paths.Current().Child("a").Key())),
			niceyaml.NewError("bad c", niceyaml.AtPath(paths.Current().Child("c"))),
		))

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
		err := yamltest.Bind(
			t,
			src,
			niceyaml.NewError("bad value", niceyaml.AtPath(paths.Current().Child("b").Index(0))),
		)

		assert.Equal(t,
			"$.b[0]: bad value\n\nno excerpt: resolve $.b[0]: not found",
			fmt.Sprintf("%+v", err),
		)
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

		got := niceyaml.FormatError(err, 0)
		assert.NotContains(t, got, "\x1b")
		assert.Contains(t, got, "m\u241b[31mX")
		assert.Contains(t, got, `no excerpt: resolve $.'k\u001b[31mY'`)
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
				opt:   niceyaml.AtPath(paths.Current().Child("a")),
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
				opt:   niceyaml.AtPath(paths.Current().Child("a")),
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
				opt:   niceyaml.AtPath(paths.Current().Child("a")),
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
				opt:   niceyaml.AtPath(paths.Current().Index(0)),
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

	// A nil *Error carries no message and specializes to nothing, in line
	// with Cause, Errors, Unwrap, and Path, which all accept nil.
	var missing *niceyaml.Error

	assert.Empty(t, missing.Error())
	assert.Nil(t, missing.With(niceyaml.AtPath(paths.Current().Child("a"))))
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

	index, bound := missing.DocumentIndex()
	assert.Zero(t, index)
	assert.False(t, bound)
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

			bound := docs[1].Bind(niceyaml.NewError("required property 'a' missing", niceyaml.AtPath(paths.Current())))

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

	source := xmlSource("a: 1\nb: 2\n")
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
				niceyaml.AtRange(position.NewRange(position.New(1, 3), position.New(1, 4))),
			),
			wantTree:  "",
			wantBound: "2:4:",
		},
		"path": {
			err:       niceyaml.NewError("", niceyaml.AtPath(paths.Current().Child("b"))),
			wantTree:  "@.b:",
			wantBound: "2:4: $.b:",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Empty(t, tc.err.Error())
			assert.Equal(t, tc.wantTree, niceyaml.FormatError(tc.err, 0))

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
	inner := niceyaml.NewError("bad a", niceyaml.AtPath(paths.Current().Child("a")))
	nested := niceyaml.NewError("bad b", niceyaml.AtPath(paths.Current().Child("b")))

	// Nested errors on the wrapper become its children and add annotations,
	// and the wrapper still takes its position from the Error it wraps.
	wrapped := yamltest.Bind(t, source, niceyaml.Invalid(inner, niceyaml.WithDetails(nested)))
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
	inner := niceyaml.NewError("bad a", niceyaml.AtPath(paths.Current().Child("a")))
	nested := niceyaml.NewError("", niceyaml.AtPath(paths.Current().Child("b")))

	// A nested Error with an empty message names a location and nothing
	// else, so the printer highlights that location and marks it with a
	// caret run alone, with no message beside it.
	wrapped := yamltest.Bind(t, source, niceyaml.Invalid(inner, niceyaml.WithDetails(nested)))

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
			niceyaml.WithDetails(
				niceyaml.NewError(
					"error at line3",
					niceyaml.AtPath(paths.Current().Child("line3").Key()),
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
			niceyaml.WithDetails(
				niceyaml.NewError(
					"error at line1",
					niceyaml.AtPath(paths.Current().Child("line1").Key()),
				),
				niceyaml.NewError(
					"error at line6",
					niceyaml.AtPath(paths.Current().Child("line6").Key()),
				),
			),
		))

		assert.Equal(t, stringtest.JoinLF(
			"validation error",
			"├── 1:1: $.line1~: error at line1",
			"└── 6:1: $.line6~: error at line6",
			"",
			"<genericError>line1</genericError><punctuationMappingValue>:</punctuationMappingValue><text> </text><literalString>a</literalString>",
			"<textError>^^^^^ error at line1</textError>",
			"<uiSeparator>...</uiSeparator>",
			"<genericError>line6</genericError><punctuationMappingValue>:</punctuationMappingValue><text> </text><literalString>f</literalString>",
			"<textError>^^^^^ error at line6</textError>",
		), trimLines(renderContext(err, 0)))
	})

	t.Run("nested errors on one line share an annotation", func(t *testing.T) {
		t.Parallel()

		source := stringtest.Input(`
			line1: a
			line2: b
			line3: c
		`)

		err := yamltest.Bind(t, xmlSource(source), niceyaml.NewSummary(
			"validation error",
			niceyaml.NewError(
				"error 1",
				niceyaml.AtPath(paths.Current().Child("line2").Key()),
			),
			niceyaml.NewError(
				"error 2",
				niceyaml.AtPath(paths.Current().Child("line2")),
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
		err := yamltest.Bind(t, xmlSource(source), niceyaml.NewSummary(
			"validation error",
			niceyaml.NewError(
				"error at start",
				niceyaml.AtPath(paths.Current().Child("line1").Key()),
			),
			niceyaml.NewError(
				"error at end",
				niceyaml.AtPath(paths.Current().Child("line10").Key()),
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
			niceyaml.AtPath(paths.Current().Child("line4")),
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
			niceyaml.AtPath(paths.Current().Child("line1").Key()),
			niceyaml.WithDetails(
				niceyaml.NewError(
					"error at end",
					niceyaml.AtPath(paths.Current().Child("line6").Key()),
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
		err := yamltest.Bind(t, xmlSource(source), niceyaml.NewSummary(
			"validation error",
			niceyaml.NewError(
				"first error",
				niceyaml.AtPath(paths.Current().Child("line1").Key()),
			),
			niceyaml.NewError(
				"second error",
				niceyaml.AtPath(paths.Current().Child("line3").Key()),
			),
		))

		// The hunk runs from line1 through line4 with no separator.
		assert.Equal(t, stringtest.JoinLF(
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
		err := yamltest.Bind(t, xmlSource(source), niceyaml.NewSummary(
			"validation failed at 2 locations",
			niceyaml.NewError(
				"first location error",
				niceyaml.AtPath(paths.Current().Child("line1").Key()),
			),
			niceyaml.NewError(
				"second location error",
				niceyaml.AtPath(paths.Current().Child("line10").Key()),
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
		err := yamltest.Bind(t, xmlSource(source), niceyaml.NewSummary(
			"validation error",
			niceyaml.NewError(
				"error at first",
				niceyaml.AtPath(paths.Current().Child("first").Key()),
			),
			niceyaml.NewError(
				"error at last",
				niceyaml.AtPath(paths.Current().Child("last").Key()),
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
			"<textError>^^^^^ error at first</textError>",
			"<nameTag>middle1</nameTag><punctuationMappingValue>:</punctuationMappingValue><text> </text><literalString>a</literalString>",
			"<uiSeparator>...</uiSeparator>",
			"<nameTag>middle5</nameTag><punctuationMappingValue>:</punctuationMappingValue><text> </text><literalString>e</literalString>",
			"<genericError>last</genericError><punctuationMappingValue>:</punctuationMappingValue><text> </text><literalString>value</literalString>",
			"<textError>^^^^ error at last</textError>",
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
			niceyaml.AtPath(paths.Current().Child("line1").Key()),
			niceyaml.WithDetails(
				niceyaml.NewError(
					"nested error",
					niceyaml.AtPath(paths.Current().Child("line10").Key()),
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
		err := yamltest.Bind(t, xmlSource(source), niceyaml.NewSummary(
			"validation error",
			niceyaml.NewError(
				"error at line1",
				niceyaml.AtPath(paths.Current().Child("line1").Key()),
			),
			niceyaml.NewError(
				"error at line2",
				niceyaml.AtPath(paths.Current().Child("line2").Key()),
			),
		))

		// The hunk holds line1 and line2 with no separator between them.
		assert.Equal(t, stringtest.JoinLF(
			"validation error",
			"├── 1:1: $.line1~: error at line1",
			"└── 2:1: $.line2~: error at line2",
			"",
			"<genericError>line1</genericError><punctuationMappingValue>:</punctuationMappingValue><text> </text><literalString>a</literalString>",
			"<textError>^^^^^ error at line1</textError>",
			"<genericError>line2</genericError><punctuationMappingValue>:</punctuationMappingValue><text> </text><literalString>b</literalString>",
			"<textError>^^^^^ error at line2</textError>",
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
				"^^^  ^^^^^ this is a very long error",
				"           message that should",
				"           definitely wrap when the",
				"           width is limited",
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
				"^^^  ^^^^^ this is a very long error message that should not wrap",
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
				"^^^  ^^^^^ short error",
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
				niceyaml.AtPath(paths.Current().Child("key").Key()),
				niceyaml.WithDetails(
					niceyaml.NewError(
						tc.nestedErrMsg,
						niceyaml.AtPath(paths.Current().Child("key")),
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

	err := yamltest.Bind(t, niceyaml.NewSourceFromString(source), niceyaml.NewSummary(
		"validation failed at 2 locations",
		niceyaml.NewError(
			"first error with a very long message that should wrap properly",
			niceyaml.AtPath(paths.Current().Child("value")),
		),
		niceyaml.NewError(
			"second error also with a long message for testing wrap behavior",
			niceyaml.AtPath(paths.Current().Child("other").Key()),
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
		"       ^^^ first error with a very long message",
		"           that should wrap properly",
		"other: data",
		"^^^^^ second error also with a long message for",
		"      testing wrap behavior",
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
		niceyaml.AtPath(paths.Current().Child("key").Key()),
		niceyaml.WithDetails(
			niceyaml.NewError(
				"first error message here",
				niceyaml.AtPath(paths.Current().Child("key").Key()),
			),
			niceyaml.NewError(
				"second error message here",
				niceyaml.AtPath(paths.Current().Child("key")),
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
		"^^^  ^^^^^ first error message here;",
		"           second error message here",
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

	node, err := paths.Current().Child("b").Node(file.Docs[0])
	require.NoError(t, err)

	literal, ok := node.(*ast.LiteralNode)
	require.True(t, ok, "want *ast.LiteralNode, got %T", node)

	tk := literal.Value.GetToken()

	got := trimLines(render(yamltest.Bind(t, source, niceyaml.NewError(
		"bad block",
		niceyaml.AtPosition(position.NewFromToken(tk)),
		niceyaml.WithDetails(
			niceyaml.NewError("bad c", niceyaml.AtPath(paths.Current().Child("c"))),
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
	namePath := paths.Current().Child("name")

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
			niceyaml.WithDetails(
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
		niceyaml.WithDetails(
			niceyaml.NewError("nested", niceyaml.AtPath(paths.Current().Child("b"))),
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
	located := base.With(niceyaml.AtPath(paths.Current().Child("key").Key()))

	// The copy carries the option and the receiver keeps its own.
	_, ok := base.Path()
	assert.False(t, ok)

	locatedPath, ok := located.Path()
	require.True(t, ok)
	assert.Equal(t, paths.Current().Child("key").Key(), locatedPath)

	// The path stays out of the message, and FormatError names it.
	assert.Equal(t, "bad key", base.Error())
	assert.Equal(t, "bad key", located.Error())
	assert.Equal(t, "@.key~: bad key", niceyaml.FormatError(located, 0))

	// The details of the copy are its own too.
	reason := errors.New("reason")
	detailed := located.With(niceyaml.WithDetails(reason))
	more := detailed.With(niceyaml.WithDetails(errors.New("more")))

	assert.Empty(t, located.Details())
	assert.Equal(t, []error{reason}, detailed.Details())
	assert.Len(t, more.Details(), 2)
}

func TestNewSummary(t *testing.T) {
	t.Parallel()

	source := niceyaml.NewSourceFromString("a: 1\nb: 2\n", niceyaml.WithName("f.yaml"))
	badA := niceyaml.NewError("bad a", niceyaml.AtPath(paths.Current().Child("a")))
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

		summary, ok := errors.AsType[*niceyaml.Error](err)
		require.True(t, ok)
		assert.Equal(t, []error{badA, tooOld}, summary.Errors())
		assert.Empty(t, summary.Details())

		_, ok = summary.Path()
		assert.False(t, ok)

		// The summary is no problem of its own, and it lists each of the
		// problems below its line.
		bound := yamltest.Bind(t, source, err)
		require.EqualError(t, bound, "f.yaml: 2 problems\nf.yaml:1:4: $.a: bad a\nf.yaml: too old")
		assert.Equal(t, []string{"bad a", "too old"}, problemRows(niceyaml.NewErrorTree(bound)))
		assert.Equal(t, []string{"@.a: bad a", "too old"}, problemRows(niceyaml.NewErrorTree(err)))
	})
}

func TestWithDetails(t *testing.T) {
	t.Parallel()

	source := niceyaml.NewSourceFromString("spec:\n  a: 80\n  b: 80\n", niceyaml.WithName("cfg.yaml"))
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
		), niceyaml.FormatError(err, 0))

		problems := slices.Collect(niceyaml.NewErrorTree(err).Problems())
		require.Len(t, problems, 1)
		require.Len(t, problems[0].Children, 1)
		assert.True(t, problems[0].Children[0].Detail)

		var bound *niceyaml.SourceError

		require.ErrorAs(t, err, &bound)
		assert.Empty(t, bound.Errors())
		assert.Equal(t, []string{"cfg.yaml:2:6: $.spec.a: first declared here"}, errorTexts(bound.Details()))
	})

	t.Run("a detail bound to another file keeps its binding", func(t *testing.T) {
		t.Parallel()

		other := niceyaml.NewSourceFromString("base:\n  port: 80\n", niceyaml.WithName("base.yaml"))
		declared := yamltest.Bind(t, other, niceyaml.NewError(
			"first declared here",
			niceyaml.AtPath(paths.Current().Child("base", "port")),
		))

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
		), niceyaml.FormatError(err, 0))

		problems := slices.Collect(niceyaml.NewErrorTree(err).Problems())
		require.Len(t, problems, 1)
		require.Len(t, problems[0].Children, 1)
		assert.Same(t, other, problems[0].Children[0].Bound.Source())
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
			problems := slices.Collect(niceyaml.NewErrorTree(err).Problems())
			require.Len(t, problems, 1)
			require.Len(t, problems[0].Children, 2)

			for _, detail := range problems[0].Children {
				assert.True(t, detail.Detail, detail.Text)
			}
		}
	})
}

func TestError_WrappedContext(t *testing.T) {
	t.Parallel()

	source := niceyaml.NewSourceFromString("name: first\n---\nname: second\n")

	docs, err := source.Documents()
	require.NoError(t, err)
	require.Len(t, docs, 2)

	inner := niceyaml.NewError("bad name", niceyaml.AtPath(paths.Current().Child("name")))

	wrapped := docs[1].Bind(fmt.Errorf("document 1: %w", inner))

	// The outer context stays as the wrapper wrote it, and the position the
	// path resolves to and the path go in front of the whole message.
	assert.Equal(t, "3:7: $.name: document 1: bad name", wrapped.Error())
	assert.Equal(t, "3:7: $.name: document 1: bad name", fmt.Sprintf("%v", wrapped))
	require.ErrorIs(t, wrapped, inner)

	// Binding first puts the context in front of the position instead.
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
	located := niceyaml.NewError("bad name", niceyaml.AtPath(paths.Current().Child("name")))
	wrapped := docs[1].Bind(niceyaml.Invalid(fmt.Errorf("validate: %w", located)))

	// The producer's context stays as written, behind the resolved position
	// and the path.
	assert.Equal(t, "3:7: $.name: validate: bad name", wrapped.Error())

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
	badA := niceyaml.NewError("bad a", niceyaml.AtPath(paths.Current().Child("a")))
	badB := niceyaml.NewError("bad b", niceyaml.AtPath(paths.Current().Child("b")))
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
			niceyaml.AtPath(paths.Current().Child("a")),
			niceyaml.WithDetails(
				niceyaml.Invalid(fmt.Errorf("ctx: %w",
					niceyaml.NewError("bad", niceyaml.AtPath(paths.Current().Child("b"))),
				)),
			),
		))

		var bound *niceyaml.SourceError

		require.ErrorAs(t, err, &bound)

		excerpt, ok := bound.Excerpt(2)
		require.True(t, ok)

		got := trimLines(newXMLPrinter().Print(excerpt))
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
		), trimLines(render(err)))
	})

	t.Run("annotation drops the position the caret marks", func(t *testing.T) {
		t.Parallel()

		source := xmlSource("a: 1\nb: 2\n")
		tk := source.Lines().TokenAt(position.New(1, 0))
		require.NotNil(t, tk)

		err := yamltest.Bind(t, source, niceyaml.NewError(
			"outer",
			niceyaml.AtPath(paths.Current().Child("a")),
			niceyaml.WithDetails(
				niceyaml.Invalid(niceyaml.NewError("inner", niceyaml.AtPosition(position.NewFromToken(tk)))),
				niceyaml.Invalid(niceyaml.NewError("also", niceyaml.AtPath(paths.Current().Child("b")))),
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
	err := yamltest.Bind(t, source, niceyaml.NewSummary(
		"validation failed",
		niceyaml.NewError(
			"bad a\n  see docs for details",
			niceyaml.AtPath(paths.Current().Child("a")),
		),
		niceyaml.NewError("bad b", niceyaml.AtPath(paths.Current().Child("b"))),
	))

	// The message lists the nested message under the headline whole,
	// continuation line included. So does the %+v verb, the tree indents
	// the continuation line under the branch, and the annotation carries
	// it as well.
	assert.Equal(t, "validation failed\n1:4: $.a: bad a\n  see docs for details\n2:4: $.b: bad b", err.Error())
	assert.Equal(t,
		"validation failed\n|-- 1:4: $.a: bad a\n|     see docs for details\n`-- 2:4: $.b: bad b",
		report(err),
	)

	got := trimLines(render(err))
	message, detail, _ := strings.Cut(got, "\n\n")

	assert.Equal(t,
		"validation failed\n├── 1:4: $.a: bad a\n│     see docs for details\n└── 2:4: $.b: bad b",
		message,
	)
	assert.Contains(t, detail, "see docs for details")
}

func TestSourceError_NestedPositionsBehindWrappers(t *testing.T) {
	t.Parallel()

	source := niceyaml.NewSourceFromString("a: 1\nb: 2\n", niceyaml.WithName("f.yaml"))
	inner := niceyaml.NewSummary("2 problems",
		niceyaml.NewError("bad a", niceyaml.AtPath(paths.Current().Child("a"))),
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
			err:     inner,
			want:    "f.yaml: 2 problems\n|-- 1:4: $.a: bad a\n`-- $.missing[0]: bad b",
			wantMsg: "f.yaml: 2 problems\nf.yaml:1:4: $.a: bad a\nf.yaml: $.missing[0]: bad b",
		},
		"context in front keeps the nested lines at the end": {
			err:     fmt.Errorf("document 0: %w", inner),
			want:    "f.yaml: document 0: 2 problems\n|-- 1:4: $.a: bad a\n`-- $.missing[0]: bad b",
			wantMsg: "f.yaml: document 0: 2 problems\nf.yaml:1:4: $.a: bad a\nf.yaml: $.missing[0]: bad b",
		},
		"details inside details": {
			err: niceyaml.NewError("outer", niceyaml.WithDetails(
				niceyaml.NewError("middle", niceyaml.AtPath(paths.Current().Child("b")), niceyaml.WithDetails(
					niceyaml.NewError("leaf", niceyaml.AtPath(paths.Current().Child("a"))),
				)),
			)),
			want:    "f.yaml: outer\n`-- 2:4: $.b: middle\n    `-- 1:4: $.a: leaf",
			wantMsg: "f.yaml: outer",
		},
		"a wrapper that rewrites the message keeps the nested lines": {
			err:     yamltest.RewriteError{Err: inner},
			want:    "f.yaml: rewritten\n|-- 1:4: $.a: bad a\n`-- $.missing[0]: bad b",
			wantMsg: "f.yaml: rewritten\nf.yaml:1:4: $.a: bad a\nf.yaml: $.missing[0]: bad b",
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

	source := niceyaml.NewSourceFromString("name: first\n---\nname: second\n")
	namePath := paths.Current().Child("name")

	docs, err := source.Documents()
	require.NoError(t, err)
	require.Len(t, docs, 2)

	t.Run("nested wrappers keep their text behind the position", func(t *testing.T) {
		t.Parallel()

		inner := niceyaml.NewError("bad name", niceyaml.AtPath(namePath))
		wrapped := docs[0].Bind(fmt.Errorf("outer: %w", fmt.Errorf("inner: %w", inner)))

		assert.Equal(t, "1:7: $.name: outer: inner: bad name", wrapped.Error())
	})

	t.Run("a bound join binds each branch as a child", func(t *testing.T) {
		t.Parallel()

		first := niceyaml.NewError("bad first", niceyaml.AtPath(namePath))
		second := niceyaml.NewError("bad second", niceyaml.AtPath(namePath))
		wrapped := docs[0].Bind(errors.Join(
			fmt.Errorf("a: %w", first),
			fmt.Errorf("b: %w", second),
		))

		// The join carries no location of its own, so the message is its
		// branches, and each branch is a child with its position and its
		// path in front of the text its wrapper wrote.
		assert.Equal(t, "1:7: $.name: a: bad first\n1:7: $.name: b: bad second", wrapped.Error())
		require.ErrorIs(t, wrapped, first)
		require.ErrorIs(t, wrapped, second)

		var bound *niceyaml.SourceError

		require.ErrorAs(t, wrapped, &bound)
		require.Len(t, bound.Errors(), 2)
		assert.Equal(t, "1:7: $.name: a: bad first", bound.Errors()[0].Error())
		assert.Equal(t, "1:7: $.name: b: bad second", bound.Errors()[1].Error())
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

	t.Run("a wrapper that blanks the message still gets the position", func(t *testing.T) {
		t.Parallel()

		inner := niceyaml.NewError("bad name", niceyaml.AtPath(namePath))
		wrapped := docs[0].Bind(blankError{err: inner})

		// The binding puts the position and the path in front of the
		// empty text, so the line still says where the problem is.
		assert.Equal(t, "1:7: $.name:", wrapped.Error())
		require.ErrorIs(t, wrapped, inner)
	})

	t.Run("a wrapper that rewrites the message still gets the position", func(t *testing.T) {
		t.Parallel()

		inner := niceyaml.NewError("bad name", niceyaml.AtPath(namePath))
		wrapped := docs[0].Bind(fmt.Errorf("outer: %w", yamltest.RewriteError{Err: inner}))

		// The position and the path come from the Error in the chain, not
		// from its text, so a wrapper that hides the text hides neither.
		assert.Equal(t, "1:7: $.name: outer: rewritten", wrapped.Error())
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

	t.Run("an anchor with details takes the position of the error it wraps", func(t *testing.T) {
		t.Parallel()

		tk := source.Lines().TokenAt(position.New(2, 6))
		nested := niceyaml.NewError("bad name", niceyaml.AtPath(namePath))

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
						niceyaml.AtRange(position.NewRange(position.New(2, 6), position.New(2, 12))),
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
				assert.Equal(t, tc.bound+"\n`-- 1:7: $.name: bad name", report(docs[0].Bind(tc.err)))
			})
		}
	})

	t.Run("a path anchor resolves its path rather than the error it wraps", func(t *testing.T) {
		t.Parallel()

		tk := source.Lines().TokenAt(position.New(2, 6))
		inner := niceyaml.NewError("bad token", niceyaml.AtPosition(position.NewFromToken(tk)))
		outer := niceyaml.Invalid(inner, niceyaml.AtPath(namePath))

		// The path anchor resolves in the first document, and the message
		// carries that one position, which agrees with the highlight.
		assert.Equal(t, "bad token", outer.Error())
		assert.Equal(t, "1:7: $.name: bad token", docs[0].Bind(outer).Error())
	})

	t.Run("a path anchor replaces the path of the error it wraps", func(t *testing.T) {
		t.Parallel()

		inner := niceyaml.NewError("bad token", niceyaml.AtPath(paths.Current().Child("other")))
		outer := wrapError(t, inner, niceyaml.AtPath(namePath))

		// The tree and the binding name the one location Path reports, not
		// both.
		p, ok := outer.Path()
		require.True(t, ok)
		assert.Equal(t, namePath, p)
		assert.Equal(t, "bad token", outer.Error())
		assert.Equal(t, "@.name: bad token", niceyaml.FormatError(outer, 0))
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
	located := niceyaml.NewError("bad name", niceyaml.AtPath(paths.Current().Child("name")))

	docs, err := source.Documents()
	require.NoError(t, err)
	require.Len(t, docs, 2)

	// Error wrappers add no message text of their own, so the document
	// attached above them still resolves the location in the message.
	wrapped := docs[0].Bind(niceyaml.Invalid(located))

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
			err:  niceyaml.NewError("bad", niceyaml.AtPath(paths.Current().Child("value"))),
			want: position.NewRange(position.New(1, 7), position.New(1, 10)),
		},
		"path targets the key token": {
			err:  niceyaml.NewError("bad", niceyaml.AtPath(paths.Current().Child("value").Key())),
			want: position.NewRange(position.New(1, 0), position.New(1, 5)),
		},
		"path resolves in the bound document": {
			err:  niceyaml.NewError("bad", niceyaml.AtPath(paths.Current().Child("name"))),
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
			err: niceyaml.NewError("bad", niceyaml.AtPath(paths.Current().Child("missing").Index(0))),
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
			path:  paths.Current().Child("a"),
			want:  position.New(0, 2),
		},
		"comment after the colon": {
			input: "a: # c\nb: 1\n",
			path:  paths.Current().Child("a"),
			want:  position.New(0, 2),
		},
		"spaces after the colon": {
			input: "a:   \nb: 1\n",
			path:  paths.Current().Child("a"),
			want:  position.New(0, 2),
		},
		"comment after the dash": {
			input: "- # c\n- 1\n",
			path:  paths.Current().Index(0),
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
		niceyaml.AtPath(paths.Current().Child("text")),
	)), &bound)

	got, ok := bound.Range()
	require.True(t, ok)

	// The plain scalar spans two lines, so the range ends on the second.
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

func TestSourceError_Position(t *testing.T) {
	t.Parallel()

	source := xmlSource("name: hello\nvalue: 123\n")

	bind := func(t *testing.T, err error) *niceyaml.SourceError {
		t.Helper()

		var bound *niceyaml.SourceError

		require.ErrorAs(t, yamltest.Bind(t, source, err), &bound)

		return bound
	}

	tcs := map[string]struct {
		build func(t *testing.T) *niceyaml.SourceError
		// The start of the range the error resolves to, which differs from
		// the position only for a position inside a token.
		start position.Position
		want  position.Position
		ok    bool
	}{
		"path": {
			build: func(t *testing.T) *niceyaml.SourceError {
				t.Helper()

				return bind(t, niceyaml.NewError("bad", niceyaml.AtPath(paths.Current().Child("value"))))
			},
			want:  position.New(1, 7),
			start: position.New(1, 7),
			ok:    true,
		},
		"range": {
			build: func(t *testing.T) *niceyaml.SourceError {
				t.Helper()

				return bind(t, niceyaml.NewError("bad", niceyaml.AtRange(
					position.NewRange(position.New(0, 6), position.New(0, 9)),
				)))
			},
			want:  position.New(0, 6),
			start: position.New(0, 6),
			ok:    true,
		},
		"position at the start of a token": {
			build: func(t *testing.T) *niceyaml.SourceError {
				t.Helper()

				return bind(t, niceyaml.NewError("bad", niceyaml.AtPosition(position.New(1, 7))))
			},
			want:  position.New(1, 7),
			start: position.New(1, 7),
			ok:    true,
		},
		"position inside a token": {
			build: func(t *testing.T) *niceyaml.SourceError {
				t.Helper()

				return bind(t, niceyaml.NewError("bad", niceyaml.AtPosition(position.New(0, 8))))
			},
			want:  position.New(0, 8),
			start: position.New(0, 6),
			ok:    true,
		},
		"binding around a binding": {
			build: func(t *testing.T) *niceyaml.SourceError {
				t.Helper()

				inner := bind(t, niceyaml.NewError("bad", niceyaml.AtPosition(position.New(0, 8))))

				return bind(t, niceyaml.Invalid(inner, niceyaml.WithDetails(errors.New("see docs"))))
			},
			want:  position.New(0, 8),
			start: position.New(0, 6),
			ok:    true,
		},
		"no location": {
			build: func(t *testing.T) *niceyaml.SourceError {
				t.Helper()

				return bind(t, niceyaml.NewError("bad"))
			},
		},
		"position past the last line": {
			build: func(t *testing.T) *niceyaml.SourceError {
				t.Helper()

				return bind(t, niceyaml.NewError("bad", niceyaml.AtPosition(position.New(99, 0))))
			},
		},
		"nil": {
			build: func(*testing.T) *niceyaml.SourceError { return nil },
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			bound := tc.build(t)

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

	source := xmlSource("name: test\nvalue: 123\n")

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
			wantRender: "$.missing[0]: bad\n\nno excerpt: resolve $.missing[0]: not found",
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
				niceyaml.AtPath(paths.Current().Child("missing").Index(0)),
				niceyaml.WithDetails(
					niceyaml.NewError("first",
						niceyaml.AtRange(position.NewRange(position.New(-1, 0), position.New(-1, 2))),
					),
				),
			),
			is:         paths.ErrNotFound,
			wantRender: "$.missing[0]: bad\n└── first\n\nno excerpt: resolve $.missing[0]: not found",
		},
		"every nested error unresolved": {
			err: niceyaml.NewSummary("bad",
				niceyaml.NewError("first", niceyaml.AtPath(paths.Current().Child("missing").Index(0))),
				niceyaml.NewError("second", niceyaml.AtPosition(position.New(9, 0))),
			),
			wantRender: "bad\n├── $.missing[0]: first\n└── second",
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

		require.ErrorAs(t, yamltest.Bind(t, source, niceyaml.NewSummary("bad",
			niceyaml.NewError("first", niceyaml.AtPath(paths.Current().Child("missing").Index(0))),
			niceyaml.NewError("second", niceyaml.AtPath(paths.Current().Child("value"))),
		)), &bound)

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
			niceyaml.AtPath(paths.Current().Child("missing").Index(0)),
			niceyaml.WithDetails(niceyaml.NewError("first", niceyaml.AtPath(paths.Current().Child("name")))),
		)), &bound)

		requireUnresolved(t, bound, paths.ErrNotFound)

		_, ok := bound.Excerpt(0)
		require.True(t, ok)

		// The child resolves, so the excerpt marks it, and the root keeps
		// its message in the tree without a position or a reason.
		assert.Equal(t,
			"$.missing[0]: bad\n`-- 1:7: $.name: first\n\n   1 | name: test\n     |       ^^^^ first",
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
		niceyaml.AtPath(paths.Current().Child("b")),
		niceyaml.WithDetails(
			niceyaml.NewError("bad h", niceyaml.AtPath(paths.Current().Child("h"))),
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

	t.Run("a line several errors mark carries one annotation per message", func(t *testing.T) {
		t.Parallel()

		source := niceyaml.NewSourceFromString("key: value\n")
		value := paths.Current().Child("key")

		var bound *niceyaml.SourceError

		// The second and third problems say the same of one value, as two
		// branches of an anyOf do.
		require.ErrorAs(t, yamltest.Bind(t, source, niceyaml.NewSummary("3 problems",
			niceyaml.NewError("about value", niceyaml.AtPath(value)),
			niceyaml.NewError("about value", niceyaml.AtPath(value)),
			niceyaml.NewError("about key", niceyaml.AtPosition(position.New(0, 0))),
		)), &bound)

		excerpt, ok := bound.Excerpt(0)
		require.True(t, ok)

		assert.Equal(t, line.Annotations{
			{Content: "about value", Kind: kind.TextError, Placement: line.Below, Col: 5},
			{Content: "about key", Kind: kind.TextError, Placement: line.Below, Col: 0},
		}, excerpt.Annotations(0))
		assert.Equal(t, line.Overlays{
			{Kind: kind.GenericError, Cols: position.NewSpan(5, 10)},
			{Kind: kind.GenericError, Cols: position.NewSpan(0, 3)},
		}, excerpt.Overlays(0))

		// A renderer joins the messages in the order of their columns.
		assert.Equal(t, stringtest.JoinLF(
			"   1 | key: value",
			"     | ^^^  ^^^^^ about key; about value",
		), excerpt.String())
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
				err: niceyaml.NewError("top", niceyaml.WithDetails(
					niceyaml.NewError("far", niceyaml.AtPosition(far)),
				)),
				want:    line.Annotations{{Content: "far", Kind: kind.TextError, Placement: line.Below, Col: 4}},
				row:     "     |     ^ far",
				printed: "<textError>    ^ far</textError>",
			},
			"nested range": {
				err: niceyaml.NewError("top", niceyaml.WithDetails(
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

	t.Run("marks the error and every binding below it with their messages", func(t *testing.T) {
		t.Parallel()

		bound := excerptError(t)
		view := bound.Source().View()

		require.True(t, bound.Annotate(view))

		assert.Equal(t, []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}, lineNumbers(view))

		want := line.Overlays{{Kind: kind.GenericError, Cols: position.NewSpan(3, 4)}}
		assert.Equal(t, want, view.Overlays(1))
		assert.Equal(t, want, view.Overlays(7))
		assert.Equal(t, line.Annotations{
			{Content: "bad b", Kind: kind.TextError, Placement: line.Below, Col: 3},
		}, view.Annotations(1))
		assert.Equal(t, line.Annotations{
			{Content: "bad h", Kind: kind.TextError, Placement: line.Below, Col: 3},
		}, view.Annotations(7))

		for i := range view.All() {
			if i == 1 || i == 7 {
				continue
			}

			assert.Empty(t, view.Overlays(i), "line %d carries no overlay", i)
			assert.Empty(t, view.Annotations(i), "line %d carries no annotation", i)
		}

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
			niceyaml.AtPath(paths.Current().Child("a")),
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
		require.True(t, niceyaml.Annotate(err, view))

		assert.Contains(t, view.String(), "^^^  ^^^^^ about key; about value")
		assert.Contains(t, niceyaml.FormatError(err, 0), "^^^  ^^^^^ about key; about value")
	})

	t.Run("a binding the tree reaches twice marks its line once", func(t *testing.T) {
		t.Parallel()

		source := niceyaml.NewSourceFromString("a: 1\nb: 2\n")
		badA := yamltest.Bind(t, source, niceyaml.NewError("bad a", niceyaml.AtPath(paths.Current().Child("a"))))
		parent := yamltest.Bind(t, source, niceyaml.NewError("parent", niceyaml.WithDetails(badA)))
		err := yamltest.Bind(t, source, errors.Join(parent, badA))

		got := niceyaml.FormatError(err, 1)
		assert.Contains(t, got, "^ bad a")
		assert.NotContains(t, got, "bad a; bad a")

		// The excerpt marks the view as Annotate marks it.
		view := source.View()
		require.True(t, niceyaml.Annotate(err, view))

		var bound *niceyaml.SourceError

		require.ErrorAs(t, err, &bound)

		excerpt, ok := bound.Excerpt(0)
		require.True(t, ok)
		assert.Equal(t, view.Annotations(0), excerpt.Annotations(0))

		// A walk of the tree meets the binding once per node that holds it,
		// and the line keeps the mark of the first.
		walked := source.View()
		for node := range niceyaml.NewErrorTree(err).All() {
			node.Bound.Annotate(walked)
		}

		assert.Equal(t, view.Annotations(0), walked.Annotations(0))
		assert.Equal(t, view.Overlays(0), walked.Overlays(0))
	})

	t.Run("a binding reached along many paths marks in linear time", func(t *testing.T) {
		t.Parallel()

		// Every binding nests the one before it twice, so a walk that
		// visits a binding once per path takes 2^40 steps.
		source := niceyaml.NewSourceFromString("a: 1\n")
		err := yamltest.Bind(t, source, niceyaml.NewError("bad a", niceyaml.AtPath(paths.Current().Child("a"))))

		for range 40 {
			err = yamltest.Bind(t, source, niceyaml.NewSummary("summary", err, err))
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
			"bad b", niceyaml.AtPath(paths.Current().Child("b")),
		)), &first)
		require.ErrorAs(t, yamltest.Bind(t, source, niceyaml.NewError(
			"bad d",
			niceyaml.WithDetails(niceyaml.NewError("too big", niceyaml.AtPath(paths.Current().Child("d")))),
		)), &second)

		require.True(t, first.Annotate(view))
		require.True(t, second.Annotate(view), "the detail marks its line for an error with no location of its own")

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
				msg, niceyaml.AtPath(paths.Current().Child("b")),
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

	t.Run("a message that repeats at one column reads once", func(t *testing.T) {
		t.Parallel()

		source := niceyaml.NewSourceFromString(excerptSource)
		view := source.View()

		// Two bindings say the same of one value, as two branches of an
		// anyOf do.
		for range 2 {
			var bound *niceyaml.SourceError

			require.ErrorAs(t, yamltest.Bind(t, source, niceyaml.NewError(
				"bad b", niceyaml.AtPath(paths.Current().Child("b")),
			)), &bound)
			require.True(t, bound.Annotate(view), "the view holds the line, marked or not")
		}

		assert.Equal(t, line.Overlays{{Kind: kind.GenericError, Cols: position.NewSpan(3, 4)}}, view.Overlays(1))
		assert.Equal(t, line.Annotations{
			{Content: "bad b", Kind: kind.TextError, Placement: line.Below, Col: 3},
		}, view.Annotations(1))
	})

	t.Run("marking a view twice leaves it as marking it once does", func(t *testing.T) {
		t.Parallel()

		b := paths.Current().Child("b")

		tcs := map[string]struct {
			err error
			// The lines of excerptSource the error marks, by index.
			want []int
		}{
			"error at a path": {
				err:  niceyaml.NewError("bad b", niceyaml.AtPath(b)),
				want: []int{1},
			},
			"error with no message": {
				err:  niceyaml.NewError("", niceyaml.AtPath(b)),
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
					niceyaml.NewError("bad h", niceyaml.AtPath(paths.Current().Child("h"))),
				)),
				want: []int{1, 7},
			},
			"two problems on one line": {
				err: niceyaml.NewSummary("2 problems",
					niceyaml.NewError("bad key", niceyaml.AtPosition(position.New(1, 0))),
					niceyaml.NewError("bad value", niceyaml.AtPath(b)),
				),
				want: []int{1},
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				source := niceyaml.NewSourceFromString(excerptSource)
				err := yamltest.Bind(t, source, tc.err)

				once, twice := source.View(), source.View()

				for bound := range niceyaml.AllBindings(err) {
					bound.Annotate(once)
				}

				for range 2 {
					for bound := range niceyaml.AllBindings(err) {
						bound.Annotate(twice)
					}
				}

				var marked []int

				for i := range once.Hunks(0).All() {
					marked = append(marked, i)
				}

				assert.Equal(t, tc.want, marked)

				for i := range once.All() {
					assert.Equal(t, once.Overlays(i), twice.Overlays(i), "overlays of line %d", i)
					assert.Equal(t, once.Annotations(i), twice.Annotations(i), "annotations of line %d", i)
				}

				assert.Equal(t, once.String(), twice.String())
			})
		}
	})

	t.Run("an error with no message marks a caret run alone", func(t *testing.T) {
		t.Parallel()

		source := niceyaml.NewSourceFromString(excerptSource)
		view := source.View()

		var bound *niceyaml.SourceError

		require.ErrorAs(t, yamltest.Bind(t, source, niceyaml.NewError(
			"", niceyaml.AtPath(paths.Current().Child("b")),
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

		require.True(t, bound.Annotate(view), "the slice holds the line of the detail")

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
			"bad b", niceyaml.AtPath(paths.Current().Child("b")),
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
					"bad "+tc.key, niceyaml.AtPath(paths.Current().Child(tc.key)),
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
				err: niceyaml.NewError("bad", niceyaml.AtPath(paths.Current().Child("missing").Index(0))),
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

	// Assert that got carries the overlays and annotations of want.
	sameMarks := func(t *testing.T, want, got *line.View) {
		t.Helper()

		for i := range want.All() {
			assert.Equal(t, want.Overlays(i), got.Overlays(i), "overlays of line %d", i)
			assert.Equal(t, want.Annotations(i), got.Annotations(i), "annotations of line %d", i)
		}
	}

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
			"loop over AllBindings": func(err error, view *line.View) bool {
				marked := false

				for bound := range niceyaml.AllBindings(err) {
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
				build: decode("name: x\na: many\nb: 2\n"),
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
				build: decode("name: x\na: 0\nb: -1\n"),
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

					source := niceyaml.NewSourceFromString("a: 1\nb: 2\nc: 3\n")

					return source, yamltest.Bind(t, source, niceyaml.NewSummary("2 problems",
						niceyaml.NewError("a conflicts",
							niceyaml.AtPath(paths.Current().Child("a")),
							niceyaml.WithDetails(
								niceyaml.NewError("with b", niceyaml.AtPath(paths.Current().Child("b"))),
							),
						),
						niceyaml.NewError("bad c", niceyaml.AtPath(paths.Current().Child("c"))),
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
					sameMarks(t, want, view)
				})
			}
		}
	})

	t.Run("a loop that skips a binding still marks it below one it keeps", func(t *testing.T) {
		t.Parallel()

		source := niceyaml.NewSourceFromString("a: 1\nb: 2\n")
		err := yamltest.Bind(t, source, niceyaml.NewError("a conflicts",
			niceyaml.AtPath(paths.Current().Child("a")),
			niceyaml.WithDetails(niceyaml.NewError("with b", niceyaml.AtPath(paths.Current().Child("b")))),
		))

		view := source.View()

		for bound := range niceyaml.AllBindings(err) {
			if len(bound.Details()) > 0 {
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
		badA := yamltest.Bind(t, source, niceyaml.NewError("bad a", niceyaml.AtPath(paths.Current().Child("a"))))
		badB := yamltest.Bind(t, source, niceyaml.NewError("bad b", niceyaml.AtPath(paths.Current().Child("b"))))

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
			yamltest.Bind(t, manifest, niceyaml.NewError("bad a", niceyaml.AtPath(paths.Current().Child("a")))),
			yamltest.Bind(t, values, niceyaml.NewError("bad b", niceyaml.AtPath(paths.Current().Child("b")))),
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
		located := yamltest.Bind(t, source, niceyaml.NewError("bad a", niceyaml.AtPath(paths.Current().Child("a"))))

		tcs := map[string]struct {
			err  error
			view *line.View
		}{
			"nil error": {
				view: source.View(),
			},
			"error bound to no source": {
				err:  niceyaml.NewError("bad a", niceyaml.AtPath(paths.Current().Child("a"))),
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

				for i := range tc.view.Lines().All() {
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

	source := xmlSource("a: 1\nb: 2\nc: 3\n")
	badA := niceyaml.NewError("bad a", niceyaml.AtPath(paths.Current().Child("a")))
	badB := niceyaml.NewError("bad b", niceyaml.AtPath(paths.Current().Child("b")))
	badC := niceyaml.NewError("bad c", niceyaml.AtPath(paths.Current().Child("c")))

	t.Run("join branches bind as children", func(t *testing.T) {
		t.Parallel()

		err := yamltest.Bind(t, source, errors.Join(badA, badB))

		// The join has no location of its own, each branch is a child
		// with one, and the excerpt marks both with their messages. The
		// message is the branches behind their positions, and the %+v
		// verb leads with them, since the join says nothing they do not.
		assert.Equal(t, "1:4: $.a: bad a\n2:4: $.b: bad b", err.Error())
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

	t.Run("a join in a named source names the source of each branch", func(t *testing.T) {
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

		// A binding of another source names that source alone, and a
		// branch the join binds itself names the source of the join. A
		// branch that joins bindings of other sources holds no text of its
		// own, so it adds no name.
		err = yamltest.Bind(t, named("c.yaml"), errors.Join(yamltest.Bind(t, named("a.yaml"), badA), badB))
		assert.Equal(t, "a.yaml:1:4: $.a: bad a\nc.yaml:2:4: $.b: bad b", err.Error())

		err = yamltest.Bind(t, named("c.yaml"), errors.Join(
			errors.Join(yamltest.Bind(t, named("a.yaml"), badA), yamltest.Bind(t, named("b.yaml"), badB)),
			yamltest.Bind(t, named("b.yaml"), badB),
		))
		assert.Equal(t, "a.yaml:1:4: $.a: bad a\nb.yaml:2:4: $.b: bad b\nb.yaml:2:4: $.b: bad b", err.Error())

		// So does an Error that only wraps a binding of such a join.
		joinSrc := named("c.yaml")
		err = yamltest.Bind(t, joinSrc, errors.Join(
			yamltest.Bind(t, named("a.yaml"), badA),
			niceyaml.Invalid(yamltest.Bind(t, joinSrc, errors.Join(
				yamltest.Bind(t, named("b.yaml"), badB),
			))),
		))
		assert.Equal(t, "a.yaml:1:4: $.a: bad a\nb.yaml:2:4: $.b: bad b", err.Error())

		// A binding of such a join to this source has no line of its own,
		// so its branches stand in its place. A join that leads with that
		// binding, or a summary that heads it, lists them beside the child
		// that belongs to this source, with the sources in the order they
		// first appear.
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
				want: "a.yaml:1:4: $.a: bad a\nb.yaml:2:4: $.b: bad b\nf.yaml:3:4: $.c: bad c",
			},
			"summary that heads it": {
				err:  niceyaml.NewSummary("2 checks", joinOfOthers, badC),
				want: "f.yaml: 2 checks\na.yaml:1:4: $.a: bad a\nb.yaml:2:4: $.b: bad b\nf.yaml:3:4: $.c: bad c",
			},
		}

		for name, tc := range leads {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				err := yamltest.Bind(t, src, tc.err)
				assert.Equal(t, tc.want, err.Error())
			})
		}

		// A binding of a source with no name has no name to put in front
		// of its branches, so a branch of that source carries its position
		// alone, and one bound to this source keeps the name it carries.
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
				want: "f.yaml:1:4: $.a: bad a\nf.yaml:3:4: $.c: bad c\n2:4: $.b: bad b",
			},
			"summary that heads it": {
				err: errors.Join(
					yamltest.Bind(t, unnamed, niceyaml.NewSummary("2 checks",
						yamltest.Bind(t, unnamed, errors.Join(ownLead)),
						unnamedB,
					)),
					badC,
				),
				want: "f.yaml:3:4: $.c: bad c\n2 checks\nf.yaml:1:4: $.a: bad a\n2:4: $.b: bad b",
			},
		}

		for name, tc := range unnamedLeads {
			t.Run("unnamed "+name, func(t *testing.T) {
				t.Parallel()

				err := yamltest.Bind(t, src, tc.err)
				assert.Equal(t, tc.want, err.Error())
			})
		}

		// Every line takes the name, so a join names the source in front
		// of each branch it binds, a branch nested in another join
		// included, and the lines read in the order of their positions.
		err = yamltest.Bind(t, src, errors.Join(yamltest.Bind(t, src, badA), badB))
		assert.Equal(t, "f.yaml:1:4: $.a: bad a\nf.yaml:2:4: $.b: bad b", err.Error())

		err = yamltest.Bind(t, src, errors.Join(
			errors.Join(yamltest.Bind(t, src, badA), badB),
			badC,
		))
		assert.Equal(t, "f.yaml:1:4: $.a: bad a\nf.yaml:2:4: $.b: bad b\nf.yaml:3:4: $.c: bad c", err.Error())

		err = yamltest.Bind(t, src, errors.Join(badB, yamltest.Bind(t, src, badA)))
		assert.Equal(t, "f.yaml:1:4: $.a: bad a\nf.yaml:2:4: $.b: bad b", err.Error())

		// An Error with details above a binding writes the text of the
		// binding as it is, and the details stay out of the line. An Error
		// that carries a position alone binds anew, so its line carries its
		// own position in front of the one the binding wrote.
		tcs := map[string]struct {
			lead error
			want string
		}{
			"details": {
				lead: niceyaml.Invalid(yamltest.Bind(t, src, badA), niceyaml.WithDetails(badB)),
				want: "f.yaml:1:4: $.a: bad a\nf.yaml:3:4: $.c: bad c",
			},
			"position alone": {
				lead: niceyaml.Invalid(yamltest.Bind(t, src, badA), niceyaml.AtPosition(position.New(0, 3))),
				want: "f.yaml:1:4: f.yaml:1:4: $.a: bad a\nf.yaml:3:4: $.c: bad c",
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				err := yamltest.Bind(t, src, errors.Join(tc.lead, badC))
				assert.Equal(t, tc.want, err.Error())
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
				want: "1:4: $.a: bad a\n2:4: $.b: bad b",
			},
			"two %w verbs": {
				err:  fmt.Errorf("%w; %w", badA, badB),
				want: "bad a; bad b\n1:4: $.a: bad a\n2:4: $.b: bad b",
			},
			"wrapped join": {
				err:  fmt.Errorf("ctx: %w", errors.Join(badA, badB)),
				want: "ctx:\n1:4: $.a: bad a\n2:4: $.b: bad b",
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				err := yamltest.Bind(t, source, tc.err)

				var bound *niceyaml.SourceError

				require.ErrorAs(t, err, &bound)
				assert.Equal(t, tc.want, bound.Error())

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

				var bound *niceyaml.SourceError

				require.ErrorAs(t, err, &bound)

				rng, ok := bound.Range()
				require.True(t, ok)
				assert.Equal(t, 1, rng.Start.Line)
				assert.Empty(t, bound.Errors())
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

		got := trimLines(render(err))
		assert.Contains(t, got, "<genericError>1</genericError>")
		assert.Contains(t, got, "^ first: bad a")
		assert.Contains(t, got, "^ bad b")
		assert.Contains(t, got, "^ bad c")
	})

	t.Run("a join branch that does not resolve is listed without a position", func(t *testing.T) {
		t.Parallel()

		missing := niceyaml.NewError("bad x", niceyaml.AtPath(paths.Current().Child("x").Index(0)))
		err := yamltest.Bind(t, source, errors.Join(badA, missing))

		got := trimLines(render(err))
		assert.Equal(t, "├── 1:4: $.a: bad a\n└── $.x[0]: bad x", strings.SplitN(got, "\n\n", 2)[0])
		assert.Equal(t, 1, strings.Count(got, "$.x[0]: bad x"))
	})

	t.Run("a bound error inside the tree keeps its own binding", func(t *testing.T) {
		t.Parallel()

		other := xmlSource("z: 9\n")
		inner := yamltest.Bind(t, other, niceyaml.NewError("bad z", niceyaml.AtPath(paths.Current().Child("z"))))
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

	first := docs[0].Bind(niceyaml.NewError("bad a", niceyaml.AtPath(paths.Current().Child("a"))))
	second := docs[1].Bind(niceyaml.NewError("bad b", niceyaml.AtPath(paths.Current().Child("b"))))
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

	port := yamltest.Bind(t, values, niceyaml.NewError("not a number", niceyaml.AtPath(paths.Current().Child("port"))))
	host := yamltest.Bind(t, values, niceyaml.NewError("not a name", niceyaml.AtPath(paths.Current().Child("host"))))
	err := yamltest.Bind(t, manifest, niceyaml.NewError(
		"bad values",
		niceyaml.AtPath(paths.Current().Child("values")),
		niceyaml.WithDetails(port, host),
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
		require.True(t, bound.Annotate(view), "the details are bound to the source of the view")

		assert.Equal(t, stringtest.JoinLF(
			"   1 | port: many",
			"     |       ^^^^ not a number",
			"   2 | host: 7",
			"     |       ^ not a name",
		), view.String())

		// The view of the binding's own source takes the mark of the
		// binding and none of the details.
		own := bound.Source().View()
		require.True(t, bound.Annotate(own))
		assert.Equal(t, []int{2}, lineNumbers(own.Hunks(0)))
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
					niceyaml.AtPath(paths.Current().Child(spec.key)),
				)))
			}
		}

		var mixed *niceyaml.SourceError

		require.ErrorAs(t, yamltest.Bind(t, srcs[1], niceyaml.NewError(
			"root",
			niceyaml.AtPath(paths.Current().Child("b")),
			niceyaml.WithDetails(children...),
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

		require.ErrorAs(t, yamltest.Bind(t, values, niceyaml.NewError("bad", niceyaml.WithDetails(
			niceyaml.NewError("not a number", niceyaml.AtPath(paths.Current().Child("port"))),
		))), &one)

		assert.Len(t, maps.Collect(one.Excerpts(0)), 1)
	})

	t.Run("nothing resolved yields nothing", func(t *testing.T) {
		t.Parallel()

		var none *niceyaml.SourceError

		require.ErrorAs(
			t,
			yamltest.Bind(
				t,
				values,
				niceyaml.NewError("bad", niceyaml.AtPath(paths.Current().Child("missing").Index(0))),
			),
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

	badA := yamltest.Bind(t, first, niceyaml.NewError("bad a", niceyaml.AtPath(paths.Current().Child("a"))))
	badB := yamltest.Bind(t, first, niceyaml.NewError("bad b", niceyaml.AtPath(paths.Current().Child("b"))))
	badC := yamltest.Bind(t, second, niceyaml.NewError("bad c", niceyaml.AtPath(paths.Current().Child("c"))))
	gone := yamltest.Bind(t, second, niceyaml.NewError("gone", niceyaml.AtPath(paths.Current().Child("x").Index(0))))

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
				yamltest.Bind(t, first, niceyaml.NewError("parent", niceyaml.WithDetails(badA))),
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
			niceyaml.AtPath(paths.Current().Child("a")),
			niceyaml.WithDetails(badC),
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
	nested := niceyaml.NewError("nested", niceyaml.AtPath(paths.Current().Child("a")))

	var nilNested *niceyaml.Error

	err := wrapError(t, cause, niceyaml.WithDetails(nil, nested, nilNested))

	assert.Equal(t, cause, err.Cause())
	assert.Equal(t, []error{nested}, err.Details())
	assert.Empty(t, err.Errors())
	assert.Equal(t, []error{cause, nested}, err.Unwrap())

	// The slice of details is a copy.
	err.Details()[0] = nil
	assert.Equal(t, []error{nested}, err.Details())

	other := niceyaml.NewError("other")

	summary, ok := errors.AsType[*niceyaml.Error](niceyaml.NewSummary("2 problems", nil, nested, nilNested, other))
	require.True(t, ok)

	assert.Equal(t, "2 problems", summary.Cause().Error())
	assert.Equal(t, []error{nested, other}, summary.Errors())
	assert.Empty(t, summary.Details())
	assert.Equal(t, []error{summary.Cause(), nested, other}, summary.Unwrap())

	// The slice of the errors a summary heads is a copy.
	summary.Errors()[0] = nil
	assert.Equal(t, []error{nested, other}, summary.Errors())

	require.EqualError(t, niceyaml.NewError("plain").Cause(), "plain")
	assert.Empty(t, niceyaml.NewError("plain").Errors())
	assert.Empty(t, niceyaml.NewError("plain").Details())
	require.EqualError(t, niceyaml.NewError("").Cause(), "")

	var (
		zero   niceyaml.Error
		nilErr *niceyaml.Error
	)

	assert.NoError(t, zero.Cause())
	assert.NoError(t, nilErr.Cause())
	assert.Empty(t, nilErr.Errors())
	assert.Empty(t, nilErr.Details())
}

// ruleError is a cause of a type the caller defines.
type ruleError struct {
	id string
}

func (e *ruleError) Error() string {
	return "rule " + e.id
}

func TestSourceError_Cause(t *testing.T) {
	t.Parallel()

	source := niceyaml.NewSourceFromString("x:\n  a: 1\n  b: 2\n")
	x := paths.Current().Child("x")
	a := paths.Current().Child("a")

	rule := &ruleError{id: "a"}
	located := niceyaml.Invalid(rule, niceyaml.AtPath(a))
	inContext := fmt.Errorf("check: %w", rule)
	aroundLocated := fmt.Errorf("check: %w", niceyaml.Invalid(rule, niceyaml.AtPath(x.Child("a"))))

	tcs := map[string]struct {
		err  error
		want error
	}{
		"located error": {
			err:  niceyaml.Invalid(rule, niceyaml.AtPath(x.Child("a"))),
			want: rule,
		},
		"rebased error": {
			err:  niceyaml.Rebase(located, x),
			want: rule,
		},
		"error rebased twice": {
			err:  niceyaml.Rebase(niceyaml.Rebase(located, paths.Current()), x),
			want: rule,
		},
		"error wrapped in a located error": {
			err:  niceyaml.Invalid(located, niceyaml.AtPath(x.Child("b"))),
			want: rule,
		},
		"error above a binding": {
			err: niceyaml.Invalid(
				yamltest.Bind(t, source, niceyaml.Rebase(located, x)),
				niceyaml.WithDetails(errors.New("more")),
			),
			want: rule,
		},
		"wrapper under a located error": {
			err:  niceyaml.Invalid(inContext, niceyaml.AtPath(x.Child("a"))),
			want: inContext,
		},
		"wrapper around a located error": {
			err:  aroundLocated,
			want: aroundLocated,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var bound *niceyaml.SourceError

			require.ErrorAs(t, yamltest.Bind(t, source, tc.err), &bound)
			assert.Same(t, tc.want, bound.Cause())
		})
	}

	t.Run("message from NewError", func(t *testing.T) {
		t.Parallel()

		var bound *niceyaml.SourceError

		require.ErrorAs(t, yamltest.Bind(t, source, niceyaml.NewError("bad", niceyaml.AtPath(x))), &bound)
		require.EqualError(t, bound.Cause(), "bad")
	})

	t.Run("empty message from NewError", func(t *testing.T) {
		t.Parallel()

		var bound *niceyaml.SourceError

		require.ErrorAs(t, yamltest.Bind(t, source, niceyaml.NewError("", niceyaml.AtPath(x.Child("a")))), &bound)
		require.EqualError(t, bound.Cause(), "")
	})

	t.Run("the zero Error with a location", func(t *testing.T) {
		t.Parallel()

		var (
			zero  niceyaml.Error
			bound *niceyaml.SourceError
		)

		require.ErrorAs(t, yamltest.Bind(t, source, zero.With(niceyaml.AtPath(x.Child("a")))), &bound)
		assert.NoError(t, bound.Cause())
	})

	t.Run("a summary", func(t *testing.T) {
		t.Parallel()

		// A summary heads one error per violation. [errors.As] on its
		// binding descends into them and finds the rule of the first, and
		// Cause returns the error of each binding alone.
		ruleB := &ruleError{id: "b"}
		summary := niceyaml.NewSummary("2 violations",
			located,
			niceyaml.Invalid(ruleB, niceyaml.AtPath(paths.Current().Child("b"))),
		)

		var bound *niceyaml.SourceError

		require.ErrorAs(t, yamltest.Bind(t, source, niceyaml.Rebase(summary, x)), &bound)

		found, ok := errors.AsType[*ruleError](bound)
		require.True(t, ok)
		assert.Same(t, rule, found)

		own, ok := errors.AsType[*ruleError](bound.Cause())
		assert.False(t, ok)
		assert.Nil(t, own)
		require.EqualError(t, bound.Cause(), "2 violations")

		children := bound.Errors()
		require.Len(t, children, 2)
		assert.Same(t, rule, children[0].Cause())
		assert.Same(t, ruleB, children[1].Cause())
	})

	t.Run("nil receiver", func(t *testing.T) {
		t.Parallel()

		var nilErr *niceyaml.SourceError

		assert.NoError(t, nilErr.Cause())
	})
}

func TestSourceError_Errors(t *testing.T) {
	t.Parallel()

	source := niceyaml.NewSourceFromString("a: 1\nb: 2\n")
	badA := niceyaml.NewError("bad a", niceyaml.AtPath(paths.Current().Child("a")))
	badX := niceyaml.NewError("bad x", niceyaml.AtPath(paths.Current().Child("x").Index(0)))
	deep := niceyaml.NewError("deep", niceyaml.AtPath(paths.Current().Child("b")))
	mid := niceyaml.NewError("mid", niceyaml.WithDetails(deep))

	var bound *niceyaml.SourceError

	require.ErrorAs(
		t,
		yamltest.Bind(t, source, niceyaml.NewSummary("main", badA, badX, nil, mid)),
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

	// The detail of a problem the summary heads binds below that problem,
	// which heads nothing, and the summary holds no details.
	assert.Empty(t, bound.Details())
	assert.Empty(t, children[2].Errors())

	grandchildren := children[2].Details()
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
	bound := dd.Bind(niceyaml.NewError("root", niceyaml.AtPath(paths.Current().Child("name"))))
	kid := niceyaml.NewError("kid", niceyaml.AtPath(paths.Current().Child("hours", "open")))

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

	// The second document of three does not parse.
	broken := niceyaml.NewSourceFromString("a: 1\n---\nb: [\n---\nc: 3\n")
	brokenDocs := broken.AllDocuments()
	require.Len(t, brokenDocs, 3)

	tcs := map[string]struct {
		err  error
		want *niceyaml.Node
	}{
		"bound by a document": {
			err:  docs[1].Bind(niceyaml.NewError("bad", niceyaml.AtPath(paths.Current().Child("b")))),
			want: docs[1],
		},
		"produced by a document": {
			err: func() error {
				_, err := docs[0].Ranges(paths.Current().Child("missing"))

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
		"syntax error of the second document of three": {
			err: func() error {
				_, err := broken.File()

				return err
			}(),
			want: brokenDocs[1],
		},
		"syntax error from the document that holds it": {
			err:  brokenDocs[1].Validate(t.Context(), nil),
			want: brokenDocs[1],
		},
		"bound by the source at a position beside a syntax error": {
			err:  broken.Bind(niceyaml.NewError("bad", niceyaml.AtPosition(position.New(4, 0)))),
			want: brokenDocs[2],
		},
		"bound by the source at a path beside a syntax error": {
			err: broken.Bind(niceyaml.NewError("bad", niceyaml.AtPath(paths.Current().Child("c")))),
		},
		"wrapping a binding keeps its document": {
			err:  docs[1].Bind(fmt.Errorf("context: %w", docs[0].Bind(niceyaml.NewError("bad")))),
			want: docs[0],
		},
		"nesting errors around a binding keeps its document": {
			err: docs[1].Bind(niceyaml.Invalid(
				docs[0].Bind(niceyaml.NewError("bad")),
				niceyaml.WithDetails(niceyaml.NewError("b", niceyaml.AtPath(paths.Current().Child("b")))),
			)),
			want: docs[0],
		},
		"nesting errors around a binding to no document keeps none": {
			err: docs[1].Bind(niceyaml.Invalid(
				source.Bind(niceyaml.NewError("bad")),
				niceyaml.WithDetails(niceyaml.NewError("b", niceyaml.AtPath(paths.Current().Child("b")))),
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

func TestSourceError_DocumentIndex(t *testing.T) {
	t.Parallel()

	source := niceyaml.NewSourceFromString("a: 1\n---\nb: 2\n")
	docs, err := source.Documents()
	require.NoError(t, err)

	// The second document of three does not parse.
	broken := niceyaml.NewSourceFromString("a: 1\n---\nb: [\n---\nc: 3\n")
	brokenDocs := broken.AllDocuments()
	require.Len(t, brokenDocs, 3)

	// The header follows an anchor with no value, so both documents parse
	// together and share the syntax error of the second.
	together := niceyaml.NewSourceFromString("a: &x\n---\nb: [1\n").AllDocuments()
	require.Len(t, together, 2)

	tcs := map[string]struct {
		err  error
		want int
		ok   bool
	}{
		"bound by the first document": {
			err:  docs[0].Bind(niceyaml.NewError("bad")),
			want: 0,
			ok:   true,
		},
		"bound by the second document": {
			err:  docs[1].Bind(niceyaml.NewError("bad", niceyaml.AtPath(paths.Current().Child("b")))),
			want: 1,
			ok:   true,
		},
		"bound by a scoped node of the second document": {
			err:  yamltest.At(t, docs[1], paths.Current().Child("b")).Bind(niceyaml.NewError("bad")),
			want: 1,
			ok:   true,
		},
		"bound by the source at a position": {
			err:  source.Bind(niceyaml.NewError("bad", niceyaml.AtPosition(position.New(2, 0)))),
			want: 1,
			ok:   true,
		},
		"bound by the source at no location": {
			err: source.Bind(niceyaml.NewError("bad")),
		},
		"bound by the source on a line no document holds": {
			err: source.Bind(niceyaml.NewError("bad", niceyaml.AtPosition(position.New(9, 0)))),
		},
		"bound by the source at a path among several documents": {
			err: source.Bind(niceyaml.NewError("bad", niceyaml.AtPath(paths.Current().Child("b")))),
		},
		"syntax error of the second document of three": {
			err: func() error {
				_, err := broken.File()

				return err
			}(),
			want: 1,
			ok:   true,
		},
		"syntax error from the document that parsed together with its own": {
			err:  together[0].Validate(t.Context(), nil),
			want: 1,
			ok:   true,
		},
		"bound by the source at a position beside a syntax error": {
			err:  broken.Bind(niceyaml.NewError("bad", niceyaml.AtPosition(position.New(4, 0)))),
			want: 2,
			ok:   true,
		},
		"wrapping a binding keeps its document": {
			err:  docs[1].Bind(fmt.Errorf("context: %w", docs[0].Bind(niceyaml.NewError("bad")))),
			want: 0,
			ok:   true,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var bound *niceyaml.SourceError

			require.ErrorAs(t, tc.err, &bound)

			got, ok := bound.DocumentIndex()
			assert.Equal(t, tc.ok, ok)
			assert.Equal(t, tc.want, got)

			// The index is that of the document, where the error has one.
			if doc := bound.Document(); doc != nil {
				assert.Equal(t, doc.DocumentIndex(), got)
			} else {
				assert.False(t, ok)
			}
		})
	}

	t.Run("a report reads the document of every problem from its binding", func(t *testing.T) {
		t.Parallel()

		// A syntax error, a violation in a document that parsed, and an
		// error bound to no source, which has no binding to read.
		err := errors.Join(
			broken.ValidateDocuments(t.Context(), nil),
			brokenDocs[2].Bind(niceyaml.NewError("bad c", niceyaml.AtPath(paths.Current().Child("c")))),
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
		return niceyaml.NewError("closes before it opens", niceyaml.AtPath(paths.Current().Child("close")))
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
		niceyaml.NewError("bad open", niceyaml.AtPath(paths.Current().Child("open"))),
		niceyaml.NewError("bad close", niceyaml.AtPath(paths.Current().Child("close"))),
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
	hours := paths.Current().Child("hours")
	closePath := paths.Current().Child("close")

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

		// The join heads no error, so it is one problem rather than a
		// heading, and a problem with no location takes the base, as a
		// member of a summary as well.
		dd := yamltest.FirstDocument(t, input)

		report := niceyaml.NewSummary("invalid hours", joined, errors.New("bad open"))
		bound := dd.Bind(niceyaml.Rebase(report, hours))

		var se *niceyaml.SourceError

		require.ErrorAs(t, bound, &se)
		require.Len(t, se.Errors(), 2)

		child := se.Errors()[0]
		assert.NotPanics(t, func() {
			assert.Equal(t, "3:3: $.hours:", strings.TrimSpace(child.Error()))
			assert.Equal(t, "\n", child.Message())
			assert.Contains(t, niceyaml.FormatError(bound, 1), "invalid hours\n")
		})

		var rows []string

		for problem := range niceyaml.NewErrorTree(dd.Bind(niceyaml.Rebase(joined, hours))).Problems() {
			rows = append(rows, strings.TrimSpace(problem.Text))
		}

		assert.Equal(t, []string{"3:3: $.hours:"}, rows)
	})

	t.Run("path composes with the base", func(t *testing.T) {
		t.Parallel()

		err := niceyaml.Rebase(niceyaml.NewError("closes before it opens", niceyaml.AtPath(closePath)), hours)

		var e *niceyaml.Error

		require.ErrorAs(t, err, &e)

		p, ok := e.Path()
		require.True(t, ok)
		assert.Equal(t, "@.hours.close", p.String())
		assert.Equal(t, "closes before it opens", err.Error())
		assert.Equal(t, "@.hours.close: closes before it opens", niceyaml.FormatError(err, 0))
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

	t.Run("the errors a summary heads compose too", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, input)

		report := niceyaml.NewSummary("invalid hours",
			niceyaml.NewError("bad open", niceyaml.AtPath(paths.Current().Child("open"))),
			niceyaml.NewError("bad close", niceyaml.AtPath(closePath)),
		)

		// The summary is a heading, so it takes no location from the base.
		err := dd.Bind(niceyaml.Rebase(report, hours))
		require.EqualError(t, err, stringtest.JoinLF(
			"invalid hours",
			"3:9: $.hours.open: bad open",
			"4:10: $.hours.close: bad close",
		))

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

		report := niceyaml.NewError("invalid hours", niceyaml.WithDetails(
			niceyaml.NewError("bad open", niceyaml.AtPath(paths.Current().Child("open"))),
		))

		assert.Equal(t, stringtest.JoinLF(
			"@.hours: invalid hours",
			"`-- @.hours.open: bad open",
		), fmt.Sprintf("%+v", niceyaml.Rebase(report, hours)))
	})

	t.Run("a join rebases branch by branch", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, input)

		open := niceyaml.NewError("bad open", niceyaml.AtPath(paths.Current().Child("open")))
		other := errors.New("bad hours")

		err := niceyaml.Rebase(errors.Join(open, other), hours)
		require.EqualError(t, err, "bad open\nbad hours")
		assert.Equal(t, "|-- @.hours.open: bad open\n`-- @.hours: bad hours", niceyaml.FormatError(err, 0))
		require.ErrorIs(t, err, open)
		require.ErrorIs(t, err, other)

		got := []string{}
		for se := range niceyaml.AllBindings(dd.Bind(err)) {
			got = append(got, se.Error())
		}

		assert.Equal(t, []string{
			"3:3: $.hours: bad hours\n3:9: $.hours.open: bad open",
			"3:9: $.hours.open: bad open",
			"3:3: $.hours: bad hours",
		}, got)
	})

	t.Run("an error that only wraps a join rebases as the join", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, input)

		open := niceyaml.NewError("bad open", niceyaml.AtPath(paths.Current().Child("open")))
		closeErr := niceyaml.NewError("bad close", niceyaml.AtPath(closePath))

		err := niceyaml.Rebase(niceyaml.Invalid(errors.Join(open, closeErr)), hours)
		require.EqualError(t, err, "bad open\nbad close")
		require.ErrorIs(t, err, open)
		require.ErrorIs(t, err, closeErr)

		var e *niceyaml.Error

		require.ErrorAs(t, err, &e)

		got := []string{}
		for se := range niceyaml.AllBindings(dd.Bind(err)) {
			got = append(got, se.Error())
		}

		assert.Equal(t, []string{
			"3:9: $.hours.open: bad open\n4:10: $.hours.close: bad close",
			"3:9: $.hours.open: bad open",
			"4:10: $.hours.close: bad close",
		}, got)

		bare := dd.Bind(niceyaml.Rebase(errors.Join(open, closeErr), hours))
		assert.Equal(t, niceyaml.FormatError(bare, -1), niceyaml.FormatError(dd.Bind(err), -1))
	})

	t.Run("a join of the caller's own type keeps its type", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, input)

		open := niceyaml.NewError("bad open", niceyaml.AtPath(paths.Current().Child("open")))
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
				err: niceyaml.Invalid(joined),
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				err := niceyaml.Rebase(tc.err, hours)
				require.EqualError(t, err, "bad open\nbad close")
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
			niceyaml.NewError("bad open", niceyaml.AtPath(paths.Current().Child("open"))),
			niceyaml.NewError("bad close", niceyaml.AtPath(closePath)),
		}}}

		err := niceyaml.Rebase(joined, hours)
		require.EqualError(t, err, "bad open\nbad close")
		require.ErrorIs(t, err, errInvalidHours)

		var custom *customTestError

		require.ErrorAs(t, err, &custom)
		assert.Equal(t, "invalid hours", custom.Error())
	})

	t.Run("a decode keeps the type of a join a self validator returns", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, input)

		_, err := dd.Decode[joinedConfig](t.Context())
		require.EqualError(t, err, "3:9: $.hours.open: bad open\n4:10: $.hours.close: bad close")

		var got sparseJoinError

		require.ErrorAs(t, err, &got)
	})

	t.Run("a problem without a location in a summary points at the base", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, input)

		report := niceyaml.NewSummary("invalid hours", errors.New("bad open"), errors.New("bad close"))

		err := dd.Bind(niceyaml.Rebase(report, hours))

		var bound *niceyaml.SourceError

		require.ErrorAs(t, err, &bound)
		require.Len(t, bound.Errors(), 2)
		assert.Equal(t, "3:3: $.hours: bad open", bound.Errors()[0].Error())
		assert.Equal(t, "3:3: $.hours: bad close", bound.Errors()[1].Error())

		_, ok := bound.Range()
		assert.False(t, ok)
	})

	t.Run("a detail without a location takes none from the base", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, input)

		report := niceyaml.NewError("invalid hours", niceyaml.WithDetails(errors.New("bad hours")))

		err := dd.Bind(niceyaml.Rebase(report, hours))
		require.EqualError(t, err, "3:3: $.hours: invalid hours")

		var bound *niceyaml.SourceError

		require.ErrorAs(t, err, &bound)
		assert.Empty(t, bound.Errors())
		require.Len(t, bound.Details(), 1)

		detail := bound.Details()[0]
		assert.Equal(t, "bad hours", detail.Error())

		_, ok := detail.Path()
		assert.False(t, ok)
	})

	t.Run("an unlocated problem in a summary under a root base points at the root", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, input)

		report := niceyaml.NewSummary("invalid", errors.New("bad child"), errors.New("bad sibling"))
		rebased := niceyaml.Rebase(report, paths.Current())

		err := dd.Bind(rebased)
		require.EqualError(t, err, "invalid\n1:1: $: bad child\n1:1: $: bad sibling")

		var bound *niceyaml.SourceError

		require.ErrorAs(t, err, &bound)
		require.Len(t, bound.Errors(), 2)

		child := bound.Errors()[0]
		assert.Equal(t, "1:1: $: bad child", child.Error())

		p, ok := child.Path()
		require.True(t, ok)
		assert.Equal(t, paths.Doc(), p)

		assert.Equal(t, stringtest.JoinLF(
			"invalid",
			"|-- @: bad child",
			"`-- @: bad sibling",
		), fmt.Sprintf("%+v", rebased))
	})

	t.Run("rebases compose", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, "spec:\n  hours:\n    open: 1\n    close: 0\n")

		inner := niceyaml.NewError("closes before it opens", niceyaml.AtPath(closePath))
		err := niceyaml.Rebase(niceyaml.Rebase(inner, hours), paths.Current().Child("spec"))

		assert.Equal(t, "closes before it opens", err.Error())
		assert.Equal(t, "@.spec.hours.close: closes before it opens", niceyaml.FormatError(err, 0))
		require.EqualError(t, dd.Bind(err), "4:12: $.spec.hours.close: closes before it opens")
	})

	t.Run("wrappers between rebases add no paths", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, "spec:\n  hours:\n    open: 1\n    close: 0\n")

		// Each level wraps the result of the level below and rebases it, so
		// the text holds every wrapper and binding names the joined path
		// once, in front of all of them.
		inner := niceyaml.NewError("closes before it opens", niceyaml.AtPath(closePath))
		err := niceyaml.Rebase(
			fmt.Errorf("check spec: %w", niceyaml.Rebase(fmt.Errorf("check hours: %w", inner), hours)),
			paths.Current().Child("spec"),
		)

		assert.Equal(t, "check spec: check hours: closes before it opens", err.Error())
		assert.Equal(t,
			"@.spec.hours.close: check spec: check hours: closes before it opens",
			niceyaml.FormatError(err, -1),
		)

		bound := dd.Bind(err)
		require.EqualError(t, bound, "4:12: $.spec.hours.close: check spec: check hours: closes before it opens")

		var se *niceyaml.SourceError

		require.ErrorAs(t, bound, &se)
		assert.Equal(t, "check spec: check hours: closes before it opens", se.Message())
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

	t.Run("details above a binding join the base", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, input)

		namePath := paths.Current().Child("name")
		bound := dd.Bind(niceyaml.NewError("bad name", niceyaml.AtPath(namePath)))
		wrapped := niceyaml.Invalid(bound, niceyaml.WithDetails(
			niceyaml.NewError("bad open", niceyaml.AtPath(paths.Current().Child("open"))),
		))

		r := niceyaml.Rebase(wrapped, hours)
		assert.Equal(t, "1:7: $.name: bad name", r.Error())

		var e *niceyaml.Error

		require.ErrorAs(t, r, &e)

		p, ok := e.Path()
		require.True(t, ok)
		assert.Equal(t, paths.Doc().Join(namePath), p)

		var rebound *niceyaml.SourceError

		require.ErrorAs(t, dd.Bind(r), &rebound)
		assert.Equal(t, "1:7: $.name: bad name", rebound.Error())

		p, ok = rebound.Path()
		require.True(t, ok)
		assert.Equal(t, paths.Doc().Join(namePath), p)

		assert.Empty(t, rebound.Errors())
		require.Len(t, rebound.Details(), 1)
		assert.Equal(t, "3:9: $.hours.open: bad open", rebound.Details()[0].Error())
	})

	t.Run("an unlocated binding under the base keeps no path", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, input)

		bound := dd.Bind(errors.New("plain"))
		wrapped := niceyaml.Invalid(bound, niceyaml.WithDetails(
			niceyaml.NewError("bad open", niceyaml.AtPath(paths.Current().Child("open"))),
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

		moved := e.With(niceyaml.AtPath(paths.Current().Child("open")))

		p, ok := moved.Path()
		require.True(t, ok)
		assert.Equal(t, hours.Child("open"), p)
		assert.Equal(t, "closes before it opens", moved.Error())
		assert.Equal(t, "@.hours.open: closes before it opens", niceyaml.FormatError(moved, 0))
		require.EqualError(t, dd.Bind(moved), `3:9: $.hours.open: closes before it opens`)
	})

	t.Run("a wrapper between the check and the rebase adds no path", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, input)

		inner := niceyaml.NewError("closes before it opens", niceyaml.AtPath(closePath))
		err := niceyaml.Rebase(fmt.Errorf("checking hours: %w", inner), hours)

		// The wrapper holds the message alone, so binding names the joined
		// path once, in front of the text the wrapper added.
		require.ErrorIs(t, err, inner)
		assert.Equal(t, "checking hours: closes before it opens", err.Error())
		assert.Equal(t, "@.hours.close: checking hours: closes before it opens", niceyaml.FormatError(err, 0))
		require.EqualError(t, dd.Bind(err), "4:10: $.hours.close: checking hours: closes before it opens")

		var se *niceyaml.SourceError

		require.ErrorAs(t, dd.Bind(err), &se)
		assert.Equal(t, "checking hours: closes before it opens", se.Message())
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

	open := func() error {
		return niceyaml.NewError("bad", niceyaml.AtPath(paths.Current().Child("open")))
	}
	name := func() error {
		return niceyaml.NewError("bad", niceyaml.AtPath(paths.Doc().Child("name")))
	}

	tcs := map[string]struct {
		err func() error
		// The node that binds the result.
		node *niceyaml.Node
		// The path of the unbound result, and the message of its binding.
		wantPath string
		want     string
	}{
		"relative path under a relative base": {
			err:      func() error { return niceyaml.Rebase(open(), paths.Current().Child("spec", "hours")) },
			node:     doc,
			wantPath: "@.spec.hours.open",
			want:     `4:11: $.spec.hours.open: bad`,
		},
		"relative path under an absolute base": {
			err:      func() error { return niceyaml.Rebase(open(), hoursPath) },
			node:     doc,
			wantPath: "$.spec.hours.open",
			want:     `4:11: $.spec.hours.open: bad`,
		},
		"absolute path under a relative base": {
			err:      func() error { return niceyaml.Rebase(name(), paths.Current().Child("spec", "hours")) },
			node:     doc,
			wantPath: "$.name",
			want:     `1:7: $.name: bad`,
		},
		"absolute path under an absolute base": {
			err:      func() error { return niceyaml.Rebase(name(), hoursPath) },
			node:     doc,
			wantPath: "$.name",
			want:     `1:7: $.name: bad`,
		},
		"no location under an absolute base": {
			err:      func() error { return niceyaml.Rebase(errors.New("bad"), hoursPath) },
			node:     doc,
			wantPath: "$.spec.hours",
			want:     `4:5: $.spec.hours: bad`,
		},
		"absolute base stops the bases above it": {
			err: func() error {
				return niceyaml.Rebase(niceyaml.Rebase(open(), hoursPath), paths.Current().Child("x"))
			},
			node:     doc,
			wantPath: "$.spec.hours.open",
			want:     `4:11: $.spec.hours.open: bad`,
		},
		"scoped Node binds under its own path": {
			err:      func() error { return niceyaml.Rebase(open(), hours.Path()) },
			node:     hours,
			wantPath: "$.spec.hours.open",
			want:     `4:11: $.spec.hours.open: bad`,
		},
		"scoped Node puts its path in front of a relative path": {
			err:      func() error { return niceyaml.Rebase(open(), paths.Current()) },
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

			err := tc.err()

			var located *niceyaml.Error

			require.ErrorAs(t, err, &located)

			path, ok := located.Path()
			require.True(t, ok)
			assert.Equal(t, tc.wantPath, path.String())

			bound := tc.node.Bind(err)
			require.EqualError(t, bound, tc.want)

			var source *niceyaml.SourceError

			require.ErrorAs(t, bound, &source)

			boundPath, ok := source.Path()
			require.True(t, ok)
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
		niceyaml.NewError("name is required", niceyaml.AtPath(paths.Current().Child("name"))),
	}

	return niceyaml.NewSummary("2 violations", errs[:n]...)
}

// valueCheckError wraps the error of a check with a prefix, as a caller's
// own error type does.
type valueCheckError struct {
	err error
}

func (e *valueCheckError) Error() string {
	return "check: " + e.err.Error()
}

func (e *valueCheckError) Unwrap() error {
	return e.err
}

// valueFixedError wraps an error under a message of its own, as an error
// type that maps a failure to a status does.
type valueFixedError struct {
	err error
}

func (e valueFixedError) Error() string {
	return "invalid body"
}

func (e valueFixedError) Unwrap() error {
	return e.err
}

// valueIndentError wraps an error and indents each line of its text.
type valueIndentError struct {
	err error
}

func (e valueIndentError) Error() string {
	return "validation:\n  " + strings.ReplaceAll(e.err.Error(), "\n", "\n  ")
}

func (e valueIndentError) Unwrap() error {
	return e.err
}

// valueConfig holds a [valueRequest] under the key request.
type valueConfig struct {
	Request valueRequest `yaml:"request"`
}

// valueRequest is a [niceyaml.SelfValidator] whose Validate returns its
// error through [niceyaml.BindValue], so a caller outside a decode
// prints the path. With fixed, it returns that error inside a
// [valueFixedError].
type valueRequest struct {
	Port int `yaml:"port"`

	fixed bool
}

func (r valueRequest) Validate() error {
	if r.Port >= 1 {
		return nil
	}

	err := niceyaml.BindValue(niceyaml.NewError("0 is less than 1", niceyaml.AtPath(paths.Current().Child("port"))))
	if r.fixed {
		return valueFixedError{err: err}
	}

	//nolint:wrapcheck // The test checks where a decode places the result as it is.
	return err
}

func TestBindValue(t *testing.T) {
	t.Parallel()

	port := niceyaml.NewError("0 is less than 1", niceyaml.AtPath(paths.Current().Child("port")))

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
				niceyaml.NewError("name is required", niceyaml.AtPath(paths.Current().Child("name"))),
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
				niceyaml.NewError("first declared here", niceyaml.AtPath(paths.Current().Child("port"))),
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
			require.EqualError(t, errors.Join(errors.New("other failure"), got), "other failure\n"+tc.want)

			// The source holds no line to excerpt, so the tree stands
			// alone, with no line that says so.
			assert.Equal(t, tc.wantFormat, niceyaml.FormatError(got, 2))
			assert.Equal(t, tc.wantFormat, fmt.Sprintf("%+v", got))

			// No binding of the result names a Node, a document, or a
			// position, and its source holds no name and no line.
			var gotPaths []string

			for b := range niceyaml.AllBindings(got) {
				assert.Nil(t, b.Node())
				assert.Nil(t, b.Document())
				assert.Empty(t, b.Source().Name())
				assert.Zero(t, b.Source().Lines().Len())

				_, ok := b.DocumentIndex()
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

		require.EqualError(t, niceyaml.BindValue(niceyaml.Place(inner, at)), stringtest.JoinLF(
			"$.request: 2 violations",
			"$.port: 0 is less than 1",
			"$.name: name is required",
		))
	})

	t.Run("a position finds no line", func(t *testing.T) {
		t.Parallel()

		got := niceyaml.BindValue(niceyaml.NewError("bad indent", niceyaml.AtPosition(position.New(1, 2))))
		require.EqualError(t, got, "bad indent")

		var bound *niceyaml.SourceError

		require.ErrorAs(t, got, &bound)
		require.ErrorIs(t, bound.Unresolved(), niceyaml.ErrOutOfRange)

		// A document places the position as it places a path.
		doc := yamltest.FirstDocument(t, "request:\n  port: 0\n", niceyaml.WithName("app.yaml"))
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

	doc := yamltest.FirstDocument(t, "request:\n  port: 0\n", niceyaml.WithName("app.yaml"))
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

			for b := range niceyaml.AllBindings(placed) {
				assert.Same(t, doc.Source(), b.Source())
				assert.Same(t, doc, b.Document())
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
			require.EqualError(t, niceyaml.NewLayers(doc).Bind(nil, err), tc.wantRoot)

			// A join places each result it holds.
			require.EqualError(t, doc.Bind(niceyaml.Rebase(errors.Join(err), base)), tc.wantBound)

			// Placing builds a new error and leaves the result as it was.
			require.EqualError(t, err, tc.wantValue)

			for b := range niceyaml.AllBindings(err) {
				assert.NotSame(t, doc.Source(), b.Source())
				assert.Nil(t, b.Document())
			}
		})
	}

	t.Run("a wrapper keeps what it matches", func(t *testing.T) {
		t.Parallel()

		wrapped := &valueCheckError{err: niceyaml.BindValue(valueErrors(1))}
		require.EqualError(t, wrapped, "check: $.port: 0 is less than 1")

		placed := doc.Bind(niceyaml.Rebase(wrapped, base))
		require.EqualError(t, placed, "app.yaml:2:9: $.request.port: check: 0 is less than 1")
		require.ErrorIs(t, placed, wrapped)

		var got *valueCheckError

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
				err:  valueFixedError{err: one},
				want: "app.yaml:2:9: $.request.port: invalid body",
			},
			"message of its own above two problems": {
				err: valueFixedError{err: two},
				want: stringtest.JoinLF(
					"app.yaml: invalid body",
					"app.yaml:1:1: $.request.name: name is required",
					"app.yaml:2:9: $.request.port: 0 is less than 1",
				),
			},
			"message of its own beside a sentinel": {
				err:  fmt.Errorf("%w: %w", errCheck, valueFixedError{err: one}),
				want: "app.yaml:2:9: $.request.port: check: invalid body",
			},
			"indented lines": {
				err: valueIndentError{err: two},
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

				for b := range niceyaml.AllBindings(placed) {
					assert.Same(t, doc.Source(), b.Source())
				}
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
			"Place with no option under a wrapper": {
				wrap:     func(err error) error { return fmt.Errorf("check: %w", niceyaml.Place(err)) },
				problems: 1,
				want:     "app.yaml:2:9: $.request.port: check: 0 is less than 1",
			},
			"location above one problem": {
				wrap:     func(err error) error { return niceyaml.Invalid(err, at) },
				problems: 1,
				want:     "app.yaml:2:3: $.request: 0 is less than 1",
			},
			"location above two problems": {
				wrap:     func(err error) error { return niceyaml.Invalid(err, at) },
				problems: 2,
				want: stringtest.JoinLF(
					"app.yaml:2:3: $.request: 2 violations",
					"app.yaml:1:1: $.request.name: name is required",
					"app.yaml:2:9: $.request.port: 0 is less than 1",
				),
			},
			"wrapper around a location": {
				wrap:     func(err error) error { return fmt.Errorf("check: %w", niceyaml.Place(err, at)) },
				problems: 2,
				want: stringtest.JoinLF(
					"app.yaml:2:3: $.request: check: 2 violations",
					"app.yaml:1:1: $.request.name: name is required",
					"app.yaml:2:9: $.request.port: 0 is less than 1",
				),
			},
			"details beside one problem": {
				wrap:        func(err error) error { return niceyaml.Place(err, why) },
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

				var bound *niceyaml.SourceError

				require.ErrorAs(t, placed, &bound)

				var gotDetails []string

				for _, detail := range bound.Details() {
					gotDetails = append(gotDetails, detail.Message())
				}

				assert.Equal(t, tc.wantDetails, gotDetails)

				for _, problem := range bound.Errors() {
					assert.Same(t, doc.Source(), problem.Source())
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
		require.EqualError(t, err, "app.yaml:2:9: $.request.port: invalid body")
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

	t.Run("a summary and a detail place the results they hold", func(t *testing.T) {
		t.Parallel()

		port := niceyaml.BindValue(valueErrors(1))
		name := niceyaml.BindValue(
			niceyaml.NewError("name is required", niceyaml.AtPath(paths.Current().Child("name"))),
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

		var bound *niceyaml.SourceError

		require.ErrorAs(t, doc.Bind(detailed), &bound)
		require.Len(t, bound.Details(), 1)
		assert.Same(t, doc.Source(), bound.Details()[0].Source())
		require.EqualError(t, bound.Details()[0], "app.yaml:2:9: $.request.port: 0 is less than 1")
	})

	t.Run("each problem places on its own", func(t *testing.T) {
		t.Parallel()

		var result *niceyaml.SourceError

		require.ErrorAs(t, niceyaml.BindValue(valueErrors(2)), &result)
		require.Len(t, result.Errors(), 2)

		// A caller that keeps only some of the problems places the ones
		// it keeps, and each stands in the document as it does when the
		// caller places the whole result.
		want := []string{
			"app.yaml:2:9: $.request.port: 0 is less than 1",
			"app.yaml:1:1: $.request.name: name is required",
		}

		for i, problem := range result.Errors() {
			placed := doc.Bind(niceyaml.Rebase(problem, base))
			require.EqualError(t, placed, want[i])
			assert.True(t, niceyaml.IsInvalid(placed))

			for b := range niceyaml.AllBindings(placed) {
				assert.Same(t, doc.Source(), b.Source())
			}

			// The problem itself stays where the result put it.
			assert.Nil(t, problem.Document())
		}

		kept := errors.Join(result.Errors()[1], result.Errors()[0])
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
		bound := yamltest.Bind(t, src, niceyaml.NewError("bad", niceyaml.AtPath(paths.Current().Child("b"))))
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
			yamltest.Bind(t, first, niceyaml.NewError("bad a", niceyaml.AtPath(paths.Current().Child("a")))),
			yamltest.Bind(t, second, niceyaml.NewError("bad b", niceyaml.AtPath(paths.Current().Child("b")))),
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
			docs[0].Bind(niceyaml.NewError("not a number", niceyaml.AtPath(paths.Current().Child("a")))),
			docs[1].Bind(niceyaml.NewError("not a number", niceyaml.AtPath(paths.Current().Child("a")))),
			docs[2].Bind(niceyaml.NewError("invalid document", niceyaml.WithDetails(
				niceyaml.NewError("not a string", niceyaml.AtPath(paths.Current().Child("b"))),
			))),
			docs[3].Bind(niceyaml.NewError("not allowed", niceyaml.AtPath(paths.Current().Child("c").Key()))),
		)

		assert.Equal(t, stringtest.JoinLF(
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
		), niceyaml.FormatError(joined, 2))

		// Each binding still renders on its own, for a caller that wants
		// one section per document.
		var bound *niceyaml.SourceError

		require.ErrorAs(t, docs[1].Bind(niceyaml.NewError("bad", niceyaml.AtPath(paths.Current().Child("a")))), &bound)

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
			docs[0].Bind(niceyaml.NewError("gone", niceyaml.AtPath(paths.Current().Child("x").Index(0)))),
			docs[1].Bind(niceyaml.NewError("bad b", niceyaml.AtPath(paths.Current().Child("b")))),
			docs[1].Bind(errors.New("plain")),
		)

		assert.Equal(t, stringtest.JoinLF(
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
		), niceyaml.FormatError(joined, 2))
	})

	t.Run("an error bound to no source renders as its message", func(t *testing.T) {
		t.Parallel()

		assert.Equal(t, "plain", niceyaml.FormatError(errors.New("plain"), 2))

		located := niceyaml.NewError("bad", niceyaml.AtPath(paths.Current().Child("a")))

		assert.Equal(t, "@.a: bad", niceyaml.FormatError(located, 2))
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
			// A path writes a tab in a key as an escape, so the tree and
			// the reason a location did not resolve spell the key alike
			// and neither lays out a tab.
			"key of a path that does not resolve": {
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
			"key with a line feed and a tab": {
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
			"message beside a caret": {
				err: yamltest.Bind(t,
					niceyaml.NewSourceFromString("a: 1\nb: 2\n", niceyaml.WithName("cfg.yaml")),
					niceyaml.NewError("invalid", niceyaml.WithDetails(
						niceyaml.NewError("bad\ta", niceyaml.AtPath(paths.Current().Child("a"))),
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

func TestFormatError_WrappedJoin(t *testing.T) {
	t.Parallel()

	source := niceyaml.NewSourceFromString("name: x\nport: 0\n", niceyaml.WithName("svc.yaml"))
	badPort := niceyaml.NewError("0 is less than 1", niceyaml.AtPath(paths.Current().Child("port")))
	badName := niceyaml.NewError("unknown name", niceyaml.AtPath(paths.Current().Child("name")))

	excerpt := []string{
		"",
		"   1 | name: x",
		"     |       ^ unknown name",
		"   2 | port: 0",
		"     |       ^ 0 is less than 1",
	}

	// The message of a wrapper around a join holds every branch of the
	// join, and the branches are the children of the node, so the root
	// keeps the text of the wrapper alone and each branch prints once.
	tcs := map[string]struct {
		err  error
		want []string
	}{
		"join of bindings": {
			err: errors.Join(yamltest.Bind(t, source, badPort), yamltest.Bind(t, source, badName)),
			want: []string{
				"load config:",
				"|-- svc.yaml:2:7: $.port: 0 is less than 1",
				"`-- svc.yaml:1:7: $.name: unknown name",
			},
		},
		"bound join": {
			err: yamltest.Bind(t, source, errors.Join(badPort, badName)),
			want: []string{
				"load config:",
				"|-- svc.yaml:1:7: $.name: unknown name",
				"`-- svc.yaml:2:7: $.port: 0 is less than 1",
			},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			err := fmt.Errorf("load config: %w", tc.err)

			assert.Equal(t, stringtest.JoinLF(append(tc.want, excerpt...)...), niceyaml.FormatError(err, 0))
		})
	}
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

func TestFormatError_NoContent(t *testing.T) {
	t.Parallel()

	badPort := niceyaml.NewError("0 is less than 1", niceyaml.AtPath(paths.Current().Child("port")))
	noName := niceyaml.NewError("name is required", niceyaml.AtPath(paths.Current().Child("name")))

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
			want:   "bad\n\nno excerpt: location outside source: line 4 of an empty source",
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

			var bound *niceyaml.SourceError

			require.ErrorAs(t, err, &bound)

			// The binding still reports why its location did not resolve,
			// for a caller that renders the error itself.
			if tc.reason == nil {
				require.NoError(t, bound.Unresolved())
			} else {
				require.ErrorIs(t, bound.Unresolved(), tc.reason)
			}

			assert.Equal(t, tc.want, niceyaml.FormatError(err, 2))
			assert.Equal(t, tc.want, fmt.Sprintf("%+v", err))
		})
	}
}

func TestSourceError_MessageAndPath(t *testing.T) {
	t.Parallel()

	src := niceyaml.NewSourceFromString("a:\n  b: 1\n", niceyaml.WithName("x.yaml"))
	bPath := paths.Current().Child("a", "b")
	// A binding reports the path from the root of the document.
	wantB := paths.Doc().Child("a", "b")

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
		assert.Equal(t, wantB, p)
	})

	t.Run("a summary keeps the errors it lists out of its message", func(t *testing.T) {
		t.Parallel()

		bound := bind(t, niceyaml.NewSummary("2 problems",
			niceyaml.NewError("bad", niceyaml.AtPath(bPath)),
			niceyaml.NewError("worse", niceyaml.AtPath(bPath)),
		))

		assert.Equal(t, "x.yaml: 2 problems\nx.yaml:2:6: $.a.b: bad\nx.yaml:2:6: $.a.b: worse", bound.Error())
		assert.Equal(t, "2 problems", bound.Message())
	})

	t.Run("a wrapper around a summary keeps the errors it lists out of its message", func(t *testing.T) {
		t.Parallel()

		summary := yamltest.Bind(t, src, niceyaml.NewSummary("2 problems",
			niceyaml.NewError("bad", niceyaml.AtPath(bPath)),
			niceyaml.NewError("worse", niceyaml.AtPath(bPath)),
		))
		bound := bind(t, niceyaml.NewSummary("2 checks",
			fmt.Errorf("spec check: %w", summary),
			errors.New("lint check"),
		))

		children := bound.Errors()
		require.Len(t, children, 2)

		assert.Equal(t,
			"spec check: x.yaml: 2 problems\nx.yaml:2:6: $.a.b: bad\nx.yaml:2:6: $.a.b: worse",
			children[0].Error(),
		)
		assert.Equal(t, "spec check: x.yaml: 2 problems", children[0].Message())
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
		assert.Equal(t, wantB, p)

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
		assert.Equal(t, wantB, p)

		got, ok := bound.Range()
		require.True(t, ok)
		assert.Equal(t, position.New(1, 2), got.Start)
	})

	t.Run("a range locates the error whether or not the path resolves", func(t *testing.T) {
		t.Parallel()

		rng := position.NewRange(position.New(1, 2), position.New(1, 3))
		nope := paths.Current().Child("nope")
		bound := bind(t, niceyaml.NewError("bad", niceyaml.AtPath(nope), niceyaml.AtRange(rng)))

		require.NoError(t, bound.Unresolved())
		assert.Equal(t, "x.yaml:2:3: $.nope: bad", bound.Error())

		p, ok := bound.Path()
		require.True(t, ok)
		assert.Equal(t, paths.Doc().Child("nope"), p)

		got, ok := bound.Range()
		require.True(t, ok)
		assert.Equal(t, rng, got)
	})

	t.Run("a rebased path beside a range joins the base and keeps the range", func(t *testing.T) {
		t.Parallel()

		rng := position.NewRange(position.New(1, 2), position.New(1, 3))
		inner := niceyaml.NewError("bad", niceyaml.AtPath(paths.Current().Child("b")), niceyaml.AtRange(rng))
		bound := bind(t, niceyaml.Rebase(inner, paths.Current().Child("a")))

		assert.Equal(t, "x.yaml:2:3: $.a.b: bad", bound.Error())

		p, ok := bound.Path()
		require.True(t, ok)
		assert.Equal(t, wantB, p)

		got, ok := bound.Range()
		require.True(t, ok)
		assert.Equal(t, rng, got)
	})

	t.Run("a rebased path joins the base", func(t *testing.T) {
		t.Parallel()

		inner := niceyaml.NewError("bad", niceyaml.AtPath(paths.Current().Child("b")))
		bound := bind(t, niceyaml.Rebase(inner, paths.Current().Child("a")))

		assert.Equal(t, "bad", bound.Message())

		p, ok := bound.Path()
		require.True(t, ok)
		assert.Equal(t, wantB, p)
	})

	t.Run("a path from a scoped document reads from the document root", func(t *testing.T) {
		t.Parallel()

		doc, err := src.Document()
		require.NoError(t, err)

		scoped, err := doc.At(paths.Current().Child("a"))
		require.NoError(t, err)

		var bound *niceyaml.SourceError

		require.ErrorAs(t, scoped.Bind(niceyaml.NewError("bad", niceyaml.AtPath(paths.Current().Child("b")))), &bound)

		assert.Equal(t, "x.yaml:2:6: $.a.b: bad", bound.Error())
		assert.Equal(t, "bad", bound.Message())
		assert.Same(t, scoped, bound.Node())

		p, ok := bound.Path()
		require.True(t, ok)
		assert.Equal(t, wantB, p)

		rng, ok := bound.Range()
		require.True(t, ok)
		assert.Equal(t, 1, rng.Start.Line)
	})

	t.Run("text a wrapper added stays without the path", func(t *testing.T) {
		t.Parallel()

		bound := bind(t, fmt.Errorf("ctx: %w", niceyaml.NewError("bad", niceyaml.AtPath(bPath))))

		assert.Equal(t, "ctx: bad", bound.Message())
		assert.Equal(t, "x.yaml:2:6: $.a.b: ctx: bad", bound.Error())

		p, ok := bound.Path()
		require.True(t, ok)
		assert.Equal(t, wantB, p)
	})

	t.Run("a binding that wraps a binding reports the path of the inner one", func(t *testing.T) {
		t.Parallel()

		inner := bind(t, niceyaml.NewError("bad", niceyaml.AtPath(bPath)))

		// A detail that wraps a binding binds as a detail that takes over
		// the inner binding, rather than as the inner binding itself.
		outer := bind(t, niceyaml.NewError("outer", niceyaml.WithDetails(fmt.Errorf("ctx: %w", inner))))
		require.Len(t, outer.Details(), 1)

		child := outer.Details()[0]
		require.NotSame(t, inner, child)
		assert.Equal(t, "ctx: x.yaml:2:6: $.a.b: bad", child.Message())

		p, ok := child.Path()
		require.True(t, ok)
		assert.Equal(t, wantB, p)
	})

	t.Run("an Error with details around a binding keeps the inner message", func(t *testing.T) {
		t.Parallel()

		inner := bind(t, niceyaml.NewError("bad", niceyaml.AtPath(bPath)))

		// The Error adds no text of its own, so the message is the one the
		// inner binding annotates with, and Error still reports the
		// position and path in front of it.
		wrapped := bind(t, niceyaml.Invalid(inner, niceyaml.WithDetails(errors.New("zz"))))
		assert.Equal(t, "bad", wrapped.Message())
		assert.Equal(t, "x.yaml:2:6: $.a.b: bad", wrapped.Error())

		outer := bind(t, niceyaml.NewError("outer", niceyaml.WithDetails(
			niceyaml.Invalid(inner, niceyaml.WithDetails(errors.New("zz"))),
		)))
		require.NotEmpty(t, outer.Details())
		assert.Equal(t, "bad", outer.Details()[0].Message())

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

	second := paths.Doc().Child("servers").Index(1)
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
			err:  niceyaml.NewError("owner is required", niceyaml.AtPath(paths.Current().Child("owner"))),
			want: "cfg.yaml:1:1: $.owner: owner is required",
			near: "$",
			rng:  position.NewRange(position.New(0, 0), position.New(0, 7)),
		},
		"several names below a null": {
			err:  niceyaml.NewError("cert is required", niceyaml.AtPath(paths.Current().Child("tls", "cert", "file"))),
			want: "cfg.yaml:5:1: $.tls.cert.file: cert is required",
			near: "$.tls",
			rng:  position.NewRange(position.New(4, 0), position.New(4, 3)),
		},
		"rebased under the mapping": {
			err: niceyaml.Rebase(
				niceyaml.NewError("name is required", niceyaml.AtPath(paths.Current().Child("name"))),
				second,
			),
			want: "cfg.yaml:4:5: $.servers[1].name: name is required",
			near: "$.servers[1]",
			rng:  position.NewRange(position.New(3, 4), position.New(3, 8)),
		},
		"key the document holds": {
			err: niceyaml.NewError(
				"bad name",
				niceyaml.AtPath(paths.Current().Child("servers").Index(0).Child("name")),
			),
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
		"name in a scalar": {
			err:    niceyaml.NewError("bad", niceyaml.AtPath(paths.Current().Child("note", "text"))),
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
				assert.Equal(t, paths.Current(), near)
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

		err := doc.Bind(niceyaml.NewSummary("2 problems",
			niceyaml.NewError("name is required", niceyaml.AtPath(name)),
			niceyaml.NewError("cert is required", niceyaml.AtPath(paths.Current().Child("tls", "cert"))),
		))

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

		err := scoped.Bind(niceyaml.NewError("name is required", niceyaml.AtPath(paths.Current().Child("name"))))
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

		require.ErrorAs(t, doc.Bind(niceyaml.NewError("outer", niceyaml.WithDetails(
			fmt.Errorf("check: %w", inner),
		))), &outer)
		require.Len(t, outer.Details(), 1)

		near, ok := outer.Details()[0].Nearest()
		require.True(t, ok)
		assert.Equal(t, second, near)
	})

	t.Run("nil has none", func(t *testing.T) {
		t.Parallel()

		var bound *niceyaml.SourceError

		near, ok := bound.Nearest()
		assert.False(t, ok)
		assert.Equal(t, paths.Current(), near)
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
		star := position.NewRange(position.New(0, 8), position.New(0, 9))

		tcs := map[string]struct {
			err error
			// The message, the path of the alias the error is bound at,
			// when it is bound at one, the range it covers, if any, and
			// the reason it keeps.
			want   string
			near   string
			rng    position.Range
			reason error
		}{
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
					niceyaml.NewError("bad", niceyaml.AtPath(paths.Current().Child("port"))),
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
				rng:    position.NewRange(position.New(2, 4), position.New(2, 5)),
				reason: paths.ErrAlias,
			},
			"alias inside an anchor the path reads through": {
				err:    niceyaml.NewError("bad", niceyaml.AtPath(paths.Doc().Child("copy", "tls", "cert"))),
				want:   "app.yaml:5:8: $.copy.tls.cert: bad",
				near:   "$.copy.tls",
				rng:    position.NewRange(position.New(4, 7), position.New(4, 8)),
				reason: paths.ErrAlias,
			},
			"alias inside the anchor it names": {
				err:    niceyaml.NewError("bad", niceyaml.AtPath(paths.Doc().Child("loop", "self", "port"))),
				want:   "app.yaml:8:9: $.loop.self.port: bad",
				near:   "$.loop.self",
				rng:    position.NewRange(position.New(7, 8), position.New(7, 9)),
				reason: paths.ErrAlias,
			},
			"alias below a merge key": {
				err:    niceyaml.NewError("bad", niceyaml.AtPath(paths.Doc().Child("after", "server", "port"))),
				want:   "app.yaml:14:11: $.after.server.port: bad",
				near:   "$.after.server",
				rng:    position.NewRange(position.New(13, 10), position.New(13, 11)),
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

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				err := aliased.Bind(tc.err)
				require.EqualError(t, err, tc.want)

				var bound *niceyaml.SourceError

				require.ErrorAs(t, err, &bound)

				near, ok := bound.Nearest()
				assert.Equal(t, tc.near != "", ok)

				if ok {
					assert.Equal(t, tc.near, near.String())
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

		t.Run("the message keeps the path and the excerpt marks the alias", func(t *testing.T) {
			t.Parallel()

			err := aliased.Bind(niceyaml.NewError("bad", niceyaml.AtPath(server.Child("port"))))

			assert.Equal(t, stringtest.JoinLF(
				"app.yaml:1:9: $.server.port: bad",
				"",
				"   1 | server: *server",
				"     |         ^",
				"   2 | servers:",
			), niceyaml.FormatError(err, 1))

			var bound *niceyaml.SourceError

			require.ErrorAs(t, err, &bound)

			path, ok := bound.Path()
			require.True(t, ok)
			assert.Equal(t, server.Child("port"), path)
		})

		t.Run("a scoped Node binds at an alias below it", func(t *testing.T) {
			t.Parallel()

			scoped := yamltest.At(t, aliased, paths.Doc().Child("base"))

			err := scoped.Bind(niceyaml.NewError("bad", niceyaml.AtPath(paths.Current().Child("tls", "cert"))))
			require.EqualError(t, err, "app.yaml:5:8: $.base.tls.cert: bad")

			var bound *niceyaml.SourceError

			require.ErrorAs(t, err, &bound)

			near, ok := bound.Nearest()
			require.True(t, ok)
			assert.Equal(t, paths.Doc().Child("base", "tls"), near)
		})

		t.Run("a binding that wraps one bound at an alias keeps the alias and the reason", func(t *testing.T) {
			t.Parallel()

			inner := aliased.Bind(niceyaml.NewError("bad", niceyaml.AtPath(server.Child("port"))))

			var outer *niceyaml.SourceError

			require.ErrorAs(t, aliased.Bind(niceyaml.NewError("outer", niceyaml.WithDetails(
				fmt.Errorf("check: %w", inner),
			))), &outer)
			require.Len(t, outer.Details(), 1)

			near, ok := outer.Details()[0].Nearest()
			require.True(t, ok)
			assert.Equal(t, server, near)
			require.ErrorIs(t, outer.Details()[0].Unresolved(), paths.ErrAlias)
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
		return niceyaml.NewError("name is required", niceyaml.AtPath(paths.Current().Child("name")))
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

	first := docs[0].Bind(niceyaml.NewError("bad a", niceyaml.AtPath(paths.Current().Child("a"))))
	second := docs[1].Bind(niceyaml.NewError("bad b", niceyaml.AtPath(paths.Current().Child("b"))))
	other := yamltest.Bind(
		t,
		niceyaml.NewSourceFromString("c: 3\n"),
		niceyaml.NewError("outer", niceyaml.WithDetails(first)),
	)
	tree := docs[0].Bind(niceyaml.NewSummary("2 violations",
		niceyaml.NewError("bad a", niceyaml.AtPath(paths.Current().Child("a"))),
		niceyaml.NewError("missing", niceyaml.AtPath(paths.Current().Child("nope").Index(0))),
	))
	nested := yamltest.Bind(t, niceyaml.NewSourceFromString("a:\n  b: 1\n  c: 2\n"), niceyaml.NewSummary(
		"2 violations",
		niceyaml.NewError("bad b", niceyaml.AtPath(paths.Current().Child("a", "b"))),
		niceyaml.NewError("bad a", niceyaml.AtPath(paths.Current().Child("a")), niceyaml.WithDetails(
			niceyaml.NewError("bad c", niceyaml.AtPath(paths.Current().Child("a", "c"))),
		)),
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
			err:  errors.Join(first, docs[0].Bind(niceyaml.NewError("outer", niceyaml.WithDetails(first)))),
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

	err := niceyaml.NewSummary("2 violations",
		niceyaml.NewError("bad a", niceyaml.AtPath(paths.Current().Child("a"))),
		niceyaml.NewError("bad b", niceyaml.AtPath(paths.Current().Child("b"))),
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
			want:   "2 violations\n|-- @.a: bad a\n`-- @.b: bad b",
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

		err := niceyaml.NewSummary("2 schema violations",
			niceyaml.NewError("bad x", niceyaml.AtPath(paths.Current().Child("x"))),
			niceyaml.NewError("bad y", niceyaml.AtPath(paths.Current().Child("y"))),
		)

		want := stringtest.JoinLF(
			"2 schema violations",
			"|-- @.x: bad x",
			"`-- @.y: bad y",
		)

		summary, ok := errors.AsType[*niceyaml.Error](err)
		require.True(t, ok)

		assert.Equal(t, want, summary.LogValue().String())
		assert.Equal(t, want, logged(t, err))
	})

	t.Run("an error with nothing nested logs its message", func(t *testing.T) {
		t.Parallel()

		err := niceyaml.NewError("bad x", niceyaml.AtPath(paths.Current().Child("x")))

		assert.Equal(t, "@.x: bad x", logged(t, err))
	})

	t.Run("a wrapper around an unbound error logs its message alone", func(t *testing.T) {
		t.Parallel()

		// The wrapper is no LogValuer, so a handler logs the message the
		// wrapper wrote, which holds no path.
		wrapped := fmt.Errorf("check: %w", niceyaml.NewError("bad x", niceyaml.AtPath(paths.Current().Child("x"))))

		assert.Equal(t, "check: bad x", logged(t, wrapped))

		var buf bytes.Buffer

		slog.New(slog.NewTextHandler(&buf, nil)).Error("load", slog.Any("err", wrapped))

		assert.Contains(t, buf.String(), `err="check: bad x"`)
	})

	t.Run("a join of typed-nil errors logs its message", func(t *testing.T) {
		t.Parallel()

		var nilErr *niceyaml.Error

		err := wrapError(t, errors.Join(nilErr, nilErr))

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

		err := yamltest.Bind(t, source, niceyaml.NewSummary("2 schema violations",
			niceyaml.NewError("bad x", niceyaml.AtPath(paths.Current().Child("x"))),
			niceyaml.NewError("bad y", niceyaml.AtPath(paths.Current().Child("y"))),
		))

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

		err := yamltest.Bind(t, source, niceyaml.NewError("bad x", niceyaml.AtPath(paths.Current().Child("x"))))
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
	licenseRange := position.NewRange(position.New(3, 9), position.New(3, 16))

	errStat := fmt.Errorf("stat license: %w", fs.ErrPermission)

	document := func(t *testing.T) *niceyaml.Node {
		t.Helper()

		return yamltest.FirstDocument(t, input, niceyaml.WithName("cfg.yaml"))
	}

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

				// NoError compares the interface with nil, which a nil
				// *Error inside it would fail.
				require.NoError(t, niceyaml.Invalid(tc.err))
				require.NoError(t, niceyaml.Invalid(tc.err,
					niceyaml.AtPath(licensePath),
					niceyaml.AtRange(licenseRange),
					niceyaml.WithDetails(errors.New("named here")),
				))
			})
		}
	})

	t.Run("wraps the result of a check as it is", func(t *testing.T) {
		t.Parallel()

		tcs := map[string]struct {
			check func() error
			want  string
		}{
			"valid value": {
				check: func() error { return nil },
			},
			"invalid value": {
				check: func() error { return errors.New("closes before it opens") },
				want:  "cfg.yaml:2:3: $.hours: closes before it opens",
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				hours := yamltest.At(t, document(t), hoursPath)
				err := hours.Bind(niceyaml.Invalid(tc.check()))

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

				path, ok := wrapped.Path()
				require.True(t, ok)
				assert.Equal(t, licensePath, path)

				require.ErrorIs(t, wrapped, tc.is)
				assert.Equal(t, tc.want, niceyaml.IsInvalid(wrapped), "unbound")
				assert.Equal(t, tc.want, niceyaml.IsInvalid(document(t).Bind(wrapped)), "bound")
			})
		}
	})

	t.Run("options on a join locate the heading and no branch", func(t *testing.T) {
		t.Parallel()

		joined := errors.Join(errStat, errors.New("too many keys"))

		tcs := map[string]struct {
			err  error
			want string
		}{
			"path": {
				err: niceyaml.Invalid(joined, niceyaml.AtPath(licensePath)),
				want: stringtest.JoinLF(
					"cfg.yaml:4:10: $.license:",
					"cfg.yaml: stat license: permission denied",
					"cfg.yaml: too many keys",
				),
			},
			"position": {
				err: niceyaml.Invalid(joined, niceyaml.AtPosition(licenseRange.Start)),
				want: stringtest.JoinLF(
					"cfg.yaml: stat license: permission denied",
					"cfg.yaml: too many keys",
				),
			},
			"range": {
				err: niceyaml.Invalid(joined, niceyaml.AtRange(licenseRange)),
				want: stringtest.JoinLF(
					"cfg.yaml: stat license: permission denied",
					"cfg.yaml: too many keys",
				),
			},
			"Rebase moves each branch under the path": {
				err: niceyaml.Invalid(niceyaml.Rebase(joined, licensePath)),
				want: stringtest.JoinLF(
					"cfg.yaml:4:10: $.license: stat license: permission denied",
					"cfg.yaml:4:10: $.license: too many keys",
				),
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				err := document(t).Bind(tc.err)

				require.EqualError(t, err, tc.want)
				assert.True(t, niceyaml.IsInvalid(err))
			})
		}
	})
}

func TestPlace(t *testing.T) {
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
	licenseStart := position.New(3, 9)
	licenseRange := position.NewRange(licenseStart, position.New(3, 16))

	errStat := fmt.Errorf("stat license: %w", fs.ErrPermission)

	document := func(t *testing.T) *niceyaml.Node {
		t.Helper()

		return yamltest.FirstDocument(t, input, niceyaml.WithName("cfg.yaml"))
	}

	t.Run("nothing to place returns nil", func(t *testing.T) {
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

				// NoError compares the interface with nil, which a nil
				// *Error inside it would fail.
				require.NoError(t, niceyaml.Place(tc.err))
				require.NoError(t, niceyaml.Place(tc.err,
					niceyaml.AtPath(licensePath),
					niceyaml.AtRange(licenseRange),
					niceyaml.WithDetails(errors.New("named here")),
				))
			})
		}
	})

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
			"Place": {
				err: niceyaml.Place(errStat, niceyaml.AtPath(licensePath)),
			},
			"Rebase": {
				err: niceyaml.Rebase(errStat, licensePath),
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				err := document(t).Bind(tc.err)

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
				opts: []niceyaml.ErrorOption{niceyaml.AtPosition(licenseStart)},
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

				placed := niceyaml.Place(errStat, tc.opts...)
				want := wrapError(t, errStat, tc.opts...)

				// The Error carries what the same options give Invalid.
				got, ok := errors.AsType[*niceyaml.Error](placed)
				require.True(t, ok)

				assert.Same(t, errStat, got.Cause())
				assert.Equal(t, want.Details(), got.Details())

				gotPath, gotOK := got.Path()
				wantPath, wantOK := want.Path()
				assert.Equal(t, wantOK, gotOK)
				assert.Equal(t, wantPath, gotPath)

				gotPos, gotOK := got.Position()
				wantPos, wantOK := want.Position()
				assert.Equal(t, wantOK, gotOK)
				assert.Equal(t, wantPos, gotPos)

				gotRange, gotOK := got.Range()
				wantRange, wantOK := want.Range()
				assert.Equal(t, wantOK, gotOK)
				assert.Equal(t, wantRange, gotRange)

				err := document(t).Bind(placed)

				require.EqualError(t, err, tc.want)
				assert.False(t, niceyaml.IsInvalid(placed), "unbound")
				assert.False(t, niceyaml.IsInvalid(err), "bound")
			})
		}
	})

	t.Run("declares nothing and clears nothing", func(t *testing.T) {
		t.Parallel()

		badOpen := niceyaml.NewError("opens too late", niceyaml.AtPath(openPath))

		tcs := map[string]struct {
			err  error
			want bool
		}{
			"I/O error with a located detail from NewError": {
				// The detail explains the I/O error and decides nothing.
				err: niceyaml.Place(errStat,
					niceyaml.AtRange(licenseRange),
					niceyaml.WithDetails(niceyaml.NewError("named here", niceyaml.AtPath(openPath))),
				),
			},
			"NewError": {
				err:  niceyaml.Place(badOpen, niceyaml.AtRange(licenseRange)),
				want: true,
			},
			"fmt wrapper around NewError": {
				err:  niceyaml.Place(fmt.Errorf("check hours: %w", badOpen), niceyaml.AtRange(licenseRange)),
				want: true,
			},
			"Invalid of an I/O error": {
				err:  niceyaml.Place(niceyaml.Invalid(errStat), niceyaml.AtRange(licenseRange)),
				want: true,
			},
			"Invalid around Place": {
				err:  niceyaml.Invalid(niceyaml.Place(errStat, niceyaml.AtRange(licenseRange))),
				want: true,
			},
			"Rebase around Place": {
				err: niceyaml.Rebase(
					niceyaml.Place(errStat, niceyaml.AtPath(paths.Current().Child("open"))),
					hoursPath,
				),
			},
			"canceled context": {
				err: niceyaml.Place(context.Canceled, niceyaml.AtRange(licenseRange)),
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				assert.Equal(t, tc.want, niceyaml.IsInvalid(tc.err), "unbound")
				assert.Equal(t, tc.want, niceyaml.IsInvalid(document(t).Bind(tc.err)), "bound")
			})
		}
	})

	t.Run("a scoped Node binds a placed error at its value", func(t *testing.T) {
		t.Parallel()

		hours := yamltest.At(t, document(t), hoursPath)

		tcs := map[string]struct {
			err  error
			want string
		}{
			"no location": {
				err:  niceyaml.Place(errStat),
				want: "cfg.yaml:2:3: $.hours: stat license: permission denied",
			},
			"path from the value": {
				err:  niceyaml.Place(errStat, niceyaml.AtPath(paths.Current().Child("close"))),
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

	t.Run("a SelfValidator marks a placed error invalid", func(t *testing.T) {
		t.Parallel()

		cfg := checkedConfig{Hours: selfChecked{check: func() error {
			return niceyaml.Place(errStat, niceyaml.AtPath(paths.Current().Child("close")))
		}}}

		err := document(t).DecodeInto(t.Context(), &cfg)

		require.EqualError(t, err, "cfg.yaml:3:10: $.hours.close: stat license: permission denied")
		require.ErrorIs(t, err, fs.ErrPermission)
		assert.True(t, niceyaml.IsInvalid(err))
	})

	t.Run("options on a join locate the heading and no branch", func(t *testing.T) {
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
				err: niceyaml.Place(joined),
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
				err: niceyaml.Place(joined, niceyaml.AtPath(licensePath)),
				want: stringtest.JoinLF(
					"cfg.yaml:4:10: $.license:",
					"cfg.yaml:2:9: $.hours.open: opens too late",
					"cfg.yaml: stat license: permission denied",
				),
				problems: []string{
					"2:9: $.hours.open: opens too late true",
					"stat license: permission denied false",
				},
			},
			"range": {
				err: niceyaml.Place(joined, niceyaml.AtRange(licenseRange)),
				want: stringtest.JoinLF(
					"cfg.yaml:2:9: $.hours.open: opens too late",
					"cfg.yaml: stat license: permission denied",
				),
				problems: []string{
					"cfg.yaml:2:9: $.hours.open: opens too late true",
					"cfg.yaml: stat license: permission denied false",
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

				err := document(t).Bind(tc.err)

				require.EqualError(t, err, tc.want)
				assert.False(t, niceyaml.IsInvalid(err))

				var problems []string

				for problem := range niceyaml.NewErrorTree(err).Problems() {
					problems = append(problems, fmt.Sprintf("%s %t", problem.Text, problem.Invalid()))
				}

				assert.Equal(t, tc.problems, problems)
			})
		}
	})
}

func TestError_Is(t *testing.T) {
	t.Parallel()

	located := niceyaml.NewError("unknown name", niceyaml.AtPath(paths.Current().Child("name")))

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

	unknownName := niceyaml.ValidatorFunc(func(context.Context, *niceyaml.Node) error {
		return niceyaml.NewError("unknown name", niceyaml.AtPath(namePath))
	})
	failing := niceyaml.ValidatorFunc(func(context.Context, *niceyaml.Node) error {
		return ioErr
	})
	unlocated := niceyaml.ValidatorFunc(func(context.Context, *niceyaml.Node) error {
		return errors.New("closes before it opens")
	})
	declared := niceyaml.ValidatorFunc(func(context.Context, *niceyaml.Node) error {
		return niceyaml.NewError("closes before it opens")
	})
	joined := niceyaml.ValidatorFunc(func(context.Context, *niceyaml.Node) error {
		return niceyaml.Invalid(errors.Join(errors.New("opens too late"), errors.New("closes too early")))
	})
	rebased := niceyaml.ValidatorFunc(func(context.Context, *niceyaml.Node) error {
		return niceyaml.Rebase(fmt.Errorf("stat license: %w", fs.ErrPermission), namePath)
	})
	contextual := niceyaml.ValidatorFunc(func(ctx context.Context, _ *niceyaml.Node) error {
		return ctx.Err()
	})
	denied := niceyaml.ValidatorFunc(func(context.Context, *niceyaml.Node) error {
		return fmt.Errorf("read names.db: %w", fs.ErrPermission)
	})
	decoding := niceyaml.ValidatorFunc(func(ctx context.Context, n *niceyaml.Node) error {
		var target struct {
			Port []int `yaml:"port"`
		}

		return n.DecodeInto(ctx, &target)
	})

	placed := niceyaml.ValidatorFunc(func(context.Context, *niceyaml.Node) error {
		return niceyaml.Place(fmt.Errorf("stat license: %w", fs.ErrPermission), niceyaml.AtPath(namePath))
	})

	// The explained error is an I/O error placed at the name, with a
	// located detail the document is at fault for.
	explained := func(*testing.T) error {
		return niceyaml.Place(
			fmt.Errorf("stat license: %w", fs.ErrPermission),
			niceyaml.AtRange(position.NewRange(position.New(0, 6), position.New(0, 10))),
			niceyaml.WithDetails(niceyaml.NewError("license named here", niceyaml.AtPath(namePath))),
		)
	}

	canceled := func(t *testing.T) context.Context {
		t.Helper()

		ctx, cancel := context.WithCancel(t.Context())
		cancel()

		return ctx
	}

	// The document names its schema in fsys, which holds the files of the
	// case.
	directive := func(t *testing.T, fsys fs.FS, ref string) *niceyaml.Node {
		t.Helper()

		return yamltest.FirstDocument(t,
			"# yaml-language-server: $schema="+ref+"\nname: cafe\n",
			niceyaml.WithFilePath("c.yaml"), niceyaml.WithFS(fsys),
		)
	}

	byDirective := schema.NewRegistry(schema.WithResolvers(schema.Directive()))

	validated := func(t *testing.T, v niceyaml.Validator) error {
		t.Helper()

		return yamltest.FirstDocument(t, input).Validate(t.Context(), v)
	}

	scopedValidated := func(t *testing.T, v niceyaml.Validator) error {
		t.Helper()

		return yamltest.At(t, yamltest.FirstDocument(t, input), hoursPath).Validate(t.Context(), v)
	}

	selfValidated := func(t *testing.T, check func() error) error {
		t.Helper()

		cfg := checkedConfig{Hours: selfChecked{check: check}}

		return yamltest.FirstDocument(t, input).DecodeInto(t.Context(), &cfg)
	}

	decoded := func(t *testing.T, text string, opts ...niceyaml.DecodeOption) error {
		t.Helper()

		_, err := niceyaml.NewSourceFromString(text).Decode[invalidConfig](t.Context(), opts...)

		return err
	}

	// The errors of boundRead and boundPlain are no fault of the document,
	// and each is bound at a value already.
	boundRead := func(t *testing.T) error {
		t.Helper()

		return yamltest.FirstDocument(t, input).Bind(niceyaml.Rebase(ioErr, namePath))
	}

	boundPlain := func(t *testing.T) error {
		t.Helper()

		doc := yamltest.FirstDocument(t, input)

		return yamltest.At(t, doc, hoursPath).Bind(errors.New("closes before it opens"))
	}

	// Each case builds the error of one origin. The err field pins the
	// origin, so a case that stops reaching it fails rather than passing
	// on another error.
	tcs := map[string]struct {
		build func(t *testing.T) error
		err   error
		want  bool
	}{
		"syntax error": {
			build: func(t *testing.T) error {
				t.Helper()

				return decoded(t, "name: [cafe\nport: 80\n")
			},
			err:  niceyaml.ErrSyntax,
			want: true,
		},
		"decode type mismatch": {
			build: func(t *testing.T) error {
				t.Helper()

				return decoded(t, "name: cafe\nport: eighty\n")
			},
			err:  niceyaml.ErrDecode,
			want: true,
		},
		"unknown field": {
			build: func(t *testing.T) error {
				t.Helper()

				return decoded(t, "name: cafe\nprot: 80\n", niceyaml.WithDisallowUnknownFields(true))
			},
			err:  niceyaml.ErrDecode,
			want: true,
		},
		"schema violation": {
			build: func(t *testing.T) error {
				t.Helper()

				return validated(t, portSchema)
			},
			want: true,
		},
		"schema violation with no location": {
			build: func(t *testing.T) error {
				t.Helper()

				return validated(t, niceyaml.ValidatorFunc(func(context.Context, *niceyaml.Node) error {
					return &schema.Violation{Keyword: "type", Message: `expected "object", got "string"`}
				}))
			},
			want: true,
		},
		"SelfValidator NewError with AtPath": {
			build: func(t *testing.T) error {
				t.Helper()

				return selfValidated(t, func() error {
					return niceyaml.NewError("closes before it opens", niceyaml.AtPath(closePath))
				})
			},
			want: true,
		},
		"SelfValidator errors.New on a field": {
			build: func(t *testing.T) error {
				t.Helper()

				return selfValidated(t, func() error {
					return errors.New("closes before it opens")
				})
			},
			want: true,
		},
		"SelfValidator errors.New at the root": {
			build: func(t *testing.T) error {
				t.Helper()

				v := selfChecked{check: func() error {
					return errors.New("closes before it opens")
				}}

				return yamltest.FirstDocument(t, input).DecodeInto(t.Context(), &v)
			},
			want: true,
		},
		"SelfValidator summary of errors with no location": {
			build: func(t *testing.T) error {
				t.Helper()

				return selfValidated(t, func() error {
					return niceyaml.NewSummary(
						"2 problems",
						errors.New("opens too late"),
						errors.New("closes too early"),
					)
				})
			},
			want: true,
		},
		"SelfValidator join of errors with no location": {
			build: func(t *testing.T) error {
				t.Helper()

				return selfValidated(t, func() error {
					return errors.Join(errors.New("opens too late"), errors.New("closes too early"))
				})
			},
			want: true,
		},
		"SelfValidator I/O error": {
			build: func(t *testing.T) error {
				t.Helper()

				return selfValidated(t, func() error {
					return fmt.Errorf("load holidays: %w", ioErr)
				})
			},
			err:  fs.ErrNotExist,
			want: true,
		},
		"SelfValidator bound I/O error": {
			// A binding is an error a SelfValidator returns as any other
			// is.
			build: func(t *testing.T) error {
				t.Helper()

				return selfValidated(t, func() error {
					return boundRead(t)
				})
			},
			err:  fs.ErrNotExist,
			want: true,
		},
		"SelfValidator join of bound errors": {
			build: func(t *testing.T) error {
				t.Helper()

				return selfValidated(t, func() error {
					return errors.Join(boundRead(t), boundPlain(t))
				})
			},
			err:  fs.ErrNotExist,
			want: true,
		},
		"Validator NewError with AtPath": {
			build: func(t *testing.T) error {
				t.Helper()

				return validated(t, unknownName)
			},
			want: true,
		},
		"Validator NewError with AtPath of the root": {
			build: func(t *testing.T) error {
				t.Helper()

				return scopedValidated(t, niceyaml.ValidatorFunc(func(context.Context, *niceyaml.Node) error {
					return niceyaml.NewError("closes before it opens", niceyaml.AtPath(paths.Current()))
				}))
			},
			want: true,
		},
		"Validator NewError with AtPath bound through Bind": {
			build: func(t *testing.T) error {
				t.Helper()

				return scopedValidated(t, niceyaml.ValidatorFunc(func(_ context.Context, n *niceyaml.Node) error {
					return n.Bind(niceyaml.NewError("closes before it opens", niceyaml.AtPath(closePath)))
				}))
			},
			want: true,
		},
		"Validator NewError with no location at the root": {
			build: func(t *testing.T) error {
				t.Helper()

				return validated(t, declared)
			},
			want: true,
		},
		"Validator NewError with no location on a scoped Node": {
			build: func(t *testing.T) error {
				t.Helper()

				return scopedValidated(t, declared)
			},
			want: true,
		},
		"Validator Invalid of a join at the root": {
			build: func(t *testing.T) error {
				t.Helper()

				return validated(t, joined)
			},
			want: true,
		},
		"Validator Invalid of a join on a scoped Node": {
			build: func(t *testing.T) error {
				t.Helper()

				return scopedValidated(t, joined)
			},
			want: true,
		},
		"Validator join of errors.New": {
			build: func(t *testing.T) error {
				t.Helper()

				return validated(t, niceyaml.ValidatorFunc(func(context.Context, *niceyaml.Node) error {
					return errors.Join(errors.New("opens too late"), errors.New("closes too early"))
				}))
			},
		},
		"Validator errors.New at the root": {
			build: func(t *testing.T) error {
				t.Helper()

				return validated(t, unlocated)
			},
		},
		"Validator errors.New on a scoped Node": {
			build: func(t *testing.T) error {
				t.Helper()

				return scopedValidated(t, unlocated)
			},
		},
		"Validator Bind of errors.New on a scoped Node": {
			build: func(t *testing.T) error {
				t.Helper()

				return scopedValidated(t, niceyaml.ValidatorFunc(func(_ context.Context, n *niceyaml.Node) error {
					return n.Bind(errors.New("closes before it opens"))
				}))
			},
		},
		"Validator Rebase of an I/O error": {
			build: func(t *testing.T) error {
				t.Helper()

				return validated(t, rebased)
			},
			err: fs.ErrPermission,
		},
		"Validator Place of an I/O error at the root": {
			build: func(t *testing.T) error {
				t.Helper()

				return validated(t, placed)
			},
			err: fs.ErrPermission,
		},
		"Validator Place of an I/O error on a scoped Node": {
			build: func(t *testing.T) error {
				t.Helper()

				return scopedValidated(t, niceyaml.ValidatorFunc(func(context.Context, *niceyaml.Node) error {
					return niceyaml.Place(ioErr)
				}))
			},
			err: fs.ErrNotExist,
		},
		"Validator Place of NewError": {
			// Place declares nothing and clears nothing.
			build: func(t *testing.T) error {
				t.Helper()

				return validated(t, niceyaml.ValidatorFunc(func(context.Context, *niceyaml.Node) error {
					return niceyaml.Place(niceyaml.NewError("unknown name"), niceyaml.AtPath(namePath))
				}))
			},
			want: true,
		},
		"SelfValidator Place of an I/O error": {
			// A decode marks every error a SelfValidator returns.
			build: func(t *testing.T) error {
				t.Helper()

				return selfValidated(t, func() error {
					return niceyaml.Place(ioErr, niceyaml.AtPath(closePath))
				})
			},
			err:  fs.ErrNotExist,
			want: true,
		},
		"Validator error with no location and a located detail": {
			build: func(t *testing.T) error {
				t.Helper()

				return validated(t, niceyaml.ValidatorFunc(func(context.Context, *niceyaml.Node) error {
					return niceyaml.NewError("name taken",
						niceyaml.WithDetails(niceyaml.NewError("first used here", niceyaml.AtPath(namePath))))
				}))
			},
			want: true,
		},
		"schema that does not load": {
			build: func(t *testing.T) error {
				t.Helper()

				return directive(t, fstest.MapFS{}, "./missing.json").Validate(t.Context(), byDirective)
			},
			err: schema.ErrLoad,
		},
		"schema that does not compile": {
			build: func(t *testing.T) error {
				t.Helper()

				bad := fstest.MapFS{"bad.json": {Data: []byte(`{"type": 12}`)}}

				return directive(t, bad, "./bad.json").Validate(t.Context(), byDirective)
			},
			err: schema.ErrCompile,
		},
		"$ref that does not resolve": {
			build: func(t *testing.T) error {
				t.Helper()

				return validated(t, schema.MustCompile(
					[]byte(`{"properties": {"port": {"$ref": "https://example.invalid/nope.json"}}}`),
				))
			},
			err: schema.ErrValidate,
		},
		"no matching schema with reasons": {
			build: func(t *testing.T) error {
				t.Helper()

				return validated(t, schema.NewRegistry(schema.WithResolvers(schema.Directive())))
			},
			err:  schema.ErrNoDirective,
			want: true,
		},
		"no matching schema without reasons": {
			build: func(t *testing.T) error {
				t.Helper()

				return validated(t, schema.NewRegistry())
			},
			err:  schema.ErrNoMatch,
			want: true,
		},
		"canceled context in a validator": {
			build: func(t *testing.T) error {
				t.Helper()

				return yamltest.FirstDocument(t, input).Validate(canceled(t), contextual)
			},
			err: context.Canceled,
		},
		"canceled context in a validator on a scoped Node": {
			build: func(t *testing.T) error {
				t.Helper()

				return yamltest.At(t, yamltest.FirstDocument(t, input), hoursPath).Validate(canceled(t), contextual)
			},
			err: context.Canceled,
		},
		"canceled context in a registry": {
			build: func(t *testing.T) error {
				t.Helper()

				return directive(t, fstest.MapFS{}, "./missing.json").Validate(canceled(t), byDirective)
			},
			err: context.Canceled,
		},
		"canceled context in the self-validation walk": {
			build: func(t *testing.T) error {
				t.Helper()

				cfg := checkedConfig{Hours: selfChecked{check: func() error {
					return errors.New("closes before it opens")
				}}}

				return yamltest.FirstDocument(t, input).SelfValidate(canceled(t), &cfg)
			},
			err: context.Canceled,
		},
		"SelfValidator context error": {
			build: func(t *testing.T) error {
				t.Helper()

				return selfValidated(t, func() error {
					return context.Canceled
				})
			},
			err: context.Canceled,
		},
		"Validator Invalid of a canceled check": {
			build: func(t *testing.T) error {
				t.Helper()

				hours := yamltest.At(t, yamltest.FirstDocument(t, input), hoursPath)

				return hours.Validate(canceled(t), niceyaml.ValidatorFunc(
					func(ctx context.Context, n *niceyaml.Node) error {
						return n.Bind(niceyaml.Invalid(ctx.Err()))
					},
				))
			},
			err: context.Canceled,
		},
		"Validator I/O error at the root": {
			build: func(t *testing.T) error {
				t.Helper()

				return validated(t, failing)
			},
			err: fs.ErrNotExist,
		},
		"Validator I/O error on a scoped Node": {
			build: func(t *testing.T) error {
				t.Helper()

				return scopedValidated(t, failing)
			},
			err: fs.ErrNotExist,
		},
		"Validator Invalid of an I/O error": {
			build: func(t *testing.T) error {
				t.Helper()

				return validated(t, niceyaml.ValidatorFunc(func(context.Context, *niceyaml.Node) error {
					return niceyaml.Invalid(ioErr, niceyaml.AtPath(namePath))
				}))
			},
			err:  fs.ErrNotExist,
			want: true,
		},
		"multiple documents": {
			build: func(t *testing.T) error {
				t.Helper()

				return decoded(t, "name: cafe\n---\nname: bar\n")
			},
			err:  niceyaml.ErrMultipleDocuments,
			want: true,
		},
		"Node.At of a missing key": {
			build: func(t *testing.T) error {
				t.Helper()

				_, err := yamltest.FirstDocument(t, input).At(hoursPath.Child("lunch"))

				return err //nolint:wrapcheck // The test inspects the error of the call.
			},
			err:  paths.ErrNotFound,
			want: true,
		},
		"Node.At of a missing index": {
			build: func(t *testing.T) error {
				t.Helper()

				_, err := yamltest.FirstDocument(t, "items: []\n").At(paths.Current().Child("items").Index(0))

				return err //nolint:wrapcheck // The test inspects the error of the call.
			},
			err:  paths.ErrNotFound,
			want: true,
		},
		"Node.At of a key of a scalar": {
			build: func(t *testing.T) error {
				t.Helper()

				_, err := yamltest.FirstDocument(t, input).At(namePath.Child("first"))

				return err //nolint:wrapcheck // The test inspects the error of the call.
			},
			err:  paths.ErrNotFound,
			want: true,
		},
		"Node.Ranges of a missing index": {
			build: func(t *testing.T) error {
				t.Helper()

				_, err := yamltest.FirstDocument(t, "items: []\n").Ranges(paths.Current().Child("items").Index(0))

				return err //nolint:wrapcheck // The test inspects the error of the call.
			},
			err:  paths.ErrNotFound,
			want: true,
		},
		"Node.At in a document with no content": {
			build: func(t *testing.T) error {
				t.Helper()

				_, err := yamltest.FirstDocument(t, "# no content\n").At(paths.Current().Child("items"))

				return err //nolint:wrapcheck // The test inspects the error of the call.
			},
			err:  paths.ErrNoDocument,
			want: true,
		},
		"Node.At of a wildcard path": {
			build: func(t *testing.T) error {
				t.Helper()

				_, err := yamltest.FirstDocument(t, input).At(paths.Current().ChildAll())

				return err //nolint:wrapcheck // The test inspects the error of the call.
			},
			err: paths.ErrWildcard,
		},
		"UnmarshalYAML I/O error": {
			build: func(t *testing.T) error {
				t.Helper()

				_, err := yamltest.FirstDocument(t, "v: x\n").Decode[struct {
					V fetchingUnmarshaler `yaml:"v"`
				}](t.Context())

				return err
			},
			err:  fs.ErrNotExist,
			want: true,
		},
		"UnmarshalYAML panic": {
			build: func(t *testing.T) error {
				t.Helper()

				_, err := yamltest.FirstDocument(t, "v: x\n").Decode[struct {
					V panickingUnmarshaler `yaml:"v"`
				}](t.Context())

				return err
			},
			err:  niceyaml.ErrDecode,
			want: true,
		},
		"target type the decoder refuses": {
			build: func(t *testing.T) error {
				t.Helper()

				_, err := yamltest.FirstDocument(t, "a: 1\n").Decode[struct {
					A int `yaml:"a"`
					B int `yaml:"a"`
				}](t.Context())

				return err
			},
			err:  niceyaml.ErrDecode,
			want: true,
		},
		"decode target that is no pointer": {
			build: func(t *testing.T) error {
				t.Helper()

				return yamltest.FirstDocument(t, input).DecodeInto(t.Context(), (*invalidConfig)(nil))
			},
			err: niceyaml.ErrDecodeTarget,
		},
		"self-validation target that is nil": {
			build: func(t *testing.T) error {
				t.Helper()

				return yamltest.FirstDocument(t, input).SelfValidate(t.Context(), nil)
			},
			err: niceyaml.ErrSelfValidateTarget,
		},
		"excessive aliasing": {
			build: func(t *testing.T) error {
				t.Helper()

				_, err := yamltest.FirstDocument(t, yamltest.AliasLevels(5)).Decode[any](t.Context())

				return err
			},
			err:  niceyaml.ErrExcessiveAliasing,
			want: true,
		},
		"excessive aliasing a schema refuses": {
			build: func(t *testing.T) error {
				t.Helper()

				return yamltest.FirstDocument(t, yamltest.AliasLevels(5)).Validate(t.Context(), portSchema)
			},
			err:  niceyaml.ErrExcessiveAliasing,
			want: true,
		},
		"excessive aliasing a schema refuses in a decoded value": {
			build: func(t *testing.T) error {
				t.Helper()

				trusted := yamltest.FirstDocument(t, yamltest.AliasLevels(5), niceyaml.WithAliasLimit(false))

				data, err := trusted.Decode[any](t.Context())
				require.NoError(t, err)

				return portSchema.ValidateValue(t.Context(), data)
			},
			err:  niceyaml.ErrExcessiveAliasing,
			want: true,
		},
		"excessive aliasing a content matcher refuses": {
			build: func(t *testing.T) error {
				t.Helper()

				routed := schema.NewRegistry(schema.WithResolvers(schema.When(
					matcher.Content(paths.Doc().Child("a"), "x"),
					portSchema.Ref(),
				)))

				return yamltest.FirstDocument(t, yamltest.AliasLevels(5)).Validate(t.Context(), routed)
			},
			err:  schema.ErrResolve,
			want: true,
		},
		"excessive aliasing of a path": {
			// The path selects far more nodes than the document holds,
			// which is no fault of the document.
			build: func(t *testing.T) error {
				t.Helper()

				_, err := yamltest.FirstDocument(t, yamltest.AliasLevels(5)).
					Nodes(paths.MustParse("$.a[5][*][*][*][*][*]"))

				return err //nolint:wrapcheck // The test inspects the error of the call.
			},
			err: niceyaml.ErrExcessiveAliasing,
		},
		"source that does not read": {
			build: func(*testing.T) error {
				_, err := niceyaml.NewSourceFromFS(fstest.MapFS{}, "cafe.yaml")

				return err
			},
			err: fs.ErrNotExist,
		},
		"join of a violation and a schema that does not load": {
			build: func(t *testing.T) error {
				t.Helper()

				return errors.Join(
					validated(t, portSchema),
					directive(t, fstest.MapFS{}, "./missing.json").Validate(t.Context(), byDirective),
				)
			},
			err: schema.ErrLoad,
		},
		"MultiValidator of an invalid problem and an I/O error": {
			build: func(t *testing.T) error {
				t.Helper()

				return validated(t, niceyaml.MultiValidator(unknownName, failing))
			},
			err: fs.ErrNotExist,
		},
		"join of a syntax error and a violation": {
			build: func(t *testing.T) error {
				t.Helper()

				return errors.Join(decoded(t, "name: [cafe\n"), validated(t, portSchema))
			},
			err:  niceyaml.ErrSyntax,
			want: true,
		},
		"join of a syntax error and a source that does not read": {
			// The error matches ErrSyntax through one problem, and the
			// other is no fault of the document.
			build: func(t *testing.T) error {
				t.Helper()

				_, err := niceyaml.NewSourceFromFS(fstest.MapFS{}, "cafe.yaml")

				return errors.Join(decoded(t, "name: [cafe\n"), err)
			},
			err: niceyaml.ErrSyntax,
		},
		"unbound located error": {
			build: func(*testing.T) error {
				return niceyaml.NewError("unknown name", niceyaml.AtPath(namePath))
			},
			want: true,
		},
		"unbound Invalid of a join": {
			build: func(*testing.T) error {
				return niceyaml.Invalid(errors.Join(errors.New("opens too late"), ioErr))
			},
			err:  fs.ErrNotExist,
			want: true,
		},
		"unbound Rebase of Invalid of a join": {
			build: func(*testing.T) error {
				return niceyaml.Rebase(
					niceyaml.Invalid(errors.Join(errors.New("opens too late"), ioErr)),
					hoursPath,
				)
			},
			err:  fs.ErrNotExist,
			want: true,
		},
		"unbound Invalid of a join of bound errors": {
			// A branch that is bound already is a problem the Error
			// heads, as any other branch is.
			build: func(t *testing.T) error {
				t.Helper()

				return niceyaml.Invalid(errors.Join(boundRead(t), boundPlain(t)))
			},
			err:  fs.ErrNotExist,
			want: true,
		},
		"bound Invalid of a join of a bound error and a plain one": {
			build: func(t *testing.T) error {
				t.Helper()

				return yamltest.FirstDocument(t, input).Bind(
					niceyaml.Invalid(errors.Join(boundRead(t), errors.New("opens too late"))),
				)
			},
			err:  fs.ErrNotExist,
			want: true,
		},
		"bound Rebase of Invalid of a summary of bound errors": {
			build: func(t *testing.T) error {
				t.Helper()

				return yamltest.FirstDocument(t, input).Bind(niceyaml.Rebase(
					niceyaml.Invalid(niceyaml.NewSummary("2 problems", boundRead(t), boundPlain(t))),
					hoursPath,
				))
			},
			err:  fs.ErrNotExist,
			want: true,
		},
		"unbound join of bound errors": {
			// No Invalid heads the join, so each binding answers for
			// itself.
			build: func(t *testing.T) error {
				t.Helper()

				return errors.Join(boundRead(t), boundPlain(t))
			},
			err: fs.ErrNotExist,
		},
		"unbound join of plain errors": {
			build: func(*testing.T) error {
				return errors.Join(errors.New("opens too late"), ioErr)
			},
			err: fs.ErrNotExist,
		},
		"unbound summary of a located error and one with no location": {
			build: func(*testing.T) error {
				return niceyaml.NewSummary("2 problems",
					niceyaml.NewError("unknown name", niceyaml.AtPath(namePath)), ioErr)
			},
			err: fs.ErrNotExist,
		},
		"unbound NewError at a position": {
			build: func(*testing.T) error {
				return niceyaml.NewError("bad name", niceyaml.AtPosition(position.New(0, 6)))
			},
			want: true,
		},
		"unbound NewError over a range": {
			build: func(*testing.T) error {
				return niceyaml.NewError("bad name",
					niceyaml.AtRange(position.NewRange(position.New(0, 6), position.New(0, 10))))
			},
			want: true,
		},
		"unbound NewError with no location": {
			build: func(*testing.T) error {
				return niceyaml.NewError("name taken")
			},
			want: true,
		},
		"unbound Invalid of an I/O error": {
			build: func(*testing.T) error {
				return niceyaml.Invalid(ioErr)
			},
			err:  fs.ErrNotExist,
			want: true,
		},
		"unbound Invalid of a located error": {
			build: func(*testing.T) error {
				return niceyaml.Invalid(niceyaml.NewError("unknown name", niceyaml.AtPath(namePath)))
			},
			want: true,
		},
		"unbound fmt wrapper around a located error": {
			build: func(*testing.T) error {
				return fmt.Errorf("check names: %w", niceyaml.NewError("unknown name", niceyaml.AtPath(namePath)))
			},
			want: true,
		},
		"unbound I/O error": {
			build: func(*testing.T) error {
				return ioErr
			},
			err: fs.ErrNotExist,
		},
		"unbound fmt wrapper around an I/O error": {
			build: func(*testing.T) error {
				return fmt.Errorf("check names: %w", ioErr)
			},
			err: fs.ErrNotExist,
		},
		"unbound Rebase of an I/O error": {
			build: func(*testing.T) error {
				return niceyaml.Rebase(ioErr, namePath)
			},
			err: fs.ErrNotExist,
		},
		"unbound Place of an I/O error": {
			build: func(*testing.T) error {
				return niceyaml.Place(ioErr, niceyaml.AtPath(namePath))
			},
			err: fs.ErrNotExist,
		},
		"unbound Place of a fmt wrapper around NewError": {
			build: func(*testing.T) error {
				return niceyaml.Place(
					fmt.Errorf("check names: %w", niceyaml.NewError("unknown name")),
					niceyaml.AtPath(namePath),
				)
			},
			want: true,
		},
		"unbound Invalid of Place of an I/O error": {
			build: func(*testing.T) error {
				return niceyaml.Invalid(niceyaml.Place(ioErr, niceyaml.AtPath(namePath)))
			},
			err:  fs.ErrNotExist,
			want: true,
		},
		"unbound Place of a mixed join": {
			// Each branch answers for itself, as it does with no Place.
			build: func(*testing.T) error {
				return niceyaml.Place(errors.Join(niceyaml.NewError("unknown name"), ioErr))
			},
			err: fs.ErrNotExist,
		},
		"unbound Place of a join of NewErrors": {
			build: func(*testing.T) error {
				return niceyaml.Place(errors.Join(
					niceyaml.NewError("unknown name"),
					niceyaml.NewError("closes before it opens"),
				))
			},
			want: true,
		},
		"unbound Rebase of NewError with no location": {
			build: func(*testing.T) error {
				return niceyaml.Rebase(niceyaml.NewError("name taken"), namePath)
			},
			want: true,
		},
		"unbound Rebase of a located error": {
			build: func(*testing.T) error {
				return niceyaml.Rebase(
					niceyaml.NewError("closes before it opens", niceyaml.AtPath(closePath)),
					hoursPath,
				)
			},
			want: true,
		},
		"unbound summary of plain errors": {
			build: func(*testing.T) error {
				return niceyaml.NewSummary("2 problems", errors.New("opens too late"), ioErr)
			},
			err: fs.ErrNotExist,
		},
		"MultiValidator of a violation and a permission error": {
			build: func(t *testing.T) error {
				t.Helper()

				return validated(t, niceyaml.MultiValidator(portSchema, denied))
			},
			err: fs.ErrPermission,
		},
		"MultiValidator of a decode rejection and a permission error": {
			// The error matches ErrDecode through one problem, and the
			// other is no fault of the document.
			build: func(t *testing.T) error {
				t.Helper()

				return validated(t, niceyaml.MultiValidator(decoding, denied))
			},
			err: niceyaml.ErrDecode,
		},
		"Validator I/O error with a located detail": {
			// The detail explains the I/O error and decides nothing.
			build: func(t *testing.T) error {
				t.Helper()

				return validated(t, niceyaml.ValidatorFunc(func(context.Context, *niceyaml.Node) error {
					return explained(t)
				}))
			},
			err: fs.ErrPermission,
		},
		"unbound I/O error with a located detail": {
			build: explained,
			err:   fs.ErrPermission,
		},
		"wrapper with several verbs around violations and an I/O error": {
			// The wrapper is one error, so the violations decide.
			build: func(t *testing.T) error {
				t.Helper()

				return fmt.Errorf("%w; close: %w", validated(t, portSchema), ioErr)
			},
			err:  fs.ErrNotExist,
			want: true,
		},
		"wrapper with several verbs around NewError and an I/O error": {
			build: func(*testing.T) error {
				return fmt.Errorf("%w; close: %w", niceyaml.NewError("name taken"), ioErr)
			},
			err:  fs.ErrNotExist,
			want: true,
		},
		"join of violations and an I/O error": {
			// The join holds two problems, and one is no fault of the
			// document.
			build: func(t *testing.T) error {
				t.Helper()

				return errors.Join(validated(t, portSchema), ioErr)
			},
			err: fs.ErrNotExist,
		},
		"error with an empty message": {
			build: func(*testing.T) error {
				return errors.New("")
			},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			err := tc.build(t)
			require.Error(t, err)

			if tc.err != nil {
				require.ErrorIs(t, err, tc.err)
			}

			assert.Equal(t, tc.want, niceyaml.IsInvalid(err))

			// The root of the tree answers as IsInvalid does, and each
			// problem answers as IsInvalid does for its error.
			tree := niceyaml.NewErrorTree(err)
			assert.Equal(t, tc.want, tree.Invalid())

			for problem := range tree.Problems() {
				assert.Equal(t, niceyaml.IsInvalid(problem.Err), problem.Invalid(), problem.Text)
			}
		})
	}

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
			"Place with a path":   {validator: placed},
			"Rebase under a path": {validator: rebased},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				err := validated(t, tc.validator)

				var bound *niceyaml.SourceError

				require.ErrorAs(t, err, &bound)

				path, ok := bound.Path()
				require.True(t, ok)
				assert.Equal(t, paths.Doc().Join(namePath), path)
				assert.Equal(t, "1:7: $.name: stat license: permission denied", err.Error())
				assert.False(t, niceyaml.IsInvalid(err))
			})
		}
	})
}
