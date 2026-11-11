package docparse_test

import (
	"testing"

	"github.com/goccy/go-yaml/token"
	"github.com/stretchr/testify/assert"
	"go.jacobcolvin.com/x/stringtest"

	"go.jacobcolvin.com/niceyaml/internal/docparse"
	"go.jacobcolvin.com/niceyaml/tokens"
)

func TestSize(t *testing.T) {
	t.Parallel()

	text := stringtest.LinesLF("# c", "a: 1", "---", "b: [2]")

	tcs := map[string]struct {
		tks  token.Tokens
		want int
	}{
		"text": {tks: tokens.Tokenize(text), want: len(text)},
		// The lexer repeats the line ending after a tag that ends its line.
		"tag that ends a line": {tks: tokens.Tokenize("a: !!str\n  x\n"), want: len("a: !!str\n  x\n")},
		"tag that ends a CRLF line": {
			tks:  tokens.Tokenize("a: !!str\r\n  x\r\n"),
			want: len("a: !!str\r\n  x\r\n"),
		},
		"tag before a blank line":          {tks: tokens.Tokenize("- !!str\n\n  x\n"), want: len("- !!str\n\n  x\n")},
		"text after a block scalar header": {tks: tokens.Tokenize("a: |x\n  y\n"), want: len("a: |x\n  y\n")},
		"blank line before a comment": {
			tks:  tokens.Tokenize("a: 1\n\n# c\n\n# d\n"),
			want: len("a: 1\n\n# c\n\n# d\n"),
		},
		"no tokens": {want: 0},
		"nil token": {tks: token.Tokens{nil, token.New("a", "a", nil)}, want: 1},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, docparse.Size(tc.tks))
		})
	}
}

func TestSizeError_Error(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "document is larger than 256 KiB", (&docparse.SizeError{Limit: 256 << 10}).Error())
}

func TestRuns(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		text string
		want []string
	}{
		"one document": {
			text: "a: 1\n",
			want: []string{"a: 1\n"},
		},
		"header": {
			text: "a: 1\n# c\n---\nb: 2\n",
			want: []string{"a: 1\n# c\n", "---\nb: 2\n"},
		},
		"comments below a marker": {
			text: "a: 1\n...\n# c\n---\nb: 2\n",
			want: []string{"a: 1\n...", "\n# c\n---\nb: 2\n"},
		},
		"anchor without a value": {
			text: "a: &x\n---\nb: 2\n",
			want: []string{"a: &x\n---\nb: 2\n"},
		},
		"tag before a header": {
			// The lexer repeats the line ending after the tag in the
			// header, and the run of the tag counts it.
			text: "a: !!str\n---\nb: 2\n",
			want: []string{"a: !!str\n", "---\nb: 2\n"},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			tks := tokens.Tokenize(tc.text)

			var got []string

			n := 0

			for _, run := range docparse.Runs(tks) {
				got = append(got, tc.text[n:n+run.Size])
				n += run.Size
			}

			assert.Equal(t, tc.want, got)
			assert.Equal(t, len(tc.text), n)
		})
	}
}
