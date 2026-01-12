package niceyaml_test

import (
	"errors"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/lexer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/macropower/niceyaml"
	"github.com/macropower/niceyaml/yamltest"
)

// trimLines trims trailing whitespace from each line of a string.
// This is useful for comparing styled output where lipgloss adds padding.
func trimLines(s string) string {
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimRight(line, " \t")
	}

	return strings.Join(lines, "\n")
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
			err:  niceyaml.NewError(nil),
			want: "",
		},
		"no path or token returns plain error": {
			err:  niceyaml.NewError(errors.New("something went wrong")),
			want: "something went wrong",
		},
		"with path and source shows annotated source": {
			err: niceyaml.NewError(
				errors.New("invalid value"),
				niceyaml.WithPath(niceyaml.NewPathBuilder().Child("key").Build()),
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
				"<key>a</key><punctuation>:</punctuation><default> </default><string>b</string>",
				"<key>foo</key><punctuation>:</punctuation><default> </default><string>bar</string>",
				"<error>key</error><punctuation>:</punctuation><default> </default><string>value</string>",
			),
		},
		"with direct token bypasses path resolution": {
			err: niceyaml.NewError(
				errors.New("bad token"),
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
				"<error>a</error><punctuation>:</punctuation><default> </default><string>b</string>",
				"<key>foo</key><punctuation>:</punctuation><default> </default><string>bar</string>",
				"<key>key</key><punctuation>:</punctuation><default> </default><string>value</string>",
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
					errors.New("test error"),
					niceyaml.WithPath(niceyaml.NewPathBuilder().Child("name").Build()),
				)
			},
			wantExact: yamltest.JoinLF(
				"[1:1] test error:",
				"",
				"<error>name</error><punctuation>:</punctuation><default> </default><string>test</string>",
				"<key>value</key><punctuation>:</punctuation><default> </default><number>123</number>",
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
			err:  niceyaml.NewError(errors.New("test")),
			want: "",
		},
		"returns path string when set": {
			err: niceyaml.NewError(
				errors.New("test"),
				niceyaml.WithPath(niceyaml.NewPathBuilder().Child("foo").Build()),
			),
			want: "$.foo",
		},
		"nested path": {
			err: niceyaml.NewError(
				errors.New("test"),
				niceyaml.WithPath(niceyaml.NewPathBuilder().Child("foo").Child("bar").Build()),
			),
			want: "$.foo.bar",
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
				errors.New("not found"),
				niceyaml.WithPath(niceyaml.NewPathBuilder().Child("nonexistent").Build()),
				niceyaml.WithSource(niceyaml.NewSourceFromTokens(tokens)),
			),
			want: "at $.nonexistent: not found",
		},
		"path without source": {
			err: niceyaml.NewError(
				errors.New("missing source"),
				niceyaml.WithPath(niceyaml.NewPathBuilder().Child("key").Build()),
			),
			want: "at $.key: missing source",
		},
		"empty source": {
			err: niceyaml.NewError(
				errors.New("error in empty source"),
				niceyaml.WithPath(niceyaml.NewPathBuilder().Child("key").Build()),
				niceyaml.WithSource(niceyaml.NewSourceFromTokens(emptyTokens)),
			),
			want: "at $.key: error in empty source",
		},
		"nil source": {
			err: niceyaml.NewError(
				errors.New("nil source error"),
				niceyaml.WithPath(niceyaml.NewPathBuilder().Child("key").Build()),
				niceyaml.WithSource(nil),
			),
			want: "at $.key: nil source error",
		},
		"nonexistent path in source": {
			err: niceyaml.NewError(
				errors.New("path not found"),
				niceyaml.WithPath(niceyaml.NewPathBuilder().Child("nonexistent").Child("deep").Build()),
				niceyaml.WithSource(niceyaml.NewSourceFromTokens(tokens)),
			),
			want: "at $.nonexistent.deep: path not found",
		},
		"empty document source": {
			// Tests graceful handling when source has no documents (Docs slice is empty).
			err: niceyaml.NewError(
				errors.New("empty doc error"),
				niceyaml.WithPath(niceyaml.NewPathBuilder().Child("key").Build()),
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
		path        *yaml.Path
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
			path:   niceyaml.NewPathBuilder().Child("foo").Child("bar").Build(),
			errMsg: "nested error",
			want: yamltest.JoinLF(
				"[2:3] nested error:",
				"",
				"<key>foo</key><punctuation>:</punctuation>",
				"<error>  </error><error>bar</error><punctuation>:</punctuation><default> </default><string>value</string>",
			),
		},
		"array element path - first item": {
			source: yamltest.Input(`
				items:
				  - first
				  - second
			`),
			path:   niceyaml.NewPathBuilder().Child("items").Index(0).Build(),
			errMsg: "array error",
			want: yamltest.JoinLF(
				"[2:5] array error:",
				"",
				"<key>items</key><punctuation>:</punctuation>",
				"<default>  </default><punctuation>-</punctuation><error> </error><error>first</error>",
				"<error>  </error><punctuation>-</punctuation><default> </default><string>second</string>",
			),
		},
		"array element path - nested object in array": {
			source: yamltest.Input(`
				users:
				  - name: alice
				    age: 30
			`),
			path:   niceyaml.NewPathBuilder().Child("users").Index(0).Child("name").Build(),
			errMsg: "nested array error",
			want: yamltest.JoinLF(
				"[2:5] nested array error:",
				"",
				"<key>users</key><punctuation>:</punctuation>",
				"<default>  </default><punctuation>-</punctuation><error> </error><error>name</error><punctuation>:</punctuation><default> </default><string>alice</string>",
				"<string>    </string><key>age</key><punctuation>:</punctuation><default> </default><number>30</number>",
			),
		},
		"root path": {
			source: "key: value",
			path:   niceyaml.NewPathBuilder().Build(),
			errMsg: "root error",
			want: yamltest.JoinLF(
				"[1:4] root error:",
				"",
				"<key>key</key><error>:</error><default> </default><string>value</string>",
			),
		},
		"single top-level key path": {
			source: "key: value",
			path:   niceyaml.NewPathBuilder().Child("key").Build(),
			errMsg: "top level error",
			want: yamltest.JoinLF(
				"[1:1] top level error:",
				"",
				"<error>key</error><punctuation>:</punctuation><default> </default><string>value</string>",
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
			path:        niceyaml.NewPathBuilder().Child("line3").Build(),
			errMsg:      "middle error",
			sourceLines: 1,
			want: yamltest.JoinLF(
				"[3:1] middle error:",
				"",
				"<key>line2</key><punctuation>:</punctuation><default> </default><string>b</string>",
				"<error>line3</error><punctuation>:</punctuation><default> </default><string>c</string>",
				"<key>line4</key><punctuation>:</punctuation><default> </default><string>d</string>",
				"<key>line5</key><punctuation>:</punctuation><default> </default><string>e</string>",
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

			err := niceyaml.NewError(errors.New(tc.errMsg), opts...)

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
		errors.New("test error"),
		niceyaml.WithErrorToken(tokens[0]),
		niceyaml.WithPrinter(customPrinter),
	)

	want := yamltest.JoinLF(
		"[1:1] test error:",
		"",
		"<error>key</error><punctuation>:</punctuation><default> </default><string>value</string>",
		"<key>foo</key><punctuation>:</punctuation><default> </default><string>bar</string>",
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
		path   *yaml.Path
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
			path:   niceyaml.NewPathBuilder().Index(1).Build(),
			errMsg: "array element error",
			want: yamltest.JoinLF(
				"[2:3] array element error:",
				"",
				"<punctuation>-</punctuation><default> </default><string>first</string>",
				"<punctuation>-</punctuation><error> </error><error>second</error>",
				"<punctuation>-</punctuation><default> </default><string>third</string>",
			),
		},
		"document root - parent is nil": {
			// Tests findKeyToken returning nil when there's no parent.
			source: yamltest.Input(`
				key: value
				another: line
			`),
			path:   niceyaml.NewPathBuilder().Build(),
			errMsg: "document root error",
			want: yamltest.JoinLF(
				"[1:4] document root error:",
				"",
				"<key>key</key><error>:</error><default> </default><string>value</string>",
				"<key>another</key><punctuation>:</punctuation><default> </default><string>line</string>",
			),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			err := niceyaml.NewError(
				errors.New(tc.errMsg),
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
		errors.New("nil token error"),
		niceyaml.WithErrorToken(nil),
	)

	got := err.Error()
	// Should still work and show the error message.
	assert.Equal(t, "nil token error", got)
}

func TestError_Unwrap(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input          error
		wantReturnsNil bool
	}{
		"unwraps underlying error": {
			input: errors.New("underlying error"),
		},
		"nil error unwraps to nil": {
			input:          nil,
			wantReturnsNil: true,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			err := niceyaml.NewError(tc.input)
			got := errors.Unwrap(err)

			if tc.wantReturnsNil {
				assert.NoError(t, got)
			} else {
				assert.Equal(t, tc.input, got)
			}
		})
	}

	t.Run("errors.Is works through Error wrapper", func(t *testing.T) {
		t.Parallel()

		sentinel := errors.New("sentinel error")
		err := niceyaml.NewError(sentinel)

		require.ErrorIs(t, err, sentinel)
	})
}
