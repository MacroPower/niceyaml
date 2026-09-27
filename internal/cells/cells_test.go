package cells_test

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"

	"go.jacobcolvin.com/niceyaml/internal/cells"
)

func TestRow(t *testing.T) {
	t.Parallel()

	zwj := "a: \U0001F468\u200d\U0001F469 x"

	tcs := map[string]struct {
		content   string
		col       int
		wantStart int
		wantCells int
		wantWidth int
	}{
		"start":                    {content: "a: b", col: 0, wantStart: 0, wantCells: 1, wantWidth: 0},
		"negative clamps to start": {content: "a: b", col: -1, wantStart: 0, wantCells: 1, wantWidth: 0},
		"control character":        {content: "a\tb", col: 1, wantStart: 1, wantCells: 1, wantWidth: 1},
		"wide rune":                {content: "k: 日本", col: 4, wantStart: 4, wantCells: 2, wantWidth: 5},
		"cluster start":            {content: zwj, col: 3, wantStart: 3, wantCells: 2, wantWidth: 3},
		"zwj joiner":               {content: zwj, col: 4, wantStart: 3, wantCells: 0, wantWidth: 3},
		"second zwj emoji":         {content: zwj, col: 5, wantStart: 3, wantCells: 0, wantWidth: 3},
		"after a zwj sequence":     {content: zwj, col: 7, wantStart: 7, wantCells: 1, wantWidth: 6},
		"past the end":             {content: "a: b", col: 6, wantStart: 6, wantCells: 1, wantWidth: 6},
		"max column after wide runes saturates": {
			content:   "k: 日本語",
			col:       math.MaxInt,
			wantStart: math.MaxInt,
			wantCells: 1,
			wantWidth: math.MaxInt,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			row := cells.NewRow(tc.content)

			assert.Equal(t, tc.wantStart, row.Start(tc.col), "start")
			assert.Equal(t, tc.wantCells, row.Cells(tc.col), "cells")
			assert.Equal(t, tc.wantWidth, row.Width(tc.col), "width")
		})
	}
}
