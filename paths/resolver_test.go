package paths_test

import (
	"testing"

	"github.com/goccy/go-yaml/ast"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.jacobcolvin.com/x/stringtest"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/paths"
)

func TestResolver_Node(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input string
		path  string
		want  string
		err   error
	}{
		"plain child": {
			input: "a:\n  b: x\n",
			path:  "$.a.b",
			want:  "x",
		},
		"index": {
			input: "a: [x, y]\n",
			path:  "$.a[1]",
			want:  "y",
		},
		"alias": {
			input: "base: &b {k: x}\nref: *b\n",
			path:  "$.ref.k",
			want:  "x",
		},
		"key a merge brings in": {
			input: "base: &b {k: x}\nm:\n  <<: *b\n  own: 1\n",
			path:  "$.m.k",
			want:  "x",
		},
		"unknown alias": {
			input: "a: *nope\n",
			path:  "$.a",
			err:   paths.ErrAlias,
		},
		"wildcard": {
			input: "a: [x, y]\n",
			path:  "$.a[*]",
			err:   paths.ErrWildcard,
		},
		"missing path": {
			input: "a: x\n",
			path:  "$.b",
			err:   paths.ErrNotFound,
		},
		"nil document": {
			path: "$.a",
			err:  paths.ErrNoDocument,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			// A case with no input resolves in a nil document.
			var doc *ast.DocumentNode

			if tc.input != "" {
				file, err := niceyaml.NewSourceFromString(tc.input).File()
				require.NoError(t, err)

				doc = file.Docs[0]
			}

			node, err := paths.NewResolver(doc).Node(paths.MustParse(tc.path))
			if tc.err != nil {
				require.ErrorIs(t, err, tc.err)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, tc.want, node.String())
		})
	}
}

func TestResolver_Node_SeveralPaths(t *testing.T) {
	t.Parallel()

	// One Resolver resolves every path, including one it resolved before,
	// to the node and error that Path.Node finds on its own.
	source := niceyaml.NewSourceFromString(stringtest.Input(`
		base: &base
		  name: shared
		items: [a, b]
		spec:
		  name: x
		ref: *base
		mixed:
		  <<: *base
		  extra: 1
		bad: *nope
	`))
	file, err := source.File()
	require.NoError(t, err)

	doc := file.Docs[0]
	r := paths.NewResolver(doc)

	for _, expr := range []string{
		"$.spec.name",
		"$.items[1]",
		"$.ref.name",
		"$.mixed.name",
		"$.mixed.extra",
		"$.bad",
		"$.items[*]",
		"$.spec.missing",
		"$.spec.name",
	} {
		p := paths.MustParse(expr)

		want, wantErr := p.Node(doc)
		got, err := r.Node(p)

		if wantErr != nil {
			require.EqualError(t, err, wantErr.Error(), expr)

			continue
		}

		require.NoError(t, err, expr)
		assert.Same(t, want, got, expr)
	}
}
