package niceyaml_test

import (
	"errors"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/lexer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/macropower/niceyaml"
)

func TestError(t *testing.T) {
	t.Parallel()

	source := `a: b
foo: bar
key: value`
	tokens := lexer.Tokenize(source)

	tcs := map[string]struct {
		err          error
		wantExact    string
		wantContains []string
		wantEmpty    bool
	}{
		"nil error returns empty string": {
			err:       niceyaml.NewError(nil),
			wantEmpty: true,
		},
		"no path or token returns plain error": {
			err:       niceyaml.NewError(errors.New("something went wrong")),
			wantExact: "something went wrong",
		},
		"with path and tokens shows annotated source": {
			err: niceyaml.NewError(
				errors.New("invalid value"),
				niceyaml.WithPath(niceyaml.NewPathBuilder().Root().Child("key").Build()),
				niceyaml.WithTokens(tokens),
			),
			wantContains: []string{"[3:1]", "invalid value"},
		},
		"with direct token bypasses path resolution": {
			err: niceyaml.NewError(
				errors.New("bad token"),
				niceyaml.WithErrorToken(tokens[0]),
			),
			wantContains: []string{"[1:1]", "bad token"},
		},
		"invalid path gracefully degrades": {
			err: niceyaml.NewError(
				errors.New("not found"),
				niceyaml.WithPath(niceyaml.NewPathBuilder().Root().Child("nonexistent").Build()),
				niceyaml.WithTokens(tokens),
			),
			wantContains: []string{"error at $.nonexistent", "not found"},
		},
		"path without tokens gracefully degrades": {
			err: niceyaml.NewError(
				errors.New("missing tokens"),
				niceyaml.WithPath(niceyaml.NewPathBuilder().Root().Child("key").Build()),
			),
			wantContains: []string{"error at $.key", "missing tokens"},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := tc.err.Error()

			if tc.wantEmpty {
				assert.Empty(t, got)
				return
			}
			if tc.wantExact != "" {
				assert.Equal(t, tc.wantExact, got)
				return
			}

			require.NotEmpty(t, tc.wantContains, "test case must specify wantContains")

			for _, want := range tc.wantContains {
				assert.Contains(t, got, want)
			}
		})
	}
}

func TestError_EmptyFile(t *testing.T) {
	t.Parallel()

	// Tokenize an empty string to simulate an empty YAML file.
	tokens := lexer.Tokenize("")

	err := niceyaml.NewError(
		errors.New("error in empty file"),
		niceyaml.WithPath(niceyaml.NewPathBuilder().Root().Child("key").Build()),
		niceyaml.WithTokens(tokens),
	)

	got := err.Error()
	// With empty tokens, path resolution should fail gracefully.
	assert.Contains(t, got, "error at $.key")
	assert.Contains(t, got, "error in empty file")
}

func TestErrorWrapper(t *testing.T) {
	t.Parallel()

	source := `name: test
value: 123`
	tokens := lexer.Tokenize(source)

	tcs := map[string]struct {
		wrapperOpts  func() []niceyaml.ErrorOpt
		inputErr     func() error
		wrapOpts     func() []niceyaml.ErrorOpt
		wantContains []string
		wantNil      bool
		wantSameErr  bool
	}{
		"wrap nil returns nil": {
			wrapperOpts: func() []niceyaml.ErrorOpt { return nil },
			inputErr:    func() error { return nil },
			wantNil:     true,
		},
		"wrap non-error type returns unchanged": {
			wrapperOpts: func() []niceyaml.ErrorOpt { return nil },
			inputErr:    func() error { return errors.New("plain error") },
			wantSameErr: true,
		},
		"wrap error applies default options": {
			wrapperOpts: func() []niceyaml.ErrorOpt { return []niceyaml.ErrorOpt{niceyaml.WithTokens(tokens)} },
			inputErr: func() error {
				return niceyaml.NewError(
					errors.New("test error"),
					niceyaml.WithPath(niceyaml.NewPathBuilder().Root().Child("name").Build()),
				)
			},
			wantContains: []string{"[1:1]", "test error"},
		},
		"call-site options override defaults": {
			wrapperOpts: func() []niceyaml.ErrorOpt { return []niceyaml.ErrorOpt{niceyaml.WithSourceLines(1)} },
			inputErr: func() error {
				return niceyaml.NewError(
					errors.New("test error"),
					niceyaml.WithPath(niceyaml.NewPathBuilder().Root().Child("name").Build()),
				)
			},
			wrapOpts: func() []niceyaml.ErrorOpt {
				return []niceyaml.ErrorOpt{niceyaml.WithTokens(tokens), niceyaml.WithSourceLines(3)}
			},
			wantContains: []string{"[1:1]"},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			wrapper := niceyaml.NewErrorWrapper(tc.wrapperOpts()...)
			inputErr := tc.inputErr()

			var wrapOpts []niceyaml.ErrorOpt
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

			for _, want := range tc.wantContains {
				assert.Contains(t, got.Error(), want)
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
				niceyaml.WithPath(niceyaml.NewPathBuilder().Root().Child("foo").Build()),
			),
			want: "$.foo",
		},
		"nested path": {
			err: niceyaml.NewError(
				errors.New("test"),
				niceyaml.WithPath(niceyaml.NewPathBuilder().Root().Child("foo").Child("bar").Build()),
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

func TestErrorAnnotation(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		pathBuilder  func() *yaml.Path
		source       string
		errMsg       string
		wantContains []string
		sourceLines  int
	}{
		"nested path shows correct key": {
			source:       "foo:\n  bar: value",
			pathBuilder:  func() *yaml.Path { return niceyaml.NewPathBuilder().Root().Child("foo").Child("bar").Build() },
			errMsg:       "nested error",
			wantContains: []string{"[2:3]", "nested error"},
		},
		"array element path - first item": {
			source:       "items:\n  - first\n  - second",
			pathBuilder:  func() *yaml.Path { return niceyaml.NewPathBuilder().Root().Child("items").Index(0).Build() },
			errMsg:       "array error",
			wantContains: []string{"array error", "first"},
		},
		"array element path - nested object in array": {
			source: "users:\n  - name: alice\n    age: 30",
			pathBuilder: func() *yaml.Path {
				return niceyaml.NewPathBuilder().Root().Child("users").Index(0).Child("name").Build()
			},
			errMsg:       "nested array error",
			wantContains: []string{"nested array error"},
		},
		"root path": {
			source:       "key: value",
			pathBuilder:  func() *yaml.Path { return niceyaml.NewPathBuilder().Root().Build() },
			errMsg:       "root error",
			wantContains: []string{"root error"},
		},
		"single top-level key path": {
			source:       "key: value",
			pathBuilder:  func() *yaml.Path { return niceyaml.NewPathBuilder().Root().Child("key").Build() },
			errMsg:       "top level error",
			wantContains: []string{"[1:1]", "top level error"},
		},
		"with custom source lines": {
			source:       "line1: a\nline2: b\nline3: c\nline4: d\nline5: e",
			pathBuilder:  func() *yaml.Path { return niceyaml.NewPathBuilder().Root().Child("line3").Build() },
			errMsg:       "middle error",
			sourceLines:  1,
			wantContains: []string{"[3:1]", "middle error"},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			tokens := lexer.Tokenize(tc.source)

			opts := []niceyaml.ErrorOpt{
				niceyaml.WithPath(tc.pathBuilder()),
				niceyaml.WithTokens(tokens),
			}
			if tc.sourceLines > 0 {
				opts = append(opts, niceyaml.WithSourceLines(tc.sourceLines))
			}

			err := niceyaml.NewError(errors.New(tc.errMsg), opts...)

			got := err.Error()
			for _, want := range tc.wantContains {
				assert.Contains(t, got, want)
			}
		})
	}
}

func TestWithPrinter(t *testing.T) {
	t.Parallel()

	source := `key: value
foo: bar`
	tokens := lexer.Tokenize(source)

	t.Run("custom printer is used for error formatting", func(t *testing.T) {
		t.Parallel()

		// Create a printer with a transform that wraps content in markers.
		customPrinter := niceyaml.NewPrinter(
			niceyaml.WithStyle(lipgloss.NewStyle().Transform(func(s string) string {
				return "<<" + s + ">>"
			})),
			niceyaml.WithColorScheme(niceyaml.ColorScheme{}),
		)

		err := niceyaml.NewError(
			errors.New("test error"),
			niceyaml.WithErrorToken(tokens[0]),
			niceyaml.WithPrinter(customPrinter),
		)

		got := err.Error()
		// The custom printer's transform should wrap the source in markers.
		assert.Contains(t, got, "<<")
		assert.Contains(t, got, ">>")
		assert.Contains(t, got, "test error")
	})

	t.Run("custom printer with line numbers", func(t *testing.T) {
		t.Parallel()

		customPrinter := niceyaml.NewPrinter(
			niceyaml.WithLineNumbers(),
			niceyaml.WithColorScheme(niceyaml.ColorScheme{}),
			niceyaml.WithStyle(lipgloss.NewStyle()),
		)

		err := niceyaml.NewError(
			errors.New("line number error"),
			niceyaml.WithErrorToken(tokens[0]),
			niceyaml.WithPrinter(customPrinter),
		)

		got := err.Error()
		// Should contain line numbers from the custom printer.
		assert.Contains(t, got, "1")
		assert.Contains(t, got, "line number error")
	})
}

func TestError_RootLevelArrayPath(t *testing.T) {
	t.Parallel()

	// Path to array element at root level - parent is SequenceNode, not MappingValueNode.
	// This tests findKeyToken returning nil when parent is not a MappingValueNode.
	source := `- first
- second
- third`
	tokens := lexer.Tokenize(source)

	err := niceyaml.NewError(
		errors.New("array element error"),
		niceyaml.WithPath(niceyaml.NewPathBuilder().Root().Index(1).Build()),
		niceyaml.WithTokens(tokens),
	)

	got := err.Error()
	// Should still work and show the error.
	assert.Contains(t, got, "array element error")
	// Should point to line 2 where "second" is.
	assert.Contains(t, got, "[2:")
}

func TestError_DocumentRootPath(t *testing.T) {
	t.Parallel()

	// Path to document root - parent is nil.
	// This tests findKeyToken returning nil when there's no parent.
	source := `key: value
another: line`
	tokens := lexer.Tokenize(source)

	err := niceyaml.NewError(
		errors.New("document root error"),
		niceyaml.WithPath(niceyaml.NewPathBuilder().Root().Build()),
		niceyaml.WithTokens(tokens),
	)

	got := err.Error()
	// Should show the error at the root.
	assert.Contains(t, got, "document root error")
}
