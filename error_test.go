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
	"go.jacobcolvin.com/niceyaml/paths"
	"go.jacobcolvin.com/niceyaml/style"
)

// customTestError is a test error type for errors.As testing.
type customTestError struct {
	msg string
}

func (e *customTestError) Error() string {
	return e.msg
}

// render formats err with %+v, which prints the annotated source when the
// error resolves to a location and the plain message otherwise.
func render(err error) string {
	return fmt.Sprintf("%+v", err)
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
			err: niceyaml.NewError(
				"invalid value",
				niceyaml.WithPath(paths.Root().Child("key").Key()),
				niceyaml.WithSource(niceyaml.NewSourceFromTokens(tokens)),
				niceyaml.WithPrinter(niceyaml.NewPrinter(
					niceyaml.WithStyles(yamltest.NewXMLStyles()),
					niceyaml.WithGutter(niceyaml.NoGutter),
					niceyaml.WithContainerStyle(lipgloss.NewStyle()),
				)),
			),
			want: stringtest.JoinLF(
				"[3:1] invalid value",
				"",
				"<nameTag>a</nameTag><punctuationMappingValue>:</punctuationMappingValue><text> </text><literalString>b</literalString>",
				"<nameTag>foo</nameTag><punctuationMappingValue>:</punctuationMappingValue><text> </text><literalString>bar</literalString>",
				"<genericError>key</genericError><punctuationMappingValue>:</punctuationMappingValue><text> </text><literalString>value</literalString>",
			),
		},
		"with direct token bypasses path resolution": {
			err: niceyaml.NewError(
				"bad token",
				niceyaml.WithErrorToken(tokens[0]),
				niceyaml.WithPrinter(niceyaml.NewPrinter(
					niceyaml.WithStyles(yamltest.NewXMLStyles()),
					niceyaml.WithGutter(niceyaml.NoGutter),
					niceyaml.WithContainerStyle(lipgloss.NewStyle()),
				)),
			),
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

	newXMLPrinter := func() *niceyaml.Printer {
		return niceyaml.NewPrinter(
			niceyaml.WithStyles(yamltest.NewXMLStyles()),
			niceyaml.WithGutter(niceyaml.NoGutter),
			niceyaml.WithContainerStyle(lipgloss.NewStyle()),
		)
	}

	tcs := map[string]struct {
		opts        func() []niceyaml.ErrorOption
		inputErr    func() error
		wantExact   string
		wantNil     bool
		wantSameErr bool
	}{
		"wrap nil returns nil": {
			opts:     func() []niceyaml.ErrorOption { return nil },
			inputErr: func() error { return nil },
			wantNil:  true,
		},
		"wrap non-error type returns unchanged": {
			opts:        func() []niceyaml.ErrorOption { return nil },
			inputErr:    func() error { return errors.New("plain error") },
			wantSameErr: true,
		},
		"wrap error applies options and source": {
			opts: func() []niceyaml.ErrorOption {
				return []niceyaml.ErrorOption{
					niceyaml.WithPrinter(newXMLPrinter()),
				}
			},
			inputErr: func() error {
				return niceyaml.NewError(
					"test error",
					niceyaml.WithPath(paths.Root().Child("name").Key()),
				)
			},
			wantExact: stringtest.JoinLF(
				"[1:1] test error",
				"",
				"<genericError>name</genericError><punctuationMappingValue>:</punctuationMappingValue><text> </text><literalString>test</literalString>",
				"<nameTag>value</nameTag><punctuationMappingValue>:</punctuationMappingValue><text> </text><literalNumberInteger>123</literalNumberInteger>",
			),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			source := niceyaml.NewSourceFromString(sourceInput, niceyaml.WithErrorOptions(tc.opts()...))
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

func TestGetPath(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		err  *niceyaml.Error
		want string
	}{
		"nil path returns empty string": {
			err:  niceyaml.NewError("test"),
			want: "",
		},
		"returns path string when set": {
			err: niceyaml.NewError(
				"test",
				niceyaml.WithPath(paths.Root().Child("foo").Key()),
			),
			want: "$.foo",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := tc.err.Path()
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestError_GracefulDegradation(t *testing.T) {
	t.Parallel()

	// Create tokens from a simple source for tests that need them.
	source := `key: value`
	tokens := lexer.Tokenize(source)

	// Empty tokens for edge case tests.
	emptyTokens := lexer.Tokenize("")

	tcs := map[string]struct {
		err  *niceyaml.Error
		want string
	}{
		"invalid path": {
			err: niceyaml.NewError(
				"not found",
				niceyaml.WithPath(paths.Root().Child("nonexistent").Key()),
				niceyaml.WithSource(niceyaml.NewSourceFromTokens(tokens)),
			),
			want: "at $.nonexistent: not found",
		},
		"path without source": {
			err: niceyaml.NewError(
				"missing source",
				niceyaml.WithPath(paths.Root().Child("key").Key()),
			),
			want: "at $.key: missing source",
		},
		"empty source": {
			err: niceyaml.NewError(
				"error in empty source",
				niceyaml.WithPath(paths.Root().Child("key").Key()),
				niceyaml.WithSource(niceyaml.NewSourceFromTokens(emptyTokens)),
			),
			want: "at $.key: error in empty source",
		},
		"nil source": {
			err: niceyaml.NewError(
				"nil source error",
				niceyaml.WithPath(paths.Root().Child("key").Key()),
				niceyaml.WithSource(nil),
			),
			want: "at $.key: nil source error",
		},
		"nonexistent path in source": {
			err: niceyaml.NewError(
				"path not found",
				niceyaml.WithPath(
					paths.Root().Child("nonexistent").Child("deep").Key(),
				),
				niceyaml.WithSource(niceyaml.NewSourceFromTokens(tokens)),
			),
			want: "at $.nonexistent.deep: path not found",
		},
		"empty document source": {
			// Tests graceful handling when source has no documents (Docs slice is empty).
			err: niceyaml.NewError(
				"empty doc error",
				niceyaml.WithPath(paths.Root().Child("key").Key()),
				niceyaml.WithSource(niceyaml.NewSourceFromTokens(emptyTokens)),
			),
			want: "at $.key: empty doc error",
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
		path         *paths.Path
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
				"[2:3] nested error",
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
				"[2:5] array error",
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
				"[2:5] nested array error",
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
				"[1:1] root error",
				"",
				"<genericError>key</genericError><punctuationMappingValue>:</punctuationMappingValue><text> </text><literalString>value</literalString>",
			),
		},
		"single top-level key path": {
			source: "key: value",
			path:   paths.Root().Child("key").Key(),
			errMsg: "top level error",
			want: stringtest.JoinLF(
				"[1:1] top level error",
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
				"[3:1] middle error",
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

			opts := []niceyaml.ErrorOption{
				niceyaml.WithPath(tc.path),
				niceyaml.WithSource(niceyaml.NewSourceFromString(tc.source)),
				niceyaml.WithPrinter(niceyaml.NewPrinter(
					niceyaml.WithStyles(yamltest.NewXMLStyles()),
					niceyaml.WithGutter(niceyaml.NoGutter),
					niceyaml.WithContainerStyle(lipgloss.NewStyle()),
				)),
			}
			if tc.contextLines > 0 {
				opts = append(opts, niceyaml.WithContextLines(tc.contextLines))
			}

			err := niceyaml.NewError(tc.errMsg, opts...)

			assert.Equal(t, tc.want, trimLines(render(err)))
		})
	}
}

func TestErrorAnnotation_PathTargetValue(t *testing.T) {
	t.Parallel()

	newXMLPrinter := func() *niceyaml.Printer {
		return niceyaml.NewPrinter(
			niceyaml.WithStyles(yamltest.NewXMLStyles()),
			niceyaml.WithGutter(niceyaml.NoGutter),
			niceyaml.WithContainerStyle(lipgloss.NewStyle()),
		)
	}

	tcs := map[string]struct {
		path   *paths.Path
		source string
		errMsg string
		want   string
	}{
		"value selection highlights value token": {
			source: "key: value",
			path:   paths.Root().Child("key").Value(),
			errMsg: "invalid value",
			want: stringtest.JoinLF(
				"[1:6] invalid value",
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
				"[2:8] nested value error",
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
				"[2:5] array error",
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

			err := niceyaml.NewError(
				tc.errMsg,
				niceyaml.WithPath(tc.path),
				niceyaml.WithSource(niceyaml.NewSourceFromString(tc.source)),
				niceyaml.WithPrinter(newXMLPrinter()),
			)

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

	customPrinter := niceyaml.NewPrinter(
		niceyaml.WithStyles(yamltest.NewXMLStyles()),
		niceyaml.WithGutter(niceyaml.NoGutter),
		niceyaml.WithContainerStyle(lipgloss.NewStyle()),
	)

	err := niceyaml.NewError(
		"test error",
		niceyaml.WithErrorToken(tokens[0]),
		niceyaml.WithPrinter(customPrinter),
	)

	want := stringtest.JoinLF(
		"[1:1] test error",
		"",
		"<genericError>key</genericError><punctuationMappingValue>:</punctuationMappingValue><text> </text><literalString>value</literalString>",
		"<nameTag>foo</nameTag><punctuationMappingValue>:</punctuationMappingValue><text> </text><literalString>bar</literalString>",
	)
	assert.Equal(t, want, trimLines(render(err)))
}

func TestError_SpecialParentContext(t *testing.T) {
	t.Parallel()

	newXMLPrinter := func() *niceyaml.Printer {
		return niceyaml.NewPrinter(
			niceyaml.WithStyles(yamltest.NewXMLStyles()),
			niceyaml.WithGutter(niceyaml.NoGutter),
			niceyaml.WithContainerStyle(lipgloss.NewStyle()),
		)
	}

	tcs := map[string]struct {
		source string
		path   *paths.Path
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
				"[2:3] array element error",
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
				"[1:1] document root error",
				"",
				"<genericError>key</genericError><punctuationMappingValue>:</punctuationMappingValue><text> </text><literalString>value</literalString>",
				"<nameTag>another</nameTag><punctuationMappingValue>:</punctuationMappingValue><text> </text><literalString>line</literalString>",
			),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			err := niceyaml.NewError(
				tc.errMsg,
				niceyaml.WithPath(tc.path),
				niceyaml.WithSource(niceyaml.NewSourceFromString(tc.source)),
				niceyaml.WithPrinter(newXMLPrinter()),
			)

			assert.Equal(t, tc.want, trimLines(render(err)))
		})
	}
}

func TestError_NilToken(t *testing.T) {
	t.Parallel()

	// Test getTokenPosition with nil token - should return empty position.
	err := niceyaml.NewError(
		"nil token error",
		niceyaml.WithErrorToken(nil),
	)

	got := render(err)
	// Should still work and show the error message.
	assert.Equal(t, "nil token error", got)
}

func TestError_Unwrap(t *testing.T) {
	t.Parallel()

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

	newXMLPrinter := func() *niceyaml.Printer {
		return niceyaml.NewPrinter(
			niceyaml.WithStyles(yamltest.NewXMLStyles()),
			niceyaml.WithGutter(niceyaml.NoGutter),
			niceyaml.WithContainerStyle(lipgloss.NewStyle()),
		)
	}

	t.Run("single nested error with path", func(t *testing.T) {
		t.Parallel()

		source := stringtest.Input(`
			name: test
			value: 123
			other: data
		`)

		err := niceyaml.NewError(
			"validation failed",
			niceyaml.WithPath(paths.Root().Child("name").Key()),
			niceyaml.WithSource(niceyaml.NewSourceFromString(source)),
			niceyaml.WithPrinter(newXMLPrinter()),
			niceyaml.WithErrors(
				niceyaml.NewError(
					"invalid type",
					niceyaml.WithPath(paths.Root().Child("value").Value()),
				),
			),
		)

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

		err := niceyaml.NewError(
			"validation failed",
			niceyaml.WithPath(paths.Root().Child("name").Key()),
			niceyaml.WithSource(niceyaml.NewSourceFromString(source)),
			niceyaml.WithPrinter(newXMLPrinter()),
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
		)

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
		err := niceyaml.NewError(
			"validation failed",
			niceyaml.WithErrorToken(tokens[0]),
			niceyaml.WithPrinter(newXMLPrinter()),
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
		)

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

		err := niceyaml.NewError(
			"main error",
			niceyaml.WithErrorToken(tokens[0]),
			niceyaml.WithPrinter(newXMLPrinter()),
			niceyaml.WithErrors(
				niceyaml.NewError(
					"nested with token",
					niceyaml.WithErrorToken(fooToken),
				),
			),
		)

		got := trimLines(render(err))

		// Should highlight both tokens.
		assert.Contains(t, got, "<genericError>key</genericError>")
		assert.Contains(t, got, "<genericError>foo</genericError>")
		assert.Contains(t, got, "^ nested with token")
	})

	t.Run("failed nested resolution is skipped silently", func(t *testing.T) {
		t.Parallel()

		source := stringtest.Input(`
			key: value
		`)

		err := niceyaml.NewError(
			"main error",
			niceyaml.WithPath(paths.Root().Child("key").Key()),
			niceyaml.WithSource(niceyaml.NewSourceFromString(source)),
			niceyaml.WithPrinter(newXMLPrinter()),
			niceyaml.WithErrors(
				niceyaml.NewError(
					"nested error",
					niceyaml.WithPath(paths.Root().Child("nonexistent").Key()),
				),
			),
		)

		got := trimLines(render(err))

		// Should still render the main error, nested error is skipped.
		assert.Contains(t, got, "[1:1] main error")
		assert.Contains(t, got, "<genericError>key</genericError>")
		// Should NOT contain annotation for failed nested error.
		assert.NotContains(t, got, "nested error")
	})

	t.Run("plain error with nested errors renders bullets", func(t *testing.T) {
		t.Parallel()

		err := niceyaml.NewError(
			"main error",
			niceyaml.WithErrors(
				niceyaml.NewError("nested 1"),
				niceyaml.NewError("nested 2"),
			),
		)

		got := render(err)

		want := stringtest.JoinLF(
			"main error",
			"  • nested 1",
			"  • nested 2",
		)
		assert.Equal(t, want, got)
	})

	t.Run("nested error without path or token is skipped", func(t *testing.T) {
		t.Parallel()

		source := stringtest.Input(`
			key: value
		`)

		err := niceyaml.NewError(
			"main error",
			niceyaml.WithPath(paths.Root().Child("key").Key()),
			niceyaml.WithSource(niceyaml.NewSourceFromString(source)),
			niceyaml.WithPrinter(newXMLPrinter()),
			niceyaml.WithErrors(
				niceyaml.NewError("no location"),
			),
		)

		got := trimLines(render(err))

		// Should still render main error.
		assert.Contains(t, got, "[1:1] main error")
		// Should NOT contain annotation for nested error without location.
		assert.NotContains(t, got, "no location")
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
		err := niceyaml.NewError(
			"validation failed at 1 location",
			niceyaml.WithSource(niceyaml.NewSourceFromString(source)),
			niceyaml.WithPrinter(newXMLPrinter()),
			niceyaml.WithErrors(
				niceyaml.NewError(
					"got number, want string",
					niceyaml.WithPath(paths.Root().Child("value").Value()),
				),
			),
		)

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

		got := render(err)

		// Should fall back to plain bullet format since no source is available.
		want := stringtest.JoinLF(
			"validation failed",
			"  • at $.value: nested error",
		)
		assert.Equal(t, want, got)
	})

	t.Run("nested-only error with multiple lines", func(t *testing.T) {
		t.Parallel()

		source := stringtest.Input(`
			name: test
			value: 123
			other: data
		`)

		// Create error with nested errors on different lines.
		err := niceyaml.NewError(
			"validation failed at 2 locations",
			niceyaml.WithSource(niceyaml.NewSourceFromString(source)),
			niceyaml.WithPrinter(newXMLPrinter()),
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
		)

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
		err := niceyaml.NewError(
			"validation failed",
			niceyaml.WithSource(niceyaml.NewSourceFromString(source)),
			niceyaml.WithPrinter(newXMLPrinter()),
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
		)

		got := trimLines(render(err))

		// Should contain the resolvable error annotation.
		assert.Contains(t, got, "resolvable error")
		// Should NOT contain the unresolvable error (silently skipped).
		assert.NotContains(t, got, "unresolvable error")
	})

	t.Run("nested-only error with nested error that has no path or token", func(t *testing.T) {
		t.Parallel()

		source := stringtest.Input(`
			name: test
			value: 123
		`)

		// All nested errors lack path/token, so hasNestedPaths returns false
		// and should fall back to plain bullet rendering.
		err := niceyaml.NewError(
			"validation failed",
			niceyaml.WithSource(niceyaml.NewSourceFromString(source)),
			niceyaml.WithErrors(
				niceyaml.NewError("nested without location"),
			),
		)

		got := render(err)

		// Should fall back to plain bullet format.
		want := stringtest.JoinLF(
			"validation failed",
			"  • nested without location",
		)
		assert.Equal(t, want, got)
	})
}

func TestError_hasNestedPaths(t *testing.T) {
	t.Parallel()

	// These tests verify hasNestedPaths behavior indirectly through Error() output.
	// When hasNestedPaths returns true and source is provided, we get annotated output.
	// When hasNestedPaths returns false, we get plain bullet output regardless of source.

	newXMLPrinter := func() *niceyaml.Printer {
		return niceyaml.NewPrinter(
			niceyaml.WithStyles(yamltest.NewXMLStyles()),
			niceyaml.WithGutter(niceyaml.NoGutter),
			niceyaml.WithContainerStyle(lipgloss.NewStyle()),
		)
	}

	source := stringtest.Input(`
		key: value
		other: data
	`)

	tcs := map[string]struct {
		err        *niceyaml.Error
		wantYAML   bool // If true, expect YAML output; if false, expect bullet format.
		wantBullet string
	}{
		"returns false when no nested errors": {
			err: niceyaml.NewError(
				"main error",
				niceyaml.WithSource(niceyaml.NewSourceFromString(source)),
			),
			wantYAML:   false,
			wantBullet: "main error",
		},
		"returns false when nested errors have no paths or tokens": {
			err: niceyaml.NewError(
				"main error",
				niceyaml.WithSource(niceyaml.NewSourceFromString(source)),
				niceyaml.WithErrors(
					niceyaml.NewError("nested 1"),
					niceyaml.NewError("nested 2"),
				),
			),
			wantYAML: false,
			wantBullet: stringtest.JoinLF(
				"main error",
				"  • nested 1",
				"  • nested 2",
			),
		},
		"returns true when nested error has path": {
			err: niceyaml.NewError(
				"main error",
				niceyaml.WithSource(niceyaml.NewSourceFromString(source)),
				niceyaml.WithPrinter(newXMLPrinter()),
				niceyaml.WithErrors(
					niceyaml.NewError(
						"nested with path",
						niceyaml.WithPath(paths.Root().Child("key").Key()),
					),
				),
			),
			wantYAML: true,
		},
		"returns true when nested error has token": {
			err: func() *niceyaml.Error {
				tokens := lexer.Tokenize(source)

				return niceyaml.NewError(
					"main error",
					niceyaml.WithSource(niceyaml.NewSourceFromString(source)),
					niceyaml.WithPrinter(newXMLPrinter()),
					niceyaml.WithErrors(
						niceyaml.NewError(
							"nested with token",
							niceyaml.WithErrorToken(tokens[0]),
						),
					),
				)
			}(),
			wantYAML: true,
		},
		"returns true with mix of path and no-path nested errors": {
			err: niceyaml.NewError(
				"main error",
				niceyaml.WithSource(niceyaml.NewSourceFromString(source)),
				niceyaml.WithPrinter(newXMLPrinter()),
				niceyaml.WithErrors(
					niceyaml.NewError("no path"),
					niceyaml.NewError(
						"has path",
						niceyaml.WithPath(paths.Root().Child("key").Key()),
					),
					niceyaml.NewError("also no path"),
				),
			),
			wantYAML: true,
		},
		"returns false with nil nested errors only": {
			err: niceyaml.NewError(
				"main error",
				niceyaml.WithSource(niceyaml.NewSourceFromString(source)),
				niceyaml.WithErrors(nil, nil),
			),
			wantYAML:   false,
			wantBullet: "main error",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := render(tc.err)

			if tc.wantYAML {
				// Should contain YAML-style output (not just bullet points).
				assert.Contains(t, got, "main error")
				assert.Contains(t, got, "key")
			} else {
				// Should be plain bullet format.
				assert.Equal(t, tc.wantBullet, got)
			}
		})
	}
}

func TestError_calculateNestedLineRange(t *testing.T) {
	t.Parallel()

	// These tests verify calculateNestedLineRange indirectly through rendered output.
	// The line range determines which lines are visible in the output.

	newXMLPrinter := func() *niceyaml.Printer {
		return niceyaml.NewPrinter(
			niceyaml.WithStyles(yamltest.NewXMLStyles()),
			niceyaml.WithGutter(niceyaml.NoGutter),
			niceyaml.WithContainerStyle(lipgloss.NewStyle()),
		)
	}

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

		err := niceyaml.NewError(
			"validation error",
			niceyaml.WithSource(niceyaml.NewSourceFromString(source)),
			niceyaml.WithPrinter(newXMLPrinter()),
			niceyaml.WithContextLines(1),
			niceyaml.WithErrors(
				niceyaml.NewError(
					"error at line3",
					niceyaml.WithPath(paths.Root().Child("line3").Key()),
				),
			),
		)

		got := trimLines(render(err))

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

		err := niceyaml.NewError(
			"validation error",
			niceyaml.WithSource(niceyaml.NewSourceFromString(source)),
			niceyaml.WithPrinter(newXMLPrinter()),
			niceyaml.WithContextLines(0), // No extra context.
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
		)

		got := trimLines(render(err))

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

		err := niceyaml.NewError(
			"validation error",
			niceyaml.WithSource(niceyaml.NewSourceFromString(source)),
			niceyaml.WithPrinter(newXMLPrinter()),
			niceyaml.WithContextLines(0),
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
		)

		got := trimLines(render(err))

		// Should show line2 with combined errors.
		assert.Contains(t, got, "line2")
		assert.Contains(t, got, "error 1; error 2")
	})
}

func TestError_HunkDisplay(t *testing.T) {
	t.Parallel()

	newXMLPrinter := func() *niceyaml.Printer {
		return niceyaml.NewPrinter(
			niceyaml.WithStyles(yamltest.NewXMLStyles()),
			niceyaml.WithGutter(niceyaml.NoGutter),
			niceyaml.WithContainerStyle(lipgloss.NewStyle()),
		)
	}

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
		err := niceyaml.NewError(
			"validation error",
			niceyaml.WithSource(niceyaml.NewSourceFromString(source)),
			niceyaml.WithPrinter(newXMLPrinter()),
			niceyaml.WithContextLines(1),
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
		)

		got := trimLines(render(err))

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
		err := niceyaml.NewError(
			"validation error",
			niceyaml.WithSource(niceyaml.NewSourceFromString(source)),
			niceyaml.WithPrinter(newXMLPrinter()),
			niceyaml.WithContextLines(1),
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
		)

		got := trimLines(render(err))

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
		err := niceyaml.NewError(
			"validation failed at 2 locations",
			niceyaml.WithSource(niceyaml.NewSourceFromString(source)),
			niceyaml.WithPrinter(newXMLPrinter()),
			niceyaml.WithContextLines(1),
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
		)

		got := trimLines(render(err))

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
		err := niceyaml.NewError(
			"validation error",
			niceyaml.WithSource(niceyaml.NewSourceFromString(source)),
			niceyaml.WithPrinter(newXMLPrinter()),
			niceyaml.WithContextLines(1),
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
		)

		got := trimLines(render(err))

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
		err := niceyaml.NewError(
			"main error",
			niceyaml.WithPath(paths.Root().Child("line1").Key()),
			niceyaml.WithSource(niceyaml.NewSourceFromString(source)),
			niceyaml.WithPrinter(newXMLPrinter()),
			niceyaml.WithContextLines(1),
			niceyaml.WithErrors(
				niceyaml.NewError(
					"nested error",
					niceyaml.WithPath(paths.Root().Child("line10").Key()),
				),
			),
		)

		got := trimLines(render(err))

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
		err := niceyaml.NewError(
			"validation error",
			niceyaml.WithSource(niceyaml.NewSourceFromString(source)),
			niceyaml.WithPrinter(newXMLPrinter()),
			niceyaml.WithContextLines(0),
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
		)

		got := trimLines(render(err))

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

			err := niceyaml.NewError("test error",
				niceyaml.WithErrorToken(tokens[0]),
				niceyaml.WithPrinter(niceyaml.NewPrinter(
					niceyaml.WithGutter(niceyaml.NoGutter),
					niceyaml.WithContainerStyle(lipgloss.NewStyle()),
					niceyaml.WithWidth(tc.width),
				)),
			)

			output := render(err)
			lines := strings.Split(output, "\n")

			// Skip the header line "[1:1] test error:" and the empty line.
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
	customPrinter := niceyaml.NewPrinter(
		niceyaml.WithStyles(yamltest.NewXMLStyles()),
		niceyaml.WithGutter(niceyaml.NoGutter),
		niceyaml.WithContainerStyle(lipgloss.NewStyle()),
	)

	err := niceyaml.NewError(
		"test error",
		niceyaml.WithErrorToken(tokens[0]),
		niceyaml.WithPrinter(customPrinter.With(niceyaml.WithWidth(30))),
	)

	output := render(err)
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
	err := niceyaml.NewError(
		"test error",
		niceyaml.WithErrorToken(tokens[0]),
		niceyaml.WithPrinter(niceyaml.NewPrinter(niceyaml.WithWidth(30))),
	)

	output := render(err)
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
				"[1:1] validation failed",
				"",
				"key: value",
				"     ^ this is a very long error message",
				"       that should definitely wrap when the",
				"       width is limited",
				"other: data",
			),
		},
		"annotation does not wrap when width is 0": {
			width:        0,
			nestedErrMsg: "this is a very long error message that should not wrap",
			want: stringtest.JoinLF(
				"[1:1] validation failed",
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
				"[1:1] validation failed",
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

			err := niceyaml.NewError(
				"validation failed",
				niceyaml.WithPath(paths.Root().Child("key").Key()),
				niceyaml.WithSource(niceyaml.NewSourceFromString(source)),
				niceyaml.WithPrinter(niceyaml.NewPrinter(
					niceyaml.WithStyles(&style.Styles{}),
					niceyaml.WithGutter(niceyaml.NoGutter),
					niceyaml.WithContainerStyle(lipgloss.NewStyle()),
					niceyaml.WithWidth(tc.width),
				)),
				niceyaml.WithErrors(
					niceyaml.NewError(
						tc.nestedErrMsg,
						niceyaml.WithPath(paths.Root().Child("key").Value()),
					),
				),
			)

			got := trimLines(render(err))

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
	err := niceyaml.NewError(
		"validation failed at 2 locations",
		niceyaml.WithSource(niceyaml.NewSourceFromString(source)),
		niceyaml.WithPrinter(niceyaml.NewPrinter(
			niceyaml.WithStyles(&style.Styles{}),
			niceyaml.WithGutter(niceyaml.NoGutter),
			niceyaml.WithContainerStyle(lipgloss.NewStyle()),
			niceyaml.WithWidth(50),
		)),
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
	)
	got := trimLines(render(err))

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
	err := niceyaml.NewError(
		"validation failed",
		niceyaml.WithPath(paths.Root().Child("key").Key()),
		niceyaml.WithSource(niceyaml.NewSourceFromString(source)),
		niceyaml.WithPrinter(niceyaml.NewPrinter(
			niceyaml.WithStyles(&style.Styles{}),
			niceyaml.WithGutter(niceyaml.NoGutter),
			niceyaml.WithContainerStyle(lipgloss.NewStyle()),
			niceyaml.WithWidth(40),
		)),
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
	)
	got := trimLines(render(err))

	want := stringtest.JoinLF(
		"[1:1] validation failed",
		"",
		"key: value",
		"^ first error message here; second error",
		"  message here",
	)
	assert.Equal(t, want, got)
}

func TestError_TokenRendersFromSource(t *testing.T) {
	t.Parallel()

	newXMLPrinter := func() *niceyaml.Printer {
		return niceyaml.NewPrinter(
			niceyaml.WithStyles(yamltest.NewXMLStyles()),
			niceyaml.WithGutter(niceyaml.NoGutter),
			niceyaml.WithContainerStyle(lipgloss.NewStyle()),
		)
	}

	source := niceyaml.NewSourceFromString(stringtest.Input(`
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

	node, err := paths.Root().Child("b").Path().Node(file.Docs[0])
	require.NoError(t, err)

	literal, ok := node.(*ast.LiteralNode)
	require.True(t, ok, "want *ast.LiteralNode, got %T", node)

	tk := literal.Value.GetToken()

	got := trimLines(render(source.WrapError(niceyaml.NewError(
		"bad block",
		niceyaml.WithErrorToken(tk),
		niceyaml.WithPrinter(newXMLPrinter()),
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

func TestError_DocumentIndex(t *testing.T) {
	t.Parallel()

	newXMLPrinter := func() *niceyaml.Printer {
		return niceyaml.NewPrinter(
			niceyaml.WithStyles(yamltest.NewXMLStyles()),
			niceyaml.WithGutter(niceyaml.NoGutter),
			niceyaml.WithContainerStyle(lipgloss.NewStyle()),
		)
	}

	source := niceyaml.NewSourceFromString("name: first\n---\nname: second\n")
	namePath := paths.Root().Child("name").Value()

	t.Run("path resolves in the selected document", func(t *testing.T) {
		t.Parallel()

		err := niceyaml.NewError(
			"bad name",
			niceyaml.WithPath(namePath),
			niceyaml.WithDocumentIndex(1),
			niceyaml.WithSource(source),
			niceyaml.WithPrinter(newXMLPrinter()),
		)

		got := trimLines(render(err))

		assert.True(t, strings.HasPrefix(got, "[3:7] bad name"), got)
		assert.Contains(t, got, "<genericError>second</genericError>")
		assert.NotContains(t, got, "<genericError>first</genericError>")
	})

	t.Run("path resolves in the first document by default", func(t *testing.T) {
		t.Parallel()

		err := niceyaml.NewError(
			"bad name",
			niceyaml.WithPath(namePath),
			niceyaml.WithSource(source),
			niceyaml.WithPrinter(newXMLPrinter()),
		)

		got := trimLines(render(err))

		assert.True(t, strings.HasPrefix(got, "[1:7] bad name"), got)
		assert.Contains(t, got, "<genericError>first</genericError>")

		_, set := err.DocumentIndex()
		assert.False(t, set)
	})

	t.Run("nested errors inherit the document index", func(t *testing.T) {
		t.Parallel()

		err := niceyaml.NewError(
			"validation failed",
			niceyaml.WithDocumentIndex(1),
			niceyaml.WithSource(source),
			niceyaml.WithPrinter(newXMLPrinter()),
			niceyaml.WithErrors(
				niceyaml.NewError("bad name", niceyaml.WithPath(namePath)),
			),
		)

		got := trimLines(render(err))

		assert.Contains(t, got, "<genericError>second</genericError>")
		assert.Contains(t, got, "^ bad name")
		assert.NotContains(t, got, "<genericError>first</genericError>")
	})

	t.Run("out of range index degrades to a plain message", func(t *testing.T) {
		t.Parallel()

		err := niceyaml.NewError(
			"bad name",
			niceyaml.WithPath(namePath),
			niceyaml.WithDocumentIndex(5),
			niceyaml.WithSource(source),
			niceyaml.WithPrinter(newXMLPrinter()),
		)

		assert.Equal(t, "at $.name: bad name", render(err))
	})
}

func TestError_DoesNotMutateSource(t *testing.T) {
	t.Parallel()

	source := niceyaml.NewSourceFromString(stringtest.Input(`
		a: 1
		b: 2
		c: 3
	`))

	err := source.WrapError(niceyaml.NewError(
		"main",
		niceyaml.WithPrinter(niceyaml.NewPrinter(
			niceyaml.WithStyles(yamltest.NewXMLStyles()),
			niceyaml.WithGutter(niceyaml.NoGutter),
			niceyaml.WithContainerStyle(lipgloss.NewStyle()),
		)),
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

	// The caller's Source is untouched.
	for _, ln := range source.Lines() {
		assert.Empty(t, ln.Overlays)
		assert.Empty(t, ln.Annotations)
	}
}

func TestError_With(t *testing.T) {
	t.Parallel()

	source := niceyaml.NewSourceFromString("key: value\n")
	base := niceyaml.NewError("bad key", niceyaml.WithPath(paths.Root().Child("key").Key()))

	located := base.With(niceyaml.WithSource(source))

	// The receiver is unchanged and the copy resolves against the source.
	assert.Equal(t, "at $.key: bad key", base.Error())
	assert.Equal(t, "[1:1] bad key", located.Error())
	assert.Empty(t, base.Detail())
	assert.NotEmpty(t, located.Detail())
}

func TestError_WrappedContext(t *testing.T) {
	t.Parallel()

	source := niceyaml.NewSourceFromString("name: first\n---\nname: second\n")
	inner := niceyaml.NewError(
		"bad name",
		niceyaml.WithPath(paths.Root().Child("name").Value()),
		niceyaml.WithDocumentIndex(1),
	)

	wrapped := source.WrapError(fmt.Errorf("document 1: %w", inner))

	// The message keeps the outer context, and the location comes from the
	// inner Error.
	assert.Equal(t, "document 1: at $.name: bad name", wrapped.Error())
	require.ErrorIs(t, wrapped, inner)

	var got *niceyaml.Error

	require.ErrorAs(t, wrapped, &got)
	assert.Equal(t, "$.name", got.Path())

	idx, set := got.DocumentIndex()
	assert.True(t, set)
	assert.Equal(t, 1, idx)

	detail := got.Detail()
	assert.Contains(t, detail, "second")
	assert.NotContains(t, detail, "^")

	// Wrapping a direct Error resolves its position in the message.
	direct := source.WrapError(inner)
	assert.Equal(t, "[3:7] bad name", direct.Error())

	// Wrapping twice renders the same output.
	twice := source.WrapError(direct)
	assert.Equal(t, direct.Error(), twice.Error())
	assert.Equal(t, fmt.Sprintf("%+v", direct), fmt.Sprintf("%+v", twice))
}

func TestError_DocumentIndexAboveLocation(t *testing.T) {
	t.Parallel()

	source := niceyaml.NewSourceFromString("name: first\n---\nname: second\n")

	// A producer that wraps its own Error with context, the way a Validator
	// does, leaves the document index to the caller above that wrapping.
	located := niceyaml.NewError(
		"bad name",
		niceyaml.WithPath(paths.Root().Child("name").Value()),
		niceyaml.WithPrinter(niceyaml.NewPrinter(
			niceyaml.WithStyles(yamltest.NewXMLStyles()),
			niceyaml.WithGutter(niceyaml.NoGutter),
			niceyaml.WithContainerStyle(lipgloss.NewStyle()),
		)),
	)
	indexed := niceyaml.NewErrorFrom(
		fmt.Errorf("validate: %w", located),
		niceyaml.WithDocumentIndex(1),
	)

	wrapped := source.WrapError(indexed)

	var got *niceyaml.Error

	require.ErrorAs(t, wrapped, &got)

	// The index survives the extra layer, so the path resolves in document 1.
	idx, set := got.DocumentIndex()
	assert.True(t, set)
	assert.Equal(t, 1, idx)

	// The highlight lands on the second document's value, not the first's.
	detail := got.Detail()
	assert.Contains(t, detail, "<genericError>second</genericError>")
	assert.NotContains(t, detail, "<genericError>first</genericError>")
}

func TestError_FormatDropsNestedBullets(t *testing.T) {
	t.Parallel()

	source := niceyaml.NewSourceFromString("a: 1\nb: 2\n")
	inner := niceyaml.NewError(
		"validation failed at 2 locations",
		niceyaml.WithErrors(
			niceyaml.NewError("bad a", niceyaml.WithPath(paths.Root().Child("a").Value())),
			niceyaml.NewError("bad b", niceyaml.WithPath(paths.Root().Child("b").Value())),
		),
		niceyaml.WithPrinter(niceyaml.NewPrinter(
			niceyaml.WithStyles(style.Styles{}),
			niceyaml.WithGutter(niceyaml.NoGutter),
			niceyaml.WithContainerStyle(lipgloss.NewStyle()),
		)),
	)

	wrapped := source.WrapError(fmt.Errorf("document 0: %w", inner))

	// Error keeps the bullets, since nothing else carries the nested messages.
	assert.Contains(t, wrapped.Error(), "\n  • at $.a: bad a")

	// The %+v form renders them as annotations instead, so the bullets would
	// only repeat what the detail already shows.
	got := trimLines(fmt.Sprintf("%+v", wrapped))

	assert.Equal(t, "document 0: validation failed at 2 locations", strings.SplitN(got, "\n", 2)[0])
	assert.NotContains(t, got, "•")
	assert.Contains(t, got, "^ bad a")
	assert.Contains(t, got, "^ bad b")
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

	// The bullet carries the whole nested message, continuation line included.
	assert.Contains(t, err.Error(), "\n  • [1:4] bad a\n  see docs for details")

	// The %+v headline drops the bullet whole, rather than leaving the lines
	// below its marker behind.
	got := trimLines(render(err))
	headline, _, _ := strings.Cut(got, "\n\n")

	assert.Equal(t, "validation failed", headline)
	assert.NotContains(t, headline, "see docs for details")
}

func TestError_MessageKeepsItsOwnBulletMarker(t *testing.T) {
	t.Parallel()

	source := niceyaml.NewSourceFromString("a: 1\nb: 2\n")
	inner := niceyaml.NewError(
		"validation failed\n  • see the schema docs",
		niceyaml.WithErrors(
			niceyaml.NewError("bad a", niceyaml.WithPath(paths.Root().Child("a").Value())),
		),
	)

	err := source.WrapError(fmt.Errorf("document 0: %w", inner))

	// The %+v headline drops only the bullets Error appended, so a marker the
	// message carries on its own survives.
	got := trimLines(render(err))
	headline, _, _ := strings.Cut(got, "\n\n")

	assert.Equal(t, "document 0: validation failed\n  • see the schema docs", headline)
	assert.Contains(t, got, "^ bad a")
	assert.NotContains(t, got, "at $.a: bad a")
}

func TestError_ResolvesThroughErrorWrappers(t *testing.T) {
	t.Parallel()

	source := niceyaml.NewSourceFromString("name: first\n---\nname: second\n")
	located := niceyaml.NewError("bad name", niceyaml.WithPath(paths.Root().Child("name").Value()))

	// Error wrappers add no message text of their own, so the source attached
	// above them still resolves the location in the message.
	wrapped := source.WrapError(niceyaml.NewErrorFrom(located))

	assert.Equal(t, "[1:7] bad name", wrapped.Error())

	var got *niceyaml.Error

	require.ErrorAs(t, wrapped, &got)
	assert.NotEmpty(t, got.Detail())
}
