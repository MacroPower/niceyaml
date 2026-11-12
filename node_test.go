package niceyaml_test

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"

	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/token"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.jacobcolvin.com/x/stringtest"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/diff"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/line"
	"go.jacobcolvin.com/niceyaml/paths"
	"go.jacobcolvin.com/niceyaml/position"
	"go.jacobcolvin.com/niceyaml/printer"
	"go.jacobcolvin.com/niceyaml/tokens"
)

func TestNodeKind_String(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		want string
		kind niceyaml.NodeKind
	}{
		"none":         {kind: niceyaml.NodeNone, want: "none"},
		"mapping":      {kind: niceyaml.NodeMapping, want: "mapping"},
		"sequence":     {kind: niceyaml.NodeSequence, want: "sequence"},
		"scalar":       {kind: niceyaml.NodeScalar, want: "scalar"},
		"out of range": {kind: niceyaml.NodeKind(4), want: "NodeKind(4)"},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, tc.kind.String())
		})
	}
}

func TestNode_Kind(t *testing.T) {
	t.Parallel()

	// Each case lists the kind of the Node and whether IsEmpty reports it
	// empty. Kind is NodeNone for more than the empty documents, so a
	// caller that skips those asks IsEmpty.
	tcs := map[string]struct {
		input string
		// The text of a reference document, or empty for none.
		refs  string
		path  paths.Path
		kind  niceyaml.NodeKind
		empty bool
	}{
		"block mapping":               {input: "a: 1\n", kind: niceyaml.NodeMapping},
		"flow mapping":                {input: "{a: 1}\n", kind: niceyaml.NodeMapping},
		"empty mapping":               {input: "{}\n", kind: niceyaml.NodeMapping},
		"anchored mapping":            {input: "&r {a: 1}\n", kind: niceyaml.NodeMapping},
		"tagged mapping":              {input: "!!map {a: 1}\n", kind: niceyaml.NodeMapping},
		"anchored and tagged mapping": {input: stringtest.LinesLF("&r !!map", "a: 1"), kind: niceyaml.NodeMapping},
		"block sequence":              {input: stringtest.LinesLF("- a", "- b"), kind: niceyaml.NodeSequence},
		"flow sequence":               {input: "[a, b]\n", kind: niceyaml.NodeSequence},
		"empty sequence":              {input: "[]\n", kind: niceyaml.NodeSequence},
		"tagged sequence":             {input: "&s !!seq [a]\n", kind: niceyaml.NodeSequence},
		"scalar":                      {input: "hello\n", kind: niceyaml.NodeScalar},
		"null":                        {input: "null\n", kind: niceyaml.NodeScalar},
		"tilde":                       {input: "~\n", kind: niceyaml.NodeScalar},
		"null below a header":         {input: "--- null\n", kind: niceyaml.NodeScalar},
		"tagged null":                 {input: "!!null\n", kind: niceyaml.NodeScalar},
		"tagged scalar":               {input: "!!str 1\n", kind: niceyaml.NodeScalar},
		"empty single-quoted string":  {input: "''\n", kind: niceyaml.NodeScalar},
		"empty double-quoted string":  {input: "\"\"\n", kind: niceyaml.NodeScalar},
		"block scalar":                {input: stringtest.LinesLF("|", "  text"), kind: niceyaml.NodeScalar},
		"empty block scalar":          {input: "--- |\n", kind: niceyaml.NodeScalar},
		"empty source":                {input: "", kind: niceyaml.NodeNone, empty: true},
		"whitespace alone":            {input: "   \n", kind: niceyaml.NodeNone, empty: true},
		"comments alone":              {input: "# only a comment\n", kind: niceyaml.NodeNone, empty: true},
		"header alone":                {input: "---\n", kind: niceyaml.NodeNone, empty: true},
		"comment on the header line":  {input: "--- # nothing\n", kind: niceyaml.NodeNone, empty: true},
		"header and comment": {
			input: stringtest.LinesLF("---", "# Source: chart/templates/empty.yaml"),
			kind:  niceyaml.NodeNone,
			empty: true,
		},
		"directive and header":  {input: stringtest.LinesLF("%YAML 1.2", "---"), kind: niceyaml.NodeNone, empty: true},
		"header and end marker": {input: stringtest.LinesLF("---", "..."), kind: niceyaml.NodeNone, empty: true},
		"end marker alone":      {input: "...\n", kind: niceyaml.NodeNone, empty: true},
		// An alias that names no anchor holds no value, and the document
		// that holds it is not empty.
		"alias with no anchor":                {input: "*nope\n", kind: niceyaml.NodeNone},
		"alias with no anchor below a header": {input: "--- *nope\n", kind: niceyaml.NodeNone},
		"tagged alias with no anchor":         {input: "!!str *x\n", kind: niceyaml.NodeNone},
		// The decoder reads an alias of a collection under !!str or !!null
		// as a scalar, but under a local tag as the collection.
		"string tag on an alias of a sequence": {
			input: "a: &k [x, y]\nm: !!str *k\n", path: paths.Doc().Child("m"), kind: niceyaml.NodeScalar,
		},
		"null tag on an alias of a mapping": {
			input: "a: &k {p: 1}\nm: !!null *k\n", path: paths.Doc().Child("m"), kind: niceyaml.NodeScalar,
		},
		"local tag on an alias of a sequence": {
			input: "a: &k [x, y]\nm: !t *k\n", path: paths.Doc().Child("m"), kind: niceyaml.NodeSequence,
		},
		"string tag before the anchor of a mapping": {
			input: "p: !!str &k {a: 1}\n", path: paths.Doc().Child("p"), kind: niceyaml.NodeScalar,
		},
		// The decoder reads an alias inside the content of the anchor it
		// refers to as null.
		"alias inside its own anchor": {
			input: "a: &a [1, *a]\n", path: paths.Doc().Child("a").Index(1), kind: niceyaml.NodeScalar,
		},
		"int tag on an alias inside its own anchor": {
			input: "a: &a !!int *a\n", path: paths.Doc().Child("a"), kind: niceyaml.NodeScalar,
		},
		"alias of an anchor whose value is an alias inside its own anchor": {
			// The decoder reads b as "", the string tag over the null of *a.
			input: stringtest.LinesLF("a: &a !!str *a", "b: !!str *a"),
			path:  paths.Doc().Child("b"),
			kind:  niceyaml.NodeScalar,
		},
		"alias without a tag of an anchor whose value is an alias inside it": {
			input: stringtest.LinesLF("a: &a !t *a", "b: *a"),
			path:  paths.Doc().Child("b"),
			kind:  niceyaml.NodeScalar,
		},
		// An alias that reads a reference document has the kind of the
		// anchor there, as a decode reads it.
		"alias to a mapping of a reference": {
			input: "*m\n",
			refs:  "m: &m {a: 1}\n",
			kind:  niceyaml.NodeMapping,
		},
		"alias to a sequence of a reference": {
			input: "*s\n",
			refs:  "s: &s [1]\n",
			kind:  niceyaml.NodeSequence,
		},
		"tagged alias to a scalar of a reference": {
			input: "!!str *v\n",
			refs:  "v: &v 1\n",
			kind:  niceyaml.NodeScalar,
		},
		"alias to a reference anchor that holds another alias": {
			input: "*m\n",
			refs:  stringtest.LinesLF("s: &s [1]", "m: &m !t *s"),
			kind:  niceyaml.NodeSequence,
		},
		"alias that no reference defines": {
			input: "*nope\n",
			refs:  "m: &m {a: 1}\n",
			kind:  niceyaml.NodeNone,
		},
		"alias to a reference anchor that leads back to itself": {
			input: "*m\n",
			refs:  stringtest.LinesLF("m: &m !t *n", "n: &n !t *m"),
			kind:  niceyaml.NodeNone,
		},
		"value mapping": {
			input: stringtest.LinesLF(
				"a:",
				"  b: 1",
			),
			path: paths.MustParse("$.a"),
			kind: niceyaml.NodeMapping,
		},
		"empty mapping value": {
			input: "a: {}\n",
			path:  paths.MustParse("$.a"),
			kind:  niceyaml.NodeMapping,
		},
		"value sequence": {
			input: "a: [1]\n",
			path:  paths.MustParse("$.a"),
			kind:  niceyaml.NodeSequence,
		},
		"value scalar": {
			input: "a: 1\n",
			path:  paths.MustParse("$.a"),
			kind:  niceyaml.NodeScalar,
		},
		"value left out": {
			input: "a:\n",
			path:  paths.MustParse("$.a"),
			kind:  niceyaml.NodeScalar,
		},
		"value tagged as a string": {
			input: "a: !!str 1\n",
			path:  paths.MustParse("$.a"),
			kind:  niceyaml.NodeScalar,
		},
		"alias to a sequence": {
			input: stringtest.JoinLF(
				"a: &x [1]",
				"b: *x",
			),
			path: paths.MustParse("$.b"),
			kind: niceyaml.NodeSequence,
		},
		"tagged alias to a mapping": {
			input: stringtest.JoinLF(
				"a: &x {k: 1}",
				"b: !custom *x",
			),
			path: paths.MustParse("$.b"),
			kind: niceyaml.NodeMapping,
		},
		"tagged alias to a scalar": {
			input: stringtest.JoinLF(
				"a: &x 1",
				"b: !!str *x",
			),
			path: paths.MustParse("$.b"),
			kind: niceyaml.NodeScalar,
		},
		"anchor on a tagged mapping, through an alias": {
			input: stringtest.JoinLF(
				"a: &x !!map {k: 1}",
				"b: *x",
			),
			path: paths.MustParse("$.b"),
			kind: niceyaml.NodeMapping,
		},
		"key": {
			input: "a: 1\n",
			path:  paths.MustParse("$.a~"),
			kind:  niceyaml.NodeScalar,
		},
		"explicit key": {
			input: stringtest.LinesLF("? a", ": [1]"),
			path:  paths.MustParse("$.a~"),
			kind:  niceyaml.NodeScalar,
		},
		"entry through a merge key": {
			input: stringtest.JoinLF(
				"base: &base {k: [1]}",
				"a:",
				"  <<: *base",
			),
			path: paths.MustParse("$.a.k"),
			kind: niceyaml.NodeSequence,
		},
		"value at an alias to a reference": {
			input: "a: *s\n",
			refs:  "s: &s [1]\n",
			path:  paths.MustParse("$.a"),
			kind:  niceyaml.NodeSequence,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var opts []niceyaml.SourceOption

			if tc.refs != "" {
				opts = append(opts, yamltest.References(tc.refs))
			}

			n := yamltest.AtOrSelf(t, yamltest.FirstDocument(t, tc.input, opts...), tc.path)

			assert.Equal(t, tc.kind, n.Kind())
			assert.Equal(t, tc.empty, n.IsEmpty())
		})
	}

	t.Run("nil node", func(t *testing.T) {
		t.Parallel()

		var n *niceyaml.Node

		assert.Equal(t, niceyaml.NodeNone, n.Kind())
		assert.False(t, n.IsEmpty())
	})

	t.Run("document that did not parse", func(t *testing.T) {
		t.Parallel()

		// The Node of such a document holds no body, with a header above
		// it or without, and the document is not empty.
		for _, input := range []string{"a: [\n", stringtest.LinesLF("---", "a: [")} {
			doc := yamltest.UnparsedDocument(t, input)

			assert.Equal(t, niceyaml.NodeNone, doc.Kind(), input)
			assert.False(t, doc.IsEmpty(), input)
			require.ErrorIs(t, doc.Err(), niceyaml.ErrSyntax)
		}
	})

	t.Run("kind of each document", func(t *testing.T) {
		t.Parallel()

		docs := yamltest.AllDocuments(t, niceyaml.NewSourceFromString(stringtest.LinesLF(
			"a: 1",
			"---",
			"- b",
			"---",
			"c",
			"---",
		)), 4)

		got := make([]niceyaml.NodeKind, 0, len(docs))
		for _, doc := range docs {
			got = append(got, doc.Kind())
		}

		assert.Equal(t, []niceyaml.NodeKind{
			niceyaml.NodeMapping,
			niceyaml.NodeSequence,
			niceyaml.NodeScalar,
			niceyaml.NodeNone,
		}, got)
	})

	t.Run("emptiness of each document", func(t *testing.T) {
		t.Parallel()

		docs := yamltest.AllDocuments(t, niceyaml.NewSourceFromString(stringtest.LinesLF(
			"a: 1",
			"---",
			"# Source: chart/templates/empty.yaml",
			"---",
			"--- ~",
			"--- *nope",
			"---",
			"b: 2",
			"---",
		)), 7)

		got := make([]bool, 0, len(docs))
		for _, doc := range docs {
			got = append(got, doc.IsEmpty())
		}

		assert.Equal(t, []bool{false, true, true, false, false, false, true}, got)
	})

	t.Run("each match", func(t *testing.T) {
		t.Parallel()

		doc := yamltest.FirstDocument(t, stringtest.JoinLF(
			"items:",
			"  - {a: 1}",
			"  - [1]",
			"  - x",
			"  -",
			"  - &n !!map {b: 2}",
			"  - *n",
		))

		items, err := doc.Nodes(paths.Current().Child("items").IndexAll())
		require.NoError(t, err)

		got := make([]niceyaml.NodeKind, 0, len(items))
		for _, item := range items {
			got = append(got, item.Kind())
		}

		assert.Equal(t, []niceyaml.NodeKind{
			niceyaml.NodeMapping,
			niceyaml.NodeSequence,
			niceyaml.NodeScalar,
			niceyaml.NodeScalar,
			niceyaml.NodeMapping,
			niceyaml.NodeMapping,
		}, got)
	})

	t.Run("node at the root path", func(t *testing.T) {
		t.Parallel()

		// The root path of an empty document below a header selects the
		// null at the header, and the Node there is the root.
		empty := yamltest.FirstDocument(t, "---\n")
		assert.Same(t, empty, yamltest.At(t, empty, paths.Doc()))
		assert.True(t, yamltest.At(t, empty, paths.Doc()).IsEmpty())

		nodes, err := empty.Nodes(paths.Doc())
		require.NoError(t, err)
		require.Len(t, nodes, 1)
		assert.Same(t, empty, nodes[0])
		assert.True(t, nodes[0].IsEmpty())

		full := yamltest.FirstDocument(t, "a: 1\n")
		assert.False(t, yamltest.At(t, full, paths.Doc()).IsEmpty())
	})
}

func TestNode_Target(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		opts  []niceyaml.SourceOption
		input string
		path  paths.Path
		// The path of the target, or empty when the target is the Node.
		want string
		// The text of the AST of the target.
		wantAST string
		span    position.Span
		// Whether the target lies in a reference Source.
		reference bool
	}{
		"alias": {
			input:   stringtest.LinesLF("base: &x", "  port: 1", "other: 1", "c: *x"),
			path:    paths.Doc().Child("c"),
			want:    "$.base",
			wantAST: "  port: 1",
			span:    position.NewSpan(0, 2),
		},
		"tagged alias": {
			input:   stringtest.LinesLF("base: &x {port: 1}", "d: !t *x"),
			path:    paths.Doc().Child("d"),
			want:    "$.base",
			wantAST: "{port: 1}",
			span:    position.NewSpan(0, 1),
		},
		"alias element": {
			input:   stringtest.LinesLF("list:", "  - &e {q: 1}", "g: *e"),
			path:    paths.Doc().Child("g"),
			want:    "$.list[0]",
			wantAST: "{q: 1}",
			span:    position.NewSpan(1, 2),
		},
		"anchor on a merge value": {
			input:   stringtest.LinesLF("m:", "  <<: &mm {z: 1}", "f: *mm"),
			path:    paths.Doc().Child("f"),
			want:    "$.m.<<",
			wantAST: "{z: 1}",
			span:    position.NewSpan(1, 2),
		},
		"anchor on a hidden entry": {
			opts:    []niceyaml.SourceOption{niceyaml.WithAllowDuplicateKeys(true)},
			input:   stringtest.LinesLF("a: &x {k: 1}", "a: 2", "c: *x"),
			path:    paths.Doc().Child("c"),
			want:    "$.a",
			wantAST: "{k: 1}",
			span:    position.NewSpan(0, 1),
		},
		"anchor on a hidden block entry": {
			opts:    []niceyaml.SourceOption{niceyaml.WithAllowDuplicateKeys(true)},
			input:   stringtest.LinesLF("a: &x", "  k: 1", "a: 2", "c: *x"),
			path:    paths.Doc().Child("c"),
			want:    "$.a",
			wantAST: "  k: 1",
			span:    position.NewSpan(0, 2),
		},
		"anchor on a hidden merge value": {
			opts:    []niceyaml.SourceOption{niceyaml.WithAllowDuplicateKeys(true)},
			input:   stringtest.LinesLF("m:", "  <<: &x {k: 1}", "  <<: {j: 2}", "c: *x"),
			path:    paths.Doc().Child("c"),
			want:    "$.m.<<",
			wantAST: "{k: 1}",
			span:    position.NewSpan(1, 2),
		},
		"alias inside its own anchor": {
			input:   "a: &x {b: *x}\n",
			path:    paths.Doc().Child("a", "b"),
			want:    "$.a",
			wantAST: "{b: *x}",
			span:    position.NewSpan(0, 1),
		},
		"alias to a reference": {
			opts:      []niceyaml.SourceOption{yamltest.References("other: 1\nbase: &base {port: 80}\n")},
			input:     "server: *base\n",
			path:      paths.Doc().Child("server"),
			want:      "$.base",
			wantAST:   "{port: 80}",
			span:      position.NewSpan(1, 2),
			reference: true,
		},
		"not an alias": {
			input: stringtest.LinesLF("base: &x {port: 1}", "c: *x"),
			path:  paths.Doc().Child("base"),
		},
		"path through an alias": {
			input: stringtest.LinesLF("base: &x {port: 1}", "c: *x"),
			path:  paths.Doc().Child("c", "port"),
		},
		"root": {
			input: "a: 1\n",
			path:  paths.Doc(),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			doc := yamltest.FirstDocument(t, tc.input, tc.opts...)
			n := yamltest.AtOrSelf(t, doc, tc.path)

			got, err := n.Target()
			require.NoError(t, err)

			if tc.want == "" {
				assert.Same(t, n, got)

				return
			}

			assert.NotSame(t, n, got)
			assert.Equal(t, tc.want, got.Path().String())
			assert.Equal(t, tc.wantAST, got.AST().String())
			assert.Equal(t, tc.span, got.Span())
			assert.Equal(t, tc.reference, got.Source() != doc.Source())

			// The Node at the alias decodes what the target decodes.
			want, err := got.Decode[any](t.Context())
			require.NoError(t, err)

			if name != "alias inside its own anchor" {
				value, err := n.Decode[any](t.Context())
				require.NoError(t, err)
				assert.Equal(t, want, value)
			}
		})
	}

	t.Run("anchor below a hidden entry", func(t *testing.T) {
		t.Parallel()

		doc := yamltest.FirstDocument(t, stringtest.LinesLF(
			"a:",
			"  b: &x {k: 1}",
			"a: 2",
			"c: *x",
		), niceyaml.WithAllowDuplicateKeys(true), niceyaml.WithName("a.yaml"))

		c := yamltest.At(t, doc, paths.Doc().Child("c"))

		got, err := c.Target()
		require.NoError(t, err)

		// The path spells the anchor, but the later entry of a holds the
		// scalar 2, so the path selects no node.
		assert.Equal(t, "$.a.b", got.Path().String())
		assert.Equal(t, "{k: 1}", got.AST().String())
		assert.Equal(t, position.NewSpan(1, 2), got.Span())

		_, err = doc.At(got.Path())
		require.Error(t, err)

		// An error bound at the root of the target resolves by its path,
		// which reaches the later entry, outside the view of the target.
		bound := yamltest.RequireSourceError(t, got.Bind(niceyaml.NewError("bad")))
		at, ok := bound.Position()
		require.True(t, ok)
		assert.Equal(t, position.New(2, 3), at)
		assert.False(t, bound.Annotate(got.View()))

		// A decode that rejects the content binds at the content.
		_, err = got.Decode[int](t.Context())
		require.Error(t, err)

		bound = yamltest.RequireSourceError(t, err)
		assert.True(t, bound.Annotate(got.View()))
	})

	t.Run("an alias without an anchor", func(t *testing.T) {
		t.Parallel()

		doc := yamltest.FirstDocument(t, "*nope\n")

		_, err := doc.Target()
		require.ErrorIs(t, err, paths.ErrAlias)
	})

	t.Run("a nil Node", func(t *testing.T) {
		t.Parallel()

		var n *niceyaml.Node

		got, err := n.Target()
		require.NoError(t, err)
		assert.Nil(t, got)
	})

	t.Run("a document that did not parse", func(t *testing.T) {
		t.Parallel()

		doc := yamltest.UnparsedDocument(t, "a: [\n")

		_, err := doc.Target()
		require.ErrorIs(t, err, niceyaml.ErrSyntax)
	})
}

// A Node at an alias describes the alias as the document writes it, and
// a decode reads the content of the anchor. An error below the alias binds
// at the anchor, which the view of the target holds.
func TestNode_Target_errorBelowAlias(t *testing.T) {
	t.Parallel()

	type T struct {
		Port string `yaml:"port"`
	}

	doc := yamltest.FirstDocument(t, stringtest.LinesLF(
		"base: &x",
		"  port: [1]",
		"other: 1",
		"c: *x",
	), niceyaml.WithName("a.yaml"))

	c := yamltest.At(t, doc, paths.Doc().Child("c"))
	assert.IsType(t, &ast.AliasNode{}, c.AST())
	assert.Equal(t, position.NewSpan(3, 4), c.Span())
	assert.Equal(t, niceyaml.NodeMapping, c.Kind())

	tks := c.Tokens()
	require.NotEmpty(t, tks)
	assert.Equal(t, token.AliasType, tks[0].Type)

	target, err := c.Target()
	require.NoError(t, err)

	var v T

	err = c.DecodeInto(t.Context(), &v)
	require.Error(t, err)

	bound := yamltest.RequireSourceError(t, err)
	assert.False(t, bound.Annotate(c.View()))
	assert.True(t, bound.Annotate(target.View()))
}

// A path that the content of an anchor does not hold binds at the alias
// that reads the anchor. An error at the root of a Node at the alias binds
// there too. The view of that Node holds the alias even when the alias
// sits on the line after its key.
func TestNode_Bind_missingBelowAlias(t *testing.T) {
	t.Parallel()

	ref := niceyaml.NewSourceFromString("defaults: &r {a: 1}\n", niceyaml.WithName("ref.yaml"))

	tcs := map[string]struct {
		input string
		scope paths.Path
		path  paths.Path
		want  string
		// Whether the view of the Node at scope marks the error.
		marked bool
	}{
		"alias on the line after its key": {
			input:  "base: &x\n  port: 1\nc:\n  *x\n",
			scope:  paths.Doc().Child("c"),
			path:   paths.Current().Child("zz"),
			want:   "a.yaml:4:3: $.c.zz: missing",
			marked: true,
		},
		"alias on the line of its key": {
			input:  "base: &x\n  port: 1\nc: *x\n",
			scope:  paths.Doc().Child("c"),
			path:   paths.Current().Child("zz"),
			want:   "a.yaml:3:4: $.c.zz: missing",
			marked: true,
		},
		"tagged alias on the line after its key": {
			input:  "base: &x\n  port: 1\nc: !t\n  *x\n",
			scope:  paths.Doc().Child("c"),
			path:   paths.Current().Child("zz"),
			want:   "a.yaml:4:3: $.c.zz: missing",
			marked: true,
		},
		"alias element": {
			input:  "base: &x\n  port: 1\nc:\n  - *x\n",
			scope:  paths.Doc().Child("c").Index(0),
			path:   paths.Current().Child("zz"),
			want:   "a.yaml:4:5: $.c[0].zz: missing",
			marked: true,
		},
		"alias below a scope": {
			input:  "base: &x\n  port: 1\nc:\n  *x\n",
			scope:  paths.Doc(),
			path:   paths.Current().Child("c").Child("zz"),
			want:   "a.yaml:4:3: $.c.zz: missing",
			marked: true,
		},
		"alias of an anchor with a merge key of a reference": {
			input:  "base: &x\n  <<: *r\n  port: 1\nc:\n  *x\n",
			scope:  paths.Doc().Child("c"),
			path:   paths.Current().Child("zz"),
			want:   "a.yaml:5:3: $.c.zz: missing",
			marked: true,
		},
		"path through the alias binds at the anchor": {
			input: "base: &x\n  port: 1\nc:\n  *x\n",
			scope: paths.Doc().Child("c"),
			path:  paths.Current().Child("port").Child("zz"),
			want:  "a.yaml:2:9: $.c.port.zz: missing",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			doc := yamltest.FirstDocument(t, tc.input, niceyaml.WithName("a.yaml"), niceyaml.WithReferences(ref))
			scope := yamltest.At(t, doc, tc.scope)

			err := scope.Bind(niceyaml.NewError("missing", niceyaml.AtPath(tc.path)))
			require.EqualError(t, err, tc.want)

			bound := yamltest.RequireSourceError(t, err)
			assert.Equal(t, tc.marked, bound.Annotate(scope.View()))
		})
	}
}

func TestNode_At_Root(t *testing.T) {
	t.Parallel()

	// The preamble and the blank line keep the root apart from its body,
	// whose lines start at "a: 1".
	doc := yamltest.FirstDocument(t, stringtest.Input(`
		# hi
		---
		a: 1

		b:
		  c: 2
	`))
	b := yamltest.At(t, doc, paths.Doc().Child("b"))

	nodes, err := doc.Nodes(paths.Doc())
	require.NoError(t, err)
	require.Len(t, nodes, 1)

	blank, ok := doc.NodeAt(position.New(3, 0))
	require.True(t, ok)

	tcs := map[string]struct {
		node *niceyaml.Node
	}{
		"At $":            {node: yamltest.At(t, doc, paths.Doc())},
		"At @":            {node: yamltest.At(t, doc, paths.Current())},
		"At $ of a scope": {node: yamltest.At(t, b, paths.Doc())},
		"Nodes $":         {node: nodes[0]},
		"NodeAt":          {node: blank},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			// A document has one Node at `$`: the root, which spans the
			// whole document and holds its preamble.
			assert.Same(t, doc, tc.node)
			assert.Same(t, tc.node, tc.node.Document())
			assert.True(t, tc.node.Path().IsRoot())
			assert.Equal(t, position.NewSpan(0, 6), tc.node.Span())
			assert.Equal(t, doc.Tokens(), tc.node.Tokens())
		})
	}
}

// pathsAt lists what [niceyaml.Node.PathAt] reports for every position of
// source, one row per run of columns with one answer: the 0-indexed line
// and the first column of the run, then the path, or "-" where no
// document reports one. The columns of a line run one past its last
// character. A source of several documents puts the index of the document
// that reports the path in front of it.
//
// It fails the test when two documents report a path for one position,
// and when a path it lists does not resolve in its document.
func pathsAt(t *testing.T, source *niceyaml.Source) string {
	t.Helper()

	// A document that did not parse still has a Node, so the error is not
	// one of the test.
	docs := source.AllDocuments()

	var sb strings.Builder

	for i, l := range source.Lines().All() {
		prev := ""

		for col := range utf8.RuneCountInString(l.Content()) + 1 {
			pos := position.New(i, col)
			got := "-"

			for _, doc := range docs {
				path, ok := doc.PathAt(pos)
				if !ok {
					continue
				}

				require.Equal(t, "-", got, "two documents report a path at %s", pos)

				_, err := doc.At(path)
				require.NoError(t, err, "path %s at %s", path, pos)

				got = path.String()
				if len(docs) > 1 {
					got = fmt.Sprintf("#%d %s", doc.DocumentIndex(), path)
				}
			}

			if got != prev {
				fmt.Fprintf(&sb, "%s %s\n", pos, got)

				prev = got
			}
		}
	}

	return strings.TrimSuffix(sb.String(), "\n")
}

func TestNode_PathAt(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input string
		want  string
		opts  []niceyaml.SourceOption
	}{
		"block mapping and sequence": {
			input: stringtest.JoinLF(
				"# head",
				"servers:",
				"  - host: h  # note",
				"    port: 1",
				"",
				"  - host: g",
				"name: x",
			),
			want: stringtest.JoinLF(
				"0:0 -",
				"1:0 $.servers~",
				"1:7 $.servers",
				"1:8 -",
				"2:0 $.servers[0]",
				"2:3 $.servers[0].host~",
				"2:8 $.servers[0].host",
				"2:11 -",
				"3:0 $.servers[0].port~",
				"3:8 $.servers[0].port",
				"3:11 -",
				"4:0 -",
				"5:0 $.servers[1]",
				"5:3 $.servers[1].host~",
				"5:8 $.servers[1].host",
				"5:11 -",
				"6:0 $.name~",
				"6:4 $.name",
				"6:7 -",
			),
		},
		"flow collections": {
			input: stringtest.JoinLF("x: [a: 1, {b: 2}, [c],]", "y: {p: 1, q: 2,}"),
			want: stringtest.JoinLF(
				"0:0 $.x~",
				"0:1 $.x",
				"0:4 $.x[0].a~",
				"0:5 $.x[0].a",
				"0:8 $.x",
				"0:9 $.x[1]",
				"0:11 $.x[1].b~",
				"0:12 $.x[1].b",
				"0:15 $.x[1]",
				"0:16 $.x",
				"0:17 $.x[2]",
				"0:19 $.x[2][0]",
				"0:20 $.x[2]",
				"0:21 $.x",
				"0:23 -",
				"1:0 $.y~",
				"1:1 $.y",
				"1:4 $.y.p~",
				"1:5 $.y.p",
				"1:8 $.y",
				"1:9 $.y.q~",
				"1:11 $.y.q",
				"1:14 $.y",
				"1:16 -",
			),
		},
		"explicit key, anchor, tag, and alias": {
			input: stringtest.JoinLF(
				"? k",
				": &anc !!str w",
				"z: *anc",
			),
			want: stringtest.JoinLF(
				"0:0 $.k~",
				"0:3 -",
				"1:0 $.k",
				"1:12 -",
				"1:13 $.k",
				"1:14 -",
				"2:0 $.z~",
				"2:1 $.z",
				"2:7 -",
			),
		},
		"scalars over several lines": {
			input: stringtest.JoinLF(
				"a: |",
				"b:",
				"c: |",
				"  text",
				"  more",
				`d: "q`,
				`  r"`,
			),
			want: stringtest.JoinLF(
				"0:0 $.a~",
				"0:1 $.a",
				"0:4 -",
				"1:0 $.b~",
				"1:1 $.b",
				"1:2 -",
				"2:0 $.c~",
				"2:1 $.c",
				"2:4 -",
				"3:0 $.c",
				"3:6 -",
				"4:0 $.c",
				"4:6 -",
				"5:0 $.d~",
				"5:1 $.d",
				"5:5 -",
				"6:0 $.d",
				"6:4 -",
			),
		},
		"scalar at the root": {
			input: "abc",
			want: stringtest.JoinLF(
				"0:0 $",
				"0:3 -",
			),
		},
		"sequence at the root": {
			input: stringtest.JoinLF(
				"- a",
				"-",
				"- b: 1",
				"  c: 2",
				"- - x",
				"  - y",
			),
			want: stringtest.JoinLF(
				"0:0 $[0]",
				"0:3 -",
				"1:0 $[1]",
				"1:1 -",
				"2:0 $[2]",
				"2:1 $[2].b~",
				"2:3 $[2].b",
				"2:6 -",
				"3:0 $[2].c~",
				"3:3 $[2].c",
				"3:6 -",
				"4:0 $[3]",
				"4:1 $[3][0]",
				"4:5 -",
				"5:0 $[3][1]",
				"5:5 -",
			),
		},
		"tag at the root between markers": {
			input: stringtest.JoinLF(
				"--- !!map",
				"a: 1",
				"...",
			),
			want: stringtest.JoinLF(
				"0:0 -",
				"0:3 $",
				"0:9 -",
				"1:0 $.a~",
				"1:1 $.a",
				"1:4 -",
				"2:0 -",
			),
		},
		"directive and comments": {
			input: stringtest.JoinLF(
				"%YAML 1.2",
				"---",
				"a: 1 # c",
				"# foot",
			),
			want: stringtest.JoinLF(
				"0:0 -",
				"1:0 -",
				"2:0 $.a~",
				"2:1 $.a",
				"2:4 -",
				"3:0 -",
			),
		},
		"merge key and the content of its anchor": {
			input: stringtest.JoinLF(
				"defs: &d",
				"  port: 1",
				"servers:",
				"  - <<: *d",
				"    host: &h x",
				"    other: *h",
			),
			want: stringtest.JoinLF(
				"0:0 $.defs~",
				"0:4 $.defs",
				"0:8 -",
				"1:0 $.defs.port~",
				"1:6 $.defs.port",
				"1:9 -",
				"2:0 $.servers~",
				"2:7 $.servers",
				"2:8 -",
				"3:0 $.servers[0]",
				"3:3 $.servers[0].<<~",
				"3:6 $.servers[0].<<",
				"3:10 -",
				"4:0 $.servers[0].host~",
				"4:8 $.servers[0].host",
				"4:14 -",
				"5:0 $.servers[0].other~",
				"5:9 $.servers[0].other",
				"5:13 -",
			),
		},
		"entry a later merge key overrides": {
			input: stringtest.JoinLF(
				"base: &b",
				"  k: 1",
				"m:",
				"  k: 2",
				"  <<: *b",
				"  j: 3",
			),
			want: stringtest.JoinLF(
				"0:0 $.base~",
				"0:4 $.base",
				"0:8 -",
				"1:0 $.base.k~",
				"1:3 $.base.k",
				"1:6 -",
				"2:0 $.m~",
				"2:1 $.m",
				"2:2 -",
				"3:0 -",
				"4:0 $.m.<<~",
				"4:4 $.m.<<",
				"4:8 -",
				"5:0 $.m.j~",
				"5:3 $.m.j",
				"5:6 -",
			),
		},
		"entry after the merge key that holds its name": {
			input: stringtest.JoinLF(
				"base: &b",
				"  k: 1",
				"m:",
				"  <<: *b",
				"  k: 2",
			),
			want: stringtest.JoinLF(
				"0:0 $.base~",
				"0:4 $.base",
				"0:8 -",
				"1:0 $.base.k~",
				"1:3 $.base.k",
				"1:6 -",
				"2:0 $.m~",
				"2:1 $.m",
				"2:2 -",
				"3:0 $.m.<<~",
				"3:4 $.m.<<",
				"3:8 -",
				"4:0 $.m.k~",
				"4:3 $.m.k",
				"4:6 -",
			),
		},
		"earlier of two entries with one key": {
			input: stringtest.JoinLF(
				"a:",
				"  x: 1",
				"a:",
				"  x: 2",
				"b: 3",
			),
			opts: []niceyaml.SourceOption{niceyaml.WithAllowDuplicateKeys(true)},
			want: stringtest.JoinLF(
				"0:0 -",
				"1:0 -",
				"2:0 $.a~",
				"2:1 $.a",
				"2:2 -",
				"3:0 $.a.x~",
				"3:3 $.a.x",
				"3:6 -",
				"4:0 $.b~",
				"4:1 $.b",
				"4:4 -",
			),
		},
		"earlier of two merge keys": {
			input: stringtest.JoinLF(
				"k: v",
				"<<: {a: 1}",
				"<<: {b: 2}",
			),
			opts: []niceyaml.SourceOption{niceyaml.WithAllowDuplicateKeys(true)},
			want: stringtest.JoinLF(
				"0:0 $.k~",
				"0:1 $.k",
				"0:4 -",
				"1:0 -",
				"2:0 $.<<~",
				"2:2 $.<<",
				"2:5 $.<<.b~",
				"2:6 $.<<.b",
				"2:9 $.<<",
				"2:10 -",
			),
		},
		"alias key": {
			input: stringtest.JoinLF("a: &k name", "*k : w"),
			want: stringtest.JoinLF(
				"0:0 $.a~",
				"0:1 $.a",
				"0:10 -",
				"1:0 $.name~",
				"1:2 $.name",
				"1:6 -",
			),
		},
		"key with no name": {
			input: stringtest.JoinLF("b: 2", "*nope : 1"),
			want: stringtest.JoinLF(
				"0:0 $.b~",
				"0:1 $.b",
				"0:4 -",
				"1:0 -",
			),
		},
		"keys a path quotes": {
			input: stringtest.JoinLF(
				"0x10: a",
				`'a.b': {"c d": 1}`,
				`"": e`,
			),
			want: stringtest.JoinLF(
				"0:0 $.0x10~",
				"0:4 $.0x10",
				"0:7 -",
				"1:0 $.'a.b'~",
				"1:5 $.'a.b'",
				"1:8 $.'a.b'.'c d'~",
				"1:13 $.'a.b'.'c d'",
				"1:16 $.'a.b'",
				"1:17 -",
				"2:0 $.''~",
				"2:2 $.''",
				"2:5 -",
			),
		},
		"several documents": {
			input: stringtest.JoinLF(
				"a: 1",
				"---",
				"b: 2",
				"---",
				"- c",
			),
			want: stringtest.JoinLF(
				"0:0 #0 $.a~",
				"0:1 #0 $.a",
				"0:4 -",
				"1:0 -",
				"2:0 #1 $.b~",
				"2:1 #1 $.b",
				"2:4 -",
				"3:0 -",
				"4:0 #2 $[0]",
				"4:3 -",
			),
		},
		"document that does not parse": {
			input: stringtest.JoinLF(
				"a: [1, 2",
				"---",
				"b: 2",
			),
			want: stringtest.JoinLF(
				"0:0 -",
				"1:0 -",
				"2:0 #1 $.b~",
				"2:1 #1 $.b",
				"2:4 -",
			),
		},
		"comment alone":    {input: "# only", want: "0:0 -"},
		"empty source":     {input: ""},
		"whitespace alone": {input: "   ", want: "0:0 -"},
		"header alone":     {input: "---", want: "0:0 -"},
		"characters wider than a byte": {
			input: stringtest.JoinLF("名前: 値", "é: [ü]"),
			want: stringtest.JoinLF(
				"0:0 $.名前~",
				"0:2 $.名前",
				"0:5 -",
				"1:0 $.é~",
				"1:1 $.é",
				"1:4 $.é[0]",
				"1:5 $.é",
				"1:6 -",
			),
		},
		"CRLF line endings": {
			input: stringtest.LinesCRLF(
				"a: 1",
				"b:",
				"  - c",
			),
			want: stringtest.JoinLF(
				"0:0 $.a~",
				"0:1 $.a",
				"0:4 -",
				"1:0 $.b~",
				"1:1 $.b",
				"1:2 -",
				"2:0 $.b[0]",
				"2:5 -",
			),
		},
		"tab after a value": {
			input: stringtest.JoinLF("a: 1\t# c", "b: 2"),
			want: stringtest.JoinLF(
				"0:0 $.a~",
				"0:1 $.a",
				"0:4 -",
				"1:0 $.b~",
				"1:1 $.b",
				"1:4 -",
			),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			source := niceyaml.NewSourceFromString(tc.input, tc.opts...)

			assert.Equal(t, tc.want, pathsAt(t, source))
		})
	}
}

func TestNode_PathAt_File(t *testing.T) {
	t.Parallel()

	source := niceyaml.NewSourceFromString(yamltest.ReadFile(t, filepath.Join("testdata", "full.yaml")))
	docs := yamltest.Documents(t, source, 2)

	// A directive holds plain tokens after its "%", which belong to no
	// node either.
	directives := map[int]bool{}

	for _, tk := range source.Tokens() {
		if tk.Type == token.DirectiveType {
			directives[tk.Position.Line] = true
		}
	}

	found := 0

	for _, tk := range source.Tokens() {
		pos := position.NewFromToken(tk)

		var got []string

		for _, doc := range docs {
			path, ok := doc.PathAt(pos)
			if !ok {
				continue
			}

			_, err := doc.At(path)
			require.NoError(t, err, "path %s at %s", path, pos)

			got = append(got, path.String())
		}

		switch tk.Type {
		case token.CommentType, token.DirectiveType, token.DocumentHeaderType, token.DocumentEndType:
			assert.Empty(t, got, "%s token at %s", tk.Type, pos)

		default:
			if directives[tk.Position.Line] {
				assert.Empty(t, got, "directive token at %s", pos)

				continue
			}

			// One document holds every other token, as a node or
			// between the nodes of a collection.
			assert.Len(t, got, 1, "%s token %q at %s", tk.Type, tk.Value, pos)

			found++
		}
	}

	assert.Greater(t, found, 1000)
}

func TestNode_PathAt_NoPath(t *testing.T) {
	t.Parallel()

	doc := yamltest.FirstDocument(t, stringtest.LinesLF("a: 1", "b: 2"))

	tcs := map[string]struct {
		node *niceyaml.Node
		line int
		col  int
	}{
		"negative line":        {node: doc, line: -1},
		"negative column":      {node: doc, col: -1},
		"line past the last":   {node: doc, line: 2},
		"line far past":        {node: doc, line: 100},
		"column past the line": {node: doc, col: 100},
		"nil node":             {},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, ok := tc.node.PathAt(position.New(tc.line, tc.col))
			assert.False(t, ok)
			assert.Equal(t, paths.Current(), got)
		})
	}
}

func TestNode_PathAt_SelectsNode(t *testing.T) {
	t.Parallel()

	doc := yamltest.FirstDocument(t, stringtest.Input(`
		defs: &d
		  port: 8080
		servers:
		  - <<: *d
		    host: "a.example"
		  - name: [x, yy]
	`))

	tcs := map[string]struct {
		line int
		col  int
		want string
	}{
		"key":                         {line: 1, col: 3, want: "$.defs.port~"},
		"scalar value":                {line: 1, col: 10, want: "$.defs.port"},
		"key under an element":        {line: 4, col: 5, want: "$.servers[0].host~"},
		"quoted value":                {line: 4, col: 12, want: "$.servers[0].host"},
		"element of a flow sequence":  {line: 5, col: 15, want: "$.servers[1].name[1]"},
		"key of a merge key":          {line: 3, col: 4, want: "$.servers[0].<<~"},
		"alias under a merge key":     {line: 3, col: 8, want: "$.servers[0].<<"},
		"first key of a root mapping": {line: 0, col: 0, want: "$.defs~"},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			pos := position.New(tc.line, tc.col)

			got, ok := doc.PathAt(pos)
			require.True(t, ok)
			assert.Equal(t, tc.want, got.String())
			assert.True(t, paths.MustParse(tc.want).Equal(got))

			// The path resolves to the token at the position.
			ranges, err := doc.Ranges(got)
			require.NoError(t, err)
			require.Len(t, ranges, 1)
			assert.True(t, ranges[0].Contains(pos), "ranges %s", ranges)
		})
	}
}

func TestNode_PathAt_AnchorContent(t *testing.T) {
	t.Parallel()

	// The content of an anchor has one place in the document, where the
	// anchor defines it, whatever alias or merge key a path reaches it
	// through.
	doc := yamltest.FirstDocument(t, stringtest.Input(`
		defs: &d
		  port: 8080
		servers:
		  - <<: *d
		copy: *d
	`))

	for _, through := range []string{"$.defs.port", "$.servers[0].port", "$.copy.port"} {
		ranges, err := doc.Ranges(paths.MustParse(through))
		require.NoError(t, err)
		require.Len(t, ranges, 1)
		assert.Equal(t, position.New(1, 8), ranges[0].Start, through)

		got, ok := doc.PathAt(ranges[0].Start)
		require.True(t, ok, through)
		assert.Equal(t, "$.defs.port", got.String(), through)
	}
}

func TestNode_PathAt_ScopedNode(t *testing.T) {
	t.Parallel()

	doc := yamltest.FirstDocument(t, stringtest.Input(`
		spec:
		  hours:
		    open: 9
		name: x
	`))
	hours := yamltest.At(t, doc, paths.Doc().Child("spec", "hours"))

	tcs := map[string]struct {
		want string
		line int
		col  int
		ok   bool
	}{
		"inside the scope":       {line: 2, col: 10, want: "$.spec.hours.open", ok: true},
		"key of the scope":       {line: 1, col: 2, want: "$.spec.hours~", ok: true},
		"outside the scope":      {line: 3, col: 6, want: "$.name"},
		"parent outside a scope": {line: 0, col: 0, want: "$.spec~"},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			pos := position.New(tc.line, tc.col)

			// The root answers for every position of the document.
			want, ok := doc.PathAt(pos)
			require.True(t, ok)
			assert.Equal(t, tc.want, want.String())

			fromRoot, ok := hours.Document().PathAt(pos)
			require.True(t, ok)
			assert.Equal(t, want, fromRoot)

			// A scoped Node answers within its scope, as NodeAt does.
			got, ok := hours.PathAt(pos)
			_, nodeOK := hours.NodeAt(pos)
			assert.Equal(t, tc.ok, nodeOK)

			if !tc.ok {
				assert.False(t, ok)
				assert.Equal(t, paths.Path{}, got)

				return
			}

			require.True(t, ok)
			assert.Equal(t, want, got)

			// The path starts at `$`, so it goes back through the scoped
			// Node and selects the node at pos.
			assert.Equal(t, got, yamltest.At(t, hours, got).Path())

			ranges, err := hours.Ranges(got)
			require.NoError(t, err)
			require.NotEmpty(t, ranges)
			assert.Equal(t, tc.line, ranges[0].Start.Line)
		})
	}

	t.Run("the path of the scoped Node selects the Node", func(t *testing.T) {
		t.Parallel()

		self := yamltest.At(t, hours, hours.Path())
		assert.Equal(t, hours.Path(), self.Path())
		assert.Equal(t, hours.Span(), self.Span())
		assert.Same(t, hours.AST(), self.AST())
	})
}

// TestNode_Nodes_OneLine verifies that the nodes of a document on a single
// line, as a minified JSON document is, each find their own columns of
// that line.
func TestNode_Nodes_OneLine(t *testing.T) {
	t.Parallel()

	const items = 300

	doc := oneLineDocument(t, items)
	width := doc.Source().Lines().Line(0).Width()

	found, err := doc.Nodes(paths.Current().IndexAll())
	require.NoError(t, err)
	require.Len(t, found, items)

	for i, item := range found {
		// Each item takes 22 columns and the ", " after it. The first
		// starts after the "[" that opens the sequence.
		start := 1 + 24*i

		require.Equal(t, paths.Doc().Index(i), item.Path())
		require.Equal(t, position.NewSpan(0, 1), item.Span())
		require.Equal(t, yamltest.LineRange(0, start, start+22), yamltest.RangeOf(t, item))

		name := yamltest.At(t, item, paths.Current().Child("name"))
		require.Equal(t, yamltest.LineRange(0, start+18, start+21), yamltest.RangeOf(t, name))

		at, ok := doc.NodeAt(position.New(0, start+7))
		require.True(t, ok)
		require.Equal(t, paths.Doc().Index(i).Child("id"), at.Path())
	}

	// A position past the end of the line takes the node of the last text
	// before it, the "]" that closes the sequence.
	for _, col := range []int{width - 1, width, width + 1} {
		at, ok := doc.NodeAt(position.New(0, col))
		require.True(t, ok)
		assert.Equal(t, paths.Doc(), at.Path())
	}

	assert.Equal(t, yamltest.LineRange(0, 0, width), yamltest.RangeOf(t, doc))
}

// nodesAt lists what [niceyaml.Node.NodeAt] reports for every position of
// the lines of its source, one row per run of columns with one answer, as
// pathsAt lists paths. The columns of a line run two past its last
// character, so each line shows the answer past its end. It fails the
// test when the path of a Node it lists does not select that Node.
func nodesAt(t *testing.T, doc *niceyaml.Node) string {
	t.Helper()

	var sb strings.Builder

	for i, l := range doc.Source().Lines().All() {
		prev := ""

		for col := range utf8.RuneCountInString(l.Content()) + 2 {
			pos := position.New(i, col)
			got := "-"

			if n, ok := doc.NodeAt(pos); ok {
				same, err := doc.At(n.Path())
				require.NoError(t, err, "path %s at %s", n.Path(), pos)
				assert.Equal(t, same.Span(), n.Span(), "node at %s", pos)

				got = n.Path().String()
			}

			if got != prev {
				fmt.Fprintf(&sb, "%s %s\n", pos, got)

				prev = got
			}
		}
	}

	return strings.TrimSuffix(sb.String(), "\n")
}

func TestNode_NodeAt(t *testing.T) {
	t.Parallel()

	servers := stringtest.JoinLF(
		"# head",
		"servers:",
		"  - host: h  # note",
		"    port: 1",
		"",
		"  - host: g",
		"name: x",
	)

	tcs := map[string]struct {
		input string
		want  string
		path  paths.Path
		opts  []niceyaml.SourceOption
	}{
		"block mapping and sequence": {
			input: servers,
			want: stringtest.JoinLF(
				"0:0 $",
				"1:0 $.servers",
				"2:0 $.servers[0]",
				"2:3 $.servers[0].host",
				"3:0 $.servers[0].port",
				"4:0 $.servers[0]",
				"5:0 $.servers[1]",
				"5:3 $.servers[1].host",
				"6:0 $.name",
			),
		},
		"scoped node answers inside its node alone": {
			input: servers,
			path:  paths.Doc().Child("servers"),
			want: stringtest.JoinLF(
				"0:0 -",
				"1:0 $.servers",
				"2:0 $.servers[0]",
				"2:3 $.servers[0].host",
				"3:0 $.servers[0].port",
				"4:0 $.servers[0]",
				"5:0 $.servers[1]",
				"5:3 $.servers[1].host",
				"6:0 -",
			),
		},
		"flow mapping": {
			input: "flow: {a: 1, b: 2}",
			want: stringtest.JoinLF(
				"0:0 $.flow",
				"0:7 $.flow.a",
				"0:11 $.flow",
				"0:12 $.flow.b",
				"0:17 $.flow",
			),
		},
		// A position in the spaces after the text of a token takes the node
		// of the last text before it, whatever follows it on the line.
		"spaces after the values of a flow sequence": {
			input: "seq: [a , {b: 1 } , c ]  # note",
			want: stringtest.JoinLF(
				"0:0 $.seq",
				"0:6 $.seq[0]",
				"0:8 $.seq",
				"0:9 $.seq[1]",
				"0:11 $.seq[1].b",
				"0:16 $.seq[1]",
				"0:17 $.seq",
				"0:19 $.seq[2]",
				"0:22 $.seq",
			),
		},
		// The indentation of a line belongs to the token that ends on the
		// line before, so the tokens of the line do not cover it. A
		// position in it, or on a comment after it, takes the node of the
		// last text on the lines before.
		"comment on a line of its own": {
			input: stringtest.JoinLF("a:", "  b: 1", "      # note", "  c: 2"),
			want: stringtest.JoinLF(
				"0:0 $.a",
				"1:0 $.a.b",
				"2:0 $.a",
				"3:0 $.a.c",
			),
		},
		"spaces on the last line without a line ending": {
			input: stringtest.JoinLF("a:", "  b: 1", "      "),
			want: stringtest.JoinLF(
				"0:0 $.a",
				"1:0 $.a.b",
				"2:0 $.a",
			),
		},
		"end of an entry without a value": {
			input: stringtest.JoinLF("y:", "z: 1"),
			want: stringtest.JoinLF(
				"0:0 $.y",
				"1:0 $.z",
			),
		},
		"line of spaces after the last entry of a mapping": {
			input: stringtest.JoinLF("server:", "  host: a", "  ", "name: x"),
			want: stringtest.JoinLF(
				"0:0 $.server",
				"1:0 $.server.host",
				"2:0 $.server",
				"3:0 $.name",
			),
		},
		"blank line before the first entry of a mapping": {
			input: stringtest.JoinLF("server:", "", "  host: a"),
			want: stringtest.JoinLF(
				"0:0 $.server",
				"1:0 $.server",
				"2:0 $.server.host",
			),
		},
		"blank line in a block scalar": {
			input: stringtest.JoinLF("a: |", "  foo", "", "  bar", "b: 2"),
			want: stringtest.JoinLF(
				"0:0 $.a",
				"1:0 $.a",
				"2:0 $.a",
				"3:0 $.a",
				"4:0 $.b",
			),
		},
		"blank lines around the document": {
			input: stringtest.JoinLF("", "a: 1", "", ""),
			want: stringtest.JoinLF(
				"0:0 $",
				"1:0 $.a",
				"2:0 $",
			),
		},
		"directive, header, and end marker": {
			input: stringtest.JoinLF("%YAML 1.2", "---", "a: 1", "..."),
			want: stringtest.JoinLF(
				"0:0 $",
				"1:0 $",
				"2:0 $.a",
				"3:0 $",
			),
		},
		"entry that no path selects gives the mapping": {
			input: stringtest.JoinLF("a: 1", "a: 2"),
			opts:  []niceyaml.SourceOption{niceyaml.WithAllowDuplicateKeys(true)},
			want: stringtest.JoinLF(
				"0:0 $",
				"1:0 $.a",
			),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			doc := yamltest.FirstDocument(t, tc.input, tc.opts...)
			doc = yamltest.AtOrSelf(t, doc, tc.path)

			assert.Equal(t, tc.want, nodesAt(t, doc))
		})
	}
}

func TestNode_NodeAt_NoNode(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		node *niceyaml.Node
		pos  position.Position
	}{
		"nil node":           {node: nil},
		"unparsed":           {node: yamltest.UnparsedDocument(t, "a: [")},
		"empty document":     {node: yamltest.FirstDocument(t, "")},
		"line before source": {node: yamltest.FirstDocument(t, "a: 1\n"), pos: position.New(-1, 0)},
		"line past source":   {node: yamltest.FirstDocument(t, "a: 1\n"), pos: position.New(5, 0)},
		"negative column":    {node: yamltest.FirstDocument(t, "a: 1\n"), pos: position.New(0, -1)},
		"other document": {
			node: yamltest.Documents(t, niceyaml.NewSourceFromString("a: 1\n---\nb: 2\n"), 2)[0],
			pos:  position.New(2, 0),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, ok := tc.node.NodeAt(tc.pos)
			assert.False(t, ok)
		})
	}
}

func TestNode_Range(t *testing.T) {
	t.Parallel()

	doc := yamltest.FirstDocument(t, stringtest.JoinLF(
		"flow: {a: 1, b: 2}",
		"y:",
		"seq:",
		"  -",
		"  - item",
		"block: |",
		"  line1",
		"  line2",
		"empty: |",
		"map:",
		"  k: v  # note",
		"  # trailing",
		"text: first",
		"  second",
	))

	tcs := map[string]struct {
		path paths.Path
		want position.Range
	}{
		"root covers the first key through the last value": {
			path: paths.Doc(),
			want: position.NewRange(position.New(0, 0), position.New(13, 8)),
		},
		"flow mapping covers its braces": {
			path: paths.Doc().Child("flow"),
			want: yamltest.LineRange(0, 6, 18),
		},
		"entry of a flow mapping covers its value": {
			path: paths.Doc().Child("flow", "a"),
			want: yamltest.LineRange(0, 10, 11),
		},
		"last entry of a flow mapping": {
			path: paths.Doc().Child("flow", "b"),
			want: yamltest.LineRange(0, 16, 17),
		},
		"key path covers the key": {
			path: paths.Doc().Child("flow").Key(),
			want: yamltest.LineRange(0, 0, 4),
		},
		"implicit null is empty after the colon": {
			path: paths.Doc().Child("y"),
			want: yamltest.LineRange(1, 2, 2),
		},
		"block sequence covers its first dash through its last element": {
			path: paths.Doc().Child("seq"),
			want: position.NewRange(position.New(3, 2), position.New(4, 8)),
		},
		"element without a value is empty after the dash": {
			path: paths.Doc().Child("seq").Index(0),
			want: yamltest.LineRange(3, 3, 3),
		},
		"element covers its value": {
			path: paths.Doc().Child("seq").Index(1),
			want: yamltest.LineRange(4, 4, 8),
		},
		"block scalar covers its indicator through its last line": {
			path: paths.Doc().Child("block"),
			want: position.NewRange(position.New(5, 7), position.New(7, 7)),
		},
		"block scalar without text covers its indicator": {
			path: paths.Doc().Child("empty"),
			want: yamltest.LineRange(8, 7, 8),
		},
		"mapping leaves out the comments": {
			path: paths.Doc().Child("map"),
			want: yamltest.LineRange(10, 2, 6),
		},
		"scalar leaves out the comment after it": {
			path: paths.Doc().Child("map", "k"),
			want: yamltest.LineRange(10, 5, 6),
		},
		"scalar over several lines": {
			path: paths.Doc().Child("text"),
			want: position.NewRange(position.New(12, 6), position.New(13, 8)),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			n := yamltest.At(t, doc, tc.path)

			got, ok := n.Range()
			require.True(t, ok)
			assert.Equal(t, tc.want, got)

			// The range lies in the lines the node spans.
			assert.True(t, n.Span().Contains(got.Start.Line), "span %s holds %s", n.Span(), got)
			assert.True(t, n.Span().Contains(got.LastLine()), "span %s holds %s", n.Span(), got)
		})
	}
}

func TestNode_Range_NoRange(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		node *niceyaml.Node
	}{
		"nil node":       {node: nil},
		"unparsed":       {node: yamltest.UnparsedDocument(t, "a: [")},
		"empty document": {node: yamltest.FirstDocument(t, "")},
		"comments alone": {node: yamltest.FirstDocument(t, "# note\n")},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, ok := tc.node.Range()
			assert.False(t, ok)

			_, ok = tc.node.KeyRange()
			assert.False(t, ok)
		})
	}
}

func TestNode_Text_Document(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input string
		want  []string
	}{
		"crlf line endings": {
			input: "a: 1\r\n---\r\nb: 2 # pinned\r\n",
			want:  []string{"a: 1\r\n", "---\r\nb: 2 # pinned\r\n"},
		},
		"no final line break": {
			input: "a: 1\n---\nb: 2",
			want:  []string{"a: 1\n", "---\nb: 2"},
		},
		"comments between documents": {
			input: "a: 1\n# between\n---\nb: 2\n",
			want:  []string{"a: 1\n", "# between\n---\nb: 2\n"},
		},
		"end marker with comments after it": {
			input: "a: 1\n...\n# after\n---\nb: 2\n",
			want:  []string{"a: 1\n...\n", "# after\n---\nb: 2\n"},
		},
		"empty document between two": {
			input: "a: 1\n---\n---\nb: 2\n",
			want:  []string{"a: 1\n", "---\n", "---\nb: 2\n"},
		},
		"byte order mark at the start of each document": {
			input: "\ufeffa: 1\n\ufeff---\nb: 2\n",
			want:  []string{"\ufeffa: 1\n", "\ufeff---\nb: 2\n"},
		},
		"byte order mark alone": {
			input: "\ufeff",
			want:  []string{"\ufeff"},
		},
		"comments alone": {
			input: "# note\n",
			want:  []string{"# note\n"},
		},
		"empty text": {
			input: "",
			want:  []string{""},
		},
		"document that does not parse": {
			input: "a: 1\n---\nb: [\n",
			want:  []string{"a: 1\n", "---\nb: [\n"},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			source := niceyaml.NewSourceFromString(tc.input)
			docs := yamltest.AllDocuments(t, source, len(tc.want))

			var joined strings.Builder

			for i, doc := range docs {
				assert.Equal(t, tc.want[i], doc.Text(), "document %d", i)

				joined.WriteString(doc.Text())
			}

			assert.Equal(t, source.Text(), joined.String())
		})
	}
}

func TestNode_Text(t *testing.T) {
	t.Parallel()

	doc := yamltest.FirstDocument(t, stringtest.JoinCRLF(
		`image: "nginx:1.25"  # pinned`,
		"flow: {a: 1, b: 2}",
		"spec:",
		"  ports:",
		"    - 80",
		"    - 443",
		"block: |",
		"  line1",
		"  line2",
		"y:",
		"",
	))

	tcs := map[string]struct {
		path paths.Path
		want string
	}{
		"quoted scalar keeps its quotes": {
			path: paths.Doc().Child("image"),
			want: `"nginx:1.25"`,
		},
		"key path covers the key": {
			path: paths.Doc().Child("image").Key(),
			want: "image",
		},
		"flow mapping covers its braces": {
			path: paths.Doc().Child("flow"),
			want: "{a: 1, b: 2}",
		},
		"block mapping keeps the indentation of later lines": {
			path: paths.Doc().Child("spec"),
			want: "ports:\r\n    - 80\r\n    - 443",
		},
		"block sequence": {
			path: paths.Doc().Child("spec", "ports"),
			want: "- 80\r\n    - 443",
		},
		"block scalar covers its indicator through its last line": {
			path: paths.Doc().Child("block"),
			want: "|\r\n  line1\r\n  line2",
		},
		"implicit null is empty": {
			path: paths.Doc().Child("y"),
			want: "",
		},
		"root path gives the whole document": {
			path: paths.Doc(),
			want: doc.Source().Text(),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			n := yamltest.At(t, doc, tc.path)
			assert.Equal(t, tc.want, n.Text())

			matches, err := doc.Nodes(tc.path)
			require.NoError(t, err)
			require.Len(t, matches, 1)
			assert.Equal(t, tc.want, matches[0].Text())
		})
	}

	t.Run("node at a position", func(t *testing.T) {
		t.Parallel()

		n, ok := doc.NodeAt(position.New(4, 6))
		require.True(t, ok)
		assert.Equal(t, "80", n.Text())
	})
}

func TestNode_Text_Nil(t *testing.T) {
	t.Parallel()

	var node *niceyaml.Node

	assert.Empty(t, node.Text())
}

func TestNode_Nil(t *testing.T) {
	t.Parallel()

	t.Run("readers return zero values", func(t *testing.T) {
		t.Parallel()

		var n *niceyaml.Node

		assert.Nil(t, n.AST())
		assert.Nil(t, n.DocumentAST())
		assert.Nil(t, n.Source())
		assert.Nil(t, n.FS())
		assert.Empty(t, n.FilePath())
		assert.Nil(t, n.Tokens())
		assert.Nil(t, n.Preamble())
		assert.Equal(t, position.Span{}, n.Span())
		assert.True(t, n.Path().Equal(paths.Path{}))
		assert.Zero(t, n.View().Count())
		require.NoError(t, n.Bind(nil))
		require.NoError(t, n.Bind((*niceyaml.Error)(nil)))
	})

	t.Run("the Node of an error without a location", func(t *testing.T) {
		t.Parallel()

		src := niceyaml.NewSourceFromString("a: 1\n", niceyaml.WithName("f.yaml"))

		bindings := slices.Collect(niceyaml.Bindings(src.Bind(niceyaml.NewError("bad"))))
		require.Len(t, bindings, 1)

		for _, bound := range bindings {
			n, ok := bound.Node()
			require.False(t, ok)

			assert.Equal(t, niceyaml.NodeNone, n.Kind())
			assert.Empty(t, n.FilePath())
			assert.True(t, n.Path().Equal(paths.Path{}))
			assert.Empty(t, n.Source().Name())
			assert.Zero(t, n.View().Count())
		}
	})

	t.Run("methods that need the document panic", func(t *testing.T) {
		t.Parallel()

		var n *niceyaml.Node

		tcs := map[string]struct {
			call func(t *testing.T)
		}{
			"At": {call: func(t *testing.T) {
				t.Helper()

				_, err := n.At(paths.Doc())
				assert.NoError(t, err)
			}},
			"Nodes": {call: func(t *testing.T) {
				t.Helper()

				_, err := n.Nodes(paths.Doc())
				assert.NoError(t, err)
			}},
			"Ranges": {call: func(t *testing.T) {
				t.Helper()

				_, err := n.Ranges(paths.Doc())
				assert.NoError(t, err)
			}},
			"DocumentIndex": {call: func(*testing.T) {
				n.DocumentIndex()
			}},
			"DocumentResolver": {call: func(*testing.T) {
				n.DocumentResolver()
			}},
			"Bind": {call: func(t *testing.T) {
				t.Helper()

				assert.NoError(t, n.Bind(niceyaml.NewError("bad")))
			}},
		}

		for name, tc := range tcs {
			assert.Panics(t, func() { tc.call(t) }, name)
		}
	})
}

func TestNode_Scalar(t *testing.T) {
	t.Parallel()

	refs := niceyaml.NewSourceFromString(stringtest.LinesLF(
		`version: &version "1.10"`,
		"base: &base {x: 1}",
	), niceyaml.WithName("refs.yaml"))

	tcs := map[string]struct {
		input string
		// The path of the Node, $ for the root of the document, or $.a
		// when unset.
		path string
		want string
		ok   bool
	}{
		"float keeps its spelling":       {input: "a: 3.10\n", want: "3.10", ok: true},
		"float with a trailing zero":     {input: "a: 2.0\n", want: "2.0", ok: true},
		"octal integer":                  {input: "a: 0755\n", want: "0755", ok: true},
		"hexadecimal integer":            {input: "a: 0x10\n", want: "0x10", ok: true},
		"bool keeps its case":            {input: "a: True\n", want: "True", ok: true},
		"double-quoted scalar":           {input: `a: "nginx:1.25"` + "\n", want: "nginx:1.25", ok: true},
		"escapes of a quoted scalar":     {input: `a: "x\ty"` + "\n", want: "x\ty", ok: true},
		"single-quoted scalar":           {input: "a: 'it''s'\n", want: "it's", ok: true},
		"empty quoted string":            {input: "a: \"\"\n", want: "", ok: true},
		"tilde":                          {input: "a: ~\n", want: "~", ok: true},
		"written null":                   {input: "a: null\n", want: "null", ok: true},
		"entry without a value":          {input: "a:\n", ok: false},
		"float tag on a quoted scalar":   {input: "a: !!float \"1.10\"\n", want: "1.10", ok: true},
		"int tag on a quoted scalar":     {input: "a: !!int \"0x10\"\n", want: "0x10", ok: true},
		"binary tag":                     {input: "a: !!binary aGk=\n", want: "aGk=", ok: true},
		"timestamp tag":                  {input: "a: !!timestamp 2001-12-14\n", want: "2001-12-14", ok: true},
		"anchored scalar":                {input: "a: &v 1.10\n", want: "1.10", ok: true},
		"literal block scalar":           {input: stringtest.LinesLF("a: |", "  1.10"), want: "1.10\n", ok: true},
		"folded block scalar with strip": {input: stringtest.LinesLF("a: >-", "  x", "  y"), want: "x y", ok: true},
		"mapping":                        {input: "a: {b: 1}\n", ok: false},
		"sequence":                       {input: "a: [1]\n", ok: false},
		"alias of a scalar": {
			input: stringtest.LinesLF("v: &v 1.10", "a: *v"),
			want:  "1.10",
			ok:    true,
		},
		"alias of a mapping": {
			input: stringtest.LinesLF("v: &v {x: 1}", "a: *v"),
			ok:    false,
		},
		"alias of a scalar of a reference document": {
			input: "a: *version\n",
			want:  "1.10",
			ok:    true,
		},
		"alias of a mapping of a reference document": {
			input: "a: *base\n",
			ok:    false,
		},
		"alias inside its own anchor": {
			input: "a: &a [1, *a]\n",
			path:  "$.a[1]",
			ok:    false,
		},
		"string tag on an alias of a sequence": {
			// The tag converts the sequence, so the text is what a decode
			// reads.
			input: stringtest.LinesLF("k: &k [x, y]", "a: !!str *k"),
			want:  "[x y]",
			ok:    true,
		},
		"string tag on an alias of a mapping": {
			input: stringtest.LinesLF("k: &k {p: 1}", "a: !!str *k"),
			want:  "map[p:1]",
			ok:    true,
		},
		"int tag on an alias of a sequence": {
			// A decode reads an int, which Scalar prints.
			input: stringtest.LinesLF("k: &k [x, y]", "a: !!int *k"),
			want:  "0",
			ok:    true,
		},
		"bool tag on an alias of a sequence": {
			// The decode fails.
			input: stringtest.LinesLF("k: &k [x, y]", "a: !!bool *k"),
			ok:    false,
		},
		"null tag on an alias of a mapping": {
			input: stringtest.LinesLF("k: &k {p: 1}", "a: !!null *k"),
			ok:    false,
		},
		"tag without a scalar": {
			input: "a: !!str\n",
			ok:    false,
		},
		"root scalar": {
			input: "1.10\n",
			path:  "$",
			want:  "1.10",
			ok:    true,
		},
		"root mapping": {
			input: "a: 1\n",
			path:  "$",
			ok:    false,
		},
		"empty document": {
			input: "# only a comment\n",
			path:  "$",
			ok:    false,
		},
		"alias with no anchor": {
			input: "*nope\n",
			path:  "$",
			ok:    false,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			doc := yamltest.FirstDocument(t, tc.input, niceyaml.WithReferences(refs))

			// The root of the document is the Node at $, which At does not
			// return for a document without a value.
			n := doc
			if tc.path != "$" {
				n = yamltest.At(t, doc, paths.MustParse(cmp.Or(tc.path, "$.a")))
			}

			got, ok := n.Scalar()
			assert.Equal(t, tc.want, got)
			assert.Equal(t, tc.ok, ok)
		})
	}

	t.Run("document that does not parse", func(t *testing.T) {
		t.Parallel()

		docs := yamltest.AllDocuments(t, niceyaml.NewSourceFromString("a: 1\n---\nb: [\n"), 2)

		got, ok := docs[1].Scalar()
		assert.Empty(t, got)
		assert.False(t, ok)
	})

	t.Run("nil Node", func(t *testing.T) {
		t.Parallel()

		var node *niceyaml.Node

		got, ok := node.Scalar()
		assert.Empty(t, got)
		assert.False(t, ok)
	})
}

func TestNode_KeyRange(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input string
		path  paths.Path
		want  position.Range
		ok    bool
	}{
		"block entry": {
			input: "server:\n  host: a\n",
			path:  paths.Doc().Child("server", "host"),
			want:  yamltest.LineRange(1, 2, 6),
			ok:    true,
		},
		"entry whose value is a mapping": {
			input: "server:\n  host: a\n",
			path:  paths.Doc().Child("server"),
			want:  yamltest.LineRange(0, 0, 6),
			ok:    true,
		},
		"flow entry": {
			input: "flow: {a: 1, b: 2}\n",
			path:  paths.Doc().Child("flow", "b"),
			want:  yamltest.LineRange(0, 13, 14),
			ok:    true,
		},
		"key path reports its own key": {
			input: "flow: {a: 1, b: 2}\n",
			path:  paths.Doc().Child("flow").Key(),
			want:  yamltest.LineRange(0, 0, 4),
			ok:    true,
		},
		"explicit key leaves out the question mark": {
			input: "? k\n: v\n",
			path:  paths.Doc().Child("k"),
			want:  yamltest.LineRange(0, 2, 3),
			ok:    true,
		},
		"quoted key covers its quotes": {
			input: "\"k\": v\n",
			path:  paths.Doc().Child("k"),
			want:  yamltest.LineRange(0, 0, 3),
			ok:    true,
		},
		"tagged key covers its tag": {
			input: "!!str k: v\n",
			path:  paths.Doc().Child("k"),
			want:  yamltest.LineRange(0, 0, 7),
			ok:    true,
		},
		"entry of a mapping element": {
			input: "- name: a\n",
			path:  paths.Doc().Index(0).Child("name"),
			want:  yamltest.LineRange(0, 2, 6),
			ok:    true,
		},
		"entry read through a merge key lies at its anchor": {
			input: "base: &b\n  port: 1\nx:\n  <<: *b\n",
			path:  paths.Doc().Child("x", "port"),
			want:  yamltest.LineRange(1, 2, 6),
			ok:    true,
		},
		"root": {
			input: "a: 1\n",
			path:  paths.Doc(),
		},
		"sequence element": {
			input: "- a\n",
			path:  paths.Doc().Index(0),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			n := yamltest.At(t, yamltest.FirstDocument(t, tc.input), tc.path)

			got, ok := n.KeyRange()
			require.Equal(t, tc.ok, ok)
			assert.Equal(t, tc.want, got)

			if !tc.ok {
				return
			}

			// The key is what an error at the key path highlights.
			ranges, err := n.Ranges(paths.Current().Key())
			require.NoError(t, err)
			require.Len(t, ranges, 1)
			assert.True(t, got.Contains(ranges[0].Start), "range %s holds %s", got, ranges[0])
		})
	}
}

func TestNode_Range_Alias(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input string
		path  paths.Path
		want  position.Range
	}{
		"alias of a mapping covers the alias": {
			input: "a: &x {b: 1}\nc: *x\n",
			path:  paths.Doc().Child("c"),
			want:  yamltest.LineRange(1, 3, 5),
		},
		"alias of a scalar covers the alias": {
			input: "a: &x 5\nc: *x\n",
			path:  paths.Doc().Child("c"),
			want:  yamltest.LineRange(1, 3, 5),
		},
		"alias covers its tag": {
			input: "a: &x 5\nc: !!int *x\n",
			path:  paths.Doc().Child("c"),
			want:  yamltest.LineRange(1, 3, 11),
		},
		"alias leaves out the comment after it": {
			input: "a: &x {b: 1}\nc: *x # note\n",
			path:  paths.Doc().Child("c"),
			want:  yamltest.LineRange(1, 3, 5),
		},
		"alias on the line after its key": {
			input: "a: &x\n  - 1\nc:\n  *x\n",
			path:  paths.Doc().Child("c"),
			want:  yamltest.LineRange(3, 2, 4),
		},
		"alias as an element of a block sequence": {
			input: "- &x {b: 1}\n- *x\n",
			path:  paths.Doc().Index(1),
			want:  yamltest.LineRange(1, 2, 4),
		},
		"alias as an element of a flow sequence": {
			input: "a: &x {b: 1}\nc: [*x]\n",
			path:  paths.Doc().Child("c").Index(0),
			want:  yamltest.LineRange(1, 4, 6),
		},
		"alias after a merge key": {
			input: "a: &x {b: 1}\nc:\n  <<: *x\n",
			path:  paths.Doc().Child("c", "<<"),
			want:  yamltest.LineRange(2, 6, 8),
		},
		"alias in the anchor it refers to": {
			input: "a: &x {b: *x}\n",
			path:  paths.Doc().Child("a", "b"),
			want:  yamltest.LineRange(0, 10, 12),
		},
		"alias key covers the alias": {
			input: "a: &x k\n*x : 1\n",
			path:  paths.Doc().Child("k").Key(),
			want:  yamltest.LineRange(1, 0, 2),
		},
		"explicit alias key covers the alias": {
			input: "a: &x 5\n? *x\n: 1\n",
			path:  paths.Doc().Child("5").Key(),
			want:  yamltest.LineRange(1, 2, 4),
		},
		"path through an alias covers the text at the anchor": {
			input: "a: &x {b: 1}\nc: *x\n",
			path:  paths.Doc().Child("c", "b"),
			want:  yamltest.LineRange(0, 10, 11),
		},
		"entry a merge key brings in covers the text at the anchor": {
			input: "a: &x {b: 1}\nc:\n  <<: *x\n",
			path:  paths.Doc().Child("c", "b"),
			want:  yamltest.LineRange(0, 10, 11),
		},
		"alias in the anchor that a path enters through an alias": {
			input: "s: &s 5\na: &x {b: *s}\nc: *x\n",
			path:  paths.Doc().Child("c", "b"),
			want:  yamltest.LineRange(1, 10, 12),
		},
		"alias in an entry a merge key brings in": {
			input: "s: &s 5\na: &x {b: *s}\nc:\n  <<: *x\n",
			path:  paths.Doc().Child("c", "b"),
			want:  yamltest.LineRange(1, 10, 12),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			n := yamltest.At(t, yamltest.FirstDocument(t, tc.input), tc.path)

			got, ok := n.Range()
			require.True(t, ok)
			assert.Equal(t, tc.want, got)

			// The range holds the start of what an error at the path
			// highlights.
			ranges, err := n.Ranges(paths.Current())
			require.NoError(t, err)
			require.NotEmpty(t, ranges)
			assert.True(t, got.Contains(ranges[0].Start), "range %s holds %s", got, ranges[0])

			// A symbol from the key through the value holds both.
			key, ok := n.KeyRange()
			if !ok {
				return
			}

			symbol := position.NewRange(key.Start, got.End)
			assert.True(t, symbol.Contains(key.Start), "symbol %s holds key %s", symbol, key)
			assert.True(t, symbol.Contains(got.Start), "symbol %s holds value %s", symbol, got)
		})
	}
}

func TestNode_ConcurrentPaths(t *testing.T) {
	t.Parallel()

	// Every Node of a document shares what the first PathAt finds and the
	// resolver that the first path creates, so several goroutines ask
	// Nodes of one document at once.
	doc := yamltest.FirstDocument(t, stringtest.Input(`
		base: &b
		  name: x
		items: [*b, *b]
		spec:
		  replicas: 1
	`))
	spec := yamltest.At(t, doc, paths.Current().Child("spec"))

	var wg sync.WaitGroup

	for range 8 {
		wg.Go(func() {
			got, ok := doc.PathAt(position.New(1, 8))
			if assert.True(t, ok) {
				assert.Equal(t, "$.base.name", got.String())
			}

			got, ok = spec.PathAt(position.New(4, 2))
			if assert.True(t, ok) {
				assert.Equal(t, "$.spec.replicas~", got.String())
			}

			_, ok = doc.PathAt(position.New(9, 0))
			assert.False(t, ok)

			node, err := doc.At(paths.Current().Child("items").Index(1).Child("name"))
			if assert.NoError(t, err) {
				assert.Equal(t, position.NewSpan(1, 2), node.Span())
			}

			items, err := doc.Nodes(paths.Current().Child("items").IndexAll())
			if assert.NoError(t, err) {
				assert.Len(t, items, 2)
			}

			ranges, err := spec.Ranges(paths.Current().Child("replicas"))
			if assert.NoError(t, err) {
				assert.Equal(t, position.Ranges{yamltest.LineRange(4, 12, 13)}, ranges)
			}
		})
	}

	wg.Wait()
}

func TestDocument_At(t *testing.T) {
	t.Parallel()

	input := stringtest.Input(`
		kind: Deployment
		version: 2
		enabled: true
		empty: null
		tags:
		  - a
		  - b
		meta:
		  name: app
	`)

	t.Run("decodes the value the path selects", func(t *testing.T) {
		t.Parallel()

		type meta struct {
			Name string `yaml:"name"`
		}

		tcs := map[string]struct {
			decode func(t *testing.T, n *niceyaml.Node) (any, error)
			want   any
			key    string
		}{
			"string":                     {key: "kind", decode: decodedAs[string], want: "Deployment"},
			"int":                        {key: "version", decode: decodedAs[int], want: 2},
			"bool":                       {key: "enabled", decode: decodedAs[bool], want: true},
			"null decodes to zero value": {key: "empty", decode: decodedAs[string], want: ""},
			"slice":                      {key: "tags", decode: decodedAs[[]string], want: []string{"a", "b"}},
			"struct":                     {key: "meta", decode: decodedAs[meta], want: meta{Name: "app"}},
		}

		dd := yamltest.FirstDocument(t, input)

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				got, err := tc.decode(t, yamltest.At(t, dd, paths.Current().Child(tc.key)))
				require.NoError(t, err)
				assert.Equal(t, tc.want, got)
			})
		}
	})

	t.Run("key decodes as the mapping reads it", func(t *testing.T) {
		t.Parallel()

		tcs := map[string]struct {
			want  any
			input string
		}{
			"plain key": {
				input: "2: x\n",
				want:  uint64(2),
			},
			"quoted key": {
				input: "\"2\": x\n",
				want:  "2",
			},
			"tagged key": {
				input: "!!str 2: x\n",
				want:  "2",
			},
			"explicit tagged key": {
				input: "? !!str 2\n: x\n",
				want:  "2",
			},
			"anchored tagged key": {
				input: "&k !!str 2: x\n",
				want:  "2",
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				dd := yamltest.FirstDocument(t, tc.input)

				whole, err := dd.Decode[map[any]any](t.Context())
				require.NoError(t, err)
				require.Contains(t, whole, tc.want)

				got, err := yamltest.At(t, dd, paths.Current().Child("2").Key()).Decode[any](t.Context())
				require.NoError(t, err)
				assert.Equal(t, tc.want, got)
			})
		}
	})

	t.Run("returns the error of a path that selects no one node", func(t *testing.T) {
		t.Parallel()

		jobs := stringtest.LinesLF("jobs:", "  build: 1")

		tcs := map[string]struct {
			input string
			path  paths.Path
			// The errors the result matches, and one it does not match.
			is    []error
			isNot error
			// Text the message holds.
			contains string
		}{
			"missing key": {
				input:    input,
				path:     paths.Current().Child("nonexistent"),
				is:       []error{paths.ErrNotFound},
				isNot:    paths.ErrAlias,
				contains: "$.nonexistent",
			},
			"empty document": {
				input:    "---\n",
				path:     paths.Current().Child("key"),
				is:       []error{paths.ErrNotFound, paths.ErrNoDocument},
				isNot:    paths.ErrAlias,
				contains: "$.key",
			},
			"file of whitespace": {
				input:    "\n",
				path:     paths.Current().Child("key"),
				is:       []error{paths.ErrNotFound, paths.ErrNoDocument},
				isNot:    paths.ErrAlias,
				contains: "$.key",
			},
			"comments alone": {
				input:    "# only a comment\n",
				path:     paths.Current().Child("key"),
				is:       []error{paths.ErrNotFound, paths.ErrNoDocument},
				isNot:    paths.ErrAlias,
				contains: "$.key",
			},
			// A document that holds only a directive has no content to
			// resolve in.
			"directive alone": {
				input:    "%YAML 1.2\n---\n",
				path:     paths.Current().Child("key"),
				is:       []error{paths.ErrNotFound, paths.ErrNoDocument},
				isNot:    paths.ErrAlias,
				contains: "$.key",
			},
			"alias without anchor": {
				input:    "kind: *nope",
				path:     paths.Current().Child("kind"),
				is:       []error{paths.ErrAlias},
				isNot:    paths.ErrNotFound,
				contains: "*nope",
			},
			"sequence wildcard": {
				input:    "tags: [a, b]",
				path:     paths.Current().Child("tags").IndexAll(),
				is:       []error{paths.ErrWildcard},
				isNot:    paths.ErrNotFound,
				contains: "$.tags[*]",
			},
			"mapping wildcard": {
				input:    jobs,
				path:     paths.Current().Child("jobs").ChildAll(),
				is:       []error{paths.ErrWildcard},
				isNot:    paths.ErrNotFound,
				contains: "$.jobs.*",
			},
			"recursive wildcard": {
				input:    jobs,
				path:     paths.Current().RecursiveAll(),
				is:       []error{paths.ErrWildcard},
				isNot:    paths.ErrNotFound,
				contains: "$..*",
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				dd := yamltest.FirstDocument(t, tc.input)

				got, err := dd.At(tc.path)
				for _, is := range tc.is {
					require.ErrorIs(t, err, is)
				}

				require.NotErrorIs(t, err, tc.isNot)
				assert.Contains(t, err.Error(), tc.contains)
				assert.Nil(t, got)
				assert.Same(t, dd.Source(), yamltest.RequireSourceError(t, err).Source())
			})
		}
	})

	t.Run("resolves in the document below a directive", func(t *testing.T) {
		t.Parallel()

		// A %YAML directive above the header is the preamble of the
		// document below it, so the path resolves in that document.
		docs := yamltest.Documents(t, niceyaml.NewSourceFromString(stringtest.JoinLF(
			"%YAML 1.2",
			"---",
			"key: value",
		)), 1)

		v, err := yamltest.At(t, docs[0], paths.Current().Child("key")).Decode[string](t.Context())
		require.NoError(t, err)
		assert.Equal(t, "value", v)
	})

	t.Run("type mismatch returns Error", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, input)

		got, err := yamltest.At(t, dd, paths.Current().Child("kind")).Decode[int](t.Context())
		require.ErrorAs(t, err, new(*niceyaml.Error))
		assert.Zero(t, got)
	})

	t.Run("alias resolves to the anchor before it when a later anchor reuses the name", func(t *testing.T) {
		t.Parallel()

		// The alias names the anchor defined last before it, so a path
		// through the alias reads v1, and so does a decode of the node
		// that holds the alias.
		tcs := map[string]struct {
			input string
			path  paths.Path
		}{
			"later anchor after the node": {
				input: stringtest.LinesLF(
					"a: &x v1",
					"m:",
					"  k: *x",
					"c: &x v2",
				),
				path: paths.Current().Child("m"),
			},
			"later anchor inside an enclosing mapping": {
				input: stringtest.LinesLF(
					"a: &x v1",
					"m:",
					"  s:",
					"    k: *x",
					"  c: &x v2",
				),
				path: paths.Current().Child("m", "s"),
			},
			"later anchor inside an enclosing anchor": {
				input: stringtest.LinesLF(
					"m: &m",
					"  a: &x v1",
					"  s:",
					"    k: *x",
					"  c: &x v2",
				),
				path: paths.Current().Child("m", "s"),
			},
			"later anchor inside an enclosing merge": {
				input: stringtest.LinesLF(
					"a: &x v1",
					"m:",
					"  <<:",
					"    s:",
					"      k: *x",
					"    c: &x v2",
				),
				path: paths.Current().Child("m", "s"),
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				dd := yamltest.FirstDocument(t, tc.input)

				k, err := yamltest.At(t, dd, tc.path.Child("k")).Decode[string](t.Context())
				require.NoError(t, err)
				require.Equal(t, "v1", k)

				got, err := yamltest.At(t, dd, tc.path).Decode[map[string]any](t.Context())
				require.NoError(t, err)
				assert.Equal(t, map[string]any{"k": "v1"}, got)

				var scoped map[string]any

				err = yamltest.At(t, dd, tc.path).DecodeInto(t.Context(), &scoped)
				require.NoError(t, err)
				assert.Equal(t, map[string]any{"k": "v1"}, scoped)
			})
		}
	})

	t.Run("scoped decode reads the anchors its aliases need", func(t *testing.T) {
		t.Parallel()

		outside := stringtest.Input(`
			base: &b
			  x: 1
			item:
			  ref: *b
			list:
			  - *b
		`)

		tcs := map[string]struct {
			input string
			path  paths.Path
			want  any
		}{
			"alias in a mapping to an anchor outside it": {
				input: outside,
				path:  paths.Current().Child("item"),
				want:  map[string]any{"ref": map[string]any{"x": uint64(1)}},
			},
			"alias in a sequence to an anchor outside it": {
				input: outside,
				path:  paths.Current().Child("list"),
				want:  []any{map[string]any{"x": uint64(1)}},
			},
			"key below a comment under an anchor with no value": {
				input: stringtest.LinesLF(
					"a: &x",
					"# c",
					"b: 1",
				),
				path: paths.Current().Child("b"),
				want: uint64(1),
			},
			// The undefined alias under $.bad fails to decode, and the
			// value the caller asked for still comes back.
			"undefined alias after the anchor": {
				input: stringtest.LinesLF(
					"a: &x 1",
					"sub: {k: *x}",
					"bad: *nope",
				),
				path: paths.Current().Child("sub"),
				want: map[string]any{"k": uint64(1)},
			},
			"undefined alias before the anchor": {
				input: stringtest.LinesLF(
					"bad: *nope",
					"a: &x 1",
					"sub: {k: *x}",
				),
				path: paths.Current().Child("sub"),
				want: map[string]any{"k": uint64(1)},
			},
			"undefined alias inside another anchor": {
				input: stringtest.LinesLF(
					"b: &y [*nope]",
					"a: &x 1",
					"sub: {k: *x}",
				),
				path: paths.Current().Child("sub"),
				want: map[string]any{"k": uint64(1)},
			},
			"merge key in each list entry": {
				input: stringtest.LinesLF(
					"defaults: &d",
					"  a: 1",
					"items:",
					"  - <<: *d",
					"    name: x",
					"  - <<: *d",
					"    name: y",
				),
				path: paths.Current().Child("items").Index(1),
				want: map[string]any{"a": uint64(1), "name": "y"},
			},
			"anchor inside another anchor": {
				input: stringtest.LinesLF(
					"outer: &o",
					"  inner: &i 1",
					"sub: {k: *i}",
				),
				path: paths.Current().Child("sub"),
				want: map[string]any{"k": uint64(1)},
			},
			"anchor inside an anchor that holds the node": {
				input: stringtest.LinesLF(
					"outer: &o",
					"  inner: &i 1",
					"  sub: {k: *i, s: *o}",
				),
				path: paths.Current().Child("outer", "sub"),
				want: map[string]any{"k": uint64(1), "s": nil},
			},
			"anchor that refers to another anchor": {
				input: stringtest.LinesLF(
					"a: &a 1",
					"b: &b [*a]",
					"sub: {k: *b}",
				),
				path: paths.Current().Child("sub"),
				want: map[string]any{"k": []any{uint64(1)}},
			},
			"anchor inside the node": {
				input: stringtest.LinesLF(
					"a: &x 1",
					"sub:",
					"  b: &x 2",
					"  k: *x",
				),
				path: paths.Current().Child("sub"),
				want: map[string]any{"b": uint64(2), "k": uint64(2)},
			},
			"merge key that records an anchor again": {
				// The merge under q records the anchor b of m again, so p
				// reads 1, while X read b before the merge, as 2.
				input: stringtest.Input(`
					m: &m
					  x: &b 1
					b: &b 2
					X: &X
					  z: *b
					q:
					  <<: *m
					node:
					  p: *b
					  r: *X
				`),
				path: paths.Current().Child("node"),
				want: map[string]any{"p": uint64(1), "r": map[string]any{"z": uint64(2)}},
			},
			// The decoder reads an alias inside the value of its own anchor as
			// null, as a decode of the whole document does.
			"alias to the anchor the node is the value of": {
				input: stringtest.LinesLF(
					"a: &a",
					"  - 1",
					"  - *a",
				),
				path: paths.Current().Child("a"),
				want: []any{uint64(1), nil},
			},
			"alias inside the anchor that it refers to": {
				input: stringtest.LinesLF(
					"a: &a",
					"  - 1",
					"  - *a",
				),
				path: paths.Current().Child("a").Index(1),
				want: nil,
			},
			"alias to an anchor that holds the node": {
				input: stringtest.LinesLF(
					"a: &a",
					"  b:",
					"    c: *a",
				),
				path: paths.Current().Child("a", "b"),
				want: map[string]any{"c": nil},
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				dd := yamltest.FirstDocument(t, tc.input)

				got, err := yamltest.At(t, dd, tc.path).Decode[any](t.Context())
				require.NoError(t, err)
				assert.Equal(t, tc.want, got)
			})
		}
	})

	t.Run("alias inside its own anchor writes out as null", func(t *testing.T) {
		t.Parallel()

		// A value that decodes itself from YAML bytes gets the anchor
		// written out, with null for the alias inside it.
		tcs := map[string]struct {
			input string
			want  string
		}{
			"flow mapping": {
				input: stringtest.LinesLF("a: &a {k: *a}", "kind: {x: *a}"),
				want:  "{k: null}",
			},
			"flow sequence": {
				input: stringtest.LinesLF("a: &a [1, *a]", "kind: {x: *a}"),
				want:  "[1, null]",
			},
			"block sequence": {
				input: stringtest.LinesLF(
					"a: &a",
					"  - 1",
					"  - *a",
					"kind: {x: *a}",
				),
				want: stringtest.JoinLF(
					"- 1",
					"- null",
				),
			},
			"anchor name used again later": {
				input: stringtest.LinesLF(
					"a: &a {k: *a}",
					"kind: {x: *a}",
					"b: &a 1",
				),
				want: "{k: null}",
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				dd := yamltest.FirstDocument(t, tc.input)

				got, err := yamltest.At(t, dd, paths.Current().Child("kind")).Decode[map[string]rawText](t.Context())
				require.NoError(t, err)
				assert.Equal(t, tc.want, got["x"].text)
			})
		}
	})

	t.Run("a path that ends at an alias to a reference scopes the alias", func(t *testing.T) {
		t.Parallel()

		doc := yamltest.FirstDocument(t, stringtest.LinesLF(
			"items:",
			"  - *kaffe",
			"count: !!str *num",
		), niceyaml.WithName("order.yaml"), yamltest.References(stringtest.LinesLF(
			"kaffe: &kaffe {item: Kaffe, price: 35}",
			"num: &num 7",
		)))

		tcs := map[string]struct {
			path paths.Path
			// The text of the node that the Node holds.
			ast  string
			kind niceyaml.NodeKind
			span position.Span
			// The value that a decode into any reads.
			want any
		}{
			"alias": {
				path: paths.Current().Child("items").Index(0),
				ast:  "*kaffe",
				kind: niceyaml.NodeMapping,
				span: position.NewSpan(1, 2),
				want: map[string]any{"item": "Kaffe", "price": uint64(35)},
			},
			"tagged alias": {
				// The tag decides how the anchor decodes, so the Node keeps it.
				path: paths.Current().Child("count"),
				ast:  "!!str *num",
				kind: niceyaml.NodeScalar,
				span: position.NewSpan(2, 3),
				want: "7",
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				n := yamltest.At(t, doc, tc.path)
				assert.Equal(t, tc.ast, n.AST().String())
				assert.Equal(t, tc.kind, n.Kind())
				assert.Equal(t, tc.span, n.Span())

				got, err := n.Decode[any](t.Context())
				require.NoError(t, err)
				assert.Equal(t, tc.want, got)
			})
		}

		t.Run("a path through the alias is an error", func(t *testing.T) {
			t.Parallel()

			_, err := doc.At(paths.Current().Child("items").Index(0).Child("price"))
			require.EqualError(
				t,
				err,
				"order.yaml:2:5: $.items[0]: resolve $.items[0].price: alias reads a reference document: *kaffe",
			)
			require.ErrorIs(t, err, paths.ErrAlias)
			require.ErrorIs(t, err, paths.ErrReferenceAlias)
			assert.False(t, niceyaml.IsInvalid(err))
		})
	})

	t.Run("alias to a later anchor stays an error", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, stringtest.Input(`
			item:
			  ref: *b
			base: &b
			  x: 1
		`))

		// The document defines the anchor after the alias, so the value's
		// own decode reports the alias error, bound to the source.
		_, err := yamltest.At(t, dd, paths.Current().Child("item")).Decode[map[string]any](t.Context())
		require.ErrorAs(t, err, new(*niceyaml.SourceError))
		assert.Contains(t, err.Error(), "could not find alias")
	})
}

// scopeHoursInput holds a key open at the root and another in the
// mapping at $.spec.hours, so a path that resolves from the wrong Node
// selects a node on another line.
var scopeHoursInput = stringtest.Input(`
	open: 1
	spec:
	  hours:
	    open: "17:00"
	    close: "09:00"
`)

// requireNodeExtent checks the span and the token values of the Node that
// path selects in doc.
func requireNodeExtent(t *testing.T, doc *niceyaml.Node, path paths.Path, span position.Span, values []string) {
	t.Helper()

	scoped := yamltest.At(t, doc, path)

	assert.Equal(t, span, scoped.Span())
	assert.Equal(t, values, yamltest.TokenValues(scoped.Tokens()))
}

func TestDocument_At_Extent(t *testing.T) {
	t.Parallel()

	hoursPath := paths.Doc().Child("spec", "hours")

	// Each case lists the span and the token values of the Node that path
	// selects.
	tcs := map[string]struct {
		input  string
		path   paths.Path
		span   position.Span
		tokens []string
	}{
		"block scalar covers every line of it": {
			input: stringtest.LinesLF(
				"text: |",
				"  a",
				"  b",
				"next: 1",
			),
			path:   paths.Current().Child("text"),
			span:   position.NewSpan(0, 3),
			tokens: []string{"|", "a\nb\n"},
		},
		// The extent ends on the last line of content.
		"block scalar with trailing blank lines": {
			input: stringtest.LinesLF(
				"a: |",
				"  t",
				"",
				"",
				"b: 1",
			),
			path:   paths.Current().Child("a"),
			span:   position.NewSpan(0, 2),
			tokens: []string{"|", "t\n"},
		},
		"kept block scalar with trailing blank lines": {
			input: stringtest.LinesLF(
				"a: |+",
				"  t",
				"",
				"",
				"b: 1",
			),
			path:   paths.Current().Child("a"),
			span:   position.NewSpan(0, 2),
			tokens: []string{"|+", "t\n\n\n"},
		},
		"multi-line plain scalar": {
			input: stringtest.LinesLF(
				"a: one",
				"  two",
				"  three",
				"b: 1",
			),
			path:   paths.Current().Child("a"),
			span:   position.NewSpan(0, 3),
			tokens: []string{"one two three"},
		},
		"multi-line double-quoted scalar": {
			input:  stringtest.LinesLF(`a: "one`, `  two"`, "b: 1"),
			path:   paths.Current().Child("a"),
			span:   position.NewSpan(0, 2),
			tokens: []string{"one two"},
		},
		"CRLF source": {
			input: stringtest.LinesCRLF(
				"a:",
				"  x: 1",
				"  y: |",
				"    t",
				"b: 1",
			),
			path:   paths.Current().Child("a"),
			span:   position.NewSpan(0, 4),
			tokens: []string{"x", ":", "1", "y", ":", "|", "t\n"},
		},
		"element that ends in a trailing comment": {
			input:  stringtest.LinesLF("- x: 1", "  y: 2 # c", "- b"),
			path:   paths.Current().Index(0),
			span:   position.NewSpan(0, 2),
			tokens: []string{"x", ":", "1", "y", ":", "2", " c"},
		},
		// The token that closes a flow collection belongs to the node, so
		// the extent runs through it.
		"flow sequence over several lines": {
			input: stringtest.LinesLF(
				"a: [",
				"  1,",
				"  2,",
				"]",
				"b: 3",
			),
			path:   paths.Current().Child("a"),
			span:   position.NewSpan(0, 4),
			tokens: []string{"[", "1", ",", "2", ",", "]"},
		},
		"flow mapping at the root": {
			input: stringtest.LinesLF(
				"{",
				`  "a": 1,`,
				`  "b": [`,
				"    2",
				"  ]",
				"}",
			),
			path:   paths.Current(),
			span:   position.NewSpan(0, 6),
			tokens: []string{"{", "a", ":", "1", ",", "b", ":", "[", "2", "]", "}"},
		},
		"flow sequence on one line": {
			input:  "a: [1, 2]\n",
			path:   paths.Current().Child("a"),
			span:   position.NewSpan(0, 1),
			tokens: []string{"[", "1", ",", "2", "]"},
		},
		// A token that holds no text can share its offset with a token of
		// another node. The empty content of a block scalar sits where the
		// next key starts, and an implicit null takes the offset of the ":"
		// or "-" before it. The extent keeps to the tokens of the node.
		"empty block scalar before a sibling": {
			input:  stringtest.LinesLF("a: |", "b: 1"),
			path:   paths.Current().Child("a"),
			span:   position.NewSpan(0, 1),
			tokens: []string{"|", ""},
		},
		"empty folded scalar before an indented sibling": {
			input:  stringtest.LinesLF("x:", "  a: >", "  b: 1"),
			path:   paths.Current().Child("x", "a"),
			span:   position.NewSpan(1, 2),
			tokens: []string{">", ""},
		},
		"mapping that ends in an empty block scalar": {
			input: stringtest.LinesLF(
				"x:",
				"  a: 1",
				"  b: |",
				"y: 1",
			),
			path:   paths.Current().Child("x"),
			span:   position.NewSpan(0, 3),
			tokens: []string{"a", ":", "1", "b", ":", "|", ""},
		},
		"key after an empty block scalar": {
			input:  stringtest.LinesLF("a: |", "b: 1"),
			path:   paths.Current().Child("b").Key(),
			span:   position.NewSpan(1, 2),
			tokens: []string{"b"},
		},
		"implicit null in a mapping": {
			input: stringtest.LinesLF("a:", "b: 1"),
			path:  paths.Current().Child("a"),
			span:  position.NewSpan(0, 1),
		},
		"implicit null in a sequence": {
			input: stringtest.LinesLF("- ", "- 1"),
			path:  paths.Current().Index(0),
			span:  position.NewSpan(0, 1),
		},
		"mapping that ends in an implicit null": {
			input: stringtest.LinesLF(
				"x:",
				"  a: 1",
				"  b:",
				"y: 2",
			),
			path:   paths.Current().Child("x"),
			span:   position.NewSpan(0, 3),
			tokens: []string{"a", ":", "1", "b", ":"},
		},
		"sequence that ends in an implicit null": {
			input: stringtest.LinesLF(
				"x:",
				"  - a",
				"  -",
				"y: 1",
			),
			path:   paths.Current().Child("x"),
			span:   position.NewSpan(0, 3),
			tokens: []string{"-", "a", "-"},
		},
		"block mapping value": {
			// The span starts at the key, which is not among the tokens of
			// the value.
			input:  scopeHoursInput,
			path:   hoursPath,
			span:   position.NewSpan(2, 5),
			tokens: []string{"open", ":", "17:00", "close", ":", "09:00"},
		},
		"scalar on the key line": {
			input:  scopeHoursInput,
			path:   paths.Current().Child("open"),
			span:   position.NewSpan(0, 1),
			tokens: []string{"1"},
		},
		"key selector": {
			input:  scopeHoursInput,
			path:   hoursPath.Key(),
			span:   position.NewSpan(2, 3),
			tokens: []string{"hours"},
		},
		"nested scalar": {
			input:  scopeHoursInput,
			path:   hoursPath.Child("close"),
			span:   position.NewSpan(4, 5),
			tokens: []string{"09:00"},
		},
		"nested scalar before a sibling": {
			// The lexer folds the indentation of the next line into the
			// scalar, which does not put that line into the span.
			input:  scopeHoursInput,
			path:   hoursPath.Child("open"),
			span:   position.NewSpan(3, 4),
			tokens: []string{"17:00"},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			requireNodeExtent(t, yamltest.FirstDocument(t, tc.input), tc.path, tc.span, tc.tokens)
		})
	}
}

// hoursConfig is a value decoded from one node of a document, and its
// Validate names a path from that node.
type hoursConfig struct {
	Open  string `yaml:"open"`
	Close string `yaml:"close"`
}

func (h hoursConfig) Validate() error {
	if h.Open == "" {
		return niceyaml.NewError("open is required", niceyaml.AtPath(paths.Current().Child("open").Key()))
	}

	if h.Open >= h.Close {
		return yamltest.ErrorAt("open must be before close", "open")
	}

	return nil
}

func TestDocument_At_Scope(t *testing.T) {
	t.Parallel()

	hoursPath := paths.Doc().Child("spec", "hours")

	dd := yamltest.FirstDocument(t, scopeHoursInput)
	hours := yamltest.At(t, dd, hoursPath)

	// The value of spec.hours.open sits on line 4 (index 3) at column 11
	// (index 10), and the root key open on line 1.
	openValue := position.New(3, 10)

	t.Run("SelfValidator paths resolve from the node", func(t *testing.T) {
		t.Parallel()

		_, err := hours.Decode[hoursConfig](t.Context())
		bound := yamltest.RequireSourceError(t, err)

		assert.Equal(t, openValue, yamltest.RangeOf(t, bound).Start)
		assert.Equal(t, "4:11: $.spec.hours.open: open must be before close", bound.Error())
	})

	t.Run("Bind resolves a path from the node", func(t *testing.T) {
		t.Parallel()

		bound := yamltest.RequireSourceError(t, hours.Bind(yamltest.ErrorAt("bad", "open")))
		assert.Equal(t, openValue, yamltest.RangeOf(t, bound).Start)
		assert.Same(t, hours, yamltest.BoundNode(t, bound))
		assert.Same(t, dd, yamltest.BoundDocument(t, bound))

		// The whole document resolves the same path at its root.
		bound = yamltest.RequireSourceError(t, dd.Bind(yamltest.ErrorAt("bad", "open")))
		assert.Equal(t, position.New(0, 6), yamltest.RangeOf(t, bound).Start)
	})

	t.Run("Ranges resolves from the node", func(t *testing.T) {
		t.Parallel()

		want, err := dd.Ranges(hoursPath.Child("open"))
		require.NoError(t, err)

		got, err := hours.Ranges(paths.Current().Child("open"))
		require.NoError(t, err)
		assert.Equal(t, want, got)
		assert.Equal(t, openValue, got[0].Start)
	})

	t.Run("a validator receives the scope", func(t *testing.T) {
		t.Parallel()

		var seen map[string]any

		capture := niceyaml.ValidatorFunc(func(ctx context.Context, doc *niceyaml.Node) error {
			assert.Equal(t, hoursPath, doc.Path())

			var err error

			seen, err = doc.Decode[map[string]any](ctx)

			return err
		})

		h, err := hours.Decode[hoursConfig](t.Context(),
			niceyaml.WithValidator(capture),
			niceyaml.WithSelfValidation(false),
		)
		require.NoError(t, err)
		assert.Equal(t, hoursConfig{Open: "17:00", Close: "09:00"}, h)
		assert.Equal(t, map[string]any{"open": "17:00", "close": "09:00"}, seen)
	})

	t.Run("a validator error resolves from the node", func(t *testing.T) {
		t.Parallel()

		bound := yamltest.RequireSourceError(
			t,
			hours.Validate(t.Context(), rejectingValidator(yamltest.ErrorAt("bad", "open"))),
		)
		assert.Equal(t, openValue, yamltest.RangeOf(t, bound).Start)
	})

	t.Run("scopes chain", func(t *testing.T) {
		t.Parallel()

		chained := yamltest.At(t, yamltest.At(t, dd, paths.Current().Child("spec")), paths.Current().Child("hours"))
		assert.Equal(t, hours.Span(), chained.Span())
		assert.Equal(t, hours.Tokens(), chained.Tokens())
		assert.Same(t, hours.AST(), chained.AST())

		open := yamltest.At(t, chained, paths.Current().Child("open"))
		assert.Equal(t, "$.spec.hours.open", open.Path().String())

		got, err := open.Decode[string](t.Context())
		require.NoError(t, err)
		assert.Equal(t, "17:00", got)

		// The receiver keeps its scope.
		assert.True(t, dd.Path().IsRoot())
	})

	t.Run("scope on an alias", func(t *testing.T) {
		t.Parallel()

		// The node of an alias is the alias, and the node of a tagged alias
		// is the tag. Each sits on the line of the alias, and a decode reads
		// the content of the anchor.
		tcs := map[string]struct {
			input string
			want  string
			span  position.Span
		}{
			"alias": {
				input: stringtest.LinesLF(
					"x: &x {k: 1}",
					"mid: 2",
					"c: *x",
				),
				want: "*x",
				span: position.NewSpan(2, 3),
			},
			"tagged alias": {
				input: stringtest.LinesLF(
					"x: &x {k: 1}",
					"mid: 2",
					"c: !t *x",
				),
				want: "!t *x",
				span: position.NewSpan(2, 3),
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				scoped := yamltest.At(t, yamltest.FirstDocument(t, tc.input), paths.Current().Child("c"))

				assert.Equal(t, tc.span, scoped.Span())
				assert.Equal(t, tc.want, scoped.AST().String())

				got, err := scoped.Decode[map[string]int](t.Context())
				require.NoError(t, err)
				assert.Equal(t, map[string]int{"k": 1}, got)
			})
		}
	})

	t.Run("scope on an anchored value covers the comment above it", func(t *testing.T) {
		t.Parallel()

		// Where the parser reads the value below a comment between an
		// anchor and that value, the tree keeps the comment, as it does
		// without the anchor.
		tcs := map[string]struct {
			input string
			path  paths.Path
			span  position.Span
		}{
			"mapping value": {
				input: stringtest.LinesLF("base: &base", "  # c", "  restart: always"),
				path:  paths.Current().Child("base"),
				span:  position.NewSpan(0, 3),
			},
			"sequence value": {
				input: stringtest.LinesLF("base: &base", "  # c", "  - 1"),
				path:  paths.Current().Child("base"),
				span:  position.NewSpan(0, 3),
			},
			"sequence in the column of the key": {
				input: stringtest.LinesLF("base: &base", "# c", "- 1"),
				path:  paths.Current().Child("base"),
				span:  position.NewSpan(0, 3),
			},
			"value of an anchored key": {
				input: stringtest.LinesLF("&k a: &x", "# c", "  b: 1"),
				path:  paths.Current().Child("a"),
				span:  position.NewSpan(0, 3),
			},
			"value of an explicit key": {
				input: stringtest.LinesLF(
					"? a",
					": &x",
					"# c",
					"  b: 2",
				),
				path: paths.Current().Child("a"),
				span: position.NewSpan(0, 4),
			},
			"value in a flow mapping": {
				input: stringtest.LinesLF("{a: &x", "# c", " b}"),
				path:  paths.Current().Child("a"),
				span:  position.NewSpan(1, 3),
			},
			"sequence entry": {
				input: stringtest.LinesLF("- &e", "  # c", "  name: x"),
				path:  paths.Current().Index(0),
				span:  position.NewSpan(0, 3),
			},
			"root": {
				input: stringtest.LinesLF("&x", "# c", "b: 1"),
				path:  paths.Current(),
				span:  position.NewSpan(0, 3),
			},
			"anchor below the key and comment left of the key": {
				input: stringtest.LinesLF(
					"x:",
					"  a:",
					"    &x",
					"# c",
					"    b: 1",
				),
				path: paths.Current().Child("x", "a"),
				span: position.NewSpan(1, 5),
			},
			"sequence below an anchor below the key": {
				input: stringtest.LinesLF(
					"x:",
					"  a:",
					"    &x",
					"# c",
					"  - 1",
				),
				path: paths.Current().Child("x", "a"),
				span: position.NewSpan(1, 5),
			},
			"anchor below the key in a sequence entry": {
				input: stringtest.LinesLF(
					"- a:",
					"    &x",
					"# c",
					"    b: 1",
				),
				path: paths.Current().Index(0).Child("a"),
				span: position.NewSpan(0, 4),
			},
			"anchor below the dash and comment left of the dash": {
				input: stringtest.LinesLF(
					"x:",
					"  -",
					"    &x",
					"# c",
					"    b: 1",
				),
				path: paths.Current().Child("x").Index(0),
				span: position.NewSpan(1, 5),
			},
			"anchor after a tag and comment left of the key": {
				input: stringtest.LinesLF(
					"x:",
					"  a: !t &x",
					"# c",
					"    b: 1",
				),
				path: paths.Current().Child("x", "a"),
				span: position.NewSpan(1, 4),
			},
			"anchor after a tag and comment left of the dash": {
				input: stringtest.LinesLF(
					"x:",
					"  - !t &x",
					"# c",
					"    b: 1",
				),
				path: paths.Current().Child("x").Index(0),
				span: position.NewSpan(1, 4),
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				dd := yamltest.FirstDocument(t, tc.input)
				scoped := yamltest.At(t, dd, tc.path)

				// The span of a mapping or a sequence starts at its key or
				// its "-", above the comment, so the tokens of the value
				// show that the Node holds the comment.
				assert.Equal(t, tc.span, scoped.Span())
				assert.Contains(t, yamltest.TokenValues(scoped.Tokens()), " c")
				assert.Contains(t, dd.DocumentAST().String(), "# c")
			})
		}
	})

	t.Run("a scope that selects nothing leaves the receiver as it is", func(t *testing.T) {
		t.Parallel()

		_, err := dd.At(paths.Current().Child("spec", "missing"))
		require.ErrorIs(t, err, paths.ErrNotFound)

		// The receiver keeps its scope and its extent.
		assert.True(t, dd.Path().IsRoot())
		assert.Equal(t, dd.Span(), yamltest.FirstDocument(t, scopeHoursInput).Span())
	})

	t.Run("excerpt marks the node", func(t *testing.T) {
		t.Parallel()

		_, err := hours.Decode[hoursConfig](t.Context())
		bound := yamltest.RequireSourceError(t, err)

		excerpt, ok := bound.Excerpt(niceyaml.WithContextLines(0))
		require.True(t, ok)
		assert.Equal(t, stringtest.JoinLF(
			`   4 |     open: "17:00"`,
			`     |           ^^^^^^^`,
		), excerpt.String())
	})
}

func TestDocument_Bind(t *testing.T) {
	t.Parallel()

	// The source has no name, so an error bound to its second document
	// names the number of the document.
	twoDocuments := stringtest.LinesLF("name: a", "---", "name: b")

	t.Run("a nil error comes back as a nil error", func(t *testing.T) {
		t.Parallel()

		second := yamltest.Documents(t, niceyaml.NewSourceFromString(twoDocuments), 2)[1]

		for name, err := range map[string]error{
			"nil":                       nil,
			"a nil Error pointer":       (*niceyaml.Error)(nil),
			"a nil SourceError pointer": (*niceyaml.SourceError)(nil),
		} {
			require.NoError(t, second.Bind(err), name)
		}
	})

	t.Run("an error without a location names the source", func(t *testing.T) {
		t.Parallel()

		var nilErr *niceyaml.Error

		// Each case binds err to the last document of input.
		tcs := map[string]struct {
			input string
			opts  []niceyaml.SourceOption
			err   error
			want  string
		}{
			"no name leaves the message as it is": {
				input: "key: value\n",
				err:   errors.New("standard error"),
				want:  "standard error",
			},
			"name goes in front of the message": {
				input: "key: value\n",
				opts:  []niceyaml.SourceOption{niceyaml.WithName("config")},
				err:   errors.New("standard error"),
				want:  "config: standard error",
			},
			"file path names the source": {
				input: "key: value\n",
				opts:  []niceyaml.SourceOption{niceyaml.WithFilePath("dir/config.yaml")},
				err:   errors.New("standard error"),
				want:  "dir/config.yaml: standard error",
			},
			"a later document names its line and number": {
				input: twoDocuments,
				err:   errors.New("plain"),
				want:  "2: document 2: plain",
			},
			"keeps context wrapped around an Error": {
				input: "key: value\n",
				err:   fmt.Errorf("document 3: %w", niceyaml.NewError("test error")),
				want:  "document 3: test error",
			},
			// A nil Error has an empty message, so the text of the wrapper
			// ends at its own prefix.
			"keeps context wrapped around a nil Error": {
				input: "key: value\n",
				opts:  []niceyaml.SourceOption{niceyaml.WithName("config")},
				err:   fmt.Errorf("document 3: %w", nilErr),
				want:  "config: document 3: ",
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				source := niceyaml.NewSourceFromString(tc.input, tc.opts...)
				docs := source.Documents()
				require.NotEmpty(t, docs)

				err := docs[len(docs)-1].Bind(tc.err)
				require.ErrorIs(t, err, tc.err)

				bound := yamltest.RequireSourceError(t, err)
				assert.Same(t, source, bound.Source())
				assert.Equal(t, tc.want, err.Error())
				assert.Equal(t, tc.want, fmt.Sprintf("%+v", err), "no location, so no excerpt")

				_, resolved := bound.Range()
				require.False(t, resolved)
				require.NoError(t, bound.Unresolved())
			})
		}
	})

	t.Run("binds an Error to the source and this document", func(t *testing.T) {
		t.Parallel()

		source := niceyaml.NewSourceFromString(twoDocuments)
		err := yamltest.Documents(t, source, 2)[1].Bind(yamltest.ErrorAt("bad name", "name"))

		bound := yamltest.RequireSourceError(t, err)
		assert.Same(t, source, bound.Source())
		assert.Equal(t, "3:7: $.name: bad name", err.Error())

		// The binding keeps the Error in its chain.
		assert.Equal(t, "bad name", yamltest.RequireError(t, err).Error())
	})

	t.Run("looks past a nil SourceError in the chain", func(t *testing.T) {
		t.Parallel()

		source := niceyaml.NewSourceFromString("name: value\n")
		pathErr := yamltest.ErrorAt("bad name", "name")

		var nilBound *niceyaml.SourceError

		tcs := map[string]error{
			"nil binding after the Error":  errors.Join(pathErr, nilBound),
			"nil binding before the Error": errors.Join(nilBound, pathErr),
			"nil binding wrapped":          fmt.Errorf("ctx: %w", errors.Join(nilBound, pathErr)),
		}

		for name, err := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				// The nil pointer binds nothing, so the join binds to this
				// source with the Error as its only problem, and the path
				// resolves there.
				wrapped := yamltest.Bind(t, source, err)
				require.ErrorIs(t, wrapped, pathErr)

				bound := yamltest.RequireSourceError(t, wrapped)
				assert.Same(t, source, bound.Source())
				assert.Equal(t, yamltest.LineRange(0, 6, 11), yamltest.RangeOf(t, yamltest.OnlyProblem(t, bound)))
			})
		}
	})

	t.Run("returns an error bound to the same source unchanged", func(t *testing.T) {
		t.Parallel()

		source := niceyaml.NewSourceFromString("name: value\n")

		once := yamltest.Bind(t, source, yamltest.ErrorAt("bad name", "name"))
		assert.Same(t, once, yamltest.Bind(t, source, once))

		outer := fmt.Errorf("document 0: %w", once)
		assert.Same(t, outer, yamltest.Bind(t, source, outer))
	})

	t.Run("leaves an error bound to another source as it is", func(t *testing.T) {
		t.Parallel()

		first := niceyaml.NewSourceFromString("name: value\n")
		second := niceyaml.NewSourceFromString(
			stringtest.LinesLF("# comment", "name: value"),
			niceyaml.WithName("second"),
		)

		once := yamltest.Bind(t, first, yamltest.ErrorAt("bad name", "name"))
		twice := yamltest.Bind(t, second, once)

		// The error is bound already, so the second binding neither moves
		// it nor adds its name.
		require.Same(t, once, twice)

		bound := yamltest.RequireSourceError(t, twice)
		assert.Same(t, first, bound.Source())
		assert.Equal(t, "1:7: $.name: bad name", twice.Error())
	})
}

func TestDocument_Err(t *testing.T) {
	t.Parallel()

	// The second document does not parse, and the documents around it do.
	// The first one reuses an anchor name and the third holds a !!int tag,
	// so each decodes from the second parse of the source.
	input := stringtest.LinesLF(
		"a: &x 1",
		"b: &x 2",
		"c: *x",
		"---",
		"d: [",
		"---",
		"e: !!int 0x10",
	)

	documents := func(t *testing.T) []*niceyaml.Node {
		t.Helper()

		docs := yamltest.AllDocuments(t, niceyaml.NewSourceFromString(input, niceyaml.WithName("f.yaml")), 3)
		require.EqualError(t, docs[1].Err(), "f.yaml:5:4: sequence end token ']' not found")

		return docs
	}

	t.Run("a document that parsed has none", func(t *testing.T) {
		t.Parallel()

		docs := documents(t)

		require.NoError(t, docs[0].Err())
		require.NoError(t, docs[2].Err())

		first, err := docs[0].Decode[map[string]int](t.Context())
		require.NoError(t, err)
		assert.Equal(t, map[string]int{"a": 1, "b": 2, "c": 2}, first)

		third, err := docs[2].Decode[map[string]int](t.Context())
		require.NoError(t, err)
		assert.Equal(t, map[string]int{"e": 16}, third)

		require.NoError(t, docs[0].Validate(t.Context(), passingValidator()))

		scoped := yamltest.At(t, docs[2], paths.Current().Child("e"))
		assert.Equal(t, 2, scoped.DocumentIndex())
		require.NoError(t, scoped.Err())
	})

	t.Run("the methods that read the tree return it", func(t *testing.T) {
		t.Parallel()

		// A validator that ran would replace the syntax error with its own.
		ran := &fieldValidator{err: errDocumentRejected}

		tcs := map[string]struct {
			// Calls the method and returns its error.
			call func(t *testing.T, doc *niceyaml.Node) error
		}{
			"Decode": {call: func(t *testing.T, doc *niceyaml.Node) error {
				t.Helper()

				got, err := doc.Decode[map[string]any](t.Context())
				assert.Nil(t, got)

				return err
			}},
			"Decode with a validator": {call: func(t *testing.T, doc *niceyaml.Node) error {
				t.Helper()

				_, err := doc.Decode[map[string]any](t.Context(), niceyaml.WithValidator(ran))

				return err
			}},
			"DecodeInto": {call: func(t *testing.T, doc *niceyaml.Node) error {
				t.Helper()

				got := map[string]int{"kept": 1}
				err := doc.DecodeInto(t.Context(), &got)
				assert.Equal(t, map[string]int{"kept": 1}, got)

				return err //nolint:wrapcheck // The test inspects the error of the call.
			}},
			"Validate with NoValidator()": {call: func(t *testing.T, doc *niceyaml.Node) error {
				t.Helper()

				return doc.Validate(t.Context(), niceyaml.NoValidator())
			}},
			"Validate": {call: func(t *testing.T, doc *niceyaml.Node) error {
				t.Helper()

				return doc.Validate(t.Context(), ran)
			}},
			"At": {call: func(t *testing.T, doc *niceyaml.Node) error {
				t.Helper()

				scoped, err := doc.At(paths.Current().Child("d"))
				assert.Nil(t, scoped)

				return err //nolint:wrapcheck // The test inspects the error of the call.
			}},
			"At the root": {call: func(t *testing.T, doc *niceyaml.Node) error {
				t.Helper()

				_, err := doc.At(paths.Current())

				return err //nolint:wrapcheck // The test inspects the error of the call.
			}},
			"Nodes": {call: func(t *testing.T, doc *niceyaml.Node) error {
				t.Helper()

				nodes, err := doc.Nodes(paths.Current().Child("d").IndexAll())
				assert.Nil(t, nodes)

				return err //nolint:wrapcheck // The test inspects the error of the call.
			}},
			"Ranges": {call: func(t *testing.T, doc *niceyaml.Node) error {
				t.Helper()

				ranges, err := doc.Ranges(paths.Current().Child("d"))
				assert.Nil(t, ranges)

				return err //nolint:wrapcheck // The test inspects the error of the call.
			}},
			"ValidatorFunc called directly": {call: func(t *testing.T, doc *niceyaml.Node) error {
				t.Helper()

				return niceyaml.ValidatorFunc(ran.Check).Check(t.Context(), doc)
			}},
			"MultiValidator called directly": {call: func(t *testing.T, doc *niceyaml.Node) error {
				t.Helper()

				return niceyaml.MultiValidator(ran, ran).Check(t.Context(), doc)
			}},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				doc := documents(t)[1]

				err := tc.call(t, doc)
				require.Error(t, err)
				assert.Same(t, doc.Err(), err)
			})
		}
	})

	t.Run("the document has no tree", func(t *testing.T) {
		t.Parallel()

		doc := documents(t)[1]

		assert.Nil(t, doc.AST())
		assert.Nil(t, doc.DocumentAST())
		assert.Same(t, doc, doc.Document())
		assert.True(t, doc.Path().IsRoot())
	})

	t.Run("the methods that read the tokens work", func(t *testing.T) {
		t.Parallel()

		doc := documents(t)[1]

		assert.Equal(t, 1, doc.DocumentIndex())
		assert.Equal(t, position.NewSpan(3, 5), doc.Span())
		assert.Equal(t, stringtest.JoinLF(
			"---",
			"d: [",
		), doc.View().Held().Content())
		assert.Equal(t, 4, doc.View().Held().Line(0).Number())

		assert.Equal(t, []string{"---", "d", ":", "["}, yamltest.TokenValues(doc.Tokens()))
		require.Len(t, doc.Preamble(), 1)
		assert.Equal(t, token.DocumentHeaderType, doc.Preamble()[0].Type)
	})

	t.Run("diffs against a revision that parses", func(t *testing.T) {
		t.Parallel()

		before := documents(t)

		after := yamltest.Documents(t, niceyaml.NewSourceFromString(strings.Replace(input, "d: [", "d: []", 1)), 3)

		result := yamltest.DiffLines(t, before[1].View(), after[1].View())

		assert.Equal(t, diff.Stats{Added: 1, Removed: 1}, result.Stats())

		unified := result.Unified().String()
		assert.Contains(t, unified, "d: []")
		assert.NotContains(t, unified, "a: &x 1", "the first document is not in the diff")
	})

	t.Run("the error is bound to the document", func(t *testing.T) {
		t.Parallel()

		doc := documents(t)[1]

		bound, ok := doc.Err().(*niceyaml.SourceError) //nolint:errorlint // The value itself is the bound error.
		require.True(t, ok, "want *niceyaml.SourceError, got %T", doc.Err())
		assert.Same(t, doc.Source(), bound.Source())
		assert.Same(t, doc, yamltest.BoundNode(t, bound))
		assert.Same(t, doc, yamltest.BoundDocument(t, bound))

		assert.True(t, doc.Span().Contains(yamltest.RangeOf(t, bound).Start.Line))

		// File returns the same binding, so it names the document too.
		_, fileErr := doc.Source().File()
		assert.Same(t, doc.Err(), fileErr)
	})

	t.Run("a path bound through the document resolves nowhere", func(t *testing.T) {
		t.Parallel()

		doc := documents(t)[1]

		err := doc.Bind(yamltest.ErrorAt("bad", "d"))

		bound := yamltest.RequireSourceError(t, err)
		assert.Same(t, doc, yamltest.BoundDocument(t, bound))
		assert.Equal(t, "f.yaml:4: document 2: $.d: bad", err.Error())

		_, ok := bound.Range()
		assert.False(t, ok)

		require.ErrorIs(t, bound.Unresolved(), niceyaml.ErrPathNeedsDocument)
		require.ErrorIs(t, bound.Unresolved(), doc.Err())
	})

	t.Run("a position bound through the document resolves", func(t *testing.T) {
		t.Parallel()

		doc := documents(t)[1]

		err := doc.Bind(niceyaml.NewError("bad", niceyaml.AtPosition(position.New(4, 0))))

		bound := yamltest.RequireSourceError(t, err)
		assert.Same(t, doc, yamltest.BoundDocument(t, bound))
		assert.Equal(t, "f.yaml:5:1: bad", err.Error())
		require.NoError(t, bound.Unresolved())
	})
}

func TestDocument_Node(t *testing.T) {
	t.Parallel()

	dd := yamltest.FirstDocument(t, stringtest.Input(`
		kind: Deployment
		meta:
		  name: app
	`))

	t.Run("root scope is the body", func(t *testing.T) {
		t.Parallel()

		assert.Same(t, dd.DocumentAST().Body, dd.AST())
	})

	t.Run("scope is the node the path selects", func(t *testing.T) {
		t.Parallel()

		node := yamltest.At(t, dd, paths.Current().Child("meta")).AST()
		assert.Equal(t, "  name: app", node.String())

		want, err := paths.NewResolver(dd.DocumentAST()).Node(paths.Doc().Child("meta"))
		require.NoError(t, err)
		assert.Same(t, want, node)
	})

	t.Run("file of whitespace holds a placeholder that decodes to nothing", func(t *testing.T) {
		t.Parallel()

		// The lexer emits nothing for the text, so the document holds no
		// value, and the decode leaves its target as it is rather than
		// reading the placeholder token as a string.
		for _, input := range []string{"\n", "  \n", "\t\n", "\n\n", "!"} {
			blank := yamltest.FirstDocument(t, input)

			scalar, ok := blank.AST().(*ast.StringNode)
			require.True(t, ok, "%q: %T", input, blank.AST())
			assert.True(t, tokens.IsPlaceholder(scalar.Token), "%q", input)

			got := map[string]int{"kept": 1}
			require.NoError(t, blank.DecodeInto(t.Context(), &got), "%q", input)
			assert.Equal(t, map[string]int{"kept": 1}, got, "%q", input)

			v, err := blank.Decode[any](t.Context())
			require.NoError(t, err, "%q", input)
			assert.Nil(t, v, "%q", input)
		}
	})

	t.Run("empty document has no body", func(t *testing.T) {
		t.Parallel()

		assert.Nil(t, yamltest.FirstDocument(t, "---\n").AST())
	})

	t.Run("document of comments has its comment group", func(t *testing.T) {
		t.Parallel()

		assert.IsType(t, &ast.CommentGroupNode{}, yamltest.FirstDocument(t, "# only a comment\n").AST())
	})

	t.Run("document of whitespace below a header has no body", func(t *testing.T) {
		t.Parallel()

		assert.Nil(t, yamltest.FirstDocument(t, stringtest.LinesLF("---", "  ")).AST())

		docs := yamltest.AllDocuments(t, niceyaml.NewSourceFromString(stringtest.LinesLF(
			"a: 1",
			"---",
			"  ",
		)), 2)
		assert.Nil(t, docs[1].AST())
	})

	t.Run("a scoped node reaches the whole document", func(t *testing.T) {
		t.Parallel()

		meta := yamltest.At(t, dd, paths.Current().Child("meta"))

		assert.Same(t, dd, meta.Document())
		assert.Same(t, dd.DocumentAST(), meta.Document().DocumentAST())
		assert.Same(t, dd.Source(), meta.Source())
		assert.Equal(t, dd.DocumentIndex(), meta.Document().DocumentIndex())
		assert.Equal(t, dd.Preamble(), meta.Document().Preamble())
		assert.Equal(t, dd.FilePath(), meta.Document().FilePath())
		assert.Equal(t, paths.Doc().Child("meta"), meta.Path())
		assert.True(t, dd.Path().IsRoot())
		assert.Equal(t, paths.Doc(), yamltest.At(t, dd, paths.Doc()).Path())
		assert.Same(t, dd, dd.Document())

		var nothing *niceyaml.Node

		assert.Nil(t, nothing.Document())
	})
}

func TestDocument_PathAnchors(t *testing.T) {
	t.Parallel()

	// The hours mapping holds a key `open`, and so does the root, so a
	// path that resolves from the wrong node selects a node on another
	// line rather than none.
	doc := yamltest.FirstDocument(t, stringtest.Input(`
		open: 1
		spec:
		  hours:
		    open: 9
		name: x
	`))

	hoursPath := paths.Doc().Child("spec", "hours")

	tcs := map[string]struct {
		scope paths.Path
		path  paths.Path
		want  string
		err   error
	}{
		"relative path from a scoped Node": {
			scope: hoursPath,
			path:  paths.Current().Child("open"),
			want:  "$.spec.hours.open",
		},
		"absolute path from a scoped Node": {
			scope: hoursPath,
			path:  paths.Doc().Child("open"),
			want:  "$.open",
		},
		"absolute path outside the scope": {
			scope: hoursPath,
			path:  paths.Doc().Child("name"),
			want:  "$.name",
		},
		"path of the scoped Node itself": {
			scope: hoursPath,
			path:  hoursPath,
			want:  "$.spec.hours",
		},
		"current node": {
			scope: hoursPath,
			path:  paths.Current(),
			want:  "$.spec.hours",
		},
		"document root from a scoped Node": {
			scope: hoursPath,
			path:  paths.Doc(),
			want:  "$",
		},
		"absolute path names no key of the root": {
			scope: hoursPath,
			path:  paths.Doc().Child("hours"),
			err:   paths.ErrNotFound,
		},
		"relative path from the root": {
			scope: paths.Doc(),
			path:  paths.Current().Child("spec", "hours", "open"),
			want:  "$.spec.hours.open",
		},
		"absolute path from the root": {
			scope: paths.Doc(),
			path:  paths.Doc().Child("spec", "hours", "open"),
			want:  "$.spec.hours.open",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			node := yamltest.At(t, doc, tc.scope)

			got, err := node.At(tc.path)
			if tc.err != nil {
				require.ErrorIs(t, err, tc.err)

				_, err = node.Ranges(tc.path)
				require.ErrorIs(t, err, tc.err)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, tc.want, got.Path().String())

			want := yamltest.At(t, doc, paths.MustParse(tc.want))
			assert.Equal(t, want.Span(), got.Span())

			nodes, err := node.Nodes(tc.path)
			require.NoError(t, err)
			require.Len(t, nodes, 1)
			assert.Equal(t, tc.want, nodes[0].Path().String())

			ranges, err := node.Ranges(tc.path)
			require.NoError(t, err)

			wantRanges, err := doc.Ranges(paths.MustParse(tc.want))
			require.NoError(t, err)
			assert.Equal(t, wantRanges, ranges)

			// An Error at the path binds where the path resolves, and the
			// binding reports the path from the root of the document.
			bound := yamltest.RequireSourceError(t, node.Bind(niceyaml.NewError("bad", niceyaml.AtPath(tc.path))))
			require.NoError(t, bound.Unresolved())

			assert.Equal(t, tc.want, yamltest.PathOf(t, bound).String())

			require.NotEmpty(t, wantRanges)
			assert.Equal(t, wantRanges[0].Start, yamltest.RangeOf(t, bound).Start)
		})
	}
}

func TestDocument_Preamble(t *testing.T) {
	t.Parallel()

	// The parser cuts the comments and directives above a header into a
	// node of their own, and Documents folds it into the document below.
	// Each case lists the preamble and the content of every document, as
	// the origin text of their tokens, and some list the span of each
	// document.
	type doc struct {
		preamble string
		content  string
	}

	tcs := map[string]struct {
		input string
		want  []doc
		spans []position.Span
	}{
		"mapping": {
			input: "a: 1\n",
			want:  []doc{{content: "a: 1\n"}},
		},
		"mapping over two lines": {
			input: stringtest.LinesLF("a: 1", "b: 2"),
			want:  []doc{{content: stringtest.LinesLF("a: 1", "b: 2")}},
			spans: []position.Span{position.NewSpan(0, 2)},
		},
		"leading comment": {
			input: stringtest.LinesLF("# note", "a: 1"),
			want:  []doc{{preamble: "# note\n", content: "a: 1\n"}},
			spans: []position.Span{position.NewSpan(0, 2)},
		},
		"leading blank lines": {
			input: stringtest.LinesLF("", "", "a: 1"),
			want:  []doc{{content: "a: 1\n"}},
			spans: []position.Span{position.NewSpan(0, 3)},
		},
		"leading blank lines before a header": {
			input: stringtest.LinesLF(
				"",
				"---",
				"a: 1",
				"---",
				"b: 2",
			),
			want: []doc{
				{preamble: "---\n", content: "a: 1\n"},
				{preamble: "---\n", content: "b: 2\n"},
			},
			spans: []position.Span{
				position.NewSpan(0, 3),
				position.NewSpan(3, 5),
			},
		},
		"comment above the header": {
			input: stringtest.LinesLF("# license", "---", "a: 1"),
			want:  []doc{{preamble: stringtest.LinesLF("# license", "---"), content: "a: 1\n"}},
			spans: []position.Span{position.NewSpan(0, 3)},
		},
		// The comment and the blank line below it open the tokens of the
		// document.
		"comment and a blank line above the header": {
			input: stringtest.JoinLF("# top", "", "---", "b: 2"),
			want:  []doc{{preamble: stringtest.LinesLF("# top", "", "---"), content: "b: 2"}},
		},
		"comment below the header": {
			input: stringtest.LinesLF("--- # note", "a: 1"),
			want:  []doc{{preamble: "--- # note\n", content: "a: 1\n"}},
		},
		"directive above the header": {
			input: stringtest.LinesLF("%YAML 1.2", "---", "a: 1"),
			want:  []doc{{preamble: stringtest.LinesLF("%YAML 1.2", "---"), content: "a: 1\n"}},
		},
		"explicit empty": {
			input: "---\n",
			want:  []doc{{preamble: "---\n"}},
		},
		"explicit empty with a comment": {
			input: stringtest.LinesLF("---", "# note", "---", "b: 2"),
			want: []doc{
				{preamble: stringtest.LinesLF("---", "# note")},
				{preamble: "---\n", content: "b: 2\n"},
			},
		},
		"empty file": {
			input: "",
			want:  []doc{{}},
			spans: []position.Span{position.NewSpan(0, 0)},
		},
		"comment only": {
			input: "# note\n",
			want:  []doc{{preamble: "# note\n"}},
		},
		"trailing comment after an end marker": {
			input: stringtest.LinesLF("a: 1", "...", "# trailing"),
			want:  []doc{{content: stringtest.LinesLF("a: 1", "...", "# trailing")}},
			spans: []position.Span{position.NewSpan(0, 3)},
		},
		"leading end marker": {
			input: stringtest.LinesLF("...", "a: 1"),
			want:  []doc{{preamble: "...\n", content: "a: 1\n"}},
		},
		"leading end marker above a header": {
			input: stringtest.LinesLF("...", "---", "a: 1"),
			want:  []doc{{preamble: stringtest.LinesLF("...", "---"), content: "a: 1\n"}},
		},
		"leading end marker above a later header": {
			input: stringtest.LinesLF("...", "a: 1", "---", "b: 2"),
			want: []doc{
				{preamble: "...\n", content: "a: 1\n"},
				{preamble: "---\n", content: "b: 2\n"},
			},
			spans: []position.Span{
				position.NewSpan(0, 2),
				position.NewSpan(2, 4),
			},
		},
		"comment on the line of an end marker": {
			input: stringtest.LinesLF("a: 1", "... # e", "k: v"),
			want: []doc{
				{content: stringtest.LinesLF("a: 1", "... # e")},
				{content: "k: v\n"},
			},
			spans: []position.Span{
				position.NewSpan(0, 2),
				position.NewSpan(2, 3),
			},
		},
		"comment on the line of an end marker above a header": {
			input: stringtest.LinesLF("a: 1", "... # e", "---", "k: v"),
			want: []doc{
				{content: stringtest.LinesLF("a: 1", "... # e")},
				{preamble: "---\n", content: "k: v\n"},
			},
		},
		"comment between documents": {
			input: stringtest.LinesLF(
				"a: 1",
				"...",
				"# note",
				"---",
				"b: 2",
			),
			want: []doc{
				{content: stringtest.LinesLF("a: 1", "...")},
				{preamble: stringtest.LinesLF("# note", "---"), content: "b: 2\n"},
			},
			spans: []position.Span{
				position.NewSpan(0, 2),
				position.NewSpan(2, 5),
			},
		},
		"directive below an empty document": {
			input: stringtest.LinesLF(
				"---",
				"...",
				"# note",
				"%YAML 1.2",
				"---",
				"a: 1",
			),
			want: []doc{
				{preamble: stringtest.LinesLF("---", "...")},
				{preamble: stringtest.LinesLF("# note", "%YAML 1.2", "---"), content: "a: 1\n"},
			},
			spans: []position.Span{
				position.NewSpan(0, 2),
				position.NewSpan(2, 6),
			},
		},
		"version directive on each side of an empty document": {
			input: stringtest.LinesLF(
				"%YAML 1.2",
				"---",
				"...",
				"%YAML 1.2",
				"---",
				"a: 1",
			),
			want: []doc{
				{preamble: stringtest.LinesLF("%YAML 1.2", "---", "...")},
				{preamble: stringtest.LinesLF("%YAML 1.2", "---"), content: "a: 1\n"},
			},
		},
		"tag directive below an empty document": {
			input: stringtest.LinesLF(
				"%YAML 1.2",
				"---",
				"...",
				"%TAG ! tag:x,2000:",
				"---",
				"!a b",
			),
			want: []doc{
				{preamble: stringtest.LinesLF("%YAML 1.2", "---", "...")},
				{preamble: stringtest.LinesLF("%TAG ! tag:x,2000:", "---"), content: "!a b\n"},
			},
		},
		"comments on the markers of an empty document": {
			input: stringtest.LinesLF(
				"--- # c",
				"... # d",
				"# e",
				"%YAML 1.2",
				"--- # f",
				"a: 1",
			),
			want: []doc{
				{preamble: stringtest.LinesLF("--- # c", "... # d")},
				{preamble: stringtest.LinesLF("# e", "%YAML 1.2", "--- # f"), content: "a: 1\n"},
			},
		},
		"document without a header below an empty document": {
			input: stringtest.LinesLF("---", "...", "# note", "b: 1"),
			want: []doc{
				{preamble: stringtest.LinesLF("---", "...")},
				{preamble: "# note\n", content: "b: 1\n"},
			},
		},
		"scalar document below an end marker": {
			input: stringtest.LinesLF("a: 1", "...", "b"),
			want: []doc{
				{content: stringtest.LinesLF("a: 1", "...")},
				{content: "b\n"},
			},
		},
		"end marker": {
			input: stringtest.LinesLF("a: 1", "...", "b: 2"),
			want: []doc{
				{content: stringtest.LinesLF("a: 1", "...")},
				{content: "b: 2\n"},
			},
			spans: []position.Span{
				position.NewSpan(0, 2),
				position.NewSpan(2, 3),
			},
		},
		"headers": {
			input: stringtest.LinesLF("a: 1", "---", "b: 2"),
			want: []doc{
				{content: "a: 1\n"},
				{preamble: "---\n", content: "b: 2\n"},
			},
		},
		"headers around a document of two lines": {
			input: stringtest.LinesLF(
				"a: 1",
				"---",
				"b: 2",
				"c: 3",
				"---",
				"d: 4",
			),
			want: []doc{
				{content: "a: 1\n"},
				{preamble: "---\n", content: stringtest.LinesLF("b: 2", "c: 3")},
				{preamble: "---\n", content: "d: 4\n"},
			},
			spans: []position.Span{
				position.NewSpan(0, 1),
				position.NewSpan(1, 4),
				position.NewSpan(4, 6),
			},
		},
		"block scalar at the end of a document": {
			input: stringtest.LinesLF(
				"a: |",
				"  one",
				"  two",
				"---",
				"b: 2",
			),
			want: []doc{
				{content: stringtest.LinesLF("a: |", "  one", "  two")},
				{preamble: "---\n", content: "b: 2\n"},
			},
			spans: []position.Span{
				position.NewSpan(0, 3),
				position.NewSpan(3, 5),
			},
		},
		"comment above a later header": {
			input: stringtest.LinesLF("a: 1", "# note", "---", "b: 2"),
			want: []doc{
				{content: "a: 1\n"},
				{preamble: stringtest.LinesLF("# note", "---"), content: "b: 2\n"},
			},
			spans: []position.Span{
				position.NewSpan(0, 1),
				position.NewSpan(1, 4),
			},
		},
		"comments and a blank line above a later header": {
			input: stringtest.LinesLF(
				"a: 1",
				"# one",
				"",
				"# two",
				"---",
				"b: 2",
			),
			want: []doc{
				{content: "a: 1\n"},
				{preamble: stringtest.LinesLF("# one", "", "# two", "---"), content: "b: 2\n"},
			},
		},
		"comment above a later header with crlf": {
			input: stringtest.LinesCRLF("a: 1", "# note", "---", "b: 2"),
			want: []doc{
				{content: "a: 1\r\n"},
				{preamble: stringtest.LinesCRLF("# note", "---"), content: "b: 2\r\n"},
			},
		},
		"comment after a block scalar above a later header": {
			input: stringtest.LinesLF(
				"a: |",
				"  x",
				"  y",
				"# note",
				"---",
				"b: 2",
			),
			want: []doc{
				{content: stringtest.LinesLF("a: |", "  x", "  y")},
				{preamble: stringtest.LinesLF("# note", "---"), content: "b: 2\n"},
			},
		},
		"comment after a block scalar that opens with a blank line": {
			input: stringtest.LinesLF(
				"a: |",
				"",
				"  x",
				"",
				"# note",
				"---",
				"b: 2",
			),
			want: []doc{
				{content: stringtest.LinesLF(
					"a: |",
					"",
					"  x",
					"",
				)},
				{preamble: stringtest.LinesLF("# note", "---"), content: "b: 2\n"},
			},
		},
		"comment after an empty block scalar": {
			input: stringtest.LinesLF("a: |", "# note", "---", "b: 2"),
			want: []doc{
				{content: "a: |\n"},
				{preamble: stringtest.LinesLF("# note", "---"), content: "b: 2\n"},
			},
		},
		"comment after a value on the next line": {
			input: stringtest.LinesLF(
				"a:",
				"  b",
				"# note",
				"---",
				"c: 1",
			),
			want: []doc{
				{content: stringtest.LinesLF("a:", "  b")},
				{preamble: stringtest.LinesLF("# note", "---"), content: "c: 1\n"},
			},
		},
		"comment after a quoted value on the next line": {
			input: stringtest.LinesLF(
				"a:",
				"  'q'",
				"# note",
				"---",
				"c: 1",
			),
			want: []doc{
				{content: stringtest.LinesLF("a:", "  'q'")},
				{preamble: stringtest.LinesLF("# note", "---"), content: "c: 1\n"},
			},
		},
		"comment after a sequence entry on the next line": {
			input: stringtest.LinesLF(
				"-",
				"  y",
				"# note",
				"---",
				"c: 1",
			),
			want: []doc{
				{content: stringtest.LinesLF("-", "  y")},
				{preamble: stringtest.LinesLF("# note", "---"), content: "c: 1\n"},
			},
		},
		"comment after a value below blank lines": {
			input: stringtest.LinesLF(
				"a:",
				"",
				"",
				"  x",
				"# note",
				"---",
				"c: 1",
			),
			want: []doc{
				{content: stringtest.LinesLF(
					"a:",
					"",
					"",
					"  x",
				)},
				{preamble: stringtest.LinesLF("# note", "---"), content: "c: 1\n"},
			},
		},
		"comment after a value on the next line with crlf": {
			input: stringtest.LinesCRLF(
				"a:",
				"  b",
				"# note",
				"---",
				"b: 2",
			),
			want: []doc{
				{content: stringtest.LinesCRLF("a:", "  b")},
				{preamble: stringtest.LinesCRLF("# note", "---"), content: "b: 2\r\n"},
			},
		},
		"indented comment after a value": {
			input: stringtest.LinesLF(
				"a: 1",
				"  # note",
				"---",
				"b: 2",
			),
			want: []doc{
				{content: "a: 1\n"},
				{preamble: stringtest.LinesLF("# note", "---"), content: "b: 2\n"},
			},
		},
		"indented comment after a nested value": {
			input: stringtest.LinesLF(
				"a:",
				"  b: 1",
				"  # note",
				"---",
				"c: 1",
			),
			want: []doc{
				{content: stringtest.LinesLF("a:", "  b: 1")},
				{preamble: stringtest.LinesLF("# note", "---"), content: "c: 1\n"},
			},
		},
		"indented comment after a nested block scalar": {
			input: stringtest.LinesLF(
				"a:",
				"  b: |",
				"    x",
				"  # note",
				"---",
				"c: 1",
			),
			want: []doc{
				{content: stringtest.LinesLF("a:", "  b: |", "    x")},
				{preamble: stringtest.LinesLF("# note", "---"), content: "c: 1\n"},
			},
		},
		"indented comment after a nested value with crlf": {
			input: stringtest.LinesCRLF(
				"a:",
				"  b: 1",
				"  # note",
				"---",
				"c: 1",
			),
			want: []doc{
				{content: stringtest.LinesCRLF("a:", "  b: 1")},
				{preamble: stringtest.LinesCRLF("# note", "---"), content: "c: 1\r\n"},
			},
		},
		"comment on the line of a value on the next line": {
			input: stringtest.LinesLF(
				"a:",
				"  b # same",
				"---",
				"c: 1",
			),
			want: []doc{
				{content: stringtest.LinesLF("a:", "  b # same")},
				{preamble: "---\n", content: "c: 1\n"},
			},
		},
		"comment on the line of a no-break space value": {
			input: stringtest.LinesLF(
				"a:",
				"  \u00a0 # same",
				"---",
				"c: 1",
			),
			want: []doc{
				{content: stringtest.LinesLF("a:", "  \u00a0 # same")},
				{preamble: "---\n", content: "c: 1\n"},
			},
		},
		"comment on the last line of a value that opens with a no-break space": {
			input: stringtest.LinesLF(
				"a:",
				"  \u00a0",
				"  x # same",
				"---",
				"c: 1",
			),
			want: []doc{
				{content: stringtest.LinesLF("a:", "  \u00a0", "  x # same")},
				{preamble: "---\n", content: "c: 1\n"},
			},
		},
		"comment on the last line of a value that opens with an ideographic space": {
			input: stringtest.LinesLF(
				"a: \u3000",
				"  x # same",
				"---",
				"c: 1",
			),
			want: []doc{
				{content: stringtest.LinesLF("a: \u3000", "  x # same")},
				{preamble: "---\n", content: "c: 1\n"},
			},
		},
		"comment on the content line above a later header": {
			input: stringtest.LinesLF("a: 1 # same line", "---", "b: 2"),
			want: []doc{
				{content: "a: 1 # same line\n"},
				{preamble: "---\n", content: "b: 2\n"},
			},
		},
		"comment above a trailing header": {
			input: stringtest.LinesLF("a: 1", "# note", "---"),
			want: []doc{
				{content: "a: 1\n"},
				{preamble: stringtest.LinesLF("# note", "---")},
			},
		},
		"comment after a scalar": {
			input: stringtest.LinesLF("x", "# note"),
			want:  []doc{{content: stringtest.LinesLF("x", "# note")}},
		},
		"comment after a flow sequence": {
			input: stringtest.LinesLF("[1, 2]", "# note"),
			want:  []doc{{content: stringtest.LinesLF("[1, 2]", "# note")}},
		},
		"comment after a flow sequence above a later header": {
			input: stringtest.LinesLF("[1, 2]", "# note", "---", "[3]"),
			want: []doc{
				{content: "[1, 2]\n"},
				{preamble: stringtest.LinesLF("# note", "---"), content: "[3]\n"},
			},
		},
		"comment left of an indented mapping": {
			input: stringtest.LinesLF("---", "  a: 1", "# note"),
			want:  []doc{{preamble: "---\n", content: stringtest.LinesLF("a: 1", "# note")}},
		},
		"comment left of an indented mapping above a later header": {
			input: stringtest.LinesLF(
				"  a: 1",
				"# note",
				"---",
				"b: 2",
			),
			want: []doc{
				{content: "a: 1\n"},
				{preamble: stringtest.LinesLF("# note", "---"), content: "b: 2\n"},
			},
		},
		"comment between a directive and its header": {
			input: stringtest.LinesLF("%YAML 1.2", "# note", "---", "a: 1"),
			want:  []doc{{preamble: stringtest.LinesLF("%YAML 1.2", "# note", "---"), content: "a: 1\n"}},
		},
		"comment inside a flow mapping": {
			input: stringtest.LinesLF(
				"{",
				"  # note",
				"  a: 1",
				"}",
			),
			want: []doc{{content: stringtest.LinesLF(
				"{",
				"  # note",
				"  a: 1",
				"}",
			)}},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			source := niceyaml.NewSourceFromString(tc.input)

			_, err := source.File()
			require.NoError(t, err)

			docs := yamltest.AllDocuments(t, source, len(tc.want))
			spans := make([]position.Span, 0, len(docs))

			for i, d := range docs {
				spans = append(spans, d.Span())
				preamble := d.Preamble()
				all := d.Tokens()

				// The lexer gives the line ending after a "---" to the token
				// that follows it, so compare the text without the whitespace
				// around it.
				got := doc{
					preamble: strings.TrimSpace(yamltest.DumpTokenOrigins(preamble)),
					content:  strings.TrimSpace(yamltest.DumpTokenOrigins(all[len(preamble):])),
				}
				want := doc{
					preamble: strings.TrimSpace(tc.want[i].preamble),
					content:  strings.TrimSpace(tc.want[i].content),
				}

				assert.Equal(t, want, got, "document %d", i)
				assert.Equal(t, i, d.DocumentIndex())
			}

			if tc.spans != nil {
				assert.Equal(t, tc.spans, spans)
			}
		})
	}
}

func TestDocument_Ranges(t *testing.T) {
	t.Parallel()

	input := stringtest.Input(`
		kind: Deployment
		text: first
		  second
		block: |
		  line1
		  line2
		empty:
		list:
		  - a
		map:
		  x: 1
		  y: 2
		tagged:
		  !!str k: v
		items:
		  - name: a
		flow: [{a: 1}]
	`)

	tcs := map[string]struct {
		path paths.Path
		want position.Ranges
		is   error
	}{
		"value": {
			path: paths.Current().Child("kind"),
			want: position.Ranges{yamltest.LineRange(0, 6, 16)},
		},
		"key": {
			path: paths.Current().Child("kind").Key(),
			want: position.Ranges{yamltest.LineRange(0, 0, 4)},
		},
		"key of a sequence element is the element": {
			path: paths.Current().Child("list").Index(0).Key(),
			want: position.Ranges{yamltest.LineRange(8, 4, 5)},
		},
		"mapping with a tagged first key covers the key of its entry": {
			path: paths.Current().Child("tagged"),
			want: position.Ranges{yamltest.LineRange(12, 0, 6)},
		},
		"value across lines": {
			path: paths.Current().Child("text"),
			want: position.Ranges{
				yamltest.LineRange(1, 6, 11),
				yamltest.LineRange(2, 2, 8),
			},
		},
		"block scalar covers its indicator": {
			path: paths.Current().Child("block"),
			want: position.Ranges{yamltest.LineRange(3, 7, 8)},
		},
		"mapping covers the key of its entry": {
			path: paths.Current().Child("map"),
			want: position.Ranges{yamltest.LineRange(9, 0, 3)},
		},
		"sequence covers the key of its entry": {
			path: paths.Current().Child("list"),
			want: position.Ranges{yamltest.LineRange(7, 0, 4)},
		},
		"mapping element covers its dash": {
			path: paths.Current().Child("items").Index(0),
			want: position.Ranges{yamltest.LineRange(15, 2, 3)},
		},
		"key of a mapping element is the element": {
			path: paths.Current().Child("items").Index(0).Key(),
			want: position.Ranges{yamltest.LineRange(15, 2, 3)},
		},
		"flow mapping in a flow sequence covers its brace": {
			path: paths.Current().Child("flow").Index(0),
			want: position.Ranges{yamltest.LineRange(16, 7, 8)},
		},
		"block mapping at the root covers its first key": {
			path: paths.Current(),
			want: position.Ranges{yamltest.LineRange(0, 0, 4)},
		},
		"missing path": {
			path: paths.Current().Child("missing"),
			is:   paths.ErrNotFound,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			dd := yamltest.FirstDocument(t, input)

			got, err := dd.Ranges(tc.path)
			if tc.is != nil {
				require.ErrorIs(t, err, tc.is)

				bound := yamltest.RequireSourceError(t, err)
				assert.Same(t, dd.Source(), bound.Source())

				return
			}

			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}

	t.Run("value after an escaped scalar", func(t *testing.T) {
		t.Parallel()

		// The lexer drops the code of the escape from the scalar before b,
		// and the range of b still covers the runes of its value.
		dd := yamltest.FirstDocument(t, stringtest.LinesLF(`m: {a: "\u00e9", b: xx}`))

		got, err := dd.Ranges(paths.Current().Child("m").Child("b"))
		require.NoError(t, err)
		assert.Equal(t, position.Ranges{yamltest.LineRange(0, 20, 22)}, got)
	})

	t.Run("empty value", func(t *testing.T) {
		t.Parallel()

		// The parser makes a token for an empty value, which no line
		// holds, so a comment or the spaces after the colon at its
		// column are not its content.
		tcs := map[string]struct {
			input string
			path  paths.Path
		}{
			"nothing after the colon": {input: stringtest.LinesLF("a:", "b: 1"), path: paths.Current().Child("a")},
			"comment after the colon": {input: stringtest.LinesLF("a: # c", "b: 1"), path: paths.Current().Child("a")},
			"spaces after the colon":  {input: stringtest.LinesLF("a:   ", "b: 1"), path: paths.Current().Child("a")},
			"comment after the dash":  {input: stringtest.LinesLF("- # c", "- 1"), path: paths.Current().Index(0)},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				got, err := yamltest.FirstDocument(t, tc.input).Ranges(tc.path)
				require.NoError(t, err)
				assert.Nil(t, got)
			})
		}
	})

	t.Run("matches the ranges a bound error highlights", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, input)

		for _, path := range []paths.Path{
			paths.Current().Child("kind"),
			paths.Current().Child("text"),
			paths.Current().Child("block"),
			paths.Current().Child("empty"),
			paths.Current().Child("list"),
			paths.Current().Child("map"),
			paths.Current().Child("list").Index(0),
			paths.Current().Child("kind").Key(),
		} {
			want, err := dd.Ranges(path)
			require.NoError(t, err)

			view := dd.Source().View()

			bound := yamltest.RequireSourceError(t, dd.Bind(niceyaml.NewError("bad", niceyaml.AtPath(path))))
			require.True(t, bound.Annotate(view))

			var got position.Ranges

			for i := range view.All() {
				for _, o := range view.Overlays(i) {
					// A location with nothing to highlight, such as an
					// empty value, marks its line with an overlay of no
					// width, which Ranges does not report.
					if o.Cols.Len() == 0 {
						continue
					}

					got = append(got, yamltest.LineRange(i, o.Cols.Start, o.Cols.End))
				}
			}

			assert.Equal(t, want, got, "path %s", path)
		}
	})
}

func TestDocument_Tokens(t *testing.T) {
	t.Parallel()

	t.Run("yields the same tokens on every pass", func(t *testing.T) {
		t.Parallel()

		source := niceyaml.NewSourceFromString(stringtest.Input(`
			---
			a: 1
			---
			b: 2
		`))

		docs := yamltest.Documents(t, source, 2)

		// The tokens are the source's originals, not copies, so the first
		// token of the second document is the same pointer on each pass.
		for range 2 {
			assert.Len(t, docs[0].Tokens(), 4)

			second := docs[1].Tokens()
			require.Len(t, second, 4)
			assert.Same(t, source.Tokens()[4], second[0])
		}
	})

	t.Run("pairs tokens with documents closed by an end marker", func(t *testing.T) {
		t.Parallel()

		docs := yamltest.Documents(t, niceyaml.NewSourceFromString(stringtest.Input(`
			kind: A
			...
			kind: B
		`)), 2)

		for i, want := range []string{"A", "B"} {
			kind, err := yamltest.At(t, docs[i], paths.Current().Child("kind")).Decode[string](t.Context())
			require.NoError(t, err)
			assert.Equal(t, want, kind)

			tks := docs[i].Tokens()
			require.NotEmpty(t, tks, "document %d has no tokens", i)
			assert.Contains(t, tks[0].Origin, "kind")

			// The marker ends the first document alone.
			assert.Equal(t, i == 0, tks[len(tks)-1].Type == token.DocumentEndType, "document %d", i)
		}
	})

	t.Run("returns a copy of the token slice", func(t *testing.T) {
		t.Parallel()

		doc := yamltest.FirstDocument(t, stringtest.LinesLF("a: 1", "b: 2"))

		tks := doc.Tokens()
		require.NotEmpty(t, tks)

		first := tks[0]
		tks[0] = nil

		// The document keeps its own slice, so the caller's write does not
		// reach it.
		again := doc.Tokens()
		require.NotEmpty(t, again)
		assert.Same(t, first, again[0])
	})

	t.Run("splits consecutive headers into an empty document and the next one", func(t *testing.T) {
		t.Parallel()

		// Each header starts a document, so the first header holds an empty
		// document and the second opens the document that holds the content.
		input := stringtest.Input(`
			---
			---
			b: 2
		`)
		source := niceyaml.NewSourceFromString(input)
		d := yamltest.AllDocuments(t, source, 2)

		first := d[0].Tokens()
		require.Len(t, first, 1)
		assert.Same(t, source.Tokens()[0], first[0])

		second := d[1].Tokens()
		require.NotEmpty(t, second)
		assert.Equal(t, token.DocumentHeaderType, second[0].Type)
		assert.Equal(t, input, yamltest.DumpTokenOrigins(first)+yamltest.DumpTokenOrigins(second))

		empty, err := d[0].Decode[map[string]int](t.Context())
		require.NoError(t, err)
		assert.Nil(t, empty)

		got, err := d[1].Decode[map[string]int](t.Context())
		require.NoError(t, err)
		assert.Equal(t, map[string]int{"b": 2}, got)
	})

	t.Run("keeps every document of a stream that holds an empty one", func(t *testing.T) {
		t.Parallel()

		tcs := map[string]struct {
			input string
			// The value of each document, with nil for an empty one.
			want []map[string]int
		}{
			"consecutive headers": {
				input: stringtest.JoinLF(
					"a: 1",
					"---",
					"---",
					"b: 2",
					"---",
					"c: 3",
				),
				want: []map[string]int{{"a": 1}, nil, {"b": 2}, {"c": 3}},
			},
			"comment-only document": {
				input: stringtest.LinesLF(
					"a: 1",
					"---",
					"# placeholder",
					"---",
					"b: 2",
				),
				want: []map[string]int{{"a": 1}, nil, {"b": 2}},
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				var got []map[string]int

				for _, dd := range niceyaml.NewSourceFromString(tc.input).AllDocuments() {
					v, err := dd.Decode[map[string]int](t.Context())
					require.NoError(t, err)

					got = append(got, v)
				}

				assert.Equal(t, tc.want, got)
			})
		}
	})

	t.Run("splits consecutive headers past a comment on the first", func(t *testing.T) {
		t.Parallel()

		// The parser folds the comment into the first header, so the two
		// headers still meet with nothing between them.
		d := yamltest.AllDocuments(t, niceyaml.NewSourceFromString(stringtest.Input(`
			--- # first
			---
			port: http
		`)), 2)

		got, err := d[1].Decode[map[string]string](t.Context())
		require.NoError(t, err)
		assert.Equal(t, map[string]string{"port": "http"}, got)
	})
}

func TestDocument_View(t *testing.T) {
	t.Parallel()

	source := niceyaml.NewSourceFromString(stringtest.Input(`
		a: 1
		---
		spec:
		  hours:
		    open: "09:00"
		    close: "17:00"
		b: 2
	`))

	docs := yamltest.Documents(t, source, 2)
	second := docs[1]
	hours := yamltest.At(t, second, paths.Current().Child("spec", "hours"))

	t.Run("covers the document at its indices with the file's line numbers", func(t *testing.T) {
		t.Parallel()

		view := second.View()
		span := second.Span()

		assert.Equal(t, source.View().Slice(span).String(), view.String())
		assert.Equal(t, 2, view.Line(span.Start).Number())
		assert.False(t, view.Contains(0), "the view keeps the indices of the source")
		require.Equal(t, span.Len(), view.Count())

		want := span.Start

		for i, l := range view.All() {
			assert.Equal(t, want, i)
			assert.Same(t, source.Lines().Line(i), l)

			want++
		}

		// The held lines count from zero and keep the numbers of the file.
		held := view.Held()

		require.Equal(t, span.Len(), held.Len())
		assert.Same(t, source.Lines().Line(span.Start), held.Line(0))
		assert.Equal(t, 2, held.Line(0).Number())
	})

	t.Run("prints one document with the file's line numbers", func(t *testing.T) {
		t.Parallel()

		src := niceyaml.NewSourceFromString(stringtest.LinesLF(
			"a: 1",
			"---",
			"b: 2",
			"c: 3",
		))
		last := yamltest.Documents(t, src, 2)[1]

		p := yamltest.NewXMLPrinter(printer.WithGutter(printer.LineNumberGutter()))

		got := p.Print(src.View().Slice(last.Span()))
		assert.NotContains(t, got, "a</nameTag>")
		assert.Contains(t, got, "   2 ")
		assert.Contains(t, got, "   4 ")
		assert.NotContains(t, got, "   1 ")
	})

	t.Run("covers only the lines of a scoped Node", func(t *testing.T) {
		t.Parallel()

		view := hours.View()
		assert.Equal(t, stringtest.JoinLF(
			`   4 |   hours:`,
			`   5 |     open: "09:00"`,
			`   6 |     close: "17:00"`,
		), view.String())

		held := view.Held()

		require.Equal(t, 3, held.Len())
		assert.Equal(t, "  hours:", held.Line(0).Content())
		assert.Equal(t, 4, held.Line(0).Number())
	})

	t.Run("each call returns a view of its own", func(t *testing.T) {
		t.Parallel()

		doc := docs[0]

		first := doc.View()
		first.AnnotateLine(0, line.Annotation{Content: "here", Placement: line.Below})

		assert.NotEqual(t, first.String(), doc.View().String())
		assert.Equal(t, source.View().Slice(doc.Span()).String(), doc.View().String())
	})

	t.Run("a bound error marks the view", func(t *testing.T) {
		t.Parallel()

		bound := yamltest.RequireSourceError(t, second.Bind(yamltest.ErrorAt("closed", "b")))

		view := second.View()
		require.True(t, bound.Annotate(view))

		assert.Contains(t, view.String(), stringtest.JoinLF(
			"   7 | b: 2",
			"     |    ^",
		))
	})

	t.Run("an error at the root of a scoped block mapping lies in its view", func(t *testing.T) {
		t.Parallel()

		bound := yamltest.RequireSourceError(t, hours.Bind(errors.New("closed")))
		require.EqualError(t, bound, "4:3: $.spec.hours: closed")

		// The error points at the key, where the span of the mapping
		// starts.
		assert.Equal(t, position.New(3, 2), yamltest.PositionOf(t, bound))
		assert.Equal(t, position.NewSpan(3, 6), hours.Span())

		view := hours.View()
		require.True(t, bound.Annotate(view))
		assert.Equal(t, stringtest.JoinLF(
			"   4 |   hours:",
			"     |   ^^^^^ closed",
			`   5 |     open: "09:00"`,
			`   6 |     close: "17:00"`,
		), view.String())
	})

	t.Run("an error at the root of a scoped element lies in its view", func(t *testing.T) {
		t.Parallel()

		doc := yamltest.FirstDocument(t, stringtest.LinesLF(
			"items:",
			"  - name: a",
			"    port: 1",
		))
		item := yamltest.At(t, doc, paths.Current().Child("items").Index(0))

		bound := yamltest.RequireSourceError(t, item.Bind(errors.New("bad item")))
		require.EqualError(t, bound, "2:3: $.items[0]: bad item")

		view := item.View()
		require.True(t, bound.Annotate(view))
		assert.Equal(t, stringtest.JoinLF(
			"   2 |   - name: a",
			"     |   ^ bad item",
			"   3 |     port: 1",
		), view.String())
	})

	t.Run("diffs one document of a file that holds several", func(t *testing.T) {
		t.Parallel()

		before := yamltest.Documents(t, niceyaml.NewSourceFromString(stringtest.Input(`
			a: 1
			---
			b: 2
			c: 3
		`)), 2)
		after := yamltest.Documents(t, niceyaml.NewSourceFromString(stringtest.Input(`
			a: 9
			---
			b: 2
			c: 9
		`)), 2)

		result := yamltest.DiffLines(t, before[1].View(), after[1].View())

		assert.Equal(t, diff.Stats{Added: 1, Removed: 1}, result.Stats())

		unified := result.Unified().String()
		assert.Contains(t, unified, "c: 9")
		assert.NotContains(t, unified, "a: 9", "the first document is not in the diff")
	})
}

func TestNode_At_NotFound(t *testing.T) {
	t.Parallel()

	doc := yamltest.FirstDocument(t, stringtest.Input(`
		# c
		name: s
		hours:
		  open: 9
		items:
		  - x
		  - k: 1
		aliases:
		  - *nope
	`), niceyaml.WithName("c.yaml"))

	hoursPath := paths.Current().Child("hours")
	itemsPath := paths.Current().Child("items")

	tcs := map[string]struct {
		// The path of the Node that resolves path, and the path it gets.
		scope paths.Path
		path  paths.Path
		want  string
		// The path the error reports and the nearest node it is bound at.
		wantPath string
		near     string
	}{
		"a key the root leaves out binds at the first key of the root": {
			path:     paths.Current().Child("zzz"),
			want:     "c.yaml:2:1: $.zzz: not found",
			wantPath: "$.zzz",
			near:     "$",
		},
		"a key a mapping leaves out binds at the key of the mapping": {
			path:     hoursPath.Child("close"),
			want:     "c.yaml:3:1: $.hours.close: not found",
			wantPath: "$.hours.close",
			near:     "$.hours",
		},
		"a path below a key the root leaves out binds at the root": {
			path:     paths.Current().Child("zzz", "yyy"),
			want:     "c.yaml:2:1: $.zzz.yyy: not found",
			wantPath: "$.zzz.yyy",
			near:     "$",
		},
		"a key an element leaves out binds at the dash of the element": {
			path:     itemsPath.Index(1).Child("q"),
			want:     "c.yaml:7:3: $.items[1].q: not found",
			wantPath: "$.items[1].q",
			near:     "$.items[1]",
		},
		"a key the scope leaves out binds at the key of the scope": {
			scope:    hoursPath,
			path:     paths.Current().Child("close"),
			want:     "c.yaml:3:1: $.hours.close: not found",
			wantPath: "$.hours.close",
			near:     "$.hours",
		},
		"an index past the end of a sequence binds at the key of the sequence": {
			path:     itemsPath.Index(7),
			want:     "c.yaml:5:1: $.items[7]: not found",
			wantPath: "$.items[7]",
			near:     "$.items",
		},
		"an index past the end of the scope binds at the key of the scope": {
			scope:    itemsPath,
			path:     paths.Current().Index(7),
			want:     "c.yaml:5:1: $.items[7]: not found",
			wantPath: "$.items[7]",
			near:     "$.items",
		},
		"an index below a key the root leaves out binds at the root": {
			path:     paths.Current().Child("zzz").Index(0),
			want:     "c.yaml:2:1: $.zzz[0]: not found",
			wantPath: "$.zzz[0]",
			near:     "$",
		},
		"a key below a scalar binds at the scalar": {
			path:     paths.Current().Child("name", "sub"),
			want:     "c.yaml:2:7: $.name.sub: not found",
			wantPath: "$.name.sub",
			near:     "$.name",
		},
		"a key below a scalar element binds at the scalar": {
			path:     itemsPath.Index(0).Child("sub"),
			want:     "c.yaml:6:5: $.items[0].sub: not found",
			wantPath: "$.items[0].sub",
			near:     "$.items[0]",
		},
		"an index of a mapping binds at the key of the mapping": {
			path:     hoursPath.Index(3),
			want:     "c.yaml:3:1: $.hours[3]: not found",
			wantPath: "$.hours[3]",
			near:     "$.hours",
		},
		"a key of a sequence binds at the key of the sequence": {
			path:     itemsPath.Child("k"),
			want:     "c.yaml:5:1: $.items.k: not found",
			wantPath: "$.items.k",
			near:     "$.items",
		},
		// The `~` of an element selects nothing whatever the element holds,
		// so the path does not read the alias.
		"a key below the key of an element alias binds at the alias": {
			path:     paths.Current().Child("aliases").Index(0).Key().Child("x"),
			want:     "c.yaml:9:5: $.aliases[0]~.x: not found",
			wantPath: "$.aliases[0]~.x",
			near:     "$.aliases[0]",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			node := yamltest.AtOrSelf(t, doc, tc.scope)

			// An Error at the same path binds at the same place.
			required := yamltest.RequireSourceError(
				t,
				node.Bind(niceyaml.NewError("required", niceyaml.AtPath(tc.path))),
			)

			wantRange, ok := required.Range()
			require.True(t, ok)

			_, atErr := node.At(tc.path)
			_, rangesErr := node.Ranges(tc.path)

			for method, err := range map[string]error{"At": atErr, "Ranges": rangesErr} {
				t.Run(method, func(t *testing.T) {
					t.Parallel()

					require.ErrorIs(t, err, paths.ErrNotFound)
					require.EqualError(t, err, tc.want)

					// The error binds to the receiver, not to a copy scoped
					// to the path that did not resolve.
					bound := yamltest.RequireSourceError(t, err)
					assert.Same(t, node, yamltest.BoundNode(t, bound))
					assert.Same(t, doc, yamltest.BoundDocument(t, bound))

					rng, ok := bound.Range()
					assert.True(t, ok)
					assert.Equal(t, wantRange, rng)
					require.NoError(t, bound.Unresolved())

					path, ok := bound.Path()
					assert.True(t, ok)

					near, ok := bound.Nearest()
					assert.True(t, ok)

					assert.Equal(t, "not found", bound.Message())
					assert.Equal(t, tc.wantPath, path.String())
					assert.Equal(t, tc.near, near.String())
				})
			}
		})
	}

	t.Run("the excerpt marks the mapping that lacks the key", func(t *testing.T) {
		t.Parallel()

		_, err := doc.At(hoursPath.Child("close"))

		assert.Equal(t, stringtest.JoinLF(
			"c.yaml:3:1: $.hours.close: not found",
			"",
			"   2 | name: s",
			"   3 | hours:",
			"     | ^^^^^",
			"   4 |   open: 9",
		), niceyaml.FormatError(err, niceyaml.WithContextLines(1)))
	})

	t.Run("the excerpt marks the sequence that an index runs past", func(t *testing.T) {
		t.Parallel()

		_, err := doc.At(itemsPath.Index(7))

		assert.Equal(t, stringtest.JoinLF(
			"c.yaml:5:1: $.items[7]: not found",
			"",
			"   4 |   open: 9",
			"   5 | items:",
			"     | ^^^^^",
			"   6 |   - x",
		), niceyaml.FormatError(err, niceyaml.WithContextLines(1)))
	})

	t.Run("the excerpt marks the scalar that a key looks into", func(t *testing.T) {
		t.Parallel()

		_, err := doc.At(paths.Current().Child("name", "sub"))

		assert.Equal(t, stringtest.JoinLF(
			"c.yaml:2:7: $.name.sub: not found",
			"",
			"   1 | # c",
			"   2 | name: s",
			"     |       ^",
			"   3 | hours:",
		), niceyaml.FormatError(err, niceyaml.WithContextLines(1)))
	})

	t.Run("a document with no content has no node to bind at", func(t *testing.T) {
		t.Parallel()

		empty := yamltest.FirstDocumentWithPath(t, "# nothing\n", "e.yaml")
		path := paths.Current().Child("zzz")
		want := "e.yaml: $.zzz: not found: document has no content"

		_, atErr := empty.At(path)
		_, rangesErr := empty.Ranges(path)

		for _, err := range []error{atErr, rangesErr} {
			require.ErrorIs(t, err, paths.ErrNoDocument)
			require.EqualError(t, err, want)

			bound := yamltest.RequireSourceError(t, err)
			assert.Equal(t, "not found: document has no content", bound.Message())

			got, ok := bound.Path()
			require.True(t, ok)
			assert.Equal(t, "$.zzz", got.String())

			_, ok = bound.Position()
			assert.False(t, ok)
		}
	})

	t.Run("Nodes returns no error for a key a mapping leaves out", func(t *testing.T) {
		t.Parallel()

		nodes, err := doc.Nodes(hoursPath.Child("close"))
		require.NoError(t, err)
		assert.Empty(t, nodes)
	})

	t.Run("a key selector on a node without a key selects nothing", func(t *testing.T) {
		t.Parallel()

		tcs := map[string]struct {
			// The path of the Node that resolves path, and the path it gets.
			scope paths.Path
			path  paths.Path
			want  string
			// The range that Ranges gives for path, which still points at
			// the node before the `~`. Without one, Ranges fails as At does.
			ranges position.Ranges
		}{
			"the root": {
				path:   paths.Current().Key(),
				want:   "c.yaml:2:1: $~: not found",
				ranges: position.Ranges{yamltest.LineRange(1, 0, 4)},
			},
			"a scalar element": {
				path:   itemsPath.Index(0).Key(),
				want:   "c.yaml:6:5: $.items[0]~: not found",
				ranges: position.Ranges{yamltest.LineRange(5, 4, 5)},
			},
			"a mapping element": {
				path:   itemsPath.Index(1).Key(),
				want:   "c.yaml:7:3: $.items[1]~: not found",
				ranges: position.Ranges{yamltest.LineRange(6, 2, 3)},
			},
			"an element that is the scope": {
				scope:  itemsPath.Index(1),
				path:   paths.Current().Key(),
				want:   "c.yaml:7:3: $.items[1]~: not found",
				ranges: position.Ranges{yamltest.LineRange(6, 2, 3)},
			},
			"a selector after the key of an element": {
				path: itemsPath.Index(1).Key().Child("k"),
				want: "c.yaml:7:3: $.items[1]~.k: not found",
			},
			"a selector after the key of the root": {
				path: paths.Current().Key().Child("name"),
				want: "c.yaml:2:1: $~.name: not found",
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				node := yamltest.AtOrSelf(t, doc, tc.scope)

				_, err := node.At(tc.path)
				require.ErrorIs(t, err, paths.ErrNotFound)
				require.EqualError(t, err, tc.want)

				nodes, err := node.Nodes(tc.path)
				require.NoError(t, err)
				assert.Empty(t, nodes)

				ranges, err := node.Ranges(tc.path)
				if tc.ranges == nil {
					require.ErrorIs(t, err, paths.ErrNotFound)
					require.EqualError(t, err, tc.want)

					return
				}

				require.NoError(t, err)
				assert.Equal(t, tc.ranges, ranges)
			})
		}
	})

	t.Run("a key selector on a scoped entry selects its key", func(t *testing.T) {
		t.Parallel()

		key, err := yamltest.At(t, doc, hoursPath).At(paths.Current().Key())
		require.NoError(t, err)
		assert.Equal(t, "$.hours~", key.Path().String())
		assert.Equal(t, "hours", key.AST().String())
	})

	t.Run("a key selector on a key node keeps one key selector", func(t *testing.T) {
		t.Parallel()

		key := yamltest.At(t, doc, hoursPath.Key())

		again, err := key.At(paths.Current().Key())
		require.NoError(t, err)
		assert.True(t, key.Path().Equal(again.Path()))
		assert.Equal(t, "$.hours~", again.Path().String())

		err = key.Bind(niceyaml.NewError("bad", niceyaml.AtPath(paths.Current().Key())))
		require.EqualError(t, err, "c.yaml:3:1: $.hours~: bad")
	})
}

func TestNode_At_MergeLimit(t *testing.T) {
	t.Parallel()

	// The mapping d merges 1000 inline mappings that each merge the
	// sequence s of 1000 aliases, so one lookup in d reads s 1000 times.
	input := stringtest.LinesLF(
		"a: &a {z: 0}",
		"s: &s "+yamltest.FlowSequence("*a", 1000),
		"d: {<<: "+yamltest.FlowSequence("{<<: *s}", 1000)+"}",
	)
	path := paths.MustParse("$.d.nope")

	tcs := map[string]struct {
		err  error
		opts []niceyaml.SourceOption
	}{
		"limit on": {
			err: paths.ErrExcessiveMerging,
		},
		"limit off": {
			opts: []niceyaml.SourceOption{niceyaml.WithAliasLimit(false)},
			err:  paths.ErrNotFound,
		},
		"limit on with references": {
			opts: []niceyaml.SourceOption{yamltest.References("base: &base {port: 80}\n")},
			err:  paths.ErrExcessiveMerging,
		},
		"limit off with references": {
			opts: []niceyaml.SourceOption{
				niceyaml.WithAliasLimit(false),
				yamltest.References("base: &base {port: 80}\n"),
			},
			err: paths.ErrNotFound,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			doc := yamltest.FirstDocument(t, input, tc.opts...)

			_, err := doc.At(path)
			require.ErrorIs(t, err, tc.err)
			assert.True(t, niceyaml.IsInvalid(err))

			_, err = doc.Ranges(path)
			require.ErrorIs(t, err, tc.err)
			assert.True(t, niceyaml.IsInvalid(err))
		})
	}
}

func TestNode_At_UnresolvedAlias(t *testing.T) {
	t.Parallel()

	// A path that reads an alias that does not resolve fails with an error
	// bound at that alias, under the path that reads it, and Node.At and
	// Node.Nodes bind such errors alike. The message names the path when
	// the alias lies above it.
	tcs := map[string]struct {
		input string
		path  paths.Path
		want  string
		// Whether Node.Nodes resolves path, rather than Node.At.
		nodes bool
	}{
		"path that ends at the alias": {
			input: stringtest.LinesLF("a: 1", "b: {c: *q}"),
			path:  paths.Doc().Child("b", "c"),
			want:  "t.yaml:2:8: $.b.c: alias does not resolve: *q has no anchor before it",
		},
		"path below the alias": {
			input: stringtest.LinesLF("a: 1", "b: {c: *q}"),
			path:  paths.Doc().Child("b", "c", "z"),
			want:  "t.yaml:2:8: $.b.c: resolve $.b.c.z: alias does not resolve: *q has no anchor before it",
		},
		"recursive wildcard that lists the alias": {
			input: stringtest.LinesLF("a: 1", "b: {c: *q}"),
			path:  paths.Doc().RecursiveAll(),
			want:  "t.yaml:2:8: $.b.c: resolve $..*: alias does not resolve: *q has no anchor before it",
			nodes: true,
		},
		"wildcard that lists the alias": {
			input: stringtest.LinesLF("a: *nope", "b: 1"),
			path:  paths.Doc().ChildAll(),
			want:  "t.yaml:1:4: $.a: resolve $.*: alias does not resolve: *nope has no anchor before it",
			nodes: true,
		},
		"path below an alias of an anchor on a tagged alias inside it": {
			// The alias inside its own anchor does not resolve, so the error
			// binds there, as the error of the resolver names it.
			input: stringtest.LinesLF("a: &x !t *x", "b: *x"),
			path:  paths.Doc().Child("b", "c"),
			want:  "t.yaml:1:10: $.b: resolve $.b.c: alias does not resolve: *x forms a cycle",
		},
		"key below a mapping whose merge key holds the alias": {
			input: stringtest.LinesLF("b:", "  <<: *nope", "  c: 1"),
			path:  paths.Doc().Child("b", "x"),
			want:  "t.yaml:2:7: $.b: resolve $.b.x: alias does not resolve: *nope has no anchor before it",
		},
		"wildcards through a mapping whose merge key holds the alias": {
			input: stringtest.LinesLF("d: [1]", "b:", "  <<: *nope", "  c: 1"),
			path:  paths.Doc().ChildAll().ChildAll(),
			want:  "t.yaml:3:7: $.b: resolve $.*.*: alias does not resolve: *nope has no anchor before it",
			nodes: true,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			doc := yamltest.FirstDocumentWithPath(t, tc.input, "t.yaml")

			var err error

			if tc.nodes {
				_, err = doc.Nodes(tc.path)
			} else {
				_, err = doc.At(tc.path)
			}

			require.ErrorIs(t, err, paths.ErrAlias)
			assert.Equal(t, tc.want, err.Error())
		})
	}
}

func TestNode_At_Span(t *testing.T) {
	t.Parallel()

	multiDocument := stringtest.LinesLF(
		"a: 1",
		"---",
		"hours:",
		"  open: 9",
		"---",
		"- - a",
		"-",
		"  - b",
	)

	// Each case lists the span of the Node that path selects in the
	// document at index doc, and the view of that Node with an error
	// bound at the Node marked on it.
	tcs := map[string]struct {
		input string
		path  paths.Path
		want  string
		span  position.Span
		doc   int
	}{
		"block mapping under a key": {
			input: stringtest.LinesLF("hours:", "  open: 9", "  close: 5", "next: 1"),
			path:  paths.Current().Child("hours"),
			span:  position.NewSpan(0, 3),
			want: stringtest.JoinLF(
				"   1 | hours:",
				"     | ^^^^^ bad",
				"   2 |   open: 9",
				"   3 |   close: 5",
			),
		},
		"block sequence under a key": {
			input: stringtest.LinesLF("days:", "  - mon", "  - tue", "next: 1"),
			path:  paths.Current().Child("days"),
			span:  position.NewSpan(0, 3),
			want: stringtest.JoinLF(
				"   1 | days:",
				"     | ^^^^ bad",
				"   2 |   - mon",
				"   3 |   - tue",
			),
		},
		"block sequence in the column of its key": {
			input: stringtest.LinesLF("days:", "- mon", "- tue"),
			path:  paths.Current().Child("days"),
			span:  position.NewSpan(0, 3),
			want: stringtest.JoinLF(
				"   1 | days:",
				"     | ^^^^ bad",
				"   2 | - mon",
				"   3 | - tue",
			),
		},
		"element whose dash stands alone on its line": {
			input: stringtest.LinesLF("items:", "  -", "    a: 1", "  - b"),
			path:  paths.Current().Child("items").Index(0),
			span:  position.NewSpan(1, 3),
			want: stringtest.JoinLF(
				"   2 |   -",
				"     |   ^ bad",
				"   3 |     a: 1",
			),
		},
		"element that shares a line with its dash": {
			input: stringtest.LinesLF("items:", "  - a: 1", "    b: 2", "  - c"),
			path:  paths.Current().Child("items").Index(0),
			span:  position.NewSpan(1, 3),
			want: stringtest.JoinLF(
				"   2 |   - a: 1",
				"     |   ^ bad",
				"   3 |     b: 2",
			),
		},
		"sequence in an element whose dash stands alone": {
			input: stringtest.LinesLF("-", "  - a", "  - b"),
			path:  paths.Current().Index(0),
			span:  position.NewSpan(0, 3),
			want: stringtest.JoinLF(
				"   1 | -",
				"     | ^ bad",
				"   2 |   - a",
				"   3 |   - b",
			),
		},
		"flow mapping below its key": {
			input: stringtest.LinesLF("m:", "  {a: 1}", "next: 1"),
			path:  paths.Current().Child("m"),
			span:  position.NewSpan(0, 2),
			want: stringtest.JoinLF(
				"   1 | m:",
				"     | ^ bad",
				"   2 |   {a: 1}",
			),
		},
		"flow sequence below its key": {
			input: stringtest.LinesLF("s:", "  [1,", "   2]", "next: 1"),
			path:  paths.Current().Child("s"),
			span:  position.NewSpan(0, 3),
			want: stringtest.JoinLF(
				"   1 | s:",
				"     | ^ bad",
				"   2 |   [1,",
				"   3 |    2]",
			),
		},
		"flow mapping on the line of its key": {
			input: stringtest.LinesLF("m: {a: 1,", "  b: 2}"),
			path:  paths.Current().Child("m"),
			span:  position.NewSpan(0, 2),
			want: stringtest.JoinLF(
				"   1 | m: {a: 1,",
				"     | ^ bad",
				"   2 |   b: 2}",
			),
		},
		"explicit key": {
			input: stringtest.LinesLF("? hours", ": open: 9", "  close: 5"),
			path:  paths.Current().Child("hours"),
			span:  position.NewSpan(0, 3),
			want: stringtest.JoinLF(
				"   1 | ? hours",
				"     |   ^^^^^ bad",
				"   2 | : open: 9",
				"   3 |   close: 5",
			),
		},
		"explicit key with the value below its colon": {
			input: stringtest.LinesLF("? hours", ":", "  open: 9"),
			path:  paths.Current().Child("hours"),
			span:  position.NewSpan(0, 3),
			want: stringtest.JoinLF(
				"   1 | ? hours",
				"     |   ^^^^^ bad",
				"   2 | :",
				"   3 |   open: 9",
			),
		},
		"anchor on the key line": {
			input: stringtest.LinesLF("hours: &h", "  open: 9"),
			path:  paths.Current().Child("hours"),
			span:  position.NewSpan(0, 2),
			want: stringtest.JoinLF(
				"   1 | hours: &h",
				"     | ^^^^^ bad",
				"   2 |   open: 9",
			),
		},
		"tag on the key line": {
			input: stringtest.LinesLF("hours: !t", "  open: 9"),
			path:  paths.Current().Child("hours"),
			span:  position.NewSpan(0, 2),
			want: stringtest.JoinLF(
				"   1 | hours: !t",
				"     | ^^^^^ bad",
				"   2 |   open: 9",
			),
		},
		"comment on the key line": {
			input: stringtest.LinesLF("hours: # note", "  open: 9"),
			path:  paths.Current().Child("hours"),
			span:  position.NewSpan(0, 2),
			want: stringtest.JoinLF(
				"   1 | hours: # note",
				"     | ^^^^^ bad",
				"   2 |   open: 9",
			),
		},
		"anchor below the key": {
			input: stringtest.LinesLF("hours:", "  &h", "  open: 9"),
			path:  paths.Current().Child("hours"),
			span:  position.NewSpan(0, 3),
			want: stringtest.JoinLF(
				"   1 | hours:",
				"     | ^^^^^ bad",
				"   2 |   &h",
				"   3 |   open: 9",
			),
		},
		"comment and blank line below the key": {
			input: stringtest.LinesLF("hours:", "  # note", "", "  open: 9"),
			path:  paths.Current().Child("hours"),
			span:  position.NewSpan(0, 4),
			want: stringtest.JoinLF(
				"   1 | hours:",
				"     | ^^^^^ bad",
				"   2 |   # note",
				"   3 | ",
				"   4 |   open: 9",
			),
		},
		"comment on the line of a dash": {
			input: stringtest.LinesLF("- # note", "  a: 1"),
			path:  paths.Current().Index(0),
			span:  position.NewSpan(0, 2),
			want: stringtest.JoinLF(
				"   1 | - # note",
				"     | ^ bad",
				"   2 |   a: 1",
			),
		},
		"mapping under a key of an element": {
			input: stringtest.LinesLF("- hours:", "    open: 9", "  next: 1"),
			path:  paths.Current().Index(0).Child("hours"),
			span:  position.NewSpan(0, 2),
			want: stringtest.JoinLF(
				"   1 | - hours:",
				"     |   ^^^^^ bad",
				"   2 |     open: 9",
			),
		},
		// The key is an alias, and the path ends at the mapping under it.
		"mapping under an alias key": {
			input: stringtest.LinesLF("x: &k hours", "*k :", "  open: 9"),
			path:  paths.Current().Child("hours"),
			span:  position.NewSpan(1, 3),
			want: stringtest.JoinLF(
				"   2 | *k :",
				"     | ^ bad",
				"   3 |   open: 9",
			),
		},
		// The path goes through an alias and ends at a mapping, which
		// its own key introduces where the anchor holds it.
		"mapping below an alias": {
			input: stringtest.LinesLF("base: &b", "  hours:", "    open: 9", "mid: 0", "shop: *b"),
			path:  paths.Current().Child("shop", "hours"),
			span:  position.NewSpan(1, 3),
			want: stringtest.JoinLF(
				"   2 |   hours:",
				"     |   ^^^^^ bad",
				"   3 |     open: 9",
			),
		},
		"mapping a merge key brings in": {
			input: stringtest.LinesLF("base: &b", "  hours:", "    open: 9", "mid: 0", "shop:", "  <<: *b"),
			path:  paths.Current().Child("shop", "hours"),
			span:  position.NewSpan(1, 3),
			want: stringtest.JoinLF(
				"   2 |   hours:",
				"     |   ^^^^^ bad",
				"   3 |     open: 9",
			),
		},
		// A scalar is its own token, so its span leaves out the key
		// above it.
		"scalar below its key": {
			input: stringtest.LinesLF("plain:", "  one", "next: 1"),
			path:  paths.Current().Child("plain"),
			span:  position.NewSpan(1, 2),
			want: stringtest.JoinLF(
				"   2 |   one",
				"     |   ^^^ bad",
			),
		},
		"scalar on the key line": {
			input: stringtest.LinesLF("hours:", "  open: 9"),
			path:  paths.Current().Child("hours", "open"),
			span:  position.NewSpan(1, 2),
			want: stringtest.JoinLF(
				"   2 |   open: 9",
				"     |         ^ bad",
			),
		},
		"key": {
			input: stringtest.LinesLF("hours:", "  open: 9"),
			path:  paths.Current().Child("hours").Key(),
			span:  position.NewSpan(0, 1),
			want: stringtest.JoinLF(
				"   1 | hours:",
				"     | ^^^^^ bad",
			),
		},
		"mapping of a later document": {
			input: multiDocument,
			doc:   1,
			path:  paths.Current().Child("hours"),
			span:  position.NewSpan(2, 4),
			want: stringtest.JoinLF(
				"   3 | hours:",
				"     | ^^^^^ bad",
				"   4 |   open: 9",
			),
		},
		"element of a later document": {
			input: multiDocument,
			doc:   2,
			path:  paths.Current().Index(1),
			span:  position.NewSpan(6, 8),
			want: stringtest.JoinLF(
				"   7 | -",
				"     | ^ bad",
				"   8 |   - b",
			),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			docs := niceyaml.NewSourceFromString(tc.input).Documents()
			require.Greater(t, len(docs), tc.doc)

			scoped := yamltest.At(t, docs[tc.doc], tc.path)
			assert.Equal(t, tc.span, scoped.Span())

			// Node.Nodes scopes the same lines.
			found, err := docs[tc.doc].Nodes(tc.path)
			require.NoError(t, err)
			require.Len(t, found, 1)
			assert.Equal(t, tc.span, found[0].Span())

			// An error at the Node lies in its view.
			bound := yamltest.RequireSourceError(t, scoped.Bind(errors.New("bad")))

			view := scoped.View()
			require.True(t, bound.Annotate(view))
			assert.Equal(t, tc.want, view.String())
		})
	}

	t.Run("a path that ends at an alias covers the alias", func(t *testing.T) {
		t.Parallel()

		// An error at such a Node binds at the alias, inside the span.
		// Node.Target spans the content of the anchor.
		tcs := map[string]struct {
			input  string
			path   paths.Path
			target paths.Path
			want   string
			span   position.Span
			anchor position.Span
		}{
			"alias under a key": {
				input:  stringtest.LinesLF("base: &h", "  open: 9", "  close: 5", "mid: 0", "hours: *h"),
				path:   paths.Current().Child("hours"),
				target: paths.Doc().Child("base"),
				span:   position.NewSpan(4, 5),
				anchor: position.NewSpan(0, 3),
				want: stringtest.JoinLF(
					"   5 | hours: *h",
					"     |        ^ bad",
				),
			},
			"alias on the line below its key": {
				input:  stringtest.LinesLF("base: &h", "  open: 9", "hours:", "  *h"),
				path:   paths.Current().Child("hours"),
				target: paths.Doc().Child("base"),
				span:   position.NewSpan(3, 4),
				anchor: position.NewSpan(0, 2),
				want: stringtest.JoinLF(
					"   4 |   *h",
					"     |   ^ bad",
				),
			},
			"alias element": {
				input:  stringtest.LinesLF("base: &d", "  - mon", "days:", "  - *d"),
				path:   paths.Current().Child("days").Index(0),
				target: paths.Doc().Child("base"),
				span:   position.NewSpan(3, 4),
				anchor: position.NewSpan(0, 2),
				want: stringtest.JoinLF(
					"   4 |   - *d",
					"     |     ^ bad",
				),
			},
			"alias to a scalar": {
				input:  stringtest.LinesLF("base: &v one", "mid: 0", "plain:", "  *v"),
				path:   paths.Current().Child("plain"),
				target: paths.Doc().Child("base"),
				span:   position.NewSpan(3, 4),
				anchor: position.NewSpan(0, 1),
				want: stringtest.JoinLF(
					"   4 |   *v",
					"     |   ^ bad",
				),
			},
			"alias key": {
				input:  stringtest.LinesLF("x: &k hours", "*k :", "  open: 9"),
				path:   paths.Current().Child("hours").Key(),
				target: paths.Doc().Child("x"),
				span:   position.NewSpan(1, 2),
				anchor: position.NewSpan(0, 1),
				want: stringtest.JoinLF(
					"   2 | *k :",
					"     | ^ bad",
				),
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				doc := yamltest.FirstDocument(t, tc.input)

				scoped := yamltest.At(t, doc, tc.path)
				assert.Equal(t, tc.span, scoped.Span())

				found, err := doc.Nodes(tc.path)
				require.NoError(t, err)
				require.Len(t, found, 1)
				assert.Equal(t, tc.span, found[0].Span())

				bound := yamltest.RequireSourceError(t, scoped.Bind(errors.New("bad")))

				view := scoped.View()
				require.True(t, bound.Annotate(view))
				assert.Equal(t, tc.want, view.String())

				target, err := scoped.Target()
				require.NoError(t, err)
				assert.Equal(t, tc.target, target.Path())
				assert.Equal(t, tc.anchor, target.Span())
			})
		}
	})

	t.Run("an alias on a line of the span lies in the view", func(t *testing.T) {
		t.Parallel()

		tcs := map[string]struct {
			input string
			path  paths.Path
			want  string
		}{
			"alias in the flow sequence of its anchor": {
				input: "hours: [&h {open: 9}, *h]\n",
				path:  paths.Current().Child("hours").Index(1),
				want: stringtest.JoinLF(
					"   1 | hours: [&h {open: 9}, *h]",
					"     |                       ^ bad",
				),
			},
			// The node of a tagged alias is the tag, which sits on the
			// line of the alias, and the span leaves out the key above it.
			"tagged alias below its key": {
				input: stringtest.LinesLF("x: &x {k: 1}", "mid: 2", "c:", "  !t *x"),
				path:  paths.Current().Child("c"),
				want: stringtest.JoinLF(
					"   4 |   !t *x",
					"     |      ^ bad",
				),
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				scoped := yamltest.At(t, yamltest.FirstDocument(t, tc.input), tc.path)

				bound := yamltest.RequireSourceError(t, scoped.Bind(errors.New("bad")))

				view := scoped.View()
				require.True(t, bound.Annotate(view))
				assert.Equal(t, tc.want, view.String())
			})
		}
	})

	t.Run("a path that ends at an alias to a reference covers the alias", func(t *testing.T) {
		t.Parallel()

		// The anchor lies in the reference document. The span thus holds
		// the lines of the alias alone, and an error at or below the Node
		// binds in its view.
		tcs := map[string]struct {
			input string
			// The path of the error from the Node.
			at   paths.Path
			want string
			span position.Span
		}{
			"alias under a key": {
				input: stringtest.LinesLF("a: 1", "spec: *base"),
				at:    paths.Current(),
				span:  position.NewSpan(1, 2),
				want: stringtest.JoinLF(
					"   2 | spec: *base",
					"     |       ^ bad",
				),
			},
			"alias on the line below its key": {
				input: stringtest.LinesLF("a: 1", "spec:", "  *base"),
				at:    paths.Current(),
				span:  position.NewSpan(2, 3),
				want: stringtest.JoinLF(
					"   3 |   *base",
					"     |   ^ bad",
				),
			},
			"error below the alias": {
				input: stringtest.LinesLF("a: 1", "spec: *base"),
				at:    paths.Current().Child("port"),
				span:  position.NewSpan(1, 2),
				want: stringtest.JoinLF(
					"   2 | spec: *base",
					"     |       ^ bad",
				),
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				doc := yamltest.FirstDocument(t, tc.input, yamltest.References("base: &base {port: 80}\n"))

				scoped := yamltest.At(t, doc, paths.Current().Child("spec"))
				assert.Equal(t, niceyaml.NodeMapping, scoped.Kind())
				assert.Equal(t, tc.span, scoped.Span())

				bound := yamltest.RequireSourceError(t, scoped.Bind(niceyaml.NewError("bad", niceyaml.AtPath(tc.at))))

				view := scoped.View()
				require.True(t, bound.Annotate(view))
				assert.Equal(t, tc.want, view.String())
			})
		}
	})

	t.Run("the root keeps the lines of the document", func(t *testing.T) {
		t.Parallel()

		docs := yamltest.Documents(t, niceyaml.NewSourceFromString(multiDocument), 3)

		assert.Equal(t, position.NewSpan(1, 4), docs[1].Span())
		assert.Equal(t, position.NewSpan(4, 8), docs[2].Span())

		// At gives the root itself for the root path, so the Node keeps
		// the lines of the whole document.
		for _, doc := range docs[1:] {
			root := yamltest.At(t, doc, paths.Current())
			assert.Same(t, doc, root)
			assert.Equal(t, doc.Span(), root.Span())
		}
	})
}

func TestNode_Bind_Scope(t *testing.T) {
	t.Parallel()

	// The root holds a key close of its own, so a path the binding left
	// as the check wrote it would name that key.
	doc := yamltest.FirstDocument(t, stringtest.Input(`
		close: top
		shops:
		  - hours:
		      open: "17:00"
		      close: "09:00"
	`), niceyaml.WithName("cfg.yaml"))

	hoursPath := paths.Current().Child("shops").Index(0).Child("hours")
	hours := yamltest.At(t, doc, hoursPath)

	closePath := paths.Current().Child("close")

	tcs := map[string]struct {
		err error
		// The message of the binding, its path, when it carries one, the
		// message of each problem it heads, and of each of its details.
		want     string
		path     string
		children []string
		details  []string
	}{
		"a path joins the scope": {
			err:  yamltest.ErrorAt("bad", "close"),
			want: "cfg.yaml:5:14: $.shops[0].hours.close: bad",
			path: "$.shops[0].hours.close",
		},
		"the root path names the scope": {
			err:  yamltest.ErrorAt("bad"),
			want: "cfg.yaml:3:5: $.shops[0].hours: bad",
			path: "$.shops[0].hours",
		},
		"a key path joins the scope": {
			err:  niceyaml.NewError("bad", niceyaml.AtPath(closePath.Key())),
			want: "cfg.yaml:5:7: $.shops[0].hours.close~: bad",
			path: "$.shops[0].hours.close~",
		},
		"a key the scope leaves out binds at the key of the scope": {
			err:  yamltest.ErrorAt("bad", "nope"),
			want: "cfg.yaml:3:5: $.shops[0].hours.nope: bad",
			path: "$.shops[0].hours.nope",
		},
		"a path below a scalar joins the scope and binds at the scalar": {
			err:  yamltest.ErrorAt("bad", "open", "nope"),
			want: "cfg.yaml:4:13: $.shops[0].hours.open.nope: bad",
			path: "$.shops[0].hours.open.nope",
		},
		"a path beside a range joins the scope and binds at the range": {
			err: niceyaml.NewError("bad",
				niceyaml.AtPath(closePath),
				niceyaml.AtRange(yamltest.LineRange(4, 6, 11)),
			),
			want: "cfg.yaml:5:7: $.shops[0].hours.close: bad",
			path: "$.shops[0].hours.close",
		},
		"a rebased error joins the scope in front of its base": {
			err:  niceyaml.Rebase(errors.New("bad"), closePath),
			want: "cfg.yaml:5:14: $.shops[0].hours.close: bad",
			path: "$.shops[0].hours.close",
		},
		"an error with no location binds at the scope": {
			err:  errors.New("bad"),
			want: "cfg.yaml:3:5: $.shops[0].hours: bad",
			path: "$.shops[0].hours",
		},
		"a wrapped error with no location binds at the scope": {
			err:  fmt.Errorf("check: %w", errors.New("bad")),
			want: "cfg.yaml:3:5: $.shops[0].hours: check: bad",
			path: "$.shops[0].hours",
		},
		"the error of a context that ended gains no location": {
			err:  context.Canceled,
			want: "cfg.yaml: context canceled",
		},
		"an error that wraps the error of a context gains no location": {
			err:  fmt.Errorf("fetch: %w", context.DeadlineExceeded),
			want: "cfg.yaml: fetch: context deadline exceeded",
		},
		"a position alone stays as it is": {
			err:  niceyaml.NewError("bad", niceyaml.AtPosition(position.New(0, 7))),
			want: "cfg.yaml:1:8: bad",
		},
		// The text a wrapper wrote holds the message alone, so the binding
		// names the joined path once, in front of it.
		"a wrapper adds no path of its own": {
			err:  fmt.Errorf("check: %w", yamltest.ErrorAt("bad", "close")),
			want: "cfg.yaml:5:14: $.shops[0].hours.close: check: bad",
			path: "$.shops[0].hours.close",
		},
		// A multi-error keeps the message it wrote, which names none of
		// the paths, so each branch the scope locates follows it.
		"a multi-error lists each branch below its own message": {
			err: listError{
				yamltest.ErrorAt("bad open", "open"),
				yamltest.ErrorAt("bad close", "close"),
			},
			want: stringtest.JoinLF(
				"cfg.yaml: bad open; bad close",
				"cfg.yaml:4:13: $.shops[0].hours.open: bad open",
				"cfg.yaml:5:14: $.shops[0].hours.close: bad close",
			),
			children: []string{
				"cfg.yaml:4:13: $.shops[0].hours.open: bad open",
				"cfg.yaml:5:14: $.shops[0].hours.close: bad close",
			},
		},
		"a wrapper with two %w verbs lists each branch below its own message": {
			err: fmt.Errorf("%w; %w",
				yamltest.ErrorAt("bad open", "open"),
				yamltest.ErrorAt("bad close", "close"),
			),
			want: stringtest.JoinLF(
				"cfg.yaml: bad open; bad close",
				"cfg.yaml:4:13: $.shops[0].hours.open: bad open",
				"cfg.yaml:5:14: $.shops[0].hours.close: bad close",
			),
			children: []string{
				"cfg.yaml:4:13: $.shops[0].hours.open: bad open",
				"cfg.yaml:5:14: $.shops[0].hours.close: bad close",
			},
		},
		// Each branch is a problem of its own, so the branch with no
		// location binds at the scope whatever the others carry.
		"each line of a join joins the scope": {
			err: errors.Join(
				yamltest.ErrorAt("early", "open"),
				errors.New("plain"),
				yamltest.ErrorAt("late", "close"),
			),
			want: stringtest.JoinLF(
				"cfg.yaml:3:5: $.shops[0].hours: plain",
				"cfg.yaml:4:13: $.shops[0].hours.open: early",
				"cfg.yaml:5:14: $.shops[0].hours.close: late",
			),
			children: []string{
				"cfg.yaml:3:5: $.shops[0].hours: plain",
				"cfg.yaml:4:13: $.shops[0].hours.open: early",
				"cfg.yaml:5:14: $.shops[0].hours.close: late",
			},
		},
		"each branch of a join with no location binds at the scope": {
			err: errors.Join(errors.New("one"), errors.New("two")),
			want: stringtest.JoinLF(
				"cfg.yaml:3:5: $.shops[0].hours: one",
				"cfg.yaml:3:5: $.shops[0].hours: two",
			),
			children: []string{
				"cfg.yaml:3:5: $.shops[0].hours: one",
				"cfg.yaml:3:5: $.shops[0].hours: two",
			},
		},
		// A summary is a heading, so it takes no location, and each error
		// it heads binds on its own.
		"the errors a summary heads bind at the scope and the summary binds nowhere": {
			err: niceyaml.NewSummary("2 problems",
				errors.New("one"),
				errors.New("two"),
			),
			want: stringtest.JoinLF(
				"cfg.yaml: 2 problems",
				"cfg.yaml:3:5: $.shops[0].hours: one",
				"cfg.yaml:3:5: $.shops[0].hours: two",
			),
			children: []string{
				"cfg.yaml:3:5: $.shops[0].hours: one",
				"cfg.yaml:3:5: $.shops[0].hours: two",
			},
		},
		"each error a summary heads joins the scope": {
			err: niceyaml.NewSummary("2 problems",
				yamltest.ErrorAt("early", "open"),
				errors.New("plain"),
			),
			want: stringtest.JoinLF(
				"cfg.yaml: 2 problems",
				"cfg.yaml:3:5: $.shops[0].hours: plain",
				"cfg.yaml:4:13: $.shops[0].hours.open: early",
			),
			children: []string{
				"cfg.yaml:3:5: $.shops[0].hours: plain",
				"cfg.yaml:4:13: $.shops[0].hours.open: early",
			},
		},
		// A problem with no location binds at the scope whatever its
		// details carry, and each detail keeps its own location.
		"a problem with no location binds at the scope above its located details": {
			err: niceyaml.NewError("hours conflict", niceyaml.WithDetails(
				yamltest.ErrorAt("opens here", "open"),
				yamltest.ErrorAt("closes here", "close"),
			)),
			want: "cfg.yaml:3:5: $.shops[0].hours: hours conflict",
			path: "$.shops[0].hours",
			details: []string{
				"cfg.yaml:4:13: $.shops[0].hours.open: opens here",
				"cfg.yaml:5:14: $.shops[0].hours.close: closes here",
			},
		},
		// A detail explains its parent, so it takes no location.
		"a detail with no location stays as it is": {
			err: niceyaml.NewError("bad",
				niceyaml.AtPath(closePath),
				niceyaml.WithDetails(errors.New("reason")),
			),
			want:    "cfg.yaml:5:14: $.shops[0].hours.close: bad",
			path:    "$.shops[0].hours.close",
			details: []string{"cfg.yaml: reason"},
		},
		"a detail with no location under a problem with none stays as it is": {
			err:     niceyaml.NewError("bad", niceyaml.WithDetails(errors.New("reason"))),
			want:    "cfg.yaml:3:5: $.shops[0].hours: bad",
			path:    "$.shops[0].hours",
			details: []string{"cfg.yaml: reason"},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			err := hours.Bind(tc.err)
			require.EqualError(t, err, tc.want)

			bound := yamltest.RequireSourceError(t, err)
			assert.Same(t, hours, yamltest.BoundNode(t, bound))

			path, ok := bound.Path()
			assert.Equal(t, tc.path != "", ok)

			if ok {
				assert.Equal(t, tc.path, path.String())
			}

			assert.Equal(t, tc.children, errorTexts(yamltest.ProblemsOf(bound)))
			assert.Equal(t, tc.details, errorTexts(yamltest.DetailsOf(bound)))

			// A Rebase under the path of the scope binds the same way,
			// except for the error of a context that ended, which is about
			// the call, so a scoped bind gives it no location.
			if errors.Is(tc.err, context.Canceled) || errors.Is(tc.err, context.DeadlineExceeded) {
				return
			}

			rebased := yamltest.RequireSourceError(t, doc.Bind(niceyaml.Rebase(tc.err, hoursPath)))
			assert.Equal(t, tc.want, rebased.Error())
			assert.Equal(t, tc.children, errorTexts(yamltest.ProblemsOf(rebased)))
			assert.Equal(t, tc.details, errorTexts(yamltest.DetailsOf(rebased)))
		})
	}

	t.Run("the path resolves from the document and cuts back to the scope", func(t *testing.T) {
		t.Parallel()

		bound := yamltest.RequireSourceError(t, hours.Bind(yamltest.ErrorAt("bad", "close")))
		path := yamltest.PathOf(t, bound)

		ranges, err := yamltest.BoundDocument(t, bound).Ranges(path)
		require.NoError(t, err)
		assert.Equal(t, position.Ranges{yamltest.RangeOf(t, bound)}, ranges)

		written, ok := path.CutPrefix(yamltest.BoundNode(t, bound).Path())
		require.True(t, ok)
		assert.Equal(t, closePath, written)
	})

	t.Run("a binding comes back as it is", func(t *testing.T) {
		t.Parallel()

		bound := hours.Bind(yamltest.ErrorAt("bad", "close"))

		assert.Same(t, bound, hours.Bind(bound))
		assert.Same(t, bound, doc.Bind(bound))
		assert.Same(t, bound, yamltest.At(t, doc, paths.Current().Child("shops")).Bind(bound))
	})

	t.Run("the root of the document binds a path as written", func(t *testing.T) {
		t.Parallel()

		require.EqualError(t, doc.Bind(yamltest.ErrorAt("bad", "close")), "cfg.yaml:1:8: $.close: bad")
	})

	t.Run("the root of the document gives an error with no location none", func(t *testing.T) {
		t.Parallel()

		err := doc.Bind(errors.New("bad"))
		require.EqualError(t, err, "cfg.yaml: bad")

		assert.Same(t, doc, yamltest.BoundNode(t, yamltest.RequireSourceError(t, err)))

		// A Node scoped to the root path is the root of the document too.
		err = yamltest.At(t, doc, paths.Current()).Bind(errors.New("bad"))
		require.EqualError(t, err, "cfg.yaml: bad")
	})

	t.Run("an error with no location marks the value of the scope", func(t *testing.T) {
		t.Parallel()

		plain := errors.New("bad")

		bound := yamltest.RequireSourceError(t, hours.Bind(plain))
		require.ErrorIs(t, bound, plain)
		assert.Equal(t, "bad", bound.Message())

		// The binding points where an Error at the root path of the scope
		// does.
		located := yamltest.RequireSourceError(t, hours.Bind(yamltest.ErrorAt("bad")))
		assert.Equal(t, yamltest.RangeOf(t, located), yamltest.RangeOf(t, bound))
	})

	t.Run("a constructor declares whether the document is at fault", func(t *testing.T) {
		t.Parallel()

		errStat := errors.New("stat license: permission denied")

		tcs := map[string]struct {
			// The error Bind returns for what the package function builds.
			got     error
			want    string
			invalid bool
		}{
			"NewError binds at the scope": {
				got:     hours.Bind(niceyaml.NewError("bad hours")),
				want:    "cfg.yaml:3:5: $.shops[0].hours: bad hours",
				invalid: true,
			},
			"NewError through the root gains no location": {
				got:     doc.Bind(niceyaml.NewError("bad")),
				want:    "cfg.yaml: bad",
				invalid: true,
			},
			"Invalid declares the document at fault": {
				got:     hours.Bind(niceyaml.Invalid(errStat, niceyaml.AtPath(closePath))),
				want:    "cfg.yaml:5:14: $.shops[0].hours.close: stat license: permission denied",
				invalid: true,
			},
			"Invalid with no options binds at the scope": {
				got:     hours.Bind(niceyaml.Invalid(errStat)),
				want:    "cfg.yaml:3:5: $.shops[0].hours: stat license: permission denied",
				invalid: true,
			},
			"Unchecked declares no fault": {
				got:  hours.Bind(niceyaml.Unchecked(errStat, niceyaml.AtPath(closePath))),
				want: "cfg.yaml:5:14: $.shops[0].hours.close: stat license: permission denied",
			},
			"Unchecked keeps the fault of the error it wraps": {
				got:     hours.Bind(niceyaml.Unchecked(niceyaml.NewError("bad"), niceyaml.AtPath(closePath))),
				want:    "cfg.yaml:5:14: $.shops[0].hours.close: bad",
				invalid: true,
			},
			"Locate declares nothing": {
				got:  hours.Bind(niceyaml.Locate(errStat, niceyaml.AtPath(closePath))),
				want: "cfg.yaml:5:14: $.shops[0].hours.close: stat license: permission denied",
			},
			"Locate keeps the fault of the error it wraps": {
				got:     hours.Bind(niceyaml.Locate(niceyaml.NewError("bad"), niceyaml.AtPath(closePath))),
				want:    "cfg.yaml:5:14: $.shops[0].hours.close: bad",
				invalid: true,
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				require.EqualError(t, tc.got, tc.want)
				assert.Equal(t, tc.invalid, niceyaml.IsInvalid(tc.got))
			})
		}
	})

	t.Run("a nil error stays nil through a constructor", func(t *testing.T) {
		t.Parallel()

		var typed *niceyaml.Error

		require.NoError(t, hours.Bind(niceyaml.Invalid(nil, niceyaml.AtPath(closePath))))
		require.NoError(t, hours.Bind(niceyaml.Unchecked(nil, niceyaml.AtPath(closePath))))
		require.NoError(t, hours.Bind(niceyaml.Locate(nil, niceyaml.AtPath(closePath))))
		require.NoError(t, hours.Bind(niceyaml.Invalid(typed)))
		require.NoError(t, hours.Bind(niceyaml.Unchecked(typed)))
		require.NoError(t, hours.Bind(niceyaml.Locate(typed)))
	})

	t.Run("a Node at an alias to a reference binds at the alias", func(t *testing.T) {
		t.Parallel()

		doc := yamltest.FirstDocument(t, stringtest.LinesLF(
			"items:",
			"  - *kaffe",
		), niceyaml.WithName("order.yaml"), yamltest.References("kaffe: &kaffe {item: Kaffe, price: 35}\n"))

		item := yamltest.At(t, doc, paths.Current().Child("items").Index(0))

		// The document holds no line for the price, so a problem below the
		// alias binds at the alias, as one without a location does.
		tcs := map[string]struct {
			err  error
			want string
		}{
			"problem without a location": {
				err:  errors.New("too expensive"),
				want: "order.yaml:2:5: $.items[0]: too expensive",
			},
			"problem below the alias": {
				err:  yamltest.ErrorAt("too expensive", "price"),
				want: "order.yaml:2:5: $.items[0].price: too expensive",
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				err := item.Bind(tc.err)
				require.EqualError(t, err, tc.want)

				bound := yamltest.RequireSourceError(t, err)

				pos, ok := bound.Position()
				require.True(t, ok)
				assert.Equal(t, position.New(1, 4), pos)
			})
		}

		t.Run("the binding names the alias that the path enters", func(t *testing.T) {
			t.Parallel()

			bound := yamltest.RequireSourceError(t, item.Bind(yamltest.ErrorAt("too expensive", "price")))

			near, ok := bound.Nearest()
			require.True(t, ok)
			assert.Equal(t, paths.Doc().Child("items").Index(0), near)
			require.ErrorIs(t, bound.Unresolved(), paths.ErrReferenceAlias)
		})
	})

	t.Run("a validator's error with no location binds at the scope", func(t *testing.T) {
		t.Parallel()

		plain := errors.New("bad")
		want := "cfg.yaml:3:5: $.shops[0].hours: bad"

		fn := rejectingValidator(plain)

		// A direct call binds the error at the scope, as the Node does.
		require.EqualError(t, fn.Check(t.Context(), hours), want)
		require.EqualError(t, hours.Validate(t.Context(), fn), want)

		// Validate binds the error a validator leaves unbound.
		require.EqualError(t, hours.Validate(t.Context(), &fieldValidator{err: plain}), want)

		_, err := hours.Decode[checkHours](t.Context(), niceyaml.WithValidator(&fieldValidator{err: plain}))
		require.EqualError(t, err, want)

		multi := niceyaml.MultiValidator(&fieldValidator{err: plain}, &fieldValidator{err: errors.New("worse")})
		require.EqualError(t, hours.Validate(t.Context(), multi), stringtest.JoinLF(
			"cfg.yaml:3:5: $.shops[0].hours: bad",
			"cfg.yaml:3:5: $.shops[0].hours: worse",
		))

		// The same validators on the root of the document name the source
		// alone.
		require.EqualError(t, doc.Validate(t.Context(), fn), "cfg.yaml: bad")
		require.EqualError(t, doc.Validate(t.Context(), &fieldValidator{err: plain}), "cfg.yaml: bad")

		// A context that ended is no fault of the value.
		ended := &fieldValidator{err: context.DeadlineExceeded}
		require.EqualError(t, hours.Validate(t.Context(), ended), "cfg.yaml: context deadline exceeded")
	})

	t.Run("an error of the Node's own operation gains no location", func(t *testing.T) {
		t.Parallel()

		tcs := map[string]struct {
			run  func(t *testing.T) error
			is   error
			want string
		}{
			"a decode target that is no pointer": {
				run: func(t *testing.T) error {
					t.Helper()

					return hours.DecodeInto(t.Context(), nil)
				},
				is:   niceyaml.ErrDecodeTarget,
				want: "cfg.yaml: decode target is not a non-nil pointer: got nil",
			},
			"a wildcard path given to At": {
				run: func(t *testing.T) error {
					t.Helper()

					_, err := hours.At(paths.Current().ChildAll())

					return err //nolint:wrapcheck // The test inspects the error of the call.
				},
				is:   paths.ErrWildcard,
				want: "cfg.yaml: resolve $.shops[0].hours.*: wildcard path matches any number of nodes",
			},
			"a wildcard path given to Ranges": {
				run: func(t *testing.T) error {
					t.Helper()

					_, err := hours.Ranges(paths.Current().ChildAll())

					return err //nolint:wrapcheck // The test inspects the error of the call.
				},
				is:   paths.ErrWildcard,
				want: "cfg.yaml: resolve $.shops[0].hours.*: wildcard path matches any number of nodes",
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				err := tc.run(t)
				require.ErrorIs(t, err, tc.is)
				require.EqualError(t, err, tc.want)

				bound := yamltest.RequireSourceError(t, err)
				assert.Same(t, hours, yamltest.BoundNode(t, bound))

				_, ok := bound.Range()
				assert.False(t, ok)
			})
		}
	})

	t.Run("an error a scoped method returns names the scope once", func(t *testing.T) {
		t.Parallel()

		_, err := hours.At(paths.Current().Child("nope"))
		require.ErrorIs(t, err, paths.ErrNotFound)
		assert.Equal(t, 1, strings.Count(err.Error(), "$.shops[0].hours.nope"))
		assert.NotContains(t, err.Error(), "$.shops[0].hours.shops")
	})

	t.Run("a scoped validator and the same check at the root agree", func(t *testing.T) {
		t.Parallel()

		// The validator writes a `$` path, which reads from the root of the
		// document, so the binding leaves the path as written.
		reject := niceyaml.ValidatorFunc(func(_ context.Context, n *niceyaml.Node) error {
			return n.Document().Bind(niceyaml.NewError("bad", niceyaml.AtPath(n.Path().Join(closePath))))
		})

		want := "cfg.yaml:5:14: $.shops[0].hours.close: bad"

		require.EqualError(t, reject.Check(t.Context(), hours), want)
		require.EqualError(t, hours.Validate(t.Context(), reject), want)

		scoped := rejectingValidator(yamltest.ErrorAt("bad", "close"))

		require.EqualError(t, hours.Validate(t.Context(), scoped), want)

		_, err := hours.Decode[checkHours](t.Context(), niceyaml.WithValidator(scoped))
		require.EqualError(t, err, want)
	})
}

// nodePaths returns the path of each Node of nodes as a string, or nil for
// no nodes.
func nodePaths(nodes []*niceyaml.Node) []string {
	var got []string

	for _, n := range nodes {
		got = append(got, n.Path().String())
	}

	return got
}

func TestNode_Nodes(t *testing.T) {
	t.Parallel()

	doc := yamltest.FirstDocument(t, stringtest.Input(`
		items:
		  - name: tea
		    price: 1
		  - name: coffee
		    price: -1
		spec:
		  image: a
		  nested:
		    image: b
		aliased: &items
		  - x
		ref: *items
	`), niceyaml.WithName("m.yaml"))

	t.Run("scopes each element of a sequence", func(t *testing.T) {
		t.Parallel()

		items, err := doc.Nodes(paths.Current().Child("items").IndexAll())
		require.NoError(t, err)
		require.Len(t, items, 2)

		assert.Equal(t, "$.items[0]", items[0].Path().String())
		assert.Equal(t, "$.items[1]", items[1].Path().String())
		assert.Equal(t, position.NewSpan(1, 3), items[0].Span())
		assert.Same(t, doc, items[1].Document())

		got, err := yamltest.At(t, items[1], paths.Current().Child("price")).Decode[int](t.Context())
		require.NoError(t, err)
		assert.Equal(t, -1, got)

		// The error writes its path from the scope, and the message
		// carries it from the root of the document, beside the position
		// it resolved to.
		err = items[1].Bind(yamltest.ErrorAt("negative price", "price"))
		require.EqualError(t, err, "m.yaml:5:12: $.items[1].price: negative price")
	})

	t.Run("scopes each node the path selects", func(t *testing.T) {
		t.Parallel()

		tcs := map[string]struct {
			// The path of the Node that lists the nodes, and the path it
			// gets.
			scope paths.Path
			path  paths.Path
			want  []string
		}{
			"each entry a recursive selector finds": {
				scope: paths.Doc(),
				path:  paths.Current().Recursive("image"),
				want:  []string{"$.spec.image", "$.spec.nested.image"},
			},
			"from the scope of the receiver": {
				scope: paths.Doc().Child("spec"),
				path:  paths.Current().Recursive("image"),
				want:  []string{"$.spec.image", "$.spec.nested.image"},
			},
			"a single path yields its one node": {
				scope: paths.Doc(),
				path:  paths.Current().Child("spec", "image"),
				want:  []string{"$.spec.image"},
			},
			"a mapping wildcard on a sequence yields no nodes": {
				scope: paths.Doc(),
				path:  paths.Current().Child("items").ChildAll(),
			},
			"a path that selects nothing yields no nodes": {
				scope: paths.Doc(),
				path:  paths.Current().Child("missing").IndexAll(),
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				nodes, err := yamltest.At(t, doc, tc.scope).Nodes(tc.path)
				require.NoError(t, err)
				assert.Equal(t, tc.want, nodePaths(nodes))
			})
		}
	})

	t.Run("scopes every node at any depth", func(t *testing.T) {
		t.Parallel()

		config := yamltest.FirstDocument(t, stringtest.Input(`
			defaults: &defaults
			  logLevel: info
			server:
			  <<: *defaults
			  listen_addr: ":80"
			  TLS:
			    cert_file: a.pem
			routes:
			  - match: /api
			    upstreamURL: http://api
			  - *defaults
		`), niceyaml.WithName("c.yaml"))

		nodes, err := config.Nodes(paths.Current().RecursiveAll())
		require.NoError(t, err)

		// The merged key and the alias are listed where the source writes
		// them, not again where they are used.
		assert.Equal(t, []string{
			"$.defaults", "$.defaults.logLevel",
			"$.server", "$.server.listen_addr", "$.server.TLS", "$.server.TLS.cert_file",
			"$.routes", "$.routes[0]", "$.routes[0].match", "$.routes[0].upstreamURL", "$.routes[1]",
		}, nodePaths(nodes))

		snake := regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

		var errs []error

		for _, n := range nodes {
			sel, ok := n.Path().Last()
			if ok && sel.Kind == paths.SelectorChild && !snake.MatchString(sel.Name) {
				errs = append(errs, config.Bind(niceyaml.NewError(
					fmt.Sprintf("key %q is not snake_case", sel.Name),
					niceyaml.AtPath(n.Path().Key()),
				)))
			}
		}

		got := make([]string, 0, len(errs))
		for _, err := range errs {
			got = append(got, err.Error())
		}

		assert.Equal(t, []string{
			`c.yaml:2:3: $.defaults.logLevel~: key "logLevel" is not snake_case`,
			`c.yaml:6:3: $.server.TLS~: key "TLS" is not snake_case`,
			`c.yaml:10:5: $.routes[0].upstreamURL~: key "upstreamURL" is not snake_case`,
		}, got)
	})

	t.Run("scopes each entry of a mapping", func(t *testing.T) {
		t.Parallel()

		workflow := yamltest.FirstDocument(t, stringtest.Input(`
			defaults: &defaults
			  lint:
			    runs-on: linux
			jobs:
			  <<: *defaults
			  build:
			    runs-on: mac
			  3.10:
			    runs-on: bsd
			    steps:
			      - uses: checkout
			      - run: make
		`), niceyaml.WithName("w.yaml"))

		jobs, err := workflow.Nodes(paths.Current().Child("jobs").ChildAll())
		require.NoError(t, err)
		require.Len(t, jobs, 3)

		type job struct {
			RunsOn string `yaml:"runs-on"`
		}

		var gotPaths, gotNames, gotTexts, gotRunners []string

		for _, j := range jobs {
			gotPaths = append(gotPaths, j.Path().String())

			// The key decodes as the decoder reads it, so the key 3.10
			// gives 3.1, while the path keeps the text of the source.
			key := yamltest.At(t, j, paths.Current().Key())

			name, err := key.Decode[string](t.Context())
			require.NoError(t, err)

			gotNames = append(gotNames, name)
			gotTexts = append(gotTexts, key.AST().GetToken().Value)

			got, err := j.Decode[job](t.Context())
			require.NoError(t, err)

			gotRunners = append(gotRunners, got.RunsOn)

			// A Node scoped to one entry is the Node its own path selects.
			assert.Same(t, j.AST(), yamltest.At(t, workflow, j.Path()).AST())
		}

		assert.Equal(t, []string{"$.jobs.lint", "$.jobs.build", "$.jobs.'3.10'"}, gotPaths)
		assert.Equal(t, []string{"lint", "build", "3.1"}, gotNames)
		assert.Equal(t, []string{"lint", "build", "3.10"}, gotTexts)
		assert.Equal(t, []string{"linux", "mac", "bsd"}, gotRunners)

		// A merged entry lies where its anchor defines it, from its key
		// there.
		assert.Equal(t, position.NewSpan(1, 3), jobs[0].Span())
		assert.Same(t, workflow, jobs[2].Document())

		err = jobs[2].Bind(yamltest.ErrorAt("unknown runner", "runs-on"))
		require.EqualError(t, err, "w.yaml:9:14: $.jobs.'3.10'.runs-on: unknown runner")

		uses, err := workflow.Nodes(paths.MustParse("$.jobs.*.steps[*].uses"))
		require.NoError(t, err)
		require.Len(t, uses, 1)
		assert.Equal(t, "$.jobs.'3.10'.steps[0].uses", uses[0].Path().String())

		keys, err := workflow.Nodes(paths.Current().Child("jobs").ChildAll().Key())
		require.NoError(t, err)
		require.Len(t, keys, 3)
		assert.Equal(t, "$.jobs.build~", keys[1].Path().String())
	})

	t.Run("scopes the keys alone through a key selector", func(t *testing.T) {
		t.Parallel()

		listed := yamltest.FirstDocument(t, stringtest.Input(`
			name: x
			tags: [snake_case_value, other]
			items:
			  - one
			  - k: v
		`))

		tcs := map[string]struct {
			path paths.Path
			want []string
		}{
			"every key at any depth": {
				path: paths.Doc().RecursiveAll().Key(),
				want: []string{"$.name~", "$.tags~", "$.items~", "$.items[1].k~"},
			},
			"every key with a name": {
				path: paths.Doc().Recursive("k").Key(),
				want: []string{"$.items[1].k~"},
			},
			"every element has no key": {
				path: paths.Doc().Child("items").IndexAll().Key(),
			},
			"one element has no key": {
				path: paths.Doc().Child("items").Index(0).Key(),
			},
			"the root has no key": {
				path: paths.Doc().Key(),
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				keys, err := listed.Nodes(tc.path)
				require.NoError(t, err)
				assert.Equal(t, tc.want, nodePaths(keys))

				// Each Node is a key, so its own range is its key range.
				for _, key := range keys {
					got, ok := key.KeyRange()
					require.True(t, ok, key.Path().String())

					want, ok := key.Range()
					require.True(t, ok)
					assert.Equal(t, want, got)
				}
			})
		}
	})

	t.Run("a merge key that does not resolve is an error bound to the source", func(t *testing.T) {
		t.Parallel()

		broken := yamltest.FirstDocument(t, stringtest.LinesLF(
			"jobs:",
			"  <<: *missing",
			"  build: 1",
		))

		_, err := broken.Nodes(paths.Current().Child("jobs").ChildAll())
		require.ErrorIs(t, err, paths.ErrAlias)

		bound := yamltest.RequireSourceError(t, err)
		assert.Same(t, broken.Source(), bound.Source())
	})

	t.Run("keeps document order for nested recursive matches", func(t *testing.T) {
		t.Parallel()

		nested := yamltest.FirstDocument(t, stringtest.LinesLF(
			"a:",
			"  b:",
			"    a:",
			"      c: 1",
			"  c: 2",
		))

		// The outer entry a comes first, but its c follows the c of the
		// inner one in the source.
		nodes, err := nested.Nodes(paths.Current().Recursive("a").Child("c"))
		require.NoError(t, err)
		assert.Equal(t, []string{"$.a.b.a.c", "$.a.c"}, nodePaths(nodes))
	})

	t.Run("a duplicate key scopes the entry the decode keeps", func(t *testing.T) {
		t.Parallel()

		dup := yamltest.FirstDocument(t, stringtest.LinesLF("a: 1", "a: 2"), niceyaml.WithAllowDuplicateKeys(true))

		// The path $.a selects the later entry, so `..a` lists only that
		// one.
		entries, err := dup.Nodes(paths.Current().Recursive("a"))
		require.NoError(t, err)
		require.Len(t, entries, 1)
		assert.Equal(t, "$.a", entries[0].Path().String())

		got, err := entries[0].Decode[int](t.Context())
		require.NoError(t, err)
		assert.Equal(t, 2, got)
	})

	t.Run("an empty block scalar stays off the line of its sibling", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, stringtest.LinesLF(
			"x:",
			"  a: |",
			"  b: 1",
		))

		nodes, err := dd.Nodes(paths.Current().Child("x", "a"))
		require.NoError(t, err)
		require.Len(t, nodes, 1)
		assert.Equal(t, position.NewSpan(1, 2), nodes[0].Span())
	})

	t.Run("a node through an alias keeps the path as written", func(t *testing.T) {
		t.Parallel()

		nodes, err := doc.Nodes(paths.Current().Child("ref").IndexAll())
		require.NoError(t, err)
		require.Len(t, nodes, 1)
		assert.Equal(t, "$.ref[0]", nodes[0].Path().String())

		// The content lies at the anchor, so the lines are the anchor's.
		assert.Equal(t, position.NewSpan(10, 11), nodes[0].Span())
	})

	t.Run("an error at the root of an aliased scope binds at the alias", func(t *testing.T) {
		t.Parallel()

		ref := yamltest.At(t, doc, paths.Current().Child("ref"))
		assert.Equal(t, position.NewSpan(11, 12), ref.Span())

		// The path points at the alias, so the error binds there, where
		// the document binds the same path, and the view of the scope
		// marks it.
		err := ref.Bind(yamltest.ErrorAt("bad"))
		require.EqualError(t, err, "m.yaml:12:6: $.ref: bad")

		bound := yamltest.RequireSourceError(t, err)
		assert.True(t, bound.Annotate(ref.View()))

		// The view of the content of the anchor does not hold the alias.
		target, err := ref.Target()
		require.NoError(t, err)
		assert.Equal(t, position.NewSpan(9, 11), target.Span())
		assert.False(t, bound.Annotate(target.View()))

		err = doc.Bind(yamltest.ErrorAt("bad", "ref"))
		require.EqualError(t, err, "m.yaml:12:6: $.ref: bad")
	})

	t.Run("an alias to a reference is listed as the alias", func(t *testing.T) {
		t.Parallel()

		doc := yamltest.FirstDocument(t, stringtest.LinesLF(
			"items:",
			"  - *kaffe",
			"  - {item: Te, price: 30}",
		), niceyaml.WithName("order.yaml"), yamltest.References("kaffe: &kaffe {item: Kaffe, price: 35}\n"))

		items, err := doc.Nodes(paths.Current().Child("items").IndexAll())
		require.NoError(t, err)
		require.Len(t, items, 2)

		assert.Equal(t, "$.items[0]", items[0].Path().String())
		assert.Equal(t, "*kaffe", items[0].AST().String())
		assert.Equal(t, position.NewSpan(1, 2), items[0].Span())
		assert.Equal(t, "$.items[1]", items[1].Path().String())

		got, err := items[0].Decode[map[string]any](t.Context())
		require.NoError(t, err)
		assert.Equal(t, map[string]any{"item": "Kaffe", "price": uint64(35)}, got)
	})

	t.Run("a descent fails at an entry that a merge of a reference decides", func(t *testing.T) {
		t.Parallel()

		// The merge key of web brings in environment from the reference,
		// which then wins over the entry that web writes before it. The
		// descent fails at the alias, as a path through web.environment
		// does. The merge key of api does not bring in environment, so api
		// writes its own. The last text writes the merge key first, a
		// control in which web writes its own environment.
		tcs := map[string]struct {
			input string
			// The paths of the nodes, or the error.
			want []string
			err  string
		}{
			"entry before a merge key that brings in its key": {
				input: stringtest.LinesLF(
					"services:",
					"  web:",
					"    environment: {A: 1}",
					"    <<: *base",
					"  db:",
					"    environment: {A: 2}",
				),
				err: "compose.yaml:4:9: $.services.web.<<: resolve $..environment: alias reads a reference document: *base",
			},
			"entry before a merge key that does not bring in its key": {
				input: stringtest.LinesLF(
					"services:",
					"  api:",
					"    environment: {A: 1}",
					"    <<: *image",
					"  db:",
					"    environment: {A: 2}",
				),
				want: []string{"$.services.api.environment", "$.services.db.environment"},
			},
			"entry after the merge key": {
				input: stringtest.LinesLF(
					"services:",
					"  web:",
					"    <<: *base",
					"    environment: {A: 1}",
					"  db:",
					"    environment: {A: 2}",
				),
				want: []string{"$.services.web.environment", "$.services.db.environment"},
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				doc := yamltest.FirstDocument(t, tc.input, niceyaml.WithName("compose.yaml"),
					yamltest.References(stringtest.LinesLF(
						"base: &base {image: app, environment: {B: 1}}",
						"image: &image {image: app}",
					)))

				nodes, err := doc.Nodes(paths.Current().Recursive("environment"))
				if tc.err != "" {
					require.ErrorIs(t, err, paths.ErrReferenceAlias)
					require.EqualError(t, err, tc.err)

					return
				}

				require.NoError(t, err)

				got := make([]string, 0, len(nodes))
				for _, n := range nodes {
					got = append(got, n.Path().String())
				}

				assert.Equal(t, tc.want, got)
			})
		}
	})

	t.Run("a document with no content yields no nodes", func(t *testing.T) {
		t.Parallel()

		// A document that holds null yields none either, so a loop over
		// the items of each document reads an empty one the same way.
		inputs := map[string]string{
			"empty file":           "",
			"whitespace":           "\n",
			"comments alone":       "# only a comment\n",
			"header alone":         "---\n",
			"directive and header": "%YAML 1.2\n---\n",
			"null":                 "null\n",
		}

		selectors := map[string]paths.Path{
			"every element of a key": paths.Current().Child("items").IndexAll(),
			"every element":          paths.Current().IndexAll(),
			"every entry":            paths.Current().ChildAll(),
			"every node":             paths.Current().RecursiveAll(),
			"key":                    paths.Current().Child("items"),
		}

		for name, input := range inputs {
			for selector, path := range selectors {
				t.Run(name+"/"+selector, func(t *testing.T) {
					t.Parallel()

					nodes, err := yamltest.FirstDocument(t, input).Nodes(path)
					require.NoError(t, err)
					assert.Empty(t, nodes)
				})
			}
		}
	})

	t.Run("the root path selects the null at a header", func(t *testing.T) {
		t.Parallel()

		tcs := map[string]struct {
			input string
			want  int
		}{
			"empty file":           {input: ""},
			"comments alone":       {input: "# only a comment\n"},
			"header alone":         {input: "---\n", want: 1},
			"header and comment":   {input: "---\n# only a comment\n", want: 1},
			"directive and header": {input: "%YAML 1.2\n---\n", want: 1},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				nodes, err := yamltest.FirstDocument(t, tc.input).Nodes(paths.Doc())
				require.NoError(t, err)
				require.Len(t, nodes, tc.want)

				for _, n := range nodes {
					assert.Equal(t, "$", n.Path().String())
				}
			})
		}
	})

	t.Run("a path that fans out through nested aliases is refused", func(t *testing.T) {
		t.Parallel()

		bomb := yamltest.FirstDocument(t, yamltest.AliasLevels(5))

		// Each [*] lists ten aliases to the level below, so the path would
		// select 10^5 nodes. The decoder refuses the document too.
		_, err := bomb.Nodes(paths.MustParse("$.a[5][*][*][*][*][*]"))
		require.ErrorIs(t, err, niceyaml.ErrExcessiveAliasing)

		bound := yamltest.RequireSourceError(t, err)
		assert.Same(t, bomb.Source(), bound.Source())

		_, err = bomb.Decode[any](t.Context())
		require.ErrorIs(t, err, niceyaml.ErrExcessiveAliasing)
	})

	t.Run("a path past a limit is a fault of the document", func(t *testing.T) {
		t.Parallel()

		tcs := map[string]struct {
			err   error
			input string
			path  string
		}{
			"nested aliases": {
				input: yamltest.AliasLevels(5),
				path:  "$.a[5][*][*][*][*][*]",
				err:   niceyaml.ErrExcessiveAliasing,
			},
			"many merge sources": {
				input: yamltest.WideMerge(2000),
				path:  "$..nope",
				err:   paths.ErrExcessiveMerging,
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				doc := yamltest.FirstDocument(t, tc.input)

				_, err := doc.Nodes(paths.MustParse(tc.path))
				require.ErrorIs(t, err, tc.err)
				assert.True(t, niceyaml.IsInvalid(err))

				// The error is about the whole path, so it binds without a
				// location.
				bound := yamltest.RequireSourceError(t, err)
				assert.Same(t, doc.Source(), bound.Source())

				_, located := bound.Position()
				assert.False(t, located)
			})
		}
	})

	t.Run("the alias limit option turns off the path limits", func(t *testing.T) {
		t.Parallel()

		tcs := map[string]struct {
			input string
			path  string
			refs  string
			want  int
		}{
			"nested aliases": {
				input: yamltest.AliasLevels(5),
				path:  "$.a[5][*][*][*][*][*]",
				want:  100_000,
			},
			"nested aliases with references": {
				input: yamltest.AliasLevels(5),
				path:  "$.a[5][*][*][*][*][*]",
				refs:  "base: &base {port: 80}\n",
				want:  100_000,
			},
			"many merge sources": {
				input: yamltest.WideMerge(2000),
				path:  "$..nope",
				want:  0,
			},
			"many merge sources with references": {
				input: yamltest.WideMerge(2000),
				path:  "$..nope",
				refs:  "base: &base {port: 80}\n",
				want:  0,
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				opts := []niceyaml.SourceOption{niceyaml.WithAliasLimit(false)}
				if tc.refs != "" {
					opts = append(opts, yamltest.References(tc.refs))
				}

				doc := yamltest.FirstDocument(t, tc.input, opts...)

				nodes, err := doc.Nodes(paths.MustParse(tc.path))
				require.NoError(t, err)
				assert.Len(t, nodes, tc.want)

				err = doc.Validate(t.Context(), niceyaml.Each(paths.MustParse(tc.path),
					niceyaml.ValidatorFunc(func(context.Context, *niceyaml.Node) error { return nil })))
				require.NoError(t, err)
			})
		}
	})
}

func TestNode_DocumentResolver(t *testing.T) {
	t.Parallel()

	docs := yamltest.Documents(t, niceyaml.NewSourceFromString(stringtest.Input(`
		base: &base
		  name: app
		items:
		  - *base
		  - name: other
		---
		second: 1
	`)), 2)

	t.Run("every node of a document shares one resolver", func(t *testing.T) {
		t.Parallel()

		item := yamltest.At(t, docs[0], paths.Current().Child("items").Index(1))

		assert.Same(t, docs[0].DocumentResolver(), item.DocumentResolver())
		assert.Same(t, docs[0].DocumentResolver(), item.Document().DocumentResolver())
		assert.NotSame(t, docs[0].DocumentResolver(), docs[1].DocumentResolver())
	})

	t.Run("paths resolve from the document root", func(t *testing.T) {
		t.Parallel()

		item := yamltest.At(t, docs[0], paths.Current().Child("items").Index(1))

		got, err := item.DocumentResolver().Node(item.Path())
		require.NoError(t, err)
		assert.Same(t, item.AST(), got)

		// The resolver does not have the Node as a current node, so an
		// `@` path below the Node starts with the path of the Node.
		name := paths.Current().Child("name")

		_, err = item.DocumentResolver().Token(name)
		require.ErrorIs(t, err, paths.ErrNoCurrentNode)

		tk, err := item.DocumentResolver().Token(item.Path().Join(name))
		require.NoError(t, err)
		assert.Equal(t, "other", tk.Value)
	})

	t.Run("an alias in a scoped node reaches its anchor", func(t *testing.T) {
		t.Parallel()

		item := yamltest.At(t, docs[0], paths.Current().Child("items").Index(0))

		got, err := item.DocumentResolver().Deref(item.AST())
		require.NoError(t, err)

		want, err := paths.NewResolver(docs[0].DocumentAST()).Node(paths.Doc().Child("base"))
		require.NoError(t, err)
		assert.Same(t, want, got)
		assert.Equal(t, "  name: app", got.String())
	})

	t.Run("a document that did not parse resolves no path", func(t *testing.T) {
		t.Parallel()

		// A path that lists nothing in an empty document is an error
		// here, with or without a header above the document.
		for name, input := range map[string]string{
			"no header": "items: [\n",
			"header":    "---\nitems: [\n",
		} {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				doc := yamltest.UnparsedDocument(t, input)
				require.ErrorIs(t, doc.Err(), niceyaml.ErrSyntax)

				resolver := doc.DocumentResolver()

				for _, path := range []paths.Path{
					paths.Doc(),
					paths.Doc().Child("items"),
					paths.Doc().Child("items").IndexAll(),
				} {
					matches, err := resolver.Matches(path)
					require.ErrorIs(t, err, paths.ErrNoDocument, path)
					assert.Empty(t, matches, path)
				}

				_, err := resolver.Node(paths.Doc())
				require.ErrorIs(t, err, paths.ErrNoDocument)

				_, err = resolver.Token(paths.Doc().Child("items"))
				require.ErrorIs(t, err, paths.ErrNoDocument)
			})
		}
	})
}
