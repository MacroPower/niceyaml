package segment_test

import (
	"testing"

	"github.com/goccy/go-yaml/token"
	"github.com/stretchr/testify/assert"

	"go.jacobcolvin.com/niceyaml/internal/segment"
	"go.jacobcolvin.com/niceyaml/position"
)

func TestSegment_Width(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		origin string
		want   int
	}{
		"ascii":                    {origin: "hello", want: 5},
		"trailing newline":         {origin: "hello\n", want: 5},
		"crlf ending":              {origin: "hello\r\n", want: 5},
		"bare carriage return":     {origin: "hello\r", want: 5},
		"only newline":             {origin: "\n", want: 0},
		"only crlf":                {origin: "\r\n", want: 0},
		"multi-byte runes":         {origin: "日本語\n", want: 3},
		"emoji":                    {origin: "🎉🎊", want: 2},
		"empty":                    {origin: "", want: 0},
		"mixed content":            {origin: "key: 日本語\n", want: 8},
		"interior newline is kept": {origin: "a\nb", want: 3},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			seg := segment.New(nil, &token.Token{Origin: tc.origin})
			assert.Equal(t, tc.want, seg.Width())
		})
	}

	t.Run("nil part", func(t *testing.T) {
		t.Parallel()

		assert.Equal(t, 0, segment.New(nil, nil).Width())
		assert.Equal(t, position.Span{}, segment.New(nil, nil).ContentSpan())
	})
}

func TestSegment_Contains(t *testing.T) {
	t.Parallel()

	source := &token.Token{
		Type:     token.StringType,
		Value:    "a\nb",
		Origin:   "a\nb\n",
		Position: &token.Position{Line: 1, Column: 1, Offset: 0},
	}
	part := &token.Token{
		Type:     token.StringType,
		Value:    "a",
		Origin:   "a\n",
		Position: &token.Position{Line: 1, Column: 1, Offset: 0},
	}
	other := &token.Token{Origin: "c"}

	seg := segment.New(source, part)

	assert.True(t, seg.Contains(source))
	assert.True(t, seg.Contains(part))
	assert.False(t, seg.Contains(other))
	assert.False(t, seg.Contains(nil))

	t.Run("a copy matches by its fields", func(t *testing.T) {
		t.Parallel()

		assert.True(t, seg.Contains(source.Clone()))
		assert.True(t, seg.Contains(part.Clone()))
	})

	t.Run("a copy that differs in one field does not match", func(t *testing.T) {
		t.Parallel()

		tcs := map[string]func(*token.Token){
			"type":     func(tk *token.Token) { tk.Type = token.IntegerType },
			"value":    func(tk *token.Token) { tk.Value = "x" },
			"origin":   func(tk *token.Token) { tk.Origin = "x" },
			"line":     func(tk *token.Token) { tk.Position.Line++ },
			"column":   func(tk *token.Token) { tk.Position.Column++ },
			"offset":   func(tk *token.Token) { tk.Position.Offset++ },
			"position": func(tk *token.Token) { tk.Position = nil },
		}

		for name, change := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				copied := source.Clone()
				change(copied)

				assert.False(t, seg.Contains(copied))
			})
		}
	})

	t.Run("tokens without positions match by the rest", func(t *testing.T) {
		t.Parallel()

		bare := &token.Token{Type: token.StringType, Value: "a", Origin: "a"}
		seg := segment.New(bare, bare)

		assert.True(t, seg.Contains(bare.Clone()))
		assert.False(
			t,
			seg.Contains(&token.Token{Type: token.StringType, Value: "a", Origin: "a", Position: &token.Position{}}),
		)
	})
}

func TestSegment_ContentSpan(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		origin string
		want   [2]int
	}{
		"no spaces":          {origin: "value\n", want: [2]int{0, 5}},
		"leading spaces":     {origin: "  value", want: [2]int{2, 7}},
		"trailing spaces":    {origin: "value   \n", want: [2]int{0, 5}},
		"both sides":         {origin: " value  \r\n", want: [2]int{1, 6}},
		"spaces only":        {origin: "    \n", want: [2]int{4, 4}},
		"tabs are content":   {origin: "\tvalue", want: [2]int{0, 6}},
		"multi-byte content": {origin: " 日本 ", want: [2]int{1, 3}},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			span := segment.New(nil, &token.Token{Origin: tc.origin}).ContentSpan()
			assert.Equal(t, tc.want[0], span.Start)
			assert.Equal(t, tc.want[1], span.End)
		})
	}
}

func TestSegments_SourceTokenAt(t *testing.T) {
	t.Parallel()

	key := &token.Token{Origin: "key"}
	colon := &token.Token{Origin: ":"}
	value := &token.Token{Origin: " value\n"}

	segs := segment.Segments{
		segment.New(key, key),
		segment.New(colon, colon),
		segment.New(value, value),
	}

	assert.Same(t, key, segs.SourceTokenAt(0))
	assert.Same(t, key, segs.SourceTokenAt(2))
	assert.Same(t, colon, segs.SourceTokenAt(3))
	assert.Same(t, value, segs.SourceTokenAt(4))
	assert.Same(t, value, segs.SourceTokenAt(9))
	assert.Nil(t, segs.SourceTokenAt(10))
	assert.Nil(t, segs.SourceTokenAt(-1))
	assert.Nil(t, segment.Segments(nil).SourceTokenAt(0))
}

func TestSegments_EndColumn(t *testing.T) {
	t.Parallel()

	at := func(col int, origin string) segment.Segment {
		return segment.New(nil, &token.Token{Origin: origin, Position: &token.Position{Column: col}})
	}

	assert.Equal(t, 0, segment.Segments(nil).EndColumn())
	assert.Equal(t, 8, segment.Segments{at(1, "x"), at(7, "x"), at(4, "x")}.EndColumn())
	assert.Equal(t, 7, segment.Segments{at(1, "a"), at(2, ":"), at(4, " !t\n")}.EndColumn(),
		"a multi-rune part ends past its start column")
	assert.Equal(t, 1, segment.Segments{at(1, "\n")}.EndColumn(), "a newline has no width")
	assert.Equal(t, 0, segment.Segments{segment.New(nil, &token.Token{Origin: "x"})}.EndColumn())
}

func TestSegments_Clone(t *testing.T) {
	t.Parallel()

	tk := &token.Token{Origin: "x"}
	segs := segment.Segments{segment.New(tk, tk)}
	clone := segs.Clone()

	clone = append(clone, segment.New(tk, tk))

	assert.Len(t, segs, 1)
	assert.Len(t, clone, 2)
	assert.Same(t, tk, clone[0].Part())
	assert.Nil(t, segment.Segments(nil).Clone())
	assert.Equal(t, token.Tokens{tk}, segs.PartTokens())
	assert.Nil(t, segment.Segments(nil).PartTokens())
}
