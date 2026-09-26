package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/schema"
)

// nameSchema is the schema the validateFile tests apply to every document
// of their fixtures. It requires a "name" key.
var nameSchema = []byte(`{
	"type": "object",
	"properties": {"name": {"type": "string"}},
	"required": ["name"]
}`)

func TestValidateFile(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		content string
		// Positions of the invalid documents, each as the "line:col:" that
		// follows the file path in the joined error, or none when the file
		// is valid.
		want []string
	}{
		"every document valid": {
			content: "name: a\n---\nname: b\n---\nname: c\n",
		},
		"first and last document invalid": {
			content: "value: 1\n---\nname: b\n---\nvalue: 3\n",
			want:    []string{"1:1:", "5:1:"},
		},
		"middle document invalid": {
			content: "name: a\n---\nvalue: 2\n---\nname: c\n",
			want:    []string{"3:1:"},
		},
		"comment above the first header": {
			content: "# yaml-language-server: $schema=./s.json\n---\nname: a\n",
		},
		"comment above an invalid document": {
			content: "# a preamble\n---\nvalue: 1\n",
			want:    []string{"3:1:"},
		},
		"invalid document after consecutive headers": {
			content: "name: a\n---\n---\nvalue: 1\n",
			want:    []string{"2:1:", "4:1:"},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			path := filepath.Join(t.TempDir(), "config.yaml")
			require.NoError(t, os.WriteFile(path, []byte(tc.content), 0o600))

			reg := schema.NewRegistry(schema.WithResolvers(schema.Embedded(nameSchema)))

			err := validateFile(t.Context(), path, reg)
			if len(tc.want) == 0 {
				require.NoError(t, err)

				return
			}

			require.Error(t, err)

			for _, want := range tc.want {
				assert.Contains(t, err.Error(), path+":"+want+" ")
			}
		})
	}
}

func TestValidateFileRoutesOnAbsolutePath(t *testing.T) {
	t.Parallel()

	// SchemaStore routes on patterns that name parent directories, so the
	// resolver must see the absolute path even when the user types a
	// relative one. Messages still name the file as the user typed it.
	path := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte("value: 1\n"), 0o600))

	wd, err := os.Getwd()
	require.NoError(t, err)

	rel, err := filepath.Rel(wd, path)
	require.NoError(t, err)
	require.False(t, filepath.IsAbs(rel))

	var got string

	reg := schema.NewRegistry(schema.WithResolvers(
		schema.ResolverFunc(func(_ context.Context, doc *niceyaml.Node) (schema.Ref, error) {
			got = doc.FilePath()

			return schema.Embedded(nameSchema), nil
		}),
	))

	err = validateFile(t.Context(), rel, reg)
	require.Error(t, err)
	assert.Equal(t, path, got)
	assert.Contains(t, err.Error(), rel+":1:1: ")
}

func TestValidateCmdOutput(t *testing.T) {
	t.Parallel()

	// The per-file line goes to the writer the caller set on the command,
	// so embedding the command and redirecting its output captures it.
	dir := t.TempDir()

	schemaPath := filepath.Join(dir, "schema.json")
	require.NoError(t, os.WriteFile(schemaPath, []byte(`{"type": "object"}`), 0o600))

	paths := []string{
		filepath.Join(dir, "a.yaml"),
		filepath.Join(dir, "b.yaml"),
	}
	for _, path := range paths {
		require.NoError(t, os.WriteFile(path, []byte("name: a\n"), 0o600))
	}

	out := &bytes.Buffer{}

	cmd := validateCmd()
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs(append([]string{"--schema", schemaPath}, paths...))

	require.NoError(t, cmd.Execute())
	assert.Equal(t, paths[0]+": valid\n"+paths[1]+": valid\n", out.String())
}
