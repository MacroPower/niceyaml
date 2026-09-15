package schema_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.jacobcolvin.com/x/stringtest"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/paths"
	"go.jacobcolvin.com/niceyaml/schema"
)

// Compile-time interface satisfaction check.
var _ schema.Resolver = schema.ResolverFunc(nil)

func TestResolverFunc(t *testing.T) {
	t.Parallel()

	kindPath := paths.Root().Child("kind")

	// A resolver that names a schema per kind and reports ErrNoMatch for
	// documents without one.
	r := schema.ResolverFunc(func(_ context.Context, doc *niceyaml.Document) (schema.Ref, error) {
		kind, err := doc.GetValue(kindPath)
		if err != nil {
			return schema.Ref{}, schema.ErrNoMatch
		}

		return schema.Ref{
			URL: kind + ".json",
			Load: func(_ context.Context) ([]byte, error) {
				return []byte(`{"title": "` + kind + `"}`), nil
			},
		}, nil
	})

	tcs := map[string]struct {
		input    string
		wantURL  string
		wantData string
		err      error
	}{
		"names the schema for a kind": {
			input:    `kind: Deployment`,
			wantURL:  "Deployment.json",
			wantData: `{"title": "Deployment"}`,
		},
		"reports no match without a kind": {
			input: `name: x`,
			err:   schema.ErrNoMatch,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			doc := yamltest.FirstDocument(t, stringtest.Input(tc.input))
			ref, err := r.Resolve(t.Context(), doc)

			if tc.err != nil {
				require.ErrorIs(t, err, tc.err)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, tc.wantURL, ref.URL)

			data, err := ref.Load(t.Context())
			require.NoError(t, err)
			assert.Equal(t, tc.wantData, string(data))
		})
	}
}
