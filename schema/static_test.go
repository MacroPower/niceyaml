package schema_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.jacobcolvin.com/x/stringtest"

	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/schema"
	"go.jacobcolvin.com/niceyaml/schema/matcher"
)

func TestStatic(t *testing.T) {
	t.Parallel()

	compiled := schema.MustCompile([]byte(`{
		"type": "object",
		"properties": {"kind": {"type": "string"}, "replicas": {"type": "integer"}}
	}`))

	t.Run("names the validator", func(t *testing.T) {
		t.Parallel()

		ref, err := schema.Static(compiled).Resolve(t.Context(), document(t))
		require.NoError(t, err)
		assert.Same(t, compiled, ref.Validator)
		assert.Nil(t, ref.Load)
		assert.Empty(t, ref.Key)
	})

	t.Run("registry uses the validator as it is", func(t *testing.T) {
		t.Parallel()

		reg := schema.NewRegistry()
		reg.Register(schema.When(matcher.Content(kindPath, "Deployment"), schema.Static(compiled)))

		doc := yamltest.FirstDocument(t, stringtest.Input(`kind: Deployment`))
		v, err := reg.Lookup(t.Context(), doc)
		require.NoError(t, err)
		assert.Same(t, compiled, v)

		require.NoError(t, reg.Validate(t.Context(), doc))

		bad := yamltest.FirstDocument(t, stringtest.Input(`
			kind: Deployment
			replicas: many
		`))
		err = reg.Validate(t.Context(), bad)
		require.Error(t, err)
		require.NotErrorIs(t, err, schema.ErrNoMatch)
	})

	t.Run("nil validator panics", func(t *testing.T) {
		t.Parallel()

		assert.PanicsWithValue(t, "schema.Static: validator is nil", func() {
			schema.Static(nil)
		})
	})
}
