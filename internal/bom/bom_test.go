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

func TestContentStart(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input string
		want  int
	}{
		"empty":                         {input: "", want: 0},
		"content first":                 {input: stringtest.LinesLF("a: 1", "b: 2"), want: 0},
		"after a comment":               {input: stringtest.LinesLF("# c", "a: 1"), want: 4},
		"after blank lines":             {input: stringtest.LinesLF("", "  ", "a: 1"), want: 4},
		"indented comment":              {input: stringtest.LinesLF("  # c", "a: 1"), want: 6},
		"CRLF line endings":             {input: "# c\r\na: 1\r\n", want: 5},
		"document marker":               {input: stringtest.LinesLF("# c", "---", "a: 1"), want: 4},
		"directive":                     {input: stringtest.LinesLF("%YAML 1.2", "---"), want: 0},
		"only comments":                 {input: stringtest.LinesLF("# c", "# d"), want: 8},
		"comment without a line ending": {input: "# c", want: 3},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, bom.ContentStart(tc.input))
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

func TestIndex_Position(t *testing.T) {
	t.Parallel()

	twoLines := stringtest.LinesLF("a: 1", "b: 2")

	tcs := map[string]struct {
		src       string
		off       int
		line, col int
		ok        bool
	}{
		"the start":                    {src: twoLines, ok: true},
		"a later line":                 {src: twoLines, off: 5, line: 1, ok: true},
		"the line ending":              {src: twoLines, off: 4, col: 4, ok: true},
		"the length of the text":       {src: twoLines, off: 10, line: 2, ok: true},
		"past the text":                {src: twoLines, off: 11},
		"a negative offset":            {src: twoLines, off: -1},
		"the end of a text without LF": {src: "a: 1", off: 4, col: 4, ok: true},
		"the empty text":               {src: "", ok: true},
		"a mark before the text":       {src: bom.Mark + "a: 1\n", off: 3, ok: true},
		"a byte of a mark":             {src: bom.Mark + "a: 1\n", off: 1, ok: true},
		"after a mark":                 {src: bom.Mark + "a: 1\n", off: 4, col: 1, ok: true},
		"a mark alone":                 {src: bom.Mark, off: 3, ok: true},
		"a later mark": {
			src:  "a: 1\n" + bom.Mark + "---\nb: 2\n",
			off:  9,
			line: 1,
			col:  1,
			ok:   true,
		},
		"a carriage return":               {src: "a\r\nb", off: 1, col: 1, ok: true},
		"a feed after a carriage return":  {src: "a\r\nb", off: 2, col: 1, ok: true},
		"past a carriage return and feed": {src: "a\r\nb", off: 3, line: 1, ok: true},
		"a bare carriage return":          {src: "a\rb", off: 2, line: 1, ok: true},
		"a rune of several bytes":         {src: "é: 日本\n", off: 7, col: 4, ok: true},
		"a byte inside a rune":            {src: "é: 日本\n", off: 5, col: 3, ok: true},
		"a byte that is not UTF-8":        {src: "a\xffb\n", off: 2, col: 2, ok: true},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			line, col, ok := bom.NewIndex(tc.src).Position(tc.off)
			assert.Equal(t, tc.ok, ok)
			assert.Equal(t, tc.line, line, "line")
			assert.Equal(t, tc.col, col, "col")
		})
	}
}

func TestIndex_UTF16Column(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		src       string
		line, col int
		want      int
		ok        bool
	}{
		"the start":                     {src: "a: 1\n", want: 0, ok: true},
		"a rune of two units":           {src: "a: 😀 b\n", col: 4, want: 5, ok: true},
		"the line ending":               {src: "a: 😀\n", col: 4, want: 5, ok: true},
		"past the line ending":          {src: "a: 😀\n", col: 5, want: 6, ok: true},
		"a column past the line ending": {src: "a: 😀\n", col: 6},
		"the end of text with no break": {src: "a: 😀", col: 4, want: 5, ok: true},
		"past the end of the text":      {src: "a: 😀", col: 5},
		"a mark before the text":        {src: bom.Mark + "a: 😀\n", col: 4, want: 5, ok: true},
		"a byte that is not UTF-8":      {src: "a\xff😀\n", col: 3, want: 4, ok: true},
		"a line past the last":          {src: "a: 1\n", line: 2},
		"a negative column":             {src: "a: 1\n", col: -1},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, ok := bom.NewIndex(tc.src).UTF16Column(tc.line, tc.col)
			assert.Equal(t, tc.ok, ok)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestIndex_ColumnAtUTF16(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		src         string
		line, units int
		want        int
		ok          bool
	}{
		"the start":                     {src: "a: 1\n", want: 0, ok: true},
		"after a rune of two units":     {src: "a: 😀 b\n", units: 5, want: 4, ok: true},
		"inside a rune of two units":    {src: "a: 😀 b\n", units: 4, want: 3, ok: true},
		"the line ending":               {src: "a: 😀\n", units: 5, want: 4, ok: true},
		"past the line ending":          {src: "a: 😀\n", units: 6, want: 5, ok: true},
		"a unit past the line ending":   {src: "a: 😀\n", units: 7},
		"the end of text with no break": {src: "a: 😀", units: 5, want: 4, ok: true},
		"past the end of the text":      {src: "a: 😀", units: 6},
		"a mark before the text":        {src: bom.Mark + "a: 😀\n", units: 5, want: 4, ok: true},
		"a byte that is not UTF-8":      {src: "a\xff😀\n", units: 4, want: 3, ok: true},
		"a line past the last":          {src: "a: 1\n", line: 2},
		"a negative line":               {src: "a: 1\n", line: -1},
		"a negative unit":               {src: "a: 1\n", units: -1},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, ok := bom.NewIndex(tc.src).ColumnAtUTF16(tc.line, tc.units)
			assert.Equal(t, tc.ok, ok)
			assert.Equal(t, tc.want, got)
		})
	}
}
