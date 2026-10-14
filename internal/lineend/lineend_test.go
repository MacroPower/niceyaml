package lineend_test

import (
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"go.jacobcolvin.com/x/stringtest"

	"go.jacobcolvin.com/niceyaml/internal/lineend"
)

func TestLines(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input string
		want  []string
		// The line breaks that CountBreaks counts in input.
		breaks int
	}{
		"empty":                      {input: "", want: nil, breaks: 0},
		"no line ending":             {input: "a", want: []string{"a"}, breaks: 0},
		"LF":                         {input: "a\n", want: []string{"a\n"}, breaks: 1},
		"CRLF":                       {input: "a\r\nb", want: []string{"a\r\n", "b"}, breaks: 1},
		"bare CR":                    {input: "a\rb", want: []string{"a\r", "b"}, breaks: 1},
		"bare CR before CRLF":        {input: "\r\r\n", want: []string{"\r", "\r\n"}, breaks: 2},
		"bare CR before CRLF and LF": {input: "\r\r\n\n", want: []string{"\r", "\r\n", "\n"}, breaks: 3},
		"blank lines":                {input: "\n\n", want: []string{"\n", "\n"}, breaks: 2},
		"CR after LF":                {input: "\n\r", want: []string{"\n", "\r"}, breaks: 2},
		"text after each kind":       {input: "a\nb\r\nc\rd", want: []string{"a\n", "b\r\n", "c\r", "d"}, breaks: 3},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, slices.Collect(lineend.Lines(tc.input)))
			assert.Equal(t, tc.breaks, lineend.CountBreaks(tc.input), "breaks")
		})
	}
}

func TestLinesStopsEarly(t *testing.T) {
	t.Parallel()

	var got []string

	for ln := range lineend.Lines(stringtest.JoinLF("a", "b", "c")) {
		got = append(got, ln)
		if len(got) == 2 {
			break
		}
	}

	assert.Equal(t, []string{"a\n", "b\n"}, got)
}
