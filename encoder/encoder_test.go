package encoder_test

import (
	"bytes"
	"context"
	"errors"
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

			err := enc.Encode(t.Context(), tc.input)
			require.NoError(t, err)

			got := buf.String()
			assert.Equal(t, tc.want, got)
		})
	}
}

// policyKey is the context key that [policyMarshaler] reads.
type policyKey struct{}

// policyMarshaler encodes as the policy its context holds, or as "none".
type policyMarshaler struct{}

func (policyMarshaler) MarshalYAML(ctx context.Context) (any, error) {
	policy, ok := ctx.Value(policyKey{}).(string)
	if !ok {
		policy = "none"
	}

	return policy, nil
}

func TestEncoder_Encode_context(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	enc := encoder.New(&buf)
	ctx := context.WithValue(t.Context(), policyKey{}, "redact")

	require.NoError(t, enc.Encode(ctx, map[string]policyMarshaler{"policy": {}}))
	assert.Equal(t, "policy: redact\n", buf.String())
}

func TestEncoder_Close(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	enc := encoder.New(&buf)

	err := enc.Close()
	assert.NoError(t, err)
}

// failWriter refuses every write with errWrite.
type failWriter struct{}

var errWrite = errors.New("disk full")

func (failWriter) Write([]byte) (int, error) {
	return 0, errWrite
}

// countingMarshaler counts the calls to its MarshalYAML method.
type countingMarshaler struct {
	calls *int
}

func (m countingMarshaler) MarshalYAML() (any, error) {
	*m.calls++

	return "x", nil
}

func TestEncoder_Encode_writeError(t *testing.T) {
	t.Parallel()

	enc := encoder.New(failWriter{})

	err := enc.Encode(t.Context(), map[string]int{"a": 1})
	require.ErrorIs(t, err, errWrite)

	err = enc.Encode(t.Context(), map[string]int{"b": 2})
	require.ErrorIs(t, err, errWrite, "a later Encode reports the same error")

	err = enc.Encode(t.Context(), make(chan int))
	require.ErrorIs(t, err, errWrite, "a value go-yaml cannot encode reports the write error")

	calls := 0
	err = enc.Encode(t.Context(), countingMarshaler{calls: &calls})
	require.ErrorIs(t, err, errWrite)
	assert.Zero(t, calls, "Encode runs no marshaler after a refused write")

	err = enc.Close()
	require.ErrorIs(t, err, errWrite, "Close reports the write error")
}

func TestWithIndent_panicsBelowOne(t *testing.T) {
	t.Parallel()

	for _, spaces := range []int{0, -1} {
		assert.Panics(t, func() { encoder.WithIndent(spaces) }, "spaces=%d", spaces)
	}
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

	err := enc.Encode(t.Context(), input)
	require.NoError(t, err)

	got := buf.String()
	want := "items:\n  - one\n  - two\n"
	assert.Equal(t, want, got)
}

func TestWithYAMLOptions(t *testing.T) {
	t.Parallel()

	type config struct {
		Items []string `yaml:"items"`
	}

	var buf bytes.Buffer

	enc := encoder.New(&buf, encoder.WithYAMLOptions(yaml.Flow(true)))

	require.NoError(t, enc.Encode(t.Context(), config{Items: []string{"one", "two"}}))
	assert.Equal(t, "{items: [one, two]}\n", buf.String())
}
