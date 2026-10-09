package clip_test

import (
	"math"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"

	"go.jacobcolvin.com/niceyaml/internal/clip"
	"go.jacobcolvin.com/niceyaml/position"
)

const alphabet = "abcdefghijklmnopqrstuvwxyz"

func TestMap_Content(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		content string
		want    string
		windows []position.Span
		width   int
	}{
		"whole line": {
			content: alphabet,
			width:   26,
			windows: []position.Span{position.NewSpan(0, 26)},
			want:    alphabet,
		},
		"cut at the end": {
			content: alphabet,
			width:   26,
			windows: []position.Span{position.NewSpan(0, 4)},
			want:    "abcd...",
		},
		"cut at the start": {
			content: alphabet,
			width:   26,
			windows: []position.Span{position.NewSpan(20, 26)},
			want:    "...uvwxyz",
		},
		"cut at both ends": {
			content: alphabet,
			width:   26,
			windows: []position.Span{position.NewSpan(10, 14)},
			want:    "...klmn...",
		},
		"cut between two windows": {
			content: alphabet,
			width:   26,
			windows: []position.Span{position.NewSpan(0, 4), position.NewSpan(20, 26)},
			want:    "abcd...uvwxyz",
		},
		"touching windows join without an ellipsis": {
			content: alphabet,
			width:   26,
			windows: []position.Span{position.NewSpan(0, 4), position.NewSpan(4, 8)},
			want:    "abcdefgh...",
		},
		"window past the line stops at its end": {
			content: alphabet,
			width:   26,
			windows: []position.Span{position.NewSpan(22, 40)},
			want:    "...wxyz",
		},
		"window that overlaps the one before keeps the rest": {
			content: alphabet,
			width:   26,
			windows: []position.Span{position.NewSpan(0, 6), position.NewSpan(4, 8)},
			want:    "abcdefgh...",
		},
		"no window leaves an ellipsis": {
			content: alphabet,
			width:   26,
			want:    "...",
		},
		"empty line": {
			want: "",
		},
		"windows count runes": {
			content: "日本語のテキスト",
			width:   8,
			windows: []position.Span{position.NewSpan(0, 2), position.NewSpan(6, 8)},
			want:    "日本...スト",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			m := clip.New(tc.width, tc.windows)

			assert.Equal(t, tc.want, m.Content(tc.content))
			assert.Equal(t, len([]rune(tc.want)), m.Width())
		})
	}
}

func TestMap_Col(t *testing.T) {
	t.Parallel()

	// The row reads "...efgh...uvwx...", with the windows at columns 3
	// and 10 of it.
	windows := []position.Span{position.NewSpan(4, 8), position.NewSpan(20, 24)}

	tcs := map[string]struct {
		col  int
		want int
	}{
		"before the first window takes the first ellipsis": {col: 2, want: 0},
		"start of the line":                               {col: 0, want: 0},
		"negative counts as the start":                    {col: -5, want: 0},
		"start of a window":                               {col: 4, want: 3},
		"inside a window":                                 {col: 6, want: 5},
		"just past a window takes the next ellipsis":      {col: 8, want: 7},
		"between windows takes the ellipsis between them": {col: 15, want: 7},
		"start of the second window":                      {col: 20, want: 10},
		"end of the second window":                        {col: 23, want: 13},
		"after the last window takes the last ellipsis":   {col: 25, want: 14},
		"end of the line is the end of the row":           {col: 26, want: 17},
		"past the end keeps its distance":                 {col: 30, want: 21},
		"max column saturates":                            {col: math.MaxInt, want: math.MaxInt - 9},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			m := clip.New(26, windows)

			assert.Equal(t, tc.want, m.Col(tc.col))
		})
	}
}

func TestMap_Col_EmptyLine(t *testing.T) {
	t.Parallel()

	m := clip.New(0, nil)

	assert.Equal(t, 0, m.Col(0))
	assert.Equal(t, 3, m.Col(3))
	assert.Equal(t, 0, m.Width())
}

func TestMap_Spans(t *testing.T) {
	t.Parallel()

	// The row reads "...efgh...uvwx...", with the windows at columns 3
	// and 10 of it.
	windows := []position.Span{position.NewSpan(4, 8), position.NewSpan(20, 24)}

	tcs := map[string]struct {
		want position.Spans
		cols position.Span
	}{
		"inside one window": {
			cols: position.NewSpan(5, 7),
			want: position.Spans{position.NewSpan(4, 6)},
		},
		"cut at the end of a window": {
			cols: position.NewSpan(6, 12),
			want: position.Spans{position.NewSpan(5, 7)},
		},
		"cut at the start of a window": {
			cols: position.NewSpan(1, 6),
			want: position.Spans{position.NewSpan(3, 5)},
		},
		"across both windows": {
			cols: position.NewSpan(0, 26),
			want: position.Spans{position.NewSpan(3, 7), position.NewSpan(10, 14)},
		},
		"inside a gap":       {cols: position.NewSpan(10, 14)},
		"past the end":       {cols: position.NewSpan(26, 30)},
		"no columns":         {cols: position.NewSpan(5, 5)},
		"start past the end": {cols: position.NewSpan(7, 5)},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			m := clip.New(26, windows)

			assert.Equal(t, tc.want, m.Spans(tc.cols))
		})
	}
}

func TestMap_Pieces(t *testing.T) {
	t.Parallel()

	m := clip.New(26, []position.Span{position.NewSpan(4, 8), position.NewSpan(20, 24)})

	want := []clip.Piece{
		{Cols: position.NewSpan(0, 4), At: 0, Gap: true},
		{Cols: position.NewSpan(4, 8), At: 3},
		{Cols: position.NewSpan(8, 20), At: 7, Gap: true},
		{Cols: position.NewSpan(20, 24), At: 10},
		{Cols: position.NewSpan(24, 26), At: 14, Gap: true},
	}

	assert.Equal(t, want, slices.Collect(m.Pieces()))

	// The iterator stops when the caller does.
	var first []clip.Piece

	for p := range m.Pieces() {
		first = append(first, p)

		break
	}

	assert.Equal(t, want[:1], first)
}
