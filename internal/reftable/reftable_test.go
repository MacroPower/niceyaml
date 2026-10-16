package reftable_test

import (
	"testing"

	"github.com/goccy/go-yaml/ast"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.jacobcolvin.com/x/stringtest"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/astnode"
	"go.jacobcolvin.com/niceyaml/internal/reftable"
)

// newTable returns a [*reftable.Table] of the documents of each of
// inputs, in order, over refs.
func newTable(tb testing.TB, inputs []string, refs ...*reftable.Table) *reftable.Table {
	tb.Helper()

	var bodies []ast.Node

	for _, input := range inputs {
		for _, doc := range niceyaml.NewSourceFromString(input).AllDocuments() {
			require.NoError(tb, doc.Err())

			bodies = append(bodies, doc.AST())
		}
	}

	return reftable.New(bodies, refs...)
}

// anchorText returns the text of node, which must be an anchor, such as
// "&x 1".
func anchorText(tb testing.TB, node ast.Node) string {
	tb.Helper()

	anchor, ok := node.(*ast.AnchorNode)
	require.True(tb, ok, "not an anchor: %T", node)

	return anchor.String()
}

func TestTable_Anchor(t *testing.T) {
	t.Parallel()

	one := newTable(t, []string{"a: &x 1\n"})
	two := newTable(t, []string{"b: &x 2\n"})

	tcs := map[string]struct {
		table *reftable.Table
		name  string
		// The text of the anchor the table holds for name, or empty for
		// none.
		want string
	}{
		"anchor of a document": {
			table: one,
			name:  "x",
			want:  "&x 1",
		},
		"last anchor of a name in one document": {
			table: newTable(t, []string{stringtest.LinesLF("a: &x 1", "b: &x 2")}),
			name:  "x",
			want:  "&x 2",
		},
		"anchor inside an anchor of its name comes after it": {
			table: newTable(t, []string{"a: &x [&x 2]\n"}),
			name:  "x",
			want:  "&x 2",
		},
		"later document": {
			table: newTable(t, []string{stringtest.LinesLF("a: &x 1", "---", "b: &x 2")}),
			name:  "x",
			want:  "&x 2",
		},
		"documents of the table hide those of the tables it reads": {
			table: newTable(t, []string{"a: &x 1\n"}, two),
			name:  "x",
			want:  "&x 1",
		},
		"later table hides an earlier one": {
			table: reftable.New(nil, one, two),
			name:  "x",
			want:  "&x 2",
		},
		"anchor of a table it reads": {
			table: reftable.New(nil, nil, one),
			name:  "x",
			want:  "&x 1",
		},
		"name of no anchor": {
			table: one,
			name:  "y",
		},
		"nil table": {
			name: "x",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, ok := tc.table.Anchor(tc.name)
			if tc.want == "" {
				assert.False(t, ok)

				return
			}

			require.True(t, ok)
			assert.Equal(t, tc.want, got.String())
		})
	}
}

func TestTable_Prime(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input string
		names []string
		// The text of each anchor Prime returns, in order.
		want []string
	}{
		"anchor with no alias": {
			input: stringtest.LinesLF("a: &a 1", "b: &b 2"),
			names: []string{"b"},
			want:  []string{"&b 2"},
		},
		"anchor after the anchors it reaches": {
			input: stringtest.LinesLF("list: &list [*x, *y]", "x: &x 1", "y: &y [*x]"),
			names: []string{"list"},
			want:  []string{"&x 1", "&y [*x]", "&list [*x, *y]"},
		},
		"anchor two names reach comes once": {
			input: stringtest.LinesLF("x: &x 1", "a: &a [*x]", "b: &b [*x]"),
			names: []string{"a", "b", "a"},
			want:  []string{"&x 1", "&a [*x]", "&b [*x]"},
		},
		"last anchor of a name": {
			input: stringtest.LinesLF("x: &x 1", "list: &list [*x]", "y: &x 2"),
			names: []string{"list"},
			want:  []string{"&x 2", "&list [*x]"},
		},
		"alias inside its own anchor": {
			input: "a: &a [1, *a]\n",
			names: []string{"a"},
			want:  []string{"&a [1, *a]"},
		},
		"loop of anchors": {
			input: stringtest.LinesLF("a: &a [*b]", "b: &b [*a]"),
			names: []string{"a"},
			want:  []string{"&b [*a]", "&a [*b]"},
		},
		"alias to no anchor": {
			input: "a: &a [*missing]\n",
			names: []string{"a", "other"},
			want:  []string{"&a [*missing]"},
		},
		"no name": {
			input: "a: &a 1\n",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var got []string

			for _, node := range newTable(t, []string{tc.input}).Prime(tc.names) {
				got = append(got, anchorText(t, node))
			}

			assert.Equal(t, tc.want, got)
		})
	}

	t.Run("alias with a comment on its name", func(t *testing.T) {
		t.Parallel()

		// The decoder finds an anchor by the text of the name of an alias,
		// so the anchor comes back with names free of comments.
		got := newTable(t, []string{stringtest.LinesLF("x: &x 1", "a: &a [*x # note", "]")}).Prime([]string{"a"})
		require.Len(t, got, 2)

		var names []string

		astnode.Inspect(got[1], func(n ast.Node) bool {
			if alias, ok := n.(*ast.AliasNode); ok {
				names = append(names, alias.Value.String())
			}

			return true
		})

		assert.Equal(t, []string{"x"}, names)
	})

	t.Run("nil table", func(t *testing.T) {
		t.Parallel()

		var table *reftable.Table

		assert.Empty(t, table.Prime([]string{"x"}))
	})
}
