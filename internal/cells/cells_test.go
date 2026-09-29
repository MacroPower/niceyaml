package cells_test

import (
	"math"
	"testing"

	"github.com/charmbracelet/x/ansi"
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
		"start":                           {content: "a: b", col: 0, wantStart: 0, wantCells: 1, wantWidth: 0},
		"negative clamps to start":        {content: "a: b", col: -1, wantStart: 0, wantCells: 1, wantWidth: 0},
		"negative clamps to a wide start": {content: "日本", col: -1, wantStart: 0, wantCells: 2, wantWidth: 0},
		"control character":               {content: "a\tb", col: 1, wantStart: 1, wantCells: 1, wantWidth: 1},
		"wide rune":                       {content: "k: 日本", col: 4, wantStart: 4, wantCells: 2, wantWidth: 5},
		"cluster start":                   {content: zwj, col: 3, wantStart: 3, wantCells: 2, wantWidth: 3},
		"zwj joiner":                      {content: zwj, col: 4, wantStart: 3, wantCells: 0, wantWidth: 3},
		"second zwj emoji":                {content: zwj, col: 5, wantStart: 3, wantCells: 0, wantWidth: 3},
		"after a zwj sequence":            {content: zwj, col: 7, wantStart: 7, wantCells: 1, wantWidth: 6},
		"past the end":                    {content: "a: b", col: 6, wantStart: 6, wantCells: 1, wantWidth: 6},
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

func TestRowCol(t *testing.T) {
	t.Parallel()

	zwj := "a: \U0001F468\u200d\U0001F469 x"

	tcs := map[string]struct {
		content string
		lo      int
		cell    int
		want    int
	}{
		"ascii":                           {content: "a: b", lo: 0, cell: 2, want: 2},
		"wide rune start":                 {content: "k: 日本", lo: 0, cell: 3, want: 3},
		"inside a wide rune":              {content: "k: 日本", lo: 0, cell: 4, want: 4},
		"inside a flag":                   {content: "x\U0001F1FA\U0001F1F8y", lo: 0, cell: 2, want: 3},
		"after a combining accent":        {content: "cafe\u0301 x", lo: 0, cell: 4, want: 5},
		"inside a zwj sequence":           {content: zwj, lo: 0, cell: 4, want: 6},
		"after a zwj sequence":            {content: zwj, lo: 0, cell: 5, want: 6},
		"end of the content":              {content: "a: b", lo: 0, cell: 4, want: 4},
		"past the end":                    {content: "a: b", lo: 0, cell: 6, want: 6},
		"past the end after wide runes":   {content: "日本", lo: 0, cell: 5, want: 3},
		"cell before lo":                  {content: "abc", lo: 2, cell: 0, want: 2},
		"lo skips a zero-width cluster":   {content: "a\u200bb", lo: 2, cell: 1, want: 2},
		"zero-width cluster before lo":    {content: "a\u200bb", lo: 0, cell: 1, want: 1},
		"lo inside a cluster":             {content: zwj, lo: 4, cell: 3, want: 3},
		"negative lo":                     {content: "a: b", lo: -2, cell: 1, want: 1},
		"lo past the end":                 {content: "ab", lo: 5, cell: 1, want: 5},
		"lo past a cell past the end":     {content: "ab", lo: 5, cell: 3, want: 5},
		"max cell after zero-width runes": {content: "cafe\u0301", lo: 0, cell: math.MaxInt, want: math.MaxInt},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, cells.NewRow(tc.content).Col(tc.lo, tc.cell))
		})
	}
}

func TestRowNext(t *testing.T) {
	t.Parallel()

	zwj := "a: \U0001F468\u200d\U0001F469 x"

	tcs := map[string]struct {
		content string
		col     int
		want    int
	}{
		"cluster start":              {content: zwj, col: 3, want: 3},
		"zwj joiner":                 {content: zwj, col: 4, want: 6},
		"last rune of a cluster":     {content: zwj, col: 5, want: 6},
		"combining accent":           {content: "a \u0301b", col: 2, want: 3},
		"accent at the end":          {content: "e\u0301", col: 1, want: 2},
		"negative":                   {content: "ab", col: -1, want: 0},
		"past the end":               {content: "ab", col: 4, want: 4},
		"empty content past the end": {content: "", col: 2, want: 2},
		"empty content":              {content: "", col: 0, want: 0},
		"wide rune is one cluster":   {content: "日本", col: 1, want: 1},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, cells.NewRow(tc.content).Next(tc.col))
		})
	}
}

func TestRowZeroValue(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		col       int
		wantStart int
		wantCells int
		wantWidth int
		wantCol   int
	}{
		"start":    {col: 0, wantStart: 0, wantCells: 1, wantWidth: 0, wantCol: 0},
		"negative": {col: -1, wantStart: 0, wantCells: 1, wantWidth: 0, wantCol: 0},
		"past the end": {
			col:       3,
			wantStart: 3,
			wantCells: 1,
			wantWidth: 3,
			wantCol:   3,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var row cells.Row

			assert.Equal(t, tc.wantStart, row.Start(tc.col), "start")
			assert.Equal(t, tc.wantCells, row.Cells(tc.col), "cells")
			assert.Equal(t, tc.wantWidth, row.Width(tc.col), "width")
			assert.Equal(t, tc.wantCol, row.Col(0, tc.wantWidth), "col")
		})
	}
}

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
		"ascii":                    {input: "abcdef", left: 1, right: 4, want: "bcd", wantWidth: 3},
		"empty range":              {input: "\x1b[31mabcdef\x1b[0m", left: 2, right: 2, want: "", wantWidth: 0},
		"range past the end":       {input: "ab", left: 1, right: 9, want: "b", wantWidth: 1},
		"keycap whole":             {input: keycap, left: 0, right: 4, want: "ab1\ufe0f\u20e3", wantWidth: 4},
		"right edge inside keycap": {input: keycap, left: 0, right: 3, want: "ab", wantWidth: 2},
		"left edge inside keycap":  {input: keycap, left: 3, right: 6, want: "cd", wantWidth: 2},
		"past a keycap":            {input: keycap, left: 4, right: 6, want: "cd", wantWidth: 2},
		"wide rune":                {input: "a日b", left: 1, right: 3, want: "日", wantWidth: 2},
		"edges inside a wide rune": {input: "a日b", left: 2, right: 4, want: "b", wantWidth: 1},
		"zwj sequence":             {input: zwj, left: 1, right: 3, want: "\U0001F468\u200d\U0001F469", wantWidth: 2},
		"edge inside zwj sequence": {input: zwj, left: 0, right: 2, want: "a", wantWidth: 1},
		"combining mark":           {input: "cafe\u0301!", left: 3, right: 4, want: "e\u0301", wantWidth: 1},
		"styled keeps escapes": {
			input:     "\x1b[31m" + keycap + "\x1b[0m",
			left:      1,
			right:     3,
			want:      "\x1b[31mb\x1b[0m",
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

			assert.Equal(t, tc.want, cells.Cut(tc.input, tc.left, tc.right))

			got, width := cells.CutWidth(tc.input, tc.left, tc.right)
			assert.Equal(t, tc.want, got)
			assert.Equal(t, tc.wantWidth, width, "width")
			assert.Equal(t, ansi.StringWidth(got), width, "width agrees with ansi.StringWidth")
		})
	}
}

func TestTrimLastCluster(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input string
		want  string
	}{
		"empty":                         {input: "", want: ""},
		"ascii":                         {input: "abc", want: "ab"},
		"wide rune":                     {input: "k日本", want: "k日"},
		"combining mark":                {input: "cafe\u0301", want: "caf"},
		"emoji with skin tone modifier": {input: "a\U0001F44D\U0001F3FD", want: "a"},
		"flag":                          {input: "x\U0001F1FA\U0001F1F8", want: "x"},
		"zwj sequence":                  {input: "a\U0001F468\u200d\U0001F469", want: "a"},
		"crlf":                          {input: "a\r\n", want: "a"},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, cells.TrimLastCluster(tc.input))
		})
	}
}
