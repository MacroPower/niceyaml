package niceyaml_test

import (
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/macropower/niceyaml"
)

func TestNewDecoder(t *testing.T) {
	t.Parallel()

	r := strings.NewReader("key: value")
	dec := niceyaml.NewDecoder(r)
	require.NotNil(t, dec)
}

func TestDecoder_Decode(t *testing.T) {
	t.Parallel()

	t.Run("simple string", func(t *testing.T) {
		t.Parallel()

		r := strings.NewReader("hello")
		dec := niceyaml.NewDecoder(r)

		var got string

		err := dec.Decode(&got)
		require.NoError(t, err)
		assert.Equal(t, "hello", got)
	})

	t.Run("simple map", func(t *testing.T) {
		t.Parallel()

		r := strings.NewReader("key: value")
		dec := niceyaml.NewDecoder(r)

		var got map[string]any

		err := dec.Decode(&got)
		require.NoError(t, err)
		assert.Equal(t, map[string]any{"key": "value"}, got)
	})

	t.Run("nested struct", func(t *testing.T) {
		t.Parallel()

		type nested struct {
			Name  string `yaml:"name"`
			Value int    `yaml:"value"`
		}

		r := strings.NewReader("name: test\nvalue: 42")
		dec := niceyaml.NewDecoder(r)

		var got nested

		err := dec.Decode(&got)
		require.NoError(t, err)
		assert.Equal(t, nested{Name: "test", Value: 42}, got)
	})

	t.Run("slice", func(t *testing.T) {
		t.Parallel()

		r := strings.NewReader("- a\n- b\n- c")
		dec := niceyaml.NewDecoder(r)

		var got []string

		err := dec.Decode(&got)
		require.NoError(t, err)
		assert.Equal(t, []string{"a", "b", "c"}, got)
	})
}

func TestDecoder_Decode_EmptyInput(t *testing.T) {
	t.Parallel()

	r := strings.NewReader("")
	dec := niceyaml.NewDecoder(r)

	var v any

	err := dec.Decode(&v)
	assert.ErrorIs(t, err, io.EOF)
}

func TestDecoder_Decode_MultipleDocuments(t *testing.T) {
	t.Parallel()

	input := "doc1\n---\ndoc2\n---\ndoc3"
	r := strings.NewReader(input)
	dec := niceyaml.NewDecoder(r)

	var docs []string
	for {
		var v string

		err := dec.Decode(&v)
		if errors.Is(err, io.EOF) {
			break
		}

		require.NoError(t, err)

		docs = append(docs, v)
	}

	want := []string{"doc1", "doc2", "doc3"}
	assert.Equal(t, want, docs)
}

func TestDecoder_Decode_Error(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input        string
		into         func() any
		wantContains string
	}{
		"invalid yaml syntax": {
			input:        "key: [unclosed",
			into:         func() any { return &map[string]any{} },
			wantContains: "not found",
		},
		"type mismatch": {
			input:        "not_an_int",
			into:         func() any { var i int; return &i },
			wantContains: "cannot unmarshal",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			r := strings.NewReader(tc.input)
			dec := niceyaml.NewDecoder(r)

			v := tc.into()
			err := dec.Decode(v)
			require.Error(t, err)

			var yamlErr *niceyaml.Error
			require.ErrorAs(t, err, &yamlErr)
			assert.Contains(t, yamlErr.Error(), tc.wantContains)
		})
	}
}
