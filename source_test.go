package niceyaml_test

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"iter"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"testing/iotest"
	"time"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/parser"
	"github.com/goccy/go-yaml/token"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.jacobcolvin.com/x/stringtest"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/diff"
	"go.jacobcolvin.com/niceyaml/encoder"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/line"
	"go.jacobcolvin.com/niceyaml/matcher"
	"go.jacobcolvin.com/niceyaml/paths"
	"go.jacobcolvin.com/niceyaml/position"
	"go.jacobcolvin.com/niceyaml/printer"
	"go.jacobcolvin.com/niceyaml/schema"
	"go.jacobcolvin.com/niceyaml/style"
	"go.jacobcolvin.com/niceyaml/style/kind"
	"go.jacobcolvin.com/niceyaml/tokens"
)

var (
	// The error a failing reader reports.
	errReadFailed = errors.New("read failed")

	// A mapping of five keys, one on each line.
	sourceFiveLines = stringtest.LinesLF(
		"a: 1",
		"b: 2",
		"c: 3",
		"d: 4",
		"e: 5",
	)

	// Two documents with a byte order mark in front of the header
	// between them.
	sourceMarkedHeader = stringtest.LinesLF("a: 1", "\ufeff---", "b: 2")
)

func TestSource_View_Annotate(t *testing.T) {
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
				1: {Content: "middle", Placement: line.Below, Cols: position.NewSpan(2, 2)},
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

			view := niceyaml.NewSourceFromString(tc.input, niceyaml.WithName("test")).View()

			for idx, ann := range tc.annotations {
				require.Less(t, idx, view.Count(), "annotation index out of range")

				view.AnnotateLine(idx, ann)
			}

			assert.Equal(t, tc.want, view.String())
		})
	}
}

type runePosition struct {
	R   rune
	Pos position.Position
}

// runePositions returns each rune that seq yields, with its position, in
// order.
func runePositions(seq iter.Seq2[position.Position, rune]) []runePosition {
	var got []runePosition

	for pos, r := range seq {
		got = append(got, runePosition{R: r, Pos: pos})
	}

	return got
}

func TestSource_ZeroValue(t *testing.T) {
	t.Parallel()

	// A report reads the Source of each bound error, and a nil SourceError
	// returns a nil Source. Each read in the table answers for a nil Source
	// as it does for an empty Source, and so does each read of the zero
	// value. Offset and PositionAt tell a nil Source from the zero value.
	// The zero value holds an empty text, and a nil Source holds no text at
	// all.
	var bound *niceyaml.SourceError

	require.Nil(t, bound.Source())

	sources := map[string]*niceyaml.Source{
		"nil":        bound.Source(),
		"zero value": new(niceyaml.Source),
	}

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
		"excerpts": {
			read: func(s *niceyaml.Source) any { return s.ShowsExcerpts() },
			want: true,
		},
		"positions": {
			read: func(s *niceyaml.Source) any { return s.ReportsPositions() },
			want: true,
		},
		"text": {
			read: func(s *niceyaml.Source) any { return s.Text() },
			want: "",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			for via, source := range sources {
				assert.Equal(t, tc.want, tc.read(source), via)
			}
		})
	}

	t.Run("nil Source holds no text to count in", func(t *testing.T) {
		t.Parallel()

		_, ok := bound.Source().Offset(position.New(0, 0))
		assert.False(t, ok)
	})

	t.Run("zero value counts in an empty text", func(t *testing.T) {
		t.Parallel()

		// The zero value holds an empty text, as a Source of "" does, so
		// the start of that text is its one position and its one offset.
		var s niceyaml.Source

		got, ok := s.Offset(position.New(0, 0))
		assert.True(t, ok)
		assert.Equal(t, 0, got)

		_, ok = s.Offset(position.New(0, 1))
		assert.False(t, ok)

		pos, ok := s.PositionAt(0)
		assert.True(t, ok)
		assert.Equal(t, position.New(0, 0), pos)

		_, ok = s.PositionAt(1)
		assert.False(t, ok)
	})

	t.Run("zero value holds one empty document", func(t *testing.T) {
		t.Parallel()

		var s niceyaml.Source

		file, err := s.File()
		require.NoError(t, err)
		assert.NotNil(t, file)

		assert.True(t, yamltest.Documents(t, &s, 1)[0].IsEmpty())
	})
}

func TestSource_Runes(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input  string
		ranges []position.Range
		// The rune after which the loop stops, or 0 to read every rune.
		stop rune
		want []runePosition
	}{
		"simple key-value": {
			input: "a: b\n",
			want: []runePosition{
				{R: 'a', Pos: position.New(0, 0)},
				{R: ':', Pos: position.New(0, 1)},
				{R: ' ', Pos: position.New(0, 2)},
				{R: 'b', Pos: position.New(0, 3)},
				{R: '\n', Pos: position.New(0, 4)},
			},
		},
		"multi-line": {
			input: stringtest.LinesLF("a: 1", "b: 2"),
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
				{R: '\n', Pos: position.New(1, 4)},
			},
		},
		"utf8 - multibyte char": {
			input: "k: ü\n",
			want: []runePosition{
				{R: 'k', Pos: position.New(0, 0)},
				{R: ':', Pos: position.New(0, 1)},
				{R: ' ', Pos: position.New(0, 2)},
				{R: 'ü', Pos: position.New(0, 3)},
				{R: '\n', Pos: position.New(0, 4)},
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
				{R: '\n', Pos: position.New(0, 5)},
			},
		},
		"nested with indent": {
			input: stringtest.LinesLF(
				"p:",
				"  c: v",
			),
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
				{R: '\n', Pos: position.New(1, 6)},
			},
		},
		// The content of a literal block lies on the lines below its
		// header, and the block keeps its final line break.
		"literal block": {
			input: stringtest.LinesLF(
				"s: |",
				"  a",
				"  b",
			),
			want: []runePosition{
				{R: 's', Pos: position.New(0, 0)},
				{R: ':', Pos: position.New(0, 1)},
				{R: ' ', Pos: position.New(0, 2)},
				{R: '|', Pos: position.New(0, 3)},
				{R: '\n', Pos: position.New(0, 4)},
				{R: ' ', Pos: position.New(1, 0)},
				{R: ' ', Pos: position.New(1, 1)},
				{R: 'a', Pos: position.New(1, 2)},
				{R: '\n', Pos: position.New(1, 3)},
				{R: ' ', Pos: position.New(2, 0)},
				{R: ' ', Pos: position.New(2, 1)},
				{R: 'b', Pos: position.New(2, 2)},
				{R: '\n', Pos: position.New(2, 3)},
			},
		},
		"empty": {
			input: "",
			want:  nil,
		},
		"empty with a range": {
			input:  "",
			ranges: []position.Range{yamltest.LineRange(0, 0, 5)},
		},
		// The range covers "b: 2" on line 1, and leaves its end out.
		"single range filters runes correctly": {
			input:  sourceFiveLines,
			ranges: []position.Range{yamltest.LineRange(1, 0, 4)},
			want: []runePosition{
				{R: 'b', Pos: position.New(1, 0)},
				{R: ':', Pos: position.New(1, 1)},
				{R: ' ', Pos: position.New(1, 2)},
				{R: '2', Pos: position.New(1, 3)},
			},
		},
		// Line 1 from column 2 through its newline at column 4, then
		// columns 0 and 1 of line 2.
		"range spanning multiple lines": {
			input:  sourceFiveLines,
			ranges: []position.Range{position.NewRange(position.New(1, 2), position.New(2, 2))},
			want: []runePosition{
				{R: ' ', Pos: position.New(1, 2)},
				{R: '2', Pos: position.New(1, 3)},
				{R: '\n', Pos: position.New(1, 4)},
				{R: 'c', Pos: position.New(2, 0)},
				{R: ':', Pos: position.New(2, 1)},
			},
		},
		"out-of-range yields nothing": {
			input:  sourceFiveLines,
			ranges: []position.Range{yamltest.LineRange(100, 0, 10)},
		},
		"multiple ranges": {
			input: sourceFiveLines,
			ranges: []position.Range{
				yamltest.LineRange(0, 0, 1),
				yamltest.LineRange(2, 0, 1),
			},
			want: []runePosition{
				{R: 'a', Pos: position.New(0, 0)},
				{R: 'c', Pos: position.New(2, 0)},
			},
		},
		"early break": {
			input: "abc\n",
			stop:  'b',
			want: []runePosition{
				{R: 'a', Pos: position.New(0, 0)},
				{R: 'b', Pos: position.New(0, 1)},
			},
		},
		"early break with a range": {
			input:  sourceFiveLines,
			ranges: []position.Range{position.NewRange(position.New(0, 0), position.New(2, 10))},
			stop:   ':',
			want: []runePosition{
				{R: 'a', Pos: position.New(0, 0)},
				{R: ':', Pos: position.New(0, 1)},
			},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			source := niceyaml.NewSourceFromString(tc.input)

			// A case without ranges reads every rune.
			seq := source.Lines().Runes()
			if tc.ranges != nil {
				seq = source.Lines().RunesIn(tc.ranges...)
			}

			var got []runePosition

			for pos, r := range seq {
				got = append(got, runePosition{R: r, Pos: pos})
				if r == tc.stop {
					break
				}
			}

			assert.Equal(t, tc.want, got)
		})
	}

	t.Run("no range and a range past both ends return all runes", func(t *testing.T) {
		t.Parallel()

		source := niceyaml.NewSourceFromString(sourceFiveLines)
		require.Equal(t, 5, source.Lines().Len())

		all := runePositions(source.Lines().Runes())
		require.NotEmpty(t, all)
		assert.Equal(t, runePosition{R: 'a', Pos: position.New(0, 0)}, all[0])

		rng := position.NewRange(position.New(-5, 0), position.New(100, 100))
		assert.Equal(t, all, runePositions(source.Lines().RunesIn(rng)))
	})
}

func TestSource_Runes_DiffBuiltLines(t *testing.T) {
	t.Parallel()

	// When a diff builds Lines, Position.Line follows the visual line index
	// within Lines, not the source token position.
	// Finder maps its matches back onto those visual lines.
	before := niceyaml.NewSourceFromString("key: old\n", niceyaml.WithName("before"))
	after := niceyaml.NewSourceFromString("key: new\n", niceyaml.WithName("after"))

	// The diff holds the deleted line above the inserted one. Both come
	// from line 1 of their source, and each has a visual line of its own.
	lines := yamltest.DiffLines(t, before.Lines(), after.Lines()).Unified()
	require.Equal(t, 2, lines.Count())

	// The first character of each line.
	var got []position.Position

	for pos, r := range lines.Held().Runes() {
		if r == 'k' {
			got = append(got, pos)
		}
	}

	assert.Equal(t, []position.Position{position.New(0, 0), position.New(1, 0)}, got)
}

func TestSource_Lines_All(t *testing.T) {
	t.Parallel()

	source := niceyaml.NewSourceFromString(sourceFiveLines)

	require.Equal(t, 5, source.Lines().Len())

	tcs := map[string]struct {
		spans []position.Span
		// The number of lines the loop reads before it stops, or 0 to
		// read every line.
		limit int
		want  []int
	}{
		"no spans returns all lines": {
			want: []int{0, 1, 2, 3, 4},
		},
		"single span filters lines": {
			spans: []position.Span{position.NewSpan(1, 3)},
			want:  []int{1, 2},
		},
		"multiple spans iterate in order": {
			spans: []position.Span{position.NewSpan(0, 1), position.NewSpan(3, 5)},
			want:  []int{0, 3, 4},
		},
		"span clamped to bounds": {
			spans: []position.Span{position.NewSpan(-5, 100)},
			want:  []int{0, 1, 2, 3, 4},
		},
		"empty span yields nothing": {
			spans: []position.Span{position.NewSpan(2, 2)},
		},
		"span beyond length yields nothing": {
			spans: []position.Span{position.NewSpan(10, 20)},
		},
		"early break": {
			limit: 2,
			want:  []int{0, 1},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			// A case without spans reads every line.
			seq := source.Lines().All()
			if tc.spans != nil {
				seq = source.Lines().Within(tc.spans...)
			}

			var got []int

			for idx := range seq {
				got = append(got, idx)
				if len(got) == tc.limit {
					break
				}
			}

			assert.Equal(t, tc.want, got)
		})
	}
}

func TestSource_Lines(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input string
		// The content of each line.
		want []string
	}{
		"single line": {
			input: "key: value\n",
			want:  []string{"key: value"},
		},
		"no final line ending": {
			input: "key: value\nfoo: bar",
			want:  []string{"key: value", "foo: bar"},
		},
		"multiple lines": {
			input: stringtest.LinesLF("a: 1", "b: 2", "c: 3"),
			want:  []string{"a: 1", "b: 2", "c: 3"},
		},
		"empty": {input: ""},
		"nested yaml": {
			input: stringtest.LinesLF(
				"parent:",
				"  child: value",
			),
			want: []string{"parent:", "  child: value"},
		},
		"literal block": {
			input: stringtest.LinesLF(
				"key: |",
				"  line1",
				"  line2",
			),
			want: []string{"key: |", "  line1", "  line2"},
		},
		"blank line before an explicit key": {
			input: stringtest.LinesLF("tags: [a, b]", "", "? key", ": value"),
			want:  []string{"tags: [a, b]", "", "? key", ": value"},
		},
		"explicit key after a leading blank line": {
			input: stringtest.LinesLF("", "? key", ": value"),
			want:  []string{"", "? key", ": value"},
		},
		"text after a block scalar header at the end": {
			input: "key: |abc",
			want:  []string{"key: |abc"},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			lines := niceyaml.NewSourceFromString(tc.input).Lines()

			require.Equal(t, len(tc.want), lines.Len())
			assert.Equal(t, len(tc.want) == 0, lines.IsEmpty())

			for i, want := range tc.want {
				assert.Equal(t, want, lines.Line(i).Content(), "line %d", i)
			}

			assert.Equal(t, stringtest.JoinLF(tc.want...), lines.Content())
			require.NoError(t, yamltest.ValidateLines(lines))

			// A Source built from the bytes of the input holds the same
			// lines.
			fromBytes := niceyaml.NewSourceFromBytes([]byte(tc.input)).Lines()
			assert.Equal(t, lines.Content(), fromBytes.Content())
		})
	}
}

func TestSource_Tokens(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input string
	}{
		"mapping": {input: stringtest.LinesLF("key: value", "foo: bar")},
		"block scalar": {
			input: stringtest.LinesLF(
				"key: |",
				"  one",
				"  two",
				"foo: bar",
			),
		},
		"documents": {input: stringtest.LinesLF("a: 1", "---", "b: 2")},
		"empty":     {input: ""},
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

			assert.Equal(t, tc.numbers, yamltest.LineNumbers(src.Lines()))
			assert.Equal(t, tc.want, yamltest.Document(t, src).Span())
		})
	}
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
		yamltest.LineRange(1, 2, 7),
		yamltest.LineRange(2, 2, 7),
	}, content)
}

func TestSource_Lines_DroppedText(t *testing.T) {
	t.Parallel()

	// The lexer drops trailing spaces, the whitespace of a blank line, the
	// space in front of a ":" after a quoted key, and the code of an escape
	// in a double-quoted scalar. The lines still hold the whole source, the
	// position of each token leads back to it, and a blank line with a tab
	// before the first key still parses. The key after an escaped scalar
	// keeps its own line, so the parser reads the mapping the source holds.
	tcs := map[string]struct {
		input string
	}{
		"code of an escape in a double-quoted scalar": {input: `m: {a: "\u00e9", b: xx, c: yy}` + "\n"},
		"code of an escape after a tag":               {input: stringtest.LinesLF(`name: !!str "Caf\u00e9"`, "age: 3")},
		"blank line with a tab before the first key":  {input: stringtest.LinesLF(" \t", "a: 1")},
		"blank line of a tab before the first key":    {input: stringtest.LinesLF("\t", "a: 1")},
		"space before a colon after a quoted key":     {input: `{"a" : 1, "b" : "x"}` + "\n"},
		"trailing spaces":                             {input: stringtest.LinesLF("a: 1   ", "b: 2")},
		"blank line of spaces":                        {input: stringtest.LinesLF("a: 1", "   ", "b: 2")},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			source := niceyaml.NewSourceFromString(tc.input)

			assert.Equal(t, strings.TrimSuffix(tc.input, "\n"), source.Lines().Content())

			for _, tk := range source.Tokens() {
				assert.Same(t, tk, source.Lines().TokenAt(position.NewFromToken(tk)), "token %q", tk.Origin)
			}

			// The documents of the file parse.
			_, err := source.File()
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
		"blank line of spaces":  {input: stringtest.LinesLF("  ", "a: 1", "b: 2")},
		"blank line with a tab": {input: stringtest.LinesLF(" \t", "a: 1", "b: 2")},
		"blank line of a tab":   {input: stringtest.LinesLF("\t", "a: 1", "b: 2")},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			source := niceyaml.NewSourceFromString(tc.input)

			doc := yamltest.Document(t, source)

			keyTk, err := paths.NewResolver(doc.DocumentAST()).Token(paths.Doc().Child("a").Key())
			require.NoError(t, err)
			require.NotNil(t, keyTk)

			original := source.Lines().TokenAt(position.NewFromToken(keyTk))
			require.NotNil(t, original)
			assert.Equal(t, original.Origin, keyTk.Origin)
			assert.Equal(t, position.Ranges{
				yamltest.LineRange(1, 0, 1),
			}, source.Lines().ContentRanges(keyTk))

			assert.Equal(t,
				yamltest.DumpTokenOrigins(source.Tokens()),
				yamltest.DumpTokenOrigins(yamltest.At(t, doc, paths.Current()).Tokens()),
			)
			assert.Len(t, yamltest.At(t, doc, paths.Current().Child("a").Key()).Tokens(), 1)

			_, err = doc.Decode[struct{ B int }](t.Context(), niceyaml.WithUnknownFields(niceyaml.UnknownFieldsReject))
			require.ErrorIs(t, err, niceyaml.ErrDecode)
		})
	}
}

func TestSource_File_SecondSource(t *testing.T) {
	t.Parallel()

	// The File of a second Source of the same text parses as File does,
	// and no Node of the first Source shares it. The go-yaml parser
	// accepts each text from ForParser only when go-yaml accepts it.
	tcs := map[string]struct {
		input string
		// The error that the go-yaml parser reports for the stream that
		// ForParser returns, or empty when it parses.
		parserErr string
	}{
		"blank line of a tab": {
			input: stringtest.LinesLF("\t", "key: v"),
		},
		"comment below a flow collection": {
			input:     stringtest.LinesLF("[a, b]", "# trailing"),
			parserErr: "value is not allowed in this context",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			source := niceyaml.NewSourceFromString(tc.input)

			shared, err := source.File()
			require.NoError(t, err)

			own, err := source.WithText(source.Text()).File()
			require.NoError(t, err)

			assert.NotSame(t, shared, own)
			assert.Equal(t, shared.String(), own.String())

			_, err = parser.Parse(tokens.ForParser(source.Tokens()), parser.ParseComments)
			if tc.parserErr != "" {
				require.ErrorContains(t, err, tc.parserErr)

				return
			}

			require.NoError(t, err)
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
		"comment below the content": {
			input:       stringtest.LinesLF("a: |+", "  ", "# c", "b: 1"),
			wantComment: "# c",
		},
		"key below the content": {
			input: stringtest.LinesLF("a: |+", "  ", "b: 1"),
		},
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

			doc := yamltest.Document(t, source)

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
			input: stringtest.LinesLF(
				"- a: &x",
				"# c",
				"- 1",
			),
			want: []any{map[string]any{"a": nil}, uint64(1)},
			kept: true,
		},
		"comment left of the key above the next key": {
			input: stringtest.LinesLF(
				"a:",
				"  b: &x",
				"# c",
				"  d: 1",
			),
			want: map[string]any{"a": map[string]any{"b": nil, "d": uint64(1)}},
			kept: true,
		},
		"comment left of the entry above a key of an outer mapping": {
			input: stringtest.LinesLF(
				"a:",
				"  - &x",
				"# c",
				"b: 1",
			),
			want: map[string]any{"a": []any{nil}, "b": uint64(1)},
			kept: true,
		},
		"comment left of the entry above a key in its column": {
			input: stringtest.LinesLF(
				"p:",
				"  q:",
				"  - &x",
				"# c",
				"  n: 1",
			),
			want: map[string]any{"p": map[string]any{"q": []any{nil}, "n": uint64(1)}},
			kept: true,
		},
		"comment left of the key above the value": {
			input: stringtest.LinesLF(
				"a:",
				"  b: &x",
				"# c",
				"    d: 1",
			),
			want: map[string]any{"a": map[string]any{"b": map[string]any{"d": uint64(1)}}},
		},
		"comment left of the key above a sequence in the column of the key": {
			input: stringtest.LinesLF(
				"a:",
				"  b: &x",
				"# c",
				"  - 1",
			),
			want: map[string]any{"a": map[string]any{"b": []any{uint64(1)}}},
		},
		"comment left of the entry above the value": {
			input: stringtest.LinesLF(
				"- - &x",
				"# c",
				"    - 1",
			),
			want: []any{[]any{[]any{uint64(1)}}},
		},
		"comment in the column of the key above the value": {
			input: stringtest.LinesLF(
				"a: &x",
				"# c",
				"  b: 1",
			),
			want: map[string]any{"a": map[string]any{"b": uint64(1)}},
			kept: true,
		},
		"comment in the column of the key above a sequence in that column": {
			input: stringtest.LinesLF(
				"a: &x",
				"# c",
				"- 1",
			),
			want: map[string]any{"a": []any{uint64(1)}},
			kept: true,
		},
		"comment right of the entry above the value": {
			input: stringtest.LinesLF(
				"- &x",
				"    # c",
				"  b: 1",
			),
			want: []any{map[string]any{"b": uint64(1)}},
			kept: true,
		},
		"comment in the column of the key above the next key": {
			input: stringtest.LinesLF(
				"a: &x",
				"# c",
				"b: 1",
			),
			want: map[string]any{"a": nil, "b": uint64(1)},
		},
		"comment in the column of the entry above the next entry": {
			input: stringtest.LinesLF(
				"- &x",
				"# c",
				"- 1",
			),
			want: []any{nil, uint64(1)},
		},
		"comment right of the key above a key of an outer mapping": {
			input: stringtest.LinesLF(
				"a:",
				"  b: &x",
				"    # c",
				"d: 1",
			),
			want: map[string]any{"a": map[string]any{"b": nil}, "d": uint64(1)},
		},
		"comment right of the entry above an entry of an outer sequence": {
			input: stringtest.LinesLF(
				"- - &x",
				"    # c",
				"- 1",
			),
			want: []any{[]any{nil}, uint64(1)},
		},
		// An explicit key starts at its "?".
		"comment in the column of the ? above the next key": {
			input: stringtest.LinesLF(
				"? a: &x",
				"# c",
				"b: 1",
			),
			want: map[string]any{"a": nil, "b": uint64(1)},
		},
		"comment between the ? and its key above the next key": {
			input: stringtest.LinesLF(
				"p:",
				"  ? a: &x",
				"   # c",
				"  b: 1",
			),
			want: map[string]any{"p": map[string]any{"a": nil, "b": uint64(1)}},
		},
		"comment in the column of the ? above the next key of a sequence entry": {
			input: stringtest.LinesLF(
				"- ? a: &x",
				"  # c",
				"  b: 1",
			),
			want: []any{map[string]any{"a": nil, "b": uint64(1)}},
		},
		"comment in the column of the ? above the value": {
			input: stringtest.LinesLF(
				"? a: &x",
				"# c",
				"    b: 1",
			),
			want: map[string]any{"a": map[string]any{"b": uint64(1)}},
			kept: true,
		},
		"comment right of the ? above a value left of its key": {
			input: stringtest.LinesLF(
				"p:",
				"  ? a: &x",
				"    # c",
				"   b: 1",
			),
			want: map[string]any{"p": map[string]any{"a": map[string]any{"b": uint64(1)}}},
			kept: true,
		},
		"comment left of the ? above the value": {
			input: stringtest.LinesLF(
				"p:",
				"  ? a: &x",
				"# c",
				"   b: 1",
			),
			want: map[string]any{"p": map[string]any{"a": map[string]any{"b": uint64(1)}}},
		},
		// The parser reads any other anchor the same way with the comment
		// as without it.
		"comment below an anchor after a key whose ? is on the line above": {
			input: stringtest.LinesLF(
				"? ",
				" a: &x",
				"  # c",
				" b: 1",
			),
			want: map[string]any{"a": map[string]any{"b": uint64(1)}},
			kept: true,
		},
		// The tree holds a null in place of a comment that closes the
		// document below the anchor.
		"closing comment below an anchor after a key whose ? is on the line above": {
			input: stringtest.LinesLF(
				"  ? ",
				"    a: &x",
				"# c",
				"---",
			),
			want: map[string]any{"a": nil},
		},
		"comment left of the key below an anchor after a tag": {
			input: stringtest.LinesLF(
				"a:",
				"  b: !t &x",
				"# c",
				"    d: 1",
			),
			want: map[string]any{"a": map[string]any{"b": map[string]any{"d": uint64(1)}}},
			kept: true,
		},
		"comment left of the key below an anchor on a line of its own": {
			input: stringtest.LinesLF(
				"a:",
				"  b:",
				"    &x",
				"# c",
				"    d: 1",
			),
			want: map[string]any{"a": map[string]any{"b": map[string]any{"d": uint64(1)}}},
			kept: true,
		},
		// Above a comment left of its key that closes the document, the
		// parser itself gives the anchor a null value. The comment stays in
		// the tree at or right of the first key of the root. Left of that
		// key, the tree leaves it out above a header, and the parser
		// rejects it at the end of the source.
		"closing comment left of the key": {
			input: stringtest.LinesLF(
				"a:",
				"  b: &x",
				"# c",
			),
			want: map[string]any{"a": map[string]any{"b": nil}},
			kept: true,
		},
		"closing comment left of the root above a header": {
			input: stringtest.LinesLF(
				"  a: &x",
				"# c",
				"---",
			),
			want: map[string]any{"a": nil},
		},
		"closing comment left of the root": {
			input: stringtest.LinesLF(
				"  a: &x",
				"# c",
			),
			err: "2:1: value is not allowed in this context",
		},
		// Above a header, the tree first leaves out the comments left of
		// the root, and the first comment that remains decides. Left of the
		// key of the anchor, it stays in the tree. In the column of that
		// key, the parser takes it as the value of the anchor, and the tree
		// holds a null in its place.
		"closing comment left of the key below one left of the root": {
			input: stringtest.LinesLF(
				"  - a: &x",
				"# d",
				"  # c",
				"---",
			),
			want: []any{map[string]any{"a": nil}},
			kept: true,
		},
		"closing comment in the column of the key below one left of the root": {
			input: stringtest.LinesLF(
				"  a: &x",
				"# d",
				"  # c",
				"---",
			),
			want: map[string]any{"a": nil},
		},
		// At the end of the source, the tree leaves none of the comments
		// out first, so the parser rejects the one left of the root.
		"closing comments in and left of the column of the root": {
			input: stringtest.LinesLF(
				"  a: &x",
				"# d",
				"  # c",
			),
			err: "2:1: value is not allowed in this context",
		},
		// The go-yaml parser takes the token after a "&" as the name of the
		// anchor, so it rejects a "&" with no name above a comment. The
		// Source keeps that comment in the parser input wherever it sits.
		// Without the comment, the parser would name the anchor after the
		// token below it.
		"no name, comment left of the key above a scalar": {
			input: stringtest.LinesLF(
				"p:",
				"  a: &",
				"# c",
				"   1",
			),
			err: "3:1: unexpected scalar value type",
		},
		"no name, comment left of the key above a scalar and the next key": {
			input: stringtest.LinesLF(
				"p:",
				"  a: &",
				"# c",
				"   1",
				"z: 2",
			),
			err: "3:1: unexpected scalar value type",
		},
		"no name, comment in the column of the key above a tagged scalar left of it": {
			input: stringtest.LinesLF(
				"p:",
				"   q: 1",
				"   a: &",
				"   # c",
				"!t 1",
			),
			err: "4:4: unexpected scalar value type",
		},
		"no name, comment left of the entry above a scalar and the next entry": {
			input: stringtest.LinesLF(
				"- - &",
				"# c",
				"    1",
				"- 2",
			),
			err: "2:1: unexpected scalar value type",
		},
		"no name, comment above the end of a flow sequence": {
			input: stringtest.LinesLF(
				"[&",
				"# c",
				"]",
			),
			err: "2:1: unexpected scalar value type",
		},
		"no name, comment above a colon": {
			input: stringtest.LinesLF(
				"a: &",
				"# c",
				": 1",
			),
			err: "1:4: mapping value is not allowed in this context",
		},
		"no name, closing comment left of the root above a header": {
			input: stringtest.LinesLF(
				"  a: &",
				"# c",
				"---",
			),
			err: "2:1: unexpected scalar value type",
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

// tokenCollector is an [ast.Visitor] that passes the token of each node it
// visits to its function.
type tokenCollector func(tk *token.Token)

func (c tokenCollector) Visit(node ast.Node) ast.Visitor {
	if tk := node.GetToken(); tk != nil {
		c(tk)
	}

	return c
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
		tk, err := paths.NewResolver(doc.DocumentAST()).Token(paths.Doc().Child("other"))
		require.NoError(t, err)
		require.NotNil(t, tk)

		original := source.Lines().TokenAt(position.NewFromToken(tk))
		require.NotNil(t, original)
		assert.NotSame(t, original, tk)

		assert.Equal(t, position.Ranges{
			yamltest.LineRange(3, 7, 12),
		}, source.Lines().ContentRanges(tk))
		assert.Equal(t, source.Lines().TokenRanges(original), source.Lines().TokenRanges(tk))

		// A path to a key resolves to the key token, and a block scalar to
		// its indicator, each of which finds its own columns.
		keyTk, err := paths.NewResolver(doc.DocumentAST()).Token(paths.Doc().Child("other").Key())
		require.NoError(t, err)
		assert.Equal(t, position.Ranges{
			yamltest.LineRange(3, 0, 5),
		}, source.Lines().ContentRanges(keyTk))

		blockTk, err := paths.NewResolver(doc.DocumentAST()).Token(paths.Doc().Child("key"))
		require.NoError(t, err)
		assert.Equal(t, position.Ranges{
			yamltest.LineRange(0, 5, 6),
		}, source.Lines().ContentRanges(blockTk))
	})

	t.Run("every token of the file finds its ranges", func(t *testing.T) {
		t.Parallel()

		source := niceyaml.NewSourceFromString(yamltest.ReadFile(t, filepath.Join("testdata", "full.yaml")))

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

func TestNewSourceFromString_ByteOrderMark(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input string
		want  map[string]any
		lines string
	}{
		"before a comment": {
			input: stringtest.LinesLF("\ufeff# yaml-language-server: $schema=foo.json", "a: 1"),
			want:  map[string]any{"a": uint64(1)},
			lines: stringtest.JoinLF("# yaml-language-server: $schema=foo.json", "a: 1"),
		},
		"before a key": {
			input: "\ufeffa: 1\n",
			want:  map[string]any{"a": uint64(1)},
			lines: "a: 1",
		},
		"before a document marker": {
			input: stringtest.LinesLF("\ufeff---", "a: 1"),
			want:  map[string]any{"a": uint64(1)},
			lines: stringtest.JoinLF("---", "a: 1"),
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
			input: stringtest.LinesLF("a: 1", "---", "\ufeffb: 2"),
			lines: stringtest.JoinLF("a: 1", "---", "b: 2"),
			err:   "3:4: $.b: bad",
		},
		"before each document marker": {
			input: stringtest.LinesLF("\ufeff---", "a: 1", "\ufeff---", "b: 2"),
			lines: stringtest.JoinLF("---", "a: 1", "---", "b: 2"),
			err:   "4:4: $.b: bad",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			source := niceyaml.NewSourceFromString(tc.input)
			assert.Equal(t, tc.lines, source.Lines().Content())

			docs := yamltest.Documents(t, source, 2)

			got, err := docs[1].Decode[map[string]any](t.Context())
			require.NoError(t, err)
			assert.Equal(t, map[string]any{"b": uint64(2)}, got)

			// The positions count in the text without the marks, so an
			// error at the value of b points at it.
			err = docs[1].Bind(yamltest.ErrorAt("bad", "b"))
			assert.EqualError(t, err, tc.err)
		})
	}
}

func TestSource_Text(t *testing.T) {
	t.Parallel()

	// The lines and the token Origins each change some of these texts,
	// and Text returns every one as the Source read it.
	tcs := map[string]string{
		"empty":                     "",
		"LF":                        stringtest.LinesLF("a: 1", "b: 2"),
		"no final line ending":      "a: 1\nb: 2",
		"CRLF":                      stringtest.LinesCRLF("a: 1", "b: 2"),
		"bare CR":                   "a: 1\rb: 2\r",
		"trailing blank lines":      stringtest.LinesLF("a: 1", "", ""),
		"byte order mark":           "\ufeffa: 1\n",
		"mark at a document marker": sourceMarkedHeader,
		"invalid UTF-8":             "a: \"x\xffy\"\n",
		"tag that ends its line":    stringtest.LinesLF("a: !t", "b: 1"),
		"tag that ends the file":    "a: 1\nb: !t",
	}

	for name, text := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, text, niceyaml.NewSourceFromString(text).Text())
			assert.Equal(t, text, niceyaml.NewSourceFromBytes([]byte(text)).Text())

			fromReader, err := niceyaml.NewSourceFromReader(strings.NewReader(text))
			require.NoError(t, err)
			assert.Equal(t, text, fromReader.Text())

			fromFS, err := niceyaml.NewSourceFromFS(yamltest.MapFS(map[string]string{"a.yaml": text}), "a.yaml")
			require.NoError(t, err)
			assert.Equal(t, text, fromFS.Text())

			fromFile, err := niceyaml.NewSourceFromFile(yamltest.WriteFile(t, t.TempDir(), "a.yaml", text))
			require.NoError(t, err)
			assert.Equal(t, text, fromFile.Text())
		})
	}
}

func TestSource_DocumentAt(t *testing.T) {
	t.Parallel()

	source := niceyaml.NewSourceFromString(stringtest.JoinLF(
		"a: 1",
		"---",
		"b: 2",
		"---",
		"",
	))
	docs := yamltest.AllDocuments(t, source, 3)

	tcs := map[string]struct {
		pos  position.Position
		want int
		ok   bool
	}{
		"first line":             {pos: position.New(0, 0), want: 0, ok: true},
		"column past the end":    {pos: position.New(0, 40), want: 0, ok: true},
		"header of the second":   {pos: position.New(1, 0), want: 1, ok: true},
		"second document":        {pos: position.New(2, 3), want: 1, ok: true},
		"header of an empty one": {pos: position.New(3, 0), want: 2, ok: true},
		"line before the source": {pos: position.New(-1, 0)},
		"line past the source":   {pos: position.New(4, 0)},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, ok := source.DocumentAt(tc.pos)
			require.Equal(t, tc.ok, ok)

			if !tc.ok {
				assert.Nil(t, got)

				return
			}

			assert.Same(t, docs[tc.want], got)

			// Bind routes an error at the position to the same document.
			bound := yamltest.RequireSourceError(t, source.Bind(niceyaml.NewError("m", niceyaml.AtPosition(tc.pos))))
			assert.Same(t, got, yamltest.BoundDocument(t, bound))
		})
	}
}

func TestSource_DocumentAt_NilSource(t *testing.T) {
	t.Parallel()

	var source *niceyaml.Source

	got, ok := source.DocumentAt(position.New(0, 0))
	assert.False(t, ok)
	assert.Nil(t, got)
}

func TestSource_Offset(t *testing.T) {
	t.Parallel()

	// Two lines, each of which ends in a line break.
	lf := stringtest.LinesLF("a: 1", "b: 2")
	crlf := stringtest.LinesCRLF("a: 1", "b: 2")

	tcs := map[string]struct {
		input string
		line  int
		col   int
		want  int
		ok    bool
	}{
		"start of the text": {
			input: lf,
			want:  0,
			ok:    true,
		},
		"column of a later line": {
			input: lf,
			line:  1,
			col:   3,
			want:  8,
			ok:    true,
		},
		"rune of several bytes takes one column": {
			input: "é: 日本\n",
			line:  0,
			col:   4,
			want:  7,
			ok:    true,
		},
		"LF takes the column after the content": {
			input: lf,
			line:  0,
			col:   4,
			want:  4,
			ok:    true,
		},
		"column past a LF is the next line": {
			input: lf,
			line:  0,
			col:   5,
			want:  5,
			ok:    true,
		},
		"CRLF takes one column": {
			input: crlf,
			line:  0,
			col:   4,
			want:  4,
			ok:    true,
		},
		"column past a CRLF is the next line": {
			input: crlf,
			line:  0,
			col:   5,
			want:  6,
			ok:    true,
		},
		"line after a CRLF": {
			input: crlf,
			line:  1,
			col:   0,
			want:  6,
			ok:    true,
		},
		"line after a bare CR": {
			input: "a: 1\rb: 2\r",
			line:  1,
			col:   0,
			want:  5,
			ok:    true,
		},
		"end of a text with no final line ending": {
			input: "a: 1",
			line:  0,
			col:   4,
			want:  4,
			ok:    true,
		},
		"past the end of a text with no final line ending": {
			input: "a: 1",
			line:  0,
			col:   5,
		},
		"line after a text with no final line ending": {
			input: "a: 1",
			line:  1,
			col:   0,
		},
		"end of a text after its final line ending": {
			input: "a: 1\n",
			line:  1,
			col:   0,
			want:  5,
			ok:    true,
		},
		"past the end of a text after its final line ending": {
			input: "a: 1\n",
			line:  1,
			col:   1,
		},
		"line past the last": {
			input: "a: 1\n",
			line:  2,
			col:   0,
		},
		"column past a line ending": {
			input: lf,
			line:  0,
			col:   6,
		},
		"empty text": {
			input: "",
			want:  0,
			ok:    true,
		},
		"past the end of an empty text": {
			input: "",
			line:  0,
			col:   1,
		},
		"negative line": {
			input: "a: 1\n",
			line:  -1,
			col:   0,
		},
		"negative column": {
			input: "a: 1\n",
			line:  0,
			col:   -1,
		},
		"byte order mark lies before column 0": {
			input: "\ufeffa: 1\n",
			want:  3,
			ok:    true,
		},
		"column after a byte order mark": {
			input: "\ufeffa: 1\n",
			line:  0,
			col:   3,
			want:  6,
			ok:    true,
		},
		"mark at a document marker lies before column 0": {
			input: sourceMarkedHeader,
			line:  1,
			col:   0,
			want:  8,
			ok:    true,
		},
		"line after a mark at a document marker": {
			input: sourceMarkedHeader,
			line:  2,
			col:   3,
			want:  15,
			ok:    true,
		},
		"mark between a bare CR and a LF": {
			input: "\r\ufeff\na: 1\n",
			line:  1,
			col:   3,
			want:  8,
			ok:    true,
		},
		"line ending around a mark": {
			input: "\r\ufeff\na: 1\n",
			want:  0,
			ok:    true,
		},
		"mark the tokenizer keeps takes one column": {
			input: "a: 1\n\ufeffb: 2\n",
			line:  1,
			col:   1,
			want:  8,
			ok:    true,
		},
		"invalid UTF-8 byte takes one column": {
			input: "a: \"x\xffy\"\n",
			line:  0,
			col:   6,
			want:  6,
			ok:    true,
		},
		"line after a tag that ends its line": {
			input: stringtest.LinesLF("a: !t", "b: 1"),
			line:  1,
			col:   3,
			want:  9,
			ok:    true,
		},
		"text the lexer drops": {
			input: "a: 1\nb: !t",
			line:  1,
			col:   4,
			want:  9,
			ok:    true,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			source := niceyaml.NewSourceFromString(tc.input)

			got, ok := source.Offset(position.New(tc.line, tc.col))
			assert.Equal(t, tc.ok, ok)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestSource_Offset_Runes(t *testing.T) {
	t.Parallel()

	tcs := map[string]string{
		"LF": stringtest.LinesLF(
			"a: 1",
			"b:",
			"  - x",
			"  - y",
		),
		"CRLF": stringtest.LinesCRLF(
			"a: 1",
			"b:",
			"  - x",
			"  - y",
		),
		"bare CR":                   "a: 1\rb: 2\r",
		"no final line ending":      "a: 1\nb: 2",
		"byte order mark":           stringtest.LinesLF("\ufeff# c", "a: 1"),
		"mark at a document marker": sourceMarkedHeader,
		"wide runes":                stringtest.LinesLF(`é: "日本語" # 😀`, "b: 2"),
		"invalid UTF-8":             stringtest.LinesLF("a: \"x\xffy\"", "b: 2"),
		"block scalar": stringtest.LinesCRLF(
			"a: |",
			"  x",
			"",
			"  y",
			"b: 2",
		),
		"tag that ends its line": stringtest.LinesLF(
			"a: !t",
			"  - x",
			"b: 1",
		),
		"full.yaml": yamltest.ReadFile(t, filepath.Join("testdata", "full.yaml")),
	}

	for name, input := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			source := niceyaml.NewSourceFromString(input)
			text := source.Text()

			var count int

			for pos, want := range source.Lines().Runes() {
				offset, ok := source.Offset(pos)
				require.True(t, ok, "position %s", pos)
				require.Less(t, offset, len(text), "position %s", pos)

				got, _ := utf8.DecodeRuneInString(text[offset:])
				if got == '\r' {
					// The lines yield every line ending as "\n".
					got = '\n'
				}

				require.Equal(t, string(want), string(got), "position %s", pos)

				count++
			}

			assert.Positive(t, count)
		})
	}
}

func TestSource_Offset_Range(t *testing.T) {
	t.Parallel()

	// Each case writes 5 over the 3 of its replicas, between the offsets
	// of the range the path resolves to.
	tcs := map[string]struct {
		input string
		path  paths.Path
	}{
		"plain scalar": {
			input: stringtest.LinesLF(
				"spec:",
				"  replicas: 3 # scaled",
				"  paused: false",
			),
			path: paths.Doc().Child("spec", "replicas"),
		},
		"CRLF and a byte order mark": {
			input: stringtest.LinesCRLF(
				"\ufeffspec:",
				"  replicas: 3",
			),
			path: paths.Doc().Child("spec", "replicas"),
		},
		"after a tag that ends its line": {
			input: stringtest.LinesLF(
				"value: !Join",
				"  - a",
				"  - b",
				"replicas: 3",
			),
			path: paths.Doc().Child("replicas"),
		},
		"last value of a text with no final line ending": {
			input: "name: é日本\nreplicas: 3",
			path:  paths.Doc().Child("replicas"),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			source := niceyaml.NewSourceFromString(tc.input)

			doc := yamltest.Document(t, source)

			ranges, err := doc.Ranges(tc.path)
			require.NoError(t, err)
			require.Len(t, ranges, 1)

			start, ok := source.Offset(ranges[0].Start)
			require.True(t, ok)

			end, ok := source.Offset(ranges[0].End)
			require.True(t, ok)

			text := source.Text()
			want := strings.Replace(tc.input, "replicas: 3", "replicas: 5", 1)
			assert.Equal(t, want, text[:start]+"5"+text[end:])
		})
	}
}

func TestSource_Parse(t *testing.T) {
	t.Parallel()

	t.Run("valid YAML", func(t *testing.T) {
		t.Parallel()

		tcs := map[string]struct {
			input string
			// The number of documents in the file, and the number
			// AllDocuments returns.
			want, all int
		}{
			"simple key-value": {
				input: "key: value\n",
				want:  1,
				all:   1,
			},
			"nested map": {
				input: stringtest.Input(`
					parent:
					  child: value
				`),
				want: 1,
				all:  1,
			},
			"multiple documents": {
				input: stringtest.Input(`
					---
					doc1: value1
					---
					doc2: value2
				`),
				want: 2,
				all:  2,
			},
			"consecutive headers": {
				input: stringtest.Input(`
					---
					---
				`),
				want: 2,
				all:  2,
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
				all:  3,
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
				all:  3,
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
				all:  2,
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
				all:  3,
			},
			"anchor with no value before a header": {
				input: stringtest.Input(`
					a: &x
					---
					b: 1
				`),
				want: 2,
				all:  2,
			},
			"anchored entry with no value before a header": {
				input: stringtest.Input(`
					- &x
					---
				`),
				want: 2,
				all:  2,
			},
			"tag directive for the secondary handle": {
				// The directive holds for the first document alone. Carried
				// into the second, it made the parser panic on the !!str
				// tag with no value. The parser puts the directive in a
				// node of its own.
				input: stringtest.LinesLF(
					"%TAG !! tag:example.com,2000:app/",
					"---",
					"!!int 1 - 3",
					"---",
					"- !!str",
				),
				want: 3,
				all:  2,
			},
			"list": {
				input: stringtest.Input(`
					items:
					  - one
					  - two
				`),
				want: 1,
				all:  1,
			},
			// The go-yaml parser returns one empty document for an empty
			// input.
			"empty source": {
				input: "",
				want:  1,
				all:   1,
			},
			// The parser merges a header that an end marker closes into the
			// next document, which then holds two headers.
			"end marker between headers": {
				input: stringtest.Input(`
					---
					...
					---
					a: 1
				`),
				want: 2,
				all:  2,
			},
			"end marker between headers with a comment": {
				input: stringtest.Input(`
					--- # c
					...
					---
				`),
				want: 2,
				all:  2,
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
				all:  3,
			},
			"consecutive headers before an end marker": {
				input: stringtest.Input(`
					---
					---
					...
					---
				`),
				want: 3,
				all:  3,
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				source := niceyaml.NewSourceFromString(tc.input)
				file, err := source.File()
				require.NoError(t, err)
				assert.Len(t, file.Docs, tc.want)
				assert.Len(t, source.AllDocuments(), tc.all)
			})
		}
	})

	t.Run("syntax error comes back bound to the source", func(t *testing.T) {
		t.Parallel()

		source := niceyaml.NewSourceFromString("a: b: c\n")

		_, err := source.File()
		require.Error(t, err)

		bound, ok := err.(*niceyaml.SourceError) //nolint:errorlint // The top-level value is the bound error.
		require.True(t, ok, "want *niceyaml.SourceError, got %T", err)
		assert.Same(t, source, bound.Source())

		excerpt, ok := bound.Excerpt()
		require.True(t, ok)

		detail := yamltest.NewXMLPrinter().Print(excerpt)
		assert.Contains(t, detail, "<genericError>b</genericError>", "the offending token is highlighted")

		// The document returns the same bound error.
		assert.Same(t, bound, yamltest.Documents(t, source, 1)[0].Err())
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

		_, ok = bound.Excerpt()
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
				input: stringtest.LinesLF(
					"a:",
					"\tb: 1",
				),
				err: "t.yaml:2:2: found character '\u2409' that cannot start any token",
				want: stringtest.JoinLF(
					"t.yaml:2:2: found character '\u2409' that cannot start any token",
					"",
					"   1 | a:",
					"   2 | \u2409b: 1",
					"     |  ^",
				),
			},
			"tab alone on a line": {
				input: stringtest.LinesLF("a:", "\t", "b: 1"),
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
				input: stringtest.LinesLF(
					"a: 1",
					"\t- x",
				),
				err: "t.yaml:1:4: found character '\u2409' that cannot start any token",
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

				assert.Equal(t, tc.want, niceyaml.FormatError(err, niceyaml.WithContextLines(1)))
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
			input: stringtest.LinesLF("a: 1", "a: 2"),
			want:  []bool{true},
		},
		"duplicate key the source allows": {
			input: stringtest.LinesLF("a: 1", "a: 2"),
			opts:  []niceyaml.SourceOption{niceyaml.WithAllowDuplicateKeys(true)},
			want:  []bool{false},
		},
		"tab indents a key": {
			input: stringtest.LinesLF(
				"a:",
				"\tb: 1",
			),
			want: []bool{true},
		},
		"unknown escape": {
			input: `a: "\q"` + "\n",
			want:  []bool{true},
		},
		"second of three documents": {
			input: stringtest.LinesLF(
				"a: 1",
				"---",
				"b: [",
				"---",
				"c: 3",
			),
			want: []bool{false, true, false},
		},
		"first and third of three documents": {
			input: stringtest.LinesLF(
				"a: [",
				"---",
				"b: 2",
				"---",
				"c: @x",
			),
			want: []bool{true, false, true},
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

			all := yamltest.AllDocuments(t, source, len(tc.want))

			for i, doc := range all {
				if !tc.want[i] {
					require.NoError(t, doc.Err())

					_, decodeErr := doc.Decode[any](t.Context())
					require.NotErrorIs(t, decodeErr, niceyaml.ErrSyntax)

					continue
				}

				require.ErrorIs(t, doc.Err(), niceyaml.ErrSyntax)

				_, decodeErr := doc.Decode[any](t.Context())
				require.ErrorIs(t, decodeErr, niceyaml.ErrSyntax)

				// [errors.As] still finds the go-yaml error.
				var syntaxErr *yaml.SyntaxError

				require.ErrorAs(t, doc.Err(), &syntaxErr)
			}

			file, fileErr := source.File()
			docs := source.Documents()

			if !slices.Contains(tc.want, true) {
				require.NoError(t, fileErr)
				assert.NotNil(t, file)

				assert.Equal(t, all, docs)

				return
			}

			// One error or a join of several, which matches through each.
			require.ErrorIs(t, fileErr, niceyaml.ErrSyntax)
			assert.Nil(t, file)

			// Documents returns every document with content, whether it
			// parsed or not.
			assert.Equal(t, all, docs)

			_, err := source.Document()
			require.ErrorIs(t, err, niceyaml.ErrSyntax)

			_, err = source.Decode[any](t.Context())
			require.ErrorIs(t, err, niceyaml.ErrSyntax)
		})
	}
}

func TestSource_File_ErrEncoding(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input string
		opts  []niceyaml.SourceOption
		// The error the parse returns, with its details and its excerpt,
		// or "" when the text parses.
		err string
	}{
		"utf-8": {
			input: "name: caf\u00e9\n",
		},
		"utf-8 with a byte order mark": {
			input: "\ufeffname: caf\u00e9\n",
		},
		"latin-1 byte": {
			input: stringtest.LinesLF("name: caf\xe9", "port: 80"),
			opts:  []niceyaml.SourceOption{niceyaml.WithName("latin1.yaml")},
			err: stringtest.JoinLF(
				"latin1.yaml:1:10: byte 0xe9 is not UTF-8",
				"`-- encode the text as UTF-8",
				"",
				"   1 | name: caf\ufffd",
				"     |          ^",
				"   2 | port: 80",
			),
		},
		"latin-1 byte in a comment": {
			input: stringtest.LinesLF("# caf\xe9", "port: 80"),
			opts:  []niceyaml.SourceOption{niceyaml.WithName("latin1.yaml")},
			err: stringtest.JoinLF(
				"latin1.yaml:1:6: byte 0xe9 is not UTF-8",
				"`-- encode the text as UTF-8",
				"",
				"   1 | # caf\ufffd",
				"     |      ^",
				"   2 | port: 80",
			),
		},
		"latin-1 byte of a file": {
			input: "name: caf\xe9\n",
			opts:  []niceyaml.SourceOption{niceyaml.WithFilePath("latin1.yaml")},
			err: stringtest.JoinLF(
				"latin1.yaml:1:10: byte 0xe9 is not UTF-8",
				"`-- save the file as UTF-8",
				"",
				"   1 | name: caf\ufffd",
				"     |          ^",
			),
		},
		"latin-1 byte of a later document": {
			input: stringtest.LinesLF("a: 1", "---", "b: caf\xe9"),
			opts:  []niceyaml.SourceOption{niceyaml.WithName("latin1.yaml")},
			err: stringtest.JoinLF(
				"latin1.yaml:3:7: byte 0xe9 is not UTF-8",
				"`-- encode the text as UTF-8",
				"",
				"   1 | a: 1",
				"   2 | ---",
				"   3 | b: caf\ufffd",
				"     |       ^",
			),
		},
		"utf-16 byte order mark": {
			input: "\xff\xfen\x00:\x00 \x00a\x00\n\x00",
			opts:  []niceyaml.SourceOption{niceyaml.WithName("utf16.yaml")},
			err:   stringtest.JoinLF("utf16.yaml: text is UTF-16, not UTF-8", "`-- encode the text as UTF-8"),
		},
		"utf-16 without a byte order mark": {
			input: "\x00n\x00:",
			opts:  []niceyaml.SourceOption{niceyaml.WithFilePath("utf16.yaml")},
			err:   stringtest.JoinLF("utf16.yaml: file is UTF-16, not UTF-8", "`-- save the file as UTF-8"),
		},
		"utf-32 byte order mark": {
			input: "\xff\xfe\x00\x00n\x00\x00\x00",
			opts:  []niceyaml.SourceOption{niceyaml.WithName("utf32.yaml")},
			err:   stringtest.JoinLF("utf32.yaml: text is UTF-32, not UTF-8", "`-- encode the text as UTF-8"),
		},
		"utf-32 without a byte order mark": {
			input: "n\x00\x00\x00",
			opts:  []niceyaml.SourceOption{niceyaml.WithName("utf32.yaml")},
			err:   stringtest.JoinLF("utf32.yaml: text is UTF-32, not UTF-8", "`-- encode the text as UTF-8"),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			source := niceyaml.NewSourceFromString(tc.input, tc.opts...)

			_, fileErr := source.File()
			if tc.err == "" {
				require.NoError(t, fileErr)

				return
			}

			require.ErrorIs(t, fileErr, niceyaml.ErrEncoding)
			require.NotErrorIs(t, fileErr, niceyaml.ErrSyntax)
			assert.True(t, niceyaml.IsInvalid(fileErr))
			assert.Equal(t, tc.err, niceyaml.FormatError(fileErr))

			// The whole text is one document that carries the error, and the
			// lines keep the text.
			doc := yamltest.AllDocuments(t, source, 1)[0]
			assert.Same(t, fileErr, doc.Err())
			assert.Nil(t, doc.AST())
			assert.Equal(t, source.Tokens(), doc.Tokens())
			assert.Equal(t, tc.input, source.Text())

			_, err := source.Document()
			assert.Same(t, fileErr, err)

			_, err = source.Decode[any](t.Context())
			require.ErrorIs(t, err, niceyaml.ErrEncoding)

			err = source.ValidateDocuments(t.Context(), niceyaml.NoValidator())
			require.ErrorIs(t, err, niceyaml.ErrEncoding)
		})
	}
}

func TestSource_File_ErrEncodingPastByteLimit(t *testing.T) {
	t.Parallel()

	source := niceyaml.NewSourceFromString("name: caf\xe9\n", niceyaml.WithName("big.yaml"), niceyaml.WithByteLimit(4))

	_, err := source.File()
	require.ErrorIs(t, err, niceyaml.ErrLimit)
	require.NotErrorIs(t, err, niceyaml.ErrEncoding)
	assert.Equal(t, "big.yaml: file is larger than 4 bytes", err.Error())
}

func TestSource_File_ErrEncodingLossyRead(t *testing.T) {
	t.Parallel()

	// The function strings.ToValidUTF8 puts one U+FFFD in place of each
	// run of bytes that UTF-8 does not allow. So the run of two reads as
	// one.
	text := "name: caf\xe9\xe9\n"

	_, err := niceyaml.NewSourceFromString(text).Decode[map[string]string](t.Context())
	require.ErrorIs(t, err, niceyaml.ErrEncoding)

	lossy := niceyaml.NewSourceFromString(strings.ToValidUTF8(text, "\ufffd"))

	got, err := lossy.Decode[map[string]string](t.Context())
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"name": "caf\ufffd"}, got)
}

func TestSource_AllDocuments(t *testing.T) {
	t.Parallel()

	t.Run("file with a syntax error holds every document", func(t *testing.T) {
		t.Parallel()

		source := niceyaml.NewSourceFromString(
			stringtest.LinesLF(
				"a: 1",
				"---",
				"b: 2",
				"---",
				"a: [",
				"---",
				"c: 3",
			),
			niceyaml.WithName("cfg.yaml"),
		)

		_, fileErr := source.File()
		require.EqualError(t, fileErr, "cfg.yaml:5:4: "+unclosedSequence)

		// Documents returns the document that did not parse beside the
		// others, with its error on the Node.
		assert.Same(t, fileErr, yamltest.Documents(t, source, 4)[2].Err())

		all := yamltest.AllDocuments(t, source, 4)

		require.NoError(t, all[0].Err())
		require.NoError(t, all[1].Err())
		assert.Same(t, fileErr, all[2].Err())
		require.NoError(t, all[3].Err())

		// The document below the one that did not parse decodes.
		last, err := all[3].Decode[map[string]int](t.Context())
		require.NoError(t, err)
		assert.Equal(t, map[string]int{"c": 3}, last)
	})

	t.Run("two documents that parse together return one syntax error", func(t *testing.T) {
		t.Parallel()

		source := niceyaml.NewSourceFromString(stringtest.LinesLF("a: &x", "---", "b: ["))

		all := yamltest.AllDocuments(t, source, 2)

		require.ErrorIs(t, all[0].Err(), niceyaml.ErrSyntax)
		assert.Same(t, all[0].Err(), all[1].Err())

		// ValidateDocuments reports the error both documents return once.
		assert.Same(t, all[0].Err(), source.ValidateDocuments(t.Context(), niceyaml.NoValidator()))

		// The one error is bound to the document that holds its location,
		// whichever of the two returns it.
		var cfg map[string]any

		bound := yamltest.RequireSourceError(t, all[0].DecodeInto(t.Context(), &cfg))
		assert.Same(t, all[1], yamltest.BoundDocument(t, bound))
	})

	t.Run("each syntax error is bound to the document that holds it", func(t *testing.T) {
		t.Parallel()

		tcs := map[string]struct {
			input string
			// The index of the document each syntax error of the file is
			// bound to, in file order.
			want []int
		}{
			"only document": {
				input: "a: [1\n",
				want:  []int{0},
			},
			"first of three": {
				input: stringtest.LinesLF(
					"a: [1",
					"---",
					"b: 2",
					"---",
					"c: 3",
				),
				want: []int{0},
			},
			"second of three": {
				input: stringtest.LinesLF(
					"a: 1",
					"---",
					"b: [1, 2",
					"---",
					"c: 3",
				),
				want: []int{1},
			},
			"last of three": {
				input: stringtest.LinesLF(
					"a: 1",
					"---",
					"b: 2",
					"---",
					"c: [3",
				),
				want: []int{2},
			},
			"first and last of three": {
				input: stringtest.LinesLF(
					"a: [1",
					"---",
					"b: 2",
					"---",
					"c: [3",
				),
				want: []int{0, 2},
			},
			"second of two that parse together": {
				input: stringtest.LinesLF("a: &x", "---", "b: [1"),
				want:  []int{1},
			},
			"first of two that parse together": {
				input: stringtest.LinesLF("a: [1", "b: &x", "---", "c: 1"),
				want:  []int{0},
			},
			"second of two that parse together below another": {
				input: stringtest.LinesLF(
					"k: 1",
					"---",
					"a: &x",
					"---",
					"b: [1",
					"---",
					"c: 2",
				),
				want: []int{2},
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				source := niceyaml.NewSourceFromString(tc.input)

				_, err := source.File()
				require.ErrorIs(t, err, niceyaml.ErrSyntax)

				docs := source.AllDocuments()

				var got []int

				for bound := range niceyaml.Bindings(err) {
					doc := yamltest.BoundDocument(t, bound)
					assert.Same(t, yamltest.BoundNode(t, bound), doc)
					assert.Same(t, docs[doc.DocumentIndex()], doc)
					assert.Same(t, error(bound), doc.Err(), "the document returns the error bound to it")
					assert.True(t, doc.Span().Contains(yamltest.RangeOf(t, bound).Start.Line))

					got = append(got, doc.DocumentIndex())
				}

				assert.Equal(t, tc.want, got)
			})
		}
	})

	t.Run("File and AllDocuments from two goroutines return the bound error", func(t *testing.T) {
		t.Parallel()

		// Binding a syntax error to its document writes to the error. The
		// race detector fails the test when one goroutine reads the error
		// File returned while the other still builds the documents.
		for range 20 {
			source := niceyaml.NewSourceFromString(stringtest.LinesLF(
				"a: 1",
				"---",
				"b: [1, 2",
				"---",
				"c: 3",
			))

			var (
				wg                 sync.WaitGroup
				fromFile, fromDocs *niceyaml.Node
			)

			wg.Go(func() {
				_, err := source.File()

				for bound := range niceyaml.Bindings(err) {
					fromFile, _ = bound.Document()
				}
			})

			wg.Go(func() {
				for bound := range niceyaml.Bindings(source.AllDocuments()[1].Err()) {
					fromDocs, _ = bound.Document()
				}
			})

			wg.Wait()

			require.NotNil(t, fromFile)
			assert.Same(t, fromFile, fromDocs)
			assert.Same(t, source.AllDocuments()[1], fromFile)
		}
	})
}

// unclosedSequence is the message of the syntax error for a flow sequence
// with no "]".
const unclosedSequence = "sequence end token ']' not found"

func TestSource_Documents_CollectErrors(t *testing.T) {
	t.Parallel()

	// A caller that reports on each document collects the error of every
	// document Documents returns, parsed or not.
	tolerant := func(t *testing.T, source *niceyaml.Source, wrap func(error) error) error {
		t.Helper()

		var errs []error

		for _, doc := range source.Documents() {
			_, err := doc.Decode[map[string]any](t.Context())
			if err != nil {
				errs = append(errs, wrap(err))
			}
		}

		return errors.Join(errs...)
	}

	// A caller that needs the whole file to parse returns the error of
	// File, and decodes each document Documents returns otherwise.
	strict := func(t *testing.T, source *niceyaml.Source, wrap func(error) error) error {
		t.Helper()

		_, err := source.File()
		if err != nil {
			return wrap(err)
		}

		return tolerant(t, source, wrap)
	}

	wraps := map[string]func(error) error{
		"as returned": func(err error) error { return err },
		"wrapped":     func(err error) error { return fmt.Errorf("load: %w", err) },
	}

	tcs := map[string]struct {
		input string
		// The message of each binding the strict caller collects.
		strict []string
		// The message of each binding a loop over Documents collects.
		tolerant []string
	}{
		"file that parses": {input: stringtest.LinesLF("a: 1", "---", "b: 2")},
		"one syntax error": {
			input: stringtest.LinesLF(
				"a: 1",
				"---",
				"b: 2",
				"---",
				"a: [",
				"---",
				"c: 3",
			),
			strict:   []string{"cfg.yaml:5:4: " + unclosedSequence},
			tolerant: []string{"cfg.yaml:5:4: " + unclosedSequence},
		},
		"two syntax errors": {
			input: stringtest.LinesLF(
				"a: [",
				"---",
				"b: 1",
				"---",
				"c: [",
			),
			strict:   []string{"cfg.yaml:1:4: " + unclosedSequence, "cfg.yaml:5:4: " + unclosedSequence},
			tolerant: []string{"cfg.yaml:1:4: " + unclosedSequence, "cfg.yaml:5:4: " + unclosedSequence},
		},
		// The header parses together with the document above it, so both
		// documents return the one error.
		"syntax error two documents share": {
			input:    stringtest.LinesLF("a: &x", "---", "b: ["),
			strict:   []string{"cfg.yaml:3:4: " + unclosedSequence},
			tolerant: []string{"cfg.yaml:3:4: " + unclosedSequence, "cfg.yaml:3:4: " + unclosedSequence},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			for wrapName, wrap := range wraps {
				t.Run(wrapName, func(t *testing.T) {
					t.Parallel()

					source := niceyaml.NewSourceFromString(tc.input, niceyaml.WithName("cfg.yaml"))

					// Each binding reads as one line of the message.
					err := strict(t, source, wrap)
					assert.Equal(t, tc.strict, bindingMessages(err))
					assert.Len(t, messageLines(err), len(tc.strict))

					err = tolerant(t, source, wrap)
					assert.Equal(t, tc.tolerant, bindingMessages(err))
					assert.Len(t, messageLines(err), len(tc.tolerant))
				})
			}
		})
	}
}

// messageLines returns the lines of the message of err, which are none
// for a nil err.
func messageLines(err error) []string {
	if err == nil {
		return nil
	}

	return strings.Split(err.Error(), "\n")
}

func TestSource_AllDocuments_SyntaxError(t *testing.T) {
	t.Parallel()

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
			want:  []string{"f.yaml:1:4: " + unclosedSequence},
			spans: []position.Span{position.NewSpan(0, 1)},
		},
		"first of two": {
			input: stringtest.LinesLF("a: [", "---", "b: 1"),
			want:  []string{"f.yaml:1:4: " + unclosedSequence, ""},
			spans: []position.Span{position.NewSpan(0, 1), position.NewSpan(1, 3)},
		},
		"last of three": {
			input: stringtest.LinesLF(
				"a: 1",
				"---",
				"b: 2",
				"---",
				"c: [",
			),
			want:  []string{"", "", "f.yaml:5:4: " + unclosedSequence},
			spans: []position.Span{position.NewSpan(0, 1), position.NewSpan(1, 3), position.NewSpan(3, 5)},
		},
		"second and fourth of five": {
			input: stringtest.LinesLF(
				"a: 1",
				"---",
				"b: [",
				"---",
				"c: 3",
				"---",
				"d: @x",
				"---",
				"e: 5",
			),
			want: []string{
				"",
				"f.yaml:3:4: " + unclosedSequence,
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
			input: stringtest.LinesLF(`a: "x`, "---", "b: 2"),
			want:  []string{"f.yaml:1:4: found unexpected document separator", ""},
			spans: []position.Span{position.NewSpan(0, 1), position.NewSpan(1, 3)},
		},
		"document without a header after an end marker": {
			input: stringtest.LinesLF(
				"a: 1",
				"...",
				"b: [",
				"...",
				"c: 3",
			),
			want:  []string{"", "f.yaml:3:4: " + unclosedSequence, ""},
			spans: []position.Span{position.NewSpan(0, 2), position.NewSpan(2, 4), position.NewSpan(4, 5)},
		},
		"comment and directive above the header": {
			input: stringtest.LinesLF(
				"a: 1",
				"...",
				"# top",
				"%YAML 1.2",
				"---",
				"b: [",
				"---",
				"c: 3",
			),
			want:  []string{"", "f.yaml:6:4: " + unclosedSequence, ""},
			spans: []position.Span{position.NewSpan(0, 2), position.NewSpan(2, 6), position.NewSpan(6, 8)},
		},
		"directive that no document follows": {
			input: stringtest.LinesLF("a: 1", "...", "%YAML 1.2"),
			want:  []string{"", "f.yaml:3:1: unexpected directive value. document not started"},
			spans: []position.Span{position.NewSpan(0, 2), position.NewSpan(2, 3)},
		},
		// The header parses together with the document above it, so the
		// error of either document fails both.
		"header after an anchor with no value": {
			input: stringtest.LinesLF(
				"a: &x",
				"---",
				"b: [",
				"---",
				"c: 3",
			),
			want:  []string{"f.yaml:3:4: " + unclosedSequence, "f.yaml:3:4: " + unclosedSequence, ""},
			spans: []position.Span{position.NewSpan(0, 1), position.NewSpan(1, 3), position.NewSpan(3, 5)},
		},
		"empty documents around the failure": {
			input: stringtest.LinesLF(
				"---",
				"---",
				"a: [",
				"---",
				"---",
			),
			want: []string{"", "f.yaml:3:4: " + unclosedSequence, "", ""},
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

			docs := yamltest.AllDocuments(t, source, len(tc.want))

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

			// File returns one syntax error as it is, and several in one
			// binding of the Source, which Bindings walks in file order.
			file, err := source.File()
			require.Error(t, err)
			assert.Nil(t, file)

			if len(failed) == 1 {
				assert.Same(t, failed[0], err)
			} else {
				bound, ok := err.(*niceyaml.SourceError) //nolint:errorlint // The value itself is the bound error.
				require.True(t, ok, "want *niceyaml.SourceError, got %T", err)
				assert.Same(t, source, bound.Source())
				assert.Same(t, err, source.Bind(err))

				_, inDoc := bound.DocumentIndex()
				assert.False(t, inDoc)

				// The binding of the file stops errors.As, so the error of
				// each document comes from Bindings.
				var target *niceyaml.SourceError

				require.ErrorAs(t, err, &target)
				assert.Same(t, bound, target)

				parts := slices.Collect(niceyaml.Bindings(err))
				require.Len(t, parts, len(failed))

				for i, part := range parts {
					assert.Same(t, failed[i], part)
				}
			}

			// The other methods that need the whole file to parse return
			// the same error and nothing beside it.
			doc, docErr := source.Document()
			assert.Nil(t, doc)
			assert.Same(t, err, docErr)

			_, decodeErr := source.Decode[any](t.Context())
			assert.Same(t, err, decodeErr)

			assert.Equal(t, docs, source.AllDocuments())
		})
	}
}

func TestSource_AllDocuments_UnclosedFlow(t *testing.T) {
	t.Parallel()

	// A "{" or "[" that nothing closes fails its own document. The
	// documents after it decode to the values they hold in a file of
	// their own, where the lexer alone would read them as the inside of
	// that flow collection.
	tcs := map[string]struct {
		input string
		// The value of each document in file order, nil for a document
		// with a syntax error.
		want []any
	}{
		"nested mapping after an open mapping": {
			input: stringtest.LinesLF(
				"name: web",
				"labels: {app: web",
				"---",
				"name: worker",
				"spec:",
				"  tier: db",
			),
			want: []any{
				nil,
				map[string]any{"name": "worker", "spec": map[string]any{"tier": "db"}},
			},
		},
		"comma in a plain scalar after an open mapping": {
			input: stringtest.LinesLF(
				"name: web",
				"labels: {app: web",
				"---",
				"name: worker",
				"command: sleep 1, 2",
			),
			want: []any{
				nil,
				map[string]any{"name": "worker", "command": "sleep 1, 2"},
			},
		},
		"comma in a plain scalar after an open sequence": {
			input: stringtest.LinesLF("ports: [80, 443", "---", "command: sleep 1, 2"),
			want: []any{
				nil,
				map[string]any{"command": "sleep 1, 2"},
			},
		},
		"flow sequence after a sequence end that closes nothing": {
			input: stringtest.LinesLF("a: ]", "---", "x: [a, b]"),
			want: []any{
				nil,
				map[string]any{"x": []any{"a", "b"}},
			},
		},
		"document end marker": {
			input: stringtest.LinesLF(
				"a: [b",
				"...",
				"spec:",
				"  tier: db",
				"command: sleep 1, 2",
			),
			want: []any{
				nil,
				map[string]any{"spec": map[string]any{"tier": "db"}, "command": "sleep 1, 2"},
			},
		},
		"crlf": {
			input: stringtest.LinesCRLF(
				"a: {b",
				"---",
				"spec:",
				"  tier: db",
				"command: sleep 1, 2",
			),
			want: []any{
				nil,
				map[string]any{"spec": map[string]any{"tier": "db"}, "command": "sleep 1, 2"},
			},
		},
		"each of several documents leaves a flow open": {
			input: stringtest.LinesLF(
				"a: {",
				"---",
				"b: [",
				"---",
				"spec:",
				"  tier: db",
				"---",
				"z: ]",
				"---",
				"x: [a, b]",
			),
			want: []any{
				nil,
				nil,
				map[string]any{"spec": map[string]any{"tier": "db"}},
				nil,
				map[string]any{"x": []any{"a", "b"}},
			},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			docs := yamltest.AllDocuments(t, niceyaml.NewSourceFromString(tc.input), len(tc.want))

			for i, doc := range docs {
				if tc.want[i] == nil {
					require.ErrorIs(t, doc.Err(), niceyaml.ErrSyntax, "document %d", i)

					continue
				}

				require.NoError(t, doc.Err(), "document %d", i)

				got, err := doc.Decode[any](t.Context())
				require.NoError(t, err, "document %d", i)
				assert.Equal(t, tc.want[i], got, "document %d", i)
			}
		})
	}
}

func TestSource_AllDocuments_SyntaxErrorTokens(t *testing.T) {
	t.Parallel()

	// A document that did not parse keeps the tokens a document in its
	// place has when the file parses, and so do the documents around it.
	tcs := map[string]struct {
		// The lines of each document, which together are the lines of the
		// file.
		want [][]string
		// The values of the tokens in the preamble of each document.
		preambles [][]string
	}{
		"comment above the header": {
			want: [][]string{
				{"a: 1"},
				{"# lifted", "---", "b: ["},
				{"---", "c: 3"},
			},
			preambles: [][]string{
				nil,
				{" lifted", "---"},
				{"---"},
			},
		},
		"comments around an end marker": {
			want: [][]string{
				{"a: 1", "... # same"},
				{"# below", "---", "b: [", "...", "# after"},
			},
			preambles: [][]string{
				nil,
				{" below", "---"},
			},
		},
		"directive above the header": {
			want: [][]string{
				{"%YAML 1.2", "---", "a: ["},
				{"---", "b: 1"},
			},
			preambles: [][]string{
				{"%", "YAML", "1.2", "---"},
				{"---"},
			},
		},
		"comment-only document after the failure": {
			want: [][]string{
				{"# top", "---", "a: ["},
				{"---", "# mid"},
				{"---", "b: 1"},
			},
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

			source := niceyaml.NewSourceFromString(stringtest.LinesLF(slices.Concat(tc.want...)...))

			_, err := source.File()
			require.Error(t, err)

			var all token.Tokens

			for i, doc := range yamltest.AllDocuments(t, source, len(tc.want)) {
				assert.Equal(t, stringtest.JoinLF(tc.want[i]...), doc.View().Held().Content())
				assert.Equal(t, tc.preambles[i], yamltest.TokenValues(doc.Preamble()))

				all = append(all, doc.Tokens()...)
			}

			// The documents share out every token of the source, in order.
			yamltest.RequireTokensEqual(t, source.Tokens(), all)
		})
	}
}

// requireName is a [niceyaml.Validator] that rejects a document without a
// name key at the root of the document, and counts its calls in calls.
func requireName(calls *atomic.Int32) niceyaml.Validator {
	return niceyaml.ValidatorFunc(func(_ context.Context, n *niceyaml.Node) error {
		calls.Add(1)

		_, err := n.At(paths.Current().Child("name"))
		if err != nil {
			return yamltest.ErrorAt("name is required")
		}

		return nil
	})
}

func TestSource_Documents_LeavesOutEmpty(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input string
		// The index in the file of each document Documents returns.
		want []int
		// The number of documents AllDocuments returns.
		all int
	}{
		"no empty document": {
			input: stringtest.LinesLF("a: 1", "---", "b: 2"),
			want:  []int{0, 1},
			all:   2,
		},
		"trailing header": {
			input: stringtest.LinesLF("a: 1", "---"),
			want:  []int{0},
			all:   2,
		},
		"comment below a trailing header": {
			input: stringtest.LinesLF("a: 1", "---", "# Source: t.yaml"),
			want:  []int{0},
			all:   2,
		},
		"header that follows another": {
			input: stringtest.LinesLF("---", "---", "b: 2"),
			want:  []int{1},
			all:   2,
		},
		"directive above an empty document": {
			input: stringtest.LinesLF("%YAML 1.2", "---", "---", "b: 2"),
			want:  []int{1},
			all:   2,
		},
		"empty document between two": {
			input: stringtest.LinesLF("a: 1", "---", "---", "b: 2"),
			want:  []int{0, 2},
			all:   3,
		},
		"empty documents on both sides": {
			input: stringtest.LinesLF(
				"--- # head",
				"---",
				"a: 1",
				"---",
				"---",
			),
			want: []int{1},
			all:  4,
		},
		// A document that holds a value is not empty, whatever the value.
		"explicit null": {
			input: stringtest.LinesLF("a: 1", "--- ~"),
			want:  []int{0, 1},
			all:   2,
		},
		"empty mapping": {
			input: stringtest.LinesLF("a: 1", "---", "{}"),
			want:  []int{0, 1},
			all:   2,
		},
		"empty string": {
			input: stringtest.LinesLF("a: 1", "---", `""`),
			want:  []int{0, 1},
			all:   2,
		},
		// A file with no content holds its first document alone.
		"empty file": {
			input: "",
			want:  []int{0},
			all:   1,
		},
		"comments alone": {
			input: "# a\n",
			want:  []int{0},
			all:   1,
		},
		"header alone": {
			input: "---\n",
			want:  []int{0},
			all:   1,
		},
		"headers alone": {
			input: stringtest.LinesLF("---", "---"),
			want:  []int{0},
			all:   2,
		},
		"comments below headers alone": {
			input: stringtest.LinesLF("---", "# a", "---", "# b"),
			want:  []int{0},
			all:   2,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			source := niceyaml.NewSourceFromString(tc.input)

			all := yamltest.AllDocuments(t, source, tc.all)

			docs := source.Documents()

			got := make([]int, 0, len(docs))
			for _, doc := range docs {
				got = append(got, doc.DocumentIndex())

				// Documents hands out the Nodes AllDocuments does.
				assert.Same(t, all[doc.DocumentIndex()], doc)
			}

			assert.Equal(t, tc.want, got)
		})
	}
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

	tcs := map[string]struct {
		input string
		// The message of each binding the result holds, in file order.
		want []string
	}{
		"every document valid": {input: stringtest.LinesLF("name: a", "---", "name: b")},
		"one document": {
			input: "value: 1\n",
			want:  []string{"f.yaml:1:1: $: name is required"},
		},
		"first and last document invalid": {
			input: stringtest.LinesLF(
				"value: 1",
				"---",
				"name: b",
				"---",
				"value: 3",
			),
			want: []string{"f.yaml:1:1: $: name is required", "f.yaml:5:1: $: name is required"},
		},
		"syntax error beside violations": {
			input: stringtest.LinesLF(
				"value: 1",
				"---",
				"name: [",
				"---",
				"value: 3",
			),
			want: []string{
				"f.yaml:1:1: $: name is required",
				"f.yaml:3:7: " + unclosedSequence,
				"f.yaml:5:1: $: name is required",
			},
		},
		"two syntax errors": {
			input: stringtest.LinesLF(
				"a: [",
				"---",
				"name: b",
				"---",
				"c: [",
			),
			want: []string{"f.yaml:1:4: " + unclosedSequence, "f.yaml:5:4: " + unclosedSequence},
		},
		"syntax errors of adjacent documents": {
			input: stringtest.LinesLF("a: [", "---", "b: ["),
			want:  []string{"f.yaml:1:4: " + unclosedSequence, "f.yaml:3:4: " + unclosedSequence},
		},
		// The header parses together with the document above it, so both
		// documents carry the one error.
		"syntax error two documents share": {
			input: stringtest.LinesLF(
				"name: &x",
				"---",
				"name: [",
				"---",
				"value: 5",
			),
			want: []string{"f.yaml:3:7: " + unclosedSequence, "f.yaml:5:1: $: name is required"},
		},
		"empty document beside one with content": {input: stringtest.LinesLF("name: x", "---")},
		"empty documents alone": {
			input: stringtest.LinesLF("---", "---"),
			want:  []string{"f.yaml:1:1: $: name is required"},
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
			yamltest.AssertBoundTo(t, source, err)

			// Every document that parsed runs the validator, but an empty
			// one in a file that holds a document with content, and every
			// empty one past the first in a file of empty documents alone.
			docs := source.AllDocuments()
			content := slices.ContainsFunc(docs, func(doc *niceyaml.Node) bool { return !doc.IsEmpty() })

			var ran int32

			for i, doc := range docs {
				switch {
				case doc.Err() != nil:
				case content && doc.IsEmpty():
				case !content && i > 0:
				default:
					ran++
				}
			}

			assert.Equal(t, ran, calls.Load())
		})
	}

	t.Run("NoValidator() returns the error File returns", func(t *testing.T) {
		t.Parallel()

		tcs := map[string]string{
			"every document valid": stringtest.LinesLF("name: a", "---", "name: b"),
			"one syntax error":     "a: [\n",
			"two syntax errors": stringtest.LinesLF(
				"a: [",
				"---",
				"b: 1",
				"---",
				"c: [",
			),
			"syntax error two documents share": stringtest.LinesLF("a: &x", "---", "b: ["),
			"shared syntax error beside another": stringtest.LinesLF(
				"a: &x",
				"---",
				"b: [",
				"---",
				"c: 1",
				"---",
				"d: @x",
			),
			"stream of markers alone": "...\n",
		}

		// A validator that passes every Node of its own returns the same.
		validators := map[string]niceyaml.Validator{
			"NoValidator()": niceyaml.NoValidator(),
			"ValidatorFunc": niceyaml.ValidatorFunc(func(context.Context, *niceyaml.Node) error { return nil }),
		}

		for name, input := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				source := niceyaml.NewSourceFromString(input, niceyaml.WithName("f.yaml"))

				_, fileErr := source.File()

				// The call returns the very error File returns, not an
				// equal one built anew.
				for via, v := range validators {
					err := source.ValidateDocuments(t.Context(), v)
					if fileErr == nil {
						require.NoError(t, err, via)

						continue
					}

					assert.Same(t, fileErr, err, via)
				}
			})
		}
	})

	t.Run("panics for a nil validator", func(t *testing.T) {
		t.Parallel()

		// A file that does not parse panics too, so the call fails the
		// same way for every file.
		inputs := map[string]string{
			"a file that parses":         "name: a\n",
			"a file that does not parse": "a: [\n",
			"an empty file":              "",
		}

		for name, v := range nilValidators() {
			for of, input := range inputs {
				t.Run(name+" on "+of, func(t *testing.T) {
					t.Parallel()

					source := niceyaml.NewSourceFromString(input)

					assert.PanicsWithValue(t, "niceyaml.Source.ValidateDocuments: validator is nil", func() {
						//nolint:errcheck // The call panics before it returns.
						_ = source.ValidateDocuments(t.Context(), v)
					})
				})
			}
		}
	})

	t.Run("the validator decides how many violations a document reports", func(t *testing.T) {
		t.Parallel()

		// Rejects returns a validator that reports msg at the name of a
		// document whose name is one of bad.
		rejects := func(msg string, bad ...string) niceyaml.Validator {
			return niceyaml.ValidatorFunc(func(ctx context.Context, n *niceyaml.Node) error {
				name, err := n.DecodeAt[string](ctx, paths.Current().Child("name"))
				if err != nil {
					return err
				}

				if slices.Contains(bad, name) {
					return yamltest.ErrorAt(msg, "name")
				}

				return nil
			})
		}

		// The first document breaks both rules, and the last breaks the
		// second alone.
		input := stringtest.LinesLF(
			"name: root",
			"---",
			"name: [",
			"---",
			"name: ok",
			"---",
			"name: admin",
		)

		reserved := rejects("reserved name", "root")
		privileged := rejects("privileged name", "root", "admin")

		tcs := map[string]struct {
			v niceyaml.Validator
			// The message of each binding the result holds, in file order.
			want []string
		}{
			"NoValidator() reports the syntax alone": {
				v:    niceyaml.NoValidator(),
				want: []string{"f.yaml:3:7: " + unclosedSequence},
			},
			"one validator": {
				v:    reserved,
				want: []string{"f.yaml:1:7: $.name: reserved name", "f.yaml:3:7: " + unclosedSequence},
			},
			"ChainValidator stops at the first failure of each document": {
				v: niceyaml.ChainValidator(reserved, privileged),
				want: []string{
					"f.yaml:1:7: $.name: reserved name",
					"f.yaml:3:7: " + unclosedSequence,
					"f.yaml:7:7: $.name: privileged name",
				},
			},
			// The failures of one document come back in one binding.
			"MultiValidator reports every failure of each document": {
				v: niceyaml.MultiValidator(reserved, privileged),
				want: []string{
					stringtest.JoinLF(
						"f.yaml:1:7: $.name: reserved name",
						"f.yaml:1:7: $.name: privileged name",
					),
					"f.yaml:3:7: " + unclosedSequence,
					"f.yaml:7:7: $.name: privileged name",
				},
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				source := niceyaml.NewSourceFromString(input, niceyaml.WithName("f.yaml"))

				err := source.ValidateDocuments(t.Context(), tc.v)
				require.ErrorIs(t, err, niceyaml.ErrSyntax)
				assert.Equal(t, tc.want, bindingMessages(err))
			})
		}
	})
}

func TestSource_ValidateDocuments_SeveralDocuments(t *testing.T) {
	t.Parallel()

	short := schema.MustCompile([]byte(`{"additionalProperties": {"maxLength": 1}}`))

	// The documents of many lists one violation per document, more than
	// the message lists.
	var (
		many     strings.Builder
		manyRows []string
	)

	for i := range niceyaml.ErrorListLimit + 5 {
		many.WriteString("---\na: xx\n")

		if i < niceyaml.ErrorListLimit {
			manyRows = append(manyRows, fmt.Sprintf("f.yaml:%d:4: $.a: string length 2 is greater than 1", 2*i+2))
		}
	}

	tcs := map[string]struct {
		input string
		// The message of the result.
		want string
		// The number of documents that fail.
		docs int
	}{
		// The heading of each document does not carry a position, and
		// still comes before the syntax error of a later document.
		"violations of several documents beside a syntax error": {
			input: stringtest.LinesLF(
				"a: xx",
				"b: xx",
				"---",
				"a: xx",
				"b: xx",
				"---",
				"x: [",
			),
			want: stringtest.JoinLF(
				"f.yaml:1: document 1: 2 schema violations",
				"f.yaml:1:4: $.a: string length 2 is greater than 1",
				"f.yaml:2:4: $.b: string length 2 is greater than 1",
				"f.yaml:3: document 2: 2 schema violations",
				"f.yaml:4:4: $.a: string length 2 is greater than 1",
				"f.yaml:5:4: $.b: string length 2 is greater than 1",
				"f.yaml:7:4: "+unclosedSequence,
			),
			docs: 3,
		},
		"more violations than the message lists": {
			input: many.String(),
			want:  stringtest.JoinLF(append(manyRows, "f.yaml: and 5 more")...),
			docs:  niceyaml.ErrorListLimit + 5,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			source := niceyaml.NewSourceFromString(tc.input, niceyaml.WithName("f.yaml"))

			err := source.ValidateDocuments(t.Context(), short)
			require.EqualError(t, err, tc.want)

			// One binding of the Source heads the binding of each
			// document, whatever the number of documents.
			bound, ok := err.(*niceyaml.SourceError) //nolint:errorlint // The value itself is the bound error.
			require.True(t, ok, "want *niceyaml.SourceError, got %T", err)
			assert.Same(t, source, bound.Source())
			assert.Same(t, err, source.Bind(err))

			_, inDoc := bound.DocumentIndex()
			assert.False(t, inDoc)

			var indexes []int

			for part := range niceyaml.Bindings(err) {
				index, ok := part.DocumentIndex()
				require.True(t, ok)

				indexes = append(indexes, index)
			}

			want := make([]int, tc.docs)
			for i := range want {
				want[i] = i
			}

			assert.Equal(t, want, indexes)

			// Warnings.Report returns the binding as it is when it holds
			// no warning.
			assert.Same(t, err, niceyaml.NewWarnings().Report(err))
		})
	}
}

func TestSource_ValidateDocuments_SkipEmpty(t *testing.T) {
	t.Parallel()

	const (
		notNull = `$: expected "object", got "null"`
		noMatch = "no matching schema"
	)

	// The schema wants a mapping with a kind. The registry picks it for
	// the kinds it knows and requires a schema of every other document.
	manifest := schema.MustCompile([]byte(`{"type": "object", "required": ["kind"]}`))
	kindPath := paths.Doc().Child("kind")

	var known []matcher.Matcher

	for _, kind := range []string{"A", "Pod", "ConfigMap", "Service"} {
		known = append(known, matcher.Content(kindPath, kind))
	}

	registry := schema.NewRegistry(schema.WithResolvers(schema.When(matcher.Any(known...), manifest)))

	// A chart renders each template below a header and a comment that
	// names it, and a template that renders nothing else leaves an empty
	// document.
	helm := stringtest.LinesLF(
		"---",
		"# Source: demo/templates/cm.yaml",
		"apiVersion: v1",
		"kind: ConfigMap",
		"metadata:",
		"  name: demo",
		"",
		"---",
		"# Source: demo/templates/ingress.yaml",
		"# Ingress is optional.",
		"---",
		"# Source: demo/templates/svc.yaml",
		"apiVersion: v1",
		"kind: Service",
	)

	// A manifest above a template that renders the comment that names it
	// and nothing else.
	sourced := stringtest.LinesLF("kind: A", "---", "# Source: t.yaml")

	// Two manifests with an empty document between them.
	spaced := stringtest.LinesLF("kind: A", "---", "---", "kind: B")

	// The output of a chart whose one template renders a comment alone.
	commentAlone := stringtest.LinesLF(
		"---",
		"# Source: demo/templates/ingress.yaml",
		"# Ingress is optional.",
		"",
	)

	tcs := map[string]struct {
		v     niceyaml.Validator
		input string
		// The message of each binding the result holds, in file order,
		// with v alone and with v inside SkipEmpty.
		strict []string
		want   []string
	}{
		// An empty document beside one with content passes with v alone.
		"schema, trailing header": {
			v:     manifest,
			input: stringtest.LinesLF("kind: Pod", "---"),
		},
		"registry, trailing header": {
			v:     registry,
			input: stringtest.LinesLF("kind: Pod", "---"),
		},
		"schema, empty file": {
			v:      manifest,
			input:  "",
			strict: []string{"f.yaml: " + notNull},
		},
		"registry, empty file": {
			v:      registry,
			input:  "",
			strict: []string{"f.yaml: " + noMatch},
		},
		"schema, comment after a manifest": {
			v:     manifest,
			input: sourced,
		},
		"registry, comment after a manifest": {
			v:     registry,
			input: sourced,
		},
		"schema, chart output": {
			v:     manifest,
			input: helm,
		},
		"registry, chart output": {
			v:     registry,
			input: helm,
		},
		// A file that holds empty documents alone validates the first of
		// them with v alone.
		"schema, headers alone": {
			v:      manifest,
			input:  stringtest.LinesLF("---", "---"),
			strict: []string{"f.yaml:1:1: " + notNull},
		},
		"registry, headers alone": {
			v:      registry,
			input:  stringtest.LinesLF("---", "---"),
			strict: []string{"f.yaml:1: document 1: " + noMatch},
		},
		"schema, template that renders a comment alone": {
			v:      manifest,
			input:  commentAlone,
			strict: []string{"f.yaml:1:1: " + notNull},
		},
		"registry, template that renders a comment alone": {
			v:      registry,
			input:  commentAlone,
			strict: []string{"f.yaml: " + noMatch},
		},
		// A document that holds a value answers to the validator.
		"schema, explicit null": {
			v:      manifest,
			input:  stringtest.LinesLF("kind: Pod", "--- null"),
			strict: []string{"f.yaml:2:5: " + notNull},
			want:   []string{"f.yaml:2:5: " + notNull},
		},
		"registry, explicit null": {
			v:      registry,
			input:  stringtest.LinesLF("kind: Pod", "--- null"),
			strict: []string{"f.yaml:2: document 2: " + noMatch},
			want:   []string{"f.yaml:2: document 2: " + noMatch},
		},
		"schema, alias with no anchor": {
			v:      manifest,
			input:  stringtest.LinesLF("kind: Pod", "--- *nope"),
			strict: []string{`f.yaml:2:5: $: could not find alias "nope"`},
			want:   []string{`f.yaml:2:5: $: could not find alias "nope"`},
		},
		"registry, alias with no anchor": {
			v:      registry,
			input:  stringtest.LinesLF("kind: Pod", "--- *nope"),
			strict: []string{"f.yaml:2:5: $: resolve $.kind: alias does not resolve: *nope has no anchor before it"},
			want:   []string{"f.yaml:2:5: $: resolve $.kind: alias does not resolve: *nope has no anchor before it"},
		},
		// The message counts every document of the file, so the one below
		// an empty document keeps its number.
		"registry, document below an empty one": {
			v:      registry,
			input:  spaced,
			strict: []string{"f.yaml:3: document 3: " + noMatch},
			want:   []string{"f.yaml:3: document 3: " + noMatch},
		},
		"registry, syntax error beside an empty document": {
			v: registry,
			input: stringtest.LinesLF(
				"kind: A",
				"---",
				"kind: [",
				"---",
				"---",
				"kind: B",
			),
			strict: []string{"f.yaml:3:7: " + unclosedSequence, "f.yaml:5: document 4: " + noMatch},
			want:   []string{"f.yaml:3:7: " + unclosedSequence, "f.yaml:5: document 4: " + noMatch},
		},
		// A document that did not parse is not empty, so the empty one
		// beside it passes.
		"schema, empty document beside one that did not parse": {
			v:      manifest,
			input:  stringtest.LinesLF("kind: [", "---"),
			strict: []string{"f.yaml:1:7: " + unclosedSequence},
			want:   []string{"f.yaml:1:7: " + unclosedSequence},
		},
		// The header parses together with the document above it, so both
		// documents carry the one error, which the result holds once.
		"schema, syntax error two documents share": {
			v: manifest,
			input: stringtest.LinesLF(
				"name: &x",
				"---",
				"name: [",
				"---",
				"---",
				"value: 5",
			),
			strict: []string{"f.yaml:3:7: " + unclosedSequence, `f.yaml:6:1: $.kind: missing required property "kind"`},
			want:   []string{"f.yaml:3:7: " + unclosedSequence, `f.yaml:6:1: $.kind: missing required property "kind"`},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			source := niceyaml.NewSourceFromString(tc.input, niceyaml.WithName("f.yaml"))

			err := source.ValidateDocuments(t.Context(), tc.v)
			if len(tc.strict) == 0 {
				require.NoError(t, err)
			}

			assert.Equal(t, tc.strict, bindingMessages(err))

			err = source.ValidateDocuments(t.Context(), niceyaml.SkipEmpty(tc.v))
			if len(tc.want) == 0 {
				require.NoError(t, err)
			}

			assert.Equal(t, tc.want, bindingMessages(err))
		})
	}

	type manifestKind struct {
		Kind string `yaml:"kind"`
	}

	t.Run("agrees with a loop over the documents", func(t *testing.T) {
		t.Parallel()

		files := map[string]struct {
			input string
			// The kind each document Documents returns decodes to.
			want []string
		}{
			"trailing header":             {input: stringtest.LinesLF("kind: Pod", "---"), want: []string{"Pod"}},
			"comment after a manifest":    {input: sourced, want: []string{"A"}},
			"chart output":                {input: helm, want: []string{"ConfigMap", "Service"}},
			"document below an empty one": {input: spaced, want: []string{"A", "B"}},
			"headers alone":               {input: stringtest.LinesLF("---", "---"), want: []string{""}},
			"template that renders no content": {
				input: stringtest.LinesLF("---", "# Source: demo/templates/ingress.yaml"),
				want:  []string{""},
			},
			"empty file": {input: "", want: []string{""}},
		}

		for name, v := range map[string]niceyaml.Validator{"schema": manifest, "registry": registry} {
			for file, tc := range files {
				t.Run(name+", "+file, func(t *testing.T) {
					t.Parallel()

					source := niceyaml.NewSourceFromString(tc.input, niceyaml.WithName("f.yaml"))

					docs := source.Documents()

					// A loop that validates each document reports what
					// ValidateDocuments does, and a decode with the
					// validator fails the documents the loop fails.
					var (
						errs  []error
						kinds []string
					)

					for _, doc := range docs {
						err := doc.Validate(t.Context(), v)
						if err != nil {
							errs = append(errs, err)
						}

						_, decodeErr := doc.Decode[manifestKind](t.Context(), niceyaml.WithValidator(v))
						assert.Equal(t, err == nil, decodeErr == nil)

						got, err := doc.Decode[manifestKind](t.Context())
						require.NoError(t, err)

						kinds = append(kinds, got.Kind)
					}

					assert.Equal(t, tc.want, kinds)
					assert.Equal(t,
						bindingMessages(source.ValidateDocuments(t.Context(), v)),
						bindingMessages(errors.Join(errs...)),
					)
				})
			}
		}
	})

	t.Run("an empty document from AllDocuments runs the validator", func(t *testing.T) {
		t.Parallel()

		for name, v := range map[string]niceyaml.Validator{"schema": manifest, "registry": registry} {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				source := niceyaml.NewSourceFromString(sourced, niceyaml.WithName("f.yaml"))

				docs := yamltest.AllDocuments(t, source, 2)
				require.True(t, docs[1].IsEmpty())

				// ValidateDocuments leaves the empty document out, and
				// the document itself fails v alone, in a decode too.
				require.NoError(t, source.ValidateDocuments(t.Context(), v))

				strict := docs[1].Validate(t.Context(), v)
				require.Error(t, strict)

				_, err := docs[1].Decode[manifestKind](t.Context(), niceyaml.WithValidator(v))
				require.EqualError(t, err, strict.Error())

				// Inside SkipEmpty it passes and decodes to the zero value.
				skip := niceyaml.SkipEmpty(v)
				require.NoError(t, docs[1].Validate(t.Context(), skip))

				got, err := docs[1].Decode[manifestKind](t.Context(), niceyaml.WithValidator(skip))
				require.NoError(t, err)
				assert.Empty(t, got.Kind)
			})
		}
	})
}

func TestSource_ValidateDocuments_Context(t *testing.T) {
	t.Parallel()

	input := stringtest.LinesLF(
		"value: 1",
		"---",
		"value: 2",
		"---",
		"value: 3",
		"---",
		"value: 4",
	)

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

		assert.Same(t, source, yamltest.RequireSourceError(t, err).Source())

		// With the Validator from NoValidator, the ended ctx still fails the call.
		err = source.ValidateDocuments(ctx, niceyaml.NoValidator())
		require.ErrorIs(t, err, context.Canceled)
		assert.Equal(t, []string{"f.yaml: context canceled"}, bindingMessages(err))
	})

	t.Run("ended by the second call of a validator", func(t *testing.T) {
		t.Parallel()

		tcs := map[string]struct {
			// The error the validator returns from the call that ends ctx.
			result func(ctx context.Context) error
			// The message of each binding the result holds, in file order.
			want []string
		}{
			"validator that reports it": {
				result: func(ctx context.Context) error { return ctx.Err() },
				want:   []string{"f.yaml:1:1: $: bad", "f.yaml:2: document 2: context canceled"},
			},
			"validator that passes": {
				result: func(context.Context) error { return nil },
				want:   []string{"f.yaml:1:1: $: bad", "f.yaml: context canceled"},
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				ctx, cancel := context.WithCancel(t.Context())
				t.Cleanup(cancel)

				var calls atomic.Int32

				v := niceyaml.ValidatorFunc(func(ctx context.Context, _ *niceyaml.Node) error {
					if calls.Add(1) == 2 {
						cancel()

						return tc.result(ctx)
					}

					return yamltest.ErrorAt("bad")
				})

				source := niceyaml.NewSourceFromString(input, niceyaml.WithName("f.yaml"))

				err := source.ValidateDocuments(ctx, v)
				require.ErrorIs(t, err, context.Canceled)
				assert.Equal(t, int32(2), calls.Load())
				assert.Equal(t, tc.want, bindingMessages(err))
				assert.Equal(t, 1, strings.Count(err.Error(), context.Canceled.Error()), err.Error())
			})
		}
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

func TestSource_MarshalYAML(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		err   error
		input string
		want  string
		opts  []niceyaml.SourceOption
	}{
		"one document": {
			input: "b: 1\na: [x]\n",
			want:  stringtest.LinesLF("b: 1", "a:", "- x"),
		},
		"key that the document repeats": {
			input: "a: 1\nb: 2\na: 3\n",
			opts:  []niceyaml.SourceOption{niceyaml.WithAllowDuplicateKeys(true)},
			want:  stringtest.LinesLF("a: 3", "b: 2"),
		},
		"empty source": {
			input: "",
			want:  "null\n",
		},
		"more than one document": {
			input: "a: 1\n---\nb: 2\n",
			err:   niceyaml.ErrMultipleDocuments,
		},
		"document that does not parse": {
			input: "a: [\n",
			err:   niceyaml.ErrSyntax,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			source := niceyaml.NewSourceFromString(tc.input, tc.opts...)

			got, err := encoder.Marshal(t.Context(), source)
			if tc.err != nil {
				require.ErrorIs(t, err, tc.err)
				require.ErrorIs(t, err, encoder.ErrEncode)

				// The error is the one that Document returns.
				_, want := source.Document()
				_, err = source.MarshalYAML(t.Context())
				assert.Equal(t, want, err)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, tc.want, string(got))
		})
	}

	t.Run("nil source encodes as null", func(t *testing.T) {
		t.Parallel()

		var source *niceyaml.Source

		v, err := source.MarshalYAML(t.Context())
		require.NoError(t, err)
		assert.Nil(t, v)

		got, err := encoder.Marshal(t.Context(), map[string]any{"a": source})
		require.NoError(t, err)
		assert.Equal(t, "a: null\n", string(got))
	})
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
		"empty name": {},
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

			assert.Equal(t, tc.want, source.Name())
		})
	}
}

func TestSource_TokenAt(t *testing.T) {
	t.Parallel()

	source := niceyaml.NewSourceFromString("key: value\n")

	tcs := map[string]struct {
		pos position.Position
		// The value of the token at pos, empty where no token lies.
		want string
	}{
		"key token at start":      {pos: position.New(0, 0), want: "key"},
		"key token at end of key": {pos: position.New(0, 2), want: "key"},
		"colon token":             {pos: position.New(0, 3), want: ":"},
		"value token":             {pos: position.New(0, 5), want: "value"},
		"out of bounds line":      {pos: position.New(10, 0)},
		"negative line":           {pos: position.New(-1, 0)},
		"out of bounds column":    {pos: position.New(0, 100)},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := source.Lines().TokenAt(tc.pos)
			if tc.want == "" {
				assert.Nil(t, got)

				return
			}

			require.NotNil(t, got)
			assert.Equal(t, tc.want, got.Value)
		})
	}
}

func TestSource_View_IsIndependent(t *testing.T) {
	t.Parallel()

	source := niceyaml.NewSourceFromString("key: value\n")

	// A change to one view reaches neither the Source nor another view.
	view := source.View()
	view.AddOverlay("test1", yamltest.LineRange(0, 0, 5))
	view.AnnotateLine(0, line.Annotation{Content: "note", Placement: line.Below})

	assert.Empty(t, source.View().Overlays(0))
	assert.Empty(t, source.View().Annotations(0))

	// The view keeps its overlay and annotation.
	require.Len(t, view.Overlays(0), 1)
	assert.Equal(t, kind.Kind("test1"), view.Overlays(0)[0].Kind)
	assert.NotEmpty(t, view.Annotations(0))
	assert.Equal(t, "key: value", view.Held().Content())

	// An overlay on one line of the view stays in the view too.
	for i := range view.All() {
		view.AddLineOverlay(i, line.Overlay{Cols: position.NewSpan(0, 3), Kind: kind.GenericError})
	}

	assert.Empty(t, source.View().Overlays(0))

	// A fresh view renders the pristine document.
	plain := yamltest.NewXMLPrinter(printer.WithStyles(style.Styles{}))
	assert.Equal(t, "key: value", plain.Print(source.View()))
}

func TestSource_Bind(t *testing.T) {
	t.Parallel()

	// Two documents, so Source.Document would refuse to pick one.
	source := niceyaml.NewSourceFromString(stringtest.LinesLF(
		"a: 1",
		"---",
		"b: 22",
	), niceyaml.WithName("two.yaml"))

	t.Run("range error binds to the document its line falls in", func(t *testing.T) {
		t.Parallel()

		docs := source.Documents()

		rng := yamltest.LineRange(2, 3, 5)
		err := source.Bind(niceyaml.NewError("too wide", niceyaml.AtRange(rng)))

		bound := yamltest.RequireSourceError(t, err)
		assert.Same(t, source, bound.Source())
		assert.Same(t, docs[1], yamltest.BoundDocument(t, bound))
		assert.Equal(t, "two.yaml:3:4: too wide", err.Error())
		assert.Equal(t, rng, yamltest.RangeOf(t, bound))

		assert.Equal(t, stringtest.JoinLF(
			"two.yaml:3:4: too wide",
			"",
			"   1 | a: 1",
			"   2 | ---",
			"   3 | b: 22",
			"     |    ^^",
		), fmt.Sprintf("%+v", err))
	})

	t.Run("position error binds to the document that holds its line", func(t *testing.T) {
		t.Parallel()

		tcs := map[string]struct {
			// The reason the position resolves to no range, when it does
			// not.
			unresolved error
			want       string
			rng        position.Range
			pos        position.Position
			// The index of the document the error binds to, or -1 for
			// none.
			doc int
		}{
			"position in the first document": {
				pos:  position.New(0, 3),
				doc:  0,
				want: "two.yaml:1:4: bad",
				rng:  yamltest.LineRange(0, 3, 4),
			},
			"position in the second document resolves to its token": {
				pos:  position.New(2, 3),
				doc:  1,
				want: "two.yaml:3:4: bad",
				rng:  yamltest.LineRange(2, 3, 5),
			},
			"position outside the source binds to no document": {
				pos:        position.New(9, 0),
				doc:        -1,
				want:       "two.yaml: bad",
				unresolved: niceyaml.ErrOutOfRange,
			},
			"column before the first binds to the document holding the line": {
				pos:        position.New(2, -3),
				doc:        1,
				want:       "two.yaml:2: document 2: bad",
				unresolved: niceyaml.ErrOutOfRange,
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				err := source.Bind(niceyaml.NewError("bad", niceyaml.AtPosition(tc.pos)))
				require.EqualError(t, err, tc.want)

				bound := yamltest.RequireSourceError(t, err)

				if tc.doc < 0 {
					_, ok := bound.Document()
					assert.False(t, ok)
				} else {
					assert.Same(t, source.Documents()[tc.doc], yamltest.BoundDocument(t, bound))
				}

				got, ok := bound.Range()
				assert.Equal(t, tc.unresolved == nil, ok)
				assert.Equal(t, tc.rng, got)
				require.ErrorIs(t, bound.Unresolved(), tc.unresolved)
			})
		}
	})

	t.Run("position in a source with a syntax error binds to the document holding the line", func(t *testing.T) {
		t.Parallel()

		// The second document of three does not parse.
		broken := niceyaml.NewSourceFromString(stringtest.LinesLF(
			"a: 1",
			"---",
			"b: [1, 2",
			"---",
			"c: 3",
		), niceyaml.WithName("broken.yaml"))

		docs := yamltest.AllDocuments(t, broken, 3)
		require.NoError(t, docs[0].Err())
		require.ErrorIs(t, docs[1].Err(), niceyaml.ErrSyntax)
		require.NoError(t, docs[2].Err())

		tcs := map[string]struct {
			opt niceyaml.ErrorOption
			// The index of the document the error binds to.
			want int
			msg  string
		}{
			"position in the document above the syntax error": {
				opt:  niceyaml.AtPosition(position.New(0, 0)),
				want: 0,
				msg:  "broken.yaml:1:1: bad",
			},
			"position in the document that did not parse": {
				opt:  niceyaml.AtPosition(position.New(2, 0)),
				want: 1,
				msg:  "broken.yaml:3:1: bad",
			},
			"position in the document below the syntax error": {
				opt:  niceyaml.AtPosition(position.New(4, 3)),
				want: 2,
				msg:  "broken.yaml:5:4: bad",
			},
			"range in the document below the syntax error": {
				opt:  niceyaml.AtRange(yamltest.LineRange(4, 0, 1)),
				want: 2,
				msg:  "broken.yaml:5:1: bad",
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				err := broken.Bind(niceyaml.NewError("bad", tc.opt))

				bound := yamltest.RequireSourceError(t, err)
				assert.Same(t, docs[tc.want], yamltest.BoundDocument(t, bound))
				assert.Equal(t, tc.msg, err.Error())
				require.NoError(t, bound.Unresolved())
			})
		}
	})

	t.Run("position in a source of one document that does not parse binds to it", func(t *testing.T) {
		t.Parallel()

		broken := niceyaml.NewSourceFromString("a: [\n", niceyaml.WithName("broken.yaml"))
		err := broken.Bind(niceyaml.NewError("bad", niceyaml.AtPosition(position.New(0, 0))))

		bound := yamltest.RequireSourceError(t, err)
		assert.Same(t, broken.AllDocuments()[0], yamltest.BoundDocument(t, bound))
		assert.Equal(t, "broken.yaml:1:1: bad", err.Error())
	})

	t.Run("path error resolves in the one document", func(t *testing.T) {
		t.Parallel()

		// A path resolves in the document Source.Document returns, so a
		// caller holding a configuration file binds through the source.
		one := niceyaml.NewSourceFromString(stringtest.LinesLF(
			"# header",
			"a: 1",
			"b: 22",
		), niceyaml.WithName("one.yaml"))
		doc := yamltest.Document(t, one)

		err := one.Bind(yamltest.ErrorAt("bad", "b"))

		bound := yamltest.RequireSourceError(t, err)
		assert.Equal(t, "one.yaml:3:4: $.b: bad", err.Error())
		assert.Same(t, doc, yamltest.BoundDocument(t, bound))
		assert.Equal(t, yamltest.LineRange(2, 3, 5), yamltest.RangeOf(t, bound))

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

		one := niceyaml.NewSourceFromString(stringtest.LinesLF("a: 1", "b: 2"), niceyaml.WithName("one.yaml"))
		doc := yamltest.Document(t, one)

		err := one.Bind(niceyaml.NewError("bad", niceyaml.AtPath(paths.Current().Child("zz").Index(0))))

		bound := yamltest.RequireSourceError(t, err)
		assert.Equal(t, "one.yaml:1:1: $.zz[0]: bad", err.Error())
		assert.Same(t, doc, yamltest.BoundDocument(t, bound))
		require.NoError(t, bound.Unresolved())

		near, ok := bound.Nearest()
		require.True(t, ok)
		assert.Equal(t, "$", near.String())
	})

	t.Run("wildcard path binds to the one document", func(t *testing.T) {
		t.Parallel()

		one := niceyaml.NewSourceFromString(stringtest.LinesLF("a: 1", "b: 2"), niceyaml.WithName("one.yaml"))
		doc := yamltest.Document(t, one)

		err := one.Bind(niceyaml.NewError("bad", niceyaml.AtPath(paths.Current().Child("zz").IndexAll())))

		bound := yamltest.RequireSourceError(t, err)
		assert.Equal(t, "one.yaml: $.zz[*]: bad", err.Error())
		assert.Same(t, doc, yamltest.BoundDocument(t, bound))
		require.ErrorIs(t, bound.Unresolved(), paths.ErrWildcard)

		_, ok := bound.Range()
		assert.False(t, ok)
	})

	t.Run("path error after a blank line before an explicit key", func(t *testing.T) {
		t.Parallel()

		// The lexer drops the blank line between the flow sequence and
		// the "?", and the source keeps it, so the key after the explicit
		// entry keeps its line number and the excerpt shows its line.
		keyed := niceyaml.NewSourceFromString(
			stringtest.LinesLF(
				"tags: [a, b]",
				"",
				"? key",
				": value",
				"port: http",
			),
			niceyaml.WithName("f.yaml"),
		)

		err := keyed.Bind(yamltest.ErrorAt("bad port", "port"))

		bound := yamltest.RequireSourceError(t, err)
		assert.Equal(t, "f.yaml:5:7: $.port: bad port", err.Error())
		assert.Equal(t, yamltest.LineRange(4, 6, 10), yamltest.RangeOf(t, bound))

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
		err := source.Bind(yamltest.ErrorAt("bad", "b"))

		bound := yamltest.RequireSourceError(t, err)
		assert.Equal(t, "two.yaml: $.b: bad", err.Error())

		_, ok := bound.Document()
		assert.False(t, ok)

		rangeErr := bound.Unresolved()
		require.ErrorIs(t, rangeErr, niceyaml.ErrPathNeedsDocument)
		require.ErrorIs(t, rangeErr, niceyaml.ErrMultipleDocuments)

		assert.Equal(t, stringtest.JoinLF(
			"two.yaml: $.b: bad",
			"",
			"no excerpt: path needs a document to resolve in: $.b: multiple documents in source: 2 documents",
		), fmt.Sprintf("%+v", err))
	})

	t.Run("path error binds to the document Document returns", func(t *testing.T) {
		t.Parallel()

		// The empty documents do not count, so the path resolves in the
		// document with content, wherever it sits in the file. A stream of
		// "..." markers alone holds one empty document, as an empty file
		// does, so a path resolves in that document and finds nothing
		// there.
		tcs := map[string]struct {
			input string
			want  string
			// The reason the path resolves to no range, when it does not.
			unresolved error
		}{
			"empty document below": {
				input: stringtest.LinesLF("b: 2", "---"),
				want:  "one.yaml:1:4: $.b: bad",
			},
			"empty documents above": {
				input: stringtest.LinesLF("---", "---", "b: 2"),
				want:  "one.yaml:3:4: $.b: bad",
			},
			"stream of markers alone": {
				input:      "...\n",
				want:       "one.yaml: $.b: bad",
				unresolved: paths.ErrNoDocument,
			},
			"empty file": {
				input:      "",
				want:       "one.yaml: $.b: bad",
				unresolved: paths.ErrNoDocument,
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				src := niceyaml.NewSourceFromString(tc.input, niceyaml.WithName("one.yaml"))
				err := src.Bind(yamltest.ErrorAt("bad", "b"))
				require.EqualError(t, err, tc.want)

				bound := yamltest.RequireSourceError(t, err)
				assert.Same(t, yamltest.Document(t, src), yamltest.BoundDocument(t, bound))
				require.ErrorIs(t, bound.Unresolved(), tc.unresolved)
				require.NotErrorIs(t, bound.Unresolved(), niceyaml.ErrPathNeedsDocument)
			})
		}
	})

	t.Run("path error in a source that does not parse names the parse error", func(t *testing.T) {
		t.Parallel()

		broken := niceyaml.NewSourceFromString("a: [\n", niceyaml.WithName("broken.yaml"))
		_, parseErr := broken.File()
		require.Error(t, parseErr)

		err := broken.Bind(yamltest.ErrorAt("bad", "a"))

		bound := yamltest.RequireSourceError(t, err)

		_, ok := bound.Document()
		assert.False(t, ok)

		rangeErr := bound.Unresolved()
		require.ErrorIs(t, rangeErr, niceyaml.ErrPathNeedsDocument)
		assert.Contains(t, rangeErr.Error(), parseErr.Error())
	})

	t.Run("each location in a tree finds its own document", func(t *testing.T) {
		t.Parallel()

		docs := source.Documents()

		err := source.Bind(errors.Join(
			niceyaml.NewError("bad a", niceyaml.AtPosition(position.New(0, 3))),
			niceyaml.NewError("bad b", niceyaml.AtPosition(position.New(2, 3))),
		))

		bound := yamltest.RequireSourceError(t, err)

		_, ok := bound.Document()
		assert.False(t, ok)

		children := yamltest.ProblemsOf(bound)
		require.Len(t, children, 2)
		assert.Same(t, docs[0], yamltest.BoundDocument(t, children[0].Bound))
		assert.Same(t, docs[1], yamltest.BoundDocument(t, children[1].Bound))
	})

	t.Run("nested path errors resolve nowhere in several documents", func(t *testing.T) {
		t.Parallel()

		err := source.Bind(niceyaml.NewSummary("2 problems",
			yamltest.ErrorAt("bad a", "a"),
			yamltest.ErrorAt("bad b", "b"),
		))

		bound := yamltest.RequireSourceError(t, err)

		for _, child := range yamltest.ProblemsOf(bound) {
			rangeErr := child.Bound.Unresolved()
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

		assert.Same(t, source, yamltest.RequireSourceError(t, err).Source())
		assert.Equal(t, "two.yaml: plain", err.Error())
	})

	t.Run("nil and bound errors come back as they are", func(t *testing.T) {
		t.Parallel()

		require.NoError(t, source.Bind(nil))

		docs := source.Documents()

		bound := docs[1].Bind(yamltest.ErrorAt("bad", "b"))
		assert.Same(t, bound, source.Bind(bound))
	})
}

func TestSource_Bind_RoutesDocuments(t *testing.T) {
	t.Parallel()

	// Four documents, the second holding a comment alone.
	commented := stringtest.LinesLF(
		"a: 1",
		"---",
		"# only",
		"---",
		"b: 2",
		"---",
		"c: 3",
	)
	// Three documents, each a header alone.
	headers := stringtest.LinesLF("---", "---", "---")
	// Two documents, the first of which ends at a marker with a comment.
	marked := stringtest.LinesLF("a: 1", "... # e", "k: v")

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
		"commented end marker of a document": {input: marked, line: 1, want: 0},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			source := niceyaml.NewSourceFromString(tc.input)

			// A line of an empty document binds to that document, which
			// Documents leaves out beside one with content.
			docs := source.AllDocuments()

			rng := yamltest.LineRange(tc.line, 0, 1)

			bound := yamltest.RequireSourceError(t, source.Bind(niceyaml.NewError("bad", niceyaml.AtRange(rng))))

			if tc.want < 0 {
				_, ok := bound.Document()
				assert.False(t, ok)

				return
			}

			assert.Same(t, docs[tc.want], yamltest.BoundDocument(t, bound))
		})
	}
}

func TestNewSourceFromReader(t *testing.T) {
	t.Parallel()

	t.Run("reads to the end", func(t *testing.T) {
		t.Parallel()

		source, err := niceyaml.NewSourceFromReader(strings.NewReader("key: value\n"), niceyaml.WithName("<stdin>"))
		require.NoError(t, err)
		assert.Equal(t, "<stdin>", source.Name())
		assert.Empty(t, source.FilePath())

		doc := yamltest.Document(t, source)

		got, err := doc.Decode[map[string]string](t.Context())
		require.NoError(t, err)
		assert.Equal(t, map[string]string{"key": "value"}, got)
	})

	t.Run("reports a read error", func(t *testing.T) {
		t.Parallel()

		denied := &fs.PathError{Op: "open", Path: "/cache/app.yaml", Err: fs.ErrPermission}

		tcs := map[string]struct {
			err      error
			is       error
			want     string
			wantName string
			opts     []niceyaml.SourceOption
		}{
			"named": {
				err:      errReadFailed,
				is:       errReadFailed,
				opts:     []niceyaml.SourceOption{niceyaml.WithName("<stdin>")},
				want:     "<stdin>: read failed",
				wantName: "<stdin>",
			},
			"unnamed": {
				err:  errReadFailed,
				is:   errReadFailed,
				want: "read failed",
			},
			"path error": {
				// The name of the Source replaces the path in the message.
				err:      denied,
				is:       fs.ErrPermission,
				opts:     []niceyaml.SourceOption{niceyaml.WithName("remote")},
				want:     "remote: " + fs.ErrPermission.Error(),
				wantName: "remote",
			},
			"path error under the context of the reader": {
				// The message keeps the context that the reader put around
				// the path error, and the path with it.
				err:      fmt.Errorf("fetch https://example.com/app.yaml: %w", denied),
				is:       fs.ErrPermission,
				opts:     []niceyaml.SourceOption{niceyaml.WithName("remote")},
				want:     "remote: fetch https://example.com/app.yaml: open /cache/app.yaml: " + fs.ErrPermission.Error(),
				wantName: "remote",
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				_, err := niceyaml.NewSourceFromReader(iotest.ErrReader(tc.err), tc.opts...)
				require.ErrorIs(t, err, tc.is)
				assert.Equal(t, tc.want, err.Error())
				assert.False(t, niceyaml.IsInvalid(err))

				bound := slices.Collect(niceyaml.Bindings(err))
				require.Len(t, bound, 1)
				assert.Equal(t, tc.wantName, bound[0].Name())
			})
		}
	})
}

// NewSourceFromFile names the Source by the path as given and gives it
// the absolute path of the file, built from the working directory at
// the time of the read. The test changes the process's working
// directory, so it does not run in parallel.
//
//nolint:paralleltest // See above.
func TestNewSourceFromFile(t *testing.T) {
	dir := t.TempDir()
	file := yamltest.WriteFile(t, dir, "configs/app.yaml", "key: value\n")

	t.Chdir(dir)

	tcs := map[string]struct {
		path         string
		wantName     string
		wantFilePath string
		opts         []niceyaml.SourceOption
	}{
		"relative path": {
			path:         "configs/app.yaml",
			wantName:     "configs/app.yaml",
			wantFilePath: file,
		},
		"relative path with dot elements": {
			path:         "./configs/../configs/app.yaml",
			wantName:     "./configs/../configs/app.yaml",
			wantFilePath: file,
		},
		"absolute path": {
			path:         file,
			wantName:     file,
			wantFilePath: file,
		},
		"name option": {
			path:         "configs/app.yaml",
			opts:         []niceyaml.SourceOption{niceyaml.WithName("app")},
			wantName:     "app",
			wantFilePath: file,
		},
		"file path option": {
			path:         "configs/app.yaml",
			opts:         []niceyaml.SourceOption{niceyaml.WithFilePath("virtual/app.yaml")},
			wantName:     "configs/app.yaml",
			wantFilePath: "virtual/app.yaml",
		},
	}

	for name, tc := range tcs {
		//nolint:paralleltest // See above.
		t.Run(name, func(t *testing.T) {
			source, err := niceyaml.NewSourceFromFile(tc.path, tc.opts...)
			require.NoError(t, err)

			// The file path stays where the read found the file.
			t.Chdir(t.TempDir())

			assert.Equal(t, tc.wantName, source.Name())
			assert.Equal(t, tc.wantFilePath, source.FilePath())

			doc := yamltest.Document(t, source)
			assert.Equal(t, tc.wantFilePath, doc.FilePath())

			got, err := doc.Decode[map[string]string](t.Context())
			require.NoError(t, err)
			assert.Equal(t, map[string]string{"key": "value"}, got)
		})
	}

	// The message opens with the name of the Source, drawn as escape.Line
	// draws it, and goes on with the reason that the system gives.
	readErrs := map[string]struct {
		is           error
		path         string
		wantPrefix   string
		wantName     string
		wantFilePath string
		opts         []niceyaml.SourceOption
		unix         bool
	}{
		"missing file": {
			path:         "configs/missing.yaml",
			is:           fs.ErrNotExist,
			wantPrefix:   "configs/missing.yaml: ",
			wantName:     "configs/missing.yaml",
			wantFilePath: filepath.Join(dir, "configs", "missing.yaml"),
		},
		"missing file with a line feed and an escape in its name": {
			path:         "no\nsuch\x1b[31m.yaml",
			is:           fs.ErrNotExist,
			wantPrefix:   "no\u240asuch\u241b[31m.yaml: ",
			wantName:     "no\nsuch\x1b[31m.yaml",
			wantFilePath: dir + string(filepath.Separator) + "no\nsuch\x1b[31m.yaml",
		},
		"missing file with options": {
			path: "configs/missing.yaml",
			opts: []niceyaml.SourceOption{
				niceyaml.WithName("defaults"),
				niceyaml.WithFilePath("virtual/app.yaml"),
			},
			is:           fs.ErrNotExist,
			wantPrefix:   "defaults: ",
			wantName:     "defaults",
			wantFilePath: "virtual/app.yaml",
		},
		"directory": {
			// The open of a directory succeeds, and the read that follows
			// it fails.
			path:         "configs",
			wantPrefix:   "configs: ",
			wantName:     "configs",
			wantFilePath: filepath.Join(dir, "configs"),
			unix:         true,
		},
	}

	for name, tc := range readErrs {
		//nolint:paralleltest // See above.
		t.Run(name, func(t *testing.T) {
			if tc.unix && runtime.GOOS == "windows" {
				t.Skip("Windows does not open a directory as a file")
			}

			_, err := niceyaml.NewSourceFromFile(tc.path, tc.opts...)
			require.Error(t, err)

			if tc.is != nil {
				require.ErrorIs(t, err, tc.is)
			}

			assert.False(t, niceyaml.IsInvalid(err))

			pathErr, ok := errors.AsType[*fs.PathError](err)
			require.True(t, ok, "the chain keeps the error of the system")
			assert.Equal(t, tc.path, pathErr.Path)
			assert.Equal(t, tc.wantPrefix+pathErr.Err.Error(), err.Error())

			bound := slices.Collect(niceyaml.Bindings(err))
			require.Len(t, bound, 1)
			assert.Equal(t, tc.wantName, bound[0].Name())
			assert.Equal(t, tc.wantFilePath, bound[0].FilePath())
		})
	}
}

func TestNewSourceFromFile_DotDotAfterSymlink(t *testing.T) {
	t.Parallel()

	if runtime.GOOS == "windows" {
		t.Skip("Windows drops a .. element as text before it follows a symlink")
	}

	// With link leading to real/sub, the OS reads link/../doc.yaml as
	// real/doc.yaml, whose directive names real/schema.json. The
	// schema.json beside link, which cleaning the path as text names,
	// accepts any document.
	dir := t.TempDir()
	yamltest.WriteFiles(t, dir, map[string]string{
		"real/doc.yaml":       yamltest.SchemaDirective("./schema.json", "name: 1\n"),
		"real/schema.json":    `{"properties": {"name": {"type": "string"}}}`,
		"real/sub/inner.yaml": yamltest.SchemaDirective("./inner.json", "name: a\n"),
		"real/sub/inner.json": `{"properties": {"name": {"type": "string"}}}`,
		"schema.json":         `{}`,
	})
	yamltest.SymlinkOrSkip(t, filepath.Join("real", "sub"), filepath.Join(dir, "link"))

	physical, err := filepath.EvalSymlinks(dir)
	require.NoError(t, err)

	tcs := map[string]struct {
		path         string
		wantFilePath string
		invalid      bool
	}{
		// The test builds each path as text, because filepath.Join
		// cleans it.
		"dot-dot after a symlink": {
			path:         dir + "/link/../doc.yaml",
			wantFilePath: filepath.Join(physical, "real", "doc.yaml"),
			invalid:      true,
		},
		"symlinked name without a dot-dot": {
			path:         dir + "/link/inner.yaml",
			wantFilePath: filepath.Join(dir, "link", "inner.yaml"),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			source, err := niceyaml.NewSourceFromFile(tc.path)
			require.NoError(t, err)

			assert.Equal(t, tc.path, source.Name())
			assert.Equal(t, tc.wantFilePath, source.FilePath())

			reg := schema.NewRegistry(
				schema.WithAccess(schema.Unrestricted()),
				schema.WithResolvers(schema.Directive()),
			)

			err = source.ValidateDocuments(t.Context(), reg)
			if !tc.invalid {
				require.NoError(t, err)

				return
			}

			require.Error(t, err)
			assert.True(t, niceyaml.IsInvalid(err), err.Error())
			assert.Contains(t, err.Error(), tc.path+`:2:7: $.name: expected "string", got "integer"`)
		})
	}
}

func TestSource_FS(t *testing.T) {
	t.Parallel()

	const text = "key: value\n"

	// A pointer compares by identity, so the test tells this file system
	// from any other.
	fsys := yamltest.MapFS(map[string]string{"configs/app.yaml": text})
	bundle := &fsys

	fromFS, err := niceyaml.NewSourceFromFS(bundle, "configs/app.yaml")
	require.NoError(t, err)

	replaced, err := niceyaml.NewSourceFromFS(bundle, "configs/app.yaml", niceyaml.WithFS(nil))
	require.NoError(t, err)

	tcs := map[string]struct {
		source *niceyaml.Source
		want   fs.FS
	}{
		"NewSourceFromFS keeps its file system": {
			source: fromFS,
			want:   bundle,
		},
		"WithFS sets it for a source built from bytes": {
			source: niceyaml.NewSourceFromBytes([]byte(text),
				niceyaml.WithFilePath("configs/app.yaml"), niceyaml.WithFS(bundle)),
			want: bundle,
		},
		"WithFS sets it for a source with no path": {
			source: niceyaml.NewSourceFromString(text, niceyaml.WithFS(bundle)),
			want:   bundle,
		},
		"a later option replaces it": {
			source: replaced,
		},
		"a source with a path alone has none": {
			source: niceyaml.NewSourceFromString(text, niceyaml.WithFilePath("configs/app.yaml")),
		},
		"a source with no path has none": {
			source: niceyaml.NewSourceFromString(text),
		},
		"a nil source has none": {},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if tc.want == nil {
				assert.Nil(t, tc.source.FS())
			} else {
				assert.Same(t, tc.want, tc.source.FS())
			}

			if tc.source == nil {
				return
			}

			doc := yamltest.Document(t, tc.source)

			if tc.want == nil {
				assert.Nil(t, doc.FS())
			} else {
				assert.Same(t, tc.want, doc.FS())
			}
		})
	}
}

func TestNewSourceFromFS(t *testing.T) {
	t.Parallel()

	fsys := yamltest.MapFS(map[string]string{"configs/app.yaml": "key: value\n"})

	t.Run("reads the file and sets its path", func(t *testing.T) {
		t.Parallel()

		source, err := niceyaml.NewSourceFromFS(fsys, "configs/app.yaml")
		require.NoError(t, err)
		assert.Equal(t, "configs/app.yaml", source.FilePath())
		assert.Equal(t, "configs/app.yaml", source.Name())

		doc := yamltest.Document(t, source)
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

		tcs := map[string]struct {
			want     string
			wantName string
			opts     []niceyaml.SourceOption
		}{
			"by its path": {
				want:     "configs/missing.yaml: " + fs.ErrNotExist.Error(),
				wantName: "configs/missing.yaml",
			},
			"by its name": {
				opts:     []niceyaml.SourceOption{niceyaml.WithName("defaults")},
				want:     "defaults: " + fs.ErrNotExist.Error(),
				wantName: "defaults",
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				_, err := niceyaml.NewSourceFromFS(fsys, "configs/missing.yaml", tc.opts...)
				require.ErrorIs(t, err, fs.ErrNotExist)
				assert.Equal(t, tc.want, err.Error())
				assert.False(t, niceyaml.IsInvalid(err))

				var pathErr *fs.PathError

				require.ErrorAs(t, err, &pathErr, "the chain keeps the error of the file system")

				bound := slices.Collect(niceyaml.Bindings(err))
				require.Len(t, bound, 1)
				assert.Equal(t, tc.wantName, bound[0].Name())
				assert.Equal(t, "configs/missing.yaml", bound[0].FilePath())
			})
		}
	})

	t.Run("reports a file that opens and does not read", func(t *testing.T) {
		t.Parallel()

		tcs := map[string]struct {
			want     string
			wantName string
			opts     []niceyaml.SourceOption
		}{
			"by its path": {
				want:     "configs/app\u240a.yaml: " + fs.ErrPermission.Error(),
				wantName: "configs/app\n.yaml",
			},
			"by its name": {
				opts:     []niceyaml.SourceOption{niceyaml.WithName("defaults")},
				want:     "defaults: " + fs.ErrPermission.Error(),
				wantName: "defaults",
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				fsys := unreadableFS{err: fs.ErrPermission}

				_, err := niceyaml.NewSourceFromFS(fsys, "configs/app\n.yaml", tc.opts...)
				require.ErrorIs(t, err, fs.ErrPermission)
				assert.Equal(t, tc.want, err.Error())
				assert.False(t, niceyaml.IsInvalid(err))

				pathErr, ok := errors.AsType[*fs.PathError](err)
				require.True(t, ok, "the chain keeps the error of the file system")
				assert.Equal(t, "read", pathErr.Op)

				bound := slices.Collect(niceyaml.Bindings(err))
				require.Len(t, bound, 1)
				assert.Equal(t, tc.wantName, bound[0].Name())
				assert.Equal(t, "configs/app\n.yaml", bound[0].FilePath())
			})
		}
	})
}

// unreadableFS is a file system whose every file opens, and whose every
// read of a file fails with a [*fs.PathError] that holds err.
type unreadableFS struct{ err error }

func (f unreadableFS) Open(name string) (fs.File, error) {
	return unreadableFile{name: name, err: f.err}, nil
}

// unreadableFile is a file of an [unreadableFS].
type unreadableFile struct {
	err  error
	name string
}

func (f unreadableFile) Stat() (fs.FileInfo, error) {
	return nil, &fs.PathError{Op: "stat", Path: f.name, Err: f.err}
}

func (f unreadableFile) Read([]byte) (int, error) {
	return 0, &fs.PathError{Op: "read", Path: f.name, Err: f.err}
}

func (unreadableFile) Close() error { return nil }

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

	t.Run("starts from the defaults that Node.Decode starts from", func(t *testing.T) {
		t.Parallel()

		source := niceyaml.NewSourceFromString("seats: 40\n")

		got, err := source.Decode[defaultsCafe](t.Context(), defaultsDecoding)
		require.NoError(t, err)

		want, err := yamltest.Document(t, source).Decode[defaultsCafe](t.Context(), defaultsDecoding)
		require.NoError(t, err)
		assert.Equal(t, want, got)
		assert.Equal(t, 40, got.Seats)
		assert.Equal(t, "SEK", got.Currency)
		assert.Equal(t, 9101, got.Kitchen.Port)
	})

	t.Run("DecodeInto keeps the fields the document leaves out", func(t *testing.T) {
		t.Parallel()

		source := niceyaml.NewSourceFromString("{}\n")

		cfg := config{Name: "default"}
		require.NoError(t, source.DecodeInto(t.Context(), &cfg))
		assert.Equal(t, config{Name: "default"}, cfg)
	})

	t.Run("decodes the one document beside an empty one", func(t *testing.T) {
		t.Parallel()

		tcs := map[string]string{
			"empty document below":  stringtest.LinesLF("name: a", "---"),
			"empty documents above": stringtest.LinesLF("---", "# head", "---", "name: a"),
		}

		for name, input := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				source := niceyaml.NewSourceFromString(input)

				got, err := source.Decode[config](t.Context())
				require.NoError(t, err)
				assert.Equal(t, config{Name: "a"}, got)

				cfg := config{Name: "default"}
				require.NoError(t, source.DecodeInto(t.Context(), &cfg))
				assert.Equal(t, config{Name: "a"}, cfg)
			})
		}
	})

	t.Run("several documents return the error Document returns", func(t *testing.T) {
		t.Parallel()

		source := niceyaml.NewSourceFromString(stringtest.LinesLF(
			"name: a",
			"---",
			"name: b",
		), niceyaml.WithName("two.yaml"))

		got, err := source.Decode[config](t.Context())
		require.ErrorIs(t, err, niceyaml.ErrMultipleDocuments)
		assert.Equal(t, config{}, got)
		assert.Equal(t, "two.yaml:2:1: multiple documents in source: 2 documents", err.Error())
	})

	t.Run("a decode error binds to the source", func(t *testing.T) {
		t.Parallel()

		source := niceyaml.NewSourceFromString("name: [1]\n", niceyaml.WithName("x.yaml"))

		_, err := source.Decode[config](t.Context())
		assert.Same(t, source, yamltest.RequireSourceError(t, err).Source())
	})
}

func TestSource_SelfValidate(t *testing.T) {
	t.Parallel()

	type config struct {
		Servers []minPort `yaml:"servers"`
	}

	// The second server sets no port, whatever the Source holds.
	failing := func() *config {
		return &config{Servers: []minPort{{Port: 80}, {}}}
	}

	// The file that holds the value failing returns.
	held := stringtest.LinesLF(
		"servers:",
		"  - port: 80",
		"  - {}",
	)

	// The walk runs through the one document of the Source, so one call
	// validates a value with a file behind it and a value with none. An
	// empty Source holds no line for the error, so its text is the path
	// and the message.
	tcs := map[string]struct {
		source string
		name   string
		want   string
		// Whether the Source holds the second server, whose line the
		// excerpt of the error then shows.
		located bool
	}{
		"no file": {
			want: "$.servers[1].port: port must be at least 1",
		},
		"no file under a name": {
			name: "defaults",
			want: "defaults: $.servers[1].port: port must be at least 1",
		},
		"file of comments alone": {
			source: "# every default applies\n",
			name:   "app.yaml",
			want:   "app.yaml: $.servers[1].port: port must be at least 1",
		},
		"file that holds the value": {
			source:  held,
			name:    "app.yaml",
			want:    "app.yaml:3:3: $.servers[1].port: port must be at least 1",
			located: true,
		},
		"file that holds the value above a trailing header": {
			source:  held + "---\n",
			name:    "app.yaml",
			want:    "app.yaml:3:3: $.servers[1].port: port must be at least 1",
			located: true,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			source := niceyaml.NewSourceFromString(tc.source, niceyaml.WithName(tc.name))

			wantFormat := tc.want
			if tc.located {
				wantFormat = stringtest.JoinLF(tc.want, "", "   3 |   - {}", "     |   ^")
			}

			err := source.SelfValidate(t.Context(), failing())
			require.EqualError(t, err, tc.want)
			assert.True(t, niceyaml.IsInvalid(err))
			assert.Equal(t, wantFormat, niceyaml.FormatError(err, niceyaml.WithContextLines(0)))

			bound := yamltest.RequireSourceError(t, err)
			assert.Same(t, source, bound.Source())

			_, ok := bound.Position()
			assert.Equal(t, tc.located, ok)

			// The root Node of the document returns the same error.
			doc := yamltest.Document(t, source)
			require.EqualError(t, doc.SelfValidate(t.Context(), failing()), tc.want)

			// The walk runs whatever WithSelfValidation says.
			require.EqualError(t,
				source.SelfValidate(t.Context(), failing(), niceyaml.WithSelfValidation(false)),
				tc.want,
			)

			require.NoError(t, source.SelfValidate(t.Context(), &config{Servers: []minPort{{Port: 80}}}))
		})
	}

	t.Run("validates the layers over a file that may not exist", func(t *testing.T) {
		t.Parallel()

		layers := map[string]struct {
			source string
		}{
			"no file": {},
			"file":    {source: held},
		}

		for name, tc := range layers {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				source := niceyaml.NewSourceFromString(tc.source)

				// The decode of an empty Source leaves the defaults as
				// they were, and the file sets the same value.
				cfg := failing()
				require.NoError(t, source.DecodeInto(t.Context(), cfg, niceyaml.WithSelfValidation(false)))
				assert.Equal(t, failing(), cfg)
				require.Error(t, source.SelfValidate(t.Context(), cfg))

				// A later layer sets the port, and the value passes.
				cfg.Servers[1].Port = 8080
				require.NoError(t, source.SelfValidate(t.Context(), cfg))
			})
		}
	})

	t.Run("several documents return the error DecodeInto returns", func(t *testing.T) {
		t.Parallel()

		source := niceyaml.NewSourceFromString(stringtest.LinesLF(
			"servers: []",
			"---",
			"servers: []",
		), niceyaml.WithName("two.yaml"))

		err := source.SelfValidate(t.Context(), failing())
		require.ErrorIs(t, err, niceyaml.ErrMultipleDocuments)
		require.EqualError(t, err, "two.yaml:2:1: multiple documents in source: 2 documents")
		require.EqualError(t, source.DecodeInto(t.Context(), failing()), err.Error())

		// A value that passes meets the same error.
		require.ErrorIs(t, source.SelfValidate(t.Context(), &config{}), niceyaml.ErrMultipleDocuments)
	})

	t.Run("document that does not parse returns its syntax error", func(t *testing.T) {
		t.Parallel()

		source := niceyaml.NewSourceFromString("servers: [\n", niceyaml.WithName("bad.yaml"))

		_, fileErr := source.File()
		require.ErrorIs(t, fileErr, niceyaml.ErrSyntax)

		err := source.SelfValidate(t.Context(), failing())
		require.ErrorIs(t, err, niceyaml.ErrSyntax)
		require.EqualError(t, err, fileErr.Error())
	})

	t.Run("nil target", func(t *testing.T) {
		t.Parallel()

		err := niceyaml.NewSourceFromString("").SelfValidate(t.Context(), nil)
		require.ErrorIs(t, err, niceyaml.ErrSelfValidateTarget)
		assert.False(t, niceyaml.IsInvalid(err))
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

	defaults := niceyaml.NewSourceFromString(stringtest.LinesLF(
		"base: &base",
		"  port: 8080",
	), niceyaml.WithName("defaults.yaml"))

	// The server of this reference merges its own base, so an alias to the
	// server reads port 443 whatever base the document defines.
	merged := niceyaml.NewSourceFromString(stringtest.LinesLF(
		"base: &base {port: 443}",
		"server: &server {<<: *base}",
	))

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
			"Node.Validate": func(ctx context.Context, _ *niceyaml.Source, doc *niceyaml.Node, v niceyaml.Validator) error {
				return doc.Validate(ctx, v)
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
				input: stringtest.LinesLF(
					"server:",
					"  <<: *base",
				),
				refs: []*niceyaml.Source{defaults},
			},
			"violation beside an alias to the reference": {
				input: stringtest.LinesLF("name: 1", "server: *base"),
				refs:  []*niceyaml.Source{defaults},
				want:  []string{`app.yaml:1:7: $.name: expected "string", got "integer"`},
			},
			"violation beside a merge of the reference": {
				input: stringtest.LinesLF(
					"server:",
					"  <<: *base",
					"  port: x",
				),
				refs: []*niceyaml.Source{defaults},
				want: []string{`app.yaml:3:9: $.server.port: expected "integer", got "string"`},
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

					doc := yamltest.Document(t, src)

					// The validator records the Node each run gets.
					var seen []*niceyaml.Node

					v := niceyaml.ValidatorFunc(func(ctx context.Context, n *niceyaml.Node) error {
						seen = append(seen, n)

						return n.Validate(ctx, serverSchema)
					})

					err := check(t.Context(), src, doc, v)
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

		src := niceyaml.NewSourceFromString(
			stringtest.LinesLF("server: *base", "---", "server: {port: x}"),
			niceyaml.WithName("app.yaml"),
			niceyaml.WithReferences(defaults),
		)

		want := []string{`app.yaml:3:16: $.server.port: expected "integer", got "string"`}

		err := src.ValidateDocuments(t.Context(), serverSchema)
		assert.Equal(t, want, bindingMessages(err))

		docs := yamltest.Documents(t, src, 2)

		_, err = docs[0].Decode[config](t.Context(), niceyaml.WithValidator(serverSchema))
		require.NoError(t, err)

		_, err = docs[1].Decode[config](t.Context(), niceyaml.WithValidator(serverSchema))
		assert.Equal(t, want, bindingMessages(err))
	})

	t.Run("anchors of the references", func(t *testing.T) {
		t.Parallel()

		first := niceyaml.NewSourceFromString(stringtest.LinesLF("old: &old 1", "x: &x 1"))
		second := niceyaml.NewSourceFromString("x: &x 2\n")
		nested := niceyaml.NewSourceFromString("list: &list [*x, 3]\n", niceyaml.WithReferences(second))

		tcs := map[string]struct {
			input string
			refs  []*niceyaml.Source
			want  map[string]any
		}{
			"later reference overrides an earlier one": {
				input: stringtest.LinesLF("a: *old", "b: *x"),
				refs:  []*niceyaml.Source{first, second},
				want:  map[string]any{"a": uint64(1), "b": uint64(2)},
			},
			"reference brings its own references": {
				input: stringtest.LinesLF("a: *list", "b: *x"),
				refs:  []*niceyaml.Source{nested},
				want:  map[string]any{"a": []any{uint64(2), uint64(3)}, "b": uint64(2)},
			},
			"nil reference is skipped": {
				input: "a: *x\n",
				refs:  []*niceyaml.Source{nil, second, nil},
				want:  map[string]any{"a": uint64(2)},
			},
			"anchor of the document before the alias wins": {
				input: stringtest.LinesLF("x: &x 5", "a: *x"),
				refs:  []*niceyaml.Source{second},
				want:  map[string]any{"x": uint64(5), "a": uint64(5)},
			},
			"reference with CRLF line endings": {
				input: "a: *y\n",
				refs: []*niceyaml.Source{
					niceyaml.NewSourceFromString(stringtest.LinesCRLF(
						"y: &y",
						"  k: v",
					)),
				},
				want: map[string]any{"a": map[string]any{"k": "v"}},
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

		list := niceyaml.NewSourceFromString(stringtest.LinesLF("x: &x 1", "list: &list [*x]"))

		tcs := map[string]struct {
			ref   *niceyaml.Source
			input string
			want  target
		}{
			"anchor of the document before the alias": {
				ref:   list,
				input: stringtest.LinesLF("x: &x 9", "v: *list"),
				want:  target{X: 9, V: []int{1}},
			},
			"anchor of the document after the alias": {
				ref:   list,
				input: stringtest.LinesLF("v: *list", "x: &x 9"),
				want:  target{X: 9, V: []int{1}},
			},
			"alias of the document to its own anchor": {
				ref:   list,
				input: stringtest.LinesLF("x: &x 9", "v: *list", "w: [*x]"),
				want:  target{X: 9, V: []int{1}, W: []int{9}},
			},
			"two anchors of the document": {
				ref:   list,
				input: stringtest.LinesLF("w: [&x 8, *x, &x 9, *x]", "v: *list"),
				want:  target{V: []int{1}, W: []int{8, 8, 9, 9}},
			},
			"merge inside the reference": {
				ref:   merged,
				input: stringtest.LinesLF("base: &base {port: 80}", "server: *server"),
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
				input: stringtest.LinesLF("base: &base {port: 80}", "server: *server"),
				want:  443,
			},
			"alias of the document reads the document": {
				input: stringtest.LinesLF("base: &base {port: 80}", "server: *base"),
				err:   []string{`app.yaml:1:20: $.server.port: value does not match const`},
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				doc := yamltest.FirstDocument(t, tc.input,
					niceyaml.WithName("app.yaml"),
					niceyaml.WithReferences(merged),
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
		doc := yamltest.FirstDocument(t, stringtest.LinesLF("kind: Deployment", "<<: *base"),
			yamltest.References("base: &base {kind: Bogus}\n"))

		got, err := doc.Decode[map[string]any](t.Context())
		require.NoError(t, err)
		assert.Equal(t, map[string]any{"kind": "Bogus"}, got)
	})

	t.Run("last anchor of a name in the references wins", func(t *testing.T) {
		t.Parallel()

		// The References section of the package documentation states the
		// rule. An alias inside a reference reads the last anchor of its
		// name, in a decode into any as in a typed decode.
		tcs := map[string]struct {
			refs []*niceyaml.Source
		}{
			"in one reference": {
				refs: []*niceyaml.Source{
					niceyaml.NewSourceFromString(stringtest.LinesLF(
						"x: &x 1",
						"list: &list [*x]",
						"y: &x 2",
					)),
				},
			},
			"in two references": {
				refs: []*niceyaml.Source{
					niceyaml.NewSourceFromString(stringtest.LinesLF("x: &x 1", "list: &list [*x]")),
					niceyaml.NewSourceFromString("x: &x 2\n"),
				},
			},
			"anchor after the alias in its reference": {
				refs: []*niceyaml.Source{
					niceyaml.NewSourceFromString(stringtest.LinesLF("list: &list [*x]", "x: &x 2")),
				},
			},
			"in a reference of the reference": {
				refs: []*niceyaml.Source{
					niceyaml.NewSourceFromString("list: &list [*x]\n",
						yamltest.References(stringtest.LinesLF("x: &x 1", "y: &x 2"))),
				},
			},
			"anchor inside an anchor of its name": {
				refs: []*niceyaml.Source{
					niceyaml.NewSourceFromString(stringtest.LinesLF("outer: &x [&x 2]", "list: &list [*x]")),
				},
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				doc := yamltest.FirstDocument(t, "v: *list\n", niceyaml.WithReferences(tc.refs...))

				untyped, err := doc.Decode[map[string]any](t.Context())
				require.NoError(t, err)
				assert.Equal(t, map[string]any{"v": []any{uint64(2)}}, untyped)

				typed, err := doc.Decode[map[string][]int](t.Context())
				require.NoError(t, err)
				assert.Equal(t, map[string][]int{"v": {2}}, typed)
			})
		}
	})

	t.Run("anchor inside an anchor a decode reads registers its name again", func(t *testing.T) {
		t.Parallel()

		// The References section of the package documentation describes
		// this exception to the rule that the last anchor of a name wins.
		// A merge of layers follows the rule.
		tcs := map[string]struct {
			ref     string
			input   string
			untyped map[string]any
			typed   map[string][]int
			layered map[string]any
		}{
			"alias read after the outer anchor": {
				ref:     stringtest.LinesLF("base: &base [&x 1, *x]", "z: &x 9"),
				input:   stringtest.LinesLF("a: *base", "b: [*x]"),
				untyped: map[string]any{"a": []any{uint64(1), uint64(1)}, "b": []any{uint64(1)}},
				typed:   map[string][]int{"a": {1, 1}, "b": {1}},
				layered: map[string]any{"a": []any{uint64(1), uint64(9)}, "b": []any{uint64(9)}},
			},
			"typed decode of an anchor read before the outer anchor": {
				ref:     stringtest.LinesLF("outer: &outer [&x 1]", "y: &x 2", "list: &list [*x]"),
				input:   stringtest.LinesLF("l: *list", "o: *outer"),
				untyped: map[string]any{"l": []any{uint64(2)}, "o": []any{uint64(1)}},
				typed:   map[string][]int{"l": {1}, "o": {1}},
				layered: map[string]any{"l": []any{uint64(2)}, "o": []any{uint64(1)}},
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				src := niceyaml.NewSourceFromString(tc.input, yamltest.References(tc.ref))
				doc := yamltest.Document(t, src)

				untyped, err := doc.Decode[map[string]any](t.Context())
				require.NoError(t, err)
				assert.Equal(t, tc.untyped, untyped)

				typed, err := doc.Decode[map[string][]int](t.Context())
				require.NoError(t, err)
				assert.Equal(t, tc.typed, typed)

				merged, err := niceyaml.NewSourceFromLayers(t.Context(), []*niceyaml.Source{src})
				require.NoError(t, err)

				layered, err := yamltest.Document(t, merged).Decode[map[string]any](t.Context())
				require.NoError(t, err)
				assert.Equal(t, tc.layered, layered)
			})
		}
	})

	t.Run("scoped node reads the anchors of the references first", func(t *testing.T) {
		t.Parallel()

		// The anchor of the document outside the scoped node reads an
		// anchor of the reference, so the decode reads that one first.
		doc := yamltest.FirstDocument(t, stringtest.LinesLF("d: &d {p: *base}", "svc: {x: *d}"),
			yamltest.References("base: &base {port: 80}\n"))

		got, err := yamltest.AtOrSelf(t, doc, paths.Doc().Child("svc")).Decode[map[string]any](t.Context())
		require.NoError(t, err)
		assert.Equal(t, map[string]any{"x": map[string]any{"p": map[string]any{"port": uint64(80)}}}, got)
	})

	t.Run("references serve every decode from any goroutine", func(t *testing.T) {
		t.Parallel()

		// Two sources share the parse of one reference.
		ref := niceyaml.NewSourceFromString("base: &x 1\n")

		docs := slices.Concat(
			yamltest.Documents(t, niceyaml.NewSourceFromString(
				stringtest.LinesLF("b: *x", "---", "c: *x"),
				niceyaml.WithReferences(ref),
			), 2),
			yamltest.Documents(t, niceyaml.NewSourceFromString("d: *x\n", niceyaml.WithReferences(ref)), 1),
		)

		var wg sync.WaitGroup

		for range 4 {
			for i, doc := range docs {
				key := []string{"b", "c", "d"}[i]

				wg.Go(func() {
					got, err := doc.Decode[map[string]int](t.Context())
					if assert.NoError(t, err) {
						assert.Equal(t, map[string]int{key: 1}, got)
					}
				})

				wg.Go(func() {
					untyped, err := doc.Decode[any](t.Context())
					if assert.NoError(t, err) {
						assert.Equal(t, map[string]any{key: uint64(1)}, untyped)
					}
				})
			}
		}

		wg.Wait()
	})

	t.Run("a path through a key beside a merge of a reference", func(t *testing.T) {
		t.Parallel()

		// The mapping of base holds host and port. A key that it does not
		// hold resolves as it does in a mapping without the merge key. The
		// value of a key that it holds lies in defaults.yaml.
		defs := niceyaml.NewSourceFromString(stringtest.LinesLF("base: &base", "  host: localhost", "  port: 80"),
			niceyaml.WithName("defaults.yaml"))

		doc := yamltest.FirstDocument(t,
			stringtest.LinesLF("version: v1", "<<: *base", "other:", "  name: web", "  <<: *base"),
			niceyaml.WithName("app.yaml"),
			niceyaml.WithReferences(defs),
		)

		var version string

		ok, err := doc.DecodeIfPresent(t.Context(), paths.Doc().Child("version"), &version)
		require.NoError(t, err)
		assert.True(t, ok)
		assert.Equal(t, "v1", version)

		var tls string

		ok, err = doc.DecodeIfPresent(t.Context(), paths.Doc().Child("other", "tls"), &tls)
		require.NoError(t, err)
		assert.False(t, ok)

		name := paths.Doc().Child("other", "name")

		ranges, err := doc.Ranges(name)
		require.NoError(t, err)
		assert.Equal(t, "3:8-3:11", ranges.String())
		require.EqualError(
			t,
			doc.Bind(niceyaml.NewError("bad", niceyaml.AtPath(name))),
			"app.yaml:4:9: $.other.name: bad",
		)

		names, err := doc.Nodes(paths.Doc().Child("other").Recursive("name"))
		require.NoError(t, err)
		require.Len(t, names, 1)
		assert.Equal(t, "$.other.name", names[0].Path().String())

		// The merge key brings in host from defaults.yaml.
		host := paths.Doc().Child("other", "host")

		_, err = doc.DecodeAt[string](t.Context(), host)
		require.ErrorIs(t, err, paths.ErrReferenceAlias)
		require.EqualError(t, err,
			"app.yaml:5:7: $.other: resolve $.other.host: alias reads a reference document: *base")
		assert.False(t, niceyaml.IsInvalid(err))

		err = doc.Bind(niceyaml.NewError("bad", niceyaml.AtPath(host)))
		require.EqualError(t, err, "app.yaml:5:7: $.other.host: bad")

		bound := yamltest.RequireSourceError(t, err)
		require.ErrorIs(t, bound.Unresolved(), paths.ErrReferenceAlias)

		// A decode of the mapping reads the entries of defaults.yaml.
		other, err := doc.At(paths.Doc().Child("other"))
		require.NoError(t, err)

		got, err := other.Decode[map[string]any](t.Context())
		require.NoError(t, err)
		assert.Equal(t, map[string]any{"name": "web", "host": "localhost", "port": uint64(80)}, got)
	})

	t.Run("a path resolves in the document alone", func(t *testing.T) {
		t.Parallel()

		src := niceyaml.NewSourceFromString(stringtest.LinesLF("name: app", "server: *base"),
			niceyaml.WithName("app.yaml"),
			niceyaml.WithReferences(defaults),
		)

		doc := yamltest.Document(t, src)

		merged := yamltest.Document(t, niceyaml.NewSourceFromString(
			stringtest.LinesLF("name: app", "server: {<<: *base}"),
			niceyaml.WithName("app.yaml"),
			niceyaml.WithReferences(defaults),
		))

		alias := paths.Current().Child("server")
		port := alias.Child("port")

		// The error of each read that enters the alias, the position of the
		// alias, and the path that the error binds under. Each error binds
		// at the alias, under the path of the value that reads it. A merge
		// key is that value for a `.*` that lists the keys that it brings in.
		type read struct {
			err  error
			at   position.Position
			path string
		}

		ifPresent := func(doc *niceyaml.Node) error {
			var v int

			_, err := doc.DecodeIfPresent(t.Context(), port, &v)

			return err //nolint:wrapcheck // The test reads the error of the decode.
		}

		_, atBelow := doc.At(port)
		_, decodeBelow := doc.DecodeAt[int](t.Context(), port)
		_, nodesBelow := doc.Nodes(alias.ChildAll())
		_, rangesBelow := doc.Ranges(port)
		_, matchBelow := matcher.Content(port, 8080).Match(t.Context(), doc)
		_, atMerge := merged.At(port)
		_, decodeMerge := merged.DecodeAt[int](t.Context(), port)
		_, nodesMerge := merged.Nodes(alias.ChildAll())

		at, mergeAt := position.New(1, 8), position.New(1, 13)

		tcs := map[string]read{
			"At below the alias":                         {err: atBelow, at: at},
			"DecodeAt below the alias":                   {err: decodeBelow, at: at},
			"DecodeIfPresent below the alias":            {err: ifPresent(doc), at: at},
			"Nodes below the alias":                      {err: nodesBelow, at: at},
			"Ranges below the alias":                     {err: rangesBelow, at: at},
			"Content matcher below the alias":            {err: matchBelow, at: at},
			"At below a merge of the alias":              {err: atMerge, at: mergeAt},
			"DecodeAt below a merge of the alias":        {err: decodeMerge, at: mergeAt},
			"DecodeIfPresent below a merge of the alias": {err: ifPresent(merged), at: mergeAt},
			"Nodes below a merge of the alias":           {err: nodesMerge, at: mergeAt, path: "$.server.<<"},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				require.ErrorIs(t, tc.err, paths.ErrAlias)
				require.ErrorIs(t, tc.err, paths.ErrReferenceAlias)
				require.ErrorContains(t, tc.err, "alias reads a reference document: *base")
				assert.False(t, niceyaml.IsInvalid(tc.err))
				assert.Len(t, slices.Collect(niceyaml.Problems(tc.err)), 1)

				want := tc.path
				if want == "" {
					want = "$.server"
				}

				bound := yamltest.RequireSourceError(t, tc.err)
				assert.Equal(t, tc.at, yamltest.PositionOf(t, bound))
				assert.Equal(t, want, yamltest.PathOf(t, bound).String())
			})
		}

		t.Run("Ranges at the alias", func(t *testing.T) {
			t.Parallel()

			got, err := doc.Ranges(alias)
			require.NoError(t, err)
			assert.Equal(t, position.Ranges{
				yamltest.LineRange(1, 8, 9),
			}, got)
		})

		t.Run("At and Nodes at the alias", func(t *testing.T) {
			t.Parallel()

			// The path selects the alias, and a decode of its Node reads the
			// anchor of the reference.
			at := yamltest.At(t, doc, alias)

			nodes, err := doc.Nodes(alias)
			require.NoError(t, err)
			require.Len(t, nodes, 1)

			for _, n := range []*niceyaml.Node{at, nodes[0]} {
				assert.Equal(t, paths.Doc().Child("server"), n.Path())
				assert.Equal(t, "*base", n.AST().String())
				assert.Equal(t, position.NewSpan(1, 2), n.Span())

				got, err := n.Decode[server](t.Context())
				require.NoError(t, err)
				assert.Equal(t, server{Port: 8080}, got)
			}
		})
	})

	t.Run("a path names an alias key of a reference anchor", func(t *testing.T) {
		t.Parallel()

		// The decode reads the key of the anchor k at the alias key, and so
		// does a path.
		keys := niceyaml.NewSourceFromString("&k name: 1\n")

		tcs := map[string]struct {
			input string
		}{
			"block mapping": {input: "m:\n  x: 1\n  *k : 2\n"},
			"flow mapping":  {input: "m: {x: 1, *k : 2}\n"},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				doc := yamltest.FirstDocument(t, tc.input, niceyaml.WithName("app.yaml"), niceyaml.WithReferences(keys))

				got, err := doc.Decode[map[string]map[string]int](t.Context())
				require.NoError(t, err)
				assert.Equal(t, map[string]map[string]int{"m": {"x": 1, "name": 2}}, got)

				value, err := doc.DecodeAt[int](t.Context(), paths.Current().Child("m", "name"))
				require.NoError(t, err)
				assert.Equal(t, 2, value)
			})
		}
	})

	t.Run("error under an alias to a reference binds at the alias", func(t *testing.T) {
		t.Parallel()

		type target struct {
			Server minPort `yaml:"server"`
		}

		tcs := map[string]struct {
			reference string
			validator niceyaml.Validator
			want      string
		}{
			"error of a SelfValidator": {
				reference: stringtest.LinesLF(
					"base: &base",
					"  port: 0",
				),
				validator: niceyaml.NoValidator(),
				want:      "app.yaml:2:9: $.server.port: port must be at least 1",
			},
			"key the reference leaves out": {
				reference: stringtest.LinesLF(
					"base: &base",
					"  host: localhost",
				),
				validator: niceyaml.NoValidator(),
				want:      "app.yaml:2:9: $.server.port: port must be at least 1",
			},
			"violation of a schema": {
				reference: stringtest.LinesLF(
					"base: &base",
					"  port: oops",
				),
				validator: serverSchema,
				want:      `app.yaml:2:9: $.server.port: expected "integer", got "string"`,
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				doc := yamltest.FirstDocument(t, stringtest.LinesLF("name: app", "server: *base"),
					niceyaml.WithName("app.yaml"),
					yamltest.References(tc.reference),
				)

				_, err := doc.Decode[target](t.Context(), niceyaml.WithValidator(tc.validator))
				require.EqualError(t, err, tc.want)
				assert.True(t, niceyaml.IsInvalid(err))

				// The excerpt marks the alias under every message.
				assert.Equal(t, stringtest.JoinLF(
					tc.want,
					"",
					"   2 | server: *base",
					"     |         ^",
				), niceyaml.FormatError(err, niceyaml.WithContextLines(0)))

				bound := yamltest.RequireSourceError(t, err)

				near, ok := bound.Nearest()
				require.True(t, ok)
				assert.Equal(t, paths.Doc().Child("server"), near)
				require.ErrorIs(t, bound.Unresolved(), paths.ErrAlias)
				assert.Equal(t, paths.Doc().Child("server", "port"), yamltest.PathOf(t, bound))
			})
		}
	})

	t.Run("value that decodes itself at a root alias binds at the alias", func(t *testing.T) {
		t.Parallel()

		// A time.Duration reports its own error, which does not carry a
		// token. The decode places it at the alias, as it places the error
		// of a scalar that the document writes.
		doc := yamltest.FirstDocument(t, "*wait\n",
			niceyaml.WithName("app.yaml"),
			yamltest.References("wait: &wait soon\n"),
		)

		_, err := doc.Decode[time.Duration](t.Context())
		require.EqualError(t, err, `app.yaml:1:1: $: time: invalid duration "soon"`)
		require.ErrorIs(t, err, niceyaml.ErrDecode)
		assert.True(t, niceyaml.IsInvalid(err))
	})

	t.Run("error at a key before a merge of a reference binds at the key", func(t *testing.T) {
		t.Parallel()

		// The merge sets no kind, so the decode keeps the misspelled one, and
		// the caret points at the typo.
		doc := yamltest.FirstDocument(t, stringtest.LinesLF("kind: Deploymnet", "<<: *base"),
			niceyaml.WithName("app.yaml"),
			yamltest.References(stringtest.LinesLF(
				"base: &base",
				"  replicas: 1",
			)),
		)

		kinds := schema.MustCompile([]byte(`{"properties": {"kind": {"enum": ["Deployment", "Service"]}}}`))

		err := doc.Validate(t.Context(), kinds)
		require.EqualError(t, err, "app.yaml:1:7: $.kind: value does not match any enum member")
		assert.Equal(t, stringtest.JoinLF(
			"app.yaml:1:7: $.kind: value does not match any enum member",
			"",
			"   1 | kind: Deploymnet",
			"     |       ^^^^^^^^^^",
		), niceyaml.FormatError(err, niceyaml.WithContextLines(0)))

		bound := yamltest.RequireSourceError(t, err)

		_, ok := bound.Nearest()
		assert.False(t, ok)
		require.NoError(t, bound.Unresolved())
	})

	t.Run("error beside a merge of a reference binds by the keys of the reference", func(t *testing.T) {
		t.Parallel()

		// The anchor base sets the replicas and leaves out the name. The
		// fallback is an alias to a reference document itself.
		doc := yamltest.FirstDocument(t,
			stringtest.LinesLF("kind: a", "list: [1]", "spec: {q: 1}", "fallback: *base", "<<: *base"),
			niceyaml.WithName("app.yaml"),
			yamltest.References(stringtest.LinesLF(
				"base: &base",
				"  replicas: 1",
			)),
		)

		tcs := map[string]struct {
			path paths.Path
			want string
			// The path that Nearest reports, or empty for none.
			near string
			// The text of the error that Unresolved returns, or empty for
			// none.
			unresolved string
			// Whether that error wraps paths.ErrReferenceAlias.
			reference bool
		}{
			"key the mapping writes": {
				path: paths.Doc().Child("kind"),
				want: "app.yaml:1:7: $.kind: bad",
			},
			"key the merge brings in": {
				path:       paths.Doc().Child("replicas"),
				want:       "app.yaml:5:5: $.replicas: bad",
				near:       "$",
				unresolved: "resolve $.replicas: alias reads a reference document: *base",
				reference:  true,
			},
			"key that neither writes": {
				path: paths.Doc().Child("name"),
				want: "app.yaml:1:1: $.name: bad",
				near: "$",
			},
			"key below an alias that the mapping writes": {
				path:       paths.Doc().Child("fallback", "replicas"),
				want:       "app.yaml:4:11: $.fallback.replicas: bad",
				near:       "$.fallback",
				unresolved: "resolve $.fallback.replicas: alias reads a reference document: *base",
				reference:  true,
			},
			// No entry has a real key "<<", so the name selects the merge key.
			"merge key": {
				path: paths.Doc().Child("<<"),
				want: "app.yaml:5:5: $.<<: bad",
			},
			"key below the merge key": {
				path:       paths.Doc().Child("<<", "replicas"),
				want:       "app.yaml:5:5: $.<<.replicas: bad",
				near:       "$.<<",
				unresolved: "resolve $.<<.replicas: alias reads a reference document: *base",
				reference:  true,
			},
			// A path that selects nothing below a key that the merge does not
			// bring in reads as it reads in a mapping without the merge key,
			// so it binds at the nearest node the document holds.
			"index past the end of a sequence that the mapping writes": {
				path: paths.Doc().Child("list").Index(5),
				want: "app.yaml:2:1: $.list[5]: bad",
				near: "$.list",
			},
			"index into a scalar that the mapping writes": {
				path: paths.Doc().Child("kind").Index(0),
				want: "app.yaml:1:7: $.kind[0]: bad",
				near: "$.kind",
			},
			"key below a scalar that the mapping writes": {
				path: paths.Doc().Child("spec", "q", "r"),
				want: "app.yaml:3:11: $.spec.q.r: bad",
				near: "$.spec.q",
			},
			"entry of a key that neither writes": {
				path: paths.Doc().Child("missing").Key(),
				want: "app.yaml:1:1: $.missing~: bad",
				near: "$",
			},
			"key of an entry that the mapping writes": {
				path: paths.Doc().Child("list").Key().Key(),
				want: "app.yaml:2:1: $.list~: bad",
			},
			// A key is a scalar, so a selector after its `~` binds at the key.
			"index into a key that the mapping writes": {
				path: paths.Doc().Child("list").Key().Index(0),
				want: "app.yaml:2:1: $.list~[0]: bad",
				near: "$.list~",
			},
			"name below a key that the mapping writes": {
				path: paths.Doc().Child("kind").Key().Child("x"),
				want: "app.yaml:1:1: $.kind~.x: bad",
				near: "$.kind~",
			},
			// An element does not have a key, so a selector after its `~`
			// binds at the element.
			"name below the key of an element": {
				path: paths.Doc().Child("list").Index(0).Key().Child("x"),
				want: "app.yaml:2:8: $.list[0]~.x: bad",
				near: "$.list[0]",
			},
			"key of an element": {
				path: paths.Doc().Child("list").Index(0).Key(),
				want: "app.yaml:2:8: $.list[0]~: bad",
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				err := doc.Bind(niceyaml.NewError("bad", niceyaml.AtPath(tc.path)))
				require.EqualError(t, err, tc.want)

				bound := yamltest.RequireSourceError(t, err)

				near, ok := bound.Nearest()
				if tc.near == "" {
					assert.False(t, ok)
				} else {
					require.True(t, ok)
					assert.Equal(t, tc.near, near.String())
				}

				if tc.unresolved == "" {
					require.NoError(t, bound.Unresolved())

					return
				}

				require.EqualError(t, bound.Unresolved(), tc.unresolved)
				assert.Equal(t, tc.reference, errors.Is(bound.Unresolved(), paths.ErrReferenceAlias))
			})
		}
	})

	t.Run("selector after the key of an element alias binds at the alias with or without a merge", func(t *testing.T) {
		t.Parallel()

		// The `~` of an element selects nothing whatever the element holds,
		// so a path through it does not read the alias of the element.
		docs := map[string]*niceyaml.Node{}
		for name, merge := range map[string]string{"merge": "<<: *base", "plain": "spec: b"} {
			docs[name] = yamltest.FirstDocument(t,
				stringtest.LinesLF("kind: a", "list: [*base, *nope]", merge),
				niceyaml.WithName("app.yaml"),
				yamltest.References(stringtest.LinesLF(
					"base: &base",
					"  replicas: 1",
				)),
			)
		}

		tcs := map[string]struct {
			path paths.Path
			want string
			// The path that Nearest reports, or empty for none.
			near string
		}{
			"name below the key of an alias to a reference document": {
				path: paths.Doc().Child("list").Index(0).Key().Child("x"),
				want: "app.yaml:2:8: $.list[0]~.x: bad",
				near: "$.list[0]",
			},
			"name below the key of an alias without an anchor": {
				path: paths.Doc().Child("list").Index(1).Key().Child("x"),
				want: "app.yaml:2:15: $.list[1]~.x: bad",
				near: "$.list[1]",
			},
			"key of an alias to a reference document": {
				path: paths.Doc().Child("list").Index(0).Key(),
				want: "app.yaml:2:8: $.list[0]~: bad",
			},
		}

		for name, tc := range tcs {
			for docName, doc := range docs {
				t.Run(name+"/"+docName, func(t *testing.T) {
					t.Parallel()

					err := doc.Bind(niceyaml.NewError("bad", niceyaml.AtPath(tc.path)))
					require.EqualError(t, err, tc.want)

					bound := yamltest.RequireSourceError(t, err)
					require.NoError(t, bound.Unresolved())

					near, ok := bound.Nearest()
					if tc.near == "" {
						assert.False(t, ok)

						return
					}

					require.True(t, ok)
					assert.Equal(t, tc.near, near.String())
				})
			}
		}
	})

	t.Run("error beside a merge of a reference in a document past the alias limit", func(t *testing.T) {
		t.Parallel()

		// The path reads the mapping of the merge key as a path reads a
		// merge of the document, which the alias limit does not stop. The
		// error thus binds as it does with the limit off.
		const merge = "x:\n  kind: a\n  <<: *base\n"

		tcs := map[string]struct {
			input string
			refs  string
			want  string
		}{
			"aliases to anchors of the document": {
				input: yamltest.AliasLevels(5) + merge,
				refs:  "base: &base {replicas: 1}\n",
				want:  "app.yaml:9:9: $.x.kind: bad",
			},
			"aliases to an anchor of a reference document": {
				input: "v: " + yamltest.FlowSequence("*d", 500) + "\n" + merge,
				refs:  "base: &base {replicas: 1}\nd: &d " + yamltest.FlowSequence("x", 1000) + "\n",
				want:  "app.yaml:3:9: $.x.kind: bad",
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				refs := yamltest.References(tc.refs)
				path := paths.Doc().Child("x", "kind")

				doc := yamltest.FirstDocument(t, tc.input, niceyaml.WithName("app.yaml"), refs)

				_, err := doc.Decode[any](t.Context())
				require.ErrorIs(t, err, niceyaml.ErrExcessiveAliasing)

				err = doc.Bind(niceyaml.NewError("bad", niceyaml.AtPath(path)))
				require.EqualError(t, err, tc.want)

				// A validator binds its error before any decode counts the
				// aliases, and it binds the same way.
				fresh := yamltest.FirstDocument(t, tc.input, niceyaml.WithName("app.yaml"), refs)

				err = fresh.Validate(t.Context(), niceyaml.ValidatorFunc(func(context.Context, *niceyaml.Node) error {
					return niceyaml.NewError("bad", niceyaml.AtPath(path))
				}))
				require.EqualError(t, err, tc.want)

				trusted := yamltest.FirstDocument(t, tc.input,
					niceyaml.WithName("app.yaml"), refs, niceyaml.WithAliasLimit(false))

				err = trusted.Bind(niceyaml.NewError("bad", niceyaml.AtPath(path)))
				require.EqualError(t, err, tc.want)
			})
		}
	})

	t.Run("goroutines bind beside a merge of a reference at once", func(t *testing.T) {
		t.Parallel()

		// The reference keeps the keys that the merge key brings in, so
		// bindings on many goroutines read and keep them at once.
		doc := yamltest.FirstDocument(t,
			stringtest.LinesLF("kind: a", "<<: *base"),
			niceyaml.WithName("app.yaml"),
			yamltest.References("base: &base {replicas: 1}\n"),
		)

		bad := errors.Join(
			yamltest.ErrorAt("bad", "kind"),
			yamltest.ErrorAt("bad", "replicas"),
			yamltest.ErrorAt("bad", "name"),
		)

		var wg sync.WaitGroup

		for range 8 {
			wg.Go(func() {
				assert.EqualError(t, doc.Bind(bad), stringtest.JoinLF(
					"app.yaml:1:1: $.name: bad",
					"app.yaml:1:7: $.kind: bad",
					"app.yaml:2:5: $.replicas: bad",
				))
			})
		}

		wg.Wait()
	})

	t.Run("documents bind beside merges of one reference anchor at once", func(t *testing.T) {
		t.Parallel()

		// The documents of a Source share the keys of a reference anchor
		// that their merge keys read. So the bindings of many documents, each
		// on a goroutine of its own, read and keep those keys at once.
		source := niceyaml.NewSourceFromString(
			strings.Repeat("---\nx: {kind: a, <<: *base}\n", 8),
			niceyaml.WithName("app.yaml"),
			yamltest.References("base: &base {replicas: 1}\n"),
		)

		bad := errors.Join(
			yamltest.ErrorAt("bad", "x", "kind"),
			yamltest.ErrorAt("bad", "x", "replicas"),
		)

		var wg sync.WaitGroup

		for i, doc := range yamltest.Documents(t, source, 8) {
			wg.Go(func() {
				row := 2*i + 2
				assert.EqualError(t, doc.Bind(bad), stringtest.JoinLF(
					fmt.Sprintf("app.yaml:%d:11: $.x.kind: bad", row),
					fmt.Sprintf("app.yaml:%d:18: $.x.replicas: bad", row),
				))
			})
		}

		wg.Wait()
	})

	t.Run("sources that share a reference read its alias keys apart", func(t *testing.T) {
		t.Parallel()

		// An alias key of a reference anchor reads the anchor that the
		// references of each Source pick. Two Sources that read one
		// reference can thus find different keys in one anchor. Each binds
		// the key that its own decode reads at the alias.
		shared := niceyaml.NewSourceFromString(stringtest.LinesLF("k: &k a", "u: &u {*k : 1}"))
		later := niceyaml.NewSourceFromString("k: &k b\n")

		tcs := map[string]struct {
			refs       []*niceyaml.Source
			key, other string
		}{
			"one reference":   {refs: []*niceyaml.Source{shared}, key: "a", other: "b"},
			"later reference": {refs: []*niceyaml.Source{shared, later}, key: "b", other: "a"},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				doc := yamltest.FirstDocument(t, "x: {<<: *u}\n",
					niceyaml.WithName("d.yaml"),
					niceyaml.WithReferences(tc.refs...),
				)

				got, err := doc.Decode[map[string]map[string]int](t.Context())
				require.NoError(t, err)
				assert.Equal(t, map[string]map[string]int{"x": {tc.key: 1}}, got)

				err = doc.Bind(yamltest.ErrorAt("bad", "x", tc.key))
				require.EqualError(t, err, "d.yaml:1:9: $.x."+tc.key+": bad")

				err = doc.Bind(yamltest.ErrorAt("bad", "x", tc.other))
				require.EqualError(t, err, "d.yaml:1:1: $.x."+tc.other+": bad")
			})
		}
	})

	t.Run("sources that share a reference find the last entry of a key beside its alias keys", func(t *testing.T) {
		t.Parallel()

		// The alias key *k names a for a Source that reads the shared
		// reference alone. It names b for a Source that also reads the later
		// one. The merge key between the two entries of a reads the anchor
		// m, which only the later reference defines. The first Source thus
		// cannot read that merge key, which might bring in any key. It binds
		// every key at the alias, as the second Source does.
		later := niceyaml.NewSourceFromString(stringtest.LinesLF("k: &k b", "m: &m {c: 3}"))

		tcs := map[string]struct {
			anchor string
		}{
			"alias key after a key of its name":  {anchor: "{a: 1, <<: *m, *k : 2}"},
			"alias key before a key of its name": {anchor: "{*k : 1, <<: *m, a: 2}"},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				shared := niceyaml.NewSourceFromString(stringtest.LinesLF("k: &k a", "u: &u "+tc.anchor))

				readers := []struct {
					refs []*niceyaml.Source
					want []string
				}{
					{
						refs: []*niceyaml.Source{shared},
						want: []string{"d.yaml:1:9: $.x.a: bad", "d.yaml:1:9: $.x.b: bad", "d.yaml:1:9: $.x.c: bad"},
					},
					{
						refs: []*niceyaml.Source{shared, later},
						want: []string{"d.yaml:1:9: $.x.a: bad", "d.yaml:1:9: $.x.b: bad", "d.yaml:1:9: $.x.c: bad"},
					},
				}

				for _, reader := range readers {
					doc := yamltest.FirstDocument(t, "x: {<<: *u}\n",
						niceyaml.WithName("d.yaml"),
						niceyaml.WithReferences(reader.refs...),
					)

					for i, key := range []string{"a", "b", "c"} {
						require.EqualError(t, doc.Bind(yamltest.ErrorAt("bad", "x", key)), reader.want[i])
					}
				}
			})
		}
	})

	t.Run("sources that share a reference bind beside merges of its anchor at once", func(t *testing.T) {
		t.Parallel()

		// Every Source that reads one reference shares the keys of its
		// anchors, other than the names of alias keys. So the bindings of
		// many Sources, each on a goroutine of its own, read and keep those
		// keys at once.
		shared := niceyaml.NewSourceFromString(stringtest.LinesLF("k: &k kind", "base: &base {replicas: 1, *k : a}"))

		bad := errors.Join(
			yamltest.ErrorAt("bad", "x", "kind"),
			yamltest.ErrorAt("bad", "x", "replicas"),
			yamltest.ErrorAt("bad", "x", "name"),
		)

		var wg sync.WaitGroup

		for range 8 {
			wg.Go(func() {
				doc := yamltest.FirstDocument(t, "x: {<<: *base}\n",
					niceyaml.WithName("app.yaml"),
					niceyaml.WithReferences(shared),
				)

				assert.EqualError(t, doc.Bind(bad), stringtest.JoinLF(
					"app.yaml:1:1: $.x.name: bad",
					"app.yaml:1:9: $.x.kind: bad",
					"app.yaml:1:9: $.x.replicas: bad",
				))
			})
		}

		wg.Wait()
	})

	t.Run("references read from the files of a directory", func(t *testing.T) {
		t.Parallel()

		// A program reads each file of a directory into a Source, in the
		// order it picks, and an anchor of a later file wins a name two
		// files define.
		fsys := yamltest.MapFS(map[string]string{
			"refs/a.yaml": stringtest.LinesLF(
				"base: &base",
				"  port: 80",
			),
			"refs/b.yaml": stringtest.LinesLF(
				"base: &base",
				"  port: 443",
				"name: &name api",
			),
			"refs/note.md": "no reference",
		})

		names, err := fs.Glob(fsys, "refs/*.yaml")
		require.NoError(t, err)

		refs := make([]*niceyaml.Source, 0, len(names))

		for _, name := range names {
			ref, err := niceyaml.NewSourceFromFS(fsys, name)
			require.NoError(t, err)

			refs = append(refs, ref)
		}

		doc := yamltest.FirstDocument(
			t,
			stringtest.LinesLF("name: *name", "server: *base"),
			niceyaml.WithReferences(refs...),
		)

		got, err := doc.Decode[config](t.Context(), niceyaml.WithValidator(serverSchema))
		require.NoError(t, err)
		assert.Equal(t, config{Name: "api", Server: server{Port: 443}}, got)
	})

	t.Run("reference that does not parse fails every decode", func(t *testing.T) {
		t.Parallel()

		ref := niceyaml.NewSourceFromString("x: [\n", niceyaml.WithName("ref.yaml"))
		doc := yamltest.FirstDocument(t, "a: 1\n", niceyaml.WithReferences(ref))

		// Every decode returns the error of the parse of the reference.
		_, want := ref.File()

		_, err := doc.Decode[map[string]int](t.Context())
		require.ErrorIs(t, err, niceyaml.ErrSyntax)
		assert.Equal(t, want, err)
		assert.Equal(t, "ref.yaml:1:4: "+unclosedSequence, err.Error())
	})

	t.Run("alias with no anchor in a reference fails the decodes that reach it", func(t *testing.T) {
		t.Parallel()

		ref := yamltest.References(stringtest.LinesLF(
			"good: &good {port: 80}",
			"bad: &bad {port: *missing}",
		))

		tcs := map[string]struct {
			input string
			want  map[string]any
			err   string
		}{
			"alias to the anchor that reads no alias": {
				input: "server: *good\n",
				want:  map[string]any{"server": map[string]any{"port": uint64(80)}},
			},
			"alias to the anchor that holds the alias": {
				input: "server: *bad\n",
				err:   `app.yaml:1:9: could not find alias "missing"`,
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				doc := yamltest.FirstDocument(t, tc.input, niceyaml.WithName("app.yaml"), ref)

				got, err := doc.Decode[map[string]any](t.Context())
				if tc.err != "" {
					require.EqualError(t, err, tc.err)
					require.ErrorIs(t, err, niceyaml.ErrDecode)

					return
				}

				require.NoError(t, err)
				assert.Equal(t, tc.want, got)
			})
		}
	})
}

// minPort is a server whose port must be at least 1, for the test of an
// error under an alias to a reference document.
type minPort struct {
	Port int `yaml:"port"`
}

// Validate implements [niceyaml.SelfValidator].
func (s minPort) Validate() error {
	if s.Port < 1 {
		return yamltest.ErrorAt("port must be at least 1", "port")
	}

	return nil
}

func ExampleWithUnmarshalers() {
	ctx := context.Background()

	type policy struct {
		Allow []net.IPNet `yaml:"allow"`
		Port  int         `yaml:"port"`
	}

	cidr := niceyaml.CustomUnmarshaler(func(_ context.Context, n *net.IPNet, decode func(any) error) error {
		var s string

		err := decode(&s)
		if err != nil {
			return err
		}

		_, parsed, err := net.ParseCIDR(s)
		if err != nil {
			return err //nolint:wrapcheck // The decode binds the error at the value.
		}

		*n = *parsed

		return nil
	})

	allowPath := paths.Current().Child("allow")

	// The validator passes no option to its decode, and the Source gives
	// that decode the function for a net.IPNet.
	private := niceyaml.ValidatorFunc(func(ctx context.Context, n *niceyaml.Node) error {
		nets, err := n.DecodeAt[[]net.IPNet](ctx, allowPath)
		if err != nil {
			return err
		}

		for i, allowed := range nets {
			if !allowed.IP.IsPrivate() {
				return niceyaml.NewError(allowed.String()+" is not private", niceyaml.AtPath(allowPath.Index(i)))
			}
		}

		return nil
	})

	inputs := []string{
		stringtest.LinesLF("port: 8080", "allow:", "  - 10.0.0.0/8"),
		stringtest.LinesLF("port: 8080", "allow:", "  - 10.0.0.0/8", "  - 8.8.8.0/24"),
	}

	for _, input := range inputs {
		source := niceyaml.NewSourceFromString(input,
			niceyaml.WithName("policy.yaml"), niceyaml.WithUnmarshalers(cidr))

		p, err := source.Decode[policy](ctx, niceyaml.WithValidator(private))
		if err != nil {
			fmt.Println(err)

			continue
		}

		fmt.Println(p.Port, p.Allow[0].String())
	}

	// Output:
	// 8080 10.0.0.0/8
	// policy.yaml:4:5: $.allow[1]: 8.8.8.0/24 is not private
}

func TestWithUnmarshalers(t *testing.T) {
	t.Parallel()

	levels := niceyaml.CustomUnmarshaler(decodeLevel)

	t.Run("every decode of the documents applies them", func(t *testing.T) {
		t.Parallel()

		type config struct {
			Level plainLevel `yaml:"level"`
		}

		input := stringtest.LinesLF(
			"level: high",
			"spans:",
			"  - {from: 1, to: 2, level: low}",
		)

		levelPath := paths.Current().Child("level")
		spansPath := paths.Current().Child("spans")

		// Each decode reads a level, which decodes from its name only
		// through the function of the Source.
		tcs := map[string]struct {
			decode func(ctx context.Context, source *niceyaml.Source, doc *niceyaml.Node) (any, error)
			want   any
		}{
			"Source.Decode": {
				decode: func(ctx context.Context, source *niceyaml.Source, _ *niceyaml.Node) (any, error) {
					return source.Decode[config](ctx)
				},
				want: config{Level: levelHigh},
			},
			"Source.DecodeInto": {
				decode: func(ctx context.Context, source *niceyaml.Source, _ *niceyaml.Node) (any, error) {
					var got config

					err := source.DecodeInto(ctx, &got)

					return got, err
				},
				want: config{Level: levelHigh},
			},
			"Node.Decode": {
				decode: func(ctx context.Context, _ *niceyaml.Source, doc *niceyaml.Node) (any, error) {
					return doc.Decode[config](ctx)
				},
				want: config{Level: levelHigh},
			},
			"Node.DecodeInto": {
				decode: func(ctx context.Context, _ *niceyaml.Source, doc *niceyaml.Node) (any, error) {
					var got config

					err := doc.DecodeInto(ctx, &got)

					return got, err //nolint:wrapcheck // The test reads the error as the decode returns it.
				},
				want: config{Level: levelHigh},
			},
			"Node.DecodeAt": {
				decode: func(ctx context.Context, _ *niceyaml.Source, doc *niceyaml.Node) (any, error) {
					return doc.DecodeAt[plainLevel](ctx, levelPath)
				},
				want: levelHigh,
			},
			"Node.DecodeIfPresent": {
				decode: func(ctx context.Context, _ *niceyaml.Source, doc *niceyaml.Node) (any, error) {
					var got plainLevel

					_, err := doc.DecodeIfPresent(ctx, levelPath, &got)

					return got, err //nolint:wrapcheck // The test reads the error as the decode returns it.
				},
				want: levelHigh,
			},
			"the decode of a validator": {
				decode: func(ctx context.Context, _ *niceyaml.Source, doc *niceyaml.Node) (any, error) {
					var got plainLevel

					read := niceyaml.ValidatorFunc(func(ctx context.Context, n *niceyaml.Node) error {
						var err error

						got, err = n.DecodeAt[plainLevel](ctx, levelPath)

						return err
					})

					_, err := doc.Decode[map[string]any](ctx, niceyaml.WithValidator(read))

					return got, err
				},
				want: levelHigh,
			},
			"the decode function of a custom unmarshaler": {
				decode: func(ctx context.Context, _ *niceyaml.Source, doc *niceyaml.Node) (any, error) {
					return doc.DecodeAt[[]plainSpan](
						ctx,
						spansPath,
						niceyaml.WithUnmarshaler(niceyaml.CustomUnmarshaler(decodeSpan)),
					)
				},
				want: []plainSpan{{From: 1, To: 2, Level: levelLow}},
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				source := niceyaml.NewSourceFromString(input, niceyaml.WithUnmarshalers(levels))

				got, err := tc.decode(t.Context(), source, yamltest.Document(t, source))
				require.NoError(t, err)
				assert.Equal(t, tc.want, got)

				// A Source without the option reads a level as an integer.
				plain := niceyaml.NewSourceFromString(input)

				_, err = tc.decode(t.Context(), plain, yamltest.Document(t, plain))
				require.ErrorIs(t, err, niceyaml.ErrWrongKind)
			})
		}
	})

	t.Run("self-validation spells each map key as the document does", func(t *testing.T) {
		t.Parallel()

		input := stringtest.Input(`
			by_grade:
			  high: {url: http://h}
		`)

		// A grade decodes from its name only through gradeNames, so the
		// key of the document matches the key of the map only when the
		// walk has it.
		tcs := map[string]struct {
			sourceOpts []niceyaml.SourceOption
			decodeOpts []niceyaml.DecodeOption
			err        string
		}{
			"the Source gives the function of the key": {
				sourceOpts: []niceyaml.SourceOption{niceyaml.WithUnmarshalers(gradeNames)},
				err:        `app.yaml:2:15: $.by_grade.high.url: url "ftp://h" is not http`,
			},
			"without it, the error binds at the key of the map": {
				decodeOpts: []niceyaml.DecodeOption{niceyaml.WithUnmarshaler(gradeNames)},
				err:        `app.yaml:1:1: $.by_grade.3.url: url "ftp://h" is not http`,
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				source := niceyaml.NewSourceFromString(input,
					append([]niceyaml.SourceOption{niceyaml.WithName("app.yaml")}, tc.sourceOpts...)...)

				var cfg gradedConfig

				err := source.DecodeInto(t.Context(), &cfg,
					append([]niceyaml.DecodeOption{niceyaml.WithSelfValidation(false)}, tc.decodeOpts...)...)
				require.NoError(t, err)

				cfg.ByGrade[gradeHigh] = upstream{URL: "ftp://h"}

				require.EqualError(t, source.SelfValidate(t.Context(), &cfg), tc.err)
				require.EqualError(t, yamltest.Document(t, source).SelfValidate(t.Context(), &cfg), tc.err)
			})
		}
	})

	t.Run("SelfValidateValue reads no Source", func(t *testing.T) {
		t.Parallel()

		source := niceyaml.NewSourceFromString("by_grade:\n  high: {url: http://h}\n",
			niceyaml.WithUnmarshalers(gradeNames))

		var cfg gradedConfig

		err := source.DecodeInto(t.Context(), &cfg, niceyaml.WithSelfValidation(false))
		require.NoError(t, err)

		cfg.ByGrade[gradeHigh] = upstream{URL: "ftp://h"}

		require.EqualError(t, source.SelfValidate(t.Context(), &cfg),
			`2:15: $.by_grade.high.url: url "ftp://h" is not http`)
		require.EqualError(t, niceyaml.SelfValidateValue(t.Context(), &cfg),
			`$.by_grade.3.url: url "ftp://h" is not http`)
	})

	t.Run("each constructor takes the option", func(t *testing.T) {
		t.Parallel()

		const input = "level: high\n"

		path := filepath.Join(t.TempDir(), "app.yaml")
		require.NoError(t, os.WriteFile(path, []byte(input), 0o600))

		fsys := fstest.MapFS{"app.yaml": &fstest.MapFile{Data: []byte(input)}}

		tcs := map[string]func(opts ...niceyaml.SourceOption) (*niceyaml.Source, error){
			"NewSourceFromFile": func(opts ...niceyaml.SourceOption) (*niceyaml.Source, error) {
				return niceyaml.NewSourceFromFile(path, opts...)
			},
			"NewSourceFromFS": func(opts ...niceyaml.SourceOption) (*niceyaml.Source, error) {
				return niceyaml.NewSourceFromFS(fsys, "app.yaml", opts...)
			},
			"NewSourceFromReader": func(opts ...niceyaml.SourceOption) (*niceyaml.Source, error) {
				return niceyaml.NewSourceFromReader(strings.NewReader(input), opts...)
			},
			"NewSourceFromBytes": func(opts ...niceyaml.SourceOption) (*niceyaml.Source, error) {
				return niceyaml.NewSourceFromBytes([]byte(input), opts...), nil
			},
			"NewSourceFromString": func(opts ...niceyaml.SourceOption) (*niceyaml.Source, error) {
				return niceyaml.NewSourceFromString(input, opts...), nil
			},
		}

		for name, build := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				source, err := build(niceyaml.WithUnmarshalers(levels))
				require.NoError(t, err)

				got, err := source.Decode[map[string]plainLevel](t.Context())
				require.NoError(t, err)
				assert.Equal(t, map[string]plainLevel{"level": levelHigh}, got)
			})
		}
	})

	t.Run("a call replaces or removes the function of a type", func(t *testing.T) {
		t.Parallel()

		seven := niceyaml.CustomUnmarshaler(func(_ context.Context, l *plainLevel, _ func(any) error) error {
			*l = 7

			return nil
		})

		removed := niceyaml.CustomUnmarshaler[plainLevel](nil)

		tcs := map[string]struct {
			input string
			err   string
			opts  []niceyaml.DecodeOption
			want  plainLevel
		}{
			"the function of the Source": {
				input: "level: high\n",
				want:  levelHigh,
			},
			"a function of the call replaces it": {
				input: "level: high\n",
				opts:  []niceyaml.DecodeOption{niceyaml.WithUnmarshaler(seven)},
				want:  7,
			},
			"a nil function removes it": {
				input: "level: 2\n",
				opts:  []niceyaml.DecodeOption{niceyaml.WithUnmarshaler(removed)},
				want:  levelHigh,
			},
			"a type with no function reads by its kind": {
				input: "level: high\n",
				opts:  []niceyaml.DecodeOption{niceyaml.WithUnmarshaler(removed)},
				err:   "1:8: $.level: expected integer, got string",
			},
			"a function after a nil one gives the type a function again": {
				input: "level: high\n",
				opts:  []niceyaml.DecodeOption{niceyaml.WithUnmarshaler(removed, seven)},
				want:  7,
			},
			"a function in a later option replaces one in an earlier option": {
				input: "level: high\n",
				opts: []niceyaml.DecodeOption{
					niceyaml.WithUnmarshaler(removed),
					niceyaml.WithUnmarshaler(seven),
				},
				want: 7,
			},
			"a bundle of decode options applies the unmarshalers it holds": {
				input: "level: high\n",
				opts: []niceyaml.DecodeOption{
					niceyaml.DecodeOptions(
						niceyaml.WithUnknownFields(niceyaml.UnknownFieldsReject),
						niceyaml.WithUnmarshaler(seven),
					),
				},
				want: 7,
			},
			"a nil function for another type keeps it": {
				input: "level: high\n",
				opts:  []niceyaml.DecodeOption{niceyaml.WithUnmarshaler(niceyaml.CustomUnmarshaler[net.IPNet](nil))},
				want:  levelHigh,
			},
			"the zero Unmarshaler keeps it": {
				input: "level: high\n",
				opts:  []niceyaml.DecodeOption{niceyaml.WithUnmarshaler(niceyaml.Unmarshaler{})},
				want:  levelHigh,
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				doc := yamltest.FirstDocument(t, tc.input, niceyaml.WithUnmarshalers(levels))

				got, err := doc.Decode[map[string]plainLevel](t.Context(), tc.opts...)
				if tc.err != "" {
					require.EqualError(t, err, tc.err)

					return
				}

				require.NoError(t, err)
				assert.Equal(t, map[string]plainLevel{"level": tc.want}, got)
			})
		}
	})

	t.Run("a call turns the JSON methods of the Source off", func(t *testing.T) {
		t.Parallel()

		type config struct {
			Size jsonSize `yaml:"size"`
		}

		doc := yamltest.FirstDocument(t, "size: 4k\n",
			niceyaml.WithUnmarshalers(niceyaml.JSONUnmarshalers(true)))

		got, err := doc.Decode[config](t.Context())
		require.NoError(t, err)
		assert.Equal(t, 4096, got.Size.Bytes)

		_, err = doc.Decode[config](t.Context(), niceyaml.WithUnmarshaler(niceyaml.JSONUnmarshalers(false)))
		require.EqualError(t, err, "1:7: $.size: expected mapping, got string")
	})

	t.Run("several unmarshalers add to each other in order", func(t *testing.T) {
		t.Parallel()

		type config struct {
			Size  jsonSize   `yaml:"size"`
			Level plainLevel `yaml:"level"`
		}

		seven := niceyaml.CustomUnmarshaler(func(_ context.Context, l *plainLevel, _ func(any) error) error {
			*l = 7

			return nil
		})

		jsonOn := niceyaml.JSONUnmarshalers(true)

		tcs := map[string]struct {
			input string
			err   string
			opts  []niceyaml.SourceOption
			want  config
		}{
			"each option gives what it sets": {
				input: "level: high\nsize: 4k\n",
				opts: []niceyaml.SourceOption{
					niceyaml.WithUnmarshalers(levels),
					niceyaml.WithUnmarshalers(jsonOn),
				},
				want: config{Level: levelHigh, Size: jsonSize{Bytes: 4096}},
			},
			"one option gives each unmarshaler it holds": {
				input: "level: high\nsize: 4k\n",
				opts:  []niceyaml.SourceOption{niceyaml.WithUnmarshalers(levels, jsonOn)},
				want:  config{Level: levelHigh, Size: jsonSize{Bytes: 4096}},
			},
			"a later function for a type replaces an earlier one": {
				input: "level: high\n",
				opts: []niceyaml.SourceOption{
					niceyaml.WithUnmarshalers(levels),
					niceyaml.WithUnmarshalers(seven),
				},
				want: config{Level: 7},
			},
			"a later function in one option replaces an earlier one": {
				input: "level: high\n",
				opts:  []niceyaml.SourceOption{niceyaml.WithUnmarshalers(levels, seven)},
				want:  config{Level: 7},
			},
			"a later nil function removes an earlier one": {
				input: "level: 2\n",
				opts: []niceyaml.SourceOption{
					niceyaml.WithUnmarshalers(levels),
					niceyaml.WithUnmarshalers(niceyaml.CustomUnmarshaler[plainLevel](nil)),
				},
				want: config{Level: levelHigh},
			},
			"the last JSON setting applies": {
				input: "size: 4k\n",
				opts: []niceyaml.SourceOption{
					niceyaml.WithUnmarshalers(jsonOn),
					niceyaml.WithUnmarshalers(niceyaml.JSONUnmarshalers(false)),
				},
				err: "1:7: $.size: expected mapping, got string",
			},
			"a later option without a JSON setting keeps the earlier one": {
				input: "size: 4k\n",
				opts: []niceyaml.SourceOption{
					niceyaml.WithUnmarshalers(jsonOn),
					niceyaml.WithUnmarshalers(levels),
				},
				want: config{Size: jsonSize{Bytes: 4096}},
			},
			"the zero Unmarshaler changes nothing": {
				input: "level: high\nsize: 4k\n",
				opts: []niceyaml.SourceOption{
					niceyaml.WithUnmarshalers(levels, jsonOn),
					niceyaml.WithUnmarshalers(niceyaml.Unmarshaler{}),
				},
				want: config{Level: levelHigh, Size: jsonSize{Bytes: 4096}},
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				got, err := yamltest.FirstDocument(t, tc.input, tc.opts...).Decode[config](t.Context())
				if tc.err != "" {
					require.EqualError(t, err, tc.err)

					return
				}

				require.NoError(t, err)
				assert.Equal(t, tc.want, got)
			})
		}
	})

	t.Run("keeps its unmarshalers when the caller changes the slice", func(t *testing.T) {
		t.Parallel()

		us := []niceyaml.Unmarshaler{levels}
		opt := niceyaml.WithUnmarshalers(us...)
		us[0] = niceyaml.CustomUnmarshaler[plainLevel](nil)

		got, err := yamltest.FirstDocument(t, "level: high\n", opt).Decode[map[string]plainLevel](t.Context())
		require.NoError(t, err)
		assert.Equal(t, map[string]plainLevel{"level": levelHigh}, got)
	})
}

func TestWithExcerpts(t *testing.T) {
	t.Parallel()

	// The anchor of the reference document holds a secret, and its
	// Source has excerpts off.
	refs := niceyaml.NewSourceFromString("port: &port hunter2\n", niceyaml.WithExcerpts(false))

	// The excerpt of an error at a port of 0.
	zero := stringtest.JoinLF(
		"   1 | port: 0",
		"     |       ^",
	)

	tcs := map[string]struct {
		input string
		// The excerpt of an error at the port, which is empty when the
		// Source shows none.
		want string
		opts []niceyaml.SourceOption
	}{
		"no option": {
			input: "port: 0\n",
			want:  zero,
		},
		"on": {
			input: "port: 0\n",
			opts:  []niceyaml.SourceOption{niceyaml.WithExcerpts(true)},
			want:  zero,
		},
		"off": {
			input: "port: 0\n",
			opts:  []niceyaml.SourceOption{niceyaml.WithExcerpts(false)},
		},
		"the last option wins": {
			input: "port: 0\n",
			opts:  []niceyaml.SourceOption{niceyaml.WithExcerpts(false), niceyaml.WithExcerpts(true)},
			want:  zero,
		},
		// The lines of the Source hold the alias and none of the text of
		// the reference document, so its own option decides.
		"a reference document with excerpts off": {
			input: "port: *port\n",
			opts:  []niceyaml.SourceOption{niceyaml.WithReferences(refs)},
			want: stringtest.JoinLF(
				"   1 | port: *port",
				"     |       ^",
			),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			source := niceyaml.NewSourceFromString(tc.input, tc.opts...)
			assert.Equal(t, tc.want != "", source.ShowsExcerpts())

			err := yamltest.Bind(t, source, niceyaml.NewError("bad port", niceyaml.AtPath(paths.Doc().Child("port"))))

			bound := yamltest.RequireSourceError(t, err)

			excerpt, ok := bound.Excerpt(niceyaml.WithContextLines(0))
			require.Equal(t, tc.want != "", ok)

			got := ""
			if ok {
				got = excerpt.String()
			}

			assert.Equal(t, tc.want, got)
		})
	}
}

func TestWithPositions(t *testing.T) {
	t.Parallel()

	const input = "server:\n  port: http\n"

	port := paths.Doc().Child("server", "port")

	tcs := map[string]struct {
		// The error the case binds in the Source.
		err error
		// FormatError of the bound error.
		want string
		opts []niceyaml.SourceOption
	}{
		"no option": {
			err: niceyaml.NewError("bad port", niceyaml.AtPath(port)),
			want: stringtest.JoinLF(
				"env:2:9: $.server.port: bad port",
				"",
				"   1 | server:",
				"   2 |   port: http",
				"     |         ^^^^",
			),
		},
		"on": {
			err:  niceyaml.NewError("bad port", niceyaml.AtPath(port)),
			opts: []niceyaml.SourceOption{niceyaml.WithPositions(true), niceyaml.WithExcerpts(false)},
			want: "env:2:9: $.server.port: bad port",
		},
		"off": {
			err:  niceyaml.NewError("bad port", niceyaml.AtPath(port)),
			opts: []niceyaml.SourceOption{niceyaml.WithPositions(false)},
			want: "env: $.server.port: bad port",
		},
		"off with a position alone": {
			err:  niceyaml.NewError("bad port", niceyaml.AtPosition(position.New(1, 8))),
			opts: []niceyaml.SourceOption{niceyaml.WithPositions(false)},
			want: "env: $.server.port: bad port",
		},
		"off with a range alone": {
			err: niceyaml.NewError(
				"bad port",
				niceyaml.AtRange(position.NewRange(position.New(1, 8), position.New(1, 12))),
			),
			opts: []niceyaml.SourceOption{niceyaml.WithPositions(false)},
			want: "env: $.server.port: bad port",
		},
		"off with a position at a key": {
			err:  niceyaml.NewError("bad key", niceyaml.AtPosition(position.New(1, 2))),
			opts: []niceyaml.SourceOption{niceyaml.WithPositions(false)},
			want: "env: $.server.port~: bad key",
		},
		"off at a key the mapping lacks": {
			err:  niceyaml.NewError("host is required", niceyaml.AtPath(paths.Doc().Child("server", "host"))),
			opts: []niceyaml.SourceOption{niceyaml.WithPositions(false)},
			want: "env: $.server.host: host is required",
		},
		"the last option wins": {
			err:  niceyaml.NewError("bad port", niceyaml.AtPath(port)),
			opts: []niceyaml.SourceOption{niceyaml.WithPositions(true), niceyaml.WithPositions(false)},
			want: "env: $.server.port: bad port",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			source := niceyaml.NewSourceFromString(
				input,
				append([]niceyaml.SourceOption{niceyaml.WithName("env")}, tc.opts...)...,
			)

			err := yamltest.Bind(t, source, tc.err)
			assert.Equal(t, tc.want, niceyaml.FormatError(err))

			bound := yamltest.RequireSourceError(t, err)
			assert.Same(t, source, bound.Source())
			require.NoError(t, bound.Unresolved())

			_, located := bound.Position()
			assert.Equal(t, source.ReportsPositions(), located)

			_, ranged := bound.Range()
			assert.Equal(t, located, ranged)

			_, excerpted := bound.Excerpt()
			assert.Equal(t, source.ShowsExcerpts(), excerpted)
		})
	}

	t.Run("positions off turn excerpts off", func(t *testing.T) {
		t.Parallel()

		source := niceyaml.NewSourceFromString(input, niceyaml.WithPositions(false))
		assert.False(t, source.ReportsPositions())
		assert.False(t, source.ShowsExcerpts())

		source = niceyaml.NewSourceFromString(input)
		assert.True(t, source.ReportsPositions())
		assert.True(t, source.ShowsExcerpts())
	})

	t.Run("a bound error keeps its node and path", func(t *testing.T) {
		t.Parallel()

		source := niceyaml.NewSourceFromString(input, niceyaml.WithName("env"), niceyaml.WithPositions(false))
		host := paths.Doc().Child("server", "host")

		err := yamltest.Bind(t, source, niceyaml.NewError("host is required", niceyaml.AtPath(host)))
		bound := yamltest.RequireSourceError(t, err)

		node, ok := bound.Node()
		require.True(t, ok)
		assert.Same(t, source, node.Source())

		path, ok := bound.Path()
		require.True(t, ok)
		assert.Equal(t, host, path)

		_, near := bound.Nearest()
		assert.False(t, near)

		// No line of the view gets a mark, because the error lacks a
		// position.
		assert.False(t, niceyaml.Annotate(err, source.View()))

		// No report lists the error as one without an excerpt.
		rep := niceyaml.NewErrorReport(err)
		assert.Empty(t, rep.Excerpts)
		assert.Empty(t, rep.Unresolved)
		assert.Equal(t, "env: $.server.host: host is required", niceyaml.FormatError(niceyaml.Detach(err)))
	})

	t.Run("a position alone names the value there", func(t *testing.T) {
		t.Parallel()

		source := niceyaml.NewSourceFromString("# env\n"+input, niceyaml.WithName("env"), niceyaml.WithPositions(false))

		err := yamltest.Bind(t, source, niceyaml.NewError("bad port", niceyaml.AtPosition(position.New(2, 8))))
		bound := yamltest.RequireSourceError(t, err)

		path, ok := bound.Path()
		require.True(t, ok)
		assert.Equal(t, port, path)

		// A comment holds no value, so the error keeps only the name.
		err = yamltest.Bind(t, source, niceyaml.NewError("bad comment", niceyaml.AtPosition(position.New(0, 2))))
		assert.Equal(t, "env: bad comment", niceyaml.FormatError(err))

		_, ok = yamltest.RequireSourceError(t, err).Path()
		assert.False(t, ok)

		// A path of the error itself wins over the value at its position.
		err = yamltest.Bind(t, source, niceyaml.NewError("bad server",
			niceyaml.AtPath(paths.Doc().Child("server")), niceyaml.AtPosition(position.New(2, 8))))
		assert.Equal(t, "env: $.server: bad server", niceyaml.FormatError(err))
	})

	t.Run("a located error gives way to the binding", func(t *testing.T) {
		t.Parallel()

		source := niceyaml.NewSourceFromString(input, niceyaml.WithName("env"), niceyaml.WithPositions(false))
		inner := yamltest.Bind(t, source, niceyaml.NewError("bad port", niceyaml.AtPath(port)))

		err := yamltest.Bind(
			t,
			source,
			niceyaml.Locate(fmt.Errorf("read: %w", inner), niceyaml.AtPath(paths.Doc().Child("server"))),
		)
		assert.Equal(t, "env: $.server.port: read: bad port", niceyaml.FormatError(err))
	})

	t.Run("a problem without a location binds at its heading", func(t *testing.T) {
		t.Parallel()

		source := niceyaml.NewSourceFromString(input, niceyaml.WithName("env"), niceyaml.WithPositions(false))

		err := yamltest.Bind(t, source, niceyaml.NewError("bad server",
			niceyaml.AtPath(paths.Doc().Child("server")),
			niceyaml.WithDetails(errors.New("port is not a number")),
		))
		assert.Equal(t, stringtest.JoinLF(
			"env: $.server: bad server",
			"`-- port is not a number",
		), niceyaml.FormatError(err))

		for bound := range niceyaml.Bindings(err) {
			_, located := bound.Position()
			assert.False(t, located)
			require.NoError(t, bound.Unresolved())
		}
	})

	t.Run("a document of several names its index", func(t *testing.T) {
		t.Parallel()

		source := niceyaml.NewSourceFromString("a: 1\n---\nport: http\n",
			niceyaml.WithName("env"), niceyaml.WithPositions(false))

		err := source.AllDocuments()[1].Bind(niceyaml.NewError("bad port", niceyaml.AtPath(paths.Doc().Child("port"))))
		assert.Equal(t, "env: document 2: $.port: bad port", niceyaml.FormatError(err))

		// A problem about the whole document names the document without
		// its first line.
		whole := source.AllDocuments()[1].Bind(errors.New("no matching schema"))
		assert.Equal(t, "env: document 2: no matching schema", whole.Error())
		assert.Equal(t, "env: document 2: no matching schema", niceyaml.FormatError(whole))

		// The lines of the source name nothing the user can find, so the
		// binding reports no lines of its document, detached or not.
		for _, e := range []error{err, niceyaml.Detach(err)} {
			bound := yamltest.RequireSourceError(t, e)

			_, ok := bound.DocumentSpan()
			assert.False(t, ok)

			index, ok := bound.DocumentIndex()
			require.True(t, ok)
			assert.Equal(t, 1, index)
		}
	})

	t.Run("a syntax error names the document of its line", func(t *testing.T) {
		t.Parallel()

		// The anchor without a value joins documents 2 and 3 in one parse,
		// so they share one error, which the line of document 3 holds.
		source := niceyaml.NewSourceFromString("k: 1\n---\na: &x\n---\nb: [1\n---\nc: 2\n",
			niceyaml.WithName("env"), niceyaml.WithPositions(false))

		_, err := source.File()
		require.Error(t, err)
		assert.Equal(t, "env: document 3: sequence end token ']' not found", niceyaml.FormatError(err))

		var bindings int

		for bound := range niceyaml.Bindings(err) {
			bindings++

			doc, ok := bound.Document()
			require.True(t, ok)
			assert.Equal(t, 2, doc.DocumentIndex())

			_, located := bound.Position()
			assert.False(t, located)
			require.NoError(t, bound.Unresolved())
		}

		assert.Equal(t, 1, bindings)
	})
}

func TestWithAliasLimit(t *testing.T) {
	t.Parallel()

	// Each item of the bomb lists ten aliases to the item above it, so the
	// last one expands to 10^5 scalars. The first alias of the last item
	// repeats the most, so every refusal binds there.
	bomb := yamltest.AliasLevels(5)
	last := paths.Current().Child("a").Index(5)

	// The schema accepts the root and the last item, and it decodes the
	// node it checks, which a schema that accepts everything skips.
	shape := schema.MustCompile([]byte(`{"type": ["object", "array"]}`))

	// The registry routes a document on the content of the last item, so
	// its matcher reads a node that holds an alias. No document matches,
	// and the registry then passes it.
	routed := schema.NewRegistry(
		schema.WithResolvers(schema.When(matcher.Content(paths.Doc().Child("a").Index(5), "x"), shape.Ref())),
		schema.WithRequireSchema(false),
	)

	// A reader reads a node and returns the error of the read.
	type reader = func(ctx context.Context, n *niceyaml.Node) error

	decode := func(opts ...niceyaml.DecodeOption) reader {
		return func(ctx context.Context, n *niceyaml.Node) error {
			_, err := n.Decode[any](ctx, opts...)

			return err
		}
	}

	validate := func(v niceyaml.Validator) reader {
		return func(ctx context.Context, n *niceyaml.Node) error { return n.Validate(ctx, v) }
	}

	tcs := map[string]struct {
		read reader
		path paths.Path
		// The message of the refusal with the limit on.
		want string
		// The refusal comes from a resolver of a registry.
		resolve bool
	}{
		"decode": {
			read: decode(),
			want: "bomb.yaml:7:10: aliases expand the document past the alias limit",
		},
		"decode with a schema validator": {
			read: decode(niceyaml.WithValidator(shape)),
			want: "bomb.yaml:7:10: aliases expand the document past the alias limit",
		},
		"schema validation": {
			read: validate(shape),
			want: "bomb.yaml:7:10: aliases expand the document past the alias limit",
		},
		"validation of every document": {
			read: func(ctx context.Context, n *niceyaml.Node) error {
				return n.Source().ValidateDocuments(ctx, shape)
			},
			want: "bomb.yaml:7:10: aliases expand the document past the alias limit",
		},
		"registry with a content matcher": {
			read:    validate(routed),
			want:    "resolve schema: bomb.yaml:7:10: aliases expand the document past the alias limit",
			resolve: true,
		},
		"decode with a registry with a content matcher": {
			read:    decode(niceyaml.WithValidator(routed)),
			want:    "resolve schema: bomb.yaml:7:10: aliases expand the document past the alias limit",
			resolve: true,
		},
		// The decode binds its refusal at the position of the alias that
		// repeats the most, so a scoped node names no path in front of it.
		"decode of a scoped node": {
			path: last,
			read: decode(),
			want: "bomb.yaml:7:10: aliases expand the document past the alias limit",
		},
		"schema validation of a scoped node": {
			path: last,
			read: validate(shape),
			want: "bomb.yaml:7:10: aliases expand the document past the alias limit",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			// The node of the case, in a Source of the bomb with opts.
			node := func(opts ...niceyaml.SourceOption) *niceyaml.Node {
				doc := yamltest.FirstDocument(t, bomb, append(opts, niceyaml.WithName("bomb.yaml"))...)

				return yamltest.AtOrSelf(t, doc, tc.path)
			}

			limited := map[string]*niceyaml.Node{
				"default":  node(),
				"limit on": node(niceyaml.WithAliasLimit(true)),
			}

			for via, n := range limited {
				// Every reader reports the aliases of the document as a
				// fault of the document.
				err := tc.read(t.Context(), n)
				require.EqualError(t, err, tc.want, via)
				require.ErrorIs(t, err, niceyaml.ErrExcessiveAliasing, via)
				require.NotErrorIs(t, err, schema.ErrValidate, via)
				require.NotErrorIs(t, err, niceyaml.ErrDecode, via)
				assert.Equal(t, tc.resolve, errors.Is(err, schema.ErrResolve), via)
				assert.True(t, niceyaml.IsInvalid(err), via)
			}

			// With the limit off, the same reader reads the document.
			require.NoError(t, tc.read(t.Context(), node(niceyaml.WithAliasLimit(false))))
		})
	}

	t.Run("every document of the source", func(t *testing.T) {
		t.Parallel()

		source := niceyaml.NewSourceFromString(bomb+"---\n"+bomb, niceyaml.WithAliasLimit(false))

		for _, doc := range yamltest.Documents(t, source, 2) {
			require.NoError(t, decode(niceyaml.WithValidator(shape))(t.Context(), doc))
		}
	})

	t.Run("limits that stay on", func(t *testing.T) {
		t.Parallel()

		trusted := yamltest.FirstDocument(t, bomb, niceyaml.WithName("bomb.yaml"), niceyaml.WithAliasLimit(false))

		data, err := trusted.Decode[any](t.Context(), niceyaml.WithValidator(shape))
		require.NoError(t, err)

		// ValidateValue takes a Go value and no source, so it applies the
		// limit to the value the source decoded.
		err = shape.ValidateValue(t.Context(), data)
		require.EqualError(t, err, "excessive aliasing")
		require.ErrorIs(t, err, niceyaml.ErrExcessiveAliasing)
		require.NotErrorIs(t, err, schema.ErrValidate)
		assert.True(t, niceyaml.IsInvalid(err))

		// Each [*] lists ten aliases to the item above, so the path would
		// select 10^5 nodes. The path is at fault for that, and the
		// document is not.
		_, err = trusted.Nodes(paths.MustParse("$.a[5][*][*][*][*][*]"))
		require.EqualError(t, err, "bomb.yaml: resolve $.a[5][*][*][*][*][*]: excessive aliasing")
		require.ErrorIs(t, err, niceyaml.ErrExcessiveAliasing)
		assert.False(t, niceyaml.IsInvalid(err))
	})
}

func TestWithAliasLimit_References(t *testing.T) {
	t.Parallel()

	// The reference document anchors a sequence of 1000 scalars. The
	// upload lists 500 aliases to it, so a decode of the upload reads
	// half a million nodes through them.
	defaults := "d: &d " + yamltest.FlowSequence("x", 1000) + "\n"
	upload := "v: " + yamltest.FlowSequence("*d", 500) + "\n"

	const want = "upload.yaml:1:5: aliases expand the document past the alias limit"

	type config struct {
		V [][]string `yaml:"v"`
	}

	shape := schema.MustCompile([]byte(`{"type": "object"}`))

	// Each reader takes the test that runs it, so a helper that fails
	// inside a reader fails that test.
	tcs := map[string]func(t *testing.T, n *niceyaml.Node) error{
		"typed decode": func(t *testing.T, n *niceyaml.Node) error {
			t.Helper()

			_, err := n.Decode[config](t.Context())

			return err
		},
		"decode into any": func(t *testing.T, n *niceyaml.Node) error {
			t.Helper()

			_, err := n.Decode[any](t.Context())

			return err
		},
		"decode with a schema validator": func(t *testing.T, n *niceyaml.Node) error {
			t.Helper()

			_, err := n.Decode[config](t.Context(), niceyaml.WithValidator(shape))

			return err
		},
		"schema validation": func(t *testing.T, n *niceyaml.Node) error {
			t.Helper()

			return n.Validate(t.Context(), shape)
		},
		"decode of a scoped node": func(t *testing.T, n *niceyaml.Node) error {
			t.Helper()

			_, err := yamltest.At(t, n, paths.Current().Child("v")).Decode[[][]string](t.Context())

			return err
		},
		// An encode decodes the document, so it gets the error of that
		// decode.
		"encode of a node": func(t *testing.T, n *niceyaml.Node) error {
			t.Helper()

			_, err := n.MarshalYAML(t.Context())

			return err //nolint:wrapcheck // The test inspects the error as it is.
		},
		"encode of a source": func(t *testing.T, n *niceyaml.Node) error {
			t.Helper()

			_, err := n.Source().MarshalYAML(t.Context())

			return err
		},
	}

	for name, read := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			// The node of the upload, in a Source with opts that reads a
			// reference Source with refOpts.
			node := func(refOpts []niceyaml.SourceOption, opts ...niceyaml.SourceOption) *niceyaml.Node {
				shared := niceyaml.NewSourceFromString(defaults, refOpts...)
				opts = append(opts, niceyaml.WithName("upload.yaml"), niceyaml.WithReferences(shared))

				return yamltest.FirstDocument(t, upload, opts...)
			}

			// The option of the reference Source does not reach the
			// upload.
			limited := map[string]*niceyaml.Node{
				"default":             node(nil),
				"limit on":            node(nil, niceyaml.WithAliasLimit(true)),
				"reference limit off": node([]niceyaml.SourceOption{niceyaml.WithAliasLimit(false)}),
			}

			for via, n := range limited {
				err := read(t, n)
				require.EqualError(t, err, want, via)
				require.ErrorIs(t, err, niceyaml.ErrExcessiveAliasing, via)
				require.NotErrorIs(t, err, niceyaml.ErrLimit, via)
				require.NotErrorIs(t, err, niceyaml.ErrDecode, via)
				assert.True(t, niceyaml.IsInvalid(err), via)
			}

			// With the limit off, the same reader reads the upload.
			require.NoError(t, read(t, node(nil, niceyaml.WithAliasLimit(false))))
		})
	}

	t.Run("the same anchor inside the document", func(t *testing.T) {
		t.Parallel()

		// A document that defines the anchor gets the same verdict, one
		// line further down.
		doc := yamltest.FirstDocument(t, defaults+upload, niceyaml.WithName("upload.yaml"))

		_, err := doc.Decode[any](t.Context())
		require.EqualError(t, err, "upload.yaml:2:5: aliases expand the document past the alias limit")
	})

	t.Run("shared defaults", func(t *testing.T) {
		t.Parallel()

		type service struct {
			Host string `yaml:"host"`
			Port int    `yaml:"port"`
		}

		shared := niceyaml.NewSourceFromString("d: &d {host: localhost, port: 80}\n")
		doc := yamltest.FirstDocument(t,
			stringtest.LinesLF("server: {<<: *d}", "worker: {<<: *d, port: 81}"),
			niceyaml.WithReferences(shared),
		)

		got, err := doc.Decode[map[string]service](t.Context())
		require.NoError(t, err)
		assert.Equal(t, map[string]service{
			"server": {Host: "localhost", Port: 80},
			"worker": {Host: "localhost", Port: 81},
		}, got)
	})

	t.Run("one alias to a large anchor", func(t *testing.T) {
		t.Parallel()

		doc := yamltest.FirstDocument(t, "v: [*d]\n", yamltest.References(defaults))

		got, err := doc.Decode[config](t.Context())
		require.NoError(t, err)
		require.Len(t, got.V, 1)
		assert.Len(t, got.V[0], 1000)
	})

	t.Run("text of a typed decode", func(t *testing.T) {
		t.Parallel()

		// The decoder writes the node of a value that reads text out as
		// text, so the aliases must fit the document byte limit of the upload.
		type texts struct {
			V []aliasText `yaml:"v"`
		}

		shared := niceyaml.NewSourceFromString("s: &s " + strings.Repeat("x", 1000) + "\n")
		doc := yamltest.FirstDocument(t, "v: "+yamltest.FlowSequence("*s", 20)+"\n",
			niceyaml.WithName("upload.yaml"),
			niceyaml.WithDocumentByteLimit(4096),
			niceyaml.WithReferences(shared),
		)

		_, err := doc.Decode[texts](t.Context())
		require.EqualError(t, err, "upload.yaml:1:21: aliases repeat more than 4 KiB of text")
		require.ErrorIs(t, err, niceyaml.ErrLimit)
		require.ErrorIs(t, err, niceyaml.ErrExcessiveAliasing)
		assert.True(t, niceyaml.IsInvalid(err))
	})
}

func TestWithAliasLimit_ReferenceLoop(t *testing.T) {
	t.Parallel()

	const want = "upload.yaml:1:4: aliases expand the document past the alias limit"

	// A decode into a recursive type follows a loop of aliases for as
	// many levels as the loop gives it.
	type tree map[string]tree

	readers := map[string]func(t *testing.T, n *niceyaml.Node) error{
		"decode into a recursive type": func(t *testing.T, n *niceyaml.Node) error {
			t.Helper()

			_, err := n.Decode[tree](t.Context())

			return err
		},
		"decode into a type that reads text": func(t *testing.T, n *niceyaml.Node) error {
			t.Helper()

			_, err := n.Decode[map[string]aliasText](t.Context())

			return err
		},
		"decode into any": func(t *testing.T, n *niceyaml.Node) error {
			t.Helper()

			_, err := n.Decode[any](t.Context())

			return err
		},
		"layer merge": func(t *testing.T, n *niceyaml.Node) error {
			t.Helper()

			base := yamltest.FirstDocument(t, "base: 1\n")
			_, err := niceyaml.NewSourceFromLayers(t.Context(), []*niceyaml.Node{base, n})

			return err
		},
	}

	// Each reference document holds an alias inside an anchor of its own
	// name, and the alias of the upload reaches that anchor.
	tcs := map[string]struct {
		refs  string
		input string
	}{
		"anchor around aliases of its name": {
			refs:  "m: &m {k: *m, j: *m}\n",
			input: "v: *m\n",
		},
		"anchor on an alias of its name": {
			refs:  "e: &e\n  *e\n",
			input: "v: *e\n",
		},
		"anchor inside another around aliases to the outer": {
			refs:  "a: &a {b: &b {k: *a, j: *a}}\n",
			input: "v: *b\n",
		},
		"anchor inside another before the last of its name": {
			// A read of the anchor o follows each alias inside it to the
			// last anchor a.
			refs:  "o: &o {k: &a [*a, *a]}\nb: &a [x, y]\n",
			input: "v: *o\n",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			for via, read := range readers {
				doc := yamltest.FirstDocument(t, tc.input,
					niceyaml.WithName("upload.yaml"), yamltest.References(tc.refs))

				err := read(t, doc)
				require.EqualError(t, err, want, via)
				require.ErrorIs(t, err, niceyaml.ErrExcessiveAliasing, via)
				require.NotErrorIs(t, err, niceyaml.ErrDecode, via)
				assert.True(t, niceyaml.IsInvalid(err), via)
			}
		})
	}

	const loop = "m: &m {k: *m, j: *m}\n"

	t.Run("anchor beside the loop", func(t *testing.T) {
		t.Parallel()

		// The aliases of the document do not reach the anchor m.
		doc := yamltest.FirstDocument(t, "v: *d\n", yamltest.References(loop+"d: &d {k: {}}\n"))

		got, err := doc.Decode[tree](t.Context())
		require.NoError(t, err)
		assert.Equal(t, tree{"v": {"k": {}}}, got)
	})

	t.Run("the same anchor inside the document", func(t *testing.T) {
		t.Parallel()

		// One file reads an alias inside the anchor of its name as null,
		// so its decode ends.
		doc := yamltest.FirstDocument(t, loop+"v: *m\n")

		got, err := doc.Decode[tree](t.Context())
		require.NoError(t, err)
		assert.Equal(t, tree{
			"m": {"k": nil, "j": nil},
			"v": {"k": nil, "j": nil},
		}, got)
	})
}

func TestSource_Documents(t *testing.T) {
	t.Parallel()

	source := niceyaml.NewSourceFromString(stringtest.Input(`
		---
		a: 1
		---
		b: 2
	`), niceyaml.WithFilePath("two.yaml"))

	// The file holds no empty document, so both methods return its two
	// documents.
	tcs := map[string]struct {
		docs func() []*niceyaml.Node
	}{
		"Documents":    {docs: source.Documents},
		"AllDocuments": {docs: source.AllDocuments},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			docs := tc.docs()
			require.Len(t, docs, 2)

			second := docs[1]
			assert.Equal(t, 1, second.DocumentIndex())
			assert.Equal(t, "two.yaml", second.FilePath())
			assert.Same(t, source, second.Source())
			assert.NotNil(t, second.Tokens())

			// Every call hands out the same root Node for an index, in a
			// slice of its own.
			again := tc.docs()
			assert.Same(t, second, again[1])

			again[1] = nil

			assert.Same(t, second, tc.docs()[1])
		})
	}
}

func TestSource_Document(t *testing.T) {
	t.Parallel()

	t.Run("rejects several documents", func(t *testing.T) {
		t.Parallel()

		tcs := map[string]struct {
			input string
			want  string
		}{
			"at the second header": {
				input: stringtest.JoinLF("a: 1", "---", "b: 2"),
				want:  "2:1: multiple documents in source: 2 documents",
			},
			// A "..." marker ends the first document, so the second has no
			// header, and the error points at its first token instead.
			"at a second document without a header": {
				input: stringtest.JoinLF("a: 1", "...", "b: 2"),
				want:  "3:1: multiple documents in source: 2 documents",
			},
			// The empty document between the two does not count, and the
			// error points at the header of the second document with content.
			"past an empty one between them": {
				input: stringtest.JoinLF("a: 1", "---", "---", "b: 2"),
				want:  "3:1: multiple documents in source: 2 documents",
			},
			// The comments after a "..." marker fold into the document below
			// them as its preamble, and the error points past them at the
			// first token of its content.
			"past the comments above the second": {
				input: stringtest.JoinLF("a: 1", "...", "# tail", "b: 2"),
				want:  "4:1: multiple documents in source: 2 documents",
			},
			"below a comment block": {
				input: stringtest.JoinLF(
					"# Copyright notice.",
					"---",
					"a: 1",
					"---",
					"b: 2",
				),
				want: "4:1: multiple documents in source: 2 documents",
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				source := niceyaml.NewSourceFromString(tc.input)

				_, err := source.Document()
				require.ErrorIs(t, err, niceyaml.ErrMultipleDocuments)
				require.EqualError(t, err, tc.want)
				assert.Same(t, source, yamltest.RequireSourceError(t, err).Source())
			})
		}
	})

	t.Run("returns the one document Documents lists", func(t *testing.T) {
		t.Parallel()

		tcs := map[string]struct {
			input string
			// The index in the file of the document Document returns, and
			// the value it decodes to.
			index int
			want  map[string]int
		}{
			"single document": {
				input: "a: 1",
				want:  map[string]int{"a": 1},
			},
			"trailing header": {
				input: stringtest.LinesLF("b: 2", "---"),
				want:  map[string]int{"b": 2},
			},
			"comment below a trailing header": {
				input: stringtest.LinesLF("b: 2", "---", "# Source: t.yaml"),
				want:  map[string]int{"b": 2},
			},
			// The first header holds an empty document, so the second
			// header starts another one.
			"header that follows another": {
				input: stringtest.LinesLF("---", "---", "b: 2"),
				index: 1,
				want:  map[string]int{"b": 2},
			},
			"empty documents on both sides": {
				input: stringtest.LinesLF(
					"--- # head",
					"---",
					"b: 2",
					"---",
					"---",
				),
				index: 1,
				want:  map[string]int{"b": 2},
			},
			"comment block above the first header": {
				input: stringtest.JoinLF("# Copyright notice.", "---", "a: 1"),
				want:  map[string]int{"a": 1},
			},
			"directive above the first header": {
				input: stringtest.JoinLF("%YAML 1.2", "---", "a: 1"),
				want:  map[string]int{"a": 1},
			},
			// A file of empty documents alone reads as an empty file does.
			"empty file":                         {input: ""},
			"several empty documents":            {input: stringtest.JoinLF("---", "# a", "---", "# b")},
			"comments on both sides of a header": {input: stringtest.JoinLF("# a", "---", "# b")},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				source := niceyaml.NewSourceFromString(tc.input)

				doc := yamltest.Document(t, source)
				assert.Equal(t, tc.index, doc.DocumentIndex())
				assert.Same(t, source.AllDocuments()[tc.index], doc)
				assert.Equal(t, tc.want == nil, doc.IsEmpty())
				assert.Same(t, yamltest.Documents(t, source, 1)[0], doc)

				got, err := source.Decode[map[string]int](t.Context())
				require.NoError(t, err)
				assert.Equal(t, tc.want, got)
			})
		}
	})

	t.Run("returns the empty document of a stream of markers alone", func(t *testing.T) {
		t.Parallel()

		// The parser finds no document in a stream of "..." markers alone,
		// and the Source gives it one empty document, as it gives an
		// empty file. The markers and any comment on their lines are the
		// preamble of that document.
		tcs := map[string]struct {
			input string
			lines int
		}{
			"lone marker": {
				input: "...\n",
				lines: 1,
			},
			"marker without a line break": {
				input: "...",
				lines: 1,
			},
			"marker with comment": {
				input: "... # license\n",
				lines: 1,
			},
			"two markers": {
				input: "...\n...\n",
				lines: 2,
			},
			"blank lines above a marker": {
				input: "\n\n...\n",
				lines: 3,
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				source := niceyaml.NewSourceFromString(tc.input)

				doc := yamltest.Document(t, source)
				assert.Same(t, yamltest.Documents(t, source, 1)[0], doc)

				file, err := source.File()
				require.NoError(t, err)
				require.Len(t, file.Docs, 1)
				assert.Same(t, file.Docs[0], doc.DocumentAST())

				assert.Nil(t, doc.AST())
				assert.Equal(t, source.Tokens(), doc.Tokens())
				assert.Equal(t, doc.Tokens(), doc.Preamble())
				assert.Equal(t, position.NewSpan(0, tc.lines), doc.Span())

				got, err := source.Decode[map[string]int](t.Context())
				require.NoError(t, err)
				assert.Nil(t, got)
			})
		}
	})
}

func TestWithByteLimit(t *testing.T) {
	t.Parallel()

	text := "name: " + strings.Repeat("x", 2000) + "\n"

	t.Run("a text past the limit keeps its lines and fails its document", func(t *testing.T) {
		t.Parallel()

		opts := []niceyaml.SourceOption{niceyaml.WithName("big.yaml"), niceyaml.WithByteLimit(1024)}

		tcs := map[string]struct {
			source *niceyaml.Source
		}{
			"string": {source: niceyaml.NewSourceFromString(text, opts...)},
			"bytes":  {source: niceyaml.NewSourceFromBytes([]byte(text), opts...)},
			"text of another source": {
				source: niceyaml.NewSourceFromString("name: x\n", opts...).WithText(text),
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				source := tc.source

				assert.Equal(t, text, source.Text())
				assert.Equal(t, strings.TrimSuffix(text, "\n"), source.Lines().Content())
				assert.Equal(t, 1024, source.ByteLimit())

				_, err := source.Document()
				require.ErrorIs(t, err, niceyaml.ErrLimit)
				assert.Equal(t, "big.yaml: file is larger than 1 KiB", err.Error())
				assert.True(t, niceyaml.IsInvalid(err))

				_, fileErr := source.File()
				assert.Same(t, err, fileErr)

				doc := yamltest.Documents(t, source, 1)[0]
				require.ErrorIs(t, doc.Err(), niceyaml.ErrLimit)
				require.NotErrorIs(t, doc.Err(), niceyaml.ErrSyntax)
				assert.Nil(t, doc.AST())
				assert.Equal(t, position.NewSpan(0, 1), doc.Span())
				assert.Equal(t, source.Tokens(), doc.Tokens())

				_, err = source.Decode[map[string]any](t.Context())
				require.ErrorIs(t, err, niceyaml.ErrLimit)
			})
		}
	})

	t.Run("texts past the limit diff by their lines", func(t *testing.T) {
		t.Parallel()

		before := niceyaml.NewSourceFromString(text+"port: 80\n", niceyaml.WithByteLimit(1024))
		after := niceyaml.NewSourceFromString(text+"port: 81\n", niceyaml.WithByteLimit(1024))

		result := yamltest.DiffLines(t, before.Lines(), after.Lines())

		assert.Len(t, result.Changes(), 1)
		assert.Equal(t, diff.Stats{Added: 1, Removed: 1}, result.Stats())
	})

	t.Run("a text of several documents past the limit fails as one document", func(t *testing.T) {
		t.Parallel()

		source := niceyaml.NewSourceFromString(
			stringtest.LinesLF("# first", "a: "+strings.Repeat("x", 600), "---", "b: "+strings.Repeat("y", 600), "---"),
			niceyaml.WithName("big.yaml"), niceyaml.WithByteLimit(1024),
		)

		_, fileErr := source.File()
		require.ErrorIs(t, fileErr, niceyaml.ErrLimit)
		assert.Equal(t, "big.yaml: file is larger than 1 KiB", fileErr.Error())

		doc := yamltest.AllDocuments(t, source, 1)[0]
		assert.Same(t, fileErr, doc.Err())
		assert.Equal(t, position.NewSpan(0, source.Lines().Len()), doc.Span())
		assert.Equal(t, source.Tokens(), doc.Tokens())
		assert.Same(t, doc, yamltest.BoundDocument(t, yamltest.RequireSourceError(t, fileErr)))

		at, ok := source.DocumentAt(position.New(3, 0))
		require.True(t, ok)
		assert.Same(t, doc, at)

		err := source.ValidateDocuments(t.Context(), niceyaml.NoValidator())
		assert.Same(t, fileErr, err)
	})

	t.Run("a text of exactly the limit parses", func(t *testing.T) {
		t.Parallel()

		yamltest.Document(t, niceyaml.NewSourceFromString(text, niceyaml.WithByteLimit(len(text))))
	})

	t.Run("NoLimit turns the check off", func(t *testing.T) {
		t.Parallel()

		yamltest.Document(t, niceyaml.NewSourceFromString(text, niceyaml.WithByteLimit(niceyaml.NoLimit)))
	})

	t.Run("a limit of zero or less keeps the default", func(t *testing.T) {
		t.Parallel()

		big := "name: " + strings.Repeat("x", niceyaml.DefaultByteLimit) + "\n"

		for _, n := range []int{0, -1} {
			_, err := niceyaml.NewSourceFromReader(strings.NewReader(big), niceyaml.WithByteLimit(n))
			require.ErrorIs(t, err, niceyaml.ErrLimit)
			assert.ErrorContains(t, err, "file is larger than 256 KiB")
		}
	})

	t.Run("a limit after WithoutLimits applies", func(t *testing.T) {
		t.Parallel()

		tcs := map[string]struct {
			source *niceyaml.Source
		}{
			"options": {
				source: niceyaml.NewSourceFromString(text, niceyaml.WithoutLimits(), niceyaml.WithByteLimit(1024)),
			},
			"text of a source without limits": {
				source: niceyaml.NewSourceFromString("name: x\n", niceyaml.WithoutLimits()).
					WithText(text, niceyaml.WithByteLimit(1024)),
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				assert.Equal(t, 1024, tc.source.ByteLimit())

				_, err := tc.source.Document()
				require.ErrorIs(t, err, niceyaml.ErrLimit)
			})
		}
	})

	t.Run("readers stop one byte past the limit", func(t *testing.T) {
		t.Parallel()

		path := yamltest.WriteFile(t, t.TempDir(), "big.yaml", text)
		fsys := yamltest.MapFS(map[string]string{"big.yaml": text})

		// Each error names the Source as the constructor or WithName names
		// it, as the error of a string past the limit does.
		tcs := map[string]struct {
			read func() (*niceyaml.Source, error)
			// The name of the Source that the error binds to.
			name string
		}{
			"file": {
				read: func() (*niceyaml.Source, error) {
					return niceyaml.NewSourceFromFile(path, niceyaml.WithByteLimit(1000))
				},
				name: path,
			},
			"file system": {
				read: func() (*niceyaml.Source, error) {
					return niceyaml.NewSourceFromFS(fsys, "big.yaml", niceyaml.WithByteLimit(1000))
				},
				name: "big.yaml",
			},
			"named file system": {
				read: func() (*niceyaml.Source, error) {
					return niceyaml.NewSourceFromFS(fsys, "big.yaml",
						niceyaml.WithByteLimit(1000), niceyaml.WithName("bundle.yaml"))
				},
				name: "bundle.yaml",
			},
			"named reader": {
				read: func() (*niceyaml.Source, error) {
					return niceyaml.NewSourceFromReader(strings.NewReader(text),
						niceyaml.WithByteLimit(1000), niceyaml.WithName("upload.yaml"))
				},
				name: "upload.yaml",
			},
			"reader": {
				read: func() (*niceyaml.Source, error) {
					return niceyaml.NewSourceFromReader(strings.NewReader(text), niceyaml.WithByteLimit(1000))
				},
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				source, err := tc.read()
				require.ErrorIs(t, err, niceyaml.ErrLimit)
				assert.True(t, niceyaml.IsInvalid(err))
				assert.Nil(t, source)

				want := "file is larger than 1000 bytes"
				if tc.name != "" {
					want = tc.name + ": " + want
				}

				assert.Equal(t, want, err.Error())

				var bound *niceyaml.SourceError

				require.ErrorAs(t, err, &bound)
				assert.Equal(t, tc.name, bound.Source().Name())
			})
		}
	})

	t.Run("readers read the whole text with NoLimit", func(t *testing.T) {
		t.Parallel()

		path := yamltest.WriteFile(t, t.TempDir(), "big.yaml", text)
		fsys := yamltest.MapFS(map[string]string{"big.yaml": text})

		tcs := map[string]struct {
			read func(opt niceyaml.SourceOption) (*niceyaml.Source, error)
		}{
			"file": {read: func(opt niceyaml.SourceOption) (*niceyaml.Source, error) {
				return niceyaml.NewSourceFromFile(path, opt)
			}},
			"file system": {read: func(opt niceyaml.SourceOption) (*niceyaml.Source, error) {
				return niceyaml.NewSourceFromFS(fsys, "big.yaml", opt)
			}},
			"reader": {read: func(opt niceyaml.SourceOption) (*niceyaml.Source, error) {
				return niceyaml.NewSourceFromReader(strings.NewReader(text), opt)
			}},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				source, err := tc.read(niceyaml.WithByteLimit(niceyaml.NoLimit))
				require.NoError(t, err)
				assert.Equal(t, text, source.Text())
			})
		}
	})

	t.Run("the merged text of layers skips the limit", func(t *testing.T) {
		t.Parallel()

		half := "a: " + strings.Repeat("x", 700) + "\n"
		other := "b: " + strings.Repeat("y", 700) + "\n"

		merged := yamltest.Layers(t,
			niceyaml.NewSourceFromString(half, niceyaml.WithByteLimit(1024)),
			niceyaml.NewSourceFromString(other, niceyaml.WithByteLimit(1024)),
		)

		yamltest.Document(t, merged)
	})

	t.Run("a reference past the limit fails every decode", func(t *testing.T) {
		t.Parallel()

		ref := niceyaml.NewSourceFromString("base: &base {port: 1}\n"+strings.Repeat("#\n", 600),
			niceyaml.WithByteLimit(1024))
		source := niceyaml.NewSourceFromString("server: *base\n", niceyaml.WithReferences(ref))

		_, err := source.Decode[map[string]any](t.Context())
		require.ErrorIs(t, err, niceyaml.ErrLimit)
	})
}

func TestWithDocumentByteLimit(t *testing.T) {
	t.Parallel()

	// The large document holds more than 1 KiB, and each other document
	// holds a few bytes.
	large := "name: " + strings.Repeat("x", 2000) + "\n"

	tcs := map[string]struct {
		input string
		// The message of the error of the document at index bad, or none.
		want string
		opts []niceyaml.SourceOption
		bad  int
	}{
		"a document past a limit": {
			input: large,
			opts:  []niceyaml.SourceOption{niceyaml.WithDocumentByteLimit(1024)},
			want:  "big.yaml:1:1: document is larger than 1 KiB",
		},
		"a document after a header past a limit": {
			input: "a: 1\n---\n" + large,
			opts:  []niceyaml.SourceOption{niceyaml.WithDocumentByteLimit(1024)},
			want:  "big.yaml:2:1: document is larger than 1 KiB",
			bad:   1,
		},
		"a document binds past its comments": {
			input: "# note\n" + large,
			opts:  []niceyaml.SourceOption{niceyaml.WithDocumentByteLimit(1024)},
			want:  "big.yaml:2:1: document is larger than 1 KiB",
		},
		"a document of comments past a limit": {
			input: "# " + strings.Repeat("x", 2000) + "\n",
			opts:  []niceyaml.SourceOption{niceyaml.WithDocumentByteLimit(1024)},
			want:  "big.yaml:1:1: document is larger than 1 KiB",
		},
		"a document past the default": {
			input: "name: " + strings.Repeat("x", niceyaml.DefaultDocumentByteLimit) + "\n",
			opts:  []niceyaml.SourceOption{niceyaml.WithByteLimit(niceyaml.NoLimit)},
			want:  "big.yaml:1:1: document is larger than 256 KiB",
		},
		"a document of exactly the limit": {
			input: large,
			opts:  []niceyaml.SourceOption{niceyaml.WithDocumentByteLimit(len(large))},
		},
		"a tagged document of exactly both limits": {
			// The lexer repeats the line ending after the tag, and the
			// check counts it once.
			input: "a: !!str\n  x\n",
			opts: []niceyaml.SourceOption{
				niceyaml.WithByteLimit(len("a: !!str\n  x\n")),
				niceyaml.WithDocumentByteLimit(len("a: !!str\n  x\n")),
			},
		},
		"a tagged document past a limit": {
			input: "a: !!str\n  x\n",
			opts:  []niceyaml.SourceOption{niceyaml.WithDocumentByteLimit(len("a: !!str\n  x\n") - 1)},
			want:  "big.yaml:1:1: document is larger than 12 bytes",
		},
		"comments below a marker count in the next document": {
			input: "a: 1\n...\n# " + strings.Repeat("x", 2000) + "\nb: 2\n",
			opts:  []niceyaml.SourceOption{niceyaml.WithDocumentByteLimit(1024)},
			want:  "big.yaml:4:1: document is larger than 1 KiB",
			bad:   1,
		},
		"documents that each fit a limit": {
			input: large + "---\n" + large,
			opts:  []niceyaml.SourceOption{niceyaml.WithDocumentByteLimit(len(large) + 4)},
		},
		"NoLimit turns the check off": {
			input: large,
			opts:  []niceyaml.SourceOption{niceyaml.WithDocumentByteLimit(niceyaml.NoLimit)},
		},
		"a limit of zero keeps an earlier limit": {
			input: large,
			opts: []niceyaml.SourceOption{
				niceyaml.WithDocumentByteLimit(1024),
				niceyaml.WithDocumentByteLimit(0),
			},
			want: "big.yaml:1:1: document is larger than 1 KiB",
		},
		"a negative limit keeps an earlier limit": {
			input: large,
			opts: []niceyaml.SourceOption{
				niceyaml.WithDocumentByteLimit(1024),
				niceyaml.WithDocumentByteLimit(-1),
			},
			want: "big.yaml:1:1: document is larger than 1 KiB",
		},
		"WithoutLimits turns the check off": {
			input: large,
			opts:  []niceyaml.SourceOption{niceyaml.WithDocumentByteLimit(1024), niceyaml.WithoutLimits()},
		},
		"a limit after WithoutLimits": {
			input: large,
			opts:  []niceyaml.SourceOption{niceyaml.WithoutLimits(), niceyaml.WithDocumentByteLimit(1024)},
			want:  "big.yaml:1:1: document is larger than 1 KiB",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			opts := append([]niceyaml.SourceOption{niceyaml.WithName("big.yaml")}, tc.opts...)
			source := niceyaml.NewSourceFromString(tc.input, opts...)

			_, err := source.File()
			if tc.want == "" {
				require.NoError(t, err)

				return
			}

			require.ErrorIs(t, err, niceyaml.ErrLimit)
			require.NotErrorIs(t, err, niceyaml.ErrSyntax)
			assert.Equal(t, tc.want, err.Error())
			assert.True(t, niceyaml.IsInvalid(err))

			doc := source.AllDocuments()[tc.bad]
			assert.Same(t, err, doc.Err())
			assert.Nil(t, doc.AST())
		})
	}

	t.Run("the other documents still parse", func(t *testing.T) {
		t.Parallel()

		source := niceyaml.NewSourceFromString("a: 1\n---\n"+large+"---\nb: 2\n",
			niceyaml.WithDocumentByteLimit(1024))

		docs := yamltest.Documents(t, source, 3)
		require.NoError(t, docs[0].Err())
		require.ErrorIs(t, docs[1].Err(), niceyaml.ErrLimit)
		require.NoError(t, docs[2].Err())

		got, err := docs[2].Decode[map[string]int](t.Context())
		require.NoError(t, err)
		assert.Equal(t, map[string]int{"b": 2}, got)
	})

	t.Run("a raised byte limit reads a stream of documents that each fit", func(t *testing.T) {
		t.Parallel()

		// The stream holds more than the default byte limit, and each
		// document holds less than the default document byte limit. A
		// raised byte limit thus reads it whole, and the one document past
		// the document byte limit fails alone.
		item := "a: " + strings.Repeat("x", 64<<10) + "\n"
		stream := strings.Repeat(item+"---\n", 5) + "big: " + strings.Repeat("y", niceyaml.DefaultDocumentByteLimit)
		require.Greater(t, len(stream), niceyaml.DefaultByteLimit)

		_, err := niceyaml.NewSourceFromReader(strings.NewReader(stream), niceyaml.WithName("stream.yaml"))
		require.ErrorIs(t, err, niceyaml.ErrLimit)
		assert.Equal(t, "stream.yaml: file is larger than 256 KiB", err.Error())

		source, err := niceyaml.NewSourceFromReader(strings.NewReader(stream),
			niceyaml.WithName("stream.yaml"), niceyaml.WithByteLimit(4<<20))
		require.NoError(t, err)

		docs := yamltest.Documents(t, source, 6)
		for _, doc := range docs[:5] {
			require.NoError(t, doc.Err())
		}

		require.ErrorIs(t, docs[5].Err(), niceyaml.ErrLimit)
		assert.Equal(t, "stream.yaml:10:1: document is larger than 256 KiB", docs[5].Err().Error())
	})

	t.Run("the byte limit does not bound the aliases", func(t *testing.T) {
		t.Parallel()

		// The two aliases repeat 1200 bytes of text, past a byte limit of
		// 1 KiB, and the text holds less.
		text := "a: &a " + strings.Repeat("x", 600) + "\nb: *a\nc: *a\n"

		got, err := niceyaml.NewSourceFromString(text, niceyaml.WithByteLimit(1024)).
			Decode[map[string]rawText](t.Context())
		require.NoError(t, err)
		assert.Len(t, got, 3)

		_, err = niceyaml.NewSourceFromString(text, niceyaml.WithDocumentByteLimit(1024)).
			Decode[map[string]rawText](t.Context())
		require.ErrorIs(t, err, niceyaml.ErrLimit)
		assert.Equal(t, "3:4: aliases repeat more than 1 KiB of text", err.Error())
	})

	t.Run("a reference past the limit fails every decode", func(t *testing.T) {
		t.Parallel()

		ref := niceyaml.NewSourceFromString("base: &base {port: 1}\n"+strings.Repeat("#\n", 600),
			niceyaml.WithName("ref.yaml"), niceyaml.WithDocumentByteLimit(1024))
		source := niceyaml.NewSourceFromString("server: *base\n", niceyaml.WithReferences(ref))

		_, err := source.Decode[map[string]any](t.Context())
		require.ErrorIs(t, err, niceyaml.ErrLimit)
		assert.Equal(t, "ref.yaml:1:1: document is larger than 1 KiB", err.Error())
	})
}

func TestSource_DocumentByteLimit(t *testing.T) {
	t.Parallel()

	var nilSource *niceyaml.Source

	tcs := map[string]struct {
		source *niceyaml.Source
		want   int
	}{
		"default": {source: niceyaml.NewSourceFromString("a: 1\n"), want: niceyaml.DefaultDocumentByteLimit},
		"set": {
			source: niceyaml.NewSourceFromString("a: 1\n", niceyaml.WithDocumentByteLimit(10)),
			want:   10,
		},
		"off": {
			source: niceyaml.NewSourceFromString("a: 1\n", niceyaml.WithDocumentByteLimit(niceyaml.NoLimit)),
			want:   niceyaml.NoLimit,
		},
		"zero keeps the default": {
			source: niceyaml.NewSourceFromString("a: 1\n", niceyaml.WithDocumentByteLimit(0)),
			want:   niceyaml.DefaultDocumentByteLimit,
		},
		"the byte limit leaves it": {
			source: niceyaml.NewSourceFromString("a: 1\n", niceyaml.WithByteLimit(10)),
			want:   niceyaml.DefaultDocumentByteLimit,
		},
		"WithoutLimits": {
			source: niceyaml.NewSourceFromString("a: 1\n",
				niceyaml.WithDocumentByteLimit(10), niceyaml.WithoutLimits()),
			want: niceyaml.NoLimit,
		},
		"text of another source": {
			source: niceyaml.NewSourceFromString("a: 1\n", niceyaml.WithDocumentByteLimit(10)).WithText("b: 2\n"),
			want:   10,
		},
		"zero value": {source: &niceyaml.Source{}, want: niceyaml.DefaultDocumentByteLimit},
		"nil":        {source: nilSource, want: niceyaml.NoLimit},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, tc.source.DocumentByteLimit())
		})
	}

	t.Run("layers", func(t *testing.T) {
		t.Parallel()

		merged := yamltest.Layers(
			t,
			yamltest.Document(t, niceyaml.NewSourceFromString("a: 1\n", niceyaml.WithDocumentByteLimit(10))),
		)

		assert.Equal(t, niceyaml.NoLimit, merged.DocumentByteLimit())
	})
}

func TestWithDepthLimit(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input string
		// The message of the error of the first document, or none.
		want string
		opts []niceyaml.SourceOption
	}{
		"flow brackets past the default": {
			input: "a: " + strings.Repeat("[", 101) + strings.Repeat("]", 101) + "\n",
			want:  "deep.yaml:1:103: document nests more than 100 levels",
		},
		"block mappings past a limit": {
			input: stringtest.Input(`
				a:
				  b:
				    c:
				      d: 1
			`),
			opts: []niceyaml.SourceOption{niceyaml.WithDepthLimit(3)},
			want: "deep.yaml:4:7: document nests more than 3 levels",
		},
		"a sequence in the column of its key opens a level": {
			input: stringtest.Input(`
				a:
				- b:
				  - 1
			`),
			opts: []niceyaml.SourceOption{niceyaml.WithDepthLimit(3)},
			want: "deep.yaml:3:3: document nests more than 3 levels",
		},
		"block mappings within a limit": {
			input: stringtest.LinesLF(
				"a:",
				"  b:",
				"    c: 1",
			),
			opts: []niceyaml.SourceOption{niceyaml.WithDepthLimit(3)},
		},
		"an alias opens no level": {
			input: stringtest.LinesLF("a: &a [[1]]", "b: [*a, *a]"),
			opts:  []niceyaml.SourceOption{niceyaml.WithDepthLimit(3)},
		},
		"NoLimit turns the check off": {
			input: "a: " + strings.Repeat("[", 200) + strings.Repeat("]", 200) + "\n",
			opts:  []niceyaml.SourceOption{niceyaml.WithDepthLimit(niceyaml.NoLimit)},
		},
		"a limit of zero keeps the default": {
			input: "a: " + strings.Repeat("[", 101) + strings.Repeat("]", 101) + "\n",
			opts:  []niceyaml.SourceOption{niceyaml.WithDepthLimit(0)},
			want:  "deep.yaml:1:103: document nests more than 100 levels",
		},
		"a negative limit keeps the default": {
			input: "a: " + strings.Repeat("[", 101) + strings.Repeat("]", 101) + "\n",
			opts:  []niceyaml.SourceOption{niceyaml.WithDepthLimit(-1)},
			want:  "deep.yaml:1:103: document nests more than 100 levels",
		},
		"a limit after WithoutLimits": {
			input: stringtest.LinesLF("a:", "  b:", "    c: 1"),
			opts:  []niceyaml.SourceOption{niceyaml.WithoutLimits(), niceyaml.WithDepthLimit(2)},
			want:  "deep.yaml:3:5: document nests more than 2 levels",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			opts := append([]niceyaml.SourceOption{niceyaml.WithName("deep.yaml")}, tc.opts...)
			source := niceyaml.NewSourceFromString(tc.input, opts...)

			_, err := source.Document()
			if tc.want == "" {
				require.NoError(t, err)

				return
			}

			require.ErrorIs(t, err, niceyaml.ErrLimit)
			require.NotErrorIs(t, err, niceyaml.ErrSyntax)
			assert.Equal(t, tc.want, err.Error())
			assert.True(t, niceyaml.IsInvalid(err))
		})
	}

	t.Run("the other documents still parse", func(t *testing.T) {
		t.Parallel()

		source := niceyaml.NewSourceFromString(
			stringtest.LinesLF("a: [[[1]]]", "---", "b: 1"),
			niceyaml.WithDepthLimit(2),
		)

		docs := yamltest.Documents(t, source, 2)
		require.ErrorIs(t, docs[0].Err(), niceyaml.ErrLimit)
		require.NotErrorIs(t, docs[0].Err(), niceyaml.ErrSyntax)
		assert.Nil(t, docs[0].AST())
		require.NoError(t, docs[1].Err())
	})

	t.Run("a reference past the limit fails every decode", func(t *testing.T) {
		t.Parallel()

		ref := niceyaml.NewSourceFromString(stringtest.LinesLF("base: &base {port: 1}", "deep: [[[1]]]"),
			niceyaml.WithName("ref.yaml"), niceyaml.WithDepthLimit(2))
		source := niceyaml.NewSourceFromString("server: *base\n", niceyaml.WithReferences(ref))

		_, err := source.Decode[map[string]any](t.Context())
		require.ErrorIs(t, err, niceyaml.ErrLimit)
		assert.Equal(t, "ref.yaml:2:8: document nests more than 2 levels", err.Error())
	})
}

func TestSource_WithText(t *testing.T) {
	t.Parallel()

	t.Run("keeps every setting", func(t *testing.T) {
		t.Parallel()

		type port int

		um := niceyaml.CustomUnmarshaler(func(_ context.Context, v *port, _ func(any) error) error {
			*v = 7

			return nil
		})

		source := niceyaml.NewSourceFromString("a: *x\n",
			niceyaml.WithName("app.yaml"), niceyaml.WithFilePath("/app.yaml"), niceyaml.WithFS(fstest.MapFS{}),
			yamltest.References("x: &x 1\n"), niceyaml.WithAllowDuplicateKeys(true),
			niceyaml.WithByteLimit(100), niceyaml.WithDocumentByteLimit(50), niceyaml.WithDepthLimit(3),
			niceyaml.WithUnmarshalers(um), niceyaml.WithExcerpts(false))

		next := source.WithText("b: *x\nb: 0\n")

		assert.Equal(t, "app.yaml", next.Name())
		assert.Equal(t, "/app.yaml", next.FilePath())
		assert.Equal(t, source.FS(), next.FS())
		assert.Equal(t, source.References(), next.References())
		assert.True(t, next.AllowsDuplicateKeys())
		assert.Equal(t, 100, next.ByteLimit())
		assert.Equal(t, 50, next.DocumentByteLimit())
		assert.Equal(t, 3, next.DepthLimit())
		assert.False(t, next.ShowsExcerpts())
		assert.Equal(t, "b: *x\nb: 0\n", next.Text())

		// The reference resolves, the repeated key parses, and the
		// unmarshaler reads the value.
		got, err := next.Decode[map[string]port](t.Context())
		require.NoError(t, err)
		assert.Equal(t, map[string]port{"b": 7}, got)
	})

	t.Run("applies opts over the settings", func(t *testing.T) {
		t.Parallel()

		source := niceyaml.NewSourceFromString("a: 1\n", niceyaml.WithName("app.yaml"), niceyaml.WithByteLimit(100))

		next := source.WithText("b: 2\n", niceyaml.WithName("formatted"), niceyaml.WithByteLimit(4))

		assert.Equal(t, "formatted", next.Name())
		assert.Equal(t, 4, next.ByteLimit())
		assert.Equal(t, "app.yaml", source.Name())
		assert.Equal(t, 100, source.ByteLimit())

		_, err := next.Document()
		require.ErrorIs(t, err, niceyaml.ErrLimit)
		assert.Equal(t, "formatted: file is larger than 4 bytes", err.Error())
	})

	t.Run("adds references of its own", func(t *testing.T) {
		t.Parallel()

		// Three references leave room past the length of the slice, which
		// each copy must not share.
		source := niceyaml.NewSourceFromString("a: *x\n",
			yamltest.References("r1: &r1 1\n"), yamltest.References("r2: &r2 2\n"),
			yamltest.References("r3: &r3 3\n"))

		first := niceyaml.NewSourceFromString("x: &x 10\n")
		second := niceyaml.NewSourceFromString("x: &x 20\n")

		one := source.WithText("a: *x\n", niceyaml.WithReferences(first))
		two := source.WithText("a: *x\n", niceyaml.WithReferences(second))

		assert.Same(t, first, one.References()[3])
		assert.Same(t, second, two.References()[3])
		assert.Len(t, source.References(), 3)

		got, err := one.Decode[map[string]int](t.Context())
		require.NoError(t, err)
		assert.Equal(t, map[string]int{"a": 10}, got)
	})

	t.Run("keeps the default limits", func(t *testing.T) {
		t.Parallel()

		next := niceyaml.NewSourceFromString("a: 1\n").WithText("b: 2\n")

		assert.Equal(t, niceyaml.DefaultByteLimit, next.ByteLimit())
		assert.Equal(t, niceyaml.DefaultDocumentByteLimit, next.DocumentByteLimit())
		assert.Equal(t, niceyaml.DefaultDepthLimit, next.DepthLimit())
	})

	t.Run("the text of one document gives it a Source of its own", func(t *testing.T) {
		t.Parallel()

		tcs := map[string]struct {
			values map[string]any
			input  string
			want   string
			doc    int
		}{
			"final line break": {
				input:  "kind: A\nport: 1\n---\nkind: B\r\nport: 2\n",
				doc:    1,
				want:   "---\nkind: B\r\nport: 2\n",
				values: map[string]any{"kind": "B", "port": uint64(2)},
			},
			"no final line break": {
				input:  "kind: A\nport: 1\n---\nkind: B\nport: 2",
				doc:    1,
				want:   "---\nkind: B\nport: 2",
				values: map[string]any{"kind": "B", "port": uint64(2)},
			},
			"byte order mark at the start of the text": {
				input:  "\uFEFFkind: A\nport: 1\n---\nkind: B\nport: 2\n",
				doc:    0,
				want:   "\uFEFFkind: A\nport: 1\n",
				values: map[string]any{"kind": "A", "port": uint64(1)},
			},
			"byte order mark before the next header": {
				input:  "kind: A\nport: 1\n\uFEFF---\nkind: B\nport: 2\n",
				doc:    0,
				want:   "kind: A\nport: 1\n",
				values: map[string]any{"kind": "A", "port": uint64(1)},
			},
			"byte order mark before its header": {
				input:  "kind: A\nport: 1\n\uFEFF---\nkind: B\nport: 2\n",
				doc:    1,
				want:   "\uFEFF---\nkind: B\nport: 2\n",
				values: map[string]any{"kind": "B", "port": uint64(2)},
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				source := niceyaml.NewSourceFromString(tc.input)
				doc := yamltest.Documents(t, source, 2)[tc.doc]

				one := source.WithText(doc.Text())
				assert.Equal(t, tc.want, one.Text())

				pos, ok := one.PositionAt(len(one.Text()))
				require.True(t, ok)

				off, ok := one.Offset(pos)
				require.True(t, ok)
				assert.Equal(t, len(tc.want), off)

				got, err := one.Decode[map[string]any](t.Context())
				require.NoError(t, err)
				assert.Equal(t, tc.values, got)
			})
		}
	})

	t.Run("a Source of layers gives a Source of text", func(t *testing.T) {
		t.Parallel()

		base := yamltest.Document(t, niceyaml.NewSourceFromString("a: 1\n", niceyaml.WithName("base.yaml")))
		over := yamltest.Document(t, niceyaml.NewSourceFromString("b: 2\n", niceyaml.WithName("over.yaml")))
		merged := yamltest.Layers(t, base, over)

		next := merged.WithText("c: x\n")

		// The merged Source skips the limits, and so does the next one.
		assert.Equal(t, niceyaml.NoLimit, next.ByteLimit())
		assert.Equal(t, niceyaml.NoLimit, next.DepthLimit())

		_, err := next.Decode[struct {
			C int `yaml:"c"`
		}](t.Context())
		require.Error(t, err)
		yamltest.AssertBoundTo(t, next, err)
	})

	t.Run("a derived Source gives a Source that binds in itself", func(t *testing.T) {
		t.Parallel()

		home := niceyaml.NewSourceFromString("a: 1\n", niceyaml.WithName("home.yaml"))
		work := niceyaml.NewSourceFromDerived("a: 2\n", yamltest.Document(t, home), nil)

		next := work.WithText("a: x\n")

		_, err := next.Decode[struct {
			A int `yaml:"a"`
		}](t.Context())
		require.Error(t, err)
		yamltest.AssertBoundTo(t, next, err)
		assert.Equal(t, "home.yaml", next.Name())
	})

	t.Run("a derived Source keeps the alias bounds of home", func(t *testing.T) {
		t.Parallel()

		// The two aliases repeat 1200 bytes of text, past the 1 KiB that the
		// document byte limit of home allows.
		text := "a: &a " + strings.Repeat("x", 600) + "\nb: *a\nc: *a\n"
		home := niceyaml.NewSourceFromString(text, niceyaml.WithDocumentByteLimit(1024))
		work := niceyaml.NewSourceFromDerived(text, yamltest.Document(t, home), nil, niceyaml.WithoutLimits())

		next := work.WithText(text)

		assert.Equal(t, niceyaml.NoLimit, next.ByteLimit())

		_, err := next.Decode[map[string]rawText](t.Context())
		require.ErrorIs(t, err, niceyaml.ErrLimit)
		assert.Equal(t, "3:4: aliases repeat more than 1 KiB of text", err.Error())
	})

	t.Run("a derived Source with limits keeps the alias bounds of home", func(t *testing.T) {
		t.Parallel()

		// The derived Source keeps the limits of home, a document byte
		// limit of 1 KiB, and its copy turns that limit off. The copy
		// still holds the aliases of home, so the 1 KiB that the document
		// byte limit of home allows bounds them.
		text := "a: &a " + strings.Repeat("x", 600) + "\nb: *a\nc: *a\n"
		home := niceyaml.NewSourceFromString(text, niceyaml.WithDocumentByteLimit(1024))
		work := niceyaml.NewSourceFromDerived(text, yamltest.Document(t, home), nil)

		next := work.WithText(text, niceyaml.WithDocumentByteLimit(niceyaml.NoLimit))

		assert.Equal(t, niceyaml.NoLimit, next.DocumentByteLimit())

		_, err := next.Decode[map[string]rawText](t.Context())
		require.ErrorIs(t, err, niceyaml.ErrLimit)
		assert.Equal(t, "3:4: aliases repeat more than 1 KiB of text", err.Error())
	})

	t.Run("nil Source", func(t *testing.T) {
		t.Parallel()

		var source *niceyaml.Source

		next := source.WithText("a: 1\n", niceyaml.WithName("new"))

		assert.Equal(t, "new", next.Name())
		assert.Equal(t, "a: 1\n", next.Text())
	})
}

func TestSource_ByteLimit(t *testing.T) {
	t.Parallel()

	var nilSource *niceyaml.Source

	tcs := map[string]struct {
		source *niceyaml.Source
		want   int
	}{
		"default": {source: niceyaml.NewSourceFromString("a: 1\n"), want: niceyaml.DefaultByteLimit},
		"set":     {source: niceyaml.NewSourceFromString("a: 1\n", niceyaml.WithByteLimit(10)), want: 10},
		"off": {
			source: niceyaml.NewSourceFromString("a: 1\n", niceyaml.WithByteLimit(niceyaml.NoLimit)),
			want:   niceyaml.NoLimit,
		},
		"zero keeps the default": {
			source: niceyaml.NewSourceFromString("a: 1\n", niceyaml.WithByteLimit(0)),
			want:   niceyaml.DefaultByteLimit,
		},
		"negative keeps the default": {
			source: niceyaml.NewSourceFromString("a: 1\n", niceyaml.WithByteLimit(-1)),
			want:   niceyaml.DefaultByteLimit,
		},
		"zero keeps an earlier limit": {
			source: niceyaml.NewSourceFromString("a: 1\n", niceyaml.WithByteLimit(10), niceyaml.WithByteLimit(0)),
			want:   10,
		},
		"WithoutLimits": {
			source: niceyaml.NewSourceFromString("a: 1\n", niceyaml.WithByteLimit(10), niceyaml.WithoutLimits()),
			want:   niceyaml.NoLimit,
		},
		"a limit after WithoutLimits": {
			source: niceyaml.NewSourceFromString("a: 1\n", niceyaml.WithoutLimits(), niceyaml.WithByteLimit(10)),
			want:   10,
		},
		"zero value": {source: &niceyaml.Source{}, want: niceyaml.DefaultByteLimit},
		"nil":        {source: nilSource, want: niceyaml.NoLimit},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, tc.source.ByteLimit())
		})
	}

	t.Run("layers", func(t *testing.T) {
		t.Parallel()

		merged := yamltest.Layers(
			t,
			yamltest.Document(t, niceyaml.NewSourceFromString("a: 1\n", niceyaml.WithByteLimit(10))),
		)

		assert.Equal(t, niceyaml.NoLimit, merged.ByteLimit())
	})
}

func TestSource_DepthLimit(t *testing.T) {
	t.Parallel()

	var nilSource *niceyaml.Source

	tcs := map[string]struct {
		source *niceyaml.Source
		want   int
	}{
		"default": {source: niceyaml.NewSourceFromString("a: 1\n"), want: niceyaml.DefaultDepthLimit},
		"set":     {source: niceyaml.NewSourceFromString("a: 1\n", niceyaml.WithDepthLimit(10)), want: 10},
		"off": {
			source: niceyaml.NewSourceFromString("a: 1\n", niceyaml.WithDepthLimit(niceyaml.NoLimit)),
			want:   niceyaml.NoLimit,
		},
		"zero keeps the default": {
			source: niceyaml.NewSourceFromString("a: 1\n", niceyaml.WithDepthLimit(0)),
			want:   niceyaml.DefaultDepthLimit,
		},
		"negative keeps the default": {
			source: niceyaml.NewSourceFromString("a: 1\n", niceyaml.WithDepthLimit(-1)),
			want:   niceyaml.DefaultDepthLimit,
		},
		"zero keeps an earlier limit": {
			source: niceyaml.NewSourceFromString("a: 1\n", niceyaml.WithDepthLimit(10), niceyaml.WithDepthLimit(0)),
			want:   10,
		},
		"WithoutLimits": {
			source: niceyaml.NewSourceFromString("a: 1\n", niceyaml.WithDepthLimit(10), niceyaml.WithoutLimits()),
			want:   niceyaml.NoLimit,
		},
		"a limit after WithoutLimits": {
			source: niceyaml.NewSourceFromString("a: 1\n", niceyaml.WithoutLimits(), niceyaml.WithDepthLimit(10)),
			want:   10,
		},
		"zero value": {source: &niceyaml.Source{}, want: niceyaml.DefaultDepthLimit},
		"nil":        {source: nilSource, want: niceyaml.NoLimit},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, tc.source.DepthLimit())
		})
	}

	t.Run("layers", func(t *testing.T) {
		t.Parallel()

		merged := yamltest.Layers(
			t,
			yamltest.Document(t, niceyaml.NewSourceFromString("a: 1\n", niceyaml.WithDepthLimit(10))),
		)

		assert.Equal(t, niceyaml.NoLimit, merged.DepthLimit())
	})
}

func TestSource_AllowsDuplicateKeys(t *testing.T) {
	t.Parallel()

	var nilSource *niceyaml.Source

	tcs := map[string]struct {
		source *niceyaml.Source
		want   bool
	}{
		"default": {source: niceyaml.NewSourceFromString("a: 1\n")},
		"allowed": {
			source: niceyaml.NewSourceFromString("a: 1\n", niceyaml.WithAllowDuplicateKeys(true)),
			want:   true,
		},
		"refused": {
			source: niceyaml.NewSourceFromString(
				"a: 1\n",
				niceyaml.WithAllowDuplicateKeys(true),
				niceyaml.WithAllowDuplicateKeys(false),
			),
		},
		"zero value": {source: &niceyaml.Source{}},
		"nil":        {source: nilSource},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, tc.source.AllowsDuplicateKeys())
		})
	}
}

func TestSource_References(t *testing.T) {
	t.Parallel()

	t.Run("in the order given", func(t *testing.T) {
		t.Parallel()

		first := niceyaml.NewSourceFromString("x: &x 1\n")
		second := niceyaml.NewSourceFromString("y: &y 2\n")
		source := niceyaml.NewSourceFromString(
			"a: *x\n",
			niceyaml.WithReferences(first, nil),
			niceyaml.WithReferences(second),
		)

		assert.Equal(t, []*niceyaml.Source{first, second}, source.References())
	})

	t.Run("a copy", func(t *testing.T) {
		t.Parallel()

		ref := niceyaml.NewSourceFromString("x: &x 1\n")
		source := niceyaml.NewSourceFromString("a: *x\n", niceyaml.WithReferences(ref))

		refs := source.References()
		refs[0] = nil

		assert.Equal(t, []*niceyaml.Source{ref}, source.References())

		got, err := source.Decode[map[string]int](t.Context())
		require.NoError(t, err)
		assert.Equal(t, map[string]int{"a": 1}, got)
	})

	t.Run("none", func(t *testing.T) {
		t.Parallel()

		var nilSource *niceyaml.Source

		assert.Nil(t, niceyaml.NewSourceFromString("a: 1\n").References())
		assert.Nil(t, (&niceyaml.Source{}).References())
		assert.Nil(t, nilSource.References())
	})
}

func TestSource_Layers(t *testing.T) {
	t.Parallel()

	t.Run("documents lowest first", func(t *testing.T) {
		t.Parallel()

		base := niceyaml.NewSourceFromString("a: 1\n", niceyaml.WithName("base.yaml"))
		prod := niceyaml.NewSourceFromString("b: 2\n", niceyaml.WithName("prod.yaml"))

		merged, err := niceyaml.NewSourceFromLayers(t.Context(), []*niceyaml.Source{base, nil, prod},
			niceyaml.MergeAs(prod))
		require.NoError(t, err)

		got := merged.Layers()
		require.Len(t, got, 2)
		assert.Same(t, yamltest.Document(t, base), got[0])
		assert.Same(t, yamltest.Document(t, prod), got[1])
	})

	t.Run("scoped nodes as given", func(t *testing.T) {
		t.Parallel()

		doc := yamltest.FirstDocument(t, "app: {a: 1}\n", niceyaml.WithName("base.yaml"))
		app, err := doc.At(paths.Doc().Child("app"))
		require.NoError(t, err)

		top := yamltest.FirstDocument(t, "b: 2\n", niceyaml.WithName("prod.yaml"))

		merged := yamltest.Layers(t, app, top)

		assert.Equal(t, []*niceyaml.Node{app, top}, merged.Layers())
	})

	t.Run("nested merge as it is", func(t *testing.T) {
		t.Parallel()

		inner := yamltest.Layers(t,
			niceyaml.NewSourceFromString("a: 1\n", niceyaml.WithName("base.yaml")),
			niceyaml.NewSourceFromString("b: 2\n", niceyaml.WithName("mid.yaml")),
		)
		top := niceyaml.NewSourceFromString("c: 3\n", niceyaml.WithName("prod.yaml"))

		got := yamltest.Layers(t, inner, top).Layers()
		require.Len(t, got, 2)
		assert.Same(t, yamltest.Document(t, inner), got[0])
		assert.Len(t, got[0].Source().Layers(), 2)
		assert.Empty(t, got[1].Source().Layers())
	})

	t.Run("a copy", func(t *testing.T) {
		t.Parallel()

		base := niceyaml.NewSourceFromString("a: 1\n", niceyaml.WithName("base.yaml"))
		merged := yamltest.Layers(t, base)

		layers := merged.Layers()
		layers[0] = nil

		assert.Equal(t, []*niceyaml.Node{yamltest.Document(t, base)}, merged.Layers())
	})

	t.Run("none", func(t *testing.T) {
		t.Parallel()

		var nilSource *niceyaml.Source

		merged := yamltest.Layers(t, niceyaml.NewSourceFromString("a: 1\n"))

		assert.Nil(t, niceyaml.NewSourceFromString("a: 1\n").Layers())
		assert.Nil(t, merged.WithText(merged.Text()).Layers())
		assert.Nil(t, (&niceyaml.Source{}).Layers())
		assert.Nil(t, nilSource.Layers())
	})
}

func TestSource_Unmarshalers(t *testing.T) {
	t.Parallel()

	type level int

	unmarshalerOf := func(want level) niceyaml.Unmarshaler {
		return niceyaml.CustomUnmarshaler(func(_ context.Context, v *level, _ func(any) error) error {
			*v = want

			return nil
		})
	}

	t.Run("in the order given", func(t *testing.T) {
		t.Parallel()

		source := niceyaml.NewSourceFromString("a: x\n",
			niceyaml.WithUnmarshalers(unmarshalerOf(1)),
			niceyaml.WithUnmarshalers(unmarshalerOf(2)),
		)

		us := source.Unmarshalers()
		require.Len(t, us, 2)

		// Each function decodes as the Source gives it, and the later one
		// applies.
		for i, want := range []level{1, 2} {
			got, err := niceyaml.NewSourceFromString("a: x\n", niceyaml.WithUnmarshalers(us[i])).
				Decode[map[string]level](t.Context())
			require.NoError(t, err)
			assert.Equal(t, map[string]level{"a": want}, got)
		}

		got, err := niceyaml.NewSourceFromString("a: x\n", niceyaml.WithUnmarshalers(us...)).
			Decode[map[string]level](t.Context())
		require.NoError(t, err)
		assert.Equal(t, map[string]level{"a": 2}, got)
	})

	t.Run("a copy", func(t *testing.T) {
		t.Parallel()

		source := niceyaml.NewSourceFromString("a: x\n", niceyaml.WithUnmarshalers(unmarshalerOf(1)))

		us := source.Unmarshalers()
		us[0] = unmarshalerOf(2)

		got, err := source.Decode[map[string]level](t.Context())
		require.NoError(t, err)
		assert.Equal(t, map[string]level{"a": 1}, got)
		assert.Len(t, source.Unmarshalers(), 1)
	})

	t.Run("the unmarshalers of a merge", func(t *testing.T) {
		t.Parallel()

		layer := niceyaml.NewSourceFromString("a: x\n", niceyaml.WithUnmarshalers(unmarshalerOf(1)))

		merged, err := niceyaml.NewSourceFromLayers(t.Context(), []*niceyaml.Source{layer},
			niceyaml.MergeUnmarshalers(unmarshalerOf(2), unmarshalerOf(3)))
		require.NoError(t, err)
		assert.Len(t, merged.Unmarshalers(), 2)
	})

	t.Run("none", func(t *testing.T) {
		t.Parallel()

		var nilSource *niceyaml.Source

		assert.Nil(t, niceyaml.NewSourceFromString("a: 1\n").Unmarshalers())
		assert.Nil(t, (&niceyaml.Source{}).Unmarshalers())
		assert.Nil(t, nilSource.Unmarshalers())
	})
}

func TestSource_WithoutLimits(t *testing.T) {
	t.Parallel()

	source := niceyaml.NewSourceFromString("a: {b: {c: 123456}}\n",
		niceyaml.WithByteLimit(8), niceyaml.WithDocumentByteLimit(8), niceyaml.WithDepthLimit(1),
		niceyaml.WithoutLimits())

	got, err := source.Decode[map[string]any](t.Context())
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"a": map[string]any{"b": map[string]any{"c": uint64(123456)}}}, got)
}

func TestSource_PositionAt(t *testing.T) {
	t.Parallel()

	// Two lines, each of which ends in a line break.
	lf := stringtest.LinesLF("a: 1", "b: 2")
	crlf := stringtest.LinesCRLF("a: 1", "b: 2")

	tcs := map[string]struct {
		input string
		off   int
		want  position.Position
		ok    bool
	}{
		"start of the text": {
			input: lf,
			ok:    true,
		},
		"byte of a later line": {
			input: lf,
			off:   8,
			want:  position.New(1, 3),
			ok:    true,
		},
		"first byte of a rune of several bytes": {
			input: "é: 日本\n",
			off:   7,
			want:  position.New(0, 4),
			ok:    true,
		},
		"later byte of a rune of several bytes": {
			input: "é: 日本\n",
			off:   8,
			want:  position.New(0, 4),
			ok:    true,
		},
		"LF has the column after the content": {
			input: lf,
			off:   4,
			want:  position.New(0, 4),
			ok:    true,
		},
		"byte past a LF is on the next line": {
			input: lf,
			off:   5,
			want:  position.New(1, 0),
			ok:    true,
		},
		"CR of a CRLF has the column after the content": {
			input: crlf,
			off:   4,
			want:  position.New(0, 4),
			ok:    true,
		},
		"LF of a CRLF has the same column": {
			input: crlf,
			off:   5,
			want:  position.New(0, 4),
			ok:    true,
		},
		"byte past a CRLF is on the next line": {
			input: crlf,
			off:   6,
			want:  position.New(1, 0),
			ok:    true,
		},
		"bare CR ends a line": {
			input: "a: 1\rb: 2",
			off:   5,
			want:  position.New(1, 0),
			ok:    true,
		},
		"length of a text with a final line ending": {
			input: lf,
			off:   10,
			want:  position.New(2, 0),
			ok:    true,
		},
		"length of a text without a final line ending": {
			input: "a: 1",
			off:   4,
			want:  position.New(0, 4),
			ok:    true,
		},
		"empty text": {
			input: "",
			ok:    true,
		},
		"byte of a dropped mark": {
			input: "\ufeffa: 1\n",
			off:   1,
			ok:    true,
		},
		"byte after a dropped mark": {
			input: "\ufeffa: 1\n",
			off:   4,
			want:  position.New(0, 1),
			ok:    true,
		},
		"byte that is not UTF-8 takes one column": {
			input: "a: \xffb\n",
			off:   4,
			want:  position.New(0, 4),
			ok:    true,
		},
		"negative offset": {
			input: lf,
			off:   -1,
		},
		"offset past the text": {
			input: lf,
			off:   11,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			source := niceyaml.NewSourceFromString(tc.input)

			got, ok := source.PositionAt(tc.off)
			assert.Equal(t, tc.ok, ok)
			assert.Equal(t, tc.want, got)
		})
	}

	t.Run("nil Source", func(t *testing.T) {
		t.Parallel()

		var source *niceyaml.Source

		got, ok := source.PositionAt(0)
		assert.False(t, ok)
		assert.Equal(t, position.Position{}, got)
	})
}

func TestSource_PositionAt_Offset(t *testing.T) {
	t.Parallel()

	tcs := map[string]string{
		"LF":           stringtest.LinesLF("a: 1", "b: é", "c: 日本"),
		"CRLF":         stringtest.LinesCRLF("a: 1", "b: é", "c: 日本"),
		"bare CR":      "a: 1\rb: é\rc: 日本\r",
		"no final LF":  "a: 1\nb: 日本",
		"mark":         "\ufeffa: 1\n\ufeff---\nb: 2\n",
		"invalid byte": "a: \xff\nb: 2\n",
	}

	for name, input := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			source := niceyaml.NewSourceFromString(input)
			text := source.Text()

			var count int

			// Each rune of the lines comes back from its offset, and from
			// each later byte of the rune.
			for pos := range source.Lines().Runes() {
				off, ok := source.Offset(pos)
				require.True(t, ok, "position %s", pos)

				_, size := utf8.DecodeRuneInString(text[off:])
				for i := range size {
					got, ok := source.PositionAt(off + i)
					require.True(t, ok, "offset %d", off+i)
					assert.Equal(t, pos, got, "offset %d", off+i)
				}

				count++
			}

			assert.Positive(t, count)

			// The length of the text comes back as the position past the
			// last rune, and that position gives the length back.
			end, ok := source.PositionAt(len(text))
			require.True(t, ok)

			off, ok := source.Offset(end)
			require.True(t, ok)
			assert.Equal(t, len(text), off)
		})
	}
}

func TestSource_UTF16Column(t *testing.T) {
	t.Parallel()

	tags := `tags: ["😀", 2]` + "\n"

	tcs := map[string]struct {
		input string
		line  int
		col   int
		want  int
		ok    bool
	}{
		"start of the text": {
			input: "a: 1\n",
			want:  0,
			ok:    true,
		},
		"rune inside the Basic Multilingual Plane takes one unit": {
			input: "é: 日本\n",
			col:   4,
			want:  4,
			ok:    true,
		},
		"rune outside the Basic Multilingual Plane takes two units": {
			input: tags,
			col:   12,
			want:  13,
			ok:    true,
		},
		"end of a range after a rune of two units": {
			input: tags,
			col:   13,
			want:  14,
			ok:    true,
		},
		"rune of two units on a later line": {
			input: stringtest.LinesLF("😀: 1", "b: 😀"),
			line:  1,
			col:   4,
			want:  5,
			ok:    true,
		},
		"LF takes the unit after the content": {
			input: "a: 😀\n",
			col:   4,
			want:  5,
			ok:    true,
		},
		"column past a LF": {
			input: "a: 😀\n",
			col:   5,
			want:  6,
			ok:    true,
		},
		"CRLF takes one unit": {
			input: "a: 😀\r\nb: 2",
			col:   5,
			want:  6,
			ok:    true,
		},
		"end of a text with no final line ending": {
			input: "a: 😀",
			col:   4,
			want:  5,
			ok:    true,
		},
		"past the end of a text with no final line ending": {
			input: "a: 😀",
			col:   5,
		},
		"column past a line ending": {
			input: "a: 1\n",
			col:   6,
		},
		"byte order mark lies before unit 0": {
			input: "\ufeffa: 😀\n",
			col:   4,
			want:  5,
			ok:    true,
		},
		"mark at a document marker lies before unit 0": {
			input: sourceMarkedHeader,
			line:  1,
			col:   3,
			want:  3,
			ok:    true,
		},
		"mark the tokenizer keeps takes one unit": {
			input: "a: 1\n\ufeffb: 2\n",
			line:  1,
			col:   1,
			want:  1,
			ok:    true,
		},
		"invalid UTF-8 byte takes one unit": {
			input: "a: \"\xff😀\"\n",
			col:   6,
			want:  7,
			ok:    true,
		},
		"empty text": {
			input: "",
			want:  0,
			ok:    true,
		},
		"line past the last": {
			input: "a: 1\n",
			line:  2,
		},
		"negative line": {
			input: "a: 1\n",
			line:  -1,
		},
		"negative column": {
			input: "a: 1\n",
			col:   -1,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			source := niceyaml.NewSourceFromString(tc.input)

			got, ok := source.UTF16Column(position.New(tc.line, tc.col))
			assert.Equal(t, tc.ok, ok)
			assert.Equal(t, tc.want, got)
		})
	}

	t.Run("nil Source", func(t *testing.T) {
		t.Parallel()

		var source *niceyaml.Source

		got, ok := source.UTF16Column(position.New(0, 0))
		assert.False(t, ok)
		assert.Zero(t, got)
	})
}

func TestSource_PositionAtUTF16(t *testing.T) {
	t.Parallel()

	tags := `tags: ["😀", 2]` + "\n"

	tcs := map[string]struct {
		input string
		line  int
		col   int
		want  position.Position
		ok    bool
	}{
		"start of the text": {
			input: "a: 1\n",
			ok:    true,
		},
		"unit after a rune of two units": {
			input: tags,
			col:   13,
			want:  position.New(0, 12),
			ok:    true,
		},
		"end of a range after a rune of two units": {
			input: tags,
			col:   14,
			want:  position.New(0, 13),
			ok:    true,
		},
		"first unit of a surrogate pair": {
			input: "a: 😀\n",
			col:   3,
			want:  position.New(0, 3),
			ok:    true,
		},
		"second unit of a surrogate pair names its rune": {
			input: "a: 😀\n",
			col:   4,
			want:  position.New(0, 3),
			ok:    true,
		},
		"unit past the content names the line ending": {
			input: "a: 😀\n",
			col:   5,
			want:  position.New(0, 4),
			ok:    true,
		},
		"unit past a line ending": {
			input: "a: 😀\n",
			col:   6,
			want:  position.New(0, 5),
			ok:    true,
		},
		"unit past the column past a line ending": {
			input: "a: 😀\n",
			col:   7,
		},
		"later line": {
			input: stringtest.LinesCRLF("😀: 1", "b: 😀"),
			line:  1,
			col:   5,
			want:  position.New(1, 4),
			ok:    true,
		},
		"end of a text with no final line ending": {
			input: "a: 😀",
			col:   5,
			want:  position.New(0, 4),
			ok:    true,
		},
		"past the end of a text with no final line ending": {
			input: "a: 😀",
			col:   6,
		},
		"byte order mark lies before unit 0": {
			input: "\ufeffa: 😀\n",
			col:   5,
			want:  position.New(0, 4),
			ok:    true,
		},
		"invalid UTF-8 byte takes one unit": {
			input: "a: \"\xff😀\"\n",
			col:   7,
			want:  position.New(0, 6),
			ok:    true,
		},
		"empty text": {
			input: "",
			ok:    true,
		},
		"line past the last": {
			input: "a: 1\n",
			line:  2,
		},
		"negative line": {
			input: "a: 1\n",
			line:  -1,
		},
		"negative column": {
			input: "a: 1\n",
			col:   -1,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			source := niceyaml.NewSourceFromString(tc.input)

			got, ok := source.PositionAtUTF16(tc.line, tc.col)
			assert.Equal(t, tc.ok, ok)
			assert.Equal(t, tc.want, got)
		})
	}

	t.Run("nil Source", func(t *testing.T) {
		t.Parallel()

		var source *niceyaml.Source

		got, ok := source.PositionAtUTF16(0, 0)
		assert.False(t, ok)
		assert.Equal(t, position.Position{}, got)
	})
}

func TestSource_UTF16Column_PositionAtUTF16(t *testing.T) {
	t.Parallel()

	tcs := map[string]string{
		"LF":           stringtest.LinesLF("a: 1", `b: "😀é"`, "c: 日本 # 😀😀"),
		"CRLF":         stringtest.LinesCRLF("a: 1", `b: "😀é"`, "c: 日本 # 😀😀"),
		"bare CR":      "a: 😀\rb: é\rc: 日本\r",
		"no final LF":  "a: 1\nb: 😀",
		"mark":         "\ufeffa: 😀\n\ufeff---\nb: 😀\n",
		"kept mark":    "a: 1\n\ufeffb: 😀\n",
		"invalid byte": "a: \xff😀\nb: 2\n",
	}

	for name, input := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			source := niceyaml.NewSourceFromString(input)
			text := source.Text()

			var count int

			for pos := range source.Lines().Runes() {
				// The column in units counts the text in front of the
				// position on its line, from column 0.
				start, ok := source.Offset(position.New(pos.Line, 0))
				require.True(t, ok, "position %s", pos)

				off, ok := source.Offset(pos)
				require.True(t, ok, "position %s", pos)

				units, ok := source.UTF16Column(pos)
				require.True(t, ok, "position %s", pos)
				assert.Len(t, utf16.Encode([]rune(text[start:off])), units, "position %s", pos)

				// Each unit of the rune comes back as its position.
				r, _ := utf8.DecodeRuneInString(text[off:])
				for i := range max(utf16.RuneLen(r), 1) {
					got, ok := source.PositionAtUTF16(pos.Line, units+i)
					require.True(t, ok, "position %s unit %d", pos, i)
					assert.Equal(t, pos, got, "position %s unit %d", pos, i)
				}

				count++
			}

			assert.Positive(t, count)
		})
	}
}
