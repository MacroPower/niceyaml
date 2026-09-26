package niceyaml_test

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"testing/iotest"

	"charm.land/lipgloss/v2"
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
	"go.jacobcolvin.com/niceyaml/style"
	"go.jacobcolvin.com/niceyaml/style/kind"
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
		tk, err := paths.Root().Child("other").Token(doc.DocumentAST())
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
		keyTk, err := paths.Root().Child("other").Key().Token(doc.DocumentAST())
		require.NoError(t, err)
		assert.Equal(t, position.Ranges{
			position.NewRange(position.New(3, 0), position.New(3, 5)),
		}, source.Lines().ContentRanges(keyTk))

		blockTk, err := paths.Root().Child("key").Token(doc.DocumentAST())
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

		err := yamltest.Bind(t, source, niceyaml.NewError("bad b", niceyaml.AtPath(paths.Root().Child("b"))))
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
			err = docs[1].Bind(niceyaml.NewError("bad", niceyaml.AtPath(paths.Root().Child("b"))))
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
		pathErr := niceyaml.NewError("bad name", niceyaml.AtPath(paths.Root().Child("name")))

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
		namePath := paths.Root().Child("name")

		once := yamltest.Bind(t, source, niceyaml.NewError("bad name", niceyaml.AtPath(namePath)))
		assert.Same(t, once, yamltest.Bind(t, source, once))

		outer := fmt.Errorf("document 0: %w", once)
		assert.Same(t, outer, yamltest.Bind(t, source, outer))
	})

	t.Run("leaves an error bound to another source as it is", func(t *testing.T) {
		t.Parallel()

		first := niceyaml.NewSourceFromString("name: value\n")
		second := niceyaml.NewSourceFromString("# comment\nname: value\n", niceyaml.WithName("second"))
		namePath := paths.Root().Child("name")

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

		err = one.Bind(niceyaml.NewError("bad", niceyaml.AtPath(paths.Root().Child("b"))))

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

	t.Run("path error after a blank line before an explicit key", func(t *testing.T) {
		t.Parallel()

		// The lexer drops the blank line between the flow sequence and
		// the "?", and the source keeps it, so the key after the explicit
		// entry keeps its line number and the excerpt shows its line.
		keyed := niceyaml.NewSourceFromString(
			"tags: [a, b]\n\n? key\n: value\nport: http\n",
			niceyaml.WithName("f.yaml"),
		)

		err := keyed.Bind(niceyaml.NewError("bad port", niceyaml.AtPath(paths.Root().Child("port"))))

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
		err := source.Bind(niceyaml.NewError("bad", niceyaml.AtPath(paths.Root().Child("b"))))

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

	t.Run("path error resolves nowhere in no documents", func(t *testing.T) {
		t.Parallel()

		none := niceyaml.NewSourceFromString("...\n", niceyaml.WithName("none.yaml"))
		err := none.Bind(niceyaml.NewError("bad", niceyaml.AtPath(paths.Root().Child("b"))))

		var bound *niceyaml.SourceError

		require.ErrorAs(t, err, &bound)
		assert.Nil(t, bound.Document())

		rangeErr := bound.Unresolved()
		require.ErrorIs(t, rangeErr, niceyaml.ErrPathNeedsDocument)
		require.ErrorIs(t, rangeErr, niceyaml.ErrNoDocuments)
	})

	t.Run("path error in a source that does not parse names the parse error", func(t *testing.T) {
		t.Parallel()

		broken := niceyaml.NewSourceFromString("a: [\n", niceyaml.WithName("broken.yaml"))
		_, parseErr := broken.File()
		require.Error(t, parseErr)

		err := broken.Bind(niceyaml.NewError("bad", niceyaml.AtPath(paths.Root().Child("a"))))

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

		err := source.Bind(niceyaml.NewError("2 problems", niceyaml.WithErrors(
			niceyaml.NewError("bad a", niceyaml.AtPath(paths.Root().Child("a"))),
			niceyaml.NewError("bad b", niceyaml.AtPath(paths.Root().Child("b"))),
		)))

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

		bound := docs[1].Bind(niceyaml.NewError("bad", niceyaml.AtPath(paths.Root().Child("b"))))
		assert.Same(t, bound, source.Bind(bound))
	})
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
