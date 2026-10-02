package paths_test

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/parser"
	"github.com/goccy/go-yaml/token"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.jacobcolvin.com/x/stringtest"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/paths"
)

func TestRoot(t *testing.T) {
	t.Parallel()

	t.Run("empty path is the root", func(t *testing.T) {
		t.Parallel()

		path := paths.Root()

		require.NotNil(t, path)
		assert.Equal(t, "$", path.String())
	})

	t.Run("single child", func(t *testing.T) {
		t.Parallel()

		path := paths.Root().Child("kind")

		require.NotNil(t, path)
		assert.Equal(t, "$.kind", path.String())
	})

	t.Run("multiple children", func(t *testing.T) {
		t.Parallel()

		path := paths.Root().Child("metadata", "name")

		require.NotNil(t, path)
		assert.Equal(t, "$.metadata.name", path.String())
	})
}

func TestPath_Build(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		build    func() paths.Path
		want     string
		wantYAML string
	}{
		"root path": {
			build: paths.Root,
			want:  "$",
		},
		"chained children": {
			build: func() paths.Path { return paths.Root().Child("metadata", "labels") },
			want:  "$.metadata.labels",
		},
		"child then index": {
			build: func() paths.Path { return paths.Root().Child("items").Index(0) },
			want:  "$.items[0]",
		},
		"variadic index": {
			build: func() paths.Path { return paths.Root().Child("matrix").Index(0, 1) },
			want:  "$.matrix[0][1]",
		},
		"index all": {
			build: func() paths.Path { return paths.Root().Child("items").IndexAll() },
			want:  "$.items[*]",
		},
		"recursive descent": {
			build: func() paths.Path { return paths.Root().Recursive("name") },
			want:  "$..name",
		},
		"large index": {
			build: func() paths.Path { return paths.Root().Child("items").Index(999) },
			want:  "$.items[999]",
		},
		"recursive with index": {
			build: func() paths.Path { return paths.Root().Recursive("items").Index(0) },
			want:  "$..items[0]",
		},
		"multiple recursive": {
			build: func() paths.Path { return paths.Root().Recursive("containers").Recursive("name") },
			want:  "$..containers..name",
		},
		"child after index": {
			build: func() paths.Path { return paths.Root().Child("items").Index(0).Child("name") },
			want:  "$.items[0].name",
		},
		"index all then index": {
			build: func() paths.Path { return paths.Root().Child("matrix").IndexAll().Index(0) },
			want:  "$.matrix[*][0]",
		},
		"dotted child name is quoted": {
			build: func() paths.Path { return paths.Root().Child("kubernetes.io/name") },
			want:  "$.'kubernetes.io/name'",
		},
		"child name with quote is quoted and escaped": {
			build:    func() paths.Path { return paths.Root().Child("it's") },
			want:     `$.'it\'s'`,
			wantYAML: "$.it's",
		},
		"empty child name is quoted, but goccy leaves it bare": {
			build:    func() paths.Path { return paths.Root().Child("") },
			want:     "$.''",
			wantYAML: "$.",
		},
		"empty recursive name is quoted, but goccy leaves it bare": {
			build:    func() paths.Path { return paths.Root().Recursive("") },
			want:     "$..''",
			wantYAML: "$..",
		},
		"child name with brackets is quoted": {
			build:    func() paths.Path { return paths.Root().Child("a[0]") },
			want:     "$.'a[0]'",
			wantYAML: "$.a[0]",
		},
		"recursive name with a dot is quoted, but goccy cannot quote it": {
			build:    func() paths.Path { return paths.Root().Recursive("x.y") },
			want:     "$..'x.y'",
			wantYAML: "$..x.y",
		},
		"key selector, which goccy has no form for": {
			build:    func() paths.Path { return paths.Root().Child("spec", "name").Key() },
			want:     "$.spec.name~",
			wantYAML: "$.spec.name",
		},
		"key selector mid-path": {
			build:    func() paths.Path { return paths.Root().Child("a").Key().Child("b") },
			want:     "$.a~.b",
			wantYAML: "$.a.b",
		},
		"name wrapped in single quotes": {
			build:    func() paths.Path { return paths.Root().Child("'x'") },
			want:     `$.'\'x\''`,
			wantYAML: "$.'x'",
		},
		"tilde in a name is quoted": {
			build:    func() paths.Path { return paths.Root().Child("a~b") },
			want:     "$.'a~b'",
			wantYAML: "$.a~b",
		},
		"colon and space in a name are quoted": {
			build:    func() paths.Path { return paths.Root().Child("x: y") },
			want:     "$.'x: y'",
			wantYAML: "$.x: y",
		},
		"trailing space in a name is quoted": {
			build:    func() paths.Path { return paths.Root().Child("name ") },
			want:     "$.'name '",
			wantYAML: "$.name ",
		},
		"line break in a name is quoted": {
			build:    func() paths.Path { return paths.Root().Child("a\nb") },
			want:     "$.'a\nb'",
			wantYAML: "$.a\nb",
		},
		"recursive name with a space is quoted": {
			build:    func() paths.Path { return paths.Root().Recursive("a b") },
			want:     "$..'a b'",
			wantYAML: "$..a b",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			path := tc.build()

			require.NotNil(t, path)
			assert.Equal(t, tc.want, path.String())

			// The goccy form only differs where goccy quotes a name
			// differently or has no form for a selector.
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

	t.Run("extending does not change the receiver", func(t *testing.T) {
		t.Parallel()

		p := paths.Root().Child("metadata", "name")

		assert.Equal(t, "$.metadata.name.labels", p.Child("labels").String())
		assert.Equal(t, "$.metadata.name", p.String())
	})

	t.Run("zero value is the root", func(t *testing.T) {
		t.Parallel()

		var p paths.Path

		assert.Equal(t, paths.Root(), p)
		assert.Equal(t, "$", p.String())
		assert.Equal(t, "$.a", p.Child("a").String())
	})

	t.Run("an empty extension is the root", func(t *testing.T) {
		t.Parallel()

		assert.Equal(t, paths.Root(), paths.Root().Child())
		assert.Equal(t, paths.Root(), paths.Root().Index())
		assert.Equal(t, paths.Root(), paths.Root().Join(paths.Root()))
		assert.Equal(t, paths.Root().Child("a"), paths.Root().Child("a").Child())
	})
}

func TestPath_Join(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		p    paths.Path
		want string
		qs   []paths.Path
	}{
		"root to root": {
			p:    paths.Root(),
			qs:   []paths.Path{paths.Root()},
			want: "$",
		},
		"root to path": {
			p:    paths.Root(),
			qs:   []paths.Path{paths.Root().Child("open")},
			want: "$.open",
		},
		"path to root": {
			p:    paths.Root().Child("spec", "hours"),
			qs:   []paths.Path{paths.Root()},
			want: "$.spec.hours",
		},
		"path to path": {
			p:    paths.Root().Child("spec", "hours"),
			qs:   []paths.Path{paths.Root().Child("open")},
			want: "$.spec.hours.open",
		},
		"keeps every selector kind": {
			p:    paths.Root().Child("items").Index(0),
			qs:   []paths.Path{paths.Root().Recursive("name").IndexAll().Key()},
			want: "$.items[0]..name[*]~",
		},
		"nothing": {
			p:    paths.Root().Child("spec"),
			want: "$.spec",
		},
		"several paths in order": {
			p:    paths.Root().Child("spec"),
			qs:   []paths.Path{paths.Root().Child("hours"), paths.Root(), paths.Root().Index(1).Key()},
			want: "$.spec.hours[1]~",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, tc.p.Join(tc.qs...).String())
			assert.Equal(t, paths.MustParse(tc.want), tc.p.Join(tc.qs...))
		})
	}

	t.Run("leaves both paths as they were", func(t *testing.T) {
		t.Parallel()

		p := paths.Root().Child("spec")
		q := paths.Root().Child("open")
		joined := p.Join(q)

		assert.Equal(t, "$.spec.open", joined.String())
		assert.Equal(t, "$.spec", p.String())
		assert.Equal(t, "$.open", q.String())
		assert.Equal(t, "$.spec.open.x", joined.Child("x").String())
		assert.Equal(t, "$.spec", p.String())
	})
}

func TestPath_IsRoot(t *testing.T) {
	t.Parallel()

	var zero paths.Path

	assert.True(t, paths.Root().IsRoot())
	assert.True(t, zero.IsRoot())
	assert.True(t, paths.MustParse("$").IsRoot())
	assert.False(t, paths.Root().Child("a").IsRoot())
	assert.False(t, paths.Root().Key().IsRoot())
	assert.False(t, paths.Root().Index(0).IsRoot())
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
		"quoted key with escaped backslash": {
			expr: `$.'a\\b.c'`,
			want: `$.'a\\b.c'`,
		},
		"backslash before another character is dropped": {
			expr: `$.'C:\temp'`,
			want: `$.'C:temp'`,
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
		"key selector": {
			expr: "$.spec.name~",
			want: "$.spec.name~",
		},
		"key selector on the root": {
			expr: "$~",
			want: "$~",
		},
		"key selector after an index": {
			expr: "$.items[0]~",
			want: "$.items[0]~",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			p, err := paths.Parse(tc.expr)

			require.NoError(t, err)
			require.NotNil(t, p)
			assert.Equal(t, tc.want, p.String())
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
		"index overflows int": {
			expr: "$[99999999999999999999]",
		},
		"wildcard child": {
			expr: "$.*",
		},
		"unterminated quote": {
			expr: "$.'foo",
		},
		"unterminated escape": {
			expr: `$.'a\`,
		},
		"bare text after root": {
			expr: "$foo",
		},
		"bare text after key selector": {
			expr: "$.a~b",
		},
		"non-ASCII after root": {
			expr: "$é",
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

func TestParse_ErrorMessage(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		expr string
		want string
	}{
		"ASCII after root": {
			expr: "$a",
			want: `parse path "$a": invalid path: unexpected 'a' at 1`,
		},
		"non-ASCII after root": {
			expr: "$é",
			want: `parse path "$é": invalid path: unexpected 'é' at 1`,
		},
		"non-ASCII after index": {
			expr: "$[0]日",
			want: `parse path "$[0]日": invalid path: unexpected '日' at 4`,
		},
		"invalid UTF-8 after quoted name": {
			expr: "$.'a'\xff",
			want: `parse path "$.'a'\xff": invalid path: unexpected "\xff" at 5`,
		},
		"index with a leading zero": {
			expr: "$[01]",
			want: `parse path "$[01]": invalid path: index "01": not a canonical non-negative integer`,
		},
		"index overflows int": {
			expr: "$[99999999999999999999]",
			want: `parse path "$[99999999999999999999]": invalid path: ` +
				`index "99999999999999999999": out of range`,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, err := paths.Parse(tc.expr)
			require.EqualError(t, err, tc.want)
		})
	}
}

func TestParse_RoundTrip(t *testing.T) {
	t.Parallel()

	tcs := map[string]paths.Path{
		"root":             paths.Root(),
		"children":         paths.Root().Child("a", "b"),
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
		"colon in name":    paths.Root().Child("x: y"),
		"trailing space":   paths.Root().Child("name "),
		"line break":       paths.Root().Child("a\nb"),
		"only reserved":    paths.Root().Child("."),
		"tilde in name":    paths.Root().Child("a~b"),
		"goccy compatible": paths.Root().Child("a.b").Index(1).Child("c"),
	}

	for name, want := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, err := paths.Parse(want.String())
			require.NoError(t, err)

			assert.Equal(t, want, got)

			// The goccy parser accepts the same expression.
			_, err = yaml.PathString(want.String())
			require.NoError(t, err)
		})
	}
}

func TestParse_RoundTrip_Key(t *testing.T) {
	t.Parallel()

	// The goccy parser has no key selector, so a key path stays out of the
	// table above, but Parse reads back what String writes.
	tcs := map[string]paths.Path{
		"key":               paths.Root().Child("a").Key(),
		"key of root":       paths.Root().Key(),
		"key of element":    paths.Root().Child("items").Index(0).Key(),
		"key then child":    paths.Root().Child("a").Key().Child("b"),
		"quoted name key":   paths.Root().Child("a.b").Key(),
		"recursive key":     paths.Root().Recursive("name").Key(),
		"empty name key":    paths.Root().Child("").Key(),
		"tilde in name key": paths.Root().Child("a~b").Key(),
	}

	for name, want := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, err := paths.Parse(want.String())
			require.NoError(t, err)
			assert.Equal(t, want, got)
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

	source := niceyaml.NewSourceFromString(
		"a:\n  'b.c': [x, y]\n  don't: 1\n  a\\.b: 2\n  a.b'c: 3\n" +
			"'':\n  a'.b: 4\n  a'*b: 5\n  a.b: 6\n",
	)
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
		"dotted name with a quote filters by its raw text": {
			path:       paths.Root().Child("a", "a.b'c"),
			wantString: `$.a.'a.b\'c'`,
			want:       "3",
		},
		"dotted name with a quote via builder fallback": {
			path:       paths.Root().Child("", "a'.b"),
			wantString: "$..'a'.b'",
			want:       "4",
		},
		"star name with a quote via builder fallback": {
			path:       paths.Root().Child("", "a'*b"),
			wantString: "$..'a'*b'",
			want:       "5",
		},
		"dotted name via builder fallback": {
			path:       paths.Root().Child("", "a.b"),
			wantString: "$..'a.b'",
			want:       "6",
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

func TestPath_YAMLPath_Replace(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		src  string
		path paths.Path
		want string
	}{
		"dotted name": {
			src:  "plain: 1\na.b: 2\n",
			path: paths.Root().Child("a.b"),
			want: "plain: 1\na.b: 9\n",
		},
		"star name": {
			src:  "plain: 1\na*b: 2\n",
			path: paths.Root().Child("a*b"),
			want: "plain: 1\na*b: 9\n",
		},
		"nested dotted label": {
			src:  "metadata:\n  labels:\n    app.kubernetes.io/name: web\n",
			path: paths.Root().Child("metadata", "labels", "app.kubernetes.io/name"),
			want: "metadata:\n  labels:\n    app.kubernetes.io/name: 9\n",
		},
		"empty name via builder fallback": {
			src:  "\"\": v\n",
			path: paths.Root().Child(""),
			want: "\"\": 9\n",
		},
		"dotted name with a quote via builder fallback matches no key": {
			src:  "\"\":\n  a'.b: v\n",
			path: paths.Root().Child("", "a'.b"),
			want: "\"\":\n  a'.b: v\n",
		},
		"name that is not valid UTF-8 matches no key": {
			src:  string(utf8.RuneError) + ": v\n",
			path: paths.Root().Child("\xff"),
			want: string(utf8.RuneError) + ": v\n",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			file, err := parser.ParseBytes([]byte(tc.src), 0)
			require.NoError(t, err)

			replacement, err := parser.ParseBytes([]byte("9"), 0)
			require.NoError(t, err)

			err = tc.path.YAMLPath().ReplaceWithNode(file, replacement.Docs[0].Body)
			require.NoError(t, err)
			assert.Equal(t, tc.want, file.String())
		})
	}
}

func TestPath_YAMLPath_Limits(t *testing.T) {
	t.Parallel()

	t.Run("goccy strips single quotes from a name", func(t *testing.T) {
		t.Parallel()

		file, err := parser.ParseBytes([]byte("x: plain\n\"'x'\": quoted\n"), 0)
		require.NoError(t, err)

		node, err := paths.Root().Child("'x'").YAMLPath().FilterNode(file.Docs[0].Body)
		require.NoError(t, err)
		require.NotNil(t, node, "node not found")
		assert.Equal(t, "plain", node.GetToken().Value)
	})

	t.Run("goccy drops a key selector mid-path", func(t *testing.T) {
		t.Parallel()

		file, err := parser.ParseBytes([]byte("a:\n  b: 1\n"), 0)
		require.NoError(t, err)

		path := paths.Root().Child("a").Key().Child("b")

		node, err := path.YAMLPath().FilterNode(file.Docs[0].Body)
		require.NoError(t, err)
		require.NotNil(t, node, "node not found")
		assert.Equal(t, "1", node.GetToken().Value)

		_, err = path.Node(file.Docs[0])
		require.ErrorIs(t, err, paths.ErrNotFound)
	})

	t.Run("goccy matches a decorated key by its indicator", func(t *testing.T) {
		t.Parallel()

		tcs := map[string]struct {
			input     string
			indicator string
		}{
			"anchored key": {
				input:     "m:\n  &a k: v\n",
				indicator: "&",
			},
			"tagged key": {
				input:     "m:\n  !!str k: v\n",
				indicator: "!!str",
			},
			"alias key": {
				input:     "b: &k k\nm:\n  *k : v\n",
				indicator: "*",
			},
			"explicit key": {
				input:     "m:\n  ? k\n  : v\n",
				indicator: "?",
			},
			"explicit block scalar key": {
				input:     "m:\n  ? |-\n    k\n  : v\n",
				indicator: "?",
			},
			"block scalar key without ?": {
				input:     "m:\n  |-\n    k\n  : v\n",
				indicator: "|-",
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				file, err := parser.ParseBytes([]byte(tc.input), 0)
				require.NoError(t, err)

				doc := file.Docs[0]

				byName := paths.Root().Child("m", "k")

				node, err := byName.Node(doc)
				require.NoError(t, err)
				assert.Equal(t, "v", node.GetToken().Value)

				node, err = byName.YAMLPath().FilterNode(doc.Body)
				require.NoError(t, err)
				assert.Nil(t, node)

				byIndicator := paths.Root().Child("m", tc.indicator)

				_, err = byIndicator.Node(doc)
				require.ErrorIs(t, err, paths.ErrNotFound)

				node, err = byIndicator.YAMLPath().FilterNode(doc.Body)
				require.NoError(t, err)
				require.NotNil(t, node, "node not found")
				assert.Equal(t, "v", node.GetToken().Value)
			})
		}
	})

	t.Run("goccy skips merged entries", func(t *testing.T) {
		t.Parallel()

		file, err := parser.ParseBytes([]byte("base: &b\n  k: v\nm:\n  <<: *b\n"), 0)
		require.NoError(t, err)

		path := paths.Root().Child("m", "k")

		node, err := path.Node(file.Docs[0])
		require.NoError(t, err)
		assert.Equal(t, "v", node.GetToken().Value)

		node, err = path.YAMLPath().FilterNode(file.Docs[0].Body)
		require.NoError(t, err)
		assert.Nil(t, node)
	})

	t.Run("goccy selects the first duplicate and replaces all", func(t *testing.T) {
		t.Parallel()

		input := "m:\n  k: first\n  k: last\n"

		file, err := parser.ParseBytes([]byte(input), 0, parser.AllowDuplicateMapKey())
		require.NoError(t, err)

		path := paths.Root().Child("m", "k")

		node, err := path.Node(file.Docs[0])
		require.NoError(t, err)
		assert.Equal(t, "last", node.GetToken().Value)

		node, err = path.YAMLPath().FilterNode(file.Docs[0].Body)
		require.NoError(t, err)
		require.NotNil(t, node, "node not found")
		assert.Equal(t, "first", node.GetToken().Value)

		replacement, err := parser.ParseBytes([]byte("new\n"), 0)
		require.NoError(t, err)

		err = path.YAMLPath().ReplaceWithNode(file, replacement.Docs[0].Body)
		require.NoError(t, err)
		assert.Equal(t, "m:\n  k: new\n  k: new\n", file.String())
	})
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
tag_first:
  !!str inner: 8
anchor_first:
  &ka inner: 9
explicit_first:
  ? inner
  : 10
`

	source := niceyaml.NewSourceFromString(input)
	file, err := source.File()
	require.NoError(t, err)

	tcs := map[string]struct {
		path      paths.Path
		wantValue string
		key       bool
		wantType  token.Type
	}{
		"root value returns the first key": {
			path:      paths.Root(),
			wantValue: "name",
			wantType:  token.StringType,
		},
		"root key returns the first key": {
			path:      paths.Root(),
			key:       true,
			wantValue: "name",
			wantType:  token.StringType,
		},
		"mapping value target returns its first key": {
			path:      paths.Root().Child("metadata"),
			wantValue: "labels",
			wantType:  token.StringType,
		},
		"mapping with a tagged first key starts at the key": {
			path:      paths.Root().Child("tag_first"),
			wantValue: "inner",
			wantType:  token.StringType,
		},
		"mapping with an anchored first key starts at the key": {
			path:      paths.Root().Child("anchor_first"),
			wantValue: "inner",
			wantType:  token.StringType,
		},
		"mapping with an explicit first key starts at the key": {
			path:      paths.Root().Child("explicit_first"),
			wantValue: "inner",
			wantType:  token.StringType,
		},
		"mapping key target returns the entry key": {
			path:      paths.Root().Child("metadata"),
			key:       true,
			wantValue: "metadata",
			wantType:  token.StringType,
		},
		"sequence value target returns its first element": {
			path:      paths.Root().Child("items"),
			wantValue: "first",
			wantType:  token.StringType,
		},
		"sequence key target returns the entry key": {
			path:      paths.Root().Child("items"),
			key:       true,
			wantValue: "items",
			wantType:  token.StringType,
		},
		"simple key target returns key token": {
			path:      paths.Root().Child("name"),
			key:       true,
			wantValue: "name",
			wantType:  token.StringType,
		},
		"simple value target returns value token": {
			path:      paths.Root().Child("name"),
			wantValue: "test",
			wantType:  token.StringType,
		},
		"nested key target": {
			path:      paths.Root().Child("metadata", "labels", "app"),
			key:       true,
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
			path:      paths.Root().Child("items").Index(1),
			key:       true,
			wantValue: "second",
			wantType:  token.StringType,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			tk := resolveToken(t, tc.path, tc.key, file.Docs[0])
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

	path := paths.Root().Child("name")

	for name, resolve := range map[string]func(*ast.DocumentNode) (*token.Token, error){
		"Token": path.Token,
		"Key":   path.Key().Token,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, err := resolve(nil)
			require.ErrorIs(t, err, paths.ErrNoDocument)
			require.ErrorIs(t, err, paths.ErrNotFound)

			_, err = resolve(&ast.DocumentNode{})
			require.ErrorIs(t, err, paths.ErrNoDocument)
			require.ErrorIs(t, err, paths.ErrNotFound)
			assert.Contains(t, err.Error(), "$.name")
		})
	}
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

func TestPath_WhitespaceDocument(t *testing.T) {
	t.Parallel()

	// The tokenizer gives a source the lexer emits nothing for one
	// placeholder token, which the parser reads as a plain scalar. That
	// scalar is not content, so nothing resolves in the document.
	tcs := map[string]struct {
		input string
	}{
		"newline":     {input: "\n"},
		"blank lines": {input: "\n\n"},
		"spaces":      {input: "   "},
		"lone bang":   {input: "!"},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			file, err := niceyaml.NewSourceFromString(tc.input).File()
			require.NoError(t, err)
			require.Len(t, file.Docs, 1)

			_, err = paths.Root().Node(file.Docs[0])
			require.ErrorIs(t, err, paths.ErrNoDocument)
			require.ErrorIs(t, err, paths.ErrNotFound)

			_, err = paths.Root().Child("a").Token(file.Docs[0])
			require.ErrorIs(t, err, paths.ErrNoDocument)
			require.ErrorIs(t, err, paths.ErrNotFound)

			_, err = paths.Root().Nodes(file.Docs[0])
			require.ErrorIs(t, err, paths.ErrNoDocument)
			require.ErrorIs(t, err, paths.ErrNotFound)
		})
	}
}

func TestPath_Token_UnresolvableAlias(t *testing.T) {
	t.Parallel()

	// Node looks through the alias and reports that it names no anchor, while
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
		key       bool
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
			path:      paths.Root().Child("nested", "deep", "deeper", "value"),
			key:       true,
			wantValue: "value",
			wantType:  token.StringType,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			tk := resolveToken(t, tc.path, tc.key, file.Docs[0])
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

	node, err := paths.Root().Child("a", "b").Node(file.Docs[0])
	require.NoError(t, err)

	seq, ok := node.(*ast.SequenceNode)
	require.True(t, ok, "want *ast.SequenceNode, got %T", node)
	assert.Len(t, seq.Values, 2)
}

func TestPath_Token_Anchors(t *testing.T) {
	t.Parallel()

	input := `
base: &b
  a: 1
  b: 2
flow: &f {x: 10, b: 200}
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
late:
  b: 30
  c: 31
  <<: *b
`

	source := niceyaml.NewSourceFromString(input)
	file, err := source.File()
	require.NoError(t, err)

	tcs := map[string]struct {
		path      paths.Path
		wantValue string
		key       bool
		wantLine  int
	}{
		"child of anchored mapping": {
			path:      paths.Root().Child("base", "a"),
			wantValue: "1",
			wantLine:  3,
		},
		"key of anchored mapping entry": {
			path:      paths.Root().Child("base", "a"),
			key:       true,
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
			path:      paths.Root().Child("other"),
			key:       true,
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
			path:      paths.Root().Child("merged", "a"),
			key:       true,
			wantValue: "a",
			wantLine:  3,
		},
		"own key after merge wins": {
			path:      paths.Root().Child("merged", "b"),
			wantValue: "20",
			wantLine:  14,
		},
		"merge after own key wins": {
			path:      paths.Root().Child("late", "b"),
			wantValue: "2",
			wantLine:  4,
		},
		"merge after own key wins at the key": {
			path:      paths.Root().Child("late", "b"),
			key:       true,
			wantValue: "b",
			wantLine:  4,
		},
		"own key before merge without that key": {
			path:      paths.Root().Child("late", "c"),
			wantValue: "31",
			wantLine:  25,
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
		"merge sequence later source wins": {
			path:      paths.Root().Child("multi", "b"),
			wantValue: "200",
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

			tk := resolveToken(t, tc.path, tc.key, file.Docs[0])
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

func TestPath_Node_TaggedKey(t *testing.T) {
	t.Parallel()

	// A `~` keeps a tag on the key, as a path to a value keeps a tag on
	// the value, while the token of the key is still the key text.
	tcs := map[string]struct {
		input     string
		want      string
		wantToken string
	}{
		"plain key": {
			input:     "2: x\n",
			want:      "2",
			wantToken: "2",
		},
		"tagged key": {
			input:     "!!str 2: x\n",
			want:      "!!str 2",
			wantToken: "2",
		},
		"explicit tagged key": {
			input:     "? !!str 2\n: x\n",
			want:      "!!str 2",
			wantToken: "2",
		},
		"anchored tagged key": {
			input:     "&k !!str 2: x\n",
			want:      "!!str 2",
			wantToken: "2",
		},
		"anchored key": {
			input:     "&k 2: x\n",
			want:      "2",
			wantToken: "2",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			doc := yamltest.FirstDocument(t, tc.input).DocumentAST()
			path := paths.Root().Child("2").Key()

			node, err := path.Node(doc)
			require.NoError(t, err)
			assert.Equal(t, tc.want, node.String())

			nodes, err := path.Nodes(doc)
			require.NoError(t, err)
			require.Len(t, nodes, 1)
			assert.Same(t, node, nodes[0])

			tk, err := path.Token(doc)
			require.NoError(t, err)
			assert.Equal(t, tc.wantToken, tk.Value)
		})
	}
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

func TestPath_TaggedAlias(t *testing.T) {
	t.Parallel()

	// A tag on an alias stays over the alias, and an alias under a tag
	// that does not resolve fails as one without a tag does.
	tcs := map[string]struct {
		err   error
		input string
		want  string
		path  paths.Path
	}{
		"alias to an anchor": {
			input: "x: &x {k: 1}\nb: !t *x\n",
			path:  paths.Root().Child("b"),
			want:  "!t *x",
		},
		"alias with no anchor before it": {
			input: "b: !t *nope\n",
			path:  paths.Root().Child("b"),
			err:   paths.ErrAlias,
		},
		"alias inside its own anchor": {
			input: "a: &x [!t *x]\n",
			path:  paths.Root().Child("a").Index(0),
			err:   paths.ErrAlias,
		},
		"anchor over a tag on an alias to itself": {
			input: "a: &x !t *x\n",
			path:  paths.Root().Child("a"),
			err:   paths.ErrAlias,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			dd := yamltest.FirstDocument(t, tc.input)
			doc := dd.DocumentAST()

			node, err := tc.path.Node(doc)
			_, nodesErr := tc.path.Nodes(doc)
			_, atErr := dd.At(tc.path)

			if tc.err != nil {
				require.ErrorIs(t, err, tc.err)
				require.ErrorIs(t, nodesErr, tc.err)
				require.ErrorIs(t, atErr, tc.err)

				return
			}

			require.NoError(t, err)
			require.NoError(t, nodesErr)
			require.NoError(t, atErr)
			assert.Equal(t, tc.want, node.String())
		})
	}
}

func TestPath_UnknownAliasKey(t *testing.T) {
	t.Parallel()

	// An alias key that names no anchor has no name, so no selector
	// matches it, and a lookup that passes it on the way to another key
	// still resolves.
	source := niceyaml.NewSourceFromString("b: 2\n*nope : 1\n")
	file, err := source.File()
	require.NoError(t, err)

	doc := file.Docs[0]

	node, err := paths.Root().Child("b").Node(doc)
	require.NoError(t, err)
	assert.Equal(t, "2", node.String())

	matches, err := paths.Root().Recursive("b").Matches(doc)
	require.NoError(t, err)
	require.Len(t, matches, 1)
	assert.Equal(t, "$.b", matches[0].Path.String())

	for _, name := range []string{"nope", "*"} {
		_, err := paths.Root().Child(name).Node(doc)
		require.ErrorIs(t, err, paths.ErrNotFound, "Child(%q)", name)
	}
}

func TestPath_UnnamedKey(t *testing.T) {
	t.Parallel()

	// A key with no name has no text a selector can match, so neither a
	// `.''` nor a `..name` selector reaches its entry, and the entry never
	// hides a real empty key. The decoder never reads such a key as the
	// empty key.
	tcs := map[string]struct {
		err       error
		input     string
		path      string
		want      string
		recursive string
		matches   []string
		opts      []niceyaml.SourceOption
	}{
		"alias key with no anchor": {
			input:     "b: 2\n*nope : 1\n",
			path:      "$.''",
			err:       paths.ErrNotFound,
			recursive: "$..''",
		},
		"alias key inside its own anchor": {
			input:     "x: &x\n  *x : v\n",
			path:      "$.x.''",
			err:       paths.ErrNotFound,
			recursive: "$..''",
		},
		"alias key to a mapping": {
			input:     "a: &m {k: 1}\n*m : v\n",
			path:      "$.''",
			err:       paths.ErrNotFound,
			recursive: "$..''",
		},
		"alias key to a sequence": {
			input:     "a: &m [1]\n*m : v\n",
			path:      "$.''",
			err:       paths.ErrNotFound,
			recursive: "$..''",
		},
		"entry below an alias key with no anchor": {
			input:     "*nope : {name: 1}\nb: {name: 2}\n",
			path:      "$.''.name",
			err:       paths.ErrNotFound,
			recursive: "$..name",
			matches:   []string{"$.b.name=2"},
		},
		"real empty key before an alias key with no name": {
			// The decoder keeps both entries, and reads the alias key
			// as null.
			opts:      []niceyaml.SourceOption{niceyaml.WithAllowDuplicateKeys(true)},
			input:     "x: &x\n  \"\": {name: 1}\n  *x : {name: 2}\n",
			path:      "$.x.''",
			want:      "{name: 1}",
			recursive: "$..name",
			matches:   []string{"$.x.''.name=1"},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			file, err := niceyaml.NewSourceFromString(tc.input, tc.opts...).File()
			require.NoError(t, err)

			doc := file.Docs[0]

			node, err := paths.MustParse(tc.path).Node(doc)
			if tc.err != nil {
				require.ErrorIs(t, err, tc.err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tc.want, node.String())
			}

			matches, err := paths.MustParse(tc.recursive).Matches(doc)
			require.NoError(t, err)

			var got []string

			for _, m := range matches {
				got = append(got, m.Path.String()+"="+m.Node.String())
			}

			assert.Equal(t, tc.matches, got)
		})
	}
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
		"alias inside its own mapping anchor": {
			input: "b: &x {s: *x, t: 1}\n",
			path:  paths.Root().Child("b", "s", "t"),
			want:  "*x forms a cycle",
		},
		"alias inside its own sequence anchor": {
			input: "a: &a [1, *a]\n",
			path:  paths.Root().Child("a").Index(1).Index(0),
			want:  "*a forms a cycle",
		},
		"alias inside its own anchor on a merge value": {
			input: "x: {<<: &m {k: *m, j: 1}}\n",
			path:  paths.Root().Child("x", "k", "j"),
			want:  "*m forms a cycle",
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

func TestPath_AliasInsideOwnAnchor(t *testing.T) {
	t.Parallel()

	// The decoder reads an alias inside the content of the anchor it refers
	// to as null. A path to such an alias selects no node, while its token
	// still marks the alias.
	tcs := map[string]struct {
		err   error
		input string
		want  string
		path  paths.Path
	}{
		"mapping": {
			input: "b: &x {s: *x, t: 1}\n",
			path:  paths.Root().Child("b", "s"),
			err:   paths.ErrAlias,
		},
		"sequence": {
			input: "a: &a [1, *a]\n",
			path:  paths.Root().Child("a").Index(1),
			err:   paths.ErrAlias,
		},
		"anchor on a merge value": {
			input: "x: {<<: &m {k: *m, j: 1}}\n",
			path:  paths.Root().Child("x", "k"),
			err:   paths.ErrAlias,
		},
		"alias a merge key names inside its own anchor": {
			// The decode leaves an alias that a `<<` merge key names as it
			// is, so a path through the merge key reaches the content of
			// the anchor.
			input: "a: &a {k: 1, i: {<<: *a}}\n",
			path:  paths.Root().Child("a", "i", "<<"),
			want:  "{k: 1, i: {<<: *a}}",
		},
		"alias after an anchor of the same name inside the content": {
			// The inner &x is the last anchor of its name before *x, and *x
			// lies outside the content of that anchor.
			input: "b: &x {a: &x 1, s: *x}\n",
			path:  paths.Root().Child("b", "s"),
			want:  "1",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			dd := yamltest.FirstDocument(t, tc.input)
			doc := dd.DocumentAST()

			tk, err := tc.path.Token(doc)
			require.NoError(t, err)
			assert.Equal(t, "*", tk.Value)

			node, err := tc.path.Node(doc)
			_, nodesErr := tc.path.Nodes(doc)
			_, atErr := dd.At(tc.path)

			if tc.err != nil {
				require.ErrorIs(t, err, tc.err)
				require.ErrorIs(t, nodesErr, tc.err)
				require.ErrorIs(t, atErr, tc.err)

				return
			}

			require.NoError(t, err)
			require.NoError(t, nodesErr)
			require.NoError(t, atErr)
			assert.Equal(t, tc.want, node.String())
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

		// An entry without a key has no name, so not even the empty
		// name selects it.
		_, err = paths.Root().Child("").Key().Token(doc)
		require.ErrorIs(t, err, paths.ErrNotFound)
	})
}

// mapNode builds a mapping node from entries, for a tree the parser would
// never produce.
func mapNode(entries ...*ast.MappingValueNode) *ast.MappingNode {
	return &ast.MappingNode{Values: entries}
}

// mapEntry builds a mapping entry from key and value.
func mapEntry(key ast.MapKeyNode, value ast.Node) *ast.MappingValueNode {
	return &ast.MappingValueNode{Key: key, Value: value}
}

func TestPath_Node_HandBuiltTree(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		body ast.Node
		path paths.Path
		err  error
	}{
		"nil mapping entry": {
			body: mapNode(nil),
			path: paths.Root().Child("a"),
			err:  paths.ErrNotFound,
		},
		"nil entry beside a real one": {
			body: mapNode(nil, mapEntry(&ast.StringNode{Value: "a"}, &ast.StringNode{Value: "1"})),
			path: paths.Root().Child("b"),
			err:  paths.ErrNotFound,
		},
		"key without a token": {
			body: mapNode(mapEntry(&ast.IntegerNode{}, &ast.StringNode{Value: "1"})),
			path: paths.Root().Child("a"),
			err:  paths.ErrNotFound,
		},
		"alias name holding a typed nil": {
			body: mapNode(mapEntry(
				&ast.StringNode{Value: "a"},
				&ast.AliasNode{Value: (*ast.StringNode)(nil)},
			)),
			path: paths.Root().Child("a"),
			err:  paths.ErrAlias,
		},
		"anchor name without a token": {
			body: mapNode(mapEntry(&ast.StringNode{Value: "a"}, &ast.AnchorNode{
				Name:  &ast.StringNode{},
				Value: &ast.StringNode{Value: "1"},
			})),
			path: paths.Root().Child("a", "b"),
			err:  paths.ErrNotFound,
		},
		"entry without a value": {
			body: mapNode(mapEntry(&ast.StringNode{Value: "a"}, nil)),
			path: paths.Root().Child("a", "b"),
			err:  paths.ErrNotFound,
		},
		"path ending at an entry without a value": {
			body: mapNode(mapEntry(&ast.StringNode{Value: "a"}, nil)),
			path: paths.Root().Child("a"),
			err:  paths.ErrNotFound,
		},
		"key holding a typed nil": {
			body: mapNode(mapEntry((*ast.StringNode)(nil), &ast.StringNode{Value: "1"})),
			path: paths.Root().Child("a"),
			err:  paths.ErrNotFound,
		},
		"key holding a typed nil anchor": {
			body: mapNode(mapEntry((*ast.AnchorNode)(nil), &ast.StringNode{Value: "1"})),
			path: paths.Root().Child("a"),
			err:  paths.ErrNotFound,
		},
		"key holding a typed nil tag": {
			body: mapNode(mapEntry((*ast.TagNode)(nil), &ast.StringNode{Value: "1"})),
			path: paths.Root().Child("a"),
			err:  paths.ErrNotFound,
		},
		"key holding a typed nil explicit key": {
			body: mapNode(mapEntry((*ast.MappingKeyNode)(nil), &ast.StringNode{Value: "1"})),
			path: paths.Root().Child("a"),
			err:  paths.ErrNotFound,
		},
		"explicit key wrapping a typed nil anchor": {
			body: mapNode(mapEntry(
				&ast.MappingKeyNode{Value: (*ast.AnchorNode)(nil)},
				&ast.StringNode{Value: "1"},
			)),
			path: paths.Root().Child("a"),
			err:  paths.ErrNotFound,
		},
		"child of a typed nil mapping": {
			body: mapNode(mapEntry(&ast.StringNode{Value: "a"}, (*ast.MappingNode)(nil))),
			path: paths.Root().Child("a", "b"),
			err:  paths.ErrNotFound,
		},
		"index of a typed nil sequence": {
			body: mapNode(mapEntry(&ast.StringNode{Value: "a"}, (*ast.SequenceNode)(nil))),
			path: paths.Root().Child("a").Index(0),
			err:  paths.ErrNotFound,
		},
		"path ending at a typed nil anchor": {
			body: mapNode(mapEntry(&ast.StringNode{Value: "a"}, (*ast.AnchorNode)(nil))),
			path: paths.Root().Child("a"),
			err:  paths.ErrNotFound,
		},
		"child of a typed nil anchor": {
			body: mapNode(mapEntry(&ast.StringNode{Value: "a"}, (*ast.AnchorNode)(nil))),
			path: paths.Root().Child("a", "b"),
			err:  paths.ErrNotFound,
		},
		"child of a typed nil tag": {
			body: mapNode(mapEntry(&ast.StringNode{Value: "a"}, (*ast.TagNode)(nil))),
			path: paths.Root().Child("a", "b"),
			err:  paths.ErrNotFound,
		},
		"merge of a typed nil mapping": {
			body: mapNode(mapEntry(
				&ast.MergeKeyNode{},
				&ast.SequenceNode{BaseNode: &ast.BaseNode{}, Values: []ast.Node{(*ast.MappingNode)(nil)}},
			)),
			path: paths.Root().Child("b"),
			err:  paths.ErrNotFound,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			doc := &ast.DocumentNode{Body: tc.body}

			_, err := tc.path.Node(doc)
			require.ErrorIs(t, err, tc.err)
		})
	}
}

func TestPath_Nodes_HandBuiltTreeRecursive(t *testing.T) {
	t.Parallel()

	doc := &ast.DocumentNode{Body: mapNode(
		nil,
		mapEntry(&ast.IntegerNode{}, mapNode(
			nil,
			mapEntry(&ast.IntegerNode{}, &ast.StringNode{Value: "1"}),
		)),
	)}

	nodes, err := paths.Root().Recursive("b").Nodes(doc)
	require.NoError(t, err)
	assert.Empty(t, nodes)
}

func TestPath_Matches_HandBuiltTreeRecursive(t *testing.T) {
	t.Parallel()

	shared := &ast.StringNode{Value: "1"}

	tcs := map[string]struct {
		body ast.Node
		path string
		want []string
	}{
		"keys of entries without a value": {
			body: mapNode(
				mapEntry(&ast.StringNode{Value: "a"}, nil),
				mapEntry(&ast.StringNode{Value: "b"}, mapNode(
					mapEntry(&ast.StringNode{Value: "a"}, nil),
				)),
			),
			path: "$..a~",
			want: []string{"$.a~", "$.b.a~"},
		},
		"entries sharing a value node": {
			body: mapNode(
				mapEntry(&ast.StringNode{Value: "c"}, mapNode(mapEntry(&ast.StringNode{Value: "x"}, shared))),
				mapEntry(&ast.StringNode{Value: "d"}, mapNode(mapEntry(&ast.StringNode{Value: "x"}, shared))),
			),
			path: "$..x",
			want: []string{"$.c.x", "$.d.x"},
		},
		"keys of entries sharing a value node": {
			body: mapNode(
				mapEntry(&ast.StringNode{Value: "c"}, mapNode(mapEntry(&ast.StringNode{Value: "x"}, shared))),
				mapEntry(&ast.StringNode{Value: "d"}, mapNode(mapEntry(&ast.StringNode{Value: "x"}, shared))),
			),
			path: "$..x~",
			want: []string{"$.c.x~", "$.d.x~"},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			doc := &ast.DocumentNode{Body: tc.body}

			matches, err := paths.MustParse(tc.path).Matches(doc)
			require.NoError(t, err)

			got := make([]string, 0, len(matches))
			for _, m := range matches {
				got = append(got, m.Path.String())
			}

			assert.Equal(t, tc.want, got)
		})
	}
}

func TestPath_Nodes_HandBuiltTreeTypedNil(t *testing.T) {
	t.Parallel()

	doc := &ast.DocumentNode{Body: mapNode(
		mapEntry(&ast.StringNode{Value: "m"}, (*ast.MappingNode)(nil)),
		mapEntry(&ast.StringNode{Value: "s"}, (*ast.SequenceNode)(nil)),
		mapEntry(&ast.StringNode{Value: "a"}, (*ast.AnchorNode)(nil)),
		mapEntry(&ast.StringNode{Value: "t"}, (*ast.TagNode)(nil)),
	)}

	tcs := map[string]paths.Path{
		"every element of a typed nil sequence": paths.Root().Child("s").IndexAll(),
		"recursive through typed nils":          paths.Root().Recursive("q"),
	}

	for name, path := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			nodes, err := path.Nodes(doc)
			require.NoError(t, err)
			assert.Empty(t, nodes)
		})
	}
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
			got, err := yamltest.At(t, dd, paths.Root().Child(key)).Decode[string](t.Context())
			require.NoError(t, err)
			assert.Equal(t, decoded[key], got)
		}
	})

	t.Run("anchor of the same name inside the content", func(t *testing.T) {
		t.Parallel()

		// The decoder records an anchor again when it has read the content,
		// so an alias after it refers to the outer anchor.
		tcs := map[string]struct {
			input string
			key   string
			want  string
		}{
			"mapping": {
				input: "a: &y {b: &y 3}\nk: *y\n",
				key:   "k",
				want:  "{b: &y 3}",
			},
			"sequence": {
				input: "a: &y [1, &y 2]\nk: *y\n",
				key:   "k",
				want:  "[1, &y 2]",
			},
			"merge of an aliased mapping": {
				input: "base: &b {k: &x {j: &x 1}}\nm: {<<: *b}\nv: *x\n",
				key:   "v",
				want:  "{j: &x 1}",
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				dd := yamltest.FirstDocument(t, tc.input)

				node, err := paths.Root().Child(tc.key).Node(dd.DocumentAST())
				require.NoError(t, err)
				assert.Equal(t, tc.want, node.String())

				var decoded map[string]any

				require.NoError(t, yaml.Unmarshal([]byte(tc.input), &decoded))

				got, err := yamltest.At(t, dd, paths.Root().Child(tc.key)).Decode[any](t.Context())
				require.NoError(t, err)
				assert.Equal(t, decoded[tc.key], got)
			})
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
		"missing key then child":   paths.Root().Child("nope", "deeper"),
		"index then missing child": paths.Root().Child("items").Index(0).Child("x"),
	}

	for name, path := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, err := path.Token(file.Docs[0])
			require.ErrorIs(t, err, paths.ErrNotFound)
			assert.Contains(t, err.Error(), path.String())

			_, err = path.Key().Token(file.Docs[0])
			require.ErrorIs(t, err, paths.ErrNotFound)

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
nest:
  a:
    b:
      a:
        c: 1
    c: 2
nestseq:
  a:
    - a: [x, y]
    - z
aliased: [&s [p, q], [r], *s]
refs: [*r, *r]
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
		"recursive then child keeps document order": {
			// The outer entry a comes first, but its c follows the c of
			// the inner one in the source.
			path: paths.Root().Child("nest").Recursive("a").Child("c"),
			want: []string{"1", "2"},
		},
		"recursive then index all keeps document order": {
			// The mapping {a: [x, y]} prints as its ":" token.
			path: paths.Root().Child("nestseq").Recursive("a").IndexAll(),
			want: []string{":", "x", "y", "z"},
		},
		"index all through an alias keeps path order": {
			path: paths.Root().Child("aliased").IndexAll().IndexAll(),
			want: []string{"p", "q", "r", "p", "q"},
		},
		"index all through aliases repeats the anchor": {
			path: paths.Root().Child("refs").IndexAll().Child("name"),
			want: []string{"e", "e"},
		},
		"recursive looks through an alias at its start": {
			path: paths.Root().Child("alias").Recursive("name"),
			want: []string{"e"},
		},
		"recursive skips alias merge sources": {
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

func TestPath_Matches_AliasFanOut(t *testing.T) {
	t.Parallel()

	// The document of aliasList holds n scalars under the key a and n
	// aliases to a under the key b, so $.b[*][*] selects n*n nodes.
	aliasList := func(n int) string {
		elems := strings.TrimSuffix(strings.Repeat("x, ", n), ", ")
		aliases := strings.TrimSuffix(strings.Repeat("*a, ", n), ", ")

		return "a: &a [" + elems + "]\nb: [" + aliases + "]\n"
	}

	tcs := map[string]struct {
		err   error
		input string
		path  paths.Path
		want  int
	}{
		"nested alias levels": {
			input: yamltest.AliasLevels(5),
			path:  paths.MustParse("$.a[5][*][*][*][*][*]"),
			err:   paths.ErrExcessiveAliasing,
		},
		"wide list of aliases": {
			input: aliasList(500),
			path:  paths.MustParse("$.b[*][*]"),
			err:   paths.ErrExcessiveAliasing,
		},
		"few alias levels": {
			input: yamltest.AliasLevels(2),
			path:  paths.MustParse("$.a[2][*][*]"),
			want:  100,
		},
		"short list of aliases": {
			input: aliasList(10),
			path:  paths.MustParse("$.b[*][*]"),
			want:  100,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			doc := yamltest.FirstDocument(t, tc.input).DocumentAST()

			matches, err := tc.path.Matches(doc)
			if tc.err != nil {
				require.ErrorIs(t, err, tc.err)

				_, err = tc.path.Nodes(doc)
				require.ErrorIs(t, err, tc.err)

				return
			}

			require.NoError(t, err)
			assert.Len(t, matches, tc.want)
		})
	}
}

func TestPath_Matches_MergeLookups(t *testing.T) {
	t.Parallel()

	// The document of mergeChain holds the mappings b0 through b<n-1>.
	// Each later mapping holds a key of its own and then merges the one
	// before it, so a lookup of a key that none of them hold reads every
	// mapping below the one it starts in.
	mergeChain := func(n int) string {
		var sb strings.Builder

		sb.WriteString("b0: &b0 {x0: 0}\n")

		for i := 1; i < n; i++ {
			fmt.Fprintf(&sb, "b%d: &b%d {x%d: %d, <<: *b%d}\n", i, i, i, i, i-1)
		}

		return sb.String()
	}

	// The document of inlineChain holds no aliases. Its mapping m holds n
	// keys and then a merge key whose value nests n merge keys inline.
	inlineChain := func(n int) string {
		var sb strings.Builder

		sb.WriteString("m:\n")

		for i := range n {
			fmt.Fprintf(&sb, "  k%d: %d\n", i, i)
		}

		sb.WriteString("  <<: " + strings.Repeat("{<<: ", n) + "{z: 0}" + strings.Repeat("}", n) + "\n")

		return sb.String()
	}

	// The flow sequence that list returns holds elem n times and then each
	// element of tail.
	list := func(elem string, n int, tail ...string) string {
		return "[" + strings.Join(append(slices.Repeat([]string{elem}, n), tail...), ", ") + "]"
	}

	// The document of aliasChain adds to mergeChain the key l, which lists
	// n aliases to the last mapping of the chain.
	aliasChain := func(n int) string {
		return mergeChain(n) + "l: " + list(fmt.Sprintf("*b%d", n-1), n) + "\n"
	}

	// The document of wideMerge holds the mapping a, and the mapping m with
	// n keys and then a merge key whose value lists elem n times and then
	// each element of tail. A lookup of each key of m reads that whole list.
	wideMerge := func(n int, elem string, tail ...string) string {
		var sb strings.Builder

		sb.WriteString("a: &a {z: 0}\nm:\n")

		for i := range n {
			fmt.Fprintf(&sb, "  k%d: %d\n", i, i)
		}

		sb.WriteString("  <<: " + list(elem, n, tail...) + "\n")

		return sb.String()
	}

	// The document of missingFirst is that of wideMerge(n, "*a"), except
	// that its merge list starts with an alias that does not resolve. A
	// lookup of each key of m stops at that alias.
	missingFirst := func(n int) string {
		return strings.Replace(wideMerge(n, "*a"), "<<: [", "<<: [*missing, ", 1)
	}

	// The document of aliasWideMerge holds the mapping x, which merges n
	// aliases to the mapping a, and the key l, which lists n aliases to x.
	aliasWideMerge := func(n int) string {
		return "a: &a {z: 0}\nx: &x {<<: " + list("*a", n) + "}\nl: " + list("*x", n) + "\n"
	}

	// The document of sharedMerge holds the sequence s of n aliases to the
	// mapping a, and the mapping d, which merges n inline mappings that
	// each merge s. One lookup in d reads s once for each of them.
	sharedMerge := func(n int) string {
		return "a: &a {z: 0}\ns: &s " + list("*a", n) + "\nd: {<<: " + list("{<<: *s}", n) + "}\n"
	}

	// The document of records holds the mappings d0 through d99 and the
	// sequence items of n mappings. Each of those holds the key name, then
	// 100 more keys of its own, and then a merge key that lists an alias to
	// each of d0 through d99. A lookup of each key of a record reads that
	// list, so a `..name` walk reads the same multiple of the nodes of the
	// document however large n is.
	records := func(n int) string {
		var (
			sb      strings.Builder
			sources []string
		)

		for i := range 100 {
			fmt.Fprintf(&sb, "d%d: &d%d {p%d: 0}\n", i, i, i)

			sources = append(sources, fmt.Sprintf("*d%d", i))
		}

		sb.WriteString("items:\n")

		for i := range n {
			fmt.Fprintf(&sb, "  - name: r%d\n", i)

			for k := range 100 {
				fmt.Fprintf(&sb, "    k%d: %d\n", k, k)
			}

			sb.WriteString("    <<: [" + strings.Join(sources, ", ") + "]\n")
		}

		return sb.String()
	}

	tcs := map[string]struct {
		err   error
		input string
		path  paths.Path
		want  int
	}{
		"recursive key over a merge chain": {
			input: mergeChain(2000),
			path:  paths.Root().Recursive("nope"),
			err:   paths.ErrExcessiveMerging,
		},
		"recursive key over an inline merge chain": {
			input: inlineChain(2000),
			path:  paths.Root().Recursive("nope"),
			err:   paths.ErrExcessiveMerging,
		},
		"child key of each alias to a merge chain": {
			input: aliasChain(2000),
			path:  paths.MustParse("$.l[*].nope"),
			err:   paths.ErrExcessiveMerging,
		},
		"recursive key over a merge of many aliases": {
			input: wideMerge(2000, "*a"),
			path:  paths.Root().Recursive("nope"),
			err:   paths.ErrExcessiveMerging,
		},
		"recursive key over a merge of many scalars": {
			input: wideMerge(2000, "0"),
			path:  paths.Root().Recursive("nope"),
			err:   paths.ErrExcessiveMerging,
		},
		"recursive key over a merge of many aliases and a missing one": {
			input: wideMerge(2000, "*a", "*missing"),
			path:  paths.Root().Recursive("nope"),
			err:   paths.ErrExcessiveMerging,
		},
		"recursive key over a merge of many aliases and a later anchor": {
			input: wideMerge(2000, "*a", "*late") + "late: &late {y: 1}\n",
			path:  paths.Root().Recursive("nope"),
			err:   paths.ErrExcessiveMerging,
		},
		"recursive key over a merge of many scalars and a missing alias": {
			input: wideMerge(2000, "0", "*missing"),
			path:  paths.Root().Recursive("nope"),
			err:   paths.ErrExcessiveMerging,
		},
		"child key of each alias to a wide merge": {
			input: aliasWideMerge(2000),
			path:  paths.MustParse("$.l[*].nope"),
			err:   paths.ErrExcessiveMerging,
		},
		"child key over many merges of one sequence": {
			input: sharedMerge(1000),
			path:  paths.MustParse("$.d.nope"),
			err:   paths.ErrExcessiveMerging,
		},
		"recursive key over a short merge chain": {
			input: mergeChain(10),
			path:  paths.Root().Recursive("x5"),
			want:  1,
		},
		"child key of each alias to a short merge chain": {
			input: aliasChain(10),
			path:  paths.MustParse("$.l[*].x0"),
			want:  10,
		},
		"recursive key over a merge of a few aliases": {
			input: wideMerge(10, "*a"),
			path:  paths.Root().Recursive("k5"),
			want:  1,
		},
		"recursive key over a merge of a few aliases and a missing one": {
			input: wideMerge(10, "*a", "*missing"),
			path:  paths.Root().Recursive("k5"),
			want:  0,
		},
		"recursive key over a merge that lists a missing alias first": {
			input: missingFirst(10),
			path:  paths.Root().Recursive("k5"),
			want:  0,
		},
		"recursive key over a wide merge that lists a missing alias first": {
			input: missingFirst(2000),
			path:  paths.Root().Recursive("nope"),
			want:  0,
		},
		"child key of each alias to a narrow merge": {
			input: aliasWideMerge(10),
			path:  paths.MustParse("$.l[*].z"),
			want:  10,
		},
		"child key over a few merges of one sequence": {
			input: sharedMerge(10),
			path:  paths.MustParse("$.d.z"),
			want:  1,
		},
		// The lookups of this walk read over 4 million nodes under merge
		// keys, which is about 20 times the nodes of the document.
		"recursive key over many records that merge one list": {
			input: records(400),
			path:  paths.Root().Recursive("name"),
			want:  400,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			doc := yamltest.FirstDocument(t, tc.input).DocumentAST()

			matches, err := tc.path.Matches(doc)
			if tc.err != nil {
				require.ErrorIs(t, err, tc.err)

				_, err = tc.path.Nodes(doc)
				require.ErrorIs(t, err, tc.err)

				// Path.Node takes only a path without a wildcard.
				_, err = tc.path.Node(doc)
				if !errors.Is(err, paths.ErrWildcard) {
					require.ErrorIs(t, err, tc.err)
				}

				return
			}

			require.NoError(t, err)
			assert.Len(t, matches, tc.want)
		})
	}
}

func TestPath_Node_NestedMergeLists(t *testing.T) {
	t.Parallel()

	// The mapping c merges a list of three sources. The middle one, b,
	// merges a list of its own, and the last source of that list merges a
	// longer list. A lookup in c searches each list from its last source
	// back, so it searches the longest list before the earlier sources of
	// the two shorter ones.
	input := `
a: &a {k: A}
b: &b
  <<:
    - {j: J}
    - {i: I}
    - <<: [*a, *a, *a, *a, *a, *a, *a, *a, *a, *a, *a, *a, *a, *a, *a, *a]
c: &c
  own: C
  <<: [{h: H}, *b, {g: G}]
l: [*c, *c, *c]
`

	doc := yamltest.FirstDocument(t, input).DocumentAST()

	tcs := map[string]struct {
		err  error
		name string
		want string
	}{
		"key of the last source": {
			name: "g",
			want: "G",
		},
		"key of the longest list": {
			name: "k",
			want: "A",
		},
		"key of a source before the longest list": {
			name: "i",
			want: "I",
		},
		"key of the first source of the middle list": {
			name: "j",
			want: "J",
		},
		"key of the first source": {
			name: "h",
			want: "H",
		},
		"key of the mapping itself": {
			name: "own",
			want: "C",
		},
		"key that no source holds": {
			name: "nope",
			err:  paths.ErrNotFound,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			node, err := paths.Root().Child("c", tc.name).Node(doc)
			if tc.err != nil {
				require.ErrorIs(t, err, tc.err)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, tc.want, node.String())

			// The `[*]` selector shares one lookup state among the lookups
			// in each element, and each of them finds the same entry.
			nodes, err := paths.Root().Child("l").IndexAll().Child(tc.name).Nodes(doc)
			require.NoError(t, err)
			require.Len(t, nodes, 3)

			for _, n := range nodes {
				assert.Same(t, node, n)
			}
		})
	}
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
base: &kk aliased_name
*kk : 8
? |
  block
: 9
`

	source := niceyaml.NewSourceFromString(input)
	file, err := source.File()
	require.NoError(t, err)

	tcs := map[string]struct {
		path      paths.Path
		wantValue string
		key       bool
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
			path:      paths.Root().Child("complex"),
			key:       true,
			wantValue: "complex",
		},
		"tagged key matches by content": {
			path:      paths.Root().Child("tagged"),
			wantValue: "6",
		},
		"tagged key target skips the tag": {
			path:      paths.Root().Child("tagged"),
			key:       true,
			wantValue: "tagged",
		},
		"anchored key matches by content": {
			path:      paths.Root().Child("anchored"),
			wantValue: "7",
		},
		"anchored key target skips the anchor": {
			path:      paths.Root().Child("anchored"),
			key:       true,
			wantValue: "anchored",
		},
		"alias key matches by its anchor's content": {
			path:      paths.Root().Child("aliased_name"),
			wantValue: "8",
		},
		"alias key target is the alias": {
			path:      paths.Root().Child("aliased_name"),
			key:       true,
			wantValue: "*",
		},
		"block scalar key matches by its content": {
			path:      paths.Root().Child("block\n"),
			wantValue: "9",
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

			tk := resolveToken(t, tc.path, tc.key, file.Docs[0])
			assert.Equal(t, tc.wantValue, tk.Value)
		})
	}

	t.Run("indicator, tag, anchor, and alias text are not key names", func(t *testing.T) {
		t.Parallel()

		for _, name := range []string{"!!str", "&k", "k", "&", "?", "*", "kk", "|"} {
			_, err := paths.Root().Child(name).Node(file.Docs[0])
			require.ErrorIs(t, err, paths.ErrNotFound, "Child(%q)", name)
		}
	})
}

// resolveToken returns the token path selects in doc, the key of the entry
// when key is set and the value otherwise. It fails the test when the path
// does not resolve.
func resolveToken(t *testing.T, path paths.Path, key bool, doc *ast.DocumentNode) *token.Token {
	t.Helper()

	if key {
		path = path.Key()
	}

	tk, err := path.Token(doc)
	require.NoError(t, err)
	require.NotNil(t, tk)

	return tk
}

func TestPath_Token_HandBuiltMapping(t *testing.T) {
	t.Parallel()

	// A tree built by hand may hold a typed nil or a nil entry where the
	// parser always puts a node. Token returns a not-found error for such a
	// mapping rather than panicking, and names the path as other resolution
	// errors do.
	tcs := map[string]struct {
		body ast.Node
		path paths.Path
		want string
	}{
		"key holding a typed nil": {
			body: mapNode(mapEntry((*ast.StringNode)(nil), &ast.StringNode{Value: "1"})),
			path: paths.Root(),
			want: "resolve $: not found: node has no token",
		},
		"nil entry": {
			body: &ast.MappingNode{Values: []*ast.MappingValueNode{nil}},
			path: paths.Root(),
			want: "resolve $: not found: node has no token",
		},
		"value holding a typed nil": {
			body: mapNode(mapEntry(&ast.StringNode{Value: "a"}, (*ast.StringNode)(nil))),
			path: paths.Root().Child("a"),
			want: "resolve $.a: not found: node has no token",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, err := tc.path.Token(&ast.DocumentNode{Body: tc.body})
			require.ErrorIs(t, err, paths.ErrNotFound)
			require.EqualError(t, err, tc.want)
		})
	}
}

func TestPath_Node_LaterMergeKeyWins(t *testing.T) {
	t.Parallel()

	// A mapping may hold two merge keys when the parser allows duplicates.
	// The decoder takes the value of the later one, so the path does too.
	src := "p: &p {k: P}\nq: &q {k: Q}\nt:\n  <<: *p\n  <<: *q\n"

	f, err := parser.ParseBytes([]byte(src), 0, parser.AllowDuplicateMapKey())
	require.NoError(t, err)

	node, err := paths.MustParse("$.t.k").Node(f.Docs[0])
	require.NoError(t, err)
	assert.Equal(t, "Q", node.String())
}

func TestPath_OwnKeyBetweenMergeKeys(t *testing.T) {
	t.Parallel()

	// The decoder sets each entry in document order, so a key of the
	// mapping between two merge keys wins over the earlier merge key and
	// loses to the later one when that one brings in the same key.
	tcs := map[string]struct {
		input string
		want  string
		found []string
	}{
		"own key wins over an earlier merge key": {
			input: "p: &p {k: P}\nq: &q {j: Q}\nt:\n  <<: *p\n  k: own\n  <<: *q\n",
			want:  "own",
			found: []string{"$.t.k"},
		},
		"later merge key wins over the own key": {
			input: "p: &p {k: P}\nq: &q {k: Q}\nt:\n  <<: *p\n  k: own\n  <<: *q\n",
			want:  "Q",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			f, err := parser.ParseBytes([]byte(tc.input), 0, parser.AllowDuplicateMapKey())
			require.NoError(t, err)

			doc := f.Docs[0]

			var decoded struct {
				T map[string]string `yaml:"t"`
			}

			err = yaml.UnmarshalWithOptions([]byte(tc.input), &decoded, yaml.AllowDuplicateMapKey())
			require.NoError(t, err)
			assert.Equal(t, tc.want, decoded.T["k"])

			node, err := paths.MustParse("$.t.k").Node(doc)
			require.NoError(t, err)
			assert.Equal(t, tc.want, node.String())

			matches, err := paths.MustParse("$.t..k").Matches(doc)
			require.NoError(t, err)

			var found []string

			for _, m := range matches {
				found = append(found, m.Path.String())
			}

			assert.Equal(t, tc.found, found)
		})
	}
}

func TestPath_Matches_RepeatedMergeKey(t *testing.T) {
	t.Parallel()

	// The decoder merges both inline mappings, but a path through `<<`
	// selects the later one, so `..` visits only that mapping.
	src := "t:\n  <<: {a: 1}\n  <<: {b: 2}\n"

	f, err := parser.ParseBytes([]byte(src), 0, parser.AllowDuplicateMapKey())
	require.NoError(t, err)

	doc := f.Docs[0]

	matches, err := paths.MustParse("$..a").Matches(doc)
	require.NoError(t, err)
	assert.Empty(t, matches)

	matches, err = paths.MustParse("$..b").Matches(doc)
	require.NoError(t, err)
	require.Len(t, matches, 1)
	assert.Equal(t, "$.t.<<.b", matches[0].Path.String())

	single, err := matches[0].Path.Node(doc)
	require.NoError(t, err)
	assert.Same(t, matches[0].Node, single)

	node, err := paths.MustParse("$.t.a").Node(doc)
	require.NoError(t, err)
	assert.Equal(t, "1", node.String())
}

func TestPath_Matches_InlineMergeSource(t *testing.T) {
	t.Parallel()

	// A `..name` selector walks a mapping written under `<<` as any other
	// value and skips an alias there, even when the decoder takes the
	// entry from a later source or from the mapping's own key. It skips a
	// key of the mapping that a later merge brings in again, and the path
	// of each match selects that match's node.
	tcs := map[string]struct {
		input string
		want  []string
		value string
	}{
		"later alias source overrides": {
			input: "base: &b {name: y}\nm:\n  <<: [{name: x}, *b]\n",
			want:  []string{"$.m.<<[0].name"},
			value: "y",
		},
		"own key overrides": {
			input: "m:\n  <<: {name: x}\n  name: z\n",
			want:  []string{"$.m.<<.name", "$.m.name"},
			value: "z",
		},
		"later merge overrides an own key": {
			input: "m:\n  name: z\n  <<: {name: x}\n",
			want:  []string{"$.m.<<.name"},
			value: "x",
		},
		"later alias merge overrides an own key": {
			input: "base: &b {name: y}\nm:\n  name: z\n  <<: *b\n",
			want:  nil,
			value: "y",
		},
		"alias source alone": {
			input: "base: &b {name: y}\nm:\n  <<: *b\n",
			want:  nil,
			value: "y",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			file, err := niceyaml.NewSourceFromString(tc.input).File()
			require.NoError(t, err)

			doc := file.Docs[0]

			matches, err := paths.MustParse("$.m..name").Matches(doc)
			require.NoError(t, err)

			var got []string

			for _, m := range matches {
				got = append(got, m.Path.String())

				node, err := m.Path.Node(doc)
				require.NoError(t, err)
				assert.Same(t, m.Node, node)
			}

			assert.Equal(t, tc.want, got)

			node, err := paths.MustParse("$.m.name").Node(doc)
			require.NoError(t, err)
			assert.Equal(t, tc.value, node.String())
		})
	}
}

func TestPath_MergeKeyAndLiteralMergeName(t *testing.T) {
	t.Parallel()

	// An alias key whose anchor holds the text `<<` is a real key, which
	// the decoder keeps apart from a merge key in the same mapping. A path
	// through `<<` selects the real key in either order, so `..k` lists
	// only the entry under the real key, and the path of each match
	// selects that match's node.
	tcs := map[string]struct {
		input string
	}{
		"alias key first": {
			input: "a: {? &x \"<<\" : 1}\nm:\n  *x : {k: 1}\n  <<: {k: 2}\n",
		},
		"merge key first": {
			input: "a: {? &x \"<<\" : 1}\nm:\n  <<: {k: 2}\n  *x : {k: 1}\n",
		},
		"alias key before a source with a merge key": {
			input: "a: {? &x \"<<\" : 1}\nb: &b {j: 3}\nm:\n  *x : {k: 1}\n  <<: {<<: *b, k: 2}\n",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			file, err := niceyaml.NewSourceFromString(tc.input).File()
			require.NoError(t, err)

			doc := file.Docs[0]

			node, err := paths.MustParse("$.m.'<<'.k").Node(doc)
			require.NoError(t, err)
			assert.Equal(t, "1", node.String())

			node, err = paths.MustParse("$.m.k").Node(doc)
			require.NoError(t, err)
			assert.Equal(t, "2", node.String())

			matches, err := paths.MustParse("$.m..k").Matches(doc)
			require.NoError(t, err)
			require.Len(t, matches, 1)
			assert.Equal(t, "1", matches[0].Node.String())
			assert.Equal(t, "$.m.<<.k", matches[0].Path.String())

			node, err = matches[0].Path.Node(doc)
			require.NoError(t, err)
			assert.Same(t, matches[0].Node, node)
		})
	}
}

func TestPath_MergeBringsInLiteralMergeName(t *testing.T) {
	t.Parallel()

	// A merge source may hold a real key with the text `<<`, such as an
	// alias key whose anchor holds that text. The decoder brings that key
	// in as it does any other, so a path through `<<` selects it rather
	// than the merge key, and `..'<<'` skips the merge key and its inline
	// mapping. The path of each match selects that match's node.
	tcs := map[string]struct {
		input string
		want  []string
	}{
		"block source": {
			input: "k: &k \"<<\"\ninner: &inner\n  *k : real\ntop:\n  <<: *inner\n",
			want:  []string{"$.inner.<<"},
		},
		"inline source": {
			input: "k: &k \"<<\"\ntop:\n  <<: {*k : real}\n",
		},
		"sequence source": {
			input: "k: &k \"<<\"\ninner: &inner\n  *k : real\ntop:\n  <<: [*inner]\n",
			want:  []string{"$.inner.<<"},
		},
		"source that merges the key": {
			input: "k: &k \"<<\"\nbase: &base {*k : real}\nmid: &mid {<<: *base}\ntop:\n  <<: *mid\n",
			want:  []string{"$.base.<<"},
		},
		"earlier merge key": {
			input: "k: &k \"<<\"\ntop:\n  <<: {*k : real}\n  <<: {a: 1}\n",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var decoded struct {
				Top map[string]any `yaml:"top"`
			}

			err := yaml.UnmarshalWithOptions([]byte(tc.input), &decoded, yaml.AllowDuplicateMapKey())
			require.NoError(t, err)
			require.Equal(t, "real", decoded.Top["<<"])

			f, err := parser.ParseBytes([]byte(tc.input), 0, parser.AllowDuplicateMapKey())
			require.NoError(t, err)

			doc := f.Docs[0]

			node, err := paths.MustParse("$.top.'<<'").Node(doc)
			require.NoError(t, err)
			assert.Equal(t, "real", node.String())

			matches, err := paths.MustParse("$..'<<'").Matches(doc)
			require.NoError(t, err)

			var found []string

			for _, m := range matches {
				found = append(found, m.Path.String())

				single, err := m.Path.Node(doc)
				require.NoError(t, err)
				assert.Same(t, m.Node, single)
			}

			assert.Equal(t, tc.want, found)
		})
	}
}

func TestPath_Node_LaterDuplicateKeyWins(t *testing.T) {
	t.Parallel()

	// A mapping may hold one key twice when the parser allows duplicates.
	// The decoder keeps the value of the later entry, so the path selects
	// it, and `..a` lists that entry alone.
	src := "m:\n  a: 1\n  a: 2\n"

	f, err := parser.ParseBytes([]byte(src), 0, parser.AllowDuplicateMapKey())
	require.NoError(t, err)

	doc := f.Docs[0]

	node, err := paths.MustParse("$.m.a").Node(doc)
	require.NoError(t, err)
	assert.Equal(t, "2", node.String())

	key, err := paths.MustParse("$.m.a~").Token(doc)
	require.NoError(t, err)
	assert.Equal(t, 3, key.Position.Line)

	matches, err := paths.MustParse("$..a").Matches(doc)
	require.NoError(t, err)
	require.Len(t, matches, 1)
	assert.Equal(t, "$.m.a", matches[0].Path.String())

	single, err := matches[0].Path.Node(doc)
	require.NoError(t, err)
	assert.Same(t, matches[0].Node, single)
}

// nestedChain returns depth flow mappings nested in one another, each with
// the single key a, and the paths of the entries from the outermost in.
func nestedChain(depth int) (string, []string) {
	want := make([]string, 0, depth)

	for i := 1; i <= depth; i++ {
		want = append(want, "$"+strings.Repeat(".a", i))
	}

	return strings.Repeat("{a: ", depth) + "1" + strings.Repeat("}", depth), want
}

func TestPath_Matches_RecursiveReachedTwice(t *testing.T) {
	t.Parallel()

	chain, chainPaths := nestedChain(400)

	tcs := map[string]struct {
		input string
		path  string
		want  []string
	}{
		"single recursive on a deep chain": {
			input: chain,
			path:  "$..a",
			want:  chainPaths,
		},
		"chained recursive on a deep chain": {
			// The second ..a reaches each entry below the first from
			// every enclosing match, and keeps the outermost path.
			input: chain,
			path:  "$..a..a",
			want:  chainPaths[1:],
		},
		"aliases to one anchor keep the first": {
			input: "base: &b {c: {name: 1}}\nrefs: [*b, *b]\n",
			path:  "$.refs[*]..name",
			want:  []string{"$.refs[0].c.name"},
		},
		"outer match reaches an alias target first": {
			input: "k:\n  y: &b\n    name: 1\n  k: *b\n",
			path:  "$..k..name",
			want:  []string{"$.k.y.name"},
		},
		"merged entry comes before the outer walk": {
			// The walk from $.x.k reaches the anchor through the `<<`
			// entry, one place deeper than the order of the merged
			// entry $.x.k.x.k, so the merged entry's path comes first.
			input: "x:\n  k:\n    x:\n      <<: {k: &c {name: 1}}\n",
			path:  "$..x.k..name",
			want:  []string{"$.x.k.x.k.name"},
		},
		"later walk stops at an earlier match": {
			// The walk from $.b.k stops at the mapping that the walk
			// from $.a.k covers. Without the stop, the walk would find
			// the entry again, and resolve would keep only the copy that
			// sorts first, so the output alone does not show that the
			// walk stopped.
			input: "a: &x\n  k:\n    name: 1\nb:\n  k: *x\n",
			path:  "$..k..name",
			want:  []string{"$.a.k.name"},
		},
		"later walk stops at an earlier match in a sequence": {
			input: "a: &x\n  k:\n    - name: 1\nb:\n  k: *x\n",
			path:  "$..k..name",
			want:  []string{"$.a.k[0].name"},
		},
		"walk that extends an earlier match keeps its own path": {
			// The merged $.a.k.v takes the place of the `<<` key, so
			// the walk from $.a.k.<<.k.v reaches the anchor at an order
			// that extends the order of $.a.k.v. The entry it finds
			// there sorts first, so the walk must not stop.
			input: "s: &V {x: 0, name: 1}\na:\n  k:\n    <<: {k: {v: *V}, v: *V}\n",
			path:  "$..k.v..name",
			want:  []string{"$.a.k.<<.k.v.name"},
		},
		"tagged mapping is walked": {
			input: "a: !!map {name: 1}\n",
			path:  "$..name",
			want:  []string{"$.a.name"},
		},
		"tagged sequence is walked": {
			input: "a: !!seq [{name: 1}]\n",
			path:  "$..name",
			want:  []string{"$.a[0].name"},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			file, err := niceyaml.NewSourceFromString(tc.input).File()
			require.NoError(t, err)

			doc := file.Docs[0]

			matches, err := paths.MustParse(tc.path).Matches(doc)
			require.NoError(t, err)

			got := make([]string, 0, len(matches))
			for _, m := range matches {
				got = append(got, m.Path.String())
			}

			assert.Equal(t, tc.want, got)

			nodes, err := paths.MustParse(tc.path).Nodes(doc)
			require.NoError(t, err)
			require.Len(t, nodes, len(matches))

			for i, m := range matches {
				assert.Same(t, m.Node, nodes[i])
			}
		})
	}
}

func TestPath_Matches(t *testing.T) {
	t.Parallel()

	source := niceyaml.NewSourceFromString(stringtest.Input(`
		items:
		  - a
		  - b
		spec:
		  name: x
		  deep:
		    - name: y
		    - other: z
		base: &base
		  name: merged
		mixed:
		  <<: *base
		  extra: 1
		ref: *base
		'dot.key':
		  - q
		label: &label tagline
		keyed:
		  ? >-
		    folded
		    text
		  : 1
		  *label : 2
		twice: [*base, *base]
		nest:
		  a:
		    b:
		      a:
		        c: 1
		    c: 2
		siblings:
		  a:
		    id: 1
		    x:
		      - id: 2
		  b:
		    id: 3
		levels:
		  one:
		    two: [p, q]
	`))
	file, err := source.File()
	require.NoError(t, err)

	doc := file.Docs[0]

	tcs := map[string]struct {
		path paths.Path
		want []string
	}{
		"index all": {
			path: paths.Root().Child("items").IndexAll(),
			want: []string{"$.items[0]", "$.items[1]"},
		},
		"index all gives each element its own path below a long prefix": {
			path: paths.Root().Child("levels", "one", "two").IndexAll(),
			want: []string{"$.levels.one.two[0]", "$.levels.one.two[1]"},
		},
		"recursive": {
			path: paths.Root().Recursive("name"),
			want: []string{"$.spec.name", "$.spec.deep[0].name", "$.base.name"},
		},
		"recursive below a child": {
			path: paths.Root().Child("spec").Recursive("name"),
			want: []string{"$.spec.name", "$.spec.deep[0].name"},
		},
		"recursive gives each sibling its own path": {
			path: paths.Root().Recursive("id"),
			want: []string{"$.siblings.a.id", "$.siblings.a.x[0].id", "$.siblings.b.id"},
		},
		"recursive below a recursive match": {
			path: paths.Root().Recursive("a").Recursive("id"),
			want: []string{"$.siblings.a.id", "$.siblings.a.x[0].id"},
		},
		"single": {
			path: paths.Root().Child("spec", "name"),
			want: []string{"$.spec.name"},
		},
		"key of an entry": {
			path: paths.Root().Child("spec").Recursive("name").Key(),
			want: []string{"$.spec.name~", "$.spec.deep[0].name~"},
		},
		"through an alias keeps the path as written": {
			path: paths.Root().Child("ref", "name"),
			want: []string{"$.ref.name"},
		},
		"through a merge key keeps the path of the mapping": {
			path: paths.Root().Child("mixed", "name"),
			want: []string{"$.mixed.name"},
		},
		"each alias to one anchor is its own match": {
			path: paths.Root().Child("twice").IndexAll(),
			want: []string{"$.twice[0]", "$.twice[1]"},
		},
		"recursive then child keeps document order": {
			path: paths.Root().Child("nest").Recursive("a").Child("c"),
			want: []string{"$.nest.a.b.a.c", "$.nest.a.c"},
		},
		"quoted name": {
			path: paths.Root().Child("dot.key").IndexAll(),
			want: []string{"$.'dot.key'[0]"},
		},
		"recursive finds an alias key by its anchor's content": {
			path: paths.Root().Recursive("tagline"),
			want: []string{"$.keyed.tagline"},
		},
		"recursive finds a block scalar key by its content": {
			path: paths.Root().Recursive("folded text"),
			want: []string{"$.keyed.'folded text'"},
		},
		"nothing": {
			path: paths.Root().Child("missing").IndexAll(),
			want: nil,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			matches, err := tc.path.Matches(doc)
			require.NoError(t, err)

			var got []string

			for _, m := range matches {
				got = append(got, m.Path.String())
			}

			assert.Equal(t, tc.want, got)

			nodes, err := tc.path.Nodes(doc)
			require.NoError(t, err)
			require.Len(t, nodes, len(matches))

			for i, m := range matches {
				assert.Same(t, nodes[i], m.Node)

				// The path of a match selects its node alone.
				single, err := m.Path.Node(doc)
				require.NoError(t, err)
				assert.Same(t, m.Node, single)
			}
		})
	}
}

// emptyDocument returns the last document of input, which the tests below
// write as an explicit "---" header with nothing under it.
func emptyDocument(t *testing.T, input string) *ast.DocumentNode {
	t.Helper()

	docs, err := niceyaml.NewSourceFromString(input).Documents()
	require.NoError(t, err)
	require.NotEmpty(t, docs)

	node := docs[len(docs)-1].DocumentAST()
	require.Nil(t, node.Body, "the document should have no body")

	return node
}

func TestPath_EmptyDocument(t *testing.T) {
	t.Parallel()

	t.Run("the root is the null below the header", func(t *testing.T) {
		t.Parallel()

		doc := emptyDocument(t, "a: 1\n---\n")

		node, err := paths.Root().Node(doc)
		require.NoError(t, err)
		assert.Equal(t, ast.NullType, node.Type())

		tk, err := paths.Root().Token(doc)
		require.NoError(t, err)
		assert.Equal(t, token.DocumentHeaderType, tk.Type)
		assert.Equal(t, 2, tk.Position.Line)
	})

	t.Run("the key of the root is the same null", func(t *testing.T) {
		t.Parallel()

		doc := emptyDocument(t, "a: 1\n---\n")

		tk, err := paths.Root().Key().Token(doc)
		require.NoError(t, err)
		assert.Equal(t, token.DocumentHeaderType, tk.Type)
	})

	t.Run("a header over comments is the same null", func(t *testing.T) {
		t.Parallel()

		// A parse that keeps comments makes the comment group the body of
		// the document, which still holds no content.
		tcs := map[string]struct {
			input string
			line  int
		}{
			"after a document": {input: "a: 1\n---\n# comment\n", line: 2},
			"first document":   {input: "---\n# comment\n", line: 1},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				docs, err := niceyaml.NewSourceFromString(tc.input).Documents()
				require.NoError(t, err)
				require.NotEmpty(t, docs)

				doc := docs[len(docs)-1].DocumentAST()

				node, err := paths.Root().Node(doc)
				require.NoError(t, err)
				assert.Equal(t, ast.NullType, node.Type())

				tk, err := paths.Root().Token(doc)
				require.NoError(t, err)
				assert.Equal(t, token.DocumentHeaderType, tk.Type)
				assert.Equal(t, tc.line, tk.Position.Line)

				_, err = paths.Root().Child("a").Node(doc)
				require.ErrorIs(t, err, paths.ErrNoDocument)
				require.ErrorIs(t, err, paths.ErrNotFound)
			})
		}
	})

	t.Run("a match keeps the path as given", func(t *testing.T) {
		t.Parallel()

		doc := emptyDocument(t, "---\n")

		for _, path := range []paths.Path{paths.Root(), paths.Root().Key()} {
			matches, err := path.Matches(doc)
			require.NoError(t, err)
			require.Len(t, matches, 1)
			assert.Equal(t, path.String(), matches[0].Path.String())
		}
	})

	t.Run("a path with segments reaches nothing", func(t *testing.T) {
		t.Parallel()

		doc := emptyDocument(t, "a: 1\n---\n")

		tcs := map[string]struct {
			path paths.Path
		}{
			"child": {path: paths.Root().Child("a")},
			"index": {path: paths.Root().Index(0)},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				_, err := tc.path.Node(doc)
				require.ErrorIs(t, err, paths.ErrNoDocument)
				require.ErrorIs(t, err, paths.ErrNotFound)
			})
		}
	})

	t.Run("a document without a header reaches nothing", func(t *testing.T) {
		t.Parallel()

		_, err := paths.Root().Node(&ast.DocumentNode{})
		require.ErrorIs(t, err, paths.ErrNoDocument)
	})
}
