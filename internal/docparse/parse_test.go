package docparse_test

import (
	"errors"
	"math"
	"strings"
	"testing"

	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/token"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.jacobcolvin.com/x/stringtest"

	"go.jacobcolvin.com/niceyaml/internal/docparse"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/position"
	"go.jacobcolvin.com/niceyaml/tokens"
)

func TestParse(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input string
		// The text of each document node of the file.
		want     []string
		failures int
	}{
		"one document": {
			input: "a: 1\n",
			want:  []string{"a: 1"},
		},
		"a header after a header starts a document": {
			input: stringtest.LinesLF("---", "---", "a: 1"),
			want:  []string{"---", stringtest.JoinLF("---", "a: 1")},
		},
		"a run the parser rejects keeps its place": {
			input:    stringtest.LinesLF("a: [", "---", "b: 1"),
			want:     []string{"", stringtest.JoinLF("---", "b: 1")},
			failures: 1,
		},
		"a stream of markers holds one document": {
			input: stringtest.LinesLF("...", "..."),
			want:  []string{"..."},
		},
		"an empty file holds one document": {
			input: "",
			want:  []string{""},
		},
		"a comment below a scalar root drops": {
			input: stringtest.LinesLF("a", "# c"),
			want:  []string{"a"},
		},
		"a comment between a directive and its header drops": {
			input: stringtest.LinesLF("%YAML 1.2", "# c", "---", "a: 1"),
			want:  []string{"%YAML 1.2", stringtest.JoinLF("---", "a: 1")},
		},
		"a comment before a ',' of a flow sequence drops": {
			input: stringtest.LinesLF("a: [1", "# c", ", 2]"),
			want:  []string{"a: [1, 2]"},
		},
		"a comment before the '}' of a flow mapping drops": {
			input: stringtest.LinesLF("{a: 1", "# c", "}"),
			want:  []string{"{a: 1}"},
		},
		"a comment where a flow mapping expects a key drops": {
			input: stringtest.LinesLF("a: {b: 1,", "# c", "  d: 2}"),
			want:  []string{"a: {b: 1, d: 2}"},
		},
		"a comment between the entries of a flow sequence stays": {
			input: stringtest.LinesLF("a: [1,", "# c", "  2]"),
			want:  []string{"a: [1, 2 # c]"},
		},
		"a comment after a '?' drops": {
			input: stringtest.LinesLF("? ", "# c", "  a", ": 1"),
			want:  []string{"? a: 1"},
		},
		"a comment before a ':' drops": {
			input: stringtest.LinesLF("? a", "# c", ": 1"),
			want:  []string{"? a: 1"},
		},
		"a comment left of the key of an anchor above its value drops": {
			input: stringtest.LinesLF("m:", "  a: &x", "# c", "    1"),
			want:  []string{stringtest.JoinLF("m:", "  a: &x 1")},
		},
		"a comment left of the key of an anchor above its sequence drops": {
			input: stringtest.LinesLF("m:", "  a: &x", "# c", "  - 1"),
			want:  []string{stringtest.JoinLF("m:", "  a: &x", "  - 1")},
		},
		"a comment at the key of an anchor above its value stays": {
			input: stringtest.LinesLF("a: &x", "# c", "  1"),
			want:  []string{"a: &x 1 # c"},
		},
		"a comment left of the key of an anchor above the next key stays": {
			input: stringtest.LinesLF("m:", "  a: &x", "# c", "  b: 1"),
			want:  []string{stringtest.JoinLF("m:", "  a: &x", "  # c", "  b: 1")},
		},
		"a comment above a key left of the key of an anchor drops": {
			input: stringtest.LinesLF("m:", "  a: &x", "  # c", "b: 1"),
			want:  []string{stringtest.JoinLF("m:", "  a: &x", "b: 1")},
		},
		"a comment between an anchor and the next key drops": {
			input: stringtest.LinesLF("a: &x", "# c", "b: 1"),
			want:  []string{stringtest.JoinLF("a: &x", "b: 1")},
		},
		"a comment on the line of an anchor stays as the one below drops": {
			input: stringtest.LinesLF("a: &x # c", "# d", "b: 1"),
			want:  []string{stringtest.JoinLF("a: &x # c", "b: 1")},
		},
		"a comment between an anchor in an entry and the next key drops": {
			input: stringtest.LinesLF("- a: &x", "  # c", "  b: 1"),
			want:  []string{stringtest.JoinLF("- a: &x", "  b: 1")},
		},
		"a comment between an anchor and the next '?' key drops": {
			input: stringtest.LinesLF("a: &x", "# c", "? b", ": 1"),
			want:  []string{stringtest.JoinLF("a: &x", "? b: 1")},
		},
		"a comment above a flow value at the key of an anchor stays": {
			input: stringtest.LinesLF("a: &x", "# c", "[1]"),
			want:  []string{"a: &x [1] # c"},
		},
		"a comment above a scalar at the key of an anchor stays": {
			input: stringtest.LinesLF("a: &x", "# c", "b"),
			want:  []string{"a: &x b # c"},
		},
		"a comment between an anchor and the next '-' drops": {
			input: stringtest.LinesLF("- &x", "# c", "- 1"),
			want:  []string{stringtest.JoinLF("- &x", "- 1")},
		},
		"a comment above a scalar at the '-' of an anchor stays": {
			input: stringtest.LinesLF("- &x", "# c", "a"),
			want:  []string{"- &x a # c"},
		},
		"a comment below a '&' with no name stays, and the run fails": {
			input:    stringtest.LinesLF("a: &", "# c", "b: 1"),
			want:     []string{""},
			failures: 1,
		},
		"a comment below an anchor at the root stays": {
			input: stringtest.LinesLF("&x", "# c", "a: 1"),
			want:  []string{stringtest.JoinLF("&x", "# c", "a: 1")},
		},
		"a comment below an anchor after a tag stays": {
			input: stringtest.LinesLF("a: !t &x", "# c", "  b"),
			want:  []string{"a: !t &x b # c"},
		},
		"a comment left of the root that closes the document of an anchor drops": {
			input: stringtest.LinesLF("  a: &x", "# c", "---", "b: 1"),
			want:  []string{"  a: &x", stringtest.JoinLF("---", "b: 1")},
		},
		// The parser rejects the anchor without the comment, which it takes
		// as the value of the anchor.
		"a comment left of the root below an anchor on a line of its own stays": {
			input: stringtest.LinesLF("  a:", "    &x", "# c", "---", "b: 1"),
			want:  []string{"  a: &x", stringtest.JoinLF("---", "b: 1")},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			r := docparse.Parse(tokens.Tokenize(tc.input))

			got := make([]string, len(r.File.Docs))
			for i, doc := range r.File.Docs {
				got[i] = doc.String()
			}

			assert.Equal(t, tc.want, got)
			assert.Len(t, r.Failures, tc.failures)

			for _, f := range r.Failures {
				require.Error(t, f.Err)
				assert.Nil(t, f.Panic)
				assert.NotEmpty(t, f.Run)
			}
		})
	}
}

func TestParse_EmptyAnchor(t *testing.T) {
	t.Parallel()

	// The parser takes the comment below the anchor as its value.
	r := docparse.Parse(tokens.Tokenize(stringtest.LinesLF("a: &x", "# c")))
	require.Empty(t, r.Failures)

	mapping, ok := r.File.Docs[0].Body.(*ast.MappingNode)
	require.True(t, ok, "got %T", r.File.Docs[0].Body)
	require.Len(t, mapping.Values, 1)

	anchor, ok := mapping.Values[0].Value.(*ast.AnchorNode)
	require.True(t, ok, "got %T", mapping.Values[0].Value)
	assert.IsType(t, &ast.NullNode{}, anchor.Value)
}

func TestParse_Index(t *testing.T) {
	t.Parallel()

	tks := tokens.Tokenize(stringtest.LinesLF("a: 1", "# c", "b: [x, y]"))

	r := docparse.Parse(tks)
	yamltest.RequireTokensEqual(t, tks, r.Tokens)
	require.Len(t, r.Index, len(tks))

	for i, tk := range r.Tokens {
		assert.NotSame(t, tks[i], tk)
		assert.Equal(t, i, r.Index[tk])

		_, ok := r.Index[tks[i]]
		assert.False(t, ok, "token %d of the input", i)
	}
}

func TestParse_Panic(t *testing.T) {
	t.Parallel()

	// The go-yaml parser dereferences the position of this token.
	tks := tokens.Tokenize(stringtest.LinesLF("a: 1", "b: 2"))
	tks[2].Position = nil

	r := docparse.Parse(tks)
	require.Len(t, r.Failures, 1)

	f := r.Failures[0]
	require.NoError(t, f.Err)
	assert.NotNil(t, f.Panic)
	assert.Contains(t, string(f.Stack), "go-yaml/parser")
	assert.Len(t, f.Run, len(tks))

	// The stand-in of the run takes the place of its document.
	require.Len(t, r.File.Docs, 1)
	assert.Nil(t, r.File.Docs[0].Body)
}

func TestFail(t *testing.T) {
	t.Parallel()

	errRefused := errors.New("refused")

	tcs := map[string]struct {
		input string
	}{
		"one document":      {input: stringtest.LinesLF("# c", "a: 1")},
		"several documents": {input: stringtest.LinesLF("a: 1", "---", "b: [", "---", "c: 3")},
		"no text":           {input: ""},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			tks := tokens.Tokenize(tc.input)
			lines := strings.Count(tc.input, "\n") + 1

			r := docparse.Fail(tks, errRefused)

			require.Len(t, r.Failures, 1)
			require.ErrorIs(t, r.Failures[0].Err, errRefused)
			assert.Nil(t, r.Failures[0].Panic)
			assert.Empty(t, r.Tokens)
			assert.Empty(t, r.Index)

			// The one stand-in holds every token and every line of the
			// stream.
			require.Len(t, r.File.Docs, 1)
			assert.Nil(t, r.File.Docs[0].Body)

			docs := r.Documents(tks, lines)
			require.Len(t, docs, 1)

			assert.Same(t, r.File.Docs[0], docs[0].Root)
			assert.Equal(t, 0, docs[0].Failure)
			assert.Equal(t, position.NewSpan(0, lines), docs[0].Span)
			assert.Equal(t, tc.input, yamltest.DumpTokenOrigins(docs[0].Tokens))

			for i, tk := range docs[0].Tokens {
				assert.Same(t, tks[i], tk, "token %d", i)
			}
		})
	}
}

func TestParseLimits(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input  string
		limits docparse.Limits
		// The message of each failure, in file order.
		want []string
		// The line and column of the token each failure binds at.
		at []position.Position
	}{
		"a run past the byte limit": {
			input:  stringtest.LinesLF("a: 1", "---", "# c", "b: "+strings.Repeat("x", 20), "---", "c: 3"),
			limits: docparse.Limits{Bytes: 20, Depth: math.MaxInt},
			want:   []string{"document is larger than 20 bytes"},
			at:     []position.Position{position.New(1, 0)},
		},
		"a run of exactly the byte limit": {
			// The line break before the header ends the last token of the
			// first run, so the second run holds "---\nb: 2\n".
			input:  stringtest.LinesLF("a: 1", "---", "b: 2"),
			limits: docparse.Limits{Bytes: len("---\nb: 2\n"), Depth: math.MaxInt},
		},
		"a run one byte past the byte limit": {
			input:  stringtest.LinesLF("a: 1", "---", "b: 2"),
			limits: docparse.Limits{Bytes: len("---\nb: 2\n") - 1, Depth: math.MaxInt},
			want:   []string{"document is larger than 8 bytes"},
			at:     []position.Position{position.New(1, 0)},
		},
		"the byte limit goes before the depth limit": {
			input:  "a: [[[" + strings.Repeat("x", 20) + "]]]\n",
			limits: docparse.Limits{Bytes: 20, Depth: 2},
			want:   []string{"document is larger than 20 bytes"},
			at:     []position.Position{position.New(0, 0)},
		},
		"a run past the depth limit": {
			input:  "a: [[[1]]]\n",
			limits: docparse.Limits{Bytes: math.MaxInt, Depth: 2},
			want:   []string{"document nests more than 2 levels"},
			at:     []position.Position{position.New(0, 4)},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			r := docparse.ParseLimits(tokens.Tokenize(tc.input), tc.limits)

			require.Len(t, r.Failures, len(tc.want))

			for i, f := range r.Failures {
				assert.Equal(t, tc.want[i], f.Err.Error())

				var tk *token.Token

				{
					var (
						err  *docparse.SizeError
						err1 *docparse.DepthError
					)

					switch {
					case errors.As(f.Err, &err):
						tk = err.Token
					case errors.As(f.Err, &err1):
						tk = err1.Token
					default:
						require.Failf(t, "unexpected failure", "%T", f.Err)
					}
				}

				require.NotNil(t, tk)
				assert.Equal(t, tc.at[i], position.NewFromToken(tk))
			}
		})
	}
}

func TestParseWithoutComments(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input string
		// The keys of the root mapping of the first document.
		want []string
	}{
		"a text without comments parses as Parse parses it": {
			input: stringtest.LinesLF("a: 1", "b: [x, y]"),
			want:  []string{"a", "b"},
		},
		"a comment line after a converting tag before an anchor": {
			// Go-yaml's parser drops the entry after the collection without the
			// comment line, as yaml.Unmarshal reads the text.
			input: stringtest.LinesLF("p: !!str &k [1]", "# l", "q: 1"),
			want:  []string{"p"},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			r := docparse.ParseWithoutComments(tokens.Tokenize(tc.input))
			require.Empty(t, r.Failures)

			mapping, ok := r.File.Docs[0].Body.(*ast.MappingNode)
			require.True(t, ok, "got %T", r.File.Docs[0].Body)

			got := make([]string, len(mapping.Values))
			for i, e := range mapping.Values {
				got[i] = e.Key.String()
			}

			assert.Equal(t, tc.want, got)
		})
	}
}
