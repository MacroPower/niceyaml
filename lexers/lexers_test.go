package lexers_test

import (
	"iter"
	"testing"

	"github.com/goccy/go-yaml/lexer"
	"github.com/goccy/go-yaml/token"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/lexers"
)

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
			input: yamltest.JoinLF(
				"key1: value1",
				"key2: value2",
				"key3: value3",
			),
		},
		"nested structure": {
			input: yamltest.JoinLF(
				"parent:",
				"  child1: value1",
				"  child2: value2",
			),
		},
		"unicode content": {
			input: "greeting: こんにちは\n",
		},
		"list": {
			input: yamltest.JoinLF(
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

		input := yamltest.JoinLF(
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

		input := yamltest.JoinLF(
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

		input := yamltest.JoinLF(
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

		input := yamltest.JoinLF(
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

		input := yamltest.JoinLF(
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

		input := yamltest.JoinLF(
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

		input := yamltest.JoinLF(
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
