package cellcut_test

import (
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"

	"go.jacobcolvin.com/niceyaml/internal/cellcut"
)

func TestCut(t *testing.T) {
	t.Parallel()

	keycap := "ab1\ufe0f\u20e3cd"
	zwj := "a\U0001F468\u200d\U0001F469b"

	tcs := map[string]struct {
		input     string
		left      int
		right     int
		want      string
		wantWidth int
	}{
		"ascii":              {input: "abcdef", left: 1, right: 4, want: "bcd", wantWidth: 3},
		"empty range":        {input: "\x1b[31mabcdef\x1b[0m", left: 2, right: 2, want: "", wantWidth: 0},
		"range past the end": {input: "ab", left: 1, right: 9, want: "b", wantWidth: 1},
		// A negative left counts as 0, so the cut starts at the first cell
		// and a range that ends at or before it is empty.
		"negative left":           {input: "abcdef", left: -2, right: 3, want: "abc", wantWidth: 3},
		"negative left to cell 0": {input: "\x1b[31mab\x1b[0m", left: -5, right: 0, want: "", wantWidth: 0},
		"keycap whole":            {input: keycap, left: 0, right: 4, want: "ab1\ufe0f\u20e3", wantWidth: 4},
		// A cluster that an edge splits leaves a space on each of its cells
		// inside the range, so every cell keeps its place.
		"right edge inside keycap":        {input: keycap, left: 0, right: 3, want: "ab ", wantWidth: 3},
		"left edge inside keycap":         {input: keycap, left: 3, right: 6, want: " cd", wantWidth: 3},
		"past a keycap":                   {input: keycap, left: 4, right: 6, want: "cd", wantWidth: 2},
		"wide rune":                       {input: "a日b", left: 1, right: 3, want: "日", wantWidth: 2},
		"edges inside a wide rune":        {input: "a日b", left: 2, right: 4, want: " b", wantWidth: 2},
		"wide rune wider than the window": {input: "a日b", left: 2, right: 3, want: " ", wantWidth: 1},
		"zwj sequence": {
			input:     zwj,
			left:      1,
			right:     3,
			want:      "\U0001F468\u200d\U0001F469",
			wantWidth: 2,
		},
		"edge inside zwj sequence":        {input: zwj, left: 0, right: 2, want: "a ", wantWidth: 2},
		"combining mark":                  {input: "cafe\u0301!", left: 3, right: 4, want: "e\u0301", wantWidth: 1},
		"zero-width cluster at the right": {input: "ab\u200bc", left: 0, right: 2, want: "ab", wantWidth: 2},
		"styled keeps escapes": {
			input:     "\x1b[31m" + keycap + "\x1b[0m",
			left:      1,
			right:     3,
			want:      "\x1b[31mb \x1b[0m",
			wantWidth: 2,
		},
		"filler keeps the style": {
			input:     "\x1b[41m日\x1b[0m",
			left:      1,
			right:     2,
			want:      "\x1b[41m \x1b[0m",
			wantWidth: 1,
		},
		"styled drops nothing": {
			input:     "\x1b[31m" + keycap + "\x1b[0m",
			left:      0,
			right:     8,
			want:      "\x1b[31m" + keycap + "\x1b[0m",
			wantWidth: 6,
		},
		"escapes between dropped clusters": {
			input:     "\x1b[1ma\x1b[0mb\x1b[2mc\x1b[0m",
			left:      1,
			right:     2,
			want:      "\x1b[1m\x1b[0mb\x1b[2m\x1b[0m",
			wantWidth: 1,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, cellcut.Cut(tc.input, tc.left, tc.right))

			got, width := cellcut.CutWidth(tc.input, tc.left, tc.right)
			assert.Equal(t, tc.want, got)
			assert.Equal(t, tc.wantWidth, width, "width")
			assert.Equal(t, ansi.StringWidth(got), width, "width agrees with ansi.StringWidth")
		})
	}
}
