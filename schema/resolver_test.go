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

// Compile-time interface satisfaction checks.
var (
	_ schema.Resolver = schema.ResolverFunc(nil)
	_ schema.Resolver = schema.Ref{}
)

func TestRef_Resolve(t *testing.T) {
	t.Parallel()

	t.Run("names itself for every document", func(t *testing.T) {
		t.Parallel()

		ref := schema.Loadable("config.json", func(_ context.Context) ([]byte, error) {
			return []byte(`{"type": "object"}`), nil
		})

		got, err := ref.Resolve(t.Context(), document(t))
		require.NoError(t, err)
		assert.Equal(t, ref.Key(), got.Key())

		data, err := got.Load(t.Context())
		require.NoError(t, err)
		assert.JSONEq(t, `{"type": "object"}`, string(data))
	})

	t.Run("zero ref names no schema", func(t *testing.T) {
		t.Parallel()

		got, err := schema.Ref{}.Resolve(t.Context(), document(t))
		require.NoError(t, err)
		assert.Empty(t, got.Key())
		assert.Nil(t, got.Schema())
	})
}

func TestResolverFunc(t *testing.T) {
	t.Parallel()

	kindPath := paths.Root().Child("kind")

	// A resolver that names a schema per kind and reports ErrNoMatch for
	// documents without one.
	r := schema.ResolverFunc(func(ctx context.Context, doc *niceyaml.Document) (schema.Ref, error) {
		kind, err := doc.Get[string](ctx, kindPath)
		if err != nil {
			return schema.Ref{}, schema.ErrNoMatch
		}

		return schema.Loadable(kind+".json", func(_ context.Context) ([]byte, error) {
			return []byte(`{"title": "` + kind + `"}`), nil
		}), nil
	})

	tcs := map[string]struct {
		input    string
		wantKey  string
		wantData string
		err      error
	}{
		"names the schema for a kind": {
			input:    `kind: Deployment`,
			wantKey:  "Deployment.json",
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
			assert.Equal(t, tc.wantKey, ref.Key())

			data, err := ref.Load(t.Context())
			require.NoError(t, err)
			assert.Equal(t, tc.wantData, string(data))
		})
	}
}

func TestCompiled(t *testing.T) {
	t.Parallel()

	t.Run("carries the schema", func(t *testing.T) {
		t.Parallel()

		compiled := schema.MustCompile([]byte(`{"type": "object"}`))
		ref := schema.Compiled(compiled)

		assert.Same(t, compiled, ref.Schema())
		assert.Empty(t, ref.Key())

		_, err := ref.Load(t.Context())
		require.ErrorIs(t, err, schema.ErrLoad)
	})

	t.Run("nil schema panics", func(t *testing.T) {
		t.Parallel()

		assert.PanicsWithValue(t, "schema.Compiled: schema is nil", func() {
			schema.Compiled(nil)
		})
	})
}

func TestLoadable(t *testing.T) {
	t.Parallel()

	t.Run("names the key and loads the bytes", func(t *testing.T) {
		t.Parallel()

		ref := schema.Loadable("config.json", func(_ context.Context) ([]byte, error) {
			return []byte(`{"type": "object"}`), nil
		})

		assert.Nil(t, ref.Schema())
		assert.Equal(t, "config.json", ref.Key())

		data, err := ref.Load(t.Context())
		require.NoError(t, err)
		assert.JSONEq(t, `{"type": "object"}`, string(data))
	})

	t.Run("empty key panics", func(t *testing.T) {
		t.Parallel()

		assert.PanicsWithValue(t, "schema.Loadable: key is empty", func() {
			schema.Loadable("", func(_ context.Context) ([]byte, error) { return nil, nil })
		})
	})

	t.Run("nil load panics", func(t *testing.T) {
		t.Parallel()

		assert.PanicsWithValue(t, "schema.Loadable: load is nil", func() {
			schema.Loadable("config.json", nil)
		})
	})

	t.Run("zero ref loads nothing", func(t *testing.T) {
		t.Parallel()

		var ref schema.Ref

		assert.Nil(t, ref.Schema())
		assert.Empty(t, ref.Key())

		_, err := ref.Load(t.Context())
		require.ErrorIs(t, err, schema.ErrLoad)
	})
}
