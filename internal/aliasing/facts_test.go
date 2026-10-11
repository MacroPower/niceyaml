package aliasing_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"go.jacobcolvin.com/x/stringtest"

	"go.jacobcolvin.com/niceyaml/internal/aliasing"
	"go.jacobcolvin.com/niceyaml/internal/astnode"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
)

func TestNewFacts(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input string
		// Each alias as "*name -> &anchor", with " inside" and " merge"
		// after it for the facts that hold.
		aliases []string
		anchors []string
		// The name of each alias in Enclosed, in document order.
		enclosed []string
	}{
		"an alias after its anchor": {
			input:   stringtest.LinesLF("a: &x 1", "b: *x"),
			aliases: []string{"*x -> &x"},
			anchors: []string{"x"},
		},
		"an alias inside its anchor": {
			input:    "a: &x [1, *x]\n",
			aliases:  []string{"*x -> &x inside"},
			anchors:  []string{"x"},
			enclosed: []string{"x"},
		},
		"a merge inside its anchor": {
			input:   "a: &x {b: 1, <<: *x}\n",
			aliases: []string{"*x -> &x inside merge"},
			anchors: []string{"x"},
		},
		"a merge list": {
			input:   stringtest.LinesLF("a: &x {b: 1}", "c: &y {d: 2}", "e: {<<: [*x, *y]}"),
			aliases: []string{"*x -> &x merge", "*y -> &y merge"},
			anchors: []string{"x", "y"},
		},
		"an alias to no anchor": {
			input:   "a: *x\n",
			aliases: []string{"*x -> none"},
		},
		"aliases inside nested anchors": {
			input:    "a: &x {b: &y [*x, *y]}\n",
			aliases:  []string{"*x -> &x inside", "*y -> &y inside"},
			anchors:  []string{"x", "y"},
			enclosed: []string{"x", "y"},
		},
		"an alias after a nested anchor": {
			input:   stringtest.LinesLF("a: &x {b: &y 1}", "c: *y"),
			aliases: []string{"*y -> &y"},
			anchors: []string{"x", "y"},
		},
		"a later anchor of the same name": {
			input:    stringtest.LinesLF("a: &x 1", "b: &x [*x]"),
			aliases:  []string{"*x -> &x inside"},
			anchors:  []string{"x", "x"},
			enclosed: []string{"x"},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			doc := yamltest.FirstDocument(t, tc.input)
			facts := aliasing.NewFacts(doc.AST(), doc.Resolver())

			var aliases, enclosed []string

			for _, a := range facts.Aliases {
				s := "*" + a.Name + " -> none"
				if a.Anchor != nil {
					s = "*" + a.Name + " -> &" + a.Anchor.Name.GetToken().Value
				}

				if a.Inside {
					s += " inside"
				}

				if a.Merge {
					s += " merge"
				}

				aliases = append(aliases, s)

				if facts.Enclosed[a.Node] {
					enclosed = append(enclosed, a.Name)
				}
			}

			var anchors []string

			for _, a := range facts.Anchors {
				assert.Equal(t, a.Name, a.Node.Name.GetToken().Value)

				anchors = append(anchors, a.Name)
			}

			assert.Equal(t, tc.aliases, aliases)
			assert.Equal(t, tc.anchors, anchors)
			assert.Equal(t, tc.enclosed, enclosed)
			assert.Len(t, facts.Enclosed, len(tc.enclosed))
		})
	}
}

func TestNewFacts_Extent(t *testing.T) {
	t.Parallel()

	tcs := map[string]string{
		"a scalar":              "a: &x 1\n",
		"a block mapping":       stringtest.LinesLF("a: &x", "  b: 1", "  c: [2, 3]", "d: 4"),
		"nested anchors":        stringtest.LinesLF("a: &x", "  b: &y", "    - 1", "    - *x", "  c: 2"),
		"a comment inside":      stringtest.LinesLF("a: &x", "  b: 1", "  # note", "  c: 2"),
		"a flow sequence":       "a: &x [1, &y {b: *x}, 3]\n",
		"an empty entry":        stringtest.LinesLF("a: &x", "  -", "  - 1"),
		"a comment on an alias": stringtest.LinesLF("a: &x", "  - 1", "  - *x # note"),
	}

	for name, input := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			doc := yamltest.FirstDocument(t, input)
			facts := aliasing.NewFacts(doc.AST(), doc.Resolver())
			assert.NotEmpty(t, facts.Anchors)

			for _, a := range facts.Anchors {
				assert.Equal(t, astnode.ExtentOf(a.Node, true), a.Extent, "anchor %s", a.Name)
			}
		})
	}
}
