package matcher_test

import (
	"context"
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

	t.Run("alias without an anchor is an error", func(t *testing.T) {
		t.Parallel()

		doc := yamltest.FirstDocument(t, `metadata: *missing`)

		_, err := matcher.Exists(metadataName).Match(t.Context(), doc)
		require.ErrorIs(t, err, paths.ErrAlias)
	})

	t.Run("wildcard path is an error", func(t *testing.T) {
		t.Parallel()

		doc := yamltest.FirstDocument(t, stringtest.Input(`
			jobs:
			  build:
			    steps:
			      - uses: checkout
		`))

		for _, expr := range []string{"$.jobs.*", "$.jobs.*.steps", "$.jobs.build.steps[*]", "$..uses", "$..*"} {
			_, err := matcher.Exists(paths.MustParse(expr)).Match(t.Context(), doc)
			require.ErrorIs(t, err, paths.ErrWildcard, expr)
		}
	})
}

func TestExists_ContextEnded(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	doc := yamltest.FirstDocument(t, `kind: Deployment`)

	// A matcher whose context ended cannot decide, so it returns the error
	// rather than a match, and the registry stops at the document.
	ok, err := matcher.Exists(kindPath).Match(ctx, doc)
	require.ErrorIs(t, err, context.Canceled)
	assert.False(t, ok)
}
