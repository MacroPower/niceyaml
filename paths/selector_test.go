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

func TestSelector_String(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		want string
		sel  paths.Selector
	}{
		"child": {
			sel:  paths.Selector{Kind: paths.SelectorChild, Name: "name"},
			want: ".name",
		},
		"child with the empty name": {
			sel:  paths.Selector{Kind: paths.SelectorChild},
			want: ".''",
		},
		"child with a reserved character": {
			sel:  paths.Selector{Kind: paths.SelectorChild, Name: "a.b"},
			want: ".'a.b'",
		},
		"child with a space": {
			sel:  paths.Selector{Kind: paths.SelectorChild, Name: "my job"},
			want: ".'my job'",
		},
		"child named star": {
			sel:  paths.Selector{Kind: paths.SelectorChild, Name: "*"},
			want: ".'*'",
		},
		"index": {
			sel:  paths.Selector{Kind: paths.SelectorIndex, Index: 3},
			want: "[3]",
		},
		"index zero": {
			sel:  paths.Selector{Kind: paths.SelectorIndex},
			want: "[0]",
		},
		"mapping wildcard": {
			sel:  paths.Selector{Kind: paths.SelectorChildAll},
			want: ".*",
		},
		"sequence wildcard": {
			sel:  paths.Selector{Kind: paths.SelectorIndexAll},
			want: "[*]",
		},
		"recursive": {
			sel:  paths.Selector{Kind: paths.SelectorRecursive, Name: "name"},
			want: "..name",
		},
		"recursive with a reserved character": {
			sel:  paths.Selector{Kind: paths.SelectorRecursive, Name: "a.b"},
			want: "..'a.b'",
		},
		"recursive wildcard": {
			sel:  paths.Selector{Kind: paths.SelectorRecursiveAll},
			want: "..*",
		},
		"key": {
			sel:  paths.Selector{Kind: paths.SelectorKey},
			want: "~",
		},
		"zero selector": {
			want: "",
		},
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
		"root": {
			expr: "$",
		},
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
			var sb strings.Builder

			sb.WriteString("$")

			for _, sel := range got {
				sb.WriteString(sel.String())
			}

			assert.Equal(t, p.String(), sb.String())

			// The builders turn the selectors back into an equal path.
			rebuilt := buildPath(t, p.Selectors())
			assert.True(t, rebuilt.Equal(p), "rebuilt %s from %s", rebuilt, p)
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

	t.Run("builders", func(t *testing.T) {
		t.Parallel()

		p := paths.Doc().Child("spec", "a.b").Index(0, 2).ChildAll().IndexAll().Recursive("").RecursiveAll().Key()

		assert.Equal(t, []paths.Selector{
			{Kind: paths.SelectorChild, Name: "spec"},
			{Kind: paths.SelectorChild, Name: "a.b"},
			{Kind: paths.SelectorIndex},
			{Kind: paths.SelectorIndex, Index: 2},
			{Kind: paths.SelectorChildAll},
			{Kind: paths.SelectorIndexAll},
			{Kind: paths.SelectorRecursive},
			{Kind: paths.SelectorRecursiveAll},
			{Kind: paths.SelectorKey},
		}, slices.Collect(p.Selectors()))
		assert.True(t, buildPath(t, p.Selectors()).Equal(p))
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

func TestPath_Last(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		p    paths.Path
		want paths.Selector
		ok   bool
	}{
		"root": {
			p: paths.Doc(),
		},
		"zero value": {},
		"child": {
			p:    paths.Doc().Child("spec", "replicas"),
			want: paths.Selector{Kind: paths.SelectorChild, Name: "replicas"},
			ok:   true,
		},
		"empty name": {
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

	t.Run("empty name differs from the zero selector", func(t *testing.T) {
		t.Parallel()

		got, ok := paths.MustParse("$.''").Last()
		require.True(t, ok)
		assert.NotEqual(t, paths.Selector{}, got)
		assert.Equal(t, paths.SelectorChild, got.Kind)
		assert.Empty(t, got.Name)
	})

	t.Run("name of a key through the parent", func(t *testing.T) {
		t.Parallel()

		parent, ok := paths.MustParse("$.jobs.build~").Parent()
		require.True(t, ok)

		got, ok := parent.Last()
		require.True(t, ok)
		assert.Equal(t, paths.Selector{Kind: paths.SelectorChild, Name: "build"}, got)
	})
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

			matches, err := doc.Resolver().Matches(paths.MustParse(tc.expr))
			require.NoError(t, err)

			got := make([]paths.Selector, 0, len(matches))

			for _, m := range matches {
				sel, ok := m.Path.Last()
				require.True(t, ok)

				got = append(got, sel)

				// The name of each match selects the node again.
				node, err := doc.Resolver().Node(m.Path)
				require.NoError(t, err)
				assert.Same(t, m.Node, node)
			}

			assert.Equal(t, tc.want, got)
		})
	}
}
