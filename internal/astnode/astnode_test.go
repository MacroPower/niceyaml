package astnode_test

import (
	"fmt"
	"testing"

	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/parser"
	"github.com/goccy/go-yaml/token"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.jacobcolvin.com/niceyaml/internal/astnode"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/tokens"
)

// embeddedNode is a struct value that satisfies [ast.Node] through an
// embedded node pointer.
type embeddedNode struct{ *ast.StringNode }

// decoratedNode wraps an [ast.Node] the way a decorator does.
type decoratedNode struct{ ast.Node }

func TestContent(t *testing.T) {
	t.Parallel()

	mapping := &ast.MappingNode{}
	scalar := &ast.StringNode{Value: "a"}
	alias := &ast.AliasNode{Value: scalar}

	tcs := map[string]struct {
		node ast.Node
		want ast.Node
	}{
		"nil": {
			node: nil,
		},
		"typed nil": {
			node: (*ast.MappingNode)(nil),
		},
		"scalar": {
			node: scalar,
			want: scalar,
		},
		"document": {
			node: &ast.DocumentNode{Body: mapping},
			want: mapping,
		},
		"anchor": {
			node: &ast.AnchorNode{Value: mapping},
			want: mapping,
		},
		"tag": {
			node: &ast.TagNode{Value: mapping},
			want: mapping,
		},
		"explicit key": {
			node: &ast.MappingKeyNode{Value: mapping},
			want: mapping,
		},
		"typed nil wrapper": {
			node: (*ast.AnchorNode)(nil),
		},
		"wrapper around typed nil": {
			node: &ast.TagNode{Value: (*ast.MappingNode)(nil)},
		},
		"nested wrappers": {
			node: &ast.DocumentNode{Body: &ast.AnchorNode{Value: &ast.TagNode{Value: mapping}}},
			want: mapping,
		},
		"alias": {
			node: alias,
			want: alias,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := astnode.Content(tc.node)
			if tc.want == nil {
				// Callers assert a pointer type on the result, and a typed
				// nil passes that assertion, so Content returns an untyped
				// nil.
				assert.Equal(t, ast.Node(nil), got)

				return
			}

			assert.Same(t, tc.want, got)
		})
	}
}

func TestIsNil(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		node ast.Node
		want bool
	}{
		"nil": {
			node: nil,
			want: true,
		},
		"typed nil": {
			node: (*ast.StringNode)(nil),
			want: true,
		},
		"node": {
			node: &ast.StringNode{},
			want: false,
		},
		"struct embedding a node": {
			node: embeddedNode{&ast.StringNode{}},
			want: false,
		},
		"decorator": {
			node: decoratedNode{&ast.StringNode{}},
			want: false,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, astnode.IsNil(tc.node))
		})
	}
}

func TestHasContent(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		node ast.Node
		want bool
	}{
		"nil": {
			node: nil,
			want: false,
		},
		"typed nil mapping": {
			node: (*ast.MappingNode)(nil),
			want: false,
		},
		"typed nil scalar": {
			node: (*ast.StringNode)(nil),
			want: false,
		},
		"comment group": {
			node: &ast.CommentGroupNode{},
			want: false,
		},
		"directive": {
			node: &ast.DirectiveNode{},
			want: false,
		},
		"placeholder scalar": {
			node: &ast.StringNode{Token: tokens.Tokenize("  \n")[0]},
			want: false,
		},
		"scalar": {
			node: &ast.StringNode{Token: tokens.Tokenize("a")[0], Value: "a"},
			want: true,
		},
		"mapping": {
			node: &ast.MappingNode{},
			want: true,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, astnode.HasContent(tc.node))
		})
	}
}

// step walks from node to the value of the entry named by a string
// step, or to the element at an int step, and returns that node with the
// [astnode.Parent] that holds it.
func step(t *testing.T, node ast.Node, steps ...any) (ast.Node, astnode.Parent) {
	t.Helper()

	var in astnode.Parent

	for _, s := range steps {
		switch s := s.(type) {
		case string:
			mapping, ok := astnode.Content(node).(*ast.MappingNode)
			require.True(t, ok, "step %q reads a mapping", s)

			var found *ast.MappingValueNode

			for _, entry := range mapping.Values {
				if astnode.KeyToken(entry).Value == s {
					found = entry
				}
			}

			require.NotNil(t, found, "mapping holds the key %q", s)

			node, in = found.Value, astnode.Parent{Entry: found}

		case int:
			seq, ok := astnode.Content(node).(*ast.SequenceNode)
			require.True(t, ok, "step %d reads a sequence", s)
			require.Less(t, s, len(seq.Values))

			node, in = seq.Values[s], astnode.Parent{Sequence: seq, Index: s}

		default:
			require.Fail(t, "step is neither a key nor an index")
		}
	}

	return node, in
}

// place returns the text of tk and where it starts, as "text@line:col",
// counting from 1 as the token does.
func place(tk *token.Token) string {
	if tk == nil || tk.Position == nil {
		return ""
	}

	return fmt.Sprintf("%s@%d:%d", tk.Value, tk.Position.Line, tk.Position.Column)
}

func TestPathToken(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input string
		// The keys and the indexes from the root down to the node.
		steps []any
		want  string
	}{
		"scalar at the root": {
			input: "abc\n",
			want:  "abc@1:1",
		},
		"tag on a scalar": {
			input: "!!str v\n",
			want:  "v@1:7",
		},
		"scalar under a key": {
			input: "a: v\n",
			steps: []any{"a"},
			want:  "v@1:4",
		},
		"scalar element": {
			input: "- x\n",
			steps: []any{0},
			want:  "x@1:3",
		},
		"block mapping at the root starts at its first key": {
			input: "k: v\n",
			want:  "k@1:1",
		},
		"block mapping at the root with an explicit first key": {
			input: "? k\n: v\n",
			want:  "k@1:3",
		},
		"block mapping at the root with an anchor on the first key": {
			input: "&a k: v\n",
			want:  "k@1:4",
		},
		"block sequence at the root starts at its first element": {
			input: "- x\n- y\n",
			want:  "x@1:3",
		},
		"block sequence of mappings at the root": {
			input: "- k: v\n",
			want:  "k@1:3",
		},
		"flow mapping at the root": {
			input: "{k: v}\n",
			want:  "{@1:1",
		},
		"flow sequence at the root": {
			input: "[x, y]\n",
			want:  "[@1:1",
		},
		"anchor on a flow mapping at the root": {
			input: "&a {k: v}\n",
			want:  "{@1:4",
		},
		"empty mapping at the root": {
			input: "{}\n",
			want:  "{@1:1",
		},
		"empty sequence at the root": {
			input: "[]\n",
			want:  "[@1:1",
		},
		"block mapping under a key": {
			input: "a:\n  k: v\n",
			steps: []any{"a"},
			want:  "a@1:1",
		},
		"block sequence under a key": {
			input: "a:\n  - x\n",
			steps: []any{"a"},
			want:  "a@1:1",
		},
		"flow mapping under a key": {
			input: "a: {k: v}\n",
			steps: []any{"a"},
			want:  "a@1:1",
		},
		"empty sequence under a key": {
			input: "a: []\n",
			steps: []any{"a"},
			want:  "a@1:1",
		},
		"anchor on a mapping under a key": {
			input: "a: &x\n  k: v\n",
			steps: []any{"a"},
			want:  "a@1:1",
		},
		"tag on a mapping under a key": {
			input: "a: !!map\n  k: v\n",
			steps: []any{"a"},
			want:  "a@1:1",
		},
		"mapping under an explicit key": {
			input: "? a\n: {k: v}\n",
			steps: []any{"a"},
			want:  "a@1:3",
		},
		"mapping under a nested key": {
			input: "a:\n  b:\n    k: v\n",
			steps: []any{"a", "b"},
			want:  "b@2:3",
		},
		"block mapping element": {
			input: "- k: v\n- l: w\n",
			steps: []any{1},
			want:  "-@2:1",
		},
		"flow mapping element of a block sequence": {
			input: "- {k: v}\n",
			steps: []any{0},
			want:  "-@1:1",
		},
		"anchor on a block mapping element": {
			input: "- &x\n  k: v\n",
			steps: []any{0},
			want:  "-@1:1",
		},
		"block sequence element of a block sequence": {
			input: "- - x\n",
			steps: []any{0},
			want:  "-@1:1",
		},
		"scalar in a nested block sequence": {
			input: "- - x\n",
			steps: []any{0, 0},
			want:  "x@1:5",
		},
		"first flow mapping element of a flow sequence": {
			input: "[{a: 1}, {b: 2}]\n",
			steps: []any{0},
			want:  "{@1:2",
		},
		"later flow mapping element of a flow sequence": {
			input: "[{a: 1}, {b: 2}]\n",
			steps: []any{1},
			want:  "{@1:10",
		},
		"flow sequence element of a flow sequence": {
			input: "[[x], [y]]\n",
			steps: []any{1},
			want:  "[@1:7",
		},
		"mapping element under a key": {
			input: "a:\n  - k: v\n",
			steps: []any{"a", 0},
			want:  "-@2:3",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			node, in := step(t, yamltest.FirstDocument(t, tc.input).AST(), tc.steps...)

			assert.Equal(t, tc.want, place(astnode.PathToken(node, in)))
		})
	}

	t.Run("alias is its own token", func(t *testing.T) {
		t.Parallel()

		file, err := parser.ParseBytes([]byte("a: &x {k: v}\nb: *x\nc:\n  - *x\n"), 0)
		require.NoError(t, err)

		alias, in := step(t, file.Docs[0].Body, "b")
		assert.Same(t, alias.GetToken(), astnode.PathToken(alias, in))

		alias, in = step(t, file.Docs[0].Body, "c", 0)
		assert.Same(t, alias.GetToken(), astnode.PathToken(alias, in))
	})

	t.Run("entry with no key introduces nothing", func(t *testing.T) {
		t.Parallel()

		file, err := parser.ParseBytes([]byte("{k: v}\n"), 0)
		require.NoError(t, err)

		mapping := file.Docs[0].Body
		entry := &ast.MappingValueNode{Value: mapping}

		assert.Equal(t, "{@1:1", place(astnode.PathToken(mapping, astnode.Parent{Entry: entry})))
	})

	t.Run("sequence with no entries introduces nothing", func(t *testing.T) {
		t.Parallel()

		file, err := parser.ParseBytes([]byte("- k: v\n"), 0)
		require.NoError(t, err)

		element, in := step(t, file.Docs[0].Body, 0)
		in.Sequence = &ast.SequenceNode{Values: in.Sequence.Values}

		assert.Equal(t, "k@1:3", place(astnode.PathToken(element, in)))
	})

	t.Run("index outside the sequence introduces nothing", func(t *testing.T) {
		t.Parallel()

		file, err := parser.ParseBytes([]byte("- k: v\n"), 0)
		require.NoError(t, err)

		element, in := step(t, file.Docs[0].Body, 0)

		in.Index = 1
		assert.Equal(t, "k@1:3", place(astnode.PathToken(element, in)))

		in.Index = -1
		assert.Equal(t, "k@1:3", place(astnode.PathToken(element, in)))
	})

	t.Run("nil node has no token", func(t *testing.T) {
		t.Parallel()

		assert.Nil(t, astnode.PathToken(nil, astnode.Parent{}))
		assert.Nil(t, astnode.PathToken((*ast.MappingNode)(nil), astnode.Parent{}))
	})
}

func TestKeyToken(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input string
		want  string
	}{
		"plain key": {
			input: "k: v\n",
			want:  "k@1:1",
		},
		"explicit key leaves out its indicator": {
			input: "? k\n: v\n",
			want:  "k@1:3",
		},
		"anchor on the key": {
			input: "&a k: v\n",
			want:  "k@1:4",
		},
		"tag on the key": {
			input: "!!str k: v\n",
			want:  "k@1:7",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			mapping, ok := yamltest.FirstDocument(t, tc.input).AST().(*ast.MappingNode)
			require.True(t, ok)
			require.Len(t, mapping.Values, 1)

			assert.Equal(t, tc.want, place(astnode.KeyToken(mapping.Values[0])))
		})
	}

	t.Run("no key has no token", func(t *testing.T) {
		t.Parallel()

		assert.Nil(t, astnode.KeyToken(nil))
		assert.Nil(t, astnode.KeyToken(&ast.MappingValueNode{}))
		assert.Nil(t, astnode.KeyToken(&ast.MappingValueNode{Key: (*ast.StringNode)(nil)}))
	})
}
