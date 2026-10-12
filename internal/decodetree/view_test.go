package decodetree_test

import (
	"testing"

	"github.com/goccy/go-yaml/ast"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.jacobcolvin.com/x/stringtest"

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
