package loader_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.jacobcolvin.com/x/stringtest"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/paths"
	"go.jacobcolvin.com/niceyaml/schema/loader"
)

func TestCustom(t *testing.T) {
	t.Parallel()

	t.Run("success", func(t *testing.T) {
		t.Parallel()

		called := false
		schemaData := []byte(`{"type": "object"}`)
		kindPath := paths.Root().Child("kind").Path()

		l := loader.Custom(func(_ context.Context, doc *niceyaml.DocumentDecoder) ([]byte, string, error) {
			called = true
			kind, _ := doc.GetValue(kindPath)

			return schemaData, kind + ".json", nil
		})

		input := stringtest.Input(`kind: Deployment`)
		source := niceyaml.NewSourceFromString(input)
		decoder, err := source.Decoder()
		require.NoError(t, err)

		var doc *niceyaml.DocumentDecoder
		for _, d := range decoder.Documents() {
			doc = d

			break
		}

		result, err := l.Load(t.Context(), doc)
		require.NoError(t, err)
		assert.True(t, called)
		assert.Equal(t, schemaData, result.Data)
		assert.Equal(t, "Deployment.json", result.URL)
	})

	t.Run("error", func(t *testing.T) {
		t.Parallel()

		testErr := errors.New("custom error")
		l := loader.Custom(func(_ context.Context, _ *niceyaml.DocumentDecoder) ([]byte, string, error) {
			return nil, "", testErr
		})

		_, err := l.Load(t.Context(), nil)
		require.ErrorIs(t, err, testErr)
	})

	t.Run("empty data panics", func(t *testing.T) {
		t.Parallel()

		l := loader.Custom(func(_ context.Context, _ *niceyaml.DocumentDecoder) ([]byte, string, error) {
			return []byte{}, "empty.json", nil
		})

		assert.Panics(t, func() {
			//nolint:errcheck // Panic happens before return.
			l.Load(t.Context(), nil)
		})
	})
}
