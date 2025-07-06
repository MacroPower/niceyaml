package niceyaml_test

import (
	"errors"
	"testing"

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

			for _, want := range tc.wantContains {
				assert.Contains(t, got, want)
			}
		})
	}
}

func TestErrorWrapper(t *testing.T) {
	t.Parallel()

	source := `name: test
value: 123`
	tokens := lexer.Tokenize(source)

	t.Run("wrap nil returns nil", func(t *testing.T) {
		t.Parallel()

		wrapper := niceyaml.NewErrorWrapper()
		got := wrapper.Wrap(nil)
		assert.NoError(t, got)
	})

	t.Run("wrap non-error type returns unchanged", func(t *testing.T) {
		t.Parallel()

		wrapper := niceyaml.NewErrorWrapper()
		plainErr := errors.New("plain error")
		got := wrapper.Wrap(plainErr)
		assert.Equal(t, plainErr, got)
	})

	t.Run("wrap error applies default options", func(t *testing.T) {
		t.Parallel()

		wrapper := niceyaml.NewErrorWrapper(
			niceyaml.WithTokens(tokens),
		)
		err := niceyaml.NewError(
			errors.New("test error"),
			niceyaml.WithPath(niceyaml.NewPathBuilder().Root().Child("name").Build()),
		)
		got := wrapper.Wrap(err)

		require.Error(t, got)
		assert.Contains(t, got.Error(), "[1:1]")
		assert.Contains(t, got.Error(), "test error")
	})

	t.Run("call-site options override defaults", func(t *testing.T) {
		t.Parallel()

		wrapper := niceyaml.NewErrorWrapper(
			niceyaml.WithSourceLines(1),
		)
		err := niceyaml.NewError(
			errors.New("test error"),
			niceyaml.WithPath(niceyaml.NewPathBuilder().Root().Child("name").Build()),
		)

		got := wrapper.Wrap(err, niceyaml.WithTokens(tokens), niceyaml.WithSourceLines(3))

		require.Error(t, got)
		assert.Contains(t, got.Error(), "[1:1]")
	})
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

	t.Run("nested path shows correct key", func(t *testing.T) {
		t.Parallel()

		source := `foo:
  bar: value`
		tokens := lexer.Tokenize(source)

		err := niceyaml.NewError(
			errors.New("nested error"),
			niceyaml.WithPath(niceyaml.NewPathBuilder().Root().Child("foo").Child("bar").Build()),
			niceyaml.WithTokens(tokens),
		)

		got := err.Error()
		assert.Contains(t, got, "[2:3]")
		assert.Contains(t, got, "nested error")
	})

	t.Run("array element path", func(t *testing.T) {
		t.Parallel()

		source := `items:
  - first
  - second`
		tokens := lexer.Tokenize(source)

		err := niceyaml.NewError(
			errors.New("array error"),
			niceyaml.WithPath(niceyaml.NewPathBuilder().Root().Child("items").Index(0).Build()),
			niceyaml.WithTokens(tokens),
		)

		got := err.Error()
		assert.Contains(t, got, "array error")
	})

	t.Run("root path", func(t *testing.T) {
		t.Parallel()

		source := `key: value`
		tokens := lexer.Tokenize(source)

		err := niceyaml.NewError(
			errors.New("root error"),
			niceyaml.WithPath(niceyaml.NewPathBuilder().Root().Build()),
			niceyaml.WithTokens(tokens),
		)

		got := err.Error()
		assert.Contains(t, got, "root error")
	})

	t.Run("with custom source lines", func(t *testing.T) {
		t.Parallel()

		source := `line1: a
line2: b
line3: c
line4: d
line5: e`
		tokens := lexer.Tokenize(source)

		err := niceyaml.NewError(
			errors.New("middle error"),
			niceyaml.WithPath(niceyaml.NewPathBuilder().Root().Child("line3").Build()),
			niceyaml.WithTokens(tokens),
			niceyaml.WithSourceLines(1),
		)

		got := err.Error()
		assert.Contains(t, got, "[3:1]")
		assert.Contains(t, got, "middle error")
	})
}
