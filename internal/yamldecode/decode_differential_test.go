package yamldecode_test

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	up "github.com/goccy/go-yaml"
	yamltestsuite "github.com/goccy/go-yaml/testdata/yaml-test-suite"

	yaml "go.jacobcolvin.com/niceyaml/internal/yamldecode"
)

// decodeStream decodes every document of a stream into an any with decode.
// It returns the value of each document, and the text of the error or the
// panic that ended the stream.
func decodeStream(
	decode func(v any) error,
) (values []any, errText string) { //nolint:nonamedreturns // The recover sets them.
	defer func() {
		if p := recover(); p != nil {
			errText = fmt.Sprintf("panic: %v", p)
		}
	}()

	for len(values) <= 1000 {
		var v any

		err := decode(&v)
		if errors.Is(err, io.EOF) {
			return values, ""
		}

		if err != nil {
			return values, err.Error()
		}

		values = append(values, v)
	}

	return values, "more than 1000 documents"
}

// TestDecoderDecodeSuite compares the copy with go-yaml's decoder on every
// input of the YAML test suite. Each input decodes as a stream into an any,
// with plain maps and with ordered maps.
func TestDecoderDecodeSuite(t *testing.T) {
	t.Parallel()

	suite, err := yamltestsuite.TestSuites()
	require.NoError(t, err)
	require.NotEmpty(t, suite)

	modes := map[string]struct {
		upstream []up.DecodeOption
		copied   []yaml.DecodeOption
	}{
		"plain maps": {},
		"ordered maps": {
			upstream: []up.DecodeOption{up.UseOrderedMap()},
			copied:   []yaml.DecodeOption{yaml.UseOrderedMap()},
		},
	}

	for name, mode := range modes {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			for _, input := range suite {
				want, wantErr := decodeStream(up.NewDecoder(bytes.NewReader(input.InYAML), mode.upstream...).Decode)
				got, gotErr := decodeStream(yaml.NewDecoder(bytes.NewReader(input.InYAML), mode.copied...).Decode)
				assert.Equal(t, want, got, input.Name)
				assert.Equal(t, wantErr, gotErr, input.Name)
			}
		})
	}
}
