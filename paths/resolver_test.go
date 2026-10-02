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
		"earlier anchor over the anchor of an inline merge source": {
			// The decoder records an anchor on a merge value only for the
			// aliases other merge keys name, so *m reads the value &m.
			input: "a: &m 1\nb: {<<: &m {k: 2}}\nc: *m\n",
			path:  "$.c",
			want:  "1",
		},
		"earlier anchor over the anchor of an inline merge source in a sequence": {
			input: "a: &m 1\nb: {<<: [&m {k: 2}]}\nc: *m\n",
			path:  "$.c",
			want:  "1",
		},
		"earlier anchor over the anchor of a block merge source": {
			input: "a: &m 1\nb:\n  <<: &m\n    k: 2\nc: *m\n",
			path:  "$.c",
			want:  "1",
		},
		"earlier anchor over the anchor of a merge source a merge brings in again": {
			input: "base: &b {x: {<<: &m {k: 2}}}\na: &m 1\nd: {<<: *b}\nc: *m\n",
			path:  "$.c",
			want:  "1",
		},
		"anchor of an inline merge source with no earlier anchor": {
			input: "b: {<<: &m {k: 2}}\nc: *m\n",
			path:  "$.c.k",
			want:  "2",
		},
		"value alias beside an anchor of an inline merge source": {
			input: "a: &m {k: 1}\nb: {<<: &m {k: 2}}\nd: {<<: *m}\nc: *m\n",
			path:  "$.c.k",
			want:  "1",
		},
		"merge alias to the anchor of an inline merge source": {
			input: "a: &m {k: 1}\nb: {<<: &m {k: 2}}\nd: {<<: *m}\nc: *m\n",
			path:  "$.d.k",
			want:  "2",
		},
		"anchor of a merge source that merges itself": {
			// The decoder rejects the merge of a mapping into itself. The
			// binding stops at the cycle and still counts &x one again.
			input: "a: &a {k: &x one, i: {<<: *a}}\nother: &x two\nm:\n  <<: *a\n  v: *x\n",
			path:  "$.m.v",
			want:  "one",
		},
		"anchor of a merge source after a merge alias in it binds": {
			// The first merge of *B reads &B before the walk binds its *C,
			// so a later merge of *B must read &B again to count &N cval.
			input: "c: &C {x: &N cval}\nm:\n  <<:\n    - <<: *B\n    - &B\n      <<: *C\nn: &N other\nv:\n  <<: *B\n  w: *N\n",
			path:  "$.v.w",
			want:  "cval",
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
			input: "a:\n  b:\n    c: x\n",
			from:  "$.a",
			path:  "$.b.c",
			want:  "x",
		},
		"index": {
			input: "a: [x, [y, z]]\n",
			from:  "$.a",
			path:  "$[1][0]",
			want:  "y",
		},
		"root path selects the node itself": {
			input: "a:\n  b: x\n",
			from:  "$.a.b",
			path:  "$",
			want:  "x",
		},
		"from a node an alias leads to": {
			input: "base: &b {k: {v: x}}\nref: *b\n",
			from:  "$.ref",
			path:  "$.k.v",
			want:  "x",
		},
		"key a merge brings in": {
			input: "base: &b {k: x}\nm:\n  <<: *b\n  own: 1\n",
			from:  "$.m",
			path:  "$.k",
			want:  "x",
		},
		"from a tagged mapping": {
			input: "a: !!map {b: x}\n",
			from:  "$.a",
			path:  "$.b",
			want:  "x",
		},
		"key of an entry": {
			input: "a:\n  b: x\n",
			from:  "$.a",
			path:  "$.b~",
			want:  "b",
		},
		"leading key selects the node itself": {
			input: "a:\n  b: x\n",
			from:  "$.a.b",
			path:  "$~",
			want:  "x",
		},
		"unknown alias": {
			input: "a:\n  b: *nope\n",
			from:  "$.a",
			path:  "$.b",
			err:   paths.ErrAlias,
		},
		"wildcard": {
			input: "a: [x, y]\n",
			from:  "$.a",
			path:  "$[*]",
			err:   paths.ErrWildcard,
		},
		"mapping wildcard": {
			input: "a: {x: 1}\n",
			from:  "$.a",
			path:  "$.*",
			err:   paths.ErrWildcard,
		},
		"missing path": {
			input: "a:\n  b: x\n",
			from:  "$.a",
			path:  "$.c",
			err:   paths.ErrNotFound,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			file, err := niceyaml.NewSourceFromString(tc.input).File()
			require.NoError(t, err)

			r := paths.NewResolver(file.Docs[0])
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
			if strings.HasPrefix(tc.path, "$~") {
				return
			}

			joined, err := r.Node(from.Join(path))
			require.NoError(t, err)
			assert.Same(t, joined, node)
		})
	}

	t.Run("nil node", func(t *testing.T) {
		t.Parallel()

		file, err := niceyaml.NewSourceFromString("a: x\n").File()
		require.NoError(t, err)

		r := paths.NewResolver(file.Docs[0])

		for _, path := range []paths.Path{paths.Root(), paths.Root().Child("a"), paths.Root().Key()} {
			_, err := r.NodeFrom(nil, path)
			require.ErrorIs(t, err, paths.ErrNotFound, path.String())
		}
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
		"mapping wildcard": {
			input: "a: {x: 1}\n",
			path:  "$.a.*~",
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
			input: "server:\n  port: 81\n",
			path:  "$.server.name",
			want:  "$.server",
		},
		"key the root leaves out": {
			input: "server:\n  port: 81\n",
			path:  "$.name",
			want:  "$",
		},
		"key an element of a sequence leaves out": {
			input: "servers:\n  - port: 80\n    name: a\n  - port: 81\n",
			path:  "$.servers[1].name",
			want:  "$.servers[1]",
		},
		"several names below the mapping": {
			input: "server:\n  port: 81\n",
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
			input: "base: &b {port: 81}\nserver: *b\n",
			path:  "$.server.name",
			want:  "$.server",
		},
		"mapping behind a tag": {
			input: "server: !srv {port: 81}\n",
			path:  "$.server.name",
			want:  "$.server",
		},
		"mapping a merge key fills": {
			input: "base: &b {port: 81}\nserver:\n  <<: *b\n",
			path:  "$.server.name",
			want:  "$.server",
		},
		"path that resolves": {
			input: "server:\n  port: 81\n",
			path:  "$.server.port",
		},
		"key a merge brings in": {
			input: "base: &b {port: 81}\nserver:\n  <<: *b\n",
			path:  "$.server.port",
		},
		"root": {
			input: "server:\n  port: 81\n",
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
			input: "servers:\n  - port: 80\n",
			path:  "$.servers[5].name",
		},
		"index below the missing key": {
			input: "server:\n  port: 81\n",
			path:  "$.server.names[0]",
		},
		"key selector on the missing key": {
			input: "server:\n  port: 81\n",
			path:  "$.server.name~",
		},
		"wildcard": {
			input: "servers:\n  - port: 80\n",
			path:  "$.servers[*].name",
		},
		"mapping wildcard": {
			input: "servers:\n  web:\n    port: 80\n",
			path:  "$.servers.*.name",
		},
		"mapping wildcard before the missing key": {
			input: "servers:\n  web:\n    port: 80\n",
			path:  "$.*.web.name",
		},
		"recursive selector": {
			input: "server:\n  port: 81\n",
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
		"nil document": {
			path: "$.name",
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

			r := paths.NewResolver(doc)
			path := paths.MustParse(tc.path)

			got, ok := r.Nearest(path)
			assert.Equal(t, tc.want != "", ok)

			if !ok {
				assert.Equal(t, paths.Root(), got)

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
			assert.False(t, rest.IsRoot())
		})
	}

	t.Run("leaves the path as it was", func(t *testing.T) {
		t.Parallel()

		file, err := niceyaml.NewSourceFromString("server:\n  port: 81\n").File()
		require.NoError(t, err)

		path := paths.Root().Child("server", "tls", "cert")

		near, ok := paths.NewResolver(file.Docs[0]).Nearest(path)
		require.True(t, ok)

		assert.Equal(t, "$.server.x", near.Child("x").String())
		assert.Equal(t, "$.server", near.String())
		assert.Equal(t, "$.server.tls.cert", path.String())
	})
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
		"every entry of a mapping": {
			input:      "base: &b {k: x}\nm:\n  <<: *b\n  0x10: y\n",
			path:       "$.m.*",
			want:       []string{"$.m.k", "$.m.0x10"},
			wantValues: []string{"x", "y"},
		},
		"every entry of a sequence": {
			input: "a: [x, y]\n",
			path:  "$.a.*",
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
		lit: {? &lt "<<" : 1}
		both:
		  *lt : {name: real}
		  <<: {name: merged}
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

func TestResolver_NestedOpenMerges(t *testing.T) {
	t.Parallel()

	// Each bN merges the two mappings before it, and b0 merges a. No merge
	// of a bN is final while the walk is inside a. Reading every merge
	// again would take about 1.6^60 reads.
	var sb strings.Builder

	sb.WriteString("a: &a\n  b0: &b0 {<<: *a, k: &x one}\n  b1: &b1 {<<: [*b0, *a]}\n")

	for i := 2; i < 60; i++ {
		fmt.Fprintf(&sb, "  b%d: &b%d {<<: [*b%d, *b%d]}\n", i, i, i-1, i-2)
	}

	sb.WriteString("  v: *x\n")

	file, err := niceyaml.NewSourceFromString(sb.String()).File()
	require.NoError(t, err)

	nodes := make(chan ast.Node, 1)

	go func() {
		node, err := paths.NewResolver(file.Docs[0]).Node(paths.Root().Child("a", "v"))
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
			input: "a: &a !t *b\nv: *a\nb: &b x\n",
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
			"nil node": {
				node: nil,
			},
			"typed nil": {
				node: (*ast.StringNode)(nil),
			},
			"anchor over a typed nil": {
				node: &ast.AnchorNode{Value: (*ast.MappingNode)(nil)},
			},
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

	// Each case names the key of the only entry of the mapping at $.m.
	tcs := map[string]struct {
		input string
		want  string
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
			input: "m:\n  ? |-\n    k\n  : v\n",
			want:  "k",
			ok:    true,
		},
		"anchor and tag": {
			input: "m:\n  &a !!str k: v\n",
			want:  "k",
			ok:    true,
		},
		"alias": {
			input: "base: &k n\nm:\n  *k : v\n",
			want:  "n",
			ok:    true,
		},
		"tagged alias": {
			input: "base: &k 0x10\nm:\n  !!int *k : v\n",
			want:  "0x10",
			ok:    true,
		},
		"alias to a tagged alias": {
			input: "base: &k 0x10\nb: &j !!int *k\nm:\n  *j : v\n",
			want:  "0x10",
			ok:    true,
		},
		"alias with no anchor before it": {
			input: "m:\n  *k : v\nbase: &k n\n",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			file, err := niceyaml.NewSourceFromString(tc.input).File()
			require.NoError(t, err)

			r := paths.NewResolver(file.Docs[0])
			m := paths.Root().Child("m")

			node, err := r.Node(m)
			require.NoError(t, err)

			mapping, ok := node.(*ast.MappingNode)
			require.True(t, ok, "m is a %T", node)
			require.Len(t, mapping.Values, 1)

			got, ok := r.KeyName(mapping.Values[0].Key)
			assert.Equal(t, tc.ok, ok)
			assert.Equal(t, tc.want, got)

			if !tc.ok {
				return
			}

			// A child selector with the name selects the value of the
			// entry.
			value, err := r.Node(m.Child(got))
			require.NoError(t, err)
			assert.Equal(t, "v", value.String())
		})
	}

	t.Run("later merge key brings in the key", func(t *testing.T) {
		t.Parallel()

		file, err := niceyaml.NewSourceFromString("m:\n  k: own\n  <<: {k: merged}\n").File()
		require.NoError(t, err)

		r := paths.NewResolver(file.Docs[0])
		m := paths.Root().Child("m")

		node, err := r.Node(m)
		require.NoError(t, err)

		mapping, ok := node.(*ast.MappingNode)
		require.True(t, ok, "m is a %T", node)

		got, ok := r.KeyName(mapping.Values[0].Key)
		require.True(t, ok)
		assert.Equal(t, "k", got)

		// The merge comes after the entry, so a child selector with the
		// name selects the value the merge brings in.
		value, err := r.Node(m.Child(got))
		require.NoError(t, err)
		assert.Equal(t, "merged", value.String())
	})

	// The parser rejects a sequence or mapping key, but a tree built by
	// hand may hold one.
	handBuilt := map[string]struct {
		key ast.Node
	}{
		"nil": {},
		"typed nil": {
			key: (*ast.StringNode)(nil),
		},
		"sequence": {
			key: &ast.SequenceNode{BaseNode: &ast.BaseNode{}},
		},
		"explicit sequence": {
			key: &ast.MappingKeyNode{Value: &ast.SequenceNode{BaseNode: &ast.BaseNode{}}},
		},
		"mapping": {
			key: &ast.MappingNode{BaseNode: &ast.BaseNode{}},
		},
	}

	for name, tc := range handBuilt {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, ok := paths.NewResolver(nil).KeyName(tc.key)
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
			input: "k: &k x\nm:\n  *k : 1\n",
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

			file, err := niceyaml.NewSourceFromString(tc.input, tc.opts...).File()
			require.NoError(t, err)

			r := paths.NewResolver(file.Docs[0])
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

	// The mapping m of each document holds the keys k0 and up, then a merge
	// key that lists as many aliases to one mapping, then the key last. A
	// lookup of each of those keys reads the whole list, and a lookup of
	// last reads no merge key.
	tcs := map[string]struct {
		err  error
		keys int
	}{
		"few merge sources": {
			keys: 10,
		},
		"many merge sources": {
			keys: 2000,
			err:  paths.ErrExcessiveMerging,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var sb strings.Builder

			sb.WriteString("a: &a {z: 0}\nm:\n")

			for i := range tc.keys {
				fmt.Fprintf(&sb, "  k%d: %d\n", i, i)
			}

			sb.WriteString("  <<: [" + strings.Repeat("*a, ", tc.keys-1) + "*a]\n  last: x\n")

			file, err := niceyaml.NewSourceFromString(sb.String()).File()
			require.NoError(t, err)

			r := paths.NewResolver(file.Docs[0])

			m, err := r.Node(paths.MustParse("$.m"))
			require.NoError(t, err)

			finder := r.EntryFinder()

			// The lookups share one limit, so no lookup of these keys
			// finds its entry after the one that passes the limit.
			found := 0

			for i := range tc.keys {
				entry, err := finder.Entry(m, fmt.Sprintf("k%d", i))
				if err != nil {
					require.ErrorIs(t, err, tc.err)
					assert.Nil(t, entry)

					continue
				}

				require.Equal(t, i, found, "lookup of k%d found its entry past the limit", i)

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
			key := fmt.Sprintf("k%d", tc.keys-1)

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
			input: "a: &a one\nb: &a two\nv: *a\n",
			want:  "&a two",
			line:  2,
		},
		"anchor a merge brings in again": {
			input: "base: &b\n  k: &x one\nother: &x two\nm:\n  <<: *b\nv: *x\n",
			want:  "&x one",
			line:  2,
		},
		"anchor that holds an alias": {
			// Deref goes on to the content of b, while Anchor stops at a.
			input: "b: &b one\na: &a\n  *b\nv: *a\n",
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

			got, err := paths.NewResolver(file.Docs[0]).Anchor(value)
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
		file, err := niceyaml.NewSourceFromString("a: &x 1\nb: &y 2\nv: *x\n").File()
		require.NoError(t, err)

		mapping, ok := file.Docs[0].Body.(*ast.MappingNode)
		require.True(t, ok, "body is a %T", file.Docs[0].Body)
		require.Len(t, mapping.Values, 3)

		x, ok := mapping.Values[0].Value.(*ast.AnchorNode)
		require.True(t, ok, "a holds a %T", mapping.Values[0].Value)

		y, ok := mapping.Values[1].Value.(*ast.AnchorNode)
		require.True(t, ok, "b holds a %T", mapping.Values[1].Value)

		y.Value = x.Value

		got, err := paths.NewResolver(file.Docs[0]).Anchor(mapping.Values[2].Value)
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
