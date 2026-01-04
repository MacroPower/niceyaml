package niceyaml_test

import (
	"testing"
	"unicode/utf8"

	"github.com/goccy/go-yaml/lexer"
	"github.com/goccy/go-yaml/parser"
	"github.com/goccy/go-yaml/token"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/macropower/niceyaml"
	"github.com/macropower/niceyaml/line"
	"github.com/macropower/niceyaml/position"
	"github.com/macropower/niceyaml/yamltest"
)

func TestTokens_String_Annotation(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input       string
		annotations map[int]line.Annotation // Line index -> annotation.
		want        string
	}{
		"single line with annotation": {
			input: "key: value\n",
			annotations: map[int]line.Annotation{
				0: {Content: "error", Column: 1},
			},
			want: yamltest.JoinLF(
				"   1 | key: value",
				"   1 | ^ error",
			),
		},
		"multiple lines one annotation": {
			input: yamltest.Input(`
				first: 1
				second: 2
			`),
			annotations: map[int]line.Annotation{
				1: {Content: "here", Column: 1},
			},
			want: yamltest.JoinLF(
				"   1 | first: 1",
				"   2 | second: 2",
				"   2 | ^ here",
			),
		},
		"multiple lines multiple annotations": {
			input: yamltest.Input(`
				first: 1
				second: 2
				third: 3
			`),
			annotations: map[int]line.Annotation{
				0: {Content: "start", Column: 1},
				2: {Content: "end", Column: 1},
			},
			want: yamltest.JoinLF(
				"   1 | first: 1",
				"   1 | ^ start",
				"   2 | second: 2",
				"   3 | third: 3",
				"   3 | ^ end",
			),
		},
		"mixed annotated and non-annotated": {
			input: yamltest.Input(`
				a: 1
				b: 2
				c: 3
				d: 4
			`),
			annotations: map[int]line.Annotation{
				1: {Content: "middle", Column: 3},
			},
			want: yamltest.JoinLF(
				"   1 | a: 1",
				"   2 | b: 2",
				"   2 |   ^ middle",
				"   3 | c: 3",
				"   4 | d: 4",
			),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			tks := lexer.Tokenize(tc.input)
			result := niceyaml.NewSourceFromTokens(tks, niceyaml.WithName("test"))

			// Apply annotations to specified lines.
			for idx, ann := range tc.annotations {
				require.Less(t, idx, result.Count(), "annotation index out of range")

				result.Annotate(idx, ann)
			}

			assert.Equal(t, tc.want, result.String())
		})
	}
}

type runePosition struct {
	R   rune
	Pos position.Position
}

func TestLines_Runes(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input string
		want  []runePosition
	}{
		"simple key-value": {
			// Note: lexer strips trailing newline from final simple value.
			input: "a: b\n",
			want: []runePosition{
				{R: 'a', Pos: position.New(0, 0)},
				{R: ':', Pos: position.New(0, 1)},
				{R: ' ', Pos: position.New(0, 2)},
				{R: 'b', Pos: position.New(0, 3)},
			},
		},
		"multi-line": {
			// Note: lexer strips trailing newline from final value on each line.
			input: "a: 1\nb: 2\n",
			want: []runePosition{
				{R: 'a', Pos: position.New(0, 0)},
				{R: ':', Pos: position.New(0, 1)},
				{R: ' ', Pos: position.New(0, 2)},
				{R: '1', Pos: position.New(0, 3)},
				{R: '\n', Pos: position.New(0, 4)},
				{R: 'b', Pos: position.New(1, 0)},
				{R: ':', Pos: position.New(1, 1)},
				{R: ' ', Pos: position.New(1, 2)},
				{R: '2', Pos: position.New(1, 3)},
			},
		},
		"utf8 - multibyte char": {
			input: "k: ü\n",
			want: []runePosition{
				{R: 'k', Pos: position.New(0, 0)},
				{R: ':', Pos: position.New(0, 1)},
				{R: ' ', Pos: position.New(0, 2)},
				{R: 'ü', Pos: position.New(0, 3)},
			},
		},
		"utf8 - japanese": {
			input: "k: 日本\n",
			want: []runePosition{
				{R: 'k', Pos: position.New(0, 0)},
				{R: ':', Pos: position.New(0, 1)},
				{R: ' ', Pos: position.New(0, 2)},
				{R: '日', Pos: position.New(0, 3)},
				{R: '本', Pos: position.New(0, 4)},
			},
		},
		"nested with indent": {
			input: "p:\n  c: v\n",
			want: []runePosition{
				{R: 'p', Pos: position.New(0, 0)},
				{R: ':', Pos: position.New(0, 1)},
				{R: '\n', Pos: position.New(0, 2)},
				{R: ' ', Pos: position.New(1, 0)},
				{R: ' ', Pos: position.New(1, 1)},
				{R: 'c', Pos: position.New(1, 2)},
				{R: ':', Pos: position.New(1, 3)},
				{R: ' ', Pos: position.New(1, 4)},
				{R: 'v', Pos: position.New(1, 5)},
			},
		},
		"empty": {
			input: "",
			want:  nil,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			tks := lexer.Tokenize(tc.input)
			lines := niceyaml.NewSourceFromTokens(tks)

			var got []runePosition

			for pos, r := range lines.Runes() {
				got = append(got, runePosition{R: r, Pos: pos})
			}

			assert.Equal(t, tc.want, got)
		})
	}
}

func TestLines_Runes_LiteralBlock(t *testing.T) {
	t.Parallel()

	// Literal blocks have multi-line content.
	// Runes should iterate through all content with correct positions.
	input := "s: |\n  a\n  b\n"
	tks := lexer.Tokenize(input)
	lines := niceyaml.NewSourceFromTokens(tks)

	var runes []rune //nolint:prealloc // Size unknown from iterator.

	var positions []position.Position //nolint:prealloc // Size unknown from iterator.

	for pos, r := range lines.Runes() {
		runes = append(runes, r)
		positions = append(positions, pos)
	}

	// Verify we iterate through all content.
	require.NotEmpty(t, runes, "should have runes")
	require.NotEmpty(t, positions, "should have positions")

	// Verify positions start at line 0 (0-indexed).
	assert.Equal(t, 0, positions[0].Line, "should start at line 0")
	assert.Equal(t, 0, positions[0].Col, "should start at column 0")

	// Verify positions increase monotonically.
	prevLine := -1

	prevCol := -1

	for i, pos := range positions {
		if pos.Line == prevLine {
			assert.Greater(t, pos.Col, prevCol, "column should increase on same line at index %d", i)
		} else if pos.Line > prevLine {
			// Line changed, column should reset to 0.
			assert.Equal(t, 0, pos.Col, "column should reset to 0 on new line at index %d", i)
		}

		prevLine = pos.Line
		prevCol = pos.Col
	}

	// Verify the last position is on a later line (multi-line content).
	assert.Positive(t, positions[len(positions)-1].Line, "should have content on multiple lines")
}

func TestLines_Runes_DiffBuiltLines(t *testing.T) {
	t.Parallel()

	// When Lines are built from a diff, Position.Line should be based on
	// the visual line index (Line.idx), not the source token position.
	// This is critical for Finder to work correctly with diffs.

	before := "key: old\n"
	after := "key: new\n"

	beforeLines := niceyaml.NewSourceFromString(before, niceyaml.WithName("before"))
	afterLines := niceyaml.NewSourceFromString(after, niceyaml.WithName("after"))

	revBefore := niceyaml.NewRevision(beforeLines)
	revAfter := niceyaml.NewRevision(afterLines)

	diff := niceyaml.NewFullDiff(revBefore, revAfter)
	lines := diff.Lines()

	// Diff should produce two lines: deleted (old) and inserted (new).
	// Both have the same source token line (1), but different visual indices (0, 1).
	require.Equal(t, 2, lines.Count(), "diff should produce 2 lines")

	var positions []struct {
		line int
		col  int
	}

	for pos, r := range lines.Runes() {
		if r == 'k' { // First char of each line.
			positions = append(positions, struct {
				line int
				col  int
			}{line: pos.Line, col: pos.Col})
		}
	}

	// Should have 2 'k' characters, one on each visual line.
	require.Len(t, positions, 2, "should have 2 lines starting with 'k'")

	// First line should be at visual line 0.
	assert.Equal(t, 0, positions[0].line, "first line should be at visual line 0")
	assert.Equal(t, 0, positions[0].col, "first 'k' should be at column 0")

	// Second line should be at visual line 1 (not 0, even though source token line is same).
	assert.Equal(t, 1, positions[1].line, "second line should be at visual line 1")
	assert.Equal(t, 0, positions[1].col, "second 'k' should be at column 0")
}

func TestLines_Lines_EarlyBreak(t *testing.T) {
	t.Parallel()

	input := "a: 1\nb: 2\nc: 3\n"
	tks := lexer.Tokenize(input)
	lines := niceyaml.NewSourceFromTokens(tks)

	var collected []int //nolint:prealloc // Testing early break with unknown iteration count.
	for pos := range lines.Lines() {
		collected = append(collected, pos.Line)
		if pos.Line >= 1 {
			break
		}
	}

	assert.Equal(t, []int{0, 1}, collected)
}

func TestLines_Runes_EarlyBreak(t *testing.T) {
	t.Parallel()

	input := "abc\n"
	tks := lexer.Tokenize(input)
	lines := niceyaml.NewSourceFromTokens(tks)

	var collected []rune //nolint:prealloc // Testing early break with unknown iteration count.
	for _, r := range lines.Runes() {
		collected = append(collected, r)
		if r == 'b' {
			break
		}
	}

	assert.Equal(t, []rune{'a', 'b'}, collected)
}

func TestLines_TokenPositionRanges(t *testing.T) {
	t.Parallel()

	t.Run("literal block content - returns all joined lines", func(t *testing.T) {
		t.Parallel()

		input := yamltest.Input(`
			key: |
			  line1
			  line2
		`)
		tks := lexer.Tokenize(input)
		result := niceyaml.NewSourceFromTokens(tks, niceyaml.WithName("test"))

		require.Equal(t, 3, result.Count())

		// Query line index 1 (second line), column 0 (start of the string token).
		// The literal block content starts at column 0 (the indentation spaces are part of Origin).
		ranges := result.TokenPositionRanges(position.Position{Line: 1, Col: 0})
		require.NotNil(t, ranges, "expected ranges for joined literal block")
		require.Len(t, ranges, 2, "expected 2 ranges for 2-line literal block content")

		// Collect line indices from ranges.
		lineIdxs := make([]int, len(ranges))
		for i, r := range ranges {
			lineIdxs[i] = r.Start.Line
			// End line should match start line for single-line ranges.
			assert.Equal(t, r.Start.Line, r.End.Line)
			// End column should be greater than start column.
			assert.Greater(t, r.End.Col, r.Start.Col)
		}

		assert.ElementsMatch(t, []int{1, 2}, lineIdxs)
	})

	t.Run("non-joined line - returns range", func(t *testing.T) {
		t.Parallel()

		input := "key: value\n"
		tks := lexer.Tokenize(input)
		result := niceyaml.NewSourceFromTokens(tks, niceyaml.WithName("test"))

		require.Equal(t, 1, result.Count())

		// Query line index 0, column 0 (the "key" token).
		ranges := result.TokenPositionRanges(position.Position{Line: 0, Col: 0})
		require.NotNil(t, ranges)
		require.Len(t, ranges, 1)
		assert.Equal(t, 0, ranges[0].Start.Line)
		assert.Equal(t, 0, ranges[0].Start.Col)
		assert.Equal(t, 0, ranges[0].End.Line)
		assert.Equal(t, 3, ranges[0].End.Col) // "key" is 3 chars.
	})

	t.Run("non-joined line multiple tokens - returns correct range", func(t *testing.T) {
		t.Parallel()

		input := "key: value\n"
		tks := lexer.Tokenize(input)
		result := niceyaml.NewSourceFromTokens(tks, niceyaml.WithName("test"))

		require.Equal(t, 1, result.Count())

		// Find where the last token ("value") actually starts.
		ln := result.Line(0)
		lastTokenIdx := len(ln.Tokens()) - 1
		require.Positive(t, lastTokenIdx, "expected multiple tokens")

		// Calculate where the last token starts.
		var expectedCol int
		for i := range lastTokenIdx {
			tk := ln.Token(i)
			origin := tk.Origin
			// Remove trailing newline if present for column calculation.
			if origin != "" && origin[len(origin)-1] == '\n' {
				origin = origin[:len(origin)-1]
			}

			expectedCol += utf8.RuneCountInString(origin)
		}

		// Query a column within the last token.
		ranges := result.TokenPositionRanges(position.Position{Line: 0, Col: expectedCol})
		require.NotNil(t, ranges)
		require.Len(t, ranges, 1)
		assert.Equal(t, 0, ranges[0].Start.Line)
		assert.Equal(t, expectedCol, ranges[0].Start.Col)
		assert.Equal(t, 0, ranges[0].End.Line)
		assert.Greater(t, ranges[0].End.Col, ranges[0].Start.Col)
	})

	t.Run("query indicator line of literal block - returns range", func(t *testing.T) {
		t.Parallel()

		input := yamltest.Input(`
			key: |
			  line1
			  line2
		`)
		tks := lexer.Tokenize(input)
		result := niceyaml.NewSourceFromTokens(tks, niceyaml.WithName("test"))

		require.Equal(t, 3, result.Count())

		// Query line index 0 (indicator line), column 0 (the "key" token).
		// The indicator line itself is not part of the join, but has tokens.
		ranges := result.TokenPositionRanges(position.Position{Line: 0, Col: 0})
		require.NotNil(t, ranges)
		require.Len(t, ranges, 1)
		assert.Equal(t, 0, ranges[0].Start.Line)
		assert.Equal(t, 0, ranges[0].Start.Col)
		assert.Equal(t, 0, ranges[0].End.Line)
		assert.Equal(t, 3, ranges[0].End.Col) // "key" is 3 chars.
	})

	t.Run("query from last line of literal block", func(t *testing.T) {
		t.Parallel()

		input := yamltest.Input(`
			key: |
			  line1
			  line2
		`)
		tks := lexer.Tokenize(input)
		result := niceyaml.NewSourceFromTokens(tks, niceyaml.WithName("test"))

		require.Equal(t, 3, result.Count())

		// Query line index 2 (last content line), column 0.
		ranges := result.TokenPositionRanges(position.Position{Line: 2, Col: 0})
		require.NotNil(t, ranges, "expected ranges when querying last joined line")
		require.Len(t, ranges, 2)

		lineIdxs := make([]int, len(ranges))
		for i, r := range ranges {
			lineIdxs[i] = r.Start.Line
			assert.Equal(t, r.Start.Line, r.End.Line)
			assert.Greater(t, r.End.Col, r.Start.Col)
		}

		assert.ElementsMatch(t, []int{1, 2}, lineIdxs)
	})

	t.Run("line index out of bounds - returns nil", func(t *testing.T) {
		t.Parallel()

		input := "key: value\n"
		tks := lexer.Tokenize(input)
		result := niceyaml.NewSourceFromTokens(tks, niceyaml.WithName("test"))

		// Query non-existent line index.
		ranges := result.TokenPositionRanges(position.Position{Line: 999, Col: 0})
		assert.Nil(t, ranges)

		// Negative index.
		ranges = result.TokenPositionRanges(position.Position{Line: -1, Col: 0})
		assert.Nil(t, ranges)
	})

	t.Run("column outside token range - returns nil", func(t *testing.T) {
		t.Parallel()

		input := yamltest.Input(`
			key: |
			  line1
			  line2
		`)
		tks := lexer.Tokenize(input)
		result := niceyaml.NewSourceFromTokens(tks, niceyaml.WithName("test"))

		require.Equal(t, 3, result.Count())

		// Query line index 1 with a column that's way beyond the token.
		ranges := result.TokenPositionRanges(position.Position{Line: 1, Col: 100})
		assert.Nil(t, ranges, "expected nil for column outside token range")
	})

	t.Run("three-line literal block", func(t *testing.T) {
		t.Parallel()

		input := yamltest.Input(`
			key: |
			  line1
			  line2
			  line3
		`)
		tks := lexer.Tokenize(input)
		result := niceyaml.NewSourceFromTokens(tks, niceyaml.WithName("test"))

		require.Equal(t, 4, result.Count())

		// Query middle line (line index 2), column 0.
		ranges := result.TokenPositionRanges(position.Position{Line: 2, Col: 0})
		require.NotNil(t, ranges)
		require.Len(t, ranges, 3, "expected 3 ranges for 3-line literal block content")

		lineIdxs := make([]int, len(ranges))
		for i, r := range ranges {
			lineIdxs[i] = r.Start.Line
			assert.Equal(t, r.Start.Line, r.End.Line)
			assert.Greater(t, r.End.Col, r.Start.Col)
		}

		assert.ElementsMatch(t, []int{1, 2, 3}, lineIdxs)
	})

	t.Run("folded block", func(t *testing.T) {
		t.Parallel()

		input := yamltest.Input(`
			key: >
			  line1
			  line2
		`)
		tks := lexer.Tokenize(input)
		result := niceyaml.NewSourceFromTokens(tks, niceyaml.WithName("test"))

		require.Equal(t, 3, result.Count())

		// Query line index 1 (first content line), column 0.
		ranges := result.TokenPositionRanges(position.Position{Line: 1, Col: 0})
		require.NotNil(t, ranges)
		require.Len(t, ranges, 2)

		lineIdxs := make([]int, len(ranges))
		for i, r := range ranges {
			lineIdxs[i] = r.Start.Line
			assert.Equal(t, r.Start.Line, r.End.Line)
			assert.Greater(t, r.End.Col, r.Start.Col)
		}

		assert.ElementsMatch(t, []int{1, 2}, lineIdxs)
	})

	t.Run("multiple positions - combined results", func(t *testing.T) {
		t.Parallel()

		input := yamltest.Input(`
			first: 1
			second: 2
		`)
		tks := lexer.Tokenize(input)
		result := niceyaml.NewSourceFromTokens(tks, niceyaml.WithName("test"))

		require.Equal(t, 2, result.Count())

		// Query two different positions: "first" token on line 0, "second" token on line 1.
		ranges := result.TokenPositionRanges(
			position.Position{Line: 0, Col: 0},
			position.Position{Line: 1, Col: 0},
		)
		require.NotNil(t, ranges)
		require.Len(t, ranges, 2)

		lineIdxs := make([]int, len(ranges))
		for i, r := range ranges {
			lineIdxs[i] = r.Start.Line
		}

		assert.ElementsMatch(t, []int{0, 1}, lineIdxs)
	})

	t.Run("multiple positions - deduplication", func(t *testing.T) {
		t.Parallel()

		input := "key: value\n"
		tks := lexer.Tokenize(input)
		result := niceyaml.NewSourceFromTokens(tks, niceyaml.WithName("test"))

		require.Equal(t, 1, result.Count())

		// Query the same position twice.
		ranges := result.TokenPositionRanges(
			position.Position{Line: 0, Col: 0},
			position.Position{Line: 0, Col: 0},
		)
		require.NotNil(t, ranges)
		require.Len(t, ranges, 1, "expected duplicates to be removed")

		assert.Equal(t, 0, ranges[0].Start.Line)
		assert.Equal(t, 0, ranges[0].Start.Col)
		assert.Equal(t, 0, ranges[0].End.Line)
		assert.Equal(t, 3, ranges[0].End.Col)
	})

	t.Run("multiple positions - empty input", func(t *testing.T) {
		t.Parallel()

		input := "key: value\n"
		tks := lexer.Tokenize(input)
		result := niceyaml.NewSourceFromTokens(tks, niceyaml.WithName("test"))

		// Query with no positions.
		ranges := result.TokenPositionRanges()
		assert.Nil(t, ranges)
	})

	t.Run("multiple positions - joined lines deduplication", func(t *testing.T) {
		t.Parallel()

		input := yamltest.Input(`
			key: |
			  line1
			  line2
		`)
		tks := lexer.Tokenize(input)
		result := niceyaml.NewSourceFromTokens(tks, niceyaml.WithName("test"))

		require.Equal(t, 3, result.Count())

		// Query both content lines of the same joined block.
		// Both positions are within the joined block, so they should return
		// the same ranges (deduplicated).
		ranges := result.TokenPositionRanges(
			position.Position{Line: 1, Col: 0},
			position.Position{Line: 2, Col: 0},
		)
		require.NotNil(t, ranges)
		require.Len(t, ranges, 2, "expected 2 unique ranges from joined block")

		lineIdxs := make([]int, len(ranges))
		for i, r := range ranges {
			lineIdxs[i] = r.Start.Line
		}

		assert.ElementsMatch(t, []int{1, 2}, lineIdxs)
	})
}

func TestNewSourceFromToken_WalksToPrev(t *testing.T) {
	t.Parallel()

	// Test that NewSourceFromToken walks to the first token when given a middle token.
	input := "a: 1\nb: 2\n"
	tks := lexer.Tokenize(input)

	// Get a token that's not at the start (the second key "b").
	var middleToken *token.Token

	for _, tk := range tks {
		if tk.Value == "b" {
			middleToken = tk
			break
		}
	}

	require.NotNil(t, middleToken, "should find 'b' token")

	// Create source from middle token - it should walk to the start.
	source := niceyaml.NewSourceFromToken(middleToken)

	// Should contain all lines, not just from 'b' onwards.
	require.Equal(t, 2, source.Count())
	assert.Contains(t, source.Content(), "a: 1")
	assert.Contains(t, source.Content(), "b: 2")
}

func TestNewSourceFromToken_FiltersImplicitNull(t *testing.T) {
	t.Parallel()

	// Parse YAML with an implicit null (key with no value).
	// The parser adds ImplicitNullType tokens which should be filtered.
	input := "key:\n"
	tks := lexer.Tokenize(input)

	// Parse to get the AST with implicit null tokens.
	file, err := parser.Parse(tks, 0)
	require.NoError(t, err)
	require.Len(t, file.Docs, 1)

	// Get a token from the parsed AST (which may have ImplicitNullType).
	bodyToken := file.Docs[0].Body.GetToken()
	require.NotNil(t, bodyToken)

	// Create source from this token chain.
	source := niceyaml.NewSourceFromToken(bodyToken)

	// Should still work and produce valid output.
	require.NotNil(t, source)
	// The content should not be affected by filtering.
	assert.Contains(t, source.Content(), "key:")
}

func TestSource_SetFlag(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input string
		flag  line.Flag
		idx   int
	}{
		"set inserted flag": {
			input: "key: value\n",
			idx:   0,
			flag:  line.FlagInserted,
		},
		"set deleted flag": {
			input: "key: value\n",
			idx:   0,
			flag:  line.FlagDeleted,
		},
		"set flag on second line": {
			input: "a: 1\nb: 2\n",
			idx:   1,
			flag:  line.FlagInserted,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			source := niceyaml.NewSourceFromString(tc.input)
			source.SetFlag(tc.idx, tc.flag)

			got := source.Line(tc.idx).Flag
			assert.Equal(t, tc.flag, got)
		})
	}
}

func TestSource_Content(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input string
		want  string
	}{
		"single line": {
			input: "key: value\n",
			want:  "key: value",
		},
		"multiple lines": {
			input: "a: 1\nb: 2\nc: 3\n",
			want: yamltest.JoinLF(
				"a: 1",
				"b: 2",
				"c: 3",
			),
		},
		"empty": {
			input: "",
			want:  "",
		},
		"nested yaml": {
			input: "parent:\n  child: value\n",
			want: yamltest.JoinLF(
				"parent:",
				"  child: value",
			),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			source := niceyaml.NewSourceFromString(tc.input)
			got := source.Content()

			assert.Equal(t, tc.want, got)
		})
	}
}

func TestSource_Validate(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input string
	}{
		"valid source": {
			input: "key: value\n",
		},
		"empty source": {
			input: "",
		},
		"multi-line source": {
			input: "a: 1\nb: 2\nc: 3\n",
		},
		"literal block source": {
			input: yamltest.Input(`
				key: |
				  line1
				  line2
			`),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			source := niceyaml.NewSourceFromString(tc.input)
			err := source.Validate()

			assert.NoError(t, err)
		})
	}
}

func TestSource_Parse(t *testing.T) {
	t.Parallel()

	t.Run("valid YAML", func(t *testing.T) {
		t.Parallel()

		tcs := map[string]struct {
			input        string
			wantDocCount int
		}{
			"simple key-value": {
				input:        "key: value\n",
				wantDocCount: 1,
			},
			"nested map": {
				input: yamltest.Input(`
					parent:
					  child: value
				`),
				wantDocCount: 1,
			},
			"multiple documents": {
				input: yamltest.Input(`
					---
					doc1: value1
					---
					doc2: value2
				`),
				wantDocCount: 2,
			},
			"list": {
				input: yamltest.Input(`
					items:
					  - one
					  - two
				`),
				wantDocCount: 1,
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				source := niceyaml.NewSourceFromString(tc.input)
				file, err := source.Parse()

				require.NoError(t, err)
				require.NotNil(t, file)
				assert.Len(t, file.Docs, tc.wantDocCount)
			})
		}
	})

	t.Run("empty source", func(t *testing.T) {
		t.Parallel()

		source := niceyaml.NewSourceFromString("")
		file, err := source.Parse()

		require.NoError(t, err)
		require.NotNil(t, file)
		// Go-yaml parser returns 1 doc for empty input (empty document).
		assert.Len(t, file.Docs, 1)
	})

	t.Run("invalid YAML returns Error", func(t *testing.T) {
		t.Parallel()

		// Create invalid YAML by manually constructing malformed tokens.
		tkb := yamltest.NewTokenBuilder().Type(token.MappingValueType).Value(":").PositionLine(1)
		tks := token.Tokens{}
		tks.Add(tkb.Clone().Origin(":").PositionColumn(1).Build())
		tks.Add(tkb.Clone().Origin(":\n").PositionColumn(2).Build())

		source := niceyaml.NewSourceFromTokens(tks)
		file, err := source.Parse()

		require.Error(t, err)
		assert.Nil(t, file)

		// Verify it's a niceyaml.Error with source annotation.
		var yamlErr *niceyaml.Error
		require.ErrorAs(t, err, &yamlErr)
	})
}
