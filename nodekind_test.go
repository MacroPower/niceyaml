package niceyaml_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.jacobcolvin.com/x/stringtest"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/paths"
)

func TestNode_Kind(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input string
		path  string
		want  niceyaml.NodeKind
	}{
		"block mapping": {
			input: "a: 1\n",
			want:  niceyaml.NodeMapping,
		},
		"flow mapping": {
			input: "{a: 1}\n",
			want:  niceyaml.NodeMapping,
		},
		"empty mapping": {
			input: "{}\n",
			want:  niceyaml.NodeMapping,
		},
		"anchored mapping": {
			input: "&r {a: 1}\n",
			want:  niceyaml.NodeMapping,
		},
		"tagged mapping": {
			input: "!!map {a: 1}\n",
			want:  niceyaml.NodeMapping,
		},
		"anchored and tagged mapping": {
			input: "&r !!map\na: 1\n",
			want:  niceyaml.NodeMapping,
		},
		"block sequence": {
			input: "- a\n- b\n",
			want:  niceyaml.NodeSequence,
		},
		"flow sequence": {
			input: "[a, b]\n",
			want:  niceyaml.NodeSequence,
		},
		"empty sequence": {
			input: "[]\n",
			want:  niceyaml.NodeSequence,
		},
		"tagged sequence": {
			input: "&s !!seq [a]\n",
			want:  niceyaml.NodeSequence,
		},
		"scalar": {
			input: "hello\n",
			want:  niceyaml.NodeScalar,
		},
		"null scalar": {
			input: "~\n",
			want:  niceyaml.NodeScalar,
		},
		"tagged scalar": {
			input: "!!str 1\n",
			want:  niceyaml.NodeScalar,
		},
		"block scalar": {
			input: "|\n  text\n",
			want:  niceyaml.NodeScalar,
		},
		"empty source": {
			input: "",
			want:  niceyaml.NodeNone,
		},
		"comments alone": {
			input: "# only a comment\n",
			want:  niceyaml.NodeNone,
		},
		"whitespace alone": {
			input: "   \n",
			want:  niceyaml.NodeNone,
		},
		"header alone": {
			input: "---\n",
			want:  niceyaml.NodeNone,
		},
		"directive alone": {
			input: "%YAML 1.2\n---\n",
			want:  niceyaml.NodeNone,
		},
		"alias with no anchor": {
			input: "!!str *x\n",
			want:  niceyaml.NodeNone,
		},
		"value mapping": {
			input: "a:\n  b: 1\n",
			path:  "$.a",
			want:  niceyaml.NodeMapping,
		},
		"value sequence": {
			input: "a: [1]\n",
			path:  "$.a",
			want:  niceyaml.NodeSequence,
		},
		"value scalar": {
			input: "a: 1\n",
			path:  "$.a",
			want:  niceyaml.NodeScalar,
		},
		"value left out": {
			input: "a:\n",
			path:  "$.a",
			want:  niceyaml.NodeScalar,
		},
		"value tagged as a string": {
			input: "a: !!str 1\n",
			path:  "$.a",
			want:  niceyaml.NodeScalar,
		},
		"alias to a sequence": {
			input: stringtest.JoinLF(
				"a: &x [1]",
				"b: *x",
			),
			path: "$.b",
			want: niceyaml.NodeSequence,
		},
		"tagged alias to a mapping": {
			input: stringtest.JoinLF(
				"a: &x {k: 1}",
				"b: !custom *x",
			),
			path: "$.b",
			want: niceyaml.NodeMapping,
		},
		"tagged alias to a scalar": {
			input: stringtest.JoinLF(
				"a: &x 1",
				"b: !!str *x",
			),
			path: "$.b",
			want: niceyaml.NodeScalar,
		},
		"anchor on a tagged mapping, through an alias": {
			input: stringtest.JoinLF(
				"a: &x !!map {k: 1}",
				"b: *x",
			),
			path: "$.b",
			want: niceyaml.NodeMapping,
		},
		"key": {
			input: "a: 1\n",
			path:  "$.a~",
			want:  niceyaml.NodeScalar,
		},
		"explicit key": {
			input: "? a\n: [1]\n",
			path:  "$.a~",
			want:  niceyaml.NodeScalar,
		},
		"entry through a merge key": {
			input: stringtest.JoinLF(
				"base: &base {k: [1]}",
				"a:",
				"  <<: *base",
			),
			path: "$.a.k",
			want: niceyaml.NodeSequence,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			n := yamltest.FirstDocument(t, tc.input)
			if tc.path != "" {
				n = yamltest.At(t, n, paths.MustParse(tc.path))
			}

			assert.Equal(t, tc.want, n.Kind())
		})
	}

	t.Run("nil node", func(t *testing.T) {
		t.Parallel()

		var n *niceyaml.Node

		assert.Equal(t, niceyaml.NodeNone, n.Kind())
	})

	t.Run("document that did not parse", func(t *testing.T) {
		t.Parallel()

		docs, err := niceyaml.NewSourceFromString("a: [\n").Documents()
		require.Error(t, err)
		require.Len(t, docs, 1)

		assert.Equal(t, niceyaml.NodeNone, docs[0].Kind())
		require.ErrorIs(t, docs[0].Err(), niceyaml.ErrSyntax)
	})

	t.Run("each document", func(t *testing.T) {
		t.Parallel()

		docs, err := niceyaml.NewSourceFromString("a: 1\n---\n- b\n---\nc\n---\n").Documents()
		require.NoError(t, err)

		got := make([]niceyaml.NodeKind, 0, len(docs))
		for _, doc := range docs {
			got = append(got, doc.Kind())
		}

		assert.Equal(t, []niceyaml.NodeKind{
			niceyaml.NodeMapping,
			niceyaml.NodeSequence,
			niceyaml.NodeScalar,
			niceyaml.NodeNone,
		}, got)
	})

	t.Run("each match", func(t *testing.T) {
		t.Parallel()

		doc := yamltest.FirstDocument(t, stringtest.JoinLF(
			"items:",
			"  - {a: 1}",
			"  - [1]",
			"  - x",
			"  -",
			"  - &n !!map {b: 2}",
			"  - *n",
		))

		items, err := doc.Nodes(paths.Current().Child("items").IndexAll())
		require.NoError(t, err)

		got := make([]niceyaml.NodeKind, 0, len(items))
		for _, item := range items {
			got = append(got, item.Kind())
		}

		assert.Equal(t, []niceyaml.NodeKind{
			niceyaml.NodeMapping,
			niceyaml.NodeSequence,
			niceyaml.NodeScalar,
			niceyaml.NodeScalar,
			niceyaml.NodeMapping,
			niceyaml.NodeMapping,
		}, got)
	})
}
