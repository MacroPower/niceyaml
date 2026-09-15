package registry_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.jacobcolvin.com/x/stringtest"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/schema"
	"go.jacobcolvin.com/niceyaml/schema/loader"
	"go.jacobcolvin.com/niceyaml/schema/matcher"
	"go.jacobcolvin.com/niceyaml/schema/registry"
)

func TestWhen(t *testing.T) {
	t.Parallel()

	schemaData := []byte(`{"type": "object"}`)

	tcs := map[string]struct {
		input    string
		wantURL  string
		wantErr  error
		wantLoad bool
	}{
		"matcher accepts": {
			input:    `kind: Deployment`,
			wantURL:  "deployment.json",
			wantLoad: true,
		},
		"matcher rejects": {
			input:   `kind: Service`,
			wantErr: schema.ErrNoMatch,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			r := registry.When(
				matcher.Content(kindPath, "Deployment"),
				loader.Embedded("deployment.json", schemaData),
			)

			doc := yamltest.FirstDocument(t, stringtest.Input(tc.input))
			ref, err := r.Resolve(t.Context(), doc)

			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, tc.wantURL, ref.URL)

			data, err := ref.Load(t.Context())
			require.NoError(t, err)
			assert.Equal(t, schemaData, data)
		})
	}

	t.Run("guarded resolver errors pass through", func(t *testing.T) {
		t.Parallel()

		inner := errors.New("inner")
		r := registry.When(
			matcher.Content(kindPath, "Deployment"),
			schema.ResolverFunc(func(_ context.Context, _ *niceyaml.Document) (schema.Ref, error) {
				return schema.Ref{}, inner
			}),
		)

		doc := yamltest.FirstDocument(t, stringtest.Input(`kind: Deployment`))
		_, err := r.Resolve(t.Context(), doc)
		require.ErrorIs(t, err, inner)
	})

	t.Run("guarded resolver is not consulted on reject", func(t *testing.T) {
		t.Parallel()

		called := false
		r := registry.When(
			matcher.Content(kindPath, "Deployment"),
			schema.ResolverFunc(func(_ context.Context, _ *niceyaml.Document) (schema.Ref, error) {
				called = true

				return schema.Ref{}, nil
			}),
		)

		doc := yamltest.FirstDocument(t, stringtest.Input(`kind: Service`))
		_, err := r.Resolve(t.Context(), doc)
		require.ErrorIs(t, err, schema.ErrNoMatch)
		assert.False(t, called)
	})

	t.Run("nil matcher panics", func(t *testing.T) {
		t.Parallel()

		assert.PanicsWithValue(t, "registry.When: matcher is nil", func() {
			registry.When(nil, loader.Embedded("x.json", schemaData))
		})
	})

	t.Run("nil resolver panics", func(t *testing.T) {
		t.Parallel()

		assert.PanicsWithValue(t, "registry.When: resolver is nil", func() {
			registry.When(matcher.Content(kindPath, "x"), nil)
		})
	})
}
