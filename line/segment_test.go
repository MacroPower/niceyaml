package line_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.jacobcolvin.com/niceyaml/line"
	"go.jacobcolvin.com/niceyaml/position"
	"go.jacobcolvin.com/niceyaml/style/kind"
	"go.jacobcolvin.com/niceyaml/tokens"
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
			input: "text: |\n  hello\n",
			line:  1,
			want:  []kind.Kind{kind.LiteralString},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			lines := line.NewLines(tokens.Tokenize(tc.input))
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

func TestView_Segments(t *testing.T) {
	t.Parallel()

	t.Run("one segment per token with its whitespace in Text", func(t *testing.T) {
		t.Parallel()

		view := newTestView(t, "  key: value # note\n", 1)

		assert.Equal(t,
			"text(  ) nameTag(key) punctuationMappingValue(:) text( ) literalString(value) text( ) comment(# note)",
			segmentText(view, 0),
		)
	})

	t.Run("segments cover the content once in column order", func(t *testing.T) {
		t.Parallel()

		input := "list:\n  - &a [1, 2]\n  - *a\n"
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

	t.Run("an overlay splits the segment it starts or ends inside", func(t *testing.T) {
		t.Parallel()

		view := newTestView(t, "key: value\n", 1)
		view.AddOverlay("hl", position.NewRange(position.New(0, 3), position.New(0, 7)))

		assert.Equal(t,
			"nameTag(key) punctuationMappingValue(:)[hl] text( )[hl] literalString(va)[hl] literalString(lue)",
			segmentText(view, 0),
		)
	})

	t.Run("overlays come in the order they were added", func(t *testing.T) {
		t.Parallel()

		view := newTestView(t, "abcdef\n", 1)
		view.AddOverlay("outer", position.NewRange(position.New(0, 0), position.New(0, 6)))
		view.BlendOverlay("inner", position.NewRange(position.New(0, 2), position.New(0, 4)))

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
		view.AddOverlay("w", position.NewRange(position.New(0, 0), position.New(0, 10)))

		want := slices.Clone(view.Overlays(0))

		segs := slices.Collect(view.Segments(0))
		require.Greater(t, len(segs), 1)

		segs[0].Overlays[0].Kind = "changed"

		assert.Equal(t, want, view.Overlays(0))
		assert.Equal(t, kind.Kind("w"), segs[1].Overlays[0].Kind)
	})

	t.Run("an overlay on a line the view does not hold is not asked for", func(t *testing.T) {
		t.Parallel()

		view := newTestView(t, "a: 1\nb: 2\n", 2)
		view.AddOverlay("hl", position.NewRange(position.New(1, 0), position.New(1, 1)))

		assert.Equal(t, "nameTag(a) punctuationMappingValue(:) text( ) literalNumberInteger(1)", segmentText(view, 0))
		assert.Equal(t,
			"nameTag(b)[hl] punctuationMappingValue(:) text( ) literalNumberInteger(2)",
			segmentText(view.Slice(position.NewSpan(1, 2)), 1),
		)
	})

	t.Run("the indentation before a comment is Text", func(t *testing.T) {
		t.Parallel()

		view := newTestView(t, "a: 1\n   # c\n", 2)

		assert.Equal(t, "text(   ) comment(# c)", segmentText(view, 1))
	})

	t.Run("a line of only spaces in a block scalar is Text", func(t *testing.T) {
		t.Parallel()

		view := newTestView(t, "k: |\n  a\n    \n  b\n", 4)

		assert.Equal(t, "text(    )", segmentText(view, 2))
	})

	t.Run("an empty line has no segments", func(t *testing.T) {
		t.Parallel()

		view := newTestView(t, "a: 1\n\nb: 2\n", 3)

		assert.Empty(t, slices.Collect(view.Segments(1)))
	})

	t.Run("the line ending is not part of any segment", func(t *testing.T) {
		t.Parallel()

		view := newTestView(t, "a: 1\r\nb: 2\r\n", 2)

		assert.Equal(t, "nameTag(a) punctuationMappingValue(:) text( ) literalNumberInteger(1)", segmentText(view, 0))
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
