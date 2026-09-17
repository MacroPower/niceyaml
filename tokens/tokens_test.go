package tokens_test

import (
	"iter"
	"strings"
	"testing"

	"github.com/goccy/go-yaml/lexer"
	"github.com/goccy/go-yaml/token"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.jacobcolvin.com/x/stringtest"

	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/tokens"
)

func collectDocs(seq iter.Seq2[int, token.Tokens]) []token.Tokens {
	var result []token.Tokens

	for _, tks := range seq {
		result = append(result, tks)
	}

	return result
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
		"trailing blank line": {
			input: "key: value\n\n",
		},
		"crlf trailing blank line": {
			input: "key: value\r\n\r\n",
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
			got := tokens.Tokenize(tc.input)

			// The stream covers the whole source, including the final
			// line ending the lexer leaves out of the last Origin.
			var joined strings.Builder

			for _, tk := range got {
				joined.WriteString(tk.Origin)
			}

			assert.Equal(t, tc.input, joined.String())

			if len(want) > 0 {
				last := len(want) - 1
				assert.True(t, strings.HasPrefix(got[last].Origin, want[last].Origin))

				want[last].Origin = got[last].Origin
			}

			diff := yamltest.CompareTokenSlices(want, got)
			require.True(t, diff.Equal(), diff.String())
		})
	}
}

func TestSplitDocuments(t *testing.T) {
	t.Parallel()

	t.Run("nil input", func(t *testing.T) {
		t.Parallel()

		got := collectDocs(tokens.SplitDocuments(nil))

		assert.Empty(t, got)
	})

	t.Run("empty slice", func(t *testing.T) {
		t.Parallel()

		got := collectDocs(tokens.SplitDocuments(token.Tokens{}))

		assert.Empty(t, got)
	})

	t.Run("single doc no header", func(t *testing.T) {
		t.Parallel()

		tkb := yamltest.NewTokenBuilder()
		input := token.Tokens{
			tkb.Clone().Type(token.StringType).Value("key").Build(),
			tkb.Clone().Type(token.MappingValueType).Value(":").Build(),
			tkb.Clone().Type(token.StringType).Value("value").Build(),
		}

		got := collectDocs(tokens.SplitDocuments(input))

		require.Len(t, got, 1)
		require.Len(t, got[0], 3)

		diff := yamltest.CompareTokenSlices(input, got[0])
		require.True(t, diff.Equal(), diff.String())
	})

	t.Run("nil tokens are skipped", func(t *testing.T) {
		t.Parallel()

		// Positions start at 1:1 so a reset leaves them unchanged.
		tkb := yamltest.NewTokenBuilder().PositionLine(1).PositionColumn(1)
		want := token.Tokens{
			tkb.Clone().Type(token.StringType).Value("key").Origin("key").PositionOffset(1).Build(),
			tkb.Clone().Type(token.MappingValueType).Value(":").Origin(":").PositionOffset(4).Build(),
			tkb.Clone().Type(token.StringType).Value("value").Origin(" value").PositionOffset(6).Build(),
		}
		input := token.Tokens{nil, want[0], want[1], nil, want[2], nil}

		for name, opts := range map[string][]tokens.SplitDocumentsOption{
			"shared":   nil,
			"reset":    {tokens.WithResetPositions(true)},
			"no reset": {tokens.WithResetPositions(false)},
		} {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				got := collectDocs(tokens.SplitDocuments(input, opts...))

				require.Len(t, got, 1)

				diff := yamltest.CompareTokenSlices(want, got[0])
				require.True(t, diff.Equal(), diff.String())
			})
		}
	})

	t.Run("single doc with header", func(t *testing.T) {
		t.Parallel()

		tkb := yamltest.NewTokenBuilder()
		input := token.Tokens{
			tkb.Clone().Type(token.DocumentHeaderType).Value("---").Build(),
			tkb.Clone().Type(token.StringType).Value("key").Build(),
			tkb.Clone().Type(token.MappingValueType).Value(":").Build(),
			tkb.Clone().Type(token.StringType).Value("value").Build(),
		}

		got := collectDocs(tokens.SplitDocuments(input))

		require.Len(t, got, 1)
		require.Len(t, got[0], 4)

		diff := yamltest.CompareTokenSlices(input, got[0])
		require.True(t, diff.Equal(), diff.String())
	})

	t.Run("end marker closes a document", func(t *testing.T) {
		t.Parallel()

		tkb := yamltest.NewTokenBuilder()
		input := token.Tokens{
			tkb.Clone().Type(token.StringType).Value("a").Build(),
			tkb.Clone().Type(token.MappingValueType).Value(":").Build(),
			tkb.Clone().Type(token.StringType).Value("1").Build(),
			tkb.Clone().Type(token.DocumentEndType).Value("...").Build(),
			tkb.Clone().Type(token.StringType).Value("b").Build(),
			tkb.Clone().Type(token.MappingValueType).Value(":").Build(),
			tkb.Clone().Type(token.StringType).Value("2").Build(),
		}

		got := collectDocs(tokens.SplitDocuments(input))

		require.Len(t, got, 2)
		require.Len(t, got[0], 4)
		require.Len(t, got[1], 3)

		diff := yamltest.CompareTokenSlices(input[:4], got[0])
		require.True(t, diff.Equal(), diff.String())

		diff = yamltest.CompareTokenSlices(input[4:], got[1])
		require.True(t, diff.Equal(), diff.String())
	})

	t.Run("end marker followed by header", func(t *testing.T) {
		t.Parallel()

		tkb := yamltest.NewTokenBuilder()
		input := token.Tokens{
			tkb.Clone().Type(token.StringType).Value("a").Build(),
			tkb.Clone().Type(token.DocumentEndType).Value("...").Build(),
			tkb.Clone().Type(token.DocumentHeaderType).Value("---").Build(),
			tkb.Clone().Type(token.StringType).Value("b").Build(),
		}

		got := collectDocs(tokens.SplitDocuments(input))

		require.Len(t, got, 2)
		require.Len(t, got[0], 2)
		require.Len(t, got[1], 2)
		assert.Equal(t, token.DocumentHeaderType, got[1][0].Type)
	})

	t.Run("two docs", func(t *testing.T) {
		t.Parallel()

		tkb := yamltest.NewTokenBuilder()
		key1 := tkb.Clone().Type(token.StringType).Value("key1").Build()
		colon1 := tkb.Clone().Type(token.MappingValueType).Value(":").Build()
		value1 := tkb.Clone().Type(token.StringType).Value("v1").Build()
		header := tkb.Clone().Type(token.DocumentHeaderType).Value("---").Build()
		key2 := tkb.Clone().Type(token.StringType).Value("key2").Build()
		colon2 := tkb.Clone().Type(token.MappingValueType).Value(":").Build()
		value2 := tkb.Clone().Type(token.StringType).Value("v2").Build()

		input := token.Tokens{key1, colon1, value1, header, key2, colon2, value2}

		got := collectDocs(tokens.SplitDocuments(input))

		require.Len(t, got, 2)
		require.Len(t, got[0], 3)
		require.Len(t, got[1], 4)

		diff0 := yamltest.CompareTokenSlices(token.Tokens{key1, colon1, value1}, got[0])
		require.True(t, diff0.Equal(), diff0.String())

		diff1 := yamltest.CompareTokenSlices(token.Tokens{header, key2, colon2, value2}, got[1])
		require.True(t, diff1.Equal(), diff1.String())
	})

	t.Run("three docs with headers", func(t *testing.T) {
		t.Parallel()

		tkb := yamltest.NewTokenBuilder()
		header1 := tkb.Clone().Type(token.DocumentHeaderType).Value("---").Build()
		doc1 := tkb.Clone().Type(token.StringType).Value("doc1").Build()
		header2 := tkb.Clone().Type(token.DocumentHeaderType).Value("---").Build()
		doc2 := tkb.Clone().Type(token.StringType).Value("doc2").Build()
		header3 := tkb.Clone().Type(token.DocumentHeaderType).Value("---").Build()
		doc3 := tkb.Clone().Type(token.StringType).Value("doc3").Build()

		input := token.Tokens{header1, doc1, header2, doc2, header3, doc3}

		got := collectDocs(tokens.SplitDocuments(input))

		require.Len(t, got, 3)

		diff0 := yamltest.CompareTokenSlices(token.Tokens{header1, doc1}, got[0])
		require.True(t, diff0.Equal(), diff0.String())

		diff1 := yamltest.CompareTokenSlices(token.Tokens{header2, doc2}, got[1])
		require.True(t, diff1.Equal(), diff1.String())

		diff2 := yamltest.CompareTokenSlices(token.Tokens{header3, doc3}, got[2])
		require.True(t, diff2.Equal(), diff2.String())
	})

	t.Run("doc with end marker", func(t *testing.T) {
		t.Parallel()

		tkb := yamltest.NewTokenBuilder()
		input := token.Tokens{
			tkb.Clone().Type(token.StringType).Value("key").Build(),
			tkb.Clone().Type(token.MappingValueType).Value(":").Build(),
			tkb.Clone().Type(token.StringType).Value("value").Build(),
			tkb.Clone().Type(token.DocumentEndType).Value("...").Build(),
		}

		got := collectDocs(tokens.SplitDocuments(input))

		require.Len(t, got, 1)
		require.Len(t, got[0], 4)

		diff := yamltest.CompareTokenSlices(input, got[0])
		require.True(t, diff.Equal(), diff.String())
	})

	t.Run("doc end followed by new doc", func(t *testing.T) {
		t.Parallel()

		tkb := yamltest.NewTokenBuilder()
		doc1 := tkb.Clone().Type(token.StringType).Value("doc1").Build()
		docEnd := tkb.Clone().Type(token.DocumentEndType).Value("...").Build()
		header := tkb.Clone().Type(token.DocumentHeaderType).Value("---").Build()
		doc2 := tkb.Clone().Type(token.StringType).Value("doc2").Build()

		input := token.Tokens{doc1, docEnd, header, doc2}

		got := collectDocs(tokens.SplitDocuments(input))

		require.Len(t, got, 2)

		diff0 := yamltest.CompareTokenSlices(token.Tokens{doc1, docEnd}, got[0])
		require.True(t, diff0.Equal(), diff0.String())

		diff1 := yamltest.CompareTokenSlices(token.Tokens{header, doc2}, got[1])
		require.True(t, diff1.Equal(), diff1.String())
	})

	t.Run("early termination at first doc", func(t *testing.T) {
		t.Parallel()

		tkb := yamltest.NewTokenBuilder()
		doc1 := tkb.Clone().Type(token.StringType).Value("doc1").Build()
		header := tkb.Clone().Type(token.DocumentHeaderType).Value("---").Build()
		doc2 := tkb.Clone().Type(token.StringType).Value("doc2").Build()

		input := token.Tokens{doc1, header, doc2}

		var got []token.Tokens

		for _, tks := range tokens.SplitDocuments(input) {
			got = append(got, tks)

			break // Early termination after first document.
		}

		require.Len(t, got, 1)

		diff := yamltest.CompareTokenSlices(token.Tokens{doc1}, got[0])
		require.True(t, diff.Equal(), diff.String())
	})

	t.Run("early termination at second doc", func(t *testing.T) {
		t.Parallel()

		tkb := yamltest.NewTokenBuilder()
		doc1 := tkb.Clone().Type(token.StringType).Value("doc1").Build()
		header1 := tkb.Clone().Type(token.DocumentHeaderType).Value("---").Build()
		doc2 := tkb.Clone().Type(token.StringType).Value("doc2").Build()
		header2 := tkb.Clone().Type(token.DocumentHeaderType).Value("---").Build()
		doc3 := tkb.Clone().Type(token.StringType).Value("doc3").Build()

		input := token.Tokens{doc1, header1, doc2, header2, doc3}

		var got []token.Tokens

		count := 0
		for _, tks := range tokens.SplitDocuments(input) {
			got = append(got, tks)
			count++
			if count == 2 {
				break // Early termination after second document.
			}
		}

		require.Len(t, got, 2)

		diff0 := yamltest.CompareTokenSlices(token.Tokens{doc1}, got[0])
		require.True(t, diff0.Equal(), diff0.String())

		diff1 := yamltest.CompareTokenSlices(token.Tokens{header1, doc2}, got[1])
		require.True(t, diff1.Equal(), diff1.String())
	})

	t.Run("early termination single doc", func(t *testing.T) {
		t.Parallel()

		tkb := yamltest.NewTokenBuilder()
		input := token.Tokens{
			tkb.Clone().Type(token.StringType).Value("only").Build(),
		}

		var got []token.Tokens

		for _, tks := range tokens.SplitDocuments(input) {
			got = append(got, tks)

			break // Early termination on single document.
		}

		require.Len(t, got, 1)
		require.Len(t, got[0], 1)
	})
}

func TestSplitDocuments_WithResetPositions(t *testing.T) {
	t.Parallel()

	t.Run("single doc resets to line 1", func(t *testing.T) {
		t.Parallel()

		tkb := yamltest.NewTokenBuilder()
		input := token.Tokens{
			tkb.Clone().Type(token.StringType).Value("key").
				PositionLine(5).PositionColumn(3).PositionOffset(100).Build(),
			tkb.Clone().Type(token.MappingValueType).Value(":").
				PositionLine(5).PositionColumn(6).PositionOffset(103).Build(),
			tkb.Clone().Type(token.StringType).Value("value").
				PositionLine(5).PositionColumn(8).PositionOffset(105).Build(),
		}

		got := collectDocs(tokens.SplitDocuments(input, tokens.WithResetPositions(true)))

		require.Len(t, got, 1)
		require.Len(t, got[0], 3)

		// All tokens should be on line 1 now.
		assert.Equal(t, 1, got[0][0].Position.Line)
		assert.Equal(t, 1, got[0][0].Position.Column)
		assert.Equal(t, 1, got[0][0].Position.Offset)

		assert.Equal(t, 1, got[0][1].Position.Line)
		assert.Equal(t, 4, got[0][1].Position.Column)
		assert.Equal(t, 4, got[0][1].Position.Offset)

		assert.Equal(t, 1, got[0][2].Position.Line)
		assert.Equal(t, 6, got[0][2].Position.Column)
		assert.Equal(t, 6, got[0][2].Position.Offset)
	})

	t.Run("multi doc each starts at line 1", func(t *testing.T) {
		t.Parallel()

		tkb := yamltest.NewTokenBuilder()
		// First document at line 1.
		doc1Key := tkb.Clone().Type(token.StringType).Value("key1").
			PositionLine(1).PositionColumn(1).PositionOffset(0).Build()
		// Second document starts at line 3.
		header := tkb.Clone().Type(token.DocumentHeaderType).Value("---").
			PositionLine(3).PositionColumn(1).PositionOffset(15).Build()
		doc2Key := tkb.Clone().Type(token.StringType).Value("key2").
			PositionLine(4).PositionColumn(1).PositionOffset(20).Build()

		input := token.Tokens{doc1Key, header, doc2Key}

		got := collectDocs(tokens.SplitDocuments(input, tokens.WithResetPositions(true)))

		require.Len(t, got, 2)

		// First document should start at line 1.
		require.Len(t, got[0], 1)
		assert.Equal(t, 1, got[0][0].Position.Line)
		assert.Equal(t, 1, got[0][0].Position.Column)
		assert.Equal(t, 1, got[0][0].Position.Offset)

		// Second document should also start at line 1.
		require.Len(t, got[1], 2)
		assert.Equal(t, 1, got[1][0].Position.Line) // Header.
		assert.Equal(t, 1, got[1][0].Position.Column)
		assert.Equal(t, 1, got[1][0].Position.Offset)

		assert.Equal(t, 2, got[1][1].Position.Line) // Key on next line.
		assert.Equal(t, 1, got[1][1].Position.Column)
		assert.Equal(t, 6, got[1][1].Position.Offset)
	})

	t.Run("a later false keeps the original tokens", func(t *testing.T) {
		t.Parallel()

		input := lexer.Tokenize("key: value\n")

		docs := collectDocs(tokens.SplitDocuments(input,
			tokens.WithResetPositions(true),
			tokens.WithResetPositions(false),
		))

		require.Len(t, docs, 1)
		require.Len(t, docs[0], len(input))

		for i, tk := range docs[0] {
			assert.Same(t, input[i], tk)
		}
	})

	t.Run("preserves original tokens when option not used", func(t *testing.T) {
		t.Parallel()

		tkb := yamltest.NewTokenBuilder()
		input := token.Tokens{
			tkb.Clone().Type(token.StringType).Value("key").
				PositionLine(5).PositionColumn(3).PositionOffset(100).Build(),
		}

		got := collectDocs(tokens.SplitDocuments(input))

		require.Len(t, got, 1)
		require.Len(t, got[0], 1)

		// Position should be unchanged.
		assert.Equal(t, 5, got[0][0].Position.Line)
		assert.Equal(t, 3, got[0][0].Position.Column)
		assert.Equal(t, 100, got[0][0].Position.Offset)

		// Should be the same pointer (not cloned).
		assert.Same(t, input[0], got[0][0])
	})

	t.Run("clones tokens when reset", func(t *testing.T) {
		t.Parallel()

		tkb := yamltest.NewTokenBuilder()
		original := tkb.Clone().Type(token.StringType).Value("key").
			PositionLine(5).PositionColumn(3).PositionOffset(100).Build()
		input := token.Tokens{original}

		got := collectDocs(tokens.SplitDocuments(input, tokens.WithResetPositions(true)))

		require.Len(t, got, 1)
		require.Len(t, got[0], 1)

		// Should be a different pointer (cloned).
		assert.NotSame(t, original, got[0][0])

		// Original should be unchanged.
		assert.Equal(t, 5, original.Position.Line)
		assert.Equal(t, 3, original.Position.Column)
		assert.Equal(t, 100, original.Position.Offset)
	})

	t.Run("handles multiline tokens within doc", func(t *testing.T) {
		t.Parallel()

		tkb := yamltest.NewTokenBuilder()
		// A multiline block scalar starting at line 10.
		blockIndicator := tkb.Clone().Type(token.LiteralType).Value("|").
			PositionLine(10).PositionColumn(5).PositionOffset(50).Build()
		blockContent := tkb.Clone().Type(token.StringType).Value("line1\nline2").
			PositionLine(11).PositionColumn(5).PositionOffset(52).Build()

		input := token.Tokens{blockIndicator, blockContent}

		got := collectDocs(tokens.SplitDocuments(input, tokens.WithResetPositions(true)))

		require.Len(t, got, 1)
		require.Len(t, got[0], 2)

		// Block indicator should be at line 1.
		assert.Equal(t, 1, got[0][0].Position.Line)
		assert.Equal(t, 1, got[0][0].Position.Column)
		assert.Equal(t, 1, got[0][0].Position.Offset)

		// Content should be at line 2 (relative to doc start).
		assert.Equal(t, 2, got[0][1].Position.Line)
		assert.Equal(t, 5, got[0][1].Position.Column)
		assert.Equal(t, 3, got[0][1].Position.Offset)
	})

	t.Run("handles nil position", func(t *testing.T) {
		t.Parallel()

		// Token with nil position should not cause panic.
		input := token.Tokens{
			&token.Token{Type: token.StringType, Value: "test", Position: nil},
		}

		got := collectDocs(tokens.SplitDocuments(input, tokens.WithResetPositions(true)))

		require.Len(t, got, 1)
		require.Len(t, got[0], 1)
		assert.Nil(t, got[0][0].Position)
	})

	t.Run("handles empty input", func(t *testing.T) {
		t.Parallel()

		got := collectDocs(tokens.SplitDocuments(nil, tokens.WithResetPositions(true)))

		assert.Empty(t, got)
	})

	t.Run("three docs all reset independently", func(t *testing.T) {
		t.Parallel()

		tkb := yamltest.NewTokenBuilder()
		// Doc 1 at line 1.
		doc1 := tkb.Clone().Type(token.StringType).Value("doc1").
			PositionLine(1).PositionColumn(1).PositionOffset(0).Build()
		// Doc 2 at line 5.
		header2 := tkb.Clone().Type(token.DocumentHeaderType).Value("---").
			PositionLine(5).PositionColumn(1).PositionOffset(20).Build()
		doc2 := tkb.Clone().Type(token.StringType).Value("doc2").
			PositionLine(6).PositionColumn(1).PositionOffset(25).Build()
		// Doc 3 at line 10.
		header3 := tkb.Clone().Type(token.DocumentHeaderType).Value("---").
			PositionLine(10).PositionColumn(1).PositionOffset(40).Build()
		doc3 := tkb.Clone().Type(token.StringType).Value("doc3").
			PositionLine(11).PositionColumn(1).PositionOffset(45).Build()

		input := token.Tokens{doc1, header2, doc2, header3, doc3}

		got := collectDocs(tokens.SplitDocuments(input, tokens.WithResetPositions(true)))

		require.Len(t, got, 3)

		// Doc 1 starts at line 1.
		assert.Equal(t, 1, got[0][0].Position.Line)

		// Doc 2 starts at line 1 (header) then line 2 (content).
		assert.Equal(t, 1, got[1][0].Position.Line)
		assert.Equal(t, 2, got[1][1].Position.Line)

		// Doc 3 starts at line 1 (header) then line 2 (content).
		assert.Equal(t, 1, got[2][0].Position.Line)
		assert.Equal(t, 2, got[2][1].Position.Line)
	})
}

// resetOne splits tks as a single document with reset positions.
func resetOne(tks token.Tokens) token.Tokens {
	for _, doc := range tokens.SplitDocuments(tks, tokens.WithResetPositions(true)) {
		return doc
	}

	return nil
}

func TestSplitDocuments_ResetPositions(t *testing.T) {
	t.Parallel()

	t.Run("matches a fresh tokenize of the same text", func(t *testing.T) {
		t.Parallel()

		tcs := map[string]struct {
			input string
		}{
			"after document header":        {input: "a: 1\n---\nb: 2\n"},
			"after document end":           {input: "a: 1\n...\nb: 2\n"},
			"comment after document end":   {input: "a: 1\n...\n# c\nb: 2\n"},
			"comment on document end line": {input: "a: 1\n... # c\nb: 2\n"},
			"indented after document end":  {input: "a: 1\n...\n  b: 2\n"},
			"header after document end":    {input: "a: 1\n...\n---\nb: 2\n"},
			"header after directive":       {input: "%YAML 1.2\n---\na: 1\n"},
			"leading blank lines":          {input: "\n\na: 1\n"},
			"crlf after document end":      {input: "a: 1\r\n...\r\nb: 2\r\n"},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				for i, doc := range tokens.SplitDocuments(lexer.Tokenize(tc.input), tokens.WithResetPositions(true)) {
					// A document's text is the Origins of its tokens.
					fresh := lexer.Tokenize(yamltest.DumpTokenOrigins(doc))
					require.Len(t, doc, len(fresh), "document %d", i)

					for j, want := range fresh {
						got := doc[j].Position
						assert.Equal(t, want.Position.Line, got.Line, "document %d token %d line", i, j)
						assert.Equal(t, want.Position.Column, got.Column, "document %d token %d column", i, j)
						assert.Equal(t, want.Position.Offset, got.Offset, "document %d token %d offset", i, j)
					}
				}
			})
		}
	})

	t.Run("severs links to neighboring documents", func(t *testing.T) {
		t.Parallel()

		orig := lexer.Tokenize("a: 1\n...\nb: 2\n")
		docs := collectDocs(tokens.SplitDocuments(orig, tokens.WithResetPositions(true)))
		require.Len(t, docs, 2)

		first, second := docs[0], docs[1]

		assert.Nil(t, first[len(first)-1].Next, "the last clone of a document has no Next")
		assert.Nil(t, second[0].Prev, "the first clone of a document has no Prev")
		assert.Nil(t, first[0].Prev)
		assert.Nil(t, second[len(second)-1].Next)

		// The clones still link to each other inside a document.
		assert.Same(t, first[1], first[0].Next)
		assert.Same(t, first[0], first[1].Prev)

		// The originals keep their links across the boundary.
		assert.Same(t, orig[4], orig[3].Next)
		assert.Same(t, orig[3], orig[4].Prev)
	})

	t.Run("resets positions to line 1 column 1", func(t *testing.T) {
		t.Parallel()

		tkb := yamltest.NewTokenBuilder()
		input := token.Tokens{
			tkb.Clone().Type(token.StringType).Value("key").
				PositionLine(5).PositionColumn(3).PositionOffset(100).Build(),
			tkb.Clone().Type(token.MappingValueType).Value(":").
				PositionLine(5).PositionColumn(6).PositionOffset(103).Build(),
			tkb.Clone().Type(token.StringType).Value("value").
				PositionLine(5).PositionColumn(8).PositionOffset(105).Build(),
		}

		got := resetOne(input)

		require.Len(t, got, 3)

		// First token should be at line 1, column 1, offset 0.
		assert.Equal(t, 1, got[0].Position.Line)
		assert.Equal(t, 1, got[0].Position.Column)
		assert.Equal(t, 1, got[0].Position.Offset)

		// Second token: same line, column adjusted relatively.
		assert.Equal(t, 1, got[1].Position.Line)
		assert.Equal(t, 4, got[1].Position.Column) // Offset 6 relative to start 3, plus the 1-based origin.
		assert.Equal(t, 4, got[1].Position.Offset) // Offset 103 relative to start 100, plus the 1-based origin.

		// Third token: same line, column adjusted relatively.
		assert.Equal(t, 1, got[2].Position.Line)
		assert.Equal(t, 6, got[2].Position.Column) // Offset 8 relative to start 3, plus the 1-based origin.
		assert.Equal(t, 6, got[2].Position.Offset) // Offset 105 relative to start 100, plus the 1-based origin.
	})

	t.Run("handles multiline tokens", func(t *testing.T) {
		t.Parallel()

		tkb := yamltest.NewTokenBuilder()
		input := token.Tokens{
			tkb.Clone().Type(token.StringType).Value("key").
				PositionLine(10).PositionColumn(5).PositionOffset(50).Build(),
			tkb.Clone().Type(token.StringType).Value("value").
				PositionLine(11).PositionColumn(5).PositionOffset(55).Build(),
		}

		got := resetOne(input)

		require.Len(t, got, 2)

		// First token at line 1.
		assert.Equal(t, 1, got[0].Position.Line)
		assert.Equal(t, 1, got[0].Position.Column)
		assert.Equal(t, 1, got[0].Position.Offset)

		// Second token at line 2 (relative).
		assert.Equal(t, 2, got[1].Position.Line)
		assert.Equal(t, 5, got[1].Position.Column) // Column preserved for non-first lines.
		assert.Equal(t, 6, got[1].Position.Offset) // Offset 55 relative to start 50, plus the 1-based origin.
	})

	t.Run("returns original slice for empty input", func(t *testing.T) {
		t.Parallel()

		input := token.Tokens{}
		got := resetOne(input)

		assert.Empty(t, got)
	})

	t.Run("returns original slice for nil input", func(t *testing.T) {
		t.Parallel()

		got := resetOne(nil)

		assert.Nil(t, got)
	})

	t.Run("clones tokens", func(t *testing.T) {
		t.Parallel()

		tkb := yamltest.NewTokenBuilder()
		original := tkb.Clone().Type(token.StringType).Value("key").
			PositionLine(5).PositionColumn(3).PositionOffset(100).Build()
		input := token.Tokens{original}

		got := resetOne(input)

		require.Len(t, got, 1)

		// Should be a different pointer.
		assert.NotSame(t, original, got[0])

		// Original should be unchanged.
		assert.Equal(t, 5, original.Position.Line)
		assert.Equal(t, 3, original.Position.Column)
		assert.Equal(t, 100, original.Position.Offset)
	})

	t.Run("handles nil position", func(t *testing.T) {
		t.Parallel()

		input := token.Tokens{
			&token.Token{Type: token.StringType, Value: "test", Position: nil},
		}

		got := resetOne(input)

		require.Len(t, got, 1)
		assert.Nil(t, got[0].Position)
	})

	t.Run("skips tokens with nil position when finding start", func(t *testing.T) {
		t.Parallel()

		tkb := yamltest.NewTokenBuilder()
		input := token.Tokens{
			&token.Token{Type: token.StringType, Value: "nil-pos", Position: nil},
			tkb.Clone().Type(token.StringType).Value("key").
				PositionLine(5).PositionColumn(3).PositionOffset(100).Build(),
		}

		got := resetOne(input)

		require.Len(t, got, 2)

		// First token still has nil position.
		assert.Nil(t, got[0].Position)

		// Second token should be reset to line 1.
		assert.Equal(t, 1, got[1].Position.Line)
		assert.Equal(t, 1, got[1].Position.Column)
		assert.Equal(t, 1, got[1].Position.Offset)
	})

	t.Run("preserves token values", func(t *testing.T) {
		t.Parallel()

		tkb := yamltest.NewTokenBuilder()
		input := token.Tokens{
			tkb.Clone().Type(token.StringType).Value("mykey").Origin("mykey").
				PositionLine(5).PositionColumn(3).Build(),
		}

		got := resetOne(input)

		require.Len(t, got, 1)
		assert.Equal(t, token.StringType, got[0].Type)
		assert.Equal(t, "mykey", got[0].Value)
		assert.Equal(t, "mykey", got[0].Origin)
	})
}
