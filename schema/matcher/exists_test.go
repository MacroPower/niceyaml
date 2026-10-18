package matcher_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.jacobcolvin.com/x/stringtest"

	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/paths"
	"go.jacobcolvin.com/niceyaml/schema/matcher"
)

func TestExists(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		path  paths.Path
		input string
		want  bool
	}{
		"field exists with value":   {path: kindPath, input: `kind: Deployment`, want: true},
		"field missing":             {path: kindPath, input: `apiVersion: v1`, want: false},
		"field empty unquoted":      {path: kindPath, input: `kind:`, want: true},
		"field null":                {path: kindPath, input: `kind: null`, want: true},
		"field empty double quoted": {path: kindPath, input: `kind: ""`, want: true},
		"field empty single quoted": {path: kindPath, input: `kind: ''`, want: true},
		"field with mapping value": {
			path: kindPath,
			input: stringtest.Input(`
				kind:
				  name: x
			`),
			want: true,
		},
		"document without content":    {path: kindPath, input: `# only a comment`, want: false},
		"field with whitespace value": {path: kindPath, input: `kind: " "`, want: true},
		"field with numeric value":    {path: kindPath, input: `kind: 123`, want: true},
		"field with boolean value":    {path: kindPath, input: `kind: true`, want: true},
		"field that holds an alias inside its own anchor": {
			// The decoder reads the alias as null, so kind reads "".
			path:  kindPath,
			input: `kind: &a !!str *a`,
			want:  true,
		},
		"nested path exists": {
			path: metadataName,
			input: stringtest.Input(`
				kind: Deployment
				metadata:
				  name: my-app
			`),
			want: true,
		},
		"nested path missing": {
			path: metadataName,
			input: stringtest.Input(`
				kind: Deployment
				metadata:
				  namespace: default
			`),
			want: false,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			doc := yamltest.FirstDocument(t, tc.input)

			assert.Equal(t, tc.want, match(t, matcher.Exists(tc.path), doc))
		})
	}

	t.Run("alias to a reference", func(t *testing.T) {
		t.Parallel()

		doc := yamltest.FirstDocument(t, `kind: *k`, yamltest.References(`k: &k {name: x}`))

		// The path selects the alias, but it does not enter it.
		assert.True(t, match(t, matcher.Exists(kindPath), doc))

		_, err := matcher.Exists(kindPath.Child("name")).Match(t.Context(), doc)
		require.ErrorIs(t, err, paths.ErrReferenceAlias)
	})

	testCannotDecide(t, matcher.Exists)
}

func TestExists_Describe(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		path paths.Path
		want string
	}{
		"path from the root":         {path: paths.Doc().Child("metadata"), want: "$.metadata exists"},
		"path from the current node": {path: kindPath, want: "@.kind exists"},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, description(t, matcher.Exists(tc.path)))
		})
	}
}
