package paths_test

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/token"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.jacobcolvin.com/x/stringtest"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/paths"
)

func TestResolver_Node(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input string
		path  string
		want  string
		err   error
	}{
		"plain child": {
			input: "a:\n  b: x\n",
			path:  "$.a.b",
			want:  "x",
		},
		"index": {
			input: "a: [x, y]\n",
			path:  "$.a[1]",
			want:  "y",
		},
		"alias": {
			input: "base: &b {k: x}\nref: *b\n",
			path:  "$.ref.k",
			want:  "x",
		},
		"key a merge brings in": {
			input: "base: &b {k: x}\nm:\n  <<: *b\n  own: 1\n",
			path:  "$.m.k",
			want:  "x",
		},
		"anchor a merge brings in again": {
			// The decoder reads the anchors of a merged mapping again at
			// the merge key, so &x one is the last &x before *x.
			input: "base: &b\n  k: &x one\nother: &x two\nm:\n  <<: *b\n  v: *x\n",
			path:  "$.m.v",
			want:  "one",
		},
		"anchor a merge brings in again for a later mapping": {
			input: "base: &b\n  k: &x one\nother: &x two\nm:\n  <<: *b\nv: *x\n",
			path:  "$.v",
			want:  "one",
		},
		"anchor a nested merge brings in again": {
			input: "b1: &b1 {k: &x one}\nb2: &b2 {<<: *b1}\nother: &x two\nm:\n  <<: *b2\n  v: *x\n",
			path:  "$.m.v",
			want:  "one",
		},
		"anchor of the later merge source": {
			input: "a: &a {k: &x one}\nb: &b {k: &x two}\nother: &x three\nm:\n  <<: [*b, *a]\n  v: *x\n",
			path:  "$.m.v",
			want:  "one",
		},
		"anchor of a later inline merge source": {
			input: "base: &b {k: &x one}\nother: &x two\nm:\n  <<: [*b, {j: &x three}]\n  v: *x\n",
			path:  "$.m.v",
			want:  "three",
		},
		"anchor of a merge source that merges itself": {
			// The decoder rejects the merge of a mapping into itself. The
			// binding stops at the cycle and still counts &x one again.
			input: "a: &a {k: &x one, i: {<<: *a}}\nother: &x two\nm:\n  <<: *a\n  v: *x\n",
			path:  "$.m.v",
			want:  "one",
		},
		"unknown alias": {
			input: "a: *nope\n",
			path:  "$.a",
			err:   paths.ErrAlias,
		},
		"wildcard": {
			input: "a: [x, y]\n",
			path:  "$.a[*]",
			err:   paths.ErrWildcard,
		},
		"missing path": {
			input: "a: x\n",
			path:  "$.b",
			err:   paths.ErrNotFound,
		},
		"nil document": {
			path: "$.a",
			err:  paths.ErrNoDocument,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			// A case with no input resolves in a nil document.
			var doc *ast.DocumentNode

			if tc.input != "" {
				file, err := niceyaml.NewSourceFromString(tc.input).File()
				require.NoError(t, err)

				doc = file.Docs[0]
			}

			node, err := paths.NewResolver(doc).Node(paths.MustParse(tc.path))
			if tc.err != nil {
				require.ErrorIs(t, err, tc.err)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, tc.want, node.String())
		})
	}
}

func TestResolver_Token(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input    string
		path     string
		want     string
		wantLine int
		err      error
	}{
		"plain child": {
			input:    "a:\n  b: x\n",
			path:     "$.a.b",
			want:     "x",
			wantLine: 2,
		},
		"key": {
			input:    "a:\n  b: x\n",
			path:     "$.a.b~",
			want:     "b",
			wantLine: 2,
		},
		"through an alias": {
			input:    "base: &b {k: x}\nref: *b\n",
			path:     "$.ref.k",
			want:     "x",
			wantLine: 1,
		},
		"alias at the end": {
			input:    "base: &b x\nref: *b\n",
			path:     "$.ref",
			want:     "*",
			wantLine: 2,
		},
		"key a merge brings in": {
			input:    "base: &b {k: x}\nm:\n  <<: *b\n  own: 1\n",
			path:     "$.m.k",
			want:     "x",
			wantLine: 1,
		},
		"unknown alias": {
			input: "a: *nope\n",
			path:  "$.a.b",
			err:   paths.ErrAlias,
		},
		"wildcard": {
			input: "a: [x, y]\n",
			path:  "$..a",
			err:   paths.ErrWildcard,
		},
		"missing path": {
			input: "a: x\n",
			path:  "$.b",
			err:   paths.ErrNotFound,
		},
		"nil document": {
			path: "$.a",
			err:  paths.ErrNoDocument,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			// A case with no input resolves in a nil document.
			var doc *ast.DocumentNode

			if tc.input != "" {
				file, err := niceyaml.NewSourceFromString(tc.input).File()
				require.NoError(t, err)

				doc = file.Docs[0]
			}

			tk, err := paths.NewResolver(doc).Token(paths.MustParse(tc.path))
			if tc.err != nil {
				require.ErrorIs(t, err, tc.err)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, tc.want, tk.Value)
			assert.Equal(t, tc.wantLine, tk.Position.Line)
		})
	}
}

func TestResolver_Matches(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input      string
		path       string
		want       []string
		wantValues []string
		err        error
	}{
		"index all": {
			input:      "a: [x, y]\n",
			path:       "$.a[*]",
			want:       []string{"$.a[0]", "$.a[1]"},
			wantValues: []string{"x", "y"},
		},
		"recursive": {
			input:      "a: {name: x}\nb: {c: {name: y}}\n",
			path:       "$..name",
			want:       []string{"$.a.name", "$.b.c.name"},
			wantValues: []string{"x", "y"},
		},
		"each alias to one anchor": {
			input:      "base: &b {k: x}\nrefs: [*b, *b]\n",
			path:       "$.refs[*].k",
			want:       []string{"$.refs[0].k", "$.refs[1].k"},
			wantValues: []string{"x", "x"},
		},
		"key a merge brings in": {
			input:      "base: &b {k: x}\nm:\n  <<: *b\n  own: 1\n",
			path:       "$.m.k",
			want:       []string{"$.m.k"},
			wantValues: []string{"x"},
		},
		"key of each entry": {
			input:      "a: {name: x}\nb: {name: y}\n",
			path:       "$..name~",
			want:       []string{"$.a.name~", "$.b.name~"},
			wantValues: []string{"name", "name"},
		},
		"nothing": {
			input: "a: x\n",
			path:  "$.b[*]",
		},
		"unknown alias": {
			input: "a: *nope\n",
			path:  "$.a[*]",
			err:   paths.ErrAlias,
		},
		"nil document": {
			path: "$.a",
			err:  paths.ErrNoDocument,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			// A case with no input resolves in a nil document.
			var doc *ast.DocumentNode

			if tc.input != "" {
				file, err := niceyaml.NewSourceFromString(tc.input).File()
				require.NoError(t, err)

				doc = file.Docs[0]
			}

			matches, err := paths.NewResolver(doc).Matches(paths.MustParse(tc.path))
			if tc.err != nil {
				require.ErrorIs(t, err, tc.err)

				return
			}

			require.NoError(t, err)

			var got, gotValues []string

			for _, m := range matches {
				got = append(got, m.Path.String())
				gotValues = append(gotValues, m.Node.String())
			}

			assert.Equal(t, tc.want, got)
			assert.Equal(t, tc.wantValues, gotValues)
		})
	}
}

func TestResolver_SeveralPaths(t *testing.T) {
	t.Parallel()

	// One Resolver resolves every path, including one it resolved before,
	// to the node, token, matches, and error that Path.Node, Path.Token,
	// and Path.Matches find on their own.
	source := niceyaml.NewSourceFromString(stringtest.Input(`
		base: &base
		  name: shared
		items: [a, b]
		spec:
		  name: x
		ref: *base
		mixed:
		  <<: *base
		  extra: 1
		bad: *nope
	`))
	file, err := source.File()
	require.NoError(t, err)

	doc := file.Docs[0]
	r := paths.NewResolver(doc)

	for _, expr := range []string{
		"$.spec.name",
		"$.items[1]",
		"$.ref.name",
		"$.mixed.name",
		"$.mixed.extra",
		"$.bad",
		"$.items[*]",
		"$..name",
		"$.mixed~",
		"$.spec.missing",
		"$.spec.name",
	} {
		p := paths.MustParse(expr)

		wantNode, wantErr := p.Node(doc)
		gotNode, err := r.Node(p)

		if assertSameError(t, wantErr, err, expr) {
			assert.Same(t, wantNode, gotNode, expr)
		}

		wantToken, wantErr := p.Token(doc)
		gotToken, err := r.Token(p)

		if assertSameError(t, wantErr, err, expr) {
			assert.Same(t, wantToken, gotToken, expr)
		}

		wantMatches, wantErr := p.Matches(doc)
		gotMatches, err := r.Matches(p)

		if assertSameError(t, wantErr, err, expr) {
			require.Len(t, gotMatches, len(wantMatches), expr)

			for i, want := range wantMatches {
				assert.Same(t, want.Node, gotMatches[i].Node, expr)
				assert.Equal(t, want.Path, gotMatches[i].Path, expr)
			}
		}
	}
}

func TestResolver_ConcurrentUse(t *testing.T) {
	t.Parallel()

	// Several goroutines look up keys in the same mappings through one
	// Resolver, as the Nodes of one document do, and each finds the node
	// Path.Node finds on its own.
	var sb strings.Builder

	sb.WriteString("base: &base {shared: 1}\n")

	for i := range 50 {
		fmt.Fprintf(&sb, "k%d: {<<: *base, own: %d}\n", i, i)
	}

	file, err := niceyaml.NewSourceFromString(sb.String()).File()
	require.NoError(t, err)

	doc := file.Docs[0]

	want := map[string]ast.Node{}

	for i := range 50 {
		for _, name := range []string{"own", "shared"} {
			p := paths.Root().Child(fmt.Sprintf("k%d", i), name)

			node, err := p.Node(doc)
			require.NoError(t, err)

			want[p.String()] = node
		}
	}

	r := paths.NewResolver(doc)

	var wg sync.WaitGroup

	for range 8 {
		wg.Go(func() {
			for expr, node := range want {
				got, err := r.Node(paths.MustParse(expr))
				if assert.NoError(t, err, expr) {
					assert.Same(t, node, got, expr)
				}
			}
		})
	}

	wg.Wait()
}

// assertSameError asserts that err has the text of wantErr, or that both
// are nil, and reports whether both are nil, so the caller compares the
// results.
func assertSameError(t *testing.T, wantErr, err error, expr string) bool {
	t.Helper()

	if wantErr != nil {
		require.EqualError(t, err, wantErr.Error(), expr)

		return false
	}

	require.NoError(t, err, expr)

	return true
}

func TestResolver_NestedMerges(t *testing.T) {
	t.Parallel()

	// Each mapping merges the one before it four times, so reading every
	// merge again would take 4^39 reads. The anchors of each mapping count
	// once per merge all the same.
	var sb strings.Builder

	sb.WriteString("l0: &l0 {k: &x one}\nother: &x two\n")

	for i := 1; i < 40; i++ {
		fmt.Fprintf(&sb, "l%d: &l%d {<<: [*l%d, *l%d, *l%d, *l%d]}\n", i, i, i-1, i-1, i-1, i-1)
	}

	sb.WriteString("v: *x\n")

	file, err := niceyaml.NewSourceFromString(sb.String()).File()
	require.NoError(t, err)

	nodes := make(chan ast.Node, 1)

	go func() {
		node, err := paths.NewResolver(file.Docs[0]).Node(paths.Root().Child("v"))
		assert.NoError(t, err)

		nodes <- node
	}()

	select {
	case node := <-nodes:
		require.NotNil(t, node)
		assert.Equal(t, "one", node.String())

	case <-time.After(10 * time.Second):
		require.FailNow(t, "binding the aliases did not return within 10s")
	}
}

func TestResolver_Deref(t *testing.T) {
	t.Parallel()

	// Each case dereferences the value of the entry v.
	tcs := map[string]struct {
		input string
		want  string
		err   error
	}{
		"alias": {
			input: "a: &a {k: x}\nv: *a\n",
			want:  "{k: x}",
		},
		"last anchor of its name": {
			input: "a: &a one\nb: &a two\nv: *a\n",
			want:  "two",
		},
		"anchor a merge brings in again": {
			input: "base: &b\n  k: &x one\nother: &x two\nm:\n  <<: *b\nv: *x\n",
			want:  "one",
		},
		"anchor": {
			input: "v: &a {k: x}\n",
			want:  "{k: x}",
		},
		"tag on the content of the anchor": {
			input: "a: &a !!str 0x10\nv: *a\n",
			want:  "!!str 0x10",
		},
		"no alias": {
			input: "v: {k: x}\n",
			want:  "{k: x}",
		},
		"alias with no anchor before it": {
			input: "v: *a\na: &a x\n",
			err:   paths.ErrAlias,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			file, err := niceyaml.NewSourceFromString(tc.input).File()
			require.NoError(t, err)

			mapping, ok := file.Docs[0].Body.(*ast.MappingNode)
			require.True(t, ok, "body is a %T", file.Docs[0].Body)

			var value ast.Node

			for _, entry := range mapping.Values {
				if entry.Key.GetToken().Value == "v" {
					value = entry.Value
				}
			}

			require.NotNil(t, value, "no entry v")

			got, err := paths.NewResolver(file.Docs[0]).Deref(value)
			if tc.err != nil {
				require.ErrorIs(t, err, tc.err)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, tc.want, got.String())
		})
	}

	t.Run("alias that leads back to itself", func(t *testing.T) {
		t.Parallel()

		// The parser never puts an alias right under an anchor, but a tree
		// built by hand may, and the alias then refers to itself.
		name := &ast.StringNode{Token: &token.Token{Value: "a"}, Value: "a"}
		alias := &ast.AliasNode{Value: name}
		doc := &ast.DocumentNode{Body: &ast.AnchorNode{Name: name, Value: alias}}

		_, err := paths.NewResolver(doc).Deref(alias)
		require.ErrorIs(t, err, paths.ErrAlias)
	})

	t.Run("nil node", func(t *testing.T) {
		t.Parallel()

		got, err := paths.NewResolver(nil).Deref(nil)
		require.NoError(t, err)
		assert.Nil(t, got)
	})
}

func TestResolver_MergeSources(t *testing.T) {
	t.Parallel()

	// Each case reads the merge sources of the mapping at $.m, and names
	// each source by its first key.
	tcs := map[string]struct {
		input string
		opts  []niceyaml.SourceOption
		want  []string
		err   error
	}{
		"one mapping": {
			input: "a: &a {x: 1}\nm: {<<: *a, own: 1}\n",
			want:  []string{"x"},
		},
		"sequence of mappings": {
			input: "a: &a {x: 1}\nb: &b {y: 2}\nm: {<<: [*a, *b]}\n",
			want:  []string{"x", "y"},
		},
		"alias to a sequence": {
			input: "a: &a {x: 1}\nb: &b {y: 2}\nl: &l [*a, *b]\nm: {<<: *l}\n",
			want:  []string{"x", "y"},
		},
		"repeated merge keys": {
			input: "a: &a {x: 1}\nb: &b {y: 2}\nm: {<<: *b, own: 1, <<: *a}\n",
			opts:  []niceyaml.SourceOption{niceyaml.WithAllowDuplicateKeys(true)},
			want:  []string{"y", "x"},
		},
		"no merge key": {
			input: "m: {k: x}\n",
		},
		"not a mapping": {
			input: "m: [x, y]\n",
		},
		"unknown alias": {
			input: "m: {<<: *nope}\n",
			err:   paths.ErrAlias,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			file, err := niceyaml.NewSourceFromString(tc.input, tc.opts...).File()
			require.NoError(t, err)

			r := paths.NewResolver(file.Docs[0])

			node, err := r.Node(paths.MustParse("$.m"))
			require.NoError(t, err)

			sources, err := r.MergeSources(node)
			if tc.err != nil {
				require.ErrorIs(t, err, tc.err)

				return
			}

			require.NoError(t, err)

			var got []string

			for _, src := range sources {
				mapping, ok := src.(*ast.MappingNode)
				require.True(t, ok, "source is a %T", src)
				require.NotEmpty(t, mapping.Values)

				got = append(got, mapping.Values[0].Key.GetToken().Value)
			}

			assert.Equal(t, tc.want, got)
		})
	}
}

func TestResolver_MergeSources_HandBuilt(t *testing.T) {
	t.Parallel()

	// The parser never produces these keys, but MergeSources accepts any
	// node, so a mutated tree must return no sources rather than panic.
	source := mapNode(mapEntry(&ast.StringNode{Value: "k"}, &ast.StringNode{Value: "x"}))

	tcs := map[string]struct {
		key  ast.MapKeyNode
		want int
	}{
		"explicit key wrapping a typed nil anchor": {
			key: &ast.MappingKeyNode{Value: (*ast.AnchorNode)(nil)},
		},
		"anchor key wrapping a typed nil tag": {
			key: &ast.AnchorNode{Value: (*ast.TagNode)(nil)},
		},
		"typed nil explicit key": {
			key: (*ast.MappingKeyNode)(nil),
		},
		"explicit merge key": {
			key:  &ast.MappingKeyNode{Value: &ast.MergeKeyNode{}},
			want: 1,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			mapping := mapNode(mapEntry(tc.key, source))
			doc := &ast.DocumentNode{Body: mapping}

			var (
				sources []ast.Node
				err     error
			)

			require.NotPanics(t, func() {
				sources, err = paths.NewResolver(doc).MergeSources(mapping)
			})
			require.NoError(t, err)
			require.Len(t, sources, tc.want)

			for _, src := range sources {
				assert.Same(t, source, src)
			}
		})
	}
}
