package escape_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"go.jacobcolvin.com/niceyaml/escape"
)

func TestControl(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input string
		want  string
	}{
		"empty text": {input: "", want: ""},
		"plain text unchanged": {
			input: "café.yaml:3:1: $.menu: 2 problems",
			want:  "café.yaml:3:1: $.menu: 2 problems",
		},
		"escape sequence":                       {input: "unknown level a\x1b[2Jb", want: "unknown level a␛[2Jb"},
		"line feed":                             {input: "evil\nother.yaml", want: "evil␊other.yaml"},
		"carriage return":                       {input: "ok.yaml\rEVIL", want: "ok.yaml␍EVIL"},
		"tab":                                   {input: "a\tb", want: "a␉b"},
		"first and last C0 controls":            {input: "\x00\x1f", want: "␀␟"},
		"space after the C0 controls unchanged": {input: "a b", want: "a b"},
		"DEL":                                   {input: "a\x7fb", want: "a␡b"},
		"C1 control":                            {input: "a\u009b2Jb", want: "a\ufffd2Jb"},
		"first and last C1 controls":            {input: "\u0080\u009f", want: "\ufffd\ufffd"},
		"no-break space after the C1 controls unchanged": {input: "a\u00a0b", want: "a\u00a0b"},
		// A lone byte of the C1 range is no valid UTF-8.
		"C1 control as one byte":                         {input: "a\x9b2Jb", want: "a\ufffd2Jb"},
		"each invalid byte":                              {input: "a\xe2\x82b", want: "a\ufffd\ufffdb"},
		"right-to-left override unchanged":               {input: "a\u202etxt.lmay", want: "a\u202etxt.lmay"},
		"zero width space and byte order mark unchanged": {input: "a\u200bb\ufeffc", want: "a\u200bb\ufeffc"},
		"line and paragraph separators unchanged":        {input: "a\u2028b\u2029c", want: "a\u2028b\u2029c"},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, escape.Control(tc.input))
		})
	}
}
