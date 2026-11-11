package cells_test

import (
	"math"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.jacobcolvin.com/niceyaml/internal/cells"
)

// zwjRow holds two emoji joined by a zero-width joiner, which make one
// cluster two cells wide at columns 3 through 5.
const zwjRow = "a: \U0001F468\u200d\U0001F469 x"

func TestRow(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		content string
		tabs    cells.Tabs
		// Zero measures the zero Row in place of NewRow(content).
		zero      bool
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
		"tab to the next stop": {
			content: "a\tb", tabs: cells.Tabs{Width: 4}, col: 1,
			wantStart: 1, wantCells: 3, wantWidth: 1,
		},
		"after a tab": {
			content: "a\tb", tabs: cells.Tabs{Width: 4}, col: 2,
			wantStart: 2, wantCells: 1, wantWidth: 4,
		},
		"tab at a stop takes a full width": {
			content: "\t\tb", tabs: cells.Tabs{Width: 4}, col: 2,
			wantStart: 2, wantCells: 1, wantWidth: 8,
		},
		"tab before a stop takes one cell": {
			content: "abc\td", tabs: cells.Tabs{Width: 4}, col: 3,
			wantStart: 3, wantCells: 1, wantWidth: 3,
		},
		"tab after a wide rune": {
			content: "日\tb", tabs: cells.Tabs{Width: 4}, col: 2,
			wantStart: 2, wantCells: 1, wantWidth: 4,
		},
		"tab stops count from start": {
			content: "a\tb", tabs: cells.Tabs{Width: 4, Start: 2}, col: 2,
			wantStart: 2, wantCells: 1, wantWidth: 2,
		},
		"negative start counts as zero": {
			content: "a\tb", tabs: cells.Tabs{Width: 4, Start: -3}, col: 2,
			wantStart: 2, wantCells: 1, wantWidth: 4,
		},
		"negative tab width draws the picture": {
			content: "a\tb", tabs: cells.Tabs{Width: -1}, col: 2,
			wantStart: 2, wantCells: 1, wantWidth: 2,
		},
		"cluster start":        {content: zwjRow, col: 3, wantStart: 3, wantCells: 2, wantWidth: 3},
		"zwj joiner":           {content: zwjRow, col: 4, wantStart: 3, wantCells: 0, wantWidth: 3},
		"second zwj emoji":     {content: zwjRow, col: 5, wantStart: 3, wantCells: 0, wantWidth: 3},
		"after a zwj sequence": {content: zwjRow, col: 7, wantStart: 7, wantCells: 1, wantWidth: 6},
		"past the end":         {content: "a: b", col: 6, wantStart: 6, wantCells: 1, wantWidth: 6},
		"max column after wide runes saturates": {
			content:   "k: 日本語",
			col:       math.MaxInt,
			wantStart: math.MaxInt,
			wantCells: 1,
			wantWidth: math.MaxInt,
		},
		"zero Row start":        {zero: true, col: 0, wantStart: 0, wantCells: 1, wantWidth: 0},
		"zero Row negative":     {zero: true, col: -1, wantStart: 0, wantCells: 1, wantWidth: 0},
		"zero Row past the end": {zero: true, col: 3, wantStart: 3, wantCells: 1, wantWidth: 3},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			row := cells.NewRow(tc.content, tc.tabs)
			if tc.zero {
				row = cells.Row{}
			}

			assert.Equal(t, tc.wantStart, row.Start(tc.col), "start")
			assert.Equal(t, tc.wantCells, row.Cells(tc.col), "cells")
			assert.Equal(t, tc.wantWidth, row.Width(tc.col), "width")
		})
	}
}

func TestRowCol(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		content string
		tabs    cells.Tabs
		// Zero maps the cell on the zero Row in place of NewRow(content).
		zero bool
		lo   int
		cell int
		want int
	}{
		"inside a tab":                    {content: "a\tb", tabs: cells.Tabs{Width: 4}, lo: 0, cell: 2, want: 2},
		"after a tab":                     {content: "a\tb", tabs: cells.Tabs{Width: 4}, lo: 0, cell: 4, want: 2},
		"ascii":                           {content: "a: b", lo: 0, cell: 2, want: 2},
		"wide rune start":                 {content: "k: 日本", lo: 0, cell: 3, want: 3},
		"inside a wide rune":              {content: "k: 日本", lo: 0, cell: 4, want: 4},
		"inside a flag":                   {content: "x\U0001F1FA\U0001F1F8y", lo: 0, cell: 2, want: 3},
		"after a combining accent":        {content: "cafe\u0301 x", lo: 0, cell: 4, want: 5},
		"inside a zwj sequence":           {content: zwjRow, lo: 0, cell: 4, want: 6},
		"after a zwj sequence":            {content: zwjRow, lo: 0, cell: 5, want: 6},
		"end of the content":              {content: "a: b", lo: 0, cell: 4, want: 4},
		"past the end":                    {content: "a: b", lo: 0, cell: 6, want: 6},
		"past the end after wide runes":   {content: "日本", lo: 0, cell: 5, want: 3},
		"cell before lo":                  {content: "abc", lo: 2, cell: 0, want: 2},
		"lo skips a zero-width cluster":   {content: "a\u200bb", lo: 2, cell: 1, want: 2},
		"zero-width cluster before lo":    {content: "a\u200bb", lo: 0, cell: 1, want: 1},
		"lo inside a cluster":             {content: zwjRow, lo: 4, cell: 3, want: 3},
		"negative lo":                     {content: "a: b", lo: -2, cell: 1, want: 1},
		"lo past the end":                 {content: "ab", lo: 5, cell: 1, want: 5},
		"lo past a cell past the end":     {content: "ab", lo: 5, cell: 3, want: 5},
		"max cell after zero-width runes": {content: "cafe\u0301", lo: 0, cell: math.MaxInt, want: math.MaxInt},
		"zero Row start":                  {zero: true, lo: 0, cell: 0, want: 0},
		"zero Row past the end":           {zero: true, lo: 0, cell: 3, want: 3},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			row := cells.NewRow(tc.content, tc.tabs)
			if tc.zero {
				row = cells.Row{}
			}

			assert.Equal(t, tc.want, row.Col(tc.lo, tc.cell))
		})
	}
}

func TestRowNext(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		content string
		col     int
		want    int
	}{
		"cluster start":              {content: zwjRow, col: 3, want: 3},
		"zwj joiner":                 {content: zwjRow, col: 4, want: 6},
		"last rune of a cluster":     {content: zwjRow, col: 5, want: 6},
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

			assert.Equal(t, tc.want, cells.NewRow(tc.content, cells.Tabs{}).Next(tc.col))
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

func TestFirst(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input     string
		want      string
		wantWidth int
	}{
		"empty":          {input: "", want: "", wantWidth: 0},
		"ascii":          {input: "ab", want: "a", wantWidth: 1},
		"wide rune":      {input: "日本", want: "日", wantWidth: 2},
		"combining mark": {input: "e\u0301x", want: "e\u0301", wantWidth: 1},
		"keycap":         {input: "1\ufe0f\u20e3x", want: "1\ufe0f\u20e3", wantWidth: 2},
		"zwj sequence": {
			input:     "\U0001F468\u200d\U0001F469x",
			want:      "\U0001F468\u200d\U0001F469",
			wantWidth: 2,
		},
		"zero-width space": {input: "\u200bx", want: "\u200b", wantWidth: 0},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, width := cells.First(tc.input)
			assert.Equal(t, tc.want, got)
			assert.Equal(t, tc.wantWidth, width, "width")
		})
	}
}

// rendererHelperEnv makes [TestNewRow_AgreesWithRenderer] measure its
// corpus in the process it runs in. The value is the width that the helper
// expects for a rune of ambiguous East Asian width.
const rendererHelperEnv = "CELLS_TEST_RENDERER_HELPER"

// rendererCorpus holds the text that a [cells.Row] and
// [ansi.StringWidth] must measure alike.
var rendererCorpus = []string{
	"a: b",
	"k: 日本",
	"ｱｲｳ Ａ",
	"한국어",
	// Runes of ambiguous East Asian width.
	"± § ° × ÷ → ← ①",
	"αβγ Ω",
	"\u2026 \u2010 \u203b \u2606",
	"1\ufe0f\u20e3 #\ufe0f\u20e3",
	"a\U0001F468\u200d\U0001F469b",
	"\U0001F1FA\U0001F1F8",
	"\U0001F44D\U0001F3FD",
	"cafe\u0301",
	"a\u200bb",
	"\u263a\ufe0f \u263a",
	"\u2764 \u2764\ufe0f",
}

// TestNewRow_AgreesWithRenderer checks that a [cells.Row] measures each
// line of a corpus as [ansi.StringWidth] does. Both read the
// RUNEWIDTH_EASTASIAN variable when the program starts, so each case runs
// the test binary again with the variable set to its value.
func TestNewRow_AgreesWithRenderer(t *testing.T) {
	t.Parallel()

	if want := os.Getenv(rendererHelperEnv); want != "" {
		ambiguous, err := strconv.Atoi(want)
		require.NoError(t, err)

		_, width := cells.First("±")
		require.Equal(t, ambiguous, width, "width of an ambiguous rune")

		for _, line := range rendererCorpus {
			row := cells.NewRow(line, cells.Tabs{})
			assert.Equal(t, ansi.StringWidth(line), row.Width(utf8.RuneCountInString(line)), "%q", line)
		}

		return
	}

	tcs := map[string]struct {
		// Unset runs the helper without the variable.
		unset         bool
		eastAsian     string
		wantAmbiguous int
	}{
		"unset":        {unset: true, wantAmbiguous: 1},
		"east asian":   {eastAsian: "1", wantAmbiguous: 2},
		"false":        {eastAsian: "0", wantAmbiguous: 1},
		"not a number": {eastAsian: "wide", wantAmbiguous: 1},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var env []string

			for _, kv := range os.Environ() {
				if !strings.HasPrefix(kv, "RUNEWIDTH_EASTASIAN=") {
					env = append(env, kv)
				}
			}

			env = append(env, rendererHelperEnv+"="+strconv.Itoa(tc.wantAmbiguous))
			if !tc.unset {
				env = append(env, "RUNEWIDTH_EASTASIAN="+tc.eastAsian)
			}

			cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestNewRow_AgreesWithRenderer$")
			cmd.Env = env

			out, err := cmd.CombinedOutput()
			require.NoError(t, err, "%s", out)
		})
	}
}
