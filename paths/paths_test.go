package paths_test

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/token"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.jacobcolvin.com/x/stringtest"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/paths"
)

// yamlPath returns the goccy/go-yaml form of p, for a path that has one.
func yamlPath(t *testing.T, p paths.Path) *yaml.Path {
	t.Helper()

	yp, err := p.YAMLPath()
	require.NoError(t, err)
	require.NotNil(t, yp)

	return yp
}

func TestPath_Build(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		path     paths.Path
		want     string
		wantYAML string
	}{
		"root path": {
			path: paths.Doc(),
			want: "$",
		},
		"current path": {
			path:     paths.Current(),
			want:     "@",
			wantYAML: "$",
		},
		"current path with selectors": {
			path:     paths.Current().Child("items").Index(0).Key(),
			want:     "@.items[0]~",
			wantYAML: "$.items[0]",
		},
		"zero value with a child": {
			path:     paths.Path{}.Child("kind"),
			want:     "@.kind",
			wantYAML: "$.kind",
		},
		"chained children": {
			path: paths.Doc().Child("metadata", "labels"),
			want: "$.metadata.labels",
		},
		"child then index": {
			path: paths.Doc().Child("items").Index(0),
			want: "$.items[0]",
		},
		"variadic index": {
			path: paths.Doc().Child("matrix").Index(0, 1),
			want: "$.matrix[0][1]",
		},
		"index all": {
			path: paths.Doc().Child("items").IndexAll(),
			want: "$.items[*]",
		},
		"recursive descent": {
			path: paths.Doc().Recursive("name"),
			want: "$..name",
		},
		"large index": {
			path: paths.Doc().Child("items").Index(999),
			want: "$.items[999]",
		},
		"recursive with index": {
			path: paths.Doc().Recursive("items").Index(0),
			want: "$..items[0]",
		},
		"multiple recursive": {
			path: paths.Doc().Recursive("containers").Recursive("name"),
			want: "$..containers..name",
		},
		"child after index": {
			path: paths.Doc().Child("items").Index(0).Child("name"),
			want: "$.items[0].name",
		},
		"index all then index": {
			path: paths.Doc().Child("matrix").IndexAll().Index(0),
			want: "$.matrix[*][0]",
		},
		"dotted child name is quoted": {
			path: paths.Doc().Child("kubernetes.io/name"),
			want: "$.'kubernetes.io/name'",
		},
		"child name with quote is quoted and escaped": {
			path:     paths.Doc().Child("it's"),
			want:     `$.'it\'s'`,
			wantYAML: "$.it's",
		},
		"empty child name is quoted, but goccy leaves it bare": {
			path:     paths.Doc().Child(""),
			want:     "$.''",
			wantYAML: "$.",
		},
		"empty recursive name is quoted, but goccy leaves it bare": {
			path:     paths.Doc().Recursive(""),
			want:     "$..''",
			wantYAML: "$..",
		},
		"child name with brackets is quoted": {
			path:     paths.Doc().Child("a[0]"),
			want:     "$.'a[0]'",
			wantYAML: "$.a[0]",
		},
		"recursive name with a dot is quoted, but goccy cannot quote it": {
			path:     paths.Doc().Recursive("x.y"),
			want:     "$..'x.y'",
			wantYAML: "$..x.y",
		},
		"key selector, which goccy has no form for": {
			path:     paths.Doc().Child("spec", "name").Key(),
			want:     "$.spec.name~",
			wantYAML: "$.spec.name",
		},
		"key selector mid-path": {
			path:     paths.Doc().Child("a").Key().Child("b"),
			want:     "$.a~.b",
			wantYAML: "$.a.b",
		},
		"name wrapped in single quotes": {
			path:     paths.Doc().Child("'x'"),
			want:     `$.'\'x\''`,
			wantYAML: "$.'x'",
		},
		"tilde in a name is quoted": {
			path:     paths.Doc().Child("a~b"),
			want:     "$.'a~b'",
			wantYAML: "$.a~b",
		},
		"colon and space in a name are quoted": {
			path:     paths.Doc().Child("x: y"),
			want:     "$.'x: y'",
			wantYAML: "$.x: y",
		},
		"trailing space in a name is quoted": {
			path:     paths.Doc().Child("name "),
			want:     "$.'name '",
			wantYAML: "$.name ",
		},
		"line break in a name is escaped": {
			path:     paths.Doc().Child("a\nb"),
			want:     `$.'a\nb'`,
			wantYAML: "$.a\nb",
		},
		"tab and carriage return in a name are escaped": {
			path:     paths.Doc().Child("a\tb\rc"),
			want:     `$.'a\tb\rc'`,
			wantYAML: "$.a\tb\rc",
		},
		"escape character in a name is escaped": {
			path:     paths.Doc().Child("a\x1b[31mb"),
			want:     `$.'a\u001b[31mb'`,
			wantYAML: "$.a\x1b[31mb",
		},
		"delete and a C1 control in a name are escaped": {
			path:     paths.Doc().Child("a\x7fb\u0085c"),
			want:     `$.'a\u007fb\u0085c'`,
			wantYAML: "$.a\x7fb\u0085c",
		},
		"line and paragraph separators in a name are escaped": {
			path:     paths.Doc().Child("a\u2028b\u2029c"),
			want:     `$.'a\u2028b\u2029c'`,
			wantYAML: "$.a\u2028b\u2029c",
		},
		"forged line in a name stays on one line": {
			path: paths.Doc().Child(stringtest.JoinLF("a", "other.yaml:9:9: $.secret: forged")),
			want: `$.'a\nother.yaml:9:9: $.secret: forged'`,
			wantYAML: stringtest.JoinLF(
				"$.'a",
				"other.yaml:9:9: $.secret: forged'",
			),
		},
		"recursive name with a space is quoted": {
			path:     paths.Doc().Recursive("a b"),
			want:     "$..'a b'",
			wantYAML: "$..a b",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, tc.path.String())
			assert.Equal(t, tc.want[0] == '$', tc.path.IsAbsolute())

			// The goccy form only differs where goccy quotes a name
			// differently or has no form for a selector.
			assert.Equal(t, cmp.Or(tc.wantYAML, tc.want), yamlPath(t, tc.path).String())
		})
	}
}

func TestPath_Index_Negative(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		want string
		idx  []int
	}{
		"not-found sentinel": {
			idx:  []int{slices.Index([]string{"a", "b"}, "c")},
			want: "paths.Path.Index: index -1 is below 0",
		},
		"after a valid index": {
			idx:  []int{0, -2},
			want: "paths.Path.Index: index -2 is below 0",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.PanicsWithValue(t, tc.want, func() {
				paths.Doc().Child("items").Index(tc.idx...)
			})
		})
	}
}

func TestPath_ChildAll(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		path paths.Path
		want string
	}{
		"after a child": {
			path: paths.Doc().Child("jobs").ChildAll(),
			want: "$.jobs.*",
		},
		"on the root": {
			path: paths.Doc().ChildAll(),
			want: "$.*",
		},
		"then a child": {
			path: paths.Doc().Child("jobs").ChildAll().Child("steps"),
			want: "$.jobs.*.steps",
		},
		"then index all": {
			path: paths.Doc().Child("jobs").ChildAll().Child("steps").IndexAll().Child("uses"),
			want: "$.jobs.*.steps[*].uses",
		},
		"then a key selector": {
			path: paths.Doc().Child("jobs").ChildAll().Key(),
			want: "$.jobs.*~",
		},
		"twice": {
			path: paths.Doc().Child("paths").ChildAll().ChildAll(),
			want: "$.paths.*.*",
		},
		"after index all": {
			path: paths.Doc().Child("items").IndexAll().ChildAll(),
			want: "$.items[*].*",
		},
		"after a recursive selector": {
			path: paths.Doc().Recursive("env").ChildAll(),
			want: "$..env.*",
		},
		"after the name star": {
			path: paths.Doc().Child("*").ChildAll(),
			want: "$.'*'.*",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			// The goccy syntax has no selector for every entry of a
			// mapping, and a path without it would name the mapping.
			assertNoYAMLPath(t, tc.path, tc.want)
		})
	}

	t.Run("the name star is a child selector", func(t *testing.T) {
		t.Parallel()

		star := paths.Doc().Child("jobs", "*")

		assert.NotEqual(t, paths.Doc().Child("jobs").ChildAll(), star)
		assert.Equal(t, "$.jobs.'*'", star.String())
		assert.Equal(t, "$.jobs.'*'", yamlPath(t, star).String())
	})
}

// assertNoYAMLPath asserts that path prints as want and parses back from
// it, and that goccy/go-yaml has no form for it. YAMLPath names the path
// it cannot convert, and the goccy parser rejects the text.
func assertNoYAMLPath(t *testing.T, path paths.Path, want string) {
	t.Helper()

	assert.Equal(t, want, path.String())

	got, err := paths.Parse(want)
	require.NoError(t, err)
	assert.Equal(t, path, got)

	yp, err := path.YAMLPath()
	require.ErrorIs(t, err, paths.ErrNoYAMLPath)
	require.EqualError(t, err, "convert "+want+": selector has no goccy/go-yaml equivalent")
	assert.Nil(t, yp)

	_, err = yaml.PathString(want)
	require.Error(t, err)
}

func TestPath_RecursiveAll(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		path paths.Path
		want string
	}{
		"on the root": {
			path: paths.Doc().RecursiveAll(),
			want: "$..*",
		},
		"after a child": {
			path: paths.Doc().Child("spec").RecursiveAll(),
			want: "$.spec..*",
		},
		"then a child": {
			path: paths.Doc().RecursiveAll().Child("name"),
			want: "$..*.name",
		},
		"then an index": {
			path: paths.Doc().RecursiveAll().Index(0),
			want: "$..*[0]",
		},
		"then a key selector": {
			path: paths.Doc().RecursiveAll().Key(),
			want: "$..*~",
		},
		"twice": {
			path: paths.Doc().RecursiveAll().RecursiveAll(),
			want: "$..*..*",
		},
		"after index all": {
			path: paths.Doc().Child("items").IndexAll().RecursiveAll(),
			want: "$.items[*]..*",
		},
		"after child all": {
			path: paths.Doc().ChildAll().RecursiveAll(),
			want: "$.*..*",
		},
		"after a recursive selector": {
			path: paths.Doc().Recursive("env").RecursiveAll(),
			want: "$..env..*",
		},
		"after the recursive name star": {
			path: paths.Doc().Recursive("*").RecursiveAll(),
			want: "$..'*'..*",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			// The goccy syntax takes a name alone after `..`.
			assertNoYAMLPath(t, tc.path, tc.want)
		})
	}

	t.Run("the name star is a recursive selector", func(t *testing.T) {
		t.Parallel()

		star := paths.Doc().Recursive("*")

		assert.False(t, paths.Doc().RecursiveAll().Equal(star))
		assert.Equal(t, "$..'*'", star.String())
		assert.Equal(t, star, paths.MustParse("$..'*'"))
	})
}

func TestPath_Immutable(t *testing.T) {
	t.Parallel()

	t.Run("shared prefix is not aliased", func(t *testing.T) {
		t.Parallel()

		spec := paths.Doc().Child("spec")
		replicas := spec.Child("replicas")
		image := spec.Child("image")

		assert.Equal(t, "$.spec.replicas", replicas.String())
		assert.Equal(t, "$.spec.image", image.String())
		assert.Equal(t, "$.spec", spec.String())
	})

	t.Run("extending does not change the receiver", func(t *testing.T) {
		t.Parallel()

		p := paths.Doc().Child("metadata", "name")

		assert.Equal(t, "$.metadata.name.labels", p.Child("labels").String())
		assert.Equal(t, "$.metadata.name", p.String())
	})

	t.Run("zero value is the current node", func(t *testing.T) {
		t.Parallel()

		var p paths.Path

		assert.Equal(t, paths.Current(), p)
		assert.Equal(t, "@", p.String())
		assert.Equal(t, "@.a", p.Child("a").String())
	})

	t.Run("an empty extension is the root", func(t *testing.T) {
		t.Parallel()

		assert.Equal(t, paths.Doc(), paths.Doc().Child())
		assert.Equal(t, paths.Doc(), paths.Doc().Index())
		assert.Equal(t, paths.Doc(), paths.Doc().Join(paths.Doc()))
		assert.Equal(t, paths.Doc().Child("a"), paths.Doc().Child("a").Child())
		assert.Equal(t, paths.Current(), paths.Current().Child())
		assert.Equal(t, paths.Current(), paths.Current().Join(paths.Current()))
	})
}

func TestPath_Join(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		p    paths.Path
		want string
		qs   []paths.Path
	}{
		"current to doc": {
			p:    paths.Doc(),
			qs:   []paths.Path{paths.Current()},
			want: "$",
		},
		"doc to doc": {
			p:    paths.Doc(),
			qs:   []paths.Path{paths.Doc()},
			want: "$",
		},
		"relative path to doc": {
			p:    paths.Doc(),
			qs:   []paths.Path{paths.Current().Child("open")},
			want: "$.open",
		},
		"current to path": {
			p:    paths.Doc().Child("spec", "hours"),
			qs:   []paths.Path{paths.Current()},
			want: "$.spec.hours",
		},
		"relative path to path": {
			p:    paths.Doc().Child("spec", "hours"),
			qs:   []paths.Path{paths.Current().Child("open")},
			want: "$.spec.hours.open",
		},
		"relative path to relative path": {
			p:    paths.Current().Child("hours"),
			qs:   []paths.Path{paths.Current().Child("open")},
			want: "@.hours.open",
		},
		"relative path to current": {
			p:    paths.Current(),
			qs:   []paths.Path{paths.Current().Child("open")},
			want: "@.open",
		},
		"absolute path replaces a path": {
			p:    paths.Doc().Child("spec", "hours"),
			qs:   []paths.Path{paths.Doc().Child("name")},
			want: "$.name",
		},
		"absolute path replaces a relative path": {
			p:    paths.Current().Child("hours"),
			qs:   []paths.Path{paths.Doc().Child("name")},
			want: "$.name",
		},
		"doc replaces a path": {
			p:    paths.Doc().Child("spec", "hours"),
			qs:   []paths.Path{paths.Doc()},
			want: "$",
		},
		"keeps every selector kind": {
			p:    paths.Doc().Child("items").Index(0),
			qs:   []paths.Path{paths.Current().Recursive("name").IndexAll().Key()},
			want: "$.items[0]..name[*]~",
		},
		"nothing": {
			p:    paths.Doc().Child("spec"),
			want: "$.spec",
		},
		"nothing to current": {
			p:    paths.Current().Child("spec"),
			want: "@.spec",
		},
		"several paths in order": {
			p:    paths.Doc().Child("spec"),
			qs:   []paths.Path{paths.Current().Child("hours"), paths.Current(), paths.Current().Index(1).Key()},
			want: "$.spec.hours[1]~",
		},
		"several paths after an absolute one": {
			p:    paths.Doc().Child("spec"),
			qs:   []paths.Path{paths.Current().Child("hours"), paths.Doc().Child("meta"), paths.Current().Index(1)},
			want: "$.meta[1]",
		},
		"last absolute path wins": {
			p:    paths.Current().Child("spec"),
			qs:   []paths.Path{paths.Doc().Child("a"), paths.Doc().Child("b"), paths.Current().Child("c")},
			want: "$.b.c",
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

		p := paths.Doc().Child("spec")
		q := paths.Current().Child("open")
		joined := p.Join(q)

		assert.Equal(t, "$.spec.open", joined.String())
		assert.Equal(t, "$.spec", p.String())
		assert.Equal(t, "@.open", q.String())
		assert.Equal(t, "$.spec.open.x", joined.Child("x").String())
		assert.Equal(t, "$.spec", p.String())
	})

	t.Run("leaves a replacing path as it was", func(t *testing.T) {
		t.Parallel()

		p := paths.Doc().Child("spec")
		q := paths.Doc().Child("name")
		joined := p.Join(q, paths.Current().Child("first"))

		assert.Equal(t, "$.name.first", joined.String())
		assert.Equal(t, "$.name", q.String())
	})
}

func TestPath_CutPrefix(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		p      paths.Path
		prefix paths.Path
		want   string
		ok     bool
	}{
		"root from root": {
			p:      paths.Doc(),
			prefix: paths.Doc(),
			want:   "@",
			ok:     true,
		},
		"root from path": {
			p:      paths.Doc().Child("spec", "hours"),
			prefix: paths.Doc(),
			want:   "@.spec.hours",
			ok:     true,
		},
		"path from itself": {
			p:      paths.Doc().Child("spec", "hours"),
			prefix: paths.Doc().Child("spec", "hours"),
			want:   "@",
			ok:     true,
		},
		"leading selectors": {
			p:      paths.Doc().Child("spec", "hours", "open"),
			prefix: paths.Doc().Child("spec", "hours"),
			want:   "@.open",
			ok:     true,
		},
		"leading selectors of a relative path": {
			p:      paths.Current().Child("spec", "hours", "open"),
			prefix: paths.Current().Child("spec", "hours"),
			want:   "@.open",
			ok:     true,
		},
		"current from a relative path": {
			p:      paths.Current().Child("spec"),
			prefix: paths.Current(),
			want:   "@.spec",
			ok:     true,
		},
		"relative prefix of an absolute path": {
			p:      paths.Doc().Child("spec", "hours"),
			prefix: paths.Current().Child("spec"),
			want:   "$.spec.hours",
		},
		"absolute prefix of a relative path": {
			p:      paths.Current().Child("spec", "hours"),
			prefix: paths.Doc().Child("spec"),
			want:   "@.spec.hours",
		},
		"current from an absolute path": {
			p:      paths.Doc().Child("spec"),
			prefix: paths.Current(),
			want:   "$.spec",
		},
		"keeps every selector kind": {
			p:      paths.Doc().Child("items").Index(0).Recursive("name").IndexAll().Key(),
			prefix: paths.Doc().Child("items").Index(0),
			want:   "@..name[*]~",
			ok:     true,
		},
		"longer prefix": {
			p:      paths.Doc().Child("spec"),
			prefix: paths.Doc().Child("spec", "hours"),
			want:   "$.spec",
		},
		"another name": {
			p:      paths.Doc().Child("spec", "hours"),
			prefix: paths.Doc().Child("meta"),
			want:   "$.spec.hours",
		},
		"another index": {
			p:      paths.Doc().Child("items").Index(1).Child("name"),
			prefix: paths.Doc().Child("items").Index(0),
			want:   "$.items[1].name",
		},
		"wildcard against an index": {
			p:      paths.Doc().Child("items").Index(0).Child("name"),
			prefix: paths.Doc().Child("items").IndexAll(),
			want:   "$.items[0].name",
		},
		"mapping wildcard against a name": {
			p:      paths.Doc().Child("jobs", "build", "steps"),
			prefix: paths.Doc().Child("jobs").ChildAll(),
			want:   "$.jobs.build.steps",
		},
		"mapping wildcard against itself": {
			p:      paths.Doc().Child("jobs").ChildAll().Child("steps"),
			prefix: paths.Doc().Child("jobs").ChildAll(),
			want:   "@.steps",
			ok:     true,
		},
		"mapping wildcard against the name star": {
			p:      paths.Doc().Child("jobs", "*"),
			prefix: paths.Doc().Child("jobs").ChildAll(),
			want:   "$.jobs.'*'",
		},
		"keeps a mapping wildcard": {
			p:      paths.Doc().Child("jobs").ChildAll().Key(),
			prefix: paths.Doc().Child("jobs"),
			want:   "@.*~",
			ok:     true,
		},
		"recursive wildcard against a name": {
			p:      paths.Doc().Child("spec", "name"),
			prefix: paths.Doc().RecursiveAll(),
			want:   "$.spec.name",
		},
		"recursive wildcard against itself": {
			p:      paths.Doc().Child("spec").RecursiveAll().Key(),
			prefix: paths.Doc().Child("spec").RecursiveAll(),
			want:   "@~",
			ok:     true,
		},
		"key against its value": {
			p:      paths.Doc().Child("spec").Key(),
			prefix: paths.Doc().Child("spec").Child("hours"),
			want:   "$.spec~",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, ok := tc.p.CutPrefix(tc.prefix)
			assert.Equal(t, tc.ok, ok)
			assert.Equal(t, tc.want, got.String())
			assert.Equal(t, paths.MustParse(tc.want), got)

			if ok {
				assert.Equal(t, tc.p, tc.prefix.Join(got))
			}
		})
	}

	t.Run("leaves both paths as they were", func(t *testing.T) {
		t.Parallel()

		p := paths.Doc().Child("spec", "hours", "open")
		prefix := paths.Doc().Child("spec")

		rest, ok := p.CutPrefix(prefix)
		require.True(t, ok)

		assert.Equal(t, "@.hours.open.x", rest.Child("x").String())
		assert.Equal(t, "@.hours.open", rest.String())
		assert.Equal(t, "$.spec.hours.open", p.String())
		assert.Equal(t, "$.spec", prefix.String())
	})
}

func TestPath_IsRoot(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		path paths.Path
		want bool
	}{
		"doc":                  {path: paths.Doc(), want: true},
		"parsed doc":           {path: paths.MustParse("$"), want: true},
		"doc child":            {path: paths.Doc().Child("a")},
		"doc key":              {path: paths.Doc().Key()},
		"doc index":            {path: paths.Doc().Index(0)},
		"doc mapping wildcard": {path: paths.Doc().ChildAll()},
		"current":              {path: paths.Current()},
		"zero value":           {path: paths.Path{}},
		"parsed current":       {path: paths.MustParse("@")},
		"current child":        {path: paths.Current().Child("a")},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, tc.path.IsRoot())
		})
	}
}

func TestPath_IsAbsolute(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		path paths.Path
		want bool
	}{
		"doc":            {path: paths.Doc(), want: true},
		"doc child":      {path: paths.Doc().Child("a"), want: true},
		"parsed doc":     {path: paths.MustParse("$.a[0]"), want: true},
		"doc parent":     {path: parentOf(t, paths.Doc().Child("a")), want: true},
		"current":        {path: paths.Current()},
		"zero value":     {path: paths.Path{}},
		"current child":  {path: paths.Current().Child("a")},
		"parsed current": {path: paths.MustParse("@.a[0]")},
		"current parent": {path: parentOf(t, paths.Current().Child("a"))},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, tc.path.IsAbsolute())
		})
	}
}

func TestPath_Parent(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		p    paths.Path
		want string
		ok   bool
	}{
		"root": {
			p:    paths.Doc(),
			want: "$",
		},
		"zero value": {want: "@"},
		"current": {
			p:    paths.Current(),
			want: "@",
		},
		"one child": {
			p:    paths.Doc().Child("spec"),
			want: "$",
			ok:   true,
		},
		"one child of current": {
			p:    paths.Current().Child("spec"),
			want: "@",
			ok:   true,
		},
		"relative child": {
			p:    paths.Current().Child("spec", "hours"),
			want: "@.spec",
			ok:   true,
		},
		"relative key": {
			p:    paths.Current().Key(),
			want: "@",
			ok:   true,
		},
		"child": {
			p:    paths.Doc().Child("spec", "hours"),
			want: "$.spec",
			ok:   true,
		},
		"index": {
			p:    paths.Doc().Child("items").Index(2),
			want: "$.items",
			ok:   true,
		},
		"key": {
			p:    paths.Doc().Child("spec").Key(),
			want: "$.spec",
			ok:   true,
		},
		"key at the root": {
			p:    paths.Doc().Key(),
			want: "$",
			ok:   true,
		},
		"sequence wildcard": {
			p:    paths.Doc().Child("items").IndexAll(),
			want: "$.items",
			ok:   true,
		},
		"mapping wildcard": {
			p:    paths.Doc().Child("jobs").ChildAll(),
			want: "$.jobs",
			ok:   true,
		},
		"key of a mapping wildcard": {
			p:    paths.Doc().Child("jobs").ChildAll().Key(),
			want: "$.jobs.*",
			ok:   true,
		},
		"recursive wildcard": {
			p:    paths.Doc().Child("spec").RecursiveAll(),
			want: "$.spec",
			ok:   true,
		},
		"key of a recursive wildcard": {
			p:    paths.Doc().RecursiveAll().Key(),
			want: "$..*",
			ok:   true,
		},
		"recursive": {
			p:    paths.Doc().Child("spec").Recursive("name"),
			want: "$.spec",
			ok:   true,
		},
		"quoted name": {
			p:    paths.Doc().Child("a.b", "c d"),
			want: "$.'a.b'",
			ok:   true,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, ok := tc.p.Parent()
			assert.Equal(t, tc.ok, ok)
			assert.Equal(t, tc.want, got.String())
			assert.Equal(t, paths.MustParse(tc.want), got)
		})
	}

	t.Run("walks up to the anchor", func(t *testing.T) {
		t.Parallel()

		for _, anchor := range []string{"$", "@"} {
			var got []string

			p, ok := paths.MustParse(anchor+".items[0].tags.*~"), true
			for ok {
				got = append(got, p.String())
				p, ok = p.Parent()
			}

			want := []string{".items[0].tags.*~", ".items[0].tags.*", ".items[0].tags", ".items[0]", ".items", ""}
			for i, w := range want {
				want[i] = anchor + w
			}

			assert.Equal(t, want, got)
		}
	})

	t.Run("leaves the path as it was", func(t *testing.T) {
		t.Parallel()

		p := paths.Doc().Child("spec", "hours", "open")

		parent, ok := p.Parent()
		require.True(t, ok)

		assert.Equal(t, "$.spec.hours.close", parent.Child("close").String())
		assert.Equal(t, "$.spec.hours", parent.String())
		assert.Equal(t, "$.spec.hours.open", p.String())
	})
}

func TestPath_Equal(t *testing.T) {
	t.Parallel()

	var zero paths.Path

	tcs := map[string]struct {
		p    paths.Path
		q    paths.Path
		want bool
	}{
		"root and root": {
			p:    paths.Doc(),
			q:    paths.Doc(),
			want: true,
		},
		"current and the zero value": {
			p:    paths.Current(),
			q:    zero,
			want: true,
		},
		"root and the zero value": {
			p: paths.Doc(),
			q: zero,
		},
		"root and current": {
			p: paths.Doc(),
			q: paths.Current(),
		},
		"absolute and relative path": {
			p: paths.Doc().Child("a", "b"),
			q: paths.Current().Child("a", "b"),
		},
		"relative and a parsed relative path": {
			p:    paths.Current().Child("a").Index(0),
			q:    paths.MustParse("@.a[0]"),
			want: true,
		},
		"current and a cut path": {
			p:    paths.Current(),
			q:    cutOf(t, paths.Doc().Child("a"), paths.Doc().Child("a")),
			want: true,
		},
		"root and a parsed root": {
			p:    paths.Doc(),
			q:    paths.MustParse("$"),
			want: true,
		},
		"root and the parent of a child": {
			p:    paths.Doc(),
			q:    parentOf(t, paths.Doc().Child("a")),
			want: true,
		},
		"built and parsed": {
			p:    paths.Doc().Child("items").Index(0).Child("name"),
			q:    paths.MustParse("$.items[0].name"),
			want: true,
		},
		"quoted and unquoted spelling": {
			p:    paths.MustParse("$.'name'"),
			q:    paths.MustParse("$.name"),
			want: true,
		},
		"every selector kind": {
			p:    paths.Doc().Child("a").ChildAll().Index(1).IndexAll().Recursive("b").Key(),
			q:    paths.MustParse("$.a.*[1][*]..b~"),
			want: true,
		},
		"joined and built": {
			p:    paths.Doc().Child("spec").Join(paths.Current().Child("hours")),
			q:    paths.Doc().Child("spec", "hours"),
			want: true,
		},
		"another name": {
			p: paths.Doc().Child("a"),
			q: paths.Doc().Child("b"),
		},
		"another index": {
			p: paths.Doc().Index(0),
			q: paths.Doc().Index(1),
		},
		"prefix": {
			p: paths.Doc().Child("a"),
			q: paths.Doc().Child("a", "b"),
		},
		"root and a child": {
			p: paths.Doc(),
			q: paths.Doc().Child("a"),
		},
		"value and key": {
			p: paths.Doc().Child("a"),
			q: paths.Doc().Child("a").Key(),
		},
		"sequence wildcard and an index": {
			p: paths.Doc().Child("items").IndexAll(),
			q: paths.Doc().Child("items").Index(0),
		},
		"mapping wildcard and a name": {
			p: paths.Doc().Child("jobs").ChildAll(),
			q: paths.Doc().Child("jobs", "build"),
		},
		"mapping wildcard and the name star": {
			p: paths.Doc().ChildAll(),
			q: paths.Doc().Child("*"),
		},
		"mapping wildcard and sequence wildcard": {
			p: paths.Doc().ChildAll(),
			q: paths.Doc().IndexAll(),
		},
		"recursive wildcard and recursive wildcard": {
			p:    paths.Doc().Child("spec").RecursiveAll(),
			q:    paths.MustParse("$.spec..*"),
			want: true,
		},
		"recursive wildcard and the recursive name star": {
			p: paths.Doc().RecursiveAll(),
			q: paths.Doc().Recursive("*"),
		},
		"recursive wildcard and mapping wildcard": {
			p: paths.Doc().RecursiveAll(),
			q: paths.Doc().ChildAll(),
		},
		"child and recursive of one name": {
			p: paths.Doc().Child("a"),
			q: paths.Doc().Recursive("a"),
		},
		"name and the index it spells": {
			p: paths.Doc().Child("0"),
			q: paths.Doc().Index(0),
		},
		"empty name and root": {
			p: paths.Doc().Child(""),
			q: paths.Doc(),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, tc.p.Equal(tc.q))
			assert.Equal(t, tc.want, tc.q.Equal(tc.p))
			assert.Equal(t, tc.want, tc.p.String() == tc.q.String())
		})
	}
}

// parentOf returns the parent of p, which must have one.
func parentOf(t *testing.T, p paths.Path) paths.Path {
	t.Helper()

	parent, ok := p.Parent()
	require.True(t, ok)

	return parent
}

// cutOf returns p without prefix, which p must start with.
func cutOf(t *testing.T, p, prefix paths.Path) paths.Path {
	t.Helper()

	rest, ok := p.CutPrefix(prefix)
	require.True(t, ok)

	return rest
}

func TestPath_MarshalText(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		p    paths.Path
		want string
	}{
		"root": {
			p:    paths.Doc(),
			want: "$",
		},
		"zero value": {want: "@"},
		"current": {
			p:    paths.Current(),
			want: "@",
		},
		"children and an index": {
			p:    paths.Doc().Child("items").Index(0).Child("name"),
			want: "$.items[0].name",
		},
		"relative children and an index": {
			p:    paths.Current().Child("items").Index(0).Child("name"),
			want: "@.items[0].name",
		},
		"relative key": {
			p:    paths.Current().Child("spec").Key(),
			want: "@.spec~",
		},
		"key": {
			p:    paths.Doc().Child("spec").Key(),
			want: "$.spec~",
		},
		"mapping wildcard": {
			p:    paths.Doc().Child("jobs").ChildAll().Child("steps"),
			want: "$.jobs.*.steps",
		},
		"key of a mapping wildcard": {
			p:    paths.Doc().Child("jobs").ChildAll().Key(),
			want: "$.jobs.*~",
		},
		"recursive wildcard": {
			p:    paths.Doc().Child("spec").RecursiveAll().Key(),
			want: "$.spec..*~",
		},
		"sequence wildcard": {
			p:    paths.Doc().Child("items").IndexAll(),
			want: "$.items[*]",
		},
		"recursive": {
			p:    paths.Doc().Recursive("name"),
			want: "$..name",
		},
		"quoted names": {
			p:    paths.Doc().Child("a.b", "it's", "", "*").Recursive("x y"),
			want: `$.'a.b'.'it\'s'.''.'*'..'x y'`,
		},
		"name that is not valid UTF-8": {
			p:    paths.Doc().Child("\xff"),
			want: "$.\xff",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			text, err := tc.p.MarshalText()
			require.NoError(t, err)
			assert.Equal(t, tc.want, string(text))
			assert.Equal(t, tc.p.String(), string(text))

			var got paths.Path

			require.NoError(t, got.UnmarshalText(text))
			assert.True(t, tc.p.Equal(got), "got %s", got)
		})
	}

	t.Run("writes a JSON string", func(t *testing.T) {
		t.Parallel()

		type report struct {
			Path  paths.Path   `json:"path"`
			Ptr   *paths.Path  `json:"ptr"`
			Paths []paths.Path `json:"paths"`
		}

		key := paths.Doc().Child("jobs").ChildAll().Key()

		data, err := json.Marshal(report{
			Path:  paths.Doc().Child("items").Index(0),
			Ptr:   &key,
			Paths: []paths.Path{paths.Doc(), paths.Doc().Child("a b"), paths.Current().Child("c")},
		})
		require.NoError(t, err)
		assert.JSONEq(t, `{"path":"$.items[0]","ptr":"$.jobs.*~","paths":["$","$.'a b'","@.c"]}`, string(data))
	})

	t.Run("writes a YAML string", func(t *testing.T) {
		t.Parallel()

		type report struct {
			Path paths.Path `yaml:"path"`
		}

		data, err := yaml.Marshal(report{Path: paths.Doc().Child("items").Index(0).Key()})
		require.NoError(t, err)
		assert.Equal(t, "path: $.items[0]~\n", string(data))

		relative := report{Path: paths.Current().Child("items").Index(0)}

		data, err = yaml.Marshal(relative)
		require.NoError(t, err)

		var got report

		require.NoError(t, yaml.Unmarshal(data, &got))
		assert.Equal(t, relative, got)
	})
}

func TestPath_UnmarshalText(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		text string
		want paths.Path
	}{
		"root": {
			text: "$",
			want: paths.Doc(),
		},
		"current": {
			text: "@",
			want: paths.Current(),
		},
		"children and an index": {
			text: "$.items[0].name",
			want: paths.Doc().Child("items").Index(0).Child("name"),
		},
		"relative children and an index": {
			text: "@.items[0].name",
			want: paths.Current().Child("items").Index(0).Child("name"),
		},
		"key": {
			text: "$.spec~",
			want: paths.Doc().Child("spec").Key(),
		},
		"mapping wildcard and its key": {
			text: "$.jobs.*~",
			want: paths.Doc().Child("jobs").ChildAll().Key(),
		},
		"recursive wildcard": {
			text: "$..*.name",
			want: paths.Doc().RecursiveAll().Child("name"),
		},
		"sequence wildcard": {
			text: "$.items[*].name",
			want: paths.Doc().Child("items").IndexAll().Child("name"),
		},
		"recursive": {
			text: "$..'a.b'",
			want: paths.Doc().Recursive("a.b"),
		},
		"quoted star": {
			text: "$.'*'",
			want: paths.Doc().Child("*"),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			// A path set before the call must not show through.
			got := paths.Doc().Child("before")

			require.NoError(t, got.UnmarshalText([]byte(tc.text)))
			assert.True(t, tc.want.Equal(got), "got %s", got)
			assert.Equal(t, tc.want, got)
		})
	}

	t.Run("malformed expression", func(t *testing.T) {
		t.Parallel()

		got := paths.Doc().Child("before")

		err := got.UnmarshalText([]byte("items[0]"))
		require.ErrorIs(t, err, paths.ErrInvalidPath)
		assert.Equal(t, "$.before", got.String())

		_, parseErr := paths.Parse("items[0]")
		require.Error(t, parseErr)
		assert.Equal(t, parseErr.Error(), err.Error())
	})

	t.Run("empty text", func(t *testing.T) {
		t.Parallel()

		var got paths.Path

		require.ErrorIs(t, got.UnmarshalText(nil), paths.ErrInvalidPath)
	})

	t.Run("reads a JSON string", func(t *testing.T) {
		t.Parallel()

		type rule struct {
			Path  paths.Path   `json:"path"`
			Ptr   *paths.Path  `json:"ptr"`
			Paths []paths.Path `json:"paths"`
		}

		var got rule

		require.NoError(
			t,
			json.Unmarshal([]byte(`{"path":"$.items[0]","ptr":"$.jobs.*~","paths":["$","$.'a b'","@.c"]}`), &got),
		)
		assert.Equal(t, "$.items[0]", got.Path.String())
		require.NotNil(t, got.Ptr)
		assert.Equal(t, "$.jobs.*~", got.Ptr.String())
		require.Len(t, got.Paths, 3)
		assert.Equal(t, "$", got.Paths[0].String())
		assert.Equal(t, "$.'a b'", got.Paths[1].String())
		assert.Equal(t, paths.Current().Child("c"), got.Paths[2])

		err := json.Unmarshal([]byte(`{"path":"$.items["}`), &got)
		require.ErrorIs(t, err, paths.ErrInvalidPath)
	})

	t.Run("reads a YAML string", func(t *testing.T) {
		t.Parallel()

		type rule struct {
			Path paths.Path `yaml:"path"`
		}

		got, err := yamltest.FirstDocument(t, "path: $.items[0].name~\n").Decode[rule](t.Context())
		require.NoError(t, err)
		assert.Equal(t, "$.items[0].name~", got.Path.String())

		// A plain scalar cannot start with @, so the document quotes a
		// relative path.
		got, err = yamltest.FirstDocument(t, "path: '@.items[0]'\n").Decode[rule](t.Context())
		require.NoError(t, err)
		assert.Equal(t, paths.Current().Child("items").Index(0), got.Path)
	})
}

func TestParse(t *testing.T) {
	t.Parallel()

	// A case that leaves want empty reads back as the expression it parses.
	tcs := map[string]struct {
		expr string
		want string
	}{
		"simple path":       {expr: "$.foo"},
		"nested path":       {expr: "$.foo.bar.baz"},
		"array index":       {expr: "$.items[0]"},
		"array wildcard":    {expr: "$.items[*]"},
		"recursive descent": {expr: "$..name"},
		"root only":         {expr: "$"},
		"root index":        {expr: "$[2]"},
		"quoted key":        {expr: "$.'kubernetes.io/name'"},
		"quoted key without reserved characters is unquoted": {
			expr: "$.'plain'",
			want: "$.plain",
		},
		"quoted key with escaped quote":     {expr: `$.'it\'s'`},
		"quoted key with escaped backslash": {expr: `$.'a\\b.c'`},
		"backslash before another character is dropped": {
			expr: `$.'C:\docs'`,
			want: `$.'C:docs'`,
		},
		"escaped line feed":                     {expr: `$.'a\nb'`},
		"escaped tab and carriage return":       {expr: `$.'a\tb\rc'`},
		"unicode escape of a control character": {expr: `$.'a\u001bb'`},
		"unicode escape in upper case": {
			expr: `$.'a\u001Bb'`,
			want: `$.'a\u001bb'`,
		},
		"unicode escape of a plain character": {
			expr: `$.'\u0041bc'`,
			want: "$.Abc",
		},
		"raw line feed in quotes": {
			expr: "$.'a\nb'",
			want: `$.'a\nb'`,
		},
		"escaped backslash before n": {
			expr: `$.'a\\nb'`,
			want: `$.a\nb`,
		},
		"quoted key followed by index":         {expr: "$.'a.b'[1].c"},
		"empty quoted key":                     {expr: "$.''"},
		"empty quoted recursive key":           {expr: "$..''[0]"},
		"key selector":                         {expr: "$.spec.name~"},
		"key selector on the root":             {expr: "$~"},
		"key selector after an index":          {expr: "$.items[0]~"},
		"mapping wildcard":                     {expr: "$.jobs.*"},
		"mapping wildcard on the root":         {expr: "$.*"},
		"mapping wildcard then child":          {expr: "$.jobs.*.steps[*].uses"},
		"mapping wildcard then index":          {expr: "$.*[0]"},
		"mapping wildcard then key selector":   {expr: "$.jobs.*~"},
		"quoted star is a name":                {expr: "$.'*'"},
		"recursive wildcard":                   {expr: "$..*"},
		"recursive wildcard then child":        {expr: "$.spec..*.name"},
		"recursive wildcard then index":        {expr: "$..*[0]"},
		"recursive wildcard then key selector": {expr: "$..*~"},
		"recursive wildcard twice":             {expr: "$..*..*"},
		"quoted recursive star is a name":      {expr: "$..'*'"},
		"current node":                         {expr: "@"},
		"relative child":                       {expr: "@.foo.bar"},
		"relative index":                       {expr: "@[2]"},
		"relative key":                         {expr: "@~"},
		"relative wildcards":                   {expr: "@.jobs.*..name[*]"},
		"at sign in a name":                    {expr: "$.@ref"},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			p, err := paths.Parse(tc.expr)

			require.NoError(t, err)
			assert.Equal(t, cmp.Or(tc.want, tc.expr), p.String())
			assert.Equal(t, tc.expr[0] == '$', p.IsAbsolute())
		})
	}
}

func TestParse_Invalid(t *testing.T) {
	t.Parallel()

	// A case that sets want pins the whole message of the error.
	tcs := map[string]struct {
		expr string
		want string
	}{
		"empty":                      {expr: ""},
		"no anchor":                  {expr: "foo"},
		"anchor after a selector":    {expr: ".foo$"},
		"two anchors":                {expr: "$@"},
		"two anchors the other way":  {expr: "@$"},
		"trailing dot after current": {expr: "@."},
		"bare text after current": {
			expr: "@foo",
			want: `parse path "@foo": invalid path: unexpected 'f' at 1`,
		},
		"leading dot without root": {
			expr: ".foo",
			want: `parse path ".foo": invalid path: expression must start with $ or @`,
		},
		"trailing dot":        {expr: "$."},
		"unclosed bracket":    {expr: "$["},
		"empty index":         {expr: "$[]"},
		"negative index":      {expr: "$[-1]"},
		"negative zero index": {expr: "$[-0]"},
		"index with a leading zero": {
			expr: "$[01]",
			want: `parse path "$[01]": invalid path: index "01": not a canonical non-negative integer`,
		},
		"index with a plus sign": {expr: "$[+1]"},
		"non-numeric index":      {expr: "$[a]"},
		"index overflows int": {
			expr: "$[99999999999999999999]",
			want: `parse path "$[99999999999999999999]": invalid path: ` +
				`index "99999999999999999999": out of range`,
		},
		"star before a name": {
			expr: "$.*a",
			want: `parse path "$.*a": invalid path: unexpected '*' in selector "*a"`,
		},
		"star after a name": {expr: "$.a*"},
		"two stars":         {expr: "$.**"},
		"recursive star before a name": {
			expr: "$..*a",
			want: `parse path "$..*a": invalid path: unexpected '*' in selector "*a"`,
		},
		"recursive star after a name":         {expr: "$..a*"},
		"two recursive stars":                 {expr: "$..**"},
		"recursive star before a quoted name": {expr: "$..*'a'"},
		"star before a quoted name":           {expr: "$.*'a'"},
		"unterminated quote":                  {expr: "$.'foo"},
		"unterminated escape":                 {expr: `$.'a\`},
		"unicode escape with too few digits": {
			expr: `$.'a\u00'`,
			want: `parse path "$.'a\\u00'": invalid path: ` +
				`\u escape in quoted selector needs four hexadecimal digits`,
		},
		"unicode escape at the end":                           {expr: `$.'a\u`},
		"unicode escape with a digit that is not hexadecimal": {expr: `$.'a\u00zz'`},
		"unicode escape of a surrogate half": {
			expr: `$.'a\ud800'`,
			want: `parse path "$.'a\\ud800'": invalid path: ` +
				`\ud800 in quoted selector is a surrogate half, which names no character`,
		},
		"bare text after root": {
			expr: "$foo",
			want: `parse path "$foo": invalid path: unexpected 'f' at 1`,
		},
		"bare text after key selector": {expr: "$.a~b"},
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
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			p, err := paths.Parse(tc.expr)

			require.ErrorIs(t, err, paths.ErrInvalidPath)
			assert.Equal(t, paths.Path{}, p)
			assert.Contains(t, err.Error(), "parse path")

			if tc.want != "" {
				require.EqualError(t, err, tc.want)
			}
		})
	}
}

func TestParse_RoundTrip(t *testing.T) {
	t.Parallel()

	tcs := map[string]paths.Path{
		"root":             paths.Doc(),
		"children":         paths.Doc().Child("a", "b"),
		"index":            paths.Doc().Child("items").Index(3),
		"wildcards":        paths.Doc().Child("items").IndexAll().Recursive("name"),
		"dotted name":      paths.Doc().Child("kubernetes.io/name"),
		"quote in name":    paths.Doc().Child("it's"),
		"backslash":        paths.Doc().Child(`a\b.c`),
		"brackets":         paths.Doc().Child("a[0]"),
		"dollar":           paths.Doc().Child("$ref"),
		"star":             paths.Doc().Child("*"),
		"numeric name":     paths.Doc().Child("1"),
		"space in name":    paths.Doc().Child("has space"),
		"colon in name":    paths.Doc().Child("x: y"),
		"trailing space":   paths.Doc().Child("name "),
		"line break":       paths.Doc().Child("a\nb"),
		"tab":              paths.Doc().Child("a\tb"),
		"carriage return":  paths.Doc().Child("a\rb"),
		"escape character": paths.Doc().Child("a\x1b[31mb"),
		"delete":           paths.Doc().Child("a\x7fb"),
		"C1 control":       paths.Doc().Child("a\u0085b"),
		"line separator":   paths.Doc().Child("a\u2028b\u2029c"),
		"backslash and n":  paths.Doc().Child(`a\nb`),
		"only reserved":    paths.Doc().Child("."),
		"tilde in name":    paths.Doc().Child("a~b"),
		"goccy compatible": paths.Doc().Child("a.b").Index(1).Child("c"),
		"current":          paths.Current(),
		"relative":         paths.Current().Child("items").Index(3).Child("a.b"),
		"at sign in name":  paths.Doc().Child("@"),
	}

	for name, want := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, err := paths.Parse(want.String())
			require.NoError(t, err)

			assert.Equal(t, want, got)

			// The goccy parser accepts the same expression, with `$` in
			// place of `@`.
			expr := want.String()
			if !want.IsAbsolute() {
				expr = "$" + expr[1:]
			}

			_, err = yaml.PathString(expr)
			require.NoError(t, err)
		})
	}
}

func TestParse_RoundTrip_Key(t *testing.T) {
	t.Parallel()

	// The goccy parser has no key selector, so a key path stays out of the
	// table above, but Parse reads back what String writes.
	tcs := map[string]paths.Path{
		"key":               paths.Doc().Child("a").Key(),
		"key of root":       paths.Doc().Key(),
		"key of element":    paths.Doc().Child("items").Index(0).Key(),
		"key then child":    paths.Doc().Child("a").Key().Child("b"),
		"quoted name key":   paths.Doc().Child("a.b").Key(),
		"recursive key":     paths.Doc().Recursive("name").Key(),
		"empty name key":    paths.Doc().Child("").Key(),
		"tilde in name key": paths.Doc().Child("a~b").Key(),
		"key of current":    paths.Current().Key(),
		"relative key":      paths.Current().Child("a").Key().Child("b"),
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
	doc := yamltest.FirstDocument(t, "'': v\n").DocumentAST()

	tcs := map[string]paths.Path{
		"child":     paths.Doc().Child(""),
		"recursive": paths.Doc().Recursive(""),
	}

	for name, want := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, err := paths.Parse(want.String())
			require.NoError(t, err)
			assert.Equal(t, want, got)

			nodes, err := paths.NewResolver(doc).Nodes(got)
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

		assert.Equal(t, "$.foo.bar", paths.MustParse("$.foo.bar").String())
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

	doc := yamltest.FirstDocument(t, stringtest.Input(`
		a:
		  'b.c': [x, y]
		  don't: 1
		  a\.b: 2
		  a.b'c: 3
		'':
		  a'.b: 4
		  a'*b: 5
		  a.b: 6
	`)).DocumentAST()

	tcs := map[string]struct {
		path       paths.Path
		wantString string
		want       string
	}{
		"dotted name is quoted": {
			path:       paths.Doc().Child("a", "b.c").Index(1),
			wantString: "$.a.'b.c'[1]",
			want:       "y",
		},
		"name with a quote filters by its raw text": {
			path:       paths.Doc().Child("a", "don't"),
			wantString: "$.a.don't",
			want:       "1",
		},
		"name with a backslash filters by its raw text": {
			path:       paths.Doc().Child("a", `a\.b`),
			wantString: `$.a.'a\.b'`,
			want:       "2",
		},
		"dotted name with a quote filters by its raw text": {
			path:       paths.Doc().Child("a", "a.b'c"),
			wantString: `$.a.'a.b\'c'`,
			want:       "3",
		},
		"dotted name with a quote via builder fallback": {
			path:       paths.Doc().Child("", "a'.b"),
			wantString: "$..'a'.b'",
			want:       "4",
		},
		"star name with a quote via builder fallback": {
			path:       paths.Doc().Child("", "a'*b"),
			wantString: "$..'a'*b'",
			want:       "5",
		},
		"dotted name via builder fallback": {
			path:       paths.Doc().Child("", "a.b"),
			wantString: "$..'a.b'",
			want:       "6",
		},
		"relative path reads from the node FilterNode starts at": {
			path:       paths.Current().Child("a", "b.c").Index(0),
			wantString: "$.a.'b.c'[0]",
			want:       "x",
		},
		"relative path via builder fallback": {
			path:       paths.Current().Child("", "a.b"),
			wantString: "$..'a.b'",
			want:       "6",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.wantString, yamlPath(t, tc.path).String())
			assert.Equal(t, tc.want, goccyValue(t, tc.path, doc))
		})
	}
}

// goccyValue returns the text of the node that the goccy/go-yaml form of
// path selects in doc, or "" when it selects none.
func goccyValue(t *testing.T, path paths.Path, doc *ast.DocumentNode) string {
	t.Helper()

	node, err := yamlPath(t, path).FilterNode(doc.Body)
	require.NoError(t, err)

	if node == nil {
		return ""
	}

	return node.GetToken().Value
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
			path: paths.Doc().Child("a.b"),
			want: "plain: 1\na.b: 9\n",
		},
		"star name": {
			src:  "plain: 1\na*b: 2\n",
			path: paths.Doc().Child("a*b"),
			want: "plain: 1\na*b: 9\n",
		},
		"nested dotted label": {
			src: stringtest.LinesLF(
				"metadata:",
				"  labels:",
				"    app.kubernetes.io/name: web",
			),
			path: paths.Doc().Child("metadata", "labels", "app.kubernetes.io/name"),
			want: stringtest.LinesLF(
				"metadata:",
				"  labels:",
				"    app.kubernetes.io/name: 9",
			),
		},
		"empty name via builder fallback": {
			src:  "\"\": v\n",
			path: paths.Doc().Child(""),
			want: "\"\": 9\n",
		},
		"dotted name with a quote via builder fallback matches no key": {
			src:  stringtest.LinesLF(`"":`, "  a'.b: v"),
			path: paths.Doc().Child("", "a'.b"),
			want: stringtest.LinesLF(`"":`, "  a'.b: v"),
		},
		"name that is not valid UTF-8 matches no key": {
			src:  string(utf8.RuneError) + ": v\n",
			path: paths.Doc().Child("\xff"),
			want: string(utf8.RuneError) + ": v\n",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			file, err := niceyaml.NewSourceFromString(tc.src).File()
			require.NoError(t, err)

			replacement := yamltest.FirstDocument(t, "9").DocumentAST().Body

			require.NoError(t, yamlPath(t, tc.path).ReplaceWithNode(file, replacement))
			assert.Equal(t, tc.want, file.String())
		})
	}
}

func TestPath_YAMLPath_Limits(t *testing.T) {
	t.Parallel()

	t.Run("goccy strips single quotes from a name", func(t *testing.T) {
		t.Parallel()

		doc := yamltest.FirstDocument(t, stringtest.LinesLF("x: plain", `"'x'": quoted`)).DocumentAST()

		assert.Equal(t, "plain", goccyValue(t, paths.Doc().Child("'x'"), doc))
	})

	t.Run("goccy drops a key selector mid-path", func(t *testing.T) {
		t.Parallel()

		doc := yamltest.FirstDocument(t, stringtest.LinesLF("a:", "  b: 1")).DocumentAST()
		path := paths.Doc().Child("a").Key().Child("b")

		assert.Equal(t, "1", goccyValue(t, path, doc))

		_, err := paths.NewResolver(doc).Node(path)
		require.ErrorIs(t, err, paths.ErrNotFound)
	})

	t.Run("goccy matches a decorated key by its indicator", func(t *testing.T) {
		t.Parallel()

		tcs := map[string]struct {
			input     string
			indicator string
		}{
			"anchored key": {
				input:     stringtest.LinesLF("m:", "  &a k: v"),
				indicator: "&",
			},
			"tagged key": {
				input:     stringtest.LinesLF("m:", "  !!str k: v"),
				indicator: "!!str",
			},
			"alias key": {
				input:     stringtest.LinesLF("b: &k k", "m:", "  *k : v"),
				indicator: "*",
			},
			"explicit key": {
				input:     stringtest.LinesLF("m:", "  ? k", "  : v"),
				indicator: "?",
			},
			"explicit block scalar key": {
				input:     stringtest.LinesLF("m:", "  ? |-", "    k", "  : v"),
				indicator: "?",
			},
			"block scalar key without ?": {
				input:     stringtest.LinesLF("m:", "  |-", "    k", "  : v"),
				indicator: "|-",
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				doc := yamltest.FirstDocument(t, tc.input).DocumentAST()
				byName := paths.Doc().Child("m", "k")

				node, err := paths.NewResolver(doc).Node(byName)
				require.NoError(t, err)
				assert.Equal(t, "v", node.GetToken().Value)
				assert.Empty(t, goccyValue(t, byName, doc))

				byIndicator := paths.Doc().Child("m", tc.indicator)

				_, err = paths.NewResolver(doc).Node(byIndicator)
				require.ErrorIs(t, err, paths.ErrNotFound)
				assert.Equal(t, "v", goccyValue(t, byIndicator, doc))
			})
		}
	})

	t.Run("goccy skips merged entries", func(t *testing.T) {
		t.Parallel()

		doc := yamltest.FirstDocument(t, stringtest.LinesLF("base: &b", "  k: v", "m:", "  <<: *b")).DocumentAST()
		path := paths.Doc().Child("m", "k")

		node, err := paths.NewResolver(doc).Node(path)
		require.NoError(t, err)
		assert.Equal(t, "v", node.GetToken().Value)
		assert.Empty(t, goccyValue(t, path, doc))
	})

	t.Run("goccy selects the first duplicate and replaces all", func(t *testing.T) {
		t.Parallel()

		input := stringtest.LinesLF("m:", "  k: first", "  k: last")

		file, err := niceyaml.NewSourceFromString(input, niceyaml.WithAllowDuplicateKeys(true)).File()
		require.NoError(t, err)

		path := paths.Doc().Child("m", "k")

		node, err := paths.NewResolver(file.Docs[0]).Node(path)
		require.NoError(t, err)
		assert.Equal(t, "last", node.GetToken().Value)
		assert.Equal(t, "first", goccyValue(t, path, file.Docs[0]))

		replacement := yamltest.FirstDocument(t, "new\n").DocumentAST().Body

		require.NoError(t, yamlPath(t, path).ReplaceWithNode(file, replacement))
		assert.Equal(t, stringtest.LinesLF("m:", "  k: new", "  k: new"), file.String())
	})
}

func TestPath_Token(t *testing.T) {
	t.Parallel()

	doc := yamltest.FirstDocument(t, stringtest.Input(`
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
	`)).DocumentAST()

	// Each path points at a string token.
	tcs := map[string]struct {
		path string
		want string
	}{
		"root value returns the first key":                         {path: "$", want: "name"},
		"root key returns the first key":                           {path: "$~", want: "name"},
		"mapping value target returns the entry key":               {path: "$.metadata", want: "metadata"},
		"mapping with a tagged first key returns the entry key":    {path: "$.tag_first", want: "tag_first"},
		"mapping with an anchored first key returns the entry key": {path: "$.anchor_first", want: "anchor_first"},
		"mapping with an explicit first key returns the entry key": {
			path: "$.explicit_first",
			want: "explicit_first",
		},
		"mapping key target returns the entry key":                   {path: "$.metadata~", want: "metadata"},
		"sequence value target returns the entry key":                {path: "$.items", want: "items"},
		"sequence key target returns the entry key":                  {path: "$.items~", want: "items"},
		"simple key target returns key token":                        {path: "$.name~", want: "name"},
		"simple value target returns value token":                    {path: "$.name", want: "test"},
		"nested key target":                                          {path: "$.metadata.labels.app~", want: "app"},
		"nested value target":                                        {path: "$.metadata.labels.app", want: "myapp"},
		"array element value target":                                 {path: "$.items[0]", want: "first"},
		"array element key target returns value (no parent mapping)": {path: "$.items[1]~", want: "second"},
		"nested array first element name":                            {path: "$.list[0].name", want: "first"},
		"nested array second element items first":                    {path: "$.list[1].items[0]", want: "c"},
		"deeply nested value": {
			path: "$.nested.deep.deeper.value",
			want: "found",
		},
		"deeply nested key": {
			path: "$.nested.deep.deeper.value~",
			want: "value",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			tk := resolveToken(t, doc, tc.path)
			assert.Equal(t, tc.want, tk.Value)
			assert.Equal(t, token.StringType, tk.Type)
		})
	}
}

func TestPath_Token_Collections(t *testing.T) {
	t.Parallel()

	items := stringtest.LinesLF("items:", "  - name: a", "  - name: b")

	// A mapping or a sequence points at the token that introduces it. Each
	// want holds the text of the token and where it starts, counted from
	// 1.
	tcs := map[string]struct {
		input string
		path  string
		want  string
	}{
		"block mapping at the root starts at its first key": {
			input: "a: 1\n",
			path:  "$",
			want:  "a@1:1",
		},
		"block mapping at the root with a tagged first key": {
			input: "!!str inner: 8\n",
			path:  "$",
			want:  "inner@1:7",
		},
		"block mapping at the root with an anchored first key": {
			input: "&ka inner: 9\n",
			path:  "$",
			want:  "inner@1:5",
		},
		"block mapping at the root with an explicit first key": {
			input: "? inner\n: 10\n",
			path:  "$",
			want:  "inner@1:3",
		},
		"block sequence at the root starts at its first element": {
			input: "- x\n- y\n",
			path:  "$",
			want:  "x@1:3",
		},
		"block sequence of mappings at the root": {
			input: "- k: v\n",
			path:  "$",
			want:  "k@1:3",
		},
		"flow mapping at the root": {
			input: "{k: v}\n",
			path:  "$",
			want:  "{@1:1",
		},
		"flow sequence at the root": {
			input: "[x, y]\n",
			path:  "$",
			want:  "[@1:1",
		},
		"key selector on a flow sequence at the root": {
			input: "[x, y]\n",
			path:  "$~",
			want:  "[@1:1",
		},
		"block mapping under a key": {
			input: stringtest.LinesLF("spec:", "  replicas: 3"),
			path:  "$.spec",
			want:  "spec@1:1",
		},
		"key of a block mapping": {
			input: stringtest.LinesLF("spec:", "  replicas: 3"),
			path:  "$.spec~",
			want:  "spec@1:1",
		},
		"flow mapping under a key": {
			input: "spec: {replicas: 3}\n",
			path:  "$.spec",
			want:  "spec@1:1",
		},
		"block sequence under a key": {
			input: stringtest.LinesLF("items:", "  - a"),
			path:  "$.items",
			want:  "items@1:1",
		},
		"flow sequence under a key": {
			input: "items: [a, b]\n",
			path:  "$.items",
			want:  "items@1:1",
		},
		"tagged mapping under a key": {
			input: stringtest.LinesLF("spec: !!map", "  k: v"),
			path:  "$.spec",
			want:  "spec@1:1",
		},
		"mapping under an explicit key": {
			input: "? spec\n: {k: v}\n",
			path:  "$.spec",
			want:  "spec@1:3",
		},
		"mapping under a nested key": {
			input: stringtest.LinesLF("a:", "  b:", "    k: v"),
			path:  "$.a.b",
			want:  "b@2:3",
		},
		"scalar under a key is its own token": {
			input: "a: v\n",
			path:  "$.a",
			want:  "v@1:4",
		},
		"block mapping element": {
			input: items,
			path:  "$.items[1]",
			want:  "-@3:3",
		},
		"key selector on a block mapping element": {
			input: items,
			path:  "$.items[1]~",
			want:  "-@3:3",
		},
		"first key of a block mapping element": {
			input: items,
			path:  "$.items[1].name~",
			want:  "name@3:5",
		},
		"block mapping element at the root": {
			input: "- k: v\n",
			path:  "$[0]",
			want:  "-@1:1",
		},
		"anchored block mapping element": {
			input: stringtest.LinesLF("items:", "  - &x", "    name: a"),
			path:  "$.items[0]",
			want:  "-@2:3",
		},
		"flow mapping element of a block sequence": {
			input: stringtest.LinesLF("items:", "  - {name: a}"),
			path:  "$.items[0]",
			want:  "-@2:3",
		},
		"block sequence element of a block sequence": {
			input: stringtest.LinesLF("- - x", "  - y"),
			path:  "$[0]",
			want:  "-@1:1",
		},
		"scalar in a nested block sequence": {
			input: stringtest.LinesLF("- - x", "  - y"),
			path:  "$[0][1]",
			want:  "y@2:5",
		},
		"first flow mapping element of a flow sequence": {
			input: "items: [{a: 1}, {b: 2}]\n",
			path:  "$.items[0]",
			want:  "{@1:9",
		},
		"later flow mapping element of a flow sequence": {
			input: "items: [{a: 1}, {b: 2}]\n",
			path:  "$.items[1]",
			want:  "{@1:17",
		},
		"flow sequence element of a flow sequence": {
			input: "m: [[x], [y]]\n",
			path:  "$.m[1]",
			want:  "[@1:10",
		},
		"alias to a mapping is its own token": {
			input: stringtest.LinesLF(
				"base: &b",
				"  sub:",
				"    k: v",
				"other: *b",
			),
			path: "$.other",
			want: "*@4:8",
		},
		"mapping through an alias points at its key in the anchor": {
			input: stringtest.LinesLF(
				"base: &b",
				"  sub:",
				"    k: v",
				"other: *b",
			),
			path: "$.other.sub",
			want: "sub@2:3",
		},
		"mapping a merge key brings in points at its key in the source": {
			input: stringtest.LinesLF(
				"base: &b",
				"  sub:",
				"    k: v",
				"m:",
				"  <<: *b",
			),
			path: "$.m.sub",
			want: "sub@2:3",
		},
		"mapping element through an aliased sequence": {
			input: stringtest.LinesLF("list: &l", "  - k: v", "copy: *l"),
			path:  "$.copy[0]",
			want:  "-@2:3",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			doc := yamltest.FirstDocument(t, tc.input).DocumentAST()

			tk, err := paths.NewResolver(doc).Token(paths.MustParse(tc.path))
			require.NoError(t, err)

			assert.Equal(t, tc.want, yamltest.TokenPlace(tk))
		})
	}
}

func TestPath_Token_NoDocument(t *testing.T) {
	t.Parallel()

	path := paths.Doc().Child("name")

	for name, p := range map[string]paths.Path{
		"Token": path,
		"Key":   path.Key(),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, err := paths.NewResolver(nil).Token(p)
			require.ErrorIs(t, err, paths.ErrNoDocument)
			require.ErrorIs(t, err, paths.ErrNotFound)

			_, err = paths.NewResolver(&ast.DocumentNode{}).Token(p)
			require.ErrorIs(t, err, paths.ErrNoDocument)
			require.ErrorIs(t, err, paths.ErrNotFound)
			assert.Contains(t, err.Error(), "$.name")
		})
	}
}

func TestPath_DirectiveDocument(t *testing.T) {
	t.Parallel()

	// The directive parses as a document of its own, ahead of the content.
	source := niceyaml.NewSourceFromString(stringtest.LinesLF("%YAML 1.2", "---", "key: v"))
	file, err := source.File()
	require.NoError(t, err)
	require.Len(t, file.Docs, 2)

	_, err = paths.NewResolver(file.Docs[0]).Node(paths.Doc())
	require.ErrorIs(t, err, paths.ErrNoDocument)
	require.ErrorIs(t, err, paths.ErrNotFound)

	_, err = paths.NewResolver(file.Docs[0]).Token(paths.Doc().Child("key"))
	require.ErrorIs(t, err, paths.ErrNoDocument)

	// The directive stands above the header, so its document holds no
	// header for the root path to select.
	nodes, err := paths.NewResolver(file.Docs[0]).Nodes(paths.Doc())
	require.NoError(t, err)
	assert.Empty(t, nodes)

	node, err := paths.NewResolver(file.Docs[1]).Node(paths.Doc().Child("key"))
	require.NoError(t, err)
	assert.Equal(t, "v", node.String())
}

func TestPath_NoContentDocument(t *testing.T) {
	t.Parallel()

	// No single node resolves in a document that holds no content, and a
	// path lists no nodes there.
	tcs := map[string]string{
		// A parse that keeps comments makes the comment group the body of
		// a document that holds nothing else.
		"comment": "# just a comment\n",
		// The tokenizer gives a source the lexer emits nothing for one
		// placeholder token, which the parser reads as a plain scalar.
		// That scalar is not content.
		"newline":     "\n",
		"blank lines": "\n\n",
		"spaces":      "   ",
		"lone bang":   "!",
	}

	for name, input := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			file, err := niceyaml.NewSourceFromString(input).File()
			require.NoError(t, err)
			require.Len(t, file.Docs, 1)

			_, err = paths.NewResolver(file.Docs[0]).Node(paths.Doc())
			require.ErrorIs(t, err, paths.ErrNoDocument)
			require.ErrorIs(t, err, paths.ErrNotFound)

			for _, path := range []paths.Path{paths.Doc(), paths.Doc().Child("a")} {
				_, err = paths.NewResolver(file.Docs[0]).Token(path)
				require.ErrorIs(t, err, paths.ErrNoDocument, path)
				require.ErrorIs(t, err, paths.ErrNotFound, path)
			}

			nodes, err := paths.NewResolver(file.Docs[0]).Nodes(paths.Doc())
			require.NoError(t, err)
			assert.Empty(t, nodes)
		})
	}
}

func TestPath_Token_MultipleDocuments(t *testing.T) {
	t.Parallel()

	source := niceyaml.NewSourceFromString(stringtest.LinesLF("name: first", "---", "name: second"))
	file, err := source.File()
	require.NoError(t, err)
	require.Len(t, file.Docs, 2)

	path := paths.Doc().Child("name")

	tk, err := paths.NewResolver(file.Docs[0]).Token(path)
	require.NoError(t, err)
	assert.Equal(t, "first", tk.Value)

	tk, err = paths.NewResolver(file.Docs[1]).Token(path)
	require.NoError(t, err)
	assert.Equal(t, "second", tk.Value)
}

func TestPath_Token_Anchors(t *testing.T) {
	t.Parallel()

	doc := yamltest.FirstDocument(t, stringtest.Input(`
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
	`)).DocumentAST()

	tcs := map[string]struct {
		path string
		want string
		line int
	}{
		"child of anchored mapping":                           {path: "$.base.a", want: "1", line: 2},
		"key of anchored mapping entry":                       {path: "$.base.a~", want: "a", line: 2},
		"anchored mapping value target is the entry key":      {path: "$.base", want: "base", line: 1},
		"anchored flow mapping value target is the entry key": {path: "$.flow", want: "flow", line: 4},
		"anchored sequence value target is the entry key":     {path: "$.list", want: "list", line: 6},
		"child through alias lands in the anchor":             {path: "$.other.b", want: "2", line: 3},
		"alias value target is the alias token":               {path: "$.other", want: "*", line: 5},
		"alias key target is its key":                         {path: "$.other~", want: "other", line: 5},
		"index through anchored sequence":                     {path: "$.list[1]", want: "two", line: 8},
		"index through aliased sequence":                      {path: "$.copy[0]", want: "one", line: 7},
		"tagged scalar value target skips the tag":            {path: "$.tagged", want: "5", line: 10},
		"merged key resolves to the anchor":                   {path: "$.merged.a", want: "1", line: 2},
		"merged key target resolves to the anchor key":        {path: "$.merged.a~", want: "a", line: 2},
		"own key after merge wins":                            {path: "$.merged.b", want: "20", line: 13},
		"merge after own key wins":                            {path: "$.late.b", want: "2", line: 3},
		"merge after own key wins at the key":                 {path: "$.late.b~", want: "b", line: 3},
		"own key before merge without that key":               {path: "$.late.c", want: "31", line: 24},
		"own key next to merge":                               {path: "$.merged.c", want: "3", line: 14},
		"merge sequence first source":                         {path: "$.multi.a", want: "1", line: 2},
		"merge sequence second source":                        {path: "$.multi.x", want: "10", line: 4},
		"merge sequence later source wins":                    {path: "$.multi.b", want: "200", line: 4},
		"merge of a merged mapping":                           {path: "$.chain.a", want: "1", line: 2},
		"merge of a merged mapping own key":                   {path: "$.chain.d", want: "4", line: 19},
		"merge key entry itself is addressable":               {path: "$.merged.<<~", want: "<<", line: 12},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			tk := resolveToken(t, doc, tc.path)
			assert.Equal(t, tc.want, tk.Value)
			assert.Equal(t, tc.line, tk.Position.Line)
		})
	}

	t.Run("merged key not present", func(t *testing.T) {
		t.Parallel()

		_, err := paths.NewResolver(doc).Token(paths.Doc().Child("merged", "zzz"))
		require.ErrorIs(t, err, paths.ErrNotFound)
	})
}

func TestPath_Node_Anchors(t *testing.T) {
	t.Parallel()

	doc := yamltest.FirstDocument(t, stringtest.LinesLF(
		"base: &b {a: 1}",
		"other: *b",
		"tagged: !!str 5",
	)).DocumentAST()

	tcs := map[string]struct {
		want ast.Node
		key  string
	}{
		"anchor is looked through": {key: "base", want: &ast.MappingNode{}},
		"alias is looked through":  {key: "other", want: &ast.MappingNode{}},
		"tag is kept":              {key: "tagged", want: &ast.TagNode{}},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			node, err := paths.NewResolver(doc).Node(paths.Doc().Child(tc.key))
			require.NoError(t, err)
			assert.IsType(t, tc.want, node)
		})
	}
}

func TestPath_Node_TaggedKey(t *testing.T) {
	t.Parallel()

	// A `~` keeps a tag on the key, as a path to a value keeps a tag on
	// the value, while the token of the key is still the key text.
	tcs := map[string]struct {
		input string
		want  string
	}{
		"plain key":           {input: "2: x\n", want: "2"},
		"tagged key":          {input: "!!str 2: x\n", want: "!!str 2"},
		"explicit tagged key": {input: "? !!str 2\n: x\n", want: "!!str 2"},
		"anchored tagged key": {input: "&k !!str 2: x\n", want: "!!str 2"},
		"anchored key":        {input: "&k 2: x\n", want: "2"},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			doc := yamltest.FirstDocument(t, tc.input).DocumentAST()
			path := paths.Doc().Child("2").Key()

			node, err := paths.NewResolver(doc).Node(path)
			require.NoError(t, err)
			assert.Equal(t, tc.want, node.String())

			nodes, err := paths.NewResolver(doc).Nodes(path)
			require.NoError(t, err)
			require.Len(t, nodes, 1)
			assert.Same(t, node, nodes[0])

			tk, err := paths.NewResolver(doc).Token(path)
			require.NoError(t, err)
			assert.Equal(t, "2", tk.Value)
		})
	}
}

func TestPath_UnknownAlias(t *testing.T) {
	t.Parallel()

	// An alias refers to the last anchor of its name before it. With no
	// such anchor, Node looks through the alias and reports that it names
	// none, as a path below the alias does, while Token returns the
	// alias's own token.
	tcs := map[string]struct {
		input string
		key   string
		want  string
	}{
		"no anchor of its name": {
			input: stringtest.LinesLF("base: &b {a: 1}", "other: *nope"),
			key:   "other",
			want:  "*nope has no anchor before it",
		},
		"anchor after the alias": {
			input: stringtest.LinesLF("b: *x", "a: &x", "  v: 1"),
			key:   "b",
			want:  "*x has no anchor before it",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			doc := yamltest.FirstDocument(t, tc.input).DocumentAST()
			alias := paths.Doc().Child(tc.key)

			_, err := paths.NewResolver(doc).Node(alias)
			require.ErrorIs(t, err, paths.ErrAlias)

			_, err = paths.NewResolver(doc).Token(alias.Child("v"))
			require.ErrorIs(t, err, paths.ErrAlias)
			require.NotErrorIs(t, err, paths.ErrNotFound)
			assert.Contains(t, err.Error(), tc.want)

			tk, err := paths.NewResolver(doc).Token(alias)
			require.NoError(t, err)
			assert.Equal(t, token.AliasType, tk.Type)
		})
	}
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
			path:  paths.Doc().Child("b"),
			want:  "!t *x",
		},
		"alias with no anchor before it": {
			input: "b: !t *nope\n",
			path:  paths.Doc().Child("b"),
			err:   paths.ErrAlias,
		},
		"alias inside its own anchor": {
			input: "a: &x [!t *x]\n",
			path:  paths.Doc().Child("a").Index(0),
			err:   paths.ErrAlias,
		},
		"anchor over a tag on an alias to itself": {
			input: "a: &x !t *x\n",
			path:  paths.Doc().Child("a"),
			err:   paths.ErrAlias,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assertNodeText(t, yamltest.FirstDocument(t, tc.input), tc.path, tc.want, tc.err)
		})
	}
}

// assertNodeText asserts that path selects the node with the text want in
// the document of dd, or that it fails with wantErr when wantErr is set.
// Node, Nodes, and [niceyaml.Node.At] must agree on which.
func assertNodeText(t *testing.T, dd *niceyaml.Node, path paths.Path, want string, wantErr error) {
	t.Helper()

	doc := dd.DocumentAST()

	node, err := paths.NewResolver(doc).Node(path)
	_, nodesErr := paths.NewResolver(doc).Nodes(path)
	_, atErr := dd.At(path)

	if wantErr != nil {
		require.ErrorIs(t, err, wantErr)
		require.ErrorIs(t, nodesErr, wantErr)
		require.ErrorIs(t, atErr, wantErr)

		return
	}

	require.NoError(t, err)
	require.NoError(t, nodesErr)
	require.NoError(t, atErr)
	assert.Equal(t, want, node.String())
}

func TestPath_UnnamedKey(t *testing.T) {
	t.Parallel()

	// A key with no name has no text a selector can match, so neither a
	// `.''` nor a `..name` selector reaches its entry, and the entry never
	// hides a real empty key. The decoder never reads such a key as the
	// empty key. A lookup that passes the entry on the way to another key
	// still resolves.
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
		"name of an alias key with no anchor": {
			input:     "b: 2\n*nope : 1\n",
			path:      "$.nope",
			err:       paths.ErrNotFound,
			recursive: "$..nope",
		},
		"indicator of an alias key with no anchor": {
			input:     "b: 2\n*nope : 1\n",
			path:      "$.'*'",
			err:       paths.ErrNotFound,
			recursive: "$..'*'",
		},
		"key beside an alias key with no anchor": {
			input:     "b: 2\n*nope : 1\n",
			path:      "$.b",
			want:      "2",
			recursive: "$..b",
			matches:   []string{"$.b=2"},
		},
		"alias key inside its own anchor": {
			input:     stringtest.LinesLF("x: &x", "  *x : v"),
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
			input:     stringtest.LinesLF("*nope : {name: 1}", "b: {name: 2}"),
			path:      "$.''.name",
			err:       paths.ErrNotFound,
			recursive: "$..name",
			matches:   []string{"$.b.name=2"},
		},
		"real empty key before an alias key with no name": {
			// The decoder keeps both entries, and reads the alias key
			// as null.
			opts: []niceyaml.SourceOption{niceyaml.WithAllowDuplicateKeys(true)},
			input: stringtest.LinesLF(
				"x: &x",
				`  "": {name: 1}`,
				"  *x : {name: 2}",
			),
			path:      "$.x.''",
			want:      "{name: 1}",
			recursive: "$..name",
			matches:   []string{"$.x.''.name=1"},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			doc := yamltest.FirstDocument(t, tc.input, tc.opts...).DocumentAST()

			node, err := paths.NewResolver(doc).Node(paths.MustParse(tc.path))
			if tc.err != nil {
				require.ErrorIs(t, err, tc.err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tc.want, node.String())
			}

			matches := requireMatches(t, doc, tc.recursive)
			assert.Equal(t, tc.matches, matchValues(matches))
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
			path:  paths.Doc().Child("a", "c"),
			want:  "*x forms a cycle",
		},
		"merge key through an alias inside its own anchor": {
			input: stringtest.LinesLF("a: &x !t *x", "m:", "  <<: *x"),
			path:  paths.Doc().Child("m", "c"),
			want:  "*x forms a cycle",
		},
		"anchors whose tags alias each other": {
			// The *y on line 1 comes before &y, so it names no anchor and
			// resolution stops there.
			input: "a: &x !t *y\nb: &y !t *x\n",
			path:  paths.Doc().Child("b", "c"),
			want:  "*y has no anchor before it",
		},
		"alias inside its own mapping anchor": {
			input: "b: &x {s: *x, t: 1}\n",
			path:  paths.Doc().Child("b", "s", "t"),
			want:  "*x forms a cycle",
		},
		"alias inside its own sequence anchor": {
			input: "a: &a [1, *a]\n",
			path:  paths.Doc().Child("a").Index(1).Index(0),
			want:  "*a forms a cycle",
		},
		"alias inside its own anchor on a merge value": {
			input: "x: {<<: &m {k: *m, j: 1}}\n",
			path:  paths.Doc().Child("x", "k", "j"),
			want:  "*m forms a cycle",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			doc := yamltest.FirstDocument(t, tc.input).DocumentAST()

			// Resolve in a goroutine so a loop that never returns fails this
			// test instead of stalling the run.
			errs := make(chan error, 3)

			go func() {
				_, err := paths.NewResolver(doc).Token(tc.path)
				errs <- err

				_, err = paths.NewResolver(doc).Node(tc.path)
				errs <- err

				_, err = paths.NewResolver(doc).Nodes(tc.path)
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
			path:  paths.Doc().Child("b", "s"),
			err:   paths.ErrAlias,
		},
		"sequence": {
			input: "a: &a [1, *a]\n",
			path:  paths.Doc().Child("a").Index(1),
			err:   paths.ErrAlias,
		},
		"anchor on a merge value": {
			input: "x: {<<: &m {k: *m, j: 1}}\n",
			path:  paths.Doc().Child("x", "k"),
			err:   paths.ErrAlias,
		},
		"alias a merge key names inside its own anchor": {
			// The decode leaves an alias that a `<<` merge key names as it
			// is, so a path through the merge key reaches the content of
			// the anchor.
			input: "a: &a {k: 1, i: {<<: *a}}\n",
			path:  paths.Doc().Child("a", "i", "<<"),
			want:  "{k: 1, i: {<<: *a}}",
		},
		"alias after an anchor of the same name inside the content": {
			// The inner &x is the last anchor of its name before *x, and *x
			// lies outside the content of that anchor.
			input: "b: &x {a: &x 1, s: *x}\n",
			path:  paths.Doc().Child("b", "s"),
			want:  "1",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			dd := yamltest.FirstDocument(t, tc.input)

			tk, err := paths.NewResolver(dd.DocumentAST()).Token(tc.path)
			require.NoError(t, err)
			assert.Equal(t, "*", tk.Value)

			assertNodeText(t, dd, tc.path, tc.want, tc.err)
		})
	}
}

func TestPath_HandBuiltAST(t *testing.T) {
	t.Parallel()

	// The parser never produces these shapes, but Node and Token accept any
	// *ast.DocumentNode, so a mutated tree must error rather than panic.
	t.Run("alias without a name", func(t *testing.T) {
		t.Parallel()

		doc := yamltest.FirstDocument(t, "a: 1\n").DocumentAST()

		mapping, ok := doc.Body.(*ast.MappingNode)
		require.True(t, ok, "want *ast.MappingNode, got %T", doc.Body)

		mapping.Values[0].Value = &ast.AliasNode{}

		_, err := paths.NewResolver(doc).Node(paths.Doc().Child("a"))
		require.ErrorIs(t, err, paths.ErrAlias)
		assert.Contains(t, err.Error(), "alias has no name")

		_, err = paths.NewResolver(doc).Token(paths.Doc().Child("a", "b"))
		require.ErrorIs(t, err, paths.ErrAlias)
	})

	t.Run("entry without a key", func(t *testing.T) {
		t.Parallel()

		doc := yamltest.FirstDocument(t, "a: 1\n").DocumentAST()

		mapping, ok := doc.Body.(*ast.MappingNode)
		require.True(t, ok, "want *ast.MappingNode, got %T", doc.Body)

		mapping.Values[0].Key = nil

		tk, err := paths.NewResolver(doc).Token(paths.Doc())
		require.NoError(t, err)
		assert.Equal(t, mapping.Values[0].GetToken(), tk)

		// An entry without a key has no name, so not even the empty
		// name selects it.
		_, err = paths.NewResolver(doc).Token(paths.Doc().Child("").Key())
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

// strNode builds a string node with the text value and no token.
func strNode(value string) *ast.StringNode {
	return &ast.StringNode{Value: value}
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
			path: paths.Doc().Child("a"),
			err:  paths.ErrNotFound,
		},
		"nil entry beside a real one": {
			body: mapNode(nil, mapEntry(strNode("a"), strNode("1"))),
			path: paths.Doc().Child("b"),
			err:  paths.ErrNotFound,
		},
		"key without a token": {
			body: mapNode(mapEntry(&ast.IntegerNode{}, strNode("1"))),
			path: paths.Doc().Child("a"),
			err:  paths.ErrNotFound,
		},
		"alias name holding a typed nil": {
			body: mapNode(mapEntry(strNode("a"), &ast.AliasNode{Value: (*ast.StringNode)(nil)})),
			path: paths.Doc().Child("a"),
			err:  paths.ErrAlias,
		},
		"anchor name without a token": {
			body: mapNode(mapEntry(strNode("a"), &ast.AnchorNode{Name: &ast.StringNode{}, Value: strNode("1")})),
			path: paths.Doc().Child("a", "b"),
			err:  paths.ErrNotFound,
		},
		"entry without a value": {
			body: mapNode(mapEntry(strNode("a"), nil)),
			path: paths.Doc().Child("a", "b"),
			err:  paths.ErrNotFound,
		},
		"path ending at an entry without a value": {
			body: mapNode(mapEntry(strNode("a"), nil)),
			path: paths.Doc().Child("a"),
			err:  paths.ErrNotFound,
		},
		"key holding a typed nil": {
			body: mapNode(mapEntry((*ast.StringNode)(nil), strNode("1"))),
			path: paths.Doc().Child("a"),
			err:  paths.ErrNotFound,
		},
		"key holding a typed nil anchor": {
			body: mapNode(mapEntry((*ast.AnchorNode)(nil), strNode("1"))),
			path: paths.Doc().Child("a"),
			err:  paths.ErrNotFound,
		},
		"key holding a typed nil tag": {
			body: mapNode(mapEntry((*ast.TagNode)(nil), strNode("1"))),
			path: paths.Doc().Child("a"),
			err:  paths.ErrNotFound,
		},
		"key holding a typed nil explicit key": {
			body: mapNode(mapEntry((*ast.MappingKeyNode)(nil), strNode("1"))),
			path: paths.Doc().Child("a"),
			err:  paths.ErrNotFound,
		},
		"explicit key wrapping a typed nil anchor": {
			body: mapNode(mapEntry(&ast.MappingKeyNode{Value: (*ast.AnchorNode)(nil)}, strNode("1"))),
			path: paths.Doc().Child("a"),
			err:  paths.ErrNotFound,
		},
		"child of a typed nil mapping": {
			body: mapNode(mapEntry(strNode("a"), (*ast.MappingNode)(nil))),
			path: paths.Doc().Child("a", "b"),
			err:  paths.ErrNotFound,
		},
		"index of a typed nil sequence": {
			body: mapNode(mapEntry(strNode("a"), (*ast.SequenceNode)(nil))),
			path: paths.Doc().Child("a").Index(0),
			err:  paths.ErrNotFound,
		},
		"path ending at a typed nil anchor": {
			body: mapNode(mapEntry(strNode("a"), (*ast.AnchorNode)(nil))),
			path: paths.Doc().Child("a"),
			err:  paths.ErrNotFound,
		},
		"child of a typed nil anchor": {
			body: mapNode(mapEntry(strNode("a"), (*ast.AnchorNode)(nil))),
			path: paths.Doc().Child("a", "b"),
			err:  paths.ErrNotFound,
		},
		"child of a typed nil tag": {
			body: mapNode(mapEntry(strNode("a"), (*ast.TagNode)(nil))),
			path: paths.Doc().Child("a", "b"),
			err:  paths.ErrNotFound,
		},
		"merge of a typed nil mapping": {
			body: mapNode(mapEntry(
				&ast.MergeKeyNode{},
				&ast.SequenceNode{BaseNode: &ast.BaseNode{}, Values: []ast.Node{(*ast.MappingNode)(nil)}},
			)),
			path: paths.Doc().Child("b"),
			err:  paths.ErrNotFound,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			doc := &ast.DocumentNode{Body: tc.body}

			_, err := paths.NewResolver(doc).Node(tc.path)
			require.ErrorIs(t, err, tc.err)
		})
	}
}

func TestPath_Matches_HandBuiltTree(t *testing.T) {
	t.Parallel()

	shared := strNode("1")
	sharedMap := mapNode(mapEntry(strNode("x"), shared))
	sharedSeq := &ast.SequenceNode{Values: []ast.Node{shared}}

	tcs := map[string]struct {
		body ast.Node
		path string
		want []string
	}{
		"recursive keys of entries without a value": {
			body: mapNode(
				mapEntry(strNode("a"), nil),
				mapEntry(strNode("b"), mapNode(
					mapEntry(strNode("a"), nil),
				)),
			),
			path: "$..a~",
			want: []string{"$.a~", "$.b.a~"},
		},
		"entries sharing a value node": {
			body: mapNode(
				mapEntry(strNode("c"), mapNode(mapEntry(strNode("x"), shared))),
				mapEntry(strNode("d"), mapNode(mapEntry(strNode("x"), shared))),
			),
			path: "$..x",
			want: []string{"$.c.x", "$.d.x"},
		},
		"keys of entries sharing a value node": {
			body: mapNode(
				mapEntry(strNode("c"), mapNode(mapEntry(strNode("x"), shared))),
				mapEntry(strNode("d"), mapNode(mapEntry(strNode("x"), shared))),
			),
			path: "$..x~",
			want: []string{"$.c.x~", "$.d.x~"},
		},
		"every entry sharing a value node": {
			body: mapNode(
				mapEntry(strNode("c"), shared),
				mapEntry(strNode("d"), shared),
			),
			path: "$..*",
			want: []string{"$.c", "$.d"},
		},
		"elements sharing a value node": {
			body: mapNode(
				mapEntry(strNode("s"), &ast.SequenceNode{Values: []ast.Node{shared, shared}}),
			),
			path: "$..*",
			want: []string{"$.s", "$.s[0]", "$.s[1]"},
		},
		// A walk lists each entry and each element of a shared node once,
		// at the first place it reaches them.
		"entries of a mapping two entries share": {
			body: mapNode(
				mapEntry(strNode("c"), sharedMap),
				mapEntry(strNode("d"), sharedMap),
			),
			path: "$..*",
			want: []string{"$.c", "$.c.x", "$.d"},
		},
		"elements of a sequence two entries share": {
			body: mapNode(
				mapEntry(strNode("c"), sharedSeq),
				mapEntry(strNode("d"), sharedSeq),
			),
			path: "$..*",
			want: []string{"$.c", "$.c[0]", "$.d"},
		},
		// A `..name` selector passes the entries it cannot name.
		"recursive name past nil entries and keys with no name": {
			body: mapNode(
				nil,
				mapEntry(&ast.IntegerNode{}, mapNode(
					nil,
					mapEntry(&ast.IntegerNode{}, strNode("1")),
				)),
			),
			path: "$..b",
		},
		"nil entry and keys with no name": {
			body: mapNode(
				nil,
				mapEntry(nil, strNode("1")),
				mapEntry(&ast.IntegerNode{}, strNode("2")),
				mapEntry((*ast.StringNode)(nil), strNode("3")),
				mapEntry(&ast.MappingKeyNode{Value: (*ast.AnchorNode)(nil)}, strNode("4")),
				mapEntry(strNode("a"), strNode("5")),
			),
			path: "$.*",
			want: []string{"$.a"},
		},
		"entries without a value hold no node to list": {
			body: mapNode(
				mapEntry(strNode("a"), nil),
				mapEntry(strNode("b"), strNode("1")),
			),
			path: "$.*",
			want: []string{"$.b"},
		},
		"keys of every entry without a value": {
			body: mapNode(
				mapEntry(strNode("a"), nil),
				mapEntry(strNode("b"), nil),
			),
			path: "$.*~",
			want: []string{"$.a~", "$.b~"},
		},
		"merge of a typed nil mapping": {
			body: mapNode(
				mapEntry(
					&ast.MergeKeyNode{},
					&ast.SequenceNode{BaseNode: &ast.BaseNode{}, Values: []ast.Node{(*ast.MappingNode)(nil)}},
				),
				mapEntry(strNode("a"), strNode("1")),
			),
			path: "$.*",
			want: []string{"$.a"},
		},
		"merge key without a value": {
			body: mapNode(
				mapEntry(&ast.MergeKeyNode{}, nil),
				mapEntry(strNode("a"), strNode("1")),
			),
			path: "$.*",
			want: []string{"$.a"},
		},
		"merge source with a nil entry": {
			body: mapNode(
				mapEntry(&ast.MergeKeyNode{}, mapNode(
					nil,
					mapEntry(strNode("b"), strNode("1")),
				)),
				mapEntry(strNode("a"), strNode("2")),
			),
			path: "$.*",
			want: []string{"$.b", "$.a"},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			doc := &ast.DocumentNode{Body: tc.body}
			path := paths.MustParse(tc.path)

			matches, err := paths.NewResolver(doc).Matches(path)
			require.NoError(t, err)
			assert.Equal(t, tc.want, matchPaths(matches))

			nodes, err := paths.NewResolver(doc).Nodes(path)
			require.NoError(t, err)
			assert.Len(t, nodes, len(tc.want))
		})
	}
}

func TestPath_Nodes_HandBuiltTreeTypedNil(t *testing.T) {
	t.Parallel()

	doc := &ast.DocumentNode{Body: mapNode(
		mapEntry(strNode("m"), (*ast.MappingNode)(nil)),
		mapEntry(strNode("s"), (*ast.SequenceNode)(nil)),
		mapEntry(strNode("a"), (*ast.AnchorNode)(nil)),
		mapEntry(strNode("t"), (*ast.TagNode)(nil)),
	)}

	tcs := map[string]paths.Path{
		"every element of a typed nil sequence": paths.Doc().Child("s").IndexAll(),
		"every entry of a typed nil mapping":    paths.Doc().Child("m").ChildAll(),
		"every entry of each typed nil":         paths.Doc().ChildAll().ChildAll(),
		"recursive through typed nils":          paths.Doc().Recursive("q"),
		"every node below typed nils":           paths.Doc().RecursiveAll(),
	}

	for name, path := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			nodes, err := paths.NewResolver(doc).Nodes(path)
			require.NoError(t, err)
			assert.Empty(t, nodes)
		})
	}
}

func TestPath_RedefinedAnchor(t *testing.T) {
	t.Parallel()

	// An alias refers to the last anchor of its name before it, so the *x on
	// line 3 reaches `v: 1` and the *x on line 6 reaches `v: 2`.
	doc := yamltest.FirstDocument(t, stringtest.LinesLF(
		"a: &x",
		"  v: 1",
		"b: *x",
		"c: &x",
		"  v: 2",
		"d: *x",
	)).DocumentAST()

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

			tk, err := paths.NewResolver(doc).Token(paths.Doc().Child(tc.key, "v"))
			require.NoError(t, err)
			assert.Equal(t, tc.wantValue, tk.Value)
			assert.Equal(t, tc.wantLine, tk.Position.Line)

			node, err := paths.NewResolver(doc).Node(paths.Doc().Child(tc.key))
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

		dd := yamltest.FirstDocument(t, stringtest.LinesLF("a: &x v1", "b: *x", "c: &x v2", "d: *x"))

		var decoded map[string]any

		require.NoError(t, dd.DecodeInto(t.Context(), &decoded))
		assert.Equal(t, map[string]any{"a": "v1", "b": "v1", "c": "v2", "d": "v2"}, decoded)

		for _, key := range []string{"b", "d"} {
			got, err := yamltest.At(t, dd, paths.Doc().Child(key)).Decode[string](t.Context())
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
				input: stringtest.LinesLF(
					"base: &b {k: &x {j: &x 1}}",
					"m: {<<: *b}",
					"v: *x",
				),
				key:  "v",
				want: "{j: &x 1}",
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				dd := yamltest.FirstDocument(t, tc.input)

				node, err := paths.NewResolver(dd.DocumentAST()).Node(paths.Doc().Child(tc.key))
				require.NoError(t, err)
				assert.Equal(t, tc.want, node.String())

				var decoded map[string]any

				require.NoError(t, yaml.Unmarshal([]byte(tc.input), &decoded))

				got, err := yamltest.At(t, dd, paths.Doc().Child(tc.key)).Decode[any](t.Context())
				require.NoError(t, err)
				assert.Equal(t, decoded[tc.key], got)
			})
		}
	})
}

func TestPath_Token_NotFound(t *testing.T) {
	t.Parallel()

	doc := yamltest.FirstDocument(t, "name: test\nitems: [a, b]\n").DocumentAST()

	tcs := map[string]paths.Path{
		"missing key":              paths.Doc().Child("nope"),
		"child of scalar":          paths.Doc().Child("name", "x"),
		"index of mapping":         paths.Doc().Index(0),
		"index of scalar":          paths.Doc().Child("name").Index(0),
		"index out of range":       paths.Doc().Child("items").Index(2),
		"child of sequence":        paths.Doc().Child("items", "a"),
		"missing key then index":   paths.Doc().Child("nope").Index(0),
		"missing key then child":   paths.Doc().Child("nope", "deeper"),
		"index then missing child": paths.Doc().Child("items").Index(0).Child("x"),
	}

	for name, path := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, err := paths.NewResolver(doc).Token(path)
			require.ErrorIs(t, err, paths.ErrNotFound)
			assert.Contains(t, err.Error(), path.String())

			_, err = paths.NewResolver(doc).Token(path.Key())
			require.ErrorIs(t, err, paths.ErrNotFound)

			_, err = paths.NewResolver(doc).Node(path)
			require.ErrorIs(t, err, paths.ErrNotFound)

			nodes, err := paths.NewResolver(doc).Nodes(path)
			require.NoError(t, err)
			assert.Empty(t, nodes)
		})
	}
}

func TestPath_Wildcards(t *testing.T) {
	t.Parallel()

	doc := yamltest.FirstDocument(t, stringtest.Input(`
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
		nestseq:
		  a:
		    - a: [x, y]
		    - z
		aliased: [&s [p, q], [r], *s]
		refs: [*r, *r]
	`)).DocumentAST()

	tcs := map[string]struct {
		path string
		want []string
	}{
		"index all":                                       {path: "$.items[*].name", want: []string{"a", "b"}},
		"index all then index all":                        {path: "$.items[*].tags[*]", want: []string{"x", "y", "z"}},
		"index all on a mapping matches nothing":          {path: "$.meta[*]", want: []string{}},
		"child all":                                       {path: "$.ref.*", want: []string{"e"}},
		"child all then child":                            {path: "$.meta.*.name", want: []string{"d"}},
		"child all on a sequence matches nothing":         {path: "$.items.*", want: []string{}},
		"child all on a scalar matches nothing":           {path: "$.meta.name.*", want: []string{}},
		"index all then child all then index all":         {path: "$.items[*].*[*]", want: []string{"x", "y", "z"}},
		"child all through an alias":                      {path: "$.alias.*", want: []string{"e"}},
		"child all sees a merged entry":                   {path: "$.merged.*", want: []string{"e"}},
		"child all through aliases repeats the anchor":    {path: "$.refs[*].*", want: []string{"e", "e"}},
		"recursive then child all":                        {path: "$..nested.*", want: []string{"d"}},
		"recursive visits anchors once and skips aliases": {path: "$..name", want: []string{"a", "b", "c", "d", "e"}},
		"recursive below a child":                         {path: "$.meta..name", want: []string{"c", "d"}},
		"recursive then index":                            {path: "$..tags[0]", want: []string{"x", "z"}},
		// The inner ..a reaches the scalar 1 from two outer matches. The
		// mapping {a: 1} prints as its ":" token.
		"chained recursive yields each node once": {path: "$.chain..a..a", want: []string{":", "1"}},
		// The mapping {a: [x, y]} prints as its ":" token.
		"recursive then index all keeps document order": {path: "$.nestseq..a[*]", want: []string{":", "x", "y", "z"}},
		"index all through an alias keeps path order": {
			path: "$.aliased[*][*]",
			want: []string{"p", "q", "r", "p", "q"},
		},
		"index all through aliases repeats the anchor":  {path: "$.refs[*].name", want: []string{"e", "e"}},
		"recursive looks through an alias at its start": {path: "$.alias..name", want: []string{"e"}},
		"recursive skips alias merge sources":           {path: "$.merged..name", want: []string{}},
		"single match":                                  {path: "$.meta.name", want: []string{"c"}},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			nodes, err := paths.NewResolver(doc).Nodes(paths.MustParse(tc.path))
			require.NoError(t, err)

			got := make([]string, 0, len(nodes))
			for _, n := range nodes {
				got = append(got, n.GetToken().Value)
			}

			assert.Equal(t, tc.want, got)
		})
	}
}

// missingFirst returns the document of [yamltest.WideMerge], except that
// its merge list starts with an alias that does not resolve. A lookup of
// each key of m stops at that alias.
func missingFirst(n int) string {
	return strings.Replace(yamltest.WideMerge(n), "<<: [", "<<: [*missing, ", 1)
}

// mergedRecords returns a document that holds as many mappings as
// sources, named d0 and up, and the sequence items of n mappings. Each of
// those holds the key name, then as many more keys of its own as keys,
// and then a merge key that lists an alias to each of the d mappings. A
// lookup of each key of a record reads that list, so a `..name` walk
// reads the same multiple of the nodes of the document however large n
// is.
func mergedRecords(n, sources, keys int) string {
	var (
		sb      strings.Builder
		aliases []string
	)

	for i := range sources {
		fmt.Fprintf(&sb, "d%d: &d%d {p%d: 0}\n", i, i, i)

		aliases = append(aliases, fmt.Sprintf("*d%d", i))
	}

	sb.WriteString("items:\n")

	for i := range n {
		fmt.Fprintf(&sb, "  - name: r%d\n", i)

		for k := range keys {
			fmt.Fprintf(&sb, "    k%d: %d\n", k, k)
		}

		sb.WriteString("    <<: [" + strings.Join(aliases, ", ") + "]\n")
	}

	return sb.String()
}

func TestPath_Matches_AliasFanOut(t *testing.T) {
	t.Parallel()

	// The document of aliasList holds n scalars under the key a and n
	// aliases to a under the key b, so $.b[*][*] selects n*n nodes.
	aliasList := func(n int) string {
		return stringtest.LinesLF(
			"a: &a "+yamltest.FlowSequence("x", n),
			"b: "+yamltest.FlowSequence("*a", n),
		)
	}

	// The document of aliasMap holds n entries under the key a and n
	// entries that are aliases to a under the key b, so $.b.*.* selects
	// n*n nodes.
	aliasMap := func(n int) string {
		var entries, aliases []string

		for i := range n {
			entries = append(entries, fmt.Sprintf("k%d: x", i))
			aliases = append(aliases, fmt.Sprintf("j%d: *a", i))
		}

		return stringtest.LinesLF(
			"a: &a {"+strings.Join(entries, ", ")+"}",
			"b: {"+strings.Join(aliases, ", ")+"}",
		)
	}

	tcs := map[string]struct {
		err   error
		input string
		path  string
		want  int
	}{
		"wide mapping of aliases": {
			input: aliasMap(1000),
			path:  "$.b.*.*",
			err:   paths.ErrExcessiveAliasing,
		},
		"small mapping of aliases": {
			input: aliasMap(10),
			path:  "$.b.*.*",
			want:  100,
		},
		"nested alias levels": {
			input: yamltest.AliasLevels(5),
			path:  "$.a[5][*][*][*][*][*]",
			err:   paths.ErrExcessiveAliasing,
		},
		"wide list of aliases": {
			input: aliasList(500),
			path:  "$.b[*][*]",
			err:   paths.ErrExcessiveAliasing,
		},
		"few alias levels": {
			input: yamltest.AliasLevels(2),
			path:  "$.a[2][*][*]",
			want:  100,
		},
		"short list of aliases": {
			input: aliasList(10),
			path:  "$.b[*][*]",
			want:  100,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assertMatchCount(t, tc.input, tc.path, tc.want, tc.err)
		})
	}
}

// assertMatchCount asserts that the path expression expr selects want
// nodes in the first document of input. When wantErr is set, it asserts
// instead that Matches and Nodes fail with it, as Node does for a path
// without a wildcard.
func assertMatchCount(t *testing.T, input, expr string, want int, wantErr error) {
	t.Helper()

	doc := yamltest.FirstDocument(t, input, niceyaml.WithDepthLimit(0), niceyaml.WithByteLimit(0)).DocumentAST()
	path := paths.MustParse(expr)

	matches, err := paths.NewResolver(doc).Matches(path)
	if wantErr == nil {
		require.NoError(t, err)
		assert.Len(t, matches, want)

		return
	}

	require.ErrorIs(t, err, wantErr)

	_, err = paths.NewResolver(doc).Nodes(path)
	require.ErrorIs(t, err, wantErr)

	// Node takes only a path without a wildcard.
	_, err = paths.NewResolver(doc).Node(path)
	if !errors.Is(err, paths.ErrWildcard) {
		require.ErrorIs(t, err, wantErr)
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
		return "m:\n" + yamltest.GenerateLines(n, "  k%[1]d: %[1]d") +
			"  <<: " + strings.Repeat("{<<: ", n) + "{z: 0}" + strings.Repeat("}", n) + "\n"
	}

	// The document of aliasChain adds to mergeChain the key l, which lists
	// n aliases to the last mapping of the chain.
	aliasChain := func(n int) string {
		return mergeChain(n) + "l: " + yamltest.FlowSequence(fmt.Sprintf("*b%d", n-1), n) + "\n"
	}

	// The document of aliasWideMerge holds the mapping x, which merges n
	// aliases to the mapping a, and the key l, which lists n aliases to x.
	aliasWideMerge := func(n int) string {
		return stringtest.LinesLF(
			"a: &a {z: 0}",
			"x: &x {<<: "+yamltest.FlowSequence("*a", n)+"}",
			"l: "+yamltest.FlowSequence("*x", n),
		)
	}

	// The document of sharedMerge holds the sequence s of n aliases to the
	// mapping a, and the mapping d, which merges n inline mappings that
	// each merge s. One lookup in d reads s once for each of them.
	sharedMerge := func(n int) string {
		return stringtest.LinesLF(
			"a: &a {z: 0}",
			"s: &s "+yamltest.FlowSequence("*a", n),
			"d: {<<: "+yamltest.FlowSequence("{<<: *s}", n)+"}",
		)
	}

	tcs := map[string]struct {
		err   error
		input string
		path  string
		want  int
	}{
		"recursive key over a merge chain": {
			input: mergeChain(2000),
			path:  "$..nope",
			err:   paths.ErrExcessiveMerging,
		},
		"recursive key over an inline merge chain": {
			input: inlineChain(2000),
			path:  "$..nope",
			err:   paths.ErrExcessiveMerging,
		},
		"child key of each alias to a merge chain": {
			input: aliasChain(2000),
			path:  "$.l[*].nope",
			err:   paths.ErrExcessiveMerging,
		},
		"recursive key over a merge of many aliases": {
			input: yamltest.WideMerge(2000),
			path:  "$..nope",
			err:   paths.ErrExcessiveMerging,
		},
		"recursive key over a merge of many scalars": {
			input: yamltest.WideMergeOf(2000, "0"),
			path:  "$..nope",
			err:   paths.ErrExcessiveMerging,
		},
		"recursive key over a merge of many aliases and a missing one": {
			input: yamltest.WideMergeOf(2000, "*a", "*missing"),
			path:  "$..nope",
			err:   paths.ErrExcessiveMerging,
		},
		"recursive key over a merge of many aliases and a later anchor": {
			input: yamltest.WideMergeOf(2000, "*a", "*late") + "late: &late {y: 1}\n",
			path:  "$..nope",
			err:   paths.ErrExcessiveMerging,
		},
		"recursive key over a merge of many scalars and a missing alias": {
			input: yamltest.WideMergeOf(2000, "0", "*missing"),
			path:  "$..nope",
			err:   paths.ErrExcessiveMerging,
		},
		"child key of each alias to a wide merge": {
			input: aliasWideMerge(2000),
			path:  "$.l[*].nope",
			err:   paths.ErrExcessiveMerging,
		},
		"child key over many merges of one sequence": {
			input: sharedMerge(1000),
			path:  "$.d.nope",
			err:   paths.ErrExcessiveMerging,
		},
		"every entry over a merge chain": {
			input: mergeChain(2000),
			path:  "$.b1999.*",
			err:   paths.ErrExcessiveMerging,
		},
		"every entry over a merge of many aliases": {
			input: yamltest.WideMerge(2000),
			path:  "$.m.*",
			err:   paths.ErrExcessiveMerging,
		},
		"every entry of each alias to a wide merge": {
			input: aliasWideMerge(2000),
			path:  "$.l[*].*",
			err:   paths.ErrExcessiveMerging,
		},
		"every node over a merge chain": {
			input: mergeChain(2000),
			path:  "$..*",
			err:   paths.ErrExcessiveMerging,
		},
		"every node over an inline merge chain": {
			input: inlineChain(2000),
			path:  "$..*",
			err:   paths.ErrExcessiveMerging,
		},
		"every node over a merge of many aliases": {
			input: yamltest.WideMerge(2000),
			path:  "$..*",
			err:   paths.ErrExcessiveMerging,
		},
		"every node over a short merge chain": {
			input: mergeChain(10),
			path:  "$..*",
			want:  20,
		},
		"every node over a merge of a few aliases": {
			input: yamltest.WideMerge(10),
			path:  "$..*",
			want:  13,
		},
		// The lookups of this walk read as many nodes under merge keys as
		// those of a `..name` walk do.
		"every node over many records that merge one list": {
			input: mergedRecords(400, 100, 100),
			path:  "$..*",
			want:  41001,
		},
		"every entry over a short merge chain": {
			input: mergeChain(10),
			path:  "$.b9.*",
			want:  10,
		},
		"every entry over a merge of a few aliases": {
			input: yamltest.WideMerge(10),
			path:  "$.m.*",
			want:  11,
		},
		"every entry of each alias to a narrow merge": {
			input: aliasWideMerge(10),
			path:  "$.l[*].*",
			want:  10,
		},
		"recursive key over a short merge chain": {
			input: mergeChain(10),
			path:  "$..x5",
			want:  1,
		},
		"child key of each alias to a short merge chain": {
			input: aliasChain(10),
			path:  "$.l[*].x0",
			want:  10,
		},
		"recursive key over a merge of a few aliases": {
			input: yamltest.WideMerge(10),
			path:  "$..0x1005",
			want:  1,
		},
		"recursive key over a merge of a few aliases and a missing one": {
			input: yamltest.WideMergeOf(10, "*a", "*missing"),
			path:  "$..0x1005",
			want:  0,
		},
		"recursive key over a merge that lists a missing alias first": {
			input: missingFirst(10),
			path:  "$..0x1005",
			want:  0,
		},
		"recursive key over a wide merge that lists a missing alias first": {
			input: missingFirst(2000),
			path:  "$..nope",
			want:  0,
		},
		"child key of each alias to a narrow merge": {
			input: aliasWideMerge(10),
			path:  "$.l[*].z",
			want:  10,
		},
		"child key over a few merges of one sequence": {
			input: sharedMerge(10),
			path:  "$.d.z",
			want:  1,
		},
		// The lookups of this walk read over 4 million nodes under merge
		// keys, which is about 20 times the nodes of the document.
		"recursive key over many records that merge one list": {
			input: mergedRecords(400, 100, 100),
			path:  "$..name",
			want:  400,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assertMatchCount(t, tc.input, tc.path, tc.want, tc.err)
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
	doc := yamltest.FirstDocument(t, stringtest.Input(`
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
	`)).DocumentAST()

	tcs := map[string]struct {
		err  error
		name string
		want string
	}{
		"key of the last source":                     {name: "g", want: "G"},
		"key of the longest list":                    {name: "k", want: "A"},
		"key of a source before the longest list":    {name: "i", want: "I"},
		"key of the first source of the middle list": {name: "j", want: "J"},
		"key of the first source":                    {name: "h", want: "H"},
		"key of the mapping itself":                  {name: "own", want: "C"},
		"key that no source holds":                   {name: "nope", err: paths.ErrNotFound},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			node, err := paths.NewResolver(doc).Node(paths.Doc().Child("c", tc.name))
			if tc.err != nil {
				require.ErrorIs(t, err, tc.err)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, tc.want, node.String())

			// The `[*]` selector shares one lookup state among the lookups
			// in each element, and each of them finds the same entry.
			nodes, err := paths.NewResolver(doc).Nodes(paths.Doc().Child("l").IndexAll().Child(tc.name))
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

	doc := yamltest.FirstDocument(t, stringtest.Input(`
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
	`)).DocumentAST()

	tcs := map[string]struct {
		path string
		want string
	}{
		"double quoted key": {path: "$.'quoted key'", want: "1"},
		"single quoted key": {path: "$.single", want: "2"},
		"dotted key":        {path: "$.'plain.dotted'", want: "3"},
		"integer key":       {path: "$.7", want: "4"},
		"explicit key":      {path: "$.complex", want: "5"},
		"explicit key target is the key itself, not the indicator": {path: "$.complex~", want: "complex"},
		"tagged key matches by content":                            {path: "$.tagged", want: "6"},
		"tagged key target skips the tag":                          {path: "$.tagged~", want: "tagged"},
		"anchored key matches by content":                          {path: "$.anchored", want: "7"},
		"anchored key target skips the anchor":                     {path: "$.anchored~", want: "anchored"},
		"alias key matches by its anchor's content":                {path: "$.aliased_name", want: "8"},
		"alias key target is the alias":                            {path: "$.aliased_name~", want: "*"},
		"block scalar key matches by its content":                  {path: `$.'block\n'`, want: "9"},
		"empty flow mapping value target is the entry key":         {path: "$.empty", want: "empty"},
		"empty flow sequence value target is the entry key":        {path: "$.none", want: "none"},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, resolveToken(t, doc, tc.path).Value)
		})
	}

	t.Run("indicator, tag, anchor, and alias text are not key names", func(t *testing.T) {
		t.Parallel()

		for _, name := range []string{"!!str", "&k", "k", "&", "?", "*", "kk", "|"} {
			_, err := paths.NewResolver(doc).Node(paths.Doc().Child(name))
			require.ErrorIs(t, err, paths.ErrNotFound, "Child(%q)", name)
		}
	})
}

// resolveToken returns the token the path expression expr selects in doc.
// It fails the test when the path does not resolve.
func resolveToken(t *testing.T, doc *ast.DocumentNode, expr string) *token.Token {
	t.Helper()

	tk, err := paths.NewResolver(doc).Token(paths.MustParse(expr))
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
			body: mapNode(mapEntry((*ast.StringNode)(nil), strNode("1"))),
			path: paths.Doc(),
			want: "resolve $: not found: node has no token",
		},
		"nil entry": {
			body: mapNode(nil),
			path: paths.Doc(),
			want: "resolve $: not found: node has no token",
		},
		"value holding a typed nil": {
			body: mapNode(mapEntry(strNode("a"), (*ast.StringNode)(nil))),
			path: paths.Doc().Child("a"),
			want: "resolve $.a: not found: node has no token",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, err := paths.NewResolver(&ast.DocumentNode{Body: tc.body}).Token(tc.path)
			require.ErrorIs(t, err, paths.ErrNotFound)
			require.EqualError(t, err, tc.want)
		})
	}
}

func TestPath_Node_TwoMergeKeys(t *testing.T) {
	t.Parallel()

	// A mapping may hold two merge keys when the parser allows duplicates.
	// The decoder sets each entry in document order. It thus takes the
	// value of the later merge key, and a key of the mapping between the
	// two wins over the earlier merge key and loses to the later one when
	// that one brings in the same key. The path selects what the decoder
	// takes.
	tcs := map[string]struct {
		input string
		want  string
		found []string
	}{
		"later merge key wins over the earlier one": {
			input: stringtest.LinesLF(
				"p: &p {k: P}",
				"q: &q {k: Q}",
				"t:",
				"  <<: *p",
				"  <<: *q",
			),
			want: "Q",
		},
		"own key wins over an earlier merge key": {
			input: stringtest.LinesLF(
				"p: &p {k: P}",
				"q: &q {j: Q}",
				"t:",
				"  <<: *p",
				"  k: own",
				"  <<: *q",
			),
			want:  "own",
			found: []string{"$.t.k"},
		},
		"later merge key wins over the own key": {
			input: stringtest.LinesLF(
				"p: &p {k: P}",
				"q: &q {k: Q}",
				"t:",
				"  <<: *p",
				"  k: own",
				"  <<: *q",
			),
			want: "Q",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			doc := yamltest.FirstDocument(t, tc.input, niceyaml.WithAllowDuplicateKeys(true)).DocumentAST()

			var decoded struct {
				T map[string]string `yaml:"t"`
			}

			err := yaml.UnmarshalWithOptions([]byte(tc.input), &decoded, yaml.AllowDuplicateMapKey())
			require.NoError(t, err)
			assert.Equal(t, tc.want, decoded.T["k"])

			node, err := paths.NewResolver(doc).Node(paths.MustParse("$.t.k"))
			require.NoError(t, err)
			assert.Equal(t, tc.want, node.String())

			matches := requireMatches(t, doc, "$.t..k")
			assert.Equal(t, tc.found, matchPaths(matches))
		})
	}
}

func TestPath_Matches_RepeatedMergeKey(t *testing.T) {
	t.Parallel()

	// The decoder merges both inline mappings, but a path through `<<`
	// selects the later one, so `..` visits only that mapping.
	src := stringtest.LinesLF("t:", "  <<: {a: 1}", "  <<: {b: 2}")

	doc := yamltest.FirstDocument(t, src, niceyaml.WithAllowDuplicateKeys(true)).DocumentAST()

	assert.Empty(t, requireMatches(t, doc, "$..a"))
	assert.Equal(t, []string{"$.t.<<.b"}, matchPaths(requireMatches(t, doc, "$..b")))

	node, err := paths.NewResolver(doc).Node(paths.MustParse("$.t.a"))
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
			input: stringtest.LinesLF(
				"base: &b {name: y}",
				"m:",
				"  <<: [{name: x}, *b]",
			),
			want:  []string{"$.m.<<[0].name"},
			value: "y",
		},
		"own key overrides": {
			input: stringtest.LinesLF("m:", "  <<: {name: x}", "  name: z"),
			want:  []string{"$.m.<<.name", "$.m.name"},
			value: "z",
		},
		"later merge overrides an own key": {
			input: stringtest.LinesLF("m:", "  name: z", "  <<: {name: x}"),
			want:  []string{"$.m.<<.name"},
			value: "x",
		},
		"later alias merge overrides an own key": {
			input: stringtest.LinesLF(
				"base: &b {name: y}",
				"m:",
				"  name: z",
				"  <<: *b",
			),
			want:  nil,
			value: "y",
		},
		"alias source alone": {
			input: stringtest.LinesLF("base: &b {name: y}", "m:", "  <<: *b"),
			want:  nil,
			value: "y",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			doc := yamltest.FirstDocument(t, tc.input).DocumentAST()

			matches := requireMatches(t, doc, "$.m..name")
			assert.Equal(t, tc.want, matchPaths(matches))

			node, err := paths.NewResolver(doc).Node(paths.MustParse("$.m.name"))
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
	tcs := map[string]string{
		"alias key first": stringtest.LinesLF(
			`a: {? &x "<<" : 1}`,
			"m:",
			"  *x : {k: 1}",
			"  <<: {k: 2}",
		),
		"merge key first": stringtest.LinesLF(
			`a: {? &x "<<" : 1}`,
			"m:",
			"  <<: {k: 2}",
			"  *x : {k: 1}",
		),
		"alias key before a source with a merge key": stringtest.LinesLF(
			`a: {? &x "<<" : 1}`,
			"b: &b {j: 3}",
			"m:",
			"  *x : {k: 1}",
			"  <<: {<<: *b, k: 2}",
		),
	}

	for name, input := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			doc := yamltest.FirstDocument(t, input).DocumentAST()

			node, err := paths.NewResolver(doc).Node(paths.MustParse("$.m.'<<'.k"))
			require.NoError(t, err)
			assert.Equal(t, "1", node.String())

			node, err = paths.NewResolver(doc).Node(paths.MustParse("$.m.k"))
			require.NoError(t, err)
			assert.Equal(t, "2", node.String())

			matches := requireMatches(t, doc, "$.m..k")
			assert.Equal(t, []string{"$.m.<<.k=1"}, matchValues(matches))
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
			input: stringtest.LinesLF(
				`k: &k "<<"`,
				"inner: &inner",
				"  *k : real",
				"top:",
				"  <<: *inner",
			),
			want: []string{"$.inner.<<"},
		},
		"inline source": {input: stringtest.LinesLF(
			`k: &k "<<"`,
			"top:",
			"  <<: {*k : real}",
		)},
		"sequence source": {
			input: stringtest.LinesLF(
				`k: &k "<<"`,
				"inner: &inner",
				"  *k : real",
				"top:",
				"  <<: [*inner]",
			),
			want: []string{"$.inner.<<"},
		},
		"source that merges the key": {
			input: stringtest.LinesLF(
				`k: &k "<<"`,
				"base: &base {*k : real}",
				"mid: &mid {<<: *base}",
				"top:",
				"  <<: *mid",
			),
			want: []string{"$.base.<<"},
		},
		"earlier merge key": {input: stringtest.LinesLF(
			`k: &k "<<"`,
			"top:",
			"  <<: {*k : real}",
			"  <<: {a: 1}",
		)},
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

			doc := yamltest.FirstDocument(t, tc.input, niceyaml.WithAllowDuplicateKeys(true)).DocumentAST()

			node, err := paths.NewResolver(doc).Node(paths.MustParse("$.top.'<<'"))
			require.NoError(t, err)
			assert.Equal(t, "real", node.String())

			matches := requireMatches(t, doc, "$..'<<'")
			assert.Equal(t, tc.want, matchPaths(matches))
		})
	}
}

func TestPath_Node_LaterDuplicateKeyWins(t *testing.T) {
	t.Parallel()

	// A mapping may hold one key twice when the parser allows duplicates.
	// The decoder keeps the value of the later entry, so the path selects
	// it, and `..a` lists that entry alone.
	src := stringtest.LinesLF("m:", "  a: 1", "  a: 2")

	doc := yamltest.FirstDocument(t, src, niceyaml.WithAllowDuplicateKeys(true)).DocumentAST()

	node, err := paths.NewResolver(doc).Node(paths.MustParse("$.m.a"))
	require.NoError(t, err)
	assert.Equal(t, "2", node.String())

	key, err := paths.NewResolver(doc).Token(paths.MustParse("$.m.a~"))
	require.NoError(t, err)
	assert.Equal(t, 3, key.Position.Line)

	assert.Equal(t, []string{"$.m.a"}, matchPaths(requireMatches(t, doc, "$..a")))
}

// nestedChain returns depth flow mappings nested in one another, each with
// the single key a.
func nestedChain(depth int) string {
	return strings.Repeat("{a: ", depth) + "1" + strings.Repeat("}", depth)
}

func TestPath_Matches_RecursiveReachedTwice(t *testing.T) {
	t.Parallel()

	chain := nestedChain(400)

	// The paths of the entries of chain, from the outermost in.
	chainPaths := make([]string, 0, 400)
	for i := 1; i <= 400; i++ {
		chainPaths = append(chainPaths, "$"+strings.Repeat(".a", i))
	}

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
			input: stringtest.LinesLF(
				"base: &b {c: {name: 1}}",
				"refs: [*b, *b]",
			),
			path: "$.refs[*]..name",
			want: []string{"$.refs[0].c.name"},
		},
		"outer match reaches an alias target first": {
			input: stringtest.LinesLF(
				"k:",
				"  y: &b",
				"    name: 1",
				"  k: *b",
			),
			path: "$..k..name",
			want: []string{"$.k.y.name"},
		},
		"merged entry comes before the outer walk": {
			// The walk from $.x.k reaches the anchor through the `<<`
			// entry, one place deeper than the order of the merged
			// entry $.x.k.x.k, so the merged entry's path comes first.
			input: stringtest.LinesLF(
				"x:",
				"  k:",
				"    x:",
				"      <<: {k: &c {name: 1}}",
			),
			path: "$..x.k..name",
			want: []string{"$.x.k.x.k.name"},
		},
		"later walk stops at an earlier match": {
			// The walk from $.b.k stops at the mapping that the walk
			// from $.a.k covers. Without the stop, the walk would find
			// the entry again, and resolve would keep only the copy that
			// sorts first, so the output alone does not show that the
			// walk stopped.
			input: stringtest.LinesLF(
				"a: &x",
				"  k:",
				"    name: 1",
				"b:",
				"  k: *x",
			),
			path: "$..k..name",
			want: []string{"$.a.k.name"},
		},
		"later walk stops at an earlier match in a sequence": {
			input: stringtest.LinesLF(
				"a: &x",
				"  k:",
				"    - name: 1",
				"b:",
				"  k: *x",
			),
			path: "$..k..name",
			want: []string{"$.a.k[0].name"},
		},
		"walk that extends an earlier match keeps its own path": {
			// The merged $.a.k.v takes the place of the `<<` key, so
			// the walk from $.a.k.<<.k.v reaches the anchor at an order
			// that extends the order of $.a.k.v. The entry it finds
			// there sorts first, so the walk must not stop.
			input: stringtest.LinesLF(
				"s: &V {x: 0, name: 1}",
				"a:",
				"  k:",
				"    <<: {k: {v: *V}, v: *V}",
			),
			path: "$..k.v..name",
			want: []string{"$.a.k.<<.k.v.name"},
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

			doc := yamltest.FirstDocument(t, tc.input, niceyaml.WithDepthLimit(0)).DocumentAST()

			matches := requireMatches(t, doc, tc.path)

			assert.Equal(t, tc.want, matchPaths(matches))
		})
	}
}

// matchPaths returns the path of each match as text, and nil for no
// matches.
func matchPaths(matches []paths.Match) []string {
	var got []string

	for _, m := range matches {
		got = append(got, m.Path.String())
	}

	return got
}

// matchValues returns each match as its path and the text of its node,
// joined by "=", and nil for no matches.
func matchValues(matches []paths.Match) []string {
	var got []string

	for _, m := range matches {
		got = append(got, m.Path.String()+"="+m.Node.String())
	}

	return got
}

// requireMatches returns the matches of the path expression expr in doc.
// The test fails unless Nodes lists the node of each match in the same
// order, and unless the path of each match selects the node of that match
// alone and reads back from its text.
func requireMatches(t *testing.T, doc *ast.DocumentNode, expr string) []paths.Match {
	t.Helper()

	path := paths.MustParse(expr)

	matches, err := paths.NewResolver(doc).Matches(path)
	require.NoError(t, err)

	nodes, err := paths.NewResolver(doc).Nodes(path)
	require.NoError(t, err)
	require.Len(t, nodes, len(matches))

	for i, m := range matches {
		assert.Same(t, nodes[i], m.Node)

		single, err := paths.NewResolver(doc).Node(m.Path)
		require.NoError(t, err)
		assert.Same(t, m.Node, single)

		parsed, err := paths.Parse(m.Path.String())
		require.NoError(t, err)
		assert.Equal(t, m.Path, parsed)
	}

	return matches
}

func TestPath_Matches(t *testing.T) {
	t.Parallel()

	doc := yamltest.FirstDocument(t, stringtest.Input(`
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
	`)).DocumentAST()

	tcs := map[string]struct {
		path string
		want []string
	}{
		"index all": {
			path: "$.items[*]",
			want: []string{"$.items[0]", "$.items[1]"},
		},
		"index all gives each element its own path below a long prefix": {
			path: "$.levels.one.two[*]",
			want: []string{"$.levels.one.two[0]", "$.levels.one.two[1]"},
		},
		"recursive": {
			path: "$..name",
			want: []string{"$.spec.name", "$.spec.deep[0].name", "$.base.name"},
		},
		"recursive below a child": {
			path: "$.spec..name",
			want: []string{"$.spec.name", "$.spec.deep[0].name"},
		},
		"recursive gives each sibling its own path": {
			path: "$..id",
			want: []string{"$.siblings.a.id", "$.siblings.a.x[0].id", "$.siblings.b.id"},
		},
		"recursive below a recursive match": {
			path: "$..a..id",
			want: []string{"$.siblings.a.id", "$.siblings.a.x[0].id"},
		},
		"single": {
			path: "$.spec.name",
			want: []string{"$.spec.name"},
		},
		"key of an entry": {
			path: "$.spec..name~",
			want: []string{"$.spec.name~", "$.spec.deep[0].name~"},
		},
		"through an alias keeps the path as written": {
			path: "$.ref.name",
			want: []string{"$.ref.name"},
		},
		"through a merge key keeps the path of the mapping": {
			path: "$.mixed.name",
			want: []string{"$.mixed.name"},
		},
		"each alias to one anchor is its own match": {
			path: "$.twice[*]",
			want: []string{"$.twice[0]", "$.twice[1]"},
		},
		"recursive then child keeps document order": {
			// The outer entry a comes first, but its c follows the c of
			// the inner one in the source.
			path: "$.nest..a.c",
			want: []string{"$.nest.a.b.a.c", "$.nest.a.c"},
		},
		"quoted name": {
			path: "$.'dot.key'[*]",
			want: []string{"$.'dot.key'[0]"},
		},
		"recursive finds an alias key by its anchor's content": {
			path: "$..tagline",
			want: []string{"$.keyed.tagline"},
		},
		"recursive finds a block scalar key by its content": {
			path: "$..'folded text'",
			want: []string{"$.keyed.'folded text'"},
		},
		"nothing": {
			path: "$.missing[*]",
			want: nil,
		},
		"relative path reads from the root": {
			path: "@.items[*]",
			want: []string{"$.items[0]", "$.items[1]"},
		},
		"relative path with no wildcard": {
			path: "@.spec.name",
			want: []string{"$.spec.name"},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, matchPaths(requireMatches(t, doc, tc.path)))
		})
	}
}

func TestPath_Matches_ChildAll(t *testing.T) {
	t.Parallel()

	// A `.*` selector lists the entry a `.name` selector resolves in a
	// mapping, once for each name, so the path of each match selects that
	// match's node and reads back from its text.
	dups := []niceyaml.SourceOption{niceyaml.WithAllowDuplicateKeys(true)}

	tcs := map[string]struct {
		input string
		path  string
		want  []string
		opts  []niceyaml.SourceOption
	}{
		"each entry in document order": {
			input: stringtest.LinesLF(
				"jobs:",
				"  build: 1",
				"  test: 2",
				"  lint: 3",
			),
			path: "$.jobs.*",
			want: []string{"$.jobs.build=1", "$.jobs.test=2", "$.jobs.lint=3"},
		},
		"entries of the root": {
			input: "a: 1\nb: 2\n",
			path:  "$.*",
			want:  []string{"$.a=1", "$.b=2"},
		},
		"child of each entry": {
			input: stringtest.LinesLF(
				"jobs:",
				"  build:",
				"    runs-on: linux",
				"  docs:",
				"    uses: x",
				"  test:",
				"    runs-on: mac",
			),
			path: "$.jobs.*.runs-on",
			want: []string{"$.jobs.build.runs-on=linux", "$.jobs.test.runs-on=mac"},
		},
		"element of a sequence in each entry": {
			input: stringtest.LinesLF(
				"jobs:",
				"  a:",
				"    steps:",
				"      - uses: x",
				"      - run: y",
				"  b:",
				"    steps:",
				"      - uses: z",
			),
			path: "$.jobs.*.steps[*].uses",
			want: []string{"$.jobs.a.steps[0].uses=x", "$.jobs.b.steps[0].uses=z"},
		},
		"entries of each entry": {
			input: stringtest.LinesLF(
				"paths:",
				"  /a:",
				"    get: 1",
				"    post: 2",
				"  /b:",
				"    get: 3",
			),
			path: "$.paths.*.*",
			want: []string{"$.paths./a.get=1", "$.paths./a.post=2", "$.paths./b.get=3"},
		},
		"key of each entry": {
			input: stringtest.LinesLF("jobs:", "  build: 1", "  test: 2"),
			path:  "$.jobs.*~",
			want:  []string{"$.jobs.build~=build", "$.jobs.test~=test"},
		},
		"entries below a recursive match": {
			input: stringtest.LinesLF(
				"x:",
				"  env: {A: 1}",
				"y:",
				"  env: {B: 2}",
			),
			path: "$..env.*",
			want: []string{"$.x.env.A=1", "$.y.env.B=2"},
		},
		"recursive below each entry": {
			input: stringtest.LinesLF(
				"jobs:",
				"  a:",
				"    steps:",
				"      - uses: x",
				"  b:",
				"    uses: y",
			),
			path: "$.jobs.*..uses",
			want: []string{"$.jobs.a.steps[0].uses=x", "$.jobs.b.uses=y"},
		},
		"keys the decoder respells keep their source text": {
			input: stringtest.LinesLF(
				"ports:",
				"  0x10: a",
				"  3.10: b",
				"  ~: c",
				"  yes: d",
				`  "": e`,
				"  a.b: f",
				"  1_000: g",
			),
			path: "$.ports.*",
			want: []string{
				"$.ports.0x10=a", "$.ports.'3.10'=b", "$.ports.'~'=c", "$.ports.yes=d",
				"$.ports.''=e", "$.ports.'a.b'=f", "$.ports.1_000=g",
			},
		},
		"key named star is one of the entries": {
			input: stringtest.LinesLF("m:", `  "*": 1`, "  b: 2"),
			path:  "$.m.*",
			want:  []string{"$.m.'*'=1", "$.m.b=2"},
		},
		"quoted star selects the key named star alone": {
			input: stringtest.LinesLF("m:", `  "*": 1`, "  b: 2"),
			path:  "$.m.'*'",
			want:  []string{"$.m.'*'=1"},
		},
		"alias key takes the name of its anchor": {
			input: stringtest.LinesLF("k: &k name", "m:", "  *k : v"),
			path:  "$.m.*",
			want:  []string{"$.m.name=v"},
		},
		"key with no name is left out": {
			input: stringtest.LinesLF("m:", "  b: 2", "  *nope : 1"),
			path:  "$.m.*",
			want:  []string{"$.m.b=2"},
		},
		"sequence has no entries": {
			input: "m: [1, 2]\n",
			path:  "$.m.*",
		},
		"scalar has no entries": {
			input: "m: x\n",
			path:  "$.m.*",
		},
		"null has no entries": {
			input: "m:\nn: 1\n",
			path:  "$.m.*",
		},
		"missing key has no entries": {
			input: "m: x\n",
			path:  "$.nope.*",
		},
		"empty mapping has no entries": {
			input: "m: {}\n",
			path:  "$.m.*",
		},
		"through an alias keeps the path as written": {
			input: "base: &b {a: 1}\nm: *b\n",
			path:  "$.m.*",
			want:  []string{"$.m.a=1"},
		},
		"through a tag": {
			input: "m: !!map {a: 1}\n",
			path:  "$.m.*",
			want:  []string{"$.m.a=1"},
		},
		"each alias to one mapping lists its entries": {
			input: stringtest.LinesLF("base: &b {a: 1}", "l: [*b, *b]"),
			path:  "$.l[*].*",
			want:  []string{"$.l[0].a=1", "$.l[1].a=1"},
		},
		"merged entries take the place of a merge key before the own keys": {
			input: stringtest.LinesLF(
				"base: &base",
				"  lint: a",
				"  build: old",
				"jobs:",
				"  <<: *base",
				"  build: b",
				"  test: c",
			),
			path: "$.jobs.*",
			want: []string{"$.jobs.lint=a", "$.jobs.build=b", "$.jobs.test=c"},
			opts: dups,
		},
		"merged entries take the place of a merge key after the own keys": {
			input: stringtest.LinesLF(
				"base: &base",
				"  lint: a",
				"  build: old",
				"jobs:",
				"  build: b",
				"  test: c",
				"  <<: *base",
			),
			path: "$.jobs.*",
			want: []string{"$.jobs.test=c", "$.jobs.lint=a", "$.jobs.build=old"},
			opts: dups,
		},
		"later source of a merge list wins": {
			input: stringtest.LinesLF(
				"a: &a {x: 1, y: 1}",
				"b: &b {y: 2, z: 2}",
				"m:",
				"  <<: [*a, *b]",
				"  w: 0",
			),
			path: "$.m.*",
			want: []string{"$.m.x=1", "$.m.y=2", "$.m.z=2", "$.m.w=0"},
		},
		"merge of a mapping that merges another": {
			input: stringtest.LinesLF(
				"base: &base {a: 1}",
				"mid: &mid {<<: *base, b: 2}",
				"m:",
				"  <<: *mid",
				"  c: 3",
			),
			path: "$.m.*",
			want: []string{"$.m.a=1", "$.m.b=2", "$.m.c=3"},
		},
		"inline merge source": {
			input: stringtest.LinesLF("m:", "  <<: {a: 1}", "  b: 2"),
			path:  "$.m.*",
			want:  []string{"$.m.a=1", "$.m.b=2"},
		},
		"key of a merged entry": {
			input: stringtest.LinesLF(
				"base: &base {a: 1}",
				"m:",
				"  <<: *base",
				"  b: 2",
			),
			path: "$.m.*~",
			want: []string{"$.m.a~=a", "$.m.b~=b"},
		},
		"each merge key holds the entries it wins": {
			input: stringtest.LinesLF(
				"a: &a {x: 1, y: 1}",
				"b: &b {y: 2}",
				"m:",
				"  <<: *a",
				"  k: 0",
				"  <<: *b",
			),
			path: "$.m.*",
			want: []string{"$.m.x=1", "$.m.k=0", "$.m.y=2"},
			opts: dups,
		},
		"one source under two merge keys counts at the later": {
			input: stringtest.LinesLF(
				"a: &a {x: 1}",
				"m:",
				"  <<: *a",
				"  k: 0",
				"  <<: *a",
			),
			path: "$.m.*",
			want: []string{"$.m.k=0", "$.m.x=1"},
			opts: dups,
		},
		"merge that leads back to its own mapping": {
			input: stringtest.LinesLF("a: &a", "  x: 1", "  <<: *a"),
			path:  "$.a.*",
			want:  []string{"$.a.x=1"},
		},
		"later duplicate key wins": {
			input: stringtest.LinesLF("m:", "  a: 1", "  b: 2", "  a: 3"),
			path:  "$.m.*",
			want:  []string{"$.m.b=2", "$.m.a=3"},
			opts:  dups,
		},
		"real key with the text of a merge key is an entry": {
			input: stringtest.LinesLF(
				`a: {? &x "<<" : 1}`,
				"m:",
				"  <<: {k: 2}",
				"  *x : real",
			),
			path: "$.m.*",
			want: []string{"$.m.k=2", "$.m.<<=real"},
		},
		"merge brings in a real key with the text of a merge key": {
			input: stringtest.LinesLF(
				`k: &k "<<"`,
				"inner: &inner",
				"  j: 1",
				"  *k : real",
				"top:",
				"  <<: *inner",
			),
			path: "$.top.*",
			want: []string{"$.top.j=1", "$.top.<<=real"},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			doc := yamltest.FirstDocument(t, tc.input, tc.opts...).DocumentAST()
			matches := requireMatches(t, doc, tc.path)

			assert.Equal(t, tc.want, matchValues(matches))
		})
	}
}

func TestPath_Matches_RecursiveAll(t *testing.T) {
	t.Parallel()

	// A `..*` selector visits what a `..name` selector visits and lists
	// each node it visits, so the path of each match selects that match's
	// node and reads back from its text.
	dups := []niceyaml.SourceOption{niceyaml.WithAllowDuplicateKeys(true)}

	tcs := map[string]struct {
		input string
		path  string
		want  []string
		opts  []niceyaml.SourceOption
	}{
		"every node in document order": {
			input: stringtest.LinesLF(
				"a: 1",
				"b: [x, {c: 2}]",
				"d: {e: {f: 3}}",
			),
			path: "$..*",
			want: []string{
				"$.a=1", "$.b=[x, {c: 2}]", "$.b[0]=x", "$.b[1]={c: 2}", "$.b[1].c=2",
				"$.d={e: {f: 3}}", "$.d.e={f: 3}", "$.d.e.f=3",
			},
		},
		"below a child": {
			input: "a: 1\nd: {e: {f: 3}}\n",
			path:  "$.d..*",
			want:  []string{"$.d.e={f: 3}", "$.d.e.f=3"},
		},
		"below a sequence": {
			input: "- [a]\n- b\n",
			path:  "$..*",
			want:  []string{"$[0]=[a]", "$[0][0]=a", "$[1]=b"},
		},
		"through a tag on the start": {
			input: "!!map {a: [1]}\n",
			path:  "$..*",
			want:  []string{"$.a=[1]", "$.a[0]=1"},
		},
		"through an alias on the start keeps the path as written": {
			input: "a: &x {k: [1]}\nb: *x\n",
			path:  "$.b..*",
			want:  []string{"$.b.k=[1]", "$.b.k[0]=1"},
		},
		"aliases below the start are not followed": {
			input: stringtest.LinesLF("a: &x {k: 1}", "b: *x", "c: [*x]"),
			path:  "$..*",
			want:  []string{"$.a={k: 1}", "$.a.k=1", "$.b={k: 1}", "$.c=[*x]", "$.c[0]={k: 1}"},
		},
		"anchors and tags below the start": {
			input: "a: &x !!map {k: !!str 1}\n",
			path:  "$..*",
			want:  []string{"$.a=!!map {k: !!str 1}", "$.a.k=!!str 1"},
		},
		"scalar has nothing below it": {
			input: "a: 1\n",
			path:  "$.a..*",
		},
		"empty collections have nothing below them": {
			input: "a: {}\nb: []\n",
			path:  "$..*",
			want:  []string{"$.a={}", "$.b=[]"},
		},
		"missing key has nothing below it": {
			input: "a: 1\n",
			path:  "$.b..*",
		},
		"key of each node": {
			input: "a: [x]\nb: {c: 1}\n",
			path:  "$..*~",
			want:  []string{"$.a~=a", "$.a[0]~=x", "$.b~=b", "$.b.c~=c"},
		},
		"then a child": {
			input: "a: {b: 1}\nc: [{b: 2}]\n",
			path:  "$..*.b",
			want:  []string{"$.a.b=1", "$.c[0].b=2"},
		},
		"twice lists each node once": {
			input: "a: [x, [y]]\nb: {c: 1}\n",
			path:  "$..*..*",
			want:  []string{"$.a[0]=x", "$.a[1]=[y]", "$.a[1][0]=y", "$.b.c=1"},
		},
		"twice through an alias lists each node once": {
			input: "a: &x {k: 1}\nb: *x\n",
			path:  "$..*..*",
			want:  []string{"$.a.k=1"},
		},
		"below each recursive match": {
			input: stringtest.LinesLF(
				"x:",
				"  env: {A: 1}",
				"y:",
				"  env: {B: [2]}",
			),
			path: "$..env..*",
			want: []string{"$.x.env.A=1", "$.y.env.B=[2]", "$.y.env.B[0]=2"},
		},
		"keys the decoder respells keep the names a selector matches": {
			input: stringtest.LinesLF("0x10: a", "3.10: b", "'q x': c"),
			path:  "$..*",
			want:  []string{"$.0x10=a", "$.'3.10'=b", "$.'q x'=c"},
		},
		"key with no name is left out with everything below it": {
			input: stringtest.LinesLF("*nope : {name: 1}", "b: {name: 2}"),
			path:  "$..*",
			want:  []string{"$.b={name: 2}", "$.b.name=2"},
		},
		"merged entries are not listed under the mapping that merges them": {
			input: stringtest.LinesLF("base: &b {k: 1}", "m: {<<: *b, j: 2}"),
			path:  "$..*",
			want:  []string{"$.base={k: 1}", "$.base.k=1", "$.m={<<: *b, j: 2}", "$.m.j=2"},
		},
		"inline merge source": {
			input: "m: {<<: {k: 1}, j: 2}\n",
			path:  "$..*",
			want:  []string{"$.m={<<: {k: 1}, j: 2}", "$.m.<<.k=1", "$.m.j=2"},
		},
		"sequence of merge sources": {
			input: stringtest.LinesLF(
				"b: &b {x: 1}",
				"m: {<<: [{k: 1}, *b], j: 2}",
			),
			path: "$..*",
			want: []string{"$.b={x: 1}", "$.b.x=1", "$.m={<<: [{k: 1}, *b], j: 2}", "$.m.<<[0].k=1", "$.m.j=2"},
		},
		"from the sources of a merge key": {
			input: stringtest.LinesLF("b: &b {x: 1}", "m: {<<: [{k: 1}, *b]}"),
			path:  "$.m.'<<'..*",
			want:  []string{"$.m.<<[0].k=1"},
		},
		"entry that a later merge key overrides": {
			input: stringtest.LinesLF("base: &b {k: 1}", "m: {k: 0, <<: *b}"),
			path:  "$..*",
			want:  []string{"$.base={k: 1}", "$.base.k=1", "$.m={k: 0, <<: *b}"},
		},
		"real key with the text of a merge key": {
			input: stringtest.LinesLF("x: &m '<<'", "m: {<<: {k: 1}, *m : 2}"),
			path:  "$..*",
			want:  []string{"$.x='<<'", "$.m={<<: {k: 1}, *m: 2}", "$.m.<<=2"},
		},
		"later duplicate key wins": {
			input: "a: {x: 1}\na: [2]\n",
			path:  "$..*",
			want:  []string{"$.a=[2]", "$.a[0]=2"},
			opts:  dups,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			doc := yamltest.FirstDocument(t, tc.input, tc.opts...).DocumentAST()
			matches := requireMatches(t, doc, tc.path)

			assert.Equal(t, tc.want, matchValues(matches))
		})
	}
}

func TestPath_Matches_WildcardErrors(t *testing.T) {
	t.Parallel()

	// A wildcard returns the error of an alias that does not resolve where
	// the alias could hold a node it lists. The path named lists no such
	// node, so it finds its one match, whose text is 1.
	tcs := map[string]struct {
		input string
		path  string
		named string
	}{
		// A merge key whose alias does not resolve could bring in any key,
		// so a `.*` selector returns the error even where a `.name`
		// selector finds a key of the mapping itself.
		"every entry over a merge alias with no anchor": {
			input: stringtest.LinesLF("m:", "  <<: *missing", "  a: 1"),
			path:  "$.m.*",
			named: "$.m.a",
		},
		"every entry over a merge list with an alias with no anchor": {
			input: stringtest.LinesLF(
				"b: &b {x: 1}",
				"m:",
				"  <<: [*b, *missing]",
				"  a: 1",
			),
			path:  "$.m.*",
			named: "$.m.a",
		},
		"every entry over a merge source that merges an alias with no anchor": {
			input: stringtest.LinesLF(
				"b: &b {<<: *missing, x: 1}",
				"m:",
				"  <<: *b",
				"  a: 1",
			),
			path:  "$.m.*",
			named: "$.m.a",
		},
		"every entry of an alias with no anchor": {
			input: "m: *missing\n",
			path:  "$.m.*",
		},
		// A `..*` selector lists every value below the node it starts
		// from, so an alias there that does not resolve is an error, where
		// a `..name` selector that lists no such value finds its entries.
		"every node over an alias with no anchor": {
			input: stringtest.LinesLF("a: {b: *missing}", "c: {name: 1}"),
			path:  "$..*",
			named: "$..name",
		},
		"every node below an alias with no anchor": {
			input: "m: *missing\n",
			path:  "$.m..*",
		},
		"every node over an alias inside its own anchor": {
			input: "a: &x [*x]\nc: {name: 1}\n",
			path:  "$..*",
			named: "$..name",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			doc := yamltest.FirstDocument(t, tc.input).DocumentAST()
			path := paths.MustParse(tc.path)

			_, err := paths.NewResolver(doc).Matches(path)
			require.ErrorIs(t, err, paths.ErrAlias)

			_, err = paths.NewResolver(doc).Nodes(path)
			require.ErrorIs(t, err, paths.ErrAlias)

			if tc.named == "" {
				return
			}

			named := requireMatches(t, doc, tc.named)
			require.Len(t, named, 1)
			assert.Equal(t, "1", named[0].Node.String())
		})
	}
}

// emptyDocument returns the last document of input, which the tests below
// write as an explicit "---" header with nothing under it.
func emptyDocument(t *testing.T, input string) *ast.DocumentNode {
	t.Helper()

	docs := niceyaml.NewSourceFromString(input).AllDocuments()
	require.NotEmpty(t, docs)

	node := docs[len(docs)-1].DocumentAST()
	require.Nil(t, node.Body, "the document should have no body")

	return node
}

func TestPath_EmptyDocument(t *testing.T) {
	t.Parallel()

	t.Run("the root is the null below the header", func(t *testing.T) {
		t.Parallel()

		// A parse that keeps comments makes a comment group below the
		// header the body of the document, which still holds no content.
		tcs := map[string]struct {
			input string
			line  int
		}{
			"header after a document": {input: "a: 1\n---\n", line: 2},
			"header over a comment":   {input: "---\n# comment\n", line: 1},
			"header over a comment after a document": {
				input: stringtest.LinesLF("a: 1", "---", "# comment"),
				line:  2,
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				docs := niceyaml.NewSourceFromString(tc.input).AllDocuments()
				require.NotEmpty(t, docs)

				doc := docs[len(docs)-1].DocumentAST()

				node, err := paths.NewResolver(doc).Node(paths.Doc())
				require.NoError(t, err)
				assert.Equal(t, ast.NullType, node.Type())

				// The key of the root is the same null.
				for _, path := range []paths.Path{paths.Doc(), paths.Doc().Key()} {
					tk, err := paths.NewResolver(doc).Token(path)
					require.NoError(t, err)
					assert.Equal(t, token.DocumentHeaderType, tk.Type)
					assert.Equal(t, tc.line, tk.Position.Line)
				}

				_, err = paths.NewResolver(doc).Node(paths.Doc().Child("a"))
				require.ErrorIs(t, err, paths.ErrNoDocument)
				require.ErrorIs(t, err, paths.ErrNotFound)
			})
		}
	})

	t.Run("a match keeps the path as given", func(t *testing.T) {
		t.Parallel()

		doc := emptyDocument(t, "---\n")

		for _, path := range []paths.Path{paths.Doc(), paths.Doc().Key()} {
			matches, err := paths.NewResolver(doc).Matches(path)
			require.NoError(t, err)
			require.Len(t, matches, 1)
			assert.Equal(t, path.String(), matches[0].Path.String())
		}
	})

	t.Run("a path with segments selects nothing", func(t *testing.T) {
		t.Parallel()

		doc := emptyDocument(t, "a: 1\n---\n")

		tcs := map[string]paths.Path{
			"child":         paths.Doc().Child("a"),
			"index":         paths.Doc().Index(0),
			"every element": paths.Doc().Child("items").IndexAll(),
			"every entry":   paths.Doc().ChildAll(),
			"every node":    paths.Doc().RecursiveAll(),
		}

		for name, path := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				nodes, err := paths.NewResolver(doc).Nodes(path)
				require.NoError(t, err)
				assert.Empty(t, nodes)

				matches, err := paths.NewResolver(doc).Matches(path)
				require.NoError(t, err)
				assert.Empty(t, matches)

				// Node takes only a path without a wildcard.
				_, err = paths.NewResolver(doc).Node(path)
				if !errors.Is(err, paths.ErrWildcard) {
					require.ErrorIs(t, err, paths.ErrNoDocument)
					require.ErrorIs(t, err, paths.ErrNotFound)
				}
			})
		}
	})

	t.Run("a document without a header reaches nothing", func(t *testing.T) {
		t.Parallel()

		_, err := paths.NewResolver(&ast.DocumentNode{}).Node(paths.Doc())
		require.ErrorIs(t, err, paths.ErrNoDocument)
	})

	t.Run("a document without a header lists nothing", func(t *testing.T) {
		t.Parallel()

		for _, path := range []paths.Path{paths.Doc(), paths.Doc().Child("items").IndexAll()} {
			nodes, err := paths.NewResolver(&ast.DocumentNode{}).Nodes(path)
			require.NoError(t, err, path)
			assert.Empty(t, nodes, path)
		}
	})

	t.Run("a nil document is an error", func(t *testing.T) {
		t.Parallel()

		for _, path := range []paths.Path{paths.Doc(), paths.Doc().Child("items").IndexAll()} {
			_, err := paths.NewResolver(nil).Nodes(path)
			require.ErrorIs(t, err, paths.ErrNoDocument, path)
			require.ErrorIs(t, err, paths.ErrNotFound, path)

			_, err = paths.NewResolver(nil).Matches(path)
			require.ErrorIs(t, err, paths.ErrNoDocument, path)
		}
	})
}
