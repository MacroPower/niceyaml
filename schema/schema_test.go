package schema_test

import (
	"math"
	"testing"

	"github.com/invopop/jsonschema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/macropower/niceyaml/schema"
)

// newTestSchema creates a test schema with "name" and "age" properties.
func newTestSchema() *jsonschema.Schema {
	props := jsonschema.NewProperties()
	props.Set("name", &jsonschema.Schema{Type: "string"})
	props.Set("age", &jsonschema.Schema{Type: "integer"})

	return &jsonschema.Schema{
		Type:       "object",
		Properties: props,
	}
}

func TestGetProperty(t *testing.T) {
	t.Parallel()

	js := newTestSchema()

	tcs := map[string]struct {
		input   string
		wantErr bool
	}{
		"property exists": {
			input:   "name",
			wantErr: false,
		},
		"another property exists": {
			input:   "age",
			wantErr: false,
		},
		"property not found": {
			input:   "missing",
			wantErr: true,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, err := schema.GetProperty(tc.input, js)

			if tc.wantErr {
				require.Error(t, err)
				assert.Nil(t, got)
				assert.Contains(t, err.Error(), tc.input)

				return
			}

			require.NoError(t, err)
			assert.NotNil(t, got)
		})
	}
}

func TestMustGetProperty(t *testing.T) {
	t.Parallel()

	js := newTestSchema()

	tcs := map[string]struct {
		input       string
		shouldPanic bool
	}{
		"property exists": {
			input:       "name",
			shouldPanic: false,
		},
		"property not found": {
			input:       "missing",
			shouldPanic: true,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if tc.shouldPanic {
				assert.Panics(t, func() {
					schema.MustGetProperty(tc.input, js)
				})

				return
			}

			got := schema.MustGetProperty(tc.input, js)
			assert.NotNil(t, got)
		})
	}
}

func TestPtrUint64(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input uint64
		want  uint64
	}{
		"zero value": {
			input: 0,
			want:  0,
		},
		"positive value": {
			input: 42,
			want:  42,
		},
		"max value": {
			input: math.MaxUint64,
			want:  math.MaxUint64,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := schema.PtrUint64(tc.input)

			require.NotNil(t, got)
			assert.Equal(t, tc.want, *got)
		})
	}
}
