package niceyaml_test

import (
	"errors"
	"fmt"
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
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/line"
	"go.jacobcolvin.com/niceyaml/paths"
	"go.jacobcolvin.com/niceyaml/position"
	"go.jacobcolvin.com/niceyaml/printer"
	"go.jacobcolvin.com/niceyaml/style"
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

// renderContext is [render] with the given number of context lines.
func renderContext(err error, context int) string {
	return renderWith(err, newXMLPrinter(), context)
}

// renderWith is [render] with the given printer and number of context
// lines. An error that is not a [*niceyaml.SourceError] formats with %+v.
func renderWith(err error, p *printer.Printer, context int) string {
	if bound, ok := err.(*niceyaml.SourceError); ok { //nolint:errorlint // Mirrors %+v, which formats the top-level value.
		return p.PrintError(bound, context)
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
// This is useful for comparing styled output where lipgloss adds padding.
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
			err:  niceyaml.NewErrorFrom(nil),
			want: "",
		},
		"no path or token returns plain error": {
			err:  niceyaml.NewError("something went wrong"),
			want: "something went wrong",
		},
		"with path and source shows annotated source": {
			err: xmlSource(source).WrapError(niceyaml.NewError(
				"invalid value",
				niceyaml.WithPath(paths.Root().Child("key").Key()),
			)),
			want: stringtest.JoinLF(
				"[3:1] $.key: invalid value",
				"",
				"<nameTag>a</nameTag><punctuationMappingValue>:</punctuationMappingValue><text> </text><literalString>b</literalString>",
				"<nameTag>foo</nameTag><punctuationMappingValue>:</punctuationMappingValue><text> </text><literalString>bar</literalString>",
				"<genericError>key</genericError><punctuationMappingValue>:</punctuationMappingValue><text> </text><literalString>value</literalString>",
			),
		},
		"with direct token bypasses path resolution": {
			err: xmlSource(source).WrapError(niceyaml.NewError(
				"bad token",
				niceyaml.WithToken(tokens[0]),
			)),
			want: stringtest.JoinLF(
				"[1:1] bad token",
				"",
				"<genericError>a</genericError><punctuationMappingValue>:</punctuationMappingValue><text> </text><literalString>b</literalString>",
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

func TestSourceWrapError(t *testing.T) {
	t.Parallel()

	sourceInput := stringtest.Input(`
		name: test
		value: 123
	`)

	tcs := map[string]struct {
		inputErr    func() error
		wantExact   string
		wantNil     bool
		wantSameErr bool
	}{
		"wrap nil returns nil": {
			inputErr: func() error { return nil },
			wantNil:  true,
		},
		"wrap non-error type returns unchanged": {
			inputErr:    func() error { return errors.New("plain error") },
			wantSameErr: true,
		},
		"wrap error renders with source": {
			inputErr: func() error {
				return niceyaml.NewError(
					"test error",
					niceyaml.WithPath(paths.Root().Child("name").Key()),
				)
			},
			wantExact: stringtest.JoinLF(
				"[1:1] $.name: test error",
				"",
				"<genericError>name</genericError><punctuationMappingValue>:</punctuationMappingValue><text> </text><literalString>test</literalString>",
				"<nameTag>value</nameTag><punctuationMappingValue>:</punctuationMappingValue><text> </text><literalNumberInteger>123</literalNumberInteger>",
			),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			source := niceyaml.NewSourceFromString(sourceInput)
			inputErr := tc.inputErr()

			got := source.WrapError(inputErr)

			if tc.wantNil {
				assert.NoError(t, got)

				return
			}

			if tc.wantSameErr {
				assert.Equal(t, inputErr, got)

				return
			}

			require.Error(t, got)

			if tc.wantExact != "" {
				assert.Equal(t, tc.wantExact, trimLines(render(got)))
			}
		})
	}
}

func TestError_Path(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		err    *niceyaml.Error
		want   paths.Path
		wantOK bool
	}{
		"no path": {
			err: niceyaml.NewError("test"),
		},
		"path set": {
			err: niceyaml.NewError(
				"test",
				niceyaml.WithPath(paths.Root().Child("foo").Key()),
			),
			want:   paths.Root().Child("foo").Key(),
			wantOK: true,
		},
		"path on a wrapped error": {
			err: niceyaml.NewErrorFrom(
				fmt.Errorf("context: %w", niceyaml.NewError(
					"test",
					niceyaml.WithPath(paths.Root().Child("foo")),
				)),
			),
			want:   paths.Root().Child("foo"),
			wantOK: true,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, ok := tc.err.Path()
			assert.Equal(t, tc.wantOK, ok)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestError_Token(t *testing.T) {
	t.Parallel()

	tk := lexer.Tokenize("key: value")[0]

	tcs := map[string]struct {
		err    *niceyaml.Error
		want   *token.Token
		wantOK bool
	}{
		"no token": {
			err: niceyaml.NewError("test"),
		},
		"token set": {
			err:    niceyaml.NewError("test", niceyaml.WithToken(tk)),
			want:   tk,
			wantOK: true,
		},
		"token on a wrapped error": {
			err: niceyaml.NewErrorFrom(
				fmt.Errorf("context: %w", niceyaml.NewError("test", niceyaml.WithToken(tk))),
			),
			want:   tk,
			wantOK: true,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, ok := tc.err.Token()
			assert.Equal(t, tc.wantOK, ok)
			assert.Same(t, tc.want, got)
		})
	}
}

func TestError_Range(t *testing.T) {
	t.Parallel()

	rng := position.NewRange(position.New(1, 2), position.New(1, 5))

	tcs := map[string]struct {
		err    *niceyaml.Error
		want   position.Range
		wantOK bool
	}{
		"no range": {
			err: niceyaml.NewError("test"),
		},
		"range set": {
			err:    niceyaml.NewError("test", niceyaml.WithRange(rng)),
			want:   rng,
			wantOK: true,
		},
		"range on a wrapped error": {
			err: niceyaml.NewErrorFrom(
				fmt.Errorf("context: %w", niceyaml.NewError("test", niceyaml.WithRange(rng))),
			),
			want:   rng,
			wantOK: true,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, ok := tc.err.Range()
			assert.Equal(t, tc.wantOK, ok)
			assert.Equal(t, tc.want, got)
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
			err: niceyaml.NewSourceFromString(source).WrapError(niceyaml.NewError(
				"not found",
				niceyaml.WithPath(paths.Root().Child("nonexistent").Key()),
			)),
			want: "$.nonexistent: not found\n\nno excerpt: resolve $.nonexistent: not found",
		},
		"path without source": {
			err: niceyaml.NewError(
				"missing source",
				niceyaml.WithPath(paths.Root().Child("key").Key()),
			),
			want: "$.key: missing source",
		},
		"empty source": {
			err: niceyaml.NewSourceFromTokens(emptyTokens).WrapError(niceyaml.NewError(
				"error in empty source",
				niceyaml.WithPath(paths.Root().Child("key").Key()),
			)),
			want: "$.key: error in empty source\n\nno excerpt: resolve $.key: not found: document has no content",
		},
		"nonexistent path in source": {
			err: niceyaml.NewSourceFromString(source).WrapError(niceyaml.NewError(
				"path not found",
				niceyaml.WithPath(
					paths.Root().Child("nonexistent").Child("deep").Key(),
				),
			)),
			want: "$.nonexistent.deep: path not found\n\nno excerpt: resolve $.nonexistent.deep: not found",
		},
		"empty document source": {
			// Tests graceful handling when source has no documents (Docs slice is empty).
			err: niceyaml.NewSourceFromTokens(emptyTokens).WrapError(niceyaml.NewError(
				"empty doc error",
				niceyaml.WithPath(paths.Root().Child("key").Key()),
			)),
			want: "$.key: empty doc error\n\nno excerpt: resolve $.key: not found: document has no content",
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
		path         paths.Path
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
			path:   paths.Root().Child("foo", "bar").Key(),
			errMsg: "nested error",
			want: stringtest.JoinLF(
				"[2:3] $.foo.bar: nested error",
				"",
				"<nameTag>foo</nameTag><punctuationMappingValue>:</punctuationMappingValue>",
				"<text>  </text><genericError>bar</genericError><punctuationMappingValue>:</punctuationMappingValue><text> </text><literalString>value</literalString>",
			),
		},
		"array element path - first item": {
			source: stringtest.Input(`
				items:
				  - first
				  - second
			`),
			path:   paths.Root().Child("items").Index(0).Key(),
			errMsg: "array error",
			want: stringtest.JoinLF(
				"[2:5] $.items[0]: array error",
				"",
				"<nameTag>items</nameTag><punctuationMappingValue>:</punctuationMappingValue>",
				"<text>  </text><punctuationSequenceEntry>-</punctuationSequenceEntry><text> </text><genericError>first</genericError>",
				"<text>  </text><punctuationSequenceEntry>-</punctuationSequenceEntry><text> </text><literalString>second</literalString>",
			),
		},
		"array element path - nested object in array": {
			source: stringtest.Input(`
				users:
				  - name: alice
				    age: 30
			`),
			path:   paths.Root().Child("users").Index(0).Child("name").Key(),
			errMsg: "nested array error",
			want: stringtest.JoinLF(
				"[2:5] $.users[0].name: nested array error",
				"",
				"<nameTag>users</nameTag><punctuationMappingValue>:</punctuationMappingValue>",
				"<text>  </text><punctuationSequenceEntry>-</punctuationSequenceEntry><text> </text><genericError>name</genericError><punctuationMappingValue>:</punctuationMappingValue><text> </text><literalString>alice</literalString>",
				"<text>    </text><nameTag>age</nameTag><punctuationMappingValue>:</punctuationMappingValue><text> </text><literalNumberInteger>30</literalNumberInteger>",
			),
		},
		"root path highlights the first key": {
			source: "key: value",
			path:   paths.Root().Key(),
			errMsg: "root error",
			want: stringtest.JoinLF(
				"[1:1] $: root error",
				"",
				"<genericError>key</genericError><punctuationMappingValue>:</punctuationMappingValue><text> </text><literalString>value</literalString>",
			),
		},
		"single top-level key path": {
			source: "key: value",
			path:   paths.Root().Child("key").Key(),
			errMsg: "top level error",
			want: stringtest.JoinLF(
				"[1:1] $.key: top level error",
				"",
				"<genericError>key</genericError><punctuationMappingValue>:</punctuationMappingValue><text> </text><literalString>value</literalString>",
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
			path:         paths.Root().Child("line3").Key(),
			errMsg:       "middle error",
			contextLines: 1,
			want: stringtest.JoinLF(
				"[3:1] $.line3: middle error",
				"",
				"<nameTag>line2</nameTag><punctuationMappingValue>:</punctuationMappingValue><text> </text><literalString>b</literalString>",
				"<genericError>line3</genericError><punctuationMappingValue>:</punctuationMappingValue><text> </text><literalString>c</literalString>",
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

			err := xmlSource(tc.source).WrapError(
				niceyaml.NewError(tc.errMsg, niceyaml.WithPath(tc.path)),
			)

			assert.Equal(t, tc.want, trimLines(renderContext(err, context)))
		})
	}
}

func TestErrorAnnotation_PathTargetValue(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		path   paths.Path
		source string
		errMsg string
		want   string
	}{
		"value selection highlights value token": {
			source: "key: value",
			path:   paths.Root().Child("key").Value(),
			errMsg: "invalid value",
			want: stringtest.JoinLF(
				"[1:6] $.key: invalid value",
				"",
				"<nameTag>key</nameTag><punctuationMappingValue>:</punctuationMappingValue><text> </text><genericError>value</genericError>",
			),
		},
		"nested path with value target": {
			source: stringtest.Input(`
				foo:
				  bar: nested_value
			`),
			path:   paths.Root().Child("foo", "bar").Value(),
			errMsg: "nested value error",
			want: stringtest.JoinLF(
				"[2:8] $.foo.bar: nested value error",
				"",
				"<nameTag>foo</nameTag><punctuationMappingValue>:</punctuationMappingValue>",
				"<text>  </text><nameTag>bar</nameTag><punctuationMappingValue>:</punctuationMappingValue><text> </text><genericError>nested_value</genericError>",
			),
		},
		"array element works same as key target": {
			// Array elements don't have keys, so both targets return the value token.
			source: stringtest.Input(`
				items:
				  - first
				  - second
			`),
			path:   paths.Root().Child("items").Index(0).Value(),
			errMsg: "array error",
			want: stringtest.JoinLF(
				"[2:5] $.items[0]: array error",
				"",
				"<nameTag>items</nameTag><punctuationMappingValue>:</punctuationMappingValue>",
				"<text>  </text><punctuationSequenceEntry>-</punctuationSequenceEntry><text> </text><genericError>first</genericError>",
				"<text>  </text><punctuationSequenceEntry>-</punctuationSequenceEntry><text> </text><literalString>second</literalString>",
			),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			err := xmlSource(tc.source).WrapError(niceyaml.NewError(
				tc.errMsg,
				niceyaml.WithPath(tc.path),
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

	err := niceyaml.NewSourceFromString(source).WrapError(niceyaml.NewError(
		"test error",
		niceyaml.WithToken(tokens[0]),
	))

	want := stringtest.JoinLF(
		"[1:1] test error",
		"",
		"<genericError>key</genericError><punctuationMappingValue>:</punctuationMappingValue><text> </text><literalString>value</literalString>",
		"<nameTag>foo</nameTag><punctuationMappingValue>:</punctuationMappingValue><text> </text><literalString>bar</literalString>",
	)

	var bound *niceyaml.SourceError

	require.ErrorAs(t, err, &bound)
	assert.Equal(t, want, trimLines(customPrinter.PrintError(bound, 2)))
}

func TestError_SpecialParentContext(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		source string
		path   paths.Path
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
			path:   paths.Root().Index(1).Key(),
			errMsg: "array element error",
			want: stringtest.JoinLF(
				"[2:3] $[1]: array element error",
				"",
				"<punctuationSequenceEntry>-</punctuationSequenceEntry><text> </text><literalString>first</literalString>",
				"<punctuationSequenceEntry>-</punctuationSequenceEntry><text> </text><genericError>second</genericError>",
				"<punctuationSequenceEntry>-</punctuationSequenceEntry><text> </text><literalString>third</literalString>",
			),
		},
		"document root - no entry selected": {
			// A key target on the root falls back to the mapping's first key.
			source: stringtest.Input(`
				key: value
				another: line
			`),
			path:   paths.Root().Key(),
			errMsg: "document root error",
			want: stringtest.JoinLF(
				"[1:1] $: document root error",
				"",
				"<genericError>key</genericError><punctuationMappingValue>:</punctuationMappingValue><text> </text><literalString>value</literalString>",
				"<nameTag>another</nameTag><punctuationMappingValue>:</punctuationMappingValue><text> </text><literalString>line</literalString>",
			),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			err := xmlSource(tc.source).WrapError(niceyaml.NewError(
				tc.errMsg,
				niceyaml.WithPath(tc.path),
			))

			assert.Equal(t, tc.want, trimLines(render(err)))
		})
	}
}

func TestError_NilToken(t *testing.T) {
	t.Parallel()

	// Test getTokenPosition with nil token - should return empty position.
	err := niceyaml.NewError(
		"nil token error",
		niceyaml.WithToken(nil),
	)

	got := render(err)
	// Should still work and show the error message.
	assert.Equal(t, "nil token error", got)
}

func TestError_Unwrap(t *testing.T) {
	t.Parallel()

	t.Run("a nil Error unwraps to nothing", func(t *testing.T) {
		t.Parallel()

		var nilErr *niceyaml.Error

		assert.Nil(t, nilErr.Unwrap())

		// A chain that holds a nil Error behind a real one is safe to walk.
		outer := niceyaml.NewErrorFrom(nilErr)

		var bound *niceyaml.SourceError

		assert.NotErrorAs(t, outer, &bound)
	})

	t.Run("unwraps underlying error", func(t *testing.T) {
		t.Parallel()

		underlying := errors.New("underlying error")
		err := niceyaml.NewErrorFrom(underlying)

		got := err.Unwrap()

		require.Len(t, got, 1)
		assert.Equal(t, underlying, got[0])
	})

	t.Run("nil error unwraps to nil", func(t *testing.T) {
		t.Parallel()

		err := niceyaml.NewErrorFrom(nil)

		got := err.Unwrap()

		assert.Nil(t, got)
	})

	t.Run("errors.Is works through Error wrapper", func(t *testing.T) {
		t.Parallel()

		sentinel := errors.New("sentinel error")
		err := niceyaml.NewErrorFrom(sentinel)

		require.ErrorIs(t, err, sentinel)
	})

	t.Run("unwraps nested errors", func(t *testing.T) {
		t.Parallel()

		underlying := errors.New("main error")
		nested1 := errors.New("nested error 1")
		nested2 := errors.New("nested error 2")

		err := niceyaml.NewErrorFrom(underlying,
			niceyaml.WithErrors(
				niceyaml.NewErrorFrom(nested1),
				niceyaml.NewErrorFrom(nested2),
			),
		)

		got := err.Unwrap()

		require.Len(t, got, 3)
		assert.Equal(t, underlying, got[0])
		// Check that nested errors are included.
		require.ErrorIs(t, err, nested1)
		require.ErrorIs(t, err, nested2)
	})

	t.Run("skips nil nested errors", func(t *testing.T) {
		t.Parallel()

		underlying := errors.New("main error")
		nested := errors.New("nested error")

		err := niceyaml.NewErrorFrom(underlying,
			niceyaml.WithErrors(
				nil,
				niceyaml.NewErrorFrom(nested),
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

		err := xmlSource(source).WrapError(niceyaml.NewError(
			"validation failed",
			niceyaml.WithPath(paths.Root().Child("name").Key()),
			niceyaml.WithErrors(
				niceyaml.NewError(
					"invalid type",
					niceyaml.WithPath(paths.Root().Child("value").Value()),
				),
			),
		))

		got := trimLines(render(err))

		// Should highlight main error token and include annotation for nested error.
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

		err := xmlSource(source).WrapError(niceyaml.NewError(
			"validation failed",
			niceyaml.WithPath(paths.Root().Child("name").Key()),
			niceyaml.WithErrors(
				niceyaml.NewError(
					"invalid type",
					niceyaml.WithPath(paths.Root().Child("value").Value()),
				),
				niceyaml.NewError(
					"missing field",
					niceyaml.WithPath(paths.Root().Child("other").Key()),
				),
			),
		))

		got := trimLines(render(err))

		// Should include both annotations.
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
		err := xmlSource(source).WrapError(niceyaml.NewError(
			"validation failed",
			niceyaml.WithToken(tokens[0]),
			niceyaml.WithErrors(
				niceyaml.NewError(
					"error1",
					niceyaml.WithPath(paths.Root().Child("key").Key()),
				),
				niceyaml.NewError(
					"error2",
					niceyaml.WithPath(paths.Root().Child("key").Value()),
				),
			),
		))

		got := trimLines(render(err))

		// Errors on same line should be combined with "; ".
		assert.Contains(t, got, "error1; error2")
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

		err := xmlSource(source).WrapError(niceyaml.NewError(
			"main error",
			niceyaml.WithToken(tokens[0]),
			niceyaml.WithErrors(
				niceyaml.NewError(
					"nested with token",
					niceyaml.WithToken(fooToken),
				),
			),
		))

		got := trimLines(render(err))

		// Should highlight both tokens.
		assert.Contains(t, got, "<genericError>key</genericError>")
		assert.Contains(t, got, "<genericError>foo</genericError>")
		assert.Contains(t, got, "^ nested with token")
	})

	t.Run("nested error that does not resolve is listed after the excerpt", func(t *testing.T) {
		t.Parallel()

		source := stringtest.Input(`
			key: value
		`)

		err := xmlSource(source).WrapError(niceyaml.NewError(
			"main error",
			niceyaml.WithPath(paths.Root().Child("key").Key()),
			niceyaml.WithErrors(
				niceyaml.NewError(
					"nested error",
					niceyaml.WithPath(paths.Root().Child("nonexistent").Key()),
				),
			),
		))

		got := trimLines(render(err))

		assert.Contains(t, got, "[1:1] $.key: main error")
		assert.Contains(t, got, "<genericError>key</genericError>")
		// The nested error has no line to annotate, so it follows the
		// excerpt with its unresolved location.
		assert.NotContains(t, got, "^ nested error")
		assert.True(t, strings.HasSuffix(got, "\n\n$.nonexistent: nested error"), got)
	})

	t.Run("plain error with nested errors renders the headline only", func(t *testing.T) {
		t.Parallel()

		nested1 := niceyaml.NewError("nested 1")
		nested2 := niceyaml.NewError("nested 2")
		err := niceyaml.NewError("main error", niceyaml.WithErrors(nested1, nested2))

		assert.Equal(t, "main error", render(err))

		// The nested errors surface through Unwrap instead.
		require.ErrorIs(t, err, nested1)
		require.ErrorIs(t, err, nested2)
	})

	t.Run("nested error without path or token is listed after the excerpt", func(t *testing.T) {
		t.Parallel()

		source := stringtest.Input(`
			key: value
		`)

		err := xmlSource(source).WrapError(niceyaml.NewError(
			"main error",
			niceyaml.WithPath(paths.Root().Child("key").Key()),
			niceyaml.WithErrors(
				niceyaml.NewError("no location"),
			),
		))

		got := trimLines(render(err))

		assert.Contains(t, got, "[1:1] $.key: main error")
		assert.NotContains(t, got, "^ no location")
		assert.True(t, strings.HasSuffix(got, "\n\nno location"), got)
	})

	t.Run("errors.Is works with nested errors", func(t *testing.T) {
		t.Parallel()

		sentinel1 := errors.New("sentinel 1")
		sentinel2 := errors.New("sentinel 2")

		err := niceyaml.NewError(
			"main error",
			niceyaml.WithErrors(
				niceyaml.NewErrorFrom(sentinel1),
				niceyaml.NewErrorFrom(sentinel2),
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
				niceyaml.NewErrorFrom(customErr),
			),
		)

		// The errors.As should find the custom error through the nested errors.
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

		// Create error with NO main path, but nested error with path.
		err := xmlSource(source).WrapError(niceyaml.NewError(
			"validation failed at 1 location",
			niceyaml.WithErrors(
				niceyaml.NewError(
					"got number, want string",
					niceyaml.WithPath(paths.Root().Child("value").Value()),
				),
			),
		))

		got := trimLines(render(err))

		// Should contain the main message.
		assert.Contains(t, got, "validation failed at 1 location")
		// Should contain nested error annotation.
		assert.Contains(t, got, "got number, want string")
		// Should contain YAML content (not just bullet points).
		assert.Contains(t, got, "value")
		assert.Contains(t, got, "123")
		// Should highlight the nested error value.
		assert.Contains(t, got, "<genericError>123</genericError>")
	})

	t.Run("nested-only error without source falls back to plain", func(t *testing.T) {
		t.Parallel()

		// Create error with NO main path, no source, but nested error with path.
		err := niceyaml.NewError(
			"validation failed",
			niceyaml.WithErrors(
				niceyaml.NewError(
					"nested error",
					niceyaml.WithPath(paths.Root().Child("value").Value()),
				),
			),
		)

		// Without a source, only the headline renders.
		assert.Equal(t, "validation failed", render(err))
	})

	t.Run("nested-only error with multiple lines", func(t *testing.T) {
		t.Parallel()

		source := stringtest.Input(`
			name: test
			value: 123
			other: data
		`)

		// Create error with nested errors on different lines.
		err := xmlSource(source).WrapError(niceyaml.NewError(
			"validation failed at 2 locations",
			niceyaml.WithErrors(
				niceyaml.NewError(
					"type error on value",
					niceyaml.WithPath(paths.Root().Child("value").Value()),
				),
				niceyaml.NewError(
					"unexpected property",
					niceyaml.WithPath(paths.Root().Child("other").Key()),
				),
			),
		))

		got := trimLines(render(err))

		// Should contain both annotations.
		assert.Contains(t, got, "type error on value")
		assert.Contains(t, got, "unexpected property")
		// Should highlight both error locations.
		assert.Contains(t, got, "<genericError>123</genericError>")
		assert.Contains(t, got, "<genericError>other</genericError>")
	})

	t.Run("nested-only error with resolvable and unresolvable nested errors", func(t *testing.T) {
		t.Parallel()

		source := stringtest.Input(`
			name: test
			value: 123
		`)

		// Create error with some nested errors that resolve and some that don't.
		err := xmlSource(source).WrapError(niceyaml.NewError(
			"validation failed",
			niceyaml.WithErrors(
				niceyaml.NewError(
					"resolvable error",
					niceyaml.WithPath(paths.Root().Child("value").Value()),
				),
				niceyaml.NewError(
					"unresolvable error",
					niceyaml.WithPath(paths.Root().Child("nonexistent").Key()),
				),
			),
		))

		got := trimLines(render(err))

		assert.Contains(t, got, "^ resolvable error")
		// The unresolvable error follows the excerpt instead of annotating it.
		assert.NotContains(t, got, "^ unresolvable error")
		assert.True(t, strings.HasSuffix(got, "\n\n$.nonexistent: unresolvable error"), got)
	})

	t.Run("nested-only error with nested error that has no path or token", func(t *testing.T) {
		t.Parallel()

		source := stringtest.Input(`
			name: test
			value: 123
		`)

		// No nested error has a location, so nothing annotates the source
		// and the nested error follows the headline as a line of its own.
		err := niceyaml.NewSourceFromString(source).WrapError(niceyaml.NewError(
			"validation failed",
			niceyaml.WithErrors(
				niceyaml.NewError("nested without location"),
			),
		))

		assert.Equal(t, "validation failed\n\nnested without location", render(err))
	})
}

func TestSourceError_Detail_NestedLocations(t *testing.T) {
	t.Parallel()

	// A nested error with a location annotates the source excerpt. A nested
	// error without one follows the headline as a line of its own.

	source := stringtest.Input(`
		key: value
		other: data
	`)

	tcs := map[string]struct {
		err        error
		want       string
		wantDetail bool
	}{
		"no nested errors": {
			err: niceyaml.NewSourceFromString(source).WrapError(niceyaml.NewError(
				"main error",
			)),
			want: "main error",
		},
		"nested errors without locations": {
			err: niceyaml.NewSourceFromString(source).WrapError(niceyaml.NewError(
				"main error",
				niceyaml.WithErrors(
					niceyaml.NewError("nested 1"),
					niceyaml.NewError("nested 2"),
				),
			)),
			want: "main error\n\nnested 1\nnested 2",
		},
		"nested error with path": {
			err: xmlSource(source).WrapError(niceyaml.NewError(
				"main error",
				niceyaml.WithErrors(
					niceyaml.NewError(
						"nested with path",
						niceyaml.WithPath(paths.Root().Child("key").Key()),
					),
				),
			)),
			wantDetail: true,
		},
		"nested error with token": {
			err: func() error {
				tokens := lexer.Tokenize(source)

				return xmlSource(source).WrapError(niceyaml.NewError(
					"main error",
					niceyaml.WithErrors(
						niceyaml.NewError(
							"nested with token",
							niceyaml.WithToken(tokens[0]),
						),
					),
				))
			}(),
			wantDetail: true,
		},
		"mix of located and unlocated nested errors": {
			err: xmlSource(source).WrapError(niceyaml.NewError(
				"main error",
				niceyaml.WithErrors(
					niceyaml.NewError("no path"),
					niceyaml.NewError(
						"has path",
						niceyaml.WithPath(paths.Root().Child("key").Key()),
					),
					niceyaml.NewError("also no path"),
				),
			)),
			wantDetail: true,
		},
		"nil nested errors only": {
			err: niceyaml.NewSourceFromString(source).WrapError(niceyaml.NewError(
				"main error",
				niceyaml.WithErrors(nil, nil),
			)),
			want: "main error",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := render(tc.err)

			if tc.wantDetail {
				assert.Contains(t, got, "main error")
				assert.Contains(t, got, "key")
			} else {
				assert.Equal(t, tc.want, got)
			}
		})
	}
}

func TestSourceError_Detail_ListsUnresolvedNested(t *testing.T) {
	t.Parallel()

	source := niceyaml.NewSourceFromString("a: 1\n")
	err := source.WrapError(niceyaml.NewError(
		"2 schema violations",
		niceyaml.WithErrors(
			niceyaml.NewError("bad x", niceyaml.WithPath(paths.Root().Child("x").Value())),
			niceyaml.NewError("bad y", niceyaml.WithPath(paths.Root().Child("y").Value())),
		),
	))

	// No location resolves, so the message is the headline alone and the
	// %+v form lists each nested error with its unresolved location.
	assert.Equal(t, "2 schema violations", err.Error())
	assert.Equal(t, "2 schema violations\n\n$.x: bad x\n$.y: bad y", render(err))
}

func TestSourceError_Format_Plain(t *testing.T) {
	t.Parallel()

	source := niceyaml.NewSourceFromString("a: 1\nb: 2\nc: 3\n")

	t.Run("marks the location without escape sequences", func(t *testing.T) {
		t.Parallel()

		err := source.WrapError(niceyaml.NewError("bad value", niceyaml.WithPath(paths.Root().Child("b").Value())))

		got := fmt.Sprintf("%+v", err)

		assert.Equal(t, stringtest.JoinLF(
			"[2:4] $.b: bad value",
			"",
			"   1 | a: 1",
			"   2 | b: 2",
			"     |    ^",
			"   3 | c: 3",
		), got)
		assert.NotContains(t, got, "\x1b")
	})

	t.Run("nested errors annotate their lines", func(t *testing.T) {
		t.Parallel()

		err := source.WrapError(niceyaml.NewError("2 problems", niceyaml.WithErrors(
			niceyaml.NewError("bad a", niceyaml.WithPath(paths.Root().Child("a").Key())),
			niceyaml.NewError("bad c", niceyaml.WithPath(paths.Root().Child("c").Value())),
		)))

		assert.Equal(t, stringtest.JoinLF(
			"2 problems",
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
			"[2:4] $.b: bad b",
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

		multi := niceyaml.NewSourceFromString("a: 1\n---\nb: 2\n")
		err := multi.WrapError(niceyaml.NewError("bad value", niceyaml.WithPath(paths.Root().Child("b").Value())))

		assert.Equal(t,
			"$.b: bad value\n\nno excerpt: [2:1] multiple documents in source: 2 documents",
			fmt.Sprintf("%+v", err),
		)
	})

	t.Run("control characters render as pictures", func(t *testing.T) {
		t.Parallel()

		src := niceyaml.NewSourceFromString("a: \"x\\ty\"\n")
		err := src.WrapError(niceyaml.NewError("bad", niceyaml.WithRange(
			position.NewRange(position.New(0, 3), position.New(0, 9)),
		)))

		assert.Equal(t, stringtest.JoinLF(
			"[1:4] bad",
			"",
			"   1 | a: \"x\\ty\"",
			"     |    ^^^^^^",
		), fmt.Sprintf("%+v", err))
	})
}

func TestError_NilInnerError(t *testing.T) {
	t.Parallel()

	err := niceyaml.NewErrorFrom(niceyaml.NewErrorFrom(nil))
	assert.Empty(t, err.Error())

	wrapped := niceyaml.NewSourceFromString("a: 1\n").WrapError(err)
	assert.Empty(t, wrapped.Error())
	assert.Empty(t, render(wrapped))
}

func TestError_NilInnerErrorWithLocation(t *testing.T) {
	t.Parallel()

	source := xmlSource("a: 1\nb: 2\n")
	tk := source.Lines().TokenAt(position.New(0, 3))
	require.NotNil(t, tk)

	// An Error from a nil error has no message, so its text is the path it
	// carries, or nothing, and binding puts the resolved position in front.
	tcs := map[string]struct {
		err       *niceyaml.Error
		want      string
		wantBound string
	}{
		"token": {
			err:       niceyaml.NewErrorFrom(nil, niceyaml.WithToken(tk)),
			want:      "",
			wantBound: "[1:4]",
		},
		"range": {
			err: niceyaml.NewErrorFrom(nil,
				niceyaml.WithRange(position.NewRange(position.New(1, 3), position.New(1, 4))),
			),
			want:      "",
			wantBound: "[2:4]",
		},
		"path": {
			err:       niceyaml.NewErrorFrom(nil, niceyaml.WithPath(paths.Root().Child("b").Value())),
			want:      "$.b:",
			wantBound: "[2:4] $.b:",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, tc.err.Error())

			bound := source.WrapError(tc.err)
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
	inner := niceyaml.NewError("bad a", niceyaml.WithPath(paths.Root().Child("a").Value()))
	nested := niceyaml.NewError("bad b", niceyaml.WithPath(paths.Root().Child("b").Value()))

	// Nested errors on the wrapper add annotations, and the wrapper still
	// takes its position from the Error it wraps.
	wrapped := source.WrapError(niceyaml.NewErrorFrom(inner, niceyaml.WithErrors(nested)))
	assert.Equal(t, "[1:4] $.a: bad a", wrapped.Error())

	var bound *niceyaml.SourceError

	require.ErrorAs(t, wrapped, &bound)

	rng, err := bound.Location()
	require.NoError(t, err)
	assert.Equal(t, 0, rng.Start.Line)
	assert.Equal(t, 3, rng.Start.Col)

	got := trimLines(render(wrapped))
	assert.Contains(t, got, "<genericError>1</genericError>")
	assert.Contains(t, got, "^ bad b")
}

func TestError_NestedLocationWithoutMessage(t *testing.T) {
	t.Parallel()

	source := niceyaml.NewSourceFromString("a: 1\nb: 2\n")
	inner := niceyaml.NewError("bad a", niceyaml.WithPath(paths.Root().Child("a").Value()))
	nested := niceyaml.NewErrorFrom(nil, niceyaml.WithPath(paths.Root().Child("b").Value()))

	// A nested Error built from a nil error names a location and nothing
	// else, so that location is highlighted and carries no annotation.
	wrapped := source.WrapError(niceyaml.NewErrorFrom(inner, niceyaml.WithErrors(nested)))

	got := trimLines(render(wrapped))
	assert.Contains(t, got, "<genericError>1</genericError>")
	assert.Contains(t, got, "<genericError>2</genericError>")
	assert.NotContains(t, got, "^")
}

func TestError_calculateNestedLineRange(t *testing.T) {
	t.Parallel()

	// These tests verify calculateNestedLineRange indirectly through rendered output.
	// The line range determines which lines are visible in the output.

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

		err := xmlSource(source).WrapError(niceyaml.NewError(
			"validation error",
			niceyaml.WithErrors(
				niceyaml.NewError(
					"error at line3",
					niceyaml.WithPath(paths.Root().Child("line3").Key()),
				),
			),
		))

		got := trimLines(renderContext(err, 1))

		// Should show context around line3.
		assert.Contains(t, got, "line2")
		assert.Contains(t, got, "line3")
		assert.Contains(t, got, "line4")
	})

	t.Run("multiple nested errors on different lines expands range", func(t *testing.T) {
		t.Parallel()

		source := stringtest.Input(`
			line1: a
			line2: b
			line3: c
			line4: d
			line5: e
			line6: f
		`)

		err := xmlSource(source).WrapError(niceyaml.NewError(
			"validation error", // No extra context.
			niceyaml.WithErrors(
				niceyaml.NewError(
					"error at line1",
					niceyaml.WithPath(paths.Root().Child("line1").Key()),
				),
				niceyaml.NewError(
					"error at line6",
					niceyaml.WithPath(paths.Root().Child("line6").Key()),
				),
			),
		))

		got := trimLines(renderContext(err, 0))

		// Should show the full range from line1 to line6.
		assert.Contains(t, got, "line1")
		assert.Contains(t, got, "line6")
		assert.Contains(t, got, "error at line1")
		assert.Contains(t, got, "error at line6")
	})

	t.Run("nested errors on same line do not expand range", func(t *testing.T) {
		t.Parallel()

		source := stringtest.Input(`
			line1: a
			line2: b
			line3: c
		`)

		err := xmlSource(source).WrapError(niceyaml.NewError(
			"validation error",
			niceyaml.WithErrors(
				niceyaml.NewError(
					"error 1",
					niceyaml.WithPath(paths.Root().Child("line2").Key()),
				),
				niceyaml.NewError(
					"error 2",
					niceyaml.WithPath(paths.Root().Child("line2").Value()),
				),
			),
		))

		got := trimLines(renderContext(err, 0))

		// Should show line2 with combined errors.
		assert.Contains(t, got, "line2")
		assert.Contains(t, got, "error 1; error 2")
	})
}

func TestError_HunkDisplay(t *testing.T) {
	t.Parallel()

	t.Run("distant errors show separate hunks with separator", func(t *testing.T) {
		t.Parallel()

		// Create a source with many lines.
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

		// Errors at line1 and line10 with contextLines=1 should create separate hunks.
		err := xmlSource(source).WrapError(niceyaml.NewError(
			"validation error",
			niceyaml.WithErrors(
				niceyaml.NewError(
					"error at start",
					niceyaml.WithPath(paths.Root().Child("line1").Key()),
				),
				niceyaml.NewError(
					"error at end",
					niceyaml.WithPath(paths.Root().Child("line10").Key()),
				),
			),
		))

		got := trimLines(renderContext(err, 1))

		// Should show both errors.
		assert.Contains(t, got, "error at start")
		assert.Contains(t, got, "error at end")
		// Should have "..." separator between hunks.
		assert.Contains(t, got, "...")
		// Should show context lines around errors.
		assert.Contains(t, got, "line1")
		assert.Contains(t, got, "line2")
		assert.Contains(t, got, "line9")
		assert.Contains(t, got, "line10")
		// Should NOT show middle lines (lines 3-8).
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

		// The token points past the end of line 5, where the view holds no
		// token, so there is nothing to highlight.
		err := xmlSource(source).WrapError(niceyaml.NewError(
			"boom",
			niceyaml.WithToken(&token.Token{Position: &token.Position{Line: 5, Column: 50}}),
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

		err := xmlSource(source).WrapError(niceyaml.NewError(
			"missing",
			niceyaml.WithPath(paths.Root().Child("line4").Value()),
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

		err := xmlSource(source).WrapError(niceyaml.NewError(
			"validation error",
			niceyaml.WithPath(paths.Root().Child("line1").Key()),
			niceyaml.WithErrors(
				niceyaml.NewError(
					"error at end",
					niceyaml.WithPath(paths.Root().Child("line6").Key()),
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

		// Errors at line1 and line3 with contextLines=1 should merge into one hunk
		// since they're within 2*contextLines of each other.
		err := xmlSource(source).WrapError(niceyaml.NewError(
			"validation error",
			niceyaml.WithErrors(
				niceyaml.NewError(
					"first error",
					niceyaml.WithPath(paths.Root().Child("line1").Key()),
				),
				niceyaml.NewError(
					"second error",
					niceyaml.WithPath(paths.Root().Child("line3").Key()),
				),
			),
		))

		got := trimLines(renderContext(err, 1))

		// Should show both errors.
		assert.Contains(t, got, "first error")
		assert.Contains(t, got, "second error")
		// Should NOT have "..." separator since errors are close.
		assert.NotContains(t, got, "...")
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

		// No main path, but nested errors at distant locations.
		err := xmlSource(source).WrapError(niceyaml.NewError(
			"validation failed at 2 locations",
			niceyaml.WithErrors(
				niceyaml.NewError(
					"first location error",
					niceyaml.WithPath(paths.Root().Child("line1").Key()),
				),
				niceyaml.NewError(
					"second location error",
					niceyaml.WithPath(paths.Root().Child("line10").Key()),
				),
			),
		))

		got := trimLines(renderContext(err, 1))

		// Should show both errors.
		assert.Contains(t, got, "first location error")
		assert.Contains(t, got, "second location error")
		// Should have "..." separator.
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

		// Errors at first and last lines.
		err := xmlSource(source).WrapError(niceyaml.NewError(
			"validation error",
			niceyaml.WithErrors(
				niceyaml.NewError(
					"error at first",
					niceyaml.WithPath(paths.Root().Child("first").Key()),
				),
				niceyaml.NewError(
					"error at last",
					niceyaml.WithPath(paths.Root().Child("last").Key()),
				),
			),
		))

		got := trimLines(renderContext(err, 1))

		// Should show both errors with context clipped to valid range.
		assert.Contains(t, got, "error at first")
		assert.Contains(t, got, "error at last")
		assert.Contains(t, got, "first")
		assert.Contains(t, got, "last")
		// Should have "..." separator.
		assert.Contains(t, got, "...")
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

		// Main error at line1, nested error at line10.
		err := xmlSource(source).WrapError(niceyaml.NewError(
			"main error",
			niceyaml.WithPath(paths.Root().Child("line1").Key()),
			niceyaml.WithErrors(
				niceyaml.NewError(
					"nested error",
					niceyaml.WithPath(paths.Root().Child("line10").Key()),
				),
			),
		))

		got := trimLines(renderContext(err, 1))

		// Should show both error locations.
		assert.Contains(t, got, "<genericError>line1</genericError>")
		assert.Contains(t, got, "<genericError>line10</genericError>")
		// Should show nested error annotation.
		assert.Contains(t, got, "nested error")
		// Should have "..." separator.
		assert.Contains(t, got, "...")
	})

	t.Run("adjacent errors with no context merge into single hunk", func(t *testing.T) {
		t.Parallel()

		source := stringtest.Input(`
			line1: a
			line2: b
			line3: c
		`)

		// With contextLines=0, errors at line1 and line2 should merge because
		// they're within threshold (2*0+1=1) of each other.
		err := xmlSource(source).WrapError(niceyaml.NewError(
			"validation error",
			niceyaml.WithErrors(
				niceyaml.NewError(
					"error at line1",
					niceyaml.WithPath(paths.Root().Child("line1").Key()),
				),
				niceyaml.NewError(
					"error at line2",
					niceyaml.WithPath(paths.Root().Child("line2").Key()),
				),
			),
		))

		got := trimLines(renderContext(err, 0))

		// Should show both error annotations.
		assert.Contains(t, got, "error at line1")
		assert.Contains(t, got, "error at line2")
		// Should NOT have "..." separator since errors are adjacent.
		assert.NotContains(t, got, "...")
	})
}

func TestError_Width(t *testing.T) {
	t.Parallel()

	// Create YAML with a long value that will need wrapping.
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
				printer.WithWidth(tc.width),
			)

			err := niceyaml.NewSourceFromString(source).WrapError(niceyaml.NewError("test error",
				niceyaml.WithToken(tokens[0]),
			))

			output := renderWith(err, errPrinter, 2)
			lines := strings.Split(output, "\n")

			// Skip the header line "[1:1] $.name: test error:" and the empty line.
			contentLines := 0
			for _, line := range lines {
				if strings.Contains(line, "key:") || strings.Contains(line, "this is") ||
					strings.Contains(line, "should wrap") || strings.Contains(line, "limited") {
					contentLines++
				}
			}

			if tc.wantWrapped {
				// When wrapped, the content should span multiple lines.
				assert.Greater(t, contentLines, 1, "expected content to wrap into multiple lines")
			} else {
				// When not wrapped, the YAML content should be on a single line.
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

	// Width comes from the printer. Word wrap is enabled by default in
	// NewPrinter.
	customPrinter := printer.New(
		printer.WithStyles(yamltest.NewXMLStyles()),
		printer.WithGutter(printer.NoGutter),
		printer.WithContainerStyle(lipgloss.NewStyle()),
	)

	errPrinter := customPrinter.With(printer.WithWidth(30))

	err := niceyaml.NewSourceFromString(source).WrapError(niceyaml.NewError(
		"test error",
		niceyaml.WithToken(tokens[0]),
	))

	output := renderWith(err, errPrinter, 2)
	lines := strings.Split(output, "\n")

	// Should have multiple content lines due to wrapping.
	contentLines := 0
	for _, line := range lines {
		if strings.Contains(line, "key") || strings.Contains(line, "this is") ||
			strings.Contains(line, "should wrap") {
			contentLines++
		}
	}

	assert.Greater(t, contentLines, 1, "expected content to wrap into multiple lines with custom printer")

	// The caller's printer keeps its own width.
	assert.Equal(t, 0, customPrinter.Width())
}

func TestError_Width_DefaultPrinter(t *testing.T) {
	t.Parallel()

	source := stringtest.Input(`
		key: this is a very long value that should wrap when width is limited
	`)
	tokens := lexer.Tokenize(source)

	// A printer with only a width keeps the default styles and gutter.
	errPrinter := printer.New(printer.WithWidth(30))

	err := niceyaml.NewSourceFromString(source).WrapError(niceyaml.NewError(
		"test error",
		niceyaml.WithToken(tokens[0]),
	))

	output := renderWith(err, errPrinter, 2)
	lines := strings.Split(output, "\n")

	// Should have multiple content lines due to wrapping.
	contentLines := 0
	for _, line := range lines {
		if strings.Contains(line, "key") || strings.Contains(line, "this is") ||
			strings.Contains(line, "should wrap") {
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
		want         string
	}{
		"long annotation wraps at narrow width": {
			width:        40,
			nestedErrMsg: "this is a very long error message that should definitely wrap when the width is limited",
			want: stringtest.JoinLF(
				"[1:1] $.key: validation failed",
				"",
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
			want: stringtest.JoinLF(
				"[1:1] $.key: validation failed",
				"",
				"key: value",
				"     ^ this is a very long error message that should not wrap",
				"other: data",
			),
		},
		"short annotation fits on single line": {
			width:        80,
			nestedErrMsg: "short error",
			want: stringtest.JoinLF(
				"[1:1] $.key: validation failed",
				"",
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
				printer.WithWidth(tc.width),
			)

			err := niceyaml.NewSourceFromString(source).WrapError(niceyaml.NewError(
				"validation failed",
				niceyaml.WithPath(paths.Root().Child("key").Key()),
				niceyaml.WithErrors(
					niceyaml.NewError(
						tc.nestedErrMsg,
						niceyaml.WithPath(paths.Root().Child("key").Value()),
					),
				),
			))

			got := trimLines(renderWith(err, errPrinter, 2))

			assert.Equal(t, tc.want, got)
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

	// Test multiple nested errors with long messages.
	errPrinter := printer.New(
		printer.WithStyles(&style.Styles{}),
		printer.WithGutter(printer.NoGutter),
		printer.WithContainerStyle(lipgloss.NewStyle()),
		printer.WithWidth(50),
	)

	err := niceyaml.NewSourceFromString(source).WrapError(niceyaml.NewError(
		"validation failed at 2 locations",
		niceyaml.WithErrors(
			niceyaml.NewError(
				"first error with a very long message that should wrap properly",
				niceyaml.WithPath(paths.Root().Child("value").Value()),
			),
			niceyaml.NewError(
				"second error also with a long message for testing wrap behavior",
				niceyaml.WithPath(paths.Root().Child("other").Key()),
			),
		),
	))
	got := trimLines(renderWith(err, errPrinter, 2))

	want := stringtest.JoinLF(
		"validation failed at 2 locations",
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

	// Multiple errors on same line get combined with "; ".
	errPrinter := printer.New(
		printer.WithStyles(&style.Styles{}),
		printer.WithGutter(printer.NoGutter),
		printer.WithContainerStyle(lipgloss.NewStyle()),
		printer.WithWidth(40),
	)

	err := niceyaml.NewSourceFromString(source).WrapError(niceyaml.NewError(
		"validation failed",
		niceyaml.WithPath(paths.Root().Child("key").Key()),
		niceyaml.WithErrors(
			niceyaml.NewError(
				"first error message here",
				niceyaml.WithPath(paths.Root().Child("key").Key()),
			),
			niceyaml.NewError(
				"second error message here",
				niceyaml.WithPath(paths.Root().Child("key").Value()),
			),
		),
	))
	got := trimLines(renderWith(err, errPrinter, 2))

	want := stringtest.JoinLF(
		"[1:1] $.key: validation failed",
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
	// but not identity with the tokens the source's view was built from.
	file, err := source.File()
	require.NoError(t, err)

	node, err := paths.Root().Child("b").Node(file.Docs[0])
	require.NoError(t, err)

	literal, ok := node.(*ast.LiteralNode)
	require.True(t, ok, "want *ast.LiteralNode, got %T", node)

	tk := literal.Value.GetToken()

	got := trimLines(render(source.WrapError(niceyaml.NewError(
		"bad block",
		niceyaml.WithToken(tk),
		niceyaml.WithErrors(
			niceyaml.NewError("bad c", niceyaml.WithPath(paths.Root().Child("c").Value())),
		),
	))))

	// Both lines of the block scalar are highlighted, comments keep their
	// place, and the nested path resolves in the same source.
	assert.Contains(t, got, "<genericError>two</genericError>")
	assert.Contains(t, got, "<genericError>lines</genericError>")
	assert.Contains(t, got, "<comment># note</comment>")
	assert.Contains(t, got, "<genericError>3</genericError>")
	assert.Contains(t, got, "^ bad c")
}

func TestError_BoundDocument(t *testing.T) {
	t.Parallel()

	source := xmlSource("name: first\n---\nname: second\n")
	namePath := paths.Root().Child("name").Value()

	docs, err := source.Documents()
	require.NoError(t, err)
	require.Len(t, docs, 2)

	t.Run("path resolves in the document that bound it", func(t *testing.T) {
		t.Parallel()

		err := docs[1].WrapError(niceyaml.NewError("bad name", niceyaml.WithPath(namePath)))

		got := trimLines(render(err))

		assert.True(t, strings.HasPrefix(got, "[3:7] $.name: bad name"), got)
		assert.Contains(t, got, "<genericError>second</genericError>")
		assert.NotContains(t, got, "<genericError>first</genericError>")
	})

	t.Run("nested errors resolve in the same document", func(t *testing.T) {
		t.Parallel()

		err := docs[1].WrapError(niceyaml.NewError(
			"validation failed",
			niceyaml.WithErrors(
				niceyaml.NewError("bad name", niceyaml.WithPath(namePath)),
			),
		))

		got := trimLines(render(err))

		assert.Contains(t, got, "<genericError>second</genericError>")
		assert.Contains(t, got, "^ bad name")
		assert.NotContains(t, got, "<genericError>first</genericError>")
	})

	t.Run("the source binds to its single document", func(t *testing.T) {
		t.Parallel()

		single := xmlSource("name: only\n")
		err := single.WrapError(niceyaml.NewError("bad name", niceyaml.WithPath(namePath)))

		got := trimLines(render(err))

		assert.True(t, strings.HasPrefix(got, "[1:7] $.name: bad name"), got)
		assert.Contains(t, got, "<genericError>only</genericError>")
	})

	t.Run("a path bound through a multi-document source names the reason", func(t *testing.T) {
		t.Parallel()

		err := source.WrapError(niceyaml.NewError("bad name", niceyaml.WithPath(namePath)))

		// The message carries no position, and the detail says why the
		// excerpt is missing rather than dropping it without a trace.
		assert.Equal(t, "$.name: bad name", err.Error())
		assert.Equal(t,
			"$.name: bad name\n\nno excerpt: [2:1] multiple documents in source: 2 documents",
			render(err),
		)

		var bound *niceyaml.SourceError

		require.ErrorAs(t, err, &bound)

		_, err = bound.Location()
		require.ErrorIs(t, err, niceyaml.ErrMultipleDocuments)
	})
}

func TestError_DoesNotMutateSource(t *testing.T) {
	t.Parallel()

	source := xmlSource(stringtest.Input(`
		a: 1
		b: 2
		c: 3
	`))

	err := source.WrapError(niceyaml.NewError(
		"main",
		niceyaml.WithErrors(
			niceyaml.NewError("nested", niceyaml.WithPath(paths.Root().Child("b").Value())),
		),
	))

	first := render(err)
	second := render(err)

	// Rendering is idempotent.
	assert.Equal(t, first, second)
	assert.Equal(t, 1, strings.Count(second, "^ nested"))
	assert.Equal(t, 1, strings.Count(second, "<genericError>2</genericError>"))

	// The caller's Source is untouched: a fresh view still renders
	// undecorated.
	view := source.View()
	for i := range view.AllLines() {
		assert.Empty(t, view.Overlays(i))
		assert.Empty(t, view.Annotations(i))
	}
}

func TestError_With(t *testing.T) {
	t.Parallel()

	base := niceyaml.NewError("bad key")
	located := base.With(niceyaml.WithPath(paths.Root().Child("key").Key()))

	// The copy carries the new option and the receiver keeps its own.
	_, set := base.Path()
	assert.False(t, set)

	got, set := located.Path()
	assert.True(t, set)
	assert.Equal(t, "$.key", got.String())

	assert.Equal(t, "bad key", base.Error())
	assert.Equal(t, "$.key: bad key", located.Error())
}

func TestError_WrappedContext(t *testing.T) {
	t.Parallel()

	source := niceyaml.NewSourceFromString("name: first\n---\nname: second\n")

	docs, err := source.Documents()
	require.NoError(t, err)
	require.Len(t, docs, 2)

	inner := niceyaml.NewError("bad name", niceyaml.WithPath(paths.Root().Child("name").Value()))

	wrapped := docs[1].WrapError(fmt.Errorf("document 1: %w", inner))

	// The outer context stays as the wrapper wrote it, and the position the
	// path resolves to goes in front of the whole message.
	assert.Equal(t, "[3:7] document 1: $.name: bad name", wrapped.Error())
	assert.Equal(t, "[3:7] document 1: $.name: bad name", fmt.Sprintf("%v", wrapped))
	require.ErrorIs(t, wrapped, inner)

	// Binding first keeps the position beside the message under the context.
	boundFirst := fmt.Errorf("document 1: %w", docs[1].WrapError(inner))
	assert.Equal(t, "document 1: [3:7] $.name: bad name", boundFirst.Error())

	var got *niceyaml.Error

	require.ErrorAs(t, wrapped, &got)

	gotPath, ok := got.Path()
	require.True(t, ok)
	assert.Equal(t, "$.name", gotPath.String())

	var bound *niceyaml.SourceError

	require.ErrorAs(t, wrapped, &bound)

	excerpt, err := bound.Excerpt(2)
	require.NoError(t, err)

	detail := newXMLPrinter().Print(excerpt)
	assert.Contains(t, detail, "second")
	assert.NotContains(t, detail, "^")

	// Wrapping a direct Error resolves its position in the message.
	direct := docs[1].WrapError(inner)
	assert.Equal(t, "[3:7] $.name: bad name", direct.Error())

	// Wrapping twice renders the same output.
	twice := docs[1].WrapError(direct)
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
	located := niceyaml.NewError("bad name", niceyaml.WithPath(paths.Root().Child("name").Value()))
	wrapped := docs[1].WrapError(niceyaml.NewErrorFrom(fmt.Errorf("validate: %w", located)))

	// The producer's context stays as written, behind the resolved position.
	assert.Equal(t, "[3:7] validate: $.name: bad name", wrapped.Error())

	// The highlight lands on the second document's value, not the first's.
	var bound *niceyaml.SourceError

	require.ErrorAs(t, wrapped, &bound)

	excerpt, err := bound.Excerpt(2)
	require.NoError(t, err)

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
	badA := niceyaml.NewError("bad a", niceyaml.WithPath(paths.Root().Child("a").Value()))
	badB := niceyaml.NewError("bad b", niceyaml.WithPath(paths.Root().Child("b").Value()))
	inner := niceyaml.NewError(
		"validation failed at 2 locations",
		niceyaml.WithErrors(badA, badB),
	)

	wrapped := source.WrapError(fmt.Errorf("document 0: %w", inner))

	// The message is the headline alone. The nested errors are reachable
	// through Unwrap and appear in the %+v form as annotations.
	assert.Equal(t, "document 0: validation failed at 2 locations", wrapped.Error())
	require.ErrorIs(t, wrapped, badA)
	require.ErrorIs(t, wrapped, badB)

	var bound *niceyaml.SourceError

	require.ErrorAs(t, wrapped, &bound)

	got := trimLines(plain.PrintError(bound, 2))

	assert.Equal(t, "document 0: validation failed at 2 locations", strings.SplitN(got, "\n", 2)[0])
	assert.NotContains(t, got, "$.a")
	assert.Contains(t, got, "^ bad a")
	assert.Contains(t, got, "^ bad b")
}

func TestError_NestedErrorChains(t *testing.T) {
	t.Parallel()

	// A nested error is a chain like the main one: it resolves through the
	// Error inside it, in the document the binder picked, and annotates the
	// line with its message alone.

	t.Run("location behind foreign wrapping resolves", func(t *testing.T) {
		t.Parallel()

		source := xmlSource("a: 1\nb: 2\nc: 3\n")
		err := source.WrapError(niceyaml.NewError(
			"outer",
			niceyaml.WithPath(paths.Root().Child("a").Value()),
			niceyaml.WithErrors(
				niceyaml.NewErrorFrom(fmt.Errorf("ctx: %w",
					niceyaml.NewError("bad", niceyaml.WithPath(paths.Root().Child("b").Value())),
				)),
			),
		))

		var bound *niceyaml.SourceError

		require.ErrorAs(t, err, &bound)

		excerpt, excerptErr := bound.Excerpt(2)
		require.NoError(t, excerptErr)

		got := trimLines(newXMLPrinter().Print(excerpt))
		assert.Contains(t, got, "<genericError>2</genericError>")
		assert.Contains(t, got, "^ ctx: $.b: bad")

		// Nothing is left unresolved, so the excerpt is the whole output.
		assert.Equal(t, "[1:4] $.a: outer\n\n"+got, trimLines(render(err)))
	})

	t.Run("annotation drops the position the caret marks", func(t *testing.T) {
		t.Parallel()

		source := xmlSource("a: 1\nb: 2\n")
		tk := source.Lines().TokenAt(position.New(1, 0))
		require.NotNil(t, tk)

		err := source.WrapError(niceyaml.NewError(
			"outer",
			niceyaml.WithPath(paths.Root().Child("a").Value()),
			niceyaml.WithErrors(
				niceyaml.NewErrorFrom(niceyaml.NewError("inner", niceyaml.WithToken(tk))),
				niceyaml.NewErrorFrom(niceyaml.NewError("also", niceyaml.WithPath(paths.Root().Child("b").Value()))),
			),
		))

		got := trimLines(render(err))
		assert.Contains(t, got, "^ inner; also")
		assert.NotContains(t, got, "[2:1]")
		assert.NotContains(t, got, "$.b")
	})
}

func TestError_NestedMessageSpansLines(t *testing.T) {
	t.Parallel()

	source := niceyaml.NewSourceFromString("a: 1\nb: 2\n")
	err := source.WrapError(niceyaml.NewError(
		"validation failed",
		niceyaml.WithErrors(
			niceyaml.NewError(
				"bad a\n  see docs for details",
				niceyaml.WithPath(paths.Root().Child("a").Value()),
			),
		),
	))

	// The nested message stays out of the headline whole, continuation line
	// included, and appears only in the annotation.
	assert.Equal(t, "validation failed", err.Error())

	got := trimLines(render(err))
	headline, detail, _ := strings.Cut(got, "\n\n")

	assert.Equal(t, "validation failed", headline)
	assert.Contains(t, detail, "see docs for details")
}

// reformatError wraps another error without embedding its text, the way a
// wrapper that rewrites the message it wraps does.
type reformatError struct{ err error }

func (r reformatError) Error() string { return "rewritten" }
func (r reformatError) Unwrap() error { return r.err }

func TestSourceError_KeepsWrappedText(t *testing.T) {
	t.Parallel()

	source := niceyaml.NewSourceFromString("name: first\n---\nname: second\n")
	namePath := paths.Root().Child("name").Value()

	docs, err := source.Documents()
	require.NoError(t, err)
	require.Len(t, docs, 2)

	t.Run("nested wrappers keep their text behind the position", func(t *testing.T) {
		t.Parallel()

		inner := niceyaml.NewError("bad name", niceyaml.WithPath(namePath))
		wrapped := docs[0].WrapError(fmt.Errorf("outer: %w", fmt.Errorf("inner: %w", inner)))

		assert.Equal(t, "[1:7] outer: inner: $.name: bad name", wrapped.Error())
	})

	t.Run("a bound join reports the position of its first branch", func(t *testing.T) {
		t.Parallel()

		first := niceyaml.NewError("bad first", niceyaml.WithPath(namePath))
		second := niceyaml.NewError("bad second", niceyaml.WithPath(namePath))
		wrapped := docs[0].WrapError(errors.Join(
			fmt.Errorf("a: %w", first),
			fmt.Errorf("b: %w", second),
		))

		// A SourceError has one location, the first Error's, and the text
		// of the other branch stays as the join wrote it.
		assert.Equal(t, "[1:7] a: $.name: bad first\nb: $.name: bad second", wrapped.Error())
		require.ErrorIs(t, wrapped, second)
	})

	t.Run("binding each branch reports every position", func(t *testing.T) {
		t.Parallel()

		first := niceyaml.NewError("bad first", niceyaml.WithPath(namePath))
		second := niceyaml.NewError("bad second", niceyaml.WithPath(namePath))
		joined := errors.Join(
			fmt.Errorf("a: %w", docs[0].WrapError(first)),
			fmt.Errorf("b: %w", docs[1].WrapError(second)),
		)

		// Each branch resolves in the document that bound it.
		assert.Equal(t, "a: [1:7] $.name: bad first\nb: [3:7] $.name: bad second", joined.Error())
	})

	t.Run("a wrapper that rewrites the message still gets the position", func(t *testing.T) {
		t.Parallel()

		inner := niceyaml.NewError("bad name", niceyaml.WithPath(namePath))
		wrapped := docs[0].WrapError(fmt.Errorf("outer: %w", reformatError{inner}))

		// The position comes from the Error in the chain, not from its text,
		// so a wrapper that hides the text does not hide the position.
		assert.Equal(t, "[1:7] outer: rewritten", wrapped.Error())
		require.ErrorIs(t, wrapped, inner)
	})

	t.Run("a token error gets its position in front of the wrapper's text", func(t *testing.T) {
		t.Parallel()

		tk := source.Lines().TokenAt(position.New(2, 6))
		inner := niceyaml.NewError("bad token", niceyaml.WithToken(tk))
		wrapped := docs[0].WrapError(fmt.Errorf("document 1: %w", inner))

		// The Error carries no position in its text, and binding puts the
		// token's in front of the whole message, as it does for a path.
		assert.Equal(t, "bad token", inner.Error())
		assert.Equal(t, "[3:7] document 1: bad token", wrapped.Error())
	})

	t.Run("an anchor with nested errors takes the position of the error it wraps", func(t *testing.T) {
		t.Parallel()

		tk := source.Lines().TokenAt(position.New(2, 6))
		nested := niceyaml.NewError("bad name", niceyaml.WithPath(namePath))

		tcs := map[string]struct {
			err     *niceyaml.Error
			unbound string
			bound   string
		}{
			"direct": {
				err: niceyaml.NewErrorFrom(
					niceyaml.NewError("bad token", niceyaml.WithToken(tk)),
					niceyaml.WithErrors(nested),
				),
				unbound: "bad token",
				bound:   "[3:7] bad token",
			},
			"behind context": {
				err: niceyaml.NewErrorFrom(
					fmt.Errorf("document 1: %w", niceyaml.NewError("bad token", niceyaml.WithToken(tk))),
					niceyaml.WithErrors(nested),
				),
				unbound: "document 1: bad token",
				bound:   "[3:7] document 1: bad token",
			},
			"range": {
				err: niceyaml.NewErrorFrom(
					niceyaml.NewError("bad range",
						niceyaml.WithRange(position.NewRange(position.New(2, 6), position.New(2, 12))),
					),
					niceyaml.WithErrors(nested),
				),
				unbound: "bad range",
				bound:   "[3:7] bad range",
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				// The nested errors make the outer Error the anchor, and it
				// takes the location of the Error it wraps.
				assert.Equal(t, tc.unbound, tc.err.Error())
				assert.Equal(t, tc.bound, docs[0].WrapError(tc.err).Error())
			})
		}
	})

	t.Run("a path anchor resolves its path rather than the error it wraps", func(t *testing.T) {
		t.Parallel()

		tk := source.Lines().TokenAt(position.New(2, 6))
		inner := niceyaml.NewError("bad token", niceyaml.WithToken(tk))
		outer := niceyaml.NewErrorFrom(inner, niceyaml.WithPath(namePath))

		// The path anchor resolves in the first document, and the message
		// carries that one position, which agrees with the highlight.
		assert.Equal(t, "$.name: bad token", outer.Error())
		assert.Equal(t, "[1:7] $.name: bad token", docs[0].WrapError(outer).Error())
	})

	t.Run("a second binding adds no position", func(t *testing.T) {
		t.Parallel()

		inner := niceyaml.NewError("bad name", niceyaml.WithPath(namePath))
		once := docs[0].WrapError(inner)
		wrapper := fmt.Errorf("document 0: %w", once)
		twice := docs[0].WrapError(wrapper)

		// The chain is bound to this source already, so the second binding
		// returns the wrapper as it is.
		assert.Same(t, wrapper, twice)
		assert.Equal(t, "document 0: [1:7] $.name: bad name", twice.Error())
	})
}

func TestError_ResolvesThroughErrorWrappers(t *testing.T) {
	t.Parallel()

	source := niceyaml.NewSourceFromString("name: first\n---\nname: second\n")
	located := niceyaml.NewError("bad name", niceyaml.WithPath(paths.Root().Child("name").Value()))

	docs, err := source.Documents()
	require.NoError(t, err)
	require.Len(t, docs, 2)

	// Error wrappers add no message text of their own, so the document
	// attached above them still resolves the location in the message.
	wrapped := docs[0].WrapError(niceyaml.NewErrorFrom(located))

	assert.Equal(t, "[1:7] $.name: bad name", wrapped.Error())

	var got *niceyaml.SourceError

	require.ErrorAs(t, wrapped, &got)

	excerpt, err := got.Excerpt(2)
	require.NoError(t, err)
	require.NotNil(t, excerpt)
	assert.Positive(t, excerpt.Len())
}

func TestError_RangeRendersFromSource(t *testing.T) {
	t.Parallel()

	source := xmlSource("key: some value\n")
	rng := position.NewRange(position.New(0, 5), position.New(0, 9))

	err := source.WrapError(niceyaml.NewError("bad word", niceyaml.WithRange(rng)))

	// The headline is 1-indexed, and the highlight covers the range rather
	// than the token under it.
	got := trimLines(render(err))

	assert.Equal(t, stringtest.JoinLF(
		"[1:6] bad word",
		"",
		"<nameTag>key</nameTag><punctuationMappingValue>:</punctuationMappingValue><text> </text><genericError>some</genericError><literalString> value</literalString>",
	), got)

	// A range puts no position in the message until a source binds it.
	bare := niceyaml.NewError("bad word", niceyaml.WithRange(rng))
	assert.Equal(t, "bad word", bare.Error())
}

func TestSourceError_Location(t *testing.T) {
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
		doc  int
	}{
		"path targets the value token": {
			err:  niceyaml.NewError("bad", niceyaml.WithPath(paths.Root().Child("value").Value())),
			want: position.NewRange(position.New(1, 7), position.New(1, 10)),
		},
		"path targets the key token": {
			err:  niceyaml.NewError("bad", niceyaml.WithPath(paths.Root().Child("value").Key())),
			want: position.NewRange(position.New(1, 0), position.New(1, 5)),
		},
		"path resolves in the bound document": {
			err:  niceyaml.NewError("bad", niceyaml.WithPath(paths.Root().Child("name").Value())),
			want: position.NewRange(position.New(3, 6), position.New(3, 12)),
			doc:  1,
		},
		"range is returned as given": {
			err: niceyaml.NewError("bad",
				niceyaml.WithRange(position.NewRange(position.New(0, 1), position.New(0, 3))),
			),
			want: position.NewRange(position.New(0, 1), position.New(0, 3)),
		},
		"token covers its content": {
			err: niceyaml.NewError("bad", niceyaml.WithToken(&token.Token{
				Position: &token.Position{Line: 1, Column: 7},
			})),
			want: position.NewRange(position.New(0, 6), position.New(0, 10)),
		},
		"no location": {
			err: niceyaml.NewError("bad"),
			is:  niceyaml.ErrNoLocation,
		},
		"token without position": {
			err: niceyaml.NewError("bad", niceyaml.WithToken(&token.Token{})),
			is:  niceyaml.ErrTokenNotFound,
		},
		"path that does not resolve": {
			err: niceyaml.NewError("bad", niceyaml.WithPath(paths.Root().Child("missing").Value())),
			is:  paths.ErrNotFound,
		},
		"range past the last line": {
			err: niceyaml.NewError("bad",
				niceyaml.WithRange(position.NewRange(position.New(9, 0), position.New(9, 3))),
			),
			is: niceyaml.ErrOutOfRange,
		},
		"range before the first line": {
			err: niceyaml.NewError("bad",
				niceyaml.WithRange(position.NewRange(position.New(-1, 0), position.New(-1, 2))),
			),
			is: niceyaml.ErrOutOfRange,
		},
		"token from other text": {
			err: niceyaml.NewError("bad", niceyaml.WithToken(&token.Token{
				Position: &token.Position{Line: 9, Column: 1},
			})),
			is: niceyaml.ErrOutOfRange,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var bound *niceyaml.SourceError

			require.ErrorAs(t, docs[tc.doc].WrapError(tc.err), &bound)

			got, err := bound.Location()
			if tc.is != nil {
				require.ErrorIs(t, err, tc.is)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestSourceError_Location_MultiLineToken(t *testing.T) {
	t.Parallel()

	source := xmlSource(stringtest.Input(`
		text: first
		  second
	`))

	var bound *niceyaml.SourceError

	require.ErrorAs(t, source.WrapError(niceyaml.NewError("bad",
		niceyaml.WithPath(paths.Root().Child("text").Value()),
	)), &bound)

	got, err := bound.Location()
	require.NoError(t, err)

	// The plain scalar continues on the second line, so the range ends there.
	assert.Equal(t, position.NewRange(position.New(0, 6), position.New(1, 8)), got)
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
			is:         niceyaml.ErrNoLocation,
			wantRender: "bad",
		},
		"path that does not resolve": {
			err:        niceyaml.NewError("bad", niceyaml.WithPath(paths.Root().Child("missing").Value())),
			is:         paths.ErrNotFound,
			wantRender: "$.missing: bad\n\nno excerpt: resolve $.missing: not found",
		},
		"range past the last line": {
			err: niceyaml.NewError("bad",
				niceyaml.WithRange(position.NewRange(position.New(9, 0), position.New(9, 3))),
			),
			is:         niceyaml.ErrOutOfRange,
			wantRender: "[10:1] bad\n\nno excerpt: location outside source: line 10 not in lines 1-2",
		},
		"range before the first line": {
			err: niceyaml.NewError("bad",
				niceyaml.WithRange(position.NewRange(position.New(-1, 0), position.New(-1, 2))),
			),
			is:         niceyaml.ErrOutOfRange,
			wantRender: "[0:1] bad\n\nno excerpt: location outside source: line 0 not in lines 1-2",
		},
		"nested range before the first line": {
			err: niceyaml.NewError("bad",
				niceyaml.WithPath(paths.Root().Child("missing").Value()),
				niceyaml.WithErrors(
					niceyaml.NewError("first",
						niceyaml.WithRange(position.NewRange(position.New(-1, 0), position.New(-1, 2))),
					),
				),
			),
			is:         niceyaml.ErrOutOfRange,
			wantRender: "$.missing: bad\n\nno excerpt: resolve $.missing: not found\n\nfirst",
		},
		"every nested error unresolved": {
			err: niceyaml.NewError("bad", niceyaml.WithErrors(
				niceyaml.NewError("first", niceyaml.WithPath(paths.Root().Child("missing").Value())),
				niceyaml.NewError("second", niceyaml.WithToken(&token.Token{})),
			)),
			is:         niceyaml.ErrTokenNotFound,
			wantRender: "bad\n\n$.missing: first\nsecond",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var bound *niceyaml.SourceError

			require.ErrorAs(t, source.WrapError(tc.err), &bound)

			got, err := bound.Excerpt(2)
			require.ErrorIs(t, err, tc.is)
			assert.Nil(t, got)

			// With no excerpt to show, the detail names why the location did
			// not resolve, unless the error carries none, and lists any nested
			// errors the excerpt would have annotated.
			assert.Equal(t, tc.wantRender, render(bound))
		})
	}

	t.Run("one resolved location renders without error", func(t *testing.T) {
		t.Parallel()

		var bound *niceyaml.SourceError

		require.ErrorAs(t, source.WrapError(niceyaml.NewError("bad", niceyaml.WithErrors(
			niceyaml.NewError("first", niceyaml.WithPath(paths.Root().Child("missing").Value())),
			niceyaml.NewError("second", niceyaml.WithPath(paths.Root().Child("value").Value())),
		))), &bound)

		excerpt, err := bound.Excerpt(2)
		require.NoError(t, err)

		got := newXMLPrinter().Print(excerpt)
		assert.Contains(t, got, "^ second")
		assert.NotContains(t, got, "first")
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

	err := niceyaml.NewSourceFromString(excerptSource).WrapError(niceyaml.NewError(
		"bad b",
		niceyaml.WithPath(paths.Root().Child("b").Value()),
		niceyaml.WithErrors(
			niceyaml.NewError("bad h", niceyaml.WithPath(paths.Root().Child("h").Value())),
		),
	))

	var bound *niceyaml.SourceError

	require.ErrorAs(t, err, &bound)

	return bound
}

// lineNumbers returns the source line number of every line in view.
func lineNumbers(view *line.View) []int {
	numbers := make([]int, 0, view.Len())
	for _, l := range view.AllLines() {
		numbers = append(numbers, l.Number())
	}

	return numbers
}

func TestSourceError_Excerpt(t *testing.T) {
	t.Parallel()

	t.Run("holds only the hunk lines with their source numbers", func(t *testing.T) {
		t.Parallel()

		excerpt, err := excerptError(t).Excerpt(1)
		require.NoError(t, err)

		assert.Equal(t, []int{1, 2, 3, 7, 8, 9}, lineNumbers(excerpt))
		assert.Equal(t, "b: 2", excerpt.Line(1).Content())
		assert.Equal(t, "h: 8", excerpt.Line(4).Content())
	})

	t.Run("overlays the error ranges with the error style", func(t *testing.T) {
		t.Parallel()

		excerpt, err := excerptError(t).Excerpt(1)
		require.NoError(t, err)

		want := line.Overlays{{Style: style.GenericError, Cols: position.NewSpan(3, 4)}}
		assert.Equal(t, want, excerpt.Overlays(1), "the main error covers the value of b")
		assert.Equal(t, want, excerpt.Overlays(4), "the nested error covers the value of h")

		for _, i := range []int{0, 2, 3, 5} {
			assert.Empty(t, excerpt.Overlays(i), "line %d carries no overlay", i)
		}
	})

	t.Run("annotates nested messages below their lines", func(t *testing.T) {
		t.Parallel()

		excerpt, err := excerptError(t).Excerpt(1)
		require.NoError(t, err)

		assert.Equal(t, line.Annotations{
			{Content: "bad h", Placement: line.Below, Col: 3},
		}, excerpt.Annotations(4).Filter(line.Below))
		assert.Empty(t, excerpt.Annotations(1), "the main error has no message of its own")
	})

	t.Run("separates hunks after the first with an ellipsis", func(t *testing.T) {
		t.Parallel()

		excerpt, err := excerptError(t).Excerpt(1)
		require.NoError(t, err)

		assert.Equal(t, line.Annotations{
			{Content: "...", Placement: line.Above},
		}, excerpt.Annotations(3))
		assert.Empty(t, excerpt.Annotations(0), "the first hunk has no separator")

		for _, i := range []int{1, 2, 4, 5} {
			assert.Empty(t, excerpt.Annotations(i).Filter(line.Above), "line %d has no separator", i)
		}
	})

	t.Run("prints as the render body", func(t *testing.T) {
		t.Parallel()

		bound := excerptError(t)

		excerpt, err := bound.Excerpt(1)
		require.NoError(t, err)

		want := stringtest.JoinLF(
			"<nameTag>a</nameTag><punctuationMappingValue>:</punctuationMappingValue><text> </text><literalNumberInteger>1</literalNumberInteger>",
			"<nameTag>b</nameTag><punctuationMappingValue>:</punctuationMappingValue><text> </text><genericError>2</genericError>",
			"<nameTag>c</nameTag><punctuationMappingValue>:</punctuationMappingValue><text> </text><literalNumberInteger>3</literalNumberInteger>",
			"<comment>...</comment>",
			"<nameTag>g</nameTag><punctuationMappingValue>:</punctuationMappingValue><text> </text><literalNumberInteger>7</literalNumberInteger>",
			"<nameTag>h</nameTag><punctuationMappingValue>:</punctuationMappingValue><text> </text><genericError>8</genericError>",
			"<comment>   ^ bad h</comment>",
			"<nameTag>i</nameTag><punctuationMappingValue>:</punctuationMappingValue><text> </text><literalNumberInteger>9</literalNumberInteger>",
		)

		got := trimLines(newXMLPrinter().Print(excerpt))
		assert.Equal(t, want, got)
		assert.Equal(t, bound.Error()+"\n\n"+got, trimLines(renderContext(bound, 1)))
	})

	t.Run("negative context shows the marked lines alone", func(t *testing.T) {
		t.Parallel()

		bound := excerptError(t)

		zero, err := bound.Excerpt(0)
		require.NoError(t, err)

		negative, err := bound.Excerpt(-1)
		require.NoError(t, err)

		assert.Equal(t, []int{2, 8}, lineNumbers(zero))
		assert.Equal(t, lineNumbers(zero), lineNumbers(negative))
		assert.Equal(t, newXMLPrinter().Print(zero), newXMLPrinter().Print(negative))
	})

	t.Run("no location", func(t *testing.T) {
		t.Parallel()

		var bound *niceyaml.SourceError

		require.ErrorAs(t, niceyaml.NewSourceFromString(excerptSource).WrapError(niceyaml.NewError("bad")), &bound)

		excerpt, err := bound.Excerpt(2)
		require.ErrorIs(t, err, niceyaml.ErrNoLocation)
		assert.Nil(t, excerpt)
	})
}

func TestSourceError_Annotate(t *testing.T) {
	t.Parallel()

	// Assert that no line of view carries an overlay or annotation.
	unmarked := func(t *testing.T, view *line.View) {
		t.Helper()

		for i := range view.Len() {
			assert.Empty(t, view.Overlays(i), "line %d carries no overlay", i)
			assert.Empty(t, view.Annotations(i), "line %d carries no annotation", i)
		}
	}

	t.Run("marks the full view as the excerpt is marked", func(t *testing.T) {
		t.Parallel()

		bound := excerptError(t)
		view := bound.Source().View()

		require.NoError(t, bound.Annotate(view))

		assert.Equal(t, []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}, lineNumbers(view))

		want := line.Overlays{{Style: style.GenericError, Cols: position.NewSpan(3, 4)}}
		assert.Equal(t, want, view.Overlays(1))
		assert.Equal(t, want, view.Overlays(7))
		assert.Equal(t, line.Annotations{
			{Content: "bad h", Placement: line.Below, Col: 3},
		}, view.Annotations(7))

		for i := range view.Len() {
			if i == 1 || i == 7 {
				continue
			}

			assert.Empty(t, view.Overlays(i), "line %d carries no overlay", i)
			assert.Empty(t, view.Annotations(i), "line %d carries no annotation", i)
		}

		// The excerpt is the marked view cut down to its hunks.
		excerpt, err := bound.Excerpt(0)
		require.NoError(t, err)
		assert.Equal(t, view.Overlays(1), excerpt.Overlays(0))
		assert.Equal(t, view.Overlays(7), excerpt.Overlays(1))
		assert.Equal(t, view.Annotations(7), excerpt.Annotations(1).Filter(line.Below))
	})

	t.Run("two errors annotate one view", func(t *testing.T) {
		t.Parallel()

		source := niceyaml.NewSourceFromString(excerptSource)
		view := source.View()

		var first, second *niceyaml.SourceError

		require.ErrorAs(t, source.WrapError(niceyaml.NewError(
			"bad b", niceyaml.WithPath(paths.Root().Child("b").Value()),
		)), &first)
		require.ErrorAs(t, source.WrapError(niceyaml.NewError(
			"bad d",
			niceyaml.WithErrors(niceyaml.NewError("too big", niceyaml.WithPath(paths.Root().Child("d").Value()))),
		)), &second)

		require.NoError(t, first.Annotate(view))
		require.NoError(t, second.Annotate(view))

		want := line.Overlays{{Style: style.GenericError, Cols: position.NewSpan(3, 4)}}
		assert.Equal(t, want, view.Overlays(1))
		assert.Equal(t, want, view.Overlays(3))
		assert.Empty(t, view.Annotations(1))
		assert.Equal(t, line.Annotations{
			{Content: "too big", Placement: line.Below, Col: 3},
		}, view.Annotations(3))
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
				is:  niceyaml.ErrNoLocation,
			},
			"path that does not resolve": {
				err: niceyaml.NewError("bad", niceyaml.WithPath(paths.Root().Child("missing").Value())),
				is:  paths.ErrNotFound,
			},
			"range past the last line": {
				err: niceyaml.NewError("bad",
					niceyaml.WithRange(position.NewRange(position.New(20, 0), position.New(20, 1))),
				),
				is: niceyaml.ErrOutOfRange,
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				var bound *niceyaml.SourceError

				require.ErrorAs(t, source.WrapError(tc.err), &bound)

				view := source.View()

				require.ErrorIs(t, bound.Annotate(view), tc.is)
				unmarked(t, view)
			})
		}
	})
}
