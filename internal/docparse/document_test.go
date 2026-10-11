package docparse_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.jacobcolvin.com/niceyaml/internal/docparse"
	"go.jacobcolvin.com/niceyaml/position"
	"go.jacobcolvin.com/niceyaml/tokens"
)

func TestResult_Documents(t *testing.T) {
	t.Parallel()

	type doc struct {
		// The text of the tokens of the document.
		text      string
		span      position.Span
		fileIndex int
		preamble  int
		failure   int
	}

	tcs := map[string]struct {
		input string
		want  []doc
	}{
		"one document": {
			input: "a: 1\n",
			want: []doc{
				{text: "a: 1\n", span: position.NewSpan(0, 2), failure: -1},
			},
		},
		"an empty file": {
			input: "",
			want: []doc{
				{span: position.NewSpan(0, 1), failure: -1},
			},
		},
		"a header after a header": {
			input: "---\n---\na: 1\n",
			want: []doc{
				{text: "---", span: position.NewSpan(0, 1), preamble: 1, failure: -1},
				{text: "\n---\na: 1\n", span: position.NewSpan(1, 4), fileIndex: 1, preamble: 1, failure: -1},
			},
		},
		"a document that did not parse names its failure": {
			input: "a: 1\n---\nb: [\n",
			want: []doc{
				{text: "a: 1\n", span: position.NewSpan(0, 1), failure: -1},
				{text: "---\nb: [\n", span: position.NewSpan(1, 4), fileIndex: 1, preamble: 1},
			},
		},
		"comments above the first header fold into the document below": {
			input: "# c\n---\na: 1\n",
			want: []doc{
				{text: "# c\n---\na: 1\n", span: position.NewSpan(0, 4), fileIndex: 1, preamble: 2, failure: -1},
			},
		},
		"a comment above a header moves to the document below": {
			input: "a: 1\n# about b\n---\nb: 2\n",
			want: []doc{
				{text: "a: 1\n", span: position.NewSpan(0, 1), failure: -1},
				{text: "# about b\n---\nb: 2\n", span: position.NewSpan(1, 5), fileIndex: 1, preamble: 2, failure: -1},
			},
		},
		"a comment on the line of a marker stays with the document above": {
			input: "a: 1\n... # end\nb: 2\n",
			want: []doc{
				{text: "a: 1\n... # end\n", span: position.NewSpan(0, 2), failure: -1},
				{text: "b: 2\n", span: position.NewSpan(2, 4), fileIndex: 1, failure: -1},
			},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			tks := tokens.Tokenize(tc.input)
			r := docparse.Parse(tks)
			docs := r.Documents(tks, strings.Count(tc.input, "\n")+1)

			require.Len(t, docs, len(tc.want))

			for i, d := range docs {
				want := tc.want[i]

				var sb strings.Builder

				for _, tk := range d.Tokens {
					sb.WriteString(tk.Origin)
				}

				assert.Equal(t, want.text, sb.String(), "text of document %d", i)
				assert.Equal(t, want.span, d.Span, "span of document %d", i)
				assert.Equal(t, want.fileIndex, d.FileIndex, "file index of document %d", i)
				assert.Equal(t, want.preamble, d.Preamble, "preamble of document %d", i)
				assert.Equal(t, want.failure, d.Failure, "failure of document %d", i)
				assert.Same(t, r.File.Docs[d.FileIndex], d.Root, "root of document %d", i)
				assert.Len(t, d.Positioned, len(d.Tokens), "every token of document %d has a position", i)
			}
		})
	}
}
