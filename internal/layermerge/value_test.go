package layermerge_test

import (
	"errors"
	"testing"

	"github.com/goccy/go-yaml/ast"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.jacobcolvin.com/x/stringtest"

	"go.jacobcolvin.com/niceyaml/internal/aliasing"
	"go.jacobcolvin.com/niceyaml/internal/layermerge"
	"go.jacobcolvin.com/niceyaml/internal/reftable"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/paths"
)

// errUnnamed stands for the error of a key with no name.
var errUnnamed = errors.New("unnamed key")

// read returns the value a [layermerge.Reader] reads from the document
// of input, as the layer at index layer. The first document of each of
// refs is a reference document of the layer.
func read(tb testing.TB, input string, layer int, refs ...string) (*layermerge.Value, error) {
	tb.Helper()

	doc := yamltest.FirstDocument(tb, input)

	r := layermerge.Reader{
		Ctx:        tb.Context(),
		Resolver:   doc.DocumentResolver(),
		Enclosed:   aliasing.NewFacts(doc.AST(), doc.DocumentResolver()).Enclosed,
		UnnamedKey: errUnnamed,
		Layer:      layer,
	}

	var bodies []ast.Node

	for _, ref := range refs {
		bodies = append(bodies, yamltest.FirstDocument(tb, ref).AST())
	}

	if len(bodies) > 0 {
		r.References = reftable.New(bodies)
	}

	return r.Read(doc.AST())
}

// merge reads each of layers and merges them, lowest first. A sequence
// replaces the sequence below it.
func merge(tb testing.TB, layers ...string) *layermerge.Value {
	tb.Helper()

	return mergeAppending(tb, layermerge.Appends{}, layers...)
}

// mergeAppending reads each of layers and merges them, lowest first, with
// the sequences that appends names.
func mergeAppending(tb testing.TB, appends layermerge.Appends, layers ...string) *layermerge.Value {
	tb.Helper()

	var root *layermerge.Value

	for i, input := range layers {
		v, err := read(tb, input, i)
		require.NoError(tb, err)

		root = root.Merge(v, appends)
	}

	return root
}

func TestValue_Merge(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		want    string
		layers  []string
		appends []paths.Path
	}{
		"mappings merge key by key": {
			layers: []string{
				stringtest.LinesLF("a: 1", "b: {c: 2, d: 3}"),
				stringtest.LinesLF("b: {d: 4}", "e: 5"),
			},
			want: stringtest.LinesLF("a: 1", "b:", "  c: 2", "  d: 4", "e: 5"),
		},
		"a written null removes its key": {
			layers: []string{stringtest.LinesLF("a: 1", "b: 2"), "a: null\n"},
			want:   "b: 2\n",
		},
		"a written null adds nothing where the layer below lacks its key": {
			layers: []string{"a: 1\n", stringtest.LinesLF("b: null", "c: !!null ~")},
			want:   "a: 1\n",
		},
		"a written null removes a written null of the lowest layer": {
			layers: []string{stringtest.LinesLF("a: 1", "b: ~"), "b: null\n"},
			want:   "a: 1\n",
		},
		"a written null removes a null with no text of the lowest layer": {
			layers: []string{"b:\n", "b: null\n"},
			want:   "{}\n",
		},
		"a written null removes a null with no text of a layer between": {
			layers: []string{"a: 1\n", "b:\n", "b: ~\n"},
			want:   "a: 1\n",
		},
		"a written null adds nothing over an empty mapping": {
			layers: []string{"{}\n", stringtest.LinesLF("a: ~", "b: {c: null}")},
			want:   "b: {}\n",
		},
		"a written null adds nothing over a null at the root": {
			layers: []string{"~\n", stringtest.LinesLF("a: ~", "b: 1")},
			want:   "b: 1\n",
		},
		"a null with no text keeps out the key a written null did not add": {
			layers: []string{"a: 1\n", "b: ~\n", "b:\n"},
			want:   "a: 1\n",
		},
		"the lowest layer keeps its nulls": {
			layers: []string{stringtest.LinesLF("a: null", "b: {c: ~}"), "d: 1\n"},
			want:   stringtest.LinesLF("a: null", "b:", "  c: ~", "d: 1"),
		},
		"a written null adds nothing inside a mapping the merge takes whole": {
			layers: []string{
				stringtest.LinesLF("a: 1", "b: [x]", "d:"),
				stringtest.LinesLF("a: {k: ~, l: 2}", "b: {k: ~}", "c: {k: {m: ~}}", "d: {k: ~}"),
			},
			want: stringtest.LinesLF("a:", "  l: 2", "b: {}", "d: {}", "c:", "  k: {}"),
		},
		"a written null stays inside a sequence": {
			layers: []string{"a: 1\n", "b: [{k: ~}, ~]\n"},
			want:   stringtest.LinesLF("a: 1", "b:", "  - k: ~", "  - ~"),
		},
		"a null with no text stays where the layer below lacks its key": {
			layers: []string{"a: 1\n", "b:\n"},
			want:   stringtest.LinesLF("a: 1", "b:"),
		},
		"a null with no text keeps the value below": {
			layers: []string{"a: 1\n", "a:\n"},
			want:   "a: 1\n",
		},
		"a null a tag converts keeps the value below": {
			layers: []string{"a: 1\n", "a: !!str\n"},
			want:   "a: 1\n",
		},
		"a null under the null tag removes its key": {
			layers: []string{stringtest.LinesLF("a: 1", "b: 2"), "a: !!null null\n"},
			want:   "b: 2\n",
		},
		"a sequence replaces": {
			layers: []string{"a: [1, 2]\n", "a: [3]\n"},
			want:   stringtest.LinesLF("a:", "  - 3"),
		},
		"a sequence appends at a path that names it": {
			layers:  []string{"a: [1, 2]\n", "a: [3]\n", "a: [4]\n"},
			appends: []paths.Path{paths.Current().Child("a")},
			want:    stringtest.LinesLF("a:", "  - 1", "  - 2", "  - 3", "  - 4"),
		},
		"a sequence replaces at a path that names another": {
			layers:  []string{stringtest.LinesLF("a: [1]", "b: {a: [1]}"), stringtest.LinesLF("a: [2]", "b: {a: [2]}")},
			appends: []paths.Path{paths.Current().Child("b", "a")},
			want:    stringtest.LinesLF("a:", "  - 2", "b:", "  a:", "    - 1", "    - 2"),
		},
		"a wildcard names the sequence under every key": {
			layers: []string{
				stringtest.LinesLF("web: {ports: [80]}", "db: {ports: [5432]}"),
				stringtest.LinesLF("web: {ports: [443]}", "db: {ports: [5433]}"),
			},
			appends: []paths.Path{paths.Current().ChildAll().Child("ports")},
			want: stringtest.LinesLF(
				"web:", "  ports:", "    - 80", "    - 443",
				"db:", "  ports:", "    - 5432", "    - 5433",
			),
		},
		"a sequence at the root appends": {
			layers:  []string{"[1]\n", "[2]\n"},
			appends: []paths.Path{paths.Current()},
			want:    stringtest.LinesLF("- 1", "- 2"),
		},
		"an empty sequence keeps the elements below a sequence that appends": {
			layers:  []string{"a: [1]\n", "a: []\n"},
			appends: []paths.Path{paths.Current().Child("a")},
			want:    stringtest.LinesLF("a:", "  - 1"),
		},
		"a written null removes a sequence that appends": {
			layers:  []string{"a: [1]\n", "a: ~\n", "a: [2]\n", "a: [3]\n"},
			appends: []paths.Path{paths.Current().Child("a")},
			want:    stringtest.LinesLF("a:", "  - 2", "  - 3"),
		},
		"a sequence that appends keeps each null and each element twice": {
			layers:  []string{"a: [1, ~]\n", "a: [1, {k: ~}]\n"},
			appends: []paths.Path{paths.Current().Child("a")},
			want:    stringtest.LinesLF("a:", "  - 1", "  - ~", "  - 1", "  - k: ~"),
		},
		"a sequence that appends replaces a scalar and a mapping": {
			layers:  []string{stringtest.LinesLF("a: x", "b: {k: 1}"), stringtest.LinesLF("a: [1]", "b: [2]")},
			appends: []paths.Path{paths.Current().ChildAll()},
			want:    stringtest.LinesLF("a:", "  - 1", "b:", "  - 2"),
		},
		"a scalar replaces a sequence that appends": {
			layers:  []string{"a: [1]\n", "a: x\n"},
			appends: []paths.Path{paths.Current().Child("a")},
			want:    "a: x\n",
		},
		"a path that names a mapping leaves the sequences below it to replace": {
			layers:  []string{"a: {b: [1]}\n", "a: {b: [2]}\n"},
			appends: []paths.Path{paths.Current().Child("a")},
			want:    stringtest.LinesLF("a:", "  b:", "    - 2"),
		},
		"a sequence inside an element never appends": {
			layers:  []string{"a: [{b: [1]}]\n", "a: [{b: [2]}]\n"},
			appends: []paths.Path{paths.Current().Child("a").Index(0).Child("b"), paths.Current().Child("a", "b")},
			want:    stringtest.LinesLF("a:", "  - b:", "      - 2"),
		},
		"a sequence that appends takes the tag of the higher layer": {
			layers:  []string{"a: !x [1]\n", "a: !y [2]\n", "a: [3]\n"},
			appends: []paths.Path{paths.Current().Child("a")},
			want:    stringtest.LinesLF("a: !y", "  - 1", "  - 2", "  - 3"),
		},
		"a scalar replaces a mapping": {
			layers: []string{"a: {b: 1}\n", "a: x\n"},
			want:   "a: x\n",
		},
		"a value takes the place of a removed key": {
			layers: []string{"a: {b: 1}\n", "a: ~\n", "a: {c: 2, d: ~}\n"},
			want:   stringtest.LinesLF("a:", "  c: 2"),
		},
		"aliases and merge keys resolve": {
			layers: []string{stringtest.LinesLF("d: &d {x: 1}", "e: {<<: *d, y: 2}", "f: *d")},
			want: stringtest.LinesLF(
				"d:", "  x: 1",
				"e:", "  x: 1", "  y: 2",
				"f:", "  x: 1",
			),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			root := mergeAppending(t, layermerge.NewAppends(tc.appends...), tc.layers...)

			assert.Equal(t, tc.want, layermerge.Text("", root))
		})
	}
}

func TestValue_LayerPath(t *testing.T) {
	t.Parallel()

	var (
		list  = paths.Current().Child("list")
		plain = paths.Current().Child("plain")
	)

	// The layers append to list and replace plain. The middle layer holds
	// a sequence inside its one element of list.
	root := mergeAppending(t, layermerge.NewAppends(list),
		stringtest.LinesLF("list: [a, b]", "plain: [x, y, z]"),
		stringtest.LinesLF("list: [{k: [c, d]}]"),
		stringtest.LinesLF("list: [e, f]", "plain: [p, q]"),
	)

	tcs := map[string]struct {
		path  paths.Path
		want  string
		layer int
	}{
		"an element of the lowest layer": {path: list.Index(1), layer: 0, want: "@.list[1]"},
		"an element of the middle layer": {path: list.Index(2), layer: 1, want: "@.list[0]"},
		"a value inside that element": {
			path:  list.Index(2).Child("k").Index(1),
			layer: 1,
			want:  "@.list[0].k[1]",
		},
		"a key inside that element":         {path: list.Index(2).Child("k").Key(), layer: 1, want: "@.list[0].k~"},
		"a key that the element lacks":      {path: list.Index(2).Child("z", "y"), layer: 1, want: "@.list[0].z.y"},
		"an element of the highest layer":   {path: list.Index(4), layer: 2, want: "@.list[1]"},
		"an element of another layer":       {path: list.Index(4), layer: 0, want: "@.list[4]"},
		"an index past the sequence":        {path: list.Index(9), layer: 2, want: "@.list[9]"},
		"the sequence itself":               {path: list, layer: 2, want: "@.list"},
		"an element of a replaced sequence": {path: plain.Index(1), layer: 2, want: "@.plain[1]"},
		"a wildcard":                        {path: list.IndexAll().Child("k"), layer: 1, want: "@.list[*].k"},
		"a wildcard for a name":             {path: paths.Current().ChildAll().Child("k"), layer: 1, want: "@.*.k"},
		"a recursive name":                  {path: paths.Current().Recursive("k"), layer: 1, want: "@..k"},
		"a recursive wildcard":              {path: paths.Current().RecursiveAll(), layer: 1, want: "@..*"},
		"an index behind a recursive name": {
			path:  paths.Current().Recursive("list").Index(2),
			layer: 1,
			want:  "@..list[2]",
		},
		"the root": {path: paths.Current(), layer: 2, want: "@"},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, root.LayerPath(tc.path, tc.layer).String())
		})
	}

	// Each element names its own layer, and the sequence names the highest.
	for i, layer := range []int{0, 0, 1, 2, 2} {
		v, found := root.Find(list.Index(i))
		require.True(t, found)
		assert.Equal(t, layer, v.Layer)
	}

	v, found := root.Find(list)
	require.True(t, found)
	assert.Equal(t, 2, v.Layer)
}

func TestValue_Element(t *testing.T) {
	t.Parallel()

	var (
		list  = paths.Current().Child("list")
		plain = paths.Current().Child("plain")
		only  = paths.Current().Child("svc", "only")
	)

	appends := layermerge.NewAppends(list, paths.Current().Child("svc").ChildAll())

	// The layers append to list and to each sequence of svc, and they
	// replace plain. The middle layer alone holds the sequence at only,
	// and it holds a sequence inside its one element of list. The highest
	// layer holds a sequence under the name list inside the last element
	// of plain.
	root := mergeAppending(t, appends,
		stringtest.LinesLF("list: [a, b]", "plain: [x, y, z]"),
		stringtest.LinesLF("list: [{k: [c, d]}]", "svc: {only: [m]}"),
		stringtest.LinesLF("list: [e, f]", "plain: [p, q, {list: [r]}]", "svc: {name: n}"),
	)

	// Each case names the element that Element returns by its path, or
	// leaves want nil when Element does not return one.
	tcs := map[string]struct {
		want    *paths.Path
		path    paths.Path
		appends layermerge.Appends
		layer   int
	}{
		"an element of the lowest layer": {
			path: list.Index(1), appends: appends, want: new(list.Index(1)), layer: 0,
		},
		"an element of the highest layer": {
			path: list.Index(4), appends: appends, want: new(list.Index(4)), layer: 2,
		},
		"a value inside an element": {
			path: list.Index(2).Child("k").Index(1), appends: appends, want: new(list.Index(2)), layer: 1,
		},
		"a key that the element lacks": {
			path: list.Index(2).Child("z", "y"), appends: appends, want: new(list.Index(2)), layer: 1,
		},
		"an element of a sequence that one layer holds": {
			path: only.Index(0), appends: appends, want: new(only.Index(0)), layer: 1,
		},
		"the sequence itself":               {path: list, appends: appends},
		"the key of the sequence":           {path: list.Key(), appends: appends},
		"an index past the sequence":        {path: list.Index(9), appends: appends},
		"an element of a replaced sequence": {path: plain.Index(1), appends: appends},
		"a wildcard":                        {path: list.IndexAll().Child("k"), appends: appends},
		"a sequence inside an element of a replaced sequence": {
			path:    plain.Index(2).Child("list").Index(0),
			appends: layermerge.NewAppends(plain.Child("list")),
		},
		"a scalar under a name that appends": {
			path: paths.Current().Child("svc", "name").Index(0), appends: appends,
		},
		"the root":             {path: paths.Current(), appends: appends},
		"no sequence appends":  {path: list.Index(1)},
		"another path appends": {path: list.Index(1), appends: layermerge.NewAppends(plain.Child("list"))},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := root.Element(tc.path, tc.appends)

			if tc.want == nil {
				assert.Nil(t, got)

				return
			}

			want, found := root.Find(*tc.want)
			require.True(t, found)
			assert.Same(t, want, got)
			assert.Equal(t, tc.layer, got.Layer)
		})
	}

	var none *layermerge.Value

	assert.Nil(t, none.Element(list.Index(0), appends))
}

func TestValue_Find(t *testing.T) {
	t.Parallel()

	root := merge(t, stringtest.LinesLF("a: {b: 1, c: [x]}", "d: 2"), stringtest.LinesLF("a: {b: 3}", "d: null"))

	tcs := map[string]struct {
		path  paths.Path
		layer int
		found bool
	}{
		"the root":               {path: paths.Current(), layer: 1, found: true},
		"a value of the top":     {path: paths.Current().Child("a", "b"), layer: 1, found: true},
		"a value of the base":    {path: paths.Current().Child("a", "c").Index(0), found: true},
		"a removed key":          {path: paths.Current().Child("d"), layer: 1, found: true},
		"a key no layer holds":   {path: paths.Current().Child("a", "z"), layer: 1},
		"an index past the list": {path: paths.Current().Child("a", "c").Index(4)},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			v, found := root.Find(tc.path)
			assert.Equal(t, tc.found, found)
			require.NotNil(t, v)
			assert.Equal(t, tc.layer, v.Layer)
		})
	}

	var none *layermerge.Value

	v, found := none.Find(paths.Current())
	assert.Nil(t, v)
	assert.False(t, found)
}

func TestValue_Carry(t *testing.T) {
	t.Parallel()

	// The merged layers remove b, which the text of their merge leaves
	// out, so a layer that reads that text carries the removal.
	held := merge(t, stringtest.LinesLF("a: 1", "b: 2", "c: 3"), "b: null\n")

	v, err := read(t, layermerge.Text("", held), 2)
	require.NoError(t, err)

	v.Carry(held, 2)

	removed, found := v.Find(paths.Current().Child("b"))
	require.True(t, found)
	assert.Equal(t, 2, removed.Layer)

	// Over a lower layer that holds b, the carried entry removes it.
	assert.Equal(
		t,
		stringtest.LinesLF("a: 1", "c: 3"),
		layermerge.Text("", merge(t, "b: 9\n").Merge(v, layermerge.Appends{})),
	)
}
