package decodetree_test

import (
	"errors"
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
	"go.jacobcolvin.com/niceyaml/internal/docparse"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
)

// newTree returns the tree of the one document of input, with the
// document. Its second parse holds a null in the token of each alias the
// facts hold as enclosed, as the Source writes it.
func newTree(tb testing.TB, input string) (*decodetree.Tree, *niceyaml.Node) {
	tb.Helper()

	src := niceyaml.NewSourceFromString(input)
	doc := yamltest.Document(tb, src)
	facts := aliasing.NewFacts(doc.AST(), doc.Resolver())

	reparse := func() (ast.Node, map[*token.Token]int) {
		r := docparse.Parse(src.Tokens())

		for alias := range facts.Enclosed {
			for _, tk := range r.Tokens {
				if tk.Type == token.AliasType && tk.Position.Offset == alias.Start.Position.Offset {
					decodetree.WriteNull(tk)
				}
			}
		}

		return r.File.Docs[0].Body, r.Index
	}

	return decodetree.New(doc.AST(), facts, decodetree.Options{
		Reparse: reparse,
		Texts:   func() []string { return []string{input} },
	}), doc
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

func TestNew(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input string
		steps []any
		// The text of the view of the node at steps, and whether the tree
		// reads a second parse.
		want   string
		parsed bool
	}{
		"an alias with a comment": {
			input: stringtest.LinesLF("a: &x 1", "b: *x # note"),
			steps: []any{"b"},
			want:  "*x",
		},
		"reused anchor names": {
			input:  stringtest.LinesLF("a: &x 1", "b: *x", "c: &x 2", "d: *x"),
			steps:  []any{"d"},
			want:   "*x [2]",
			parsed: true,
		},
		"an alias inside its anchor": {
			input:  "a: &x [1, *x]\n",
			steps:  []any{"a", 1},
			want:   "null",
			parsed: true,
		},
		"an integer tag": {
			input:  "a: !!int 1\n",
			steps:  []any{"a"},
			want:   "!<tag:yaml.org,2002:int> 1",
			parsed: true,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			tree, doc := newTree(t, tc.input)
			node := at(t, doc, tc.steps...)

			view := tree.View(node)
			assert.Equal(t, tc.want, view.String())
			assert.Equal(t, tc.parsed, tree.Parsed(node) != node)
			assert.True(t, tree.Holds(view.GetToken()))
		})
	}
}

func TestChanges(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input string
		want  bool
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
		"an alias inside its anchor": {
			input: "a: &x [1, *x]\n",
			want:  true,
		},
		"an integer tag": {
			input: "a: !!int 1\n",
			want:  true,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			tree, doc := newTree(t, tc.input)
			facts := aliasing.NewFacts(doc.AST(), doc.Resolver())

			got := decodetree.Changes(doc.AST(), facts, false)
			assert.Equal(t, tc.want, got)
			assert.Equal(t, tree.Changed(), got)
		})
	}
}

func TestTree_Holds(t *testing.T) {
	t.Parallel()

	tree, doc := newTree(t, stringtest.LinesLF("a: 1", "b:"))

	// The parser makes a null for the value of b, which no lexer token
	// stands for.
	null := at(t, doc, "b")
	assert.True(t, tree.Holds(null.GetToken()))
	assert.False(t, tree.Holds(token.New("a", "a", &token.Position{})))
}

func TestTree_RestoreNames(t *testing.T) {
	t.Parallel()

	tree, _ := newTree(t, stringtest.LinesLF("a: &x 1", "b: &x 2"))
	assert.Equal(t, "anchor x and x", tree.RestoreNames("anchor x [1] and x [2]"))

	plain, _ := newTree(t, "a: &x 1\n")
	assert.Equal(t, "anchor x [1]", plain.RestoreNames("anchor x [1]"))

	var none *decodetree.Tree

	assert.Equal(t, "anchor x [1]", none.RestoreNames("anchor x [1]"))
}

func TestTree_NewNames(t *testing.T) {
	t.Parallel()

	// The document spells "x [1]", so the first anchor named x skips it.
	tree, doc := newTree(t, stringtest.LinesLF(`k: "x [1]"`, "a: &x 1", "b: &x 2", "c: *x"))
	assert.Equal(t, "*x [3]", tree.View(at(t, doc, "c")).String())
}

func TestTree_Prime(t *testing.T) {
	t.Parallel()

	tree, doc := newTree(t, stringtest.LinesLF("a: &x 1", "b: &y {c: *x}", "z: &z 3", "d: [*y]"))

	var names []string

	for _, anchor := range tree.Prime(tree.View(at(t, doc, "d"))) {
		names = append(names, anchor.Name.String())
	}

	// The anchor z registers nothing the node reads.
	assert.Equal(t, []string{"x", "y"}, names)
	assert.Empty(t, tree.Prime(tree.View(at(t, doc, "z"))))
}

func TestTree_UnresolvedMerge(t *testing.T) {
	t.Parallel()

	tree, doc := newTree(t, stringtest.LinesLF("a: 1", "b: {<<: *missing}"))
	scope := tree.View(at(t, doc, "b"))

	tk, msg, ok := tree.UnresolvedMerge(scope, errors.New("cannot find anchor by alias name missing"))
	require.True(t, ok)
	assert.Equal(t, "cannot find anchor by alias name missing", msg)
	assert.Equal(t, 1, tk.Position.Line-1)

	_, _, ok = tree.UnresolvedMerge(scope, errors.New("other"))
	assert.False(t, ok)
}

func TestTree_ReferenceAliases(t *testing.T) {
	t.Parallel()

	tree, doc := newTree(t, stringtest.LinesLF("a: *r1", "b: &x {c: *r2}", "d: *x", "e: {<<: *r3}"))

	var got []string

	for _, alias := range tree.ReferenceAliases(tree.View(at(t, doc, "d"))) {
		got = append(got, alias.Token.Next.Value)
	}

	// The alias d reads the anchor x, which holds r2.
	assert.Equal(t, []string{"r2"}, got)

	all := tree.ReferenceAliases(tree.View(doc.AST()))
	require.Len(t, all, 3)

	// A merge key merges r3 alone.
	assert.Equal(t, []decodetree.ReferenceAlias{
		{Token: all[0].Token, Name: "r1"},
		{Token: all[1].Token, Name: "r2"},
		{Token: all[2].Token, Name: "r3", Merge: true},
	}, all)
}

func TestWriteNull(t *testing.T) {
	t.Parallel()

	tks := niceyaml.NewSourceFromString("a: *x\n").Tokens()

	i := slices.IndexFunc(tks, func(tk *token.Token) bool { return tk.Type == token.AliasType })
	require.GreaterOrEqual(t, i, 0, "no alias token")

	alias := tks[i]
	prev, next := alias.Prev, alias.Next

	decodetree.WriteNull(alias)
	assert.Equal(t, token.NullType, alias.Type)
	assert.Equal(t, " null", alias.Origin)
	assert.Same(t, prev, alias.Prev)
	assert.Same(t, next, alias.Next)
}

func TestTree_Anchor(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input string
		steps []any
		// The text of the anchor of the tree that the alias at steps
		// reads, and the name that the tree gives it. An empty text means
		// that the alias reads none.
		want, name string
	}{
		"alias of an anchor before it": {
			input: stringtest.LinesLF("a: &x {b: 1}", "c: *x"),
			steps: []any{"c"},
			want:  "&x {b: 1}",
			name:  "x",
		},
		"alias of a renamed anchor": {
			// The text of the anchor spells the name of the document.
			input: stringtest.LinesLF("a: &x 1", "b: *x", "c: &x 2", "d: *x"),
			steps: []any{"d"},
			want:  "&x 2",
			name:  "x [2]",
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

			tree, doc := newTree(t, tc.input)

			alias, ok := tree.View(at(t, doc, tc.steps...)).(*ast.AliasNode)
			require.True(t, ok, "not an alias")

			got, ok := tree.Anchor(alias)
			if tc.want == "" {
				assert.False(t, ok)

				return
			}

			require.True(t, ok)
			assert.Equal(t, tc.want, got.String())

			name, ok := astnode.Name(got.Name)
			require.True(t, ok)
			assert.Equal(t, tc.name, name)
			assert.True(t, tree.Holds(got.GetToken()))
		})
	}
}
