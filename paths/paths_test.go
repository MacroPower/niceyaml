package paths_test

import (
	"testing"

	"github.com/goccy/go-yaml/token"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/paths"
)

func TestRoot(t *testing.T) {
	t.Parallel()

	t.Run("empty path with value target", func(t *testing.T) {
		t.Parallel()

		path := paths.Root().Value()

		require.NotNil(t, path)
		assert.Equal(t, "$.(value)", path.String())
		assert.Equal(t, paths.PartValue, path.Part())
	})

	t.Run("single child with key target", func(t *testing.T) {
		t.Parallel()

		path := paths.Root().Child("kind").Key()

		require.NotNil(t, path)
		assert.Equal(t, "$.kind.(key)", path.String())
		assert.Equal(t, paths.PartKey, path.Part())
	})

	t.Run("multiple children with value target", func(t *testing.T) {
		t.Parallel()

		path := paths.Root().Child("metadata", "name").Value()

		require.NotNil(t, path)
		assert.Equal(t, "$.metadata.name.(value)", path.String())
		assert.Equal(t, paths.PartValue, path.Part())
	})

	t.Run("deeply nested path with key target", func(t *testing.T) {
		t.Parallel()

		path := paths.Root().Child("spec", "template", "spec", "containers").Key()

		require.NotNil(t, path)
		assert.Equal(t, "$.spec.template.spec.containers.(key)", path.String())
		assert.Equal(t, paths.PartKey, path.Part())
	})
}

func TestPathBuilder(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		want   string
		target paths.Part
		build  func() *paths.Path
	}{
		"returns root path": {
			build: func() *paths.Path {
				return paths.Root().Value()
			},
			target: paths.PartValue,
			want:   "$.(value)",
		},
		"can chain children": {
			build: func() *paths.Path {
				return paths.Root().Child("metadata", "labels").Key()
			},
			target: paths.PartKey,
			want:   "$.metadata.labels.(key)",
		},
		"can chain index": {
			build: func() *paths.Path {
				return paths.Root().Child("items").Index(0).Value()
			},
			target: paths.PartValue,
			want:   "$.items[0].(value)",
		},
		"variadic index": {
			build: func() *paths.Path {
				return paths.Root().Child("matrix").Index(0, 1).Value()
			},
			target: paths.PartValue,
			want:   "$.matrix[0][1].(value)",
		},
		"index all": {
			build: func() *paths.Path {
				return paths.Root().Child("items").IndexAll().Key()
			},
			target: paths.PartKey,
			want:   "$.items[*].(key)",
		},
		"recursive descent": {
			build: func() *paths.Path {
				return paths.Root().Recursive("name").Value()
			},
			target: paths.PartValue,
			want:   "$..name.(value)",
		},
		"large index": {
			build: func() *paths.Path {
				return paths.Root().Child("items").Index(999).Value()
			},
			target: paths.PartValue,
			want:   "$.items[999].(value)",
		},
		"deep nested indices": {
			build: func() *paths.Path {
				return paths.Root().Child("matrix").Index(0).Index(1).Index(2).Value()
			},
			target: paths.PartValue,
			want:   "$.matrix[0][1][2].(value)",
		},
		"recursive with index": {
			build: func() *paths.Path {
				return paths.Root().Recursive("items").Index(0).Key()
			},
			target: paths.PartKey,
			want:   "$..items[0].(key)",
		},
		"multiple recursive": {
			build: func() *paths.Path {
				return paths.Root().Recursive("containers").Recursive("name").Value()
			},
			target: paths.PartValue,
			want:   "$..containers..name.(value)",
		},
		"child after index": {
			build: func() *paths.Path {
				return paths.Root().Child("items").Index(0).Child("name").Value()
			},
			target: paths.PartValue,
			want:   "$.items[0].name.(value)",
		},
		"index all then index": {
			build: func() *paths.Path {
				return paths.Root().Child("matrix").IndexAll().Index(0).Value()
			},
			target: paths.PartValue,
			want:   "$.matrix[*][0].(value)",
		},
		"special characters in child name are quoted": {
			build: func() *paths.Path {
				return paths.Root().Child("kubernetes.io/name").Value()
			},
			target: paths.PartValue,
			want:   "$.'kubernetes.io/name'.(value)",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			path := tc.build()

			require.NotNil(t, path)
			assert.Equal(t, tc.want, path.String())
			assert.Equal(t, tc.target, path.Part())
			assert.NotNil(t, path.Path()) // Underlying yaml.Path should be accessible.
		})
	}
}

func TestPath_NilHandling(t *testing.T) {
	t.Parallel()

	t.Run("nil path returns empty string", func(t *testing.T) {
		t.Parallel()

		var path *paths.Path
		assert.Empty(t, path.String())
	})

	t.Run("nil path returns Value target", func(t *testing.T) {
		t.Parallel()

		var path *paths.Path
		assert.Equal(t, paths.PartValue, path.Part())
	})

	t.Run("nil path returns nil underlying path", func(t *testing.T) {
		t.Parallel()

		var path *paths.Path
		assert.Nil(t, path.Path())
	})
}

func TestPath_Token(t *testing.T) {
	t.Parallel()

	yaml := `
name: test
kind: Service
metadata:
  labels:
    app: myapp
items:
  - first
  - second
`

	source := niceyaml.NewSourceFromString(yaml)
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

			tk, err := tc.path.Token(file)
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

	_, err = path.Token(file)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nil path")
}

func TestPath_Token_InvalidPath(t *testing.T) {
	t.Parallel()

	source := niceyaml.NewSourceFromString(`name: test`)
	file, err := source.File()
	require.NoError(t, err)

	path := paths.Root().Child("nonexistent").Value()
	_, err = path.Token(file)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "filter from ast.File by YAMLPath")
}

func TestPath_Token_EmptyDocs(t *testing.T) {
	t.Parallel()

	// Test findKeyToken edge case where file has no docs.
	source := niceyaml.NewSourceFromString(``)
	file, err := source.File()
	require.NoError(t, err)

	path := paths.Root().Child("name").Key()
	_, err = path.Token(file)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "filter from ast.File by YAMLPath")
}

func TestPath_Token_NestedStructures(t *testing.T) {
	t.Parallel()

	yaml := `
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

	source := niceyaml.NewSourceFromString(yaml)
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

			tk, err := tc.path.Token(file)
			require.NoError(t, err)
			require.NotNil(t, tk)
			assert.Equal(t, tc.wantValue, tk.Value)
			assert.Equal(t, tc.wantType, tk.Type)
		})
	}
}
