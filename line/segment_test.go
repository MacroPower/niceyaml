package line_test

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.jacobcolvin.com/x/stringtest"

	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/line"
	"go.jacobcolvin.com/niceyaml/position"
	"go.jacobcolvin.com/niceyaml/style/kind"
)

func TestLine_Kind(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input string
		line  int
		want  []kind.Kind
	}{
		"mapping entry": {
			input: "key: value\n",
			want:  []kind.Kind{kind.NameTag, kind.PunctuationMappingValue, kind.LiteralString},
		},
		"anchor and alias take the kind of their marker": {
			input: "a: &x 1\nb: *x\n",
			line:  1,
			want:  []kind.Kind{kind.NameTag, kind.PunctuationMappingValue, kind.NameAlias, kind.NameAlias},
		},
		"key whose colon sits on the next line": {
			input: "? key\n: value\n",
			want:  []kind.Kind{kind.NameTag, kind.NameTag},
		},
		"merge key keeps its kind": {
			input: "<<: *base\n",
			want:  []kind.Kind{kind.NameAliasMerge, kind.PunctuationMappingValue, kind.NameAlias, kind.NameAlias},
		},
		"comment between explicit key and colon": {
			input: "? a # c\n: b\n",
			want:  []kind.Kind{kind.NameTag, kind.NameTag, kind.Comment},
		},
		"flow sequence key keeps its closing bracket": {
			input: "[a]: v\n",
			want: []kind.Kind{
				kind.PunctuationSequenceStart,
				kind.LiteralString,
				kind.PunctuationSequenceEnd,
				kind.PunctuationMappingValue,
				kind.LiteralString,
			},
		},
		"tag before an empty key stays a decorator": {
			input: "!!null : v\n",
			want:  []kind.Kind{kind.NameDecorator, kind.PunctuationMappingValue, kind.LiteralString},
		},
		"sequence entry before an empty key stays punctuation": {
			input: "- : v\n",
			want:  []kind.Kind{kind.PunctuationSequenceEntry, kind.PunctuationMappingValue, kind.LiteralString},
		},
		"block scalar continuation is a string": {
			input: stringtest.LinesLF(
				"text: |",
				"  hello",
			),
			line: 1,
			want: []kind.Kind{kind.LiteralString},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			lines := newLines(tc.input)
			ln := lines.Line(tc.line)

			got := make([]kind.Kind, 0, len(ln.Tokens()))
			for i := range ln.Tokens() {
				got = append(got, ln.Kind(i))
			}

			assert.Equal(t, tc.want, got)
		})
	}
}

// segmentText joins the segments of line i of view as "text" in kind,
// with the kinds of the overlays covering each in brackets, so a test
// reads the split at a glance.
func segmentText(view *line.View, i int) string {
	var parts []string

	for seg := range view.Segments(i) {
		part := string(seg.Kind) + "(" + seg.Text + ")"

		if len(seg.Overlays) > 0 {
			kinds := make([]string, 0, len(seg.Overlays))
			for _, o := range seg.Overlays {
				kinds = append(kinds, string(o.Kind))
			}

			part += "[" + strings.Join(kinds, ",") + "]"
		}

		parts = append(parts, part)
	}

	return strings.Join(parts, " ")
}

// flowMappingView returns a view of a one-line flow mapping of n entries,
// {k0: v, k1: v, ...}. With highlight set, every v has a blend overlay,
// as a search for v would add to a long minified line.
func flowMappingView(n int, highlight bool) *line.View {
	var (
		sb     strings.Builder
		ranges []position.Range
	)

	sb.WriteString("{")

	for i := range n {
		if i > 0 {
			sb.WriteString(", ")
		}

		fmt.Fprintf(&sb, "k%d: ", i)

		col := sb.Len()
		ranges = append(ranges, yamltest.LineRange(0, col, col+1))

		sb.WriteString("v")
	}

	sb.WriteString("}\n")

	view := line.NewView(newLines(sb.String()))
	if highlight {
		view.BlendOverlay("hl", ranges...)
	}

	return view
}

func TestView_Segments(t *testing.T) {
	t.Parallel()

	t.Run("splits a line into segments", func(t *testing.T) {
		t.Parallel()

		tcs := map[string]struct {
			input    string
			want     string
			overlays line.Overlays
			line     int
		}{
			"one segment per token with its whitespace in Text": {
				input: "  key: value # note\n",
				want: "text(  ) nameTag(key) punctuationMappingValue(:) text( ) " +
					"literalString(value) text( ) comment(# note)",
			},
			"an overlay splits the segment it starts or ends inside": {
				input:    "key: value\n",
				overlays: line.Overlays{{Kind: "hl", Cols: position.NewSpan(3, 7)}},
				want: "nameTag(key) punctuationMappingValue(:)[hl] text( )[hl] " +
					"literalString(va)[hl] literalString(lue)",
			},
			"overlapping overlays keep added order across many edges": {
				input: "abcdefgh\n",
				overlays: line.Overlays{
					{Kind: "w", Cols: position.NewSpan(0, 8)},
					{Kind: "b", Cols: position.NewSpan(2, 5), Blend: true},
					{Kind: "a", Cols: position.NewSpan(4, 6)},
				},
				want: "literalString(ab)[w] literalString(cd)[w,b] literalString(e)[w,b,a] " +
					"literalString(f)[w,a] literalString(gh)[w]",
			},
			"a zero-width overlay still splits but covers nothing": {
				input:    "abcdef\n",
				overlays: line.Overlays{{Kind: "z", Cols: position.NewSpan(3, 3)}},
				want:     "literalString(abc) literalString(def)",
			},
			"the indentation before a comment is Text": {
				input: stringtest.LinesLF(
					"a: 1",
					"   # c",
				),
				line: 1,
				want: "text(   ) comment(# c)",
			},
			"a line of only spaces in a block scalar is Text": {
				input: stringtest.LinesLF(
					"k: |",
					"  a",
					"    ",
					"  b",
				),
				line: 2,
				want: "text(    )",
			},
			"the line ending is not part of any segment": {
				input: "a: 1\r\nb: 2\r\n",
				want:  "nameTag(a) punctuationMappingValue(:) text( ) literalNumberInteger(1)",
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				view := newTestView(t, tc.input, strings.Count(tc.input, "\n"))
				view.AddLineOverlay(tc.line, tc.overlays...)

				assert.Equal(t, tc.want, segmentText(view, tc.line))
			})
		}
	})

	t.Run("segments cover the content once in column order", func(t *testing.T) {
		t.Parallel()

		input := stringtest.LinesLF(
			"list:",
			"  - &a [1, 2]",
			"  - *a",
		)
		view := newTestView(t, input, 3)

		for i, ln := range view.All() {
			var (
				text string
				col  int
			)

			for seg := range view.Segments(i) {
				assert.Equal(t, col, seg.Cols.Start, "line %d", i)
				assert.Len(t, []rune(seg.Text), seg.Cols.Len(), "line %d", i)
				assert.NotEmpty(t, seg.Text, "line %d", i)

				text += seg.Text
				col = seg.Cols.End
			}

			assert.Equal(t, ln.Content(), text, "line %d", i)
			assert.Equal(t, ln.Width(), col, "line %d", i)
		}
	})

	t.Run("overlays come in the order they were added", func(t *testing.T) {
		t.Parallel()

		view := newTestView(t, "abcdef\n", 1)
		view.AddOverlay("outer", yamltest.LineRange(0, 0, 6))
		view.BlendOverlay("inner", yamltest.LineRange(0, 2, 4))

		assert.Equal(t,
			"literalString(ab)[outer] literalString(cd)[outer,inner] literalString(ef)[outer]",
			segmentText(view, 0),
		)

		segs := slices.Collect(view.Segments(0))
		require.Len(t, segs, 3)
		assert.False(t, segs[1].Overlays[0].Blend)
		assert.True(t, segs[1].Overlays[1].Blend)
		assert.Equal(t, position.NewSpan(2, 4), segs[1].Cols)
	})

	t.Run("a segment's overlays are its own", func(t *testing.T) {
		t.Parallel()

		view := newTestView(t, "key: value\n", 1)
		view.AddOverlay("w", yamltest.LineRange(0, 0, 10))

		want := slices.Clone(view.Overlays(0))

		segs := slices.Collect(view.Segments(0))
		require.Greater(t, len(segs), 1)

		segs[0].Overlays[0].Kind = "changed"

		assert.Equal(t, want, view.Overlays(0))
		assert.Equal(t, kind.Kind("w"), segs[1].Overlays[0].Kind)
	})

	t.Run("many overlays on one long line", func(t *testing.T) {
		t.Parallel()

		const n = 500

		view := flowMappingView(n, true)
		ln := view.Line(0)

		var (
			text        string
			highlighted int
		)

		for seg := range view.Segments(0) {
			text += seg.Text

			if seg.Text == "v" {
				require.Len(t, seg.Overlays, 1)

				highlighted++

				continue
			}

			assert.Empty(t, seg.Overlays, "segment %q at %s", seg.Text, seg.Cols)
		}

		assert.Equal(t, ln.Content(), text)
		assert.Equal(t, n, highlighted)
	})

	t.Run("an overlay on a line the view does not hold is not asked for", func(t *testing.T) {
		t.Parallel()

		view := newTestView(t, "a: 1\nb: 2\n", 2)
		view.AddOverlay("hl", yamltest.LineRange(1, 0, 1))

		assert.Equal(t, "nameTag(a) punctuationMappingValue(:) text( ) literalNumberInteger(1)", segmentText(view, 0))
		assert.Equal(t,
			"nameTag(b)[hl] punctuationMappingValue(:) text( ) literalNumberInteger(2)",
			segmentText(view.Slice(position.NewSpan(1, 2)), 1),
		)
	})

	t.Run("an empty line has no segments", func(t *testing.T) {
		t.Parallel()

		view := newTestView(t, stringtest.LinesLF(
			"a: 1",
			"",
			"b: 2",
		), 3)

		assert.Empty(t, slices.Collect(view.Segments(1)))
	})

	t.Run("stops when the caller does", func(t *testing.T) {
		t.Parallel()

		view := newTestView(t, "key: value\n", 1)

		n := 0
		for range view.Segments(0) {
			n++

			break
		}

		assert.Equal(t, 1, n)
	})

	t.Run("panics outside the content", func(t *testing.T) {
		t.Parallel()

		view := newTestView(t, "a: 1\n", 1)

		assert.Panics(t, func() { view.Segments(1) })
	})
}
