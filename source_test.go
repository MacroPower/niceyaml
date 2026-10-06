package niceyaml_test

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"testing/iotest"

	"charm.land/lipgloss/v2"
	"github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/lexer"
	"github.com/goccy/go-yaml/parser"
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
	"go.jacobcolvin.com/niceyaml/schema"
	"go.jacobcolvin.com/niceyaml/schema/matcher"
	"go.jacobcolvin.com/niceyaml/style"
	"go.jacobcolvin.com/niceyaml/style/kind"
	"go.jacobcolvin.com/niceyaml/tokens"
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
				0: {Content: "error", Placement: line.Below},
			},
			want: stringtest.JoinLF(
				"   1 | key: value",
				"     | ^ error",
			),
		},
		"multiple lines one annotation": {
			input: stringtest.Input(`
				first: 1
				second: 2
			`),
			annotations: map[int]line.Annotation{
				1: {Content: "here", Placement: line.Below},
			},
			want: stringtest.JoinLF(
				"   1 | first: 1",
				"   2 | second: 2",
				"     | ^ here",
			),
		},
		"multiple lines multiple annotations": {
			input: stringtest.Input(`
				first: 1
				second: 2
				third: 3
			`),
			annotations: map[int]line.Annotation{
				0: {Content: "start", Placement: line.Below},
				2: {Content: "end", Placement: line.Below},
			},
			want: stringtest.JoinLF(
				"   1 | first: 1",
				"     | ^ start",
				"   2 | second: 2",
				"   3 | third: 3",
				"     | ^ end",
			),
		},
		"mixed annotated and non-annotated": {
			input: stringtest.Input(`
				a: 1
				b: 2
				c: 3
				d: 4
			`),
			annotations: map[int]line.Annotation{
				1: {Content: "middle", Placement: line.Below, Col: 2},
			},
			want: stringtest.JoinLF(
				"   1 | a: 1",
				"   2 | b: 2",
				"     |   ^ middle",
				"   3 | c: 3",
				"   4 | d: 4",
			),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			tks := lexer.Tokenize(tc.input)
			view := niceyaml.NewSourceFromTokens(tks, niceyaml.WithName("test")).View()

			// Apply annotations to specified lines.
			for idx, ann := range tc.annotations {
				require.Less(t, idx, view.Count(), "annotation index out of range")

				view.Annotate(idx, ann)
			}

			assert.Equal(t, tc.want, view.String())
		})
	}
}

type runePosition struct {
	R   rune
	Pos position.Position
}

func TestSource_Runes(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input string
		want  []runePosition
	}{
		"simple key-value": {
			// The lexer strips the trailing newline from the final simple value.
			input: "a: b\n",
			want: []runePosition{
				{R: 'a', Pos: position.New(0, 0)},
				{R: ':', Pos: position.New(0, 1)},
				{R: ' ', Pos: position.New(0, 2)},
				{R: 'b', Pos: position.New(0, 3)},
			},
		},
		"multi-line": {
			// The lexer strips the trailing newline from the final value on each line.
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

			for pos, r := range lines.Lines().Runes() {
				got = append(got, runePosition{R: r, Pos: pos})
			}

			assert.Equal(t, tc.want, got)
		})
	}
}

func TestSource_Runes_LiteralBlock(t *testing.T) {
	t.Parallel()

	// Literal blocks have multi-line content.
	// Runes should iterate through all content with correct positions.
	input := "s: |\n  a\n  b\n"
	tks := lexer.Tokenize(input)
	lines := niceyaml.NewSourceFromTokens(tks)

	var (
		runes     []rune
		positions []position.Position
	)

	for pos, r := range lines.Lines().Runes() {
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

func TestSource_Runes_DiffBuiltLines(t *testing.T) {
	t.Parallel()

	// When a diff builds Lines, Position.Line follows the visual line index
	// within Lines, not the source token position.
	// Finder maps its matches back onto those visual lines.

	before := "key: old\n"
	after := "key: new\n"

	beforeLines := niceyaml.NewSourceFromString(before, niceyaml.WithName("before"))
	afterLines := niceyaml.NewSourceFromString(after, niceyaml.WithName("after"))

	lines := diff.Diff(beforeLines.Lines(), afterLines.Lines()).Unified()

	// Diff should produce two lines: deleted (old) and inserted (new).
	// Both have the same source token line (1), but different visual indices (0, 1).
	require.Equal(t, 2, lines.Count(), "diff should produce 2 lines")

	var positions []struct {
		line int
		col  int
	}

	for pos, r := range lines.Lines().Runes() {
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

func TestSource_All_EarlyBreak(t *testing.T) {
	t.Parallel()

	input := "a: 1\nb: 2\nc: 3\n"
	tks := lexer.Tokenize(input)
	lines := niceyaml.NewSourceFromTokens(tks)

	var collected []int

	for idx := range lines.Lines().All() {
		collected = append(collected, idx)
		if idx >= 1 {
			break
		}
	}

	assert.Equal(t, []int{0, 1}, collected)
}

func TestSource_All_WithSpans(t *testing.T) {
	t.Parallel()

	input := "a: 1\nb: 2\nc: 3\nd: 4\ne: 5\n"
	source := niceyaml.NewSourceFromString(input)

	require.Equal(t, 5, source.Lines().Len())

	t.Run("no spans returns all lines", func(t *testing.T) {
		t.Parallel()

		var collected []int

		for idx := range source.Lines().All() {
			collected = append(collected, idx)
		}

		assert.Equal(t, []int{0, 1, 2, 3, 4}, collected)
	})

	t.Run("single span filters lines", func(t *testing.T) {
		t.Parallel()

		var collected []int

		for idx := range source.Lines().All(position.NewSpan(1, 3)) {
			collected = append(collected, idx)
		}

		assert.Equal(t, []int{1, 2}, collected)
	})

	t.Run("multiple spans iterate in order", func(t *testing.T) {
		t.Parallel()

		var collected []int

		for idx := range source.Lines().All(
			position.NewSpan(0, 1),
			position.NewSpan(3, 5),
		) {
			collected = append(collected, idx)
		}

		assert.Equal(t, []int{0, 3, 4}, collected)
	})

	t.Run("span clamped to bounds", func(t *testing.T) {
		t.Parallel()

		var collected []int

		for idx := range source.Lines().All(position.NewSpan(-5, 100)) {
			collected = append(collected, idx)
		}

		assert.Equal(t, []int{0, 1, 2, 3, 4}, collected)
	})

	t.Run("empty span yields nothing", func(t *testing.T) {
		t.Parallel()

		var collected []int

		for idx := range source.Lines().All(position.NewSpan(2, 2)) {
			collected = append(collected, idx)
		}

		assert.Nil(t, collected)
	})

	t.Run("span beyond length yields nothing", func(t *testing.T) {
		t.Parallel()

		var collected []int

		for idx := range source.Lines().All(position.NewSpan(10, 20)) {
			collected = append(collected, idx)
		}

		assert.Nil(t, collected)
	})
}

func TestSource_Lines(t *testing.T) {
	t.Parallel()

	src := niceyaml.NewSourceFromString("key: value\nfoo: bar")
	lines := src.Lines()

	require.Equal(t, 2, lines.Len())
	assert.Equal(t, "key: value", lines.Line(0).Content())
	assert.Equal(t, "foo: bar", lines.Line(1).Content())
}

func TestSource_Tokens(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input string
	}{
		"mapping": {
			input: "key: value\nfoo: bar\n",
		},
		"block scalar": {
			input: "key: |\n  one\n  two\nfoo: bar\n",
		},
		"documents": {
			input: "a: 1\n---\nb: 2\n",
		},
		"empty": {
			input: "",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			src := niceyaml.NewSourceFromString(tc.input)

			first := src.Tokens()
			second := src.Tokens()

			require.Len(t, second, len(first))

			for i := range first {
				assert.Same(t, first[i], second[i], "token %d", i)
			}

			if len(first) == 0 {
				return
			}

			first[0] = nil

			third := src.Tokens()
			require.Len(t, third, len(second))

			for i := range second {
				assert.Same(t, second[i], third[i], "token %d", i)
			}
		})
	}
}

func TestSource_Lines_Whitespace(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input   string
		numbers []int
		want    position.Span
	}{
		"line break":          {input: "\n", numbers: []int{1}, want: position.NewSpan(0, 1)},
		"two line breaks":     {input: "\n\n", numbers: []int{1, 2}, want: position.NewSpan(0, 2)},
		"spaces":              {input: "  ", numbers: []int{1}, want: position.NewSpan(0, 1)},
		"spaces then a break": {input: "   \n", numbers: []int{1}, want: position.NewSpan(0, 1)},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			src := niceyaml.NewSourceFromString(tc.input)
			lines := src.Lines()

			require.Equal(t, len(tc.numbers), lines.Len())

			for i, want := range tc.numbers {
				assert.Equal(t, want, lines.Line(i).Number(), "line %d", i)
			}

			docs, err := src.Documents()
			require.NoError(t, err)
			require.Len(t, docs, 1)
			assert.Equal(t, tc.want, docs[0].Span())
		})
	}
}

func TestSource_Runes_EarlyBreak(t *testing.T) {
	t.Parallel()

	input := "abc\n"
	tks := lexer.Tokenize(input)
	lines := niceyaml.NewSourceFromTokens(tks)

	var collected []rune

	for _, r := range lines.Lines().Runes() {
		collected = append(collected, r)
		if r == 'b' {
			break
		}
	}

	assert.Equal(t, []rune{'a', 'b'}, collected)
}

func TestSource_Runes_WithRanges(t *testing.T) {
	t.Parallel()

	input := "a: 1\nb: 2\nc: 3\nd: 4\ne: 5\n"
	source := niceyaml.NewSourceFromString(input)

	require.Equal(t, 5, source.Lines().Len())

	t.Run("no ranges returns all runes", func(t *testing.T) {
		t.Parallel()

		var collected []runePosition

		for pos, r := range source.Lines().Runes() {
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

		for pos, r := range source.Lines().Runes(rng) {
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

		for pos, r := range source.Lines().Runes(rng) {
			collected = append(collected, runePosition{R: r, Pos: pos})
		}

		// Line 1: cols 2-4 (newline at 4), Line 2: cols 0-1.
		want := []runePosition{
			{R: ' ', Pos: position.New(1, 2)},
			{R: '2', Pos: position.New(1, 3)},
			{R: '\n', Pos: position.New(1, 4)},
			{R: 'c', Pos: position.New(2, 0)},
			{R: ':', Pos: position.New(2, 1)},
		}
		assert.Equal(t, want, collected)
	})

	t.Run("range clamped to bounds", func(t *testing.T) {
		t.Parallel()

		// Range that extends beyond source bounds.
		rng := position.NewRange(position.New(-5, 0), position.New(100, 100))

		var collected, all []runePosition

		for pos, r := range source.Lines().Runes(rng) {
			collected = append(collected, runePosition{R: r, Pos: pos})
		}

		for pos, r := range source.Lines().Runes() {
			all = append(all, runePosition{R: r, Pos: pos})
		}

		// Should return all runes since range encompasses everything.
		require.NotEmpty(t, all)
		assert.Equal(t, all, collected)
	})

	t.Run("empty source returns nothing", func(t *testing.T) {
		t.Parallel()

		emptySource := niceyaml.NewSourceFromString("")
		rng := position.NewRange(position.New(0, 0), position.New(0, 5))

		var collected []runePosition

		for pos, r := range emptySource.Lines().Runes(rng) {
			collected = append(collected, runePosition{R: r, Pos: pos})
		}

		assert.Nil(t, collected)
	})

	t.Run("out-of-range yields nothing", func(t *testing.T) {
		t.Parallel()

		// Range that's completely beyond the source.
		rng := position.NewRange(position.New(100, 0), position.New(100, 10))

		var collected []runePosition

		for pos, r := range source.Lines().Runes(rng) {
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

		for pos, r := range source.Lines().Runes(rng1, rng2) {
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

		var collected []rune

		for _, r := range source.Lines().Runes(rng) {
			collected = append(collected, r)
			if r == ':' {
				break
			}
		}

		// Should stop at first colon.
		assert.Equal(t, []rune{'a', ':'}, collected)
	})
}

func TestSource_Lines_TokenLookup(t *testing.T) {
	t.Parallel()

	input := stringtest.Input(`
		key: |
		  line1
		  line2
	`)
	source := niceyaml.NewSourceFromString(input)
	require.Equal(t, 3, source.Lines().Len())

	// The view resolves a position inside the block to the lexer's token and
	// reports one range per line the token occupies.
	tk := source.Lines().TokenAt(position.New(1, 0))
	require.NotNil(t, tk)

	ranges := source.Lines().TokenRanges(tk)
	require.Len(t, ranges, 2)
	assert.Equal(t, 1, ranges[0].Start.Line)
	assert.Equal(t, 2, ranges[1].Start.Line)

	content := source.Lines().ContentRanges(tk)
	assert.Equal(t, position.Ranges{
		position.NewRange(position.New(1, 2), position.New(1, 7)),
		position.NewRange(position.New(2, 2), position.New(2, 7)),
	}, content)
}

func TestSource_Lines_EscapedScalar(t *testing.T) {
	t.Parallel()

	// The lexer drops the code of an escape from a double-quoted scalar,
	// and the lines still hold the whole source, so the position of each
	// token leads back to it.
	input := "m: {a: \"\\u00e9\", b: xx, c: yy}\n"
	source := niceyaml.NewSourceFromString(input)

	assert.Equal(t, strings.TrimSuffix(input, "\n"), source.Lines().Content())

	for _, tk := range source.Tokens() {
		assert.Same(t, tk, source.Lines().TokenAt(position.NewFromToken(tk)), "token %q", tk.Origin)
	}
}

func TestSource_Lines_DroppedWhitespace(t *testing.T) {
	t.Parallel()

	// The lexer drops trailing spaces, the whitespace of a blank line, and
	// the space in front of a ":" after a quoted key. The lines still hold
	// the whole source, the position of each token leads back to it, and
	// a blank line with a tab before the first key still parses.
	tcs := map[string]struct {
		input string
	}{
		"blank line with a tab before the first key": {input: " \t\na: 1\n"},
		"blank line of a tab before the first key":   {input: "\t\na: 1\n"},
		"space before a colon after a quoted key":    {input: "{\"a\" : 1, \"b\" : \"x\"}\n"},
		"trailing spaces":                            {input: "a: 1   \nb: 2\n"},
		"blank line of spaces":                       {input: "a: 1\n   \nb: 2\n"},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			source := niceyaml.NewSourceFromString(tc.input)

			assert.Equal(t, strings.TrimSuffix(tc.input, "\n"), source.Lines().Content())

			for _, tk := range source.Tokens() {
				assert.Same(t, tk, source.Lines().TokenAt(position.NewFromToken(tk)), "token %q", tk.Origin)
			}

			_, err := source.Documents()
			require.NoError(t, err)
		})
	}
}

func TestSource_File_BlankLineBeforeFirstKey(t *testing.T) {
	t.Parallel()

	// The parser reads the first key without the spaces and tabs of the
	// blank line above it. The tree still holds a copy of the Source's
	// own first token, so the key finds its lines, a scope keeps it, and
	// a decode error binds to it.
	tcs := map[string]struct {
		input string
	}{
		"blank line of spaces":  {input: "  \na: 1\nb: 2\n"},
		"blank line with a tab": {input: " \t\na: 1\nb: 2\n"},
		"blank line of a tab":   {input: "\t\na: 1\nb: 2\n"},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			source := niceyaml.NewSourceFromString(tc.input)

			docs, err := source.Documents()
			require.NoError(t, err)
			require.Len(t, docs, 1)

			doc := docs[0]

			keyTk, err := paths.Current().Child("a").Key().Token(doc.DocumentAST())
			require.NoError(t, err)
			require.NotNil(t, keyTk)

			original := source.Lines().TokenAt(position.NewFromToken(keyTk))
			require.NotNil(t, original)
			assert.Equal(t, original.Origin, keyTk.Origin)
			assert.Equal(t, position.Ranges{
				position.NewRange(position.New(1, 0), position.New(1, 1)),
			}, source.Lines().ContentRanges(keyTk))

			assert.Equal(t,
				yamltest.DumpTokenOrigins(source.Tokens()),
				yamltest.DumpTokenOrigins(yamltest.At(t, doc, paths.Current()).Tokens()),
			)
			assert.Len(t, yamltest.At(t, doc, paths.Current().Child("a").Key()).Tokens(), 1)

			_, err = doc.Decode[struct{ B int }](t.Context(), niceyaml.WithDisallowUnknownFields(true))
			require.ErrorIs(t, err, niceyaml.ErrDecode)
		})
	}
}

func TestSource_File_BlankBlockScalarContent(t *testing.T) {
	t.Parallel()

	// The parser reads the blank content of a block scalar on the line
	// after the header, so a comment below the content goes to the next
	// key. The tree still holds each copy at the position of the Source's
	// own token, and the scalar's node holds its content.
	tcs := map[string]struct {
		input       string
		wantComment string
	}{
		"comment below the content": {input: "a: |+\n  \n# c\nb: 1\n", wantComment: "# c"},
		"key below the content":     {input: "a: |+\n  \nb: 1\n"},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			source := niceyaml.NewSourceFromString(tc.input)

			file, err := source.File()
			require.NoError(t, err)

			var copies token.Tokens

			for _, doc := range file.Docs {
				ast.Walk(tokenCollector(func(tk *token.Token) { copies = append(copies, tk) }), doc)
			}

			require.NotEmpty(t, copies)

			for _, tk := range copies {
				matches := slices.ContainsFunc(source.Tokens(), func(original *token.Token) bool {
					return len(yamltest.DiffTokenFields(original, tk)) == 0
				})
				assert.True(t, matches, "copy %s matches no token of the Source", yamltest.FormatToken(tk))
			}

			doc, err := source.Document()
			require.NoError(t, err)

			yamltest.RequireTokensEqual(t,
				source.Tokens()[2:4],
				yamltest.At(t, doc, paths.Current().Child("a")).Tokens(),
			)

			mapping, ok := file.Docs[0].Body.(*ast.MappingNode)
			require.True(t, ok)
			require.Len(t, mapping.Values, 2)

			var comment string

			if group := mapping.Values[1].GetComment(); group != nil {
				comment = group.String()
			}

			assert.Equal(t, tc.wantComment, comment)
		})
	}
}

func TestSource_File_CommentBelowAnchor(t *testing.T) {
	t.Parallel()

	// Below an anchor that directly follows a "-", or a key and its ":", on
	// its line, the go-yaml parser reads the first comment on a line of its
	// own. Left of that "-" or key, the comment makes the parser give the
	// anchor a null value. Anywhere else, it makes the parser give the
	// anchor the node below it. The tree leaves the comment out where that
	// is wrong and the parser reads the node correctly without it.
	tcs := map[string]struct {
		want  any
		input string
		err   string
		kept  bool
	}{
		"comment left of the key above the next entry of an outer sequence": {
			input: "- a: &x\n# c\n- 1\n",
			want:  []any{map[string]any{"a": nil}, uint64(1)},
			kept:  true,
		},
		"comment left of the key above the next key": {
			input: "a:\n  b: &x\n# c\n  d: 1\n",
			want:  map[string]any{"a": map[string]any{"b": nil, "d": uint64(1)}},
			kept:  true,
		},
		"comment left of the entry above a key of an outer mapping": {
			input: "a:\n  - &x\n# c\nb: 1\n",
			want:  map[string]any{"a": []any{nil}, "b": uint64(1)},
			kept:  true,
		},
		"comment left of the entry above a key in its column": {
			input: "p:\n  q:\n  - &x\n# c\n  n: 1\n",
			want:  map[string]any{"p": map[string]any{"q": []any{nil}, "n": uint64(1)}},
			kept:  true,
		},
		"comment left of the key above the value": {
			input: "a:\n  b: &x\n# c\n    d: 1\n",
			want:  map[string]any{"a": map[string]any{"b": map[string]any{"d": uint64(1)}}},
		},
		"comment left of the key above a sequence in the column of the key": {
			input: "a:\n  b: &x\n# c\n  - 1\n",
			want:  map[string]any{"a": map[string]any{"b": []any{uint64(1)}}},
		},
		"comment left of the entry above the value": {
			input: "- - &x\n# c\n    - 1\n",
			want:  []any{[]any{[]any{uint64(1)}}},
		},
		"comment in the column of the key above the value": {
			input: "a: &x\n# c\n  b: 1\n",
			want:  map[string]any{"a": map[string]any{"b": uint64(1)}},
			kept:  true,
		},
		"comment in the column of the key above a sequence in that column": {
			input: "a: &x\n# c\n- 1\n",
			want:  map[string]any{"a": []any{uint64(1)}},
			kept:  true,
		},
		"comment right of the entry above the value": {
			input: "- &x\n    # c\n  b: 1\n",
			want:  []any{map[string]any{"b": uint64(1)}},
			kept:  true,
		},
		"comment in the column of the key above the next key": {
			input: "a: &x\n# c\nb: 1\n",
			want:  map[string]any{"a": nil, "b": uint64(1)},
		},
		"comment in the column of the entry above the next entry": {
			input: "- &x\n# c\n- 1\n",
			want:  []any{nil, uint64(1)},
		},
		"comment right of the key above a key of an outer mapping": {
			input: "a:\n  b: &x\n    # c\nd: 1\n",
			want:  map[string]any{"a": map[string]any{"b": nil}, "d": uint64(1)},
		},
		"comment right of the entry above an entry of an outer sequence": {
			input: "- - &x\n    # c\n- 1\n",
			want:  []any{[]any{nil}, uint64(1)},
		},
		// An explicit key starts at its "?".
		"comment in the column of the ? above the next key": {
			input: "? a: &x\n# c\nb: 1\n",
			want:  map[string]any{"a": nil, "b": uint64(1)},
		},
		"comment between the ? and its key above the next key": {
			input: "p:\n  ? a: &x\n   # c\n  b: 1\n",
			want:  map[string]any{"p": map[string]any{"a": nil, "b": uint64(1)}},
		},
		"comment in the column of the ? above the next key of a sequence entry": {
			input: "- ? a: &x\n  # c\n  b: 1\n",
			want:  []any{map[string]any{"a": nil, "b": uint64(1)}},
		},
		"comment in the column of the ? above the value": {
			input: "? a: &x\n# c\n    b: 1\n",
			want:  map[string]any{"a": map[string]any{"b": uint64(1)}},
			kept:  true,
		},
		"comment right of the ? above a value left of its key": {
			input: "p:\n  ? a: &x\n    # c\n   b: 1\n",
			want:  map[string]any{"p": map[string]any{"a": map[string]any{"b": uint64(1)}}},
			kept:  true,
		},
		"comment left of the ? above the value": {
			input: "p:\n  ? a: &x\n# c\n   b: 1\n",
			want:  map[string]any{"p": map[string]any{"a": map[string]any{"b": uint64(1)}}},
		},
		// The parser reads any other anchor the same way with the comment
		// as without it.
		"comment below an anchor after a key whose ? is on the line above": {
			input: "? \n a: &x\n  # c\n b: 1\n",
			want:  map[string]any{"a": map[string]any{"b": uint64(1)}},
			kept:  true,
		},
		// The tree holds a null in place of a comment that closes the
		// document below the anchor.
		"closing comment below an anchor after a key whose ? is on the line above": {
			input: "  ? \n    a: &x\n# c\n---\n",
			want:  map[string]any{"a": nil},
		},
		"comment left of the key below an anchor after a tag": {
			input: "a:\n  b: !t &x\n# c\n    d: 1\n",
			want:  map[string]any{"a": map[string]any{"b": map[string]any{"d": uint64(1)}}},
			kept:  true,
		},
		"comment left of the key below an anchor on a line of its own": {
			input: "a:\n  b:\n    &x\n# c\n    d: 1\n",
			want:  map[string]any{"a": map[string]any{"b": map[string]any{"d": uint64(1)}}},
			kept:  true,
		},
		// Above a comment left of its key that closes the document, the
		// parser itself gives the anchor a null value. The comment stays in
		// the tree at or right of the first key of the root. Left of that
		// key, the tree leaves it out above a header, and the parser
		// rejects it at the end of the source.
		"closing comment left of the key": {
			input: "a:\n  b: &x\n# c\n",
			want:  map[string]any{"a": map[string]any{"b": nil}},
			kept:  true,
		},
		"closing comment left of the root above a header": {
			input: "  a: &x\n# c\n---\n",
			want:  map[string]any{"a": nil},
		},
		"closing comment left of the root": {
			input: "  a: &x\n# c\n",
			err:   "2:1: value is not allowed in this context",
		},
		// Above a header, the tree first leaves out the comments left of
		// the root, and the first comment that remains decides. Left of the
		// key of the anchor, it stays in the tree. In the column of that
		// key, the parser takes it as the value of the anchor, and the tree
		// holds a null in its place.
		"closing comment left of the key below one left of the root": {
			input: "  - a: &x\n# d\n  # c\n---\n",
			want:  []any{map[string]any{"a": nil}},
			kept:  true,
		},
		"closing comment in the column of the key below one left of the root": {
			input: "  a: &x\n# d\n  # c\n---\n",
			want:  map[string]any{"a": nil},
		},
		// At the end of the source, the tree leaves none of the comments
		// out first, so the parser rejects the one left of the root.
		"closing comments in and left of the column of the root": {
			input: "  a: &x\n# d\n  # c\n",
			err:   "2:1: value is not allowed in this context",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if tc.err != "" {
				_, err := niceyaml.NewSourceFromString(tc.input).File()
				require.EqualError(t, err, tc.err)

				return
			}

			dd := yamltest.FirstDocument(t, tc.input)

			got, err := dd.Decode[any](t.Context())
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)

			assert.Equal(t, tc.kept, strings.Contains(dd.DocumentAST().String(), "# c"))
		})
	}
}

func TestSource_File_CommentBelowAnchorWithNoName(t *testing.T) {
	t.Parallel()

	// The go-yaml parser takes the token after a "&" as the name of the
	// anchor, so it rejects a "&" with no name above a comment. The Source
	// keeps that comment in the parser input wherever it sits. Without the
	// comment, the parser would name the anchor after the token below it.
	tcs := map[string]struct {
		input string
		err   string
	}{
		"comment left of the key above a scalar": {
			input: "p:\n  a: &\n# c\n   1\n",
			err:   "3:1: unexpected scalar value type",
		},
		"comment left of the key above a scalar and the next key": {
			input: "p:\n  a: &\n# c\n   1\nz: 2\n",
			err:   "3:1: unexpected scalar value type",
		},
		"comment in the column of the key above a tagged scalar left of it": {
			input: "p:\n   q: 1\n   a: &\n   # c\n!t 1\n",
			err:   "4:4: unexpected scalar value type",
		},
		"comment left of the entry above a scalar and the next entry": {
			input: "- - &\n# c\n    1\n- 2\n",
			err:   "2:1: unexpected scalar value type",
		},
		"comment above the end of a flow sequence": {
			input: "[&\n# c\n]\n",
			err:   "2:1: unexpected scalar value type",
		},
		"comment above a colon": {
			input: "a: &\n# c\n: 1\n",
			err:   "1:4: mapping value is not allowed in this context",
		},
		"closing comment left of the root above a header": {
			input: "  a: &\n# c\n---\n",
			err:   "2:1: unexpected scalar value type",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, err := niceyaml.NewSourceFromString(tc.input).File()
			require.EqualError(t, err, tc.err)
		})
	}
}

// tokenCollector is an [ast.Visitor] that passes the token of each node it
// visits to its function.
type tokenCollector func(tk *token.Token)

func (c tokenCollector) Visit(node ast.Node) ast.Visitor {
	if tk := node.GetToken(); tk != nil {
		c(tk)
	}

	return c
}

func TestSource_Document_EscapeAfterTag(t *testing.T) {
	t.Parallel()

	// The key after the escaped scalar keeps its own line, so the parser
	// reads the mapping the source holds.
	_, err := niceyaml.NewSourceFromString("name: !!str \"Caf\\u00e9\"\nage: 3\n").Document()
	require.NoError(t, err)
}

func TestSource_File_TokensFindLines(t *testing.T) {
	t.Parallel()

	t.Run("a token from a node finds its ranges", func(t *testing.T) {
		t.Parallel()

		input := stringtest.Input(`
			key: |
			  line1
			  line2
			other: value
		`)
		source := niceyaml.NewSourceFromString(input)
		doc := yamltest.FirstDocument(t, input)

		// The parser holds copies of the Source's tokens, and a copy finds
		// the ranges the original does.
		tk, err := paths.Current().Child("other").Token(doc.DocumentAST())
		require.NoError(t, err)
		require.NotNil(t, tk)

		original := source.Lines().TokenAt(position.NewFromToken(tk))
		require.NotNil(t, original)
		assert.NotSame(t, original, tk)

		assert.Equal(t, position.Ranges{
			position.NewRange(position.New(3, 7), position.New(3, 12)),
		}, source.Lines().ContentRanges(tk))
		assert.Equal(t, source.Lines().TokenRanges(original), source.Lines().TokenRanges(tk))

		// A path to a key resolves to the key token, and a block scalar to
		// its indicator, each of which finds its own columns.
		keyTk, err := paths.Current().Child("other").Key().Token(doc.DocumentAST())
		require.NoError(t, err)
		assert.Equal(t, position.Ranges{
			position.NewRange(position.New(3, 0), position.New(3, 5)),
		}, source.Lines().ContentRanges(keyTk))

		blockTk, err := paths.Current().Child("key").Token(doc.DocumentAST())
		require.NoError(t, err)
		assert.Equal(t, position.Ranges{
			position.NewRange(position.New(0, 5), position.New(0, 6)),
		}, source.Lines().ContentRanges(blockTk))
	})

	t.Run("every token of the file finds its ranges", func(t *testing.T) {
		t.Parallel()

		data, err := os.ReadFile(filepath.Join("testdata", "full.yaml"))
		require.NoError(t, err)

		source := niceyaml.NewSourceFromString(string(data))

		file, err := source.File()
		require.NoError(t, err)

		lines := source.Lines()
		checked := 0

		for _, doc := range file.Docs {
			for _, node := range ast.Filter(ast.StringType, doc) {
				tk := node.GetToken()
				if tk == nil || strings.TrimSpace(tk.Origin) == "" {
					continue
				}

				assert.NotEmpty(t, lines.TokenRanges(tk), "token %q at %s", tk.Value, tk.Position)

				checked++
			}
		}

		assert.Positive(t, checked)
	})
}

func TestNewSourceFromTokens_LaterDocument(t *testing.T) {
	t.Parallel()

	// The second document's tokens start at line 2 of the stream, and the
	// Source renumbers them so its own text counts from line 1.
	full := niceyaml.NewSourceFromString("a: 1\n---\nb: 2\nc: 3\nd: 4\n")

	docs, err := full.Documents()
	require.NoError(t, err)
	require.Len(t, docs, 2)

	source := niceyaml.NewSourceFromTokens(docs[1].Tokens())
	view := source.Lines()
	require.Equal(t, 4, view.Len())
	assert.Equal(t, 1, view.Line(0).Number())
	assert.Equal(t, "---\nb: 2\nc: 3\nd: 4", view.Content())

	t.Run("the caller's tokens keep their positions", func(t *testing.T) {
		t.Parallel()

		assert.Equal(t, 2, docs[1].Tokens()[0].Position.Line)
	})

	t.Run("a token position is a view position", func(t *testing.T) {
		t.Parallel()

		for _, l := range view.All() {
			for _, tk := range l.SourceTokens() {
				assert.Same(t, tk, view.TokenAt(position.NewFromToken(tk)), "token %q", tk.Value)
			}
		}
	})

	t.Run("path error reports the renumbered line", func(t *testing.T) {
		t.Parallel()

		err := yamltest.Bind(t, source, niceyaml.NewError("bad b", niceyaml.AtPath(paths.Current().Child("b"))))
		assert.Equal(t, "2:4: $.b: bad b", err.Error())

		var bound *niceyaml.SourceError

		require.ErrorAs(t, err, &bound)

		rng, ok := bound.Range()
		require.True(t, ok)
		assert.Equal(t, position.NewRange(position.New(1, 3), position.New(1, 4)), rng)

		got := trimLines(render(bound))
		assert.Contains(t, got, "<genericError>2</genericError>")
		assert.NotContains(t, got, "<genericError>3</genericError>")
	})

	t.Run("token error reports the renumbered line", func(t *testing.T) {
		t.Parallel()

		tk := view.TokenAt(position.New(2, 0))
		require.NotNil(t, tk)

		err := yamltest.Bind(t, source, niceyaml.NewError("bad c", niceyaml.AtPosition(position.NewFromToken(tk))))
		assert.Equal(t, "3:1: bad c", err.Error())

		got := trimLines(render(err))
		assert.Contains(t, got, "<genericError>c</genericError>")
		assert.NotContains(t, got, "<genericError>b</genericError>")
	})

	t.Run("token from the whole file is out of range past the last line", func(t *testing.T) {
		t.Parallel()

		last := full.Lines().TokenAt(position.New(4, 0))
		require.NotNil(t, last)

		var bound *niceyaml.SourceError

		require.ErrorAs(
			t,
			yamltest.Bind(t, source, niceyaml.NewError("bad d", niceyaml.AtPosition(position.NewFromToken(last)))),
			&bound,
		)

		err := bound.Unresolved()
		require.ErrorIs(t, err, niceyaml.ErrOutOfRange)
		assert.Equal(t, "location outside source: line 5 not in lines 1-4", err.Error())
	})
}

func TestNewSourceFromTokens_WhitespaceDocument(t *testing.T) {
	t.Parallel()

	// A document of whitespace alone cut after "..." counts its lines from
	// 1, as a document with text does.
	tcs := map[string]struct {
		input string
		want  []int
	}{
		"between document end and header": {
			input: "a: 1\n...\n\t\n---\nb: 2\n",
			want:  []int{1, 2},
		},
		"after document end at the end": {
			input: "a: 1\n...\n\t\n",
			want:  []int{1, 2},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var doc token.Tokens

			for i, d := range tokens.SplitDocuments(tokens.Tokenize(tc.input)) {
				if i == 1 {
					doc = d
				}
			}

			require.NotEmpty(t, doc)

			lines := niceyaml.NewSourceFromTokens(doc).Lines()

			got := make([]int, 0, lines.Len())
			for _, l := range lines.All() {
				got = append(got, l.Number())
			}

			assert.Equal(t, tc.want, got)
		})
	}
}

func TestNewSourceFromBytes(t *testing.T) {
	t.Parallel()

	src := []byte("key: value")
	s := niceyaml.NewSourceFromBytes(src)
	assert.Equal(t, "key: value", s.Lines().Content())
}

func TestNewSourceFromString_ByteOrderMark(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input string
		want  map[string]any
		lines string
	}{
		"before a comment": {
			input: "\ufeff# yaml-language-server: $schema=foo.json\na: 1\n",
			want:  map[string]any{"a": uint64(1)},
			lines: "# yaml-language-server: $schema=foo.json\na: 1",
		},
		"before a key": {
			input: "\ufeffa: 1\n",
			want:  map[string]any{"a": uint64(1)},
			lines: "a: 1",
		},
		"before a document marker": {
			input: "\ufeff---\na: 1\n",
			want:  map[string]any{"a": uint64(1)},
			lines: "---\na: 1",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			source := niceyaml.NewSourceFromString(tc.input)

			got, err := source.Decode[map[string]any](t.Context())
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
			assert.Equal(t, tc.lines, source.Lines().Content())
		})
	}
}

func TestNewSourceFromString_ByteOrderMarkLaterDocument(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input string
		lines string
		err   string
	}{
		"after a document marker": {
			input: "a: 1\n---\n\ufeffb: 2\n",
			lines: "a: 1\n---\nb: 2",
			err:   "3:4: $.b: bad",
		},
		"before each document marker": {
			input: "\ufeff---\na: 1\n\ufeff---\nb: 2\n",
			lines: "---\na: 1\n---\nb: 2",
			err:   "4:4: $.b: bad",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			source := niceyaml.NewSourceFromString(tc.input)
			assert.Equal(t, tc.lines, source.Lines().Content())

			docs, err := source.Documents()
			require.NoError(t, err)
			require.Len(t, docs, 2)

			got, err := docs[1].Decode[map[string]any](t.Context())
			require.NoError(t, err)
			assert.Equal(t, map[string]any{"b": uint64(2)}, got)

			// The positions count in the text the Source holds, so an
			// error at the value of b points at it.
			err = docs[1].Bind(niceyaml.NewError("bad", niceyaml.AtPath(paths.Current().Child("b"))))
			assert.EqualError(t, err, tc.err)
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
			want: stringtest.JoinLF(
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
			want: stringtest.JoinLF(
				"parent:",
				"  child: value",
			),
		},
		"blank line before an explicit key": {
			input: "tags: [a, b]\n\n? key\n: value\n",
			want: stringtest.JoinLF(
				"tags: [a, b]",
				"",
				"? key",
				": value",
			),
		},
		"explicit key after a leading blank line": {
			input: "\n? key\n: value\n",
			want: stringtest.JoinLF(
				"",
				"? key",
				": value",
			),
		},
		"text after a block scalar header at the end": {
			input: "key: |abc",
			want:  "key: |abc",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			source := niceyaml.NewSourceFromString(tc.input)
			got := source.Lines().Content()

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
			input: stringtest.Input(`
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
			err := yamltest.ValidateLines(source.Lines())

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
				input: stringtest.Input(`
					parent:
					  child: value
				`),
				want: 1,
			},
			"multiple documents": {
				input: stringtest.Input(`
					---
					doc1: value1
					---
					doc2: value2
				`),
				want: 2,
			},
			"consecutive headers": {
				input: stringtest.Input(`
					---
					---
				`),
				want: 2,
			},
			"consecutive headers before more documents": {
				input: stringtest.Input(`
					---
					---
					kind: A
					---
					kind: B
				`),
				want: 3,
			},
			"consecutive headers after an end marker": {
				input: stringtest.Input(`
					a: 1
					...
					---
					---
					b: 2
				`),
				want: 3,
			},
			"consecutive headers after a directive": {
				// The parser puts the directive in a node of its own.
				input: stringtest.Input(`
					%YAML 1.2
					---
					---
					a: 1
				`),
				want: 3,
			},
			"comment between headers": {
				input: stringtest.Input(`
					a: 1
					---
					# c
					---
					b: 2
				`),
				want: 3,
			},
			"anchor with no value before a header": {
				input: stringtest.Input(`
					a: &x
					---
					b: 1
				`),
				want: 2,
			},
			"anchored entry with no value before a header": {
				input: stringtest.Input(`
					- &x
					---
				`),
				want: 2,
			},
			"tag directive for the secondary handle": {
				// The directive holds for the first document alone. Carried
				// into the second, it made the parser panic on the !!str
				// tag with no value. The parser puts the directive in a
				// node of its own.
				input: "%TAG !! tag:example.com,2000:app/\n---\n!!int 1 - 3\n---\n- !!str\n",
				want:  3,
			},
			"list": {
				input: stringtest.Input(`
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

	t.Run("an end marker between headers ends a document", func(t *testing.T) {
		t.Parallel()

		// The parser merges a header that an end marker closes into the
		// next document, which then holds two headers.
		tcs := map[string]struct {
			input string
			want  int
		}{
			"end marker between headers": {
				input: stringtest.Input(`
					---
					...
					---
					a: 1
				`),
				want: 2,
			},
			"end marker between headers with a comment": {
				input: stringtest.Input(`
					--- # c
					...
					---
				`),
				want: 2,
			},
			"end marker between headers after a document": {
				input: stringtest.Input(`
					a: 0
					---
					...
					---
					a: 1
				`),
				want: 3,
			},
			"consecutive headers before an end marker": {
				input: stringtest.Input(`
					---
					---
					...
					---
				`),
				want: 3,
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				source := niceyaml.NewSourceFromString(tc.input)
				file, err := source.File()
				require.NoError(t, err)
				assert.Len(t, file.Docs, tc.want)

				docs, err := source.Documents()
				require.NoError(t, err)
				assert.Len(t, docs, tc.want)
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

	t.Run("syntax error comes back bound to the source", func(t *testing.T) {
		t.Parallel()

		source := niceyaml.NewSourceFromString("a: b: c\n")

		_, err := source.File()
		require.Error(t, err)

		bound, ok := err.(*niceyaml.SourceError) //nolint:errorlint // The top-level value is the bound error.
		require.True(t, ok, "want *niceyaml.SourceError, got %T", err)
		assert.Same(t, source, bound.Source())

		excerpt, ok := bound.Excerpt(2)
		require.True(t, ok)

		detail := newXMLPrinter().Print(excerpt)
		assert.Contains(t, detail, "<genericError>b</genericError>", "the offending token is highlighted")

		// Documents forwards the same bound error.
		_, err = source.Documents()
		assert.Same(t, bound, err)
	})

	t.Run("parser panic comes back as an error on every call", func(t *testing.T) {
		t.Parallel()

		// The go-yaml parser dereferences the position of this token.
		tks := lexer.Tokenize("a: 1\nb: 2\n")
		tks[2].Position = nil

		source := niceyaml.NewSourceFromTokens(tks)

		file, err := source.File()
		require.ErrorIs(t, err, niceyaml.ErrSyntax)
		require.EqualError(t, err,
			"1:1: parser rejected the tokens: panic: runtime error: invalid memory address or nil pointer dereference")
		assert.Nil(t, file)

		var bound *niceyaml.SourceError

		require.ErrorAs(t, err, &bound)
		assert.Same(t, source, bound.Source())

		file, again := source.File()
		assert.Nil(t, file)
		assert.Same(t, err, again)

		docs, err := source.Documents()
		require.ErrorIs(t, err, niceyaml.ErrSyntax)
		require.Len(t, docs, 1)
		require.ErrorIs(t, docs[0].Err(), niceyaml.ErrSyntax)

		_, err = source.Document()
		require.ErrorIs(t, err, niceyaml.ErrSyntax)
	})

	t.Run("syntax error on a line the lexer dropped keeps its line", func(t *testing.T) {
		t.Parallel()

		// The lexer drops the text of the last line and leaves the empty
		// content of the block scalar on it. The line still exists, so
		// the error names it and the excerpt shows it.
		source := niceyaml.NewSourceFromString("k: |+\n!", niceyaml.WithName("f.yaml"))

		_, err := source.File()
		require.EqualError(t, err, "f.yaml:2:1: could not find multi-line content")

		bound, ok := err.(*niceyaml.SourceError) //nolint:errorlint // The top-level value is the bound error.
		require.True(t, ok, "want *niceyaml.SourceError, got %T", err)

		_, ok = bound.Excerpt(2)
		assert.True(t, ok)
	})

	t.Run("syntax error names a rejected tab by its picture", func(t *testing.T) {
		t.Parallel()

		// The go-yaml scanner spells the tab as a raw tab, which a
		// renderer lays out as four spaces. The message names the
		// character the excerpt shows instead.
		tcs := map[string]struct {
			input string
			err   string
			want  string
		}{
			"tab indents a key": {
				input: "a:\n\tb: 1\n",
				err:   "t.yaml:2:2: found character '\u2409' that cannot start any token",
				want: stringtest.JoinLF(
					"t.yaml:2:2: found character '\u2409' that cannot start any token",
					"",
					"   1 | a:",
					"   2 | \u2409b: 1",
					"     |  ^",
				),
			},
			"tab alone on a line": {
				input: "a:\n\t\nb: 1\n",
				err:   "t.yaml:3:1: found character '\u2409' that cannot start any token",
				want: stringtest.JoinLF(
					"t.yaml:3:1: found character '\u2409' that cannot start any token",
					"",
					"   2 | \u2409",
					"   3 | b: 1",
					"     | ^",
				),
			},
			"tab indents a sequence entry": {
				input: "a: 1\n\t- x\n",
				err:   "t.yaml:1:4: found character '\u2409' that cannot start any token",
				want: stringtest.JoinLF(
					"t.yaml:1:4: found character '\u2409' that cannot start any token",
					"",
					"   1 | a: 1",
					"     |    ^",
					"   2 | \u2409- x",
				),
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				_, err := niceyaml.NewSourceFromString(tc.input, niceyaml.WithName("t.yaml")).File()
				require.EqualError(t, err, tc.err)

				assert.Equal(t, tc.want, niceyaml.FormatError(err, 1))
			})
		}
	})
}

func TestSource_File_ErrSyntax(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input string
		opts  []niceyaml.SourceOption
		// Whether each document fails to parse, in file order.
		want []bool
	}{
		"unclosed flow sequence": {
			input: "a: [1\n",
			want:  []bool{true},
		},
		"duplicate key": {
			input: "a: 1\na: 2\n",
			want:  []bool{true},
		},
		"duplicate key the source allows": {
			input: "a: 1\na: 2\n",
			opts:  []niceyaml.SourceOption{niceyaml.WithAllowDuplicateKeys(true)},
			want:  []bool{false},
		},
		"tab indents a key": {
			input: "a:\n\tb: 1\n",
			want:  []bool{true},
		},
		"unknown escape": {
			input: "a: \"\\q\"\n",
			want:  []bool{true},
		},
		"second of three documents": {
			input: "a: 1\n---\nb: [\n---\nc: 3\n",
			want:  []bool{false, true, false},
		},
		"first and third of three documents": {
			input: "a: [\n---\nb: 2\n---\nc: @x\n",
			want:  []bool{true, false, true},
		},
		// The parser takes an alias with no anchor, and a decode rejects
		// it.
		"alias with no anchor": {
			input: "a: *x\n",
			want:  []bool{false},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			source := niceyaml.NewSourceFromString(tc.input, tc.opts...)

			docs, err := source.Documents()
			require.Len(t, docs, len(tc.want))

			for i, doc := range docs {
				if !tc.want[i] {
					require.NoError(t, doc.Err())

					_, decodeErr := doc.Decode[any](t.Context())
					require.NotErrorIs(t, decodeErr, niceyaml.ErrSyntax)

					continue
				}

				require.ErrorIs(t, doc.Err(), niceyaml.ErrSyntax)

				_, decodeErr := doc.Decode[any](t.Context())
				require.ErrorIs(t, decodeErr, niceyaml.ErrSyntax)

				// The go-yaml error stays in the chain.
				var syntaxErr *yaml.SyntaxError

				require.ErrorAs(t, doc.Err(), &syntaxErr)
			}

			file, fileErr := source.File()

			if !slices.Contains(tc.want, true) {
				require.NoError(t, err)
				require.NoError(t, fileErr)
				assert.NotNil(t, file)

				return
			}

			// One error or a join of several, which matches through each.
			require.ErrorIs(t, err, niceyaml.ErrSyntax)
			require.ErrorIs(t, fileErr, niceyaml.ErrSyntax)
			assert.Nil(t, file)

			_, err = source.Document()
			require.ErrorIs(t, err, niceyaml.ErrSyntax)

			_, err = source.Decode[any](t.Context())
			require.ErrorIs(t, err, niceyaml.ErrSyntax)
		})
	}
}

func TestSource_Documents_SyntaxError(t *testing.T) {
	t.Parallel()

	const unclosed = "sequence end token ']' not found"

	tcs := map[string]struct {
		input string
		// The syntax error of each document in file order, empty for a
		// document that parsed.
		want []string
		// The lines each document covers.
		spans []position.Span
	}{
		"only document": {
			input: "a: [\n",
			want:  []string{"f.yaml:1:4: " + unclosed},
			spans: []position.Span{position.NewSpan(0, 1)},
		},
		"first of two": {
			input: "a: [\n---\nb: 1\n",
			want:  []string{"f.yaml:1:4: " + unclosed, ""},
			spans: []position.Span{position.NewSpan(0, 1), position.NewSpan(1, 3)},
		},
		"last of three": {
			input: "a: 1\n---\nb: 2\n---\nc: [\n",
			want:  []string{"", "", "f.yaml:5:4: " + unclosed},
			spans: []position.Span{position.NewSpan(0, 1), position.NewSpan(1, 3), position.NewSpan(3, 5)},
		},
		"second and fourth of five": {
			input: "a: 1\n---\nb: [\n---\nc: 3\n---\nd: @x\n---\ne: 5\n",
			want: []string{
				"",
				"f.yaml:3:4: " + unclosed,
				"",
				"f.yaml:7:4: '@' is a reserved character",
				"",
			},
			spans: []position.Span{
				position.NewSpan(0, 1),
				position.NewSpan(1, 3),
				position.NewSpan(3, 5),
				position.NewSpan(5, 7),
				position.NewSpan(7, 9),
			},
		},
		"unclosed quote ends at the next header": {
			input: "a: \"x\n---\nb: 2\n",
			want:  []string{"f.yaml:1:4: found unexpected document separator", ""},
			spans: []position.Span{position.NewSpan(0, 1), position.NewSpan(1, 3)},
		},
		"document without a header after an end marker": {
			input: "a: 1\n...\nb: [\n...\nc: 3\n",
			want:  []string{"", "f.yaml:3:4: " + unclosed, ""},
			spans: []position.Span{position.NewSpan(0, 2), position.NewSpan(2, 4), position.NewSpan(4, 5)},
		},
		"comment and directive above the header": {
			input: "a: 1\n...\n# top\n%YAML 1.2\n---\nb: [\n---\nc: 3\n",
			want:  []string{"", "f.yaml:6:4: " + unclosed, ""},
			spans: []position.Span{position.NewSpan(0, 2), position.NewSpan(2, 6), position.NewSpan(6, 8)},
		},
		"directive that no document follows": {
			input: "a: 1\n...\n%YAML 1.2\n",
			want:  []string{"", "f.yaml:3:1: unexpected directive value. document not started"},
			spans: []position.Span{position.NewSpan(0, 2), position.NewSpan(2, 3)},
		},
		// The header parses together with the document above it, so the
		// error of either document fails both.
		"header after an anchor with no value": {
			input: "a: &x\n---\nb: [\n---\nc: 3\n",
			want:  []string{"f.yaml:3:4: " + unclosed, "f.yaml:3:4: " + unclosed, ""},
			spans: []position.Span{position.NewSpan(0, 1), position.NewSpan(1, 3), position.NewSpan(3, 5)},
		},
		"empty documents around the failure": {
			input: "---\n---\na: [\n---\n---\n",
			want:  []string{"", "f.yaml:3:4: " + unclosed, "", ""},
			spans: []position.Span{
				position.NewSpan(0, 1),
				position.NewSpan(1, 3),
				position.NewSpan(3, 4),
				position.NewSpan(4, 5),
			},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			source := niceyaml.NewSourceFromString(tc.input, niceyaml.WithName("f.yaml"))

			docs, err := source.Documents()
			require.Error(t, err)
			require.Len(t, docs, len(tc.want))

			// The syntax errors in file order. The two documents of one
			// run share its error.
			var failed []error

			for i, doc := range docs {
				assert.Equal(t, i, doc.DocumentIndex())
				assert.Equal(t, tc.spans[i], doc.Span())

				if tc.want[i] == "" {
					require.NoError(t, doc.Err())

					_, decodeErr := doc.Decode[any](t.Context())
					require.NoError(t, decodeErr)

					continue
				}

				require.EqualError(t, doc.Err(), tc.want[i])

				bound, ok := doc.Err().(*niceyaml.SourceError) //nolint:errorlint // The value itself is the bound error.
				require.True(t, ok, "want *niceyaml.SourceError, got %T", doc.Err())
				assert.Same(t, source, bound.Source())

				if !slices.Contains(failed, doc.Err()) {
					failed = append(failed, doc.Err())
				}
			}

			// One syntax error comes back as it is, and several come back
			// joined in file order.
			if len(failed) == 1 {
				assert.Same(t, failed[0], err)
			} else {
				joined, ok := err.(interface{ Unwrap() []error }) //nolint:errorlint // The value itself is the join.
				require.True(t, ok, "want a join, got %T", err)
				require.Len(t, joined.Unwrap(), len(failed))

				for i, branch := range joined.Unwrap() {
					assert.Same(t, failed[i], branch)
				}
			}

			// The methods that need the whole file to parse return the
			// same error and nothing beside it.
			file, fileErr := source.File()
			assert.Nil(t, file)
			assert.Equal(t, err, fileErr)

			doc, docErr := source.Document()
			assert.Nil(t, doc)
			assert.Equal(t, err, docErr)

			_, decodeErr := source.Decode[any](t.Context())
			assert.Equal(t, err, decodeErr)

			again, againErr := source.Documents()
			assert.Equal(t, err, againErr)
			assert.Equal(t, docs, again)
		})
	}
}

func TestSource_Documents_SyntaxErrorTokens(t *testing.T) {
	t.Parallel()

	// A document that did not parse keeps the tokens a document in its
	// place has when the file parses, and so do the documents around it.
	tcs := map[string]struct {
		input string
		// The text of each document, as its lines hold it.
		want []string
		// The values of the tokens in the preamble of each document.
		preambles [][]string
	}{
		"comment above the header": {
			input: "a: 1\n# lifted\n---\nb: [\n---\nc: 3\n",
			want:  []string{"a: 1", "# lifted\n---\nb: [", "---\nc: 3"},
			preambles: [][]string{
				nil,
				{" lifted", "---"},
				{"---"},
			},
		},
		"comments around an end marker": {
			input: "a: 1\n... # same\n# below\n---\nb: [\n...\n# after\n",
			want:  []string{"a: 1\n... # same", "# below\n---\nb: [\n...\n# after"},
			preambles: [][]string{
				nil,
				{" below", "---"},
			},
		},
		"directive above the header": {
			input: "%YAML 1.2\n---\na: [\n---\nb: 1\n",
			want:  []string{"%YAML 1.2\n---\na: [", "---\nb: 1"},
			preambles: [][]string{
				{"%", "YAML", "1.2", "---"},
				{"---"},
			},
		},
		"comment-only document after the failure": {
			input: "# top\n---\na: [\n---\n# mid\n---\nb: 1\n",
			want:  []string{"# top\n---\na: [", "---\n# mid", "---\nb: 1"},
			preambles: [][]string{
				{" top", "---"},
				{"---", " mid"},
				{"---"},
			},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			source := niceyaml.NewSourceFromString(tc.input)

			docs, err := source.Documents()
			require.Error(t, err)
			require.Len(t, docs, len(tc.want))

			var all token.Tokens

			for i, doc := range docs {
				assert.Equal(t, tc.want[i], doc.View().Held().Content())

				var preamble []string

				for _, tk := range doc.Preamble() {
					preamble = append(preamble, tk.Value)
				}

				assert.Equal(t, tc.preambles[i], preamble)

				all = append(all, doc.Tokens()...)
			}

			// The documents share out every token of the source, in order.
			yamltest.RequireTokensEqual(t, source.Tokens(), all)
		})
	}
}

// requireName is a [niceyaml.Validator] that rejects a document without a
// name key at the root of the document, and counts its calls in calls when
// calls is not nil.
func requireName(calls *atomic.Int32) niceyaml.Validator {
	return niceyaml.ValidatorFunc(func(_ context.Context, n *niceyaml.Node) error {
		if calls != nil {
			calls.Add(1)
		}

		_, err := n.At(paths.Current().Child("name"))
		if err != nil {
			return niceyaml.NewError("name is required", niceyaml.AtPath(paths.Current()))
		}

		return nil
	})
}

// bindingMessages returns the message of each binding in the tree of err,
// in order.
func bindingMessages(err error) []string {
	var msgs []string

	for b := range niceyaml.Bindings(err) {
		msgs = append(msgs, b.Error())
	}

	return msgs
}

func TestSource_ValidateDocuments(t *testing.T) {
	t.Parallel()

	const unclosed = "sequence end token ']' not found"

	tcs := map[string]struct {
		input string
		// The message of each binding the result holds, in file order.
		want []string
	}{
		"every document valid": {
			input: "name: a\n---\nname: b\n",
		},
		"one document": {
			input: "value: 1\n",
			want:  []string{"f.yaml:1:1: $: name is required"},
		},
		"first and last document invalid": {
			input: "value: 1\n---\nname: b\n---\nvalue: 3\n",
			want:  []string{"f.yaml:1:1: $: name is required", "f.yaml:5:1: $: name is required"},
		},
		"syntax error beside violations": {
			input: "value: 1\n---\nname: [\n---\nvalue: 3\n",
			want: []string{
				"f.yaml:1:1: $: name is required",
				"f.yaml:3:7: " + unclosed,
				"f.yaml:5:1: $: name is required",
			},
		},
		"two syntax errors": {
			input: "a: [\n---\nname: b\n---\nc: [\n",
			want:  []string{"f.yaml:1:4: " + unclosed, "f.yaml:5:4: " + unclosed},
		},
		"syntax errors of adjacent documents": {
			input: "a: [\n---\nb: [\n",
			want:  []string{"f.yaml:1:4: " + unclosed, "f.yaml:3:4: " + unclosed},
		},
		// The header parses together with the document above it, so both
		// documents carry the one error.
		"syntax error two documents share": {
			input: "name: &x\n---\nname: [\n---\nvalue: 5\n",
			want:  []string{"f.yaml:3:7: " + unclosed, "f.yaml:5:1: $: name is required"},
		},
		"explicit empty document": {
			input: "name: x\n---\n",
			want:  []string{"f.yaml:2:1: $: name is required"},
		},
		"empty file": {
			input: "",
			want:  []string{"f.yaml: $: name is required"},
		},
		"stream of markers alone": {
			input: "...\n",
			want:  []string{"f.yaml: $: name is required"},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			source := niceyaml.NewSourceFromString(tc.input, niceyaml.WithName("f.yaml"))

			var calls atomic.Int32

			err := source.ValidateDocuments(t.Context(), requireName(&calls))
			if len(tc.want) == 0 {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}

			assert.Equal(t, tc.want, bindingMessages(err))

			for b := range niceyaml.Bindings(err) {
				assert.Same(t, source, b.Source())
			}

			// Every document that parsed runs the validator.
			docs, _ := source.Documents() //nolint:errcheck // The documents come back with the error.

			var parsed int32

			for _, doc := range docs {
				if doc.Err() == nil {
					parsed++
				}
			}

			assert.Equal(t, parsed, calls.Load())
		})
	}

	t.Run("no validators returns the error File returns", func(t *testing.T) {
		t.Parallel()

		for _, input := range []string{
			"name: a\n---\nname: b\n",
			"a: [\n",
			"a: [\n---\nb: 1\n---\nc: [\n",
			"a: &x\n---\nb: [\n",
			"a: &x\n---\nb: [\n---\nc: 1\n---\nd: @x\n",
			"...\n",
		} {
			source := niceyaml.NewSourceFromString(input, niceyaml.WithName("f.yaml"))

			_, fileErr := source.File()

			err := source.ValidateDocuments(t.Context())
			assert.Equal(t, fileErr, err, input)
		}
	})

	t.Run("syntax errors match ErrSyntax", func(t *testing.T) {
		t.Parallel()

		source := niceyaml.NewSourceFromString("value: 1\n---\nname: [\n", niceyaml.WithName("f.yaml"))

		err := source.ValidateDocuments(t.Context(), requireName(nil))
		require.ErrorIs(t, err, niceyaml.ErrSyntax)
	})
}

func TestSource_ValidateDocuments_Context(t *testing.T) {
	t.Parallel()

	const input = "value: 1\n---\nvalue: 2\n---\nvalue: 3\n---\nvalue: 4\n"

	t.Run("ended before the call", func(t *testing.T) {
		t.Parallel()

		ctx, cancel := context.WithCancel(t.Context())
		cancel()

		source := niceyaml.NewSourceFromString(input, niceyaml.WithName("f.yaml"))

		var calls atomic.Int32

		err := source.ValidateDocuments(ctx, requireName(&calls))
		require.ErrorIs(t, err, context.Canceled)
		assert.Equal(t, int32(0), calls.Load())
		assert.Equal(t, []string{"f.yaml: context canceled"}, bindingMessages(err))

		var bound *niceyaml.SourceError

		require.ErrorAs(t, err, &bound)
		assert.Same(t, source, bound.Source())

		// With no validators, the ended ctx still fails the call.
		err = source.ValidateDocuments(ctx)
		require.ErrorIs(t, err, context.Canceled)
	})

	t.Run("ended by a validator that reports it", func(t *testing.T) {
		t.Parallel()

		ctx, cancel := context.WithCancel(t.Context())
		t.Cleanup(cancel)

		var calls atomic.Int32

		v := niceyaml.ValidatorFunc(func(ctx context.Context, _ *niceyaml.Node) error {
			if calls.Add(1) == 2 {
				cancel()

				return ctx.Err()
			}

			return niceyaml.NewError("bad", niceyaml.AtPath(paths.Current()))
		})

		source := niceyaml.NewSourceFromString(input, niceyaml.WithName("f.yaml"))

		err := source.ValidateDocuments(ctx, v)
		require.ErrorIs(t, err, context.Canceled)
		assert.Equal(t, int32(2), calls.Load())
		assert.Equal(t, []string{"f.yaml:1:1: $: bad", "f.yaml: document 2: context canceled"}, bindingMessages(err))
		assert.Equal(t, 1, strings.Count(err.Error(), context.Canceled.Error()), err.Error())
	})

	t.Run("ended by a validator that passes", func(t *testing.T) {
		t.Parallel()

		ctx, cancel := context.WithCancel(t.Context())
		t.Cleanup(cancel)

		var calls atomic.Int32

		v := niceyaml.ValidatorFunc(func(context.Context, *niceyaml.Node) error {
			if calls.Add(1) == 2 {
				cancel()

				return nil
			}

			return niceyaml.NewError("bad", niceyaml.AtPath(paths.Current()))
		})

		source := niceyaml.NewSourceFromString(input, niceyaml.WithName("f.yaml"))

		err := source.ValidateDocuments(ctx, v)
		require.ErrorIs(t, err, context.Canceled)
		assert.Equal(t, int32(2), calls.Load())
		assert.Equal(t, []string{"f.yaml:1:1: $: bad", "f.yaml: context canceled"}, bindingMessages(err))
	})

	t.Run("a deadline of a validator's own fails its document alone", func(t *testing.T) {
		t.Parallel()

		var calls atomic.Int32

		v := niceyaml.ValidatorFunc(func(ctx context.Context, _ *niceyaml.Node) error {
			calls.Add(1)

			short, cancel := context.WithTimeout(ctx, 0)
			defer cancel()

			<-short.Done()

			return short.Err()
		})

		source := niceyaml.NewSourceFromString(input, niceyaml.WithName("f.yaml"))

		err := source.ValidateDocuments(t.Context(), v)
		require.ErrorIs(t, err, context.DeadlineExceeded)
		require.NotErrorIs(t, err, context.Canceled)
		assert.Equal(t, int32(4), calls.Load())
		assert.Len(t, bindingMessages(err), 4)
	})
}

func TestSource_WithYAMLParserOptions(t *testing.T) {
	t.Parallel()

	input := stringtest.Input(`
		name: first
		name: second
	`)

	tcs := map[string]struct {
		opts []niceyaml.SourceOption
		err  string
	}{
		"without options the parser rejects duplicate keys": {
			opts: []niceyaml.SourceOption{niceyaml.WithYAMLParserOptions()},
			err:  `mapping key "name" already defined`,
		},
		"forwards parser options": {
			opts: []niceyaml.SourceOption{
				niceyaml.WithYAMLParserOptions(parser.AllowDuplicateMapKey()),
			},
		},
		"a later call keeps earlier options": {
			opts: []niceyaml.SourceOption{
				niceyaml.WithYAMLParserOptions(parser.AllowDuplicateMapKey()),
				niceyaml.WithYAMLParserOptions(),
			},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			file, err := niceyaml.NewSourceFromString(input, tc.opts...).File()
			if tc.err != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.err)

				return
			}

			require.NoError(t, err)
			assert.Len(t, file.Docs, 1)
		})
	}
}

func TestSource_View_IndependentViews(t *testing.T) {
	t.Parallel()

	source := niceyaml.NewSourceFromString("key: value\n")

	first := source.View()
	first.AddOverlay("test1", position.NewRange(position.New(0, 0), position.New(0, 5)))
	first.Annotate(0, line.Annotation{Content: "note", Placement: line.Below})

	// A second view starts from the pristine document.
	second := source.View()
	assert.Empty(t, second.Overlays(0))
	assert.Empty(t, second.Annotations(0))

	// The first view keeps its overlay and annotation.
	require.Len(t, first.Overlays(0), 1)
	assert.Equal(t, kind.Kind("test1"), first.Overlays(0)[0].Kind)
	assert.Equal(t, "key: value", first.Lines().Content())
}

func TestSource_Name(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		name     string
		filePath string
		want     string
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
		"file path without name": {
			filePath: "/path/to/file.yaml",
			want:     "/path/to/file.yaml",
		},
		"name wins over file path": {
			name:     "custom",
			filePath: "/path/to/file.yaml",
			want:     "custom",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			source := niceyaml.NewSourceFromString("key: value",
				niceyaml.WithName(tc.name),
				niceyaml.WithFilePath(tc.filePath),
			)
			got := source.Name()

			assert.Equal(t, tc.want, got)
		})
	}
}

func TestSource_NilReceiver(t *testing.T) {
	t.Parallel()

	// A report reads the Source of each bound error, and a nil SourceError
	// returns a nil Source. Each read answers as an empty Source does.
	tcs := map[string]struct {
		read func(s *niceyaml.Source) any
		want any
	}{
		"name": {
			read: func(s *niceyaml.Source) any { return s.Name() },
			want: "",
		},
		"file path": {
			read: func(s *niceyaml.Source) any { return s.FilePath() },
			want: "",
		},
		"tokens": {
			read: func(s *niceyaml.Source) any { return s.Tokens() },
			want: token.Tokens(nil),
		},
		"lines": {
			read: func(s *niceyaml.Source) any { return s.Lines().Len() },
			want: 0,
		},
		"view": {
			read: func(s *niceyaml.Source) any { return s.View().Count() },
			want: 0,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var bound *niceyaml.SourceError

			source := bound.Source()
			require.Nil(t, source)

			assert.Equal(t, tc.want, tc.read(source))
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
			input: stringtest.Input(`
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
			got := source.Lines().Len()

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
			got := source.Lines().IsEmpty()

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

			got := source.Lines().TokenAt(tc.pos)

			if tc.wantNil {
				assert.Nil(t, got)
			} else {
				require.NotNil(t, got)
				assert.Equal(t, tc.wantValue, got.Value)
			}
		})
	}
}

func TestDocument_BindChain(t *testing.T) {
	t.Parallel()

	t.Run("wraps niceyaml.Error", func(t *testing.T) {
		t.Parallel()

		source := niceyaml.NewSourceFromString("key: value\n")
		yamlErr := niceyaml.NewError("test error")

		wrapped := yamltest.Bind(t, source, yamlErr)

		require.Error(t, wrapped)

		var gotErr *niceyaml.Error

		require.ErrorAs(t, wrapped, &gotErr)
	})

	t.Run("returns nil for nil error", func(t *testing.T) {
		t.Parallel()

		source := niceyaml.NewSourceFromString("key: value\n")
		wrapped := yamltest.Bind(t, source, nil)

		assert.NoError(t, wrapped)
	})

	t.Run("keeps context wrapped around the Error", func(t *testing.T) {
		t.Parallel()

		source := niceyaml.NewSourceFromString("key: value\n")
		yamlErr := niceyaml.NewError("test error")
		outer := fmt.Errorf("document 3: %w", yamlErr)

		wrapped := yamltest.Bind(t, source, outer)

		require.ErrorIs(t, wrapped, outer)
		assert.Contains(t, wrapped.Error(), "document 3: ")
	})

	t.Run("binds a non-Error and names the source", func(t *testing.T) {
		t.Parallel()

		stdErr := errors.New("standard error")

		tcs := map[string]struct {
			opts []niceyaml.SourceOption
			want string
		}{
			"no name leaves the message as it is": {
				want: "standard error",
			},
			"name goes in front of the message": {
				opts: []niceyaml.SourceOption{niceyaml.WithName("config")},
				want: "config: standard error",
			},
			"file path names the source": {
				opts: []niceyaml.SourceOption{niceyaml.WithFilePath("dir/config.yaml")},
				want: "dir/config.yaml: standard error",
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				source := niceyaml.NewSourceFromString("key: value\n", tc.opts...)

				wrapped := yamltest.Bind(t, source, stdErr)
				require.ErrorIs(t, wrapped, stdErr)

				var bound *niceyaml.SourceError

				require.ErrorAs(t, wrapped, &bound)
				assert.Same(t, source, bound.Source())
				assert.Equal(t, tc.want, wrapped.Error())
				assert.Equal(t, tc.want, fmt.Sprintf("%+v", wrapped), "no location, so no excerpt")
				assert.Equal(t, "document 0: "+tc.want, fmt.Errorf("document 0: %w", wrapped).Error())

				_, resolved := bound.Range()
				require.False(t, resolved)
				require.NoError(t, bound.Unresolved())
			})
		}
	})

	t.Run("returns a nil Error as a nil error", func(t *testing.T) {
		t.Parallel()

		source := niceyaml.NewSourceFromString("key: value\n")

		var nilErr *niceyaml.Error

		require.NoError(t, yamltest.Bind(t, source, nilErr))
	})

	t.Run("binds context around a nil Error without a location", func(t *testing.T) {
		t.Parallel()

		source := niceyaml.NewSourceFromString("key: value\n", niceyaml.WithName("config"))

		var nilErr *niceyaml.Error

		outer := fmt.Errorf("document 3: %w", nilErr)

		wrapped := yamltest.Bind(t, source, outer)

		// A nil Error has an empty message, so the wrapper's text ends at
		// its own prefix.
		require.ErrorIs(t, wrapped, outer)
		assert.Equal(t, "config: document 3: ", wrapped.Error())
		assert.Equal(t, "config: document 3: ", fmt.Sprintf("%+v", wrapped))
	})

	t.Run("looks past a nil SourceError in the chain", func(t *testing.T) {
		t.Parallel()

		source := niceyaml.NewSourceFromString("name: value\n")
		pathErr := niceyaml.NewError("bad name", niceyaml.AtPath(paths.Current().Child("name")))

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
				// source with the Error as its only child, and the path
				// resolves there.
				wrapped := yamltest.Bind(t, source, err)

				var bound *niceyaml.SourceError

				require.ErrorAs(t, wrapped, &bound)
				assert.Same(t, source, bound.Source())
				require.ErrorIs(t, wrapped, pathErr)
				require.Len(t, bound.Errors(), 1)

				rng, ok := bound.Errors()[0].Range()
				require.True(t, ok)
				assert.Equal(t, position.NewRange(position.New(0, 6), position.New(0, 11)), rng)

				docs, docsErr := source.Documents()
				require.NoError(t, docsErr)

				require.ErrorAs(t, docs[0].Bind(err), &bound)
				assert.Same(t, source, bound.Source())
			})
		}
	})

	t.Run("returns an error bound to the same source unchanged", func(t *testing.T) {
		t.Parallel()

		source := niceyaml.NewSourceFromString("name: value\n")
		namePath := paths.Current().Child("name")

		once := yamltest.Bind(t, source, niceyaml.NewError("bad name", niceyaml.AtPath(namePath)))
		assert.Same(t, once, yamltest.Bind(t, source, once))

		outer := fmt.Errorf("document 0: %w", once)
		assert.Same(t, outer, yamltest.Bind(t, source, outer))
	})

	t.Run("leaves an error bound to another source as it is", func(t *testing.T) {
		t.Parallel()

		first := niceyaml.NewSourceFromString("name: value\n")
		second := niceyaml.NewSourceFromString("# comment\nname: value\n", niceyaml.WithName("second"))
		namePath := paths.Current().Child("name")

		once := yamltest.Bind(t, first, niceyaml.NewError("bad name", niceyaml.AtPath(namePath)))
		twice := yamltest.Bind(t, second, once)

		// The error is bound already, so the second binding neither moves
		// it nor adds its name.
		require.Same(t, once, twice)

		var bound *niceyaml.SourceError

		require.ErrorAs(t, twice, &bound)
		assert.Same(t, first, bound.Source())
		assert.Equal(t, "1:7: $.name: bad name", twice.Error())
	})
}

func TestSource_View_IsIndependent(t *testing.T) {
	t.Parallel()

	source := niceyaml.NewSourceFromString("key: value\n")

	// A change to one view reaches neither the Source nor another view.
	view := source.View()
	for i := range view.All() {
		view.Annotate(i, line.Annotation{Content: "note", Placement: line.Below})
		view.AddLineOverlay(i, line.Overlay{Cols: position.NewSpan(0, 3), Kind: kind.GenericError})
	}

	assert.NotEmpty(t, view.Annotations(0))
	assert.Empty(t, source.View().Annotations(0))
	assert.Empty(t, source.View().Overlays(0))

	// A fresh view renders the pristine document.
	plain := printer.New(
		printer.WithStyles(style.Styles{}),
		printer.WithContainerStyle(lipgloss.NewStyle()),
		printer.WithGutter(printer.NoGutter),
	)
	assert.Equal(t, "key: value", plain.Print(source.View()))
}

func TestSource_Bind(t *testing.T) {
	t.Parallel()

	// Two documents, so Source.Document would refuse to pick one.
	source := niceyaml.NewSourceFromString("a: 1\n---\nb: 22\n", niceyaml.WithName("two.yaml"))

	t.Run("range error binds to the document its line falls in", func(t *testing.T) {
		t.Parallel()

		docs, err := source.Documents()
		require.NoError(t, err)

		rng := position.NewRange(position.New(2, 3), position.New(2, 5))
		err = source.Bind(niceyaml.NewError("too wide", niceyaml.AtRange(rng)))

		var bound *niceyaml.SourceError

		require.ErrorAs(t, err, &bound)
		assert.Same(t, source, bound.Source())
		assert.Same(t, docs[1], bound.Document())
		assert.Equal(t, "two.yaml:3:4: too wide", err.Error())

		got, ok := bound.Range()
		require.True(t, ok)
		assert.Equal(t, rng, got)

		assert.Equal(t, stringtest.JoinLF(
			"two.yaml:3:4: too wide",
			"",
			"   1 | a: 1",
			"   2 | ---",
			"   3 | b: 22",
			"     |    ^^",
		), fmt.Sprintf("%+v", err))
	})

	t.Run("position error resolves to its token", func(t *testing.T) {
		t.Parallel()

		docs, err := source.Documents()
		require.NoError(t, err)

		err = source.Bind(niceyaml.NewError("bad value", niceyaml.AtPosition(position.New(2, 3))))

		var bound *niceyaml.SourceError

		require.ErrorAs(t, err, &bound)
		assert.Same(t, docs[1], bound.Document())

		got, ok := bound.Range()
		require.True(t, ok)
		assert.Equal(t, position.NewRange(position.New(2, 3), position.New(2, 5)), got)
	})

	t.Run("position in the first document binds there", func(t *testing.T) {
		t.Parallel()

		docs, err := source.Documents()
		require.NoError(t, err)

		err = source.Bind(niceyaml.NewError("bad value", niceyaml.AtPosition(position.New(0, 3))))

		var bound *niceyaml.SourceError

		require.ErrorAs(t, err, &bound)
		assert.Same(t, docs[0], bound.Document())
		assert.Equal(t, "two.yaml:1:4: bad value", err.Error())
	})

	t.Run("position outside the source binds to no document", func(t *testing.T) {
		t.Parallel()

		err := source.Bind(niceyaml.NewError("far", niceyaml.AtPosition(position.New(9, 0))))

		var bound *niceyaml.SourceError

		require.ErrorAs(t, err, &bound)
		assert.Nil(t, bound.Document())

		rangeErr := bound.Unresolved()
		require.ErrorIs(t, rangeErr, niceyaml.ErrOutOfRange)
	})

	t.Run("column before the first binds to the document holding the line", func(t *testing.T) {
		t.Parallel()

		docs, err := source.Documents()
		require.NoError(t, err)

		err = source.Bind(niceyaml.NewError("far", niceyaml.AtPosition(position.New(2, -3))))

		var bound *niceyaml.SourceError

		require.ErrorAs(t, err, &bound)
		assert.Equal(t, "two.yaml: document 2: far", err.Error())
		assert.Same(t, docs[1], bound.Document())

		rangeErr := bound.Unresolved()
		require.ErrorIs(t, rangeErr, niceyaml.ErrOutOfRange)

		_, ok := bound.Range()
		assert.False(t, ok)
	})

	t.Run("position in a source that does not parse binds to none", func(t *testing.T) {
		t.Parallel()

		broken := niceyaml.NewSourceFromString("a: [\n", niceyaml.WithName("broken.yaml"))
		err := broken.Bind(niceyaml.NewError("bad", niceyaml.AtPosition(position.New(0, 0))))

		var bound *niceyaml.SourceError

		require.ErrorAs(t, err, &bound)
		assert.Nil(t, bound.Document())
		assert.Equal(t, "broken.yaml:1:1: bad", err.Error())
	})

	t.Run("path error resolves in the one document", func(t *testing.T) {
		t.Parallel()

		// A path resolves in the document Source.Document returns, so a
		// caller holding a configuration file binds through the source.
		one := niceyaml.NewSourceFromString("# header\na: 1\nb: 22\n", niceyaml.WithName("one.yaml"))
		doc, err := one.Document()
		require.NoError(t, err)

		err = one.Bind(niceyaml.NewError("bad", niceyaml.AtPath(paths.Current().Child("b"))))

		var bound *niceyaml.SourceError

		require.ErrorAs(t, err, &bound)
		assert.Equal(t, "one.yaml:3:4: $.b: bad", err.Error())
		assert.Same(t, doc, bound.Document())

		got, ok := bound.Range()
		require.True(t, ok)
		assert.Equal(t, position.NewRange(position.New(2, 3), position.New(2, 5)), got)

		assert.Equal(t, stringtest.JoinLF(
			"one.yaml:3:4: $.b: bad",
			"",
			"   1 | # header",
			"   2 | a: 1",
			"   3 | b: 22",
			"     |    ^^",
		), fmt.Sprintf("%+v", err))
	})

	t.Run("missing path binds to the one document", func(t *testing.T) {
		t.Parallel()

		one := niceyaml.NewSourceFromString("a: 1\nb: 2\n", niceyaml.WithName("one.yaml"))
		doc, err := one.Document()
		require.NoError(t, err)

		err = one.Bind(niceyaml.NewError("bad", niceyaml.AtPath(paths.Current().Child("zz").Index(0))))

		var bound *niceyaml.SourceError

		require.ErrorAs(t, err, &bound)
		assert.Equal(t, "one.yaml: $.zz[0]: bad", err.Error())
		assert.Same(t, doc, bound.Document())

		rangeErr := bound.Unresolved()
		require.ErrorIs(t, rangeErr, paths.ErrNotFound)

		_, ok := bound.Range()
		assert.False(t, ok)
	})

	t.Run("path error after a blank line before an explicit key", func(t *testing.T) {
		t.Parallel()

		// The lexer drops the blank line between the flow sequence and
		// the "?", and the source keeps it, so the key after the explicit
		// entry keeps its line number and the excerpt shows its line.
		keyed := niceyaml.NewSourceFromString(
			"tags: [a, b]\n\n? key\n: value\nport: http\n",
			niceyaml.WithName("f.yaml"),
		)

		err := keyed.Bind(niceyaml.NewError("bad port", niceyaml.AtPath(paths.Current().Child("port"))))

		var bound *niceyaml.SourceError

		require.ErrorAs(t, err, &bound)
		assert.Equal(t, "f.yaml:5:7: $.port: bad port", err.Error())

		got, ok := bound.Range()
		require.True(t, ok)
		assert.Equal(t, position.NewRange(position.New(4, 6), position.New(4, 10)), got)

		assert.Equal(t, stringtest.JoinLF(
			"f.yaml:5:7: $.port: bad port",
			"",
			"   3 | ? key",
			"   4 | : value",
			"   5 | port: http",
			"     |       ^^^^",
		), fmt.Sprintf("%+v", err))
	})

	t.Run("path error resolves nowhere in several documents", func(t *testing.T) {
		t.Parallel()

		// The source holds two documents and Bind picks neither, so the
		// error keeps its message and name and no position, and names the
		// reason in place of the excerpt.
		err := source.Bind(niceyaml.NewError("bad", niceyaml.AtPath(paths.Current().Child("b"))))

		var bound *niceyaml.SourceError

		require.ErrorAs(t, err, &bound)
		assert.Equal(t, "two.yaml: $.b: bad", err.Error())
		assert.Nil(t, bound.Document())

		rangeErr := bound.Unresolved()
		require.ErrorIs(t, rangeErr, niceyaml.ErrPathNeedsDocument)
		require.ErrorIs(t, rangeErr, niceyaml.ErrMultipleDocuments)

		assert.Equal(t, stringtest.JoinLF(
			"two.yaml: $.b: bad",
			"",
			"no excerpt: path needs a document to resolve in: $.b: multiple documents in source: 2 documents",
		), fmt.Sprintf("%+v", err))
	})

	t.Run("path error in a stream of markers alone binds as in an empty file", func(t *testing.T) {
		t.Parallel()

		// A stream of "..." markers alone holds one empty document, as an
		// empty file does, so a path resolves in that document and finds
		// nothing there.
		for _, input := range []string{"...\n", ""} {
			src := niceyaml.NewSourceFromString(input, niceyaml.WithName("none.yaml"))
			err := src.Bind(niceyaml.NewError("bad", niceyaml.AtPath(paths.Current().Child("b"))))

			doc, docErr := src.Document()
			require.NoError(t, docErr)

			var bound *niceyaml.SourceError

			require.ErrorAs(t, err, &bound)
			assert.Same(t, doc, bound.Document(), input)

			rangeErr := bound.Unresolved()
			require.ErrorIs(t, rangeErr, paths.ErrNoDocument, input)
			require.NotErrorIs(t, rangeErr, niceyaml.ErrPathNeedsDocument, input)
		}
	})

	t.Run("path error in a source that does not parse names the parse error", func(t *testing.T) {
		t.Parallel()

		broken := niceyaml.NewSourceFromString("a: [\n", niceyaml.WithName("broken.yaml"))
		_, parseErr := broken.File()
		require.Error(t, parseErr)

		err := broken.Bind(niceyaml.NewError("bad", niceyaml.AtPath(paths.Current().Child("a"))))

		var bound *niceyaml.SourceError

		require.ErrorAs(t, err, &bound)
		assert.Nil(t, bound.Document())

		rangeErr := bound.Unresolved()
		require.ErrorIs(t, rangeErr, niceyaml.ErrPathNeedsDocument)
		assert.Contains(t, rangeErr.Error(), parseErr.Error())
	})

	t.Run("each location in a tree finds its own document", func(t *testing.T) {
		t.Parallel()

		docs, err := source.Documents()
		require.NoError(t, err)

		err = source.Bind(errors.Join(
			niceyaml.NewError("bad a", niceyaml.AtPosition(position.New(0, 3))),
			niceyaml.NewError("bad b", niceyaml.AtPosition(position.New(2, 3))),
		))

		var bound *niceyaml.SourceError

		require.ErrorAs(t, err, &bound)
		assert.Nil(t, bound.Document())

		children := bound.Errors()
		require.Len(t, children, 2)
		assert.Same(t, docs[0], children[0].Document())
		assert.Same(t, docs[1], children[1].Document())
	})

	t.Run("nested path errors resolve nowhere in several documents", func(t *testing.T) {
		t.Parallel()

		err := source.Bind(niceyaml.NewSummary("2 problems",
			niceyaml.NewError("bad a", niceyaml.AtPath(paths.Current().Child("a"))),
			niceyaml.NewError("bad b", niceyaml.AtPath(paths.Current().Child("b"))),
		))

		var bound *niceyaml.SourceError

		require.ErrorAs(t, err, &bound)

		for _, child := range bound.Errors() {
			rangeErr := child.Unresolved()
			require.ErrorIs(t, rangeErr, niceyaml.ErrPathNeedsDocument)
		}

		// The root carries no location of its own, so the %+v verb lists
		// the children and prints no excerpt line.
		assert.Equal(t, stringtest.JoinLF(
			"two.yaml: 2 problems",
			"|-- $.a: bad a",
			"`-- $.b: bad b",
		), fmt.Sprintf("%+v", err))
	})

	t.Run("error without a location names the source", func(t *testing.T) {
		t.Parallel()

		err := source.Bind(errors.New("plain"))

		var bound *niceyaml.SourceError

		require.ErrorAs(t, err, &bound)
		assert.Equal(t, "two.yaml: plain", err.Error())
	})

	t.Run("nil and bound errors come back as they are", func(t *testing.T) {
		t.Parallel()

		require.NoError(t, source.Bind(nil))

		docs, err := source.Documents()
		require.NoError(t, err)

		bound := docs[1].Bind(niceyaml.NewError("bad", niceyaml.AtPath(paths.Current().Child("b"))))
		assert.Same(t, bound, source.Bind(bound))
	})
}

func TestSource_Bind_RoutesDocuments(t *testing.T) {
	t.Parallel()

	// Four documents, the second holding a comment alone.
	commented := "a: 1\n---\n# only\n---\nb: 2\n---\nc: 3\n"
	// Three documents, each a header alone.
	headers := "---\n---\n---\n"

	tcs := map[string]struct {
		input string
		line  int
		// The index of the document the error binds to, or -1 for none.
		want int
	}{
		"first line of the first document":   {input: commented, line: 0, want: 0},
		"header of a comment-only document":  {input: commented, line: 1, want: 1},
		"comment of a comment-only document": {input: commented, line: 2, want: 1},
		"header of a middle document":        {input: commented, line: 3, want: 2},
		"content of a middle document":       {input: commented, line: 4, want: 2},
		"last line of the last document":     {input: commented, line: 6, want: 3},
		"line past the last document":        {input: commented, line: 7, want: -1},
		"first of several headers":           {input: headers, line: 0, want: 0},
		"middle of several headers":          {input: headers, line: 1, want: 1},
		"last of several headers":            {input: headers, line: 2, want: 2},
		"line of an empty source":            {input: "", line: 0, want: -1},
		"commented end marker of a document": {input: "a: 1\n... # e\nk: v\n", line: 1, want: 0},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			source := niceyaml.NewSourceFromString(tc.input)

			docs, err := source.Documents()
			require.NoError(t, err)

			rng := position.NewRange(position.New(tc.line, 0), position.New(tc.line, 1))

			var bound *niceyaml.SourceError

			require.ErrorAs(t, source.Bind(niceyaml.NewError("bad", niceyaml.AtRange(rng))), &bound)

			if tc.want < 0 {
				assert.Nil(t, bound.Document())

				return
			}

			assert.Same(t, docs[tc.want], bound.Document())
		})
	}
}

// errReadFailed is the error a failing reader reports.
var errReadFailed = errors.New("read failed")

func TestNewSourceFromReader(t *testing.T) {
	t.Parallel()

	t.Run("reads to the end", func(t *testing.T) {
		t.Parallel()

		source, err := niceyaml.NewSourceFromReader(strings.NewReader("key: value\n"), niceyaml.WithName("<stdin>"))
		require.NoError(t, err)
		assert.Equal(t, "<stdin>", source.Name())
		assert.Empty(t, source.FilePath())

		doc, err := source.Document()
		require.NoError(t, err)

		got, err := doc.Decode[map[string]string](t.Context())
		require.NoError(t, err)
		assert.Equal(t, map[string]string{"key": "value"}, got)
	})

	t.Run("reports a read error", func(t *testing.T) {
		t.Parallel()

		_, err := niceyaml.NewSourceFromReader(iotest.ErrReader(errReadFailed))
		require.ErrorIs(t, err, errReadFailed)
	})
}

func TestNewSourceFromFS(t *testing.T) {
	t.Parallel()

	fsys := fstest.MapFS{
		"configs/app.yaml": &fstest.MapFile{Data: []byte("key: value\n")},
	}

	t.Run("reads the file and sets its path", func(t *testing.T) {
		t.Parallel()

		source, err := niceyaml.NewSourceFromFS(fsys, "configs/app.yaml")
		require.NoError(t, err)
		assert.Equal(t, "configs/app.yaml", source.FilePath())
		assert.Equal(t, "configs/app.yaml", source.Name())

		doc, err := source.Document()
		require.NoError(t, err)
		assert.Equal(t, "configs/app.yaml", doc.FilePath())
	})

	t.Run("options apply after the path", func(t *testing.T) {
		t.Parallel()

		source, err := niceyaml.NewSourceFromFS(fsys, "configs/app.yaml", niceyaml.WithName("app"))
		require.NoError(t, err)
		assert.Equal(t, "app", source.Name())
		assert.Equal(t, "configs/app.yaml", source.FilePath())
	})

	t.Run("reports a missing file", func(t *testing.T) {
		t.Parallel()

		_, err := niceyaml.NewSourceFromFS(fsys, "configs/missing.yaml")
		require.ErrorIs(t, err, fs.ErrNotExist)
	})
}

func TestSource_Decode(t *testing.T) {
	t.Parallel()

	type config struct {
		Name string `yaml:"name"`
	}

	t.Run("decodes the one document", func(t *testing.T) {
		t.Parallel()

		called := false

		source := niceyaml.NewSourceFromString("name: test\n")

		got, err := source.Decode[config](t.Context(), niceyaml.WithValidator(
			niceyaml.ValidatorFunc(func(_ context.Context, n *niceyaml.Node) error {
				called = true

				assert.True(t, n.Path().IsRoot())

				return nil
			}),
		))
		require.NoError(t, err)
		assert.Equal(t, config{Name: "test"}, got)
		assert.True(t, called, "the validator of the call runs on the root")
	})

	t.Run("DecodeInto keeps the fields the document leaves out", func(t *testing.T) {
		t.Parallel()

		source := niceyaml.NewSourceFromString("{}\n")

		cfg := config{Name: "default"}
		require.NoError(t, source.DecodeInto(t.Context(), &cfg))
		assert.Equal(t, config{Name: "default"}, cfg)
	})

	t.Run("several documents return the error Document returns", func(t *testing.T) {
		t.Parallel()

		source := niceyaml.NewSourceFromString("name: a\n---\nname: b\n", niceyaml.WithName("two.yaml"))

		got, err := source.Decode[config](t.Context())
		require.ErrorIs(t, err, niceyaml.ErrMultipleDocuments)
		assert.Equal(t, config{}, got)
		assert.Equal(t, "two.yaml:2:1: multiple documents in source: 2 documents", err.Error())
	})

	t.Run("a decode error binds to the source", func(t *testing.T) {
		t.Parallel()

		source := niceyaml.NewSourceFromString("name: [1]\n", niceyaml.WithName("x.yaml"))

		_, err := source.Decode[config](t.Context())
		require.Error(t, err)

		var bound *niceyaml.SourceError

		require.ErrorAs(t, err, &bound)
		assert.Same(t, source, bound.Source())
	})
}

func TestWithReferences(t *testing.T) {
	t.Parallel()

	type server struct {
		Port int `yaml:"port"`
	}

	type config struct {
		Name   string `yaml:"name"`
		Server server `yaml:"server"`
	}

	// The schema decodes the node it checks, so it reads an alias as the
	// decodes of the source read it.
	serverSchema := schema.MustCompile([]byte(`{
		"type": "object",
		"properties": {
			"name": {"type": "string"},
			"server": {
				"type": "object",
				"required": ["port"],
				"properties": {"port": {"type": "integer"}}
			}
		}
	}`))

	defaults := niceyaml.NewSourceFromString("base: &base\n  port: 8080\n", niceyaml.WithName("defaults.yaml"))

	t.Run("every decode and validation agrees", func(t *testing.T) {
		t.Parallel()

		// Each check runs v on the one document of src through one entry
		// point.
		checks := map[string]func(ctx context.Context, src *niceyaml.Source, doc *niceyaml.Node, v niceyaml.Validator) error{
			"Node.Decode": func(ctx context.Context, _ *niceyaml.Source, doc *niceyaml.Node, v niceyaml.Validator) error {
				_, err := doc.Decode[config](ctx, niceyaml.WithValidator(v))

				return err
			},
			"Source.Decode": func(ctx context.Context, src *niceyaml.Source, _ *niceyaml.Node, v niceyaml.Validator) error {
				_, err := src.Decode[config](ctx, niceyaml.WithValidator(v))

				return err
			},
			"Decoder.Decode": func(ctx context.Context, _ *niceyaml.Source, doc *niceyaml.Node, v niceyaml.Validator) error {
				_, err := niceyaml.NewDecoder(niceyaml.WithValidator(v)).Decode[config](ctx, doc)

				return err
			},
			"Node.Validate": func(ctx context.Context, _ *niceyaml.Source, doc *niceyaml.Node, v niceyaml.Validator) error {
				return doc.Validate(ctx, v)
			},
			"Decoder.Validate": func(ctx context.Context, _ *niceyaml.Source, doc *niceyaml.Node, v niceyaml.Validator) error {
				return niceyaml.NewDecoder(niceyaml.WithValidator(v)).Validate(ctx, doc)
			},
			"Source.ValidateDocuments": func(ctx context.Context, src *niceyaml.Source, _ *niceyaml.Node, v niceyaml.Validator) error {
				return src.ValidateDocuments(ctx, v)
			},
		}

		tcs := map[string]struct {
			input string
			refs  []*niceyaml.Source
			// The message of each binding the result holds.
			want []string
		}{
			"alias to a mapping of the reference": {
				input: "server: *base\n",
				refs:  []*niceyaml.Source{defaults},
			},
			"merge of a mapping of the reference": {
				input: "server:\n  <<: *base\n",
				refs:  []*niceyaml.Source{defaults},
			},
			"violation beside an alias to the reference": {
				input: "name: 1\nserver: *base\n",
				refs:  []*niceyaml.Source{defaults},
				want:  []string{`app.yaml:1:7: $.name: expected "string", got "integer"`},
			},
			"violation beside a merge of the reference": {
				input: "server:\n  <<: *base\n  port: x\n",
				refs:  []*niceyaml.Source{defaults},
				want:  []string{`app.yaml:3:9: $.server.port: expected "integer", got "string"`},
			},
			"alias without the reference": {
				input: "server: *base\n",
				want:  []string{`app.yaml:1:9: $.server: could not find alias "base"`},
			},
		}

		for name, tc := range tcs {
			for checkName, check := range checks {
				t.Run(name+"/"+checkName, func(t *testing.T) {
					t.Parallel()

					src := niceyaml.NewSourceFromString(tc.input,
						niceyaml.WithName("app.yaml"),
						niceyaml.WithReferences(tc.refs...),
					)

					doc, err := src.Document()
					require.NoError(t, err)

					// The validator records the Node each run gets.
					var seen []*niceyaml.Node

					v := niceyaml.ValidatorFunc(func(ctx context.Context, n *niceyaml.Node) error {
						seen = append(seen, n)

						return serverSchema.Validate(ctx, n)
					})

					err = check(t.Context(), src, doc, v)
					assert.Equal(t, tc.want, bindingMessages(err))

					require.Len(t, seen, 1)
					assert.Same(t, doc, seen[0], "the validator got a Node other than the caller's")
					assert.Same(t, doc, seen[0].Document())
				})
			}
		}
	})

	t.Run("a file of several documents reports the real violation alone", func(t *testing.T) {
		t.Parallel()

		src := niceyaml.NewSourceFromString("server: *base\n---\nserver: {port: x}\n",
			niceyaml.WithName("app.yaml"),
			niceyaml.WithReferences(defaults),
		)

		want := []string{`app.yaml:3:16: $.server.port: expected "integer", got "string"`}

		err := src.ValidateDocuments(t.Context(), serverSchema)
		assert.Equal(t, want, bindingMessages(err))

		docs, err := src.Documents()
		require.NoError(t, err)
		require.Len(t, docs, 2)

		_, err = docs[0].Decode[config](t.Context(), niceyaml.WithValidator(serverSchema))
		require.NoError(t, err)

		_, err = docs[1].Decode[config](t.Context(), niceyaml.WithValidator(serverSchema))
		assert.Equal(t, want, bindingMessages(err))
	})

	t.Run("anchors of the references", func(t *testing.T) {
		t.Parallel()

		first := niceyaml.NewSourceFromString("old: &old 1\nx: &x 1\n")
		second := niceyaml.NewSourceFromString("x: &x 2\n")
		nested := niceyaml.NewSourceFromString("list: &list [*x, 3]\n", niceyaml.WithReferences(second))

		tcs := map[string]struct {
			input string
			refs  []*niceyaml.Source
			want  map[string]any
		}{
			"later reference overrides an earlier one": {
				input: "a: *old\nb: *x\n",
				refs:  []*niceyaml.Source{first, second},
				want:  map[string]any{"a": uint64(1), "b": uint64(2)},
			},
			"reference brings its own references": {
				input: "a: *list\nb: *x\n",
				refs:  []*niceyaml.Source{nested},
				want:  map[string]any{"a": []any{uint64(2), uint64(3)}, "b": uint64(2)},
			},
			"nil reference is skipped": {
				input: "a: *x\n",
				refs:  []*niceyaml.Source{nil, second, nil},
				want:  map[string]any{"a": uint64(2)},
			},
			"anchor of the document before the alias wins": {
				input: "x: &x 5\na: *x\n",
				refs:  []*niceyaml.Source{second},
				want:  map[string]any{"x": uint64(5), "a": uint64(5)},
			},
			"reference with CRLF line endings": {
				input: "a: *y\n",
				refs:  []*niceyaml.Source{niceyaml.NewSourceFromString("y: &y\r\n  k: v\r\n")},
				want:  map[string]any{"a": map[string]any{"k": "v"}},
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				doc := yamltest.FirstDocument(t, tc.input, niceyaml.WithReferences(tc.refs...))

				got, err := doc.Decode[map[string]any](t.Context())
				require.NoError(t, err)
				assert.Equal(t, tc.want, got)
			})
		}
	})

	t.Run("alias inside a reference reads the anchors of the references", func(t *testing.T) {
		t.Parallel()

		type target struct {
			Base   server `yaml:"base"`
			Server server `yaml:"server"`
			V      []int  `yaml:"v"`
			W      []int  `yaml:"w"`
			X      int    `yaml:"x"`
		}

		list := niceyaml.NewSourceFromString("x: &x 1\nlist: &list [*x]\n")
		merged := niceyaml.NewSourceFromString("base: &base {port: 443}\nserver: &server {<<: *base}\n")

		tcs := map[string]struct {
			ref   *niceyaml.Source
			input string
			want  target
		}{
			"anchor of the document before the alias": {
				ref:   list,
				input: "x: &x 9\nv: *list\n",
				want:  target{X: 9, V: []int{1}},
			},
			"anchor of the document after the alias": {
				ref:   list,
				input: "v: *list\nx: &x 9\n",
				want:  target{X: 9, V: []int{1}},
			},
			"alias of the document to its own anchor": {
				ref:   list,
				input: "x: &x 9\nv: *list\nw: [*x]\n",
				want:  target{X: 9, V: []int{1}, W: []int{9}},
			},
			"two anchors of the document": {
				ref:   list,
				input: "w: [&x 8, *x, &x 9, *x]\nv: *list\n",
				want:  target{V: []int{1}, W: []int{8, 8, 9, 9}},
			},
			"merge inside the reference": {
				ref:   merged,
				input: "base: &base {port: 80}\nserver: *server\n",
				want:  target{Base: server{Port: 80}, Server: server{Port: 443}},
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				doc := yamltest.FirstDocument(t, tc.input, niceyaml.WithReferences(tc.ref))

				got, err := doc.Decode[target](t.Context())
				require.NoError(t, err)
				assert.Equal(t, tc.want, got)

				// A decode into any reads each alias as the typed decode
				// does. The text it encodes to holds no alias, so go-yaml
				// reads that text into the target on its own.
				untyped, err := doc.Decode[any](t.Context())
				require.NoError(t, err)

				text, err := yaml.Marshal(untyped)
				require.NoError(t, err)

				var again target

				require.NoError(t, yaml.Unmarshal(text, &again))
				assert.Equal(t, tc.want, again)
			})
		}
	})

	t.Run("schema checks the value the decode returns", func(t *testing.T) {
		t.Parallel()

		type target struct {
			Server server `yaml:"server"`
		}

		secure := schema.MustCompile([]byte(`{
			"type": "object",
			"required": ["server"],
			"properties": {
				"server": {
					"type": "object",
					"required": ["port"],
					"properties": {"port": {"const": 443}}
				}
			}
		}`))

		tcs := map[string]struct {
			input string
			want  int
			err   []string
		}{
			"alias inside the reference reads the reference": {
				input: "base: &base {port: 80}\nserver: *server\n",
				want:  443,
			},
			"alias of the document reads the document": {
				input: "base: &base {port: 80}\nserver: *base\n",
				err:   []string{`app.yaml:1:20: $.server.port: value does not match const`},
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				doc := yamltest.FirstDocument(t, tc.input,
					niceyaml.WithName("app.yaml"),
					niceyaml.WithReferences(niceyaml.NewSourceFromString(
						"base: &base {port: 443}\nserver: &server {<<: *base}\n",
					)),
				)

				got, err := doc.Decode[target](t.Context(), niceyaml.WithValidator(secure))
				assert.Equal(t, tc.err, bindingMessages(err))
				assert.Equal(t, tc.want, got.Server.Port)
			})
		}
	})

	t.Run("merge of a reference sets a key the document sets before it", func(t *testing.T) {
		t.Parallel()

		// The go-yaml decoder lets a `<<` merge key set a key the mapping
		// holds above it.
		doc := yamltest.FirstDocument(t, "kind: Deployment\n<<: *base\n",
			niceyaml.WithReferences(niceyaml.NewSourceFromString("base: &base {kind: Bogus}\n")))

		got, err := doc.Decode[map[string]any](t.Context())
		require.NoError(t, err)
		assert.Equal(t, map[string]any{"kind": "Bogus"}, got)
	})

	t.Run("anchor name the references define twice", func(t *testing.T) {
		t.Parallel()

		// WithReferences documents this limit. The go-yaml decoder reads the
		// alias inside the list by name again for a typed target, after it
		// has read every reference document, so it finds the second anchor.
		tcs := map[string]struct {
			refs []*niceyaml.Source
		}{
			"in one reference": {
				refs: []*niceyaml.Source{
					niceyaml.NewSourceFromString("x: &x 1\nlist: &list [*x]\ny: &x 2\n"),
				},
			},
			"in two references": {
				refs: []*niceyaml.Source{
					niceyaml.NewSourceFromString("x: &x 1\nlist: &list [*x]\n"),
					niceyaml.NewSourceFromString("x: &x 2\n"),
				},
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				doc := yamltest.FirstDocument(t, "v: *list\n", niceyaml.WithReferences(tc.refs...))

				untyped, err := doc.Decode[map[string]any](t.Context())
				require.NoError(t, err)
				assert.Equal(t, map[string]any{"v": []any{uint64(1)}}, untyped)

				typed, err := doc.Decode[map[string][]int](t.Context())
				require.NoError(t, err)
				assert.Equal(t, map[string][]int{"v": {2}}, typed)
			})
		}
	})

	t.Run("references serve every decode from any goroutine", func(t *testing.T) {
		t.Parallel()

		docs, err := niceyaml.NewSourceFromString("b: *x\n---\nc: *x\n",
			niceyaml.WithReferences(niceyaml.NewSourceFromString("base: &x 1\n")),
		).Documents()
		require.NoError(t, err)
		require.Len(t, docs, 2)

		dec := niceyaml.NewDecoder()

		var wg sync.WaitGroup

		for range 4 {
			for i, doc := range docs {
				wg.Go(func() {
					got, err := dec.Decode[map[string]int](t.Context(), doc)
					if assert.NoError(t, err) {
						assert.Equal(t, map[string]int{[]string{"b", "c"}[i]: 1}, got)
					}
				})
			}
		}

		wg.Wait()
	})

	t.Run("reference of one decode wins over the references of the source", func(t *testing.T) {
		t.Parallel()

		file := filepath.Join(t.TempDir(), "override.yaml")
		require.NoError(t, os.WriteFile(file, []byte("x: &x 3\n"), 0o600))

		doc := yamltest.FirstDocument(t, "a: *x\n",
			niceyaml.WithReferences(niceyaml.NewSourceFromString("x: &x 1\n")))

		got, err := doc.Decode[map[string]int](t.Context(),
			niceyaml.WithYAMLDecodeOptions(yaml.ReferenceFiles(file)))
		require.NoError(t, err)
		assert.Equal(t, map[string]int{"a": 3}, got)

		// The option reaches that decode alone.
		got, err = doc.Decode[map[string]int](t.Context())
		require.NoError(t, err)
		assert.Equal(t, map[string]int{"a": 1}, got)
	})

	t.Run("reference of one decode shares its anchor names with the document", func(t *testing.T) {
		t.Parallel()

		file := filepath.Join(t.TempDir(), "lists.yaml")
		require.NoError(t, os.WriteFile(file, []byte("x: &x 1\nlist: &list [*x]\n"), 0o600))

		// WithYAMLDecodeOptions documents this limit. The source sees no
		// reference file that one decode names, so it keeps the anchors of
		// that file apart from its own only when it has references.
		tcs := map[string]struct {
			opts []niceyaml.SourceOption
			want map[string][]int
		}{
			"source without references": {
				want: map[string][]int{"w": {9}, "v": {9}},
			},
			"source with references": {
				opts: []niceyaml.SourceOption{
					niceyaml.WithReferences(niceyaml.NewSourceFromString("other: &other 0\n")),
				},
				want: map[string][]int{"w": {9}, "v": {1}},
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				doc := yamltest.FirstDocument(t, "w: [&x 9]\nv: *list\n", tc.opts...)

				untyped, err := doc.Decode[map[string]any](t.Context(),
					niceyaml.WithYAMLDecodeOptions(yaml.ReferenceFiles(file)))
				require.NoError(t, err)
				assert.Equal(t, map[string]any{"w": []any{uint64(9)}, "v": []any{uint64(1)}}, untyped)

				typed, err := doc.Decode[map[string][]int](t.Context(),
					niceyaml.WithYAMLDecodeOptions(yaml.ReferenceFiles(file)))
				require.NoError(t, err)
				assert.Equal(t, tc.want, typed)
			})
		}
	})

	t.Run("a path resolves in the document alone", func(t *testing.T) {
		t.Parallel()

		bad := niceyaml.NewSourceFromString("base: &base\n  port: oops\n")
		src := niceyaml.NewSourceFromString("server: *base\n",
			niceyaml.WithName("app.yaml"),
			niceyaml.WithReferences(bad),
		)

		doc, err := src.Document()
		require.NoError(t, err)

		_, err = doc.At(paths.Current().Child("server", "port"))
		require.ErrorIs(t, err, paths.ErrAlias)

		_, err = doc.Nodes(paths.Current().Child("server").ChildAll())
		require.ErrorIs(t, err, paths.ErrAlias)

		err = src.ValidateDocuments(t.Context(), serverSchema)
		assert.Equal(t, []string{`app.yaml: $.server.port: expected "integer", got "string"`}, bindingMessages(err))
	})

	t.Run("reference that does not parse fails every decode", func(t *testing.T) {
		t.Parallel()

		doc := yamltest.FirstDocument(t, "a: 1\n",
			niceyaml.WithReferences(niceyaml.NewSourceFromString("x: [\n")))

		_, err := doc.Decode[map[string]int](t.Context())
		require.ErrorContains(t, err, "sequence end token ']' not found")
	})
}

func TestWithAliasLimit(t *testing.T) {
	t.Parallel()

	// Each level of the bomb lists nine aliases to the level below, so a4
	// expands to 9^5 scalars from 252 bytes.
	var sb strings.Builder

	sb.WriteString("a0: &a0 [x, x, x, x, x, x, x, x, x]\n")

	for level := 1; level <= 4; level++ {
		aliases := strings.Repeat(fmt.Sprintf("*a%d, ", level-1), 9)
		fmt.Fprintf(&sb, "a%d: &a%d [%s]\n", level, level, strings.TrimSuffix(aliases, ", "))
	}

	bomb := sb.String()
	last := paths.Current().Child("a4")

	// The schema accepts the root and the list under a4, and it decodes
	// the node it checks, which a schema that accepts everything skips.
	shape := schema.MustCompile([]byte(`{"type": ["object", "array"]}`))

	// The registry routes a document on the content of a4, so its matcher
	// reads a node that holds an alias. No document matches, and the
	// registry then passes it.
	routed := schema.NewRegistry(
		schema.WithResolvers(schema.When(matcher.Content(paths.Doc().Child("a4"), "x"), shape.Ref())),
		schema.WithRequireSchema(false),
	)

	decode := func(ctx context.Context, n *niceyaml.Node, opts ...niceyaml.DecodeOption) error {
		_, err := n.Decode[any](ctx, opts...)

		return err
	}

	tcs := map[string]struct {
		read func(ctx context.Context, n *niceyaml.Node) error
		path paths.Path
		// The message of the refusal with the limit on.
		want string
		// The refusal comes from a resolver of a registry.
		resolve bool
	}{
		"decode": {
			read: func(ctx context.Context, n *niceyaml.Node) error {
				return decode(ctx, n)
			},
			want: "bomb.yaml:1:1: excessive aliasing",
		},
		"decode with a schema validator": {
			read: func(ctx context.Context, n *niceyaml.Node) error {
				return decode(ctx, n, niceyaml.WithValidator(shape))
			},
			want: "bomb.yaml:1:1: excessive aliasing",
		},
		"decoder with a schema validator": {
			read: func(ctx context.Context, n *niceyaml.Node) error {
				_, err := niceyaml.NewDecoder(niceyaml.WithValidator(shape)).Decode[any](ctx, n)

				return err
			},
			want: "bomb.yaml:1:1: excessive aliasing",
		},
		"schema validation": {
			read: func(ctx context.Context, n *niceyaml.Node) error {
				return n.Validate(ctx, shape)
			},
			want: "bomb.yaml:1:1: excessive aliasing",
		},
		"validation of every document": {
			read: func(ctx context.Context, n *niceyaml.Node) error {
				return n.Source().ValidateDocuments(ctx, shape)
			},
			want: "bomb.yaml:1:1: excessive aliasing",
		},
		"registry with a content matcher": {
			read: func(ctx context.Context, n *niceyaml.Node) error {
				return n.Validate(ctx, routed)
			},
			want:    "resolve schema: bomb.yaml: excessive aliasing",
			resolve: true,
		},
		"decode with a registry with a content matcher": {
			read: func(ctx context.Context, n *niceyaml.Node) error {
				return decode(ctx, n, niceyaml.WithValidator(routed))
			},
			want:    "resolve schema: bomb.yaml: excessive aliasing",
			resolve: true,
		},
		// The decode binds its refusal at the first token of the node, so
		// a scoped node names no path in front of it.
		"decode of a scoped node": {
			path: last,
			read: func(ctx context.Context, n *niceyaml.Node) error {
				return decode(ctx, n)
			},
			want: "bomb.yaml:5:9: excessive aliasing",
		},
		"schema validation of a scoped node": {
			path: last,
			read: func(ctx context.Context, n *niceyaml.Node) error {
				return n.Validate(ctx, shape)
			},
			want: "bomb.yaml:5:9: excessive aliasing",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			sources := map[string][]niceyaml.SourceOption{
				"default":  {niceyaml.WithName("bomb.yaml")},
				"limit on": {niceyaml.WithName("bomb.yaml"), niceyaml.WithAliasLimit(true)},
			}

			for via, opts := range sources {
				node := yamltest.FirstDocument(t, bomb, opts...)
				if tc.path.Len() > 0 {
					node = yamltest.At(t, node, tc.path)
				}

				// Every reader reports the aliases of the document as a
				// fault of the document.
				err := tc.read(t.Context(), node)
				require.EqualError(t, err, tc.want, via)
				require.ErrorIs(t, err, niceyaml.ErrExcessiveAliasing, via)
				require.NotErrorIs(t, err, schema.ErrValidate, via)
				require.NotErrorIs(t, err, niceyaml.ErrDecode, via)
				assert.Equal(t, tc.resolve, errors.Is(err, schema.ErrResolve), via)
				assert.True(t, niceyaml.IsInvalid(err), via)
			}

			// With the limit off, the same reader reads the document.
			trusted := yamltest.FirstDocument(t, bomb, niceyaml.WithName("bomb.yaml"), niceyaml.WithAliasLimit(false))
			if tc.path.Len() > 0 {
				trusted = yamltest.At(t, trusted, tc.path)
			}

			require.NoError(t, tc.read(t.Context(), trusted))
		})
	}

	t.Run("every document of the source", func(t *testing.T) {
		t.Parallel()

		docs, err := niceyaml.NewSourceFromString(bomb+"---\n"+bomb, niceyaml.WithAliasLimit(false)).Documents()
		require.NoError(t, err)
		require.Len(t, docs, 2)

		for _, doc := range docs {
			require.NoError(t, decode(t.Context(), doc, niceyaml.WithValidator(shape)))
		}
	})

	t.Run("limits that stay on", func(t *testing.T) {
		t.Parallel()

		trusted := yamltest.FirstDocument(t, bomb, niceyaml.WithName("bomb.yaml"), niceyaml.WithAliasLimit(false))

		data, err := trusted.Decode[any](t.Context(), niceyaml.WithValidator(shape))
		require.NoError(t, err)

		// ValidateValue takes a Go value and holds no source, so it
		// applies the limit to the value the source decoded.
		err = shape.ValidateValue(t.Context(), data)
		require.EqualError(t, err, "excessive aliasing")
		require.ErrorIs(t, err, niceyaml.ErrExcessiveAliasing)
		require.NotErrorIs(t, err, schema.ErrValidate)
		assert.True(t, niceyaml.IsInvalid(err))

		// Each [*] lists nine aliases to the level below, so the path
		// would select 9^5 nodes. The path is at fault for that, and the
		// document is not.
		_, err = trusted.Nodes(paths.MustParse("$.a4[*][*][*][*][*]"))
		require.EqualError(t, err, "bomb.yaml: resolve $.a4[*][*][*][*][*]: excessive aliasing")
		require.ErrorIs(t, err, niceyaml.ErrExcessiveAliasing)
		assert.False(t, niceyaml.IsInvalid(err))
	})
}
