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

func TestResolver_MergeSources(t *testing.T) {
	t.Parallel()

	// Each case reads the merge sources of the mapping at $.m, and names
	// each source by its first key.
	tcs := map[string]struct {
		input string
		opts  []niceyaml.SourceOption
		want  []string
		err   error
	}{
		"one mapping": {
			input: "a: &a {x: 1}\nm: {<<: *a, own: 1}\n",
			want:  []string{"x"},
		},
		"sequence of mappings": {
			input: "a: &a {x: 1}\nb: &b {y: 2}\nm: {<<: [*a, *b]}\n",
			want:  []string{"x", "y"},
		},
		"alias to a sequence": {
			input: "a: &a {x: 1}\nb: &b {y: 2}\nl: &l [*a, *b]\nm: {<<: *l}\n",
			want:  []string{"x", "y"},
		},
		"repeated merge keys": {
			input: "a: &a {x: 1}\nb: &b {y: 2}\nm: {<<: *b, own: 1, <<: *a}\n",
			opts:  []niceyaml.SourceOption{niceyaml.WithAllowDuplicateKeys(true)},
			want:  []string{"y", "x"},
		},
		"no merge key": {
			input: "m: {k: x}\n",
		},
		"not a mapping": {
			input: "m: [x, y]\n",
		},
		"unknown alias": {
			input: "m: {<<: *nope}\n",
			err:   paths.ErrAlias,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			file, err := niceyaml.NewSourceFromString(tc.input, tc.opts...).File()
			require.NoError(t, err)

			r := paths.NewResolver(file.Docs[0])

			node, err := r.Node(paths.MustParse("$.m"))
			require.NoError(t, err)

			sources, err := r.MergeSources(node)
			if tc.err != nil {
				require.ErrorIs(t, err, tc.err)

				return
			}

			require.NoError(t, err)

			var got []string

			for _, src := range sources {
				mapping, ok := src.(*ast.MappingNode)
				require.True(t, ok, "source is a %T", src)
				require.NotEmpty(t, mapping.Values)

				got = append(got, mapping.Values[0].Key.GetToken().Value)
			}

			assert.Equal(t, tc.want, got)
		})
	}
}
