package lexers_test

import (
	"fmt"
	"iter"
	"strings"
	"testing"

	"github.com/goccy/go-yaml/lexer"
	"github.com/goccy/go-yaml/token"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.jacobcolvin.com/x/stringtest"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/lexers"
	"go.jacobcolvin.com/niceyaml/tokens"
)

// describeLines renders each line as its number and content followed by the
// line, column, and offset of every token on it.
func describeLines(lines niceyaml.Lines) []string {
	out := make([]string, 0, len(lines))

	for i := range lines {
		var sb strings.Builder

		fmt.Fprintf(&sb, "%d %q", lines[i].Number(), lines[i].Content())

		for _, tk := range lines[i].Tokens() {
			fmt.Fprintf(&sb, " %d:%d@%d", tk.Position.Line, tk.Position.Column, tk.Position.Offset)
		}

		out = append(out, sb.String())
	}

	return out
}

func collectDocs(seq iter.Seq2[int, token.Tokens]) []token.Tokens {
	result := []token.Tokens{}

	for _, tks := range seq {
		result = append(result, tks)
	}

	return result
}

func collectDocsWithIndices(seq iter.Seq2[int, token.Tokens]) ([]int, []token.Tokens) {
	indices := []int{}
	docs := []token.Tokens{}

	for idx, tks := range seq {
		indices = append(indices, idx)
		docs = append(docs, tks)
	}

	return indices, docs
}

func TestTokenize(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input string
	}{
		"empty string": {
			input: "",
		},
		"simple key value": {
			input: "key: value\n",
		},
		"multi-line": {
			input: stringtest.JoinLF(
				"key1: value1",
				"key2: value2",
				"key3: value3",
			),
		},
		"nested structure": {
			input: stringtest.JoinLF(
				"parent:",
				"  child1: value1",
				"  child2: value2",
			),
		},
		"unicode content": {
			input: "greeting: こんにちは\n",
		},
		"list": {
			input: stringtest.JoinLF(
				"items:",
				"  - one",
				"  - two",
				"  - three",
			),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			want := lexer.Tokenize(tc.input)
			got := lexers.Tokenize(tc.input)

			diff := yamltest.CompareTokenSlices(want, got)
			require.True(t, diff.Equal(), diff.String())
		})
	}
}

func TestTokenizeDocuments(t *testing.T) {
	t.Parallel()

	t.Run("empty string", func(t *testing.T) {
		t.Parallel()

		got := collectDocs(lexers.TokenizeDocuments(""))

		assert.Empty(t, got)
	})

	t.Run("single doc no header", func(t *testing.T) {
		t.Parallel()

		input := "key: value\n"

		indices, docs := collectDocsWithIndices(lexers.TokenizeDocuments(input))

		require.Len(t, docs, 1)
		require.Equal(t, []int{0}, indices)
		require.NotEmpty(t, docs[0])

		// Verify tokens match what lexer.Tokenize produces.
		diff := yamltest.CompareTokenSlices(lexer.Tokenize(input), docs[0])
		require.True(t, diff.Equal(), diff.String())
	})

	t.Run("single doc with header", func(t *testing.T) {
		t.Parallel()

		input := stringtest.JoinLF(
			"---",
			"key: value",
		)

		indices, docs := collectDocsWithIndices(lexers.TokenizeDocuments(input))

		require.Len(t, docs, 1)
		require.Equal(t, []int{0}, indices)
		require.NotEmpty(t, docs[0])
		assert.Equal(t, token.DocumentHeaderType, docs[0][0].Type)
	})

	t.Run("two docs", func(t *testing.T) {
		t.Parallel()

		input := stringtest.JoinLF(
			"key1: v1",
			"---",
			"key2: v2",
		)

		indices, docs := collectDocsWithIndices(lexers.TokenizeDocuments(input))

		require.Len(t, docs, 2)
		require.Equal(t, []int{0, 1}, indices)

		// First doc should not start with header.
		require.NotEmpty(t, docs[0])
		assert.NotEqual(t, token.DocumentHeaderType, docs[0][0].Type)

		// Second doc should start with header.
		require.NotEmpty(t, docs[1])
		assert.Equal(t, token.DocumentHeaderType, docs[1][0].Type)
	})

	t.Run("three docs", func(t *testing.T) {
		t.Parallel()

		input := stringtest.JoinLF(
			"a: 1",
			"---",
			"b: 2",
			"---",
			"c: 3",
		)

		indices, docs := collectDocsWithIndices(lexers.TokenizeDocuments(input))

		require.Len(t, docs, 3)
		require.Equal(t, []int{0, 1, 2}, indices)

		// Second and third docs should start with header.
		assert.Equal(t, token.DocumentHeaderType, docs[1][0].Type)
		assert.Equal(t, token.DocumentHeaderType, docs[2][0].Type)
	})

	t.Run("header at start creates single doc", func(t *testing.T) {
		t.Parallel()

		input := stringtest.JoinLF(
			"---",
			"key: value",
		)

		docs := collectDocs(lexers.TokenizeDocuments(input))

		// Header at start should still be one document.
		require.Len(t, docs, 1)
		assert.Equal(t, token.DocumentHeaderType, docs[0][0].Type)
	})

	t.Run("multiple headers at start", func(t *testing.T) {
		t.Parallel()

		input := stringtest.JoinLF(
			"---",
			"doc1: value1",
			"---",
			"doc2: value2",
		)

		indices, docs := collectDocsWithIndices(lexers.TokenizeDocuments(input))

		require.Len(t, docs, 2)
		require.Equal(t, []int{0, 1}, indices)

		// Both docs should start with header.
		assert.Equal(t, token.DocumentHeaderType, docs[0][0].Type)
		assert.Equal(t, token.DocumentHeaderType, docs[1][0].Type)
	})

	t.Run("doc with end marker", func(t *testing.T) {
		t.Parallel()

		input := stringtest.JoinLF(
			"key: value",
			"...",
		)

		docs := collectDocs(lexers.TokenizeDocuments(input))

		require.Len(t, docs, 1)

		// Should contain the end marker token.
		hasEndMarker := false

		for _, tk := range docs[0] {
			if tk.Type == token.DocumentEndType {
				hasEndMarker = true

				break
			}
		}

		assert.True(t, hasEndMarker, "expected document to contain end marker")
	})

	t.Run("early termination", func(t *testing.T) {
		t.Parallel()

		input := stringtest.JoinLF(
			"doc1: v1",
			"---",
			"doc2: v2",
			"---",
			"doc3: v3",
		)

		// Only collect first document to test early termination.
		var firstDoc token.Tokens

		var firstIdx int

		for idx, tks := range lexers.TokenizeDocuments(input) {
			firstIdx = idx
			firstDoc = tks

			break
		}

		assert.Equal(t, 0, firstIdx)
		require.NotEmpty(t, firstDoc)
	})
}

func TestTokenizeDocuments_WithResetPositions(t *testing.T) {
	t.Parallel()

	t.Run("single doc keeps its leading blank lines", func(t *testing.T) {
		t.Parallel()

		// The blank lines sit in the first token's Origin, so the token stays
		// below them, where a fresh tokenize places it.
		input := stringtest.JoinLF(
			"",
			"",
			"key: value",
		)

		docs := collectDocs(lexers.TokenizeDocuments(input, lexers.WithResetPositions()))

		require.Len(t, docs, 1)
		require.NotEmpty(t, docs[0])

		assert.Equal(t, 3, docs[0][0].Position.Line)
		assert.Equal(t, 1, docs[0][0].Position.Column)
		assert.Equal(t, 3, docs[0][0].Position.Offset)
	})

	t.Run("documents build the lines of a fresh tokenize", func(t *testing.T) {
		t.Parallel()

		tcs := map[string]struct {
			input string
		}{
			"after document end":         {input: "a: 1\n...\nb: 2\n"},
			"comment after document end": {input: "a: 1\n...\n# c\nb: 2\n"},
			"header after directive":     {input: "%YAML 1.2\n---\na: 1\n"},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				for i, doc := range lexers.TokenizeDocuments(tc.input, lexers.WithResetPositions()) {
					got := niceyaml.NewLines(doc)
					want := niceyaml.NewLines(lexers.Tokenize(yamltest.DumpTokenOrigins(doc)))

					require.NoError(t, got.Validate(), "document %d", i)
					assert.Equal(t, describeLines(want), describeLines(got), "document %d", i)
				}
			})
		}
	})

	t.Run("multi doc each starts at line 1", func(t *testing.T) {
		t.Parallel()

		input := stringtest.JoinLF(
			"key1: v1",
			"---",
			"key2: v2",
		)

		docs := collectDocs(lexers.TokenizeDocuments(input, lexers.WithResetPositions()))

		require.Len(t, docs, 2)

		// First doc should start at line 1.
		require.NotEmpty(t, docs[0])
		assert.Equal(t, 1, docs[0][0].Position.Line)
		assert.Equal(t, 1, docs[0][0].Position.Column)
		assert.Equal(t, 1, docs[0][0].Position.Offset)

		// Second doc should also start at line 1 (header token).
		require.NotEmpty(t, docs[1])
		assert.Equal(t, 1, docs[1][0].Position.Line)
		assert.Equal(t, 1, docs[1][0].Position.Column)
		assert.Equal(t, 1, docs[1][0].Position.Offset)
	})

	t.Run("preserves original positions when option not used", func(t *testing.T) {
		t.Parallel()

		input := stringtest.JoinLF(
			"key1: v1",
			"---",
			"key2: v2",
		)

		docs := collectDocs(lexers.TokenizeDocuments(input))

		require.Len(t, docs, 2)

		// First doc should be at line 1.
		require.NotEmpty(t, docs[0])
		assert.Equal(t, 1, docs[0][0].Position.Line)

		// Second doc header should be at line 2 (original position).
		require.NotEmpty(t, docs[1])
		assert.Equal(t, 2, docs[1][0].Position.Line)
	})

	t.Run("clones tokens when reset", func(t *testing.T) {
		t.Parallel()

		input := "key: value\n"

		// Get original tokens for comparison.
		original := lexer.Tokenize(input)
		require.NotEmpty(t, original)

		originalLine := original[0].Position.Line

		docs := collectDocs(lexers.TokenizeDocuments(input, lexers.WithResetPositions()))

		require.Len(t, docs, 1)
		require.NotEmpty(t, docs[0])

		// Should be different pointers (cloned).
		assert.NotSame(t, original[0], docs[0][0])

		// Original should be unchanged.
		assert.Equal(t, originalLine, original[0].Position.Line)
	})

	t.Run("three docs all reset independently", func(t *testing.T) {
		t.Parallel()

		input := stringtest.JoinLF(
			"a: 1",
			"---",
			"b: 2",
			"---",
			"c: 3",
		)

		docs := collectDocs(lexers.TokenizeDocuments(input, lexers.WithResetPositions()))

		require.Len(t, docs, 3)

		// Each doc should start at line 1.
		for i, doc := range docs {
			require.NotEmpty(t, doc, "doc %d should not be empty", i)
			assert.Equal(t, 1, doc[0].Position.Line, "doc %d should start at line 1", i)
			assert.Equal(t, 1, doc[0].Position.Offset, "doc %d should start at offset 1", i)
		}
	})

	t.Run("matches SplitDocuments behavior", func(t *testing.T) {
		t.Parallel()

		input := stringtest.JoinLF(
			"key1: v1",
			"---",
			"key2: v2",
		)

		// Get results from TokenizeDocuments.
		tokenizeDocs := collectDocs(lexers.TokenizeDocuments(input, lexers.WithResetPositions()))

		// Get results from SplitDocuments with same option.
		allTokens := lexer.Tokenize(input)
		splitDocs := []token.Tokens{}

		for _, tks := range tokens.SplitDocuments(allTokens, tokens.WithResetPositions()) {
			splitDocs = append(splitDocs, tks)
		}

		// Both should produce the same number of documents.
		require.Len(t, tokenizeDocs, len(splitDocs))

		// First tokens of each doc should have the same positions.
		for i := range tokenizeDocs {
			require.NotEmpty(t, tokenizeDocs[i])
			require.NotEmpty(t, splitDocs[i])
			assert.Equal(t, splitDocs[i][0].Position.Line, tokenizeDocs[i][0].Position.Line,
				"doc %d line mismatch", i)
			assert.Equal(t, splitDocs[i][0].Position.Column, tokenizeDocs[i][0].Position.Column,
				"doc %d column mismatch", i)
			assert.Equal(t, splitDocs[i][0].Position.Offset, tokenizeDocs[i][0].Position.Offset,
				"doc %d offset mismatch", i)
		}
	})

	t.Run("empty input", func(t *testing.T) {
		t.Parallel()

		docs := collectDocs(lexers.TokenizeDocuments("", lexers.WithResetPositions()))

		assert.Empty(t, docs)
	})
}
