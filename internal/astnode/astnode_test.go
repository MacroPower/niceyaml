package astnode_test

import (
	"testing"

	"github.com/goccy/go-yaml/ast"
	"github.com/stretchr/testify/assert"

	"go.jacobcolvin.com/niceyaml/internal/astnode"
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
