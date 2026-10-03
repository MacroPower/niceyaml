package matcher_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.jacobcolvin.com/x/stringtest"

	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/paths"
	"go.jacobcolvin.com/niceyaml/schema/matcher"
)

func TestExists(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input string
		want  bool
	}{
		"field exists with value": {
			input: stringtest.Input(`kind: Deployment`),
			want:  true,
		},
		"field missing": {
			input: stringtest.Input(`apiVersion: v1`),
			want:  false,
		},
		"field empty unquoted": {
			input: stringtest.Input(`kind:`),
			want:  true,
		},
		"field null": {
			input: stringtest.Input(`kind: null`),
			want:  true,
		},
		"field empty double quoted": {
			input: stringtest.Input(`kind: ""`),
			want:  true,
		},
		"field empty single quoted": {
			input: stringtest.Input(`kind: ''`),
			want:  true,
		},
		"field with mapping value": {
			input: stringtest.Input(`
				kind:
				  name: x
			`),
			want: true,
		},
		"document without content": {
			input: stringtest.Input(`# only a comment`),
			want:  false,
		},
		"field with whitespace value": {
			input: stringtest.Input(`kind: " "`),
			want:  true,
		},
		"field with numeric value": {
			input: stringtest.Input(`kind: 123`),
			want:  true,
		},
		"field with boolean value": {
			input: stringtest.Input(`kind: true`),
			want:  true,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			m := matcher.Exists(kindPath)
			doc := yamltest.FirstDocument(t, tc.input)

			got := match(t, m, doc)
			assert.Equal(t, tc.want, got)
		})
	}

	t.Run("nested path exists", func(t *testing.T) {
		t.Parallel()

		m := matcher.Exists(metadataName)
		doc := yamltest.FirstDocument(t, stringtest.Input(`
			kind: Deployment
			metadata:
			  name: my-app
		`))

		got := match(t, m, doc)
		assert.True(t, got)
	})

	t.Run("nested path missing", func(t *testing.T) {
		t.Parallel()

		m := matcher.Exists(metadataName)
		doc := yamltest.FirstDocument(t, stringtest.Input(`
			kind: Deployment
			metadata:
			  namespace: default
		`))

		got := match(t, m, doc)
		assert.False(t, got)
	})

	t.Run("alias without an anchor is an error", func(t *testing.T) {
		t.Parallel()

		m := matcher.Exists(metadataName)
		doc := yamltest.FirstDocument(t, stringtest.Input(`metadata: *missing`))

		_, err := m.Match(t.Context(), doc)
		require.ErrorIs(t, err, paths.ErrAlias)
	})

	t.Run("wildcard path is an error", func(t *testing.T) {
		t.Parallel()

		doc := yamltest.FirstDocument(t, stringtest.Input(`
			jobs:
			  build:
			    steps:
			      - uses: checkout
		`))

		for _, expr := range []string{"$.jobs.*", "$.jobs.*.steps", "$.jobs.build.steps[*]", "$..uses", "$..*"} {
			_, err := matcher.Exists(paths.MustParse(expr)).Match(t.Context(), doc)
			require.ErrorIs(t, err, paths.ErrWildcard, expr)
		}
	})
}

func TestExists_WithAll(t *testing.T) {
	t.Parallel()

	t.Run("both fields exist", func(t *testing.T) {
		t.Parallel()

		m := matcher.All(
			matcher.Exists(kindPath),
			matcher.Exists(apiVersionPath),
		)
		doc := yamltest.FirstDocument(t, stringtest.Input(`
			kind: Deployment
			apiVersion: apps/v1
		`))

		got := match(t, m, doc)
		assert.True(t, got)
	})

	t.Run("one field missing", func(t *testing.T) {
		t.Parallel()

		m := matcher.All(
			matcher.Exists(kindPath),
			matcher.Exists(apiVersionPath),
		)
		doc := yamltest.FirstDocument(t, stringtest.Input(`kind: Deployment`))

		got := match(t, m, doc)
		assert.False(t, got)
	})

	t.Run("one field empty", func(t *testing.T) {
		t.Parallel()

		m := matcher.All(
			matcher.Exists(kindPath),
			matcher.Exists(apiVersionPath),
		)
		doc := yamltest.FirstDocument(t, stringtest.Input(`
			kind: Deployment
			apiVersion: ""
		`))

		got := match(t, m, doc)
		assert.True(t, got)
	})
}

func TestExists_ContextEnded(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	doc := yamltest.FirstDocument(t, stringtest.Input(`kind: Deployment`))

	// A matcher whose context ended cannot decide, so it returns the error
	// rather than a match, and the registry stops at the document.
	ok, err := matcher.Exists(paths.Root().Child("kind")).Match(ctx, doc)
	require.ErrorIs(t, err, context.Canceled)
	assert.False(t, ok)
}
