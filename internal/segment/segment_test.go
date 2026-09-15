package segment_test

import (
	"testing"

	"github.com/goccy/go-yaml/token"
	"github.com/stretchr/testify/assert"

	"go.jacobcolvin.com/niceyaml/internal/segment"
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
	})
}

func TestSegment_Contains(t *testing.T) {
	t.Parallel()

	source := &token.Token{Origin: "a\nb\n"}
	part := &token.Token{Origin: "a\n"}
	other := &token.Token{Origin: "c"}

	seg := segment.New(source, part)

	assert.True(t, seg.Contains(source))
	assert.True(t, seg.Contains(part))
	assert.False(t, seg.Contains(other))
	assert.False(t, seg.Contains(nil))
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

func TestSegments_LastColumn(t *testing.T) {
	t.Parallel()

	at := func(col int) segment.Segment {
		return segment.New(nil, &token.Token{Origin: "x", Position: &token.Position{Column: col}})
	}

	assert.Equal(t, 0, segment.Segments(nil).LastColumn())
	assert.Equal(t, 7, segment.Segments{at(1), at(7), at(4)}.LastColumn())
	assert.Equal(t, 0, segment.Segments{segment.New(nil, &token.Token{Origin: "x"})}.LastColumn())
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
