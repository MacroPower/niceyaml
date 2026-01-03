package yamltest_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/macropower/niceyaml/yamltest"
)

func TestInput(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input string
		want  string
	}{
		"empty string": {
			input: "",
			want:  "",
		},
		"single line no indent": {
			input: "hello",
			want:  "hello",
		},
		"single line with leading newline": {
			input: "\nhello",
			want:  "hello",
		},
		"single line with trailing newline": {
			input: "hello\n",
			want:  "hello",
		},
		"single line with both newlines": {
			input: "\nhello\n",
			want:  "hello",
		},
		"multi-line no indent": {
			input: "line1\nline2\nline3",
			want:  "line1\nline2\nline3",
		},
		"multi-line with common indent spaces": {
			input: `
    line1
    line2
    line3`,
			want: "line1\nline2\nline3",
		},
		"multi-line with common indent tabs": {
			input: "\n\tline1\n\tline2\n\tline3",
			want:  "line1\nline2\nline3",
		},
		"multi-line with varying indent": {
			input: `
    line1
      indented
    line3`,
			want: "line1\n  indented\nline3",
		},
		"multi-line with empty lines": {
			input: `
    line1

    line3`,
			want: "line1\n\nline3",
		},
		"multi-line with whitespace-only lines": {
			input: "\n    line1\n    \n    line3",
			want:  "line1\n\nline3",
		},
		"preserves multiple leading newlines minus one": {
			input: "\n\nline1\nline2",
			want:  "\nline1\nline2",
		},
		"preserves multiple trailing newlines minus one": {
			input: "line1\nline2\n\n",
			want:  "line1\nline2\n",
		},
		"yaml-like input": {
			input: `
    key: value
    nested:
      child: data
    list:
      - item1
      - item2`,
			want: "key: value\nnested:\n  child: data\nlist:\n  - item1\n  - item2",
		},
		"already dedented": {
			input: "key: value\nnested:\n  child: data",
			want:  "key: value\nnested:\n  child: data",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := yamltest.Input(tc.input)
			assert.Equal(t, tc.want, got)
		})
	}
}
