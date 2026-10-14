package paths_test

import (
	"cmp"
	"encoding/json"
	"slices"
	"testing"
	"unicode/utf8"

	"github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/ast"
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

// assertPathText asserts that path writes the expression want, through
// String and MarshalText, and that Parse and UnmarshalText read path back
// from it. The path is absolute when want starts at `$`.
func assertPathText(t *testing.T, path paths.Path, want string) {
	t.Helper()

	assert.Equal(t, want, path.String())
	assert.Equal(t, want[0] == '$', path.IsAbsolute())

	text, err := path.MarshalText()
	require.NoError(t, err)
	assert.Equal(t, want, string(text))

	assert.Equal(t, path, paths.MustParse(want))

	// A path set before the call must not show through.
	got := paths.Doc().Child("before")

	require.NoError(t, got.UnmarshalText(text))
	assert.Equal(t, path, got)
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
		"zero value": {
			want:     "@",
			wantYAML: "$",
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
		// A builder given nothing to append returns the path it extends.
		"child with no names": {
			path: paths.Doc().Child(),
			want: "$",
		},
		"index with no indices": {
			path: paths.Doc().Index(),
			want: "$",
		},
		"no names after a child": {
			path: paths.Doc().Child("a").Child(),
			want: "$.a",
		},
		"current with no names": {
			path:     paths.Current().Child(),
			want:     "@",
			wantYAML: "$",
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
		"names with reserved characters in one path": {
			path:     paths.Doc().Child("a.b", "it's", "", "*").Recursive("x y"),
			want:     `$.'a.b'.'it\'s'.''.'*'..'x y'`,
			wantYAML: "$.'a.b'.it's..'*'..x y",
		},
		"name star is a child selector": {
			path: paths.Doc().Child("jobs", "*"),
			want: "$.jobs.'*'",
		},
		"recursive name star is a recursive selector": {
			path:     paths.Doc().Recursive("*"),
			want:     "$..'*'",
			wantYAML: "$..*",
		},
		"name that is not valid UTF-8": {
			path: paths.Doc().Child("\xff"),
			want: "$.\xff",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assertPathText(t, tc.path, tc.want)

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

func TestPath_YAMLPath_Wildcards(t *testing.T) {
	t.Parallel()

	// The goccy syntax has no selector for every entry of a mapping, and a
	// path without it would name the mapping. The goccy syntax takes a name
	// alone after `..`, so it has no form for every node below one either.
	tcs := map[string]struct {
		path paths.Path
		want string
	}{
		"mapping wildcard after a child": {
			path: paths.Doc().Child("jobs").ChildAll(),
			want: "$.jobs.*",
		},
		"mapping wildcard on the root": {
			path: paths.Doc().ChildAll(),
			want: "$.*",
		},
		"mapping wildcard then a child": {
			path: paths.Doc().Child("jobs").ChildAll().Child("steps"),
			want: "$.jobs.*.steps",
		},
		"mapping wildcard then index all": {
			path: paths.Doc().Child("jobs").ChildAll().Child("steps").IndexAll().Child("uses"),
			want: "$.jobs.*.steps[*].uses",
		},
		"mapping wildcard then a key selector": {
			path: paths.Doc().Child("jobs").ChildAll().Key(),
			want: "$.jobs.*~",
		},
		"mapping wildcard twice": {
			path: paths.Doc().Child("paths").ChildAll().ChildAll(),
			want: "$.paths.*.*",
		},
		"mapping wildcard after index all": {
			path: paths.Doc().Child("items").IndexAll().ChildAll(),
			want: "$.items[*].*",
		},
		"mapping wildcard after a recursive selector": {
			path: paths.Doc().Recursive("env").ChildAll(),
			want: "$..env.*",
		},
		"mapping wildcard after the name star": {
			path: paths.Doc().Child("*").ChildAll(),
			want: "$.'*'.*",
		},
		"recursive wildcard on the root": {
			path: paths.Doc().RecursiveAll(),
			want: "$..*",
		},
		"recursive wildcard after a child": {
			path: paths.Doc().Child("spec").RecursiveAll(),
			want: "$.spec..*",
		},
		"recursive wildcard then a child": {
			path: paths.Doc().RecursiveAll().Child("name"),
			want: "$..*.name",
		},
		"recursive wildcard then an index": {
			path: paths.Doc().RecursiveAll().Index(0),
			want: "$..*[0]",
		},
		"recursive wildcard then a key selector": {
			path: paths.Doc().RecursiveAll().Key(),
			want: "$..*~",
		},
		"recursive wildcard twice": {
			path: paths.Doc().RecursiveAll().RecursiveAll(),
			want: "$..*..*",
		},
		"recursive wildcard after index all": {
			path: paths.Doc().Child("items").IndexAll().RecursiveAll(),
			want: "$.items[*]..*",
		},
		"recursive wildcard after a mapping wildcard": {
			path: paths.Doc().ChildAll().RecursiveAll(),
			want: "$.*..*",
		},
		"recursive wildcard after a recursive selector": {
			path: paths.Doc().Recursive("env").RecursiveAll(),
			want: "$..env..*",
		},
		"recursive wildcard after the recursive name star": {
			path: paths.Doc().Recursive("*").RecursiveAll(),
			want: "$..'*'..*",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assertPathText(t, tc.path, tc.want)

			// YAMLPath names the path it cannot convert, and the goccy
			// parser rejects the text.
			yp, err := tc.path.YAMLPath()
			require.ErrorIs(t, err, paths.ErrNoYAMLPath)
			require.EqualError(t, err, "convert "+tc.want+": selector has no goccy/go-yaml equivalent")
			assert.Nil(t, yp)

			_, err = yaml.PathString(tc.want)
			require.Error(t, err)
		})
	}
}

func TestPath_Immutable(t *testing.T) {
	t.Parallel()

	// The document Nearest reads in the case that derives its path.
	server := documentOf(t, stringtest.LinesLF("server:", "  port: 81"))

	// Each case derives a path from the paths in from. Extending the
	// derived path, or a path it came from, changes none of the others.
	tcs := map[string]struct {
		derive func(from []paths.Path) (paths.Path, bool)
		want   string
		from   []paths.Path
	}{
		"child of a path": {
			from:   []paths.Path{paths.Doc().Child("spec")},
			derive: func(from []paths.Path) (paths.Path, bool) { return from[0].Child("replicas"), true },
			want:   "$.spec.replicas",
		},
		"child of a longer path": {
			from:   []paths.Path{paths.Doc().Child("metadata", "name")},
			derive: func(from []paths.Path) (paths.Path, bool) { return from[0].Child("labels"), true },
			want:   "$.metadata.name.labels",
		},
		"join": {
			from:   []paths.Path{paths.Doc().Child("spec"), paths.Current().Child("open")},
			derive: func(from []paths.Path) (paths.Path, bool) { return from[0].Join(from[1]), true },
			want:   "$.spec.open",
		},
		"join with an absolute path that replaces the receiver": {
			from: []paths.Path{
				paths.Doc().Child("spec"),
				paths.Doc().Child("name"),
				paths.Current().Child("first"),
			},
			derive: func(from []paths.Path) (paths.Path, bool) { return from[0].Join(from[1:]...), true },
			want:   "$.name.first",
		},
		"cut prefix": {
			from:   []paths.Path{paths.Doc().Child("spec", "hours", "open"), paths.Doc().Child("spec")},
			derive: func(from []paths.Path) (paths.Path, bool) { return from[0].CutPrefix(from[1]) },
			want:   "@.hours.open",
		},
		"parent": {
			from:   []paths.Path{paths.Doc().Child("spec", "hours", "open")},
			derive: func(from []paths.Path) (paths.Path, bool) { return from[0].Parent() },
			want:   "$.spec.hours",
		},
		"nearest mapping": {
			from:   []paths.Path{paths.Doc().Child("server", "tls", "cert")},
			derive: func(from []paths.Path) (paths.Path, bool) { return paths.NewResolver(server).Nearest(from[0]) },
			want:   "$.server",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			before := make([]string, 0, len(tc.from))
			for _, p := range tc.from {
				before = append(before, p.String())
			}

			derived, ok := tc.derive(tc.from)
			require.True(t, ok)

			assert.Equal(t, tc.want+".x", derived.Child("x").String())

			for i, p := range tc.from {
				assert.Equal(t, before[i]+".y", p.Child("y").String())
			}

			assert.Equal(t, tc.want, derived.String())

			for i, p := range tc.from {
				assert.Equal(t, before[i], p.String())
			}
		})
	}
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
		"current to current": {
			p:    paths.Current(),
			qs:   []paths.Path{paths.Current()},
			want: "@",
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

			assertPathText(t, tc.p.Join(tc.qs...), tc.want)
		})
	}
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
			assertPathText(t, got, tc.want)

			if ok {
				assert.Equal(t, tc.p, tc.prefix.Join(got))
			}
		})
	}
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
			assertPathText(t, got, tc.want)
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

func TestPath_YAMLPath(t *testing.T) {
	t.Parallel()

	doc := documentOf(t, stringtest.Input(`
		a:
		  'b.c': [x, y]
		  don't: 1
		  a\.b: 2
		  a.b'c: 3
		'':
		  a'.b: 4
		  a'*b: 5
		  a.b: 6
	`))

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
			src:  stringtest.LinesLF(`"": v`),
			path: paths.Doc().Child(""),
			want: stringtest.LinesLF(`"": 9`),
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

			replacement := documentOf(t, "9").Body

			require.NoError(t, yamlPath(t, tc.path).ReplaceWithNode(file, replacement))
			assert.Equal(t, tc.want, file.String())
		})
	}
}

func TestPath_YAMLPath_Limits(t *testing.T) {
	t.Parallel()

	t.Run("goccy strips single quotes from a name", func(t *testing.T) {
		t.Parallel()

		doc := documentOf(t, stringtest.LinesLF("x: plain", `"'x'": quoted`))

		assert.Equal(t, "plain", goccyValue(t, paths.Doc().Child("'x'"), doc))
	})

	t.Run("goccy drops a key selector mid-path", func(t *testing.T) {
		t.Parallel()

		doc := documentOf(t, stringtest.LinesLF("a:", "  b: 1"))
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
				input: stringtest.LinesLF(
					"b: &k k",
					"m:",
					"  *k : v",
				),
				indicator: "*",
			},
			"explicit key": {
				input: stringtest.LinesLF(
					"m:",
					"  ? k",
					"  : v",
				),
				indicator: "?",
			},
			"explicit block scalar key": {
				input: stringtest.LinesLF(
					"m:",
					"  ? |-",
					"    k",
					"  : v",
				),
				indicator: "?",
			},
			"block scalar key without ?": {
				input: stringtest.LinesLF(
					"m:",
					"  |-",
					"    k",
					"  : v",
				),
				indicator: "|-",
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				doc := documentOf(t, tc.input)
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

		doc := documentOf(t, stringtest.LinesLF(
			"base: &b",
			"  k: v",
			"m:",
			"  <<: *b",
		))
		path := paths.Doc().Child("m", "k")

		node, err := paths.NewResolver(doc).Node(path)
		require.NoError(t, err)
		assert.Equal(t, "v", node.GetToken().Value)
		assert.Empty(t, goccyValue(t, path, doc))
	})

	t.Run("goccy selects the first duplicate and replaces all", func(t *testing.T) {
		t.Parallel()

		input := stringtest.LinesLF(
			"m:",
			"  k: first",
			"  k: last",
		)

		file, err := niceyaml.NewSourceFromString(input, niceyaml.WithAllowDuplicateKeys(true)).File()
		require.NoError(t, err)

		path := paths.Doc().Child("m", "k")

		node, err := paths.NewResolver(file.Docs[0]).Node(path)
		require.NoError(t, err)
		assert.Equal(t, "last", node.GetToken().Value)
		assert.Equal(t, "first", goccyValue(t, path, file.Docs[0]))

		replacement := documentOf(t, "new\n").Body

		require.NoError(t, yamlPath(t, path).ReplaceWithNode(file, replacement))
		assert.Equal(t, stringtest.LinesLF(
			"m:",
			"  k: new",
			"  k: new",
		), file.String())
	})
}
