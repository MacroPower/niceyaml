package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.jacobcolvin.com/niceyaml/schema"
)

func TestValidateFile(t *testing.T) {
	t.Parallel()

	// A schema every document of the fixture validates against.
	schemaData := []byte(`{
		"type": "object",
		"properties": {"name": {"type": "string"}},
		"required": ["name"]
	}`)

	tcs := map[string]struct {
		content string
		// Substrings the joined error names, or none when the file is valid.
		want []string
	}{
		"every document valid": {
			content: "name: a\n---\nname: b\n---\nname: c\n",
		},
		"first and last document invalid": {
			content: "value: 1\n---\nname: b\n---\nvalue: 3\n",
			want:    []string{"document 0", "document 2"},
		},
		"middle document invalid": {
			content: "name: a\n---\nvalue: 2\n---\nname: c\n",
			want:    []string{"document 1"},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			path := filepath.Join(t.TempDir(), "config.yaml")
			require.NoError(t, os.WriteFile(path, []byte(tc.content), 0o600))

			reg := schema.NewRegistry()
			reg.Register(schema.Embedded("go.jacobcolvin.com/niceyaml/cmd/nyaml/test.json", schemaData))

			err := validateFile(t.Context(), path, reg)
			if len(tc.want) == 0 {
				require.NoError(t, err)

				return
			}

			require.Error(t, err)

			for _, want := range tc.want {
				assert.Contains(t, err.Error(), want)
			}
		})
	}
}
