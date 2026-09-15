package paths_test

import (
	"testing"

	"github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/token"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/paths"
)

func TestRoot(t *testing.T) {
	t.Parallel()

	t.Run("empty path targets the node", func(t *testing.T) {
		t.Parallel()

		path := paths.Root().Path()

		require.NotNil(t, path)
		assert.Equal(t, "$", path.String())
		assert.Equal(t, paths.PartNode, path.Part())
	})

	t.Run("empty path with value target", func(t *testing.T) {
		t.Parallel()

		path := paths.Root().Value()

		require.NotNil(t, path)
		assert.Equal(t, "$", path.String())
		assert.Equal(t, paths.PartValue, path.Part())
	})

	t.Run("single child with key target", func(t *testing.T) {
		t.Parallel()

		path := paths.Root().Child("kind").Key()

		require.NotNil(t, path)
		assert.Equal(t, "$.kind", path.String())
		assert.Equal(t, paths.PartKey, path.Part())
	})

	t.Run("multiple children with value target", func(t *testing.T) {
		t.Parallel()

		path := paths.Root().Child("metadata", "name").Value()

		require.NotNil(t, path)
		assert.Equal(t, "$.metadata.name", path.String())
		assert.Equal(t, paths.PartValue, path.Part())
	})
}

func TestBuilder(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		build func() *paths.Path
		want  string
		part  paths.Part
	}{
		"root path": {
			build: func() *paths.Path { return paths.Root().Path() },
			part:  paths.PartNode,
			want:  "$",
		},
		"chained children": {
			build: func() *paths.Path { return paths.Root().Child("metadata", "labels").Key() },
			part:  paths.PartKey,
			want:  "$.metadata.labels",
		},
		"child then index": {
			build: func() *paths.Path { return paths.Root().Child("items").Index(0).Value() },
			part:  paths.PartValue,
			want:  "$.items[0]",
		},
		"variadic index": {
			build: func() *paths.Path { return paths.Root().Child("matrix").Index(0, 1).Path() },
			part:  paths.PartNode,
			want:  "$.matrix[0][1]",
		},
		"index all": {
			build: func() *paths.Path { return paths.Root().Child("items").IndexAll().Key() },
			part:  paths.PartKey,
			want:  "$.items[*]",
		},
		"recursive descent": {
			build: func() *paths.Path { return paths.Root().Recursive("name").Value() },
			part:  paths.PartValue,
			want:  "$..name",
		},
		"large index": {
			build: func() *paths.Path { return paths.Root().Child("items").Index(999).Value() },
			part:  paths.PartValue,
			want:  "$.items[999]",
		},
		"recursive with index": {
			build: func() *paths.Path { return paths.Root().Recursive("items").Index(0).Key() },
			part:  paths.PartKey,
			want:  "$..items[0]",
		},
		"multiple recursive": {
			build: func() *paths.Path { return paths.Root().Recursive("containers").Recursive("name").Value() },
			part:  paths.PartValue,
			want:  "$..containers..name",
		},
		"child after index": {
			build: func() *paths.Path { return paths.Root().Child("items").Index(0).Child("name").Value() },
			part:  paths.PartValue,
			want:  "$.items[0].name",
		},
		"index all then index": {
			build: func() *paths.Path { return paths.Root().Child("matrix").IndexAll().Index(0).Value() },
			part:  paths.PartValue,
			want:  "$.matrix[*][0]",
		},
		"dotted child name is quoted": {
			build: func() *paths.Path { return paths.Root().Child("kubernetes.io/name").Value() },
			part:  paths.PartValue,
			want:  "$.'kubernetes.io/name'",
		},
		"child name with quote is quoted and escaped": {
			build: func() *paths.Path { return paths.Root().Child("it's").Path() },
			part:  paths.PartNode,
			want:  `$.'it\'s'`,
		},
		"child name with brackets is quoted": {
			build: func() *paths.Path { return paths.Root().Child("a[0]").Path() },
			part:  paths.PartNode,
			want:  "$.'a[0]'",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			path := tc.build()

			require.NotNil(t, path)
			assert.Equal(t, tc.want, path.String())
			assert.Equal(t, tc.part, path.Part())
			assert.Equal(t, tc.want, path.YAMLPath().String())
		})
	}
}

func TestBuilder_Immutable(t *testing.T) {
	t.Parallel()

	t.Run("shared prefix is not aliased", func(t *testing.T) {
		t.Parallel()

		spec := paths.Root().Child("spec")
		replicas := spec.Child("replicas").Value()
		image := spec.Child("image").Value()

		assert.Equal(t, "$.spec.replicas", replicas.String())
		assert.Equal(t, "$.spec.image", image.String())
		assert.Equal(t, "$.spec", spec.Value().String())
	})

	t.Run("finalizing does not consume the builder", func(t *testing.T) {
		t.Parallel()

		b := paths.Root().Child("metadata", "name")

		assert.Equal(t, paths.PartKey, b.Key().Part())
		assert.Equal(t, paths.PartValue, b.Value().Part())
		assert.Equal(t, "$.metadata.name.labels", b.Child("labels").Value().String())
	})
}

func TestPath_KeyValue(t *testing.T) {
	t.Parallel()

	t.Run("derives copies with a different part", func(t *testing.T) {
		t.Parallel()

		node := paths.MustParse("$.a.b")
		key := node.Key()
		value := node.Value()

		assert.Equal(t, paths.PartNode, node.Part())
		assert.Equal(t, paths.PartKey, key.Part())
		assert.Equal(t, paths.PartValue, value.Part())
		assert.Equal(t, "$.a.b", key.String())
		assert.Equal(t, "$.a.b", value.String())
		assert.Equal(t, paths.PartKey, value.Key().Part())
	})

	t.Run("nil receiver returns nil", func(t *testing.T) {
		t.Parallel()

		var path *paths.Path

		assert.Nil(t, path.Key())
		assert.Nil(t, path.Value())
	})
}

func TestPart_String(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "node", paths.PartNode.String())
	assert.Equal(t, "key", paths.PartKey.String())
	assert.Equal(t, "value", paths.PartValue.String())
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
		"non-numeric index": {
			expr: "$[a]",
		},
		"wildcard child": {
			expr: "$.*",
		},
		"unterminated quote": {
			expr: "$.'foo",
		},
		"empty quoted key": {
			expr: "$.''",
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
			assert.Nil(t, p)
			assert.Contains(t, err.Error(), "parse path")
		})
	}
}

func TestParse_RoundTrip(t *testing.T) {
	t.Parallel()

	tcs := map[string]*paths.Path{
		"root":             paths.Root().Path(),
		"children":         paths.Root().Child("a", "b").Key(),
		"index":            paths.Root().Child("items").Index(3).Value(),
		"wildcards":        paths.Root().Child("items").IndexAll().Recursive("name").Path(),
		"dotted name":      paths.Root().Child("kubernetes.io/name").Value(),
		"quote in name":    paths.Root().Child("it's").Path(),
		"backslash":        paths.Root().Child(`a\b.c`).Path(),
		"brackets":         paths.Root().Child("a[0]").Path(),
		"dollar":           paths.Root().Child("$ref").Path(),
		"star":             paths.Root().Child("*").Path(),
		"numeric name":     paths.Root().Child("1").Path(),
		"space in name":    paths.Root().Child("has space").Path(),
		"only reserved":    paths.Root().Child(".").Path(),
		"goccy compatible": paths.Root().Child("a.b").Index(1).Child("c").Path(),
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

func TestPath_NilHandling(t *testing.T) {
	t.Parallel()

	t.Run("nil path returns empty string", func(t *testing.T) {
		t.Parallel()

		var path *paths.Path

		assert.Empty(t, path.String())
	})

	t.Run("nil path targets the node", func(t *testing.T) {
		t.Parallel()

		var path *paths.Path

		assert.Equal(t, paths.PartNode, path.Part())
	})

	t.Run("nil path returns nil YAMLPath", func(t *testing.T) {
		t.Parallel()

		var path *paths.Path

		assert.Nil(t, path.YAMLPath())
	})
}

func TestPath_YAMLPath(t *testing.T) {
	t.Parallel()

	source := niceyaml.NewSourceFromString("a:\n  'b.c': [x, y]\n")
	file, err := source.File()
	require.NoError(t, err)

	yp := paths.Root().Child("a", "b.c").Index(1).Value().YAMLPath()
	require.NotNil(t, yp)
	assert.Equal(t, "$.a.'b.c'[1]", yp.String())

	node, err := yp.FilterNode(file.Docs[0].Body)
	require.NoError(t, err)
	assert.Equal(t, "y", node.GetToken().Value)
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
		path      *paths.Path
		wantValue string
		wantType  token.Type
	}{
		"root value returns mapping separator": {
			path:      paths.Root().Value(),
			wantValue: ":",
			wantType:  token.MappingValueType,
		},
		"simple key target returns key token": {
			path:      paths.Root().Child("name").Key(),
			wantValue: "name",
			wantType:  token.StringType,
		},
		"simple value target returns value token": {
			path:      paths.Root().Child("name").Value(),
			wantValue: "test",
			wantType:  token.StringType,
		},
		"node target returns value token": {
			path:      paths.Root().Child("name").Path(),
			wantValue: "test",
			wantType:  token.StringType,
		},
		"nested key target": {
			path:      paths.Root().Child("metadata", "labels", "app").Key(),
			wantValue: "app",
			wantType:  token.StringType,
		},
		"nested value target": {
			path:      paths.Root().Child("metadata", "labels", "app").Value(),
			wantValue: "myapp",
			wantType:  token.StringType,
		},
		"array element value target": {
			path:      paths.Root().Child("items").Index(0).Value(),
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

func TestPath_Token_NilPath(t *testing.T) {
	t.Parallel()

	source := niceyaml.NewSourceFromString(`name: test`)
	file, err := source.File()
	require.NoError(t, err)

	var path *paths.Path

	_, err = path.Token(file.Docs[0])
	require.ErrorIs(t, err, paths.ErrNilPath)

	_, err = path.Node(file.Docs[0])
	require.ErrorIs(t, err, paths.ErrNilPath)
}

func TestPath_Token_InvalidPath(t *testing.T) {
	t.Parallel()

	source := niceyaml.NewSourceFromString(`name: test`)
	file, err := source.File()
	require.NoError(t, err)

	path := paths.Root().Child("nonexistent").Value()
	_, err = path.Token(file.Docs[0])
	require.ErrorIs(t, err, yaml.ErrNotFoundNode)
}

func TestPath_Token_NoDocument(t *testing.T) {
	t.Parallel()

	path := paths.Root().Child("name").Key()

	_, err := path.Token(nil)
	require.ErrorIs(t, err, paths.ErrNoDocument)

	_, err = path.Token(&ast.DocumentNode{})
	require.ErrorIs(t, err, paths.ErrNoDocument)
}

func TestPath_Token_MultipleDocuments(t *testing.T) {
	t.Parallel()

	source := niceyaml.NewSourceFromString("name: first\n---\nname: second\n")
	file, err := source.File()
	require.NoError(t, err)
	require.Len(t, file.Docs, 2)

	path := paths.Root().Child("name").Value()

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
		path      *paths.Path
		wantValue string
		wantType  token.Type
	}{
		"nested array first element name": {
			path:      paths.Root().Child("list").Index(0).Child("name").Value(),
			wantValue: "first",
			wantType:  token.StringType,
		},
		"nested array second element items first": {
			path:      paths.Root().Child("list").Index(1).Child("items").Index(0).Value(),
			wantValue: "c",
			wantType:  token.StringType,
		},
		"deeply nested value": {
			path:      paths.Root().Child("nested", "deep", "deeper", "value").Value(),
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
	for _, path := range []*paths.Path{
		paths.Root().Child("a", "b").Path(),
		paths.Root().Child("a", "b").Key(),
		paths.Root().Child("a", "b").Value(),
	} {
		node, err := path.Node(file.Docs[0])
		require.NoError(t, err)

		seq, ok := node.(*ast.SequenceNode)
		require.True(t, ok, "want *ast.SequenceNode, got %T", node)
		assert.Len(t, seq.Values, 2)
	}
}
