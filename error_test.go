package niceyaml_test

import (
	"errors"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/goccy/go-yaml/lexer"
	"github.com/goccy/go-yaml/token"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/macropower/niceyaml"
	"github.com/macropower/niceyaml/paths"
	"github.com/macropower/niceyaml/yamltest"
)

// customTestError is a test error type for errors.As testing.
type customTestError struct {
	msg string
}

func (e *customTestError) Error() string {
	return e.msg
}

// trimLines trims trailing whitespace from each line of a string.
// This is useful for comparing styled output where lipgloss adds padding.
func trimLines(s string) string {
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimRight(line, " \t")
	}

	return yamltest.JoinLF(lines...)
}

func TestError(t *testing.T) {
	t.Parallel()

	source := yamltest.Input(`
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
					niceyaml.WithStyle(lipgloss.NewStyle()),
				)),
			),
			want: yamltest.JoinLF(
				"[3:1] invalid value:",
				"",
				"<name-tag>a</name-tag><punctuation-mapping-value>:</punctuation-mapping-value><text> </text><literal-string>b</literal-string>",
				"<name-tag>foo</name-tag><punctuation-mapping-value>:</punctuation-mapping-value><text> </text><literal-string>bar</literal-string>",
				"<generic-error>key</generic-error><punctuation-mapping-value>:</punctuation-mapping-value><text> </text><literal-string>value</literal-string>",
			),
		},
		"with direct token bypasses path resolution": {
			err: niceyaml.NewError(
				"bad token",
				niceyaml.WithErrorToken(tokens[0]),
				niceyaml.WithPrinter(niceyaml.NewPrinter(
					niceyaml.WithStyles(yamltest.NewXMLStyles()),
					niceyaml.WithGutter(niceyaml.NoGutter),
					niceyaml.WithStyle(lipgloss.NewStyle()),
				)),
			),
			want: yamltest.JoinLF(
				"[1:1] bad token:",
				"",
				"<generic-error>a</generic-error><punctuation-mapping-value>:</punctuation-mapping-value><text> </text><literal-string>b</literal-string>",
				"<name-tag>foo</name-tag><punctuation-mapping-value>:</punctuation-mapping-value><text> </text><literal-string>bar</literal-string>",
				"<name-tag>key</name-tag><punctuation-mapping-value>:</punctuation-mapping-value><text> </text><literal-string>value</literal-string>",
			),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := tc.err.Error()

			assert.Equal(t, tc.want, trimLines(got))
		})
	}
}

func TestSourceWrapError(t *testing.T) {
	t.Parallel()

	sourceInput := yamltest.Input(`
		name: test
		value: 123
	`)

	newXMLPrinter := func() *niceyaml.Printer {
		return niceyaml.NewPrinter(
			niceyaml.WithStyles(yamltest.NewXMLStyles()),
			niceyaml.WithGutter(niceyaml.NoGutter),
			niceyaml.WithStyle(lipgloss.NewStyle()),
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
			wantExact: yamltest.JoinLF(
				"[1:1] test error:",
				"",
				"<generic-error>name</generic-error><punctuation-mapping-value>:</punctuation-mapping-value><text> </text><literal-string>test</literal-string>",
				"<name-tag>value</name-tag><punctuation-mapping-value>:</punctuation-mapping-value><text> </text><literal-number-integer>123</literal-number-integer>",
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
				assert.Equal(t, tc.wantExact, trimLines(got.Error()))
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
		"nested path": {
			err: niceyaml.NewError(
				"test",
				niceyaml.WithPath(paths.Root().Child("foo").Child("bar").Key()),
			),
			want: "$.foo.bar",
		},
		"returns first nested path when main path nil": {
			err: niceyaml.NewError(
				"test",
				niceyaml.WithErrors(
					niceyaml.NewError(
						"nested error",
						niceyaml.WithPath(paths.Root().Child("nested").Value()),
					),
				),
			),
			want: "$.nested",
		},
		"prefers key-targeting nested errors": {
			err: niceyaml.NewError(
				"test",
				niceyaml.WithErrors(
					niceyaml.NewError(
						"type error",
						niceyaml.WithPath(paths.Root().Child("value").Value()),
					),
					niceyaml.NewError(
						"additional property",
						niceyaml.WithPath(paths.Root().Child("extra").Key()),
					),
				),
			),
			want: "$.extra",
		},
		"prefers longer path when same target": {
			err: niceyaml.NewError(
				"test",
				niceyaml.WithErrors(
					niceyaml.NewError(
						"short path error",
						niceyaml.WithPath(paths.Root().Child("a").Key()),
					),
					niceyaml.NewError(
						"long path error",
						niceyaml.WithPath(
							paths.Root().Child("a").Child("b").Child("c").Key(),
						),
					),
				),
			),
			want: "$.a.b.c",
		},
		"returns empty when nested errors have no paths": {
			err: niceyaml.NewError(
				"test",
				niceyaml.WithErrors(
					niceyaml.NewError("nested 1"),
					niceyaml.NewError("nested 2"),
				),
			),
			want: "",
		},
		"skips nil nested errors when finding path": {
			err: niceyaml.NewError(
				"test",
				niceyaml.WithErrors(
					nil,
					niceyaml.NewError(
						"nested error",
						niceyaml.WithPath(paths.Root().Child("found").Key()),
					),
				),
			),
			want: "$.found",
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

			got := tc.err.Error()
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestErrorAnnotation(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		path        *paths.Path
		source      string
		errMsg      string
		want        string
		sourceLines int
	}{
		"nested path shows correct key": {
			source: yamltest.Input(`
				foo:
				  bar: value
			`),
			path:   paths.Root().Child("foo", "bar").Key(),
			errMsg: "nested error",
			want: yamltest.JoinLF(
				"[2:3] nested error:",
				"",
				"<name-tag>foo</name-tag><punctuation-mapping-value>:</punctuation-mapping-value>",
				"<text>  </text><generic-error>bar</generic-error><punctuation-mapping-value>:</punctuation-mapping-value><text> </text><literal-string>value</literal-string>",
			),
		},
		"array element path - first item": {
			source: yamltest.Input(`
				items:
				  - first
				  - second
			`),
			path:   paths.Root().Child("items").Index(0).Key(),
			errMsg: "array error",
			want: yamltest.JoinLF(
				"[2:5] array error:",
				"",
				"<name-tag>items</name-tag><punctuation-mapping-value>:</punctuation-mapping-value>",
				"<text>  </text><punctuation-sequence-entry>-</punctuation-sequence-entry><text> </text><generic-error>first</generic-error>",
				"<text>  </text><punctuation-sequence-entry>-</punctuation-sequence-entry><text> </text><literal-string>second</literal-string>",
			),
		},
		"array element path - nested object in array": {
			source: yamltest.Input(`
				users:
				  - name: alice
				    age: 30
			`),
			path:   paths.Root().Child("users").Index(0).Child("name").Key(),
			errMsg: "nested array error",
			want: yamltest.JoinLF(
				"[2:5] nested array error:",
				"",
				"<name-tag>users</name-tag><punctuation-mapping-value>:</punctuation-mapping-value>",
				"<text>  </text><punctuation-sequence-entry>-</punctuation-sequence-entry><text> </text><generic-error>name</generic-error><punctuation-mapping-value>:</punctuation-mapping-value><text> </text><literal-string>alice</literal-string>",
				"<text>    </text><name-tag>age</name-tag><punctuation-mapping-value>:</punctuation-mapping-value><text> </text><literal-number-integer>30</literal-number-integer>",
			),
		},
		"root path": {
			source: "key: value",
			path:   paths.Root().Key(),
			errMsg: "root error",
			want: yamltest.JoinLF(
				"[1:4] root error:",
				"",
				"<name-tag>key</name-tag><generic-error>:</generic-error><text> </text><literal-string>value</literal-string>",
			),
		},
		"single top-level key path": {
			source: "key: value",
			path:   paths.Root().Child("key").Key(),
			errMsg: "top level error",
			want: yamltest.JoinLF(
				"[1:1] top level error:",
				"",
				"<generic-error>key</generic-error><punctuation-mapping-value>:</punctuation-mapping-value><text> </text><literal-string>value</literal-string>",
			),
		},
		"with custom source lines": {
			source: yamltest.Input(`
				line1: a
				line2: b
				line3: c
				line4: d
				line5: e
			`),
			path:        paths.Root().Child("line3").Key(),
			errMsg:      "middle error",
			sourceLines: 1,
			want: yamltest.JoinLF(
				"[3:1] middle error:",
				"",
				"<name-tag>line2</name-tag><punctuation-mapping-value>:</punctuation-mapping-value><text> </text><literal-string>b</literal-string>",
				"<generic-error>line3</generic-error><punctuation-mapping-value>:</punctuation-mapping-value><text> </text><literal-string>c</literal-string>",
				"<name-tag>line4</name-tag><punctuation-mapping-value>:</punctuation-mapping-value><text> </text><literal-string>d</literal-string>",
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
					niceyaml.WithStyle(lipgloss.NewStyle()),
				)),
			}
			if tc.sourceLines > 0 {
				opts = append(opts, niceyaml.WithSourceLines(tc.sourceLines))
			}

			err := niceyaml.NewError(tc.errMsg, opts...)

			assert.Equal(t, tc.want, trimLines(err.Error()))
		})
	}
}

func TestErrorAnnotation_PathTargetValue(t *testing.T) {
	t.Parallel()

	newXMLPrinter := func() *niceyaml.Printer {
		return niceyaml.NewPrinter(
			niceyaml.WithStyles(yamltest.NewXMLStyles()),
			niceyaml.WithGutter(niceyaml.NoGutter),
			niceyaml.WithStyle(lipgloss.NewStyle()),
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
			want: yamltest.JoinLF(
				"[1:6] invalid value:",
				"",
				"<name-tag>key</name-tag><punctuation-mapping-value>:</punctuation-mapping-value><text> </text><generic-error>value</generic-error>",
			),
		},
		"nested path with value target": {
			source: yamltest.Input(`
				foo:
				  bar: nested_value
			`),
			path:   paths.Root().Child("foo", "bar").Value(),
			errMsg: "nested value error",
			want: yamltest.JoinLF(
				"[2:8] nested value error:",
				"",
				"<name-tag>foo</name-tag><punctuation-mapping-value>:</punctuation-mapping-value>",
				"<text>  </text><name-tag>bar</name-tag><punctuation-mapping-value>:</punctuation-mapping-value><text> </text><generic-error>nested_value</generic-error>",
			),
		},
		"array element works same as key target": {
			// Array elements don't have keys, so both targets return the value token.
			source: yamltest.Input(`
				items:
				  - first
				  - second
			`),
			path:   paths.Root().Child("items").Index(0).Value(),
			errMsg: "array error",
			want: yamltest.JoinLF(
				"[2:5] array error:",
				"",
				"<name-tag>items</name-tag><punctuation-mapping-value>:</punctuation-mapping-value>",
				"<text>  </text><punctuation-sequence-entry>-</punctuation-sequence-entry><text> </text><generic-error>first</generic-error>",
				"<text>  </text><punctuation-sequence-entry>-</punctuation-sequence-entry><text> </text><literal-string>second</literal-string>",
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

			assert.Equal(t, tc.want, trimLines(err.Error()))
		})
	}
}

func TestWithPrinter(t *testing.T) {
	t.Parallel()

	source := yamltest.Input(`
		key: value
		foo: bar
	`)
	tokens := lexer.Tokenize(source)

	customPrinter := niceyaml.NewPrinter(
		niceyaml.WithStyles(yamltest.NewXMLStyles()),
		niceyaml.WithGutter(niceyaml.NoGutter),
		niceyaml.WithStyle(lipgloss.NewStyle()),
	)

	err := niceyaml.NewError(
		"test error",
		niceyaml.WithErrorToken(tokens[0]),
		niceyaml.WithPrinter(customPrinter),
	)

	want := yamltest.JoinLF(
		"[1:1] test error:",
		"",
		"<generic-error>key</generic-error><punctuation-mapping-value>:</punctuation-mapping-value><text> </text><literal-string>value</literal-string>",
		"<name-tag>foo</name-tag><punctuation-mapping-value>:</punctuation-mapping-value><text> </text><literal-string>bar</literal-string>",
	)
	assert.Equal(t, want, trimLines(err.Error()))
}

func TestError_SpecialParentContext(t *testing.T) {
	t.Parallel()

	newXMLPrinter := func() *niceyaml.Printer {
		return niceyaml.NewPrinter(
			niceyaml.WithStyles(yamltest.NewXMLStyles()),
			niceyaml.WithGutter(niceyaml.NoGutter),
			niceyaml.WithStyle(lipgloss.NewStyle()),
		)
	}

	tcs := map[string]struct {
		source string
		path   *paths.Path
		errMsg string
		want   string
	}{
		"root level array - parent is SequenceNode": {
			// Tests findKeyToken returning nil when parent is not a MappingValueNode.
			source: yamltest.Input(`
				- first
				- second
				- third
			`),
			path:   paths.Root().Index(1).Key(),
			errMsg: "array element error",
			want: yamltest.JoinLF(
				"[2:3] array element error:",
				"",
				"<punctuation-sequence-entry>-</punctuation-sequence-entry><text> </text><literal-string>first</literal-string>",
				"<punctuation-sequence-entry>-</punctuation-sequence-entry><text> </text><generic-error>second</generic-error>",
				"<punctuation-sequence-entry>-</punctuation-sequence-entry><text> </text><literal-string>third</literal-string>",
			),
		},
		"document root - parent is nil": {
			// Tests findKeyToken returning nil when there's no parent.
			source: yamltest.Input(`
				key: value
				another: line
			`),
			path:   paths.Root().Key(),
			errMsg: "document root error",
			want: yamltest.JoinLF(
				"[1:4] document root error:",
				"",
				"<name-tag>key</name-tag><generic-error>:</generic-error><text> </text><literal-string>value</literal-string>",
				"<name-tag>another</name-tag><punctuation-mapping-value>:</punctuation-mapping-value><text> </text><literal-string>line</literal-string>",
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

			assert.Equal(t, tc.want, trimLines(err.Error()))
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

	got := err.Error()
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
			niceyaml.WithStyle(lipgloss.NewStyle()),
		)
	}

	t.Run("single nested error with path", func(t *testing.T) {
		t.Parallel()

		source := yamltest.Input(`
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

		got := trimLines(err.Error())

		// Should highlight main error token and include annotation for nested error.
		assert.Contains(t, got, "<generic-error>name</generic-error>")
		assert.Contains(t, got, "<generic-error>123</generic-error>")
		assert.Contains(t, got, "^ invalid type")
	})

	t.Run("multiple nested errors on different lines", func(t *testing.T) {
		t.Parallel()

		source := yamltest.Input(`
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

		got := trimLines(err.Error())

		// Should include both annotations.
		assert.Contains(t, got, "^ invalid type")
		assert.Contains(t, got, "^ missing field")
	})

	t.Run("multiple nested errors on same line", func(t *testing.T) {
		t.Parallel()

		source := yamltest.Input(`
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

		got := trimLines(err.Error())

		// Errors on same line should be combined with "; ".
		assert.Contains(t, got, "error1; error2")
	})

	t.Run("nested error with direct token", func(t *testing.T) {
		t.Parallel()

		source := yamltest.Input(`
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

		got := trimLines(err.Error())

		// Should highlight both tokens.
		assert.Contains(t, got, "<generic-error>key</generic-error>")
		assert.Contains(t, got, "<generic-error>foo</generic-error>")
		assert.Contains(t, got, "^ nested with token")
	})

	t.Run("failed nested resolution is skipped silently", func(t *testing.T) {
		t.Parallel()

		source := yamltest.Input(`
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

		got := trimLines(err.Error())

		// Should still render the main error, nested error is skipped.
		assert.Contains(t, got, "[1:1] main error:")
		assert.Contains(t, got, "<generic-error>key</generic-error>")
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

		got := err.Error()

		want := yamltest.JoinLF(
			"main error",
			"  • nested 1",
			"  • nested 2",
		)
		assert.Equal(t, want, got)
	})

	t.Run("nested error without path or token is skipped", func(t *testing.T) {
		t.Parallel()

		source := yamltest.Input(`
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

		got := trimLines(err.Error())

		// Should still render main error.
		assert.Contains(t, got, "[1:1] main error:")
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

		source := yamltest.Input(`
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

		got := trimLines(err.Error())

		// Should contain the main message.
		assert.Contains(t, got, "validation failed at 1 location")
		// Should contain nested error annotation.
		assert.Contains(t, got, "got number, want string")
		// Should contain YAML content (not just bullet points).
		assert.Contains(t, got, "value")
		assert.Contains(t, got, "123")
		// Should highlight the nested error value.
		assert.Contains(t, got, "<generic-error>123</generic-error>")
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

		got := err.Error()

		// Should fall back to plain bullet format since no source is available.
		want := yamltest.JoinLF(
			"validation failed",
			"  • nested error",
		)
		assert.Equal(t, want, got)
	})

	t.Run("nested-only error with multiple lines", func(t *testing.T) {
		t.Parallel()

		source := yamltest.Input(`
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

		got := trimLines(err.Error())

		// Should contain both annotations.
		assert.Contains(t, got, "type error on value")
		assert.Contains(t, got, "unexpected property")
		// Should highlight both error locations.
		assert.Contains(t, got, "<generic-error>123</generic-error>")
		assert.Contains(t, got, "<generic-error>other</generic-error>")
	})

	t.Run("nested-only error with resolvable and unresolvable nested errors", func(t *testing.T) {
		t.Parallel()

		source := yamltest.Input(`
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

		got := trimLines(err.Error())

		// Should contain the resolvable error annotation.
		assert.Contains(t, got, "resolvable error")
		// Should NOT contain the unresolvable error (silently skipped).
		assert.NotContains(t, got, "unresolvable error")
	})

	t.Run("nested-only error with nested error that has no path or token", func(t *testing.T) {
		t.Parallel()

		source := yamltest.Input(`
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

		got := err.Error()

		// Should fall back to plain bullet format.
		want := yamltest.JoinLF(
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
			niceyaml.WithStyle(lipgloss.NewStyle()),
		)
	}

	source := yamltest.Input(`
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
			wantBullet: yamltest.JoinLF(
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

			got := tc.err.Error()

			if tc.wantYAML {
				// Should contain YAML-style output (not just bullet points).
				assert.Contains(t, got, "main error:")
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
			niceyaml.WithStyle(lipgloss.NewStyle()),
		)
	}

	t.Run("single nested error shows correct context lines", func(t *testing.T) {
		t.Parallel()

		source := yamltest.Input(`
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
			niceyaml.WithSourceLines(1),
			niceyaml.WithErrors(
				niceyaml.NewError(
					"error at line3",
					niceyaml.WithPath(paths.Root().Child("line3").Key()),
				),
			),
		)

		got := trimLines(err.Error())

		// Should show context around line3.
		assert.Contains(t, got, "line2")
		assert.Contains(t, got, "line3")
		assert.Contains(t, got, "line4")
	})

	t.Run("multiple nested errors on different lines expands range", func(t *testing.T) {
		t.Parallel()

		source := yamltest.Input(`
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
			niceyaml.WithSourceLines(0), // No extra context.
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

		got := trimLines(err.Error())

		// Should show the full range from line1 to line6.
		assert.Contains(t, got, "line1")
		assert.Contains(t, got, "line6")
		assert.Contains(t, got, "error at line1")
		assert.Contains(t, got, "error at line6")
	})

	t.Run("nested errors on same line do not expand range", func(t *testing.T) {
		t.Parallel()

		source := yamltest.Input(`
			line1: a
			line2: b
			line3: c
		`)

		err := niceyaml.NewError(
			"validation error",
			niceyaml.WithSource(niceyaml.NewSourceFromString(source)),
			niceyaml.WithPrinter(newXMLPrinter()),
			niceyaml.WithSourceLines(0),
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

		got := trimLines(err.Error())

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
			niceyaml.WithStyle(lipgloss.NewStyle()),
		)
	}

	t.Run("distant errors show separate hunks with separator", func(t *testing.T) {
		t.Parallel()

		// Create a source with many lines.
		source := yamltest.Input(`
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

		// Errors at line1 and line10 with sourceLines=1 should create separate hunks.
		err := niceyaml.NewError(
			"validation error",
			niceyaml.WithSource(niceyaml.NewSourceFromString(source)),
			niceyaml.WithPrinter(newXMLPrinter()),
			niceyaml.WithSourceLines(1),
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

		got := trimLines(err.Error())

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

		source := yamltest.Input(`
			line1: a
			line2: b
			line3: c
			line4: d
			line5: e
		`)

		// Errors at line1 and line3 with sourceLines=1 should merge into one hunk
		// since they're within 2*sourceLines of each other.
		err := niceyaml.NewError(
			"validation error",
			niceyaml.WithSource(niceyaml.NewSourceFromString(source)),
			niceyaml.WithPrinter(newXMLPrinter()),
			niceyaml.WithSourceLines(1),
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

		got := trimLines(err.Error())

		// Should show both errors.
		assert.Contains(t, got, "first error")
		assert.Contains(t, got, "second error")
		// Should NOT have "..." separator since errors are close.
		assert.NotContains(t, got, "...")
	})

	t.Run("nested-only distant errors show separate hunks", func(t *testing.T) {
		t.Parallel()

		source := yamltest.Input(`
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
			niceyaml.WithSourceLines(1),
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

		got := trimLines(err.Error())

		// Should show both errors.
		assert.Contains(t, got, "first location error")
		assert.Contains(t, got, "second location error")
		// Should have "..." separator.
		assert.Contains(t, got, "...")
	})

	t.Run("errors at start and end of file", func(t *testing.T) {
		t.Parallel()

		source := yamltest.Input(`
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
			niceyaml.WithSourceLines(1),
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

		got := trimLines(err.Error())

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

		source := yamltest.Input(`
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
			niceyaml.WithSourceLines(1),
			niceyaml.WithErrors(
				niceyaml.NewError(
					"nested error",
					niceyaml.WithPath(paths.Root().Child("line10").Key()),
				),
			),
		)

		got := trimLines(err.Error())

		// Should show both error locations.
		assert.Contains(t, got, "<generic-error>line1</generic-error>")
		assert.Contains(t, got, "<generic-error>line10</generic-error>")
		// Should show nested error annotation.
		assert.Contains(t, got, "nested error")
		// Should have "..." separator.
		assert.Contains(t, got, "...")
	})

	t.Run("adjacent errors with no context merge into single hunk", func(t *testing.T) {
		t.Parallel()

		source := yamltest.Input(`
			line1: a
			line2: b
			line3: c
		`)

		// With sourceLines=0, errors at line1 and line2 should merge because
		// they're within threshold (2*0+1=1) of each other.
		err := niceyaml.NewError(
			"validation error",
			niceyaml.WithSource(niceyaml.NewSourceFromString(source)),
			niceyaml.WithPrinter(newXMLPrinter()),
			niceyaml.WithSourceLines(0),
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

		got := trimLines(err.Error())

		// Should show both error annotations.
		assert.Contains(t, got, "error at line1")
		assert.Contains(t, got, "error at line2")
		// Should NOT have "..." separator since errors are adjacent.
		assert.NotContains(t, got, "...")
	})
}
