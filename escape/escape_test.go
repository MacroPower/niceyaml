package escape_test

import (
	"strings"
	"testing"
	"unicode/utf8"

	"charm.land/lipgloss/v2"
	"github.com/stretchr/testify/assert"
	"go.jacobcolvin.com/x/stringtest"

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
		"ANSI red sequence":                     {input: "\x1b[31mRed\x1b[0m", want: "␛[31mRed␛[0m"},
		"line feed":                             {input: "evil\nother.yaml", want: "evil␊other.yaml"},
		"carriage return":                       {input: "ok.yaml\rEVIL", want: "ok.yaml␍EVIL"},
		"tab":                                   {input: "a\tb", want: "a␉b"},
		"first and last C0 controls":            {input: "\x00\x1f", want: "␀␟"},
		"space after the C0 controls unchanged": {input: "a b", want: "a b"},
		"tilde before DEL unchanged":            {input: "a~b", want: "a~b"},
		"DEL":                                   {input: "a\x7fb", want: "a␡b"},
		"all C0 controls": {
			input: "\x00\x01\x02\x03\x04\x05\x06\x07\x08\x09\x0a\x0b\x0c\x0d\x0e\x0f" +
				"\x10\x11\x12\x13\x14\x15\x16\x17\x18\x19\x1a\x1b\x1c\x1d\x1e\x1f",
			want: "␀␁␂␃␄␅␆␇␈␉␊␋␌␍␎␏␐␑␒␓␔␕␖␗␘␙␚␛␜␝␞␟",
		},
		"C1 control":                 {input: "a\u009b2Jb", want: "a\ufffd2Jb"},
		"first and last C1 controls": {input: "\u0080\u009f", want: "\ufffd\ufffd"},
		"all C1 controls": {
			input: "\u0080\u0081\u0082\u0083\u0084\u0085\u0086\u0087\u0088\u0089\u008A\u008B\u008C\u008D\u008E\u008F" +
				"\u0090\u0091\u0092\u0093\u0094\u0095\u0096\u0097\u0098\u0099\u009A\u009B\u009C\u009D\u009E\u009F",
			want: strings.Repeat("\uFFFD", 32),
		},
		"no-break space after the C1 controls unchanged": {input: "a\u00a0b", want: "a\u00a0b"},
		"mixed C0 and C1 controls": {
			input: "\x00\u0080\x1b\u009B\x7f\u009F",
			want:  "␀\ufffd␛\ufffd␡\ufffd",
		},
		"wide runes unchanged":   {input: "日本語\x1bテスト", want: "日本語␛テスト"},
		"right-to-left override": {input: "a\u202etxt.lmay", want: "a\ufffdtxt.lmay"},
		"all embeddings and overrides": {
			input: "\u202a\u202b\u202c\u202d\u202e",
			want:  strings.Repeat("\uFFFD", 5),
		},
		"all isolates": {
			input: "\u2066\u2067\u2068\u2069",
			want:  strings.Repeat("\uFFFD", 4),
		},
		"runes next to the embeddings and isolates unchanged": {
			input: "\u2029\u202f\u2065\u206a",
			want:  "\u2029\u202f\u2065\u206a",
		},
		"direction marks unchanged": {
			input: "a\u200eb\u200fc\u061cd",
			want:  "a\u200eb\u200fc\u061cd",
		},
		"zero width space and byte order mark unchanged": {input: "a\u200bb\ufeffc", want: "a\u200bb\ufeffc"},
		"line and paragraph separators unchanged":        {input: "a b c", want: "a b c"},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := escape.Control(tc.input)
			assert.Equal(t, tc.want, got)

			// Each replacement is one rune, so the columns of the text
			// stay where they were.
			assert.Equal(t, utf8.RuneCountInString(tc.input), utf8.RuneCountInString(got))
		})
	}
}

func TestControlInvalidUTF8(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input string
		want  string
	}{
		// A lone byte of the C1 range is no valid UTF-8.
		"C1 control as one byte": {input: "a\x9b2Jb", want: "a\ufffd2Jb"},
		"each invalid byte":      {input: "a\xe2\x82b", want: "a\ufffd\ufffdb"},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, escape.Control(tc.input))
		})
	}
}

func TestRows(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input string
		want  string
	}{
		"empty string":                  {input: "", want: ""},
		"single row":                    {input: "a\x1bb", want: "a␛b"},
		"line feed kept":                {input: "a\nb", want: "a\nb"},
		"controls replaced in each row": {input: "\x1b[31m\nx\ty", want: "␛[31m\nx␉y"},
		"carriage return before line feed replaced": {input: "a\r\nb", want: "a␍\nb"},
		"leading and trailing line feeds kept":      {input: "\na\n", want: "\na\n"},
		"only line feeds kept":                      {input: "\n\n", want: "\n\n"},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, escape.Rows(tc.input))
		})
	}
}

func TestTabWidth(t *testing.T) {
	t.Parallel()

	// A lipgloss style lays a tab out at its default tab width, so Tabs
	// draws a tab as the style does.
	const text = "a\tb\n\t\tc"

	assert.Equal(t, lipgloss.NewStyle().Render(text), escape.Tabs(text))
	assert.Equal(t, strings.Repeat(" ", escape.TabWidth), lipgloss.NewStyle().Render("\t"))
}

func TestTabs(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input string
		want  string
	}{
		"empty string":                                 {input: "", want: ""},
		"text without tabs unchanged":                  {input: "a b\nc", want: "a b\nc"},
		"leading tab expands to four spaces":           {input: "\tvalidate", want: "    validate"},
		"tab after three cells expands to four spaces": {input: "abc\td", want: "abc    d"},
		"consecutive tabs expand to four spaces each":  {input: "a\t\tb", want: "a        b"},
		"other controls kept": {
			input: "\x1b[31m\t\x07\r\n\tx",
			want:  "\x1b[31m    \x07\r\n    x",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, escape.Tabs(tc.input))
		})
	}
}

func TestMessage(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input string
		want  string
	}{
		"empty string":                                 {input: "", want: ""},
		"normal text unchanged":                        {input: "a\nb", want: "a\nb"},
		"leading tab expands to four spaces":           {input: "\tvalidate", want: "    validate"},
		"tab mid-row expands to four spaces":           {input: "ab\tc", want: "ab    c"},
		"tab after four cells expands to four spaces":  {input: "abcd\te", want: "abcd    e"},
		"consecutive tabs expand to four spaces each":  {input: "a\t\tb", want: "a        b"},
		"tabs expand in each row":                      {input: "abc\n\tx", want: "abc\n    x"},
		"tab after a wide rune expands to four spaces": {input: "名\tx", want: "名    x"},
		"tab after a picture expands to four spaces":   {input: "\x1b\tx", want: "␛    x"},
		"other controls replaced":                      {input: "\x1b[31m\x07\r", want: "␛[31m␇␍"},
		"line feeds kept": {
			input: stringtest.LinesLF("", "Did you mean this?", "\tvalidate"),
			want:  stringtest.LinesLF("", "Did you mean this?", "    validate"),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, escape.Message(tc.input))
		})
	}
}

func TestLine(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input string
		want  string
	}{
		"empty string":                  {input: "", want: ""},
		"normal text unchanged":         {input: "app.yaml", want: "app.yaml"},
		"tab expands to four spaces":    {input: "a\tb", want: "a    b"},
		"line feed becomes its picture": {input: "a\nb", want: "a␊b"},
		"escape becomes its picture":    {input: "\x1b[31m", want: "␛[31m"},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, escape.Line(tc.input))
		})
	}
}
