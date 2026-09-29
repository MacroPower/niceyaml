package astnode_test

import (
	"testing"

	"github.com/goccy/go-yaml/ast"
	"github.com/stretchr/testify/assert"

	"go.jacobcolvin.com/niceyaml/internal/astnode"
)

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
				assert.Nil(t, got)

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
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, astnode.IsNil(tc.node))
		})
	}
}
