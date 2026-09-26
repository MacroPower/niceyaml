package lineend_test

import (
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"

	"go.jacobcolvin.com/niceyaml/internal/lineend"
)

func TestLines(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input string
		want  []string
	}{
		"empty":                {input: "", want: nil},
		"no line ending":       {input: "a", want: []string{"a"}},
		"LF":                   {input: "a\n", want: []string{"a\n"}},
		"CRLF":                 {input: "a\r\nb", want: []string{"a\r\n", "b"}},
		"bare CR":              {input: "a\rb", want: []string{"a\r", "b"}},
		"bare CR before CRLF":  {input: "\r\r\n", want: []string{"\r", "\r\n"}},
		"blank lines":          {input: "\n\n", want: []string{"\n", "\n"}},
		"CR after LF":          {input: "\n\r", want: []string{"\n", "\r"}},
		"text after each kind": {input: "a\nb\r\nc\rd", want: []string{"a\n", "b\r\n", "c\r", "d"}},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, slices.Collect(lineend.Lines(tc.input)))
		})
	}
}

func TestLinesStopsEarly(t *testing.T) {
	t.Parallel()

	var got []string

	for ln := range lineend.Lines("a\nb\nc") {
		got = append(got, ln)
		if len(got) == 2 {
			break
		}
	}

	assert.Equal(t, []string{"a\n", "b\n"}, got)
}

func TestCountBreaks(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input string
		want  int
	}{
		"empty":          {input: "", want: 0},
		"no line ending": {input: "a", want: 0},
		"LF":             {input: "\n", want: 1},
		"CRLF":           {input: "\r\n", want: 1},
		"bare CR":        {input: "\r", want: 1},
		"mixed":          {input: "\r\r\n\n", want: 3},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, lineend.CountBreaks(tc.input))
		})
	}
}
