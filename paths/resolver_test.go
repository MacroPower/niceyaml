package paths_test

import (
	"cmp"
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
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/paths"
)

// documentOf returns the first document of input. A table case that
// resolves in no document leaves its input empty, and documentOf returns
// a nil document for it.
func documentOf(t *testing.T, input string) *ast.DocumentNode {
	t.Helper()

	if input == "" {
		return nil
	}

	return yamltest.FirstDocument(t, input).DocumentAST()
}

func TestResolver_Node(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input string
		path  string
		want  string
		err   error
	}{
		"plain child": {
			input: stringtest.LinesLF("a:", "  b: x"),
			path:  "$.a.b",
			want:  "x",
		},
		"index": {
			input: "a: [x, y]\n",
			path:  "$.a[1]",
			want:  "y",
		},
		"sequence": {
			input: stringtest.LinesLF("a:", "  b: [1, 2]"),
			path:  "$.a.b",
			want:  "[1, 2]",
		},
		"alias": {
			input: "base: &b {k: x}\nref: *b\n",
			path:  "$.ref.k",
			want:  "x",
		},
		"key a merge brings in": {
			input: stringtest.LinesLF(
				"base: &b {k: x}",
				"m:",
				"  <<: *b",
				"  own: 1",
			),
			path: "$.m.k",
			want: "x",
		},
		"anchor a merge brings in again": {
			// The decoder reads the anchors of a merged mapping again at
			// the merge key, so &x one is the last &x before *x.
			input: stringtest.LinesLF(
				"base: &b",
				"  k: &x one",
				"other: &x two",
				"m:",
				"  <<: *b",
				"  v: *x",
			),
			path: "$.m.v",
			want: "one",
		},
		"anchor a merge brings in again for a later mapping": {
			input: stringtest.LinesLF(
				"base: &b",
				"  k: &x one",
				"other: &x two",
				"m:",
				"  <<: *b",
				"v: *x",
			),
			path: "$.v",
			want: "one",
		},
		"anchor a nested merge brings in again": {
			input: stringtest.LinesLF(
				"b1: &b1 {k: &x one}",
				"b2: &b2 {<<: *b1}",
				"other: &x two",
				"m:",
				"  <<: *b2",
				"  v: *x",
			),
			path: "$.m.v",
			want: "one",
		},
		"anchor of the later merge source": {
			input: stringtest.LinesLF(
				"a: &a {k: &x one}",
				"b: &b {k: &x two}",
				"other: &x three",
				"m:",
				"  <<: [*b, *a]",
				"  v: *x",
			),
			path: "$.m.v",
			want: "one",
		},
		"anchor of a later inline merge source": {
			input: stringtest.LinesLF(
				"base: &b {k: &x one}",
				"other: &x two",
				"m:",
				"  <<: [*b, {j: &x three}]",
				"  v: *x",
			),
			path: "$.m.v",
			want: "three",
		},
		"earlier anchor over the anchor of an inline merge source": {
			// The decoder records an anchor on a merge value only for the
			// aliases other merge keys name, so *m reads the value &m.
			input: stringtest.LinesLF("a: &m 1", "b: {<<: &m {k: 2}}", "c: *m"),
			path:  "$.c",
			want:  "1",
		},
		"earlier anchor over the anchor of an inline merge source in a sequence": {
			input: stringtest.LinesLF(
				"a: &m 1",
				"b: {<<: [&m {k: 2}]}",
				"c: *m",
			),
			path: "$.c",
			want: "1",
		},
		"earlier anchor over the anchor of a block merge source": {
			input: stringtest.LinesLF(
				"a: &m 1",
				"b:",
				"  <<: &m",
				"    k: 2",
				"c: *m",
			),
			path: "$.c",
			want: "1",
		},
		"earlier anchor over the anchor of a merge source a merge brings in again": {
			input: stringtest.LinesLF(
				"base: &b {x: {<<: &m {k: 2}}}",
				"a: &m 1",
				"d: {<<: *b}",
				"c: *m",
			),
			path: "$.c",
			want: "1",
		},
		"anchor of an inline merge source with no earlier anchor": {
			input: "b: {<<: &m {k: 2}}\nc: *m\n",
			path:  "$.c.k",
			want:  "2",
		},
		"value alias beside an anchor of an inline merge source": {
			input: stringtest.LinesLF(
				"a: &m {k: 1}",
				"b: {<<: &m {k: 2}}",
				"d: {<<: *m}",
				"c: *m",
			),
			path: "$.c.k",
			want: "1",
		},
		"merge alias to the anchor of an inline merge source": {
			input: stringtest.LinesLF(
				"a: &m {k: 1}",
				"b: {<<: &m {k: 2}}",
				"d: {<<: *m}",
				"c: *m",
			),
			path: "$.d.k",
			want: "2",
		},
		"anchor of a merge source that merges itself": {
			// The decoder rejects the merge of a mapping into itself. The
			// binding stops at the cycle and still counts &x one again.
			input: stringtest.LinesLF(
				"a: &a {k: &x one, i: {<<: *a}}",
				"other: &x two",
				"m:",
				"  <<: *a",
				"  v: *x",
			),
			path: "$.m.v",
			want: "one",
		},
		"anchor of a merge source after a merge alias in it binds": {
			// The first merge of *B reads &B before the walk binds its *C,
			// so a later merge of *B must read &B again to count &N cval.
			input: stringtest.LinesLF(
				"c: &C {x: &N cval}",
				"m:",
				"  <<:",
				"    - <<: *B",
				"    - &B",
				"      <<: *C",
				"n: &N other",
				"v:",
				"  <<: *B",
				"  w: *N",
			),
			path: "$.v.w",
			want: "cval",
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
		"mapping wildcard": {
			input: "a: {x: 1}\n",
			path:  "$.a.*",
			err:   paths.ErrWildcard,
		},
		"recursive wildcard": {
			input: "a: {x: 1}\n",
			path:  "$.a..*",
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

			node, err := paths.NewResolver(documentOf(t, tc.input)).Node(paths.MustParse(tc.path))
			if tc.err != nil {
				require.ErrorIs(t, err, tc.err)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, tc.want, node.String())
		})
	}
}

func TestResolver_NodeFrom(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		err   error
		input string
		from  string
		path  string
		want  string
	}{
		"child": {
			input: stringtest.LinesLF("a:", "  b:", "    c: x"),
			from:  "$.a",
			path:  "@.b.c",
			want:  "x",
		},
		"index": {
			input: "a: [x, [y, z]]\n",
			from:  "$.a",
			path:  "@[1][0]",
			want:  "y",
		},
		"current path selects the node itself": {
			input: stringtest.LinesLF("a:", "  b: x"),
			from:  "$.a.b",
			path:  "@",
			want:  "x",
		},
		"from a node an alias leads to": {
			input: stringtest.LinesLF("base: &b {k: {v: x}}", "ref: *b"),
			from:  "$.ref",
			path:  "@.k.v",
			want:  "x",
		},
		"key a merge brings in": {
			input: stringtest.LinesLF(
				"base: &b {k: x}",
				"m:",
				"  <<: *b",
				"  own: 1",
			),
			from: "$.m",
			path: "@.k",
			want: "x",
		},
		"from a tagged mapping": {
			input: "a: !!map {b: x}\n",
			from:  "$.a",
			path:  "@.b",
			want:  "x",
		},
		"key of an entry": {
			input: stringtest.LinesLF("a:", "  b: x"),
			from:  "$.a",
			path:  "@.b~",
			want:  "b",
		},
		"leading key selects the node itself": {
			input: stringtest.LinesLF("a:", "  b: x"),
			from:  "$.a.b",
			path:  "@~",
			want:  "x",
		},
		"unknown alias": {
			input: stringtest.LinesLF("a:", "  b: *nope"),
			from:  "$.a",
			path:  "@.b",
			err:   paths.ErrAlias,
		},
		"wildcard": {
			input: "a: [x, y]\n",
			from:  "$.a",
			path:  "@[*]",
			err:   paths.ErrWildcard,
		},
		"mapping wildcard": {
			input: "a: {x: 1}\n",
			from:  "$.a",
			path:  "@.*",
			err:   paths.ErrWildcard,
		},
		"recursive wildcard": {
			input: "a: {x: 1}\n",
			from:  "$.a",
			path:  "@..*",
			err:   paths.ErrWildcard,
		},
		"missing path": {
			input: stringtest.LinesLF("a:", "  b: x"),
			from:  "$.a",
			path:  "@.c",
			err:   paths.ErrNotFound,
		},
		"absolute path reads from the root": {
			input: stringtest.LinesLF("a:", "  b: x", "c: y"),
			from:  "$.a",
			path:  "$.c",
			want:  "y",
		},
		"absolute path misses where the node holds the key": {
			input: stringtest.LinesLF("a:", "  b: x"),
			from:  "$.a",
			path:  "$.b",
			err:   paths.ErrNotFound,
		},
		"doc selects the root": {
			input: "a: x\n",
			from:  "$.a",
			path:  "$",
			want:  "a: x",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			doc := yamltest.FirstDocument(t, tc.input).DocumentAST()

			r := paths.NewResolver(doc)
			from, path := paths.MustParse(tc.from), paths.MustParse(tc.path)

			start, err := r.Node(from)
			require.NoError(t, err)

			node, err := r.NodeFrom(start, path)
			if tc.err != nil {
				require.ErrorIs(t, err, tc.err)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, tc.want, node.String())

			// A `~` at the start of the path selects the start node, where
			// the joined path selects the key of its entry.
			if strings.HasPrefix(tc.path, "@~") {
				return
			}

			joined, err := r.Node(from.Join(path))
			require.NoError(t, err)
			assert.Same(t, joined, node)
		})
	}

	t.Run("nil node", func(t *testing.T) {
		t.Parallel()

		doc := yamltest.FirstDocument(t, "a: x\n").DocumentAST()

		r := paths.NewResolver(doc)

		for _, path := range []paths.Path{paths.Current(), paths.Current().Child("a"), paths.Current().Key()} {
			_, err := r.NodeFrom(nil, path)
			require.ErrorIs(t, err, paths.ErrNotFound, path.String())
		}

		// A `$` path reads from the root, so a nil node plays no part.
		node, err := r.NodeFrom(nil, paths.Doc().Child("a"))
		require.NoError(t, err)
		assert.Equal(t, "x", node.String())
	})
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
			input:    stringtest.LinesLF("a:", "  b: x"),
			path:     "$.a.b",
			want:     "x",
			wantLine: 2,
		},
		"key": {
			input:    stringtest.LinesLF("a:", "  b: x"),
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
			input: stringtest.LinesLF(
				"base: &b {k: x}",
				"m:",
				"  <<: *b",
				"  own: 1",
			),
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
		"mapping wildcard": {
			input: "a: {x: 1}\n",
			path:  "$.a.*~",
			err:   paths.ErrWildcard,
		},
		"recursive wildcard": {
			input: "a: {x: 1}\n",
			path:  "$..*~",
			err:   paths.ErrWildcard,
		},
		"missing path": {
			input: "a: x\n",
			path:  "$.b",
			err:   paths.ErrNotFound,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			doc := yamltest.FirstDocument(t, tc.input).DocumentAST()

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

func TestResolver_Nearest(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input string
		path  string
		// The path of the mapping that lacks the key, or "" when the
		// path has none.
		want string
	}{
		"key a mapping leaves out": {
			input: stringtest.LinesLF("server:", "  port: 81"),
			path:  "$.server.name",
			want:  "$.server",
		},
		"key the root leaves out": {
			input: stringtest.LinesLF("server:", "  port: 81"),
			path:  "$.name",
			want:  "$",
		},
		"key an element of a sequence leaves out": {
			input: stringtest.LinesLF(
				"servers:",
				"  - port: 80",
				"    name: a",
				"  - port: 81",
			),
			path: "$.servers[1].name",
			want: "$.servers[1]",
		},
		"several names below the mapping": {
			input: stringtest.LinesLF("server:", "  port: 81"),
			path:  "$.server.tls.cert.file",
			want:  "$.server",
		},
		"null stands where a mapping would": {
			input: "server:\n",
			path:  "$.server.name",
			want:  "$.server",
		},
		"explicit null": {
			input: "server: ~\n",
			path:  "$.server.name",
			want:  "$.server",
		},
		"empty flow mapping": {
			input: "{}\n",
			path:  "$.name",
			want:  "$",
		},
		"mapping behind an alias": {
			input: stringtest.LinesLF("base: &b {port: 81}", "server: *b"),
			path:  "$.server.name",
			want:  "$.server",
		},
		"mapping behind a tag": {
			input: "server: !srv {port: 81}\n",
			path:  "$.server.name",
			want:  "$.server",
		},
		"mapping a merge key fills": {
			input: stringtest.LinesLF(
				"base: &b {port: 81}",
				"server:",
				"  <<: *b",
			),
			path: "$.server.name",
			want: "$.server",
		},
		"path that resolves": {
			input: stringtest.LinesLF("server:", "  port: 81"),
			path:  "$.server.port",
		},
		"key a merge brings in": {
			input: stringtest.LinesLF(
				"base: &b {port: 81}",
				"server:",
				"  <<: *b",
			),
			path: "$.server.port",
		},
		"root": {
			input: stringtest.LinesLF("server:", "  port: 81"),
			path:  "$",
		},
		"name in a scalar": {
			input: "server: hello\n",
			path:  "$.server.name",
		},
		"name in a sequence": {
			input: "servers: [a, b]\n",
			path:  "$.servers.name",
		},
		"index past the end": {
			input: "servers: [a, b]\n",
			path:  "$.servers[5]",
		},
		"name below an index past the end": {
			input: stringtest.LinesLF("servers:", "  - port: 80"),
			path:  "$.servers[5].name",
		},
		"index below the missing key": {
			input: stringtest.LinesLF("server:", "  port: 81"),
			path:  "$.server.names[0]",
		},
		"key selector on the missing key": {
			input: stringtest.LinesLF("server:", "  port: 81"),
			path:  "$.server.name~",
		},
		"wildcard": {
			input: stringtest.LinesLF("servers:", "  - port: 80"),
			path:  "$.servers[*].name",
		},
		"mapping wildcard": {
			input: stringtest.LinesLF("servers:", "  web:", "    port: 80"),
			path:  "$.servers.*.name",
		},
		"mapping wildcard before the missing key": {
			input: stringtest.LinesLF("servers:", "  web:", "    port: 80"),
			path:  "$.*.web.name",
		},
		"recursive wildcard before the missing key": {
			input: stringtest.LinesLF("servers:", "  web:", "    port: 80"),
			path:  "$..*.name",
		},
		"recursive selector": {
			input: stringtest.LinesLF("server:", "  port: 81"),
			path:  "$..name",
		},
		"alias that does not resolve": {
			input: "server: *nope\n",
			path:  "$.server.name",
		},
		"document with no content": {
			input: "# only a comment\n",
			path:  "$.name",
		},
		"null document": {
			input: "---\n",
			path:  "$.name",
		},
		"nil document": {path: "$.name"},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			r := paths.NewResolver(documentOf(t, tc.input))
			path := paths.MustParse(tc.path)

			got, ok := r.Nearest(path)
			assert.Equal(t, tc.want != "", ok)

			if !ok {
				assert.Equal(t, paths.Path{}, got)

				return
			}

			assert.Equal(t, tc.want, got.String())
			assert.Equal(t, paths.MustParse(tc.want), got)

			// The mapping resolves and the path below it does not.
			_, err := r.Node(got)
			require.NoError(t, err)

			_, err = r.Node(path)
			require.ErrorIs(t, err, paths.ErrNotFound)

			rest, ok := path.CutPrefix(got)
			require.True(t, ok)
			assert.Positive(t, rest.Len())

			// The resolver reads an `@` path from the root, and the path
			// it returns starts at `$`.
			fromCurrent, ok := r.Nearest(paths.MustParse("@" + tc.path[1:]))
			require.True(t, ok)
			assert.Equal(t, got, fromCurrent)
		})
	}

	t.Run("leaves the path as it was", func(t *testing.T) {
		t.Parallel()

		doc := yamltest.FirstDocument(t, stringtest.LinesLF("server:", "  port: 81")).DocumentAST()
		path := paths.Doc().Child("server", "tls", "cert")

		near, ok := paths.NewResolver(doc).Nearest(path)
		require.True(t, ok)

		assert.Equal(t, "$.server.x", near.Child("x").String())
		assert.Equal(t, "$.server", near.String())
		assert.Equal(t, "$.server.tls.cert", path.String())
	})
}

func TestResolver_Matches(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input string
		path  string
		want  []string
		err   error
	}{
		"index all": {
			input: "a: [x, y]\n",
			path:  "$.a[*]",
			want:  []string{"$.a[0]=x", "$.a[1]=y"},
		},
		"recursive": {
			input: stringtest.LinesLF("a: {name: x}", "b: {c: {name: y}}"),
			path:  "$..name",
			want:  []string{"$.a.name=x", "$.b.c.name=y"},
		},
		"each alias to one anchor": {
			input: stringtest.LinesLF("base: &b {k: x}", "refs: [*b, *b]"),
			path:  "$.refs[*].k",
			want:  []string{"$.refs[0].k=x", "$.refs[1].k=x"},
		},
		"key a merge brings in": {
			input: stringtest.LinesLF(
				"base: &b {k: x}",
				"m:",
				"  <<: *b",
				"  own: 1",
			),
			path: "$.m.k",
			want: []string{"$.m.k=x"},
		},
		"key of each entry": {
			input: "a: {name: x}\nb: {name: y}\n",
			path:  "$..name~",
			want:  []string{"$.a.name~=name", "$.b.name~=name"},
		},
		"every entry of a mapping": {
			input: stringtest.LinesLF(
				"base: &b {k: x}",
				"m:",
				"  <<: *b",
				"  0x10: y",
			),
			path: "$.m.*",
			want: []string{"$.m.k=x", "$.m.0x10=y"},
		},
		"every entry of a sequence": {
			input: "a: [x, y]\n",
			path:  "$.a.*",
		},
		"every node below a mapping": {
			input: stringtest.LinesLF(
				"base: &b {k: x}",
				"m:",
				"  <<: *b",
				"  l: [y]",
			),
			path: "$.m..*",
			want: []string{"$.m.l=[y]", "$.m.l[0]=y"},
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
		"document of comments alone": {
			input: "# nothing\n",
			path:  "$.a[*]",
		},
		"root of a document of comments alone": {
			input: "# nothing\n",
			path:  "$",
		},
		"empty document below a header": {
			input: "---\n",
			path:  "$.a[*]",
		},
		"root of an empty document below a header": {
			input: "---\n",
			path:  "$",
			want:  []string{"$=null"},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			doc := yamltest.FirstDocument(t, tc.input).DocumentAST()

			matches, err := paths.NewResolver(doc).Matches(paths.MustParse(tc.path))
			if tc.err != nil {
				require.ErrorIs(t, err, tc.err)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, tc.want, matchValues(matches))
		})
	}
}

func TestResolver_SeveralPaths(t *testing.T) {
	t.Parallel()

	// One Resolver resolves every path, including one it resolved before,
	// to the node, token, matches, and error that a Resolver made for that
	// path alone finds.
	doc := yamltest.FirstDocument(t, stringtest.Input(`
		base: &base
		  name: shared
		items: [a, b]
		spec:
		  name: x
		ref: *base
		mixed:
		  <<: *base
		  extra: 1
		lit: {? &lt "<<" : 1}
		both:
		  *lt : {name: real}
		  <<: {name: merged}
		bad: *nope
	`)).DocumentAST()
	r := paths.NewResolver(doc)

	for _, expr := range []string{
		"$.spec.name",
		"$.items[1]",
		"$.ref.name",
		"$.mixed.name",
		"$.mixed.extra",
		"$.both.name",
		"$.both..name",
		"$.both.'<<'.name",
		"$.bad",
		"$.items[*]",
		"$..name",
		"$.mixed~",
		"$.spec.missing",
		"$.spec.name",
	} {
		p := paths.MustParse(expr)

		wantNode, wantErr := paths.NewResolver(doc).Node(p)
		gotNode, err := r.Node(p)

		if assertSameError(t, wantErr, err, expr) {
			assert.Same(t, wantNode, gotNode, expr)
		}

		wantToken, wantErr := paths.NewResolver(doc).Token(p)
		gotToken, err := r.Token(p)

		if assertSameError(t, wantErr, err, expr) {
			assert.Same(t, wantToken, gotToken, expr)
		}

		wantMatches, wantErr := paths.NewResolver(doc).Matches(p)
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
	// a Resolver made for that path alone finds.
	var sb strings.Builder

	sb.WriteString("base: &base {shared: 1}\n")

	for i := range 50 {
		fmt.Fprintf(&sb, "k%d: {<<: *base, own: %d}\n", i, i)
	}

	doc := yamltest.FirstDocument(t, sb.String()).DocumentAST()

	want := map[string]ast.Node{}

	for i := range 50 {
		for _, name := range []string{"own", "shared"} {
			p := paths.Doc().Child(fmt.Sprintf("k%d", i), name)

			node, err := paths.NewResolver(doc).Node(p)
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

// openMerges returns a document whose mapping a holds, under an anchor of
// its own, the mappings b0 and up, as many as links. Each bN merges the
// two mappings before it, b1 merges b0 and a, and b0 merges a and holds
// the anchor x.
func openMerges(links int) string {
	var sb strings.Builder

	sb.WriteString(stringtest.LinesLF(
		"a: &a",
		"  b0: &b0 {<<: *a, k: &x one}",
		"  b1: &b1 {<<: [*b0, *a]}",
	))

	for i := 2; i < links; i++ {
		fmt.Fprintf(&sb, "  b%d: &b%d {<<: [*b%d, *b%d]}\n", i, i, i-1, i-2)
	}

	return sb.String()
}

func TestResolver_NestedMerges(t *testing.T) {
	t.Parallel()

	// The mappings l0 through l39, where each later one merges the one
	// before it four times.
	var repeated strings.Builder

	repeated.WriteString(stringtest.LinesLF("l0: &l0 {k: &x one}", "other: &x two"))

	for i := 1; i < 40; i++ {
		fmt.Fprintf(&repeated, "l%d: &l%d {<<: [*l%d, *l%d, *l%d, *l%d]}\n", i, i, i-1, i-1, i-1, i-1)
	}

	repeated.WriteString("v: *x\n")

	// The alias at each path refers to the anchor &x one.
	tcs := map[string]struct {
		input string
		path  paths.Path
	}{
		// Reading every merge again would take 4^39 reads. The anchors of
		// each mapping count once per merge all the same.
		"merges of one mapping": {
			input: repeated.String(),
			path:  paths.Doc().Child("v"),
		},
		// No merge of a bN is final while the walk is inside a. Reading
		// every merge again would take about 1.6^60 reads.
		"merges inside an open anchor": {
			input: openMerges(60) + "  v: *x\n",
			path:  paths.Doc().Child("a", "v"),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			doc := yamltest.FirstDocument(t, tc.input).DocumentAST()
			nodes := make(chan ast.Node, 1)

			go func() {
				node, err := paths.NewResolver(doc).Node(tc.path)
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
		})
	}
}

// valueOfV returns the value of the entry v in the mapping at the root of
// doc, as the parser left it, so an alias there is still an alias.
func valueOfV(t *testing.T, doc *ast.DocumentNode) ast.Node {
	t.Helper()

	mapping, ok := doc.Body.(*ast.MappingNode)
	require.True(t, ok, "body is a %T", doc.Body)

	var value ast.Node

	for _, entry := range mapping.Values {
		if entry.Key.GetToken().Value == "v" {
			value = entry.Value
		}
	}

	require.NotNil(t, value, "no entry v")

	return value
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
			input: stringtest.LinesLF("a: &a one", "b: &a two", "v: *a"),
			want:  "two",
		},
		"anchor a merge brings in again": {
			input: stringtest.LinesLF(
				"base: &b",
				"  k: &x one",
				"other: &x two",
				"m:",
				"  <<: *b",
				"v: *x",
			),
			want: "one",
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
		"tagged alias": {
			input: "a: &a {k: x}\nv: !t *a\n",
			want:  "!t *a",
		},
		"alias with no anchor before it": {
			input: "v: *a\na: &a x\n",
			err:   paths.ErrAlias,
		},
		"tagged alias with no anchor before it": {
			input: "v: !t *a\na: &a x\n",
			err:   paths.ErrAlias,
		},
		"alias to a tagged alias with no anchor before it": {
			input: stringtest.LinesLF("a: &a !t *b", "v: *a", "b: &b x"),
			err:   paths.ErrAlias,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			doc := yamltest.FirstDocument(t, tc.input).DocumentAST()

			got, err := paths.NewResolver(doc).Deref(valueOfV(t, doc))
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

	t.Run("nil content", func(t *testing.T) {
		t.Parallel()

		// A tree built by hand may hold a typed nil where the parser always
		// puts a node. The alias here refers to an anchor over one.
		anchorName := &ast.StringNode{Token: &token.Token{Value: "a"}, Value: "a"}
		alias := &ast.AliasNode{Value: anchorName}
		doc := &ast.DocumentNode{Body: &ast.SequenceNode{
			BaseNode: &ast.BaseNode{},
			Values: []ast.Node{
				&ast.AnchorNode{Name: anchorName, Value: (*ast.MappingNode)(nil)},
				alias,
			},
		}}

		tcs := map[string]struct {
			doc  *ast.DocumentNode
			node ast.Node
		}{
			"nil node":                {node: nil},
			"typed nil":               {node: (*ast.StringNode)(nil)},
			"anchor over a typed nil": {node: &ast.AnchorNode{Value: (*ast.MappingNode)(nil)}},
			"alias to an anchor over a typed nil": {
				doc:  doc,
				node: alias,
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				got, err := paths.NewResolver(tc.doc).Deref(tc.node)
				require.NoError(t, err)

				// Compare against an untyped nil, since assert.Nil also
				// accepts a typed nil.
				assert.Equal(t, ast.Node(nil), got)
			})
		}
	})
}

func TestResolver_KeyName(t *testing.T) {
	t.Parallel()

	// Each case names the key of the first entry of the mapping at $.m. A
	// child selector with that name selects the value v, or the value the
	// case sets.
	tcs := map[string]struct {
		input string
		want  string
		value string
		ok    bool
	}{
		"plain": {
			input: "m: {k: v}\n",
			want:  "k",
			ok:    true,
		},
		"hexadecimal int": {
			input: "m: {0x10: v}\n",
			want:  "0x10",
			ok:    true,
		},
		"quoted empty": {
			input: "m: {\"\": v}\n",
			want:  "",
			ok:    true,
		},
		"block scalar": {
			input: stringtest.LinesLF("m:", "  ? |-", "    k", "  : v"),
			want:  "k",
			ok:    true,
		},
		"anchor and tag": {
			input: stringtest.LinesLF("m:", "  &a !!str k: v"),
			want:  "k",
			ok:    true,
		},
		"alias": {
			input: stringtest.LinesLF("base: &k n", "m:", "  *k : v"),
			want:  "n",
			ok:    true,
		},
		"tagged alias": {
			input: stringtest.LinesLF("base: &k 0x10", "m:", "  !!int *k : v"),
			want:  "0x10",
			ok:    true,
		},
		"alias to a tagged alias": {
			input: stringtest.LinesLF(
				"base: &k 0x10",
				"b: &j !!int *k",
				"m:",
				"  *j : v",
			),
			want: "0x10",
			ok:   true,
		},
		"alias with no anchor before it": {input: stringtest.LinesLF("m:", "  *k : v", "base: &k n")},
		"key a later merge key brings in": {
			// The merge comes after the entry, so the selector selects
			// the value the merge brings in.
			input: stringtest.LinesLF("m:", "  k: own", "  <<: {k: merged}"),
			want:  "k",
			ok:    true,
			value: "merged",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			doc := yamltest.FirstDocument(t, tc.input).DocumentAST()

			r := paths.NewResolver(doc)
			m := paths.Doc().Child("m")

			node, err := r.Node(m)
			require.NoError(t, err)

			mapping, ok := node.(*ast.MappingNode)
			require.True(t, ok, "m is a %T", node)
			require.NotEmpty(t, mapping.Values)

			got, ok := r.KeyName(mapping.Values[0].Key)
			assert.Equal(t, tc.ok, ok)
			assert.Equal(t, tc.want, got)

			if !tc.ok {
				return
			}

			value, err := r.Node(m.Child(got))
			require.NoError(t, err)
			assert.Equal(t, cmp.Or(tc.value, "v"), value.String())
		})
	}

	// The parser rejects a sequence or mapping key, but a tree built by
	// hand may hold one.
	handBuilt := map[string]ast.Node{
		"nil":               nil,
		"typed nil":         (*ast.StringNode)(nil),
		"sequence":          &ast.SequenceNode{BaseNode: &ast.BaseNode{}},
		"explicit sequence": &ast.MappingKeyNode{Value: &ast.SequenceNode{BaseNode: &ast.BaseNode{}}},
		"mapping":           &ast.MappingNode{BaseNode: &ast.BaseNode{}},
	}

	for name, key := range handBuilt {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, ok := paths.NewResolver(nil).KeyName(key)
			assert.False(t, ok)
			assert.Empty(t, got)
		})
	}
}

func TestResolver_Entry(t *testing.T) {
	t.Parallel()

	// Each case reads the entry name selects in the mapping at $.m, and
	// names the entry by the text of its value.
	tcs := map[string]struct {
		input string
		name  string
		opts  []niceyaml.SourceOption
		want  string
		err   error
	}{
		"own key": {
			input: "m: {x: 1}\n",
			name:  "x",
			want:  "1",
		},
		"merged key": {
			input: "a: &a {x: 1}\nm: {<<: *a}\n",
			name:  "x",
			want:  "1",
		},
		"own key after the merge key": {
			input: "m: {<<: {x: 1}, x: 2}\n",
			name:  "x",
			want:  "2",
		},
		"merge key after the own key": {
			input: "m: {x: 1, <<: {x: 2}}\n",
			name:  "x",
			want:  "2",
		},
		"later source of one merge key": {
			input: "m: {<<: [{x: 1}, {x: 2}]}\n",
			name:  "x",
			want:  "2",
		},
		"later merge key": {
			input: "m: {<<: {x: 1}, <<: {x: 2}}\n",
			name:  "x",
			opts:  []niceyaml.SourceOption{niceyaml.WithAllowDuplicateKeys(true)},
			want:  "2",
		},
		"alias key": {
			input: stringtest.LinesLF("k: &k x", "m:", "  *k : 1"),
			name:  "x",
			want:  "1",
		},
		"key text rather than member name": {
			input: "m: {0x10: 1}\n",
			name:  "0x10",
			want:  "1",
		},
		"member name of a respelled key": {
			input: "m: {0x10: 1}\n",
			name:  "16",
			err:   paths.ErrNotFound,
		},
		"missing name": {
			input: "m: {x: 1}\n",
			name:  "y",
			err:   paths.ErrNotFound,
		},
		"not a mapping": {
			input: "m: [x]\n",
			name:  "x",
			err:   paths.ErrNotFound,
		},
		"unknown alias in a merge key": {
			input: "m: {<<: *nope}\n",
			name:  "x",
			err:   paths.ErrAlias,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			doc := yamltest.FirstDocument(t, tc.input, tc.opts...).DocumentAST()

			r := paths.NewResolver(doc)
			m := paths.MustParse("$.m")

			node, err := r.Node(m)
			require.NoError(t, err)

			got, err := r.Entry(node, tc.name)
			if tc.err != nil {
				require.ErrorIs(t, err, tc.err)
				assert.Nil(t, got)

				return
			}

			require.NoError(t, err)

			entry, ok := got.(*ast.MappingValueNode)
			require.True(t, ok, "entry is a %T", got)
			assert.Equal(t, tc.want, entry.Value.String())

			// A child selector with the name selects the value of the
			// entry.
			value, err := r.Node(m.Child(tc.name))
			require.NoError(t, err)
			assert.Same(t, entry.Value, value)
		})
	}
}

func TestEntryFinder_Entry(t *testing.T) {
	t.Parallel()

	// Each document is that of [yamltest.WideMerge] with the key last
	// after the merge key of the mapping m. A lookup of each key before the
	// merge key reads its whole list, and a lookup of last reads no merge
	// key.
	tcs := map[string]struct {
		err  error
		keys int
	}{
		"few merge sources": {keys: 10},
		"many merge sources": {
			keys: 2000,
			err:  paths.ErrExcessiveMerging,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			doc := yamltest.FirstDocument(t, yamltest.WideMerge(tc.keys)+"  last: x\n").DocumentAST()

			r := paths.NewResolver(doc)

			m, err := r.Node(paths.MustParse("$.m"))
			require.NoError(t, err)

			finder := r.EntryFinder()

			// The lookups share one limit, so no lookup of these keys
			// finds its entry after the one that passes the limit.
			found := 0

			for i := range tc.keys {
				entry, err := finder.Entry(m, fmt.Sprintf("%#x", 0x1000+i))
				if err != nil {
					require.ErrorIs(t, err, tc.err)
					assert.Nil(t, entry)

					continue
				}

				require.Equal(t, i, found, "lookup of key %d found its entry past the limit", i)

				found++
			}

			if tc.err == nil {
				assert.Equal(t, tc.keys, found)
			} else {
				assert.Positive(t, found)
				assert.Less(t, found, tc.keys)
			}

			// A lookup that reads no merge key finds its entry past the
			// limit.
			got, err := finder.Entry(m, "last")
			require.NoError(t, err)

			entry, ok := got.(*ast.MappingValueNode)
			require.True(t, ok, "entry is a %T", got)
			assert.Equal(t, "x", entry.Value.String())

			// A call on the resolver and a new finder each count their
			// own reads.
			key := fmt.Sprintf("%#x", 0x1000+tc.keys-1)

			_, err = r.Entry(m, key)
			require.NoError(t, err)

			_, err = r.EntryFinder().Entry(m, key)
			require.NoError(t, err)
		})
	}
}

func TestResolver_Anchor(t *testing.T) {
	t.Parallel()

	// Each case looks up the anchor of the value of the entry v.
	tcs := map[string]struct {
		input string
		want  string
		line  int
		err   error
	}{
		"alias": {
			input: "a: &a {k: x}\nv: *a\n",
			want:  "&a {k: x}",
			line:  1,
		},
		"last anchor of its name": {
			input: stringtest.LinesLF("a: &a one", "b: &a two", "v: *a"),
			want:  "&a two",
			line:  2,
		},
		"anchor a merge brings in again": {
			input: stringtest.LinesLF(
				"base: &b",
				"  k: &x one",
				"other: &x two",
				"m:",
				"  <<: *b",
				"v: *x",
			),
			want: "&x one",
			line: 2,
		},
		"anchor that holds an alias": {
			// Deref goes on to the content of b, while Anchor stops at a.
			input: stringtest.LinesLF("b: &b one", "a: &a", "  *b", "v: *a"),
			want:  "&a *b",
			line:  2,
		},
		"alias with no anchor before it": {
			input: "v: *a\na: &a x\n",
			err:   paths.ErrAlias,
		},
		"not an alias": {
			input: "v: &a x\n",
			err:   paths.ErrAlias,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			doc := yamltest.FirstDocument(t, tc.input).DocumentAST()

			got, err := paths.NewResolver(doc).Anchor(valueOfV(t, doc))
			if tc.err != nil {
				require.ErrorIs(t, err, tc.err)

				return
			}

			require.NoError(t, err)

			anchor, ok := got.(*ast.AnchorNode)
			require.True(t, ok, "got a %T", got)
			assert.Equal(t, tc.want, anchor.String())
			assert.Equal(t, tc.line, anchor.GetToken().Position.Line)
		})
	}

	t.Run("anchors that share their content", func(t *testing.T) {
		t.Parallel()

		// The parser gives each anchor its own content, but a tree built by
		// hand may give two anchors one content node.
		doc := yamltest.FirstDocument(t, stringtest.LinesLF("a: &x 1", "b: &y 2", "v: *x")).DocumentAST()

		mapping, ok := doc.Body.(*ast.MappingNode)
		require.True(t, ok, "body is a %T", doc.Body)
		require.Len(t, mapping.Values, 3)

		x, ok := mapping.Values[0].Value.(*ast.AnchorNode)
		require.True(t, ok, "a holds a %T", mapping.Values[0].Value)

		y, ok := mapping.Values[1].Value.(*ast.AnchorNode)
		require.True(t, ok, "b holds a %T", mapping.Values[1].Value)

		y.Value = x.Value

		got, err := paths.NewResolver(doc).Anchor(mapping.Values[2].Value)
		require.NoError(t, err)
		assert.Same(t, x, got)
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
			input: stringtest.LinesLF("a: &a {x: 1}", "m: {<<: *a, own: 1}"),
			want:  []string{"x"},
		},
		"sequence of mappings": {
			input: stringtest.LinesLF(
				"a: &a {x: 1}",
				"b: &b {y: 2}",
				"m: {<<: [*a, *b]}",
			),
			want: []string{"x", "y"},
		},
		"alias to a sequence": {
			input: stringtest.LinesLF(
				"a: &a {x: 1}",
				"b: &b {y: 2}",
				"l: &l [*a, *b]",
				"m: {<<: *l}",
			),
			want: []string{"x", "y"},
		},
		"repeated merge keys": {
			input: stringtest.LinesLF(
				"a: &a {x: 1}",
				"b: &b {y: 2}",
				"m: {<<: *b, own: 1, <<: *a}",
			),
			opts: []niceyaml.SourceOption{niceyaml.WithAllowDuplicateKeys(true)},
			want: []string{"y", "x"},
		},
		"no merge key":  {input: "m: {k: x}\n"},
		"not a mapping": {input: "m: [x, y]\n"},
		"unknown alias": {
			input: "m: {<<: *nope}\n",
			err:   paths.ErrAlias,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			doc := yamltest.FirstDocument(t, tc.input, tc.opts...).DocumentAST()

			r := paths.NewResolver(doc)

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
	source := mapNode(mapEntry(strNode("k"), strNode("x")))

	tcs := map[string]struct {
		key  ast.MapKeyNode
		want int
	}{
		"explicit key wrapping a typed nil anchor": {key: &ast.MappingKeyNode{Value: (*ast.AnchorNode)(nil)}},
		"anchor key wrapping a typed nil tag":      {key: &ast.AnchorNode{Value: (*ast.TagNode)(nil)}},
		"typed nil explicit key":                   {key: (*ast.MappingKeyNode)(nil)},
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
