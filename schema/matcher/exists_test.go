package matcher_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/schema/matcher"
)

func TestExists(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input string
		want  bool
	}{
		"field exists with value": {
			input: yamltest.Input(`kind: Deployment`),
			want:  true,
		},
		"field missing": {
			input: yamltest.Input(`apiVersion: v1`),
			want:  false,
		},
		"field empty unquoted": {
			input: yamltest.Input(`kind:`),
			want:  false,
		},
		"field empty double quoted": {
			input: yamltest.Input(`kind: ""`),
			want:  false,
		},
		"field empty single quoted": {
			input: yamltest.Input(`kind: ''`),
			want:  false,
		},
		"field with whitespace value": {
			input: yamltest.Input(`kind: " "`),
			want:  true,
		},
		"field with numeric value": {
			input: yamltest.Input(`kind: 123`),
			want:  true,
		},
		"field with boolean value": {
			input: yamltest.Input(`kind: true`),
			want:  true,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			m := matcher.Exists(kindPath)
			doc := yamltest.FirstDocument(t, tc.input)

			got := m.Match(t.Context(), doc)
			assert.Equal(t, tc.want, got)
		})
	}

	t.Run("nested path exists", func(t *testing.T) {
		t.Parallel()

		m := matcher.Exists(metadataName)
		doc := yamltest.FirstDocument(t, yamltest.Input(`
			kind: Deployment
			metadata:
			  name: my-app
		`))

		got := m.Match(t.Context(), doc)
		assert.True(t, got)
	})

	t.Run("nested path missing", func(t *testing.T) {
		t.Parallel()

		m := matcher.Exists(metadataName)
		doc := yamltest.FirstDocument(t, yamltest.Input(`
			kind: Deployment
			metadata:
			  namespace: default
		`))

		got := m.Match(t.Context(), doc)
		assert.False(t, got)
	})

	t.Run("nil path panics", func(t *testing.T) {
		t.Parallel()

		assert.PanicsWithValue(t, "matcher.Exists: path is nil", func() {
			matcher.Exists(nil)
		})
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
		doc := yamltest.FirstDocument(t, yamltest.Input(`
			kind: Deployment
			apiVersion: apps/v1
		`))

		got := m.Match(t.Context(), doc)
		assert.True(t, got)
	})

	t.Run("one field missing", func(t *testing.T) {
		t.Parallel()

		m := matcher.All(
			matcher.Exists(kindPath),
			matcher.Exists(apiVersionPath),
		)
		doc := yamltest.FirstDocument(t, yamltest.Input(`kind: Deployment`))

		got := m.Match(t.Context(), doc)
		assert.False(t, got)
	})

	t.Run("one field empty", func(t *testing.T) {
		t.Parallel()

		m := matcher.All(
			matcher.Exists(kindPath),
			matcher.Exists(apiVersionPath),
		)
		doc := yamltest.FirstDocument(t, yamltest.Input(`
			kind: Deployment
			apiVersion: ""
		`))

		got := m.Match(t.Context(), doc)
		assert.False(t, got)
	})
}
