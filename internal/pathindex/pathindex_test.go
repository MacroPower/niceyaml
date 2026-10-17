package pathindex_test

import (
	"strings"
	"testing"

	"github.com/goccy/go-yaml/token"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.jacobcolvin.com/x/stringtest"

	"go.jacobcolvin.com/niceyaml/internal/pathindex"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
)

// find returns the first token among tks with the value text.
func find(tb testing.TB, tks token.Tokens, text string) *token.Token {
	tb.Helper()

	for _, tk := range tks {
		if tk.Value == text && tk.Position != nil {
			return tk
		}
	}

	tb.Fatalf("no token %q", text)

	return nil
}

// foreign returns a token of another text, at an offset past the end of
// each text of these tests, so no node of their documents holds it.
func foreign(tb testing.TB) *token.Token {
	tb.Helper()

	return find(tb, yamltest.FirstDocument(tb, strings.Repeat("\n", 1000)+"z: 1\n").Tokens(), "z")
}

func TestIndex_PathAt(t *testing.T) {
	t.Parallel()

	input := stringtest.LinesLF(
		"a: 1",
		"b: [x, {c: y}]",
		"d:",
		"  - e",
		"  - f: g",
		"# note",
	)

	tcs := map[string]struct {
		text string
		want string
	}{
		"a key":                     {text: "a", want: "$.a~"},
		"a value":                   {text: "1", want: "$.a"},
		"an element":                {text: "x", want: "$.b[0]"},
		"a value in a flow mapping": {text: "y", want: "$.b[1].c"},
		"a flow bracket":            {text: "[", want: "$.b"},
		"a dash":                    {text: "-", want: "$.d[0]"},
		"a nested key":              {text: "f", want: "$.d[1].f~"},
		"a nested value":            {text: "g", want: "$.d[1].f"},
	}

	doc := yamltest.FirstDocument(t, input)
	idx := pathindex.New(doc.AST())
	tks := doc.Tokens()

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			path, ok := idx.PathAt(doc.Resolver(), find(t, tks, tc.text).Position.Offset)
			assert.True(t, ok)
			assert.Equal(t, tc.want, path.String())
		})
	}

	t.Run("an offset past the body", func(t *testing.T) {
		t.Parallel()

		_, ok := idx.PathAt(doc.Resolver(), len(input))
		assert.False(t, ok)
	})
}

func TestIndex_PathAt_Unselected(t *testing.T) {
	t.Parallel()

	// A merge after the first entry overrides it, so no path selects it.
	doc := yamltest.FirstDocument(t, stringtest.LinesLF("base: &b {k: 1}", "m: {k: 2, <<: *b}"))
	idx := pathindex.New(doc.AST())

	_, ok := idx.PathAt(doc.Resolver(), find(t, doc.Tokens(), "2").Position.Offset)
	assert.False(t, ok)
}

func TestIndex_SitePath(t *testing.T) {
	t.Parallel()

	input := stringtest.LinesLF(
		"a: 1",
		"d:",
		"  - e",
		"  - f: g",
		"base: &b {k: 1}",
		"m: {k: 2, <<: *b}",
		"n:",
		"  <<: {p: {q: 3}}",
		"c: &c [x]",
		"*c : y",
	)

	tcs := map[string]struct {
		text  string
		want  string
		key   bool
		inKey bool
	}{
		"a key":                         {text: "a", want: "$.a", inKey: true},
		"a key of its mapping":          {text: "a", key: true, want: "$"},
		"a value":                       {text: "1", want: "$.a"},
		"a dash":                        {text: "-", want: "$.d[0]"},
		"a dash of its sequence":        {text: "-", key: true, want: "$.d"},
		"a key in an item":              {text: "f", want: "$.d[1].f", inKey: true},
		"a key in an item of its item":  {text: "f", key: true, want: "$.d[1]"},
		"an entry that no path selects": {text: "2", want: "$.m.k"},
		"a merge key":                   {text: "<<", want: "$.m"},
		"a merge key of its mapping":    {text: "<<", key: true, want: "$.m"},
		"a key inside a merge entry":    {text: "p", want: "$.n"},
		"a value inside a merge entry":  {text: "3", key: true, want: "$.n"},
		"a key without a name":          {text: "y", want: "$"},
	}

	doc := yamltest.FirstDocument(t, input)
	idx := pathindex.New(doc.AST())
	tks := doc.Tokens()

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			path, inKey := idx.SitePath(doc.Resolver(), find(t, tks, tc.text).Position.Offset, tc.key)
			assert.Equal(t, tc.want, path.String())
			assert.Equal(t, tc.inKey, inKey)
		})
	}

	t.Run("an offset past the body", func(t *testing.T) {
		t.Parallel()

		path, inKey := idx.SitePath(doc.Resolver(), len(input)+10, false)
		assert.Equal(t, "$", path.String())
		assert.False(t, inKey)
	})
}

func TestIndex_OwnerPath(t *testing.T) {
	t.Parallel()

	doc := yamltest.FirstDocument(t, stringtest.LinesLF("a: 1", "b:", "  - c", "d:"))
	idx := pathindex.New(doc.AST())
	tks := doc.Tokens()

	tcs := map[string]struct {
		tk      *token.Token
		want    string
		selects bool
		ok      bool
	}{
		"the colon of the first entry names the mapping": {
			tk: find(t, tks, ":"), want: "$", selects: true, ok: true,
		},
		"the dash of the first element names the sequence": {
			tk: find(t, tks, "-"), want: "$.b", selects: true, ok: true,
		},
		"a scalar names its entry": {
			tk: find(t, tks, "c"), want: "$.b[0]", selects: true, ok: true,
		},
		"a token of no node": {tk: foreign(t)},
		"a nil token":        {},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			path, selects, ok := idx.OwnerPath(doc.Resolver(), tc.tk)
			assert.Equal(t, tc.ok, ok)
			assert.Equal(t, tc.selects, selects)

			if tc.ok {
				assert.Equal(t, tc.want, path.String())
			}
		})
	}

	t.Run("a scalar below a tag that converts its collection", func(t *testing.T) {
		t.Parallel()

		// The resolver selects nothing below the tag, so the path spells the
		// node without selecting it.
		doc := yamltest.FirstDocument(t, "a: !!str &k [x, 4]\n")
		idx := pathindex.New(doc.AST())

		path, selects, ok := idx.OwnerPath(doc.Resolver(), find(t, doc.Tokens(), "x"))
		require.True(t, ok)
		assert.False(t, selects)
		assert.Equal(t, "$.a[0]", path.String())
	})
}

func TestIndex_OwnerToken(t *testing.T) {
	t.Parallel()

	doc := yamltest.FirstDocument(t, stringtest.LinesLF("a: 1", "b:", "  - c", "  - {g: h}", "d:", "  e: f"))
	idx := pathindex.New(doc.AST())
	tks := doc.Tokens()

	// The token is the one that the path of the node resolves to.
	tcs := map[string]struct {
		tk   *token.Token
		want string
	}{
		"the colon of the first entry names the root": {tk: find(t, tks, ":"), want: "a"},
		"a key names its entry":                       {tk: find(t, tks, "a"), want: "a"},
		"a scalar names itself":                       {tk: find(t, tks, "1"), want: "1"},
		"the dash of the first element names the key": {tk: find(t, tks, "-"), want: "b"},
		"a flow mapping in a block sequence":          {tk: find(t, tks, "{"), want: "-"},
		"a block mapping names its key":               {tk: find(t, tks, "e"), want: "e"},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := idx.OwnerToken(tc.tk)
			require.NotNil(t, got)
			assert.Equal(t, tc.want, got.Value)

			path, selects, ok := idx.OwnerPath(doc.Resolver(), tc.tk)
			require.True(t, ok)
			require.True(t, selects)

			want, err := doc.Resolver().Token(path)
			require.NoError(t, err)
			assert.Same(t, want, got)
		})
	}

	t.Run("a token of no node", func(t *testing.T) {
		t.Parallel()

		assert.Nil(t, idx.OwnerToken(foreign(t)))
		assert.Nil(t, idx.OwnerToken(nil))
	})
}

func TestIndex_MergedOwnerPath(t *testing.T) {
	t.Parallel()

	doc := yamltest.FirstDocument(t, stringtest.LinesLF(
		"b: &b {c: 1}",
		"m:",
		"  <<: {q: v1}",
		"  r: v2",
		"s:",
		"  <<: [{a: v3}, *b]",
		"n:",
		"  <<: {<<: {e: v4}}",
		`"<<": {d: v5}`,
	))
	idx := pathindex.New(doc.AST())
	tks := doc.Tokens()

	// The one alias of the document is the value of the merge key of s.
	var alias *token.Token

	for _, tk := range tks {
		if tk.Type == token.AliasType {
			alias = tk
		}
	}

	tcs := map[string]struct {
		tk   *token.Token
		want string
		ok   bool
	}{
		"an entry that a merge key brings in": {tk: find(t, tks, "v1"), want: "$.m.q", ok: true},
		"an entry of the mapping":             {tk: find(t, tks, "v2"), want: "$.m.r", ok: true},
		"an entry of an element of a sequence of merge sources": {
			tk: find(t, tks, "v3"), want: "$.s.a", ok: true,
		},
		"the alias of a merge key":      {tk: alias, want: "$.s", ok: true},
		"an entry of nested merge keys": {tk: find(t, tks, "v4"), want: "$.n.e", ok: true},
		// A quoted key is not a merge key.
		"an entry of a quoted key": {tk: find(t, tks, "v5"), want: "$.<<.d", ok: true},
		"a token of no node":       {tk: foreign(t)},
		"a nil token":              {},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			path, ok := idx.MergedOwnerPath(doc.Resolver(), tc.tk)
			assert.Equal(t, tc.ok, ok)

			if tc.ok {
				assert.Equal(t, tc.want, path.String())
			}
		})
	}
}

func TestNew_Empty(t *testing.T) {
	t.Parallel()

	doc := yamltest.FirstDocument(t, "# only a comment\n")

	_, ok := pathindex.New(doc.AST()).PathAt(doc.Resolver(), 0)
	assert.False(t, ok)
}
