package matcher_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"go.jacobcolvin.com/x/stringtest"

	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/schema/matcher"
)

func TestContent(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		matcher matcher.Matcher
		input   string
		want    bool
	}{
		"string match": {
			matcher: matcher.Content(kindPath, "Deployment"),
			input:   stringtest.Input(`kind: Deployment`),
			want:    true,
		},
		"string no match": {
			matcher: matcher.Content(kindPath, "Deployment"),
			input:   stringtest.Input(`kind: Service`),
			want:    false,
		},
		"missing field": {
			matcher: matcher.Content(missingPath, "value"),
			input:   stringtest.Input(`kind: Deployment`),
			want:    false,
		},
		"string matches number text": {
			matcher: matcher.Content(versionPath, "2"),
			input:   stringtest.Input(`version: 2`),
			want:    true,
		},
		"float matches unquoted float": {
			matcher: matcher.Content(versionPath, 1.0),
			input:   stringtest.Input(`version: 1.0`),
			want:    true,
		},
		"float matches integer spelling": {
			matcher: matcher.Content(versionPath, 1.0),
			input:   stringtest.Input(`version: 1`),
			want:    true,
		},
		"int no match": {
			matcher: matcher.Content(versionPath, 2),
			input:   stringtest.Input(`version: 3`),
			want:    false,
		},
		"bool match": {
			matcher: matcher.Content(enabledPath, true),
			input:   stringtest.Input(`enabled: true`),
			want:    true,
		},
		"bool does not match string": {
			matcher: matcher.Content(enabledPath, true),
			input:   stringtest.Input(`enabled: "true"`),
			want:    false,
		},
		"value that does not decode": {
			matcher: matcher.Content(versionPath, 1),
			input:   stringtest.Input(`version: abc`),
			want:    false,
		},
		"mapping does not decode into scalar": {
			matcher: matcher.Content(kindPath, "Deployment"),
			input: stringtest.Input(`
				kind:
				  name: Deployment
			`),
			want: false,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			doc := yamltest.FirstDocument(t, tc.input)

			got := tc.matcher.Match(t.Context(), doc)
			assert.Equal(t, tc.want, got)
		})
	}

	t.Run("nested path match", func(t *testing.T) {
		t.Parallel()

		m := matcher.Content(metadataName, "my-app")
		doc := yamltest.FirstDocument(t, stringtest.Input(`
			kind: Deployment
			metadata:
			  name: my-app
		`))

		got := m.Match(t.Context(), doc)
		assert.True(t, got)
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
		doc := yamltest.FirstDocument(t, stringtest.Input(`
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
		doc := yamltest.FirstDocument(t, stringtest.Input(`
			kind: Deployment
			apiVersion: v1
		`))

		got := m.Match(t.Context(), doc)
		assert.False(t, got)
	})
}
