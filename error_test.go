package niceyaml_test

import (
	"errors"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/lexer"
	"github.com/goccy/go-yaml/parser"
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
	file, err := parser.Parse(tokens, 0)
	require.NoError(t, err)

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
		"with path and tokens shows annotated source": {
			err: niceyaml.NewError(
				errors.New("invalid value"),
				niceyaml.WithPath(niceyaml.NewPathBuilder().Child("key").Build()),
				niceyaml.WithTokens(tokens),
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
		"with path and file shows annotated source": {
			err: niceyaml.NewError(
				errors.New("invalid value"),
				niceyaml.WithPath(niceyaml.NewPathBuilder().Child("key").Build()),
				niceyaml.WithFile(file),
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

func TestErrorWrapper(t *testing.T) {
	t.Parallel()

	source := yamltest.Input(`
		name: test
		value: 123
	`)
	tokens := lexer.Tokenize(source)

	newXMLPrinter := func() *niceyaml.Printer {
		return niceyaml.NewPrinter(
			niceyaml.WithStyles(yamltest.NewXMLStyles()),
			niceyaml.WithGutter(niceyaml.NoGutter),
			niceyaml.WithStyle(lipgloss.NewStyle()),
		)
	}

	tcs := map[string]struct {
		wrapperOpts func() []niceyaml.ErrorOption
		inputErr    func() error
		wrapOpts    func() []niceyaml.ErrorOption
		wantExact   string
		wantNil     bool
		wantSameErr bool
	}{
		"wrap nil returns nil": {
			wrapperOpts: func() []niceyaml.ErrorOption { return nil },
			inputErr:    func() error { return nil },
			wantNil:     true,
		},
		"wrap non-error type returns unchanged": {
			wrapperOpts: func() []niceyaml.ErrorOption { return nil },
			inputErr:    func() error { return errors.New("plain error") },
			wantSameErr: true,
		},
		"wrap error applies default options": {
			wrapperOpts: func() []niceyaml.ErrorOption {
				return []niceyaml.ErrorOption{niceyaml.WithTokens(tokens), niceyaml.WithPrinter(newXMLPrinter())}
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
		"call-site options override defaults": {
			wrapperOpts: func() []niceyaml.ErrorOption { return []niceyaml.ErrorOption{niceyaml.WithSourceLines(1)} },
			inputErr: func() error {
				return niceyaml.NewError(
					errors.New("test error"),
					niceyaml.WithPath(niceyaml.NewPathBuilder().Child("name").Build()),
				)
			},
			wrapOpts: func() []niceyaml.ErrorOption {
				return []niceyaml.ErrorOption{
					niceyaml.WithTokens(tokens),
					niceyaml.WithSourceLines(3),
					niceyaml.WithPrinter(newXMLPrinter()),
				}
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

			wrapper := niceyaml.NewErrorWrapper(tc.wrapperOpts()...)
			inputErr := tc.inputErr()

			var wrapOpts []niceyaml.ErrorOption
			if tc.wrapOpts != nil {
				wrapOpts = tc.wrapOpts()
			}

			got := wrapper.Wrap(inputErr, wrapOpts...)

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

			got := tc.err.GetPath()
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestError_GracefulDegradation(t *testing.T) {
	t.Parallel()

	// Create tokens from a simple source for tests that need them.
	source := `key: value`
	tokens := lexer.Tokenize(source)
	file, err := parser.Parse(tokens, 0)
	require.NoError(t, err)

	// Empty tokens and file for edge case tests.
	emptyTokens := lexer.Tokenize("")
	emptyFile, err := parser.Parse(emptyTokens, 0)
	require.NoError(t, err)

	tcs := map[string]struct {
		err  *niceyaml.Error
		want string
	}{
		"invalid path": {
			err: niceyaml.NewError(
				errors.New("not found"),
				niceyaml.WithPath(niceyaml.NewPathBuilder().Child("nonexistent").Build()),
				niceyaml.WithTokens(tokens),
			),
			want: "error at $.nonexistent: not found",
		},
		"path without tokens": {
			err: niceyaml.NewError(
				errors.New("missing tokens"),
				niceyaml.WithPath(niceyaml.NewPathBuilder().Child("key").Build()),
			),
			want: "error at $.key: missing tokens",
		},
		"empty file": {
			err: niceyaml.NewError(
				errors.New("error in empty file"),
				niceyaml.WithPath(niceyaml.NewPathBuilder().Child("key").Build()),
				niceyaml.WithTokens(emptyTokens),
			),
			want: "error at $.key: error in empty file",
		},
		"nil file": {
			err: niceyaml.NewError(
				errors.New("nil file error"),
				niceyaml.WithPath(niceyaml.NewPathBuilder().Child("key").Build()),
				niceyaml.WithFile(nil),
			),
			want: "error at $.key: nil file error",
		},
		"empty docs": {
			err: niceyaml.NewError(
				errors.New("empty docs error"),
				niceyaml.WithPath(niceyaml.NewPathBuilder().Child("key").Build()),
				niceyaml.WithFile(emptyFile),
			),
			want: "error at $.key: empty docs error",
		},
		"nonexistent path in file": {
			err: niceyaml.NewError(
				errors.New("path not found"),
				niceyaml.WithPath(niceyaml.NewPathBuilder().Child("nonexistent").Child("deep").Build()),
				niceyaml.WithFile(file),
			),
			want: "error at $.nonexistent.deep: path not found",
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
		wantExact   string
		sourceLines int
		useFile     bool // Use WithFile instead of WithTokens.
	}{
		"nested path shows correct key": {
			source: yamltest.Input(`
				foo:
				  bar: value
			`),
			path:   niceyaml.NewPathBuilder().Child("foo").Child("bar").Build(),
			errMsg: "nested error",
			wantExact: yamltest.JoinLF(
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
			wantExact: yamltest.JoinLF(
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
			wantExact: yamltest.JoinLF(
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
			wantExact: yamltest.JoinLF(
				"[1:4] root error:",
				"",
				"<key>key</key><error>:</error><default> </default><string>value</string>",
			),
		},
		"single top-level key path": {
			source: "key: value",
			path:   niceyaml.NewPathBuilder().Child("key").Build(),
			errMsg: "top level error",
			wantExact: yamltest.JoinLF(
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
			wantExact: yamltest.JoinLF(
				"[3:1] middle error:",
				"",
				"<key>line2</key><punctuation>:</punctuation><default> </default><string>b</string>",
				"<error>line3</error><punctuation>:</punctuation><default> </default><string>c</string>",
				"<key>line4</key><punctuation>:</punctuation><default> </default><string>d</string>",
				"<key>line5</key><punctuation>:</punctuation><default> </default><string>e</string>",
			),
		},
		"basic path with file": {
			source: yamltest.Input(`
				foo: bar
				key: value
			`),
			path:    niceyaml.NewPathBuilder().Child("key").Build(),
			errMsg:  "file error",
			useFile: true,
			wantExact: yamltest.JoinLF(
				"[2:1] file error:",
				"",
				"<key>foo</key><punctuation>:</punctuation><default> </default><string>bar</string>",
				"<error>key</error><punctuation>:</punctuation><default> </default><string>value</string>",
			),
		},
		"nested path with file": {
			source: yamltest.Input(`
				outer:
				  inner: value
			`),
			path:    niceyaml.NewPathBuilder().Child("outer").Child("inner").Build(),
			errMsg:  "nested file error",
			useFile: true,
			wantExact: yamltest.JoinLF(
				"[2:3] nested file error:",
				"",
				"<key>outer</key><punctuation>:</punctuation>",
				"<error>  </error><error>inner</error><punctuation>:</punctuation><default> </default><string>value</string>",
			),
		},
		"array element with file": {
			source: yamltest.Input(`
				items:
				  - first
				  - second
			`),
			path:    niceyaml.NewPathBuilder().Child("items").Index(1).Build(),
			errMsg:  "array file error",
			useFile: true,
			wantExact: yamltest.JoinLF(
				"[3:5] array file error:",
				"",
				"<key>items</key><punctuation>:</punctuation>",
				"<default>  </default><punctuation>-</punctuation><default> </default><string>first</string>",
				"<string>  </string><punctuation>-</punctuation><error> </error><error>second</error>",
			),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			tokens := lexer.Tokenize(tc.source)

			opts := []niceyaml.ErrorOption{
				niceyaml.WithPath(tc.path),
				niceyaml.WithPrinter(niceyaml.NewPrinter(
					niceyaml.WithStyles(yamltest.NewXMLStyles()),
					niceyaml.WithGutter(niceyaml.NoGutter),
					niceyaml.WithStyle(lipgloss.NewStyle()),
				)),
			}
			if tc.useFile {
				file, parseErr := parser.Parse(tokens, 0)
				require.NoError(t, parseErr)

				opts = append(opts, niceyaml.WithFile(file))
			} else {
				opts = append(opts, niceyaml.WithTokens(tokens))
			}
			if tc.sourceLines > 0 {
				opts = append(opts, niceyaml.WithSourceLines(tc.sourceLines))
			}

			err := niceyaml.NewError(errors.New(tc.errMsg), opts...)

			assert.Equal(t, tc.wantExact, trimLines(err.Error()))
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
		source    string
		path      *yaml.Path
		errMsg    string
		wantExact string
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
			wantExact: yamltest.JoinLF(
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
			wantExact: yamltest.JoinLF(
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

			tokens := lexer.Tokenize(tc.source)

			err := niceyaml.NewError(
				errors.New(tc.errMsg),
				niceyaml.WithPath(tc.path),
				niceyaml.WithTokens(tokens),
				niceyaml.WithPrinter(newXMLPrinter()),
			)

			assert.Equal(t, tc.wantExact, trimLines(err.Error()))
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
