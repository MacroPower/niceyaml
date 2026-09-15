package niceyaml_test

import (
	"errors"
	"fmt"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/goccy/go-yaml/lexer"
	"github.com/goccy/go-yaml/token"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.jacobcolvin.com/x/stringtest"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/line"
	"go.jacobcolvin.com/niceyaml/position"
	"go.jacobcolvin.com/niceyaml/style"
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
				"   1 | ^ error",
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
				"   2 | ^ here",
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
				"   1 | ^ start",
				"   2 | second: 2",
				"   3 | third: 3",
				"   3 | ^ end",
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
			view := niceyaml.NewSourceFromTokens(tks, niceyaml.WithName("test")).Lines()

			// Apply annotations to specified lines.
			for idx, ann := range tc.annotations {
				require.Less(t, idx, view.Len(), "annotation index out of range")

				view[idx].AddAnnotation(ann)
			}

			assert.Equal(t, tc.want, view.String())
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

	var (
		runes     []rune
		positions []position.Position
	)

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

	lines := niceyaml.Diff(beforeLines, afterLines).Unified()

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

	var collected []int

	for idx := range lines.AllLines() {
		collected = append(collected, idx)
		if idx >= 1 {
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

		for idx := range source.AllLines() {
			collected = append(collected, idx)
		}

		assert.Equal(t, []int{0, 1, 2, 3, 4}, collected)
	})

	t.Run("single span filters lines", func(t *testing.T) {
		t.Parallel()

		var collected []int

		for idx := range source.AllLines(position.NewSpan(1, 3)) {
			collected = append(collected, idx)
		}

		assert.Equal(t, []int{1, 2}, collected)
	})

	t.Run("multiple spans iterate in order", func(t *testing.T) {
		t.Parallel()

		var collected []int

		for idx := range source.AllLines(
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

		for idx := range source.AllLines(position.NewSpan(-5, 100)) {
			collected = append(collected, idx)
		}

		assert.Equal(t, []int{0, 1, 2, 3, 4}, collected)
	})

	t.Run("empty span yields nothing", func(t *testing.T) {
		t.Parallel()

		var collected []int

		for idx := range source.AllLines(position.NewSpan(2, 2)) {
			collected = append(collected, idx)
		}

		assert.Nil(t, collected)
	})

	t.Run("span beyond length yields nothing", func(t *testing.T) {
		t.Parallel()

		var collected []int

		for idx := range source.AllLines(position.NewSpan(10, 20)) {
			collected = append(collected, idx)
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

	var collected []rune

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

		var collected []rune

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

func TestSource_Lines_TokenLookup(t *testing.T) {
	t.Parallel()

	input := stringtest.Input(`
		key: |
		  line1
		  line2
	`)
	source := niceyaml.NewSourceFromString(input)
	require.Equal(t, 3, source.Len())

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

func TestNewSourceFromBytes(t *testing.T) {
	t.Parallel()

	src := []byte("key: value")
	s := niceyaml.NewSourceFromBytes(src)
	assert.Equal(t, "key: value", s.Lines().Content())
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
			err := source.Lines().Validate()

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
}

func TestSource_WithYAMLParserOptions(t *testing.T) {
	t.Parallel()

	t.Run("parses with default options", func(t *testing.T) {
		t.Parallel()

		source := niceyaml.NewSourceFromString(
			"key: value\n",
			niceyaml.WithYAMLParserOptions(),
		)

		file, err := source.File()
		require.NoError(t, err)
		assert.NotNil(t, file)
	})

	t.Run("parses complex YAML with options", func(t *testing.T) {
		t.Parallel()

		input := stringtest.Input(`
			key1: value1
			key2: value2
			nested:
			  child: data
		`)

		source := niceyaml.NewSourceFromString(
			input,
			niceyaml.WithYAMLParserOptions(),
		)

		file, err := source.File()
		require.NoError(t, err)
		assert.NotNil(t, file)
		assert.Len(t, file.Docs, 1)
	})
}

func TestSource_Lines_IndependentViews(t *testing.T) {
	t.Parallel()

	source := niceyaml.NewSourceFromString("key: value\n")

	first := source.Lines()
	first.AddOverlay("test1", position.NewRange(position.New(0, 0), position.New(0, 5)))
	first[0].AddAnnotation(line.Annotation{Content: "note", Placement: line.Below})

	// A second view starts from the pristine document.
	second := source.Lines()
	assert.Empty(t, second[0].Overlays)
	assert.Empty(t, second[0].Annotations)

	// The first view keeps what was added to it.
	require.Len(t, first[0].Overlays, 1)
	assert.Equal(t, style.Style("test1"), first[0].Overlays[0].Style)
	assert.Equal(t, "key: value", first.Content())
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

	t.Run("keeps context wrapped around the Error", func(t *testing.T) {
		t.Parallel()

		source := niceyaml.NewSourceFromString("key: value\n")
		yamlErr := niceyaml.NewError("test error")
		outer := fmt.Errorf("document 3: %w", yamlErr)

		wrapped := source.WrapError(outer)

		require.ErrorIs(t, wrapped, outer)
		assert.Contains(t, wrapped.Error(), "document 3: ")
	})

	t.Run("returns non-Error unchanged", func(t *testing.T) {
		t.Parallel()

		source := niceyaml.NewSourceFromString("key: value\n")
		stdErr := errors.New("standard error")

		wrapped := source.WrapError(stdErr)

		assert.Equal(t, stdErr, wrapped)
	})

	t.Run("returns a nil Error unchanged", func(t *testing.T) {
		t.Parallel()

		source := niceyaml.NewSourceFromString("key: value\n")

		var nilErr *niceyaml.Error

		err := error(nilErr)

		assert.Equal(t, err, source.WrapError(err))
	})

	t.Run("returns context around a nil Error unchanged", func(t *testing.T) {
		t.Parallel()

		source := niceyaml.NewSourceFromString("key: value\n")

		var nilErr *niceyaml.Error

		outer := fmt.Errorf("document 3: %w", nilErr)

		wrapped := source.WrapError(outer)

		require.Equal(t, outer, wrapped)
		assert.Equal(t, "document 3: <nil>", wrapped.Error())
		assert.Equal(t, "document 3: <nil>", fmt.Sprintf("%+v", wrapped))
	})
}

// Both a [niceyaml.Lines] collection and a [*niceyaml.Source] are a [niceyaml.View].
var (
	_ niceyaml.View = niceyaml.Lines(nil)
	_ niceyaml.View = (*niceyaml.Source)(nil)
)

func TestSource_AllLines_YieldsCopies(t *testing.T) {
	t.Parallel()

	source := niceyaml.NewSourceFromString("key: value\n")

	// Mutating a line yielded by the Source's own iterator leaves the
	// document untouched.
	for _, ln := range source.AllLines() {
		ln.AddAnnotation(line.Annotation{Content: "note", Placement: line.Below})
		ln.AddOverlay(line.Overlay{Cols: position.NewSpan(0, 3), Style: style.GenericError})
	}

	assert.Empty(t, source.Lines()[0].Annotations)
	assert.Empty(t, source.Lines()[0].Overlays)

	// Printing the Source renders the pristine document.
	plain := niceyaml.NewPrinter(
		niceyaml.WithStyles(style.Styles{}),
		niceyaml.WithContainerStyle(lipgloss.NewStyle()),
		niceyaml.WithGutter(niceyaml.NoGutter),
	)
	assert.Equal(t, "key: value", plain.Print(source))
}
