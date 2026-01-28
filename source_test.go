package niceyaml_test

import (
	"errors"
	"testing"
	"unicode/utf8"

	"github.com/goccy/go-yaml/lexer"
	"github.com/goccy/go-yaml/parser"
	"github.com/goccy/go-yaml/token"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"jacobcolvin.com/niceyaml"
	"jacobcolvin.com/niceyaml/internal/yamltest"
	"jacobcolvin.com/niceyaml/line"
	"jacobcolvin.com/niceyaml/position"
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
				0: {Content: "error", Position: line.Below},
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
				1: {Content: "here", Position: line.Below},
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
				0: {Content: "start", Position: line.Below},
				2: {Content: "end", Position: line.Below},
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
				1: {Content: "middle", Position: line.Below, Col: 2},
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
				require.Less(t, idx, result.Len(), "annotation index out of range")

				result.Line(idx).AddAnnotation(ann)
			}

			assert.Equal(t, tc.want, result.String())
		})
	}
}

type runePosition struct {
	R   rune
	Pos position.Position
}

func TestSource_AllRunes(t *testing.T) {
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

			for pos, r := range lines.AllRunes() {
				got = append(got, runePosition{R: r, Pos: pos})
			}

			assert.Equal(t, tc.want, got)
		})
	}
}

func TestSource_AllRunes_LiteralBlock(t *testing.T) {
	t.Parallel()

	// Literal blocks have multi-line content.
	// Runes should iterate through all content with correct positions.
	input := "s: |\n  a\n  b\n"
	tks := lexer.Tokenize(input)
	lines := niceyaml.NewSourceFromTokens(tks)

	var runes []rune //nolint:prealloc // Size unknown from iterator.

	var positions []position.Position //nolint:prealloc // Size unknown from iterator.

	for pos, r := range lines.AllRunes() {
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

func TestSource_AllRunes_DiffBuiltLines(t *testing.T) {
	t.Parallel()

	// When Lines are built from a diff, Position.Line should be based on the
	// visual line index (Line.idx), not the source token position.
	// This is critical for Finder to work correctly with diffs.

	before := "key: old\n"
	after := "key: new\n"

	beforeLines := niceyaml.NewSourceFromString(before, niceyaml.WithName("before"))
	afterLines := niceyaml.NewSourceFromString(after, niceyaml.WithName("after"))

	revBefore := niceyaml.NewRevision(beforeLines)
	revAfter := niceyaml.NewRevision(afterLines)

	lines := niceyaml.Diff(revBefore, revAfter).Full()

	// Diff should produce two lines: deleted (old) and inserted (new).
	// Both have the same source token line (1), but different visual indices (0, 1).
	require.Equal(t, 2, lines.Len(), "diff should produce 2 lines")

	var positions []struct {
		line int
		col  int
	}

	for pos, r := range lines.AllRunes() {
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

func TestSource_AllLines_EarlyBreak(t *testing.T) {
	t.Parallel()

	input := "a: 1\nb: 2\nc: 3\n"
	tks := lexer.Tokenize(input)
	lines := niceyaml.NewSourceFromTokens(tks)

	var collected []int //nolint:prealloc // Testing early break with unknown iteration count.
	for pos := range lines.AllLines() {
		collected = append(collected, pos.Line)
		if pos.Line >= 1 {
			break
		}
	}

	assert.Equal(t, []int{0, 1}, collected)
}

func TestSource_AllLines_WithSpans(t *testing.T) {
	t.Parallel()

	input := "a: 1\nb: 2\nc: 3\nd: 4\ne: 5\n"
	source := niceyaml.NewSourceFromString(input)

	require.Equal(t, 5, source.Len())

	t.Run("no spans returns all lines", func(t *testing.T) {
		t.Parallel()

		var collected []int
		for pos := range source.AllLines() {
			collected = append(collected, pos.Line)
		}

		assert.Equal(t, []int{0, 1, 2, 3, 4}, collected)
	})

	t.Run("single span filters lines", func(t *testing.T) {
		t.Parallel()

		var collected []int
		for pos := range source.AllLines(position.NewSpan(1, 3)) {
			collected = append(collected, pos.Line)
		}

		assert.Equal(t, []int{1, 2}, collected)
	})

	t.Run("multiple spans iterate in order", func(t *testing.T) {
		t.Parallel()

		var collected []int
		for pos := range source.AllLines(
			position.NewSpan(0, 1),
			position.NewSpan(3, 5),
		) {
			collected = append(collected, pos.Line)
		}

		assert.Equal(t, []int{0, 3, 4}, collected)
	})

	t.Run("span clamped to bounds", func(t *testing.T) {
		t.Parallel()

		var collected []int
		for pos := range source.AllLines(position.NewSpan(-5, 100)) {
			collected = append(collected, pos.Line)
		}

		assert.Equal(t, []int{0, 1, 2, 3, 4}, collected)
	})

	t.Run("empty span yields nothing", func(t *testing.T) {
		t.Parallel()

		var collected []int
		for pos := range source.AllLines(position.NewSpan(2, 2)) {
			collected = append(collected, pos.Line)
		}

		assert.Nil(t, collected)
	})

	t.Run("span beyond length yields nothing", func(t *testing.T) {
		t.Parallel()

		var collected []int
		for pos := range source.AllLines(position.NewSpan(10, 20)) {
			collected = append(collected, pos.Line)
		}

		assert.Nil(t, collected)
	})
}

func TestSource_Lines(t *testing.T) {
	t.Parallel()

	src := niceyaml.NewSourceFromString("key: value\nfoo: bar")
	lines := src.Lines()

	assert.Len(t, lines, src.Len())
	assert.Equal(t, "key: value", lines[0].Content())
	assert.Equal(t, "foo: bar", lines[1].Content())
}

func TestSource_AllRunes_EarlyBreak(t *testing.T) {
	t.Parallel()

	input := "abc\n"
	tks := lexer.Tokenize(input)
	lines := niceyaml.NewSourceFromTokens(tks)

	var collected []rune //nolint:prealloc // Testing early break with unknown iteration count.
	for _, r := range lines.AllRunes() {
		collected = append(collected, r)
		if r == 'b' {
			break
		}
	}

	assert.Equal(t, []rune{'a', 'b'}, collected)
}

func TestSource_AllRunes_WithRanges(t *testing.T) {
	t.Parallel()

	input := "a: 1\nb: 2\nc: 3\nd: 4\ne: 5\n"
	source := niceyaml.NewSourceFromString(input)

	require.Equal(t, 5, source.Len())

	t.Run("no ranges returns all runes", func(t *testing.T) {
		t.Parallel()

		var collected []runePosition
		for pos, r := range source.AllRunes() {
			collected = append(collected, runePosition{R: r, Pos: pos})
		}

		// Should have all runes from all lines.
		assert.NotEmpty(t, collected)
		// First rune should be 'a' at line 0, col 0.
		assert.Equal(t, 'a', collected[0].R)
		assert.Equal(t, position.New(0, 0), collected[0].Pos)
	})

	t.Run("single range filters runes correctly", func(t *testing.T) {
		t.Parallel()

		// Range covering only "b: 2" on line 1.
		rng := position.NewRange(position.New(1, 0), position.New(1, 4))

		var collected []runePosition
		for pos, r := range source.AllRunes(rng) {
			collected = append(collected, runePosition{R: r, Pos: pos})
		}

		// Should only have runes from line 1, columns 0-3 (half-open range).
		require.Len(t, collected, 4)
		assert.Equal(t, 'b', collected[0].R)
		assert.Equal(t, position.New(1, 0), collected[0].Pos)
		assert.Equal(t, ':', collected[1].R)
		assert.Equal(t, position.New(1, 1), collected[1].Pos)
		assert.Equal(t, ' ', collected[2].R)
		assert.Equal(t, position.New(1, 2), collected[2].Pos)
		assert.Equal(t, '2', collected[3].R)
		assert.Equal(t, position.New(1, 3), collected[3].Pos)
	})

	t.Run("range spanning multiple lines", func(t *testing.T) {
		t.Parallel()

		// Range from middle of line 1 to middle of line 2.
		rng := position.NewRange(position.New(1, 2), position.New(2, 2))

		var collected []runePosition
		for pos, r := range source.AllRunes(rng) {
			collected = append(collected, runePosition{R: r, Pos: pos})
		}

		// Line 1: cols 2-4 (newline at 4), Line 2: cols 0-1.
		require.NotEmpty(t, collected)

		// First should be ' ' at line 1, col 2.
		assert.Equal(t, ' ', collected[0].R)
		assert.Equal(t, position.New(1, 2), collected[0].Pos)

		// Verify all positions are within the range.
		for _, rp := range collected {
			assert.True(t, rng.Contains(rp.Pos), "position %v should be in range", rp.Pos)
		}
	})

	t.Run("range clamped to bounds", func(t *testing.T) {
		t.Parallel()

		// Range that extends beyond source bounds.
		rng := position.NewRange(position.New(-5, 0), position.New(100, 100))

		var collected []runePosition
		for pos, r := range source.AllRunes(rng) {
			collected = append(collected, runePosition{R: r, Pos: pos})
		}

		// Should return all runes since range encompasses everything.
		assert.NotEmpty(t, collected)
		// First should be 'a'.
		assert.Equal(t, 'a', collected[0].R)
	})

	t.Run("empty source returns nothing", func(t *testing.T) {
		t.Parallel()

		emptySource := niceyaml.NewSourceFromString("")
		rng := position.NewRange(position.New(0, 0), position.New(0, 5))

		var collected []runePosition
		for pos, r := range emptySource.AllRunes(rng) {
			collected = append(collected, runePosition{R: r, Pos: pos})
		}

		assert.Nil(t, collected)
	})

	t.Run("out-of-range yields nothing", func(t *testing.T) {
		t.Parallel()

		// Range that's completely beyond the source.
		rng := position.NewRange(position.New(100, 0), position.New(100, 10))

		var collected []runePosition
		for pos, r := range source.AllRunes(rng) {
			collected = append(collected, runePosition{R: r, Pos: pos})
		}

		assert.Nil(t, collected)
	})

	t.Run("multiple ranges", func(t *testing.T) {
		t.Parallel()

		// Two non-overlapping ranges.
		rng1 := position.NewRange(position.New(0, 0), position.New(0, 1)) // 'a'.
		rng2 := position.NewRange(position.New(2, 0), position.New(2, 1)) // 'c'.

		var collected []runePosition
		for pos, r := range source.AllRunes(rng1, rng2) {
			collected = append(collected, runePosition{R: r, Pos: pos})
		}

		require.Len(t, collected, 2)
		assert.Equal(t, 'a', collected[0].R)
		assert.Equal(t, position.New(0, 0), collected[0].Pos)
		assert.Equal(t, 'c', collected[1].R)
		assert.Equal(t, position.New(2, 0), collected[1].Pos)
	})

	t.Run("early break works with ranges", func(t *testing.T) {
		t.Parallel()

		rng := position.NewRange(position.New(0, 0), position.New(2, 10))

		var collected []rune //nolint:prealloc // Testing early break.
		for _, r := range source.AllRunes(rng) {
			collected = append(collected, r)
			if r == ':' {
				break
			}
		}

		// Should stop at first colon.
		assert.Equal(t, []rune{'a', ':'}, collected)
	})
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

		require.Equal(t, 3, result.Len())

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

		require.Equal(t, 1, result.Len())

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

		require.Equal(t, 1, result.Len())

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

		require.Equal(t, 3, result.Len())

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

		require.Equal(t, 3, result.Len())

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

		require.Equal(t, 3, result.Len())

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

		require.Equal(t, 4, result.Len())

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

		require.Equal(t, 3, result.Len())

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

		require.Equal(t, 2, result.Len())

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

		require.Equal(t, 1, result.Len())

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

		require.Equal(t, 3, result.Len())

		// Query both content lines of the same joined block.
		//
		// Both positions are within the joined block, so they should return the same
		// ranges (deduplicated).
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
	require.Equal(t, 2, source.Len())
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
			input string
			want  int
		}{
			"simple key-value": {
				input: "key: value\n",
				want:  1,
			},
			"nested map": {
				input: yamltest.Input(`
					parent:
					  child: value
				`),
				want: 1,
			},
			"multiple documents": {
				input: yamltest.Input(`
					---
					doc1: value1
					---
					doc2: value2
				`),
				want: 2,
			},
			"list": {
				input: yamltest.Input(`
					items:
					  - one
					  - two
				`),
				want: 1,
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				source := niceyaml.NewSourceFromString(tc.input)
				file, err := source.File()

				require.NoError(t, err)
				require.NotNil(t, file)
				assert.Len(t, file.Docs, tc.want)
			})
		}
	})

	t.Run("empty source", func(t *testing.T) {
		t.Parallel()

		source := niceyaml.NewSourceFromString("")
		file, err := source.File()

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
		file, err := source.File()

		require.Error(t, err)
		assert.Nil(t, file)

		// Verify it's a niceyaml.Error with source annotation.
		var yamlErr *niceyaml.Error
		require.ErrorAs(t, err, &yamlErr)
	})
}

func TestSource_WithParserOptions(t *testing.T) {
	t.Parallel()

	t.Run("parses with default options", func(t *testing.T) {
		t.Parallel()

		source := niceyaml.NewSourceFromString(
			"key: value\n",
			niceyaml.WithParserOptions(),
		)

		file, err := source.File()
		require.NoError(t, err)
		assert.NotNil(t, file)
	})

	t.Run("parses complex YAML with options", func(t *testing.T) {
		t.Parallel()

		input := yamltest.Input(`
			key1: value1
			key2: value2
			nested:
			  child: data
		`)

		source := niceyaml.NewSourceFromString(
			input,
			niceyaml.WithParserOptions(),
		)

		file, err := source.File()
		require.NoError(t, err)
		assert.NotNil(t, file)
		assert.Len(t, file.Docs, 1)
	})
}

func TestSource_AddOverlay(t *testing.T) {
	t.Parallel()

	t.Run("single line range", func(t *testing.T) {
		t.Parallel()

		source := niceyaml.NewSourceFromString("key: value\n")
		require.Equal(t, 1, source.Len())

		source.AddOverlay(1, position.NewRange(
			position.New(0, 0),
			position.New(0, 5),
		))

		ln := source.Line(0)
		require.Len(t, ln.Overlays, 1)
		assert.Equal(t, position.NewSpan(0, 5), ln.Overlays[0].Cols)
		assert.Equal(t, 1, ln.Overlays[0].Kind)
	})

	t.Run("multi-line range splits across lines", func(t *testing.T) {
		t.Parallel()

		input := yamltest.Input(`
			key1: value1
			key2: value2
			key3: value3
		`)
		source := niceyaml.NewSourceFromString(input)
		require.Equal(t, 3, source.Len())

		// Add overlay spanning all three lines.
		source.AddOverlay(2, position.NewRange(
			position.New(0, 3),
			position.New(2, 5),
		))

		// First line: col 3 to end of line.
		ln0 := source.Line(0)
		require.Len(t, ln0.Overlays, 1)
		assert.Equal(t, 3, ln0.Overlays[0].Cols.Start)
		assert.Equal(t, 2, ln0.Overlays[0].Kind)

		// Middle line: full line.
		ln1 := source.Line(1)
		require.Len(t, ln1.Overlays, 1)
		assert.Equal(t, 0, ln1.Overlays[0].Cols.Start)
		assert.Equal(t, 2, ln1.Overlays[0].Kind)

		// Last line: start to col 5.
		ln2 := source.Line(2)
		require.Len(t, ln2.Overlays, 1)
		assert.Equal(t, 0, ln2.Overlays[0].Cols.Start)
		assert.Equal(t, 5, ln2.Overlays[0].Cols.End)
		assert.Equal(t, 2, ln2.Overlays[0].Kind)
	})

	t.Run("multiple ranges", func(t *testing.T) {
		t.Parallel()

		input := yamltest.Input(`
			key1: value1
			key2: value2
		`)
		source := niceyaml.NewSourceFromString(input)
		require.Equal(t, 2, source.Len())

		source.AddOverlay(1,
			position.NewRange(position.New(0, 0), position.New(0, 4)),
			position.NewRange(position.New(1, 0), position.New(1, 4)),
		)

		require.Len(t, source.Line(0).Overlays, 1)
		require.Len(t, source.Line(1).Overlays, 1)
	})

	t.Run("empty source no-op", func(t *testing.T) {
		t.Parallel()

		source := niceyaml.NewSourceFromString("")
		// Should not panic on empty source.
		source.AddOverlay(1, position.NewRange(position.New(0, 0), position.New(0, 5)))

		assert.True(t, source.IsEmpty())
	})
}

func TestSource_ClearOverlays(t *testing.T) {
	t.Parallel()

	t.Run("clears all overlays", func(t *testing.T) {
		t.Parallel()

		input := yamltest.Input(`
			key1: value1
			key2: value2
		`)
		source := niceyaml.NewSourceFromString(input)
		require.Equal(t, 2, source.Len())

		// Add overlays to both lines.
		source.AddOverlay(1,
			position.NewRange(position.New(0, 0), position.New(0, 10)),
			position.NewRange(position.New(1, 0), position.New(1, 10)),
		)

		require.Len(t, source.Line(0).Overlays, 1)
		require.Len(t, source.Line(1).Overlays, 1)

		// Clear all overlays.
		source.ClearOverlays()

		assert.Nil(t, source.Line(0).Overlays)
		assert.Nil(t, source.Line(1).Overlays)
	})

	t.Run("idempotent on empty", func(t *testing.T) {
		t.Parallel()

		source := niceyaml.NewSourceFromString("key: value\n")
		require.Equal(t, 1, source.Len())

		// Clear without any overlays set.
		source.ClearOverlays()

		assert.Nil(t, source.Line(0).Overlays)
	})
}

func TestSource_Name(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		name string
		want string
	}{
		"with name": {
			name: "test.yaml",
			want: "test.yaml",
		},
		"empty name": {
			name: "",
			want: "",
		},
		"path-like name": {
			name: "/path/to/file.yaml",
			want: "/path/to/file.yaml",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			source := niceyaml.NewSourceFromString("key: value", niceyaml.WithName(tc.name))
			got := source.Name()

			assert.Equal(t, tc.want, got)
		})
	}
}

func TestSource_Len(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input string
		want  int
	}{
		"single line": {
			input: "key: value\n",
			want:  1,
		},
		"multiple lines": {
			input: "a: 1\nb: 2\nc: 3\n",
			want:  3,
		},
		"empty": {
			input: "",
			want:  0,
		},
		"literal block": {
			input: yamltest.Input(`
				key: |
				  line1
				  line2
			`),
			want: 3,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			source := niceyaml.NewSourceFromString(tc.input)
			got := source.Len()

			assert.Equal(t, tc.want, got)
		})
	}
}

func TestSource_IsEmpty(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input string
		want  bool
	}{
		"empty string": {
			input: "",
			want:  true,
		},
		"single line": {
			input: "key: value\n",
			want:  false,
		},
		"multiple lines": {
			input: "a: 1\nb: 2\n",
			want:  false,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			source := niceyaml.NewSourceFromString(tc.input)
			got := source.IsEmpty()

			assert.Equal(t, tc.want, got)
		})
	}
}

func TestSource_TokenAt(t *testing.T) {
	t.Parallel()

	input := "key: value\n"
	source := niceyaml.NewSourceFromString(input)

	tcs := map[string]struct {
		pos       position.Position
		wantValue string
		wantNil   bool
	}{
		"key token at start": {
			pos:       position.New(0, 0),
			wantValue: "key",
		},
		"key token at end of key": {
			pos:       position.New(0, 2),
			wantValue: "key",
		},
		"colon token": {
			pos:       position.New(0, 3),
			wantValue: ":",
		},
		"value token": {
			pos:       position.New(0, 5),
			wantValue: "value",
		},
		"out of bounds line": {
			pos:     position.New(10, 0),
			wantNil: true,
		},
		"negative line": {
			pos:     position.New(-1, 0),
			wantNil: true,
		},
		"out of bounds column": {
			pos:     position.New(0, 100),
			wantNil: true,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := source.TokenAt(tc.pos)

			if tc.wantNil {
				assert.Nil(t, got)
			} else {
				require.NotNil(t, got)
				assert.Equal(t, tc.wantValue, got.Value)
			}
		})
	}
}

func TestSource_TokenPositionRangesFromToken(t *testing.T) {
	t.Parallel()

	t.Run("simple token", func(t *testing.T) {
		t.Parallel()

		input := "key: value\n"
		tks := lexer.Tokenize(input)
		source := niceyaml.NewSourceFromTokens(tks)

		// Find the "key" token.
		var keyToken *token.Token
		for _, tk := range tks {
			if tk.Value == "key" {
				keyToken = tk
				break
			}
		}

		require.NotNil(t, keyToken)

		ranges := source.TokenPositionRangesFromToken(keyToken)

		require.Len(t, ranges, 1)
		assert.Equal(t, 0, ranges[0].Start.Line)
		assert.Equal(t, 0, ranges[0].Start.Col)
		assert.Equal(t, 0, ranges[0].End.Line)
		assert.Equal(t, 3, ranges[0].End.Col)
	})

	t.Run("nil token returns nil", func(t *testing.T) {
		t.Parallel()

		source := niceyaml.NewSourceFromString("key: value\n")
		ranges := source.TokenPositionRangesFromToken(nil)

		assert.Nil(t, ranges)
	})

	t.Run("token not in source returns nil", func(t *testing.T) {
		t.Parallel()

		source := niceyaml.NewSourceFromString("key: value\n")
		otherToken := &token.Token{Value: "other"}

		ranges := source.TokenPositionRangesFromToken(otherToken)

		assert.Nil(t, ranges)
	})
}

func TestSource_ContentPositionRanges(t *testing.T) {
	t.Parallel()

	t.Run("simple content", func(t *testing.T) {
		t.Parallel()

		input := "key: value\n"
		source := niceyaml.NewSourceFromString(input)

		// Query position at "key".
		ranges := source.ContentPositionRanges(position.New(0, 0))

		require.NotNil(t, ranges)
		require.Len(t, ranges, 1)
		assert.Equal(t, 0, ranges[0].Start.Line)
	})

	t.Run("multiple positions", func(t *testing.T) {
		t.Parallel()

		input := yamltest.Input(`
			first: 1
			second: 2
		`)
		source := niceyaml.NewSourceFromString(input)

		ranges := source.ContentPositionRanges(
			position.New(0, 0), // "first".
			position.New(1, 0), // "second".
		)

		require.NotNil(t, ranges)
		require.Len(t, ranges, 2)
	})

	t.Run("empty positions returns nil", func(t *testing.T) {
		t.Parallel()

		source := niceyaml.NewSourceFromString("key: value\n")
		ranges := source.ContentPositionRanges()

		assert.Nil(t, ranges)
	})

	t.Run("out of bounds position returns nil", func(t *testing.T) {
		t.Parallel()

		source := niceyaml.NewSourceFromString("key: value\n")
		ranges := source.ContentPositionRanges(position.New(100, 0))

		assert.Nil(t, ranges)
	})
}

func TestSource_ContentPositionRangesFromToken(t *testing.T) {
	t.Parallel()

	t.Run("simple token", func(t *testing.T) {
		t.Parallel()

		input := "key: value\n"
		tks := lexer.Tokenize(input)
		source := niceyaml.NewSourceFromTokens(tks)

		// Find the "value" token.
		var valueToken *token.Token
		for _, tk := range tks {
			if tk.Value == "value" {
				valueToken = tk
				break
			}
		}

		require.NotNil(t, valueToken)

		ranges := source.ContentPositionRangesFromToken(valueToken)

		require.NotNil(t, ranges)
		require.Len(t, ranges, 1)
		assert.Equal(t, 0, ranges[0].Start.Line)
	})

	t.Run("nil token returns nil", func(t *testing.T) {
		t.Parallel()

		source := niceyaml.NewSourceFromString("key: value\n")
		ranges := source.ContentPositionRangesFromToken(nil)

		assert.Nil(t, ranges)
	})

	t.Run("token not in source returns nil", func(t *testing.T) {
		t.Parallel()

		source := niceyaml.NewSourceFromString("key: value\n")
		otherToken := &token.Token{Value: "other"}

		ranges := source.ContentPositionRangesFromToken(otherToken)

		assert.Nil(t, ranges)
	})
}

func TestSource_WrapError(t *testing.T) {
	t.Parallel()

	t.Run("wraps niceyaml.Error", func(t *testing.T) {
		t.Parallel()

		source := niceyaml.NewSourceFromString("key: value\n")
		yamlErr := niceyaml.NewError("test error")

		wrapped := source.WrapError(yamlErr)

		require.Error(t, wrapped)

		var gotErr *niceyaml.Error
		require.ErrorAs(t, wrapped, &gotErr)
	})

	t.Run("returns nil for nil error", func(t *testing.T) {
		t.Parallel()

		source := niceyaml.NewSourceFromString("key: value\n")
		wrapped := source.WrapError(nil)

		assert.NoError(t, wrapped)
	})

	t.Run("returns non-Error unchanged", func(t *testing.T) {
		t.Parallel()

		source := niceyaml.NewSourceFromString("key: value\n")
		stdErr := errors.New("standard error")

		wrapped := source.WrapError(stdErr)

		assert.Equal(t, stdErr, wrapped)
	})
}
