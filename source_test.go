package niceyaml_test

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
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
	"unicode/utf8"

	"github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/lexer"
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

			view := niceyaml.NewSourceFromTokens(lexer.Tokenize(tc.input), niceyaml.WithName("test")).View()

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

// runePositions returns each rune that lines yields over ranges, with its
// position, in order.
func runePositions(lines line.Lines, ranges ...position.Range) []runePosition {
	var got []runePosition

	for pos, r := range lines.Runes(ranges...) {
		got = append(got, runePosition{R: r, Pos: pos})
	}

	return got
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
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			source := niceyaml.NewSourceFromTokens(lexer.Tokenize(tc.input))

			assert.Equal(t, tc.want, runePositions(source.Lines()))
		})
	}
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
	lines := diff.Diff(before.Lines(), after.Lines()).Unified()
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

func TestSource_All_EarlyBreak(t *testing.T) {
	t.Parallel()

	source := niceyaml.NewSourceFromTokens(lexer.Tokenize(stringtest.LinesLF(
		"a: 1",
		"b: 2",
		"c: 3",
	)))

	var collected []int

	for idx := range source.Lines().All() {
		collected = append(collected, idx)
		if idx >= 1 {
			break
		}
	}

	assert.Equal(t, []int{0, 1}, collected)
}

func TestSource_All_WithSpans(t *testing.T) {
	t.Parallel()

	source := niceyaml.NewSourceFromString(sourceFiveLines)

	require.Equal(t, 5, source.Lines().Len())

	tcs := map[string]struct {
		spans []position.Span
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
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var got []int

			for idx := range source.Lines().All(tc.spans...) {
				got = append(got, idx)
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
		"mapping": {input: "key: value\nfoo: bar\n"},
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
			lines := src.Lines()

			require.Equal(t, len(tc.numbers), lines.Len())

			for i, want := range tc.numbers {
				assert.Equal(t, want, lines.Line(i).Number(), "line %d", i)
			}

			assert.Equal(t, tc.want, yamltest.Document(t, src).Span())
		})
	}
}

func TestSource_Runes_EarlyBreak(t *testing.T) {
	t.Parallel()

	source := niceyaml.NewSourceFromTokens(lexer.Tokenize("abc\n"))

	var collected []rune

	for _, r := range source.Lines().Runes() {
		collected = append(collected, r)
		if r == 'b' {
			break
		}
	}

	assert.Equal(t, []rune{'a', 'b'}, collected)
}

func TestSource_Runes_WithRanges(t *testing.T) {
	t.Parallel()

	source := niceyaml.NewSourceFromString(sourceFiveLines)

	require.Equal(t, 5, source.Lines().Len())

	tcs := map[string]struct {
		ranges []position.Range
		want   []runePosition
	}{
		// The range covers "b: 2" on line 1, and leaves its end out.
		"single range filters runes correctly": {
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
			ranges: []position.Range{yamltest.LineRange(100, 0, 10)},
		},
		"multiple ranges": {
			ranges: []position.Range{
				yamltest.LineRange(0, 0, 1),
				yamltest.LineRange(2, 0, 1),
			},
			want: []runePosition{
				{R: 'a', Pos: position.New(0, 0)},
				{R: 'c', Pos: position.New(2, 0)},
			},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, runePositions(source.Lines(), tc.ranges...))
		})
	}

	t.Run("no range and a range past both ends return all runes", func(t *testing.T) {
		t.Parallel()

		all := runePositions(source.Lines())
		require.NotEmpty(t, all)
		assert.Equal(t, runePosition{R: 'a', Pos: position.New(0, 0)}, all[0])

		rng := position.NewRange(position.New(-5, 0), position.New(100, 100))
		assert.Equal(t, all, runePositions(source.Lines(), rng))
	})

	t.Run("empty source returns nothing", func(t *testing.T) {
		t.Parallel()

		rng := yamltest.LineRange(0, 0, 5)
		assert.Nil(t, runePositions(niceyaml.NewSourceFromString("").Lines(), rng))
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
		"blank line with a tab before the first key":  {input: " \t\na: 1\n"},
		"blank line of a tab before the first key":    {input: "\t\na: 1\n"},
		"space before a colon after a quoted key":     {input: `{"a" : 1, "b" : "x"}` + "\n"},
		"trailing spaces":                             {input: "a: 1   \nb: 2\n"},
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

			keyTk, err := paths.NewResolver(doc.DocumentAST()).Token(paths.Current().Child("a").Key())
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
		tk, err := paths.NewResolver(doc.DocumentAST()).Token(paths.Current().Child("other"))
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
		keyTk, err := paths.NewResolver(doc.DocumentAST()).Token(paths.Current().Child("other").Key())
		require.NoError(t, err)
		assert.Equal(t, position.Ranges{
			yamltest.LineRange(3, 0, 5),
		}, source.Lines().ContentRanges(keyTk))

		blockTk, err := paths.NewResolver(doc.DocumentAST()).Token(paths.Current().Child("key"))
		require.NoError(t, err)
		assert.Equal(t, position.Ranges{
			yamltest.LineRange(0, 5, 6),
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
	full := niceyaml.NewSourceFromString(stringtest.LinesLF(
		"a: 1",
		"---",
		"b: 2",
		"c: 3",
		"d: 4",
	))

	docs := full.Documents()
	require.Len(t, docs, 2)

	source := niceyaml.NewSourceFromTokens(docs[1].Tokens())
	view := source.Lines()
	require.Equal(t, 4, view.Len())
	assert.Equal(t, 1, view.Line(0).Number())
	assert.Equal(t, stringtest.JoinLF("---", "b: 2", "c: 3", "d: 4"), view.Content())

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

		bound := yamltest.RequireSourceError(t, err)

		rng, ok := bound.Range()
		require.True(t, ok)
		assert.Equal(t, yamltest.LineRange(1, 3, 4), rng)

		got := stringtest.TrimLineEnds(render(bound))
		assert.Contains(t, got, "<genericError>2</genericError>")
		assert.NotContains(t, got, "<genericError>3</genericError>")
	})

	t.Run("token error reports the renumbered line", func(t *testing.T) {
		t.Parallel()

		tk := view.TokenAt(position.New(2, 0))
		require.NotNil(t, tk)

		err := yamltest.Bind(t, source, niceyaml.NewError("bad c", niceyaml.AtPosition(position.NewFromToken(tk))))
		assert.Equal(t, "3:1: bad c", err.Error())

		got := stringtest.TrimLineEnds(render(err))
		assert.Contains(t, got, "<genericError>c</genericError>")
		assert.NotContains(t, got, "<genericError>b</genericError>")
	})

	t.Run("token from the whole file is out of range past the last line", func(t *testing.T) {
		t.Parallel()

		last := full.Lines().TokenAt(position.New(4, 0))
		require.NotNil(t, last)

		bound := yamltest.RequireSourceError(t,
			yamltest.Bind(t, source, niceyaml.NewError("bad d", niceyaml.AtPosition(position.NewFromToken(last)))))

		err := bound.Unresolved()
		require.ErrorIs(t, err, niceyaml.ErrOutOfRange)
		assert.Equal(t, "location outside source: line 5 not in lines 1-4", err.Error())
	})
}

func TestNewSourceFromTokens_WhitespaceDocument(t *testing.T) {
	t.Parallel()

	// A document of whitespace alone cut after "..." counts its lines from
	// 1, as a document with text does.
	tcs := map[string]string{
		"between document end and header": stringtest.LinesLF(
			"a: 1",
			"...",
			"\t",
			"---",
			"b: 2",
		),
		"after document end at the end": stringtest.LinesLF("a: 1", "...", "\t"),
	}

	for name, input := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var doc token.Tokens

			for i, d := range tokens.SplitDocuments(tokens.Tokenize(input)) {
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

			assert.Equal(t, []int{1, 2}, got)
		})
	}
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

			docs := source.Documents()
			require.Len(t, docs, 2)

			got, err := docs[1].Decode[map[string]any](t.Context())
			require.NoError(t, err)
			assert.Equal(t, map[string]any{"b": uint64(2)}, got)

			// The positions count in the text without the marks, so an
			// error at the value of b points at it.
			err = docs[1].Bind(niceyaml.NewError("bad", niceyaml.AtPath(paths.Current().Child("b"))))
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
		"LF":                        "a: 1\nb: 2\n",
		"no final line ending":      "a: 1\nb: 2",
		"CRLF":                      "a: 1\r\nb: 2\r\n",
		"bare CR":                   "a: 1\rb: 2\r",
		"trailing blank lines":      stringtest.LinesLF("a: 1", "", ""),
		"byte order mark":           "\ufeffa: 1\n",
		"mark at a document marker": sourceMarkedHeader,
		"invalid UTF-8":             "a: \"x\xffy\"\n",
		"tag that ends its line":    "a: !t\nb: 1\n",
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

			fsys := fstest.MapFS{"a.yaml": &fstest.MapFile{Data: []byte(text)}}

			fromFS, err := niceyaml.NewSourceFromFS(fsys, "a.yaml")
			require.NoError(t, err)
			assert.Equal(t, text, fromFS.Text())

			path := filepath.Join(t.TempDir(), "a.yaml")
			require.NoError(t, os.WriteFile(path, []byte(text), 0o600))

			fromFile, err := niceyaml.NewSourceFromFile(path)
			require.NoError(t, err)
			assert.Equal(t, text, fromFile.Text())
		})
	}
}

func TestSource_Offset(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input string
		line  int
		col   int
		want  int
		ok    bool
	}{
		"start of the text": {
			input: "a: 1\nb: 2\n",
			want:  0,
			ok:    true,
		},
		"column of a later line": {
			input: "a: 1\nb: 2\n",
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
			input: "a: 1\nb: 2\n",
			line:  0,
			col:   4,
			want:  4,
			ok:    true,
		},
		"column past a LF is the next line": {
			input: "a: 1\nb: 2\n",
			line:  0,
			col:   5,
			want:  5,
			ok:    true,
		},
		"CRLF takes one column": {
			input: "a: 1\r\nb: 2\r\n",
			line:  0,
			col:   4,
			want:  4,
			ok:    true,
		},
		"column past a CRLF is the next line": {
			input: "a: 1\r\nb: 2\r\n",
			line:  0,
			col:   5,
			want:  6,
			ok:    true,
		},
		"line after a CRLF": {
			input: "a: 1\r\nb: 2\r\n",
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
			input: "a: 1\nb: 2\n",
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
			input: "a: !t\nb: 1\n",
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

	full, err := os.ReadFile(filepath.Join("testdata", "full.yaml"))
	require.NoError(t, err)

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
		"byte order mark":           "\ufeff# c\na: 1\n",
		"mark at a document marker": sourceMarkedHeader,
		"wide runes":                "é: \"日本語\" # 😀\nb: 2\n",
		"invalid UTF-8":             "a: \"x\xffy\"\nb: 2\n",
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
		"full.yaml": string(full),
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

func TestNewSourceFromTokens_Text(t *testing.T) {
	t.Parallel()

	// A Source built from tokens reads no text, so it has none to return
	// and no offset to count in it.
	source := niceyaml.NewSourceFromTokens(tokens.Tokenize("a: 1\n"))
	assert.Equal(t, "a: 1", source.Lines().Content())
	assert.Empty(t, source.Text())

	_, ok := source.Offset(position.New(0, 0))
	assert.False(t, ok)
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
				input: stringtest.LinesLF(
					"%TAG !! tag:example.com,2000:app/",
					"---",
					"!!int 1 - 3",
					"---",
					"- !!str",
				),
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
			// The go-yaml parser returns one empty document for an empty
			// input.
			"empty source": {
				input: "",
				want:  1,
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				file, err := niceyaml.NewSourceFromString(tc.input).File()
				require.NoError(t, err)
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
				assert.Len(t, source.AllDocuments(), tc.want)
			})
		}
	})

	t.Run("invalid YAML returns Error", func(t *testing.T) {
		t.Parallel()

		// Create invalid YAML by manually constructing malformed tokens.
		tkb := yamltest.NewTokenBuilder().Type(token.MappingValueType).Value(":").PositionLine(1)
		tks := token.Tokens{}
		tks.Add(tkb.Clone().Origin(":").PositionColumn(1).Build())
		tks.Add(tkb.Clone().Origin(":\n").PositionColumn(2).Build())

		file, err := niceyaml.NewSourceFromTokens(tks).File()
		assert.Nil(t, file)

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

		excerpt, ok := bound.Excerpt()
		require.True(t, ok)

		detail := yamltest.NewXMLPrinter().Print(excerpt)
		assert.Contains(t, detail, "<genericError>b</genericError>", "the offending token is highlighted")

		// The document returns the same bound error.
		docs := source.Documents()
		require.Len(t, docs, 1)
		assert.Same(t, bound, docs[0].Err())
	})

	t.Run("parser panic comes back as an error on every call", func(t *testing.T) {
		t.Parallel()

		// The go-yaml parser dereferences the position of this token.
		tks := lexer.Tokenize("a: 1\nb: 2\n")
		tks[2].Position = nil

		source := niceyaml.NewSourceFromTokens(tks)

		file, err := source.File()
		require.NotErrorIs(t, err, niceyaml.ErrSyntax)
		require.EqualError(t, err,
			"1:1: parser panicked: runtime error: invalid memory address or nil pointer dereference")
		assert.Nil(t, file)

		// The panic is a fault of the code that built the tokens, so the
		// error holds its stack and the document is not at fault.
		value, stack := requirePanic(t, err)
		assert.Contains(t, stack, "go-yaml/parser")

		_, ok := value.(runtime.Error)
		assert.True(t, ok, "got %T", value)

		bound := yamltest.RequireSourceError(t, err)
		assert.Same(t, source, bound.Source())

		file, again := source.File()
		assert.Nil(t, file)
		assert.Same(t, err, again)

		docs := source.Documents()
		require.Len(t, docs, 1)
		assert.Same(t, err, docs[0].Err())

		docs = source.AllDocuments()
		require.Len(t, docs, 1)
		assert.Same(t, err, docs[0].Err())

		_, again = source.Document()
		assert.Same(t, err, again)
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
			input: "a: 1\na: 2\n",
			want:  []bool{true},
		},
		"duplicate key the source allows": {
			input: "a: 1\na: 2\n",
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

			all := source.AllDocuments()
			require.Len(t, all, len(tc.want))

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

func TestSource_AllDocuments(t *testing.T) {
	t.Parallel()

	t.Run("file that parses holds the Nodes Documents returns", func(t *testing.T) {
		t.Parallel()

		source := niceyaml.NewSourceFromString(stringtest.LinesLF("a: 1", "---", "b: 2"))

		docs := source.Documents()
		require.Len(t, docs, 2)

		all := source.AllDocuments()
		require.Len(t, all, 2)

		for i, doc := range docs {
			assert.Same(t, doc, all[i])
		}

		// Every call returns a slice of its own.
		all[1] = nil

		assert.Same(t, docs[1], source.AllDocuments()[1])
	})

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
		docs := source.Documents()
		require.Len(t, docs, 4)
		assert.Same(t, fileErr, docs[2].Err())

		all := source.AllDocuments()
		require.Len(t, all, 4)

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

		all := source.AllDocuments()
		require.Len(t, all, 2)

		require.ErrorIs(t, all[0].Err(), niceyaml.ErrSyntax)
		assert.Same(t, all[0].Err(), all[1].Err())

		// ValidateDocuments reports the error both documents return once.
		assert.Same(t, all[0].Err(), source.ValidateDocuments(t.Context(), nil))

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

					rng, ok := bound.Range()
					require.True(t, ok)
					assert.True(t, doc.Span().Contains(rng.Start.Line))

					got = append(got, doc.DocumentIndex())
				}

				assert.Equal(t, tc.want, got)
			})
		}
	})

	t.Run("a parser panic with no location is bound to the first document it fails", func(t *testing.T) {
		t.Parallel()

		// The parser panics on a token with no position, and no token of
		// the run has one to locate the error at. The header follows an
		// anchor with no value, so both documents parse together.
		tks := lexer.Tokenize(stringtest.LinesLF("a: &x", "---", "b: 1"))
		for _, tk := range tks {
			tk.Position = nil
		}

		source := niceyaml.NewSourceFromTokens(tks, niceyaml.WithName("f.yaml"))

		all := source.AllDocuments()
		require.Len(t, all, 2)
		requirePanic(t, all[0].Err())
		assert.Same(t, all[0].Err(), all[1].Err())

		bound := yamltest.RequireSourceError(t, all[1].Err())
		assert.Same(t, all[0], yamltest.BoundDocument(t, bound))

		_, ok := bound.Position()
		assert.False(t, ok)

		// No position says which document the error is about, so the
		// message names the document.
		assert.True(t,
			strings.HasPrefix(bound.Error(), "f.yaml: document 1: parser panicked:"),
			bound.Error(),
		)
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

			docs := source.AllDocuments()
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

			// File returns one syntax error as it is, and several joined
			// in file order.
			file, err := source.File()
			require.Error(t, err)
			assert.Nil(t, file)

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

			docs := niceyaml.NewSourceFromString(tc.input).AllDocuments()
			require.Len(t, docs, len(tc.want))

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

			docs := source.AllDocuments()
			require.Len(t, docs, len(tc.want))

			var all token.Tokens

			for i, doc := range docs {
				assert.Equal(t, stringtest.JoinLF(tc.want[i]...), doc.View().Held().Content())

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
// name key at the root of the document, and counts its calls in calls.
func requireName(calls *atomic.Int32) niceyaml.Validator {
	return niceyaml.ValidatorFunc(func(_ context.Context, n *niceyaml.Node) error {
		calls.Add(1)

		_, err := n.At(paths.Current().Child("name"))
		if err != nil {
			return niceyaml.NewError("name is required", niceyaml.AtPath(paths.Current()))
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
			input: "a: 1\n---\n",
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
			input: "a: 1\n--- ~\n",
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
			input: "---\n---\n",
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

			all := source.AllDocuments()
			require.Len(t, all, tc.all)

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
		"empty document beside one with content": {input: "name: x\n---\n"},
		"empty documents alone": {
			input: "---\n---\n",
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

			for b := range niceyaml.Bindings(err) {
				assert.Same(t, source, b.Source())
			}

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

	t.Run("a nil validator returns the error File returns", func(t *testing.T) {
		t.Parallel()

		for _, input := range []string{
			stringtest.LinesLF("name: a", "---", "name: b"),
			"a: [\n",
			stringtest.LinesLF(
				"a: [",
				"---",
				"b: 1",
				"---",
				"c: [",
			),
			stringtest.LinesLF("a: &x", "---", "b: ["),
			stringtest.LinesLF(
				"a: &x",
				"---",
				"b: [",
				"---",
				"c: 1",
				"---",
				"d: @x",
			),
			"...\n",
		} {
			source := niceyaml.NewSourceFromString(input, niceyaml.WithName("f.yaml"))

			_, fileErr := source.File()

			// A nil pointer to a fieldValidator panics if it runs.
			for _, v := range []niceyaml.Validator{nil, (*fieldValidator)(nil), niceyaml.ValidatorFunc(nil)} {
				err := source.ValidateDocuments(t.Context(), v)
				assert.Equal(t, fileErr, err, input)
			}
		}
	})

	t.Run("the validator decides how many violations a document reports", func(t *testing.T) {
		t.Parallel()

		namePath := paths.Current().Child("name")

		// Rejects returns a validator that reports msg at the name of a
		// document whose name is one of bad.
		rejects := func(msg string, bad ...string) niceyaml.Validator {
			return niceyaml.ValidatorFunc(func(ctx context.Context, n *niceyaml.Node) error {
				name, err := n.DecodeAt[string](ctx, namePath)
				if err != nil {
					return err
				}

				if slices.Contains(bad, name) {
					return niceyaml.NewError(msg, niceyaml.AtPath(namePath))
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
			"nil reports the syntax alone": {
				v:    nil,
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
			input: "kind: Pod\n---\n",
		},
		"registry, trailing header": {
			v:     registry,
			input: "kind: Pod\n---\n",
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
			input:  "---\n---\n",
			strict: []string{"f.yaml:1:1: " + notNull},
		},
		"registry, headers alone": {
			v:      registry,
			input:  "---\n---\n",
			strict: []string{"f.yaml: document 1: " + noMatch},
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
			input:  "kind: Pod\n--- null\n",
			strict: []string{"f.yaml:2:5: " + notNull},
			want:   []string{"f.yaml:2:5: " + notNull},
		},
		"registry, explicit null": {
			v:      registry,
			input:  "kind: Pod\n--- null\n",
			strict: []string{"f.yaml: document 2: " + noMatch},
			want:   []string{"f.yaml: document 2: " + noMatch},
		},
		"schema, alias with no anchor": {
			v:      manifest,
			input:  "kind: Pod\n--- *nope\n",
			strict: []string{`f.yaml:2:5: $: could not find alias "nope"`},
			want:   []string{`f.yaml:2:5: $: could not find alias "nope"`},
		},
		"registry, alias with no anchor": {
			v:     registry,
			input: "kind: Pod\n--- *nope\n",
			strict: []string{
				"f.yaml: document 2: resolve $.kind: alias does not resolve: *nope has no anchor before it",
			},
			want: []string{
				"f.yaml: document 2: resolve $.kind: alias does not resolve: *nope has no anchor before it",
			},
		},
		// The message counts every document of the file, so the one below
		// an empty document keeps its number.
		"registry, document below an empty one": {
			v:      registry,
			input:  spaced,
			strict: []string{"f.yaml: document 3: " + noMatch},
			want:   []string{"f.yaml: document 3: " + noMatch},
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
			strict: []string{"f.yaml:3:7: " + unclosedSequence, "f.yaml: document 4: " + noMatch},
			want:   []string{"f.yaml:3:7: " + unclosedSequence, "f.yaml: document 4: " + noMatch},
		},
		// A document that did not parse is not empty, so the empty one
		// beside it passes.
		"schema, empty document beside one that did not parse": {
			v:      manifest,
			input:  "kind: [\n---\n",
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
			"trailing header":             {input: "kind: Pod\n---\n", want: []string{"Pod"}},
			"comment after a manifest":    {input: sourced, want: []string{"A"}},
			"chart output":                {input: helm, want: []string{"ConfigMap", "Service"}},
			"document below an empty one": {input: spaced, want: []string{"A", "B"}},
			"headers alone":               {input: "---\n---\n", want: []string{""}},
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

				docs := source.AllDocuments()
				require.Len(t, docs, 2)
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

		bound := yamltest.RequireSourceError(t, err)
		assert.Same(t, source, bound.Source())

		// With a nil validator, the ended ctx still fails the call.
		err = source.ValidateDocuments(ctx, nil)
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
				want:   []string{"f.yaml:1:1: $: bad", "f.yaml: document 2: context canceled"},
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

					return niceyaml.NewError("bad", niceyaml.AtPath(paths.Current()))
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
		"excerpts": {
			read: func(s *niceyaml.Source) any { return s.ShowsExcerpts() },
			want: true,
		},
		"text": {
			read: func(s *niceyaml.Source) any { return s.Text() },
			want: "",
		},
		"offset": {
			read: func(s *niceyaml.Source) any {
				_, ok := s.Offset(position.New(0, 0))

				return ok
			},
			want: false,
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
	view.Annotate(0, line.Annotation{Content: "note", Placement: line.Below})

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
				want:       "two.yaml: document 2: bad",
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

		docs := broken.AllDocuments()
		require.Len(t, docs, 3)
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

		err := one.Bind(niceyaml.NewError("bad", niceyaml.AtPath(paths.Current().Child("b"))))

		bound := yamltest.RequireSourceError(t, err)
		assert.Equal(t, "one.yaml:3:4: $.b: bad", err.Error())
		assert.Same(t, doc, yamltest.BoundDocument(t, bound))

		got, ok := bound.Range()
		require.True(t, ok)
		assert.Equal(t, yamltest.LineRange(2, 3, 5), got)

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
		doc := yamltest.Document(t, one)

		err := one.Bind(niceyaml.NewError("bad", niceyaml.AtPath(paths.Current().Child("zz").Index(0))))

		bound := yamltest.RequireSourceError(t, err)
		assert.Equal(t, "one.yaml: $.zz[0]: bad", err.Error())
		assert.Same(t, doc, yamltest.BoundDocument(t, bound))

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
			stringtest.LinesLF(
				"tags: [a, b]",
				"",
				"? key",
				": value",
				"port: http",
			),
			niceyaml.WithName("f.yaml"),
		)

		err := keyed.Bind(niceyaml.NewError("bad port", niceyaml.AtPath(paths.Current().Child("port"))))

		bound := yamltest.RequireSourceError(t, err)
		assert.Equal(t, "f.yaml:5:7: $.port: bad port", err.Error())

		got, ok := bound.Range()
		require.True(t, ok)
		assert.Equal(t, yamltest.LineRange(4, 6, 10), got)

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

	t.Run("path error resolves in the one document beside an empty one", func(t *testing.T) {
		t.Parallel()

		// The empty documents do not count, so the path resolves in the
		// document with content, wherever it sits in the file.
		for input, want := range map[string]string{
			"b: 2\n---\n":                            "one.yaml:1:4: $.b: bad",
			stringtest.LinesLF("---", "---", "b: 2"): "one.yaml:3:4: $.b: bad",
		} {
			src := niceyaml.NewSourceFromString(input, niceyaml.WithName("one.yaml"))
			err := src.Bind(niceyaml.NewError("bad", niceyaml.AtPath(paths.Current().Child("b"))))

			doc := yamltest.Document(t, src)

			bound := yamltest.RequireSourceError(t, err)
			assert.Equal(t, want, err.Error())
			assert.Same(t, doc, yamltest.BoundDocument(t, bound), input)
			require.NoError(t, bound.Unresolved(), input)
		}
	})

	t.Run("path error in a stream of markers alone binds as in an empty file", func(t *testing.T) {
		t.Parallel()

		// A stream of "..." markers alone holds one empty document, as an
		// empty file does, so a path resolves in that document and finds
		// nothing there.
		for _, input := range []string{"...\n", ""} {
			src := niceyaml.NewSourceFromString(input, niceyaml.WithName("none.yaml"))
			err := src.Bind(niceyaml.NewError("bad", niceyaml.AtPath(paths.Current().Child("b"))))

			doc := yamltest.Document(t, src)

			bound := yamltest.RequireSourceError(t, err)
			assert.Same(t, doc, yamltest.BoundDocument(t, bound), input)

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
			niceyaml.NewError("bad a", niceyaml.AtPath(paths.Current().Child("a"))),
			niceyaml.NewError("bad b", niceyaml.AtPath(paths.Current().Child("b"))),
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

		bound := docs[1].Bind(niceyaml.NewError("bad", niceyaml.AtPath(paths.Current().Child("b"))))
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

		_, err := niceyaml.NewSourceFromReader(iotest.ErrReader(errReadFailed))
		require.ErrorIs(t, err, errReadFailed)
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
	file := filepath.Join(dir, "configs", "app.yaml")

	require.NoError(t, os.Mkdir(filepath.Dir(file), 0o700))
	require.NoError(t, os.WriteFile(file, []byte("key: value\n"), 0o600))

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

	//nolint:paralleltest // See above.
	t.Run("reports a missing file", func(t *testing.T) {
		_, err := niceyaml.NewSourceFromFile("configs/missing.yaml")
		require.ErrorIs(t, err, fs.ErrNotExist)
	})
}

func TestSource_FS(t *testing.T) {
	t.Parallel()

	const text = "key: value\n"

	// A pointer compares by identity, so the test tells this file system
	// from any other.
	bundle := &fstest.MapFS{"configs/app.yaml": &fstest.MapFile{Data: []byte(text)}}

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

	fsys := fstest.MapFS{
		"configs/app.yaml": &fstest.MapFile{Data: []byte("key: value\n")},
	}

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

	t.Run("decodes the one document beside an empty one", func(t *testing.T) {
		t.Parallel()

		for _, input := range []string{
			"name: a\n---\n",
			stringtest.LinesLF("---", "# head", "---", "name: a"),
		} {
			source := niceyaml.NewSourceFromString(input)

			got, err := source.Decode[config](t.Context())
			require.NoError(t, err, input)
			assert.Equal(t, config{Name: "a"}, got, input)

			cfg := config{Name: "default"}
			require.NoError(t, source.DecodeInto(t.Context(), &cfg), input)
			assert.Equal(t, config{Name: "a"}, cfg, input)
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
		require.Error(t, err)

		bound := yamltest.RequireSourceError(t, err)
		assert.Same(t, source, bound.Source())
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
				input: "name: 1\nserver: *base\n",
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

		docs := src.Documents()
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

		list := niceyaml.NewSourceFromString("x: &x 1\nlist: &list [*x]\n")

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
				input: stringtest.LinesLF("x: &x 9", "v: *list", "w: [*x]"),
				want:  target{X: 9, V: []int{1}, W: []int{9}},
			},
			"two anchors of the document": {
				ref:   list,
				input: "w: [&x 8, *x, &x 9, *x]\nv: *list\n",
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
					niceyaml.NewSourceFromString(stringtest.LinesLF(
						"x: &x 1",
						"list: &list [*x]",
						"y: &x 2",
					)),
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

		docs := niceyaml.NewSourceFromString(
			stringtest.LinesLF("b: *x", "---", "c: *x"),
			niceyaml.WithReferences(niceyaml.NewSourceFromString("base: &x 1\n")),
		).Documents()
		require.Len(t, docs, 2)

		var wg sync.WaitGroup

		for range 4 {
			for i, doc := range docs {
				wg.Go(func() {
					got, err := doc.Decode[map[string]int](t.Context())
					if assert.NoError(t, err) {
						assert.Equal(t, map[string]int{[]string{"b", "c"}[i]: 1}, got)
					}
				})
			}
		}

		wg.Wait()
	})

	t.Run("a path resolves in the document alone", func(t *testing.T) {
		t.Parallel()

		src := niceyaml.NewSourceFromString("name: app\nserver: *base\n",
			niceyaml.WithName("app.yaml"),
			niceyaml.WithReferences(defaults),
		)

		doc := yamltest.Document(t, src)

		alias := paths.Current().Child("server")
		port := alias.Child("port")

		_, atBelow := doc.At(port)
		_, atAlias := doc.At(alias)
		_, nodesBelow := doc.Nodes(alias.ChildAll())
		_, nodesAlias := doc.Nodes(alias)
		_, rangesBelow := doc.Ranges(port)
		_, matchBelow := matcher.Content(port, 8080).Match(t.Context(), doc)

		// The error of each read that stops at the alias.
		tcs := map[string]error{
			"At below the alias":              atBelow,
			"At the alias":                    atAlias,
			"Nodes below the alias":           nodesBelow,
			"Nodes at the alias":              nodesAlias,
			"Ranges below the alias":          rangesBelow,
			"Content matcher below the alias": matchBelow,
		}

		for name, err := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				require.ErrorIs(t, err, paths.ErrAlias)
				assert.False(t, niceyaml.IsInvalid(err))

				bound := yamltest.RequireSourceError(t, err)

				_, ok := bound.Position()
				assert.False(t, ok)
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
				want: "app.yaml:2:9: $.server.port: port must be at least 1",
			},
			"key the reference leaves out": {
				reference: stringtest.LinesLF(
					"base: &base",
					"  host: localhost",
				),
				want: "app.yaml:2:9: $.server.port: port must be at least 1",
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

				doc := yamltest.FirstDocument(t, "name: app\nserver: *base\n",
					niceyaml.WithName("app.yaml"),
					niceyaml.WithReferences(niceyaml.NewSourceFromString(tc.reference)),
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

				path, ok := bound.Path()
				require.True(t, ok)
				assert.Equal(t, paths.Doc().Child("server", "port"), path)
			})
		}
	})

	t.Run("error at a key beside a merge of a reference has no position", func(t *testing.T) {
		t.Parallel()

		// The merge sets no kind, so the decode keeps the misspelled one.
		// The document cannot tell, so a caret under the alias would point
		// one line below the typo.
		doc := yamltest.FirstDocument(t, "kind: Deploymnet\n<<: *base\n",
			niceyaml.WithName("app.yaml"),
			niceyaml.WithReferences(niceyaml.NewSourceFromString(stringtest.LinesLF(
				"base: &base",
				"  replicas: 1",
			))),
		)

		kinds := schema.MustCompile([]byte(`{"properties": {"kind": {"enum": ["Deployment", "Service"]}}}`))

		err := doc.Validate(t.Context(), kinds)
		require.EqualError(t, err, "app.yaml: $.kind: value does not match any enum member")
		assert.Equal(t, stringtest.JoinLF(
			"app.yaml: $.kind: value does not match any enum member",
			"",
			"no excerpt: resolve $.kind: alias does not resolve: *base has no anchor before it",
		), niceyaml.FormatError(err, niceyaml.WithContextLines(0)))

		bound := yamltest.RequireSourceError(t, err)

		_, ok := bound.Nearest()
		assert.False(t, ok)
		require.ErrorIs(t, bound.Unresolved(), paths.ErrAlias)
	})

	t.Run("references read from the files of a directory", func(t *testing.T) {
		t.Parallel()

		// A program reads each file of a directory into a Source, in the
		// order it picks, and an anchor of a later file wins a name two
		// files define.
		fsys := fstest.MapFS{
			"refs/a.yaml": &fstest.MapFile{
				Data: []byte(stringtest.LinesLF(
					"base: &base",
					"  port: 80",
				)),
			},
			"refs/b.yaml": &fstest.MapFile{
				Data: []byte(stringtest.LinesLF(
					"base: &base",
					"  port: 443",
					"name: &name api",
				)),
			},
			"refs/note.md": &fstest.MapFile{Data: []byte("no reference")},
		}

		names, err := fs.Glob(fsys, "refs/*.yaml")
		require.NoError(t, err)

		refs := make([]*niceyaml.Source, 0, len(names))

		for _, name := range names {
			ref, err := niceyaml.NewSourceFromFS(fsys, name)
			require.NoError(t, err)

			refs = append(refs, ref)
		}

		doc := yamltest.FirstDocument(t, "name: *name\nserver: *base\n", niceyaml.WithReferences(refs...))

		got, err := doc.Decode[config](t.Context(), niceyaml.WithValidator(serverSchema))
		require.NoError(t, err)
		assert.Equal(t, config{Name: "api", Server: server{Port: 443}}, got)
	})

	t.Run("reference that does not parse fails every decode", func(t *testing.T) {
		t.Parallel()

		doc := yamltest.FirstDocument(t, "a: 1\n",
			niceyaml.WithReferences(niceyaml.NewSourceFromString("x: [\n")))

		_, err := doc.Decode[map[string]int](t.Context())
		require.ErrorContains(t, err, unclosedSequence)
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
		return niceyaml.NewError("port must be at least 1", niceyaml.AtPath(paths.Current().Child("port")))
	}

	return nil
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

func TestWithAliasLimit(t *testing.T) {
	t.Parallel()

	// Each item of the bomb lists ten aliases to the item above it, so the
	// last one expands to 10^5 scalars.
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
			want: "bomb.yaml:1:1: excessive aliasing",
		},
		"decode with a schema validator": {
			read: decode(niceyaml.WithValidator(shape)),
			want: "bomb.yaml:1:1: excessive aliasing",
		},
		"schema validation": {
			read: validate(shape),
			want: "bomb.yaml:1:1: excessive aliasing",
		},
		"validation of every document": {
			read: func(ctx context.Context, n *niceyaml.Node) error {
				return n.Source().ValidateDocuments(ctx, shape)
			},
			want: "bomb.yaml:1:1: excessive aliasing",
		},
		"registry with a content matcher": {
			read:    validate(routed),
			want:    "resolve schema: bomb.yaml: excessive aliasing",
			resolve: true,
		},
		"decode with a registry with a content matcher": {
			read:    decode(niceyaml.WithValidator(routed)),
			want:    "resolve schema: bomb.yaml: excessive aliasing",
			resolve: true,
		},
		// The decode binds its refusal at the first token of the node, so
		// a scoped node names no path in front of it.
		"decode of a scoped node": {
			path: last,
			read: decode(),
			want: "bomb.yaml:7:9: excessive aliasing",
		},
		"schema validation of a scoped node": {
			path: last,
			read: validate(shape),
			want: "bomb.yaml:7:9: excessive aliasing",
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

		docs := niceyaml.NewSourceFromString(bomb+"---\n"+bomb, niceyaml.WithAliasLimit(false)).Documents()
		require.Len(t, docs, 2)

		for _, doc := range docs {
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

func TestSource_Documents(t *testing.T) {
	t.Parallel()

	source := niceyaml.NewSourceFromString(stringtest.Input(`
		---
		a: 1
		---
		b: 2
	`), niceyaml.WithFilePath("two.yaml"))

	docs := source.Documents()
	require.Len(t, docs, 2)

	second := docs[1]
	assert.Equal(t, 1, second.DocumentIndex())
	assert.Equal(t, "two.yaml", second.FilePath())
	assert.Same(t, source, second.Source())
	assert.NotNil(t, second.Tokens())

	// Every call hands out the same root Node for an index, in a slice of
	// its own.
	again := source.Documents()
	assert.Same(t, second, again[1])

	again[1] = nil

	third := source.Documents()
	assert.Same(t, second, third[1])
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
				input: "b: 2\n---\n",
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

				docs := source.Documents()
				require.Len(t, docs, 1)
				assert.Same(t, docs[0], doc)

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

				docs := source.Documents()
				require.Len(t, docs, 1)

				doc := yamltest.Document(t, source)
				assert.Same(t, docs[0], doc)

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

	t.Run("returns the parse error", func(t *testing.T) {
		t.Parallel()

		source := niceyaml.NewSourceFromString("a: [")

		_, err := source.Document()
		require.Error(t, err)

		_, fileErr := source.File()
		assert.Equal(t, fileErr, err)
	})
}
