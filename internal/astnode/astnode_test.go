package astnode_test

import (
	"testing"

	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/parser"
	"github.com/goccy/go-yaml/token"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.jacobcolvin.com/x/stringtest"

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
		"nil":       {node: nil},
		"typed nil": {node: (*ast.MappingNode)(nil)},
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
		"typed nil wrapper":        {node: (*ast.AnchorNode)(nil)},
		"wrapper around typed nil": {node: &ast.TagNode{Value: (*ast.MappingNode)(nil)}},
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
			input: stringtest.LinesLF("a:", "  k: v"),
			steps: []any{"a"},
			want:  "a@1:1",
		},
		"block sequence under a key": {
			input: stringtest.LinesLF("a:", "  - x"),
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
			input: stringtest.LinesLF("a: &x", "  k: v"),
			steps: []any{"a"},
			want:  "a@1:1",
		},
		"tag on a mapping under a key": {
			input: stringtest.LinesLF("a: !!map", "  k: v"),
			steps: []any{"a"},
			want:  "a@1:1",
		},
		"mapping under an explicit key": {
			input: "? a\n: {k: v}\n",
			steps: []any{"a"},
			want:  "a@1:3",
		},
		"mapping under a nested key": {
			input: stringtest.LinesLF("a:", "  b:", "    k: v"),
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
			input: stringtest.LinesLF("- &x", "  k: v"),
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
			input: stringtest.LinesLF("a:", "  - k: v"),
			steps: []any{"a", 0},
			want:  "-@2:3",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			node, in := step(t, yamltest.FirstDocument(t, tc.input).AST(), tc.steps...)

			assert.Equal(t, tc.want, yamltest.TokenPlace(astnode.PathToken(node, in)))
		})
	}

	t.Run("alias is its own token", func(t *testing.T) {
		t.Parallel()

		root := yamltest.FirstDocument(t, stringtest.LinesLF("a: &x {k: v}", "b: *x", "c:", "  - *x")).AST()

		alias, in := step(t, root, "b")
		assert.Same(t, alias.GetToken(), astnode.PathToken(alias, in))

		alias, in = step(t, root, "c", 0)
		assert.Same(t, alias.GetToken(), astnode.PathToken(alias, in))
	})

	t.Run("entry with no key introduces nothing", func(t *testing.T) {
		t.Parallel()

		mapping := yamltest.FirstDocument(t, "{k: v}\n").AST()
		entry := &ast.MappingValueNode{Value: mapping}

		assert.Equal(t, "{@1:1", yamltest.TokenPlace(astnode.PathToken(mapping, astnode.Parent{Entry: entry})))
	})

	t.Run("sequence with no entries introduces nothing", func(t *testing.T) {
		t.Parallel()

		element, in := step(t, yamltest.FirstDocument(t, "- k: v\n").AST(), 0)
		in.Sequence = &ast.SequenceNode{Values: in.Sequence.Values}

		assert.Equal(t, "k@1:3", yamltest.TokenPlace(astnode.PathToken(element, in)))
	})

	t.Run("index outside the sequence introduces nothing", func(t *testing.T) {
		t.Parallel()

		element, in := step(t, yamltest.FirstDocument(t, "- k: v\n").AST(), 0)

		in.Index = 1
		assert.Equal(t, "k@1:3", yamltest.TokenPlace(astnode.PathToken(element, in)))

		in.Index = -1
		assert.Equal(t, "k@1:3", yamltest.TokenPlace(astnode.PathToken(element, in)))
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

			assert.Equal(t, tc.want, yamltest.TokenPlace(astnode.KeyToken(mapping.Values[0])))
		})
	}

	t.Run("no key has no token", func(t *testing.T) {
		t.Parallel()

		assert.Nil(t, astnode.KeyToken(nil))
		assert.Nil(t, astnode.KeyToken(&ast.MappingValueNode{}))
		assert.Nil(t, astnode.KeyToken(&ast.MappingValueNode{Key: (*ast.StringNode)(nil)}))
	})
}

// parse returns the body of the first document of input, comments
// included.
func parse(t *testing.T, input string) ast.Node {
	t.Helper()

	file, err := parser.ParseBytes([]byte(input), parser.ParseComments)
	require.NoError(t, err)
	require.NotEmpty(t, file.Docs)

	return file.Docs[0].Body
}

// describe returns the type of node and the value of its token.
func describe(node ast.Node) string {
	tk := node.GetToken()
	if tk == nil {
		return node.Type().String()
	}

	return node.Type().String() + ":" + tk.Value
}

func TestInspect(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		node ast.Node
		// The node types whose children the walk passes over.
		prune map[ast.NodeType]bool
		want  []string
	}{
		"nil": {
			node: nil,
		},
		"typed nil": {
			node: (*ast.MappingNode)(nil),
		},
		"typed nil below a node": {
			node: &ast.TagNode{Value: (*ast.StringNode)(nil)},
			want: []string{"Tag"},
		},
		"walk order": {
			node: parse(t, "a: &x [1, 2] # c\nb: *x\n"),
			want: []string{
				"Mapping::", "MappingValue::", "String:a", "Anchor:&", "String:x",
				"Sequence:[", "Comment: c", "Integer:1", "Integer:2",
				"MappingValue::", "String:b", "Alias:*", "String:x",
			},
		},
		"pruned": {
			node:  parse(t, "a: [1, 2]\nb: 3\n"),
			prune: map[ast.NodeType]bool{ast.SequenceType: true},
			want: []string{
				"Mapping::", "MappingValue::", "String:a", "Sequence:[",
				"MappingValue::", "String:b", "Integer:3",
			},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var got []string

			astnode.Inspect(tc.node, func(n ast.Node) bool {
				got = append(got, describe(n))

				return !tc.prune[n.Type()]
			})

			assert.Equal(t, tc.want, got)
		})
	}
}

func TestNodes(t *testing.T) {
	t.Parallel()

	var got []string

	for _, n := range astnode.Nodes(parse(t, "# head\na: 1 # line\n")) {
		got = append(got, describe(n))
	}

	assert.Equal(t, []string{"Mapping::", "MappingValue::", "String:a", "Integer:1"}, got)
	assert.Empty(t, astnode.Nodes(nil))
}

func TestToken(t *testing.T) {
	t.Parallel()

	scalar := &ast.StringNode{Token: token.New("a", "a", nil), Value: "a"}

	assert.Same(t, scalar.Token, astnode.Token(scalar))
	assert.Nil(t, astnode.Token(nil))
	assert.Nil(t, astnode.Token((*ast.StringNode)(nil)))
}

func TestName(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		node ast.Node
		want string
		ok   bool
	}{
		"scalar": {
			node: &ast.StringNode{Token: token.New("x", "x", nil)},
			want: "x",
			ok:   true,
		},
		"no token": {
			node: &ast.StringNode{},
		},
		"nil": {
			node: nil,
		},
		"typed nil": {
			node: (*ast.StringNode)(nil),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, ok := astnode.Name(tc.node)
			assert.Equal(t, tc.want, got)
			assert.Equal(t, tc.ok, ok)
		})
	}
}

func TestExtent(t *testing.T) {
	t.Parallel()

	at := func(off int) *token.Token {
		return &token.Token{Position: &token.Position{Offset: off}}
	}

	tcs := map[string]struct {
		extent astnode.Extent
		want   astnode.Extent
	}{
		"empty": {
			extent: astnode.Extent{},
			want:   astnode.Extent{},
		},
		"one token": {
			extent: astnode.Extent{}.With(at(4)),
			want:   astnode.Extent{Lo: 4, Hi: 4, OK: true},
		},
		"tokens widen it": {
			extent: astnode.Extent{}.With(at(4)).With(at(9)).With(at(2)),
			want:   astnode.Extent{Lo: 2, Hi: 9, OK: true},
		},
		"no position changes nothing": {
			extent: astnode.Extent{}.With(at(4)).With(&token.Token{}).With(nil),
			want:   astnode.Extent{Lo: 4, Hi: 4, OK: true},
		},
		"union": {
			extent: astnode.Extent{Lo: 1, Hi: 3, OK: true}.Union(astnode.Extent{Lo: 6, Hi: 8, OK: true}),
			want:   astnode.Extent{Lo: 1, Hi: 8, OK: true},
		},
		"union with empty": {
			extent: astnode.Extent{}.Union(astnode.Extent{Lo: 6, Hi: 8, OK: true}).Union(astnode.Extent{}),
			want:   astnode.Extent{Lo: 6, Hi: 8, OK: true},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, tc.extent)
		})
	}

	t.Run("holds", func(t *testing.T) {
		t.Parallel()

		e := astnode.Extent{Lo: 2, Hi: 5, OK: true}

		assert.False(t, e.Holds(1))
		assert.True(t, e.Holds(2))
		assert.True(t, e.Holds(5))
		assert.False(t, e.Holds(6))
		assert.False(t, astnode.Extent{}.Holds(0))
	})
}

func TestExtentOf(t *testing.T) {
	t.Parallel()

	// The go-yaml parser counts the offsets of the first line from 1.
	tcs := map[string]struct {
		input    string
		comments astnode.Extent
		content  astnode.Extent
	}{
		"scalar": {
			input:    "abc\n",
			comments: astnode.Extent{Lo: 1, Hi: 1, OK: true},
			content:  astnode.Extent{Lo: 1, Hi: 1, OK: true},
		},
		"flow mapping ends at its brace": {
			input:    "{a: 1}\n",
			comments: astnode.Extent{Lo: 1, Hi: 6, OK: true},
			content:  astnode.Extent{Lo: 1, Hi: 6, OK: true},
		},
		"block sequence starts at its dash": {
			input:    "- a\n- b\n",
			comments: astnode.Extent{Lo: 1, Hi: 7, OK: true},
			content:  astnode.Extent{Lo: 1, Hi: 7, OK: true},
		},
		"comment after a value": {
			input:    "a: 1 # c\n",
			comments: astnode.Extent{Lo: 1, Hi: 6, OK: true},
			content:  astnode.Extent{Lo: 1, Hi: 4, OK: true},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			node := parse(t, tc.input)

			assert.Equal(t, tc.comments, astnode.ExtentOf(node, true))
			assert.Equal(t, tc.content, astnode.ExtentOf(node, false))
		})
	}

	t.Run("matches the bounds", func(t *testing.T) {
		t.Parallel()

		inputs := []string{
			"# head\na: 1 # line\nb: [x, y] # tail\n",
			"- &a {k: v}\n- *a\n-\n",
			"<<: [*a, *b]\n? k\n: v\n",
			"a: |\n  text\n# foot\n",
			"%YAML 1.2\n---\n!!map {a: 1}\n",
		}

		for _, input := range inputs {
			file, err := parser.ParseBytes([]byte(input), parser.ParseComments)
			require.NoError(t, err)

			for _, n := range astnode.Nodes(file.Docs[0]) {
				first, last := astnode.Bounds(n)

				want := astnode.Extent{}
				if len(first) > 0 {
					want = astnode.Extent{Lo: first[0].Position.Offset, Hi: last[0].Position.Offset, OK: true}
				}

				assert.Equal(t, want, astnode.ExtentOf(n, true), "%q: %s", input, describe(n))

				start := astnode.StartToken(n)
				if start != nil {
					assert.Equal(t, start.Position.Offset, astnode.ExtentOf(n, false).Lo)
				}
			}
		}
	})

	t.Run("nil", func(t *testing.T) {
		t.Parallel()

		assert.Equal(t, astnode.Extent{}, astnode.ExtentOf(nil, true))
		assert.Equal(t, astnode.Extent{}, astnode.ExtentOf((*ast.MappingNode)(nil), false))
	})
}

func TestBounds(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input       string
		first, last []string
	}{
		"block mapping": {
			input: "a: 1\nb: 2\n",
			first: []string{"a@1:1"},
			last:  []string{"2@2:4"},
		},
		"comment counts": {
			input: "a: 1 # c\n",
			first: []string{"a@1:1"},
			last:  []string{" c@1:6"},
		},
		"flow sequence ends at its bracket": {
			input: "[a, b]\n",
			first: []string{"[@1:1"},
			last:  []string{"]@1:6"},
		},
		"implicit null shares the offset of its dash": {
			input: "- a\n-\n",
			first: []string{"-@1:1", "-@1:1"},
			last:  []string{"-@2:1", "null@2:3"},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			first, last := astnode.Bounds(parse(t, tc.input))

			assert.Equal(t, tc.first, places(first))
			assert.Equal(t, tc.last, places(last))
		})
	}

	t.Run("nil", func(t *testing.T) {
		t.Parallel()

		first, last := astnode.Bounds((*ast.MappingNode)(nil))
		assert.Nil(t, first)
		assert.Nil(t, last)
	})
}

// places returns the place of each token, as [yamltest.TokenPlace]
// writes it.
func places(tks token.Tokens) []string {
	var out []string

	for _, tk := range tks {
		out = append(out, yamltest.TokenPlace(tk))
	}

	return out
}

func TestStartToken(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		node ast.Node
		want string
	}{
		"comment before a value": {
			node: parse(t, "# c\na: 1\n"),
			want: "a@2:1",
		},
		"anchor": {
			node: parse(t, "&x [1]\n"),
			want: "&@1:1",
		},
		"nil": {
			node: nil,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := astnode.StartToken(tc.node)
			if tc.want == "" {
				assert.Nil(t, got)

				return
			}

			assert.Equal(t, tc.want, yamltest.TokenPlace(got))
		})
	}
}

// firstEntry returns the first entry of the mapping that input holds.
func firstEntry(t *testing.T, input string) *ast.MappingValueNode {
	t.Helper()

	entries, ok := astnode.Entries(parse(t, input))
	require.True(t, ok)
	require.NotEmpty(t, entries)

	return entries[0]
}

func TestIsMergeKey(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		key  ast.Node
		want bool
	}{
		"merge key": {
			key:  firstEntry(t, "<<: {a: 1}\n").Key,
			want: true,
		},
		"explicit merge key": {
			key:  &ast.MappingKeyNode{Value: &ast.MergeKeyNode{}},
			want: true,
		},
		"anchored merge key": {
			key:  firstEntry(t, "&k <<: {a: 1}\n").Key,
			want: true,
		},
		"quoted key": {
			key:  firstEntry(t, "'<<': {a: 1}\n").Key,
			want: false,
		},
		"plain key": {
			key:  firstEntry(t, "a: 1\n").Key,
			want: false,
		},
		"nil": {
			key:  nil,
			want: false,
		},
		"typed nil": {
			key:  (*ast.MappingKeyNode)(nil),
			want: false,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, astnode.IsMergeKey(tc.key))
		})
	}
}

func TestMergeValues(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input string
		want  []string
	}{
		"one alias": {
			input: "<<: *a\n",
			want:  []string{"Alias:*"},
		},
		"sequence of aliases": {
			input: "<<: [*a, &b !t {k: v}]\n",
			want:  []string{"Alias:*", "Mapping:{"},
		},
		"no merge key": {
			input: "a: *a\n",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var got []string

			for _, n := range astnode.MergeValues(firstEntry(t, tc.input)) {
				got = append(got, describe(n))
			}

			assert.Equal(t, tc.want, got)
		})
	}

	t.Run("nil entry", func(t *testing.T) {
		t.Parallel()

		assert.Nil(t, astnode.MergeValues(nil))
	})
}

func TestEntries(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		node ast.Node
		want int
		ok   bool
	}{
		"mapping": {
			node: parse(t, "a: 1\nb: 2\n"),
			want: 2,
			ok:   true,
		},
		"mapping of one entry": {
			node: firstEntry(t, "a: 1\n"),
			want: 1,
			ok:   true,
		},
		"anchored mapping": {
			node: parse(t, "&x {a: 1, b: 2, c: 3}\n"),
			want: 3,
			ok:   true,
		},
		"sequence": {
			node: parse(t, "[a]\n"),
		},
		"typed nil": {
			node: (*ast.MappingNode)(nil),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, ok := astnode.Entries(tc.node)
			assert.Len(t, got, tc.want)
			assert.Equal(t, tc.ok, ok)
		})
	}
}

func TestIsTaggedNull(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input string
		want  bool
	}{
		"null tag over a value": {
			input: "a: !!null x\n",
			want:  true,
		},
		"map tag over no value": {
			input: "a: !!map\n",
			want:  true,
		},
		"local tag over no value": {
			input: "a: !local\n",
			want:  true,
		},
		"anchor on a tagged null": {
			input: "a: &x !!seq\n",
			want:  true,
		},
		"string tag over no value": {
			input: "a: !!str\n",
			want:  false,
		},
		"local tag over a value": {
			input: "a: !local x\n",
			want:  false,
		},
		"no tag": {
			input: "a: x\n",
			want:  false,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, astnode.IsTaggedNull(firstEntry(t, tc.input).Value))
		})
	}
}

func TestIsScalar(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		node ast.Node
		want bool
	}{
		"nil":      {node: nil, want: false},
		"mapping":  {node: &ast.MappingNode{}, want: false},
		"sequence": {node: &ast.SequenceNode{}, want: false},
		"scalar":   {node: &ast.StringNode{}, want: true},
		"alias":    {node: &ast.AliasNode{}, want: true},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, astnode.IsScalar(tc.node))
		})
	}
}
