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
		Resolver:   doc.Resolver(),
		Enclosed:   aliasing.NewFacts(doc.AST(), doc.Resolver()).Enclosed,
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

// merge reads each of layers and merges them, lowest first.
func merge(tb testing.TB, layers ...string) *layermerge.Value {
	tb.Helper()

	var root *layermerge.Value

	for i, input := range layers {
		v, err := read(tb, input, i)
		require.NoError(tb, err)

		root = root.Merge(v)
	}

	return root
}

func TestValue_Merge(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		layers []string
		want   string
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
		"a scalar replaces a mapping": {
			layers: []string{"a: {b: 1}\n", "a: x\n"},
			want:   "a: x\n",
		},
		"a value takes the place of a removed key": {
			layers: []string{"a: {b: 1}\n", "a: ~\n", "a: {c: 2}\n"},
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

			assert.Equal(t, tc.want, layermerge.Text("", merge(t, tc.layers...)))
		})
	}
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
	assert.Equal(t, stringtest.LinesLF("a: 1", "c: 3"), layermerge.Text("", merge(t, "b: 9\n").Merge(v)))
}
