package paths_test

import (
	"iter"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.jacobcolvin.com/x/stringtest"

	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/paths"
)

func TestSelectorKind_String(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		want string
		kind paths.SelectorKind
	}{
		"child":         {kind: paths.SelectorChild, want: "child"},
		"index":         {kind: paths.SelectorIndex, want: "index"},
		"child all":     {kind: paths.SelectorChildAll, want: "child-all"},
		"index all":     {kind: paths.SelectorIndexAll, want: "index-all"},
		"recursive":     {kind: paths.SelectorRecursive, want: "recursive"},
		"recursive all": {kind: paths.SelectorRecursiveAll, want: "recursive-all"},
		"key":           {kind: paths.SelectorKey, want: "key"},
		"zero":          {kind: paths.SelectorKind(0), want: "SelectorKind(0)"},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, tc.kind.String())
		})
	}
}

func TestSelector_String(t *testing.T) {
	t.Parallel()

	// TestPath_Selectors checks the text of the selectors of each kind.
	tcs := map[string]struct {
		want string
		sel  paths.Selector
	}{
		"child with a space": {
			sel:  paths.Selector{Kind: paths.SelectorChild, Name: "my job"},
			want: ".'my job'",
		},
		"zero selector": {want: ""},
		"unknown kind": {
			sel:  paths.Selector{Kind: paths.SelectorKind(-1), Name: "a"},
			want: "",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, tc.sel.String())
		})
	}
}

func TestPath_Selectors(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		expr string
		want []paths.Selector
	}{
		"root": {expr: "$"},
		"child": {
			expr: "$.metadata.name",
			want: []paths.Selector{
				{Kind: paths.SelectorChild, Name: "metadata"},
				{Kind: paths.SelectorChild, Name: "name"},
			},
		},
		"empty name": {
			expr: "$.''",
			want: []paths.Selector{{Kind: paths.SelectorChild}},
		},
		"quoted name": {
			expr: `$.'a.b'.'c~d'.'it\'s'`,
			want: []paths.Selector{
				{Kind: paths.SelectorChild, Name: "a.b"},
				{Kind: paths.SelectorChild, Name: "c~d"},
				{Kind: paths.SelectorChild, Name: "it's"},
			},
		},
		"name a decoder respells": {
			expr: "$.ports.0x10",
			want: []paths.Selector{
				{Kind: paths.SelectorChild, Name: "ports"},
				{Kind: paths.SelectorChild, Name: "0x10"},
			},
		},
		"name star": {
			expr: "$.'*'",
			want: []paths.Selector{{Kind: paths.SelectorChild, Name: "*"}},
		},
		"index": {
			expr: "$.items[0][12]",
			want: []paths.Selector{
				{Kind: paths.SelectorChild, Name: "items"},
				{Kind: paths.SelectorIndex},
				{Kind: paths.SelectorIndex, Index: 12},
			},
		},
		"sequence wildcard": {
			expr: "$.items[*].name",
			want: []paths.Selector{
				{Kind: paths.SelectorChild, Name: "items"},
				{Kind: paths.SelectorIndexAll},
				{Kind: paths.SelectorChild, Name: "name"},
			},
		},
		"mapping wildcard": {
			expr: "$.jobs.*.steps",
			want: []paths.Selector{
				{Kind: paths.SelectorChild, Name: "jobs"},
				{Kind: paths.SelectorChildAll},
				{Kind: paths.SelectorChild, Name: "steps"},
			},
		},
		"recursive": {
			expr: "$..name",
			want: []paths.Selector{{Kind: paths.SelectorRecursive, Name: "name"}},
		},
		"quoted recursive": {
			expr: "$..'x y'",
			want: []paths.Selector{{Kind: paths.SelectorRecursive, Name: "x y"}},
		},
		"empty recursive name": {
			expr: "$..''",
			want: []paths.Selector{{Kind: paths.SelectorRecursive}},
		},
		"recursive wildcard": {
			expr: "$.spec..*.name",
			want: []paths.Selector{
				{Kind: paths.SelectorChild, Name: "spec"},
				{Kind: paths.SelectorRecursiveAll},
				{Kind: paths.SelectorChild, Name: "name"},
			},
		},
		"recursive name star": {
			expr: "$..'*'",
			want: []paths.Selector{{Kind: paths.SelectorRecursive, Name: "*"}},
		},
		"key": {
			expr: "$.jobs.build~",
			want: []paths.Selector{
				{Kind: paths.SelectorChild, Name: "jobs"},
				{Kind: paths.SelectorChild, Name: "build"},
				{Kind: paths.SelectorKey},
			},
		},
		"key in the middle": {
			expr: "$.a~.b",
			want: []paths.Selector{
				{Kind: paths.SelectorChild, Name: "a"},
				{Kind: paths.SelectorKey},
				{Kind: paths.SelectorChild, Name: "b"},
			},
		},
		"key at the root": {
			expr: "$~",
			want: []paths.Selector{{Kind: paths.SelectorKey}},
		},
		"every kind": {
			expr: "$.a.*[1][*]..b..*~",
			want: []paths.Selector{
				{Kind: paths.SelectorChild, Name: "a"},
				{Kind: paths.SelectorChildAll},
				{Kind: paths.SelectorIndex, Index: 1},
				{Kind: paths.SelectorIndexAll},
				{Kind: paths.SelectorRecursive, Name: "b"},
				{Kind: paths.SelectorRecursiveAll},
				{Kind: paths.SelectorKey},
			},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			p := paths.MustParse(tc.expr)

			got := slices.Collect(p.Selectors())
			assert.Equal(t, tc.want, got)
			assert.Equal(t, len(tc.want), p.Len())

			// The selectors print the path expression after its root.
			texts := []string{"$"}
			for _, sel := range got {
				texts = append(texts, sel.String())
			}

			assert.Equal(t, tc.expr, strings.Join(texts, ""))
			assert.Equal(t, p.String(), strings.Join(texts, ""))

			// The builders turn the selectors back into an equal path,
			// which holds the same selectors.
			rebuilt := buildPath(t, p.Selectors())
			assert.True(t, rebuilt.Equal(p), "rebuilt %s from %s", rebuilt, p)
			assert.Equal(t, got, slices.Collect(rebuilt.Selectors()))

			// Append turns the selectors back into an equal path too.
			appended := p.Prefix(0).Append(got...)
			assert.True(t, appended.Equal(p), "appended %s from %s", appended, p)
		})
	}

	t.Run("stops when the caller stops", func(t *testing.T) {
		t.Parallel()

		var got []paths.Selector

		for sel := range paths.MustParse("$.a.b.c").Selectors() {
			got = append(got, sel)
			if sel.Name == "b" {
				break
			}
		}

		assert.Equal(t, []paths.Selector{
			{Kind: paths.SelectorChild, Name: "a"},
			{Kind: paths.SelectorChild, Name: "b"},
		}, got)
	})
}

// buildPath appends each of sels to the root with the builder for its kind.
func buildPath(t *testing.T, sels iter.Seq[paths.Selector]) paths.Path {
	t.Helper()

	p := paths.Doc()

	for sel := range sels {
		switch sel.Kind {
		case paths.SelectorChild:
			p = p.Child(sel.Name)
		case paths.SelectorIndex:
			p = p.Index(sel.Index)
		case paths.SelectorChildAll:
			p = p.ChildAll()
		case paths.SelectorIndexAll:
			p = p.IndexAll()
		case paths.SelectorRecursive:
			p = p.Recursive(sel.Name)
		case paths.SelectorRecursiveAll:
			p = p.RecursiveAll()
		case paths.SelectorKey:
			p = p.Key()
		default:
			require.Failf(t, "unknown selector kind", "kind %d", sel.Kind)
		}
	}

	return p
}

func TestPath_Append(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		p    paths.Path
		want paths.Path
		sels []paths.Selector
	}{
		"no selectors": {
			p:    paths.Doc().Child("a"),
			want: paths.Doc().Child("a"),
		},
		"onto the root": {
			p: paths.Doc(),
			sels: []paths.Selector{
				{Kind: paths.SelectorChild, Name: "items"},
				{Kind: paths.SelectorIndex, Index: 3},
				{Kind: paths.SelectorKey},
			},
			want: paths.Doc().Child("items").Index(3).Key(),
		},
		"keeps the current node": {
			p:    paths.Current().Child("spec"),
			sels: []paths.Selector{{Kind: paths.SelectorChild, Name: "replicas"}},
			want: paths.Current().Child("spec", "replicas"),
		},
		"every kind": {
			p: paths.Doc(),
			sels: []paths.Selector{
				{Kind: paths.SelectorChild, Name: "a"},
				{Kind: paths.SelectorIndex, Index: 0},
				{Kind: paths.SelectorChildAll},
				{Kind: paths.SelectorIndexAll},
				{Kind: paths.SelectorRecursive, Name: "b"},
				{Kind: paths.SelectorRecursiveAll},
				{Kind: paths.SelectorKey},
			},
			want: paths.Doc().Child("a").Index(0).ChildAll().IndexAll().Recursive("b").RecursiveAll().Key(),
		},
		"ignores fields the kind does not hold": {
			p: paths.Doc(),
			sels: []paths.Selector{
				{Kind: paths.SelectorChild, Name: "a", Index: 5},
				{Kind: paths.SelectorIndex, Name: "b", Index: 1},
				{Kind: paths.SelectorIndexAll, Name: "c", Index: 2},
				{Kind: paths.SelectorChildAll, Name: "d", Index: -1},
				{Kind: paths.SelectorRecursiveAll, Name: "e"},
				{Kind: paths.SelectorKey, Name: "f", Index: 3},
			},
			want: paths.Doc().Child("a").Index(1).IndexAll().ChildAll().RecursiveAll().Key(),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := tc.p.Append(tc.sels...)
			assert.True(t, got.Equal(tc.want), "got %s, want %s", got, tc.want)
			assert.Equal(t, tc.want.String(), got.String())

			// The appended path holds the selectors as the builders store
			// them, so it starts with the path it extends.
			rest, ok := got.CutPrefix(tc.p)
			require.True(t, ok)
			assert.Equal(t, len(tc.sels), rest.Len())
		})
	}

	t.Run("leaves the receiver as it was", func(t *testing.T) {
		t.Parallel()

		base := paths.Doc().Child("a")
		x := base.Append(paths.Selector{Kind: paths.SelectorChild, Name: "x"})
		y := base.Append(paths.Selector{Kind: paths.SelectorChild, Name: "y"})

		assert.Equal(t, "$.a", base.String())
		assert.Equal(t, "$.a.x", x.String())
		assert.Equal(t, "$.a.y", y.String())
	})
}

func TestPath_Append_Panics(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		want string
		sels []paths.Selector
	}{
		"zero selector": {
			sels: []paths.Selector{{}},
			want: "paths.Path.Append: unknown selector kind SelectorKind(0)",
		},
		"unknown kind after a valid selector": {
			sels: []paths.Selector{
				{Kind: paths.SelectorChild, Name: "a"},
				{Kind: paths.SelectorKind(99)},
			},
			want: "paths.Path.Append: unknown selector kind SelectorKind(99)",
		},
		"negative index": {
			sels: []paths.Selector{{Kind: paths.SelectorIndex, Index: -1}},
			want: "paths.Path.Append: index -1 is below 0",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.PanicsWithValue(t, tc.want, func() {
				paths.Doc().Append(tc.sels...)
			})
		})
	}
}

func TestPath_Last(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		p    paths.Path
		want paths.Selector
		ok   bool
	}{
		"root":       {p: paths.Doc()},
		"zero value": {},
		"child": {
			p:    paths.Doc().Child("spec", "replicas"),
			want: paths.Selector{Kind: paths.SelectorChild, Name: "replicas"},
			ok:   true,
		},
		"empty name": {
			// The selector differs from the zero selector, which has no
			// kind.
			p:    paths.Doc().Child(""),
			want: paths.Selector{Kind: paths.SelectorChild},
			ok:   true,
		},
		"index": {
			p:    paths.Doc().Child("items").Index(2),
			want: paths.Selector{Kind: paths.SelectorIndex, Index: 2},
			ok:   true,
		},
		"mapping wildcard": {
			p:    paths.Doc().Child("jobs").ChildAll(),
			want: paths.Selector{Kind: paths.SelectorChildAll},
			ok:   true,
		},
		"sequence wildcard": {
			p:    paths.Doc().Child("items").IndexAll(),
			want: paths.Selector{Kind: paths.SelectorIndexAll},
			ok:   true,
		},
		"recursive": {
			p:    paths.Doc().Recursive("name"),
			want: paths.Selector{Kind: paths.SelectorRecursive, Name: "name"},
			ok:   true,
		},
		"recursive wildcard": {
			p:    paths.Doc().RecursiveAll(),
			want: paths.Selector{Kind: paths.SelectorRecursiveAll},
			ok:   true,
		},
		"key": {
			p:    paths.Doc().Child("jobs", "build").Key(),
			want: paths.Selector{Kind: paths.SelectorKey},
			ok:   true,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, ok := tc.p.Last()
			assert.Equal(t, tc.ok, ok)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestPath_Last_Matches(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input string
		expr  string
		want  []paths.Selector
	}{
		"key names": {
			input: stringtest.JoinLF(
				"ports:",
				"  0x10: a",
				"  True: b",
				"  ~: c",
				"  'q x': d",
				`  "e\tf": e`,
				"  3.10: f",
				"  !!str 7: g",
			),
			expr: "$.ports.*",
			want: []paths.Selector{
				{Kind: paths.SelectorChild, Name: "0x10"},
				{Kind: paths.SelectorChild, Name: "True"},
				{Kind: paths.SelectorChild, Name: "~"},
				{Kind: paths.SelectorChild, Name: "q x"},
				{Kind: paths.SelectorChild, Name: "e\tf"},
				{Kind: paths.SelectorChild, Name: "3.10"},
				{Kind: paths.SelectorChild, Name: "7"},
			},
		},
		"alias key": {
			input: stringtest.JoinLF(
				"name: &k build",
				"jobs:",
				"  *k : {}",
			),
			expr: "$.jobs.*",
			want: []paths.Selector{{Kind: paths.SelectorChild, Name: "build"}},
		},
		"block scalar key": {
			input: stringtest.JoinLF(
				"jobs:",
				"  ? |-",
				"    build",
				"  : {}",
			),
			expr: "$.jobs.*",
			want: []paths.Selector{{Kind: paths.SelectorChild, Name: "build"}},
		},
		"sequence elements": {
			input: stringtest.JoinLF(
				"items:",
				"  - a",
				"  - b",
			),
			expr: "$.items[*]",
			want: []paths.Selector{
				{Kind: paths.SelectorIndex},
				{Kind: paths.SelectorIndex, Index: 1},
			},
		},
		"recursive": {
			input: stringtest.JoinLF(
				"spec:",
				"  containers:",
				"    - name: app",
			),
			expr: "$..name",
			want: []paths.Selector{{Kind: paths.SelectorChild, Name: "name"}},
		},
		"recursive wildcard": {
			input: stringtest.JoinLF(
				"spec:",
				"  containers:",
				"    - name: app",
			),
			expr: "$..*",
			want: []paths.Selector{
				{Kind: paths.SelectorChild, Name: "spec"},
				{Kind: paths.SelectorChild, Name: "containers"},
				{Kind: paths.SelectorIndex},
				{Kind: paths.SelectorChild, Name: "name"},
			},
		},
		"keys": {
			input: stringtest.JoinLF(
				"jobs:",
				"  build: {}",
			),
			expr: "$.jobs.*~",
			want: []paths.Selector{{Kind: paths.SelectorKey}},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			doc := yamltest.FirstDocument(t, tc.input)

			matches, err := doc.DocumentResolver().Matches(paths.MustParse(tc.expr))
			require.NoError(t, err)

			got := make([]paths.Selector, 0, len(matches))

			for _, m := range matches {
				sel, ok := m.Path.Last()
				require.True(t, ok)

				got = append(got, sel)

				// The name of each match selects the node again.
				node, err := doc.DocumentResolver().Node(m.Path)
				require.NoError(t, err)
				assert.Same(t, m.Node, node)
			}

			assert.Equal(t, tc.want, got)
		})
	}
}
