package line_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"go.jacobcolvin.com/niceyaml/line"
	"go.jacobcolvin.com/niceyaml/position"
)

// twoWindows cut a line of 26 columns to a row that reads
// "...efgh...uvwx...", with the windows at columns 3 and 10 of it.
var twoWindows = position.Spans{position.NewSpan(4, 8), position.NewSpan(20, 24)}

func TestClipMap_Col(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		windows position.Spans
		width   int
		col     int
		want    int
	}{
		"column before the first window takes the first ellipsis": {width: 26, windows: twoWindows, col: 2, want: 0},
		"start of a window": {width: 26, windows: twoWindows, col: 4, want: 3},
		"inside a window":   {width: 26, windows: twoWindows, col: 6, want: 5},
		"between windows takes the ellipsis between them":    {width: 26, windows: twoWindows, col: 15, want: 7},
		"after the last window takes the last ellipsis":      {width: 26, windows: twoWindows, col: 25, want: 14},
		"end of the line is the end of the row":              {width: 26, windows: twoWindows, col: 26, want: 17},
		"past the end keeps its distance":                    {width: 26, windows: twoWindows, col: 30, want: 21},
		"negative counts as the start":                       {width: 26, windows: twoWindows, col: -5, want: 0},
		"line shown whole keeps its column":                  {width: 26, col: 6, want: 6},
		"line shown whole past the end":                      {width: 26, col: 30, want: 30},
		"negative on a line shown whole counts as the start": {width: 26, col: -5, want: 0},
		"zero value keeps its column":                        {col: 3, want: 3},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, line.NewClipMap(tc.width, tc.windows).Col(tc.col))
		})
	}
}

func TestClipMap_LineCol(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		windows position.Spans
		width   int
		col     int
		want    int
		ok      bool
	}{
		"first ellipsis":                     {width: 26, windows: twoWindows, col: 1},
		"start of a window":                  {width: 26, windows: twoWindows, col: 3, want: 4, ok: true},
		"inside a window":                    {width: 26, windows: twoWindows, col: 5, want: 6, ok: true},
		"ellipsis between two windows":       {width: 26, windows: twoWindows, col: 8},
		"second window":                      {width: 26, windows: twoWindows, col: 13, want: 23, ok: true},
		"last ellipsis":                      {width: 26, windows: twoWindows, col: 15},
		"end of the row":                     {width: 26, windows: twoWindows, col: 17},
		"negative counts as the start":       {width: 26, windows: twoWindows, col: -5},
		"line shown whole keeps its column":  {width: 26, col: 6, want: 6, ok: true},
		"end of a line shown whole":          {width: 26, col: 26},
		"negative on a line shown whole":     {width: 26, col: -5, want: 0, ok: true},
		"zero value has no column":           {col: 0},
		"zero value past the end":            {col: 3},
		"empty windows show the line whole":  {width: 26, windows: position.Spans{}, col: 6, want: 6, ok: true},
		"negative width shows an empty line": {width: -3, col: 0},
		"clamped window past the line shows none": {
			width:   26,
			windows: position.Spans{position.NewSpan(30, 40)},
			col:     0,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, ok := line.NewClipMap(tc.width, tc.windows).LineCol(tc.col)

			assert.Equal(t, tc.ok, ok)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestView_ClipMap(t *testing.T) {
	t.Parallel()

	const letters = "key: abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"

	// The clipped line shows "...fghijklmno...", with the mark at column 8
	// of the row.
	mark := position.NewSpan(15, 16)

	tcs := map[string]struct {
		cols        int
		col         int
		wantCol     int
		wantLineCol int
		wantOK      bool
	}{
		"clipped line maps its mark to the row":         {cols: 10, col: 15, wantCol: 8, wantLineCol: 15, wantOK: true},
		"clipped line maps a cut column to an ellipsis": {cols: 10, col: 30, wantCol: 13},
		"line shown whole keeps its column":             {cols: 0, col: 15, wantCol: 15, wantLineCol: 15, wantOK: true},
		"line that fits keeps its column": {
			cols:        100,
			col:         15,
			wantCol:     15,
			wantLineCol: 15,
			wantOK:      true,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			view := newClippedView(letters, 0, tc.cols, []position.Span{mark})
			m := view.ClipMap(0)

			assert.Equal(t, tc.wantCol, m.Col(tc.col))

			got, ok := m.LineCol(tc.wantCol)
			assert.Equal(t, tc.wantOK, ok)
			assert.Equal(t, tc.wantLineCol, got)

			assert.Equal(t, line.NewClipMap(view.Line(0).Width(), view.Windows(0)), m)
		})
	}
}
