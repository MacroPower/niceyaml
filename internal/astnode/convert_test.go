package astnode_test

import (
	"testing"

	"github.com/goccy/go-yaml/ast"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.jacobcolvin.com/niceyaml/internal/astnode"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/paths"
)

func TestConverted(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input string
		// The type of the node that Converted returns for the value of m.
		want ast.NodeType
	}{
		"string tag on an alias of a sequence": {input: "a: &k [x, y]\nm: !!str *k\n", want: ast.TagType},
		"null tag on an alias of a mapping":    {input: "a: &k {p: 1}\nm: !!null *k\n", want: ast.TagType},
		"anchor over a string tag on an alias": {input: "a: &k [x]\nm: &x !!str *k\n", want: ast.TagType},
		"local tag on an alias":                {input: "a: &k [x]\nm: !t *k\n", want: ast.AliasType},
		"local tag on a mapping":               {input: "m: !t {p: 1}\n", want: ast.MappingType},
		"string tag on a scalar":               {input: "m: !!str 16\n", want: ast.TagType},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			doc := yamltest.FirstDocument(t, tc.input)

			node, err := doc.Resolver().Node(paths.MustParse("$.m"))
			require.NoError(t, err)

			got := astnode.Converted(node)
			require.NotNil(t, got)
			assert.Equal(t, tc.want, got.Type())
		})
	}
}

func TestConvertsCollection(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		tag      string
		converts bool
		reads    bool
	}{
		"string":          {tag: "!!str", converts: true, reads: true},
		"null":            {tag: "!!null", converts: true},
		"integer":         {tag: "!!int", converts: true},
		"float":           {tag: "!!float", converts: true},
		"boolean":         {tag: "!!bool", converts: true},
		"verbatim string": {tag: "!<tag:yaml.org,2002:str>", reads: true},
		"local tag":       {tag: "!t", reads: true},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			doc := yamltest.FirstDocument(t, "a: &k {p: 1}\nm: "+tc.tag+" *k\n")

			node, err := doc.Resolver().Node(paths.MustParse("$.m"))
			require.NoError(t, err)

			tag, ok := node.(*ast.TagNode)
			require.True(t, ok)
			assert.Equal(t, tc.converts, astnode.ConvertsCollection(tag))
			assert.Equal(t, tc.reads, astnode.ReadsCollection(tag))
		})
	}
}
