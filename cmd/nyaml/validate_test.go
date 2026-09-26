package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
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
	tcs := map[string]struct {
		// Names of the valid files to write to the test directory.
		files []string
		// Arguments after the schema flag, relative to the test directory.
		args []string
		// Names that the "valid" lines report in order, relative to the
		// test directory.
		want []string
	}{
		"explicit names": {
			files: []string{"a.yaml", "b.yaml"},
			args:  []string{"a.yaml", "b.yaml"},
			want:  []string{"a.yaml", "b.yaml"},
		},
		"control characters in a matched name": {
			files: []string{"\x1b]52;c;eA==\a.yaml"},
			args:  []string{"*.yaml"},
			// The Control Pictures for ESC and BEL stand in for them.
			want: []string{"␛]52;c;eA==␇.yaml"},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()

			schemaPath := filepath.Join(dir, "schema.json")
			require.NoError(t, os.WriteFile(schemaPath, []byte(`{"type": "object"}`), 0o600))

			for _, file := range tc.files {
				err := os.WriteFile(filepath.Join(dir, file), []byte("name: a\n"), 0o600)
				if err != nil {
					// Some file systems, such as those on Windows, reject
					// control characters in a name.
					t.Skipf("write %q: %v", file, err)
				}
			}

			args := []string{"--schema", schemaPath}
			for _, arg := range tc.args {
				args = append(args, filepath.Join(dir, arg))
			}

			var want strings.Builder

			for _, w := range tc.want {
				want.WriteString(filepath.Join(dir, w) + ": valid\n")
			}

			out := &bytes.Buffer{}

			cmd := validateCmd()
			cmd.SetOut(out)
			cmd.SetErr(out)
			cmd.SetArgs(args)

			require.NoError(t, cmd.Execute())
			assert.Equal(t, want.String(), out.String())
			assert.NotContains(t, out.String(), "\x1b")
		})
	}
}
