package bom_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"go.jacobcolvin.com/x/stringtest"

	"go.jacobcolvin.com/niceyaml/internal/bom"
)

func TestDrop(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input string
		want  string
		marks []int
	}{
		"empty": {input: "", want: ""},
		"no mark": {
			input: stringtest.LinesLF("a: 1", "b: 2"),
			want:  stringtest.LinesLF("a: 1", "b: 2"),
		},
		"mark opens the text": {
			input: "\ufeffa: 1\n",
			want:  "a: 1\n",
			marks: []int{0},
		},
		"mark alone": {
			input: "\ufeff",
			want:  "",
			marks: []int{0},
		},
		"only the first of two marks": {
			input: "\ufeff\ufeffa: 1\n",
			want:  "\ufeffa: 1\n",
			marks: []int{0},
		},
		"mark after blank and comment lines": {
			input: stringtest.LinesLF("", "# c\r", "\ufeffa: 1"),
			want:  stringtest.LinesLF("", "# c\r", "a: 1"),
			marks: []int{6},
		},
		"mark on each line before the content": {
			input: stringtest.LinesLF("\ufeff# c", "\ufeffa: 1"),
			want:  stringtest.LinesLF("# c", "a: 1"),
			marks: []int{0, 4},
		},
		"mark inside the content stays": {
			input: stringtest.LinesLF("a: 1", "\ufeffb: 2"),
			want:  stringtest.LinesLF("a: 1", "\ufeffb: 2"),
		},
		"mark inside a line stays": {
			input: "a: \"\ufeffx\"\n",
			want:  "a: \"\ufeffx\"\n",
		},
		"mark in front of a document marker": {
			input: stringtest.LinesLF("a: 1", "\ufeff---", "b: 2"),
			want:  stringtest.LinesLF("a: 1", "---", "b: 2"),
			marks: []int{5},
		},
		"mark in front of a document end marker": {
			input: "a: 1\r\ufeff...\r",
			want:  "a: 1\r...\r",
			marks: []int{5},
		},
		"mark after a marker line": {
			input: stringtest.LinesLF("a: 1", "--- # c", "\ufeffb: 2"),
			want:  stringtest.LinesLF("a: 1", "--- # c", "b: 2"),
			marks: []int{13},
		},
		"mark after a marker line with content": {
			input: stringtest.LinesLF("--- a", "\ufeffb"),
			want:  stringtest.LinesLF("--- a", "\ufeffb"),
		},
		"mark in front of text that opens like a marker": {
			input: stringtest.LinesLF("a: 1", "\ufeff---b"),
			want:  stringtest.LinesLF("a: 1", "\ufeff---b"),
		},
		"mark between a bare CR and a LF": {
			input: "\r\ufeff\na: 1\n",
			want:  "\r\na: 1\n",
			marks: []int{1},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, marks := bom.Drop(tc.input)

			assert.Equal(t, tc.want, got)
			assert.Equal(t, tc.marks, marks)
		})
	}
}

func TestIndex_Offset(t *testing.T) {
	t.Parallel()

	twoLines := stringtest.LinesLF("a: 1", "b: 2")

	tcs := map[string]struct {
		src       string
		line, col int
		want      int
		ok        bool
	}{
		"the start":                     {src: twoLines, want: 0, ok: true},
		"a later line":                  {src: twoLines, line: 1, want: 5, ok: true},
		"the end of a line":             {src: twoLines, line: 1, col: 4, want: 9, ok: true},
		"past the line ending":          {src: twoLines, line: 1, col: 5, want: 10, ok: true},
		"the empty last line":           {src: twoLines, line: 2, want: 10, ok: true},
		"a line past the last":          {src: twoLines, line: 3},
		"a column past the line ending": {src: "a: 1\n", col: 6},
		"a mark before the text":        {src: bom.Mark + "a: 1\n", col: 1, want: 4, ok: true},
		"a carriage return and a feed":  {src: "a\r\nb", col: 2, want: 3, ok: true},
		"the end of text with no break": {src: "a\r\nb", line: 1, col: 1, want: 4, ok: true},
		"a rune of two bytes":           {src: "é: 1\n", col: 1, want: 2, ok: true},
		"a negative column":             {src: "a: 1\n", col: -1},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, ok := bom.NewIndex(tc.src).Offset(tc.line, tc.col)
			assert.Equal(t, tc.ok, ok)
			assert.Equal(t, tc.want, got)
		})
	}
}
