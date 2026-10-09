package bom_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

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
			input: "a: 1\nb: 2\n",
			want:  "a: 1\nb: 2\n",
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
			input: "\n# c\r\n\ufeffa: 1\n",
			want:  "\n# c\r\na: 1\n",
			marks: []int{6},
		},
		"mark on each line before the content": {
			input: "\ufeff# c\n\ufeffa: 1\n",
			want:  "# c\na: 1\n",
			marks: []int{0, 4},
		},
		"mark inside the content stays": {
			input: "a: 1\n\ufeffb: 2\n",
			want:  "a: 1\n\ufeffb: 2\n",
		},
		"mark inside a line stays": {
			input: "a: \"\ufeffx\"\n",
			want:  "a: \"\ufeffx\"\n",
		},
		"mark in front of a document marker": {
			input: "a: 1\n\ufeff---\nb: 2\n",
			want:  "a: 1\n---\nb: 2\n",
			marks: []int{5},
		},
		"mark in front of a document end marker": {
			input: "a: 1\r\ufeff...\r",
			want:  "a: 1\r...\r",
			marks: []int{5},
		},
		"mark after a marker line": {
			input: "a: 1\n--- # c\n\ufeffb: 2\n",
			want:  "a: 1\n--- # c\nb: 2\n",
			marks: []int{13},
		},
		"mark after a marker line with content": {
			input: "--- a\n\ufeffb\n",
			want:  "--- a\n\ufeffb\n",
		},
		"mark in front of text that opens like a marker": {
			input: "a: 1\n\ufeff---b\n",
			want:  "a: 1\n\ufeff---b\n",
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
