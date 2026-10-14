package pathindex_test

import (
	"testing"

	"github.com/goccy/go-yaml/token"
	"github.com/stretchr/testify/assert"
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
		"a token of no node": {tk: token.New("z", "z", &token.Position{Offset: 1000})},
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
}

func TestNew_Empty(t *testing.T) {
	t.Parallel()

	doc := yamltest.FirstDocument(t, "# only a comment\n")

	_, ok := pathindex.New(doc.AST()).PathAt(doc.Resolver(), 0)
	assert.False(t, ok)
}
