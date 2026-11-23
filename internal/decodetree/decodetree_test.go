package decodetree_test

import (
	"slices"
	"testing"

	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/token"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.jacobcolvin.com/x/stringtest"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/aliasing"
	"go.jacobcolvin.com/niceyaml/internal/astnode"
	"go.jacobcolvin.com/niceyaml/internal/decodetree"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
)

// newTree returns the tree of the one document of input, with the
// document and its facts.
func newTree(tb testing.TB, input string) (*decodetree.Tree, *niceyaml.Node, *aliasing.Facts) {
	tb.Helper()

	doc := yamltest.Document(tb, niceyaml.NewSourceFromString(input))
	facts := aliasing.NewFacts(doc.AST(), doc.DocumentResolver())

	return decodetree.New(doc.AST(), facts), doc, facts
}

// at returns the node of doc that steps reach from its root, a key for
// each string and an index for each int. Unlike a path, it stops at an
// alias rather than reading through it.
func at(tb testing.TB, doc *niceyaml.Node, steps ...any) ast.Node {
	tb.Helper()

	node := doc.AST()

	for _, step := range steps {
		switch s := step.(type) {
		case string:
			entries, ok := astnode.Entries(node)
			require.True(tb, ok, "no mapping for %q", s)

			i := slices.IndexFunc(entries, func(e *ast.MappingValueNode) bool { return e.Key.String() == s })
			require.GreaterOrEqual(tb, i, 0, "no key %q", s)

			node = entries[i].Value

		case int:
			seq, ok := astnode.Content(node).(*ast.SequenceNode)
			require.True(tb, ok, "no sequence for %d", s)

			node = seq.Values[s]
		}
	}

	return node
}

// aliasAt returns the alias of doc that steps reach, as [at] finds it.
func aliasAt(tb testing.TB, doc *niceyaml.Node, steps ...any) *ast.AliasNode {
	tb.Helper()

	alias, ok := at(tb, doc, steps...).(*ast.AliasNode)
	require.True(tb, ok, "not an alias")

	return alias
}

func TestChanges(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input      string
		want       bool
		references bool
	}{
		"a text without anchors": {
			input: stringtest.LinesLF("a: 1", "b: [2]"),
		},
		"anchors of unique names and their aliases": {
			input: stringtest.LinesLF("a: &x 1", "b: *x", "c: &y {d: *x}", "e: *y"),
		},
		"reused anchor names": {
			input: stringtest.LinesLF("a: &x 1", "b: *x", "c: &x 2", "d: *x"),
			want:  true,
		},
		"an alias before an anchor of its name": {
			input: stringtest.LinesLF("a: *x", "b: &x 1"),
			want:  true,
		},
		"an alias inside its anchor": {
			input: "a: &x [1, *x]\n",
			want:  true,
		},
		"an anchor with reference documents": {
			input:      "a: &x 1\n",
			references: true,
			want:       true,
		},
		"an integer tag": {
			input: "a: !!int 1\n",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, _, facts := newTree(t, tc.input)
			assert.Equal(t, tc.want, decodetree.Changes(facts, tc.references))
		})
	}
}

func TestTree_Resolve(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input string
		steps []any
		// The text of the node that the decoder reads for the alias at
		// steps, and whether the tree binds the alias.
		want  string
		bound bool
		// Whether the node is a null that holds the token of the alias.
		null bool
	}{
		"alias of an anchor before it": {
			input: stringtest.LinesLF("a: &x {b: 1}", "c: *x"),
			steps: []any{"c"},
			want:  "{b: 1}",
			bound: true,
		},
		"alias of a reused anchor name": {
			input: stringtest.LinesLF("a: &x 1", "b: *x", "c: &x 2", "d: *x"),
			steps: []any{"b"},
			want:  "1",
			bound: true,
		},
		"alias inside its anchor": {
			input: "a: &x [1, *x]\n",
			steps: []any{"a", 1},
			bound: true,
			null:  true,
		},
		"merge alias inside its anchor": {
			input: "a: &x {<<: *x}\n",
			steps: []any{"a", "<<"},
			want:  "{<<: *x}",
			bound: true,
		},
		"alias without an anchor": {
			input: "a: *r\n",
			steps: []any{"a"},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			tree, doc, _ := newTree(t, tc.input)
			alias := aliasAt(t, doc, tc.steps...)

			got, bound := tree.Resolve(alias)
			require.Equal(t, tc.bound, bound)

			switch {
			case tc.null:
				null, ok := got.(*ast.NullNode)
				require.True(t, ok, "not a null: %T", got)
				assert.Same(t, alias.Start, null.Token)

			case tc.want == "":
				assert.Nil(t, got)

			default:
				require.NotNil(t, got)
				assert.Equal(t, tc.want, got.String())
			}
		})
	}

	t.Run("alias outside the document", func(t *testing.T) {
		t.Parallel()

		tree, _, _ := newTree(t, "a: &x 1\n")
		_, other, _ := newTree(t, stringtest.LinesLF("a: &x 1", "b: *x"))

		_, bound := tree.Resolve(aliasAt(t, other, "b"))
		assert.False(t, bound)
	})
}

func TestTree_Anchor(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input string
		steps []any
		// The text of the anchor that the alias at steps reads. An empty
		// text means that the alias reads none.
		want string
	}{
		"alias of an anchor before it": {
			input: stringtest.LinesLF("a: &x {b: 1}", "c: *x"),
			steps: []any{"c"},
			want:  "&x {b: 1}",
		},
		"alias of a reused anchor name": {
			input: stringtest.LinesLF("a: &x 1", "b: *x", "c: &x 2", "d: *x"),
			steps: []any{"d"},
			want:  "&x 2",
		},
		"alias without an anchor": {
			input: "a: *r\n",
			steps: []any{"a"},
		},
		"alias inside its anchor": {
			input: "a: &x {<<: *x}\n",
			steps: []any{"a", "<<"},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			tree, doc, _ := newTree(t, tc.input)

			got, ok := tree.Anchor(aliasAt(t, doc, tc.steps...))
			if tc.want == "" {
				assert.False(t, ok)

				return
			}

			require.True(t, ok)
			assert.Equal(t, tc.want, got.String())
		})
	}
}

func TestTree_Holds(t *testing.T) {
	t.Parallel()

	tree, doc, _ := newTree(t, stringtest.LinesLF("a: 1", "b:"))

	// The parser makes a null for the value of b, which no lexer token
	// stands for.
	null := at(t, doc, "b")
	assert.True(t, tree.Holds(null.GetToken()))
	assert.False(t, tree.Holds(token.New("a", "a", &token.Position{})))
}

func TestTree_ReferenceAliases(t *testing.T) {
	t.Parallel()

	tree, doc, _ := newTree(t, stringtest.LinesLF("a: *r1", "b: &x {c: *r2}", "d: *x", "e: {<<: *r3}"))

	var got []string

	for _, alias := range tree.ReferenceAliases(at(t, doc, "d")) {
		got = append(got, alias.Token.Next.Value)
	}

	// The alias d reads the anchor x, which holds r2.
	assert.Equal(t, []string{"r2"}, got)

	all := tree.ReferenceAliases(doc.AST())
	require.Len(t, all, 3)

	// A merge key merges r3 alone.
	assert.Equal(t, []decodetree.ReferenceAlias{
		{Token: all[0].Token, Name: "r1"},
		{Token: all[1].Token, Name: "r2"},
		{Token: all[2].Token, Name: "r3", Merge: true},
	}, all)
}
