package niceyaml_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/macropower/niceyaml"
)

func TestNewPath(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		want  string
		input []string
	}{
		"empty path": {
			input: nil,
			want:  "$",
		},
		"single child": {
			input: []string{"kind"},
			want:  "$.kind",
		},
		"multiple children": {
			input: []string{"metadata", "name"},
			want:  "$.metadata.name",
		},
		"deeply nested path": {
			input: []string{"spec", "template", "spec", "containers"},
			want:  "$.spec.template.spec.containers",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			path := niceyaml.NewPath(tc.input...)

			require.NotNil(t, path)
			assert.Equal(t, tc.want, path.String())
		})
	}
}

func TestNewPathBuilder(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		want  string
		input []string
	}{
		"returns root path builder": {
			input: nil,
			want:  "$",
		},
		"can chain children": {
			input: []string{"metadata", "labels"},
			want:  "$.metadata.labels",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			pb := niceyaml.NewPathBuilder()
			require.NotNil(t, pb)

			for _, child := range tc.input {
				pb = pb.Child(child)
			}

			path := pb.Build()
			assert.Equal(t, tc.want, path.String())
		})
	}
}
