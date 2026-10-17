package paths_test

import (
	"cmp"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

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

var (
	// A merge key gives the mapping m the key k of base.
	mergedKeyDoc = stringtest.LinesLF(
		"base: &b {k: x}",
		"m:",
		"  <<: *b",
		"  own: 1",
	)

	// Two anchors hold the name x. The decoder reads the anchors of a
	// merged mapping again at the merge key, so the alias under v refers
	// to the x of base.
	mergedAnchorDoc = stringtest.LinesLF(
		"base: &b",
		"  k: &x one",
		"other: &x two",
		"m:",
		"  <<: *b",
		"v: *x",
	)
)

// documentOf returns the first document of input, from a source created
// with opts. A table case that resolves in no document leaves its input
// empty, and documentOf returns a nil document for it.
func documentOf(tb testing.TB, input string, opts ...niceyaml.SourceOption) *ast.DocumentNode {
	tb.Helper()

	if input == "" {
		return nil
	}

	return yamltest.FirstDocument(tb, input, opts...).DocumentAST()
}

// requireAs returns node as a T. The test fails when node is not a T.
func requireAs[T ast.Node](t *testing.T, node ast.Node) T {
	t.Helper()

	got, ok := node.(T)
	require.True(t, ok, "want %T, got %T", *new(T), node)

	return got
}

// requireNoDocument asserts that err reports a document that holds no
// content to resolve in.
func requireNoDocument(t *testing.T, err error, msgAndArgs ...any) {
	t.Helper()

	require.ErrorIs(t, err, paths.ErrNoDocument, msgAndArgs...)
	require.ErrorIs(t, err, paths.ErrNotFound, msgAndArgs...)
}

func TestResolver_Node(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input string
		path  string
		want  string
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
			input: mergedKeyDoc,
			path:  "$.m.k",
			want:  "x",
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
			input: mergedAnchorDoc,
			path:  "$.v",
			want:  "one",
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
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			node, err := paths.NewResolver(documentOf(t, tc.input)).Node(paths.MustParse(tc.path))
			require.NoError(t, err)
			assert.Equal(t, tc.want, node.String())
		})
	}
}

func TestResolver_NodeFrom(t *testing.T) {
	t.Parallel()

	nested := stringtest.LinesLF("a:", "  b: x")

	tcs := map[string]struct {
		err   error
		input string
		from  string
		path  string
		want  string
	}{
		"child": {
			input: stringtest.LinesLF(
				"a:",
				"  b:",
				"    c: x",
			),
			from: "$.a",
			path: "@.b.c",
			want: "x",
		},
		"index": {
			input: "a: [x, [y, z]]\n",
			from:  "$.a",
			path:  "@[1][0]",
			want:  "y",
		},
		"current path selects the node itself": {
			input: nested,
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
			input: mergedKeyDoc,
			from:  "$.m",
			path:  "@.k",
			want:  "x",
		},
		"from a tagged mapping": {
			input: "a: !!map {b: x}\n",
			from:  "$.a",
			path:  "@.b",
			want:  "x",
		},
		"key of an entry": {
			input: nested,
			from:  "$.a",
			path:  "@.b~",
			want:  "b",
		},
		"leading key selects the node itself": {
			input: nested,
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
		"missing path": {
			input: nested,
			from:  "$.a",
			path:  "@.c",
			err:   paths.ErrNotFound,
		},
		"absolute path reads from the root": {
			input: stringtest.LinesLF(
				"a:",
				"  b: x",
				"c: y",
			),
			from: "$.a",
			path: "$.c",
			want: "y",
		},
		"absolute path misses where the node holds the key": {
			input: nested,
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

			doc := documentOf(t, tc.input)

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

		doc := documentOf(t, "a: x\n")

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

func TestResolver_Wildcard(t *testing.T) {
	t.Parallel()

	// Node, NodeFrom, and Token take only a path without a wildcard, which
	// needs Nodes. NodeFrom reads the `@` form of the path from the root.
	tcs := map[string]struct {
		input string
		path  string
	}{
		"sequence wildcard":  {input: "a: [x, y]\n", path: "$.a[*]"},
		"mapping wildcard":   {input: "a: {x: 1}\n", path: "$.a.*"},
		"recursive wildcard": {input: "a: {x: 1}\n", path: "$.a..*"},
		"recursive name":     {input: "a: [x, y]\n", path: "$..a"},
		"key of every entry": {input: "a: {x: 1}\n", path: "$.a.*~"},
		"key of every node":  {input: "a: {x: 1}\n", path: "$..*~"},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			r := paths.NewResolver(documentOf(t, tc.input))
			path := paths.MustParse(tc.path)

			_, err := r.Node(path)
			require.ErrorIs(t, err, paths.ErrWildcard)

			_, err = r.Token(path)
			require.ErrorIs(t, err, paths.ErrWildcard)

			root, err := r.Node(paths.Doc())
			require.NoError(t, err)

			_, err = r.NodeFrom(root, paths.MustParse("@"+tc.path[1:]))
			require.ErrorIs(t, err, paths.ErrWildcard)
		})
	}
}

func TestResolver_Nearest(t *testing.T) {
	t.Parallel()

	// Most cases look up a path in one of these two documents.
	server := stringtest.LinesLF("server:", "  port: 81")
	servers := stringtest.LinesLF(
		"servers:",
		"  web:",
		"    port: 80",
	)

	tcs := map[string]struct {
		input string
		path  string
		// The path of the mapping that lacks the key, or "" when the
		// path has none.
		want string
	}{
		"key a mapping leaves out": {
			input: server,
			path:  "$.server.name",
			want:  "$.server",
		},
		"key the root leaves out": {
			input: server,
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
			input: server,
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
			input: server,
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
			input: server,
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
			input: server,
			path:  "$.server.names[0]",
		},
		"key selector on the missing key": {
			input: server,
			path:  "$.server.name~",
		},
		"wildcard": {
			input: stringtest.LinesLF("servers:", "  - port: 80"),
			path:  "$.servers[*].name",
		},
		"mapping wildcard": {
			input: servers,
			path:  "$.servers.*.name",
		},
		"mapping wildcard before the missing key": {
			input: servers,
			path:  "$.*.web.name",
		},
		"recursive wildcard before the missing key": {
			input: servers,
			path:  "$..*.name",
		},
		"recursive selector": {
			input: server,
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

			assertPathText(t, got, tc.want)

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
}

func TestResolver_SeveralPaths(t *testing.T) {
	t.Parallel()

	// One Resolver resolves every path, including one it resolved before,
	// to the node, token, matches, and error that a Resolver made for that
	// path alone finds.
	doc := documentOf(t, stringtest.Input(`
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
	`))
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
	input := "base: &base {shared: 1}\n" + yamltest.GenerateLines(50, "k%[1]d: {<<: *base, own: %[1]d}")
	doc := documentOf(t, input)

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

			doc := documentOf(t, tc.input)
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

	mapping := requireAs[*ast.MappingNode](t, doc.Body)

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
			input: mergedAnchorDoc,
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

			doc := documentOf(t, tc.input)

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
			input: stringtest.LinesLF(`m: {"": v}`),
			want:  "",
			ok:    true,
		},
		"block scalar": {
			input: stringtest.LinesLF(
				"m:",
				"  ? |-",
				"    k",
				"  : v",
			),
			want: "k",
			ok:   true,
		},
		"anchor and tag": {
			input: stringtest.LinesLF("m:", "  &a !!str k: v"),
			want:  "k",
			ok:    true,
		},
		"alias": {
			input: stringtest.LinesLF(
				"base: &k n",
				"m:",
				"  *k : v",
			),
			want: "n",
			ok:   true,
		},
		"tagged alias": {
			input: stringtest.LinesLF(
				"base: &k 0x10",
				"m:",
				"  !!int *k : v",
			),
			want: "0x10",
			ok:   true,
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
		"alias with no anchor before it": {input: stringtest.LinesLF(
			"m:",
			"  *k : v",
			"base: &k n",
		)},
		"key a later merge key brings in": {
			// The merge comes after the entry, so the selector selects
			// the value the merge brings in.
			input: stringtest.LinesLF(
				"m:",
				"  k: own",
				"  <<: {k: merged}",
			),
			want:  "k",
			ok:    true,
			value: "merged",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			doc := documentOf(t, tc.input)

			r := paths.NewResolver(doc)
			m := paths.Doc().Child("m")

			node, err := r.Node(m)
			require.NoError(t, err)

			mapping := requireAs[*ast.MappingNode](t, node)
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
			input: stringtest.LinesLF(
				"k: &k x",
				"m:",
				"  *k : 1",
			),
			name: "x",
			want: "1",
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

			doc := documentOf(t, tc.input, tc.opts...)

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

			entry := requireAs[*ast.MappingValueNode](t, got)
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

			doc := documentOf(t, yamltest.WideMerge(tc.keys)+"  last: x\n")

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

			entry := requireAs[*ast.MappingValueNode](t, got)
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
			input: mergedAnchorDoc,
			want:  "&x one",
			line:  2,
		},
		"anchor that holds an alias": {
			// Deref goes on to the content of b, while Anchor stops at a.
			input: stringtest.LinesLF(
				"b: &b one",
				"a: &a",
				"  *b",
				"v: *a",
			),
			want: "&a *b",
			line: 2,
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

			doc := documentOf(t, tc.input)

			got, err := paths.NewResolver(doc).Anchor(valueOfV(t, doc))
			if tc.err != nil {
				require.ErrorIs(t, err, tc.err)

				return
			}

			require.NoError(t, err)

			anchor := requireAs[*ast.AnchorNode](t, got)
			assert.Equal(t, tc.want, anchor.String())
			assert.Equal(t, tc.line, anchor.GetToken().Position.Line)
		})
	}

	t.Run("anchors that share their content", func(t *testing.T) {
		t.Parallel()

		// The parser gives each anchor its own content, but a tree built by
		// hand may give two anchors one content node.
		doc := documentOf(t, stringtest.LinesLF("a: &x 1", "b: &y 2", "v: *x"))

		mapping := requireAs[*ast.MappingNode](t, doc.Body)
		require.Len(t, mapping.Values, 3)

		x := requireAs[*ast.AnchorNode](t, mapping.Values[0].Value)
		y := requireAs[*ast.AnchorNode](t, mapping.Values[1].Value)
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

			doc := documentOf(t, tc.input, tc.opts...)

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
				mapping := requireAs[*ast.MappingNode](t, src)
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

func TestResolver_Token(t *testing.T) {
	t.Parallel()

	doc := documentOf(t, stringtest.Input(`
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
	`))

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

func TestResolver_Token_Collections(t *testing.T) {
	t.Parallel()

	items := stringtest.LinesLF(
		"items:",
		"  - name: a",
		"  - name: b",
	)

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
			input: stringtest.LinesLF(
				"a:",
				"  b:",
				"    k: v",
			),
			path: "$.a.b",
			want: "b@2:3",
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
			input: stringtest.LinesLF(
				"items:",
				"  - &x",
				"    name: a",
			),
			path: "$.items[0]",
			want: "-@2:3",
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
			input: stringtest.LinesLF(
				"list: &l",
				"  - k: v",
				"copy: *l",
			),
			path: "$.copy[0]",
			want: "-@2:3",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			doc := documentOf(t, tc.input)

			tk, err := paths.NewResolver(doc).Token(paths.MustParse(tc.path))
			require.NoError(t, err)

			assert.Equal(t, tc.want, yamltest.TokenPlace(tk))
		})
	}
}

func TestResolver_NoDocument(t *testing.T) {
	t.Parallel()

	// A nil document holds nothing to resolve in, and neither does a
	// document without a header that holds no content. Node and Token
	// report both, with the path. Nodes and Matches list nothing in such a
	// document, and fail without one.
	tcs := map[string]struct {
		doc     *ast.DocumentNode
		listErr bool
	}{
		"nil document":              {listErr: true},
		"document without a header": {doc: &ast.DocumentNode{}},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			r := paths.NewResolver(tc.doc)

			for _, path := range []paths.Path{paths.Doc(), paths.Doc().Child("name")} {
				_, err := r.Node(path)
				requireNoDocument(t, err, path)
			}

			for _, path := range []paths.Path{paths.Doc().Child("name"), paths.Doc().Child("name").Key()} {
				_, err := r.Token(path)
				requireNoDocument(t, err, path)
				assert.Contains(t, err.Error(), "$.name")
			}

			for _, path := range []paths.Path{paths.Doc(), paths.Doc().Child("items").IndexAll()} {
				nodes, err := r.Nodes(path)
				matches, matchesErr := r.Matches(path)

				if tc.listErr {
					requireNoDocument(t, err, path)
					requireNoDocument(t, matchesErr, path)

					continue
				}

				require.NoError(t, err, path)
				assert.Empty(t, nodes, path)
				require.NoError(t, matchesErr, path)
				assert.Empty(t, matches, path)
			}
		})
	}
}

func TestResolver_NoContentDocument(t *testing.T) {
	t.Parallel()

	// No single node resolves in a document that holds no content, and a
	// path lists no nodes or matches there. Each case reads the first
	// document of its input.
	tcs := map[string]struct {
		input string
		docs  int // Documents the input parses to. Zero means one.
	}{
		// A parse that keeps comments makes the comment group the body of
		// a document that holds nothing else.
		"comment": {input: "# just a comment\n"},
		// The tokenizer gives a source the lexer emits nothing for one
		// placeholder token, which the parser reads as a plain scalar.
		// That scalar is not content.
		"newline":     {input: "\n"},
		"blank lines": {input: "\n\n"},
		"spaces":      {input: "   "},
		"lone bang":   {input: "!"},
		// The directive parses as a document of its own, ahead of the
		// content. It stands above the header, so its document holds no
		// header for the root path to select.
		"directive": {input: stringtest.LinesLF("%YAML 1.2", "---", "key: v"), docs: 2},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			file, err := niceyaml.NewSourceFromString(tc.input).File()
			require.NoError(t, err)
			require.Len(t, file.Docs, cmp.Or(tc.docs, 1))

			r := paths.NewResolver(file.Docs[0])

			_, err = r.Node(paths.Doc())
			requireNoDocument(t, err)

			for _, path := range []paths.Path{paths.Doc(), paths.Doc().Child("a")} {
				_, err = r.Token(path)
				requireNoDocument(t, err, path)
			}

			for _, path := range []paths.Path{paths.Doc(), paths.Doc().Child("a").IndexAll()} {
				nodes, err := r.Nodes(path)
				require.NoError(t, err, path)
				assert.Empty(t, nodes, path)

				matches, err := r.Matches(path)
				require.NoError(t, err, path)
				assert.Empty(t, matches, path)
			}
		})
	}
}

func TestResolver_DirectiveDocument(t *testing.T) {
	t.Parallel()

	// The directive parses as a document of its own, and the content
	// resolves in the document after it.
	file, err := niceyaml.NewSourceFromString(stringtest.LinesLF("%YAML 1.2", "---", "key: v")).File()
	require.NoError(t, err)
	require.Len(t, file.Docs, 2)

	node, err := paths.NewResolver(file.Docs[1]).Node(paths.Doc().Child("key"))
	require.NoError(t, err)
	assert.Equal(t, "v", node.String())
}

func TestResolver_MultipleDocuments(t *testing.T) {
	t.Parallel()

	docs := yamltest.Documents(
		t,
		niceyaml.NewSourceFromString(stringtest.LinesLF("name: first", "---", "name: second")),
		2,
	)
	path := paths.Doc().Child("name")

	tk, err := paths.NewResolver(docs[0].DocumentAST()).Token(path)
	require.NoError(t, err)
	assert.Equal(t, "first", tk.Value)

	tk, err = paths.NewResolver(docs[1].DocumentAST()).Token(path)
	require.NoError(t, err)
	assert.Equal(t, "second", tk.Value)
}

func TestResolver_Token_Anchors(t *testing.T) {
	t.Parallel()

	doc := documentOf(t, stringtest.Input(`
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
	`))

	// Each want names the token and where it starts, counted from 1.
	tcs := map[string]struct {
		path string
		want string
	}{
		"child of anchored mapping":                           {path: "$.base.a", want: "1@2:6"},
		"key of anchored mapping entry":                       {path: "$.base.a~", want: "a@2:3"},
		"anchored mapping value target is the entry key":      {path: "$.base", want: "base@1:1"},
		"anchored flow mapping value target is the entry key": {path: "$.flow", want: "flow@4:1"},
		"anchored sequence value target is the entry key":     {path: "$.list", want: "list@6:1"},
		"child through alias lands in the anchor":             {path: "$.other.b", want: "2@3:6"},
		"alias value target is the alias token":               {path: "$.other", want: "*@5:8"},
		"alias key target is its key":                         {path: "$.other~", want: "other@5:1"},
		"index through anchored sequence":                     {path: "$.list[1]", want: "two@8:5"},
		"index through aliased sequence":                      {path: "$.copy[0]", want: "one@7:5"},
		"tagged scalar value target skips the tag":            {path: "$.tagged", want: "5@10:15"},
		"merged key resolves to the anchor":                   {path: "$.merged.a", want: "1@2:6"},
		"merged key target resolves to the anchor key":        {path: "$.merged.a~", want: "a@2:3"},
		"own key after merge wins":                            {path: "$.merged.b", want: "20@13:6"},
		"merge after own key wins":                            {path: "$.late.b", want: "2@3:6"},
		"merge after own key wins at the key":                 {path: "$.late.b~", want: "b@3:3"},
		"own key before merge without that key":               {path: "$.late.c", want: "31@24:6"},
		"own key next to merge":                               {path: "$.merged.c", want: "3@14:6"},
		"merge sequence first source":                         {path: "$.multi.a", want: "1@2:6"},
		"merge sequence second source":                        {path: "$.multi.x", want: "10@4:14"},
		"merge sequence later source wins":                    {path: "$.multi.b", want: "200@4:21"},
		"merge of a merged mapping":                           {path: "$.chain.a", want: "1@2:6"},
		"merge of a merged mapping own key":                   {path: "$.chain.d", want: "4@19:6"},
		"merge key entry itself is addressable":               {path: "$.merged.<<~", want: "<<@12:3"},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, yamltest.TokenPlace(resolveToken(t, doc, tc.path)))
		})
	}

	t.Run("merged key not present", func(t *testing.T) {
		t.Parallel()

		_, err := paths.NewResolver(doc).Token(paths.Doc().Child("merged", "zzz"))
		require.ErrorIs(t, err, paths.ErrNotFound)
	})
}

func TestResolver_Node_Anchors(t *testing.T) {
	t.Parallel()

	doc := documentOf(t, stringtest.LinesLF(
		"base: &b {a: 1}",
		"other: *b",
		"tagged: !!str 5",
	))

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

func TestResolver_Node_TaggedKey(t *testing.T) {
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

			doc := documentOf(t, tc.input)
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

func TestResolver_Alias(t *testing.T) {
	t.Parallel()

	// A path that ends at an alias points at the alias's own token, and
	// Node looks through the alias to the last anchor of its name before
	// it. With no such anchor, Node reports that the alias names none. The
	// decoder reads an alias inside the content of the anchor it refers to
	// as null, so a path to such an alias selects no node either. A tag on
	// an alias stays over the alias, and an alias under a tag that does
	// not resolve fails as one without a tag does. Node, Nodes, and
	// [niceyaml.Node.At] agree on which.
	tcs := map[string]struct {
		err   error
		input string
		want  string
		path  paths.Path
	}{
		"alias with no anchor of its name": {
			input: stringtest.LinesLF("base: &b {a: 1}", "other: *nope"),
			path:  paths.Doc().Child("other"),
			err:   paths.ErrAlias,
		},
		"alias before the anchor of its name": {
			input: stringtest.LinesLF(
				"b: *x",
				"a: &x",
				"  v: 1",
			),
			path: paths.Doc().Child("b"),
			err:  paths.ErrAlias,
		},
		"tagged alias to an anchor": {
			input: "x: &x {k: 1}\nb: !t *x\n",
			path:  paths.Doc().Child("b"),
			want:  "!t *x",
		},
		"tagged alias with no anchor before it": {
			input: "b: !t *nope\n",
			path:  paths.Doc().Child("b"),
			err:   paths.ErrAlias,
		},
		"tagged alias inside its own anchor": {
			input: "a: &x [!t *x]\n",
			path:  paths.Doc().Child("a").Index(0),
			err:   paths.ErrAlias,
		},
		"anchor over a tag on an alias to itself": {
			input: "a: &x !t *x\n",
			path:  paths.Doc().Child("a"),
			err:   paths.ErrAlias,
		},
		"alias inside its own mapping anchor": {
			input: "b: &x {s: *x, t: 1}\n",
			path:  paths.Doc().Child("b", "s"),
			err:   paths.ErrAlias,
		},
		"alias inside its own sequence anchor": {
			input: "a: &a [1, *a]\n",
			path:  paths.Doc().Child("a").Index(1),
			err:   paths.ErrAlias,
		},
		"alias inside its own anchor on a merge value": {
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
			doc := dd.DocumentAST()

			tk, err := paths.NewResolver(doc).Token(tc.path)
			require.NoError(t, err)
			assert.Equal(t, token.AliasType, tk.Type)
			assert.Equal(t, "*", tk.Value)

			node, err := paths.NewResolver(doc).Node(tc.path)
			_, nodesErr := paths.NewResolver(doc).Nodes(tc.path)
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

func TestResolver_UnnamedKey(t *testing.T) {
	t.Parallel()

	// A key with no name has no text a selector can match, so neither a
	// `.''` nor a `..name` selector reaches its entry, and the entry never
	// hides a real empty key. The decoder never reads such a key as the
	// empty key. A lookup that passes the entry on the way to another key
	// still resolves.
	aliasKey := stringtest.LinesLF("b: 2", "*nope : 1")

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
			input:     aliasKey,
			path:      "$.''",
			err:       paths.ErrNotFound,
			recursive: "$..''",
		},
		"name of an alias key with no anchor": {
			input:     aliasKey,
			path:      "$.nope",
			err:       paths.ErrNotFound,
			recursive: "$..nope",
		},
		"indicator of an alias key with no anchor": {
			input:     aliasKey,
			path:      "$.'*'",
			err:       paths.ErrNotFound,
			recursive: "$..'*'",
		},
		"key beside an alias key with no anchor": {
			input:     aliasKey,
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
		"real empty key": {
			input:     "'': v\n",
			path:      "$.''",
			want:      "v",
			recursive: "$..''",
			matches:   []string{"$.''=v"},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			doc := documentOf(t, tc.input, tc.opts...)

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

func TestResolver_UnresolvedAlias(t *testing.T) {
	t.Parallel()

	// A path through an alias that does not resolve fails with ErrAlias,
	// whose text says why, rather than with ErrNotFound. Token, Node, and
	// Nodes agree.
	tcs := map[string]struct {
		input string
		want  string
		path  paths.Path
	}{
		"alias with no anchor of its name": {
			input: stringtest.LinesLF("base: &b {a: 1}", "other: *nope"),
			path:  paths.Doc().Child("other", "v"),
			want:  "*nope has no anchor before it",
		},
		"alias before the anchor of its name": {
			input: stringtest.LinesLF(
				"b: *x",
				"a: &x",
				"  v: 1",
			),
			path: paths.Doc().Child("b", "v"),
			want: "*x has no anchor before it",
		},
		"alias inside its own anchor through a tag": {
			input: "a: &x !t *x\n",
			path:  paths.Doc().Child("a", "c"),
			want:  "*x forms a cycle",
		},
		"merge key through an alias inside its own anchor": {
			input: stringtest.LinesLF(
				"a: &x !t *x",
				"m:",
				"  <<: *x",
			),
			path: paths.Doc().Child("m", "c"),
			want: "*x forms a cycle",
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

			doc := documentOf(t, tc.input)

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

func TestWithReferenceAnchors(t *testing.T) {
	t.Parallel()

	// The reference documents define the anchor shared alone.
	shared := paths.WithReferenceAnchors(func(name string) bool { return name == "shared" })
	port := paths.Doc().Child("server", "port")

	tcs := map[string]struct {
		opt   paths.ResolverOption
		input string
		path  paths.Path
		// The text of the error, or empty for a path that resolves.
		want string
		// Whether the error wraps paths.ErrReferenceAlias.
		reference bool
		// Whether Token finds a token where Node fails.
		token bool
	}{
		"path through an alias that reads a reference": {
			opt:       shared,
			input:     "server: *shared\n",
			path:      port,
			want:      "alias reads a reference document: *shared",
			reference: true,
		},
		"path to an alias that reads a reference": {
			// Token does not look through the node of the last selector, so
			// it finds the token of the alias.
			opt:       shared,
			input:     "server: *shared\n",
			path:      paths.Doc().Child("server"),
			want:      "alias reads a reference document: *shared",
			reference: true,
			token:     true,
		},
		"path through a tagged alias that reads a reference": {
			opt:       shared,
			input:     "server: !t *shared\n",
			path:      port,
			want:      "alias reads a reference document: *shared",
			reference: true,
		},
		"alias before the anchor of its name": {
			opt:       shared,
			input:     stringtest.LinesLF("server: *shared", "later: &shared {port: 1}"),
			path:      port,
			want:      "alias reads a reference document: *shared",
			reference: true,
		},
		"alias that no reference defines": {
			opt:   shared,
			input: "server: *nope\n",
			path:  port,
			want:  "alias does not resolve: *nope has no anchor before it",
		},
		"anchor of the document before the alias": {
			opt:   shared,
			input: stringtest.LinesLF("base: &shared {port: 1}", "server: *shared"),
			path:  port,
		},
		"nil function": {
			opt:   paths.WithReferenceAnchors(nil),
			input: "server: *shared\n",
			path:  port,
			want:  "alias does not resolve: *shared has no anchor before it",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			r := paths.NewResolver(documentOf(t, tc.input), tc.opt)

			_, tokenErr := r.Token(tc.path)
			_, nodeErr := r.Node(tc.path)
			_, nodesErr := r.Nodes(tc.path)

			errs := []error{nodeErr, nodesErr}
			if tc.token {
				require.NoError(t, tokenErr)
			} else {
				errs = append(errs, tokenErr)
			}

			for _, err := range errs {
				if tc.want == "" {
					require.NoError(t, err)

					continue
				}

				require.ErrorIs(t, err, paths.ErrAlias)
				assert.Equal(t, tc.reference, errors.Is(err, paths.ErrReferenceAlias))
				assert.Contains(t, err.Error(), tc.want)
			}
		})
	}

	t.Run("anchor of an alias that reads a reference", func(t *testing.T) {
		t.Parallel()

		doc := documentOf(t, "server: *shared\n")
		alias := requireAs[*ast.MappingNode](t, doc.Body).Values[0].Value

		_, err := paths.NewResolver(doc, shared).Anchor(alias)
		require.ErrorIs(t, err, paths.ErrAlias)
		require.ErrorIs(t, err, paths.ErrReferenceAlias)
		require.EqualError(t, err, "anchor: alias reads a reference document: *shared")
	})
}

func TestResolver_HandBuiltAST(t *testing.T) {
	t.Parallel()

	// The parser never produces these shapes, but Node and Token accept any
	// *ast.DocumentNode, so a mutated tree must error rather than panic.
	t.Run("alias without a name", func(t *testing.T) {
		t.Parallel()

		doc := documentOf(t, "a: 1\n")

		mapping := requireAs[*ast.MappingNode](t, doc.Body)

		mapping.Values[0].Value = &ast.AliasNode{}

		_, err := paths.NewResolver(doc).Node(paths.Doc().Child("a"))
		require.ErrorIs(t, err, paths.ErrAlias)
		assert.Contains(t, err.Error(), "alias has no name")

		_, err = paths.NewResolver(doc).Token(paths.Doc().Child("a", "b"))
		require.ErrorIs(t, err, paths.ErrAlias)
	})

	t.Run("entry without a key", func(t *testing.T) {
		t.Parallel()

		doc := documentOf(t, "a: 1\n")

		mapping := requireAs[*ast.MappingNode](t, doc.Body)

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

func TestResolver_Node_HandBuiltTree(t *testing.T) {
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

func TestResolver_Matches_HandBuiltTree(t *testing.T) {
	t.Parallel()

	shared := strNode("1")
	sharedMap := mapNode(mapEntry(strNode("x"), shared))
	sharedSeq := &ast.SequenceNode{Values: []ast.Node{shared}}

	// Each value of this mapping is a typed nil, which holds no node to
	// list.
	typedNils := mapNode(
		mapEntry(strNode("m"), (*ast.MappingNode)(nil)),
		mapEntry(strNode("s"), (*ast.SequenceNode)(nil)),
		mapEntry(strNode("a"), (*ast.AnchorNode)(nil)),
		mapEntry(strNode("t"), (*ast.TagNode)(nil)),
	)

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
		"every element of a typed nil sequence": {body: typedNils, path: "$.s[*]"},
		"every entry of a typed nil mapping":    {body: typedNils, path: "$.m.*"},
		"every entry of each typed nil":         {body: typedNils, path: "$.*.*"},
		"recursive through typed nils":          {body: typedNils, path: "$..q"},
		"every node below typed nils":           {body: typedNils, path: "$..*"},
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

func TestResolver_RedefinedAnchor(t *testing.T) {
	t.Parallel()

	// An alias refers to the last anchor of its name before it, so the *x on
	// line 3 reaches `v: 1` and the *x on line 6 reaches `v: 2`.
	doc := documentOf(t, stringtest.LinesLF(
		"a: &x",
		"  v: 1",
		"b: *x",
		"c: &x",
		"  v: 2",
		"d: *x",
	))

	// Each want names the token of the value of v and where it starts.
	tcs := map[string]struct {
		key  string
		want string
	}{
		"alias before the second anchor": {key: "b", want: "1@2:6"},
		"alias after the second anchor":  {key: "d", want: "2@5:6"},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			tk, err := paths.NewResolver(doc).Token(paths.Doc().Child(tc.key, "v"))
			require.NoError(t, err)
			assert.Equal(t, tc.want, yamltest.TokenPlace(tk))

			node, err := paths.NewResolver(doc).Node(paths.Doc().Child(tc.key))
			require.NoError(t, err)

			mapping := requireAs[*ast.MappingNode](t, node)
			require.Len(t, mapping.Values, 1)

			assert.Equal(t, tc.want, yamltest.TokenPlace(mapping.Values[0].Value.GetToken()))
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

func TestResolver_Token_NotFound(t *testing.T) {
	t.Parallel()

	doc := documentOf(t, "name: test\nitems: [a, b]\n")

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

func TestResolver_Nodes(t *testing.T) {
	t.Parallel()

	doc := documentOf(t, stringtest.Input(`
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
	`))

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

func TestResolver_Matches_AliasFanOut(t *testing.T) {
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

	doc := documentOf(t, input, yamltest.NoSizeLimits()...)
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

func TestResolver_Matches_MergeLookups(t *testing.T) {
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

func TestResolver_Node_NestedMergeLists(t *testing.T) {
	t.Parallel()

	// The mapping c merges a list of three sources. The middle one, b,
	// merges a list of its own, and the last source of that list merges a
	// longer list. A lookup in c searches each list from its last source
	// back, so it searches the longest list before the earlier sources of
	// the two shorter ones.
	doc := documentOf(t, stringtest.Input(`
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
	`))

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

func TestResolver_Token_Keys(t *testing.T) {
	t.Parallel()

	doc := documentOf(t, stringtest.Input(`
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
	`))

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

func TestResolver_Token_HandBuiltMapping(t *testing.T) {
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

func TestResolver_Node_TwoMergeKeys(t *testing.T) {
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

			doc := documentOf(t, tc.input, niceyaml.WithAllowDuplicateKeys(true))

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

func TestResolver_Matches_RepeatedMergeKey(t *testing.T) {
	t.Parallel()

	// The decoder merges both inline mappings, but a path through `<<`
	// selects the later one, so `..` visits only that mapping.
	src := stringtest.LinesLF(
		"t:",
		"  <<: {a: 1}",
		"  <<: {b: 2}",
	)

	doc := documentOf(t, src, niceyaml.WithAllowDuplicateKeys(true))

	assert.Empty(t, requireMatches(t, doc, "$..a"))
	assert.Equal(t, []string{"$.t.<<.b"}, matchPaths(requireMatches(t, doc, "$..b")))

	node, err := paths.NewResolver(doc).Node(paths.MustParse("$.t.a"))
	require.NoError(t, err)
	assert.Equal(t, "1", node.String())
}

func TestResolver_Matches_InlineMergeSource(t *testing.T) {
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
			input: stringtest.LinesLF(
				"m:",
				"  <<: {name: x}",
				"  name: z",
			),
			want:  []string{"$.m.<<.name", "$.m.name"},
			value: "z",
		},
		"later merge overrides an own key": {
			input: stringtest.LinesLF(
				"m:",
				"  name: z",
				"  <<: {name: x}",
			),
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
			input: stringtest.LinesLF(
				"base: &b {name: y}",
				"m:",
				"  <<: *b",
			),
			want:  nil,
			value: "y",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			doc := documentOf(t, tc.input)

			matches := requireMatches(t, doc, "$.m..name")
			assert.Equal(t, tc.want, matchPaths(matches))

			node, err := paths.NewResolver(doc).Node(paths.MustParse("$.m.name"))
			require.NoError(t, err)
			assert.Equal(t, tc.value, node.String())
		})
	}
}

func TestResolver_MergeKeyAndLiteralMergeName(t *testing.T) {
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

			doc := documentOf(t, input)

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

func TestResolver_MergeBringsInLiteralMergeName(t *testing.T) {
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

			doc := documentOf(t, tc.input, niceyaml.WithAllowDuplicateKeys(true))

			node, err := paths.NewResolver(doc).Node(paths.MustParse("$.top.'<<'"))
			require.NoError(t, err)
			assert.Equal(t, "real", node.String())

			matches := requireMatches(t, doc, "$..'<<'")
			assert.Equal(t, tc.want, matchPaths(matches))
		})
	}
}

func TestResolver_Node_LaterDuplicateKeyWins(t *testing.T) {
	t.Parallel()

	// A mapping may hold one key twice when the parser allows duplicates.
	// The decoder keeps the value of the later entry, so the path selects
	// it, and `..a` lists that entry alone.
	src := stringtest.LinesLF(
		"m:",
		"  a: 1",
		"  a: 2",
	)

	doc := documentOf(t, src, niceyaml.WithAllowDuplicateKeys(true))

	node, err := paths.NewResolver(doc).Node(paths.MustParse("$.m.a"))
	require.NoError(t, err)
	assert.Equal(t, "2", node.String())

	key, err := paths.NewResolver(doc).Token(paths.MustParse("$.m.a~"))
	require.NoError(t, err)
	assert.Equal(t, "a@3:3", yamltest.TokenPlace(key))

	assert.Equal(t, []string{"$.m.a"}, matchPaths(requireMatches(t, doc, "$..a")))
}

// nestedChain returns depth flow mappings nested in one another, each with
// the single key a.
func nestedChain(depth int) string {
	return strings.Repeat("{a: ", depth) + "1" + strings.Repeat("}", depth)
}

func TestResolver_Matches_RecursiveReachedTwice(t *testing.T) {
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

			doc := documentOf(t, tc.input, niceyaml.WithDepthLimit(0))

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

func TestResolver_Matches_Paths(t *testing.T) {
	t.Parallel()

	doc := documentOf(t, stringtest.Input(`
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

func TestResolver_Matches(t *testing.T) {
	t.Parallel()

	// The path of each match selects that match's node and reads back from
	// its text.
	dups := []niceyaml.SourceOption{niceyaml.WithAllowDuplicateKeys(true)}

	tcs := map[string]struct {
		input string
		path  string
		want  []string
		opts  []niceyaml.SourceOption
	}{
		// A `.*` selector lists the entry a `.name` selector resolves in a
		// mapping, once for each name.
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
			input: stringtest.LinesLF(
				"jobs:",
				"  build: 1",
				"  test: 2",
			),
			path: "$.jobs.*~",
			want: []string{"$.jobs.build~=build", "$.jobs.test~=test"},
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
			input: stringtest.LinesLF(
				"m:",
				`  "*": 1`,
				"  b: 2",
			),
			path: "$.m.*",
			want: []string{"$.m.'*'=1", "$.m.b=2"},
		},
		"quoted star selects the key named star alone": {
			input: stringtest.LinesLF(
				"m:",
				`  "*": 1`,
				"  b: 2",
			),
			path: "$.m.'*'",
			want: []string{"$.m.'*'=1"},
		},
		"alias key takes the name of its anchor": {
			input: stringtest.LinesLF(
				"k: &k name",
				"m:",
				"  *k : v",
			),
			path: "$.m.*",
			want: []string{"$.m.name=v"},
		},
		"key with no name is left out": {
			input: stringtest.LinesLF(
				"m:",
				"  b: 2",
				"  *nope : 1",
			),
			path: "$.m.*",
			want: []string{"$.m.b=2"},
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
		"merged entry that no own key overrides": {
			input: stringtest.LinesLF(
				"base: &b {k: x}",
				"m:",
				"  <<: *b",
				"  0x10: y",
			),
			path: "$.m.*",
			want: []string{"$.m.k=x", "$.m.0x10=y"},
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
		"entries of an inline merge source": {
			input: stringtest.LinesLF(
				"m:",
				"  <<: {a: 1}",
				"  b: 2",
			),
			path: "$.m.*",
			want: []string{"$.m.a=1", "$.m.b=2"},
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
			input: stringtest.LinesLF(
				"a: &a",
				"  x: 1",
				"  <<: *a",
			),
			path: "$.a.*",
			want: []string{"$.a.x=1"},
		},
		"later duplicate key wins among the entries": {
			input: stringtest.LinesLF(
				"m:",
				"  a: 1",
				"  b: 2",
				"  a: 3",
			),
			path: "$.m.*",
			want: []string{"$.m.b=2", "$.m.a=3"},
			opts: dups,
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

		// A `..*` selector visits what a `..name` selector visits and lists
		// each node it visits.
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
		"merged entries are not listed below a start that merges them": {
			input: stringtest.LinesLF(
				"base: &b {k: x}",
				"m:",
				"  <<: *b",
				"  l: [y]",
			),
			path: "$.m..*",
			want: []string{"$.m.l=[y]", "$.m.l[0]=y"},
		},
		"nodes of an inline merge source": {
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
		"later duplicate key wins among the nodes": {
			input: "a: {x: 1}\na: [2]\n",
			path:  "$..*",
			want:  []string{"$.a=[2]", "$.a[0]=2"},
			opts:  dups,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			doc := documentOf(t, tc.input, tc.opts...)
			matches := requireMatches(t, doc, tc.path)

			assert.Equal(t, tc.want, matchValues(matches))
		})
	}
}

func TestResolver_Matches_WildcardErrors(t *testing.T) {
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
			input: stringtest.LinesLF(
				"m:",
				"  <<: *missing",
				"  a: 1",
			),
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
		"every element of an alias with no anchor": {
			input: "a: *nope\n",
			path:  "$.a[*]",
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

			doc := documentOf(t, tc.input)
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

func TestResolver_EmptyDocument(t *testing.T) {
	t.Parallel()

	t.Run("the root is the null below the header", func(t *testing.T) {
		t.Parallel()

		// A parse that keeps comments makes a comment group below the
		// header the body of the document, which still holds no content.
		// Each want names the header token and where it starts.
		tcs := map[string]struct {
			input string
			want  string
		}{
			"header after a document": {input: "a: 1\n---\n", want: "---@2:1"},
			"header over a comment":   {input: "---\n# comment\n", want: "---@1:1"},
			"header over a comment after a document": {
				input: stringtest.LinesLF("a: 1", "---", "# comment"),
				want:  "---@2:1",
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
					assert.Equal(t, tc.want, yamltest.TokenPlace(tk))
				}

				_, err = paths.NewResolver(doc).Node(paths.Doc().Child("a"))
				requireNoDocument(t, err)
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
					requireNoDocument(t, err)
				}
			})
		}
	})
}
