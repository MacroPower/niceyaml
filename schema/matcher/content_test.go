package matcher_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/schema/matcher"
)

func TestContent(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		value string
		input string
		want  bool
	}{
		"single condition match": {
			value: "Deployment",
			input: yamltest.Input(`kind: Deployment`),
			want:  true,
		},
		"single condition no match": {
			value: "Deployment",
			input: yamltest.Input(`kind: Service`),
			want:  false,
		},
		"missing field": {
			value: "value",
			input: yamltest.Input(`kind: Deployment`),
			want:  false,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			path := kindPath
			if !tc.want && tc.value == "value" {
				path = missingPath
			}

			m := matcher.Content(path, tc.value)
			doc := yamltest.FirstDocument(t, tc.input)

			got := m.Match(t.Context(), doc)
			assert.Equal(t, tc.want, got)
		})
	}

	t.Run("nested path match", func(t *testing.T) {
		t.Parallel()

		m := matcher.Content(metadataName, "my-app")
		doc := yamltest.FirstDocument(t, yamltest.Input(`
			kind: Deployment
			metadata:
			  name: my-app
		`))

		got := m.Match(t.Context(), doc)
		assert.True(t, got)
	})

	t.Run("nil path panics", func(t *testing.T) {
		t.Parallel()

		assert.PanicsWithValue(t, "matcher.Content: path is nil", func() {
			matcher.Content(nil, "value")
		})
	})
}

func TestContent_WithAll(t *testing.T) {
	t.Parallel()

	t.Run("multiple conditions all match", func(t *testing.T) {
		t.Parallel()

		m := matcher.All(
			matcher.Content(kindPath, "Deployment"),
			matcher.Content(apiVersionPath, "apps/v1"),
		)
		doc := yamltest.FirstDocument(t, yamltest.Input(`
			kind: Deployment
			apiVersion: apps/v1
		`))

		got := m.Match(t.Context(), doc)
		assert.True(t, got)
	})

	t.Run("multiple conditions partial match", func(t *testing.T) {
		t.Parallel()

		m := matcher.All(
			matcher.Content(kindPath, "Deployment"),
			matcher.Content(apiVersionPath, "apps/v1"),
		)
		doc := yamltest.FirstDocument(t, yamltest.Input(`
			kind: Deployment
			apiVersion: v1
		`))

		got := m.Match(t.Context(), doc)
		assert.False(t, got)
	})
}
