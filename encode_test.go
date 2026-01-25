package niceyaml_test

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"jacobcolvin.com/niceyaml"
)

func TestNewEncoder(t *testing.T) {
	t.Parallel()

	t.Run("creates encoder with no options", func(t *testing.T) {
		t.Parallel()

		var buf bytes.Buffer

		enc := niceyaml.NewEncoder(&buf)
		require.NotNil(t, enc)
	})

	t.Run("creates encoder with PrettyEncoderOptions", func(t *testing.T) {
		t.Parallel()

		var buf bytes.Buffer

		enc := niceyaml.NewEncoder(&buf, niceyaml.PrettyEncoderOptions...)
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

			enc := niceyaml.NewEncoder(&buf)

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

	enc := niceyaml.NewEncoder(&buf)

	err := enc.Close()
	assert.NoError(t, err)
}

func TestPrettyEncoderOptions(t *testing.T) {
	t.Parallel()

	type config struct {
		Items []string `yaml:"items"`
	}

	input := config{
		Items: []string{"one", "two"},
	}

	var buf bytes.Buffer

	enc := niceyaml.NewEncoder(&buf, niceyaml.PrettyEncoderOptions...)

	err := enc.Encode(input)
	require.NoError(t, err)

	got := buf.String()
	want := "items:\n  - one\n  - two\n"
	assert.Equal(t, want, got)
}
