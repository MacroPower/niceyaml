package reftable_test

import (
	"testing"

	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/token"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.jacobcolvin.com/x/stringtest"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/astnode"
	"go.jacobcolvin.com/niceyaml/internal/reftable"
	"go.jacobcolvin.com/niceyaml/paths"
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

func TestTable_View(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		table *reftable.Table
		name  string
		// The text of the anchor the table holds for name, or empty for
		// none.
		want string
	}{
		"anchor of a document": {
			table: newTable(t, []string{"a: &x 1\n"}),
			name:  "x",
			want:  "&x 1",
		},
		"anchor of a table it reads": {
			table: reftable.New(nil, newTable(t, []string{"a: &x 1\n"})),
			name:  "x",
			want:  "&x 1",
		},
		"name of no anchor": {
			table: newTable(t, []string{"a: &x 1\n"}),
			name:  "y",
		},
		"nil table": {
			name: "x",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, ok := tc.table.View(tc.name)
			if tc.want == "" {
				assert.False(t, ok)

				return
			}

			require.True(t, ok)
			assert.Equal(t, tc.want, got.String())
		})
	}

	t.Run("view that Prime returns", func(t *testing.T) {
		t.Parallel()

		// The view holds each alias with a name free of comments, as the
		// decoder reads it.
		table := newTable(t, []string{stringtest.LinesLF("x: &x 1", "a: &a [*x # note", "]")})

		got, ok := table.View("a")
		require.True(t, ok)

		primed := table.Prime([]string{"a"})
		require.Len(t, primed, 2)
		assert.Same(t, primed[1], got)

		raw, ok := table.Anchor("a")
		require.True(t, ok)
		assert.NotSame(t, raw, got)
	})

	t.Run("anchor inside another", func(t *testing.T) {
		t.Parallel()

		// The view of the inner anchor is the node that stands for it in the
		// view of the outer one, whether the views copy it or not.
		tcs := map[string]struct {
			input string
			// Whether the first view the table gives is that of the inner
			// anchor.
			innerFirst bool
		}{
			"without aliases": {
				input: "a: &x {k: 1, <<: &y {j: 1}}\n",
			},
			"with an alias in each anchor": {
				input: stringtest.LinesLF("z: &z 1", "a: &x {k: *z, <<: &y {j: *z}}"),
			},
			"inner anchor first": {
				input:      stringtest.LinesLF("z: &z 1", "a: &x {k: *z, <<: &y {j: *z}}"),
				innerFirst: true,
			},
			"two levels deep": {
				input: stringtest.LinesLF("z: &z 1", "a: &w {<<: &x {k: *z, <<: &y {j: *z}}}"),
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				table := newTable(t, []string{tc.input})

				if tc.innerFirst {
					_, ok := table.View("y")
					require.True(t, ok)
				}

				outer, ok := table.View("x")
				require.True(t, ok)

				inner, ok := table.View("y")
				require.True(t, ok)

				entries, ok := astnode.Entries(outer.Value)
				require.True(t, ok)
				assert.Same(t, inner, entries[len(entries)-1].Value)
			})
		}
	})
}

func TestTable_Keys(t *testing.T) {
	t.Parallel()

	shared := newTable(t, []string{stringtest.LinesLF("a: &x 1", "b: &y 2")})

	tcs := map[string]struct {
		table *reftable.Table
		name  string
		// Whether the table holds an anchor for name.
		want bool
	}{
		"anchor of a document": {
			table: shared,
			name:  "x",
			want:  true,
		},
		"anchor of a table it reads": {
			table: reftable.New(nil, shared),
			name:  "x",
			want:  true,
		},
		"name of no anchor": {
			table: shared,
			name:  "z",
		},
		"nil table": {
			name: "x",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := tc.table.Keys(tc.name)
			if !tc.want {
				assert.Nil(t, got)

				return
			}

			require.NotNil(t, got)
			assert.Same(t, got, tc.table.Keys(tc.name))
		})
	}

	t.Run("tables that read one table", func(t *testing.T) {
		t.Parallel()

		// The anchors of the documents of a table share its keys, and every
		// table that reads it returns them. The anchors of the documents of
		// another table have the keys of that table.
		table := newTable(t, []string{stringtest.LinesLF("a: &x 1", "b: &y {c: &z 2}")})
		got := table.Keys("x")

		assert.Same(t, got, table.Keys("y"))
		assert.Same(t, got, table.Keys("z"))
		assert.Same(t, got, reftable.New(nil, table).Keys("x"))
		assert.Same(t, got, reftable.New(nil, nil, table).Keys("x"))

		outer := newTable(t, []string{"d: &w 3\n"}, table)
		assert.Same(t, got, outer.Keys("x"))
		assert.NotSame(t, got, outer.Keys("w"))
	})
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

func TestPlaces_Paths(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input string
		// The name of the anchor the paths start at.
		from string
		// The name of the anchor that holds the token, and the text of the
		// token. Paths finds the first token of that text there.
		in, token string
		want      []string
	}{
		"value of an entry": {
			input: "x: &x {a: {b: bad}}\n",
			from:  "x", in: "x", token: "bad",
			want: []string{"@.a.b"},
		},
		"key of an entry": {
			input: "x: &x {a: {extra: 1}}\n",
			from:  "x", in: "x", token: "extra",
			want: []string{"@.a.extra~"},
		},
		"mapping that the decoder reports by its token": {
			input: "x: &x {a: {b: 1}}\n",
			from:  "x", in: "x", token: "{",
			want: []string{"@"},
		},
		"element of a sequence": {
			input: "x: &x [1, bad]\n",
			from:  "x", in: "x", token: "bad",
			want: []string{"@[1]"},
		},
		"token through an alias": {
			input: stringtest.LinesLF("y: &y {p: bad}", "x: &x {s: *y}"),
			from:  "x", in: "y", token: "bad",
			want: []string{"@.s.p"},
		},
		"entry that a merge key brings in": {
			input: stringtest.LinesLF("y: &y {p: bad}", "x: &x {<<: *y, q: 1}"),
			from:  "x", in: "y", token: "bad",
			want: []string{"@.p"},
		},
		"entry of a sequence of merge sources": {
			input: "x: &x {<<: [{a: 1}, {p: bad}]}\n",
			from:  "x", in: "x", token: "bad",
			want: []string{"@.p"},
		},
		"two aliases of one anchor": {
			input: stringtest.LinesLF("y: &y bad", "x: &x [*y, *y]"),
			from:  "x", in: "y", token: "bad",
			want: []string{"@[0]", "@[1]"},
		},
		// Paths stops at the second place.
		"three aliases of one anchor": {
			input: stringtest.LinesLF("y: &y bad", "x: &x [*y, *y, *y]"),
			from:  "x", in: "y", token: "bad",
			want: []string{"@[0]", "@[1]"},
		},
		"anchors whose aliases repeat each other": {
			input: stringtest.LinesLF("a: &a {k: 1, v: bad}", "b: &b [*a, *a, *a]", "x: &x {p: *b, q: *b}"),
			from:  "x", in: "a", token: "bad",
			want: []string{"@.p[0].v", "@.p[1].v"},
		},
		"anchor that the token of its value names": {
			input: stringtest.LinesLF("a: &a {k: 1}", "b: &b [*a, *a]", "x: &x {p: *b, q: *b}"),
			from:  "x", in: "x", token: "{",
			want: []string{"@"},
		},
		"alias to an anchor without the token": {
			input: stringtest.LinesLF("a: &a {k: 1}", "x: &x {p: *a, q: bad}"),
			from:  "x", in: "x", token: "bad",
			want: []string{"@.q"},
		},
		// The decoder reads an alias inside its own anchor as null, so the
		// path from x through y does not read x again.
		"loop of anchors": {
			input: stringtest.LinesLF("x: &x {a: *y, b: bad}", "y: &y {c: *x}"),
			from:  "x", in: "x", token: "bad",
			want: []string{"@.b"},
		},
		"alias that closes a loop of anchors": {
			input: stringtest.LinesLF("x: &x {a: *y}", "y: &y {c: *x}"),
			from:  "x", in: "y", token: "*",
			want: []string{"@.a.c"},
		},
		"alias key": {
			input: stringtest.LinesLF("k: &k p", "x: &x {*k : bad}"),
			from:  "x", in: "x", token: "bad",
			want: []string{"@.p"},
		},
		"alias inside its own anchor": {
			input: "x: &x {a: *x, b: bad}\n",
			from:  "x", in: "x", token: "bad",
			want: []string{"@.b"},
		},
		"token outside the anchor": {
			input: stringtest.LinesLF("y: &y bad", "x: &x {a: 1}"),
			from:  "x", in: "y", token: "bad",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			table := newTable(t, []string{tc.input})

			from, ok := table.View(tc.from)
			require.True(t, ok, "no anchor %s", tc.from)

			tk := anchorToken(t, table, tc.in, tc.token)

			assert.Equal(t, tc.want, pathTexts(table.Places(tk).Paths(from)))

			// Places that found the token as a merge source first, and so
			// counted its places that way, answer the same.
			places := table.Places(tk)
			places.MergePaths(from)
			assert.Equal(t, tc.want, pathTexts(places.Paths(from)))
		})
	}
}

func TestPlaces_MergePaths(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input string
		// The name of the anchor that a merge key reads.
		from string
		// The name of the anchor that holds the token, and the text of the
		// token. MergePaths finds the first token of that text there.
		in, token string
		want      []string
	}{
		"entry of a mapping": {
			input: "x: &x {a: {b: bad}}\n",
			from:  "x", in: "x", token: "bad",
			want: []string{"@.a.b"},
		},
		// The merge brings in the entries of each element, so the path does
		// not name the index.
		"entry of an element of a sequence": {
			input: "x: &x [{a: 1}, {p: bad}]\n",
			from:  "x", in: "x", token: "bad",
			want: []string{"@.p"},
		},
		"entry of an element through an alias": {
			input: stringtest.LinesLF("y: &y {p: bad}", "x: &x [{a: 1}, *y]"),
			from:  "x", in: "y", token: "bad",
			want: []string{"@.p"},
		},
		"entry that a merge key of the anchor brings in": {
			input: stringtest.LinesLF("y: &y {p: bad}", "x: &x {<<: *y}"),
			from:  "x", in: "y", token: "bad",
			want: []string{"@.p"},
		},
		// The decoder reports an element that is not a mapping by its token.
		"element that is not a mapping": {
			input: "x: &x [{a: 1}, bad]\n",
			from:  "x", in: "x", token: "bad",
			want: []string{"@"},
		},
		"token outside the anchor": {
			input: stringtest.LinesLF("y: &y bad", "x: &x [{a: 1}]"),
			from:  "x", in: "y", token: "bad",
		},
		// MergePaths stops at the second place.
		"entry of each of three elements through an alias": {
			input: stringtest.LinesLF("y: &y {p: bad}", "x: &x [*y, *y, *y]"),
			from:  "x", in: "y", token: "bad",
			want: []string{"@.p", "@.p"},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			table := newTable(t, []string{tc.input})

			from, ok := table.View(tc.from)
			require.True(t, ok, "no anchor %s", tc.from)

			tk := anchorToken(t, table, tc.in, tc.token)

			assert.Equal(t, tc.want, pathTexts(table.Places(tk).MergePaths(from)))

			// Places that found the token as a value first, and so counted
			// its places that way, answer the same.
			places := table.Places(tk)
			places.Paths(from)
			assert.Equal(t, tc.want, pathTexts(places.MergePaths(from)))
		})
	}
}

// pathTexts returns the text of each of ps, or nil when ps is empty.
func pathTexts(ps []paths.Path) []string {
	var texts []string

	for _, p := range ps {
		texts = append(texts, p.String())
	}

	return texts
}

// anchorToken returns the first token with the text text under the anchor
// that table holds for name, as the decoder reads it.
func anchorToken(tb testing.TB, table *reftable.Table, name, text string) *token.Token {
	tb.Helper()

	in, ok := table.View(name)
	require.True(tb, ok, "no anchor %s", name)

	var tk *token.Token

	astnode.Inspect(in, func(n ast.Node) bool {
		if tk == nil && n.GetToken() != nil && n.GetToken().Value == text {
			tk = n.GetToken()
		}

		return tk == nil
	})
	require.NotNil(tb, tk, "no token %q", text)

	return tk
}
