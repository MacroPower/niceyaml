package decodetree_test

import (
	"testing"

	"github.com/goccy/go-yaml/ast"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.jacobcolvin.com/x/stringtest"

	"go.jacobcolvin.com/niceyaml/internal/astnode"
	"go.jacobcolvin.com/niceyaml/internal/decodetree"
)

func TestDecodeView(t *testing.T) {
	t.Parallel()

	_, doc := newTree(t, stringtest.LinesLF("a: &x 1", "b: [*x # note", "]"))
	node := at(t, doc, "b")

	view := decodetree.DecodeView(node)
	assert.NotSame(t, node, view)

	seq, ok := view.(*ast.SequenceNode)
	require.True(t, ok)

	alias, ok := seq.Values[0].(*ast.AliasNode)
	require.True(t, ok)
	assert.Equal(t, "x", alias.Value.GetToken().Value)
	assert.IsType(t, &ast.StringNode{}, alias.Value)

	plain := at(t, doc, "a")
	assert.Same(t, plain, decodetree.DecodeView(plain))
}

func TestAnchorViews(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input string
		// Whether the first call reads the inner anchor.
		innerFirst bool
	}{
		"outer anchor first": {
			input: stringtest.LinesLF("z: &z 1", "a: &x {k: *z, <<: &y {j: *z}}"),
		},
		"inner anchor first": {
			input:      stringtest.LinesLF("z: &z 1", "a: &x {k: *z, <<: &y {j: *z}}"),
			innerFirst: true,
		},
		"alias in the outer anchor alone": {
			input:      stringtest.LinesLF("z: &z 1", "a: &x {k: *z, <<: &y {j: 1}}"),
			innerFirst: true,
		},
		"anchors without aliases": {
			input: "a: &x {k: 1, <<: &y {j: 1}}\n",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, doc := newTree(t, tc.input)
			outer, ok := at(t, doc, "a").(*ast.AnchorNode)
			require.True(t, ok)

			entries, ok := astnode.Entries(outer.Value)
			require.True(t, ok)

			inner, ok := entries[len(entries)-1].Value.(*ast.AnchorNode)
			require.True(t, ok)

			var views decodetree.AnchorViews

			order := []*ast.AnchorNode{outer, inner}
			if tc.innerFirst {
				order = []*ast.AnchorNode{inner, outer}
			}

			for _, anchor := range order {
				view := views.View(anchor)
				assert.Equal(t, decodetree.DecodeView(anchor).String(), view.String())
				assert.Same(t, view, views.View(anchor))
			}

			// The view of the outer anchor holds the view of the inner one.
			viewEntries, ok := astnode.Entries(views.View(outer).Value)
			require.True(t, ok)
			assert.Same(t, views.View(inner), viewEntries[len(viewEntries)-1].Value)
		})
	}
}
