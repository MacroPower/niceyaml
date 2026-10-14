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
		want   position.Span
	}{
		"no spaces":          {origin: "value\n", want: position.NewSpan(0, 5)},
		"leading spaces":     {origin: "  value", want: position.NewSpan(2, 7)},
		"trailing spaces":    {origin: "value   \n", want: position.NewSpan(0, 5)},
		"both sides":         {origin: " value  \r\n", want: position.NewSpan(1, 6)},
		"spaces only":        {origin: "    \n", want: position.NewSpan(4, 4)},
		"leading tab":        {origin: "\tvalue", want: position.NewSpan(1, 6)},
		"trailing tab":       {origin: "value\t\n", want: position.NewSpan(0, 5)},
		"tab inside content": {origin: "a\tb", want: position.NewSpan(0, 3)},
		"multi-byte content": {origin: " 日本 ", want: position.NewSpan(1, 3)},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, segment.New(nil, &token.Token{Origin: tc.origin}).ContentSpan())
		})
	}
}

func TestSegments_SourceTokenAt(t *testing.T) {
	t.Parallel()

	key := &token.Token{Origin: "key"}
	colon := &token.Token{Origin: ":"}
	value := &token.Token{Origin: " value\n"}
	entry := segment.Segments{
		segment.New(key, key),
		segment.New(colon, colon),
		segment.New(value, value),
	}

	// The lexer bundles the indentation of the next line into the end of a
	// plain scalar, so " x\n  " spans two lines and leaves "  " on the second.
	prev := &token.Token{Origin: " x\n  "}
	indent := &token.Token{Origin: "  "}
	other := &token.Token{Origin: "other"}
	blank := &token.Token{Origin: "  \n"}
	tabbed := &token.Token{Origin: " \t"}
	// A token that starts with a line ending leaves only that line ending
	// on the line before it.
	next := &token.Token{Origin: "\nb"}
	nl := &token.Token{Origin: "\n"}
	empty := &token.Token{}

	tcs := map[string]struct {
		segs segment.Segments
		want *token.Token
		col  int
	}{
		"first column of the first token":  {segs: entry, col: 0, want: key},
		"last column of the first token":   {segs: entry, col: 2, want: key},
		"first column of a later token":    {segs: entry, col: 3, want: colon},
		"first column of the last token":   {segs: entry, col: 4, want: value},
		"last column before a line ending": {segs: entry, col: 9, want: value},
		"column past the last token":       {segs: entry, col: 10},
		"negative column":                  {segs: entry, col: -1},
		"no segments":                      {segs: nil, col: 0},
		"indentation resolves to the next token": {
			segs: segment.Segments{segment.New(prev, indent), segment.New(other, other)},
			col:  0,
			want: other,
		},
		"last indentation column resolves to the next token": {
			segs: segment.Segments{segment.New(prev, indent), segment.New(other, other)},
			col:  1,
			want: other,
		},
		"indentation with a tab resolves to the next token": {
			segs: segment.Segments{segment.New(prev, tabbed), segment.New(other, other)},
			col:  1,
			want: other,
		},
		"indentation with nothing after it": {
			segs: segment.Segments{segment.New(prev, indent)},
			col:  0,
			want: nil,
		},
		"indentation before only the line ending of the next token": {
			segs: segment.Segments{segment.New(tabbed, tabbed), segment.New(next, nl)},
			col:  0,
			want: nil,
		},
		"indentation skips an empty token to reach the next one": {
			segs: segment.Segments{
				segment.New(prev, indent),
				segment.New(empty, empty),
				segment.New(other, other),
			},
			col:  0,
			want: other,
		},
		"whitespace with a line ending keeps its own token": {
			segs: segment.Segments{segment.New(blank, blank)},
			col:  0,
			want: blank,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Same(t, tc.want, tc.segs.SourceTokenAt(tc.col))
		})
	}
}

func TestSegments_PartTokens(t *testing.T) {
	t.Parallel()

	tk := &token.Token{Origin: "x"}
	segs := segment.Segments{segment.New(tk, tk)}

	assert.Equal(t, token.Tokens{tk}, segs.PartTokens())
	assert.Nil(t, segment.Segments(nil).PartTokens())
}
