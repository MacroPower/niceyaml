package encoder_test

import (
	"bytes"
	"testing"

	"github.com/goccy/go-yaml"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.jacobcolvin.com/niceyaml/encoder"
)

func TestNew(t *testing.T) {
	t.Parallel()

	t.Run("creates encoder with no options", func(t *testing.T) {
		t.Parallel()

		var buf bytes.Buffer

		enc := encoder.New(&buf)
		require.NotNil(t, enc)
	})

	t.Run("creates encoder with Pretty", func(t *testing.T) {
		t.Parallel()

		var buf bytes.Buffer

		enc := encoder.New(&buf, encoder.Pretty()...)
		require.NotNil(t, enc)
	})
}

func TestEncoder_Encode(t *testing.T) {
	t.Parallel()

	type nested struct {
		Name  string `yaml:"name"`
		Value int    `yaml:"value"`
	}

	tcs := map[string]struct {
		input any
		want  string
	}{
		"simple string": {
			input: "hello",
			want:  "hello\n",
		},
		"simple map": {
			input: map[string]string{"key": "value"},
			want:  "key: value\n",
		},
		"nested struct": {
			input: nested{Name: "test", Value: 42},
			want:  "name: test\nvalue: 42\n",
		},
		"slice": {
			input: []string{"a", "b", "c"},
			want:  "- a\n- b\n- c\n",
		},
		"nil value": {
			input: nil,
			want:  "null\n",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var buf bytes.Buffer

			enc := encoder.New(&buf)

			err := enc.Encode(tc.input)
			require.NoError(t, err)

			got := buf.String()
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestEncoder_Close(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	enc := encoder.New(&buf)

	err := enc.Close()
	assert.NoError(t, err)
}

func TestPretty(t *testing.T) {
	t.Parallel()

	type config struct {
		Items []string `yaml:"items"`
	}

	input := config{
		Items: []string{"one", "two"},
	}

	var buf bytes.Buffer

	enc := encoder.New(&buf, encoder.Pretty()...)

	err := enc.Encode(input)
	require.NoError(t, err)

	got := buf.String()
	want := "items:\n  - one\n  - two\n"
	assert.Equal(t, want, got)
}

func TestWithYAMLEncodeOptions(t *testing.T) {
	t.Parallel()

	type config struct {
		Items []string `yaml:"items"`
	}

	var buf bytes.Buffer

	enc := encoder.New(&buf, encoder.WithYAMLEncodeOptions(yaml.Flow(true)))

	require.NoError(t, enc.Encode(config{Items: []string{"one", "two"}}))
	assert.Equal(t, "{items: [one, two]}\n", buf.String())
}
