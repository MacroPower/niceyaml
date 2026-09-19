package paths_test

import (
	"testing"
	"time"

	"github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/token"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/paths"
)

func TestRoot(t *testing.T) {
	t.Parallel()

	t.Run("empty path targets the node", func(t *testing.T) {
		t.Parallel()

		path := paths.Root()

		require.NotNil(t, path)
		assert.Equal(t, "$", path.String())
		assert.Equal(t, paths.PartNode, path.Part())
	})

	t.Run("single child with key target", func(t *testing.T) {
		t.Parallel()

		path := paths.Root().Child("kind").Key()

		require.NotNil(t, path)
		assert.Equal(t, "$.kind", path.String())
		assert.Equal(t, paths.PartKey, path.Part())
	})

	t.Run("multiple children", func(t *testing.T) {
		t.Parallel()

		path := paths.Root().Child("metadata", "name")

		require.NotNil(t, path)
		assert.Equal(t, "$.metadata.name", path.String())
		assert.Equal(t, paths.PartNode, path.Part())
	})
}

func TestPath_Build(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		build    func() paths.Path
		want     string
		wantYAML string
		part     paths.Part
	}{
		"root path": {
			build: paths.Root,
			part:  paths.PartNode,
			want:  "$",
		},
		"chained children": {
			build: func() paths.Path { return paths.Root().Child("metadata", "labels").Key() },
			part:  paths.PartKey,
			want:  "$.metadata.labels",
		},
		"child then index": {
			build: func() paths.Path { return paths.Root().Child("items").Index(0) },
			part:  paths.PartNode,
			want:  "$.items[0]",
		},
		"variadic index": {
			build: func() paths.Path { return paths.Root().Child("matrix").Index(0, 1) },
			part:  paths.PartNode,
			want:  "$.matrix[0][1]",
		},
		"index all": {
			build: func() paths.Path { return paths.Root().Child("items").IndexAll().Key() },
			part:  paths.PartKey,
			want:  "$.items[*]",
		},
		"recursive descent": {
			build: func() paths.Path { return paths.Root().Recursive("name") },
			part:  paths.PartNode,
			want:  "$..name",
		},
		"large index": {
			build: func() paths.Path { return paths.Root().Child("items").Index(999) },
			part:  paths.PartNode,
			want:  "$.items[999]",
		},
		"recursive with index": {
			build: func() paths.Path { return paths.Root().Recursive("items").Index(0).Key() },
			part:  paths.PartKey,
			want:  "$..items[0]",
		},
		"multiple recursive": {
			build: func() paths.Path { return paths.Root().Recursive("containers").Recursive("name") },
			part:  paths.PartNode,
			want:  "$..containers..name",
		},
		"child after index": {
			build: func() paths.Path { return paths.Root().Child("items").Index(0).Child("name") },
			part:  paths.PartNode,
			want:  "$.items[0].name",
		},
		"index all then index": {
			build: func() paths.Path { return paths.Root().Child("matrix").IndexAll().Index(0) },
			part:  paths.PartNode,
			want:  "$.matrix[*][0]",
		},
		"dotted child name is quoted": {
			build: func() paths.Path { return paths.Root().Child("kubernetes.io/name") },
			part:  paths.PartNode,
			want:  "$.'kubernetes.io/name'",
		},
		"child name with quote is quoted and escaped": {
			build:    func() paths.Path { return paths.Root().Child("it's") },
			part:     paths.PartNode,
			want:     `$.'it\'s'`,
			wantYAML: "$.it's",
		},
		"empty child name is quoted, but goccy leaves it bare": {
			build:    func() paths.Path { return paths.Root().Child("") },
			part:     paths.PartNode,
			want:     "$.''",
			wantYAML: "$.",
		},
		"empty recursive name is quoted, but goccy leaves it bare": {
			build:    func() paths.Path { return paths.Root().Recursive("") },
			part:     paths.PartNode,
			want:     "$..''",
			wantYAML: "$..",
		},
		"child name with brackets is quoted": {
			build:    func() paths.Path { return paths.Root().Child("a[0]") },
			part:     paths.PartNode,
			want:     "$.'a[0]'",
			wantYAML: "$.a[0]",
		},
		"recursive name with a dot is quoted, but goccy cannot quote it": {
			build:    func() paths.Path { return paths.Root().Recursive("x.y") },
			part:     paths.PartNode,
			want:     "$..'x.y'",
			wantYAML: "$..x.y",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			path := tc.build()

			require.NotNil(t, path)
			assert.Equal(t, tc.want, path.String())
			assert.Equal(t, tc.part, path.Part())

			// The goccy form only differs where goccy quotes differently.
			wantGoccy := tc.wantYAML
			if wantGoccy == "" {
				wantGoccy = tc.want
			}

			assert.Equal(t, wantGoccy, path.YAMLPath().String())
		})
	}
}

func TestPath_Index_Negative(t *testing.T) {
	t.Parallel()

	// A negative index has no meaning of its own, so every rendering of the
	// path agrees on element 0.
	negative := paths.Root().Child("items").Index(-1)
	zero := paths.Root().Child("items").Index(0)

	assert.Equal(t, zero, negative)
	assert.Equal(t, "$.items[0]", negative.String())
	assert.Equal(t, "$.items[0]", negative.YAMLPath().String())

	source := niceyaml.NewSourceFromString("items: [a, b]\n")
	file, err := source.File()
	require.NoError(t, err)

	tk, err := negative.Token(file.Docs[0])
	require.NoError(t, err)
	assert.Equal(t, "a", tk.Value)
}

func TestPath_Immutable(t *testing.T) {
	t.Parallel()

	t.Run("shared prefix is not aliased", func(t *testing.T) {
		t.Parallel()

		spec := paths.Root().Child("spec")
		replicas := spec.Child("replicas")
		image := spec.Child("image")

		assert.Equal(t, "$.spec.replicas", replicas.String())
		assert.Equal(t, "$.spec.image", image.String())
		assert.Equal(t, "$.spec", spec.String())
	})

	t.Run("picking a part does not change the receiver", func(t *testing.T) {
		t.Parallel()

		p := paths.Root().Child("metadata", "name")

		assert.Equal(t, paths.PartKey, p.Key().Part())
		assert.Equal(t, paths.PartNode, p.Part())
		assert.Equal(t, "$.metadata.name.labels", p.Child("labels").String())
	})

	t.Run("zero value is the root", func(t *testing.T) {
		t.Parallel()

		var p paths.Path

		assert.Equal(t, paths.Root(), p)
		assert.Equal(t, "$", p.String())
		assert.Equal(t, paths.PartNode, p.Part())
		assert.Equal(t, "$.a", p.Child("a").String())
	})
}

func TestPath_Key(t *testing.T) {
	t.Parallel()

	t.Run("derives copies with a different part", func(t *testing.T) {
		t.Parallel()

		node := paths.MustParse("$.a.b")
		key := node.Key()

		assert.Equal(t, paths.PartNode, node.Part())
		assert.Equal(t, paths.PartKey, key.Part())
		assert.Equal(t, "$.a.b", key.String())
		assert.Equal(t, "$.a.b", node.String())
	})
}

func TestPart_String(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "node", paths.PartNode.String())
	assert.Equal(t, "key", paths.PartKey.String())
	assert.Equal(t, "Part(7)", paths.Part(7).String())
}

func TestParse(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		expr string
		want string
	}{
		"simple path": {
			expr: "$.foo",
			want: "$.foo",
		},
		"nested path": {
			expr: "$.foo.bar.baz",
			want: "$.foo.bar.baz",
		},
		"array index": {
			expr: "$.items[0]",
			want: "$.items[0]",
		},
		"array wildcard": {
			expr: "$.items[*]",
			want: "$.items[*]",
		},
		"recursive descent": {
			expr: "$..name",
			want: "$..name",
		},
		"root only": {
			expr: "$",
			want: "$",
		},
		"root index": {
			expr: "$[2]",
			want: "$[2]",
		},
		"quoted key": {
			expr: "$.'kubernetes.io/name'",
			want: "$.'kubernetes.io/name'",
		},
		"quoted key without reserved characters is unquoted": {
			expr: "$.'plain'",
			want: "$.plain",
		},
		"quoted key with escaped quote": {
			expr: `$.'it\'s'`,
			want: `$.'it\'s'`,
		},
		"quoted key followed by index": {
			expr: "$.'a.b'[1].c",
			want: "$.'a.b'[1].c",
		},
		"empty quoted key": {
			expr: "$.''",
			want: "$.''",
		},
		"empty quoted recursive key": {
			expr: "$..''[0]",
			want: "$..''[0]",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			p, err := paths.Parse(tc.expr)

			require.NoError(t, err)
			require.NotNil(t, p)
			assert.Equal(t, tc.want, p.String())
			assert.Equal(t, paths.PartNode, p.Part())
		})
	}
}

func TestParse_Invalid(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		expr string
	}{
		"empty": {
			expr: "",
		},
		"no dollar prefix": {
			expr: "foo",
		},
		"leading dot without root": {
			expr: ".foo",
		},
		"trailing dot": {
			expr: "$.",
		},
		"unclosed bracket": {
			expr: "$[",
		},
		"empty index": {
			expr: "$[]",
		},
		"negative index": {
			expr: "$[-1]",
		},
		"negative zero index": {
			expr: "$[-0]",
		},
		"index with a leading zero": {
			expr: "$[01]",
		},
		"index with a plus sign": {
			expr: "$[+1]",
		},
		"non-numeric index": {
			expr: "$[a]",
		},
		"wildcard child": {
			expr: "$.*",
		},
		"unterminated quote": {
			expr: "$.'foo",
		},
		"bare text after root": {
			expr: "$foo",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			p, err := paths.Parse(tc.expr)

			require.ErrorIs(t, err, paths.ErrInvalidPath)
			assert.Equal(t, paths.Path{}, p)
			assert.Contains(t, err.Error(), "parse path")
		})
	}
}

func TestParse_RoundTrip(t *testing.T) {
	t.Parallel()

	tcs := map[string]paths.Path{
		"root":             paths.Root(),
		"children":         paths.Root().Child("a", "b").Key(),
		"index":            paths.Root().Child("items").Index(3),
		"wildcards":        paths.Root().Child("items").IndexAll().Recursive("name"),
		"dotted name":      paths.Root().Child("kubernetes.io/name"),
		"quote in name":    paths.Root().Child("it's"),
		"backslash":        paths.Root().Child(`a\b.c`),
		"brackets":         paths.Root().Child("a[0]"),
		"dollar":           paths.Root().Child("$ref"),
		"star":             paths.Root().Child("*"),
		"numeric name":     paths.Root().Child("1"),
		"space in name":    paths.Root().Child("has space"),
		"only reserved":    paths.Root().Child("."),
		"goccy compatible": paths.Root().Child("a.b").Index(1).Child("c"),
	}

	for name, want := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, err := paths.Parse(want.String())
			require.NoError(t, err)

			assert.Equal(t, want.String(), got.String())
			assert.Equal(t, paths.PartNode, got.Part())

			// The goccy parser accepts the same expression.
			_, err = yaml.PathString(want.String())
			require.NoError(t, err)
		})
	}
}

func TestParse_RoundTrip_EmptyName(t *testing.T) {
	t.Parallel()

	// The goccy parser rejects $.'' so the empty name stays out of the table
	// above, but Parse reads back what String writes.
	source := niceyaml.NewSourceFromString("'': v\n")
	file, err := source.File()
	require.NoError(t, err)

	tcs := map[string]paths.Path{
		"child":     paths.Root().Child(""),
		"recursive": paths.Root().Recursive(""),
	}

	for name, want := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, err := paths.Parse(want.String())
			require.NoError(t, err)
			assert.Equal(t, want, got)

			nodes, err := got.Nodes(file.Docs[0])
			require.NoError(t, err)
			require.Len(t, nodes, 1)
			assert.Equal(t, "v", nodes[0].GetToken().Value)
		})
	}
}

func TestMustParse(t *testing.T) {
	t.Parallel()

	t.Run("valid expression returns path", func(t *testing.T) {
		t.Parallel()

		p := paths.MustParse("$.foo.bar")
		require.NotNil(t, p)
		assert.Equal(t, "$.foo.bar", p.String())
	})

	t.Run("panics on invalid expression", func(t *testing.T) {
		t.Parallel()

		assert.Panics(t, func() {
			paths.MustParse("not a valid path")
		})
	})
}

func TestPath_YAMLPath(t *testing.T) {
	t.Parallel()

	source := niceyaml.NewSourceFromString("a:\n  'b.c': [x, y]\n  don't: 1\n  a\\.b: 2\n")
	file, err := source.File()
	require.NoError(t, err)

	tcs := map[string]struct {
		path       paths.Path
		wantString string
		want       string
	}{
		"dotted name is quoted": {
			path:       paths.Root().Child("a", "b.c").Index(1),
			wantString: "$.a.'b.c'[1]",
			want:       "y",
		},
		"name with a quote filters by its raw text": {
			path:       paths.Root().Child("a", "don't"),
			wantString: "$.a.don't",
			want:       "1",
		},
		"name with a backslash filters by its raw text": {
			path:       paths.Root().Child("a", `a\.b`),
			wantString: `$.a.'a\.b'`,
			want:       "2",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			yp := tc.path.YAMLPath()
			require.NotNil(t, yp)
			assert.Equal(t, tc.wantString, yp.String())

			node, err := yp.FilterNode(file.Docs[0].Body)
			require.NoError(t, err)
			require.NotNil(t, node, "node not found")
			assert.Equal(t, tc.want, node.GetToken().Value)
		})
	}
}

func TestPath_Token(t *testing.T) {
	t.Parallel()

	input := `
name: test
kind: Service
metadata:
  labels:
    app: myapp
items:
  - first
  - second
`

	source := niceyaml.NewSourceFromString(input)
	file, err := source.File()
	require.NoError(t, err)

	tcs := map[string]struct {
		path      paths.Path
		wantValue string
		wantType  token.Type
	}{
		"root value returns the first key": {
			path:      paths.Root(),
			wantValue: "name",
			wantType:  token.StringType,
		},
		"root key returns the first key": {
			path:      paths.Root().Key(),
			wantValue: "name",
			wantType:  token.StringType,
		},
		"mapping value target returns its first key": {
			path:      paths.Root().Child("metadata"),
			wantValue: "labels",
			wantType:  token.StringType,
		},
		"mapping key target returns the entry key": {
			path:      paths.Root().Child("metadata").Key(),
			wantValue: "metadata",
			wantType:  token.StringType,
		},
		"sequence value target returns its first element": {
			path:      paths.Root().Child("items"),
			wantValue: "first",
			wantType:  token.StringType,
		},
		"sequence key target returns the entry key": {
			path:      paths.Root().Child("items").Key(),
			wantValue: "items",
			wantType:  token.StringType,
		},
		"simple key target returns key token": {
			path:      paths.Root().Child("name").Key(),
			wantValue: "name",
			wantType:  token.StringType,
		},
		"simple value target returns value token": {
			path:      paths.Root().Child("name"),
			wantValue: "test",
			wantType:  token.StringType,
		},
		"node target returns value token": {
			path:      paths.Root().Child("name"),
			wantValue: "test",
			wantType:  token.StringType,
		},
		"nested key target": {
			path:      paths.Root().Child("metadata", "labels", "app").Key(),
			wantValue: "app",
			wantType:  token.StringType,
		},
		"nested value target": {
			path:      paths.Root().Child("metadata", "labels", "app"),
			wantValue: "myapp",
			wantType:  token.StringType,
		},
		"array element value target": {
			path:      paths.Root().Child("items").Index(0),
			wantValue: "first",
			wantType:  token.StringType,
		},
		"array element key target returns value (no parent mapping)": {
			path:      paths.Root().Child("items").Index(1).Key(),
			wantValue: "second",
			wantType:  token.StringType,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			tk, err := tc.path.Token(file.Docs[0])
			require.NoError(t, err)
			require.NotNil(t, tk)
			assert.Equal(t, tc.wantValue, tk.Value)
			assert.Equal(t, tc.wantType, tk.Type)
		})
	}
}

func TestPath_Token_InvalidPath(t *testing.T) {
	t.Parallel()

	source := niceyaml.NewSourceFromString(`name: test`)
	file, err := source.File()
	require.NoError(t, err)

	path := paths.Root().Child("nonexistent")
	_, err = path.Token(file.Docs[0])
	require.ErrorIs(t, err, paths.ErrNotFound)
}

func TestPath_Token_NoDocument(t *testing.T) {
	t.Parallel()

	path := paths.Root().Child("name").Key()

	_, err := path.Token(nil)
	require.ErrorIs(t, err, paths.ErrNoDocument)
	require.ErrorIs(t, err, paths.ErrNotFound)

	_, err = path.Token(&ast.DocumentNode{})
	require.ErrorIs(t, err, paths.ErrNoDocument)
	require.ErrorIs(t, err, paths.ErrNotFound)
	assert.Contains(t, err.Error(), "$.name")
}

func TestPath_DirectiveDocument(t *testing.T) {
	t.Parallel()

	// The directive parses as a document of its own, ahead of the content.
	source := niceyaml.NewSourceFromString("%YAML 1.2\n---\nkey: v\n")
	file, err := source.File()
	require.NoError(t, err)
	require.Len(t, file.Docs, 2)

	_, err = paths.Root().Node(file.Docs[0])
	require.ErrorIs(t, err, paths.ErrNoDocument)
	require.ErrorIs(t, err, paths.ErrNotFound)

	_, err = paths.Root().Child("key").Token(file.Docs[0])
	require.ErrorIs(t, err, paths.ErrNoDocument)

	_, err = paths.Root().Nodes(file.Docs[0])
	require.ErrorIs(t, err, paths.ErrNoDocument)

	node, err := paths.Root().Child("key").Node(file.Docs[1])
	require.NoError(t, err)
	assert.Equal(t, "v", node.String())
}

func TestPath_CommentDocument(t *testing.T) {
	t.Parallel()

	// A parse that keeps comments makes the comment group the body of a
	// document that holds nothing else, and nothing resolves in it.
	source := niceyaml.NewSourceFromString("# just a comment\n")
	file, err := source.File()
	require.NoError(t, err)
	require.Len(t, file.Docs, 1)

	_, err = paths.Root().Node(file.Docs[0])
	require.ErrorIs(t, err, paths.ErrNoDocument)
	require.ErrorIs(t, err, paths.ErrNotFound)

	_, err = paths.Root().Token(file.Docs[0])
	require.ErrorIs(t, err, paths.ErrNoDocument)

	_, err = paths.Root().Nodes(file.Docs[0])
	require.ErrorIs(t, err, paths.ErrNoDocument)
}

func TestPath_Token_UnresolvableAlias(t *testing.T) {
	t.Parallel()

	// Node dereferences the alias and reports that it names no anchor, while
	// Token returns the alias's own token.
	source := niceyaml.NewSourceFromString("a: *x\nb: &x 1\n")
	file, err := source.File()
	require.NoError(t, err)
	require.Len(t, file.Docs, 1)

	_, err = paths.Root().Child("a").Node(file.Docs[0])
	require.ErrorIs(t, err, paths.ErrAlias)

	tk, err := paths.Root().Child("a").Token(file.Docs[0])
	require.NoError(t, err)
	assert.Equal(t, token.AliasType, tk.Type)
}

func TestPath_Token_MultipleDocuments(t *testing.T) {
	t.Parallel()

	source := niceyaml.NewSourceFromString("name: first\n---\nname: second\n")
	file, err := source.File()
	require.NoError(t, err)
	require.Len(t, file.Docs, 2)

	path := paths.Root().Child("name")

	tk, err := path.Token(file.Docs[0])
	require.NoError(t, err)
	assert.Equal(t, "first", tk.Value)

	tk, err = path.Token(file.Docs[1])
	require.NoError(t, err)
	assert.Equal(t, "second", tk.Value)
}

func TestPath_Token_NestedStructures(t *testing.T) {
	t.Parallel()

	input := `
list:
  - name: first
    items:
      - a
      - b
  - name: second
    items:
      - c
      - d
nested:
  deep:
    deeper:
      value: found
`

	source := niceyaml.NewSourceFromString(input)
	file, err := source.File()
	require.NoError(t, err)

	tcs := map[string]struct {
		path      paths.Path
		wantValue string
		wantType  token.Type
	}{
		"nested array first element name": {
			path:      paths.Root().Child("list").Index(0).Child("name"),
			wantValue: "first",
			wantType:  token.StringType,
		},
		"nested array second element items first": {
			path:      paths.Root().Child("list").Index(1).Child("items").Index(0),
			wantValue: "c",
			wantType:  token.StringType,
		},
		"deeply nested value": {
			path:      paths.Root().Child("nested", "deep", "deeper", "value"),
			wantValue: "found",
			wantType:  token.StringType,
		},
		"deeply nested key": {
			path:      paths.Root().Child("nested", "deep", "deeper", "value").Key(),
			wantValue: "value",
			wantType:  token.StringType,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			tk, err := tc.path.Token(file.Docs[0])
			require.NoError(t, err)
			require.NotNil(t, tk)
			assert.Equal(t, tc.wantValue, tk.Value)
			assert.Equal(t, tc.wantType, tk.Type)
		})
	}
}

func TestPath_Node(t *testing.T) {
	t.Parallel()

	source := niceyaml.NewSourceFromString("a:\n  b: [1, 2]\n")
	file, err := source.File()
	require.NoError(t, err)

	// The part does not affect the resolved node.
	for _, path := range []paths.Path{
		paths.Root().Child("a", "b"),
		paths.Root().Child("a", "b").Key(),
		paths.Root().Child("a", "b"),
	} {
		node, err := path.Node(file.Docs[0])
		require.NoError(t, err)

		seq, ok := node.(*ast.SequenceNode)
		require.True(t, ok, "want *ast.SequenceNode, got %T", node)
		assert.Len(t, seq.Values, 2)
	}
}

func TestPath_Token_Anchors(t *testing.T) {
	t.Parallel()

	input := `
base: &b
  a: 1
  b: 2
flow: &f {x: 10}
other: *b
list: &l
  - one
  - two
copy: *l
tagged: !!str 5
merged:
  <<: *b
  b: 20
  c: 3
multi:
  <<: [*b, *f]
m: &m
  <<: *b
  d: 4
chain:
  <<: *m
`

	source := niceyaml.NewSourceFromString(input)
	file, err := source.File()
	require.NoError(t, err)

	tcs := map[string]struct {
		path      paths.Path
		wantValue string
		wantLine  int
	}{
		"child of anchored mapping": {
			path:      paths.Root().Child("base", "a"),
			wantValue: "1",
			wantLine:  3,
		},
		"key of anchored mapping entry": {
			path:      paths.Root().Child("base", "a").Key(),
			wantValue: "a",
			wantLine:  3,
		},
		"anchored mapping value target skips the anchor": {
			path:      paths.Root().Child("base"),
			wantValue: "a",
			wantLine:  3,
		},
		"child through alias lands in the anchor": {
			path:      paths.Root().Child("other", "b"),
			wantValue: "2",
			wantLine:  4,
		},
		"alias value target is the alias token": {
			path:      paths.Root().Child("other"),
			wantValue: "*",
			wantLine:  6,
		},
		"alias key target is its key": {
			path:      paths.Root().Child("other").Key(),
			wantValue: "other",
			wantLine:  6,
		},
		"index through anchored sequence": {
			path:      paths.Root().Child("list").Index(1),
			wantValue: "two",
			wantLine:  9,
		},
		"index through aliased sequence": {
			path:      paths.Root().Child("copy").Index(0),
			wantValue: "one",
			wantLine:  8,
		},
		"tagged scalar value target skips the tag": {
			path:      paths.Root().Child("tagged"),
			wantValue: "5",
			wantLine:  11,
		},
		"merged key resolves to the anchor": {
			path:      paths.Root().Child("merged", "a"),
			wantValue: "1",
			wantLine:  3,
		},
		"merged key target resolves to the anchor key": {
			path:      paths.Root().Child("merged", "a").Key(),
			wantValue: "a",
			wantLine:  3,
		},
		"own key wins over merged key": {
			path:      paths.Root().Child("merged", "b"),
			wantValue: "20",
			wantLine:  14,
		},
		"own key next to merge": {
			path:      paths.Root().Child("merged", "c"),
			wantValue: "3",
			wantLine:  15,
		},
		"merge sequence first source": {
			path:      paths.Root().Child("multi", "a"),
			wantValue: "1",
			wantLine:  3,
		},
		"merge sequence second source": {
			path:      paths.Root().Child("multi", "x"),
			wantValue: "10",
			wantLine:  5,
		},
		"merge of a merged mapping": {
			path:      paths.Root().Child("chain", "a"),
			wantValue: "1",
			wantLine:  3,
		},
		"merge of a merged mapping own key": {
			path:      paths.Root().Child("chain", "d"),
			wantValue: "4",
			wantLine:  20,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			tk, err := tc.path.Token(file.Docs[0])
			require.NoError(t, err)
			require.NotNil(t, tk)
			assert.Equal(t, tc.wantValue, tk.Value)
			assert.Equal(t, tc.wantLine, tk.Position.Line)
		})
	}

	t.Run("merged key not present", func(t *testing.T) {
		t.Parallel()

		_, err := paths.Root().Child("merged", "zzz").Token(file.Docs[0])
		require.ErrorIs(t, err, paths.ErrNotFound)
	})

	t.Run("merge key entry itself is addressable", func(t *testing.T) {
		t.Parallel()

		tk, err := paths.Root().Child("merged", "<<").Key().Token(file.Docs[0])
		require.NoError(t, err)
		assert.Equal(t, "<<", tk.Value)
	})
}

func TestPath_Node_Anchors(t *testing.T) {
	t.Parallel()

	source := niceyaml.NewSourceFromString("base: &b {a: 1}\nother: *b\ntagged: !!str 5\n")
	file, err := source.File()
	require.NoError(t, err)

	t.Run("anchor is looked through", func(t *testing.T) {
		t.Parallel()

		node, err := paths.Root().Child("base").Node(file.Docs[0])
		require.NoError(t, err)
		assert.IsType(t, &ast.MappingNode{}, node)
	})

	t.Run("alias is looked through", func(t *testing.T) {
		t.Parallel()

		node, err := paths.Root().Child("other").Node(file.Docs[0])
		require.NoError(t, err)
		assert.IsType(t, &ast.MappingNode{}, node)
	})

	t.Run("tag is kept", func(t *testing.T) {
		t.Parallel()

		node, err := paths.Root().Child("tagged").Node(file.Docs[0])
		require.NoError(t, err)
		assert.IsType(t, &ast.TagNode{}, node)
	})
}

func TestPath_UnknownAlias(t *testing.T) {
	t.Parallel()

	source := niceyaml.NewSourceFromString("base: &b {a: 1}\nother: *nope\n")
	file, err := source.File()
	require.NoError(t, err)

	_, err = paths.Root().Child("other", "a").Token(file.Docs[0])
	require.ErrorIs(t, err, paths.ErrAlias)
	require.NotErrorIs(t, err, paths.ErrNotFound)
	assert.Contains(t, err.Error(), "*nope")

	_, err = paths.Root().Child("other").Node(file.Docs[0])
	require.ErrorIs(t, err, paths.ErrAlias)
}

func TestPath_AliasCycle(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input string
		want  string
		path  paths.Path
	}{
		"alias inside its own anchor through a tag": {
			input: "a: &x !t *x\n",
			path:  paths.Root().Child("a", "c"),
			want:  "*x forms a cycle",
		},
		"merge key through an alias inside its own anchor": {
			input: "a: &x !t *x\nm:\n  <<: *x\n",
			path:  paths.Root().Child("m", "c"),
			want:  "*x forms a cycle",
		},
		"anchors whose tags alias each other": {
			// The *y on line 1 comes before &y, so it names no anchor and
			// resolution stops there.
			input: "a: &x !t *y\nb: &y !t *x\n",
			path:  paths.Root().Child("b", "c"),
			want:  "*y has no anchor before it",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			source := niceyaml.NewSourceFromString(tc.input)
			file, err := source.File()
			require.NoError(t, err)

			doc := file.Docs[0]

			// Resolve in a goroutine so a loop that never returns fails this
			// test instead of stalling the run.
			errs := make(chan error, 3)

			go func() {
				_, err := tc.path.Token(doc)
				errs <- err

				_, err = tc.path.Node(doc)
				errs <- err

				_, err = tc.path.Nodes(doc)
				errs <- err
			}()

			for range 3 {
				select {
				case err := <-errs:
					require.ErrorIs(t, err, paths.ErrAlias)
					require.NotErrorIs(t, err, paths.ErrNotFound)
					assert.Contains(t, err.Error(), tc.want)

				case <-time.After(10 * time.Second):
					require.FailNow(t, "path resolution did not return within 10s")
				}
			}
		})
	}
}

func TestPath_HandBuiltAST(t *testing.T) {
	t.Parallel()

	// The parser never produces these shapes, but Node and Token accept any
	// *ast.DocumentNode, so a mutated tree must error rather than panic.
	t.Run("alias without a name", func(t *testing.T) {
		t.Parallel()

		file, err := niceyaml.NewSourceFromString("a: 1\n").File()
		require.NoError(t, err)

		doc := file.Docs[0]
		mapping, ok := doc.Body.(*ast.MappingNode)
		require.True(t, ok, "want *ast.MappingNode, got %T", doc.Body)

		mapping.Values[0].Value = &ast.AliasNode{}

		_, err = paths.Root().Child("a").Node(doc)
		require.ErrorIs(t, err, paths.ErrAlias)
		assert.Contains(t, err.Error(), "alias has no name")

		_, err = paths.Root().Child("a", "b").Token(doc)
		require.ErrorIs(t, err, paths.ErrAlias)
	})

	t.Run("entry without a key", func(t *testing.T) {
		t.Parallel()

		file, err := niceyaml.NewSourceFromString("a: 1\n").File()
		require.NoError(t, err)

		doc := file.Docs[0]
		mapping, ok := doc.Body.(*ast.MappingNode)
		require.True(t, ok, "want *ast.MappingNode, got %T", doc.Body)

		mapping.Values[0].Key = nil

		tk, err := paths.Root().Token(doc)
		require.NoError(t, err)
		assert.Equal(t, mapping.Values[0].GetToken(), tk)

		tk, err = paths.Root().Child("").Key().Token(doc)
		require.NoError(t, err)
		assert.Equal(t, "1", tk.Value)
	})
}

func TestPath_RedefinedAnchor(t *testing.T) {
	t.Parallel()

	// An alias refers to the last anchor of its name before it, so the *x on
	// line 3 reaches `v: 1` and the *x on line 6 reaches `v: 2`.
	source := niceyaml.NewSourceFromString("a: &x\n  v: 1\nb: *x\nc: &x\n  v: 2\nd: *x\n")
	file, err := source.File()
	require.NoError(t, err)

	tcs := map[string]struct {
		key       string
		wantValue string
		wantLine  int
	}{
		"alias before the second anchor": {
			key:       "b",
			wantValue: "1",
			wantLine:  2,
		},
		"alias after the second anchor": {
			key:       "d",
			wantValue: "2",
			wantLine:  5,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			tk, err := paths.Root().Child(tc.key, "v").Token(file.Docs[0])
			require.NoError(t, err)
			assert.Equal(t, tc.wantValue, tk.Value)
			assert.Equal(t, tc.wantLine, tk.Position.Line)

			node, err := paths.Root().Child(tc.key).Node(file.Docs[0])
			require.NoError(t, err)

			mapping, ok := node.(*ast.MappingNode)
			require.True(t, ok, "want *ast.MappingNode, got %T", node)
			require.Len(t, mapping.Values, 1)

			value := mapping.Values[0].Value.GetToken()
			assert.Equal(t, tc.wantValue, value.Value)
			assert.Equal(t, tc.wantLine, value.Position.Line)
		})
	}

	t.Run("document decoder value matches the decoded document", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, "a: &x v1\nb: *x\nc: &x v2\nd: *x\n")

		var decoded map[string]any

		require.NoError(t, dd.DecodeInto(t.Context(), &decoded))
		assert.Equal(t, map[string]any{"a": "v1", "b": "v1", "c": "v2", "d": "v2"}, decoded)

		for _, key := range []string{"b", "d"} {
			got, err := dd.Get[string](t.Context(), paths.Root().Child(key))
			require.NoError(t, err)
			assert.Equal(t, decoded[key], got)
		}
	})

	t.Run("alias before any anchor of its name", func(t *testing.T) {
		t.Parallel()

		forward := niceyaml.NewSourceFromString("b: *x\na: &x\n  v: 1\n")
		forwardFile, err := forward.File()
		require.NoError(t, err)

		_, err = paths.Root().Child("b", "v").Token(forwardFile.Docs[0])
		require.ErrorIs(t, err, paths.ErrAlias)
		require.NotErrorIs(t, err, paths.ErrNotFound)
		assert.Contains(t, err.Error(), "*x has no anchor before it")

		_, err = paths.Root().Child("b").Node(forwardFile.Docs[0])
		require.ErrorIs(t, err, paths.ErrAlias)
	})
}

func TestPath_Token_NotFound(t *testing.T) {
	t.Parallel()

	source := niceyaml.NewSourceFromString("name: test\nitems: [a, b]\n")
	file, err := source.File()
	require.NoError(t, err)

	tcs := map[string]paths.Path{
		"missing key":              paths.Root().Child("nope"),
		"child of scalar":          paths.Root().Child("name", "x"),
		"index of mapping":         paths.Root().Index(0),
		"index of scalar":          paths.Root().Child("name").Index(0),
		"index out of range":       paths.Root().Child("items").Index(2),
		"child of sequence":        paths.Root().Child("items", "a"),
		"missing key then index":   paths.Root().Child("nope").Index(0),
		"missing key then child":   paths.Root().Child("nope", "deeper").Key(),
		"index then missing child": paths.Root().Child("items").Index(0).Child("x").Key(),
	}

	for name, path := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, err := path.Token(file.Docs[0])
			require.ErrorIs(t, err, paths.ErrNotFound)
			assert.Contains(t, err.Error(), path.String())

			_, err = path.Node(file.Docs[0])
			require.ErrorIs(t, err, paths.ErrNotFound)

			nodes, err := path.Nodes(file.Docs[0])
			require.NoError(t, err)
			assert.Empty(t, nodes)
		})
	}
}

func TestPath_Wildcards(t *testing.T) {
	t.Parallel()

	input := `
items:
  - name: a
    tags: [x, y]
  - name: b
    tags: [z]
meta:
  name: c
  nested:
    name: d
ref: &r
  name: e
alias: *r
chain:
  a:
    a:
      a: 1
merged:
  <<: *r
`

	source := niceyaml.NewSourceFromString(input)
	file, err := source.File()
	require.NoError(t, err)

	tcs := map[string]struct {
		path paths.Path
		want []string
	}{
		"index all": {
			path: paths.Root().Child("items").IndexAll().Child("name"),
			want: []string{"a", "b"},
		},
		"index all then index all": {
			path: paths.Root().Child("items").IndexAll().Child("tags").IndexAll(),
			want: []string{"x", "y", "z"},
		},
		"index all on a mapping matches nothing": {
			path: paths.Root().Child("meta").IndexAll(),
			want: []string{},
		},
		"recursive visits anchors once and skips aliases": {
			path: paths.Root().Recursive("name"),
			want: []string{"a", "b", "c", "d", "e"},
		},
		"recursive below a child": {
			path: paths.Root().Child("meta").Recursive("name"),
			want: []string{"c", "d"},
		},
		"recursive then index": {
			path: paths.Root().Recursive("tags").Index(0),
			want: []string{"x", "z"},
		},
		"chained recursive yields each node once": {
			// The inner ..a reaches the scalar 1 from two outer matches. The
			// mapping {a: 1} prints as its ":" token.
			path: paths.Root().Child("chain").Recursive("a").Recursive("a"),
			want: []string{":", "1"},
		},
		"recursive skips merge sources": {
			path: paths.Root().Child("merged").Recursive("name"),
			want: []string{},
		},
		"single match": {
			path: paths.Root().Child("meta", "name"),
			want: []string{"c"},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			nodes, err := tc.path.Nodes(file.Docs[0])
			require.NoError(t, err)

			got := make([]string, 0, len(nodes))
			for _, n := range nodes {
				got = append(got, n.GetToken().Value)
			}

			assert.Equal(t, tc.want, got)
		})
	}

	t.Run("token and node refuse wildcards", func(t *testing.T) {
		t.Parallel()

		path := paths.Root().Child("items").IndexAll().Child("name")

		_, err := path.Token(file.Docs[0])
		require.ErrorIs(t, err, paths.ErrWildcard)

		_, err = path.Node(file.Docs[0])
		require.ErrorIs(t, err, paths.ErrWildcard)

		_, err = paths.Root().Recursive("name").Key().Token(file.Docs[0])
		require.ErrorIs(t, err, paths.ErrWildcard)
	})

	t.Run("nodes rejects a nil document", func(t *testing.T) {
		t.Parallel()

		_, err := paths.Root().Nodes(nil)
		require.ErrorIs(t, err, paths.ErrNoDocument)
	})
}

func TestPath_Token_Keys(t *testing.T) {
	t.Parallel()

	input := `
"quoted key": 1
'single': 2
plain.dotted: 3
7: 4
? complex
: 5
empty: {}
none: []
!!str tagged: 6
&k anchored: 7
`

	source := niceyaml.NewSourceFromString(input)
	file, err := source.File()
	require.NoError(t, err)

	tcs := map[string]struct {
		path      paths.Path
		wantValue string
	}{
		"double quoted key": {
			path:      paths.Root().Child("quoted key"),
			wantValue: "1",
		},
		"single quoted key": {
			path:      paths.Root().Child("single"),
			wantValue: "2",
		},
		"dotted key": {
			path:      paths.Root().Child("plain.dotted"),
			wantValue: "3",
		},
		"integer key": {
			path:      paths.Root().Child("7"),
			wantValue: "4",
		},
		"explicit key": {
			path:      paths.Root().Child("complex"),
			wantValue: "5",
		},
		"explicit key target is the key itself, not the indicator": {
			path:      paths.Root().Child("complex").Key(),
			wantValue: "complex",
		},
		"tagged key matches by content": {
			path:      paths.Root().Child("tagged"),
			wantValue: "6",
		},
		"tagged key target skips the tag": {
			path:      paths.Root().Child("tagged").Key(),
			wantValue: "tagged",
		},
		"anchored key matches by content": {
			path:      paths.Root().Child("anchored"),
			wantValue: "7",
		},
		"anchored key target skips the anchor": {
			path:      paths.Root().Child("anchored").Key(),
			wantValue: "anchored",
		},
		"empty flow mapping value target": {
			path:      paths.Root().Child("empty"),
			wantValue: "{",
		},
		"empty flow sequence value target": {
			path:      paths.Root().Child("none"),
			wantValue: "[",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			tk, err := tc.path.Token(file.Docs[0])
			require.NoError(t, err)
			assert.Equal(t, tc.wantValue, tk.Value)
		})
	}

	t.Run("tag and anchor text are not key names", func(t *testing.T) {
		t.Parallel()

		for _, name := range []string{"!!str", "&k", "k", "&", "?"} {
			_, err := paths.Root().Child(name).Node(file.Docs[0])
			require.ErrorIs(t, err, paths.ErrNotFound, "Child(%q)", name)
		}
	})
}
